use axum::{Json, Router, http::StatusCode, routing::get};
use serde_json::{Value, json};

pub fn router() -> Router {
    Router::new()
        .route(
            "/health/live",
            get(|| async { Json(json!({"status": "alive", "service": "lmm-core"})) }),
        )
        .route("/health/ready", get(unavailable))
        .fallback(unavailable)
}

async fn unavailable() -> (StatusCode, [(String, String); 1], Json<Value>) {
    // No environment variable can turn an incomplete ledger into a ready core.
    // Never forward an uncharged request, silently call Go, or fake a balance.
    (
        StatusCode::SERVICE_UNAVAILABLE,
        [("cache-control".into(), "no-store".into())],
        Json(
            json!({"error": {"code": "core_migration_incomplete", "message": "Core billing, identity and forwarding are not enabled. Keep production on the existing Go service."}}),
        ),
    )
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{body::Body, http::Request};
    use tower::ServiceExt;

    #[tokio::test]
    async fn process_health_does_not_advertise_billing_readiness() {
        for (path, status) in [
            ("/health/live", StatusCode::OK),
            ("/health/ready", StatusCode::SERVICE_UNAVAILABLE),
            ("/v1/chat/completions", StatusCode::SERVICE_UNAVAILABLE),
            ("/api/user/self", StatusCode::SERVICE_UNAVAILABLE),
        ] {
            let response = router()
                .oneshot(Request::builder().uri(path).body(Body::empty()).unwrap())
                .await
                .unwrap();
            assert_eq!(response.status(), status, "{path}");
        }
    }

    #[tokio::test]
    async fn model_calls_fail_before_any_upstream_or_extension_work() {
        let response = router()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/v1/responses")
                    .header("authorization", "Bearer a-test-key")
                    .body(Body::from(r#"{"model":"test","stream":true}"#))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(response.headers()["cache-control"], "no-store");
    }
}
