//! Legacy-compatible security email verification delivery.
//!
//! Codes are scoped to sensitive-action verification and are removed when
//! outbound delivery fails. The default mail adapter is deliberately disabled:
//! production must inject a real SMTP-capable sender before this route can
//! report success. Code persistence is also disabled by default until the
//! embedding listener wires the matching consumer and scoped-proof issuer.

use std::{
    collections::{HashMap, VecDeque},
    sync::{Arc, Mutex},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use lettre::{
    AsyncSmtpTransport, AsyncTransport, Message, Tokio1Executor,
    message::{Mailbox, SinglePart},
    transport::smtp::{
        authentication::{Credentials, Mechanism},
        client::{Tls, TlsParameters},
    },
};
use secrecy::SecretString;
use serde_json::json;
use sqlx::PgPool;

use crate::{
    ClientIpKey, RequestContext,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, UserAuthPolicyError,
        dashboard_token_candidate, user_auth_message, user_auth_status,
    },
    legacy_empty_response,
};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const SECURITY_EMAIL_PURPOSE: &str = "s";
const VERIFICATION_TTL: Duration = Duration::from_secs(10 * 60);
const EMAIL_RATE_LIMIT_MAX: usize = 2;
const EMAIL_RATE_LIMIT_WINDOW: Duration = Duration::from_secs(30);
const RATE_LIMIT_CLEANUP_INTERVAL: Duration = Duration::from_secs(20 * 60);
const ADMIN_ROLE: i64 = 10;
const MINIMUM_TRUST_LEVEL: i64 = 1;

/// Dependencies for `POST /api/verify/email`.
#[derive(Clone)]
pub struct VerifyEmailState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
    identities: Arc<dyn VerifyEmailIdentityResolver>,
    codes: Arc<dyn VerificationCodeStore>,
    email_rate_limiter: Arc<dyn EmailVerificationRateLimiter>,
    mailer: Arc<dyn SecurityEmailSender>,
    system_name_override: Option<Arc<str>>,
}

impl VerifyEmailState {
    /// Creates a production state whose SMTP and verification-consumer
    /// boundaries fail closed until compatible implementations are injected.
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, auth: Arc<dyn DashboardAuth>) -> Self {
        let dependency_timeout = Duration::from_secs(2);
        Self {
            identities: Arc::new(DashboardVerifyEmailIdentityResolver {
                pg: pg.clone(),
                auth: Arc::clone(&auth),
            }),
            // Candidate and test embeddings must explicitly opt into the
            // shared store only when their `/api/verify` consumer is wired.
            // The normal listener supplies that override below; the default
            // remains fail-closed so an isolated router cannot send an
            // unusable code merely because SMTP is configured.
            codes: Arc::new(DisabledVerificationCodeStore),
            email_rate_limiter: Arc::new(ValkeyMemoryEmailRateLimiter {
                valkey,
                memory: Mutex::new(MemoryRateLimitState::default()),
                dependency_timeout,
            }),
            pg,
            auth,
            mailer: Arc::new(DisabledSecurityEmailSender),
            system_name_override: None,
        }
    }

    /// Replaces the fail-closed mail adapter with an SMTP-capable sender.
    #[must_use]
    pub fn with_mailer(mut self, mailer: Arc<dyn SecurityEmailSender>) -> Self {
        self.mailer = mailer;
        self
    }

    /// Replaces identity resolution for deterministic route tests.
    #[must_use]
    pub fn with_identity_resolver(
        mut self,
        identities: Arc<dyn VerifyEmailIdentityResolver>,
    ) -> Self {
        self.identities = identities;
        self
    }

    /// Replaces verification-code persistence after the embedding listener
    /// has also wired a compatible verifier and scoped-proof issuer.
    ///
    /// Injecting storage alone is insufficient for production: the matching
    /// `/api/verify` path must atomically consume the same purpose-scoped code.
    #[must_use]
    pub fn with_code_store(mut self, codes: Arc<dyn VerificationCodeStore>) -> Self {
        self.codes = codes;
        self
    }

    /// Replaces the dedicated email limiter for deterministic route tests.
    #[must_use]
    pub fn with_email_rate_limiter(
        mut self,
        limiter: Arc<dyn EmailVerificationRateLimiter>,
    ) -> Self {
        self.email_rate_limiter = limiter;
        self
    }

    /// Overrides the operator-configured system name for tests or embedding.
    #[must_use]
    pub fn with_system_name(mut self, system_name: impl Into<String>) -> Self {
        self.system_name_override = Some(Arc::from(system_name.into()));
        self
    }

    async fn system_name(&self) -> Result<String, VerifyEmailDependencyError> {
        if let Some(system_name) = self.system_name_override.as_deref() {
            return Ok(system_name.to_owned());
        }
        sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = 'SystemName'")
            .fetch_optional(&self.pg)
            .await
            .map(|value| value.unwrap_or_else(|| "LMM API".to_owned()))
            .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))
    }
}

/// Builds the current-only security email verification route.
///
/// The normal listener must wrap this router in its existing global API rate
/// limiter. Console access, UserAuth, CT, EV, and DisableCache ordering is
/// enforced inside the route.
pub fn router(state: VerifyEmailState) -> Router {
    Router::new()
        .route("/api/verify/email", post(send_security_email))
        .with_state(state)
}

/// Authenticated dashboard identity needed by the mail controller.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VerifyEmailIdentity {
    pub id: i64,
    pub username: String,
    pub role: i64,
    pub status: i64,
    pub email: String,
    pub developer_access_granted: bool,
}

/// Authentication and console-discovery failures at the route boundary.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum VerifyEmailAuthError {
    ConsoleHidden,
    TokenExpired,
    SessionRevoked,
    UserDisabled,
    Unauthorized,
    Internal,
}

/// Injectable authority that preserves ConsoleAccessGate before UserAuth.
#[async_trait]
pub trait VerifyEmailIdentityResolver: Send + Sync {
    async fn resolve(
        &self,
        headers: &HeaderMap,
    ) -> Result<VerifyEmailIdentity, VerifyEmailAuthError>;
}

/// Error exposed by injectable mail, code-store, and limiter boundaries.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct VerifyEmailDependencyError(String);

impl VerifyEmailDependencyError {
    #[must_use]
    pub fn new(message: impl Into<String>) -> Self {
        Self(message.into())
    }
}

/// Outbound HTML email produced by the legacy controller.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct SecurityEmailMessage {
    pub from_name: String,
    pub subject: String,
    pub recipient: String,
    pub html: String,
}

/// SMTP-capable outbound boundary. Implementations report success only after
/// the remote mail service has accepted the message.
#[async_trait]
pub trait SecurityEmailSender: Send + Sync {
    async fn send(&self, message: &SecurityEmailMessage) -> Result<(), VerifyEmailDependencyError>;
}

/// Default sender used when the Rust process has no configured SMTP adapter.
pub struct DisabledSecurityEmailSender;

#[async_trait]
impl SecurityEmailSender for DisabledSecurityEmailSender {
    async fn send(&self, _: &SecurityEmailMessage) -> Result<(), VerifyEmailDependencyError> {
        Err(VerifyEmailDependencyError::new("SMTP 服务器未配置"))
    }
}

/// PostgreSQL-configured async SMTP sender for the normal listener.
///
/// All SMTP options are loaded for every message so administrator changes take
/// effect without a process restart. Invalid or missing configuration fails
/// before any delivery success can be reported.
pub struct PgSmtpSecurityEmailSender {
    pg: PgPool,
    timeout: Duration,
}

impl PgSmtpSecurityEmailSender {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self {
            pg,
            timeout: Duration::from_secs(30),
        }
    }

    #[must_use]
    pub fn with_timeout(mut self, timeout: Duration) -> Self {
        self.timeout = timeout;
        self
    }

    async fn config(&self) -> Result<SmtpConfig, VerifyEmailDependencyError> {
        let options = sqlx::query_as::<_, (String, String)>(
            r#"SELECT key, value
               FROM options
               WHERE key IN (
                   'SMTPServer', 'SMTPPort', 'SMTPFrom', 'SMTPAccount', 'SMTPToken',
                   'SMTPSSLEnabled', 'SMTPStartTLSEnabled',
                   'SMTPInsecureSkipVerify', 'SMTPForceAuthLogin'
               )"#,
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))?
        .into_iter()
        .collect::<HashMap<_, _>>();
        SmtpConfig::from_options(&options)
    }
}

#[async_trait]
impl SecurityEmailSender for PgSmtpSecurityEmailSender {
    async fn send(&self, message: &SecurityEmailMessage) -> Result<(), VerifyEmailDependencyError> {
        let config = self.config().await?;
        let email = build_smtp_message(&config, message)?;
        let transport = smtp_transport(&config, self.timeout)?;
        tokio::time::timeout(self.timeout, transport.send(email))
            .await
            .map_err(|_| VerifyEmailDependencyError::new("SMTP delivery timed out"))?
            .map(|_| ())
            .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum SmtpTlsMode {
    Plaintext,
    RequiredStartTls,
    ImplicitTls,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct SmtpConfig {
    server: String,
    port: u16,
    from: String,
    account: String,
    token: String,
    tls_mode: SmtpTlsMode,
    insecure_skip_verify: bool,
    force_auth_login: bool,
}

impl SmtpConfig {
    fn from_options(options: &HashMap<String, String>) -> Result<Self, VerifyEmailDependencyError> {
        let value = |key: &str| options.get(key).map_or("", String::as_str);
        let server = value("SMTPServer").to_owned();
        let account = value("SMTPAccount").to_owned();
        if server.is_empty() && account.is_empty() {
            return Err(VerifyEmailDependencyError::new("SMTP 服务器未配置"));
        }
        let port = value("SMTPPort")
            .trim()
            .parse::<u16>()
            .ok()
            .filter(|port| *port > 0)
            .unwrap_or(587);
        let ssl_enabled = option_bool(value("SMTPSSLEnabled"));
        let starttls_enabled = option_bool(value("SMTPStartTLSEnabled"));
        let tls_mode = if ssl_enabled || (port == 465 && !starttls_enabled) {
            SmtpTlsMode::ImplicitTls
        } else if starttls_enabled {
            SmtpTlsMode::RequiredStartTls
        } else {
            SmtpTlsMode::Plaintext
        };
        let configured_from = value("SMTPFrom");
        let from = if configured_from.is_empty() {
            account.clone()
        } else {
            configured_from.to_owned()
        };
        if from.is_empty() {
            return Err(VerifyEmailDependencyError::new("invalid SMTP sender"));
        }
        Ok(Self {
            server,
            port,
            from,
            account,
            token: value("SMTPToken").to_owned(),
            tls_mode,
            insecure_skip_verify: option_bool(value("SMTPInsecureSkipVerify")),
            force_auth_login: option_bool(value("SMTPForceAuthLogin")),
        })
    }

    fn auth_mechanisms(&self) -> Option<Vec<Mechanism>> {
        if self.account.is_empty() || self.token.is_empty() {
            return None;
        }
        if self.force_auth_login
            || self.account.contains("outlook")
            || self.account.contains("onmicrosoft")
            || matches!(
                self.server.as_str(),
                "smtp.sendcloud.net" | "smtp.azurecomm.net"
            )
        {
            Some(vec![Mechanism::Login])
        } else {
            Some(vec![Mechanism::Plain, Mechanism::Login])
        }
    }
}

fn option_bool(value: &str) -> bool {
    value.trim().eq_ignore_ascii_case("true")
}

fn smtp_transport(
    config: &SmtpConfig,
    timeout: Duration,
) -> Result<AsyncSmtpTransport<Tokio1Executor>, VerifyEmailDependencyError> {
    let tls = match config.tls_mode {
        SmtpTlsMode::Plaintext => Tls::None,
        SmtpTlsMode::RequiredStartTls => Tls::Required(tls_parameters(config)?),
        SmtpTlsMode::ImplicitTls => Tls::Wrapper(tls_parameters(config)?),
    };
    let mut builder = AsyncSmtpTransport::<Tokio1Executor>::builder_dangerous(&config.server)
        .port(config.port)
        .timeout(Some(timeout))
        .tls(tls);
    if let Some(mechanisms) = config.auth_mechanisms() {
        builder = builder
            .credentials(Credentials::new(
                config.account.clone(),
                config.token.clone(),
            ))
            .authentication(mechanisms);
    }
    Ok(builder.build())
}

fn tls_parameters(config: &SmtpConfig) -> Result<TlsParameters, VerifyEmailDependencyError> {
    TlsParameters::builder(config.server.clone())
        .dangerous_accept_invalid_certs(config.insecure_skip_verify)
        .dangerous_accept_invalid_hostnames(config.insecure_skip_verify)
        .build()
        .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))
}

fn build_smtp_message(
    config: &SmtpConfig,
    message: &SecurityEmailMessage,
) -> Result<Message, VerifyEmailDependencyError> {
    let parsed_sender = config.from.parse::<Mailbox>().map_err(|error| {
        VerifyEmailDependencyError::new(format!("invalid SMTP sender: {error}"))
    })?;
    let sender_domain = parsed_sender.email.domain().to_owned();
    let sender = Mailbox::new(Some(message.from_name.clone()), parsed_sender.email);
    let mut builder = Message::builder()
        .from(sender)
        .subject(&message.subject)
        .message_id(Some(smtp_message_id(&sender_domain)));
    let mut recipient_count = 0_usize;
    for raw_recipient in message.recipient.split(';') {
        if raw_recipient.trim().is_empty() {
            return Err(VerifyEmailDependencyError::new("email recipient is empty"));
        }
        let recipient = raw_recipient.trim().parse::<Mailbox>().map_err(|error| {
            VerifyEmailDependencyError::new(format!("invalid email recipient: {error}"))
        })?;
        builder = builder.to(recipient);
        recipient_count += 1;
    }
    if recipient_count == 0 {
        return Err(VerifyEmailDependencyError::new("email recipient is empty"));
    }
    builder
        .singlepart(SinglePart::html(message.html.clone()))
        .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))
}

fn smtp_message_id(domain: &str) -> String {
    let nanos = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_nanos());
    let random = uuid::Uuid::new_v4()
        .simple()
        .to_string()
        .chars()
        .take(12)
        .collect::<String>();
    format!("<{nanos}.{random}@{domain}>")
}

/// Purpose-scoped verification-code persistence.
#[async_trait]
pub trait VerificationCodeStore: Send + Sync {
    async fn register(
        &self,
        email: &str,
        code: &str,
        purpose: &str,
        ttl: Duration,
    ) -> Result<(), VerifyEmailDependencyError>;

    async fn delete(&self, email: &str, purpose: &str) -> Result<(), VerifyEmailDependencyError>;
}

/// Fail-closed default used until the normal listener has a compatible
/// `/api/verify` consumer and scoped security-proof issuer.
pub struct DisabledVerificationCodeStore;

#[async_trait]
impl VerificationCodeStore for DisabledVerificationCodeStore {
    async fn register(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: Duration,
    ) -> Result<(), VerifyEmailDependencyError> {
        Err(VerifyEmailDependencyError::new("安全验证服务暂不可用"))
    }

    async fn delete(&self, _: &str, _: &str) -> Result<(), VerifyEmailDependencyError> {
        Ok(())
    }
}

/// Result of the dedicated 2-per-30-second email limiter.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EmailVerificationRateLimitOutcome {
    Allowed,
    RedisRejected { retry_after_seconds: u64 },
    MemoryRejected,
}

/// Injectable IP rate limiter used after the shared critical limiter.
#[async_trait]
pub trait EmailVerificationRateLimiter: Send + Sync {
    async fn take(&self, client_ip: &str) -> EmailVerificationRateLimitOutcome;
}

#[derive(Clone)]
struct DashboardVerifyEmailIdentityResolver {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

#[async_trait]
impl VerifyEmailIdentityResolver for DashboardVerifyEmailIdentityResolver {
    async fn resolve(
        &self,
        headers: &HeaderMap,
    ) -> Result<VerifyEmailIdentity, VerifyEmailAuthError> {
        let credential = dashboard_credential(headers).ok_or(VerifyEmailAuthError::Unauthorized)?;
        let internal = dashboard_token_candidate(&credential);
        let identity = match self
            .auth
            .self_user_view_for_optional(SecretString::from(credential.clone()))
            .await
        {
            Ok(user) => VerifyEmailIdentity {
                id: user.id,
                username: user.username,
                role: user.role,
                status: user.status,
                email: user.email,
                developer_access_granted: user.developer_access_granted,
            },
            Err(error) if !internal && error.kind == AuthErrorKind::UserDisabled => self
                .personal_token_identity(&credential)
                .await?
                .ok_or(VerifyEmailAuthError::Unauthorized)?,
            Err(error) => return Err(map_auth_error(error.kind)),
        };
        match console_access(&self.pg, &identity).await {
            Ok(true) => Ok(identity),
            Ok(false) | Err(_) => Err(VerifyEmailAuthError::ConsoleHidden),
        }
    }
}

impl DashboardVerifyEmailIdentityResolver {
    async fn personal_token_identity(
        &self,
        credential: &str,
    ) -> Result<Option<VerifyEmailIdentity>, VerifyEmailAuthError> {
        sqlx::query_as::<_, (i64, String, i64, i64, String)>(
            r#"SELECT id::BIGINT,
                      COALESCE(username, ''),
                      role::BIGINT,
                      status::BIGINT,
                      COALESCE(email, '')
               FROM users
               WHERE TRIM(TRAILING FROM access_token) = $1
                 AND deleted_at IS NULL
               LIMIT 1"#,
        )
        .bind(credential)
        .fetch_optional(&self.pg)
        .await
        .map(|row| {
            row.map(|(id, username, role, status, email)| VerifyEmailIdentity {
                id,
                username,
                role,
                status,
                email,
                developer_access_granted: false,
            })
        })
        .map_err(|_| VerifyEmailAuthError::Internal)
    }
}

fn map_auth_error(kind: AuthErrorKind) -> VerifyEmailAuthError {
    match kind {
        AuthErrorKind::TokenExpired => VerifyEmailAuthError::TokenExpired,
        AuthErrorKind::SessionRevoked => VerifyEmailAuthError::SessionRevoked,
        AuthErrorKind::UserDisabled => VerifyEmailAuthError::UserDisabled,
        AuthErrorKind::Internal => VerifyEmailAuthError::Internal,
        _ => VerifyEmailAuthError::Unauthorized,
    }
}

#[derive(Clone, Debug)]
struct ConsoleUser {
    id: i64,
    role: i64,
    trust_level_override: Option<i64>,
    console_activated_at: i64,
}

async fn console_access(
    pg: &PgPool,
    identity: &VerifyEmailIdentity,
) -> Result<bool, VerifyEmailDependencyError> {
    let user = sqlx::query_as::<_, (i64, i64, Option<i64>, i64)>(
        r#"SELECT id::BIGINT,
                  role::BIGINT,
                  CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
                       THEN (to_jsonb(users)->>'trust_level_override')::BIGINT END,
                  CASE WHEN COALESCE(to_jsonb(users)->>'console_activated_at', '') ~ '^-?[0-9]+$'
                       THEN (to_jsonb(users)->>'console_activated_at')::BIGINT ELSE 0 END
           FROM users
           WHERE id = $1 AND deleted_at IS NULL"#,
    )
    .bind(identity.id)
    .fetch_optional(pg)
    .await
    .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))?
    .map(
        |(id, role, trust_level_override, console_activated_at)| ConsoleUser {
            id,
            role,
            trust_level_override,
            console_activated_at,
        },
    )
    .ok_or_else(|| VerifyEmailDependencyError::new("record not found"))?;
    let granted = if user.role >= ADMIN_ROLE {
        true
    } else if let Some(level) = user.trust_level_override {
        (MINIMUM_TRUST_LEVEL..=4).contains(&level)
    } else if user.console_activated_at > 0 {
        true
    } else {
        paid_activation_complete(pg, user.id).await?
    };
    Ok(granted || identity.developer_access_granted)
}

async fn paid_activation_complete(
    pg: &PgPool,
    user_id: i64,
) -> Result<bool, VerifyEmailDependencyError> {
    sqlx::query_scalar::<_, bool>(
        r#"WITH parsed AS (
               SELECT COALESCE(row_data->>'status', '') AS status,
                      COALESCE(row_data->>'payment_method', '') AS payment_method,
                      COALESCE(row_data->>'payment_provider', '') AS payment_provider,
                      CASE WHEN COALESCE(row_data->>'money', '') ~ '^-?[0-9]+([.][0-9]+)?$'
                           THEN (row_data->>'money')::DOUBLE PRECISION ELSE 0 END AS money,
                      CASE WHEN COALESCE(row_data->>'amount', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'amount')::BIGINT ELSE 0 END AS amount,
                      CASE WHEN COALESCE(row_data->>'credited_quota', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'credited_quota')::BIGINT ELSE 0 END AS credited_quota,
                      CASE WHEN COALESCE(row_data->>'settled_amount_micros', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'settled_amount_micros')::BIGINT ELSE 0 END AS settled_amount_micros
               FROM (
                   SELECT to_jsonb(top_ups) AS row_data FROM top_ups WHERE user_id = $1
               ) rows
           ), qualified AS (
               SELECT status = 'success'
                  AND payment_method <> 'balance'
                  AND payment_provider <> 'balance'
                  AND (settled_amount_micros > 0 OR (settled_amount_micros = 0 AND money > 0))
                  AND NOT (
                      LOWER(payment_provider) = 'epay'
                      AND LOWER(payment_method) IN ('epay', 'ldc', 'linuxdo', 'linux_do', 'linuxdo_credit')
                  )
                  AND (credited_quota > 0 OR amount > 0)
                  AND (
                      payment_provider IN ('epay', 'stripe', 'creem', 'waffo', 'waffo_pancake')
                      OR (
                          payment_provider = ''
                          AND payment_method IN ('stripe', 'creem', 'waffo', 'waffo_pancake', 'alipay', 'wxpay')
                      )
                  ) AS qualifies
               FROM parsed
           )
           SELECT COALESCE(BOOL_OR(qualifies), FALSE) FROM qualified"#,
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|error| VerifyEmailDependencyError::new(error.to_string()))
}

/// Shared Valkey code storage for the sender/verifier pair.
///
/// The normal listener injects this into [`VerifyEmailState`] and its
/// `/api/verify` implementation calls [`Self::verify_and_consume`] before the
/// shared auth adapter issues a compatible session-bound scoped proof.
#[derive(Clone)]
pub struct ValkeyVerificationCodeStore {
    valkey: redis::Client,
    dependency_timeout: Duration,
}

impl ValkeyVerificationCodeStore {
    #[must_use]
    pub fn new(valkey: redis::Client) -> Self {
        Self {
            valkey,
            dependency_timeout: Duration::from_secs(2),
        }
    }

    #[must_use]
    pub fn with_dependency_timeout(mut self, dependency_timeout: Duration) -> Self {
        self.dependency_timeout = dependency_timeout;
        self
    }

    /// Checks a purpose-scoped code without consuming it.
    ///
    /// Go's email-binding controller deliberately leaves the `v` code in
    /// place until its normal TTL expires.  This is separate from
    /// [`Self::verify_and_consume`], which is reserved for the security-proof
    /// flow and must remain one-shot.
    pub async fn verify_without_consuming(
        &self,
        email: &str,
        code: &str,
        purpose: &str,
    ) -> Result<bool, VerifyEmailDependencyError> {
        let key = verification_key(email, purpose);
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("GET")
                .arg(key)
                .query_async::<Option<String>>(&mut connection)
                .await
        })
        .await;
        match result {
            Ok(Ok(value)) => Ok(value.as_deref() == Some(code)),
            Ok(Err(error)) => Err(VerifyEmailDependencyError::new(error.to_string())),
            Err(_) => Err(VerifyEmailDependencyError::new(
                "verification code validation timed out",
            )),
        }
    }

    /// Atomically checks and consumes one purpose-scoped code.
    pub async fn verify_and_consume(
        &self,
        email: &str,
        code: &str,
        purpose: &str,
    ) -> Result<bool, VerifyEmailDependencyError> {
        const SCRIPT: &str = r#"
local value = redis.call('GET', KEYS[1])
if not value or value ~= ARGV[1] then return 0 end
redis.call('DEL', KEYS[1])
return 1
"#;
        let key = verification_key(email, purpose);
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::Script::new(SCRIPT)
                .key(key)
                .arg(code)
                .invoke_async::<i64>(&mut connection)
                .await
        })
        .await;
        match result {
            Ok(Ok(result)) => Ok(result == 1),
            Ok(Err(error)) => Err(VerifyEmailDependencyError::new(error.to_string())),
            Err(_) => Err(VerifyEmailDependencyError::new(
                "verification code validation timed out",
            )),
        }
    }
}

#[async_trait]
impl VerificationCodeStore for ValkeyVerificationCodeStore {
    async fn register(
        &self,
        email: &str,
        code: &str,
        purpose: &str,
        ttl: Duration,
    ) -> Result<(), VerifyEmailDependencyError> {
        let key = verification_key(email, purpose);
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("SET")
                .arg(key)
                .arg(code)
                .arg("EX")
                .arg(ttl.as_secs())
                .query_async::<()>(&mut connection)
                .await
        })
        .await;
        match result {
            Ok(Ok(())) => Ok(()),
            Ok(Err(error)) => Err(VerifyEmailDependencyError::new(error.to_string())),
            Err(_) => Err(VerifyEmailDependencyError::new(
                "verification code storage timed out",
            )),
        }
    }

    async fn delete(&self, email: &str, purpose: &str) -> Result<(), VerifyEmailDependencyError> {
        let key = verification_key(email, purpose);
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::cmd("DEL")
                .arg(key)
                .query_async::<i64>(&mut connection)
                .await
        })
        .await;
        match result {
            Ok(Ok(_)) => Ok(()),
            Ok(Err(error)) => Err(VerifyEmailDependencyError::new(error.to_string())),
            Err(_) => Err(VerifyEmailDependencyError::new(
                "verification code cleanup timed out",
            )),
        }
    }
}

fn verification_key(email: &str, purpose: &str) -> String {
    format!("verification:{purpose}:{email}")
}

struct ValkeyMemoryEmailRateLimiter {
    valkey: redis::Client,
    memory: Mutex<MemoryRateLimitState>,
    dependency_timeout: Duration,
}

#[derive(Default)]
struct MemoryRateLimitState {
    queues: HashMap<String, VecDeque<i64>>,
    last_cleanup: i64,
}

#[async_trait]
impl EmailVerificationRateLimiter for ValkeyMemoryEmailRateLimiter {
    async fn take(&self, client_ip: &str) -> EmailVerificationRateLimitOutcome {
        match self.take_valkey(client_ip).await {
            Ok(outcome) => outcome,
            Err(()) => self.take_memory(client_ip),
        }
    }
}

impl ValkeyMemoryEmailRateLimiter {
    async fn take_valkey(&self, client_ip: &str) -> Result<EmailVerificationRateLimitOutcome, ()> {
        const SCRIPT: &str = r#"
local count = redis.call('INCR', KEYS[1])
if count == 1 then redis.call('EXPIRE', KEYS[1], ARGV[2]) end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then return {0, count, ttl} end
return {1, count, ttl}
"#;
        let key = format!("rateLimit:v2:ip:EV:{client_ip}");
        let result = tokio::time::timeout(self.dependency_timeout, async {
            let mut connection = self.valkey.get_multiplexed_async_connection().await?;
            redis::Script::new(SCRIPT)
                .key(key)
                .arg(EMAIL_RATE_LIMIT_MAX)
                .arg(EMAIL_RATE_LIMIT_WINDOW.as_secs())
                .invoke_async::<(i64, i64, i64)>(&mut connection)
                .await
        })
        .await
        .map_err(|_| ())?
        .map_err(|_| ())?;
        if result.0 == 1 {
            Ok(EmailVerificationRateLimitOutcome::Allowed)
        } else {
            Ok(EmailVerificationRateLimitOutcome::RedisRejected {
                retry_after_seconds: if result.2 > 0 {
                    result.2 as u64
                } else {
                    EMAIL_RATE_LIMIT_WINDOW.as_secs()
                },
            })
        }
    }

    fn take_memory(&self, client_ip: &str) -> EmailVerificationRateLimitOutcome {
        let now = unix_seconds();
        let mut state = self
            .memory
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if now.saturating_sub(state.last_cleanup) >= RATE_LIMIT_CLEANUP_INTERVAL.as_secs() as i64 {
            let expiration = RATE_LIMIT_CLEANUP_INTERVAL.as_secs() as i64;
            state.queues.retain(|_, queue| {
                queue
                    .back()
                    .is_some_and(|timestamp| now.saturating_sub(*timestamp) <= expiration)
            });
            state.last_cleanup = now;
        }
        let queue = state.queues.entry(format!("EV:{client_ip}")).or_default();
        if queue.len() < EMAIL_RATE_LIMIT_MAX {
            queue.push_back(now);
            return EmailVerificationRateLimitOutcome::Allowed;
        }
        let window = EMAIL_RATE_LIMIT_WINDOW.as_secs() as i64;
        if queue
            .front()
            .is_some_and(|timestamp| now.saturating_sub(*timestamp) >= window)
        {
            queue.pop_front();
            queue.push_back(now);
            EmailVerificationRateLimitOutcome::Allowed
        } else {
            EmailVerificationRateLimitOutcome::MemoryRejected
        }
    }
}

async fn send_security_email(State(state): State<VerifyEmailState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let identity = match state.identities.resolve(&headers).await {
        Ok(identity) => identity,
        Err(VerifyEmailAuthError::ConsoleHidden) => return console_not_found(),
        Err(error) => return dashboard_auth_error(&headers, error),
    };
    if let Err(error) = enforce_user_auth(&identity) {
        return user_auth_error(&headers, error);
    }
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let email_limit = state.email_rate_limiter.take(&client_ip).await;
    if email_limit != EmailVerificationRateLimitOutcome::Allowed {
        return with_auth_version(email_rate_limit_response(email_limit));
    }

    let response = send_security_email_after_limits(&state, &identity).await;
    with_auth_version(disable_cache(response))
}

async fn send_security_email_after_limits(
    state: &VerifyEmailState,
    identity: &VerifyEmailIdentity,
) -> Response {
    let email = identity.email.trim().to_lowercase();
    if email.is_empty() {
        return coded_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "SECURITY_EMAIL_REQUIRED",
            "请先绑定邮箱后再使用邮箱验证",
        );
    }
    let system_name = match state.system_name().await {
        Ok(system_name) => system_name,
        Err(error) => return api_error(error.to_string()),
    };
    let code = verification_code();
    if let Err(error) = state
        .codes
        .register(&email, &code, SECURITY_EMAIL_PURPOSE, VERIFICATION_TTL)
        .await
    {
        return api_error(error.to_string());
    }
    let message = SecurityEmailMessage {
        from_name: system_name.clone(),
        subject: format!("{system_name}安全验证邮件"),
        recipient: email.clone(),
        html: format!(
            "<p>您好，你正在进行{system_name}敏感操作安全验证。</p>\
             <p>您的验证码为: <strong>{code}</strong></p>\
             <p>验证码 {} 分钟内有效。如果不是本人操作，请忽略。</p>",
            VERIFICATION_TTL.as_secs() / 60
        ),
    };
    if let Err(error) = state.mailer.send(&message).await {
        let _ = state.codes.delete(&email, SECURITY_EMAIL_PURPOSE).await;
        return api_error(error.to_string());
    }
    Json(json!({
        "success": true,
        "message": "安全验证码已发送",
        "data": {"email_hint": mask_security_email(&email)},
    }))
    .into_response()
}

fn enforce_user_auth(identity: &VerifyEmailIdentity) -> Result<(), UserAuthPolicyError> {
    if identity.status != 1 {
        return Err(UserAuthPolicyError::UserDisabled);
    }
    if identity.role < 1 {
        return Err(UserAuthPolicyError::InsufficientPrivilege);
    }
    if identity.id <= 0
        || identity.username.trim().is_empty()
        || !matches!(identity.role, 0 | 1 | 10 | 100)
    {
        return Err(UserAuthPolicyError::InvalidUserInfo);
    }
    Ok(())
}

async fn critical_rate_limit(state: &VerifyEmailState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

fn email_rate_limit_response(outcome: EmailVerificationRateLimitOutcome) -> Response {
    let message = match outcome {
        EmailVerificationRateLimitOutcome::RedisRejected {
            retry_after_seconds,
        } => format!("发送过于频繁，请等待 {retry_after_seconds} 秒后再试"),
        EmailVerificationRateLimitOutcome::MemoryRejected => "发送过于频繁，请稍后再试".to_owned(),
        EmailVerificationRateLimitOutcome::Allowed => String::new(),
    };
    (
        StatusCode::TOO_MANY_REQUESTS,
        Json(json!({"success": false, "message": message})),
    )
        .into_response()
}

fn verification_code() -> String {
    uuid::Uuid::new_v4()
        .simple()
        .to_string()
        .chars()
        .take(6)
        .collect()
}

fn mask_security_email(email: &str) -> String {
    let Some((local, domain)) = email.split_once('@') else {
        return String::new();
    };
    let mut characters = local.chars();
    let Some(first) = characters.next() else {
        return String::new();
    };
    let remainder: Vec<char> = characters.collect();
    if remainder.len() <= 1 {
        format!("{first}***@{domain}")
    } else {
        format!("{first}***{}@{domain}", remainder[remainder.len() - 1])
    }
}

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_else(|| "unknown".to_owned())
}

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

fn dashboard_auth_error(headers: &HeaderMap, error: VerifyEmailAuthError) -> Response {
    let (status, code, english) = match error {
        VerifyEmailAuthError::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        VerifyEmailAuthError::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        VerifyEmailAuthError::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        VerifyEmailAuthError::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        VerifyEmailAuthError::Unauthorized | VerifyEmailAuthError::ConsoleHidden => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    let message = if accepts_chinese(headers) {
        match error {
            VerifyEmailAuthError::Internal => "数据库出错，请联系管理员",
            VerifyEmailAuthError::TokenExpired | VerifyEmailAuthError::SessionRevoked => {
                "无权进行此操作，未登录且未提供 access token"
            }
            VerifyEmailAuthError::UserDisabled => "用户已被封禁",
            VerifyEmailAuthError::Unauthorized | VerifyEmailAuthError::ConsoleHidden => {
                "无权进行此操作，access token 无效"
            }
        }
    } else {
        english
    };
    coded_error(status, code, message)
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    coded_error(
        status,
        code,
        user_auth_message(
            error,
            headers
                .get(header::ACCEPT_LANGUAGE)
                .and_then(|value| value.to_str().ok()),
        ),
    )
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn api_error(message: String) -> Response {
    Json(json!({"success": false, "message": message})).into_response()
}

fn coded_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

fn disable_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn unix_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn smtp_options(entries: &[(&str, &str)]) -> HashMap<String, String> {
        entries
            .iter()
            .map(|(key, value)| ((*key).to_owned(), (*value).to_owned()))
            .collect()
    }

    #[test]
    fn email_mask_matches_legacy_ascii_shapes() {
        assert_eq!(mask_security_email("a@example.com"), "a***@example.com");
        assert_eq!(mask_security_email("ab@example.com"), "a***@example.com");
        assert_eq!(
            mask_security_email("alice@example.com"),
            "a***e@example.com"
        );
    }

    #[test]
    fn verification_code_is_six_lowercase_hex_characters() {
        let code = verification_code();
        assert_eq!(code.len(), 6);
        assert!(code.chars().all(|character| character.is_ascii_hexdigit()));
    }

    #[test]
    fn smtp_config_applies_from_fallback_port_465_tls_and_login_rules() -> TestResult {
        let options = smtp_options(&[
            ("SMTPServer", "smtp.example.com"),
            ("SMTPPort", "465"),
            ("SMTPAccount", "operator@outlook.example"),
            ("SMTPToken", "secret"),
        ]);

        let config = SmtpConfig::from_options(&options)?;

        assert_eq!(config.from, "operator@outlook.example");
        assert_eq!(config.tls_mode, SmtpTlsMode::ImplicitTls);
        assert_eq!(config.auth_mechanisms(), Some(vec![Mechanism::Login]));
        Ok(())
    }

    #[test]
    fn smtp_config_requires_starttls_and_only_skips_verification_when_explicit() -> TestResult {
        let mut options = smtp_options(&[
            ("SMTPServer", "smtp.example.com"),
            ("SMTPAccount", "operator@example.com"),
            ("SMTPToken", "secret"),
            ("SMTPStartTLSEnabled", "true"),
        ]);

        let strict = SmtpConfig::from_options(&options)?;
        assert_eq!(strict.tls_mode, SmtpTlsMode::RequiredStartTls);
        assert!(!strict.insecure_skip_verify);
        assert_eq!(
            strict.auth_mechanisms(),
            Some(vec![Mechanism::Plain, Mechanism::Login])
        );

        options.insert("SMTPInsecureSkipVerify".to_owned(), "true".to_owned());
        let insecure = SmtpConfig::from_options(&options)?;
        assert!(insecure.insecure_skip_verify);
        Ok(())
    }

    #[test]
    fn smtp_config_requires_server_or_account() -> TestResult {
        let error = SmtpConfig::from_options(&HashMap::new())
            .err()
            .ok_or_else(|| std::io::Error::other("missing SMTP configuration was accepted"))?;

        assert_eq!(error.to_string(), "SMTP 服务器未配置");
        Ok(())
    }

    #[test]
    fn smtp_message_contains_named_sender_recipient_html_and_sender_domain_id() -> TestResult {
        let config = SmtpConfig::from_options(&smtp_options(&[
            ("SMTPServer", "smtp.example.com"),
            ("SMTPAccount", "operator@example.com"),
        ]))?;
        let message = SecurityEmailMessage {
            from_name: "LMM API".to_owned(),
            subject: "Security verification".to_owned(),
            recipient: "member@example.net".to_owned(),
            html: "<p>code 123456</p>".to_owned(),
        };

        let formatted = String::from_utf8(build_smtp_message(&config, &message)?.formatted())?;

        assert!(formatted.contains("From: "));
        assert!(formatted.contains("LMM API"));
        assert!(formatted.contains("<operator@example.com>"));
        assert!(formatted.contains("To: member@example.net"));
        assert!(formatted.contains("Message-ID: <") && formatted.contains("@example.com>"));
        assert!(formatted.contains("Content-Type: text/html; charset=utf-8"));
        assert!(formatted.contains("<p>code 123456</p>"));
        Ok(())
    }
}
