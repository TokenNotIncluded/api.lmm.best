//! Cache invalidation follows a committed quota mutation. PostgreSQL remains
//! authoritative; a cache outage cannot turn a committed debit into a retry.

use std::{sync::Arc, time::Duration};

use sqlx::PgPool;

#[derive(Clone)]
pub(super) struct RelayCache {
    client: redis::Client,
    crypto_secret: Arc<str>,
    timeout: Duration,
}

impl RelayCache {
    pub(super) fn new(client: redis::Client, crypto_secret: Arc<str>, timeout: Duration) -> Self {
        Self {
            client,
            crypto_secret,
            timeout,
        }
    }

    pub(super) async fn invalidate(
        &self,
        pg: &PgPool,
        user_id: i64,
        token_id: i64,
        credential: Option<&str>,
    ) {
        let work = async {
            // Live requests retain their authenticated key only in memory.
            // Recovery resolves the current key after the financial commit;
            // credentials are never included in the settlement ledger.
            let recovered;
            let credential = if let Some(credential) = credential {
                Some(credential)
            } else {
                recovered = sqlx::query_scalar::<_, Option<String>>(
                    "SELECT key FROM tokens WHERE id=$1 AND user_id=$2",
                )
                .bind(token_id)
                .bind(user_id)
                .fetch_optional(pg)
                .await
                .map_err(|_| ())?
                .flatten();
                recovered.as_deref()
            };
            let mut connection = self
                .client
                .get_multiplexed_async_connection()
                .await
                .map_err(|_| ())?;
            let user_key = format!("user:{user_id}");
            if let Some(key) = credential.filter(|key| !key.is_empty()) {
                let token_key =
                    crate::routes::api_token::legacy_token_cache_key(&self.crypto_secret, key)
                        .ok_or(())?;
                let digest = token_key.strip_prefix("token:").ok_or(())?;
                // The Go fence lives for ten seconds. An atomic script closes
                // the interval in which a stale reader could rehydrate quota
                // between the fence and the two cache deletions.
                redis::Script::new(
                    "redis.call('SET',KEYS[3],1,'EX',10); redis.call('DEL',KEYS[1],KEYS[2]); return 1",
                )
                .key(user_key)
                .key(&token_key)
                .key(format!("token:fence:{digest}"))
                .invoke_async::<i64>(&mut connection)
                .await
                .map_err(|_| ())?;
            } else {
                redis::cmd("DEL")
                    .arg(user_key)
                    .query_async::<i64>(&mut connection)
                    .await
                    .map_err(|_| ())?;
            }
            Ok::<_, ()>(())
        };
        if !matches!(tokio::time::timeout(self.timeout, work).await, Ok(Ok(()))) {
            tracing::warn!(
                user_id,
                token_id,
                "relay quota cache invalidation unavailable after database commit"
            );
        }
    }
}
