//! Real Core identity + PostgreSQL + public Remote relay admission. The local
//! transport observer counts actual HTTP dispatches; it neither authorizes nor
//! executes Remote business operations. OAuth Remote and paid MCP execution
//! have no public execution entry in this slice and are not claimed covered.
use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Method, Request, StatusCode},
    response::IntoResponse,
};
use lmm_core::{
    core_schema,
    identity::{IdentityError, IdentityStore},
    remote_control::RemoteProxy,
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{
    sync::{Arc, Mutex},
    time::Duration,
};
use tokio::sync::watch;
use tower::ServiceExt;
#[path = "common/abuse_resources.rs"]
mod common;

#[sqlx::test(migrations = false)]
async fn marked_remote_new_actions_never_dispatch_but_history_cleanup_and_normal_control_do(
    pool: PgPool,
) {
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let user = identity.bootstrap_user(11, 1).await.unwrap();
    let normal = identity.bootstrap_user(22, 1).await.unwrap();
    let marker = common::marker(&pool, &identity).await;
    let dispatches = Arc::new(Mutex::new(Vec::<(Method, String)>::new()));
    let observed = dispatches.clone();
    let upstream = Router::new().fallback(move |request: Request<Body>| {
        let observed = observed.clone();
        async move {
            observed
                .lock()
                .unwrap()
                .push((request.method().clone(), request.uri().path().to_owned()));
            // A transport observation only, never a fake Remote execution receipt.
            ([("content-type", "application/json")], "{}").into_response()
        }
    });
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let origin = format!("http://{}", listener.local_addr().unwrap());
    let (shutdown, stopping) = tokio::sync::oneshot::channel::<()>();
    let server = tokio::spawn(async move {
        axum::serve(listener, upstream)
            .with_graceful_shutdown(async {
                let _ = stopping.await;
            })
            .await
            .unwrap()
    });
    let (_stop, stopped) = watch::channel(false);
    let proxy = RemoteProxy::new(
        identity.clone(),
        &origin,
        b"synthetic_private_remote_transport_token_12345",
        stopped,
    )
    .unwrap()
    .router();
    let request = |method: Method, path: &str, secret: &str| {
        Request::builder()
            .method(method)
            .uri(path)
            .header("authorization", format!("Bearer {secret}"))
            .body(Body::from("{}"))
            .unwrap()
    };
    assert_eq!(
        proxy
            .clone()
            .oneshot(request(
                Method::PUT,
                "/api/remote-control/v1/pi/sessions/session_test",
                &user.secret
            ))
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );
    assert_eq!(dispatches.lock().unwrap().len(), 1);
    marker.mark(11).await;
    let before = common::financial_snapshot(&pool).await;
    for (method, path) in [
        (
            Method::PUT,
            "/api/remote-control/v1/pi/sessions/session_test",
        ),
        (
            Method::POST,
            "/api/remote-control/v1/pi/sessions/session_test/messages",
        ),
    ] {
        let response = proxy
            .clone()
            .oneshot(request(method, path, &user.secret))
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::FORBIDDEN);
        let bytes = to_bytes(response.into_body(), 1024).await.unwrap();
        let body: Value = serde_json::from_slice(&bytes).unwrap();
        assert_eq!(body, json!({"success":false,"error":"forbidden"}));
        assert_eq!(
            dispatches.lock().unwrap().len(),
            1,
            "marked new action reached the transport"
        );
    }
    assert_eq!(common::financial_snapshot(&pool).await, before);
    for (method, path) in [
        (Method::GET, "/api/remote-control/v1/pi/sessions"),
        (
            Method::GET,
            "/api/remote-control/v1/pi/sessions/session_test/messages",
        ),
        (
            Method::DELETE,
            "/api/remote-control/v1/pi/sessions/session_test",
        ),
    ] {
        assert_eq!(
            proxy
                .clone()
                .oneshot(request(method, path, &user.secret))
                .await
                .unwrap()
                .status(),
            StatusCode::OK
        );
    }
    assert_eq!(dispatches.lock().unwrap().len(), 4);
    assert_eq!(
        proxy
            .oneshot(request(
                Method::POST,
                "/api/remote-control/v1/pi/sessions/normal_test/messages",
                &normal.secret
            ))
            .await
            .unwrap()
            .status(),
        StatusCode::OK
    );
    assert_eq!(dispatches.lock().unwrap().len(), 5);
    // The old RPC is still identity projection, not new-action authorization.
    assert_eq!(
        identity
            .authorize_remote_native(&user.secret)
            .await
            .unwrap()
            .user_id,
        11
    );
    assert!(matches!(
        identity.admit_new_remote_execution(&user.secret).await,
        Err(IdentityError::Forbidden)
    ));
    assert_eq!(identity.authorize(&user.secret).await.unwrap().user_id, 11);
    let _ = shutdown.send(());
    tokio::time::timeout(Duration::from_secs(3), server)
        .await
        .unwrap()
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn older_serializable_snapshot_cannot_admit_after_real_mark_commits(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let user = identity.bootstrap_user(11, 1).await.unwrap();
    let marker = common::marker(&pool, &identity).await;
    let commit = marker.preview(11, "resource-older-snapshot").await;
    let mut older = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *older)
        .await
        .unwrap();
    let active: bool = sqlx::query_scalar("SELECT active FROM core_identity.users WHERE id=11")
        .fetch_one(&mut *older)
        .await
        .unwrap();
    assert!(active);
    let mark = marker
        .abuse
        .commit_users(&marker.root_secret, &commit)
        .await
        .unwrap();
    assert_eq!(mark["users"][0]["marked"], true);
    // Mark must physically update the canonical user tuple without changing
    // auth_version. A lock alone lets a SERIALIZABLE older snapshot miss a new
    // mark after waiting, even though it acquires SHARE after the commit.
    let result = sqlx::query("SELECT id FROM core_identity.users WHERE id=11 FOR SHARE")
        .fetch_one(&mut *older)
        .await;
    let error = result.expect_err(
        "mark left an older serializable snapshot able to acquire new resource authority",
    );
    assert_eq!(
        error.as_database_error().and_then(|e| e.code()).as_deref(),
        Some("40001")
    );
    older.rollback().await.unwrap();
    let live = identity.authorize(&user.secret).await.unwrap();
    assert_eq!(live.user_id, 11);
    let version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=11")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(version, 1);
    assert!(matches!(
        identity.admit_new_remote_execution(&user.secret).await,
        Err(IdentityError::Forbidden)
    ));
}
