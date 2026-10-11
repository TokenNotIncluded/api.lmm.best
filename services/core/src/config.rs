use serde::Deserialize;
use std::{
    collections::{BTreeMap, BTreeSet},
    env,
    ffi::OsString,
    fs, io,
    io::Read,
    path::{Component, Path, PathBuf},
    sync::{Arc, OnceLock},
    time::Duration,
};

fn invalid_config(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

pub const DEFAULT_CONFIG: &str = "/etc/lmm.conf.d/core.toml";

enum Source {
    LegacyEnv,
    File(BTreeMap<&'static str, OsString>),
}
static SOURCE: OnceLock<Source> = OnceLock::new();

/// Called once by the executable before reading any runtime settings. File mode
/// never reads or changes process environment, and never falls back to it.
pub fn initialize(path: Option<&Path>, legacy_env: bool) -> io::Result<()> {
    if legacy_env && path.is_some() {
        return Err(invalid_config(
            "--config and --legacy-env are mutually exclusive",
        ));
    }
    let source = if legacy_env {
        Source::LegacyEnv
    } else {
        if env::vars_os().any(|(key, _)| key.as_encoded_bytes().starts_with(b"LMM_CORE_")) {
            return Err(invalid_config(
                "TOML configuration cannot be mixed with LMM_CORE environment settings",
            ));
        }
        let document = configuration_file(path.unwrap_or(Path::new(DEFAULT_CONFIG)), 256 * 1024)?;
        let values = parse_core_file(&document)?;
        // Even an offline command must not accept references to broadly
        // readable secrets or unsafe sidecar paths in its selected config.
        for (name, path) in &values {
            if name.ends_with("_FILE") {
                if name.ends_with("_CONFIG_FILE") {
                    configuration_file(Path::new(path), 16 * 1024 * 1024)?;
                } else {
                    private_file(Path::new(path), 16 * 1024)?;
                }
            }
        }
        Source::File(values)
    };
    SOURCE
        .set(source)
        .map_err(|_| invalid_config("core configuration was already initialized"))
}

pub fn setting_os(name: &str) -> Option<OsString> {
    match SOURCE.get() {
        Some(Source::LegacyEnv) => env::var_os(name),
        Some(Source::File(values)) => values.get(name).cloned(),
        None => None,
    }
}

pub fn setting(name: &str) -> Result<String, env::VarError> {
    setting_os(name)
        .ok_or(env::VarError::NotPresent)?
        .into_string()
        .map_err(env::VarError::NotUnicode)
}

fn default_listen() -> String {
    "127.0.0.1:8080".to_owned()
}
fn one() -> u32 {
    1
}
fn two() -> u32 {
    2
}
fn sixteen() -> u32 {
    16
}
fn thirty() -> u32 {
    30
}

#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
struct ServerFile {
    listen: String,
    worker_threads: u32,
    max_inflight: u32,
    drain_timeout_seconds: u32,
}
impl Default for ServerFile {
    fn default() -> Self {
        Self {
            listen: default_listen(),
            worker_threads: one(),
            max_inflight: sixteen(),
            drain_timeout_seconds: thirty(),
        }
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct DatabaseFile {
    url_file: PathBuf,
    #[serde(default = "two")]
    max_connections: u32,
}
#[derive(Deserialize)]
#[serde(default, deny_unknown_fields)]
struct IdentityFile {
    registration_enabled: bool,
    password_slots: u32,
    api_key_encryption_key_file: Option<PathBuf>,
    web: Option<WebFile>,
    google: Option<GoogleFile>,
}
impl Default for IdentityFile {
    fn default() -> Self {
        Self {
            registration_enabled: false,
            password_slots: one(),
            api_key_encryption_key_file: None,
            web: None,
            google: None,
        }
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct WebFile {
    origin: String,
    refresh_key_file: PathBuf,
    #[serde(default)]
    local_http: bool,
    // Publication facts must be explicit whenever Web authentication is enabled.
    user_agreement_enabled: bool,
    privacy_policy_enabled: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct GoogleFile {
    client_id: String,
    client_secret_file: PathBuf,
    redirect_uri: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct RpcFile {
    socket: PathBuf,
    token_file: PathBuf,
    shared_group_id: Option<u32>,
}
#[derive(Deserialize, Default)]
#[serde(default, deny_unknown_fields)]
struct ConsoleFile {
    enabled: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ModelFile {
    config_file: PathBuf,
    #[serde(default)]
    local_http: bool,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct FinancialFile {
    config_file: PathBuf,
    token_file: PathBuf,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct IngressFile {
    ack_url: String,
    token_file: PathBuf,
    instance: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct CoreFile {
    version: u32,
    #[serde(default)]
    server: ServerFile,
    database: Option<DatabaseFile>,
    #[serde(default)]
    identity: IdentityFile,
    rpc: Option<RpcFile>,
    #[serde(default)]
    console: ConsoleFile,
    model: Option<ModelFile>,
    payments: Option<FinancialFile>,
    commerce: Option<FinancialFile>,
    ingress: Option<IngressFile>,
}

fn parse_document<T: serde::de::DeserializeOwned>(document: &[u8]) -> io::Result<T> {
    let document =
        std::str::from_utf8(document).map_err(|_| invalid_config("core TOML must be UTF-8"))?;
    // Parser errors can contain configuration values; never return them.
    toml::from_str(document).map_err(|_| invalid_config("invalid core TOML configuration"))
}

fn absolute_path(path: &Path) -> io::Result<()> {
    if path.to_str().is_none()
        || !path.is_absolute()
        || path
            .as_os_str()
            .as_encoded_bytes()
            .split(|b| *b == b'/')
            .any(|part| part == b"." || part == b"..")
        || path
            .components()
            .any(|c| matches!(c, Component::CurDir | Component::ParentDir))
    {
        return Err(invalid_config(
            "core paths must be absolute without dot components",
        ));
    }
    let mut prefix = PathBuf::new();
    for component in path.components() {
        prefix.push(component);
        match fs::symlink_metadata(&prefix) {
            Ok(metadata) if metadata.file_type().is_symlink() => {
                return Err(invalid_config("core paths must not contain symlinks"));
            }
            Ok(_) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(_) => return Err(invalid_config("core path unavailable")),
        }
    }
    Ok(())
}

fn parse_core_file(document: &[u8]) -> io::Result<BTreeMap<&'static str, OsString>> {
    let file: CoreFile = parse_document(document)?;
    if file.version != 1 {
        return Err(invalid_config("unsupported core configuration version"));
    }
    if file.database.is_none()
        && (file.identity.registration_enabled
            || file.identity.web.is_some()
            || file.identity.google.is_some()
            || file.identity.api_key_encryption_key_file.is_some()
            || file.console.enabled
            || file.model.is_some()
            || file.payments.is_some()
            || file.commerce.is_some())
    {
        return Err(invalid_config(
            "configured identity and business capabilities require a core database",
        ));
    }
    file.server
        .listen
        .parse::<std::net::SocketAddr>()
        .map_err(|_| invalid_config("invalid core listen address"))?;
    let mut values = BTreeMap::new();
    let mut put = |name, value: String| {
        values.insert(name, value.into());
    };
    put("LMM_CORE_LISTEN", file.server.listen);
    for (name, number, maximum) in [
        ("LMM_CORE_WORKER_THREADS", file.server.worker_threads, 4),
        ("LMM_CORE_MAX_INFLIGHT", file.server.max_inflight, 64),
        (
            "LMM_CORE_DRAIN_TIMEOUT_SECONDS",
            file.server.drain_timeout_seconds,
            300,
        ),
        ("LMM_CORE_PASSWORD_SLOTS", file.identity.password_slots, 2),
    ] {
        if number == 0 || number > maximum {
            return Err(invalid_config("invalid bounded core resource limit"));
        }
        put(name, number.to_string());
    }
    put(
        "LMM_CORE_REGISTRATION_ENABLED",
        file.identity.registration_enabled.to_string(),
    );
    put("LMM_CORE_CONSOLE_ENABLED", file.console.enabled.to_string());
    let mut paths = Vec::new();
    if let Some(db) = file.database {
        if !(1..=8).contains(&db.max_connections) {
            return Err(invalid_config("invalid core database connection limit"));
        }
        put("LMM_CORE_DB_CONNECTIONS", db.max_connections.to_string());
        paths.push(("LMM_CORE_DATABASE_URL_FILE", db.url_file));
    }
    if let Some(path) = file.identity.api_key_encryption_key_file {
        paths.push(("LMM_CORE_API_KEY_ENCRYPTION_KEY_FILE", path));
    }
    if let Some(web) = file.identity.web {
        put("LMM_CORE_WEB_ORIGIN", web.origin);
        put("LMM_CORE_WEB_LOCAL_HTTP", web.local_http.to_string());
        put(
            "LMM_CORE_WEB_USER_AGREEMENT_ENABLED",
            web.user_agreement_enabled.to_string(),
        );
        put(
            "LMM_CORE_WEB_PRIVACY_POLICY_ENABLED",
            web.privacy_policy_enabled.to_string(),
        );
        paths.push(("LMM_CORE_WEB_REFRESH_KEY_FILE", web.refresh_key_file));
    }
    if let Some(google) = file.identity.google {
        put("LMM_CORE_GOOGLE_CLIENT_ID", google.client_id);
        put("LMM_CORE_GOOGLE_REDIRECT_URI", google.redirect_uri);
        paths.push((
            "LMM_CORE_GOOGLE_CLIENT_SECRET_FILE",
            google.client_secret_file,
        ));
    }
    if let Some(rpc) = file.rpc {
        if let Some(gid) = rpc.shared_group_id {
            if gid == 0 {
                return Err(invalid_config("RPC shared group must not be root"));
            }
            put("LMM_CORE_RPC_SHARED_GROUP_ID", gid.to_string());
        }
        paths.extend([
            ("LMM_CORE_RPC_SOCKET", rpc.socket),
            ("LMM_CORE_RPC_TOKEN_FILE", rpc.token_file),
        ]);
    }
    if let Some(model) = file.model {
        put("LMM_CORE_MODEL_LOCAL_HTTP", model.local_http.to_string());
        paths.push(("LMM_CORE_MODEL_CONFIG_FILE", model.config_file));
    }
    for (section, config_name, token_name) in [
        (
            file.payments,
            "LMM_CORE_PAYMENTS_CONFIG_FILE",
            "LMM_CORE_PAYMENTS_TOKEN_FILE",
        ),
        (
            file.commerce,
            "LMM_CORE_COMMERCE_CONFIG_FILE",
            "LMM_CORE_COMMERCE_TOKEN_FILE",
        ),
    ] {
        if let Some(section) = section {
            paths.extend([
                (config_name, section.config_file),
                (token_name, section.token_file),
            ]);
        }
    }
    if let Some(ingress) = file.ingress {
        put("LMM_CORE_INGRESS_ACK_URL", ingress.ack_url);
        put("LMM_CORE_INSTANCE", ingress.instance);
        paths.push(("LMM_CORE_INGRESS_TOKEN_FILE", ingress.token_file));
    }
    for (name, path) in paths {
        absolute_path(&path)?;
        values.insert(name, path.into_os_string());
    }
    Ok(values)
}

pub fn listen_address() -> io::Result<std::net::SocketAddr> {
    match setting("LMM_CORE_LISTEN") {
        Ok(value) => value
            .parse()
            .map_err(|_| invalid_config("invalid core listen address")),
        Err(env::VarError::NotPresent) => default_listen()
            .parse()
            .map_err(|_| invalid_config("invalid default core listen address")),
        Err(_) => Err(invalid_config("invalid core listen address")),
    }
}
pub fn drain_timeout() -> io::Result<Duration> {
    Ok(Duration::from_secs(
        configured_limit("LMM_CORE_DRAIN_TIMEOUT_SECONDS", 30, 300)? as u64,
    ))
}
#[cfg(unix)]
pub fn service_token(path: &Path) -> io::Result<Vec<u8>> {
    let bytes = private_file(path, 4096)?;
    let value = std::str::from_utf8(&bytes)
        .map_err(|_| invalid_config("invalid service credential"))?
        .trim()
        .as_bytes();
    if !(32..=256).contains(&value.len()) || !value.iter().all(u8::is_ascii_graphic) {
        return Err(invalid_config("invalid service credential"));
    }
    Ok(value.to_vec())
}
pub fn rpc_shared_group() -> io::Result<Option<u32>> {
    match setting("LMM_CORE_RPC_SHARED_GROUP_ID") {
        Err(env::VarError::NotPresent) => Ok(None),
        Ok(value) => value
            .parse::<u32>()
            .ok()
            .filter(|gid| *gid != 0)
            .map(Some)
            .ok_or_else(|| invalid_config("invalid RPC shared group")),
        Err(_) => Err(invalid_config("invalid RPC shared group")),
    }
}

pub fn identity_auth(
    store: crate::identity::IdentityStore,
) -> io::Result<crate::identity::IdentityStore> {
    let mut store = if matches!(SOURCE.get(), Some(Source::LegacyEnv)) {
        store
            .with_auth_from_env()
            .map_err(|_| invalid_config("invalid identity authentication configuration"))?
    } else {
        let mut store =
            store.with_registration_enabled(bool_setting("LMM_CORE_REGISTRATION_ENABLED")?);
        if let (Ok(id), Some(path), Ok(uri)) = (
            setting("LMM_CORE_GOOGLE_CLIENT_ID"),
            setting_os("LMM_CORE_GOOGLE_CLIENT_SECRET_FILE"),
            setting("LMM_CORE_GOOGLE_REDIRECT_URI"),
        ) {
            let secret = String::from_utf8(private_file(Path::new(&path), 4096)?)
                .map_err(|_| invalid_config("invalid OAuth credential"))?;
            let google = crate::identity::GoogleOAuth::new(id, secret.trim().to_owned(), uri)
                .map_err(|_| invalid_config("invalid OAuth configuration"))?;
            store = store.with_google_oauth(google);
        }
        store
    };
    if let Some(path) = setting_os("LMM_CORE_API_KEY_ENCRYPTION_KEY_FILE") {
        store = store
            .with_api_key_encryption_key(&private_file(Path::new(&path), 32)?)
            .map_err(|_| invalid_config("invalid API key encryption key"))?;
    }
    Ok(store)
}

pub fn ingress() -> io::Result<Option<crate::lifecycle::ingress::IngressAcks>> {
    match (
        setting("LMM_CORE_INGRESS_ACK_URL"),
        setting_os("LMM_CORE_INGRESS_TOKEN_FILE"),
        setting("LMM_CORE_INSTANCE"),
    ) {
        (Err(env::VarError::NotPresent), None, Err(env::VarError::NotPresent)) => Ok(None),
        (Ok(url), Some(path), Ok(instance)) => {
            let bytes = private_file(Path::new(&path), 4096)?;
            let token = std::str::from_utf8(&bytes)
                .map_err(|_| invalid_config("invalid ingress credential"))?
                .trim()
                .as_bytes();
            crate::lifecycle::ingress::IngressAcks::new(&url, token, &instance)
                .map(Some)
                .map_err(|_| invalid_config("invalid ingress configuration"))
        }
        _ => Err(invalid_config(
            "configure ingress endpoint, credential and instance together",
        )),
    }
}

pub fn identity_database_required() -> io::Result<bool> {
    Ok(bool_setting("LMM_CORE_REGISTRATION_ENABLED")?
        || setting_os("LMM_CORE_API_KEY_ENCRYPTION_KEY_FILE").is_some()
        || setting_os("LMM_CORE_GOOGLE_CLIENT_ID").is_some()
        || setting_os("LMM_CORE_GOOGLE_CLIENT_SECRET").is_some()
        || setting_os("LMM_CORE_GOOGLE_CLIENT_SECRET_FILE").is_some()
        || setting_os("LMM_CORE_GOOGLE_REDIRECT_URI").is_some())
}

/// A bounded core pool. RPC identity and event delivery use separate pools.
pub async fn database_pool(url: &str) -> io::Result<sqlx::PgPool> {
    bounded_database_pool(url, database_connections()?).await
}

async fn bounded_database_pool(url: &str, maximum: u32) -> io::Result<sqlx::PgPool> {
    sqlx::postgres::PgPoolOptions::new()
        .max_connections(maximum)
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
    match setting(name).as_deref() {
        Ok("true" | "1") => Ok(true),
        Ok("false" | "0") | Err(env::VarError::NotPresent) => Ok(false),
        _ => Err(invalid_config("invalid boolean core setting")),
    }
}

pub fn console_enabled() -> io::Result<bool> {
    bool_setting("LMM_CORE_CONSOLE_ENABLED")
}

pub fn payments_configured() -> bool {
    setting_os("LMM_CORE_PAYMENTS_CONFIG_FILE").is_some()
        || setting_os("LMM_CORE_PAYMENTS_TOKEN_FILE").is_some()
}

pub fn commerce_configured() -> bool {
    setting_os("LMM_CORE_COMMERCE_CONFIG_FILE").is_some()
        || setting_os("LMM_CORE_COMMERCE_TOKEN_FILE").is_some()
}

#[derive(Deserialize, Default)]
#[serde(default, deny_unknown_fields)]
struct PaymentPermissions {
    prepare: bool,
    credit_evidence: bool,
    refund_user: bool,
    refund_evidence: bool,
    read_user: bool,
    reconcile: bool,
    proof_ingress: bool,
    proof_read: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct TestPaymentChannel {
    id: i64,
    merchant: String,
    webhook_secret_file: PathBuf,
    max_usd_cents: i64,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct PaymentConfig {
    version: u32,
    // Only the explicitly named synthetic/test provider is supported here.
    provider: String,
    channels: Vec<TestPaymentChannel>,
    permissions: PaymentPermissions,
}

#[derive(Deserialize, Default)]
#[serde(default, deny_unknown_fields)]
struct CommerceConfig {
    version: u32,
    pay: bool,
    settle: bool,
    refund: bool,
    read: bool,
    complete_delivery: bool,
    reconcile: bool,
}

#[cfg(unix)]
fn rpc_configuration(
    config_name: &'static str,
    token_name: &'static str,
) -> io::Result<Option<(Vec<u8>, Vec<u8>)>> {
    match (setting_os(config_name), setting_os(token_name)) {
        (None, None) => Ok(None),
        (Some(config), Some(token)) => Ok(Some((
            configuration_file(Path::new(&config), 64 * 1024)?,
            service_token(Path::new(&token))?,
        ))),
        _ => Err(invalid_config(
            "configure both financial RPC document and independent credential file",
        )),
    }
}

#[cfg(unix)]
pub struct FinancialRpcs {
    pub payments: Option<crate::payments::rpc::PaymentsRpc>,
    pub commerce: Option<crate::commerce::rpc::CommerceRpc>,
}

#[cfg(unix)]
fn distinct_financial_credentials(
    control: &[u8],
    payment: Option<&[u8]>,
    commerce: Option<&[u8]>,
) -> io::Result<()> {
    if payment.is_some_and(|token| token == control)
        || commerce.is_some_and(|token| token == control)
        || matches!((payment, commerce), (Some(a), Some(b)) if a == b)
    {
        return Err(invalid_config(
            "financial RPC credentials must be distinct from each other and identity transport",
        ));
    }
    Ok(())
}

/// Each money service has its own credential and bounded writable pool. The
/// generic identity transport credential never gains spending authority.
#[cfg(unix)]
pub async fn financial_rpcs(url: &str, control_token: &[u8]) -> io::Result<FinancialRpcs> {
    let payments = rpc_configuration(
        "LMM_CORE_PAYMENTS_CONFIG_FILE",
        "LMM_CORE_PAYMENTS_TOKEN_FILE",
    )?;
    let commerce = rpc_configuration(
        "LMM_CORE_COMMERCE_CONFIG_FILE",
        "LMM_CORE_COMMERCE_TOKEN_FILE",
    )?;
    distinct_financial_credentials(
        control_token,
        payments.as_ref().map(|(_, token)| token.as_slice()),
        commerce.as_ref().map(|(_, token)| token.as_slice()),
    )?;
    let payments = if let Some((document, token)) = payments {
        let config: PaymentConfig = parse_document(&document)
            .map_err(|_| invalid_config("invalid test payment configuration"))?;
        if config.version != 1
            || config.provider != "stripe-test"
            || config.channels.is_empty()
            || config.channels.len() > 16
        {
            return Err(invalid_config(
                "payments require bounded explicitly configured Stripe test channels",
            ));
        }
        let mut channels = Vec::new();
        for channel in config.channels {
            let secret = String::from_utf8(private_file(&channel.webhook_secret_file, 4096)?)
                .map_err(|_| invalid_config("invalid private payment verification key"))?;
            channels.push(crate::payments::Channel::stripe_test(
                channel.id,
                channel.merchant,
                secret.trim().to_owned(),
                channel.max_usd_cents,
            ));
        }
        let p = config.permissions;
        let permissions = crate::payments::rpc::Permissions {
            prepare: p.prepare,
            credit_evidence: p.credit_evidence,
            refund_user: p.refund_user,
            refund_evidence: p.refund_evidence,
            read_user: p.read_user,
            reconcile: p.reconcile,
            proof_ingress: p.proof_ingress,
            proof_read: p.proof_read,
        };
        let store =
            crate::payments::PaymentsStore::new(bounded_database_pool(url, 2).await?, channels)
                .map_err(|_| invalid_config("invalid test payment channel configuration"))?;
        let rpc = crate::payments::rpc::PaymentsRpc::new(store, &token, permissions)
            .map_err(|_| invalid_config("invalid payment RPC configuration"))?;
        if rpc.features().is_empty() {
            return Err(invalid_config(
                "payment RPC requires explicit service permissions",
            ));
        }
        Some(rpc)
    } else {
        None
    };
    let commerce = if let Some((document, token)) = commerce {
        let config: CommerceConfig = parse_document(&document)
            .map_err(|_| invalid_config("invalid commerce RPC configuration"))?;
        if config.version != 1 {
            return Err(invalid_config("unsupported commerce configuration version"));
        }
        let store = crate::commerce::CommerceStore::connect(url)
            .await
            .map_err(|_| invalid_config("commerce database unavailable"))?;
        let rpc = crate::commerce::rpc::CommerceRpc::new(
            store,
            &token,
            crate::commerce::rpc::Permissions {
                pay: config.pay,
                settle: config.settle,
                refund: config.refund,
                read: config.read,
                complete_delivery: config.complete_delivery,
                reconcile: config.reconcile,
            },
        )
        .map_err(|_| invalid_config("invalid commerce RPC configuration"))?;
        if rpc.features().is_empty() {
            return Err(invalid_config(
                "commerce RPC requires explicit service permissions",
            ));
        }
        Some(rpc)
    } else {
        None
    };
    Ok(FinancialRpcs { payments, commerce })
}

/// Secret/configuration files must be private regular files. Errors deliberately
/// omit paths and contents, which may carry operator-provided credential data.
pub(crate) fn private_file(path: &Path, maximum: usize) -> io::Result<Vec<u8>> {
    read_file(path, maximum, true)
}
fn configuration_file(path: &Path, maximum: usize) -> io::Result<Vec<u8>> {
    read_file(path, maximum, false)
}
fn read_file(path: &Path, maximum: usize, private: bool) -> io::Result<Vec<u8>> {
    absolute_path(path)?;
    let before = fs::symlink_metadata(path)
        .map_err(|_| invalid_config("core configuration file unavailable"))?;
    if !before.is_file() {
        return Err(invalid_config("core configuration must be a regular file"));
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        let mode = before.permissions().mode() & 0o777;
        if (private && !matches!(mode, 0o400 | 0o600)) || (!private && mode & 0o022 != 0) {
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
        let mode = opened.permissions().mode() & 0o777;
        if (private && !matches!(mode, 0o400 | 0o600)) || (!private && mode & 0o022 != 0) {
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
    let origin = setting("LMM_CORE_WEB_ORIGIN");
    let key_file = setting_os("LMM_CORE_WEB_REFRESH_KEY_FILE");
    let local_http = bool_setting("LMM_CORE_WEB_LOCAL_HTTP")?;
    match (origin, key_file) {
        (Err(env::VarError::NotPresent), None) if !local_http => Ok(None),
        (Ok(origin), Some(path)) => {
            let agreement = setting("LMM_CORE_WEB_USER_AGREEMENT_ENABLED");
            let privacy = setting("LMM_CORE_WEB_PRIVACY_POLICY_ENABLED");
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
    version: u32,
    routing: ModelRouting,
    prices: crate::routing::PriceConfig,
    credentials: Vec<ModelCredential>,
    key_groups: Vec<KeyGroup>,
    input_token_ceiling: i64,
    lease_seconds: i64,
    timeout_seconds: u64,
    max_output_tokens: u32,
    #[serde(default)]
    local_streaming: bool,
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
    let config: ModelConfig = parse_document(document)
        .map_err(|_| invalid_config("invalid model configuration document"))?;
    if config.version != 1
        || config.routing.routes.is_empty()
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
    let gateway = crate::model_http::ModelGateway::new(
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
    .map_err(|_| invalid_config("unsafe or incomplete model gateway configuration"))?;
    Ok(if config.local_streaming {
        gateway.with_local_streaming()
    } else {
        gateway
    })
}

pub fn models(pool: sqlx::PgPool) -> io::Result<Option<crate::model_http::ModelGateway>> {
    let local_http = bool_setting("LMM_CORE_MODEL_LOCAL_HTTP")?;
    match setting_os("LMM_CORE_MODEL_CONFIG_FILE") {
        Some(path) => model_gateway(
            pool,
            &configuration_file(Path::new(&path), 16 * 1024 * 1024)?,
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
    let inline = setting_os("LMM_CORE_DATABASE_URL")
        .map(|value| {
            value.into_string().map_err(|_| {
                io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "database URL is not valid UTF-8",
                )
            })
        })
        .transpose()?;
    let file = setting_os("LMM_CORE_DATABASE_URL_FILE").map(PathBuf::from);
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
    match setting(name) {
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
    fn typed_toml_is_versioned_bounded_and_does_not_accept_secret_literals() {
        let minimal = parse_core_file(b"version=1\n").unwrap();
        assert_eq!(minimal["LMM_CORE_LISTEN"], "127.0.0.1:8080");
        assert_eq!(minimal["LMM_CORE_DRAIN_TIMEOUT_SECONDS"], "30");
        assert!(!minimal.contains_key("LMM_CORE_DATABASE_URL"));
        for document in [
            b"".as_slice(), b"version=2\n", b"version=1\nversion=1\n",
            b"version=1\nunknown='do-not-print-this'\n", b"version=1\n[database]\nurl='secret'\n",
            b"version=1\n[server]\nworker_threads=0\n", b"version=1\n[server]\ndrain_timeout_seconds=301\n",
            b"version=1\n[identity.web]\norigin='http://api.lmm.local'\nrefresh_key_file='/missing-key'\n",
            b"version=1\n[identity]\napi_key_encryption_key_file='relative-key'\n",
            b"version=1\n[identity]\nregistration_enabled=true\n",
            b"version=1\n[identity.google]\nclient_id='id'\nclient_secret_file='/missing-key'\nredirect_uri='https://example.test/core/v1/auth/oauth/google/callback'\n",
            b"version=1\n[rpc]\nsocket='/tmp/../core.sock'\ntoken_file='/token'\n",
            b"version=1\n[rpc]\nsocket='/tmp/./core.sock'\ntoken_file='/token'\n",
            b"version=1\n[rpc]\nsocket='/core.sock'\ntoken_file='/token'\nshared_group_id=0\n",
            b"version=1\n\xff",
        ] { let error = parse_core_file(document).unwrap_err().to_string(); assert!(!error.contains("do-not-print-this")); }
    }

    #[test]
    fn typed_toml_maps_all_supported_capabilities_without_environment_mutation() {
        let document = b"version=1\n[server]\nlisten='127.0.0.1:8000'\nworker_threads=2\nmax_inflight=8\ndrain_timeout_seconds=15\n[database]\nurl_file='/nonexistent-lmm-test/database'\nmax_connections=3\n[identity]\nregistration_enabled=true\npassword_slots=2\napi_key_encryption_key_file='/nonexistent-lmm-test/api-key'\n[identity.web]\norigin='https://api.example.test'\nrefresh_key_file='/nonexistent-lmm-test/refresh'\nuser_agreement_enabled=false\nprivacy_policy_enabled=true\n[identity.google]\nclient_id='public-client-id'\nclient_secret_file='/nonexistent-lmm-test/google'\nredirect_uri='https://api.example.test/core/v1/auth/oauth/google/callback'\n[rpc]\nsocket='/nonexistent-lmm-test/core.sock'\ntoken_file='/nonexistent-lmm-test/rpc'\nshared_group_id=65534\n[console]\nenabled=true\n[model]\nconfig_file='/nonexistent-lmm-test/model.toml'\nlocal_http=true\n[payments]\nconfig_file='/nonexistent-lmm-test/payments.toml'\ntoken_file='/nonexistent-lmm-test/payments-token'\n[commerce]\nconfig_file='/nonexistent-lmm-test/commerce.toml'\ntoken_file='/nonexistent-lmm-test/commerce-token'\n[ingress]\nack_url='http://127.0.0.1:3000/__ack'\ntoken_file='/nonexistent-lmm-test/ingress'\ninstance='core-test'\n";
        let parsed = parse_core_file(document).unwrap();
        assert_eq!(parsed.len(), 30);
        for (key, expected) in [
            ("LMM_CORE_WORKER_THREADS", "2"),
            ("LMM_CORE_DRAIN_TIMEOUT_SECONDS", "15"),
            ("LMM_CORE_DB_CONNECTIONS", "3"),
            ("LMM_CORE_PASSWORD_SLOTS", "2"),
            (
                "LMM_CORE_GOOGLE_CLIENT_SECRET_FILE",
                "/nonexistent-lmm-test/google",
            ),
            (
                "LMM_CORE_API_KEY_ENCRYPTION_KEY_FILE",
                "/nonexistent-lmm-test/api-key",
            ),
            ("LMM_CORE_RPC_SHARED_GROUP_ID", "65534"),
            ("LMM_CORE_INSTANCE", "core-test"),
        ] {
            assert_eq!(parsed[key], expected);
        }
        assert!(!parsed.contains_key("LMM_CORE_GOOGLE_CLIENT_SECRET"));
    }

    #[cfg(unix)]
    #[test]
    fn ordinary_toml_allows_readable_templates_but_rejects_writable_and_symlink_paths() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let directory = std::env::temp_dir().join(format!(
            "lmm-toml-path-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        fs::create_dir(&directory).unwrap();
        let path = directory.join("core.toml");
        fs::write(&path, b"version=1\n").unwrap();
        for mode in [0o400, 0o600, 0o640, 0o644] {
            fs::set_permissions(&path, fs::Permissions::from_mode(mode)).unwrap();
            assert!(configuration_file(&path, 256).is_ok());
        }
        fs::set_permissions(&path, fs::Permissions::from_mode(0o664)).unwrap();
        assert!(configuration_file(&path, 256).is_err());
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        let linked = directory.with_extension("link");
        symlink(&directory, &linked).unwrap();
        assert!(configuration_file(&linked.join("core.toml"), 256).is_err());
        fs::remove_file(linked).unwrap();
        fs::remove_dir_all(directory).unwrap();
    }
    #[cfg(unix)]
    #[test]
    fn financial_configuration_cannot_reuse_identity_or_sibling_authority() {
        let control = b"control-credential-only";
        let payment = b"payment-credential-only";
        let commerce = b"commerce-credential-only";
        assert!(distinct_financial_credentials(control, None, None).is_ok());
        assert!(distinct_financial_credentials(control, Some(payment), Some(commerce)).is_ok());
        for (a, b) in [
            (Some(control.as_slice()), None),
            (None, Some(control.as_slice())),
            (Some(payment.as_slice()), Some(payment.as_slice())),
        ] {
            assert!(distinct_financial_credentials(control, a, b).is_err());
        }
        let permissions: PaymentPermissions = parse_document(b"").unwrap();
        assert!(
            !permissions.prepare
                && !permissions.credit_evidence
                && !permissions.refund_user
                && !permissions.refund_evidence
                && !permissions.read_user
                && !permissions.reconcile
                && !permissions.proof_ingress
                && !permissions.proof_read
        );
        assert!(parse_document::<CommerceConfig>(b"version=1\ntransfer=true\n").is_err());
        assert!(parse_document::<PaymentConfig>(b"version=1\nprovider='stripe-test'\nchannels=[]\n[permissions]\nrelease_refund=true\n").is_err());
    }
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
        let empty = b"version=1\ncredentials=[]\nkey_groups=[]\ninput_token_ceiling=1024\nlease_seconds=90\ntimeout_seconds=60\nmax_output_tokens=4096\n[routing]\nversion=1\ngroups=[]\nupstreams=[]\nroutes=[]\naliases=[]\n[prices]\nversion=1\nrules=[]\n";
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
