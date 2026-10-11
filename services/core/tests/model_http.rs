//! Real PostgreSQL charging + double-entry ledger, with a local synthetic TCP
//! provider. These are protocol/ledger tests, not live supplier compatibility.
use axum::{
    Json, Router,
    body::{Body, to_bytes},
    extract::State,
    http::{Request, StatusCode},
    routing::post,
};
use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        ledger::{Action, Ledger, Request as LedgerRequest},
    },
    core_schema,
    funding::FundingPolicy,
    identity::IdentityStore,
    model_http::{ChargePolicy, ModelGateway, UpstreamCredential},
    relay::{ClientProtocol, Limits, Request as RelayRequest},
    routing::*,
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{
    collections::BTreeMap,
    sync::{
        Arc, Mutex,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};
use tokio::{net::TcpListener, task::JoinHandle};
use tower::ServiceExt;

struct Provider {
    endpoint: String,
    hits: Arc<AtomicUsize>,
    auth: Arc<Mutex<Vec<String>>>,
    task: JoinHandle<()>,
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
}
async fn provider(
    State(state): State<ProviderState>,
    headers: axum::http::HeaderMap,
    Json(request): Json<Value>,
) -> Json<Value> {
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
    let mut value = json!({"id":"synthetic-completion","object":"chat.completion","created":0,"model":"provider-model",
        "choices":[{"index":0,"message":{"role":"assistant","content":"local synthetic answer"},"finish_reason":"stop"}]});
    if state.usage {
        value["usage"] = json!({"prompt_tokens":10,"completion_tokens":20,"prompt_tokens_details":{"cached_tokens":2}});
    }
    Json(value)
}
impl Provider {
    async fn start(usage: bool) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/custom/v1", listener.local_addr().unwrap());
        let hits = Arc::new(AtomicUsize::new(0));
        let auth = Arc::new(Mutex::new(Vec::new()));
        let app = Router::new()
            .route("/custom/v1/chat/completions", post(provider))
            .with_state(ProviderState {
                hits: hits.clone(),
                auth: auth.clone(),
                usage,
            });
        let task = tokio::spawn(async move {
            axum::serve(listener, app).await.unwrap();
        });
        Self {
            endpoint,
            hits,
            auth,
            task,
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
