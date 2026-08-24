//! Remaining Go-compatible assistant routes not yet on the core router.

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use axum::{
    Json, Router,
    extract::{Path, Query, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::Engine;
use hmac::{Hmac, Mac};
use secrecy::{ExposeSecret, SecretString};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::Sha256;
use sqlx::{PgPool, Row};

use crate::migration_routes::assistant::{
    AssistantReadState, api_error, assistant_error, assistant_session_required,
    authenticated_admin, authenticated_user, browser_user, success, with_no_store,
};

type HmacSha256 = Hmac<Sha256>;

const ASSISTANT_HISTORY_PRIVACY_NOTICE: &str = "你与助手的对话不是私密通信，请勿发送个人信息、密码、API Key、Cookie 或其他凭证。敏感内容会被自动脱敏。";
const ASSISTANT_GIFT_UNAVAILABLE: &str = "ASSISTANT_NEW_USER_GIFT_UNAVAILABLE";
const ASSISTANT_WEEKLY_UNAVAILABLE: &str = "ASSISTANT_WEEKLY_DISCOUNT_UNAVAILABLE";
const ADMIN_ROLE: i64 = 10;
const ROOT_ROLE: i64 = 100;
const ASSISTANT_HISTORY_PAGE_MAX: i64 = 100;
const ASSISTANT_REQUEST_REVIEW_PAGE_MAX: i64 = 100;

pub fn extended_router() -> Router<AssistantReadState> {
    Router::new()
        .route("/api/assistant/journey", get(get_journey))
        .route("/api/assistant/new-user-gift", get(get_new_user_gift))
        .route(
            "/api/assistant/new-user-gift/claim",
            post(claim_new_user_gift),
        )
        .route("/api/assistant/weekly-discount", get(get_weekly_discount))
        .route(
            "/api/assistant/weekly-discount/claim",
            post(claim_weekly_discount),
        )
        .route("/api/assistant/conversations", get(list_conversations))
        .route("/api/assistant/conversations/{id}", get(get_conversation))
        .route(
            "/api/assistant/conversations/{id}/archive",
            post(archive_conversation),
        )
        .route(
            "/api/assistant/conversations/{id}/unarchive",
            post(unarchive_conversation),
        )
        .route("/api/assistant/cards/{id}/reveal", get(reveal_secure_card))
        .route("/api/assistant/drawing/generate", post(generate_drawing))
        .route(
            "/api/assistant/pre-conversation-presets",
            get(list_prompt_presets),
        )
        .route(
            "/api/assistant/pre-conversation-presets/{id}/click",
            post(count_prompt_preset_click),
        )
        .route("/api/assistant/admin/apply", post(admin_apply))
        .route(
            "/api/assistant/admin/first-questions",
            get(admin_first_questions),
        )
        .route("/api/assistant/admin/profiles", get(admin_profiles))
        .route("/api/assistant/admin/funding", get(admin_funding))
        .route("/api/assistant/admin/review", get(admin_review))
        .route("/api/assistant/admin/review/run", post(admin_run_review))
        .route(
            "/api/assistant/admin/request-reviews",
            get(admin_request_reviews),
        )
        .route(
            "/api/assistant/admin/users/{user_id}/request-reviews/reset",
            post(admin_reset_request_reviews),
        )
}

#[derive(Debug, Deserialize)]
struct SummaryDaysQuery {
    days: Option<i64>,
}

#[derive(Debug, Deserialize)]
struct ConversationListQuery {
    user_id: Option<i64>,
    limit: Option<i64>,
    archived: Option<String>,
    cursor: Option<String>,
}

#[derive(Debug, Deserialize)]
struct ConversationHistoryQuery {
    limit: Option<i64>,
}

#[derive(Debug, Deserialize)]
struct RequestReviewsQuery {
    user_id: Option<i64>,
    page: Option<i64>,
    page_size: Option<i64>,
    violations_only: Option<String>,
}

#[derive(Debug, Deserialize)]
struct AdminApplyInput {
    confirmed: bool,
    confirmation_token: String,
}

#[derive(Debug, Deserialize)]
struct DrawingGenerateInput {
    confirmation_token: String,
}

async fn get_journey(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match load_journey(&state.pg, principal.user.id).await {
        Ok(journey) => with_no_store(success(json!(journey))),
        Err(message) => with_no_store(api_error(message)),
    }
}

async fn get_new_user_gift(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match load_new_user_gift(&state.pg, principal.user.id).await {
        Ok(gift) => with_no_store(success(json!(gift))),
        Err(message) => with_no_store(api_error(message)),
    }
}

async fn claim_new_user_gift(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match claim_new_user_gift_tx(&state.pg, principal.user.id, &principal.user.username).await {
        Ok((gift, already_claimed)) => with_no_store(success(json!({
            "gift": gift,
            "already_claimed": already_claimed,
        }))),
        Err(ClaimGiftError::Unavailable) => with_no_store(assistant_error(
            StatusCode::CONFLICT,
            ASSISTANT_GIFT_UNAVAILABLE,
            "new-user gift is not available",
        )),
        Err(ClaimGiftError::Internal(message)) => with_no_store(api_error(message)),
    }
}

async fn get_weekly_discount(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match load_weekly_discount(&state.pg, principal.user.id).await {
        Ok(discount) => with_no_store(success(json!(discount))),
        Err(message) => with_no_store(api_error(message)),
    }
}

async fn claim_weekly_discount(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match claim_weekly_discount_tx(&state.pg, principal.user.id).await {
        Ok((discount, already_claimed)) => with_no_store(success(json!({
            "discount": discount,
            "already_claimed": already_claimed,
        }))),
        Err(ClaimWeeklyError::Unavailable) => with_no_store(assistant_error(
            StatusCode::CONFLICT,
            ASSISTANT_WEEKLY_UNAVAILABLE,
            "weekly discount is not available",
        )),
        Err(ClaimWeeklyError::Internal(message)) => with_no_store(api_error(message)),
    }
}

async fn list_conversations(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Query(query): Query<ConversationListQuery>,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let owner_user_id = match query.user_id {
        Some(id) if id > 0 => id,
        Some(_) => {
            return with_no_store(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_HISTORY_INVALID_USER",
                "user_id must be a positive integer",
            ));
        }
        None => principal.user.id,
    };
    let limit = query.limit.unwrap_or(30);
    if limit < 1 {
        return with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_LIMIT",
            "limit must be a positive integer",
        ));
    }
    let archived = match query
        .archived
        .as_deref()
        .map(str::trim)
        .filter(|v| !v.is_empty())
    {
        None => false,
        Some(raw) => match raw.parse::<bool>() {
            Ok(value) => value,
            Err(_) => {
                return with_no_store(assistant_error(
                    StatusCode::BAD_REQUEST,
                    "ASSISTANT_HISTORY_INVALID_ARCHIVED",
                    "archived must be a boolean",
                ));
            }
        },
    };
    match list_conversations_page(
        &state,
        principal.user.id,
        owner_user_id,
        limit,
        archived,
        query.cursor.as_deref(),
    )
    .await
    {
        Ok(page) => with_no_store(success(json!({
            "conversations": page.conversations,
            "next_cursor": page.next_cursor,
            "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
        }))),
        Err(HistoryError::NotFound) => with_no_store(assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_HISTORY_NOT_FOUND",
            "assistant conversation was not found",
        )),
        Err(HistoryError::InvalidCursor) => with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_CURSOR",
            "cursor is invalid or expired",
        )),
        Err(HistoryError::InvalidUser) => with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_USER",
            "user_id must be a positive integer",
        )),
        Err(HistoryError::InvalidLimit) => with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_LIMIT",
            "limit must be a positive integer",
        )),
        Err(HistoryError::Internal(message)) => with_no_store(api_error(message)),
        Err(other) => with_no_store(history_error(other)),
    }
}

async fn get_conversation(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Path(id): Path<String>,
    Query(query): Query<ConversationHistoryQuery>,
) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let conversation_id = match id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_no_store(assistant_error(
                StatusCode::NOT_FOUND,
                "ASSISTANT_HISTORY_NOT_FOUND",
                "assistant conversation was not found",
            ));
        }
    };
    let limit = query.limit.unwrap_or(100);
    if limit < 1 {
        return with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_LIMIT",
            "limit must be a positive integer",
        ));
    }
    match load_conversation_history(&state.pg, principal.user.id, conversation_id, limit).await {
        Ok((conversation, messages)) => with_no_store(success(json!({
            "conversation": conversation,
            "messages": messages,
            "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
        }))),
        Err(HistoryError::NotFound) => with_no_store(assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_HISTORY_NOT_FOUND",
            "assistant conversation was not found",
        )),
        Err(HistoryError::Internal(message)) => with_no_store(api_error(message)),
        Err(other) => with_no_store(history_error(other)),
    }
}

async fn archive_conversation(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    set_conversation_archived(&state, &headers, &id, true).await
}

async fn unarchive_conversation(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Path(id): Path<String>,
) -> Response {
    set_conversation_archived(&state, &headers, &id, false).await
}

async fn set_conversation_archived(
    state: &AssistantReadState,
    headers: &HeaderMap,
    raw_id: &str,
    archived: bool,
) -> Response {
    let principal = match authenticated_user(state, headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let conversation_id = match raw_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_no_store(assistant_error(
                StatusCode::NOT_FOUND,
                "ASSISTANT_HISTORY_NOT_FOUND",
                "assistant conversation was not found",
            ));
        }
    };
    match update_conversation_archive(&state.pg, principal.user.id, conversation_id, archived).await
    {
        Ok(row) => with_no_store(success(json!({
            "id": row.id,
            "archived": row.archived_at != 0,
            "archived_at": row.archived_at,
        }))),
        Err(HistoryError::NotFound) => with_no_store(assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_HISTORY_NOT_FOUND",
            "assistant conversation was not found",
        )),
        Err(HistoryError::AlreadyArchived) => with_no_store(assistant_error(
            StatusCode::CONFLICT,
            "ASSISTANT_CONVERSATION_ALREADY_ARCHIVED",
            "assistant conversation is already archived",
        )),
        Err(HistoryError::NotArchived) => with_no_store(assistant_error(
            StatusCode::CONFLICT,
            "ASSISTANT_CONVERSATION_NOT_ARCHIVED",
            "assistant conversation is not archived",
        )),
        Err(HistoryError::Internal(message)) => with_no_store(api_error(message)),
        Err(other) => with_no_store(history_error(other)),
    }
}

async fn reveal_secure_card(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Path(card_id): Path<String>,
) -> Response {
    if browser_user(&state, &headers).await.is_err() {
        return with_no_store(assistant_session_required());
    }
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    match reveal_card(&state.pg, principal.user.id, card_id.trim()).await {
        Ok((card, payload)) => with_no_store(success(json!({
            "card": card,
            "payload": payload,
            "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
        }))),
        Err(RevealError::NotFound) => with_no_store(assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_SECURE_CARD_NOT_FOUND",
            "secure card was not found",
        )),
        Err(RevealError::Consumed) => with_no_store(assistant_error(
            StatusCode::GONE,
            "ASSISTANT_SECURE_CARD_CONSUMED",
            "secure card has already been revealed",
        )),
        Err(RevealError::Expired) => with_no_store(assistant_error(
            StatusCode::GONE,
            "ASSISTANT_SECURE_CARD_EXPIRED",
            "secure card has expired",
        )),
        Err(RevealError::Invalid) => with_no_store(assistant_error(
            StatusCode::INTERNAL_SERVER_ERROR,
            "ASSISTANT_SECURE_CARD_INVALID",
            "secure card could not be decoded",
        )),
        Err(RevealError::Internal(message)) => with_no_store(api_error(message)),
    }
}

async fn generate_drawing(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Json(input): Json<DrawingGenerateInput>,
) -> Response {
    if browser_user(&state, &headers).await.is_err() {
        return with_no_store(
            (
                StatusCode::FORBIDDEN,
                Json(json!({"error": {"message": "browser authentication is required"}})),
            )
                .into_response(),
        );
    }
    if input.confirmation_token.trim().is_empty() {
        return with_no_store(
            (
                StatusCode::BAD_REQUEST,
                Json(json!({"error": {"message": "a drawing confirmation token is required"}})),
            )
                .into_response(),
        );
    }
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let session = match state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
    {
        Ok(session) => session,
        Err(_) => return with_no_store(assistant_session_required()),
    };
    match consume_auth_flow(
        &state.pg,
        &state.session_secret,
        input.confirmation_token.trim(),
        "assistant_drawing_generation",
        principal.user.id,
        &session.session_id,
    )
    .await
    {
        Ok(Some(_payload)) => with_no_store(
            (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(json!({"error": {"message": "drawing relay is not configured on this listener"}})),
            )
                .into_response(),
        ),
        Ok(None) => with_no_store(
            (
                StatusCode::UNPROCESSABLE_ENTITY,
                Json(json!({"error": {"message": "image confirmation is invalid or expired; ask the assistant to prepare it again"}})),
            )
                .into_response(),
        ),
        Err(AuthFlowError::Consumed) => with_no_store(
            (
                StatusCode::CONFLICT,
                Json(json!({"error": {"message": "image confirmation is invalid or expired; ask the assistant to prepare it again"}})),
            )
                .into_response(),
        ),
        Err(AuthFlowError::Internal(message)) => with_no_store(api_error(message)),
    }
}

async fn list_prompt_presets(State(state): State<AssistantReadState>) -> Response {
    match load_prompt_presets(&state.pg).await {
        Ok(presets) => {
            let mut response = success(json!(presets));
            response.headers_mut().insert(
                header::CACHE_CONTROL,
                axum::http::HeaderValue::from_static("public, max-age=300"),
            );
            response
        }
        Err(message) => api_error(message),
    }
}

async fn count_prompt_preset_click(
    State(state): State<AssistantReadState>,
    Path(preset_id): Path<String>,
) -> Response {
    match increment_preset_click(&state.pg, preset_id.trim()).await {
        Ok(()) => success(json!(null)),
        Err(PresetError::NotFound) => assistant_error(
            StatusCode::NOT_FOUND,
            "ASSISTANT_PRESET_NOT_FOUND",
            "assistant preset was not found",
        ),
        Err(PresetError::Internal(message)) => api_error(message),
    }
}

async fn admin_apply(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Json(input): Json<AdminApplyInput>,
) -> Response {
    let principal = match authenticated_admin(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    if !input.confirmed || input.confirmation_token.trim().is_empty() {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_ADMIN_CONFIRMATION_REQUIRED",
            "explicit confirmation of the administrator preview is required",
        ));
    }
    let session = match state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
    {
        Ok(session) => session,
        Err(_) => {
            return with_no_store(assistant_error(
                StatusCode::FORBIDDEN,
                "ASSISTANT_ADMIN_SESSION_REQUIRED",
                "a browser login session is required for administrator changes",
            ));
        }
    };
    match consume_auth_flow(
        &state.pg,
        &state.session_secret,
        input.confirmation_token.trim(),
        "assistant_admin_change",
        principal.user.id,
        &session.session_id,
    )
    .await
    {
        Ok(Some(_payload)) => with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_ADMIN_CHANGE_FAILED",
            "administrator preview could not be applied on this listener",
        )),
        Ok(None) => with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_ADMIN_CONFIRMATION_INVALID",
            "administrator preview is invalid or expired; ask the assistant to prepare it again",
        )),
        Err(AuthFlowError::Consumed) => with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_ADMIN_CONFIRMATION_INVALID",
            "administrator preview is invalid or expired; ask the assistant to prepare it again",
        )),
        Err(AuthFlowError::Internal(message)) => with_no_store(api_error(message)),
    }
}

async fn admin_first_questions(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Query(query): Query<SummaryDaysQuery>,
) -> Response {
    if authenticated_admin(&state, &headers).await.is_err() {
        return admin_required(&state, &headers).await;
    }
    let since = match summary_since(query.days, "ASSISTANT_FIRST_QUESTION_DAYS_INVALID") {
        Ok(since) => since,
        Err(response) => return response,
    };
    match load_first_question_summary(&state.pg, since).await {
        Ok(data) => success(json!(data)),
        Err(message) => api_error(message),
    }
}

async fn admin_profiles(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Query(query): Query<SummaryDaysQuery>,
) -> Response {
    if authenticated_admin(&state, &headers).await.is_err() {
        return admin_required(&state, &headers).await;
    }
    let since = match summary_since(query.days, "ASSISTANT_PROFILE_DAYS_INVALID") {
        Ok(since) => since,
        Err(response) => return response,
    };
    match load_profile_summary(&state.pg, since).await {
        Ok(data) => success(json!(data)),
        Err(message) => api_error(message),
    }
}

async fn admin_funding(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Query(query): Query<SummaryDaysQuery>,
) -> Response {
    if authenticated_admin(&state, &headers).await.is_err() {
        return admin_required(&state, &headers).await;
    }
    let since = match summary_since(query.days, "ASSISTANT_FUNDING_DAYS_INVALID") {
        Ok(since) => since,
        Err(response) => return response,
    };
    match load_funding_summary(&state.pg, since).await {
        Ok(data) => success(json!(data)),
        Err(FundingError::BillingUnavailable) => assistant_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_BILLING_ACCOUNT_UNAVAILABLE",
            "AI assistant billing account is unavailable",
        ),
        Err(FundingError::Internal(message)) => api_error(message),
    }
}

async fn admin_review(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    if authenticated_admin(&state, &headers).await.is_err() {
        return admin_required(&state, &headers).await;
    }
    match load_latest_review_task(&state.pg).await {
        Ok(task) => with_no_store(success(json!(task))),
        Err(message) => with_no_store(api_error(message)),
    }
}

async fn admin_run_review(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    if authenticated_admin(&state, &headers).await.is_err() {
        return admin_required(&state, &headers).await;
    }
    with_no_store(api_error(
        "assistant review runner is not configured on this listener".to_owned(),
    ))
}

async fn admin_request_reviews(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Query(query): Query<RequestReviewsQuery>,
) -> Response {
    let principal = match authenticated_admin(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let user_id = match query.user_id {
        Some(id) if id > 0 => id,
        _ => {
            return with_no_store(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_REVIEW_USER_INVALID",
                "user_id must be a positive integer",
            ));
        }
    };
    if !can_manage_target(&state.pg, principal.user.id, principal.user.role, user_id).await {
        return with_no_store(api_error(
            "you do not have permission to manage this user".to_owned(),
        ));
    }
    let page = query.page.unwrap_or(1);
    if page < 1 {
        return with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_REVIEW_PAGE_INVALID",
            "page must be a positive integer",
        ));
    }
    let page_size = query.page_size.unwrap_or(ASSISTANT_REQUEST_REVIEW_PAGE_MAX);
    if !(1..=ASSISTANT_REQUEST_REVIEW_PAGE_MAX).contains(&page_size) {
        return with_no_store(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_REVIEW_PAGE_SIZE_INVALID",
            "page_size must be between 1 and 100",
        ));
    }
    let violations_only = query
        .violations_only
        .as_deref()
        .is_some_and(|value| value.eq_ignore_ascii_case("true"));
    match load_request_reviews(&state.pg, user_id, violations_only, page, page_size).await {
        Ok(data) => with_no_store(success(json!(data))),
        Err(message) => with_no_store(api_error(message)),
    }
}

async fn admin_reset_request_reviews(
    State(state): State<AssistantReadState>,
    headers: HeaderMap,
    Path(user_id): Path<String>,
) -> Response {
    let principal = match authenticated_admin(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return with_no_store(response),
    };
    let target_id = match user_id.parse::<i64>() {
        Ok(id) if id > 0 => id,
        _ => {
            return with_no_store(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_REVIEW_USER_INVALID",
                "user_id must be a positive integer",
            ));
        }
    };
    if !can_manage_target(&state.pg, principal.user.id, principal.user.role, target_id).await {
        return with_no_store(api_error(
            "you do not have permission to manage this user".to_owned(),
        ));
    }
    let now = unix_seconds();
    if let Err(error) = sqlx::query(
        "UPDATE assistant_request_review_violations SET violation_count = 0, reset_at = $2 \
         WHERE user_id = $1",
    )
    .bind(target_id)
    .bind(now)
    .execute(&state.pg)
    .await
    {
        return with_no_store(api_error(error.to_string()));
    }
    with_no_store(success(json!({
        "user_id": target_id,
        "violation_count": 0,
        "reset_at": now,
    })))
}

async fn admin_summary<F, Fut>(
    state: &AssistantReadState,
    headers: &HeaderMap,
    days: Option<i64>,
    days_code: &'static str,
    loader: F,
) -> Response
where
    F: FnOnce(&PgPool, i64) -> Fut,
    Fut: std::future::Future<Output = Result<Value, String>>,
{
    if authenticated_admin(state, headers).await.is_err() {
        return admin_required(state, headers).await;
    }
    let since = match summary_since(days, days_code) {
        Ok(since) => since,
        Err(response) => return response,
    };
    match loader(&state.pg, since).await {
        Ok(data) => success(json!(data)),
        Err(message) => api_error(message),
    }
}

async fn admin_required(state: &AssistantReadState, headers: &HeaderMap) -> Response {
    match authenticated_admin(state, headers).await {
        Ok(_) => success(json!(null)),
        Err(response) => response,
    }
}

fn summary_since(days: Option<i64>, code: &'static str) -> Result<i64, Response> {
    let days = days.unwrap_or(30);
    if !(1..=365).contains(&days) {
        return Err(assistant_error(
            StatusCode::BAD_REQUEST,
            code,
            "days must be between 1 and 365",
        ));
    }
    Ok(unix_seconds() - days * 24 * 60 * 60)
}

fn history_error(error: HistoryError) -> Response {
    match error {
        HistoryError::InvalidUser => assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_USER",
            "user_id must be a positive integer",
        ),
        HistoryError::InvalidLimit => assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_LIMIT",
            "limit must be a positive integer",
        ),
        HistoryError::InvalidCursor => assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HISTORY_INVALID_CURSOR",
            "cursor is invalid or expired",
        ),
        other => api_error(other.to_string()),
    }
}

#[derive(Debug)]
enum HistoryError {
    NotFound,
    Forbidden,
    AlreadyArchived,
    NotArchived,
    InvalidUser,
    InvalidLimit,
    InvalidCursor,
    Internal(String),
}

impl std::fmt::Display for HistoryError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Internal(message) => write!(f, "{message}"),
            _ => write!(f, "{self:?}"),
        }
    }
}

#[derive(Debug)]
enum ClaimGiftError {
    Unavailable,
    Internal(String),
}

#[derive(Debug)]
enum ClaimWeeklyError {
    Unavailable,
    Internal(String),
}

#[derive(Debug)]
enum RevealError {
    NotFound,
    Consumed,
    Expired,
    Invalid,
    Internal(String),
}

#[derive(Debug)]
enum PresetError {
    NotFound,
    Internal(String),
}

#[derive(Debug)]
enum FundingError {
    BillingUnavailable,
    Internal(String),
}

#[derive(Debug)]
enum AuthFlowError {
    Consumed,
    Internal(String),
}

#[derive(Serialize)]
struct JourneyStep {
    id: String,
    status: String,
}

#[derive(Serialize)]
struct Journey {
    main: Vec<JourneyStep>,
    side: Vec<JourneyStep>,
}

#[derive(Serialize)]
struct ConversationPage {
    conversations: Vec<Value>,
    next_cursor: Option<String>,
}

#[derive(Serialize)]
struct ArchiveRow {
    id: i64,
    archived_at: i64,
}

async fn load_journey(pg: &PgPool, user_id: i64) -> Result<Journey, String> {
    let conversations: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM assistant_conversations WHERE user_id = $1")
            .bind(user_id)
            .fetch_one(pg)
            .await
            .map_err(|error| error.to_string())?;
    let recommendation: Option<String> = sqlx::query_scalar(
        "SELECT COALESCE(ai_recommendation, '') FROM developer_access_requests \
         WHERE user_id = $1 ORDER BY id DESC LIMIT 1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?
    .filter(|value: &String| !value.trim().is_empty());
    let onboarding_state = load_onboarding_step_state(pg, user_id).await?;
    let bounty_count: i64 = sqlx::query_scalar(
        "SELECT COUNT(*) FROM open_source_bounty_challenges WHERE participant_user_id = $1",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|error| error.to_string())?;
    let gift_status = load_new_user_gift(pg, user_id)
        .await
        .ok()
        .flatten()
        .and_then(|value| {
            value
                .get("status")
                .and_then(Value::as_str)
                .map(str::to_owned)
        });
    Ok(Journey {
        main: vec![
            journey_step("ask_ai", conversations > 0),
            journey_step(
                "get_recommendation",
                recommendation
                    .as_ref()
                    .is_some_and(|v| !v.trim().is_empty()),
            ),
            journey_step(
                "create_api_key",
                *onboarding_state.get("create_api_key").unwrap_or(&false),
            ),
            journey_step(
                "install_client",
                *onboarding_state.get("install_client").unwrap_or(&false),
            ),
            journey_step(
                "configure_client",
                *onboarding_state.get("configure_client").unwrap_or(&false),
            ),
            journey_step(
                "first_api_call",
                *onboarding_state
                    .get("first_successful_response")
                    .unwrap_or(&false),
            ),
        ],
        side: vec![
            gift_journey_step(gift_status.as_deref()),
            journey_step("accept_bounty", bounty_count > 0),
        ],
    })
}

fn journey_step(id: &str, completed: bool) -> JourneyStep {
    JourneyStep {
        id: id.to_owned(),
        status: if completed {
            "completed".to_owned()
        } else {
            "pending".to_owned()
        },
    }
}

fn gift_journey_step(status: Option<&str>) -> JourneyStep {
    let status = match status {
        Some("offered") | Some("claimed") => "completed",
        Some("declined") => "failed",
        _ => "pending",
    };
    JourneyStep {
        id: "earn_ai_gift".to_owned(),
        status: status.to_owned(),
    }
}

async fn load_new_user_gift(pg: &PgPool, user_id: i64) -> Result<Option<Value>, String> {
    let row = sqlx::query(
        "SELECT id, conversation_id, amount_cents, quota, status, reason, created_at, claimed_at \
         FROM assistant_new_user_gifts WHERE user_id = $1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?;
    Ok(row.map(|row| {
        json!({
            "id": row.try_get::<i64, _>("id").unwrap_or_default(),
            "conversation_id": row.try_get::<i64, _>("conversation_id").unwrap_or_default(),
            "amount_cents": row.try_get::<i32, _>("amount_cents").unwrap_or_default(),
            "quota": row.try_get::<i32, _>("quota").unwrap_or_default(),
            "status": row.try_get::<String, _>("status").unwrap_or_default(),
            "reason": row.try_get::<String, _>("reason").unwrap_or_default(),
            "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
            "claimed_at": row.try_get::<i64, _>("claimed_at").unwrap_or_default(),
        })
    }))
}

async fn claim_new_user_gift_tx(
    pg: &PgPool,
    user_id: i64,
    username: &str,
) -> Result<(Value, bool), ClaimGiftError> {
    let mut tx = pg
        .begin()
        .await
        .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    let row = sqlx::query(
        "SELECT id, amount_cents, quota, status FROM assistant_new_user_gifts \
         WHERE user_id = $1 FOR UPDATE",
    )
    .bind(user_id)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    let Some(row) = row else {
        return Err(ClaimGiftError::Unavailable);
    };
    let status: String = row
        .try_get("status")
        .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    if status != "offered" {
        if status == "claimed" {
            let gift = load_new_user_gift(pg, user_id)
                .await
                .map_err(ClaimGiftError::Internal)?
                .unwrap_or(Value::Null);
            return Ok((gift, true));
        }
        return Err(ClaimGiftError::Unavailable);
    }
    let quota: i32 = row
        .try_get("quota")
        .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    let claimed_at = unix_seconds();
    sqlx::query(
        "UPDATE assistant_new_user_gifts SET status = 'claimed', claimed_at = $2 WHERE user_id = $1",
    )
    .bind(user_id)
    .bind(claimed_at)
    .execute(&mut *tx)
    .await
    .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    sqlx::query("UPDATE users SET quota = COALESCE(quota, 0) + $2 WHERE id = $1")
        .bind(user_id)
        .bind(i64::from(quota))
        .execute(&mut *tx)
        .await
        .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    tx.commit()
        .await
        .map_err(|error| ClaimGiftError::Internal(error.to_string()))?;
    let _ = sqlx::query(
        "INSERT INTO logs (user_id, created_at, type, content, username, request_id) \
         VALUES ($1, $2, 3, $3, $4, $5)",
    )
    .bind(user_id)
    .bind(claimed_at)
    .bind(format!("领取 AI 新用户礼包，获得额度 {quota}"))
    .bind(username)
    .bind(uuid::Uuid::new_v4().to_string())
    .execute(pg)
    .await;
    let gift = load_new_user_gift(pg, user_id)
        .await
        .map_err(ClaimGiftError::Internal)?
        .unwrap_or(Value::Null);
    Ok((gift, false))
}

async fn load_weekly_discount(pg: &PgPool, user_id: i64) -> Result<Option<Value>, String> {
    let week_start = assistant_week_start();
    let row = sqlx::query(
        "SELECT id, week_start, conversation_id, discount_percent, status, reason, created_at, \
         claimed_at, code_id FROM assistant_weekly_discounts \
         WHERE user_id = $1 AND week_start = $2",
    )
    .bind(user_id)
    .bind(week_start)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?;
    let Some(row) = row else {
        return Ok(None);
    };
    let mut value = json!({
        "id": row.try_get::<i64, _>("id").unwrap_or_default(),
        "week_start": row.try_get::<i64, _>("week_start").unwrap_or_default(),
        "conversation_id": row.try_get::<i64, _>("conversation_id").unwrap_or_default(),
        "discount_percent": row.try_get::<i32, _>("discount_percent").unwrap_or_default(),
        "status": row.try_get::<String, _>("status").unwrap_or_default(),
        "reason": row.try_get::<String, _>("reason").unwrap_or_default(),
        "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
        "claimed_at": row.try_get::<i64, _>("claimed_at").unwrap_or_default(),
    });
    if row.try_get::<String, _>("status").unwrap_or_default() == "claimed"
        && let Ok(code_id) = row.try_get::<i32, _>("code_id")
        && code_id > 0
        && let Ok(code) = sqlx::query_scalar::<_, String>(
            "SELECT code FROM discount_codes WHERE id = $1 AND owner_user_id = $2",
        )
        .bind(code_id)
        .bind(user_id)
        .fetch_optional(pg)
        .await
        && let Some(code) = code
    {
        value["code"] = json!(code);
    }
    Ok(Some(value))
}

async fn claim_weekly_discount_tx(
    pg: &PgPool,
    user_id: i64,
) -> Result<(Value, bool), ClaimWeeklyError> {
    let week_start = assistant_week_start();
    let mut tx = pg
        .begin()
        .await
        .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    let row = sqlx::query(
        "SELECT id, discount_percent, status FROM assistant_weekly_discounts \
         WHERE user_id = $1 AND week_start = $2 FOR UPDATE",
    )
    .bind(user_id)
    .bind(week_start)
    .fetch_optional(&mut *tx)
    .await
    .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    let Some(row) = row else {
        return Err(ClaimWeeklyError::Unavailable);
    };
    let status: String = row
        .try_get("status")
        .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    if status != "offered" {
        if status == "claimed" {
            let discount = load_weekly_discount(pg, user_id)
                .await
                .map_err(ClaimWeeklyError::Internal)?
                .unwrap_or(Value::Null);
            return Ok((discount, true));
        }
        return Err(ClaimWeeklyError::Unavailable);
    }
    let discount_percent: i32 = row
        .try_get("discount_percent")
        .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    let claimed_at = unix_seconds();
    let code = format!("AWD-{user_id}-{week_start}-{claimed_at}");
    let code_id: i32 = sqlx::query_scalar(
        "INSERT INTO discount_codes (code, owner_user_id, discount_percent, status, created_time, updated_time, starts_time, expired_time) \
         VALUES ($1, $2, $3, 1, $4, $4, $4, $5) RETURNING id",
    )
    .bind(&code)
    .bind(user_id)
    .bind(discount_percent)
    .bind(claimed_at)
    .bind(week_start + 7 * 24 * 60 * 60)
    .fetch_one(&mut *tx)
    .await
    .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    sqlx::query(
        "UPDATE assistant_weekly_discounts SET status = 'claimed', claimed_at = $3, code_id = $4 \
         WHERE user_id = $1 AND week_start = $2",
    )
    .bind(user_id)
    .bind(week_start)
    .bind(claimed_at)
    .bind(code_id)
    .execute(&mut *tx)
    .await
    .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    tx.commit()
        .await
        .map_err(|error| ClaimWeeklyError::Internal(error.to_string()))?;
    let discount = load_weekly_discount(pg, user_id)
        .await
        .map_err(ClaimWeeklyError::Internal)?
        .unwrap_or(Value::Null);
    Ok((discount, false))
}

async fn list_conversations_page(
    state: &AssistantReadState,
    viewer_user_id: i64,
    owner_user_id: i64,
    limit: i64,
    archived: bool,
    raw_cursor: Option<&str>,
) -> Result<ConversationPage, HistoryError> {
    authorize_history_viewer(&state.pg, viewer_user_id, owner_user_id).await?;
    let limit = limit.min(ASSISTANT_HISTORY_PAGE_MAX);
    let cursor = if let Some(raw) = raw_cursor.filter(|value| !value.trim().is_empty()) {
        Some(decode_history_cursor(
            &state.session_secret,
            raw.trim(),
            owner_user_id,
            archived,
        )?)
    } else {
        None
    };
    let sql = conversation_list_sql(limit, archived, cursor.is_some());
    let mut query = sqlx::query(&sql).bind(owner_user_id);
    if let Some(cursor) = cursor.as_ref() {
        query = query.bind(cursor.updated_at).bind(cursor.id);
    }
    let rows = query
        .fetch_all(&state.pg)
        .await
        .map_err(|error| HistoryError::Internal(error.to_string()))?;
    let owner = if viewer_user_id == owner_user_id {
        "self"
    } else {
        "lower_level_user"
    };
    let has_more = rows.len() as i64 > limit;
    let rows = if has_more {
        &rows[..limit as usize]
    } else {
        &rows[..]
    };
    let conversations: Vec<Value> = rows
        .iter()
        .map(|row| {
            json!({
                "id": row.try_get::<i64, _>("id").unwrap_or_default(),
                "title": row.try_get::<String, _>("title").unwrap_or_default(),
                "last_message_preview": row.try_get::<String, _>("last_message_preview").unwrap_or_default(),
                "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
                "updated_at": row.try_get::<i64, _>("updated_at").unwrap_or_default(),
                "archived_at": row.try_get::<i64, _>("archived_at").unwrap_or_default(),
                "restricted_at": row.try_get::<i64, _>("restricted_at").unwrap_or_default(),
                "owner": owner,
                "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
            })
        })
        .collect();
    let next_cursor = if has_more {
        let last = rows.last().expect("has_more implies row");
        Some(encode_history_cursor(
            &state.session_secret,
            owner_user_id,
            archived,
            HistoryCursor {
                updated_at: last.try_get("updated_at").unwrap_or_default(),
                id: last.try_get("id").unwrap_or_default(),
            },
        )?)
    } else {
        None
    };
    Ok(ConversationPage {
        conversations,
        next_cursor,
    })
}

fn conversation_list_sql(limit: i64, archived: bool, has_cursor: bool) -> String {
    let archive_filter = if archived {
        "archived_at <> 0"
    } else {
        "archived_at = 0"
    };
    let mut sql = format!(
        "SELECT id, title, last_message_preview, created_at, updated_at, archived_at, restricted_at \
         FROM assistant_conversations WHERE user_id = $1 AND {archive_filter} \
         AND EXISTS (SELECT 1 FROM assistant_history_messages m WHERE m.conversation_id = assistant_conversations.id)"
    );
    if has_cursor {
        sql.push_str(" AND ((updated_at < $2) OR (updated_at = $2 AND id < $3))");
    }
    sql.push_str(" ORDER BY updated_at DESC, id DESC LIMIT ");
    sql.push_str(&(limit + 1).to_string());
    sql
}

async fn load_conversation_history(
    pg: &PgPool,
    viewer_user_id: i64,
    conversation_id: i64,
    limit: i64,
) -> Result<(Value, Vec<Value>), HistoryError> {
    let owner_user_id: i64 =
        sqlx::query_scalar("SELECT user_id FROM assistant_conversations WHERE id = $1")
            .bind(conversation_id)
            .fetch_optional(pg)
            .await
            .map_err(|error| HistoryError::Internal(error.to_string()))?
            .ok_or(HistoryError::NotFound)?;
    authorize_history_viewer(pg, viewer_user_id, owner_user_id).await?;
    let conversation_row = sqlx::query(
        "SELECT id, title, last_message_preview, created_at, updated_at, archived_at, restricted_at \
         FROM assistant_conversations WHERE id = $1",
    )
    .bind(conversation_id)
    .fetch_one(pg)
    .await
    .map_err(|error| HistoryError::Internal(error.to_string()))?;
    let owner = if viewer_user_id == owner_user_id {
        "self"
    } else {
        "lower_level_user"
    };
    let conversation = json!({
        "id": conversation_row.try_get::<i64, _>("id").unwrap_or_default(),
        "title": conversation_row.try_get::<String, _>("title").unwrap_or_default(),
        "last_message_preview": conversation_row.try_get::<String, _>("last_message_preview").unwrap_or_default(),
        "created_at": conversation_row.try_get::<i64, _>("created_at").unwrap_or_default(),
        "updated_at": conversation_row.try_get::<i64, _>("updated_at").unwrap_or_default(),
        "archived_at": conversation_row.try_get::<i64, _>("archived_at").unwrap_or_default(),
        "restricted_at": conversation_row.try_get::<i64, _>("restricted_at").unwrap_or_default(),
        "owner": owner,
        "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
    });
    let message_rows = sqlx::query(
        "SELECT id, role, content, created_at FROM assistant_history_messages \
         WHERE conversation_id = $1 ORDER BY sequence DESC LIMIT $2",
    )
    .bind(conversation_id)
    .bind(limit.min(ASSISTANT_HISTORY_PAGE_MAX))
    .fetch_all(pg)
    .await
    .map_err(|error| HistoryError::Internal(error.to_string()))?;
    let messages: Vec<Value> = message_rows
        .into_iter()
        .rev()
        .map(|row| {
            json!({
                "id": row.try_get::<i64, _>("id").unwrap_or_default(),
                "role": row.try_get::<String, _>("role").unwrap_or_default(),
                "content": row.try_get::<String, _>("content").unwrap_or_default(),
                "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
                "privacy_notice": ASSISTANT_HISTORY_PRIVACY_NOTICE,
            })
        })
        .collect();
    Ok((conversation, messages))
}

async fn update_conversation_archive(
    pg: &PgPool,
    user_id: i64,
    conversation_id: i64,
    archived: bool,
) -> Result<ArchiveRow, HistoryError> {
    let now = unix_seconds();
    let archived_at = if archived { now } else { 0 };
    let result = if archived {
        sqlx::query(
            "UPDATE assistant_conversations SET archived_at = $3 \
             WHERE id = $1 AND user_id = $2 AND archived_at = 0 RETURNING id, archived_at",
        )
    } else {
        sqlx::query(
            "UPDATE assistant_conversations SET archived_at = 0 \
             WHERE id = $1 AND user_id = $2 AND archived_at <> 0 RETURNING id, archived_at",
        )
    }
    .bind(conversation_id)
    .bind(user_id)
    .bind(archived_at)
    .fetch_optional(pg)
    .await
    .map_err(|error| HistoryError::Internal(error.to_string()))?;
    match result {
        Some(row) => Ok(ArchiveRow {
            id: row.try_get("id").unwrap_or_default(),
            archived_at: row.try_get("archived_at").unwrap_or_default(),
        }),
        None => {
            let exists: bool = sqlx::query_scalar(
                "SELECT EXISTS(SELECT 1 FROM assistant_conversations WHERE id = $1 AND user_id = $2)",
            )
            .bind(conversation_id)
            .bind(user_id)
            .fetch_one(pg)
            .await
            .map_err(|error| HistoryError::Internal(error.to_string()))?;
            if !exists {
                Err(HistoryError::NotFound)
            } else if archived {
                Err(HistoryError::AlreadyArchived)
            } else {
                Err(HistoryError::NotArchived)
            }
        }
    }
}

async fn reveal_card(
    pg: &PgPool,
    owner_user_id: i64,
    card_id: &str,
) -> Result<(Value, Value), RevealError> {
    let now = unix_seconds();
    let row = sqlx::query(
        "UPDATE assistant_secure_cards SET revealed_at = $3 \
         WHERE id = $1 AND owner_user_id = $2 AND revealed_at = 0 AND expires_at > $4 \
         RETURNING type, summary, ciphertext",
    )
    .bind(card_id)
    .bind(owner_user_id)
    .bind(now)
    .bind(now)
    .fetch_optional(pg)
    .await
    .map_err(|error| RevealError::Internal(error.to_string()))?;
    let Some(row) = row else {
        let exists = sqlx::query(
            "SELECT revealed_at, expires_at FROM assistant_secure_cards \
             WHERE id = $1 AND owner_user_id = $2",
        )
        .bind(card_id)
        .bind(owner_user_id)
        .fetch_optional(pg)
        .await
        .map_err(|error| RevealError::Internal(error.to_string()))?;
        return Err(match exists {
            None => RevealError::NotFound,
            Some(row) => {
                let revealed_at: i64 = row.try_get("revealed_at").unwrap_or_default();
                let expires_at: i64 = row.try_get("expires_at").unwrap_or_default();
                if revealed_at != 0 {
                    RevealError::Consumed
                } else if expires_at <= now {
                    RevealError::Expired
                } else {
                    RevealError::NotFound
                }
            }
        });
    };
    let card = json!({
        "type": row.try_get::<String, _>("type").unwrap_or_default(),
        "label": row.try_get::<String, _>("summary").unwrap_or_default(),
        "owner": "self",
        "shield": false,
    });
    let ciphertext: String = row
        .try_get("ciphertext")
        .map_err(|error| RevealError::Internal(error.to_string()))?;
    let payload = decode_secure_card_payload(&ciphertext).map_err(|_| RevealError::Invalid)?;
    Ok((card, payload))
}

async fn load_prompt_presets(pg: &PgPool) -> Result<Vec<Value>, String> {
    let rows = sqlx::query(
        "SELECT preset_id, prompt, label FROM assistant_pre_conversation_preset_cache \
         ORDER BY generation DESC, position ASC LIMIT 4",
    )
    .fetch_all(pg)
    .await
    .map_err(|error| error.to_string())?;
    Ok(rows
        .into_iter()
        .map(|row| {
            json!({
                "id": row.try_get::<String, _>("preset_id").unwrap_or_default(),
                "prompt": row.try_get::<String, _>("prompt").unwrap_or_default(),
                "label": row.try_get::<String, _>("label").unwrap_or_default(),
            })
        })
        .collect())
}

async fn increment_preset_click(pg: &PgPool, preset_id: &str) -> Result<(), PresetError> {
    let updated = sqlx::query(
        "UPDATE assistant_pre_conversation_preset_stats SET click_count = click_count + 1, \
         updated_at = $2 WHERE preset_id = $1",
    )
    .bind(preset_id)
    .bind(unix_seconds())
    .execute(pg)
    .await
    .map_err(|error| PresetError::Internal(error.to_string()))?;
    if updated.rows_affected() == 0 {
        let exists: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM assistant_pre_conversation_preset_cache WHERE preset_id = $1)",
        )
        .bind(preset_id)
        .fetch_one(pg)
        .await
        .map_err(|error| PresetError::Internal(error.to_string()))?;
        if !exists {
            return Err(PresetError::NotFound);
        }
    }
    Ok(())
}

async fn load_first_question_summary(pg: &PgPool, since: i64) -> Result<Value, String> {
    let rows = sqlx::query(
        "SELECT question, count, last_asked_at FROM assistant_first_question_stats \
         WHERE last_asked_at >= $1 ORDER BY count DESC, last_asked_at DESC LIMIT 100",
    )
    .bind(since)
    .fetch_all(pg)
    .await
    .map_err(|error| error.to_string())?;
    Ok(json!(
        rows.into_iter()
            .map(|row| {
                json!({
                    "question": row.try_get::<String, _>("question").unwrap_or_default(),
                    "count": row.try_get::<i64, _>("count").unwrap_or_default(),
                    "last_asked_at": row.try_get::<i64, _>("last_asked_at").unwrap_or_default(),
                })
            })
            .collect::<Vec<_>>()
    ))
}

async fn load_profile_summary(pg: &PgPool, since: i64) -> Result<Value, String> {
    let rows = sqlx::query(
        "SELECT persona, count FROM assistant_profile_stats WHERE updated_at >= $1 \
         ORDER BY count DESC LIMIT 100",
    )
    .bind(since)
    .fetch_all(pg)
    .await
    .map_err(|error| error.to_string())?;
    Ok(json!(
        rows.into_iter()
            .map(|row| {
                json!({
                    "persona": row.try_get::<String, _>("persona").unwrap_or_default(),
                    "count": row.try_get::<i64, _>("count").unwrap_or_default(),
                })
            })
            .collect::<Vec<_>>()
    ))
}

async fn load_funding_summary(pg: &PgPool, since: i64) -> Result<Value, FundingError> {
    let billing_user_id: Option<i64> = sqlx::query_scalar(
        "SELECT id FROM users WHERE role = $1 AND deleted_at IS NULL AND status = 1 \
         ORDER BY id LIMIT 1",
    )
    .bind(ROOT_ROLE)
    .fetch_optional(pg)
    .await
    .map_err(|error| FundingError::Internal(error.to_string()))?;
    let Some(billing_user_id) = billing_user_id else {
        return Err(FundingError::BillingUnavailable);
    };
    let spent: i64 = sqlx::query_scalar(
        "SELECT COALESCE(SUM(quota), 0) FROM logs WHERE user_id = $1 AND type = 2 \
         AND created_at >= $2 AND created_at <= $3",
    )
    .bind(billing_user_id)
    .bind(since)
    .bind(unix_seconds())
    .fetch_one(pg)
    .await
    .map_err(|error| FundingError::Internal(error.to_string()))?;
    let remaining_quota: i64 =
        sqlx::query_scalar("SELECT COALESCE(quota, 0) FROM users WHERE id = $1")
            .bind(billing_user_id)
            .fetch_one(pg)
            .await
            .map_err(|error| FundingError::Internal(error.to_string()))?;
    Ok(json!({
        "start_timestamp": since,
        "end_timestamp": unix_seconds(),
        "spent_quota": spent,
        "remaining_quota": remaining_quota,
    }))
}

async fn load_latest_review_task(pg: &PgPool) -> Result<Value, String> {
    let row = sqlx::query(
        "SELECT id, task_type, status, payload, result, created_at, updated_at \
         FROM system_tasks WHERE task_type = 'assistant_review' ORDER BY id DESC LIMIT 1",
    )
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?;
    Ok(row
        .map(|row| {
            json!({
                "id": row.try_get::<i64, _>("id").unwrap_or_default(),
                "task_type": row.try_get::<String, _>("task_type").unwrap_or_default(),
                "status": row.try_get::<String, _>("status").unwrap_or_default(),
                "payload": row.try_get::<String, _>("payload").unwrap_or_default(),
                "result": row.try_get::<String, _>("result").unwrap_or_default(),
                "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
                "updated_at": row.try_get::<i64, _>("updated_at").unwrap_or_default(),
            })
        })
        .unwrap_or(Value::Null))
}

async fn load_request_reviews(
    pg: &PgPool,
    user_id: i64,
    violations_only: bool,
    page: i64,
    page_size: i64,
) -> Result<Value, String> {
    let offset = (page - 1) * page_size;
    let violation_filter = if violations_only {
        " AND violation = TRUE"
    } else {
        ""
    };
    let sql = format!(
        "SELECT id, conversation_id, model, violation, reason, created_at \
         FROM assistant_request_reviews WHERE user_id = $1{violation_filter} \
         ORDER BY id DESC LIMIT $2 OFFSET $3"
    );
    let rows = sqlx::query(&sql)
        .bind(user_id)
        .bind(page_size)
        .bind(offset)
        .fetch_all(pg)
        .await
        .map_err(|error| error.to_string())?;
    let total: i64 = sqlx::query_scalar(&format!(
        "SELECT COUNT(*) FROM assistant_request_reviews WHERE user_id = $1{violation_filter}"
    ))
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|error| error.to_string())?;
    let violation_count: i64 = sqlx::query_scalar(
        "SELECT COALESCE(violation_count, 0) FROM assistant_request_review_violations WHERE user_id = $1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?
    .unwrap_or(0);
    let reset_at: i64 = sqlx::query_scalar(
        "SELECT COALESCE(reset_at, 0) FROM assistant_request_review_violations WHERE user_id = $1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?
    .unwrap_or(0);
    Ok(json!({
        "items": rows.into_iter().map(|row| json!({
            "id": row.try_get::<i64, _>("id").unwrap_or_default(),
            "conversation_id": row.try_get::<i64, _>("conversation_id").unwrap_or_default(),
            "model": row.try_get::<String, _>("model").unwrap_or_default(),
            "violation": row.try_get::<bool, _>("violation").unwrap_or_default(),
            "reason": row.try_get::<String, _>("reason").unwrap_or_default(),
            "created_at": row.try_get::<i64, _>("created_at").unwrap_or_default(),
        })).collect::<Vec<_>>(),
        "total": total,
        "page": page,
        "page_size": page_size,
        "violation_count": violation_count,
        "reset_at": reset_at,
        "queue_stats": {},
    }))
}

async fn authorize_history_viewer(
    pg: &PgPool,
    viewer_user_id: i64,
    owner_user_id: i64,
) -> Result<(), HistoryError> {
    if viewer_user_id == owner_user_id {
        return Ok(());
    }
    let rows = sqlx::query("SELECT id, role FROM users WHERE id = ANY($1) AND deleted_at IS NULL")
        .bind([viewer_user_id, owner_user_id])
        .fetch_all(pg)
        .await
        .map_err(|error| HistoryError::Internal(error.to_string()))?;
    let mut viewer_role = None;
    let mut owner_role = None;
    for row in rows {
        let id: i64 = row.try_get("id").unwrap_or_default();
        let role: i64 = row.try_get("role").unwrap_or_default();
        if id == viewer_user_id {
            viewer_role = Some(role);
        }
        if id == owner_user_id {
            owner_role = Some(role);
        }
    }
    if viewer_role.is_none() || owner_role.is_none() {
        return Err(HistoryError::NotFound);
    }
    let viewer_rank = conversation_rank(viewer_role.unwrap_or_default());
    let owner_rank = conversation_rank(owner_role.unwrap_or_default());
    if viewer_rank <= owner_rank {
        return Err(HistoryError::NotFound);
    }
    Ok(())
}

async fn can_manage_target(pg: &PgPool, viewer_id: i64, viewer_role: i64, target_id: i64) -> bool {
    if viewer_id == target_id {
        return true;
    }
    let target_role: Option<i64> =
        sqlx::query_scalar("SELECT role FROM users WHERE id = $1 AND deleted_at IS NULL")
            .bind(target_id)
            .fetch_optional(pg)
            .await
            .ok()
            .flatten();
    match target_role {
        Some(role) => conversation_rank(viewer_role) > conversation_rank(role),
        None => false,
    }
}

fn conversation_rank(role: i64) -> i64 {
    if role >= ROOT_ROLE {
        10_000
    } else if role >= ADMIN_ROLE {
        1_000 + role
    } else {
        0
    }
}

#[derive(Clone, Copy, Debug)]
struct HistoryCursor {
    updated_at: i64,
    id: i64,
}

fn encode_history_cursor(
    session_secret: &SecretString,
    owner_user_id: i64,
    archived: bool,
    cursor: HistoryCursor,
) -> Result<String, HistoryError> {
    let payload = json!({
        "v": 1,
        "owner_id": owner_user_id,
        "archived": archived,
        "updated_at": cursor.updated_at,
        "id": cursor.id,
    });
    let encoded = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(
        serde_json::to_vec(&payload).map_err(|error| HistoryError::Internal(error.to_string()))?,
    );
    let signature = history_cursor_mac(session_secret, &encoded);
    Ok(format!("{encoded}.{signature}"))
}

fn decode_history_cursor(
    session_secret: &SecretString,
    raw: &str,
    owner_user_id: i64,
    archived: bool,
) -> Result<HistoryCursor, HistoryError> {
    let (encoded, signature) = raw
        .split_once('.')
        .filter(|(payload, sig)| !payload.is_empty() && !sig.is_empty())
        .ok_or(HistoryError::InvalidCursor)?;
    if history_cursor_mac(session_secret, encoded) != signature {
        return Err(HistoryError::InvalidCursor);
    }
    let payload: Value = serde_json::from_slice(
        &base64::engine::general_purpose::URL_SAFE_NO_PAD
            .decode(encoded)
            .map_err(|_| HistoryError::InvalidCursor)?,
    )
    .map_err(|_| HistoryError::InvalidCursor)?;
    if payload.get("v").and_then(Value::as_i64) != Some(1)
        || payload.get("owner_id").and_then(Value::as_i64) != Some(owner_user_id)
        || payload.get("archived").and_then(Value::as_bool) != Some(archived)
    {
        return Err(HistoryError::InvalidCursor);
    }
    let updated_at = payload
        .get("updated_at")
        .and_then(Value::as_i64)
        .filter(|value| *value > 0)
        .ok_or(HistoryError::InvalidCursor)?;
    let id = payload
        .get("id")
        .and_then(Value::as_i64)
        .filter(|value| *value > 0)
        .ok_or(HistoryError::InvalidCursor)?;
    Ok(HistoryCursor { updated_at, id })
}

fn history_cursor_mac(session_secret: &SecretString, encoded: &str) -> String {
    let key = format!(
        "assistant-history-cursor-v1:{}",
        session_secret.expose_secret()
    );
    let mut mac = HmacSha256::new_from_slice(key.as_bytes()).expect("HMAC key");
    mac.update(encoded.as_bytes());
    base64::engine::general_purpose::URL_SAFE_NO_PAD.encode(mac.finalize().into_bytes())
}

async fn consume_auth_flow(
    pg: &PgPool,
    session_secret: &SecretString,
    token: &str,
    purpose: &str,
    user_id: i64,
    session_id: &str,
) -> Result<Option<String>, AuthFlowError> {
    let token_hash = auth_flow_hash(session_secret, token)
        .map_err(|_| AuthFlowError::Internal("hash failed".to_owned()))?;
    let row = sqlx::query(
        "UPDATE auth_flows SET consumed_at = NOW() WHERE token_hash = $1 AND purpose = $2 \
         AND user_id = $3 AND session_id = $4 AND consumed_at IS NULL AND expires_at > NOW() \
         RETURNING payload",
    )
    .bind(token_hash)
    .bind(purpose)
    .bind(user_id)
    .bind(session_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| AuthFlowError::Internal(error.to_string()))?;
    Ok(row.and_then(|row| row.try_get::<Option<String>, _>("payload").ok().flatten()))
}

fn auth_flow_hash(session_secret: &SecretString, token: &str) -> Result<String, ()> {
    let key = format!("auth-flow-v1:{}", session_secret.expose_secret());
    let mut mac = HmacSha256::new_from_slice(key.as_bytes()).map_err(|_| ())?;
    mac.update(token.as_bytes());
    Ok(hex::encode(mac.finalize().into_bytes()))
}

fn decode_secure_card_payload(ciphertext: &str) -> Result<Value, ()> {
    let decoded = base64::engine::general_purpose::STANDARD
        .decode(ciphertext.trim())
        .map_err(|_| ())?;
    serde_json::from_slice(&decoded).map_err(|_| ())
}

async fn load_onboarding_step_state(
    pg: &PgPool,
    user_id: i64,
) -> Result<std::collections::BTreeMap<String, bool>, String> {
    let key_exists: i64 = sqlx::query_scalar(
        "SELECT COUNT(*)::BIGINT FROM tokens WHERE user_id = $1 AND status = 1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|error| error.to_string())?;
    let todo = sqlx::query(
        "SELECT COALESCE(client_installed_at, 0) AS client_installed_at, \
         COALESCE(client_configured_at, 0) AS client_configured_at \
         FROM l1_onboarding_todos WHERE user_id = $1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|error| error.to_string())?;
    let (installed, configured) = todo.map_or((0, 0), |row| {
        (
            row.try_get::<i64, _>("client_installed_at")
                .unwrap_or_default(),
            row.try_get::<i64, _>("client_configured_at")
                .unwrap_or_default(),
        )
    });
    let last_api: i64 = sqlx::query_scalar(
        "SELECT COALESCE(last_api_activity_at, 0) FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(|error| error.to_string())?;
    let key_complete = key_exists > 0;
    let install_complete = key_complete && installed > 0;
    let configure_complete = install_complete && configured > 0;
    let first_response_complete = configure_complete && last_api >= configured && last_api > 0;
    Ok(std::collections::BTreeMap::from([
        ("create_api_key".to_owned(), key_complete),
        ("install_client".to_owned(), install_complete),
        ("configure_client".to_owned(), configure_complete),
        (
            "first_successful_response".to_owned(),
            first_response_complete,
        ),
    ]))
}

fn assistant_week_start() -> i64 {
    use chrono::Datelike;
    let now = chrono::Utc::now();
    let weekday = now.weekday().num_days_from_monday();
    let day = now.date_naive();
    let monday = day - chrono::Duration::days(i64::from(weekday));
    monday
        .and_hms_opt(0, 0, 0)
        .expect("midnight exists")
        .and_utc()
        .timestamp()
}

fn unix_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .unwrap_or(Duration::ZERO)
        .as_secs() as i64
}

#[cfg(test)]
mod tests {
    use super::conversation_list_sql;

    #[test]
    fn conversation_list_sql_uses_contiguous_cursor_bindings() {
        let sql = conversation_list_sql(25, false, true);

        assert!(sql.contains("user_id = $1"));
        assert!(sql.contains("updated_at < $2"));
        assert!(sql.contains("updated_at = $2 AND id < $3"));
        assert!(!sql.contains("$4"));
        assert_eq!(sql.matches("$2").count(), 2);
        assert!(sql.ends_with("LIMIT 26"));
    }

    #[test]
    fn conversation_list_sql_omits_cursor_bindings_without_cursor() {
        let sql = conversation_list_sql(10, true, false);

        assert!(sql.contains("archived_at <> 0"));
        assert!(!sql.contains("$2"));
        assert!(sql.ends_with("LIMIT 11"));
    }
}
