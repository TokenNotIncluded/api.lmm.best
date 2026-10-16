//! Shared complete-response and ledger assertions for resource acceptance clients.
//! Callers provide their real Core ledger balance assertion through `super::balanced`.
use super::balanced;
#[path = "model_resource_response.rs"]
mod response;
use serde_json::{Value, json};
use sqlx::PgPool;
use std::collections::HashSet;

pub(super) fn valid_complete_body(bytes: &[u8], stream: bool, request_id: &str) -> bool {
    response::valid_complete_body(bytes, stream, request_id)
}

#[derive(Clone)]
pub(super) struct Outcome {
    pub(super) ordinal: usize,
    pub(super) stream: bool,
    pub(super) status: u16,
    pub(super) code: String,
    pub(super) id: Option<String>,
    pub(super) success: bool,
    pub(super) latency_ms: u64,
    pub(super) submission_lag_ms: u64,
}
impl Outcome {
    pub(super) fn value(&self) -> Value {
        json!({"ordinal":self.ordinal,"stream":self.stream,"status":self.status,
        "code":self.code,"request_id":self.id,"complete_success":self.success,
        "latency_ms":self.latency_ms,"submission_lag_ms":self.submission_lag_ms})
    }
}

pub(super) fn body(prompt: &str, stream: bool) -> Value {
    json!({"model":"local-model","messages":[{"role":"user","content":prompt}],"max_tokens":20,
        "stream":stream,"stream_options":if stream {json!({"include_usage":true})} else {Value::Null}})
}
pub(super) fn valid_id(response: &reqwest::Response) -> Option<String> {
    response.headers().get("x-request-id").map(|raw| {
        let id = raw.to_str().unwrap().to_owned();
        assert_eq!(id.len(), 64);
        assert!(id.bytes().all(|x| x.is_ascii_hexdigit()));
        id
    })
}

pub(super) async fn complete(
    client: reqwest::Client,
    endpoint: String,
    key: String,
    phase: String,
    ordinal: usize,
    stream: bool,
    due: tokio::time::Instant,
) -> Outcome {
    let lag = due.elapsed().as_millis() as u64;
    let mut result = Outcome {
        ordinal,
        stream,
        status: 0,
        code: String::new(),
        id: None,
        success: false,
        latency_ms: 0,
        submission_lag_ms: lag,
    };
    let response = client
        .post(format!("{endpoint}/v1/chat/completions"))
        .bearer_auth(key)
        .header("idempotency-key", format!("resource-{phase}-{ordinal:06}"))
        .json(&body("hello", stream))
        .send()
        .await;
    match response {
        Err(error) => {
            result.code = if error.is_timeout() {
                "client_timeout"
            } else {
                "transport_error"
            }
            .into()
        }
        Ok(mut response) => {
            result.status = response.status().as_u16();
            result.id = valid_id(&response);
            let mut bytes = Vec::new();
            let mut read_ok = true;
            loop {
                match response.chunk().await {
                    Ok(Some(chunk)) => {
                        bytes.extend_from_slice(&chunk);
                        if bytes.len() > 16_384 {
                            read_ok = false;
                            break;
                        }
                    }
                    Ok(None) => break,
                    Err(_) => {
                        read_ok = false;
                        break;
                    }
                }
            }
            if !read_ok {
                result.code = "incomplete_or_oversized_body".into();
            } else if result.status == 200 {
                let valid = result
                    .id
                    .as_deref()
                    .is_some_and(|id| valid_complete_body(&bytes, stream, id));
                result.success = valid && result.id.is_some();
                if !result.success {
                    result.code = "invalid_complete_response".into();
                }
            } else {
                let value: Value = serde_json::from_slice(&bytes).unwrap();
                result.code = value["error"]["code"]
                    .as_str()
                    .unwrap_or("unclassified_error")
                    .into();
            }
        }
    }
    result.latency_ms = due.elapsed().as_millis() as u64;
    result
}

pub(super) fn percentile(values: &[u64], numerator: usize) -> u64 {
    let mut values = values.to_vec();
    values.sort_unstable();
    values[(values.len() * numerator).div_ceil(100).saturating_sub(1)]
}

pub(super) async fn verify_paid(pool: &PgPool, account: i64, ids: &[String]) {
    assert_eq!(
        ids.iter().collect::<HashSet<_>>().len(),
        ids.len(),
        "successful logical requests reused a durable ID"
    );
    let rows:Vec<Value>=sqlx::query_scalar("SELECT jsonb_build_object('id',r.id,'state',r.state,'reserved',r.reserved,'settled',r.settled,'payer',r.payer_account_id,'input',r.input_tokens,'cached',r.cached_tokens,'output',r.output_tokens,'hold_reserved',h.reserved,'captured',h.captured,'closed',h.closed,'reservation_journal',h.reservation_journal_id IS NOT NULL,'capture_journal',h.capture_journal_id IS NOT NULL,'wallet_surplus',(SELECT sum(e.delta_units)::bigint FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=h.capture_journal_id AND a.owner_account_id=r.payer_account_id AND a.bucket='wallet')) FROM core_charging.requests r JOIN core_charging.ledger_holds h ON h.request_id=r.id WHERE r.id=ANY($1)")
        .bind(ids).fetch_all(pool).await.unwrap();
    assert_eq!(rows.len(), ids.len());
    for row in rows {
        assert_eq!(row["state"], "settled");
        assert_eq!(row["reserved"], 140);
        assert_eq!(row["settled"], 49);
        assert_eq!(row["payer"], account);
        assert_eq!(row["input"], 10);
        assert_eq!(row["cached"], 2);
        assert_eq!(row["output"], 20);
        assert_eq!(row["hold_reserved"], 140);
        assert_eq!(row["captured"], 49);
        assert_eq!(row["closed"], true);
        assert_eq!(row["reservation_journal"], true);
        assert_eq!(row["capture_journal"], true);
        assert_eq!(row["wallet_surplus"], 91);
    }
    balanced(pool).await;
}
