use axum::{extract::State, http::StatusCode, response::Response};
use secrecy::SecretString;
use serde::Deserialize;
use serde_json::{Map, Value, json};

use super::super::{
    ASSISTANT_KEY_NAME_MAX_CHARS, AssistantReadState, AssistantToolOutcome,
    CriticalRateLimitOutcome, DashboardUserView, assistant_console_not_found, assistant_error,
    assistant_error_owned, assistant_session_required, authenticated_user, input_number,
    input_string, invalid_assistant_create_key_request, success, tool_result, with_auth_version,
    with_no_store,
};
use super::{KEY_MUTATION_BODY_LIMIT_BYTES, domain::*};
use crate::legacy_empty_response;

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct PrepareKeyInput {
    #[serde(default)]
    name: String,
    group: String,
    #[serde(default)]
    conversation_id: i64,
    #[serde(default)]
    group_warning_confirmations: i64,
}

#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct ConfirmKeyInput {
    confirmation_token: String,
    #[serde(default)]
    two_factor_code: String,
}

struct KeyMutationContext {
    user: DashboardUserView,
    session_id: String,
    authorization_fence: AuthorizationFence,
}

async fn key_mutation_context(
    state: &AssistantReadState,
    headers: &axum::http::HeaderMap,
) -> Result<KeyMutationContext, Response> {
    let principal = authenticated_user(state, headers).await?;
    let session = state
        .auth
        .current_session(SecretString::from(principal.credential))
        .await
        .map_err(|_| with_no_store(assistant_session_required()))?;
    if session.user.id != principal.user.id {
        return Err(with_no_store(assistant_session_required()));
    }
    let authorization_fence = AuthorizationFence::capture(
        principal.user.id,
        &principal.user.username,
        &session.session_id,
        session.session_version,
        session.user_auth_version,
        state.developer_access_policy,
    )
    .map_err(|_| with_no_store(assistant_session_required()))?;
    Ok(KeyMutationContext {
        user: principal.user,
        session_id: session.session_id,
        authorization_fence,
    })
}

pub(in crate::routes::assistant) async fn prepare_key_handler(
    State(state): State<AssistantReadState>,
    request: axum::extract::Request,
) -> Response {
    let headers = request.headers().clone();
    let context = match key_mutation_context(&state, &headers).await {
        Ok(context) => context,
        Err(response) => return response,
    };
    let input = match parse_body::<PrepareKeyInput>(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    if !context.user.developer_access_granted {
        return with_no_store(assistant_console_not_found());
    }
    if let Some(response) =
        mutation_rate_limit(&state, "assistant-prepare-key", context.user.id).await
    {
        return response;
    }
    let result = prepare_for_user(&state, &context.user, &context.session_id, input).await;
    with_no_store(match result {
        Ok(action) => success(json!(action)),
        Err(error) => error_response(error),
    })
}

pub(in crate::routes::assistant) async fn confirm_key_handler(
    State(state): State<AssistantReadState>,
    request: axum::extract::Request,
) -> Response {
    let headers = request.headers().clone();
    let context = match key_mutation_context(&state, &headers).await {
        Ok(context) => context,
        Err(response) => return response,
    };
    let input = match parse_body::<ConfirmKeyInput>(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    if !context.user.developer_access_granted {
        return with_no_store(assistant_console_not_found());
    }
    if let Some(response) =
        mutation_rate_limit(&state, "assistant-create-key", context.user.id).await
    {
        return response;
    }
    let token = match ConfirmationToken::parse(&input.confirmation_token) {
        Ok(token) => token,
        Err(error) => return with_no_store(error_response(error)),
    };
    let result = state
        .store
        .confirm_key_draft(
            context.authorization_fence,
            token,
            input.two_factor_code.trim(),
        )
        .await;
    with_no_store(match result {
        Ok(created) => success(json!(created)),
        Err(error) => error_response(error),
    })
}

pub(in crate::routes::assistant) async fn prepare_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    input: &Map<String, Value>,
) -> AssistantToolOutcome {
    if !actor.developer_access_granted {
        return tool_result(json!({
            "ok": false,
            "error": "L1 access is required to create an API key"
        }));
    }
    let options = match state.store.key_group_options(&actor.group).await {
        Ok(options) => options,
        Err(error) => return tool_result(json!({"ok":false,"error":error})),
    };
    let group = input_string(input, "group");
    let name = normalized_name(&input_string(input, "name"));
    if name.chars().count() > ASSISTANT_KEY_NAME_MAX_CHARS {
        return tool_result(json!({
            "ok":false,"status":"name_invalid","error":"API key name must be at most 50 characters"
        }));
    }
    if group.trim().is_empty() {
        return tool_result(json!({
            "ok":true,"status":"group_required","action":"create_key",
            "available_groups":options,
            "message":"Ask the user to choose one exact routing group before requesting confirmation.",
            "requested_name":name,
        }));
    }
    let real_group = match RealSelectableGroup::parse(&group) {
        Ok(group) if options.iter().any(|option| option.id == group.as_str()) => group,
        _ => {
            return tool_result(json!({
                "ok":false,"status":"invalid_group",
                "error":"the selected group is not available for this account",
                "available_groups":options,
            }));
        }
    };
    let selected = options
        .iter()
        .find(|option| option.id == real_group.as_str());
    let warning = selected.and_then(|option| option.warning.clone());
    let confirmations = input_number(input, "group_warning_confirmations")
        .filter(|value| value.is_finite() && value.fract() == 0.0)
        .map(|value| value as i64)
        .unwrap_or_default();
    if warning
        .as_ref()
        .is_some_and(|warning| warning.confirmations != confirmations)
    {
        return tool_result(json!({
            "ok":true,"status":"group_warning_required","action":"create_key",
            "requested_name":name,"requested_group":real_group.as_str(),
            "warning":warning,
            "required_confirmations":warning.as_ref().map_or(0, |warning| warning.confirmations),
            "message":"Show this group warning and collect the exact required confirmations before preparing key creation.",
        }));
    }
    let conversation_id = input_number(input, "conversation_id")
        .filter(|value| value.is_finite() && value.fract() == 0.0 && *value >= 0.0)
        .map(|value| value as i64)
        .unwrap_or_default();
    let draft = PreparedKeyDraft {
        version: DRAFT_VERSION,
        name: name.clone(),
        group: real_group,
        conversation_id,
        warning,
    };
    let action = match state
        .store
        .prepare_key_draft(actor.id, session_id, draft)
        .await
    {
        Ok(action) => action,
        Err(error) => return tool_result(json!({"ok":false,"error":error.to_string()})),
    };
    AssistantToolOutcome {
        result: json!({
            "ok":true,"status":"confirmation_required","action":"create_key","ui_path":"/keys",
            "message":"Ask the user to explicitly confirm this server-prepared key draft; do not claim that a key exists yet.",
            "requested_name":action.name,
            "requested_group":action.group,
        }),
        action: Some(json!(action)),
    }
}

async fn prepare_for_user(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    input: PrepareKeyInput,
) -> Result<PreparedKeyAction, KeyCreationError> {
    let name = normalized_name(&input.name);
    if name.chars().count() > ASSISTANT_KEY_NAME_MAX_CHARS {
        return Err(KeyCreationError::Unavailable(
            "API key name must be at most 50 characters".to_owned(),
        ));
    }
    let group = RealSelectableGroup::parse(&input.group)?;
    let options = state
        .store
        .key_group_options(&actor.group)
        .await
        .map_err(KeyCreationError::Unavailable)?;
    let selected = options
        .iter()
        .find(|option| option.id == group.as_str())
        .ok_or(KeyCreationError::InvalidGroup)?;
    if selected
        .warning
        .as_ref()
        .is_some_and(|warning| warning.confirmations != input.group_warning_confirmations)
    {
        return Err(KeyCreationError::WarningChanged);
    }
    state
        .store
        .prepare_key_draft(
            actor.id,
            session_id,
            PreparedKeyDraft {
                version: DRAFT_VERSION,
                name,
                group,
                conversation_id: input.conversation_id,
                warning: selected.warning.clone(),
            },
        )
        .await
}

fn normalized_name(raw: &str) -> String {
    let name = raw.trim();
    if name.is_empty() {
        "AI assistant key".to_owned()
    } else {
        name.to_owned()
    }
}

async fn mutation_rate_limit(
    state: &AssistantReadState,
    scope: &str,
    user_id: i64,
) -> Option<Response> {
    match state.user_rate_limiter.check(scope, user_id).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(with_no_store(with_auth_version(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )))),
        Err(()) => Some(with_no_store(with_auth_version(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )))),
    }
}

async fn parse_body<T: for<'de> Deserialize<'de>>(
    request: axum::extract::Request,
) -> Result<T, Response> {
    let body = axum::body::to_bytes(request.into_body(), KEY_MUTATION_BODY_LIMIT_BYTES)
        .await
        .map_err(|_| {
            with_no_store(with_auth_version(legacy_empty_response(
                StatusCode::PAYLOAD_TOO_LARGE,
                None,
            )))
        })?;
    if body.is_empty() {
        return Err(with_no_store(invalid_assistant_create_key_request()));
    }
    serde_json::from_slice(&body).map_err(|_| with_no_store(invalid_assistant_create_key_request()))
}

fn error_response(error: KeyCreationError) -> Response {
    match error {
        KeyCreationError::ConfirmationRequired => assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_CONFIRMATION_REQUIRED",
            "an opaque confirmation token is required",
        ),
        KeyCreationError::InvalidConfirmation => assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_KEY_CONFIRMATION_INVALID",
            "API key confirmation is invalid, expired, or already used",
        ),
        KeyCreationError::InvalidGroup => assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_INVALID_GROUP",
            "the selected group is no longer available for this account",
        ),
        KeyCreationError::WarningChanged => assistant_error(
            StatusCode::CONFLICT,
            "ASSISTANT_GROUP_WARNING_CHANGED",
            "the selected group warning changed; prepare the key again",
        ),
        KeyCreationError::TokenLimit(limit) => assistant_error_owned(
            StatusCode::CONFLICT,
            "ASSISTANT_KEY_LIMIT_REACHED",
            format!("API key limit reached ({limit})"),
        ),
        KeyCreationError::TwoFactorInvalid => assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_TWO_FACTOR_INVALID",
            "a valid two-factor code is required",
        ),
        KeyCreationError::Unavailable(_) => assistant_error(
            StatusCode::INTERNAL_SERVER_ERROR,
            "ASSISTANT_INTERNAL_ERROR",
            "assistant key creation is temporarily unavailable",
        ),
    }
}
