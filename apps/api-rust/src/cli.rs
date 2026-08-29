//! Stable public CLI dispatched before server configuration is read.

use std::{
    ffi::OsString,
    fs::{self, OpenOptions},
    io::{self, Write},
    os::unix::fs::OpenOptionsExt,
    path::{Path, PathBuf},
    time::Duration,
};

use clap::{ArgAction, Args, CommandFactory, Parser, Subcommand};

use crate::{
    deployment, frontend_deploy,
    provider_link::{Provider, os_manager},
    route_contract,
};

pub const EXIT_OK: i32 = 0;
pub const EXIT_ERROR: i32 = 1;
pub const EXIT_HTTP_FAILURE: i32 = 22;
pub const EXIT_USAGE: i32 = 64;

#[derive(Debug, Eq, PartialEq)]
pub enum DispatchOutcome {
    Serve,
    Exit(i32),
}

#[derive(Parser)]
#[command(
    name = "lmm-api",
    disable_version_flag = true,
    disable_help_subcommand = true
)]
struct Cli {
    #[command(subcommand)]
    command: Command,
}

#[derive(Subcommand)]
enum Command {
    Version,
    Status(RequestArgs),
    Doctor(RequestArgs),
    Request(RequestArgs),
    Migrate(MigrationArgs),
    Backend {
        #[command(subcommand)]
        command: BackendCommand,
    },
    Deploy {
        #[command(subcommand)]
        command: DeployCommand,
    },
}

#[derive(Args)]
struct MigrationArgs {
    #[arg(long, action = ArgAction::SetTrue, conflicts_with = "verify")]
    apply: bool,
    #[arg(long, action = ArgAction::SetTrue, conflicts_with = "apply")]
    verify: bool,
}

#[derive(Subcommand)]
enum BackendCommand {
    Status {
        #[arg(long)]
        json: bool,
    },
    Select {
        provider: Provider,
        #[arg(long)]
        json: bool,
    },
}

#[derive(Subcommand)]
enum DeployCommand {
    Frontend {
        #[command(subcommand)]
        command: FrontendCommand,
    },
    Production {
        #[command(subcommand)]
        command: ProductionCommand,
    },
    Contract {
        #[command(subcommand)]
        command: ContractCommand,
    },
    Plan,
    Stage,
    Promote,
}

#[derive(Subcommand)]
enum FrontendCommand {
    PackageActivate {
        #[arg(long)]
        package_version: String,
    },
    Prepare {
        #[arg(long, default_value = "/srv/lmm-api-frontend")]
        root: PathBuf,
    },
    Publish {
        #[arg(long)]
        source: PathBuf,
        #[arg(long)]
        release: String,
        #[arg(long, default_value = "/srv/lmm-api-frontend")]
        root: PathBuf,
        #[arg(long, default_value_t = 3)]
        keep: usize,
    },
    Rollback {
        #[arg(long)]
        release: Option<String>,
        #[arg(long, default_value = "/srv/lmm-api-frontend")]
        root: PathBuf,
        #[arg(long, default_value_t = 3)]
        keep: usize,
    },
}

#[derive(Subcommand)]
enum ProductionCommand {
    Status(ProductionTargetArgs),
    Confirm(ProductionTargetArgs),
    Rollback(ProductionRollbackArgs),
    Plan,
    Stage,
    Promote,
}

#[derive(Args)]
struct ProductionTargetArgs {
    #[arg(long)]
    workspace: PathBuf,
}

#[derive(Args)]
struct ProductionRollbackArgs {
    #[arg(long)]
    workspace: PathBuf,
    #[arg(long, default_value = "operator-request")]
    reason: String,
}

#[derive(Subcommand)]
enum ContractCommand {
    Route {
        #[command(subcommand)]
        command: RouteCommand,
    },
}

#[derive(Subcommand)]
enum RouteCommand {
    Print,
    Generate { output: PathBuf },
    Verify { revision_file: PathBuf },
}

#[derive(Clone, Debug, Args)]
struct RequestArgs {
    #[arg(value_name = "URL-or-path")]
    positional: Option<String>,
    #[arg(long)]
    url: Option<String>,
    #[arg(long)]
    base_url: Option<String>,
    #[arg(long)]
    path: Option<String>,
    #[arg(short = 'X', long, default_value = "GET")]
    method: String,
    #[arg(short = 'd', long)]
    body: Option<String>,
    #[arg(long)]
    body_file: Option<PathBuf>,
    #[arg(long)]
    token_file: Option<PathBuf>,
    #[arg(long)]
    token_env: Option<String>,
    #[arg(short = 'H', long = "header")]
    headers: Vec<String>,
    #[arg(short = 'o', long)]
    output: Option<PathBuf>,
    #[arg(long)]
    status_file: Option<PathBuf>,
    #[arg(long)]
    json: bool,
    #[arg(long)]
    fail: bool,
    #[arg(long)]
    show_status: bool,
    #[arg(long)]
    insecure: bool,
    #[arg(long)]
    no_follow: bool,
    #[arg(long, default_value = "30s", value_parser = parse_duration)]
    timeout: Duration,
}

fn parse_duration(value: &str) -> Result<Duration, String> {
    humantime::parse_duration(value).map_err(|_| "invalid duration".to_owned())
}

pub async fn dispatch_environment() -> DispatchOutcome {
    dispatch(std::env::args_os(), &mut io::stdout(), &mut io::stderr()).await
}

pub async fn dispatch<I, T>(
    args: I,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> DispatchOutcome
where
    I: IntoIterator<Item = T>,
    T: Into<OsString> + Clone,
{
    let values = args.into_iter().map(Into::into).collect::<Vec<_>>();
    if values.len() <= 1 {
        return DispatchOutcome::Serve;
    }
    let command = values[1].to_string_lossy();
    if command == "serve" || command.starts_with('-') {
        return DispatchOutcome::Serve;
    }
    if matches!(command.as_ref(), "help" | "--help" | "-h") {
        let mut help = Vec::new();
        let _ = Cli::command().write_long_help(&mut help);
        let _ = stdout.write_all(&help);
        let _ = writeln!(stdout);
        return DispatchOutcome::Exit(EXIT_OK);
    }
    if command == "--version" {
        let _ = writeln!(stdout, "{}", version());
        return DispatchOutcome::Exit(EXIT_OK);
    }
    let cli = match Cli::try_parse_from(values) {
        Ok(cli) => cli,
        Err(error) => {
            let _ = write!(stderr, "{error}");
            return DispatchOutcome::Exit(if error.use_stderr() {
                EXIT_USAGE
            } else {
                EXIT_OK
            });
        }
    };
    let code = execute(cli.command, stdout, stderr).await;
    DispatchOutcome::Exit(code)
}

async fn execute(command: Command, stdout: &mut dyn Write, stderr: &mut dyn Write) -> i32 {
    let result: Result<(), String> = match command {
        Command::Version => writeln!(stdout, "{}", version()).map_err(|error| error.to_string()),
        Command::Status(mut options) => {
            options.method = "GET".to_owned();
            options.path = Some("/api/status".to_owned());
            options.fail = true;
            request(options, stdout, stderr).await
        }
        Command::Doctor(mut options) => {
            options.method = "GET".to_owned();
            options.path = Some("/api/livez".to_owned());
            options.fail = true;
            request(options, stdout, stderr).await
        }
        Command::Request(options) => request(options, stdout, stderr).await,
        Command::Migrate(options) => {
            if options.apply == options.verify {
                Err("choose exactly one of --apply or --verify".to_owned())
            } else {
                Err(
                    "native Rust schema migration is not implemented; refusing to report success"
                        .to_owned(),
                )
            }
        }
        Command::Backend { command } => backend(command, stdout),
        Command::Deploy { command } => deploy(command, stdout).await,
    };
    match result {
        Ok(()) => EXIT_OK,
        Err(error) => {
            let _ = writeln!(stderr, "lmm-api: {error}");
            if error.starts_with("HTTP_STATUS=") {
                EXIT_HTTP_FAILURE
            } else {
                EXIT_ERROR
            }
        }
    }
}

fn backend(command: BackendCommand, stdout: &mut dyn Write) -> Result<(), String> {
    let manager = os_manager();
    let (status, json) = match command {
        BackendCommand::Status { json } => (manager.status(), json),
        BackendCommand::Select { provider, json } => (manager.select(provider), json),
    };
    let status = status.map_err(|error| error.to_string())?;
    if json {
        serde_json::to_writer_pretty(&mut *stdout, &status).map_err(|error| error.to_string())?;
        writeln!(stdout).map_err(|error| error.to_string())
    } else {
        writeln!(stdout, "provider={}", status.provider).map_err(|error| error.to_string())?;
        writeln!(stdout, "target={}", status.target).map_err(|error| error.to_string())?;
        writeln!(stdout, "package={}", status.package).map_err(|error| error.to_string())
    }
}

async fn deploy(command: DeployCommand, stdout: &mut dyn Write) -> Result<(), String> {
    match command {
        DeployCommand::Frontend { command } => {
            let current = match command {
                FrontendCommand::PackageActivate { package_version } => {
                    frontend_deploy::package_activate(&package_version)
                }
                FrontendCommand::Prepare { root } => {
                    frontend_deploy::prepare(&root).map_err(|error| error.to_string())?;
                    return writeln!(stdout, "prepared={}", root.display())
                        .map_err(|error| error.to_string());
                }
                FrontendCommand::Publish {
                    source,
                    release,
                    root,
                    keep,
                } => frontend_deploy::publish(&root, &source, &release, keep),
                FrontendCommand::Rollback {
                    release,
                    root,
                    keep,
                } => frontend_deploy::rollback(&root, release.as_deref(), keep),
            }
            .map_err(|error| error.to_string())?;
            writeln!(stdout, "current={current}").map_err(|error| error.to_string())
        }
        DeployCommand::Production { command } => {
            let status = match command {
                ProductionCommand::Status(options) => deployment::target_status(&options.workspace),
                ProductionCommand::Confirm(options) => {
                    deployment::target_confirm(&options.workspace).await
                }
                ProductionCommand::Rollback(options) => {
                    deployment::target_rollback(&options.workspace, &options.reason).await
                }
                ProductionCommand::Plan => {
                    return Err(deployment::DeploymentError::UnsupportedController(
                        "plan".to_owned(),
                    )
                    .to_string());
                }
                ProductionCommand::Stage => {
                    return Err(deployment::DeploymentError::UnsupportedController(
                        "stage".to_owned(),
                    )
                    .to_string());
                }
                ProductionCommand::Promote => {
                    return Err(deployment::DeploymentError::UnsupportedController(
                        "promote".to_owned(),
                    )
                    .to_string());
                }
            }
            .map_err(|error| error.to_string())?;
            serde_json::to_writer_pretty(&mut *stdout, &status)
                .map_err(|error| error.to_string())?;
            writeln!(stdout).map_err(|error| error.to_string())
        }
        DeployCommand::Contract { command } => match command {
            ContractCommand::Route { command } => {
                let contract = route_contract::default_contract_path();
                let digest = match command {
                    RouteCommand::Print => route_contract::revision(&contract),
                    RouteCommand::Generate { output } => {
                        route_contract::generate(&contract, &output)
                    }
                    RouteCommand::Verify { revision_file } => {
                        route_contract::verify(&contract, &revision_file)
                    }
                }
                .map_err(|error| error.to_string())?;
                writeln!(stdout, "{digest}").map_err(|error| error.to_string())
            }
        },
        DeployCommand::Plan => {
            Err(deployment::DeploymentError::UnsupportedController("plan".to_owned()).to_string())
        }
        DeployCommand::Stage => {
            Err(deployment::DeploymentError::UnsupportedController("stage".to_owned()).to_string())
        }
        DeployCommand::Promote => Err(deployment::DeploymentError::UnsupportedController(
            "promote".to_owned(),
        )
        .to_string()),
    }
}

async fn request(
    options: RequestArgs,
    stdout: &mut dyn Write,
    stderr: &mut dyn Write,
) -> Result<(), String> {
    if options.body.is_some() && options.body_file.is_some() {
        return Err("--body and --body-file are mutually exclusive".to_owned());
    }
    if options.token_file.is_some() && options.token_env.is_some() {
        return Err("--token-file and --token-env are mutually exclusive".to_owned());
    }
    if !options
        .method
        .bytes()
        .all(|byte| byte.is_ascii_alphabetic())
    {
        return Err("HTTP method must contain only letters".to_owned());
    }
    let target = request_url(&options)?;
    let mut builder = reqwest::Client::builder()
        .timeout(options.timeout)
        .danger_accept_invalid_certs(options.insecure);
    if options.no_follow {
        builder = builder.redirect(reqwest::redirect::Policy::none());
    }
    let client = builder.build().map_err(|error| error.to_string())?;
    let method = reqwest::Method::from_bytes(options.method.to_uppercase().as_bytes())
        .map_err(|_| "invalid HTTP method".to_owned())?;
    let mut request = client.request(method, target).header(
        reqwest::header::USER_AGENT,
        format!("lmm-api/{}", version()),
    );
    if options.json {
        request = request
            .header(reqwest::header::ACCEPT, "application/json")
            .header(reqwest::header::CONTENT_TYPE, "application/json");
    }
    for header in &options.headers {
        let (name, value) = header
            .split_once(':')
            .ok_or_else(|| "header must contain a colon".to_owned())?;
        request = request.header(name.trim(), value.trim());
    }
    let token = if let Some(path) = &options.token_file {
        Some(read_private_text(path, 64 * 1024)?)
    } else if let Some(name) = &options.token_env {
        Some(std::env::var(name).map_err(|_| "token environment variable is missing".to_owned())?)
    } else {
        None
    };
    if let Some(token) = token {
        if token.contains(['\r', '\n']) || token.is_empty() {
            return Err("bearer token is invalid".to_owned());
        }
        request = request.bearer_auth(token);
    }
    if let Some(body) = options.body {
        request = request.body(body);
    } else if let Some(path) = options.body_file {
        request = request.body(fs::read(path).map_err(|error| error.to_string())?);
    }
    let response = request.send().await.map_err(|error| error.to_string())?;
    let status = response.status().as_u16();
    let bytes = response.bytes().await.map_err(|error| error.to_string())?;
    if let Some(path) = options.output {
        write_private(&path, &bytes)?;
    } else {
        stdout
            .write_all(&bytes)
            .map_err(|error| error.to_string())?;
    }
    if let Some(path) = options.status_file {
        write_private(&path, format!("{status}\n").as_bytes())?;
    }
    if options.show_status {
        writeln!(stderr, "HTTP_STATUS={status}").map_err(|error| error.to_string())?;
    }
    if options.fail && status >= 400 {
        return Err(format!("HTTP_STATUS={status}"));
    }
    Ok(())
}

fn request_url(options: &RequestArgs) -> Result<reqwest::Url, String> {
    let positional_is_url = options
        .positional
        .as_deref()
        .is_some_and(|value| value.starts_with("http://") || value.starts_with("https://"));
    if options.url.is_some() && (options.base_url.is_some() || options.path.is_some()) {
        return Err("--url cannot be combined with --base-url or --path".to_owned());
    }
    if options.positional.is_some() && (options.url.is_some() || options.path.is_some()) {
        return Err("positional URL or path conflicts with --url or --path".to_owned());
    }
    if let Some(url) = options.url.as_ref().or_else(|| {
        positional_is_url
            .then_some(options.positional.as_ref())
            .flatten()
    }) {
        let parsed = reqwest::Url::parse(url).map_err(|_| "invalid URL".to_owned())?;
        if !matches!(parsed.scheme(), "http" | "https")
            || parsed.username() != ""
            || parsed.password().is_some()
        {
            return Err("URL must be HTTP(S) without embedded credentials".to_owned());
        }
        return Ok(parsed);
    }
    let base = options
        .base_url
        .clone()
        .or_else(|| std::env::var("LMM_API_URL").ok())
        .unwrap_or_else(|| "http://127.0.0.1:3000".to_owned());
    let mut parsed = reqwest::Url::parse(&base).map_err(|_| "invalid base URL".to_owned())?;
    let path = options
        .path
        .as_ref()
        .or(options.positional.as_ref())
        .map_or("/", String::as_str);
    parsed.set_path(if path.starts_with('/') {
        path
    } else {
        return Err("request path must start with /".to_owned());
    });
    Ok(parsed)
}

fn read_private_text(path: &Path, maximum: u64) -> Result<String, String> {
    let metadata = fs::symlink_metadata(path).map_err(|error| error.to_string())?;
    if metadata.file_type().is_symlink() || !metadata.is_file() || metadata.len() > maximum {
        return Err("private input file is unsafe".to_owned());
    }
    let value = fs::read_to_string(path).map_err(|error| error.to_string())?;
    Ok(value.trim_end_matches(['\r', '\n']).to_owned())
}

fn write_private(path: &Path, bytes: &[u8]) -> Result<(), String> {
    let mut file = OpenOptions::new()
        .write(true)
        .create(true)
        .truncate(true)
        .mode(0o600)
        .open(path)
        .map_err(|error| error.to_string())?;
    file.write_all(bytes).map_err(|error| error.to_string())?;
    file.sync_all().map_err(|error| error.to_string())
}

#[must_use]
pub fn version() -> &'static str {
    option_env!("LMM_BUILD_VERSION").unwrap_or(env!("CARGO_PKG_VERSION"))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test]
    async fn no_command_starts_server_without_reading_configuration() {
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        assert_eq!(
            dispatch(["lmm-api"], &mut stdout, &mut stderr).await,
            DispatchOutcome::Serve
        );
    }

    #[tokio::test]
    async fn version_is_client_only() {
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        assert_eq!(
            dispatch(["lmm-api", "version"], &mut stdout, &mut stderr).await,
            DispatchOutcome::Exit(EXIT_OK)
        );
        assert!(!stdout.is_empty());
        assert!(stderr.is_empty());
    }

    #[tokio::test]
    async fn controller_operations_fail_closed() {
        let mut stdout = Vec::new();
        let mut stderr = Vec::new();
        assert_eq!(
            dispatch(
                ["lmm-api", "deploy", "production", "plan"],
                &mut stdout,
                &mut stderr,
            )
            .await,
            DispatchOutcome::Exit(EXIT_ERROR)
        );
        assert!(String::from_utf8_lossy(&stderr).contains("unsupported controller-side"));
    }
}
