//! Bounded public connections, with protocol/header deadlines rather than a
//! total stream lifetime or a connection-wide read-idle timeout.
//!
//! Adapter pattern adapted from the official MIT-licensed examples:
//! https://github.com/tokio-rs/axum/blob/c5a451d94ba063e026d3b450795abf39744bd1c5/examples/serve-with-hyper/src/main.rs
//! https://github.com/hyperium/hyper-util/blob/v0.1.21/examples/server_graceful.rs
//! Copyright (c) 2019 axum Contributors; Copyright (c) 2023-2025 Sean McArthur.
//! HTTP parsing remains Hyper-owned.
//!
//! Permission is hereby granted, free of charge, to any person obtaining a copy
//! of this software and associated documentation files (the "Software"), to deal
//! in the Software without restriction, including without limitation the rights
//! to use, copy, modify, merge, publish, distribute, sublicense, and/or sell
//! copies of the Software, and to permit persons to whom the Software is
//! furnished to do so, subject to the following conditions:
//!
//! The above copyright notice and this permission notice shall be included in
//! all copies or substantial portions of the Software.
//!
//! THE SOFTWARE IS PROVIDED "AS IS", WITHOUT WARRANTY OF ANY KIND, EXPRESS OR
//! IMPLIED, INCLUDING BUT NOT LIMITED TO THE WARRANTIES OF MERCHANTABILITY,
//! FITNESS FOR A PARTICULAR PURPOSE AND NONINFRINGEMENT. IN NO EVENT SHALL THE
//! AUTHORS OR COPYRIGHT HOLDERS BE LIABLE FOR ANY CLAIM, DAMAGES OR OTHER
//! LIABILITY, WHETHER IN AN ACTION OF CONTRACT, TORT OR OTHERWISE, ARISING FROM,
//! OUT OF OR IN CONNECTION WITH THE SOFTWARE OR THE USE OR OTHER DEALINGS IN
//! THE SOFTWARE.

use axum::{Router, body::Body, serve::Listener};
use hyper::{Request, body::Incoming, service::service_fn};
use hyper_util::{
    rt::{TokioExecutor, TokioIo, TokioTimer},
    server::{conn::auto::Builder, graceful::GracefulShutdown},
    service::TowerToHyperService,
};
use std::{
    future::Future,
    io,
    net::SocketAddr,
    pin::Pin,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    task::{Context, Poll},
    time::Duration,
};
use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    net::{TcpListener, TcpStream},
    sync::{OwnedSemaphorePermit, Semaphore, watch},
    time::{Instant, sleep_until},
};
use tower::{Service, ServiceExt};

/// Independent from the lifecycle's per-request admission and finalizer leases.
pub const MAX_PUBLIC_CONNECTIONS: usize = 64;
/// Absolute first-request deadline and Hyper HTTP/1 subsequent-header budget.
pub const PUBLIC_HEADER_TIMEOUT: Duration = Duration::from_secs(10);

// The permit follows the actual IO through HTTP upgrades. Keeping it only in
// the connection task would release capacity while the upgraded socket lives.
struct AdmittedIo {
    inner: crate::remote_control::PublicWriteIo<TcpStream>,
    first_parsed: Arc<AtomicBool>,
    abort_on_drop: Arc<AtomicBool>,
    _permit: OwnedSemaphorePermit,
}
impl Drop for AdmittedIo {
    fn drop(&mut self) {
        // Before the first parsed head, no business handler or healthy stream
        // exists. Abort these sockets on timeout, malformed input or shutdown
        // instead of leaving FIN-WAIT2 orphans outside the live permit bound.
        // A later native HTTP/1 header timeout also marks this transport for
        // abort. Healthy SSE/H2/upgrades and non-timeout errors keep normal FIN.
        static ABORT_FAILURE_REPORTED: AtomicBool = AtomicBool::new(false);
        if (!self.first_parsed.load(Ordering::Acquire)
            || self.abort_on_drop.load(Ordering::Acquire))
            && let Err(error) = self.inner.reject_unadmitted()
            && !ABORT_FAILURE_REPORTED.swap(true, Ordering::Relaxed)
        {
            // At most one payload-free diagnostic per process, not per peer.
            eprintln!(
                "core_header_abort_failed kind={:?} os_error={:?}",
                error.kind(),
                error.raw_os_error()
            );
        }
    }
}
impl AsyncRead for AdmittedIo {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_read(cx, buf)
    }
}
impl AsyncWrite for AdmittedIo {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        Pin::new(&mut self.inner).poll_write(cx, buf)
    }
    fn poll_write_vectored(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        bufs: &[io::IoSlice<'_>],
    ) -> Poll<io::Result<usize>> {
        Pin::new(&mut self.inner).poll_write_vectored(cx, bufs)
    }
    fn is_write_vectored(&self) -> bool {
        self.inner.is_write_vectored()
    }
    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_flush(cx)
    }
    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.inner).poll_shutdown(cx)
    }
}

/// Serve the existing Router without changing its request/stream admission.
/// At most MAX_PUBLIC_CONNECTIONS admitted socket IOs plus one transient accepted IO
/// exist here. Overflow is dropped before any task or HTTP dispatch is created.
// @mark runtime.connection-admission role=implementation
pub async fn serve<F>(listener: TcpListener, app: Router, shutdown: F) -> io::Result<()>
where
    F: Future<Output = ()> + Send + 'static,
{
    let mut listener = crate::remote_control::public_listener(listener);
    let slots = std::sync::Arc::new(Semaphore::new(MAX_PUBLIC_CONNECTIONS));
    let graceful = GracefulShutdown::new();
    let mut make_service = app.into_make_service_with_connect_info::<SocketAddr>();
    tokio::pin!(shutdown);
    loop {
        let (io, peer) = tokio::select! {
            biased;
            () = &mut shutdown => break,
            accepted = listener.accept() => accepted,
        };
        let accepted_at = Instant::now();
        let Ok(permit) = slots.clone().try_acquire_owned() else {
            // Reject only this unadmitted transport. Normal FIN would leave
            // kernel FIN-WAIT2 state outside the live connection permit bound.
            io.reject_unadmitted()?;
            drop(io);
            continue;
        };
        // Connected<SocketAddr> is provided by Axum: use the actual accepted
        // peer, never the listener address or an untrusted forwarding header.
        let tower_service = match make_service.call(peer).await {
            Ok(service) => service,
            Err(never) => match never {},
        };
        let tower_service =
            tower_service.map_request(|request: Request<Incoming>| request.map(Body::new));
        let hyper_service = TowerToHyperService::new(tower_service);
        let (parsed, mut first_request) = watch::channel(false);
        let first_parsed = Arc::new(AtomicBool::new(false));
        let notify_parsed = first_parsed.clone();
        let deadline_parsed = first_parsed.clone();
        let abort_on_drop = Arc::new(AtomicBool::new(false));
        let abort_failed = abort_on_drop.clone();
        let hyper_service = service_fn(move |request: Request<Incoming>| {
            // This callback runs only after Hyper parsed a valid request head.
            // Clear the initial deadline before running any business handler.
            notify_parsed.store(true, Ordering::Release);
            parsed.send_replace(true);
            hyper::service::Service::call(&hyper_service, request)
        });
        let mut builder = Builder::new(TokioExecutor::new());
        builder
            .http1()
            .timer(TokioTimer::new())
            .header_read_timeout(PUBLIC_HEADER_TIMEOUT);
        builder
            .http2()
            .timer(TokioTimer::new())
            .enable_connect_protocol();
        let connection = builder
            .serve_connection_with_upgrades(
                TokioIo::new(AdmittedIo {
                    inner: io,
                    first_parsed,
                    abort_on_drop,
                    _permit: permit,
                }),
                hyper_service,
            )
            .into_owned();
        let connection = graceful.watch(connection);
        tokio::spawn(async move {
            tokio::pin!(connection);
            let deadline = sleep_until(accepted_at + PUBLIC_HEADER_TIMEOUT);
            tokio::pin!(deadline);
            let mut awaiting_first = true;
            loop {
                tokio::select! {
                    result = &mut connection => {
                        // Locked Hyper 1.12.0 http1 Connection::poll (572-586)
                        // returns the error while retaining its IO; hyper-util
                        // 0.1.21 auto (699-705) and graceful wrappers retain that
                        // connection until Drop. Mark the IO before dropping it.
                        // Only the configured native HTTP/1 header timer uses
                        // Hyper timeouts here: no H2 keepalive/total timer is set.
                        if let Err(error) = result
                            && error.downcast_ref::<hyper::Error>().is_some_and(hyper::Error::is_timeout)
                        {
                            abort_failed.store(true,Ordering::Release);
                        }
                        // Avoid an attacker-controlled unbounded per-socket log.
                        break;
                    }
                    () = &mut deadline, if awaiting_first => {
                        // Recheck the state: a parsed request and timer can be
                        // ready in the same poll. Never kill an established SSE
                        // or HTTP/2 connection because its notification lost.
                        if !deadline_parsed.load(Ordering::Acquire) { break; }
                        awaiting_first = false;
                    }
                    changed = first_request.changed(), if awaiting_first => {
                        if changed.is_err() { break; }
                        if *first_request.borrow() { awaiting_first = false; }
                    }
                }
            }
        });
    }
    // Hyper's graceful watcher cancels protocol detection and disables further
    // requests while allowing active responses to drain. The caller retains
    // ownership of lifecycle cancellation and durable finalizer completion.
    drop(listener);
    graceful.shutdown().await;
    Ok(())
}
