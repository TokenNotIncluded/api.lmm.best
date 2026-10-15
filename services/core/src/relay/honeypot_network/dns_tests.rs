//! Controlled, process-owned lookup dependency. Never manipulates system DNS,
//! networking, other processes or production; password work is real Argon2id.
use super::*;
use argon2::{
    Algorithm, Argon2, Params, Version,
    password_hash::{PasswordHash, PasswordHasher, PasswordVerifier, SaltString},
};
use std::sync::{
    Condvar, Mutex,
    atomic::{AtomicUsize, Ordering},
};
use tokio::{
    sync::mpsc,
    time::{Duration, Instant, sleep, timeout_at},
};

struct BlockedResolvers {
    released: Arc<(Mutex<bool>, Condvar)>,
    active: Arc<AtomicUsize>,
    peak: Arc<AtomicUsize>,
    calls: Arc<AtomicUsize>,
    entered: mpsc::Sender<()>,
}
struct Active(Arc<AtomicUsize>);
impl Drop for Active {
    fn drop(&mut self) {
        self.0.fetch_sub(1, Ordering::SeqCst);
    }
}
impl BlockedResolvers {
    fn new() -> (Self, mpsc::Receiver<()>) {
        let (entered, receiver) = mpsc::channel(2);
        (
            Self {
                released: Arc::new((Mutex::new(false), Condvar::new())),
                active: Arc::new(AtomicUsize::new(0)),
                peak: Arc::new(AtomicUsize::new(0)),
                calls: Arc::new(AtomicUsize::new(0)),
                entered,
            },
            receiver,
        )
    }
    fn launch(
        &self,
        slots: Arc<Semaphore>,
    ) -> tokio::task::JoinHandle<Result<Vec<SocketAddr>, RelayError>> {
        let released = self.released.clone();
        let active = self.active.clone();
        let peak = self.peak.clone();
        let calls = self.calls.clone();
        let entered = self.entered.clone();
        tokio::spawn(run_lookup(slots, move || {
            assert_eq!(std::thread::current().name(), Some("lmm-honeypot-dns"));
            calls.fetch_add(1, Ordering::SeqCst);
            let count = active.fetch_add(1, Ordering::SeqCst) + 1;
            peak.fetch_max(count, Ordering::SeqCst);
            let _active = Active(active);
            entered.blocking_send(()).unwrap();
            let (lock, wake) = &*released;
            let mut ready = lock.lock().unwrap();
            while !*ready {
                ready = wake.wait(ready).unwrap();
            }
            Ok(vec!["8.8.8.8:443".parse().unwrap()])
        }))
    }
    fn release(&self) {
        let (lock, wake) = &*self.released;
        *lock.lock().unwrap() = true;
        wake.notify_all();
    }
}
impl Drop for BlockedResolvers {
    fn drop(&mut self) {
        // Also unblocks owned resolver threads if an assertion unwinds.
        self.release();
    }
}
async fn entered_twice(receiver: &mut mpsc::Receiver<()>, deadline: Instant) {
    for _ in 0..2 {
        timeout_at(deadline, receiver.recv())
            .await
            .expect("both actual resolver jobs must enter before the deadline")
            .expect("resolver event channel closed");
    }
}
async fn idle(control: &BlockedResolvers, slots: &Semaphore, deadline: Instant) {
    timeout_at(deadline, async {
        while control.active.load(Ordering::SeqCst) != 0 || slots.available_permits() != 2 {
            sleep(Duration::from_millis(1)).await;
        }
    })
    .await
    .expect("actual jobs must exit and restore their permits");
}

#[test]
fn dns_thread_capacity_is_capped_independently_of_model_concurrency() {
    assert_eq!(dns_slots(256).available_permits(), 2);
    assert_eq!(dns_slots(4).available_permits(), 2);
    assert_eq!(dns_slots(1).available_permits(), 1);
}

#[tokio::test]
async fn cancelled_waiters_keep_real_dns_capacity_until_lookup_exit() {
    let deadline = Instant::now() + Duration::from_secs(3);
    let slots = dns_slots(256);
    let (control, mut receiver) = BlockedResolvers::new();
    let first = control.launch(slots.clone());
    let second = control.launch(slots.clone());
    entered_twice(&mut receiver, deadline).await;
    assert_eq!(control.active.load(Ordering::SeqCst), 2);
    assert_eq!(slots.available_permits(), 0);
    first.abort();
    second.abort();
    assert!(first.await.unwrap_err().is_cancelled());
    assert!(second.await.unwrap_err().is_cancelled());
    assert_eq!(slots.available_permits(), 0);
    assert!(matches!(
        run_lookup(slots.clone(), || panic!("third resolver must not start")).await,
        Err(RelayError::Busy)
    ));
    assert_eq!(control.calls.load(Ordering::SeqCst), 2);
    assert_eq!(control.peak.load(Ordering::SeqCst), 2);
    control.release();
    idle(&control, &slots, deadline).await;
    let fresh = timeout_at(
        deadline,
        run_lookup(slots.clone(), || Ok(vec!["9.9.9.9:443".parse().unwrap()])),
    )
    .await
    .unwrap()
    .unwrap();
    assert_eq!(fresh, vec!["9.9.9.9:443".parse::<SocketAddr>().unwrap()]);
    // Cancelled one-shot results cannot become a later request's address set.
    assert_eq!(control.calls.load(Ordering::SeqCst), 2);
    assert_eq!(control.active.load(Ordering::SeqCst), 0);
}

fn real_password_hash_and_verify() {
    assert_ne!(std::thread::current().name(), Some("lmm-honeypot-dns"));
    // Exact current Core Argon2id engine and strength; no password-policy change.
    let params = Params::new(19_456, 2, 1, Some(32)).unwrap();
    let engine = Argon2::new(Algorithm::Argon2id, Version::V0x13, params);
    let salt = SaltString::encode_b64(b"owned-test-salt!").unwrap();
    let encoded = engine
        .hash_password(b"owned synthetic password", &salt)
        .unwrap()
        .to_string();
    assert!(encoded.starts_with("$argon2id$v=19$m=19456,t=2,p=1$"));
    engine
        .verify_password(
            b"owned synthetic password",
            &PasswordHash::new(&encoded).unwrap(),
        )
        .unwrap();
}

#[test]
fn paused_dns_does_not_delay_real_argon2_on_shared_blocking_pool() {
    for blocking_threads in [4, 1] {
        let runtime = tokio::runtime::Builder::new_current_thread()
            .max_blocking_threads(blocking_threads)
            .enable_all()
            .build()
            .unwrap();
        runtime.block_on(async {
            let deadline = Instant::now() + Duration::from_secs(3);
            let slots = dns_slots(256);
            let (control, mut receiver) = BlockedResolvers::new();
            let first = control.launch(slots.clone());
            let second = control.launch(slots.clone());
            entered_twice(&mut receiver, deadline).await;
            let started = Instant::now();
            timeout_at(deadline, tokio::task::spawn_blocking(real_password_hash_and_verify))
                .await.expect("real password work must finish while DNS is paused")
                .expect("password hash/verify failed");
            assert!(!*control.released.0.lock().unwrap(), "password must finish BEFORE resolver release");
            assert_eq!(control.active.load(Ordering::SeqCst), 2);
            assert_eq!(control.peak.load(Ordering::SeqCst), 2);
            println!("password finished before DNS release: blocking_pool={blocking_threads}, dns_active=2, password_ms={}", started.elapsed().as_millis());
            control.release();
            assert_eq!(timeout_at(deadline, first).await.unwrap().unwrap().unwrap().len(), 1);
            assert_eq!(timeout_at(deadline, second).await.unwrap().unwrap().unwrap().len(), 1);
            idle(&control, &slots, deadline).await;
        });
    }
}
