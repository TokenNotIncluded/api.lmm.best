use std::{env, sync::Arc, time::Duration};

use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use hmac::{Hmac, Mac};
use lmm_api_rs::routes::api_token::{
    ApiTokenHttpState, ApiTokenPrincipal, PgValkeyApiTokenService, api_token_router,
};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use tower::ServiceExt;

#[tokio::test]
async fn frozen_api_token_routes_accept_an_authenticated_non_admin_user() {
    let router = router();
    let response = router
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/token/")
                .extension(ApiTokenPrincipal {
                    user_id: 7,
                    role: 1,
                    preferred_language: None,
                })
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    // The lazy pool is intentionally unreachable: a 200 legacy failure means
    // the request passed UserAuth instead of being incorrectly admin-gated.
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(body(response).await["success"], false);
}

#[tokio::test]
async fn api_token_routes_reject_a_missing_authenticated_principal() {
    let response = router()
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/api/token/")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("router response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
}

#[tokio::test]
async fn api_token_routes_reject_guest_principals_before_database_access() {
    let response = call(
        &router(),
        "GET",
        "/api/token/",
        None,
        ApiTokenPrincipal {
            user_id: 7,
            role: 0,
            preferred_language: Some("zh"),
        },
    )
    .await;
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        body(response).await,
        json!({
            "success": false,
            "code": "AUTH_INSUFFICIENT_PRIVILEGE",
            "message": "无权进行此操作，权限不足"
        })
    );
}

#[tokio::test]
async fn api_token_routes_reject_invalid_roles_before_database_access() {
    let response = call(
        &router(),
        "GET",
        "/api/token/",
        None,
        ApiTokenPrincipal {
            user_id: 7,
            role: 2,
            preferred_language: Some("en"),
        },
    )
    .await;
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(
        body(response).await,
        json!({
            "success": false,
            "code": "AUTH_USER_INVALID",
            "message": "Unauthorized, invalid user info"
        })
    );
}

#[tokio::test]
async fn top_level_token_shapes_keep_the_frozen_go_error_for_create_and_update() {
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    for (label, payload, expected) in [
        (
            "array",
            br#"[]"#.as_slice(),
            "json: cannot unmarshal array into Go value of type model.Token",
        ),
        (
            "bool",
            br#"true"#.as_slice(),
            "json: cannot unmarshal bool into Go value of type model.Token",
        ),
        (
            "string",
            br#""token""#.as_slice(),
            "json: cannot unmarshal string into Go value of type model.Token",
        ),
        (
            "number",
            br#"1"#.as_slice(),
            "json: cannot unmarshal number into Go value of type model.Token",
        ),
    ] {
        for method in ["POST", "PUT"] {
            let path = "/api/token/";
            let value =
                body(call_raw(&frozen_router(), method, path, payload, principal).await).await;
            assert_eq!(
                value,
                json!({"success": false, "message": expected}),
                "{method} top-level {label}"
            );
        }
    }
}

#[tokio::test]
async fn batch_key_database_fault_returns_a_failure_envelope_instead_of_a_partial_map() {
    let response = call(
        &router(),
        "POST",
        "/api/token/batch/keys",
        Some(json!({"ids":[1]})),
        ApiTokenPrincipal {
            user_id: 7,
            role: 1,
            preferred_language: None,
        },
    )
    .await;
    let headers = response.headers().clone();
    assert_eq!(
        headers["cache-control"],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    let value = body(response).await;
    assert_eq!(value["success"], false);
    assert!(value.get("data").is_none());
}

#[tokio::test]
async fn malformed_token_id_uses_the_legacy_success_status_error_envelope() {
    let value = body(
        call(
            &router(),
            "GET",
            "/api/token/not-an-integer",
            None,
            ApiTokenPrincipal {
                user_id: 7,
                role: 1,
                preferred_language: None,
            },
        )
        .await,
    )
    .await;
    assert_eq!(value["success"], false);
    assert_eq!(
        value["message"],
        "strconv.Atoi: parsing \"not-an-integer\": invalid syntax"
    );
}

#[tokio::test]
async fn huge_path_ids_keep_strconv_atoi_overflow_errors_on_all_token_paths() {
    for id in ["9223372036854775808", "-9223372036854775809"] {
        for (method, suffix) in [("GET", ""), ("POST", "/key"), ("DELETE", "")] {
            let value = body(
                call(
                    &router(),
                    method,
                    &format!("/api/token/{id}{suffix}"),
                    None,
                    ApiTokenPrincipal {
                        user_id: 7,
                        role: 1,
                        preferred_language: None,
                    },
                )
                .await,
            )
            .await;
            assert_eq!(value["success"], false);
            if method == "DELETE" {
                assert!(
                    !value["message"]
                        .as_str()
                        .unwrap_or_default()
                        .starts_with("strconv.Atoi:"),
                    "DELETE must saturate the path ID before it reaches the database"
                );
                continue;
            }
            let expected = format!("strconv.Atoi: parsing \"{id}\": value out of range");
            assert_eq!(value["message"], expected);
        }
    }
}

#[tokio::test]
async fn deleted_at_binding_matches_gorm_null_timestamp_and_type_errors() {
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    let invalid = body(
        call_raw(
            &router(),
            "POST",
            "/api/token/",
            br#"{"name":"invalid","DeletedAt":"not-a-timestamp"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(invalid["success"], false);
    assert_eq!(
        invalid["message"],
        "parsing time \"not-a-timestamp\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"not-a-timestamp\" as \"2006\""
    );

    let wrong_type = body(
        call_raw(
            &router(),
            "POST",
            "/api/token/",
            br#"{"name":"wrong-type","DeletedAt":123}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(wrong_type["success"], false);
    assert_eq!(
        wrong_type["message"],
        "Time.UnmarshalJSON: input is not a JSON string"
    );

    let valid = body(
        call_raw(
            &router(),
            "POST",
            "/api/token/",
            br#"{"name":"valid","DeletedAt":"2026-08-01T12:34:56Z"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(valid["success"], false);
    assert_ne!(
        valid["message"],
        "parsing time \"2026-08-01T12:34:56Z\" as \"2006-01-02T15:04:05Z07:00\": cannot parse \"2026-08-01T12:34:56Z\" as \"2006\""
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn mixed_case_deleted_at_create_update_preserves_active_rows_and_rejects_invalid_inputs() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .unwrap();
    reset(&pool).await;
    let router = router_for(pool.clone(), &valkey_url);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };

    let created_null = body(
        call_raw(
            &router,
            "POST",
            "/api/token/",
            br#"{"name":"mixed-null-create","dElEtEdAt":null}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(created_null["success"], true);
    assert_active_token_row(&pool, 1, "mixed-null-create", 1, -1).await;

    let created_valid = body(
        call_raw(
            &router,
            "POST",
            "/api/token/",
            br#"{"name":"mixed-valid-create","dElEtEdAt":"2026-08-01T12:34:56Z"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(created_valid["success"], true);
    assert_active_token_row(&pool, 2, "mixed-valid-create", 1, -1).await;

    let before_invalid_create: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM tokens WHERE deleted_at IS NULL")
            .fetch_one(&pool)
            .await
            .unwrap();
    let invalid_create = body(
        call_raw(
            &router,
            "POST",
            "/api/token/",
            br#"{"name":"mixed-invalid-create","dElEtEdAt":"not-a-timestamp"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(invalid_create["success"], false);
    assert!(
        invalid_create["message"]
            .as_str()
            .unwrap_or_default()
            .starts_with("parsing time")
    );
    let after_invalid_create: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM tokens WHERE deleted_at IS NULL")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(after_invalid_create, before_invalid_create);
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM tokens WHERE name = 'mixed-invalid-create'",
        )
        .fetch_one(&pool)
        .await
        .unwrap(),
        0
    );

    let updated_null = body(
        call_raw(
            &router,
            "PUT",
            "/api/token/",
            br#"{"id":1,"name":"mixed-null-update","dElEtEdAt":null}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(updated_null["success"], true);
    assert_active_token_row(&pool, 1, "mixed-null-update", 1, 0).await;

    let updated_valid = body(
        call_raw(
            &router,
            "PUT",
            "/api/token/",
            br#"{"id":1,"name":"mixed-valid-update","dElEtEdAt":"2026-08-01T12:34:56+00:00"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(updated_valid["success"], true);
    assert_active_token_row(&pool, 1, "mixed-valid-update", 1, 0).await;

    let before_invalid_update = token_row(&pool, 1).await;
    let invalid_update = body(
        call_raw(
            &router,
            "PUT",
            "/api/token/",
            br#"{"id":1,"name":"mixed-invalid-update","dElEtEdAt":"not-a-timestamp"}"#,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(invalid_update["success"], false);
    assert!(
        invalid_update["message"]
            .as_str()
            .unwrap_or_default()
            .starts_with("parsing time")
    );
    assert_eq!(token_row(&pool, 1).await, before_invalid_update);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn create_missing_and_explicit_zero_fields_use_go_model_defaults() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    let router = router_for(pool.clone(), &valkey_url);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };

    let created = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"create-default"})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(created["success"], true);
    assert_active_token_row(&pool, 1, "create-default", 1, -1).await;

    let explicit_zero = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"create-explicit-zero","status":0,"expired_time":0})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(explicit_zero["success"], true);
    assert_active_token_row(&pool, 2, "create-explicit-zero", 1, -1).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn create_token_activation_is_one_time_and_transactional() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    let router = router_for(pool.clone(), &valkey_url);
    let user_seven = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };

    assert_eq!(console_activated_at(&pool, 7).await, 0);
    let first = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"first-activation"})),
            user_seven,
        )
        .await,
    )
    .await;
    assert_eq!(first["success"], true);
    assert!(console_activated_at(&pool, 7).await > 0);

    sqlx::query("UPDATE users SET console_activated_at = 123 WHERE id = 7")
        .execute(&pool)
        .await
        .expect("set preserved activation timestamp");
    let subsequent = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"subsequent-token"})),
            user_seven,
        )
        .await,
    )
    .await;
    assert_eq!(subsequent["success"], true);
    assert_eq!(console_activated_at(&pool, 7).await, 123);

    sqlx::query(
        "ALTER TABLE tokens ADD CONSTRAINT reject_activation_test CHECK (name <> 'reject-activation')",
    )
    .execute(&pool)
    .await
    .expect("install rejected token fixture");
    let rejected = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"reject-activation"})),
            ApiTokenPrincipal {
                user_id: 8,
                role: 1,
                preferred_language: None,
            },
        )
        .await,
    )
    .await;
    assert_eq!(rejected["success"], false);
    assert_eq!(console_activated_at(&pool, 8).await, 0);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT COUNT(*) FROM tokens WHERE user_id = 8")
            .fetch_one(&pool)
            .await
            .expect("rejected token count"),
        0
    );
}

#[tokio::test]
async fn api_token_router_rejects_oversized_body_before_legacy_decode() {
    let padding = "x".repeat(2 * 1024 * 1024 + 1);
    let payload = format!(r#"{{"name":"oversized","padding":"{padding}"}}"#);
    let response = call_raw(
        &router(),
        "POST",
        "/api/token/",
        payload.as_bytes(),
        ApiTokenPrincipal {
            user_id: 7,
            role: 1,
            preferred_language: None,
        },
    )
    .await;
    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
}

#[tokio::test]
async fn api_token_body_parsing_does_not_require_content_type_or_json_media_type() {
    for content_type in [None, Some("text/plain")] {
        let mut builder = Request::builder()
            .method("POST")
            .uri("/api/token/")
            .extension(ApiTokenPrincipal {
                user_id: 7,
                role: 1,
                preferred_language: None,
            });
        if let Some(content_type) = content_type {
            builder = builder.header("content-type", content_type);
        }
        let response = router()
            .oneshot(
                builder
                    .body(Body::from(r#"{"name":"content-type-agnostic"}"#))
                    .expect("request"),
            )
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{content_type:?}");
        assert_eq!(body(response).await["success"], false);
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_mutations_invalidate_cached_credentials_and_keep_listings_masked() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .unwrap();
    reset(&pool).await;
    let router = router_for(pool.clone(), &valkey_url);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 10,
        preferred_language: None,
    };

    let create = call(
        &router,
        "POST",
        "/api/token/",
        Some(json!({"name":"oracle","remain_quota":42})),
        principal,
    )
    .await;
    assert_eq!(create.status(), StatusCode::OK);
    assert_eq!(body(create).await["success"], true);
    let listed = body(
        call(
            &router,
            "GET",
            "/api/token/?p=1&page_size=10",
            None,
            principal,
        )
        .await,
    )
    .await;
    let id = listed["data"]["items"][0]["id"].as_i64().unwrap();
    assert!(
        listed["data"]["items"][0]["key"]
            .as_str()
            .unwrap()
            .contains('*')
    );
    assert!(listed["data"]["items"][0]["DeletedAt"].is_null());
    let revealed = body(
        call(
            &router,
            "POST",
            &format!("/api/token/{id}/key"),
            None,
            principal,
        )
        .await,
    )
    .await;
    let key = revealed["data"]["key"].as_str().unwrap().to_owned();
    assert!(!key.contains('*'));

    let client = redis::Client::open(valkey_url).unwrap();
    let mut cache = client.get_multiplexed_async_connection().await.unwrap();
    let cache_key = token_cache_key(&key);
    redis::cmd("HSET")
        .arg(&cache_key)
        .arg("Status")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    redis::cmd("EXPIRE")
        .arg(&cache_key)
        .arg(600)
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    let updated = call(
        &router,
        "PUT",
        "/api/token/?status_only=1",
        Some(json!({"id":id,"status":2,"name":"ignored"})),
        principal,
    )
    .await;
    let updated = body(updated).await;
    assert_eq!(updated["success"], true);
    assert_eq!(updated["data"]["allow_ips"], "");
    assert!(updated["data"]["DeletedAt"].is_null());
    let status: Option<String> = redis::cmd("HGET")
        .arg(&cache_key)
        .arg("Status")
        .query_async(&mut cache)
        .await
        .unwrap();
    assert_eq!(status.as_deref(), Some("2"));
    let cached_owner: Option<String> = redis::cmd("HGET")
        .arg(&cache_key)
        .arg("UserId")
        .query_async(&mut cache)
        .await
        .unwrap();
    let cached_secret: Option<String> = redis::cmd("HGET")
        .arg(&cache_key)
        .arg("Key")
        .query_async(&mut cache)
        .await
        .unwrap();
    assert_eq!(cached_owner.as_deref(), Some("7"));
    assert_eq!(cached_secret.as_deref(), Some(""));
    let ttl: i64 = redis::cmd("TTL")
        .arg(&cache_key)
        .query_async(&mut cache)
        .await
        .unwrap();
    assert!((1..=60).contains(&ttl));

    let full_update = body(
        call(
            &router,
            "PUT",
            "/api/token/",
            Some(json!({
                "id":id,
                "name":"oracle-full-update",
                "status":1,
                "expired_time":-1,
                "remain_quota":42,
                "unlimited_quota":false,
                "model_limits_enabled":false,
                "model_limits":"",
                "group":"default",
                "cross_group_retry":false
            })),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(full_update["success"], true);
    assert!(full_update["data"]["allow_ips"].is_null());
    assert!(full_update["data"]["DeletedAt"].is_null());

    let deleted = body(
        call(
            &router,
            "POST",
            "/api/token/batch",
            Some(json!({"ids":[id,id]})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(deleted["data"], 1);
    let replayed_batch_delete = body(
        call(
            &router,
            "POST",
            "/api/token/batch",
            Some(json!({"ids":[id]})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(replayed_batch_delete["data"], 0);
    let exists: i64 = redis::cmd("EXISTS")
        .arg(&cache_key)
        .query_async(&mut cache)
        .await
        .unwrap();
    assert_eq!(exists, 0);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_status_only_writes_the_legacy_update_column_set() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("DROP TABLE IF EXISTS token_status_only_columns")
        .execute(&pool)
        .await
        .expect("remove prior isolated trigger audit table");
    sqlx::query("DROP FUNCTION IF EXISTS observe_token_status_only_column()")
        .execute(&pool)
        .await
        .expect("remove prior isolated trigger function");
    sqlx::query("CREATE TABLE token_status_only_columns (column_name TEXT PRIMARY KEY)")
        .execute(&pool)
        .await
        .expect("create trigger audit table");
    sqlx::query("CREATE FUNCTION observe_token_status_only_column() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN INSERT INTO token_status_only_columns (column_name) VALUES (TG_ARGV[0]); RETURN NEW; END $$")
        .execute(&pool)
        .await
        .expect("create update-column observer");
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,7,'status-only-key',1,'preserved',1,1,123,17,TRUE,TRUE,'gpt-*',NULL,0,'ops',TRUE)")
        .execute(&pool)
        .await
        .expect("seed status-only token");
    for column in [
        "name",
        "status",
        "expired_time",
        "remain_quota",
        "unlimited_quota",
        "model_limits_enabled",
        "model_limits",
        "allow_ips",
        "group",
        "cross_group_retry",
    ] {
        sqlx::query(&format!(
            "CREATE TRIGGER observe_status_only_{column} AFTER UPDATE OF \"{column}\" ON tokens FOR EACH ROW EXECUTE FUNCTION observe_token_status_only_column('{column}')"
        ))
        .execute(&pool)
        .await
        .expect("install update-column observer");
    }

    let value = body(
        call(
            &router_for(pool.clone(), &valkey_url),
            "PUT",
            "/api/token/?status_only=1",
            Some(json!({
                "id": 1,
                "status": 2,
                "name": "ignored",
                "expired_time": 0,
                "remain_quota": 0,
                "unlimited_quota": false,
                "model_limits_enabled": false,
                "model_limits": "",
                "allow_ips": "ignored",
                "group": "ignored",
                "cross_group_retry": false
            })),
            ApiTokenPrincipal {
                user_id: 7,
                role: 1,
                preferred_language: None,
            },
        )
        .await,
    )
    .await;
    assert_eq!(value["success"], true);
    assert_eq!(value["data"]["status"], 2);
    assert_eq!(value["data"]["name"], "preserved");
    assert_eq!(value["data"]["expired_time"], 123);
    assert_eq!(value["data"]["remain_quota"], 17);
    assert_eq!(value["data"]["unlimited_quota"], true);
    assert_eq!(value["data"]["model_limits_enabled"], true);
    assert_eq!(value["data"]["model_limits"], "gpt-*");
    assert!(value["data"]["allow_ips"].is_null());
    assert_eq!(value["data"]["group"], "ops");
    assert_eq!(value["data"]["cross_group_retry"], true);

    let updated_columns: String = sqlx::query_scalar(
        "SELECT string_agg(column_name, ',' ORDER BY column_name) FROM token_status_only_columns",
    )
    .fetch_one(&pool)
    .await
    .expect("read update-column audit");
    assert_eq!(
        updated_columns,
        "allow_ips,cross_group_retry,expired_time,group,model_limits,model_limits_enabled,name,remain_quota,status,unlimited_quota"
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_delete_is_idempotent_under_replay_and_competing_requests() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .connect(&database_url)
        .await
        .unwrap();
    reset(&pool).await;
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,7,'replaykey',1,'replay',1,1,-1,1,FALSE,FALSE,'','',0,'',FALSE)").execute(&pool).await.unwrap();
    let router = router_for(pool.clone(), &valkey_url);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 10,
        preferred_language: None,
    };
    let (one, two) = tokio::join!(
        call(&router, "DELETE", "/api/token/1", None, principal),
        call(&router, "DELETE", "/api/token/1", None, principal)
    );
    let responses = [body(one).await, body(two).await];
    let successes = responses
        .iter()
        .filter(|response| response["success"] == true)
        .count();
    let failures = responses
        .iter()
        .filter(|response| response["message"] == "record not found")
        .count();
    assert_eq!(
        (successes, failures),
        (1, 1),
        "one concurrent delete owns the soft-delete; its replay observes Go's record-not-found envelope"
    );
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_batch_delete_is_owner_scoped_and_preserves_foreign_cache_on_replay() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,7,'owner-batch-key',1,'owner',1,1,-1,1,FALSE,FALSE,'','',0,'',FALSE),(2,8,'foreign-batch-key',1,'foreign',1,1,-1,1,FALSE,FALSE,'','',0,'',FALSE)")
        .execute(&pool)
        .await
        .expect("seed owner and foreign tokens");
    let client = redis::Client::open(valkey_url.as_str()).expect("Valkey URL");
    let mut cache = client
        .get_multiplexed_async_connection()
        .await
        .expect("isolated Valkey");
    let owner_cache_key = token_cache_key("owner-batch-key");
    let foreign_cache_key = token_cache_key("foreign-batch-key");
    for cache_key in [&owner_cache_key, &foreign_cache_key] {
        redis::cmd("SET")
            .arg(cache_key)
            .arg("present")
            .query_async::<()>(&mut cache)
            .await
            .expect("seed token cache entry");
    }
    let router = router_for(pool.clone(), &valkey_url);
    let owner = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };

    let keys = body(
        call(
            &router,
            "POST",
            "/api/token/batch/keys",
            Some(json!({"ids":[1,2]})),
            owner,
        )
        .await,
    )
    .await;
    assert_eq!(
        keys,
        json!({"success":true,"message":"","data":{"keys":{"1":"owner-batch-key"}}})
    );

    let deleted = body(
        call(
            &router,
            "POST",
            "/api/token/batch",
            Some(json!({"ids":[1,2]})),
            owner,
        )
        .await,
    )
    .await;
    assert_eq!(deleted, json!({"success":true,"message":"","data":1}));
    assert!(
        sqlx::query_scalar::<_, bool>("SELECT deleted_at IS NOT NULL FROM tokens WHERE id = 1")
            .fetch_one(&pool)
            .await
            .expect("read owner deletion")
    );
    assert!(
        !sqlx::query_scalar::<_, bool>("SELECT deleted_at IS NOT NULL FROM tokens WHERE id = 2")
            .fetch_one(&pool)
            .await
            .expect("read foreign deletion")
    );
    let owner_cache_exists: i64 = redis::cmd("EXISTS")
        .arg(&owner_cache_key)
        .query_async(&mut cache)
        .await
        .expect("read owner cache");
    let foreign_cache_exists: i64 = redis::cmd("EXISTS")
        .arg(&foreign_cache_key)
        .query_async(&mut cache)
        .await
        .expect("read foreign cache");
    assert_eq!((owner_cache_exists, foreign_cache_exists), (0, 1));

    let replayed = body(
        call(
            &router,
            "POST",
            "/api/token/batch",
            Some(json!({"ids":[1,2]})),
            owner,
        )
        .await,
    )
    .await;
    assert_eq!(replayed, json!({"success":true,"message":"","data":0}));
    let foreign_cache_exists: i64 = redis::cmd("EXISTS")
        .arg(&foreign_cache_key)
        .query_async(&mut cache)
        .await
        .expect("read foreign cache after replay");
    assert_eq!(foreign_cache_exists, 1);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_token_limit_and_owner_scope_use_postgres_authority() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    let router = router_for_limited(pool, &valkey_url, 1);
    let owner = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    let first = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"first","remain_quota":1})),
            owner,
        )
        .await,
    )
    .await;
    assert_eq!(first["success"], true);
    let limited = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"second","remain_quota":1})),
            owner,
        )
        .await,
    )
    .await;
    assert_eq!(limited["success"], false);
    assert_eq!(limited["message"], "已达到最大令牌数量限制 (1)");

    let foreign = body(
        call(
            &router,
            "GET",
            "/api/token/1",
            None,
            ApiTokenPrincipal {
                user_id: 8,
                role: 1,
                preferred_language: None,
            },
        )
        .await,
    )
    .await;
    assert_eq!(foreign["success"], false);
    assert_eq!(foreign["message"], "record not found");
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn concurrent_create_keeps_the_legacy_count_then_insert_race_contract() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    let router = router_for_limited(pool.clone(), &valkey_url, 1);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    let (first, second) = tokio::join!(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"concurrent-first"})),
            principal,
        ),
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"concurrent-second"})),
            principal,
        )
    );
    let responses = [body(first).await, body(second).await];
    let successful = responses
        .iter()
        .filter(|response| response["success"] == true)
        .count();
    let limited = responses
        .iter()
        .filter(|response| response["message"] == "已达到最大令牌数量限制 (1)")
        .count();
    assert_eq!(successful + limited, 2);
    let persisted: i64 =
        sqlx::query_scalar("SELECT COUNT(*) FROM tokens WHERE user_id = 7 AND deleted_at IS NULL")
            .fetch_one(&pool)
            .await
            .expect("count concurrent token rows");
    // Go performs an unconstrained count followed by an insert. Depending on
    // scheduling the frozen race permits one or two inserts; the test records
    // that contract without imposing advisory-lock serialization.
    assert!((1..=2).contains(&persisted));
    assert_eq!(persisted as usize, successful);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_options_refresh_is_best_effort_and_retains_last_good_snapshot() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("INSERT INTO options (key,value) VALUES ('token_setting.max_user_tokens','2'),('QuotaPerUnit','2')")
        .execute(&pool)
        .await
        .expect("seed dynamic legacy options");
    let router = router_for(pool.clone(), &valkey_url);
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    for name in ["one", "two"] {
        let value = body(
            call(
                &router,
                "POST",
                "/api/token/",
                Some(json!({"name":name,"remain_quota":1})),
                principal,
            )
            .await,
        )
        .await;
        assert_eq!(value["success"], true);
    }
    let limit = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"three","remain_quota":1})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(limit["message"], "已达到最大令牌数量限制 (2)");

    // A successful refresh updates the in-memory snapshot without rebuilding
    // the service.
    sqlx::query("UPDATE options SET value = '3'")
        .execute(&pool)
        .await
        .expect("refresh token options");
    let refreshed = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"three","remain_quota":1})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(refreshed["success"], true);

    // A row-decode fault is not a request failure boundary: the last-good
    // snapshot remains authoritative for token work.
    sqlx::query("ALTER TABLE options ALTER COLUMN value TYPE BIGINT USING value::bigint")
        .execute(&pool)
        .await
        .expect("inject options row decode fault");
    let too_large = body(
        call(
            &router,
            "PUT",
            "/api/token/",
            Some(json!({"id":1,"name":"one","status":0,"remain_quota":3_000_000_001_i64})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(too_large["success"], false);

    // A SELECT fault has the same fail-safe behavior and does not become a
    // fabricated 503 or an options-specific error envelope.
    sqlx::query("DROP TABLE options")
        .execute(&pool)
        .await
        .expect("inject options SELECT fault");
    let retained = body(
        call(
            &router,
            "POST",
            "/api/token/",
            Some(json!({"name":"fourth","remain_quota":1})),
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(retained["success"], false);
    assert_eq!(retained["message"], "已达到最大令牌数量限制 (3)");
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_update_returns_loaded_mutation_without_a_post_write_select() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,7,'update-key',1,'before',1,1,-1,10,FALSE,FALSE,'',NULL,0,'default',FALSE)")
        .execute(&pool)
        .await
        .expect("seed update token");
    sqlx::query("ALTER TABLE tokens ENABLE ROW LEVEL SECURITY")
        .execute(&pool)
        .await
        .expect("enable row security");
    sqlx::query("ALTER TABLE tokens FORCE ROW LEVEL SECURITY")
        .execute(&pool)
        .await
        .expect("force row security");
    sqlx::query("CREATE POLICY token_visible ON tokens USING (current_setting('app.token_hidden', true) IS DISTINCT FROM '1') WITH CHECK (true)")
        .execute(&pool)
        .await
        .expect("install token visibility policy");
    sqlx::query("CREATE FUNCTION hide_token_after_update() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN PERFORM set_config('app.token_hidden', '1', false); RETURN NEW; END $$")
        .execute(&pool)
        .await
        .expect("install update fault trigger function");
    sqlx::query("CREATE TRIGGER hide_token_after_update AFTER UPDATE ON tokens FOR EACH ROW EXECUTE FUNCTION hide_token_after_update()")
        .execute(&pool)
        .await
        .expect("install update fault trigger");

    let value = body(
        call(
            &router_for(pool, &valkey_url),
            "PUT",
            "/api/token/",
            Some(json!({"id":1,"status":2,"name":"after","remain_quota":42})),
            ApiTokenPrincipal {
                user_id: 7,
                role: 1,
                preferred_language: None,
            },
        )
        .await,
    )
    .await;
    assert_eq!(value["success"], true);
    assert_eq!(value["data"]["name"], "after");
    assert_eq!(value["data"]["remain_quota"], 42);
    assert_eq!(value["data"]["status"], 1);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_row_decode_faults_keep_raw_generic_detail_but_search_maps_the_error() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("INSERT INTO tokens (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,\"group\",cross_group_retry) VALUES (1,7,'decode-key',1,'decode',1,1,-1,10,FALSE,FALSE,'',NULL,0,'default',FALSE)")
        .execute(&pool)
        .await
        .expect("seed decode token");
    sqlx::query("ALTER TABLE tokens ALTER COLUMN allow_ips TYPE BIGINT USING 1::BIGINT")
        .execute(&pool)
        .await
        .expect("inject incompatible row type");
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    for path in ["/api/token/?p=1&page_size=10", "/api/token/1"] {
        let value = body(
            call(
                &router_for(pool.clone(), &valkey_url),
                "GET",
                path,
                None,
                principal,
            )
            .await,
        )
        .await;
        assert_eq!(value["success"], false);
        assert_ne!(value["message"], "Internal server error");
        assert!(
            value["message"]
                .as_str()
                .unwrap_or_default()
                .contains("mismatched types")
        );
    }
    let searched = body(
        call(
            &router_for(pool, &valkey_url),
            "GET",
            "/api/token/search?keyword=decode",
            None,
            principal,
        )
        .await,
    )
    .await;
    assert_eq!(searched["success"], false);
    assert_eq!(searched["message"], "搜索令牌失败");
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_listener_preserves_field_specific_query_overflows_and_repeated_keys() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    let cases = [
        ("p=9223372036854775808&p=2", i64::MAX, 10),
        ("p=-9223372036854775809&p=2", i64::MIN, 10),
        ("p=1&ps=9223372036854775808&ps=3", 1, 100),
        ("p=1&ps=-9223372036854775809&ps=3", 1, i64::MIN),
        ("p=1&size=9223372036854775808&size=3", 1, 100),
        ("p=1&size=-9223372036854775809&size=3", 1, i64::MIN),
        ("p=1&page_size=9223372036854775808&page_size=4&ps=7", 1, 7),
        ("p=1&page_size=-9223372036854775809&page_size=4&ps=7", 1, 7),
        ("p=1&page_size=9223372036854775808&page_size=4&size=8", 1, 8),
        (
            "p=1&page_size=-9223372036854775809&page_size=4&size=8",
            1,
            8,
        ),
        ("p=1&page_size=9223372036854775808&page_size=4", 1, 10),
        ("p=1&page_size=-9223372036854775809&page_size=4", 1, 10),
        ("p=1&page_size=4&page_size=8", 1, 4),
    ];
    for route in ["/api/token/?", "/api/token/search?"] {
        for (query, expected_page, expected_page_size) in cases {
            let path = format!("{route}{query}");
            let value = body(
                call(
                    &router_for(pool.clone(), &valkey_url),
                    "GET",
                    &path,
                    None,
                    principal,
                )
                .await,
            )
            .await;
            assert_eq!(value["success"], true, "{path}");
            assert_eq!(value["data"]["page"], expected_page, "{path}");
            assert_eq!(value["data"]["page_size"], expected_page_size, "{path}");
        }
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; use tests/scripts/run-real-integration-gates.sh"]
async fn api_token_batch_key_database_fault_is_not_silently_downgraded_to_an_empty_map() {
    let database_url = env::var("LMM_API_TOKEN_TEST_DATABASE_URL")
        .expect("set LMM_API_TOKEN_TEST_DATABASE_URL for the isolated PostgreSQL 18 harness");
    let valkey_url = env::var("LMM_API_TOKEN_TEST_VALKEY_URL")
        .expect("set LMM_API_TOKEN_TEST_VALKEY_URL for the isolated Valkey harness");
    let pool = PgPoolOptions::new()
        .max_connections(3)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL 18");
    reset(&pool).await;
    sqlx::query("DROP TABLE tokens")
        .execute(&pool)
        .await
        .expect("inject batch read fault");
    let response = call(
        &router_for(pool, &valkey_url),
        "POST",
        "/api/token/batch/keys",
        Some(json!({"ids":[1]})),
        ApiTokenPrincipal {
            user_id: 7,
            role: 1,
            preferred_language: None,
        },
    )
    .await;
    assert_eq!(
        response.headers()["cache-control"],
        "no-store, no-cache, must-revalidate, private, max-age=0"
    );
    let value = body(response).await;
    assert_eq!(value["success"], false);
    assert!(value.get("data").is_none());
}

fn router() -> axum::Router {
    router_for(
        PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(25))
            .connect_lazy("postgres://127.0.0.1:1/unused")
            .expect("lazy test pool"),
        "redis://127.0.0.1/",
    )
}
fn router_for(pool: PgPool, valkey_url: &str) -> axum::Router {
    router_for_limited(pool, valkey_url, 1_000)
}
fn router_for_limited(pool: PgPool, valkey_url: &str, max_user_tokens: i64) -> axum::Router {
    router_for_with_wire_errors(pool, valkey_url, max_user_tokens, false)
}
fn frozen_router() -> axum::Router {
    router_for_with_wire_errors(
        PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(25))
            .connect_lazy("postgres://127.0.0.1:1/unused")
            .expect("lazy test pool"),
        "redis://127.0.0.1/",
        1_000,
        true,
    )
}
fn router_for_with_wire_errors(
    pool: PgPool,
    valkey_url: &str,
    max_user_tokens: i64,
    frozen_wire_errors: bool,
) -> axum::Router {
    api_token_router(
        ApiTokenHttpState::new(Arc::new(
            PgValkeyApiTokenService::new(pool, redis::Client::open(valkey_url).unwrap())
                .with_crypto_secret("api-token-integration-secret")
                .with_max_user_tokens(max_user_tokens),
        ))
        .with_frozen_wire_errors(frozen_wire_errors),
    )
}
fn token_cache_key(key: &str) -> String {
    let mut mac = Hmac::<sha2::Sha256>::new_from_slice(b"api-token-integration-secret")
        .expect("HMAC-SHA256 accepts every key length");
    mac.update(key.as_bytes());
    format!("token:{}", hex::encode(mac.finalize().into_bytes()))
}
async fn call(
    router: &axum::Router,
    method: &str,
    path: &str,
    payload: Option<Value>,
    principal: ApiTokenPrincipal,
) -> axum::response::Response {
    router
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .extension(principal)
                .header("content-type", "application/json")
                .body(Body::from(
                    payload.map(|value| value.to_string()).unwrap_or_default(),
                ))
                .unwrap(),
        )
        .await
        .unwrap()
}
async fn call_raw(
    router: &axum::Router,
    method: &str,
    path: &str,
    payload: &[u8],
    principal: ApiTokenPrincipal,
) -> axum::response::Response {
    router
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .extension(principal)
                .header("content-type", "application/json")
                .body(Body::from(payload.to_vec()))
                .unwrap(),
        )
        .await
        .unwrap()
}
async fn body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), usize::MAX).await.unwrap()).unwrap()
}
async fn token_row(
    pool: &PgPool,
    id: i64,
) -> (
    String,
    i64,
    i64,
    i64,
    bool,
    bool,
    String,
    String,
    String,
    bool,
    Option<String>,
) {
    let row = sqlx::query(
        "SELECT name, status, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, COALESCE(allow_ips, '') AS allow_ips, COALESCE(\"group\", '') AS group_name, cross_group_retry, deleted_at::text AS deleted_at FROM tokens WHERE id = $1",
    )
    .bind(id)
    .fetch_one(pool)
    .await
    .unwrap();
    (
        row.get("name"),
        row.get("status"),
        row.get("expired_time"),
        row.get("remain_quota"),
        row.get("unlimited_quota"),
        row.get("model_limits_enabled"),
        row.get("model_limits"),
        row.get("allow_ips"),
        row.get("group_name"),
        row.get("cross_group_retry"),
        row.get("deleted_at"),
    )
}
async fn assert_active_token_row(
    pool: &PgPool,
    id: i64,
    name: &str,
    status: i64,
    expired_time: i64,
) {
    let row = token_row(pool, id).await;
    assert_eq!(row.0, name);
    assert_eq!(row.1, status);
    assert_eq!(row.2, expired_time);
    assert_eq!(row.3, 0);
    assert!(!row.4);
    assert!(!row.5);
    assert_eq!(row.6, "");
    assert_eq!(row.7, "");
    assert_eq!(row.8, "");
    assert!(!row.9);
    assert_eq!(row.10, None);
}
async fn console_activated_at(pool: &PgPool, user_id: i64) -> i64 {
    sqlx::query_scalar("SELECT console_activated_at FROM users WHERE id = $1")
        .bind(user_id)
        .fetch_one(pool)
        .await
        .expect("console activation timestamp")
}
async fn reset(pool: &PgPool) {
    sqlx::query("DROP TABLE IF EXISTS options")
        .execute(pool)
        .await
        .unwrap();
    sqlx::query("DROP TABLE IF EXISTS tokens")
        .execute(pool)
        .await
        .unwrap();
    sqlx::query("DROP FUNCTION IF EXISTS hide_token_after_update()")
        .execute(pool)
        .await
        .unwrap();
    sqlx::query("DROP TABLE IF EXISTS users")
        .execute(pool)
        .await
        .unwrap();
    sqlx::query(
        "CREATE TABLE users (
            id BIGINT PRIMARY KEY,
            console_activated_at BIGINT NOT NULL DEFAULT 0,
            deleted_at TIMESTAMPTZ
        )",
    )
    .execute(pool)
    .await
    .unwrap();
    sqlx::query("INSERT INTO users (id) VALUES (7), (8)")
        .execute(pool)
        .await
        .unwrap();
    sqlx::query("CREATE TABLE tokens (id BIGSERIAL PRIMARY KEY,user_id BIGINT,key TEXT,status BIGINT,name TEXT,created_time BIGINT,accessed_time BIGINT,expired_time BIGINT,remain_quota BIGINT,unlimited_quota BOOL,model_limits_enabled BOOL,model_limits TEXT,allow_ips TEXT,used_quota BIGINT,\"group\" TEXT,cross_group_retry BOOL,deleted_at TIMESTAMPTZ)").execute(pool).await.unwrap();
    sqlx::query("CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT NOT NULL)")
        .execute(pool)
        .await
        .unwrap();
}
