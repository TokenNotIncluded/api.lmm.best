//! Compatibility implementation of the legacy model-deletion boundary.
//!
//! Model deletion remains separate from the mounted GET catalogue because the
//! legacy contract returns a deliberate `501 Not Implemented`. Focused tests
//! exercise this handler without exposing it on the listener.

use axum::{
    Json,
    extract::Request,
    http::{HeaderMap, StatusCode},
    response::{IntoResponse, Response},
};
use serde::Serialize;

use crate::routes::model_lookup::{
    ModelLookupState, auth_failure, compat_response, model_lookup_request, request_id,
};

pub(crate) async fn delete_model(
    state: ModelLookupState,
    _model: String,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let request_id = request_id(&request);
    let lookup_request = model_lookup_request(&headers, &request);
    if let Err(error) = state
        .service
        .authenticate_model_delete(lookup_request)
        .await
    {
        return auth_failure(error, &state.version, &request_id);
    }

    compat_response(
        (
            StatusCode::NOT_IMPLEMENTED,
            Json(LegacyModelDeleteEnvelope {
                error: LegacyModelDeleteError {
                    message: "API not implemented",
                    kind: "new_api_error",
                    param: "",
                    code: "api_not_implemented",
                },
            }),
        )
            .into_response(),
        &state.version,
        &request_id,
    )
}

#[derive(Serialize)]
struct LegacyModelDeleteEnvelope {
    error: LegacyModelDeleteError,
}

#[derive(Serialize)]
struct LegacyModelDeleteError {
    message: &'static str,
    #[serde(rename = "type")]
    kind: &'static str,
    param: &'static str,
    code: &'static str,
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use async_trait::async_trait;
    use axum::{
        Router,
        body::Body,
        extract::{Path, State},
        http::Request as HttpRequest,
        routing::delete,
    };
    use serde_json::{Value, json};
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    use super::*;
    use crate::{
        models::{ModelView, ModelsError, ModelsErrorKind},
        routes::model_lookup::{ModelLookupRequest, ModelLookupService},
    };

    #[derive(Clone)]
    struct TestService {
        authenticated: Result<(), ModelsErrorKind>,
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
            Ok(None)
        }
    }

    async fn delete_model_route(
        State(state): State<ModelLookupState>,
        Path(model): Path<String>,
        headers: HeaderMap,
        request: Request,
    ) -> Response {
        delete_model(state, model, headers, request).await
    }

    fn app(authenticated: Result<(), ModelsErrorKind>) -> Router {
        Router::new()
            .route("/v1/models/{model}", delete(delete_model_route))
            .with_state(ModelLookupState::new(
                Arc::new(TestService { authenticated }),
                "v0.0.0-test",
            ))
    }

    async fn response_json(response: Response) -> Result<Value, Box<dyn std::error::Error>> {
        let bytes = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        serde_json::from_slice(&bytes).map_err(Into::into)
    }

    #[tokio::test]
    async fn model_delete_returns_the_frozen_501_shape_after_authentication() -> TestResult {
        let response = app(Ok(()))
            .oneshot(
                HttpRequest::delete("/v1/models/gpt-4o")
                    .header("authorization", "Bearer test-token")
                    .body(Body::empty())?,
            )
            .await?;

        assert_eq!(response.status(), StatusCode::NOT_IMPLEMENTED);
        assert_eq!(
            response_json(response).await?,
            json!({"error":{"message":"API not implemented","type":"new_api_error","param":"","code":"api_not_implemented"}})
        );
        Ok(())
    }

    #[tokio::test]
    async fn model_delete_rejects_missing_auth_before_returning_the_frozen_shape() -> TestResult {
        let response = app(Err(ModelsErrorKind::MissingToken))
            .oneshot(HttpRequest::delete("/v1/models/gpt-4o").body(Body::empty())?)
            .await?;

        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            response_json(response).await?["error"]["type"],
            "new_api_error"
        );
        Ok(())
    }
}
