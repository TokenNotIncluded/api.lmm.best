//! Public hosted-script downloads and root-only file management, matching Go.

mod repository;
pub use repository::RepositoryServices;

use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{self, Read, Write},
    path::{Path as FsPath, PathBuf},
    sync::Arc,
};

use axum::{
    Router,
    body::{Body, to_bytes},
    extract::{Path, Request, State},
    http::{HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::Response,
    routing::{get, post},
};
use chrono::{DateTime, Timelike, Utc};
use serde::Deserialize;
use serde_json::{Value, json};
use tokio::sync::Mutex;

use super::{
    legacy_http::legacy_json,
    system_config::{SystemConfigAuthorizer, auth_rejection},
};
use crate::legacy_empty_response;

const MAX_SCRIPT_BYTES: usize = 512 << 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

#[derive(Clone)]
pub struct ScriptsState {
    directory: Arc<PathBuf>,
    authorizer: Arc<dyn SystemConfigAuthorizer>,
    stats_lock: Arc<Mutex<()>>,
    repository: Option<Arc<RepositoryServices>>,
}

impl ScriptsState {
    pub fn new(directory: PathBuf, authorizer: Arc<dyn SystemConfigAuthorizer>) -> Self {
        Self {
            directory: Arc::new(directory),
            authorizer,
            stats_lock: Arc::new(Mutex::new(())),
            repository: None,
        }
    }

    #[must_use]
    pub fn with_repository(mut self, repository: RepositoryServices) -> Self {
        self.repository = Some(Arc::new(repository));
        self
    }

    pub fn from_environment(authorizer: Arc<dyn SystemConfigAuthorizer>) -> io::Result<Self> {
        let directory = match std::env::var("SCRIPTS_DIR") {
            Ok(value) if !value.trim().is_empty() => PathBuf::from(value.trim()),
            _ => std::env::current_dir()?.join("hosted-scripts"),
        };
        Ok(Self::new(directory, authorizer))
    }
}

/// `/scripts/{name}` is mounted separately from the `/api` rate-limit group,
/// exactly like the public Go download alias.
pub fn download_router(state: ScriptsState) -> Router {
    Router::new()
        .route("/scripts/{name}", get(download))
        .with_state(state)
}

pub fn api_router(state: ScriptsState) -> Router {
    let management = Router::new()
        .route(
            "/api/scripts/repository",
            get(repository::get).put(repository::put),
        )
        .route("/api/scripts/repository/pull", post(repository::pull))
        .route("/api/scripts/{name}", get(read).put(write).delete(delete))
        .route_layer(middleware::from_fn_with_state(state.clone(), root_guard));
    Router::new()
        .route("/api/scripts", get(list))
        .route("/api/scripts/{name}/raw", get(download))
        .merge(management)
        .with_state(state)
}

fn no_cache(mut response: Response) -> Response {
    for (name, value) in [
        (
            header::CACHE_CONTROL,
            "no-store, no-cache, must-revalidate, private, max-age=0",
        ),
        (header::PRAGMA, "no-cache"),
        (header::EXPIRES, "0"),
    ] {
        response
            .headers_mut()
            .insert(name, HeaderValue::from_static(value));
    }
    response
}

fn success(data: Value) -> Response {
    no_cache(legacy_json(
        StatusCode::OK,
        json!({"success":true,"message":"","data":data}),
    ))
}

fn failure(message: impl ToString) -> Response {
    no_cache(legacy_json(
        StatusCode::OK,
        json!({"success":false,"message":message.to_string()}),
    ))
}

async fn root_guard(
    State(state): State<ScriptsState>,
    mut request: Request,
    next: Next,
) -> Response {
    let actor = match state.authorizer.authorize_root(request.headers()).await {
        Ok(actor) => actor,
        Err(rejection) => return auth_rejection(request.headers(), rejection),
    };
    request.extensions_mut().insert(actor);
    let body_limit = if request.uri().path() == "/api/scripts/repository" {
        16 << 10
    } else {
        MAX_SCRIPT_BYTES
    };
    // Go's body-limit middleware runs after RootAuth and before the handler.
    if request.method() == axum::http::Method::PUT
        && request
            .headers()
            .get(header::CONTENT_LENGTH)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.parse::<u64>().ok())
            .is_some_and(|length| length > body_limit as u64)
    {
        let mut response = no_cache(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
        response
            .headers_mut()
            .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
        return response;
    }
    let mut response = no_cache(next.run(request).await);
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn valid_name(name: &str) -> Result<(), &'static str> {
    if name.is_empty()
        || name.len() > 128
        || !name.as_bytes()[0].is_ascii_alphanumeric()
        || !name
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'-'))
    {
        return Err("invalid script name");
    }
    let extension = FsPath::new(name)
        .extension()
        .and_then(|value| value.to_str())
        .unwrap_or("")
        .to_ascii_lowercase();
    if !matches!(
        extension.as_str(),
        "sh" | "bash" | "zsh" | "ps1" | "cmd" | "bat"
    ) {
        return Err("unsupported script extension");
    }
    Ok(())
}

fn read_file(directory: &FsPath, name: &str) -> io::Result<(Vec<u8>, fs::Metadata)> {
    valid_name(name).map_err(io::Error::other)?;
    let path = directory.join(name);
    let metadata = fs::symlink_metadata(&path)?;
    if !metadata.is_file() {
        return Err(io::Error::other("script is not a regular file"));
    }
    if metadata.len() > MAX_SCRIPT_BYTES as u64 {
        return Err(io::Error::other("script is too large"));
    }
    // O_NOFOLLOW also closes the lstat/open symlink-swap window.
    let fd = rustix::fs::open(
        &path,
        rustix::fs::OFlags::RDONLY | rustix::fs::OFlags::NOFOLLOW | rustix::fs::OFlags::CLOEXEC,
        rustix::fs::Mode::empty(),
    )?;
    let mut body = Vec::new();
    File::from(fd)
        .take((MAX_SCRIPT_BYTES + 1) as u64)
        .read_to_end(&mut body)?;
    if body.len() > MAX_SCRIPT_BYTES {
        return Err(io::Error::other("script is too large"));
    }
    Ok((body, metadata))
}

fn timestamp(metadata: &fs::Metadata) -> io::Result<String> {
    let date: DateTime<Utc> = metadata.modified()?.into();
    let base = date.format("%Y-%m-%dT%H:%M:%S").to_string();
    if date.nanosecond() == 0 {
        return Ok(format!("{base}Z"));
    }
    Ok(format!(
        "{base}.{}Z",
        format!("{:09}", date.nanosecond()).trim_end_matches('0')
    ))
}

fn stats(directory: &FsPath) -> BTreeMap<String, i64> {
    fs::read(directory.join(".fetch-stats.json"))
        .ok()
        .and_then(|raw| serde_json::from_slice(&raw).ok())
        .unwrap_or_default()
}

fn atomic_write(directory: &FsPath, name: &str, content: &[u8], mode: u32) -> io::Result<()> {
    use std::os::unix::fs::{DirBuilderExt, OpenOptionsExt};
    fs::DirBuilder::new()
        .recursive(true)
        .mode(0o750)
        .create(directory)?;
    let temporary = directory.join(format!(".script-{}", uuid::Uuid::new_v4()));
    let result = (|| {
        let mut file = OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(mode)
            .open(&temporary)?;
        file.write_all(content)?;
        drop(file);
        fs::rename(&temporary, directory.join(name))
    })();
    if result.is_err() {
        let _ = fs::remove_file(temporary);
    }
    result
}

async fn list(State(state): State<ScriptsState>) -> Response {
    match tokio::task::spawn_blocking(move || -> io::Result<Value> {
        let entries = match fs::read_dir(&*state.directory) {
            Ok(entries) => entries,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(json!([])),
            Err(error) => return Err(error),
        };
        let fetches = stats(&state.directory);
        let mut items = Vec::new();
        for entry in entries {
            let entry = entry?;
            let Some(name) = entry.file_name().to_str().map(str::to_owned) else { continue; };
            if valid_name(&name).is_err() { continue; }
            let Ok(metadata) = fs::symlink_metadata(entry.path()) else { continue; };
            if !metadata.is_file() || metadata.len() > MAX_SCRIPT_BYTES as u64 { continue; }
            items.push((name.clone(), json!({"name":name,"size":metadata.len(),
                "updated":timestamp(&metadata)?,"fetches":fetches.get(&name).copied().unwrap_or(0)})));
        }
        items.sort_by(|left, right| left.0.cmp(&right.0));
        Ok(Value::Array(items.into_iter().map(|(_, value)| value).collect()))
    }).await {
        Ok(Ok(items)) => success(items),
        Ok(Err(error)) => failure(error),
        Err(_) => failure("script storage unavailable"),
    }
}

async fn download(State(state): State<ScriptsState>, Path(name): Path<String>) -> Response {
    let directory = Arc::clone(&state.directory);
    let read_name = name.clone();
    let body = match tokio::task::spawn_blocking(move || read_file(&directory, &read_name)).await {
        Ok(Ok((body, _))) => body,
        Ok(Err(error)) => {
            return no_cache(legacy_empty_response(
                if error.kind() == io::ErrorKind::NotFound {
                    StatusCode::NOT_FOUND
                } else {
                    StatusCode::BAD_REQUEST
                },
                None,
            ));
        }
        Err(_) => return no_cache(legacy_empty_response(StatusCode::BAD_REQUEST, None)),
    };
    let _lock = state.stats_lock.lock().await;
    let directory = Arc::clone(&state.directory);
    let _ = tokio::task::spawn_blocking(move || {
        let mut fetches = stats(&directory);
        let count = fetches.entry(name).or_default();
        *count = count.wrapping_add(1);
        if let Ok(raw) = serde_json::to_vec(&fetches) {
            let _ = atomic_write(&directory, ".fetch-stats.json", &raw, 0o600);
        }
    })
    .await;
    let mut response = Response::new(Body::from(body));
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/plain; charset=utf-8"),
    );
    no_cache(response)
}

async fn read(State(state): State<ScriptsState>, Path(name): Path<String>) -> Response {
    match tokio::task::spawn_blocking(move || {
        let (body, metadata) = read_file(&state.directory, &name)?;
        Ok::<_, io::Error>(json!({"name":name,"content":String::from_utf8_lossy(&body),
            "size":metadata.len(),"updated":timestamp(&metadata)?}))
    })
    .await
    {
        Ok(Ok(value)) => success(value),
        Ok(Err(error)) if error.kind() == io::ErrorKind::NotFound => {
            legacy_empty_response(StatusCode::NOT_FOUND, None)
        }
        Ok(Err(error)) => failure(error),
        Err(_) => failure("script storage unavailable"),
    }
}

#[derive(Default)]
struct WriteRequest {
    content: Option<String>,
}

impl<'de> Deserialize<'de> for WriteRequest {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct RequestVisitor;
        impl<'de> serde::de::Visitor<'de> for RequestVisitor {
            type Value = WriteRequest;
            fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                formatter.write_str("a script content object")
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut request = WriteRequest::default();
                while let Some(key) = map.next_key::<String>()? {
                    if key.eq_ignore_ascii_case("content") {
                        // encoding/json matches struct fields case-insensitively
                        // and later occurrences win, including mixed spellings.
                        if let Some(content) = map.next_value::<Option<String>>()? {
                            request.content = Some(content);
                        }
                    } else {
                        let _ = map.next_value::<serde::de::IgnoredAny>()?;
                    }
                }
                Ok(request)
            }
        }
        deserializer.deserialize_map(RequestVisitor)
    }
}

async fn write(
    State(state): State<ScriptsState>,
    Path(name): Path<String>,
    request: Request,
) -> Response {
    if let Err(error) = valid_name(&name) {
        return failure(error);
    }
    let json_content = request
        .headers()
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| {
            value
                .split(';')
                .next()
                .unwrap_or("")
                .trim()
                .eq_ignore_ascii_case("application/json")
        });
    let body = match to_bytes(request.into_body(), MAX_SCRIPT_BYTES).await {
        Ok(body) => body,
        Err(_) => {
            return legacy_json(
                StatusCode::PAYLOAD_TOO_LARGE,
                json!({"success":false,"message":"script is too large"}),
            );
        }
    };
    let content = if json_content {
        match serde_json::from_slice::<Option<WriteRequest>>(&body) {
            Ok(request) => request
                .unwrap_or_default()
                .content
                .unwrap_or_default()
                .into_bytes(),
            Err(_) => {
                return legacy_json(
                    StatusCode::BAD_REQUEST,
                    json!({"success":false,"message":"invalid JSON request"}),
                );
            }
        }
    } else {
        body.to_vec()
    };
    let size = content.len();
    match tokio::task::spawn_blocking(move || {
        atomic_write(&state.directory, &name, &content, 0o640)?;
        Ok::<_, io::Error>(name)
    })
    .await
    {
        Ok(Ok(name)) => success(json!({"name":name,"size":size})),
        Ok(Err(error)) => failure(format!("write script: {error}")),
        Err(_) => failure("script storage unavailable"),
    }
}

async fn delete(State(state): State<ScriptsState>, Path(name): Path<String>) -> Response {
    if let Err(error) = valid_name(&name) {
        return failure(error);
    }
    match tokio::task::spawn_blocking(move || {
        fs::remove_file(state.directory.join(&name))?;
        Ok::<_, io::Error>(name)
    })
    .await
    {
        Ok(Ok(name)) => success(json!({"name":name})),
        Ok(Err(error)) if error.kind() == io::ErrorKind::NotFound => {
            legacy_empty_response(StatusCode::NOT_FOUND, None)
        }
        Ok(Err(error)) => failure(error),
        Err(_) => failure("script storage unavailable"),
    }
}
