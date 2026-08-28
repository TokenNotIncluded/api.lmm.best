use super::super::key_creation::{
    AssistantKeyGroupOption, AuthorizationFence, ConfirmationToken, CreatedKey, KeyCreationError,
    PreparedKeyAction, PreparedKeyDraft, Repository,
};
use super::super::*;
use super::{TestResult, test_error};
use axum::body::to_bytes;
use secrecy::{ExposeSecret, SecretString};
use std::{
    collections::VecDeque,
    sync::{Mutex, MutexGuard},
};

use crate::auth::{
    AuthBundle, AuthError, CriticalRateLimitOutcome, DashboardSessionContext, DashboardUser,
    LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
    TwoFactorLoginRequest,
};

fn lock_recover<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    match mutex.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(in crate::migration_routes::assistant) struct FixtureResolveCall {
    pub(in crate::migration_routes::assistant) admin_user_id: i64,
    pub(in crate::migration_routes::assistant) admin_username: String,
    pub(in crate::migration_routes::assistant) lead_id: i64,
    pub(in crate::migration_routes::assistant) note: String,
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub(in crate::migration_routes::assistant) struct FixtureSubmitCall {
    pub(in crate::migration_routes::assistant) user_id: i64,
    pub(in crate::migration_routes::assistant) username: String,
    pub(in crate::migration_routes::assistant) message: String,
}

#[derive(Clone, Debug)]
pub(in crate::migration_routes::assistant) struct FixturePrepareKeyCall {
    pub(in crate::migration_routes::assistant) user_id: i64,
    pub(in crate::migration_routes::assistant) session_id: String,
    pub(in crate::migration_routes::assistant) draft: PreparedKeyDraft,
}

#[derive(Clone, Debug)]
pub(in crate::migration_routes::assistant) struct FixtureConfirmKeyCall {
    pub(in crate::migration_routes::assistant) actor_id: i64,
    pub(in crate::migration_routes::assistant) session_id: String,
    pub(in crate::migration_routes::assistant) expected_session_version: i64,
    pub(in crate::migration_routes::assistant) expected_user_auth_version: i64,
    pub(in crate::migration_routes::assistant) token: ConfirmationToken,
    pub(in crate::migration_routes::assistant) two_factor_code: String,
}

#[derive(Clone)]
pub(in crate::migration_routes::assistant) struct FixtureStore {
    pub(in crate::migration_routes::assistant) settings: AssistantSettingsView,
    pub(in crate::migration_routes::assistant) assistant_model_ids: Option<Vec<String>>,
    pub(in crate::migration_routes::assistant) latest: Option<AssistantLead>,
    pub(in crate::migration_routes::assistant) handoffs: Vec<AssistantLeadView>,
    pub(in crate::migration_routes::assistant) expected_handoff_status: &'static str,
    pub(in crate::migration_routes::assistant) summary: Vec<AssistantIntentSummary>,
    pub(in crate::migration_routes::assistant) key_group_options_result:
        Option<Result<Vec<AssistantKeyGroupOption>, String>>,
    pub(in crate::migration_routes::assistant) key_group_calls: Arc<Mutex<Vec<String>>>,
    pub(in crate::migration_routes::assistant) prepare_key_result:
        Option<Result<PreparedKeyAction, KeyCreationError>>,
    pub(in crate::migration_routes::assistant) prepare_key_calls:
        Arc<Mutex<Vec<FixturePrepareKeyCall>>>,
    pub(in crate::migration_routes::assistant) confirm_key_result:
        Option<Result<CreatedKey, KeyCreationError>>,
    pub(in crate::migration_routes::assistant) confirm_key_calls:
        Arc<Mutex<Vec<FixtureConfirmKeyCall>>>,
    pub(in crate::migration_routes::assistant) billing_result:
        Option<Result<AssistantBillingAccount, String>>,
    pub(in crate::migration_routes::assistant) intent_calls: Arc<Mutex<Vec<(i64, String)>>>,
    pub(in crate::migration_routes::assistant) cached_response: Option<AssistantCachedResponse>,
    pub(in crate::migration_routes::assistant) stored_cache:
        Arc<Mutex<Vec<(String, AssistantCachedResponse, Duration)>>>,
    pub(in crate::migration_routes::assistant) submit_result: Option<Result<AssistantLead, String>>,
    pub(in crate::migration_routes::assistant) submit_calls: Arc<Mutex<Vec<FixtureSubmitCall>>>,
    pub(in crate::migration_routes::assistant) resolve_result:
        Option<Result<AssistantLead, ResolveHandoffError>>,
    pub(in crate::migration_routes::assistant) resolve_calls: Arc<Mutex<Vec<FixtureResolveCall>>>,
    pub(in crate::migration_routes::assistant) audits: Arc<Mutex<Vec<AssistantAdminAudit>>>,
}

impl FixtureStore {
    pub(in crate::migration_routes::assistant) fn with_key_groups(
        mut self,
        options: Vec<AssistantKeyGroupOption>,
    ) -> Self {
        self.key_group_options_result = Some(Ok(options));
        self
    }

    pub(in crate::migration_routes::assistant) fn with_prepare_result(
        mut self,
        result: Result<PreparedKeyAction, KeyCreationError>,
    ) -> Self {
        self.prepare_key_result = Some(result);
        self
    }

    pub(in crate::migration_routes::assistant) fn with_confirm_result(
        mut self,
        result: Result<CreatedKey, KeyCreationError>,
    ) -> Self {
        self.confirm_key_result = Some(result);
        self
    }

    pub(in crate::migration_routes::assistant) fn prepare_key_calls(
        &self,
    ) -> Arc<Mutex<Vec<FixturePrepareKeyCall>>> {
        Arc::clone(&self.prepare_key_calls)
    }

    pub(in crate::migration_routes::assistant) fn confirm_key_calls(
        &self,
    ) -> Arc<Mutex<Vec<FixtureConfirmKeyCall>>> {
        Arc::clone(&self.confirm_key_calls)
    }
}

impl Default for FixtureStore {
    fn default() -> Self {
        Self {
            settings: AssistantSettingsView::default(),
            assistant_model_ids: None,
            latest: None,
            handoffs: Vec::new(),
            expected_handoff_status: ASSISTANT_HANDOFF_PENDING,
            summary: Vec::new(),
            key_group_options_result: None,
            key_group_calls: Arc::new(Mutex::new(Vec::new())),
            prepare_key_result: None,
            prepare_key_calls: Arc::new(Mutex::new(Vec::new())),
            confirm_key_result: None,
            confirm_key_calls: Arc::new(Mutex::new(Vec::new())),
            billing_result: None,
            intent_calls: Arc::new(Mutex::new(Vec::new())),
            cached_response: None,
            stored_cache: Arc::new(Mutex::new(Vec::new())),
            submit_result: None,
            submit_calls: Arc::new(Mutex::new(Vec::new())),
            resolve_result: None,
            resolve_calls: Arc::new(Mutex::new(Vec::new())),
            audits: Arc::new(Mutex::new(Vec::new())),
        }
    }
}

#[async_trait]
impl AssistantReadStore for FixtureStore {
    async fn settings(&self) -> Result<AssistantSettingsView, String> {
        Ok(self.settings.clone())
    }

    async fn assistant_model_ids(&self, group: &str) -> Result<Vec<String>, String> {
        if group != self.settings.group {
            return Ok(Vec::new());
        }
        Ok(self
            .assistant_model_ids
            .clone()
            .unwrap_or_else(|| vec![self.settings.model.clone()]))
    }

    async fn latest_handoff(&self, _: i64) -> Result<Option<AssistantLead>, String> {
        Ok(self.latest.clone())
    }

    async fn list_handoffs(
        &self,
        status: &str,
        limit: i64,
    ) -> Result<Vec<AssistantLeadView>, String> {
        if status != self.expected_handoff_status || limit != 100 {
            return Err("unexpected handoff query".to_owned());
        }
        Ok(self.handoffs.clone())
    }

    async fn intent_summary(&self, since: i64) -> Result<Vec<AssistantIntentSummary>, String> {
        if since <= 0 {
            return Err("invalid intent cutoff".to_owned());
        }
        Ok(self.summary.clone())
    }

    async fn billing_account(&self) -> Result<AssistantBillingAccount, String> {
        self.billing_result
            .clone()
            .unwrap_or_else(|| Err("unexpected billing account call".to_owned()))
    }

    async fn record_intent(&self, user_id: i64, intent: &str) {
        lock_recover(&self.intent_calls).push((user_id, intent.to_owned()));
    }

    async fn cached_response(&self, _: &str) -> Option<AssistantCachedResponse> {
        self.cached_response.clone()
    }

    async fn store_cached_response(
        &self,
        key: &str,
        response: &AssistantCachedResponse,
        ttl: Duration,
    ) {
        lock_recover(&self.stored_cache).push((key.to_owned(), response.clone(), ttl));
    }

    async fn submit_handoff(
        &self,
        user_id: i64,
        username: &str,
        message: &str,
    ) -> Result<AssistantLead, String> {
        lock_recover(&self.submit_calls).push(FixtureSubmitCall {
            user_id,
            username: username.to_owned(),
            message: message.to_owned(),
        });
        self.submit_result
            .clone()
            .unwrap_or_else(|| Err("unexpected submit call".to_owned()))
    }

    async fn resolve_handoff(
        &self,
        admin_user_id: i64,
        admin_username: &str,
        lead_id: i64,
        note: &str,
    ) -> Result<AssistantLead, ResolveHandoffError> {
        lock_recover(&self.resolve_calls).push(FixtureResolveCall {
            admin_user_id,
            admin_username: admin_username.to_owned(),
            lead_id,
            note: note.to_owned(),
        });
        self.resolve_result.clone().unwrap_or_else(|| {
            Err(ResolveHandoffError::Unavailable(
                "unexpected resolve call".to_owned(),
            ))
        })
    }

    async fn record_admin_audit(&self, audit: AssistantAdminAudit) {
        lock_recover(&self.audits).push(audit);
    }
}

#[derive(Clone, Copy, Default)]
pub(in crate::migration_routes::assistant) enum FixtureRateLimit {
    #[default]
    Allowed,
    Rejected(u64),
    Failed,
}

pub(in crate::migration_routes::assistant) struct FixtureAuth {
    pub(in crate::migration_routes::assistant) rate_limit: FixtureRateLimit,
}

impl Default for FixtureAuth {
    fn default() -> Self {
        Self {
            rate_limit: FixtureRateLimit::Allowed,
        }
    }
}

pub(in crate::migration_routes::assistant) struct FixtureUserRateLimiter {
    pub(in crate::migration_routes::assistant) outcome: Result<CriticalRateLimitOutcome, ()>,
    pub(in crate::migration_routes::assistant) calls: Arc<Mutex<Vec<(String, i64)>>>,
}

pub(in crate::migration_routes::assistant) struct FixtureAgentBackend {
    pub(in crate::migration_routes::assistant) responses:
        Mutex<VecDeque<Result<AssistantAgentTurnResponse, String>>>,
    pub(in crate::migration_routes::assistant) turns: Arc<Mutex<Vec<AssistantAgentTurn>>>,
}

#[async_trait]
impl AssistantAgentBackend for FixtureAgentBackend {
    async fn relay_turn(
        &self,
        turn: AssistantAgentTurn,
    ) -> Result<AssistantAgentTurnResponse, String> {
        lock_recover(&self.turns).push(turn);
        lock_recover(&self.responses)
            .pop_front()
            .unwrap_or_else(|| Err("unexpected agent turn".to_owned()))
    }
}

#[async_trait]
impl AssistantUserRateLimiter for FixtureUserRateLimiter {
    async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()> {
        lock_recover(&self.calls).push((scope.to_owned(), user_id));
        self.outcome
    }
}

#[async_trait]
impl DashboardAuth for FixtureAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        match self.rate_limit {
            FixtureRateLimit::Allowed => Ok(CriticalRateLimitOutcome::Allowed),
            FixtureRateLimit::Rejected(retry_after_seconds) => {
                Ok(CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds,
                })
            }
            FixtureRateLimit::Failed => Err(AuthError::new(AuthErrorKind::Internal)),
        }
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        panic!("unused")
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        panic!("unused")
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        panic!("unused")
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let role = match token.expose_secret() {
            "user-token" | "browser-session" => 1,
            "admin-token" | "admin-session" => ADMIN_ROLE,
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(fixture_user(role))
    }

    async fn current_session(
        &self,
        token: SecretString,
    ) -> Result<DashboardSessionContext, AuthError> {
        if !matches!(token.expose_secret(), "browser-session" | "admin-session") {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let role = if token.expose_secret() == "admin-session" {
            ADMIN_ROLE
        } else {
            1
        };
        Ok(DashboardSessionContext {
            user: fixture_user(role),
            session_id: "assistant-session".to_owned(),
            session_version: 1,
            user_auth_version: 1,
            client_ip: "127.0.0.1".to_owned(),
            user_agent: "assistant-test".to_owned(),
        })
    }

    async fn create_assistant_l1_confirmation(
        &self,
        _: i64,
        _: &str,
        _: &str,
        _: Duration,
    ) -> Result<String, AuthError> {
        Ok("assistant-confirmation-token".to_owned())
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        panic!("unused")
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        panic!("unused")
    }
}

pub(in crate::migration_routes::assistant) fn fixture_user(role: i64) -> DashboardUser {
    DashboardUser {
        id: if role >= ADMIN_ROLE { 10 } else { 7 },
        username: if role >= ADMIN_ROLE {
            "assistant-admin".to_owned()
        } else {
            "assistant-user".to_owned()
        },
        display_name: String::new(),
        role,
        status: 1,
        email: String::new(),
        github_id: String::new(),
        discord_id: String::new(),
        oidc_id: String::new(),
        wechat_id: String::new(),
        telegram_id: String::new(),
        group: "default".to_owned(),
        quota: 0,
        used_quota: 0,
        request_count: 0,
        aff_code: String::new(),
        aff_count: 0,
        aff_quota: 0,
        aff_history_quota: 0,
        inviter_id: 0,
        linux_do_id: String::new(),
        setting: "{}".to_owned(),
        stripe_customer: String::new(),
        sidebar_modules: json!({}),
        permissions: json!({}),
    }
}

pub(in crate::migration_routes::assistant) fn fixture_lead() -> AssistantLead {
    AssistantLead {
        id: 3,
        user_id: 7,
        source: ASSISTANT_HANDOFF_SOURCE.to_owned(),
        intent: "human_support".to_owned(),
        message: "Need help".to_owned(),
        status: ASSISTANT_HANDOFF_PENDING.to_owned(),
        admin_user_id: 0,
        admin_note: String::new(),
        created_at: 1_700_000_000,
        resolved_at: 0,
    }
}

#[async_trait]
impl Repository for FixtureStore {
    async fn key_group_options(
        &self,
        user_group: &str,
    ) -> Result<Vec<AssistantKeyGroupOption>, String> {
        lock_recover(&self.key_group_calls).push(user_group.to_owned());
        self.key_group_options_result
            .clone()
            .unwrap_or_else(|| Err("unexpected key group call".to_owned()))
    }

    async fn prepare_key_draft(
        &self,
        user_id: i64,
        session_id: &str,
        draft: PreparedKeyDraft,
    ) -> Result<PreparedKeyAction, KeyCreationError> {
        lock_recover(&self.prepare_key_calls).push(FixturePrepareKeyCall {
            user_id,
            session_id: session_id.to_owned(),
            draft,
        });
        self.prepare_key_result.clone().unwrap_or_else(|| {
            Err(KeyCreationError::Unavailable(
                "unexpected prepare key call".to_owned(),
            ))
        })
    }

    async fn confirm_key_draft(
        &self,
        authorization_fence: AuthorizationFence,
        token: ConfirmationToken,
        two_factor_code: &str,
    ) -> Result<CreatedKey, KeyCreationError> {
        lock_recover(&self.confirm_key_calls).push(FixtureConfirmKeyCall {
            actor_id: authorization_fence.actor_id(),
            session_id: authorization_fence.session_id().to_owned(),
            expected_session_version: authorization_fence.expected_session_version(),
            expected_user_auth_version: authorization_fence.expected_user_auth_version(),
            token,
            two_factor_code: two_factor_code.to_owned(),
        });
        self.confirm_key_result.clone().unwrap_or_else(|| {
            Err(KeyCreationError::Unavailable(
                "unexpected confirm key call".to_owned(),
            ))
        })
    }
}

pub(in crate::migration_routes::assistant) fn fixture_key_groups() -> Vec<AssistantKeyGroupOption> {
    vec![AssistantKeyGroupOption::selectable("default", "默认分组")]
}

pub(in crate::migration_routes::assistant) fn fixture_router(
    store: FixtureStore,
) -> TestResult<Router> {
    fixture_router_with_auth(store, FixtureAuth::default())
}

pub(in crate::migration_routes::assistant) fn fixture_router_with_auth(
    store: FixtureStore,
    auth: FixtureAuth,
) -> TestResult<Router> {
    fixture_router_with_dependencies(store, auth, None)
}

pub(in crate::migration_routes::assistant) fn fixture_router_with_user_rate_limiter(
    store: FixtureStore,
    limiter: Arc<dyn AssistantUserRateLimiter>,
) -> TestResult<Router> {
    fixture_router_with_dependencies(store, FixtureAuth::default(), Some(limiter))
}

pub(in crate::migration_routes::assistant) fn fixture_router_with_dependencies(
    store: FixtureStore,
    auth: FixtureAuth,
    limiter: Option<Arc<dyn AssistantUserRateLimiter>>,
) -> TestResult<Router> {
    const POSTGRES_URI: &str = "postgres://postgres@127.0.0.1:1/assistant";
    const VALKEY_URI: &str = "redis://127.0.0.1/";

    let pg = sqlx::postgres::PgPoolOptions::new()
        .connect_lazy(POSTGRES_URI)
        .map_err(|error| {
            test_error(format!(
                "parse fixture PostgreSQL URI `{POSTGRES_URI}`: {error}"
            ))
        })?;
    let valkey = redis::Client::open(VALKEY_URI).map_err(|error| {
        test_error(format!("parse fixture Valkey URI `{VALKEY_URI}`: {error}"))
    })?;
    let mut state = AssistantReadState::new(
        pg,
        valkey,
        Arc::new(auth),
        SecretString::from("assistant-test-session-secret"),
        AssistantRateLimitConfig {
            enabled: false,
            max_requests: 1,
            window: Duration::from_secs(1),
            dependency_timeout: Duration::from_secs(1),
        },
        crate::auth::DashboardDeveloperAccessPolicy::new(false),
    )
    .with_store(Arc::new(store));
    if let Some(limiter) = limiter {
        state = state.with_user_rate_limiter(limiter);
    }
    Ok(assistant_read_router(state))
}

pub(in crate::migration_routes::assistant) fn fixture_router_with_agent(
    store: FixtureStore,
    backend: Arc<dyn AssistantAgentBackend>,
) -> TestResult<Router> {
    const POSTGRES_URI: &str = "postgres://postgres@127.0.0.1:1/assistant";
    const VALKEY_URI: &str = "redis://127.0.0.1/";

    let pg = sqlx::postgres::PgPoolOptions::new()
        .connect_lazy(POSTGRES_URI)
        .map_err(|error| {
            test_error(format!(
                "parse fixture PostgreSQL URI `{POSTGRES_URI}`: {error}"
            ))
        })?;
    let valkey = redis::Client::open(VALKEY_URI).map_err(|error| {
        test_error(format!("parse fixture Valkey URI `{VALKEY_URI}`: {error}"))
    })?;
    let state = AssistantReadState::new(
        pg,
        valkey,
        Arc::new(FixtureAuth::default()),
        SecretString::from("assistant-test-session-secret"),
        AssistantRateLimitConfig {
            enabled: false,
            max_requests: 1,
            window: Duration::from_secs(1),
            dependency_timeout: Duration::from_secs(1),
        },
        crate::auth::DashboardDeveloperAccessPolicy::new(false),
    )
    .with_store(Arc::new(store))
    .with_agent_backend(backend);
    Ok(assistant_read_router(state))
}

pub(in crate::migration_routes::assistant) async fn response_json(
    response: Response,
) -> TestResult<Value> {
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .map_err(|error| test_error(format!("read assistant fixture response body: {error}")))?;
    let body = std::str::from_utf8(&bytes).map_err(|error| {
        test_error(format!(
            "decode assistant fixture response body as UTF-8: {error}"
        ))
    })?;
    serde_json::from_str(body).map_err(|error| {
        test_error(format!(
            "decode assistant fixture response body as JSON: {error}; body: {body:?}"
        ))
    })
}
