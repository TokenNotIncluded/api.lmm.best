//! Real PostgreSQL charging + double-entry ledger, with a local synthetic TCP
//! provider. These are protocol/ledger tests, not live supplier compatibility.
use axum::{
    Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::State,
    http::{Request, StatusCode},
    response::{IntoResponse, Response},
    routing::post,
};
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        charging::{
            Charging, Outcome as ChargingOutcome, Price as ChargingPrice,
            Request as ChargingRequest, Usage as ChargingUsage,
        },
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    funding::FundingPolicy,
    identity::{DashboardKeyInput, IdentityStore},
    model_http::{ChargePolicy, ModelGateway, UpstreamCredential},
    relay::{ClientProtocol, Limits, Request as RelayRequest},
    routing::*,
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};
use tokio::{
    net::TcpListener,
    sync::{Semaphore, mpsc},
    task::JoinHandle,
};
use tokio_stream::{StreamExt, wrappers::ReceiverStream};
use tower::ServiceExt;

struct Provider {
    endpoint: String,
    hits: Arc<AtomicUsize>,
    auth: Arc<Mutex<Vec<String>>>,
    task: JoinHandle<()>,
    stream_gate: Arc<Semaphore>,
}
impl Drop for Provider {
    fn drop(&mut self) {
        self.task.abort();
    }
}

#[derive(Clone)]
struct ProviderState {
    hits: Arc<AtomicUsize>,
    auth: Arc<Mutex<Vec<String>>>,
    usage: bool,
    stream_gate: Arc<Semaphore>,
}
async fn provider(
    State(state): State<ProviderState>,
    headers: axum::http::HeaderMap,
    Json(request): Json<Value>,
) -> Response {
    state.hits.fetch_add(1, Ordering::SeqCst);
    state
        .auth
        .lock()
        .unwrap()
        .push(headers["authorization"].to_str().unwrap().to_owned());
    assert_eq!(request["model"], "provider-model");
    if request["messages"][0]["content"] == "pause" {
        tokio::time::sleep(Duration::from_secs(30)).await;
    }
    if request["messages"][0]["content"] == "rate-limit" {
        return (StatusCode::TOO_MANY_REQUESTS, "synthetic supplier refusal").into_response();
    }
    if request["stream"] == true {
        let malformed_tool = request["messages"][0]["content"] == "malformed-tool";
        let content = if malformed_tool {
            format!(
                "data: {}\n\n",
                json!({"id":"synthetic-stream","choices":[{"index":0,"delta":{"role":"assistant","tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather","arguments":"{"}}]},"finish_reason":null}]})
            )
        } else {
            format!(
                "data: {}\n\n",
                json!({"id":"synthetic-stream","choices":[{"index":0,"delta":{"role":"assistant","content":"local stream answer"},"finish_reason":null}]})
            )
        };
        let finish = format!(
            "data: {}\n\n",
            json!({"id":"synthetic-stream","choices":[{"index":0,"delta":{},"finish_reason":if malformed_tool {"tool_calls"} else {"stop"}}]})
        );
        let usage = format!(
            "data: {}\n\n",
            json!({"id":"synthetic-stream","choices":[],"usage":{"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":2}}})
        );
        let prompt = request["messages"][0]["content"]
            .as_str()
            .unwrap()
            .to_owned();
        let (tx, rx) = mpsc::channel::<Result<Bytes, std::io::Error>>(8);
        tokio::spawn(async move {
            if tx.send(Ok(Bytes::from(content))).await.is_err() {
                return;
            }
            if prompt == "stream-pause" {
                state.stream_gate.acquire().await.unwrap().forget();
            }
            if prompt == "malformed" {
                let _ = tx
                    .send(Ok(Bytes::from_static(b"data: {invalid json\n\n")))
                    .await;
                return;
            }
            let _ = tx.send(Ok(Bytes::from(finish))).await;
            if state.usage {
                let _ = tx.send(Ok(Bytes::from(usage))).await;
            }
            if prompt != "truncate" {
                let _ = tx.send(Ok(Bytes::from_static(b"data: [DONE]\n\n"))).await;
            }
        });
        return (
            [("content-type", "text/event-stream")],
            Body::from_stream(ReceiverStream::new(rx)),
        )
            .into_response();
    }
    let mut value = json!({"id":"synthetic-completion","object":"chat.completion","created":0,"model":"provider-model",
        "choices":[{"index":0,"message":{"role":"assistant","content":"local synthetic answer"},"finish_reason":"stop"}]});
    if state.usage {
        value["usage"] = json!({"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":2}});
    }
    Json(value).into_response()
}
impl Provider {
    async fn start(usage: bool) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/custom/v1", listener.local_addr().unwrap());
        let hits = Arc::new(AtomicUsize::new(0));
        let auth = Arc::new(Mutex::new(Vec::new()));
        let stream_gate = Arc::new(Semaphore::new(0));
        let app = Router::new()
            .route("/custom/v1/chat/completions", post(provider))
            .with_state(ProviderState {
                hits: hits.clone(),
                auth: auth.clone(),
                usage,
                stream_gate: stream_gate.clone(),
            });
        let task = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        Self {
            endpoint,
            hits,
            auth,
            task,
            stream_gate,
        }
    }
}

fn routes(
    endpoint: &str,
    cache_write: u64,
    request_fee: u64,
    policy: PayloadPolicy,
) -> Arc<lmm_core::routing::Router> {
    let prices = PriceSnapshot::compile(PriceConfig {
        version: 1,
        rules: vec![PriceRule {
            billing_mode: lmm_core::routing::BillingMode::Tokens,
            id: 1,
            model: "local-model".into(),
            group: "allowed".into(),
            speed: Speed::Standard,
            rates: TokenRates {
                input: 2_000_000,
                output: 4_000_000,
                cache_read: 500_000,
                cache_write,
                request: request_fee,
            },
            group_multiplier_ppm: MULTIPLIER_ONE,
            speed_multiplier_ppm: MULTIPLIER_ONE,
        }],
    })
    .unwrap();
    Arc::new(
        lmm_core::routing::Router::new(
            RoutingConfig {
                version: 1,
                groups: vec![ModelGroup {
                    name: "allowed".into(),
                    enabled: true,
                }],
                upstreams: vec![UpstreamConfig {
                    id: 1,
                    endpoint: if endpoint.starts_with("https:") {
                        endpoint.to_owned().try_into().unwrap()
                    } else {
                        Endpoint::local_loopback(endpoint.to_owned()).unwrap()
                    },
                    protocol: Protocol::OpenAiChat,
                    credential: CredentialRef { id: 7, revision: 1 },
                    enabled: true,
                }],
                routes: vec![ModelRoute {
                    id: 1,
                    model: "local-model".into(),
                    group: "allowed".into(),
                    enabled: true,
                    speeds: vec![Speed::Standard],
                    targets: vec![RouteTarget {
                        id: 1,
                        upstream_id: 1,
                        upstream_model: "provider-model".into(),
                        enabled: true,
                        priority: 0,
                        weight: 1,
                        speeds: vec![Speed::Standard],
                        payload: policy,
                        timeout_ms: 5_000,
                    }],
                    failover: FailoverPolicy {
                        max_attempts: 1,
                        retry_on: vec![],
                        allow_replay_after_send: false,
                    },
                }],
                aliases: vec![],
            },
            Arc::new(prices),
        )
        .unwrap(),
    )
}

fn gateway(
    pool: &PgPool,
    provider: &Provider,
    key: i64,
    cache_write: u64,
    request_fee: u64,
    payload: PayloadPolicy,
) -> ModelGateway {
    gateway_with_routes(
        pool,
        routes(&provider.endpoint, cache_write, request_fee, payload),
        key,
    )
}
fn gateway_with_routes(
    pool: &PgPool,
    router: Arc<lmm_core::routing::Router>,
    key: i64,
) -> ModelGateway {
    ModelGateway::new(
        pool.clone(),
        router,
        vec![UpstreamCredential {
            reference: CredentialRef { id: 7, revision: 1 },
            secret: "synthetic-upstream-key".into(),
        }],
        BTreeMap::from([(key, "allowed".into())]),
        Limits {
            total_timeout: Duration::from_secs(3),
            hook_timeout: Duration::from_secs(2),
            ..Limits::default()
        },
        ChargePolicy {
            input_token_ceiling: 100,
            lease_seconds: 30,
        },
    )
    .unwrap()
}
async fn fixture(pool: &PgPool, amount: i64) -> (IdentityStore, Ledger, String, String, i64, i64) {
    core_schema::init(pool).await.unwrap();
    core_schema::check(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 1).await.unwrap();
    let principal = identity.authorize(&session.secret).await.unwrap();
    let key = identity
        .issue_key(
            &session.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger
        .open_wallet(principal.owner_account_id)
        .await
        .unwrap();
    if amount > 0 {
        ledger
            .execute(&LedgerRequest {
                scope: "model-http-local-test".into(),
                operation_key: "synthetic-funds".into(),
                actor_user_id: Some(7),
                reason: "Synthetic funds in a disposable local database".into(),
                action: Action::Credit {
                    account_id: principal.owner_account_id,
                    amount_units: Credits::try_from(amount).unwrap(),
                    payment_reference: "synthetic:model-http-test".into(),
                },
            })
            .await
            .unwrap();
    }
    (
        identity,
        ledger,
        session.secret,
        key.secret,
        key.id,
        principal.owner_account_id,
    )
}
fn chat() -> Value {
    json!({"model":"local-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20})
}
async fn call(
    gateway: &ModelGateway,
    key: Option<&str>,
    path: &str,
    body: Value,
    id: Option<&str>,
) -> (StatusCode, Value) {
    let (status, value, _) = call_with_request_id(gateway, key, path, body, id).await;
    (status, value)
}
async fn call_with_request_id(
    gateway: &ModelGateway,
    key: Option<&str>,
    path: &str,
    body: Value,
    id: Option<&str>,
) -> (StatusCode, Value, Option<String>) {
    let mut builder = Request::builder()
        .method(if path == "/v1/models" { "GET" } else { "POST" })
        .uri(path)
        .header("content-type", "application/json");
    if let Some(key) = key {
        builder = builder.header("authorization", format!("Bearer {key}"));
    }
    if let Some(id) = id {
        builder = builder.header("idempotency-key", id);
    }
    let response = gateway
        .router()
        .oneshot(
            builder
                .body(Body::from(serde_json::to_vec(&body).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let id = response
        .headers()
        .get("x-request-id")
        .map(|v| v.to_str().unwrap().to_owned());
    let bytes = to_bytes(response.into_body(), 1_000_000).await.unwrap();
    (status, serde_json::from_slice(&bytes).unwrap(), id)
}
async fn balanced(pool: &PgPool) {
    let count:i64=sqlx::query_scalar("SELECT count(*) FROM (SELECT journal_id FROM core_billing.ledger_entries GROUP BY journal_id HAVING sum(delta_units)<>0) e")
        .fetch_one(pool).await.unwrap();
    assert_eq!(count, 0);
}

async fn stream_response(
    gateway: &ModelGateway,
    key: &str,
    path: &str,
    prompt: &str,
    id: &str,
) -> Response {
    let body = if path.ends_with("responses") {
        json!({"model":"local-model","input":prompt,"stream":true,"max_output_tokens":20})
    } else {
        json!({"model":"local-model","messages":[{"role":"user","content":prompt}],"stream":true,"stream_options":{"include_usage":true},"max_tokens":20})
    };
    gateway
        .router()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri(path)
                .header("authorization", format!("Bearer {key}"))
                .header("content-type", "application/json")
                .header("idempotency-key", id)
                .body(Body::from(serde_json::to_vec(&body).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap()
}

#[sqlx::test(migrations = false)]
async fn streaming_chat_and_responses_commit_real_evidence_then_settle_once(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    assert!(gateway.streaming_enabled());
    for (path, id) in [
        ("/v1/chat/completions", "stream-chat"),
        ("/v1/responses", "stream-responses"),
    ] {
        let response = stream_response(&gateway, &key, path, "hello", id).await;
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(response.headers()["x-accel-buffering"], "no");
        let request_id = response.headers()["x-request-id"]
            .to_str()
            .unwrap()
            .to_owned();
        let text = String::from_utf8(
            to_bytes(response.into_body(), 1_000_000)
                .await
                .unwrap()
                .to_vec(),
        )
        .unwrap();
        assert!(text.contains("local stream answer"));
        if path.ends_with("responses") {
            assert!(text.contains("response.completed"));
        } else {
            assert!(text.ends_with("data: [DONE]\n\n"));
            assert!(text.contains("\"completion_tokens\":20"));
        }
        let charge:(String,i64,i64,i64)=sqlx::query_as("SELECT state,settled,input_tokens,output_tokens FROM core_charging.requests WHERE id=$1")
            .bind(&request_id).fetch_one(&pool).await.unwrap();
        assert_eq!(charge, ("settled".into(), 49, 10, 20));
        let checkpoints:Vec<(i64,i64,Value,bool)>=sqlx::query_as("SELECT sequence,packet_bytes,provider_usage,terminal_observed FROM core_model.output_checkpoints WHERE request_id=$1 ORDER BY sequence")
            .bind(&request_id).fetch_all(&pool).await.unwrap();
        assert!(checkpoints.len() >= 4);
        for (index, row) in checkpoints.iter().enumerate() {
            assert_eq!(row.0, index as i64 + 1);
        }
        assert!(
            checkpoints
                .iter()
                .any(|row| row.1 == 0 && row.2["output_tokens"] == 20)
        );
        assert!(checkpoints.last().unwrap().3);
        let duplicate = stream_response(&gateway, &key, path, "hello", id).await;
        assert_eq!(duplicate.status(), StatusCode::CONFLICT);
    }
    assert_eq!(provider.hits.load(Ordering::SeqCst), 2);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (902, 0));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn streaming_output_has_committed_checkpoint_before_provider_finishes(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        "stream-pause",
        "incremental",
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    let mut body = response.into_body().into_data_stream();
    let first = tokio::time::timeout(Duration::from_secs(2), body.next())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
    assert!(!String::from_utf8_lossy(&first).contains("[DONE]"));
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_model.output_checkpoints")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(count >= 1);
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 140);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT settled FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
    provider.stream_gate.add_permits(1);
    let mut text = first.to_vec();
    while let Some(chunk) = body.next().await {
        text.extend(chunk.unwrap());
    }
    assert!(
        String::from_utf8(text)
            .unwrap()
            .ends_with("data: [DONE]\n\n")
    );
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn incomplete_streams_keep_holds_and_restart_never_redispatches(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    for (prompt, usage, id) in [
        ("hello", false, "stream-no-usage"),
        ("truncate", true, "stream-truncated"),
        ("malformed", true, "stream-malformed"),
    ] {
        let provider = Provider::start(usage).await;
        let running_gateway =
            gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
        let response =
            stream_response(&running_gateway, &key, "/v1/chat/completions", prompt, id).await;
        assert_eq!(response.status(), StatusCode::OK);
        let text = String::from_utf8(
            to_bytes(response.into_body(), 1_000_000)
                .await
                .unwrap()
                .to_vec(),
        )
        .unwrap();
        assert!(text.contains("error"));
        assert!(!text.contains("[DONE]"));
        assert!(!text.contains("\"finish_reason\":\"stop\""));
        let mut tx = pool.begin().await.unwrap();
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await
            .unwrap();
        sqlx::query("UPDATE core_charging.requests SET lease_until=0,revision=revision+1 WHERE state='streaming'").execute(&mut *tx).await.unwrap();
        tx.commit().await.unwrap();
        let restarted =
            gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
        assert_eq!(restarted.recover_expired(10).await.unwrap(), 1);
        assert_eq!(restarted.recover_expired(10).await.unwrap(), 0);
        assert_eq!(restarted.reconcile_completed_streams(10).await.unwrap(), 0);
        let duplicate = stream_response(&restarted, &key, "/v1/chat/completions", prompt, id).await;
        assert_eq!(duplicate.status(), StatusCode::CONFLICT);
        assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    }
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (580, 420)
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM core_charging.requests WHERE state='reconcile' AND settled=0"
        )
        .fetch_one(&pool)
        .await
        .unwrap(),
        3
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn downstream_drop_keeps_partial_stream_evidence_and_hold(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        "stream-pause",
        "disconnect",
    )
    .await;
    let mut body = response.into_body().into_data_stream();
    assert!(body.next().await.unwrap().is_ok());
    drop(body);
    assert_eq!(gateway.drain(Duration::from_secs(2)).await.remaining, 0);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (860, 140)
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM core_model.output_checkpoints WHERE terminal_observed"
        )
        .fetch_one(&pool)
        .await
        .unwrap(),
        0
    );
    assert_eq!(gateway.reconcile_completed_streams(10).await.unwrap(), 0);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    provider.stream_gate.add_permits(1);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn checkpoint_failure_never_exposes_corresponding_output(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    sqlx::raw_sql("CREATE FUNCTION public.reject_model_checkpoint() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic evidence write failure'; END $$; CREATE TRIGGER reject_model_checkpoint BEFORE INSERT ON core_model.output_checkpoints FOR EACH ROW EXECUTE FUNCTION public.reject_model_checkpoint()")
        .execute(&pool).await.unwrap();
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        "hello",
        "checkpoint-rejected",
    )
    .await;
    let text = String::from_utf8(
        to_bytes(response.into_body(), 1_000_000)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap();
    assert!(!text.contains("local stream answer"));
    assert!(!text.contains("\"role\":\"assistant\""));
    assert!(!text.contains("[DONE]"));
    assert!(text.contains("settlement_pending"));
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 140);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_model.output_checkpoints")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn malformed_tool_json_with_usage_and_done_remains_frozen(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        "malformed-tool",
        "bad-tool-json",
    )
    .await;
    assert_eq!(response.status(), StatusCode::OK);
    let text = String::from_utf8(
        to_bytes(response.into_body(), 1_000_000)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap();
    assert!(text.contains("invalid_upstream_response"));
    assert!(!text.contains("[DONE]"));
    assert!(!text.contains("\"finish_reason\":\"tool_calls\""));
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 140);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT settled FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>(
            "SELECT count(*) FROM core_model.output_checkpoints WHERE terminal_observed"
        )
        .fetch_one(&pool)
        .await
        .unwrap(),
        0
    );
    assert_eq!(gateway.reconcile_completed_streams(10).await.unwrap(), 0);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn completed_stream_recovers_after_final_transaction_failure_without_second_dispatch(
    pool: PgPool,
) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    sqlx::raw_sql("CREATE FUNCTION public.reject_stream_final() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_id='final' THEN RAISE EXCEPTION 'synthetic final commit failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_stream_final BEFORE INSERT ON core_charging.usage_events FOR EACH ROW EXECUTE FUNCTION public.reject_stream_final()")
        .execute(&pool).await.unwrap();
    let provider = Provider::start(true).await;
    let running_gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &running_gateway,
        &key,
        "/v1/chat/completions",
        "hello",
        "completed-recovery",
    )
    .await;
    let text = String::from_utf8(
        to_bytes(response.into_body(), 1_000_000)
            .await
            .unwrap()
            .to_vec(),
    )
    .unwrap();
    assert!(text.contains("local stream answer"));
    assert!(!text.contains("[DONE]"));
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 140);
    assert!(
        sqlx::query_scalar::<_, bool>(
            "SELECT bool_or(terminal_observed) FROM core_model.output_checkpoints"
        )
        .fetch_one(&pool)
        .await
        .unwrap()
    );
    sqlx::query("DROP TRIGGER reject_stream_final ON core_charging.usage_events")
        .execute(&pool)
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=0,revision=revision+1 WHERE state='streaming'").execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    let restarted =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    assert_eq!(restarted.recover_expired(10).await.unwrap(), 1);
    let (one, two) = tokio::join!(
        restarted.reconcile_completed_streams(10),
        restarted.reconcile_completed_streams(10)
    );
    assert!(one.unwrap() + two.unwrap() >= 1);
    assert_eq!(restarted.reconcile_completed_streams(10).await.unwrap(), 0);
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 0);
    let duplicate = stream_response(
        &restarted,
        &key,
        "/v1/chat/completions",
        "hello",
        "completed-recovery",
    )
    .await;
    assert_eq!(duplicate.status(), StatusCode::CONFLICT);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn output_checkpoint_exact_replay_conflict_worker_binding_and_immutability(pool: PgPool) {
    let (_, _, _, key, _, _) = fixture(&pool, 1000).await;
    let worker = [42_u8; 32];
    let request_id = "model-checkpoint-capability-test";
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    let spec = ChargingRequest {
        id: request_id.into(),
        fingerprint: [17; 32],
        price: ChargingPrice {
            upstream_cost_group_ppm: None,
            version: "synthetic-checkpoint-v1".into(),
            model: "local-model".into(),
            group: "allowed".into(),
            input_per_million: 1_000_000,
            cached_per_million: 250_000,
            output_per_million: 2_000_000,
        },
        reserve: 140,
        lease_seconds: 30,
        maximum_seconds: 30,
    };
    charging.reserve(&key, &spec, &worker).await.unwrap();
    assert!(charging.start(request_id, &worker).await.unwrap());
    let provider_usage = json!({"input_tokens":10,"cached_input_tokens":2,"output_tokens":20});
    let packet_digest = Sha256::digest(b"synthetic prepared packet").to_vec();
    let mut prepare = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *prepare)
        .await
        .unwrap();
    sqlx::query("SELECT core_model.checkpoint_output($1,$2,1,$3,25,$4,true)")
        .bind(request_id)
        .bind(worker.as_slice())
        .bind(&packet_digest)
        .bind(&provider_usage)
        .execute(&mut *prepare)
        .await
        .unwrap();
    prepare.commit().await.unwrap();
    charging
        .settle(
            request_id,
            &worker,
            ChargingUsage {
                upstream_cost_atto_usd: None,
                input: 10,
                cached: 2,
                output: 20,
            },
            ChargingOutcome::Completed,
        )
        .await
        .unwrap();
    let stored_digest: Vec<u8> =
        sqlx::query_scalar("SELECT worker_digest FROM core_charging.requests WHERE id=$1")
            .bind(request_id)
            .fetch_one(&pool)
            .await
            .unwrap();
    let row:(Vec<u8>,i64,Value,bool)=sqlx::query_as("SELECT packet_sha256,packet_bytes,provider_usage,terminal_observed FROM core_model.output_checkpoints WHERE request_id=$1 AND sequence=1")
        .bind(request_id).fetch_one(&pool).await.unwrap();
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM core_model.output_checkpoints")
        .fetch_one(&pool)
        .await
        .unwrap();
    for mode in 0..4 {
        let mut tx = pool.begin().await.unwrap();
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await
            .unwrap();
        let mut digest = row.0.clone();
        if mode == 1 {
            digest[0] ^= 1;
        }
        let capability = match mode {
            2 => stored_digest.clone(),
            3 => vec![0u8; 32],
            _ => worker.to_vec(),
        };
        let result = sqlx::query("SELECT core_model.checkpoint_output($1,$2,1,$3,$4,$5,$6)")
            .bind(request_id)
            .bind(capability)
            .bind(digest)
            .bind(row.1)
            .bind(&row.2)
            .bind(row.3)
            .execute(&mut *tx)
            .await;
        if mode == 0 {
            result.unwrap();
            tx.commit().await.unwrap();
        } else {
            let error = result.unwrap_err();
            assert_eq!(
                error.as_database_error().unwrap().code().as_deref(),
                Some(if mode == 1 { "23505" } else { "42501" })
            );
            tx.rollback().await.unwrap();
        }
    }
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_model.output_checkpoints")
            .fetch_one(&pool)
            .await
            .unwrap(),
        before
    );
    for query in [
        "DELETE FROM core_model.output_checkpoints",
        "UPDATE core_model.output_checkpoints SET packet_bytes=0",
        "TRUNCATE core_model.output_checkpoints",
    ] {
        assert!(sqlx::query(query).execute(&pool).await.is_err());
    }
    let public_exec:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace CROSS JOIN LATERAL aclexplode(p.proacl) a WHERE n.nspname='core_model' AND a.grantee=0 AND a.privilege_type='EXECUTE')")
        .fetch_one(&pool).await.unwrap();
    assert!(!public_exec);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn input_budget_refuses_before_reserve_and_supplier_429_keeps_unknown_hold(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway =
        gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt).with_local_streaming();
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        &"x".repeat(101),
        "large-prompt",
    )
    .await;
    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 0);
    let response = stream_response(
        &gateway,
        &key,
        "/v1/chat/completions",
        "rate-limit",
        "upstream-429",
    )
    .await;
    assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 140);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn dashboard_key_without_host_mapping_obeys_real_peer_policy_and_ledger(pool: PgPool) {
    let (identity, ledger, session, _, native_key_id, account) = fixture(&pool, 1000).await;
    // The explicit offline bootstrap already installs this synthetic grant.
    // Assert the prerequisite instead of duplicating its primary key.
    let developer: bool =
        sqlx::query_scalar("SELECT granted FROM core_identity.developer_access WHERE user_id=7")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(developer);
    sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,enabled,source_reference) VALUES (7,'allowed',true,'synthetic:model-key-test')").execute(&pool).await.unwrap();
    let provider = Provider::start(true).await;
    let router = routes(&provider.endpoint, 0, 0, PayloadPolicy::Adapt);
    let identity = identity.with_dashboard_routing(router.clone());
    let input:DashboardKeyInput=serde_json::from_value(json!({"name":"synthetic dashboard model key","expired_time":-1,"remain_quota":1000,"unlimited_quota":false,"group":"allowed","one_time_reveal":true,"model_limits_enabled":true,"model_limits":"local-model","allow_ips":"127.0.0.1/32"})).unwrap();
    let issued = identity
        .dashboard_create_key(&session, &input)
        .await
        .unwrap();
    let key = issued["key"].as_str().unwrap();
    let key_id = issued["id"].as_i64().unwrap();
    assert_ne!(key_id, native_key_id);
    let gateway = gateway_with_routes(&pool, router, native_key_id);
    let request = || {
        Request::builder()
            .method("POST")
            .uri("/v1/chat/completions")
            .header("authorization", format!("Bearer {key}"))
            .header("content-type", "application/json")
            .header("x-forwarded-for", "127.0.0.1")
    };
    let refused = gateway
        .router()
        .oneshot(
            request()
                .body(Body::from(serde_json::to_vec(&chat()).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(refused.status(), StatusCode::FORBIDDEN);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    let response = gateway
        .router()
        .oneshot(
            request()
                .extension(axum::extract::ConnectInfo(std::net::SocketAddr::from((
                    [127, 0, 0, 1],
                    45678,
                ))))
                .body(Body::from(serde_json::to_vec(&chat()).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let value: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 1_000_000).await.unwrap()).unwrap();
    assert_eq!(
        value["choices"][0]["message"]["content"],
        "local synthetic answer"
    );
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    let used: i64 = sqlx::query_scalar(
        "SELECT sum(settled)::bigint FROM core_charging.requests WHERE key_id=$1",
    )
    .bind(key_id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(used, 49);
    let disable: DashboardKeyInput =
        serde_json::from_value(json!({"id":key_id,"status":2})).unwrap();
    identity
        .dashboard_update_key(&session, &disable, true)
        .await
        .unwrap();
    let disabled = gateway
        .router()
        .oneshot(
            request()
                .extension(axum::extract::ConnectInfo(std::net::SocketAddr::from((
                    [127, 0, 0, 1],
                    45678,
                ))))
                .body(Body::from(serde_json::to_vec(&chat()).unwrap()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert!(matches!(
        disabled.status(),
        StatusCode::FORBIDDEN | StatusCode::UNAUTHORIZED
    ));
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn chat_responses_catalog_and_real_charging(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let (status, list) = call(&gateway, Some(&key), "/v1/models", json!(null), None).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(list["data"][0]["id"], "local-model");
    let (status, answer) = call(
        &gateway,
        Some(&key),
        "/v1/chat/completions",
        chat(),
        Some("chat-one"),
    )
    .await;
    assert_eq!(status, StatusCode::OK, "{answer}");
    assert_eq!(
        answer["choices"][0]["message"]["content"],
        "local synthetic answer"
    );
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    let (status, answer) = call(
        &gateway,
        Some(&key),
        "/v1/responses",
        json!({"model":"local-model","input":"hello","max_output_tokens":20}),
        Some("response-one"),
    )
    .await;
    assert_eq!(status, StatusCode::OK, "{answer}");
    assert_eq!(answer["object"], "response");
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (902, 0));
    assert_eq!(provider.hits.load(Ordering::SeqCst), 2);
    assert!(
        provider
            .auth
            .lock()
            .unwrap()
            .iter()
            .all(|v| v == "Bearer synthetic-upstream-key")
    );
    let charges: Vec<(String, i64)> =
        sqlx::query_as("SELECT state,settled FROM core_charging.requests ORDER BY id")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert_eq!(
        charges,
        vec![("settled".into(), 49), ("settled".into(), 49)]
    );
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn authorization_group_and_insufficient_funds_reject_before_dispatch(pool: PgPool) {
    let (identity, ledger, session, key, key_id, account) = fixture(&pool, 0).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    assert_eq!(
        call(&gateway, None, "/v1/chat/completions", chat(), None)
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        call(
            &gateway,
            Some(&session),
            "/v1/chat/completions",
            chat(),
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let blocked = identity
        .issue_key(
            &session,
            Account {
                kind: AccountKind::Personal,
                id: 7,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    assert_eq!(
        call(
            &gateway,
            Some(&blocked.secret),
            "/v1/models",
            json!(null),
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let (status, error) = call(&gateway, Some(&key), "/v1/chat/completions", chat(), None).await;
    assert_eq!(status, StatusCode::PAYMENT_REQUIRED, "{error}");
    assert_eq!(error["error"]["code"], "insufficient_funds");
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    assert_eq!(ledger.balance(account).await.unwrap().reserved_units, 0);
    for duplicate in ["authorization", "idempotency-key"] {
        let mut request = Request::builder()
            .method("POST")
            .uri("/v1/chat/completions")
            .header("authorization", format!("Bearer {key}"));
        request = if duplicate == "authorization" {
            request.header("authorization", format!("Bearer {key}"))
        } else {
            request
                .header("idempotency-key", "one")
                .header("idempotency-key", "two")
        };
        let response = gateway
            .router()
            .oneshot(
                request
                    .body(Body::from(serde_json::to_vec(&chat()).unwrap()))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    }
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
}

#[sqlx::test(migrations = false)]
async fn idempotency_survives_gateway_reconstruction_and_changed_body(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let first = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    assert_eq!(
        call(
            &first,
            Some(&key),
            "/v1/chat/completions",
            chat(),
            Some("durable-one")
        )
        .await
        .0,
        StatusCode::OK
    );
    let second = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let mut changed = chat();
    changed["messages"][0]["content"] = json!("changed");
    assert_eq!(
        call(
            &second,
            Some(&key),
            "/v1/chat/completions",
            changed,
            Some("durable-one")
        )
        .await
        .0,
        StatusCode::CONFLICT
    );
    assert_eq!(
        call(
            &second,
            Some(&key),
            "/v1/chat/completions",
            chat(),
            Some("durable-one")
        )
        .await
        .0,
        StatusCode::CONFLICT
    );
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn unsupported_price_payload_and_stream_reject_without_funds_or_provider_use(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    for (cache_write, request_fee, payload) in [
        (1, 0, PayloadPolicy::Adapt),
        (0, 1, PayloadPolicy::Adapt),
        (0, 0, PayloadPolicy::Passthrough),
    ] {
        let gateway = gateway(&pool, &provider, key_id, cache_write, request_fee, payload);
        assert_ne!(
            call(&gateway, Some(&key), "/v1/chat/completions", chat(), None)
                .await
                .0,
            StatusCode::OK
        );
    }
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let mut body = chat();
    body["stream"] = json!(true);
    assert_eq!(
        call(&gateway, Some(&key), "/v1/chat/completions", body, None)
            .await
            .0,
        StatusCode::BAD_REQUEST
    );
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (1000, 0));
}

#[sqlx::test(migrations = false)]
async fn unknown_usage_retains_hold_and_recovery_does_not_redispatch(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(false).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let (status, error, id) = call_with_request_id(
        &gateway,
        Some(&key),
        "/v1/chat/completions",
        chat(),
        Some("unknown-usage"),
    )
    .await;
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE, "{error}");
    assert_eq!(id.as_ref().unwrap().len(), 64);
    assert_eq!(error["error"]["code"], "settlement_pending");
    assert!(error.get("choices").is_none());
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (860, 140)
    );
    let mut expiry = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *expiry)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=0,revision=revision+1")
        .execute(&mut *expiry)
        .await
        .unwrap();
    expiry.commit().await.unwrap();
    assert_eq!(gateway.recover_expired(10).await.unwrap(), 1);
    assert_eq!(gateway.recover_expired(10).await.unwrap(), 0);
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT state FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        "reconcile"
    );
    assert_eq!(
        call(
            &gateway,
            Some(&key),
            "/v1/chat/completions",
            chat(),
            Some("unknown-usage")
        )
        .await
        .0,
        StatusCode::CONFLICT
    );
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (860, 140)
    );
    balanced(&pool).await;
}

#[test]
fn routing_and_http_control_ambiguity_fail_closed() {
    for endpoint in [
        "http://localhost:1/v1",
        "http://192.168.1.1/v1",
        "http://127.0.0.1.evil/v1",
        "https://127.0.0.1/v1",
        "http://u@127.0.0.1/v1",
        "http://127.0.0.1/v1?next=x",
    ] {
        assert!(
            Endpoint::local_loopback(endpoint.into()).is_err(),
            "{endpoint}"
        );
    }
    assert!(Endpoint::local_loopback("http://127.0.0.1:123/v1".into()).is_ok());
    assert!(Endpoint::local_loopback("http://[::1]:123/v1".into()).is_ok());
    assert!(serde_json::from_str::<Endpoint>("\"http://127.0.0.1/v1\"").is_err());
    for body in [
        r#"{"model":"local-model","model":"other","messages":[]}"#,
        r#"{"model":"local-model","MODEL":"other","messages":[]}"#,
        r#"{"model":"local-model","messages":[],"stream":false,"stream":true}"#,
    ] {
        assert!(
            RelayRequest::parse(ClientProtocol::Chat, body.as_bytes(), &Limits::default()).is_err()
        );
    }
}

#[sqlx::test(migrations = false)]
async fn concurrent_idempotency_and_external_routes_never_duplicate_spend(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let (first, second) = tokio::join!(
        call(
            &gateway,
            Some(&key),
            "/v1/chat/completions",
            chat(),
            Some("concurrent-one")
        ),
        call(
            &gateway,
            Some(&key),
            "/v1/chat/completions",
            chat(),
            Some("concurrent-one")
        )
    );
    assert!(
        (first.0 == StatusCode::OK && second.0 == StatusCode::CONFLICT)
            || (second.0 == StatusCode::OK && first.0 == StatusCode::CONFLICT)
    );
    let external = gateway_with_routes(
        &pool,
        routes(
            "https://external-provider.invalid/v1",
            0,
            0,
            PayloadPolicy::Adapt,
        ),
        key_id,
    );
    let (status, error) = call(&external, Some(&key), "/v1/chat/completions", chat(), None).await;
    assert_eq!(status, StatusCode::BAD_REQUEST, "{error}");
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    assert_eq!(ledger.balance(account).await.unwrap().available_units, 951);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn route_disabled_during_dispatch_claim_is_checked_again_before_send(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let router = routes(&provider.endpoint, 0, 0, PayloadPolicy::Adapt);
    let gateway = gateway_with_routes(&pool, router.clone(), key_id);
    sqlx::raw_sql("CREATE FUNCTION public.pause_model_start() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF OLD.state='reserved' AND NEW.state='streaming' THEN PERFORM pg_advisory_xact_lock(812991); END IF; RETURN NEW; END $$; CREATE TRIGGER pause_model_start BEFORE UPDATE ON core_charging.requests FOR EACH ROW EXECUTE FUNCTION public.pause_model_start()")
        .execute(&pool).await.unwrap();
    let mut blocker = pool.acquire().await.unwrap();
    sqlx::query("SELECT pg_advisory_lock(812991)")
        .execute(&mut *blocker)
        .await
        .unwrap();
    let request_gateway = gateway.clone();
    let request_key = key.clone();
    let task = tokio::spawn(async move {
        call(
            &request_gateway,
            Some(&request_key),
            "/v1/chat/completions",
            chat(),
            None,
        )
        .await
    });
    tokio::time::timeout(Duration::from_secs(2),async {
        loop {
            let waiting:bool=sqlx::query_scalar("SELECT EXISTS(SELECT FROM pg_locks WHERE locktype='advisory' AND objid=812991 AND NOT granted)")
                .fetch_one(&pool).await.unwrap();
            if waiting {break;}
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    }).await.unwrap();
    router
        .set_operational_enabled(router.version().unwrap(), 1, false)
        .unwrap();
    sqlx::query("SELECT pg_advisory_unlock(812991)")
        .execute(&mut *blocker)
        .await
        .unwrap();
    let (status, error) = task.await.unwrap();
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE, "{error}");
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (1000, 0));
    let charge: (String, i64) = sqlx::query_as("SELECT state,settled FROM core_charging.requests")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(charge, ("settled".into(), 0));
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn cancelled_waiting_client_retains_durable_provider_risk(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let request_gateway = gateway.clone();
    let request_key = key.clone();
    let task = tokio::spawn(async move {
        let mut body = chat();
        body["messages"][0]["content"] = json!("pause");
        call(
            &request_gateway,
            Some(&request_key),
            "/v1/chat/completions",
            body,
            Some("cancelled-one"),
        )
        .await
    });
    tokio::time::timeout(Duration::from_secs(2), async {
        while provider.hits.load(Ordering::SeqCst) == 0 {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    task.abort();
    assert!(task.await.unwrap_err().is_cancelled());
    assert_eq!(gateway.drain(Duration::from_secs(2)).await.remaining, 0);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (860, 140)
    );
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.requests SET lease_until=0,revision=revision+1")
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    assert_eq!(gateway.recover_expired(10).await.unwrap(), 1);
    assert_eq!(
        sqlx::query_scalar::<_, String>("SELECT state FROM core_charging.requests")
            .fetch_one(&pool)
            .await
            .unwrap(),
        "reconcile"
    );
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    balanced(&pool).await;
}

#[sqlx::test(migrations = false)]
async fn failed_final_transaction_never_exposes_success_or_releases_hold(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    sqlx::raw_sql("CREATE FUNCTION public.reject_model_final() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.event_id='final' THEN RAISE EXCEPTION 'synthetic finalization failure'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_model_final BEFORE INSERT ON core_charging.usage_events FOR EACH ROW EXECUTE FUNCTION public.reject_model_final()")
        .execute(&pool).await.unwrap();
    let (status, error, id) = call_with_request_id(
        &gateway,
        Some(&key),
        "/v1/chat/completions",
        chat(),
        Some("failed-final"),
    )
    .await;
    assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE, "{error}");
    assert!(error.get("choices").is_none());
    assert!(id.is_some());
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (860, 140)
    );
    let captures: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.ledger_journals WHERE kind='capture'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(captures, 0);
    assert_eq!(provider.hits.load(Ordering::SeqCst), 1);
    balanced(&pool).await;
}

#[path = "model_http_upstream_cost/mod.rs"]
mod upstream_cost_models;
