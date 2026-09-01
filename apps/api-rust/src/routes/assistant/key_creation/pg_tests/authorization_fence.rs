use bcrypt::{DEFAULT_COST, hash};

use super::support::*;
use crate::routes::assistant::key_creation::{KeyCreationError, confirm_pg};

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
async fn pg_commit_time_authorization_fence_rejects_completed_security_mutations() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping assistant-key PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let store = harness.store()?;
    for case in [
        "revoke-l1",
        "revoke-session",
        "bump-session-version",
        "bump-user-auth-version",
        "enable-two-factor",
        "rotate-backup-code",
    ] {
        sqlx::query(
            "UPDATE users SET status = 1, auth_version = 1, trust_level_override = 1 WHERE id = 7",
        )
        .execute(&store.pg)
        .await?;
        sqlx::query("DELETE FROM user_sessions")
            .execute(&store.pg)
            .await?;
        sqlx::query("DELETE FROM two_fa_backup_codes")
            .execute(&store.pg)
            .await?;
        sqlx::query("DELETE FROM two_fas")
            .execute(&store.pg)
            .await?;

        let action = prepare(&store, &format!("fence-{case}"), "default").await?;
        let old_backup = "ABCD-EFGH-IJKL";
        if case == "rotate-backup-code" {
            sqlx::query(
                "INSERT INTO two_fas (user_id, secret, is_enabled, failed_attempts, updated_at) \
                 VALUES (7, 'JBSWY3DPEHPK3PXP', TRUE, 0, NOW())",
            )
            .execute(&store.pg)
            .await?;
            sqlx::query(
                "INSERT INTO two_fa_backup_codes (user_id, code_hash, is_used) VALUES (7, $1, FALSE)",
            )
            .bind(hash(old_backup, DEFAULT_COST)?)
            .execute(&store.pg)
            .await?;
        }

        let mut mutation = store.pg.begin().await?;
        let blocker_pid = sqlx::query_scalar::<_, i32>("SELECT pg_backend_pid()")
            .fetch_one(&mut *mutation)
            .await?;

        sqlx::query("SELECT id FROM users WHERE id = 7 FOR UPDATE")
            .execute(&mut *mutation)
            .await?;
        match case {
            "revoke-l1" => {
                sqlx::query("UPDATE users SET trust_level_override = 0 WHERE id = 7")
                    .execute(&mut *mutation)
                    .await?;
            }
            "revoke-session" => {
                sqlx::query(
                    "UPDATE user_sessions SET status = 'revoked', revoked_at = EXTRACT(EPOCH FROM NOW())::BIGINT WHERE sid = $1 AND user_id = 7",
                )
                .bind(format!("fence-{case}"))
                .execute(&mut *mutation)
                .await?;
            }
            "bump-session-version" => {
                sqlx::query("UPDATE user_sessions SET version = 2 WHERE sid = $1 AND user_id = 7")
                    .bind(format!("fence-{case}"))
                    .execute(&mut *mutation)
                    .await?;
            }
            "bump-user-auth-version" => {
                sqlx::query("UPDATE users SET auth_version = 2 WHERE id = 7")
                    .execute(&mut *mutation)
                    .await?;
            }
            "enable-two-factor" => {
                sqlx::query("UPDATE users SET auth_version = 2 WHERE id = 7")
                    .execute(&mut *mutation)
                    .await?;
                sqlx::query(
                    "INSERT INTO two_fas (user_id, secret, is_enabled, failed_attempts, updated_at) \
                     VALUES (7, 'JBSWY3DPEHPK3PXP', TRUE, 0, NOW())",
                )
                .execute(&mut *mutation)
                .await?;
            }
            "rotate-backup-code" => {
                sqlx::query("SELECT id FROM two_fas WHERE user_id = 7 FOR UPDATE")
                    .execute(&mut *mutation)
                    .await?;
                sqlx::query(
                    "SELECT id FROM two_fa_backup_codes WHERE user_id = 7 AND is_used = FALSE FOR UPDATE",
                )
                .execute(&mut *mutation)
                .await?;
                sqlx::query("DELETE FROM two_fa_backup_codes WHERE user_id = 7")
                    .execute(&mut *mutation)
                    .await?;
                sqlx::query(
                    "INSERT INTO two_fa_backup_codes (user_id, code_hash, is_used) VALUES (7, $1, FALSE)",
                )
                .bind(hash("MNOP-QRST-UVWX", DEFAULT_COST)?)
                .execute(&mut *mutation)
                .await?;
            }
            _ => {
                return Err(test_error(
                    "authorization mutation case is outside the fixture",
                ));
            }
        }

        let confirm_store = store.clone();
        let session_id = format!("fence-{case}");
        let two_factor_code = if case == "rotate-backup-code" {
            old_backup.to_owned()
        } else {
            String::new()
        };
        let token = confirmation(&action)?;
        let fence = authorization_fence(&session_id)?;
        let confirmation_task = tokio::spawn(async move {
            confirm_pg(&confirm_store, fence, token, &two_factor_code).await
        });
        wait_until_blocked_by(&harness.admin, blocker_pid).await?;
        mutation.commit().await?;

        let confirmation_result = confirmation_task.await?;
        let error = match confirmation_result {
            Err(error) => error,
            Ok(_) => return Err(test_error("stale authorization fence was accepted")),
        };
        if case == "rotate-backup-code" {
            assert_eq!(error, KeyCreationError::TwoFactorInvalid);
        } else {
            assert_eq!(error, KeyCreationError::InvalidConfirmation);
        }
        assert_eq!(count(&store.pg, "tokens").await?, 0, "case {case}");
        assert_eq!(
            count(&store.pg, "assistant_secure_cards").await?,
            0,
            "case {case}"
        );
        let consumed: bool = sqlx::query_scalar(
            "SELECT consumed_at IS NOT NULL FROM auth_flows WHERE session_id = $1 ORDER BY id DESC LIMIT 1",
        )
        .bind(format!("fence-{case}"))
        .fetch_one(&store.pg)
        .await?;
        assert!(!consumed, "case {case} consumed the flow");
    }

    harness.cleanup().await?;
    Ok(())
}
