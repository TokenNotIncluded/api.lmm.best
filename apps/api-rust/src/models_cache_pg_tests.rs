use super::*;
use crate::routes::api_token::{
    ApiTokenHttpState, ApiTokenPrincipal, PgValkeyApiTokenService, api_token_router,
};
use axum::{
    body::{Body, to_bytes},
    http::Request,
};
use tower::ServiceExt;

#[tokio::test]
#[ignore = "requires isolated PostgreSQL and Valkey via LMM_TEST_DATABASE_URL and LMM_AUTH_TEST_VALKEY_URL"]
async fn current_token_cache_fences_old_readers_and_preserves_reserved_quota() {
    let database = std::env::var("LMM_TEST_DATABASE_URL").expect("isolated PostgreSQL");
    let valkey_url = std::env::var("LMM_AUTH_TEST_VALKEY_URL").expect("isolated Valkey");
    let admin = PgPool::connect(&database).await.unwrap();
    let schema = format!("current_token_cache_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let pg = sqlx::postgres::PgPoolOptions::new()
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
        .connect(&database)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT);CREATE TABLE tokens(id BIGINT PRIMARY KEY,user_id BIGINT,key TEXT,status BIGINT,name TEXT,created_time BIGINT,accessed_time BIGINT,expired_time BIGINT,remain_quota BIGINT,unlimited_quota BOOLEAN,model_limits_enabled BOOLEAN,model_limits TEXT,allow_ips TEXT,used_quota BIGINT,\"group\" TEXT,cross_group_retry BOOLEAN,deleted_at TIMESTAMPTZ)").execute(&pg).await.unwrap();
    let key = uuid::Uuid::new_v4().simple().to_string();
    sqlx::query("INSERT INTO tokens VALUES(17,7,$1,1,'cache',0,0,-1,100,FALSE,FALSE,'','',0,'default',FALSE,NULL)").bind(&key).execute(&pg).await.unwrap();
    let valkey = redis::Client::open(valkey_url).unwrap();
    let mut connection = valkey.get_multiplexed_async_connection().await.unwrap();
    let models = PgModelsService::with_valkey(
        pg.clone(),
        valkey.clone(),
        "current-cache-fixture",
        Duration::from_secs(60),
    );
    let cache_key = models.token_key(&key).unwrap();
    let fence_key = cache_key.replacen("token:", "token:fence:", 1);
    let stale = sqlx::query("SELECT *,status::INT4 AS status FROM tokens WHERE id=17")
        .fetch_one(&pg)
        .await
        .unwrap();
    models.store_token(&key, &stale).await;
    assert_eq!(
        redis::cmd("HGET")
            .arg(&cache_key)
            .arg("RemainQuota")
            .query_async::<i64>(&mut connection)
            .await
            .unwrap(),
        100
    );

    // A DB reader paused before a reservation must not overwrite the live
    // exhausted hash when it eventually publishes its older row snapshot.
    redis::cmd("HSET")
        .arg(&cache_key)
        .arg("RemainQuota")
        .arg(0)
        .arg("UsedQuota")
        .arg(100)
        .query_async::<()>(&mut connection)
        .await
        .unwrap();
    models.store_token(&key, &stale).await;
    let quota: Vec<i64> = redis::cmd("HMGET")
        .arg(&cache_key)
        .arg("RemainQuota")
        .arg("UsedQuota")
        .query_async(&mut connection)
        .await
        .unwrap();
    assert_eq!(quota, [0, 100]);
    assert!(
        redis::cmd("TTL")
            .arg(&cache_key)
            .query_async::<i64>(&mut connection)
            .await
            .unwrap()
            >= 58
    );

    let router = api_token_router(ApiTokenHttpState::new(Arc::new(
        PgValkeyApiTokenService::new(pg.clone(), valkey)
            .with_crypto_secret("current-cache-fixture"),
    )));
    let principal = ApiTokenPrincipal {
        user_id: 7,
        role: 1,
        preferred_language: None,
    };
    // Hold the durable row lock: the updater must publish its fence before
    // it waits for the write. Replay a real older PgRow in that exact gap.
    let mut blocker = pg.begin().await.unwrap();
    sqlx::query("SELECT id FROM tokens WHERE id=17 FOR UPDATE")
        .fetch_one(&mut *blocker)
        .await
        .unwrap();
    let updating = tokio::spawn(
        router.clone().oneshot(
            Request::builder()
                .method("PUT")
                .uri("/api/token/?status_only=1")
                .extension(principal)
                .header("content-type", "application/json")
                .body(Body::from(r#"{"id":17,"status":2}"#))
                .unwrap(),
        ),
    );
    let deadline = tokio::time::Instant::now() + Duration::from_secs(3);
    loop {
        if redis::cmd("EXISTS")
            .arg(&fence_key)
            .query_async::<bool>(&mut connection)
            .await
            .unwrap()
        {
            break;
        }
        assert!(
            tokio::time::Instant::now() < deadline,
            "token mutation did not fence before waiting for database row lock"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    assert!(!updating.is_finished());
    models.store_token(&key, &stale).await;
    assert!(
        !redis::cmd("EXISTS")
            .arg(&cache_key)
            .query_async::<bool>(&mut connection)
            .await
            .unwrap()
    );
    blocker.commit().await.unwrap();
    let response = updating.await.unwrap().unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let body: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), usize::MAX).await.unwrap()).unwrap();
    assert_eq!(body["success"], true, "{body}");
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT status FROM tokens WHERE id=17")
            .fetch_one(&pg)
            .await
            .unwrap(),
        2
    );
    models.store_token(&key, &stale).await;
    assert!(
        !redis::cmd("EXISTS")
            .arg(&cache_key)
            .query_async::<bool>(&mut connection)
            .await
            .unwrap()
    );

    // After the fence expires, hydration uses the committed row. An exhausted
    // token remains exhausted even if an older reader still tries to fill it.
    redis::cmd("DEL")
        .arg(&fence_key)
        .query_async::<()>(&mut connection)
        .await
        .unwrap();
    sqlx::query("UPDATE tokens SET status=4,remain_quota=0,used_quota=100 WHERE id=17")
        .execute(&pg)
        .await
        .unwrap();
    let committed = sqlx::query("SELECT *,status::INT4 AS status FROM tokens WHERE id=17")
        .fetch_one(&pg)
        .await
        .unwrap();
    models.store_token(&key, &committed).await;
    models.store_token(&key, &stale).await;
    let status_quota: Vec<i64> = redis::cmd("HMGET")
        .arg(&cache_key)
        .arg("Status")
        .arg("RemainQuota")
        .arg("UsedQuota")
        .query_async(&mut connection)
        .await
        .unwrap();
    assert_eq!(status_quota, [4, 0, 100]);
    // Incomplete historical hashes must be replaced rather than made immortal.
    redis::cmd("HDEL")
        .arg(&cache_key)
        .arg("Id")
        .query_async::<()>(&mut connection)
        .await
        .unwrap();
    models.store_token(&key, &committed).await;
    assert_eq!(
        redis::cmd("HGET")
            .arg(&cache_key)
            .arg("Id")
            .query_async::<i64>(&mut connection)
            .await
            .unwrap(),
        17
    );
    let deleted = router
        .clone()
        .oneshot(
            Request::builder()
                .method("DELETE")
                .uri("/api/token/17")
                .extension(principal)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    let body: Value =
        serde_json::from_slice(&to_bytes(deleted.into_body(), usize::MAX).await.unwrap()).unwrap();
    assert_eq!(body["success"], true, "{body}");
    assert!(
        redis::cmd("EXISTS")
            .arg(&fence_key)
            .query_async::<bool>(&mut connection)
            .await
            .unwrap()
    );
    models.store_token(&key, &committed).await;
    assert!(
        !redis::cmd("EXISTS")
            .arg(&cache_key)
            .query_async::<bool>(&mut connection)
            .await
            .unwrap()
    );
    redis::cmd("DEL")
        .arg(&cache_key)
        .arg(&fence_key)
        .query_async::<()>(&mut connection)
        .await
        .unwrap();
    drop(router);
    drop(models);
    pg.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
