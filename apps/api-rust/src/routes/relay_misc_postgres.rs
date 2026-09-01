//! PostgreSQL-backed executor for the non-chat OpenAI-compatible relay paths.
//!
//! This executor is deliberately fail-closed outside the behaviorally proven
//! fixed-price, non-streaming OpenAI-compatible vertical. It is not mounted by
//! the normal listener until the real Go/Rust differential approves the whole
//! route lifecycle.

use std::{
    collections::{HashMap, HashSet, VecDeque},
    net::{IpAddr, Ipv4Addr},
    path::{Path, PathBuf},
    sync::{Arc, Mutex},
    time::{Duration, Instant, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::Request,
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde::Serialize;
use serde_json::Value;
use sqlx::{PgPool, Postgres, Row, Transaction, postgres::PgRow};

use crate::{
    RequestContext,
    models::{ModelsError, ModelsErrorKind, ModelsRequest, PgModelsService},
};

use super::relay_misc::{
    RelayAuth, RelayMiscHttpState, RelayMiscService, RelayProtocol, RelayRequestContext,
    filtered_upstream_response, routes,
};

const MAX_RELAY_BODY_BYTES: usize = 128 * 1024 * 1024;
const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
const MAX_QUOTA: f64 = i32::MAX as f64;
const MODEL_RATE_LIMIT_TOKEN_BUCKET: &str = r#"
local now = redis.call('TIME')
local now_seconds = tonumber(now[1])
local bucket = redis.call('HMGET', KEYS[1], 'tokens', 'last_time')
local tokens = tonumber(bucket[1])
local last_time = tonumber(bucket[2])
local requested = tonumber(ARGV[1])
local rate = tonumber(ARGV[2])
local capacity = tonumber(ARGV[3])
if not tokens or not last_time then
  tokens = capacity
  last_time = now_seconds
else
  tokens = math.min(capacity, tokens + ((now_seconds - last_time) * rate))
  last_time = now_seconds
end
local allowed = 0
if tokens >= requested then
  tokens = tokens - requested
  allowed = 1
end
redis.call('HMSET', KEYS[1], 'tokens', tokens, 'last_time', last_time)
return allowed
"#;

const SYSTEM_PERFORMANCE_SAMPLE_INTERVAL: Duration = Duration::from_secs(5);

#[derive(Clone, Debug, Eq, PartialEq)]
struct PerformanceMonitorConfig {
    enabled: bool,
    cpu_threshold: i64,
    memory_threshold: i64,
    disk_threshold: i64,
    disk_cache_path: String,
}

impl Default for PerformanceMonitorConfig {
    fn default() -> Self {
        Self {
            enabled: true,
            cpu_threshold: 90,
            memory_threshold: 90,
            disk_threshold: 95,
            disk_cache_path: String::new(),
        }
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq)]
struct SystemPerformanceStatus {
    cpu_usage: f64,
    memory_usage: f64,
    disk_usage: f64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
struct CpuTimes {
    total: u64,
    busy: u64,
}

struct SystemPerformanceMonitor {
    state: Mutex<SystemPerformanceState>,
}

struct SystemPerformanceState {
    status: SystemPerformanceStatus,
    cpu_times: Option<CpuTimes>,
    disk_path: PathBuf,
    sampled_at: Instant,
}

impl SystemPerformanceMonitor {
    fn new() -> Self {
        let disk_path = std::env::temp_dir();
        Self {
            state: Mutex::new(SystemPerformanceState {
                status: SystemPerformanceStatus {
                    // Go's first percentage is measured from an earlier
                    // process baseline. Rust establishes that baseline here
                    // and reports zero until the next five-second sample.
                    cpu_usage: 0.0,
                    memory_usage: read_memory_usage().unwrap_or(0.0),
                    disk_usage: read_disk_usage(&disk_path).unwrap_or(0.0),
                },
                cpu_times: read_cpu_times(),
                disk_path,
                sampled_at: Instant::now(),
            }),
        }
    }

    fn status(&self, disk_path: &Path) -> SystemPerformanceStatus {
        let mut state = self
            .state
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if state.disk_path == disk_path
            && state.sampled_at.elapsed() < SYSTEM_PERFORMANCE_SAMPLE_INTERVAL
        {
            return state.status;
        }

        let current_cpu_times = read_cpu_times();
        let cpu_usage = state
            .cpu_times
            .zip(current_cpu_times)
            .map_or(0.0, |(previous, current)| cpu_usage(previous, current));
        state.status = SystemPerformanceStatus {
            cpu_usage,
            memory_usage: read_memory_usage().unwrap_or(0.0),
            disk_usage: read_disk_usage(disk_path).unwrap_or(0.0),
        };
        state.cpu_times = current_cpu_times;
        state.disk_path = disk_path.to_path_buf();
        state.sampled_at = Instant::now();
        state.status
    }
}

fn performance_monitor_config(values: &HashMap<String, String>) -> PerformanceMonitorConfig {
    let mut config = PerformanceMonitorConfig::default();
    if let Some(enabled) = values
        .get("performance_setting.monitor_enabled")
        .and_then(|value| go_config_bool(value))
    {
        config.enabled = enabled;
    }
    if let Some(threshold) = values
        .get("performance_setting.monitor_cpu_threshold")
        .and_then(|value| go_config_integer(value))
    {
        config.cpu_threshold = threshold;
    }
    if let Some(threshold) = values
        .get("performance_setting.monitor_memory_threshold")
        .and_then(|value| go_config_integer(value))
    {
        config.memory_threshold = threshold;
    }
    if let Some(threshold) = values
        .get("performance_setting.monitor_disk_threshold")
        .and_then(|value| go_config_integer(value))
    {
        config.disk_threshold = threshold;
    }
    if let Some(path) = values.get("performance_setting.disk_cache_path") {
        config.disk_cache_path.clone_from(path);
    }
    config
}

fn go_config_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "TRUE" | "true" | "True" => Some(true),
        "0" | "f" | "F" | "FALSE" | "false" | "False" => Some(false),
        _ => None,
    }
}

fn go_config_integer(value: &str) -> Option<i64> {
    value.parse::<i64>().ok().or_else(|| {
        value
            .parse::<f64>()
            .ok()
            .filter(|value| value.is_finite())
            .map(|value| value as i64)
    })
}

fn performance_rejection(
    config: PerformanceMonitorConfig,
    status: SystemPerformanceStatus,
) -> Option<RelayAuth> {
    if threshold_exceeded(status.cpu_usage, config.cpu_threshold) {
        return Some(performance_overload(
            "cpu",
            status.cpu_usage,
            config.cpu_threshold,
            "system_cpu_overloaded",
        ));
    }
    if threshold_exceeded(status.memory_usage, config.memory_threshold) {
        return Some(performance_overload(
            "memory",
            status.memory_usage,
            config.memory_threshold,
            "system_memory_overloaded",
        ));
    }
    if threshold_exceeded(status.disk_usage, config.disk_threshold) {
        return Some(performance_overload(
            "disk",
            status.disk_usage,
            config.disk_threshold,
            "system_disk_overloaded",
        ));
    }
    None
}

fn threshold_exceeded(usage: f64, threshold: i64) -> bool {
    threshold > 0 && usage.trunc() > threshold as f64
}

fn performance_overload(
    resource: &str,
    usage: f64,
    threshold: i64,
    code: &'static str,
) -> RelayAuth {
    RelayAuth::RejectedOpenAiWithParam {
        status: StatusCode::SERVICE_UNAVAILABLE,
        message: format!(
            "system {resource} overloaded (current: {usage:.1}%, threshold: {threshold}%)"
        ),
        code,
    }
}

fn read_cpu_times() -> Option<CpuTimes> {
    #[cfg(target_os = "linux")]
    {
        std::fs::read_to_string("/proc/stat")
            .ok()
            .and_then(|raw| cpu_times_from_stat(&raw))
    }
    #[cfg(not(target_os = "linux"))]
    None
}

fn cpu_times_from_stat(raw: &str) -> Option<CpuTimes> {
    let mut fields = raw
        .lines()
        .find(|line| line.starts_with("cpu "))?
        .split_whitespace()
        .skip(1);
    let user = fields.next()?.parse::<u64>().ok()?;
    let nice = fields.next()?.parse::<u64>().ok()?;
    let system = fields.next()?.parse::<u64>().ok()?;
    let idle = fields.next()?.parse::<u64>().ok()?;
    let iowait = fields.next()?.parse::<u64>().ok()?;
    let irq = fields.next()?.parse::<u64>().ok()?;
    let softirq = fields.next()?.parse::<u64>().ok()?;
    let steal = match fields.next() {
        Some(value) => value.parse::<u64>().ok()?,
        None => 0,
    };
    // gopsutil v3 counts iowait as busy for this API and deliberately omits
    // guest/guest_nice because those counters are already included in user.
    let busy = user
        .saturating_add(nice)
        .saturating_add(system)
        .saturating_add(iowait)
        .saturating_add(irq)
        .saturating_add(softirq)
        .saturating_add(steal);
    Some(CpuTimes {
        total: busy.saturating_add(idle),
        busy,
    })
}

fn cpu_usage(previous: CpuTimes, current: CpuTimes) -> f64 {
    if current.busy <= previous.busy {
        return 0.0;
    }
    if current.total <= previous.total {
        return 100.0;
    }
    ((current.busy - previous.busy) as f64 / (current.total - previous.total) as f64 * 100.0)
        .clamp(0.0, 100.0)
}

fn read_memory_usage() -> Option<f64> {
    #[cfg(target_os = "linux")]
    {
        std::fs::read_to_string("/proc/meminfo")
            .ok()
            .and_then(|raw| memory_usage_from_meminfo(&raw))
    }
    #[cfg(not(target_os = "linux"))]
    None
}

fn memory_usage_from_meminfo(raw: &str) -> Option<f64> {
    let mut total = None;
    let mut free = None;
    let mut buffers = None;
    let mut cached = None;
    let mut reclaimable = None;
    for line in raw.lines() {
        let Some((key, value)) = line.split_once(':') else {
            continue;
        };
        match key {
            "MemTotal" | "MemFree" | "Buffers" | "Cached" | "SReclaimable" => {
                let value = value.split_whitespace().next()?.parse::<u64>().ok()?;
                match key {
                    "MemTotal" => total = Some(value),
                    "MemFree" => free = Some(value),
                    "Buffers" => buffers = Some(value),
                    "Cached" => cached = Some(value),
                    "SReclaimable" => reclaimable = Some(value),
                    _ => unreachable!(),
                }
            }
            _ => {}
        }
    }
    let total = total?;
    let used = total
        .saturating_sub(free?)
        .saturating_sub(buffers?)
        .saturating_sub(cached?)
        .saturating_sub(reclaimable?);
    usage_percent(total, used)
}

#[cfg(unix)]
fn read_disk_usage(path: &Path) -> Option<f64> {
    let stat = rustix::fs::statvfs(path).ok()?;
    let total = stat.f_blocks.saturating_mul(stat.f_bsize);
    let free = stat.f_bfree.saturating_mul(stat.f_bsize);
    usage_percent(total, total.saturating_sub(free))
}

#[cfg(not(unix))]
fn read_disk_usage(_: &Path) -> Option<f64> {
    None
}

fn usage_percent(total: u64, used: u64) -> Option<f64> {
    (total > 0).then(|| used as f64 / total as f64 * 100.0)
}

/// A fail-closed PostgreSQL executor for the behaviorally proven relay-misc
/// subset.
#[derive(Clone)]
pub struct PgRelayMiscService {
    pg: PgPool,
    models: Arc<PgModelsService>,
    client: reqwest::Client,
    response_header_timeout: Duration,
    valkey: Option<redis::Client>,
    rate_limit_timeout: Duration,
    memory_rate_limits: Arc<MemoryModelRateLimits>,
    performance_monitor: Arc<SystemPerformanceMonitor>,
}

impl PgRelayMiscService {
    /// Creates an executor whose authentication boundary is shared with the
    /// current model-list token/trust policy.
    #[must_use]
    pub fn new(
        pg: PgPool,
        models: Arc<PgModelsService>,
        client: reqwest::Client,
        response_header_timeout: Duration,
    ) -> Self {
        Self {
            pg,
            models,
            client,
            response_header_timeout,
            valkey: None,
            rate_limit_timeout: response_header_timeout,
            memory_rate_limits: Arc::new(MemoryModelRateLimits::default()),
            performance_monitor: Arc::new(SystemPerformanceMonitor::new()),
        }
    }

    /// Uses the same Valkey authority as the normal listener for Go-compatible
    /// per-user model request limits. Without this adapter the service follows
    /// Go's Redis-disabled in-process fallback.
    #[must_use]
    pub fn with_model_rate_limit_valkey(
        mut self,
        valkey: redis::Client,
        timeout: Duration,
    ) -> Self {
        self.valkey = Some(valkey);
        self.rate_limit_timeout = timeout;
        self
    }

    async fn authenticate_request(
        &self,
        input: RelayAuthenticationInput,
    ) -> Result<RelayPrincipal, RelayAuth> {
        let RelayAuthenticationInput {
            request_id,
            client_ip,
            authorization,
            credential,
        } = input;
        self.models
            .authenticate_only_with_policy(
                ModelsRequest {
                    authorization,
                    api_key: None,
                    gemini_key: None,
                    mj_api_secret: None,
                    client_ip,
                },
                true,
            )
            .await
            .map_err(|error| auth_failure(error, &request_id))?;

        let row = sqlx::query(
            r#"SELECT t.id AS token_id, t.user_id, COALESCE(t.name,'') AS token_name,
                      COALESCE(t.model_limits_enabled,FALSE) AS model_limits_enabled,
                      COALESCE(t.model_limits,'') AS model_limits,
                      COALESCE(t."group",'') AS token_group,
                      COALESCE(u.username,'') AS username,
                      COALESCE(u."group",'default') AS user_group,
                      COALESCE(u.setting,'') AS user_setting,
                      COALESCE(u.role,1) AS role,
                      COALESCE(u.created_at,0) AS user_created_at,
                      COALESCE(u.last_api_activity_at,0) AS last_api_activity_at,
                      u.trust_level_override::BIGINT AS trust_level_override
                 FROM tokens t
                 JOIN users u ON u.id=t.user_id
                WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL"#,
        )
        .bind(&credential.key)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| storage_rejection(&request_id))?
        .ok_or(RelayAuth::ConcealedNotFound)?;

        let role = row
            .try_get::<i64, _>("role")
            .map_err(|_| storage_rejection(&request_id))?;
        let specific_channel_id = match credential.channel_suffix {
            Some(suffix) if role >= 10 => Some(suffix.parse::<i64>().map_err(|_| {
                coded_rejection(StatusCode::BAD_REQUEST, "无效的渠道 ID", "", &request_id)
            })?),
            Some(_) => {
                return Err(coded_rejection(
                    StatusCode::FORBIDDEN,
                    "普通用户不支持指定渠道",
                    "",
                    &request_id,
                ));
            }
            None => None,
        };

        let mut principal = RelayPrincipal {
            token_id: row
                .try_get("token_id")
                .map_err(|_| storage_rejection(&request_id))?,
            token_key: credential.key,
            token_name: row
                .try_get("token_name")
                .map_err(|_| storage_rejection(&request_id))?,
            user_id: row
                .try_get("user_id")
                .map_err(|_| storage_rejection(&request_id))?,
            username: row
                .try_get("username")
                .map_err(|_| storage_rejection(&request_id))?,
            role,
            created_at: row
                .try_get("user_created_at")
                .map_err(|_| storage_rejection(&request_id))?,
            last_api_activity_at: row
                .try_get("last_api_activity_at")
                .map_err(|_| storage_rejection(&request_id))?,
            trust_level_override: row
                .try_get("trust_level_override")
                .map_err(|_| storage_rejection(&request_id))?,
            user_group: row
                .try_get("user_group")
                .map_err(|_| storage_rejection(&request_id))?,
            token_group: row
                .try_get("token_group")
                .map_err(|_| storage_rejection(&request_id))?,
            using_group: String::new(),
            record_ip_log: row
                .try_get::<String, _>("user_setting")
                .map(|setting| record_ip_enabled(&setting))
                .map_err(|_| storage_rejection(&request_id))?,
            model_limits_enabled: row
                .try_get("model_limits_enabled")
                .map_err(|_| storage_rejection(&request_id))?,
            model_limits: row
                .try_get("model_limits")
                .map_err(|_| storage_rejection(&request_id))?,
            specific_channel_id,
            request_id,
            client_ip,
        };
        let group_options = options(&self.pg, &["UserUsableGroups", "GroupRatio"])
            .await
            .map_err(|()| storage_rejection(&principal.request_id))?;
        principal.using_group = selected_group(&principal, &group_options)?;
        Ok(principal)
    }

    async fn performance_gate(&self) -> RelayAuth {
        // Go keeps the last/default monitor configuration in memory when the
        // option store is unavailable. Falling back to the same defaults here
        // avoids turning a monitor lookup into a different pre-auth error.
        let values = options(
            &self.pg,
            &[
                "performance_setting.monitor_enabled",
                "performance_setting.monitor_cpu_threshold",
                "performance_setting.monitor_memory_threshold",
                "performance_setting.monitor_disk_threshold",
                "performance_setting.disk_cache_path",
            ],
        )
        .await
        .unwrap_or_default();
        let config = performance_monitor_config(&values);
        if !config.enabled {
            return RelayAuth::Authorized;
        }
        let disk_path = if config.disk_cache_path.is_empty() {
            std::env::temp_dir()
        } else {
            PathBuf::from(&config.disk_cache_path)
        };
        performance_rejection(config, self.performance_monitor.status(&disk_path))
            .unwrap_or(RelayAuth::Authorized)
    }

    async fn rate_limit_gate(
        &self,
        principal: &RelayPrincipal,
    ) -> Result<Option<ModelRateLimitCommit>, RelayAuth> {
        let config = model_rate_limit_config(&self.pg, principal)
            .await
            .map_err(|()| storage_rejection(&principal.request_id))?;
        if !config.enabled {
            return Ok(None);
        }
        if config.duration_minutes <= 0
            || config.total_max_count < 0
            || config.success_max_count < 0
        {
            return Err(model_rate_limit_internal(&principal.request_id));
        }

        if let Some(valkey) = self.valkey.as_ref() {
            return tokio::time::timeout(
                self.rate_limit_timeout,
                check_valkey_model_rate_limit(valkey, principal.user_id, &config),
            )
            .await
            .map_err(|_| model_rate_limit_internal(&principal.request_id))?
            .map(Some)
            .map_err(|failure| match failure {
                ModelRateLimitFailure::Dependency => {
                    model_rate_limit_internal(&principal.request_id)
                }
                ModelRateLimitFailure::TotalExceeded => {
                    model_rate_limit_total_exceeded(&config, &principal.request_id)
                }
                ModelRateLimitFailure::SuccessExceeded => {
                    model_rate_limit_success_exceeded(&config, &principal.request_id)
                }
            });
        }

        self.memory_rate_limits
            .check(principal.user_id, &config)
            .map(Some)
            .map_err(|failure| match failure {
                ModelRateLimitFailure::Dependency => {
                    model_rate_limit_internal(&principal.request_id)
                }
                ModelRateLimitFailure::TotalExceeded => {
                    model_rate_limit_total_exceeded(&config, &principal.request_id)
                }
                ModelRateLimitFailure::SuccessExceeded => {
                    model_rate_limit_success_exceeded(&config, &principal.request_id)
                }
            })
    }

    async fn select_request(
        &self,
        context: &RelayRequestContext,
        principal: &RelayPrincipal,
    ) -> Result<SelectedRelay, RelayAuth> {
        let model = context
            .model
            .as_deref()
            .filter(|model| !model.is_empty())
            .ok_or_else(|| {
                coded_rejection(
                    StatusCode::BAD_REQUEST,
                    "模型名称不能为空",
                    "",
                    &principal.request_id,
                )
            })?;
        if principal.model_limits_enabled && !model_allowed(model, &principal.model_limits) {
            return Err(coded_rejection(
                StatusCode::FORBIDDEN,
                &format!("该令牌无权访问模型 {model}"),
                "",
                &principal.request_id,
            ));
        }

        let options = options(
            &self.pg,
            &[
                "GroupRatio",
                "GroupGroupRatio",
                "ModelPrice",
                "QuotaPerUnit",
            ],
        )
        .await
        .map_err(|()| storage_rejection(&principal.request_id))?;
        let using_group = principal.using_group.clone();
        let mut rows = select_channel(&self.pg, principal.specific_channel_id, &using_group, model)
            .await
            .map_err(|()| storage_rejection(&principal.request_id))?;
        if principal.specific_channel_id.is_none() && rows.len() > 1 {
            let first_priority = rows[0]
                .try_get::<i64, _>("ability_priority")
                .map_err(|_| storage_rejection(&principal.request_id))?;
            let second_priority = rows[1]
                .try_get::<i64, _>("ability_priority")
                .map_err(|_| storage_rejection(&principal.request_id))?;
            if first_priority == second_priority {
                return Err(coded_rejection(
                    StatusCode::SERVICE_UNAVAILABLE,
                    "multiple top-priority channels require the Go weighted selector",
                    "channel_not_found",
                    &principal.request_id,
                ));
            }
        }
        let row = rows.drain(..).next().ok_or_else(|| {
            coded_rejection(
                StatusCode::SERVICE_UNAVAILABLE,
                &format!("当前分组 {using_group} 下对于模型 {model} 无可用渠道"),
                "model_not_found",
                &principal.request_id,
            )
        })?;

        let channel_type = row
            .try_get::<i64, _>("channel_type")
            .map_err(|_| storage_rejection(&principal.request_id))?;
        if !channel_supports_protocol(channel_type, context.protocol) {
            return Err(coded_rejection(
                StatusCode::SERVICE_UNAVAILABLE,
                "selected channel protocol adapter is not yet available in Rust",
                "channel_not_found",
                &principal.request_id,
            ));
        }
        let param_override = row
            .try_get::<Option<String>, _>("param_override")
            .map_err(|_| storage_rejection(&principal.request_id))?
            .unwrap_or_default();
        let header_override = row
            .try_get::<Option<String>, _>("header_override")
            .map_err(|_| storage_rejection(&principal.request_id))?
            .unwrap_or_default();
        if !empty_json_object(&param_override) || !empty_json_object(&header_override) {
            return Err(coded_rejection(
                StatusCode::SERVICE_UNAVAILABLE,
                "selected channel override adapter is not yet available in Rust",
                "channel_not_found",
                &principal.request_id,
            ));
        }
        let status_code_mapping = row
            .try_get::<Option<String>, _>("status_code_mapping")
            .map_err(|_| storage_rejection(&principal.request_id))?
            .unwrap_or_default();

        let model_mapping = row
            .try_get::<Option<String>, _>("model_mapping")
            .map_err(|_| storage_rejection(&principal.request_id))?
            .unwrap_or_default();
        let upstream_model = mapped_model(model, &model_mapping).map_err(|message| {
            coded_rejection(
                StatusCode::BAD_REQUEST,
                message,
                "channel_model_mapped_error",
                &principal.request_id,
            )
        })?;
        let trust_discount_ratio = trust_discount_ratio(&self.pg, principal).await;
        let pricing = fixed_price(
            model,
            &principal.user_group,
            &using_group,
            trust_discount_ratio,
            &options,
        )
        .map_err(|message| {
            coded_rejection(
                StatusCode::BAD_REQUEST,
                message,
                "model_price_error",
                &principal.request_id,
            )
        })?;
        let raw_key = row
            .try_get::<String, _>("channel_key")
            .map_err(|_| storage_rejection(&principal.request_id))?;
        let keys = raw_key
            .lines()
            .map(str::trim)
            .filter(|key| !key.is_empty())
            .collect::<Vec<_>>();
        if keys.len() != 1 {
            return Err(coded_rejection(
                StatusCode::SERVICE_UNAVAILABLE,
                "multi-key channel selection is not yet available in Rust",
                "channel_not_found",
                &principal.request_id,
            ));
        }
        let api_key = keys[0].to_owned();
        let mut base_url = row
            .try_get::<Option<String>, _>("base_url")
            .map_err(|_| storage_rejection(&principal.request_id))?
            .unwrap_or_default();
        if base_url.trim().is_empty() && channel_type == 1 {
            base_url = "https://api.openai.com".to_owned();
        }
        if api_key.is_empty() || base_url.trim().is_empty() {
            return Err(coded_rejection(
                StatusCode::SERVICE_UNAVAILABLE,
                "selected channel has no usable server credential",
                "channel_not_found",
                &principal.request_id,
            ));
        }

        Ok(SelectedRelay {
            channel_id: row
                .try_get("channel_id")
                .map_err(|_| storage_rejection(&principal.request_id))?,
            base_url,
            api_key,
            origin_model: model.to_owned(),
            upstream_model,
            using_group,
            request_path: context.path.clone(),
            status_code_mapping: parsed_status_code_mapping(&status_code_mapping),
            pricing,
            started_at: Instant::now(),
        })
    }

    async fn execute_selected(
        &self,
        context: &RelayRequestContext,
        request: Request,
    ) -> Result<Response, RelayFailure> {
        let principal = request
            .extensions()
            .get::<RelayPrincipal>()
            .cloned()
            .ok_or_else(|| {
                RelayFailure::internal("authenticated principal is missing", "unknown")
            })?;
        let selected = request
            .extensions()
            .get::<SelectedRelay>()
            .cloned()
            .ok_or_else(|| {
                RelayFailure::internal("selected channel is missing", &principal.request_id)
            })?;
        let model_rate_limit_commit = request.extensions().get::<ModelRateLimitCommit>().cloned();
        let (parts, body) = request.into_parts();
        let body = to_bytes(body, MAX_RELAY_BODY_BYTES)
            .await
            .map_err(|_| RelayFailure::payload_too_large(&principal.request_id))?;
        let outbound_body = mapped_body(&body, &selected.origin_model, &selected.upstream_model)
            .map_err(|message| RelayFailure::bad_request(message, &principal.request_id))?;

        let mut tx = self
            .pg
            .begin()
            .await
            .map_err(|_| RelayFailure::storage(&principal.request_id))?;
        lock_and_revalidate(&mut tx, &principal, &selected)
            .await
            .map_err(|failure| failure.with_request_id(&principal.request_id))?;

        let url = upstream_url(&selected.base_url, &context.path).map_err(|_| {
            RelayFailure::upstream("invalid upstream target", &principal.request_id)
        })?;
        let mut upstream_request = self
            .client
            .post(url)
            .header(
                header::AUTHORIZATION,
                format!("Bearer {}", selected.api_key),
            )
            .body(outbound_body);
        for name in [header::ACCEPT, header::CONTENT_TYPE] {
            if let Some(value) = parts.headers.get(&name) {
                upstream_request = upstream_request.header(name, value);
            }
        }
        let upstream = tokio::time::timeout(self.response_header_timeout, upstream_request.send())
            .await
            .map_err(|_| {
                RelayFailure::upstream("upstream response timed out", &principal.request_id)
            })?
            .map_err(|_| {
                RelayFailure::upstream("upstream request failed", &principal.request_id)
            })?;
        let status = upstream.status();
        let headers = upstream.headers().clone();
        let response_body = read_bounded(upstream, MAX_RELAY_BODY_BYTES)
            .await
            .map_err(|message| RelayFailure::upstream(message, &principal.request_id))?;

        if status != StatusCode::OK {
            tx.rollback().await.ok();
            return Ok(upstream_error_response(
                mapped_status_code(status, &selected.status_code_mapping),
                headers,
                &response_body,
                status,
                &principal.request_id,
            ));
        }
        let usage = usage_from_response(&response_body);
        let actual_quota = if usage.billable() {
            selected.pricing.settlement_quota
        } else {
            0
        };
        settle_success(
            &mut tx,
            &principal,
            &selected,
            &usage,
            actual_quota,
            upstream_request_id(&headers),
        )
        .await
        .map_err(|_| RelayFailure::storage(&principal.request_id))?;
        tx.commit()
            .await
            .map_err(|_| RelayFailure::storage(&principal.request_id))?;
        if let Some(commit) = model_rate_limit_commit.as_ref() {
            self.commit_model_rate_limit_success(commit).await;
        }
        Ok(upstream_response(status, headers, response_body))
    }

    async fn commit_model_rate_limit_success(&self, commit: &ModelRateLimitCommit) {
        match commit.backend {
            ModelRateLimitBackend::Memory => {
                self.memory_rate_limits.commit_success(commit);
            }
            ModelRateLimitBackend::Valkey => {
                let Some(valkey) = self.valkey.as_ref() else {
                    return;
                };
                let _ = tokio::time::timeout(
                    self.rate_limit_timeout,
                    record_valkey_model_rate_limit_success(valkey, commit),
                )
                .await;
            }
        }
    }
}

/// Builds the behaviorally approved PostgreSQL relay-misc route slice.
///
/// The normal listener must still opt in explicitly after the remaining
/// fail-closed feature gates are implemented and approved.
pub fn relay_misc_postgres_router(service: PgRelayMiscService) -> Router {
    routes(RelayMiscHttpState::new(Arc::new(service)))
}

#[async_trait]
impl RelayMiscService for PgRelayMiscService {
    async fn system_performance(&self, _: &Request) -> RelayAuth {
        self.performance_gate().await
    }

    async fn authorize(&self, _: &Request) -> RelayAuth {
        RelayAuth::Rejected {
            status: StatusCode::SERVICE_UNAVAILABLE,
            message: "mutable relay authorization hook is required".to_owned(),
        }
    }

    async fn authorize_prepared(&self, request: &mut Request) -> RelayAuth {
        // Extract every request-owned value before the first await.  The
        // async-trait future is Send, while axum's `Request<Body>` is not
        // Sync; retaining even a shared borrow across authentication would
        // therefore reject the production PostgreSQL adapter at compile time.
        let input = {
            let request_ref: &Request = &*request;
            match relay_authentication_input(request_ref) {
                Ok(input) => input,
                Err(failure) => return failure,
            }
        };
        match self.authenticate_request(input).await {
            Ok(principal) => {
                request.extensions_mut().insert(principal);
                RelayAuth::Authorized
            }
            Err(failure) => failure,
        }
    }

    async fn model_rate_limit_prepared(&self, request: &mut Request) -> RelayAuth {
        let Some(principal) = request.extensions().get::<RelayPrincipal>().cloned() else {
            return storage_rejection(&request_id(request));
        };
        match self.rate_limit_gate(&principal).await {
            Ok(commit) => {
                if let Some(commit) = commit {
                    request.extensions_mut().insert(commit);
                }
                RelayAuth::Authorized
            }
            Err(failure) => failure,
        }
    }

    async fn distribute(&self, _: &RelayRequestContext, _: &Request) -> RelayAuth {
        RelayAuth::Rejected {
            status: StatusCode::SERVICE_UNAVAILABLE,
            message: "mutable relay distribution hook is required".to_owned(),
        }
    }

    async fn distribute_prepared(
        &self,
        context: &RelayRequestContext,
        request: &mut Request,
    ) -> RelayAuth {
        let Some(principal) = request.extensions().get::<RelayPrincipal>().cloned() else {
            return storage_rejection(&request_id(request));
        };
        match self.select_request(context, &principal).await {
            Ok(selected) => {
                request.extensions_mut().insert(selected);
                RelayAuth::Authorized
            }
            Err(failure) => failure,
        }
    }

    async fn relay(&self, _: RelayProtocol, _: Request) -> Response {
        RelayFailure::internal("composite relay hook is required", "unknown").into_response()
    }

    async fn execute_prepared(&self, context: &RelayRequestContext, request: Request) -> Response {
        match self.execute_selected(context, request).await {
            Ok(response) => response,
            Err(failure) => failure.into_response(),
        }
    }
}

#[derive(Clone)]
struct RelayPrincipal {
    token_id: i64,
    token_key: String,
    token_name: String,
    user_id: i64,
    username: String,
    role: i64,
    created_at: i64,
    last_api_activity_at: i64,
    trust_level_override: Option<i64>,
    user_group: String,
    token_group: String,
    using_group: String,
    record_ip_log: bool,
    model_limits_enabled: bool,
    model_limits: String,
    specific_channel_id: Option<i64>,
    request_id: String,
    client_ip: IpAddr,
}

#[derive(Clone, Copy)]
enum ModelRateLimitBackend {
    Memory,
    Valkey,
}

#[derive(Clone)]
struct ModelRateLimitCommit {
    backend: ModelRateLimitBackend,
    user_id: i64,
    duration_minutes: i64,
    duration_seconds: i64,
    success_max_count: i64,
}

struct ModelRateLimitConfig {
    enabled: bool,
    duration_minutes: i64,
    total_max_count: i64,
    success_max_count: i64,
}

#[derive(Debug)]
enum ModelRateLimitFailure {
    Dependency,
    TotalExceeded,
    SuccessExceeded,
}

#[derive(Default)]
struct MemoryModelRateLimits {
    queues: Mutex<HashMap<String, VecDeque<i64>>>,
}

impl MemoryModelRateLimits {
    fn check(
        &self,
        user_id: i64,
        config: &ModelRateLimitConfig,
    ) -> Result<ModelRateLimitCommit, ModelRateLimitFailure> {
        let now = unix_now();
        let duration_seconds = config.duration_minutes.saturating_mul(60);
        let mut queues = self
            .queues
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        if config.total_max_count > 0
            && !memory_rate_limit_request(
                &mut queues,
                &format!("MRRL{user_id}"),
                config.total_max_count,
                duration_seconds,
                now,
            )
        {
            return Err(ModelRateLimitFailure::TotalExceeded);
        }
        if config.success_max_count > 0
            && !memory_rate_limit_check(
                &queues,
                &format!("MRRLS{user_id}"),
                config.success_max_count,
                duration_seconds,
                now,
            )
        {
            return Err(ModelRateLimitFailure::SuccessExceeded);
        }
        Ok(ModelRateLimitCommit {
            backend: ModelRateLimitBackend::Memory,
            user_id,
            duration_minutes: config.duration_minutes,
            duration_seconds,
            success_max_count: config.success_max_count,
        })
    }

    fn commit_success(&self, commit: &ModelRateLimitCommit) {
        if commit.success_max_count <= 0 {
            return;
        }
        let mut queues = self
            .queues
            .lock()
            .unwrap_or_else(std::sync::PoisonError::into_inner);
        let _ = memory_rate_limit_request(
            &mut queues,
            &format!("MRRLS{}", commit.user_id),
            commit.success_max_count,
            commit.duration_seconds,
            unix_now(),
        );
    }
}

#[derive(Clone)]
struct SelectedRelay {
    channel_id: i64,
    base_url: String,
    api_key: String,
    origin_model: String,
    upstream_model: String,
    using_group: String,
    request_path: String,
    status_code_mapping: HashMap<StatusCode, StatusCode>,
    pricing: FixedPrice,
    started_at: Instant,
}

#[derive(Clone, Copy)]
struct FixedPrice {
    model_price: f64,
    group_ratio: f64,
    user_group_ratio: f64,
    preconsume_quota: i64,
    settlement_quota: i64,
}

struct TokenCredential {
    key: String,
    channel_suffix: Option<String>,
}

struct RelayAuthenticationInput {
    request_id: String,
    client_ip: IpAddr,
    authorization: Option<String>,
    credential: TokenCredential,
}

#[derive(Default)]
struct Usage {
    prompt_tokens: i64,
    completion_tokens: i64,
    total_tokens: i64,
}

impl Usage {
    fn billable(&self) -> bool {
        self.prompt_tokens > 0 || self.completion_tokens > 0 || self.total_tokens > 0
    }
}

async fn options(pg: &PgPool, keys: &[&str]) -> Result<HashMap<String, String>, ()> {
    let keys = keys.iter().map(|key| (*key).to_owned()).collect::<Vec<_>>();
    let rows = sqlx::query("SELECT key,value FROM options WHERE key=ANY($1)")
        .bind(keys)
        .fetch_all(pg)
        .await
        .map_err(|_| ())?;
    Ok(rows
        .into_iter()
        .filter_map(|row| {
            Some((
                row.try_get::<String, _>("key").ok()?,
                row.try_get::<Option<String>, _>("value").ok()??,
            ))
        })
        .collect())
}

async fn model_rate_limit_config(
    pg: &PgPool,
    principal: &RelayPrincipal,
) -> Result<ModelRateLimitConfig, ()> {
    let values = options(
        pg,
        &[
            "ModelRequestRateLimitEnabled",
            "ModelRequestRateLimitDurationMinutes",
            "ModelRequestRateLimitCount",
            "ModelRequestRateLimitSuccessCount",
            "ModelRequestRateLimitGroup",
        ],
    )
    .await?;
    let mut config = ModelRateLimitConfig {
        enabled: values
            .get("ModelRequestRateLimitEnabled")
            .is_some_and(|value| parse_bool(value)),
        duration_minutes: integer_option(&values, "ModelRequestRateLimitDurationMinutes", 1),
        total_max_count: integer_option(&values, "ModelRequestRateLimitCount", 0),
        success_max_count: integer_option(&values, "ModelRequestRateLimitSuccessCount", 1_000),
    };
    let group = if principal.token_group.is_empty() {
        principal.user_group.as_str()
    } else {
        principal.token_group.as_str()
    };
    let group_limits = values
        .get("ModelRequestRateLimitGroup")
        .and_then(|raw| serde_json::from_str::<HashMap<String, [i64; 2]>>(raw).ok())
        .unwrap_or_default();
    if let Some([total, success]) = group_limits.get(group) {
        config.total_max_count = *total;
        config.success_max_count = *success;
    }
    Ok(config)
}

fn integer_option(values: &HashMap<String, String>, key: &str, default: i64) -> i64 {
    values
        .get(key)
        .map_or(default, |value| value.parse::<i64>().unwrap_or(0))
}

fn memory_rate_limit_request(
    queues: &mut HashMap<String, VecDeque<i64>>,
    key: &str,
    max_count: i64,
    duration_seconds: i64,
    now: i64,
) -> bool {
    if max_count == 0 {
        return true;
    }
    let max_count = usize::try_from(max_count).unwrap_or(usize::MAX);
    let queue = queues.entry(key.to_owned()).or_default();
    if queue.len() < max_count {
        queue.push_back(now);
        return true;
    }
    if queue
        .front()
        .is_some_and(|oldest| now.saturating_sub(*oldest) >= duration_seconds)
    {
        queue.pop_front();
        queue.push_back(now);
        return true;
    }
    false
}

fn memory_rate_limit_check(
    queues: &HashMap<String, VecDeque<i64>>,
    key: &str,
    max_count: i64,
    duration_seconds: i64,
    now: i64,
) -> bool {
    if max_count == 0 {
        return true;
    }
    let max_count = usize::try_from(max_count).unwrap_or(usize::MAX);
    let Some(queue) = queues.get(key) else {
        return true;
    };
    queue.len() < max_count
        || queue
            .front()
            .is_some_and(|oldest| now.saturating_sub(*oldest) >= duration_seconds)
}

async fn check_valkey_model_rate_limit(
    valkey: &redis::Client,
    user_id: i64,
    config: &ModelRateLimitConfig,
) -> Result<ModelRateLimitCommit, ModelRateLimitFailure> {
    let duration_seconds = config.duration_minutes.saturating_mul(60);
    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .map_err(|_| ModelRateLimitFailure::Dependency)?;
    let success_key = format!("rateLimit:MRRLS:{user_id}");
    if !valkey_success_rate_limit_allows(
        &mut connection,
        &success_key,
        config.success_max_count,
        duration_seconds,
        config.duration_minutes,
    )
    .await?
    {
        return Err(ModelRateLimitFailure::SuccessExceeded);
    }
    if config.total_max_count > 0 {
        let allowed = redis::Script::new(MODEL_RATE_LIMIT_TOKEN_BUCKET)
            .key(format!("rateLimit:{user_id}"))
            .arg(duration_seconds)
            .arg(config.total_max_count)
            .arg(config.total_max_count.saturating_mul(duration_seconds))
            .invoke_async::<i64>(&mut connection)
            .await
            .map_err(|_| ModelRateLimitFailure::Dependency)?;
        if allowed != 1 {
            return Err(ModelRateLimitFailure::TotalExceeded);
        }
    }
    Ok(ModelRateLimitCommit {
        backend: ModelRateLimitBackend::Valkey,
        user_id,
        duration_minutes: config.duration_minutes,
        duration_seconds,
        success_max_count: config.success_max_count,
    })
}

async fn valkey_success_rate_limit_allows(
    connection: &mut redis::aio::MultiplexedConnection,
    key: &str,
    max_count: i64,
    duration_seconds: i64,
    duration_minutes: i64,
) -> Result<bool, ModelRateLimitFailure> {
    if max_count == 0 {
        return Ok(true);
    }
    let length = redis::cmd("LLEN")
        .arg(key)
        .query_async::<i64>(connection)
        .await
        .map_err(|_| ModelRateLimitFailure::Dependency)?;
    if length < max_count {
        return Ok(true);
    }
    let oldest = redis::cmd("LINDEX")
        .arg(key)
        .arg(-1)
        .query_async::<String>(connection)
        .await
        .map_err(|_| ModelRateLimitFailure::Dependency)?;
    let oldest = chrono::DateTime::parse_from_rfc3339(&oldest)
        .map_err(|_| ModelRateLimitFailure::Dependency)?;
    let elapsed_seconds = chrono::Utc::now()
        .timestamp_millis()
        .saturating_sub(oldest.timestamp_millis())
        / 1_000;
    if elapsed_seconds < duration_seconds {
        let _: Result<bool, _> = redis::cmd("EXPIRE")
            .arg(key)
            .arg(duration_minutes.saturating_mul(60))
            .query_async(connection)
            .await;
        return Ok(false);
    }
    Ok(true)
}

async fn record_valkey_model_rate_limit_success(
    valkey: &redis::Client,
    commit: &ModelRateLimitCommit,
) {
    if commit.success_max_count <= 0 {
        return;
    }
    let Ok(mut connection) = valkey.get_multiplexed_async_connection().await else {
        return;
    };
    let now = chrono::Utc::now().to_rfc3339_opts(chrono::SecondsFormat::Millis, true);
    let key = format!("rateLimit:MRRLS:{}", commit.user_id);
    let _: Result<(), _> = redis::pipe()
        .cmd("LPUSH")
        .arg(&key)
        .arg(now)
        .ignore()
        .cmd("LTRIM")
        .arg(&key)
        .arg(0)
        .arg(commit.success_max_count.saturating_sub(1))
        .ignore()
        .cmd("EXPIRE")
        .arg(&key)
        .arg(commit.duration_minutes.saturating_mul(60))
        .ignore()
        .query_async(&mut connection)
        .await;
}

fn model_rate_limit_internal(request_id: &str) -> RelayAuth {
    RelayAuth::RejectedOpenAi {
        status: StatusCode::INTERNAL_SERVER_ERROR,
        message: message_with_request_id("rate_limit_check_failed", request_id),
        code: "invalid_request".to_owned(),
    }
}

fn model_rate_limit_total_exceeded(config: &ModelRateLimitConfig, request_id: &str) -> RelayAuth {
    RelayAuth::RejectedOpenAi {
        status: StatusCode::TOO_MANY_REQUESTS,
        message: message_with_request_id(
            &format!(
                "您已达到总请求数限制：{}分钟内最多请求{}次，包括失败次数，请检查您的请求是否正确",
                config.duration_minutes, config.total_max_count
            ),
            request_id,
        ),
        code: "invalid_request".to_owned(),
    }
}

fn model_rate_limit_success_exceeded(config: &ModelRateLimitConfig, request_id: &str) -> RelayAuth {
    RelayAuth::RejectedOpenAi {
        status: StatusCode::TOO_MANY_REQUESTS,
        message: message_with_request_id(
            &format!(
                "您已达到请求数限制：{}分钟内最多请求{}次",
                config.duration_minutes, config.success_max_count
            ),
            request_id,
        ),
        code: "invalid_request".to_owned(),
    }
}

async fn select_channel(
    pg: &PgPool,
    specific_channel_id: Option<i64>,
    group: &str,
    model: &str,
) -> Result<Vec<PgRow>, ()> {
    let columns = r#"c.id AS channel_id,
        COALESCE(c.type,0) AS channel_type, c.key AS channel_key, c.base_url,
        c.model_mapping, c.param_override, c.header_override, c.status_code_mapping"#;
    if let Some(channel_id) = specific_channel_id {
        sqlx::query(&format!(
            "SELECT {columns} FROM channels c WHERE c.id=$1 AND COALESCE(c.status,1)=1"
        ))
        .bind(channel_id)
        .fetch_all(pg)
        .await
        .map_err(|_| ())
    } else {
        sqlx::query(&format!(
            r#"SELECT {columns}, COALESCE(a.priority,0) AS ability_priority
                 FROM abilities a
                 JOIN channels c ON c.id=a.channel_id
                WHERE a."group"=$1 AND a.model=$2 AND COALESCE(a.enabled,TRUE)
                  AND COALESCE(c.status,1)=1
                ORDER BY COALESCE(a.priority,0) DESC, c.id
                LIMIT 2"#
        ))
        .bind(group)
        .bind(model)
        .fetch_all(pg)
        .await
        .map_err(|_| ())
    }
}

fn selected_group(
    principal: &RelayPrincipal,
    options: &HashMap<String, String>,
) -> Result<String, RelayAuth> {
    let requested = principal.token_group.trim();
    if requested == "auto" {
        return Err(coded_rejection(
            StatusCode::SERVICE_UNAVAILABLE,
            "token auto-group selection is not yet available in Rust",
            "channel_not_found",
            &principal.request_id,
        ));
    }
    let group = if requested.is_empty() {
        principal.user_group.as_str()
    } else {
        requested
    };
    let usable = json_object(options.get("UserUsableGroups"));
    if group != principal.user_group && !usable.contains_key(group) {
        return Err(coded_rejection(
            StatusCode::FORBIDDEN,
            &format!("无权访问 {group} 分组"),
            "",
            &principal.request_id,
        ));
    }
    let ratios = json_object(options.get("GroupRatio"));
    if group != "default" && !ratios.contains_key(group) {
        return Err(coded_rejection(
            StatusCode::FORBIDDEN,
            &format!("分组 {group} 已被弃用"),
            "",
            &principal.request_id,
        ));
    }
    Ok(group.to_owned())
}

fn fixed_price(
    model: &str,
    user_group: &str,
    using_group: &str,
    trust_discount_ratio: f64,
    options: &HashMap<String, String>,
) -> Result<FixedPrice, &'static str> {
    let prices = json_object(options.get("ModelPrice"));
    let model_price = prices
        .get(matching_model_name(model))
        .and_then(value_as_f64)
        .ok_or("model fixed price is not configured for the proven Rust relay path")?;
    let ordinary_ratios = json_object(options.get("GroupRatio"));
    let special_ratios = json_object(options.get("GroupGroupRatio"));
    let group_special_ratio = special_ratios
        .get(user_group)
        .and_then(Value::as_object)
        .and_then(|ratios| ratios.get(using_group))
        .and_then(value_as_f64);
    let base_group_ratio = group_special_ratio
        .or_else(|| ordinary_ratios.get(using_group).and_then(value_as_f64))
        .or((using_group == "default").then_some(1.0))
        .ok_or("selected group ratio is not configured")?;
    let group_ratio = base_group_ratio * trust_discount_ratio;
    let quota_per_unit = options
        .get("QuotaPerUnit")
        .and_then(|value| value.parse::<f64>().ok())
        .unwrap_or(DEFAULT_QUOTA_PER_UNIT);
    let raw_quota = model_price * quota_per_unit * group_ratio;
    let preconsume_quota = quota_from_float_strict(raw_quota)?;
    let settlement_quota = quota_round_strict(raw_quota)?;
    Ok(FixedPrice {
        model_price,
        group_ratio,
        user_group_ratio: group_special_ratio.unwrap_or(-1.0),
        preconsume_quota,
        settlement_quota,
    })
}

async fn trust_discount_ratio(pg: &PgPool, principal: &RelayPrincipal) -> f64 {
    if principal.role >= 10 || principal.trust_level_override.is_some() {
        return trust_discount_from_facts(TrustFacts {
            role: principal.role,
            override_level: principal.trust_level_override,
            paid_amount_micros: 0,
            activation_complete: false,
            created_at: principal.created_at,
            last_api_activity_at: principal.last_api_activity_at,
            last_paid_complete_at: 0,
            now: unix_now(),
        });
    }
    let aggregate = sqlx::query(
        r#"SELECT
             COALESCE(SUM(CASE WHEN COALESCE(settled_amount_micros,0)>0
                               THEN settled_amount_micros ELSE 0 END),0)::BIGINT
               AS settled_amount_micros,
             COALESCE(SUM(CASE WHEN COALESCE(settled_amount_micros,0)=0
                               THEN COALESCE(money,0) ELSE 0 END),0)::DOUBLE PRECISION
               AS legacy_paid_amount,
             COALESCE(MAX(CASE WHEN COALESCE(complete_time,0)>0
                               THEN complete_time ELSE COALESCE(create_time,0) END),0)::BIGINT
               AS last_paid_complete_at,
             COUNT(*)::BIGINT AS activation_complete_rows
           FROM top_ups
          WHERE user_id=$1 AND status='success'
            AND (COALESCE(settled_amount_micros,0)>0
                 OR (COALESCE(settled_amount_micros,0)=0 AND COALESCE(money,0)>0))
            AND COALESCE(payment_method,'')<>'balance'
            AND COALESCE(payment_provider,'')<>'balance'
            AND (
              COALESCE(credited_quota,0)>0
              OR (
                COALESCE(amount,0)>0 AND (
                  COALESCE(payment_provider,'') IN
                    ('epay','stripe','creem','waffo','waffo_pancake')
                  OR (
                    COALESCE(payment_provider,'')=''
                    AND COALESCE(payment_method,'') IN
                      ('stripe','creem','waffo','waffo_pancake','alipay','wxpay')
                  )
                )
              )
            )"#,
    )
    .bind(principal.user_id)
    .fetch_one(pg)
    .await;
    let Ok(aggregate) = aggregate else {
        // Current Go logs trust lookup failures and continues without a
        // discount instead of rejecting an otherwise valid relay request.
        return 1.0;
    };
    let settled_amount_micros = aggregate
        .try_get::<i64, _>("settled_amount_micros")
        .unwrap_or(0);
    let legacy_paid_amount = aggregate
        .try_get::<f64, _>("legacy_paid_amount")
        .unwrap_or(0.0);
    let legacy_amount_micros = if legacy_paid_amount.is_finite() {
        (legacy_paid_amount * 1_000_000.0).round() as i64
    } else {
        0
    };
    trust_discount_from_facts(TrustFacts {
        role: principal.role,
        override_level: None,
        paid_amount_micros: settled_amount_micros.saturating_add(legacy_amount_micros),
        activation_complete: aggregate
            .try_get::<i64, _>("activation_complete_rows")
            .is_ok_and(|rows| rows > 0),
        created_at: principal.created_at,
        last_api_activity_at: principal.last_api_activity_at,
        last_paid_complete_at: aggregate
            .try_get::<i64, _>("last_paid_complete_at")
            .unwrap_or(0),
        now: unix_now(),
    })
}

#[derive(Clone, Copy)]
struct TrustFacts {
    role: i64,
    override_level: Option<i64>,
    paid_amount_micros: i64,
    activation_complete: bool,
    created_at: i64,
    last_api_activity_at: i64,
    last_paid_complete_at: i64,
    now: i64,
}

fn trust_discount_from_facts(facts: TrustFacts) -> f64 {
    const DISCOUNTS: [f64; 5] = [1.0, 1.0, 0.97, 0.94, 0.90];
    const DECAY_PERIOD_SECONDS: i64 = 90 * 24 * 60 * 60;
    if facts.role >= 10 {
        return DISCOUNTS[4];
    }
    if let Some(level) = facts.override_level {
        return usize::try_from(level)
            .ok()
            .and_then(|level| DISCOUNTS.get(level))
            .copied()
            .unwrap_or(DISCOUNTS[0]);
    }
    let automatic_level = if !facts.activation_complete {
        0
    } else if facts.paid_amount_micros >= 2_000_000_000 {
        4
    } else if facts.paid_amount_micros >= 500_000_000 {
        3
    } else if facts.paid_amount_micros >= 100_000_000 {
        2
    } else {
        1
    };
    let activity_anchor = facts
        .created_at
        .max(facts.last_api_activity_at)
        .max(facts.last_paid_complete_at);
    let decay_steps = if automatic_level > 0 && activity_anchor > 0 && facts.now > activity_anchor {
        ((facts.now - activity_anchor) / DECAY_PERIOD_SECONDS).min(automatic_level - 1)
    } else {
        0
    };
    DISCOUNTS[usize::try_from(automatic_level - decay_steps).unwrap_or(0)]
}

fn quota_from_float_strict(value: f64) -> Result<i64, &'static str> {
    if !value.is_finite() || !(0.0..MAX_QUOTA).contains(&value) {
        return Err("quota conversion is outside the supported range");
    }
    Ok(value.trunc() as i64)
}

fn quota_round_strict(value: f64) -> Result<i64, &'static str> {
    if !value.is_finite() || !(0.0..MAX_QUOTA).contains(&value) {
        return Err("quota conversion is outside the supported range");
    }
    Ok(value.round() as i64)
}

fn json_object(value: Option<&String>) -> serde_json::Map<String, Value> {
    value
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .and_then(|value| value.as_object().cloned())
        .unwrap_or_default()
}

fn value_as_f64(value: &Value) -> Option<f64> {
    value
        .as_f64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
}

fn empty_json_object(value: &str) -> bool {
    let value = value.trim();
    value.is_empty() || value == "{}" || value == "null"
}

fn parsed_status_code_mapping(raw: &str) -> HashMap<StatusCode, StatusCode> {
    serde_json::from_str::<Value>(raw)
        .ok()
        .and_then(|value| value.as_object().cloned())
        .into_iter()
        .flat_map(|mapping| mapping.into_iter())
        .filter_map(|(source, target)| {
            let source = source
                .parse::<u16>()
                .ok()
                .and_then(|code| StatusCode::from_u16(code).ok())?;
            Some((source, status_code_mapping_value(&target)?))
        })
        .collect()
}

fn status_code_mapping_value(value: &Value) -> Option<StatusCode> {
    let code = match value {
        Value::String(value) => value.parse::<i64>().ok()?,
        Value::Number(value) => value.as_i64().or_else(|| {
            let value = value.as_f64()?;
            (value.is_finite() && value.fract() == 0.0).then_some(value as i64)
        })?,
        _ => return None,
    };
    u16::try_from(code)
        .ok()
        .and_then(|code| StatusCode::from_u16(code).ok())
}

fn mapped_status_code(status: StatusCode, mapping: &HashMap<StatusCode, StatusCode>) -> StatusCode {
    if status == StatusCode::OK {
        status
    } else {
        mapping.get(&status).copied().unwrap_or(status)
    }
}

fn model_allowed(model: &str, raw_limits: &str) -> bool {
    let expected = matching_model_name(model);
    raw_limits
        .split(',')
        .map(str::trim)
        .filter(|limit| !limit.is_empty())
        .any(|limit| limit == expected)
}

fn matching_model_name(model: &str) -> &str {
    if model.starts_with("gpt-4-gizmo") {
        "gpt-4-gizmo-*"
    } else if model.starts_with("gpt-4o-gizmo") {
        "gpt-4o-gizmo-*"
    } else if model.starts_with("gemini-2.5-flash-lite") && model.contains("-thinking-") {
        "gemini-2.5-flash-lite-thinking-*"
    } else if model.starts_with("gemini-2.5-flash") && model.contains("-thinking-") {
        "gemini-2.5-flash-thinking-*"
    } else if model.starts_with("gemini-2.5-pro") && model.contains("-thinking-") {
        "gemini-2.5-pro-thinking-*"
    } else {
        model
    }
}

fn mapped_model(origin: &str, raw_mapping: &str) -> Result<String, &'static str> {
    if empty_json_object(raw_mapping) {
        return Ok(origin.to_owned());
    }
    let mapping = serde_json::from_str::<HashMap<String, String>>(raw_mapping)
        .map_err(|_| "unmarshal_model_mapping_failed")?;
    let mut current = origin.to_owned();
    let mut seen = HashSet::from([current.clone()]);
    while let Some(next) = mapping.get(&current) {
        if !seen.insert(next.clone()) {
            return Err("model_mapping_contains_cycle");
        }
        current = next.clone();
    }
    Ok(current)
}

fn mapped_body(body: &[u8], origin: &str, upstream: &str) -> Result<Vec<u8>, &'static str> {
    let mut value =
        serde_json::from_slice::<Value>(body).map_err(|_| "invalid JSON request body")?;
    if upstream != origin {
        let object = value.as_object_mut().ok_or("invalid JSON request body")?;
        object.insert("model".to_owned(), Value::String(upstream.to_owned()));
    }
    serde_json::to_vec(&value).map_err(|_| "failed to encode provider request")
}

fn channel_supports_protocol(channel_type: i64, protocol: RelayProtocol) -> bool {
    protocol == RelayProtocol::Embedding && channel_type == 1
}

async fn lock_and_revalidate(
    tx: &mut Transaction<'_, Postgres>,
    principal: &RelayPrincipal,
    selected: &SelectedRelay,
) -> Result<(), RelayFailure> {
    let row = sqlx::query(
        r#"SELECT COALESCE(t.status,1) AS token_status,
                  COALESCE(t.expired_time,-1) AS expired_time,
                  COALESCE(t.remain_quota,0) AS remain_quota,
                  COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,
                  COALESCE(u.status,1) AS user_status,
                  COALESCE(u.quota,0) AS user_quota,
                  COALESCE(c.status,1) AS channel_status
             FROM tokens t
             JOIN users u ON u.id=t.user_id
             JOIN channels c ON c.id=$3
            WHERE t.id=$1 AND t.user_id=$2 AND t.key=$4
              AND t.deleted_at IS NULL AND u.deleted_at IS NULL
            FOR UPDATE OF t,u,c"#,
    )
    .bind(principal.token_id)
    .bind(principal.user_id)
    .bind(selected.channel_id)
    .bind(&principal.token_key)
    .fetch_optional(&mut **tx)
    .await
    .map_err(|_| RelayFailure::storage("unknown"))?
    .ok_or_else(|| RelayFailure::concealed("unknown"))?;
    let now = unix_now();
    let valid = row.try_get::<i64, _>("token_status").ok() == Some(1)
        && row.try_get::<i64, _>("user_status").ok() == Some(1)
        && row.try_get::<i64, _>("channel_status").ok() == Some(1)
        && row
            .try_get::<i64, _>("expired_time")
            .is_ok_and(|expires| expires == -1 || expires >= now);
    if !valid {
        return Err(RelayFailure::concealed("unknown"));
    }
    if selected.pricing.preconsume_quota > 0 {
        let unlimited = row.try_get::<bool, _>("unlimited_quota").unwrap_or(false);
        let token_quota = row.try_get::<i64, _>("remain_quota").unwrap_or(0);
        let user_quota = row.try_get::<i64, _>("user_quota").unwrap_or(0);
        if (!unlimited && token_quota < selected.pricing.preconsume_quota)
            || user_quota < selected.pricing.preconsume_quota
        {
            return Err(RelayFailure::new(
                StatusCode::FORBIDDEN,
                "insufficient_user_quota",
                "insufficient quota",
                "unknown",
            ));
        }
    }
    Ok(())
}

async fn settle_success(
    tx: &mut Transaction<'_, Postgres>,
    principal: &RelayPrincipal,
    selected: &SelectedRelay,
    usage: &Usage,
    quota: i64,
    upstream_request_id: String,
) -> Result<(), sqlx::Error> {
    if quota > 0 {
        sqlx::query("UPDATE users SET quota=COALESCE(quota,0)-$2, used_quota=COALESCE(used_quota,0)+$2, request_count=COALESCE(request_count,0)+1, last_api_activity_at=$3 WHERE id=$1")
            .bind(principal.user_id)
            .bind(quota)
            .bind(unix_now())
            .execute(&mut **tx)
            .await?;
        sqlx::query("UPDATE channels SET used_quota=COALESCE(used_quota,0)+$2 WHERE id=$1")
            .bind(selected.channel_id)
            .bind(quota)
            .execute(&mut **tx)
            .await?;
    }
    sqlx::query("UPDATE tokens SET accessed_time=$2, used_quota=COALESCE(used_quota,0)+$3, remain_quota=CASE WHEN COALESCE(unlimited_quota,FALSE) THEN remain_quota ELSE COALESCE(remain_quota,0)-$3 END WHERE id=$1 AND user_id=$4")
        .bind(principal.token_id).bind(unix_now()).bind(quota).bind(principal.user_id).execute(&mut **tx).await?;
    let user_group_ratio = if selected.pricing.user_group_ratio < 0.0 {
        Value::from(-1)
    } else {
        Value::from(selected.pricing.user_group_ratio)
    };
    let other = serde_json::json!({
        "admin_info": {
            "usage_billing_path": "upstream",
            "use_channel": [selected.channel_id.to_string()],
        },
        "billing_source": "wallet",
        "model_ratio": 0,
        "group_ratio": selected.pricing.group_ratio,
        "completion_ratio": 0,
        "cache_tokens": 0,
        "cache_ratio": 0,
        "model_price": selected.pricing.model_price,
        "user_group_ratio": user_group_ratio,
        "frt": -1000,
        "request_path": selected.request_path,
        "request_conversion": ["embedding"],
    })
    .to_string();
    sqlx::query(
        r#"INSERT INTO logs
           (user_id,created_at,type,content,username,token_name,model_name,quota,
            prompt_tokens,completion_tokens,use_time,is_stream,channel_id,channel_name,
            token_id,"group",ip,request_id,upstream_request_id,other)
           VALUES ($1,$2,2,'',$3,$4,$5,$6,$7,$8,$9,FALSE,$10,$11,$12,$13,$14,$15,$16,$17)"#,
    )
    .bind(principal.user_id)
    .bind(unix_now())
    .bind(&principal.username)
    .bind(&principal.token_name)
    .bind(&selected.origin_model)
    .bind(quota)
    .bind(usage.prompt_tokens)
    .bind(usage.completion_tokens)
    .bind(i64::try_from(selected.started_at.elapsed().as_secs()).unwrap_or(i64::MAX))
    .bind(selected.channel_id)
    .bind(Option::<&str>::None)
    .bind(principal.token_id)
    .bind(&selected.using_group)
    .bind(if principal.record_ip_log {
        principal.client_ip.to_string()
    } else {
        String::new()
    })
    .bind(&principal.request_id)
    .bind(upstream_request_id)
    .bind(other)
    .execute(&mut **tx)
    .await?;
    Ok(())
}

fn record_ip_enabled(raw_setting: &str) -> bool {
    serde_json::from_str::<Value>(raw_setting)
        .ok()
        .and_then(|setting| setting.get("record_ip_log").and_then(Value::as_bool))
        .unwrap_or(false)
}

fn usage_from_response(body: &[u8]) -> Usage {
    let value = serde_json::from_slice::<Value>(body).ok();
    let usage = value.as_ref().and_then(|value| value.get("usage"));
    Usage {
        prompt_tokens: usage
            .and_then(|usage| {
                usage
                    .get("prompt_tokens")
                    .or_else(|| usage.get("input_tokens"))
            })
            .and_then(Value::as_i64)
            .unwrap_or(0),
        completion_tokens: usage
            .and_then(|usage| {
                usage
                    .get("completion_tokens")
                    .or_else(|| usage.get("output_tokens"))
            })
            .and_then(Value::as_i64)
            .unwrap_or(0),
        total_tokens: usage
            .and_then(|usage| usage.get("total_tokens"))
            .and_then(Value::as_i64)
            .unwrap_or(0),
    }
}

async fn read_bounded(
    mut response: reqwest::Response,
    limit: usize,
) -> Result<Vec<u8>, &'static str> {
    let mut body = Vec::new();
    while let Some(chunk) = response
        .chunk()
        .await
        .map_err(|_| "failed to read upstream response")?
    {
        if body.len().saturating_add(chunk.len()) > limit {
            return Err("upstream response body too large");
        }
        body.extend_from_slice(&chunk);
    }
    Ok(body)
}

fn upstream_url(base_url: &str, path: &str) -> Result<reqwest::Url, ()> {
    let mut base = reqwest::Url::parse(base_url).map_err(|_| ())?;
    if !matches!(base.scheme(), "http" | "https")
        || base.host_str().is_none()
        || base.query().is_some()
        || base.fragment().is_some()
    {
        return Err(());
    }
    if !base.path().ends_with('/') {
        base.set_path(&format!("{}/", base.path()));
    }
    base.join(path.trim_start_matches('/')).map_err(|_| ())
}

fn upstream_response(status: StatusCode, headers: HeaderMap, body: Vec<u8>) -> Response {
    let mut response = Response::new(Body::from(body));
    *response.status_mut() = status;
    for (name, value) in &headers {
        response.headers_mut().append(name, value.clone());
    }
    let mut response = filtered_upstream_response(response);
    response.headers_mut().remove(header::CONTENT_LENGTH);
    response.headers_mut().remove("x-new-api-version");
    response.headers_mut().remove("x-oneapi-request-id");
    response
}

fn upstream_error_response(
    status: StatusCode,
    _: HeaderMap,
    body: &[u8],
    upstream_status: StatusCode,
    request_id: &str,
) -> Response {
    let mut response = Response::new(Body::from(normalized_upstream_error_body_with_context(
        body,
        upstream_status,
        request_id,
    )));
    *response.status_mut() = status;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        axum::http::HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

#[cfg(test)]
fn normalized_upstream_error_body(body: &[u8]) -> Vec<u8> {
    normalized_upstream_error_body_with_context(body, StatusCode::OK, "")
}

fn normalized_upstream_error_body_with_context(
    body: &[u8],
    upstream_status: StatusCode,
    request_id: &str,
) -> Vec<u8> {
    let parsed = serde_json::from_slice::<Value>(body).ok();
    let upstream_open_ai = parsed
        .as_ref()
        .and_then(Value::as_object)
        .and_then(|root| root.get("error"))
        .and_then(Value::as_object)
        .and_then(open_ai_error_from_object);
    let error = upstream_open_ai.unwrap_or_else(|| NormalizedUpstreamError {
        message: parsed
            .as_ref()
            .and_then(general_upstream_error_message)
            .unwrap_or_else(|| {
                if request_id.is_empty() || upstream_status == StatusCode::OK {
                    "openai_error".to_owned()
                } else {
                    format!(
                        "bad response status code {} (request id: {request_id})",
                        upstream_status.as_u16()
                    )
                }
            }),
        kind: "bad_response_status_code".to_owned(),
        param: String::new(),
        code: Value::String("bad_response_status_code".to_owned()),
        metadata: None,
    });
    match serde_json::to_vec(&NormalizedUpstreamErrorEnvelope { error }) {
        Ok(encoded) => encoded,
        Err(_) => br#"{"error":{"message":"openai_error","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#.to_vec(),
    }
}

fn open_ai_error_from_object(
    object: &serde_json::Map<String, Value>,
) -> Option<NormalizedUpstreamError> {
    let message = object.get("message")?.as_str()?.to_owned();
    if message.is_empty() {
        return None;
    }
    let kind = match object.get("type") {
        Some(Value::String(kind)) if !kind.is_empty() => kind.clone(),
        Some(Value::String(_)) | None => "upstream_error".to_owned(),
        Some(_) => return None,
    };
    let param = match object.get("param") {
        Some(Value::String(param)) => param.clone(),
        None => String::new(),
        Some(_) => return None,
    };
    let code = object.get("code").cloned().unwrap_or(Value::Null);
    let metadata = object.get("metadata").cloned();
    let message = metadata.as_ref().map_or(message.clone(), |metadata| {
        format!("{message} ({metadata})")
    });
    Some(NormalizedUpstreamError {
        message,
        kind,
        param,
        code,
        metadata,
    })
}

fn general_upstream_error_message(value: &Value) -> Option<String> {
    let root = value.as_object()?;
    if let Some(error) = root.get("error") {
        match error {
            Value::String(message) if !message.is_empty() => return Some(message.clone()),
            Value::Object(_) | Value::String(_) => {}
            other => return Some(other.to_string()),
        }
    }
    for field in ["message", "msg", "err", "error_msg", "detail"] {
        if let Some(message) = root.get(field).and_then(Value::as_str)
            && !message.is_empty()
        {
            return Some(message.to_owned());
        }
    }
    root.get("header")
        .and_then(Value::as_object)
        .and_then(|header| header.get("message"))
        .and_then(Value::as_str)
        .filter(|message| !message.is_empty())
        .or_else(|| {
            root.get("response")
                .and_then(Value::as_object)
                .and_then(|response| response.get("error"))
                .and_then(Value::as_object)
                .and_then(|error| error.get("message"))
                .and_then(Value::as_str)
                .filter(|message| !message.is_empty())
        })
        .map(str::to_owned)
}

fn upstream_request_id(headers: &HeaderMap) -> String {
    headers
        .get("x-oneapi-request-id")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .to_owned()
}

fn relay_token_parts(raw: &str) -> Option<TokenCredential> {
    let raw = raw.trim();
    if raw.eq_ignore_ascii_case("bearer") {
        return None;
    }
    let raw = raw
        .strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .unwrap_or(raw)
        .trim();
    let raw = raw.strip_prefix("sk-").unwrap_or(raw);
    let mut parts = raw.split('-');
    let key = parts.next()?.trim();
    (!key.is_empty()).then(|| TokenCredential {
        key: key.to_owned(),
        channel_suffix: parts.next().map(str::to_owned),
    })
}

fn relay_authentication_input(request: &Request) -> Result<RelayAuthenticationInput, RelayAuth> {
    let request_id = request_id(request);
    let client_ip = request
        .extensions()
        .get::<RequestContext>()
        .and_then(|context| context.client_ip)
        .unwrap_or(IpAddr::V4(Ipv4Addr::LOCALHOST));
    let authorization = request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned);
    let credential = authorization
        .as_deref()
        .and_then(relay_token_parts)
        .ok_or(RelayAuth::ConcealedNotFound)?;
    Ok(RelayAuthenticationInput {
        request_id,
        client_ip,
        authorization,
        credential,
    })
}

fn parse_bool(value: &str) -> bool {
    value.eq_ignore_ascii_case("true") || value == "1"
}

fn request_id(request: &Request) -> String {
    request.extensions().get::<RequestContext>().map_or_else(
        || {
            request
                .headers()
                .get("x-oneapi-request-id")
                .and_then(|value| value.to_str().ok())
                .unwrap_or("unknown")
                .to_owned()
        },
        |context| context.request_id.clone(),
    )
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
        })
}

fn message_with_request_id(message: &str, request_id: &str) -> String {
    format!("{message} (request id: {request_id})")
}

fn auth_failure(error: ModelsError, request_id: &str) -> RelayAuth {
    match error.kind {
        ModelsErrorKind::MissingToken
        | ModelsErrorKind::InvalidToken
        | ModelsErrorKind::DiscoveryHidden => RelayAuth::ConcealedNotFound,
        ModelsErrorKind::AccessDenied => coded_rejection(
            StatusCode::FORBIDDEN,
            error.message.as_ref(),
            if error.message == "您的 IP 不在令牌允许访问的列表中" {
                "access_denied"
            } else {
                ""
            },
            request_id,
        ),
        ModelsErrorKind::UserBanned => coded_rejection(
            StatusCode::FORBIDDEN,
            error.message.as_ref(),
            "",
            request_id,
        ),
        ModelsErrorKind::Database => storage_rejection(request_id),
    }
}

fn coded_rejection(status: StatusCode, message: &str, code: &str, request_id: &str) -> RelayAuth {
    RelayAuth::RejectedOpenAi {
        status,
        message: message_with_request_id(message, request_id),
        code: code.to_owned(),
    }
}

fn storage_rejection(request_id: &str) -> RelayAuth {
    coded_rejection(
        StatusCode::INTERNAL_SERVER_ERROR,
        "Database error, please contact the administrator",
        "",
        request_id,
    )
}

struct RelayFailure {
    status: StatusCode,
    code: &'static str,
    message: String,
    request_id: String,
    concealed: bool,
}

impl RelayFailure {
    fn new(
        status: StatusCode,
        code: &'static str,
        message: impl Into<String>,
        request_id: &str,
    ) -> Self {
        Self {
            status,
            code,
            message: message.into(),
            request_id: request_id.to_owned(),
            concealed: false,
        }
    }

    fn concealed(request_id: &str) -> Self {
        Self {
            status: StatusCode::NOT_FOUND,
            code: "",
            message: "Not Found".to_owned(),
            request_id: request_id.to_owned(),
            concealed: true,
        }
    }

    fn storage(request_id: &str) -> Self {
        Self::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "",
            "Database error, please contact the administrator",
            request_id,
        )
    }

    fn internal(message: &'static str, request_id: &str) -> Self {
        Self::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "internal_error",
            message,
            request_id,
        )
    }

    fn payload_too_large(request_id: &str) -> Self {
        Self::new(
            StatusCode::PAYLOAD_TOO_LARGE,
            "invalid_request_error",
            "request body too large",
            request_id,
        )
    }

    fn bad_request(message: &'static str, request_id: &str) -> Self {
        Self::new(
            StatusCode::BAD_REQUEST,
            "invalid_request_error",
            message,
            request_id,
        )
    }

    fn upstream(message: &'static str, request_id: &str) -> Self {
        Self::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "do_request_failed",
            message,
            request_id,
        )
    }

    fn with_request_id(mut self, request_id: &str) -> Self {
        self.request_id = request_id.to_owned();
        self
    }

    fn into_response(self) -> Response {
        if self.concealed {
            return (
                self.status,
                Json(ConcealedNotFoundEnvelope {
                    message: "Not Found",
                }),
            )
                .into_response();
        }
        (
            self.status,
            Json(CurrentOpenAiErrorEnvelope {
                error: CurrentOpenAiError {
                    message: message_with_request_id(&self.message, &self.request_id),
                    kind: "new_api_error",
                    code: self.code,
                },
            }),
        )
            .into_response()
    }
}

#[derive(Serialize)]
struct ConcealedNotFoundEnvelope {
    message: &'static str,
}

#[derive(Serialize)]
struct CurrentOpenAiErrorEnvelope {
    error: CurrentOpenAiError,
}

#[derive(Serialize)]
struct CurrentOpenAiError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct NormalizedUpstreamErrorEnvelope {
    error: NormalizedUpstreamError,
}

#[derive(Serialize)]
struct NormalizedUpstreamError {
    message: String,
    #[serde(rename = "type")]
    kind: String,
    param: String,
    code: Value,
    #[serde(skip_serializing_if = "Option::is_none")]
    metadata: Option<Value>,
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::http::HeaderValue;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[test]
    fn fixed_price_contract_separates_go_preconsume_and_settlement_rounding() -> TestResult {
        let options = HashMap::from([
            (
                "ModelPrice".to_owned(),
                r#"{"gpt-test":0.000002}"#.to_owned(),
            ),
            ("GroupRatio".to_owned(), r#"{"default":1}"#.to_owned()),
            ("QuotaPerUnit".to_owned(), "500000".to_owned()),
        ]);
        let price = fixed_price("gpt-test", "default", "default", 0.9, &options)
            .map_err(|error| std::io::Error::other(format!("{error:?}")))?;
        assert_eq!(price.preconsume_quota, 0);
        assert_eq!(price.settlement_quota, 1);
        assert_eq!(price.model_price, 0.000002);
        assert_eq!(price.group_ratio, 0.9);
        assert_eq!(price.user_group_ratio, -1.0);
        Ok(())
    }

    #[test]
    fn trust_discount_matches_go_overrides_thresholds_and_decay() {
        let period = 90 * 24 * 60 * 60;
        let discount = |role, override_level, paid_amount_micros, activation_complete, now| {
            trust_discount_from_facts(TrustFacts {
                role,
                override_level,
                paid_amount_micros,
                activation_complete,
                created_at: 1,
                last_api_activity_at: 0,
                last_paid_complete_at: 0,
                now,
            })
        };
        assert_eq!(discount(100, None, 0, false, 1), 0.9);
        assert_eq!(discount(1, Some(2), 0, false, 1), 0.97);
        assert_eq!(discount(1, Some(99), 2_000_000_000, true, 1), 1.0);
        assert_eq!(discount(1, None, 0, false, 1), 1.0);
        assert_eq!(discount(1, None, 100_000_000, true, 1), 0.97);
        assert_eq!(discount(1, None, 2_000_000_000, true, 1), 0.9);
        assert_eq!(discount(1, None, 2_000_000_000, true, 1 + 2 * period), 0.97);
    }

    #[test]
    fn token_parser_keeps_the_legacy_prefix_and_channel_suffix_contract() -> TestResult {
        let token = relay_token_parts("Bearer sk-relayprobe-17")
            .ok_or_else(|| std::io::Error::other("missing relay token fixture"))?;
        assert_eq!(token.key, "relayprobe");
        assert_eq!(token.channel_suffix.as_deref(), Some("17"));
        assert!(relay_token_parts("Bearer ").is_none());
        Ok(())
    }

    #[test]
    fn model_mapping_rewrites_only_the_model_and_rejects_cycles() -> TestResult {
        let mapped = mapped_model("alias", r#"{"alias":"gpt-test"}"#)
            .map_err(|error| std::io::Error::other(error.to_owned()))?;
        assert_eq!(mapped, "gpt-test");
        let body = mapped_body(
            br#"{"input":"hello","model":"alias","vendor":{"keep":true}}"#,
            "alias",
            &mapped,
        )
        .map_err(|error| std::io::Error::other(error.to_owned()))?;
        let value: Value = serde_json::from_slice(&body)?;
        assert_eq!(value["model"], "gpt-test");
        assert_eq!(value["vendor"]["keep"], true);
        assert_eq!(
            mapped_model("a", r#"{"a":"b","b":"a"}"#),
            Err("model_mapping_contains_cycle")
        );
        Ok(())
    }

    #[test]
    fn upstream_url_preserves_an_operator_owned_path_prefix() -> TestResult {
        let url = upstream_url("https://provider.example/prefix", "/v1/embeddings")
            .map_err(|()| std::io::Error::other("invalid upstream URL fixture"))?;
        assert_eq!(
            url.as_str(),
            "https://provider.example/prefix/v1/embeddings"
        );
        Ok(())
    }

    #[test]
    fn response_usage_detects_embedding_input_tokens() {
        let usage = usage_from_response(br#"{"usage":{"prompt_tokens":1,"total_tokens":1}}"#);
        assert!(usage.billable());
        assert_eq!(usage.prompt_tokens, 1);
        assert_eq!(usage.completion_tokens, 0);
    }

    #[test]
    fn upstream_errors_match_current_go_error_normalization() {
        assert_eq!(
            normalized_upstream_error_body(br#"{"error":"fixture-rate-limit"}"#),
            br#"{"error":{"message":"fixture-rate-limit","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#
        );
        assert_eq!(
            normalized_upstream_error_body(br#"{"message":"provider unavailable"}"#),
            br#"{"error":{"message":"provider unavailable","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#
        );
        assert_eq!(
            normalized_upstream_error_body(
                br#"{"error":{"message":"limited","type":"server_error","param":"capacity","code":"busy"}}"#,
            ),
            br#"{"error":{"message":"limited","type":"server_error","param":"capacity","code":"busy"}}"#
        );
        assert_eq!(
            normalized_upstream_error_body(b"not-json"),
            br#"{"error":{"message":"openai_error","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#
        );
        assert_eq!(
            normalized_upstream_error_body_with_context(
                b"not-json",
                StatusCode::TOO_MANY_REQUESTS,
                "request-id",
            ),
            br#"{"error":{"message":"bad response status code 429 (request id: request-id)","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#
        );
    }

    #[test]
    fn upstream_errors_do_not_forward_provider_headers() {
        let mut headers = HeaderMap::new();
        headers.insert("retry-after", HeaderValue::from_static("7"));
        headers.insert("x-request-id", HeaderValue::from_static("provider-request"));
        headers.insert("server", HeaderValue::from_static("provider"));
        let response = upstream_error_response(
            StatusCode::TOO_MANY_REQUESTS,
            headers,
            br#"{"error":"limited"}"#,
            StatusCode::TOO_MANY_REQUESTS,
            "test-request-id",
        );
        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(
            response
                .headers()
                .get(header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok()),
            Some("application/json; charset=utf-8")
        );
        assert!(!response.headers().contains_key("retry-after"));
        assert!(!response.headers().contains_key("x-request-id"));
        assert!(!response.headers().contains_key("server"));
    }

    #[test]
    fn status_code_mapping_matches_current_go_supported_values() {
        let mapping = parsed_status_code_mapping(
            r#"{"429":503,"500":"502","400":401.0,"401":401.5,"403":"bad","200":418}"#,
        );
        assert_eq!(
            mapped_status_code(StatusCode::TOO_MANY_REQUESTS, &mapping),
            StatusCode::SERVICE_UNAVAILABLE
        );
        assert_eq!(
            mapped_status_code(StatusCode::INTERNAL_SERVER_ERROR, &mapping),
            StatusCode::BAD_GATEWAY
        );
        assert_eq!(
            mapped_status_code(StatusCode::BAD_REQUEST, &mapping),
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            mapped_status_code(StatusCode::UNAUTHORIZED, &mapping),
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(
            mapped_status_code(StatusCode::FORBIDDEN, &mapping),
            StatusCode::FORBIDDEN
        );
        assert_eq!(mapped_status_code(StatusCode::OK, &mapping), StatusCode::OK);
        assert!(parsed_status_code_mapping("not-json").is_empty());
    }

    #[test]
    fn production_subset_is_only_openai_embeddings_on_a_type_one_channel() {
        assert!(channel_supports_protocol(1, RelayProtocol::Embedding));
        for protocol in [
            RelayProtocol::AlphaSearch,
            RelayProtocol::Rerank,
            RelayProtocol::OpenAi,
        ] {
            assert!(!channel_supports_protocol(1, protocol));
        }
        assert!(!channel_supports_protocol(59, RelayProtocol::Embedding));
        assert!(!channel_supports_protocol(60, RelayProtocol::Embedding));
    }

    #[test]
    fn record_ip_setting_matches_go_default_and_explicit_opt_in() {
        assert!(!record_ip_enabled(""));
        assert!(!record_ip_enabled("not-json"));
        assert!(!record_ip_enabled(r#"{"record_ip_log":false}"#));
        assert!(record_ip_enabled(r#"{"record_ip_log":true}"#));
    }

    #[test]
    fn performance_config_keeps_go_defaults_and_partial_overrides() {
        assert_eq!(
            performance_monitor_config(&HashMap::new()),
            PerformanceMonitorConfig::default()
        );
        assert_eq!(
            performance_monitor_config(&HashMap::from([
                (
                    "performance_setting.monitor_enabled".to_owned(),
                    "false".to_owned(),
                ),
                (
                    "performance_setting.monitor_memory_threshold".to_owned(),
                    "73.9".to_owned(),
                ),
                (
                    "performance_setting.disk_cache_path".to_owned(),
                    "/var/tmp".to_owned(),
                ),
            ])),
            PerformanceMonitorConfig {
                enabled: false,
                cpu_threshold: 90,
                memory_threshold: 73,
                disk_threshold: 95,
                disk_cache_path: "/var/tmp".to_owned(),
            }
        );
    }

    #[test]
    fn proc_metrics_match_current_go_gopsutil_formulas() -> TestResult {
        let times = cpu_times_from_stat("cpu 100 10 20 300 40 5 6 7 999 999\n")
            .ok_or_else(|| std::io::Error::other("missing CPU times fixture"))?;
        assert_eq!(
            times,
            CpuTimes {
                total: 488,
                busy: 188,
            }
        );
        assert_eq!(
            cpu_usage(
                CpuTimes {
                    total: 100,
                    busy: 40,
                },
                CpuTimes {
                    total: 200,
                    busy: 90,
                },
            ),
            50.0
        );
        assert_eq!(
            memory_usage_from_meminfo(
                "MemTotal: 1000 kB\nMemFree: 100 kB\nBuffers: 50 kB\nCached: 200 kB\nSReclaimable: 50 kB\n",
            ),
            Some(60.0)
        );
        Ok(())
    }

    #[test]
    fn performance_thresholds_truncate_and_preserve_go_check_order() {
        let config = PerformanceMonitorConfig::default();
        assert!(matches!(
            performance_rejection(
                config.clone(),
                SystemPerformanceStatus {
                    cpu_usage: 90.9,
                    memory_usage: 91.2,
                    disk_usage: 99.0,
                },
            ),
            Some(RelayAuth::RejectedOpenAiWithParam {
                status: StatusCode::SERVICE_UNAVAILABLE,
                ref message,
                code: "system_memory_overloaded",
            }) if message == "system memory overloaded (current: 91.2%, threshold: 90%)"
        ));
        assert!(matches!(
            performance_rejection(
                config,
                SystemPerformanceStatus {
                    cpu_usage: 91.0,
                    memory_usage: 99.0,
                    disk_usage: 99.0,
                },
            ),
            Some(RelayAuth::RejectedOpenAiWithParam {
                status: StatusCode::SERVICE_UNAVAILABLE,
                ref message,
                code: "system_cpu_overloaded",
            }) if message == "system cpu overloaded (current: 91.0%, threshold: 90%)"
        ));
        assert!(
            performance_rejection(
                PerformanceMonitorConfig {
                    cpu_threshold: 0,
                    memory_threshold: 0,
                    disk_threshold: 0,
                    ..PerformanceMonitorConfig::default()
                },
                SystemPerformanceStatus {
                    cpu_usage: 100.0,
                    memory_usage: 100.0,
                    disk_usage: 100.0,
                },
            )
            .is_none()
        );
    }

    #[test]
    fn memory_model_rate_limit_separates_attempt_and_success_quotas_like_go() -> TestResult {
        let limiter = MemoryModelRateLimits::default();
        let config = ModelRateLimitConfig {
            enabled: true,
            duration_minutes: 1,
            total_max_count: 2,
            success_max_count: 1,
        };
        let first = limiter
            .check(42, &config)
            .map_err(|error| std::io::Error::other(format!("{error:?}")))?;
        limiter.commit_success(&first);
        assert!(matches!(
            limiter.check(42, &config),
            Err(ModelRateLimitFailure::SuccessExceeded)
        ));
        assert!(matches!(
            limiter.check(42, &config),
            Err(ModelRateLimitFailure::TotalExceeded)
        ));
        Ok(())
    }

    #[test]
    fn memory_model_rate_limit_releases_the_oldest_request_at_the_window_edge() {
        let mut queues = HashMap::new();
        assert!(memory_rate_limit_request(
            &mut queues,
            "MRRL42",
            1,
            60,
            1_000
        ));
        assert!(!memory_rate_limit_request(
            &mut queues,
            "MRRL42",
            1,
            60,
            1_059
        ));
        assert!(memory_rate_limit_request(
            &mut queues,
            "MRRL42",
            1,
            60,
            1_060
        ));
    }

    #[test]
    fn model_rate_limit_errors_keep_the_current_go_openai_code_and_messages() {
        let config = ModelRateLimitConfig {
            enabled: true,
            duration_minutes: 3,
            total_max_count: 7,
            success_max_count: 5,
        };
        assert!(matches!(
            model_rate_limit_internal("request-id"),
            RelayAuth::RejectedOpenAi {
                status: StatusCode::INTERNAL_SERVER_ERROR,
                ref message,
                ref code,
            } if message == "rate_limit_check_failed (request id: request-id)"
                && code == "invalid_request"
        ));
        assert!(matches!(
            model_rate_limit_total_exceeded(&config, "request-id"),
            RelayAuth::RejectedOpenAi {
                status: StatusCode::TOO_MANY_REQUESTS,
                ref message,
                ref code,
            } if message == "您已达到总请求数限制：3分钟内最多请求7次，包括失败次数，请检查您的请求是否正确 (request id: request-id)"
                && code == "invalid_request"
        ));
        assert!(matches!(
            model_rate_limit_success_exceeded(&config, "request-id"),
            RelayAuth::RejectedOpenAi {
                status: StatusCode::TOO_MANY_REQUESTS,
                ref message,
                ref code,
            } if message == "您已达到请求数限制：3分钟内最多请求5次 (request id: request-id)"
                && code == "invalid_request"
        ));
    }
}
