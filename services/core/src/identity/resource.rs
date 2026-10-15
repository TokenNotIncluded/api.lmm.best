//! New resource admission only. This is not a general authentication check.
//! Existing reservations, payment callbacks and refunds retain their authority.
use super::{AccountKind, CredentialKind, IdentityError, IdentityStore, Result};
use sqlx::PgConnection;

/// Each caller must name the new action being admitted. Never use this helper
/// in shared authorization, key management, read projections or settlement.
pub(crate) enum NewResourceAction {
    CommercePayment,
    PaymentIntent,
    RemoteExecution,
}

/// Caller must already hold canonical credential/user/account SHARE locks in
/// this same transaction, until the new action's durable admission commits.
/// Mark/unmark owns the user's UPDATE lock. Serializable callers also require
/// the mark writer to touch that user row so an older waiting snapshot aborts.
pub(crate) async fn reject_marked_new_action(
    connection: &mut PgConnection,
    user_id: i64,
    _purpose: NewResourceAction,
) -> Result<()> {
    let marked = crate::abuse::is_marked_in_transaction(connection, user_id)
        .await
        .map_err(|error| match error {
            crate::abuse::AbuseError::Conflict => IdentityError::Conflict,
            _ => IdentityError::Storage,
        })?;
    if marked {
        Err(IdentityError::Forbidden)
    } else {
        Ok(())
    }
}

impl IdentityStore {
    /// Public native Remote PUT/session or POST/message admission, using the
    /// public write-capable identity pool. The existing read-only Remote RPC
    /// remains a credential projection and cannot admit a new resource.
    /// A pre-mark admitted operation may finish after the mark commits.
    pub async fn admit_new_remote_execution(&self, secret: &str) -> Result<()> {
        let mut transaction = self.pool.begin().await?;
        sqlx::query("SET LOCAL lock_timeout='1s'")
            .execute(&mut *transaction)
            .await?;
        sqlx::query("SET LOCAL statement_timeout='1500ms'")
            .execute(&mut *transaction)
            .await?;
        let actor = super::authorize_in_transaction(&mut transaction, secret).await?;
        if actor.credential_kind != CredentialKind::Session
            || actor.owner.kind != AccountKind::Personal
            || actor.owner.id != actor.user_id
        {
            return Err(IdentityError::Forbidden);
        }
        reject_marked_new_action(
            &mut transaction,
            actor.user_id,
            NewResourceAction::RemoteExecution,
        )
        .await?;
        transaction.commit().await?;
        Ok(())
    }
}
