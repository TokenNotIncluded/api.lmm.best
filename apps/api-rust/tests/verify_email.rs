use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    ClientIpKey,
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::verify_email::{
        EmailVerificationRateLimitOutcome, EmailVerificationRateLimiter, SecurityEmailMessage,
        SecurityEmailSender, VerificationCodeStore, VerifyEmailAuthError,
        VerifyEmailDependencyError, VerifyEmailIdentity, VerifyEmailIdentityResolver,
        VerifyEmailState, router,
    },
};
use secrecy::SecretString;
use serde_json::Value;
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[derive(Clone)]
struct Identity(Result<VerifyEmailIdentity, VerifyEmailAuthError>);

#[async_trait]
impl VerifyEmailIdentityResolver for Identity {
    async fn resolve(
        &self,
        _: &axum::http::HeaderMap,
    ) -> Result<VerifyEmailIdentity, VerifyEmailAuthError> {
        self.0.clone()
    }
}

#[derive(Default)]
struct Codes {
    registered: Mutex<Vec<(String, String, String, u64)>>,
    deleted: Mutex<Vec<(String, String)>>,
}

#[async_trait]
impl VerificationCodeStore for Codes {
    async fn register(
        &self,
        email: &str,
        code: &str,
        purpose: &str,
        ttl: std::time::Duration,
    ) -> Result<(), VerifyEmailDependencyError> {
        self.registered
            .lock()
            .expect("code registration lock")
            .push((
                email.to_owned(),
                code.to_owned(),
                purpose.to_owned(),
                ttl.as_secs(),
            ));
        Ok(())
    }

    async fn delete(&self, email: &str, purpose: &str) -> Result<(), VerifyEmailDependencyError> {
        self.deleted
            .lock()
            .expect("code deletion lock")
            .push((email.to_owned(), purpose.to_owned()));
        Ok(())
    }
}

struct Limiter(EmailVerificationRateLimitOutcome);

#[async_trait]
impl EmailVerificationRateLimiter for Limiter {
    async fn take(&self, _: &str) -> EmailVerificationRateLimitOutcome {
        self.0
    }
}

struct Mailer {
    messages: Mutex<Vec<SecurityEmailMessage>>,
    error: Option<VerifyEmailDependencyError>,
}

#[async_trait]
impl SecurityEmailSender for Mailer {
    async fn send(&self, message: &SecurityEmailMessage) -> Result<(), VerifyEmailDependencyError> {
        self.messages
            .lock()
            .expect("mail recording lock")
            .push(message.clone());
        self.error.clone().map_or(Ok(()), Err)
    }
}

fn base_state() -> VerifyEmailState {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey.clone(),
            AuthConfig {
                session_secret: SecretString::from(
                    "verify-email-route-test-secret-012345678901234567890123456789",
                ),
                critical_rate_limit_enabled: false,
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );
    VerifyEmailState::new(pg, valkey, auth).with_system_name("LMM API")
}

fn active_identity(email: &str) -> VerifyEmailIdentity {
    VerifyEmailIdentity {
        id: 7,
        username: "mail-user".to_owned(),
        role: 1,
        status: 1,
        email: email.to_owned(),
        developer_access_granted: true,
    }
}

fn request() -> Request<Body> {
    let mut request = Request::post("/api/verify/email")
        .body(Body::empty())
        .expect("route request");
    request
        .extensions_mut()
        .insert(ClientIpKey("192.0.2.44".to_owned()));
    request
}

#[tokio::test]
async fn unauthenticated_request_is_rejected_before_route_dependencies() {
    let response = router(base_state())
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
}

#[tokio::test]
async fn l0_identity_is_hidden_as_not_found() {
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Err(VerifyEmailAuthError::ConsoleHidden))));
    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert!(response.headers().get("auth-version").is_none());
}

#[tokio::test]
async fn normal_state_does_not_send_a_code_without_a_compatible_verify_consumer() {
    let mailer = Arc::new(Mailer {
        messages: Mutex::new(Vec::new()),
        error: None,
    });
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity("bound@example.com")))))
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::Allowed,
        )))
        .with_mailer(mailer.clone());

    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert!(response.headers().get("auth-version").is_some());
    assert!(response.headers().get(header::CACHE_CONTROL).is_some());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("failure envelope"),
        serde_json::json!({"success": false, "message": "安全验证服务暂不可用"})
    );
    assert!(mailer.messages.lock().expect("sent messages").is_empty());
}

#[tokio::test]
async fn successful_delivery_registers_security_code_and_masks_bound_email() {
    let codes = Arc::new(Codes::default());
    let mailer = Arc::new(Mailer {
        messages: Mutex::new(Vec::new()),
        error: None,
    });
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity(
            " Alice@Example.COM ",
        )))))
        .with_code_store(codes.clone())
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::Allowed,
        )))
        .with_mailer(mailer.clone());

    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()[header::CACHE_CONTROL],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert!(response.headers().get("auth-version").is_some());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("success envelope"),
        serde_json::json!({
            "success": true,
            "message": "安全验证码已发送",
            "data": {"email_hint": "a***e@example.com"}
        })
    );
    let registered = codes.registered.lock().expect("registered codes");
    assert_eq!(registered[0].0, "alice@example.com");
    assert_eq!(registered[0].2, "s");
    assert_eq!(registered[0].3, 600);
    let messages = mailer.messages.lock().expect("sent messages");
    assert_eq!(messages[0].from_name, "LMM API");
    assert_eq!(messages[0].subject, "LMM API安全验证邮件");
    assert_eq!(messages[0].recipient, "alice@example.com");
    assert!(messages[0].html.contains(&registered[0].1));
    assert_eq!(
        messages[0].html,
        format!(
            "<p>您好，你正在进行LMM API敏感操作安全验证。</p>\
             <p>您的验证码为: <strong>{}</strong></p>\
             <p>验证码 10 分钟内有效。如果不是本人操作，请忽略。</p>",
            registered[0].1
        )
    );
}

#[tokio::test]
async fn bound_email_is_required_after_both_rate_limiters() {
    let codes = Arc::new(Codes::default());
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity("  ")))))
        .with_code_store(codes.clone())
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::Allowed,
        )))
        .with_mailer(Arc::new(Mailer {
            messages: Mutex::new(Vec::new()),
            error: None,
        }));

    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::UNPROCESSABLE_ENTITY);
    assert!(response.headers().get("auth-version").is_some());
    assert!(response.headers().get(header::CACHE_CONTROL).is_some());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("validation envelope"),
        serde_json::json!({
            "success": false,
            "code": "SECURITY_EMAIL_REQUIRED",
            "message": "请先绑定邮箱后再使用邮箱验证"
        })
    );
    assert!(
        codes
            .registered
            .lock()
            .expect("registered codes")
            .is_empty()
    );
}

#[tokio::test]
async fn mail_failure_deletes_the_registered_security_code() {
    let codes = Arc::new(Codes::default());
    let mailer = Arc::new(Mailer {
        messages: Mutex::new(Vec::new()),
        error: Some(VerifyEmailDependencyError::new("smtp rejected message")),
    });
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity("bound@example.com")))))
        .with_code_store(codes.clone())
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::Allowed,
        )))
        .with_mailer(mailer);

    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("failure envelope"),
        serde_json::json!({"success": false, "message": "smtp rejected message"})
    );
    assert_eq!(
        *codes.deleted.lock().expect("deleted codes"),
        vec![("bound@example.com".to_owned(), "s".to_owned())]
    );
}

#[tokio::test]
async fn unconfigured_production_mailer_fails_closed_and_deletes_the_code() {
    let codes = Arc::new(Codes::default());
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity("bound@example.com")))))
        .with_code_store(codes.clone())
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::Allowed,
        )));

    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::OK);
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("failure envelope"),
        serde_json::json!({"success": false, "message": "SMTP 服务器未配置"})
    );
    assert_eq!(codes.deleted.lock().expect("deleted codes").len(), 1);
}

#[tokio::test]
async fn email_limiter_rejection_precedes_disable_cache() {
    let state = base_state()
        .with_identity_resolver(Arc::new(Identity(Ok(active_identity("bound@example.com")))))
        .with_email_rate_limiter(Arc::new(Limiter(
            EmailVerificationRateLimitOutcome::RedisRejected {
                retry_after_seconds: 17,
            },
        )));
    let response = router(state)
        .oneshot(request())
        .await
        .expect("route response");

    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert!(response.headers().get(header::CACHE_CONTROL).is_none());
    assert!(response.headers().get("auth-version").is_some());
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body");
    assert_eq!(
        serde_json::from_slice::<Value>(&body).expect("rate-limit envelope"),
        serde_json::json!({
            "success": false,
            "message": "发送过于频繁，请等待 17 秒后再试"
        })
    );
}
