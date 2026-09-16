//! Real PostgreSQL adapter coverage with loopback providers and session-local fixtures.

use lmm_api_rs::routes::relay_anthropic_gemini_postgres::PgAnthropicGeminiRelayBackend;
use sqlx::postgres::PgPoolOptions;

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn native_adapters_allow_three_second_headers_with_one_second_body_idle() {
    assert_native_timeout_scenario(TimeoutScenario::SlowHeaders).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn native_adapters_enforce_short_response_header_deadline() {
    assert_native_timeout_scenario(TimeoutScenario::ShortHeaders).await;
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn native_adapters_preserve_429_when_error_body_stalls() {
    assert_native_timeout_scenario(TimeoutScenario::StalledError).await;
}

#[derive(Clone, Copy)]
enum TimeoutScenario {
    SlowHeaders,
    ShortHeaders,
    StalledError,
}

async fn assert_native_timeout_scenario(scenario: TimeoutScenario) {
    use std::time::Duration;

    use axum::{
        Json, Router,
        body::{Body, Bytes},
        http::StatusCode,
        response::{IntoResponse, Response},
        routing::post,
    };
    use lmm_api_rs::{
        relay_http::{RelayHttpClient, RelayTimeoutConfig},
        routes::relay_anthropic_gemini::{
            RelayBackend, RelayChannel, RelayFailure, RelayProtocol, UpstreamReply, UpstreamRequest,
        },
    };
    use serde_json::json;

    let database_url =
        std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL URL must be configured");
    // A single connection keeps the fixture session-local, without modifying real tables.
    let pg = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .expect("isolated PostgreSQL connection");
    sqlx::query(
        "CREATE TEMPORARY TABLE channels (id BIGINT, base_url TEXT, key TEXT, status BIGINT, type BIGINT)",
    )
    .execute(&pg)
    .await
    .unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let provider = tokio::spawn(
        axum::serve(
            listener,
            Router::new().fallback(post(move || async move {
                if matches!(scenario, TimeoutScenario::StalledError) {
                    let body = Body::from_stream(futures_util::stream::pending::<
                        Result<Bytes, std::io::Error>,
                    >());
                    Response::builder()
                        .status(StatusCode::TOO_MANY_REQUESTS)
                        .body(body)
                        .unwrap()
                } else {
                    tokio::time::sleep(Duration::from_secs(3)).await;
                    Json(json!({"fixture": "delayed headers"})).into_response()
                }
            })),
        )
        .into_future(),
    );
    sqlx::query("INSERT INTO channels VALUES (1, $1, 'fixture-key', 1, 41)")
        .bind(format!("http://{address}"))
        .execute(&pg)
        .await
        .unwrap();
    let backend = PgAnthropicGeminiRelayBackend::new(
        pg.clone(),
        RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(match scenario {
                TimeoutScenario::SlowHeaders | TimeoutScenario::StalledError => 10,
                TimeoutScenario::ShortHeaders => 1,
            })),
            idle: Duration::from_secs(1),
            total: None,
        })
        .unwrap(),
    );
    for (protocol, request_path) in [
        (RelayProtocol::Anthropic, "/v1/messages"),
        (RelayProtocol::Gemini, "/v1beta/models/test:generateContent"),
    ] {
        let body = json!({"model": "test", "messages": [], "contents": []});
        let result = tokio::time::timeout(
            Duration::from_secs(20),
            backend.invoke(
                &RelayChannel {
                    id: 1,
                    upstream_model: "test".into(),
                },
                UpstreamRequest {
                    protocol,
                    model: "test".into(),
                    request_path: request_path.into(),
                    raw_body: serde_json::to_vec(&body).unwrap(),
                    body,
                    streaming: false,
                    request_id: "delayed-headers".into(),
                },
            ),
        )
        .await;
        match scenario {
            TimeoutScenario::SlowHeaders => assert!(
                matches!(result, Ok(Ok(UpstreamReply::Json(ref value))) if value == &json!({"fixture": "delayed headers"})),
                "{protocol:?}: {result:?}"
            ),
            TimeoutScenario::ShortHeaders => assert!(
                matches!(result, Ok(Err(RelayFailure::Upstream))),
                "{protocol:?}: {result:?}"
            ),
            TimeoutScenario::StalledError => assert!(
                matches!(result, Ok(Err(RelayFailure::Provider { status: StatusCode::TOO_MANY_REQUESTS, ref body })) if body == &json!("")),
                "{protocol:?}: {result:?}"
            ),
        }
    }
    provider.abort();
    assert!(provider.await.unwrap_err().is_cancelled());
    pg.close().await;
}

#[tokio::test]
async fn postgres_relay_backend_accepts_lazy_listener_dependencies() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy PostgreSQL URL");
    let client = lmm_api_rs::relay_http::RelayHttpClient::new(Default::default())
        .expect("bounded relay client");

    let _backend = PgAnthropicGeminiRelayBackend::new(pg, client);
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn xai_messages_preserve_provider_auth_json_sse_and_selection_boundaries() {
    use std::sync::Arc;

    use axum::{
        Json, Router,
        body::{Body, to_bytes},
        http::{HeaderMap, Request, StatusCode},
        response::{IntoResponse, Response},
        routing::post,
    };
    use lmm_api_rs::{
        relay_http::RelayHttpClient,
        routes::relay_anthropic_gemini::{
            RelayBackend, RelayFailure, RelayHttpState, RelayProtocol, UpstreamRequest, router,
        },
    };
    use serde_json::{Value, json};
    use tower::ServiceExt;

    const EVENTS: &str = concat!(
        include_str!("../../api-go/relay/channel/xai/testdata/native-messages.sse"),
        "\n"
    );
    let reply = json!({"id":"msg_xai","type":"message","role":"assistant","content":[{"type":"tool_use","id":"tool_1","name":"echo","input":{}}],"model":"grok-test","stop_reason":"tool_use","usage":{"input_tokens":7,"output_tokens":2}});
    let database_url =
        std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL URL must be configured");
    let pg = PgPoolOptions::new()
        .max_connections(1)
        .connect(&database_url)
        .await
        .unwrap();
    for sql in [
        "CREATE TEMPORARY TABLE channels (id BIGINT, base_url TEXT, key TEXT, status BIGINT, type BIGINT, model_mapping TEXT)",
        "CREATE TEMPORARY TABLE users (id BIGINT, status BIGINT, \"group\" TEXT, deleted_at TIMESTAMPTZ)",
        "CREATE TEMPORARY TABLE tokens (id BIGINT, user_id BIGINT, key TEXT, status BIGINT, expired_time BIGINT, remain_quota BIGINT, unlimited_quota BOOLEAN, \"group\" TEXT, deleted_at TIMESTAMPTZ)",
        "CREATE TEMPORARY TABLE abilities (channel_id BIGINT, \"group\" TEXT, model TEXT, enabled BOOLEAN, priority BIGINT, weight BIGINT)",
        "INSERT INTO users VALUES (1, 1, 'default', NULL)",
        "INSERT INTO tokens VALUES (1, 1, 'caller-token', 1, -1, 1000, FALSE, '', NULL)",
    ] {
        sqlx::query(sql).execute(&pg).await.unwrap();
    }
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let (sender, mut requests) = tokio::sync::mpsc::unbounded_channel();
    let provider_reply = reply.clone();
    let provider = tokio::spawn(axum::serve(listener, Router::new().route("/v1/messages", post(
        move |headers: HeaderMap, Json(body): Json<Value>| {
            let sender = sender.clone();
            let reply = provider_reply.clone();
            async move {
                sender.send((headers, body.clone())).unwrap();
                if body.get("max_tokens") == Some(&json!(13)) {
                    return (StatusCode::TOO_MANY_REQUESTS, Json(json!({"type":"error","error":{"type":"rate_limit_error","message":"fixture limit"}}))).into_response();
                }
                if body["stream"] == true {
                    Response::builder().header("content-type", "text/event-stream").body(Body::from(EVENTS)).unwrap()
                } else { Json(reply).into_response() }
            }
        }
    ))).into_future());
    // A higher-priority incompatible provider must never receive Messages,
    // including when selection is repeated after the xAI channel is disabled.
    for (id, kind, priority, key) in [
        (1_i64, 24_i64, 100_i64, "gemini-key"),
        (2, 48, 50, "xai-persisted-key"),
        (3, 14, 40, "anthropic-key"),
    ] {
        sqlx::query("INSERT INTO channels VALUES ($1, $2, $3, 1, $4, $5)")
            .bind(id)
            .bind(format!("http://{address}"))
            .bind(key)
            .bind(kind)
            .bind(r#"{"public-test":"grok-test"}"#)
            .execute(&pg)
            .await
            .unwrap();
        sqlx::query("INSERT INTO abilities VALUES ($1, 'default', 'public-test', TRUE, $2, 1)")
            .bind(id)
            .bind(priority)
            .execute(&pg)
            .await
            .unwrap();
    }
    let backend = Arc::new(PgAnthropicGeminiRelayBackend::new(
        pg.clone(),
        RelayHttpClient::new(Default::default()).unwrap(),
    ));
    let identity = backend.authenticate("sk-caller-token").await.unwrap();
    let selected = backend
        .select_channel(&identity, RelayProtocol::Anthropic, "public-test")
        .await
        .unwrap();
    assert_eq!(selected.id, 2);
    assert_eq!(selected.upstream_model, "grok-test");
    assert_eq!(
        backend
            .select_channel(&identity, RelayProtocol::Gemini, "public-test")
            .await
            .unwrap()
            .id,
        1
    );
    let app = router(RelayHttpState::new(backend.clone()));
    for streaming in [false, true] {
        let body = json!({"model":"public-test","max_tokens":16,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}],"tools":[{"name":"echo","input_schema":{"type":"object"}}],"tool_choice":{"type":"auto"},"metadata":{"fixture":"unchanged"},"stream":streaming});
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/v1/messages")
                    .header("x-api-key", "sk-caller-token")
                    .header("content-type", "application/json")
                    .body(Body::from(serde_json::to_vec(&body).unwrap()))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        if streaming {
            assert_eq!(
                response.headers()["cache-control"],
                "no-cache, no-transform"
            );
            assert_eq!(response.headers()["x-accel-buffering"], "no");
        }
        let raw = to_bytes(response.into_body(), 1024 * 1024).await.unwrap();
        if streaming {
            assert_eq!(raw.as_ref(), EVENTS.as_bytes());
        } else {
            assert_eq!(serde_json::from_slice::<Value>(&raw).unwrap(), reply);
        }
        let (headers, mut received) =
            tokio::time::timeout(std::time::Duration::from_secs(5), requests.recv())
                .await
                .unwrap()
                .unwrap();
        assert_eq!(headers["authorization"], "Bearer xai-persisted-key");
        assert!(!headers.contains_key("x-api-key"));
        assert!(!headers.contains_key("x-goog-api-key"));
        assert_eq!(received["model"], "grok-test");
        received["model"] = json!("public-test");
        assert_eq!(received, body);
    }
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-api-key", "sk-caller-token")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"model":"public-test","max_tokens":13,"messages":[]}"#,
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(
        serde_json::from_slice::<Value>(&to_bytes(response.into_body(), 4096).await.unwrap())
            .unwrap()["error"]["type"],
        "rate_limit_error"
    );
    tokio::time::timeout(std::time::Duration::from_secs(5), requests.recv())
        .await
        .unwrap()
        .unwrap();
    assert!(
        requests.try_recv().is_err(),
        "a provider error must not trigger an unbounded or incompatible replay"
    );
    sqlx::query("UPDATE channels SET status=2 WHERE id=2")
        .execute(&pg)
        .await
        .unwrap();
    assert_eq!(
        backend
            .select_channel(&identity, RelayProtocol::Anthropic, "public-test")
            .await
            .unwrap()
            .id,
        3
    );
    let body = json!({"model":"public-test","max_tokens":16,"messages":[]});
    let request = UpstreamRequest {
        protocol: RelayProtocol::Anthropic,
        model: "public-test".into(),
        request_path: "/v1/messages".into(),
        raw_body: serde_json::to_vec(&body).unwrap(),
        body,
        streaming: false,
        request_id: "stale-selection".into(),
    };
    assert!(matches!(
        backend.invoke(&selected, request).await,
        Err(RelayFailure::NoChannel)
    ));
    let response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/messages")
                .header("x-api-key", "sk-caller-token")
                .header("content-type", "application/json")
                .body(Body::from(
                    r#"{"model":"public-test","max_tokens":16,"messages":[]}"#,
                ))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let (headers, _) = tokio::time::timeout(std::time::Duration::from_secs(5), requests.recv())
        .await
        .unwrap()
        .unwrap();
    assert_eq!(headers["x-api-key"], "anthropic-key");
    assert_eq!(headers["anthropic-version"], "2023-06-01");
    assert!(!headers.contains_key("authorization"));
    assert!(requests.try_recv().is_err());
    provider.abort();
    assert!(provider.await.unwrap_err().is_cancelled());
    pg.close().await;
}
