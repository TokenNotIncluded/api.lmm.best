//! Legacy-compatible public relay contribution, routing, tipping, and admin routes.

use std::{
    collections::{HashMap, HashSet},
    net::IpAddr,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, Query, RawQuery, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use lmm_contracts::LegacySuccessEnvelope;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::{
    ClientIpKey,
    auth::{
        CriticalRateLimitOutcome, DashboardAuth, DashboardUserView, UserAuthPolicyError,
        dashboard_token_candidate, enforce_user_auth_view,
    },
    legacy_empty_response,
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 256 * 1024;
const ROUTING_BODY_LIMIT_BYTES: usize = 8 * 1024;
const TIP_BODY_LIMIT_BYTES: usize = 4 * 1024;
const DEFAULT_QUOTA_PER_UNIT: f64 = 500_000.0;
const MIN_WITHDRAWAL_USD: i64 = 10;
const ROUTING_MAX_ITEMS: i64 = 200;
const LOG_TYPE_TOPUP: i64 = 1;
const LOG_TYPE_SYSTEM: i64 = 4;
const STATUS_PENDING: &str = "pending";
const STATUS_APPROVED: &str = "approved";
const STATUS_REJECTED: &str = "rejected";
const REPORT_OPEN: &str = "open";
const REPORT_CLOSED: &str = "closed";

/// PostgreSQL and dashboard-auth dependencies for public relay routes.
#[derive(Clone)]
pub struct PublicRelayState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

impl PublicRelayState {
    /// Creates the public relay state from the listener's shared authorities.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pg, auth }
    }
}

/// Builds all public, user, and administrator public-relay routes.
pub fn router(state: PublicRelayState) -> Router {
    Router::new()
        .route(
            "/api/public-relays",
            get(list_public_relays).post(create_contribution),
        )
        .route("/api/public-relays/config", get(get_config))
        .route("/api/public-relays/mine", get(list_mine))
        .route(
            "/api/public-relays/routing",
            get(get_routing).put(update_routing),
        )
        .route("/api/public-relays/admin", get(list_admin))
        .route("/api/public-relays/admin/reports", get(list_admin_reports))
        .route(
            "/api/public-relays/admin/reports/{id}/review",
            post(review_admin_report),
        )
        .route(
            "/api/public-relays/admin/{id}/review",
            post(review_admin_contribution),
        )
        .route(
            "/api/public-relays/admin/{id}/link-channel/{channel_id}",
            post(link_admin_channel),
        )
        .route("/api/public-relays/{id}/reviews", get(list_reviews))
        .route("/api/public-relays/{id}/review", post(rate_public_relay))
        .route("/api/public-relays/{id}/tip", post(tip_public_relay))
        .route("/api/public-relays/{id}/report", post(report_public_relay))
        .route(
            "/api/public-relays/{id}/withdraw",
            post(withdraw_public_relay),
        )
        .with_state(state)
}

#[derive(Clone, Debug)]
struct Principal {
    user: DashboardUserView,
}

#[derive(Debug, Deserialize)]
struct LimitQuery {
    limit: Option<String>,
}

impl LimitQuery {
    fn normalized(&self, default: i64, max: i64) -> i64 {
        self.limit
            .as_deref()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|limit| *limit > 0)
            .map(|limit| limit.min(max))
            .unwrap_or(default)
    }
}

#[derive(Debug, Deserialize)]
struct StatusQuery {
    status: Option<String>,
    limit: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ContributionInput {
    name: Option<String>,
    base_url: Option<String>,
    models: Option<String>,
    description: Option<String>,
    channel_config: Option<Value>,
}

#[derive(Debug, Deserialize)]
struct ReviewInput {
    approve: bool,
    note: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ReportInput {
    reason: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ReportReviewInput {
    close: bool,
    note: Option<String>,
}

#[derive(Debug, Deserialize)]
struct WithdrawInput {
    group: Option<String>,
}

#[derive(Debug, Deserialize)]
struct TipInput {
    amount_usd: Option<f64>,
    message: Option<String>,
}

#[derive(Debug, Deserialize)]
struct RatingInput {
    rating: Option<i64>,
    comment: Option<String>,
}

#[derive(Debug, Deserialize)]
struct RoutingInput {
    #[serde(default)]
    disabled_ids: Vec<i64>,
    #[serde(default, rename = "order_ids")]
    ordered_ids: Vec<i64>,
}

#[derive(Clone, Debug, Serialize)]
struct PublicRelayContribution {
    id: i64,
    user_id: i64,
    contributor_email: String,
    name: String,
    base_url: String,
    group: String,
    models: String,
    description: String,
    status: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    review_note: String,
    #[serde(skip_serializing_if = "is_zero")]
    reviewed_by: i64,
    created_at: i64,
    updated_at: i64,
    #[serde(skip_serializing_if = "is_zero")]
    reviewed_at: i64,
    #[serde(skip_serializing_if = "is_zero")]
    channel_id: i64,
    used_quota: i64,
    tip_quota: i64,
    tip_count: i64,
    withdrawn_quota: i64,
    rating_average: f64,
    rating_count: i64,
}

#[derive(Clone, Debug, Serialize)]
struct PublicRelayView {
    id: i64,
    contributor_email: String,
    name: String,
    base_url: String,
    group: String,
    models: String,
    description: String,
    status: String,
    created_at: i64,
    updated_at: i64,
    used_quota: i64,
    tip_quota: i64,
    tip_count: i64,
    withdrawn_quota: i64,
    used_quota_usd: f64,
    tip_quota_usd: f64,
    withdrawn_quota_usd: f64,
    rating_average: f64,
    rating_count: i64,
}

#[derive(Clone, Debug, Serialize)]
struct PublicRelayReviewView {
    id: i64,
    contribution_id: i64,
    rating: i64,
    comment: String,
    created_at: i64,
    updated_at: i64,
}

#[derive(Clone, Debug, Serialize)]
struct PublicRelayRoutingItem {
    #[serde(flatten)]
    view: PublicRelayView,
    channel_id: i64,
    disabled: bool,
    position: i64,
}

#[derive(Clone, Debug, Serialize)]
struct PublicRelayReport {
    id: i64,
    contribution_id: i64,
    reporter_user_id: i64,
    reason: String,
    status: String,
    #[serde(skip_serializing_if = "is_zero")]
    reviewed_by: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    review_note: String,
    created_at: i64,
    #[serde(skip_serializing_if = "is_zero")]
    reviewed_at: i64,
}

const CONTRIBUTION_COLUMNS: &str = "id::BIGINT AS id, user_id::BIGINT AS user_id, \
    COALESCE(contributor_email, '') AS contributor_email, COALESCE(name, '') AS name, \
    COALESCE(base_url, '') AS base_url, COALESCE(\"group\", '') AS \"group\", \
    COALESCE(models, '') AS models, COALESCE(description, '') AS description, \
    COALESCE(status, '') AS status, COALESCE(review_note, '') AS review_note, \
    COALESCE(reviewed_by, 0)::BIGINT AS reviewed_by, COALESCE(created_at, 0)::BIGINT AS created_at, \
    COALESCE(updated_at, 0)::BIGINT AS updated_at, COALESCE(reviewed_at, 0)::BIGINT AS reviewed_at, \
    COALESCE(channel_id, 0)::BIGINT AS channel_id, COALESCE(used_quota, 0)::BIGINT AS used_quota, \
    COALESCE(tip_quota, 0)::BIGINT AS tip_quota, COALESCE(tip_count, 0)::BIGINT AS tip_count, \
    COALESCE(withdrawn_quota, 0)::BIGINT AS withdrawn_quota, \
    COALESCE(rating_average, 0)::DOUBLE PRECISION AS rating_average, \
    COALESCE(rating_count, 0)::BIGINT AS rating_count";

async fn get_config(State(state): State<PublicRelayState>) -> Response {
    success(json!({
        "group": public_relay_group(&state.pg).await,
        "minimum_withdrawal_usd": MIN_WITHDRAWAL_USD,
    }))
}

async fn list_public_relays(
    State(state): State<PublicRelayState>,
    Query(query): Query<LimitQuery>,
    headers: HeaderMap,
) -> Response {
    let limit = query.normalized(50, 100);
    let group = public_relay_group(&state.pg).await;
    let qpu = quota_per_unit(&state.pg).await;
    let rows = match sqlx::query(&format!(
        "SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions \
         WHERE status = $1 AND \"group\" = $2 AND channel_id > 0 \
         ORDER BY rating_average DESC, rating_count DESC, updated_at DESC, id DESC \
         LIMIT $3"
    ))
    .bind(STATUS_APPROVED)
    .bind(&group)
    .bind(limit)
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(error) => return api_error(error.to_string()),
    };
    let items = rows
        .into_iter()
        .filter_map(|row| contribution_from_row(row).ok())
        .map(|item| item.public_view(qpu))
        .collect::<Vec<_>>();
    let response = success(json!({"items": items, "group": group}));
    with_optional_auth_version(
        response,
        optional_authenticated(&state, &headers).await.is_ok(),
    )
}

async fn create_contribution(State(state): State<PublicRelayState>, request: Request) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = client_ip(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<ContributionInput>(request, BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    let email = match user_email(&state.pg, principal.user.id).await {
        Ok(email) if !email.trim().is_empty() => email,
        _ => {
            return with_auth_version(public_relay_failure(
                StatusCode::UNPROCESSABLE_ENTITY,
                "PUBLIC_RELAY_EMAIL_REQUIRED",
                "a verified account email is required",
            ));
        }
    };
    match create_contribution_record(&state.pg, principal.user.id, &email, &input).await {
        Ok(item) => with_auth_version(success(json!(item))),
        Err(PublicRelayError::InvalidUrl(message)) => with_auth_version(public_relay_failure(
            StatusCode::UNPROCESSABLE_ENTITY,
            "PUBLIC_RELAY_INVALID_URL",
            &message,
        )),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn list_mine(
    State(state): State<PublicRelayState>,
    Query(query): Query<LimitQuery>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let limit = query.normalized(50, 100);
    let group = public_relay_group(&state.pg).await;
    let rows = match sqlx::query(&format!(
        "SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions \
         WHERE user_id = $1 ORDER BY created_at DESC, id DESC LIMIT $2"
    ))
    .bind(principal.user.id)
    .bind(limit)
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(error) => return with_auth_version(api_error(error.to_string())),
    };
    let items = rows
        .into_iter()
        .filter_map(|row| contribution_from_row(row).ok())
        .collect::<Vec<_>>();
    with_auth_version(success(json!({"items": items, "group": group})))
}

async fn list_reviews(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    Query(query): Query<LimitQuery>,
    headers: HeaderMap,
) -> Response {
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return response,
    };
    let limit = query.normalized(20, 50);
    let group = public_relay_group(&state.pg).await;
    if !approved_contribution_exists(&state.pg, contribution_id, &group).await {
        return with_optional_auth_version(
            api_error("public relay contribution not found".to_owned()),
            optional_authenticated(&state, &headers).await.is_ok(),
        );
    }
    let rows = match sqlx::query(
        "SELECT id::BIGINT AS id, contribution_id::BIGINT AS contribution_id, \
         rating::BIGINT AS rating, COALESCE(comment, '') AS comment, \
         COALESCE(created_at, 0)::BIGINT AS created_at, COALESCE(updated_at, 0)::BIGINT AS updated_at \
         FROM public_relay_reviews WHERE contribution_id = $1 \
         ORDER BY updated_at DESC, id DESC LIMIT $2",
    )
    .bind(contribution_id)
    .bind(limit)
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(error) => {
            return with_optional_auth_version(
                api_error(error.to_string()),
                optional_authenticated(&state, &headers).await.is_ok(),
            );
        }
    };
    let items = rows
        .into_iter()
        .filter_map(|row| review_from_row(row).ok())
        .collect::<Vec<_>>();
    let response = success(json!({"items": items}));
    with_optional_auth_version(
        response,
        optional_authenticated(&state, &headers).await.is_ok(),
    )
}

async fn rate_public_relay(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<RatingInput>(request, ROUTING_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    match update_public_relay_rating(
        &state.pg,
        contribution_id,
        principal.user.id,
        input.rating.unwrap_or_default(),
        input.comment.unwrap_or_default(),
    )
    .await
    {
        Ok(()) => with_auth_version(success(Value::Null)),
        Err(PublicRelayError::NotFound) => with_auth_version(public_relay_failure(
            StatusCode::NOT_FOUND,
            "PUBLIC_RELAY_REVIEW_FAILED",
            "public relay contribution not found",
        )),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn tip_public_relay(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<TipInput>(request, TIP_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    let amount_usd = input.amount_usd.unwrap_or_default();
    if amount_usd <= 0.0 {
        return with_auth_version(public_relay_failure(
            StatusCode::UNPROCESSABLE_ENTITY,
            "PUBLIC_RELAY_INVALID_TIP",
            "invalid public relay contribution",
        ));
    }
    let qpu = quota_per_unit(&state.pg).await;
    let quota = quota_from_float(amount_usd * qpu);
    match tip_public_relay_contribution(
        &state.pg,
        contribution_id,
        principal.user.id,
        quota,
        input.message.unwrap_or_default(),
    )
    .await
    {
        Ok(()) => with_auth_version(success(json!({"amount_usd": amount_usd}))),
        Err(PublicRelayError::NotFound) => with_auth_version(public_relay_failure(
            StatusCode::NOT_FOUND,
            "PUBLIC_RELAY_TIP_FAILED",
            "public relay contribution not found",
        )),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn get_routing(State(state): State<PublicRelayState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match list_public_relay_routing(&state.pg, principal.user.id).await {
        Ok((items, group)) => with_auth_version(success(json!({"items": items, "group": group}))),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn update_routing(State(state): State<PublicRelayState>, request: Request) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<RoutingInput>(request, ROUTING_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    let group = public_relay_group(&state.pg).await;
    match update_public_relay_routing(
        &state.pg,
        principal.user.id,
        &group,
        input.disabled_ids,
        input.ordered_ids,
    )
    .await
    {
        Ok(()) => with_auth_version(success(Value::Null)),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn report_public_relay(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<ReportInput>(request, ROUTING_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    match create_public_relay_report(
        &state.pg,
        contribution_id,
        principal.user.id,
        input.reason.unwrap_or_default(),
    )
    .await
    {
        Ok(report) => with_auth_version(success(json!(report))),
        Err(PublicRelayError::NotFound) => with_auth_version(public_relay_failure(
            StatusCode::NOT_FOUND,
            "PUBLIC_RELAY_NOT_FOUND",
            "public relay contribution not found",
        )),
        Err(PublicRelayError::Conflict { code, message }) => {
            with_auth_version(public_relay_failure(StatusCode::CONFLICT, code, &message))
        }
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn withdraw_public_relay(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<WithdrawInput>(request, TIP_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    let group = input.group.unwrap_or_default().trim().to_owned();
    let user_group = user_group(&state.pg, principal.user.id).await;
    if group.is_empty() || !is_user_selectable_group(&state.pg, &user_group, &group).await {
        return with_auth_version(public_relay_failure(
            StatusCode::UNPROCESSABLE_ENTITY,
            "PUBLIC_RELAY_INVALID_GROUP",
            "the selected group is not available for this account",
        ));
    }
    match withdraw_public_relay_tips(&state.pg, contribution_id, principal.user.id, &group).await {
        Ok(amount) => with_auth_version(success(json!({"quota": amount, "group": group}))),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn list_admin(
    State(state): State<PublicRelayState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let status = query_value(raw_query.as_deref(), "status")
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .map(|value| value.to_ascii_lowercase());
    let limit = query_value(raw_query.as_deref(), "limit")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|limit| *limit > 0)
        .map(|limit| limit.min(100))
        .unwrap_or(100);
    let group = public_relay_group(&state.pg).await;
    let mut sql = format!("SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions");
    let rows = if let Some(status) = status.as_deref() {
        sql.push_str(" WHERE status = $1 ORDER BY created_at DESC, id DESC LIMIT $2");
        sqlx::query(&sql).bind(status).bind(limit)
    } else {
        sql.push_str(" ORDER BY created_at DESC, id DESC LIMIT $1");
        sqlx::query(&sql).bind(limit)
    }
    .fetch_all(&state.pg)
    .await;
    match rows {
        Ok(rows) => {
            let items = rows
                .into_iter()
                .filter_map(|row| contribution_from_row(row).ok())
                .collect::<Vec<_>>();
            with_auth_version(success(json!({"items": items, "group": group})))
        }
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn review_admin_contribution(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let admin = match authenticated_admin(&state, request.headers()).await {
        Ok(admin) => admin,
        Err(response) => return response,
    };
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<ReviewInput>(request, ROUTING_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    match review_public_relay_contribution(
        &state.pg,
        contribution_id,
        admin.user.id,
        input.approve,
        input.note.unwrap_or_default(),
    )
    .await
    {
        Ok(item) => with_auth_version(success(json!(item))),
        Err(PublicRelayError::NotFound) => with_auth_version(public_relay_failure(
            StatusCode::NOT_FOUND,
            "PUBLIC_RELAY_NOT_FOUND",
            "public relay contribution not found",
        )),
        Err(PublicRelayError::Conflict { code, message }) => {
            with_auth_version(public_relay_failure(StatusCode::CONFLICT, code, &message))
        }
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn link_admin_channel(
    State(state): State<PublicRelayState>,
    Path((id, channel_id_raw)): Path<(String, String)>,
    request: Request,
) -> Response {
    if let Err(response) = authenticated_admin(&state, request.headers()).await {
        return response;
    }
    let contribution_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    let channel_id = match channel_id_raw.parse::<i64>() {
        Ok(channel_id) if channel_id > 0 => channel_id,
        _ => {
            return with_auth_version(public_relay_failure(
                StatusCode::BAD_REQUEST,
                "PUBLIC_RELAY_INVALID_CHANNEL",
                "invalid channel id",
            ));
        }
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    match link_public_relay_channel(&state.pg, contribution_id, channel_id).await {
        Ok(()) => with_auth_version(success(Value::Null)),
        Err(PublicRelayError::NotFound) => with_auth_version(public_relay_failure(
            StatusCode::NOT_FOUND,
            "PUBLIC_RELAY_NOT_FOUND",
            "public relay contribution not found",
        )),
        Err(PublicRelayError::Conflict { code, message }) => {
            with_auth_version(public_relay_failure(StatusCode::CONFLICT, code, &message))
        }
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn list_admin_reports(
    State(state): State<PublicRelayState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let status = query_value(raw_query.as_deref(), "status")
        .map(|value| value.trim().to_owned())
        .filter(|value| !value.is_empty())
        .map(|value| value.to_ascii_lowercase());
    let limit = query_value(raw_query.as_deref(), "limit")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|limit| *limit > 0)
        .map(|limit| limit.min(100))
        .unwrap_or(100);
    let rows = if let Some(status) = status.as_deref() {
        sqlx::query(
            "SELECT id::BIGINT AS id, contribution_id::BIGINT AS contribution_id, \
             reporter_user_id::BIGINT AS reporter_user_id, COALESCE(reason, '') AS reason, \
             COALESCE(status, '') AS status, COALESCE(reviewed_by, 0)::BIGINT AS reviewed_by, \
             COALESCE(review_note, '') AS review_note, COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(reviewed_at, 0)::BIGINT AS reviewed_at \
             FROM public_relay_reports WHERE status = $1 \
             ORDER BY created_at DESC, id DESC LIMIT $2",
        )
        .bind(status)
        .bind(limit)
        .fetch_all(&state.pg)
        .await
    } else {
        sqlx::query(
            "SELECT id::BIGINT AS id, contribution_id::BIGINT AS contribution_id, \
             reporter_user_id::BIGINT AS reporter_user_id, COALESCE(reason, '') AS reason, \
             COALESCE(status, '') AS status, COALESCE(reviewed_by, 0)::BIGINT AS reviewed_by, \
             COALESCE(review_note, '') AS review_note, COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(reviewed_at, 0)::BIGINT AS reviewed_at \
             FROM public_relay_reports ORDER BY created_at DESC, id DESC LIMIT $1",
        )
        .bind(limit)
        .fetch_all(&state.pg)
        .await
    };
    match rows {
        Ok(rows) => {
            let items = rows
                .into_iter()
                .filter_map(|row| report_from_row(row).ok())
                .collect::<Vec<_>>();
            with_auth_version(success(json!({"items": items})))
        }
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

async fn review_admin_report(
    State(state): State<PublicRelayState>,
    Path(id): Path<String>,
    request: Request,
) -> Response {
    let admin = match authenticated_admin(&state, request.headers()).await {
        Ok(admin) => admin,
        Err(response) => return response,
    };
    let report_id = match parse_positive_id(&id) {
        Ok(id) => id,
        Err(response) => return with_auth_version(response),
    };
    if let Some(response) = critical_rate_limit(&state, &client_ip(&request)).await {
        return with_auth_version(response);
    }
    let input = match parse_json::<ReportReviewInput>(request, ROUTING_BODY_LIMIT_BYTES).await {
        Ok(input) => input,
        Err(response) => return with_auth_version(response),
    };
    match review_public_relay_report(
        &state.pg,
        report_id,
        admin.user.id,
        input.close,
        input.note.unwrap_or_default(),
    )
    .await
    {
        Ok(()) => with_auth_version(success(Value::Null)),
        Err(PublicRelayError::Business { code, message }) => with_auth_version(
            public_relay_failure(StatusCode::UNPROCESSABLE_ENTITY, code, &message),
        ),
        Err(PublicRelayError::Database(message)) => with_auth_version(api_error(message)),
        Err(error) => with_auth_version(api_error(error.to_string())),
    }
}

#[derive(Debug)]
enum PublicRelayError {
    NotFound,
    InvalidUrl(String),
    Conflict { code: &'static str, message: String },
    Business { code: &'static str, message: String },
    Database(String),
}

impl PublicRelayError {
    fn invalid_input() -> Self {
        Self::Business {
            code: "PUBLIC_RELAY_INVALID_REQUEST",
            message: "invalid public relay contribution".to_owned(),
        }
    }

    fn invalid_url() -> Self {
        Self::InvalidUrl("public relay URL must be an HTTPS or HTTP origin".to_owned())
    }
}

impl std::fmt::Display for PublicRelayError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::NotFound => write!(f, "public relay contribution not found"),
            Self::InvalidUrl(message) | Self::Business { message, .. } => write!(f, "{message}"),
            Self::Conflict { message, .. } => write!(f, "{message}"),
            Self::Database(message) => write!(f, "{message}"),
        }
    }
}

impl PublicRelayContribution {
    fn public_view(&self, qpu: f64) -> PublicRelayView {
        PublicRelayView {
            id: self.id,
            contributor_email: self.contributor_email.clone(),
            name: self.name.clone(),
            base_url: self.base_url.clone(),
            group: self.group.clone(),
            models: self.models.clone(),
            description: self.description.clone(),
            status: self.status.clone(),
            created_at: self.created_at,
            updated_at: self.updated_at,
            used_quota: self.used_quota,
            tip_quota: self.tip_quota,
            tip_count: self.tip_count,
            withdrawn_quota: self.withdrawn_quota,
            used_quota_usd: self.used_quota as f64 / qpu,
            tip_quota_usd: self.tip_quota as f64 / qpu,
            withdrawn_quota_usd: self.withdrawn_quota as f64 / qpu,
            rating_average: self.rating_average,
            rating_count: self.rating_count,
        }
    }
}

fn contribution_from_row(row: PgRow) -> Result<PublicRelayContribution, sqlx::Error> {
    Ok(PublicRelayContribution {
        id: row.try_get("id")?,
        user_id: row.try_get("user_id")?,
        contributor_email: row.try_get("contributor_email")?,
        name: row.try_get("name")?,
        base_url: row.try_get("base_url")?,
        group: row.try_get("group")?,
        models: row.try_get("models")?,
        description: row.try_get("description")?,
        status: row.try_get("status")?,
        review_note: row.try_get("review_note")?,
        reviewed_by: row.try_get("reviewed_by")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
        reviewed_at: row.try_get("reviewed_at")?,
        channel_id: row.try_get("channel_id")?,
        used_quota: row.try_get("used_quota")?,
        tip_quota: row.try_get("tip_quota")?,
        tip_count: row.try_get("tip_count")?,
        withdrawn_quota: row.try_get("withdrawn_quota")?,
        rating_average: row.try_get("rating_average")?,
        rating_count: row.try_get("rating_count")?,
    })
}

fn review_from_row(row: PgRow) -> Result<PublicRelayReviewView, sqlx::Error> {
    Ok(PublicRelayReviewView {
        id: row.try_get("id")?,
        contribution_id: row.try_get("contribution_id")?,
        rating: row.try_get("rating")?,
        comment: row.try_get("comment")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
    })
}

fn report_from_row(row: PgRow) -> Result<PublicRelayReport, sqlx::Error> {
    Ok(PublicRelayReport {
        id: row.try_get("id")?,
        contribution_id: row.try_get("contribution_id")?,
        reporter_user_id: row.try_get("reporter_user_id")?,
        reason: row.try_get("reason")?,
        status: row.try_get("status")?,
        reviewed_by: row.try_get("reviewed_by")?,
        review_note: row.try_get("review_note")?,
        created_at: row.try_get("created_at")?,
        reviewed_at: row.try_get("reviewed_at")?,
    })
}

async fn create_contribution_record(
    pg: &PgPool,
    user_id: i64,
    email: &str,
    input: &ContributionInput,
) -> Result<PublicRelayContribution, PublicRelayError> {
    let (name, base_url, models, description) = normalize_public_relay_input(
        input.name.as_deref().unwrap_or_default(),
        input.base_url.as_deref().unwrap_or_default(),
        input.models.as_deref().unwrap_or_default(),
        input.description.as_deref().unwrap_or_default(),
    )?;
    let channel_config = normalize_public_relay_channel_config(
        input.channel_config.as_ref(),
        &name,
        &base_url,
        &models,
    )?;
    let group = public_relay_group(pg).await;
    let now = unix_now();
    let row = sqlx::query(&format!(
        "INSERT INTO public_relay_contributions \
         (user_id, contributor_email, name, base_url, channel_config, \"group\", models, description, status, created_at, updated_at) \
         VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) \
         RETURNING {CONTRIBUTION_COLUMNS}"
    ))
    .bind(user_id)
    .bind(email.trim())
    .bind(&name)
    .bind(&base_url)
    .bind(channel_config)
    .bind(&group)
    .bind(&models)
    .bind(&description)
    .bind(STATUS_PENDING)
    .bind(now)
    .bind(now)
    .fetch_one(pg)
    .await
    .map_err(|error| PublicRelayError::Database(error.to_string()))?;
    contribution_from_row(row).map_err(|error| PublicRelayError::Database(error.to_string()))
}

async fn approved_contribution_exists(pg: &PgPool, contribution_id: i64, group: &str) -> bool {
    sqlx::query_scalar::<_, i64>(
        "SELECT id::BIGINT FROM public_relay_contributions \
         WHERE id = $1 AND status = $2 AND \"group\" = $3 AND channel_id > 0",
    )
    .bind(contribution_id)
    .bind(STATUS_APPROVED)
    .bind(group)
    .fetch_optional(pg)
    .await
    .ok()
    .flatten()
    .is_some()
}

async fn update_public_relay_rating(
    pg: &PgPool,
    contribution_id: i64,
    user_id: i64,
    rating: i64,
    comment: String,
) -> Result<(), PublicRelayError> {
    let comment = comment.trim().to_owned();
    if contribution_id <= 0
        || user_id <= 0
        || !(1..=5).contains(&rating)
        || comment.chars().count() > 2000
    {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_REVIEW_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    let group = public_relay_group(pg).await;
    let mut transaction = pg.begin().await.map_err(db_error)?;
    let exists = sqlx::query_scalar::<_, i64>(
        "SELECT id::BIGINT FROM public_relay_contributions \
         WHERE id = $1 AND status = $2 AND \"group\" = $3 AND channel_id > 0 FOR UPDATE",
    )
    .bind(contribution_id)
    .bind(STATUS_APPROVED)
    .bind(&group)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?;
    if exists.is_none() {
        return Err(PublicRelayError::NotFound);
    }
    let now = unix_now();
    let updated = sqlx::query(
        "UPDATE public_relay_reviews SET rating = $1, comment = $2, updated_at = $3 \
         WHERE contribution_id = $4 AND reviewer_user_id = $5",
    )
    .bind(rating)
    .bind(&comment)
    .bind(now)
    .bind(contribution_id)
    .bind(user_id)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    if updated.rows_affected() == 0 {
        sqlx::query(
            "INSERT INTO public_relay_reviews \
             (contribution_id, reviewer_user_id, rating, comment, created_at, updated_at) \
             VALUES ($1, $2, $3, $4, $5, $6)",
        )
        .bind(contribution_id)
        .bind(user_id)
        .bind(rating)
        .bind(&comment)
        .bind(now)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(db_error)?;
    }
    let aggregate = sqlx::query(
        "SELECT COALESCE(AVG(rating), 0)::DOUBLE PRECISION AS average, COUNT(*)::BIGINT AS count \
         FROM public_relay_reviews WHERE contribution_id = $1",
    )
    .bind(contribution_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(db_error)?;
    let average: f64 = aggregate.try_get("average").map_err(db_error)?;
    let count: i64 = aggregate.try_get("count").map_err(db_error)?;
    sqlx::query(
        "UPDATE public_relay_contributions SET rating_average = $1, rating_count = $2, updated_at = $3 \
         WHERE id = $4",
    )
    .bind(average)
    .bind(count)
    .bind(now)
    .bind(contribution_id)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    transaction.commit().await.map_err(db_error)
}

async fn tip_public_relay_contribution(
    pg: &PgPool,
    contribution_id: i64,
    tipper_id: i64,
    quota: i64,
    message: String,
) -> Result<(), PublicRelayError> {
    let message = message.trim().to_owned();
    let max_tip = quota_from_float(DEFAULT_QUOTA_PER_UNIT * 100.0);
    if contribution_id <= 0
        || tipper_id <= 0
        || quota <= 0
        || quota > max_tip
        || message.chars().count() > 500
    {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_TIP_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    let group = public_relay_group(pg).await;
    let mut transaction = pg.begin().await.map_err(db_error)?;
    let recipient_id = {
        let row = sqlx::query(
            "SELECT user_id::BIGINT AS user_id FROM public_relay_contributions \
             WHERE id = $1 AND status = $2 AND \"group\" = $3 AND channel_id > 0 FOR UPDATE",
        )
        .bind(contribution_id)
        .bind(STATUS_APPROVED)
        .bind(&group)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(db_error)?;
        let Some(row) = row else {
            return Err(PublicRelayError::NotFound);
        };
        let owner: i64 = row.try_get("user_id").map_err(db_error)?;
        if owner == tipper_id {
            return Err(PublicRelayError::Business {
                code: "PUBLIC_RELAY_TIP_FAILED",
                message: "invalid public relay contribution".to_owned(),
            });
        }
        owner
    };
    let tipper_quota = sqlx::query_scalar::<_, i64>(
        "SELECT quota::BIGINT FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(tipper_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?
    .ok_or_else(|| PublicRelayError::Database("record not found".to_owned()))?;
    if tipper_quota < quota {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_TIP_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    sqlx::query("UPDATE users SET quota = quota - $1 WHERE id = $2")
        .bind(quota)
        .bind(tipper_id)
        .execute(&mut *transaction)
        .await
        .map_err(db_error)?;
    let now = unix_now();
    sqlx::query(
        "INSERT INTO public_relay_tips (contribution_id, tipper_user_id, quota, message, created_at) \
         VALUES ($1, $2, $3, $4, $5)",
    )
    .bind(contribution_id)
    .bind(tipper_id)
    .bind(quota)
    .bind(&message)
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    sqlx::query(
        "UPDATE public_relay_contributions SET tip_quota = tip_quota + $1, tip_count = tip_count + 1, updated_at = $2 \
         WHERE id = $3",
    )
    .bind(quota)
    .bind(now)
    .bind(contribution_id)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    transaction.commit().await.map_err(db_error)?;
    let _ = record_log(
        pg,
        recipient_id,
        LOG_TYPE_TOPUP,
        format!("Received {quota} pending quota tip for public relay {contribution_id}"),
    )
    .await;
    let _ = record_log(
        pg,
        tipper_id,
        LOG_TYPE_SYSTEM,
        format!("Tipped public relay {contribution_id} with {quota} quota"),
    )
    .await;
    Ok(())
}

async fn withdraw_public_relay_tips(
    pg: &PgPool,
    contribution_id: i64,
    user_id: i64,
    target_group: &str,
) -> Result<i64, PublicRelayError> {
    if contribution_id <= 0 || user_id <= 0 || target_group.trim().is_empty() {
        return Err(PublicRelayError::invalid_input());
    }
    let minimum = quota_from_float(DEFAULT_QUOTA_PER_UNIT * MIN_WITHDRAWAL_USD as f64);
    let mut transaction = pg.begin().await.map_err(db_error)?;
    let row = sqlx::query(
        "SELECT tip_quota::BIGINT AS tip_quota, withdrawn_quota::BIGINT AS withdrawn_quota \
         FROM public_relay_contributions \
         WHERE id = $1 AND user_id = $2 AND status = $3 FOR UPDATE",
    )
    .bind(contribution_id)
    .bind(user_id)
    .bind(STATUS_APPROVED)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?;
    let Some(row) = row else {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_WITHDRAW_FAILED",
            message: "record not found".to_owned(),
        });
    };
    let tip_quota: i64 = row.try_get("tip_quota").map_err(db_error)?;
    let withdrawn_quota: i64 = row.try_get("withdrawn_quota").map_err(db_error)?;
    let available = tip_quota - withdrawn_quota;
    if available < minimum {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_WITHDRAW_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    sqlx::query("UPDATE users SET quota = quota + $1 WHERE id = $2")
        .bind(available)
        .bind(user_id)
        .execute(&mut *transaction)
        .await
        .map_err(db_error)?;
    sqlx::query(
        "UPDATE public_relay_contributions SET withdrawn_quota = withdrawn_quota + $1, updated_at = $2 \
         WHERE id = $3",
    )
    .bind(available)
    .bind(unix_now())
    .bind(contribution_id)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    transaction.commit().await.map_err(db_error)?;
    let _ = record_log(
        pg,
        user_id,
        LOG_TYPE_TOPUP,
        format!(
            "Withdrew {available} quota from public relay tips {contribution_id} into group {target_group}"
        ),
    )
    .await;
    Ok(available)
}

async fn create_public_relay_report(
    pg: &PgPool,
    contribution_id: i64,
    reporter_id: i64,
    reason: String,
) -> Result<PublicRelayReport, PublicRelayError> {
    let reason = reason.trim().to_owned();
    if contribution_id <= 0
        || reporter_id <= 0
        || reason.chars().count() < 2
        || reason.chars().count() > 2000
    {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_REPORT_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    let group = public_relay_group(pg).await;
    if !approved_contribution_exists(pg, contribution_id, &group).await {
        return Err(PublicRelayError::NotFound);
    }
    if let Some(existing) = sqlx::query(
        "SELECT id::BIGINT AS id, contribution_id::BIGINT AS contribution_id, \
         reporter_user_id::BIGINT AS reporter_user_id, COALESCE(reason, '') AS reason, \
         COALESCE(status, '') AS status, COALESCE(reviewed_by, 0)::BIGINT AS reviewed_by, \
         COALESCE(review_note, '') AS review_note, COALESCE(created_at, 0)::BIGINT AS created_at, \
         COALESCE(reviewed_at, 0)::BIGINT AS reviewed_at \
         FROM public_relay_reports WHERE contribution_id = $1 AND reporter_user_id = $2",
    )
    .bind(contribution_id)
    .bind(reporter_id)
    .fetch_optional(pg)
    .await
    .map_err(db_error)?
    {
        return report_from_row(existing).map_err(db_error);
    }
    let now = unix_now();
    let inserted = sqlx::query(
        "INSERT INTO public_relay_reports \
         (contribution_id, reporter_user_id, reason, status, created_at) \
         VALUES ($1, $2, $3, $4, $5) \
         ON CONFLICT (contribution_id, reporter_user_id) DO NOTHING \
         RETURNING id::BIGINT AS id, contribution_id::BIGINT AS contribution_id, \
         reporter_user_id::BIGINT AS reporter_user_id, COALESCE(reason, '') AS reason, \
         COALESCE(status, '') AS status, COALESCE(reviewed_by, 0)::BIGINT AS reviewed_by, \
         COALESCE(review_note, '') AS review_note, COALESCE(created_at, 0)::BIGINT AS created_at, \
         COALESCE(reviewed_at, 0)::BIGINT AS reviewed_at",
    )
    .bind(contribution_id)
    .bind(reporter_id)
    .bind(&reason)
    .bind(REPORT_OPEN)
    .bind(now)
    .fetch_optional(pg)
    .await
    .map_err(db_error)?;
    if let Some(row) = inserted {
        return report_from_row(row).map_err(db_error);
    }
    Err(PublicRelayError::Conflict {
        code: "PUBLIC_RELAY_ALREADY_REPORTED",
        message: "duplicate key value violates unique constraint".to_owned(),
    })
}

async fn review_public_relay_contribution(
    pg: &PgPool,
    contribution_id: i64,
    admin_id: i64,
    approve: bool,
    note: String,
) -> Result<PublicRelayContribution, PublicRelayError> {
    let note = note.trim().to_owned();
    if contribution_id <= 0
        || admin_id <= 0
        || note.chars().count() > 2000
        || (!approve && note.chars().count() < 2)
    {
        return Err(PublicRelayError::invalid_input());
    }
    let mut transaction = pg.begin().await.map_err(db_error)?;
    let row = sqlx::query(&format!(
        "SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions WHERE id = $1 FOR UPDATE"
    ))
    .bind(contribution_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?;
    let Some(row) = row else {
        return Err(PublicRelayError::NotFound);
    };
    let item = contribution_from_row(row).map_err(db_error)?;
    if item.status != STATUS_PENDING {
        return Err(PublicRelayError::Conflict {
            code: "PUBLIC_RELAY_ALREADY_REVIEWED",
            message: "public relay contribution has already been reviewed".to_owned(),
        });
    }
    let status = if approve {
        STATUS_APPROVED
    } else {
        STATUS_REJECTED
    };
    let now = unix_now();
    let row = sqlx::query(&format!(
        "UPDATE public_relay_contributions SET status = $1, review_note = $2, reviewed_by = $3, \
         reviewed_at = $4, updated_at = $5 WHERE id = $6 RETURNING {CONTRIBUTION_COLUMNS}"
    ))
    .bind(status)
    .bind(&note)
    .bind(admin_id)
    .bind(now)
    .bind(now)
    .bind(contribution_id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(db_error)?;
    transaction.commit().await.map_err(db_error)?;
    contribution_from_row(row).map_err(db_error)
}

async fn link_public_relay_channel(
    pg: &PgPool,
    contribution_id: i64,
    channel_id: i64,
) -> Result<(), PublicRelayError> {
    if contribution_id <= 0 || channel_id <= 0 {
        return Err(PublicRelayError::invalid_input());
    }
    let group = public_relay_group(pg).await;
    let mut transaction = pg.begin().await.map_err(db_error)?;
    let contribution = sqlx::query(&format!(
        "SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions \
         WHERE id = $1 AND status = $2 FOR UPDATE"
    ))
    .bind(contribution_id)
    .bind(STATUS_APPROVED)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?;
    let Some(contribution) = contribution else {
        return Err(PublicRelayError::NotFound);
    };
    let item = contribution_from_row(contribution).map_err(db_error)?;
    let channel = sqlx::query(
        "SELECT id::BIGINT AS id, COALESCE(\"group\", '') AS \"group\", \
         COALESCE(public_relay_contribution_id, 0)::BIGINT AS public_relay_contribution_id \
         FROM channels WHERE id = $1 FOR UPDATE",
    )
    .bind(channel_id)
    .fetch_optional(&mut *transaction)
    .await
    .map_err(db_error)?;
    let Some(channel) = channel else {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_LINK_FAILED",
            message: "record not found".to_owned(),
        });
    };
    let channel_groups: String = channel.try_get("group").map_err(db_error)?;
    if !channel_in_group(&channel_groups, &group) {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_GROUP_MISMATCH",
            message: "channel is not in the configured public group".to_owned(),
        });
    }
    let linked_contribution: i64 = channel
        .try_get("public_relay_contribution_id")
        .map_err(db_error)?;
    if linked_contribution != 0 && linked_contribution != contribution_id {
        return Err(PublicRelayError::Conflict {
            code: "PUBLIC_RELAY_CHANNEL_LINKED",
            message: "channel is already linked to another public relay".to_owned(),
        });
    }
    if item.channel_id != 0 && item.channel_id != channel_id {
        return Err(PublicRelayError::Conflict {
            code: "PUBLIC_RELAY_CHANNEL_LINKED",
            message: "channel is already linked to another public relay".to_owned(),
        });
    }
    let now = unix_now();
    sqlx::query("UPDATE channels SET public_relay_contribution_id = $1 WHERE id = $2")
        .bind(contribution_id)
        .bind(channel_id)
        .execute(&mut *transaction)
        .await
        .map_err(db_error)?;
    sqlx::query(
        "UPDATE public_relay_contributions SET channel_id = $1, \"group\" = $2, updated_at = $3 \
         WHERE id = $4",
    )
    .bind(channel_id)
    .bind(&group)
    .bind(now)
    .bind(contribution_id)
    .execute(&mut *transaction)
    .await
    .map_err(db_error)?;
    transaction.commit().await.map_err(db_error)
}

async fn review_public_relay_report(
    pg: &PgPool,
    report_id: i64,
    admin_id: i64,
    close_report: bool,
    note: String,
) -> Result<(), PublicRelayError> {
    if report_id <= 0 || admin_id <= 0 {
        return Err(PublicRelayError::invalid_input());
    }
    let status = if close_report {
        REPORT_CLOSED
    } else {
        REPORT_OPEN
    };
    sqlx::query(
        "UPDATE public_relay_reports SET status = $1, reviewed_by = $2, review_note = $3, reviewed_at = $4 \
         WHERE id = $5",
    )
    .bind(status)
    .bind(admin_id)
    .bind(note.trim())
    .bind(unix_now())
    .bind(report_id)
    .execute(pg)
    .await
    .map_err(db_error)?;
    Ok(())
}

async fn list_public_relay_routing(
    pg: &PgPool,
    user_id: i64,
) -> Result<(Vec<PublicRelayRoutingItem>, String), PublicRelayError> {
    let group = public_relay_group(pg).await;
    let (disabled, ordered) = public_relay_routing_preference(pg, user_id, &group).await?;
    let qpu = quota_per_unit(pg).await;
    let mut order_pos = HashMap::new();
    for (index, id) in ordered.iter().enumerate() {
        order_pos.insert(*id, index);
    }
    let rows = sqlx::query(&format!(
        "SELECT {CONTRIBUTION_COLUMNS} FROM public_relay_contributions \
         WHERE status = $1 AND \"group\" = $2 AND channel_id > 0 \
         ORDER BY rating_average DESC, rating_count DESC, updated_at DESC, id DESC \
         LIMIT $3"
    ))
    .bind(STATUS_APPROVED)
    .bind(&group)
    .bind(ROUTING_MAX_ITEMS)
    .fetch_all(pg)
    .await
    .map_err(db_error)?;
    let mut items = rows
        .into_iter()
        .filter_map(|row| contribution_from_row(row).ok())
        .collect::<Vec<_>>();
    items.sort_by(|left, right| {
        let left_pos = order_pos.get(&left.channel_id);
        let right_pos = order_pos.get(&right.channel_id);
        match (left_pos, right_pos) {
            (Some(_), None) => std::cmp::Ordering::Less,
            (None, Some(_)) => std::cmp::Ordering::Greater,
            (Some(left_pos), Some(right_pos)) if left_pos != right_pos => left_pos.cmp(right_pos),
            _ => right
                .rating_average
                .partial_cmp(&left.rating_average)
                .unwrap_or(std::cmp::Ordering::Equal),
        }
    });
    let disabled_set = disabled.into_iter().collect::<HashSet<_>>();
    let routing = items
        .into_iter()
        .enumerate()
        .map(|(index, item)| PublicRelayRoutingItem {
            view: item.public_view(qpu),
            channel_id: item.channel_id,
            disabled: disabled_set.contains(&item.channel_id),
            position: index as i64,
        })
        .collect();
    Ok((routing, group))
}

async fn update_public_relay_routing(
    pg: &PgPool,
    user_id: i64,
    group: &str,
    disabled: Vec<i64>,
    ordered: Vec<i64>,
) -> Result<(), PublicRelayError> {
    if user_id <= 0 || group.trim().is_empty() || disabled.len() > 200 || ordered.len() > 200 {
        return Err(PublicRelayError::Business {
            code: "PUBLIC_RELAY_ROUTING_FAILED",
            message: "invalid public relay contribution".to_owned(),
        });
    }
    let valid = valid_channel_ids(pg, group, &disabled, &ordered).await?;
    let disabled = sanitize_ids(&disabled, &valid);
    let ordered = sanitize_ids(&ordered, &valid);
    let now = unix_now();
    sqlx::query(
        "INSERT INTO public_relay_preferences (user_id, \"group\", disabled_channels, ordered_channels, updated_at) \
         VALUES ($1, $2, $3, $4, $5) \
         ON CONFLICT (user_id) DO UPDATE SET \
           \"group\" = EXCLUDED.\"group\", disabled_channels = EXCLUDED.disabled_channels, \
           ordered_channels = EXCLUDED.ordered_channels, updated_at = EXCLUDED.updated_at",
    )
    .bind(user_id)
    .bind(group.trim())
    .bind(encode_public_relay_ids(&disabled))
    .bind(encode_public_relay_ids(&ordered))
    .bind(now)
    .execute(pg)
    .await
    .map_err(db_error)?;
    Ok(())
}

async fn public_relay_routing_preference(
    pg: &PgPool,
    user_id: i64,
    group: &str,
) -> Result<(Vec<i64>, Vec<i64>), PublicRelayError> {
    let row = sqlx::query(
        "SELECT COALESCE(disabled_channels, '[]') AS disabled_channels, \
         COALESCE(ordered_channels, '[]') AS ordered_channels \
         FROM public_relay_preferences WHERE user_id = $1 AND \"group\" = $2",
    )
    .bind(user_id)
    .bind(group)
    .fetch_optional(pg)
    .await
    .map_err(db_error)?;
    let Some(row) = row else {
        return Ok((Vec::new(), Vec::new()));
    };
    let disabled_raw: String = row.try_get("disabled_channels").map_err(db_error)?;
    let ordered_raw: String = row.try_get("ordered_channels").map_err(db_error)?;
    Ok((
        decode_public_relay_ids(&disabled_raw),
        decode_public_relay_ids(&ordered_raw),
    ))
}

async fn valid_channel_ids(
    pg: &PgPool,
    group: &str,
    disabled: &[i64],
    ordered: &[i64],
) -> Result<HashSet<i64>, PublicRelayError> {
    let mut candidate_ids = Vec::new();
    let mut seen = HashSet::new();
    for id in disabled.iter().chain(ordered).copied() {
        if id > 0 && seen.insert(id) {
            candidate_ids.push(id);
        }
    }
    if candidate_ids.is_empty() {
        return Ok(HashSet::new());
    }
    let rows = sqlx::query_scalar::<_, i64>(
        "SELECT channel_id::BIGINT FROM public_relay_contributions \
         WHERE status = $1 AND \"group\" = $2 AND channel_id > 0 AND channel_id = ANY($3)",
    )
    .bind(STATUS_APPROVED)
    .bind(group)
    .bind(&candidate_ids)
    .fetch_all(pg)
    .await
    .map_err(db_error)?;
    Ok(rows.into_iter().collect())
}

fn sanitize_ids(ids: &[i64], valid: &HashSet<i64>) -> Vec<i64> {
    let mut seen = HashSet::new();
    ids.iter()
        .copied()
        .filter(|id| valid.contains(id) && seen.insert(*id))
        .collect()
}

fn decode_public_relay_ids(raw: &str) -> Vec<i64> {
    let Ok(ids) = serde_json::from_str::<Vec<i64>>(raw) else {
        return Vec::new();
    };
    let mut seen = HashSet::new();
    ids.into_iter()
        .filter(|id| *id > 0 && seen.insert(*id))
        .collect()
}

fn encode_public_relay_ids(ids: &[i64]) -> String {
    serde_json::to_string(ids).unwrap_or_else(|_| "[]".to_owned())
}

fn normalize_public_relay_input(
    name: &str,
    base_url: &str,
    models: &str,
    description: &str,
) -> Result<(String, String, String, String), PublicRelayError> {
    let name = name.trim().to_owned();
    let models = models.trim().to_owned();
    let description = description.trim().to_owned();
    if name.is_empty()
        || name.chars().count() > 120
        || models.chars().count() > 4000
        || description.chars().count() > 4000
    {
        return Err(PublicRelayError::invalid_input());
    }
    let base_url = normalize_public_relay_url(base_url)?;
    Ok((name, base_url, models, description))
}

fn normalize_public_relay_url(raw: &str) -> Result<String, PublicRelayError> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Ok(String::new());
    }
    let parsed = reqwest::Url::parse(raw).map_err(|_| PublicRelayError::invalid_url())?;
    if parsed.fragment().is_some() || !parsed.username().is_empty() {
        return Err(PublicRelayError::invalid_url());
    }
    let scheme = parsed.scheme();
    if scheme != "http" && scheme != "https" {
        return Err(PublicRelayError::invalid_url());
    }
    let host = parsed
        .host_str()
        .ok_or_else(PublicRelayError::invalid_url)?;
    if host.eq_ignore_ascii_case("localhost") {
        return Err(PublicRelayError::invalid_url());
    }
    if let Ok(ip) = host.parse::<IpAddr>()
        && (ip.is_loopback()
            || ip.is_unspecified()
            || match ip {
                IpAddr::V4(v4) => v4.is_private() || v4.is_link_local(),
                IpAddr::V6(v6) => v6.is_unique_local() || is_ipv6_link_local(v6),
            })
    {
        return Err(PublicRelayError::invalid_url());
    }
    let path = parsed.path().trim_end_matches('/');
    let host = parsed.host().ok_or_else(PublicRelayError::invalid_url)?;
    if path.is_empty() {
        Ok(format!("{}://{host}", parsed.scheme()))
    } else {
        Ok(format!("{}://{host}{path}", parsed.scheme()))
    }
}

fn is_ipv6_link_local(ip: std::net::Ipv6Addr) -> bool {
    (ip.segments()[0] & 0xffc0) == 0xfe80
}

fn normalize_public_relay_channel_config(
    raw_config: Option<&Value>,
    name: &str,
    base_url: &str,
    models: &str,
) -> Result<String, PublicRelayError> {
    let raw_config = raw_config
        .map(|value| {
            if value.is_null() {
                String::new()
            } else {
                value.to_string()
            }
        })
        .unwrap_or_default();
    let raw_config = raw_config.trim();
    if raw_config.is_empty() || raw_config.len() > 128 * 1024 {
        return Err(PublicRelayError::invalid_input());
    }
    let envelope: Value =
        serde_json::from_str(raw_config).map_err(|_| PublicRelayError::invalid_input())?;
    let channel = envelope
        .get("channel")
        .ok_or_else(PublicRelayError::invalid_input)?;
    let channel_name = channel
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim();
    let channel_key = channel
        .get("key")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim();
    let channel_models = channel
        .get("models")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim();
    if channel_name != name.trim() || channel_key.is_empty() || channel_models != models.trim() {
        return Err(PublicRelayError::invalid_input());
    }
    let config_base_url = normalize_public_relay_url(
        channel
            .get("base_url")
            .and_then(Value::as_str)
            .unwrap_or_default(),
    )?;
    if config_base_url != base_url {
        return Err(PublicRelayError::invalid_input());
    }
    serde_json::to_string(&envelope).map_err(|_| PublicRelayError::invalid_input())
}

fn channel_in_group(channel_groups: &str, group: &str) -> bool {
    channel_groups
        .split(',')
        .map(str::trim)
        .any(|candidate| candidate == group)
}

async fn public_relay_group(pg: &PgPool) -> String {
    let raw = sqlx::query_scalar::<_, Option<String>>(
        "SELECT value FROM options WHERE key = 'public_relay_setting.group'",
    )
    .fetch_optional(pg)
    .await
    .ok()
    .flatten()
    .flatten()
    .unwrap_or_default();
    let group = raw.trim();
    if group.is_empty() {
        "FREE".to_owned()
    } else {
        group.to_owned()
    }
}

async fn quota_per_unit(pg: &PgPool) -> f64 {
    sqlx::query_scalar::<_, Option<String>>("SELECT value FROM options WHERE key = 'QuotaPerUnit'")
        .fetch_optional(pg)
        .await
        .ok()
        .flatten()
        .flatten()
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite() && *value > 0.0)
        .unwrap_or(DEFAULT_QUOTA_PER_UNIT)
}

async fn user_email(pg: &PgPool, user_id: i64) -> Result<String, sqlx::Error> {
    sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(email, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
}

async fn user_group(pg: &PgPool, user_id: i64) -> String {
    sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(\"group\", '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .ok()
    .flatten()
    .unwrap_or_default()
}

async fn is_user_selectable_group(pg: &PgPool, user_group: &str, group_name: &str) -> bool {
    if group_name.is_empty() || group_name == "auto" {
        return false;
    }
    let options = load_group_options(pg).await;
    let usable = json_object(options.get("UserUsableGroups"));
    let ratios = json_object(options.get("GroupRatio"));
    (usable.contains_key(group_name) || user_group == group_name) && ratios.contains_key(group_name)
}

async fn load_group_options(pg: &PgPool) -> HashMap<String, String> {
    let rows = sqlx::query("SELECT key, value FROM options WHERE key = ANY($1)")
        .bind(["UserUsableGroups", "GroupRatio"])
        .fetch_all(pg)
        .await;
    rows.map_or_else(
        |_| HashMap::new(),
        |rows| {
            rows.into_iter()
                .filter_map(|row| {
                    Some((
                        row.try_get::<String, _>("key").ok()?,
                        row.try_get::<String, _>("value").ok()?,
                    ))
                })
                .collect()
        },
    )
}

fn json_object(raw: Option<&String>) -> HashMap<String, Value> {
    raw.and_then(|value| serde_json::from_str::<Value>(value).ok())
        .and_then(|value| value.as_object().cloned())
        .map(|object| object.into_iter().collect())
        .unwrap_or_default()
}

async fn record_log(pg: &PgPool, user_id: i64, log_type: i64, content: String) -> sqlx::Result<()> {
    let username = sqlx::query_scalar::<_, String>(
        "SELECT COALESCE(username, '') FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await?
    .unwrap_or_default();
    sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, \
         token_name, model_name, quota, prompt_tokens, completion_tokens, \
         use_time, is_stream, channel_id, token_id, \"group\", ip, other) \
         VALUES ($1, $2, $3, $4, $5, '', '', 0, 0, 0, 0, FALSE, 0, 0, '', '', '')",
    )
    .bind(user_id)
    .bind(unix_now())
    .bind(log_type)
    .bind(content)
    .bind(username)
    .execute(pg)
    .await?;
    Ok(())
}

fn quota_from_float(value: f64) -> i64 {
    if !value.is_finite() {
        return 0;
    }
    value.trunc() as i64
}

async fn optional_authenticated(
    state: &PublicRelayState,
    headers: &HeaderMap,
) -> Result<(), Response> {
    let Some(token) = crate::routes::legacy_http::dashboard_credential(headers) else {
        return Ok(());
    };
    if !dashboard_token_candidate(&token) {
        let pat_owner = sqlx::query_scalar::<_, i64>(
            "SELECT id FROM users WHERE access_token = $1 AND deleted_at IS NULL",
        )
        .bind(&token)
        .fetch_optional(&state.pg)
        .await
        .map_err(|_| api_error("database error".to_owned()))?;
        if pat_owner.is_none() {
            return Ok(());
        }
    }
    state
        .auth
        .self_user_for_optional(SecretString::from(token))
        .await
        .map_err(|error| {
            crate::routes::legacy_http::simple_dashboard_auth_error(headers, Some(error.kind))
        })?;
    Ok(())
}

async fn authenticated_user(
    state: &PublicRelayState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let credential = crate::routes::legacy_http::dashboard_credential(headers)
        .ok_or_else(|| crate::routes::legacy_http::simple_dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| {
            crate::routes::legacy_http::simple_dashboard_auth_error(headers, Some(error.kind))
        })?;
    enforce_user_auth_view(&user)
        .map_err(|error| crate::routes::legacy_http::simple_user_auth_error(headers, error))?;
    Ok(Principal { user })
}

async fn authenticated_admin(
    state: &PublicRelayState,
    headers: &HeaderMap,
) -> Result<Principal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(crate::routes::legacy_http::simple_user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(principal)
}

async fn critical_rate_limit(state: &PublicRelayState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

async fn parse_json<T>(request: Request, limit: usize) -> Result<T, Response>
where
    T: for<'de> Deserialize<'de>,
{
    let bytes = to_bytes(request.into_body(), limit)
        .await
        .map_err(|_| invalid_parameters())?;
    serde_json::from_slice(&bytes).map_err(|_| invalid_parameters())
}

fn parse_positive_id(raw: &str) -> Result<i64, Response> {
    raw.parse::<i64>().ok().filter(|id| *id > 0).ok_or_else(|| {
        public_relay_failure(
            StatusCode::BAD_REQUEST,
            "PUBLIC_RELAY_INVALID_ID",
            "invalid public relay id",
        )
    })
}

fn query_value(raw_query: Option<&str>, key: &str) -> Option<String> {
    form_urlencoded::parse(raw_query.unwrap_or_default().as_bytes())
        .find_map(|(candidate, value)| (candidate == key).then_some(value.into_owned()))
}

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone())
}

fn invalid_parameters() -> Response {
    api_error("Invalid parameters".to_owned())
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn success(data: Value) -> Response {
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data,
    })
    .into_response()
}

fn api_error(message: String) -> Response {
    Json(json!({"success": false, "message": message})).into_response()
}

fn public_relay_failure(status: StatusCode, code: &'static str, message: &str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
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

fn with_optional_auth_version(mut response: Response, authenticated: bool) -> Response {
    if authenticated {
        response
            .headers_mut()
            .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    }
    response
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

fn db_error(error: sqlx::Error) -> PublicRelayError {
    PublicRelayError::Database(error.to_string())
}
