use crate::auth::{AuthErrorKind, UserAuthPolicyError, user_auth_message, user_auth_status};
use axum::{
    Json,
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
};
use serde_json::{Value, json};

pub(crate) fn legacy_json(status: StatusCode, body: Value) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

pub(crate) fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("Bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

pub(crate) fn coded_error(
    status: StatusCode,
    code: &'static str,
    message: &'static str,
) -> Response {
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

pub(crate) fn simple_dashboard_auth_error(
    headers: &HeaderMap,
    kind: Option<AuthErrorKind>,
) -> Response {
    let (status, code, english) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        Some(AuthErrorKind::UserDisabled) => (
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
    let message = if accepts_chinese(headers) {
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
    };
    coded_error(status, code, message)
}

pub(crate) fn simple_user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
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

#[derive(Clone, Copy)]
enum TokenLocale {
    En,
    ZhCn,
    ZhTw,
}

fn token_locale(headers: &HeaderMap) -> TokenLocale {
    let language = headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(',').next())
        .and_then(|value| value.split(';').next())
        .unwrap_or_default()
        .trim()
        .to_ascii_lowercase();
    if language.starts_with("zh-tw") {
        TokenLocale::ZhTw
    } else if language.starts_with("zh") {
        TokenLocale::ZhCn
    } else {
        TokenLocale::En
    }
}

fn auth_not_logged_in(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, not logged in and no access token provided",
        TokenLocale::ZhCn => "无权进行此操作，未登录且未提供 access token",
        TokenLocale::ZhTw => "無權進行此操作，未登入且未提供 access token",
    }
}

fn auth_invalid_access_token(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Unauthorized, invalid access token",
        TokenLocale::ZhCn => "无权进行此操作，access token 无效",
        TokenLocale::ZhTw => "無權進行此操作，access token 無效",
    }
}

fn auth_database_error(headers: &HeaderMap) -> &'static str {
    match token_locale(headers) {
        TokenLocale::En => "Database error, please contact the administrator",
        TokenLocale::ZhCn => "数据库出错，请联系管理员",
        TokenLocale::ZhTw => "資料庫出錯，請聯繫管理員",
    }
}

pub(crate) fn localized_dashboard_auth_error(
    headers: &HeaderMap,
    kind: Option<AuthErrorKind>,
) -> Response {
    if kind == Some(AuthErrorKind::UserDisabled) {
        return localized_user_policy_error(headers, UserAuthPolicyError::UserDisabled);
    }
    let (status, code, message) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            auth_not_logged_in(headers),
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            auth_not_logged_in(headers),
        ),
        Some(AuthErrorKind::Internal) => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            auth_database_error(headers),
        ),
        _ => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            auth_invalid_access_token(headers),
        ),
    };
    legacy_json(
        status,
        json!({"success": false, "code": code, "message": message}),
    )
}

pub(crate) fn localized_user_policy_error(
    headers: &HeaderMap,
    error: UserAuthPolicyError,
) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    legacy_json(
        StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED),
        json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}
