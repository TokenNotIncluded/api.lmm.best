use super::support::*;
use crate::migration_routes::assistant::key_creation::confirm_pg;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn pg_options_lock_is_held_through_key_and_card_commit() {
    let Some(harness) = PgHarness::new().await else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return;
    };
    let store = harness.store();
    let action = prepare(&store, "lock-session", "default").await;
    let mut token_blocker = harness.pool.begin().await.expect("begin token blocker");
    sqlx::query("LOCK TABLE tokens IN ACCESS EXCLUSIVE MODE")
        .execute(&mut *token_blocker)
        .await
        .expect("block token access after option validation");

    let confirm_store = store.clone();
    let confirm_action = action.clone();
    let confirm = tokio::spawn(async move {
        confirm_pg(
            &confirm_store,
            authorization_fence("lock-session"),
            confirmation(&confirm_action),
            "",
        )
        .await
    });
    wait_for_granted_option_share(&harness.admin, &harness.schema).await;

    let update_pool = harness.pool.clone();
    let update = tokio::spawn(async move {
        sqlx::query("UPDATE options SET value = '{\"vip\":2}' WHERE key = 'GroupRatio'")
            .execute(&update_pool)
            .await
    });
    wait_for_option_update_lock(&harness.admin, &harness.schema).await;
    assert!(!update.is_finished());

    token_blocker
        .commit()
        .await
        .expect("release deterministic token barrier");
    confirm
        .await
        .expect("join confirmation task")
        .expect("confirmation commits under its authoritative snapshot");
    update
        .await
        .expect("join option update task")
        .expect("option update proceeds after confirmation commit");
    assert_eq!(count(&harness.pool, "tokens").await, 1);
    assert_eq!(count(&harness.pool, "assistant_secure_cards").await, 1);
    assert!(flow_consumed(&harness.pool, &action).await);

    harness.cleanup().await;
}
