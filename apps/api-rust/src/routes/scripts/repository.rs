//! Root-owned repository settings and bounded, cancellable Git import.

use std::{collections::BTreeMap, path::PathBuf, sync::Arc, time::Duration};

use axum::{
    body::to_bytes,
    extract::{Extension, Request, State},
    http::StatusCode,
    response::Response,
};
use chrono::{SecondsFormat, Utc};
use serde::Deserialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row};
use tokio::{process::Command, sync::Mutex};

use super::{ScriptsState, atomic_write, failure, read_file, success, valid_name};
use crate::routes::system_config::{
    SystemConfigAuthContext, SystemConfigCredential, SystemConfigRuntimeWriter,
};
use crate::{
    ClientIpKey, RequestContext,
    auth::{CriticalRateLimitOutcome, DashboardAuth},
    legacy_empty_response,
};

const KEYS: &[&str] = &[
    "ScriptsRepoURL",
    "ScriptsRepoBranch",
    "ScriptsRepoGithubKey",
    "ScriptsRepoPulledAt",
];

pub struct RepositoryServices {
    pub(super) pool: PgPool,
    pub(super) auth: Arc<dyn DashboardAuth>,
    pub(super) runtime: Arc<dyn SystemConfigRuntimeWriter>,
    pub(super) valkey: redis::Client,
    pub(super) import_lock: Mutex<()>,
}

impl RepositoryServices {
    pub fn new(
        pool: PgPool,
        auth: Arc<dyn DashboardAuth>,
        runtime: Arc<dyn SystemConfigRuntimeWriter>,
        valkey: redis::Client,
    ) -> Self {
        Self {
            pool,
            auth,
            runtime,
            valkey,
            import_lock: Mutex::new(()),
        }
    }

    async fn options(&self) -> Result<BTreeMap<String, String>, String> {
        let rows =
            sqlx::query("SELECT key, COALESCE(value,'') AS value FROM options WHERE key = ANY($1)")
                .bind(KEYS)
                .fetch_all(&self.pool)
                .await
                .map_err(|_| "script repository storage unavailable")?;
        rows.into_iter()
            .map(|row| {
                let key: String = row
                    .try_get("key")
                    .map_err(|_| "invalid script repository option")?;
                let value: String = row
                    .try_get("value")
                    .map_err(|_| "invalid script repository option")?;
                Ok((key, value.trim().to_owned()))
            })
            .collect()
    }

    async fn save(&self, values: &[(String, String)]) -> Result<(), String> {
        self.runtime
            .preflight(values)
            .await
            .map_err(|_| "script repository runtime unavailable")?;
        let mut tx = self
            .pool
            .begin()
            .await
            .map_err(|_| "script repository storage unavailable")?;
        for (key, value) in values {
            sqlx::query("INSERT INTO options(key,value) VALUES ($1,$2) ON CONFLICT(key) DO UPDATE SET value=EXCLUDED.value")
                .bind(key).bind(value).execute(&mut *tx).await.map_err(|_| "script repository storage unavailable")?;
        }
        tx.commit()
            .await
            .map_err(|_| "script repository storage unavailable")?;
        self.runtime
            .apply_committed(values)
            .await
            .map_err(|_| "script repository runtime update failed")?;
        if let Ok(mut connection) = self.valkey.get_multiplexed_async_connection().await {
            let _ = redis::cmd("DEL")
                .arg("lmm:system-config:options")
                .query_async::<usize>(&mut connection)
                .await;
        }
        Ok(())
    }

    async fn audit(&self, actor: SystemConfigAuthContext, ip: &str, action: &str, params: Value) {
        let result = async {
            let user = sqlx::query("SELECT COALESCE(username,'') AS username, COALESCE(role,0)::BIGINT AS role FROM users WHERE id=$1")
                .bind(actor.identity.user_id).fetch_one(&self.pool).await?;
            let username: String = user.try_get("username")?;
            let role: i64 = user.try_get("role")?;
            let other = json!({"op":{"action":action,"params":params},"admin_info":{
                "admin_id":actor.identity.user_id,"admin_username":username,"admin_role":role,
                "auth_method":if actor.credential == SystemConfigCredential::PersonalAccessToken {"access_token"} else {"session"}
            }}).to_string();
            sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username,ip,other) VALUES ($1,$2,3,$3,$4,$5,$6)")
                .bind(actor.identity.user_id).bind(Utc::now().timestamp()).bind(action).bind(username).bind(ip).bind(other)
                .execute(&self.pool).await?;
            Ok::<_, sqlx::Error>(())
        }.await;
        if result.is_err() {
            tracing::warn!(action, "script repository audit write failed");
        }
    }
}

fn projection(options: &BTreeMap<String, String>) -> Value {
    let mut value = json!({"repository_url":options.get(KEYS[0]).map(String::as_str).unwrap_or(""),
        "branch":options.get(KEYS[1]).map(String::as_str).unwrap_or(""),
        "github_key_set":options.get(KEYS[2]).is_some_and(|value| !value.is_empty())});
    if let Some(timestamp) = options.get(KEYS[3]).filter(|value| !value.is_empty()) {
        value["last_pulled_at"] = json!(timestamp);
    }
    value
}

#[derive(Default)]
struct RepositoryRequest {
    repository_url: String,
    branch: String,
    github_key: String,
    clear_github_key: bool,
}

impl<'de> Deserialize<'de> for RepositoryRequest {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct RequestVisitor;
        impl<'de> serde::de::Visitor<'de> for RequestVisitor {
            type Value = RepositoryRequest;
            fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                formatter.write_str("a script repository object")
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut value = RepositoryRequest::default();
                while let Some(key) = map.next_key::<String>()? {
                    match key.to_ascii_lowercase().as_str() {
                        "repository_url" => {
                            if let Some(item) = map.next_value::<Option<String>>()? {
                                value.repository_url = item;
                            }
                        }
                        "branch" => {
                            if let Some(item) = map.next_value::<Option<String>>()? {
                                value.branch = item;
                            }
                        }
                        "github_key" => {
                            if let Some(item) = map.next_value::<Option<String>>()? {
                                value.github_key = item;
                            }
                        }
                        "clear_github_key" => {
                            if let Some(item) = map.next_value::<Option<bool>>()? {
                                value.clear_github_key = item;
                            }
                        }
                        _ => {
                            let _ = map.next_value::<serde::de::IgnoredAny>()?;
                        }
                    }
                }
                Ok(value)
            }
        }
        deserializer.deserialize_map(RequestVisitor)
    }
}

fn validated(mut input: RepositoryRequest) -> Result<RepositoryRequest, &'static str> {
    input.repository_url = input.repository_url.trim().to_owned();
    input.branch = input.branch.trim().to_owned();
    if input.branch.is_empty() {
        input.branch = "main".to_owned();
    }
    if !input.repository_url.is_empty() {
        let url = reqwest::Url::parse(&input.repository_url)
            .map_err(|_| "repository URL must be an http(s) URL without credentials")?;
        if !matches!(url.scheme(), "http" | "https")
            || url.host_str().is_none()
            || !url.username().is_empty()
            || url.password().is_some()
        {
            return Err("repository URL must be an http(s) URL without credentials");
        }
    }
    if input.branch.len() > 128
        || !input.branch.as_bytes()[0].is_ascii_alphanumeric()
        || !input
            .branch
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'.' | b'_' | b'/' | b'-'))
        || input.branch.contains("..")
        || input.branch.contains("//")
        || input.branch.ends_with(".lock")
        || input.branch.ends_with('/')
    {
        return Err("invalid repository branch");
    }
    if input.github_key.len() > 512 || input.github_key.contains(['\r', '\n']) {
        return Err("invalid GitHub key");
    }
    Ok(input)
}

fn client_ip(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|value| value.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|value| value.client_ip)
                .map(|value| value.to_string())
        })
        .unwrap_or_default()
}

pub(super) async fn get(State(state): State<ScriptsState>) -> Response {
    let Some(repository) = state.repository.as_ref() else {
        return failure("script repository storage unavailable");
    };
    match repository.options().await {
        Ok(options) => success(projection(&options)),
        Err(error) => failure(error),
    }
}

pub(super) async fn put(
    State(state): State<ScriptsState>,
    Extension(actor): Extension<SystemConfigAuthContext>,
    request: Request,
) -> Response {
    let Some(repository) = state.repository.as_ref() else {
        return failure("script repository storage unavailable");
    };
    let ip = client_ip(&request);
    let raw = match to_bytes(request.into_body(), 16 << 10).await {
        Ok(raw) => raw,
        Err(_) => return legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None),
    };
    // Go common.DecodeJson consumes the first value using json.Decoder;
    // unlike the script-content json.Unmarshal route it permits trailing data.
    let input = match Option::<RepositoryRequest>::deserialize(
        &mut serde_json::Deserializer::from_slice(&raw),
    ) {
        Ok(input) => match validated(input.unwrap_or_default()) {
            Ok(input) => input,
            Err(error) => return failure(error),
        },
        Err(_) => return failure("invalid JSON request"),
    };
    let mut values = vec![
        (KEYS[0].to_owned(), input.repository_url.clone()),
        (KEYS[1].to_owned(), input.branch.clone()),
    ];
    if input.clear_github_key {
        values.push((KEYS[2].to_owned(), String::new()));
    } else if !input.github_key.is_empty() {
        values.push((KEYS[2].to_owned(), input.github_key.trim().to_owned()));
    }
    if let Err(error) = repository.save(&values).await {
        return failure(error);
    }
    repository
        .audit(
            actor,
            &ip,
            "scripts.repository.update",
            json!({"repository_url":input.repository_url,"branch":input.branch,
        "github_key_changed":input.clear_github_key || !input.github_key.is_empty()}),
        )
        .await;
    match repository.options().await {
        Ok(options) => success(projection(&options)),
        Err(error) => failure(error),
    }
}

struct Checkout(PathBuf);
impl Drop for Checkout {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.0);
    }
}

/// Stage every supported file before publishing any of them. This also rejects
/// repository symlinks instead of reading outside the fetched tree.
fn import_files(
    source: &std::path::Path,
    destination: &std::path::Path,
) -> Result<Vec<String>, String> {
    use std::os::unix::fs::DirBuilderExt;
    let entries = std::fs::read_dir(source).map_err(|error| error.to_string())?;
    std::fs::DirBuilder::new()
        .recursive(true)
        .mode(0o750)
        .create(destination)
        .map_err(|error| error.to_string())?;
    let stage = Checkout(destination.join(format!(".script-import-{}", uuid::Uuid::new_v4())));
    std::fs::DirBuilder::new()
        .mode(0o700)
        .create(&stage.0)
        .map_err(|error| error.to_string())?;
    let mut files = Vec::new();
    for entry in entries {
        let entry = entry.map_err(|error| error.to_string())?;
        let Some(name) = entry.file_name().to_str().map(str::to_owned) else {
            continue;
        };
        if valid_name(&name).is_err()
            || entry
                .file_type()
                .map_err(|error| error.to_string())?
                .is_dir()
        {
            continue;
        }
        let (body, _) = read_file(source, &name).map_err(|error| error.to_string())?;
        // Bound resident memory to one script. Large repositories are staged
        // on disk before publication, without retaining every body in RAM.
        atomic_write(&stage.0, &name, &body, 0o640).map_err(|error| error.to_string())?;
        files.push(name);
    }
    if files.is_empty() {
        return Err("script repository contains no supported scripts".into());
    }
    files.sort();
    for name in files {
        std::fs::rename(stage.0.join(&name), destination.join(&name))
            .map_err(|error| error.to_string())?;
    }
    let mut names = Vec::new();
    for entry in std::fs::read_dir(destination).map_err(|error| error.to_string())? {
        let entry = entry.map_err(|error| error.to_string())?;
        let name = entry.file_name().to_string_lossy().into_owned();
        if !entry
            .file_type()
            .map_err(|error| error.to_string())?
            .is_dir()
            && valid_name(&name).is_ok()
        {
            names.push(name);
        }
    }
    names.sort();
    Ok(names)
}

pub(super) async fn pull(
    State(state): State<ScriptsState>,
    Extension(actor): Extension<SystemConfigAuthContext>,
    request: Request,
) -> Response {
    let Some(repository) = state.repository.as_ref() else {
        return failure("script repository storage unavailable");
    };
    let ip = client_ip(&request);
    match repository.auth.check_critical_rate_limit(&ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return legacy_empty_response(StatusCode::TOO_MANY_REQUESTS, Some(retry_after_seconds));
        }
        Err(_) => return legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
    }
    let _lock = repository.import_lock.lock().await;
    let options = match repository.options().await {
        Ok(options) => options,
        Err(error) => return failure(error),
    };
    let input = match validated(RepositoryRequest {
        repository_url: options.get(KEYS[0]).cloned().unwrap_or_default(),
        branch: options.get(KEYS[1]).cloned().unwrap_or_default(),
        github_key: options.get(KEYS[2]).cloned().unwrap_or_default(),
        clear_github_key: false,
    }) {
        Ok(input) => input,
        Err(error) => return failure(error),
    };
    if input.repository_url.is_empty() {
        return failure("configure a script repository first");
    }
    let checkout =
        Checkout(std::env::temp_dir().join(format!("lmm-script-repo-{}", uuid::Uuid::new_v4())));
    {
        use std::os::unix::fs::DirBuilderExt;
        if let Err(error) = std::fs::DirBuilder::new().mode(0o700).create(&checkout.0) {
            return failure(error);
        }
    }
    let mut command = Command::new("git");
    command
        .args([
            "clone",
            "--depth=1",
            "--branch",
            &input.branch,
            "--",
            &input.repository_url,
        ])
        .arg(&checkout.0)
        .kill_on_drop(true)
        .env("GIT_TERMINAL_PROMPT", "0");
    if !input.github_key.is_empty() {
        command
            .env("GIT_CONFIG_COUNT", "1")
            .env("GIT_CONFIG_KEY_0", "http.extraheader")
            .env(
                "GIT_CONFIG_VALUE_0",
                format!("Authorization: Bearer {}", input.github_key),
            );
    }
    match tokio::time::timeout(Duration::from_secs(120), command.output()).await {
        Ok(Ok(output)) if output.status.success() => {}
        Ok(Ok(output)) => {
            let message = String::from_utf8_lossy(&output.stderr).trim().to_owned();
            let message = if input.github_key.is_empty() {
                message
            } else {
                message.replace(&input.github_key, "[redacted]")
            };
            return failure(format!("git pull failed: {message}"));
        }
        Ok(Err(_)) => return failure("git pull failed: unable to start git"),
        Err(_) => return failure("git pull failed: deadline exceeded"),
    }
    let destination = Arc::clone(&state.directory);
    let scripts =
        match tokio::task::spawn_blocking(move || import_files(&checkout.0, &destination)).await {
            Ok(Ok(scripts)) => scripts,
            Ok(Err(error)) => return failure(error),
            Err(_) => return failure("script import failed"),
        };
    let timestamp = Utc::now().to_rfc3339_opts(SecondsFormat::Secs, true);
    if let Err(error) = repository
        .save(&[(KEYS[3].to_owned(), timestamp.clone())])
        .await
    {
        return failure(error);
    }
    repository
        .audit(
            actor,
            &ip,
            "scripts.repository.pull",
            json!({"repository_url":input.repository_url,"branch":input.branch}),
        )
        .await;
    success(json!({"updated_at":timestamp,"scripts":scripts}))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn repository_json_accepts_go_null_case_duplicates_and_first_value() {
        let raw = br#"{"BRANCH":"first","branch":null,"Branch":"release/v1","github_key":null,"clear_github_key":null} {"branch":"ignored"}"#;
        let request = Option::<RepositoryRequest>::deserialize(
            &mut serde_json::Deserializer::from_slice(raw),
        )
        .unwrap()
        .unwrap();
        assert_eq!(request.branch, "release/v1");
        assert_eq!(request.github_key, "");
        assert!(!request.clear_github_key);
    }

    #[test]
    fn validation_rejects_credential_urls_option_injection_and_invalid_branches() {
        for url in [
            "file:///etc",
            "ssh://host/repo",
            "https://user:secret@example.test/repo",
            "--upload-pack=evil",
        ] {
            assert!(
                validated(RepositoryRequest {
                    repository_url: url.into(),
                    ..Default::default()
                })
                .is_err()
            );
        }
        for branch in ["-b", "a..b", "a//b", "a.lock", "a/", "a b", "☃"] {
            assert!(
                validated(RepositoryRequest {
                    branch: branch.into(),
                    ..Default::default()
                })
                .is_err()
            );
        }
        let input = validated(RepositoryRequest {
            repository_url: " https://example.test/repo ".into(),
            ..Default::default()
        })
        .unwrap();
        assert_eq!(input.branch, "main");
        assert_eq!(input.repository_url, "https://example.test/repo");
    }

    #[test]
    fn public_projection_never_contains_the_repository_key() {
        let options = BTreeMap::from([(KEYS[2].to_owned(), "secret-fixture".to_owned())]);
        let value = projection(&options);
        assert_eq!(value["github_key_set"], true);
        assert!(!value.to_string().contains("secret-fixture"));
    }

    #[test]
    fn import_preserves_unrelated_files_and_rejects_external_symlinks_before_writes() {
        let directory = Checkout(
            std::env::temp_dir().join(format!("lmm-script-import-{}", uuid::Uuid::new_v4())),
        );
        let source = directory.0.join("source");
        let destination = directory.0.join("destination");
        std::fs::create_dir_all(&source).unwrap();
        std::fs::create_dir_all(&destination).unwrap();
        std::fs::write(source.join("one.sh"), "new").unwrap();
        std::fs::write(source.join("README.md"), "ignored").unwrap();
        std::fs::write(destination.join("local.sh"), "preserved").unwrap();
        assert_eq!(
            import_files(&source, &destination).unwrap(),
            ["local.sh", "one.sh"]
        );
        std::fs::write(source.join("one.sh"), "replacement").unwrap();
        std::os::unix::fs::symlink(destination.join("local.sh"), source.join("two.sh")).unwrap();
        assert!(import_files(&source, &destination).is_err());
        assert_eq!(
            std::fs::read_to_string(destination.join("one.sh")).unwrap(),
            "new"
        );
    }
}
