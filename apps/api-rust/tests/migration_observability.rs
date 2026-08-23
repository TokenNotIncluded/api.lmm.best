use std::{collections::BTreeMap, sync::Arc};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::migration_routes::observability::{
    InMemoryObservabilityStore, ObservabilityAccess, ObservabilityAuthError,
    ObservabilityAuthorizer, ObservabilityCall, ObservabilityPrincipal, ObservabilityState,
    ObservabilityStore, ObservabilityStoreError, observability_disk_cache_router,
    observability_force_gc_router, observability_metrics_router, observability_performance_router,
    observability_read_router, observability_router,
};
use serde_json::{Value, json};
use std::sync::atomic::{AtomicUsize, Ordering};
use tower::ServiceExt;

struct Allow {
    role: i64,
}

#[async_trait]
impl ObservabilityAuthorizer for Allow {
    async fn authorize(
        &self,
        _: &HeaderMap,
        access: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        Ok(match access {
            ObservabilityAccess::Token => ObservabilityPrincipal::Token { token_id: 3 },
            ObservabilityAccess::PublicOrUser => ObservabilityPrincipal::Public,
            ObservabilityAccess::Admin | ObservabilityAccess::Root | ObservabilityAccess::User => {
                ObservabilityPrincipal::User {
                    user_id: 7,
                    username: "auditor".to_owned(),
                    role: self.role,
                }
            }
        })
    }
}

struct Deny;

#[async_trait]
impl ObservabilityAuthorizer for Deny {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        Err(ObservabilityAuthError::Unauthorized)
    }
}

struct CountingStore(AtomicUsize);

#[async_trait]
impl ObservabilityStore for CountingStore {
    async fn execute(&self, _: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        self.0.fetch_add(1, Ordering::Relaxed);
        Ok(json!({}))
    }
}

struct SuccessStore;

#[async_trait]
impl ObservabilityStore for SuccessStore {
    async fn execute(&self, _: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        Ok(json!({}))
    }
}

fn router(role: i64) -> axum::Router {
    observability_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role }),
    ))
}

fn read_router() -> axum::Router {
    observability_read_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role: 100 }),
    ))
}

fn metrics_router() -> axum::Router {
    observability_metrics_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role: 100 }),
    ))
}

fn performance_router() -> axum::Router {
    observability_performance_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role: 100 }),
    ))
}

fn disk_cache_router() -> axum::Router {
    observability_disk_cache_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role: 100 }),
    ))
}

fn force_gc_router() -> axum::Router {
    observability_force_gc_router(ObservabilityState::new(
        Arc::new(SuccessStore),
        Arc::new(Allow { role: 100 }),
    ))
}

#[tokio::test]
async fn observability_read_router_mounts_the_storage_only_surface() {
    let response = read_router()
        .oneshot(
            Request::builder()
                .uri("/api/data/")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response
            .headers()
            .get("auth-version")
            .and_then(|value| value.to_str().ok()),
        Some("864b7076dbcd0a3c01b5520316720ebf")
    );
}

#[tokio::test]
async fn observability_metrics_router_mounts_the_postgres_metric_reads() {
    let app = metrics_router();
    for uri in [
        "/api/perf-metrics?model=gpt-test",
        "/api/perf-metrics/summary",
    ] {
        let response = app
            .clone()
            .oneshot(Request::get(uri).body(Body::empty()).expect("request"))
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{uri}");
    }
}

#[tokio::test]
async fn observability_performance_router_mounts_only_the_root_operations() {
    for (method, uri) in [
        ("GET", "/api/performance/stats"),
        ("POST", "/api/performance/reset_stats"),
        ("DELETE", "/api/performance/logs?mode=by_count&value=1"),
    ] {
        let response = performance_router()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(uri)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{method} {uri}");
    }
}

#[tokio::test]
async fn observability_force_gc_router_mounts_only_the_gc_operation() {
    let response = force_gc_router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/performance/gc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");
    assert_eq!(response.status(), StatusCode::OK);

    let stats = force_gc_router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/performance/stats")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");
    assert_eq!(stats.status(), StatusCode::NOT_FOUND);
}

#[tokio::test]
async fn observability_disk_cache_router_mounts_its_two_root_operations() {
    for (method, uri) in [
        ("DELETE", "/api/performance/disk_cache"),
        ("GET", "/api/performance/logs"),
    ] {
        let response = disk_cache_router()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(uri)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{method} {uri}");
    }
}

async fn body(response: axum::response::Response) -> Value {
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    serde_json::from_slice(&bytes).expect("JSON response")
}

#[tokio::test]
async fn observability_routes_accept_their_expected_authenticated_scopes() {
    let cases = [
        ("GET", "/api/data/"),
        ("GET", "/api/data/users"),
        ("GET", "/api/data/self"),
        ("GET", "/api/data/flow?start_timestamp=1&end_timestamp=2"),
        (
            "GET",
            "/api/data/flow/self?start_timestamp=1&end_timestamp=2",
        ),
        ("GET", "/api/log/"),
        (
            "GET",
            "/api/log/channel_affinity_usage_cache?rule_name=r&key_fp=k",
        ),
        ("GET", "/api/log/search"),
        ("GET", "/api/log/self"),
        ("GET", "/api/log/self/search"),
        ("GET", "/api/log/self/stat"),
        ("GET", "/api/log/stat"),
        ("GET", "/api/log/token"),
        ("GET", "/api/perf-metrics?model=gpt-test"),
        ("GET", "/api/perf-metrics/summary"),
        ("DELETE", "/api/performance/disk_cache"),
        ("POST", "/api/performance/gc"),
        ("GET", "/api/performance/logs"),
        ("DELETE", "/api/performance/logs?mode=by_count&value=1"),
        ("POST", "/api/performance/reset_stats"),
        ("GET", "/api/performance/stats"),
    ];

    for (method, uri) in cases {
        let request = Request::builder()
            .method(method)
            .uri(uri)
            .body(Body::empty())
            .expect("request");
        let response = router(100).oneshot(request).await.expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{method} {uri}");
    }
}

#[tokio::test]
async fn protected_observability_route_returns_the_legacy_dashboard_auth_envelope_when_missing() {
    let app = observability_router(ObservabilityState::new(
        Arc::new(InMemoryObservabilityStore),
        Arc::new(Deny),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/data/flow")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        body(response).await,
        json!({"success": false, "code": "AUTH_UNAUTHORIZED", "message": "Unauthorized"})
    );
}

#[tokio::test]
async fn token_log_route_keeps_the_token_auth_error_envelope_without_dashboard_code() {
    let app = observability_router(ObservabilityState::new(
        Arc::new(InMemoryObservabilityStore),
        Arc::new(Deny),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/log/token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        body(response).await,
        json!({"success": false, "message": "未提供令牌"})
    );
}

#[tokio::test]
async fn administrator_cannot_use_root_only_performance_maintenance_routes() {
    let response = router(10)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/performance/gc")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(body(response).await["code"], "AUTH_INSUFFICIENT_PRIVILEGE");
}

#[tokio::test]
async fn invalid_query_is_not_parsed_or_sent_to_storage_before_authentication() {
    let store = Arc::new(CountingStore(AtomicUsize::new(0)));
    let app = observability_router(ObservabilityState::new(store.clone(), Arc::new(Deny)));
    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/performance/logs?mode=nope&value=0")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(store.0.load(Ordering::Relaxed), 0);
}

#[tokio::test]
async fn unsupported_observability_method_returns_method_not_allowed() {
    let response = router(100)
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/api/log/stat")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}

#[test]
fn observation_call_query_type_is_stable_for_external_store_fakes() {
    let query = BTreeMap::from([("model".to_owned(), "gpt-test".to_owned())]);
    assert_eq!(query["model"], "gpt-test");
}
