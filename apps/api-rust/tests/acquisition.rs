use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Method, Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{
        AuthBundle, AuthConfig, AuthError, AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth,
        DashboardUser, LoginOutcome, LoginRequest, LogoutRequest, LogoutResult,
        PgValkeyDashboardAuth, RequestMetadata, TwoFactorLoginRequest,
    },
    routes::acquisition::{AcquisitionState, Error, Input, PgAcquisitionStore, router},
};
use secrecy::SecretString;
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tower::ServiceExt;

#[derive(Clone)]
struct StaticAuth {
    role: i64,
}

#[async_trait]
impl DashboardAuth for StaticAuth {
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

    async fn self_user(&self, _: SecretString) -> Result<DashboardUser, AuthError> {
        Ok(DashboardUser {
            id: 1,
            username: "operator".into(),
            display_name: "Operator".into(),
            role: self.role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".into(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".into(),
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

fn app_with_static_auth(pg: PgPool, role: i64) -> axum::Router {
    router(AcquisitionState::new(
        PgAcquisitionStore::new(pg),
        Arc::new(StaticAuth { role }),
        false,
    ))
}

async fn admin_request(
    app: &axum::Router,
    method: Method,
    path: &str,
    body: Body,
) -> (StatusCode, Value) {
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header(header::AUTHORIZATION, "Bearer dashboard-token")
                .header(header::CONTENT_TYPE, "application/json")
                .body(body)
                .expect("admin request"),
        )
        .await
        .expect("admin response");
    let status = response.status();
    let body = axum::body::to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("admin response body");
    let value = if body.is_empty() {
        Value::Null
    } else {
        serde_json::from_slice(&body).expect("JSON admin response")
    };
    (status, value)
}

fn app() -> axum::Router {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey,
            AuthConfig {
                session_secret: SecretString::from(
                    "acquisition-route-test-secret-012345678901234567890123456789",
                ),
                ..AuthConfig::default()
            },
        )
        .expect("route-test auth adapter"),
    );

    router(AcquisitionState::new(
        PgAcquisitionStore::new(pg),
        auth,
        false,
    ))
}

#[tokio::test]
async fn protected_acquisition_routes_reject_missing_dashboard_auth_before_storage() {
    let app = app();
    for (method, path) in [
        ("POST", "/api/acquisition/consent"),
        ("GET", "/api/acquisition/self-report"),
        ("GET", "/api/admin/acquisition/links"),
        ("GET", "/api/admin/acquisition/users/7/corrections"),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri(path)
                    .body(Body::empty())
                    .expect("route request"),
            )
            .await
            .expect("route response");

        assert_eq!(
            response.status(),
            StatusCode::UNAUTHORIZED,
            "{method} {path}"
        );
    }
}

#[tokio::test]
async fn public_visit_fails_closed_without_storage_for_a_malformed_body() {
    let response = app()
        .oneshot(
            Request::post("/api/acquisition/visit")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from("{"))
                .expect("visit request"),
        )
        .await
        .expect("visit response");

    assert_eq!(response.status(), StatusCode::NO_CONTENT);
    assert_eq!(
        response
            .headers()
            .get(header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok()),
        Some("no-store, no-cache, must-revalidate, private, max-age=0")
    );
}

#[tokio::test]
async fn correction_admin_http_rejects_unprivileged_and_invalid_requests_before_storage() {
    let lazy_pool = || {
        PgPoolOptions::new()
            .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
            .expect("lazy PostgreSQL pool")
    };
    let ordinary = app_with_static_auth(lazy_pool(), 1);
    for method in [Method::GET, Method::POST] {
        let (status, body) = admin_request(
            &ordinary,
            method,
            "/api/admin/acquisition/users/7/corrections",
            Body::from("{}"),
        )
        .await;
        assert_eq!(status, StatusCode::FORBIDDEN);
        assert_eq!(body["success"], false);
        assert_eq!(body["code"], "AUTH_INSUFFICIENT_PRIVILEGE");
    }

    let root = app_with_static_auth(lazy_pool(), 100);
    let (status, body) = admin_request(
        &root,
        Method::GET,
        "/api/admin/acquisition/users/0/corrections",
        Body::empty(),
    )
    .await;
    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert!(body.is_null());

    let response = root
        .oneshot(
            Request::post("/api/admin/acquisition/users/7/corrections")
                .header(header::AUTHORIZATION, "Bearer dashboard-token")
                .header(header::CONTENT_LENGTH, "4097")
                .body(Body::empty())
                .expect("oversized correction request"),
        )
        .await
        .expect("oversized correction response");
    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
}

struct PgFixture {
    admin: PgPool,
    pg: PgPool,
    schema: String,
    store: PgAcquisitionStore,
}

impl PgFixture {
    async fn new() -> Self {
        let url = std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL");
        let admin = PgPool::connect(&url).await.expect("admin pool");
        let schema = format!("acquisition_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .expect("create isolated schema");
        let pg = PgPoolOptions::new()
            .max_connections(8)
            .after_connect({
                let schema = schema.clone();
                move |connection, _| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&url)
            .await
            .expect("isolated pool");
        sqlx::raw_sql(
            "CREATE TABLE users(\
                id BIGINT PRIMARY KEY,\
                created_at BIGINT NOT NULL,\
                role BIGINT NOT NULL DEFAULT 0,\
                deleted_at TIMESTAMPTZ\
            );\
            CREATE TABLE casbin_rule(\
                ptype TEXT NOT NULL,\
                v0 TEXT NOT NULL,\
                v1 TEXT NOT NULL,\
                v2 TEXT NOT NULL,\
                v3 TEXT NOT NULL DEFAULT ''\
            );\
            INSERT INTO users VALUES(7,1700000000,0,NULL);",
        )
        .execute(&pg)
        .await
        .expect("supporting schema");
        let migration = include_str!("../migrations/0017_acquisition_foundation.sql")
            .replace("__LMM_APP_SCHEMA__", &schema);
        sqlx::raw_sql(&migration)
            .execute(&pg)
            .await
            .expect("acquisition schema");
        let store = PgAcquisitionStore::new(pg.clone());
        Self {
            admin,
            pg,
            schema,
            store,
        }
    }

    async fn set_visit_time(&self, visit: i64, created_at: i64) {
        let result = sqlx::query("UPDATE acquisition_visits SET created_at=$1 WHERE id=$2")
            .bind(created_at)
            .bind(visit)
            .execute(&self.pg)
            .await
            .unwrap();
        assert_eq!(result.rows_affected(), 1);
    }

    async fn cleanup(self) {
        self.pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await
            .expect("drop isolated schema");
        self.admin.close().await;
    }
}

fn input(value: Value) -> Input {
    serde_json::from_value(value).expect("acquisition input")
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_link_policy_and_lookback_contracts_are_durable() {
    let fixture = PgFixture::new().await;
    assert!(fixture.store.permission(7, 100, "write").await.unwrap());
    assert!(!fixture.store.permission(7, 1, "read").await.unwrap());
    sqlx::raw_sql(
        "INSERT INTO casbin_rule VALUES\
            ('p','role:admin','acquisition','read','allow'),\
            ('p','user:7','acquisition','read','deny');",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();
    assert!(!fixture.store.permission(7, 10, "read").await.unwrap());
    sqlx::query("DELETE FROM casbin_rule WHERE v0='user:7'")
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert!(fixture.store.permission(7, 10, "read").await.unwrap());

    let link = fixture
        .store
        .save_link(input(json!({
            "name":"Docs campaign",
            "source":"community",
            "medium":"documentation",
            "campaign":"launch",
            "content":"readme",
            "target":"/guide"
        })))
        .await
        .unwrap();
    assert_eq!(link.id.len(), 32);
    let preview = fixture.store.preview(&link.id).await.unwrap();
    assert_eq!(preview["source"], "community");
    assert_eq!(preview["target"], "/guide");
    let page = fixture.store.links(1, 20, "active", "docs").await.unwrap();
    assert_eq!(page["total"], 1);
    assert_eq!(page["items"][0]["id"], link.id);

    fixture.store.lookback(30).await.unwrap();
    fixture.store.lookback(45).await.unwrap();
    fixture.store.lookback(45).await.unwrap();
    let config: i64 =
        sqlx::query_scalar("SELECT lookback_days FROM acquisition_configs WHERE id=1")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    let policies: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_attribution_policies")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(config, 45);
    assert_eq!(policies, 2, "unchanged lookback must not duplicate history");

    fixture.store.delete_link(&link.id).await.unwrap();
    assert!(matches!(
        fixture.store.preview(&link.id).await,
        Err(Error::Database(sqlx::Error::RowNotFound))
    ));
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_link_updates_filters_searches_and_deletes_are_durable() {
    let fixture = PgFixture::new().await;

    for invalid in [
        json!({"name":"Missing source","target":"/guide"}),
        json!({"name":"Missing target","source":"community"}),
        json!({"name":"External target","source":"community","target":"https://example.com"}),
    ] {
        assert!(matches!(
            fixture.store.save_link(input(invalid)).await,
            Err(Error::Invalid(_))
        ));
    }
    let count: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_links")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(count, 0, "invalid links must not persist partial rows");

    let original = fixture
        .store
        .save_link(input(json!({
            "name":"Literal_mark",
            "source":"community",
            "medium":"documentation",
            "campaign":"Launch",
            "content":"readme",
            "target":"/guide"
        })))
        .await
        .unwrap();
    let wildcard_decoy = fixture
        .store
        .save_link(input(json!({
            "name":"LiteralXmark",
            "source":"social",
            "target":"/pricing"
        })))
        .await
        .unwrap();
    let archived = fixture
        .store
        .save_link(input(json!({
            "name":"Archive match",
            "source":"newsletter",
            "target":"/sign-up",
            "archived":true
        })))
        .await
        .unwrap();
    let deleted = fixture
        .store
        .save_link(input(json!({
            "name":"Deleted match",
            "source":"partner",
            "target":"/challenges"
        })))
        .await
        .unwrap();

    let updated = fixture
        .store
        .save_link(input(json!({
            "id":original.id,
            "name":"Renamed_mark",
            "source":"must-not-replace",
            "medium":"must-not-replace",
            "campaign":"must-not-replace",
            "content":"must-not-replace",
            "target":"/pricing",
            "archived":true
        })))
        .await
        .unwrap();
    assert_eq!(updated.name, "Renamed_mark");
    assert!(updated.archived);
    assert_eq!(updated.source, original.source);
    assert_eq!(updated.medium, original.medium);
    assert_eq!(updated.campaign, original.campaign);
    assert_eq!(updated.content, original.content);
    assert_eq!(updated.target, original.target);
    assert_eq!(updated.created_at, original.created_at);
    let persisted: Value =
        sqlx::query_scalar("SELECT to_jsonb(acquisition_links) FROM acquisition_links WHERE id=$1")
            .bind(&original.id)
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(persisted["name"], "Renamed_mark");
    assert_eq!(persisted["archived"], true);
    assert_eq!(persisted["source"], original.source);
    assert_eq!(persisted["medium"], original.medium);
    assert_eq!(persisted["campaign"], original.campaign);
    assert_eq!(persisted["content"], original.content);
    assert_eq!(persisted["target"], original.target);
    assert_eq!(persisted["created_at"], original.created_at);

    let literal_underscore = fixture.store.links(1, 20, "all", "_").await.unwrap();
    assert_eq!(literal_underscore["total"], 1);
    assert_eq!(literal_underscore["items"][0]["id"], original.id);
    assert_eq!(
        fixture.store.links(1, 20, "all", "%").await.unwrap()["total"],
        0,
        "SQL wildcard characters must be treated as literal search text"
    );
    assert_eq!(
        fixture
            .store
            .links(1, 20, "archived", "MATCH")
            .await
            .unwrap()["items"][0]["id"],
        archived.id,
        "searches must remain case insensitive"
    );
    let first_page = fixture.store.links(1, 1, "active", "").await.unwrap();
    let second_page = fixture.store.links(2, 1, "active", "").await.unwrap();
    assert_eq!(first_page["total"], 2);
    assert_eq!(second_page["total"], 2);
    let mut page_ids = vec![
        first_page["items"][0]["id"].as_str().unwrap().to_owned(),
        second_page["items"][0]["id"].as_str().unwrap().to_owned(),
    ];
    page_ids.sort();
    let mut expected_ids = vec![wildcard_decoy.id.clone(), deleted.id.clone()];
    expected_ids.sort();
    assert_eq!(
        page_ids, expected_ids,
        "page offsets must return each active row exactly once"
    );

    for invalid in [
        fixture.store.links(0, 20, "all", "").await,
        fixture.store.links(1, 0, "all", "").await,
        fixture.store.links(1, 20, "unknown", "").await,
        fixture.store.links(1, 20, "all", &"x".repeat(81)).await,
    ] {
        assert!(matches!(invalid, Err(Error::Invalid(_))));
    }
    assert!(matches!(
        fixture.store.delete_link("not-an-id").await,
        Err(Error::Invalid(_))
    ));

    fixture.store.delete_link(&deleted.id).await.unwrap();
    assert_eq!(
        fixture.store.links(1, 20, "deleted", "").await.unwrap()["items"][0]["id"],
        deleted.id
    );
    assert_eq!(
        fixture.store.links(1, 20, "all", "").await.unwrap()["total"],
        3,
        "the all view must still exclude soft-deleted links"
    );
    assert!(matches!(
        fixture.store.delete_link(&deleted.id).await,
        Err(Error::Database(sqlx::Error::RowNotFound))
    ));
    assert!(matches!(
        fixture
            .store
            .save_link(input(json!({
                "id":deleted.id,
                "name":"Cannot revive",
                "archived":false
            })))
            .await,
        Err(Error::Database(sqlx::Error::RowNotFound))
    ));

    let active = fixture.store.links(1, 20, "active", "").await.unwrap();
    assert_eq!(active["total"], 1);
    assert_eq!(active["items"][0]["id"], wildcard_decoy.id);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_consent_visit_report_and_withdrawal_round_trip() {
    let fixture = PgFixture::new().await;
    fixture.store.grant(7).await.unwrap();
    let consent: (bool, i64) =
        sqlx::query_as("SELECT allowed,version FROM acquisition_consents WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(consent, (true, 2));

    let link = fixture
        .store
        .save_link(input(json!({
            "name":"Launch",
            "source":"community",
            "target":"/pricing"
        })))
        .await
        .unwrap();
    let visitor = "a".repeat(64);
    let visit_input = input(json!({
        "consent":true,
        "consent_version":2,
        "nonce":"0123456789abcdef0123456789abcdef",
        "landing":"/pricing",
        "link_id":link.id
    }));
    let first = fixture
        .store
        .observe(&visitor, 7, &visit_input, &["api.lmm.best"])
        .await
        .unwrap();
    let replay = fixture
        .store
        .observe(&visitor, 7, &visit_input, &["api.lmm.best"])
        .await
        .unwrap();
    assert_eq!(replay.id, first.id);
    assert_eq!(first.source, "community");
    assert_eq!(first.evidence, "promotion_link");
    let visits: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_visits")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(visits, 1, "visitor nonce replay must stay idempotent");

    let report = fixture
        .store
        .report(
            7,
            Some(input(json!({"source":"community","detail":"Forum"}))),
            false,
        )
        .await
        .unwrap();
    assert_eq!(report["source"], "community");
    assert_eq!(report["detail"], "Forum");
    assert_eq!(
        fixture.store.report(7, None, true).await.unwrap(),
        Value::Null
    );

    // Populate every account-scoped table in the revoke contract for the withdrawing user
    // and another user. Empty tables would hide missing or overbroad deletes.
    sqlx::raw_sql(
        "INSERT INTO users VALUES(8,1700000000,0,NULL);\
        INSERT INTO acquisition_activities(user_id,day,first_at,last_at)\
            VALUES(7,1700000000,1700000000,1700000010),\
                  (7,1700086400,1700086400,1700086410),\
                  (8,1700000000,1700000000,1700000020);\
        INSERT INTO acquisition_corrections(user_id,previous_revision,source,reason)\
            VALUES(7,0,'community','fixture seven'),\
                  (7,1,'documentation','fixture seven revision two'),\
                  (8,0,'documentation','fixture eight');\
        INSERT INTO acquisition_correction_heads(user_id,revision,source,updated_at)\
            VALUES(7,2,'documentation',1700086400),\
                  (8,1,'documentation',1700000000);\
        INSERT INTO acquisition_first_payments(user_id,first_paid_at,source)\
            VALUES(7,1700000000,'community'),\
                  (8,1700000000,'documentation');",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();
    fixture.store.grant(8).await.unwrap();
    let other_visitor = "e".repeat(64);
    fixture
        .store
        .observe(&other_visitor, 8, &visit_input, &["api.lmm.best"])
        .await
        .unwrap();
    let snapshot_query = "SELECT jsonb_build_object(\
        'visitor',(SELECT to_jsonb(v) FROM acquisition_visitors v WHERE id=$1),\
        'visits',(SELECT jsonb_agg(v ORDER BY id) FROM acquisition_visits v WHERE visitor_id=$1))";
    let other_visits: Value = sqlx::query_scalar(snapshot_query)
        .bind(&other_visitor)
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    let mut unaffected = Vec::new();
    for table in [
        "acquisition_activities",
        "acquisition_corrections",
        "acquisition_correction_heads",
        "acquisition_first_payments",
        "acquisition_accounts",
    ] {
        let count: i64 =
            sqlx::query_scalar(&format!("SELECT COUNT(*) FROM {table} WHERE user_id=7"))
                .fetch_one(&fixture.pg)
                .await
                .unwrap();
        let expected = if matches!(table, "acquisition_activities" | "acquisition_corrections") {
            2
        } else {
            1
        };
        assert_eq!(
            count, expected,
            "{table} must be populated before withdrawal"
        );
        let other: Value = sqlx::query_scalar(&format!(
            "SELECT to_jsonb({table}) FROM {table} WHERE user_id=8"
        ))
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
        unaffected.push((table, other));
    }

    fixture.store.withdraw(Some(&visitor), 7).await.unwrap();
    let allowed: bool =
        sqlx::query_scalar("SELECT allowed FROM acquisition_consents WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert!(!allowed);
    for replay in [false, true] {
        if replay {
            fixture.store.withdraw(Some(&visitor), 7).await.unwrap();
        }
        for (table, column) in [
            ("acquisition_visitors", "id"),
            ("acquisition_visits", "visitor_id"),
        ] {
            let count: i64 =
                sqlx::query_scalar(&format!("SELECT COUNT(*) FROM {table} WHERE {column}=$1"))
                    .bind(&visitor)
                    .fetch_one(&fixture.pg)
                    .await
                    .unwrap();
            assert_eq!(count, 0, "{table} after withdrawal (replay={replay})");
        }
        let after: Value = sqlx::query_scalar(snapshot_query)
            .bind(&other_visitor)
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
        assert_eq!(after, other_visits, "unrelated visitor (replay={replay})");
        for (table, before) in &unaffected {
            let count: i64 =
                sqlx::query_scalar(&format!("SELECT COUNT(*) FROM {table} WHERE user_id=7"))
                    .fetch_one(&fixture.pg)
                    .await
                    .unwrap();
            assert_eq!(count, 0, "{table} after withdrawal (replay={replay})");
            let after: Value = sqlx::query_scalar(&format!(
                "SELECT to_jsonb({table}) FROM {table} WHERE user_id=8"
            ))
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
            assert_eq!(
                &after, before,
                "unrelated user in {table} (replay={replay})"
            );
        }
    }
    let consents: Vec<(i64, bool, i64)> =
        sqlx::query_as("SELECT user_id,allowed,version FROM acquisition_consents ORDER BY user_id")
            .fetch_all(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(consents, vec![(7, false, 2), (8, true, 2)]);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_self_report_writes_are_atomic_validated_and_idempotently_deleted() {
    let fixture = PgFixture::new().await;
    assert!(matches!(
        fixture
            .store
            .report(
                0,
                Some(input(json!({"source":"friend","detail":"Invalid owner"}))),
                false,
            )
            .await,
        Err(Error::Invalid(_))
    ));

    let first = fixture
        .store
        .report(
            7,
            Some(input(json!({
                "source":"community",
                "detail":"  Helpful forum thread  "
            }))),
            false,
        )
        .await
        .unwrap();
    assert_eq!(first["source"], "community");
    assert_eq!(first["detail"], "Helpful forum thread");
    let first_updated_at = first["updated_at"].as_i64().unwrap();

    assert!(matches!(
        fixture
            .store
            .report(
                7,
                Some(input(json!({
                    "source":"other",
                    "detail":"token=must-not-be-stored"
                }))),
                false,
            )
            .await,
        Err(Error::Invalid(_))
    ));
    let unchanged: (String, String, i64) = sqlx::query_as(
        "SELECT source,COALESCE(detail,''),COALESCE(updated_at,0) \
         FROM acquisition_self_reports WHERE user_id=7",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(
        unchanged,
        (
            "community".into(),
            "Helpful forum thread".into(),
            first_updated_at
        ),
        "rejected input must not partially overwrite the durable report"
    );

    let friend_store = fixture.store.clone();
    let docs_store = fixture.store.clone();
    let (friend, docs) = tokio::join!(
        friend_store.report(
            7,
            Some(input(json!({"source":"friend","detail":"Referral"}))),
            false,
        ),
        docs_store.report(
            7,
            Some(input(json!({
                "source":"documentation",
                "detail":"Migration guide"
            }))),
            false,
        )
    );
    let friend = friend.unwrap();
    let docs = docs.unwrap();
    assert_eq!(
        (friend["source"].as_str(), friend["detail"].as_str()),
        (Some("friend"), Some("Referral"))
    );
    assert_eq!(
        (docs["source"].as_str(), docs["detail"].as_str()),
        (Some("documentation"), Some("Migration guide"))
    );

    let rows: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_self_reports WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(
        rows, 1,
        "concurrent upserts must retain one report per user"
    );
    let durable: (String, String) = sqlx::query_as(
        "SELECT source,COALESCE(detail,'') FROM acquisition_self_reports WHERE user_id=7",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert!(
        durable == ("friend".into(), "Referral".into())
            || durable == ("documentation".into(), "Migration guide".into()),
        "the durable row must be one complete concurrent input, not a torn pair"
    );

    sqlx::query("UPDATE acquisition_self_reports SET updated_at=$1 WHERE user_id=7")
        .bind(chrono::Utc::now().timestamp() - 366 * 86400)
        .execute(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        fixture.store.report(7, None, false).await.unwrap(),
        Value::Null,
        "expired reports stay durable but are hidden from reads"
    );
    let retained: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_self_reports WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(retained, 1);

    assert_eq!(
        fixture.store.report(7, None, true).await.unwrap(),
        Value::Null
    );
    assert_eq!(
        fixture.store.report(7, None, true).await.unwrap(),
        Value::Null,
        "deleting an absent report must remain idempotent"
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_acquisition_audit_is_structured_and_best_effort() {
    let fixture = PgFixture::new().await;
    sqlx::raw_sql(
        "CREATE TABLE logs(\
            id BIGSERIAL PRIMARY KEY,\
            user_id BIGINT,\
            created_at BIGINT,\
            type BIGINT,\
            content TEXT,\
            username TEXT,\
            ip TEXT,\
            other TEXT\
        );",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();

    fixture
        .store
        .audit(
            7,
            100,
            "root",
            true,
            "203.0.113.7",
            "acquisition.lookback",
            json!({"days":45}),
        )
        .await;
    let stored: (i64, i64, String, String, String, Value) =
        sqlx::query_as("SELECT user_id,type,content,username,ip,other::jsonb FROM logs")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!((stored.0, stored.1), (7, 3));
    assert_eq!(stored.2, "acquisition.lookback");
    assert_eq!(stored.3, "root");
    assert_eq!(stored.4, "203.0.113.7");
    assert_eq!(stored.5["op"]["action"], "acquisition.lookback");
    assert_eq!(stored.5["op"]["params"]["days"], 45);
    assert_eq!(stored.5["admin_info"]["admin_id"], 7);
    assert_eq!(stored.5["admin_info"]["admin_role"], 100);
    assert_eq!(stored.5["admin_info"]["auth_method"], "access_token");

    sqlx::raw_sql(
        "CREATE FUNCTION reject_acquisition_audit() RETURNS trigger LANGUAGE plpgsql AS $$\
            BEGIN RAISE EXCEPTION 'forced acquisition audit failure'; END\
         $$;\
         CREATE TRIGGER reject_acquisition_audit BEFORE INSERT ON logs \
            FOR EACH ROW EXECUTE FUNCTION reject_acquisition_audit();",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();
    fixture
        .store
        .audit(
            7,
            10,
            "operator",
            false,
            "203.0.113.8",
            "acquisition.link.delete",
            json!({"link_id":"blocked"}),
        )
        .await;
    let after_failure: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs")
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    assert_eq!(
        after_failure, 1,
        "failed best-effort audit must not invent a row"
    );

    sqlx::query("DROP TRIGGER reject_acquisition_audit ON logs")
        .execute(&fixture.pg)
        .await
        .unwrap();
    fixture
        .store
        .audit(
            7,
            10,
            "operator",
            false,
            "203.0.113.9",
            "acquisition.link.delete",
            json!({"link_id":"recovered"}),
        )
        .await;
    let recovered: (i64, String) = sqlx::query_as(
        "SELECT COUNT(*),MAX(other::jsonb #>> '{admin_info,auth_method}') FROM logs",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(recovered, (2, "session".into()));
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_registration_keeps_first_touch_and_selects_last_external_visit() {
    let fixture = PgFixture::new().await;
    let registered_at: i64 = 1_800_000_000;
    sqlx::query("UPDATE users SET created_at=$1 WHERE id=7")
        .bind(registered_at)
        .execute(&fixture.pg)
        .await
        .unwrap();

    let visitor = "b".repeat(64);
    let first = fixture
        .store
        .observe(
            &visitor,
            0,
            &input(json!({
                "consent":true,
                "consent_version":1,
                "nonce":"11111111111111111111111111111111",
                "landing":"/guide",
                "source":"documentation",
                "campaign":"getting-started"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    let selected = fixture
        .store
        .observe(
            &visitor,
            0,
            &input(json!({
                "consent":true,
                "consent_version":1,
                "nonce":"22222222222222222222222222222222",
                "landing":"/pricing",
                "source":"community",
                "campaign":"launch"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    let latest = fixture
        .store
        .observe(
            &visitor,
            0,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"33333333333333333333333333333333",
                "landing":"/sign-up"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();

    // Explicit fixture timestamps keep ordering independent of runner speed.
    fixture.set_visit_time(first.id, registered_at - 100).await;
    fixture
        .set_visit_time(selected.id, registered_at - 50)
        .await;
    fixture.set_visit_time(latest.id, registered_at - 10).await;

    fixture
        .store
        .attribute_registration(7, &visitor)
        .await
        .unwrap();
    let account: Value = sqlx::query_scalar(
        "SELECT to_jsonb(acquisition_accounts) FROM acquisition_accounts WHERE user_id=7",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(account["first_visit_id"], first.id);
    assert_eq!(account["first_source"], "documentation");
    assert_eq!(account["first_evidence"], "campaign_parameters");
    assert_eq!(account["registration_visit_id"], selected.id);
    assert_eq!(account["registration_source"], "community");
    assert_eq!(account["registration_campaign"], "launch");
    assert_eq!(account["registration_evidence"], "campaign_parameters");
    assert_eq!(account["registration_inferred"], true);
    assert_eq!(account["consent_version"], latest.consent_version);
    assert_eq!(account["attribution_rule"], "current_or_last_external_30d");
    assert_eq!(account["lookback_days"], 30);

    let owner: i64 = sqlx::query_scalar("SELECT user_id FROM acquisition_visitors WHERE id=$1")
        .bind(&visitor)
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
    let consent: (bool, i64) =
        sqlx::query_as("SELECT allowed,version FROM acquisition_consents WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(owner, 7);
    assert_eq!(consent, (true, 2));

    // A newly received visit would win a fresh attribution calculation.
    // Replaying registration must preserve the original persisted snapshot.
    let later = fixture
        .store
        .observe(
            &visitor,
            7,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"66666666666666666666666666666666",
                "landing":"/guide",
                "source":"documentation",
                "campaign":"late-arrival"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    fixture.set_visit_time(later.id, registered_at - 1).await;

    fixture
        .store
        .attribute_registration(7, &visitor)
        .await
        .unwrap();
    let account_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_accounts WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(account_count, 1, "registration replay must stay idempotent");
    let replayed: Value = sqlx::query_scalar(
        "SELECT to_jsonb(acquisition_accounts) FROM acquisition_accounts WHERE user_id=7",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(
        replayed, account,
        "replay must preserve every attribution field"
    );
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_registration_cannot_override_explicit_consent_denial() {
    let fixture = PgFixture::new().await;
    fixture.store.withdraw(None, 7).await.unwrap();
    let registered_at: i64 = 1_800_000_000;
    sqlx::query("UPDATE users SET created_at=$1 WHERE id=7")
        .bind(registered_at)
        .execute(&fixture.pg)
        .await
        .unwrap();

    let visitor = "c".repeat(64);
    let visit = fixture
        .store
        .observe(
            &visitor,
            0,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"44444444444444444444444444444444",
                "landing":"/sign-up",
                "source":"community"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    fixture.set_visit_time(visit.id, registered_at - 1).await;
    fixture
        .store
        .attribute_registration(7, &visitor)
        .await
        .unwrap();

    let account_count: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_accounts WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    let consent: (bool, i64) =
        sqlx::query_as("SELECT allowed,version FROM acquisition_consents WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(account_count, 0, "denied users must not retain attribution");
    assert_eq!(consent, (false, 2));

    let denied = fixture
        .store
        .observe(
            &visitor,
            7,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"55555555555555555555555555555555",
                "landing":"/pricing",
                "source":"documentation"
            })),
            &["api.lmm.best"],
        )
        .await;
    assert!(matches!(denied, Err(Error::Invalid(_))));
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_registration_respects_visit_time_boundaries() {
    const DAY: i64 = 86_400;
    let registered_at: i64 = 1_800_000_000;
    // Each case has only one candidate, so a missing bound cannot be masked
    // by another, more recent visit. Both lower bounds are inclusive.
    for (offset, has_first, has_registration) in [
        (-90 * DAY - 1, false, false),
        (-90 * DAY, true, false),
        (-30 * DAY - 1, true, false),
        (-30 * DAY, true, true),
        (-30 * DAY + 1, true, true),
        (0, true, true),
        (1, false, false),
    ] {
        let fixture = PgFixture::new().await;
        sqlx::query("UPDATE users SET created_at=$1 WHERE id=7")
            .bind(registered_at)
            .execute(&fixture.pg)
            .await
            .unwrap();
        let visitor = "d".repeat(64);
        let visit = fixture
            .store
            .observe(
                &visitor,
                0,
                &input(json!({
                    "consent":true,
                    "consent_version":2,
                    "nonce":"77777777777777777777777777777777",
                    "landing":"/pricing",
                    "source":"community"
                })),
                &["api.lmm.best"],
            )
            .await
            .unwrap();
        fixture
            .set_visit_time(visit.id, registered_at + offset)
            .await;
        fixture
            .store
            .attribute_registration(7, &visitor)
            .await
            .unwrap();
        let account: Value = sqlx::query_scalar(
            "SELECT to_jsonb(acquisition_accounts) FROM acquisition_accounts WHERE user_id=7",
        )
        .fetch_one(&fixture.pg)
        .await
        .unwrap();
        assert_eq!(
            account["first_visit_id"],
            if has_first { visit.id } else { 0 },
            "first-touch boundary at offset {offset}"
        );
        assert_eq!(
            account["registration_visit_id"],
            if has_registration { visit.id } else { 0 },
            "registration boundary at offset {offset}"
        );
        assert_eq!(
            account["registration_source"],
            if has_registration {
                "community"
            } else {
                "unknown"
            },
            "registration source at offset {offset}"
        );
        assert_eq!(
            account["consent_version"],
            if offset <= 0 { 2 } else { 0 },
            "post-registration consent must not leak backwards at offset {offset}"
        );
        fixture.cleanup().await;
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_visit_consent_and_owner_boundaries_are_atomic() {
    let fixture = PgFixture::new().await;
    let visitor = "f".repeat(64);

    let missing_consent = fixture
        .store
        .observe(
            &visitor,
            7,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"88888888888888888888888888888888",
                "landing":"/guide",
                "source":"documentation"
            })),
            &["api.lmm.best"],
        )
        .await;
    assert!(matches!(missing_consent, Err(Error::Invalid(_))));
    let empty: (i64, i64) = sqlx::query_as(
        "SELECT (SELECT COUNT(*) FROM acquisition_visitors),\
                (SELECT COUNT(*) FROM acquisition_visits)",
    )
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(
        empty,
        (0, 0),
        "rejected consent must not persist partial rows"
    );

    let legacy = fixture
        .store
        .observe(
            &visitor,
            7,
            &input(json!({
                "consent":true,
                "consent_version":1,
                "nonce":"99999999999999999999999999999999",
                "landing":"/guide",
                "source":"documentation"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    assert_eq!(legacy.consent_version, 1);

    sqlx::query("INSERT INTO users VALUES(8,1700000000,0,NULL)")
        .execute(&fixture.pg)
        .await
        .unwrap();
    fixture.store.grant(8).await.unwrap();
    let foreign_owner = fixture
        .store
        .observe(
            &visitor,
            8,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "landing":"/pricing",
                "source":"community"
            })),
            &["api.lmm.best"],
        )
        .await;
    assert!(matches!(foreign_owner, Err(Error::Invalid(_))));

    let state: (i64, i64, i64) = sqlx::query_as(
        "SELECT (SELECT user_id FROM acquisition_visitors WHERE id=$1),\
                (SELECT COUNT(*) FROM acquisition_visits WHERE visitor_id=$1),\
                (SELECT COUNT(*) FROM acquisition_visits \
                    WHERE visitor_id=$1 AND nonce='aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa')",
    )
    .bind(&visitor)
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(
        state,
        (7, 1, 0),
        "owner conflict must roll back every write"
    );

    fixture.store.grant(7).await.unwrap();
    let upgraded = fixture
        .store
        .observe(
            &visitor,
            7,
            &input(json!({
                "consent":true,
                "consent_version":2,
                "nonce":"bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
                "landing":"/pricing",
                "source":"community"
            })),
            &["api.lmm.best"],
        )
        .await
        .unwrap();
    assert_eq!(upgraded.consent_version, 2);
    let final_state: (i64, i64, bool, i64) = sqlx::query_as(
        "SELECT (SELECT user_id FROM acquisition_visitors WHERE id=$1),\
                (SELECT COUNT(*) FROM acquisition_visits WHERE visitor_id=$1),\
                (SELECT allowed FROM acquisition_consents WHERE user_id=7),\
                (SELECT version FROM acquisition_consents WHERE user_id=7)",
    )
    .bind(&visitor)
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    assert_eq!(final_state, (7, 2, true, 2));
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_corrections_are_append_only_and_reject_stale_concurrent_writes() {
    let fixture = PgFixture::new().await;
    sqlx::query(
        "INSERT INTO acquisition_accounts(user_id,registration_source,created_at) VALUES(7,'community',$1)",
    )
    .bind(chrono::Utc::now().timestamp())
    .execute(&fixture.pg)
    .await
    .unwrap();

    let left = fixture.store.clone();
    let right = fixture.store.clone();
    let first = input(json!({
        "source":"documentation",
        "reason":"Confirmed against campaign records",
        "expected_revision":0
    }));
    let competing = input(json!({
        "source":"social",
        "reason":"Confirmed against referral records",
        "expected_revision":0
    }));
    let (left, right) = tokio::join!(
        left.save_correction(7, 101, first),
        right.save_correction(7, 102, competing)
    );
    let saved = match (left, right) {
        (Ok(saved), Err(Error::Conflict(_))) | (Err(Error::Conflict(_)), Ok(saved)) => saved,
        result => panic!("one write must win and one must conflict: {result:?}"),
    };
    assert_eq!(saved["previous_revision"], 0);
    assert_eq!(saved["previous_source"], "community");
    let revision = saved["id"].as_i64().unwrap();

    let second = fixture
        .store
        .save_correction(
            7,
            103,
            input(json!({
                "source":"client",
                "reason":"Customer attribution was verified",
                "expected_revision":revision
            })),
        )
        .await
        .unwrap();
    assert_eq!(second["previous_revision"], revision);
    assert_eq!(second["previous_source"], saved["source"]);

    let history = fixture.store.corrections(7).await.unwrap();
    assert_eq!(history["head"]["revision"], second["id"]);
    assert_eq!(history["head"]["source"], "client");
    assert_eq!(history["items"].as_array().unwrap().len(), 2);
    assert_eq!(history["items"][0]["id"], second["id"]);
    assert_eq!(history["items"][1]["id"], saved["id"]);
    assert_eq!(history["has_more"], false);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_correction_admin_http_preserves_permission_and_error_contracts() {
    let fixture = PgFixture::new().await;
    sqlx::raw_sql(
        "INSERT INTO users VALUES(9,1700000000,10,NULL);\
         INSERT INTO casbin_rule VALUES('p','role:admin','acquisition','details','allow');",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();
    let app = app_with_static_auth(fixture.pg.clone(), 10);

    let (status, body) = admin_request(
        &app,
        Method::GET,
        "/api/admin/acquisition/users/999/corrections",
        Body::empty(),
    )
    .await;
    assert_eq!(status, StatusCode::NOT_FOUND);
    assert!(body.is_null());

    let (status, body) = admin_request(
        &app,
        Method::GET,
        "/api/admin/acquisition/users/9/corrections",
        Body::empty(),
    )
    .await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert!(body.is_null());

    let (status, body) = admin_request(
        &app,
        Method::GET,
        "/api/admin/acquisition/users/7/corrections",
        Body::empty(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["success"], true);
    assert!(body["data"]["head"].is_null());
    assert!(body["data"]["items"].as_array().unwrap().is_empty());

    let correction = json!({
        "source":"documentation",
        "reason":"Verified through the administrator route",
        "expected_revision":0
    });
    let (status, denied) = admin_request(
        &app,
        Method::POST,
        "/api/admin/acquisition/users/7/corrections",
        Body::from(correction.to_string()),
    )
    .await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(denied["success"], false);
    assert_eq!(denied["message"], "Insufficient permissions");

    sqlx::query("INSERT INTO casbin_rule VALUES('p','role:admin','acquisition','write','allow')")
        .execute(&fixture.pg)
        .await
        .unwrap();

    let (status, body) = admin_request(
        &app,
        Method::POST,
        "/api/admin/acquisition/users/7/corrections",
        Body::from("{"),
    )
    .await;
    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert!(body.is_null());

    let (status, saved) = admin_request(
        &app,
        Method::POST,
        "/api/admin/acquisition/users/7/corrections",
        Body::from(correction.to_string()),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(saved["success"], true);
    assert_eq!(saved["data"]["source"], "documentation");

    let (status, stale) = admin_request(
        &app,
        Method::POST,
        "/api/admin/acquisition/users/7/corrections",
        Body::from(correction.to_string()),
    )
    .await;
    assert_eq!(status, StatusCode::CONFLICT);
    assert_eq!(stale["success"], false);
    assert_eq!(
        stale["message"],
        "Source correction changed; reload before saving"
    );

    let (status, history) = admin_request(
        &app,
        Method::GET,
        "/api/admin/acquisition/users/7/corrections",
        Body::empty(),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(history["data"]["head"]["revision"], saved["data"]["id"]);
    assert_eq!(history["data"]["items"].as_array().unwrap().len(), 1);
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_corrections_enforce_consent_target_validation_and_retention() {
    let fixture = PgFixture::new().await;
    sqlx::raw_sql(
        "INSERT INTO users VALUES(8,1,0,NULL);\
         INSERT INTO users VALUES(9,1700000000,10,NULL);\
         INSERT INTO acquisition_consents(user_id,allowed,version,updated_at)\
            VALUES(7,FALSE,2,1700000000);",
    )
    .execute(&fixture.pg)
    .await
    .unwrap();

    let denied = fixture
        .store
        .save_correction(
            7,
            101,
            input(json!({
                "source":"community",
                "reason":"Consent denial must be honored",
                "expected_revision":0
            })),
        )
        .await;
    assert!(matches!(denied, Err(Error::Invalid(_))));
    let heads: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_correction_heads WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(heads, 0, "denied correction must roll back its lock row");

    for invalid in [
        json!({"source":" community ","reason":"valid reason","expected_revision":0}),
        json!({"source":"community","reason":"no","expected_revision":0}),
        json!({"source":"community","reason":"token=private","expected_revision":0}),
    ] {
        assert!(matches!(
            fixture.store.save_correction(8, 101, input(invalid)).await,
            Err(Error::Invalid(_))
        ));
    }
    assert!(matches!(
        fixture
            .store
            .save_correction(
                9,
                101,
                input(json!({
                    "source":"community",
                    "reason":"Administrators cannot be correction targets",
                    "expected_revision":0
                }))
            )
            .await,
        Err(Error::Invalid(_))
    ));

    let saved = fixture
        .store
        .save_correction(
            8,
            101,
            input(json!({
                "source":"friend",
                "reason":"Historical account source was confirmed",
                "expected_revision":0
            })),
        )
        .await
        .unwrap();
    assert_eq!(saved["previous_source"], "historical_unrecorded");
    let old = chrono::Utc::now().timestamp() - 366 * 86400;
    sqlx::query("UPDATE acquisition_corrections SET created_at=$1 WHERE user_id=8")
        .bind(old)
        .execute(&fixture.pg)
        .await
        .unwrap();
    sqlx::query("UPDATE acquisition_correction_heads SET updated_at=$1 WHERE user_id=8")
        .bind(old)
        .execute(&fixture.pg)
        .await
        .unwrap();
    let history = fixture.store.corrections(8).await.unwrap();
    assert!(history["head"].is_null());
    assert!(history["items"].as_array().unwrap().is_empty());
    let durable: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_corrections WHERE user_id=8")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(durable, 1, "read retention must not mutate audit history");

    let restarted = fixture
        .store
        .save_correction(
            8,
            102,
            input(json!({
                "source":"documentation",
                "reason":"Current source was independently verified",
                "expected_revision":0
            })),
        )
        .await
        .expect("an expired hidden head must accept the revision exposed to clients");
    assert_eq!(restarted["previous_revision"], 0);
    assert_eq!(restarted["previous_source"], "historical_unrecorded");

    let visible = fixture.store.corrections(8).await.unwrap();
    assert_eq!(visible["head"]["revision"], restarted["id"]);
    assert_eq!(visible["head"]["source"], "documentation");
    assert_eq!(visible["items"].as_array().unwrap().len(), 1);
    assert_eq!(visible["items"][0]["id"], restarted["id"]);
    assert!(
        !visible
            .to_string()
            .contains("Historical account source was confirmed"),
        "expired correction details must remain outside the read contract"
    );
    assert!(matches!(
        fixture
            .store
            .save_correction(
                8,
                103,
                input(json!({
                    "source":"client",
                    "reason":"A stale concurrent correction must conflict",
                    "expected_revision":0
                }))
            )
            .await,
        Err(Error::Conflict(_))
    ));
    let durable: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM acquisition_corrections WHERE user_id=8")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert_eq!(durable, 2, "the expired audit row must remain append-only");
    fixture.cleanup().await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_corrections_paginate_recent_history_without_leaking_expired_rows() {
    let fixture = PgFixture::new().await;
    let now = chrono::Utc::now().timestamp();
    sqlx::query(
        "INSERT INTO acquisition_corrections(\
            user_id,previous_revision,previous_source,source,reason,actor_id,created_at)\
         SELECT 7,sequence-1,'community','documentation','verified page',101,$1 \
         FROM generate_series(1,101) AS sequence",
    )
    .bind(now)
    .execute(&fixture.pg)
    .await
    .unwrap();
    let newest_visible: i64 = sqlx::query_scalar(
        "SELECT MAX(id) FROM acquisition_corrections WHERE user_id=7 AND created_at=$1",
    )
    .bind(now)
    .fetch_one(&fixture.pg)
    .await
    .unwrap();
    let expired_id: i64 = sqlx::query_scalar(
        "INSERT INTO acquisition_corrections(\
            user_id,previous_revision,previous_source,source,reason,actor_id,created_at)\
         VALUES(7,101,'documentation','social','expired private reason',101,$1)\
         RETURNING id",
    )
    .bind(now - 366 * 86400)
    .fetch_one(&fixture.pg)
    .await
    .unwrap();

    let history = fixture.store.corrections(7).await.unwrap();
    let items = history["items"].as_array().unwrap();
    assert_eq!(items.len(), 100);
    assert_eq!(history["has_more"], true);
    assert_eq!(items[0]["id"], newest_visible);
    assert_eq!(
        items.last().unwrap()["id"].as_i64().unwrap(),
        newest_visible - 99
    );
    assert!(
        items
            .iter()
            .all(|item| item["id"].as_i64() != Some(expired_id)),
        "retention filtering must happen before the 101-row pagination probe"
    );
    fixture.cleanup().await;
}
