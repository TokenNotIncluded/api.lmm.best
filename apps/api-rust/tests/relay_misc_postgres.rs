//! Router composition contract for the PostgreSQL relay-misc executor.

use std::{sync::Arc, time::Duration};

use axum::{
    body::Body,
    http::{Request, StatusCode},
};
use lmm_api_rs::{
    models::PgModelsService,
    routes::relay_misc_postgres::{PgRelayMiscService, relay_misc_postgres_router},
};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn relay_misc_postgres_router_mounts_without_contacting_dependencies() {
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_millis(10))
        .connect_lazy("postgresql://unused:unused@127.0.0.1:1/unused")
        .expect("lazy PostgreSQL pool");
    let models = Arc::new(PgModelsService::new(pool.clone()));
    let client = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .expect("bounded relay client");
    let app = relay_misc_postgres_router(PgRelayMiscService::new(
        pool,
        models,
        client,
        Duration::from_secs(1),
    ));

    let response = app
        .oneshot(
            Request::builder()
                .method("GET")
                .uri("/v1/embeddings")
                .body(Body::empty())
                .expect("wrong-method relay request"),
        )
        .await
        .expect("router response");
    assert_eq!(response.status(), StatusCode::METHOD_NOT_ALLOWED);
}
