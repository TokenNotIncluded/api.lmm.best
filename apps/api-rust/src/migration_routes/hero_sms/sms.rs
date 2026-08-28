use std::{
    collections::{BTreeMap, HashSet},
    str::FromStr,
};

use async_trait::async_trait;
use axum::{
    Router,
    extract::{Path, Query, Request, State},
    http::{HeaderMap, StatusCode},
    response::Response,
    routing::{delete, get, post},
};
use rust_decimal::Decimal;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{Acquire, FromRow, PgPool, Postgres};
use uuid::Uuid;

use super::*;

const QUOTE_VERSION: i32 = 2;
const QUOTE_TTL_SECONDS: i64 = 120;
const COMPLAINT_WAIT_SECONDS: i64 = 120;
const COMPLAINT_RETRY_SECONDS: i64 = 30;
const COMPLAINT_MAX_ATTEMPTS: i32 = 3;
const SMS_PURCHASE_LOCK: i64 = 0x4c4d4d534d530001;
const SMS_STATE_OK: &str = "STATUS_OK";
const SMS_STATE_CANCEL: &str = "STATUS_CANCEL";
const ACTIVE_LIMIT: i64 = 20;
const CURRENT_STATUSES: &[&str] = &[
    "pending_provider",
    "purchase_unknown",
    "active",
    "cancel_pending",
];
const TERMINAL_STATUSES: &[&str] = &["completed", "cancelled", "failed"];

#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsCountry {
    pub id: i32,
    pub name: String,
    pub english_name: String,
    pub chinese_name: String,
    pub visible: bool,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsService {
    pub code: String,
    pub name: String,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsPriceTier {
    pub count: i32,
    pub price: String,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsOffer {
    pub country_id: i32,
    pub service: String,
    pub count: i32,
    pub price: String,
    pub tiers: Vec<SmsPriceTier>,
}
#[derive(Clone, Debug)]
pub struct SmsPurchase {
    pub country_id: i32,
    pub service: String,
    pub operator: String,
    pub max_price: String,
    pub currency_code: i32,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsActivation {
    pub id: String,
    pub phone_number: String,
    pub cost: String,
    pub currency_code: i32,
    pub country_code: i32,
    pub operator: String,
    pub expires_at: i64,
}
#[derive(Clone, Debug, Serialize, Deserialize)]
pub struct SmsActiveActivation {
    pub id: String,
    pub service: String,
    pub phone_number: String,
    pub cost: String,
    pub currency_code: i32,
    pub status: i32,
    pub code: String,
    pub text: String,
    pub operator: String,
    pub country_code: i32,
    pub expires_at: i64,
}
#[derive(Clone, Debug, Default, Serialize, Deserialize)]
pub struct SmsStatus {
    pub code: String,
    pub text: String,
}

#[async_trait]
pub(super) trait SmsUserRateLimiter: Send + Sync {
    async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()>;
}
pub(super) struct AllowSmsUserRateLimiter;
#[async_trait]
impl SmsUserRateLimiter for AllowSmsUserRateLimiter {
    async fn check(&self, _: &str, _: i64) -> Result<CriticalRateLimitOutcome, ()> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }
}
pub(super) struct ValkeySmsUserRateLimiter {
    valkey: redis::Client,
    config: HeroSmsRateLimitConfig,
}
impl ValkeySmsUserRateLimiter {
    pub(super) fn new(valkey: redis::Client, config: HeroSmsRateLimitConfig) -> Self {
        Self { valkey, config }
    }
}
#[async_trait]
impl SmsUserRateLimiter for ValkeySmsUserRateLimiter {
    async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()> {
        if !self.config.enabled {
            return Ok(CriticalRateLimitOutcome::Allowed);
        }
        let counter = increment_sms_rate_limit(self, scope, user_id).await?;
        Ok(sms_rate_limit_outcome(counter, self.config.max_requests))
    }
}

async fn increment_sms_rate_limit(
    limiter: &ValkeySmsUserRateLimiter,
    scope: &str,
    user_id: i64,
) -> Result<(u64, i64), ()> {
    let timeout = limiter.config.dependency_timeout;
    let connection = limiter.valkey.get_multiplexed_async_connection();
    let mut connection = tokio::time::timeout(timeout, connection)
        .await
        .map_err(|_| ())?
        .map_err(|_| ())?;
    let script = redis::Script::new(
        "local c=redis.call('INCR',KEYS[1]); if c==1 then redis.call('EXPIRE',KEYS[1],ARGV[2]) end; local t=redis.call('TTL',KEYS[1]); return {c,t}",
    );
    let key = sms_user_rate_limit_key(scope, user_id);
    let mut invocation = script.prepare_invoke();
    invocation
        .key(key)
        .arg(limiter.config.max_requests)
        .arg(limiter.config.window.as_secs().max(1));
    let result = invocation.invoke_async(&mut connection);
    tokio::time::timeout(timeout, result)
        .await
        .map_err(|_| ())?
        .map_err(|_| ())
}

fn sms_rate_limit_outcome((requests, ttl): (u64, i64), maximum: u64) -> CriticalRateLimitOutcome {
    if requests <= maximum {
        CriticalRateLimitOutcome::Allowed
    } else {
        CriticalRateLimitOutcome::Rejected {
            retry_after_seconds: u64::try_from(ttl.max(1)).unwrap_or(1),
        }
    }
}

fn sms_user_rate_limit_key(scope: &str, user_id: i64) -> String {
    format!("rateLimit:v2:user:UC:{scope}:{user_id}")
}

pub(super) fn routes() -> Router<HeroSmsState> {
    Router::new()
        .route("/api/hero-sms/sms/countries", get(countries))
        .route("/api/hero-sms/sms/services", get(services))
        .route("/api/hero-sms/sms/operators", get(operators))
        .route("/api/hero-sms/sms/offer", get(offer))
        .route(
            "/api/hero-sms/sms/orders",
            get(list_orders).post(create_order),
        )
        .route("/api/hero-sms/sms/orders/current", get(current_order))
        .route("/api/hero-sms/sms/orders/current-list", get(current_list))
        .route("/api/hero-sms/sms/history", delete(clear_history))
        .route("/api/hero-sms/sms/history/{id}", delete(hide_history))
        .route("/api/hero-sms/sms/orders/{id}", get(get_order))
        .route("/api/hero-sms/sms/orders/{id}/complaints", post(complaint))
        .route("/api/hero-sms/sms/orders/{id}/cancel", post(cancel))
}

#[derive(Serialize)]
struct CountryView {
    id: i32,
    name: String,
    english_name: String,
    chinese_name: String,
    popularity: i64,
}
#[derive(Serialize)]
struct ServiceView {
    code: String,
    name: String,
    popularity: i64,
}
#[derive(Clone, Serialize)]
struct TierView {
    id: String,
    inventory: i32,
    customer_price_usd: String,
    charge_quota: i64,
}
#[derive(Serialize)]
struct OfferView {
    id: String,
    country_id: i32,
    service: String,
    operator: String,
    inventory: i32,
    customer_price_usd: String,
    charge_quota: i64,
    bid: bool,
    tiers: Vec<TierView>,
}
#[derive(Debug, Default, Deserialize, Serialize)]
struct CreateInput {
    #[serde(default)]
    offer_id: String,
}
#[derive(Default, Deserialize)]
struct ComplaintInput {
    #[serde(default)]
    reason: String,
}
#[derive(Serialize)]
struct OrderPage {
    items: Vec<OrderView>,
    page: i32,
    size: i32,
    total: i64,
}
#[derive(Clone, Serialize)]
struct OrderView {
    id: String,
    country_id: i32,
    service: String,
    operator: String,
    status: String,
    customer_price_usd: String,
    charge_quota: i64,
    refunded_quota: i64,
    provider_id: Option<String>,
    can_cancel: bool,
    can_complain: bool,
    complaint_type: String,
    complaint_status: String,
    complaint_submitted_at: i64,
    phone_number: String,
    code: String,
    message: String,
    last_error_code: String,
    last_error_message: String,
    created_at: i64,
    updated_at: i64,
    #[serde(skip_serializing_if = "is_zero_i64")]
    expires_at: i64,
}
fn is_zero_i64(value: &i64) -> bool {
    *value == 0
}

#[derive(Clone, FromRow)]
struct OrderRow {
    id: String,
    user_id: i64,
    idempotency_key_hash: String,
    request_payload_hash: String,
    country_id: i32,
    service: String,
    operator: String,
    status: String,
    price_multiplier: String,
    provider_price_cny: String,
    customer_price_usd: String,
    reserved_quota: i64,
    charge_quota: i64,
    refunded_quota: i64,
    provider_id: Option<String>,
    provider_currency_code: i32,
    phone_ciphertext: String,
    code_ciphertext: String,
    message_ciphertext: String,
    provider_snapshot_ciphertext: String,
    complaint_type: String,
    complaint_status: String,
    complaint_submitted_at: i64,
    complaint_submit_attempts: i32,
    complaint_next_retry_at: i64,
    complaint_last_checked_at: i64,
    provider_cancel_accepted_at: i64,
    cancel_final_status: String,
    cancel_error_code: String,
    cancel_error_message: String,
    last_error_code: String,
    last_error_message: String,
    provider_request_started_at: i64,
    provider_expires_at: i64,
    completed_at: Option<i64>,
    history_hidden_at: i64,
    created_at: i64,
    updated_at: i64,
}
const ORDER_SELECT: &str = "id,user_id,idempotency_key_hash,request_payload_hash,country_id,service,COALESCE(operator,'') operator,status,price_multiplier,provider_price_cny,customer_price_usd,reserved_quota,charge_quota,refunded_quota,provider_id,provider_currency_code,COALESCE(phone_ciphertext,'') phone_ciphertext,COALESCE(code_ciphertext,'') code_ciphertext,COALESCE(message_ciphertext,'') message_ciphertext,COALESCE(provider_snapshot_ciphertext,'') provider_snapshot_ciphertext,COALESCE(complaint_type,'') complaint_type,COALESCE(complaint_status,'') complaint_status,complaint_submitted_at,complaint_submit_attempts,complaint_next_retry_at,complaint_last_checked_at,provider_cancel_accepted_at,COALESCE(cancel_final_status,'') cancel_final_status,COALESCE(cancel_error_code,'') cancel_error_code,COALESCE(cancel_error_message,'') cancel_error_message,COALESCE(last_error_code,'') last_error_code,COALESCE(last_error_message,'') last_error_message,provider_request_started_at,provider_expires_at,completed_at,history_hidden_at,created_at,updated_at";

#[derive(Serialize, Deserialize)]
struct Quote {
    version: i32,
    user_id: i64,
    country_id: i32,
    service: String,
    operator: String,
    cost_cny: String,
    multiplier: String,
    currency_code: i32,
    issued_at: i64,
    bid: bool,
}

async fn authenticated(
    state: &HeroSmsState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    require_user(state, headers).await
}

async fn authenticated_user_id(state: &HeroSmsState, headers: &HeaderMap) -> Result<i64, Response> {
    authenticated(state, headers).await.map(|user| user.id)
}

async fn prepare_empty_mutation(
    state: &HeroSmsState,
    headers: &HeaderMap,
    request: Request,
    scope: &str,
) -> Result<i64, Response> {
    let user_id = authenticated_user_id(state, headers).await?;
    bounded_body(request).await?;
    mutation_limit(state, headers, scope, user_id).await?;
    Ok(user_id)
}

async fn prepare_json_mutation<T: serde::de::DeserializeOwned + Default>(
    state: &HeroSmsState,
    headers: &HeaderMap,
    request: Request,
    scope: &str,
) -> Result<(i64, T), Response> {
    let user_id = authenticated_user_id(state, headers).await?;
    let input = parse_json::<T>(request).await?;
    mutation_limit(state, headers, scope, user_id).await?;
    Ok((user_id, input))
}

async fn mutation_limit(
    state: &HeroSmsState,
    headers: &HeaderMap,
    scope: &str,
    user_id: i64,
) -> Result<(), Response> {
    if let Some(response) = user_critical_limit(state, headers).await {
        return Err(response);
    }
    match state.sms_user_rate_limiter.check(scope, user_id).await {
        Ok(CriticalRateLimitOutcome::Allowed) => Ok(()),
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Err(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(()) => Err(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}
async fn configured(state: &HeroSmsState) -> Result<String, HeroSmsApiError> {
    let options = load_options(&state.pg).await?;
    if !purchasing_enabled(&options)
        || option_value(&options, OPTION_SMS_ENABLED, "false") != "true"
    {
        return Err(not_configured());
    }
    let encrypted = option_value(&options, OPTION_API_KEY, "");
    if encrypted.is_empty() {
        return Err(not_configured());
    }
    let key = configured_api_key(&options).await?;
    if key.trim().is_empty() {
        Err(not_configured())
    } else {
        Ok(key)
    }
}
async fn ensure_authenticated(state: &HeroSmsState, headers: &HeaderMap) -> Result<(), Response> {
    authenticated(state, headers)
        .await
        .map(|_| ())
        .map_err(done)
}

async fn catalog_context<T>(
    state: &HeroSmsState,
    headers: &HeaderMap,
    validate: impl FnOnce() -> Result<T, Response>,
) -> Result<(T, String), Response> {
    ensure_authenticated(state, headers).await?;
    let validated = validate()?;
    let key = configured_key_response(state).await?;
    Ok((validated, key))
}

async fn configured_key_response(state: &HeroSmsState) -> Result<String, Response> {
    configured(state)
        .await
        .map_err(|error| done(hero_error(error)))
}

async fn sms_operators(
    state: &HeroSmsState,
    key: &str,
    country: i32,
) -> Result<Vec<String>, Response> {
    state
        .gateway
        .list_sms_operators(key, country)
        .await
        .map_err(|error| done(hero_error(map_provider_error(error))))
}

fn country_query(query: &BTreeMap<String, String>) -> Result<i32, Response> {
    query
        .get("country")
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|value| *value >= 0)
        .ok_or_else(|| done(hero_error(invalid_request())))
}

async fn current_rows_for_user(
    state: &HeroSmsState,
    headers: &HeaderMap,
    limit: i64,
) -> Result<Vec<OrderRow>, Response> {
    let user_id = authenticated_user_id(state, headers).await.map_err(done)?;
    current_rows(&state.pg, user_id, limit)
        .await
        .map_err(|error| done(hero_error(error)))
}

fn done(response: Response) -> Response {
    disable_cache(response)
}
fn ok(data: Value) -> Response {
    done(hero_success(data))
}

async fn countries(
    State(state): State<HeroSmsState>,
    Query(q): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let (service, key) = match catalog_context(&state, &headers, || {
        let service = q.get("service").map(|value| value.trim()).unwrap_or("");
        if service.len() > 64 {
            Err(done(hero_error(invalid_request())))
        } else {
            Ok(service)
        }
    })
    .await
    {
        Ok(context) => context,
        Err(response) => return response,
    };
    let rows = match state.gateway.list_sms_countries(&key).await {
        Ok(v) => v,
        Err(e) => return done(hero_error(map_provider_error(e))),
    };
    let popularity: BTreeMap<i32,i64>=sqlx::query_as::<_,(i32,i64)>(if service.is_empty(){"SELECT country_id,COUNT(*)::BIGINT FROM hero_sms_sms_orders GROUP BY country_id"}else{"SELECT country_id,COUNT(*)::BIGINT FROM hero_sms_sms_orders WHERE service=$1 GROUP BY country_id"}).bind(service).fetch_all(&state.pg).await.unwrap_or_default().into_iter().collect();
    let mut views = rows
        .into_iter()
        .filter(|c| c.visible && !c.name.trim().is_empty())
        .map(|c| CountryView {
            id: c.id,
            name: c.name.trim().into(),
            english_name: c.english_name.trim().into(),
            chinese_name: c.chinese_name.trim().into(),
            popularity: *popularity.get(&c.id).unwrap_or(&0),
        })
        .collect::<Vec<_>>();
    views.sort_by_key(|v| (std::cmp::Reverse(v.popularity), v.id));
    ok(json!(views))
}
async fn services(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    let (_, key) = match catalog_context(&state, &headers, || Ok(())).await {
        Ok(context) => context,
        Err(response) => return response,
    };
    let rows = match state.gateway.list_sms_services(&key).await {
        Ok(v) => v,
        Err(e) => return done(hero_error(map_provider_error(e))),
    };
    let popularity: BTreeMap<String, i64> = sqlx::query_as::<_, (String, i64)>(
        "SELECT service,COUNT(*)::BIGINT FROM hero_sms_sms_orders GROUP BY service",
    )
    .fetch_all(&state.pg)
    .await
    .unwrap_or_default()
    .into_iter()
    .collect();
    let mut views = rows
        .into_iter()
        .filter(|s| !s.code.trim().is_empty() && !s.name.trim().is_empty())
        .map(|s| ServiceView {
            popularity: *popularity.get(s.code.trim()).unwrap_or(&0),
            code: s.code.trim().into(),
            name: s.name.trim().into(),
        })
        .collect::<Vec<_>>();
    views.sort_by(|a, b| b.popularity.cmp(&a.popularity).then(a.code.cmp(&b.code)));
    ok(json!(views))
}
async fn operators(
    State(state): State<HeroSmsState>,
    Query(q): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let (country, key) = match catalog_context(&state, &headers, || country_query(&q)).await {
        Ok(context) => context,
        Err(response) => return response,
    };
    match sms_operators(&state, &key, country).await {
        Ok(operators) => ok(json!(operators)),
        Err(response) => response,
    }
}
async fn offer(
    State(state): State<HeroSmsState>,
    Query(q): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let user = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(r) => return done(r),
    };
    let country = match country_query(&q) {
        Ok(country) => country,
        Err(response) => return response,
    };
    let service = q.get("service").map(|v| v.trim()).unwrap_or("");
    let mut operator = q.get("operator").map(|v| v.trim()).unwrap_or("").to_owned();
    if operator.eq_ignore_ascii_case("any") {
        operator.clear()
    }
    if service.is_empty() || service.len() > 64 || operator.len() > 64 {
        return done(hero_error(invalid_request()));
    }
    let key = match configured_key_response(&state).await {
        Ok(key) => key,
        Err(response) => return response,
    };
    if !operator.is_empty() {
        let ops = match sms_operators(&state, &key, country).await {
            Ok(operators) => operators,
            Err(response) => return response,
        };
        match ops.into_iter().find(|v| v.eq_ignore_ascii_case(&operator)) {
            Some(v) => operator = v,
            None => return done(hero_error(invalid_request())),
        }
    }
    let provider = match state.gateway.get_sms_offer(&key, country, service).await {
        Ok(v) => v,
        Err(e) => return done(hero_error(map_provider_error(e))),
    };
    let options = match load_options(&state.pg).await {
        Ok(v) => v,
        Err(e) => return done(hero_error(e)),
    };
    let multiplier = option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER);
    let multiplier_dec = match Decimal::from_str(&multiplier)
        .ok()
        .filter(|v| *v > Decimal::ZERO)
    {
        Some(v) => v,
        None => return done(hero_error(not_configured())),
    };
    let max = q.get("max_price_usd");
    let bid = max.is_some();
    let max_customer = max
        .and_then(|v| Decimal::from_str(v.trim()).ok())
        .filter(|v| *v > Decimal::ZERO && v.scale() <= 6);
    if bid && max_customer.is_none() {
        return done(hero_error(invalid_request()));
    }
    let mut tiers = Vec::new();
    for tier in &provider.tiers {
        let cost = match Decimal::from_str(&tier.price)
            .ok()
            .filter(|v| *v > Decimal::ZERO)
        {
            Some(v) => v,
            None => continue,
        };
        let customer = cost * multiplier_dec;
        let charge = match charge_quota_decimal(customer, &options) {
            Ok(v) => v,
            Err(e) => return done(hero_error(e)),
        };
        let quote = Quote {
            version: QUOTE_VERSION,
            user_id: user.id,
            country_id: country,
            service: service.into(),
            operator: operator.clone(),
            cost_cny: cost.normalize().to_string(),
            multiplier: multiplier.clone(),
            currency_code: HERO_SMS_CURRENCY_CODE,
            issued_at: now_unix(),
            bid: false,
        };
        let id = match encode_quote(&quote) {
            Ok(v) => v,
            Err(e) => return done(hero_error(e)),
        };
        tiers.push(TierView {
            id,
            inventory: tier.count,
            customer_price_usd: customer.normalize().to_string(),
            charge_quota: charge,
        });
    }
    if tiers.is_empty() {
        return done(hero_error(price_changed()));
    }
    let selected = if let Some(maximum) = max_customer {
        let provider_max = maximum / multiplier_dec;
        let inventory = provider
            .tiers
            .iter()
            .filter_map(|t| Decimal::from_str(&t.price).ok().map(|p| (p, t.count)))
            .filter(|(p, c)| *c > 0 && *p <= provider_max)
            .map(|(_, c)| c)
            .max()
            .unwrap_or(0);
        if inventory <= 0 {
            return done(hero_error(price_changed()));
        }
        let quote = Quote {
            version: QUOTE_VERSION,
            user_id: user.id,
            country_id: country,
            service: service.into(),
            operator: operator.clone(),
            cost_cny: provider_max.normalize().to_string(),
            multiplier: multiplier.clone(),
            currency_code: HERO_SMS_CURRENCY_CODE,
            issued_at: now_unix(),
            bid: true,
        };
        TierView {
            id: match encode_quote(&quote) {
                Ok(v) => v,
                Err(e) => return done(hero_error(e)),
            },
            inventory,
            customer_price_usd: maximum.normalize().to_string(),
            charge_quota: match charge_quota_decimal(maximum, &options) {
                Ok(v) => v,
                Err(e) => return done(hero_error(e)),
            },
        }
    } else {
        tiers[0].clone()
    };
    ok(json!(OfferView {
        id: selected.id,
        country_id: country,
        service: service.into(),
        operator,
        inventory: selected.inventory,
        customer_price_usd: selected.customer_price_usd,
        charge_quota: selected.charge_quota,
        bid,
        tiers
    }))
}

async fn create_order(
    State(state): State<HeroSmsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let (user_id, input) = match prepare_json_mutation::<CreateInput>(
        &state,
        &headers,
        request,
        "hero-sms-sms-purchase",
    )
    .await
    {
        Ok(prepared) => prepared,
        Err(response) => return done(response),
    };
    let idem = headers
        .get("Idempotency-Key")
        .and_then(|v| v.to_str().ok())
        .map(str::trim)
        .unwrap_or("");
    if idem.is_empty() || idem.len() > 128 || input.offer_id.trim().is_empty() {
        return done(hero_error(invalid_request()));
    }
    match purchase(&state, user_id, idem, &input).await {
        Ok((view, quota, status)) => done(hero_success_status(
            status,
            json!({"order":view,"quota":quota}),
        )),
        Err(e) => done(hero_error(e)),
    }
}
async fn list_orders(
    State(state): State<HeroSmsState>,
    Query(q): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let u = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(r) => return done(r),
    };
    let (page, size) = page_size(&q);
    let summary = q
        .get("summary")
        .is_some_and(|v| v.eq_ignore_ascii_case("true"));
    match order_page(&state.pg, u.id, page, size, summary).await {
        Ok(v) => ok(json!(v)),
        Err(e) => done(hero_error(e)),
    }
}
async fn current_order(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    let mut rows = match current_rows_for_user(&state, &headers, 1).await {
        Ok(rows) => rows,
        Err(response) => return response,
    };
    let order = if rows.is_empty() {
        Value::Null
    } else {
        json!(to_view(&rows.remove(0), false).unwrap_or_else(|_| empty_view()))
    };
    ok(json!({"order":order}))
}
async fn current_list(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    let rows = match current_rows_for_user(&state, &headers, ACTIVE_LIMIT).await {
        Ok(rows) => rows,
        Err(response) => return response,
    };
    let items = rows
        .iter()
        .filter_map(|row| to_view(row, false).ok())
        .collect::<Vec<_>>();
    ok(json!({"items":items}))
}
async fn get_order(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let u = match authenticated(&state, &headers).await {
        Ok(v) => v,
        Err(r) => return done(r),
    };
    match refresh(&state, u.id, id.trim()).await {
        Ok(v) => ok(json!({"order":v})),
        Err(e) => done(hero_error(e)),
    }
}
async fn clear_history(
    State(state): State<HeroSmsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let user_id =
        match prepare_empty_mutation(&state, &headers, request, "hero-sms-sms-history-clear").await
        {
            Ok(user_id) => user_id,
            Err(response) => return done(response),
        };
    match sqlx::query("UPDATE hero_sms_sms_orders SET history_hidden_at=$2 WHERE user_id=$1 AND history_hidden_at=0 AND status=ANY($3)").bind(user_id).bind(now_unix()).bind(TERMINAL_STATUSES).execute(&state.pg).await{Ok(v)=>ok(json!({"hidden_count":v.rows_affected()})),Err(_)=>done(hero_error(internal_error()))}
}
async fn hide_history(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let user_id = match prepare_empty_mutation(
        &state,
        &headers,
        request,
        "hero-sms-sms-history-hide",
    )
    .await
    {
        Ok(user_id) => user_id,
        Err(response) => return done(response),
    };
    match hide_one(&state.pg, user_id, id.trim()).await {
        Ok(()) => ok(json!({"hidden":true})),
        Err(e) => done(hero_error(e)),
    }
}
async fn complaint(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let (user_id, body) = match prepare_json_mutation::<ComplaintInput>(
        &state,
        &headers,
        request,
        "hero-sms-sms-complaint",
    )
    .await
    {
        Ok(prepared) => prepared,
        Err(response) => return done(response),
    };
    match submit_complaint(&state, user_id, id.trim(), body.reason.trim()).await {
        Ok(v) => done(hero_success_status(
            StatusCode::ACCEPTED,
            json!({"order":v}),
        )),
        Err(e) => done(hero_error(e)),
    }
}
async fn cancel(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let user_id =
        match prepare_empty_mutation(&state, &headers, request, "hero-sms-sms-cancel").await {
            Ok(user_id) => user_id,
            Err(response) => return done(response),
        };
    match cancel_order(&state, user_id, id.trim()).await {
        Ok((v, q)) => {
            let s = if v.status == "cancel_pending" {
                StatusCode::ACCEPTED
            } else {
                StatusCode::OK
            };
            done(hero_success_status(s, json!({"order":v,"quota":q})))
        }
        Err(e) => done(hero_error(e)),
    }
}

fn page_size(q: &BTreeMap<String, String>) -> (i32, i32) {
    let page = q
        .get("page")
        .and_then(|v| v.parse().ok())
        .filter(|v| *v > 0)
        .unwrap_or(1);
    let size = q
        .get("size")
        .and_then(|v| v.parse().ok())
        .filter(|v| *v > 0 && *v <= 100)
        .unwrap_or(20);
    (page, size)
}
async fn fetch_order(pg: &PgPool, user: i64, id: &str) -> Result<OrderRow, HeroSmsApiError> {
    let sql = format!("SELECT {ORDER_SELECT} FROM hero_sms_sms_orders WHERE id=$1 AND user_id=$2");
    sqlx::query_as(&sql)
        .bind(id)
        .bind(user)
        .fetch_optional(pg)
        .await
        .map_err(|_| internal_error())?
        .ok_or_else(order_not_found)
}
async fn current_rows(
    pg: &PgPool,
    user: i64,
    limit: i64,
) -> Result<Vec<OrderRow>, HeroSmsApiError> {
    let sql = format!(
        "SELECT {ORDER_SELECT} FROM hero_sms_sms_orders WHERE user_id=$1 AND status=ANY($2) ORDER BY created_at DESC LIMIT $3"
    );
    sqlx::query_as(&sql)
        .bind(user)
        .bind(CURRENT_STATUSES)
        .bind(limit)
        .fetch_all(pg)
        .await
        .map_err(|_| internal_error())
}
async fn order_page(
    pg: &PgPool,
    user: i64,
    page: i32,
    size: i32,
    summary: bool,
) -> Result<OrderPage, HeroSmsApiError> {
    let total = sqlx::query_scalar(
        "SELECT COUNT(*) FROM hero_sms_sms_orders WHERE user_id=$1 AND history_hidden_at=0",
    )
    .bind(user)
    .fetch_one(pg)
    .await
    .map_err(|_| internal_error())?;
    let sql = format!(
        "SELECT {ORDER_SELECT} FROM hero_sms_sms_orders WHERE user_id=$1 AND history_hidden_at=0 ORDER BY created_at DESC LIMIT $2 OFFSET $3"
    );
    let rows = sqlx::query_as::<_, OrderRow>(&sql)
        .bind(user)
        .bind(i64::from(size))
        .bind(i64::from((page - 1) * size))
        .fetch_all(pg)
        .await
        .map_err(|_| internal_error())?;
    let items = rows
        .iter()
        .map(|r| to_view(r, summary))
        .collect::<Result<_, _>>()?;
    Ok(OrderPage {
        items,
        page,
        size,
        total,
    })
}

fn to_view(r: &OrderRow, summary: bool) -> Result<OrderView, HeroSmsApiError> {
    let retry = r.complaint_status.is_empty() || r.complaint_status == "failed";
    Ok(OrderView {
        id: r.id.clone(),
        country_id: r.country_id,
        service: r.service.clone(),
        operator: r.operator.clone(),
        status: r.status.clone(),
        customer_price_usd: r.customer_price_usd.clone(),
        charge_quota: r.charge_quota,
        refunded_quota: r.refunded_quota,
        provider_id: r.provider_id.clone(),
        can_cancel: r.status == "active" && r.provider_id.is_some(),
        can_complain: r.status == "active"
            && r.provider_id.is_some()
            && r.code_ciphertext.is_empty()
            && retry
            && now_unix() >= r.created_at + COMPLAINT_WAIT_SECONDS,
        complaint_type: r.complaint_type.clone(),
        complaint_status: r.complaint_status.clone(),
        complaint_submitted_at: r.complaint_submitted_at,
        phone_number: decrypt_payload(&r.phone_ciphertext)?,
        code: if summary {
            String::new()
        } else {
            decrypt_payload(&r.code_ciphertext)?
        },
        message: if summary {
            String::new()
        } else {
            decrypt_payload(&r.message_ciphertext)?
        },
        last_error_code: r.last_error_code.clone(),
        last_error_message: r.last_error_message.clone(),
        created_at: r.created_at,
        updated_at: r.updated_at,
        expires_at: r.provider_expires_at,
    })
}
fn empty_view() -> OrderView {
    OrderView {
        id: String::new(),
        country_id: 0,
        service: String::new(),
        operator: String::new(),
        status: String::new(),
        customer_price_usd: String::new(),
        charge_quota: 0,
        refunded_quota: 0,
        provider_id: None,
        can_cancel: false,
        can_complain: false,
        complaint_type: String::new(),
        complaint_status: String::new(),
        complaint_submitted_at: 0,
        phone_number: String::new(),
        code: String::new(),
        message: String::new(),
        last_error_code: String::new(),
        last_error_message: String::new(),
        created_at: 0,
        updated_at: 0,
        expires_at: 0,
    }
}

async fn purchase(
    state: &HeroSmsState,
    user: i64,
    idem: &str,
    input: &CreateInput,
) -> Result<(OrderView, i64, StatusCode), HeroSmsApiError> {
    let idem_hash = hash(idem);
    let payload_hash = hash(&serde_json::to_string(input).map_err(|_| invalid_request())?);
    if let Some(v) = replay(&state.pg, user, &idem_hash, &payload_hash).await? {
        return Ok(v);
    }
    let key = configured(state).await?;
    let quote = decode_quote(input.offer_id.trim())?;
    if quote.version != QUOTE_VERSION
        || quote.user_id != user
        || quote.currency_code != HERO_SMS_CURRENCY_CODE
        || now_unix() - quote.issued_at > QUOTE_TTL_SECONDS
    {
        return Err(price_changed());
    }
    let options = load_options(&state.pg).await?;
    if option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER) != quote.multiplier {
        return Err(price_changed());
    }
    let reserved = Decimal::from_str(&quote.cost_cny)
        .ok()
        .filter(|v| *v > Decimal::ZERO)
        .ok_or_else(price_changed)?;
    let provider = state
        .gateway
        .get_sms_offer(&key, quote.country_id, &quote.service)
        .await
        .map_err(map_provider_error)?;
    if !inventory(&provider, reserved) {
        return Err(price_changed());
    }
    let multiplier = Decimal::from_str(&quote.multiplier).map_err(|_| price_changed())?;
    let customer = reserved * multiplier;
    let charge = charge_quota_decimal(customer, &options)?;
    let mut connection = state.pg.acquire().await.map_err(|_| internal_error())?;
    sqlx::query("SELECT pg_advisory_lock($1)")
        .bind(SMS_PURCHASE_LOCK)
        .execute(&mut *connection)
        .await
        .map_err(|_| internal_error())?;
    let result = purchase_locked(
        state,
        &mut connection,
        user,
        &idem_hash,
        &payload_hash,
        &quote,
        customer,
        charge,
        &key,
    )
    .await;
    let _ = sqlx::query("SELECT pg_advisory_unlock($1)")
        .bind(SMS_PURCHASE_LOCK)
        .execute(&mut *connection)
        .await;
    result
}
async fn purchase_locked(
    state: &HeroSmsState,
    conn: &mut sqlx::pool::PoolConnection<Postgres>,
    user: i64,
    idem_hash: &str,
    payload_hash: &str,
    quote: &Quote,
    customer: Decimal,
    charge: i64,
    key: &str,
) -> Result<(OrderView, i64, StatusCode), HeroSmsApiError> {
    if let Some(v) = replay(&state.pg, user, idem_hash, payload_hash).await? {
        return Ok(v);
    }
    if now_unix() - quote.issued_at > QUOTE_TTL_SECONDS {
        return Err(price_changed());
    }
    let locked_options = load_options(&state.pg).await?;
    if option_value(&locked_options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER)
        != quote.multiplier
    {
        return Err(price_changed());
    }
    if !quote.operator.is_empty() {
        let operators = state
            .gateway
            .list_sms_operators(key, quote.country_id)
            .await
            .map_err(map_provider_error)?;
        if !operators
            .iter()
            .any(|candidate| candidate.trim().eq_ignore_ascii_case(&quote.operator))
        {
            return Err(price_changed());
        }
    }
    let locked_offer = state
        .gateway
        .get_sms_offer(key, quote.country_id, &quote.service)
        .await
        .map_err(map_provider_error)?;
    let reserved = Decimal::from_str(&quote.cost_cny).map_err(|_| price_changed())?;
    if !inventory(&locked_offer, reserved) {
        return Err(price_changed());
    }
    let active_before = state
        .gateway
        .list_active_sms_activations(key)
        .await
        .map_err(map_provider_error)?;
    let snapshot = encrypt_persistent(
        "hero_sms.sms.snapshot",
        &serde_json::to_string(&active_before).map_err(|_| internal_error())?,
    )?;
    let id = format!("hssms_{}", Uuid::new_v4().simple());
    let now = now_unix();
    let mut tx = conn.begin().await.map_err(|_| internal_error())?;
    let quota: i64 = sqlx::query_scalar("SELECT quota FROM users WHERE id=$1 FOR UPDATE")
        .bind(user)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| internal_error())?
        .ok_or_else(order_not_found)?;
    if quota < charge {
        return Err(HeroSmsApiError {
            status: StatusCode::PAYMENT_REQUIRED,
            code: "INSUFFICIENT_QUOTA",
            message: "insufficient quota",
        });
    }
    sqlx::query("INSERT INTO hero_sms_sms_orders(id,user_id,idempotency_key_hash,request_payload_hash,country_id,service,operator,status,price_multiplier,provider_price_cny,customer_price_usd,reserved_quota,charge_quota,refunded_quota,provider_currency_code,provider_snapshot_ciphertext,last_error_code,last_error_message,provider_request_started_at,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,'pending_provider',$8,$9,$10,$11,$11,0,$12,$13,'PROVIDER_INTENT_PENDING','provider purchase intent is reserved but not started',$14,$14,$14)").bind(&id).bind(user).bind(idem_hash).bind(payload_hash).bind(quote.country_id).bind(&quote.service).bind(&quote.operator).bind(&quote.multiplier).bind(&quote.cost_cny).bind(customer.normalize().to_string()).bind(charge).bind(HERO_SMS_CURRENCY_CODE).bind(snapshot).bind(now).execute(&mut *tx).await.map_err(|_|internal_error())?;
    sqlx::query("INSERT INTO hero_sms_sms_quota_ledgers(user_id,order_id,entry_type,amount_quota,idempotency_key,created_at) VALUES($1,$2,'reserve',$3,$4,$5)")
        .bind(user)
        .bind(&id)
        .bind(-charge)
        .bind(format!("hero_sms:sms:reserve:{id}"))
        .bind(now)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    sqlx::query("UPDATE users SET quota=quota-$2 WHERE id=$1")
        .bind(user)
        .bind(charge)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    tx.commit().await.map_err(|_| internal_error())?;
    sqlx::query("UPDATE hero_sms_sms_orders SET status='purchase_unknown',last_error_code='PROVIDER_ATTEMPT_STARTED',last_error_message='provider purchase attempt may have started',updated_at=$2 WHERE id=$1").bind(&id).bind(now_unix()).execute(&state.pg).await.map_err(|_|internal_error())?;
    let purchase = state
        .gateway
        .purchase_sms_activation(
            key,
            SmsPurchase {
                country_id: quote.country_id,
                service: quote.service.clone(),
                operator: quote.operator.clone(),
                max_price: quote.cost_cny.clone(),
                currency_code: HERO_SMS_CURRENCY_CODE,
            },
        )
        .await;
    match purchase {
        Ok(a) => complete(state, &id, user, charge, a, StatusCode::CREATED).await,
        Err(HeroSmsProviderError::UpstreamTimeout | HeroSmsProviderError::UpstreamBusy) => {
            let row = fetch_order(&state.pg, user, &id).await?;
            let view = reconcile_unknown_purchase(state, row).await?;
            let response_status = if view.status == "active" {
                StatusCode::CREATED
            } else {
                StatusCode::ACCEPTED
            };
            Ok((view, quota - charge, response_status))
        }
        Err(e) => {
            let mapped = map_provider_error(e);
            refund(
                &state.pg,
                &id,
                "failed",
                mapped.code,
                mapped.message,
                &["purchase_unknown"],
            )
            .await?;
            Err(mapped)
        }
    }
}
async fn complete(
    state: &HeroSmsState,
    id: &str,
    user: i64,
    _reserved_charge: i64,
    a: SmsActivation,
    status: StatusCode,
) -> Result<(OrderView, i64, StatusCode), HeroSmsApiError> {
    if a.id.trim().is_empty() || a.phone_number.trim().is_empty() {
        refund(
            &state.pg,
            id,
            "failed",
            "BAD_UPSTREAM_RESPONSE",
            "HeroSMS returned an invalid SMS activation",
            &["purchase_unknown", "pending_provider"],
        )
        .await?;
        return Err(HeroSmsApiError {
            status: StatusCode::BAD_GATEWAY,
            code: "BAD_UPSTREAM_RESPONSE",
            message: "HeroSMS returned an invalid SMS activation",
        });
    }
    let cost = Decimal::from_str(&a.cost).map_err(|_| internal_error())?;
    let phone = encrypt_payload(&a.phone_number)?;
    let options = load_options(&state.pg).await?;
    let mut tx = state.pg.begin().await.map_err(|_| internal_error())?;
    let sql = format!(
        "SELECT {ORDER_SELECT} FROM hero_sms_sms_orders WHERE id=$1 AND user_id=$2 FOR UPDATE"
    );
    let row = sqlx::query_as::<_, OrderRow>(&sql)
        .bind(id)
        .bind(user)
        .fetch_optional(&mut *tx)
        .await
        .map_err(|_| internal_error())?
        .ok_or_else(order_not_found)?;
    if row.status == "active" || row.status == "completed" {
        tx.commit().await.map_err(|_| internal_error())?;
        return Ok((to_view(&row, false)?, quota(&state.pg, user).await?, status));
    }
    if row.status != "purchase_unknown" {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ORDER_STATE_CHANGED",
            message: "HeroSMS SMS order state changed",
        });
    }
    let reserved = Decimal::from_str(&row.provider_price_cny).map_err(|_| internal_error())?;
    let multiplier = Decimal::from_str(&row.price_multiplier).map_err(|_| internal_error())?;
    let actual_charge = charge_quota_decimal(cost * multiplier, &options)?;
    if cost > reserved
        || a.currency_code != HERO_SMS_CURRENCY_CODE
        || actual_charge > row.charge_quota
    {
        tx.rollback().await.map_err(|_| internal_error())?;
        let _ = state
            .gateway
            .set_sms_activation_status(&configured(state).await?, &a.id, 8)
            .await;
        refund(
            &state.pg,
            id,
            "failed",
            if a.currency_code != HERO_SMS_CURRENCY_CODE {
                "CURRENCY_MISMATCH"
            } else {
                "PRICE_CHANGED"
            },
            "provider activation did not match the confirmed quote",
            &["purchase_unknown"],
        )
        .await?;
        return Err(price_changed());
    }
    let price_refund = row.charge_quota - actual_charge;
    if price_refund > 0 {
        sqlx::query("UPDATE users SET quota=quota+$2 WHERE id=$1")
            .bind(user)
            .bind(price_refund)
            .execute(&mut *tx)
            .await
            .map_err(|_| internal_error())?;
        sqlx::query("INSERT INTO hero_sms_sms_quota_ledgers(user_id,order_id,entry_type,amount_quota,idempotency_key,created_at) VALUES($1,$2,'refund',$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING")
            .bind(user)
            .bind(id)
            .bind(price_refund)
            .bind(format!("hero_sms:sms:price_refund:{id}"))
            .bind(now_unix())
            .execute(&mut *tx)
            .await
            .map_err(|_| internal_error())?;
    }
    sqlx::query("UPDATE hero_sms_sms_orders SET status='active',provider_id=$2,provider_currency_code=$3,phone_ciphertext=$4,provider_expires_at=$5,provider_price_cny=$6,customer_price_usd=$7,charge_quota=$8,refunded_quota=refunded_quota+$9,last_error_code='',last_error_message='',updated_at=$10 WHERE id=$1 AND status='purchase_unknown'")
        .bind(id)
        .bind(a.id)
        .bind(a.currency_code)
        .bind(phone)
        .bind(a.expires_at)
        .bind(cost.normalize().to_string())
        .bind((cost * multiplier).normalize().to_string())
        .bind(actual_charge)
        .bind(price_refund)
        .bind(now_unix())
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    tx.commit().await.map_err(|_| internal_error())?;
    let row = fetch_order(&state.pg, user, id).await?;
    Ok((to_view(&row, false)?, quota(&state.pg, user).await?, status))
}
async fn replay(
    pg: &PgPool,
    user: i64,
    idem: &str,
    payload: &str,
) -> Result<Option<(OrderView, i64, StatusCode)>, HeroSmsApiError> {
    let sql = format!(
        "SELECT {ORDER_SELECT} FROM hero_sms_sms_orders WHERE user_id=$1 AND idempotency_key_hash=$2"
    );
    let row = sqlx::query_as::<_, OrderRow>(&sql)
        .bind(user)
        .bind(idem)
        .fetch_optional(pg)
        .await
        .map_err(|_| internal_error())?;
    let Some(row) = row else { return Ok(None) };
    if row.request_payload_hash != payload {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "IDEMPOTENCY_CONFLICT",
            message: "idempotency key was already used for another request",
        });
    }
    let status = if row.status == "active" {
        StatusCode::OK
    } else {
        StatusCode::ACCEPTED
    };
    Ok(Some((
        to_view(&row, false)?,
        quota(pg, user).await?,
        status,
    )))
}
async fn refund(
    pg: &PgPool,
    id: &str,
    status: &str,
    code: &str,
    message: &str,
    expected_statuses: &[&str],
) -> Result<(), HeroSmsApiError> {
    let mut tx = pg.begin().await.map_err(|_| internal_error())?;
    let row: (i64, String, i64, i64) = sqlx::query_as(
        "SELECT user_id,status,reserved_quota,refunded_quota FROM hero_sms_sms_orders WHERE id=$1 FOR UPDATE",
    )
    .bind(id)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|_| internal_error())?
    .ok_or_else(order_not_found)?;
    let (user, current_status, reserved, already_refunded) = row;
    if already_refunded >= reserved {
        tx.commit().await.map_err(|_| internal_error())?;
        return Ok(());
    }
    if !expected_statuses.is_empty() && !expected_statuses.contains(&current_status.as_str()) {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ORDER_STATE_CHANGED",
            message: "HeroSMS SMS order state changed",
        });
    }
    let amount = reserved - already_refunded;
    sqlx::query("UPDATE users SET quota=quota+$2 WHERE id=$1")
        .bind(user)
        .bind(amount)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    sqlx::query("INSERT INTO hero_sms_sms_quota_ledgers(user_id,order_id,entry_type,amount_quota,idempotency_key,created_at) VALUES($1,$2,'refund',$3,$4,$5) ON CONFLICT(idempotency_key) DO NOTHING")
        .bind(user)
        .bind(id)
        .bind(amount)
        .bind(format!("hero_sms:sms:refund:{id}"))
        .bind(now_unix())
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    let complaint_status = if current_status == "active" {
        Some("closed_refund")
    } else {
        None
    };
    sqlx::query("UPDATE hero_sms_sms_orders SET status=$2,refunded_quota=reserved_quota,last_error_code=$3,last_error_message=$4,complaint_status=COALESCE($5,complaint_status),updated_at=$6 WHERE id=$1")
        .bind(id)
        .bind(status)
        .bind(code)
        .bind(message)
        .bind(complaint_status)
        .bind(now_unix())
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    tx.commit().await.map_err(|_| internal_error())
}

struct CancellationTransition<'a> {
    error_code: &'a str,
    error_message: &'a str,
    pending_message: &'a str,
}

async fn mark_cancel_pending(
    pg: &PgPool,
    id: &str,
    user_id: i64,
    transition: CancellationTransition<'_>,
) -> Result<(), HeroSmsApiError> {
    sqlx::query("UPDATE hero_sms_sms_orders SET status='cancel_pending',provider_cancel_accepted_at=0,cancel_final_status='cancelled',cancel_error_code=$3,cancel_error_message=$4,last_error_code='CANCEL_PENDING',last_error_message=$5,updated_at=$6 WHERE id=$1 AND user_id=$2 AND status='active'")
        .bind(id)
        .bind(user_id)
        .bind(transition.error_code)
        .bind(transition.error_message)
        .bind(transition.pending_message)
        .bind(now_unix())
        .execute(pg)
        .await
        .map_err(|_| internal_error())?;
    Ok(())
}

async fn refresh(state: &HeroSmsState, user: i64, id: &str) -> Result<OrderView, HeroSmsApiError> {
    let mut row = fetch_order(&state.pg, user, id).await?;
    if row.status == "purchase_unknown" {
        return reconcile_unknown_purchase(state, row).await;
    }
    if row.status != "active" && row.status != "cancel_pending" {
        return to_view(&row, false);
    }
    let key = configured(state).await?;
    if row.status == "active"
        && row.provider_expires_at > 0
        && now_unix() >= row.provider_expires_at
    {
        mark_cancel_pending(
            &state.pg,
            id,
            user,
            CancellationTransition {
                error_code: "ACTIVATION_EXPIRED",
                error_message: "activation expired before receiving a code",
                pending_message: "activation expired; awaiting HeroSMS cancellation confirmation",
            },
        )
        .await?;
        row = fetch_order(&state.pg, user, id).await?;
    }
    if row.status == "cancel_pending" {
        let (view, _) = finalize_cancellation(state, &key, row).await?;
        return Ok(view);
    }
    if complaint_needs_reconciliation(&row.complaint_status) {
        return reconcile_complaint(state, &key, row).await;
    }
    let provider_id = row
        .provider_id
        .as_deref()
        .filter(|id| !id.trim().is_empty())
        .ok_or(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "RECONCILING",
            message: "wait for provider purchase reconciliation",
        })?;
    let status = state
        .gateway
        .get_sms_activation_status(&key, provider_id)
        .await
        .map_err(map_provider_error)?;
    if !status.code.is_empty() {
        complete_code(state, &key, &row, "active", &status.code, &status.text).await?;
    } else if row.provider_expires_at == 0
        && let Ok(provider_state) = state
            .gateway
            .get_sms_activation_state(&key, provider_id)
            .await
        && provider_state == SMS_STATE_CANCEL
    {
        refund(
            &state.pg,
            &row.id,
            "cancelled",
            "PROVIDER_CANCELLED",
            "HeroSMS cancelled the activation before a code arrived",
            &["active"],
        )
        .await?;
    }
    to_view(&fetch_order(&state.pg, user, id).await?, false)
}
async fn reconcile_unknown_purchase(
    state: &HeroSmsState,
    row: OrderRow,
) -> Result<OrderView, HeroSmsApiError> {
    let key = configured(state).await?;
    let before_json =
        decrypt_persistent("hero_sms.sms.snapshot", &row.provider_snapshot_ciphertext)
            .map_err(|_| internal_error())?;
    let before: Vec<SmsActiveActivation> =
        serde_json::from_str(&before_json).map_err(|_| internal_error())?;
    let before_ids = before
        .into_iter()
        .map(|item| item.id)
        .collect::<HashSet<_>>();
    let active = state
        .gateway
        .list_active_sms_activations(&key)
        .await
        .map_err(map_provider_error)?;
    let reserved = Decimal::from_str(&row.provider_price_cny).map_err(|_| internal_error())?;
    let mut candidates = active
        .into_iter()
        .filter(|item| {
            let cost = Decimal::from_str(&item.cost).ok();
            let operator_matches = row.operator.trim().is_empty()
                || row.operator.trim().eq_ignore_ascii_case("any")
                || item.operator.trim().is_empty()
                || item.operator.trim().eq_ignore_ascii_case(&row.operator);
            !before_ids.contains(&item.id)
                && !item.phone_number.trim().is_empty()
                && item.service == row.service
                && item.country_code == row.country_id
                && item.currency_code == HERO_SMS_CURRENCY_CODE
                && cost.is_some_and(|value| value > Decimal::ZERO && value <= reserved)
                && operator_matches
        })
        .collect::<Vec<_>>();
    if candidates.len() == 1 {
        let item = candidates.remove(0);
        let activation = SmsActivation {
            id: item.id,
            phone_number: item.phone_number,
            cost: item.cost,
            currency_code: item.currency_code,
            country_code: item.country_code,
            operator: item.operator,
            expires_at: item.expires_at,
        };
        let (view, _, _) = complete(
            state,
            &row.id,
            row.user_id,
            row.charge_quota,
            activation,
            StatusCode::CREATED,
        )
        .await?;
        return Ok(view);
    }
    if candidates.len() > 1 {
        sqlx::query("UPDATE hero_sms_sms_orders SET last_error_code='RECONCILIATION_AMBIGUOUS',last_error_message='multiple provider activations require manual reconciliation',updated_at=$2 WHERE id=$1 AND status='purchase_unknown'")
            .bind(&row.id)
            .bind(now_unix())
            .execute(&state.pg)
            .await
            .map_err(|_| internal_error())?;
        return to_view(&fetch_order(&state.pg, row.user_id, &row.id).await?, false);
    }
    if now_unix() >= row.provider_request_started_at + 900 {
        refund(
            &state.pg,
            &row.id,
            "failed",
            "PROVIDER_NOT_FOUND",
            "provider purchase did not create an activation",
            &["purchase_unknown"],
        )
        .await?;
        return to_view(&fetch_order(&state.pg, row.user_id, &row.id).await?, false);
    }
    to_view(&row, false)
}

fn complaint_needs_reconciliation(status: &str) -> bool {
    matches!(status, "submitting" | "submitted" | "submit_unknown")
}

async fn complete_code(
    state: &HeroSmsState,
    key: &str,
    row: &OrderRow,
    expected_status: &str,
    code: &str,
    message: &str,
) -> Result<bool, HeroSmsApiError> {
    let code = encrypt_payload(code)?;
    let message = encrypt_payload(message)?;
    let complaint_status =
        complaint_needs_reconciliation(&row.complaint_status).then_some("closed_code");
    let changed = sqlx::query("UPDATE hero_sms_sms_orders SET status='completed',code_ciphertext=$2,message_ciphertext=$3,complaint_status=COALESCE($4,complaint_status),completed_at=$5,updated_at=$5 WHERE id=$1 AND status=$6")
        .bind(&row.id)
        .bind(code)
        .bind(message)
        .bind(complaint_status)
        .bind(now_unix())
        .bind(expected_status)
        .execute(&state.pg)
        .await
        .map_err(|_| internal_error())?
        .rows_affected();
    if changed > 0
        && let Some(provider_id) = row.provider_id.as_deref()
    {
        let _ = state
            .gateway
            .set_sms_activation_status(key, provider_id, 6)
            .await;
    }
    Ok(changed > 0)
}

fn provider_id_for_reconciliation(
    row: &OrderRow,
    missing_message: &'static str,
) -> Result<String, HeroSmsApiError> {
    row.provider_id
        .as_deref()
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
        .ok_or(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "RECONCILING",
            message: missing_message,
        })
}

async fn finalize_cancellation(
    state: &HeroSmsState,
    key: &str,
    mut row: OrderRow,
) -> Result<(OrderView, i64), HeroSmsApiError> {
    let provider_id = provider_id_for_reconciliation(
        &row,
        "wait for provider purchase reconciliation before cancelling",
    )?;
    let provider_state = state
        .gateway
        .get_sms_activation_state(key, &provider_id)
        .await
        .map_err(map_provider_error)?;
    if provider_state == SMS_STATE_CANCEL {
        let status = if row.cancel_final_status.is_empty() {
            "cancelled"
        } else {
            row.cancel_final_status.as_str()
        };
        let code = if row.cancel_error_code.is_empty() {
            "USER_CANCELLED"
        } else {
            row.cancel_error_code.as_str()
        };
        let message = if row.cancel_error_message.is_empty() {
            "activation cancelled before receiving a code"
        } else {
            row.cancel_error_message.as_str()
        };
        refund(
            &state.pg,
            &row.id,
            status,
            code,
            message,
            &["cancel_pending"],
        )
        .await?;
        row = fetch_order(&state.pg, row.user_id, &row.id).await?;
        return Ok((to_view(&row, false)?, quota(&state.pg, row.user_id).await?));
    }
    let provider_status = state
        .gateway
        .get_sms_activation_status(key, &provider_id)
        .await
        .map_err(map_provider_error)?;
    if !provider_status.code.is_empty()
        && complete_code(
            state,
            key,
            &row,
            "cancel_pending",
            &provider_status.code,
            &provider_status.text,
        )
        .await?
    {
        row = fetch_order(&state.pg, row.user_id, &row.id).await?;
        return Ok((to_view(&row, false)?, quota(&state.pg, row.user_id).await?));
    }
    if provider_state == SMS_STATE_OK {
        return Err(HeroSmsApiError {
            status: StatusCode::ACCEPTED,
            code: "RECONCILING",
            message: "HeroSMS activation completion is pending",
        });
    }
    if row.provider_cancel_accepted_at == 0 {
        state
            .gateway
            .set_sms_activation_status(key, &provider_id, 8)
            .await
            .map_err(map_provider_error)?;
        sqlx::query("UPDATE hero_sms_sms_orders SET provider_cancel_accepted_at=$2,updated_at=$2 WHERE id=$1 AND status='cancel_pending' AND provider_cancel_accepted_at=0")
            .bind(&row.id)
            .bind(now_unix())
            .execute(&state.pg)
            .await
            .map_err(|_| internal_error())?;
        row = fetch_order(&state.pg, row.user_id, &row.id).await?;
    }
    Ok((to_view(&row, false)?, quota(&state.pg, row.user_id).await?))
}

async fn reconcile_complaint(
    state: &HeroSmsState,
    key: &str,
    mut row: OrderRow,
) -> Result<OrderView, HeroSmsApiError> {
    let provider_id = provider_id_for_reconciliation(
        &row,
        "wait for provider purchase reconciliation before submitting a complaint",
    )?;
    let now = now_unix();
    if matches!(
        row.complaint_status.as_str(),
        "submitting" | "submit_unknown"
    ) && row.complaint_submit_attempts < COMPLAINT_MAX_ATTEMPTS
        && (row.complaint_next_retry_at == 0 || row.complaint_next_retry_at <= now)
    {
        let result = state
            .gateway
            .submit_sms_complaint(key, &provider_id, &row.complaint_type)
            .await;
        let attempts = row.complaint_submit_attempts + 1;
        let (status, next_retry) = match &result {
            Ok(()) => ("submitted", 0),
            Err(HeroSmsProviderError::UpstreamBusy | HeroSmsProviderError::UpstreamTimeout) => {
                ("submit_unknown", now + COMPLAINT_RETRY_SECONDS)
            }
            Err(_) => ("failed", 0),
        };
        sqlx::query("UPDATE hero_sms_sms_orders SET complaint_status=$2,complaint_submit_attempts=$3,complaint_next_retry_at=$4,updated_at=$5 WHERE id=$1 AND status='active' AND complaint_status IN ('submitting','submit_unknown')")
            .bind(&row.id)
            .bind(status)
            .bind(attempts)
            .bind(next_retry)
            .bind(now)
            .execute(&state.pg)
            .await
            .map_err(|_| internal_error())?;
        row.complaint_status = status.to_owned();
        row.complaint_submit_attempts = attempts;
        row.complaint_next_retry_at = next_retry;
        if let Err(error) = result
            && !matches!(
                error,
                HeroSmsProviderError::UpstreamBusy | HeroSmsProviderError::UpstreamTimeout
            )
        {
            return Err(map_provider_error(error));
        }
    }
    let provider_state = state
        .gateway
        .get_sms_activation_state(key, &provider_id)
        .await
        .map_err(map_provider_error)?;
    if provider_state == SMS_STATE_CANCEL {
        refund(
            &state.pg,
            &row.id,
            "cancelled",
            "UPSTREAM_REFUND_CONFIRMED",
            "HeroSMS confirmed cancellation after the complaint",
            &["active"],
        )
        .await?;
    } else {
        let status = state
            .gateway
            .get_sms_activation_status(key, &provider_id)
            .await
            .map_err(map_provider_error)?;
        if !status.code.is_empty() {
            complete_code(state, key, &row, "active", &status.code, &status.text).await?;
        }
    }
    sqlx::query("UPDATE hero_sms_sms_orders SET complaint_last_checked_at=$2,updated_at=$2 WHERE id=$1 AND status='active' AND complaint_status IN ('submitting','submitted','submit_unknown')")
        .bind(&row.id)
        .bind(now_unix())
        .execute(&state.pg)
        .await
        .map_err(|_| internal_error())?;
    to_view(&fetch_order(&state.pg, row.user_id, &row.id).await?, false)
}

async fn cancel_order(
    state: &HeroSmsState,
    user: i64,
    id: &str,
) -> Result<(OrderView, i64), HeroSmsApiError> {
    let key = configured(state).await?;
    let mut row = fetch_order(&state.pg, user, id).await?;
    if TERMINAL_STATUSES.contains(&row.status.as_str()) {
        return Ok((to_view(&row, false)?, quota(&state.pg, user).await?));
    }
    if row.status != "active" && row.status != "cancel_pending" {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "RECONCILING",
            message: "wait for provider purchase reconciliation before cancelling",
        });
    }
    if row.status == "active" {
        let provider_id_present = row
            .provider_id
            .as_deref()
            .is_some_and(|value| !value.trim().is_empty());
        if !provider_id_present {
            return Err(HeroSmsApiError {
                status: StatusCode::CONFLICT,
                code: "RECONCILING",
                message: "wait for provider purchase reconciliation before cancelling",
            });
        }
        mark_cancel_pending(
            &state.pg,
            id,
            user,
            CancellationTransition {
                error_code: "USER_CANCELLED",
                error_message: "activation cancelled before receiving a code",
                pending_message: "",
            },
        )
        .await?;
        row = fetch_order(&state.pg, user, id).await?;
    }
    finalize_cancellation(state, &key, row).await
}
async fn submit_complaint(
    state: &HeroSmsState,
    user: i64,
    id: &str,
    reason: &str,
) -> Result<OrderView, HeroSmsApiError> {
    // HeroSMS's official complaint enum misspells "mismatch" in this value.
    const PROVIDER_CODE_MISMATCH: &str = concat!("SMS_CODE_DIS", "MATCH");
    const REASONS: &[&str] = &[
        "NUMBER_BLOCKED",
        "NUMBER_ALREADY_IN_USE",
        PROVIDER_CODE_MISMATCH,
        "SMS_NOT_RECEIVED",
        "CODE_SENT_TO_APP",
        "INCOMING_CALL_NUMBER",
        "INCOMING_CALL_VOICE",
    ];
    let reason = reason.trim().to_ascii_uppercase();
    if !REASONS.contains(&reason.as_str()) {
        return Err(HeroSmsApiError {
            status: StatusCode::BAD_REQUEST,
            code: "INVALID_COMPLAINT_REASON",
            message: "select a supported complaint reason",
        });
    }
    let key = configured(state).await?;
    let row = fetch_order(&state.pg, user, id).await?;
    if row.status != "active" || row.provider_id.is_none() {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ORDER_STATE_CHANGED",
            message: "only an active activation can receive a complaint",
        });
    }
    if now_unix() < row.created_at + COMPLAINT_WAIT_SECONDS {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "COMPLAINT_TOO_EARLY",
            message: "wait two minutes before submitting a complaint",
        });
    }
    if complaint_needs_reconciliation(&row.complaint_status) {
        if row.complaint_type != reason {
            return Err(HeroSmsApiError {
                status: StatusCode::CONFLICT,
                code: "COMPLAINT_ALREADY_SUBMITTED",
                message: "a complaint is already pending for this activation",
            });
        }
        return to_view(&row, false);
    }
    if !row.complaint_status.is_empty() && row.complaint_status != "failed" {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "COMPLAINT_ALREADY_CLOSED",
            message: "the complaint workflow is already closed",
        });
    }
    let now = now_unix();
    let changed=sqlx::query("UPDATE hero_sms_sms_orders SET complaint_type=$3,complaint_status='submitting',complaint_submitted_at=$4,complaint_submit_attempts=1,complaint_next_retry_at=$4+30,updated_at=$4 WHERE id=$1 AND user_id=$2 AND status='active' AND (complaint_status='' OR complaint_status='failed')").bind(id).bind(user).bind(&reason).bind(now).execute(&state.pg).await.map_err(|_|internal_error())?.rows_affected();
    if changed == 0 {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ORDER_STATE_CHANGED",
            message: "HeroSMS SMS order state changed",
        });
    }
    match state
        .gateway
        .submit_sms_complaint(
            &key,
            row.provider_id.as_deref().unwrap_or_default(),
            &reason,
        )
        .await
    {
        Ok(()) => {
            sqlx::query("UPDATE hero_sms_sms_orders SET complaint_status='submitted',complaint_next_retry_at=0,updated_at=$2 WHERE id=$1 AND complaint_status='submitting'").bind(id).bind(now_unix()).execute(&state.pg).await.map_err(|_|internal_error())?;
            to_view(&fetch_order(&state.pg, user, id).await?, false)
        }
        Err(e) => {
            let unknown = matches!(
                e,
                HeroSmsProviderError::UpstreamBusy | HeroSmsProviderError::UpstreamTimeout
            );
            sqlx::query("UPDATE hero_sms_sms_orders SET complaint_status=$2,complaint_next_retry_at=$3,updated_at=$4 WHERE id=$1 AND complaint_status='submitting'").bind(id).bind(if unknown{"submit_unknown"}else{"failed"}).bind(if unknown{now_unix()+30}else{0}).bind(now_unix()).execute(&state.pg).await.map_err(|_|internal_error())?;
            Err(map_provider_error(e))
        }
    }
}
async fn hide_one(pg: &PgPool, user: i64, id: &str) -> Result<(), HeroSmsApiError> {
    let changed=sqlx::query("UPDATE hero_sms_sms_orders SET history_hidden_at=$3 WHERE id=$1 AND user_id=$2 AND history_hidden_at=0 AND status=ANY($4)").bind(id).bind(user).bind(now_unix()).bind(TERMINAL_STATUSES).execute(pg).await.map_err(|_|internal_error())?.rows_affected();
    if changed > 0 {
        return Ok(());
    }
    let row = fetch_order(pg, user, id).await?;
    if row.history_hidden_at > 0 {
        Ok(())
    } else {
        Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ORDER_ACTIVE",
            message: "active HeroSMS SMS orders cannot be removed from history",
        })
    }
}
async fn quota(pg: &PgPool, user: i64) -> Result<i64, HeroSmsApiError> {
    sqlx::query_scalar("SELECT quota FROM users WHERE id=$1")
        .bind(user)
        .fetch_one(pg)
        .await
        .map_err(|_| internal_error())
}
fn inventory(offer: &SmsOffer, max: Decimal) -> bool {
    offer.tiers.iter().any(|t| {
        t.count > 0 && Decimal::from_str(&t.price).is_ok_and(|p| p > Decimal::ZERO && p <= max)
    })
}
fn charge_quota_decimal(
    price: Decimal,
    options: &BTreeMap<String, String>,
) -> Result<i64, HeroSmsApiError> {
    let qpu = Decimal::from_str(&option_value(
        options,
        "QuotaPerUnit",
        &DEFAULT_QUOTA_PER_UNIT.to_string(),
    ))
    .map_err(|_| invalid_request())?;
    let value = (price * qpu).ceil();
    value
        .to_string()
        .parse::<i64>()
        .ok()
        .filter(|v| *v > 0)
        .ok_or_else(invalid_request)
}
fn encode_quote(q: &Quote) -> Result<String, HeroSmsApiError> {
    encrypt_persistent(
        "hero_sms.sms.quote",
        &serde_json::to_string(q).map_err(|_| not_configured())?,
    )
}
fn decode_quote(v: &str) -> Result<Quote, HeroSmsApiError> {
    let raw = decrypt_persistent("hero_sms.sms.quote", v).map_err(|_| price_changed())?;
    serde_json::from_str(&raw).map_err(|_| price_changed())
}
fn hash(v: &str) -> String {
    format!("{:x}", Sha256::digest(v.as_bytes()))
}
fn price_changed() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::CONFLICT,
        code: "PRICE_CHANGED",
        message: "refresh the HeroSMS SMS quote",
    }
}
fn order_not_found() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::NOT_FOUND,
        code: "ORDER_NOT_FOUND",
        message: "HeroSMS SMS order not found",
    }
}

fn sms_origin(g: &ReqwestHeroSmsGateway) -> String {
    g.base_url
        .trim_end_matches("/api/v1")
        .trim_end_matches('/')
        .to_owned()
}
async fn activate(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    action: &str,
    query: &[(&str, String)],
) -> Result<Value, HeroSmsProviderError> {
    let mut url = reqwest::Url::parse(&format!("{}/stubs/handler_api.php", sms_origin(g)))
        .map_err(|_| HeroSmsProviderError::InvalidRequest)?;
    {
        let mut q = url.query_pairs_mut();
        q.append_pair("action", action).append_pair("api_key", key);
        for (k, v) in query {
            q.append_pair(k, v);
        }
    }
    let response = g
        .client
        .get(url)
        .header("Accept", "application/json, text/plain")
        .send()
        .await
        .map_err(map_reqwest_error)?;
    map_status(response.status())?;
    let text = response
        .text()
        .await
        .map_err(|_| HeroSmsProviderError::BadResponse)?;
    match text.trim() {
        "BAD_KEY" => Err(HeroSmsProviderError::Unauthorized),
        "NO_NUMBERS" => Err(HeroSmsProviderError::NotFound),
        "NO_BALANCE" => Err(HeroSmsProviderError::Unauthorized),
        v if v.starts_with("BAD_") || v.starts_with("WRONG_") => {
            Err(HeroSmsProviderError::InvalidRequest)
        }
        "ERROR_SQL" => Err(HeroSmsProviderError::UpstreamBusy),
        _ => Ok(serde_json::from_str(&text).unwrap_or(Value::String(text))),
    }
}
pub(super) async fn reqwest_list_countries(
    g: &ReqwestHeroSmsGateway,
    key: &str,
) -> Result<Vec<SmsCountry>, HeroSmsProviderError> {
    let v = activate(g, key, "getCountries", &[]).await?;
    let map = v.as_object().ok_or(HeroSmsProviderError::BadResponse)?;
    let mut out = Vec::new();
    for (id, item) in map {
        let Some(id) = id.parse().ok() else { continue };
        let eng = item.get("eng").and_then(Value::as_str).unwrap_or("").trim();
        let chn = item.get("chn").and_then(Value::as_str).unwrap_or("").trim();
        out.push(SmsCountry {
            id,
            name: if chn.is_empty() { eng } else { chn }.into(),
            english_name: eng.into(),
            chinese_name: chn.into(),
            visible: item.get("visible").and_then(Value::as_i64).unwrap_or(0) != 0,
        });
    }
    Ok(out)
}
pub(super) async fn reqwest_list_services(
    g: &ReqwestHeroSmsGateway,
    key: &str,
) -> Result<Vec<SmsService>, HeroSmsProviderError> {
    let v = activate(g, key, "getServicesList", &[]).await?;
    let mut out = Vec::new();
    if let Some(items) = v.get("services").and_then(Value::as_array) {
        if !v
            .get("status")
            .and_then(Value::as_str)
            .unwrap_or("")
            .eq_ignore_ascii_case("success")
        {
            return Err(HeroSmsProviderError::BadResponse);
        }
        for item in items {
            let code = item
                .get("code")
                .and_then(Value::as_str)
                .unwrap_or("")
                .trim();
            let name = item
                .get("name")
                .and_then(Value::as_str)
                .unwrap_or("")
                .trim();
            if !code.is_empty() && !name.is_empty() {
                out.push(SmsService {
                    code: code.into(),
                    name: name.into(),
                });
            }
        }
    } else if let Some(map) = v.as_object() {
        for (code, name) in map {
            if let Some(name) = name.as_str() {
                out.push(SmsService {
                    code: code.trim().into(),
                    name: name.trim().into(),
                });
            }
        }
    }
    out.sort_by(|a, b| a.code.cmp(&b.code));
    out.dedup_by(|a, b| a.code == b.code);
    Ok(out)
}
pub(super) async fn reqwest_list_operators(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    country: i32,
) -> Result<Vec<String>, HeroSmsProviderError> {
    let v = activate(g, key, "getOperators", &[("country", country.to_string())]).await?;
    if v.as_str() == Some("OPERATORS_NOT_FOUND") {
        return Ok(vec![]);
    }
    if !v
        .get("status")
        .and_then(Value::as_str)
        .unwrap_or("")
        .eq_ignore_ascii_case("success")
    {
        return Err(HeroSmsProviderError::BadResponse);
    }
    let mut out = v
        .pointer(&format!("/countryOperators/{country}"))
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(Value::as_str)
        .map(str::trim)
        .filter(|v| {
            !v.is_empty() && !v.eq_ignore_ascii_case("any") && v.len() <= 64 && !v.contains(',')
        })
        .map(str::to_owned)
        .collect::<Vec<_>>();
    out.sort_by_key(|v| v.to_ascii_lowercase());
    out.dedup_by(|a, b| a.eq_ignore_ascii_case(b));
    if out.len() > 128 {
        return Err(HeroSmsProviderError::BadResponse);
    }
    Ok(out)
}
pub(super) async fn reqwest_get_offer(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    country: i32,
    service: &str,
) -> Result<SmsOffer, HeroSmsProviderError> {
    let response = g
        .client
        .get(format!(
            "{}/activations/offers/sms",
            g.base_url.trim_end_matches('/')
        ))
        .query(&[
            ("countries", country.to_string()),
            ("services", service.to_owned()),
        ])
        .header("Accept", "application/json")
        .header("Authorization", format!("ApiKey {key}"))
        .send()
        .await
        .map_err(map_reqwest_error)?;
    map_status(response.status())?;
    let v: Value = response
        .json()
        .await
        .map_err(|_| HeroSmsProviderError::BadResponse)?;
    let item = v
        .pointer(&format!("/data/{service}/{country}"))
        .ok_or(HeroSmsProviderError::NotFound)?;
    let map = item.pointer("/map").and_then(Value::as_object);
    let mut tiers = map
        .into_iter()
        .flatten()
        .filter_map(|(p, c)| {
            Some(SmsPriceTier {
                count: i32::try_from(c.as_i64()?).ok()?,
                price: Decimal::from_str(p).ok()?.normalize().to_string(),
            })
        })
        .filter(|t| t.count > 0)
        .collect::<Vec<_>>();
    tiers.sort_by(|a, b| {
        Decimal::from_str(&a.price)
            .unwrap_or_default()
            .cmp(&Decimal::from_str(&b.price).unwrap_or_default())
    });
    if tiers.is_empty() {
        let price = item
            .pointer("/prices/default")
            .and_then(decimal_value)
            .ok_or(HeroSmsProviderError::BadResponse)?;
        let count = item
            .pointer("/counts/defaultPrice")
            .and_then(Value::as_i64)
            .and_then(|v| i32::try_from(v).ok())
            .filter(|v| *v > 0)
            .ok_or(HeroSmsProviderError::BadResponse)?;
        tiers.push(SmsPriceTier { count, price });
    }
    Ok(SmsOffer {
        country_id: country,
        service: service.into(),
        count: tiers[0].count,
        price: tiers[0].price.clone(),
        tiers,
    })
}
pub(super) async fn reqwest_purchase(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    r: SmsPurchase,
) -> Result<SmsActivation, HeroSmsProviderError> {
    let mut q = vec![
        ("country", r.country_id.to_string()),
        ("service", r.service),
        ("maxPrice", r.max_price),
        ("currency", r.currency_code.to_string()),
    ];
    if !r.operator.is_empty() {
        q.push(("operator", r.operator))
    }
    let v = activate(g, key, "getNumberV2", &q).await?;
    let id = v
        .get("activationId")
        .and_then(decimal_value)
        .ok_or(HeroSmsProviderError::BadResponse)?;
    let phone = v
        .get("phoneNumber")
        .and_then(Value::as_str)
        .unwrap_or("")
        .trim();
    let cost = v
        .get("activationCost")
        .and_then(decimal_value)
        .ok_or(HeroSmsProviderError::BadResponse)?;
    if phone.is_empty() {
        return Err(HeroSmsProviderError::BadResponse);
    }
    Ok(SmsActivation {
        id,
        phone_number: phone.into(),
        cost,
        currency_code: v
            .get("currencyCode")
            .or_else(|| v.get("currency"))
            .and_then(Value::as_i64)
            .and_then(|x| i32::try_from(x).ok())
            .unwrap_or(0),
        country_code: v
            .get("countryCode")
            .and_then(Value::as_i64)
            .and_then(|x| i32::try_from(x).ok())
            .unwrap_or(0),
        operator: v
            .get("activationOperator")
            .and_then(Value::as_str)
            .unwrap_or("")
            .trim()
            .into(),
        expires_at: 0,
    })
}
pub(super) async fn reqwest_list_active(
    g: &ReqwestHeroSmsGateway,
    key: &str,
) -> Result<Vec<SmsActiveActivation>, HeroSmsProviderError> {
    let v = activate(g, key, "getActiveActivations", &[]).await?;
    Ok(v.get("data")
        .and_then(Value::as_array)
        .into_iter()
        .flatten()
        .filter_map(|i| {
            Some(SmsActiveActivation {
                id: i.get("activationId").and_then(decimal_value)?,
                service: i
                    .get("serviceCode")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .into(),
                phone_number: i
                    .get("phoneNumber")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .into(),
                cost: i.get("activationCost").and_then(decimal_value)?,
                currency_code: i
                    .get("currency")
                    .and_then(Value::as_i64)
                    .and_then(|x| i32::try_from(x).ok())
                    .unwrap_or(0),
                status: i
                    .get("activationStatus")
                    .and_then(Value::as_i64)
                    .and_then(|x| i32::try_from(x).ok())
                    .unwrap_or(0),
                code: i
                    .get("smsCode")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .into(),
                text: i
                    .get("smsText")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .into(),
                operator: i
                    .get("activationOperator")
                    .and_then(Value::as_str)
                    .unwrap_or("")
                    .trim()
                    .into(),
                country_code: i
                    .get("countryCode")
                    .and_then(Value::as_i64)
                    .and_then(|x| i32::try_from(x).ok())
                    .unwrap_or(0),
                expires_at: 0,
            })
        })
        .collect())
}
pub(super) async fn reqwest_status(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    id: &str,
) -> Result<SmsStatus, HeroSmsProviderError> {
    let v = activate(g, key, "getStatusV2", &[("id", id.into())]).await?;
    let object = v.as_object().ok_or(HeroSmsProviderError::BadResponse)?;
    let Some(sms) = object.get("sms") else {
        return Err(HeroSmsProviderError::BadResponse);
    };
    if sms.is_null() {
        return Ok(SmsStatus::default());
    }
    let sms = sms.as_object().ok_or(HeroSmsProviderError::BadResponse)?;
    let string_field = |name: &str| -> Result<String, HeroSmsProviderError> {
        match sms.get(name) {
            None | Some(Value::Null) => Ok(String::new()),
            Some(Value::String(value)) => Ok(value.trim().to_owned()),
            Some(_) => Err(HeroSmsProviderError::BadResponse),
        }
    };
    Ok(SmsStatus {
        code: string_field("code")?,
        text: string_field("text")?,
    })
}
pub(super) async fn reqwest_state(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    id: &str,
) -> Result<String, HeroSmsProviderError> {
    let value = activate(g, key, "getStatus", &[("id", id.into())]).await?;
    let state = value
        .as_str()
        .map(str::trim)
        .ok_or(HeroSmsProviderError::BadResponse)?;
    if state == "NO_ACTIVATION" {
        return Err(HeroSmsProviderError::NotFound);
    }
    if matches!(
        state,
        "STATUS_WAIT_CODE"
            | "STATUS_WAIT_RETRY"
            | "STATUS_WAIT_RESEND"
            | SMS_STATE_OK
            | SMS_STATE_CANCEL
    ) {
        return Ok(state.to_owned());
    }
    if state.starts_with("STATUS_OK:") {
        return Ok(SMS_STATE_OK.to_owned());
    }
    Err(HeroSmsProviderError::BadResponse)
}

pub(super) async fn reqwest_set_status(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    id: &str,
    status: i32,
) -> Result<(), HeroSmsProviderError> {
    let v = activate(
        g,
        key,
        "setStatus",
        &[("id", id.into()), ("status", status.to_string())],
    )
    .await?;
    let expected = match status {
        3 => "ACCESS_RETRY_GET",
        6 => "ACCESS_ACTIVATION",
        8 => "ACCESS_CANCEL",
        _ => return Err(HeroSmsProviderError::InvalidRequest),
    };
    if v.as_str().is_some_and(|v| v.trim() == expected) {
        Ok(())
    } else {
        Err(HeroSmsProviderError::BadResponse)
    }
}
pub(super) async fn reqwest_complaint(
    g: &ReqwestHeroSmsGateway,
    key: &str,
    id: &str,
    reason: &str,
) -> Result<(), HeroSmsProviderError> {
    let response = g
        .client
        .post(format!(
            "{}/complaints/activations/{}",
            g.base_url.trim_end_matches('/'),
            urlencoding(id)
        ))
        .header("Accept", "application/json")
        .header("Authorization", format!("ApiKey {key}"))
        .json(&json!({"type":reason}))
        .send()
        .await
        .map_err(map_reqwest_error)?;
    map_status(response.status())
}
fn decimal_value(v: &Value) -> Option<String> {
    match v {
        Value::String(s) => Decimal::from_str(s).ok().map(|v| v.normalize().to_string()),
        Value::Number(n) => Decimal::from_str(&n.to_string())
            .ok()
            .map(|v| v.normalize().to_string()),
        _ => None,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::auth::{
        AuthBundle, AuthError, AuthErrorKind, DashboardUser, LoginOutcome, LoginRequest,
        LogoutRequest, LogoutResult, RequestMetadata, TwoFactorLoginRequest,
    };
    use axum::body::Body;
    use secrecy::SecretString;
    use sqlx::postgres::PgPoolOptions;
    use std::{
        sync::atomic::{AtomicUsize, Ordering},
        time::Duration,
    };
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[derive(Clone)]
    struct FixtureAuth {
        error: Option<AuthErrorKind>,
        critical_checks: Arc<AtomicUsize>,
    }

    fn fixture_auth_failure<T>() -> Result<T, AuthError> {
        Err(AuthError::new(AuthErrorKind::Internal))
    }

    #[async_trait]
    impl DashboardAuth for FixtureAuth {
        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            fixture_auth_failure()
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            fixture_auth_failure()
        }

        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            self.critical_checks.fetch_add(1, Ordering::SeqCst);
            Ok(CriticalRateLimitOutcome::Allowed)
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            fixture_auth_failure()
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            fixture_auth_failure()
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            fixture_auth_failure()
        }

        async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
            match self.error {
                Some(error) => Err(AuthError::new(error)),
                None => Ok(fixture_user()),
            }
        }
    }

    #[derive(Default)]
    struct CountingLimiter(AtomicUsize);

    #[async_trait]
    impl SmsUserRateLimiter for CountingLimiter {
        async fn check(&self, _: &str, _: i64) -> Result<CriticalRateLimitOutcome, ()> {
            self.0.fetch_add(1, Ordering::SeqCst);
            Ok(CriticalRateLimitOutcome::Allowed)
        }
    }

    fn fixture_user() -> DashboardUser {
        let empty = String::new;
        DashboardUser {
            username: "member".to_owned(),
            group: "default".to_owned(),
            id: 7,
            role: 1,
            status: 1,
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            permissions: Value::Null,
            sidebar_modules: Value::Null,
            display_name: empty(),
            email: empty(),
            github_id: empty(),
            discord_id: empty(),
            oidc_id: empty(),
            wechat_id: empty(),
            telegram_id: empty(),
            aff_code: empty(),
            linux_do_id: empty(),
            setting: empty(),
            stripe_customer: empty(),
        }
    }

    fn handler_fixture(
        auth_error: Option<AuthErrorKind>,
    ) -> Result<(HeroSmsState, Arc<CountingLimiter>, Arc<AtomicUsize>), sqlx::Error> {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(100))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")?;
        let critical = Arc::new(AtomicUsize::new(0));
        let auth: Arc<dyn DashboardAuth> = Arc::new(FixtureAuth {
            error: auth_error,
            critical_checks: Arc::clone(&critical),
        });
        let limiter = Arc::new(CountingLimiter::default());
        let mut state = HeroSmsState::new(pg, auth, Arc::new(DisabledHeroSmsGateway));
        state.sms_user_rate_limiter = limiter.clone();
        Ok((state, limiter, critical))
    }

    fn authorized_request(
        method: &str,
        uri: &str,
        body: Vec<u8>,
    ) -> Result<Request, axum::http::Error> {
        Request::builder()
            .method(method)
            .uri(uri)
            .header("Authorization", "Bearer fixture")
            .header("X-Real-IP", "127.0.0.1")
            .body(Body::from(body))
    }

    #[test]
    fn user_limiter_uses_go_namespace() {
        assert_eq!(
            sms_user_rate_limit_key("hero-sms-sms-purchase", 42),
            "rateLimit:v2:user:UC:hero-sms-sms-purchase:42"
        );
    }

    #[test]
    fn quota_charge_is_authoritative_and_rounded_up() -> TestResult {
        let options = BTreeMap::from([("QuotaPerUnit".to_owned(), "500000".to_owned())]);
        assert_eq!(
            charge_quota_decimal(Decimal::from_str("0.000001")?, &options)?,
            1
        );
        assert_eq!(
            charge_quota_decimal(Decimal::from_str("1.25")?, &options)?,
            625_000
        );
        Ok(())
    }

    #[tokio::test]
    async fn mutation_body_limit_precedes_payload_validation() -> TestResult {
        let request = Request::builder()
            .method("POST")
            .body(axum::body::Body::from(vec![b'x'; BODY_LIMIT_BYTES + 1]))?;
        let response = match parse_json::<CreateInput>(request).await {
            Ok(_) => {
                return Err(std::io::Error::other("oversized body was accepted").into());
            }
            Err(response) => response,
        };
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        Ok(())
    }

    #[tokio::test]
    async fn mutation_handlers_authenticate_and_validate_body_before_rate_limits() -> TestResult {
        let (state, limiter, critical) = handler_fixture(None)?;
        let app = super::routes().with_state(state);

        let oversized = authorized_request(
            "POST",
            "/api/hero-sms/sms/orders",
            vec![b'x'; BODY_LIMIT_BYTES + 1],
        )?;
        let response = app.clone().oneshot(oversized).await?;
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        assert_eq!(limiter.0.load(Ordering::SeqCst), 0);
        assert_eq!(critical.load(Ordering::SeqCst), 0);

        let invalid = authorized_request("POST", "/api/hero-sms/sms/orders", b"not-json".to_vec())?;
        let response = app.clone().oneshot(invalid).await?;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert_eq!(limiter.0.load(Ordering::SeqCst), 0);
        assert_eq!(critical.load(Ordering::SeqCst), 0);

        let mut exact = b"{}".to_vec();
        exact.resize(BODY_LIMIT_BYTES, b' ');
        let response = app
            .clone()
            .oneshot(authorized_request(
                "POST",
                "/api/hero-sms/sms/orders",
                exact,
            )?)
            .await?;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert_eq!(limiter.0.load(Ordering::SeqCst), 1);
        assert_eq!(critical.load(Ordering::SeqCst), 1);

        let response = app
            .oneshot(authorized_request(
                "DELETE",
                "/api/hero-sms/sms/history",
                vec![b'x'; BODY_LIMIT_BYTES + 1],
            )?)
            .await?;
        assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
        assert_eq!(limiter.0.load(Ordering::SeqCst), 1);
        Ok(())
    }

    #[tokio::test]
    async fn unauthenticated_oversized_mutation_fails_auth_without_consuming_limits() -> TestResult
    {
        let (state, limiter, critical) = handler_fixture(Some(AuthErrorKind::Unauthorized))?;
        let response = super::routes()
            .with_state(state)
            .oneshot(authorized_request(
                "POST",
                "/api/hero-sms/sms/orders",
                vec![b'x'; BODY_LIMIT_BYTES + 1],
            )?)
            .await?;
        assert!(matches!(
            response.status(),
            StatusCode::UNAUTHORIZED | StatusCode::NOT_FOUND
        ));
        assert_eq!(limiter.0.load(Ordering::SeqCst), 0);
        assert_eq!(critical.load(Ordering::SeqCst), 0);
        Ok(())
    }

    #[tokio::test]
    async fn bodyless_delete_accepts_empty_body_and_get_does_not_consume_mutation_limit()
    -> TestResult {
        let (state, limiter, _) = handler_fixture(None)?;
        let app = super::routes().with_state(state);
        let response = app
            .clone()
            .oneshot(authorized_request(
                "DELETE",
                "/api/hero-sms/sms/history/not-owned",
                Vec::new(),
            )?)
            .await?;
        assert_eq!(limiter.0.load(Ordering::SeqCst), 1);
        assert!(matches!(
            response.status(),
            StatusCode::NOT_FOUND | StatusCode::INTERNAL_SERVER_ERROR
        ));

        let _ = app
            .oneshot(authorized_request(
                "GET",
                "/api/hero-sms/sms/orders?page=1&size=20",
                Vec::new(),
            )?)
            .await?;
        assert_eq!(limiter.0.load(Ordering::SeqCst), 1);
        Ok(())
    }

    #[tokio::test]
    async fn postgres_refund_is_transactional_and_idempotent() -> TestResult {
        let Ok(database_url) = std::env::var("LMM_TEST_POSTGRES_URL") else {
            return Ok(());
        };
        let admin = PgPool::connect(&database_url).await?;
        let schema = format!("hero_sms_sms_test_{}", Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;
        let scoped_url = format!(
            "{database_url}{}options=-csearch_path%3D{schema}",
            if database_url.contains('?') { "&" } else { "?" }
        );
        let pg = PgPool::connect(&scoped_url).await?;
        for statement in [
            "CREATE TABLE users(id BIGINT PRIMARY KEY, quota BIGINT NOT NULL)",
            "CREATE TABLE hero_sms_sms_orders(id TEXT PRIMARY KEY,user_id BIGINT NOT NULL,status TEXT NOT NULL,reserved_quota BIGINT NOT NULL,charge_quota BIGINT NOT NULL,refunded_quota BIGINT NOT NULL DEFAULT 0,complaint_status TEXT NOT NULL DEFAULT '',last_error_code TEXT NOT NULL DEFAULT '',last_error_message TEXT NOT NULL DEFAULT '',updated_at BIGINT NOT NULL)",
            "CREATE TABLE hero_sms_sms_quota_ledgers(id BIGSERIAL PRIMARY KEY,order_id TEXT NOT NULL,user_id BIGINT NOT NULL,entry_type TEXT NOT NULL,amount_quota BIGINT NOT NULL,idempotency_key TEXT NOT NULL UNIQUE,created_at BIGINT NOT NULL)",
        ] {
            sqlx::query(statement).execute(&pg).await?;
        }
        sqlx::query("INSERT INTO users VALUES(7,100)")
            .execute(&pg)
            .await?;
        sqlx::query("INSERT INTO hero_sms_sms_orders(id,user_id,status,reserved_quota,charge_quota,updated_at) VALUES('order-1',7,'active',50,50,1)")
            .execute(&pg)
            .await?;

        let first_pg = pg.clone();
        let second_pg = pg.clone();
        let (first, second) = tokio::join!(
            refund(
                &first_pg,
                "order-1",
                "cancelled",
                "USER_CANCELLED",
                "cancelled",
                &["active"],
            ),
            refund(
                &second_pg,
                "order-1",
                "cancelled",
                "USER_CANCELLED",
                "cancelled",
                &["active"],
            )
        );
        first?;
        second?;
        refund(
            &pg,
            "order-1",
            "cancelled",
            "USER_CANCELLED",
            "cancelled",
            &["active"],
        )
        .await?;
        let quota: i64 = sqlx::query_scalar("SELECT quota FROM users WHERE id=7")
            .fetch_one(&pg)
            .await?;
        let ledgers: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM hero_sms_sms_quota_ledgers")
            .fetch_one(&pg)
            .await?;
        assert_eq!(quota, 150);
        assert_eq!(ledgers, 1);
        pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await?;
        Ok(())
    }
}
