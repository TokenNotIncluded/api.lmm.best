use lmm_core::{
    config,
    identity::IdentityStore,
    lifecycle::{Admission, Lifecycle, ingress::IngressAcks},
};
use std::{
    env,
    error::Error,
    ffi::OsString,
    io,
    net::SocketAddr,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

mod admin;

const USAGE: &str = "Usage: lmm-core [serve | init-db | bootstrap-user]\n\n\
Commands:\n\
  serve           Start the core service (the default without arguments).\n\
  init-db         Initialize a new, empty core database.\n\
  bootstrap-user  Read a user_id and platform_level JSON object from stdin.\n\n\
Options:\n\
  -h, --help      Show this help without reading database configuration.\n\
  -V, --version   Show the core version.\n\n\
bootstrap-user accepts at most 4096 bytes and writes a one-time credential to\n\
stdout. Redirect that output to a private file; do not put secrets in arguments.";

enum Command {
    Serve,
    InitDb,
    BootstrapUser,
    Help,
    Version,
}

fn command(args: impl IntoIterator<Item = OsString>) -> io::Result<Command> {
    let args: Vec<OsString> = args.into_iter().collect();
    if args.is_empty() {
        return Ok(Command::Serve);
    }
    let first = args[0].to_str();
    let known_command = matches!(first, Some("serve" | "init-db" | "bootstrap-user"));
    if args.len() == 2 && known_command && matches!(args[1].to_str(), Some("-h" | "--help")) {
        return Ok(Command::Help);
    }
    if args.len() == 1 {
        match first {
            Some("serve") => return Ok(Command::Serve),
            Some("init-db") => return Ok(Command::InitDb),
            Some("bootstrap-user") => return Ok(Command::BootstrapUser),
            Some("-h" | "--help") => return Ok(Command::Help),
            Some("-V" | "--version") => return Ok(Command::Version),
            _ => {}
        }
    }
    // Do not echo arguments: an operator may accidentally pass a secret.
    Err(io::Error::new(io::ErrorKind::InvalidInput, USAGE))
}

fn main() -> Result<(), Box<dyn Error>> {
    let operation = match command(env::args_os().skip(1))? {
        Command::Help => {
            println!("{USAGE}");
            return Ok(());
        }
        Command::Version => {
            println!("lmm-core {}", env!("CARGO_PKG_VERSION"));
            return Ok(());
        }
        Command::Serve => None,
        Command::InitDb => Some(admin::Operation::InitDb),
        Command::BootstrapUser => Some(admin::Operation::bootstrap(io::stdin().lock())?),
    };
    // One worker and a bounded blocking pool are sufficient for the 1-core
    // profile. Password hashing remains on a separate bounded blocking worker.
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(config::worker_threads()?)
        .max_blocking_threads(4)
        .enable_all()
        .build()?;
    match operation {
        Some(operation) => runtime.block_on(admin::run(operation)),
        None => runtime.block_on(run_core()),
    }
}

async fn run_core() -> Result<(), Box<dyn Error>> {
    let address: SocketAddr = env::var("LMM_CORE_LISTEN")
        .unwrap_or_else(|_| "0.0.0.0:8080".to_owned())
        .parse()?;
    let database_url = config::database_url()?;
    let lifecycle = Lifecycle::with_capacity(config::max_inflight()?);
    let ingress = IngressAcks::from_env()?;
    let legacy_config = config::legacy_auth()?;
    let mut capabilities = lmm_core::http::Capabilities::default();
    let mut public_status = lmm_core::http::PublicStatus::default();
    let mut database_pool = None;
    let mut model_gateway = None;
    let mut app = lmm_core::http::router();
    if let Some(url) = database_url.as_deref() {
        let pool = config::database_pool(url).await?;
        lmm_core::runtime_database::check(
            &pool,
            lmm_core::runtime_database::RequiredCapabilities {
                models: env::var_os("LMM_CORE_MODEL_CONFIG_FILE").is_some(),
                events: env::var_os("LMM_CORE_RPC_SOCKET").is_some(),
                payments: false,
            },
        )
        .await?;
        // Installation belongs solely to the explicit init-db command. Even a
        // valid identity-only installation cannot stand in for the whole core.
        lmm_core::core_schema::check(&pool).await?;
        let store = IdentityStore::from_pool(pool.clone())
            .with_auth_from_env()?
            .with_password_slots(config::password_slots()?)?;
        capabilities.identity = true;
        model_gateway = config::models(pool.clone())?;
        if let Some(gateway) = &model_gateway {
            // Trusted recovery keeps indeterminate upstream holds reserved.
            // A failed recovery prevents listener binding/readiness.
            gateway.recover_expired(64).await?;
            capabilities.models = true;
        }
        // A Web deployment must not offer a second registration path that
        // bypasses the Web legal-consent contract.
        let native_store = if legacy_config.is_some() {
            store.clone().with_registration_enabled(false)
        } else {
            store.clone()
        };
        let mut routes = lmm_core::identity_http::router(native_store);
        if let Some(config) = legacy_config {
            public_status = lmm_core::http::PublicStatus {
                password_login_enabled: true,
                password_register_enabled: config.registration_enabled,
                user_agreement_enabled: config.user_agreement_enabled,
                privacy_policy_enabled: config.privacy_policy_enabled,
            };
            routes = routes.merge(lmm_core::legacy_auth::router(store, config.auth));
            capabilities.legacy_web_auth = true;
        }
        if let Some(gateway) = &model_gateway {
            routes = routes.merge(gateway.router());
        }
        // The final readiness router is assembled after the optional RPC checks.
        app = routes;
        database_pool = Some(pool);
    } else if legacy_config.is_some()
        || env::var_os("LMM_CORE_MODEL_CONFIG_FILE").is_some()
        || env::var_os("LMM_CORE_MODEL_LOCAL_HTTP").is_some()
    {
        return Err("Web authentication and model configuration require a core database".into());
    }
    let (stop, stopped) = tokio::sync::watch::channel(false);
    // RPC remains usable while accepted public requests and completions drain.
    #[cfg(unix)]
    let (rpc_stop, rpc_stopped) = tokio::sync::watch::channel(false);
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
            let mut server = lmm_core::internal_rpc::RpcServer::bind(
                std::path::Path::new(&socket),
                &token,
                store,
            )?;
            if let Some(url) = database_url.as_deref() {
                let events = lmm_core::events::EventStore::connect(url).await?;
                server = server.with_events(events, "extensions")?;
                capabilities.events = true;
            }
            Some(server)
        }
        _ => return Err("configure both RPC socket and service credential file".into()),
    };
    #[cfg(not(unix))]
    if env::var_os("LMM_CORE_RPC_SOCKET").is_some()
        || env::var_os("LMM_CORE_RPC_TOKEN_FILE").is_some()
    {
        return Err("internal RPC requires a Unix socket on this release".into());
    }
    let rpc_running = Arc::new(AtomicBool::new(false));
    if let Some(pool) = &database_pool {
        let rpc_status = capabilities.events.then(|| rpc_running.clone());
        app = lmm_core::http::configured_router_with_rpc(
            pool.clone(),
            capabilities,
            rpc_status,
            public_status,
        )
        .merge(app);
    }
    let listener = tokio::net::TcpListener::bind(address).await?;
    #[cfg(unix)]
    let rpc_task = rpc.map(|server| {
        let stopped = rpc_stopped;
        let rpc_running = rpc_running.clone();
        rpc_running.store(true, Ordering::Release);
        tokio::spawn(async move {
            if server.serve(stopped).await.is_err() {
                eprintln!("internal RPC stopped; public core remains independent");
            }
            rpc_running.store(false, Ordering::Release);
        })
    });
    #[cfg(unix)]
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())?;
    app = app.layer(axum::middleware::from_fn(
        lmm_core::http::request_body_deadline,
    ));
    app = app.layer(axum::middleware::from_fn_with_state(
        Admission::new(lifecycle.clone(), ingress),
        lmm_core::lifecycle::admit,
    ));
    // All enabled components and listener binds have succeeded. The health body
    // lists capabilities; model readiness has its own explicit endpoint.
    assert!(lifecycle.mark_ready());
    let signal_lifecycle = lifecycle.clone();
    let signal_stop = stop.clone();
    let signal_gateway = model_gateway.clone();
    let signal = tokio::spawn(async move {
        #[cfg(unix)]
        tokio::select! {
            _ = tokio::signal::ctrl_c() => {},
            _ = terminate.recv() => {},
        }
        #[cfg(not(unix))]
        let _ = tokio::signal::ctrl_c().await;
        signal_lifecycle.begin_drain();
        let _ = signal_stop.send(true);
        if let Some(gateway) = signal_gateway {
            // Stop admission and cancel an overlong upstream after the grace
            // period. Never abort an accepted financial finalizer.
            gateway.drain(Duration::from_secs(30)).await;
        }
    });
    eprintln!(
        "lmm-core {} listening on {}; identity={}, legacy_web_auth={}, synthetic_models={}, events={}, payments=false",
        env!("CARGO_PKG_VERSION"),
        address,
        capabilities.identity,
        capabilities.legacy_web_auth,
        capabilities.models,
        capabilities.events,
    );
    let mut public_stop = stopped;
    // Deliberately no total timeout on a future model/SSE stream here.
    let result = axum::serve(
        listener,
        app.into_make_service_with_connect_info::<SocketAddr>(),
    )
    .with_graceful_shutdown(async move {
        if !*public_stop.borrow() {
            let _ = public_stop.changed().await;
        }
    })
    .await;
    lifecycle.begin_drain();
    let _ = stop.send(true);
    if let Some(gateway) = &model_gateway {
        while gateway.drain(Duration::from_secs(5)).await.remaining != 0 {
            eprintln!("waiting for durable model finalizers");
        }
    }
    // HTTP EOF/cancellation is not proof that asynchronous completion is done.
    // Completion tasks must own a WorkLease and write their durable outcome.
    lifecycle.wait_idle().await;
    #[cfg(unix)]
    {
        let _ = rpc_stop.send(true);
        if let Some(task) = rpc_task {
            task.await?;
        }
    }
    if let Some(pool) = database_pool {
        pool.close().await;
    }
    signal.abort();
    result?;
    Ok(())
}
