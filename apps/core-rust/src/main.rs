use lmm_core::{config, identity::IdentityStore};
use std::{env, error::Error, net::SocketAddr};

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("LMM_CORE_LISTEN")
        .unwrap_or_else(|_| "0.0.0.0:8080".to_owned())
        .parse()?;
    let mut app = lmm_core::http::router();
    if let Some(url) = config::database_url()? {
        let store = IdentityStore::connect(&url).await?;
        store.check_schema().await?;
        app = app.merge(lmm_core::identity_http::router(store));
        eprintln!("native identity is enabled; billing and model forwarding remain disabled");
    }
    #[cfg(unix)]
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    let listener = tokio::net::TcpListener::bind(address).await?;
    eprintln!(
        "lmm-core {} listening on {}; model traffic is disabled",
        env!("CARGO_PKG_VERSION"),
        address
    );
    let shutdown = async move {
        #[cfg(unix)]
        tokio::select! {
            result = tokio::signal::ctrl_c() => { if let Err(error) = result { eprintln!("signal listener failed: {error}"); } },
            _ = terminate.recv() => {},
        }
        #[cfg(not(unix))]
        if let Err(error) = tokio::signal::ctrl_c().await {
            eprintln!("signal listener failed: {error}");
        }
    };
    // Do not impose a total SSE timeout when an unrelated extension is updated.
    axum::serve(listener, app)
        .with_graceful_shutdown(shutdown)
        .await?;
    Ok(())
}
