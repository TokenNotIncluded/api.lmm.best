//! Legacy `GET /v1/models/:model` compatibility boundary.
//!
//! The other methods in this route family already have non-overlapping owners:
//! the Gemini relay slice owns `POST /v1(models|beta/models)/*path` and model
//! deletion, while the billing slice owns the dashboard aliases.  Gin still
//! exposed one missing method: retrieving a *single* static model descriptor.
//!
//! This module intentionally owns only that GET method.  Its service port
//! separates token authentication from the static catalogue lookup because the
//! legacy lookup is not filtered by the caller's currently enabled models.

use std::{
    collections::HashMap,
    net::IpAddr,
    sync::{Arc, OnceLock},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{Path, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{MethodRouter, get},
};
use serde::{Deserialize, Serialize};
use sqlx::PgPool;

use crate::{
    RequestContext,
    models::{ModelView, ModelsError, ModelsErrorKind, ModelsRequest, PgModelsService},
};

const FROZEN_CATALOG: &str = include_str!("../../assets/legacy-static-model-catalog.json");

#[derive(Deserialize)]
struct FrozenModel {
    id: String,
    owned_by: String,
    #[serde(default)]
    supported_endpoint_types: Option<Vec<String>>,
}

fn static_catalog() -> Result<&'static HashMap<String, ModelView>, ModelsError> {
    static CATALOG: OnceLock<Option<HashMap<String, ModelView>>> = OnceLock::new();
    CATALOG
        .get_or_init(|| {
            serde_json::from_str::<Vec<FrozenModel>>(FROZEN_CATALOG)
                .ok()
                .map(|models| {
                    models
                        .into_iter()
                        .map(|model| {
                            let id = model.id;
                            let view = ModelView {
                                id: id.clone(),
                                object: "model",
                                created: 1_626_777_600,
                                owned_by: model.owned_by,
                                supported_endpoint_types: model
                                    .supported_endpoint_types
                                    .unwrap_or_default(),
                            };
                            (id, view)
                        })
                        .collect()
                })
        })
        .as_ref()
        .ok_or_else(|| {
            ModelsError::new(
                ModelsErrorKind::Database,
                "Static model catalogue is unavailable",
            )
        })
}

/// PostgreSQL token authentication with the frozen Go static model map.
pub struct PgStaticModelLookup {
    authentication: PgModelsService,
    enforce_discovery_policy: bool,
}

impl PgStaticModelLookup {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self {
            authentication: PgModelsService::new(pg),
            enforce_discovery_policy: false,
        }
    }

    /// Builds the normal-listener lookup with the current Go trust gate.
    ///
    /// The static catalogue remains process-local, while authorization still
    /// consults PostgreSQL for the user's persisted trust/payment facts.  The
    /// loopback-only acceptance flag is shared with the normal listener and
    /// never changes the frozen test-instance constructor above.
    #[must_use]
    pub fn with_current_policy(pg: PgPool, local_acceptance: bool) -> Self {
        Self {
            authentication: PgModelsService::new(pg).with_local_acceptance(local_acceptance),
            enforce_discovery_policy: true,
        }
    }
}

#[async_trait]
impl ModelLookupService for PgStaticModelLookup {
    async fn authenticate(&self, request: ModelLookupRequest) -> Result<(), ModelsError> {
        let result = self
            .authentication
            .authenticate_only_with_policy(request.into(), self.enforce_discovery_policy)
            .await;
        if self.enforce_discovery_policy
            && result.as_ref().is_err_and(|error| {
                matches!(
                    error.kind,
                    ModelsErrorKind::MissingToken
                        | ModelsErrorKind::InvalidToken
                        | ModelsErrorKind::DiscoveryHidden
                )
            })
        {
            return Err(ModelsError::new(
                ModelsErrorKind::DiscoveryHidden,
                "Not Found",
            ));
        }
        result
    }

    async fn authenticate_model_delete(
        &self,
        request: ModelLookupRequest,
    ) -> Result<(), ModelsError> {
        self.authentication.authenticate_only(request.into()).await
    }

    async fn find_static_model(&self, model: &str) -> Result<Option<ModelView>, ModelsError> {
        Ok(static_catalog()?.get(model).cloned())
    }
}

/// The credentials and trusted client address supplied to legacy token auth.
#[derive(Clone, Debug)]
pub struct ModelLookupRequest {
    pub authorization: Option<String>,
    pub api_key: Option<String>,
    pub client_ip: IpAddr,
}

impl From<ModelLookupRequest> for ModelsRequest {
    fn from(value: ModelLookupRequest) -> Self {
        Self {
            authorization: value.authorization,
            api_key: value.api_key,
            gemini_key: None,
            mj_api_secret: None,
            client_ip: value.client_ip,
        }
    }
}

/// Authentication plus the legacy process-wide static model catalogue.
///
/// The lookup must not be implemented by calling `GET /v1/models` and filtering
/// it: Go's `RetrieveModel` uses `openAIModelsMap`, which is independent from
/// group visibility and token model limits.
#[async_trait]
pub trait ModelLookupService: Send + Sync {
    async fn authenticate(&self, request: ModelLookupRequest) -> Result<(), ModelsError>;
    /// Authentication for the frozen model-deletion boundary.
    ///
    /// The current normal listener applies a discovery/trust policy to the
    /// GET catalogue lookup, while the legacy deletion boundary stops after
    /// ordinary token middleware. Keep that distinction explicit at the
    /// service boundary instead of accidentally hiding a valid token.
    async fn authenticate_model_delete(
        &self,
        request: ModelLookupRequest,
    ) -> Result<(), ModelsError> {
        self.authenticate(request).await
    }
    async fn find_static_model(&self, model: &str) -> Result<Option<ModelView>, ModelsError>;
}

#[derive(Clone)]
pub struct ModelLookupState {
    pub(crate) service: Arc<dyn ModelLookupService>,
    pub(crate) version: Arc<str>,
}

impl ModelLookupState {
    #[must_use]
    pub fn new(service: Arc<dyn ModelLookupService>, version: impl Into<Arc<str>>) -> Self {
        Self {
            service,
            version: version.into(),
        }
    }
}

/// The GET half of the shared `/v1/models/{model}` method router.
///
/// It deliberately captures its state in the handler closure.  That permits
/// the composition root to add it to the relay slice's POST/DELETE methods in
/// one Axum `MethodRouter`, rather than attempting to merge overlapping route
/// patterns (which Axum rejects at router construction time).
pub fn model_lookup_method_router(state: ModelLookupState) -> MethodRouter {
    let get_state = state;
    get(
        move |Path(model): Path<String>, headers: HeaderMap, request: Request| {
            let state = get_state.clone();
            async move { retrieve_model(state, model, headers, request).await }
        },
    )
}

/// Standalone lookup router retained for focused compatibility tests.
///
/// Production composition must use [`model_lookup_method_router`] to join this
/// GET handler with the relay slice's exact POST and DELETE methods.
pub fn model_lookup_router(state: ModelLookupState) -> Router {
    Router::new()
        .route("/v1/models/{model}", get(retrieve_model_with_state))
        .with_state(state)
}

/// GET-only normal-listener composition. The model-deletion boundary remains a
/// candidate until its frozen differential evidence and independent approval
/// are accepted by the migration gate.
pub fn model_lookup_get_router(state: ModelLookupState) -> Router {
    Router::new()
        .route("/v1/models/{model}", get(retrieve_model_with_state))
        .with_state(state)
}

async fn retrieve_model_with_state(
    State(state): State<ModelLookupState>,
    Path(model): Path<String>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    retrieve_model(state, model, headers, request).await
}

async fn retrieve_model(
    state: ModelLookupState,
    model: String,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let request_id = request_id(&request);
    let anthropic = headers
        .get("x-api-key")
        .is_some_and(|value| !value.is_empty())
        && headers
            .get("anthropic-version")
            .is_some_and(|value| !value.is_empty());
    let lookup_request = ModelLookupRequest {
        authorization: headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned),
        api_key: header_value(&headers, "x-api-key"),
        client_ip: client_ip(&headers, &request),
    };

    if let Err(error) = state.service.authenticate(lookup_request).await {
        return auth_failure(error, &state.version, &request_id);
    }

    match state.service.find_static_model(&model).await {
        Ok(Some(model)) if anthropic => compat_response(
            Json(AnthropicModel {
                id: model.id.clone(),
                created_at: "2021-07-20T10:40:00Z",
                display_name: model.id,
                kind: "model",
            })
            .into_response(),
            &state.version,
            &request_id,
        ),
        Ok(Some(model)) => {
            compat_response(Json(model).into_response(), &state.version, &request_id)
        }
        Ok(None) => compat_response(
            Json(ModelNotFoundEnvelope {
                error: ModelNotFoundError {
                    message: format!("The model '{model}' does not exist"),
                    kind: "invalid_request_error",
                    param: "model",
                    code: "model_not_found",
                },
            })
            .into_response(),
            &state.version,
            &request_id,
        ),
        Err(error) => auth_failure(error, &state.version, &request_id),
    }
}

pub(crate) fn model_lookup_request(headers: &HeaderMap, request: &Request) -> ModelLookupRequest {
    ModelLookupRequest {
        authorization: headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned),
        api_key: header_value(headers, "x-api-key"),
        client_ip: client_ip(headers, request),
    }
}

fn header_value(headers: &HeaderMap, name: &str) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
}

fn client_ip(headers: &HeaderMap, request: &Request) -> IpAddr {
    request
        .extensions()
        .get::<RequestContext>()
        .and_then(|context| context.client_ip)
        .or_else(|| {
            headers
                .get("x-real-ip")
                .and_then(|value| value.to_str().ok())
                .and_then(|value| value.parse().ok())
        })
        .unwrap_or(IpAddr::V4(std::net::Ipv4Addr::LOCALHOST))
}

pub(crate) fn request_id(request: &Request) -> String {
    request.extensions().get::<RequestContext>().map_or_else(
        || uuid::Uuid::new_v4().to_string(),
        |context| context.request_id.clone(),
    )
}

pub(crate) fn auth_failure(error: ModelsError, version: &str, request_id: &str) -> Response {
    // The frozen Go relay mounts `/v1/models/:model` behind `TokenAuth`,
    // which reports both a missing and an invalid credential as the OpenAI
    // error envelope.  Keep the current-listener discovery concealment
    // separate: only an explicit trust decision is a generic 404.
    if error.kind == ModelsErrorKind::DiscoveryHidden {
        return compat_response(
            (
                StatusCode::NOT_FOUND,
                Json(DiscoveryNotFoundEnvelope {
                    message: "Not Found",
                }),
            )
                .into_response(),
            version,
            request_id,
        );
    }

    let status = match error.kind {
        ModelsErrorKind::MissingToken | ModelsErrorKind::InvalidToken => StatusCode::UNAUTHORIZED,
        ModelsErrorKind::AccessDenied | ModelsErrorKind::UserBanned => StatusCode::FORBIDDEN,
        ModelsErrorKind::Database => StatusCode::INTERNAL_SERVER_ERROR,
        ModelsErrorKind::DiscoveryHidden => unreachable!("handled above"),
    };
    compat_response(
        (
            status,
            Json(AuthorizationErrorEnvelope {
                error: AuthorizationError {
                    message: format!("{} (request id: {request_id})", error.message),
                    kind: "new_api_error",
                    code: if error.message == "您的 IP 不在令牌允许访问的列表中" {
                        "access_denied"
                    } else {
                        ""
                    },
                },
            }),
        )
            .into_response(),
        version,
        request_id,
    )
}

pub(crate) fn compat_response(mut response: Response, version: &str, request_id: &str) -> Response {
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    if let Ok(value) = HeaderValue::from_str(version) {
        response.headers_mut().insert("x-new-api-version", value);
    }
    if let Ok(value) = HeaderValue::from_str(request_id) {
        response.headers_mut().insert("x-oneapi-request-id", value);
    }
    response
}

#[derive(Serialize)]
struct AnthropicModel {
    id: String,
    created_at: &'static str,
    display_name: String,
    #[serde(rename = "type")]
    kind: &'static str,
}

#[derive(Serialize)]
struct ModelNotFoundEnvelope {
    error: ModelNotFoundError,
}

#[derive(Serialize)]
struct ModelNotFoundError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    param: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct AuthorizationErrorEnvelope {
    error: AuthorizationError,
}

#[derive(Serialize)]
struct AuthorizationError {
    message: String,
    #[serde(rename = "type")]
    kind: &'static str,
    code: &'static str,
}

#[derive(Serialize)]
struct DiscoveryNotFoundEnvelope {
    message: &'static str,
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request as HttpRequest};
    use serde_json::{Value, json};
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[derive(Clone)]
    struct TestService {
        authenticated: Result<(), ModelsErrorKind>,
        model: Option<ModelView>,
    }

    #[async_trait]
    impl ModelLookupService for TestService {
        async fn authenticate(&self, _: ModelLookupRequest) -> Result<(), ModelsError> {
            self.authenticated
                .as_ref()
                .map(|_| ())
                .map_err(|kind| ModelsError::new(*kind, "Invalid token"))
        }

        async fn find_static_model(&self, _: &str) -> Result<Option<ModelView>, ModelsError> {
            Ok(self.model.clone())
        }
    }

    fn app(authenticated: Result<(), ModelsErrorKind>, model: Option<ModelView>) -> Router {
        model_lookup_router(ModelLookupState::new(
            Arc::new(TestService {
                authenticated,
                model,
            }),
            "v0.0.0-test",
        ))
    }

    async fn json_body(response: Response) -> Result<Value, Box<dyn std::error::Error>> {
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        Ok(serde_json::from_slice(&body)?)
    }

    #[tokio::test]
    async fn openai_lookup_returns_the_legacy_static_model_shape() -> TestResult {
        let response = app(Ok(()), Some(ModelView::new("gpt-4o", "openai")))
            .oneshot(
                HttpRequest::get("/v1/models/gpt-4o")
                    .header("authorization", "Bearer test-token")
                    .body(Body::empty())?,
            )
            .await?;

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response.headers()["x-new-api-version"], "v0.0.0-test");
        assert_eq!(
            json_body(response).await?,
            json!({"id":"gpt-4o","object":"model","created":1626777600,"owned_by":"openai","supported_endpoint_types":[]})
        );
        Ok(())
    }

    #[tokio::test]
    async fn anthropic_lookup_uses_its_distinct_legacy_shape() -> TestResult {
        let response = app(Ok(()), Some(ModelView::new("claude-test", "claude")))
            .oneshot(
                HttpRequest::get("/v1/models/claude-test")
                    .header("x-api-key", "test-token")
                    .header("anthropic-version", "2023-06-01")
                    .body(Body::empty())?,
            )
            .await?;

        assert_eq!(
            json_body(response).await?,
            json!({"id":"claude-test","created_at":"2021-07-20T10:40:00Z","display_name":"claude-test","type":"model"})
        );
        Ok(())
    }

    #[tokio::test]
    async fn unknown_model_is_a_success_status_with_the_frozen_error_body() -> TestResult {
        let response = app(Ok(()), None)
            .oneshot(
                HttpRequest::get("/v1/models/missing")
                    .header("authorization", "Bearer test-token")
                    .body(Body::empty())?,
            )
            .await?;

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            json_body(response).await?,
            json!({"error":{"message":"The model 'missing' does not exist","type":"invalid_request_error","param":"model","code":"model_not_found"}})
        );
        Ok(())
    }

    #[tokio::test]
    async fn rejected_token_uses_the_legacy_openai_error_envelope() -> TestResult {
        let response = app(
            Err(ModelsErrorKind::InvalidToken),
            Some(ModelView::new("must-not-leak", "openai")),
        )
        .oneshot(HttpRequest::get("/v1/models/must-not-leak").body(Body::empty())?)
        .await?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = json_body(response).await?;
        assert_eq!(body["error"]["type"], "new_api_error");
        assert_eq!(body["error"]["code"], "");
        let message = body["error"]["message"].as_str().unwrap_or_default();
        assert!(message.starts_with("Invalid token (request id: "));
        Ok(())
    }

    #[tokio::test]
    async fn missing_token_uses_the_legacy_openai_error_envelope() -> TestResult {
        let response = app(
            Err(ModelsErrorKind::MissingToken),
            Some(ModelView::new("must-not-leak", "openai")),
        )
        .oneshot(HttpRequest::get("/v1/models/must-not-leak").body(Body::empty())?)
        .await?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = json_body(response).await?;
        assert_eq!(body["error"]["type"], "new_api_error");
        assert_eq!(body["error"]["code"], "");
        let message = body["error"]["message"].as_str().unwrap_or_default();
        assert!(message.starts_with("Invalid token (request id: "));
        Ok(())
    }

    #[tokio::test]
    async fn discovery_hidden_token_is_not_leaked_as_auth_error() -> TestResult {
        let response = app(
            Err(ModelsErrorKind::DiscoveryHidden),
            Some(ModelView::new("must-not-leak", "openai")),
        )
        .oneshot(
            HttpRequest::get("/v1/models/must-not-leak")
                .header("authorization", "Bearer test-token")
                .body(Body::empty())?,
        )
        .await?;

        assert_eq!(response.status(), StatusCode::NOT_FOUND);
        assert_eq!(json_body(response).await?, json!({"message":"Not Found"}));
        Ok(())
    }
}
