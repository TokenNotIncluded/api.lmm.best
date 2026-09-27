use super::*;

fn user(now: i64) -> UserRecord {
    UserRecord {
        id: 7,
        username: "trust-owner".into(),
        password: String::new(),
        display_name: String::new(),
        role: 1,
        status: 1,
        email: String::new(),
        github_id: String::new(),
        discord_id: String::new(),
        oidc_id: String::new(),
        wechat_id: String::new(),
        telegram_id: String::new(),
        group: "default".into(),
        quota: 1000000,
        used_quota: 0,
        request_count: 0,
        aff_code: String::new(),
        aff_count: 0,
        aff_quota: 0,
        aff_history_quota: 0,
        inviter_id: 0,
        linux_do_id: String::new(),
        setting: "{}".into(),
        stripe_customer: String::new(),
        auth_version: 1,
        created_at: now,
        last_api_activity_at: now,
        trust_level_override: None,
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL via LMM_TEST_DATABASE_URL"]
async fn current_go_credit_history_drives_dashboard_trust_access_and_refund_transitions() {
    let url = std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL");
    let admin = PgPool::connect(&url).await.unwrap();
    let schema = format!("auth_trust_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let pool = sqlx::postgres::PgPoolOptions::new()
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
        .connect(&url)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE users(id BIGINT PRIMARY KEY,console_activated_at BIGINT,deleted_at TIMESTAMPTZ);CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT);CREATE TABLE tokens(user_id BIGINT,status BIGINT,deleted_at TIMESTAMPTZ);CREATE TABLE top_ups(id BIGSERIAL PRIMARY KEY,user_id BIGINT,status TEXT,payment_provider TEXT,payment_method TEXT,amount BIGINT,credited_quota BIGINT,money DOUBLE PRECISION,settled_amount_micros BIGINT,expected_amount_micros BIGINT,settlement_currency TEXT,create_time BIGINT,complete_time BIGINT);INSERT INTO users VALUES(7,0,NULL);INSERT INTO options VALUES('QuotaPerUnit','1000'),('developer_access_setting.paid_activation_enabled','true'),('developer_access_setting.paid_activation_min_amount','1');INSERT INTO tokens VALUES(7,1,NULL)").execute(&pool).await.unwrap();
    let now = unix_now();
    // The first payment grants $500 of API credit, despite only $1 paid to
    // the gateway. Points/internal rails, balance and pending rows never count.
    for (provider, method, amount, credited, money, settled, expected, currency, status) in [
        (
            "stripe", "stripe", 0, 500000, 1.0, 1000000, 1000000, "USD", "success",
        ),
        ("creem", "creem", 100000, 0, 1.0, 0, 0, "USD", "success"),
        ("epay", "alipay", 50, 0, 1.0, 0, 0, "CNY", "success"),
        ("", "stripe", 25, 0, 1.0, 0, 0, "USD", "success"),
        (
            "epay", "ldc", 0, 9999999, 1.0, 1000000, 1000000, "LDC", "success",
        ),
        ("epay", "epay", 0, 9999999, 1.0, 0, 0, "USD", "success"),
        (
            "balance", "balance", 0, 9999999, 1.0, 1000000, 1000000, "USD", "success",
        ),
        (
            "stripe", "stripe", 0, 9999999, 1.0, 1000000, 1000000, "USD", "pending",
        ),
    ] {
        sqlx::query("INSERT INTO top_ups(user_id,status,payment_provider,payment_method,amount,credited_quota,money,settled_amount_micros,expected_amount_micros,settlement_currency,create_time,complete_time) VALUES(7,$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)").bind(status).bind(provider).bind(method).bind(amount as i64).bind(credited as i64).bind(money).bind(settled as i64).bind(expected as i64).bind(currency).bind(now).execute(&pool).await.unwrap();
    }
    let auth = PgValkeyDashboardAuth::new(
        pool.clone(),
        redis::Client::open("redis://127.0.0.1:1/").unwrap(),
        AuthConfig {
            session_secret: SecretString::from("Trust-readonly-synthetic-session-secret-2026!"),
            ..Default::default()
        },
    )
    .unwrap();
    let mut owner = user(now);
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.paid_amount, 675.0);
    assert_eq!(view.trust_level_info.level, 3);
    assert_eq!(view.trust_level_info.discount_ratio, 0.94);
    assert!(view.developer_access_granted);
    assert!(view.onboarding.paid_activation_complete);
    sqlx::query("UPDATE top_ups SET status='refunded' WHERE id=1")
        .execute(&pool)
        .await
        .unwrap();
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.paid_amount, 175.0);
    assert_eq!(view.trust_level_info.level, 2);
    assert_eq!(view.trust_level_info.discount_ratio, 0.97);
    sqlx::query("UPDATE options SET value='500' WHERE key='developer_access_setting.paid_activation_min_amount'").execute(&pool).await.unwrap();
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.level, 0);
    assert!(!view.developer_access_granted);
    sqlx::query("UPDATE users SET console_activated_at=$1 WHERE id=7")
        .bind(now)
        .execute(&pool)
        .await
        .unwrap();
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.level, 2);
    assert!(view.developer_access_granted);
    assert!(!view.onboarding.paid_activation_complete);
    sqlx::query("UPDATE options SET value='false' WHERE key='developer_access_setting.paid_activation_enabled'").execute(&pool).await.unwrap();
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.level, 2);
    assert!(view.developer_access_granted);
    owner.created_at = now - 190 * 86400;
    owner.last_api_activity_at = owner.created_at;
    sqlx::query("UPDATE top_ups SET create_time=$1,complete_time=$1")
        .bind(owner.created_at)
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        auth.dashboard_user_view(owner.clone(), json!({}))
            .await
            .trust_level_info
            .level,
        1
    );
    owner.trust_level_override = Some(0);
    assert!(
        !auth
            .dashboard_user_view(owner.clone(), json!({}))
            .await
            .developer_access_granted
    );
    owner.trust_level_override = Some(4);
    let view = auth.dashboard_user_view(owner.clone(), json!({})).await;
    assert_eq!(view.trust_level_info.discount_ratio, 0.90);
    assert!(view.developer_access_granted);
    // Explicit role/override decisions must not depend on unavailable payment
    // history; the shared no-override path remains fail-closed on DB failure.
    sqlx::query("DROP TABLE top_ups")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        auth.dashboard_user_view(owner.clone(), json!({}))
            .await
            .trust_level_info
            .level,
        4
    );
    owner.trust_level_override = None;
    owner.role = 100;
    assert_eq!(
        auth.dashboard_user_view(owner.clone(), json!({}))
            .await
            .trust_level_info
            .level,
        6
    );
    pool.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
