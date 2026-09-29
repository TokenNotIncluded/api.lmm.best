use std::{
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
    response::Response,
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    },
    routes::mandatory_announcements::{
        AnnouncementError, MandatoryAnnouncement, MandatoryAnnouncementState,
        MandatoryAnnouncementStore, router,
    },
};
use secrecy::{ExposeSecret, SecretString};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tower::ServiceExt;

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
    async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
    async fn generate_personal_access_token(&self, _: SecretString) -> Result<String, AuthError> {
        Err(AuthError::new(AuthErrorKind::Unauthorized))
    }
    async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
        let (id, role) = match token.expose_secret() {
            "user" => (1, 1),
            "other" => (2, 1),
            "admin" => (3, 10),
            "root" => (4, 100),
            _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
        };
        Ok(DashboardUser {
            id,
            role,
            username: format!("announcement-{id}"),
            display_name: String::new(),
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
}

#[derive(Default)]
struct FixtureStore {
    calls: AtomicUsize,
    acknowledgements: Mutex<Vec<(i64, i64, String)>>,
}

#[async_trait]
impl MandatoryAnnouncementStore for FixtureStore {
    async fn status(&self, user_id: i64) -> Result<Vec<MandatoryAnnouncement>, AnnouncementError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Ok(vec![MandatoryAnnouncement {
            id: user_id,
            mandatory: true,
            content: "Terms".to_owned(),
            ..Default::default()
        }])
    }
    async fn acknowledge(
        &self,
        user_id: i64,
        id: i64,
        revision: &str,
    ) -> Result<(), AnnouncementError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        if id <= 0 || revision.len() != 64 {
            return Err(AnnouncementError::InvalidData);
        }
        if id == 2 {
            return Err(AnnouncementError::Order);
        }
        self.acknowledgements
            .lock()
            .unwrap()
            .push((user_id, id, revision.to_owned()));
        Ok(())
    }
    async fn target_role(&self, user_id: i64) -> Result<i64, AnnouncementError> {
        self.calls.fetch_add(1, Ordering::SeqCst);
        match user_id {
            1 | 2 => Ok(1),
            3 => Ok(10),
            4 => Ok(100),
            _ => Err(AnnouncementError::NotFound),
        }
    }
}

fn fixture() -> (axum::Router, Arc<FixtureStore>) {
    let store = Arc::new(FixtureStore::default());
    (
        router(MandatoryAnnouncementState::with_store(
            store.clone(),
            Arc::new(FixtureAuth),
        )),
        store,
    )
}

fn request(method: &str, path: &str, token: Option<&str>, body: impl Into<Body>) -> Request<Body> {
    let mut builder = Request::builder()
        .method(method)
        .uri(path)
        .header("content-type", "application/json");
    if let Some(token) = token {
        builder = builder.header("authorization", format!("Bearer {token}"));
    }
    builder.body(body.into()).unwrap()
}

async fn body(response: Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), 64 * 1024).await.unwrap()).unwrap()
}

#[tokio::test]
async fn routes_authenticate_before_reading_body_or_accessing_store() {
    let (app, store) = fixture();
    for (method, path) in [
        ("GET", "/api/user/self/announcements"),
        ("POST", "/api/user/self/announcements/read"),
        ("GET", "/api/user/1/announcements"),
    ] {
        let response = app
            .clone()
            .oneshot(request(method, path, None, "{".repeat(2048)))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert!(!response.headers().contains_key("cache-control"));
        assert_eq!(body(response).await["code"], "AUTH_UNAUTHORIZED");
    }
    assert_eq!(store.calls.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn self_status_is_available_to_l0_and_disables_caching() {
    let (app, _) = fixture();
    let response = app
        .oneshot(request(
            "GET",
            "/api/user/self/announcements",
            Some("user"),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        response.headers()["cache-control"],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    assert_eq!(response.headers()["pragma"], "no-cache");
    assert_eq!(response.headers()["expires"], "0");
    assert_eq!(
        response.headers()["content-type"],
        "application/json; charset=utf-8"
    );
    let value = body(response).await;
    assert_eq!(value["success"], true);
    assert_eq!(value["message"], "");
    assert_eq!(value["data"][0]["id"], 1);
    assert!(value["data"][0].get("extra").is_none());
    assert!(value["data"][0].get("ackRevision").is_none());
}

#[tokio::test]
async fn acknowledgement_body_limit_distinguishes_declared_and_streamed_overflow() {
    let (app, store) = fixture();
    let path = "/api/user/self/announcements/read";
    let mut declared = request("POST", path, Some("user"), Body::empty());
    declared
        .headers_mut()
        .insert("content-length", "1025".parse().unwrap());
    let response = app.clone().oneshot(declared).await.unwrap();
    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    assert_eq!(to_bytes(response.into_body(), 1024).await.unwrap().len(), 0);
    for content in [" ".repeat(1025), "{".to_owned(), r#"{"id":"1"}"#.to_owned()] {
        let response = app
            .clone()
            .oneshot(request("POST", path, Some("user"), content))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert_eq!(
            body(response).await["message"],
            "Invalid announcement acknowledgement"
        );
    }
    assert_eq!(store.calls.load(Ordering::SeqCst), 0);
}

#[tokio::test]
async fn acknowledgement_preserves_go_error_status_and_returns_new_status() {
    let (app, store) = fixture();
    let path = "/api/user/self/announcements/read";
    let revision = "a".repeat(64);
    let response = app
        .clone()
        .oneshot(request("POST", path, Some("user"), "null"))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        body(response).await,
        json!({"success":false,"message":"unsupported data"})
    );
    let response = app
        .clone()
        .oneshot(request(
            "POST",
            path,
            Some("user"),
            json!({"id":2,"revision":revision}).to_string(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::CONFLICT);
    assert_eq!(
        body(response).await["message"],
        "announcement changed or an earlier announcement must be acknowledged first"
    );
    let response = app
        .oneshot(request(
            "POST",
            path,
            Some("user"),
            format!(r#"{{"ID":1,"ReViSiOn":"{revision}"}} {{}}"#),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(body(response).await["data"][0]["id"], 1);
    assert_eq!(*store.acknowledgements.lock().unwrap(), [(1, 1, revision)]);
}

#[tokio::test]
async fn administrator_cannot_inspect_peer_but_root_can_inspect_root() {
    let (app, store) = fixture();
    let response = app
        .clone()
        .oneshot(request(
            "GET",
            "/api/user/3/announcements",
            Some("admin"),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert!(
        to_bytes(response.into_body(), 1024)
            .await
            .unwrap()
            .is_empty()
    );
    let response = app
        .clone()
        .oneshot(request(
            "GET",
            "/api/user/4/announcements",
            Some("root"),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(body(response).await["data"][0]["id"], 4);
    let before = store.calls.load(Ordering::SeqCst);
    let response = app
        .oneshot(request(
            "GET",
            "/api/user/2/announcements",
            Some("user"),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(store.calls.load(Ordering::SeqCst), before);
}

#[tokio::test]
async fn administrator_invalid_id_keeps_go_api_error_envelope() {
    let (app, store) = fixture();
    for (id, reason) in [
        ("wrong", "invalid syntax"),
        ("9223372036854775808", "value out of range"),
    ] {
        let response = app
            .clone()
            .oneshot(request(
                "GET",
                &format!("/api/user/{id}/announcements"),
                Some("admin"),
                Body::empty(),
            ))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            body(response).await,
            json!({"success":false,"message":format!("strconv.Atoi: parsing {id:?}: {reason}")})
        );
    }
    assert_eq!(store.calls.load(Ordering::SeqCst), 0);
}

async fn set_announcements(pool: &PgPool, value: Value) {
    sqlx::query("INSERT INTO options(key,value) VALUES ('console_setting.announcements',$1) ON CONFLICT(key) DO UPDATE SET value=excluded.value")
        .bind(value.to_string()).execute(pool).await.unwrap();
}

async fn pg_status(app: &axum::Router, token: &str) -> Value {
    let response = app
        .clone()
        .oneshot(request(
            "GET",
            "/api/user/self/announcements",
            Some(token),
            Body::empty(),
        ))
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let value = body(response).await;
    assert_eq!(value["success"], true, "{value}");
    value["data"].clone()
}

async fn pg_ack(app: &axum::Router, id: i64, revision: &str) -> Response {
    app.clone()
        .oneshot(request(
            "POST",
            "/api/user/self/announcements/read",
            Some("user"),
            json!({"id":id,"revision":revision}).to_string(),
        ))
        .await
        .unwrap()
}

#[tokio::test]
#[ignore = "requires dedicated PostgreSQL via LMM_MANDATORY_ANNOUNCEMENTS_TEST_DATABASE_URL"]
async fn postgres_orders_replays_and_revisions_are_account_scoped_and_atomic() {
    let url = std::env::var("LMM_MANDATORY_ANNOUNCEMENTS_TEST_DATABASE_URL")
        .expect("dedicated PostgreSQL test URL is required");
    let admin = PgPoolOptions::new()
        .max_connections(1)
        .connect(&url)
        .await
        .unwrap();
    let schema = format!("lmm_announcements_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let search_path = format!("SET search_path TO {schema}");
    let pool = PgPoolOptions::new()
        .max_connections(6)
        .acquire_timeout(Duration::from_secs(5))
        .after_connect(move |connection, _| {
            let statement = search_path.clone();
            Box::pin(async move {
                sqlx::query(&statement).execute(connection).await?;
                Ok(())
            })
        })
        .connect(&url)
        .await
        .unwrap();
    let migration = include_str!("../migrations/0011_mandatory_announcements.sql")
        .replace("__LMM_APP_SCHEMA__", &schema);
    sqlx::raw_sql(&migration).execute(&pool).await.unwrap();
    sqlx::raw_sql(&migration).execute(&pool).await.unwrap();
    sqlx::raw_sql("CREATE TABLE users(id BIGINT PRIMARY KEY,role BIGINT NOT NULL,deleted_at TIMESTAMPTZ); CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT NOT NULL); INSERT INTO users(id,role) VALUES(1,1),(2,1),(3,10),(4,100)").execute(&pool).await.unwrap();
    let app = router(MandatoryAnnouncementState::new(
        pool.clone(),
        Arc::new(FixtureAuth),
    ));
    let mut items = json!([
        {"id":2,"content":"Second","publishDate":"2025-02-02T00:00:00Z","mandatory":true},
        {"id":1,"content":"First <terms>&","publishDate":"2025-02-01T00:00:00Z","mandatory":true},
        {"id":3,"content":"Future","publishDate":"2099-01-01T00:00:00Z","mandatory":true}
    ]);
    set_announcements(&pool, items.clone()).await;
    let status = pg_status(&app, "user").await;
    assert_eq!(status.as_array().unwrap().len(), 2);
    assert_eq!(status[0]["id"], 1);
    let first = status[0]["revision"].as_str().unwrap();
    let second = status[1]["revision"].as_str().unwrap();
    assert_eq!(pg_ack(&app, 2, second).await.status(), StatusCode::CONFLICT);
    let (first_response, replay_response) =
        tokio::join!(pg_ack(&app, 1, first), pg_ack(&app, 1, first));
    assert_eq!(body(first_response).await["success"], true);
    assert_eq!(body(replay_response).await["success"], true);
    assert_eq!(body(pg_ack(&app, 2, second).await).await["success"], true);
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
    assert_eq!(pg_status(&app, "other").await[0]["read_at"], 0);
    items[1]["content"] = json!("Corrected first announcement");
    set_announcements(&pool, items.clone()).await;
    let updated = pg_status(&app, "user").await;
    assert_eq!(updated[0]["read_at"], 0);
    assert!(updated[1]["read_at"].as_i64().unwrap() > 0);
    assert_eq!(pg_ack(&app, 1, first).await.status(), StatusCode::CONFLICT);
    assert_eq!(
        body(pg_ack(&app, 1, updated[0]["revision"].as_str().unwrap()).await).await["success"],
        true
    );
    items[1]["ackRevision"] = json!(" 1 ");
    set_announcements(&pool, items.clone()).await;
    let pinned = pg_status(&app, "user").await[0]["revision"]
        .as_str()
        .unwrap()
        .to_owned();
    assert_eq!(body(pg_ack(&app, 1, &pinned).await).await["success"], true);
    items[1]["content"] = json!("Typo fixed");
    items[1]["extra"] = json!("No repeated confirmation");
    set_announcements(&pool, items.clone()).await;
    let edited = pg_status(&app, "user").await;
    assert_eq!(edited[0]["revision"], pinned);
    assert!(edited[0]["read_at"].as_i64().unwrap() > 0);
    items[1]["ackRevision"] = json!("2");
    set_announcements(&pool, items).await;
    assert_eq!(pg_status(&app, "user").await[0]["read_at"], 0);
    // An invalid revision must not create an acknowledgement or remove history.
    assert_eq!(body(pg_ack(&app, 1, "short").await).await["success"], false);
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM announcement_reads")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 4);
    pool.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
