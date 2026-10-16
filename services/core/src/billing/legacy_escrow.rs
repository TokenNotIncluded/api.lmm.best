//! Core-only historical bounty escrow. No HTTP, Go port or generic money action.
//! Offline imports are schema-owner SQL operations; this runtime accepts only an
//! authenticated original owner's Session. Amount and recipient are frozen at
//! import, never supplied by a payout/close caller. One claim is paid once;
//! multiple claims partially consume one project pool. Open disputes remain
//! blocked until a separately reviewed native adjudication flow exists.
//!
//! Lifecycle policy follows this project's AGPL-3.0 source revision
//! 3948a0a1ee1cefdd016a7aa45a11851545bb4b85, model/open_source_bounty.go:
//! only submitted claims can be approved; accepted/submitted, open disputes and
//! unresolved rejected challenges within seven days protect a close.

use crate::{
    accounts::AccountKind,
    identity::{CredentialKind, IdentityError},
};
use serde::{Deserialize, Serialize};
use sqlx::{PgPool, Postgres, Transaction, types::Json};
use std::{fmt, time::Duration};

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PayoutCommand {
    pub migration_id: String,
    pub source_project_id: i64,
    pub source_challenge_id: i64,
    pub operation_key: String,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CloseCommand {
    pub migration_id: String,
    pub source_project_id: i64,
    pub operation_key: String,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Receipt {
    pub status: String,
    pub code: Option<String>,
    pub operation_key: String,
    pub journal_id: Option<i64>,
    pub remaining_units: i64,
    pub revision: i64,
}

/// Serialization contract for the OFFLINE owner only. The runtime store exposes
/// no import method. A review proof hash binds independently reviewed source,
/// monetary basis and complete frozen claims; merely constructing this value is
/// not an approval of real data.
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ImportBatch {
    pub migration_id: String,
    pub source_archive_sha256: String,
    pub mapping_sha256: String,
    pub review_proof_sha256: String,
    pub unit: String,
    pub observed_at: i64,
    pub projects: Vec<ImportProject>,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ImportProject {
    pub source_project_id: i64,
    pub owner_user_id: i64,
    pub payer_account_id: i64,
    pub source_row_sha256: String,
    pub source_parent_plan_sha256: String,
    pub escrow_units: i64,
    pub status: String,
    pub claims: Vec<ImportClaim>,
    pub disputes: Vec<ImportDispute>,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ImportClaim {
    pub source_challenge_id: i64,
    pub participant_user_id: i64,
    pub recipient_account_id: i64,
    pub source_row_sha256: String,
    pub rights_status: String,
    pub source_reward_quota: i64,
    pub payout_units: Option<i64>,
    pub status: String,
    pub rejected_at: i64,
    pub paid_at: i64,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ImportDispute {
    pub source_dispute_id: i64,
    pub source_challenge_id: i64,
    pub source_row_sha256: String,
    pub status: String,
    /// Immutable source evidence. These are historical integers, NOT additional
    /// cash or necessarily in the imported project's current monetary unit.
    pub reward_quota_snapshot: i64,
    pub project_escrow_quota_snapshot: i64,
    pub tip_quota_snapshot: i64,
}

#[derive(Debug)]
pub enum Error {
    Invalid,
    Unauthorized,
    Forbidden,
    Conflict,
    Missing,
    Retry,
    Storage,
    /// A timeout/COMMIT transport failure cannot prove rollback. Reconcile or
    /// replay the exact same operation key; never invent a new payout key.
    UnknownOutcome,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str(match self {
            Self::Invalid => "invalid legacy escrow command",
            Self::Unauthorized => "legacy escrow authentication required",
            Self::Forbidden => "legacy escrow original owner Session required",
            Self::Conflict => "legacy escrow operation parameters conflict",
            Self::Missing => "legacy escrow project not found",
            Self::Retry => "legacy escrow transaction retry exhausted",
            Self::Storage => "legacy escrow storage unavailable",
            Self::UnknownOutcome => "legacy escrow outcome unknown; reconcile the original key",
        })
    }
}
impl std::error::Error for Error {}
impl From<sqlx::Error> for Error {
    fn from(error: sqlx::Error) -> Self {
        match error.as_database_error().and_then(|e| e.code()).as_deref() {
            Some("P1000") => Self::Invalid,
            Some("P1001" | "23505") => Self::Conflict,
            Some("P1002") => Self::Missing,
            Some("42501") => Self::Forbidden,
            Some("40001" | "40P01" | "55P03" | "57014") => Self::Retry,
            _ => Self::Storage,
        }
    }
}
impl From<IdentityError> for Error {
    fn from(error: IdentityError) -> Self {
        match error {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            IdentityError::Conflict => Self::Retry,
            IdentityError::Storage => Self::Storage,
        }
    }
}

#[derive(Clone)]
pub struct LegacyEscrowStore {
    pool: PgPool,
}
impl LegacyEscrowStore {
    pub fn from_pool(pool: PgPool) -> Self {
        Self { pool }
    }

    // @mark migration.bounty-custody role=implementation
    pub async fn payout(&self, secret: &str, command: &PayoutCommand) -> Result<Receipt, Error> {
        if command.source_challenge_id <= 0 {
            return Err(Error::Invalid);
        }
        self.execute(secret, serde_json::json!({"kind":"payout", "migration_id":command.migration_id,
            "source_project_id":command.source_project_id,"source_challenge_id":command.source_challenge_id,
            "operation_key":command.operation_key})).await
    }
    pub async fn close(&self, secret: &str, command: &CloseCommand) -> Result<Receipt, Error> {
        self.execute(
            secret,
            serde_json::json!({"kind":"close", "migration_id":command.migration_id,
            "source_project_id":command.source_project_id,"operation_key":command.operation_key}),
        )
        .await
    }
    async fn execute(&self, secret: &str, command: serde_json::Value) -> Result<Receipt, Error> {
        let valid = |value: &str, max: usize| {
            !value.is_empty()
                && value.len() <= max
                && value
                    .bytes()
                    .all(|b| b.is_ascii_alphanumeric() || b"._:/-".contains(&b))
        };
        if !valid(command["migration_id"].as_str().ok_or(Error::Invalid)?, 128)
            || !valid(
                command["operation_key"].as_str().ok_or(Error::Invalid)?,
                128,
            )
            || command["source_project_id"].as_i64().is_none_or(|v| v <= 0)
        {
            return Err(Error::Invalid);
        }
        tokio::time::timeout(Duration::from_secs(3), async {
            for attempt in 0..4 {
                match self.once(secret, &command).await {
                    Err(Error::Retry) if attempt < 3 => tokio::task::yield_now().await,
                    result => return result,
                }
            }
            Err(Error::Retry)
        })
        .await
        .map_err(|_| Error::UnknownOutcome)?
    }
    async fn begin(&self) -> Result<Transaction<'static, Postgres>, Error> {
        let mut tx = self.pool.begin().await?;
        for statement in [
            "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE",
            "SET LOCAL synchronous_commit=on",
            "SET LOCAL lock_timeout='250ms'",
            "SET LOCAL statement_timeout='1500ms'",
            "SET LOCAL idle_in_transaction_session_timeout='2s'",
        ] {
            sqlx::query(statement).execute(&mut *tx).await?;
        }
        Ok(tx)
    }
    async fn once(&self, secret: &str, command: &serde_json::Value) -> Result<Receipt, Error> {
        let mut tx = self.begin().await?;
        let actor = crate::identity::authorize_in_transaction(&mut tx, secret).await?;
        if actor.credential_kind != CredentialKind::Session
            || actor.owner.kind != AccountKind::Personal
            || actor.owner.id != actor.user_id
        {
            return Err(Error::Forbidden);
        }
        let owner: Option<(i64, i64)> = sqlx::query_as("SELECT owner_user_id,payer_account_id FROM core_billing.legacy_escrow_projects WHERE migration_id=$1 AND source_project_id=$2")
            .bind(command["migration_id"].as_str().ok_or(Error::Invalid)?)
            .bind(command["source_project_id"].as_i64().ok_or(Error::Invalid)?)
            .fetch_optional(&mut *tx).await?;
        if owner.ok_or(Error::Missing)? != (actor.user_id, actor.owner_account_id) {
            return Err(Error::Forbidden);
        }
        let Json(receipt): Json<Receipt> =
            sqlx::query_scalar("SELECT core_billing.legacy_escrow_finalize($1,$2,$3)")
                .bind(Json(command))
                .bind(actor.user_id)
                .bind(actor.owner_account_id)
                .fetch_one(&mut *tx)
                .await?;
        if let Err(error) = tx.commit().await {
            return Err(
                if matches!(
                    error.as_database_error().and_then(|e| e.code()).as_deref(),
                    Some("40001" | "40P01")
                ) {
                    Error::Retry
                } else {
                    Error::UnknownOutcome
                },
            );
        }
        Ok(receipt)
    }
}
