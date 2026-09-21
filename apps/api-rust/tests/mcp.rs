use axum::{body::Body, http::Request};
use lmm_api_rs::routes::mcp::{McpHttpState, mcp_router};
use sqlx::postgres::PgPoolOptions;
use tower::ServiceExt;

#[tokio::test]
async fn mcp_router_requires_bearer_token() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://route-test:route-test@127.0.0.1:1/route_test")
        .expect("lazy PostgreSQL pool");
    let valkey = redis::Client::open("redis://127.0.0.1:1").expect("lazy Valkey client");
    let app = mcp_router(McpHttpState::new(
        pg,
        valkey,
        std::time::Duration::from_secs(1),
    ));

    let response = app
        .oneshot(
            Request::post("/mcp")
                .body(Body::empty())
                .expect("route request"),
        )
        .await
        .expect("route response");

    assert_eq!(response.status(), axum::http::StatusCode::UNAUTHORIZED);
}
