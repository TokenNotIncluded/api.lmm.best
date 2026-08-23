//! Observability and maintenance route candidates.
//!
//! The normal listener composes only the PostgreSQL/Valkey-backed read subset;
//! the remaining candidates stay behind migration/test boundaries until their
//! frozen Go behavior has been independently verified and approved.

use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    path::{Path, PathBuf},
    sync::{
        Arc,
        atomic::{AtomicI64, Ordering},
    },
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{RawQuery, State},
    http::{HeaderMap, HeaderValue, StatusCode},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use chrono::{SecondsFormat, Utc};
use secrecy::SecretString;
use serde::Serialize;
use serde_json::{Map, Value, json};
use sqlx::{PgPool, Row};
use thiserror::Error;

use crate::auth::DashboardAuth;

const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const MAX_SELF_RANGE_SECONDS: i64 = 30 * 24 * 60 * 60;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
// Go's log queries apply each timestamp predicate only when that query value
// is non-zero.  Keeping this separate from the bounded data/stat queries
// prevents a no-parameter log read from becoming `created_at <= 0`.
#[cfg(test)]
const OPTIONAL_LOG_TIME_RANGE: &str =
    "($2 = 0 OR created_at >= $2) AND ($3 = 0 OR created_at <= $3)";
#[cfg(test)]
const MAX_RECENT_LOGS: i64 = 1000;
#[cfg(test)]
const LOG_JSON: &str = r#"jsonb_strip_nulls(jsonb_build_object('id', id, 'user_id', COALESCE(user_id, 0), 'created_at', COALESCE(created_at, 0), 'type', COALESCE(type, 0), 'content', COALESCE(content, ''), 'username', COALESCE(username, ''), 'token_name', COALESCE(token_name, ''), 'model_name', COALESCE(model_name, ''), 'quota', COALESCE(quota, 0), 'prompt_tokens', COALESCE(prompt_tokens, 0), 'completion_tokens', COALESCE(completion_tokens, 0), 'use_time', COALESCE(use_time, 0), 'is_stream', COALESCE(is_stream, false), 'channel', COALESCE(channel_id, 0), 'channel_name', COALESCE(channel_name, ''), 'token_id', COALESCE(token_id, 0), 'group', COALESCE("group", ''), 'ip', COALESCE(ip, ''), 'request_id', NULLIF(COALESCE(request_id, ''), ''), 'upstream_request_id', NULLIF(COALESCE(upstream_request_id, ''), ''), 'other', COALESCE(other, '')))"#;

#[cfg(test)]
fn log_query(operation: ObservabilityOperation) -> String {
    match operation {
        ObservabilityOperation::SelfLogs => format!(
            "SELECT {LOG_JSON} FROM logs WHERE user_id = $1 AND {OPTIONAL_LOG_TIME_RANGE} ORDER BY id DESC LIMIT {MAX_RECENT_LOGS}"
        ),
        // Go's token-log handler ignores the optional query string and returns
        // the most recent 1,000 rows in the legacy success/data envelope.
        ObservabilityOperation::LogsByToken => format!(
            "SELECT {LOG_JSON} FROM logs WHERE token_id = $1 ORDER BY id DESC LIMIT {MAX_RECENT_LOGS}"
        ),
        _ => format!(
            "SELECT {LOG_JSON} FROM logs WHERE ($1 = 0 OR created_at >= $1) AND ($2 = 0 OR created_at <= $2) ORDER BY id DESC LIMIT {MAX_RECENT_LOGS}"
        ),
    }
}

/// The legacy authentication boundary required by a route family.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ObservabilityAccess {
    /// A dashboard administrator is required.
    Admin,
    /// The root dashboard administrator is required.
    Root,
    /// Any authenticated dashboard user is required.
    User,
    /// A read-only API token is required.
    Token,
    /// The legacy pricing-navigation gate permits public visitors or users.
    PublicOrUser,
}

/// A principal resolved by application-owned authentication middleware.
///
/// Route handlers never infer this value from role-like request headers.  A
/// production adapter must resolve it from the server-side session or API-token
/// authority before these candidate routes are composed into a listener.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ObservabilityPrincipal {
    /// An unauthenticated visitor accepted by the public pricing gate.
    Public,
    /// A validated dashboard user.
    User {
        /// Stable user identifier used for self-scoped reads.
        user_id: i64,
        /// Server-side username used by the legacy self-stat query.
        username: String,
        /// Server-side dashboard role.
        role: i64,
    },
    /// A validated read-only API token.
    Token {
        /// Stable token identifier used for token-scoped log reads.
        token_id: i64,
    },
}

/// Authentication is injected at the application boundary so the route slice
/// remains independent of a particular session or token implementation.
#[async_trait]
pub trait ObservabilityAuthorizer: Send + Sync {
    /// Resolves a principal for the requested legacy access boundary.
    async fn authorize(
        &self,
        headers: &HeaderMap,
        access: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError>;
}

/// Validates the legacy read-only API-token boundary used only by
/// `GET /api/log/token`.
///
/// This is intentionally separate from [`DashboardAuth`].  Dashboard session
/// credentials and relay API tokens have different formats and authorities;
/// treating either one as the other would let a dashboard bearer token leak
/// into an API-token-only route (or vice versa).
#[async_trait]
pub trait ObservabilityTokenAuthorizer: Send + Sync {
    /// Resolves a legacy API token after its wire-format normalization.
    async fn authorize_read_only(
        &self,
        presented: &str,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError>;
}

/// PostgreSQL authority for the legacy read-only API-token middleware.
///
/// It deliberately preserves the Go `TokenAuthReadOnly` policy: an expired or
/// depleted token may inspect its own logs, but disabled tokens and tokens
/// whose owner is disabled may not.  It does not mutate token access counters,
/// Valkey, or any request-owned state.
#[derive(Clone)]
pub struct PgReadOnlyObservabilityTokenAuthorizer {
    pg: PgPool,
}

impl PgReadOnlyObservabilityTokenAuthorizer {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl ObservabilityTokenAuthorizer for PgReadOnlyObservabilityTokenAuthorizer {
    async fn authorize_read_only(
        &self,
        presented: &str,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        let key =
            legacy_read_only_token_key(presented).ok_or(ObservabilityAuthError::Unauthorized)?;
        let token_id = sqlx::query_scalar::<_, i64>(
            "SELECT t.id FROM tokens t JOIN users u ON u.id = t.user_id \
             WHERE t.key = $1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL \
             AND t.status <> 2 AND u.status = 1",
        )
        .bind(key)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| ObservabilityAuthError::Unauthorized)?
        .ok_or(ObservabilityAuthError::Unauthorized)?;
        if token_id <= 0 {
            return Err(ObservabilityAuthError::Unauthorized);
        }
        Ok(ObservabilityPrincipal::Token { token_id })
    }
}

/// Concrete dashboard/session and API-token authorizer for this route family.
///
/// The runtime owns both authorities.  No role, user id, or token id is ever
/// read from client-controlled request headers other than the credential
/// itself; claims are resolved by the injected server-side implementations.
#[derive(Clone)]
pub struct DashboardObservabilityAuthorizer {
    dashboard_auth: Arc<dyn DashboardAuth>,
    token_auth: Arc<dyn ObservabilityTokenAuthorizer>,
}

impl DashboardObservabilityAuthorizer {
    #[must_use]
    pub fn new(
        dashboard_auth: Arc<dyn DashboardAuth>,
        token_auth: Arc<dyn ObservabilityTokenAuthorizer>,
    ) -> Self {
        Self {
            dashboard_auth,
            token_auth,
        }
    }

    async fn dashboard_principal(
        &self,
        headers: &HeaderMap,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        let credential =
            authorization_credential(headers).ok_or(ObservabilityAuthError::Unauthorized)?;
        let user = self
            .dashboard_auth
            .self_user(SecretString::from(credential))
            .await
            .map_err(|_| ObservabilityAuthError::Unauthorized)?;
        if user.id <= 0 {
            return Err(ObservabilityAuthError::Unauthorized);
        }
        Ok(ObservabilityPrincipal::User {
            user_id: user.id,
            username: user.username,
            role: user.role,
        })
    }
}

#[async_trait]
impl ObservabilityAuthorizer for DashboardObservabilityAuthorizer {
    async fn authorize(
        &self,
        headers: &HeaderMap,
        access: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        match access {
            ObservabilityAccess::Token => {
                let credential = authorization_credential(headers)
                    .ok_or(ObservabilityAuthError::Unauthorized)?;
                self.token_auth.authorize_read_only(&credential).await
            }
            ObservabilityAccess::PublicOrUser if authorization_credential(headers).is_none() => {
                Ok(ObservabilityPrincipal::Public)
            }
            ObservabilityAccess::Admin
            | ObservabilityAccess::Root
            | ObservabilityAccess::User
            | ObservabilityAccess::PublicOrUser => self.dashboard_principal(headers).await,
        }
    }
}

fn authorization_credential(headers: &HeaderMap) -> Option<String> {
    let value = headers
        .get(axum::http::header::AUTHORIZATION)?
        .to_str()
        .ok()?;
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let credential = match (
        first.eq_ignore_ascii_case("bearer"),
        fields.next(),
        fields.next(),
    ) {
        (true, Some(value), None) => value,
        (false, None, None) => first,
        _ => return None,
    };
    (!credential.is_empty()).then(|| credential.to_owned())
}

fn legacy_read_only_token_key(presented: &str) -> Option<&str> {
    let raw = presented.strip_prefix("sk-").unwrap_or(presented);
    let key = raw.split('-').next()?;
    (!key.is_empty()).then_some(key)
}

/// Authentication failures presented by the frozen route boundary.
#[derive(Clone, Copy, Debug, Eq, Error, PartialEq)]
pub enum ObservabilityAuthError {
    /// No valid credential was supplied for the requested boundary.
    #[error("Unauthorized")]
    Unauthorized,
}

/// A single storage operation selected by the HTTP compatibility layer.
///
/// Keeping this catalog-shaped boundary avoids twenty-one near-identical
/// trait methods while still making every route testable with a strict fake.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ObservabilityOperation {
    AllQuotaDates,
    QuotaDatesByUser,
    SelfQuotaDates,
    AllFlowQuotaDates,
    SelfFlowQuotaDates,
    AllLogs,
    ChannelAffinityUsageCacheStats,
    SelfLogs,
    SelfLogStats,
    LogStats,
    LogsByToken,
    PerfMetrics,
    PerfMetricsSummary,
    ClearDiskCache,
    ForceGc,
    CleanupLogFiles,
    LogFiles,
    ResetPerformanceStats,
    PerformanceStats,
}

/// Fully authorized request passed to the persistence/metrics boundary.
#[derive(Clone, Debug)]
pub struct ObservabilityCall {
    /// The selected legacy operation.
    pub operation: ObservabilityOperation,
    /// Principal authenticated before storage is invoked.
    pub principal: ObservabilityPrincipal,
    /// Decoded legacy query parameters.
    pub query: BTreeMap<String, String>,
}

/// Backend boundary for the observability route candidates.
#[async_trait]
pub trait ObservabilityStore: Send + Sync {
    /// Executes one already-authorized observation or maintenance operation.
    async fn execute(&self, call: ObservabilityCall) -> Result<Value, ObservabilityStoreError>;
}

/// Runtime metrics boundary for process-owned metric reads.
///
/// The Go implementation combines persisted buckets with hot in-memory and
/// Valkey counters. That ownership stays outside this process-metrics HTTP slice so
/// the eventual listener can inject the process-wide collector rather than
/// create a second, divergent collector here.
#[async_trait]
pub trait ObservabilityMetrics: Send + Sync {
    /// Reads one frozen process-owned metric operation.
    async fn query(
        &self,
        operation: ObservabilityOperation,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError>;
}

/// Runtime maintenance boundary for process and filesystem owned operations.
#[async_trait]
pub trait ObservabilityMaintenance: Send + Sync {
    /// Performs one authorized maintenance operation.
    async fn execute(
        &self,
        operation: ObservabilityOperation,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError>;
}

/// Explicit fail-closed metrics adapter for a process without the legacy
/// collector wired in yet.
///
/// Returning synthetic empty groups or zero counters would look like a healthy
/// metrics result to the dashboard.  This adapter instead takes the frozen 500
/// error path and has no Valkey or process side effects, which makes it safe as
/// the default dependency for an isolated test instance.
#[derive(Clone, Copy, Debug, Default)]
pub struct UnavailableObservabilityMetrics;

#[async_trait]
impl ObservabilityMetrics for UnavailableObservabilityMetrics {
    async fn query(
        &self,
        _: ObservabilityOperation,
        _: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        Err(ObservabilityStoreError::Unavailable)
    }
}

/// Explicit fail-closed maintenance adapter for a process without an
/// application-owned maintenance service.
///
/// It intentionally does not call `GC`, scan disks, list log files, or reset
/// counters.  Mounting the route with this adapter is therefore safe for a
/// test instance while preserving the legacy dependency-failure envelope.
#[derive(Clone, Copy, Debug, Default)]
pub struct UnavailableObservabilityMaintenance;

#[async_trait]
impl ObservabilityMaintenance for UnavailableObservabilityMaintenance {
    async fn execute(
        &self,
        _: ObservabilityOperation,
        _: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        Err(ObservabilityStoreError::Unavailable)
    }
}

/// PostgreSQL-configured disk-cache maintenance for the one filesystem
/// operation whose Rust listener can prove the legacy side effects.
///
/// Go recomputes this directory from `performance_setting.disk_cache_path` on
/// every call and appends `new-api-body-cache`; an absent option means the
/// process temporary directory.  The adapter deliberately only removes
/// regular files older than ten minutes, never follows directory entries or
/// symlinks, and treats a missing cache directory as an already-clean state.
/// The other performance maintenance operations remain unavailable until the
/// Rust process owns equivalent log/GC/counter services.
#[derive(Clone)]
pub struct PgDiskCacheMaintenance {
    pg: PgPool,
    stats: Arc<PerformanceStatsState>,
}

#[derive(Default)]
struct PerformanceStatsState {
    disk_cache_hits: AtomicI64,
    memory_cache_hits: AtomicI64,
}

impl PgDiskCacheMaintenance {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self {
            pg,
            stats: Arc::new(PerformanceStatsState::default()),
        }
    }

    async fn cache_dir(&self) -> Result<PathBuf, ObservabilityStoreError> {
        let configured = sqlx::query_scalar::<_, Option<String>>(
            "SELECT value FROM options WHERE key = 'performance_setting.disk_cache_path'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| ObservabilityStoreError::Unavailable)?
        .flatten()
        .unwrap_or_default();
        Ok(disk_cache_dir(&configured))
    }

    async fn performance_options(
        &self,
    ) -> Result<HashMap<String, String>, ObservabilityStoreError> {
        let rows = sqlx::query(
            "SELECT key, value FROM options WHERE key LIKE 'performance_setting.%' OR key = 'performance_setting'",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|_| ObservabilityStoreError::Unavailable)?;
        let mut options = HashMap::new();
        for row in rows {
            let key = row
                .try_get::<String, _>("key")
                .map_err(|_| ObservabilityStoreError::Unavailable)?;
            let value = row
                .try_get::<Option<String>, _>("value")
                .map_err(|_| ObservabilityStoreError::Unavailable)?
                .unwrap_or_default();
            if key == "performance_setting" {
                if let Ok(Value::Object(values)) = serde_json::from_str::<Value>(&value) {
                    for (name, value) in values {
                        options.insert(
                            format!("performance_setting.{name}"),
                            value_to_string(value),
                        );
                    }
                }
            } else {
                options.insert(key, value);
            }
        }
        Ok(options)
    }

    async fn performance_stats(&self) -> Result<Value, ObservabilityStoreError> {
        let options = self.performance_options().await?;
        let configured_path = options
            .get("performance_setting.disk_cache_path")
            .map_or("", String::as_str);
        let cache_dir = disk_cache_dir(configured_path);
        let cache_info = disk_cache_info(&cache_dir);
        let disk_space = disk_space_info(cache_dir.parent().unwrap_or(&cache_dir));
        let cache_config = json!({
            "disk_cache_enabled": option_bool(&options, "performance_setting.disk_cache_enabled", false),
            "disk_cache_threshold_mb": option_i64(&options, "performance_setting.disk_cache_threshold_mb", 10),
            "disk_cache_max_size_mb": option_i64(&options, "performance_setting.disk_cache_max_size_mb", 1024),
            "disk_cache_path": configured_path.trim(),
            "is_running_in_container": running_in_container(),
            "monitor_enabled": option_bool(&options, "performance_setting.monitor_enabled", true),
            "monitor_cpu_threshold": option_i64(&options, "performance_setting.monitor_cpu_threshold", 90),
            "monitor_memory_threshold": option_i64(&options, "performance_setting.monitor_memory_threshold", 90),
            "monitor_disk_threshold": option_i64(&options, "performance_setting.monitor_disk_threshold", 95),
        });
        let max_bytes = option_i64(&options, "performance_setting.disk_cache_max_size_mb", 1024)
            .max(0)
            .saturating_mul(1024 * 1024);
        let threshold_bytes =
            option_i64(&options, "performance_setting.disk_cache_threshold_mb", 10)
                .max(0)
                .saturating_mul(1024 * 1024);
        Ok(json!({
            "cache_stats": {
                "active_disk_files": cache_info.file_count,
                "current_disk_usage_bytes": cache_info.total_size,
                "active_memory_buffers": 0,
                "current_memory_usage_bytes": 0,
                "disk_cache_hits": self.stats.disk_cache_hits.load(Ordering::Relaxed),
                "memory_cache_hits": self.stats.memory_cache_hits.load(Ordering::Relaxed),
                "disk_cache_max_bytes": max_bytes,
                "disk_cache_threshold_bytes": threshold_bytes,
            },
            "memory_stats": process_memory_stats(),
            "disk_cache_info": {
                "path": cache_dir,
                "exists": cache_info.exists,
                "file_count": cache_info.file_count,
                "total_size": cache_info.total_size,
            },
            "disk_space_info": disk_space,
            "config": cache_config,
        }))
    }
}

#[async_trait]
impl ObservabilityMaintenance for PgDiskCacheMaintenance {
    async fn execute(
        &self,
        operation: ObservabilityOperation,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        match operation {
            ObservabilityOperation::ClearDiskCache => {
                let directory = self.cache_dir().await?;
                cleanup_disk_cache(&directory).map_err(|_| ObservabilityStoreError::Unavailable)?;
                Ok(Value::Null)
            }
            ObservabilityOperation::LogFiles => read_log_files(),
            ObservabilityOperation::CleanupLogFiles => perform_log_cleanup(query, &self.stats),
            ObservabilityOperation::ResetPerformanceStats => {
                self.stats.disk_cache_hits.store(0, Ordering::Relaxed);
                self.stats.memory_cache_hits.store(0, Ordering::Relaxed);
                Ok(Value::Null)
            }
            ObservabilityOperation::PerformanceStats => self.performance_stats().await,
            _ => Err(ObservabilityStoreError::Unavailable),
        }
    }
}

const DISK_CACHE_DIRECTORY: &str = "new-api-body-cache";

fn disk_cache_dir(configured: &str) -> PathBuf {
    let configured = configured.trim();
    let configured = configured
        .strip_prefix('"')
        .and_then(|value| value.strip_suffix('"'))
        .unwrap_or(configured)
        .trim();
    let base = if configured.is_empty() {
        std::env::temp_dir()
    } else {
        PathBuf::from(configured)
    };
    base.join(DISK_CACHE_DIRECTORY)
}

fn value_to_string(value: Value) -> String {
    match value {
        Value::String(value) => value,
        Value::Bool(value) => value.to_string(),
        Value::Number(value) => value.to_string(),
        Value::Null => String::new(),
        value => value.to_string(),
    }
}

fn option_bool(options: &HashMap<String, String>, key: &str, default: bool) -> bool {
    options
        .get(key)
        .and_then(|value| match value.trim() {
            "1" | "true" | "TRUE" | "True" | "t" | "T" => Some(true),
            "0" | "false" | "FALSE" | "False" | "f" | "F" => Some(false),
            _ => None,
        })
        .unwrap_or(default)
}

fn option_i64(options: &HashMap<String, String>, key: &str, default: i64) -> i64 {
    options
        .get(key)
        .and_then(|value| value.trim().parse::<i64>().ok())
        .unwrap_or(default)
}

#[derive(Clone, Copy, Debug, Default)]
struct DiskCacheInfoSnapshot {
    exists: bool,
    file_count: i64,
    total_size: i64,
}

fn disk_cache_info(directory: &Path) -> DiskCacheInfoSnapshot {
    let Ok(entries) = std::fs::read_dir(directory) else {
        return DiskCacheInfoSnapshot::default();
    };
    let mut info = DiskCacheInfoSnapshot {
        exists: true,
        ..DiskCacheInfoSnapshot::default()
    };
    for entry in entries.flatten() {
        let Ok(file_type) = entry.file_type() else {
            continue;
        };
        if !file_type.is_file() {
            continue;
        }
        info.file_count = info.file_count.saturating_add(1);
        if let Ok(size) = entry.metadata().map(|metadata| metadata.len()) {
            info.total_size = info
                .total_size
                .saturating_add(i64::try_from(size).unwrap_or(i64::MAX));
        }
    }
    info
}

fn disk_space_info(path: &Path) -> Value {
    #[cfg(unix)]
    {
        if let Ok(stat) = rustix::fs::statvfs(path) {
            let total = stat.f_blocks.saturating_mul(stat.f_bsize);
            let free = stat.f_bavail.saturating_mul(stat.f_bsize);
            let used = total.saturating_sub(stat.f_bfree.saturating_mul(stat.f_bsize));
            let used_percent = if total == 0 {
                0.0
            } else {
                used as f64 / total as f64 * 100.0
            };
            return json!({
                "total": total,
                "free": free,
                "used": used,
                "used_percent": used_percent,
            });
        }
    }
    json!({"total": 0, "free": 0, "used": 0, "used_percent": 0.0})
}

fn process_memory_stats() -> Value {
    #[cfg(target_os = "linux")]
    {
        let resident = std::fs::read_to_string("/proc/self/status")
            .ok()
            .and_then(|raw| {
                raw.lines()
                    .find(|line| line.starts_with("VmRSS:"))
                    .and_then(|line| line.split_whitespace().nth(1))
                    .and_then(|value| value.parse::<u64>().ok())
                    .map(|kilobytes| kilobytes.saturating_mul(1024))
            })
            .unwrap_or_default();
        let goroutines = std::fs::read_dir("/proc/self/task")
            .map(|entries| entries.filter_map(Result::ok).count())
            .unwrap_or_default();
        json!({
            "alloc": resident,
            "total_alloc": resident,
            "sys": resident,
            "num_gc": 0,
            "num_goroutine": goroutines,
        })
    }
    #[cfg(not(target_os = "linux"))]
    {
        json!({
            "alloc": 0,
            "total_alloc": 0,
            "sys": 0,
            "num_gc": 0,
            "num_goroutine": 0,
        })
    }
}

fn running_in_container() -> bool {
    if Path::new("/.dockerenv").exists() {
        return true;
    }
    std::fs::read_to_string("/proc/1/cgroup")
        .map(|value| {
            value.contains("docker") || value.contains("kubepods") || value.contains("containerd")
        })
        .unwrap_or(false)
}

fn cleanup_disk_cache(directory: &Path) -> std::io::Result<()> {
    let entries = match std::fs::read_dir(directory) {
        Ok(entries) => entries,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => return Ok(()),
        Err(error) => return Err(error),
    };
    let now = SystemTime::now();
    for entry in entries.flatten() {
        let file_type = match entry.file_type() {
            Ok(file_type) => file_type,
            Err(_) => continue,
        };
        if !file_type.is_file() {
            continue;
        }
        let modified = match entry.metadata().and_then(|metadata| metadata.modified()) {
            Ok(modified) => modified,
            Err(_) => continue,
        };
        if now.duration_since(modified).unwrap_or_default() > Duration::from_secs(10 * 60) {
            let _ = std::fs::remove_file(entry.path());
        }
    }
    Ok(())
}

fn configured_log_directory() -> Option<PathBuf> {
    let configured = std::env::var_os("LMM_LOG_DIR")
        .or_else(|| std::env::var_os("LOG_DIR"))
        .or_else(|| {
            let mut args = std::env::args_os().skip(1);
            while let Some(argument) = args.next() {
                if argument == "--log-dir" {
                    return args.next();
                }
                if let Some(value) = argument
                    .to_str()
                    .and_then(|value| value.strip_prefix("--log-dir="))
                {
                    return Some(value.into());
                }
            }
            None
        })?;
    let configured = configured.to_string_lossy();
    if configured.trim().is_empty() {
        return None;
    }
    let path = PathBuf::from(configured.trim());
    Some(if path.is_absolute() {
        path
    } else {
        std::env::current_dir().ok()?.join(path)
    })
}

fn system_time_rfc3339(value: SystemTime) -> Option<String> {
    let duration = value.duration_since(UNIX_EPOCH).ok()?;
    Some(
        chrono::DateTime::<Utc>::from_timestamp(
            i64::try_from(duration.as_secs()).ok()?,
            duration.subsec_nanos(),
        )?
        .to_rfc3339_opts(SecondsFormat::AutoSi, true),
    )
}

fn read_log_files() -> Result<Value, ObservabilityStoreError> {
    let Some(directory) = configured_log_directory() else {
        return Ok(json!({"enabled": false}));
    };
    let entries =
        std::fs::read_dir(&directory).map_err(|_| ObservabilityStoreError::Unavailable)?;
    let mut files = Vec::new();
    for entry in entries.flatten() {
        let file_type = match entry.file_type() {
            Ok(file_type) => file_type,
            Err(_) => continue,
        };
        if !file_type.is_file() {
            continue;
        }
        let name = entry.file_name().to_string_lossy().into_owned();
        if !name.starts_with("oneapi-") || !name.ends_with(".log") {
            continue;
        }
        let metadata = match entry.metadata() {
            Ok(metadata) => metadata,
            Err(_) => continue,
        };
        let Some(mod_time) = system_time_rfc3339(metadata.modified().unwrap_or(UNIX_EPOCH)) else {
            continue;
        };
        files.push(json!({
            "name": name,
            "size": metadata.len(),
            "mod_time": mod_time,
        }));
    }
    files.sort_by(|left, right| {
        right["name"]
            .as_str()
            .unwrap_or_default()
            .cmp(left["name"].as_str().unwrap_or_default())
    });
    let total_size = files
        .iter()
        .filter_map(|file| file["size"].as_u64())
        .sum::<u64>();
    let oldest_time = files
        .iter()
        .filter_map(|file| file["mod_time"].as_str())
        .min()
        .map(str::to_owned);
    let newest_time = files
        .iter()
        .filter_map(|file| file["mod_time"].as_str())
        .max()
        .map(str::to_owned);
    let mut response = json!({
        "log_dir": directory,
        "enabled": true,
        "file_count": files.len(),
        "total_size": total_size,
        "files": files,
    });
    if let Some(oldest_time) = oldest_time {
        response["oldest_time"] = Value::String(oldest_time);
    }
    if let Some(newest_time) = newest_time {
        response["newest_time"] = Value::String(newest_time);
    }
    Ok(response)
}

#[derive(Clone, Debug)]
struct LogFileEntry {
    name: String,
    size: u64,
    modified: SystemTime,
}

fn configured_log_entries() -> Result<(PathBuf, Vec<LogFileEntry>), ObservabilityStoreError> {
    let directory = configured_log_directory().ok_or_else(|| {
        ObservabilityStoreError::Legacy("log directory not configured".to_owned())
    })?;
    let entries =
        std::fs::read_dir(&directory).map_err(|_| ObservabilityStoreError::Unavailable)?;
    let mut files = Vec::new();
    for entry in entries.flatten() {
        let Ok(file_type) = entry.file_type() else {
            continue;
        };
        if !file_type.is_file() {
            continue;
        }
        let name = entry.file_name().to_string_lossy().into_owned();
        if !name.starts_with("oneapi-") || !name.ends_with(".log") {
            continue;
        }
        let Ok(metadata) = entry.metadata() else {
            continue;
        };
        files.push(LogFileEntry {
            name,
            size: metadata.len(),
            modified: metadata.modified().unwrap_or(UNIX_EPOCH),
        });
    }
    files.sort_by(|left, right| right.name.cmp(&left.name));
    Ok((directory, files))
}

fn perform_log_cleanup(
    query: &BTreeMap<String, String>,
    _: &PerformanceStatsState,
) -> Result<Value, ObservabilityStoreError> {
    let mode = query.get("mode").map_or("", String::as_str);
    let value = query
        .get("value")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .ok_or_else(|| {
            ObservabilityStoreError::Legacy("invalid value, must be a positive integer".to_owned())
        })?;
    if mode != "by_count" && mode != "by_days" {
        return Err(ObservabilityStoreError::Legacy(
            "invalid mode, must be by_count or by_days".to_owned(),
        ));
    }
    let (directory, files) = configured_log_entries()?;
    let active_path = std::env::var_os("LMM_ACTIVE_LOG_PATH").map(PathBuf::from);
    let cutoff = SystemTime::now().checked_sub(Duration::from_secs(
        value.saturating_mul(24 * 60 * 60) as u64,
    ));
    let mut to_delete = Vec::new();
    for (index, file) in files.iter().enumerate() {
        let should_delete = match mode {
            "by_count" => i64::try_from(index).unwrap_or(i64::MAX) >= value,
            "by_days" => cutoff.is_some_and(|cutoff| file.modified < cutoff),
            _ => false,
        };
        if !should_delete {
            continue;
        }
        let path = directory.join(&file.name);
        if active_path.as_ref().is_some_and(|active| *active == path) {
            continue;
        }
        to_delete.push((path, file));
    }

    let mut deleted_count = 0_i64;
    let mut freed_bytes = 0_i64;
    let mut failed_files = Vec::new();
    for (path, file) in to_delete {
        match std::fs::remove_file(path) {
            Ok(()) => {
                deleted_count = deleted_count.saturating_add(1);
                freed_bytes =
                    freed_bytes.saturating_add(i64::try_from(file.size).unwrap_or(i64::MAX));
            }
            Err(_) => failed_files.push(file.name.clone()),
        }
    }
    Ok(json!({
        "deleted_count": deleted_count,
        "freed_bytes": freed_bytes,
        "failed_files": failed_files,
    }))
}

/// PostgreSQL implementation for the dashboard and usage-audit records.
///
/// Construct this with the process-wide metrics and maintenance services for a
/// candidate router. The normal listener uses [`Self::postgres_read_only`] so
/// process metrics and filesystem maintenance remain outside its read-only
/// surface.
#[derive(Clone)]
pub struct PgObservabilityStore {
    pg: PgPool,
    metrics: Option<Arc<dyn ObservabilityMetrics>>,
    maintenance: Option<Arc<dyn ObservabilityMaintenance>>,
}

impl PgObservabilityStore {
    /// Builds the concrete store from PostgreSQL and application-owned runtime
    /// services; no request header is trusted as an identity source.
    #[must_use]
    pub fn new(
        pg: PgPool,
        metrics: Arc<dyn ObservabilityMetrics>,
        maintenance: Arc<dyn ObservabilityMaintenance>,
    ) -> Self {
        Self {
            pg,
            metrics: Some(metrics),
            maintenance: Some(maintenance),
        }
    }

    /// Builds the normal-listener read store. Only the PostgreSQL-backed
    /// usage/log queries and the concrete Valkey affinity-cache reader are
    /// available; process metrics and filesystem maintenance are absent by
    /// construction and therefore cannot be mounted accidentally.
    #[must_use]
    pub fn postgres_read_only(pg: PgPool, metrics: Arc<dyn ObservabilityMetrics>) -> Self {
        Self {
            pg,
            metrics: Some(metrics),
            maintenance: None,
        }
    }
}

#[async_trait]
impl ObservabilityStore for PgObservabilityStore {
    async fn execute(&self, call: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        match call.operation {
            ObservabilityOperation::PerfMetrics
            | ObservabilityOperation::PerfMetricsSummary
            | ObservabilityOperation::ChannelAffinityUsageCacheStats => {
                match self.metrics.as_ref() {
                    Some(metrics) => metrics.query(call.operation, &call.query).await,
                    None => Err(ObservabilityStoreError::Unavailable),
                }
            }
            ObservabilityOperation::ClearDiskCache
            | ObservabilityOperation::ForceGc
            | ObservabilityOperation::CleanupLogFiles
            | ObservabilityOperation::LogFiles
            | ObservabilityOperation::ResetPerformanceStats
            | ObservabilityOperation::PerformanceStats => match self.maintenance.as_ref() {
                Some(maintenance) => maintenance.execute(call.operation, &call.query).await,
                None => Err(ObservabilityStoreError::Unavailable),
            },
            _ => self.query_postgres(call).await,
        }
    }
}

/// Valkey-backed reader for the legacy channel-affinity usage counters.
///
/// The Go route stores this JSON value in the process-wide hybrid cache. This
/// adapter reads that same namespaced key and never mutates the cache.
#[derive(Clone)]
pub struct ValkeyObservabilityMetrics {
    valkey: redis::Client,
}

impl ValkeyObservabilityMetrics {
    const NAMESPACE: &'static str = "new-api:channel_affinity_usage_cache_stats:v1:";

    #[must_use]
    pub fn new(valkey: redis::Client) -> Self {
        Self { valkey }
    }
}

#[async_trait]
impl ObservabilityMetrics for ValkeyObservabilityMetrics {
    async fn query(
        &self,
        operation: ObservabilityOperation,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        if operation != ObservabilityOperation::ChannelAffinityUsageCacheStats {
            return Err(ObservabilityStoreError::Unavailable);
        }
        let rule_name = query.get("rule_name").map_or("", String::as_str).trim();
        let using_group = query.get("using_group").map_or("", String::as_str).trim();
        let key_fp = query.get("key_fp").map_or("", String::as_str).trim();
        let key = format!("{}{rule_name}\n{using_group}\n{key_fp}", Self::NAMESPACE);
        let mut connection = self
            .valkey
            .get_multiplexed_async_connection()
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)?;
        let raw: Option<String> = redis::cmd("GET")
            .arg(key)
            .query_async(&mut connection)
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)?;
        let mut data = match raw {
            Some(raw) => serde_json::from_str::<Value>(&raw)
                .map_err(|_| ObservabilityStoreError::Unavailable)?,
            None => json!({}),
        };
        let Some(object) = data.as_object_mut() else {
            return Err(ObservabilityStoreError::Unavailable);
        };
        object.insert("rule_name".to_owned(), Value::String(rule_name.to_owned()));
        object.insert(
            "using_group".to_owned(),
            Value::String(using_group.to_owned()),
        );
        object.insert("key_fp".to_owned(), Value::String(key_fp.to_owned()));
        Ok(data)
    }
}

const PERF_SERIES_SCHEMA: &str = "dbcd0a3c01b55203";
const DEFAULT_PERF_BUCKET_SECONDS: i64 = 60 * 60;

#[derive(Clone, Copy, Debug, Default)]
struct PerfCounters {
    request_count: i64,
    success_count: i64,
    total_latency_ms: i64,
    ttft_sum_ms: i64,
    ttft_count: i64,
    output_tokens: i64,
    generation_ms: i64,
}

impl PerfCounters {
    fn add_assign(&mut self, other: Self) {
        self.request_count += other.request_count;
        self.success_count += other.success_count;
        self.total_latency_ms += other.total_latency_ms;
        self.ttft_sum_ms += other.ttft_sum_ms;
        self.ttft_count += other.ttft_count;
        self.output_tokens += other.output_tokens;
        self.generation_ms += other.generation_ms;
    }
}

#[derive(Clone, Debug)]
struct PerfRow {
    model_name: String,
    group: String,
    bucket_ts: i64,
    counters: PerfCounters,
}

/// PostgreSQL-backed performance metrics reader for the two public metrics
/// endpoints.  The Go implementation also merges process-local hot buckets;
/// Rust has no second in-memory collector, so this adapter reads the durable
/// buckets and the compatible Valkey active bucket when one is configured.
#[derive(Clone)]
pub struct PostgresObservabilityMetrics {
    pg: PgPool,
    valkey: Option<redis::Client>,
}

impl PostgresObservabilityMetrics {
    /// Creates a reader using only the durable `perf_metrics` table.
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg, valkey: None }
    }

    /// Adds the legacy `perf:<model>:<group>:<bucket>` active-bucket reader.
    #[must_use]
    pub fn with_valkey(mut self, valkey: redis::Client) -> Self {
        self.valkey = Some(valkey);
        self
    }

    async fn query_model(
        &self,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        let model = non_empty_query(query, "model").ok_or(ObservabilityStoreError::Unavailable)?;
        let (start_ts, end_ts) = perf_time_range(query)?;
        let group = non_empty_query(query, "group");
        let rows = self
            .fetch_rows(Some(model), group, start_ts, end_ts)
            .await?;
        let mut merged = BTreeMap::<(String, i64), PerfCounters>::new();
        for row in rows {
            merged
                .entry((row.group, row.bucket_ts))
                .or_default()
                .add_assign(row.counters);
        }

        // Go only consults the Redis active bucket for a group-scoped query;
        // preserve that boundary and ignore cache failures just as Go does.
        if let Some(group) = group {
            self.merge_valkey_active_bucket(&mut merged, model, group, start_ts, end_ts)
                .await;
        }

        let active_groups = self.active_group_keys(false).await?;
        let mut groups = Vec::new();
        let mut current_group: Option<String> = None;
        let mut current_buckets = Vec::<(i64, PerfCounters)>::new();
        for ((group_name, bucket_ts), counters) in merged {
            if current_group.as_deref() != Some(group_name.as_str()) {
                if let Some(previous) = current_group.take() {
                    if active_groups.contains(&previous) {
                        groups.push(perf_group_value(&previous, &current_buckets));
                    }
                }
                current_group = Some(group_name);
                current_buckets.clear();
            }
            current_buckets.push((bucket_ts, counters));
        }
        if let Some(previous) = current_group {
            if active_groups.contains(&previous) {
                groups.push(perf_group_value(&previous, &current_buckets));
            }
        }

        Ok(json!({
            "model_name": model,
            "series_schema": PERF_SERIES_SCHEMA,
            "groups": groups,
        }))
    }

    async fn query_summary(
        &self,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        let (start_ts, end_ts) = perf_time_range(query)?;
        let active_groups = self.active_group_keys(true).await?;
        let rows = self.fetch_rows(None, None, start_ts, end_ts).await?;
        let mut totals = BTreeMap::<String, PerfCounters>::new();
        let mut buckets = BTreeMap::<String, BTreeMap<i64, PerfCounters>>::new();
        for row in rows {
            if !active_groups.contains(&row.group) || row.counters.request_count == 0 {
                continue;
            }
            totals
                .entry(row.model_name.clone())
                .or_default()
                .add_assign(row.counters);
            buckets
                .entry(row.model_name)
                .or_default()
                .entry(row.bucket_ts)
                .or_default()
                .add_assign(row.counters);
        }

        let mut models = totals
            .into_iter()
            .map(|(model_name, counters)| {
                let recent = buckets
                    .get(&model_name)
                    .map_or_else(Vec::new, |model_buckets| {
                        let start = model_buckets.len().saturating_sub(3);
                        model_buckets
                            .iter()
                            .skip(start)
                            .map(|(_, value)| go_number(round_two(success_rate(*value))))
                            .collect::<Vec<_>>()
                    });
                let mut object = Map::from_iter([
                    ("model_name".to_owned(), Value::String(model_name)),
                    (
                        "avg_latency_ms".to_owned(),
                        Value::from(avg(counters.total_latency_ms, counters.request_count)),
                    ),
                    (
                        "success_rate".to_owned(),
                        go_number(round_two(success_rate(counters))),
                    ),
                    (
                        "avg_tps".to_owned(),
                        go_number(round_two(avg_tps(counters))),
                    ),
                ]);
                if !recent.is_empty() {
                    object.insert("recent_success_rates".to_owned(), json!(recent));
                }
                (counters.request_count, Value::Object(object))
            })
            .collect::<Vec<_>>();
        // Go orders by request count descending.  The model-name tie-breaker
        // keeps Rust output deterministic when Go's map iteration ties.
        models.sort_by(|(left_count, left), (right_count, right)| {
            right_count.cmp(left_count).then_with(|| {
                left.get("model_name")
                    .and_then(Value::as_str)
                    .cmp(&right.get("model_name").and_then(Value::as_str))
            })
        });

        Ok(json!({
            "models": models.into_iter().map(|(_, value)| value).collect::<Vec<_>>(),
        }))
    }

    async fn fetch_rows(
        &self,
        model: Option<&str>,
        group: Option<&str>,
        start_ts: i64,
        end_ts: i64,
    ) -> Result<Vec<PerfRow>, ObservabilityStoreError> {
        let rows = match (model, group) {
            (Some(model), Some(group)) => sqlx::query(
                "SELECT model_name, \"group\", bucket_ts, request_count, success_count, total_latency_ms, ttft_sum_ms, ttft_count, output_tokens, generation_ms FROM perf_metrics WHERE model_name = $1 AND bucket_ts >= $2 AND bucket_ts <= $3 AND \"group\" = $4",
            )
            .bind(model)
            .bind(start_ts)
            .bind(end_ts)
            .bind(group)
            .fetch_all(&self.pg)
            .await,
            (Some(model), None) => sqlx::query(
                "SELECT model_name, \"group\", bucket_ts, request_count, success_count, total_latency_ms, ttft_sum_ms, ttft_count, output_tokens, generation_ms FROM perf_metrics WHERE model_name = $1 AND bucket_ts >= $2 AND bucket_ts <= $3",
            )
            .bind(model)
            .bind(start_ts)
            .bind(end_ts)
            .fetch_all(&self.pg)
            .await,
            (None, _) => sqlx::query(
                "SELECT model_name, \"group\", bucket_ts, request_count, success_count, total_latency_ms, ttft_sum_ms, ttft_count, output_tokens, generation_ms FROM perf_metrics WHERE bucket_ts >= $1 AND bucket_ts <= $2",
            )
            .bind(start_ts)
            .bind(end_ts)
            .fetch_all(&self.pg)
            .await,
        }
        .map_err(|_| ObservabilityStoreError::Unavailable)?;

        rows.into_iter()
            .map(|row| {
                let optional_i64 = |name: &str| {
                    row.try_get::<Option<i64>, _>(name)
                        .map(|value| value.unwrap_or_default())
                        .map_err(|_| ObservabilityStoreError::Unavailable)
                };
                Ok(PerfRow {
                    model_name: row
                        .try_get::<Option<String>, _>("model_name")
                        .map_err(|_| ObservabilityStoreError::Unavailable)?
                        .unwrap_or_default(),
                    group: row
                        .try_get::<Option<String>, _>("group")
                        .map_err(|_| ObservabilityStoreError::Unavailable)?
                        .unwrap_or_default(),
                    bucket_ts: optional_i64("bucket_ts")?,
                    counters: PerfCounters {
                        request_count: optional_i64("request_count")?,
                        success_count: optional_i64("success_count")?,
                        total_latency_ms: optional_i64("total_latency_ms")?,
                        ttft_sum_ms: optional_i64("ttft_sum_ms")?,
                        ttft_count: optional_i64("ttft_count")?,
                        output_tokens: optional_i64("output_tokens")?,
                        generation_ms: optional_i64("generation_ms")?,
                    },
                })
            })
            .collect()
    }

    async fn active_group_keys(
        &self,
        include_auto: bool,
    ) -> Result<BTreeSet<String>, ObservabilityStoreError> {
        let raw = sqlx::query_scalar::<_, Option<String>>(
            "SELECT value FROM options WHERE key = 'GroupRatio'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| ObservabilityStoreError::Unavailable)?
        .flatten();
        let mut groups =
            BTreeSet::from(["default".to_owned(), "vip".to_owned(), "svip".to_owned()]);
        if let Some(raw) = raw {
            if let Ok(Value::Object(object)) = serde_json::from_str::<Value>(&raw) {
                groups.clear();
                groups.extend(object.keys().cloned());
            }
        }
        if include_auto {
            groups.insert("auto".to_owned());
        }
        Ok(groups)
    }

    async fn merge_valkey_active_bucket(
        &self,
        merged: &mut BTreeMap<(String, i64), PerfCounters>,
        model: &str,
        group: &str,
        start_ts: i64,
        end_ts: i64,
    ) {
        let Some(valkey) = &self.valkey else {
            return;
        };
        let bucket_seconds = self
            .bucket_seconds()
            .await
            .unwrap_or(DEFAULT_PERF_BUCKET_SECONDS);
        let Ok(now) = unix_seconds() else {
            return;
        };
        let bucket_ts = bucket_start(now, bucket_seconds);
        if bucket_ts < start_ts || bucket_ts > end_ts {
            return;
        }
        let Ok(mut connection) = valkey.get_multiplexed_async_connection().await else {
            return;
        };
        let key = format!("perf:{model}:{group}:{bucket_ts}");
        let fields: Result<HashMap<String, String>, _> = redis::cmd("HGETALL")
            .arg(key)
            .query_async(&mut connection)
            .await;
        let Ok(fields) = fields else {
            return;
        };
        if fields.is_empty() {
            return;
        }
        let counters = PerfCounters {
            request_count: redis_i64(&fields, "req"),
            success_count: redis_i64(&fields, "ok"),
            total_latency_ms: redis_i64(&fields, "lat"),
            ttft_sum_ms: redis_i64(&fields, "ttft"),
            ttft_count: redis_i64(&fields, "ttft_n"),
            output_tokens: redis_i64(&fields, "out"),
            generation_ms: redis_i64(&fields, "gen_ms"),
        };
        merged
            .entry((group.to_owned(), bucket_ts))
            .or_default()
            .add_assign(counters);
    }

    async fn bucket_seconds(&self) -> Result<i64, ObservabilityStoreError> {
        let raw = sqlx::query_scalar::<_, Option<String>>(
            "SELECT value FROM options WHERE key = 'perf_metrics_setting'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| ObservabilityStoreError::Unavailable)?
        .flatten();
        let Some(raw) = raw else {
            return Ok(DEFAULT_PERF_BUCKET_SECONDS);
        };
        let bucket_time = serde_json::from_str::<Value>(&raw).ok().and_then(|value| {
            value
                .get("bucket_time")
                .and_then(Value::as_str)
                .map(str::to_owned)
        });
        Ok(match bucket_time.as_deref() {
            Some("minute") => 60,
            Some("5min") => 300,
            _ => DEFAULT_PERF_BUCKET_SECONDS,
        })
    }
}

#[async_trait]
impl ObservabilityMetrics for PostgresObservabilityMetrics {
    async fn query(
        &self,
        operation: ObservabilityOperation,
        query: &BTreeMap<String, String>,
    ) -> Result<Value, ObservabilityStoreError> {
        match operation {
            ObservabilityOperation::PerfMetrics => self.query_model(query).await,
            ObservabilityOperation::PerfMetricsSummary => self.query_summary(query).await,
            ObservabilityOperation::ChannelAffinityUsageCacheStats => {
                let Some(valkey) = &self.valkey else {
                    return Err(ObservabilityStoreError::Unavailable);
                };
                ValkeyObservabilityMetrics::new(valkey.clone())
                    .query(operation, query)
                    .await
            }
            _ => Err(ObservabilityStoreError::Unavailable),
        }
    }
}

fn non_empty_query<'a>(query: &'a BTreeMap<String, String>, key: &str) -> Option<&'a str> {
    query
        .get(key)
        .map(String::as_str)
        .filter(|value| !value.is_empty())
}

fn perf_time_range(
    query: &BTreeMap<String, String>,
) -> Result<(i64, i64), ObservabilityStoreError> {
    let parsed_hours = query
        .get("hours")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or(24);
    let hours = if parsed_hours <= 0 {
        24
    } else {
        parsed_hours.min(24 * 30)
    };
    let end = unix_seconds()?;
    Ok((end.saturating_sub(hours.saturating_mul(60 * 60)), end))
}

fn unix_seconds() -> Result<i64, ObservabilityStoreError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| ObservabilityStoreError::Unavailable)
        .and_then(|duration| {
            i64::try_from(duration.as_secs()).map_err(|_| ObservabilityStoreError::Unavailable)
        })
}

fn bucket_start(timestamp: i64, bucket_seconds: i64) -> i64 {
    if bucket_seconds <= 0 {
        return timestamp;
    }
    timestamp - timestamp.rem_euclid(bucket_seconds)
}

fn redis_i64(fields: &HashMap<String, String>, key: &str) -> i64 {
    fields
        .get(key)
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or_default()
}

fn avg(sum: i64, count: i64) -> i64 {
    if count <= 0 { 0 } else { sum / count }
}

fn success_rate(counters: PerfCounters) -> f64 {
    if counters.request_count <= 0 {
        0.0
    } else {
        counters.success_count as f64 / counters.request_count as f64 * 100.0
    }
}

fn avg_tps(counters: PerfCounters) -> f64 {
    if counters.output_tokens <= 0 || counters.generation_ms <= 0 {
        0.0
    } else {
        counters.output_tokens as f64 / (counters.generation_ms as f64 / 1000.0)
    }
}

fn round_two(value: f64) -> f64 {
    (value * 100.0).round() / 100.0
}

/// Go's JSON encoder emits an integral `float64` as an integer token (for
/// example `75`, not `75.0`).  Keep the strict-wire differential stable while
/// retaining fractional values as JSON floats.
fn go_number(value: f64) -> Value {
    if value.is_finite()
        && value.fract() == 0.0
        && value >= i64::MIN as f64
        && value <= i64::MAX as f64
    {
        Value::from(value as i64)
    } else {
        Value::from(value)
    }
}

fn perf_group_value(group: &str, buckets: &[(i64, PerfCounters)]) -> Value {
    let mut total = PerfCounters::default();
    let series = buckets
        .iter()
        .map(|(bucket_ts, counters)| {
            total.add_assign(*counters);
            json!({
                "ts": bucket_ts,
                "avg_ttft_ms": avg(counters.ttft_sum_ms, counters.ttft_count),
                "avg_latency_ms": avg(counters.total_latency_ms, counters.request_count),
                "success_rate": go_number(success_rate(*counters)),
                "avg_tps": go_number(avg_tps(*counters)),
            })
        })
        .collect::<Vec<_>>();
    json!({
        "group": group,
        "avg_ttft_ms": avg(total.ttft_sum_ms, total.ttft_count),
        "avg_latency_ms": avg(total.total_latency_ms, total.request_count),
        "success_rate": go_number(success_rate(total)),
        "avg_tps": go_number(avg_tps(total)),
        "series": series,
    })
}

impl PgObservabilityStore {
    async fn query_postgres(
        &self,
        call: ObservabilityCall,
    ) -> Result<Value, ObservabilityStoreError> {
        let start = integer_query(&call.query, "start_timestamp");
        let end = integer_query(&call.query, "end_timestamp");
        match call.operation {
            ObservabilityOperation::AllQuotaDates => {
                let username = call.query.get("username").cloned().unwrap_or_default();
                if username.is_empty() {
                    self.json_rows("SELECT jsonb_build_object('id', 0, 'user_id', 0, 'username', '', 'model_name', model_name, 'created_at', created_at, 'use_group', '', 'token_id', 0, 'channel_id', 0, 'node_name', '', 'count', SUM(count), 'quota', SUM(quota), 'token_used', SUM(token_used)) FROM quota_data WHERE created_at >= $1 AND created_at <= $2 GROUP BY model_name, created_at", start, end).await
                } else {
                    self.json_rows_for_username("SELECT jsonb_build_object('id', 0, 'user_id', user_id, 'username', username, 'model_name', model_name, 'created_at', created_at, 'use_group', '', 'token_id', 0, 'channel_id', 0, 'node_name', '', 'count', SUM(count), 'quota', SUM(quota), 'token_used', SUM(token_used)) FROM quota_data WHERE username = $1 AND created_at >= $2 AND created_at <= $3 GROUP BY user_id, username, model_name, created_at", &username, start, end).await
                }
            }
            ObservabilityOperation::QuotaDatesByUser => self
                .json_rows("SELECT jsonb_build_object('id', 0, 'user_id', 0, 'username', username, 'model_name', '', 'created_at', created_at, 'use_group', '', 'token_id', 0, 'channel_id', 0, 'node_name', '', 'count', SUM(count), 'quota', SUM(quota), 'token_used', SUM(token_used)) FROM quota_data WHERE created_at >= $1 AND created_at <= $2 GROUP BY username, created_at", start, end)
                .await,
            ObservabilityOperation::SelfQuotaDates => {
                let user_id = user_id(&call.principal)?;
                self.json_rows_for_user("SELECT jsonb_build_object('id', 0, 'user_id', user_id, 'username', username, 'model_name', model_name, 'created_at', created_at, 'use_group', '', 'token_id', 0, 'channel_id', 0, 'node_name', '', 'count', SUM(count), 'quota', SUM(quota), 'token_used', SUM(token_used)) FROM quota_data WHERE user_id = $1 AND created_at >= $2 AND created_at <= $3 GROUP BY user_id, username, model_name, created_at", user_id, start, end).await
            }
            ObservabilityOperation::AllFlowQuotaDates | ObservabilityOperation::SelfFlowQuotaDates => {
                let (user_filter, role) = match call.operation {
                    ObservabilityOperation::SelfFlowQuotaDates => {
                        (Some(user_id(&call.principal)?), None)
                    }
                    ObservabilityOperation::AllFlowQuotaDates => match &call.principal {
                        ObservabilityPrincipal::User { role, .. } => (None, Some(*role)),
                        _ => return Err(ObservabilityStoreError::Unavailable),
                    },
                    _ => unreachable!(),
                };
                let username = call.query.get("username").cloned().unwrap_or_default();
                self.flow_rows(start, end, user_filter, role, &username).await
            }
            ObservabilityOperation::AllLogs | ObservabilityOperation::SelfLogs | ObservabilityOperation::LogsByToken => self.logs(call, start, end).await,
            ObservabilityOperation::SelfLogStats | ObservabilityOperation::LogStats => self.stats(call, start, end).await,
            _ => Err(ObservabilityStoreError::Unavailable),
        }
    }

    async fn json_rows(
        &self,
        sql: &str,
        start: i64,
        end: i64,
    ) -> Result<Value, ObservabilityStoreError> {
        let rows = sqlx::query(sql)
            .bind(start)
            .bind(end)
            .fetch_all(&self.pg)
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)?;
        Ok(values(rows))
    }

    async fn json_rows_for_user(
        &self,
        sql: &str,
        user_id: i64,
        start: i64,
        end: i64,
    ) -> Result<Value, ObservabilityStoreError> {
        let rows = sqlx::query(sql)
            .bind(user_id)
            .bind(start)
            .bind(end)
            .fetch_all(&self.pg)
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)?;
        Ok(values(rows))
    }

    async fn json_rows_for_username(
        &self,
        sql: &str,
        username: &str,
        start: i64,
        end: i64,
    ) -> Result<Value, ObservabilityStoreError> {
        let rows = sqlx::query(sql)
            .bind(username)
            .bind(start)
            .bind(end)
            .fetch_all(&self.pg)
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)?;
        Ok(values(rows))
    }

    async fn flow_rows(
        &self,
        start: i64,
        end: i64,
        user_id: Option<i64>,
        role: Option<i64>,
        username: &str,
    ) -> Result<Value, ObservabilityStoreError> {
        let rows = match user_id {
            Some(user_id) => sqlx::query("SELECT jsonb_strip_nulls(jsonb_build_object('token_id', NULLIF(q.token_id, 0), 'token_name', NULLIF(t.name, ''), 'use_group', q.use_group, 'model_name', q.model_name, 'count', SUM(q.count), 'quota', SUM(q.quota), 'token_used', SUM(q.token_used))) FROM quota_data q LEFT JOIN tokens t ON t.id = q.token_id AND t.deleted_at IS NULL WHERE q.user_id = $1 AND q.use_group <> '' AND q.created_at >= $2 AND q.created_at <= $3 GROUP BY q.token_id, t.name, q.use_group, q.model_name ORDER BY SUM(q.quota) DESC").bind(user_id).bind(start).bind(end).fetch_all(&self.pg).await,
            None if role.unwrap_or(0) >= ROOT_ROLE => sqlx::query("SELECT jsonb_strip_nulls(jsonb_build_object('user_id', NULLIF(q.user_id, 0), 'username', NULLIF(q.username, ''), 'node_name', NULLIF(q.node_name, ''), 'token_id', NULLIF(q.token_id, 0), 'token_name', NULLIF(t.name, ''), 'use_group', q.use_group, 'model_name', q.model_name, 'channel_id', NULLIF(q.channel_id, 0), 'channel_name', CASE WHEN q.channel_id > 0 THEN COALESCE(NULLIF(c.name, ''), 'channel-' || q.channel_id::text) END, 'count', SUM(q.count), 'quota', SUM(q.quota), 'token_used', SUM(q.token_used))) FROM quota_data q LEFT JOIN tokens t ON t.id = q.token_id AND t.deleted_at IS NULL LEFT JOIN channels c ON c.id = q.channel_id WHERE q.use_group <> '' AND q.created_at >= $1 AND q.created_at <= $2 AND ($3 = '' OR q.username = $3) GROUP BY q.user_id, q.username, q.node_name, q.token_id, t.name, q.use_group, q.model_name, q.channel_id, c.name ORDER BY SUM(q.quota) DESC").bind(start).bind(end).bind(username).fetch_all(&self.pg).await,
            None => sqlx::query("SELECT jsonb_strip_nulls(jsonb_build_object('user_id', NULLIF(q.user_id, 0), 'username', NULLIF(q.username, ''), 'use_group', q.use_group, 'model_name', q.model_name, 'channel_id', NULLIF(q.channel_id, 0), 'channel_name', CASE WHEN q.channel_id > 0 THEN COALESCE(NULLIF(c.name, ''), 'channel-' || q.channel_id::text) END, 'count', SUM(q.count), 'quota', SUM(q.quota), 'token_used', SUM(q.token_used))) FROM quota_data q LEFT JOIN channels c ON c.id = q.channel_id WHERE q.use_group <> '' AND q.created_at >= $1 AND q.created_at <= $2 AND ($3 = '' OR q.username = $3) GROUP BY q.user_id, q.username, q.use_group, q.model_name, q.channel_id, c.name ORDER BY SUM(q.quota) DESC").bind(start).bind(end).bind(username).fetch_all(&self.pg).await,
        }.map_err(|_| ObservabilityStoreError::Unavailable)?;
        Ok(values(rows))
    }

    async fn logs(
        &self,
        call: ObservabilityCall,
        start: i64,
        end: i64,
    ) -> Result<Value, ObservabilityStoreError> {
        // The legacy wire field is named `channel`, while PostgreSQL stores it
        // as `channel_id`.  Admin reads resolve the display name from the
        // channels table exactly as Go's GetAllLogs does; self/token reads run
        // through formatUserLogs and deliberately expose an empty name.
        const ADMIN_LOG_JSON: &str = "jsonb_build_object('id', l.id, 'user_id', l.user_id, 'created_at', l.created_at, 'type', l.type, 'content', l.content, 'username', l.username, 'token_name', l.token_name, 'model_name', l.model_name, 'quota', l.quota, 'prompt_tokens', l.prompt_tokens, 'completion_tokens', l.completion_tokens, 'use_time', l.use_time, 'is_stream', l.is_stream, 'channel', l.channel_id, 'channel_name', COALESCE(c.name, ''), 'token_id', l.token_id, 'group', l.\"group\", 'ip', l.ip, 'request_id', l.request_id, 'upstream_request_id', l.upstream_request_id, 'other', l.other)";
        const USER_LOG_JSON: &str = "jsonb_build_object('id', l.id, 'user_id', l.user_id, 'created_at', l.created_at, 'type', l.type, 'content', l.content, 'username', l.username, 'token_name', l.token_name, 'model_name', l.model_name, 'quota', l.quota, 'prompt_tokens', l.prompt_tokens, 'completion_tokens', l.completion_tokens, 'use_time', l.use_time, 'is_stream', l.is_stream, 'channel', l.channel_id, 'channel_name', '', 'token_id', l.token_id, 'group', l.\"group\", 'ip', l.ip, 'request_id', l.request_id, 'upstream_request_id', l.upstream_request_id, 'other', l.other)";

        if call.operation == ObservabilityOperation::LogsByToken {
            // GetLogByTokenId intentionally ignores dashboard paging and date
            // filters and returns the most recent MaxRecentItems rows. The Go
            // default is 1000, and the user formatter assigns display ids from
            // one for this endpoint.
            let (where_sql, binds) =
                log_where(call.operation, &call.query, &call.principal, start, end)?;
            let sql = format!(
                "SELECT {USER_LOG_JSON} FROM logs l {where_sql} ORDER BY l.id DESC LIMIT 1000"
            );
            let rows = self.fetch_log_rows(&sql, &binds).await?;
            return Ok(normalize_self_log_items(values(rows), 0));
        }

        let (page, page_size, offset) = page_query(&call.query);
        let (where_sql, binds) =
            log_where(call.operation, &call.query, &call.principal, start, end)?;
        let (select_json, from_sql, order_sql, normalize_self) =
            if call.operation == ObservabilityOperation::SelfLogs {
                (USER_LOG_JSON, "FROM logs l", "ORDER BY l.id DESC", true)
            } else {
                (
                    ADMIN_LOG_JSON,
                    "FROM logs l LEFT JOIN channels c ON c.id = l.channel_id",
                    "ORDER BY l.created_at DESC, l.id DESC",
                    false,
                )
            };
        let rows_sql = format!(
            "SELECT {select_json} {from_sql} {where_sql} {order_sql} LIMIT ${} OFFSET ${}",
            binds.len() + 1,
            binds.len() + 2,
        );
        let mut row_binds = binds.clone();
        row_binds.push(LogBind::I64(page_size));
        row_binds.push(LogBind::I64(offset));
        let rows = self.fetch_log_rows(&rows_sql, &row_binds).await?;

        let count_sql = format!("SELECT COUNT(*) FROM logs l {where_sql}");
        let total = self.fetch_log_count(&count_sql, &binds).await?;
        let items = values(rows);
        let items = if normalize_self {
            normalize_self_log_items(items, offset)
        } else {
            items
        };
        Ok(json!({"page": page, "page_size": page_size, "total": total, "items": items}))
    }

    async fn stats(
        &self,
        call: ObservabilityCall,
        start: i64,
        end: i64,
    ) -> Result<Value, ObservabilityStoreError> {
        let username = match call.operation {
            ObservabilityOperation::SelfLogStats => match &call.principal {
                ObservabilityPrincipal::User { username, .. } => username.clone(),
                _ => return Err(ObservabilityStoreError::Unavailable),
            },
            _ => call.query.get("username").cloned().unwrap_or_default(),
        };
        let model_name = call.query.get("model_name").cloned().unwrap_or_default();
        let token_name = call.query.get("token_name").cloned().unwrap_or_default();
        let channel = integer_query(&call.query, "channel");
        let group = call.query.get("group").cloned().unwrap_or_default();

        // Go's SumUsedQuota ignores its logType argument and always measures
        // consume logs (type=2). The explicit date range applies only to
        // quota; rpm/tpm use the same filters with a fresh 60-second cutoff.
        let (quota_where, quota_binds) = stats_where(
            &username,
            &model_name,
            &token_name,
            channel,
            &group,
            Some(start),
            Some(end),
        )?;
        let quota_sql =
            format!("SELECT COALESCE(SUM(l.quota), 0)::BIGINT AS quota FROM logs l {quota_where}");
        let quota_row = self.fetch_log_row(&quota_sql, &quota_binds).await?;

        let recent_cutoff = chrono::Utc::now().timestamp().saturating_sub(60);
        let (rate_where, rate_binds) = stats_where(
            &username,
            &model_name,
            &token_name,
            channel,
            &group,
            Some(recent_cutoff),
            None,
        )?;
        let rate_sql = format!(
            "SELECT COUNT(*) AS rpm, (COALESCE(SUM(l.prompt_tokens), 0) + COALESCE(SUM(l.completion_tokens), 0))::BIGINT AS tpm FROM logs l {rate_where}"
        );
        let rate_row = self.fetch_log_row(&rate_sql, &rate_binds).await?;
        Ok(json!({
            "quota": quota_row.try_get::<i64, _>("quota").map_err(|_| ObservabilityStoreError::Unavailable)?,
            "rpm": rate_row.try_get::<i64, _>("rpm").map_err(|_| ObservabilityStoreError::Unavailable)?,
            "tpm": rate_row.try_get::<i64, _>("tpm").map_err(|_| ObservabilityStoreError::Unavailable)?,
        }))
    }

    async fn fetch_log_rows(
        &self,
        sql: &str,
        binds: &[LogBind],
    ) -> Result<Vec<sqlx::postgres::PgRow>, ObservabilityStoreError> {
        let mut query = sqlx::query(sql);
        for bind in binds {
            query = match bind {
                LogBind::I64(value) => query.bind(*value),
                LogBind::Text(value) => query.bind(value.clone()),
            };
        }
        query
            .fetch_all(&self.pg)
            .await
            .map_err(|_| ObservabilityStoreError::Unavailable)
    }

    async fn fetch_log_row(
        &self,
        sql: &str,
        binds: &[LogBind],
    ) -> Result<sqlx::postgres::PgRow, ObservabilityStoreError> {
        let rows = self.fetch_log_rows(sql, binds).await?;
        rows.into_iter()
            .next()
            .ok_or(ObservabilityStoreError::Unavailable)
    }

    async fn fetch_log_count(
        &self,
        sql: &str,
        binds: &[LogBind],
    ) -> Result<i64, ObservabilityStoreError> {
        let row = self.fetch_log_row(sql, binds).await?;
        row.try_get::<i64, _>(0)
            .map_err(|_| ObservabilityStoreError::Unavailable)
    }
}

#[derive(Clone, Debug)]
enum LogBind {
    I64(i64),
    Text(String),
}

fn append_log_condition(
    sql: &mut String,
    binds: &mut Vec<LogBind>,
    column: &str,
    op: &str,
    bind: LogBind,
) {
    sql.push_str(" AND ");
    sql.push_str(column);
    sql.push(' ');
    sql.push_str(op);
    sql.push_str(" $");
    sql.push_str(&(binds.len() + 1).to_string());
    binds.push(bind);
}

fn append_log_text_filter(
    sql: &mut String,
    binds: &mut Vec<LogBind>,
    column: &str,
    value: &str,
) -> Result<(), ObservabilityStoreError> {
    if value.is_empty() {
        return Ok(());
    }
    if value.contains('%') {
        let pattern = sanitize_log_like_pattern(value)?;
        sql.push_str(" AND ");
        sql.push_str(column);
        sql.push_str(" LIKE $");
        sql.push_str(&(binds.len() + 1).to_string());
        sql.push_str(" ESCAPE '!'");
        binds.push(LogBind::Text(pattern));
    } else {
        append_log_condition(sql, binds, column, "=", LogBind::Text(value.to_owned()));
    }
    Ok(())
}

fn append_log_exact(sql: &mut String, binds: &mut Vec<LogBind>, column: &str, value: &str) {
    if !value.is_empty() {
        append_log_condition(sql, binds, column, "=", LogBind::Text(value.to_owned()));
    }
}

fn append_log_i64(sql: &mut String, binds: &mut Vec<LogBind>, column: &str, value: i64) {
    if value != 0 {
        append_log_condition(sql, binds, column, "=", LogBind::I64(value));
    }
}

fn append_log_range(sql: &mut String, binds: &mut Vec<LogBind>, start: i64, end: i64) {
    if start != 0 {
        append_log_condition(sql, binds, "l.created_at", ">=", LogBind::I64(start));
    }
    if end != 0 {
        append_log_condition(sql, binds, "l.created_at", "<=", LogBind::I64(end));
    }
}

fn log_where(
    operation: ObservabilityOperation,
    query: &BTreeMap<String, String>,
    principal: &ObservabilityPrincipal,
    start: i64,
    end: i64,
) -> Result<(String, Vec<LogBind>), ObservabilityStoreError> {
    let mut sql = String::from("WHERE 1 = 1");
    let mut binds = Vec::new();
    match operation {
        ObservabilityOperation::AllLogs => {
            append_log_i64(&mut sql, &mut binds, "l.type", integer_query(query, "type"));
            append_log_text_filter(
                &mut sql,
                &mut binds,
                "l.model_name",
                query.get("model_name").map_or("", String::as_str),
            )?;
            append_log_text_filter(
                &mut sql,
                &mut binds,
                "l.username",
                query.get("username").map_or("", String::as_str),
            )?;
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.token_name",
                query.get("token_name").map_or("", String::as_str),
            );
            append_log_i64(
                &mut sql,
                &mut binds,
                "l.channel_id",
                integer_query(query, "channel"),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.\"group\"",
                query.get("group").map_or("", String::as_str),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.request_id",
                query.get("request_id").map_or("", String::as_str),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.upstream_request_id",
                query.get("upstream_request_id").map_or("", String::as_str),
            );
            append_log_range(&mut sql, &mut binds, start, end);
        }
        ObservabilityOperation::SelfLogs => {
            append_log_i64(&mut sql, &mut binds, "l.user_id", user_id(principal)?);
            append_log_i64(&mut sql, &mut binds, "l.type", integer_query(query, "type"));
            append_log_text_filter(
                &mut sql,
                &mut binds,
                "l.model_name",
                query.get("model_name").map_or("", String::as_str),
            )?;
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.token_name",
                query.get("token_name").map_or("", String::as_str),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.\"group\"",
                query.get("group").map_or("", String::as_str),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.request_id",
                query.get("request_id").map_or("", String::as_str),
            );
            append_log_exact(
                &mut sql,
                &mut binds,
                "l.upstream_request_id",
                query.get("upstream_request_id").map_or("", String::as_str),
            );
            append_log_range(&mut sql, &mut binds, start, end);
        }
        ObservabilityOperation::LogsByToken => {
            append_log_i64(&mut sql, &mut binds, "l.token_id", token_id(principal)?);
        }
        _ => return Err(ObservabilityStoreError::Unavailable),
    }
    Ok((sql, binds))
}

fn stats_where(
    username: &str,
    model_name: &str,
    token_name: &str,
    channel: i64,
    group: &str,
    start: Option<i64>,
    end: Option<i64>,
) -> Result<(String, Vec<LogBind>), ObservabilityStoreError> {
    let mut sql = String::from("WHERE l.type = 2");
    let mut binds = Vec::new();
    append_log_text_filter(&mut sql, &mut binds, "l.username", username)?;
    append_log_exact(&mut sql, &mut binds, "l.token_name", token_name);
    if let Some(start) = start.filter(|value| *value != 0) {
        append_log_condition(
            &mut sql,
            &mut binds,
            "l.created_at",
            ">=",
            LogBind::I64(start),
        );
    }
    if let Some(end) = end.filter(|value| *value != 0) {
        append_log_condition(
            &mut sql,
            &mut binds,
            "l.created_at",
            "<=",
            LogBind::I64(end),
        );
    }
    append_log_text_filter(&mut sql, &mut binds, "l.model_name", model_name)?;
    append_log_i64(&mut sql, &mut binds, "l.channel_id", channel);
    append_log_exact(&mut sql, &mut binds, "l.\"group\"", group);
    Ok((sql, binds))
}

fn sanitize_log_like_pattern(input: &str) -> Result<String, ObservabilityStoreError> {
    let pattern = input.replace('!', "!!").replace('_', "!_");
    if pattern.contains("%%") {
        return Err(ObservabilityStoreError::Legacy(
            "搜索模式中不允许包含连续的 % 通配符".to_owned(),
        ));
    }
    let count = pattern.matches('%').count();
    if count > 2 {
        return Err(ObservabilityStoreError::Legacy(
            "搜索模式中最多允许包含 2 个 % 通配符".to_owned(),
        ));
    }
    if count > 0 && pattern.replace('%', "").len() < 2 {
        return Err(ObservabilityStoreError::Legacy(
            "使用模糊搜索时，关键词长度至少为 2 个字符".to_owned(),
        ));
    }
    Ok(pattern)
}

fn integer_query(query: &BTreeMap<String, String>, key: &str) -> i64 {
    query
        .get(key)
        .and_then(|value| value.parse().ok())
        .map_or(0, |value| value)
}

fn page_query(query: &BTreeMap<String, String>) -> (i64, i64, i64) {
    let page = query
        .get("p")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|page| *page >= 1)
        .unwrap_or(1);
    let page_size = ["page_size", "ps", "size"]
        .into_iter()
        .filter_map(|key| query.get(key))
        .find_map(|value| value.parse::<i64>().ok().filter(|size| *size > 0))
        .unwrap_or(10)
        .min(100);
    let offset = page.saturating_sub(1).saturating_mul(page_size);
    (page, page_size, offset)
}

fn normalize_self_log_items(mut items: Value, offset: i64) -> Value {
    let Some(rows) = items.as_array_mut() else {
        return items;
    };
    for (index, row) in rows.iter_mut().enumerate() {
        let Some(object) = row.as_object_mut() else {
            continue;
        };
        object.insert(
            "id".to_owned(),
            json!(offset.saturating_add(index as i64).saturating_add(1)),
        );
        let raw_other = match object.get("other") {
            Some(Value::String(raw_other)) => raw_other,
            _ => "",
        };
        let mut other = serde_json::from_str::<serde_json::Map<String, Value>>(raw_other)
            .map(Value::Object)
            .unwrap_or(Value::Null);
        if let Value::Object(map) = &mut other {
            map.remove("admin_info");
            map.remove("audit_info");
            map.remove("stream_status");
        }
        object.insert(
            "other".to_owned(),
            Value::String(serde_json::to_string(&other).unwrap_or_default()),
        );
    }
    items
}

fn user_id(principal: &ObservabilityPrincipal) -> Result<i64, ObservabilityStoreError> {
    match principal {
        ObservabilityPrincipal::User { user_id, .. } if *user_id > 0 => Ok(*user_id),
        _ => Err(ObservabilityStoreError::Unavailable),
    }
}

fn token_id(principal: &ObservabilityPrincipal) -> Result<i64, ObservabilityStoreError> {
    match principal {
        ObservabilityPrincipal::Token { token_id } if *token_id > 0 => Ok(*token_id),
        _ => Err(ObservabilityStoreError::Unavailable),
    }
}

fn values(rows: Vec<sqlx::postgres::PgRow>) -> Value {
    Value::Array(
        rows.into_iter()
            .filter_map(|row| row.try_get::<Value, _>(0).ok())
            .collect(),
    )
}

#[cfg(test)]
fn format_user_visible_logs(value: Value) -> Value {
    let Value::Array(items) = value else {
        return value;
    };
    Value::Array(
        items
            .into_iter()
            .enumerate()
            .map(|(index, mut item)| {
                let Value::Object(object) = &mut item else {
                    return item;
                };
                // Go's self/token log formatter hides channel labels and
                // operator-only audit fields from non-admin viewers.
                object.insert("id".to_owned(), Value::from(index as i64 + 1));
                object.insert("channel_name".to_owned(), Value::String(String::new()));
                let Some(Value::String(raw_other)) = object.get("other").cloned() else {
                    return item;
                };
                let Ok(mut other) = serde_json::from_str::<Map<String, Value>>(&raw_other) else {
                    object.insert("other".to_owned(), Value::String("null".to_owned()));
                    return item;
                };
                other.remove("admin_info");
                other.remove("audit_info");
                other.remove("stream_status");
                if let Ok(serialized) = serde_json::to_string(&other) {
                    object.insert("other".to_owned(), Value::String(serialized));
                }
                item
            })
            .collect(),
    )
}

/// A dependency error that preserves the legacy 500 error branch.
#[derive(Clone, Debug, Error)]
pub enum ObservabilityStoreError {
    /// The selected metrics or persistence backend is unavailable.
    #[error("observability backend unavailable")]
    Unavailable,
    /// A legacy model validation error is returned as HTTP 200 with the
    /// frozen `{success:false,message}` envelope.
    #[error("{0}")]
    Legacy(String),
}

/// State for the independent observability route slice.
#[derive(Clone)]
pub struct ObservabilityState {
    store: Arc<dyn ObservabilityStore>,
    authorizer: Arc<dyn ObservabilityAuthorizer>,
}

impl ObservabilityState {
    /// Creates the route state from application-owned authorization and storage.
    #[must_use]
    pub fn new(
        store: Arc<dyn ObservabilityStore>,
        authorizer: Arc<dyn ObservabilityAuthorizer>,
    ) -> Self {
        Self { store, authorizer }
    }

    /// Retained as a source-compatible composition hook for callers that used
    /// the early candidate API. The frozen Go router applies its route-local
    /// UserAuth/AdminAuth/RootAuth tiers to observability paths; a blanket
    /// console gate would incorrectly turn `/api/data/self` and `/api/log/self`
    /// into 404s before those handlers can return their auth envelopes.
    #[must_use]
    pub fn with_console_access_gate(self, _auth: Arc<dyn DashboardAuth>) -> Self {
        self
    }
}

fn mount_observability_routes(
    routes: Router<ObservabilityState>,
    state: ObservabilityState,
) -> Router {
    routes.with_state(state)
}

fn observability_read_routes() -> Router<ObservabilityState> {
    Router::new()
        .route("/api/data/", get(all_quota_dates))
        .route("/api/data/users", get(quota_dates_by_user))
        .route("/api/data/self", get(self_quota_dates))
        .route("/api/data/flow", get(all_flow_quota_dates))
        .route("/api/data/flow/self", get(self_flow_quota_dates))
        .route("/api/log/", get(all_logs))
        .route(
            "/api/log/channel_affinity_usage_cache",
            get(channel_affinity_usage_cache_stats),
        )
        .route("/api/log/search", get(deprecated_log_search))
        .route("/api/log/self", get(self_logs))
        .route("/api/log/self/search", get(deprecated_self_log_search))
        .route("/api/log/self/stat", get(self_log_stats))
        .route("/api/log/stat", get(log_stats))
        .route("/api/log/token", get(logs_by_token))
}

/// Builds the explicitly mounted storage-only observability read routes.
///
/// This is deliberately limited to the PostgreSQL-backed quota/log reads and
/// the concrete Valkey affinity-cache read. Performance metrics and all
/// process/filesystem maintenance routes remain outside this surface.
pub fn observability_read_router(state: ObservabilityState) -> Router {
    mount_observability_routes(observability_read_routes(), state)
}

/// Builds the production-owned PostgreSQL performance metric reads.
///
/// Process/filesystem maintenance remains deliberately outside this router;
/// it cannot be treated as owned until Rust has an application-level service
/// with the same disk, log, and runtime-stat semantics as Go.
pub fn observability_metrics_router(state: ObservabilityState) -> Router {
    mount_observability_routes(observability_metrics_routes(), state)
}

fn observability_metrics_routes() -> Router<ObservabilityState> {
    Router::new()
        .route("/api/perf-metrics", get(perf_metrics))
        .route("/api/perf-metrics/summary", get(perf_metrics_summary))
}

/// Builds the production-mounted filesystem maintenance read/write routes
/// whose behavior is implemented by [`PgDiskCacheMaintenance`].
///
/// Keeping this separate from [`observability_router`] prevents the still
/// unavailable GC/log/counter operations from being exposed by accident.
pub fn observability_disk_cache_router(state: ObservabilityState) -> Router {
    mount_observability_routes(
        Router::new()
            .route("/api/performance/disk_cache", delete(clear_disk_cache))
            .route("/api/performance/logs", get(log_files)),
        state,
    )
}

/// Builds the root-only performance routes backed by the Rust process and
/// PostgreSQL configuration. Force-GC is mounted separately through
/// [`observability_force_gc_router`] so the path exists while the store can
/// still fail closed.
pub fn observability_performance_router(state: ObservabilityState) -> Router {
    mount_observability_routes(
        Router::new()
            .route("/api/performance/stats", get(performance_stats))
            .route(
                "/api/performance/reset_stats",
                post(reset_performance_stats),
            )
            .route("/api/performance/logs", delete(cleanup_log_files)),
        state,
    )
}

pub fn observability_force_gc_router(state: ObservabilityState) -> Router {
    mount_observability_routes(
        Router::new().route("/api/performance/gc", post(force_gc)),
        state,
    )
}

/// Builds the full observability and maintenance candidate router.
///
/// `/api/system-info` and `/api/system-task` are intentionally absent: their
/// PostgreSQL-backed ownership is already established in `control_admin`.
pub fn observability_router(state: ObservabilityState) -> Router {
    observability_read_router(state.clone())
        .merge(observability_metrics_router(state.clone()))
        .merge(observability_disk_cache_router(state.clone()))
        .merge(observability_performance_router(state.clone()))
        .merge(observability_force_gc_router(state))
}

#[derive(Serialize)]
struct LegacyEnvelope<T: Serialize> {
    success: bool,
    message: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    data: Option<T>,
}

fn success(data: Value) -> Response {
    Json(LegacyEnvelope {
        success: true,
        message: String::new(),
        data: Some(data),
    })
    .into_response()
}

fn success_data(data: Value) -> Response {
    Json(json!({"success": true, "data": data})).into_response()
}

fn success_message(message: &str) -> Response {
    Json(LegacyEnvelope::<Value> {
        success: true,
        message: message.to_owned(),
        data: None,
    })
    .into_response()
}

fn failure(status: StatusCode, message: impl Into<String>) -> Response {
    (
        status,
        Json(LegacyEnvelope::<Value> {
            success: false,
            message: message.into(),
            data: None,
        }),
    )
        .into_response()
}

fn failure_data(status: StatusCode, message: impl Into<String>, data: Value) -> Response {
    (
        status,
        Json(LegacyEnvelope {
            success: false,
            message: message.into(),
            data: Some(data),
        }),
    )
        .into_response()
}

fn auth_failure(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

async fn authorize(
    state: &ObservabilityState,
    headers: &HeaderMap,
    access: ObservabilityAccess,
) -> Result<ObservabilityPrincipal, Response> {
    let principal =
        state
            .authorizer
            .authorize(headers, access)
            .await
            .map_err(|_| match access {
                ObservabilityAccess::Token => failure(StatusCode::UNAUTHORIZED, "未提供令牌"),
                ObservabilityAccess::Admin
                | ObservabilityAccess::Root
                | ObservabilityAccess::User
                | ObservabilityAccess::PublicOrUser => auth_failure(
                    StatusCode::UNAUTHORIZED,
                    "AUTH_UNAUTHORIZED",
                    "Unauthorized",
                ),
            })?;
    let allowed = match (&principal, access) {
        (ObservabilityPrincipal::User { role, .. }, ObservabilityAccess::Admin) => {
            *role >= ADMIN_ROLE
        }
        (ObservabilityPrincipal::User { role, .. }, ObservabilityAccess::Root) => {
            *role >= ROOT_ROLE
        }
        (ObservabilityPrincipal::User { user_id, .. }, ObservabilityAccess::User) => *user_id > 0,
        (ObservabilityPrincipal::Token { token_id }, ObservabilityAccess::Token) => *token_id > 0,
        (
            ObservabilityPrincipal::Public | ObservabilityPrincipal::User { .. },
            ObservabilityAccess::PublicOrUser,
        ) => true,
        _ => false,
    };
    if allowed {
        Ok(principal)
    } else {
        Err(auth_failure(
            StatusCode::FORBIDDEN,
            "AUTH_INSUFFICIENT_PRIVILEGE",
            "管理员权限不足",
        ))
    }
}

async fn execute(
    state: &ObservabilityState,
    headers: &HeaderMap,
    access: ObservabilityAccess,
    operation: ObservabilityOperation,
    query: BTreeMap<String, String>,
) -> Response {
    let principal = match authorize(state, headers, access).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    execute_authorized(state, principal, operation, query).await
}

async fn execute_authorized(
    state: &ObservabilityState,
    principal: ObservabilityPrincipal,
    operation: ObservabilityOperation,
    query: BTreeMap<String, String>,
) -> Response {
    let dashboard_user = matches!(&principal, ObservabilityPrincipal::User { .. });
    let response = match state
        .store
        .execute(ObservabilityCall {
            operation,
            principal,
            query,
        })
        .await
    {
        Ok(data)
            if operation == ObservabilityOperation::CleanupLogFiles
                && data
                    .get("failed_files")
                    .and_then(Value::as_array)
                    .is_some_and(|files| !files.is_empty()) =>
        {
            let failed = data
                .get("failed_files")
                .and_then(Value::as_array)
                .map_or(0, Vec::len);
            let deleted = data
                .get("deleted_count")
                .and_then(Value::as_i64)
                .unwrap_or_default();
            let attempted = deleted.saturating_add(i64::try_from(failed).unwrap_or(i64::MAX));
            failure_data(
                StatusCode::OK,
                format!("部分文件删除失败（{failed}/{attempted}）"),
                data,
            )
        }
        Ok(data) => success(data),
        Err(ObservabilityStoreError::Legacy(message)) => failure(StatusCode::OK, message),
        Err(error) => failure(StatusCode::INTERNAL_SERVER_ERROR, error.to_string()),
    };
    if dashboard_user {
        with_auth_version(response)
    } else {
        response
    }
}

async fn execute_authorized_data_only(
    state: &ObservabilityState,
    principal: ObservabilityPrincipal,
    operation: ObservabilityOperation,
    query: BTreeMap<String, String>,
) -> Response {
    let dashboard_user = matches!(&principal, ObservabilityPrincipal::User { .. });
    let response = match state
        .store
        .execute(ObservabilityCall {
            operation,
            principal,
            query,
        })
        .await
    {
        Ok(data) => success_data(data),
        Err(error) => failure(StatusCode::INTERNAL_SERVER_ERROR, error.to_string()),
    };
    if dashboard_user {
        with_auth_version(response)
    } else {
        response
    }
}

async fn execute_raw(
    state: &ObservabilityState,
    headers: &HeaderMap,
    access: ObservabilityAccess,
    operation: ObservabilityOperation,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(state, headers, access).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    execute_authorized(state, principal, operation, parse_query(raw_query)).await
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn with_auth_version_for_user(response: Response, principal: &ObservabilityPrincipal) -> Response {
    if matches!(principal, ObservabilityPrincipal::User { .. }) {
        with_auth_version(response)
    } else {
        response
    }
}

fn parse_query(raw_query: RawQuery) -> BTreeMap<String, String> {
    raw_query.0.map_or_else(BTreeMap::new, |raw| {
        let mut query = BTreeMap::new();
        for pair in raw.split('&') {
            let (key, value) = pair.split_once('=').unwrap_or((pair, ""));
            let Some(key) = decode_query_component(key) else {
                continue;
            };
            let Some(value) = decode_query_component(value) else {
                continue;
            };
            // Gin's `Query` returns the first occurrence. Keeping the first
            // decoded value avoids making duplicate query keys change the
            // selected dashboard filter during the migration.
            query.entry(key).or_insert(value);
        }
        query
    })
}

fn decode_query_component(component: &str) -> Option<String> {
    let bytes = component.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'+' => decoded.push(b' '),
            b'%' if index + 2 < bytes.len() => {
                let high = (bytes[index + 1] as char).to_digit(16)?;
                let low = (bytes[index + 2] as char).to_digit(16)?;
                decoded.push((high * 16 + low) as u8);
                index += 2;
            }
            b'%' => return None,
            byte => decoded.push(byte),
        }
        index += 1;
    }
    String::from_utf8(decoded).ok()
}

fn flow_range(query: &BTreeMap<String, String>) -> Result<(i64, i64), &'static str> {
    let start = query
        .get("start_timestamp")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .ok_or("invalid start_timestamp")?;
    let end = query
        .get("end_timestamp")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .ok_or("invalid end_timestamp")?;
    if end < start {
        return Err("invalid time range");
    }
    Ok((start, end))
}

fn self_range_is_too_large(query: &BTreeMap<String, String>) -> bool {
    let timestamp = |name| {
        query
            .get(name)
            .and_then(|value| value.parse::<i64>().ok())
            .map_or(0, |value| value)
    };
    let start = timestamp("start_timestamp");
    let end = timestamp("end_timestamp");
    end.saturating_sub(start) > MAX_SELF_RANGE_SECONDS
}

async fn all_quota_dates(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Admin,
        ObservabilityOperation::AllQuotaDates,
        raw_query,
    )
    .await
}

async fn quota_dates_by_user(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Admin,
        ObservabilityOperation::QuotaDatesByUser,
        raw_query,
    )
    .await
}

async fn self_quota_dates(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::User).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    if self_range_is_too_large(&query) {
        return with_auth_version_for_user(
            failure(StatusCode::OK, "时间跨度不能超过 1 个月"),
            &principal,
        );
    }
    execute_authorized(
        &state,
        principal,
        ObservabilityOperation::SelfQuotaDates,
        query,
    )
    .await
}

async fn all_flow_quota_dates(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::Admin).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    if let Err(message) = flow_range(&query) {
        return with_auth_version_for_user(failure(StatusCode::OK, message), &principal);
    }
    execute_authorized(
        &state,
        principal,
        ObservabilityOperation::AllFlowQuotaDates,
        query,
    )
    .await
}

async fn self_flow_quota_dates(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::User).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    if let Err(message) = flow_range(&query) {
        return with_auth_version_for_user(failure(StatusCode::OK, message), &principal);
    }
    if self_range_is_too_large(&query) {
        return with_auth_version_for_user(
            failure(StatusCode::OK, "时间跨度不能超过 1 个月"),
            &principal,
        );
    }
    execute_authorized(
        &state,
        principal,
        ObservabilityOperation::SelfFlowQuotaDates,
        query,
    )
    .await
}

async fn all_logs(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Admin,
        ObservabilityOperation::AllLogs,
        raw_query,
    )
    .await
}

async fn channel_affinity_usage_cache_stats(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::Admin).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    if query.get("rule_name").is_none_or(String::is_empty) {
        return with_auth_version_for_user(
            failure(StatusCode::BAD_REQUEST, "missing param: rule_name"),
            &principal,
        );
    }
    if query.get("key_fp").is_none_or(String::is_empty) {
        return with_auth_version_for_user(
            failure(StatusCode::BAD_REQUEST, "missing param: key_fp"),
            &principal,
        );
    }
    execute_authorized(
        &state,
        principal,
        ObservabilityOperation::ChannelAffinityUsageCacheStats,
        query,
    )
    .await
}

async fn deprecated_log_search(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::Admin).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let _ = principal;
    failure(StatusCode::OK, "该接口已废弃")
}

async fn self_logs(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::User,
        ObservabilityOperation::SelfLogs,
        raw_query,
    )
    .await
}

async fn deprecated_self_log_search(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::User).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let _ = principal;
    failure(StatusCode::OK, "该接口已废弃")
}

async fn self_log_stats(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::User,
        ObservabilityOperation::SelfLogStats,
        raw_query,
    )
    .await
}

async fn log_stats(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Admin,
        ObservabilityOperation::LogStats,
        raw_query,
    )
    .await
}

async fn logs_by_token(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Token,
        ObservabilityOperation::LogsByToken,
        raw_query,
    )
    .await
}

async fn perf_metrics(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::PublicOrUser).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    if query.get("model").is_none_or(String::is_empty) {
        return with_auth_version_for_user(
            failure(StatusCode::BAD_REQUEST, "model is required"),
            &principal,
        );
    }
    execute_authorized_data_only(
        &state,
        principal,
        ObservabilityOperation::PerfMetrics,
        query,
    )
    .await
}

async fn perf_metrics_summary(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::PublicOrUser).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    execute_authorized_data_only(
        &state,
        principal,
        ObservabilityOperation::PerfMetricsSummary,
        parse_query(raw_query),
    )
    .await
}

async fn clear_disk_cache(State(state): State<ObservabilityState>, headers: HeaderMap) -> Response {
    let response = execute(
        &state,
        &headers,
        ObservabilityAccess::Root,
        ObservabilityOperation::ClearDiskCache,
        BTreeMap::new(),
    )
    .await;
    if response.status() == StatusCode::OK {
        success_message("不活跃的磁盘缓存已清理")
    } else {
        response
    }
}

async fn force_gc(State(state): State<ObservabilityState>, headers: HeaderMap) -> Response {
    let response = execute(
        &state,
        &headers,
        ObservabilityAccess::Root,
        ObservabilityOperation::ForceGc,
        BTreeMap::new(),
    )
    .await;
    if response.status() == StatusCode::OK {
        success_message("GC 已执行")
    } else {
        response
    }
}

async fn log_files(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Root,
        ObservabilityOperation::LogFiles,
        raw_query,
    )
    .await
}

async fn cleanup_log_files(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    let principal = match authorize(&state, &headers, ObservabilityAccess::Root).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query);
    let mode = query.get("mode").map_or("", String::as_str);
    if mode != "by_count" && mode != "by_days" {
        return failure(StatusCode::OK, "invalid mode, must be by_count or by_days");
    }
    let valid_value = query
        .get("value")
        .and_then(|value| value.parse::<i64>().ok())
        .is_some_and(|value| value > 0);
    if !valid_value {
        return failure(StatusCode::OK, "invalid value, must be a positive integer");
    }
    execute_authorized(
        &state,
        principal,
        ObservabilityOperation::CleanupLogFiles,
        query,
    )
    .await
}

async fn reset_performance_stats(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
) -> Response {
    let response = execute(
        &state,
        &headers,
        ObservabilityAccess::Root,
        ObservabilityOperation::ResetPerformanceStats,
        BTreeMap::new(),
    )
    .await;
    if response.status() == StatusCode::OK {
        success_message("统计信息已重置")
    } else {
        response
    }
}

async fn performance_stats(
    State(state): State<ObservabilityState>,
    headers: HeaderMap,
    raw_query: RawQuery,
) -> Response {
    execute_raw(
        &state,
        &headers,
        ObservabilityAccess::Root,
        ObservabilityOperation::PerformanceStats,
        raw_query,
    )
    .await
}

/// Lightweight deterministic backend for focused HTTP tests.
///
/// This is not a production default.  In particular, the affinity-cache
/// statistic fails closed because it requires a process-owned cache service.
#[derive(Default)]
pub struct InMemoryObservabilityStore;

#[async_trait]
impl ObservabilityStore for InMemoryObservabilityStore {
    async fn execute(&self, call: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        let data = match call.operation {
            ObservabilityOperation::AllQuotaDates
            | ObservabilityOperation::QuotaDatesByUser
            | ObservabilityOperation::SelfQuotaDates
            | ObservabilityOperation::AllFlowQuotaDates
            | ObservabilityOperation::SelfFlowQuotaDates
            | ObservabilityOperation::LogsByToken => json!([]),
            ObservabilityOperation::AllLogs | ObservabilityOperation::SelfLogs => {
                json!({"page": 1, "page_size": 10, "total": 0, "items": []})
            }
            ObservabilityOperation::ChannelAffinityUsageCacheStats => {
                return Err(ObservabilityStoreError::Unavailable);
            }
            ObservabilityOperation::SelfLogStats | ObservabilityOperation::LogStats => {
                json!({"quota": 0, "rpm": 0, "tpm": 0})
            }
            ObservabilityOperation::PerfMetrics => json!({"groups": []}),
            ObservabilityOperation::PerfMetricsSummary => json!({"groups": []}),
            ObservabilityOperation::ClearDiskCache
            | ObservabilityOperation::ForceGc
            | ObservabilityOperation::ResetPerformanceStats => Value::Null,
            ObservabilityOperation::CleanupLogFiles => {
                json!({"deleted_count": 0, "freed_bytes": 0, "failed_files": []})
            }
            ObservabilityOperation::LogFiles => json!({"enabled": false}),
            ObservabilityOperation::PerformanceStats => json!({
                "cache_stats": {}, "memory_stats": {}, "disk_cache_info": {},
                "disk_space_info": {}, "config": {}
            }),
        };
        Ok(data)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardUser,
        LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    };
    use async_trait::async_trait;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use std::sync::{
        Mutex,
        atomic::{AtomicUsize, Ordering},
    };
    use tower::ServiceExt;

    struct StaticDashboardAuth {
        user: DashboardUser,
    }

    #[async_trait]
    impl DashboardAuth for StaticDashboardAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            Ok(CriticalRateLimitOutcome::Allowed)
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
            Ok(self.user.clone())
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            Err(AuthError::new(AuthErrorKind::Unauthorized))
        }
    }

    #[derive(Default)]
    struct StaticTokenAuth;

    #[async_trait]
    impl ObservabilityTokenAuthorizer for StaticTokenAuth {
        async fn authorize_read_only(
            &self,
            presented: &str,
        ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
            if presented == "token" {
                Ok(ObservabilityPrincipal::Token { token_id: 9 })
            } else {
                Err(ObservabilityAuthError::Unauthorized)
            }
        }
    }

    struct CountingStore(AtomicUsize);

    #[async_trait]
    impl ObservabilityStore for CountingStore {
        async fn execute(&self, _: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
            self.0.fetch_add(1, Ordering::Relaxed);
            Ok(json!({}))
        }
    }

    #[derive(Default)]
    struct QueryCapturingStore(Mutex<Option<ObservabilityCall>>);

    #[async_trait]
    impl ObservabilityStore for QueryCapturingStore {
        async fn execute(&self, call: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
            *self.0.lock().expect("query capture lock") = Some(call);
            Ok(json!([]))
        }
    }

    fn user(role: i64) -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "member".to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: String::new(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: String::new(),
            stripe_customer: String::new(),
            sidebar_modules: Value::Null,
            permissions: Value::Null,
        }
    }

    fn router_for(role: i64, store: Arc<CountingStore>) -> Router {
        let authorizer = DashboardObservabilityAuthorizer::new(
            Arc::new(StaticDashboardAuth { user: user(role) }),
            Arc::new(StaticTokenAuth),
        );
        observability_router(ObservabilityState::new(store, Arc::new(authorizer)))
    }

    async fn response_body(response: Response) -> Value {
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body");
        serde_json::from_slice(&body).expect("JSON envelope")
    }

    #[tokio::test]
    async fn dashboard_token_public_and_role_matrix_stays_server_authorized() {
        let member_store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let member = router_for(1, member_store.clone());
        let public = member
            .clone()
            .oneshot(
                Request::get("/api/perf-metrics/summary")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(public.status(), StatusCode::OK);
        let forged_admin = member
            .clone()
            .oneshot(
                Request::get("/api/log/")
                    .header("authorization", "Bearer dashboard")
                    .header("x-role", "100")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(forged_admin.status(), StatusCode::FORBIDDEN);
        let token = member
            .oneshot(
                Request::get("/api/log/token")
                    .header("authorization", "Bearer token")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(token.status(), StatusCode::OK);
        assert_eq!(member_store.0.load(Ordering::Relaxed), 2);

        let admin = router_for(ADMIN_ROLE, Arc::new(CountingStore(AtomicUsize::new(0))));
        assert_eq!(
            admin
                .clone()
                .oneshot(
                    Request::get("/api/log/")
                        .header("authorization", "Bearer dashboard")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap()
                .status(),
            StatusCode::OK
        );
        assert_eq!(
            admin
                .oneshot(
                    Request::post("/api/performance/gc")
                        .header("authorization", "Bearer dashboard")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap()
                .status(),
            StatusCode::FORBIDDEN
        );
        let root = router_for(ROOT_ROLE, Arc::new(CountingStore(AtomicUsize::new(0))));
        assert_eq!(
            root.oneshot(
                Request::post("/api/performance/gc")
                    .header("authorization", "Bearer dashboard")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap()
            .status(),
            StatusCode::OK
        );
    }

    #[tokio::test]
    async fn observability_routes_keep_their_go_auth_boundaries_without_a_blanket_gate() {
        let store = Arc::new(CountingStore(AtomicUsize::new(0)));
        let auth: Arc<dyn DashboardAuth> = Arc::new(StaticDashboardAuth { user: user(1) });
        let authorizer =
            DashboardObservabilityAuthorizer::new(Arc::clone(&auth), Arc::new(StaticTokenAuth));
        let router = observability_router(
            ObservabilityState::new(store.clone(), Arc::new(authorizer))
                .with_console_access_gate(auth),
        );

        let user_read = router
            .clone()
            .oneshot(Request::get("/api/data/self").body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(user_read.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(store.0.load(Ordering::Relaxed), 0);

        let public_metric = router
            .oneshot(
                Request::get("/api/perf-metrics/summary")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(public_metric.status(), StatusCode::OK);
        assert_eq!(store.0.load(Ordering::Relaxed), 1);
    }

    #[tokio::test]
    async fn token_logs_without_timestamps_keep_the_legacy_unbounded_query() {
        let store = Arc::new(QueryCapturingStore::default());
        let authorizer = DashboardObservabilityAuthorizer::new(
            Arc::new(StaticDashboardAuth {
                user: user(ADMIN_ROLE),
            }),
            Arc::new(StaticTokenAuth),
        );
        let response =
            observability_read_router(ObservabilityState::new(store.clone(), Arc::new(authorizer)))
                .oneshot(
                    Request::get("/api/log/token")
                        .header("authorization", "Bearer token")
                        .body(Body::empty())
                        .unwrap(),
                )
                .await
                .unwrap();

        assert_eq!(response.status(), StatusCode::OK);
        let call = store
            .0
            .lock()
            .expect("query capture lock")
            .take()
            .expect("token log storage call");
        assert_eq!(call.operation, ObservabilityOperation::LogsByToken);
        assert!(call.query.is_empty());
        assert_eq!(
            OPTIONAL_LOG_TIME_RANGE,
            "($2 = 0 OR created_at >= $2) AND ($3 = 0 OR created_at <= $3)"
        );
        let body = response_body(response).await;
        assert_eq!(body["success"], true);
        assert_eq!(body["data"], json!([]));
        assert!(body.get("page").is_none());
    }

    #[test]
    fn token_log_query_matches_the_legacy_recent_rows_contract() {
        let query = log_query(ObservabilityOperation::LogsByToken);
        assert!(query.contains("WHERE token_id = $1 ORDER BY id DESC LIMIT 1000"));
        assert!(!query.contains("WHERE token_id = $1 AND"));
        assert!(query.contains("'channel', COALESCE(channel_id, 0)"));
    }

    #[test]
    fn user_visible_logs_match_legacy_masking_and_display_ids() {
        let formatted = format_user_visible_logs(json!([
            {
                "id": 91,
                "channel_name": "private-channel",
                "other": r#"{"admin_info":{"operator":"root"},"audit_info":{"route":"/api/log/token"},"stream_status":"done","safe":"kept"}"#,
            },
            {
                "id": 92,
                "channel_name": "another-private-channel",
                "other": "not-json",
            }
        ]));

        assert_eq!(formatted[0]["id"], 1);
        assert_eq!(formatted[0]["channel_name"], "");
        assert_eq!(formatted[0]["other"], r#"{"safe":"kept"}"#);
        assert_eq!(formatted[1]["id"], 2);
        assert_eq!(formatted[1]["channel_name"], "");
        assert_eq!(formatted[1]["other"], "null");
    }

    #[tokio::test]
    async fn unavailable_runtime_adapters_fail_closed_without_success_payloads() {
        let query = BTreeMap::new();
        assert!(
            UnavailableObservabilityMetrics
                .query(ObservabilityOperation::PerfMetrics, &query)
                .await
                .is_err()
        );
        assert!(
            UnavailableObservabilityMaintenance
                .execute(ObservabilityOperation::ForceGc, &query)
                .await
                .is_err()
        );

        let store = PgObservabilityStore::new(
            PgPool::connect_lazy("postgres://unused:unused@localhost/unused").unwrap(),
            Arc::new(UnavailableObservabilityMetrics),
            Arc::new(UnavailableObservabilityMaintenance),
        );
        let error = store
            .execute(ObservabilityCall {
                operation: ObservabilityOperation::ForceGc,
                principal: ObservabilityPrincipal::User {
                    user_id: 1,
                    username: "root".to_owned(),
                    role: ROOT_ROLE,
                },
                query,
            })
            .await
            .unwrap_err();
        assert!(matches!(error, ObservabilityStoreError::Unavailable));
    }

    #[test]
    fn disk_cache_directory_matches_go_option_and_default() {
        assert_eq!(
            disk_cache_dir(""),
            std::env::temp_dir().join("new-api-body-cache")
        );
        assert_eq!(
            disk_cache_dir("/var/tmp"),
            PathBuf::from("/var/tmp/new-api-body-cache")
        );
        assert_eq!(
            disk_cache_dir("\"/var/tmp\""),
            PathBuf::from("/var/tmp/new-api-body-cache")
        );
    }

    #[test]
    fn disk_cache_cleanup_treats_missing_directory_as_success() {
        let missing = std::env::temp_dir().join(format!(
            "lmm-observability-missing-cache-{}",
            std::process::id()
        ));
        assert!(cleanup_disk_cache(&missing).is_ok());
    }

    #[test]
    fn authorization_and_read_only_token_normalization_match_legacy_forms() {
        let mut headers = HeaderMap::new();
        headers.insert("authorization", "bearer sk-key-channel".parse().unwrap());
        assert_eq!(
            authorization_credential(&headers).as_deref(),
            Some("sk-key-channel")
        );
        assert_eq!(legacy_read_only_token_key("sk-key-channel"), Some("key"));
        headers.insert("authorization", "Bearer one two".parse().unwrap());
        assert_eq!(authorization_credential(&headers), None);
    }

    #[tokio::test]
    async fn unavailable_route_keeps_the_legacy_500_envelope() {
        let state = ObservabilityState::new(
            Arc::new(PgObservabilityStore::new(
                PgPool::connect_lazy("postgres://unused:unused@localhost/unused").unwrap(),
                Arc::new(UnavailableObservabilityMetrics),
                Arc::new(UnavailableObservabilityMaintenance),
            )),
            Arc::new(DashboardObservabilityAuthorizer::new(
                Arc::new(StaticDashboardAuth {
                    user: user(ROOT_ROLE),
                }),
                Arc::new(StaticTokenAuth),
            )),
        );
        let response = observability_router(state)
            .oneshot(
                Request::post("/api/performance/gc")
                    .header("authorization", "Bearer dashboard")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
        assert_eq!(
            response_body(response).await["message"],
            Value::String("observability backend unavailable".to_owned())
        );
    }

    #[test]
    fn performance_metric_aggregation_matches_legacy_integer_and_percentage_rules() {
        let counters = PerfCounters {
            request_count: 4,
            success_count: 3,
            total_latency_ms: 1_000,
            ttft_sum_ms: 200,
            ttft_count: 2,
            output_tokens: 300,
            generation_ms: 2_000,
        };
        let value = perf_group_value("default", &[(1_700_000_000, counters)]);
        assert_eq!(value["avg_ttft_ms"], 100);
        assert_eq!(value["avg_latency_ms"], 250);
        assert_eq!(value["success_rate"].as_i64(), Some(75));
        assert_eq!(value["avg_tps"].as_i64(), Some(150));
        assert_eq!(value["series"][0]["ts"], 1_700_000_000);
    }

    #[test]
    fn performance_metric_hours_use_go_defaults_and_thirty_day_cap() {
        let default_range = perf_time_range(&BTreeMap::new()).expect("default time range");
        assert!((default_range.1 - default_range.0) >= 24 * 60 * 60);
        assert!((default_range.1 - default_range.0) <= 24 * 60 * 60 + 1);

        let zero_range = perf_time_range(&BTreeMap::from([("hours".to_owned(), "0".to_owned())]))
            .expect("zero uses default");
        assert!((zero_range.1 - zero_range.0) >= 24 * 60 * 60);
        let capped_range =
            perf_time_range(&BTreeMap::from([("hours".to_owned(), "9999".to_owned())]))
                .expect("large range is capped");
        assert!((capped_range.1 - capped_range.0) <= 30 * 24 * 60 * 60 + 1);
    }
}
