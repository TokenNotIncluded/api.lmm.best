//! Open-source bounty discovery and settlement notifications.
//!
//! The Go service exposes the public bounty list through `TryUserAuth`: an
//! anonymous visitor may browse published/paused projects, while a valid
//! dashboard credential receives the current user's challenge for each
//! project. This slice owns public discovery, authenticated read views, owner
//! lifecycle changes, escrow and challenge settlement, MCP credentials, and
//! settlement-notification acknowledgement paths.

use crate::{ClientIpKey, legacy_empty_response};
use axum::{
    Json, Router,
    extract::{Extension, Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use lmm_contracts::LegacySuccessEnvelope;
use rand::RngCore;
use secrecy::SecretString;
use serde::{Deserialize, Serialize, Serializer, de::DeserializeOwned};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgRow};
use std::sync::Arc;

use crate::auth::{AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth};

const MAX_PAGE_SIZE: i64 = 50;
const DEFAULT_PAGE_SIZE: i64 = 20;
const ENABLED_USER_STATUS: i64 = 1;
const ROLE_ADMIN: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const MCP_PROTOCOL_VERSION: &str = "2026-07-28";
const MCP_TOKEN_PREFIX: &str = "lmm_mcp_";

/// PostgreSQL and dashboard-auth dependencies for the public bounty slice.
#[derive(Clone)]
pub struct OpenSourceBountyState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

impl OpenSourceBountyState {
    /// Creates the public bounty state from the listener's shared authorities.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self { pg, auth }
    }
}

/// Public discovery and authenticated bounty notification routes.
pub fn router(state: OpenSourceBountyState) -> Router {
    Router::new()
        .route(
            "/api/open-source-bounties",
            get(list_bounties).post(create_draft),
        )
        .route(
            "/api/open-source-bounties/projects/{id}",
            get(detail_bounty).put(update_draft).delete(delete_draft),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/pause",
            post(pause_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/resume",
            post(resume_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/publish",
            post(publish_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/close",
            post(close_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/archive",
            post(archive_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/unarchive",
            post(unarchive_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/accept",
            post(accept_bounty),
        )
        .route(
            "/api/open-source-bounties/projects/{id}/submit",
            post(submit_bounty),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/withdraw",
            post(withdraw_challenge),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/cancel",
            post(cancel_challenge),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/approve",
            post(approve_challenge),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/reject",
            post(reject_challenge),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/rate-owner",
            post(rate_owner),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/tip",
            post(tip_challenge),
        )
        .route(
            "/api/open-source-bounties/challenges/{challenge_id}/disputes",
            post(open_dispute),
        )
        .route(
            "/api/open-source-bounties/disputes/{dispute_id}/resolve",
            post(resolve_dispute),
        )
        .route("/api/open-source-bounties/config", get(bounty_config))
        .route("/api/open-source-bounties/mine", get(owned_bounties))
        .route("/api/open-source-bounties/accepted", get(accepted_bounties))
        .route(
            "/api/open-source-bounties/disputes/mine",
            get(owned_disputes),
        )
        .route(
            "/api/open-source-bounties/disputes/admin",
            get(admin_disputes),
        )
        .route(
            "/api/open-source-bounties/mcp-token",
            get(mcp_token_status)
                .post(rotate_mcp_token)
                .delete(revoke_mcp_token),
        )
        .route(
            "/api/open-source-bounties/notifications",
            get(list_notifications),
        )
        .route(
            "/api/open-source-bounties/notifications/read",
            post(mark_notifications_read),
        )
        .route(
            "/api/open-source-bounties/tips/received",
            get(list_tip_notifications),
        )
        .route(
            "/api/open-source-bounties/tips/received/read",
            post(mark_tip_notifications_read),
        )
        .route(
            "/api/open-source-bounties/tips/{tip_id}/thank",
            post(thank_tip),
        )
        .layer(middleware::from_fn(disable_mcp_cache))
        .with_state(state)
}

#[derive(Debug, Deserialize)]
struct ListQuery {
    page: Option<String>,
    page_size: Option<String>,
}

#[derive(Debug, Deserialize)]
struct DisputeListQuery {
    status: Option<String>,
    limit: Option<String>,
}

#[derive(Debug, Deserialize)]
struct NotificationQuery {
    limit: Option<String>,
}

#[derive(Debug, Deserialize)]
struct OwnedQuery {
    archived: Option<String>,
}

impl OwnedQuery {
    fn archived(&self) -> bool {
        self.archived
            .as_deref()
            .is_some_and(|value| matches!(value, "1" | "t" | "T" | "TRUE" | "true" | "True"))
    }
}

impl NotificationQuery {
    fn normalized_limit(&self) -> i64 {
        self.limit
            .as_deref()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|limit| (1..=100).contains(limit))
            .unwrap_or(50)
    }
}

impl DisputeListQuery {
    fn normalized(&self) -> Result<(Option<&str>, i64), Box<Response>> {
        let status = self
            .status
            .as_deref()
            .map(str::trim)
            .filter(|value| matches!(*value, "open" | "resolved_paid" | "resolved_denied"));
        if self.status.as_deref().is_some_and(|value| {
            let value = value.trim();
            !value.is_empty() && status.is_none()
        }) {
            return Err(Box::new(business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_FILTER",
                "invalid bounty dispute status filter",
            )));
        }
        let limit = self
            .limit
            .as_deref()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|limit| *limit > 0)
            .unwrap_or(50)
            .min(100);
        Ok((status, limit))
    }
}

impl ListQuery {
    fn normalized(&self) -> (i64, i64) {
        let page = self
            .page
            .as_deref()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|page| *page >= 1)
            .unwrap_or(1);
        let page_size = self
            .page_size
            .as_deref()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|value| (1..=MAX_PAGE_SIZE).contains(value))
            .unwrap_or(DEFAULT_PAGE_SIZE);
        (page, page_size)
    }
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct DraftInput {
    repository_url: String,
    title: String,
    description: String,
    rules: String,
    reward_quota: i64,
    reward_slots: i64,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct AcceptInput {
    github_handle: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct SubmitInput {
    issue_url: String,
    pull_request_url: String,
    submission_note: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct ReviewInput {
    review_note: String,
    rating_score: i64,
    rating_comment: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct RatingInput {
    score: i64,
    comment: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct TipInput {
    quota: i64,
    note: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct DisputeInput {
    reason: String,
    statement: String,
}

#[derive(Debug, Default, Deserialize)]
#[serde(default)]
struct ResolutionInput {
    action: String,
    resolution: String,
}

#[derive(Debug, Serialize)]
struct BountyProject {
    id: i64,
    owner_user_id: i64,
    repository_url: String,
    title: String,
    description: String,
    rules: String,
    reward_quota: i64,
    net_reward_quota: i64,
    reward_slots: i64,
    escrow_quota: i64,
    platform_fee_rate_bps: i64,
    platform_fee_quota: i64,
    status: String,
    created_at: i64,
    updated_at: i64,
    published_at: i64,
    closed_at: i64,
    archived_at: i64,
}

const RAW_PROJECT_SELECT: &str = "SELECT id::BIGINT AS id, owner_user_id::BIGINT AS owner_user_id, repository_url, title, description, rules, reward_quota::BIGINT AS reward_quota, net_reward_quota::BIGINT AS net_reward_quota, reward_slots::BIGINT AS reward_slots, escrow_quota::BIGINT AS escrow_quota, platform_fee_rate_bps::BIGINT AS platform_fee_rate_bps, platform_fee_quota::BIGINT AS platform_fee_quota, status, created_at::BIGINT AS created_at, updated_at::BIGINT AS updated_at, published_at::BIGINT AS published_at, closed_at::BIGINT AS closed_at, archived_at::BIGINT AS archived_at FROM open_source_bounty_projects WHERE id = $1";

const RAW_OWNED_PROJECT_SELECT_FOR_UPDATE: &str = "SELECT id::BIGINT AS id, owner_user_id::BIGINT AS owner_user_id, repository_url, title, description, rules, reward_quota::BIGINT AS reward_quota, net_reward_quota::BIGINT AS net_reward_quota, reward_slots::BIGINT AS reward_slots, escrow_quota::BIGINT AS escrow_quota, platform_fee_rate_bps::BIGINT AS platform_fee_rate_bps, platform_fee_quota::BIGINT AS platform_fee_quota, status, created_at::BIGINT AS created_at, updated_at::BIGINT AS updated_at, published_at::BIGINT AS published_at, closed_at::BIGINT AS closed_at, archived_at::BIGINT AS archived_at FROM open_source_bounty_projects WHERE id = $1 AND owner_user_id = $2 FOR UPDATE";

fn raw_project_from_row(row: &PgRow) -> Result<BountyProject, sqlx::Error> {
    Ok(BountyProject {
        id: row.try_get("id")?,
        owner_user_id: row.try_get("owner_user_id")?,
        repository_url: row.try_get("repository_url")?,
        title: row.try_get("title")?,
        description: row.try_get("description")?,
        rules: row.try_get("rules")?,
        reward_quota: row.try_get("reward_quota")?,
        net_reward_quota: row.try_get("net_reward_quota")?,
        reward_slots: row.try_get("reward_slots")?,
        escrow_quota: row.try_get("escrow_quota")?,
        platform_fee_rate_bps: row.try_get("platform_fee_rate_bps")?,
        platform_fee_quota: row.try_get("platform_fee_quota")?,
        status: row.try_get("status")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
        published_at: row.try_get("published_at")?,
        closed_at: row.try_get("closed_at")?,
        archived_at: row.try_get("archived_at")?,
    })
}

fn normalize_github_name(value: &str) -> bool {
    let bytes = value.as_bytes();
    if bytes.is_empty() || bytes.len() > 100 {
        return false;
    }
    if !bytes[0].is_ascii_alphanumeric() || !bytes[bytes.len() - 1].is_ascii_alphanumeric() {
        return false;
    }
    bytes
        .iter()
        .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-' | b'.'))
}

fn normalize_repository_url(raw: &str) -> Result<String, (&'static str, &'static str)> {
    let url = reqwest::Url::parse(raw.trim()).map_err(|_| {
        (
            "OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY",
            "repository must be a public GitHub HTTPS URL",
        )
    })?;
    if !url.scheme().eq_ignore_ascii_case("https")
        || !url
            .host_str()
            .is_some_and(|host| host.eq_ignore_ascii_case("github.com"))
    {
        return Err((
            "OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY",
            "repository must be a public GitHub HTTPS URL",
        ));
    }
    let segments = url
        .path_segments()
        .ok_or((
            "OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY",
            "repository must point to a GitHub owner and repository",
        ))?
        .filter(|segment| !segment.is_empty())
        .collect::<Vec<_>>();
    if segments.len() != 2 {
        return Err((
            "OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY",
            "repository must point to a GitHub owner and repository",
        ));
    }
    let owner = segments[0];
    let repository = segments[1].trim_end_matches(".git");
    if owner.is_empty()
        || repository.is_empty()
        || !normalize_github_name(owner)
        || !normalize_github_name(repository)
    {
        return Err((
            "OPEN_SOURCE_BOUNTY_INVALID_REPOSITORY",
            "repository contains an invalid GitHub owner or repository name",
        ));
    }
    Ok(format!("https://github.com/{owner}/{repository}"))
}

fn normalize_github_handle(raw: &str) -> Result<String, Box<Response>> {
    let handle = raw.trim().strip_prefix('@').unwrap_or(raw.trim());
    if !normalize_github_name(handle) {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_HANDLE",
            "GitHub handle is invalid",
        )));
    }
    Ok(handle.to_owned())
}

fn normalize_github_evidence(
    raw: &str,
    repository_url: &str,
    kind: &str,
) -> Result<String, Box<Response>> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Ok(String::new());
    }
    let url = reqwest::Url::parse(raw).map_err(|_| {
        Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE",
            "submitted Issue and pull request links must be GitHub HTTPS URLs",
        ))
    })?;
    if !url.scheme().eq_ignore_ascii_case("https")
        || !url
            .host_str()
            .is_some_and(|host| host.eq_ignore_ascii_case("github.com"))
    {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE",
            "submitted Issue and pull request links must be GitHub HTTPS URLs",
        )));
    }
    let segments = url
        .path_segments()
        .ok_or_else(|| {
            Box::new(business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE",
                "Issue or pull request URL has an invalid path",
            ))
        })?
        .filter(|segment| !segment.is_empty())
        .collect::<Vec<_>>();
    if segments.len() != 4 || segments[2] != kind {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE",
            "Issue or pull request URL has an invalid path",
        )));
    }
    if segments[3].parse::<i64>().is_err() {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_EVIDENCE",
            "Issue or pull request number is invalid",
        )));
    }
    let repo = normalize_repository_url(&format!(
        "https://github.com/{}/{}",
        segments[0], segments[1]
    ))
    .map_err(|_| {
        Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_EVIDENCE_REPOSITORY_MISMATCH",
            "every submitted Issue or pull request must belong to the bounty repository",
        ))
    })?;
    if !repo.eq_ignore_ascii_case(repository_url) {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_EVIDENCE_REPOSITORY_MISMATCH",
            "every submitted Issue or pull request must belong to the bounty repository",
        )));
    }
    Ok(format!("{repo}/{kind}/{}", segments[3]))
}

fn normalize_draft(input: DraftInput) -> Result<DraftInput, Box<Response>> {
    let repository_url = normalize_repository_url(&input.repository_url)
        .map_err(|(code, message)| Box::new(business_failure(code, message)))?;
    let title = input.title.trim().to_owned();
    let description = input.description.trim().to_owned();
    let rules = input.rules.trim().to_owned();
    if !(4..=120).contains(&title.len()) {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_TITLE",
            "title must contain 4 to 120 characters",
        )));
    }
    if !(20..=2000).contains(&description.len()) {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_DESCRIPTION",
            "description must contain 20 to 2000 characters",
        )));
    }
    if !(20..=5000).contains(&rules.len()) {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_RULES",
            "rules must contain 20 to 5000 characters",
        )));
    }
    if input.reward_quota <= 0
        || input.reward_slots < 1
        || input.reward_slots > 100
        || input.reward_quota.checked_mul(input.reward_slots).is_none()
    {
        return Err(Box::new(business_failure(
            if input.reward_quota <= 0 {
                "OPEN_SOURCE_BOUNTY_INVALID_QUOTA"
            } else if !(1..=100).contains(&input.reward_slots) {
                "OPEN_SOURCE_BOUNTY_INVALID_SLOTS"
            } else {
                "OPEN_SOURCE_BOUNTY_INVALID_QUOTA"
            },
            if input.reward_quota <= 0 {
                "reward quota must be positive"
            } else if !(1..=100).contains(&input.reward_slots) {
                "reward slots must be between 1 and 100"
            } else {
                "bounty quota is too large"
            },
        )));
    }
    Ok(DraftInput {
        repository_url,
        title,
        description,
        rules,
        ..input
    })
}

async fn read_draft_input(request: Request) -> Result<DraftInput, Response> {
    read_json_input(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty request",
    )
    .await
}

async fn read_json_input<T: DeserializeOwned>(
    request: Request,
    code: &'static str,
    message: &'static str,
) -> Result<T, Response> {
    let body = axum::body::to_bytes(request.into_body(), 2 * 1024 * 1024)
        .await
        .map_err(|_| business_failure(code, message))?;
    serde_json::from_slice::<T>(&body).map_err(|_| business_failure(code, message))
}

async fn load_raw_project(
    state: &OpenSourceBountyState,
    project_id: i64,
) -> Result<Option<BountyProject>, sqlx::Error> {
    sqlx::query(RAW_PROJECT_SELECT)
        .bind(project_id)
        .fetch_optional(&state.pg)
        .await?
        .as_ref()
        .map(raw_project_from_row)
        .transpose()
}

fn project_id_from_path(path: &str) -> Result<i64, Box<Response>> {
    path.trim_end_matches('/')
        .rsplit('/')
        .next()
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .ok_or_else(|| {
            Box::new(business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            ))
        })
}

async fn create_draft(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let input = match read_draft_input(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let input = match normalize_draft(input) {
        Ok(input) => input,
        Err(response) => return *response,
    };
    let now = chrono::Utc::now().timestamp();
    let project_id = match sqlx::query_scalar::<_, i64>(
        "INSERT INTO open_source_bounty_projects (owner_user_id, repository_url, title, description, rules, reward_quota, reward_slots, status, created_at, updated_at, published_at, closed_at) VALUES ($1,$2,$3,$4,$5,$6,$7,'draft',$8,$8,0,0) RETURNING id::BIGINT",
    )
    .bind(viewer_id)
    .bind(&input.repository_url)
    .bind(&input.title)
    .bind(&input.description)
    .bind(&input.rules)
    .bind(input.reward_quota)
    .bind(input.reward_slots)
    .bind(now)
    .fetch_one(&state.pg)
    .await
    {
        Ok(project_id) => project_id,
        Err(error) => {
            tracing::error!(%error, viewer_id, "failed to create open-source bounty draft");
            return internal_failure();
        }
    };
    let project = match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => project,
        Ok(None) | Err(_) => return internal_failure(),
    };
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: project,
    })
    .into_response()
}

async fn update_draft(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match project_id_from_path(request.uri().path()) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let input = match read_draft_input(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let input = match normalize_draft(input) {
        Ok(input) => input,
        Err(response) => return *response,
    };
    let result = sqlx::query(
        "UPDATE open_source_bounty_projects SET repository_url=$1,title=$2,description=$3,rules=$4,reward_quota=$5,reward_slots=$6,updated_at=$7 WHERE id=$8 AND owner_user_id=$9 AND status='draft'",
    )
    .bind(&input.repository_url)
    .bind(&input.title)
    .bind(&input.description)
    .bind(&input.rules)
    .bind(input.reward_quota)
    .bind(input.reward_slots)
    .bind(chrono::Utc::now().timestamp())
    .bind(project_id)
    .bind(viewer_id)
    .execute(&state.pg)
    .await;
    match result {
        Ok(result) if result.rows_affected() == 1 => {}
        Ok(_) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_DRAFT_NOT_FOUND",
                "editable bounty draft was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to update open-source bounty draft");
            return internal_failure();
        }
    }
    match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: project,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn delete_draft(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match project_id_from_path(request.uri().path()) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    match sqlx::query(
        "DELETE FROM open_source_bounty_projects WHERE id=$1 AND owner_user_id=$2 AND status='draft'",
    )
    .bind(project_id)
    .bind(viewer_id)
    .execute(&state.pg)
    .await
    {
        Ok(result) if result.rows_affected() == 1 => Json(LegacySuccessEnvelope { success: true, message: "", data: Value::Null }).into_response(),
        Ok(_) => business_failure("OPEN_SOURCE_BOUNTY_DRAFT_NOT_FOUND", "deletable bounty draft was not found"),
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to delete open-source bounty draft");
            internal_failure()
        }
    }
}

async fn set_bounty_paused(
    State(state): State<OpenSourceBountyState>,
    Path(project_id): Path<String>,
    headers: HeaderMap,
    paused: bool,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match project_id.parse::<i64>() {
        Ok(project_id) if project_id > 0 => project_id,
        _ => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let (from, to) = if paused {
        ("published", "paused")
    } else {
        ("paused", "published")
    };
    match sqlx::query("UPDATE open_source_bounty_projects SET status=$1,updated_at=$2 WHERE id=$3 AND owner_user_id=$4 AND status=$5")
        .bind(to)
        .bind(chrono::Utc::now().timestamp())
        .bind(project_id)
        .bind(viewer_id)
        .bind(from)
        .execute(&state.pg)
        .await
    {
        Ok(result) if result.rows_affected() == 1 => {}
        Ok(_) => return business_failure("OPEN_SOURCE_BOUNTY_INVALID_STATE", "bounty cannot change to the requested state"),
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to change open-source bounty state");
            return internal_failure();
        }
    }
    match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: project,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn pause_bounty(
    state: State<OpenSourceBountyState>,
    path: Path<String>,
    headers: HeaderMap,
) -> Response {
    set_bounty_paused(state, path, headers, true).await
}

async fn resume_bounty(
    state: State<OpenSourceBountyState>,
    path: Path<String>,
    headers: HeaderMap,
) -> Response {
    set_bounty_paused(state, path, headers, false).await
}

fn parse_positive_id(
    value: &str,
    code: &'static str,
    message: &'static str,
) -> Result<i64, Box<Response>> {
    value
        .parse::<i64>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or_else(|| Box::new(business_failure(code, message)))
}

fn project_action_id(path: &str) -> Result<i64, Box<Response>> {
    let segments = path.trim_matches('/').split('/').collect::<Vec<_>>();
    let Some(index) = segments.iter().position(|segment| *segment == "projects") else {
        return Err(Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_ID",
            "invalid open-source bounty identifier",
        )));
    };
    let value = segments.get(index + 1).ok_or_else(|| {
        Box::new(business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_ID",
            "invalid open-source bounty identifier",
        ))
    })?;
    parse_positive_id(
        value,
        "OPEN_SOURCE_BOUNTY_INVALID_ID",
        "invalid open-source bounty identifier",
    )
}

async fn publish_bounty(
    State(state): State<OpenSourceBountyState>,
    Path(project_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match parse_positive_id(
        &project_id,
        "OPEN_SOURCE_BOUNTY_INVALID_ID",
        "invalid open-source bounty identifier",
    ) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to begin bounty publication");
            return internal_failure();
        }
    };
    let project_row = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load bounty for publication");
            return internal_failure();
        }
    };
    let project = match raw_project_from_row(&project_row) {
        Ok(project) if project.owner_user_id == viewer_id => project,
        Ok(_) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to decode bounty for publication");
            return internal_failure();
        }
    };
    if project.status != "draft" {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_STATE",
            "only a draft bounty can be published",
        );
    }
    let fee_raw = match sqlx::query_scalar::<_, Option<String>>(
        "SELECT value FROM options WHERE key = 'OpenSourceBountyFeeRate'",
    )
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(value) => value.flatten().unwrap_or_default(),
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load bounty fee configuration");
            return internal_failure();
        }
    };
    let fee_rate_bps = parse_fee_rate_basis_points(&fee_raw).unwrap_or(100);
    let gross = match project.reward_quota.checked_mul(project.reward_slots) {
        Some(gross) if gross > 0 => gross,
        _ => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_QUOTA",
                "bounty quota is too large",
            );
        }
    };
    let fee_per_slot = (project.reward_quota / 10_000)
        .saturating_mul(fee_rate_bps)
        .saturating_add(
            (project.reward_quota % 10_000)
                .saturating_mul(fee_rate_bps)
                .saturating_add(9_999)
                / 10_000,
        );
    if fee_per_slot < 0 || fee_per_slot >= project.reward_quota {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_FEE",
            "platform fee leaves no contributor reward",
        );
    }
    let net_reward = project.reward_quota - fee_per_slot;
    let escrow = match net_reward.checked_mul(project.reward_slots) {
        Some(value) if value >= 0 => value,
        _ => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_QUOTA",
                "bounty quota is too large",
            );
        }
    };
    let platform_fee = match fee_per_slot.checked_mul(project.reward_slots) {
        Some(value) if value >= 0 && value.checked_add(escrow) == Some(gross) => value,
        _ => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_QUOTA",
                "bounty quota is too large",
            );
        }
    };
    let charged_quota = gross;
    let debit = match sqlx::query(
        "UPDATE users SET quota = quota - $1 WHERE id = $2 AND deleted_at IS NULL AND quota >= $1",
    )
    .bind(charged_quota)
    .bind(viewer_id)
    .execute(&mut *transaction)
    .await
    {
        Ok(result) => result,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to charge bounty publication");
            return internal_failure();
        }
    };
    if debit.rows_affected() != 1 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE",
            "insufficient balance to publish this bounty",
        );
    }
    let mut fee_recipient = 0_i64;
    if platform_fee > 0 {
        fee_recipient = match sqlx::query_scalar::<_, i64>(
            "SELECT id::BIGINT FROM users WHERE role = 10 AND status = 1 AND deleted_at IS NULL ORDER BY id ASC LIMIT 1",
        )
        .fetch_optional(&mut *transaction)
        .await
        {
            Ok(Some(id)) => id,
            Ok(None) => {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND",
                    "an enabled super administrator is required to receive the platform fee",
                );
            }
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to load bounty fee recipient");
                return internal_failure();
            }
        };
        let credit = match sqlx::query(
            "UPDATE users SET quota = quota + $1 WHERE id = $2 AND role = 10 AND status = 1 AND deleted_at IS NULL",
        )
        .bind(platform_fee)
        .bind(fee_recipient)
        .execute(&mut *transaction)
        .await
        {
            Ok(result) => result,
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to credit bounty fee recipient");
                return internal_failure();
            }
        };
        if credit.rows_affected() != 1 {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_FEE_RECIPIENT_NOT_FOUND",
                "the super administrator fee account is unavailable",
            );
        }
    }
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_projects SET status='published', escrow_quota=$1, net_reward_quota=$2, platform_fee_rate_bps=$3, platform_fee_quota=$4, published_at=$5, updated_at=$5 WHERE id=$6",
    )
    .bind(escrow)
    .bind(net_reward)
    .bind(fee_rate_bps)
    .bind(platform_fee)
    .bind(now)
    .bind(project_id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, project_id, "failed to publish bounty project");
        return internal_failure();
    }
    if let Err(error) = sqlx::query(
        "INSERT INTO open_source_bounty_ledgers (project_id, user_id, kind, quota, created_at) VALUES ($1,$2,'escrow_fund',$3,$4)",
    )
    .bind(project_id)
    .bind(viewer_id)
    .bind(escrow)
    .bind(now)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, project_id, "failed to record bounty escrow");
        return internal_failure();
    }
    if platform_fee > 0
        && let Err(error) = sqlx::query(
            "INSERT INTO open_source_bounty_ledgers (project_id, user_id, counterparty_user_id, kind, quota, created_at) VALUES ($1,$2,$3,'platform_fee',$4,$5)",
        )
        .bind(project_id)
        .bind(viewer_id)
        .bind(fee_recipient)
        .bind(platform_fee)
        .bind(now)
        .execute(&mut *transaction)
        .await
        {
            tracing::error!(%error, viewer_id, project_id, "failed to record bounty platform fee");
            return internal_failure();
        }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, project_id, "failed to commit bounty publication");
        return internal_failure();
    }
    let project = match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => project,
        Ok(None) | Err(_) => return internal_failure(),
    };
    let remaining_quota = match sqlx::query_scalar::<_, i64>(
        "SELECT quota::BIGINT FROM users WHERE id = $1",
    )
    .bind(viewer_id)
    .fetch_one(&state.pg)
    .await
    {
        Ok(quota) => quota,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load remaining bounty quota");
            return internal_failure();
        }
    };
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: serde_json::json!({
            "project": project,
            "charged_quota": charged_quota,
            "remaining_quota": remaining_quota,
        }),
    })
    .into_response()
}

async fn close_bounty(
    State(state): State<OpenSourceBountyState>,
    Path(project_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match parse_positive_id(
        &project_id,
        "OPEN_SOURCE_BOUNTY_INVALID_ID",
        "invalid open-source bounty identifier",
    ) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to begin bounty close");
            return internal_failure();
        }
    };
    let row = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load bounty for close");
            return internal_failure();
        }
    };
    let project = match raw_project_from_row(&row) {
        Ok(project) if project.owner_user_id == viewer_id => project,
        Ok(_) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to decode bounty for close");
            return internal_failure();
        }
    };
    if !matches!(project.status.as_str(), "published" | "paused") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_STATE",
            "only a published or paused bounty can be closed",
        );
    }
    let active = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges WHERE project_id=$1 AND status IN ('accepted','submitted')",
    )
    .bind(project_id)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(count) => count,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to count active bounty challenges");
            return internal_failure();
        }
    };
    if active > 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_ACTIVE_CHALLENGES",
            "cancel unsubmitted challenges or review submitted work before closing the bounty",
        );
    }
    let open_disputes = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE project_id=$1 AND status='open'",
    )
    .bind(project_id)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(count) => count,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to count open bounty disputes");
            return internal_failure();
        }
    };
    if open_disputes > 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_OPEN_DISPUTES",
            "resolve open bounty disputes before closing or refunding escrow",
        );
    }
    let appeal_cutoff = chrono::Utc::now().timestamp() - 7 * 24 * 60 * 60;
    let appealable = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges c WHERE c.project_id=$1 AND c.status='rejected' AND c.rejected_at>$2 AND NOT EXISTS (SELECT 1 FROM open_source_bounty_disputes d WHERE d.challenge_id=c.id AND d.status IN ('resolved_paid','resolved_denied'))",
    )
    .bind(project_id)
    .bind(appeal_cutoff)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(count) => count,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to count appealable bounty rejections");
            return internal_failure();
        }
    };
    if appealable > 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_APPEAL_WINDOW",
            "rejected challenges remain appealable for seven days before escrow can be refunded",
        );
    }
    let refunded_quota = project.escrow_quota;
    if refunded_quota > 0 {
        let credit = match sqlx::query(
            "UPDATE users SET quota=quota+$1 WHERE id=$2 AND deleted_at IS NULL",
        )
        .bind(refunded_quota)
        .bind(viewer_id)
        .execute(&mut *transaction)
        .await
        {
            Ok(result) => result,
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to refund bounty escrow");
                return internal_failure();
            }
        };
        if credit.rows_affected() != 1 {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_OWNER_NOT_FOUND",
                "bounty owner was not found",
            );
        }
    }
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_projects SET status='closed', escrow_quota=0, closed_at=$1, updated_at=$1 WHERE id=$2",
    )
    .bind(now)
    .bind(project_id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, project_id, "failed to close bounty project");
        return internal_failure();
    }
    if refunded_quota > 0
        && let Err(error) = sqlx::query(
            "INSERT INTO open_source_bounty_ledgers (project_id, user_id, kind, quota, created_at) VALUES ($1,$2,'escrow_refund',$3,$4)",
        )
        .bind(project_id)
        .bind(viewer_id)
        .bind(refunded_quota)
        .bind(now)
        .execute(&mut *transaction)
        .await
        {
            tracing::error!(%error, viewer_id, project_id, "failed to record bounty escrow refund");
            return internal_failure();
        }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, project_id, "failed to commit bounty close");
        return internal_failure();
    }
    let project = match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => project,
        Ok(None) | Err(_) => return internal_failure(),
    };
    let remaining_quota = match sqlx::query_scalar::<_, i64>(
        "SELECT quota::BIGINT FROM users WHERE id=$1",
    )
    .bind(viewer_id)
    .fetch_one(&state.pg)
    .await
    {
        Ok(quota) => quota,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load remaining quota after bounty close");
            return internal_failure();
        }
    };
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: serde_json::json!({
            "project": project,
            "refunded_quota": refunded_quota,
            "remaining_quota": remaining_quota,
        }),
    })
    .into_response()
}

fn archive_status_is_final(status: &str) -> bool {
    matches!(status, "completed" | "closed")
}

async fn archive_bounty(
    state: State<OpenSourceBountyState>,
    path: Path<String>,
    headers: HeaderMap,
) -> Response {
    set_bounty_archived(state, path, headers, true).await
}

async fn unarchive_bounty(
    state: State<OpenSourceBountyState>,
    path: Path<String>,
    headers: HeaderMap,
) -> Response {
    set_bounty_archived(state, path, headers, false).await
}

async fn set_bounty_archived(
    State(state): State<OpenSourceBountyState>,
    Path(project_id): Path<String>,
    headers: HeaderMap,
    archived: bool,
) -> Response {
    let viewer_id = match required_developer_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match parse_positive_id(
        &project_id,
        "OPEN_SOURCE_BOUNTY_INVALID_ID",
        "invalid open-source bounty identifier",
    ) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to begin bounty archive change");
            return internal_failure();
        }
    };
    let row = match sqlx::query(RAW_OWNED_PROJECT_SELECT_FOR_UPDATE)
        .bind(project_id)
        .bind(viewer_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to lock bounty for archive change");
            return internal_failure();
        }
    };
    let project = match raw_project_from_row(&row) {
        Ok(project) => project,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to decode bounty for archive change");
            return internal_failure();
        }
    };
    if !archive_status_is_final(&project.status) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_ARCHIVE_UNAVAILABLE",
            "only completed or closed bounties can be archived",
        );
    }
    if (project.archived_at > 0) != archived {
        let now = chrono::Utc::now().timestamp();
        let archived_at = if archived { now } else { 0 };
        if let Err(error) = sqlx::query(
            "UPDATE open_source_bounty_projects SET archived_at=$1,updated_at=$2 WHERE id=$3",
        )
        .bind(archived_at)
        .bind(now)
        .bind(project_id)
        .execute(&mut *transaction)
        .await
        {
            tracing::error!(%error, viewer_id, project_id, "failed to update bounty archive state");
            return internal_failure();
        }
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, project_id, "failed to commit bounty archive change");
        return internal_failure();
    }

    let action = if archived { "Archived" } else { "Unarchived" };
    let content = format!("{action} open-source bounty {project_id}");
    if let Err(error) = sqlx::query(
        "INSERT INTO logs (user_id,created_at,type,content,username,request_id) SELECT id,$2,4,$3,username,$4 FROM users WHERE id=$1",
    )
    .bind(viewer_id)
    .bind(chrono::Utc::now().timestamp())
    .bind(content)
    .bind(uuid::Uuid::new_v4().to_string())
    .execute(&state.pg)
    .await
    {
        tracing::error!(%error, viewer_id, project_id, "failed to record bounty archive change");
    }

    match load_raw_project(&state, project_id).await {
        Ok(Some(project)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: project,
        })
        .into_response(),
        Ok(None) => internal_failure(),
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to reload bounty after archive change");
            internal_failure()
        }
    }
}

async fn accept_bounty(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match project_action_id(request.uri().path()) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let input = match read_json_input::<AcceptInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty request",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let github_handle = match normalize_github_handle(&input.github_handle) {
        Ok(handle) => handle,
        Err(response) => return *response,
    };
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to begin bounty acceptance");
            return internal_failure();
        }
    };
    let row = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load bounty for acceptance");
            return internal_failure();
        }
    };
    let project = match raw_project_from_row(&row) {
        Ok(project) => project,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to decode bounty for acceptance");
            return internal_failure();
        }
    };
    if project.status != "published" {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_NOT_ACCEPTING",
            "bounty is not accepting new challenges",
        );
    }
    if project.owner_user_id == viewer_id {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_OWNER_CANNOT_ACCEPT",
            "bounty owner cannot accept their own challenge",
        );
    }
    let previous = match sqlx::query(
        "SELECT id::BIGINT AS id, status, rejected_at::BIGINT AS rejected_at FROM open_source_bounty_challenges WHERE project_id=$1 AND participant_user_id=$2 ORDER BY id DESC",
    )
    .bind(project_id)
    .bind(viewer_id)
    .fetch_all(&mut *transaction)
    .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to inspect previous bounty challenges");
            return internal_failure();
        }
    };
    let appeal_cutoff = chrono::Utc::now().timestamp() - 7 * 24 * 60 * 60;
    for row in previous {
        let status: String = match row.try_get("status") {
            Ok(status) => status,
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to decode previous bounty challenge");
                return internal_failure();
            }
        };
        if matches!(status.as_str(), "accepted" | "submitted" | "approved") {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_ALREADY_ACCEPTED",
                "this bounty already has an active or completed attempt",
            );
        }
        if status == "rejected" {
            let rejected_at: i64 = row.try_get("rejected_at").unwrap_or_default();
            let open_disputes = sqlx::query_scalar::<_, i64>(
                "SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE challenge_id=$1 AND status='open'",
            )
            .bind(row.try_get::<i64, _>("id").unwrap_or_default())
            .fetch_one(&mut *transaction)
            .await;
            let denied_disputes = sqlx::query_scalar::<_, i64>(
                "SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE challenge_id=$1 AND status='resolved_denied'",
            )
            .bind(row.try_get::<i64, _>("id").unwrap_or_default())
            .fetch_one(&mut *transaction)
            .await;
            match (open_disputes, denied_disputes) {
                (Ok(open), Ok(denied))
                    if open == 0 && (denied > 0 || rejected_at <= appeal_cutoff) => {}
                (Ok(_), Ok(_)) => {
                    return business_failure(
                        "OPEN_SOURCE_BOUNTY_RETRY_PENDING",
                        "wait until the rejected attempt's dispute is resolved or its seven-day appeal window ends",
                    );
                }
                (Err(error), _) | (_, Err(error)) => {
                    tracing::error!(%error, viewer_id, project_id, "failed to inspect bounty challenge disputes");
                    return internal_failure();
                }
            }
        }
    }
    let occupied = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges c WHERE c.project_id=$1 AND (c.status IN ('accepted','submitted','approved') OR (c.status='rejected' AND c.rejected_at>$2 AND NOT EXISTS (SELECT 1 FROM open_source_bounty_disputes d WHERE d.challenge_id=c.id AND d.status IN ('resolved_paid','resolved_denied'))) OR EXISTS (SELECT 1 FROM open_source_bounty_disputes d WHERE d.challenge_id=c.id AND d.status='open'))",
    )
    .bind(project_id)
    .bind(appeal_cutoff)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(count) => count,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to count bounty reward slots");
            return internal_failure();
        }
    };
    if occupied >= project.reward_slots {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_FULL",
            "all reward slots are currently occupied",
        );
    }
    let reward_quota = if project.net_reward_quota > 0 {
        project.net_reward_quota
    } else {
        project.reward_quota
    };
    let now = chrono::Utc::now().timestamp();
    let challenge_id = match sqlx::query_scalar::<_, i64>(
        "INSERT INTO open_source_bounty_challenges (project_id,participant_user_id,github_handle,status,reward_quota,accepted_at,created_at,updated_at) VALUES ($1,$2,$3,'accepted',$4,$5,$5,$5) RETURNING id::BIGINT",
    )
    .bind(project_id)
    .bind(viewer_id)
    .bind(github_handle)
    .bind(reward_quota)
    .bind(now)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(id) => id,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to create bounty challenge");
            return internal_failure();
        }
    };
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, project_id, "failed to commit bounty acceptance");
        return internal_failure();
    }
    match load_challenge(&state, challenge_id).await {
        Ok(Some(challenge)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: challenge,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn submit_bounty(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let project_id = match project_action_id(request.uri().path()) {
        Ok(project_id) => project_id,
        Err(response) => return *response,
    };
    let input = match read_json_input::<SubmitInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty submission",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let submission_note = input.submission_note.trim().to_owned();
    if submission_note.len() > 2000 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_SUBMISSION",
            "completion note must contain at most 2000 characters",
        );
    }
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to begin bounty submission");
            return internal_failure();
        }
    };
    let project = match sqlx::query(RAW_PROJECT_SELECT)
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => match raw_project_from_row(&row) {
            Ok(project) => project,
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to decode bounty submission project");
                return internal_failure();
            }
        },
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load bounty submission project");
            return internal_failure();
        }
    };
    let challenge_row = match sqlx::query(&format!(
        "{} WHERE project_id=$1 AND participant_user_id=$2 AND status='accepted' ORDER BY id DESC LIMIT 1 FOR UPDATE",
        challenge_select()
    ))
    .bind(project_id)
    .bind(viewer_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(row)) => row,
        Ok(None) => return business_failure("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "accepted challenge was not found"),
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to load accepted bounty challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&challenge_row) {
        Ok(challenge) => challenge,
        Err(error) => {
            tracing::error!(%error, viewer_id, project_id, "failed to decode accepted bounty challenge");
            return internal_failure();
        }
    };
    let issue_url =
        match normalize_github_evidence(&input.issue_url, &project.repository_url, "issues") {
            Ok(url) => url,
            Err(response) => return *response,
        };
    let pull_request_url =
        match normalize_github_evidence(&input.pull_request_url, &project.repository_url, "pull") {
            Ok(url) => url,
            Err(response) => return *response,
        };
    if issue_url.is_empty() && pull_request_url.is_empty() {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_EVIDENCE_REQUIRED",
            "provide at least one GitHub Issue or pull request URL",
        );
    }
    if !pull_request_url.is_empty() {
        let duplicate = match sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges WHERE project_id=$1 AND id<>$2 AND pull_request_url=$3 AND status IN ('submitted','approved')",
        )
        .bind(project_id)
        .bind(challenge.id)
        .bind(&pull_request_url)
        .fetch_one(&mut *transaction)
        .await
        {
            Ok(count) => count,
            Err(error) => {
                tracing::error!(%error, viewer_id, project_id, "failed to check duplicate bounty pull request");
                return internal_failure();
            }
        };
        if duplicate > 0 {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_DUPLICATE_PULL_REQUEST",
                "this pull request has already been submitted",
            );
        }
    }
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_challenges SET issue_url=$1,pull_request_url=$2,submission_note=$3,status='submitted',submitted_at=$4,updated_at=$4 WHERE id=$5",
    )
    .bind(&issue_url)
    .bind(&pull_request_url)
    .bind(&submission_note)
    .bind(now)
    .bind(challenge.id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, project_id, "failed to update bounty submission");
        return internal_failure();
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, project_id, "failed to commit bounty submission");
        return internal_failure();
    }
    match load_challenge(&state, challenge.id).await {
        Ok(Some(challenge)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: challenge,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn withdraw_challenge(
    State(state): State<OpenSourceBountyState>,
    Path(challenge_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    change_challenge_state(state, challenge_id, headers, false).await
}

async fn cancel_challenge(
    State(state): State<OpenSourceBountyState>,
    Path(challenge_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    change_challenge_state(state, challenge_id, headers, true).await
}

async fn change_challenge_state(
    state: OpenSourceBountyState,
    challenge_id: String,
    headers: HeaderMap,
    owner_cancel: bool,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let challenge_id = match parse_positive_id(
        &challenge_id,
        "OPEN_SOURCE_BOUNTY_INVALID_ID",
        "invalid open-source bounty identifier",
    ) {
        Ok(challenge_id) => challenge_id,
        Err(response) => return *response,
    };
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to begin bounty challenge state change");
            return internal_failure();
        }
    };
    let reference = match sqlx::query("SELECT project_id::BIGINT AS project_id, participant_user_id::BIGINT AS participant_user_id FROM open_source_bounty_challenges WHERE id=$1")
        .bind(challenge_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => return business_failure("OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND", "challenge was not found"),
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty challenge reference");
            return internal_failure();
        }
    };
    let project_id: i64 = match reference.try_get("project_id") {
        Ok(value) => value,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty challenge reference");
            return internal_failure();
        }
    };
    if owner_cancel {
        let project_owner = match sqlx::query_scalar::<_, i64>(
            "SELECT owner_user_id::BIGINT FROM open_source_bounty_projects WHERE id=$1 AND owner_user_id=$2 FOR UPDATE",
        )
        .bind(project_id)
        .bind(viewer_id)
        .fetch_optional(&mut *transaction)
        .await
        {
            Ok(value) => value,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to authorize bounty challenge cancellation");
                return internal_failure();
            }
        };
        if project_owner.is_none() {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_FORBIDDEN",
                "only the bounty owner can cancel this challenge",
            );
        }
        let project_status = match sqlx::query_scalar::<_, String>(
            "SELECT status FROM open_source_bounty_projects WHERE id=$1",
        )
        .bind(project_id)
        .fetch_one(&mut *transaction)
        .await
        {
            Ok(status) => status,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty state for cancellation");
                return internal_failure();
            }
        };
        if !matches!(project_status.as_str(), "published" | "paused") {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_STATE",
                "only a published or paused bounty can cancel a challenge",
            );
        }
    } else {
        let participant: i64 = reference.try_get("participant_user_id").unwrap_or_default();
        if participant != viewer_id {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND",
                "challenge was not found",
            );
        }
    }
    let challenge_row = match sqlx::query(&format!("{} WHERE id=$1 FOR UPDATE", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&challenge_row) {
        Ok(challenge) => challenge,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty challenge");
            return internal_failure();
        }
    };
    if owner_cancel {
        if challenge.status != "accepted" {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
                "only an unsubmitted challenge can be cancelled",
            );
        }
    } else if !matches!(challenge.status.as_str(), "accepted" | "submitted") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
            "challenge cannot be withdrawn",
        );
    }
    let open_disputes = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE challenge_id=$1 AND status='open'",
    )
    .bind(challenge_id)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(count) => count,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to inspect bounty challenge disputes");
            return internal_failure();
        }
    };
    if open_disputes > 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_OPEN_DISPUTES",
            if owner_cancel {
                "a challenge with an open dispute cannot be cancelled"
            } else {
                "a challenge with an open dispute cannot be withdrawn"
            },
        );
    }
    let target = if owner_cancel {
        "cancelled"
    } else {
        "withdrawn"
    };
    if let Err(error) =
        sqlx::query("UPDATE open_source_bounty_challenges SET status=$1,updated_at=$2 WHERE id=$3")
            .bind(target)
            .bind(chrono::Utc::now().timestamp())
            .bind(challenge_id)
            .execute(&mut *transaction)
            .await
    {
        tracing::error!(%error, viewer_id, challenge_id, "failed to change bounty challenge state");
        return internal_failure();
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, challenge_id, "failed to commit bounty challenge state change");
        return internal_failure();
    }
    match load_challenge(&state, challenge_id).await {
        Ok(Some(challenge)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: challenge,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn approve_challenge(
    State(state): State<OpenSourceBountyState>,
    request: Request,
) -> Response {
    review_challenge(state, request, true).await
}

async fn reject_challenge(
    State(state): State<OpenSourceBountyState>,
    request: Request,
) -> Response {
    review_challenge(state, request, false).await
}

async fn review_challenge(
    state: OpenSourceBountyState,
    request: Request,
    approve: bool,
) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let challenge_id = match request
        .uri()
        .path()
        .rsplit('/')
        .nth(1)
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
    {
        Some(challenge_id) => challenge_id,
        None => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let input = match read_json_input::<ReviewInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty review",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let review_note = input.review_note.trim().to_owned();
    if review_note.len() > 2000 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_REVIEW",
            "review note is too long",
        );
    }
    let rating_comment = input.rating_comment.trim().to_owned();
    if !(1..=5).contains(&input.rating_score) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_RATING",
            "rating score must be between 1 and 5",
        );
    }
    if !(2..=1000).contains(&rating_comment.len()) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_RATING",
            "rating comment must contain 2 to 1000 characters",
        );
    }
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to begin bounty review");
            return internal_failure();
        }
    };
    let project_id = match sqlx::query_scalar::<_, i64>(
        "SELECT project_id::BIGINT FROM open_source_bounty_challenges WHERE id=$1",
    )
    .bind(challenge_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(project_id)) => project_id,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND",
                "challenge submission was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty review challenge");
            return internal_failure();
        }
    };
    let project = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => match raw_project_from_row(&row) {
            Ok(project) if project.owner_user_id == viewer_id => project,
            Ok(_) => {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_FORBIDDEN",
                    "only the bounty owner can review this submission",
                );
            }
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty review project");
                return internal_failure();
            }
        },
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_FORBIDDEN",
                "only the bounty owner can review this submission",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty review project");
            return internal_failure();
        }
    };
    let challenge_row = match sqlx::query(&format!("{} WHERE id=$1 FOR UPDATE", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty review challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&challenge_row) {
        Ok(challenge) => challenge,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty review challenge");
            return internal_failure();
        }
    };
    if challenge.project_id != project.id {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH",
            "challenge project changed while the submission was reviewed",
        );
    }
    if challenge.status != "submitted" {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
            "only a submitted challenge can be reviewed",
        );
    }
    let now = chrono::Utc::now().timestamp();
    let mut transferred_quota = 0_i64;
    if approve {
        if !matches!(project.status.as_str(), "published" | "paused") {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_STATE",
                "bounty is not in a payable state",
            );
        }
        if challenge.reward_quota <= 0 || project.escrow_quota < challenge.reward_quota {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_ESCROW_INSUFFICIENT",
                "bounty escrow is insufficient",
            );
        }
        let credit = match sqlx::query(
            "UPDATE users SET quota=quota+$1 WHERE id=$2 AND deleted_at IS NULL",
        )
        .bind(challenge.reward_quota)
        .bind(challenge.participant_user_id)
        .execute(&mut *transaction)
        .await
        {
            Ok(result) => result,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to credit bounty participant");
                return internal_failure();
            }
        };
        if credit.rows_affected() != 1 {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND",
                "challenge participant was not found",
            );
        }
        transferred_quota = challenge.reward_quota;
        let remaining_escrow = project.escrow_quota - transferred_quota;
        if remaining_escrow == 0 {
            if let Err(error) = sqlx::query("UPDATE open_source_bounty_projects SET escrow_quota=0,status='completed',closed_at=$1,updated_at=$1 WHERE id=$2")
                .bind(now)
                .bind(project.id)
                .execute(&mut *transaction)
                .await
            {
                tracing::error!(%error, viewer_id, challenge_id, "failed to complete bounty project after payout");
                return internal_failure();
            }
        } else if let Err(error) = sqlx::query(
            "UPDATE open_source_bounty_projects SET escrow_quota=$1,updated_at=$2 WHERE id=$3",
        )
        .bind(remaining_escrow)
        .bind(now)
        .bind(project.id)
        .execute(&mut *transaction)
        .await
        {
            tracing::error!(%error, viewer_id, challenge_id, "failed to update bounty escrow after payout");
            return internal_failure();
        }
    }
    let target = if approve { "approved" } else { "rejected" };
    let rejected_at = if approve { 0 } else { now };
    let paid_at = if approve { now } else { 0 };
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_challenges SET status=$1,review_note=$2,owner_rating_score=$3,owner_rating_comment=$4,owner_rated_at=$5,reviewed_at=$5,rejected_at=$6,paid_at=$7,updated_at=$5 WHERE id=$8",
    )
    .bind(target)
    .bind(&review_note)
    .bind(input.rating_score)
    .bind(&rating_comment)
    .bind(now)
    .bind(rejected_at)
    .bind(paid_at)
    .bind(challenge_id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, challenge_id, "failed to update bounty review");
        return internal_failure();
    }
    if approve {
        if let Err(error) = sqlx::query("UPDATE open_source_bounty_disputes SET status='resolved_paid',resolution='The publisher approved and paid the reward after the dispute was opened.',resolved_by_user_id=$1,resolved_at=$2,updated_at=$2,open_key=NULL WHERE challenge_id=$3 AND status='open'")
            .bind(viewer_id)
            .bind(now)
            .bind(challenge_id)
            .execute(&mut *transaction)
            .await
        {
            tracing::error!(%error, viewer_id, challenge_id, "failed to resolve open bounty dispute after approval");
            return internal_failure();
        }
        let payout_key = format!("challenge:{challenge_id}");
        if let Err(error) = sqlx::query("INSERT INTO open_source_bounty_ledgers (project_id,challenge_id,user_id,counterparty_user_id,kind,quota,reward_payout_key,created_at) VALUES ($1,$2,$3,$4,'reward_transfer',$5,$6,$7)")
            .bind(project.id)
            .bind(challenge_id)
            .bind(viewer_id)
            .bind(challenge.participant_user_id)
            .bind(transferred_quota)
            .bind(payout_key)
            .bind(now)
            .execute(&mut *transaction)
            .await
        {
            tracing::error!(%error, viewer_id, challenge_id, "failed to record bounty reward payout");
            return internal_failure();
        }
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, challenge_id, "failed to commit bounty review");
        return internal_failure();
    }
    let challenge = match load_challenge(&state, challenge_id).await {
        Ok(Some(challenge)) => challenge,
        Ok(None) | Err(_) => return internal_failure(),
    };
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: serde_json::json!({"challenge": challenge, "transferred_quota": transferred_quota}),
    })
    .into_response()
}

async fn rate_owner(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let challenge_id = match request
        .uri()
        .path()
        .rsplit('/')
        .nth(1)
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
    {
        Some(id) => id,
        None => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let input = match read_json_input::<RatingInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty rating",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let comment = input.comment.trim().to_owned();
    if !(1..=5).contains(&input.score) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_RATING",
            "rating score must be between 1 and 5",
        );
    }
    if !(2..=1000).contains(&comment.len()) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_RATING",
            "rating comment must contain 2 to 1000 characters",
        );
    }
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to begin bounty owner rating");
            return internal_failure();
        }
    };
    let row = match sqlx::query(&format!(
        "{} WHERE id=$1 AND participant_user_id=$2 FOR UPDATE",
        challenge_select()
    ))
    .bind(challenge_id)
    .bind(viewer_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND",
                "challenge was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty owner rating challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&row) {
        Ok(challenge) => challenge,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty owner rating challenge");
            return internal_failure();
        }
    };
    if !matches!(challenge.status.as_str(), "approved" | "rejected") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
            "the bounty owner can only be rated after review",
        );
    }
    if challenge.contributor_rated_at > 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_RATING_EXISTS",
            "the publisher rating for this challenge has already been submitted",
        );
    }
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query("UPDATE open_source_bounty_challenges SET contributor_rating_score=$1,contributor_rating_comment=$2,contributor_rated_at=$3,updated_at=$3 WHERE id=$4")
        .bind(input.score)
        .bind(comment)
        .bind(now)
        .bind(challenge_id)
        .execute(&mut *transaction)
        .await
    {
        tracing::error!(%error, viewer_id, challenge_id, "failed to save bounty owner rating");
        return internal_failure();
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, challenge_id, "failed to commit bounty owner rating");
        return internal_failure();
    }
    match load_challenge(&state, challenge_id).await {
        Ok(Some(challenge)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: challenge,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

fn valid_idempotency_key(value: &str) -> bool {
    let bytes = value.as_bytes();
    (8..=128).contains(&bytes.len())
        && bytes[0].is_ascii_alphanumeric()
        && bytes
            .iter()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b':' | b'-'))
}

fn tip_operation_key_hash(value: &str) -> String {
    hex::encode(Sha256::digest(value.as_bytes()))
}

fn tip_operation_payload_hash(challenge_id: i64, quota: i64, note: &str) -> String {
    // Keep the JSON field order and trimmed note identical to Go's
    // OpenSourceBountyMCPPayloadHash input. This is the replay identity, not
    // a transport signature, so it must remain stable across listeners.
    let payload = serde_json::json!({
        "challenge_id": challenge_id,
        "quota": quota,
        "note": note,
    });
    let encoded = serde_json::to_vec(&payload).expect("tip payload is serializable");
    hex::encode(Sha256::digest(encoded))
}

async fn replay_tip_operation(
    state: &OpenSourceBountyState,
    viewer_id: i64,
    key_hash: &str,
    payload_hash: &str,
) -> Result<Option<Response>, Response> {
    let row = sqlx::query(
        "SELECT payload_hash, result_json, completed_at::BIGINT AS completed_at FROM open_source_bounty_rest_operations WHERE user_id=$1 AND operation='tip' AND idempotency_key_hash=$2",
    )
    .bind(viewer_id)
    .bind(key_hash)
    .fetch_optional(&state.pg)
    .await
    .map_err(|error| {
        tracing::error!(%error, viewer_id, "failed to load bounty tip idempotency record");
        internal_failure()
    })?;
    let Some(row) = row else {
        return Ok(None);
    };
    let stored_payload: String = row.try_get("payload_hash").unwrap_or_default();
    if stored_payload != payload_hash {
        return Err(business_failure(
            "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_MISMATCH",
            "Idempotency-Key was already used for a different bounty tip",
        ));
    }
    let completed_at: i64 = row.try_get("completed_at").unwrap_or_default();
    let result_json: String = row.try_get("result_json").unwrap_or_default();
    if completed_at <= 0 || result_json.is_empty() {
        return Err(business_failure(
            "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_IN_PROGRESS",
            "the bounty tip is still being committed; retry with the same Idempotency-Key",
        ));
    }
    let data = serde_json::from_str::<Value>(&result_json).map_err(|error| {
        tracing::error!(%error, viewer_id, "failed to decode bounty tip idempotency result");
        internal_failure()
    })?;
    Ok(Some(
        Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data,
        })
        .into_response(),
    ))
}

async fn tip_challenge(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let challenge_id = match request
        .uri()
        .path()
        .rsplit('/')
        .nth(1)
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
    {
        Some(id) => id,
        None => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let idempotency_key = headers
        .get("idempotency-key")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default();
    if !valid_idempotency_key(idempotency_key) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_IDEMPOTENCY_KEY",
            "Idempotency-Key must contain 8 to 128 supported characters",
        );
    }
    let input = match read_json_input::<TipInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty tip",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let note = input.note.trim().to_owned();
    if input.quota <= 0 || input.quota > 1_000_000_000 || note.len() > 500 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_TIP",
            if note.len() > 500 {
                "tip note is too long"
            } else {
                "tip quota must be a positive supported amount"
            },
        );
    }
    let idempotency_key_hash = tip_operation_key_hash(idempotency_key);
    let payload_hash = tip_operation_payload_hash(challenge_id, input.quota, &note);
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to begin bounty tip");
            return internal_failure();
        }
    };
    let existing = match sqlx::query(
        "SELECT payload_hash, result_json, completed_at::BIGINT AS completed_at FROM open_source_bounty_rest_operations WHERE user_id=$1 AND operation='tip' AND idempotency_key_hash=$2 FOR UPDATE",
    )
    .bind(viewer_id)
    .bind(&idempotency_key_hash)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty tip idempotency record");
            return internal_failure();
        }
    };
    if let Some(row) = existing {
        let stored_payload: String = row.try_get("payload_hash").unwrap_or_default();
        if stored_payload != payload_hash {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_MISMATCH",
                "Idempotency-Key was already used for a different bounty tip",
            );
        }
        let completed_at: i64 = row.try_get("completed_at").unwrap_or_default();
        let result_json: String = row.try_get("result_json").unwrap_or_default();
        if completed_at <= 0 || result_json.is_empty() {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_IDEMPOTENCY_IN_PROGRESS",
                "the bounty tip is still being committed; retry with the same Idempotency-Key",
            );
        }
        let data = match serde_json::from_str::<Value>(&result_json) {
            Ok(data) => data,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty tip idempotency result");
                return internal_failure();
            }
        };
        if transaction.commit().await.is_err() {
            return internal_failure();
        }
        return Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data,
        })
        .into_response();
    }
    let operation_id = match sqlx::query_scalar::<_, i64>(
        "INSERT INTO open_source_bounty_rest_operations (user_id,operation,idempotency_key_hash,payload_hash,created_at,completed_at,result_json) VALUES ($1,'tip',$2,$3,$4,0,'') RETURNING id::BIGINT",
    )
    .bind(viewer_id)
    .bind(&idempotency_key_hash)
    .bind(&payload_hash)
    .bind(chrono::Utc::now().timestamp())
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(id) => id,
        Err(error)
            if error
                .as_database_error()
                .and_then(|database| database.code())
                .as_deref()
                == Some("23505") =>
        {
            let _ = transaction.rollback().await;
            return match replay_tip_operation(
                &state,
                viewer_id,
                &idempotency_key_hash,
                &payload_hash,
            )
            .await
            {
                Ok(Some(response)) => response,
                Ok(None) => internal_failure(),
                Err(response) => response,
            };
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to reserve bounty tip idempotency record");
            return internal_failure();
        }
    };
    let project_id = match sqlx::query_scalar::<_, i64>(
        "SELECT project_id::BIGINT FROM open_source_bounty_challenges WHERE id=$1",
    )
    .bind(challenge_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(id)) => id,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND",
                "challenge was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty tip challenge");
            return internal_failure();
        }
    };
    let owner = match sqlx::query_scalar::<_, i64>("SELECT owner_user_id::BIGINT FROM open_source_bounty_projects WHERE id=$1 AND owner_user_id=$2 FOR UPDATE")
        .bind(project_id)
        .bind(viewer_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(owner)) => owner,
        Ok(None) => return business_failure("OPEN_SOURCE_BOUNTY_FORBIDDEN", "only the bounty owner can tip this contributor"),
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to authorize bounty tip");
            return internal_failure();
        }
    };
    let challenge_row = match sqlx::query(&format!("{} WHERE id=$1 FOR UPDATE", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty tip challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&challenge_row) {
        Ok(challenge) => challenge,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty tip challenge");
            return internal_failure();
        }
    };
    if challenge.participant_user_id == owner {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_SELF_TIP",
            "bounty owners cannot tip themselves",
        );
    }
    if matches!(challenge.status.as_str(), "withdrawn" | "cancelled") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
            "inactive challenges cannot receive tips",
        );
    }
    let debit = match sqlx::query(
        "UPDATE users SET quota=quota-$1 WHERE id=$2 AND deleted_at IS NULL AND quota >= $1",
    )
    .bind(input.quota)
    .bind(owner)
    .execute(&mut *transaction)
    .await
    {
        Ok(result) => result,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to debit bounty tip");
            return internal_failure();
        }
    };
    if debit.rows_affected() != 1 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INSUFFICIENT_BALANCE",
            "insufficient balance to send this tip",
        );
    }
    let credit =
        match sqlx::query("UPDATE users SET quota=quota+$1 WHERE id=$2 AND deleted_at IS NULL")
            .bind(input.quota)
            .bind(challenge.participant_user_id)
            .execute(&mut *transaction)
            .await
        {
            Ok(result) => result,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to credit bounty tip");
                return internal_failure();
            }
        };
    if credit.rows_affected() != 1 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND",
            "challenge participant was not found",
        );
    }
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_challenges SET tip_quota=tip_quota+$1,updated_at=$2 WHERE id=$3",
    )
    .bind(input.quota)
    .bind(now)
    .bind(challenge_id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(%error, viewer_id, challenge_id, "failed to update bounty tip total");
        return internal_failure();
    }
    if let Err(error) = sqlx::query("INSERT INTO open_source_bounty_ledgers (project_id,challenge_id,user_id,counterparty_user_id,kind,quota,note,created_at) VALUES ($1,$2,$3,$4,'tip_transfer',$5,$6,$7)")
        .bind(project_id)
        .bind(challenge_id)
        .bind(owner)
        .bind(challenge.participant_user_id)
        .bind(input.quota)
        .bind(&note)
        .bind(now)
        .execute(&mut *transaction)
        .await
    {
        tracing::error!(%error, viewer_id, challenge_id, "failed to record bounty tip");
        return internal_failure();
    }
    let challenge = match sqlx::query(&format!("{} WHERE id=$1", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => match challenge_from_row(&row) {
            Ok(challenge) => challenge,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to decode committed bounty tip challenge");
                return internal_failure();
            }
        },
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to reload committed bounty tip challenge");
            return internal_failure();
        }
    };
    let remaining_quota = match sqlx::query_scalar::<_, i64>(
        "SELECT quota::BIGINT FROM users WHERE id=$1",
    )
    .bind(owner)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(quota) => quota,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load remaining bounty tip quota");
            return internal_failure();
        }
    };
    let result_data = serde_json::json!({
        "challenge": challenge,
        "transferred_quota": input.quota,
        "remaining_quota": remaining_quota,
    });
    let result_json = match serde_json::to_string(&result_data) {
        Ok(result) => result,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to encode bounty tip idempotency result");
            return internal_failure();
        }
    };
    let completed_at = chrono::Utc::now().timestamp();
    match sqlx::query(
        "UPDATE open_source_bounty_rest_operations SET result_json=$1,completed_at=$2 WHERE id=$3 AND completed_at=0",
    )
    .bind(result_json)
    .bind(completed_at)
    .bind(operation_id)
    .execute(&mut *transaction)
    .await
    {
        Ok(result) if result.rows_affected() == 1 => {}
        Ok(_) => return internal_failure(),
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to complete bounty tip idempotency record");
            return internal_failure();
        }
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, challenge_id, "failed to commit bounty tip");
        return internal_failure();
    }
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: result_data,
    })
    .into_response()
}

fn valid_dispute_reason(value: &str) -> bool {
    matches!(
        value,
        "merged_but_unpaid"
            | "requirements_met_but_rejected"
            | "misleading_requirements"
            | "abusive_conduct"
            | "other"
    )
}

async fn open_dispute(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let challenge_id = match request
        .uri()
        .path()
        .rsplit('/')
        .nth(1)
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
    {
        Some(id) => id,
        None => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let input = match read_json_input::<DisputeInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty dispute",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let reason = input.reason.trim().to_owned();
    let statement = input.statement.trim().to_owned();
    if !valid_dispute_reason(&reason) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_DISPUTE",
            "invalid bounty dispute reason",
        );
    }
    if !(20..=5000).contains(&statement.len()) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_DISPUTE",
            "dispute statement must contain 20 to 5000 characters",
        );
    }
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to begin bounty dispute");
            return internal_failure();
        }
    };
    let challenge_ref = match sqlx::query(
        "SELECT project_id::BIGINT AS project_id FROM open_source_bounty_challenges WHERE id=$1",
    )
    .bind(challenge_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(Some(row)) => row,
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_CHALLENGE_NOT_FOUND",
                "challenge was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to load bounty dispute challenge");
            return internal_failure();
        }
    };
    let project_id: i64 = match challenge_ref.try_get("project_id") {
        Ok(id) => id,
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty dispute challenge");
            return internal_failure();
        }
    };
    let project = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => match raw_project_from_row(&row) {
            Ok(project) => project,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty dispute project");
                return internal_failure();
            }
        },
        Ok(None) => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_NOT_FOUND",
                "bounty project was not found",
            );
        }
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty dispute project");
            return internal_failure();
        }
    };
    let challenge = match sqlx::query(&format!("{} WHERE id=$1 FOR UPDATE", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => match challenge_from_row(&row) {
            Ok(challenge) => challenge,
            Err(error) => {
                tracing::error!(%error, viewer_id, challenge_id, "failed to decode bounty dispute challenge");
                return internal_failure();
            }
        },
        Err(error) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to lock bounty dispute challenge");
            return internal_failure();
        }
    };
    if challenge.project_id != project.id {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH",
            "challenge project changed while the dispute was opened",
        );
    }
    if matches!(project.status.as_str(), "draft" | "closed") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_STATE",
            "closed or unpublished bounty escrow cannot be disputed",
        );
    }
    if viewer_id != challenge.participant_user_id && viewer_id != project.owner_user_id {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_FORBIDDEN",
            "only a bounty party can open a dispute",
        );
    }
    if matches!(challenge.status.as_str(), "withdrawn" | "cancelled") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
            "inactive challenges cannot be disputed",
        );
    }
    if challenge.status == "rejected"
        && challenge.rejected_at <= chrono::Utc::now().timestamp() - 7 * 24 * 60 * 60
    {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_DISPUTE_WINDOW_EXPIRED",
            "the seven-day dispute window for this rejected challenge has expired",
        );
    }
    let open_exists = sqlx::query_scalar::<_, i64>("SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE challenge_id=$1 AND status='open'")
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await;
    let prior_exists = sqlx::query_scalar::<_, i64>("SELECT COUNT(*)::BIGINT FROM open_source_bounty_disputes WHERE challenge_id=$1 AND opened_by_user_id=$2")
        .bind(challenge_id)
        .bind(viewer_id)
        .fetch_one(&mut *transaction)
        .await;
    match (open_exists, prior_exists) {
        (Ok(open), Ok(prior)) if open > 0 || prior > 0 => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS",
                if prior > 0 {
                    "this party has already opened the final dispute case for this challenge"
                } else {
                    "an open dispute already exists for this challenge"
                },
            );
        }
        (Ok(_), Ok(_)) => {}
        (Err(error), _) | (_, Err(error)) => {
            tracing::error!(%error, viewer_id, challenge_id, "failed to inspect existing bounty disputes");
            return internal_failure();
        }
    }
    let against = if viewer_id == project.owner_user_id {
        challenge.participant_user_id
    } else {
        project.owner_user_id
    };
    let now = chrono::Utc::now().timestamp();
    let case_key = format!("challenge:{challenge_id}:user:{viewer_id}");
    let open_key = format!("challenge:{challenge_id}");
    let dispute_id = match sqlx::query_scalar::<_, i64>(
        "INSERT INTO open_source_bounty_disputes (challenge_id,project_id,opened_by_user_id,against_user_id,case_key,open_key,reason,statement,project_title_snapshot,repository_url_snapshot,project_rules_snapshot,project_escrow_quota_snapshot,challenge_status_snapshot,issue_url_snapshot,pull_request_url_snapshot,submission_note_snapshot,review_note_snapshot,reward_quota_snapshot,tip_quota_snapshot,owner_rating_score_snapshot,owner_rating_comment_snapshot,contributor_rating_score_snapshot,contributor_rating_comment_snapshot,status,created_at,updated_at) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19,$20,$21,$22,$23,'open',$24,$24) RETURNING id::BIGINT",
    )
    .bind(challenge_id)
    .bind(project.id)
    .bind(viewer_id)
    .bind(against)
    .bind(case_key)
    .bind(open_key)
    .bind(reason)
    .bind(statement)
    .bind(&project.title)
    .bind(&project.repository_url)
    .bind(&project.rules)
    .bind(project.escrow_quota)
    .bind(&challenge.status)
    .bind(&challenge.issue_url)
    .bind(&challenge.pull_request_url)
    .bind(&challenge.submission_note)
    .bind(&challenge.review_note)
    .bind(challenge.reward_quota)
    .bind(challenge.tip_quota)
    .bind(challenge.owner_rating_score)
    .bind(&challenge.owner_rating_comment)
    .bind(challenge.contributor_rating_score)
    .bind(&challenge.contributor_rating_comment)
    .bind(now)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(id) => id,
        Err(error) => {
            if error.as_database_error().is_some_and(|db| db.code().as_deref() == Some("23505")) {
                return business_failure("OPEN_SOURCE_BOUNTY_DISPUTE_EXISTS", "an open dispute already exists for this challenge");
            }
            tracing::error!(%error, viewer_id, challenge_id, "failed to create bounty dispute");
            return internal_failure();
        }
    };
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, viewer_id, challenge_id, "failed to commit bounty dispute");
        return internal_failure();
    }
    match load_dispute_view(&state, dispute_id, Some(viewer_id), false).await {
        Ok(Some(view)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: view,
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

async fn resolve_dispute(State(state): State<OpenSourceBountyState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let admin_id = match required_admin_id(&state, &headers).await {
        Ok(admin_id) => admin_id,
        Err(response) => return response,
    };
    let dispute_id = match request
        .uri()
        .path()
        .rsplit('/')
        .nth(1)
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
    {
        Some(id) => id,
        None => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let input = match read_json_input::<ResolutionInput>(
        request,
        "OPEN_SOURCE_BOUNTY_INVALID_REQUEST",
        "invalid bounty dispute resolution",
    )
    .await
    {
        Ok(input) => input,
        Err(response) => return response,
    };
    let action = input.action.trim().to_owned();
    let resolution = input.resolution.trim().to_owned();
    if !matches!(action.as_str(), "pay" | "deny") {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_RESOLUTION",
            "dispute resolution action must be pay or deny",
        );
    }
    if !(10..=5000).contains(&resolution.len()) {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_DISPUTE_RESOLUTION",
            "resolution must contain 10 to 5000 characters",
        );
    }
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(%error, admin_id, dispute_id, "failed to begin bounty dispute resolution");
            return internal_failure();
        }
    };
    let admin_is_valid = match sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS(SELECT 1 FROM users WHERE id=$1 AND deleted_at IS NULL AND status=$2 AND role >= $3)",
    )
    .bind(admin_id)
    .bind(ENABLED_USER_STATUS)
    .bind(ROLE_ADMIN)
    .fetch_one(&mut *transaction)
    .await
    {
        Ok(is_valid) => is_valid,
        Err(error) => {
            tracing::error!(%error, admin_id, dispute_id, "failed to revalidate bounty dispute administrator");
            return internal_failure();
        }
    };
    if !admin_is_valid {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_FORBIDDEN",
            "administrator access is required",
        );
    }
    let reference = match sqlx::query("SELECT project_id::BIGINT AS project_id,challenge_id::BIGINT AS challenge_id FROM open_source_bounty_disputes WHERE id=$1")
        .bind(dispute_id)
        .fetch_optional(&mut *transaction)
        .await
    {
        Ok(Some(row)) => row,
        Ok(None) => return business_failure("OPEN_SOURCE_BOUNTY_DISPUTE_NOT_FOUND", "bounty dispute was not found"),
        Err(error) => {
            tracing::error!(%error, admin_id, dispute_id, "failed to load bounty dispute reference");
            return internal_failure();
        }
    };
    let project_id: i64 = reference.try_get("project_id").unwrap_or_default();
    let challenge_id: i64 = reference.try_get("challenge_id").unwrap_or_default();
    let project = match sqlx::query(&format!("{RAW_PROJECT_SELECT} FOR UPDATE"))
        .bind(project_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => match raw_project_from_row(&row) {
            Ok(project) => project,
            Err(_) => return internal_failure(),
        },
        Err(error) => {
            tracing::error!(%error, admin_id, dispute_id, "failed to lock bounty dispute project");
            return internal_failure();
        }
    };
    let challenge_row = match sqlx::query(&format!("{} WHERE id=$1 FOR UPDATE", challenge_select()))
        .bind(challenge_id)
        .fetch_one(&mut *transaction)
        .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(%error, admin_id, dispute_id, "failed to lock bounty dispute challenge");
            return internal_failure();
        }
    };
    let challenge = match challenge_from_row(&challenge_row) {
        Ok(challenge) => challenge,
        Err(_) => return internal_failure(),
    };
    let dispute_row =
        match sqlx::query("SELECT * FROM open_source_bounty_disputes WHERE id=$1 FOR UPDATE")
            .bind(dispute_id)
            .fetch_one(&mut *transaction)
            .await
        {
            Ok(row) => row,
            Err(error) => {
                tracing::error!(%error, admin_id, dispute_id, "failed to lock bounty dispute");
                return internal_failure();
            }
        };
    let dispute_status: String = dispute_row.try_get("status").unwrap_or_default();
    if dispute_status != "open" {
        let already = (action == "pay" && dispute_status == "resolved_paid")
            || (action == "deny" && dispute_status == "resolved_denied");
        if !already {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_DISPUTE_RESOLVED",
                "bounty dispute is already resolved with a different action",
            );
        }
    }
    if challenge.project_id != project.id
        || dispute_row
            .try_get::<i64, _>("project_id")
            .unwrap_or_default()
            != project.id
        || dispute_row
            .try_get::<i64, _>("challenge_id")
            .unwrap_or_default()
            != challenge.id
        || challenge.participant_user_id <= 0
        || project.owner_user_id <= 0
        || challenge.participant_user_id == project.owner_user_id
    {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_DISPUTE_IDENTITY_MISMATCH",
            "dispute parties do not match the bounty challenge and project",
        );
    }
    if admin_id == project.owner_user_id || admin_id == challenge.participant_user_id {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_DISPUTE_CONFLICT",
            "a bounty party cannot adjudicate their own dispute",
        );
    }
    let mut transferred_quota = 0_i64;
    if dispute_status == "open" {
        let now = chrono::Utc::now().timestamp();
        let target_status = if action == "pay" {
            "resolved_paid"
        } else {
            "resolved_denied"
        };
        if action == "pay" {
            let opened_by: i64 = dispute_row.try_get("opened_by_user_id").unwrap_or_default();
            let against: i64 = dispute_row.try_get("against_user_id").unwrap_or_default();
            if opened_by != challenge.participant_user_id || against != project.owner_user_id {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_DISPUTE_NOT_PAYABLE",
                    "only a contributor claim against the bounty owner can receive an enforced escrow payment",
                );
            }
            if !matches!(challenge.status.as_str(), "submitted" | "rejected") {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
                    "an enforced payout requires a submitted or rejected challenge",
                );
            }
            if challenge.issue_url.is_empty() || challenge.pull_request_url.is_empty() {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_INVALID_CHALLENGE_STATE",
                    "a dispute payout requires submitted Issue and pull request evidence",
                );
            }
            let reward_snapshot: i64 = dispute_row
                .try_get("reward_quota_snapshot")
                .unwrap_or_default();
            if reward_snapshot <= 0
                || challenge.reward_quota != reward_snapshot
                || project.escrow_quota < reward_snapshot
            {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_ESCROW_INSUFFICIENT",
                    "bounty escrow is insufficient",
                );
            }
            let credit = match sqlx::query(
                "UPDATE users SET quota=quota+$1 WHERE id=$2 AND deleted_at IS NULL",
            )
            .bind(reward_snapshot)
            .bind(challenge.participant_user_id)
            .execute(&mut *transaction)
            .await
            {
                Ok(result) => result,
                Err(error) => {
                    tracing::error!(%error, admin_id, dispute_id, "failed to credit enforced bounty payout");
                    return internal_failure();
                }
            };
            if credit.rows_affected() != 1 {
                return business_failure(
                    "OPEN_SOURCE_BOUNTY_PARTICIPANT_NOT_FOUND",
                    "challenge participant was not found",
                );
            }
            transferred_quota = reward_snapshot;
            let remaining = project.escrow_quota - transferred_quota;
            if let Err(error) = if remaining == 0 {
                sqlx::query("UPDATE open_source_bounty_projects SET escrow_quota=0,status='completed',closed_at=$1,updated_at=$1 WHERE id=$2").bind(now).bind(project.id).execute(&mut *transaction).await
            } else {
                sqlx::query("UPDATE open_source_bounty_projects SET escrow_quota=$1,updated_at=$2 WHERE id=$3").bind(remaining).bind(now).bind(project.id).execute(&mut *transaction).await
            } {
                tracing::error!(%error, admin_id, dispute_id, "failed to update enforced bounty escrow");
                return internal_failure();
            }
            if let Err(error) = sqlx::query("UPDATE open_source_bounty_challenges SET status='approved',owner_rating_overturned=(status='rejected' AND owner_rating_score>0),paid_at=$1,updated_at=$1 WHERE id=$2").bind(now).bind(challenge.id).execute(&mut *transaction).await { tracing::error!(%error, admin_id, dispute_id, "failed to update enforced bounty challenge"); return internal_failure(); }
            let payout_key = format!("challenge:{}", challenge.id);
            if let Err(error) = sqlx::query("INSERT INTO open_source_bounty_ledgers (project_id,challenge_id,user_id,counterparty_user_id,kind,quota,note,reward_payout_key,created_at) VALUES ($1,$2,$3,$4,'dispute_reward_transfer',$5,$6,$7,$8)").bind(project.id).bind(challenge.id).bind(project.owner_user_id).bind(challenge.participant_user_id).bind(transferred_quota).bind(&resolution).bind(payout_key).bind(now).execute(&mut *transaction).await { tracing::error!(%error, admin_id, dispute_id, "failed to record enforced bounty payout"); return internal_failure(); }
        }
        if let Err(error) = sqlx::query("UPDATE open_source_bounty_disputes SET status=$1,resolution=$2,resolved_by_user_id=$3,resolved_at=$4,updated_at=$4,open_key=NULL WHERE id=$5").bind(target_status).bind(&resolution).bind(admin_id).bind(now).bind(dispute_id).execute(&mut *transaction).await { tracing::error!(%error, admin_id, dispute_id, "failed to resolve bounty dispute"); return internal_failure(); }
    }
    if let Err(error) = transaction.commit().await {
        tracing::error!(%error, admin_id, dispute_id, "failed to commit bounty dispute resolution");
        return internal_failure();
    }
    match load_dispute_view(&state, dispute_id, None, true).await {
        Ok(Some(view)) => Json(LegacySuccessEnvelope {
            success: true,
            message: "",
            data: serde_json::json!({
                "dispute": view,
                "transferred_quota": transferred_quota,
            }),
        })
        .into_response(),
        Ok(None) | Err(_) => internal_failure(),
    }
}

#[derive(Debug, Serialize)]
struct BountyProjectView {
    id: i64,
    owner_user_id: i64,
    repository_url: String,
    title: String,
    description: String,
    rules: String,
    reward_quota: i64,
    net_reward_quota: i64,
    reward_slots: i64,
    escrow_quota: i64,
    platform_fee_rate_bps: i64,
    platform_fee_quota: i64,
    status: String,
    created_at: i64,
    updated_at: i64,
    published_at: i64,
    closed_at: i64,
    archived_at: i64,
    owner_username: String,
    active_challenge_count: i64,
    approved_challenge_count: i64,
    #[serde(serialize_with = "serialize_rating_average")]
    owner_rating_average: f64,
    owner_rating_count: i64,
    owner_thank_heart_count: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    viewer_challenge: Option<BountyChallenge>,
}

#[derive(Debug, Serialize)]
struct BountyChallenge {
    id: i64,
    project_id: i64,
    participant_user_id: i64,
    github_handle: String,
    status: String,
    issue_url: String,
    pull_request_url: String,
    submission_note: String,
    review_note: String,
    reward_quota: i64,
    tip_quota: i64,
    owner_rating_score: i64,
    owner_rating_comment: String,
    owner_rated_at: i64,
    contributor_rating_score: i64,
    contributor_rating_comment: String,
    contributor_rated_at: i64,
    owner_rating_overturned: bool,
    accepted_at: i64,
    submitted_at: i64,
    reviewed_at: i64,
    rejected_at: i64,
    paid_at: i64,
    created_at: i64,
    updated_at: i64,
}

#[derive(Debug, Serialize)]
struct ListPayload {
    items: Vec<BountyProjectView>,
    total: i64,
    page: i64,
    page_size: i64,
}

#[derive(Debug, Serialize)]
struct BountyFeeConfig {
    #[serde(serialize_with = "serialize_rating_average")]
    rate_percent: f64,
    rate_basis_points: i64,
}

#[derive(Debug, Serialize)]
struct BountyMcpTokenStatus {
    configured: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    token_hint: Option<String>,
    #[serde(skip_serializing_if = "Option::is_none")]
    created_at: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    updated_at: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    last_used_at: Option<i64>,
}

#[derive(Debug, Serialize)]
struct BountyMcpTokenStatusPayload {
    status: BountyMcpTokenStatus,
    endpoint: &'static str,
    protocol_version: &'static str,
}

#[derive(Debug, Serialize)]
struct BountyMcpTokenStatusPayloadWithSecret {
    token: String,
    status: BountyMcpTokenStatus,
    endpoint: &'static str,
    protocol_version: &'static str,
}

#[derive(Debug, Serialize)]
struct BountyProjectDetail {
    project: BountyProjectView,
    challenges: Vec<BountyChallengeView>,
    ledger: Vec<BountyLedger>,
}

fn serialize_rating_average<S>(value: &f64, serializer: S) -> Result<S::Ok, S::Error>
where
    S: Serializer,
{
    // Go's encoding/json emits integral float64 values as `0`, `1`, etc.
    // Preserve fractional averages while matching that strict wire shape.
    if value.is_finite() && value.fract() == 0.0 {
        serializer.serialize_i64(*value as i64)
    } else {
        serializer.serialize_f64(*value)
    }
}

#[derive(Debug, Serialize)]
struct BountyChallengeView {
    #[serde(flatten)]
    challenge: BountyChallenge,
    participant_username: String,
    project_title: String,
    repository_url: String,
    owner_username: String,
    #[serde(serialize_with = "serialize_rating_average")]
    participant_rating_average: f64,
    participant_rating_count: i64,
    #[serde(serialize_with = "serialize_rating_average")]
    owner_rating_average: f64,
    owner_rating_count: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    dispute: Option<BountyDisputeView>,
}

#[derive(Debug, Serialize)]
struct BountyLedger {
    id: i64,
    project_id: i64,
    challenge_id: i64,
    user_id: i64,
    counterparty_user_id: i64,
    kind: String,
    quota: i64,
    note: String,
    recipient_read_at: i64,
    thanked_at: i64,
    created_at: i64,
}

#[derive(Debug, Serialize)]
struct BountyDispute {
    id: i64,
    challenge_id: i64,
    project_id: i64,
    opened_by_user_id: i64,
    against_user_id: i64,
    reason: String,
    statement: String,
    project_title_snapshot: String,
    repository_url_snapshot: String,
    project_rules_snapshot: String,
    project_escrow_quota_snapshot: i64,
    challenge_status_snapshot: String,
    issue_url_snapshot: String,
    pull_request_url_snapshot: String,
    submission_note_snapshot: String,
    review_note_snapshot: String,
    reward_quota_snapshot: i64,
    tip_quota_snapshot: i64,
    owner_rating_score_snapshot: i64,
    owner_rating_comment_snapshot: String,
    contributor_rating_score_snapshot: i64,
    contributor_rating_comment_snapshot: String,
    status: String,
    resolution: String,
    resolved_by_user_id: i64,
    created_at: i64,
    updated_at: i64,
    resolved_at: i64,
}

#[derive(Debug, Serialize)]
struct BountyDisputeView {
    #[serde(flatten)]
    dispute: BountyDispute,
    project_title: String,
    repository_url: String,
    project_rules: String,
    challenge_status: String,
    current_project_escrow_quota: i64,
    issue_url: String,
    pull_request_url: String,
    submission_note: String,
    review_note: String,
    reward_quota: i64,
    tip_quota: i64,
    owner_rating_score: i64,
    owner_rating_comment: String,
    contributor_rating_score: i64,
    contributor_rating_comment: String,
    owner_username: String,
    participant_username: String,
    opened_by_username: String,
    against_username: String,
    live_evidence_changed: bool,
}

#[derive(Debug, Serialize)]
struct BountyNotification {
    id: i64,
    project_id: i64,
    challenge_id: i64,
    sender_user_id: i64,
    sender_username: String,
    kind: String,
    project_title: String,
    quota: i64,
    note: String,
    recipient_read_at: i64,
    thanked_at: i64,
    created_at: i64,
}

#[derive(Debug, Serialize)]
struct BountyTipNotification {
    id: i64,
    project_id: i64,
    challenge_id: i64,
    sender_user_id: i64,
    sender_username: String,
    project_title: String,
    quota: i64,
    note: String,
    recipient_read_at: i64,
    thanked_at: i64,
    created_at: i64,
}

#[derive(Debug, Serialize)]
struct FailureEnvelope {
    success: bool,
    code: &'static str,
    message: &'static str,
}

fn business_failure(code: &'static str, message: &'static str) -> Response {
    Json(FailureEnvelope {
        success: false,
        code,
        message,
    })
    .into_response()
}

fn auth_failure(error: AuthErrorKind) -> Response {
    let (status, code, message) = match error {
        AuthErrorKind::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AuthErrorKind::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AuthErrorKind::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        AuthErrorKind::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    (
        status,
        Json(FailureEnvelope {
            success: false,
            code,
            message,
        }),
    )
        .into_response()
}

fn internal_failure() -> Response {
    business_failure(
        "OPEN_SOURCE_BOUNTY_INTERNAL_ERROR",
        "open-source bounty operation failed",
    )
}

fn parse_fee_rate_basis_points(raw: &str) -> Option<i64> {
    let value = raw.trim();
    let (whole, fraction) = value.split_once('.').unwrap_or((value, ""));
    if whole.is_empty()
        || !whole.bytes().all(|byte| byte.is_ascii_digit())
        || fraction.len() > 2
        || !fraction.bytes().all(|byte| byte.is_ascii_digit())
    {
        return None;
    }
    if whole == "100" {
        if fraction.bytes().any(|byte| byte != b'0') {
            return None;
        }
    } else if whole.len() > 2 {
        return None;
    }
    let whole = whole.parse::<i64>().ok()?;
    if whole > 100 {
        return None;
    }
    let fraction = match fraction.len() {
        0 => 0,
        1 => fraction.parse::<i64>().ok()? * 10,
        2 => fraction.parse::<i64>().ok()?,
        _ => return None,
    };
    Some(whole * 100 + fraction)
}

fn authorization_token(headers: &HeaderMap) -> Option<String> {
    let raw = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut parts = raw.split_ascii_whitespace();
    let first = parts.next()?;
    let second = parts.next();
    if parts.next().is_some() {
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

/// Mirrors Go's unverified dashboard-JWT classification boundary.  A token
/// that declares the dashboard issuer/audience/use is never allowed to fall
/// through to opaque PAT lookup when its signature or session is invalid.
fn dashboard_token_candidate(raw: &str) -> bool {
    let mut segments = raw.trim().split('.');
    let (Some(header), Some(payload), Some(_signature), None) = (
        segments.next(),
        segments.next(),
        segments.next(),
        segments.next(),
    ) else {
        return false;
    };
    let Ok(header) = URL_SAFE_NO_PAD.decode(header) else {
        return false;
    };
    let Ok(payload) = URL_SAFE_NO_PAD.decode(payload) else {
        return false;
    };
    let Ok(header) = serde_json::from_slice::<Value>(&header) else {
        return false;
    };
    let Ok(payload) = serde_json::from_slice::<Value>(&payload) else {
        return false;
    };
    let audience_matches = payload.get("aud").is_some_and(|audience| match audience {
        Value::String(value) => value == "new-api-dashboard",
        Value::Array(values) => values
            .iter()
            .any(|value| value.as_str() == Some("new-api-dashboard")),
        _ => false,
    });
    let algorithm_registered = header
        .get("alg")
        .and_then(Value::as_str)
        .is_some_and(|algorithm| {
            matches!(
                algorithm,
                "none"
                    | "HS256"
                    | "HS384"
                    | "HS512"
                    | "RS256"
                    | "RS384"
                    | "RS512"
                    | "PS256"
                    | "PS384"
                    | "PS512"
                    | "ES256"
                    | "ES384"
                    | "ES512"
                    | "EdDSA"
            )
        });
    algorithm_registered
        && payload.get("iss").and_then(Value::as_str) == Some("new-api")
        && matches!(
            payload.get("token_use").and_then(Value::as_str),
            Some("access" | "security_proof")
        )
        && audience_matches
}

async fn optional_viewer_id(
    state: &OpenSourceBountyState,
    headers: &HeaderMap,
) -> Result<Option<i64>, Response> {
    let Some(token) = authorization_token(headers) else {
        return Ok(None);
    };

    // Go's TryUserAuth treats an unknown opaque token as anonymous.  Querying
    // the durable PAT index first preserves that distinction while keeping the
    // shared auth service authoritative for valid credentials and disabled
    // users.
    if !dashboard_token_candidate(&token) {
        let pat_owner = sqlx::query_scalar::<_, i64>(
            "SELECT id FROM users WHERE access_token = $1 AND deleted_at IS NULL",
        )
        .bind(&token)
        .fetch_optional(&state.pg)
        .await
        .map_err(|_| internal_failure())?;
        if pat_owner.is_none() {
            return Ok(None);
        }
    }

    let user = state
        .auth
        .self_user_for_optional(SecretString::from(token))
        .await
        .map_err(|error| auth_failure(error.kind))?;
    if user.id <= 0 || user.status != ENABLED_USER_STATUS {
        return Err(auth_failure(AuthErrorKind::Unauthorized));
    }
    Ok(Some(user.id))
}

async fn bounty_config(State(state): State<OpenSourceBountyState>, headers: HeaderMap) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let raw = match sqlx::query_scalar::<_, Option<String>>(
        "SELECT value FROM options WHERE key = 'OpenSourceBountyFeeRate'",
    )
    .fetch_optional(&state.pg)
    .await
    {
        Ok(value) => value.flatten().unwrap_or_default(),
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to load open-source bounty fee configuration");
            return internal_failure();
        }
    };
    let rate_basis_points = parse_fee_rate_basis_points(&raw).unwrap_or(100);
    Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: BountyFeeConfig {
            rate_percent: rate_basis_points as f64 / 100.0,
            rate_basis_points,
        },
    })
    .into_response()
}

async fn mcp_token_status(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_mcp_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let row = match sqlx::query(
        "SELECT token_hint, created_at, updated_at, last_used_at \
         FROM open_source_bounty_mcp_tokens WHERE user_id = $1",
    )
    .bind(viewer_id)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to load open-source bounty MCP token status");
            return internal_failure();
        }
    };
    let status = match row {
        Some(row) => match mcp_token_status_from_row(&row) {
            Ok(status) => status,
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode open-source bounty MCP token status");
                return internal_failure();
            }
        },
        None => BountyMcpTokenStatus {
            configured: false,
            token_hint: None,
            created_at: None,
            updated_at: None,
            last_used_at: None,
        },
    };
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: BountyMcpTokenStatusPayload {
            status,
            endpoint: "/mcp",
            protocol_version: MCP_PROTOCOL_VERSION,
        },
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn rotate_mcp_token(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
    client_ip: Option<Extension<ClientIpKey>>,
) -> Response {
    let viewer_id = match required_mcp_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    if let Some(response) = critical_mcp_rate_limit(&state, client_ip.as_ref()).await {
        return response;
    }
    let now = chrono::Utc::now().timestamp();
    let rotated = {
        let mut rotated = None;
        for _ in 0..3 {
            let token = new_mcp_token();
            let result = sqlx::query(
                "INSERT INTO open_source_bounty_mcp_tokens \
                 (user_id, token_hash, token_hint, created_at, updated_at, last_used_at) \
                 VALUES ($1, $2, $3, $4, $4, 0) \
                 ON CONFLICT (user_id) DO UPDATE SET \
                   token_hash = EXCLUDED.token_hash, token_hint = EXCLUDED.token_hint, \
                   updated_at = EXCLUDED.updated_at, last_used_at = 0 \
                 RETURNING token_hint, created_at, updated_at, last_used_at",
            )
            .bind(viewer_id)
            .bind(mcp_token_hash(&token))
            .bind(mcp_token_hint(&token))
            .bind(now)
            .fetch_one(&state.pg)
            .await;
            match result {
                Ok(row) => {
                    rotated = Some((token, row));
                    break;
                }
                Err(error)
                    if error
                        .as_database_error()
                        .is_some_and(|database| database.code().as_deref() == Some("23505")) =>
                {
                    continue;
                }
                Err(error) => {
                    tracing::error!(error = %error, viewer_id, "failed to rotate open-source bounty MCP token");
                    return internal_failure();
                }
            }
        }
        rotated
    };
    let Some((token, row)) = rotated else {
        tracing::error!(
            viewer_id,
            "failed to generate a unique open-source bounty MCP token"
        );
        return internal_failure();
    };
    let status = match mcp_token_status_from_row(&row) {
        Ok(status) => status,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to decode rotated open-source bounty MCP token");
            return internal_failure();
        }
    };
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: BountyMcpTokenStatusPayloadWithSecret {
            token,
            status,
            endpoint: "/mcp",
            protocol_version: MCP_PROTOCOL_VERSION,
        },
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn revoke_mcp_token(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
    client_ip: Option<Extension<ClientIpKey>>,
) -> Response {
    let viewer_id = match required_mcp_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    if let Some(response) = critical_mcp_rate_limit(&state, client_ip.as_ref()).await {
        return response;
    }
    if let Err(error) = sqlx::query("DELETE FROM open_source_bounty_mcp_tokens WHERE user_id = $1")
        .bind(viewer_id)
        .execute(&state.pg)
        .await
    {
        tracing::error!(error = %error, viewer_id, "failed to revoke open-source bounty MCP token");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: Value::Null,
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn critical_mcp_rate_limit(
    state: &OpenSourceBountyState,
    client_ip: Option<&Extension<ClientIpKey>>,
) -> Option<Response> {
    let client_ip = client_ip
        .map(|extension| extension.0.0.as_str())
        .unwrap_or("unknown");
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

async fn disable_mcp_cache(request: Request, next: Next) -> Response {
    if request.uri().path() != "/api/open-source-bounties/mcp-token" {
        return next.run(request).await;
    }
    let mut response = next.run(request).await;
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

fn new_mcp_token() -> String {
    let mut bytes = [0_u8; 32];
    rand::rng().fill_bytes(&mut bytes);
    format!("{MCP_TOKEN_PREFIX}{}", URL_SAFE_NO_PAD.encode(bytes))
}

fn mcp_token_hash(token: &str) -> String {
    hex::encode(Sha256::digest(token.as_bytes()))
}

fn mcp_token_hint(token: &str) -> String {
    format!("{MCP_TOKEN_PREFIX}••••{}", &token[token.len() - 8..])
}

fn mcp_token_status_from_row(row: &PgRow) -> Result<BountyMcpTokenStatus, sqlx::Error> {
    let token_hint = row.try_get::<String, _>("token_hint")?;
    let created_at = row.try_get::<i64, _>("created_at")?;
    let updated_at = row.try_get::<i64, _>("updated_at")?;
    let last_used_at = row.try_get::<i64, _>("last_used_at")?;
    Ok(BountyMcpTokenStatus {
        configured: true,
        token_hint: (!token_hint.is_empty()).then_some(token_hint),
        created_at: (created_at != 0).then_some(created_at),
        updated_at: (updated_at != 0).then_some(updated_at),
        last_used_at: (last_used_at != 0).then_some(last_used_at),
    })
}

async fn required_viewer_id(
    state: &OpenSourceBountyState,
    headers: &HeaderMap,
) -> Result<i64, Response> {
    // Every non-discovery bounty route is a dashboard-console surface. Keep
    // this compatibility-named helper delegated to the one durable access
    // gate so a future private handler cannot accidentally accept L0 users.
    required_developer_viewer_id(state, headers).await
}

async fn required_developer_viewer_id(
    state: &OpenSourceBountyState,
    headers: &HeaderMap,
) -> Result<i64, Response> {
    let token =
        authorization_token(headers).ok_or_else(|| auth_failure(AuthErrorKind::Unauthorized))?;
    let view = state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
        .map_err(|error| auth_failure(error.kind))?;
    if view.id <= 0 || view.status != ENABLED_USER_STATUS {
        return Err(auth_failure(AuthErrorKind::Unauthorized));
    }
    if !view.developer_access_granted {
        return Err(console_not_found());
    }
    Ok(view.id)
}

/// Mirrors the Go ConsoleAccessGate boundary around the MCP management
/// surface. Anonymous, invalid, disabled, and pre-activation dashboard
/// credentials are deliberately concealed as a generic 404 before UserAuth
/// reaches the handler; an activated credential continues through the normal
/// viewer lookup.
async fn required_mcp_viewer_id(
    state: &OpenSourceBountyState,
    headers: &HeaderMap,
) -> Result<i64, Response> {
    let Some(token) = authorization_token(headers) else {
        return Err(console_not_found());
    };
    let view = state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
        .map_err(|_| console_not_found())?;
    if view.id <= 0 || view.status != ENABLED_USER_STATUS || !view.developer_access_granted {
        return Err(console_not_found());
    }
    Ok(view.id)
}

fn console_not_found() -> Response {
    let mut response =
        (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

async fn required_admin_id(
    state: &OpenSourceBountyState,
    headers: &HeaderMap,
) -> Result<i64, Response> {
    let token =
        authorization_token(headers).ok_or_else(|| auth_failure(AuthErrorKind::Unauthorized))?;
    let view = state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
        .map_err(|error| auth_failure(error.kind))?;
    if view.id <= 0 || view.status != ENABLED_USER_STATUS {
        return Err(auth_failure(AuthErrorKind::Unauthorized));
    }
    if !view.developer_access_granted {
        return Err(console_not_found());
    }
    if view.role < ROLE_ADMIN {
        return Err((
            StatusCode::FORBIDDEN,
            Json(FailureEnvelope {
                success: false,
                code: "AUTH_INSUFFICIENT_PRIVILEGE",
                message: "Unauthorized, insufficient privileges",
            }),
        )
            .into_response());
    }
    Ok(view.id)
}

fn project_select() -> &'static str {
    // Keep casts explicit: Go's integer fields are persisted differently by
    // historical PostgreSQL migrations, while the wire contract is numeric.
    "SELECT p.id::BIGINT AS id, p.owner_user_id::BIGINT AS owner_user_id, p.repository_url, p.title, p.description, p.rules, p.reward_quota::BIGINT AS reward_quota, p.net_reward_quota::BIGINT AS net_reward_quota, p.reward_slots::BIGINT AS reward_slots, p.escrow_quota::BIGINT AS escrow_quota, p.platform_fee_rate_bps::BIGINT AS platform_fee_rate_bps, p.platform_fee_quota::BIGINT AS platform_fee_quota, p.status, p.created_at::BIGINT AS created_at, p.updated_at::BIGINT AS updated_at, p.published_at::BIGINT AS published_at, p.closed_at::BIGINT AS closed_at, p.archived_at::BIGINT AS archived_at, u.username AS owner_username, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges c WHERE c.project_id = p.id AND (c.status IN ('accepted','submitted') OR (c.status = 'rejected' AND c.rejected_at > $1 AND NOT EXISTS (SELECT 1 FROM open_source_bounty_disputes resolved_dispute WHERE resolved_dispute.challenge_id = c.id AND resolved_dispute.status IN ('resolved_paid','resolved_denied'))) OR EXISTS (SELECT 1 FROM open_source_bounty_disputes dispute WHERE dispute.challenge_id = c.id AND dispute.status = 'open'))) AS active_challenge_count, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges c WHERE c.project_id = p.id AND c.status = 'approved') AS approved_challenge_count, COALESCE((SELECT AVG(c.contributor_rating_score)::DOUBLE PRECISION FROM open_source_bounty_challenges c JOIN open_source_bounty_projects rated_project ON rated_project.id = c.project_id WHERE rated_project.owner_user_id = p.owner_user_id AND c.contributor_rating_score > 0), 0)::DOUBLE PRECISION AS owner_rating_average, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges c JOIN open_source_bounty_projects rated_project ON rated_project.id = c.project_id WHERE rated_project.owner_user_id = p.owner_user_id AND c.contributor_rating_score > 0) AS owner_rating_count, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_ledgers heart WHERE heart.user_id = p.owner_user_id AND heart.kind = 'tip_transfer' AND heart.thanked_at > 0) AS owner_thank_heart_count FROM open_source_bounty_projects p JOIN users u ON u.id = p.owner_user_id AND u.deleted_at IS NULL"
}

fn project_from_row(row: &PgRow) -> Result<BountyProjectView, sqlx::Error> {
    Ok(BountyProjectView {
        id: row.try_get("id")?,
        owner_user_id: row.try_get("owner_user_id")?,
        repository_url: row.try_get("repository_url")?,
        title: row.try_get("title")?,
        description: row.try_get("description")?,
        rules: row.try_get("rules")?,
        reward_quota: row.try_get("reward_quota")?,
        net_reward_quota: row.try_get("net_reward_quota")?,
        reward_slots: row.try_get("reward_slots")?,
        escrow_quota: row.try_get("escrow_quota")?,
        platform_fee_rate_bps: row.try_get("platform_fee_rate_bps")?,
        platform_fee_quota: row.try_get("platform_fee_quota")?,
        status: row.try_get("status")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
        published_at: row.try_get("published_at")?,
        closed_at: row.try_get("closed_at")?,
        archived_at: row.try_get("archived_at")?,
        owner_username: row.try_get("owner_username")?,
        active_challenge_count: row.try_get("active_challenge_count")?,
        approved_challenge_count: row.try_get("approved_challenge_count")?,
        owner_rating_average: row.try_get("owner_rating_average")?,
        owner_rating_count: row.try_get("owner_rating_count")?,
        owner_thank_heart_count: row.try_get("owner_thank_heart_count")?,
        viewer_challenge: None,
    })
}

fn challenge_select() -> &'static str {
    "SELECT id::BIGINT AS id, project_id::BIGINT AS project_id, participant_user_id::BIGINT AS participant_user_id, github_handle, status, issue_url, pull_request_url, submission_note, review_note, reward_quota::BIGINT AS reward_quota, tip_quota::BIGINT AS tip_quota, owner_rating_score::BIGINT AS owner_rating_score, owner_rating_comment, owner_rated_at::BIGINT AS owner_rated_at, contributor_rating_score::BIGINT AS contributor_rating_score, contributor_rating_comment, contributor_rated_at::BIGINT AS contributor_rated_at, owner_rating_overturned, accepted_at::BIGINT AS accepted_at, submitted_at::BIGINT AS submitted_at, reviewed_at::BIGINT AS reviewed_at, rejected_at::BIGINT AS rejected_at, paid_at::BIGINT AS paid_at, created_at::BIGINT AS created_at, updated_at::BIGINT AS updated_at FROM open_source_bounty_challenges"
}

fn challenge_from_row(row: &PgRow) -> Result<BountyChallenge, sqlx::Error> {
    Ok(BountyChallenge {
        id: row.try_get("id")?,
        project_id: row.try_get("project_id")?,
        participant_user_id: row.try_get("participant_user_id")?,
        github_handle: row.try_get("github_handle")?,
        status: row.try_get("status")?,
        issue_url: row.try_get("issue_url")?,
        pull_request_url: row.try_get("pull_request_url")?,
        submission_note: row.try_get("submission_note")?,
        review_note: row.try_get("review_note")?,
        reward_quota: row.try_get("reward_quota")?,
        tip_quota: row.try_get("tip_quota")?,
        owner_rating_score: row.try_get("owner_rating_score")?,
        owner_rating_comment: row.try_get("owner_rating_comment")?,
        owner_rated_at: row.try_get("owner_rated_at")?,
        contributor_rating_score: row.try_get("contributor_rating_score")?,
        contributor_rating_comment: row.try_get("contributor_rating_comment")?,
        contributor_rated_at: row.try_get("contributor_rated_at")?,
        owner_rating_overturned: row.try_get("owner_rating_overturned")?,
        accepted_at: row.try_get("accepted_at")?,
        submitted_at: row.try_get("submitted_at")?,
        reviewed_at: row.try_get("reviewed_at")?,
        rejected_at: row.try_get("rejected_at")?,
        paid_at: row.try_get("paid_at")?,
        created_at: row.try_get("created_at")?,
        updated_at: row.try_get("updated_at")?,
    })
}

async fn load_challenge(
    state: &OpenSourceBountyState,
    challenge_id: i64,
) -> Result<Option<BountyChallenge>, sqlx::Error> {
    sqlx::query(&format!("{} WHERE id = $1", challenge_select()))
        .bind(challenge_id)
        .fetch_optional(&state.pg)
        .await?
        .as_ref()
        .map(challenge_from_row)
        .transpose()
}

fn challenge_view_select() -> &'static str {
    "SELECT c.id::BIGINT AS id, c.project_id::BIGINT AS project_id, c.participant_user_id::BIGINT AS participant_user_id, c.github_handle, c.status, c.issue_url, c.pull_request_url, c.submission_note, c.review_note, c.reward_quota::BIGINT AS reward_quota, c.tip_quota::BIGINT AS tip_quota, c.owner_rating_score::BIGINT AS owner_rating_score, c.owner_rating_comment, c.owner_rated_at::BIGINT AS owner_rated_at, c.contributor_rating_score::BIGINT AS contributor_rating_score, c.contributor_rating_comment, c.contributor_rated_at::BIGINT AS contributor_rated_at, c.owner_rating_overturned, c.accepted_at::BIGINT AS accepted_at, c.submitted_at::BIGINT AS submitted_at, c.reviewed_at::BIGINT AS reviewed_at, c.rejected_at::BIGINT AS rejected_at, c.paid_at::BIGINT AS paid_at, c.created_at::BIGINT AS created_at, c.updated_at::BIGINT AS updated_at, participant.username AS participant_username, p.title AS project_title, p.repository_url AS repository_url, owner.username AS owner_username, COALESCE((SELECT AVG(history.owner_rating_score)::DOUBLE PRECISION FROM open_source_bounty_challenges history WHERE history.participant_user_id = c.participant_user_id AND history.owner_rating_score > 0 AND history.owner_rating_overturned = FALSE), 0)::DOUBLE PRECISION AS participant_rating_average, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges history WHERE history.participant_user_id = c.participant_user_id AND history.owner_rating_score > 0 AND history.owner_rating_overturned = FALSE) AS participant_rating_count, COALESCE((SELECT AVG(history.contributor_rating_score)::DOUBLE PRECISION FROM open_source_bounty_challenges history JOIN open_source_bounty_projects history_project ON history_project.id = history.project_id WHERE history_project.owner_user_id = p.owner_user_id AND history.contributor_rating_score > 0), 0)::DOUBLE PRECISION AS owner_rating_average, (SELECT COUNT(*)::BIGINT FROM open_source_bounty_challenges history JOIN open_source_bounty_projects history_project ON history_project.id = history.project_id WHERE history_project.owner_user_id = p.owner_user_id AND history.contributor_rating_score > 0) AS owner_rating_count FROM open_source_bounty_challenges c JOIN users participant ON participant.id = c.participant_user_id JOIN open_source_bounty_projects p ON p.id = c.project_id JOIN users owner ON owner.id = p.owner_user_id"
}

fn challenge_view_from_row(row: &PgRow) -> Result<BountyChallengeView, sqlx::Error> {
    Ok(BountyChallengeView {
        challenge: challenge_from_row(row)?,
        participant_username: row.try_get("participant_username")?,
        project_title: row.try_get("project_title")?,
        repository_url: row.try_get("repository_url")?,
        owner_username: row.try_get("owner_username")?,
        participant_rating_average: row.try_get("participant_rating_average")?,
        participant_rating_count: row.try_get("participant_rating_count")?,
        owner_rating_average: row.try_get("owner_rating_average")?,
        owner_rating_count: row.try_get("owner_rating_count")?,
        dispute: None,
    })
}

fn ledger_from_row(row: &PgRow) -> Result<BountyLedger, sqlx::Error> {
    Ok(BountyLedger {
        id: row.try_get("id")?,
        project_id: row.try_get("project_id")?,
        challenge_id: row.try_get("challenge_id")?,
        user_id: row.try_get("user_id")?,
        counterparty_user_id: row.try_get("counterparty_user_id")?,
        kind: row.try_get("kind")?,
        quota: row.try_get("quota")?,
        note: row.try_get("note")?,
        recipient_read_at: row.try_get("recipient_read_at")?,
        thanked_at: row.try_get("thanked_at")?,
        created_at: row.try_get("created_at")?,
    })
}

fn dispute_view_select() -> &'static str {
    "SELECT d.id::BIGINT AS id, d.challenge_id::BIGINT AS challenge_id, d.project_id::BIGINT AS project_id, d.opened_by_user_id::BIGINT AS opened_by_user_id, d.against_user_id::BIGINT AS against_user_id, d.reason, d.statement, d.project_title_snapshot, d.repository_url_snapshot, d.project_rules_snapshot, d.project_escrow_quota_snapshot::BIGINT AS project_escrow_quota_snapshot, d.challenge_status_snapshot, d.issue_url_snapshot, d.pull_request_url_snapshot, d.submission_note_snapshot, d.review_note_snapshot, d.reward_quota_snapshot::BIGINT AS reward_quota_snapshot, d.tip_quota_snapshot::BIGINT AS tip_quota_snapshot, d.owner_rating_score_snapshot::BIGINT AS owner_rating_score_snapshot, d.owner_rating_comment_snapshot, d.contributor_rating_score_snapshot::BIGINT AS contributor_rating_score_snapshot, d.contributor_rating_comment_snapshot, d.status, d.resolution, d.resolved_by_user_id::BIGINT AS resolved_by_user_id, d.created_at::BIGINT AS created_at, d.updated_at::BIGINT AS updated_at, d.resolved_at::BIGINT AS resolved_at, p.title AS project_title, p.repository_url AS repository_url, p.rules AS project_rules, c.status AS challenge_status, p.escrow_quota::BIGINT AS current_project_escrow_quota, c.issue_url AS issue_url, c.pull_request_url AS pull_request_url, c.submission_note AS submission_note, c.review_note AS review_note, c.reward_quota::BIGINT AS reward_quota, c.tip_quota::BIGINT AS tip_quota, c.owner_rating_score::BIGINT AS owner_rating_score, c.owner_rating_comment AS owner_rating_comment, c.contributor_rating_score::BIGINT AS contributor_rating_score, c.contributor_rating_comment AS contributor_rating_comment, owner.username AS owner_username, participant.username AS participant_username, opener.username AS opened_by_username, against_user.username AS against_username, CASE WHEN p.title <> d.project_title_snapshot OR p.repository_url <> d.repository_url_snapshot OR p.rules <> d.project_rules_snapshot OR c.status <> d.challenge_status_snapshot OR c.issue_url <> d.issue_url_snapshot OR c.pull_request_url <> d.pull_request_url_snapshot OR c.submission_note <> d.submission_note_snapshot OR c.review_note <> d.review_note_snapshot OR c.reward_quota <> d.reward_quota_snapshot OR c.tip_quota <> d.tip_quota_snapshot OR c.owner_rating_score <> d.owner_rating_score_snapshot OR c.owner_rating_comment <> d.owner_rating_comment_snapshot OR c.contributor_rating_score <> d.contributor_rating_score_snapshot OR c.contributor_rating_comment <> d.contributor_rating_comment_snapshot THEN TRUE ELSE FALSE END AS live_evidence_changed FROM open_source_bounty_disputes d JOIN open_source_bounty_challenges c ON c.id = d.challenge_id JOIN open_source_bounty_projects p ON p.id = d.project_id JOIN users owner ON owner.id = p.owner_user_id JOIN users participant ON participant.id = c.participant_user_id JOIN users opener ON opener.id = d.opened_by_user_id JOIN users against_user ON against_user.id = d.against_user_id"
}

fn dispute_view_from_row(row: &PgRow) -> Result<BountyDisputeView, sqlx::Error> {
    Ok(BountyDisputeView {
        dispute: BountyDispute {
            id: row.try_get("id")?,
            challenge_id: row.try_get("challenge_id")?,
            project_id: row.try_get("project_id")?,
            opened_by_user_id: row.try_get("opened_by_user_id")?,
            against_user_id: row.try_get("against_user_id")?,
            reason: row.try_get("reason")?,
            statement: row.try_get("statement")?,
            project_title_snapshot: row.try_get("project_title_snapshot")?,
            repository_url_snapshot: row.try_get("repository_url_snapshot")?,
            project_rules_snapshot: row.try_get("project_rules_snapshot")?,
            project_escrow_quota_snapshot: row.try_get("project_escrow_quota_snapshot")?,
            challenge_status_snapshot: row.try_get("challenge_status_snapshot")?,
            issue_url_snapshot: row.try_get("issue_url_snapshot")?,
            pull_request_url_snapshot: row.try_get("pull_request_url_snapshot")?,
            submission_note_snapshot: row.try_get("submission_note_snapshot")?,
            review_note_snapshot: row.try_get("review_note_snapshot")?,
            reward_quota_snapshot: row.try_get("reward_quota_snapshot")?,
            tip_quota_snapshot: row.try_get("tip_quota_snapshot")?,
            owner_rating_score_snapshot: row.try_get("owner_rating_score_snapshot")?,
            owner_rating_comment_snapshot: row.try_get("owner_rating_comment_snapshot")?,
            contributor_rating_score_snapshot: row.try_get("contributor_rating_score_snapshot")?,
            contributor_rating_comment_snapshot: row
                .try_get("contributor_rating_comment_snapshot")?,
            status: row.try_get("status")?,
            resolution: row.try_get("resolution")?,
            resolved_by_user_id: row.try_get("resolved_by_user_id")?,
            created_at: row.try_get("created_at")?,
            updated_at: row.try_get("updated_at")?,
            resolved_at: row.try_get("resolved_at")?,
        },
        project_title: row.try_get("project_title")?,
        repository_url: row.try_get("repository_url")?,
        project_rules: row.try_get("project_rules")?,
        challenge_status: row.try_get("challenge_status")?,
        current_project_escrow_quota: row.try_get("current_project_escrow_quota")?,
        issue_url: row.try_get("issue_url")?,
        pull_request_url: row.try_get("pull_request_url")?,
        submission_note: row.try_get("submission_note")?,
        review_note: row.try_get("review_note")?,
        reward_quota: row.try_get("reward_quota")?,
        tip_quota: row.try_get("tip_quota")?,
        owner_rating_score: row.try_get("owner_rating_score")?,
        owner_rating_comment: row.try_get("owner_rating_comment")?,
        contributor_rating_score: row.try_get("contributor_rating_score")?,
        contributor_rating_comment: row.try_get("contributor_rating_comment")?,
        owner_username: row.try_get("owner_username")?,
        participant_username: row.try_get("participant_username")?,
        opened_by_username: row.try_get("opened_by_username")?,
        against_username: row.try_get("against_username")?,
        live_evidence_changed: row.try_get("live_evidence_changed")?,
    })
}

async fn load_dispute_view(
    state: &OpenSourceBountyState,
    dispute_id: i64,
    viewer_id: Option<i64>,
    admin: bool,
) -> Result<Option<BountyDisputeView>, sqlx::Error> {
    let mut sql = format!("{} WHERE d.id = $1", dispute_view_select());
    if !admin {
        sql.push_str(" AND (d.opened_by_user_id = $2 OR d.against_user_id = $2)");
    }
    let mut query = sqlx::query(&sql).bind(dispute_id);
    if !admin {
        query = query.bind(viewer_id.unwrap_or_default());
    }
    query
        .fetch_optional(&state.pg)
        .await?
        .as_ref()
        .map(dispute_view_from_row)
        .transpose()
}

fn challenge_priority(status: &str) -> i64 {
    match status {
        "approved" => 4,
        "accepted" | "submitted" => 3,
        "rejected" => 2,
        "withdrawn" | "cancelled" => 1,
        _ => 0,
    }
}

async fn attach_viewer_challenges(
    state: &OpenSourceBountyState,
    views: &mut [BountyProjectView],
    viewer_id: Option<i64>,
) -> Result<(), sqlx::Error> {
    let Some(viewer_id) = viewer_id else {
        return Ok(());
    };
    if views.is_empty() {
        return Ok(());
    }
    let project_ids = views.iter().map(|view| view.id).collect::<Vec<_>>();
    let query = format!(
        "{} WHERE participant_user_id = $1 AND project_id = ANY($2)",
        challenge_select()
    );
    let rows = sqlx::query(&query)
        .bind(viewer_id)
        .bind(&project_ids)
        .fetch_all(&state.pg)
        .await?;
    let mut selected = std::collections::HashMap::<i64, BountyChallenge>::new();
    for row in rows {
        let challenge = challenge_from_row(&row)?;
        let replace = selected.get(&challenge.project_id).is_none_or(|current| {
            challenge_priority(&challenge.status) > challenge_priority(&current.status)
                || (challenge_priority(&challenge.status) == challenge_priority(&current.status)
                    && challenge.id > current.id)
        });
        if replace {
            selected.insert(challenge.project_id, challenge);
        }
    }
    for view in views {
        view.viewer_challenge = selected.remove(&view.id);
    }
    Ok(())
}

async fn attach_disputes(
    state: &OpenSourceBountyState,
    views: &mut [BountyChallengeView],
) -> Result<(), sqlx::Error> {
    if views.is_empty() {
        return Ok(());
    }
    let challenge_ids = views
        .iter()
        .map(|view| view.challenge.id)
        .collect::<Vec<_>>();
    let query = format!(
        "{} WHERE d.challenge_id = ANY($1) ORDER BY d.created_at DESC, d.id DESC",
        dispute_view_select()
    );
    let rows = sqlx::query(&query)
        .bind(&challenge_ids)
        .fetch_all(&state.pg)
        .await?;
    let mut selected = std::collections::HashMap::<i64, BountyDisputeView>::new();
    for row in rows {
        let dispute = dispute_view_from_row(&row)?;
        selected
            .entry(dispute.dispute.challenge_id)
            .or_insert(dispute);
    }
    for view in views {
        view.dispute = selected.remove(&view.challenge.id);
    }
    Ok(())
}

async fn detail_bounty(
    State(state): State<OpenSourceBountyState>,
    Path(project_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let project_id = match project_id.parse::<i64>() {
        Ok(project_id) if project_id > 0 => project_id,
        _ => {
            return business_failure(
                "OPEN_SOURCE_BOUNTY_INVALID_ID",
                "invalid open-source bounty identifier",
            );
        }
    };
    let viewer_id = match optional_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let now = chrono::Utc::now().timestamp();
    let query = format!("{} WHERE p.id = $2", project_select());
    let row = match sqlx::query(&query)
        .bind(now - 7 * 24 * 60 * 60)
        .bind(project_id)
        .fetch_optional(&state.pg)
        .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(error = %error, project_id, "failed to load public open-source bounty detail");
            return internal_failure();
        }
    };
    let Some(row) = row else {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_NOT_FOUND",
            "bounty project was not found",
        );
    };
    let mut project = match project_from_row(&row) {
        Ok(project) => project,
        Err(error) => {
            tracing::error!(error = %error, project_id, "failed to decode open-source bounty detail");
            return internal_failure();
        }
    };
    if (matches!(project.status.as_str(), "draft" | "closed") || project.archived_at > 0)
        && viewer_id != Some(project.owner_user_id)
    {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_NOT_FOUND",
            "bounty project was not found",
        );
    }
    if let Err(error) =
        attach_viewer_challenges(&state, std::slice::from_mut(&mut project), viewer_id).await
    {
        tracing::error!(error = %error, project_id, "failed to attach bounty viewer challenge");
        return internal_failure();
    }
    let mut challenges = Vec::new();
    let mut ledger = Vec::new();
    if viewer_id == Some(project.owner_user_id) {
        let challenge_query = format!(
            "{} WHERE c.project_id = $1 ORDER BY c.created_at DESC, c.id DESC",
            challenge_view_select()
        );
        let rows = match sqlx::query(&challenge_query)
            .bind(project_id)
            .fetch_all(&state.pg)
            .await
        {
            Ok(rows) => rows,
            Err(error) => {
                tracing::error!(error = %error, project_id, "failed to load bounty challenges");
                return internal_failure();
            }
        };
        for row in rows {
            match challenge_view_from_row(&row) {
                Ok(challenge) => challenges.push(challenge),
                Err(error) => {
                    tracing::error!(error = %error, project_id, "failed to decode bounty challenge");
                    return internal_failure();
                }
            }
        }
        if let Err(error) = attach_disputes(&state, &mut challenges).await {
            tracing::error!(error = %error, project_id, "failed to attach bounty disputes");
            return internal_failure();
        }
        let ledger_query = "SELECT id::BIGINT AS id, project_id::BIGINT AS project_id, challenge_id::BIGINT AS challenge_id, user_id::BIGINT AS user_id, counterparty_user_id::BIGINT AS counterparty_user_id, kind, quota::BIGINT AS quota, note, recipient_read_at::BIGINT AS recipient_read_at, thanked_at::BIGINT AS thanked_at, created_at::BIGINT AS created_at FROM open_source_bounty_ledgers WHERE project_id = $1 ORDER BY created_at DESC, id DESC";
        let rows = match sqlx::query(ledger_query)
            .bind(project_id)
            .fetch_all(&state.pg)
            .await
        {
            Ok(rows) => rows,
            Err(error) => {
                tracing::error!(error = %error, project_id, "failed to load bounty ledger");
                return internal_failure();
            }
        };
        for row in rows {
            match ledger_from_row(&row) {
                Ok(entry) => ledger.push(entry),
                Err(error) => {
                    tracing::error!(error = %error, project_id, "failed to decode bounty ledger");
                    return internal_failure();
                }
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: BountyProjectDetail {
            project,
            challenges,
            ledger,
        },
    })
    .into_response();
    if viewer_id.is_some() {
        response
            .headers_mut()
            .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    }
    response
}

async fn list_bounties(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<ListQuery>,
    headers: HeaderMap,
) -> Response {
    let (page, page_size) = query.normalized();
    let viewer_id = match optional_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let total = match sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM open_source_bounty_projects WHERE status IN ('published', 'paused')",
    )
    .fetch_one(&state.pg)
    .await
    {
        Ok(total) => total,
        Err(error) => {
            tracing::error!(error = %error, "failed to count public open-source bounties");
            return internal_failure();
        }
    };
    let now = chrono::Utc::now().timestamp();
    let query_sql = format!(
        "{} WHERE p.status IN ('published', 'paused') ORDER BY p.reward_quota DESC, CASE WHEN p.status = 'published' THEN 0 ELSE 1 END ASC, p.published_at ASC, p.id ASC OFFSET $2 LIMIT $3",
        project_select()
    );
    let rows = match sqlx::query(&query_sql)
        .bind(now - 7 * 24 * 60 * 60)
        .bind((page - 1).saturating_mul(page_size))
        .bind(page_size)
        .fetch_all(&state.pg)
        .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, "failed to list public open-source bounties");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match project_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, "failed to decode public open-source bounty");
                return internal_failure();
            }
        }
    }
    if let Err(error) = attach_viewer_challenges(&state, &mut items, viewer_id).await {
        tracing::error!(error = %error, "failed to attach public bounty viewer challenge");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: ListPayload {
            items,
            total,
            page,
            page_size,
        },
    })
    .into_response();
    if viewer_id.is_some() {
        response
            .headers_mut()
            .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    }
    response
}

async fn owned_bounties(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<OwnedQuery>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let now = chrono::Utc::now().timestamp();
    let archive_filter = if query.archived() {
        "p.archived_at > 0"
    } else {
        "p.archived_at = 0"
    };
    let sql = format!(
        "{} WHERE p.owner_user_id = $2 AND {archive_filter} ORDER BY p.created_at DESC, p.id DESC",
        project_select()
    );
    let rows = match sqlx::query(&sql)
        .bind(now - 7 * 24 * 60 * 60)
        .bind(viewer_id)
        .fetch_all(&state.pg)
        .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to list owned open-source bounties");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match project_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode owned open-source bounty");
                return internal_failure();
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn accepted_bounties(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let query = format!(
        "{} WHERE c.participant_user_id = $1 ORDER BY c.updated_at DESC, c.id DESC",
        challenge_view_select()
    );
    let rows = match sqlx::query(&query)
        .bind(viewer_id)
        .fetch_all(&state.pg)
        .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to list accepted open-source bounty challenges");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match challenge_view_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode accepted open-source bounty challenge");
                return internal_failure();
            }
        }
    }
    if let Err(error) = attach_disputes(&state, &mut items).await {
        tracing::error!(error = %error, viewer_id, "failed to attach accepted bounty disputes");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn owned_disputes(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<DisputeListQuery>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let (status, limit) = match query.normalized() {
        Ok(values) => values,
        Err(response) => return *response,
    };
    let mut sql = format!(
        "{} WHERE (d.opened_by_user_id = $1 OR d.against_user_id = $1)",
        dispute_view_select()
    );
    if status.is_some() {
        sql.push_str(" AND d.status = $2");
    }
    sql.push_str(" ORDER BY CASE WHEN d.status = 'open' THEN 0 ELSE 1 END, d.updated_at DESC, d.id DESC LIMIT $");
    sql.push_str(if status.is_some() { "3" } else { "2" });
    let mut request = sqlx::query(&sql).bind(viewer_id);
    if let Some(status) = status {
        request = request.bind(status);
    }
    let rows = match request.bind(limit).fetch_all(&state.pg).await {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to list owned open-source bounty disputes");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match dispute_view_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode owned open-source bounty dispute");
                return internal_failure();
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

async fn admin_disputes(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<DisputeListQuery>,
    headers: HeaderMap,
) -> Response {
    let admin_id = match required_admin_id(&state, &headers).await {
        Ok(admin_id) => admin_id,
        Err(response) => return response,
    };
    let (status, limit) = match query.normalized() {
        Ok(values) => values,
        Err(response) => return *response,
    };
    let mut sql = dispute_view_select().to_owned();
    if status.is_some() {
        sql.push_str(" WHERE d.status = $1");
    }
    sql.push_str(" ORDER BY CASE WHEN d.status = 'open' THEN 0 ELSE 1 END, d.updated_at DESC, d.id DESC LIMIT $");
    sql.push_str(if status.is_some() { "2" } else { "1" });
    let mut request = sqlx::query(&sql);
    if let Some(status) = status {
        request = request.bind(status);
    }
    let rows = match request.bind(limit).fetch_all(&state.pg).await {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, admin_id, "failed to list administrator open-source bounty disputes");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match dispute_view_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, admin_id, "failed to decode administrator open-source bounty dispute");
                return internal_failure();
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn notification_from_row(row: &PgRow) -> Result<BountyNotification, sqlx::Error> {
    Ok(BountyNotification {
        id: row.try_get("id")?,
        project_id: row.try_get("project_id")?,
        challenge_id: row.try_get("challenge_id")?,
        sender_user_id: row.try_get("sender_user_id")?,
        sender_username: row.try_get("sender_username")?,
        kind: row.try_get("kind")?,
        project_title: row.try_get("project_title")?,
        quota: row.try_get("quota")?,
        note: row.try_get("note")?,
        recipient_read_at: row.try_get("recipient_read_at")?,
        thanked_at: row.try_get("thanked_at")?,
        created_at: row.try_get("created_at")?,
    })
}

fn tip_notification_from_row(row: &PgRow) -> Result<BountyTipNotification, sqlx::Error> {
    Ok(BountyTipNotification {
        id: row.try_get("id")?,
        project_id: row.try_get("project_id")?,
        challenge_id: row.try_get("challenge_id")?,
        sender_user_id: row.try_get("sender_user_id")?,
        sender_username: row.try_get("sender_username")?,
        project_title: row.try_get("project_title")?,
        quota: row.try_get("quota")?,
        note: row.try_get("note")?,
        recipient_read_at: row.try_get("recipient_read_at")?,
        thanked_at: row.try_get("thanked_at")?,
        created_at: row.try_get("created_at")?,
    })
}

fn add_auth_version(response: &mut Response) {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
}

async fn list_notifications(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<NotificationQuery>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let limit = query.normalized_limit();
    let rows = match sqlx::query(
        "SELECT notification.id::BIGINT AS id, notification.project_id::BIGINT AS project_id, \
                notification.challenge_id::BIGINT AS challenge_id, notification.user_id::BIGINT AS sender_user_id, \
                sender.username AS sender_username, notification.kind, project.title AS project_title, \
                notification.quota::BIGINT AS quota, notification.note, notification.recipient_read_at::BIGINT AS recipient_read_at, \
                notification.thanked_at::BIGINT AS thanked_at, notification.created_at::BIGINT AS created_at \
         FROM open_source_bounty_ledgers notification \
         JOIN users sender ON sender.id = notification.user_id AND sender.deleted_at IS NULL \
         JOIN open_source_bounty_projects project ON project.id = notification.project_id \
         WHERE notification.kind IN ('tip_transfer', 'reward_transfer', 'dispute_reward_transfer') \
           AND notification.counterparty_user_id = $1 \
         ORDER BY notification.created_at DESC, notification.id DESC LIMIT $2",
    )
    .bind(viewer_id)
    .bind(limit)
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to list open-source bounty notifications");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match notification_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode open-source bounty notification");
                return internal_failure();
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    add_auth_version(&mut response);
    response
}

async fn mark_notifications_read(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_ledgers \
         SET recipient_read_at = $1 \
         WHERE kind IN ('tip_transfer', 'reward_transfer', 'dispute_reward_transfer') \
           AND counterparty_user_id = $2 AND recipient_read_at = 0",
    )
    .bind(now)
    .bind(viewer_id)
    .execute(&state.pg)
    .await
    {
        tracing::error!(error = %error, viewer_id, "failed to mark open-source bounty notifications read");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: Value::Null,
    })
    .into_response();
    add_auth_version(&mut response);
    response
}

async fn list_tip_notifications(
    State(state): State<OpenSourceBountyState>,
    Query(query): Query<NotificationQuery>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let limit = query.normalized_limit();
    let rows = match sqlx::query(
        "SELECT tip.id::BIGINT AS id, tip.project_id::BIGINT AS project_id, tip.challenge_id::BIGINT AS challenge_id, \
                tip.user_id::BIGINT AS sender_user_id, sender.username AS sender_username, project.title AS project_title, \
                tip.quota::BIGINT AS quota, tip.note, tip.recipient_read_at::BIGINT AS recipient_read_at, \
                tip.thanked_at::BIGINT AS thanked_at, tip.created_at::BIGINT AS created_at \
         FROM open_source_bounty_ledgers tip \
         JOIN users sender ON sender.id = tip.user_id AND sender.deleted_at IS NULL \
         JOIN open_source_bounty_projects project ON project.id = tip.project_id \
         WHERE tip.kind = 'tip_transfer' AND tip.counterparty_user_id = $1 \
         ORDER BY tip.created_at DESC, tip.id DESC LIMIT $2",
    )
    .bind(viewer_id)
    .bind(limit)
    .fetch_all(&state.pg)
    .await
    {
        Ok(rows) => rows,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, "failed to list open-source bounty tip notifications");
            return internal_failure();
        }
    };
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        match tip_notification_from_row(&row) {
            Ok(item) => items.push(item),
            Err(error) => {
                tracing::error!(error = %error, viewer_id, "failed to decode open-source bounty tip notification");
                return internal_failure();
            }
        }
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: items,
    })
    .into_response();
    add_auth_version(&mut response);
    response
}

async fn mark_tip_notifications_read(
    State(state): State<OpenSourceBountyState>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    let now = chrono::Utc::now().timestamp();
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_ledgers \
         SET recipient_read_at = $1 \
         WHERE kind = 'tip_transfer' AND counterparty_user_id = $2 AND recipient_read_at = 0",
    )
    .bind(now)
    .bind(viewer_id)
    .execute(&state.pg)
    .await
    {
        tracing::error!(error = %error, viewer_id, "failed to mark open-source bounty tip notifications read");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: Value::Null,
    })
    .into_response();
    add_auth_version(&mut response);
    response
}

async fn thank_tip(
    State(state): State<OpenSourceBountyState>,
    Path(tip_id): Path<i64>,
    headers: HeaderMap,
) -> Response {
    let viewer_id = match required_viewer_id(&state, &headers).await {
        Ok(viewer_id) => viewer_id,
        Err(response) => return response,
    };
    if tip_id <= 0 {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_INVALID_ID",
            "invalid open-source bounty tip identifier",
        );
    }
    let now = chrono::Utc::now().timestamp();
    let mut transaction = match state.pg.begin().await {
        Ok(transaction) => transaction,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, tip_id, "failed to begin bounty tip acknowledgement");
            return internal_failure();
        }
    };
    if let Err(error) = sqlx::query(
        "UPDATE open_source_bounty_ledgers SET thanked_at = $1, recipient_read_at = $1 \
         WHERE id = $2 AND kind = 'tip_transfer' AND counterparty_user_id = $3 AND thanked_at = 0",
    )
    .bind(now)
    .bind(tip_id)
    .bind(viewer_id)
    .execute(&mut *transaction)
    .await
    {
        tracing::error!(error = %error, viewer_id, tip_id, "failed to acknowledge bounty tip");
        return internal_failure();
    }
    let row = match sqlx::query(
        "SELECT tip.id::BIGINT AS id, tip.project_id::BIGINT AS project_id, tip.challenge_id::BIGINT AS challenge_id, \
                tip.user_id::BIGINT AS sender_user_id, sender.username AS sender_username, project.title AS project_title, \
                tip.quota::BIGINT AS quota, tip.note, tip.recipient_read_at::BIGINT AS recipient_read_at, \
                tip.thanked_at::BIGINT AS thanked_at, tip.created_at::BIGINT AS created_at \
         FROM open_source_bounty_ledgers tip \
         JOIN users sender ON sender.id = tip.user_id AND sender.deleted_at IS NULL \
         JOIN open_source_bounty_projects project ON project.id = tip.project_id \
         WHERE tip.id = $1 AND tip.kind = 'tip_transfer' AND tip.counterparty_user_id = $2",
    )
    .bind(tip_id)
    .bind(viewer_id)
    .fetch_optional(&mut *transaction)
    .await
    {
        Ok(row) => row,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, tip_id, "failed to load acknowledged bounty tip");
            return internal_failure();
        }
    };
    let Some(row) = row else {
        return business_failure(
            "OPEN_SOURCE_BOUNTY_TIP_NOT_FOUND",
            "tip notification was not found",
        );
    };
    let notification = match tip_notification_from_row(&row) {
        Ok(notification) => notification,
        Err(error) => {
            tracing::error!(error = %error, viewer_id, tip_id, "failed to decode acknowledged bounty tip");
            return internal_failure();
        }
    };
    if let Err(error) = transaction.commit().await {
        tracing::error!(error = %error, viewer_id, tip_id, "failed to commit bounty tip acknowledgement");
        return internal_failure();
    }
    let mut response = Json(LegacySuccessEnvelope {
        success: true,
        message: "",
        data: notification,
    })
    .into_response();
    add_auth_version(&mut response);
    response
}

#[cfg(test)]
mod tests {
    use std::{sync::Arc, time::Duration};

    use async_trait::async_trait;
    use axum::{body::Body, http::Request};
    use base64::Engine;
    use secrecy::SecretString;
    use sqlx::postgres::PgPoolOptions;
    use tower::ServiceExt;

    use super::{
        BountyFeeConfig, DEFAULT_PAGE_SIZE, DisputeListQuery, ListQuery, MAX_PAGE_SIZE,
        NotificationQuery, OpenSourceBountyState, OwnedQuery, archive_status_is_final,
        challenge_priority, dashboard_token_candidate, parse_fee_rate_basis_points, router,
    };
    use crate::auth::{
        AuthBundle, AuthConfig, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardSelfUserFacts, DashboardUser, DashboardUserView, LoginOutcome, LoginRequest,
        LogoutRequest, LogoutResult, PgValkeyDashboardAuth, RequestMetadata, TwoFactorLoginRequest,
    };

    #[derive(Clone, Copy)]
    struct StaticBountyAuth {
        developer_access_granted: bool,
    }

    fn static_dashboard_user() -> DashboardUser {
        DashboardUser {
            id: 7,
            username: "bounty-user".to_owned(),
            display_name: "Bounty User".to_owned(),
            role: 1,
            status: 1,
            email: "bounty@example.test".to_owned(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: serde_json::json!({}),
            permissions: serde_json::json!({}),
        }
    }

    #[async_trait]
    impl DashboardAuth for StaticBountyAuth {
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
            Ok(static_dashboard_user())
        }

        async fn self_user_view_for_optional(
            &self,
            _: SecretString,
        ) -> Result<DashboardUserView, AuthError> {
            Ok(DashboardUserView::build(
                static_dashboard_user(),
                DashboardSelfUserFacts {
                    paid_activation_complete: self.developer_access_granted,
                    ..DashboardSelfUserFacts::default()
                },
            ))
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

    fn access_test_router(developer_access_granted: bool) -> axum::Router {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(10))
            .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
            .expect("lazy PostgreSQL pool");
        router(OpenSourceBountyState::new(
            pg,
            Arc::new(StaticBountyAuth {
                developer_access_granted,
            }),
        ))
    }

    fn archive_test_router() -> axum::Router {
        let pg = PgPoolOptions::new()
            .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
            .expect("lazy PostgreSQL pool");
        let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
        let auth_config = AuthConfig {
            session_secret: SecretString::from(
                "open-source-bounty-archive-route-test-secret-012345678901234567890123456789",
            ),
            ..AuthConfig::default()
        };
        let auth = Arc::new(
            PgValkeyDashboardAuth::new(pg.clone(), valkey, auth_config)
                .expect("route-test auth adapter"),
        );
        router(OpenSourceBountyState::new(pg, auth))
    }

    #[test]
    fn list_query_matches_go_defaults_and_bounds() {
        assert_eq!(
            ListQuery {
                page: None,
                page_size: None
            }
            .normalized(),
            (1, DEFAULT_PAGE_SIZE)
        );
        assert_eq!(
            ListQuery {
                page: Some("0".to_owned()),
                page_size: Some("0".to_owned())
            }
            .normalized(),
            (1, DEFAULT_PAGE_SIZE)
        );
        assert_eq!(
            ListQuery {
                page: Some("3".to_owned()),
                page_size: Some(MAX_PAGE_SIZE.to_string())
            }
            .normalized(),
            (3, MAX_PAGE_SIZE)
        );
        assert_eq!(
            ListQuery {
                page: Some("-4".to_owned()),
                page_size: Some((MAX_PAGE_SIZE + 1).to_string())
            }
            .normalized(),
            (1, DEFAULT_PAGE_SIZE)
        );
    }

    #[test]
    fn only_dashboard_access_tokens_are_classified_as_internal() {
        let encode = |value: &str| base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(value);
        let header = encode(r#"{"alg":"HS256"}"#);
        let dashboard =
            encode(r#"{"iss":"new-api","aud":["new-api-dashboard"],"token_use":"access"}"#);
        let proof =
            encode(r#"{"iss":"new-api","aud":["new-api-dashboard"],"token_use":"security_proof"}"#);
        let external = encode(r#"{"iss":"external","aud":["other"],"token_use":"access"}"#);
        assert!(dashboard_token_candidate(&format!(
            "{header}.{dashboard}.sig"
        )));
        assert!(dashboard_token_candidate(&format!("{header}.{proof}.sig")));
        assert!(!dashboard_token_candidate(&format!(
            "{header}.{external}.sig"
        )));
    }

    #[test]
    fn viewer_challenge_priority_matches_go() {
        assert_eq!(challenge_priority("approved"), 4);
        assert_eq!(challenge_priority("accepted"), 3);
        assert_eq!(challenge_priority("submitted"), 3);
        assert_eq!(challenge_priority("rejected"), 2);
        assert_eq!(challenge_priority("withdrawn"), 1);
        assert_eq!(challenge_priority("cancelled"), 1);
        assert_eq!(challenge_priority("draft"), 0);
    }

    #[test]
    fn fee_rate_parser_matches_go_basis_points_contract() {
        assert_eq!(parse_fee_rate_basis_points("0"), Some(0));
        assert_eq!(parse_fee_rate_basis_points("1"), Some(100));
        assert_eq!(parse_fee_rate_basis_points("2.5"), Some(250));
        assert_eq!(parse_fee_rate_basis_points("100.00"), Some(10_000));
        for value in ["", "001", "100.01", "101", "1.234", "-1"] {
            assert_eq!(parse_fee_rate_basis_points(value), None, "value={value}");
        }
    }

    #[test]
    fn fee_rate_json_matches_go_integral_float_wire_shape() {
        let integral = serde_json::to_string(&BountyFeeConfig {
            rate_percent: 1.0,
            rate_basis_points: 100,
        })
        .unwrap();
        assert_eq!(integral, r#"{"rate_percent":1,"rate_basis_points":100}"#);

        let fractional = serde_json::to_string(&BountyFeeConfig {
            rate_percent: 2.5,
            rate_basis_points: 250,
        })
        .unwrap();
        assert_eq!(
            fractional,
            r#"{"rate_percent":2.5,"rate_basis_points":250}"#
        );
    }

    #[test]
    fn dispute_list_query_matches_go_filter_and_limit_bounds() {
        let query = DisputeListQuery {
            status: Some(" resolved_paid ".to_owned()),
            limit: Some("101".to_owned()),
        };
        assert_eq!(query.normalized().unwrap(), (Some("resolved_paid"), 100));
        let default = DisputeListQuery {
            status: Some(" ".to_owned()),
            limit: Some("not-a-number".to_owned()),
        };
        assert_eq!(default.normalized().unwrap(), (None, 50));
        let invalid = DisputeListQuery {
            status: Some("pending".to_owned()),
            limit: None,
        };
        assert!(invalid.normalized().is_err());
    }

    #[test]
    fn notification_limit_matches_go_default_and_bounds() {
        assert_eq!(NotificationQuery { limit: None }.normalized_limit(), 50);
        assert_eq!(
            NotificationQuery {
                limit: Some("100".to_owned())
            }
            .normalized_limit(),
            100
        );
        for value in ["0", "-1", "101", "not-a-number"] {
            assert_eq!(
                NotificationQuery {
                    limit: Some(value.to_owned())
                }
                .normalized_limit(),
                50,
                "value={value}"
            );
        }
    }

    #[test]
    fn archive_status_accepts_only_completed_and_closed_projects() {
        assert!(archive_status_is_final("completed"));
        assert!(archive_status_is_final("closed"));
        for status in ["draft", "published", "paused", ""] {
            assert!(!archive_status_is_final(status), "status={status}");
        }
    }

    #[test]
    fn owned_query_matches_go_parse_bool_truthy_values() {
        for value in ["1", "t", "T", "TRUE", "true", "True"] {
            assert!(
                OwnedQuery {
                    archived: Some(value.to_owned())
                }
                .archived(),
                "value={value}"
            );
        }
    }

    #[test]
    fn owned_query_treats_invalid_or_false_values_as_active() {
        for value in ["0", "f", "F", "FALSE", "false", "False", "yes", ""] {
            assert!(
                !OwnedQuery {
                    archived: Some(value.to_owned())
                }
                .archived(),
                "value={value}"
            );
        }
        assert!(!OwnedQuery { archived: None }.archived());
    }

    #[tokio::test]
    async fn archive_route_requires_auth_before_project_lookup() {
        let response = archive_test_router()
            .oneshot(
                Request::post("/api/open-source-bounties/projects/7/archive")
                    .body(Body::empty())
                    .expect("route request"),
            )
            .await
            .expect("route response");

        assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn unarchive_route_requires_auth_before_project_lookup() {
        let response = archive_test_router()
            .oneshot(
                Request::post("/api/open-source-bounties/projects/7/unarchive")
                    .body(Body::empty())
                    .expect("route request"),
            )
            .await
            .expect("route response");

        assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn private_bounty_route_rejects_l0_and_reaches_handler_for_l1() {
        let l0 = access_test_router(false)
            .oneshot(
                Request::post("/api/open-source-bounties/projects/not-an-id/publish")
                    .header("authorization", "Bearer l0")
                    .body(Body::empty())
                    .expect("L0 request"),
            )
            .await
            .expect("L0 response");
        assert_eq!(l0.status(), axum::http::StatusCode::NOT_FOUND);

        let l1 = access_test_router(true)
            .oneshot(
                Request::post("/api/open-source-bounties/projects/not-an-id/publish")
                    .header("authorization", "Bearer l1")
                    .body(Body::empty())
                    .expect("L1 request"),
            )
            .await
            .expect("L1 response");
        assert_eq!(l1.status(), axum::http::StatusCode::OK);
    }
}
