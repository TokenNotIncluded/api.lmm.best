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
        "CREATE TEMPORARY TABLE channels (id BIGINT, base_url TEXT, key TEXT, status BIGINT)",
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
    sqlx::query("INSERT INTO channels VALUES (1, $1, 'fixture-key', 1)")
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
