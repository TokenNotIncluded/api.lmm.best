mod domain;
mod handler;
#[cfg(test)]
mod pg_tests;
mod repository;
#[cfg(test)]
mod tests;

use async_trait::async_trait;

pub(super) use domain::{
    AssistantKeyGroupOption, AuthorizationFence, ConfirmationToken, CreatedKey, KeyCreationError,
    PreparedKeyAction, PreparedKeyDraft,
};
pub(in crate::migration_routes::assistant) use handler::{
    confirm_key_handler, prepare_key_handler, prepare_tool,
};
pub(super) use repository::{confirm_pg, load_pg_options, prepare_pg};

#[async_trait]
pub(super) trait Repository: Send + Sync {
    async fn key_group_options(
        &self,
        user_group: &str,
    ) -> Result<Vec<AssistantKeyGroupOption>, String>;

    async fn prepare_key_draft(
        &self,
        user_id: i64,
        session_id: &str,
        draft: PreparedKeyDraft,
    ) -> Result<PreparedKeyAction, KeyCreationError>;

    async fn confirm_key_draft(
        &self,
        authorization_fence: AuthorizationFence,
        token: ConfirmationToken,
        two_factor_code: &str,
    ) -> Result<CreatedKey, KeyCreationError>;
}

#[async_trait]
impl Repository for super::PgAssistantReadStore {
    async fn key_group_options(
        &self,
        user_group: &str,
    ) -> Result<Vec<AssistantKeyGroupOption>, String> {
        load_pg_options(self, user_group).await
    }

    async fn prepare_key_draft(
        &self,
        user_id: i64,
        session_id: &str,
        draft: PreparedKeyDraft,
    ) -> Result<PreparedKeyAction, KeyCreationError> {
        prepare_pg(self, user_id, session_id, draft).await
    }

    async fn confirm_key_draft(
        &self,
        authorization_fence: AuthorizationFence,
        token: ConfirmationToken,
        two_factor_code: &str,
    ) -> Result<CreatedKey, KeyCreationError> {
        confirm_pg(self, authorization_fence, token, two_factor_code).await
    }
}
