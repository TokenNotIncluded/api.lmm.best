//! Request cancellation may hand billing work to an owned background task.
//! Graceful shutdown must drain that work after HTTP response bodies finish.

use std::sync::{
    Arc,
    atomic::{AtomicUsize, Ordering},
};
use tokio::{sync::Notify, time::Instant};

#[derive(Clone, Default)]
pub struct RelaySettlementTracker(Arc<Inner>);

#[derive(Default)]
struct Inner {
    pending: AtomicUsize,
    finished: Notify,
}

impl RelaySettlementTracker {
    /// Number of owned relay/settlement tasks which have not finished yet.
    #[must_use]
    pub fn pending(&self) -> usize {
        self.0.pending.load(Ordering::Acquire)
    }

    /// Uses the caller's existing shutdown deadline; never starts a new one.
    /// True means all tasks finished, not that a failed durable settlement
    /// was successful. Such failures remain in the reconciliation ledger.
    pub async fn drain_until(&self, deadline: Instant) -> bool {
        loop {
            let notified = self.0.finished.notified();
            tokio::pin!(notified);
            notified.as_mut().enable();
            if self.pending() == 0 {
                return true;
            }
            if tokio::time::timeout_at(deadline, notified).await.is_err() {
                return self.pending() == 0;
            }
        }
    }

    pub(super) fn enter(&self) -> Permit {
        self.0.pending.fetch_add(1, Ordering::AcqRel);
        Permit(self.clone())
    }
}

pub(super) struct Permit(RelaySettlementTracker);

impl Drop for Permit {
    fn drop(&mut self) {
        if self.0.0.pending.fetch_sub(1, Ordering::AcqRel) == 1 {
            self.0.0.finished.notify_waiters();
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::time::Duration;

    #[tokio::test]
    async fn drain_waits_for_detached_tasks_and_obeys_shared_deadline() {
        let tracker = RelaySettlementTracker::default();
        let permit = tracker.enter();
        assert_eq!(tracker.pending(), 1);
        assert!(!tracker.drain_until(Instant::now()).await);
        let (sender, receiver) = tokio::sync::oneshot::channel::<()>();
        tokio::spawn(async move {
            let _permit = permit;
            let _ = receiver.await;
        });
        let waiter = tokio::spawn({
            let tracker = tracker.clone();
            async move {
                tracker
                    .drain_until(Instant::now() + Duration::from_secs(2))
                    .await
            }
        });
        tokio::task::yield_now().await;
        assert!(!waiter.is_finished());
        drop(sender);
        assert!(waiter.await.unwrap());
        assert_eq!(tracker.pending(), 0);
    }

    #[tokio::test]
    async fn task_unwind_releases_its_permit() {
        let tracker = RelaySettlementTracker::default();
        let permit = tracker.enter();
        let task = tokio::spawn(async move {
            let _permit = permit;
            panic!("injected task panic");
        });
        assert!(task.await.is_err());
        assert!(tracker.drain_until(Instant::now()).await);
    }
}
