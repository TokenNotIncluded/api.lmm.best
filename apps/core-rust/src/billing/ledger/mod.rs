//! Trusted Rust-only ledger boundary. This module exposes no HTTP or gRPC route.
//!
//! Callers must authenticate and authorize the actor, payer, operation and payment
//! evidence before invoking it. Scope is supplied by the authenticated service,
//! never by an untrusted request. See README.md for the task-06 contract.
use std::fmt;

use serde::{Deserialize, Serialize};
use sqlx::{PgPool, Postgres, Transaction, types::Json};

use super::Credits;

pub const UNIT: &str = "credit_500k_usd";
/// Fresh-install definition. Applying it requires the separate schema-owner role.
pub const SCHEMA: &str = include_str!("../../../schema/ledger.sql");

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case", deny_unknown_fields)]
pub enum Action {
    Credit {
        account_id: i64,
        amount_units: Credits,
        payment_reference: String,
    },
    Transfer {
        from_account_id: i64,
        to_account_id: i64,
        amount_units: Credits,
    },
    Charge {
        account_id: i64,
        amount_units: Credits,
    },
    Reserve {
        account_id: i64,
        amount_units: Credits,
    },
    /// Finalize one reservation. Return its unused portion to its original wallet.
    Capture {
        reservation_journal_id: i64,
        amount_units: Credits,
    },
    Release {
        reservation_journal_id: i64,
    },
    /// Partial or full compensation, always to the original economic payer.
    Refund {
        journal_id: i64,
        amount_units: Credits,
    },
    /// Exact, one-time reversal of an unrefunded credit, charge or transfer.
    Reverse {
        journal_id: i64,
    },
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Request {
    pub scope: String,
    pub operation_key: String,
    pub actor_user_id: Option<i64>,
    pub reason: String,
    pub action: Action,
}

impl Request {
    pub fn validate(&self) -> Result<(), Error> {
        if !valid_key(&self.scope, 64)
            || !valid_key(&self.operation_key, 128)
            || self.reason.is_empty()
            || self.reason.chars().count() > 512
            || self.reason.contains('\0')
            || self.actor_user_id.is_some_and(|id| id <= 0)
        {
            return Err(Error::InvalidRequest);
        }
        let valid = match &self.action {
            Action::Credit {
                account_id,
                amount_units,
                payment_reference,
            } => {
                *account_id > 0
                    && positive(*amount_units)
                    && !payment_reference.is_empty()
                    && payment_reference.chars().count() <= 256
                    && !payment_reference.contains('\0')
            }
            Action::Transfer {
                from_account_id,
                to_account_id,
                amount_units,
            } => {
                *from_account_id > 0
                    && *to_account_id > 0
                    && from_account_id != to_account_id
                    && positive(*amount_units)
            }
            Action::Charge {
                account_id,
                amount_units,
            }
            | Action::Reserve {
                account_id,
                amount_units,
            } => *account_id > 0 && positive(*amount_units),
            Action::Capture {
                reservation_journal_id,
                ..
            }
            | Action::Release {
                reservation_journal_id,
            } => *reservation_journal_id > 0,
            Action::Refund {
                journal_id,
                amount_units,
            } => *journal_id > 0 && positive(*amount_units),
            Action::Reverse { journal_id } => *journal_id > 0,
        };
        if valid {
            Ok(())
        } else {
            Err(Error::InvalidRequest)
        }
    }
}

fn positive(amount: Credits) -> bool {
    i64::from(amount) > 0
}

fn valid_key(value: &str, max: usize) -> bool {
    !value.is_empty()
        && value.len() <= max
        && value
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"._:/-".contains(&c))
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Entry {
    pub ledger_account_id: i64,
    pub delta_units: i64,
    pub balance_after_units: i64,
    pub balance_revision: i64,
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Rejection {
    NotFound,
    InsufficientFunds,
    InvalidOriginal,
    ExceedsRemaining,
    AlreadyFinalized,
    AlreadyCompensated,
    AmountOverflow,
    DuplicatePayment,
}

/// Replays return this exact stored value, including the ORIGINAL balance snapshot.
/// Use balance() for current balances; do not treat a historical receipt as current.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "status", rename_all = "snake_case")]
pub enum Outcome {
    Posted {
        journal_id: i64,
        entries: Vec<Entry>,
    },
    Rejected {
        code: Rejection,
    },
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Wallet {
    /// core_identity.accounts.id, NOT a user ID or team ID.
    pub account_id: i64,
    pub wallet_id: i64,
    pub reserved_id: i64,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize, sqlx::FromRow)]
pub struct Balance {
    pub account_id: i64,
    pub available_units: i64,
    pub reserved_units: i64,
    pub available_revision: i64,
    pub reserved_revision: i64,
}

#[derive(Debug)]
pub enum Error {
    InvalidRequest,
    IdempotencyConflict,
    NotFound,
    /// May include an unknown COMMIT outcome. Retry the SAME request and key.
    Database(sqlx::Error),
}

impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::InvalidRequest => "invalid ledger request",
            Self::IdempotencyConflict => "operation key has different parameters",
            Self::NotFound => "ledger wallet not found",
            Self::Database(_) => "ledger database operation failed; retry with the same key",
        })
    }
}

impl std::error::Error for Error {
    fn source(&self) -> Option<&(dyn std::error::Error + 'static)> {
        match self {
            Self::Database(error) => Some(error),
            _ => None,
        }
    }
}

impl From<sqlx::Error> for Error {
    fn from(error: sqlx::Error) -> Self {
        let code = error.as_database_error().and_then(|e| e.code());
        match code.as_deref() {
            Some("P1000") => Self::InvalidRequest,
            Some("P1001") => Self::IdempotencyConflict,
            Some("P1002") => Self::NotFound,
            _ => Self::Database(error),
        }
    }
}

#[derive(Clone)]
pub struct Ledger {
    pool: PgPool,
}

impl Ledger {
    /// The runtime role has SELECT and the two controlled EXECUTE grants only.
    pub fn from_pool(pool: PgPool) -> Self {
        Self { pool }
    }

    async fn begin(&self) -> Result<Transaction<'_, Postgres>, Error> {
        let mut tx = self.pool.begin().await?;
        sqlx::raw_sql(
            "SET TRANSACTION ISOLATION LEVEL READ COMMITTED; \
             SET LOCAL lock_timeout = '3s'; SET LOCAL statement_timeout = '10s'; \
             SET LOCAL synchronous_commit = on;",
        )
        .execute(&mut *tx)
        .await?;
        Ok(tx)
    }

    /// Idempotent provisioning only. It never changes available or held money.
    pub async fn open_wallet(&self, account_id: i64) -> Result<Wallet, Error> {
        if account_id <= 0 {
            return Err(Error::InvalidRequest);
        }
        let mut tx = self.begin().await?;
        let Json(wallet) = sqlx::query_scalar::<_, Json<Wallet>>(
            "SELECT core_billing.open_ledger_wallet($1)",
        )
        .bind(account_id)
        .fetch_one(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(wallet)
    }

    /// One bounded transaction; never call a provider or extension while it is open.
    /// The stored result is returned only after COMMIT succeeds.
    pub async fn execute(&self, request: &Request) -> Result<Outcome, Error> {
        request.validate()?;
        let mut tx = self.begin().await?;
        let Json(outcome) = sqlx::query_scalar::<_, Json<Outcome>>(
            "SELECT core_billing.post_ledger($1)",
        )
        .bind(Json(request))
        .fetch_one(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(outcome)
    }

    /// One statement/snapshot for both buckets. Reserved money is NOT spendable.
    pub async fn balance(&self, account_id: i64) -> Result<Balance, Error> {
        if account_id <= 0 {
            return Err(Error::InvalidRequest);
        }
        sqlx::query_as::<_, Balance>(
            "SELECT w.owner_account_id AS account_id, wb.balance_units AS available_units, \
             rb.balance_units AS reserved_units, wb.revision AS available_revision, \
             rb.revision AS reserved_revision \
             FROM core_billing.ledger_accounts w \
             JOIN core_billing.balance_state wb ON wb.ledger_account_id = w.id \
             JOIN core_billing.ledger_accounts r ON r.owner_account_id = w.owner_account_id AND r.bucket = 'reserved' \
             JOIN core_billing.balance_state rb ON rb.ledger_account_id = r.id \
             WHERE w.owner_account_id = $1 AND w.bucket = 'wallet'",
        )
        .bind(account_id)
        .fetch_optional(&self.pool)
        .await?
        .ok_or(Error::NotFound)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn requests_reject_floats_negative_amounts_and_unknown_fields() {
        for amount in ["1.5", "-1", "9223372036854775808"] {
            let raw = format!(
                r#"{{"kind":"charge","account_id":1,"amount_units":{amount}}}"#
            );
            assert!(serde_json::from_str::<Action>(&raw).is_err());
        }
        assert!(serde_json::from_str::<Action>(
            r#"{"kind":"charge","account_id":1,"amount_units":1,"set_balance":999}"#
        )
        .is_err());
        assert_eq!(super::super::CREDITS_PER_USD, 500_000);
    }

    #[test]
    fn validation_rejects_zero_charges_but_allows_zero_capture() {
        let mut request = Request {
            scope: "core".into(),
            operation_key: "test:1".into(),
            actor_user_id: None,
            reason: "test".into(),
            action: Action::Charge {
                account_id: 1,
                amount_units: Credits::try_from(0).unwrap(),
            },
        };
        assert!(request.validate().is_err());
        request.action = Action::Capture {
            reservation_journal_id: 1,
            amount_units: Credits::try_from(0).unwrap(),
        };
        assert!(request.validate().is_ok());
        request.operation_key = "untrusted whitespace".into();
        assert!(request.validate().is_err());
    }
}
