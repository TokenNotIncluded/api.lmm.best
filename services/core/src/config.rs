use serde::Deserialize;
use std::{
    collections::{BTreeMap, BTreeSet},
    env, fs, io,
    io::Read,
    path::{Path, PathBuf},
    sync::Arc,
    time::Duration,
};

fn invalid_config(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

/// A bounded core pool. RPC identity and event delivery use separate pools.
pub async fn database_pool(url: &str) -> io::Result<sqlx::PgPool> {
    sqlx::postgres::PgPoolOptions::new()
        .max_connections(database_connections()?)
        .min_connections(0)
        .idle_timeout(Some(Duration::from_secs(30)))
        .acquire_timeout(Duration::from_secs(3))
        .after_connect(|connection, _| {
            Box::pin(async move {
                sqlx::query("SET statement_timeout='5s'")
                    .execute(&mut *connection)
                    .await?;
                sqlx::query("SET idle_in_transaction_session_timeout='10s'")
                    .execute(&mut *connection)
                    .await?;
                Ok(())
            })
        })
        .connect(url)
        .await
        .map_err(|_| io::Error::other("core database connection unavailable"))
}

fn bool_setting(name: &'static str) -> io::Result<bool> {
    match env::var(name).as_deref() {
        Ok("true" | "1") => Ok(true),
        Ok("false" | "0") | Err(env::VarError::NotPresent) => Ok(false),
        _ => Err(invalid_config("invalid boolean core setting")),
    }
}

/// Secret/configuration files must be private regular files. Errors deliberately
/// omit paths and contents, which may carry operator-provided credential data.
pub(crate) fn private_file(path: &Path, maximum: usize) -> io::Result<Vec<u8>> {
    if !path.is_absolute() {
        return Err(invalid_config("core configuration file must be absolute"));
    }
    let before = fs::symlink_metadata(path)
        .map_err(|_| invalid_config("core configuration file unavailable"))?;
    if !before.is_file() {
        return Err(invalid_config("core configuration must be a regular file"));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if before.permissions().mode() & 0o077 != 0 {
            return Err(invalid_config("core configuration file must be private"));
        }
    }
    let file =
        fs::File::open(path).map_err(|_| invalid_config("core configuration file unavailable"))?;
    let opened = file
        .metadata()
        .map_err(|_| invalid_config("core configuration file unavailable"))?;
    #[cfg(unix)]
    {
        use std::os::unix::fs::MetadataExt;
        if before.dev() != opened.dev() || before.ino() != opened.ino() {
            return Err(invalid_config(
                "core configuration file changed while opening",
            ));
        }
        use std::os::unix::fs::PermissionsExt;
        if opened.permissions().mode() & 0o077 != 0 {
            return Err(invalid_config("core configuration file must be private"));
        }
    }
    let mut bytes = Vec::new();
    file.take((maximum + 1) as u64)
        .read_to_end(&mut bytes)
        .map_err(|_| invalid_config("core configuration file unavailable"))?;
    if bytes.len() > maximum {
        return Err(invalid_config("core configuration file is too large"));
    }
    Ok(bytes)
}

pub struct WebAuthConfig {
    pub auth: crate::legacy_auth::LegacyAuthConfig,
    pub registration_enabled: bool,
    pub user_agreement_enabled: bool,
    pub privacy_policy_enabled: bool,
}

fn explicit_legal_policy(
    agreement: Option<&str>,
    privacy: Option<&str>,
) -> io::Result<(bool, bool)> {
    let parse = |value| match value {
        Some("true") => Ok(true),
        Some("false") => Ok(false),
        _ => Err(invalid_config(
            "Web legal publication flags must each be explicitly true or false",
        )),
    };
    Ok((parse(agreement)?, parse(privacy)?))
}

pub fn legacy_auth() -> io::Result<Option<WebAuthConfig>> {
    let origin = env::var("LMM_CORE_WEB_ORIGIN");
    let key_file = env::var_os("LMM_CORE_WEB_REFRESH_KEY_FILE");
    let local_http = bool_setting("LMM_CORE_WEB_LOCAL_HTTP")?;
    match (origin, key_file) {
        (Err(env::VarError::NotPresent), None) if !local_http => Ok(None),
        (Ok(origin), Some(path)) => {
            let agreement = env::var("LMM_CORE_WEB_USER_AGREEMENT_ENABLED");
            let privacy = env::var("LMM_CORE_WEB_PRIVACY_POLICY_ENABLED");
            let (user_agreement_enabled, privacy_policy_enabled) =
                explicit_legal_policy(agreement.as_deref().ok(), privacy.as_deref().ok())?;
            let key = private_file(Path::new(&path), 128)?;
            let config = crate::legacy_auth::LegacyAuthConfig::new(&origin, &key)
                .map_err(|_| invalid_config("invalid Web authentication configuration"))?;
            let config = if local_http {
                config
                    .with_insecure_local_http()
                    .map_err(|_| invalid_config("local Web HTTP origin is not allowed"))?
            } else {
                config
            };
            Ok(Some(WebAuthConfig {
                auth: config.with_legal_acceptance_required(
                    user_agreement_enabled || privacy_policy_enabled,
                ),
                registration_enabled: bool_setting("LMM_CORE_REGISTRATION_ENABLED")?,
                user_agreement_enabled,
                privacy_policy_enabled,
            }))
        }
        _ => Err(invalid_config(
            "configure both Web origin and refresh key file",
        )),
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelUpstream {
    id: u64,
    endpoint: String,
    protocol: crate::routing::Protocol,
    credential: crate::routing::CredentialRef,
    enabled: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelRouting {
    version: u64,
    groups: Vec<crate::routing::ModelGroup>,
    upstreams: Vec<ModelUpstream>,
    routes: Vec<crate::routing::ModelRoute>,
    aliases: Vec<crate::routing::ModelAlias>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelCredential {
    reference: crate::routing::CredentialRef,
    secret_file: PathBuf,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct KeyGroup {
    key_id: i64,
    group: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelConfig {
    routing: ModelRouting,
    prices: crate::routing::PriceConfig,
    credentials: Vec<ModelCredential>,
    key_groups: Vec<KeyGroup>,
    input_token_ceiling: i64,
    lease_seconds: i64,
    timeout_seconds: u64,
    max_output_tokens: u32,
}

fn model_gateway(
    pool: sqlx::PgPool,
    document: &[u8],
    local_http: bool,
) -> io::Result<crate::model_http::ModelGateway> {
    // The first assembled gateway is limited to isolated synthetic providers.
    // External billing needs trusted model context/usage bounds before rollout.
    if !local_http {
        return Err(invalid_config(
            "model gateway currently requires isolated loopback mode",
        ));
    }
    let config: ModelConfig = serde_json::from_slice(document)
        .map_err(|_| invalid_config("invalid model configuration document"))?;
    if config.routing.routes.is_empty()
        || config.key_groups.is_empty()
        || config.credentials.is_empty()
        || !(1..=240).contains(&config.timeout_seconds)
        || !(1..=65_536).contains(&config.max_output_tokens)
    {
        return Err(invalid_config(
            "model configuration has no usable grants or exceeds limits",
        ));
    }
    let upstreams = config
        .routing
        .upstreams
        .into_iter()
        .map(|upstream| {
            let endpoint = if local_http {
                // Local mode cannot accidentally contact an actual paid provider.
                crate::routing::Endpoint::local_loopback(upstream.endpoint)
            } else {
                crate::routing::Endpoint::try_from(upstream.endpoint)
            }
            .map_err(|_| invalid_config("invalid model upstream endpoint"))?;
            Ok(crate::routing::UpstreamConfig {
                id: upstream.id,
                endpoint,
                protocol: upstream.protocol,
                credential: upstream.credential,
                enabled: upstream.enabled,
            })
        })
        .collect::<io::Result<Vec<_>>>()?;
    let required: BTreeSet<_> = upstreams
        .iter()
        .filter(|u| u.enabled)
        .map(|u| (u.credential.id, u.credential.revision))
        .collect();
    let mut supplied = BTreeSet::new();
    let mut credentials = Vec::new();
    for credential in config.credentials {
        let reference = credential.reference;
        if !supplied.insert((reference.id, reference.revision)) {
            return Err(invalid_config("duplicate model credential reference"));
        }
        let bytes = private_file(&credential.secret_file, 4096)?;
        let secret = String::from_utf8(bytes)
            .map_err(|_| invalid_config("invalid model upstream credential"))?;
        let secret = secret.trim();
        if secret.is_empty() || !secret.bytes().all(|b| b.is_ascii_graphic()) {
            return Err(invalid_config("invalid model upstream credential"));
        }
        credentials.push(crate::model_http::UpstreamCredential {
            reference,
            secret: secret.to_owned(),
        });
    }
    if required.is_empty() || required != supplied {
        return Err(invalid_config(
            "model credential references do not match enabled upstreams",
        ));
    }
    let mut key_groups = BTreeMap::new();
    for grant in config.key_groups {
        if key_groups.insert(grant.key_id, grant.group).is_some() {
            return Err(invalid_config("duplicate model key grant"));
        }
    }
    let routing = crate::routing::RoutingConfig {
        version: config.routing.version,
        groups: config.routing.groups,
        upstreams,
        routes: config.routing.routes,
        aliases: config.routing.aliases,
    };
    let prices = crate::routing::PriceSnapshot::compile(config.prices)
        .map_err(|_| invalid_config("invalid model prices"))?;
    let routing = crate::routing::Router::new(routing, Arc::new(prices))
        .map_err(|_| invalid_config("invalid model routes"))?;
    let limits = crate::relay::Limits {
        max_in_flight: max_inflight()?,
        total_timeout: Duration::from_secs(config.timeout_seconds),
        header_timeout: Duration::from_secs(config.timeout_seconds.min(30)),
        read_timeout: Duration::from_secs(config.timeout_seconds.min(30)),
        hook_timeout: Duration::from_secs(5),
        max_output_tokens: config.max_output_tokens,
        ..crate::relay::Limits::default()
    };
    crate::model_http::ModelGateway::new(
        pool,
        Arc::new(routing),
        credentials,
        key_groups,
        limits,
        crate::model_http::ChargePolicy {
            input_token_ceiling: config.input_token_ceiling,
            lease_seconds: config.lease_seconds,
        },
    )
    .map_err(|_| invalid_config("unsafe or incomplete model gateway configuration"))
}

pub fn models(pool: sqlx::PgPool) -> io::Result<Option<crate::model_http::ModelGateway>> {
    let local_http = bool_setting("LMM_CORE_MODEL_LOCAL_HTTP")?;
    match env::var_os("LMM_CORE_MODEL_CONFIG_FILE") {
        Some(path) => model_gateway(
            pool,
            &private_file(Path::new(&path), 16 * 1024 * 1024)?,
            local_http,
        )
        .map(Some),
        None if !local_http => Ok(None),
        None => Err(invalid_config(
            "local model HTTP requires a model configuration file",
        )),
    }
}

pub fn database_url() -> io::Result<Option<String>> {
    let inline = env::var_os("LMM_CORE_DATABASE_URL")
        .map(|value| {
            value.into_string().map_err(|_| {
                io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "database URL is not valid UTF-8",
                )
            })
        })
        .transpose()?;
    let file = env::var_os("LMM_CORE_DATABASE_URL_FILE").map(PathBuf::from);
    read_database_url(inline, file)
}
/// Resource limits for the independently deployed Rust process.
fn parse_limit(name: &str, value: Option<&str>, default: u32, maximum: u32) -> io::Result<u32> {
    let number = match value {
        Some(raw) => raw
            .parse::<u32>()
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, format!("invalid {name}")))?,
        None => default,
    };
    if number == 0 || number > maximum {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            format!("invalid {name}: expected 1..={maximum}"),
        ));
    }
    Ok(number)
}

fn configured_limit(name: &str, default: u32, maximum: u32) -> io::Result<u32> {
    match env::var(name) {
        Ok(value) => parse_limit(name, Some(&value), default, maximum),
        Err(env::VarError::NotPresent) => parse_limit(name, None, default, maximum),
        Err(env::VarError::NotUnicode(_)) => Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            format!("invalid {name}"),
        )),
    }
}

pub fn worker_threads() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_WORKER_THREADS", 1, 4)? as usize)
}

pub fn database_connections() -> io::Result<u32> {
    configured_limit("LMM_CORE_DB_CONNECTIONS", 2, 8)
}

pub fn password_slots() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_PASSWORD_SLOTS", 1, 2)? as usize)
}

pub fn max_inflight() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_MAX_INFLIGHT", 16, 64)? as usize)
}

fn read_database_url(inline: Option<String>, file: Option<PathBuf>) -> io::Result<Option<String>> {
    let value = match (inline, file) {
        (Some(_), Some(_)) => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "configure exactly one database URL source",
            ));
        }
        (Some(value), None) => value,
        (None, Some(path)) => String::from_utf8(private_file(&path, 16 * 1024)?)
            .map_err(|_| invalid_config("database URL is not valid UTF-8"))?,
        (None, None) => return Ok(None),
    };
    let value = value.trim().to_owned();
    if value.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "database URL is empty",
        ));
    }
    Ok(Some(value))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn resource_limits_reject_invalid_or_unbounded_settings() {
        assert_eq!(parse_limit("TEST_LIMIT", None, 2, 8).unwrap(), 2);
        assert_eq!(parse_limit("TEST_LIMIT", Some("1"), 2, 8).unwrap(), 1);
        for invalid in ["", "0", "9", "-1", "1.5", "not-a-number"] {
            assert!(parse_limit("TEST_LIMIT", Some(invalid), 2, 8).is_err());
        }
    }

    #[test]
    fn database_configuration_is_explicit_and_fails_closed() {
        assert_eq!(read_database_url(None, None).unwrap(), None);
        assert!(read_database_url(Some(" ".into()), None).is_err());
        assert!(read_database_url(Some("postgres://test".into()), Some("unused".into())).is_err());
        assert_eq!(
            read_database_url(Some("postgres://test\n".into()), None)
                .unwrap()
                .as_deref(),
            Some("postgres://test")
        );
    }

    #[test]
    fn web_legal_publication_must_be_explicit_and_unambiguous() {
        assert_eq!(
            explicit_legal_policy(Some("false"), Some("false")).unwrap(),
            (false, false)
        );
        assert_eq!(
            explicit_legal_policy(Some("true"), Some("false")).unwrap(),
            (true, false)
        );
        for (agreement, privacy) in [
            (None, Some("false")),
            (Some("true"), None),
            (Some("1"), Some("false")),
            (Some("false"), Some("FALSE")),
        ] {
            assert!(explicit_legal_policy(agreement, privacy).is_err());
        }
    }

    #[cfg(unix)]
    #[test]
    fn private_configuration_rejects_public_symlink_and_oversized_files() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let directory = std::env::temp_dir().join(format!(
            "lmm-runtime-config-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        fs::create_dir(&directory).unwrap();
        let path = directory.join("secret");
        fs::write(&path, b"test-value").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(private_file(&path, 128).is_err());
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        assert_eq!(private_file(&path, 128).unwrap(), b"test-value");
        assert!(private_file(&path, 4).is_err());
        let link = directory.join("link");
        symlink(&path, &link).unwrap();
        assert!(private_file(&link, 128).is_err());
        assert!(private_file(Path::new("relative"), 128).is_err());
        fs::remove_dir_all(directory).unwrap();
    }

    #[tokio::test]
    async fn model_configuration_cannot_enable_external_traffic_or_empty_grants() {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgresql://synthetic:synthetic@127.0.0.1:1/synthetic")
            .unwrap();
        assert!(
            model_gateway(pool.clone(), b"do-not-log-this-secret", false)
                .err()
                .unwrap()
                .to_string()
                .contains("isolated loopback")
        );
        let empty = br#"{"routing":{"version":1,"groups":[],"upstreams":[],"routes":[],"aliases":[]},"prices":{"version":1,"rules":[]},"credentials":[],"key_groups":[],"input_token_ceiling":1024,"lease_seconds":90,"timeout_seconds":60,"max_output_tokens":4096}"#;
        assert!(model_gateway(pool.clone(), empty, true).is_err());
        assert!(
            !model_gateway(pool, b"do-not-log-this-secret", true)
                .err()
                .unwrap()
                .to_string()
                .contains("do-not-log-this-secret")
        );
    }
}
