//! The expected balances are produced by the real current Go BillingSession
//! against PostgreSQL, not a second Rust calculator or mocked repository.

use super::*;
use serde::Deserialize;
use sqlx::postgres::PgPoolOptions;

type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

#[derive(Debug, Deserialize)]
struct Grant {
    total: i64,
    used: i64,
    overflow: bool,
}

#[derive(Debug, Deserialize, PartialEq)]
struct Snapshot {
    wallet: i64,
    token_remain: i64,
    token_used: i64,
    subscriptions: Vec<i64>,
    ledger_status: String,
    ledger_actual_quota: i64,
    ledger_wallet_quota: i64,
}

#[derive(Debug, Deserialize)]
struct Case {
    name: String,
    preference: String,
    wallet: i64,
    token_quota: i64,
    token_unlimited: bool,
    grants: Vec<Grant>,
    budget: i64,
    grow: i64,
    actual: i64,
    refund: bool,
    delete_token_after_reserve: bool,
    source: String,
    reserved_quota: i64,
    error_code: String,
    error_status: u16,
    settle_error: bool,
    after_reserve: Snapshot,
    after_grow: Option<Snapshot>,
    after_final: Snapshot,
}

impl Case {
    fn request<'a>(
        &'a self,
        user: i64,
        token: i64,
        target: i64,
        existing: Option<&'a str>,
    ) -> Request<'a> {
        Request {
            request_id: &self.name,
            user_id: user,
            token_id: token,
            channel_id: 1,
            model_name: "oracle-model",
            using_group: "default",
            is_stream: true,
            expected_quota: target,
            free: false,
            price_snapshot: json!({"oracle":true}),
            existing_id: existing,
        }
    }
}

fn failure(error: OpenAiRelayFailure) -> std::io::Error {
    std::io::Error::other(format!("{}: {}", error.code, error.message))
}

async fn snapshot(pg: &PgPool, user: i64, token: i64, request: &str) -> TestResult<Snapshot> {
    let wallet = sqlx::query_scalar("SELECT quota FROM users WHERE id=$1")
        .bind(user)
        .fetch_one(pg)
        .await?;
    let (token_remain, token_used): (i64, i64) =
        sqlx::query_as("SELECT remain_quota,used_quota FROM tokens WHERE id=$1")
            .bind(token)
            .fetch_one(pg)
            .await?;
    let subscriptions = sqlx::query_scalar(
        "SELECT amount_used FROM user_subscriptions WHERE user_id=$1 ORDER BY id",
    )
    .bind(user)
    .fetch_all(pg)
    .await?;
    let ledger: Option<(String, i64, i64)> = sqlx::query_as("SELECT status,actual_quota,wallet_consumed FROM subscription_pre_consume_records WHERE request_id=$1")
        .bind(request).fetch_optional(pg).await?;
    let (ledger_status, ledger_actual_quota, ledger_wallet_quota) = ledger.unwrap_or_default();
    Ok(Snapshot {
        wallet,
        token_remain,
        token_used,
        subscriptions,
        ledger_status,
        ledger_actual_quota,
        ledger_wallet_quota,
    })
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn current_go_funding_vectors_match_real_postgres_reserve_settle_refund_and_grow()
-> TestResult {
    let raw = if let Ok(path) = std::env::var("LMM_RELAY_FUNDING_GO_VECTORS") {
        std::fs::read_to_string(path)?
    } else {
        include_str!("../../../../tests/behavior-oracle/fixtures/relay-funding.json").to_owned()
    };
    let cases: Vec<Case> = serde_json::from_str(&raw)?;
    assert_eq!(
        cases.len(),
        28,
        "regenerate all current-Go scenarios together"
    );
    let database_url = std::env::var("LMM_TEST_DATABASE_URL")?;
    let admin = PgPool::connect(&database_url).await?;
    let schema = format!("relay_funding_vectors_{}", uuid::Uuid::new_v4().simple());
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
    sqlx::raw_sql("CREATE TABLE users(id BIGINT PRIMARY KEY,quota BIGINT NOT NULL,setting TEXT NOT NULL,deleted_at TIMESTAMPTZ); CREATE TABLE tokens(id BIGINT PRIMARY KEY,user_id BIGINT,remain_quota BIGINT NOT NULL,used_quota BIGINT NOT NULL DEFAULT 0,unlimited_quota BOOLEAN NOT NULL,status BIGINT NOT NULL DEFAULT 1,expired_time BIGINT NOT NULL DEFAULT -1,deleted_at TIMESTAMPTZ,accessed_time BIGINT NOT NULL DEFAULT 0);")
        .execute(&pg).await?;
    sqlx::raw_sql(include_str!(
        "../../../../tests/behavior-oracle/fixtures/relay_subscription_schema.sql"
    ))
    .execute(&pg)
    .await?;
    sqlx::raw_sql(
        &include_str!("../../../../migrations/0014_relay_settlement.sql")
            .replace("__LMM_APP_SCHEMA__", &schema),
    )
    .execute(&pg)
    .await?;

    for (index, case) in cases.iter().enumerate() {
        let user = 1000 + i64::try_from(index)?;
        let token = 2000 + i64::try_from(index)?;
        sqlx::query("INSERT INTO users(id,quota,setting) VALUES($1,$2,$3)")
            .bind(user)
            .bind(case.wallet)
            .bind(json!({"billing_preference":case.preference}).to_string())
            .execute(&pg)
            .await?;
        sqlx::query(
            "INSERT INTO tokens(id,user_id,remain_quota,unlimited_quota) VALUES($1,$2,$3,$4)",
        )
        .bind(token)
        .bind(user)
        .bind(case.token_quota)
        .bind(case.token_unlimited)
        .execute(&pg)
        .await?;
        for (number, grant) in case.grants.iter().enumerate() {
            let id = 3000 + i64::try_from(index * 10 + number)?;
            sqlx::query("INSERT INTO subscription_plans(id) VALUES($1)")
                .bind(id)
                .execute(&pg)
                .await?;
            sqlx::query("INSERT INTO user_subscriptions(id,user_id,plan_id,amount_total,amount_used,start_time,end_time,allow_wallet_overflow) VALUES($1,$2,$1,$3,$4,$5,$6,$7)")
                .bind(id).bind(user).bind(grant.total).bind(grant.used).bind(epoch_seconds())
                .bind(epoch_seconds()+3600+i64::try_from(number)?).bind(grant.overflow).execute(&pg).await?;
        }
        // Go's ForcePreConsume and high-balance cases both use this exact
        // durable reservation: neither current backend has a trust bypass.
        let mut tx = pg.begin().await?;
        let result = reserve(&mut tx, case.request(user, token, case.budget, None)).await;
        let record = if case.error_code.is_empty() {
            let record = result.map_err(failure)?;
            tx.commit().await?;
            assert_eq!(record.funding_source, case.source, "{} source", case.name);
            assert_eq!(
                if record.funding_source == "subscription" {
                    record.subscription_reserved
                } else {
                    record.wallet_reserved
                },
                case.reserved_quota,
                "{} reserved quota",
                case.name
            );
            record
        } else {
            let error = result.expect_err(&case.name);
            assert_eq!(error.code, case.error_code, "{} error code", case.name);
            assert_eq!(
                error.status.as_u16(),
                case.error_status,
                "{} status",
                case.name
            );
            tx.rollback().await?;
            assert_eq!(
                snapshot(&pg, user, token, &case.name).await?,
                case.after_reserve,
                "{} rejected reserve",
                case.name
            );
            assert_eq!(
                snapshot(&pg, user, token, &case.name).await?,
                case.after_final,
                "{} rejected final",
                case.name
            );
            continue;
        };
        assert_eq!(
            snapshot(&pg, user, token, &case.name).await?,
            case.after_reserve,
            "{} after reserve",
            case.name
        );
        if case.grow > 0 {
            let mut tx = pg.begin().await?;
            reserve(
                &mut tx,
                case.request(user, token, case.grow, Some(&record.reservation_id)),
            )
            .await
            .map_err(failure)?;
            tx.commit().await?;
            assert_eq!(
                Some(snapshot(&pg, user, token, &case.name).await?),
                case.after_grow,
                "{} after grow",
                case.name
            );
        }
        if case.delete_token_after_reserve {
            sqlx::query("UPDATE tokens SET deleted_at=NOW() WHERE id=$1")
                .bind(token)
                .execute(&pg)
                .await?;
        }
        intent(
            &pg,
            &record.reservation_id,
            user,
            case.actual,
            &json!({}),
            &json!({}),
            case.refund,
        )
        .await
        .map_err(failure)?;
        let mut tx = pg.begin().await?;
        let result = finish(&mut tx, &record.reservation_id, user).await;
        if case.settle_error {
            assert!(result.is_err(), "{} retains unsettled intent", case.name);
            tx.rollback().await?;
        } else {
            assert!(
                result.map_err(failure)?.is_some(),
                "{} finalizes",
                case.name
            );
            tx.commit().await?;
            let mut replay = pg.begin().await?;
            assert!(
                finish(&mut replay, &record.reservation_id, user)
                    .await
                    .map_err(failure)?
                    .is_none(),
                "{} finalization replay",
                case.name
            );
            replay.commit().await?;
        }
        assert_eq!(
            snapshot(&pg, user, token, &case.name).await?,
            case.after_final,
            "{} final balances",
            case.name
        );
    }
    pg.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await?;
    admin.close().await;
    Ok(())
}
