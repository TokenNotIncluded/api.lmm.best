use lmm_api_rs::{
    protocol_rollout::{ProtocolRolloutConfig, RolloutConfigError},
    status::TurnstilePublicConfig,
};
use lmm_application::ValkeyReadinessPolicy;
use secrecy::{ExposeSecret, SecretString};
use std::{
    collections::BTreeMap,
    env,
    net::{IpAddr, SocketAddr},
    time::Duration,
};
use thiserror::Error;

const DEFAULT_TRUSTED_PROXY_CIDRS: &[&str] = &[
    "127.0.0.0/8",
    "::1/128",
    "10.0.0.0/8",
    "172.16.0.0/12",
    "192.168.0.0/16",
    "fc00::/7",
];
const PUBLIC_CONTENT_CACHE_TTL_NAME: &str = "LMM_PUBLIC_CONTENT_CACHE_TTL_SECONDS";
const PUBLIC_CONTENT_CACHE_TTL_MAX_SECONDS: u64 = 5;

#[derive(Clone, Debug)]
pub enum TrustedProxyPolicy {
    Default(Vec<ipnet::IpNet>),
    Disabled,
    Explicit(Vec<ipnet::IpNet>),
}

impl TrustedProxyPolicy {
    pub fn trusts(&self, address: IpAddr) -> bool {
        match self {
            Self::Disabled => false,
            Self::Default(networks) | Self::Explicit(networks) => {
                networks.iter().any(|network| network.contains(&address))
            }
        }
    }

    pub const fn uses_compatibility_defaults(&self) -> bool {
        matches!(self, Self::Default(_))
    }
}
#[derive(Clone)]
pub struct Config {
    pub listen_addr: SocketAddr,
    pub slot: String,
    pub database_url: String,
    pub valkey_url: String,
    pub schema_contract: i64,
    pub dependency_timeout: Duration,
    pub drain_timeout: Duration,
    pub public_content_cache_ttl: Duration,
    pub valkey_readiness_policy: ValkeyReadinessPolicy,
    pub global_api_rate_limit: u64,
    pub global_api_rate_limit_window: Duration,
    /// Secret for deterministic, deployment-wide model cache key derivation.
    pub crypto_secret: SecretString,
    /// Legacy-compatible model-cache lifetime shared by blue and green slots.
    pub models_cache_ttl: Duration,
    pub auth_session_secret: SecretString,
    pub auth_password_login_enabled: bool,
    pub auth_turnstile: TurnstileConfig,
    pub auth_cookie_secure: bool,
    pub auth_trusted_origins: Vec<String>,
    pub auth_anonymous_body_limit_bytes: usize,
    pub auth_active_session_limit: i64,
    pub auth_issuance_limit: i64,
    pub auth_issuance_window: Duration,
    pub auth_session_cache_ttl: Duration,
    pub auth_critical_rate_limit_enabled: bool,
    pub auth_critical_rate_limit: u64,
    pub auth_critical_rate_limit_window: Duration,
    pub api_token_search_rate_limit_enabled: bool,
    pub api_token_search_rate_limit: u64,
    pub api_token_search_rate_limit_window: Duration,
    pub trusted_proxies: TrustedProxyPolicy,
    /// Explicit opt-in for the isolated candidate surface on the test host.
    /// Any value other than exactly `1` is rejected before the listener binds.
    pub test_instance: bool,
    /// When `true`, developer access is granted to all ordinary users without
    /// paid activation.  Requires the listen address to be an exact IPv4 or
    /// IPv6 loopback address (127.0.0.1 or ::1).  Mirrors Go's
    /// `LMM_LOCAL_ACCEPTANCE` startup policy.
    pub local_acceptance: bool,
    /// Typed protocol conversion rollout controls, parsed before the listener binds.
    pub protocol_rollout: ProtocolRolloutConfig,
}

/// The startup-owned Turnstile policy. The verification secret is never part
/// of the public status representation.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TurnstileConfig {
    enabled: bool,
    secret_key: Option<String>,
}

impl TurnstileConfig {
    fn from_env() -> Result<Self, ConfigError> {
        let enabled = boolean("TURNSTILE_CHECK_ENABLED", false)?;
        turnstile_from_values(enabled, env::var("TURNSTILE_SECRET_KEY").ok().as_deref())
    }

    pub fn secret_key(&self) -> Option<String> {
        self.secret_key.clone()
    }

    pub fn resolve_public(
        &self,
        options: &BTreeMap<String, String>,
    ) -> Result<TurnstilePublicConfig, ConfigError> {
        let database_enabled = match options
            .get("TurnstileCheckEnabled")
            .map(|value: &String| value.trim())
        {
            None | Some("") | Some("false" | "0") => false,
            Some("true" | "1") => true,
            Some(_) => return Err(ConfigError::Invalid("TurnstileCheckEnabled")),
        };
        if self.enabled != database_enabled {
            return Err(ConfigError::Invalid("TurnstileCheckEnabled"));
        }
        let site_key = if self.enabled {
            options
                .get("TurnstileSiteKey")
                .map(|value: &String| value.trim())
                .filter(|site_key| !site_key.is_empty())
                .map(str::to_owned)
                .ok_or(ConfigError::Invalid("TurnstileSiteKey"))?
        } else {
            String::new()
        };
        Ok(TurnstilePublicConfig {
            enabled: self.enabled,
            site_key,
        })
    }
}

fn turnstile_from_values(
    enabled: bool,
    secret_key: Option<&str>,
) -> Result<TurnstileConfig, ConfigError> {
    let secret_key = match (enabled, secret_key) {
        (true, None) => return Err(ConfigError::Missing("TURNSTILE_SECRET_KEY")),
        (true, Some(secret)) if secret.trim().is_empty() => {
            return Err(ConfigError::Invalid("TURNSTILE_SECRET_KEY"));
        }
        (_, Some(secret)) if !secret.trim().is_empty() => Some(secret.to_owned()),
        _ => None,
    };
    Ok(TurnstileConfig {
        enabled,
        secret_key,
    })
}

impl std::fmt::Debug for Config {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        formatter
            .debug_struct("Config")
            .field("listen_addr", &self.listen_addr)
            .field("slot", &self.slot)
            .field("database_url", &"[REDACTED]")
            .field("valkey_url", &"[REDACTED]")
            .field("schema_contract", &self.schema_contract)
            .field("dependency_timeout", &self.dependency_timeout)
            .field("drain_timeout", &self.drain_timeout)
            .field("public_content_cache_ttl", &self.public_content_cache_ttl)
            .field("valkey_readiness_policy", &self.valkey_readiness_policy)
            .field("global_api_rate_limit", &self.global_api_rate_limit)
            .field(
                "global_api_rate_limit_window",
                &self.global_api_rate_limit_window,
            )
            .field("crypto_secret", &"[REDACTED]")
            .field("models_cache_ttl", &self.models_cache_ttl)
            .field("auth_session_secret", &"[REDACTED]")
            .field(
                "auth_password_login_enabled",
                &self.auth_password_login_enabled,
            )
            .field("auth_turnstile_enabled", &self.auth_turnstile.enabled)
            .field(
                "auth_turnstile_secret_key",
                &self
                    .auth_turnstile
                    .secret_key
                    .as_ref()
                    .map(|_| "[REDACTED]"),
            )
            .field("auth_cookie_secure", &self.auth_cookie_secure)
            .field("auth_trusted_origins", &self.auth_trusted_origins)
            .field(
                "auth_anonymous_body_limit_bytes",
                &self.auth_anonymous_body_limit_bytes,
            )
            .field("auth_active_session_limit", &self.auth_active_session_limit)
            .field("auth_issuance_limit", &self.auth_issuance_limit)
            .field("auth_issuance_window", &self.auth_issuance_window)
            .field("auth_session_cache_ttl", &self.auth_session_cache_ttl)
            .field(
                "auth_critical_rate_limit_enabled",
                &self.auth_critical_rate_limit_enabled,
            )
            .field("auth_critical_rate_limit", &self.auth_critical_rate_limit)
            .field(
                "auth_critical_rate_limit_window",
                &self.auth_critical_rate_limit_window,
            )
            .field(
                "api_token_search_rate_limit_enabled",
                &self.api_token_search_rate_limit_enabled,
            )
            .field(
                "api_token_search_rate_limit",
                &self.api_token_search_rate_limit,
            )
            .field(
                "api_token_search_rate_limit_window",
                &self.api_token_search_rate_limit_window,
            )
            .field("trusted_proxies", &self.trusted_proxies)
            .field("test_instance", &self.test_instance)
            .field("local_acceptance", &self.local_acceptance)
            .field("protocol_rollout", &self.protocol_rollout)
            .finish()
    }
}
#[derive(Debug, Error)]
pub enum ConfigError {
    #[error("required environment variable {0} is missing")]
    Missing(&'static str),
    #[error("environment variable {0} is invalid")]
    Invalid(&'static str),
    #[error("protocol rollout configuration is invalid: {0}")]
    ProtocolRollout(#[from] RolloutConfigError),
}
impl Config {
    pub fn from_env() -> Result<Self, ConfigError> {
        let test_instance = test_instance_enabled()?;
        // This override belongs exclusively to the isolated test listener.
        // Production deliberately never reads it, so a stray test variable
        // cannot alter the deployed Valkey contract.
        let test_valkey_port_override = if test_instance {
            Some(test_valkey_port()?)
        } else {
            None
        };
        let auth_cookie_secure =
            boolean_with_legacy("AUTH_COOKIE_SECURE", "SESSION_COOKIE_SECURE", false)?;
        let auth_trusted_origins = auth_trusted_origins(auth_cookie_secure)?;
        let listen_addr: SocketAddr = read("LMM_RS_LISTEN_ADDR")?
            .parse()
            .map_err(|_| ConfigError::Invalid("LMM_RS_LISTEN_ADDR"))?;
        let local_acceptance = local_acceptance_policy(listen_addr)?;
        let config = Self {
            slot: validated_slot(read("LMM_RS_SLOT")?, test_instance)?,
            listen_addr,
            database_url: read("DATABASE_URL")?,
            valkey_url: read("VALKEY_URL")?,
            schema_contract: read("LMM_SCHEMA_CONTRACT")?
                .parse()
                .map_err(|_| ConfigError::Invalid("LMM_SCHEMA_CONTRACT"))?,
            dependency_timeout: positive_seconds("LMM_DEPENDENCY_TIMEOUT_SECONDS", 2)?,
            // `lmm-api-rs@.service` leaves a five-second supervisor margin
            // beyond this bound.  Reject longer values rather than letting
            // systemd cut a drain short and disconnect an in-flight request.
            drain_timeout: bounded_seconds("LMM_DRAIN_TIMEOUT_SECONDS", 30, 40)?,
            public_content_cache_ttl: public_content_cache_ttl()?,
            valkey_readiness_policy: ValkeyReadinessPolicy::from_global_api_rate_limit_enabled(
                boolean("GLOBAL_API_RATE_LIMIT_ENABLE", true)?,
            ),
            global_api_rate_limit: positive_integer("GLOBAL_API_RATE_LIMIT", 360)?,
            global_api_rate_limit_window: positive_seconds("GLOBAL_API_RATE_LIMIT_DURATION", 180)?,
            crypto_secret: crypto_secret()?,
            models_cache_ttl: models_cache_ttl()?,
            auth_session_secret: validated_secret(read("SESSION_SECRET")?, "SESSION_SECRET")?,
            auth_password_login_enabled: boolean("PASSWORD_LOGIN_ENABLED", false)?,
            auth_turnstile: TurnstileConfig::from_env()?,
            auth_cookie_secure,
            auth_trusted_origins,
            auth_anonymous_body_limit_bytes: usize::try_from(positive_integer(
                "AUTH_ANONYMOUS_BODY_LIMIT_BYTES",
                512 * 1024,
            )?)
            .map_err(|_| ConfigError::Invalid("AUTH_ANONYMOUS_BODY_LIMIT_BYTES"))?,
            auth_active_session_limit: positive_i64("AUTH_ACTIVE_SESSION_LIMIT", 50)?,
            auth_issuance_limit: positive_i64("AUTH_SESSION_ISSUANCE_LIMIT", 100)?,
            auth_issuance_window: positive_seconds(
                "AUTH_SESSION_ISSUANCE_WINDOW_SECONDS",
                24 * 60 * 60,
            )?,
            auth_session_cache_ttl: positive_seconds("AUTH_SESSION_CACHE_TTL_SECONDS", 600)?,
            auth_critical_rate_limit_enabled: boolean("CRITICAL_RATE_LIMIT_ENABLE", true)?,
            auth_critical_rate_limit: positive_integer("CRITICAL_RATE_LIMIT", 20)?,
            auth_critical_rate_limit_window: positive_seconds(
                "CRITICAL_RATE_LIMIT_DURATION",
                20 * 60,
            )?,
            api_token_search_rate_limit_enabled: boolean("SEARCH_RATE_LIMIT_ENABLE", true)?,
            api_token_search_rate_limit: positive_integer("SEARCH_RATE_LIMIT", 10)?,
            api_token_search_rate_limit_window: positive_seconds("SEARCH_RATE_LIMIT_DURATION", 60)?,
            trusted_proxies: trusted_proxies()?,
            test_instance,
            local_acceptance,
            protocol_rollout: ProtocolRolloutConfig::from_env()?,
        };
        if let Some(test_valkey_port) = test_valkey_port_override {
            validate_test_instance_isolation(&config, test_valkey_port)?;
        }
        Ok(config)
    }
}

/// The candidate listener is deliberately a separate deployment from production.
/// Do not permit a copied environment file to bridge that boundary: its database,
/// database role, schema, and Valkey endpoint are all independently checked before
/// a listener can bind.
fn validate_test_instance_isolation(
    config: &Config,
    test_valkey_port: u16,
) -> Result<(), ConfigError> {
    validate_test_listener(config.listen_addr)?;
    validate_test_slot(&config.slot)?;
    validate_test_database_url(&config.database_url)?;
    validate_test_valkey_url_with_port(&config.valkey_url, test_valkey_port)?;
    validate_test_secret(config.auth_session_secret.expose_secret(), "SESSION_SECRET")?;
    validate_test_secret(config.crypto_secret.expose_secret(), "CRYPTO_SECRET")?;
    Ok(())
}

fn validate_test_listener(listen_addr: SocketAddr) -> Result<(), ConfigError> {
    if listen_addr.ip().is_loopback() {
        Ok(())
    } else {
        Err(ConfigError::Invalid("LMM_RS_LISTEN_ADDR"))
    }
}

fn validate_test_slot(slot: &str) -> Result<(), ConfigError> {
    if slot == "single" {
        Ok(())
    } else {
        Err(ConfigError::Invalid("LMM_RS_SLOT"))
    }
}

fn validate_test_database_url(raw: &str) -> Result<(), ConfigError> {
    let url = reqwest::Url::parse(raw).map_err(|_| ConfigError::Invalid("DATABASE_URL"))?;
    if !matches!(url.scheme(), "postgres" | "postgresql") {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }
    if raw.to_ascii_lowercase().contains("replace_me") {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }
    let Some(host) = url.host_str() else {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    };
    if !host
        .parse::<IpAddr>()
        .is_ok_and(|address| address.is_loopback())
    {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }
    let user = url.username();
    let database = url.path().trim_start_matches('/');
    if !test_namespace(user) || !test_namespace(database) {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }

    if url.query_pairs().any(|(key, _)| key != "options") {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }
    let search_path = url
        .query_pairs()
        .filter(|(key, _)| key == "options")
        .filter(|(_, value)| value.split_whitespace().count() == 1)
        .flat_map(|(_, value)| search_path_values(&value))
        .collect::<Vec<_>>();
    if search_path.len() != 1 || !test_namespace(&search_path[0]) {
        return Err(ConfigError::Invalid("DATABASE_URL"));
    }
    Ok(())
}

fn search_path_values(options: &str) -> Vec<String> {
    options
        .split_whitespace()
        .filter_map(|option| {
            option
                .strip_prefix("-csearch_path=")
                .or_else(|| option.strip_prefix("--search_path="))
        })
        .flat_map(|value| value.split(','))
        .map(str::to_owned)
        .collect()
}

fn test_namespace(value: &str) -> bool {
    value.len() > "lmm_test_".len()
        && value.starts_with("lmm_test_")
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}

#[cfg(test)]
fn validate_test_valkey_url(raw: &str) -> Result<(), ConfigError> {
    validate_test_valkey_url_with_port(raw, 6380)
}

fn validate_test_valkey_url_with_port(raw: &str, expected_port: u16) -> Result<(), ConfigError> {
    let url = reqwest::Url::parse(raw).map_err(|_| ConfigError::Invalid("VALKEY_URL"))?;
    let Some(host) = url.host_str() else {
        return Err(ConfigError::Invalid("VALKEY_URL"));
    };
    if !matches!(url.scheme(), "redis" | "rediss")
        || !host
            .parse::<IpAddr>()
            .is_ok_and(|address| address.is_loopback())
        || url.port_or_known_default() != Some(expected_port)
    {
        return Err(ConfigError::Invalid("VALKEY_URL"));
    }
    Ok(())
}

fn test_valkey_port() -> Result<u16, ConfigError> {
    match env::var("LMM_RS_TEST_VALKEY_PORT") {
        Ok(raw) => parse_test_valkey_port(Some(&raw)),
        Err(env::VarError::NotPresent) => parse_test_valkey_port(None),
        Err(env::VarError::NotUnicode(_)) => Err(ConfigError::Invalid("LMM_RS_TEST_VALKEY_PORT")),
    }
}

fn parse_test_valkey_port(raw: Option<&str>) -> Result<u16, ConfigError> {
    let Some(raw) = raw else {
        return Ok(6380);
    };
    let port = raw
        .parse::<u16>()
        .map_err(|_| ConfigError::Invalid("LMM_RS_TEST_VALKEY_PORT"))?;
    (port != 0)
        .then_some(port)
        .ok_or(ConfigError::Invalid("LMM_RS_TEST_VALKEY_PORT"))
}

fn validate_test_secret(secret: &str, name: &'static str) -> Result<(), ConfigError> {
    if is_test_placeholder_secret(secret) {
        return Err(ConfigError::Invalid(name));
    }
    validated_secret(secret.to_owned(), name).map(|_| ())
}

fn is_test_placeholder_secret(secret: &str) -> bool {
    let normalized = secret.trim().to_ascii_lowercase();
    is_example_secret(secret)
        || normalized.is_empty()
        || [
            "replace",
            "placeholder",
            "change-me",
            "change_me",
            "changeme",
            "example",
            "your-secret",
            "your_secret",
        ]
        .iter()
        .any(|marker| normalized.contains(marker))
}

fn test_instance_enabled() -> Result<bool, ConfigError> {
    let value = env::var_os("LMM_RS_TEST_INSTANCE")
        .map(|value| {
            value
                .into_string()
                .map_err(|_| ConfigError::Invalid("LMM_RS_TEST_INSTANCE"))
        })
        .transpose()?;
    parse_test_instance_value(value.as_deref())
}

fn parse_test_instance_value(value: Option<&str>) -> Result<bool, ConfigError> {
    match value {
        None => Ok(false),
        Some("1") => Ok(true),
        Some(_) => Err(ConfigError::Invalid("LMM_RS_TEST_INSTANCE")),
    }
}

/// Mirrors Go's `localAcceptancePolicy`.  When `LMM_LOCAL_ACCEPTANCE=true`,
/// developer access is granted without paid activation.  The listen address
/// must be an exact IPv4 or IPv6 loopback (127.0.0.1 or ::1); mapped IPv6
/// (::ffff:127.0.0.1) and scoped addresses are rejected, matching Go's
/// family-aware `netip.Addr` equality.
fn local_acceptance_policy(listen_addr: SocketAddr) -> Result<bool, ConfigError> {
    let flag = env::var("LMM_LOCAL_ACCEPTANCE").unwrap_or_default();
    parse_local_acceptance_policy(&flag, listen_addr)
}

fn parse_local_acceptance_policy(flag: &str, listen_addr: SocketAddr) -> Result<bool, ConfigError> {
    if flag != "true" {
        return Ok(false);
    }
    if !is_exact_loopback(listen_addr.ip()) {
        return Err(ConfigError::Invalid("LMM_LOCAL_ACCEPTANCE"));
    }
    Ok(true)
}

/// Returns `true` only for exactly `127.0.0.1` (IPv4) or `::1` (IPv6).
/// IPv4-mapped IPv6 (`::ffff:127.0.0.1`) and any other loopback-range
/// address (e.g. `127.0.0.2`) are rejected, matching Go's
/// `isExactLoopbackHost` which uses `netip.Addr` equality.
fn is_exact_loopback(ip: IpAddr) -> bool {
    matches!(
        ip,
        IpAddr::V4(v4) if v4 == std::net::Ipv4Addr::LOCALHOST
    ) || matches!(
        ip,
        IpAddr::V6(v6) if v6 == std::net::Ipv6Addr::LOCALHOST
    )
}

fn validated_slot(value: String, test_instance: bool) -> Result<String, ConfigError> {
    match (test_instance, value.as_str()) {
        (true, "single") | (false, "blue" | "green") => Ok(value),
        _ => Err(ConfigError::Invalid("LMM_RS_SLOT")),
    }
}
fn read(name: &'static str) -> Result<String, ConfigError> {
    env::var(name).map_err(|_| ConfigError::Missing(name))
}

/// Accepts the explicit secret first, while preserving the deployed legacy
/// `SESSION_SECRET` contract until all blue/green slots have been migrated.
fn crypto_secret() -> Result<SecretString, ConfigError> {
    let secret = env::var("CRYPTO_SECRET").or_else(|_| read("SESSION_SECRET"))?;
    validated_secret(secret, "CRYPTO_SECRET")
}

fn validated_secret(secret: String, name: &'static str) -> Result<SecretString, ConfigError> {
    if secret.trim().len() < 32 || is_example_secret(&secret) {
        return Err(ConfigError::Invalid(name));
    }
    Ok(SecretString::from(secret))
}

fn is_example_secret(secret: &str) -> bool {
    matches!(
        secret,
        "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES" | "REPLACE_ME"
    )
}

/// `SYNC_FREQUENCY` is the Go-compatible source. The namespaced setting is a
/// deliberate per-Rust override for staged migration rehearsals.
fn models_cache_ttl() -> Result<Duration, ConfigError> {
    match env::var("LMM_MODELS_CACHE_TTL_SECONDS") {
        Ok(raw) => positive_seconds_value(&raw, "LMM_MODELS_CACHE_TTL_SECONDS"),
        Err(_) => positive_seconds("SYNC_FREQUENCY", 60),
    }
}

fn positive_seconds_value(raw: &str, name: &'static str) -> Result<Duration, ConfigError> {
    let value = raw.parse().map_err(|_| ConfigError::Invalid(name))?;
    if value == 0 {
        return Err(ConfigError::Invalid(name));
    }
    Ok(Duration::from_secs(value))
}

fn seconds(name: &'static str, default: u64) -> Result<Duration, ConfigError> {
    let value = env::var(name).map_or_else(
        |_| Ok(default),
        |raw| raw.parse().map_err(|_| ConfigError::Invalid(name)),
    )?;
    Ok(Duration::from_secs(value))
}
fn positive_seconds(name: &'static str, default: u64) -> Result<Duration, ConfigError> {
    let value = seconds(name, default)?;
    if value.is_zero() {
        Err(ConfigError::Invalid(name))
    } else {
        Ok(value)
    }
}
fn public_content_cache_ttl() -> Result<Duration, ConfigError> {
    public_content_cache_ttl_value(
        seconds(
            PUBLIC_CONTENT_CACHE_TTL_NAME,
            PUBLIC_CONTENT_CACHE_TTL_MAX_SECONDS,
        )?
        .as_secs(),
    )
}
fn public_content_cache_ttl_value(value: u64) -> Result<Duration, ConfigError> {
    bounded_seconds_value(
        value,
        PUBLIC_CONTENT_CACHE_TTL_NAME,
        PUBLIC_CONTENT_CACHE_TTL_MAX_SECONDS,
    )
}
fn bounded_seconds(
    name: &'static str,
    default: u64,
    maximum: u64,
) -> Result<Duration, ConfigError> {
    bounded_seconds_value(seconds(name, default)?.as_secs(), name, maximum)
}
fn bounded_seconds_value(
    value: u64,
    name: &'static str,
    maximum: u64,
) -> Result<Duration, ConfigError> {
    if value == 0 || value > maximum {
        Err(ConfigError::Invalid(name))
    } else {
        Ok(Duration::from_secs(value))
    }
}
fn positive_integer(name: &'static str, default: u64) -> Result<u64, ConfigError> {
    let value = env::var(name).map_or_else(
        |_| Ok(default),
        |raw| raw.parse().map_err(|_| ConfigError::Invalid(name)),
    )?;
    if value == 0 {
        Err(ConfigError::Invalid(name))
    } else {
        Ok(value)
    }
}
fn positive_i64(name: &'static str, default: i64) -> Result<i64, ConfigError> {
    let value = env::var(name).map_or_else(
        |_| Ok(default),
        |raw| raw.parse().map_err(|_| ConfigError::Invalid(name)),
    )?;
    if value <= 0 {
        Err(ConfigError::Invalid(name))
    } else {
        Ok(value)
    }
}
fn auth_trusted_origins(cookie_secure: bool) -> Result<Vec<String>, ConfigError> {
    let (name, raw) = if env::var_os("AUTH_TRUSTED_ORIGINS").is_some() {
        (
            "AUTH_TRUSTED_ORIGINS",
            env::var("AUTH_TRUSTED_ORIGINS").unwrap_or_default(),
        )
    } else {
        (
            "SESSION_COOKIE_TRUSTED_URL",
            env::var("SESSION_COOKIE_TRUSTED_URL").unwrap_or_default(),
        )
    };
    let origins = if raw.trim().is_empty() {
        Vec::new()
    } else {
        raw.split(',')
            .map(str::trim)
            .map(|value| trusted_https_origin(value, name))
            .collect::<Result<Vec<_>, _>>()?
    };
    match (cookie_secure, origins.is_empty()) {
        (true, true) | (false, false) => Err(ConfigError::Invalid(name)),
        _ => Ok(origins),
    }
}
fn trusted_https_origin(value: &str, name: &'static str) -> Result<String, ConfigError> {
    if value.is_empty() {
        return Err(ConfigError::Invalid(name));
    }
    let url = reqwest::Url::parse(value).map_err(|_| ConfigError::Invalid(name))?;
    let Some(host) = url.host_str() else {
        return Err(ConfigError::Invalid(name));
    };
    if url.scheme() != "https"
        || !valid_origin_host(host)
        || !url.username().is_empty()
        || url.password().is_some()
        || url.path() != "/"
        || url.query().is_some()
        || url.fragment().is_some()
    {
        return Err(ConfigError::Invalid(name));
    }
    Ok(url.origin().ascii_serialization())
}
fn valid_origin_host(host: &str) -> bool {
    host.parse::<std::net::IpAddr>().is_ok()
        || host.split('.').all(|label| {
            !label.is_empty()
                && label.len() <= 63
                && !label.starts_with('-')
                && !label.ends_with('-')
                && label
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || byte == b'-')
        })
}

fn trusted_proxies() -> Result<TrustedProxyPolicy, ConfigError> {
    let raw = env::var("TRUSTED_PROXIES").unwrap_or_default();
    parse_trusted_proxies(&raw)
}

fn parse_trusted_proxies(raw: &str) -> Result<TrustedProxyPolicy, ConfigError> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Ok(TrustedProxyPolicy::Default(parse_proxy_networks(
            DEFAULT_TRUSTED_PROXY_CIDRS,
        )?));
    }
    if raw.eq_ignore_ascii_case("none") {
        return Ok(TrustedProxyPolicy::Disabled);
    }
    let entries = raw
        .split(',')
        .map(str::trim)
        .filter(|entry| !entry.is_empty())
        .collect::<Vec<_>>();
    if entries.is_empty()
        || entries
            .iter()
            .any(|entry| entry.eq_ignore_ascii_case("none"))
    {
        return Err(ConfigError::Invalid("TRUSTED_PROXIES"));
    }
    Ok(TrustedProxyPolicy::Explicit(parse_proxy_networks(
        &entries,
    )?))
}

fn parse_proxy_networks(entries: &[&str]) -> Result<Vec<ipnet::IpNet>, ConfigError> {
    entries
        .iter()
        .map(|entry| {
            entry
                .parse::<IpAddr>()
                .map(ipnet::IpNet::from)
                .or_else(|_| entry.parse::<ipnet::IpNet>())
                .map_err(|_| ConfigError::Invalid("TRUSTED_PROXIES"))
        })
        .collect()
}
fn boolean(name: &'static str, default: bool) -> Result<bool, ConfigError> {
    env::var(name).map_or(Ok(default), |raw| match raw.as_str() {
        "true" | "1" => Ok(true),
        "false" | "0" => Ok(false),
        _ => Err(ConfigError::Invalid(name)),
    })
}
fn boolean_with_legacy(
    primary: &'static str,
    legacy: &'static str,
    default: bool,
) -> Result<bool, ConfigError> {
    if env::var_os(primary).is_some() {
        boolean(primary, default)
    } else {
        boolean(legacy, default)
    }
}

#[cfg(test)]
mod tests {
    use super::{Config, TrustedProxyPolicy, TurnstileConfig};
    use lmm_api_rs::protocol_rollout::ProtocolRolloutConfig;
    use lmm_api_rs::status::TurnstilePublicConfig;
    use lmm_application::ValkeyReadinessPolicy;
    use secrecy::SecretString;
    use std::{net::SocketAddr, time::Duration};

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn enabled_turnstile_options(
        site_key: Option<&str>,
    ) -> std::collections::BTreeMap<String, String> {
        let mut options = std::collections::BTreeMap::from([(
            "TurnstileCheckEnabled".to_owned(),
            "true".to_owned(),
        )]);
        if let Some(site_key) = site_key {
            options.insert("TurnstileSiteKey".to_owned(), site_key.to_owned());
        }
        options
    }

    #[test]
    fn debug_should_redact_connection_urls() {
        let config = Config {
            listen_addr: SocketAddr::from(([127, 0, 0, 1], 3001)),
            slot: "green".to_owned(),
            database_url: "postgres://secret@localhost/database".to_owned(),
            valkey_url: "redis://:secret@localhost".to_owned(),
            schema_contract: 1,
            dependency_timeout: Duration::from_secs(2),
            drain_timeout: Duration::from_secs(30),
            public_content_cache_ttl: Duration::from_secs(5),
            valkey_readiness_policy: ValkeyReadinessPolicy::RequiredForRateLimiting,
            global_api_rate_limit: 360,
            global_api_rate_limit_window: Duration::from_secs(180),
            crypto_secret: SecretString::from(
                "another-secret-that-must-not-appear-in-debug".to_owned(),
            ),
            models_cache_ttl: Duration::from_secs(60),
            auth_session_secret: SecretString::from("must-never-appear-in-debug".to_owned()),
            auth_password_login_enabled: false,
            auth_turnstile: TurnstileConfig {
                enabled: false,
                secret_key: None,
            },
            auth_cookie_secure: true,
            auth_trusted_origins: Vec::new(),
            auth_anonymous_body_limit_bytes: 512 * 1024,
            auth_active_session_limit: 50,
            auth_issuance_limit: 100,
            auth_issuance_window: Duration::from_secs(24 * 60 * 60),
            auth_session_cache_ttl: Duration::from_secs(600),
            auth_critical_rate_limit_enabled: true,
            auth_critical_rate_limit: 20,
            auth_critical_rate_limit_window: Duration::from_secs(20 * 60),
            api_token_search_rate_limit_enabled: true,
            api_token_search_rate_limit: 10,
            api_token_search_rate_limit_window: Duration::from_secs(60),
            trusted_proxies: TrustedProxyPolicy::Disabled,
            test_instance: false,
            local_acceptance: false,
            protocol_rollout: ProtocolRolloutConfig::default(),
        };
        let rendered = format!("{config:?}");
        assert!(!rendered.contains("postgres://secret"));
        assert!(!rendered.contains("redis://:secret"));
        assert!(!rendered.contains("must-never-appear-in-debug"));
        assert!(!rendered.contains("another-secret-that-must-not-appear-in-debug"));
        assert!(rendered.contains("[REDACTED]"));
    }

    #[test]
    fn crypto_secret_requires_at_least_32_non_whitespace_bytes() {
        assert!(super::validated_secret("x".repeat(31), "CRYPTO_SECRET").is_err());
        assert!(super::validated_secret(" ".repeat(32), "CRYPTO_SECRET").is_err());
        assert!(super::validated_secret("x".repeat(32), "CRYPTO_SECRET").is_ok());
        assert!(super::validated_secret(
            "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES".to_owned(),
            "CRYPTO_SECRET"
        )
        .is_err());
    }

    #[test]
    fn enabled_turnstile_requires_a_nonblank_secret() {
        assert!(matches!(
            TurnstileConfig {
                enabled: true,
                secret_key: None,
            }
            .resolve_public(&enabled_turnstile_options(None)),
            Err(super::ConfigError::Invalid("TurnstileSiteKey"))
        ));
        assert!(matches!(
            super::turnstile_from_values(true, None),
            Err(super::ConfigError::Missing("TURNSTILE_SECRET_KEY"))
        ));
        assert!(matches!(
            super::turnstile_from_values(true, Some("  ")),
            Err(super::ConfigError::Invalid("TURNSTILE_SECRET_KEY"))
        ));
    }

    #[test]
    fn turnstile_rejects_database_enable_mismatch_and_blank_site_key() -> TestResult {
        let enabled = super::turnstile_from_values(true, Some("secret"))?;
        assert!(matches!(
            enabled.resolve_public(&std::collections::BTreeMap::new()),
            Err(super::ConfigError::Invalid("TurnstileCheckEnabled"))
        ));
        assert!(matches!(
            enabled.resolve_public(&enabled_turnstile_options(None)),
            Err(super::ConfigError::Invalid("TurnstileSiteKey"))
        ));
        Ok(())
    }

    #[test]
    fn turnstile_resolves_only_consistent_public_state() -> TestResult {
        let config = super::turnstile_from_values(true, Some("secret"))?;
        let public = config.resolve_public(&enabled_turnstile_options(Some("site-key")))?;
        assert!(public.enabled);
        assert_eq!(public.site_key, "site-key");

        let disabled = super::turnstile_from_values(false, None)?;
        assert_eq!(
            disabled.resolve_public(&std::collections::BTreeMap::new())?,
            TurnstilePublicConfig::disabled()
        );
        Ok(())
    }

    #[test]
    fn test_instance_database_requires_dedicated_database_role_and_schema() {
        let valid = "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dlmm_test_runtime_v1";
        assert!(super::validate_test_database_url(valid).is_ok());
        for invalid in [
            "postgres://lmm_api:secret@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dlmm_test_runtime_v1",
            "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_prod?options=-csearch_path%3Dlmm_test_runtime_v1",
            "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dpublic",
            "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_test_runtime",
            "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dlmm_test_a%2Cpublic",
            "postgres://lmm_test_runtime:secret@10.0.0.10/lmm_test_runtime?options=-csearch_path%3Dlmm_test_runtime_v1",
            "postgres://lmm_test_runtime:secret@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dlmm_test_runtime_v1%20-crole%3Dproduction",
            "postgres://lmm_test_runtime:REPLACE_ME@127.0.0.1/lmm_test_runtime?options=-csearch_path%3Dlmm_test_runtime_v1",
        ] {
            assert!(
                super::validate_test_database_url(invalid).is_err(),
                "{invalid}"
            );
        }
    }

    #[test]
    fn test_instance_listener_must_be_literal_loopback() -> TestResult {
        for valid in ["127.0.0.1:3100", "[::1]:3100"] {
            assert!(
                super::validate_test_listener(valid.parse()?).is_ok(),
                "{valid}"
            );
        }
        for invalid in ["0.0.0.0:3100", "192.0.2.10:3100", "[::]:3100"] {
            assert!(
                super::validate_test_listener(invalid.parse()?).is_err(),
                "{invalid}"
            );
        }
        Ok(())
    }

    #[test]
    fn test_instance_valkey_must_be_loopback_and_dedicated_port() {
        assert!(super::validate_test_valkey_url("redis://:secret@127.0.0.1:6380/0").is_ok());
        assert!(super::validate_test_valkey_url_with_port(
            "redis://:secret@127.0.0.1:23456/0",
            23456
        )
        .is_ok());
        assert!(super::validate_test_valkey_url_with_port(
            "redis://:secret@127.0.0.1:23456/0",
            6380
        )
        .is_err());
        for invalid in [
            "redis://:secret@127.0.0.1:6379/0",
            "redis://:secret@10.0.0.12:6380/0",
            "redis://:secret@localhost:6380/0",
        ] {
            assert!(
                super::validate_test_valkey_url(invalid).is_err(),
                "{invalid}"
            );
        }
    }

    #[test]
    fn test_instance_valkey_port_override_is_nonzero_and_defaults_safely() -> TestResult {
        assert_eq!(super::parse_test_valkey_port(None)?, 6380);
        assert_eq!(super::parse_test_valkey_port(Some("23456"))?, 23456);
        for invalid in ["", "0", "65536", "23456 "] {
            assert!(
                super::parse_test_valkey_port(Some(invalid)).is_err(),
                "{invalid:?}"
            );
        }
        Ok(())
    }

    #[test]
    fn blue_green_slot_identity_is_strict() -> TestResult {
        assert_eq!(super::validated_slot("blue".to_owned(), false)?, "blue");
        assert_eq!(super::validated_slot("green".to_owned(), false)?, "green");
        assert!(super::validated_slot("single".to_owned(), false).is_err());
        assert!(super::validated_slot("canary".to_owned(), false).is_err());
        Ok(())
    }

    #[test]
    fn test_instance_slot_is_single_only() -> TestResult {
        assert_eq!(super::validated_slot("single".to_owned(), true)?, "single");
        assert!(super::validated_slot("blue".to_owned(), true).is_err());
        assert!(super::validated_slot("green".to_owned(), true).is_err());
        Ok(())
    }

    #[test]
    fn test_instance_rejects_checked_in_or_obvious_secret_placeholders() {
        for placeholder in [
            "REPLACE_ME",
            "REPLACE_WITH_AT_LEAST_32_RANDOM_BYTES",
            "change-me-before-deploying",
            "your_secret_here",
            "example-secret-value",
        ] {
            assert!(
                super::validate_test_secret(placeholder, "SESSION_SECRET").is_err(),
                "{placeholder}"
            );
        }
    }

    #[test]
    fn test_instance_requires_the_exact_opt_in_value() -> TestResult {
        assert!(!super::parse_test_instance_value(None)?);
        assert!(super::parse_test_instance_value(Some("1"))?);
        for invalid in ["", "0", "true", "01", "1 "] {
            assert!(
                super::parse_test_instance_value(Some(invalid)).is_err(),
                "{invalid:?}"
            );
        }
        Ok(())
    }

    #[test]
    fn local_acceptance_disabled_by_default() -> TestResult {
        let addr: SocketAddr = "127.0.0.1:3101".parse()?;
        for flag in ["", "false", "TRUE", "1"] {
            assert!(!super::parse_local_acceptance_policy(flag, addr)?);
        }
        Ok(())
    }

    #[test]
    fn local_acceptance_ipv4_loopback() -> TestResult {
        let addr: SocketAddr = "127.0.0.1:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr)?);
        Ok(())
    }

    #[test]
    fn local_acceptance_ipv6_loopback() -> TestResult {
        let addr: SocketAddr = "[::1]:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr)?);
        Ok(())
    }

    #[test]
    fn local_acceptance_rejects_ipv4_wildcard() -> TestResult {
        let addr: SocketAddr = "0.0.0.0:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr).is_err());
        Ok(())
    }

    #[test]
    fn local_acceptance_rejects_ipv6_wildcard() -> TestResult {
        let addr: SocketAddr = "[::]:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr).is_err());
        Ok(())
    }

    #[test]
    fn local_acceptance_rejects_other_loopback_address() -> TestResult {
        let addr: SocketAddr = "127.0.0.2:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr).is_err());
        Ok(())
    }

    #[test]
    fn local_acceptance_rejects_public_address() -> TestResult {
        let addr: SocketAddr = "192.0.2.10:3101".parse()?;
        assert!(super::parse_local_acceptance_policy("true", addr).is_err());
        Ok(())
    }

    #[test]
    fn is_exact_loopback_accepts_only_canonical_addresses() {
        use std::net::{IpAddr, Ipv4Addr, Ipv6Addr};
        assert!(super::is_exact_loopback(IpAddr::V4(Ipv4Addr::LOCALHOST)));
        assert!(super::is_exact_loopback(IpAddr::V6(Ipv6Addr::LOCALHOST)));
        // 127.0.0.2 is in the loopback range but not the canonical address
        assert!(!super::is_exact_loopback(IpAddr::V4(Ipv4Addr::new(
            127, 0, 0, 2
        ))));
        // Wildcard
        assert!(!super::is_exact_loopback(IpAddr::V4(Ipv4Addr::UNSPECIFIED)));
        assert!(!super::is_exact_loopback(IpAddr::V6(Ipv6Addr::UNSPECIFIED)));
        // Public
        assert!(!super::is_exact_loopback(IpAddr::V4(Ipv4Addr::new(
            192, 0, 2, 10
        ))));
    }

    #[test]
    fn drain_timeout_must_leave_the_systemd_supervisor_margin() -> TestResult {
        assert_eq!(
            super::bounded_seconds("LMM_DRAIN_TIMEOUT_SECONDS", 30, 40)?,
            Duration::from_secs(30)
        );
        assert!(super::bounded_seconds_value(0, "LMM_DRAIN_TIMEOUT_SECONDS", 40).is_err());
        assert!(super::bounded_seconds_value(41, "LMM_DRAIN_TIMEOUT_SECONDS", 40).is_err());
        Ok(())
    }

    #[test]
    fn public_content_cache_ttl_should_reject_zero_seconds() {
        assert!(matches!(
            super::public_content_cache_ttl_value(0),
            Err(super::ConfigError::Invalid(
                "LMM_PUBLIC_CONTENT_CACHE_TTL_SECONDS"
            ))
        ));
    }

    #[test]
    fn public_content_cache_ttl_should_accept_one_second() -> TestResult {
        assert_eq!(
            super::public_content_cache_ttl_value(1)?,
            Duration::from_secs(1)
        );
        Ok(())
    }

    #[test]
    fn public_content_cache_ttl_should_accept_five_seconds() -> TestResult {
        assert_eq!(
            super::public_content_cache_ttl_value(5)?,
            Duration::from_secs(5)
        );
        Ok(())
    }

    #[test]
    fn public_content_cache_ttl_should_reject_six_seconds() {
        assert!(matches!(
            super::public_content_cache_ttl_value(6),
            Err(super::ConfigError::Invalid(
                "LMM_PUBLIC_CONTENT_CACHE_TTL_SECONDS"
            ))
        ));
    }

    #[test]
    fn public_content_cache_ttl_should_reject_the_largest_u64() {
        assert!(matches!(
            super::public_content_cache_ttl_value(u64::MAX),
            Err(super::ConfigError::Invalid(
                "LMM_PUBLIC_CONTENT_CACHE_TTL_SECONDS"
            ))
        ));
    }

    #[test]
    fn trusted_cookie_origins_must_be_exact_https_origins() -> TestResult {
        assert_eq!(
            super::trusted_https_origin(
                "https://Panel.Example:8443",
                "SESSION_COOKIE_TRUSTED_URL"
            )?,
            "https://panel.example:8443"
        );
        for invalid in [
            "http://panel.example",
            "https://panel.example/path",
            "https://panel.example?query=1",
            "https://user@panel.example",
            "https://panel.example,",
        ] {
            assert!(
                super::trusted_https_origin(invalid, "SESSION_COOKIE_TRUSTED_URL").is_err(),
                "{invalid} must not become a trusted origin"
            );
        }
        Ok(())
    }

    #[test]
    fn trusted_proxy_policy_matches_the_legacy_default_none_and_explicit_contracts() -> TestResult {
        let defaults = super::parse_trusted_proxies(" ")?;
        assert!(defaults.trusts("127.0.0.1".parse()?));
        assert!(defaults.trusts("172.20.0.2".parse()?));
        assert!(!defaults.trusts("198.51.100.10".parse()?));

        let disabled = super::parse_trusted_proxies(" NoNe ")?;
        assert!(!disabled.trusts("127.0.0.1".parse()?));

        let explicit = super::parse_trusted_proxies(" 192.0.2.0/24, 198.51.100.30 ")?;
        assert!(explicit.trusts("192.0.2.10".parse()?));
        assert!(explicit.trusts("198.51.100.30".parse()?));
        assert!(!explicit.trusts("127.0.0.1".parse()?));
        for invalid in [", ,", "none,127.0.0.1", "not-an-ip"] {
            assert!(super::parse_trusted_proxies(invalid).is_err(), "{invalid}");
        }
        Ok(())
    }
}
