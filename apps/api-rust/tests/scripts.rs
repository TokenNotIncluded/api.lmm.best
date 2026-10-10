use std::{path::PathBuf, sync::Arc};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, Request, StatusCode},
};
use lmm_api_rs::routes::{
    scripts::{ScriptsState, api_router, download_router},
    system_config::{SystemConfigAuthorizer, SystemConfigIdentity},
};
use serde_json::{Value, json};
use tower::ServiceExt;

struct Root(bool);
#[async_trait]
impl SystemConfigAuthorizer for Root {
    async fn require_root_dashboard_session(
        &self,
        _: &HeaderMap,
    ) -> Result<SystemConfigIdentity, ()> {
        if self.0 {
            Ok(SystemConfigIdentity { user_id: 7 })
        } else {
            Err(())
        }
    }
}

struct Fixture {
    path: PathBuf,
}
impl Fixture {
    fn new() -> Self {
        Self {
            path: std::env::temp_dir().join(format!("lmm-script-tests-{}", uuid::Uuid::new_v4())),
        }
    }
    fn app(&self, root: bool) -> axum::Router {
        let state = ScriptsState::new(self.path.clone(), Arc::new(Root(root)));
        api_router(state.clone()).merge(download_router(state))
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        let _ = std::fs::remove_dir_all(&self.path);
    }
}

async fn json_body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), usize::MAX).await.unwrap()).unwrap()
}

#[tokio::test]
async fn root_roundtrip_and_anonymous_downloads_preserve_bytes_headers_and_counts() {
    let fixture = Fixture::new();
    let app = fixture.app(true);
    let response = app
        .clone()
        .oneshot(Request::get("/api/scripts").body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(
        json_body(response).await,
        json!({"success":true,"message":"","data":[]})
    );
    let bytes = b"#!/bin/sh\nprintf '\xe4\xbd\xa0\xe5\xa5\xbd\\n'\r\n";
    let response = app
        .clone()
        .oneshot(
            Request::put("/api/scripts/install.sh")
                .header("content-type", "text/plain")
                .body(Body::from(bytes.as_slice()))
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(
        response.headers()["auth-version"],
        lmm_api_rs::auth_version::AUTH_VERSION
    );
    assert_eq!(json_body(response).await["data"]["size"], bytes.len());
    for path in ["/scripts/install.sh", "/api/scripts/install.sh/raw"] {
        let response = fixture
            .app(false)
            .oneshot(Request::get(path).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            response.headers()["content-type"],
            "text/plain; charset=utf-8"
        );
        assert!(
            response.headers()["cache-control"]
                .to_str()
                .unwrap()
                .contains("no-store")
        );
        assert_eq!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .unwrap()
                .as_ref(),
            bytes
        );
    }
    let listed = json_body(
        app.clone()
            .oneshot(Request::get("/api/scripts").body(Body::empty()).unwrap())
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(listed["data"][0]["fetches"], 2);
    let read = json_body(
        app.clone()
            .oneshot(
                Request::get("/api/scripts/install.sh")
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(
        read["data"]["content"],
        String::from_utf8_lossy(bytes).as_ref()
    );
    use std::os::unix::fs::PermissionsExt;
    assert_eq!(
        std::fs::metadata(fixture.path.join("install.sh"))
            .unwrap()
            .permissions()
            .mode()
            & 0o777,
        0o640
    );
    assert_eq!(
        json_body(
            app.clone()
                .oneshot(
                    Request::delete("/api/scripts/install.sh")
                        .body(Body::empty())
                        .unwrap()
                )
                .await
                .unwrap()
        )
        .await["success"],
        true
    );
    assert_eq!(
        app.oneshot(
            Request::get("/scripts/install.sh")
                .body(Body::empty())
                .unwrap()
        )
        .await
        .unwrap()
        .status(),
        StatusCode::NOT_FOUND
    );
}

#[tokio::test]
async fn privileged_file_mutations_authenticate_before_body_or_filesystem_access() {
    let fixture = Fixture::new();
    for method in ["GET", "PUT", "DELETE"] {
        let response = fixture
            .app(false)
            .oneshot(
                Request::builder()
                    .method(method)
                    .uri("/api/scripts/protected.sh")
                    .header("x-role", "100")
                    .body(Body::from("malformed-json"))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }
    assert!(!fixture.path.exists());
}

#[tokio::test]
async fn json_raw_size_boundary_missing_and_invalid_paths_match_go_contract() {
    let fixture = Fixture::new();
    let app = fixture.app(true);
    for (body, expected) in [
        (r#"{"content":"echo hello\\n"}"#, "echo hello\\n"),
        ("null", ""),
        (r#"{"cOnTeNt":"mixed"}"#, "mixed"),
        (r#"{"content":"first","Content":"last"}"#, "last"),
        (r#"{"content":"retained","Content":null}"#, "retained"),
        (r#"{"content":null}"#, ""),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::put("/api/scripts/a.ps1")
                    .header("content-type", "Application/JSON; charset=utf-8")
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(
            std::fs::read(fixture.path.join("a.ps1")).unwrap(),
            expected.as_bytes()
        );
    }
    for body in [r#"{"content":42}"#, "{", "[]"] {
        let response = app
            .clone()
            .oneshot(
                Request::put("/api/scripts/a.ps1")
                    .header("content-type", "application/json")
                    .body(Body::from(body))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
        assert_eq!(json_body(response).await["message"], "invalid JSON request");
    }
    for length in [512 << 10, (512 << 10) + 1] {
        let response = app
            .clone()
            .oneshot(
                Request::put("/api/scripts/size.sh")
                    .body(Body::from(vec![b'x'; length]))
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(
            response.status(),
            if length == 512 << 10 {
                StatusCode::OK
            } else {
                StatusCode::PAYLOAD_TOO_LARGE
            }
        );
    }
    for path in [
        "/scripts/no.txt",
        "/scripts/.secret.sh",
        "/scripts/%2e%2e%2fsecret.sh",
    ] {
        let response = app
            .clone()
            .oneshot(Request::get(path).body(Body::empty()).unwrap())
            .await
            .unwrap();
        assert_eq!(response.status(), StatusCode::BAD_REQUEST, "{path}");
    }
    let response = app
        .oneshot(
            Request::put("/api/scripts/limit.sh")
                .header("content-length", (512 * 1024 + 1).to_string())
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::PAYLOAD_TOO_LARGE);
    assert!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .unwrap()
            .is_empty()
    );
}

#[tokio::test]
async fn list_filters_symlinks_unsupported_oversized_and_nested_files() {
    let fixture = Fixture::new();
    std::fs::create_dir_all(fixture.path.join("directory.sh")).unwrap();
    for name in ["z.SH", "a.cmd", "readme.md"] {
        std::fs::write(fixture.path.join(name), b"content").unwrap();
    }
    std::fs::write(fixture.path.join("oversized.sh"), vec![0; (512 << 10) + 1]).unwrap();
    std::os::unix::fs::symlink(fixture.path.join("a.cmd"), fixture.path.join("link.sh")).unwrap();
    let app = fixture.app(false);
    let response = app
        .clone()
        .oneshot(
            Request::get("/scripts/link.sh")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    let listed = json_body(
        app.oneshot(Request::get("/api/scripts").body(Body::empty()).unwrap())
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(
        listed["data"]
            .as_array()
            .unwrap()
            .iter()
            .map(|item| item["name"].as_str().unwrap())
            .collect::<Vec<_>>(),
        ["a.cmd", "z.SH"]
    );
}

#[tokio::test]
async fn concurrent_downloads_never_lose_fetch_counts() {
    let fixture = Fixture::new();
    std::fs::create_dir_all(&fixture.path).unwrap();
    std::fs::write(fixture.path.join("a.sh"), b"echo hello").unwrap();
    let app = fixture.app(false);
    let mut tasks = tokio::task::JoinSet::new();
    for _ in 0..40 {
        let app = app.clone();
        tasks.spawn(async move {
            app.oneshot(Request::get("/scripts/a.sh").body(Body::empty()).unwrap())
                .await
                .unwrap()
                .status()
        });
    }
    while let Some(result) = tasks.join_next().await {
        assert_eq!(result.unwrap(), StatusCode::OK);
    }
    let listed = json_body(
        app.oneshot(Request::get("/api/scripts").body(Body::empty()).unwrap())
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(listed["data"][0]["fetches"], 40);
}

#[tokio::test]
#[ignore = "requires disposable PostgreSQL and Valkey through with-local-services.py"]
async fn repository_options_commit_refresh_runtime_invalidate_cache_and_redact_audit() {
    use lmm_api_rs::{
        auth::{AuthConfig, PgValkeyDashboardAuth},
        routes::{scripts::RepositoryServices, system_config::ProcessRuntimeOptions},
    };
    let database_url = std::env::var("LMM_TEST_DATABASE_URL").expect("disposable PostgreSQL");
    let admin = sqlx::PgPool::connect(&database_url).await.unwrap();
    let schema = format!("scripts_repository_{}", uuid::Uuid::new_v4().simple());
    sqlx::query(&format!("CREATE SCHEMA {schema}"))
        .execute(&admin)
        .await
        .unwrap();
    let pg = sqlx::postgres::PgPoolOptions::new()
        .max_connections(4)
        .after_connect({
            let schema = schema.clone();
            move |connection, _| {
                let statement = format!("SET search_path TO {schema}");
                Box::pin(async move {
                    sqlx::query(&statement).execute(connection).await?;
                    Ok(())
                })
            }
        })
        .connect(&database_url)
        .await
        .unwrap();
    sqlx::raw_sql("CREATE TABLE options(key TEXT PRIMARY KEY,value TEXT); CREATE TABLE users(id BIGINT PRIMARY KEY,username TEXT,role BIGINT); CREATE TABLE logs(user_id BIGINT,created_at BIGINT,type BIGINT,content TEXT,username TEXT,ip TEXT,other TEXT); INSERT INTO users VALUES (7,'root-fixture',100);")
        .execute(&pg).await.unwrap();
    let valkey =
        redis::Client::open(std::env::var("LMM_AUTH_TEST_VALKEY_URL").expect("disposable Valkey"))
            .unwrap();
    let mut cache = valkey.get_multiplexed_async_connection().await.unwrap();
    redis::cmd("SET")
        .arg("lmm:system-config:options")
        .arg("stale")
        .query_async::<()>(&mut cache)
        .await
        .unwrap();
    let auth = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey.clone(),
            AuthConfig {
                session_secret: secrecy::SecretString::from("ScriptParity-Synthetic-2026!Secret"),
                critical_rate_limit_enabled: false,
                ..AuthConfig::default()
            },
        )
        .unwrap(),
    );
    let runtime = Arc::new(ProcessRuntimeOptions::default());
    let fixture = Fixture::new();
    let app = api_router(
        ScriptsState::new(fixture.path.clone(), Arc::new(Root(true))).with_repository(
            RepositoryServices::new(pg.clone(), auth, runtime.clone(), valkey),
        ),
    );
    let saved = json_body(app.clone().oneshot(Request::put("/api/scripts/repository")
        .header("content-type", "application/json")
        .body(Body::from(r#"{"repository_url":" https://example.test/scripts ","github_key":"fixture-private-key"}"#)).unwrap()).await.unwrap()).await;
    assert_eq!(
        saved,
        json!({"success":true,"message":"","data":{"repository_url":"https://example.test/scripts","branch":"main","github_key_set":true}})
    );
    let options = runtime.snapshot().await;
    assert_eq!(options["ScriptsRepoGithubKey"], "fixture-private-key");
    assert_eq!(options["ScriptsRepoBranch"], "main");
    assert_eq!(
        redis::cmd("EXISTS")
            .arg("lmm:system-config:options")
            .query_async::<i64>(&mut cache)
            .await
            .unwrap(),
        0
    );
    let audit: String = sqlx::query_scalar("SELECT other FROM logs")
        .fetch_one(&pg)
        .await
        .unwrap();
    assert!(!audit.contains("fixture-private-key"));
    assert_eq!(
        serde_json::from_str::<Value>(&audit).unwrap()["op"]["action"],
        "scripts.repository.update"
    );
    let rejected = json_body(
        app.clone()
            .oneshot(
                Request::put("/api/scripts/repository")
                    .body(Body::from(
                        r#"{"repository_url":"https://other.test/scripts","branch":"../escape"}"#,
                    ))
                    .unwrap(),
            )
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(rejected["success"], false);
    assert_eq!(
        runtime.snapshot().await["ScriptsRepoURL"],
        "https://example.test/scripts"
    );
    let preserved = json_body(app.clone().oneshot(Request::put("/api/scripts/repository")
        .body(Body::from(r#"{"repository_url":"https://example.test/scripts","branch":"release/v1"}"#)).unwrap()).await.unwrap()).await;
    assert_eq!(preserved["data"]["github_key_set"], true);
    let cleared = json_body(
        app.clone()
            .oneshot(
                Request::put("/api/scripts/repository")
                    .body(Body::from(
                        r#"{"github_key":"ignored-when-clearing","clear_github_key":true}"#,
                    ))
                    .unwrap(),
            )
            .await
            .unwrap(),
    )
    .await;
    assert_eq!(cleared["data"]["github_key_set"], false);
    assert_eq!(runtime.snapshot().await["ScriptsRepoGithubKey"], "");
    let missing = json_body(
        app.oneshot(
            Request::post("/api/scripts/repository/pull")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap(),
    )
    .await;
    assert_eq!(missing["message"], "configure a script repository first");
    pg.close().await;
    sqlx::query(&format!("DROP SCHEMA {schema} CASCADE"))
        .execute(&admin)
        .await
        .unwrap();
    admin.close().await;
}
