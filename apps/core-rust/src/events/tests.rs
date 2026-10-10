use super::*;
use std::collections::HashSet;

async fn fresh(pool: &PgPool) -> EventStore {
    sqlx::raw_sql(SCHEMA).execute(pool).await.unwrap();
    EventStore::from_pool(pool.clone())
}
async fn provision(pool: &PgPool, consumer: &str, owner: &str, maximum: i64) {
    let mut tx = pool.begin().await.unwrap();
    EventStore::provision(
        &mut tx,
        consumer,
        owner,
        &["test.counter.v1".into()],
        maximum,
    )
    .await
    .unwrap();
    tx.commit().await.unwrap();
}
fn event<'a>(key: &'a str, payload: &'a [u8]) -> NewEvent<'a> {
    NewEvent {
        key,
        event_type: "test.counter.v1",
        schema_version: 1,
        resource_id: 9_007_199_254_740_993,
        resource_version: i64::MAX,
        payload,
    }
}
async fn publish(pool: &PgPool, key: &str, payload: &[u8]) -> i64 {
    let mut tx = pool.begin().await.unwrap();
    let id = EventStore::publish(&mut tx, event(key, payload))
        .await
        .unwrap();
    tx.commit().await.unwrap();
    id
}
async fn expire(pool: &PgPool) {
    sqlx::query(
        "UPDATE core_events.deliveries SET available_at=clock_timestamp()-interval '1 second'",
    )
    .execute(pool)
    .await
    .unwrap();
}
async fn count(pool: &PgPool, query: &str) -> i64 {
    sqlx::query_scalar(query).fetch_one(pool).await.unwrap()
}
#[test]
fn invalid_envelopes_and_versions_are_rejected() {
    let mut e = event("key", b"payload");
    assert!(e.validate().is_ok());
    e.resource_id = 0;
    assert_eq!(e.validate(), Err(EventError::Invalid));
    e = event("key", b"payload");
    e.schema_version = 0;
    assert!(e.validate().is_err());
    e = event("key", b"payload");
    e.event_type = "*";
    assert!(e.validate().is_err());
    assert!(event("bad key", b"").validate().is_err());
    assert!(event("key", &vec![0; MAX_PAYLOAD + 1]).validate().is_err());
    let original = event("key", b"\x10\x81\x80\x80\x80\x80\x80\x80\x10\x98\x06\x01");
    assert_ne!(
        original.fingerprint(),
        event("other", original.payload).fingerprint()
    );
    assert_eq!(original.fingerprint(), original.fingerprint());
}
#[sqlx::test(migrations = false)]
async fn business_and_outbox_rollback_together_and_late_subscription_recovers(pool: PgPool) {
    let store = fresh(&pool).await;
    sqlx::raw_sql("CREATE TABLE effect(n bigint NOT NULL); INSERT INTO effect VALUES(0)")
        .execute(&pool)
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE effect SET n=n+1")
        .execute(&mut *tx)
        .await
        .unwrap();
    EventStore::publish(&mut tx, event("rolled-back", b"x"))
        .await
        .unwrap();
    tx.rollback().await.unwrap();
    assert_eq!(
        count(&pool, "SELECT count(*) FROM core_events.outbox").await,
        0
    );
    assert_eq!(count(&pool, "SELECT n FROM effect").await, 0);
    let payload = b"\x10\x81\x80\x80\x80\x80\x80\x80\x10\x98\x06\x01";
    let id = publish(&pool, "accepted-without-live-go", payload).await;
    assert_eq!(
        count(&pool, "SELECT pending_events FROM core_events.capacity").await,
        1
    );
    provision(&pool, "consumer", "extensions", 10).await;
    let d = store
        .pull("extensions", "consumer", 8)
        .await
        .unwrap()
        .pop()
        .unwrap();
    assert_eq!(d.event.id, id);
    assert_eq!(d.event.payload, payload);
    assert_eq!(d.event.resource_id, 9_007_199_254_740_993);
    assert_eq!(d.event.resource_version, i64::MAX);
    assert!(
        !store
            .acknowledge("extensions", "consumer", id, &d.lease_token)
            .await
            .unwrap()
    );
    assert_eq!(
        count(&pool, "SELECT pending_bytes FROM core_events.capacity").await,
        0
    );
    assert_eq!(
        count(
            &pool,
            "SELECT count(*) FROM core_events.outbox WHERE payload IS NOT NULL"
        )
        .await,
        0
    );
    assert_eq!(
        publish(&pool, "accepted-without-live-go", payload).await,
        id
    );
    assert!(
        store
            .pull("extensions", "consumer", 8)
            .await
            .unwrap()
            .is_empty()
    );
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        EventStore::publish(&mut tx, event("accepted-without-live-go", b"different")).await,
        Err(EventError::Conflict)
    );
}
#[sqlx::test(migrations = false)]
async fn disconnect_reclaim_stale_ack_lost_ack_and_durable_retry(pool: PgPool) {
    let store = fresh(&pool).await;
    provision(&pool, "consumer", "extensions", 10).await;
    let id = publish(&pool, "event", b"payload").await;
    let first = store
        .pull("extensions", "consumer", 1)
        .await
        .unwrap()
        .pop()
        .unwrap();
    assert!(
        store
            .pull("extensions", "consumer", 1)
            .await
            .unwrap()
            .is_empty()
    );
    let reopened = EventStore::from_pool(pool.clone());
    assert!(
        reopened
            .pull("extensions", "consumer", 1)
            .await
            .unwrap()
            .is_empty()
    );
    expire(&pool).await;
    let second = reopened
        .pull("extensions", "consumer", 1)
        .await
        .unwrap()
        .pop()
        .unwrap();
    assert_eq!(second.attempt, 2);
    assert_ne!(first.lease_token, second.lease_token);
    assert_eq!(
        reopened
            .acknowledge("extensions", "consumer", id, &first.lease_token)
            .await,
        Err(EventError::LeaseLost)
    );
    assert!(
        !reopened
            .retry("extensions", "consumer", id, &second.lease_token, 1)
            .await
            .unwrap()
    );
    let when: String = sqlx::query_scalar("SELECT available_at::text FROM core_events.deliveries")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(
        !reopened
            .retry("extensions", "consumer", id, &second.lease_token, 1)
            .await
            .unwrap()
    );
    let again: String = sqlx::query_scalar("SELECT available_at::text FROM core_events.deliveries")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(when, again);
    assert!(
        reopened
            .pull("extensions", "consumer", 1)
            .await
            .unwrap()
            .is_empty()
    );
    assert_eq!(
        reopened
            .acknowledge("extensions", "consumer", id, &second.lease_token)
            .await,
        Err(EventError::LeaseLost)
    );
    expire(&pool).await;
    let third = reopened
        .pull("extensions", "consumer", 1)
        .await
        .unwrap()
        .pop()
        .unwrap();
    // Discard the first ACK result: a repeat after restart must still succeed.
    reopened
        .acknowledge("extensions", "consumer", id, &third.lease_token)
        .await
        .unwrap();
    let reopened = EventStore::from_pool(pool.clone());
    assert!(
        reopened
            .acknowledge("extensions", "consumer", id, &third.lease_token)
            .await
            .unwrap()
    );
    assert!(
        reopened
            .retry("extensions", "consumer", id, &third.lease_token, 1)
            .await
            .unwrap()
    );
    assert_eq!(
        count(&pool, "SELECT pending_events FROM core_events.capacity").await,
        0
    );
    assert_eq!(
        count(&pool, "SELECT pending FROM core_events.subscriptions").await,
        0
    );
}
#[sqlx::test(migrations = false)]
async fn service_scope_and_consumer_backlog_limit_cannot_be_bypassed(pool: PgPool) {
    let store = fresh(&pool).await;
    provision(&pool, "a", "extensions", 1).await;
    provision(&pool, "b", "other", 2).await;
    assert_eq!(
        store.event_types("extensions", "b").await,
        Err(EventError::Forbidden)
    );
    assert!(matches!(
        store.pull("extensions", "b", 1).await,
        Err(EventError::Forbidden)
    ));
    assert!(matches!(
        store.pull("extensions", "missing", 1).await,
        Err(EventError::Missing)
    ));
    let id = publish(&pool, "one", b"abc").await;
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        EventStore::publish(&mut tx, event("two", b"abc")).await,
        Err(EventError::Full)
    );
    tx.rollback().await.unwrap();
    assert_eq!(
        count(&pool, "SELECT count(*) FROM core_events.outbox").await,
        1
    );
    let a = store
        .pull("extensions", "a", 1)
        .await
        .unwrap()
        .pop()
        .unwrap();
    assert_eq!(
        store.acknowledge("other", "a", id, &a.lease_token).await,
        Err(EventError::Forbidden)
    );
    assert_eq!(
        store.acknowledge("extensions", "a", id, &[0; 32]).await,
        Err(EventError::LeaseLost)
    );
    store
        .acknowledge("extensions", "a", id, &a.lease_token)
        .await
        .unwrap();
    assert_eq!(
        count(&pool, "SELECT pending_events FROM core_events.capacity").await,
        1
    );
    let b = store.pull("other", "b", 1).await.unwrap().pop().unwrap();
    store
        .acknowledge("other", "b", id, &b.lease_token)
        .await
        .unwrap();
    assert_eq!(
        count(&pool, "SELECT pending_events FROM core_events.capacity").await,
        0
    );
}
#[sqlx::test(migrations = false)]
async fn global_count_bytes_and_response_limits_are_enforced(pool: PgPool) {
    let store = fresh(&pool).await;
    provision(&pool, "a", "extensions", 10).await;
    sqlx::query("UPDATE core_events.capacity SET max_events=1,max_bytes=3")
        .execute(&pool)
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        EventStore::publish(&mut tx, event("too-large", b"1234")).await,
        Err(EventError::Full)
    );
    tx.rollback().await.unwrap();
    let id = publish(&pool, "one", b"123").await;
    assert_eq!(publish(&pool, "one", b"123").await, id);
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        EventStore::publish(&mut tx, event("two", b"")).await,
        Err(EventError::Full)
    );
    tx.rollback().await.unwrap();
    assert!(matches!(
        store.pull("extensions", "a", 0).await,
        Err(EventError::Invalid)
    ));
    assert!(matches!(
        store.pull("extensions", "a", 9).await,
        Err(EventError::Invalid)
    ));
    let d = store
        .pull("extensions", "a", 1)
        .await
        .unwrap()
        .pop()
        .unwrap();
    store
        .acknowledge("extensions", "a", id, &d.lease_token)
        .await
        .unwrap();
    sqlx::query("UPDATE core_events.capacity SET max_events=10,max_bytes=1000000")
        .execute(&pool)
        .await
        .unwrap();
    for n in 0..8 {
        publish(&pool, &format!("large:{n}"), &vec![n; MAX_PAYLOAD]).await;
    }
    let ds = store.pull("extensions", "a", 8).await.unwrap();
    assert!(!ds.is_empty() && ds.len() < 8);
    assert!(
        ds.iter()
            .map(|d| d.event.payload.len() + d.event.key.len() + d.event.event_type.len() + 256)
            .sum::<usize>()
            <= MAX_BATCH_BYTES
    );
}
#[sqlx::test(migrations = false)]
async fn concurrent_claims_do_not_share_leases(pool: PgPool) {
    let store = fresh(&pool).await;
    provision(&pool, "a", "extensions", 10).await;
    for n in 0..8 {
        publish(&pool, &format!("event:{n}"), b"payload").await;
    }
    let mut jobs = Vec::new();
    for _ in 0..8 {
        let s = store.clone();
        jobs.push(tokio::spawn(async move {
            s.pull("extensions", "a", 1).await.unwrap()
        }));
    }
    let mut ids = HashSet::new();
    for job in jobs {
        for d in job.await.unwrap() {
            assert!(ids.insert(d.event.id));
        }
    }
    assert_eq!(ids.len(), 8);
}
#[sqlx::test(migrations = false)]
async fn busy_publisher_returns_without_waiting_for_business_transaction(pool: PgPool) {
    fresh(&pool).await;
    let mut first = pool.begin().await.unwrap();
    EventStore::publish(&mut first, event("held", b"payload"))
        .await
        .unwrap();
    let mut second = pool.begin().await.unwrap();
    let result = tokio::time::timeout(
        Duration::from_millis(500),
        EventStore::publish(&mut second, event("next", b"payload")),
    )
    .await
    .unwrap();
    assert_eq!(result, Err(EventError::Busy));
    second.rollback().await.unwrap();
    first.commit().await.unwrap();
}
fn command_key(actor: i64) -> CommandKey<'static> {
    CommandKey {
        service_id: "extensions",
        actor_id: actor,
        method: "/lmm.core.v1.Test/Increment",
        idempotency_key: "test-command-key-001",
    }
}
async fn command(pool: PgPool, actor: i64) -> InboxOutcome {
    let mut tx = pool.begin().await.unwrap();
    let key = command_key(actor);
    let outcome = begin_command(&mut tx, &key, b"canonical-request-v1")
        .await
        .unwrap();
    if outcome == InboxOutcome::New {
        sqlx::query("UPDATE effect SET n=n+1")
            .execute(&mut *tx)
            .await
            .unwrap();
        complete_command(&mut tx, &key, b"saved-response")
            .await
            .unwrap();
    }
    tx.commit().await.unwrap();
    outcome
}
#[sqlx::test(migrations = false)]
async fn concurrent_commands_replay_once_with_actor_scope_and_content_check(pool: PgPool) {
    fresh(&pool).await;
    sqlx::raw_sql("CREATE TABLE effect(n bigint NOT NULL); INSERT INTO effect VALUES(0)")
        .execute(&pool)
        .await
        .unwrap();
    let mut jobs = Vec::new();
    for _ in 0..8 {
        jobs.push(tokio::spawn(command(pool.clone(), 9_007_199_254_740_993)));
    }
    let mut applied = 0;
    for j in jobs {
        if j.await.unwrap() == InboxOutcome::New {
            applied += 1;
        }
    }
    assert_eq!(applied, 1);
    assert_eq!(count(&pool, "SELECT n FROM effect").await, 1);
    assert_eq!(
        command(pool.clone(), 9_007_199_254_740_993).await,
        InboxOutcome::Replay(b"saved-response".to_vec())
    );
    assert_eq!(command(pool.clone(), 2).await, InboxOutcome::New);
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        begin_command(&mut tx, &command_key(2), b"different").await,
        Err(EventError::Conflict)
    );
}
#[sqlx::test(migrations = false)]
async fn incomplete_inbox_forces_whole_transaction_rollback(pool: PgPool) {
    fresh(&pool).await;
    sqlx::raw_sql("CREATE TABLE effect(n bigint NOT NULL); INSERT INTO effect VALUES(0)")
        .execute(&pool)
        .await
        .unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(
        begin_command(&mut tx, &command_key(1), b"canonical-request-v1")
            .await
            .unwrap(),
        InboxOutcome::New
    );
    sqlx::query("UPDATE effect SET n=1")
        .execute(&mut *tx)
        .await
        .unwrap();
    assert!(tx.commit().await.is_err());
    assert_eq!(count(&pool, "SELECT n FROM effect").await, 0);
    assert_eq!(
        count(&pool, "SELECT count(*) FROM core_events.inbox").await,
        0
    );
    assert_eq!(command(pool.clone(), 1).await, InboxOutcome::New);
}
