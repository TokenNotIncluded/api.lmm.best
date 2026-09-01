use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::admin_catalog::{
    AdminCatalogActor, AdminCatalogAuthorizer, AdminCatalogState, CatalogError, CatalogOperation,
    MemoryCatalogProvider, router,
};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone)]
struct Authorizer {
    result: Result<AdminCatalogActor, CatalogError>,
}

#[async_trait]
impl AdminCatalogAuthorizer for Authorizer {
    async fn authorize(&self, _: &HeaderMap) -> Result<AdminCatalogActor, CatalogError> {
        self.result.clone()
    }
}

fn app(
    provider: Arc<MemoryCatalogProvider>,
    result: Result<AdminCatalogActor, CatalogError>,
) -> axum::Router {
    router(AdminCatalogState::new(
        provider,
        Arc::new(Authorizer { result }),
    ))
}

fn administrator() -> AdminCatalogActor {
    AdminCatalogActor {
        user_id: 7,
        role: 10,
    }
}

async fn call(app: &axum::Router, request: Request<Body>) -> (StatusCode, Value) {
    let response = app.clone().oneshot(request).await.expect("response");
    let status = response.status();
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    let body = serde_json::from_slice(&bytes).expect("json body");
    (status, body)
}

#[tokio::test]
async fn missing_credential_returns_legacy_unauthorized_envelope_before_provider_execution() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let (status, body) = call(
        &app(Arc::clone(&provider), Err(CatalogError::Unauthorized)),
        Request::builder()
            .uri("/api/models/?p=2")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::UNAUTHORIZED);
    assert_eq!(
        body,
        json!({"success": false, "message": "Unauthorized, invalid access token", "code": "AUTH_UNAUTHORIZED"})
    );
    assert!(provider.calls().expect("calls").is_empty());
}

#[tokio::test]
async fn non_administrator_is_rejected_before_provider_execution() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let (status, body) = call(
        &app(
            Arc::clone(&provider),
            Ok(AdminCatalogActor {
                user_id: 7,
                role: 1,
            }),
        ),
        Request::builder()
            .method("POST")
            .uri("/api/redemption/")
            .header("content-type", "application/json")
            .body(Body::from(r#"{"name":"trial"}"#))
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(
        body,
        json!({"success": false, "message": "管理员权限不足", "code": "AUTH_INSUFFICIENT_PRIVILEGE"})
    );
    assert!(provider.calls().expect("calls").is_empty());
}

#[tokio::test]
async fn model_search_preserves_query_and_uses_the_normalized_operation() {
    let provider = Arc::new(MemoryCatalogProvider::new(json!({"items": []})));
    let (_, body) = call(
        &app(Arc::clone(&provider), Ok(administrator())),
        Request::builder()
            .uri("/api/models/search?keyword=gpt&p=2")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(
        body,
        json!({"success": true, "message": "", "data": {"items": []}})
    );
    assert_eq!(
        provider.calls().expect("calls"),
        vec![lmm_api_rs::routes::admin_catalog::CatalogCall {
            operation: CatalogOperation::SearchModels,
            actor: administrator(),
            resource_id: None,
            input: json!({"keyword": "gpt", "p": "2"}),
        }]
    );
}

#[tokio::test]
async fn unsupported_catalog_method_keeps_axums_405_response() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let response = app(provider, Ok(administrator()))
        .oneshot(
            Request::builder()
                .method("PATCH")
                .uri("/api/vendors/")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}

#[tokio::test]
async fn invalid_item_id_returns_legacy_business_envelope_without_provider_call() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let (status, body) = call(
        &app(Arc::clone(&provider), Ok(administrator())),
        Request::builder()
            .uri("/api/prefill_group/not-a-number")
            .method("DELETE")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, json!({"success": false, "message": "id 参数错误"}));
    assert!(provider.calls().expect("calls").is_empty());
}

#[tokio::test]
async fn missing_models_preserves_hand_written_go_envelope_and_nil_slice() {
    let provider = Arc::new(MemoryCatalogProvider::new(json!([])));
    let (status, body) = call(
        &app(Arc::clone(&provider), Ok(administrator())),
        Request::builder()
            .uri("/api/models/missing")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, json!({"success": true, "data": null}));
    assert_eq!(provider.calls().expect("calls").len(), 1);
}

#[tokio::test]
async fn redemption_delete_preserves_go_envelope_without_data_key() {
    let provider = Arc::new(MemoryCatalogProvider::default());
    let (status, body) = call(
        &app(Arc::clone(&provider), Ok(administrator())),
        Request::builder()
            .method("DELETE")
            .uri("/api/redemption/41")
            .body(Body::empty())
            .expect("request"),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body, json!({"success": true, "message": ""}));
    assert_eq!(provider.calls().expect("calls").len(), 1);
}
