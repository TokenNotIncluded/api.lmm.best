use std::sync::{
    Arc, Mutex,
    atomic::{AtomicUsize, Ordering},
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
    routes::security_overview::{
        SecurityEvent, SecurityEventFilter, SecurityOverviewBackend, SecurityOverviewError,
        SecurityOverviewState, SecurityPolicySnapshot, SecurityRule, SecurityStatBucket,
        SecurityStats, router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Default)]
struct RecordingBackend {
    policy: SecurityPolicySnapshot,
    stats: SecurityStats,
    events: Vec<SecurityEvent>,
    total: i64,
    policy_calls: AtomicUsize,
    stats_filters: Mutex<Vec<SecurityEventFilter>>,
    event_calls: Mutex<Vec<(SecurityEventFilter, i64, i64)>>,
}

#[async_trait]
impl SecurityOverviewBackend for RecordingBackend {
    async fn policy_snapshot(&self) -> Result<SecurityPolicySnapshot, SecurityOverviewError> {
        self.policy_calls.fetch_add(1, Ordering::Relaxed);
        Ok(self.policy.clone())
    }

    async fn stats(
        &self,
        filter: &SecurityEventFilter,
    ) -> Result<SecurityStats, SecurityOverviewError> {
        self.stats_filters
            .lock()
            .expect("stats recording lock")
            .push(filter.clone());
        Ok(self.stats.clone())
    }

    async fn events(
        &self,
        filter: &SecurityEventFilter,
        limit: i64,
        offset: i64,
    ) -> Result<(Vec<SecurityEvent>, i64), SecurityOverviewError> {
        self.event_calls
            .lock()
            .expect("event recording lock")
            .push((filter.clone(), limit, offset));
        Ok((self.events.clone(), self.total))
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
            id: 7,
            username: "security-admin".to_owned(),
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

fn app(backend: Arc<RecordingBackend>) -> axum::Router {
    router(SecurityOverviewState::with_backend(
        backend,
        Arc::new(FixtureAuth),
    ))
}

async fn json_body(response: axum::response::Response) -> Value {
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    serde_json::from_slice(&body).expect("legacy JSON response")
}

fn policy_fixture() -> SecurityPolicySnapshot {
    SecurityPolicySnapshot {
        enabled: true,
        on_prompt: false,
        action: "audit".to_owned(),
        rules: vec![
            SecurityRule {
                id: "enabled-child-rule".to_owned(),
                name: "Child safety".to_owned(),
                category: "child_safety".to_owned(),
                layer: "universal_standard".to_owned(),
                severity: "critical".to_owned(),
                source: "anthropic_usage_policy".to_owned(),
                version: "v2".to_owned(),
                description: "Fixture".to_owned(),
                enabled: true,
                patterns: vec!["private matcher".to_owned()],
            },
            SecurityRule {
                id: "disabled-rule".to_owned(),
                name: "Disabled".to_owned(),
                category: "custom".to_owned(),
                layer: "custom".to_owned(),
                severity: "medium".to_owned(),
                source: "local_custom".to_owned(),
                version: "v1".to_owned(),
                description: "Disabled fixture".to_owned(),
                enabled: false,
                patterns: vec!["admin only".to_owned()],
            },
        ],
        grok_violation_fee_enabled: false,
        grok_violation_fee_amount_usd: 0.125,
    }
}

#[tokio::test]
async fn public_policy_is_preactivation_safe_and_never_exposes_matchers() {
    let backend = Arc::new(RecordingBackend {
        policy: policy_fixture(),
        ..RecordingBackend::default()
    });
    let response = app(backend)
        .oneshot(
            Request::get("/api/security/policy")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
    assert!(response.headers().get("auth-version").is_none());
    let body = json_body(response).await;
    assert_eq!(body["success"], true);
    assert_eq!(
        body["data"]["risk_categories"].as_array().map(Vec::len),
        Some(26)
    );
    assert_eq!(body["data"]["rules"].as_array().map(Vec::len), Some(1));
    assert!(body["data"]["rules"][0].get("patterns").is_none());
    assert_eq!(body["data"]["violation_fees"][0]["amount_usd"], 0.125);
}

#[tokio::test]
async fn public_stats_caps_the_caller_window_and_omits_rule_breakdown() {
    let backend = Arc::new(RecordingBackend {
        stats: SecurityStats {
            total_matches: 3,
            blocked_matches: 2,
            audited_matches: 1,
            affected_requests: 2,
            affected_users: 1,
            by_category: vec![SecurityStatBucket {
                key: "child_safety".to_owned(),
                count: 3,
            }],
            by_rule: vec![SecurityStatBucket {
                key: "private-rule".to_owned(),
                count: 3,
            }],
        },
        ..RecordingBackend::default()
    });
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/security/stats?start_timestamp=1&end_timestamp=10000000")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    let body = json_body(response).await;
    assert_eq!(body["data"]["start_timestamp"], 2_224_000);
    assert_eq!(body["data"]["end_timestamp"], 10_000_000);
    assert!(body["data"].get("by_rule").is_none());
    let filters = backend.stats_filters.lock().expect("stats recording lock");
    assert_eq!(
        filters.as_slice(),
        &[SecurityEventFilter {
            start_timestamp: 2_224_000,
            end_timestamp: 10_000_000,
            ..SecurityEventFilter::default()
        }]
    );
}

#[tokio::test]
async fn administrator_auth_precedes_filter_parsing_and_l0_is_hidden() {
    let backend = Arc::new(RecordingBackend::default());
    let unauthenticated = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/security/admin/stats?start_timestamp=bad")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");
    assert_eq!(unauthenticated.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        json_body(unauthenticated).await["code"],
        "AUTH_UNAUTHORIZED"
    );

    let l0 = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/security/admin/policy")
                .header(header::AUTHORIZATION, "Bearer l0")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");
    assert_eq!(l0.status(), StatusCode::NOT_FOUND);
    assert!(l0.headers().get("auth-version").is_none());
    assert_eq!(json_body(l0).await, json!({"message": "Not Found"}));
    assert_eq!(backend.policy_calls.load(Ordering::Relaxed), 0);
    assert!(
        backend
            .stats_filters
            .lock()
            .expect("stats recording lock")
            .is_empty()
    );
}

#[tokio::test]
async fn administrator_policy_exposes_patterns_only_after_admin_auth() {
    let backend = Arc::new(RecordingBackend {
        policy: policy_fixture(),
        ..RecordingBackend::default()
    });
    let response = app(backend)
        .oneshot(
            Request::get("/api/security/admin/policy")
                .header(header::AUTHORIZATION, "admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
    let body = json_body(response).await;
    assert_eq!(
        body["data"]["settings"],
        json!({
            "enabled": true,
            "on_prompt": false,
            "action": "audit",
        })
    );
    assert_eq!(body["data"]["rules"].as_array().map(Vec::len), Some(2));
    assert_eq!(body["data"]["rules"][0]["patterns"][0], "private matcher");
}

#[tokio::test]
async fn administrator_stats_normalizes_filters_and_reports_rule_buckets() {
    let backend = Arc::new(RecordingBackend {
        stats: SecurityStats {
            by_rule: vec![SecurityStatBucket {
                key: "rule-a".to_owned(),
                count: 4,
            }],
            ..SecurityStats::default()
        },
        ..RecordingBackend::default()
    });
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/security/admin/stats?start_timestamp=10&end_timestamp=20&user_id=7&rule_id=%20r1%20&category=%20CHILD_SAFETY%20&decision=%20BLOCKED%20&model_name=%20claude%20")
                .header(header::AUTHORIZATION, "Bearer admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(
        response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    let body = json_body(response).await;
    assert_eq!(
        body["data"]["by_rule"][0],
        json!({"key": "rule-a", "count": 4})
    );
    let filters = backend.stats_filters.lock().expect("stats recording lock");
    assert_eq!(
        filters.as_slice(),
        &[SecurityEventFilter {
            start_timestamp: 10,
            end_timestamp: 20,
            user_id: 7,
            rule_id: "r1".to_owned(),
            category: "child_safety".to_owned(),
            decision: "blocked".to_owned(),
            model_name: "claude".to_owned(),
        }]
    );
}

#[tokio::test]
async fn administrator_events_preserve_page_aliases_and_safe_projection() {
    let event = SecurityEvent {
        id: 9,
        created_at: 123,
        request_id: "request-1".to_owned(),
        user_id: 7,
        username: "alice".to_owned(),
        token_id: 8,
        channel_id: 3,
        model_name: "claude".to_owned(),
        group: "default".to_owned(),
        endpoint: "/v1/messages".to_owned(),
        decision: "audited".to_owned(),
        rule_id: "rule-a".to_owned(),
        rule_name: "Rule A".to_owned(),
        category: "custom".to_owned(),
        layer: "custom".to_owned(),
        severity: "medium".to_owned(),
        source: "local_custom".to_owned(),
        rule_version: "v1".to_owned(),
        pattern_digest: "pattern-digest".to_owned(),
        input_digest: "input-digest".to_owned(),
        match_count: 1,
    };
    let backend = Arc::new(RecordingBackend {
        events: vec![event],
        total: 5,
        ..RecordingBackend::default()
    });
    let response = app(Arc::clone(&backend))
        .oneshot(
            Request::get("/api/security/admin/events?p=3&page_size=0&ps=2&decision=audited")
                .header(header::AUTHORIZATION, "Bearer admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    let body = json_body(response).await;
    assert_eq!(body["data"]["page"], 3);
    assert_eq!(body["data"]["page_size"], 2);
    assert_eq!(body["data"]["total"], 5);
    assert!(body["data"]["items"][0].get("prompt").is_none());
    assert!(body["data"]["items"][0].get("patterns").is_none());
    let calls = backend.event_calls.lock().expect("event recording lock");
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].0.decision, "audited");
    assert_eq!((calls[0].1, calls[0].2), (2, 4));
}

#[tokio::test]
async fn authenticated_filter_errors_keep_http_200_and_auth_version() {
    let backend = Arc::new(RecordingBackend::default());
    let response = app(backend)
        .oneshot(
            Request::get("/api/security/admin/events?end_timestamp=1&start_timestamp=2")
                .header(header::AUTHORIZATION, "admin")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["auth-version"],
        "864b7076dbcd0a3c01b5520316720ebf"
    );
    assert_eq!(
        json_body(response).await,
        json!({
            "success": false,
            "message": "end_timestamp must be greater than or equal to start_timestamp",
        })
    );
}
