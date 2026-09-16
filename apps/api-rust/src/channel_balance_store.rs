//! Durable PostgreSQL boundary for DeepSeek channel balance refresh.
//!
//! This module deliberately separates provider egress from route handling.  The
//! single-channel operation reads only persisted channel/configuration state,
//! fetches from the fixed DeepSeek provider endpoint, then publishes balance
//! and `balance_updated_time` together in one SQL statement.

use std::time::{SystemTime, UNIX_EPOCH};

use secrecy::SecretString;
use sqlx::{PgPool, Row};

use crate::channel_balance::{DeepSeekBalanceClient, DeepSeekBalanceFetchError};

const CHANNEL_TYPE_DEEPSEEK: i64 = 43;
const USD_EXCHANGE_RATE_OPTION_KEY: &str = "USDExchangeRate";
const DEFAULT_USD_EXCHANGE_RATE: f64 = 7.3;

/// Safe failure classes for the persisted DeepSeek balance operation.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum DeepSeekBalanceStoreError {
    InvalidChannelId,
    ChannelNotFound,
    UnsupportedChannel,
    MultiKeyUnsupported,
    InvalidExchangeRate,
    Database,
    Clock,
    Fetch(DeepSeekBalanceFetchError),
}

impl std::fmt::Display for DeepSeekBalanceStoreError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::InvalidChannelId => formatter.write_str("channel id is invalid"),
            Self::ChannelNotFound => formatter.write_str("channel not found"),
            Self::UnsupportedChannel => {
                formatter.write_str("balance refresh is not supported for this channel")
            }
            Self::MultiKeyUnsupported => {
                formatter.write_str("multi-key channel does not support balance refresh")
            }
            Self::InvalidExchangeRate => {
                formatter.write_str("USD exchange rate must be finite and positive")
            }
            Self::Database => formatter.write_str("channel balance database operation failed"),
            Self::Clock => formatter.write_str("channel balance timestamp is unavailable"),
            Self::Fetch(error) => error.fmt(formatter),
        }
    }
}

impl std::error::Error for DeepSeekBalanceStoreError {}

impl From<DeepSeekBalanceFetchError> for DeepSeekBalanceStoreError {
    fn from(error: DeepSeekBalanceFetchError) -> Self {
        Self::Fetch(error)
    }
}

/// Production DeepSeek balance refresher backed by authoritative PostgreSQL.
///
/// The provider endpoint is owned by [`DeepSeekBalanceClient`]; neither route
/// input nor a persisted `base_url` participates in the balance target.
#[derive(Clone)]
pub(crate) struct PgDeepSeekBalanceService {
    pg: PgPool,
    client: DeepSeekBalanceClient,
}

impl PgDeepSeekBalanceService {
    pub(crate) fn new(pg: PgPool) -> Result<Self, DeepSeekBalanceStoreError> {
        Ok(Self {
            pg,
            client: DeepSeekBalanceClient::new()?,
        })
    }

    /// Refreshes one persisted DeepSeek channel and publishes the Go-compatible
    /// USD-denominated balance plus update timestamp atomically.
    pub(crate) async fn refresh_channel(
        &self,
        channel_id: i64,
    ) -> Result<f64, DeepSeekBalanceStoreError> {
        if channel_id <= 0 {
            return Err(DeepSeekBalanceStoreError::InvalidChannelId);
        }

        let row = sqlx::query(
            "SELECT type, COALESCE(key, '') AS key, \
             COALESCE((channel_info ->> 'is_multi_key')::boolean, false) AS multi_key \
             FROM channels WHERE id = $1",
        )
        .bind(channel_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| DeepSeekBalanceStoreError::Database)?
        .ok_or(DeepSeekBalanceStoreError::ChannelNotFound)?;

        let channel_type: i64 = row
            .try_get("type")
            .map_err(|_| DeepSeekBalanceStoreError::Database)?;
        if channel_type != CHANNEL_TYPE_DEEPSEEK {
            return Err(DeepSeekBalanceStoreError::UnsupportedChannel);
        }
        let multi_key: bool = row
            .try_get("multi_key")
            .map_err(|_| DeepSeekBalanceStoreError::Database)?;
        if multi_key {
            return Err(DeepSeekBalanceStoreError::MultiKeyUnsupported);
        }
        let credential = row
            .try_get::<String, _>("key")
            .map(SecretString::from)
            .map_err(|_| DeepSeekBalanceStoreError::Database)?;

        let exchange_rate = self.usd_exchange_rate().await?;
        let balance = self.client.fetch_usd(&credential, exchange_rate).await?;
        self.persist_balance(channel_id, balance).await?;
        Ok(balance)
    }

    async fn usd_exchange_rate(&self) -> Result<f64, DeepSeekBalanceStoreError> {
        let configured =
            sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = $1")
                .bind(USD_EXCHANGE_RATE_OPTION_KEY)
                .fetch_optional(&self.pg)
                .await
                .map_err(|_| DeepSeekBalanceStoreError::Database)?;
        parse_usd_exchange_rate(configured.as_deref())
    }

    async fn persist_balance(
        &self,
        channel_id: i64,
        balance: f64,
    ) -> Result<(), DeepSeekBalanceStoreError> {
        if !balance.is_finite() {
            return Err(DeepSeekBalanceStoreError::Fetch(
                DeepSeekBalanceFetchError::Parse(
                    crate::channel_balance::DeepSeekBalanceError::NonFiniteBalance,
                ),
            ));
        }
        if balance < 0.0 {
            return Err(DeepSeekBalanceStoreError::Fetch(
                DeepSeekBalanceFetchError::Parse(
                    crate::channel_balance::DeepSeekBalanceError::NegativeBalance,
                ),
            ));
        }
        let updated_at = unix_timestamp()?;
        let result = sqlx::query(
            "UPDATE channels \
             SET balance = $1, balance_updated_time = $2 \
             WHERE id = $3 AND type = $4",
        )
        .bind(balance)
        .bind(updated_at)
        .bind(channel_id)
        .bind(CHANNEL_TYPE_DEEPSEEK)
        .execute(&self.pg)
        .await
        .map_err(|_| DeepSeekBalanceStoreError::Database)?;
        if result.rows_affected() != 1 {
            return Err(DeepSeekBalanceStoreError::ChannelNotFound);
        }
        Ok(())
    }
}

fn parse_usd_exchange_rate(raw: Option<&str>) -> Result<f64, DeepSeekBalanceStoreError> {
    let rate = match raw {
        None => DEFAULT_USD_EXCHANGE_RATE,
        Some(value) => value
            .trim()
            .parse::<f64>()
            .map_err(|_| DeepSeekBalanceStoreError::InvalidExchangeRate)?,
    };
    if !rate.is_finite() || rate <= 0.0 {
        return Err(DeepSeekBalanceStoreError::InvalidExchangeRate);
    }
    Ok(rate)
}

fn unix_timestamp() -> Result<i64, DeepSeekBalanceStoreError> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_err(|_| DeepSeekBalanceStoreError::Clock)
        .and_then(|duration| {
            i64::try_from(duration.as_secs()).map_err(|_| DeepSeekBalanceStoreError::Clock)
        })
}

#[cfg(test)]
mod tests {
    use std::{env, process, time::SystemTime};

    use sqlx::{PgPool, Row, postgres::PgPoolOptions};

    use super::{
        CHANNEL_TYPE_DEEPSEEK, DEFAULT_USD_EXCHANGE_RATE, DeepSeekBalanceStoreError,
        PgDeepSeekBalanceService, parse_usd_exchange_rate,
    };

    #[test]
    fn missing_exchange_rate_uses_go_default() {
        assert_eq!(parse_usd_exchange_rate(None), Ok(DEFAULT_USD_EXCHANGE_RATE));
    }

    #[test]
    fn configured_exchange_rate_is_trimmed_and_validated() {
        assert_eq!(parse_usd_exchange_rate(Some(" 7.25 ")), Ok(7.25));
        for raw in ["", "0", "-1", "NaN", "inf", "not-a-number"] {
            assert_eq!(
                parse_usd_exchange_rate(Some(raw)),
                Err(DeepSeekBalanceStoreError::InvalidExchangeRate),
                "{raw}"
            );
        }
    }

    async fn isolated_balance_pool() -> Result<(PgPool, PgPool, String), sqlx::Error> {
        let database_url = env::var("TEST_DATABASE_URL")
            .or_else(|_| env::var("DATABASE_URL"))
            .expect("TEST_DATABASE_URL or DATABASE_URL must point to disposable PostgreSQL");
        let admin = PgPoolOptions::new()
            .max_connections(1)
            .connect(&database_url)
            .await?;
        let nonce = SystemTime::now()
            .duration_since(SystemTime::UNIX_EPOCH)
            .expect("system clock")
            .as_nanos();
        let schema = format!("deepseek_balance_{}_{}", process::id(), nonce);
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await?;

        let search_path = schema.clone();
        let scoped = PgPoolOptions::new()
            .max_connections(2)
            .after_connect(move |connection, _| {
                let search_path = search_path.clone();
                Box::pin(async move {
                    sqlx::query(&format!("SET search_path TO {search_path}, public"))
                        .execute(connection)
                        .await?;
                    Ok(())
                })
            })
            .connect(&database_url)
            .await?;
        sqlx::query(
            "CREATE TABLE channels (\
                id BIGINT PRIMARY KEY,\
                type BIGINT NOT NULL,\
                key TEXT,\
                channel_info JSONB,\
                balance DOUBLE PRECISION NOT NULL DEFAULT 0,\
                balance_updated_time BIGINT NOT NULL DEFAULT 0\
            )",
        )
        .execute(&scoped)
        .await?;
        sqlx::query("CREATE TABLE options (key TEXT PRIMARY KEY, value TEXT)")
            .execute(&scoped)
            .await?;
        Ok((admin, scoped, schema))
    }

    #[tokio::test]
    #[ignore = "requires disposable PostgreSQL; exercised by the real integration gate"]
    async fn persisted_balance_updates_value_and_timestamp_together() {
        let (admin, pg, schema) = isolated_balance_pool()
            .await
            .expect("create isolated balance schema");
        sqlx::query(
            "INSERT INTO channels (id, type, key, channel_info, balance, balance_updated_time) \
             VALUES ($1, $2, $3, '{}'::jsonb, 1.0, 0)",
        )
        .bind(7_i64)
        .bind(CHANNEL_TYPE_DEEPSEEK)
        .bind("persisted-secret")
        .execute(&pg)
        .await
        .expect("insert DeepSeek channel");

        let service = PgDeepSeekBalanceService::new(pg.clone()).expect("balance service");
        service
            .persist_balance(7, 12.5)
            .await
            .expect("persist balance");

        let row = sqlx::query("SELECT balance, balance_updated_time FROM channels WHERE id = $1")
            .bind(7_i64)
            .fetch_one(&pg)
            .await
            .expect("read persisted balance");
        let balance: f64 = row.try_get("balance").expect("balance column");
        let updated_at: i64 = row
            .try_get("balance_updated_time")
            .expect("balance_updated_time column");
        assert_eq!(balance, 12.5);
        assert!(updated_at > 0);

        assert_eq!(
            service.persist_balance(7, f64::NAN).await,
            Err(DeepSeekBalanceStoreError::Fetch(
                crate::channel_balance::DeepSeekBalanceFetchError::Parse(
                    crate::channel_balance::DeepSeekBalanceError::NonFiniteBalance,
                ),
            ))
        );
        let unchanged =
            sqlx::query("SELECT balance, balance_updated_time FROM channels WHERE id = $1")
                .bind(7_i64)
                .fetch_one(&pg)
                .await
                .expect("read unchanged balance");
        assert_eq!(
            unchanged.try_get::<f64, _>("balance").expect("balance"),
            12.5
        );
        assert_eq!(
            unchanged
                .try_get::<i64, _>("balance_updated_time")
                .expect("updated time"),
            updated_at
        );

        pg.close().await;
        sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
            .execute(&admin)
            .await
            .expect("drop isolated balance schema");
        admin.close().await;
    }
}
