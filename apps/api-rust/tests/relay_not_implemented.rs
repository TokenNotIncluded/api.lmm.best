//! Exact frozen Go `RelayNotImplemented` contracts.
//!
//! The twelve routes in this file intentionally stay explicit `501` seams.
//! They still pass through the same legacy gates as every `/v1` relay request;
//! a successful gate sequence must not select a channel, invoke an upstream,
//! or account usage before returning the frozen response.

use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, Bytes, to_bytes},
    extract::Request as AxumRequest,
    http::{HeaderMap, Request, StatusCode, header},
    response::Response,
};
use lmm_api_rs::RequestContext;
use lmm_api_rs::routes::{
    relay_anthropic_gemini::{
        RelayBackend, RelayChannel, RelayFailure, RelayHttpState, RelayIdentity, RelayOutcome,
        RelayProtocol as ModelRelayProtocol, UpstreamReply, UpstreamRequest,
        router as model_router,
    },
    relay_misc::{
        RelayAccounting, RelayAuth, RelayBodyEncoding, RelayMiscHttpState, RelayMiscService,
        RelayProtocol, RelayRequestContext, routes as misc_routes,
    },
};
use serde_json::json;
use tower::ServiceExt;

const FROZEN_BODY: &[u8] = br#"{"error":{"message":"API not implemented","type":"new_api_error","param":"","code":"api_not_implemented"}}"#;
const AUTH_FAILURE_BODY: &[u8] =
    br#"{"error":{"message":"Invalid token","type":"new_api_error","param":"","code":""}}"#;

const MISC_FROZEN_ROUTES: &[(&str, &str, &str)] = &[
    ("POST", "/v1/images/variations", "{}"),
    ("GET", "/v1/files", ""),
    ("POST", "/v1/files", "{}"),
    ("DELETE", "/v1/files/file-1", ""),
    ("GET", "/v1/files/file-1", ""),
    ("GET", "/v1/files/file-1/content", ""),
    ("POST", "/v1/fine-tunes", "{}"),
    ("GET", "/v1/fine-tunes", ""),
    ("GET", "/v1/fine-tunes/ft-1", ""),
    ("POST", "/v1/fine-tunes/ft-1/cancel", "{}"),
    ("GET", "/v1/fine-tunes/ft-1/events", ""),
];

#[derive(Default)]
struct FrozenMiscService {
    stages: Mutex<Vec<&'static str>>,
    relay_calls: AtomicUsize,
    account_calls: AtomicUsize,
    reject_at: Option<&'static str>,
}

impl FrozenMiscService {
    fn accepted() -> Self {
        Self::default()
    }

    fn reject_at(stage: &'static str) -> Self {
        Self {
            reject_at: Some(stage),
            ..Self::default()
        }
    }

    fn gate(&self, stage: &'static str) -> RelayAuth {
        self.stages.lock().expect("stage lock").push(stage);
        if self.reject_at == Some(stage) {
            RelayAuth::Rejected {
                status: StatusCode::UNAUTHORIZED,
                message: "Invalid token".to_owned(),
            }
        } else {
            RelayAuth::Authorized
        }
    }
}

#[async_trait]
impl RelayMiscService for FrozenMiscService {
    async fn system_performance(&self, _: &AxumRequest) -> RelayAuth {
        self.gate("system_performance")
    }

    async fn authorize(&self, _: &AxumRequest) -> RelayAuth {
        self.gate("authorize")
    }

    async fn model_rate_limit(&self, _: &AxumRequest) -> RelayAuth {
        self.gate("model_rate_limit")
    }

    async fn distribute(&self, _: &RelayRequestContext, _: &AxumRequest) -> RelayAuth {
        self.gate("distribute")
    }

    async fn decode_body(
        &self,
        encoding: RelayBodyEncoding,
        body: Bytes,
    ) -> Result<Bytes, RelayAuth> {
        assert_eq!(encoding, RelayBodyEncoding::Identity);
        Ok(body)
    }

    async fn provider_headers(
        &self,
        _: &RelayRequestContext,
        _: &HeaderMap,
    ) -> Result<HeaderMap, RelayAuth> {
        panic!("frozen 501 must not prepare upstream headers")
    }

    async fn relay(&self, _: RelayProtocol, _: AxumRequest) -> Response {
        self.relay_calls.fetch_add(1, Ordering::Relaxed);
        panic!("frozen 501 must not invoke an upstream")
    }

    async fn account(&self, _: &RelayRequestContext, _: RelayAccounting) -> RelayAuth {
        self.account_calls.fetch_add(1, Ordering::Relaxed);
        panic!("frozen 501 must not account usage")
    }
}

fn misc_app(service: Arc<FrozenMiscService>) -> Router {
    misc_routes(RelayMiscHttpState::new(service))
}

async fn request(router: &Router, method: &str, path: &str, body: &str) -> Response {
    router
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header(header::AUTHORIZATION, "Bearer frozen-fixture")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(body.to_owned()))
                .expect("valid request"),
        )
        .await
        .expect("router response")
}

#[tokio::test]
async fn eleven_files_fine_tunes_and_variations_routes_run_every_gate_then_return_exact_501() {
    for (method, path, body) in MISC_FROZEN_ROUTES {
        let service = Arc::new(FrozenMiscService::accepted());
        let response = request(&misc_app(Arc::clone(&service)), method, path, body).await;

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
            FROZEN_BODY,
            "{method} {path}"
        );
        assert_eq!(
            *service.stages.lock().expect("stage lock"),
            [
                "system_performance",
                "authorize",
                "model_rate_limit",
                "distribute"
            ],
            "{method} {path}"
        );
        assert_eq!(
            service.relay_calls.load(Ordering::Relaxed),
            0,
            "{method} {path}"
        );
        assert_eq!(
            service.account_calls.load(Ordering::Relaxed),
            0,
            "{method} {path}"
        );
    }
}

#[tokio::test]
async fn frozen_misc_routes_preserve_each_pre_501_rejection_and_stop_the_later_gates() {
    let cases = [
        ("system_performance", vec!["system_performance"]),
        ("authorize", vec!["system_performance", "authorize"]),
        (
            "model_rate_limit",
            vec!["system_performance", "authorize", "model_rate_limit"],
        ),
        (
            "distribute",
            vec![
                "system_performance",
                "authorize",
                "model_rate_limit",
                "distribute",
            ],
        ),
    ];

    for (rejected_stage, expected_stages) in cases {
        let service = Arc::new(FrozenMiscService::reject_at(rejected_stage));
        let response = request(&misc_app(Arc::clone(&service)), "GET", "/v1/files", "").await;
        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{rejected_stage}"
        );
        assert_eq!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("auth error body"),
            AUTH_FAILURE_BODY,
            "{rejected_stage}"
        );
        assert_eq!(
            *service.stages.lock().expect("stage lock"),
            expected_stages,
            "{rejected_stage}"
        );
        assert_eq!(
            service.relay_calls.load(Ordering::Relaxed),
            0,
            "{rejected_stage}"
        );
        assert_eq!(
            service.account_calls.load(Ordering::Relaxed),
            0,
            "{rejected_stage}"
        );
    }
}

#[derive(Default)]
struct FrozenModelDeleteBackend {
    authenticated: AtomicUsize,
    selected: AtomicUsize,
    invoked: AtomicUsize,
    outcomes: Mutex<Vec<RelayOutcome>>,
}

#[async_trait]
impl RelayBackend for FrozenModelDeleteBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        self.authenticated.fetch_add(1, Ordering::Relaxed);
        (token == "frozen-fixture")
            .then(|| RelayIdentity {
                token_id: "frozen-token-id".to_owned(),
            })
            .ok_or(RelayFailure::Unauthorized)
    }

    async fn select_channel(
        &self,
        _: &RelayIdentity,
        _: ModelRelayProtocol,
        _: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        self.selected.fetch_add(1, Ordering::Relaxed);
        Err(RelayFailure::NoChannel)
    }

    async fn invoke(
        &self,
        _: &RelayChannel,
        _: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        self.invoked.fetch_add(1, Ordering::Relaxed);
        Ok(UpstreamReply::Json(json!({"must_not":"reach_upstream"})))
    }

    async fn record_outcome(
        &self,
        _: Option<&RelayIdentity>,
        _: Option<&RelayChannel>,
        outcome: RelayOutcome,
    ) {
        self.outcomes.lock().expect("outcome lock").push(outcome);
    }
}

#[tokio::test]
async fn model_delete_authenticates_then_returns_exact_501_without_channel_selection_or_upstream() {
    let backend = Arc::new(FrozenModelDeleteBackend::default());
    let relay_backend: Arc<dyn RelayBackend> = backend.clone();
    let mut request = Request::delete("/v1/models/frozen-model")
        .header(header::AUTHORIZATION, "Bearer frozen-fixture")
        .body(Body::empty())
        .expect("valid delete request");
    request.extensions_mut().insert(RequestContext {
        request_id: "frozen-model-delete-request".to_owned(),
        client_ip: None,
    });
    let response = model_router(RelayHttpState::new(relay_backend))
        .oneshot(request)
        .await
        .expect("router response");

    assert_eq!(response.status(), StatusCode::NOT_IMPLEMENTED);
    assert_eq!(response.headers()[header::CONTENT_TYPE], "application/json");
    assert_eq!(
        response.headers()["x-request-id"],
        "frozen-model-delete-request"
    );
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("501 body"),
        FROZEN_BODY
    );
    assert_eq!(backend.authenticated.load(Ordering::Relaxed), 1);
    assert_eq!(backend.selected.load(Ordering::Relaxed), 0);
    assert_eq!(backend.invoked.load(Ordering::Relaxed), 0);
    assert!(backend.outcomes.lock().expect("outcome lock").is_empty());

    let rejected_backend = Arc::new(FrozenModelDeleteBackend::default());
    let rejected_relay_backend: Arc<dyn RelayBackend> = rejected_backend.clone();
    let rejected = model_router(RelayHttpState::new(rejected_relay_backend))
        .oneshot(
            Request::delete("/v1/models/frozen-model")
                .body(Body::empty())
                .expect("valid unauthenticated delete request"),
        )
        .await
        .expect("router response");
    assert_eq!(rejected.status(), StatusCode::NOT_FOUND);
    assert_eq!(rejected_backend.authenticated.load(Ordering::Relaxed), 0);
    assert_eq!(rejected_backend.selected.load(Ordering::Relaxed), 0);
    assert_eq!(rejected_backend.invoked.load(Ordering::Relaxed), 0);
    assert_eq!(
        *rejected_backend.outcomes.lock().expect("outcome lock"),
        [RelayOutcome::Unauthorized]
    );
}
