//! A recovery producer, separate from owned financial tasks. Stopping the
//! producer cannot abort a settlement whose database work already started.

use super::{PgOpenAiRelayService, epoch_seconds};
use std::time::Duration;
use tokio::{sync::watch, task::JoinHandle, time::MissedTickBehavior};

#[derive(Clone, Copy, Debug)]
pub struct RelayReconcilePolicy {
    pub interval: Duration,
    pub batch_size: i64,
    /// Age is used only for an operator warning, never as refund evidence.
    pub warn_reserved_after: Duration,
}

impl Default for RelayReconcilePolicy {
    fn default() -> Self {
        Self {
            interval: Duration::from_secs(30),
            batch_size: 100,
            warn_reserved_after: Duration::from_secs(3600),
        }
    }
}

impl PgOpenAiRelayService {
    /// The first tick runs immediately at startup. Every subsequent pass
    /// resumes only persisted `settling` intent and never calls a provider.
    /// On shutdown, stop this producer before draining settlement_tracker.
    #[must_use]
    pub fn spawn_settlement_reconciler(
        &self,
        mut shutdown: watch::Receiver<bool>,
        policy: RelayReconcilePolicy,
    ) -> JoinHandle<()> {
        let service = self.clone();
        tokio::spawn(async move {
            let mut ticks = tokio::time::interval(policy.interval.max(Duration::from_millis(10)));
            ticks.set_missed_tick_behavior(MissedTickBehavior::Skip);
            loop {
                tokio::select! {
                    biased;
                    _=shutdown.wait_for(|stopped|*stopped)=>break,
                    _=ticks.tick()=>{}
                }
                let result = tokio::select! {
                    biased;
                    _=shutdown.wait_for(|stopped|*stopped)=>break,
                    result=service.reconcile_settlements(policy.batch_size)=>result,
                };
                match result {
                    Ok(count) if count > 0 => {
                        tracing::info!(count, "relay settlement recovery pass finished")
                    }
                    Err(error) => {
                        tracing::warn!(code=%error.code,"relay settlement recovery unavailable; durable intent retained")
                    }
                    _ => {}
                }
                let age = i64::try_from(policy.warn_reserved_after.as_secs()).unwrap_or(i64::MAX);
                let cutoff = epoch_seconds().saturating_sub(age);
                let result = tokio::select! {
                    biased;
                    _=shutdown.wait_for(|stopped|*stopped)=>break,
                    result=sqlx::query_scalar::<_,i64>("SELECT COUNT(*) FROM relay_settlement_records WHERE status='reserved' AND created_at<$1")
                        .bind(cutoff).fetch_one(&service.pg)=>result,
                };
                match result {
                    Ok(count) if count > 0 => tracing::warn!(
                        count,
                        "aged relay reservations have no final usage evidence; preserve funds for reconciliation"
                    ),
                    Err(_) => tracing::warn!("relay reserved-intent inspection unavailable"),
                    _ => {}
                }
            }
        })
    }
}
