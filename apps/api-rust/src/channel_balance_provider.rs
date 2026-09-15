//! Route-level DeepSeek balance wiring for the advanced channel surface.
//!
//! The mounted legacy route stays behind the existing dashboard authorization
//! boundary. This adapter intercepts only the single-channel balance operation,
//! delegates all other operations unchanged, and keeps provider/database errors
//! inside a bounded legacy-compatible JSON envelope.

use std::sync::Arc;

use async_trait::async_trait;
use axum::http::StatusCode;
use serde_json::{Value, json};
use sqlx::PgPool;

use crate::{
    channel_balance_store::{DeepSeekBalanceStoreError, PgDeepSeekBalanceService},
    routes::channel_advanced::{
        ChannelAdvancedCall, ChannelAdvancedError, ChannelAdvancedOperation,
        ChannelAdvancedProvider, ChannelAdvancedReply,
    },
};

#[async_trait]
trait DeepSeekBalanceRefresh: Send + Sync {
    async fn refresh_channel(&self, channel_id: i64) -> Result<f64, DeepSeekBalanceStoreError>;
}

#[async_trait]
impl DeepSeekBalanceRefresh for PgDeepSeekBalanceService {
    async fn refresh_channel(&self, channel_id: i64) -> Result<f64, DeepSeekBalanceStoreError> {
        PgDeepSeekBalanceService::refresh_channel(self, channel_id).await
    }
}

/// Decorates the advanced channel provider with the production DeepSeek
/// single-channel balance operation.
///
/// Go remains the production oracle. Only `UpdateBalance` is intercepted here;
/// the batch operation and every unrelated advanced operation continue through
/// the existing provider until their own parity work is complete.
#[derive(Clone)]
pub struct DeepSeekBalanceChannelAdvancedProvider {
    inner: Arc<dyn ChannelAdvancedProvider>,
    balance: Arc<dyn DeepSeekBalanceRefresh>,
}

impl DeepSeekBalanceChannelAdvancedProvider {
    /// Builds the production adapter from the authoritative PostgreSQL pool.
    pub fn new(
        inner: Arc<dyn ChannelAdvancedProvider>,
        pg: PgPool,
    ) -> Result<Self, ChannelAdvancedError> {
        let balance = PgDeepSeekBalanceService::new(pg).map_err(map_balance_error)?;
        Ok(Self {
            inner,
            balance: Arc::new(balance),
        })
    }

    #[cfg(test)]
    fn with_balance_service(
        inner: Arc<dyn ChannelAdvancedProvider>,
        balance: Arc<dyn DeepSeekBalanceRefresh>,
    ) -> Self {
        Self { inner, balance }
    }

    async fn refresh(&self, call: &ChannelAdvancedCall) -> Result<f64, DeepSeekBalanceStoreError> {
        let channel_id = call
            .channel_id
            .filter(|channel_id| *channel_id > 0)
            .ok_or(DeepSeekBalanceStoreError::InvalidChannelId)?;
        self.balance.refresh_channel(channel_id).await
    }
}

#[async_trait]
impl ChannelAdvancedProvider for DeepSeekBalanceChannelAdvancedProvider {
    async fn execute(&self, call: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError> {
        if call.operation != ChannelAdvancedOperation::UpdateBalance {
            return self.inner.execute(call).await;
        }
        self.refresh(&call)
            .await
            .map(Value::from)
            .map_err(map_balance_error)
    }

    async fn execute_reply(
        &self,
        call: ChannelAdvancedCall,
    ) -> Result<ChannelAdvancedReply, ChannelAdvancedError> {
        if call.operation != ChannelAdvancedOperation::UpdateBalance {
            return self.inner.execute_reply(call).await;
        }

        let reply = match self.refresh(&call).await {
            Ok(balance) => ChannelAdvancedReply::new(
                StatusCode::OK,
                json!({
                    "success": true,
                    "message": "",
                    "balance": balance,
                }),
            ),
            Err(error) => balance_error_reply(error),
        };
        Ok(reply)
    }
}

fn map_balance_error(error: DeepSeekBalanceStoreError) -> ChannelAdvancedError {
    match error {
        DeepSeekBalanceStoreError::InvalidChannelId => ChannelAdvancedError::Invalid,
        DeepSeekBalanceStoreError::ChannelNotFound => ChannelAdvancedError::NotFound,
        DeepSeekBalanceStoreError::MultiKeyUnsupported => {
            ChannelAdvancedError::MultiKeyBalanceUnsupported
        }
        DeepSeekBalanceStoreError::UnsupportedChannel
        | DeepSeekBalanceStoreError::InvalidExchangeRate
        | DeepSeekBalanceStoreError::Database
        | DeepSeekBalanceStoreError::Clock
        | DeepSeekBalanceStoreError::Fetch(_) => ChannelAdvancedError::Provider,
    }
}

fn balance_error_reply(error: DeepSeekBalanceStoreError) -> ChannelAdvancedReply {
    let message = match error {
        DeepSeekBalanceStoreError::InvalidChannelId => "参数错误".to_owned(),
        DeepSeekBalanceStoreError::ChannelNotFound => "channel not found".to_owned(),
        DeepSeekBalanceStoreError::UnsupportedChannel => "尚未实现".to_owned(),
        DeepSeekBalanceStoreError::MultiKeyUnsupported => "多密钥渠道不支持余额查询".to_owned(),
        other => other.to_string(),
    };
    ChannelAdvancedReply::new(
        StatusCode::OK,
        json!({
            "success": false,
            "message": message,
        }),
    )
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    };

    use async_trait::async_trait;
    use serde_json::{Value, json};

    use super::{
        DeepSeekBalanceChannelAdvancedProvider, DeepSeekBalanceRefresh,
        DeepSeekBalanceStoreError,
    };
    use crate::routes::channel_advanced::{
        ChannelAdvancedCall, ChannelAdvancedError, ChannelAdvancedOperation,
        ChannelAdvancedProvider, ChannelAdvancedReply,
    };

    #[derive(Default)]
    struct CountingInner {
        calls: AtomicUsize,
    }

    #[async_trait]
    impl ChannelAdvancedProvider for CountingInner {
        async fn execute(&self, _: ChannelAdvancedCall) -> Result<Value, ChannelAdvancedError> {
            self.calls.fetch_add(1, Ordering::SeqCst);
            Ok(json!({"delegated": true}))
        }
    }

    struct FixedBalance(Result<f64, DeepSeekBalanceStoreError>);

    #[async_trait]
    impl DeepSeekBalanceRefresh for FixedBalance {
        async fn refresh_channel(&self, _: i64) -> Result<f64, DeepSeekBalanceStoreError> {
            self.0
        }
    }

    fn call(operation: ChannelAdvancedOperation) -> ChannelAdvancedCall {
        ChannelAdvancedCall {
            operation,
            channel_id: Some(43),
            input: json!({}),
        }
    }

    #[tokio::test]
    async fn update_balance_uses_go_top_level_balance_envelope_without_delegating() {
        let inner = Arc::new(CountingInner::default());
        let provider = DeepSeekBalanceChannelAdvancedProvider::with_balance_service(
            inner.clone(),
            Arc::new(FixedBalance(Ok(12.5))),
        );

        let reply = provider
            .execute_reply(call(ChannelAdvancedOperation::UpdateBalance))
            .await
            .expect("balance reply");
        match reply {
            ChannelAdvancedReply::Json { status, body } => {
                assert_eq!(status, axum::http::StatusCode::OK);
                assert_eq!(
                    body,
                    json!({"success": true, "message": "", "balance": 12.5})
                );
            }
            ChannelAdvancedReply::Raw(_) => panic!("balance reply must be JSON"),
        }
        assert_eq!(inner.calls.load(Ordering::SeqCst), 0);
    }

    #[tokio::test]
    async fn multi_key_failure_preserves_go_legacy_envelope() {
        let provider = DeepSeekBalanceChannelAdvancedProvider::with_balance_service(
            Arc::new(CountingInner::default()),
            Arc::new(FixedBalance(Err(
                DeepSeekBalanceStoreError::MultiKeyUnsupported,
            ))),
        );

        let reply = provider
            .execute_reply(call(ChannelAdvancedOperation::UpdateBalance))
            .await
            .expect("balance failure reply");
        match reply {
            ChannelAdvancedReply::Json { status, body } => {
                assert_eq!(status, axum::http::StatusCode::OK);
                assert_eq!(
                    body,
                    json!({"success": false, "message": "多密钥渠道不支持余额查询"})
                );
            }
            ChannelAdvancedReply::Raw(_) => panic!("balance reply must be JSON"),
        }
    }

    #[tokio::test]
    async fn unrelated_operation_is_delegated_unchanged() {
        let inner = Arc::new(CountingInner::default());
        let provider = DeepSeekBalanceChannelAdvancedProvider::with_balance_service(
            inner.clone(),
            Arc::new(FixedBalance(Ok(99.0))),
        );

        assert_eq!(
            provider
                .execute(call(ChannelAdvancedOperation::TestOne))
                .await,
            Ok(json!({"delegated": true}))
        );
        assert_eq!(inner.calls.load(Ordering::SeqCst), 1);
    }
}
