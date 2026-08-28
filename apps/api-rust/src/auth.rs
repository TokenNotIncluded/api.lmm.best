//! Legacy-compatible dashboard authentication vertical slice.

mod http;
mod postgres;
mod token;

use async_trait::async_trait;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, to_value};
use thiserror::Error;

pub use http::{
    AnonymousRequestSecurity, AuthHttpState, TurnstileCheckOutcome, TurnstileVerifier,
    anonymous_registration_surface, auth_router, turnstile_failure_response,
    turnstile_missing_response,
};
pub(crate) use postgres::dashboard_self_user_facts_in_transaction;
pub use postgres::{AuthConfig, PgValkeyDashboardAuth};
pub(crate) use token::dashboard_token_candidate;

pub const REFRESH_COOKIE_NAME: &str = "new_api_refresh";
pub const ACCESS_TOKEN_TTL_SECONDS: i64 = 15 * 60;
pub const LOGIN_SESSION_TTL_SECONDS: i64 = 30 * 24 * 60 * 60;
pub const REFRESH_REPLAY_WINDOW_SECONDS: i64 = 30;
pub const TWO_FACTOR_FLOW_TTL_SECONDS: i64 = 5 * 60;
pub const TWO_FACTOR_MAX_FAIL_ATTEMPTS: i64 = 5;
pub const TWO_FACTOR_LOCKOUT_SECONDS: i64 = 5 * 60;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum CriticalRateLimitOutcome {
    Allowed,
    Rejected { retry_after_seconds: u64 },
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AssistantL1ConfirmationError {
    Invalid,
    Internal,
}

#[derive(Clone, Debug, Deserialize)]
pub struct LoginRequest {
    pub username: String,
    pub password: SecretString,
}

#[derive(Clone, Debug, Deserialize)]
pub struct TwoFactorLoginRequest {
    pub code: SecretString,
    pub flow_token: SecretString,
}

#[derive(Clone, Debug, Serialize)]
pub struct TwoFactorChallenge {
    pub require_2fa: bool,
    pub flow_token: String,
    pub expires_at: i64,
}

#[derive(Debug)]
pub enum LoginOutcome {
    Authenticated(Box<AuthBundle>),
    TwoFactorRequired(TwoFactorChallenge),
}

#[derive(Clone, Debug, Serialize)]
pub struct LoginSessionView {
    pub sid: String,
    pub current: bool,
    pub login_method: String,
    pub ip: String,
    pub user_agent: String,
    pub created_at: i64,
    pub last_active_at: i64,
    pub expires_at: i64,
}

#[derive(Clone, Debug, Serialize)]
pub struct DashboardUser {
    pub id: i64,
    pub username: String,
    pub display_name: String,
    pub role: i64,
    pub status: i64,
    pub email: String,
    pub github_id: String,
    pub discord_id: String,
    pub oidc_id: String,
    pub wechat_id: String,
    pub telegram_id: String,
    pub group: String,
    pub quota: i64,
    pub used_quota: i64,
    pub request_count: i64,
    pub aff_code: String,
    pub aff_count: i64,
    pub aff_quota: i64,
    pub aff_history_quota: i64,
    pub inviter_id: i64,
    pub linux_do_id: String,
    pub setting: String,
    pub stripe_customer: String,
    pub sidebar_modules: Value,
    pub permissions: Value,
}

/// Server-derived identity and live session metadata for route slices that
/// must rotate the current browser session after a security change.  The
/// access token itself never exposes this context to request handlers.
#[derive(Clone, Debug)]
pub struct DashboardSessionContext {
    pub user: DashboardUser,
    pub session_id: String,
    pub session_version: i64,
    pub user_auth_version: i64,
    pub client_ip: String,
    pub user_agent: String,
}

/// Short-lived proof issued after a purpose-scoped sensitive verification.
/// The token is bound to the current dashboard session by the auth adapter;
/// callers must not construct or persist it themselves.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SecurityProof {
    pub token: String,
    pub expires_at: i64,
}

const TRUST_LEVEL_THRESHOLDS: [f64; 5] = [0.0, 0.0, 100.0, 500.0, 2_000.0];
const TRUST_LEVEL_DISCOUNT_RATIOS: [f64; 5] = [1.0, 1.0, 0.97, 0.94, 0.90];
const TRUST_LEVEL_DECAY_PERIOD_SECONDS: i64 = 90 * 24 * 60 * 60;

#[derive(Clone, Debug, Serialize, PartialEq)]
pub struct TrustLevelInfo {
    pub level: i64,
    pub automatic_level: i64,
    pub override_level: Option<i64>,
    pub paid_amount: f64,
    pub discount_ratio: f64,
    pub discount_percent: f64,
    pub next_level: Option<i64>,
    pub next_level_paid_amount: Option<f64>,
    pub amount_to_next_level: Option<f64>,
    pub next_decay_at: Option<i64>,
    pub inactivity_decay_steps: i64,
    pub decay_period_days: i64,
    pub overridden: bool,
}

#[derive(Clone, Copy, Debug, Serialize, PartialEq)]
pub struct TrustLevelTier {
    pub level: i64,
    pub min_paid_amount: f64,
    pub requires_successful_top_up: bool,
    pub discount_percent: f64,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct DashboardOnboardingView {
    pub activation_complete: bool,
    pub paid_activation_complete: bool,
    pub credential_complete: bool,
    pub first_request_complete: bool,
    pub stage: &'static str,
}

/// Safe current-user response shared by login, refresh, and `/api/user/self`.
///
/// This type contains only the dashboard-safe core projection plus derived
/// access state. Password hashes, management personal-access tokens, remarks,
/// and persisted console-activation timestamps have no representation here.
#[derive(Clone, Debug, Serialize)]
pub struct DashboardUserView {
    pub id: i64,
    pub developer_access_granted: bool,
    pub username: String,
    pub display_name: String,
    pub role: i64,
    pub status: i64,
    pub email: String,
    pub github_id: String,
    pub discord_id: String,
    pub oidc_id: String,
    pub wechat_id: String,
    pub telegram_id: String,
    pub group: String,
    pub quota: i64,
    pub used_quota: i64,
    pub request_count: i64,
    pub aff_code: String,
    pub aff_count: i64,
    pub aff_quota: i64,
    pub aff_history_quota: i64,
    pub inviter_id: i64,
    pub linux_do_id: String,
    pub setting: String,
    pub stripe_customer: String,
    pub trust_level_info: TrustLevelInfo,
    pub trust_level_tiers: [TrustLevelTier; 5],
    pub onboarding: DashboardOnboardingView,
    pub sidebar_modules: Value,
    pub permissions: Value,
}

#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct DashboardSelfUserFacts {
    pub trust_level_override: Option<i64>,
    pub paid_amount: f64,
    pub paid_activation_complete: bool,
    pub local_acceptance: bool,
    pub activity_anchor: i64,
    pub last_api_activity_at: i64,
    pub now: i64,
    pub credential_complete: bool,
}

#[derive(Clone, Copy, Debug)]
pub struct DashboardDeveloperAccessPolicy {
    local_acceptance: bool,
}

impl DashboardDeveloperAccessPolicy {
    #[must_use]
    pub fn new(local_acceptance: bool) -> Self {
        Self { local_acceptance }
    }

    pub(crate) fn local_acceptance(self) -> bool {
        self.local_acceptance
    }
}

#[derive(Clone, Copy, Debug)]
pub(crate) struct DashboardDeveloperAccessUserFacts {
    pub(crate) user_id: i64,
    pub(crate) role: i64,
    pub(crate) trust_level_override: Option<i64>,
    pub(crate) created_at: i64,
    pub(crate) last_api_activity_at: i64,
}

impl DashboardUserView {
    /// Current Go contract projection for `/api/user/login`, `/api/user/auth/refresh`
    /// and `/api/user/self` parity comparisons.
    pub(crate) fn to_legacy_go_shape(&self) -> Value {
        let mut value = match to_value(self) {
            Ok(value) => value,
            Err(_) => return Value::Null,
        };
        let Some(object) = value.as_object_mut() else {
            return value;
        };
        // These derived access/onboarding/trust fields belong to the newer
        // Rust internal projection, not the frozen Gin DTO used by the Go
        // oracle. Keep them available to policy consumers while excluding
        // them from the wire shape used for takeover parity.
        for field in [
            "developer_access_granted",
            "trust_level_info",
            "trust_level_tiers",
            "onboarding",
        ] {
            object.remove(field);
        }
        if let Some(Value::Object(permissions)) = object.get_mut("permissions") {
            permissions.remove("console_activated_at");
            permissions.remove("docs_access");
        }
        value
    }

    pub(crate) fn build(user: DashboardUser, facts: DashboardSelfUserFacts) -> Self {
        let developer_access_granted = dashboard_developer_access_granted(user.role, facts);
        let credential_complete = user.role >= 10 || facts.credential_complete;
        let first_request_complete =
            user.role >= 10 || (credential_complete && facts.last_api_activity_at > 0);
        let onboarding = DashboardOnboardingView {
            activation_complete: developer_access_granted,
            paid_activation_complete: facts.paid_activation_complete,
            credential_complete,
            first_request_complete,
            stage: onboarding_stage(
                developer_access_granted,
                credential_complete,
                first_request_complete,
            ),
        };
        let trust_level_info = evaluate_trust_level(user.role, facts);
        let mut permissions = match user.permissions {
            Value::Object(permissions) => permissions,
            _ => serde_json::Map::new(),
        };
        permissions.insert(
            "console_activated_at".to_owned(),
            Value::from(i64::from(developer_access_granted)),
        );
        permissions.insert(
            "docs_access".to_owned(),
            Value::from(developer_access_granted),
        );

        Self {
            id: user.id,
            developer_access_granted,
            username: user.username,
            display_name: user.display_name,
            role: user.role,
            status: user.status,
            email: user.email,
            github_id: user.github_id,
            discord_id: user.discord_id,
            oidc_id: user.oidc_id,
            wechat_id: user.wechat_id,
            telegram_id: user.telegram_id,
            group: user.group,
            quota: user.quota,
            used_quota: user.used_quota,
            request_count: user.request_count,
            aff_code: user.aff_code,
            aff_count: user.aff_count,
            aff_quota: user.aff_quota,
            aff_history_quota: user.aff_history_quota,
            inviter_id: user.inviter_id,
            linux_do_id: user.linux_do_id,
            setting: user.setting,
            stripe_customer: user.stripe_customer,
            trust_level_info,
            trust_level_tiers: trust_level_tiers(),
            onboarding,
            sidebar_modules: user.sidebar_modules,
            permissions: Value::Object(permissions),
        }
    }
}

/// Resolves the access bit serialized into dashboard self-user DTOs.
///
/// A present trust override is decisive, including an invalid/denying value;
/// local acceptance is only an ordinary-user fallback after that decision.
#[must_use]
pub(crate) fn dashboard_developer_access_granted(role: i64, facts: DashboardSelfUserFacts) -> bool {
    if role >= 10 {
        return true;
    }
    if let Some(level) = facts.trust_level_override {
        return (1..=4).contains(&level);
    }
    facts.paid_activation_complete || facts.local_acceptance
}

fn onboarding_stage(
    activation_complete: bool,
    credential_complete: bool,
    first_request_complete: bool,
) -> &'static str {
    if !activation_complete {
        "activate"
    } else if !credential_complete {
        "credential"
    } else if !first_request_complete {
        "first_request"
    } else {
        "complete"
    }
}

fn evaluate_trust_level(role: i64, facts: DashboardSelfUserFacts) -> TrustLevelInfo {
    if role >= 100 {
        return administrator_trust_level(6);
    }
    if role >= 10 {
        return administrator_trust_level(5);
    }

    let automatic_level = if !facts.paid_activation_complete {
        0
    } else if facts.paid_amount >= TRUST_LEVEL_THRESHOLDS[4] {
        4
    } else if facts.paid_amount >= TRUST_LEVEL_THRESHOLDS[3] {
        3
    } else if facts.paid_amount >= TRUST_LEVEL_THRESHOLDS[2] {
        2
    } else {
        1
    };
    let overridden = facts.trust_level_override.is_some();
    let (level, inactivity_decay_steps, next_decay_at) =
        if let Some(override_level) = facts.trust_level_override {
            (
                if (0..=4).contains(&override_level) {
                    override_level
                } else {
                    0
                },
                0,
                None,
            )
        } else {
            decayed_trust_level(automatic_level, facts.activity_anchor, facts.now)
        };
    let discount_ratio = TRUST_LEVEL_DISCOUNT_RATIOS[level as usize];
    let (next_level, next_level_paid_amount, amount_to_next_level) = if automatic_level < 4 {
        let next = automatic_level + 1;
        let threshold = TRUST_LEVEL_THRESHOLDS[next as usize];
        (
            Some(next),
            Some(threshold),
            Some((threshold - facts.paid_amount).max(0.0)),
        )
    } else {
        (None, None, None)
    };

    TrustLevelInfo {
        level,
        automatic_level,
        override_level: facts.trust_level_override,
        paid_amount: facts.paid_amount,
        discount_ratio,
        discount_percent: (1.0 - discount_ratio) * 100.0,
        next_level,
        next_level_paid_amount,
        amount_to_next_level,
        next_decay_at,
        inactivity_decay_steps,
        decay_period_days: 90,
        overridden,
    }
}

fn decayed_trust_level(
    automatic_level: i64,
    activity_anchor: i64,
    now: i64,
) -> (i64, i64, Option<i64>) {
    if automatic_level == 0 || activity_anchor <= 0 || now <= activity_anchor {
        return (automatic_level, 0, None);
    }
    let decay_steps = ((now - activity_anchor) / TRUST_LEVEL_DECAY_PERIOD_SECONDS)
        .min(automatic_level.saturating_sub(1));
    let level = automatic_level - decay_steps;
    let next_decay_at = (level > 1)
        .then_some(activity_anchor + (decay_steps + 1) * TRUST_LEVEL_DECAY_PERIOD_SECONDS);
    (level, decay_steps, next_decay_at)
}

fn administrator_trust_level(level: i64) -> TrustLevelInfo {
    TrustLevelInfo {
        level,
        automatic_level: level,
        override_level: None,
        paid_amount: 0.0,
        discount_ratio: TRUST_LEVEL_DISCOUNT_RATIOS[4],
        discount_percent: (1.0 - TRUST_LEVEL_DISCOUNT_RATIOS[4]) * 100.0,
        next_level: None,
        next_level_paid_amount: None,
        amount_to_next_level: None,
        next_decay_at: None,
        inactivity_decay_steps: 0,
        decay_period_days: 90,
        overridden: false,
    }
}

fn trust_level_tiers() -> [TrustLevelTier; 5] {
    std::array::from_fn(|level| TrustLevelTier {
        level: level as i64,
        min_paid_amount: TRUST_LEVEL_THRESHOLDS[level],
        requires_successful_top_up: level == 1,
        discount_percent: (1.0 - TRUST_LEVEL_DISCOUNT_RATIOS[level]) * 100.0,
    })
}

#[cfg(test)]
mod dashboard_user_view_tests {
    use super::*;
    use serde_json::json;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn dashboard_user(role: i64) -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "alice".to_owned(),
            display_name: "Alice".to_owned(),
            role,
            status: 1,
            email: "alice@example.test".to_owned(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 10,
            used_quota: 2,
            request_count: 1,
            aff_code: "ABCD".to_owned(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: json!(""),
            permissions: json!({
                "admin_permissions": {"channel": {"read": false}},
                "console_activated_at": 1_725_000_000_i64
            }),
        }
    }

    #[test]
    fn self_user_view_normalizes_access_from_current_facts() {
        let view = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                paid_amount: 500.0,
                paid_activation_complete: true,
                activity_anchor: 1_700_000_000,
                last_api_activity_at: 1_700_000_000,
                now: 1_700_000_001,
                credential_complete: true,
                ..DashboardSelfUserFacts::default()
            },
        );

        assert!(view.developer_access_granted);
        assert_eq!(view.trust_level_info.level, 3);
        assert_eq!(view.onboarding.stage, "complete");
        assert_eq!(view.permissions["console_activated_at"], 1);
        assert_eq!(view.permissions["docs_access"], true);
    }

    #[test]
    fn self_user_view_fails_closed_and_excludes_secrets() -> TestResult {
        let value = serde_json::to_value(DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts::default(),
        ))?;

        assert_eq!(value["developer_access_granted"], false);
        assert_eq!(value["permissions"]["console_activated_at"], 0);
        assert_eq!(value["permissions"]["docs_access"], false);
        assert_eq!(value["onboarding"]["stage"], "activate");
        assert!(value.get("password").is_none());
        assert!(value.get("access_token").is_none());
        assert!(value.get("remark").is_none());
        Ok(())
    }

    #[test]
    fn legacy_go_shape_matches_frozen_dashboard_user_fields() {
        let value = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                paid_amount: 500.0,
                paid_activation_complete: true,
                credential_complete: true,
                ..DashboardSelfUserFacts::default()
            },
        )
        .to_legacy_go_shape();

        assert!(value.get("developer_access_granted").is_none());
        assert!(value.get("trust_level_info").is_none());
        assert!(value.get("trust_level_tiers").is_none());
        assert!(value.get("onboarding").is_none());
        assert!(value["permissions"].get("console_activated_at").is_none());
        assert!(value["permissions"].get("docs_access").is_none());
        assert_eq!(value["username"], "alice");
        assert_eq!(value["id"], 7);
    }

    #[test]
    fn self_user_view_applies_local_acceptance_only_when_enabled() {
        let disabled = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                local_acceptance: false,
                ..DashboardSelfUserFacts::default()
            },
        );
        assert!(!disabled.developer_access_granted);

        let enabled = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                local_acceptance: true,
                ..DashboardSelfUserFacts::default()
            },
        );
        assert!(enabled.developer_access_granted);
    }

    #[test]
    fn self_user_view_trust_override_denial_beats_local_acceptance() {
        let view = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                trust_level_override: Some(99),
                local_acceptance: true,
                ..DashboardSelfUserFacts::default()
            },
        );
        assert!(!view.developer_access_granted);
    }

    #[test]
    fn self_user_view_uses_explicit_role_and_override_decisions() {
        let admin = DashboardUserView::build(dashboard_user(10), DashboardSelfUserFacts::default());
        assert!(admin.developer_access_granted);
        assert_eq!(admin.trust_level_info.level, 5);

        let denied = DashboardUserView::build(
            dashboard_user(1),
            DashboardSelfUserFacts {
                trust_level_override: Some(99),
                paid_activation_complete: true,
                ..DashboardSelfUserFacts::default()
            },
        );
        assert!(!denied.developer_access_granted);
        assert_eq!(denied.trust_level_info.level, 0);
    }
}

/// The three server-derived failures emitted by Go's `middleware.UserAuth`.
///
/// Keep this check centralized: migration slices must not turn a disabled,
/// guest, or malformed dashboard principal into a generic invalid-token
/// response.  The order is observable legacy behaviour.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum UserAuthPolicyError {
    UserDisabled,
    InsufficientPrivilege,
    InvalidUserInfo,
}

/// Applies the legacy `UserAuth` policy after a dashboard credential has been
/// resolved by [`DashboardAuth`].  The caller is responsible for translating
/// the error into its route's legacy response envelope.
pub fn enforce_user_auth(user: &DashboardUser) -> Result<(), UserAuthPolicyError> {
    enforce_user_auth_fields(user.id, &user.username, user.role, user.status)
}

pub fn enforce_user_auth_view(user: &DashboardUserView) -> Result<(), UserAuthPolicyError> {
    enforce_user_auth_fields(user.id, &user.username, user.role, user.status)
}

fn enforce_user_auth_fields(
    id: i64,
    username: &str,
    role: i64,
    status: i64,
) -> Result<(), UserAuthPolicyError> {
    if status != 1 {
        return Err(UserAuthPolicyError::UserDisabled);
    }
    if role < 1 {
        return Err(UserAuthPolicyError::InsufficientPrivilege);
    }
    if id <= 0 || username.trim().is_empty() || !matches!(role, 0 | 1 | 10 | 100) {
        return Err(UserAuthPolicyError::InvalidUserInfo);
    }
    Ok(())
}

#[must_use]
pub fn user_auth_status(error: UserAuthPolicyError) -> u16 {
    match error {
        UserAuthPolicyError::InsufficientPrivilege => 403,
        UserAuthPolicyError::UserDisabled | UserAuthPolicyError::InvalidUserInfo => 401,
    }
}

/// Legacy localized text for a post-session `UserAuth` policy rejection.
/// Accept-Language only distinguishes English, Simplified Chinese, and
/// Traditional Chinese, exactly as the migrated dashboard routes do.
#[must_use]
pub fn user_auth_message(
    error: UserAuthPolicyError,
    accept_language: Option<&str>,
) -> &'static str {
    let language = accept_language
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    let traditional = language.starts_with("zh-tw");
    let chinese = language.starts_with("zh");
    match (error, traditional, chinese) {
        (UserAuthPolicyError::UserDisabled, true, _) => "使用者已被封禁",
        (UserAuthPolicyError::UserDisabled, false, true) => "用户已被封禁",
        (UserAuthPolicyError::UserDisabled, false, false) => "User has been banned",
        (UserAuthPolicyError::InsufficientPrivilege, true, _) => "無權進行此操作，權限不足",
        (UserAuthPolicyError::InsufficientPrivilege, false, true) => "无权进行此操作，权限不足",
        (UserAuthPolicyError::InsufficientPrivilege, false, false) => {
            "Unauthorized, insufficient privileges"
        }
        (UserAuthPolicyError::InvalidUserInfo, true, _) => "無權進行此操作，使用者資訊無效",
        (UserAuthPolicyError::InvalidUserInfo, false, true) => "无权进行此操作，用户信息无效",
        (UserAuthPolicyError::InvalidUserInfo, false, false) => "Unauthorized, invalid user info",
    }
}

#[derive(Clone, Debug, Serialize)]
pub struct AuthResponseData {
    pub access_token: String,
    pub token_type: &'static str,
    pub access_expires_at: i64,
    pub session: LoginSessionView,
    pub user: DashboardUserView,
}

#[derive(Debug)]
pub struct AuthBundle {
    pub data: AuthResponseData,
    pub refresh_token: SecretString,
}

#[derive(Clone, Debug)]
pub struct RequestMetadata {
    pub ip: String,
    pub user_agent: String,
}

/// Server-authenticated session context for a sensitive account change.
///
/// This is deliberately separate from request JSON: an edge authentication
/// middleware must derive the SID from a validated access token and derive the
/// metadata from the trusted connection context before invoking a sensitive
/// route.  Callers must never construct it from client supplied body fields.
#[derive(Clone, Debug)]
pub struct SecuritySessionRotationRequest {
    pub user_id: i64,
    pub session_id: String,
    pub auth_version: i64,
    pub metadata: RequestMetadata,
}

#[derive(Clone, Debug)]
pub struct LogoutRequest {
    pub access_token: Option<SecretString>,
    pub refresh_token: Option<SecretString>,
    pub expected_sid: Option<String>,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct LogoutResult {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub revoked_sid: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub cookie_cleared: Option<bool>,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AuthErrorKind {
    InvalidCredentials,
    InvalidRequest,
    TwoFactorRequired,
    TwoFactorFlowExpired,
    InvalidTwoFactorCode,
    TwoFactorLocked,
    TwoFactorUnavailable,
    PasswordLoginDisabled,
    OriginForbidden,
    SessionLimit,
    SessionIssuanceLimit,
    SessionMismatch,
    RefreshRace,
    TokenExpired,
    SessionRevoked,
    /// An opaque dashboard personal-access-token owner is disabled.
    /// Session-backed credentials instead use [`Self::SessionRevoked`], which
    /// matches Go's session validation contract.
    UserDisabled,
    Unauthorized,
    Internal,
}

#[derive(Debug, Error)]
#[error("dashboard authentication failed: {kind:?}")]
pub struct AuthError {
    pub kind: AuthErrorKind,
    /// A legacy controller error that must be rendered verbatim by the one
    /// compatibility route that historically exposed database write errors.
    ///
    /// This is deliberately opt-in: ordinary authentication failures retain
    /// their stable, localized public envelopes.
    legacy_response_message: Option<String>,
}

impl AuthError {
    pub const fn new(kind: AuthErrorKind) -> Self {
        Self {
            kind,
            legacy_response_message: None,
        }
    }

    /// Preserves the historical `/api/user/token` controller error body for a
    /// failed personal-access-token write.  Do not use this for general auth
    /// failures: Go only exposes this detail from that legacy controller.
    #[must_use]
    pub fn with_legacy_response_message(kind: AuthErrorKind, message: String) -> Self {
        Self {
            kind,
            legacy_response_message: Some(message),
        }
    }

    #[must_use]
    pub fn legacy_response_message(&self) -> Option<&str> {
        self.legacy_response_message.as_deref()
    }
}

#[async_trait]
pub trait DashboardAuth: Send + Sync {
    async fn check_critical_rate_limit(
        &self,
        client_ip: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError>;

    async fn login(
        &self,
        request: LoginRequest,
        metadata: RequestMetadata,
    ) -> Result<LoginOutcome, AuthError>;

    async fn login_2fa(
        &self,
        request: TwoFactorLoginRequest,
        metadata: RequestMetadata,
    ) -> Result<AuthBundle, AuthError>;

    async fn refresh(
        &self,
        refresh_token: SecretString,
        expected_sid: Option<String>,
        metadata: RequestMetadata,
    ) -> Result<AuthBundle, AuthError>;

    async fn self_user(&self, access_token: SecretString) -> Result<DashboardUser, AuthError>;

    /// Resolves a live browser session, including the server-owned SID and
    /// request metadata needed by sensitive account routes. Personal access
    /// tokens and adapters without a session authority fail closed.
    async fn current_session(
        &self,
        _access_token: SecretString,
    ) -> Result<DashboardSessionContext, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    /// Issues a Go-compatible security proof for an already authenticated
    /// browser session. Implementations must revalidate the durable session
    /// and user auth version before signing; adapters without that authority
    /// fail closed.
    async fn issue_security_proof(
        &self,
        _user_id: i64,
        _session_id: &str,
        _method: &str,
        _scopes: &[String],
    ) -> Result<SecurityProof, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    /// Validates a security proof against the current durable browser session.
    /// Implementations without the shared session authority fail closed.
    async fn verify_security_proof(
        &self,
        _raw: SecretString,
        _user_id: i64,
        _session_id: &str,
        _required_scope: &str,
        _allowed_methods: &[String],
    ) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    /// Creates the short-lived, session-bound confirmation used by the
    /// assistant's L1 recommendation UI. Adapters without the durable auth
    /// flow authority fail closed.
    async fn create_assistant_l1_confirmation(
        &self,
        _user_id: i64,
        _session_id: &str,
        _payload: &str,
        _ttl: std::time::Duration,
    ) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    /// Atomically consumes a session-bound assistant L1 confirmation and
    /// returns its server-owned draft payload.
    async fn consume_assistant_l1_confirmation(
        &self,
        _user_id: i64,
        _session_id: &str,
        _token: SecretString,
    ) -> Result<String, AssistantL1ConfirmationError> {
        Err(AssistantL1ConfirmationError::Internal)
    }

    /// Resolves a dashboard credential before the route-specific `UserAuth`
    /// policy is applied.  Optional Go `TryUserAuth` consumers need the
    /// server-derived user context even when that policy would reject it;
    /// required routes continue to call [`enforce_user_auth`] themselves.
    async fn self_user_for_optional(
        &self,
        access_token: SecretString,
    ) -> Result<DashboardUser, AuthError> {
        self.self_user(access_token).await
    }

    /// Builds the serialized self-user response after resolving an optional
    /// dashboard principal. Adapters with authoritative trust/payment state
    /// override this; the default keeps every optional access field closed.
    async fn self_user_view_for_optional(
        &self,
        access_token: SecretString,
    ) -> Result<DashboardUserView, AuthError> {
        let user = self.self_user_for_optional(access_token).await?;
        Ok(DashboardUserView::build(
            user,
            DashboardSelfUserFacts::default(),
        ))
    }

    async fn logout(&self, request: LogoutRequest) -> Result<LogoutResult, AuthError>;

    async fn generate_personal_access_token(
        &self,
        access_token: SecretString,
    ) -> Result<String, AuthError>;
}

/// Auditable source-to-module mapping for the four-route migration slice.
pub const LEGACY_AUTH_SOURCE_MAP: &[(&str, &str)] = &[
    ("controller/user.go", "auth/http.rs + auth/postgres.rs"),
    (
        "controller/auth_session.go",
        "auth/http.rs + auth/postgres.rs",
    ),
    ("middleware/auth.go", "auth/http.rs + auth/token.rs"),
    ("service/auth_token.go", "auth/token.rs"),
    ("service/auth_session.go", "auth/postgres.rs"),
    ("model/user.go", "auth/postgres.rs"),
    ("model/user_auth_cache.go", "auth/postgres.rs"),
    ("model/user_session.go", "auth/postgres.rs"),
    ("router/api-router.go", "auth/http.rs"),
];
