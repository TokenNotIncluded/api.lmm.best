//! Legacy-compatible dynamic-pricing status and root setting routes.

use std::{
    collections::{BTreeMap, HashMap},
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, put},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row};

use crate::auth::{
    DashboardAuth, UserAuthPolicyError, dashboard_token_candidate, enforce_user_auth_view,
    user_auth_message, user_auth_status,
};

const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 64 * 1024 * 1024;
const CHANNEL_STATUS_ENABLED: i64 = 1;
const LOG_TYPE_MANAGE: i64 = 3;
const STATE_KEY_PREFIX: &str = "dynamic_pricing:state:";

/// PostgreSQL, Valkey, and dashboard-auth dependencies for dynamic pricing routes.
#[derive(Clone)]
pub struct DynamicPricingState {
    pg: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
}

impl DynamicPricingState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, valkey: redis::Client, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pg, valkey, auth }
    }
}

/// Builds the administrator status and root setting routes.
pub fn router(state: DynamicPricingState) -> Router {
    Router::new()
        .route("/api/dynamic_pricing/status", get(get_status))
        .route("/api/dynamic_pricing/setting", put(update_setting))
        .with_state(state)
}

#[derive(Clone, Debug, Default, Deserialize, Serialize)]
struct ModelPricingOverride {
    #[serde(default)]
    target_tpm: f64,
    #[serde(default)]
    target_rpm: f64,
    #[serde(default)]
    target_cost_rate: f64,
    #[serde(default)]
    base_price_usd_per_million: f64,
}

#[derive(Clone, Debug, Serialize)]
struct DynamicPricingSetting {
    enabled: bool,
    min_factor: f64,
    require_channel_cost: bool,
    tick_interval_seconds: i64,
    window_minutes: i64,
    target_tpm: f64,
    target_rpm: f64,
    target_cost_rate: f64,
    base_price_usd_per_million: f64,
    alpha_load: f64,
    alpha_up: f64,
    alpha_down: f64,
    cost_floor_factor: f64,
    max_factor: f64,
    load_deadzone: f64,
    heat_gamma: f64,
    max_step_up: f64,
    max_step_down: f64,
    failover_probability: f64,
    channel_costs: HashMap<String, f64>,
    per_model: HashMap<String, ModelPricingOverride>,
}

impl Default for DynamicPricingSetting {
    fn default() -> Self {
        Self {
            enabled: false,
            min_factor: 1.0,
            require_channel_cost: true,
            tick_interval_seconds: 60,
            window_minutes: 5,
            target_tpm: 100_000.0,
            target_rpm: 60.0,
            target_cost_rate: 1.0,
            base_price_usd_per_million: 1.0,
            alpha_load: 0.3,
            alpha_up: 0.30,
            alpha_down: 0.05,
            cost_floor_factor: 1.2,
            max_factor: 3.0,
            load_deadzone: 0.4,
            heat_gamma: 2.0,
            max_step_up: 0.10,
            max_step_down: 0.03,
            failover_probability: 0.15,
            channel_costs: HashMap::new(),
            per_model: HashMap::new(),
        }
    }
}

#[derive(Clone, Debug, Default, Deserialize)]
struct ModelState {
    #[serde(default, rename = "LoadEMA")]
    load_ema: f64,
    #[serde(default, rename = "CostEMA")]
    cost_ema: f64,
    #[serde(default, rename = "Factor")]
    factor: f64,
    #[serde(default, rename = "CostFloor")]
    cost_floor: f64,
    #[serde(default, rename = "UnpricedTokens")]
    unpriced_tokens: f64,
    #[serde(default, rename = "UnpricedRequests")]
    unpriced_requests: f64,
    #[serde(default, rename = "HasUnpricedTraffic")]
    has_unpriced_traffic: bool,
    #[serde(default, rename = "UpdatedAt")]
    updated_at: i64,
}

#[derive(Clone, Debug, Default, Deserialize)]
struct DynamicPricingSettingUpdate {
    enabled: Option<bool>,
    min_factor: Option<f64>,
    base_price_usd_per_million: Option<f64>,
    cost_floor_factor: Option<f64>,
    max_factor: Option<f64>,
    channel_costs: Option<HashMap<String, f64>>,
}

async fn get_status(State(state): State<DynamicPricingState>, headers: HeaderMap) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let response = match build_status_payload(&state).await {
        Ok(payload) => api_success(payload),
        Err(message) => api_error(message),
    };
    with_auth_version(response)
}

async fn update_setting(State(state): State<DynamicPricingState>, request: Request) -> Response {
    let principal = match authenticated_root(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let headers = request.headers().clone();
    let body = match parse_setting_update(request).await {
        Ok(body) => body,
        Err(()) => return with_auth_version(invalid_parameters(&headers)),
    };
    let response = match apply_setting_update(&state, &body, &principal, &headers).await {
        Ok(payload) => api_success(payload),
        Err(message) => api_error(message),
    };
    with_auth_version(response)
}

async fn build_status_payload(state: &DynamicPricingState) -> Result<Value, String> {
    let setting = load_dynamic_pricing_setting(&state.pg).await?;
    let config_err = validate_dynamic_pricing_setting(&setting).err();
    let (active_channels, configured_channels, channels, missing_channels, coverage_err) =
        active_channel_cost_coverage(&state.pg, &setting).await?;

    let mut preview_factor = 1.0;
    if setting.enabled {
        preview_factor = setting.min_factor;
        let (_, max) = dynamic_pricing_request_factor_range(
            preview_factor,
            setting.base_price_usd_per_million,
            setting.cost_floor_factor,
            &channels,
        );
        preview_factor = max;
    }

    let model_states = load_model_states(&state.valkey).await;
    let mut model_names: Vec<String> = model_states.keys().cloned().collect();
    model_names.sort();

    let mut models = BTreeMap::new();
    for model_name in model_names {
        let Some(state_row) = model_states.get(&model_name) else {
            continue;
        };
        let engine_factor = get_multiplier(&setting, state_row);
        let (request_factor_min, request_factor_max) = if setting.enabled {
            dynamic_pricing_request_factor_range(
                engine_factor,
                model_base_price(&setting, &model_name),
                setting.cost_floor_factor,
                &channels,
            )
        } else {
            (engine_factor, engine_factor)
        };
        models.insert(
            model_name.clone(),
            json!({
                "factor": engine_factor,
                "request_factor_min": request_factor_min,
                "request_factor_max": request_factor_max,
                "engine_factor": state_row.factor,
                "hard_cost_floor": state_row.cost_floor,
                "load_ema": state_row.load_ema,
                "cost_ema": state_row.cost_ema,
                "has_unpriced_traffic": state_row.has_unpriced_traffic,
                "unpriced_tokens": state_row.unpriced_tokens,
                "unpriced_requests": state_row.unpriced_requests,
                "updated_at": state_row.updated_at,
            }),
        );
        if setting.enabled {
            preview_factor = preview_factor.max(request_factor_max);
        }
    }

    let ready = config_err.is_none()
        && coverage_err.is_none()
        && setting.require_channel_cost
        && missing_channels.is_empty();
    let (status, reason) = if let Some(error) = config_err {
        ("invalid_configuration".to_owned(), error)
    } else if let Some(error) = coverage_err {
        ("coverage_check_failed".to_owned(), error)
    } else if !setting.require_channel_cost {
        (
            "cost_guard_disabled".to_owned(),
            "unknown-cost channels are not configured to fail closed".to_owned(),
        )
    } else if !missing_channels.is_empty() {
        (
            "missing_channel_costs".to_owned(),
            "one or more active channels do not have a conservative upstream cost".to_owned(),
        )
    } else {
        ("ready".to_owned(), String::new())
    };

    Ok(json!({
        "enabled": setting.enabled,
        "preview_factor": preview_factor,
        "setting": setting,
        "models": models,
        "safety": {
            "ready": ready,
            "status": status,
            "reason": reason,
            "active_channel_count": active_channels,
            "configured_channel_count": configured_channels,
            "channels": channels,
            "missing_channels": missing_channels,
            "require_channel_cost": setting.require_channel_cost,
        }
    }))
}

async fn apply_setting_update(
    state: &DynamicPricingState,
    request: &DynamicPricingSettingUpdate,
    principal: &Principal,
    headers: &HeaderMap,
) -> Result<Value, String> {
    let mut candidate = load_dynamic_pricing_setting(&state.pg).await?;
    let mut values = BTreeMap::new();
    if let Some(enabled) = request.enabled {
        candidate.enabled = enabled;
        values.insert(
            "dynamic_pricing_setting.enabled".to_owned(),
            enabled.to_string(),
        );
        if enabled {
            candidate.require_channel_cost = true;
            values.insert(
                "dynamic_pricing_setting.require_channel_cost".to_owned(),
                "true".to_owned(),
            );
        }
    }
    if let Some(min_factor) = request.min_factor {
        candidate.min_factor = min_factor;
        values.insert(
            "dynamic_pricing_setting.min_factor".to_owned(),
            format_float(min_factor),
        );
    }
    if let Some(base_price) = request.base_price_usd_per_million {
        candidate.base_price_usd_per_million = base_price;
        values.insert(
            "dynamic_pricing_setting.base_price_usd_per_million".to_owned(),
            format_float(base_price),
        );
    }
    if let Some(cost_floor_factor) = request.cost_floor_factor {
        candidate.cost_floor_factor = cost_floor_factor;
        values.insert(
            "dynamic_pricing_setting.cost_floor_factor".to_owned(),
            format_float(cost_floor_factor),
        );
    }
    if let Some(max_factor) = request.max_factor {
        candidate.max_factor = max_factor;
        values.insert(
            "dynamic_pricing_setting.max_factor".to_owned(),
            format_float(max_factor),
        );
    }
    if let Some(channel_costs) = &request.channel_costs {
        candidate.channel_costs = channel_costs.clone();
        values.insert(
            "dynamic_pricing_setting.channel_costs".to_owned(),
            serde_json::to_string(channel_costs).map_err(|error| error.to_string())?,
        );
    }
    if values.is_empty() {
        return Err("no dynamic pricing settings were supplied".to_owned());
    }
    validate_dynamic_pricing_setting(&candidate)?;
    if candidate.enabled && candidate.require_channel_cost {
        let (_, _, _, missing, err) =
            active_channel_cost_coverage(&state.pg, &candidate).await?;
        if err.is_some() {
            return Err(err.unwrap_or_else(|| "coverage check failed".to_owned()));
        }
        if !missing.is_empty() {
            let labels = missing
                .iter()
                .map(|channel| {
                    format!(
                        "{} ({})",
                        channel.get("name").and_then(Value::as_str).unwrap_or(""),
                        channel.get("id").and_then(Value::as_i64).unwrap_or_default()
                    )
                })
                .collect::<Vec<_>>();
            return Err(format!(
                "configure a positive conservative cost for every active channel before enabling dynamic pricing: {}",
                labels.join(", ")
            ));
        }
    }
    write_dynamic_pricing_options(&state.pg, &values).await?;
    record_dynamic_pricing_audit(state, principal, headers, values.len()).await;
    build_status_payload(state).await
}

async fn load_dynamic_pricing_setting(pg: &PgPool) -> Result<DynamicPricingSetting, String> {
    let rows = sqlx::query("SELECT key, value FROM options WHERE key LIKE 'dynamic_pricing_setting.%'")
        .fetch_all(pg)
        .await
        .map_err(|error| error.to_string())?;
    let mut options = HashMap::new();
    for row in rows {
        let key: String = row.try_get("key").map_err(|error| error.to_string())?;
        let value: String = row.try_get("value").map_err(|error| error.to_string())?;
        options.insert(key, value);
    }
    Ok(parse_dynamic_pricing_setting(&options))
}

fn parse_dynamic_pricing_setting(options: &HashMap<String, String>) -> DynamicPricingSetting {
    let mut setting = DynamicPricingSetting::default();
    if let Some(value) = options.get("dynamic_pricing_setting.enabled") {
        setting.enabled = value == "true";
    }
    if let Some(value) = options.get("dynamic_pricing_setting.require_channel_cost") {
        setting.require_channel_cost = value == "true";
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.min_factor")) {
        setting.min_factor = value;
    }
    if let Some(value) = parse_i64_option(options.get("dynamic_pricing_setting.tick_interval_seconds")) {
        setting.tick_interval_seconds = value;
    }
    if let Some(value) = parse_i64_option(options.get("dynamic_pricing_setting.window_minutes")) {
        setting.window_minutes = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.target_tpm")) {
        setting.target_tpm = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.target_rpm")) {
        setting.target_rpm = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.target_cost_rate")) {
        setting.target_cost_rate = value;
    }
    if let Some(value) =
        parse_f64_option(options.get("dynamic_pricing_setting.base_price_usd_per_million"))
    {
        setting.base_price_usd_per_million = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.alpha_load")) {
        setting.alpha_load = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.alpha_up")) {
        setting.alpha_up = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.alpha_down")) {
        setting.alpha_down = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.cost_floor_factor")) {
        setting.cost_floor_factor = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.max_factor")) {
        setting.max_factor = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.load_deadzone")) {
        setting.load_deadzone = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.heat_gamma")) {
        setting.heat_gamma = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.max_step_up")) {
        setting.max_step_up = value;
    }
    if let Some(value) = parse_f64_option(options.get("dynamic_pricing_setting.max_step_down")) {
        setting.max_step_down = value;
    }
    if let Some(value) =
        parse_f64_option(options.get("dynamic_pricing_setting.failover_probability"))
    {
        setting.failover_probability = value;
    }
    if let Some(raw) = options.get("dynamic_pricing_setting.channel_costs") {
        if let Ok(value) = serde_json::from_str::<HashMap<String, f64>>(raw) {
            setting.channel_costs = value;
        }
    }
    if let Some(raw) = options.get("dynamic_pricing_setting.per_model") {
        if let Ok(value) = serde_json::from_str::<HashMap<String, ModelPricingOverride>>(raw) {
            setting.per_model = value;
        }
    }
    setting
}

fn validate_dynamic_pricing_setting(setting: &DynamicPricingSetting) -> Result<(), String> {
    if setting.min_factor < 1.0 || !setting.min_factor.is_finite() {
        return Err("min_factor must be finite and at least 1".to_owned());
    }
    if setting.tick_interval_seconds <= 0 {
        return Err("tick_interval_seconds must be positive".to_owned());
    }
    if setting.window_minutes <= 0 {
        return Err("window_minutes must be positive".to_owned());
    }
    validate_non_negative("target_tpm", setting.target_tpm)?;
    validate_non_negative("target_rpm", setting.target_rpm)?;
    validate_non_negative("target_cost_rate", setting.target_cost_rate)?;
    if setting.base_price_usd_per_million < 0.0 || !setting.base_price_usd_per_million.is_finite() {
        return Err("base_price_usd_per_million must be finite and non-negative".to_owned());
    }
    if setting.enabled && setting.base_price_usd_per_million <= 0.0 {
        return Err("base_price_usd_per_million must be positive while dynamic pricing is enabled".to_owned());
    }
    if setting.enabled && !setting.require_channel_cost {
        return Err("require_channel_cost must be true while dynamic pricing is enabled".to_owned());
    }
    validate_unit_interval("alpha_load", setting.alpha_load)?;
    validate_unit_interval("alpha_up", setting.alpha_up)?;
    validate_unit_interval("alpha_down", setting.alpha_down)?;
    if setting.cost_floor_factor < 1.0 || !setting.cost_floor_factor.is_finite() {
        return Err("cost_floor_factor must be finite and at least 1".to_owned());
    }
    if setting.max_factor < 1.0 || !setting.max_factor.is_finite() {
        return Err("max_factor must be finite and at least 1".to_owned());
    }
    if setting.min_factor > setting.max_factor {
        return Err("min_factor must not exceed max_factor".to_owned());
    }
    if setting.load_deadzone < 0.0 || setting.load_deadzone >= 1.0 || !setting.load_deadzone.is_finite()
    {
        return Err("load_deadzone must be finite and in [0, 1)".to_owned());
    }
    if setting.heat_gamma < 1.0 || !setting.heat_gamma.is_finite() {
        return Err("heat_gamma must be finite and at least 1".to_owned());
    }
    validate_unit_interval("max_step_up", setting.max_step_up)?;
    validate_unit_interval("max_step_down", setting.max_step_down)?;
    validate_unit_interval("failover_probability", setting.failover_probability)?;
    for (channel_id, cost) in &setting.channel_costs {
        if *cost <= 0.0 || !cost.is_finite() {
            return Err(format!("channel_costs[{channel_id:?}] must be finite and positive"));
        }
    }
    if setting.enabled && setting.require_channel_cost && setting.channel_costs.is_empty() {
        return Err(
            "channel_costs must contain at least one positive channel cost while dynamic pricing is enabled"
                .to_owned(),
        );
    }
    for (model, override_values) in &setting.per_model {
        validate_non_negative(&format!("per_model[{model:?}].target_tpm"), override_values.target_tpm)?;
        validate_non_negative(&format!("per_model[{model:?}].target_rpm"), override_values.target_rpm)?;
        validate_non_negative(
            &format!("per_model[{model:?}].target_cost_rate"),
            override_values.target_cost_rate,
        )?;
        if override_values.base_price_usd_per_million < 0.0
            || !override_values.base_price_usd_per_million.is_finite()
        {
            return Err(format!(
                "per_model[{model:?}].base_price_usd_per_million must be finite and non-negative"
            ));
        }
    }
    Ok(())
}

async fn active_channel_cost_coverage(
    pg: &PgPool,
    setting: &DynamicPricingSetting,
) -> Result<(i64, i64, Vec<Value>, Vec<Value>, Option<String>), String> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS id, COALESCE(name, '') AS name, COALESCE(status, 0)::BIGINT AS status \
         FROM channels WHERE deleted_at IS NULL ORDER BY id ASC",
    )
    .fetch_all(pg)
    .await
    .map_err(|error| error.to_string())?;

    let mut active = 0_i64;
    let mut configured = 0_i64;
    let mut channels = Vec::new();
    let mut missing = Vec::new();
    for row in rows {
        let id: i64 = row.try_get("id").map_err(|error| error.to_string())?;
        let name: String = row.try_get("name").map_err(|error| error.to_string())?;
        let status: i64 = row.try_get("status").map_err(|error| error.to_string())?;
        if status != CHANNEL_STATUS_ENABLED {
            continue;
        }
        active += 1;
        let cost = setting
            .channel_costs
            .get(&id.to_string())
            .copied()
            .unwrap_or_default();
        let has_cost = cost > 0.0;
        channels.push(json!({
            "id": id,
            "name": name,
            "cost": cost,
            "cost_floor": cost_floor_multiplier(cost, setting.base_price_usd_per_million, setting.cost_floor_factor),
            "configured": has_cost,
        }));
        if has_cost {
            configured += 1;
        } else {
            missing.push(json!({"id": id, "name": name}));
        }
    }
    missing.sort_by_key(|entry| entry.get("id").and_then(Value::as_i64).unwrap_or_default());
    channels.sort_by_key(|entry| entry.get("id").and_then(Value::as_i64).unwrap_or_default());
    Ok((active, configured, channels, missing, None))
}

fn dynamic_pricing_request_factor_range(
    engine_factor: f64,
    base_price: f64,
    floor_factor: f64,
    channels: &[Value],
) -> (f64, f64) {
    let mut minimum = engine_factor;
    let mut maximum = engine_factor;
    let mut found_configured_channel = false;
    for channel in channels {
        let Some(cost) = channel.get("cost").and_then(Value::as_f64) else {
            continue;
        };
        if cost <= 0.0 {
            continue;
        }
        let mut request_factor = cost_floor_multiplier(cost, base_price, floor_factor);
        if request_factor <= 0.0 {
            continue;
        }
        request_factor = request_factor.max(engine_factor);
        if !found_configured_channel {
            minimum = request_factor;
            maximum = request_factor;
            found_configured_channel = true;
        } else {
            minimum = minimum.min(request_factor);
            maximum = maximum.max(request_factor);
        }
    }
    (minimum, maximum)
}

fn get_multiplier(setting: &DynamicPricingSetting, state: &ModelState) -> f64 {
    if !setting.enabled {
        return 1.0;
    }
    let minimum = if setting.min_factor < 1.0 || !setting.min_factor.is_finite() {
        1.0
    } else {
        setting.min_factor
    };
    let factor = state.factor;
    if factor >= 1.0 && factor.is_finite() {
        return factor.max(minimum);
    }
    minimum
}

fn model_base_price(setting: &DynamicPricingSetting, model: &str) -> f64 {
    let mut base = setting.base_price_usd_per_million;
    if let Some(override_values) = setting.per_model.get(model) {
        if override_values.base_price_usd_per_million > 0.0 {
            base = override_values.base_price_usd_per_million;
        }
    }
    if base <= 0.0 || !base.is_finite() {
        1.0
    } else {
        base
    }
}

fn cost_floor_multiplier(unit_cost: f64, base_price: f64, floor_factor: f64) -> f64 {
    if !is_finite_positive(unit_cost) || !is_finite_positive(base_price) {
        return 0.0;
    }
    let mut factor = floor_factor;
    if !is_finite_positive(factor) || factor < 1.0 {
        factor = 1.0;
    }
    let floor = unit_cost * factor / base_price;
    if !is_finite_positive(floor) {
        return 0.0;
    }
    floor.max(1.0)
}

async fn load_model_states(valkey: &redis::Client) -> HashMap<String, ModelState> {
    let Ok(mut connection) = valkey.get_multiplexed_async_connection().await else {
        return HashMap::new();
    };
    let Ok(keys) = scan_keys(&mut connection, &format!("{STATE_KEY_PREFIX}*")).await else {
        return HashMap::new();
    };
    let mut states = HashMap::new();
    for key in keys {
        let Some(model) = key.strip_prefix(STATE_KEY_PREFIX) else {
            continue;
        };
        let Ok(raw): Result<String, _> = redis::cmd("GET").arg(&key).query_async(&mut connection).await else {
            continue;
        };
        if let Ok(state) = serde_json::from_str::<ModelState>(&raw) {
            states.insert(model.to_owned(), state);
        }
    }
    states
}

async fn scan_keys(
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

async fn write_dynamic_pricing_options(
    pg: &PgPool,
    values: &BTreeMap<String, String>,
) -> Result<(), String> {
    let mut ordered_keys = values.keys().cloned().collect::<Vec<_>>();
    ordered_keys.sort();
    if let Some(enabled_value) = values.get("dynamic_pricing_setting.enabled") {
        ordered_keys.retain(|key| key != "dynamic_pricing_setting.enabled");
        if enabled_value == "false" {
            ordered_keys.insert(0, "dynamic_pricing_setting.enabled".to_owned());
        } else {
            ordered_keys.push("dynamic_pricing_setting.enabled".to_owned());
        }
    }
    let mut transaction = pg.begin().await.map_err(|error| error.to_string())?;
    for key in ordered_keys {
        let Some(value) = values.get(&key) else {
            continue;
        };
        sqlx::query(
            "INSERT INTO options (key, value) VALUES ($1, $2) \
             ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
        )
        .bind(&key)
        .bind(value)
        .execute(&mut *transaction)
        .await
        .map_err(|error| error.to_string())?;
    }
    transaction.commit().await.map_err(|error| error.to_string())
}

async fn record_dynamic_pricing_audit(
    state: &DynamicPricingState,
    principal: &Principal,
    headers: &HeaderMap,
    key_count: usize,
) {
    let other = json!({
        "op": {
            "action": "dynamic_pricing.update",
            "params": {"keys": key_count},
        },
        "admin_info": {
            "admin_id": principal.user.id,
            "admin_username": principal.user.username,
            "admin_role": principal.user.role,
            "auth_method": if dashboard_token_candidate(&principal.credential) {
                "session"
            } else {
                "access_token"
            },
        },
    });
    let username = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(username, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(principal.user.id)
    .fetch_optional(&state.pg)
    .await
    .ok()
    .flatten()
    .unwrap_or_else(|| principal.user.username.clone());
    let log = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
         VALUES ($1, $2, $3, $4, $5, $6, $7)",
    )
    .bind(principal.user.id)
    .bind(unix_now())
    .bind(LOG_TYPE_MANAGE)
    .bind("PUT /api/dynamic_pricing/setting")
    .bind(username)
    .bind(client_ip(headers))
    .bind(other.to_string())
    .execute(&state.pg)
    .await;
    if let Err(error) = log {
        tracing::warn!(%error, "dynamic pricing administrator audit write failed");
    }
}

#[derive(Clone, Debug)]
struct Principal {
    user: crate::auth::DashboardUserView,
    credential: String,
}

async fn authenticated_admin(state: &DynamicPricingState, headers: &HeaderMap) -> Result<(), Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(())
}

async fn authenticated_root(
    state: &DynamicPricingState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ROOT_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(principal)
}

async fn authenticated_user(
    state: &DynamicPricingState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let credential =
        dashboard_credential(headers).ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|_| dashboard_auth_error(headers, None))?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(Principal { user, credential })
}

async fn parse_setting_update(request: Request) -> Result<DynamicPricingSettingUpdate, ()> {
    let bytes = to_bytes(request.into_body(), BODY_LIMIT_BYTES)
        .await
        .map_err(|_| ())?;
    serde_json::from_slice(&bytes).map_err(|_| ())
}

fn parse_f64_option(value: Option<&String>) -> Option<f64> {
    value.and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite())
}

fn parse_i64_option(value: Option<&String>) -> Option<i64> {
    value.and_then(|value| value.parse::<i64>().ok())
}

fn format_float(value: f64) -> String {
    if value.fract() == 0.0 {
        format!("{value:.0}")
    } else {
        value.to_string()
    }
}

fn validate_non_negative(name: &str, value: f64) -> Result<(), String> {
    if value < 0.0 || !value.is_finite() {
        return Err(format!("{name} must be finite and non-negative"));
    }
    Ok(())
}

fn validate_unit_interval(name: &str, value: f64) -> Result<(), String> {
    if value < 0.0 || value > 1.0 || !value.is_finite() {
        return Err(format!("{name} must be finite and in [0, 1]"));
    }
    Ok(())
}

fn is_finite_positive(value: f64) -> bool {
    value.is_finite() && value > 0.0
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

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<crate::auth::AuthErrorKind>) -> Response {
    let (status, code, english) = match kind {
        Some(crate::auth::AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(crate::auth::AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(crate::auth::AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        Some(crate::auth::AuthErrorKind::UserDisabled) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    coded_error(
        status,
        code,
        if accepts_chinese(headers) {
            match code {
                "AUTH_INTERNAL_ERROR" => "数据库出错，请联系管理员",
                "AUTH_TOKEN_EXPIRED" | "AUTH_SESSION_REVOKED" => {
                    "无权进行此操作，未登录且未提供 access token"
                }
                "AUTH_USER_DISABLED" => "用户已被封禁",
                _ => "无权进行此操作，access token 无效",
            }
        } else {
            english
        },
    )
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

fn invalid_parameters(_headers: &HeaderMap) -> Response {
    api_error("invalid dynamic pricing settings".to_owned())
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn client_ip(headers: &HeaderMap) -> String {
    headers
        .get("x-forwarded-for")
        .or_else(|| headers.get("x-real-ip"))
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or("unknown")
        .to_owned()
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn api_success(data: Value) -> Response {
    Json(json!({"success": true, "message": "", "data": data})).into_response()
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

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cost_floor_multiplier_should_match_go_engine() {
        assert_eq!(cost_floor_multiplier(1.25, 1.0, 1.2), 1.5);
        assert_eq!(cost_floor_multiplier(0.0, 1.0, 1.2), 0.0);
    }

    #[test]
    fn validate_should_reject_enabled_without_positive_base_price() {
        let mut setting = DynamicPricingSetting::default();
        setting.enabled = true;
        setting.base_price_usd_per_million = 0.0;
        assert!(validate_dynamic_pricing_setting(&setting).is_err());
    }
}
