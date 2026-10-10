//! HTTP contracts exercised against the production PostgreSQL log store.
//! Run explicitly with LMM_TEST_DATABASE_URL; each test owns a temporary schema.

use std::{panic::AssertUnwindSafe, sync::Arc};

use async_trait::async_trait;
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use futures_util::FutureExt;
use lmm_api_rs::routes::observability::{
    ObservabilityAccess, ObservabilityAuthError, ObservabilityAuthorizer, ObservabilityPrincipal,
    ObservabilityState, ObservabilityTokenAuthorizer, PgObservabilityStore,
    PgReadOnlyObservabilityTokenAuthorizer, UnavailableObservabilityMetrics,
    observability_read_router,
};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tower::ServiceExt;

const TOKEN_A_TOTAL: i64 = 1025;

// Dashboard identity is fixed for the unrelated admin/self regression reads;
// token identity is always resolved by the real PostgreSQL token authority.
struct FixtureAuthorizer {
    token: PgReadOnlyObservabilityTokenAuthorizer,
}

#[async_trait]
impl ObservabilityAuthorizer for FixtureAuthorizer {
    async fn authorize(
        &self,
        headers: &HeaderMap,
        access: ObservabilityAccess,
    ) -> Result<ObservabilityPrincipal, ObservabilityAuthError> {
        let credential = headers
            .get("authorization")
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.strip_prefix("Bearer "))
            .ok_or(ObservabilityAuthError::Unauthorized)?;
        if access == ObservabilityAccess::Token {
            return self.token.authorize_read_only(credential).await;
        }
        if credential != "fixture_dashboard" {
            return Err(ObservabilityAuthError::Unauthorized);
        }
        Ok(ObservabilityPrincipal::User {
            user_id: 7,
            username: "reader".to_owned(),
            role: 100,
        })
    }
}

struct Fixture {
    admin: PgPool,
    pg: PgPool,
    schema: String,
}

impl Fixture {
    async fn new() -> Self {
        let url = std::env::var("LMM_TEST_DATABASE_URL")
            .expect("isolated PostgreSQL via LMM_TEST_DATABASE_URL");
        let admin = PgPool::connect(&url).await.expect("admin connection");
        let schema = format!("token_log_pages_{}", uuid::Uuid::new_v4().simple());
        if let Err(error) = sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
        {
            admin.close().await;
            panic!("temporary schema: {error}");
        }
        let connection = PgPoolOptions::new()
            .max_connections(4)
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
            .await;
        let pg = match connection {
            Ok(pg) => pg,
            Err(error) => Self::fail_setup(admin, None, &schema, "schema connection", error).await,
        };
        if let Err(error) = sqlx::raw_sql(
            r#"
            CREATE TABLE users (
                id BIGINT PRIMARY KEY,
                status BIGINT NOT NULL,
                deleted_at TIMESTAMPTZ
            );
            CREATE TABLE tokens (
                id BIGINT PRIMARY KEY,
                user_id BIGINT NOT NULL,
                key TEXT UNIQUE NOT NULL,
                status BIGINT NOT NULL,
                expired_time BIGINT NOT NULL DEFAULT 1,
                remain_quota BIGINT NOT NULL DEFAULT 0,
                deleted_at TIMESTAMPTZ
            );
            CREATE TABLE channels (id BIGINT PRIMARY KEY, name TEXT NOT NULL);
            CREATE TABLE logs (
                id BIGINT PRIMARY KEY,
                user_id BIGINT NOT NULL,
                token_id BIGINT NOT NULL,
                created_at BIGINT NOT NULL,
                type BIGINT NOT NULL DEFAULT 2,
                content TEXT NOT NULL,
                username TEXT NOT NULL DEFAULT 'reader',
                token_name TEXT NOT NULL DEFAULT 'fixture-token',
                model_name TEXT NOT NULL DEFAULT 'fixture-model',
                quota BIGINT NOT NULL DEFAULT 1,
                prompt_tokens BIGINT NOT NULL DEFAULT 2,
                completion_tokens BIGINT NOT NULL DEFAULT 3,
                use_time BIGINT NOT NULL DEFAULT 4,
                is_stream BOOLEAN NOT NULL DEFAULT true,
                channel_id BIGINT NOT NULL DEFAULT 9,
                "group" TEXT NOT NULL DEFAULT 'default',
                ip TEXT NOT NULL DEFAULT '192.0.2.1',
                request_id TEXT NOT NULL DEFAULT 'fixture-request',
                upstream_request_id TEXT NOT NULL DEFAULT 'fixture-upstream',
                other TEXT NOT NULL DEFAULT '{"admin_info":{"operator":"root"},"audit_info":{"route":"/api/log/token"},"stream_status":"done","safe":"visible"}'
            );
            INSERT INTO users VALUES (7, 1, NULL), (8, 1, NULL), (9, 2, NULL);
            INSERT INTO tokens (id, user_id, key, status) VALUES
                (31, 7, 'tokena', 1), (32, 7, 'tokenb', 1),
                (41, 8, 'foreign', 1), (51, 7, 'empty', 1),
                (61, 7, 'disabled', 2), (71, 9, 'disabledowner', 1);
            INSERT INTO channels VALUES (9, 'private-channel');
            -- Newer ids deliberately have older timestamps. Returned ids are
            -- display ids, so content is the independent storage-order oracle.
            INSERT INTO logs (id, user_id, token_id, created_at, content, type)
                SELECT 10000 + n, 7, 31, 2000000 - n, 'token-a-' || n,
                       CASE WHEN n % 2 = 0 THEN 2 ELSE 4 END
                FROM generate_series(1, 1025) AS n;
            INSERT INTO logs (id, user_id, token_id, created_at, content)
                SELECT 20000 + n, 7, 32, 3000000 - n, 'token-b-' || n
                FROM generate_series(1, 17) AS n;
            INSERT INTO logs (id, user_id, token_id, created_at, content)
                SELECT 30000 + n, 8, 41, 4000000 - n, 'foreign-' || n
                FROM generate_series(1, 7) AS n;
            "#,
        )
        .execute(&pg)
        .await
        {
            Self::fail_setup(admin, Some(pg), &schema, "seed log contract", error).await;
        }
        Self { admin, pg, schema }
    }

    fn app(&self) -> Router {
        observability_read_router(ObservabilityState::new(
            Arc::new(PgObservabilityStore::postgres_read_only(
                self.pg.clone(),
                Arc::new(UnavailableObservabilityMetrics),
            )),
            Arc::new(FixtureAuthorizer {
                token: PgReadOnlyObservabilityTokenAuthorizer::new(self.pg.clone()),
            }),
        ))
    }

    async fn cleanup_parts(
        admin: PgPool,
        pg: Option<PgPool>,
        schema: &str,
    ) -> Result<(), sqlx::Error> {
        if let Some(pg) = pg {
            pg.close().await;
        }
        let result = sqlx::query(&format!("DROP SCHEMA IF EXISTS {schema} CASCADE"))
            .execute(&admin)
            .await;
        // Close the admin pool even when dropping the schema fails.
        admin.close().await;
        result.map(|_| ())
    }

    async fn fail_setup(
        admin: PgPool,
        pg: Option<PgPool>,
        schema: &str,
        stage: &str,
        error: sqlx::Error,
    ) -> ! {
        if let Err(cleanup_error) = Self::cleanup_parts(admin, pg, schema).await {
            eprintln!("failed to clean up {schema} after {stage}: {cleanup_error}");
        }
        panic!("{stage}: {error}");
    }

    async fn finish(self, outcome: std::thread::Result<()>) {
        let Self { admin, pg, schema } = self;
        let cleanup = Self::cleanup_parts(admin, Some(pg), &schema).await;
        match outcome {
            Ok(()) => cleanup.expect("drop temporary schema"),
            Err(payload) => {
                if let Err(error) = cleanup {
                    eprintln!("failed to clean up {schema} after test panic: {error}");
                }
                // Keep the assertion's original payload if cleanup also fails.
                std::panic::resume_unwind(payload);
            }
        }
    }
}

async fn response(app: &Router, uri: &str, credential: &str) -> (StatusCode, Value) {
    let result = app
        .clone()
        .oneshot(
            Request::get(uri)
                .header("authorization", format!("Bearer {credential}"))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");
    let status = result.status();
    let bytes = to_bytes(result.into_body(), usize::MAX)
        .await
        .expect("response body");
    (
        status,
        serde_json::from_slice(&bytes).expect("JSON response"),
    )
}

fn array_items(envelope: &Value) -> &[Value] {
    assert_eq!(envelope["success"], true);
    assert_eq!(envelope["message"], "");
    envelope["data"].as_array().expect("legacy log array")
}

fn page_items(envelope: &Value, page: i64, page_size: i64, total: i64) -> &[Value] {
    assert_eq!(envelope["success"], true);
    assert_eq!(envelope["message"], "");
    let data = envelope["data"].as_object().expect("page envelope");
    assert_eq!(data.len(), 4, "only the frozen page fields");
    assert_eq!(data["page"], json!(page));
    assert_eq!(data["page_size"], json!(page_size));
    assert_eq!(data["total"], json!(total));
    data["items"].as_array().expect("page item array")
}

fn assert_token_a_window(items: &[Value], newest: i64, offset: i64) {
    for (index, item) in items.iter().enumerate() {
        let stored_sequence = newest - index as i64;
        assert_eq!(item["id"], json!(offset + index as i64 + 1));
        assert_eq!(item["content"], format!("token-a-{stored_sequence}"));
        assert_eq!(item["created_at"], json!(2000000 - stored_sequence));
        assert_eq!(item["token_id"], 31);
        assert_eq!(item["user_id"], 7);
    }
}

fn assert_user_redaction(items: &[Value]) {
    for item in items {
        assert_eq!(item["channel"], 9);
        assert_eq!(item["channel_name"], "");
        let other: Value = serde_json::from_str(item["other"].as_str().expect("other string"))
            .expect("formatted other JSON");
        assert_eq!(other, json!({"safe": "visible"}));
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_logs_postgres_preserve_legacy_and_page_contracts() {
    let fixture = Fixture::new().await;
    let outcome = AssertUnwindSafe(async {
        let app = fixture.app();

    for uri in [
        "/api/log/token",
        "/api/log/token?page=2",
        "/api/log/token?token_id=41&user_id=8&type=999&channel=999&username=missing&token_name=missing&model_name=missing&group=missing&request_id=missing&upstream_request_id=missing&start_timestamp=1&end_timestamp=1",
    ] {
        let (status, envelope) = response(&app, uri, "sk-tokena").await;
        assert_eq!(status, StatusCode::OK, "{uri}");
        let items = array_items(&envelope);
        assert_eq!(items.len(), 1000, "{uri}");
        assert_token_a_window(items, TOKEN_A_TOTAL, 0);
        assert_user_redaction(items);
        assert!(items[0]["created_at"].as_i64() < items[1]["created_at"].as_i64());
    }

    for (uri, page, expected_len, newest, offset) in [
        ("/api/log/token?p=1", 1, 10, 1025, 0),
        ("/api/log/token?p=2&size=10", 2, 10, 1015, 10),
        ("/api/log/token?p=103&ps=10", 103, 5, 5, 1020),
        ("/api/log/token?p=104&page_size=10", 104, 0, 0, 1030),
    ] {
        let (status, envelope) = response(&app, uri, "sk-tokena").await;
        assert_eq!(status, StatusCode::OK, "{uri}");
        let items = page_items(&envelope, page, 10, TOKEN_A_TOTAL);
        assert_eq!(items.len(), expected_len, "{uri}");
        assert_token_a_window(items, newest, offset);
        assert_user_redaction(items);
    }

    // An i64 page beyond 32-bit range is valid when the page end fits in i64.
    for (uri, page, size) in [
        ("/api/log/token?p=2147483648&size=3", 2147483648, 3),
        ("/api/log/token?p=9223372036854775807&size=1", i64::MAX, 1),
    ] {
        let (status, envelope) = response(&app, uri, "sk-tokena").await;
        assert_eq!(status, StatusCode::OK, "{uri}");
        assert!(page_items(&envelope, page, size, TOKEN_A_TOTAL).is_empty());
    }
    })
    .catch_unwind()
    .await;
    fixture.finish(outcome).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_logs_postgres_normalize_explicit_keys_aliases_and_page_bounds() {
    let fixture = Fixture::new().await;
    let outcome = AssertUnwindSafe(async {
        let app = fixture.app();
        for (uri, page, size) in [
            ("/api/log/token?p", 1, 10),
            ("/api/log/token?p=", 1, 10),
            ("/api/log/token?page_size=", 1, 10),
            ("/api/log/token?ps=", 1, 10),
            ("/api/log/token?size=", 1, 10),
            ("/api/log/token?p=invalid&page_size=invalid", 1, 10),
            ("/api/log/token?p=0&page_size=0&ps=0&size=0", 1, 10),
            ("/api/log/token?p=-3&page_size=-1&ps=-2&size=-3", 1, 10),
            ("/api/log/token?p=%202%20&size=%205%20", 1, 10),
            (
                "/api/log/token?p=9223372036854775808&size=9223372036854775808",
                1,
                10,
            ),
            (
                "/api/log/token?p=-9223372036854775809&size=-9223372036854775809",
                1,
                10,
            ),
            ("/api/log/token?page_size=9223372036854775808", 1, 10),
            ("/api/log/token?ps=9223372036854775808", 1, 10),
            ("/api/log/token?size=9223372036854775808", 1, 10),
            ("/api/log/token?p=2&page_size=bad&ps=-2&size=7", 2, 7),
            ("/api/log/token?p=2&page_size=0&ps=5&size=7", 2, 5),
            ("/api/log/token?p=2&page_size=-1&ps=bad&size=7", 2, 7),
            ("/api/log/token?page_size=4&ps=5&size=6", 1, 4),
            ("/api/log/token?page_size=9223372036854775808&ps=8", 1, 8),
            (
                "/api/log/token?page_size=bad&ps=9223372036854775808&size=9",
                1,
                9,
            ),
            ("/api/log/token?page_size=2000&ps=3&size=4", 1, 1000),
            ("/api/log/token?ps=2000", 1, 1000),
            ("/api/log/token?size=2000", 1, 1000),
            ("/api/log/token?p=2&p=1&size=3&size=9", 2, 3),
        ] {
            let (status, envelope) = response(&app, uri, "sk-tokena").await;
            assert_eq!(status, StatusCode::OK, "{uri}");
            let items = page_items(&envelope, page, size, TOKEN_A_TOTAL);
            let offset = (page - 1) * size;
            assert_eq!(
                items.len() as i64,
                size.min((TOKEN_A_TOTAL - offset).max(0)),
                "{uri}"
            );
            assert_token_a_window(items, TOKEN_A_TOTAL - offset, offset);
        }

        for uri in [
            "/api/log/token?p=9223372036854775807&size=2",
            // The offset fits here; only the end/display-id bound overflows.
            "/api/log/token?p=4611686018427387904&size=2",
            "/api/log/token?p=9223372036854776&page_size=2000",
        ] {
            let (status, envelope) = response(&app, uri, "sk-tokena").await;
            assert_eq!(status, StatusCode::OK, "{uri}");
            assert_eq!(
                envelope,
                json!({"success": false, "message": "分页参数超出范围"})
            );
        }
    })
    .catch_unwind()
    .await;
    fixture.finish(outcome).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_logs_postgres_isolate_tokens_and_preserve_other_log_redaction() {
    let fixture = Fixture::new().await;
    let outcome = AssertUnwindSafe(async {
        let app = fixture.app();
    for (credential, total, newest) in [
        ("sk-tokenb", 17, "token-b-17"),
        ("sk-foreign", 7, "foreign-7"),
    ] {
        let (status, envelope) = response(
            &app,
            "/api/log/token?p=1&size=1000&token_id=31&user_id=7&start_timestamp=1&end_timestamp=1&type=999",
            credential,
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        let items = page_items(&envelope, 1, 1000, total);
        assert_eq!(items.len() as i64, total);
        assert_eq!(items[0]["content"], newest);
        assert_user_redaction(items);
    }
    let (status, envelope) = response(
        &app,
        "/api/log/token?p=1&size=3&token_id=41&user_id=8&request_id=missing",
        "sk-tokena",
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let items = page_items(&envelope, 1, 3, TOKEN_A_TOTAL);
    assert_eq!(items.len(), 3);
    assert_token_a_window(items, TOKEN_A_TOTAL, 0);

    for uri in ["/api/log/token", "/api/log/token?p=2&size=3"] {
        let (status, envelope) = response(&app, uri, "sk-empty").await;
        assert_eq!(status, StatusCode::OK);
        if uri.contains('?') {
            assert!(page_items(&envelope, 2, 3, 0).is_empty());
        } else {
            assert!(array_items(&envelope).is_empty());
        }
    }
    for credential in ["sk-disabled", "sk-disabledowner", "sk-missing"] {
        let (status, envelope) = response(&app, "/api/log/token?p=1", credential).await;
        assert_eq!(status, StatusCode::UNAUTHORIZED);
        assert_eq!(envelope["success"], false);
        assert!(envelope.get("data").is_none());
    }

    let (status, envelope) = response(&app, "/api/log/self?size=1000", "fixture_dashboard").await;
    assert_eq!(status, StatusCode::OK);
    let items = page_items(&envelope, 1, 100, TOKEN_A_TOTAL + 17);
    assert_eq!(items.len(), 100);
    assert_eq!(items[0]["content"], "token-b-17");
    assert_user_redaction(items);

    let (status, envelope) = response(&app, "/api/log/?size=1000", "fixture_dashboard").await;
    assert_eq!(status, StatusCode::OK);
    let items = page_items(&envelope, 1, 100, TOKEN_A_TOTAL + 17 + 7);
    assert_eq!(items.len(), 100);
    assert_eq!(items[0]["id"], 30001, "admin storage ids stay intact");
    assert_eq!(
        items[0]["content"], "foreign-1",
        "admin timestamp order stays intact"
    );
    assert_eq!(items[0]["channel_name"], "private-channel");
    let other: Value = serde_json::from_str(items[0]["other"].as_str().expect("other string"))
        .expect("admin other JSON");
    assert_eq!(other["admin_info"]["operator"], "root");
    assert_eq!(other["audit_info"]["route"], "/api/log/token");
    assert_eq!(other["stream_status"], "done");

    sqlx::query("UPDATE logs SET other = 'not-json' WHERE id = 11025")
        .execute(&fixture.pg)
        .await
        .expect("malformed metadata fixture");
    let (status, envelope) = response(&app, "/api/log/token?size=1", "sk-tokena").await;
    assert_eq!(status, StatusCode::OK);
    let items = page_items(&envelope, 1, 1, TOKEN_A_TOTAL);
    assert_eq!(
        items[0]["other"], "null",
        "malformed metadata keeps legacy formatting"
    );
    })
    .catch_unwind()
    .await;
    fixture.finish(outcome).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn token_logs_postgres_storage_failures_never_return_successful_pages() {
    let fixture = Fixture::new().await;
    let outcome = AssertUnwindSafe(async {
        let app = fixture.app();
        // COUNT still works, but the actual row projection fails.
        sqlx::query("ALTER TABLE logs RENAME COLUMN content TO unavailable_content")
            .execute(&fixture.pg)
            .await
            .expect("row projection failure fixture");
        for uri in ["/api/log/token", "/api/log/token?p=1&size=10"] {
            let (status, envelope) = response(&app, uri, "sk-tokena").await;
            assert_eq!(status, StatusCode::INTERNAL_SERVER_ERROR, "{uri}");
            assert_eq!(
                envelope,
                json!({"success": false, "message": "observability backend unavailable"})
            );
        }
        sqlx::query("DROP TABLE logs")
            .execute(&fixture.pg)
            .await
            .expect("count failure fixture");
        let (status, envelope) = response(&app, "/api/log/token?p=1&size=10", "sk-tokena").await;
        assert_eq!(status, StatusCode::INTERNAL_SERVER_ERROR);
        assert_eq!(
            envelope,
            json!({"success": false, "message": "observability backend unavailable"})
        );

        // Overflow is a request error even when the PostgreSQL log table is absent.
        let (status, envelope) = response(
            &app,
            "/api/log/token?p=4611686018427387904&size=2",
            "sk-tokena",
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(
            envelope,
            json!({"success": false, "message": "分页参数超出范围"})
        );
    })
    .catch_unwind()
    .await;
    fixture.finish(outcome).await;
}
