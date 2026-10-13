//! Exact compiled projection SQL against private, freshly installed PostgreSQL.
use super::*;
use crate::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::IdentityStore,
};
use axum::{
    body::{Body, to_bytes},
    http::Request as HttpRequest,
};
use tower::ServiceExt;

struct Fixture {
    pool: PgPool,
    owner: i64,
    key: i64,
    secret: String,
    now: i64,
}
async fn fixture(pool: &PgPool) -> Fixture {
    crate::core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let session = identity.bootstrap_user(7, 0).await.unwrap();
    let owner = identity
        .authorize(&session.secret)
        .await
        .unwrap()
        .owner_account_id;
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
        .unwrap()
        .id;
    let now = sqlx::query_scalar("SELECT floor(extract(epoch FROM now()))::bigint")
        .fetch_one(pool)
        .await
        .unwrap();
    Fixture {
        pool: pool.clone(),
        owner,
        key,
        secret: session.secret,
        now,
    }
}
async fn requests(f: &Fixture, prefix: &str, count: i64, at: i64) {
    let mut tx = f.pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    let price = serde_json::to_value(Price {
        version: "index-fixture".into(),
        model: "test-model".into(),
        group: "default".into(),
        input_per_million: 0,
        cached_per_million: 0,
        output_per_million: 0,
        upstream_cost_group_ppm: None,
    })
    .unwrap();
    sqlx::query(r#"INSERT INTO core_charging.requests(id,actor_user_id,key_id,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,state,reserved)
      SELECT $1||lpad(g::text,6,'0'),7,$2,$3,$3,decode(repeat('03',32),'hex'),decode(repeat('03',32),'hex'),decode(repeat('03',32),'hex'),$4,$5,$5+300,$5+600,'reserved',1 FROM generate_series(1,$6::bigint) g"#)
        .bind(prefix).bind(f.key).bind(f.owner).bind(price).bind(at).bind(count).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
}
async fn commands(f: &Fixture, prefix: &str, count: i64, one_request: bool) {
    let mut tx = f.pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    let template = serde_json::to_value(LedgerCommand {
        operation_id: "0".repeat(64),
        request_id: "template".into(),
        actor_user_id: 7,
        key_id: f.key,
        owner_account_id: f.owner,
        payer_account_id: f.owner,
        price_version: "index-fixture".into(),
        source: Source::Wallet,
        change: Change::Reserve { total: 1 },
    })
    .unwrap();
    sqlx::query(r#"INSERT INTO core_charging.ledger_commands(operation_id,request_id,command)
      SELECT op,id,$4::jsonb || jsonb_build_object('operation_id',op,'request_id',id)
      FROM (SELECT md5($1||g::text)||md5('command:'||$1||g::text) AS op,
        $1||lpad((CASE WHEN $3 THEN 1 ELSE g END)::text,6,'0') AS id FROM generate_series(1,$2::bigint) g) s"#)
        .bind(prefix).bind(count).bind(one_request).bind(template).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
}
async fn endpoint(f: &Fixture, path: &str, suffix: &str) -> (StatusCode, Value) {
    let response = router(UsageStore::new(f.pool.clone()))
        .oneshot(
            HttpRequest::builder()
                .uri(format!(
                    "{path}?start_timestamp={}&end_timestamp={}&{suffix}",
                    f.now - 60,
                    f.now
                ))
                .header("authorization", format!("Bearer {}", f.secret))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let status = response.status();
    let body =
        serde_json::from_slice(&to_bytes(response.into_body(), 1 << 20).await.unwrap()).unwrap();
    (status, body)
}
fn command_nodes<'a>(plan: &'a Value, output: &mut Vec<&'a Value>) {
    if plan["Relation Name"] == "ledger_commands"
        || plan["Index Name"] == "ledger_commands_request_operation"
    {
        output.push(plan);
    }
    if let Some(children) = plan["Plans"].as_array() {
        for child in children {
            command_nodes(child, output);
        }
    }
}

#[sqlx::test(migrations = false)]
async fn exact_projection_uses_request_index_at_20k_and_40k_unrelated_history(pool: PgPool) {
    let f = fixture(&pool).await;
    requests(&f, "selected-", 1000, f.now).await;
    commands(&f, "selected-", 1000, false).await;
    for total in [20_000, 40_000] {
        let prefix = format!("history-{total}-");
        requests(&f, &prefix, 1, 1).await;
        commands(&f, &prefix, 20_000, true).await;
        sqlx::raw_sql("ANALYZE core_charging.requests; ANALYZE core_charging.ledger_commands")
            .execute(&pool)
            .await
            .unwrap();
        let sql = format!("EXPLAIN (ANALYZE, BUFFERS, FORMAT JSON) {SELECT_REQUESTS}");
        let plan: Value = sqlx::query_scalar(&sql)
            .bind(7i64)
            .bind(f.owner)
            .bind(f.now - 60)
            .bind(f.now)
            .bind(0i16)
            .bind("")
            .bind("")
            .bind("")
            .bind("")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(plan[0]["Plan"]["Actual Rows"].as_f64(), Some(1000.0));
        let mut nodes = Vec::new();
        command_nodes(&plan[0]["Plan"], &mut nodes);
        assert!(!nodes.is_empty());
        assert!(nodes.iter().all(|n| n["Node Type"] != "Seq Scan"), "{plan}");
        assert!(
            nodes
                .iter()
                .any(|n| n["Index Name"] == "ledger_commands_request_operation"
                    && n["Actual Loops"].as_f64() == Some(1000.0)
                    && n["Index Cond"]
                        .as_str()
                        .is_some_and(|s| s.contains("request_id = r.id"))),
            "{plan}"
        );
        println!(
            "history={total}, selected=1000, execution_ms={}, command_nodes={:?}",
            plan[0]["Execution Time"], nodes
        );
        if let Ok(directory) = std::env::var("LMM_USAGE_PLAN_DIR") {
            std::fs::write(
                std::path::Path::new(&directory).join(format!("index2-plan-{total}.json")),
                serde_json::to_vec_pretty(&plan).unwrap(),
            )
            .unwrap();
        }
        // Use the actual production five-second connection timeout, not a mock.
        let bounded = crate::config::database_pool(&test_url(&pool))
            .await
            .unwrap();
        let timeout: String = sqlx::query_scalar("SHOW statement_timeout")
            .fetch_one(&bounded)
            .await
            .unwrap();
        assert_eq!(timeout, "5s");
        let runtime = Fixture {
            pool: bounded.clone(),
            owner: f.owner,
            key: f.key,
            secret: f.secret.clone(),
            now: f.now,
        };
        let started = std::time::Instant::now();
        let (status, body) = endpoint(&runtime, "/api/log/self", "page_size=1").await;
        let elapsed = started.elapsed();
        assert!(elapsed < std::time::Duration::from_secs(5));
        println!(
            "history={total}, page_size=1, timeout={timeout}, endpoint_elapsed_ms={}",
            elapsed.as_millis()
        );
        assert_eq!(status, StatusCode::OK, "{body}");
        assert_eq!(body["data"]["total"], 1000);
        assert_eq!(body["data"]["items"].as_array().unwrap().len(), 1);
        bounded.close().await;
    }
}
fn test_url(pool: &PgPool) -> String {
    let mut url = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
    assert_eq!(url.host_str(), Some("127.0.0.1"));
    url.set_path(pool.connect_options().get_database().unwrap());
    url.to_string()
}

#[sqlx::test(migrations = false)]
async fn unmatched_command_outside_page_one_still_fails_all_projections(pool: PgPool) {
    let f = fixture(&pool).await;
    requests(&f, "selected-", 2, f.now).await;
    commands(&f, "selected-", 2, false).await;
    let (status, body) = endpoint(&f, "/api/log/self", "page_size=1").await;
    assert_eq!(status, StatusCode::OK, "{body}");
    assert_eq!(body["data"]["items"][0]["request_id"], "selected-000002");
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("UPDATE core_charging.ledger_commands SET command=jsonb_set(command,'{change}','{\"operation\":\"settle\",\"amount\":1}') WHERE request_id='selected-000001'").execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    for path in ["/api/log/self", "/api/log/self/stat", "/api/data/self"] {
        let (status, body) = endpoint(&f, path, "page_size=1").await;
        assert_eq!(status, StatusCode::SERVICE_UNAVAILABLE, "{body}");
        assert_eq!(body["success"], false);
        assert!(body.get("data").is_none());
    }
}

#[sqlx::test(migrations = false)]
async fn receipt_limit_distinguishes_2048_from_2049_without_silent_truncation(pool: PgPool) {
    let f = fixture(&pool).await;
    requests(&f, "selected-", 1, f.now).await;
    commands(&f, "selected-", 2048, true).await;
    let (status, body) = endpoint(&f, "/api/log/self", "page_size=1").await;
    assert_eq!(status, StatusCode::OK, "{body}");
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO core_charging.ledger_commands(operation_id,request_id,command) SELECT repeat('f',64),request_id,jsonb_set(command,'{operation_id}',to_jsonb(repeat('f',64))) FROM core_charging.ledger_commands LIMIT 1").execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    for path in ["/api/log/self", "/api/log/self/stat", "/api/data/self"] {
        let (status, body) = endpoint(&f, path, "page_size=1").await;
        assert_eq!(status, StatusCode::BAD_REQUEST, "{body}");
        assert_eq!(body["code"], "USAGE_WINDOW_TOO_DENSE");
        assert!(body.get("data").is_none());
    }
}

#[sqlx::test(migrations = false)]
async fn request_limit_distinguishes_100000_from_100001_without_silent_truncation(pool: PgPool) {
    let f = fixture(&pool).await;
    requests(&f, "selected-", 100000, f.now).await;
    let (status, body) = endpoint(&f, "/api/log/self", "page_size=1").await;
    assert_eq!(status, StatusCode::OK, "{body}");
    assert_eq!(body["data"]["total"], 100000);
    requests(&f, "extra-", 1, f.now).await;
    for path in ["/api/log/self", "/api/log/self/stat", "/api/data/self"] {
        let (status, body) = endpoint(&f, path, "page_size=1").await;
        assert_eq!(status, StatusCode::BAD_REQUEST, "{body}");
        assert_eq!(body["code"], "USAGE_WINDOW_TOO_DENSE");
        assert!(body.get("data").is_none());
    }
}
