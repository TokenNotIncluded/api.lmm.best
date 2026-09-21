//! Frozen listener contracts for the unmounted relay-misc slice.
//!
//! These are intentionally mock differentials, rather than optimistic unit
//! tests: the cases come from the checked-in legacy Go route ledger and prove
//! both observable outcomes and the service-boundary side effects.  A real
//! listener differential still needs the shared auth/distribution executor.

use std::{
    collections::HashSet,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    extract::Request,
    http::{HeaderMap, Request as HttpRequest, StatusCode, header},
    response::Response,
};
use lmm_api_rs::routes::relay_misc::{
    RelayAccounting, RelayAuth, RelayMiscHttpState, RelayMiscService, RelayProtocol,
    RelayRequestContext, routes,
};
use tower::ServiceExt;

const LEGACY_NOT_IMPLEMENTED_BODY: &str = r#"{"error":{"message":"API not implemented","type":"new_api_error","param":"","code":"api_not_implemented"}}"#;

const LEGACY_STUBS: &[(&str, &str)] = &[
    ("POST", "/v1/images/variations"),
    ("GET", "/v1/files"),
    ("POST", "/v1/files"),
    ("GET", "/v1/files/file-1"),
    ("DELETE", "/v1/files/file-1"),
    ("GET", "/v1/files/file-1/content"),
    ("GET", "/v1/fine-tunes"),
    ("POST", "/v1/fine-tunes"),
    ("GET", "/v1/fine-tunes/ft-1"),
    ("POST", "/v1/fine-tunes/ft-1/cancel"),
    ("GET", "/v1/fine-tunes/ft-1/events"),
];

struct AllowAllFixtureService {
    authorization_calls: AtomicUsize,
    relayed: Mutex<Vec<RelayProtocol>>,
    authorization: RelayAuth,
}

impl AllowAllFixtureService {
    fn allow() -> Self {
        Self {
            authorization_calls: AtomicUsize::new(0),
            relayed: Mutex::new(Vec::new()),
            authorization: RelayAuth::Authorized,
        }
    }

    fn reject() -> Self {
        Self {
            authorization_calls: AtomicUsize::new(0),
            relayed: Mutex::new(Vec::new()),
            authorization: RelayAuth::Rejected {
                status: StatusCode::UNAUTHORIZED,
                message: "Invalid token".to_owned(),
            },
        }
    }
}

#[async_trait]
impl RelayMiscService for AllowAllFixtureService {
    async fn system_performance(&self, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn authorize(&self, _: &Request) -> RelayAuth {
        self.authorization_calls.fetch_add(1, Ordering::Relaxed);
        self.authorization.clone()
    }

    async fn model_rate_limit(&self, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn distribute(&self, _: &RelayRequestContext, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn provider_headers(
        &self,
        _: &RelayRequestContext,
        headers: &HeaderMap,
    ) -> Result<HeaderMap, RelayAuth> {
        Ok(headers.clone())
    }

    async fn relay(&self, protocol: RelayProtocol, _: Request) -> Response {
        self.relayed
            .lock()
            .expect("relay calls lock")
            .push(protocol);
        Response::new(Body::from("must not relay a legacy 501 route"))
    }

    async fn account(&self, _: &RelayRequestContext, _: RelayAccounting) -> RelayAuth {
        RelayAuth::Authorized
    }
}

fn router(executor: Arc<AllowAllFixtureService>) -> Router {
    routes(RelayMiscHttpState::new(executor))
}

async fn call(router: &Router, method: &str, path: &str, authenticated: bool) -> Response {
    let mut request = HttpRequest::builder().method(method).uri(path);
    if authenticated {
        request = request.header(header::AUTHORIZATION, "Bearer relay-misc-fixture");
    }
    router
        .clone()
        .oneshot(request.body(Body::empty()).expect("frozen legacy request"))
        .await
        .expect("router response")
}

fn frozen_legacy_not_implemented_routes() -> HashSet<(&'static str, &'static str)> {
    include_str!("fixtures/routes/legacy-go-routes.tsv")
        .lines()
        .filter_map(|line| {
            let mut fields = line.split('\t');
            let method = fields.next()?;
            let path = fields.next()?;
            let handler = fields.next()?;
            (handler.ends_with("controller.RelayNotImplemented")
                && matches!(
                    path,
                    "/v1/images/variations"
                        | "/v1/files"
                        | "/v1/files/:id"
                        | "/v1/files/:id/content"
                        | "/v1/fine-tunes"
                        | "/v1/fine-tunes/:id"
                        | "/v1/fine-tunes/:id/cancel"
                        | "/v1/fine-tunes/:id/events"
                ))
            .then_some((method, path))
        })
        .collect()
}

#[test]
fn all_eleven_stubs_are_frozen_to_the_legacy_go_ledger() {
    let expected: HashSet<_> = [
        ("POST", "/v1/images/variations"),
        ("GET", "/v1/files"),
        ("POST", "/v1/files"),
        ("GET", "/v1/files/:id"),
        ("DELETE", "/v1/files/:id"),
        ("GET", "/v1/files/:id/content"),
        ("GET", "/v1/fine-tunes"),
        ("POST", "/v1/fine-tunes"),
        ("GET", "/v1/fine-tunes/:id"),
        ("POST", "/v1/fine-tunes/:id/cancel"),
        ("GET", "/v1/fine-tunes/:id/events"),
    ]
    .into_iter()
    .collect();
    assert_eq!(frozen_legacy_not_implemented_routes(), expected);
    assert_eq!(LEGACY_STUBS.len(), expected.len());
}

#[tokio::test]
async fn every_legacy_stub_authenticates_then_returns_the_exact_frozen_501_without_relaying() {
    let executor = Arc::new(AllowAllFixtureService::allow());
    let app = router(Arc::clone(&executor));

    for (method, path) in LEGACY_STUBS {
        let response = call(&app, method, path, true).await;
        assert_eq!(
            response.status(),
            StatusCode::NOT_IMPLEMENTED,
            "{method} {path}"
        );
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8",
            "{method} {path}"
        );
        assert_eq!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("501 body"),
            LEGACY_NOT_IMPLEMENTED_BODY.as_bytes(),
            "{method} {path}"
        );
    }

    assert_eq!(
        executor.authorization_calls.load(Ordering::Relaxed),
        LEGACY_STUBS.len()
    );
    assert!(
        executor
            .relayed
            .lock()
            .expect("relay calls lock")
            .is_empty()
    );
}

#[tokio::test]
async fn rejected_stub_keeps_legacy_auth_error_and_never_relays() {
    let executor = Arc::new(AllowAllFixtureService::reject());
    let response = call(&router(Arc::clone(&executor)), "GET", "/v1/files", false).await;

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(response.headers()[header::CONTENT_TYPE], "application/json");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("auth body"),
        br#"{"error":{"message":"Invalid token","type":"new_api_error","param":"","code":""}}"#
            .as_slice()
    );
    assert!(
        executor
            .relayed
            .lock()
            .expect("relay calls lock")
            .is_empty()
    );
}

#[tokio::test]
async fn unsupported_methods_fail_at_route_matching_before_legacy_stub_authorization() {
    let executor = Arc::new(AllowAllFixtureService::allow());
    let app = router(Arc::clone(&executor));

    for (method, path) in [
        ("GET", "/v1/images/variations"),
        ("PUT", "/v1/files"),
        ("PUT", "/v1/files/file-1"),
        ("POST", "/v1/files/file-1/content"),
        ("PUT", "/v1/fine-tunes"),
        ("POST", "/v1/fine-tunes/ft-1"),
        ("GET", "/v1/fine-tunes/ft-1/cancel"),
        ("POST", "/v1/fine-tunes/ft-1/events"),
    ] {
        let response = call(&app, method, path, true).await;
        assert_eq!(
            response.status(),
            StatusCode::METHOD_NOT_ALLOWED,
            "{method} {path} must never fall through to the explicit-501 handler"
        );
    }

    assert_eq!(executor.authorization_calls.load(Ordering::Relaxed), 0);
    assert!(
        executor
            .relayed
            .lock()
            .expect("relay calls lock")
            .is_empty()
    );
}
