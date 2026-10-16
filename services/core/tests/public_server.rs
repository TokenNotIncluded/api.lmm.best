//! Independent public transport checks. Local Router tests prove protocol
//! semantics; the opt-in OS gate below proves real Core admission and funds.
use axum::{
    Router,
    body::{Body, Bytes},
    extract::{ConnectInfo, Request},
    http::{StatusCode, header},
    response::IntoResponse,
    routing::{get, post},
};
use lmm_core::public_server::{MAX_PUBLIC_CONNECTIONS, PUBLIC_HEADER_TIMEOUT, serve};
use std::{
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
    time::Duration,
};
use tokio::{
    io::{AsyncReadExt, AsyncWriteExt},
    net::{TcpListener, TcpStream},
    sync::oneshot,
    task::JoinHandle,
};

struct Server {
    address: SocketAddr,
    stop: Option<oneshot::Sender<()>>,
    task: Option<JoinHandle<std::io::Result<()>>>,
}
impl Server {
    async fn start(app: Router) -> Self {
        let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        let (stop, rx) = oneshot::channel();
        let task = tokio::spawn(serve(listener, app, async {
            let _ = rx.await;
        }));
        Self {
            address,
            stop: Some(stop),
            task: Some(task),
        }
    }
    fn url(&self, path: &str) -> String {
        format!("http://{}{path}", self.address)
    }
    async fn stop(mut self) {
        let _ = self.stop.take().unwrap().send(());
        tokio::time::timeout(Duration::from_secs(3), self.task.take().unwrap())
            .await
            .expect("server graceful join exceeded3s")
            .unwrap()
            .unwrap();
    }
}
impl Drop for Server {
    fn drop(&mut self) {
        if let Some(tx) = self.stop.take() {
            let _ = tx.send(());
        }
        if let Some(task) = self.task.take() {
            task.abort();
        }
    }
}
fn client(h2: bool) -> reqwest::Client {
    let builder = reqwest::Client::builder()
        .no_proxy()
        .timeout(PUBLIC_HEADER_TIMEOUT + Duration::from_secs(8));
    if h2 {
        builder.http2_prior_knowledge().build().unwrap()
    } else {
        builder.http1_only().build().unwrap()
    }
}
async fn head(stream: &mut TcpStream) -> String {
    tokio::time::timeout(Duration::from_secs(2), async {
        let mut bytes = Vec::new();
        while !bytes.ends_with(b"\r\n\r\n") {
            bytes.push(stream.read_u8().await.unwrap());
            assert!(bytes.len() < 16384);
        }
        String::from_utf8(bytes).unwrap()
    })
    .await
    .unwrap()
}
async fn response(stream: &mut TcpStream) -> (String, Vec<u8>) {
    let header = head(stream).await;
    let size = header
        .lines()
        .find_map(|x| {
            x.split_once(':')
                .filter(|(k, _)| k.eq_ignore_ascii_case("content-length"))
                .map(|(_, v)| v.trim().parse::<usize>().unwrap())
        })
        .unwrap();
    assert!(size < 16384);
    let mut body = vec![0; size];
    stream.read_exact(&mut body).await.unwrap();
    (header, body)
}
async fn ended(stream: &mut TcpStream) {
    let result = tokio::time::timeout(Duration::from_secs(2), async {
        let mut bytes = 0;
        loop {
            let mut buf = [0; 1024];
            match stream.read(&mut buf).await {
                Ok(0) => break,
                Ok(n) => {
                    bytes += n;
                    assert!(bytes <= 16384, "closure diagnostic limit");
                }
                Err(e)
                    if matches!(
                        e.kind(),
                        std::io::ErrorKind::ConnectionReset | std::io::ErrorKind::BrokenPipe
                    ) =>
                {
                    break;
                }
                Err(e) => panic!("unexpected close error: {e}"),
            }
        }
    })
    .await;
    assert!(result.is_ok(), "connection survived its bound");
}
fn socket_observation(server: SocketAddr, peer: SocketAddr) -> String {
    let filter = format!(
        "( src {} and sport = :{} and dst {} and dport = :{} )",
        server.ip(),
        server.port(),
        peer.ip(),
        peer.port()
    );
    let out = std::process::Command::new("/usr/bin/timeout")
        .args(["2s", "/usr/bin/ss", "-atienH", &filter])
        .output()
        .unwrap();
    assert!(out.status.success() && out.stdout.len() < 16384);
    String::from_utf8(out.stdout).unwrap()
}
fn socket_inode(server: SocketAddr, peer: SocketAddr) -> u64 {
    let text = socket_observation(server, peer);
    text.split_whitespace()
        .find_map(|x| x.strip_prefix("ino:"))
        .expect("missing exact live TCP inode")
        .parse()
        .unwrap()
}

// @mark runtime.connection-admission role=test
#[tokio::test]
async fn initial_silent_partial_and_dripping_heads_have_absolute_deadline() {
    assert_eq!(MAX_PUBLIC_CONNECTIONS, 64);
    assert_eq!(PUBLIC_HEADER_TIMEOUT, Duration::from_secs(10));
    let calls = Arc::new(AtomicUsize::new(0));
    let counter = calls.clone();
    let s = Server::start(Router::new().route(
        "/ok",
        get(move || {
            let counter = counter.clone();
            async move {
                counter.fetch_add(1, Ordering::SeqCst);
                "ok"
            }
        }),
    ))
    .await;
    let mut silent = TcpStream::connect(s.address).await.unwrap();
    let mut partial = TcpStream::connect(s.address).await.unwrap();
    partial
        .write_all(b"GET /ok HTTP/1.1\r\nHost: test\r\nX-Partial:")
        .await
        .unwrap();
    let mut prefix = TcpStream::connect(s.address).await.unwrap();
    prefix.write_all(b"PRI * HTTP/2.0\r\n").await.unwrap();
    let mut drip = TcpStream::connect(s.address).await.unwrap();
    drip.write_all(b"GET /ok HTTP/1.1\r\nHost: test\r\nX-Drip:")
        .await
        .unwrap();
    let started = tokio::time::Instant::now();
    for _ in 0..24 {
        if drip.write_all(b"a").await.is_err() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    tokio::time::sleep_until(started + PUBLIC_HEADER_TIMEOUT + Duration::from_secs(2)).await;
    ended(&mut silent).await;
    ended(&mut partial).await;
    ended(&mut prefix).await;
    ended(&mut drip).await;
    assert_eq!(calls.load(Ordering::SeqCst), 0);
    assert_eq!(
        client(false)
            .get(s.url("/ok"))
            .send()
            .await
            .unwrap()
            .text()
            .await
            .unwrap(),
        "ok"
    );
    s.stop().await;
}

#[tokio::test]
async fn complete_heads_allow_slow_body_long_sse_and_same_idle_h2() {
    let delay = PUBLIC_HEADER_TIMEOUT + Duration::from_secs(2);
    let app = Router::new()
        .route(
            "/peer",
            get(|ConnectInfo(peer): ConnectInfo<SocketAddr>| async move { peer.to_string() }),
        )
        .route("/echo", post(|bytes: Bytes| async move { bytes }))
        .route(
            "/sse",
            get(move || async move {
                let (tx, rx) = tokio::sync::mpsc::channel::<Result<Bytes, std::io::Error>>(2);
                tokio::spawn(async move {
                    tx.send(Ok(Bytes::from_static(b"data: one\n\n")))
                        .await
                        .unwrap();
                    tokio::time::sleep(delay).await;
                    let _ = tx
                        .send(Ok(Bytes::from_static(b"data: two\n\ndata: [DONE]\n\n")))
                        .await;
                });
                (
                    [(header::CONTENT_TYPE, "text/event-stream")],
                    Body::from_stream(tokio_stream::wrappers::ReceiverStream::new(rx)),
                )
            }),
        );
    let s = Server::start(app).await;
    let h2 = client(true);
    let peer: SocketAddr = h2
        .get(s.url("/peer"))
        .send()
        .await
        .unwrap()
        .text()
        .await
        .unwrap()
        .parse()
        .unwrap();
    let inode = socket_inode(s.address, peer);
    let mut slow = TcpStream::connect(s.address).await.unwrap();
    slow.write_all(b"POST /echo HTTP/1.1\r\nHost: test\r\nContent-Length: 13\r\n\r\ncomplete-")
        .await
        .unwrap();
    let stream = client(false).get(s.url("/sse")).send().await.unwrap();
    assert_eq!(stream.status(), StatusCode::OK);
    tokio::time::sleep(delay).await;
    assert_eq!(
        socket_inode(s.address, peer),
        inode,
        "idle established H2 was replaced"
    );
    slow.write_all(b"body").await.unwrap();
    let (head, body) = response(&mut slow).await;
    assert!(head.starts_with("HTTP/1.1 200 "));
    assert_eq!(body, b"complete-body");
    assert_eq!(
        stream.bytes().await.unwrap(),
        b"data: one\n\ndata: two\n\ndata: [DONE]\n\n"[..]
    );
    let peer_after: SocketAddr = h2
        .get(s.url("/peer"))
        .send()
        .await
        .unwrap()
        .text()
        .await
        .unwrap()
        .parse()
        .unwrap();
    assert_eq!(peer_after, peer);
    assert_eq!(socket_inode(s.address, peer), inode);
    drop(slow);
    drop(h2);
    s.stop().await;
}

#[tokio::test]
async fn subsequent_http1_headers_cannot_extend_deadline_by_dripping() {
    let s = Server::start(Router::new().route("/ok", get(|| async { "ok" }))).await;
    let mut stream = TcpStream::connect(s.address).await.unwrap();
    let peer = stream.local_addr().unwrap();
    stream
        .write_all(b"GET /ok HTTP/1.1\r\nHost: test\r\n\r\n")
        .await
        .unwrap();
    let (_, body) = response(&mut stream).await;
    assert_eq!(body, b"ok");
    stream
        .write_all(b"GET /ok HTTP/1.1\r\nHost: test\r\nX-Drip:")
        .await
        .unwrap();
    let start = tokio::time::Instant::now();
    for _ in 0..24 {
        if stream.write_all(b"a").await.is_err() {
            break;
        }
        tokio::time::sleep(Duration::from_millis(500)).await;
    }
    tokio::time::sleep_until(start + PUBLIC_HEADER_TIMEOUT + Duration::from_secs(2)).await;
    ended(&mut stream).await;
    assert!(
        socket_observation(s.address, peer).trim().is_empty(),
        "native H1 header timeout retained kernel socket"
    );
    s.stop().await;
}

async fn upgrade(mut request: Request) -> impl IntoResponse {
    let upgrade = hyper::upgrade::on(&mut request);
    tokio::spawn(async move {
        if let Ok(io) = upgrade.await {
            let io = hyper_util::rt::TokioIo::new(io);
            let (mut read, mut write) = tokio::io::split(io);
            let _ = tokio::io::copy(&mut read, &mut write).await;
        }
    });
    (
        StatusCode::SWITCHING_PROTOCOLS,
        [
            (header::CONNECTION, "upgrade"),
            (header::UPGRADE, "test-echo"),
        ],
    )
}
#[tokio::test]
async fn upgraded_sockets_hold_capacity_until_actual_io_drop() {
    let s = Server::start(
        Router::new()
            .route("/upgrade", get(upgrade))
            .route("/ok", get(|| async { "ok" })),
    )
    .await;
    let mut upgraded = Vec::new();
    for _ in 0..MAX_PUBLIC_CONNECTIONS {
        let mut stream = TcpStream::connect(s.address).await.unwrap();
        stream.write_all(b"GET /upgrade HTTP/1.1\r\nHost: test\r\nConnection: upgrade\r\nUpgrade: test-echo\r\n\r\n").await.unwrap();
        assert!(head(&mut stream).await.starts_with("HTTP/1.1 101 "));
        stream.write_all(b"ping").await.unwrap();
        let mut echo = [0; 4];
        stream.read_exact(&mut echo).await.unwrap();
        assert_eq!(&echo, b"ping");
        upgraded.push(stream);
    }
    let mut overflow = TcpStream::connect(s.address).await.unwrap();
    ended(&mut overflow).await;
    drop(upgraded.pop());
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert_eq!(
        client(false)
            .get(s.url("/ok"))
            .send()
            .await
            .unwrap()
            .text()
            .await
            .unwrap(),
        "ok"
    );
    drop(upgraded);
    s.stop().await;
}
#[tokio::test]
async fn shutdown_cancels_unparsed_peers_without_waiting_header_deadline() {
    let s = Server::start(Router::new()).await;
    let _silent = TcpStream::connect(s.address).await.unwrap();
    let mut prefix = TcpStream::connect(s.address).await.unwrap();
    prefix.write_all(b"PRI * ").await.unwrap();
    tokio::time::sleep(Duration::from_millis(100)).await;
    let start = tokio::time::Instant::now();
    s.stop().await;
    assert!(start.elapsed() < Duration::from_secs(3));
    ended(&mut prefix).await;
}

#[tokio::test]
async fn graceful_shutdown_drains_an_already_parsed_response() {
    let gate = Arc::new(tokio::sync::Notify::new());
    let signal = gate.clone();
    let app = Router::new().route(
        "/drain",
        get(move || {
            let gate = gate.clone();
            async move {
                let (tx, rx) = tokio::sync::mpsc::channel::<Result<Bytes, std::io::Error>>(2);
                tokio::spawn(async move {
                    tx.send(Ok(Bytes::from_static(b"begin"))).await.unwrap();
                    gate.notified().await;
                    tx.send(Ok(Bytes::from_static(b"end"))).await.unwrap();
                });
                Body::from_stream(tokio_stream::wrappers::ReceiverStream::new(rx))
            }
        }),
    );
    let mut s = Server::start(app).await;
    let response = client(false).get(s.url("/drain")).send().await.unwrap();
    let _ = s.stop.take().unwrap().send(());
    tokio::time::sleep(Duration::from_millis(100)).await;
    assert!(
        !s.task.as_ref().unwrap().is_finished(),
        "active response was discarded during graceful drain"
    );
    signal.notify_one();
    assert_eq!(response.bytes().await.unwrap(), b"beginend"[..]);
    tokio::time::timeout(Duration::from_secs(3), s.task.take().unwrap())
        .await
        .unwrap()
        .unwrap()
        .unwrap();
}
