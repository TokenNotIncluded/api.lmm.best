use std::{env, error::Error, net::SocketAddr};

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("LMM_CORE_LISTEN")
        .unwrap_or_else(|_| "0.0.0.0:8080".to_owned())
        .parse()?;
    #[cfg(unix)]
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    let listener = tokio::net::TcpListener::bind(address).await?;
    eprintln!(
        "lmm-core {} listening on {}; business traffic is disabled",
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
    // No total response timeout: a future SSE body must be drained, not cut off
    // when an unrelated Go extension is updated. Docker supplies the stop limit.
    axum::serve(listener, lmm_core::http::router())
        .with_graceful_shutdown(shutdown)
        .await?;
    Ok(())
}
