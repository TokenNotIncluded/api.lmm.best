#![cfg(unix)]
//! Actual native PostgreSQL -> Rust UDS gRPC -> Go HTTP/SSE -> Axum public relay.
//! Requires a new, owned lmm_remote_test_* database, never an existing fixture.
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use lmm_core::{
    accounts::{Account, AccountKind},
    funding::FundingPolicy,
    identity::IdentityStore,
    internal_rpc,
    remote_control::RemoteProxy,
};
use reqwest::{Client, StatusCode};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::PathBuf,
    process::{Child, Command},
    time::Duration,
};
use tokio::sync::watch;
use tokio_stream::StreamExt;

struct Bridge {
    child: Child,
    root: PathBuf,
}
impl Drop for Bridge {
    fn drop(&mut self) {
        let _ = fs::write(self.root.join("stop"), b"");
        // A panic must not leave the owned Go fixture alive.
        if self.child.try_wait().ok().flatten().is_none() {
            let _ = self.child.kill();
        }
        let _ = self.child.wait();
    }
}
fn private(path: &std::path::Path, data: &[u8]) {
    use std::io::Write;
    use std::os::unix::fs::OpenOptionsExt;
    let mut f = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(path)
        .unwrap();
    f.write_all(data).unwrap();
}
async fn event(
    stream: &mut (impl tokio_stream::Stream<Item = Result<axum::body::Bytes, reqwest::Error>> + Unpin),
    name: &str,
) -> String {
    let mut received = String::new();
    tokio::time::timeout(Duration::from_secs(2), async {
        while !received.contains("\n\n") {
            let chunk = stream.next().await.expect("SSE closed").unwrap();
            received.push_str(std::str::from_utf8(&chunk).unwrap());
        }
    })
    .await
    .expect("SSE buffered or stalled");
    assert!(
        received.contains(&format!("event: {name}\n")),
        "wrong event: {received}"
    );
    received
}

#[tokio::test]
#[ignore = "requires a new owned local PostgreSQL database and tracked Go caches"]
async fn native_public_relay_actual_http_sse_and_readonly_rpc() {
    let dsn =
        std::env::var("LMM_NATIVE_REMOTE_TEST_DATABASE_URL").expect("owned test database URL");
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&dsn)
        .await
        .unwrap();
    let database: String = sqlx::query_scalar("SELECT current_database()")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(
        database.starts_with("lmm_remote_test_"),
        "refuse non-test database"
    );
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    let alice = store.bootstrap_user(11, 6).await.unwrap();
    let bob = store.bootstrap_user(22, 1).await.unwrap();
    let short = store.bootstrap_user(33, 1).await.unwrap();
    let personal_key = store
        .issue_key(
            &alice.secret,
            Account {
                kind: AccountKind::Personal,
                id: 11,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let team = store.create_team(&alice.secret).await.unwrap();
    let group_key = store
        .issue_key(
            &alice.secret,
            Account {
                kind: AccountKind::Team,
                id: team,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    // Fresh role is SELECT-only, with database-enforced read-only transactions.
    sqlx::raw_sql("CREATE ROLE native_remote_reader NOLOGIN; GRANT USAGE ON SCHEMA core_meta,core_identity TO native_remote_reader; GRANT SELECT ON ALL TABLES IN SCHEMA core_meta,core_identity TO native_remote_reader;").execute(&pool).await.unwrap();
    let readonly = PgPoolOptions::new()
        .max_connections(2)
        .after_connect(|c, _| {
            Box::pin(async move {
                sqlx::query("SET ROLE native_remote_reader")
                    .execute(&mut *c)
                    .await?;
                sqlx::query("SET default_transaction_read_only=on")
                    .execute(&mut *c)
                    .await?;
                sqlx::query("SET statement_timeout='1500ms'")
                    .execute(&mut *c)
                    .await?;
                Ok(())
            })
        })
        .connect(&dsn)
        .await
        .unwrap();
    let runtime = IdentityStore::from_pool(readonly.clone());
    runtime.check_schema().await.unwrap();
    assert!(
        sqlx::query("UPDATE core_identity.users SET active=false WHERE id=11")
            .execute(&readonly)
            .await
            .is_err()
    );
    let root = PathBuf::from(
        std::env::var("LMM_NATIVE_REMOTE_FIXTURE_DIR")
            .expect("short private tracked fixture directory"),
    );
    fs::create_dir_all(&root).unwrap();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
    let rpc_token = b"native_relay_rpc_private_test_credential_1234";
    let host_token = b"native_relay_host_private_test_credential_1234";
    private(&root.join("rpc.token"), rpc_token);
    private(&root.join("host.token"), host_token);
    let (rpc_stop, rpc_stopped) = watch::channel(false);
    let server =
        internal_rpc::RpcServer::bind(&root.join("core.sock"), rpc_token, Some(runtime.clone()))
            .unwrap();
    let rpc = tokio::spawn(server.serve(rpc_stopped));
    let log = fs::File::create(root.join("go-bridge.log")).unwrap();
    let child = Command::new("go")
        .args([
            "test",
            "./internal/modules/remotecontrol",
            "-run",
            "^TestNativeRelayBridge$",
            "-count=1",
            "-v",
            "-timeout=100s",
        ])
        .current_dir(PathBuf::from(env!("CARGO_MANIFEST_DIR")).join("../extensions"))
        .stdout(log.try_clone().unwrap())
        .stderr(log)
        .spawn()
        .unwrap();
    let mut bridge = Bridge {
        child,
        root: root.clone(),
    };
    let origin = tokio::time::timeout(Duration::from_secs(30), async {
        loop {
            if let Ok(value) = fs::read_to_string(root.join("host.addr")) {
                break value;
            }
            assert!(
                bridge.child.try_wait().unwrap().is_none(),
                "Go bridge failed; preserve go-bridge.log"
            );
            tokio::time::sleep(Duration::from_millis(20)).await;
        }
    })
    .await
    .unwrap();
    let (stop, stopped) = watch::channel(false);
    let proxy = RemoteProxy::new(runtime, &origin, host_token, stopped.clone()).unwrap();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let public = format!("http://{}", listener.local_addr().unwrap());
    let lifecycle = lmm_core::lifecycle::Lifecycle::with_capacity(8);
    assert!(lifecycle.mark_ready());
    let app = proxy
        .clone()
        .router()
        .layer(axum::middleware::from_fn_with_state(
            lmm_core::lifecycle::Admission::new(lifecycle.clone(), None),
            lmm_core::lifecycle::admit,
        ))
        .layer(axum::middleware::from_fn_with_state(
            proxy,
            lmm_core::remote_control::preadmit,
        ));
    let public_task = tokio::spawn(async move {
        axum::serve(lmm_core::remote_control::public_listener(listener), app)
            .with_graceful_shutdown(async move {
                let mut stopped = stopped;
                let _ = stopped.changed().await;
            })
            .await
            .unwrap()
    });
    let client = Client::builder().no_proxy().build().unwrap();
    let sessions = format!("{public}/api/remote-control/v1/pi/sessions");
    let session = format!("{sessions}/session_abcdefgh");
    let cipher = json!({"nonce":URL_SAFE_NO_PAD.encode([1u8;12]),"ciphertext":URL_SAFE_NO_PAD.encode([2u8;16])});
    let heartbeat = json!({"device_id":"device_abcdefgh","metadata":cipher});
    // Authenticate before query/existence, reject transport-only/API key/group/OAuth.
    for token in [
        "",
        std::str::from_utf8(host_token).unwrap(),
        &personal_key.secret,
        &group_key.secret,
        "lmm_at_fake_but_well_formed_credential_12345678",
    ] {
        let r = client
            .get(format!("{session}/events?after=broken"))
            .bearer_auth(token)
            .send()
            .await
            .unwrap();
        assert!(matches!(
            r.status(),
            StatusCode::UNAUTHORIZED | StatusCode::FORBIDDEN
        ));
    }
    for header in [
        "x-lmm-user-credential",
        "x-lmm-user-id",
        "x-lmm-owner-account-id",
        "x-lmm-protocol",
    ] {
        assert_eq!(
            client
                .get(&sessions)
                .bearer_auth(&alice.secret)
                .header(header, &bob.secret)
                .send()
                .await
                .unwrap()
                .status(),
            StatusCode::UNAUTHORIZED
        );
    }
    assert_eq!(
        client
            .get(&sessions)
            .bearer_auth(&alice.secret)
            .header("authorization", format!("Bearer {}", bob.secret))
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        client
            .put(&session)
            .bearer_auth(&alice.secret)
            .json(&heartbeat)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );
    let listed: Value = client
        .get(&sessions)
        .bearer_auth(&alice.secret)
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(listed["data"]["sessions"].as_array().unwrap().len(), 1);
    let listed: Value = client
        .get(&sessions)
        .bearer_auth(&bob.secret)
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert!(listed["data"]["sessions"].as_array().unwrap().is_empty());
    assert_eq!(
        client
            .get(format!("{session}/messages?after=0"))
            .bearer_auth(&bob.secret)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::NOT_FOUND
    );
    let open = || {
        client
            .get(format!("{session}/events?after=0"))
            .bearer_auth(&alice.secret)
    };
    let response = open().send().await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["cache-control"], "no-store");
    assert_eq!(response.headers()["content-type"], "text/event-stream");
    assert!(!response.headers().contains_key("set-cookie"));
    let mut stream = response.bytes_stream();
    event(&mut stream, "ready").await;
    let second = open().send().await.unwrap();
    let mut second = second.bytes_stream();
    event(&mut second, "ready").await;
    let refused = open().send().await.unwrap();
    assert_eq!(refused.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(refused.headers()["retry-after"], "1");
    let mut held = Vec::new();
    while let Some(lease) = lifecycle.try_accept() {
        held.push(lease);
    }
    assert_eq!(
        client.get(&sessions).send().await.unwrap().status(),
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        client
            .get(&sessions)
            .bearer_auth(&bob.secret)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    drop(held);
    let message =
        json!({"sender":"controller","nonce":cipher["nonce"],"ciphertext":cipher["ciphertext"]});
    assert_eq!(
        client
            .post(format!("{session}/messages"))
            .bearer_auth(&alice.secret)
            .json(&message)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );
    assert!(
        event(&mut stream, "messages")
            .await
            .contains("\"sequence\":1")
    );
    drop(second);
    tokio::time::sleep(Duration::from_millis(100)).await;
    let replacement = open().send().await.unwrap();
    assert_eq!(replacement.status(), StatusCode::OK);
    drop(replacement);
    // Real revocation reaches the Go recheck before the next encrypted batch.
    store.revoke(&alice.secret, alice.id).await.unwrap();
    assert_eq!(
        client
            .post(format!("{session}/messages"))
            .bearer_auth(&alice.secret)
            .json(&message)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::UNAUTHORIZED
    );
    tokio::time::timeout(Duration::from_secs(15), async {
        while let Some(chunk) = stream.next().await {
            if let Ok(chunk) = chunk {
                assert!(
                    !std::str::from_utf8(&chunk)
                        .unwrap()
                        .contains("event: messages")
                );
            }
        }
    })
    .await
    .expect("revoked stream did not close within contract");
    drop(stream);
    // Credential expiry ends even an idle stream without a new message.
    let short_session = format!("{sessions}/short_session_abcdefgh");
    assert_eq!(
        client
            .put(&short_session)
            .bearer_auth(&short.secret)
            .json(&heartbeat)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()+interval '2 seconds' WHERE id=$1").bind(short.id).execute(&pool).await.unwrap();
    let expiry = client
        .get(format!("{short_session}/events"))
        .bearer_auth(&short.secret)
        .send()
        .await
        .unwrap();
    assert_eq!(expiry.status(), StatusCode::OK);
    let mut expiry = expiry.bytes_stream();
    event(&mut expiry, "ready").await;
    tokio::time::timeout(Duration::from_secs(4), async {
        while expiry.next().await.is_some() {}
    })
    .await
    .expect("idle expiry unbounded");
    // RPC outage is not identity startup or a successful empty list.
    rpc_stop.send(true).unwrap();
    rpc.await.unwrap().unwrap();
    assert_eq!(
        client
            .get(&sessions)
            .bearer_auth(&bob.secret)
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    stop.send(true).unwrap();
    public_task.await.unwrap();
    fs::write(root.join("stop"), b"").unwrap();
    let status = tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            if let Some(status) = bridge.child.try_wait().unwrap() {
                break status;
            }
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    assert!(status.success(), "Go bridge failed; see go-bridge.log");
    readonly.close().await;
    pool.close().await;
}
