//! Loopback-only listener used by the relay-misc Go/Rust behavior oracle.
//!
//! This is an explicit test harness, not a production entry point. It refuses
//! non-loopback listeners and databases and starts only when the caller sets
//! `LMM_RELAY_MISC_HARNESS_ALLOW=1`.

use std::{
    env,
    error::Error,
    io,
    net::{IpAddr, SocketAddr},
    sync::Arc,
    time::Duration,
};

use axum::{
    Router,
    extract::Request,
    http::StatusCode,
    middleware::{self, Next},
    response::Response,
    routing::get,
};
use lmm_api_rs::{
    RequestContext,
    models::PgModelsService,
    routes::{
        relay_misc::RelayMiscHttpState, relay_misc_active::router as active_router,
        relay_misc_frozen::router as frozen_router, relay_misc_postgres::PgRelayMiscService,
    },
};
use sqlx::postgres::PgPoolOptions;
use tokio::net::TcpListener;
use uuid::Uuid;

fn invalid_input(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

async fn request_context(mut request: Request, next: Next) -> Response {
    let request_id = Uuid::new_v4().to_string();
    request.extensions_mut().insert(RequestContext {
        request_id: request_id.clone(),
        client_ip: Some(IpAddr::V4(std::net::Ipv4Addr::LOCALHOST)),
    });
    next.run(request).await
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    if env::var("LMM_RELAY_MISC_HARNESS_ALLOW").as_deref() != Ok("1") {
        return Err(invalid_input("LMM_RELAY_MISC_HARNESS_ALLOW=1 is required").into());
    }

    let listen = env::var("LMM_RELAY_MISC_HARNESS_LISTEN")
        .map_err(|_| invalid_input("LMM_RELAY_MISC_HARNESS_LISTEN is required"))?
        .parse::<SocketAddr>()?;
    if !listen.ip().is_loopback() {
        return Err(invalid_input("relay-misc harness listener must be loopback-only").into());
    }

    let database_url = env::var("LMM_RELAY_MISC_HARNESS_DATABASE_URL")
        .map_err(|_| invalid_input("LMM_RELAY_MISC_HARNESS_DATABASE_URL is required"))?;
    let parsed_database = reqwest::Url::parse(&database_url)?;
    let database_host = parsed_database
        .host_str()
        .and_then(|host| host.parse::<IpAddr>().ok())
        .ok_or_else(|| invalid_input("relay-misc harness database host must be an IP address"))?;
    if !database_host.is_loopback() {
        return Err(invalid_input("relay-misc harness database must be loopback-only").into());
    }

    let pool = PgPoolOptions::new()
        .max_connections(8)
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await?;
    let models = Arc::new(PgModelsService::new(pool.clone()));
    let outbound = reqwest::Client::builder()
        .redirect(reqwest::redirect::Policy::none())
        .build()?;
    let mut service = PgRelayMiscService::new(pool, models, outbound, Duration::from_secs(5));
    if let Ok(valkey_url) = env::var("LMM_RELAY_MISC_HARNESS_VALKEY_URL") {
        let parsed_valkey = reqwest::Url::parse(&valkey_url)?;
        if parsed_valkey.scheme() != "redis" {
            return Err(invalid_input("relay-misc harness Valkey URL must use redis://").into());
        }
        let valkey_host = parsed_valkey
            .host_str()
            .and_then(|host| host.parse::<IpAddr>().ok())
            .ok_or_else(|| invalid_input("relay-misc harness Valkey host must be an IP address"))?;
        if !valkey_host.is_loopback() {
            return Err(invalid_input("relay-misc harness Valkey must be loopback-only").into());
        }
        service = service.with_model_rate_limit_valkey(
            redis::Client::open(valkey_url.as_str())?,
            Duration::from_secs(3),
        );
    }
    let state = RelayMiscHttpState::new(Arc::new(service));
    let app = active_router(state.clone())
        .merge(frozen_router(state))
        .merge(Router::new().route("/readyz", get(|| async { StatusCode::NO_CONTENT })))
        .layer(middleware::from_fn(request_context));

    let listener = TcpListener::bind(listen).await?;
    axum::serve(listener, app).await?;
    Ok(())
}
