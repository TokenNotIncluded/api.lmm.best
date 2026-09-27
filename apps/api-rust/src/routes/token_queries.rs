//! Persisted, read-only token quota snapshots for the current `/v1/usage` API.

mod pricing;

use super::{
    legacy_http::legacy_json, public_catalog::AccountBalanceRateLimiter,
    system_config::ProcessRuntimeOptions,
};
use crate::{ClientIpKey, RequestContext, auth::CriticalRateLimitOutcome, legacy_empty_response};
use axum::{
    Router,
    extract::{Request, State},
    http::{HeaderValue, StatusCode, header},
    response::Response,
    routing::get,
};
use chrono::Utc;
use serde_json::json;
use sqlx::{PgPool, Row};
use std::{net::IpAddr, sync::Arc};

#[derive(Clone)]
pub struct TokenQueryState {
    pg: PgPool,
    log_pg: PgPool,
    limiter: Arc<dyn AccountBalanceRateLimiter>,
    runtime: Option<Arc<ProcessRuntimeOptions>>,
}
impl TokenQueryState {
    pub fn new(pg: PgPool, limiter: Arc<dyn AccountBalanceRateLimiter>) -> Self {
        Self {
            log_pg: pg.clone(),
            pg,
            limiter,
            runtime: None,
        }
    }
    pub fn with_log_pool(mut self, pg: PgPool) -> Self {
        self.log_pg = pg;
        self
    }
    pub fn with_runtime_options(mut self, runtime: Arc<ProcessRuntimeOptions>) -> Self {
        self.runtime = Some(runtime);
        self
    }
    async fn option(&self, key: &str) -> Result<Option<String>, sqlx::Error> {
        if let Some(runtime) = &self.runtime {
            return Ok(runtime.snapshot().await.get(key).cloned());
        }
        sqlx::query_scalar("SELECT value FROM options WHERE key=$1")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
    }
}
pub fn router(state: TokenQueryState) -> Router {
    Router::new()
        .route("/v1/usage", get(usage))
        .route("/v1/pricing", get(token_pricing))
        .with_state(state)
}

fn boundary(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}
fn controller(response: Response) -> Response {
    let mut response = boundary(response);
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response
}
fn failure(status: StatusCode, reason: &str) -> Response {
    boundary(legacy_json(status, json!({"valid":false,"error":reason})))
}

fn oauth_attempt(request: &Request) -> bool {
    if request.headers().contains_key("x-lmm-group") {
        return true;
    }
    for name in [
        "authorization",
        "x-api-key",
        "x-goog-api-key",
        "mj-api-secret",
        "sec-websocket-protocol",
    ] {
        for value in request.headers().get_all(name) {
            if let Ok(value) = value.to_str()
                && (value.to_ascii_lowercase().contains("lmm_") || value.contains("oauth_managed_"))
            {
                return true;
            }
        }
    }
    form_urlencoded::parse(request.uri().query().unwrap_or_default().as_bytes()).any(
        |(key, value)| {
            key == "access_token"
                || (matches!(key.as_ref(), "key" | "api_key" | "token")
                    && (value.contains("lmm_") || value.contains("oauth_managed_")))
        },
    )
}
fn client_ip(request: &Request) -> Option<IpAddr> {
    request
        .extensions()
        .get::<ClientIpKey>()
        .and_then(|value| value.0.parse().ok())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|value| value.client_ip)
        })
}
fn ip_allowed(ip: Option<IpAddr>, limits: &str) -> bool {
    let clean = limits.replace(' ', "");
    let entries = clean
        .split('\n')
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .collect::<Vec<_>>();
    if entries.is_empty() {
        return true;
    }
    let Some(ip) = ip else {
        return false;
    };
    let alternate = match ip {
        IpAddr::V4(value) => Some(IpAddr::V6(value.to_ipv6_mapped())),
        IpAddr::V6(value) => value.to_ipv4_mapped().map(IpAddr::V4),
    };
    entries.into_iter().any(|entry| {
        if let Ok(network) = entry.parse::<ipnet::IpNet>() {
            network.contains(&ip) || alternate.is_some_and(|value| network.contains(&value))
        } else {
            entry
                .parse::<IpAddr>()
                .is_ok_and(|value| value == ip || Some(value) == alternate)
        }
    })
}

#[derive(sqlx::FromRow)]
struct TokenFacts {
    id: i64,
    user_id: i64,
    status: i64,
    expired_time: i64,
    allow_ips: String,
    remain_quota: i64,
    used_quota: i64,
    unlimited_quota: bool,
    token_group: String,
    auto_groups: String,
    model_limits_enabled: bool,
    model_limits: String,
}

async fn usage(State(state): State<TokenQueryState>, request: Request) -> Response {
    query(state, request, false).await
}
async fn token_pricing(State(state): State<TokenQueryState>, request: Request) -> Response {
    query(state, request, true).await
}
async fn query(state: TokenQueryState, request: Request, prices: bool) -> Response {
    if oauth_attempt(&request) {
        return failure(StatusCode::UNAUTHORIZED, "invalid_api_key");
    }
    let Some(raw) = request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
    else {
        return failure(StatusCode::UNAUTHORIZED, "invalid_api_key");
    };
    let parts = raw.split_whitespace().collect::<Vec<_>>();
    if parts.len() != 2 || !parts[0].eq_ignore_ascii_case("Bearer") {
        return failure(StatusCode::UNAUTHORIZED, "invalid_api_key");
    }
    let key = parts[1].strip_prefix("sk-").unwrap_or(parts[1]).to_owned();
    let ip = client_ip(&request);
    let requested_model =
        form_urlencoded::parse(request.uri().query().unwrap_or_default().as_bytes())
            .find(|(key, _)| key == "model")
            .map(|(_, value)| value.trim().to_owned())
            .unwrap_or_default();
    drop(request);
    // Exact key equality and explicit oauth_managed=false are intentional.
    // Exhausted keys remain readable; no lazy status/access-time writes occur.
    let token=match sqlx::query_as::<_,TokenFacts>("SELECT id::BIGINT AS id,COALESCE(user_id,0)::BIGINT AS user_id,COALESCE(status,0)::BIGINT AS status,COALESCE(expired_time,0)::BIGINT AS expired_time,COALESCE(allow_ips,'') AS allow_ips,COALESCE(remain_quota,0)::BIGINT AS remain_quota,COALESCE(used_quota,0)::BIGINT AS used_quota,COALESCE(unlimited_quota,FALSE) AS unlimited_quota,COALESCE(to_jsonb(tokens)->>'group','') AS token_group,COALESCE(to_jsonb(tokens)->>'auto_groups','') AS auto_groups,COALESCE((to_jsonb(tokens)->>'model_limits_enabled')::BOOLEAN,FALSE) AS model_limits_enabled,COALESCE(to_jsonb(tokens)->>'model_limits','') AS model_limits FROM tokens WHERE key=$1 AND COALESCE((to_jsonb(tokens)->>'oauth_managed')::BOOLEAN,FALSE)=FALSE AND COALESCE(to_jsonb(tokens)->>'deleted_at',to_jsonb(tokens)->>'DeletedAt') IS NULL ORDER BY id LIMIT 1")
        .bind(&key).fetch_optional(&state.pg).await {Ok(Some(token))=>token,Ok(None)=>return failure(StatusCode::UNAUTHORIZED,"invalid_api_key"),Err(_)=>return failure(StatusCode::INTERNAL_SERVER_ERROR,"quota_query_unavailable")};
    let (id, owner, status, expires, remaining, used, unlimited) = (
        token.id,
        token.user_id,
        token.status,
        token.expired_time,
        token.remain_quota,
        token.used_quota,
        token.unlimited_quota,
    );
    let now = Utc::now().timestamp();
    if !matches!(status, 1 | 4) || (expires != -1 && expires <= now) {
        return failure(StatusCode::UNAUTHORIZED, "invalid_api_key");
    }
    if !ip_allowed(ip, &token.allow_ips) {
        return failure(StatusCode::FORBIDDEN, "ip_not_allowed");
    }
    match sqlx::query_scalar::<_, i64>(
        "SELECT COALESCE(status,0)::BIGINT FROM users WHERE id=$1 AND deleted_at IS NULL",
    )
    .bind(owner)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(Some(1)) => {}
        Ok(_) => return failure(StatusCode::UNAUTHORIZED, "invalid_api_key"),
        Err(_) => return failure(StatusCode::INTERNAL_SERVER_ERROR, "quota_query_unavailable"),
    }
    let pricing = if prices {
        match pricing::context(&state, &token).await {
            Ok(context) => Some(context),
            Err(response) => return boundary(response),
        }
    } else {
        None
    };
    match state.limiter.check(owner).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return boundary(legacy_empty_response(
                StatusCode::TOO_MANY_REQUESTS,
                Some(retry_after_seconds),
            ));
        }
        Err(_) => {
            return boundary(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        }
    }
    if let Some(context) = pricing {
        return controller(pricing::response(&state, &token, context, &requested_model).await);
    }
    let unavailable = || {
        controller(failure(
            StatusCode::INTERNAL_SERVER_ERROR,
            "quota_query_unavailable",
        ))
    };
    let rate = match state.option("QuotaPerUnit").await {
        Ok(raw) => raw.unwrap_or_else(|| "500000".into()).parse::<f64>().ok(),
        Err(_) => None,
    };
    let Some(rate) = rate.filter(|value| value.is_finite() && *value > 0.0) else {
        return unavailable();
    };
    let enabled = match state.option("LogConsumeEnabled").await {
        Ok(None) => true,
        Ok(Some(value)) => value == "true",
        Err(_) => return unavailable(),
    };
    let today = if enabled {
        match sqlx::query_scalar::<_,String>("SELECT COALESCE(SUM(quota),0)::TEXT FROM logs WHERE user_id=$1 AND token_id=$2 AND type=2 AND created_at >= $3 AND created_at <= $4").bind(owner).bind(id).bind(now-now.rem_euclid(86400)).bind(now).fetch_one(&state.log_pg).await{Ok(sum)=>Some(sum),Err(_)=>return unavailable()}
    } else {
        None
    };
    // NUMERIC keeps integer sums exact and reproduces decimal.Div's 16-place
    // rounding before float conversion. The explicit denominator scale covers
    // the entire finite f64 range, including positive subnormal quota units.
    let amounts=sqlx::query("SELECT CASE WHEN $4 THEN NULL ELSE ROUND($1::TEXT::NUMERIC/$3::TEXT::NUMERIC(1000,500),16)::DOUBLE PRECISION END AS remaining,ROUND($2::TEXT::NUMERIC/$3::TEXT::NUMERIC(1000,500),16)::DOUBLE PRECISION AS used_total,CASE WHEN $4 THEN NULL ELSE ROUND(($1::TEXT::NUMERIC+$2::TEXT::NUMERIC)/$3::TEXT::NUMERIC(1000,500),16)::DOUBLE PRECISION END AS total_quota,ROUND($5::TEXT::NUMERIC/$3::TEXT::NUMERIC(1000,500),16)::DOUBLE PRECISION AS used_today")
        .bind(remaining.to_string()).bind(used.to_string()).bind(rate.to_string()).bind(unlimited).bind(today).fetch_one(&state.pg).await;
    let amounts = match amounts {
        Ok(row) => row,
        Err(_) => return unavailable(),
    };
    let decoded = (|| -> Result<_, sqlx::Error> {
        Ok((
            amounts.try_get::<Option<f64>, _>("remaining")?,
            amounts.try_get::<f64, _>("used_total")?,
            amounts.try_get::<Option<f64>, _>("total_quota")?,
            amounts.try_get::<Option<f64>, _>("used_today")?,
        ))
    })();
    let (remaining, used_total, total, used_today) = match decoded {
        Ok(values) => values,
        Err(_) => return unavailable(),
    };
    controller(legacy_json(
        StatusCode::OK,
        json!({"valid":true,"currency":"USD","remaining":remaining,"used_today":used_today,"used_total":used_total,"total_quota":total,"unlimited":unlimited,"updated_at":now,"scope":"token","day_timezone":"UTC","used_today_source":"retained_consumption_logs","consistency":"persisted_snapshot"}),
    ))
}
