use lmm_core::{config, identity::IdentityStore};
use std::{env, error::Error, net::SocketAddr};

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("LMM_CORE_LISTEN")
        .unwrap_or_else(|_| "0.0.0.0:8080".to_owned())
        .parse()?;
    let database_url = config::database_url()?;
    let mut app = lmm_core::http::router();
    if let Some(url) = database_url.as_deref() {
        let store = IdentityStore::connect(url).await?;
        store.check_schema().await?;
        app = app.merge(lmm_core::identity_http::router(store));
        eprintln!("native identity is enabled; billing and model forwarding remain disabled");
    }
    let (stop, stopped) = tokio::sync::watch::channel(false);
    #[cfg(unix)]
    let rpc = match (
        env::var_os("LMM_CORE_RPC_SOCKET"),
        env::var_os("LMM_CORE_RPC_TOKEN_FILE"),
    ) {
        (None, None) => None,
        (Some(socket), Some(token_file)) => {
            let token =
                lmm_core::internal_rpc::read_service_token(std::path::Path::new(&token_file))?;
            let store = match database_url.as_deref() {
                Some(url) => Some(lmm_core::internal_rpc::readonly_store(url).await?),
                None => None,
            };
            Some(lmm_core::internal_rpc::RpcServer::bind(
                std::path::Path::new(&socket),
                &token,
                store,
            )?)
        }
        _ => return Err("configure both RPC socket and service credential file".into()),
    };
    #[cfg(not(unix))]
    if env::var_os("LMM_CORE_RPC_SOCKET").is_some()
        || env::var_os("LMM_CORE_RPC_TOKEN_FILE").is_some()
    {
        return Err("internal RPC requires a Unix socket on this release".into());
    }
    let listener = tokio::net::TcpListener::bind(address).await?;
    #[cfg(unix)]
    let rpc_task = rpc.map(|server| {
        let stopped = stopped.clone();
        tokio::spawn(async move {
            if server.serve(stopped).await.is_err() {
                eprintln!("internal RPC stopped; public core remains independent");
            }
        })
    });
    #[cfg(unix)]
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    let signal_stop = stop.clone();
    let signal = tokio::spawn(async move {
        #[cfg(unix)]
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {},
            _ = terminate.recv() => {},
        }
        #[cfg(not(unix))]
        let _ = tokio::signal::ctrl_c().await;
        let _ = signal_stop.send(true);
    });
    eprintln!(
        "lmm-core {} listening on {}; model traffic is disabled",
        env!("CARGO_PKG_VERSION"),
        address
    );
    let mut public_stop = stopped;
    // Deliberately no total timeout on a future model/SSE stream here.
    let result = axum::serve(listener, app)
        .with_graceful_shutdown(async move {
            if !*public_stop.borrow() {
                let _ = public_stop.changed().await;
            }
        })
        .await;
    let _ = stop.send(true);
    #[cfg(unix)]
    if let Some(mut task) = rpc_task
        && tokio::time::timeout(std::time::Duration::from_secs(5), &mut task)
            .await
            .is_err()
    {
        task.abort();
    }
    signal.abort();
    result?;
    Ok(())
}
