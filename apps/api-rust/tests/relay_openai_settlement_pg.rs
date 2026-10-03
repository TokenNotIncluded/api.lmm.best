//! Real PostgreSQL plus real HTTP provider coverage of the response/accounting
//! boundary. Run with tests/scripts/with-local-services.py and --include-ignored.

use std::{
    collections::VecDeque,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};

use axum::{
    Router,
    body::{Body, Bytes, to_bytes},
    extract::State,
    http::{Request, StatusCode, header},
    response::Response,
};
use futures_util::{FutureExt, StreamExt, stream};
use lmm_api_rs::{
    relay_http::{RelayHttpClient, RelayTimeoutConfig},
    routes::relay_openai::{
        OpenAiRelayHttpState, OpenAiUpstreamClient, PgOpenAiRelayService, RelayReconcilePolicy,
        RelaySettlementTracker, openai_relay_router,
    },
};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use tokio::{
    net::TcpListener,
    sync::{Mutex, mpsc, watch},
    task::JoinHandle,
    time::{sleep, timeout},
};
use tower::ServiceExt;

type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;
type WireItem = Result<Bytes, std::io::Error>;

fn price_lifecycle_vectors() -> TestResult<Vec<Value>> {
    let raw = if let Ok(path) = std::env::var("LMM_RELAY_PRICE_GO_VECTORS") {
        std::fs::read_to_string(path)?
    } else {
        include_str!("behavior-oracle/fixtures/relay-price-lifecycle.json").to_owned()
    };
    let cases: Vec<Value> = serde_json::from_str(&raw)?;
    assert_eq!(cases.len(), 32);
    Ok(cases)
}

fn response_usage(case: &Value) -> Value {
    json!({"input_tokens":case["usage"]["prompt_tokens"],"output_tokens":case["usage"]["completion_tokens"],
        "input_tokens_details":case["usage"]["prompt_tokens_details"]})
}

#[derive(Clone)]
struct Provider {
    receivers: Arc<Mutex<VecDeque<mpsc::Receiver<WireItem>>>>,
    calls: Arc<AtomicUsize>,
    streaming: bool,
}

async fn provider(State(state): State<Provider>) -> Response {
    state.calls.fetch_add(1, Ordering::SeqCst);
    let Some(mut receiver) = state.receivers.lock().await.pop_front() else {
        return Response::builder().status(500).body(Body::empty()).unwrap();
    };
    let body = if state.streaming {
        Body::from_stream(stream::unfold(receiver, |mut receiver| async move {
            receiver.recv().await.map(|item| (item, receiver))
        }))
    } else {
        Body::from_stream(stream::once(async move {
            receiver.recv().await.unwrap_or_else(|| Ok(Bytes::new()))
        }))
    };
    Response::builder()
        .header(
            header::CONTENT_TYPE,
            if state.streaming {
                "text/event-stream"
            } else {
                "application/json"
            },
        )
        .body(body)
        .unwrap()
}

struct Fixture {
    admin: PgPool,
    pg: PgPool,
    schema: String,
    app: Router,
    wire: mpsc::Sender<WireItem>,
    calls: Arc<AtomicUsize>,
    server: JoinHandle<()>,
    tracker: RelaySettlementTracker,
    receivers: Arc<Mutex<VecDeque<mpsc::Receiver<WireItem>>>>,
    service: PgOpenAiRelayService,
}

impl Fixture {
    async fn new(streaming: bool) -> TestResult<Self> {
        let database_url = std::env::var("LMM_TEST_DATABASE_URL")?;
        let admin = PgPool::connect(&database_url).await?;
        let schema = format!("relay_settlement_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;
        let pg = PgPoolOptions::new()
            .max_connections(4)
            .after_connect({
                let schema = schema.clone();
                move |connection, _| {
                    let sql = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&sql).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&database_url)
            .await?;
        for sql in [
            "CREATE TABLE users (id BIGINT PRIMARY KEY, status BIGINT, quota BIGINT, role BIGINT, setting TEXT NOT NULL DEFAULT '', deleted_at TIMESTAMPTZ, used_quota BIGINT DEFAULT 0, request_count BIGINT DEFAULT 0)",
            "CREATE TABLE tokens (id BIGINT PRIMARY KEY, user_id BIGINT, status BIGINT, expired_time BIGINT, remain_quota BIGINT, unlimited_quota BOOLEAN, allow_ips TEXT, key TEXT, \"group\" TEXT, deleted_at TIMESTAMPTZ, accessed_time BIGINT DEFAULT 0, used_quota BIGINT DEFAULT 0)",
            "CREATE TABLE channels (id BIGINT PRIMARY KEY, status BIGINT, base_url TEXT, key TEXT, used_quota BIGINT DEFAULT 0)",
            "CREATE TABLE abilities (\"group\" TEXT, model TEXT, channel_id BIGINT, enabled BOOLEAN, priority BIGINT, weight BIGINT)",
            "CREATE TABLE logs (user_id BIGINT, created_at BIGINT, type BIGINT, content TEXT, model_name TEXT, quota BIGINT, channel_id BIGINT, token_id BIGINT, \"group\" TEXT, request_id TEXT, is_stream BOOLEAN, prompt_tokens BIGINT, completion_tokens BIGINT, other TEXT)",
            "CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT)",
            "INSERT INTO users (id,status,quota,role) VALUES (1,1,10000,1)",
            "INSERT INTO tokens (id,user_id,status,expired_time,remain_quota,unlimited_quota,allow_ips,key,\"group\") VALUES (11,1,1,-1,10000,FALSE,'','tenant','default')",
            "INSERT INTO abilities (\"group\",model,channel_id,enabled,priority,weight) VALUES ('default','priced-model',1,TRUE,100,100)",
            "INSERT INTO options (key,value) VALUES ('ModelRatio','{\"priced-model\":2}'),('CompletionRatio','{\"priced-model\":3}'),('GroupRatio','{\"default\":0.5}'),('PreConsumedQuota','10')",
        ] {
            sqlx::query(sql).execute(&pg).await?;
        }
        sqlx::raw_sql(include_str!(
            "behavior-oracle/fixtures/relay_subscription_schema.sql"
        ))
        .execute(&pg)
        .await?;
        sqlx::raw_sql(
            &include_str!("../migrations/0014_relay_settlement.sql")
                .replace("__LMM_APP_SCHEMA__", &schema),
        )
        .execute(&pg)
        .await?;
        if let Ok(directory) = std::env::var("LMM_LOCAL_TEST_ARTIFACTS") {
            let constraints:Vec<(String,String)>=sqlx::query_as("SELECT conname,pg_get_constraintdef(oid) FROM pg_constraint WHERE conrelid='relay_settlement_records'::regclass ORDER BY conname")
                .fetch_all(&pg).await?;
            let directory = std::path::Path::new(&directory);
            let temporary = directory.join(format!("relay-settlement-constraints-{schema}.json"));
            std::fs::write(&temporary, serde_json::to_vec_pretty(&constraints)?)?;
            std::fs::rename(
                temporary,
                directory.join("relay-settlement-constraints.json"),
            )?;
        }
        let (wire, receiver) = mpsc::channel(8);
        let receivers = Arc::new(Mutex::new(VecDeque::from([receiver])));
        let calls = Arc::new(AtomicUsize::new(0));
        let listener = TcpListener::bind("127.0.0.1:0").await?;
        let base_url = format!("http://{}", listener.local_addr()?);
        let state = Provider {
            receivers: Arc::clone(&receivers),
            calls: Arc::clone(&calls),
            streaming,
        };
        let server = tokio::spawn(async move {
            let _ = axum::serve(listener, Router::new().fallback(provider).with_state(state)).await;
        });
        sqlx::query(
            "INSERT INTO channels (id,status,base_url,key) VALUES (1,1,$1,'upstream-secret')",
        )
        .bind(base_url)
        .execute(&pg)
        .await?;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_secs(3),
            total: None,
        })?
        .with_openai_first_output_timeout(None);
        let service = PgOpenAiRelayService::new(pg.clone(), OpenAiUpstreamClient::new(client), 1)
            .with_option_pricing();
        let tracker = service.settlement_tracker();
        let app = openai_relay_router(OpenAiRelayHttpState::new(
            Arc::new(service.clone()),
            "settlement-test",
        ));
        Ok(Self {
            admin,
            pg,
            schema,
            app,
            wire,
            calls,
            server,
            tracker,
            receivers,
            service,
        })
    }

    async fn request(&self, request_id: &str, streaming: bool) -> TestResult<Response> {
        self.request_to(request_id, streaming, "/v1/responses")
            .await
    }

    async fn queue_turn(&self) -> mpsc::Sender<WireItem> {
        let (sender, receiver) = mpsc::channel(8);
        self.receivers.lock().await.push_back(receiver);
        sender
    }

    fn use_cache(&mut self, client: redis::Client, secret: &str, deadline: Duration) {
        self.service = self.service.clone().with_valkey(client, secret, deadline);
        self.app = openai_relay_router(OpenAiRelayHttpState::new(
            Arc::new(self.service.clone()),
            "settlement-test",
        ));
    }

    fn first_output_deadline(&mut self, duration: Duration) -> TestResult {
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_secs(3),
            total: None,
        })?
        .with_openai_first_output_timeout(Some(duration));
        self.service =
            PgOpenAiRelayService::new(self.pg.clone(), OpenAiUpstreamClient::new(client), 1)
                .with_option_pricing();
        self.tracker = self.service.settlement_tracker();
        self.app = openai_relay_router(OpenAiRelayHttpState::new(
            Arc::new(self.service.clone()),
            "settlement-test",
        ));
        Ok(())
    }

    async fn set_options(&self, values: &Value) -> TestResult {
        for (key, value) in values.as_object().expect("oracle options") {
            sqlx::query("INSERT INTO options(key,value) VALUES($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
                .bind(key).bind(value.as_str().expect("raw Go option")).execute(&self.pg).await?;
        }
        Ok(())
    }

    async fn wallet_state(&self) -> TestResult<Value> {
        let (wallet,token,used):(i64,i64,i64)=sqlx::query_as("SELECT u.quota,t.remain_quota,t.used_quota FROM users u JOIN tokens t ON t.user_id=u.id WHERE u.id=1 AND t.id=11")
            .fetch_one(&self.pg).await?;
        Ok(json!({"wallet":wallet,"token":token,"used":used}))
    }

    async fn price_case(&self, case: &Value) -> TestResult {
        self.set_options(&case["options"]).await?;
        sqlx::query("UPDATE users SET quota=$1 WHERE id=1")
            .bind(case["wallet"].as_i64().unwrap())
            .execute(&self.pg)
            .await?;
        sqlx::query("UPDATE tokens SET remain_quota=1000000 WHERE id=11")
            .execute(&self.pg)
            .await?;
        Ok(())
    }

    async fn failed_settlement(&self, request_id: &str) -> TestResult {
        sqlx::query("ALTER TABLE logs ADD CONSTRAINT reject_finalization CHECK(type<>2)")
            .execute(&self.pg)
            .await?;
        let response = self.request(request_id, true).await?;
        self.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":0}}})).await?;
        to_bytes(response.into_body(), 65536).await?;
        sqlx::query("ALTER TABLE logs DROP CONSTRAINT reject_finalization")
            .execute(&self.pg)
            .await?;
        Ok(())
    }

    async fn subscription(&self, total: i64, used: i64, overflow: bool) -> TestResult {
        sqlx::query("INSERT INTO subscription_plans(id) VALUES(1) ON CONFLICT DO NOTHING")
            .execute(&self.pg)
            .await?;
        sqlx::query("INSERT INTO user_subscriptions(id,user_id,plan_id,amount_total,amount_used,start_time,end_time,allow_wallet_overflow) VALUES(1,1,1,$1,$2,EXTRACT(EPOCH FROM NOW())::BIGINT,EXTRACT(EPOCH FROM NOW())::BIGINT+86400,$3)")
            .bind(total).bind(used).bind(overflow).execute(&self.pg).await?;
        Ok(())
    }

    async fn preference(&self, preference: &str) -> TestResult {
        sqlx::query("UPDATE users SET setting=$1 WHERE id=1")
            .bind(json!({"billing_preference":preference}).to_string())
            .execute(&self.pg)
            .await?;
        Ok(())
    }

    async fn sub_usage(&self) -> TestResult<(i64, i64)> {
        Ok(
            sqlx::query_as("SELECT amount_used,quota_version FROM user_subscriptions WHERE id=1")
                .fetch_one(&self.pg)
                .await?,
        )
    }

    async fn request_to(
        &self,
        request_id: &str,
        streaming: bool,
        path: &str,
    ) -> TestResult<Response> {
        Ok(timeout(Duration::from_secs(5), self.app.clone().oneshot(Request::builder()
            .method("POST").uri(path).header(header::AUTHORIZATION, "Bearer sk-tenant")
            .header(header::CONTENT_TYPE, "application/json").header("x-oneapi-request-id", request_id)
            .body(Body::from(json!({"model":"priced-model","input":"hello","messages":[{"role":"user","content":"hello"}],"stream":streaming}).to_string()))?)).await??)
    }

    async fn event(&self, value: Value) -> TestResult {
        self.wire
            .send(Ok(Bytes::from(format!("data: {value}\n\n"))))
            .await?;
        Ok(())
    }

    async fn balance(&self) -> TestResult<(i64, i64, i64)> {
        Ok(
            sqlx::query_as("SELECT quota,used_quota,request_count FROM users WHERE id=1")
                .fetch_one(&self.pg)
                .await?,
        )
    }

    async fn settled(&self, quota: i64, count: i64) -> TestResult {
        timeout(Duration::from_secs(3), async {
            loop {
                let pending: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs WHERE type=0")
                    .fetch_one(&self.pg)
                    .await?;
                if pending == 0 {
                    return Ok::<(), sqlx::Error>(());
                }
                sleep(Duration::from_millis(10)).await;
            }
        })
        .await??;
        assert_eq!(self.balance().await?, (10000 - quota, quota, count));
        let token: (i64, i64) =
            sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
                .fetch_one(&self.pg)
                .await?;
        assert_eq!(token, (10000 - quota, quota));
        let channel: i64 = sqlx::query_scalar("SELECT used_quota FROM channels WHERE id=1")
            .fetch_one(&self.pg)
            .await?;
        assert_eq!(channel, quota);
        Ok(())
    }

    async fn cleanup(self) -> TestResult {
        self.server.abort();
        self.pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await?;
        self.admin.close().await;
        Ok(())
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn streaming_usage_settles_after_terminal_with_frozen_price_and_real_counts() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("terminal-snapshot", true).await?;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        fixture.balance().await?,
        (9990, 0, 0),
        "headers only reserve; they are not a success"
    );
    let successes: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs WHERE type=2")
        .fetch_one(&fixture.pg)
        .await?;
    assert_eq!(successes, 0);
    sqlx::query("UPDATE options SET value='{\"priced-model\":20}' WHERE key='ModelRatio'")
        .execute(&fixture.pg)
        .await?;
    fixture.event(json!({"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":100,"output_tokens":50}}})).await?;
    let body = timeout(
        Duration::from_secs(3),
        to_bytes(response.into_body(), 65536),
    )
    .await??;
    assert!(String::from_utf8_lossy(&body).contains("response.completed"));
    fixture.settled(250, 1).await?;
    let log: (i64, i64, i64) =
        sqlx::query_as("SELECT quota,prompt_tokens,completion_tokens FROM logs WHERE type=2")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(log, (250, 100, 50));
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn failed_terminal_without_usage_refunds_without_a_success_log() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("empty-failure", true).await?;
    fixture.event(json!({"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":0,"output_tokens":0}}})).await?;
    let body = to_bytes(response.into_body(), 65536).await?;
    assert!(String::from_utf8_lossy(&body).contains("response.failed"));
    fixture.settled(0, 0).await?;
    let logs: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs")
        .fetch_one(&fixture.pg)
        .await?;
    assert_eq!(logs, 0);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn responses_missing_usage_output_and_reported_zero_settle_over_real_http() -> TestResult {
    let output =
        json!([{"type":"message","content":[{"type":"output_text","text":"hello world"}]}]);
    let cases = [
        (
            "terminal-text",
            true,
            vec![
                json!({"type":"response.completed","response":{"status":"completed","output":output}}),
            ],
            2,
            true,
        ),
        (
            "terminal-refusal",
            true,
            vec![
                json!({"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"refusal","refusal":"hello world"}]}]}}),
            ],
            2,
            true,
        ),
        (
            "terminal-function",
            true,
            vec![
                json!({"type":"response.completed","response":{"status":"completed","output":[{"type":"function_call","arguments":"hello world"}]}}),
            ],
            2,
            true,
        ),
        (
            "terminal-reasoning",
            true,
            vec![
                json!({"type":"response.done","response":{"status":"completed","output":[{"type":"reasoning","summary":[{"type":"summary_text","text":"hello world"}]}]}}),
            ],
            2,
            true,
        ),
        (
            "delta-no-duplicate",
            true,
            vec![
                json!({"type":"response.output_text.delta","delta":"hello world"}),
                json!({"type":"response.completed","response":{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"hello world plus terminal snapshot"}]}]}}),
            ],
            2,
            true,
        ),
        (
            "stream-reported-zero",
            true,
            vec![
                json!({"type":"response.completed","response":{"status":"completed","output":output,"usage":{"input_tokens":0,"output_tokens":0}}}),
            ],
            0,
            true,
        ),
        (
            "json-reported-zero",
            false,
            vec![
                json!({"status":"completed","output":output,"usage":{"input_tokens":0,"output_tokens":0}}),
            ],
            0,
            true,
        ),
        (
            "failed-terminal-only",
            true,
            vec![json!({"type":"response.failed","response":{"status":"failed","output":output}})],
            0,
            false,
        ),
        (
            "nested-error-output",
            true,
            vec![
                json!({"type":"response.completed","response":{"status":"completed","error":{"code":"upstream_failed"},"output":output}}),
            ],
            0,
            false,
        ),
        (
            "flat-error-before-usage",
            true,
            vec![
                json!({"type":"error","code":"upstream_failed"}),
                json!({"type":"response.completed","response":{"usage":{"input_tokens":100,"output_tokens":50}}}),
            ],
            0,
            false,
        ),
    ];
    for (name, streaming, events, expected_output, completed) in cases {
        let fixture = Fixture::new(streaming).await?;
        let outcome = std::panic::AssertUnwindSafe(async {
            let response = fixture.request(name, streaming).await?;
            for event in events {
                if streaming {
                    fixture.event(event).await?;
                } else {
                    fixture
                        .wire
                        .send(Ok(Bytes::from(event.to_string())))
                        .await?;
                }
            }
            let body = timeout(
                Duration::from_secs(3),
                to_bytes(response.into_body(), 65536),
            )
            .await??;
            if name == "flat-error-before-usage" {
                assert!(!String::from_utf8_lossy(&body).contains("response.completed"));
                assert!(!String::from_utf8_lossy(&body).contains("upstream_stream_interrupted"));
            }
            let log: Option<(i64, i64, i64)> = sqlx::query_as(
                "SELECT quota,prompt_tokens,completion_tokens FROM logs WHERE type=2",
            )
            .fetch_optional(&fixture.pg)
            .await?;
            if expected_output > 0 {
                let (quota, input, output) =
                    log.expect("generated output must have a consumption log");
                assert!(input > 0, "{name}");
                assert_eq!(output, expected_output, "{name}");
                assert_eq!(
                    quota,
                    input + 3 * output,
                    "frozen model/group rates: {name}"
                );
                fixture.settled(quota, 1).await?;
            } else {
                fixture.settled(0, i64::from(completed)).await?;
                if completed {
                    assert_eq!(log, Some((0, 0, 0)), "{name}");
                } else {
                    assert!(log.is_none(), "{name}");
                }
            }
            assert_eq!(fixture.calls.load(Ordering::SeqCst), 1, "{name}");
            Ok::<(), Box<dyn std::error::Error + Send + Sync>>(())
        })
        .catch_unwind()
        .await;
        let cleanup = fixture.cleanup().await;
        match outcome {
            Ok(result) => {
                result?;
                cleanup?;
            }
            Err(panic) => {
                let _ = cleanup;
                std::panic::resume_unwind(panic);
            }
        }
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn downstream_drop_before_consumption_refunds_exactly_once() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("cancel-empty", true).await?;
    assert_eq!(fixture.balance().await?, (9990, 0, 0));
    drop(response);
    fixture.settled(0, 0).await?;
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn tiny_paid_price_with_empty_wallet_never_reaches_the_provider() -> TestResult {
    let fixture = Fixture::new(true).await?;
    sqlx::query("UPDATE users SET quota=0 WHERE id=1")
        .execute(&fixture.pg)
        .await?;
    sqlx::query("UPDATE tokens SET unlimited_quota=TRUE WHERE id=11")
        .execute(&fixture.pg)
        .await?;
    sqlx::query("UPDATE options SET value='{\"priced-model\":0.0001}' WHERE key='ModelRatio'")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("tiny-paid-empty-wallet", true).await?;
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 0);
    assert_eq!(fixture.balance().await?, (0, 0, 0));
    let logs: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs")
        .fetch_one(&fixture.pg)
        .await?;
    assert_eq!(logs, 0);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn managed_internal_keys_cannot_authenticate_or_contact_the_provider() -> TestResult {
    let fixture = Fixture::new(true).await?;
    sqlx::query("ALTER TABLE tokens ADD COLUMN oauth_managed BOOLEAN NOT NULL DEFAULT FALSE,ADD COLUMN creation_source TEXT NOT NULL DEFAULT ''")
        .execute(&fixture.pg).await?;
    for (managed, source) in [(true, "manual"), (false, "assistant_runtime")] {
        sqlx::query("UPDATE tokens SET oauth_managed=$1,creation_source=$2 WHERE id=11")
            .bind(managed)
            .bind(source)
            .execute(&fixture.pg)
            .await?;
        assert_eq!(
            fixture.request("internal-key", true).await?.status(),
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 0);
        fixture.settled(0, 0).await?;
    }
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn current_go_free_preconsume_policy_and_empty_failure_refunds_match_over_http() -> TestResult
{
    for case in price_lifecycle_vectors()?
        .into_iter()
        .filter(|case| case.get("retry_options").is_none())
    {
        let fixture = Fixture::new(true).await?;
        fixture.price_case(&case).await?;
        let name = case["name"].as_str().unwrap();
        let response = fixture.request(name, true).await?;
        assert_eq!(
            response.status().as_u16() as u64,
            case["status"].as_u64().unwrap(),
            "{name}"
        );
        assert_eq!(
            fixture.wallet_state().await?,
            case["reserved"],
            "{name} reservation"
        );
        if !response.status().is_success() {
            let body: Value =
                serde_json::from_slice(&to_bytes(response.into_body(), 65536).await?)?;
            assert_eq!(body["error"]["code"], case["error_code"], "{name}");
            assert_eq!(fixture.calls.load(Ordering::SeqCst), 0, "{name}");
        } else {
            let free:String=sqlx::query_scalar("SELECT price_snapshot->>'free_model' FROM relay_settlement_records WHERE request_id=$1")
                .bind(name).fetch_one(&fixture.pg).await?;
            assert_eq!(free, case["free"].to_string(), "{name} FreeModel");
            if case["refund"].as_bool().unwrap() {
                fixture.event(json!({"type":"response.failed","response":{"status":"failed","usage":{"input_tokens":0,"output_tokens":0}}})).await?;
            } else {
                fixture.event(json!({"type":"response.completed","response":{"status":"completed","usage":response_usage(&case)}})).await?;
            }
            to_bytes(response.into_body(), 65536).await?;
            assert_eq!(fixture.calls.load(Ordering::SeqCst), 1, "{name}");
        }
        assert!(
            fixture
                .tracker
                .drain_until(tokio::time::Instant::now() + Duration::from_secs(2))
                .await
        );
        assert_eq!(
            fixture.wallet_state().await?,
            case["settled"],
            "{name} final balances"
        );
        fixture.cleanup().await?;
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn current_go_retry_freezes_model_rates_and_reads_tools_and_units_at_settlement() -> TestResult
{
    for case in price_lifecycle_vectors()?
        .into_iter()
        .filter(|case| case.get("retry_options").is_some())
    {
        let mut fixture = Fixture::new(true).await?;
        fixture.price_case(&case).await?;
        fixture.first_output_deadline(Duration::from_millis(500))?;
        sqlx::query("INSERT INTO options(key,value) VALUES('RetryTimes','1')")
            .execute(&fixture.pg)
            .await?;
        sqlx::query("INSERT INTO channels(id,status,base_url,key) SELECT 2,1,base_url,key FROM channels WHERE id=1").execute(&fixture.pg).await?;
        sqlx::query("INSERT INTO abilities(\"group\",model,channel_id,enabled,priority,weight) VALUES('default','priced-model',2,TRUE,1,1)").execute(&fixture.pg).await?;
        fixture
            .event(json!({"type":"response.created","response":{"status":"in_progress"}}))
            .await?;
        let second = fixture.queue_turn().await;
        let name = case["name"].as_str().unwrap();
        let control = async {
            timeout(Duration::from_secs(2), async {
                while fixture.calls.load(Ordering::SeqCst) < 1 {
                    sleep(Duration::from_millis(2)).await;
                }
            })
            .await
            .map_err(|_| std::io::Error::other(format!("{name}: first provider call timed out")))?;
            assert_eq!(
                fixture.wallet_state().await?,
                case["reserved"],
                "{name} first attempt"
            );
            fixture.set_options(&case["retry_options"]).await?;
            timeout(Duration::from_secs(2), async {
                while fixture.calls.load(Ordering::SeqCst) < 2 {
                    sleep(Duration::from_millis(2)).await;
                }
            })
            .await
            .map_err(|_| std::io::Error::other(format!("{name}: retry provider call timed out")))?;
            assert_eq!(
                fixture.wallet_state().await?,
                case["retried"],
                "{name} retry does not replace initial budget"
            );
            if let Some(final_options) = case.get("final_options") {
                fixture.set_options(final_options).await?;
            }
            let mut events = vec![json!({"type":"response.output_text.delta","delta":"hello"})];
            if case["tool"] == "file_search" {
                events.push(
                    json!({"type":"response.output_item.done","item":{"type":"file_search_call"}}),
                );
            }
            events.push(json!({"type":"response.completed","response":{"status":"completed","usage":response_usage(&case)}}));
            for event in events {
                second
                    .send(Ok(Bytes::from(format!("data: {event}\n\n"))))
                    .await?;
            }
            Ok::<_, Box<dyn std::error::Error + Send + Sync>>(())
        };
        let (response, control) = tokio::join!(fixture.request(name, true), control);
        control?;
        let response = response?;
        assert_eq!(response.status(), StatusCode::OK, "{name}");
        to_bytes(response.into_body(), 65536).await?;
        assert_eq!(
            fixture.wallet_state().await?,
            case["settled"],
            "{name} current-Go fee"
        );
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 2);
        let (quota, other): (i64, String) =
            sqlx::query_as("SELECT quota,other FROM logs WHERE type=2 AND request_id=$1")
                .bind(name)
                .fetch_one(&fixture.pg)
                .await?;
        assert_eq!(quota, case["actual"].as_i64().unwrap());
        let metadata: Value = serde_json::from_str(&other)?;
        if case["tool"] == "file_search" {
            assert_eq!(metadata["tool_surcharges"][0]["price"], case["tool_price"]);
        }
        // A different logical request sees the newly configured expensive
        // model price; it cannot reuse the previous request's frozen rates.
        if case.get("final_options").is_some() {
            assert_eq!(
                fixture
                    .request("new-price-after-retry", true)
                    .await?
                    .status(),
                StatusCode::FORBIDDEN
            );
        }
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 2);
        fixture.cleanup().await?;
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn cancellation_while_waiting_for_upstream_body_refunds_without_waiting_for_timeout()
-> TestResult {
    let fixture = Fixture::new(false).await?;
    let request = Request::builder()
        .method("POST")
        .uri("/v1/responses")
        .header(header::AUTHORIZATION, "Bearer sk-tenant")
        .header(header::CONTENT_TYPE, "application/json")
        .header("x-oneapi-request-id", "cancel-pending-json")
        .body(Body::from(r#"{"model":"priced-model","input":"hello"}"#))?;
    let running = tokio::spawn(fixture.app.clone().oneshot(request));
    timeout(Duration::from_secs(2), async {
        while fixture.calls.load(Ordering::SeqCst) != 1 {
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await?;
    assert_eq!(fixture.balance().await?, (9990, 0, 0));
    running.abort();
    let _ = running.await;
    // The provider remains open. No bytes or terminal event are sent.
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn shutdown_tracker_waits_for_real_cancelled_refund_blocked_on_a_user_lock() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("shutdown-pending-refund", true).await?;
    let mut lock = fixture.pg.begin().await?;
    sqlx::query("SELECT id FROM users WHERE id=1 FOR UPDATE")
        .execute(&mut *lock)
        .await?;
    drop(response);
    assert!(
        fixture.tracker.pending() > 0,
        "HTTP response is gone but its financial task remains owned"
    );
    assert!(
        !fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_millis(25))
            .await
    );
    lock.commit().await?;
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(3))
            .await
    );
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn response_tool_only_usage_charges_real_calls_and_records_their_prices() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response=fixture.app.clone().oneshot(Request::builder().method("POST").uri("/v1/responses")
        .header(header::AUTHORIZATION,"Bearer sk-tenant").header(header::CONTENT_TYPE,"application/json")
        .body(Body::from(r#"{"model":"priced-model","input":"hello","stream":true,"tools":[{"type":"web_search"}]}"#))?).await?;
    assert_eq!(response.status(), StatusCode::OK);
    for _ in 0..2 {
        fixture
            .event(json!({"type":"response.output_item.done","item":{"type":"web_search_call"}}))
            .await?;
    }
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":0,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    fixture.settled(5000, 1).await?;
    let other: String = sqlx::query_scalar("SELECT other FROM logs WHERE type=2")
        .fetch_one(&fixture.pg)
        .await?;
    let other: Value = serde_json::from_str(&other)?;
    assert_eq!(
        other["tool_surcharges"],
        json!([{"name":"web_search","count":2,"price":10}])
    );
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn cancelled_stream_keeps_completed_tool_cost_without_inventing_token_usage() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("cancel-after-tool", true).await?;
    let mut body = response.into_body().into_data_stream();
    fixture
        .event(json!({"type":"response.output_item.done","item":{"type":"file_search_call"}}))
        .await?;
    timeout(Duration::from_secs(2), body.next())
        .await?
        .unwrap()?;
    drop(body);
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(3))
            .await
    );
    fixture.settled(625, 1).await?;
    let usage: (i64, i64) =
        sqlx::query_as("SELECT prompt_tokens,completion_tokens FROM logs WHERE type=2")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(usage, (0, 0));
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn failed_image_response_discards_completed_item_observations() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("failed-image", true).await?;
    fixture.event(json!({"type":"response.output_item.done","item":{"type":"image_generation_call","result":"pixels","status":"completed"}})).await?;
    fixture.event(json!({"type":"response.done","response":{"status":" FAILED ","usage":{"input_tokens":0,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn nonstream_malformed_json_matches_go_500_and_refunds() -> TestResult {
    let fixture = Fixture::new(false).await?;
    fixture
        .wire
        .send(Ok(Bytes::from_static(b"malformed-json")))
        .await?;
    let response = fixture
        .request_to("malformed-json", false, "/v1/chat/completions")
        .await?;
    assert_eq!(response.status(), StatusCode::INTERNAL_SERVER_ERROR);
    let body: Value = serde_json::from_slice(&to_bytes(response.into_body(), 65536).await?)?;
    assert_eq!(body["error"]["code"], "bad_response_body");
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn cancellation_preserves_measured_partial_usage_and_ignores_zero_placeholders() -> TestResult
{
    let fixture = Fixture::new(true).await?;
    let response = fixture.request("cancel-partial", true).await?;
    let mut body = response.into_body().into_data_stream();
    fixture.event(json!({"type":"response.in_progress","response":{"usage":{"input_tokens":30,"output_tokens":4}}})).await?;
    timeout(Duration::from_secs(2), body.next())
        .await?
        .unwrap()?;
    fixture.event(json!({"type":"response.in_progress","response":{"usage":{"input_tokens":0,"output_tokens":0}}})).await?;
    timeout(Duration::from_secs(2), body.next())
        .await?
        .unwrap()?;
    drop(body);
    fixture.settled(42, 1).await?;
    let content: String = sqlx::query_scalar("SELECT content FROM logs WHERE type=2")
        .fetch_one(&fixture.pg)
        .await?;
    assert!(content.contains("interrupted"));
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn in_flight_and_completed_request_id_replays_never_reach_provider() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let first = fixture.request("same-request", true).await?;
    let second = fixture.request("same-request", true).await?;
    assert_eq!(second.status(), StatusCode::CONFLICT);
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    assert_eq!(fixture.balance().await?, (9990, 0, 0));
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":5,"output_tokens":5}}})).await?;
    to_bytes(first.into_body(), 65536).await?;
    fixture.settled(20, 1).await?;
    assert_eq!(
        fixture.request("same-request", true).await?.status(),
        StatusCode::CONFLICT
    );
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.settled(20, 1).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn simultaneous_duplicate_requests_create_only_one_reservation_and_upstream_call()
-> TestResult {
    let fixture = Fixture::new(true).await?;
    let (first, second) = tokio::join!(
        fixture.request("racing-request", true),
        fixture.request("racing-request", true)
    );
    let first = first?;
    let second = second?;
    let successful = if first.status() == StatusCode::OK {
        assert_eq!(second.status(), StatusCode::CONFLICT);
        first
    } else {
        assert_eq!(first.status(), StatusCode::CONFLICT);
        assert_eq!(second.status(), StatusCode::OK);
        second
    };
    assert_eq!(fixture.balance().await?, (9990, 0, 0));
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":7,"output_tokens":1}}})).await?;
    to_bytes(successful.into_body(), 65536).await?;
    fixture.settled(10, 1).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn json_error_after_http_200_refunds_the_reservation() -> TestResult {
    let fixture = Fixture::new(false).await?;
    fixture
        .wire
        .send(Ok(Bytes::from_static(
            b"{\"error\":{\"message\":\"provider failed\"}}",
        )))
        .await?;
    // End the provider body while keeping the fixture's sender available.
    fixture
        .wire
        .send(Err(std::io::Error::other("provider disconnected")))
        .await?;
    let response = fixture.request("json-read-failure", false).await?;
    assert!(matches!(
        response.status(),
        StatusCode::BAD_GATEWAY | StatusCode::INTERNAL_SERVER_ERROR
    ));
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn chat_usage_after_finish_reason_is_charged_before_done() -> TestResult {
    let fixture = Fixture::new(true).await?;
    let response = fixture
        .request_to("chat-stream", true, "/v1/chat/completions")
        .await?;
    fixture
        .event(json!({"choices":[{"delta":{"content":"hello"},"finish_reason":"stop"}]}))
        .await?;
    fixture
        .event(json!({"choices":[],"usage":{"prompt_tokens":40,"completion_tokens":6}}))
        .await?;
    fixture
        .wire
        .send(Ok(Bytes::from_static(b"data: [DONE]\n\n")))
        .await?;
    let body = to_bytes(response.into_body(), 65536).await?;
    assert!(body.ends_with(b"data: [DONE]\n\n"));
    fixture.settled(58, 1).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn nonstream_chat_missing_usage_counts_output_and_returns_go_usage_shape() -> TestResult {
    let fixture = Fixture::new(false).await?;
    fixture
        .wire
        .send(Ok(Bytes::from(
            json!({"choices":[{"message":{"content":"Hello world"}}],"provider_extra":"retained"})
                .to_string(),
        )))
        .await?;
    let response = fixture
        .request_to("json-local-count", false, "/v1/chat/completions")
        .await?;
    assert_eq!(response.status(), StatusCode::OK);
    let body: Value = serde_json::from_slice(&to_bytes(response.into_body(), 65536).await?)?;
    assert_eq!(body["usage"]["prompt_tokens"], 11);
    assert_eq!(body["usage"]["completion_tokens"], 3);
    assert_eq!(body["provider_extra"], "retained");
    fixture.settled(20, 1).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn nonstream_chat_zero_prompt_keeps_provider_output_and_fills_input_usage() -> TestResult {
    let fixture = Fixture::new(false).await?;
    fixture.wire.send(Ok(Bytes::from_static(br#"{"choices":[{"message":{"content":"ignored for token counting"}}],"usage":{"prompt_tokens":0,"completion_tokens":5}}"#))).await?;
    let response = fixture
        .request_to("json-missing-prompt", false, "/v1/chat/completions")
        .await?;
    assert_eq!(response.status(), StatusCode::OK);
    let body: Value = serde_json::from_slice(&to_bytes(response.into_body(), 65536).await?)?;
    assert_eq!(body["usage"]["prompt_tokens"], 11);
    assert_eq!(body["usage"]["completion_tokens"], 5);
    fixture.settled(26, 1).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn settlement_storage_failure_preserves_provider_result_and_durable_replay_fence()
-> TestResult {
    let fixture = Fixture::new(true).await?;
    sqlx::query("ALTER TABLE logs ADD CONSTRAINT reject_finalization CHECK (type<>2)")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("settlement-unavailable", true).await?;
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":5}}})).await?;
    let body = to_bytes(response.into_body(), 65536).await?;
    let body = String::from_utf8_lossy(&body);
    assert!(body.contains("response.completed"));
    assert!(!body.contains("response.failed"));
    assert_eq!(
        fixture.balance().await?,
        (9990, 0, 0),
        "failed finalization cannot partially mutate counters or refund an executed request"
    );
    let pending: (i64, i64) =
        sqlx::query_as("SELECT type,quota FROM logs WHERE request_id='settlement-unavailable'")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(pending, (0, 10));
    assert_eq!(
        fixture
            .request("settlement-unavailable", true)
            .await?
            .status(),
        StatusCode::CONFLICT
    );
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn billing_preferences_select_wallet_subscription_and_allowed_fallbacks() -> TestResult {
    for (name, pref, wallet, subscription, allow, source, wallet_after, sub_after) in [
        (
            "wallet-only",
            "wallet_only",
            100,
            Some((100, 0)),
            true,
            "wallet",
            96,
            0,
        ),
        (
            "wallet-first",
            "wallet_first",
            100,
            Some((100, 0)),
            true,
            "wallet",
            96,
            0,
        ),
        (
            "wallet-first-fallback",
            "wallet_first",
            5,
            Some((100, 0)),
            true,
            "subscription",
            5,
            4,
        ),
        (
            "subscription-only",
            "subscription_only",
            0,
            Some((100, 0)),
            true,
            "subscription",
            0,
            4,
        ),
        (
            "subscription-first",
            "subscription_first",
            100,
            Some((100, 0)),
            true,
            "subscription",
            100,
            4,
        ),
        (
            "no-subscription",
            "subscription_first",
            100,
            None,
            true,
            "wallet",
            96,
            0,
        ),
        (
            "exhausted-fallback",
            "subscription_first",
            100,
            Some((3, 3)),
            true,
            "wallet",
            96,
            3,
        ),
        (
            "unlimited-subscription",
            "subscription_only",
            0,
            Some((0, 0)),
            false,
            "subscription",
            0,
            4,
        ),
    ] {
        let fixture = Fixture::new(true).await?;
        fixture.preference(pref).await?;
        sqlx::query("UPDATE users SET quota=$1 WHERE id=1")
            .bind(wallet)
            .execute(&fixture.pg)
            .await?;
        if let Some((total, used)) = subscription {
            fixture.subscription(total, used, allow).await?;
        }
        let response = fixture.request(name, true).await?;
        assert_eq!(response.status(), StatusCode::OK, "{name}");
        fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":4,"output_tokens":0}}})).await?;
        to_bytes(response.into_body(), 65536).await?;
        assert_eq!(fixture.balance().await?, (wallet_after, 4, 1), "{name}");
        if subscription.is_some() {
            assert_eq!(fixture.sub_usage().await?.0, sub_after, "{name}");
        }
        let token: (i64, i64) =
            sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
                .fetch_one(&fixture.pg)
                .await?;
        assert_eq!(token, (9996, 4), "{name}");
        let funding:String=sqlx::query_scalar("SELECT funding_source FROM relay_settlement_records WHERE request_id=$1 AND status='settled'")
            .bind(name).fetch_one(&fixture.pg).await?;
        assert_eq!(funding, source, "{name}");
        fixture.cleanup().await?;
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn strict_subscription_and_subscription_only_never_fall_back_to_wallet() -> TestResult {
    for (pref, has_subscription, allow) in [
        ("subscription_only", false, true),
        ("subscription_only", true, true),
        ("subscription_first", true, false),
    ] {
        let fixture = Fixture::new(true).await?;
        fixture.preference(pref).await?;
        if has_subscription {
            fixture.subscription(3, 0, allow).await?;
        }
        let response = fixture.request("strict-no-fallback", true).await?;
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        assert_eq!(fixture.calls.load(Ordering::SeqCst), 0);
        assert_eq!(fixture.balance().await?, (10000, 0, 0));
        if has_subscription {
            assert_eq!(fixture.sub_usage().await?, (0, 0));
        }
        fixture.cleanup().await?;
    }
    Ok(())
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn partial_subscription_reserves_shared_wallet_budget_until_actual_settlement() -> TestResult
{
    let fixture = Fixture::new(true).await?;
    fixture.subscription(3, 0, true).await?;
    sqlx::query("UPDATE users SET quota=7 WHERE id=1")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("partial-budget", true).await?;
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(
        fixture.balance().await?,
        (7, 0, 0),
        "overflow authorization holds budget without an early wallet debit"
    );
    assert_eq!(fixture.sub_usage().await?, (3, 0));
    let competing = fixture.request("competing-budget", true).await?;
    assert_eq!(competing.status(), StatusCode::FORBIDDEN);
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":10,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    assert_eq!(fixture.balance().await?, (0, 10, 1));
    assert_eq!(fixture.sub_usage().await?, (3, 0));
    let result:(String,i64,i64,i64)=sqlx::query_as("SELECT status,pre_consumed,wallet_consumed,token_consumed FROM subscription_pre_consume_records WHERE request_id='partial-budget'").fetch_one(&fixture.pg).await?;
    assert_eq!(result, ("settled".to_owned(), 3, 7, 10));
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn old_subscription_refund_after_request_reset_cannot_erase_new_turn_usage() -> TestResult {
    let fixture = Fixture::new(true).await?;
    fixture.subscription(100, 0, true).await?;
    let old = fixture.request("old-period", true).await?;
    assert_eq!(old.status(), StatusCode::OK);
    assert_eq!(fixture.sub_usage().await?, (10, 0));
    // Advance only calendar inputs. The new request itself runs the real
    // shared reset helper, starts the next grant, and reserves its own quota.
    sqlx::query("UPDATE subscription_plans SET quota_reset_period='custom',quota_reset_custom_seconds=60 WHERE id=1").execute(&fixture.pg).await?;
    sqlx::query("UPDATE user_subscriptions SET start_time=EXTRACT(EPOCH FROM NOW())::BIGINT-120,last_reset_time=EXTRACT(EPOCH FROM NOW())::BIGINT-120,next_reset_time=EXTRACT(EPOCH FROM NOW())::BIGINT-60 WHERE id=1").execute(&fixture.pg).await?;
    let _second_wire = fixture.queue_turn().await;
    let new = fixture.request("new-period", true).await?;
    assert_eq!(new.status(), StatusCode::OK);
    assert_eq!(fixture.sub_usage().await?, (10, 1));
    drop(old);
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(3))
            .await
    );
    assert_eq!(fixture.sub_usage().await?, (10, 1));
    let token: (i64, i64) =
        sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(token, (9990, 10));
    drop(new);
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(3))
            .await
    );
    assert_eq!(fixture.sub_usage().await?, (0, 1));
    fixture.settled(0, 0).await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn durable_settlement_recovers_after_storage_failure_without_another_provider_call()
-> TestResult {
    let fixture = Fixture::new(true).await?;
    fixture.subscription(100, 0, true).await?;
    sqlx::query("ALTER TABLE logs ADD CONSTRAINT reject_finalization CHECK(type<>2)")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("recover-settlement", true).await?;
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    assert_eq!(fixture.sub_usage().await?, (10, 0));
    let intent:(String,i64)=sqlx::query_as("SELECT status,actual_quota FROM relay_settlement_records WHERE request_id='recover-settlement'").fetch_one(&fixture.pg).await?;
    assert_eq!(intent, ("settling".to_owned(), 20));
    sqlx::query("ALTER TABLE logs DROP CONSTRAINT reject_finalization")
        .execute(&fixture.pg)
        .await?;
    assert_eq!(
        fixture
            .service
            .reconcile_settlements(10)
            .await
            .map_err(|error| std::io::Error::other(error.message))?,
        1
    );
    assert_eq!(
        fixture
            .service
            .reconcile_settlements(10)
            .await
            .map_err(|error| std::io::Error::other(error.message))?,
        0
    );
    assert_eq!(fixture.sub_usage().await?, (20, 0));
    assert_eq!(fixture.balance().await?, (10000, 20, 1));
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    let logs: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM logs WHERE type=2")
        .fetch_one(&fixture.pg)
        .await?;
    assert_eq!(logs, 1);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn changed_overflow_policy_keeps_exact_settlement_intent_for_later_reconciliation()
-> TestResult {
    let fixture = Fixture::new(true).await?;
    fixture.subscription(12, 0, true).await?;
    let response = fixture.request("policy-change", true).await?;
    sqlx::query("UPDATE user_subscriptions SET allow_wallet_overflow=FALSE WHERE id=1")
        .execute(&fixture.pg)
        .await?;
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    assert_eq!(fixture.sub_usage().await?, (10, 0));
    assert_eq!(fixture.balance().await?, (10000, 0, 0));
    let state: String = sqlx::query_scalar(
        "SELECT status FROM subscription_pre_consume_records WHERE request_id='policy-change'",
    )
    .fetch_one(&fixture.pg)
    .await?;
    assert_eq!(state, "settling");
    sqlx::query("UPDATE user_subscriptions SET allow_wallet_overflow=TRUE WHERE id=1")
        .execute(&fixture.pg)
        .await?;
    assert_eq!(
        fixture
            .service
            .reconcile_settlements(10)
            .await
            .map_err(|error| std::io::Error::other(error.message))?,
        1
    );
    assert_eq!(fixture.sub_usage().await?, (12, 0));
    assert_eq!(fixture.balance().await?, (9992, 20, 1));
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn unlimited_and_soft_deleted_tokens_keep_subscription_usage_accounting() -> TestResult {
    let fixture = Fixture::new(true).await?;
    fixture.subscription(100, 0, true).await?;
    sqlx::query("UPDATE tokens SET remain_quota=0,unlimited_quota=TRUE WHERE id=11")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("deleted-unlimited-key", true).await?;
    assert_eq!(response.status(), StatusCode::OK);
    let reserved: (i64, i64) =
        sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(
        reserved,
        (-10, 10),
        "unlimited skips only admission limits, not the accounting counter"
    );
    sqlx::query("UPDATE tokens SET deleted_at=NOW() WHERE id=11")
        .execute(&fixture.pg)
        .await?;
    fixture.event(json!({"type":"response.completed","response":{"usage":{"input_tokens":20,"output_tokens":0}}})).await?;
    to_bytes(response.into_body(), 65536).await?;
    let settled: (i64, i64) =
        sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=11")
            .fetch_one(&fixture.pg)
            .await?;
    assert_eq!(settled, (-20, 20));
    assert_eq!(fixture.sub_usage().await?, (20, 0));
    assert_eq!(fixture.balance().await?, (10000, 20, 1));
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn startup_and_periodic_workers_recover_once_and_preserve_unknown_reservations() -> TestResult
{
    let fixture = Fixture::new(true).await?;
    fixture.failed_settlement("worker-startup").await?;
    // This pending reservation stands for an interrupted process before usage
    // intent committed. Age alone must never authorize its refund or charge.
    let _wire = fixture.queue_turn().await;
    let unknown = fixture.request("unknown-evidence", true).await?;
    sqlx::query(
        "UPDATE relay_settlement_records SET created_at=1 WHERE request_id='unknown-evidence'",
    )
    .execute(&fixture.pg)
    .await?;
    let (shutdown, receiver) = watch::channel(false);
    let policy = RelayReconcilePolicy {
        interval: Duration::from_millis(20),
        batch_size: 5,
        warn_reserved_after: Duration::from_secs(1),
    };
    let first = fixture
        .service
        .spawn_settlement_reconciler(receiver.clone(), policy);
    let second = fixture
        .service
        .spawn_settlement_reconciler(receiver, policy);
    timeout(Duration::from_secs(3), async {
        loop {
            if fixture.balance().await? == (9970, 20, 1) {
                return Ok::<_, Box<dyn std::error::Error + Send + Sync>>(());
            }
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await??;
    sleep(Duration::from_millis(60)).await;
    let unknown_state: (String, Option<i64>) = sqlx::query_as("SELECT status,actual_quota FROM relay_settlement_records WHERE request_id='unknown-evidence'")
        .fetch_one(&fixture.pg).await?;
    assert_eq!(unknown_state, ("reserved".to_owned(), None));
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 2);
    // A later failed commit is recovered by a timer pass, not just startup.
    let periodic = fixture.queue_turn().await;
    sqlx::query("ALTER TABLE logs ADD CONSTRAINT reject_finalization CHECK(type<>2) NOT VALID")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("worker-periodic", true).await?;
    periodic.send(Ok(Bytes::from_static(b"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":0}}}\n\n"))).await?;
    to_bytes(response.into_body(), 65536).await?;
    sqlx::query("ALTER TABLE logs DROP CONSTRAINT reject_finalization")
        .execute(&fixture.pg)
        .await?;
    timeout(Duration::from_secs(3), async {
        loop {
            if fixture.balance().await? == (9950, 40, 2) {
                return Ok::<_, Box<dyn std::error::Error + Send + Sync>>(());
            }
            sleep(Duration::from_millis(10)).await;
        }
    })
    .await??;
    shutdown.send(true)?;
    timeout(Duration::from_secs(1), first).await??;
    timeout(Duration::from_secs(1), second).await??;
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 3);
    drop(unknown);
    fixture.settled(40, 2).await?;
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(1))
            .await
    );
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn stopped_recovery_worker_leaves_owned_financial_task_in_shutdown_drain() -> TestResult {
    let fixture = Fixture::new(true).await?;
    fixture.failed_settlement("worker-shutdown").await?;
    let mut lock = fixture.pg.begin().await?;
    sqlx::query("SELECT id FROM users WHERE id=1 FOR UPDATE")
        .execute(&mut *lock)
        .await?;
    let (shutdown, receiver) = watch::channel(false);
    let worker = fixture
        .service
        .spawn_settlement_reconciler(receiver, RelayReconcilePolicy::default());
    timeout(Duration::from_secs(2), async {
        while fixture.tracker.pending() == 0 {
            sleep(Duration::from_millis(5)).await;
        }
    })
    .await?;
    // Wait for the owned recovery to block behind the real payer row lock.
    sleep(Duration::from_millis(20)).await;
    shutdown.send(true)?;
    timeout(Duration::from_secs(1), worker).await??;
    assert!(
        !fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_millis(25))
            .await
    );
    lock.commit().await?;
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(2))
            .await
    );
    fixture.settled(20, 1).await?;
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey via local-services helper"]
async fn committed_reservation_refund_and_recovery_invalidate_go_quota_caches() -> TestResult {
    use hmac::{Hmac, Mac};
    let mut fixture = Fixture::new(true).await?;
    let client = redis::Client::open(std::env::var("LMM_API_TOKEN_TEST_VALKEY_URL")?)?;
    let secret = fixture.schema.clone();
    fixture.use_cache(client.clone(), &secret, Duration::from_secs(1));
    let mut mac = Hmac::<sha2::Sha256>::new_from_slice(secret.as_bytes())?;
    mac.update(b"tenant");
    let digest = hex::encode(mac.finalize().into_bytes());
    let token_key = format!("token:{digest}");
    let fence_key = format!("token:fence:{digest}");
    let mut connection = client.get_multiplexed_async_connection().await?;
    async fn seed(
        connection: &mut redis::aio::MultiplexedConnection,
        token_key: &str,
        fence_key: &str,
    ) -> TestResult {
        redis::pipe()
            .atomic()
            .cmd("HSET")
            .arg("user:1")
            .arg("Quota")
            .arg(999999)
            .ignore()
            .cmd("HSET")
            .arg(token_key)
            .arg("RemainQuota")
            .arg(999999)
            .ignore()
            .cmd("DEL")
            .arg(fence_key)
            .ignore()
            .query_async::<()>(connection)
            .await?;
        Ok(())
    }
    async fn absent(
        connection: &mut redis::aio::MultiplexedConnection,
        token_key: &str,
        fence_key: &str,
        operation: &str,
    ) -> TestResult {
        let existing: i64 = redis::cmd("EXISTS")
            .arg("user:1")
            .arg(token_key)
            .query_async(connection)
            .await?;
        assert_eq!(existing, 0, "{operation}");
        let ttl: i64 = redis::cmd("TTL")
            .arg(fence_key)
            .query_async(connection)
            .await?;
        assert!((1..=10).contains(&ttl), "{operation}: fence TTL={ttl}");
        Ok(())
    }
    seed(&mut connection, &token_key, &fence_key).await?;
    let response = fixture.request("cache-reserve", true).await?;
    absent(&mut connection, &token_key, &fence_key, "reserve").await?;
    seed(&mut connection, &token_key, &fence_key).await?;
    drop(response);
    fixture.settled(0, 0).await?;
    assert!(
        fixture
            .tracker
            .drain_until(tokio::time::Instant::now() + Duration::from_secs(1))
            .await
    );
    absent(&mut connection, &token_key, &fence_key, "refund").await?;
    let wire = fixture.queue_turn().await;
    sqlx::query("ALTER TABLE logs ADD CONSTRAINT reject_finalization CHECK(type<>2)")
        .execute(&fixture.pg)
        .await?;
    let response = fixture.request("cache-recovery", true).await?;
    wire.send(Ok(Bytes::from_static(b"data: {\"type\":\"response.completed\",\"response\":{\"usage\":{\"input_tokens\":20,\"output_tokens\":0}}}\n\n"))).await?;
    to_bytes(response.into_body(), 65536).await?;
    sqlx::query("ALTER TABLE logs DROP CONSTRAINT reject_finalization")
        .execute(&fixture.pg)
        .await?;
    seed(&mut connection, &token_key, &fence_key).await?;
    assert_eq!(
        fixture
            .service
            .reconcile_settlements(10)
            .await
            .map_err(|error| std::io::Error::other(error.message))?,
        1
    );
    fixture.settled(20, 1).await?;
    absent(&mut connection, &token_key, &fence_key, "recover").await?;
    redis::cmd("DEL")
        .arg(&token_key)
        .arg(&fence_key)
        .query_async::<i64>(&mut connection)
        .await?;
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn unavailable_cache_never_retries_or_rolls_back_committed_funds() -> TestResult {
    let mut fixture = Fixture::new(false).await?;
    let listener = TcpListener::bind("127.0.0.1:0").await?;
    let address = listener.local_addr()?;
    // A real connection that never replies proves the cache bound covers
    // connection setup as well as commands, not only a refused socket.
    let stalled = tokio::spawn(async move {
        let mut connections = Vec::new();
        loop {
            let Ok((stream, _)) = listener.accept().await else {
                break;
            };
            connections.push(stream);
        }
    });
    fixture.use_cache(
        redis::Client::open(format!("redis://{address}/"))?,
        "test-secret",
        Duration::from_millis(25),
    );
    fixture
        .wire
        .send(Ok(Bytes::from_static(
            b"{\"usage\":{\"input_tokens\":20,\"output_tokens\":0}}",
        )))
        .await?;
    let response = timeout(
        Duration::from_secs(2),
        fixture.request("cache-unavailable", false),
    )
    .await??;
    assert_eq!(response.status(), StatusCode::OK);
    to_bytes(response.into_body(), 65536).await?;
    fixture.settled(20, 1).await?;
    assert_eq!(
        fixture
            .service
            .reconcile_settlements(10)
            .await
            .map_err(|error| std::io::Error::other(error.message))?,
        0
    );
    assert_eq!(fixture.calls.load(Ordering::SeqCst), 1);
    stalled.abort();
    fixture.cleanup().await
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn relay_trust_discount_uses_credited_quota_and_excludes_linuxdo_credit() -> TestResult {
    for (provider, method, credit, expected) in [
        ("stripe", "stripe", 50_000_000, 97),
        ("epay", "ldc", 500_000_000, 100),
    ] {
        let fixture = Fixture::new(false).await?;
        sqlx::query("CREATE TABLE top_ups(user_id BIGINT,status TEXT,payment_provider TEXT,payment_method TEXT,money DOUBLE PRECISION,credited_quota BIGINT,complete_time BIGINT)").execute(&fixture.pg).await?;
        sqlx::query("INSERT INTO top_ups VALUES(1,'success',$1,$2,0.01,$3,EXTRACT(EPOCH FROM NOW())::BIGINT)")
            .bind(provider).bind(method).bind(credit).execute(&fixture.pg).await?;
        fixture
            .wire
            .send(Ok(Bytes::from_static(
                b"{\"usage\":{\"input_tokens\":100,\"output_tokens\":0}}",
            )))
            .await?;
        let response = fixture.request("trust-credit", false).await?;
        assert_eq!(response.status(), StatusCode::OK);
        to_bytes(response.into_body(), 65536).await?;
        fixture.settled(expected, 1).await?;
        fixture.cleanup().await?;
    }
    Ok(())
}
