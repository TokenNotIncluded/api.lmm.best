//! Complete store transactions and per-order, per-journal accounting verification.
use super::model_client::percentile;
use super::{Store, balanced, private};
use lmm_core::billing::ledger::Ledger;
use serde_json::{Value, json};
use std::{
    collections::{HashMap, HashSet},
    path::Path,
    time::Duration,
};
use tokio::task::JoinSet;

type Trace = std::sync::Arc<std::sync::Mutex<Vec<Value>>>;

// A dropped request remains visible when the single whole-flow deadline expires.
struct Attempt {
    trace: Trace,
    index: usize,
    started: tokio::time::Instant,
}
impl Attempt {
    fn outcome(&self, status: Option<u16>, code: &str) {
        let mut trace = self.trace.lock().unwrap();
        trace[self.index]["status"] = json!(status);
        trace[self.index]["code"] = json!(code);
    }
}
impl Drop for Attempt {
    fn drop(&mut self) {
        self.trace.lock().unwrap()[self.index]["latency_ms"] =
            json!(self.started.elapsed().as_millis());
    }
}

async fn call(
    store: &Store,
    user: Option<&str>,
    path: &str,
    body: Value,
    trace: &Trace,
) -> Result<(u16, Value), String> {
    let index = {
        let mut trace = trace.lock().unwrap();
        let index = trace.len();
        trace.push(
            json!({"route":path,"action":body.get("action").and_then(Value::as_str),
            "status":null,"code":"flow_deadline_cancelled","latency_ms":null}),
        );
        index
    };
    let attempt = Attempt {
        trace: trace.clone(),
        index,
        started: tokio::time::Instant::now(),
    };
    let mut request = store
        .client
        .post(format!("{}/extensions/v1/store{path}", store.endpoint))
        .bearer_auth(&store.host)
        .json(&body);
    if let Some(user) = user {
        request = request.header("x-lmm-user-credential", user);
    }
    let response = request.send().await.map_err(|error| {
        let code = if error.is_timeout() {
            "client_timeout"
        } else {
            "transport_error"
        };
        attempt.outcome(None, code);
        code.to_owned()
    })?;
    let status = response.status().as_u16();
    attempt.outcome(Some(status), "flow_deadline_cancelled");
    let bytes = response.bytes().await.map_err(|_| {
        attempt.outcome(Some(status), "incomplete_body");
        "incomplete_body".to_owned()
    })?;
    if bytes.len() > 65536 {
        attempt.outcome(Some(status), "oversized_body");
        return Err("oversized_body".into());
    }
    let value: Value = serde_json::from_slice(&bytes).map_err(|_| {
        attempt.outcome(Some(status), "non_json_response");
        "non_json_response".to_owned()
    })?;
    let code = if status == 202 && value["error"] == "money_result_pending" {
        "money_result_pending".to_owned()
    } else if status != 200 {
        format!("http_{status}")
    } else if value.get("error").is_some() {
        "business_error".to_owned()
    } else {
        "ok".to_owned()
    };
    attempt.outcome(Some(status), &code);
    Ok((status, value))
}
async fn ok(
    store: &Store,
    user: &str,
    path: &str,
    body: Value,
    trace: &Trace,
) -> Result<Value, String> {
    for attempt in 0..80 {
        let (status, value) = call(store, Some(user), path, body.clone(), trace).await?;
        if path == "/orders/change" && status == 202 && value["error"] == "money_result_pending" {
            let order = body["order_id"]
                .as_str()
                .ok_or("missing_request_order_id")?;
            if value["data"]["id"].as_str() != Some(order) {
                return Err("pending_order_mismatch".into());
            }
            if attempt == 79 {
                return Err("pending_attempt_limit".into());
            }
            tokio::time::sleep(Duration::from_millis(25)).await;
            continue;
        }
        if status != 200 {
            return Err(format!("http_{status}"));
        }
        if value.get("error").is_some() {
            return Err("business_error".into());
        }
        return Ok(value["data"].clone());
    }
    unreachable!("all bounded attempts return or retry")
}
async fn transaction(
    store: Store,
    label: String,
    ordinal: usize,
    due: tokio::time::Instant,
) -> Value {
    let lag = due.elapsed().as_millis();
    let mut id = None;
    let trace: Trace = Default::default();
    // Every attempt, denial check and retry shares this absolute full-flow budget.
    let flow = async {
        let purchase = json!({"shop_id":store.shop,"variant_id":store.variant,
            "buyer":{"kind":"personal","id":store.buyer_id},"quantity":1,
            "key":format!("mixed-{label}-{ordinal}")});
        let order = ok(&store, &store.buyer, "/orders", purchase.clone(), &trace).await?;
        let order_id = order["id"].as_str().ok_or("missing_order_id")?.to_owned();
        id = Some(order_id.clone());
        let replay = ok(&store, &store.buyer, "/orders", purchase, &trace).await?;
        if replay != order {
            return Err("purchase_replay_changed".into());
        }
        let change = |action: &str| {
            json!({"shop_id":store.shop,"order_id":order_id,
            "action":action,"text":"Synthetic mixed load"})
        };
        if ordinal == 0 {
            if call(&store, None, "/orders/change", change("pay"), &trace)
                .await?
                .0
                != 401
            {
                return Err("missing_user_not_denied".into());
            }
            if call(
                &store,
                Some(&store.outsider),
                "/orders/change",
                change("pay"),
                &trace,
            )
            .await?
            .0 != 403
            {
                return Err("outsider_not_denied".into());
            }
            let core: i64 =
                sqlx::query_scalar("SELECT count(*) FROM core_commerce.orders WHERE order_id=$1")
                    .bind(&order_id)
                    .fetch_one(&store.pool)
                    .await
                    .unwrap();
            if core != 0 {
                return Err("denial_created_funds".into());
            }
        }
        let (a, b) = tokio::join!(
            ok(
                &store,
                &store.buyer,
                "/orders/change",
                change("pay"),
                &trace
            ),
            ok(
                &store,
                &store.buyer,
                "/orders/change",
                change("pay"),
                &trace
            )
        );
        if a? != b? {
            return Err("concurrent_pay_replay_changed".into());
        }
        ok(
            &store,
            &store.seller,
            "/orders/change",
            change("fulfill"),
            &trace,
        )
        .await?;
        ok(
            &store,
            &store.seller,
            "/orders/change",
            change("settle"),
            &trace,
        )
        .await?;
        ok(
            &store,
            &store.buyer,
            "/orders/change",
            change("request_refund"),
            &trace,
        )
        .await?;
        let refunded = ok(
            &store,
            &store.seller,
            "/orders/change",
            change("refund"),
            &trace,
        )
        .await?;
        if refunded["state"] != "refunded" {
            return Err("refund_not_terminal".into());
        }
        if ok(
            &store,
            &store.seller,
            "/orders/change",
            change("refund"),
            &trace,
        )
        .await?
            != refunded
        {
            return Err("refund_replay_changed".into());
        }
        Ok(())
    };
    let result: Result<(), String> =
        match tokio::time::timeout_at(due + Duration::from_secs(2), flow).await {
            Ok(result) => result,
            Err(_) => Err("flow_deadline_exceeded".into()),
        };
    let attempts = trace.lock().unwrap().clone();
    let pending_responses = attempts
        .iter()
        .filter(|row| row["code"] == "money_result_pending")
        .count();
    json!({"ordinal":ordinal,"order_id":id,"complete_success":result.is_ok(),"code":result.err(),
        "latency_ms":due.elapsed().as_millis(),"submission_lag_ms":lag,
        "pending_responses":pending_responses,"attempt_count":attempts.len(),"attempts":attempts})
}
pub(super) async fn store_phase(
    store: &Store,
    root: &Path,
    label: &str,
    seconds: usize,
) -> Vec<String> {
    let start = tokio::time::Instant::now();
    let mut tasks = JoinSet::new();
    for ordinal in 0..seconds {
        let due = start + Duration::from_secs(ordinal as u64);
        tokio::time::sleep_until(due).await;
        tasks.spawn(transaction(store.clone(), label.into(), ordinal, due));
    }
    let mut rows = Vec::new();
    while let Some(row) = tasks.join_next().await {
        rows.push(row.unwrap());
    }
    tokio::time::sleep_until(start + Duration::from_secs(seconds as u64)).await;
    rows.sort_by_key(|row| row["ordinal"].as_u64().unwrap());
    let times = rows
        .iter()
        .map(|row| row["latency_ms"].as_u64().unwrap())
        .collect::<Vec<_>>();
    private(root,&format!("mixed-store-{label}.json"),&serde_json::to_vec(&json!({"offered_complete_transactions":seconds,"seconds":seconds,"elapsed_ms":start.elapsed().as_millis(),"p95_ms":percentile(&times,95),"p99_ms":percentile(&times,99),"outcomes":rows})).unwrap());
    assert_eq!(rows.len(), seconds);
    assert!(
        rows.iter().all(|row| row["complete_success"] == true),
        "pending/refused/failed commerce flow is not a completed transaction"
    );
    assert!(
        percentile(&times, 95) <= 1000 && percentile(&times, 99) <= 2000,
        "complete commerce latency missed fixed gate"
    );
    assert!(
        rows.iter()
            .all(|row| row["submission_lag_ms"].as_u64().unwrap() < 200)
    );
    let ids = rows
        .iter()
        .map(|row| row["order_id"].as_str().unwrap().to_owned())
        .collect::<Vec<_>>();
    verify_store(store, &ids).await;
    ids
}
pub(super) async fn verify_store(store: &Store, ids: &[String]) {
    assert_eq!(ids.iter().collect::<HashSet<_>>().len(), ids.len());
    let orders: Vec<Value> = sqlx::query_scalar("SELECT jsonb_build_object('id',order_id,'payer',buyer_account_id,'seller',seller_account_id,'credits',credits,'state',state,'reserve',reserve_journal_id,'settlement',settlement_journal_id,'delivery',delivery_reference) FROM core_commerce.orders WHERE order_id=ANY($1)")
        .bind(ids).fetch_all(&store.pool).await.unwrap();
    assert_eq!(orders.len(), ids.len());
    let core: Vec<(String, String, String, Value)> = sqlx::query_as("SELECT o.order_id,p.operation_key,p.kind,p.receipt FROM core_commerce.operations p JOIN core_commerce.orders o ON o.id=p.order_id WHERE o.order_id=ANY($1)")
        .bind(ids).fetch_all(&store.pool).await.unwrap();
    assert_eq!(core.len(), ids.len() * 3);
    let mut receipts = HashMap::new();
    let mut per_order: HashMap<String, HashMap<String, i64>> = HashMap::new();
    let mut journals = HashSet::new();
    for (order_id, key, kind, receipt) in core {
        assert!(["pay", "settle", "refund"].contains(&kind.as_str()));
        assert_eq!(receipt["key"], key);
        assert_eq!(receipt["outcome"], "succeeded");
        let journal = receipt["reference"]
            .as_str()
            .unwrap()
            .strip_prefix("commerce:journal:")
            .unwrap()
            .parse::<i64>()
            .unwrap();
        assert!(
            journals.insert(journal),
            "commerce receipt reused a journal"
        );
        assert!(
            per_order
                .entry(order_id)
                .or_default()
                .insert(kind, journal)
                .is_none(),
            "order has duplicate commerce operation kind"
        );
        assert!(receipts.insert(key, receipt).is_none());
    }
    assert_eq!(receipts.len(), ids.len() * 3);
    assert_eq!(per_order.len(), ids.len());
    let journal_ids = journals.into_iter().collect::<Vec<_>>();
    let journal_rows: Vec<(i64, String, i64, Option<i64>)> = sqlx::query_as(
        "SELECT id,kind,amount_units,parent_journal_id FROM core_billing.ledger_journals WHERE id=ANY($1)")
        .bind(&journal_ids).fetch_all(&store.pool).await.unwrap();
    assert_eq!(journal_rows.len(), ids.len() * 3);
    let journal_rows = journal_rows
        .into_iter()
        .map(|(id, kind, amount, parent)| (id, (kind, amount, parent)))
        .collect::<HashMap<_, _>>();
    let entry_rows: Vec<(i64, Option<i64>, String, i64)> = sqlx::query_as(
        "SELECT e.journal_id,a.owner_account_id,a.bucket,e.delta_units FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=ANY($1)")
        .bind(&journal_ids).fetch_all(&store.pool).await.unwrap();
    let mut entries: HashMap<i64, Vec<(Option<i64>, String, i64)>> = HashMap::new();
    for (journal, owner, bucket, delta) in entry_rows {
        entries
            .entry(journal)
            .or_default()
            .push((owner, bucket, delta));
    }
    assert_eq!(entries.len(), journal_ids.len());
    for order in orders {
        let id = order["id"].as_str().unwrap();
        assert!(ids.iter().any(|expected| expected == id));
        assert_eq!(order["payer"], store.buyer_account);
        assert_eq!(order["seller"], store.seller_account);
        assert_eq!(order["credits"], 5000);
        assert_eq!(order["state"], "refunded");
        // Manual fulfillment belongs to Go; Core only stores a delivery reference
        // for the distinct trusted fixed-content complete_delivery RPC.
        assert_eq!(order["delivery"], Value::Null);
        let operations = per_order.get(id).unwrap();
        assert_eq!(operations.len(), 3);
        let pay = operations["pay"];
        let settle = operations["settle"];
        let refund = operations["refund"];
        assert_eq!(order["reserve"].as_i64(), Some(pay));
        assert_eq!(order["settlement"].as_i64(), Some(settle));
        for (journal, kind, parent, legs) in [
            (
                pay,
                "reserve",
                None,
                vec![
                    (Some(store.buyer_account), "wallet".to_owned(), -5000),
                    (Some(store.buyer_account), "reserved".to_owned(), 5000),
                ],
            ),
            (
                settle,
                "commerce_capture",
                Some(pay),
                vec![
                    (Some(store.buyer_account), "reserved".to_owned(), -5000),
                    (Some(store.seller_account), "wallet".to_owned(), 5000),
                ],
            ),
            (
                refund,
                "refund_commerce",
                Some(settle),
                vec![
                    (Some(store.seller_account), "wallet".to_owned(), -5000),
                    (Some(store.buyer_account), "wallet".to_owned(), 5000),
                ],
            ),
        ] {
            assert_eq!(
                journal_rows.get(&journal),
                Some(&(kind.to_owned(), 5000, parent))
            );
            let mut actual = entries.remove(&journal).unwrap();
            let mut expected = legs;
            actual.sort();
            expected.sort();
            assert_eq!(
                actual, expected,
                "order {id} journal {journal} has wrong ledger legs"
            );
        }
    }
    assert!(entries.is_empty());
    let go: Vec<Value> = sqlx::query_scalar(
        "SELECT body FROM lmm_store.money_operations WHERE body->'command'->>'order_id'=ANY($1)",
    )
    .bind(ids)
    .fetch_all(&store.owner)
    .await
    .unwrap();
    assert_eq!(go.len(), ids.len() * 3);
    let mut go_keys = HashSet::new();
    for op in go {
        assert_eq!(op["done"], true);
        let key = op["command"]["key"].as_str().unwrap();
        assert!(go_keys.insert(key.to_owned()));
        let receipt = receipts.get(key).unwrap();
        for field in ["key", "command_hash", "reference", "outcome"] {
            assert_eq!(op["receipt"][field], receipt[field]);
        }
    }
    assert_eq!(go_keys.len(), receipts.len());
    let go_orders: Vec<Value> =
        sqlx::query_scalar("SELECT body FROM lmm_store.orders WHERE id=ANY($1)")
            .bind(ids)
            .fetch_all(&store.owner)
            .await
            .unwrap();
    assert_eq!(go_orders.len(), ids.len());
    let mut go_ids = HashSet::new();
    for order in go_orders {
        let id = order["id"].as_str().unwrap();
        assert!(go_ids.insert(id.to_owned()));
        let operations = per_order.get(id).unwrap();
        assert_eq!(order["state"], "refunded");
        assert_eq!(order["buyer"]["id"], store.buyer_id);
        assert_eq!(order["delivered"], true);
        assert_eq!(order["delivery"], "Synthetic mixed load");
        assert_eq!(order["quantity"], 1);
        assert_eq!(order["total"]["amount_minor"], 1);
        assert_eq!(order["total"]["currency"], "USD");
        for (field, kind) in [
            ("payment_ref", "pay"),
            ("settlement_ref", "settle"),
            ("refund_ref", "refund"),
        ] {
            assert_eq!(
                order[field],
                format!("commerce:journal:{}", operations[kind])
            );
        }
    }
    assert_eq!(go_ids.len(), ids.len());
    let ledger = Ledger::from_pool(store.pool.clone());
    let buyer = ledger.balance(store.buyer_account).await.unwrap();
    let seller = ledger.balance(store.seller_account).await.unwrap();
    assert_eq!((buyer.available_units, buyer.reserved_units), (500_000, 0));
    assert_eq!((seller.available_units, seller.reserved_units), (0, 0));
    balanced(&store.pool).await;
}
