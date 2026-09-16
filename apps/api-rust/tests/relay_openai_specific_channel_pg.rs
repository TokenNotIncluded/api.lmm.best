use std::{convert::Infallible, sync::Arc, time::Duration};

use axum::{
    Router,
    body::{Body, Bytes},
    extract::State,
    http::{Request, StatusCode, header},
    response::{IntoResponse, Response},
    routing::post,
};
use futures_util::{StreamExt, stream};
use lmm_api_rs::{
    relay_http::{RelayHttpClient, RelayTimeoutConfig},
    routes::relay_openai::{
        OpenAiRelayHttpState, OpenAiUpstreamClient, PgOpenAiRelayService, openai_relay_router,
    },
};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tokio::{net::TcpListener, sync::mpsc, task::JoinHandle, time::timeout};
use tower::ServiceExt;

const OPENAI_RESPONSE: &str = r#"{"id":"chatcmpl-fixed","object":"chat.completion","created":1,"model":"gpt-4o","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}"#;
const ROLE_ONLY_SSE: &[u8] = b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n";

type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

#[derive(Clone, Copy)]
enum MockUpstreamBehavior {
    JsonSuccess,
    RoleOnlySseThenStall,
}

#[derive(Clone)]
struct MockUpstreamState {
    behavior: MockUpstreamBehavior,
    sender: mpsc::Sender<()>,
}

struct MockUpstream {
    base_url: String,
    received: mpsc::Receiver<()>,
    task: JoinHandle<()>,
}

async fn upstream(State(state): State<MockUpstreamState>) -> Response {
    let _ = state.sender.send(()).await;
    match state.behavior {
        MockUpstreamBehavior::JsonSuccess => (
            StatusCode::OK,
            [(header::CONTENT_TYPE, "application/json")],
            OPENAI_RESPONSE,
        )
            .into_response(),
        MockUpstreamBehavior::RoleOnlySseThenStall => {
            let body =
                stream::once(async { Ok::<Bytes, Infallible>(Bytes::from_static(ROLE_ONLY_SSE)) })
                    .chain(stream::pending::<Result<Bytes, Infallible>>());
            Response::builder()
                .status(StatusCode::OK)
                .header(header::CONTENT_TYPE, "text/event-stream")
                .body(Body::from_stream(body))
                .expect("build role-only SSE response")
        }
    }
}

async fn spawn_upstream(behavior: MockUpstreamBehavior) -> TestResult<MockUpstream> {
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let address = listener.local_addr()?;
    let (sender, received) = mpsc::channel(4);
    let app = Router::new()
        .fallback(post(upstream))
        .with_state(MockUpstreamState { behavior, sender });
    let task = tokio::spawn(async move {
        let _ = axum::serve(listener, app).await;
    });
    Ok(MockUpstream {
        base_url: format!("http://{address}"),
        received,
        task,
    })
}

async fn isolated_pool() -> TestResult<Option<(PgPool, PgPool, String)>> {
    let Ok(database_url) = std::env::var("LMM_TEST_DATABASE_URL") else {
        return Ok(None);
    };
    let admin = PgPool::connect(&database_url).await?;
    let schema = format!("relay_openai_specific_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await?;
    let scoped = PgPoolOptions::new()
        .max_connections(1)
        .after_connect({
            let schema = schema.clone();
            move |connection, _metadata| {
                let statement = format!("SET search_path TO {schema}");
                Box::pin(async move {
                    sqlx::query(&statement).execute(connection).await?;
                    Ok(())
                })
            }
        })
        .connect(&database_url)
        .await?;
    Ok(Some((admin, scoped, schema)))
}

async fn create_minimal_relay_schema(pool: &PgPool) -> TestResult {
    for statement in [
        "CREATE TABLE users (id BIGINT PRIMARY KEY, status BIGINT, quota BIGINT, role BIGINT, deleted_at TIMESTAMPTZ, used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0)",
        "CREATE TABLE tokens (id BIGINT PRIMARY KEY, user_id BIGINT, status BIGINT, expired_time BIGINT, remain_quota BIGINT, unlimited_quota BOOLEAN, allow_ips TEXT, key TEXT, \"group\" TEXT, deleted_at TIMESTAMPTZ, accessed_time BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0)",
        "CREATE TABLE channels (id BIGINT PRIMARY KEY, status BIGINT, base_url TEXT, key TEXT, used_quota BIGINT DEFAULT 0)",
        "CREATE TABLE abilities (\"group\" TEXT, model TEXT, channel_id BIGINT, enabled BOOLEAN, priority BIGINT, weight BIGINT)",
        "CREATE TABLE logs (user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, model_name TEXT, quota BIGINT, channel_id BIGINT, token_id BIGINT, \"group\" TEXT, request_id TEXT, is_stream BOOLEAN)",
        "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT)",
    ] {
        sqlx::query(statement).execute(pool).await?;
    }
    Ok(())
}

async fn relay_request(
    router: &Router,
    authorization: &str,
    stream_response: bool,
) -> TestResult<StatusCode> {
    let body = if stream_response {
        r#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}],"stream":true}"#
    } else {
        r#"{"model":"gpt-4o","messages":[{"role":"user","content":"hello"}]}"#
    };
    let response = router
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/chat/completions")
                .header(header::AUTHORIZATION, authorization)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(body))?,
        )
        .await?;
    Ok(response.status())
}

fn first_output_test_client() -> TestResult<RelayHttpClient> {
    Ok(RelayHttpClient::new(RelayTimeoutConfig {
        response_headers: Some(Duration::from_secs(2)),
        idle: Duration::from_secs(2),
        total: None,
    })?
    .with_openai_first_output_timeout(Some(Duration::from_millis(100))))
}

async fn assert_reservation_refunded(pool: &PgPool, user_id: i64, token_id: i64) -> TestResult {
    let user: (i64, i64, i64) =
        sqlx::query_as("SELECT quota,used_quota,request_count FROM users WHERE id=$1")
            .bind(user_id)
            .fetch_one(pool)
            .await?;
    assert_eq!(
        user,
        (100, 0, 0),
        "failed attempt must fully refund user reservation"
    );

    let token: (i64, i64) =
        sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=$1")
            .bind(token_id)
            .fetch_one(pool)
            .await?;
    assert_eq!(
        token,
        (100, 0),
        "failed attempt must fully refund token reservation"
    );

    let channel_usage: Vec<(i64, i64)> =
        sqlx::query_as("SELECT id,COALESCE(used_quota,0) FROM channels ORDER BY id")
            .fetch_all(pool)
            .await?;
    assert!(
        channel_usage.iter().all(|(_, used_quota)| *used_quota == 0),
        "failed attempt must not leave channel usage charged: {channel_usage:?}"
    );

    let logs: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs")
        .fetch_one(pool)
        .await?;
    assert_eq!(logs, 0, "failed attempts must not create a success log");
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_specific_channel_enforces_role_pin_and_disabled_channel() -> TestResult {
    let Some((admin, pool, schema)) = isolated_pool().await? else {
        eprintln!(
            "skipping OpenAI specific-channel PostgreSQL test: LMM_TEST_DATABASE_URL is unset"
        );
        return Ok(());
    };

    let mut first = spawn_upstream(MockUpstreamBehavior::JsonSuccess).await?;
    let mut second = spawn_upstream(MockUpstreamBehavior::JsonSuccess).await?;

    let result = async {
        create_minimal_relay_schema(&pool).await?;
        sqlx::query(
            "INSERT INTO users (id,status,quota,role) VALUES (1,1,100,10),(2,1,100,1)",
        )
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO tokens (id,user_id,status,expired_time,remain_quota,unlimited_quota,allow_ips,key,\"group\") VALUES (11,1,1,-1,100,FALSE,'','admin','default'),(12,2,1,-1,100,FALSE,'','user','default')",
        )
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO channels (id,status,base_url,key) VALUES (1,1,$1,'first-key'),(2,1,$2,'second-key')",
        )
        .bind(&first.base_url)
        .bind(&second.base_url)
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-4o',1,TRUE,100,100),('default','gpt-4o',2,TRUE,1,1)",
        )
        .execute(&pool)
        .await?;

        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            ..Default::default()
        })?;
        let service = PgOpenAiRelayService::new(pool.clone(), OpenAiUpstreamClient::new(client), 1);
        let router = openai_relay_router(OpenAiRelayHttpState::new(Arc::new(service), "test"));

        assert_eq!(
            relay_request(&router, "Bearer sk-admin-2", false).await?,
            StatusCode::OK,
            "an admin suffix must pin the explicitly requested channel even when another channel has higher priority"
        );
        assert!(
            timeout(Duration::from_secs(1), second.received.recv())
                .await?
                .is_some(),
            "the pinned channel did not receive the upstream request"
        );
        assert!(
            first.received.try_recv().is_err(),
            "the normal weighted selector was used instead of the pinned channel"
        );

        assert_eq!(
            relay_request(&router, "Bearer sk-user-2", false).await?,
            StatusCode::FORBIDDEN,
            "non-admin users must not be allowed to specify a channel"
        );
        assert!(first.received.try_recv().is_err());
        assert!(second.received.try_recv().is_err());

        sqlx::query("UPDATE channels SET status=2 WHERE id=2")
            .execute(&pool)
            .await?;
        assert_eq!(
            relay_request(&router, "Bearer sk-admin-2", false).await?,
            StatusCode::FORBIDDEN,
            "a disabled pinned channel must fail closed instead of falling back"
        );
        assert!(first.received.try_recv().is_err());
        assert!(second.received.try_recv().is_err());

        assert_eq!(
            relay_request(&router, "Bearer sk-admin-not-a-channel", false).await?,
            StatusCode::BAD_REQUEST,
            "a malformed specific-channel suffix must be rejected"
        );
        Ok::<(), Box<dyn std::error::Error>>(())
    }
    .await;

    first.task.abort();
    second.task.abort();
    drop(pool);
    let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await;
    drop(admin);

    result?;
    cleanup?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_retry_times_zero_keeps_first_output_timeout_single_attempt_and_refunds()
-> TestResult {
    let Some((admin, pool, schema)) = isolated_pool().await? else {
        eprintln!("skipping OpenAI RetryTimes=0 PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };

    let mut first = spawn_upstream(MockUpstreamBehavior::RoleOnlySseThenStall).await?;
    let mut second = spawn_upstream(MockUpstreamBehavior::JsonSuccess).await?;

    let result = async {
        create_minimal_relay_schema(&pool).await?;
        sqlx::query("INSERT INTO options (key,value) VALUES ('RetryTimes','0')")
            .execute(&pool)
            .await?;
        sqlx::query("INSERT INTO users (id,status,quota,role) VALUES (1,1,100,1)")
            .execute(&pool)
            .await?;
        sqlx::query(
            "INSERT INTO tokens (id,user_id,status,expired_time,remain_quota,unlimited_quota,allow_ips,key,\"group\") VALUES (11,1,1,-1,100,FALSE,'','tenant','default')",
        )
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO channels (id,status,base_url,key) VALUES (1,1,$1,'first-key'),(2,1,$2,'second-key')",
        )
        .bind(&first.base_url)
        .bind(&second.base_url)
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-4o',1,TRUE,100,100),('default','gpt-4o',2,TRUE,1,1)",
        )
        .execute(&pool)
        .await?;

        let service = PgOpenAiRelayService::new(
            pool.clone(),
            OpenAiUpstreamClient::new(first_output_test_client()?),
            1,
        );
        let router = openai_relay_router(OpenAiRelayHttpState::new(Arc::new(service), "test"));

        assert_eq!(
            relay_request(&router, "Bearer sk-tenant", true).await?,
            StatusCode::GATEWAY_TIMEOUT,
            "RetryTimes=0 must preserve Go's single-attempt upstream_timeout behavior"
        );
        assert!(
            timeout(Duration::from_secs(1), first.received.recv())
                .await?
                .is_some(),
            "the highest-priority channel did not receive the first attempt"
        );
        assert!(
            second.received.try_recv().is_err(),
            "RetryTimes=0 must not fail over to a second channel"
        );
        assert_reservation_refunded(&pool, 1, 11).await?;
        Ok::<(), Box<dyn std::error::Error>>(())
    }
    .await;

    first.task.abort();
    second.task.abort();
    drop(pool);
    let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await;
    drop(admin);

    result?;
    cleanup?;
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn postgres_specific_channel_first_output_timeout_never_fails_over_even_with_retry_budget()
-> TestResult {
    let Some((admin, pool, schema)) = isolated_pool().await? else {
        eprintln!(
            "skipping OpenAI specific-channel retry PostgreSQL test: LMM_TEST_DATABASE_URL is unset"
        );
        return Ok(());
    };

    let mut pinned = spawn_upstream(MockUpstreamBehavior::RoleOnlySseThenStall).await?;
    let mut fallback = spawn_upstream(MockUpstreamBehavior::JsonSuccess).await?;

    let result = async {
        create_minimal_relay_schema(&pool).await?;
        sqlx::query("INSERT INTO options (key,value) VALUES ('RetryTimes','3')")
            .execute(&pool)
            .await?;
        sqlx::query("INSERT INTO users (id,status,quota,role) VALUES (1,1,100,10)")
            .execute(&pool)
            .await?;
        sqlx::query(
            "INSERT INTO tokens (id,user_id,status,expired_time,remain_quota,unlimited_quota,allow_ips,key,\"group\") VALUES (11,1,1,-1,100,FALSE,'','admin','default')",
        )
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO channels (id,status,base_url,key) VALUES (1,1,$1,'pinned-key'),(2,1,$2,'fallback-key')",
        )
        .bind(&pinned.base_url)
        .bind(&fallback.base_url)
        .execute(&pool)
        .await?;
        sqlx::query(
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight) VALUES ('default','gpt-4o',1,TRUE,100,100),('default','gpt-4o',2,TRUE,1,1)",
        )
        .execute(&pool)
        .await?;

        let service = PgOpenAiRelayService::new(
            pool.clone(),
            OpenAiUpstreamClient::new(first_output_test_client()?),
            1,
        );
        let router = openai_relay_router(OpenAiRelayHttpState::new(Arc::new(service), "test"));

        assert_eq!(
            relay_request(&router, "Bearer sk-admin-1", true).await?,
            StatusCode::GATEWAY_TIMEOUT,
            "Go specific-channel affinity must remain non-retryable even when RetryTimes > 0"
        );
        assert!(
            timeout(Duration::from_secs(1), pinned.received.recv())
                .await?
                .is_some(),
            "the pinned channel did not receive the upstream attempt"
        );
        assert!(
            fallback.received.try_recv().is_err(),
            "a specific-channel request must never fail over to another channel"
        );
        assert_reservation_refunded(&pool, 1, 11).await?;
        Ok::<(), Box<dyn std::error::Error>>(())
    }
    .await;

    pinned.task.abort();
    fallback.task.abort();
    drop(pool);
    let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await;
    drop(admin);

    result?;
    cleanup?;
    Ok(())
}
