//! Dedicated one-shot database check. No normal startup or administrative writes.
use lmm_core::{
    config,
    core_schema::{DatabaseCheckError, DatabaseCheckReport},
    runtime_database::RequiredCapabilities,
};
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{
    fs::File,
    io::{Read, Write},
    path::Path,
    time::Duration,
};
#[derive(Clone, Copy)]
struct Rejection {
    code: &'static str,
    exit: i32,
}
impl Rejection {
    fn new(code: &'static str, exit: i32) -> Self {
        Self { code, exit }
    }
}
#[derive(Serialize)]
struct Failure {
    version: u32,
    protocol: &'static str,
    status: &'static str,
    scope: &'static str,
    code: &'static str,
}
#[derive(Serialize)]
struct Execution {
    pid: u32,
    boot_id: String,
    start_ticks: String,
    executable_sha256: String,
}
#[derive(Serialize)]
struct Success {
    #[serde(flatten)]
    report: DatabaseCheckReport,
    execution: Execution,
}
fn emit(value: &impl Serialize) -> bool {
    let Ok(mut bytes) = serde_json::to_vec(value) else {
        return false;
    };
    bytes.push(b'\n');
    std::io::stdout().lock().write_all(&bytes).is_ok()
}
fn reject(error: Rejection) -> i32 {
    if emit(&Failure {
        version: 1,
        protocol: "core.check-db.v1",
        status: "rejected",
        scope: "runtime_configured_capabilities",
        code: error.code,
    }) {
        error.exit
    } else {
        5
    }
}
pub fn invalid_arguments() -> i32 {
    reject(Rejection::new("CHECK_DB_ARGUMENTS_INVALID", 2))
}
fn execution_error() -> Rejection {
    Rejection::new("CHECK_DB_EXECUTION_UNAVAILABLE", 3)
}
fn bounded_file(path: &str, maximum: u64) -> Result<Vec<u8>, Rejection> {
    let mut bytes = Vec::new();
    File::open(path)
        .map_err(|_| execution_error())?
        .take(maximum + 1)
        .read_to_end(&mut bytes)
        .map_err(|_| execution_error())?;
    if bytes.len() as u64 > maximum {
        return Err(execution_error());
    }
    Ok(bytes)
}
fn start_ticks(stat: &[u8], pid: u32) -> Result<String, Rejection> {
    let start = stat
        .iter()
        .position(|v| *v == b' ')
        .ok_or_else(execution_error)?;
    let actual = std::str::from_utf8(&stat[..start])
        .ok()
        .and_then(|v| v.parse::<u32>().ok());
    let end = stat
        .windows(2)
        .rposition(|v| v == b") ")
        .ok_or_else(execution_error)?;
    let tail = std::str::from_utf8(&stat[end + 2..]).map_err(|_| execution_error())?;
    let ticks = tail
        .split_whitespace()
        .nth(19)
        .and_then(|v| v.parse::<u64>().ok())
        .filter(|v| *v > 0);
    if actual != Some(pid) {
        return Err(execution_error());
    }
    ticks.map(|v| v.to_string()).ok_or_else(execution_error)
}
fn boot_id(bytes: &[u8]) -> Result<String, Rejection> {
    let bytes = bytes.strip_suffix(b"\n").unwrap_or(bytes);
    if bytes.len() != 36
        || !bytes.iter().enumerate().all(|(i, b)| {
            if [8, 13, 18, 23].contains(&i) {
                *b == b'-'
            } else {
                b.is_ascii_digit() || (b'a'..=b'f').contains(b)
            }
        })
    {
        return Err(execution_error());
    }
    String::from_utf8(bytes.to_vec()).map_err(|_| execution_error())
}
fn execution() -> Result<Execution, Rejection> {
    let pid = std::process::id();
    let ticks = start_ticks(&bounded_file("/proc/self/stat", 8192)?, pid)?;
    let boot = boot_id(&bounded_file("/proc/sys/kernel/random/boot_id", 128)?)?;
    let mut file = File::open("/proc/self/exe").map_err(|_| execution_error())?;
    let metadata = file.metadata().map_err(|_| execution_error())?;
    if !metadata.is_file() || metadata.len() == 0 || metadata.len() > 1024 * 1024 * 1024 {
        return Err(execution_error());
    }
    let mut hash = Sha256::new();
    let mut buffer = [0u8; 64 * 1024];
    let mut count = 0u64;
    loop {
        let read = file.read(&mut buffer).map_err(|_| execution_error())?;
        if read == 0 {
            break;
        }
        count = count.checked_add(read as u64).ok_or_else(execution_error)?;
        if count > metadata.len() {
            return Err(execution_error());
        }
        hash.update(&buffer[..read]);
    }
    if count != metadata.len() {
        return Err(execution_error());
    }
    Ok(Execution {
        pid,
        boot_id: boot,
        start_ticks: ticks,
        executable_sha256: format!("{:x}", hash.finalize()),
    })
}
fn required() -> Result<RequiredCapabilities, Rejection> {
    Ok(RequiredCapabilities {
        models: config::setting_os("LMM_CORE_MODEL_CONFIG_FILE").is_some(),
        events: config::setting_os("LMM_CORE_RPC_SOCKET").is_some(),
        payments: config::payments_configured(),
        commerce: config::commerce_configured(),
        console: config::console_enabled()
            .map_err(|_| Rejection::new("CHECK_DB_CONFIG_INVALID", 2))?,
        web_identity: config::setting_os("LMM_CORE_WEB_ORIGIN").is_some()
            || config::setting_os("LMM_CORE_WEB_REFRESH_KEY_FILE").is_some(),
        oauth2: config::oauth2_configured(),
    })
}
fn checked(error: DatabaseCheckError) -> Rejection {
    match error {
        DatabaseCheckError::SchemaUnverified => Rejection::new("CHECK_DB_SCHEMA_UNVERIFIED", 3),
        DatabaseCheckError::AuthorityUnverified => {
            Rejection::new("CHECK_DB_AUTHORITY_UNVERIFIED", 3)
        }
        DatabaseCheckError::RuntimeUnsafe => Rejection::new("CHECK_DB_RUNTIME_UNSAFE", 4),
        DatabaseCheckError::RuntimeIncomplete => Rejection::new("CHECK_DB_RUNTIME_INCOMPLETE", 4),
        DatabaseCheckError::ObservationUnavailable => {
            Rejection::new("CHECK_DB_OBSERVATION_UNAVAILABLE", 3)
        }
    }
}
async fn observe(
    url: &str,
    required: RequiredCapabilities,
) -> Result<DatabaseCheckReport, Rejection> {
    let pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(1)
        .min_connections(0)
        .acquire_timeout(Duration::from_secs(3))
        .after_connect(|connection, _| {
            Box::pin(async move {
                sqlx::query("SET default_transaction_read_only=on")
                    .execute(&mut *connection)
                    .await?;
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
        .map_err(|_| Rejection::new("CHECK_DB_DATABASE_UNAVAILABLE", 3))?;
    let result = match tokio::time::timeout(
        Duration::from_secs(15),
        lmm_core::core_schema::check_database(&pool, required),
    )
    .await
    {
        Ok(result) => result.map_err(checked),
        Err(_) => Err(Rejection::new("CHECK_DB_OBSERVATION_UNAVAILABLE", 3)),
    };
    pool.close().await;
    result
}
pub fn run(path: Option<&Path>, legacy_env: bool) -> i32 {
    let result = (|| {
        if legacy_env {
            return Err(Rejection::new("CHECK_DB_LEGACY_ENV_FORBIDDEN", 2));
        }
        let path = path
            .filter(|p| p.is_absolute())
            .ok_or(Rejection::new("CHECK_DB_CONFIG_REQUIRED", 2))?;
        config::initialize(Some(path), false)
            .map_err(|_| Rejection::new("CHECK_DB_CONFIG_INVALID", 2))?;
        let url = config::database_url()
            .map_err(|_| Rejection::new("CHECK_DB_CONFIG_INVALID", 2))?
            .ok_or(Rejection::new("CHECK_DB_CONFIG_REQUIRED", 2))?;
        let required = required()?;
        let execution = execution()?;
        let runtime = tokio::runtime::Builder::new_current_thread()
            .max_blocking_threads(1)
            .enable_all()
            .build()
            .map_err(|_| execution_error())?;
        let report = runtime.block_on(observe(&url, required))?;
        Ok(Success { report, execution })
    })();
    match result {
        Ok(success) => {
            if emit(&success) {
                0
            } else {
                5
            }
        }
        Err(error) => reject(error),
    }
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn stat_binding_handles_spaces_and_parentheses_without_echoing_comm() {
        let mut tail = ["R"; 20];
        tail[19] = "123456";
        let stat = format!("7 (a) b) {}", tail.join(" "));
        // Use only the kernel field grammar; the comm itself is never projected.
        let stat = stat.as_bytes();
        assert_eq!(start_ticks(stat, 7).ok(), Some("123456".into()));
        assert!(start_ticks(stat, 8).is_err());
        assert!(start_ticks(b"7 bad", 7).is_err());
    }
    #[test]
    fn boot_identity_rejects_noncanonical_or_extra_bytes() {
        assert!(boot_id(b"00112233-4455-6677-8899-aabbccddeeff\n").is_ok());
        for v in [
            b"00112233-4455-6677-8899-AABBCCDDEEFF".as_slice(),
            b"00112233-4455-6677-8899-aabbccddeeff\nextra",
            b"invalid",
        ] {
            assert!(boot_id(v).is_err());
        }
    }
}
