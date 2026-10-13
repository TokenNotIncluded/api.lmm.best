//! Bounded regression fixtures for independent review of 44de9804b.
//! All corruptions affect only SQLx-created private databases. Normal money
//! receipts come from the real Charging + TransactionalLedger, never a mock.
use super::*;
use lmm_core::billing::charging::{Change, LedgerCommand, Source};
use sha2::{Digest, Sha256};

fn operation(id: &str, tag: &str) -> String {
    format!(
        "{:x}",
        Sha256::digest(serde_json::to_vec(&("core-charging-v1", id, tag)).unwrap())
    )
}
async fn unavailable(f: &Fixture, id: Option<&str>) {
    for path in PATHS {
        let path = if let Some(id) = id {
            format!("{path}?request_id={id}")
        } else {
            path.to_owned()
        };
        let (status, body) = get(&f.app, &path, Some(&f.session.secret)).await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE, "{path}: {body}");
        assert_eq!(body["success"], false);
        assert!(body.get("data").is_none());
    }
}
async fn mutate(pool: &PgPool, sql: &str, id: &str) {
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query(sql).bind(id).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
}
#[sqlx::test(migrations = false)]
async fn refund_receipts_require_complete_identity_and_no_unmatched_commands(pool: PgPool) {
    let f = fixture(&pool).await;
    for (id, sql) in [
        (
            "missing",
            "DELETE FROM core_charging.ledger_commands WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "amount",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{change,amount}','19') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "actor",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{actor_user_id}','8') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "key",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{key_id}','999') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "owner",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{owner_account_id}','999') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "payer",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{payer_account_id}','999') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "source",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{source}','{\"kind\":\"subscription\",\"id\":1,\"start\":0,\"end\":1}') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "source-extra",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{source,id}','999') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "price-version",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{price_version}','\"different\"') WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
        (
            "operation",
            "UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{operation_id}',to_jsonb(repeat('0',64))) WHERE request_id=$1 AND command->'change'->>'operation'='refund'",
        ),
    ] {
        settled(&f, &f.key.secret, id, Outcome::Completed).await;
        f.charging
            .refund(id, "refund", 20, "private refund evidence")
            .await
            .unwrap();
        mutate(&pool, sql, id).await;
        unavailable(&f, Some(id)).await;
    }
    settled(&f, &f.key.secret, "extra", Outcome::Completed).await;
    f.charging
        .refund("extra", "refund", 20, "private refund evidence")
        .await
        .unwrap();
    let command = LedgerCommand {
        operation_id: operation("extra", "refund:unmatched"),
        request_id: "extra".into(),
        actor_user_id: 7,
        key_id: f.key.id,
        owner_account_id: f.account,
        payer_account_id: f.account,
        price_version: "v1".into(),
        source: Source::Wallet,
        change: Change::Refund { amount: 1 },
    };
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.ledger_commands(operation_id,request_id,command) VALUES($1,'extra',$2)").bind(&command.operation_id).bind(serde_json::to_value(&command).unwrap()).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    unavailable(&f, Some("extra")).await;
}

#[sqlx::test(migrations = false)]
async fn partial_full_and_explicit_zero_settlements_have_distinct_billing_evidence(pool: PgPool) {
    let f = fixture(&pool).await;
    for (id, refund) in [("partial", 20), ("full", 49)] {
        settled(&f, &f.key.secret, id, Outcome::Completed).await;
        f.charging
            .refund(id, "refund", refund, "private refund evidence")
            .await
            .unwrap();
        let logs = ok(&f, &format!("/api/log/self?request_id={id}")).await;
        let row = &logs["items"][0];
        let other: Value = serde_json::from_str(row["other"].as_str().unwrap()).unwrap();
        for data in [row, &other] {
            assert_eq!(data["gross_settled_credits"], 49);
            assert_eq!(data["refunded_credits"], refund);
            assert_eq!(data["net_consumption_credits"], 49 - refund);
            assert_eq!(data["billing_basis"], "net_consumption");
            assert_eq!(data["wallet_debit_supported"], false);
        }
        assert_eq!(row["quota"], 49 - refund);
        assert_eq!(row["quota_money_basis"], "captured_credits_per_usd");
        for path in ["/api/log/self/stat", "/api/data/self"] {
            let data = ok(&f, &format!("{path}?request_id={id}")).await;
            let data = if data.is_array() { &data[0] } else { &data };
            assert_eq!(data["gross_settled_credits"], 49);
            assert_eq!(data["refunded_credits"], refund);
            assert_eq!(data["net_consumption_credits"], 49 - refund);
            assert_eq!(data["billing_basis"], "net_consumption");
        }
    }
    let mut request = spec("valid-zero");
    request.reserve = 0;
    request.price.input_per_million = 0;
    request.price.cached_per_million = 0;
    request.price.output_per_million = 0;
    actual_settle(&f, &request, counts()).await;
    for path in PATHS {
        let data = ok(&f, &format!("{path}?request_id=valid-zero")).await;
        let data = if path.ends_with("stat") {
            &data
        } else if path == "/api/data/self" {
            &data[0]
        } else {
            &data["items"][0]
        };
        assert_eq!(data["gross_settled_credits"], 0);
        assert_eq!(data["refunded_credits"], 0);
        assert_eq!(data["net_consumption_credits"], 0);
        assert_eq!(data["quota_money_available"], true);
    }
}
async fn actual_settle(f: &Fixture, request: &ChargeRequest, usage: Usage) {
    f.charging
        .reserve(&f.key.secret, request, &WORKER)
        .await
        .unwrap();
    f.charging.start(&request.id, &WORKER).await.unwrap();
    f.charging
        .settle(&request.id, &WORKER, usage, Outcome::Completed)
        .await
        .unwrap();
}
#[sqlx::test(migrations = false)]
async fn unsafe_token_rows_and_safe_rows_with_unsafe_aggregate_fail_uniformly(pool: PgPool) {
    let f = fixture(&pool).await;
    for (id, input, output) in [
        ("unsafe-input", 9_007_199_254_740_993, 0),
        ("bigint-overflow", i64::MAX, 0),
        ("unsafe-output", 0, 9_007_199_254_740_993),
        ("unsafe-token-sum", 9_007_199_254_740_991, 1),
    ] {
        let mut request = spec(id);
        request.reserve = 0;
        request.price.input_per_million = 0;
        request.price.cached_per_million = 0;
        request.price.output_per_million = 0;
        actual_settle(
            &f,
            &request,
            Usage {
                input,
                cached: 0,
                output,
                ..Default::default()
            },
        )
        .await;
        unavailable(&f, Some(id)).await;
    }
    for id in ["sum-a", "sum-b"] {
        let mut request = spec(id);
        request.reserve = 0;
        request.price.model = "aggregate-model".into();
        request.price.input_per_million = 0;
        request.price.cached_per_million = 0;
        request.price.output_per_million = 0;
        actual_settle(
            &f,
            &request,
            Usage {
                input: 4_503_599_627_370_496,
                cached: 0,
                output: 0,
                ..Default::default()
            },
        )
        .await;
        assert_eq!(
            get(
                &f.app,
                &format!("/api/log/self?request_id={id}"),
                Some(&f.session.secret)
            )
            .await
            .0,
            StatusCode::OK
        );
    }
    for path in PATHS {
        let (status, body) = get(
            &f.app,
            &format!("{path}?model_name=aggregate-model"),
            Some(&f.session.secret),
        )
        .await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert!(body.get("data").is_none());
    }
}
#[sqlx::test(migrations = false)]
async fn unsafe_reservations_and_gross_aggregate_fail_without_lossy_json(pool: PgPool) {
    let f = fixture(&pool).await;
    let result = f
        .ledger
        .execute(&LedgerRequest {
            scope: "usage-test".into(),
            operation_key: "large-fixture-funds".into(),
            actor_user_id: Some(7),
            reason: "Private numeric boundary fixture".into(),
            action: Action::Credit {
                account_id: f.account,
                amount_units: Credits::try_from(9_007_199_254_740_991).unwrap(),
                payment_reference: "private-numeric-funds".into(),
            },
        })
        .await
        .unwrap();
    assert!(matches!(result, LedgerOutcome::Posted { .. }));
    let mut held = spec("unsafe-reservation");
    held.reserve = 9_007_199_254_740_992;
    f.charging
        .reserve(&f.key.secret, &held, &WORKER)
        .await
        .unwrap();
    unavailable(&f, Some(&held.id)).await;
    f.charging.release(&held.id, &WORKER).await.unwrap();
    // Row-safe gross amounts sum beyond the JS boundary; none is refunded.
    for id in ["gross-a", "gross-b"] {
        let mut request = spec(id);
        request.reserve = 4_503_599_627_370_496;
        request.price.model = "gross-model".into();
        request.price.output_per_million = 0;
        actual_settle(
            &f,
            &request,
            Usage {
                input: request.reserve,
                cached: 0,
                output: 0,
                ..Default::default()
            },
        )
        .await;
    }
    for path in PATHS {
        assert_eq!(
            get(
                &f.app,
                &format!("{path}?model_name=gross-model"),
                Some(&f.session.secret)
            )
            .await
            .0,
            StatusCode::SERVICE_UNAVAILABLE
        );
    }
}

// Matching-looking persisted receipts are deliberately corrupted fixtures, not
// mocks. Production writes cannot mutate captured Price; no triggers are disabled.
async fn imported(
    pool: &PgPool,
    f: &Fixture,
    id: &str,
    price: Value,
    usage: Value,
    amount: i64,
    at: i64,
) {
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,state,reserved,input_tokens,cached_tokens,output_tokens,settled,outcome) VALUES($1,7,$2,$3,$3,$4,$4,$4,$5,$6,253402300798,253402300799,'settled',$7,$8,$9,$10,$7,'completed')")
        .bind(id).bind(f.key.id).bind(f.account).bind(vec![3u8;32]).bind(&price).bind(at).bind(amount).bind(counts().input).bind(counts().cached).bind(counts().output).execute(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO core_charging.usage_events(request_id,event_id,detail) VALUES($1,'final',$2)").bind(id).bind(json!({"kind":"final","usage":usage,"outcome":"completed","evidence":"private corruption fixture"})).execute(&mut *tx).await.unwrap();
    let command = LedgerCommand {
        operation_id: operation(id, "settle"),
        request_id: id.into(),
        actor_user_id: 7,
        key_id: f.key.id,
        owner_account_id: f.account,
        payer_account_id: f.account,
        price_version: price["version"].as_str().unwrap().into(),
        source: Source::Wallet,
        change: Change::Settle { amount },
    };
    sqlx::query("INSERT INTO core_charging.ledger_commands(operation_id,request_id,command) VALUES($1,$2,$3)").bind(&command.operation_id).bind(id).bind(serde_json::to_value(&command).unwrap()).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
}
#[sqlx::test(migrations = false)]
async fn complete_price_final_usage_and_cost_invariants_apply_to_all_endpoints(pool: PgPool) {
    let f = fixture(&pool).await;
    let at: i64 = sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
        .fetch_one(&pool)
        .await
        .unwrap();
    let price = serde_json::to_value(spec("unused").price).unwrap();
    let usage = serde_json::to_value(counts()).unwrap();
    let mut missing = price.clone();
    missing.as_object_mut().unwrap().remove("input_per_million");
    let mut negative = price.clone();
    negative["cached_per_million"] = json!(-1);
    let mut upstream = price.clone();
    upstream["upstream_cost_group_ppm"] = json!(1_000_000);
    upstream["model"] = json!("openrouter/auto-beta");
    for key in [
        "input_per_million",
        "cached_per_million",
        "output_per_million",
    ] {
        upstream[key] = json!(0);
    }
    for (id, price, usage, amount) in [
        ("missing-rate", missing, usage.clone(), 49),
        ("negative-rate", negative, usage.clone(), 49),
        ("wrong-cost", price.clone(), usage.clone(), 48),
        ("upstream-missing-cost", upstream.clone(), usage.clone(), 0),
    ] {
        imported(&pool, &f, id, price, usage, amount, at).await;
        unavailable(&f, Some(id)).await;
    }
    let mut required_missing = usage.clone();
    required_missing.as_object_mut().unwrap().remove("input");
    // Columns retain known counters; deleting the required final usage field
    // must be rejected rather than treated as zero by any projection.
    imported(
        &pool,
        &f,
        "missing-final-input",
        price.clone(),
        required_missing,
        49,
        at,
    )
    .await;
    unavailable(&f, Some("missing-final-input")).await;
    let mut upstream_usage = usage.clone();
    upstream_usage["upstream_cost_atto_usd"] = json!(1_000_000_000_000_000_000u64);
    let mut request = spec("valid-upstream");
    request.price = serde_json::from_value(upstream).unwrap();
    request.reserve = 500_000;
    actual_settle(
        &f,
        &request,
        serde_json::from_value(upstream_usage).unwrap(),
    )
    .await;
    for path in PATHS {
        assert_eq!(
            get(
                &f.app,
                &format!("{path}?request_id=valid-upstream"),
                Some(&f.session.secret)
            )
            .await
            .0,
            StatusCode::OK
        );
    }
}

#[sqlx::test(migrations = false)]
async fn identity_disabled_mount_and_dense_bucket_limit_are_explicit(pool: PgPool) {
    let f = fixture(&pool).await;
    let disabled = http::configured_router(pool.clone(), Capabilities::default());
    for path in PATHS {
        let (status, body) = get(&disabled, path, Some(&f.session.secret)).await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(body["error"]["code"], "core_migration_incomplete");
    }
    // 10,001 bounded imported request receipts, one model bucket each. No
    // production triggers are disabled and no wallet debit is claimed. ASCII
    // fixture IDs make this SHA input exactly Charging's compact JSON tuple.
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query(r#"INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id,
        fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,state,
        reserved,input_tokens,cached_tokens,output_tokens,settled,outcome)
        SELECT 'dense-'||n,7,$1,$2,$2,$3,$3,$3,jsonb_set($4,'{model}',to_jsonb('dense-model-'||n)),
        floor(extract(epoch FROM transaction_timestamp()))::bigint,253402300798,253402300799,
        'settled',49,10,2,20,49,'completed' FROM generate_series(1,10001) n"#)
        .bind(f.key.id).bind(f.account).bind(vec![3u8;32]).bind(serde_json::to_value(spec("unused").price).unwrap())
        .execute(&mut *tx).await.unwrap();
    sqlx::query(
        r#"INSERT INTO core_charging.usage_events(request_id,event_id,detail)
        SELECT id,'final',jsonb_build_object('kind','final','outcome','completed',
        'usage',jsonb_build_object('input',10,'cached',2,'output',20))
        FROM core_charging.requests WHERE left(id,6)='dense-'"#,
    )
    .execute(&mut *tx)
    .await
    .unwrap();
    sqlx::query(r#"WITH receipts AS (
        SELECT r.*,encode(sha256(convert_to('["core-charging-v1","'||id||'","settle"]','UTF8')),'hex') AS op
        FROM core_charging.requests r WHERE left(id,6)='dense-')
        INSERT INTO core_charging.ledger_commands(operation_id,request_id,command)
        SELECT op,id,jsonb_build_object('operation_id',op,'request_id',id,'actor_user_id',actor_user_id,
          'key_id',key_id,'owner_account_id',owner_account_id,'payer_account_id',payer_account_id,
          'price_version',price->>'version','source',jsonb_build_object('kind','wallet'),
          'change',jsonb_build_object('operation','settle','amount',49)) FROM receipts"#)
        .execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    let (status, body) = get(&f.app, "/api/data/self", Some(&f.session.secret)).await;
    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert_eq!(body["code"], "USAGE_WINDOW_TOO_DENSE");
    assert!(body.get("data").is_none());
    // A narrower request filter succeeds with the same validated native receipt.
    assert_eq!(
        ok(&f, "/api/data/self?request_id=dense-1").await[0]["gross_settled_credits"],
        49
    );
}

#[sqlx::test(migrations = false)]
async fn safe_reservations_with_unsafe_aggregate_fail_closed(pool: PgPool) {
    let f = fixture(&pool).await;
    let result = f
        .ledger
        .execute(&LedgerRequest {
            scope: "usage-test".into(),
            operation_key: "large-held-funds".into(),
            actor_user_id: Some(7),
            reason: "Private reserved sum fixture".into(),
            action: Action::Credit {
                account_id: f.account,
                amount_units: Credits::try_from(9_007_199_254_740_991).unwrap(),
                payment_reference: "private-reserved-funds".into(),
            },
        })
        .await
        .unwrap();
    assert!(matches!(result, LedgerOutcome::Posted { .. }));
    for id in ["held-a", "held-b"] {
        let mut request = spec(id);
        request.reserve = 4_503_599_627_370_496;
        f.charging
            .reserve(&f.key.secret, &request, &WORKER)
            .await
            .unwrap();
        assert_eq!(
            get(
                &f.app,
                &format!("/api/log/self?request_id={id}"),
                Some(&f.session.secret)
            )
            .await
            .0,
            StatusCode::OK
        );
    }
    unavailable(&f, None).await;
    for path in PATHS {
        let (status, body) = get(
            &f.app,
            &format!("{path}?token_name={}", "a".repeat(4097)),
            Some(&f.session.secret),
        )
        .await;
        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert_eq!(body["code"], "USAGE_INVALID");
        assert!(body.get("data").is_none());
    }
}
