use std::{
    collections::HashSet,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Method, Request, StatusCode, header},
};
use lmm_api_rs::routes::admin_catalog::{
    AdminCatalogActor, AdminCatalogAuthorizer, AdminCatalogState, CatalogError,
    MemoryCatalogProvider, router as admin_catalog_router,
};
use lmm_api_rs::routes::observability::{
    ObservabilityAccess, ObservabilityAuthError, ObservabilityAuthorizer, ObservabilityCall,
    ObservabilityPrincipal, ObservabilityState, ObservabilityStore, ObservabilityStoreError,
    observability_router,
};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone, Copy, Debug, Eq, Hash, PartialEq)]
struct LegacyRoute {
    method: &'static str,
    path: &'static str,
    handler: &'static str,
}

const ADMIN_CATALOG_ROUTES: &[LegacyRoute] = &[
    route("GET", "/api/models/", "GetAllModelsMeta"),
    route("POST", "/api/models/", "CreateModelMeta"),
    route("PUT", "/api/models/", "UpdateModelMeta"),
    route("GET", "/api/models/:id", "GetModelMeta"),
    route("DELETE", "/api/models/:id", "DeleteModelMeta"),
    route("GET", "/api/models/missing", "GetMissingModels"),
    route("GET", "/api/models/search", "SearchModelsMeta"),
    route("POST", "/api/models/sync_upstream", "SyncUpstreamModels"),
    route(
        "GET",
        "/api/models/sync_upstream/preview",
        "SyncUpstreamPreview",
    ),
    route("GET", "/api/vendors/", "GetAllVendors"),
    route("POST", "/api/vendors/", "CreateVendorMeta"),
    route("PUT", "/api/vendors/", "UpdateVendorMeta"),
    route("GET", "/api/vendors/:id", "GetVendorMeta"),
    route("DELETE", "/api/vendors/:id", "DeleteVendorMeta"),
    route("GET", "/api/vendors/search", "SearchVendors"),
    route("GET", "/api/prefill_group/", "GetPrefillGroups"),
    route("POST", "/api/prefill_group/", "CreatePrefillGroup"),
    route("PUT", "/api/prefill_group/", "UpdatePrefillGroup"),
    route("DELETE", "/api/prefill_group/:id", "DeletePrefillGroup"),
    route("GET", "/api/redemption/", "GetAllRedemptions"),
    route("POST", "/api/redemption/", "AddRedemption"),
    route("PUT", "/api/redemption/", "UpdateRedemption"),
    route("GET", "/api/redemption/:id", "GetRedemption"),
    route("DELETE", "/api/redemption/:id", "DeleteRedemption"),
    route(
        "DELETE",
        "/api/redemption/invalid",
        "DeleteInvalidRedemption",
    ),
    route("GET", "/api/redemption/search", "SearchRedemptions"),
];

const OBSERVABILITY_ROUTES: &[LegacyRoute] = &[
    route("GET", "/api/data/", "GetAllQuotaDates"),
    route("GET", "/api/data/flow", "GetAllFlowQuotaDates"),
    route("GET", "/api/data/flow/self", "GetUserFlowQuotaDates"),
    route("GET", "/api/data/self", "GetUserQuotaDates"),
    route("GET", "/api/data/users", "GetQuotaDatesByUser"),
    route("GET", "/api/log/", "GetAllLogs"),
    route(
        "GET",
        "/api/log/channel_affinity_usage_cache",
        "GetChannelAffinityUsageCacheStats",
    ),
    route("GET", "/api/log/search", "SearchAllLogs"),
    route("GET", "/api/log/self", "GetUserLogs"),
    route("GET", "/api/log/self/search", "SearchUserLogs"),
    route("GET", "/api/log/self/stat", "GetLogsSelfStat"),
    route("GET", "/api/log/stat", "GetLogsStat"),
    route("GET", "/api/log/token", "GetLogByKey"),
    route("GET", "/api/perf-metrics", "GetPerfMetrics"),
    route("GET", "/api/perf-metrics/summary", "GetPerfMetricsSummary"),
    route("DELETE", "/api/performance/disk_cache", "ClearDiskCache"),
    route("POST", "/api/performance/gc", "ForceGC"),
    route("GET", "/api/performance/logs", "GetLogFiles"),
    route("DELETE", "/api/performance/logs", "CleanupLogFiles"),
    route(
        "POST",
        "/api/performance/reset_stats",
        "ResetPerformanceStats",
    ),
    route("GET", "/api/performance/stats", "GetPerformanceStats"),
];

const fn route(method: &'static str, path: &'static str, handler: &'static str) -> LegacyRoute {
    LegacyRoute {
        method,
        path,
        handler,
    }
}

fn frozen_routes() -> HashSet<LegacyRoute> {
    include_str!("fixtures/routes/legacy-go-routes.tsv")
        .lines()
        .map(|line| {
            let mut columns = line.split('\t');
            let method = columns.next().expect("legacy route method");
            let path = columns.next().expect("legacy route path");
            let handler = columns
                .next()
                .expect("legacy route handler")
                .rsplit('.')
                .next()
                .expect("legacy handler symbol");
            assert!(columns.next().is_none(), "unexpected legacy route column");
            LegacyRoute {
                method,
                path,
                handler,
            }
        })
        .collect()
}

struct DenyAdminCatalog;

#[async_trait]
impl AdminCatalogAuthorizer for DenyAdminCatalog {
    async fn authorize(&self, _: &HeaderMap) -> Result<AdminCatalogActor, CatalogError> {
        Err(CatalogError::Unauthorized)
    }
}

struct NonAdminCatalog;

#[async_trait]
impl AdminCatalogAuthorizer for NonAdminCatalog {
    async fn authorize(&self, _: &HeaderMap) -> Result<AdminCatalogActor, CatalogError> {
        Ok(AdminCatalogActor {
            user_id: 7,
            role: 1,
        })
    }
}

struct DenyObservability;

#[async_trait]
impl ObservabilityAuthorizer for DenyObservability {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        Err(ObservabilityAuthError::Unauthorized)
    }
}

struct AdminObservability;

#[async_trait]
impl ObservabilityAuthorizer for AdminObservability {
    async fn authorize(
        &self,
        _: &HeaderMap,
        _: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        Ok(ObservabilityPrincipal::User {
            user_id: 7,
            username: "admin".to_owned(),
            role: 10,
        })
    }
}

#[derive(Default)]
struct CountingObservabilityStore {
    calls: AtomicUsize,
}

impl CountingObservabilityStore {
    fn calls(&self) -> usize {
        self.calls.load(Ordering::SeqCst)
    }
}

#[async_trait]
impl ObservabilityStore for CountingObservabilityStore {
    async fn execute(&self, _: ObservabilityCall) -> Result<Value, ObservabilityStoreError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(json!({}))
    }
}

fn concrete_path(path: &str) -> String {
    path.replace(":id", "42")
}

fn request_for(route: LegacyRoute) -> Request<Body> {
    let mut builder = Request::builder()
        .method(route.method)
        .uri(concrete_path(route.path))
        .header("x-user-id", "7")
        .header("x-user-role", "100")
        .header(header::AUTHORIZATION, "Bearer client-forged-token");
    let body = if matches!(route.method, "POST" | "PUT") {
        builder = builder.header(header::CONTENT_TYPE, "application/json");
        Body::from("{")
    } else {
        Body::empty()
    };
    builder.body(body).expect("Wave 2 request")
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(
        &to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body"),
    )
    .expect("JSON response")
}

#[test]
fn wave2_route_fixture_should_match_the_frozen_go_manifest() {
    let expected = ADMIN_CATALOG_ROUTES
        .iter()
        .chain(OBSERVABILITY_ROUTES)
        .copied()
        .collect::<HashSet<_>>();
    assert_eq!(expected.len(), 47, "Wave 2 contains a duplicate route");

    let frozen = frozen_routes();
    let missing = expected.difference(&frozen).copied().collect::<Vec<_>>();
    assert!(
        missing.is_empty(),
        "Wave 2 fixture drifted from legacy-go-routes.tsv: {missing:?}"
    );
}

#[tokio::test]
async fn admin_catalog_should_authorize_all_routes_before_parsing_or_provider_access() {
    let provider = MemoryCatalogProvider::default();
    let app = admin_catalog_router(AdminCatalogState::new(
        Arc::new(provider.clone()),
        Arc::new(DenyAdminCatalog),
    ));

    for route in ADMIN_CATALOG_ROUTES {
        let response = app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("catalog response");
        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{} {} did not authorize first",
            route.method,
            route.path
        );
        assert_eq!(
            json_body(response).await,
            json!({
                "success": false,
                "message": "Unauthorized, invalid access token",
                "code": "AUTH_UNAUTHORIZED"
            }),
            "{} {} changed the legacy auth envelope",
            route.method,
            route.path
        );
    }

    assert!(
        provider.calls().expect("provider calls").is_empty(),
        "an unauthorized catalog request reached the provider"
    );
}

#[tokio::test]
async fn admin_catalog_should_expose_only_the_frozen_methods_for_each_path() {
    let app = admin_catalog_router(AdminCatalogState::new(
        Arc::new(MemoryCatalogProvider::default()),
        Arc::new(DenyAdminCatalog),
    ));
    let paths = ADMIN_CATALOG_ROUTES
        .iter()
        .map(|route| route.path)
        .collect::<HashSet<_>>();

    for path in paths {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::PATCH)
                    .uri(concrete_path(path))
                    .body(Body::empty())
                    .expect("method probe"),
            )
            .await
            .expect("method response");
        assert_eq!(
            response.status(),
            StatusCode::METHOD_NOT_ALLOWED,
            "PATCH unexpectedly matched {path}"
        );

        let actual = response
            .headers()
            .get(header::ALLOW)
            .expect("405 Allow header")
            .to_str()
            .expect("ASCII Allow header")
            .split(',')
            .map(str::trim)
            .filter(|method| *method != "HEAD")
            .collect::<HashSet<_>>();
        let expected = ADMIN_CATALOG_ROUTES
            .iter()
            .filter(|route| route.path == path)
            .map(|route| route.method)
            .collect::<HashSet<_>>();
        assert_eq!(actual, expected, "wrong method set for {path}");
    }
}

#[tokio::test]
async fn admin_catalog_should_keep_the_frozen_insufficient_privilege_code() {
    let provider = MemoryCatalogProvider::default();
    let app = admin_catalog_router(AdminCatalogState::new(
        Arc::new(provider.clone()),
        Arc::new(NonAdminCatalog),
    ));
    let response = app
        .oneshot(
            Request::builder()
                .uri("/api/models/")
                .header(header::AUTHORIZATION, "Bearer valid-non-admin")
                .body(Body::empty())
                .expect("non-admin request"),
        )
        .await
        .expect("non-admin response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        json_body(response).await["code"],
        "AUTH_INSUFFICIENT_PRIVILEGE"
    );
    assert!(provider.calls().expect("provider calls").is_empty());
}

#[tokio::test]
async fn observability_should_authorize_protected_routes_before_validation_or_store_access() {
    let store = Arc::new(CountingObservabilityStore::default());
    let app = observability_router(ObservabilityState::new(
        store.clone(),
        Arc::new(DenyObservability),
    ));

    for route in OBSERVABILITY_ROUTES
        .iter()
        .filter(|route| !route.path.starts_with("/api/perf-metrics"))
    {
        let response = app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("observability response");
        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{} {} did not authorize first",
            route.method,
            route.path
        );
        let body = json_body(response).await;
        assert_eq!(
            body["success"], false,
            "{} {} changed the legacy failure envelope",
            route.method, route.path
        );
        if route.path == "/api/log/token" {
            assert!(
                body.get("code").is_none(),
                "token auth must keep the frozen no-code failure envelope"
            );
        } else {
            assert_eq!(
                body["code"], "AUTH_UNAUTHORIZED",
                "{} {} dropped the dashboard auth error code",
                route.method, route.path
            );
        }
    }

    assert_eq!(
        store.calls(),
        0,
        "an unauthorized observability request reached the store"
    );
}

#[tokio::test]
async fn observability_performance_routes_should_require_root_not_admin() {
    let store = Arc::new(CountingObservabilityStore::default());
    let app = observability_router(ObservabilityState::new(
        store.clone(),
        Arc::new(AdminObservability),
    ));

    for route in OBSERVABILITY_ROUTES
        .iter()
        .filter(|route| route.path.starts_with("/api/performance/"))
    {
        let response = app
            .clone()
            .oneshot(request_for(*route))
            .await
            .expect("performance response");
        assert_eq!(
            response.status(),
            StatusCode::FORBIDDEN,
            "{} {} accepted an ordinary administrator",
            route.method,
            route.path
        );
    }

    assert_eq!(
        store.calls(),
        0,
        "an administrator reached a root-only performance operation"
    );
}

#[tokio::test]
async fn observability_should_expose_only_the_frozen_methods_for_each_path() {
    let app = observability_router(ObservabilityState::new(
        Arc::new(CountingObservabilityStore::default()),
        Arc::new(DenyObservability),
    ));
    let paths = OBSERVABILITY_ROUTES
        .iter()
        .map(|route| route.path)
        .collect::<HashSet<_>>();

    for path in paths {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(Method::PATCH)
                    .uri(concrete_path(path))
                    .body(Body::empty())
                    .expect("method probe"),
            )
            .await
            .expect("method response");
        assert_eq!(
            response.status(),
            StatusCode::METHOD_NOT_ALLOWED,
            "PATCH unexpectedly matched {path}"
        );

        let actual = response
            .headers()
            .get(header::ALLOW)
            .expect("405 Allow header")
            .to_str()
            .expect("ASCII Allow header")
            .split(',')
            .map(str::trim)
            .filter(|method| *method != "HEAD")
            .collect::<HashSet<_>>();
        let expected = OBSERVABILITY_ROUTES
            .iter()
            .filter(|route| route.path == path)
            .map(|route| route.method)
            .collect::<HashSet<_>>();
        assert_eq!(actual, expected, "wrong method set for {path}");
    }
}
