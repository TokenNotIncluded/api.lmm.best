use lmm_core::{
    config,
    identity::IdentityStore,
    lifecycle::{Admission, Lifecycle},
};
use std::{
    env,
    error::Error,
    ffi::OsString,
    io,
    net::SocketAddr,
    path::PathBuf,
    sync::{
        Arc,
        atomic::{AtomicBool, Ordering},
    },
    time::Duration,
};

mod admin;

const USAGE: &str = "Usage: lmm-core [--config /absolute/core.toml | --legacy-env] [serve | init-db | bootstrap-user | complete-setup]\n\n\
Commands:\n\
  serve           Start the core service (the default without arguments).\n\
  init-db         Initialize a new, empty core database.\n\
  bootstrap-user  Read a user_id and platform_level JSON object from stdin.\n\n\
  complete-setup  Record setup completion offline after creating or importing root.\n\n\
Options:\n\
  --config PATH  Read one absolute TOML file (default /etc/lmm.conf.d/core.toml).\n\
  --legacy-env   Explicit temporary environment compatibility; conflicts with --config.\n\
  -h, --help      Show this help without reading database configuration.\n\
  -V, --version   Show the core version.\n\n\
bootstrap-user accepts at most 4096 bytes and writes a one-time credential to\n\
stdout. Redirect that output to a private file; do not put secrets in arguments.";

enum Command {
    Serve,
    InitDb,
    BootstrapUser,
    CompleteSetup,
    Help,
    Version,
}

struct Arguments {
    command: Command,
    config: Option<PathBuf>,
    legacy_env: bool,
}

fn command(args: impl IntoIterator<Item = OsString>) -> io::Result<Arguments> {
    let mut args = args.into_iter();
    let mut operation = None;
    let mut config = None;
    let mut legacy_env = false;
    let mut help = false;
    let mut version = false;
    let bad = || io::Error::new(io::ErrorKind::InvalidInput, USAGE);
    while let Some(arg) = args.next() {
        match arg.to_str() {
            Some("--config") if config.is_none() && !legacy_env => {
                let path = PathBuf::from(args.next().ok_or_else(bad)?);
                if !path.is_absolute() {
                    return Err(bad());
                }
                config = Some(path);
            }
            Some("--legacy-env") if !legacy_env && config.is_none() => legacy_env = true,
            Some("-h" | "--help") if !help && !version => help = true,
            Some("-V" | "--version") if !version && !help && operation.is_none() => version = true,
            Some("serve" | "init-db" | "bootstrap-user" | "complete-setup")
                if operation.is_none() && !version =>
            {
                operation = Some(match arg.to_str() {
                    Some("init-db") => Command::InitDb,
                    Some("bootstrap-user") => Command::BootstrapUser,
                    Some("complete-setup") => Command::CompleteSetup,
                    _ => Command::Serve,
                });
            }
            _ => return Err(bad()),
        }
    }
    Ok(Arguments {
        command: if help {
            Command::Help
        } else if version {
            Command::Version
        } else {
            operation.unwrap_or(Command::Serve)
        },
        config,
        legacy_env,
    })
}

fn main() -> Result<(), Box<dyn Error>> {
    let arguments = command(env::args_os().skip(1))?;
    let operation = match arguments.command {
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
        Command::CompleteSetup => Some(admin::Operation::CompleteSetup),
    };
    config::initialize(arguments.config.as_deref(), arguments.legacy_env)?;
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
    let address = config::listen_address()?;
    let drain_timeout = config::drain_timeout()?;
    let database_url = config::database_url()?;
    let lifecycle = Lifecycle::with_capacity(config::max_inflight()?);
    let ingress = config::ingress()?;
    let legacy_config = config::legacy_auth()?;
    let console_enabled = config::console_enabled()?;
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
                models: config::setting_os("LMM_CORE_MODEL_CONFIG_FILE").is_some(),
                events: config::setting_os("LMM_CORE_RPC_SOCKET").is_some(),
                payments: config::payments_configured(),
                commerce: config::commerce_configured(),
                console: console_enabled,
                web_identity: legacy_config.is_some(),
            },
        )
        .await?;
        // Installation belongs solely to the explicit init-db command. Even a
        // valid identity-only installation cannot stand in for the whole core.
        lmm_core::core_schema::check(&pool).await?;
        let mut store = config::identity_auth(IdentityStore::from_pool(pool.clone()))?
            .with_password_slots(config::password_slots()?)?
            .with_financial_profile_enabled(legacy_config.is_some())
            .with_commerce_scope_enabled(config::commerce_configured());
        capabilities.identity = true;
        model_gateway = config::models(pool.clone())?;
        if let Some(gateway) = &model_gateway {
            store = store.with_dashboard_routing(gateway.dashboard_routing());
            // Trusted recovery keeps indeterminate upstream holds reserved.
            // A failed recovery prevents listener binding/readiness.
            gateway.recover_expired(64).await?;
            gateway.reconcile_completed_streams(64).await?;
            capabilities.models = true;
            capabilities.models_streaming = gateway.streaming_enabled();
        }
        // A Web deployment must not offer a second registration path that
        // bypasses the Web legal-consent contract.
        let native_store = if legacy_config.is_some() {
            store.clone().with_registration_enabled(false)
        } else {
            store.clone()
        };
        let mut routes = lmm_core::identity_http::router(native_store);
        if console_enabled {
            routes = routes.merge(lmm_core::console_compat::router(
                lmm_core::console_compat::ConsoleCompat::new(pool.clone(), store.clone()),
            ));
            capabilities.console = true;
        }
        if let Some(config) = legacy_config {
            public_status = lmm_core::http::PublicStatus {
                password_login_enabled: true,
                password_register_enabled: config.registration_enabled,
                user_agreement_enabled: config.user_agreement_enabled,
                privacy_policy_enabled: config.privacy_policy_enabled,
                google_client_id: config.auth.google_client_id(&store).map(str::to_owned),
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
        || config::identity_database_required()?
        || console_enabled
        || config::payments_configured()
        || config::commerce_configured()
        || config::setting_os("LMM_CORE_MODEL_CONFIG_FILE").is_some()
        || config::setting_os("LMM_CORE_MODEL_LOCAL_HTTP").is_some()
    {
        return Err("Web authentication and model configuration require a core database".into());
    }
    let (stop, stopped) = tokio::sync::watch::channel(false);
    // RPC remains usable while accepted public requests and completions drain.
    #[cfg(unix)]
    let (rpc_stop, rpc_stopped) = tokio::sync::watch::channel(false);
    #[cfg(unix)]
    let rpc = match (
        config::setting_os("LMM_CORE_RPC_SOCKET"),
        config::setting_os("LMM_CORE_RPC_TOKEN_FILE"),
    ) {
        (None, None) => None,
        (Some(socket), Some(token_file)) => {
            let token = config::service_token(std::path::Path::new(&token_file))?;
            let store = match database_url.as_deref() {
                Some(url) => Some(lmm_core::internal_rpc::readonly_store(url).await?),
                None => None,
            };
            let mut server = lmm_core::internal_rpc::RpcServer::bind_with_group(
                std::path::Path::new(&socket),
                &token,
                store,
                config::rpc_shared_group()?,
            )?;
            if let Some(url) = database_url.as_deref() {
                let events = lmm_core::events::EventStore::connect(url).await?;
                server = server.with_events(events, "extensions")?;
                capabilities.events = true;
                let financial = config::financial_rpcs(url, &token).await?;
                if let Some(payments) = financial.payments {
                    server = server.with_payments(payments);
                    capabilities.payments = true;
                }
                if let Some(commerce) = financial.commerce {
                    server = server.with_commerce(commerce);
                    capabilities.commerce = true;
                }
            }
            Some(server)
        }
        _ => return Err("configure both RPC socket and service credential file".into()),
    };
    #[cfg(unix)]
    if rpc.is_none() && (config::payments_configured() || config::commerce_configured()) {
        return Err("financial RPC configuration requires the private RPC listener".into());
    }
    #[cfg(not(unix))]
    if config::setting_os("LMM_CORE_RPC_SOCKET").is_some()
        || config::setting_os("LMM_CORE_RPC_TOKEN_FILE").is_some()
        || config::payments_configured()
        || config::commerce_configured()
    {
        return Err("internal RPC requires a Unix socket on this release".into());
    }
    let rpc_running = Arc::new(AtomicBool::new(false));
    let models_healthy = Arc::new(AtomicBool::new(true));
    if let Some(pool) = &database_pool {
        let rpc_status = capabilities.events.then(|| rpc_running.clone());
        app = lmm_core::http::configured_router_with_runtime_status(
            pool.clone(),
            capabilities,
            rpc_status,
            capabilities.models.then(|| models_healthy.clone()),
            public_status,
        )
        .merge(app);
    }
    let listener = tokio::net::TcpListener::bind(address).await?;
    let recovery_task = model_gateway.clone().map(|gateway| {
        let mut stopped = stopped.clone();
        let healthy = models_healthy.clone();
        tokio::spawn(async move {
            let mut interval = tokio::time::interval(Duration::from_secs(5));
            interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
            loop {
                tokio::select! {
                    _ = interval.tick() => {},
                    _ = stopped.changed() => break,
                }
                if *stopped.borrow() {
                    break;
                }
                // Finish accepted reconciliation transactions even if shutdown
                // arrives. Partial provider evidence never releases a hold.
                let recovered = gateway.recover_expired(64).await;
                let reconciled = gateway.reconcile_completed_streams(64).await;
                let available = recovered.is_ok() && reconciled.is_ok();
                healthy.store(available, Ordering::Release);
                if !available {
                    eprintln!("synthetic model recovery unavailable; model readiness withheld");
                }
            }
        })
    });
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
            gateway.drain(drain_timeout).await;
        }
    });
    eprintln!(
        "lmm-core {} listening on {}; identity={}, legacy_web_auth={}, console={}, synthetic_models={}, streaming={}, events={}, test_payments_rpc={}, commerce_rpc={}",
        env!("CARGO_PKG_VERSION"),
        address,
        capabilities.identity,
        capabilities.legacy_web_auth,
        capabilities.console,
        capabilities.models,
        capabilities.models_streaming,
        capabilities.events,
        capabilities.payments,
        capabilities.commerce,
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
    if let Some(task) = recovery_task {
        let _ = task.await;
    }
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
