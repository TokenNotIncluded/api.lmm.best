//! Browser compatibility routes inherit the canonical origin, rate limit and
//! private-response headers of legacy_auth. Clients never supply authority.
use super::*;
use crate::identity::{DashboardKeyInput, DashboardKeyQuery, ProfileUpdate};
use axum::{
    extract::{Path, Query},
    routing::put,
};

pub(super) fn routes() -> Router<AuthState> {
    Router::new()
        .route("/api/token", get(keys).post(create).put(update))
        .route("/api/token/", get(keys).post(create).put(update))
        .route("/api/token/search", get(keys))
        .route("/api/token/auto-groups", get(auto_groups))
        .route("/api/token/batch", post(remove_batch))
        .route("/api/token/batch/keys", post(reveal_batch))
        .route("/api/token/{id}", get(key).delete(remove))
        .route("/api/token/{id}/", get(key).delete(remove))
        .route("/api/token/{id}/key", post(reveal))
        .route(
            "/api/token/{id}/account-balance-access",
            put(balance_access),
        )
        .route("/api/user/self/groups", get(groups))
        .route("/api/user/models", get(models))
        .route(
            "/api/user/company-billing-profile",
            get(company).put(save_company),
        )
}
async fn company(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    respond(async { state.store.company_billing_profile(access(&headers)?).await }.await)
}
async fn save_company(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<crate::identity::CompanyBillingProfileInput>, JsonRejection>,
) -> Response {
    let Ok(Json(mut body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    body.normalize();
    if let Some(field) = body.invalid_field() {
        let mut errors = serde_json::Map::new();
        errors.insert(field.to_owned(), json!("invalid"));
        return (
            StatusCode::UNPROCESSABLE_ENTITY,
            Json(
                json!({"success":false,"message":"Invalid invoice identity field","errors":errors}),
            ),
        )
            .into_response();
    }
    respond(
        async {
            state
                .store
                .save_company_billing_profile(access(&headers)?, body)
                .await
        }
        .await,
    )
}
fn access(headers: &HeaderMap) -> Result<&str, IdentityError> {
    bearer(headers)?.ok_or(IdentityError::Unauthorized)
}
fn respond(result: Result<Value, IdentityError>) -> Response {
    match result {
        Ok(value) => success(Some(value)),
        Err(error) => auth_failure(error.into()),
    }
}
async fn keys(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Query(query): Query<DashboardKeyQuery>,
) -> Response {
    respond(async { state.store.dashboard_keys(access(&headers)?, &query).await }.await)
}
async fn key(State(state): State<AuthState>, headers: HeaderMap, Path(id): Path<i64>) -> Response {
    respond(async { state.store.dashboard_key(access(&headers)?, id).await }.await)
}
async fn create(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<DashboardKeyInput>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    respond(
        async {
            state
                .store
                .dashboard_create_key(access(&headers)?, &body)
                .await
        }
        .await,
    )
}
async fn update(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Query(query): Query<DashboardKeyQuery>,
    input: Result<Json<DashboardKeyInput>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    respond(
        async {
            state
                .store
                .dashboard_update_key(access(&headers)?, &body, query.status_only.unwrap_or(false))
                .await
        }
        .await,
    )
}
async fn remove(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Response {
    respond(
        async {
            state
                .store
                .dashboard_delete_keys(access(&headers)?, &[id])
                .await?;
            Ok(Value::Null)
        }
        .await,
    )
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Batch {
    ids: Vec<i64>,
}
async fn remove_batch(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<Batch>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    respond(
        async {
            Ok(json!(
                state
                    .store
                    .dashboard_delete_keys(access(&headers)?, &body.ids)
                    .await?
            ))
        }
        .await,
    )
}
async fn reveal(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
) -> Response {
    respond(
        async {
            let data = state
                .store
                .dashboard_reveal_keys(access(&headers)?, &[id])
                .await?;
            Ok(json!({"key":data["keys"][id.to_string()]}))
        }
        .await,
    )
}
async fn reveal_batch(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<Batch>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    respond(
        async {
            state
                .store
                .dashboard_reveal_keys(access(&headers)?, &body.ids)
                .await
        }
        .await,
    )
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Enabled {
    enabled: bool,
}
async fn balance_access(
    State(state): State<AuthState>,
    headers: HeaderMap,
    Path(id): Path<i64>,
    input: Result<Json<Enabled>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    respond(
        async {
            state
                .store
                .dashboard_balance_access(access(&headers)?, id, body.enabled)
                .await?;
            Ok(Value::Null)
        }
        .await,
    )
}
async fn auto_groups(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    respond(async {Ok(json!({"groups":state.store.dashboard_groups(access(&headers)?).await?,"max_count":16}))}.await)
}
async fn groups(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    respond(
        async {
            let groups = state.store.dashboard_groups(access(&headers)?).await?;
            let mut data = serde_json::Map::new();
            for group in groups.as_array().ok_or(IdentityError::Storage)? {
                let name = group.as_str().ok_or(IdentityError::Storage)?;
                // Pricing multipliers are immutable server price rules and may
                // differ per model; no fabricated universal discount is exposed.
                data.insert(
                    name.to_owned(),
                    json!({"desc":name,"ratio":"model-specific"}),
                );
            }
            Ok(Value::Object(data))
        }
        .await,
    )
}
async fn models(State(state): State<AuthState>, headers: HeaderMap) -> Response {
    respond(
        async {
            Ok(json!(
                state.store.dashboard_models(access(&headers)?).await?
            ))
        }
        .await,
    )
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub(super) struct PasswordUpdate {
    original_password: String,
    password: String,
    display_name: Option<String>,
}

// Both variants reject unknown fields. Mixed password/preferences requests
// fail rather than silently dropping one part of a security-sensitive update.
#[derive(Deserialize)]
#[serde(untagged)]
pub(super) enum ProfileMutation {
    Password(PasswordUpdate),
    Preferences(ProfileUpdate),
}

pub(super) async fn profile(
    State(state): State<AuthState>,
    headers: HeaderMap,
    input: Result<Json<ProfileMutation>, JsonRejection>,
) -> Response {
    let Ok(Json(body)) = input else {
        return auth_failure(IdentityError::Invalid.into());
    };
    match body {
        ProfileMutation::Password(body) => {
            let result = async {
                state
                    .browser
                    .change_password(
                        access(&headers)?,
                        &body.original_password,
                        &body.password,
                        body.display_name.as_deref(),
                        expected_sid(&headers)?,
                    )
                    .await
            }
            .await;
            match result {
                Ok(bundle) => bundle_response(bundle, &state.config),
                Err(error) => auth_failure(error),
            }
        }
        ProfileMutation::Preferences(body) => respond(
            async {
                state
                    .store
                    .update_dashboard_profile(access(&headers)?, &body)
                    .await?;
                Ok(Value::Null)
            }
            .await,
        ),
    }
}
