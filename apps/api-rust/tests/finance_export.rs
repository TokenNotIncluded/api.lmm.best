use std::{
    collections::BTreeMap,
    io::{Cursor, Read},
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    routes::finance_export::{
        FinanceExportArtifact, FinanceExportAudit, FinanceExportBackend, FinanceExportError,
        FinanceExportState, router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use tower::ServiceExt;

const FILE_NAMES: [&str; 15] = [
    "manifest.json",
    "financial-options.json",
    "model-prices-and-ratios.json",
    "effective-model-pricing.json",
    "users-balances.json",
    "channels-pricing.json",
    "subscription-plans.json",
    "topups.json",
    "subscription-orders.json",
    "subscription-payment-events.json",
    "usage-billing-records.json",
    "bounty-ledger.json",
    "checkins.json",
    "redemptions.json",
    "user-subscriptions.json",
];

#[derive(Default)]
struct RecordingBackend {
    build_calls: Mutex<Vec<(i64, i64, i64)>>,
    audit_calls: Mutex<Vec<FinanceExportAudit>>,
    failures: AtomicUsize,
}

#[async_trait]
impl FinanceExportBackend for RecordingBackend {
    async fn build(
        &self,
        start_timestamp: i64,
        end_timestamp: i64,
        generated_at: i64,
    ) -> Result<FinanceExportArtifact, FinanceExportError> {
        self.build_calls
            .lock()
            .expect("build recording lock")
            .push((start_timestamp, end_timestamp, generated_at));
        if self.failures.load(Ordering::Relaxed) > 0 {
            return Err(FinanceExportError("database unavailable".to_owned()));
        }
        Ok(artifact())
    }

    async fn record_audit(&self, audit: &FinanceExportAudit) {
        self.audit_calls
            .lock()
            .expect("audit recording lock")
            .push(audit.clone());
    }
}

struct FixtureAuth;

#[async_trait]
impl DashboardAuth for FixtureAuth {
    async fn check_critical_rate_limit(
        &self,
        _: &str,
    ) -> Result<CriticalRateLimitOutcome, AuthError> {
        Ok(CriticalRateLimitOutcome::Allowed)
    }

    async fn login(&self, _: LoginRequest, _: RequestMetadata) -> Result<LoginOutcome, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn login_2fa(
        &self,
        _: TwoFactorLoginRequest,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn refresh(
        &self,
        _: SecretString,
        _: Option<String>,
        _: RequestMetadata,
    ) -> Result<AuthBundle, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let role = match token.expose_secret() {
            "admin" => 10,
            "l0" => 1,
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(DashboardUser {
            id: 17,
            username: "finance-admin".to_owned(),
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        })
    }

    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }

    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
}

fn artifact() -> FinanceExportArtifact {
    let files = FILE_NAMES
        .into_iter()
        .map(|name| {
            (
                name.to_owned(),
                format!("{{\"fixture\":\"{name}\"}}").into_bytes(),
            )
        })
        .collect();
    FinanceExportArtifact {
        files,
        rows: BTreeMap::from([("users".to_owned(), 2), ("usage".to_owned(), 4)]),
    }
}

fn app(backend: Arc<RecordingBackend>) -> axum::Router {
    router(FinanceExportState::with_backend(
        backend,
        Arc::new(FixtureAuth),
    ))
}

async fn body_bytes(response: axum::response::Response) -> Vec<u8> {
    to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body")
        .to_vec()
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&body_bytes(response).await).expect("legacy JSON response")
}

#[tokio::test]
async fn export_should_require_admin_before_parsing_query() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/finance/export?format=invalid")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
    assert_eq!(json_body(response).await["code"], "AUTH_UNAUTHORIZED");
    assert!(backend.build_calls.lock().expect("build lock").is_empty());
}

#[tokio::test]
async fn export_should_hide_route_from_l0_accounts() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/finance/export")
                .header(header::AUTHORIZATION, "Bearer l0")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert!(response.headers().get("auth-version").is_none());
    assert_eq!(json_body(response).await, json!({"message": "Not Found"}));
    assert!(backend.build_calls.lock().expect("build lock").is_empty());
}

#[tokio::test]
async fn invalid_format_should_keep_legacy_error_and_disable_cache() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(backend)
        .oneshot(
            Request::get("/api/finance/export?format=csv")
                .header(header::AUTHORIZATION, "admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert_eq!(
        response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert_eq!(
        json_body(response).await,
        json!({"success": false, "message": "format must be zip or text"})
    );
}

#[tokio::test]
async fn invalid_window_should_follow_go_error_order() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(backend)
        .oneshot(
            Request::get("/api/finance/export?format=text&start_timestamp=nope&end_timestamp=0")
                .header(header::AUTHORIZATION, "Bearer admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(
        json_body(response).await["message"],
        "invalid start_timestamp"
    );
}

#[tokio::test]
async fn text_export_should_have_stable_sections_headers_and_audit() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get(
                "/api/finance/export?format=%20TEXT%20&start_timestamp=10&end_timestamp=20",
            )
            .header(header::AUTHORIZATION, "Bearer admin")
            .body(Body::empty())
            .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "text/plain; charset=utf-8"
    );
    assert_eq!(
        response.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert!(
        response
            .headers()
            .get(header::CONTENT_DISPOSITION)
            .is_none()
    );
    let text = String::from_utf8(body_bytes(response).await).expect("text export UTF-8");
    assert!(text.starts_with("LMM Finance Analysis Export\n========================================\n\n## manifest.json\n"));
    assert!(text.find("manifest.json") < text.find("user-subscriptions.json"));
    let calls = backend.build_calls.lock().expect("build lock");
    assert_eq!((calls[0].0, calls[0].1), (10, 20));
    drop(calls);
    let audits = backend.audit_calls.lock().expect("audit lock");
    assert_eq!(audits[0].actor_id, 17);
    assert_eq!(audits[0].format, "text");
    assert_eq!(audits[0].rows["usage"], 4);
}

#[tokio::test]
async fn default_zip_should_contain_fifteen_stable_entries_and_zip_headers() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/finance/export?start_timestamp=10&end_timestamp=20")
                .header(header::AUTHORIZATION, "admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.headers()[header::CONTENT_TYPE], "application/zip");
    assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
    assert_eq!(response.headers()[header::PRAGMA], "no-cache");
    assert_eq!(response.headers()[header::EXPIRES], "0");
    assert_eq!(response.headers()["x-content-type-options"], "nosniff");
    let disposition = response.headers()[header::CONTENT_DISPOSITION]
        .to_str()
        .expect("ASCII disposition");
    assert!(disposition.starts_with("attachment; filename=\"lmm-finance-export-"));
    assert!(disposition.ends_with(".zip\""));
    let bytes = body_bytes(response).await;
    let mut archive = zip::ZipArchive::new(Cursor::new(bytes)).expect("valid ZIP response");
    assert_eq!(archive.len(), FILE_NAMES.len());
    for (index, expected) in FILE_NAMES.into_iter().enumerate() {
        assert_eq!(archive.by_index(index).expect("ZIP entry").name(), expected);
    }
    let mut users = String::new();
    archive
        .by_name("users-balances.json")
        .expect("users entry")
        .read_to_string(&mut users)
        .expect("users entry bytes");
    assert!(!users.contains("password"));
    assert!(!users.contains("access_token"));
    assert_eq!(backend.audit_calls.lock().expect("audit lock").len(), 1);
}

#[tokio::test]
async fn backend_error_should_not_record_audit_or_return_file_headers() {
    let backend = Arc::new(RecordingBackend::default());
    backend.failures.store(1, Ordering::Relaxed);
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/finance/export?format=text&start_timestamp=10&end_timestamp=20")
                .header(header::AUTHORIZATION, "admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    assert!(
        response
            .headers()
            .get(header::CONTENT_DISPOSITION)
            .is_none()
    );
    assert_eq!(
        json_body(response).await,
        json!({"success": false, "message": "database unavailable"})
    );
    assert!(backend.audit_calls.lock().expect("audit lock").is_empty());
}
