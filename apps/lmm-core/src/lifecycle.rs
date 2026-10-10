//! Request admission and drain ownership, not a billing-readiness assertion.
//!
//! Handlers that start settlement work must clone the request's `WorkLease`
//! into that work BEFORE returning the response. Release that clone only after
//! a durable terminal result, or a durable recovery record, has been written.
//! This module neither implements a ledger nor makes paid model routes ready.
use axum::{
    body::{Body, Bytes},
    extract::{Request, State},
    http::{StatusCode, Version, header},
    middleware::Next,
    response::{IntoResponse, Response},
};
use http_body::{Body as HttpBody, Frame, SizeHint};
use std::{
    pin::Pin,
    sync::{Arc, Mutex},
    task::{Context, Poll},
};
use tokio::sync::Notify;

pub mod ingress;

/// Per-instance optional ingress policy. A configured policy is fail-closed:
/// missing/invalid tickets cannot fall back to ordinary request admission.
#[derive(Clone)]
pub struct Admission {
    lifecycle: Lifecycle,
    ingress: Option<ingress::IngressAcks>,
}
impl Admission {
    pub fn new(lifecycle: Lifecycle, ingress: Option<ingress::IngressAcks>) -> Self {
        Self { lifecycle, ingress }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Phase {
    Preparing,
    Ready,
    Draining,
}

struct StateData {
    phase: Phase,
    active: usize,
}
struct Inner {
    state: Mutex<StateData>,
    idle: Notify,
}

#[derive(Clone)]
pub struct Lifecycle(Arc<Inner>);
impl Default for Lifecycle {
    fn default() -> Self {
        Self(Arc::new(Inner {
            state: Mutex::new(StateData {
                phase: Phase::Preparing,
                active: 0,
            }),
            idle: Notify::new(),
        }))
    }
}
impl Lifecycle {
    /// Call only after the enabled native routes' startup checks and binds.
    /// This does NOT change `/health/ready` or enable model forwarding.
    pub fn mark_ready(&self) -> bool {
        let mut state = self.0.state.lock().expect("lifecycle state");
        if state.phase != Phase::Preparing {
            return false;
        }
        state.phase = Phase::Ready;
        true
    }

    pub fn begin_drain(&self) {
        // Admission and drain share one lock: there is no check/increment gap.
        self.0.state.lock().expect("lifecycle state").phase = Phase::Draining;
    }

    pub fn snapshot(&self) -> (Phase, usize) {
        let state = self.0.state.lock().expect("lifecycle state");
        (state.phase, state.active)
    }

    pub fn try_accept(&self) -> Option<WorkLease> {
        let mut state = self.0.state.lock().expect("lifecycle state");
        if state.phase != Phase::Ready {
            return None;
        }
        state.active = state.active.checked_add(1)?;
        Some(WorkLease(Arc::new(Ticket(self.0.clone()))))
    }

    pub async fn wait_idle(&self) {
        loop {
            // Register before testing the count, including multiple waiters.
            let changed = self.0.idle.notified();
            tokio::pin!(changed);
            changed.as_mut().enable();
            if self.snapshot().1 == 0 {
                return;
            }
            changed.await;
        }
    }
}

struct Ticket(Arc<Inner>);
impl Drop for Ticket {
    fn drop(&mut self) {
        let mut state = self.0.state.lock().expect("lifecycle state");
        state.active -= 1;
        if state.active == 0 {
            self.0.idle.notify_waiters();
        }
    }
}

/// Each clone holds the same admitted request, rather than admitting new work.
/// Extract from request extensions and keep a clone in any settlement task.
#[derive(Clone)]
pub struct WorkLease(Arc<Ticket>);
impl WorkLease {
    /// Explicitly retain an admitted request for its asynchronous completion.
    pub fn retain_for_completion(&self) -> Self {
        Self(self.0.clone())
    }
}

struct LeasedBody {
    body: Pin<Box<Body>>,
    lease: Option<WorkLease>,
}
impl HttpBody for LeasedBody {
    type Data = Bytes;
    type Error = axum::Error;

    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, Self::Error>>> {
        let frame = self.body.as_mut().poll_frame(cx);
        if matches!(&frame, Poll::Ready(None) | Poll::Ready(Some(Err(_)))) {
            self.lease = None;
        }
        frame
    }

    fn is_end_stream(&self) -> bool {
        self.body.is_end_stream()
    }

    fn size_hint(&self) -> SizeHint {
        self.body.size_hint()
    }
}

/// Apply to the outside of the native router, so existing connections also
/// pass admission for EVERY new request/HTTP2 stream, not only on TCP accept.
pub async fn admit(
    State(policy): State<Admission>,
    mut request: Request,
    next: Next,
) -> Response {
    if request.uri().path() == "/health/live" {
        return next.run(request).await;
    }
    let Some(lease) = policy.lifecycle.try_accept() else {
        return unavailable(request.version(), "core_not_accepting");
    };
    if let Some(ingress) = &policy.ingress {
        let mut tickets = request.headers().get_all(ingress::TICKET_HEADER).iter();
        let ticket = tickets.next().and_then(|value| value.to_str().ok());
        if tickets.next().is_some() {
            return unavailable(request.version(), "core_admission_unconfirmed");
        }
        let Some(ticket) = ticket else {
            return unavailable(request.version(), "core_admission_unconfirmed");
        };
        // Own the lease BEFORE confirming admission. The ACK can let ingress
        // signal drain while this future is still waiting for the ACK reply.
        // Do not invoke the business handler until the ACK is definitive.
        if !ingress.confirm(ticket).await {
            return unavailable(request.version(), "core_admission_unconfirmed");
        }
    }
    request.extensions_mut().insert(lease.clone());
    let response = next.run(request).await;
    let (parts, body) = response.into_parts();
    Response::from_parts(
        parts,
        Body::new(LeasedBody {
            body: Box::pin(body),
            lease: Some(lease),
        }),
    )
}

fn unavailable(version: Version, code: &'static str) -> Response {
    let mut response = (
        StatusCode::SERVICE_UNAVAILABLE,
        [(header::CACHE_CONTROL, "no-store")],
        axum::Json(serde_json::json!({"error":{"code":code}})),
    ).into_response();
    // Never emit HTTP/1 connection headers on an HTTP/2 stream.
    if matches!(version, Version::HTTP_10 | Version::HTTP_11) {
        response.headers_mut().insert(header::CONNECTION, "close".parse().expect("static header"));
    }
    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{Router, http::Request as HttpRequest, middleware, routing::get};
    use std::time::Duration;
    use tower::ServiceExt;

    #[tokio::test]
    async fn preparing_and_draining_never_admit_new_work() {
        let lifecycle = Lifecycle::default();
        assert!(lifecycle.try_accept().is_none());
        assert!(lifecycle.mark_ready());
        let lease = lifecycle.try_accept().unwrap();
        lifecycle.begin_drain();
        assert!(lifecycle.try_accept().is_none());
        assert!(!lifecycle.mark_ready());
        assert_eq!(lifecycle.snapshot(), (Phase::Draining, 1));
        drop(lease);
        lifecycle.wait_idle().await;
    }

    #[tokio::test]
    async fn completion_lease_outlives_client_and_keeps_drain_open() {
        let lifecycle = Lifecycle::default();
        assert!(lifecycle.mark_ready());
        let request = lifecycle.try_accept().unwrap();
        let completion = request.retain_for_completion();
        lifecycle.begin_drain();
        drop(request);
        assert!(
            tokio::time::timeout(Duration::from_millis(10), lifecycle.wait_idle())
                .await
                .is_err()
        );
        drop(completion);
        tokio::time::timeout(Duration::from_secs(1), lifecycle.wait_idle())
            .await
            .unwrap();
    }

    #[tokio::test]
    async fn returning_headers_does_not_release_an_unread_body() {
        let lifecycle = Lifecycle::default();
        assert!(lifecycle.mark_ready());
        let app = Router::new()
            .route("/stream", get(|| async { Body::from("not yet consumed") }))
            .layer(middleware::from_fn_with_state(Admission::new(lifecycle.clone(), None), admit));
        let response = app
            .clone()
            .oneshot(
                HttpRequest::builder()
                    .uri("/stream")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(lifecycle.snapshot().1, 1);
        lifecycle.begin_drain();
        let denied = app
            .oneshot(
                HttpRequest::builder()
                    .uri("/stream")
                    .version(Version::HTTP_2)
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(denied.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert!(!denied.headers().contains_key(header::CONNECTION));
        drop(response);
        lifecycle.wait_idle().await;
    }
}
