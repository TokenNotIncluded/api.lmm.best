use std::{ops::Deref, sync::Arc, time::Duration};

use secrecy::SecretString;
use tokio::{sync::Barrier, time::sleep};

use super::{RevealError, reveal_card, unix_seconds};
use crate::migration_routes::assistant::{
    encrypt_assistant_secure_card_fixture, pg_test_support::IsolatedPgSchema,
};

struct PgHarness(IsolatedPgSchema);

impl Deref for PgHarness {
    type Target = IsolatedPgSchema;

    fn deref(&self) -> &Self::Target {
        &self.0
    }
}

impl PgHarness {
    async fn new() -> Option<Self> {
        let harness = Self(IsolatedPgSchema::new("assistant_card", 4).await?);
        sqlx::query(
            "CREATE TABLE assistant_secure_cards (id TEXT PRIMARY KEY, owner_user_id BIGINT NOT NULL, conversation_id BIGINT NOT NULL, message_id BIGINT NOT NULL, type TEXT NOT NULL, summary TEXT NOT NULL, ciphertext TEXT NOT NULL, created_at BIGINT NOT NULL, expires_at BIGINT NOT NULL, revealed_at BIGINT NOT NULL)",
        )
        .execute(&harness.pool)
        .await
        .expect("create secure-card table");
        Some(harness)
    }

    async fn insert_card(
        &self,
        secret: &SecretString,
        id: &str,
        owner: i64,
        expires_at: i64,
        valid_ciphertext: bool,
    ) {
        let ciphertext = if valid_ciphertext {
            encrypt_assistant_secure_card_fixture(secret, br#"{"api_key":"sk-secret"}"#)
                .expect("encrypt secure-card fixture")
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
        .await
        .expect("insert secure-card fixture");
    }

    async fn revealed_at(&self, id: &str) -> i64 {
        sqlx::query_scalar("SELECT revealed_at FROM assistant_secure_cards WHERE id = $1")
            .bind(id)
            .fetch_one(&self.pool)
            .await
            .expect("read secure-card revealed state")
    }

    async fn cleanup(self) {
        self.0.cleanup().await;
    }
}

#[tokio::test]
async fn pg_reveal_checks_owner_expiry_and_ciphertext_before_marking_revealed() {
    let Some(harness) = PgHarness::new().await else {
        eprintln!("skipping secure-card PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return;
    };
    let secret = SecretString::from("secure-card-pg-test-secret");
    let now = unix_seconds();
    harness
        .insert_card(&secret, "owner-card", 7, now + 600, true)
        .await;
    harness
        .insert_card(&secret, "expired-card", 7, now - 1, true)
        .await;
    harness
        .insert_card(&secret, "invalid-card", 7, now + 600, false)
        .await;

    assert!(matches!(
        reveal_card(&harness.pool, &secret, 8, "owner-card").await,
        Err(RevealError::NotFound)
    ));
    assert_eq!(harness.revealed_at("owner-card").await, 0);
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "expired-card").await,
        Err(RevealError::Expired)
    ));
    assert_eq!(harness.revealed_at("expired-card").await, 0);
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "invalid-card").await,
        Err(RevealError::Invalid)
    ));
    assert_eq!(harness.revealed_at("invalid-card").await, 0);

    harness.cleanup().await;
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn pg_reveal_is_exactly_once_under_a_deterministic_start_barrier() {
    let Some(harness) = PgHarness::new().await else {
        eprintln!("skipping secure-card PostgreSQL test: LMM_TEST_DATABASE_URL is unset");
        return;
    };
    let secret = SecretString::from("secure-card-pg-test-secret");
    harness
        .insert_card(&secret, "single-use-card", 7, unix_seconds() + 600, true)
        .await;
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
    let first = tasks.remove(0).await.expect("first reveal task");
    let second = tasks.remove(0).await.expect("second reveal task");
    assert_eq!(usize::from(first.is_ok()) + usize::from(second.is_ok()), 1);
    assert!(matches!(
        first.as_ref().err().or_else(|| second.as_ref().err()),
        Some(RevealError::Consumed)
    ));
    assert!(harness.revealed_at("single-use-card").await > 0);

    // A post-commit replay must remain consumed rather than racing the decrypt.
    sleep(Duration::from_millis(1)).await;
    assert!(matches!(
        reveal_card(&harness.pool, &secret, 7, "single-use-card").await,
        Err(RevealError::Consumed)
    ));

    harness.cleanup().await;
}
