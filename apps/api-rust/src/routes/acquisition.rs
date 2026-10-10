//! Current-Go consent, attribution and promotion-link foundation.
mod data;
mod store;
pub use data::{
    Input, Link, Visit, correction_reason, label, normalize, self_report, visitor_hash,
};
pub use store::{Error, PgAcquisitionStore};

use super::legacy_http::{
    dashboard_credential, legacy_json, localized_dashboard_auth_error, localized_user_policy_error,
};
use crate::{
    RequestContext,
    auth::{
        AuthErrorKind, DashboardAuth, DashboardUserView, UserAuthPolicyError,
        dashboard_token_candidate, enforce_user_auth_view,
    },
    legacy_empty_response,
};
use axum::{
    Router,
    body::to_bytes,
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::Response,
    routing::{delete, get, post, put},
};
use data::{INVALID, random_id, unescape};
use secrecy::SecretString;
use serde::Deserialize;
use serde_json::{Value, json};
use std::{sync::Arc, time::Duration};

#[derive(Clone)]
pub struct AcquisitionState {
    store: PgAcquisitionStore,
    auth: Arc<dyn DashboardAuth>,
    cookie_secure: bool,
}
impl AcquisitionState {
    pub fn new(
        store: PgAcquisitionStore,
        auth: Arc<dyn DashboardAuth>,
        cookie_secure: bool,
    ) -> Self {
        Self {
            store,
            auth,
            cookie_secure,
        }
    }
}
pub fn router(state: AcquisitionState) -> Router {
    Router::new()
        .route("/api/acquisition/visit", post(visit))
        .route("/api/acquisition/consent", post(grant).delete(withdraw))
        .route(
            "/api/acquisition/self-report",
            get(read_report).put(save_report).delete(delete_report),
        )
        .route("/api/admin/acquisition/links", get(links).post(save_link))
        .route("/api/admin/acquisition/links/{id}", delete(delete_link))
        .route("/api/admin/acquisition/links/{id}/preview", get(preview))
        .route("/api/admin/acquisition/lookback", put(lookback))
        .route(
            "/api/admin/acquisition/users/{id}/corrections",
            get(corrections).post(save_correction),
        )
        .with_state(state)
}
#[derive(Clone, Copy)]
enum Op {
    Visit,
    Grant,
    Withdraw,
    ReadReport,
    SaveReport,
    DeleteReport,
    Links,
    SaveLink,
    DeleteLink,
    Preview,
    Lookback,
    Corrections,
    SaveCorrection,
}
impl Op {
    fn admin(self) -> bool {
        matches!(
            self,
            Self::Links
                | Self::SaveLink
                | Self::DeleteLink
                | Self::Preview
                | Self::Lookback
                | Self::Corrections
                | Self::SaveCorrection
        )
    }
    fn optional(self) -> bool {
        matches!(self, Self::Visit | Self::Withdraw)
    }
}
macro_rules! handler {
    ($name:ident,$op:ident) => {
        async fn $name(State(state): State<AcquisitionState>, request: Request) -> Response {
            dispatch(state, request, Op::$op).await
        }
    };
}
handler!(visit, Visit);
handler!(grant, Grant);
handler!(withdraw, Withdraw);
handler!(read_report, ReadReport);
handler!(save_report, SaveReport);
handler!(delete_report, DeleteReport);
handler!(links, Links);
handler!(save_link, SaveLink);
handler!(delete_link, DeleteLink);
handler!(preview, Preview);
handler!(lookback, Lookback);
handler!(corrections, Corrections);
handler!(save_correction, SaveCorrection);
fn no_cache(mut response: Response) -> Response {
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
fn failure(error: Error) -> Response {
    let message = match error {
        Error::Invalid(message) => message.to_owned(),
        Error::Conflict(message) => {
            return legacy_json(
                StatusCode::CONFLICT,
                json!({"success":false,"message":message}),
            );
        }
        Error::Database(sqlx::Error::RowNotFound) => "record not found".into(),
        Error::Database(error) => {
            if let Some(db) = error.as_database_error() {
                format!(
                    "ERROR: {} (SQLSTATE {})",
                    db.message(),
                    db.code().unwrap_or_default()
                )
            } else {
                error.to_string()
            }
        }
    };
    legacy_json(StatusCode::OK, json!({"success":false,"message":message}))
}
fn success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success":true,"message":"","data":data}),
    )
}
pub(crate) fn cookie_value(headers: &HeaderMap) -> Option<String> {
    for value in headers.get_all(header::COOKIE) {
        for part in value.to_str().ok()?.split(';') {
            if let Some(value) = part.trim().strip_prefix("lmm_acquisition=") {
                return unescape(value.trim_matches('"'), true)
                    .ok()
                    .filter(|v| v.len() == 32);
            }
        }
    }
    None
}
fn cookie(mut response: Response, value: &str, secure: bool, withdraw: bool) -> Response {
    let raw = format!(
        "lmm_acquisition={value}; Path=/; Max-Age={}; HttpOnly{}{}",
        if withdraw { 0 } else { 30 * 86400 },
        if secure { "; Secure" } else { "" },
        if withdraw { "" } else { "; SameSite=Lax" }
    );
    if let Ok(value) = HeaderValue::from_str(&raw) {
        response.headers_mut().insert(header::SET_COOKIE, value);
    }
    response
}
async fn actor(
    state: &AcquisitionState,
    headers: &HeaderMap,
    op: Op,
) -> Result<Option<DashboardUserView>, Response> {
    let Some(token) = dashboard_credential(headers) else {
        return if op.optional() {
            Ok(None)
        } else {
            Err(localized_dashboard_auth_error(headers, None))
        };
    };
    let internal = dashboard_token_candidate(&token);
    match state
        .auth
        .self_user_view_for_optional(SecretString::from(token))
        .await
    {
        Ok(user) => {
            if !op.optional() {
                enforce_user_auth_view(&user)
                    .map_err(|e| localized_user_policy_error(headers, e))?;
            }
            if op.admin() && user.role < 10 {
                return Err(localized_user_policy_error(
                    headers,
                    UserAuthPolicyError::InsufficientPrivilege,
                ));
            }
            Ok(Some(user))
        }
        Err(error)
            if op.optional()
                && !internal
                && matches!(
                    error.kind,
                    AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials
                ) =>
        {
            Ok(None)
        }
        Err(error) => Err(localized_dashboard_auth_error(headers, Some(error.kind))),
    }
}
fn oversized(headers: &HeaderMap) -> bool {
    headers
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.parse::<u64>().ok())
        .is_some_and(|v| v > 4096)
}
fn query(request: &Request, key: &str) -> Option<String> {
    form_urlencoded::parse(request.uri().query().unwrap_or("").as_bytes())
        .find(|(k, _)| k == key)
        .map(|(_, v)| v.into_owned())
}
async fn dispatch(state: AcquisitionState, request: Request, op: Op) -> Response {
    if !op.admin() && oversized(request.headers()) {
        return no_cache(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
    }
    let actor = match actor(&state, request.headers(), op).await {
        Ok(actor) => actor,
        Err(response) => {
            return if op.admin() {
                response
            } else {
                no_cache(response)
            };
        }
    };
    if op.admin() && oversized(request.headers()) {
        return no_cache(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
    }
    if op.admin() {
        let user = actor.as_ref().expect("admin");
        let actions: &[&str] = match op {
            Op::Links | Op::Preview => &["read"],
            Op::Corrections => &["details"],
            Op::SaveCorrection => &["details", "write"],
            _ => &["write"],
        };
        for action in actions {
            if !matches!(
                state.store.permission(user.id, user.role, action).await,
                Ok(true)
            ) {
                return no_cache(legacy_json(
                    StatusCode::FORBIDDEN,
                    json!({"success":false,"message":if request.headers().get(header::ACCEPT_LANGUAGE).and_then(|v|v.to_str().ok()).is_some_and(|v|v.starts_with("zh")){"无权进行此操作，权限不足"}else{"Insufficient permissions"}}),
                ));
            }
        }
    }
    let timeout = if matches!(op, Op::Visit) {
        Duration::from_millis(500)
    } else {
        Duration::from_secs(3)
    };
    let response = match tokio::time::timeout(timeout, execute(&state, request, actor.as_ref(), op))
        .await
    {
        Ok(response) => response,
        Err(_) if matches!(op, Op::Visit) => legacy_empty_response(StatusCode::NO_CONTENT, None),
        Err(_) => failure(Error::Invalid("context deadline exceeded")),
    };
    no_cache(response)
}
async fn execute(
    state: &AcquisitionState,
    request: Request,
    user: Option<&DashboardUserView>,
    op: Op,
) -> Response {
    let user_id = user.map_or(0, |u| u.id);
    let role = user.map_or(0, |u| u.role);
    let raw_cookie = cookie_value(request.headers());
    let visitor = raw_cookie.as_deref().map(visitor_hash);
    if matches!(op, Op::Withdraw) {
        if user_id == 0
            && request
                .headers()
                .get(header::AUTHORIZATION)
                .and_then(|v| v.to_str().ok())
                .is_some_and(|v| !v.trim().is_empty())
        {
            return legacy_json(
                StatusCode::UNAUTHORIZED,
                json!({"success":false,"message":"Authentication required"}),
            );
        }
        return match state.store.withdraw(visitor.as_deref(), user_id).await {
            Ok(()) => cookie(
                legacy_empty_response(StatusCode::NO_CONTENT, None),
                "",
                state.cookie_secure,
                true,
            ),
            Err(e) => failure(e),
        };
    }
    if matches!(op, Op::Grant) {
        return match state.store.grant(user_id).await {
            Ok(()) => success(json!({"allowed":true,"version":2})),
            Err(e) => failure(e),
        };
    }
    if matches!(op, Op::Links) {
        let page = query(&request, "page")
            .and_then(|v| v.parse::<i64>().ok())
            .unwrap_or(0)
            .max(1);
        let size = query(&request, "page_size")
            .filter(|v| !v.is_empty())
            .map_or(100, |v| v.parse::<i64>().unwrap_or(0));
        return match state
            .store
            .links(
                page,
                size,
                &query(&request, "status").unwrap_or_else(|| "all".into()),
                &query(&request, "q").unwrap_or_default(),
            )
            .await
        {
            Ok(v) => success(v),
            Err(e) => failure(e),
        };
    }
    let correction_target = if matches!(op, Op::Corrections | Op::SaveCorrection) {
        let target = request
            .uri()
            .path()
            .trim_end_matches("/corrections")
            .rsplit('/')
            .next()
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|value| *value > 0);
        let Some(target) = target else {
            return legacy_empty_response(StatusCode::BAD_REQUEST, None);
        };
        let target_role = match state.store.target_role(target).await {
            Ok(Some(role)) => role,
            Ok(None) | Err(Error::Database(sqlx::Error::RowNotFound)) => {
                return legacy_empty_response(StatusCode::NOT_FOUND, None);
            }
            Err(error) => return failure(error),
        };
        if target_role >= 10 || (role < 100 && role <= target_role) {
            return legacy_empty_response(StatusCode::FORBIDDEN, None);
        }
        if matches!(op, Op::Corrections) {
            return match state.store.corrections(target).await {
                Ok(value) => success(value),
                Err(error) => failure(error),
            };
        }
        Some(target)
    } else {
        None
    };
    if matches!(op, Op::Preview) {
        let id = request
            .uri()
            .path()
            .trim_end_matches("/preview")
            .rsplit('/')
            .next()
            .unwrap_or("");
        return match state.store.preview(id).await {
            Ok(v) => success(v),
            Err(e) => failure(e),
        };
    }
    let ip = request
        .extensions()
        .get::<RequestContext>()
        .and_then(|c| c.client_ip)
        .map(|v| v.to_string())
        .unwrap_or_default();
    let pat =
        dashboard_credential(request.headers()).is_some_and(|v| !dashboard_token_candidate(&v));
    if matches!(op, Op::DeleteLink) {
        let id = request.uri().path().rsplit('/').next().unwrap_or("");
        return match state.store.delete_link(id).await {
            Ok(()) => {
                let u = user.unwrap();
                state
                    .store
                    .audit(
                        u.id,
                        u.role,
                        &u.username,
                        pat,
                        &ip,
                        "acquisition.link.delete",
                        json!({"link_id":id}),
                    )
                    .await;
                success(json!({"id":id,"deleted":true}))
            }
            Err(e) => failure(e),
        };
    }
    if matches!(op, Op::ReadReport | Op::DeleteReport) {
        return match state
            .store
            .report(user_id, None, matches!(op, Op::DeleteReport))
            .await
        {
            Ok(v) => legacy_json(StatusCode::OK, json!({"success":true,"data":v})),
            Err(e) => failure(e),
        };
    }
    let own = request
        .headers()
        .get(header::HOST)
        .and_then(|v| v.to_str().ok())
        .unwrap_or("")
        .split(':')
        .next()
        .unwrap_or("")
        .to_owned();
    let parsed = match to_bytes(request.into_body(), 4096).await {
        Ok(body) => Input::deserialize(&mut serde_json::Deserializer::from_slice(&body)).ok(),
        Err(_) => None,
    };
    let Some(input) = parsed else {
        return if matches!(op, Op::Visit) {
            legacy_empty_response(StatusCode::NO_CONTENT, None)
        } else if matches!(op, Op::SaveCorrection) {
            legacy_empty_response(StatusCode::BAD_REQUEST, None)
        } else {
            failure(INVALID.into())
        };
    };
    match op {
        Op::Visit => {
            if role >= 10 {
                return legacy_empty_response(StatusCode::NO_CONTENT, None);
            }
            let raw = raw_cookie.unwrap_or_else(random_id);
            let hash = visitor_hash(&raw);
            match state
                .store
                .observe(&hash, user_id, &input, &[&own, "lmm.best"])
                .await
            {
                Ok(_) => cookie(
                    legacy_empty_response(StatusCode::NO_CONTENT, None),
                    &raw,
                    state.cookie_secure,
                    false,
                ),
                Err(_) => legacy_empty_response(StatusCode::NO_CONTENT, None),
            }
        }
        Op::SaveLink => match state.store.save_link(input).await {
            Ok(v) => success(serde_json::to_value(v).unwrap()),
            Err(e) => failure(e),
        },
        Op::SaveReport => match state.store.report(user_id, Some(input), false).await {
            Ok(v) => legacy_json(StatusCode::OK, json!({"success":true,"data":v})),
            Err(e) => failure(e),
        },
        Op::Lookback => {
            let days = input.int("days");
            match state.store.lookback(days).await {
                Ok(()) => {
                    let u = user.unwrap();
                    state
                        .store
                        .audit(
                            u.id,
                            u.role,
                            &u.username,
                            pat,
                            &ip,
                            "acquisition.lookback",
                            json!({"days":days}),
                        )
                        .await;
                    success(json!({"days":days}))
                }
                Err(e) => failure(e),
            }
        }
        Op::SaveCorrection => {
            match state
                .store
                .save_correction(correction_target.expect("validated target"), user_id, input)
                .await
            {
                Ok(value) => success(value),
                Err(error) => failure(error),
            }
        }
        _ => unreachable!(),
    }
}
