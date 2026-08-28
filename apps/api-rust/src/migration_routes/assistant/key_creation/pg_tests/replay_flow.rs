use std::sync::Arc;

use tokio::sync::Barrier;

use super::support::*;
use crate::migration_routes::assistant::key_creation::repository::ZERO_RATIO_WARNING_MESSAGE;
use crate::migration_routes::assistant::key_creation::{
    KeyCreationError, confirm_pg, load_pg_options,
};

#[tokio::test]
async fn pg_confirmation_is_session_bound_expiring_replay_safe_and_exactly_once() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let store = harness.store()?;

    let wrong_session = prepare(&store, "session-a", "default").await?;
    assert_eq!(
        confirm_action(&store, "session-b", &wrong_session, "").await?,
        Err(KeyCreationError::InvalidConfirmation)
    );
    assert!(!flow_consumed(&harness.pool, &wrong_session).await?);

    sqlx::query("UPDATE auth_flows SET expires_at = NOW() - INTERVAL '1 second'")
        .execute(&harness.pool)
        .await?;
    assert_eq!(
        confirm_action(&store, "session-a", &wrong_session, "").await?,
        Err(KeyCreationError::InvalidConfirmation)
    );

    let action = prepare(&store, "session-a", "default").await?;

    let barrier = Arc::new(Barrier::new(3));
    let mut tasks = Vec::new();
    for _ in 0..2 {
        let store = store.clone();
        let barrier = Arc::clone(&barrier);
        let token = confirmation(&action)?;
        let fence = authorization_fence("session-a")?;
        tasks.push(tokio::spawn(async move {
            barrier.wait().await;
            confirm_pg(&store, fence, token, "").await
        }));
    }
    barrier.wait().await;
    let first = tasks.remove(0).await?;
    let second = tasks.remove(0).await?;
    assert_eq!(usize::from(first.is_ok()) + usize::from(second.is_ok()), 1);
    assert!(matches!(
        first.as_ref().err().or_else(|| second.as_ref().err()),
        Some(KeyCreationError::InvalidConfirmation)
    ));
    assert_eq!(count(&harness.pool, "tokens").await?, 1);
    assert_eq!(count(&harness.pool, "assistant_secure_cards").await?, 1);
    assert!(flow_consumed(&harness.pool, &action).await?);

    harness.cleanup().await?;
    Ok(())
}

#[tokio::test]
async fn pg_confirmation_revalidates_group_ratio_warning_and_user_state() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let store = harness.store()?;

    let stale_group = prepare(&store, "group-session", "vip").await?;
    sqlx::query(
        "UPDATE options SET value = '{\"default\":\"Default\"}' WHERE key = 'UserUsableGroups'",
    )
    .execute(&harness.pool)
    .await?;
    assert_eq!(
        confirm_action(&store, "group-session", &stale_group, "").await?,
        Err(KeyCreationError::InvalidGroup)
    );
    assert!(!flow_consumed(&harness.pool, &stale_group).await?);
    sqlx::query("UPDATE options SET value = '{\"default\":\"Default\",\"vip\":\"VIP\"}' WHERE key = 'UserUsableGroups'")
        .execute(&harness.pool)
        .await?;

    let stale_ratio = prepare(&store, "ratio-session", "default").await?;
    sqlx::query("UPDATE options SET value = '{\"vip\":2}' WHERE key = 'GroupRatio'")
        .execute(&harness.pool)
        .await?;
    assert_eq!(
        confirm_action(&store, "ratio-session", &stale_ratio, "").await?,
        Err(KeyCreationError::InvalidGroup)
    );
    assert!(!flow_consumed(&harness.pool, &stale_ratio).await?);

    sqlx::query("UPDATE options SET value = '{\"default\":1,\"vip\":2}' WHERE key = 'GroupRatio'")
        .execute(&harness.pool)
        .await?;
    sqlx::query("UPDATE options SET value = '{\"vip\":{\"enabled\":true,\"message\":\"first warning\",\"mode\":\"confirm\",\"confirmations\":2}}' WHERE key = 'group_ratio_setting.group_warnings'")
        .execute(&harness.pool)
        .await?;
    let stale_warning = prepare(&store, "warning-session", "vip").await?;
    sqlx::query("UPDATE options SET value = '{\"vip\":{\"enabled\":true,\"message\":\"changed warning\",\"mode\":\"confirm\",\"confirmations\":3}}' WHERE key = 'group_ratio_setting.group_warnings'")
        .execute(&harness.pool)
        .await?;
    assert_eq!(
        confirm_action(&store, "warning-session", &stale_warning, "").await?,
        Err(KeyCreationError::WarningChanged)
    );
    assert!(!flow_consumed(&harness.pool, &stale_warning).await?);

    sqlx::query("UPDATE options SET value = '{}' WHERE key = 'UserUsableGroups'")
        .execute(&harness.pool)
        .await?;
    sqlx::query("INSERT INTO options (key, value) VALUES ('group_ratio_setting.group_special_usable_group', '{\"default\":{\"vip\":\"VIP\"}}')")
        .execute(&harness.pool)
        .await?;
    let stale_user_group = prepare(&store, "user-group-session", "vip").await?;
    sqlx::query("UPDATE users SET \"group\" = 'other' WHERE id = 7")
        .execute(&harness.pool)
        .await?;
    assert_eq!(
        confirm_action(&store, "user-group-session", &stale_user_group, "").await?,
        Err(KeyCreationError::InvalidGroup)
    );
    assert_eq!(count(&harness.pool, "tokens").await?, 0);

    harness.cleanup().await?;
    Ok(())
}

#[tokio::test]
async fn pg_zero_ratio_warning_matches_go_default_and_explicit_disable_wins() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let store = harness.store()?;
    sqlx::query("UPDATE options SET value = '{\"default\":0,\"vip\":2}' WHERE key = 'GroupRatio'")
        .execute(&harness.pool)
        .await?;
    let options = load_pg_options(&store, "default")
        .await
        .map_err(test_error)?;
    let warning = options
        .iter()
        .find(|option| option.id() == "default")
        .and_then(|option| option.warning.as_ref())
        .ok_or_else(|| test_error("zero ratio did not receive the Go-compatible warning"))?;
    assert_eq!(warning.mode, "modal");
    assert_eq!(warning.confirmations, 3);
    assert!(warning.enabled);
    assert_eq!(warning.message, ZERO_RATIO_WARNING_MESSAGE);

    sqlx::query("UPDATE options SET value = '{\" DEFAULT \" : {\"enabled\":false,\"message\":\"disabled\",\"mode\":\"modal\",\"confirmations\":3}}' WHERE key = 'group_ratio_setting.group_warnings'")
        .execute(&harness.pool)
        .await?;
    let options = load_pg_options(&store, "default")
        .await
        .map_err(test_error)?;
    assert!(
        options
            .iter()
            .find(|option| option.id() == "default")
            .is_some_and(|option| option.warning.is_none())
    );

    harness.cleanup().await?;
    Ok(())
}
