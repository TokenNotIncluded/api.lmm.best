use std::sync::Arc;

use axum::{
    body::Body,
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    auth::{AuthConfig, PgValkeyDashboardAuth},
    routes::acquisition::{AcquisitionState, Error, Input, PgAcquisitionStore, router},
};
use secrecy::SecretString;
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tower::ServiceExt;

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
                deleted_at TIMESTAMPTZ\
            );\
            CREATE TABLE casbin_rule(\
                ptype TEXT NOT NULL,\
                v0 TEXT NOT NULL,\
                v1 TEXT NOT NULL,\
                v2 TEXT NOT NULL,\
                v3 TEXT NOT NULL DEFAULT ''\
            );\
            INSERT INTO users VALUES(7,1700000000,NULL);",
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

    fixture.store.withdraw(Some(&visitor), 7).await.unwrap();
    let allowed: bool =
        sqlx::query_scalar("SELECT allowed FROM acquisition_consents WHERE user_id=7")
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
    assert!(!allowed);
    for table in [
        "acquisition_visitors",
        "acquisition_visits",
        "acquisition_accounts",
    ] {
        let count: i64 = sqlx::query_scalar(&format!("SELECT COUNT(*) FROM {table}"))
            .fetch_one(&fixture.pg)
            .await
            .unwrap();
        assert_eq!(count, 0, "{table}");
    }
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
