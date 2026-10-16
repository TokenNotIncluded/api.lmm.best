//! Opt-in native-browser relay. Transport credentials never grant user authority.
use crate::identity::{IdentityError, IdentityStore};
use axum::{
    Router,
    body::{Body, Bytes, to_bytes},
    extract::{Request, State},
    http::{StatusCode, header},
    response::{IntoResponse, Response},
    routing::any,
};
use http_body::{Body as HttpBody, Frame};
use std::{
    io,
    pin::Pin,
    sync::Arc,
    task::{Context, Poll},
    time::Duration,
};
use tokio::{
    sync::{Semaphore, mpsc, watch},
    task::JoinHandle,
};
use tokio_stream::{StreamExt, wrappers::ReceiverStream};

/// Maximum duration of a pending downstream write operation, not a stream lifetime.
pub const PUBLIC_WRITE_TIMEOUT: Duration = Duration::from_secs(5);

// Healthy Go streams recheck after at most 7 s, with 3 s for Core RPC and 5 s
// for a write. Keep margin for that path, but release local capacity if Go
// stops making byte progress even when its own 60-second lease cannot run.
const UPSTREAM_BYTE_IDLE_TIMEOUT: Duration = Duration::from_secs(20);

// Write timeout semantics adapted from the public Tokio adapter tokio-io-timeout
// 1.2.1, sfackler/tokio-io-timeout@c2406c689c782e9005af376b7359007a4a8faa96,
// src/lib.rs TimeoutState and TimeoutWriter. Kept local to avoid adding a new
// dependency; boxed Sleep replaces upstream pin-project-lite projection.
// Copyright (c) 2017 The tokio-io-timeout Developers
//
// Permission is hereby granted, free of charge, to any person obtaining a copy
// of this software and associated documentation files (the "Software"), to deal
// in the Software without restriction, including without limitation the rights
// to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
// copies of the Software, and to permit persons to whom the Software is
// furnished to do so, subject to the following conditions:
//
// The above copyright notice and this permission notice shall be included in all
// copies or substantial portions of the Software.
//
// THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
// IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
// FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
// AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
// LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
// OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN THE
// SOFTWARE.
/// Tokio connection adapter: only pending writes arm its progress timer.
/// Reads and idle/healthy streams have no connection-wide lifetime timeout.
pub struct PublicWriteIo<S> {
    inner: S,
    pending: Option<Pin<Box<tokio::time::Sleep>>>,
    timeout_abort: Option<fn(&S) -> io::Result<()>>,
    failed: Option<(io::ErrorKind, Option<i32>)>,
}
impl<S> PublicWriteIo<S> {
    fn new(inner: S) -> Self {
        Self {
            inner,
            pending: None,
            timeout_abort: None,
            failed: None,
        }
    }
    fn with_timeout_abort(mut self, abort: fn(&S) -> io::Result<()>) -> Self {
        self.timeout_abort = Some(abort);
        self
    }
    fn failure(&self) -> Option<io::Error> {
        self.failed
            .map(|(kind, errno)| errno.map_or_else(|| kind.into(), io::Error::from_raw_os_error))
    }
    fn checked<T>(
        &mut self,
        cx: &mut Context<'_>,
        result: Poll<io::Result<T>>,
    ) -> Poll<io::Result<T>> {
        use std::future::Future;
        match result {
            Poll::Pending => {
                let timer = self
                    .pending
                    .get_or_insert_with(|| Box::pin(tokio::time::sleep(PUBLIC_WRITE_TIMEOUT)));
                if timer.as_mut().poll(cx).is_ready() {
                    self.pending = None;
                    self.failed = Some((io::ErrorKind::TimedOut, None));
                    // Abort only this already failed transport. Normal FIN and
                    // healthy shared HTTP/2 connections keep their original behavior.
                    // Tokio 1.53.2 set_zero_linger is nonblocking; positive linger is not used.
                    // @mark model.socket-pressure role=implementation
                    if let Some(abort) = self.timeout_abort
                        && let Err(error) = abort(&self.inner)
                    {
                        self.failed = Some((error.kind(), error.raw_os_error()));
                        // Payload-free fields only: no peer, request, raw error description or credentials.
                        eprintln!(
                            "core_socket_abort_failed kind={:?} os_error={:?}",
                            error.kind(),
                            error.raw_os_error()
                        );
                        return Poll::Ready(Err(error));
                    }
                    Poll::Ready(Err(io::ErrorKind::TimedOut.into()))
                } else {
                    Poll::Pending
                }
            }
            ready => {
                self.pending = None;
                ready
            }
        }
    }
}
impl<S: tokio::io::AsyncRead + Unpin> tokio::io::AsyncRead for PublicWriteIo<S> {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut tokio::io::ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        if let Some(error) = self.failure() {
            return Poll::Ready(Err(error));
        }
        Pin::new(&mut self.inner).poll_read(cx, buf)
    }
}
impl<S: tokio::io::AsyncWrite + Unpin> tokio::io::AsyncWrite for PublicWriteIo<S> {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        if let Some(error) = self.failure() {
            return Poll::Ready(Err(error));
        }
        let result = Pin::new(&mut self.inner).poll_write(cx, buf);
        self.checked(cx, result)
    }
    fn poll_write_vectored(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        if let Some(error) = self.failure() {
            return Poll::Ready(Err(error));
        }
        let result = Pin::new(&mut self.inner).poll_write_vectored(cx, bufs);
        self.checked(cx, result)
    }
    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }
    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        if let Some(error) = self.failure() {
            return Poll::Ready(Err(error));
        }
        let result = Pin::new(&mut self.inner).poll_flush(cx);
        self.checked(cx, result)
    }
    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        if let Some(error) = self.failure() {
            return Poll::Ready(Err(error));
        }
        let result = Pin::new(&mut self.inner).poll_shutdown(cx);
        self.checked(cx, result)
    }
}
impl PublicWriteIo<tokio::net::TcpStream> {
    /// Drop kernel buffers only for rejected transports or connections that
    /// never reached a parsed request. Healthy streams retain normal closing.
    pub(crate) fn reject_unadmitted(&self) -> io::Result<()> {
        self.inner.set_zero_linger()
    }
}

/// Axum listener seam shared by the executable and exported integration fixtures.
pub struct PublicWriteListener(tokio::net::TcpListener);
impl axum::serve::Listener for PublicWriteListener {
    type Io = PublicWriteIo<tokio::net::TcpStream>;
    type Addr = std::net::SocketAddr;
    async fn accept(&mut self) -> (Self::Io, Self::Addr) {
        let (stream, addr) = axum::serve::Listener::accept(&mut self.0).await;
        (
            PublicWriteIo::new(stream).with_timeout_abort(tokio::net::TcpStream::set_zero_linger),
            addr,
        )
    }
    fn local_addr(&self) -> io::Result<Self::Addr> {
        self.0.local_addr()
    }
}
// Axum's TapIo has the upstream Connected<IncomingStream> implementation for
// SocketAddr; retain existing ConnectInfo extractors without an orphan impl.
pub type PublicListener =
    axum::serve::TapIo<PublicWriteListener, fn(&mut PublicWriteIo<tokio::net::TcpStream>)>;
pub fn public_listener(listener: tokio::net::TcpListener) -> PublicListener {
    use axum::serve::ListenerExt;
    PublicWriteListener(listener).tap_io((|_| {}) as fn(&mut PublicWriteIo<tokio::net::TcpStream>))
}

const PREFIX: &str = "/api/remote-control/v1/pi/";
const BODY_LIMIT: usize = 96 << 10;
const RESPONSE_LIMIT: usize = 12 << 20;
#[derive(Clone)]
pub struct RemoteProxy {
    store: IdentityStore,
    client: reqwest::Client,
    origin: String,
    service: reqwest::header::HeaderValue,
    slots: Arc<Semaphore>,
    stopped: watch::Receiver<bool>,
}
impl RemoteProxy {
    /// Only a fixed loopback HTTP origin is accepted; no paths, DNS, redirects,
    /// cookies, environment proxy or caller-selected destinations.
    pub fn new(
        store: IdentityStore,
        origin: &str,
        secret: &[u8],
        stopped: watch::Receiver<bool>,
    ) -> io::Result<Self> {
        let url = reqwest::Url::parse(origin).map_err(|_| invalid())?;
        if url.scheme() != "http"
            || url.host_str() != Some("127.0.0.1")
            || url.port().is_none_or(|port| port == 0)
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
            || origin != url.origin().ascii_serialization()
        {
            return Err(invalid());
        }
        let token = std::str::from_utf8(secret).map_err(|_| invalid())?;
        if !(32..=256).contains(&token.len()) || !token.bytes().all(|b| (33..=126).contains(&b)) {
            return Err(invalid());
        }
        let mut service = reqwest::header::HeaderValue::from_str(&format!("Bearer {token}"))
            .map_err(|_| invalid())?;
        service.set_sensitive(true);
        let client = reqwest::Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .connect_timeout(Duration::from_secs(2))
            .pool_max_idle_per_host(4)
            .build()
            .map_err(|_| invalid())?;
        Ok(Self {
            store,
            client,
            origin: origin.into(),
            service,
            slots: Arc::new(Semaphore::new(8)),
            stopped,
        })
    }
    pub fn router(self) -> Router {
        Router::new()
            .route("/api/remote-control/v1/pi/{*path}", any(proxy))
            .with_state(self)
    }
}
fn invalid() -> io::Error {
    io::Error::new(
        io::ErrorKind::InvalidInput,
        "invalid remote relay configuration",
    )
}
fn failure(status: StatusCode, code: &'static str) -> Response {
    let mut response = safe(
        (
            status,
            axum::Json(serde_json::json!({"success":false,"error":code})),
        )
            .into_response(),
    );
    if status == StatusCode::TOO_MANY_REQUESTS && code == "relay_capacity" {
        response
            .headers_mut()
            .insert(header::RETRY_AFTER, "1".parse().unwrap());
    }
    response
}
fn safe(mut response: Response) -> Response {
    for (name, value) in [
        (header::CACHE_CONTROL, "no-store"),
        (header::REFERRER_POLICY, "no-referrer"),
        (header::X_CONTENT_TYPE_OPTIONS, "nosniff"),
    ] {
        response.headers_mut().insert(name, value.parse().unwrap());
    }
    response
}
fn identifier(id: &str) -> bool {
    (8..=64).contains(&id.len())
        && id
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
}
fn credential(request: &Request) -> Option<String> {
    // Reject forged internal headers, rather than ambiguously merging them.
    if request
        .headers()
        .keys()
        .any(|h| h.as_str().starts_with("x-lmm-"))
    {
        return None;
    }
    let mut values = request.headers().get_all(header::AUTHORIZATION).iter();
    let raw = values.next()?.to_str().ok()?;
    if values.next().is_some() {
        return None;
    }
    let (scheme, token) = raw.split_once(' ')?;
    if !scheme.eq_ignore_ascii_case("bearer")
        || !(32..=256).contains(&token.len())
        || !token
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
    {
        return None;
    }
    Some(token.into())
}
fn operation(request: &Request) -> Option<bool> {
    let path = request.uri().path().strip_prefix(PREFIX)?;
    let parts: Vec<_> = path.split('/').collect();
    let method = request.method().as_str();
    let stream = match parts.as_slice() {
        ["sessions"] if method == "GET" => false,
        ["sessions", id] if identifier(id) && (method == "PUT" || method == "DELETE") => false,
        ["sessions", id, "messages"] if identifier(id) && (method == "GET" || method == "POST") => {
            false
        }
        ["sessions", id, "events"] if identifier(id) && method == "GET" => true,
        _ => return None,
    };
    // Cursor semantics are owned by Go. Only exact supported keys get there.
    let query = request.uri().query().unwrap_or("");
    if query.len() > 256 {
        return None;
    }
    let mut keys = std::collections::BTreeSet::new();
    for pair in query.split('&').filter(|s| !s.is_empty()) {
        let (key, _) = pair.split_once('=')?;
        if !keys.insert(key)
            || !(method == "GET"
                && parts.len() == 3
                && (key == "after" || (stream && key == "generation")))
        {
            return None;
        }
    }
    Some(stream)
}
#[derive(Clone)]
struct NativeRemote {
    authority: Arc<crate::identity::grants::RemoteAuthority>,
    token: String,
}

/// Mounted outside the generic Core admission layer so unauthenticated remote
/// callers cannot observe capacity or consume a streaming permit.
pub async fn preadmit(
    State(state): State<RemoteProxy>,
    mut request: Request,
    next: axum::middleware::Next,
) -> Response {
    if request.uri().path().starts_with(PREFIX) {
        match authenticate(&state, credential(&request)).await {
            Ok(actor) => {
                request.extensions_mut().insert(actor);
            }
            Err((status, code)) => return failure(status, code),
        }
    }
    next.run(request).await
}
async fn authenticate(
    state: &RemoteProxy,
    token: Option<String>,
) -> Result<NativeRemote, (StatusCode, &'static str)> {
    let Some(token) = token else {
        return Err((StatusCode::UNAUTHORIZED, "unauthorized"));
    };
    // No OAuth translation or grant-store fallback in this native-only slice.
    let authority = match tokio::time::timeout(
        Duration::from_secs(3),
        state.store.authorize_remote_native(&token),
    )
    .await
    {
        Ok(Ok(actor)) => actor,
        Ok(Err(IdentityError::Unauthorized)) => {
            return Err((StatusCode::UNAUTHORIZED, "unauthorized"));
        }
        Ok(Err(IdentityError::Forbidden)) => {
            return Err((StatusCode::FORBIDDEN, "browser_session_required"));
        }
        _ => return Err((StatusCode::SERVICE_UNAVAILABLE, "core_unavailable")),
    };
    Ok(NativeRemote {
        authority: Arc::new(authority),
        token,
    })
}
async fn proxy(State(state): State<RemoteProxy>, mut request: Request) -> Response {
    let actor = match request.extensions_mut().remove::<NativeRemote>() {
        Some(actor) => actor,
        None => match authenticate(&state, credential(&request)).await {
            Ok(actor) => actor,
            Err((status, code)) => return failure(status, code),
        },
    };
    let NativeRemote { authority, token } = actor;
    let Some(stream) = operation(&request) else {
        return failure(StatusCode::BAD_REQUEST, "invalid_request");
    };
    if *state.stopped.borrow() {
        return failure(StatusCode::SERVICE_UNAVAILABLE, "remote_unavailable");
    }
    let method = request.method().clone();
    // The validated route set contains only session PUT or message POST as
    // new actions. GET history/events and DELETE cleanup keep their authority.
    let new_execution = method == axum::http::Method::PUT || method == axum::http::Method::POST;
    if new_execution {
        match tokio::time::timeout(
            Duration::from_secs(3),
            state.store.admit_new_remote_execution(&token),
        )
        .await
        {
            Ok(Ok(())) => {}
            Ok(Err(IdentityError::Unauthorized)) => {
                return failure(StatusCode::UNAUTHORIZED, "unauthorized");
            }
            Ok(Err(IdentityError::Forbidden)) => {
                return failure(StatusCode::FORBIDDEN, "forbidden");
            }
            _ => return failure(StatusCode::SERVICE_UNAVAILABLE, "core_unavailable"),
        }
    }
    let Ok(permit) = state.slots.clone().try_acquire_owned() else {
        return failure(StatusCode::TOO_MANY_REQUESTS, "relay_capacity");
    };
    let destination = format!(
        "{}/extensions/v1/remote-control/pi/{}",
        state.origin,
        request
            .uri()
            .path_and_query()
            .unwrap()
            .as_str()
            .strip_prefix(PREFIX)
            .unwrap()
    );
    let body = match tokio::time::timeout(
        Duration::from_secs(10),
        to_bytes(request.into_body(), BODY_LIMIT),
    )
    .await
    {
        Ok(Ok(body)) => body,
        _ => return failure(StatusCode::PAYLOAD_TOO_LARGE, "invalid_request_body"),
    };
    let mut user = reqwest::header::HeaderValue::from_str(&token).unwrap();
    user.set_sensitive(true);
    let send = state
        .client
        .request(method, destination)
        .header(header::AUTHORIZATION, state.service.clone())
        .header("x-lmm-user-credential", user)
        .header(header::CONTENT_TYPE, "application/json")
        .body(body)
        .send();
    let upstream = match tokio::time::timeout(Duration::from_secs(3), send).await {
        Ok(Ok(response)) => response,
        _ => return failure(StatusCode::SERVICE_UNAVAILABLE, "remote_unavailable"),
    };
    if upstream.status() != StatusCode::OK {
        let (status, code) = match upstream.status().as_u16() {
            400 => (StatusCode::BAD_REQUEST, "invalid_request"),
            401 => (StatusCode::UNAUTHORIZED, "unauthorized"),
            403 => (StatusCode::FORBIDDEN, "forbidden"),
            404 => (StatusCode::NOT_FOUND, "session_unavailable"),
            409 => (StatusCode::CONFLICT, "session_device_changed"),
            413 => (StatusCode::PAYLOAD_TOO_LARGE, "request_too_large"),
            429 => (StatusCode::TOO_MANY_REQUESTS, "relay_capacity"),
            _ => (StatusCode::SERVICE_UNAVAILABLE, "remote_unavailable"),
        };
        return failure(status, code);
    }
    let expected = if stream {
        "text/event-stream"
    } else {
        "application/json"
    };
    if upstream
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|h| h.to_str().ok())
        != Some(expected)
    {
        return failure(StatusCode::BAD_GATEWAY, "remote_unavailable");
    }
    let mut chunks = upstream.bytes_stream();
    if !stream {
        let read = async {
            let mut body = Vec::new();
            while let Some(chunk) = chunks.next().await {
                let chunk = chunk.map_err(|_| ())?;
                if body.len() + chunk.len() > RESPONSE_LIMIT {
                    return Err(());
                }
                body.extend_from_slice(&chunk);
            }
            Ok(body)
        };
        return match tokio::time::timeout(Duration::from_secs(10), read).await {
            Ok(Ok(body)) => safe(([(header::CONTENT_TYPE, expected)], body).into_response()),
            _ => failure(StatusCode::BAD_GATEWAY, "remote_unavailable"),
        };
    }
    let (sender, receiver) = mpsc::channel::<Result<Bytes, io::Error>>(1);
    let stopped = state.stopped.clone();
    let task = tokio::spawn(async move {
        let _permit = permit;
        relay_stream(chunks, sender, stopped, authority.expires_at).await;
    });
    let body = CancelBody {
        body: Box::pin(Body::from_stream(ReceiverStream::new(receiver))),
        task,
    };
    safe(
        (
            [
                (header::CONTENT_TYPE, "text/event-stream"),
                ("x-accel-buffering".parse().unwrap(), "no"),
            ],
            Body::new(body),
        )
            .into_response(),
    )
}
async fn relay_stream<S, E>(
    mut chunks: S,
    sender: mpsc::Sender<Result<Bytes, io::Error>>,
    mut stopped: watch::Receiver<bool>,
    expires_at: i64,
) where
    S: tokio_stream::Stream<Item = Result<Bytes, E>> + Unpin,
{
    let expiry = tokio::time::sleep(Duration::from_secs(
        expires_at
            .saturating_sub(chrono::Utc::now().timestamp())
            .max(0) as u64,
    ));
    let idle = tokio::time::sleep(UPSTREAM_BYTE_IDLE_TIMEOUT);
    tokio::pin!(expiry, idle);
    loop {
        let chunk = tokio::select! {
            // Ready data, including empty chunks, must not starve cancellation
            // or either deadline. Only received nonempty bytes renew idle time.
            biased;
            _ = sender.closed() => break,
            _ = stopped.changed() => break,
            _ = &mut expiry => break,
            _ = &mut idle => break,
            chunk = chunks.next() => chunk,
        };
        let Some(chunk) = chunk else {
            break;
        };
        if let Ok(bytes) = &chunk {
            if bytes.is_empty() {
                tokio::task::yield_now().await;
                continue;
            }
            idle.as_mut()
                .reset(tokio::time::Instant::now() + UPSTREAM_BYTE_IDLE_TIMEOUT);
        }
        let data = chunk.map_err(|_| io::Error::other("remote stream unavailable"));
        let failed = data.is_err();
        // One queued chunk, no polling fallback. Slow writers close upstream
        // and release Go capacity instead of retaining an unbounded buffer.
        let sent = tokio::select! {
            _ = stopped.changed() => break,
            _ = &mut expiry => break,
            sent = tokio::time::timeout(Duration::from_secs(5), sender.send(data)) => sent,
        };
        if !matches!(sent, Ok(Ok(()))) || failed {
            break;
        }
    }
}
struct CancelBody {
    body: Pin<Box<Body>>,
    task: JoinHandle<()>,
}
impl Drop for CancelBody {
    fn drop(&mut self) {
        self.task.abort();
    }
}
impl HttpBody for CancelBody {
    type Data = Bytes;
    type Error = axum::Error;
    fn poll_frame(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, Self::Error>>> {
        self.body.as_mut().poll_frame(cx)
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn missing_credential_rejects_before_database_and_capacity_at_both_boundaries() {
        use std::sync::atomic::{AtomicUsize, Ordering};
        use tower::ServiceExt;

        // No PostgreSQL authorization is exercised: the lazy pool is closed
        // before either request, so any accidental database access must fail.
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgresql://unused@127.0.0.1/unused")
            .unwrap();
        pool.close().await;
        let (_stop, stopped) = watch::channel(false);
        let state = RemoteProxy::new(
            IdentityStore::from_pool(pool),
            "http://127.0.0.1:1234",
            b"test_transport_credential_1234567890123456",
            stopped,
        )
        .unwrap();
        let _held = state.slots.clone().try_acquire_many_owned(8).unwrap();
        let downstream_calls = Arc::new(AtomicUsize::new(0));
        let observed = downstream_calls.clone();
        let gated = Router::new()
            .fallback(move || {
                let observed = observed.clone();
                async move {
                    observed.fetch_add(1, Ordering::SeqCst);
                    StatusCode::OK
                }
            })
            .layer(axum::middleware::from_fn_with_state(
                state.clone(),
                preadmit,
            ));
        // Direct proxy covers fallback authentication. The sentinel app proves
        // preadmit rejects before the downstream admission/handler is entered.
        for app in [state.clone().router(), gated] {
            let request = Request::builder()
                .uri(format!("{PREFIX}sessions"))
                .body(Body::empty())
                .unwrap();
            let response = app.oneshot(request).await.unwrap();
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
            assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
            assert_eq!(response.headers()[header::REFERRER_POLICY], "no-referrer");
            assert_eq!(
                response.headers()[header::X_CONTENT_TYPE_OPTIONS],
                "nosniff"
            );
            let body = to_bytes(response.into_body(), 1024).await.unwrap();
            assert_eq!(
                serde_json::from_slice::<serde_json::Value>(&body).unwrap(),
                serde_json::json!({"success":false,"error":"unauthorized"}),
            );
            assert_eq!(state.slots.available_permits(), 0);
        }
        assert_eq!(downstream_calls.load(Ordering::SeqCst), 0);
    }

    #[tokio::test]
    async fn upstream_idle_and_empty_chunks_release_relay_capacity() {
        async fn check<S>(chunks: S)
        where
            S: tokio_stream::Stream<Item = Result<Bytes, io::Error>> + Unpin + Send + 'static,
        {
            let slots = Arc::new(Semaphore::new(1));
            let permit = slots.clone().acquire_owned().await.unwrap();
            let (sender, mut receiver) = mpsc::channel(1);
            let (_stop, stopped) = watch::channel(false);
            let started = tokio::time::Instant::now();
            let task = tokio::spawn(async move {
                let _permit = permit;
                relay_stream(
                    chunks,
                    sender,
                    stopped,
                    chrono::Utc::now().timestamp() + 300,
                )
                .await;
            });
            let chunk = tokio::time::timeout(
                UPSTREAM_BYTE_IDLE_TIMEOUT + Duration::from_secs(3),
                receiver.recv(),
            )
            .await
            .expect("upstream with no byte progress retained the live response");
            assert!(chunk.is_none(), "empty chunks reached the downstream queue");
            task.await.unwrap();
            assert!(started.elapsed() >= UPSTREAM_BYTE_IDLE_TIMEOUT);
            // The downstream receiver stays alive: neither cancellation nor
            // dropping the client may be needed to restore local capacity.
            assert_eq!(slots.available_permits(), 1);
        }
        tokio::join!(
            check(tokio_stream::pending::<Result<Bytes, io::Error>>()),
            check(tokio_stream::iter(std::iter::repeat_with(|| {
                Ok::<_, io::Error>(Bytes::new())
            })))
        );
    }

    #[tokio::test]
    async fn upstream_byte_progress_outlives_idle_budget() {
        let chunks = tokio_stream::wrappers::IntervalStream::new(tokio::time::interval(
            Duration::from_secs(1),
        ))
        .take(22)
        .map(|_| Ok::<_, io::Error>(Bytes::from_static(b": keepalive\n\n")));
        let slots = Arc::new(Semaphore::new(1));
        let permit = slots.clone().acquire_owned().await.unwrap();
        let (sender, mut receiver) = mpsc::channel(1);
        let (_stop, stopped) = watch::channel(false);
        let started = tokio::time::Instant::now();
        let task = tokio::spawn(async move {
            let _permit = permit;
            relay_stream(
                chunks,
                sender,
                stopped,
                chrono::Utc::now().timestamp() + 300,
            )
            .await;
        });
        let mut bytes = Vec::new();
        while let Some(chunk) = tokio::time::timeout(Duration::from_secs(3), receiver.recv())
            .await
            .expect("healthy byte progress stalled")
        {
            bytes.extend_from_slice(&chunk.unwrap());
        }
        task.await.unwrap();
        assert!(started.elapsed() > UPSTREAM_BYTE_IDLE_TIMEOUT);
        assert_eq!(bytes, b": keepalive\n\n".repeat(22));
        assert_eq!(slots.available_permits(), 1);
    }

    struct PendingWriter;
    impl tokio::io::AsyncWrite for PendingWriter {
        fn poll_write(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
            _: &[u8],
        ) -> Poll<io::Result<usize>> {
            Poll::Pending
        }
        fn poll_write_vectored(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
            _: &[io::IoSlice<'_>],
        ) -> Poll<io::Result<usize>> {
            Poll::Pending
        }
        fn poll_flush(self: Pin<&mut Self>, _: &mut Context<'_>) -> Poll<io::Result<()>> {
            Poll::Pending
        }
        fn poll_shutdown(self: Pin<&mut Self>, _: &mut Context<'_>) -> Poll<io::Result<()>> {
            Poll::Pending
        }
    }
    #[tokio::test]
    async fn public_write_timeout_bounds_each_pending_operation() {
        use tokio::io::AsyncWriteExt;
        async fn pending(mode: usize) {
            let mut writer = PublicWriteIo::new(PendingWriter);
            let result =
                tokio::time::timeout(PUBLIC_WRITE_TIMEOUT + Duration::from_secs(1), async {
                    match mode {
                        0 => writer.write(b"x").await.map(|_| ()),
                        1 => writer
                            .write_vectored(&[io::IoSlice::new(b"x")])
                            .await
                            .map(|_| ()),
                        2 => writer.flush().await,
                        _ => writer.shutdown().await,
                    }
                })
                .await
                .expect("pending operation did not wake");
            assert_eq!(result.unwrap_err().kind(), io::ErrorKind::TimedOut);
        }
        tokio::join!(pending(0), pending(1), pending(2), pending(3));
    }
    #[derive(Default)]
    struct AbortProbe {
        polls: std::sync::atomic::AtomicUsize,
        aborts: std::sync::atomic::AtomicUsize,
        ready: std::sync::atomic::AtomicBool,
        abort_fails: bool,
    }
    impl AbortProbe {
        fn poll(&self) -> Poll<io::Result<usize>> {
            use std::sync::atomic::Ordering;
            self.polls.fetch_add(1, Ordering::SeqCst);
            if self.ready.load(Ordering::SeqCst) {
                Poll::Ready(Ok(1))
            } else {
                Poll::Pending
            }
        }
        fn abort(&self) -> io::Result<()> {
            self.aborts
                .fetch_add(1, std::sync::atomic::Ordering::SeqCst);
            if self.abort_fails {
                Err(io::Error::from_raw_os_error(13))
            } else {
                Ok(())
            }
        }
    }
    impl tokio::io::AsyncRead for AbortProbe {
        fn poll_read(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
            _: &mut tokio::io::ReadBuf<'_>,
        ) -> Poll<io::Result<()>> {
            self.poll().map(|result| result.map(|_| ()))
        }
    }
    impl tokio::io::AsyncWrite for AbortProbe {
        fn poll_write(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
            _: &[u8],
        ) -> Poll<io::Result<usize>> {
            self.poll()
        }
        fn poll_write_vectored(
            self: Pin<&mut Self>,
            _: &mut Context<'_>,
            _: &[io::IoSlice<'_>],
        ) -> Poll<io::Result<usize>> {
            self.poll()
        }
        fn poll_flush(self: Pin<&mut Self>, _: &mut Context<'_>) -> Poll<io::Result<()>> {
            self.poll().map(|result| result.map(|_| ()))
        }
        fn poll_shutdown(self: Pin<&mut Self>, _: &mut Context<'_>) -> Poll<io::Result<()>> {
            self.poll().map(|result| result.map(|_| ()))
        }
    }
    async fn sticky_abort_case(mode: usize, abort_fails: bool) {
        use std::sync::atomic::Ordering;
        use tokio::io::{AsyncReadExt, AsyncWriteExt};
        let mut stream = PublicWriteIo::new(AbortProbe {
            abort_fails,
            ..Default::default()
        })
        .with_timeout_abort(AbortProbe::abort);
        // Test the transition without sleeping five seconds in every matrix cell.
        // Existing public_write_timeout_bounds_each_pending_operation verifies the real budget.
        stream.pending = Some(Box::pin(tokio::time::sleep(Duration::ZERO)));
        let error = tokio::time::timeout(Duration::from_secs(1), async {
            match mode {
                0 => stream.write(b"x").await.map(|_| ()),
                1 => stream
                    .write_vectored(&[io::IoSlice::new(b"x")])
                    .await
                    .map(|_| ()),
                2 => stream.flush().await,
                _ => stream.shutdown().await,
            }
        })
        .await
        .unwrap()
        .unwrap_err();
        if abort_fails {
            assert_eq!(error.raw_os_error(), Some(13));
        } else {
            assert_eq!(error.kind(), io::ErrorKind::TimedOut);
        }
        assert_eq!(stream.inner.aborts.load(Ordering::SeqCst), 1);
        let polls = stream.inner.polls.load(Ordering::SeqCst);
        stream.inner.ready.store(true, Ordering::SeqCst);
        let mut byte = [0u8; 1];
        for error in [
            stream.write(b"x").await.unwrap_err(),
            stream
                .write_vectored(&[io::IoSlice::new(b"x")])
                .await
                .unwrap_err(),
            stream.flush().await.unwrap_err(),
            stream.shutdown().await.unwrap_err(),
            stream.read(&mut byte).await.unwrap_err(),
        ] {
            if abort_fails {
                assert_eq!(error.raw_os_error(), Some(13));
            } else {
                assert_eq!(error.kind(), io::ErrorKind::TimedOut);
            }
        }
        assert_eq!(
            stream.inner.polls.load(Ordering::SeqCst),
            polls,
            "failed socket must short circuit before touching inner IO"
        );
        assert_eq!(
            stream.inner.aborts.load(Ordering::SeqCst),
            1,
            "abort is invoked only once"
        );
    }
    #[tokio::test]
    async fn public_write_abort_is_sticky_for_every_pending_operation() {
        tokio::join!(
            sticky_abort_case(0, false),
            sticky_abort_case(1, false),
            sticky_abort_case(2, false),
            sticky_abort_case(3, false)
        );
    }
    #[tokio::test]
    async fn public_write_abort_failure_preserves_errno_and_prevents_more_io() {
        tokio::join!(
            sticky_abort_case(0, true),
            sticky_abort_case(1, true),
            sticky_abort_case(2, true),
            sticky_abort_case(3, true)
        );
    }
    #[tokio::test]
    async fn public_write_progress_never_invokes_abort() {
        use std::sync::atomic::Ordering;
        use tokio::io::AsyncWriteExt;
        let mut stream =
            PublicWriteIo::new(AbortProbe::default()).with_timeout_abort(AbortProbe::abort);
        stream.inner.ready.store(true, Ordering::SeqCst);
        assert_eq!(stream.write(b"x").await.unwrap(), 1);
        assert_eq!(
            stream
                .write_vectored(&[io::IoSlice::new(b"x")])
                .await
                .unwrap(),
            1
        );
        stream.flush().await.unwrap();
        stream.shutdown().await.unwrap();
        assert_eq!(stream.inner.aborts.load(Ordering::SeqCst), 0);
        assert!(stream.failed.is_none());
        assert!(stream.pending.is_none());
    }
    #[tokio::test]
    async fn public_listener_installs_abort_only_for_an_expired_write() {
        use axum::serve::Listener;
        use tokio::io::AsyncReadExt;
        let socket = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = socket.local_addr().unwrap();
        let mut listener = PublicWriteListener(socket);
        let mut client = tokio::net::TcpStream::connect(address).await.unwrap();
        let (mut accepted, _) = listener.accept().await;
        assert_eq!(
            accepted.inner.linger().unwrap(),
            None,
            "normal socket must retain FIN semantics"
        );
        accepted.pending = Some(Box::pin(tokio::time::sleep(Duration::ZERO)));
        let result = std::future::poll_fn(|cx| accepted.checked::<()>(cx, Poll::Pending)).await;
        assert_eq!(result.unwrap_err().kind(), io::ErrorKind::TimedOut);
        assert_eq!(accepted.inner.linger().unwrap(), Some(Duration::ZERO));
        drop(accepted);
        let mut byte = [0u8; 1];
        let error = tokio::time::timeout(Duration::from_secs(1), client.read(&mut byte))
            .await
            .unwrap()
            .unwrap_err();
        assert_eq!(error.kind(), io::ErrorKind::ConnectionReset);
    }

    #[tokio::test]
    async fn public_listener_keeps_progressing_stream_beyond_write_timeout() {
        use axum::routing::get;
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let app = Router::new().route(
            "/events",
            get(|| async {
                let chunks = tokio_stream::wrappers::IntervalStream::new(tokio::time::interval(
                    Duration::from_secs(1),
                ))
                .take(8)
                .map(|_| Ok::<_, io::Error>(Bytes::from_static(b": healthy\n\n")));
                (
                    [(header::CONTENT_TYPE, "text/event-stream")],
                    Body::from_stream(chunks),
                )
            }),
        );
        let (stop, mut stopped) = watch::channel(false);
        let task = tokio::spawn(async move {
            axum::serve(public_listener(listener), app)
                .with_graceful_shutdown(async move {
                    let _ = stopped.changed().await;
                })
                .await
                .unwrap();
        });
        let response = reqwest::Client::builder()
            .no_proxy()
            .build()
            .unwrap()
            .get(format!("http://{address}/events"))
            .send()
            .await
            .unwrap();
        let start = tokio::time::Instant::now();
        let mut chunks = response.bytes_stream();
        let mut bytes = Vec::new();
        while let Some(chunk) = tokio::time::timeout(Duration::from_secs(2), chunks.next())
            .await
            .unwrap()
        {
            bytes.extend_from_slice(&chunk.unwrap());
        }
        assert!(
            start.elapsed() > PUBLIC_WRITE_TIMEOUT,
            "test did not outlive pending-write budget"
        );
        assert_eq!(bytes, b": healthy\n\n".repeat(8));
        stop.send(true).unwrap();
        task.await.unwrap();
    }
    #[tokio::test]
    async fn fixed_loopback_origin_and_exact_operation_allowlist() {
        let store = IdentityStore::from_pool(
            sqlx::postgres::PgPoolOptions::new()
                .connect_lazy("postgresql://unused@127.0.0.1/unused")
                .unwrap(),
        );
        let (_, stopped) = watch::channel(false);
        let secret = b"test_transport_credential_1234567890123456";
        for origin in [
            "https://127.0.0.1:1234",
            "http://localhost:1234",
            "http://127.0.0.1:0",
            "http://127.0.0.1:1234/",
            "http://127.0.0.1:1234/path",
            "http://u@127.0.0.1:1234",
            "http://127.0.0.1:1234?x=1",
        ] {
            assert!(
                RemoteProxy::new(store.clone(), origin, secret, stopped.clone()).is_err(),
                "accepted origin {origin}"
            );
        }
        assert!(RemoteProxy::new(store, "http://127.0.0.1:1234", secret, stopped).is_ok());
        for (method, path, accepted) in [
            ("GET", "sessions", true),
            ("PUT", "sessions/session_abcdefgh", true),
            ("DELETE", "sessions/session_abcdefgh", true),
            ("POST", "sessions/session_abcdefgh/messages", true),
            ("GET", "sessions/session_abcdefgh/messages?after=0", true),
            (
                "GET",
                "sessions/session_abcdefgh/events?after=0&generation=abcdefgh",
                true,
            ),
            ("POST", "sessions", false),
            ("GET", "sessions/session_abcdefgh", false),
            ("GET", "sessions/session_abcdefgh/events?user=11", false),
            (
                "GET",
                "sessions/session_abcdefgh/events?after=0&after=1",
                false,
            ),
            ("GET", "sessions/%2e%2e/events", false),
            ("GET", "sessions/session_abcdefgh/events/extra", false),
            ("POST", "sessions/session_abcdefgh/messages?after=0", false),
        ] {
            let request = Request::builder()
                .method(method)
                .uri(format!("{PREFIX}{path}"))
                .body(Body::empty())
                .unwrap();
            assert_eq!(operation(&request).is_some(), accepted, "{method} {path}");
        }
    }
}
