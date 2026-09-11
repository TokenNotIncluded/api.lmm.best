use std::{convert::Infallible, time::Duration};

use axum::{
    Router,
    body::{Body, Bytes},
    http::{Method, StatusCode},
    routing::get,
};
use futures_util::stream;
use lmm_api_rs::relay_http::{RelayHttpClient, RelayHttpError, RelayTimeoutConfig};
use tokio::{net::TcpListener, task::JoinHandle};

struct Server(JoinHandle<()>);

impl Drop for Server {
    fn drop(&mut self) {
        self.0.abort();
    }
}

async fn provider() -> (reqwest::Url, Server) {
    let app = Router::new().route(
        "/",
        get(|| async {
            Body::from_stream(stream::unfold(0, |index| async move {
                if index == 20 {
                    return None;
                }
                if index > 0 {
                    tokio::time::sleep(Duration::from_millis(50)).await;
                }
                Some((Ok::<_, Infallible>(Bytes::from_static(b":")), index + 1))
            }))
        }),
    );
    serve(app).await
}

async fn serve(app: Router) -> (reqwest::Url, Server) {
    let listener = TcpListener::bind("127.0.0.1:0").await.unwrap();
    let url = format!("http://{}/", listener.local_addr().unwrap())
        .parse()
        .unwrap();
    let server = Server(tokio::spawn(async move {
        axum::serve(listener, app).await.unwrap();
    }));
    (url, server)
}

#[tokio::test]
async fn headers_without_any_body_bytes_reach_the_idle_deadline() {
    tokio::time::timeout(Duration::from_secs(10), async {
        let app = Router::new().route(
            "/",
            get(|| async { Body::from_stream(stream::pending::<Result<Bytes, Infallible>>()) }),
        );
        let (url, _server) = serve(app).await;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_millis(100),
            total: None,
        })
        .unwrap();
        let mut response = client.send(client.request(Method::GET, url)).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert!(matches!(response.chunk().await, Err(RelayHttpError::Idle)));
        assert!(response.chunk().await.unwrap().is_none());
    })
    .await
    .expect("outer deadline");
}

#[tokio::test]
async fn consumer_pause_does_not_restart_total_deadline() {
    tokio::time::timeout(Duration::from_secs(10), async {
        let (url, _server) = provider().await;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_secs(2),
            total: Some(Duration::from_millis(400)),
        })
        .unwrap();
        let mut response = client.send(client.request(Method::GET, url)).await.unwrap();
        assert!(response.chunk().await.unwrap().is_some());
        tokio::time::sleep(Duration::from_millis(700)).await;
        match response.chunk().await {
            Err(RelayHttpError::Transport(error)) => assert!(error.is_timeout()),
            other => panic!("expected elapsed total deadline, got {other:?}"),
        }
    })
    .await
    .expect("outer deadline");
}

#[tokio::test]
async fn consumer_pause_is_not_a_downstream_write_deadline() {
    tokio::time::timeout(Duration::from_secs(10), async {
        let (url, _server) = provider().await;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_millis(400),
            total: None,
        })
        .unwrap();
        let mut response = client.send(client.request(Method::GET, url)).await.unwrap();
        let mut bytes = response.chunk().await.unwrap().unwrap().to_vec();
        tokio::time::sleep(Duration::from_millis(700)).await;
        while let Some(chunk) = response.chunk().await.unwrap() {
            bytes.extend_from_slice(&chunk);
        }
        assert_eq!(bytes, vec![b':'; 20]);
    })
    .await
    .expect("outer deadline");
}

#[tokio::test]
async fn total_deadline_truncates_an_active_byte_stream() {
    tokio::time::timeout(Duration::from_secs(10), async {
        let (url, _server) = provider().await;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_secs(2),
            total: Some(Duration::from_millis(400)),
        })
        .unwrap();
        let mut response = client.send(client.request(Method::GET, url)).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        let mut bytes = 0;
        loop {
            match response.chunk().await {
                Ok(Some(chunk)) => bytes += chunk.len(),
                Err(RelayHttpError::Transport(error)) => {
                    assert!(error.is_timeout());
                    break;
                }
                other => panic!("expected total timeout, got {other:?}"),
            }
        }
        assert!(bytes > 0 && bytes < 20);
        assert!(response.chunk().await.unwrap().is_none());
    })
    .await
    .expect("outer deadline");
}

#[tokio::test]
async fn disabled_total_deadline_allows_an_active_stream_to_finish() {
    tokio::time::timeout(Duration::from_secs(10), async {
        let (url, _server) = provider().await;
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(2)),
            idle: Duration::from_millis(400),
            total: None,
        })
        .unwrap();
        let mut response = client.send(client.request(Method::GET, url)).await.unwrap();
        let mut bytes = Vec::new();
        while let Some(chunk) = response.chunk().await.unwrap() {
            bytes.extend_from_slice(&chunk);
        }
        assert_eq!(bytes, vec![b':'; 20]);
    })
    .await
    .expect("outer deadline");
}
