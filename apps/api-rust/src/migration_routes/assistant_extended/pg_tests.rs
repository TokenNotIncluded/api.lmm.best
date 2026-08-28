use std::{error::Error, io, ops::Deref, sync::Arc, time::Duration};

use secrecy::SecretString;
use sqlx::{PgPool, postgres::PgPoolOptions};
use tokio::{sync::Barrier, time::sleep};

use super::{RevealError, reveal_card, unix_seconds};
use crate::migration_routes::assistant::{
    encrypt_assistant_secure_card_fixture, pg_test_support::IsolatedPgSchema,
};

type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

fn test_error(message: impl Into<String>) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message.into()))
}

struct PgHarness(IsolatedPgSchema);

impl Deref for PgHarness {
    type Target = IsolatedPgSchema;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

impl PgHarness {
    async fn new() -> TestResult<Option<Self>> {
        let database_url = match std::env::var("LMM_TEST_DATABASE_URL") {
            Ok(database_url) => database_url,
            Err(std::env::VarError::NotPresent) => return Ok(None),
            Err(error) => return Err(error.into()),
        };
        let admin = PgPool::connect(&database_url).await?;
        let schema = format!("assistant_card_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;
        let pool = match PgPoolOptions::new()
            .max_connections(4)
            .after_connect({
                let schema = schema.clone();
                move |connection, _metadata| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&database_url)
            .await
        {
            Ok(pool) => pool,
            Err(error) => {
                let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
                    .execute(&admin)
                    .await;
                admin.close().await;
                if let Err(cleanup_error) = cleanup {
                    return Err(test_error(format!(
                        "failed to connect to isolated PostgreSQL schema: {error}; additionally failed to drop it: {cleanup_error}"
                    )));
                }
                return Err(error.into());
            }
        };
        let harness = Self(IsolatedPgSchema {
            admin,
            pool,
            schema,
        });
        if let Err(error) = sqlx::query(
            "CREATE TABLE assistant_secure_cards (id TEXT PRIMARY KEY, owner_user_id BIGINT NOT NULL, conversation_id BIGINT NOT NULL, message_id BIGINT NOT NULL, type TEXT NOT NULL, summary TEXT NOT NULL, ciphertext TEXT NOT NULL, created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revealed_at BIGINT NOT NULL)",
        )
        .execute(&harness.pool)
        .await
        {
            if let Err(cleanup_error) = harness.cleanup().await {
                return Err(test_error(format!(
                    "failed to create secure-card table: {error}; additionally failed to clean the test schema: {cleanup_error}"
                )));
            }
            return Err(error.into());
        }
        Ok(Some(harness))
    }

    async fn insert_card(
        &self,
        secret: &SecretString,
        id: &str,
        owner: i64,
        expires_at: i64,
        valid_ciphertext: bool,
    ) -> TestResult {
        let ciphertext = if valid_ciphertext {
            encrypt_assistant_secure_card_fixture(secret, br#"{"api_key":"sk-secret"}"#)
                .map_err(|()| test_error("failed to encrypt secure-card fixture"))?
        } else {
            "invalid-ciphertext".to_owned()
        };
        sqlx::query(
            "INSERT INTO assistant_secure_cards (id, owner_user_id, conversation_id, message_id, type, summary, ciphertext, created_at, expires_at, revealed_at) VALUES ($1, $2, 0, 0, 'api_key', 'credential', $3, $4, $5, 0)",
        )
        .bind(id)
        .bind(owner)
        .bind(ciphertext)
        .bind(unix_seconds())
        .bind(expires_at)
        .execute(&self.pool)
        .await?;
        Ok(())
    }

    async fn revealed_at(&self, id: &str) -> TestResult<i64> {
        Ok(
            sqlx::query_scalar("SELECT revealed_at FROM assistant_secure_cards WHERE id = $1")
                .bind(id)
                .fetch_one(&self.pool)
                .await?,
        )
    }

    async fn cleanup(self) -> TestResult {
        let IsolatedPgSchema {
            admin,
            pool,
            schema,
        } = self.0;
        pool.close().await;
        let cleanup = sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await;
        admin.close().await;
        cleanup?;
        Ok(())
    }
}

#[tokio::test]
async fn pg_reveal_checks_owner_expiry_and_ciphertext_before_marking_revealed() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping secure-card PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let secret = SecretString::from("secure-card-pg-test-secret");
    let now = unix_seconds();
    harness
        .insert_card(&secret, "owner-card", 7, now + 600, true)
        .await?;
    harness
        .insert_card(&secret, "expired-card", 7, now - 1, true)
        .await?;
    harness
        .insert_card(&secret, "invalid-card", 7, now + 600, false)
        .await?;

    assert!(matches!(
        reveal_card(&harness.pool, &secret, 8, "owner-card").await,
        Err(RevealError::NotFound)
    ));
    assert_eq!(harness.revealed_at("owner-card").await?, 0);
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "expired-card").await,
        Err(RevealError::Expired)
    ));
    assert_eq!(harness.revealed_at("expired-card").await?, 0);
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "invalid-card").await,
        Err(RevealError::Invalid)
    ));
    assert_eq!(harness.revealed_at("invalid-card").await?, 0);

    harness.cleanup().await?;
    Ok(())
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn pg_reveal_is_exactly_once_under_a_deterministic_start_barrier() -> TestResult {
    let Some(harness) = PgHarness::new().await? else {
        eprintln!("skipping secure-card PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return Ok(());
    };
    let secret = SecretString::from("secure-card-pg-test-secret");
    harness
        .insert_card(&secret, "single-use-card", 7, unix_seconds() + 600, true)
        .await?;
    let barrier = Arc::new(Barrier::new(3));
    let mut tasks = Vec::new();
    for _ in 0..2 {
        let pool = harness.pool.clone();
        let secret = secret.clone();
        let barrier = Arc::clone(&barrier);
        tasks.push(tokio::spawn(async move {
            barrier.wait().await;
            reveal_card(&pool, &secret, 7, "single-use-card").await
        }));
    }
    barrier.wait().await;
    let first = tasks.remove(0).await?;
    let second = tasks.remove(0).await?;
    assert_eq!(usize::from(first.is_ok()) + usize::from(second.is_ok()), 1);
    assert!(matches!(
        first.as_ref().err().or_else(|| second.as_ref().err()),
        Some(RevealError::Consumed)
    ));
    assert!(harness.revealed_at("single-use-card").await? > 0);

    // A post-commit replay must remain consumed rather than racing the decrypt.
    sleep(Duration::from_millis(1)).await;
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "single-use-card").await,
        Err(RevealError::Consumed)
    ));

    harness.cleanup().await?;
    Ok(())
}
