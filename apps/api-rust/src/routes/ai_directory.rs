//! Current Go AI directory and immutable, wallet-funded advertisement contract.

mod postgres;
mod url;
pub use postgres::PgAIDirectoryStore;

use async_trait::async_trait;
use axum::{
    Router,
    body::to_bytes,
    extract::{Path, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::Response,
    routing::{get, post},
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use std::{collections::BTreeSet, net::IpAddr, sync::Arc};

use super::legacy_http::{
    dashboard_credential, legacy_json, localized_dashboard_auth_error, localized_user_policy_error,
};
use crate::{
    ClientIpKey, RequestContext,
    auth::{
        CriticalRateLimitOutcome, DashboardAuth, DashboardUserView, UserAuthPolicyError,
        dashboard_token_candidate, enforce_user_auth_view,
    },
    legacy_empty_response,
};

pub const MIN_BID_CENTS: i64 = 100;
pub const MAX_BID_CENTS: i64 = 1_000_000;
pub const MAX_WALLET_QUOTA: i64 = (1_i64 << 53) - 1;
pub const AD_DURATION_SECONDS: i64 = 30 * 24 * 60 * 60;

#[derive(Clone)]
pub struct AIDirectoryState {
    store: Arc<dyn AIDirectoryStore>,
    auth: Arc<dyn DashboardAuth>,
}

impl AIDirectoryState {
    pub fn new(store: Arc<dyn AIDirectoryStore>, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { store, auth }
    }
}

pub fn router(state: AIDirectoryState) -> Router {
    Router::new()
        .route("/api/ai-directory", get(directory))
        .route("/api/ai-directory/ads", get(list_ads).post(create_ad))
        .route("/api/ai-directory/ads/quote", get(quote_ad))
        .route("/api/ai-directory/ads/mine", get(my_ads))
        .route("/api/ai-directory/ads/{id}/hide", post(hide_ad))
        .with_state(state)
}

#[derive(Clone, Debug, PartialEq, Eq, Serialize)]
pub struct AIDirectoryAd {
    pub id: i64,
    #[serde(skip)]
    pub owner_user_id: i64,
    pub name: String,
    pub url: String,
    pub summary: String,
    pub description: String,
    pub bid_cents: i64,
    pub charged_quota: i64,
    #[serde(skip)]
    pub request_id: String,
    pub status: String,
    pub paid_at: i64,
    pub expires_at: i64,
    pub hidden_at: i64,
    pub refunded_at: i64,
}

#[derive(Clone, Debug, Default, PartialEq, Eq, Serialize)]
pub struct AIDirectoryAdInput {
    pub name: String,
    pub url: String,
    pub summary: String,
    pub description: String,
    pub bid_cents: i64,
    pub expected_quota: i64,
    pub request_id: String,
}

impl<'de> Deserialize<'de> for AIDirectoryAdInput {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = AIDirectoryAdInput;
            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("an advertisement object")
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut input = AIDirectoryAdInput::default();
                while let Some(key) = map.next_key::<String>()? {
                    match key.to_ascii_lowercase().as_str() {
                        "name" => {
                            if let Some(value) = map.next_value::<Option<String>>()? {
                                input.name = value;
                            }
                        }
                        "url" => {
                            if let Some(value) = map.next_value::<Option<String>>()? {
                                input.url = value;
                            }
                        }
                        "summary" => {
                            if let Some(value) = map.next_value::<Option<String>>()? {
                                input.summary = value;
                            }
                        }
                        "description" => {
                            if let Some(value) = map.next_value::<Option<String>>()? {
                                input.description = value;
                            }
                        }
                        "request_id" => {
                            if let Some(value) = map.next_value::<Option<String>>()? {
                                input.request_id = value;
                            }
                        }
                        "bid_cents" => {
                            if let Some(value) = map.next_value::<Option<i64>>()? {
                                input.bid_cents = value;
                            }
                        }
                        "expected_quota" => {
                            if let Some(value) = map.next_value::<Option<i64>>()? {
                                input.expected_quota = value;
                            }
                        }
                        _ => {
                            let _ = map.next_value::<serde::de::IgnoredAny>()?;
                        }
                    }
                }
                Ok(input)
            }
        }
        deserializer.deserialize_map(Visitor)
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, thiserror::Error)]
pub enum AdError {
    #[error("invalid advertisement details")]
    InvalidInput,
    #[error("advertisement bid must be between $1 and $10,000")]
    InvalidBid,
    #[error("insufficient wallet balance")]
    Insufficient,
    #[error("advertisement request ID was already used for different details")]
    Conflict,
    #[error("advertisement price changed; review a fresh quote")]
    QuoteChanged,
    #[error("advertisement not found")]
    NotFound,
    #[error("wallet quota would exceed the safe range")]
    WalletRange,
    #[error("advertisement storage unavailable")]
    Storage,
}

#[async_trait]
pub trait AIDirectoryStore: Send + Sync {
    async fn links(&self) -> Result<Value, AdError>;
    async fn quote(&self, bid_cents: i64) -> Result<i64, AdError>;
    async fn active(&self, offset: i64) -> Result<(Vec<AIDirectoryAd>, bool), AdError>;
    async fn mine(&self, owner_id: i64) -> Result<Vec<AIDirectoryAd>, AdError>;
    async fn create(
        &self,
        owner_id: i64,
        input: AIDirectoryAdInput,
    ) -> Result<(AIDirectoryAd, bool), AdError>;
    async fn hide(&self, ad_id: i64) -> Result<(AIDirectoryAd, bool), AdError>;
    async fn audit_hide(
        &self,
        _actor: &DashboardUserView,
        _pat: bool,
        _ip: &str,
        _ad_id: i64,
        _refunded: bool,
    ) {
    }
}

pub fn charge_quota(bid: i64, rate: f64) -> Result<i64, AdError> {
    if !(MIN_BID_CENTS..=MAX_BID_CENTS).contains(&bid) {
        return Err(AdError::InvalidBid);
    }
    if !rate.is_finite() || rate <= 0.0 {
        return Err(AdError::WalletRange);
    }
    // shopspring/decimal.NewFromFloat uses the shortest decimal representation,
    // divides with its default 16 decimal places before taking the ceiling.
    // Preserve that rounding step, including tiny positive rates rounding to 0.
    let repr = rate.to_string();
    let (mantissa, explicit_exponent) = repr
        .split_once(['e', 'E'])
        .map_or((repr.as_str(), 0_i32), |(m, e)| (m, e.parse().unwrap_or(0)));
    let fraction = mantissa
        .split_once('.')
        .map_or(0, |(_, tail)| tail.len() as i32);
    let digits = mantissa.replace('.', "");
    let digits = digits.trim_start_matches('0');
    let trimmed = digits.trim_end_matches('0');
    let exponent = explicit_exponent - fraction + (digits.len() - trimmed.len()) as i32 - 2 + 16;
    let coefficient = trimmed.parse::<u128>().map_err(|_| AdError::WalletRange)?;
    let amount = coefficient
        .checked_mul(bid as u128)
        .ok_or(AdError::WalletRange)?;
    let rounded = if exponent >= 0 {
        amount
            .checked_mul(
                10_u128
                    .checked_pow(exponent as u32)
                    .ok_or(AdError::WalletRange)?,
            )
            .ok_or(AdError::WalletRange)?
    } else if -exponent > 38 {
        0
    } else {
        let divisor = 10_u128.pow((-exponent) as u32);
        amount / divisor + u128::from(amount % divisor >= divisor / 2)
    };
    let precision = 10_000_000_000_000_000_u128;
    let rounded = rounded / precision + u128::from(rounded % precision != 0);
    if rounded == 0 || rounded > MAX_WALLET_QUOTA as u128 {
        return Err(AdError::WalletRange);
    }
    Ok(rounded as i64)
}

pub fn normalize_ad(mut input: AIDirectoryAdInput) -> Result<AIDirectoryAdInput, AdError> {
    input.name = input.name.trim().to_owned();
    input.summary = input.summary.trim().to_owned();
    input.description = input.description.trim().to_owned();
    if input.name.is_empty()
        || input.name.chars().count() > 80
        || input.summary.chars().count() > 180
        || input.description.chars().count() > 1200
        || !(16..=80).contains(&input.request_id.len())
        || !input
            .request_id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
    {
        return Err(AdError::InvalidInput);
    }
    input.url = input.url.trim().to_owned();
    let (host, canonical) = url::parse(&input.url, true)?;
    input.url = canonical;
    // Check the original authority as Go net/url does. URL parsers which
    // canonicalize numeric hosts must not silently widen the accepted set.
    let host = host.to_ascii_lowercase();
    if host == "localhost" || host.ends_with(".local") || !host.contains('.') {
        return Err(AdError::InvalidInput);
    }
    if let Ok(ip) = host.parse::<IpAddr>() {
        let rejected = ip.is_loopback()
            || ip.is_unspecified()
            || match ip {
                IpAddr::V4(ip) => ip.is_private() || ip.is_link_local(),
                IpAddr::V6(ip) => {
                    ip.is_unique_local()
                        || ip.is_unicast_link_local()
                        || ip.to_ipv4_mapped().is_some_and(|v4| {
                            v4.is_loopback()
                                || v4.is_private()
                                || v4.is_link_local()
                                || v4.is_unspecified()
                        })
                }
            };
        if rejected {
            return Err(AdError::InvalidInput);
        }
    }
    if !(MIN_BID_CENTS..=MAX_BID_CENTS).contains(&input.bid_cents) {
        return Err(AdError::InvalidBid);
    }
    if input.expected_quota <= 0 {
        return Err(AdError::InvalidInput);
    }
    Ok(input)
}

pub fn validate_directory_links(raw: &str) -> Result<Value, String> {
    if raw.len() > 512 * 1024 {
        return Err("AI directory configuration is too large".into());
    }
    let value: Value =
        serde_json::from_str(raw).map_err(|_| "AI directory must be a JSON array")?;
    let links = value
        .as_array()
        .ok_or("AI directory must be a JSON array")?;
    if links.len() > 60 {
        return Err("AI directory supports at most 60 websites".into());
    }
    let mut seen = BTreeSet::new();
    for (index, link) in links.iter().enumerate() {
        let number = index + 1;
        let link = link
            .as_object()
            .ok_or("AI directory entries must be JSON objects")?;
        for key in [
            "id",
            "name",
            "url",
            "category",
            "summary",
            "description",
            "enabled",
        ] {
            if link.get(key).is_none_or(Value::is_null) {
                return Err(format!("website {number} is missing {key}"));
            }
        }
        let text = |key: &str| {
            link.get(key)
                .and_then(Value::as_str)
                .ok_or("AI directory must be a JSON array".to_owned())
        };
        let id = text("id")?;
        if id.is_empty()
            || id.len() > 64
            || !id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
            || !seen.insert(id)
        {
            return Err(format!("website {number} has an invalid or duplicate ID"));
        }
        if text("name")?.trim().is_empty()
            || text("name")?.chars().count() > 80
            || text("summary")?.chars().count() > 180
            || text("description")?.chars().count() > 1200
        {
            return Err(format!("website {number} has invalid text lengths"));
        }
        if !matches!(
            text("category")?,
            "chat" | "research" | "resources" | "developer" | "creative" | "other"
        ) {
            return Err(format!("website {number} has an invalid category"));
        }
        let url = text("url")?;
        if url.len() > 2048 || url.contains([' ', '\t', '\r', '\n']) {
            return Err(format!("website {number} has an invalid URL"));
        }
        url::parse(url, false).map_err(|_| {
            format!("website {number} must use an absolute HTTP or HTTPS URL without credentials")
        })?;
        if !link["enabled"].is_boolean() {
            return Err("AI directory must be a JSON array".into());
        }
    }
    Ok(value)
}

fn success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"message":"","data":data}),
    )
}
fn error(error: AdError) -> Response {
    let (status, code) = match error {
        AdError::InvalidInput | AdError::InvalidBid => (
            StatusCode::UNPROCESSABLE_ENTITY,
            "AI_DIRECTORY_AD_INVALID_INPUT",
        ),
        AdError::Insufficient => (
            StatusCode::PAYMENT_REQUIRED,
            "AI_DIRECTORY_AD_INSUFFICIENT_BALANCE",
        ),
        AdError::Conflict => (StatusCode::CONFLICT, "AI_DIRECTORY_AD_REQUEST_CONFLICT"),
        AdError::QuoteChanged => (StatusCode::CONFLICT, "AI_DIRECTORY_AD_QUOTE_CHANGED"),
        AdError::NotFound => (StatusCode::NOT_FOUND, "AI_DIRECTORY_AD_NOT_FOUND"),
        _ => (StatusCode::INTERNAL_SERVER_ERROR, "AI_DIRECTORY_AD_FAILED"),
    };
    let message = if status == StatusCode::INTERNAL_SERVER_ERROR {
        "Unable to process the advertisement".to_owned()
    } else {
        error.to_string()
    };
    legacy_json(
        status,
        json!({"success":false,"code":code,"message":message}),
    )
}
fn authenticated(mut response: Response) -> Response {
    response.headers_mut().insert(
        "auth-version",
        HeaderValue::from_static(crate::auth_version::AUTH_VERSION),
    );
    response
}

async fn principal(
    state: &AIDirectoryState,
    headers: &HeaderMap,
    role: i64,
) -> Result<Option<DashboardUserView>, Response> {
    let Some(credential) = dashboard_credential(headers) else {
        return if role == 0 {
            Ok(None)
        } else {
            Err(localized_dashboard_auth_error(headers, None))
        };
    };
    let user = match state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
    {
        Ok(user) => user,
        Err(_) if role == 0 => return Ok(None),
        Err(error) => return Err(localized_dashboard_auth_error(headers, Some(error.kind))),
    };
    if !user.developer_access_granted {
        return Err(legacy_json(
            StatusCode::NOT_FOUND,
            json!({"message":"Not Found"}),
        ));
    }
    if role > 0 {
        enforce_user_auth_view(&user).map_err(|e| localized_user_policy_error(headers, e))?;
        if user.role < role {
            return Err(localized_user_policy_error(
                headers,
                UserAuthPolicyError::InsufficientPrivilege,
            ));
        }
    }
    Ok(Some(user))
}

fn query_value(query: Option<&str>, key: &str) -> Option<String> {
    form_urlencoded::parse(query.unwrap_or_default().as_bytes())
        .find(|(name, _)| name == key)
        .map(|(_, value)| value.into_owned())
}

async fn directory(State(state): State<AIDirectoryState>, headers: HeaderMap) -> Response {
    if let Err(response) = principal(&state, &headers, 0).await {
        return response;
    }
    let links = state.store.links().await.unwrap_or(Value::Null);
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"data":{"links":links}}),
    )
}
async fn list_ads(
    State(state): State<AIDirectoryState>,
    RawQuery(query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = principal(&state, &headers, 0).await {
        return response;
    }
    let offset = match query_value(query.as_deref(), "offset")
        .unwrap_or_else(|| "0".into())
        .parse::<i64>()
    {
        Ok(value) if (0..=100_000).contains(&value) => value,
        _ => return error(AdError::InvalidInput),
    };
    match state.store.active(offset).await {
        Ok((items, more)) => {
            success(json!({"next_offset":offset+items.len() as i64,"items":items,"has_more":more}))
        }
        Err(e) => error(e),
    }
}
async fn quote_ad(
    State(state): State<AIDirectoryState>,
    RawQuery(query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = principal(&state, &headers, 1).await {
        return response;
    }
    let bid = match query_value(query.as_deref(), "bid_cents")
        .unwrap_or_default()
        .parse::<i64>()
    {
        Ok(value) => value,
        _ => return authenticated(error(AdError::InvalidBid)),
    };
    authenticated(match state.store.quote(bid).await {
        Ok(quota) => success(
            json!({"bid_cents":bid,"quota":quota,"currency":"USD","duration_days":30,"min_bid_cents":MIN_BID_CENTS,"max_bid_cents":MAX_BID_CENTS}),
        ),
        Err(e) => error(e),
    })
}
async fn my_ads(State(state): State<AIDirectoryState>, headers: HeaderMap) -> Response {
    let user = match principal(&state, &headers, 1).await {
        Ok(Some(user)) => user,
        Err(response) => return response,
        _ => return error(AdError::Storage),
    };
    authenticated(match state.store.mine(user.id).await {
        Ok(items) => success(json!({"items":items})),
        Err(e) => error(e),
    })
}
fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|v| v.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|v| v.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_default()
}
async fn critical(state: &AIDirectoryState, ip: &str) -> Result<(), Response> {
    match state.auth.check_critical_rate_limit(ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => Ok(()),
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Err(authenticated(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        ))),
        Err(_) => Err(authenticated(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        ))),
    }
}
async fn create_ad(State(state): State<AIDirectoryState>, request: Request) -> Response {
    let user = match principal(&state, request.headers(), 1).await {
        Ok(Some(user)) => user,
        Err(response) => return response,
        _ => return error(AdError::Storage),
    };
    if let Err(response) = critical(&state, &client_ip(&request)).await {
        return response;
    }
    if request
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.parse::<u64>().ok())
        .is_some_and(|v| v > 8 * 1024)
    {
        return authenticated(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
    }
    let bytes = match to_bytes(request.into_body(), 8 * 1024).await {
        Ok(body) => body,
        Err(_) => return authenticated(error(AdError::InvalidInput)),
    };
    let input = match Option::<AIDirectoryAdInput>::deserialize(
        &mut serde_json::Deserializer::from_slice(&bytes),
    ) {
        Ok(input) => input.unwrap_or_default(),
        Err(_) => return authenticated(error(AdError::InvalidInput)),
    };
    authenticated(match state.store.create(user.id, input).await {
        Ok((ad, created)) => {
            success(json!({"charged_quota":ad.charged_quota,"ad":ad,"created":created}))
        }
        Err(e) => error(e),
    })
}
async fn hide_ad(
    State(state): State<AIDirectoryState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let user = match principal(&state, request.headers(), 100).await {
        Ok(Some(user)) => user,
        Err(response) => return response,
        _ => return error(AdError::Storage),
    };
    let ip = client_ip(&request);
    if let Err(response) = critical(&state, &ip).await {
        return response;
    }
    let id = match id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => return authenticated(error(AdError::InvalidInput)),
    };
    authenticated(match state.store.hide(id).await {
        Ok((ad, refunded)) => {
            let pat = dashboard_credential(request.headers())
                .is_some_and(|token| !dashboard_token_candidate(&token));
            state.store.audit_hide(&user, pat, &ip, id, refunded).await;
            success(json!({"refunded_quota":ad.charged_quota,"ad":ad,"refunded":refunded}))
        }
        Err(e) => error(e),
    })
}
