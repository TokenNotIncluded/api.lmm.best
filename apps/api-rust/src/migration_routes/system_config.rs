//! Legacy-compatible system configuration migration routes.
//!
//! This module deliberately owns only the current `system-config` rows in
//! `migration-plan.tsv`.  In particular it does not claim any route merely
//! because it happens to start with `/api/option`.

use crate::protocol_rollout::{
    ConverterPairOverride, FlagConfig, ProtocolRolloutConfig, ProtocolRolloutControl,
    ProtocolRolloutControlError, RolloutConfigError, parse_boolean, parse_loss_policy,
};
use async_trait::async_trait;
use axum::{
    Extension, Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::{DefaultBodyLimit, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64};
use bcrypt::{DEFAULT_COST, hash, verify};
use redis::AsyncCommands;
use rsa::{
    RsaPrivateKey,
    pkcs1::DecodeRsaPrivateKey,
    pkcs1v15::SigningKey,
    pkcs8::DecodePrivateKey,
    signature::{SignatureEncoding as _, Signer as _},
};
use rust_decimal::Decimal;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest as _, Sha256};
use sqlx::{PgPool, Row};
use std::{
    collections::BTreeMap,
    error::Error,
    fmt,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};
use tokio::sync::{Mutex, RwLock};

const OPTIONS_CACHE_KEY: &str = "lmm:system-config:options";
const AFFINITY_CACHE_PREFIX: &str = "new-api:channel_affinity:v1:";
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const OPTIONS_CACHE_TTL_SECONDS: u64 = 5;
const WAFFO_PANCAKE_MUTATION_REQUEST_MAX_BYTES: usize = 16 << 10;
const MAX_PROJECT_UPDATE_BYTES: usize = 1 << 20;
const MAX_EXCHANGE_RATE_BYTES: usize = 256 << 10;
const EXCHANGE_RATE_TIMEOUT: Duration = Duration::from_secs(10);
const FRANKFURTER_URL_PREFIX: &str = "https://api.frankfurter.app/latest?from=USD&to=";
const ER_API_URL: &str = "https://open.er-api.com/v6/latest/USD";
const PROJECT_UPDATE_URL: &str =
    "https://api.github.com/repos/LIghtJUNction/api.lmm.best/commits/main";
const PANCAKE_API_BASE_URL: &str = "https://api.waffo.ai";
const PANCAKE_GRAPHQL_PATH: &str = "/v1/graphql";
const PANCAKE_CREATE_STORE_PATH: &str = "/v1/actions/store/create-store";
const PANCAKE_CREATE_PRODUCT_PATH: &str = "/v1/actions/onetime-product/create-product";
const PANCAKE_PUBLISH_PRODUCT_PATH: &str = "/v1/actions/onetime-product/publish-product";
const PANCAKE_CREATE_SUBSCRIPTION_PRODUCT_PATH: &str =
    "/v1/actions/subscription-product/create-product";
const PANCAKE_PUBLISH_SUBSCRIPTION_PRODUCT_PATH: &str =
    "/v1/actions/subscription-product/publish-product";
const MAX_PANCAKE_RESPONSE_BYTES: usize = 1 << 20;
const PANCAKE_STORE_NAME: &str = "lmm-forge-store";
const PANCAKE_PRIMARY_PRODUCT_NAME: &str = "lmm-forge-wallet-topup";
const PANCAKE_TAX_CATEGORY: &str = "saas";

const PROTOCOL_ROLLOUT_CONVERSION_ENGINE_KEY: &str = "conversion_engine_v2";
const PROTOCOL_ROLLOUT_LOSS_POLICY_KEY: &str = "conversion_loss_policy";
const PROTOCOL_ROLLOUT_GEMINI_FUNCTION_ID_KEY: &str = "gemini_function_id_v2";
const PROTOCOL_ROLLOUT_GEMINI_THOUGHT_SIGNATURE_KEY: &str = "gemini_thought_signature_v2";
const PROTOCOL_ROLLOUT_CLAUDE_THINKING_KEY: &str = "claude_opaque_thinking_v2";
const PROTOCOL_ROLLOUT_SSE_PARSER_KEY: &str = "sse_parser_v2";
const PROTOCOL_ROLLOUT_PAIR_OVERRIDES_KEY: &str = "converter_pair_overrides";
const PROTOCOL_ROLLOUT_ROLLBACK_KEY: &str = "protocol_rollout_rollback";

const PROTOCOL_ROLLOUT_KEYS: [&str; 8] = [
    PROTOCOL_ROLLOUT_CONVERSION_ENGINE_KEY,
    PROTOCOL_ROLLOUT_LOSS_POLICY_KEY,
    PROTOCOL_ROLLOUT_GEMINI_FUNCTION_ID_KEY,
    PROTOCOL_ROLLOUT_GEMINI_THOUGHT_SIGNATURE_KEY,
    PROTOCOL_ROLLOUT_CLAUDE_THINKING_KEY,
    PROTOCOL_ROLLOUT_SSE_PARSER_KEY,
    PROTOCOL_ROLLOUT_PAIR_OVERRIDES_KEY,
    PROTOCOL_ROLLOUT_ROLLBACK_KEY,
];

/// Concrete production adapter for the pinned GitHub update probe.
///
/// The caller supplies the shared HTTPS-only client from `outbound_http`; this
/// adapter additionally keeps the legacy response cap and commit normalization
/// at the route boundary so an upstream response never becomes dashboard data
/// without validation.
#[derive(Clone)]
pub struct HttpProjectUpdateClient {
    client: reqwest::Client,
}

impl HttpProjectUpdateClient {
    #[must_use]
    pub fn new(client: reqwest::Client) -> Self {
        Self { client }
    }
}

#[async_trait]
impl ProjectUpdateClient for HttpProjectUpdateClient {
    async fn latest_main_commit(&self) -> Result<Value, ()> {
        let response = self
            .client
            .get(PROJECT_UPDATE_URL)
            .header(header::ACCEPT, "application/vnd.github+json")
            .header(header::USER_AGENT, "LMM-API-update-checker")
            .header("x-github-api-version", "2022-11-28")
            .send()
            .await
            .map_err(|_| ())?
            .error_for_status()
            .map_err(|_| ())?;
        let body = response.bytes().await.map_err(|_| ())?;
        if body.len() > MAX_PROJECT_UPDATE_BYTES {
            return Err(());
        }
        let source: GitHubCommit = serde_json::from_slice(&body).map_err(|_| ())?;
        source.into_legacy_release()
    }
}

#[derive(Deserialize)]
struct GitHubCommit {
    sha: String,
    commit: GitHubCommitDetails,
}

#[derive(Deserialize)]
struct GitHubCommitDetails {
    message: String,
    author: GitHubCommitAuthor,
    committer: GitHubCommitAuthor,
}

#[derive(Deserialize)]
struct GitHubCommitAuthor {
    date: String,
}

impl GitHubCommit {
    fn into_legacy_release(self) -> Result<Value, ()> {
        let sha = self.sha.trim().to_ascii_lowercase();
        if !(7..=64).contains(&sha.len()) || !sha.bytes().all(|byte| byte.is_ascii_hexdigit()) {
            return Err(());
        }
        let body = self.commit.message;
        let mut name = body.lines().next().unwrap_or_default().trim().to_owned();
        if name.is_empty() {
            return Err(());
        }
        name = name.chars().take(120).collect();
        let published_at = if self.commit.author.date.trim().is_empty() {
            self.commit.committer.date
        } else {
            self.commit.author.date
        };
        let published_at = chrono_like_rfc3339(&published_at)?;
        Ok(json!({
            "tag_name": &sha[..7],
            "name": name,
            "body": body,
            "html_url": format!("https://github.com/LIghtJUNction/api.lmm.best/commit/{sha}"),
            "published_at": published_at,
        }))
    }
}

fn chrono_like_rfc3339(value: &str) -> Result<String, ()> {
    chrono::DateTime::parse_from_rfc3339(value.trim())
        .map(|timestamp| timestamp.to_rfc3339_opts(chrono::SecondsFormat::AutoSi, true))
        .map_err(|_| ())
}

/// Bounded upstream update checker supplied by the host. The production
/// implementation must use the pinned GitHub commit endpoint, a ten second
/// deadline, response-size cap, and no redirect to preserve legacy safety.
#[async_trait]
pub trait ProjectUpdateClient: Send + Sync {
    /// Returns the legacy release-shaped object for the current upstream main
    /// commit. Errors intentionally stay opaque to dashboard callers.
    async fn latest_main_commit(&self) -> Result<Value, ()>;
}

/// Resolves the number of settlement-currency units represented by one USD.
/// Implementations must not infer a currency from a display symbol.
#[async_trait]
pub trait ExchangeRateProvider: Send + Sync {
    /// Returns a positive finite rate for a validated ISO 4217-style code.
    async fn settlement_units_per_usd(&self, currency: &str) -> Result<f64, ()>;
}

struct PinnedExchangeRateProvider {
    client: Option<reqwest::Client>,
}

impl PinnedExchangeRateProvider {
    fn production() -> Self {
        Self {
            client: crate::outbound_http::client(EXCHANGE_RATE_TIMEOUT).ok(),
        }
    }

    async fn fetch_rate(&self, url: &str, currency: &str) -> Result<f64, ()> {
        let client = self.client.as_ref().ok_or(())?;
        let response = client.get(url).send().await.map_err(|_| ())?;
        if !response.status().is_success() {
            return Err(());
        }
        let body = to_bytes(
            Body::from_stream(response.bytes_stream()),
            MAX_EXCHANGE_RATE_BYTES,
        )
        .await
        .map_err(|_| ())?;
        let payload: Value = serde_json::from_slice(&body).map_err(|_| ())?;
        if payload
            .get("result")
            .and_then(Value::as_str)
            .is_some_and(|result| result != "success")
        {
            return Err(());
        }
        payload
            .get("rates")
            .and_then(|rates| rates.get(currency))
            .and_then(Value::as_f64)
            .filter(|rate| rate.is_finite() && *rate > 0.0)
            .ok_or(())
    }
}

#[async_trait]
impl ExchangeRateProvider for PinnedExchangeRateProvider {
    async fn settlement_units_per_usd(&self, currency: &str) -> Result<f64, ()> {
        let currency = normalize_currency_code(currency).ok_or(())?;
        if currency == "USD" {
            return Ok(1.0);
        }
        // Both destinations are compile-time constants. The validated currency
        // can only occupy Frankfurter's `to` value, so callers cannot select a
        // host, scheme, port, path, or redirect target.
        let primary = format!("{FRANKFURTER_URL_PREFIX}{currency}");
        if let Ok(rate) = self.fetch_rate(&primary, &currency).await {
            return Ok(rate);
        }
        self.fetch_rate(ER_API_URL, &currency).await
    }
}

/// Authentication boundary supplied by the dashboard-auth migration.
///
/// The legacy routes require a root dashboard session, not a bearer API key.
/// Keeping that rule here prevents a later router integration from silently
/// treating an arbitrary header as administrative authority.
#[async_trait]
pub trait SystemConfigAuthorizer: Send + Sync {
    /// Returns a server-validated identity only for a root dashboard session.
    async fn require_root_dashboard_session(
        &self,
        headers: &HeaderMap,
    ) -> Result<SystemConfigIdentity, ()>;

    /// Rich result used by the HTTP guard. Existing test authorizers retain
    /// source compatibility and are treated as browser-session principals.
    async fn authorize_root(
        &self,
        headers: &HeaderMap,
    ) -> Result<SystemConfigAuthContext, SystemConfigAuthRejection> {
        self.require_root_dashboard_session(headers)
            .await
            .map(|identity| SystemConfigAuthContext {
                identity,
                credential: SystemConfigCredential::DashboardSession,
            })
            .map_err(|_| SystemConfigAuthRejection::Unauthorized {
                supplied: crate::migration_routes::legacy_http::dashboard_credential(headers)
                    .is_some(),
            })
    }
}

/// Applies a committed option change to the process-wide configuration that
/// services use between PostgreSQL synchronisation passes.
///
/// The frozen Go service persists an option before updating its `OptionMap`.
/// A migration listener must therefore have one explicit owner for that
/// second step; writing PostgreSQL alone would report success while the
/// running process continues with stale authentication, billing, or relay
/// settings.
#[async_trait]
pub trait SystemConfigRuntimeWriter: Send + Sync {
    /// Fails before the database mutation when this listener cannot safely
    /// apply every requested key to its shared runtime configuration.
    async fn preflight(&self, changes: &[(String, String)]) -> Result<(), ()>;

    /// Updates the shared runtime after PostgreSQL commits, preserving the
    /// legacy durable-store-first ordering.
    async fn apply_committed(&self, changes: &[(String, String)]) -> Result<(), ()>;
}

/// Safe default until listener composition provides the shared runtime owner.
///
/// This deliberately denies writes instead of constructing a private map in
/// the candidate listener.  A private map would make an administrator see a
/// success envelope while every relay worker still used the old option value.
#[derive(Clone, Copy, Debug, Default)]
pub struct MissingSystemConfigRuntimeWriter;

#[async_trait]
impl SystemConfigRuntimeWriter for MissingSystemConfigRuntimeWriter {
    async fn preflight(&self, _: &[(String, String)]) -> Result<(), ()> {
        Err(())
    }

    async fn apply_committed(&self, _: &[(String, String)]) -> Result<(), ()> {
        Err(())
    }
}

/// Errors raised while turning persisted system options into a live protocol
/// rollout configuration.
///
/// The error deliberately identifies only a stable option name.  Persisted
/// rollout JSON is configuration data and must never be copied into an error,
/// log line, or `Debug` representation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ProtocolRolloutRuntimeError {
    /// A recognized option could not be parsed without widening traffic.
    InvalidOption { key: &'static str },
    /// A parsed candidate failed complete rollout validation.
    InvalidConfig(RolloutConfigError),
    /// The live control rejected installation of a candidate.
    Control(ProtocolRolloutControlError),
    /// A protocol option was requested before the shared control was bound.
    MissingControl,
}

impl fmt::Display for ProtocolRolloutRuntimeError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidOption { key } => {
                write!(formatter, "invalid protocol rollout option {key}")
            }
            Self::InvalidConfig(error) => {
                write!(formatter, "invalid protocol rollout config: {error}")
            }
            Self::Control(error) => write!(
                formatter,
                "protocol rollout control rejected update: {error}"
            ),
            Self::MissingControl => {
                formatter.write_str("protocol rollout control is not configured")
            }
        }
    }
}

impl Error for ProtocolRolloutRuntimeError {
    fn source(&self) -> Option<&(dyn Error + 'static)> {
        match self {
            Self::InvalidConfig(error) => Some(error),
            Self::Control(error) => Some(error),
            Self::InvalidOption { .. } | Self::MissingControl => None,
        }
    }
}

fn is_protocol_rollout_key(key: &str) -> bool {
    PROTOCOL_ROLLOUT_KEYS.contains(&key)
}

fn invalid_rollout_option(key: &'static str) -> ProtocolRolloutRuntimeError {
    ProtocolRolloutRuntimeError::InvalidOption { key }
}

fn parse_rollout_flag(
    value: &str,
    key: &'static str,
) -> Result<FlagConfig, ProtocolRolloutRuntimeError> {
    // JSON is required for enabled rollout values so a canary or selector
    // cannot be widened accidentally. `false` is the only shorthand and is
    // always fail-closed.
    let flag = match value.trim() {
        "false" => FlagConfig::disabled(),
        _ => serde_json::from_str::<FlagConfig>(value).map_err(|_| invalid_rollout_option(key))?,
    };
    flag.validate()
        .map_err(ProtocolRolloutRuntimeError::InvalidConfig)?;
    Ok(flag)
}

fn parse_rollout_rollback(value: &str) -> Result<bool, ProtocolRolloutRuntimeError> {
    parse_boolean(value.trim(), PROTOCOL_ROLLOUT_ROLLBACK_KEY)
        .map_err(ProtocolRolloutRuntimeError::InvalidConfig)
}

fn emergency_rollback_value(value: &str) -> bool {
    matches!(value.trim(), "true" | "1")
}

fn rollout_config_for_changes(
    base: &ProtocolRolloutConfig,
    changes: &[(String, String)],
) -> Result<Option<ProtocolRolloutConfig>, ProtocolRolloutRuntimeError> {
    // Collapse repeated keys before applying any safety rule. This preserves
    // the database writer's last-write-wins contract, including rollback.
    let mut recognized = BTreeMap::<&str, &str>::new();
    for (key, value) in changes {
        if is_protocol_rollout_key(key) {
            recognized.insert(key.as_str(), value.as_str());
        }
    }
    if recognized.is_empty() {
        return Ok(None);
    }

    // A true rollback is an emergency fail-closed path.  Scan it before
    // parsing any other recognized value so a stale malformed v2 setting in
    // the same write cannot block the safety action or widen traffic.
    if recognized
        .get(PROTOCOL_ROLLOUT_ROLLBACK_KEY)
        .is_some_and(|value| emergency_rollback_value(value))
    {
        return Ok(Some(base.rolled_back()));
    }

    let rollback_value = recognized
        .get(PROTOCOL_ROLLOUT_ROLLBACK_KEY)
        .map(|value| parse_rollout_rollback(value))
        .transpose()?;

    // An environment-level rollback is also fail-closed until an explicit,
    // valid persisted false begins recovery. Do not make stale persisted v2
    // values capable of blocking that emergency state.
    if base.rollback && rollback_value != Some(false) {
        return Ok(Some(base.rolled_back()));
    }

    let mut candidate = base.clone();
    for (key, value) in recognized {
        match key {
            PROTOCOL_ROLLOUT_CONVERSION_ENGINE_KEY => {
                candidate.conversion_engine_v2 =
                    parse_rollout_flag(value, PROTOCOL_ROLLOUT_CONVERSION_ENGINE_KEY)?;
            }
            PROTOCOL_ROLLOUT_LOSS_POLICY_KEY => {
                candidate.conversion_loss_policy = parse_loss_policy(value.trim())
                    .map_err(ProtocolRolloutRuntimeError::InvalidConfig)?;
            }
            PROTOCOL_ROLLOUT_GEMINI_FUNCTION_ID_KEY => {
                candidate.gemini_function_id_v2 =
                    parse_rollout_flag(value, PROTOCOL_ROLLOUT_GEMINI_FUNCTION_ID_KEY)?;
            }
            PROTOCOL_ROLLOUT_GEMINI_THOUGHT_SIGNATURE_KEY => {
                candidate.gemini_thought_signature_v2 =
                    parse_rollout_flag(value, PROTOCOL_ROLLOUT_GEMINI_THOUGHT_SIGNATURE_KEY)?;
            }
            PROTOCOL_ROLLOUT_CLAUDE_THINKING_KEY => {
                candidate.claude_opaque_thinking_v2 =
                    parse_rollout_flag(value, PROTOCOL_ROLLOUT_CLAUDE_THINKING_KEY)?;
            }
            PROTOCOL_ROLLOUT_SSE_PARSER_KEY => {
                candidate.sse_parser_v2 =
                    parse_rollout_flag(value, PROTOCOL_ROLLOUT_SSE_PARSER_KEY)?;
            }
            PROTOCOL_ROLLOUT_PAIR_OVERRIDES_KEY => {
                candidate.converter_pair_overrides =
                    serde_json::from_str::<Vec<ConverterPairOverride>>(value)
                        .map_err(|_| invalid_rollout_option(PROTOCOL_ROLLOUT_PAIR_OVERRIDES_KEY))?;
            }
            PROTOCOL_ROLLOUT_ROLLBACK_KEY => {
                candidate.rollback = parse_rollout_rollback(value)?;
            }
            _ => {}
        }
    }
    if candidate.rollback && rollback_value.is_none() {
        return Ok(Some(candidate.rolled_back()));
    }
    candidate
        .validate()
        .map_err(ProtocolRolloutRuntimeError::InvalidConfig)?;
    Ok(Some(candidate))
}

fn rollout_config_from_options(
    base: &ProtocolRolloutConfig,
    initial: &BTreeMap<String, String>,
) -> Result<ProtocolRolloutConfig, ProtocolRolloutRuntimeError> {
    let changes = initial
        .iter()
        .filter(|(key, _)| is_protocol_rollout_key(key))
        .map(|(key, value)| (key.clone(), value.clone()))
        .collect::<Vec<_>>();
    Ok(rollout_config_for_changes(base, &changes)?.unwrap_or_else(|| base.clone()))
}

/// Process-wide option owner used by the normal Rust listener.
///
/// Most migrated handlers read durable options from PostgreSQL for each
/// request.  A small set of long-lived adapters, however, needs the same
/// process-wide view that the legacy Go `OptionMap` provided.  Keeping that
/// view behind one shared lock gives `/api/option/` a concrete runtime owner
/// without creating a route-local cache that other workers cannot observe.
#[derive(Clone)]
pub struct ProcessRuntimeOptions {
    values: Arc<RwLock<BTreeMap<String, String>>>,
    protocol_rollout_base: Option<ProtocolRolloutConfig>,
    protocol_rollout: Option<ProtocolRolloutControl>,
    protocol_update_lock: Arc<Mutex<()>>,
}

impl Default for ProcessRuntimeOptions {
    fn default() -> Self {
        Self::new(BTreeMap::new())
    }
}

impl fmt::Debug for ProcessRuntimeOptions {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ProcessRuntimeOptions")
            .field("values", &"<redacted>")
            .field(
                "protocol_rollout_configured",
                &self.protocol_rollout.is_some(),
            )
            .finish()
    }
}

impl ProcessRuntimeOptions {
    #[must_use]
    pub fn new(initial: BTreeMap<String, String>) -> Self {
        Self {
            values: Arc::new(RwLock::new(initial)),
            protocol_rollout_base: None,
            protocol_rollout: None,
            protocol_update_lock: Arc::new(Mutex::new(())),
        }
    }

    /// Overlays recognized persisted options onto the startup environment
    /// configuration and installs one shared live control. Enabled flags use
    /// complete `FlagConfig` JSON; only `false` is accepted as a shorthand.
    /// A persisted rollback of `true` is evaluated before stale v2 values and
    /// therefore always starts fail-closed. The startup baseline is retained
    /// so a later `rollback=false` can rebuild the complete candidate from
    /// persisted values rather than from a disabled rollback snapshot.
    pub async fn with_protocol_rollout(
        mut self,
        startup: ProtocolRolloutConfig,
    ) -> Result<Self, ProtocolRolloutRuntimeError> {
        startup
            .validate()
            .map_err(ProtocolRolloutRuntimeError::InvalidConfig)?;
        let initial = self.snapshot().await;
        let candidate = rollout_config_from_options(&startup, &initial)?;
        let control =
            ProtocolRolloutControl::new(candidate).map_err(ProtocolRolloutRuntimeError::Control)?;
        self.protocol_rollout_base = Some(startup);
        self.protocol_rollout = Some(control);
        Ok(self)
    }

    /// Returns a cheap clone of the shared live rollout holder for relay-state
    /// composition.  Requests should call its snapshot method and release the
    /// control mutex before doing request or stream work.
    #[must_use]
    pub fn protocol_rollout(&self) -> Option<ProtocolRolloutControl> {
        self.protocol_rollout.clone()
    }

    /// Returns a coherent snapshot for runtime adapters that need options
    /// without issuing another PostgreSQL query.
    pub async fn snapshot(&self) -> BTreeMap<String, String> {
        self.values.read().await.clone()
    }
}

#[async_trait]
impl SystemConfigRuntimeWriter for ProcessRuntimeOptions {
    async fn preflight(&self, changes: &[(String, String)]) -> Result<(), ()> {
        if changes.is_empty() || changes.iter().any(|(key, _)| key.trim().is_empty()) {
            return Err(());
        }
        if !changes.iter().any(|(key, _)| is_protocol_rollout_key(key)) {
            return Ok(());
        }
        let (Some(base), Some(control)) = (
            self.protocol_rollout_base.as_ref(),
            self.protocol_rollout.as_ref(),
        ) else {
            return Err(());
        };
        let _update_guard = self.protocol_update_lock.lock().await;
        control.try_snapshot().map_err(|_| ())?;
        let mut merged = self.values.read().await.clone();
        for (key, value) in changes {
            merged.insert(key.clone(), value.clone());
        }
        rollout_config_from_options(base, &merged).map_err(|_| ())?;
        Ok(())
    }

    async fn apply_committed(&self, changes: &[(String, String)]) -> Result<(), ()> {
        if changes.is_empty() {
            return Ok(());
        }
        let _update_guard = self.protocol_update_lock.lock().await;
        let mut merged = self.values.read().await.clone();
        for (key, value) in changes {
            merged.insert(key.clone(), value.clone());
        }
        let protocol_candidate = if changes.iter().any(|(key, _)| is_protocol_rollout_key(key)) {
            let (Some(base), Some(control)) = (
                self.protocol_rollout_base.as_ref(),
                self.protocol_rollout.as_ref(),
            ) else {
                return Err(());
            };
            let candidate = rollout_config_from_options(base, &merged).map_err(|_| ())?;
            Some((control.clone(), candidate))
        } else {
            None
        };
        let mut values = self.values.write().await;
        if let Some((control, candidate)) = protocol_candidate {
            // No await point remains between installing the validated control
            // snapshot and updating the corresponding option map entries.
            control.replace(candidate).map_err(|_| ())?;
        }
        for (key, value) in changes {
            values.insert(key.clone(), value.clone());
        }
        Ok(())
    }
}

#[cfg(test)]
mod protocol_rollout_runtime_tests {
    use super::{ProcessRuntimeOptions, ProtocolRolloutConfig, SystemConfigRuntimeWriter};
    use crate::protocol_rollout::{FlagConfig, ProtocolRolloutSnapshotStatus};
    use serde_json::from_str;
    use std::{collections::BTreeMap, io};

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    const FLAG_AT_ONE_PERCENT: &str =
        r#"{"enabled":true,"canary_basis_points":100,"overrides":[]}"#;
    const FLAG_AT_FIVE_PERCENT: &str =
        r#"{"enabled":true,"canary_basis_points":500,"overrides":[]}"#;

    fn invariant_error(message: &'static str) -> io::Error {
        io::Error::other(message)
    }

    async fn runtime_with_control() -> TestResult<ProcessRuntimeOptions> {
        Ok(ProcessRuntimeOptions::new(BTreeMap::new())
            .with_protocol_rollout(ProtocolRolloutConfig::default())
            .await?)
    }

    #[tokio::test]
    async fn persisted_options_overlay_startup_configuration() -> TestResult {
        let mut initial = BTreeMap::new();
        initial.insert(
            "conversion_engine_v2".to_owned(),
            FLAG_AT_ONE_PERCENT.to_owned(),
        );
        initial.insert("conversion_loss_policy".to_owned(), "warn".to_owned());
        let runtime = ProcessRuntimeOptions::new(initial)
            .with_protocol_rollout(ProtocolRolloutConfig::default())
            .await?;

        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let snapshot = control.snapshot();
        assert_eq!(
            snapshot.config().conversion_engine_v2.canary_basis_points,
            100
        );
        assert_eq!(
            snapshot.config().loss_policy(),
            lmm_contracts::relay::LossPolicy::Warn
        );
        Ok(())
    }

    #[tokio::test]
    async fn valid_protocol_change_replaces_immediately_and_advances_generation() -> TestResult {
        let runtime = runtime_with_control().await?;
        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let before = control.snapshot();
        let changes = vec![(
            "conversion_engine_v2".to_owned(),
            FLAG_AT_ONE_PERCENT.to_owned(),
        )];

        runtime
            .preflight(&changes)
            .await
            .map_err(|()| invariant_error("valid protocol changes must pass preflight"))?;
        runtime
            .apply_committed(&changes)
            .await
            .map_err(|()| invariant_error("valid protocol changes must install"))?;

        let after = control.snapshot();
        assert_eq!(after.generation(), before.generation() + 1);
        assert_eq!(after.config().conversion_engine_v2.canary_basis_points, 100);
        Ok(())
    }

    #[tokio::test]
    async fn invalid_preflight_does_not_mutate_control_or_option_map() -> TestResult {
        let runtime = runtime_with_control().await?;
        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let before = control.snapshot();
        let changes = vec![(
            "conversion_engine_v2".to_owned(),
            "{malformed rollout json}".to_owned(),
        )];

        assert!(runtime.preflight(&changes).await.is_err());
        assert_eq!(control.snapshot(), before);
        assert!(
            !runtime
                .snapshot()
                .await
                .contains_key("conversion_engine_v2")
        );
        Ok(())
    }

    #[tokio::test]
    async fn unrelated_option_does_not_advance_rollout_generation() -> TestResult {
        let runtime = runtime_with_control().await?;
        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let before = control.snapshot();
        let changes = vec![("unrelated_option".to_owned(), "new-value".to_owned())];

        runtime.preflight(&changes).await.map_err(|()| {
            invariant_error("unrelated options preserve legacy preflight behavior")
        })?;
        runtime
            .apply_committed(&changes)
            .await
            .map_err(|()| invariant_error("unrelated options must update the option map"))?;

        assert_eq!(control.snapshot().generation(), before.generation());
        assert_eq!(
            runtime
                .snapshot()
                .await
                .get("unrelated_option")
                .map(String::as_str),
            Some("new-value")
        );
        Ok(())
    }

    #[tokio::test]
    async fn emergency_rollback_wins_over_malformed_same_batch_values() -> TestResult {
        let runtime = runtime_with_control().await?;
        let changes = vec![
            (
                "conversion_engine_v2".to_owned(),
                "not-json-and-not-a-flag".to_owned(),
            ),
            ("protocol_rollout_rollback".to_owned(), "true".to_owned()),
        ];

        runtime.preflight(&changes).await.map_err(|()| {
            invariant_error("true rollback must fail closed before parsing stale values")
        })?;
        runtime
            .apply_committed(&changes)
            .await
            .map_err(|()| invariant_error("true rollback must install even with stale values"))?;
        let snapshot = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?
            .snapshot();
        assert_eq!(snapshot.status(), ProtocolRolloutSnapshotStatus::Rollback);
        assert!(snapshot.is_fail_closed());
        Ok(())
    }

    #[tokio::test]
    async fn duplicate_rollback_keys_use_only_the_last_value() -> TestResult {
        let runtime = runtime_with_control().await?;
        let changes = vec![
            ("protocol_rollout_rollback".to_owned(), "true".to_owned()),
            (
                "conversion_engine_v2".to_owned(),
                FLAG_AT_ONE_PERCENT.to_owned(),
            ),
            ("protocol_rollout_rollback".to_owned(), "false".to_owned()),
        ];

        runtime
            .preflight(&changes)
            .await
            .map_err(|()| invariant_error("the final rollback=false must win"))?;
        runtime
            .apply_committed(&changes)
            .await
            .map_err(|()| invariant_error("the final rollback=false must install the candidate"))?;
        let snapshot = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?
            .snapshot();
        assert_eq!(snapshot.status(), ProtocolRolloutSnapshotStatus::Active);
        assert_eq!(
            snapshot.config().conversion_engine_v2.canary_basis_points,
            100
        );
        Ok(())
    }

    #[tokio::test]
    async fn rollback_false_rebuilds_prior_persisted_flags_from_startup_baseline() -> TestResult {
        let mut initial = BTreeMap::new();
        initial.insert(
            "conversion_engine_v2".to_owned(),
            FLAG_AT_ONE_PERCENT.to_owned(),
        );
        initial.insert("protocol_rollout_rollback".to_owned(), "true".to_owned());
        let runtime = ProcessRuntimeOptions::new(initial)
            .with_protocol_rollout(ProtocolRolloutConfig::default())
            .await?;
        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        assert!(control.snapshot().is_fail_closed());

        let recovery = vec![("protocol_rollout_rollback".to_owned(), "false".to_owned())];
        runtime
            .preflight(&recovery)
            .await
            .map_err(|()| invariant_error("rollback=false must rebuild persisted prior flags"))?;
        runtime
            .apply_committed(&recovery)
            .await
            .map_err(|()| invariant_error("rollback=false must install the rebuilt candidate"))?;
        let recovered = control.snapshot();
        assert_eq!(recovered.status(), ProtocolRolloutSnapshotStatus::Active);
        assert_eq!(
            recovered.config().conversion_engine_v2.canary_basis_points,
            100
        );
        Ok(())
    }

    #[tokio::test]
    async fn rollback_recovery_requires_full_validation() -> TestResult {
        let runtime = runtime_with_control().await?;
        let emergency = vec![("protocol_rollout_rollback".to_owned(), "true".to_owned())];
        runtime
            .apply_committed(&emergency)
            .await
            .map_err(|()| invariant_error("rollback must install"))?;
        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let before_recovery = control.snapshot();
        let malformed_recovery = vec![
            ("protocol_rollout_rollback".to_owned(), "false".to_owned()),
            ("conversion_engine_v2".to_owned(), "malformed".to_owned()),
        ];
        assert!(runtime.preflight(&malformed_recovery).await.is_err());
        assert_eq!(control.snapshot(), before_recovery);

        let valid_recovery = vec![
            ("protocol_rollout_rollback".to_owned(), "false".to_owned()),
            (
                "conversion_engine_v2".to_owned(),
                FLAG_AT_FIVE_PERCENT.to_owned(),
            ),
        ];
        runtime
            .preflight(&valid_recovery)
            .await
            .map_err(|()| invariant_error("recovery must validate every replacement value"))?;
        runtime
            .apply_committed(&valid_recovery)
            .await
            .map_err(|()| invariant_error("valid recovery must install"))?;
        let recovered = control.snapshot();
        assert_eq!(recovered.status(), ProtocolRolloutSnapshotStatus::Active);
        assert_eq!(
            recovered.config().conversion_engine_v2.canary_basis_points,
            500
        );
        Ok(())
    }

    #[tokio::test]
    async fn concurrent_same_key_replacements_keep_map_and_control_coherent() -> TestResult {
        let runtime = runtime_with_control().await?;
        let left = runtime.clone();
        let right = runtime.clone();
        let left_change = vec![(
            "conversion_engine_v2".to_owned(),
            FLAG_AT_ONE_PERCENT.to_owned(),
        )];
        let right_change = vec![(
            "conversion_engine_v2".to_owned(),
            FLAG_AT_FIVE_PERCENT.to_owned(),
        )];

        let (left_result, right_result) = tokio::join!(
            left.apply_committed(&left_change),
            right.apply_committed(&right_change),
        );
        assert!(left_result.is_ok());
        assert!(right_result.is_ok());

        let control = runtime
            .protocol_rollout()
            .ok_or_else(|| invariant_error("builder must install the shared control"))?;
        let snapshot = control.snapshot();
        let values = runtime.snapshot().await;
        let persisted = values
            .get("conversion_engine_v2")
            .ok_or_else(|| invariant_error("same-key update must remain in the option map"))?;
        let persisted_flag = from_str::<FlagConfig>(persisted).map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidData,
                format!("the final option map value must remain valid JSON: {error}"),
            )
        })?;
        assert_eq!(snapshot.config().conversion_engine_v2, persisted_flag);
        assert_eq!(snapshot.generation(), 2);
        Ok(())
    }
}

#[cfg(test)]
mod assistant_group_option_tests {
    use super::validate_option_update;
    use std::collections::BTreeMap;

    #[test]
    fn assistant_group_must_exist_in_group_ratio() {
        let options = BTreeMap::from([(
            "GroupRatio".to_owned(),
            r#"{"default":1,"premium":2}"#.to_owned(),
        )]);
        assert!(validate_option_update("AssistantGroup", "premium", &options).is_ok());
        assert!(validate_option_update("AssistantGroup", "missing", &options).is_err());
        assert!(validate_option_update("AssistantGroup", "", &options).is_err());
        assert!(validate_option_update("AssistantReviewGroup", "premium", &options).is_ok());
        assert!(validate_option_update("AssistantReviewGroup", "missing", &options).is_err());
        assert!(validate_option_update("AssistantReviewModel", "review-live", &options).is_ok());
        assert!(validate_option_update("AssistantReviewModel", "", &options).is_err());
        assert!(
            validate_option_update("AssistantReviewModel", &"x".repeat(129), &options).is_err()
        );
        assert!(validate_option_update("AssistantReasoningEffort", "xhigh", &options).is_ok());
        assert!(validate_option_update("AssistantReviewReasoningEffort", "max", &options).is_ok());
        assert!(
            validate_option_update("AssistantReviewReasoningEffort", "ultra", &options).is_err()
        );
        assert!(validate_option_update("AssistantL1AutoApprovalUserIDs", "7,42", &options).is_ok());
        assert!(
            validate_option_update("AssistantL1AutoApprovalUserIDs", "7,nope", &options).is_err()
        );
    }
}

/// Server-validated identity used by configuration audit records.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SystemConfigIdentity {
    pub user_id: i64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SystemConfigCredential {
    DashboardSession,
    PersonalAccessToken,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SystemConfigAuthContext {
    pub identity: SystemConfigIdentity,
    pub credential: SystemConfigCredential,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum SystemConfigAuthRejection {
    /// ConsoleAccessGate hides system configuration discovery until the
    /// credential resolves to an activated developer account.
    ConsoleNotFound,
    Unauthorized {
        supplied: bool,
    },
    TokenExpired,
    SessionRevoked,
    UserDisabled,
    InvalidUser,
    InsufficientPrivilege,
    Internal,
}

/// Root-session adapter backed by the shared dashboard-auth service.
///
/// The adapter intentionally accepts only an HTTP Bearer credential and lets
/// the shared PostgreSQL/Valkey auth service validate session revocation and
/// user status.  It never trusts a caller-provided role header or body field.
#[derive(Clone)]
pub struct DashboardRootAuthorizer {
    auth: Arc<dyn crate::auth::DashboardAuth>,
}

impl DashboardRootAuthorizer {
    #[must_use]
    pub fn new(auth: Arc<dyn crate::auth::DashboardAuth>) -> Self {
        Self { auth }
    }
}

#[async_trait]
impl SystemConfigAuthorizer for DashboardRootAuthorizer {
    async fn require_root_dashboard_session(
        &self,
        headers: &HeaderMap,
    ) -> Result<SystemConfigIdentity, ()> {
        self.authorize_root(headers)
            .await
            .map(|context| context.identity)
            .map_err(|_| ())
    }

    async fn authorize_root(
        &self,
        headers: &HeaderMap,
    ) -> Result<SystemConfigAuthContext, SystemConfigAuthRejection> {
        use crate::auth::{UserAuthPolicyError, enforce_user_auth_view};

        let token = crate::migration_routes::legacy_http::dashboard_credential(headers)
            .ok_or(SystemConfigAuthRejection::ConsoleNotFound)?;
        let credential = if crate::auth::dashboard_token_candidate(&token) {
            SystemConfigCredential::DashboardSession
        } else {
            SystemConfigCredential::PersonalAccessToken
        };
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(token))
            .await
            .map_err(|_| SystemConfigAuthRejection::ConsoleNotFound)?;
        if !user.developer_access_granted {
            return Err(SystemConfigAuthRejection::ConsoleNotFound);
        }
        enforce_user_auth_view(&user).map_err(|error| match error {
            UserAuthPolicyError::UserDisabled => SystemConfigAuthRejection::UserDisabled,
            UserAuthPolicyError::InsufficientPrivilege => {
                SystemConfigAuthRejection::InsufficientPrivilege
            }
            UserAuthPolicyError::InvalidUserInfo => SystemConfigAuthRejection::InvalidUser,
        })?;
        if user.role < 100 {
            return Err(SystemConfigAuthRejection::InsufficientPrivilege);
        }
        Ok(SystemConfigAuthContext {
            identity: SystemConfigIdentity { user_id: user.id },
            credential,
        })
    }
}

/// Outbound Pancake gateway boundary. The production adapter owns credentials,
/// request timeouts, and upstream response validation while this route
/// preserves the legacy envelope.
#[async_trait]
pub trait WaffoPancakeGateway: Send + Sync {
    async fn catalog(&self, merchant_id: &str, private_key: &str) -> Result<Value, ()>;
    async fn create_pair(
        &self,
        merchant_id: &str,
        private_key: &str,
        return_url: &str,
    ) -> Result<Value, Value>;
    async fn create_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        return_url: &str,
    ) -> Result<Value, ()>;
    async fn create_subscription_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        billing_period: &str,
        return_url: &str,
    ) -> Result<Value, ()>;
}

/// Concrete Waffo Pancake merchant adapter.
///
/// It deliberately has no credential fields and never reads environment
/// variables: credentials are resolved by the authenticated route from the
/// server-side PostgreSQL option set (or a root operator's in-flight
/// configuration verification) immediately before this adapter is called.
/// The supplied client must come from [`crate::outbound_http::client`], which
/// enforces HTTPS, bounded timeouts, and disabled redirects.  The base URL is
/// pinned to Pancake's production API rather than being operator-configurable,
/// so the allowlist cannot be redirected to an arbitrary host.
#[derive(Clone)]
pub struct HttpWaffoPancakeGateway {
    client: reqwest::Client,
}

impl HttpWaffoPancakeGateway {
    /// Builds the adapter from a caller-owned outbound client.
    ///
    /// Production wiring should prefer [`Self::production`]; this constructor
    /// exists for composition with the application's already-built shared
    /// client and must not receive a permissive client.
    #[must_use]
    pub fn new(client: reqwest::Client) -> Self {
        Self { client }
    }

    /// Builds an independently safe production adapter.
    ///
    /// This is the only constructor which creates a client itself, and it
    /// delegates all transport policy to `outbound_http`: HTTPS only, bounded
    /// connect/request deadlines, and no redirect following.
    pub fn production(timeout: Duration) -> Result<Self, crate::outbound_http::OutboundHttpError> {
        crate::outbound_http::client(timeout).map(Self::new)
    }

    async fn post(
        &self,
        merchant_id: &str,
        private_key: &str,
        path: &'static str,
        body: Value,
        idempotent: bool,
    ) -> Result<PancakeEnvelope, ()> {
        let merchant_id = nonempty(merchant_id)?;
        let private_key = parse_pancake_private_key(private_key)?;
        let body = serde_json::to_vec(&body).map_err(|_| ())?;
        let timestamp = chrono_seconds().to_string();
        let signature = pancake_signature(path, &timestamp, &body, &private_key)?;
        let url = format!("{PANCAKE_API_BASE_URL}{path}");
        let mut request = self
            .client
            .post(url)
            .header(header::CONTENT_TYPE, "application/json")
            .header("x-merchant-id", merchant_id)
            .header("x-timestamp", timestamp)
            .header("x-signature", signature);
        if idempotent {
            request = request.header(
                "x-idempotency-key",
                pancake_idempotency_key(merchant_id, path, &body),
            );
        }
        let response = request.body(body).send().await.map_err(|_| ())?;
        if !response.status().is_success() {
            return Err(());
        }
        let body = pancake_response_body(response).await?;
        serde_json::from_slice(&body).map_err(|_| ())
    }

    async fn create_store(
        &self,
        merchant_id: &str,
        private_key: &str,
    ) -> Result<(String, String), ()> {
        let envelope = self
            .post(
                merchant_id,
                private_key,
                PANCAKE_CREATE_STORE_PATH,
                json!({"name": PANCAKE_STORE_NAME}),
                true,
            )
            .await?;
        let store = envelope
            .success_data()
            .ok_or(())?
            .get("store")
            .ok_or(())?
            .clone();
        let id = required_string(&store, "id").ok_or(())?;
        let name = required_string(&store, "name").ok_or(())?;
        Ok((id, name))
    }

    async fn create_and_publish_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        return_url: &str,
    ) -> Result<Value, ()> {
        let store_id = nonempty(store_id)?;
        let name = nonempty(name)?;
        let amount = nonempty(amount)?;
        let mut body = json!({
            "storeId": store_id,
            "name": name,
            "prices": {"USD": {"amount": amount, "taxCategory": PANCAKE_TAX_CATEGORY}},
        });
        if let Some(object) = body.as_object_mut()
            && let Some(return_url) = nonempty_optional(return_url)
        {
            object.insert("successUrl".to_owned(), json!(return_url));
        }
        let product = self
            .post(
                merchant_id,
                private_key,
                PANCAKE_CREATE_PRODUCT_PATH,
                body,
                true,
            )
            .await?
            .success_data()
            .ok_or(())?
            .get("product")
            .ok_or(())?
            .clone();
        let product_id = required_string(&product, "id").ok_or(())?;
        self.post(
            merchant_id,
            private_key,
            PANCAKE_PUBLISH_PRODUCT_PATH,
            json!({"id": product_id}),
            true,
        )
        .await?
        .success_data()
        .ok_or(())?;
        Ok(product)
    }

    async fn create_and_publish_subscription_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        billing_period: &str,
        return_url: &str,
    ) -> Result<Value, ()> {
        let store_id = nonempty(store_id)?;
        let name = nonempty(name)?;
        let amount = nonempty(amount)?;
        let billing_period = match billing_period {
            "weekly" | "monthly" | "quarterly" | "yearly" => billing_period,
            _ => return Err(()),
        };
        let mut body = json!({
            "storeId": store_id,
            "name": name,
            "billingPeriod": billing_period,
            "prices": {"USD": {"amount": amount, "taxCategory": PANCAKE_TAX_CATEGORY}},
        });
        if let Some(object) = body.as_object_mut()
            && let Some(return_url) = nonempty_optional(return_url)
        {
            object.insert("successUrl".to_owned(), json!(return_url));
        }
        let product = self
            .post(
                merchant_id,
                private_key,
                PANCAKE_CREATE_SUBSCRIPTION_PRODUCT_PATH,
                body,
                true,
            )
            .await?
            .success_data()
            .ok_or(())?
            .get("product")
            .ok_or(())?
            .clone();
        let product_id = required_string(&product, "id").ok_or(())?;
        if !required_string(&product, "status")
            .is_some_and(|status| status.eq_ignore_ascii_case("active"))
        {
            let published = self
                .post(
                    merchant_id,
                    private_key,
                    PANCAKE_PUBLISH_SUBSCRIPTION_PRODUCT_PATH,
                    json!({"id": product_id}),
                    true,
                )
                .await?
                .success_data()
                .ok_or(())?
                .get("product")
                .cloned()
                .ok_or(())?;
            if !required_string(&published, "status")
                .is_some_and(|status| status.eq_ignore_ascii_case("active"))
            {
                return Err(());
            }
            return Ok(published);
        }
        Ok(product)
    }
}

#[async_trait]
impl WaffoPancakeGateway for HttpWaffoPancakeGateway {
    async fn catalog(&self, merchant_id: &str, private_key: &str) -> Result<Value, ()> {
        let stores_envelope = self
            .post(
                merchant_id,
                private_key,
                PANCAKE_GRAPHQL_PATH,
                json!({"query": "query { stores(limit: 100) { id name status prodEnabled } }"}),
                false,
            )
            .await?;
        let stores_data = stores_envelope.success_data().ok_or(())?;
        let mut stores = stores_data
            .get("stores")
            .and_then(Value::as_array)
            .cloned()
            .ok_or(())?;
        for store in &mut stores {
            let store_id = required_string(store, "id").ok_or(())?;
            let products_envelope = self
                .post(
                    merchant_id,
                    private_key,
                    PANCAKE_GRAPHQL_PATH,
                    json!({
                        "query": "query ($storeId: String!) { onetimeProducts(storeId: $storeId, filter: { status: { eq: \"active\" } }) { id name status } subscriptionProducts(storeId: $storeId, filter: { status: { eq: \"active\" } }) { id name status billingPeriod } }",
                        "variables": {"storeId": store_id},
                    }),
                    false,
                )
                .await?;
            let products = products_envelope.success_data().ok_or(())?;
            let store_object = store.as_object_mut().ok_or(())?;
            store_object.insert(
                "onetimeProducts".to_owned(),
                products
                    .get("onetimeProducts")
                    .cloned()
                    .unwrap_or_else(|| json!([])),
            );
            store_object.insert(
                "subscriptionProducts".to_owned(),
                products
                    .get("subscriptionProducts")
                    .cloned()
                    .unwrap_or_else(|| json!([])),
            );
        }
        normalize_pancake_catalog(json!({"stores": stores}))
    }

    async fn create_pair(
        &self,
        merchant_id: &str,
        private_key: &str,
        return_url: &str,
    ) -> Result<Value, Value> {
        let (store_id, store_name) = self
            .create_store(merchant_id, private_key)
            .await
            .map_err(|_| json!({"error":"创建 Waffo Pancake 店铺失败"}))?;
        match self
            .create_and_publish_product(
                merchant_id,
                private_key,
                &store_id,
                PANCAKE_PRIMARY_PRODUCT_NAME,
                "1.00",
                return_url,
            )
            .await
        {
            Ok(product) => Ok(json!({
                "store_id": store_id,
                "store_name": store_name,
                "product_id": required_string(&product, "id").unwrap_or_default(),
                "product_name": required_string(&product, "name").unwrap_or_default(),
            })),
            Err(()) => Err(json!({
                "error":"店铺已创建，但 Waffo Pancake 产品创建失败",
                "store_id": store_id,
                "store_name": store_name,
                "orphan_store": true,
            })),
        }
    }

    async fn create_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        return_url: &str,
    ) -> Result<Value, ()> {
        self.create_and_publish_product(
            merchant_id,
            private_key,
            store_id,
            name,
            amount,
            return_url,
        )
        .await
    }

    async fn create_subscription_product(
        &self,
        merchant_id: &str,
        private_key: &str,
        store_id: &str,
        name: &str,
        amount: &str,
        billing_period: &str,
        return_url: &str,
    ) -> Result<Value, ()> {
        self.create_and_publish_subscription_product(
            merchant_id,
            private_key,
            store_id,
            name,
            amount,
            billing_period,
            return_url,
        )
        .await
    }
}

/// Explicit no-egress adapter for the public test instance.
///
/// This type has no HTTP client and therefore cannot contact Pancake even if
/// it is accidentally wired into a test listener.  It is deliberately not a
/// fixture or success fake: every operation fails closed.
#[derive(Clone, Copy, Debug, Default)]
pub struct TestInstanceDisabledWaffoPancakeGateway;

#[async_trait]
impl WaffoPancakeGateway for TestInstanceDisabledWaffoPancakeGateway {
    async fn catalog(&self, _: &str, _: &str) -> Result<Value, ()> {
        Err(())
    }

    async fn create_pair(&self, _: &str, _: &str, _: &str) -> Result<Value, Value> {
        Err(json!({"error":"Waffo Pancake 在测试实例中已禁用"}))
    }

    async fn create_product(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<Value, ()> {
        Err(())
    }

    async fn create_subscription_product(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<Value, ()> {
        Err(())
    }
}

#[derive(Deserialize)]
struct PancakeEnvelope {
    data: Value,
    #[serde(default)]
    errors: Vec<Value>,
}

impl PancakeEnvelope {
    fn success_data(self) -> Option<Value> {
        self.errors.is_empty().then_some(self.data)
    }
}

async fn pancake_response_body(mut response: reqwest::Response) -> Result<Vec<u8>, ()> {
    if response.content_length().is_some_and(|length| {
        usize::try_from(length).map_or(true, |length| length > MAX_PANCAKE_RESPONSE_BYTES)
    }) {
        return Err(());
    }
    let mut body = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| ())? {
        if body.len().saturating_add(chunk.len()) > MAX_PANCAKE_RESPONSE_BYTES {
            return Err(());
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn pancake_signature(
    path: &str,
    timestamp: &str,
    body: &[u8],
    private_key: &RsaPrivateKey,
) -> Result<String, ()> {
    let body_hash = BASE64.encode(Sha256::digest(body));
    let canonical = format!("POST\n{path}\n{timestamp}\n{body_hash}");
    let signature = SigningKey::<Sha256>::new(private_key.clone()).sign(canonical.as_bytes());
    Ok(BASE64.encode(signature.to_vec()))
}

fn pancake_idempotency_key(merchant_id: &str, path: &str, body: &[u8]) -> String {
    let mut digest = Sha256::new();
    digest.update(merchant_id.as_bytes());
    digest.update(b":");
    digest.update(path.as_bytes());
    digest.update(b":");
    digest.update(body);
    hex::encode(digest.finalize())
}

fn parse_pancake_private_key(raw: &str) -> Result<RsaPrivateKey, ()> {
    let normalized = raw.replace("\\n", "\n").replace("\r\n", "\n");
    let normalized = normalized.trim();
    if normalized.is_empty() {
        return Err(());
    }
    if normalized.contains("-----BEGIN RSA PRIVATE KEY-----") {
        // gitleaks:allow -- PEM boundary marker
        return RsaPrivateKey::from_pkcs1_pem(normalized).map_err(|_| ());
    }
    if normalized.contains("-----BEGIN PRIVATE KEY-----") {
        // gitleaks:allow -- PEM boundary marker
        return RsaPrivateKey::from_pkcs8_pem(normalized).map_err(|_| ());
    }
    let raw = BASE64.decode(normalized).map_err(|_| ())?;
    RsaPrivateKey::from_pkcs8_der(&raw).map_err(|_| ())
}

fn normalize_pancake_catalog(data: Value) -> Result<Value, ()> {
    let stores = data.get("stores").and_then(Value::as_array).ok_or(())?;
    let mut normalized = Vec::with_capacity(stores.len());
    for store in stores {
        let mut active_one_time = Vec::new();
        for product in store
            .get("onetimeProducts")
            .and_then(Value::as_array)
            .map(Vec::as_slice)
            .unwrap_or_default()
        {
            let status = required_string(product, "status").ok_or(())?;
            if status.eq_ignore_ascii_case("active") {
                active_one_time.push(json!({
                    "id": required_string(product, "id").ok_or(())?,
                    "name": required_string(product, "name").ok_or(())?,
                    "status": status,
                }));
            }
        }
        let mut active_subscriptions = Vec::new();
        for product in store
            .get("subscriptionProducts")
            .and_then(Value::as_array)
            .map(Vec::as_slice)
            .unwrap_or_default()
        {
            let status = required_string(product, "status").ok_or(())?;
            if status.eq_ignore_ascii_case("active") {
                active_subscriptions.push(json!({
                    "id": required_string(product, "id").ok_or(())?,
                    "name": required_string(product, "name").ok_or(())?,
                    "status": status,
                    "billingPeriod": required_string(product, "billingPeriod").ok_or(())?,
                }));
            }
        }
        normalized.push(json!({
            "id": required_string(store, "id").ok_or(())?,
            "name": required_string(store, "name").ok_or(())?,
            "status": required_string(store, "status").ok_or(())?,
            "prodEnabled": store.get("prodEnabled").and_then(Value::as_bool).unwrap_or(false),
            "onetimeProducts": active_one_time,
            "subscriptionProducts": active_subscriptions,
        }));
    }
    Ok(json!({"stores": normalized}))
}

fn required_string(value: &Value, key: &str) -> Option<String> {
    value
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(ToOwned::to_owned)
}

fn nonempty(value: &str) -> Result<&str, ()> {
    let value = value.trim();
    (!value.is_empty()).then_some(value).ok_or(())
}

fn nonempty_optional(value: &str) -> Option<&str> {
    let value = value.trim();
    (!value.is_empty()).then_some(value)
}

#[cfg(test)]
mod pancake_gateway_tests {
    use super::{
        HttpWaffoPancakeGateway, TestInstanceDisabledWaffoPancakeGateway, WaffoPancakeGateway,
        normalize_pancake_catalog, pancake_billing_period, pancake_idempotency_key,
        pancake_plan_product_options, pancake_plan_product_type, parse_pancake_private_key,
        subscription_price_in_usd,
    };
    use serde_json::json;
    use std::{io, time::Duration};

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[test]
    fn production_constructor_uses_the_bounded_outbound_policy() {
        assert!(HttpWaffoPancakeGateway::production(Duration::ZERO).is_err());
        assert!(HttpWaffoPancakeGateway::production(Duration::from_millis(1)).is_ok());
    }

    #[tokio::test]
    async fn test_instance_gateway_has_no_success_path_or_network_client() {
        let gateway = TestInstanceDisabledWaffoPancakeGateway;
        assert!(gateway.catalog("MER_test", "secret").await.is_err());
        assert!(
            gateway
                .create_product("MER_test", "secret", "STO_test", "plan", "1.00", "")
                .await
                .is_err()
        );
        assert!(
            gateway
                .create_subscription_product(
                    "MER_test", "secret", "STO_test", "plan", "1.00", "monthly", "",
                )
                .await
                .is_err()
        );
        assert_eq!(
            gateway.create_pair("MER_test", "secret", "").await,
            Err(json!({"error":"Waffo Pancake 在测试实例中已禁用"}))
        );
    }

    #[test]
    fn catalog_keeps_only_active_products_in_the_go_shape() -> TestResult {
        let catalog = normalize_pancake_catalog(json!({
            "stores": [{
                "id": "STO_1",
                "name": "Store",
                "status": "active",
                "prodEnabled": true,
                "onetimeProducts": [
                    {"id":"PROD_active","name":"Active","status":"ACTIVE"},
                    {"id":"PROD_draft","name":"Draft","status":"draft"}
                ],
                "subscriptionProducts": [
                    {"id":"PROD_monthly","name":"Monthly","status":"active","billingPeriod":"monthly"},
                    {"id":"PROD_subscription_draft","name":"Draft","status":"draft","billingPeriod":"monthly"}
                ]
            }]
        }))
        .map_err(|()| io::Error::new(io::ErrorKind::InvalidData, "valid Pancake catalog"))?;
        assert_eq!(
            catalog,
            json!({"stores":[{
                "id":"STO_1",
                "name":"Store",
                "status":"active",
                "prodEnabled":true,
                "onetimeProducts":[{"id":"PROD_active","name":"Active","status":"ACTIVE"}],
                "subscriptionProducts":[{"id":"PROD_monthly","name":"Monthly","status":"active","billingPeriod":"monthly"}]
            }]})
        );
        assert_eq!(
            pancake_plan_product_options(&catalog, "STO_1"),
            vec![
                json!({"id":"PROD_active","name":"Active","status":"ACTIVE","product_type":"one_time"}),
                json!({"id":"PROD_monthly","name":"Monthly","status":"active","billingPeriod":"monthly","product_type":"subscription"}),
            ]
        );
        Ok(())
    }

    #[test]
    fn recurring_cadence_and_fiat_conversion_are_explicit() {
        assert_eq!(pancake_plan_product_type(None), Ok("subscription"));
        assert_eq!(pancake_plan_product_type(Some("one_time")), Ok("one_time"));
        assert_eq!(pancake_plan_product_type(Some("invalid")), Err(()));
        let mut options = std::collections::BTreeMap::new();
        options.insert("USDExchangeRate".to_owned(), "6.8".to_owned());
        assert_eq!(pancake_billing_period("day", 7), Ok("weekly"));
        assert_eq!(pancake_billing_period("month", 3), Ok("quarterly"));
        assert_eq!(pancake_billing_period("custom", 1), Err(()));
        assert_eq!(
            subscription_price_in_usd("6.8", "CNY", &options),
            Ok("1.00".to_owned())
        );
        assert_eq!(
            subscription_price_in_usd("6.8", "USD", &options),
            Ok("6.80".to_owned())
        );
        assert!(subscription_price_in_usd("6.8", "EUR", &options).is_err());
    }

    #[test]
    fn signed_protocol_rejects_absent_or_malformed_private_keys() {
        assert!(parse_pancake_private_key(" ").is_err());
        assert!(parse_pancake_private_key("not a private key").is_err());
        assert_eq!(
            pancake_idempotency_key("MER_abc", "/v1/graphql", b"{}"),
            "5cacd792a8d1f3c40bff50f88df16b9db208992afac0e120e57e852e9c3c8a94"
        );
    }
}

#[derive(Clone)]
pub struct SystemConfigHttpState {
    pub pg: PgPool,
    pub valkey: redis::Client,
    pub authorizer: Arc<dyn SystemConfigAuthorizer>,
    pub project_update: Arc<dyn ProjectUpdateClient>,
    pub exchange_rate: Arc<dyn ExchangeRateProvider>,
    pub pancake: Arc<dyn WaffoPancakeGateway>,
    anonymous_body_limit_bytes: usize,
    runtime_writer: Arc<dyn SystemConfigRuntimeWriter>,
    option_write_lock: Arc<Mutex<()>>,
    pub option_cache_ttl: Duration,
    option_cache_dirty: Arc<AtomicBool>,
    runtime_coherent: Arc<AtomicBool>,
}

impl SystemConfigHttpState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        valkey: redis::Client,
        authorizer: Arc<dyn SystemConfigAuthorizer>,
        project_update: Arc<dyn ProjectUpdateClient>,
        pancake: Arc<dyn WaffoPancakeGateway>,
    ) -> Self {
        Self {
            pg,
            valkey,
            authorizer,
            project_update,
            exchange_rate: Arc::new(PinnedExchangeRateProvider::production()),
            pancake,
            anonymous_body_limit_bytes: 512 * 1024,
            runtime_writer: Arc::new(MissingSystemConfigRuntimeWriter),
            option_write_lock: Arc::new(Mutex::new(())),
            option_cache_ttl: Duration::from_secs(OPTIONS_CACHE_TTL_SECONDS),
            option_cache_dirty: Arc::new(AtomicBool::new(false)),
            runtime_coherent: Arc::new(AtomicBool::new(true)),
        }
    }

    /// Binds the one shared process runtime that owns legacy-compatible
    /// `OptionMap` updates.  Listener composition must opt in explicitly;
    /// the default is fail-closed for all configuration writes.
    #[must_use]
    pub fn with_runtime_writer(
        mut self,
        runtime_writer: Arc<dyn SystemConfigRuntimeWriter>,
    ) -> Self {
        self.runtime_writer = runtime_writer;
        self
    }

    /// Replaces the pinned exchange-rate client. This seam exists for bounded
    /// deterministic tests; production composition uses the fixed providers.
    #[must_use]
    pub fn with_exchange_rate_provider(
        mut self,
        exchange_rate: Arc<dyn ExchangeRateProvider>,
    ) -> Self {
        self.exchange_rate = exchange_rate;
        self
    }

    /// Sets the configured ceiling for the anonymous setup request body.
    #[must_use]
    pub const fn with_anonymous_body_limit_bytes(mut self, bytes: usize) -> Self {
        self.anonymous_body_limit_bytes = bytes;
        self
    }

    /// Readiness composition must include this bit before any system-config
    /// route can own production traffic. It flips false immediately after a
    /// durable write and returns true only after runtime and Valkey converge.
    #[must_use]
    pub fn runtime_coherent(&self) -> bool {
        self.runtime_coherent.load(Ordering::Acquire)
    }
}

/// System-config routes retained by the current Go listener contract.
pub fn system_config_router(state: SystemConfigHttpState) -> Router {
    let catalog = Router::new()
        .route(
            "/api/option/waffo-pancake/catalog",
            post(pancake_catalog_post),
        )
        .layer(DefaultBodyLimit::max(
            WAFFO_PANCAKE_MUTATION_REQUEST_MAX_BYTES,
        ));
    let protected = Router::new()
        .route("/api/option/", get(get_options).put(update_option))
        .route(
            "/api/option/channel_affinity_cache",
            get(affinity_stats).delete(clear_affinity_cache),
        )
        .route(
            "/api/option/payment_compliance",
            post(confirm_payment_compliance),
        )
        .route("/api/option/project-update", get(project_update))
        .route("/api/option/exchange-rate", get(exchange_rate))
        .route("/api/option/rest_model_ratio", post(reset_model_ratio))
        .merge(catalog)
        .route("/api/option/bulk", post(update_options_bulk))
        .route("/api/option/validate", post(validate_options))
        .route("/api/option/waffo-pancake/pair", post(pancake_pair))
        .route("/api/option/waffo-pancake/save", post(pancake_save))
        .route(
            "/api/option/waffo-pancake/subscription-product",
            post(pancake_subscription_product),
        )
        .route(
            "/api/option/waffo-pancake/subscription-product-options",
            get(pancake_subscription_product_options),
        )
        // Run before every body extractor so malformed unauthenticated input
        // remains an auth failure, exactly as the legacy RootAuth group did.
        .route_layer(middleware::from_fn_with_state(
            state.clone(),
            system_config_root_guard,
        ));
    let setup = Router::new()
        .route("/api/setup", get(get_setup).post(post_setup))
        // `post_setup` consumes `Bytes`; the route layer must install the
        // configured limit before Axum performs that extraction.
        .layer(DefaultBodyLimit::max(state.anonymous_body_limit_bytes));
    Router::new()
        .merge(protected)
        .merge(setup)
        .with_state(state)
}

#[derive(Serialize)]
struct Legacy<T: Serialize> {
    success: bool,
    message: String,
    data: T,
}
fn ok<T: Serialize>(data: T) -> Response {
    legacy_json(
        StatusCode::OK,
        Legacy {
            success: true,
            message: String::new(),
            data,
        },
    )
}
fn legacy_error(message: impl Into<String>) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": false, "message": message.into()}),
    )
}

fn legacy_json(status: StatusCode, body: impl Serialize) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn token_locale(headers: &HeaderMap) -> (bool, bool) {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_ascii_lowercase();
    (language.starts_with("zh-tw"), language.starts_with("zh"))
}

fn not_logged_in(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        (true, _) => "無權進行此操作，未登入且未提供 access token",
        (_, true) => "无权进行此操作，未登录且未提供 access token",
        _ => "Unauthorized, not logged in and no access token provided",
    }
}

fn invalid_access_token(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        (true, _) => "無權進行此操作，access token 無效",
        (_, true) => "无权进行此操作，access token 无效",
        _ => "Unauthorized, invalid access token",
    }
}

fn database_error(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        (true, _) => "資料庫錯誤，請聯絡管理員",
        (_, true) => "数据库错误，请联系管理员",
        _ => "Database error, please contact the administrator",
    }
}

fn user_policy_message(headers: &HeaderMap, rejection: SystemConfigAuthRejection) -> &'static str {
    use crate::auth::{UserAuthPolicyError, user_auth_message};
    let policy = match rejection {
        SystemConfigAuthRejection::UserDisabled => UserAuthPolicyError::UserDisabled,
        SystemConfigAuthRejection::InvalidUser => UserAuthPolicyError::InvalidUserInfo,
        SystemConfigAuthRejection::InsufficientPrivilege => {
            UserAuthPolicyError::InsufficientPrivilege
        }
        _ => return invalid_access_token(headers),
    };
    user_auth_message(
        policy,
        headers
            .get(header::ACCEPT_LANGUAGE)
            .and_then(|value| value.to_str().ok()),
    )
}

fn auth_rejection(headers: &HeaderMap, rejection: SystemConfigAuthRejection) -> Response {
    let (status, code, message) = match rejection {
        SystemConfigAuthRejection::ConsoleNotFound => {
            return legacy_json(StatusCode::NOT_FOUND, json!({"message": "Not Found"}));
        }
        SystemConfigAuthRejection::Unauthorized { supplied } => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            if supplied {
                invalid_access_token(headers)
            } else {
                not_logged_in(headers)
            },
        ),
        SystemConfigAuthRejection::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            not_logged_in(headers),
        ),
        SystemConfigAuthRejection::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            not_logged_in(headers),
        ),
        SystemConfigAuthRejection::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            user_policy_message(headers, rejection),
        ),
        SystemConfigAuthRejection::InvalidUser => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_INVALID",
            user_policy_message(headers, rejection),
        ),
        SystemConfigAuthRejection::InsufficientPrivilege => (
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            user_policy_message(headers, rejection),
        ),
        SystemConfigAuthRejection::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            database_error(headers),
        ),
    };
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn personal_access_token_forbidden(headers: &HeaderMap) -> Response {
    let message = match token_locale(headers) {
        (true, _) => "此操作需要已登入的儀表板工作階段",
        (_, true) => "此操作需要已登录的控制台会话",
        _ => "This operation requires a dashboard login session",
    };
    legacy_json(
        StatusCode::FORBIDDEN,
        json!({"success": false, "code": "AUTH_SESSION_REQUIRED", "message": message}),
    )
}

async fn system_config_root_guard(
    State(state): State<SystemConfigHttpState>,
    mut request: Request,
    next: Next,
) -> Response {
    let context = match state.authorizer.authorize_root(request.headers()).await {
        Ok(context) => context,
        Err(rejection) => return auth_rejection(request.headers(), rejection),
    };
    request.extensions_mut().insert(context);
    let mut response = next.run(request).await;
    response.headers_mut().insert(
        header::HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

async fn cached_options(state: &SystemConfigHttpState) -> Result<BTreeMap<String, String>, ()> {
    if state.runtime_coherent.load(Ordering::Acquire)
        && !state.option_cache_dirty.load(Ordering::Acquire)
        && let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await
        && let Ok(Some(cached)) = connection.get::<_, Option<String>>(OPTIONS_CACHE_KEY).await
    {
        if let Ok(options) = serde_json::from_str(&cached) {
            return Ok(options);
        }
        tracing::warn!("discarding malformed system-config option cache");
    }
    let rows = sqlx::query("SELECT key, value FROM options")
        .fetch_all(&state.pg)
        .await
        .map_err(|_| ())?;
    let options = rows
        .into_iter()
        .map(|row| {
            Ok((
                row.try_get::<String, _>("key")?,
                row.try_get::<Option<String>, _>("value")?
                    .unwrap_or_default(),
            ))
        })
        .collect::<Result<BTreeMap<_, _>, sqlx::Error>>()
        .map_err(|_| ())?;
    if let Ok(mut connection) = state.valkey.get_multiplexed_async_connection().await {
        let ttl = state.option_cache_ttl.as_secs();
        let serialized = serde_json::to_string(&options).map_err(|_| ())?;
        if let Err(error) = connection
            .set_ex::<_, _, ()>(OPTIONS_CACHE_KEY, serialized, ttl)
            .await
        {
            tracing::warn!(%error, "system-config option cache write failed");
        } else {
            state.option_cache_dirty.store(false, Ordering::Release);
            // A cache refill proves only durable/cache convergence; it cannot
            // prove that a previously failed runtime writer has caught up.
        }
    }
    Ok(options)
}

fn mark_runtime_dirty(state: &SystemConfigHttpState) {
    state.option_cache_dirty.store(true, Ordering::Release);
    state.runtime_coherent.store(false, Ordering::Release);
}

async fn invalidate_options(state: &SystemConfigHttpState) -> Result<(), ()> {
    // A failed delete must not allow this process to serve the old value from
    // Valkey.  The dirty bit makes PostgreSQL authoritative until a later
    // successful cache-aside refill overwrites the stale key.
    mark_runtime_dirty(state);
    let mut c = state
        .valkey
        .get_multiplexed_async_connection()
        .await
        .map_err(|error| {
            tracing::warn!(%error, "system-config option cache connection failed");
        })?;
    c.del::<_, usize>(OPTIONS_CACHE_KEY)
        .await
        .map_err(|error| {
            tracing::warn!(%error, "system-config option cache invalidation failed");
        })?;
    state.runtime_coherent.store(true, Ordering::Release);
    Ok(())
}

async fn get_options(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
) -> Response {
    let Ok(options) = cached_options(&state).await else {
        return legacy_error("获取设置失败");
    };
    let completion_ratio_meta = completion_ratio_meta(&options);
    let mut data = options
        .into_iter()
        .filter(|(key, _)| key != "theme.frontend" && !sensitive(key))
        .map(|(key, value)| json!({"key": key, "value": value}))
        .collect::<Vec<_>>();
    data.push(json!({"key":"CompletionRatioMeta", "value":completion_ratio_meta}));
    ok(data)
}
fn sensitive(key: &str) -> bool {
    ["Token", "Secret", "Key", "secret", "api_key"]
        .iter()
        .any(|suffix| key.ends_with(suffix))
        || sensitive_configuration_key(key)
}

fn sensitive_configuration_key(key: &str) -> bool {
    let key = key.to_ascii_lowercase();
    let credential = [
        "password", "private", "secret", "token", "api_key", "apikey",
    ]
    .iter()
    .any(|marker| key.contains(marker));
    credential
        && [
            "smtp", "oauth", "oidc", "payment", "waffo", "stripe", "creem", "epay",
        ]
        .iter()
        .any(|namespace| key.contains(namespace))
}

fn completion_ratio_meta(_: &BTreeMap<String, String>) -> String {
    "{}".to_owned()
}

#[derive(Deserialize)]
struct OptionUpdate {
    key: String,
    value: Value,
}
async fn update_option(
    State(state): State<SystemConfigHttpState>,
    Extension(context): Extension<SystemConfigAuthContext>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let identity = context.identity;
    let input = match serde_json::from_slice::<OptionUpdate>(&body) {
        Ok(input) => input,
        Err(_) => {
            return legacy_json(
                StatusCode::BAD_REQUEST,
                json!({"success":false,"message":"无效的参数"}),
            );
        }
    };
    if input.key.starts_with("payment_setting.compliance_") {
        return legacy_error("合规确认字段不允许通过通用设置接口修改");
    }
    let value = match input.value {
        Value::String(s) => s,
        other => other.to_string().trim_matches('"').to_owned(),
    };
    if input.key == "theme.frontend" && value != "default" {
        return legacy_error("Classic 前端已移除，主题只能设置为 default");
    }
    let options = match cached_options(&state).await {
        Ok(options) => options,
        Err(()) => return legacy_error("保存设置失败"),
    };
    let mut candidate_options = options.clone();
    candidate_options.insert(input.key.clone(), value.clone());
    if let Err(message) = validate_option_update(&input.key, &value, &candidate_options) {
        return legacy_error(message);
    }
    if let Err(message) =
        validate_assistant_model_update(&state, &input.key, &value, &candidate_options).await
    {
        return legacy_error(message);
    }
    let changes = vec![(input.key, value)];
    if persist_option_changes(&state, &changes).await.is_err() {
        return legacy_error("保存设置失败");
    }
    record_option_update_audit(&state.pg, identity, &headers, &changes[0].0).await;
    legacy_json(StatusCode::OK, json!({"success": true, "message": ""}))
}

fn validate_option_update(
    key: &str,
    value: &str,
    options: &BTreeMap<String, String>,
) -> Result<(), String> {
    match key {
        "QuotaForInviter" | "QuotaForInvitee"
            if positive_option_value(value)
                && options
                    .get("payment_setting.compliance_confirmed")
                    .is_none_or(|value| value != "true") =>
        {
            Err("请先确认支付合规声明".to_owned())
        }
        "AssistantGroup" | "AssistantReviewGroup" => {
            let group = value.trim();
            let route_label = if key == "AssistantReviewGroup" {
                "assistant review routing group"
            } else {
                "assistant routing group"
            };
            if group.is_empty() {
                return Err(format!("{route_label} is required"));
            }
            let configured = options
                .get("GroupRatio")
                .ok_or_else(|| format!("{route_label} catalog is unavailable"))?;
            let groups = parse_json_object(configured, "group ratio")?;
            if groups.contains_key(group) {
                Ok(())
            } else {
                Err(format!("{route_label} must be an existing group"))
            }
        }
        "AssistantReasoningEffort" | "AssistantReviewReasoningEffort" => {
            match value.trim().to_ascii_lowercase().as_str() {
                "auto" | "none" | "minimal" | "low" | "medium" | "high" | "xhigh"
                | "max" => Ok(()),
                _ => Err("assistant reasoning effort must be auto, none, minimal, low, medium, high, xhigh, or max".to_owned()),
            }
        }
        "AssistantReviewModel" => {
            let model = value.trim();
            if model.is_empty() {
                Err("assistant review model is required".to_owned())
            } else if model.chars().count() > 128 {
                Err("assistant review model must be at most 128 characters".to_owned())
            } else {
                Ok(())
            }
        }
        "AssistantL1AutoApprovalUserIDs" => validate_positive_id_list(value),
        "GroupRatio" => validate_nonnegative_json_number_map(value, "group ratio"),
        "gemini.safety_settings" => validate_gemini_safety_settings(value),
        "claude.default_max_tokens" => validate_nonnegative_json_integer_map(value),
        "tool_price_setting.prices" => validate_nonnegative_json_number_map(value, "tool price"),
        "AutomaticDisableStatusCodes" | "AutomaticRetryStatusCodes" => {
            validate_status_code_ranges(value)
        }
        "console_setting.api_info"
        | "console_setting.announcements"
        | "console_setting.faq"
        | "console_setting.uptime_kuma_groups" => validate_console_json(value),
        _ => Ok(()),
    }
}

async fn validate_assistant_model_update(
    state: &SystemConfigHttpState,
    key: &str,
    value: &str,
    options: &BTreeMap<String, String>,
) -> Result<(), String> {
    if key != "AssistantReviewModel" {
        return Ok(());
    }
    let group = options
        .get("AssistantReviewGroup")
        .map(String::as_str)
        .map(str::trim)
        .filter(|group| !group.is_empty())
        .unwrap_or("default");
    let enabled = sqlx::query_scalar::<_, bool>(
        r#"SELECT EXISTS(
               SELECT 1 FROM abilities
               WHERE "group" = $1 AND model = $2 AND COALESCE(enabled, TRUE) = TRUE
           )"#,
    )
    .bind(group)
    .bind(value.trim())
    .fetch_one(&state.pg)
    .await
    .map_err(|_| "assistant review model catalog is unavailable".to_owned())?;
    if enabled {
        Ok(())
    } else {
        Err(format!(
            "assistant review model is not enabled in the {group} group; choose a live model from the model list"
        ))
    }
}

fn positive_option_value(value: &str) -> bool {
    value.trim().parse::<f64>().is_ok_and(|value| value > 0.0)
}

fn validate_positive_id_list(value: &str) -> Result<(), String> {
    for token in value.split([',', '，', ' ', '\n', '\t']) {
        let token = token.trim();
        if token.is_empty() {
            continue;
        }
        if !token.parse::<i64>().is_ok_and(|id| id > 0) {
            return Err(
                "assistant automatic approval user IDs must be positive integers".to_owned(),
            );
        }
    }
    Ok(())
}

fn parse_json_object(
    value: &str,
    description: &str,
) -> Result<serde_json::Map<String, Value>, String> {
    serde_json::from_str::<Value>(value)
        .ok()
        .and_then(|value| value.as_object().cloned())
        .ok_or_else(|| format!("{description} must be a JSON object"))
}

fn validate_nonnegative_json_number_map(value: &str, description: &str) -> Result<(), String> {
    for (name, number) in parse_json_object(value, description)? {
        if !number
            .as_f64()
            .is_some_and(|number| number.is_finite() && number >= 0.0)
        {
            return Err(format!(
                "{description} {name:?} must be a finite non-negative number"
            ));
        }
    }
    Ok(())
}

fn validate_nonnegative_json_integer_map(value: &str) -> Result<(), String> {
    for (name, number) in parse_json_object(value, "Claude default max tokens")? {
        let valid = number.as_i64().is_some_and(|value| value >= 0);
        if !valid {
            return Err(format!(
                "Claude default max_tokens must be a non-negative integer for {name:?}"
            ));
        }
    }
    Ok(())
}

fn validate_gemini_safety_settings(value: &str) -> Result<(), String> {
    const THRESHOLDS: &[&str] = &[
        "OFF",
        "BLOCK_NONE",
        "BLOCK_ONLY_HIGH",
        "BLOCK_MEDIUM_AND_ABOVE",
        "BLOCK_LOW_AND_ABOVE",
        "HARM_BLOCK_THRESHOLD_UNSPECIFIED",
    ];
    for (category, threshold) in parse_json_object(value, "Gemini safety settings")? {
        let threshold = threshold
            .as_str()
            .ok_or_else(|| "Gemini safety settings must be a JSON string map".to_owned())?;
        if !threshold.is_empty() && !THRESHOLDS.contains(&threshold) {
            return Err(format!(
                "invalid Gemini safety threshold {threshold:?} for {category:?}"
            ));
        }
    }
    Ok(())
}

fn validate_status_code_ranges(value: &str) -> Result<(), String> {
    for token in value.replace('，', ",").split(',') {
        let token = token.trim().replace(' ', "");
        if token.is_empty() {
            continue;
        }
        let valid = match token.split_once('-') {
            Some((start, end)) if !start.is_empty() && !end.is_empty() => {
                let start = start.parse::<u16>().ok();
                let end = end.parse::<u16>().ok();
                start.zip(end).is_some_and(|(start, end)| {
                    (100..=599).contains(&start) && start <= end && end <= 599
                })
            }
            Some(_) => false,
            None => token
                .parse::<u16>()
                .is_ok_and(|status| (100..=599).contains(&status)),
        };
        if !valid {
            return Err(format!("invalid http status code rules: {token}"));
        }
    }
    Ok(())
}

fn validate_console_json(value: &str) -> Result<(), String> {
    if value.is_empty() {
        return Ok(());
    }
    serde_json::from_str::<Value>(value)
        .ok()
        .filter(Value::is_array)
        .map(|_| ())
        .ok_or_else(|| "console setting must be a JSON array".to_owned())
}

async fn persist_option_changes(
    state: &SystemConfigHttpState,
    changes: &[(String, String)],
) -> Result<(), ()> {
    // The runtime preflight, durable transaction, live replacement, and cache
    // invalidation form one per-process write critical section. PostgreSQL
    // advisory locks cannot serialize this route with setup or another
    // migration process, so the shared state mutex closes the local race.
    let _option_write_guard = state.option_write_lock.lock().await;
    // A missing runtime writer must fail before the durable mutation.  Once a
    // real writer is composed, this mirrors Go's database -> OptionMap ->
    // cache-invalidating sequence.
    state.runtime_writer.preflight(changes).await?;
    write_options(
        &state.pg,
        changes
            .iter()
            .map(|(key, value)| (key.as_str(), value.as_str())),
    )
    .await?;
    // PostgreSQL has committed, so the process is no longer coherent until
    // the shared runtime replacement succeeds. Keep this false on an apply
    // failure; do not mark a transaction that failed before commit.
    mark_runtime_dirty(state);
    state.runtime_writer.apply_committed(changes).await?;
    // Best effort: failure leaves dirty/runtime_coherent fail-closed.
    let _ = invalidate_options(state).await;
    Ok(())
}

async fn record_option_update_audit(
    pool: &PgPool,
    identity: SystemConfigIdentity,
    headers: &HeaderMap,
    key: &str,
) {
    let username = sqlx::query_scalar::<_, String>("SELECT username FROM users WHERE id = $1")
        .bind(identity.user_id)
        .fetch_optional(pool)
        .await
        .ok()
        .flatten()
        .unwrap_or_default();
    let other = json!({
        "op": {"action": "option.update", "params": {"key": key}},
        "admin_info": {"admin_id": identity.user_id, "admin_username": &username, "admin_role": 100},
    });
    // Go treats management audit persistence as best effort.  The value is
    // intentionally never rendered into content, params, or diagnostics.
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) VALUES ($1, $2, 3, $3, $4, $5, $6)",
    )
    .bind(identity.user_id)
    .bind(chrono_seconds())
    .bind(format!("Updated system setting {key}"))
    .bind(username)
    .bind(client_ip(headers))
    .bind(other.to_string())
    .execute(pool)
    .await;
}

async fn write_options<'a, I>(pool: &PgPool, values: I) -> Result<(), ()>
where
    I: IntoIterator<Item = (&'a str, &'a str)>,
{
    let values = values.into_iter().collect::<Vec<_>>();
    let mut transaction = pool.begin().await.map_err(|_| ())?;
    for (key, value) in &values {
        sqlx::query("INSERT INTO options (key, value) VALUES ($1, $2) ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value")
            .bind(*key)
            .bind(*value)
            .execute(&mut *transaction)
            .await
            .map_err(|_| ())?;
        let persisted =
            sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key = $1")
                .bind(*key)
                .fetch_optional(&mut *transaction)
                .await
                .map_err(|_| ())?
                .flatten();
        if persisted.as_deref() != Some(*value) {
            return Err(());
        }
    }
    transaction.commit().await.map_err(|_| ())
}

#[derive(Deserialize)]
struct AffinityQuery {
    all: Option<String>,
    rule_name: Option<String>,
}

fn query_values(raw: Option<&str>) -> BTreeMap<String, String> {
    raw.unwrap_or_default()
        .split('&')
        .filter(|part| !part.is_empty())
        .filter_map(|part| {
            let (key, value) = part.split_once('=').unwrap_or((part, ""));
            Some((decode_query_component(key)?, decode_query_component(value)?))
        })
        .collect()
}

fn decode_query_component(value: &str) -> Option<String> {
    let bytes = value.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'+' => {
                decoded.push(b' ');
                index += 1;
            }
            b'%' if index + 2 < bytes.len() => {
                let high = hex_value(bytes[index + 1])?;
                let low = hex_value(bytes[index + 2])?;
                decoded.push((high << 4) | low);
                index += 3;
            }
            b'%' => return None,
            byte => {
                decoded.push(byte);
                index += 1;
            }
        }
    }
    String::from_utf8(decoded).ok()
}

fn hex_value(byte: u8) -> Option<u8> {
    match byte {
        b'0'..=b'9' => Some(byte - b'0'),
        b'a'..=b'f' => Some(byte - b'a' + 10),
        b'A'..=b'F' => Some(byte - b'A' + 10),
        _ => None,
    }
}

fn affinity_query(raw: Option<&str>) -> AffinityQuery {
    let mut values = query_values(raw);
    AffinityQuery {
        all: values.remove("all"),
        rule_name: values.remove("rule_name"),
    }
}
async fn affinity_stats(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
) -> Response {
    let Ok(mut c) = state.valkey.get_multiplexed_async_connection().await else {
        return legacy_error("获取缓存统计失败");
    };
    let keys = match affinity_keys(&mut c, &format!("{AFFINITY_CACHE_PREFIX}*")).await {
        Ok(keys) => keys,
        Err(_) => return legacy_error("获取缓存统计失败"),
    };
    ok(json!({"entries": keys.len()}))
}
async fn clear_affinity_cache(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    _headers: HeaderMap,
    query: RawQuery,
) -> Response {
    let query = affinity_query(query.0.as_deref());
    let all = query.all.as_deref() == Some("true");
    let rule = query.rule_name.unwrap_or_default().trim().to_owned();
    if !all && rule.is_empty() {
        return legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success":false,"message":"缺少参数：rule_name，或使用 all=true 清空全部"}),
        );
    }
    let Ok(mut c) = state.valkey.get_multiplexed_async_connection().await else {
        return legacy_error("清理缓存失败");
    };
    let pattern = if all {
        format!("{AFFINITY_CACHE_PREFIX}*")
    } else {
        format!("{AFFINITY_CACHE_PREFIX}{rule}:*")
    };
    let keys = match affinity_keys(&mut c, &pattern).await {
        Ok(keys) => keys,
        Err(_) => return legacy_error("清理缓存失败"),
    };
    let deleted = if keys.is_empty() {
        0
    } else {
        match c.del::<_, usize>(keys).await {
            Ok(deleted) => deleted,
            Err(_) => return legacy_error("清理缓存失败"),
        }
    };
    ok(json!({"deleted": deleted}))
}

async fn affinity_keys(
    connection: &mut redis::aio::MultiplexedConnection,
    pattern: &str,
) -> Result<Vec<String>, redis::RedisError> {
    let mut cursor = 0_u64;
    let mut keys = Vec::new();
    loop {
        let (next, batch): (u64, Vec<String>) = redis::cmd("SCAN")
            .arg(cursor)
            .arg("MATCH")
            .arg(pattern)
            .arg("COUNT")
            .arg(500)
            .query_async(connection)
            .await?;
        keys.extend(batch);
        if next == 0 {
            return Ok(keys);
        }
        cursor = next;
    }
}

#[derive(Deserialize)]
struct Compliance {
    confirmed: bool,
}
async fn confirm_payment_compliance(
    State(state): State<SystemConfigHttpState>,
    Extension(context): Extension<SystemConfigAuthContext>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    if context.credential != SystemConfigCredential::DashboardSession {
        return personal_access_token_forbidden(&headers);
    }
    let identity = context.identity;
    let input = match serde_json::from_slice::<Compliance>(&body) {
        Ok(input) => input,
        Err(_) => return legacy_error("参数错误"),
    };
    if !input.confirmed {
        return legacy_error("请确认合规声明");
    }
    let now = chrono_seconds();
    let confirmed_by = identity.user_id.to_string();
    let confirmed_ip = client_ip(&headers);
    let values = vec![
        (
            "payment_setting.compliance_confirmed".to_owned(),
            "true".to_owned(),
        ),
        (
            "payment_setting.compliance_terms_version".to_owned(),
            "v1".to_owned(),
        ),
        (
            "payment_setting.compliance_confirmed_at".to_owned(),
            now.to_string(),
        ),
        (
            "payment_setting.compliance_confirmed_by".to_owned(),
            confirmed_by,
        ),
        (
            "payment_setting.compliance_confirmed_ip".to_owned(),
            confirmed_ip,
        ),
    ];
    if persist_option_changes(&state, &values).await.is_err() {
        return legacy_error("保存设置失败");
    }
    record_option_update_audit(
        &state.pg,
        identity,
        &headers,
        "payment_setting.compliance_confirmed",
    )
    .await;
    ok(
        json!({"confirmed":true,"terms_version":"v1","confirmed_at":now,"confirmed_by":identity.user_id}),
    )
}

fn client_ip(headers: &HeaderMap) -> String {
    headers
        .get("x-forwarded-for")
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .or_else(|| {
            headers
                .get("x-real-ip")
                .and_then(|value| value.to_str().ok())
        })
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or_default()
        .to_owned()
}
fn chrono_seconds() -> i64 {
    std::time::SystemTime::now()
        .duration_since(std::time::UNIX_EPOCH)
        .map_or(0, |d| i64::try_from(d.as_secs()).unwrap_or(0))
}

async fn project_update(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
) -> Response {
    match state.project_update.latest_main_commit().await {
        Ok(release) => ok(release),
        Err(()) => legacy_error("Failed to check for updates"),
    }
}

fn normalize_currency_code(raw: &str) -> Option<String> {
    let code = raw.trim().to_ascii_uppercase();
    (code.len() == 3 && code.bytes().all(|byte| byte.is_ascii_uppercase())).then_some(code)
}

async fn exchange_rate(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    query: RawQuery,
) -> Response {
    let currency = query_values(query.0.as_deref())
        .remove("currency")
        .and_then(|value| normalize_currency_code(&value));
    let Some(currency) = currency else {
        return legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "message": "currency must be a three-letter ISO code"}),
        );
    };
    let (provider, rate) = if currency == "USD" {
        ("base", Ok(1.0))
    } else {
        (
            "pinned-providers",
            state
                .exchange_rate
                .settlement_units_per_usd(&currency)
                .await,
        )
    };
    match rate {
        Ok(rate) if rate.is_finite() && rate > 0.0 => legacy_json(
            StatusCode::OK,
            json!({
                "success": true,
                "message": "",
                "data": {
                    "base_currency": "USD",
                    "quote_currency": currency,
                    "rate": rate,
                    "fetched_at": chrono::Utc::now().to_rfc3339_opts(
                        chrono::SecondsFormat::Secs,
                        true,
                    ),
                    "provider": provider,
                },
            }),
        ),
        _ => legacy_json(
            StatusCode::BAD_GATEWAY,
            json!({"success": false, "message": "failed to fetch the latest USD exchange rate"}),
        ),
    }
}

async fn reset_model_ratio(
    State(state): State<SystemConfigHttpState>,
    Extension(context): Extension<SystemConfigAuthContext>,
    headers: HeaderMap,
) -> Response {
    let identity = context.identity;
    let changes = vec![("ModelRatio".to_owned(), "{}".to_owned())];
    if persist_option_changes(&state, &changes).await.is_err() {
        return legacy_error("保存设置失败");
    }
    record_option_update_audit(&state.pg, identity, &headers, "ModelRatio").await;
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"message":"重置模型倍率成功"}),
    )
}

#[derive(Default, Deserialize)]
struct PancakeCreds {
    merchant_id: Option<String>,
    private_key: Option<String>,
    return_url: Option<String>,
    store_id: Option<String>,
    product_id: Option<String>,
}

#[derive(Deserialize)]
struct SubscriptionProductRequest {
    name: Option<String>,
    amount: Option<String>,
    currency: Option<String>,
    duration_unit: Option<String>,
    duration_value: Option<i64>,
    product_type: Option<String>,
}

const PANCAKE_PRODUCT_TYPE_ONE_TIME: &str = "one_time";
const PANCAKE_PRODUCT_TYPE_SUBSCRIPTION: &str = "subscription";

fn pancake_plan_product_type(value: Option<&str>) -> Result<&'static str, ()> {
    match value
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase()
        .as_str()
    {
        "" | PANCAKE_PRODUCT_TYPE_SUBSCRIPTION => Ok(PANCAKE_PRODUCT_TYPE_SUBSCRIPTION),
        PANCAKE_PRODUCT_TYPE_ONE_TIME => Ok(PANCAKE_PRODUCT_TYPE_ONE_TIME),
        _ => Err(()),
    }
}

fn pancake_plan_product_options(catalog: &Value, store_id: &str) -> Vec<Value> {
    let Some(store) = catalog
        .get("stores")
        .and_then(Value::as_array)
        .and_then(|stores| {
            stores
                .iter()
                .find(|store| store.get("id").and_then(Value::as_str) == Some(store_id))
        })
    else {
        return Vec::new();
    };
    let mut products = Vec::new();
    for (key, product_type) in [
        ("onetimeProducts", PANCAKE_PRODUCT_TYPE_ONE_TIME),
        ("subscriptionProducts", PANCAKE_PRODUCT_TYPE_SUBSCRIPTION),
    ] {
        let snake_key = if key == "onetimeProducts" {
            "onetime_products"
        } else {
            "subscription_products"
        };
        for product in store
            .get(snake_key)
            .or_else(|| store.get(key))
            .and_then(Value::as_array)
            .into_iter()
            .flatten()
        {
            let mut product = product.clone();
            if let Some(object) = product.as_object_mut() {
                object.insert("product_type".to_owned(), json!(product_type));
                products.push(product);
            }
        }
    }
    products
}

fn pancake_billing_period(duration_unit: &str, duration_value: i64) -> Result<&'static str, ()> {
    match (
        duration_unit.trim().to_ascii_lowercase().as_str(),
        duration_value,
    ) {
        ("day", 7) => Ok("weekly"),
        ("month", 1) => Ok("monthly"),
        ("month", 3) => Ok("quarterly"),
        ("month", 12) | ("year", 1) => Ok("yearly"),
        _ => Err(()),
    }
}

fn subscription_price_in_usd(
    amount: &str,
    currency: &str,
    options: &BTreeMap<String, String>,
) -> Result<String, ()> {
    let amount = Decimal::from_str_exact(amount.trim()).map_err(|_| ())?;
    if amount <= Decimal::ZERO {
        return Err(());
    }
    let usd = match currency.trim().to_ascii_uppercase().as_str() {
        "USD" => amount,
        "CNY" => {
            let cny_per_usd = options
                .get("USDExchangeRate")
                .and_then(|value| Decimal::from_str_exact(value.trim()).ok())
                .filter(|value| *value > Decimal::ZERO)
                .ok_or(())?;
            amount.checked_div(cny_per_usd).ok_or(())?
        }
        _ => return Err(()),
    }
    .round_dp(2);
    if usd < Decimal::new(1, 2) {
        return Err(());
    }
    Ok(format!("{usd:.2}"))
}

async fn pancake_values(
    state: &SystemConfigHttpState,
    supplied: &PancakeCreds,
) -> Result<(String, String, BTreeMap<String, String>), Response> {
    let supplied_merchant_id = supplied
        .merchant_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty());
    let supplied_private_key = supplied
        .private_key
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty());
    if let (Some(merchant_id), Some(private_key)) = (supplied_merchant_id, supplied_private_key) {
        return Ok((
            merchant_id.to_owned(),
            private_key.to_owned(),
            BTreeMap::new(),
        ));
    }

    let opts = cached_options(state)
        .await
        .map_err(|_| legacy_error("读取配置失败"))?;
    let m = supplied_merchant_id
        .map(str::to_owned)
        .or_else(|| opts.get("WaffoPancakeMerchantID").cloned())
        .unwrap_or_default();
    let k = supplied_private_key
        .map(str::to_owned)
        .or_else(|| opts.get("WaffoPancakePrivateKey").cloned())
        .unwrap_or_default();
    if m.is_empty() || k.is_empty() {
        Err(legacy_error("Waffo Pancake 凭证未配置"))
    } else {
        Ok((m, k, opts))
    }
}
async fn pancake_catalog_post(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    body: Bytes,
) -> Response {
    let input = if body.iter().all(u8::is_ascii_whitespace) {
        PancakeCreds::default()
    } else {
        match serde_json::from_slice::<PancakeCreds>(&body) {
            Ok(input) => input,
            Err(_) => {
                return legacy_json(StatusCode::OK, json!({"message":"error","data":"参数错误"}));
            }
        }
    };
    let Ok((m, k, _)) = pancake_values(&state, &input).await else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 凭证未配置"}),
        );
    };
    match state.pancake.catalog(&m, &k).await {
        Ok(data) => legacy_json(StatusCode::OK, json!({"message":"success","data":data})),
        Err(_) => legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"拉取目录失败"}),
        ),
    }
}

#[derive(Default, Deserialize)]
struct OptionValuesRequest {
    values: BTreeMap<String, String>,
}

async fn validate_options(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    body: Bytes,
) -> Response {
    let values = match decode_option_values(&body) {
        Ok(values) => values,
        Err(response) => return response,
    };
    let options = match cached_options(&state).await {
        Ok(options) => options,
        Err(()) => return legacy_error("保存设置失败"),
    };
    let mut candidate_options = options.clone();
    candidate_options.extend(values.clone());
    for (key, value) in &values {
        if let Err(message) = validate_option_update(key, value, &candidate_options) {
            return legacy_json(
                StatusCode::OK,
                json!({"success": false, "message": message}),
            );
        }
        if let Err(message) =
            validate_assistant_model_update(&state, key, value, &candidate_options).await
        {
            return legacy_json(
                StatusCode::OK,
                json!({"success": false, "message": message}),
            );
        }
    }
    legacy_json(StatusCode::OK, json!({"success": true, "message": ""}))
}

async fn update_options_bulk(
    State(state): State<SystemConfigHttpState>,
    Extension(context): Extension<SystemConfigAuthContext>,
    body: Bytes,
) -> Response {
    let values = match decode_option_values(&body) {
        Ok(values) => values,
        Err(response) => return response,
    };
    let options = match cached_options(&state).await {
        Ok(options) => options,
        Err(()) => return legacy_error("保存设置失败"),
    };
    let mut candidate_options = options.clone();
    candidate_options.extend(values.clone());
    for (key, value) in &values {
        if let Err(message) = validate_option_update(key, value, &candidate_options) {
            return legacy_json(
                StatusCode::OK,
                json!({"success": false, "message": message}),
            );
        }
        if let Err(message) =
            validate_assistant_model_update(&state, key, value, &candidate_options).await
        {
            return legacy_json(
                StatusCode::OK,
                json!({"success": false, "message": message}),
            );
        }
    }
    let changes: Vec<(String, String)> = values.into_iter().collect();
    if persist_option_changes(&state, &changes).await.is_err() {
        return legacy_error("保存设置失败");
    }
    let mut keys: Vec<&str> = changes.iter().map(|(key, _)| key.as_str()).collect();
    keys.sort_unstable();
    let other = json!({"op": {"action": "option.bulk_update", "params": {"keys": keys}}});
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, other) VALUES ($1, $2, 3, $3, $4, $5)",
    )
    .bind(context.identity.user_id)
    .bind(chrono_seconds())
    .bind("Updated system settings in bulk")
    .bind("")
    .bind(other.to_string())
    .execute(&state.pg)
    .await;
    legacy_json(StatusCode::OK, json!({"success": true, "message": ""}))
}

fn decode_option_values(body: &Bytes) -> Result<BTreeMap<String, String>, Response> {
    if body.is_empty() {
        return Err(legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "message": "values must be a non-empty JSON object"}),
        ));
    }
    let request: OptionValuesRequest = serde_json::from_slice(body).map_err(|_| {
        legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "message": "values must be a non-empty JSON object"}),
        )
    })?;
    if request.values.is_empty() {
        return Err(legacy_json(
            StatusCode::BAD_REQUEST,
            json!({"success": false, "message": "values must be a non-empty JSON object"}),
        ));
    }
    if request.values.len() > 128 {
        return Err(legacy_json(
            StatusCode::PAYLOAD_TOO_LARGE,
            json!({"success": false, "message": "too many option values"}),
        ));
    }
    Ok(request.values)
}

async fn pancake_pair(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    body: Bytes,
) -> Response {
    let input = if body.iter().all(u8::is_ascii_whitespace) {
        PancakeCreds::default()
    } else {
        match serde_json::from_slice::<PancakeCreds>(&body) {
            Ok(input) => input,
            Err(_) => {
                return legacy_json(StatusCode::OK, json!({"message":"error","data":"参数错误"}));
            }
        }
    };
    let Ok((m, k, _)) = pancake_values(&state, &input).await else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 凭证未配置"}),
        );
    };
    match state
        .pancake
        .create_pair(&m, &k, input.return_url.as_deref().unwrap_or(""))
        .await
    {
        Ok(data) => legacy_json(StatusCode::OK, json!({"message":"success","data":data})),
        Err(data) => legacy_json(StatusCode::OK, json!({"message":"error","data":data})),
    }
}
async fn pancake_save(
    State(state): State<SystemConfigHttpState>,
    Extension(context): Extension<SystemConfigAuthContext>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    let identity = context.identity;
    let input = match serde_json::from_slice::<PancakeCreds>(&body) {
        Ok(input) => input,
        Err(_) => {
            return legacy_json(StatusCode::OK, json!({"message":"error","data":"参数错误"}));
        }
    };
    let merchant_id = input.merchant_id.unwrap_or_default().trim().to_owned();
    let private_key = input.private_key.unwrap_or_default().trim().to_owned();
    let return_url = input.return_url.unwrap_or_default().trim().to_owned();
    let store_id = input.store_id.unwrap_or_default().trim().to_owned();
    let product_id = input.product_id.unwrap_or_default().trim().to_owned();
    if merchant_id.is_empty() || store_id.is_empty() || product_id.is_empty() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"保存配置失败"}),
        );
    }
    let mut entries = vec![
        ("WaffoPancakeMerchantID".to_owned(), merchant_id),
        ("WaffoPancakeReturnURL".to_owned(), return_url),
        ("WaffoPancakeStoreID".to_owned(), store_id.clone()),
        ("WaffoPancakeProductID".to_owned(), product_id.clone()),
    ];
    // Blank secret is a deliberate "keep current" UI value. It must never
    // erase the persisted credential or be reflected in a response/log.
    if !private_key.is_empty() {
        entries.push(("WaffoPancakePrivateKey".to_owned(), private_key));
    }
    if persist_option_changes(&state, &entries).await.is_err() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"保存配置失败"}),
        );
    }
    record_option_update_audit(&state.pg, identity, &headers, "WaffoPancakeMerchantID").await;
    legacy_json(
        StatusCode::OK,
        json!({"message":"success","data":{"product_id": product_id, "store_id": store_id}}),
    )
}
async fn pancake_subscription_product(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
    body: Bytes,
) -> Response {
    let input = if body.iter().all(u8::is_ascii_whitespace) {
        SubscriptionProductRequest {
            name: None,
            amount: None,
            currency: None,
            duration_unit: None,
            duration_value: None,
            product_type: None,
        }
    } else {
        match serde_json::from_slice::<SubscriptionProductRequest>(&body) {
            Ok(input) => input,
            Err(_) => {
                return legacy_json(StatusCode::OK, json!({"message":"error","data":"参数错误"}));
            }
        }
    };
    let name = input.name.unwrap_or_default().trim().to_owned();
    if name.is_empty() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"套餐名称不能为空"}),
        );
    }
    let amount = input.amount.unwrap_or_default().trim().to_owned();
    if amount.is_empty() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"套餐价格不能为空"}),
        );
    }
    let currency = input
        .currency
        .unwrap_or_else(|| "CNY".to_owned())
        .trim()
        .to_ascii_uppercase();
    let Ok(product_type) = pancake_plan_product_type(input.product_type.as_deref()) else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake product type must be one_time or subscription"}),
        );
    };
    let duration_unit = input.duration_unit.unwrap_or_default();
    let duration_value = input.duration_value.unwrap_or_default();
    let billing_period = if product_type == PANCAKE_PRODUCT_TYPE_SUBSCRIPTION {
        let Ok(billing_period) = pancake_billing_period(&duration_unit, duration_value) else {
            return legacy_json(
                StatusCode::OK,
                json!({"message":"error","data":"Waffo Pancake recurring billing supports exactly 7 days, 1 month, 3 months, 12 months, or 1 year"}),
            );
        };
        Some(billing_period)
    } else {
        None
    };
    let creds = PancakeCreds {
        merchant_id: None,
        private_key: None,
        return_url: None,
        store_id: None,
        product_id: None,
    };
    let Ok((merchant_id, private_key, options)) = pancake_values(&state, &creds).await else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"}),
        );
    };
    let store_id = options
        .get("WaffoPancakeStoreID")
        .map(String::as_str)
        .unwrap_or_default()
        .trim();
    if store_id.is_empty() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"}),
        );
    }
    let Ok(settlement_amount) = subscription_price_in_usd(&amount, &currency, &options) else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"套餐结算金额或币种无效"}),
        );
    };
    let return_url = options
        .get("WaffoPancakeReturnURL")
        .map(String::as_str)
        .unwrap_or_default();
    let created = if let Some(billing_period) = billing_period {
        state
            .pancake
            .create_subscription_product(
                &merchant_id,
                &private_key,
                store_id,
                &name,
                &settlement_amount,
                billing_period,
                return_url,
            )
            .await
    } else {
        state
            .pancake
            .create_product(
                &merchant_id,
                &private_key,
                store_id,
                &name,
                &settlement_amount,
                return_url,
            )
            .await
    };
    match created {
        Ok(product) => {
            let product_id = product
                .get("id")
                .or_else(|| product.get("product_id"))
                .cloned()
                .unwrap_or(product);
            legacy_json(
                StatusCode::OK,
                json!({"message":"success","data":{"product_id":product_id,"product_name":name,"store_id":store_id,"settlement_currency":"USD","settlement_amount":settlement_amount,"product_type":product_type}}),
            )
        }
        Err(()) => legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"创建套餐产品失败"}),
        ),
    }
}
async fn pancake_subscription_product_options(
    State(state): State<SystemConfigHttpState>,
    Extension(_context): Extension<SystemConfigAuthContext>,
) -> Response {
    let creds = PancakeCreds {
        merchant_id: None,
        private_key: None,
        return_url: None,
        store_id: None,
        product_id: None,
    };
    let Ok((merchant_id, private_key, options)) = pancake_values(&state, &creds).await else {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"}),
        );
    };
    let store_id = options
        .get("WaffoPancakeStoreID")
        .map(String::as_str)
        .unwrap_or_default()
        .trim();
    if store_id.is_empty() {
        return legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"Waffo Pancake 未完成配置，请先在支付设置中完成网关绑定"}),
        );
    }
    match state.pancake.catalog(&merchant_id, &private_key).await {
        Ok(catalog) => {
            let products = pancake_plan_product_options(&catalog, store_id);
            legacy_json(
                StatusCode::OK,
                json!({"message":"success","data":{"store_id":store_id,"products":products}}),
            )
        }
        Err(()) => legacy_json(
            StatusCode::OK,
            json!({"message":"error","data":"拉取产品列表失败"}),
        ),
    }
}

async fn get_setup(State(state): State<SystemConfigHttpState>) -> Response {
    let initialized = match sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM setups)")
        .fetch_one(&state.pg)
        .await
    {
        Ok(value) => value,
        Err(_) => return legacy_error("获取初始化状态失败"),
    };
    if initialized {
        return legacy_json(
            StatusCode::OK,
            json!({"success":true,"data":{"status":true,"root_init":false,"database_type":""}}),
        );
    }
    let root_init =
        match sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM users WHERE role = 100)")
            .fetch_one(&state.pg)
            .await
        {
            Ok(value) => value,
            Err(_) => return legacy_error("获取初始化状态失败"),
        };
    // The frozen Go listener runs CheckSetup during startup.  When a root
    // already exists but the setup marker is absent, that startup check
    // creates the marker and thereafter GET /api/setup reports an initialized
    // installation.  Mirror the durable marker here so a PostgreSQL-mounted
    // Rust listener does not expose the transient pre-CheckSetup state.
    if root_init
        && sqlx::query("INSERT INTO setups (version, initialized_at) VALUES ('rust-migration', $1)")
            .bind(chrono_seconds())
            .execute(&state.pg)
            .await
            .is_ok()
    {
        return legacy_json(
            StatusCode::OK,
            json!({"success":true,"data":{"status":true,"root_init":false,"database_type":""}}),
        );
    }
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"data":{"status":false,"root_init":root_init,"database_type":"PostgreSQL"}}),
    )
}
#[derive(Deserialize)]
struct SetupRequest {
    username: String,
    password: String,
    #[serde(rename = "confirmPassword")]
    confirm_password: String,
    #[serde(rename = "SelfUseModeEnabled")]
    self_use: bool,
    #[serde(rename = "DemoSiteEnabled")]
    demo: bool,
}
async fn post_setup(State(state): State<SystemConfigHttpState>, body: Bytes) -> Response {
    let input = match serde_json::from_slice::<SetupRequest>(&body) {
        Ok(input) => input,
        Err(_) => return legacy_error("请求参数有误"),
    };
    let changes = vec![
        ("SelfUseModeEnabled".to_owned(), input.self_use.to_string()),
        ("DemoSiteEnabled".to_owned(), input.demo.to_string()),
    ];
    // Keep setup's independent transaction and runtime application in the
    // same option-write order as /api/option/. No option value is logged
    // while this guard is held.
    let _option_write_guard = state.option_write_lock.lock().await;
    let mut tx = match state.pg.begin().await {
        Ok(v) => v,
        Err(_) => return legacy_error("系统初始化失败"),
    };
    // `setups` has no singleton constraint in the frozen schema.  The
    // transaction-scoped advisory lock makes the check/create transition
    // linearizable without adding a migration-only schema constraint.
    if sqlx::query("SELECT pg_advisory_xact_lock(912_004_611)")
        .execute(&mut *tx)
        .await
        .is_err()
    {
        return legacy_error("系统初始化失败");
    }
    let initialized = match sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM setups)")
        .fetch_one(&mut *tx)
        .await
    {
        Ok(value) => value,
        Err(_) => return legacy_error("系统初始化失败"),
    };
    if initialized {
        return legacy_error("系统已经初始化完成");
    }
    let root_exists =
        match sqlx::query_scalar::<_, bool>("SELECT EXISTS(SELECT 1 FROM users WHERE role = 100)")
            .fetch_one(&mut *tx)
            .await
        {
            Ok(value) => value,
            Err(_) => return legacy_error("系统初始化失败"),
        };
    // No private process-local configuration is allowed on this route.  A
    // missing shared writer rejects setup before it can create a durable but
    // inactive installation, while an already-initialized result preserves
    // the frozen Go response above.
    if state.runtime_writer.preflight(&changes).await.is_err() {
        return legacy_error("系统初始化失败");
    }
    if !root_exists {
        if input.username.len() > 12 {
            return legacy_error("用户名长度不能超过12个字符");
        }
        if input.password != input.confirm_password {
            return legacy_error("两次输入的密码不一致");
        }
        if input.password.len() < 8 {
            return legacy_error("密码长度至少为8个字符");
        }
        let hashed = match hash(input.password, DEFAULT_COST) {
            Ok(value) => value,
            Err(_) => return legacy_error("系统错误"),
        };
        if sqlx::query("INSERT INTO users (username,password,display_name,role,status,quota) VALUES ($1,$2,'Root User',100,1,100000000)")
            .bind(input.username)
            .bind(hashed)
            .execute(&mut *tx)
            .await
            .is_err()
        {
            return legacy_error("创建管理员账号失败");
        }
    } else {
        let root = sqlx::query("SELECT username, password FROM users WHERE role = 100 LIMIT 1")
            .fetch_optional(&mut *tx)
            .await;
        match root {
            Ok(Some(row)) => {
                let username: String = row.get("username");
                let password_hash: String = row.get("password");
                let username_ok = username == input.username;
                let password_ok = verify(&input.password, &password_hash).unwrap_or(false);
                if input.username.trim().is_empty()
                    || input.password.is_empty()
                    || !username_ok
                    || !password_ok
                {
                    return legacy_error("管理员账号验证失败");
                }
            }
            _ => return legacy_error("系统初始化失败"),
        }
    }
    for (key, value) in &changes {
        if sqlx::query("INSERT INTO options(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
            .bind(key)
            .bind(value)
            .execute(&mut *tx)
            .await
            .is_err()
        {
            return legacy_error("保存设置失败");
        }
    }
    if sqlx::query("INSERT INTO setups (version, initialized_at) VALUES ('rust-migration', $1)")
        .bind(chrono_seconds())
        .execute(&mut *tx)
        .await
        .is_err()
        || tx.commit().await.is_err()
    {
        return legacy_error("系统初始化失败");
    }
    mark_runtime_dirty(&state);
    // Preserve Go's durable-store-first lifecycle.  If the shared runtime
    // fails after commit the caller sees a failure instead of an invented
    // success, and the process can recover from the authoritative store.
    if state
        .runtime_writer
        .apply_committed(&changes)
        .await
        .is_err()
    {
        return legacy_error("系统初始化失败");
    }
    // Best effort: failure leaves dirty/runtime_coherent fail-closed.
    let _ = invalidate_options(&state).await;
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"message":"系统初始化成功"}),
    )
}
