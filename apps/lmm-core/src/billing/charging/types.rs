use serde::{Deserialize, Serialize};
use sqlx::PgConnection;
use std::{fmt, future::Future, pin::Pin};

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    Invalid,
    Unauthorized,
    Forbidden,
    Conflict,
    BudgetExceeded,
    NoFunds,
    NeedReservation,
    StaleWorker,
    WrongState,
    NotFound,
    Retry,
    Storage,
    Ledger,
    /// Query resolve() with the SAME request ID; never invent a new charge.
    CommitUnknown,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for Error {}
impl From<sqlx::Error> for Error {
    fn from(error: sqlx::Error) -> Self {
        match error.as_database_error().and_then(|e| e.code()).as_deref() {
            Some("40001" | "40P01" | "55P03") => Self::Retry,
            Some("23505") => Self::Conflict,
            _ => Self::Storage,
        }
    }
}
pub type Result<T> = std::result::Result<T, Error>;

#[derive(Clone, Copy, Debug, Default, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Usage {
    /// Includes cached input. Cached input is priced separately, not twice.
    pub input: i64,
    pub cached: i64,
    pub output: i64,
}
impl Usage {
    pub fn valid(self) -> bool {
        self.input >= 0 && self.cached >= 0 && self.cached <= self.input && self.output >= 0
    }
    pub fn follows(self, previous: Self) -> bool {
        self.valid()
            && self.input >= previous.input
            && self.cached >= previous.cached
            && self.output >= previous.output
    }
}

/// Supplied by the trusted Rust price/model resolver, never by client JSON.
/// Rates are integer core credits per million tokens (500,000 credits / USD).
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Price {
    pub version: String,
    pub model: String,
    pub group: String,
    pub input_per_million: i64,
    pub cached_per_million: i64,
    pub output_per_million: i64,
}
impl Price {
    pub fn validate(&self) -> Result<()> {
        if !valid_id(&self.version)
            || !valid_id(&self.model)
            || !valid_id(&self.group)
            || self.input_per_million < 0
            || self.cached_per_million < 0
            || self.output_per_million < 0
        {
            return Err(Error::Invalid);
        }
        Ok(())
    }
    pub fn cost(&self, usage: Usage) -> Result<i64> {
        self.validate()?;
        if !usage.valid() {
            return Err(Error::Invalid);
        }
        let sum = [
            (usage.input - usage.cached, self.input_per_million),
            (usage.cached, self.cached_per_million),
            (usage.output, self.output_per_million),
        ]
        .into_iter()
        .try_fold(0_i128, |total, (count, rate)| {
            total.checked_add(i128::from(count) * i128::from(rate))
        })
        .ok_or(Error::Invalid)?;
        i64::try_from(sum.checked_add(999_999).ok_or(Error::Invalid)? / 1_000_000)
            .map_err(|_| Error::Invalid)
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct Request {
    pub id: String,
    /// SHA-256 of the canonical full upstream request, including routing inputs.
    pub fingerprint: [u8; 32],
    pub price: Price,
    pub reserve: i64,
    pub lease_seconds: i64,
    pub maximum_seconds: i64,
}
impl Request {
    pub(super) fn validate(&self) -> Result<()> {
        self.price.validate()?;
        if !valid_id(&self.id)
            || self.fingerprint == [0; 32]
            || self.reserve < 0
            || !(1..=300).contains(&self.lease_seconds)
            || !(self.lease_seconds..=86_400).contains(&self.maximum_seconds)
        {
            return Err(Error::Invalid);
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "kind", rename_all = "snake_case")]
pub enum Source {
    Wallet,
    Subscription { id: i64, start: i64, end: i64 },
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Charge {
    pub id: String,
    pub actor_user_id: i64,
    pub key_id: i64,
    pub owner_account_id: i64,
    pub payer_account_id: i64,
    pub price: Price,
    pub source: Source,
    pub authorized_at: i64,
    pub lease_until: i64,
    pub hard_deadline: i64,
    pub state: String,
    pub reserved: i64,
    pub usage: Usage,
    pub settled: i64,
    pub refunded: i64,
    pub revision: i64,
    pub outcome: Option<String>,
}
#[derive(Clone, Copy, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Completed,
    Cancelled,
    Failed,
}
impl Outcome {
    pub(super) fn text(self) -> &'static str {
        match self {
            Self::Completed => "completed",
            Self::Cancelled => "cancelled",
            Self::Failed => "failed",
        }
    }
}

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(tag = "operation", rename_all = "snake_case")]
pub enum Change {
    /// Absolute ceiling, not a delta. Replays must not reserve twice.
    Reserve {
        total: i64,
    },
    /// Capture actual cost AND release unused reservation atomically.
    Settle {
        amount: i64,
    },
    Release,
    Refund {
        amount: i64,
    },
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct LedgerCommand {
    pub operation_id: String,
    pub request_id: String,
    pub actor_user_id: i64,
    pub key_id: i64,
    pub owner_account_id: i64,
    pub payer_account_id: i64,
    pub price_version: String,
    pub source: Source,
    pub change: Change,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum LedgerError {
    Insufficient,
    Conflict,
    Retry,
    Unavailable,
}
impl From<LedgerError> for Error {
    fn from(error: LedgerError) -> Self {
        match error {
            LedgerError::Insufficient => Self::NoFunds,
            LedgerError::Conflict => Self::Conflict,
            LedgerError::Retry => Self::Retry,
            LedgerError::Unavailable => Self::Ledger,
        }
    }
}
pub type LedgerFuture<'a> =
    Pin<Box<dyn Future<Output = std::result::Result<(), LedgerError>> + Send + 'a>>;

/// Task 02 integration contract. ALL changes must use `connection`, which is
/// inside the charging transaction. No COMMIT, other pool, network side effect,
/// or success-before-durability. Identical operation IDs are idempotent; changed
/// payloads conflict. Wallet effects belong exclusively to the ledger. A
/// subscription command records entitlement-funded consumption, NOT wallet cash.
/// An adapter that cannot satisfy this contract MUST NOT implement this trait.
pub trait Ledger: Send + Sync {
    fn apply<'a>(
        &'a self,
        connection: &'a mut PgConnection,
        command: LedgerCommand,
    ) -> LedgerFuture<'a>;
}

#[derive(Clone, Debug, Serialize)]
pub struct Budget {
    pub scope: String,
    pub account_id: Option<i64>,
    pub user_id: Option<i64>,
    pub key_id: Option<i64>,
    pub period: String,
    pub anchor: i64,
    pub seconds: i64,
    pub limit: i64,
}
impl Budget {
    pub(super) fn validate(&self) -> Result<()> {
        let target = match self.scope.as_str() {
            "account" => {
                self.account_id.is_some() && self.user_id.is_none() && self.key_id.is_none()
            }
            "member" => {
                self.account_id.is_some() && self.user_id.is_some() && self.key_id.is_none()
            }
            "self" => self.account_id.is_none() && self.user_id.is_some() && self.key_id.is_none(),
            "key" => self.account_id.is_none() && self.user_id.is_none() && self.key_id.is_some(),
            _ => false,
        };
        let cycle = match self.period.as_str() {
            "day" | "week" | "month" => self.anchor == 0 && self.seconds == 0,
            "custom" => {
                (-62_135_596_800..=253_402_300_799).contains(&self.anchor)
                    && (1..=315_576_000).contains(&self.seconds)
            }
            _ => false,
        };
        if !target
            || !cycle
            || self.limit < 0
            || [self.account_id, self.user_id, self.key_id]
                .into_iter()
                .flatten()
                .any(|id| id <= 0)
        {
            return Err(Error::Invalid);
        }
        Ok(())
    }
}

pub(super) fn valid_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"-_.:/".contains(&b))
}
