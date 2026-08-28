use super::support::*;
use crate::migration_routes::assistant::key_creation::confirm_pg;

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn pg_options_lock_is_held_through_key_and_card_commit() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let store = harness.store()?;
    let action = prepare(&store, "lock-session", "default").await?;
    let mut token_blocker = harness.pool.begin().await?;
    sqlx::query("LOCK TABLE tokens IN ACCESS EXCLUSIVE MODE")
        .execute(&mut *token_blocker)
        .await?;

    let confirm_store = store.clone();
    let token = confirmation(&action)?;
    let fence = authorization_fence("lock-session")?;
    let confirm = tokio::spawn(async move { confirm_pg(&confirm_store, fence, token, "").await });
    wait_for_granted_option_share(&harness.admin, &harness.schema).await?;

    let update_pool = harness.pool.clone();
    let update = tokio::spawn(async move {
        sqlx::query("UPDATE options SET value = '{\"vip\":2}' WHERE key = 'GroupRatio'")
            .execute(&update_pool)
            .await
    });
    wait_for_option_update_lock(&harness.admin, &harness.schema).await?;
    assert!(!update.is_finished());

    token_blocker.commit().await?;
    confirm.await?.map_err(test_error)?;
    update.await??;
    assert_eq!(count(&harness.pool, "tokens").await?, 1);
    assert_eq!(count(&harness.pool, "assistant_secure_cards").await?, 1);
    assert!(flow_consumed(&harness.pool, &action).await?);

    harness.cleanup().await?;
    Ok(())
}
