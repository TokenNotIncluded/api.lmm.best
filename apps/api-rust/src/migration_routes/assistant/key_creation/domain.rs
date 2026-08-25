use serde::{Deserialize, Deserializer, Serialize};

use super::super::ASSISTANT_KEY_GROUP_MAX_CHARS;
use crate::auth::DashboardDeveloperAccessPolicy;
use crate::migration_routes::missing_identity_catalog::UserGroupSelection;

pub(super) const DRAFT_VERSION: u8 = 1;

#[derive(Clone, Debug)]
pub(in crate::migration_routes::assistant) struct AuthorizationFence {
    actor_id: i64,
    actor_username: String,
    session_id: String,
    expected_session_version: i64,
    expected_user_auth_version: i64,
    developer_access_policy: DashboardDeveloperAccessPolicy,
}

impl AuthorizationFence {
    pub(super) fn capture(
        actor_id: i64,
        actor_username: &str,
        session_id: &str,
        expected_session_version: i64,
        expected_user_auth_version: i64,
        developer_access_policy: DashboardDeveloperAccessPolicy,
    ) -> Result<Self, KeyCreationError> {
        let session_id = session_id.trim();
        if actor_id <= 0
            || actor_username.trim().is_empty()
            || session_id.is_empty()
            || expected_session_version <= 0
            || expected_user_auth_version <= 0
        {
            return Err(KeyCreationError::InvalidConfirmation);
        }
        Ok(Self {
            actor_id,
            actor_username: actor_username.to_owned(),
            session_id: session_id.to_owned(),
            expected_session_version,
            expected_user_auth_version,
            developer_access_policy,
        })
    }

    pub(in crate::migration_routes::assistant) fn actor_id(&self) -> i64 {
        self.actor_id
    }

    pub(in crate::migration_routes::assistant) fn actor_username(&self) -> &str {
        &self.actor_username
    }

    pub(in crate::migration_routes::assistant) fn session_id(&self) -> &str {
        &self.session_id
    }

    pub(in crate::migration_routes::assistant) fn expected_session_version(&self) -> i64 {
        self.expected_session_version
    }

    pub(in crate::migration_routes::assistant) fn expected_user_auth_version(&self) -> i64 {
        self.expected_user_auth_version
    }

    pub(in crate::migration_routes::assistant) fn developer_access_policy(
        &self,
    ) -> DashboardDeveloperAccessPolicy {
        self.developer_access_policy
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
#[serde(transparent)]
pub(super) struct RealSelectableGroup(String);

impl RealSelectableGroup {
    pub(super) fn parse(raw: &str) -> Result<Self, KeyCreationError> {
        let group = raw.trim();
        if group.is_empty()
            || group == "auto"
            || group.chars().count() > ASSISTANT_KEY_GROUP_MAX_CHARS
        {
            return Err(KeyCreationError::InvalidGroup);
        }
        Ok(Self(group.to_owned()))
    }

    pub(super) fn as_str(&self) -> &str {
        &self.0
    }

    pub(super) fn into_inner(self) -> String {
        self.0
    }
}

impl<'de> Deserialize<'de> for RealSelectableGroup {
    fn deserialize<D>(deserializer: D) -> Result<Self, D::Error>
    where
        D: Deserializer<'de>,
    {
        let raw = String::deserialize(deserializer)?;
        Self::parse(&raw).map_err(serde::de::Error::custom)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(in crate::migration_routes::assistant) struct ConfirmationToken(String);

impl ConfirmationToken {
    pub(super) fn parse(raw: &str) -> Result<Self, KeyCreationError> {
        let token = raw.trim();
        if token.is_empty() || token.len() > 512 {
            return Err(KeyCreationError::ConfirmationRequired);
        }
        Ok(Self(token.to_owned()))
    }

    pub(super) fn expose(&self) -> &str {
        &self.0
    }
}

#[derive(Clone, Debug, Deserialize, PartialEq, Eq, Serialize)]
pub(super) struct PreparedKeyWarning {
    pub(super) enabled: bool,
    pub(super) message: String,
    pub(super) mode: String,
    pub(super) confirmations: i64,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub(in crate::migration_routes::assistant) struct PreparedKeyDraft {
    pub(super) version: u8,
    pub(super) name: String,
    pub(super) group: RealSelectableGroup,
    pub(super) conversation_id: i64,
    pub(super) warning: Option<PreparedKeyWarning>,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub(in crate::migration_routes::assistant) struct AssistantKeyGroupOption {
    pub(super) id: String,
    pub(super) description: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub(super) warning: Option<PreparedKeyWarning>,
}

impl AssistantKeyGroupOption {
    pub(in crate::migration_routes::assistant) fn selectable(
        id: impl Into<String>,
        description: impl Into<String>,
    ) -> Self {
        Self {
            id: id.into(),
            description: description.into(),
            warning: None,
        }
    }

    pub(in crate::migration_routes::assistant) fn id(&self) -> &str {
        &self.id
    }
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub(in crate::migration_routes::assistant) struct PreparedKeyAction {
    #[serde(rename = "type")]
    pub(super) kind: &'static str,
    pub(super) confirmation_token: String,
    pub(super) requires_confirmation: bool,
    pub(super) expires_in_seconds: u64,
    pub(super) name: String,
    pub(super) group: String,
    pub(super) conversation_id: i64,
    pub(super) ui_path: &'static str,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub(in crate::migration_routes::assistant) struct CreatedKey {
    pub(super) id: i64,
    pub(super) name: String,
    pub(super) group: String,
    pub(super) expired_time: i64,
    pub(super) card: SecureCardView,
    pub(super) privacy_notice: &'static str,
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub(super) struct SecureCardView {
    pub(super) id: String,
    #[serde(rename = "type")]
    pub(super) kind: &'static str,
    pub(super) summary: String,
    pub(super) created_at: i64,
    pub(super) expires_at: i64,
    pub(super) revealable: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(in crate::migration_routes::assistant) enum KeyCreationError {
    ConfirmationRequired,
    InvalidConfirmation,
    InvalidGroup,
    WarningChanged,
    TokenLimit(i64),
    TwoFactorInvalid,
    Unavailable(String),
}

impl std::fmt::Display for KeyCreationError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::ConfirmationRequired => formatter.write_str("confirmation token is required"),
            Self::InvalidConfirmation => formatter.write_str("confirmation is invalid"),
            Self::InvalidGroup => formatter.write_str("group is not selectable"),
            Self::WarningChanged => formatter.write_str("group warning changed"),
            Self::TokenLimit(limit) => write!(formatter, "token limit reached ({limit})"),
            Self::TwoFactorInvalid => formatter.write_str("two-factor code is invalid"),
            Self::Unavailable(message) => formatter.write_str(message),
        }
    }
}

pub(super) fn selectable_group_options(
    selection: UserGroupSelection,
) -> Vec<AssistantKeyGroupOption> {
    selection
        .selectable
        .into_iter()
        .filter(|(id, _)| !id.trim().is_empty() && id != "auto")
        .map(|(id, description)| AssistantKeyGroupOption {
            id,
            description,
            warning: None,
        })
        .collect()
}
