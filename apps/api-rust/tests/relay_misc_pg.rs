use std::{
    env,
    net::{IpAddr, Ipv4Addr},
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::Response,
    routing::post,
};
use lmm_api_rs::{
    RequestContext,
    models::PgModelsService,
    routes::{
        relay_misc::{RelayMiscHttpState, routes},
        relay_misc_postgres::PgRelayMiscService,
    },
};
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgPoolOptions};
use tokio::net::TcpListener;
use tower::ServiceExt;

const SUCCESS_BODY: &str = r#"{"data":[{"embedding":[0.25],"index":0}],"model":"gpt-test","usage":{"prompt_tokens":1,"total_tokens":1}}"#;
const ERROR_BODY: &str = r#"{"error":"fixture-rate-limit"}"#;
const NORMALIZED_ERROR_BODY: &str = r#"{"error":{"message":"fixture-rate-limit","type":"bad_response_status_code","param":"","code":"bad_response_status_code"}}"#;

#[derive(Clone)]
struct ProviderState {
    attempts: Arc<AtomicUsize>,
    observations: Arc<Mutex<Vec<ProviderObservation>>>,
}

struct ProviderObservation {
    authorization: Option<String>,
    caller_secret: Option<String>,
    content_type: Option<String>,
    body: Value,
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires an isolated PostgreSQL database; set LMM_RELAY_MISC_TEST_DATABASE_URL and LMM_RELAY_MISC_TEST_ALLOW_SCHEMA_RESET=1"]
async fn fixed_price_embedding_settles_atomically_and_provider_failure_rolls_back() {
    let database_url = integration_database_url();
    let pool = PgPoolOptions::new()
        .max_connections(4)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("isolated relay PostgreSQL must be reachable");
    reset_schema(&pool).await;

    let provider = ProviderState {
        attempts: Arc::new(AtomicUsize::new(0)),
        observations: Arc::new(Mutex::new(Vec::new())),
    };
    let listener = TcpListener::bind((Ipv4Addr::LOCALHOST, 0))
        .await
        .expect("loopback provider listener");
    let provider_address = listener.local_addr().expect("provider address");
    let provider_server = tokio::spawn(
        axum::serve(
            listener,
            Router::new()
                .route("/v1/embeddings", post(provider_handler))
                .with_state(provider.clone()),
        )
        .into_future(),
    );

    seed(&pool, &format!("http://{provider_address}")).await;
    let models = Arc::new(PgModelsService::new(pool.clone()));
    let outbound = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .expect("loopback provider client");
    let app = routes(RelayMiscHttpState::new(Arc::new(PgRelayMiscService::new(
        pool.clone(),
        models,
        outbound,
        Duration::from_secs(3),
    ))));

    let success = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"hello"}"#,
        "relay-pg-success",
    )
    .await;
    assert_eq!(success.status(), StatusCode::OK);
    assert_eq!(success.headers()["x-request-id"], "provider-success");
    assert!(!success.headers().contains_key(header::CONNECTION));
    assert!(!success.headers().contains_key("x-hop-leak"));
    assert!(!success.headers().contains_key("x-oneapi-request-id"));
    assert_eq!(response_body(success).await, SUCCESS_BODY.as_bytes());

    assert_eq!(accounting_snapshot(&pool).await, (999, 1, 1, 999, 1, 1, 1));
    let last_api_activity_at =
        sqlx::query_scalar::<_, i64>("SELECT last_api_activity_at FROM users WHERE id=42")
            .fetch_one(&pool)
            .await
            .expect("last API activity timestamp");
    assert!(last_api_activity_at > 0);
    let log = sqlx::query(
        r#"SELECT user_id,type,username,token_name,model_name,quota,prompt_tokens,
                  completion_tokens,is_stream,channel_id,channel_name,token_id,"group",ip,
                  request_id,upstream_request_id,other
             FROM logs"#,
    )
    .fetch_one(&pool)
    .await
    .expect("consume log");
    assert_eq!(log.try_get::<i64, _>("user_id").unwrap(), 42);
    assert_eq!(log.try_get::<i64, _>("type").unwrap(), 2);
    assert_eq!(log.try_get::<String, _>("username").unwrap(), "relay-user");
    assert_eq!(
        log.try_get::<String, _>("token_name").unwrap(),
        "relay-token"
    );
    assert_eq!(log.try_get::<String, _>("model_name").unwrap(), "gpt-test");
    assert_eq!(log.try_get::<i64, _>("quota").unwrap(), 1);
    assert_eq!(log.try_get::<i64, _>("prompt_tokens").unwrap(), 1);
    assert_eq!(log.try_get::<i64, _>("completion_tokens").unwrap(), 0);
    assert!(!log.try_get::<bool, _>("is_stream").unwrap());
    assert_eq!(log.try_get::<i64, _>("channel_id").unwrap(), 7);
    assert_eq!(
        log.try_get::<Option<String>, _>("channel_name").unwrap(),
        None
    );
    assert_eq!(log.try_get::<i64, _>("token_id").unwrap(), 73);
    assert_eq!(log.try_get::<String, _>("group").unwrap(), "default");
    assert_eq!(log.try_get::<String, _>("ip").unwrap(), "");
    assert_eq!(
        log.try_get::<String, _>("request_id").unwrap(),
        "relay-pg-success"
    );
    assert_eq!(
        log.try_get::<String, _>("upstream_request_id").unwrap(),
        "provider-shadow-request-id"
    );
    assert_eq!(
        serde_json::from_str::<Value>(&log.try_get::<String, _>("other").unwrap()).unwrap(),
        json!({
            "admin_info": {
                "usage_billing_path": "upstream",
                "use_channel": ["7"],
            },
            "billing_source": "wallet",
            "model_ratio": 0,
            "group_ratio": 0.97,
            "completion_ratio": 0,
            "cache_tokens": 0,
            "cache_ratio": 0,
            "model_price": 0.000002,
            "user_group_ratio": -1,
            "frt": -1000,
            "request_path": "/v1/embeddings",
            "request_conversion": ["embedding"],
        })
    );

    let unverified_protocol = call_at(
        &app,
        "/v1/rerank",
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"must-not-forward"}"#,
        "relay-pg-unverified-protocol",
    )
    .await;
    assert_eq!(
        unverified_protocol.status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(provider.attempts.load(Ordering::SeqCst), 1);

    sqlx::query("UPDATE channels SET key='provider-one\nprovider-two' WHERE id=7")
        .execute(&pool)
        .await
        .expect("enable multi-key rejection fixture");
    let multi_key = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"must-not-forward"}"#,
        "relay-pg-multi-key",
    )
    .await;
    assert_eq!(multi_key.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(provider.attempts.load(Ordering::SeqCst), 1);
    sqlx::query("UPDATE channels SET key='provider-owned-secret' WHERE id=7")
        .execute(&pool)
        .await
        .expect("restore single provider key");

    sqlx::query("UPDATE channels SET status_code_mapping='{\"429\":503}' WHERE id=7")
        .execute(&pool)
        .await
        .expect("enable status-code mapping fixture");
    let status_code_mapping = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"mapped-error"}"#,
        "relay-pg-status-code-mapping",
    )
    .await;
    assert_eq!(
        status_code_mapping.status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(
        response_body(status_code_mapping).await,
        NORMALIZED_ERROR_BODY.as_bytes()
    );
    assert_eq!(provider.attempts.load(Ordering::SeqCst), 2);
    sqlx::query("UPDATE channels SET status_code_mapping='' WHERE id=7")
        .execute(&pool)
        .await
        .expect("restore empty status-code mapping");

    sqlx::query(
        r#"INSERT INTO channels
           (id,type,key,status,name,weight,base_url,"group",used_quota,model_mapping,priority,param_override,header_override,status_code_mapping)
           SELECT 8,type,'second-provider-secret',status,'second-loopback',weight,base_url,"group",0,model_mapping,priority,param_override,header_override,status_code_mapping
             FROM channels WHERE id=7"#,
    )
    .execute(&pool)
    .await
    .expect("second channel fixture");
    sqlx::query(
        r#"INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight)
           VALUES ('default','gpt-test',8,TRUE,10,10)"#,
    )
    .execute(&pool)
    .await
    .expect("same-priority ability fixture");
    let ambiguous_channel = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"must-not-forward"}"#,
        "relay-pg-ambiguous-channel",
    )
    .await;
    assert_eq!(ambiguous_channel.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(provider.attempts.load(Ordering::SeqCst), 2);
    sqlx::query("DELETE FROM abilities WHERE channel_id=8")
        .execute(&pool)
        .await
        .expect("remove second ability fixture");
    sqlx::query("DELETE FROM channels WHERE id=8")
        .execute(&pool)
        .await
        .expect("remove second channel fixture");

    sqlx::query("UPDATE users SET quota=0 WHERE id=42")
        .execute(&pool)
        .await
        .expect("enable insufficient wallet fixture");
    sqlx::query("UPDATE options SET value='{\"gpt-test\":0.000004}' WHERE key='ModelPrice'")
        .execute(&pool)
        .await
        .expect("raise fixture pre-consume quota");
    let insufficient_wallet = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"must-not-forward"}"#,
        "relay-pg-insufficient-wallet",
    )
    .await;
    assert_eq!(insufficient_wallet.status(), StatusCode::FORBIDDEN);
    assert_eq!(provider.attempts.load(Ordering::SeqCst), 2);
    sqlx::query("UPDATE options SET value='{\"gpt-test\":0.000002}' WHERE key='ModelPrice'")
        .execute(&pool)
        .await
        .expect("restore fixture model price");
    sqlx::query("UPDATE users SET quota=999 WHERE id=42")
        .execute(&pool)
        .await
        .expect("restore wallet quota");

    let provider_error = call(
        &app,
        "Bearer sk-relayprobe",
        r#"{"model":"gpt-test","input":"fail"}"#,
        "relay-pg-error",
    )
    .await;
    assert_eq!(provider_error.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(
        provider_error.headers()[header::CONTENT_TYPE],
        "application/json; charset=utf-8"
    );
    assert!(!provider_error.headers().contains_key(header::RETRY_AFTER));
    assert!(!provider_error.headers().contains_key("x-request-id"));
    assert_eq!(
        response_body(provider_error).await,
        NORMALIZED_ERROR_BODY.as_bytes()
    );
    assert_eq!(
        accounting_snapshot(&pool).await,
        (999, 1, 1, 999, 1, 1, 1),
        "provider failure must not change quota or append a consume log"
    );

    let invalid = call(
        &app,
        "Bearer sk-not-a-token",
        r#"{"model":"gpt-test","input":"hidden"}"#,
        "relay-pg-invalid",
    )
    .await;
    assert_eq!(invalid.status(), StatusCode::NOT_FOUND);
    assert_eq!(
        response_body(invalid).await,
        br#"{"message":"Not Found"}"#.as_slice()
    );

    {
        let observations = provider
            .observations
            .lock()
            .expect("provider observation lock");
        assert_eq!(
            observations.len(),
            3,
            "invalid token must not reach provider"
        );
        for observation in observations.iter() {
            assert_eq!(
                observation.authorization.as_deref(),
                Some("Bearer provider-owned-secret")
            );
            assert_eq!(observation.caller_secret, None);
            assert_eq!(
                observation.content_type.as_deref(),
                Some("application/json")
            );
            assert_eq!(observation.body["model"], "gpt-test");
        }
        assert_eq!(observations[0].body["input"], "hello");
        assert_eq!(observations[1].body["input"], "mapped-error");
        assert_eq!(observations[2].body["input"], "fail");
    }

    provider_server.abort();
    let _ = provider_server.await;
    pool.close().await;
}

async fn provider_handler(State(state): State<ProviderState>, request: Request) -> Response {
    let attempt = state.attempts.fetch_add(1, Ordering::SeqCst);
    let (parts, body) = request.into_parts();
    let body = to_bytes(body, 1024 * 1024)
        .await
        .expect("bounded provider request");
    state
        .observations
        .lock()
        .expect("provider observation lock")
        .push(ProviderObservation {
            authorization: header_text(&parts.headers, header::AUTHORIZATION),
            caller_secret: header_text(&parts.headers, "x-caller-secret"),
            content_type: header_text(&parts.headers, header::CONTENT_TYPE),
            body: serde_json::from_slice(&body).expect("provider JSON body"),
        });

    let (status, body, request_id) = if attempt == 0 {
        (StatusCode::OK, SUCCESS_BODY, "provider-success")
    } else {
        (
            StatusCode::TOO_MANY_REQUESTS,
            ERROR_BODY,
            "provider-rate-limit",
        )
    };
    let mut response = Response::new(Body::from(body));
    *response.status_mut() = status;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    response.headers_mut().insert(
        "x-request-id",
        HeaderValue::from_str(request_id).expect("provider request id"),
    );
    response.headers_mut().insert(
        header::CONNECTION,
        HeaderValue::from_static("x-hop-leak, keep-alive"),
    );
    response.headers_mut().insert(
        "x-hop-leak",
        HeaderValue::from_static("must-not-reach-caller"),
    );
    response.headers_mut().insert(
        "x-oneapi-request-id",
        HeaderValue::from_static("provider-shadow-request-id"),
    );
    if status == StatusCode::TOO_MANY_REQUESTS {
        response
            .headers_mut()
            .insert(header::RETRY_AFTER, HeaderValue::from_static("7"));
    }
    response
}

fn header_text(headers: &HeaderMap, name: impl axum::http::header::AsHeaderName) -> Option<String> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::to_owned)
}

async fn call(app: &Router, authorization: &str, body: &'static str, request_id: &str) -> Response {
    call_at(app, "/v1/embeddings", authorization, body, request_id).await
}

async fn call_at(
    app: &Router,
    path: &str,
    authorization: &str,
    body: &'static str,
    request_id: &str,
) -> Response {
    let mut request = Request::builder()
        .method("POST")
        .uri(path)
        .header(header::AUTHORIZATION, authorization)
        .header(header::CONTENT_TYPE, "application/json")
        .header("x-caller-secret", "must-not-reach-provider")
        .body(Body::from(body))
        .expect("relay request");
    request.extensions_mut().insert(RequestContext {
        request_id: request_id.to_owned(),
        client_ip: Some(IpAddr::V4(Ipv4Addr::LOCALHOST)),
    });
    app.clone().oneshot(request).await.expect("relay response")
}

async fn response_body(response: Response) -> axum::body::Bytes {
    to_bytes(response.into_body(), 1024 * 1024)
        .await
        .expect("bounded relay response")
}

async fn accounting_snapshot(pool: &PgPool) -> (i64, i64, i64, i64, i64, i64, i64) {
    let row = sqlx::query(
        r#"SELECT u.quota AS user_quota,u.used_quota AS user_used,u.request_count,
                  t.remain_quota,t.used_quota AS token_used,c.used_quota AS channel_used,
                  (SELECT COUNT(*) FROM logs) AS log_count
             FROM users u
             JOIN tokens t ON t.user_id=u.id
             JOIN channels c ON c.id=7
            WHERE u.id=42 AND t.id=73"#,
    )
    .fetch_one(pool)
    .await
    .expect("accounting snapshot");
    (
        row.try_get("user_quota").unwrap(),
        row.try_get("user_used").unwrap(),
        row.try_get("request_count").unwrap(),
        row.try_get("remain_quota").unwrap(),
        row.try_get("token_used").unwrap(),
        row.try_get("channel_used").unwrap(),
        row.try_get("log_count").unwrap(),
    )
}

fn integration_database_url() -> String {
    assert_eq!(
        env::var("LMM_RELAY_MISC_TEST_ALLOW_SCHEMA_RESET").as_deref(),
        Ok("1"),
        "relay integration test requires LMM_RELAY_MISC_TEST_ALLOW_SCHEMA_RESET=1"
    );
    let database_url = env::var("LMM_RELAY_MISC_TEST_DATABASE_URL")
        .expect("LMM_RELAY_MISC_TEST_DATABASE_URL is required");
    let parsed = reqwest::Url::parse(&database_url).expect("valid PostgreSQL URL");
    let loopback = parsed
        .host_str()
        .and_then(|host| host.parse::<IpAddr>().ok())
        .is_some_and(|host| host.is_loopback());
    assert!(
        loopback,
        "relay integration PostgreSQL must be loopback-only"
    );
    database_url
}

async fn reset_schema(pool: &PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS top_ups CASCADE",
        "DROP TABLE IF EXISTS logs CASCADE",
        "DROP TABLE IF EXISTS abilities CASCADE",
        "DROP TABLE IF EXISTS channels CASCADE",
        "DROP TABLE IF EXISTS tokens CASCADE",
        "DROP TABLE IF EXISTS users CASCADE",
        "DROP TABLE IF EXISTS options CASCADE",
        "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT)",
        r#"CREATE TABLE users (
            id BIGINT PRIMARY KEY, username TEXT, password TEXT NOT NULL, role BIGINT DEFAULT 1,
            status BIGINT DEFAULT 1, email TEXT, quota BIGINT DEFAULT 0,
            used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0,
            created_at BIGINT NOT NULL DEFAULT 0,
            last_api_activity_at BIGINT NOT NULL DEFAULT 0,
            trust_level_override BIGINT,
            "group" VARCHAR(64) DEFAULT 'default', setting TEXT, auth_version BIGINT DEFAULT 1,
            deleted_at TIMESTAMPTZ
        )"#,
        r#"CREATE TABLE tokens (
            id BIGINT PRIMARY KEY, user_id BIGINT, key VARCHAR(128), status BIGINT DEFAULT 1,
            name TEXT, created_time BIGINT, accessed_time BIGINT, expired_time BIGINT DEFAULT -1,
            remain_quota BIGINT DEFAULT 0, unlimited_quota BOOLEAN,
            model_limits_enabled BOOLEAN, model_limits TEXT, allow_ips TEXT DEFAULT '',
            used_quota BIGINT DEFAULT 0, "group" TEXT DEFAULT '', cross_group_retry BOOLEAN,
            deleted_at TIMESTAMPTZ
        )"#,
        r#"CREATE TABLE channels (
            id BIGINT PRIMARY KEY, type BIGINT DEFAULT 0, key TEXT NOT NULL,
            status BIGINT DEFAULT 1, name TEXT, weight BIGINT DEFAULT 0,
            base_url TEXT DEFAULT '', "group" VARCHAR(64) DEFAULT 'default',
            used_quota BIGINT DEFAULT 0, model_mapping TEXT, priority BIGINT DEFAULT 0,
            param_override TEXT, header_override TEXT, status_code_mapping TEXT
        )"#,
        r#"CREATE TABLE abilities (
            "group" VARCHAR(64) NOT NULL, model VARCHAR(255) NOT NULL,
            channel_id BIGINT NOT NULL, enabled BOOLEAN, priority BIGINT DEFAULT 0,
            weight BIGINT DEFAULT 0, PRIMARY KEY ("group",model,channel_id)
        )"#,
        r#"CREATE TABLE logs (
            user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT,
            username TEXT DEFAULT '', token_name TEXT DEFAULT '', model_name TEXT DEFAULT '',
            quota BIGINT DEFAULT 0, prompt_tokens BIGINT DEFAULT 0,
            completion_tokens BIGINT DEFAULT 0, use_time BIGINT DEFAULT 0,
            is_stream BOOLEAN, channel_id BIGINT, channel_name TEXT, token_id BIGINT DEFAULT 0,
            "group" TEXT, ip TEXT DEFAULT '', request_id VARCHAR(64) DEFAULT '',
            upstream_request_id VARCHAR(128) DEFAULT '', other TEXT
        )"#,
        r#"CREATE TABLE top_ups (
            id BIGINT PRIMARY KEY, user_id BIGINT, amount BIGINT DEFAULT 0,
            credited_quota BIGINT DEFAULT 0, settled_amount_micros BIGINT DEFAULT 0,
            money DOUBLE PRECISION DEFAULT 0, payment_method TEXT,
            payment_provider TEXT, create_time BIGINT DEFAULT 0,
            complete_time BIGINT DEFAULT 0, status TEXT
        )"#,
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("isolated relay schema statement");
    }
}

async fn seed(pool: &PgPool, provider_url: &str) {
    for (key, value) in [
        ("performance_setting.monitor_enabled", "false"),
        ("ModelRequestRateLimitEnabled", "false"),
        ("UserUsableGroups", r#"{"default":"default"}"#),
        ("GroupRatio", r#"{"default":1}"#),
        ("GroupGroupRatio", "{}"),
        ("ModelPrice", r#"{"gpt-test":0.000002}"#),
        ("QuotaPerUnit", "500000"),
    ] {
        sqlx::query("INSERT INTO options (key,value) VALUES ($1,$2)")
            .bind(key)
            .bind(value)
            .execute(pool)
            .await
            .expect("relay option fixture");
    }
    sqlx::query(
        r#"INSERT INTO users
           (id,username,password,role,status,email,quota,used_quota,request_count,created_at,
            trust_level_override,"group",setting,auth_version)
           VALUES (42,'relay-user','unused',1,1,'relay@example.test',1000,0,0,
                   EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::BIGINT,NULL,'default','{}',1)"#,
    )
    .execute(pool)
    .await
    .expect("relay user fixture");
    sqlx::query(
        r#"INSERT INTO top_ups
           (id,user_id,amount,credited_quota,settled_amount_micros,money,payment_method,
            payment_provider,create_time,complete_time,status)
           VALUES (1,42,50000000,0,0,100,'stripe','stripe',
                   EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::BIGINT,
                   EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::BIGINT,'success')"#,
    )
    .execute(pool)
    .await
    .expect("paid trust-level fixture");
    sqlx::query(
        r#"INSERT INTO tokens
           (id,user_id,key,status,name,created_time,accessed_time,expired_time,remain_quota,
            unlimited_quota,model_limits_enabled,model_limits,allow_ips,used_quota,"group",cross_group_retry)
           VALUES (73,42,'relayprobe',1,'relay-token',1,1,-1,1000,FALSE,FALSE,'','',0,'',FALSE)"#,
    )
    .execute(pool)
    .await
    .expect("relay token fixture");
    sqlx::query(
        r#"INSERT INTO channels
           (id,type,key,status,name,weight,base_url,"group",used_quota,model_mapping,priority,param_override,header_override,status_code_mapping)
           VALUES (7,1,'provider-owned-secret',1,'loopback',10,$1,'default',0,'',10,'','','')"#,
    )
    .bind(provider_url)
    .execute(pool)
    .await
    .expect("relay channel fixture");
    sqlx::query(
        r#"INSERT INTO abilities ("group",model,channel_id,enabled,priority,weight)
           VALUES ('default','gpt-test',7,TRUE,10,10)"#,
    )
    .execute(pool)
    .await
    .expect("relay ability fixture");
}
