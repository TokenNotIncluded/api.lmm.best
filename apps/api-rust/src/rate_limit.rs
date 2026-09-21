use async_trait::async_trait;
use lmm_application::{
    GlobalApiRateLimiter, RateLimitError, RateLimitOutcome, ValkeyReadinessPolicy,
};
use std::{future::Future, time::Duration};

const FIXED_WINDOW_SCRIPT: &str = r#"
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then
  return {0, count, ttl}
end
return {1, count, ttl}
"#;

pub struct ValkeyGlobalApiRateLimiter {
    client: redis::Client,
    valkey_policy: ValkeyReadinessPolicy,
    maximum: u64,
    window: Duration,
    dependency_timeout: Duration,
}

impl ValkeyGlobalApiRateLimiter {
    pub fn new(
        client: redis::Client,
        valkey_policy: ValkeyReadinessPolicy,
        maximum: u64,
        window: Duration,
        dependency_timeout: Duration,
    ) -> Self {
        Self {
            client,
            valkey_policy,
            maximum,
            window,
            dependency_timeout,
        }
    }
}

#[async_trait]
impl GlobalApiRateLimiter for ValkeyGlobalApiRateLimiter {
    async fn check(&self, client_ip: &str) -> Result<RateLimitOutcome, RateLimitError> {
        if self.valkey_policy == ValkeyReadinessPolicy::OptionalCacheOnly {
            return Ok(RateLimitOutcome::Allowed);
        }
        let key = format!("rateLimit:v2:ip:GA:{client_ip}");
        let reply = bounded_dependency(self.dependency_timeout, async {
            let mut connection = self.client.get_multiplexed_async_connection().await?;
            redis::Script::new(FIXED_WINDOW_SCRIPT)
                .key(key)
                .arg(self.maximum)
                .arg(self.window.as_secs())
                .invoke_async::<Vec<i64>>(&mut connection)
                .await
        })
        .await?;
        if reply.len() != 3 {
            return Err(RateLimitError);
        }
        if reply[0] == 1 {
            Ok(RateLimitOutcome::Allowed)
        } else {
            Ok(RateLimitOutcome::Rejected {
                retry_after_seconds: u64::try_from(reply[2]).ok().filter(|ttl| *ttl > 0),
            })
        }
    }
}

async fn bounded_dependency<T, E>(
    timeout: Duration,
    operation: impl Future<Output = Result<T, E>>,
) -> Result<T, RateLimitError> {
    tokio::time::timeout(timeout, operation)
        .await
        .map_err(|_| RateLimitError)?
        .map_err(|_| RateLimitError)
}

#[cfg(test)]
mod tests {
    use super::{ValkeyGlobalApiRateLimiter, bounded_dependency};
    use lmm_application::{GlobalApiRateLimiter, RateLimitOutcome, ValkeyReadinessPolicy};
    use std::{future, time::Duration};

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[tokio::test]
    async fn dependency_timeout_should_fail_closed_for_pending_work() {
        let result = bounded_dependency(
            Duration::from_millis(1),
            future::pending::<Result<(), ()>>(),
        )
        .await;
        assert!(result.is_err());
    }

    #[tokio::test]
    async fn disabled_limiter_should_bypass_an_unreachable_valkey() -> TestResult {
        let client = redis::Client::open("redis://127.0.0.1:1")?;
        let limiter = ValkeyGlobalApiRateLimiter::new(
            client,
            ValkeyReadinessPolicy::OptionalCacheOnly,
            1,
            Duration::from_secs(1),
            Duration::from_millis(1),
        );
        let result =
            tokio::time::timeout(Duration::from_millis(20), limiter.check("192.0.2.1")).await??;
        assert_eq!(result, RateLimitOutcome::Allowed);
        Ok(())
    }
}
