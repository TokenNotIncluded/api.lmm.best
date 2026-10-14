//! Shared synthetic loopback provider and owner-only database seeds.
//! HTTP admission and funds assertions belong to the importing tests.
use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::State,
    http::StatusCode,
    response::{IntoResponse, Response},
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
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{
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
use tokio_stream::wrappers::ReceiverStream;

pub(super) struct Provider {
    pub(super) endpoint: String,
    pub(super) hits: Arc<AtomicUsize>,
    pub(super) auth: Arc<Mutex<Vec<String>>>,
    task: JoinHandle<()>,
    pub(super) stream_gate: Arc<Semaphore>,
    pub(super) stream_closed: Arc<AtomicUsize>,
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
    stream_closed: Arc<AtomicUsize>,
}
struct ObservedStream {
    inner: ReceiverStream<Result<Bytes, std::io::Error>>,
    closed: Arc<AtomicUsize>,
}
impl tokio_stream::Stream for ObservedStream {
    type Item = Result<Bytes, std::io::Error>;
    fn poll_next(
        self: std::pin::Pin<&mut Self>,
        cx: &mut std::task::Context<'_>,
    ) -> std::task::Poll<Option<Self::Item>> {
        tokio_stream::Stream::poll_next(std::pin::Pin::new(&mut self.get_mut().inner), cx)
    }
}
impl Drop for ObservedStream {
    fn drop(&mut self) {
        self.closed.fetch_add(1, Ordering::SeqCst);
    }
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
        let stream_closed = state.stream_closed.clone();
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
            Body::from_stream(ObservedStream {
                inner: ReceiverStream::new(rx),
                closed: stream_closed,
            }),
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
    pub(super) async fn start(usage: bool) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/custom/v1", listener.local_addr().unwrap());
        let hits = Arc::new(AtomicUsize::new(0));
        let auth = Arc::new(Mutex::new(Vec::new()));
        let stream_gate = Arc::new(Semaphore::new(0));
        let stream_closed = Arc::new(AtomicUsize::new(0));
        let app = Router::new()
            .route("/custom/v1/chat/completions", post(provider))
            .with_state(ProviderState {
                hits: hits.clone(),
                auth: auth.clone(),
                usage,
                stream_gate: stream_gate.clone(),
                stream_closed: stream_closed.clone(),
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
            stream_closed,
        }
    }
}

pub(super) async fn fixture(
    pool: &PgPool,
    amount: i64,
) -> (IdentityStore, Ledger, String, String, i64, i64) {
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
