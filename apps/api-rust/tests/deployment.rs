use std::{
    collections::VecDeque,
    env,
    sync::{Arc, Mutex},
    time::Duration,
};

use async_trait::async_trait;
use axum::{
    Extension,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_api_rs::routes::deployment::{
    DeploymentActor, DeploymentCall, DeploymentError, DeploymentJobRunner, DeploymentOperation,
    DeploymentProvider, DeploymentState, PgValkeyDeploymentProvider, router,
};
use serde_json::{Value, json};
use sqlx::postgres::PgPoolOptions;
use tokio::{sync::Notify, time::sleep};
use tower::ServiceExt;

#[derive(Default)]
struct Stub {
    calls: Mutex<Vec<DeploymentCall>>,
    error: Mutex<Option<DeploymentError>>,
}
#[async_trait]
impl DeploymentProvider for Stub {
    async fn execute(&self, call: DeploymentCall) -> Result<Value, DeploymentError> {
        self.calls.lock().expect("stub lock").push(call.clone());
        self.error.lock().expect("stub lock").clone().map_or_else(
            || Ok(json!({"operation": format!("{:?}", call.operation)})),
            Err,
        )
    }
}
fn app(stub: Arc<Stub>, actor: DeploymentActor) -> axum::Router {
    router(DeploymentState::new(stub)).layer(Extension(actor))
}
async fn call(app: &axum::Router, request: Request<Body>) -> (StatusCode, Value) {
    let response = app.clone().oneshot(request).await.expect("response");
    let status = response.status();
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    (status, serde_json::from_slice(&bytes).expect("json"))
}
fn admin() -> DeploymentActor {
    DeploymentActor {
        user_id: 7,
        role: 10,
    }
}

#[tokio::test]
async fn every_legacy_route_is_admin_gated_with_current_go_auth_envelope() {
    let stub = Arc::new(Stub::default());
    let (status, body) = call(
        &app(
            stub,
            DeploymentActor {
                user_id: 7,
                role: 1,
            },
        ),
        Request::builder()
            .uri("/api/deployments/settings")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(
        (status, body),
        (
            StatusCode::FORBIDDEN,
            json!({
                "success": false,
                "code": "AUTH_INSUFFICIENT_PRIVILEGE",
                "message": "Unauthorized, insufficient privileges"
            })
        )
    );
}

#[tokio::test]
async fn authorization_precedes_body_and_query_parsing() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let app = app(
        stub,
        DeploymentActor {
            user_id: 7,
            role: 1,
        },
    );

    for request in [
        Request::builder()
            .method("POST")
            .uri("/api/deployments/")
            .header("content-type", "application/json")
            .body(Body::from("{"))
            .expect("malformed JSON request"),
        Request::builder()
            .uri("/api/deployments/available-replicas?hardware_id=%ZZ")
            .body(Body::empty())
            .expect("malformed query request"),
    ] {
        let (status, body) = call(&app, request).await;
        assert_eq!(status, StatusCode::FORBIDDEN);
        assert_eq!(
            body,
            json!({
                "success": false,
                "code": "AUTH_INSUFFICIENT_PRIVILEGE",
                "message": "Unauthorized, insufficient privileges"
            })
        );
    }
    assert!(inspected.calls.lock().expect("stub lock").is_empty());
}

#[tokio::test]
async fn malformed_admin_input_stays_inside_the_legacy_http_200_envelope() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let app = app(stub, admin());

    for (request, message) in [
        (
            Request::builder()
                .method("POST")
                .uri("/api/deployments/")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("malformed JSON request"),
            "invalid request payload",
        ),
        (
            Request::builder()
                .uri("/api/deployments/search?keyword=%ZZ")
                .body(Body::empty())
                .expect("malformed query request"),
            "invalid query parameters",
        ),
    ] {
        let (status, body) = call(&app, request).await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(body, json!({"success": false, "message": message}));
    }
    assert!(inspected.calls.lock().expect("stub lock").is_empty());
}

#[tokio::test]
async fn list_preserves_query_and_returns_exact_success_envelope() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let (_, body) = call(
        &app(stub, admin()),
        Request::builder()
            .uri("/api/deployments/?page=2&page_size=20&status=RUNNING")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(
        body,
        json!({"success": true, "message": "", "data": {"operation": "List"}})
    );
    assert_eq!(
        inspected.calls.lock().expect("stub lock")[0].input,
        json!({"page":"2", "page_size":"20", "status":"RUNNING"})
    );
}

#[tokio::test]
async fn invalid_hardware_request_does_not_reach_provider() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let (_, body) = call(
        &app(stub, admin()),
        Request::builder()
            .uri("/api/deployments/available-replicas?hardware_id=0")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(
        body,
        json!({"success": false, "message": "invalid hardware_id parameter"})
    );
    assert!(inspected.calls.lock().expect("stub lock").is_empty());
}

#[tokio::test]
async fn logs_without_a_container_id_do_not_reach_provider() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let (_, body) = call(
        &app(stub, admin()),
        Request::builder()
            .uri("/api/deployments/d-1/logs?limit=100")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(
        body,
        json!({"success": false, "message": "container_id parameter is required"})
    );
    assert!(inspected.calls.lock().expect("stub lock").is_empty());
}

#[tokio::test]
async fn name_availability_uses_the_trimmed_legacy_query_value() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let (_, body) = call(
        &app(stub, admin()),
        Request::builder()
            .uri("/api/deployments/check-name?name=%20primary%20")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(body["success"], true);
    assert_eq!(
        inspected.calls.lock().expect("stub lock")[0].input["name"],
        "primary"
    );
}

#[tokio::test]
async fn duplicate_rename_is_exposed_as_non_retryable_legacy_business_error() {
    let stub = Arc::new(Stub::default());
    *stub.error.lock().expect("stub lock") = Some(DeploymentError::InProgress);
    let (_, body) = call(
        &app(stub, admin()),
        Request::builder()
            .method("PUT")
            .uri("/api/deployments/deploy-1/name")
            .header("content-type", "application/json")
            .body(Body::from(
                r#"{"name":"  primary  ","idempotency_key":"abc"}"#,
            ))
            .expect("request"),
    )
    .await;
    assert_eq!(
        body,
        json!({"success": false, "message": "deployment request is already in progress"})
    );
}

#[tokio::test]
async fn container_detail_uses_path_identifier_and_provider_contract() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let _ = call(
        &app(stub, admin()),
        Request::builder()
            .uri("/api/deployments/d-1/containers/c-1")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    let call = &inspected.calls.lock().expect("stub lock")[0];
    assert_eq!(
        (call.operation, call.deployment_id.as_deref(), &call.input),
        (
            DeploymentOperation::GetContainer,
            Some("d-1"),
            &json!({"container_id":"c-1"})
        )
    );
}

#[tokio::test]
async fn test_connection_accepts_legacy_empty_body_and_rejects_malformed_json_in_the_envelope() {
    let stub = Arc::new(Stub::default());
    let inspected = Arc::clone(&stub);
    let (status, body) = call(
        &app(Arc::clone(&stub), admin()),
        Request::builder()
            .method("POST")
            .uri("/api/deployments/test-connection")
            .body(Body::empty())
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["success"], true);
    assert_eq!(
        inspected.calls.lock().expect("stub lock")[0].input,
        json!({})
    );

    let (status, body) = call(
        &app(stub, admin()),
        Request::builder()
            .method("POST")
            .uri("/api/deployments/test-connection")
            .body(Body::from("{not-json"))
            .expect("request"),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        body,
        json!({"success": false, "message": "invalid request payload"})
    );
}

#[derive(Default)]
struct JobRunner {
    calls: Mutex<Vec<DeploymentCall>>,
    results: Mutex<VecDeque<Result<Value, DeploymentError>>>,
}

#[async_trait]
impl DeploymentJobRunner for JobRunner {
    async fn run(&self, call: DeploymentCall) -> Result<Value, DeploymentError> {
        self.calls.lock().expect("job runner lock").push(call);
        self.results
            .lock()
            .expect("job runner lock")
            .pop_front()
            .unwrap_or_else(|| Ok(json!({"deployment_id":"d-1"})))
    }
}

fn create_call(key: &str) -> DeploymentCall {
    DeploymentCall {
        operation: DeploymentOperation::Create,
        actor: admin(),
        deployment_id: None,
        input: json!({"resource_private_name":"example"}),
        idempotency_key: Some(key.to_owned()),
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; set LMM_DEPLOYMENT_TEST_DATABASE_URL and LMM_DEPLOYMENT_TEST_VALKEY_URL"]
async fn pg_valkey_provider_commits_idempotent_job_results_and_retries_failed_jobs() {
    let database_url = env::var("LMM_DEPLOYMENT_TEST_DATABASE_URL")
        .expect("LMM_DEPLOYMENT_TEST_DATABASE_URL is required");
    let valkey_url = env::var("LMM_DEPLOYMENT_TEST_VALKEY_URL")
        .expect("LMM_DEPLOYMENT_TEST_VALKEY_URL is required");
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    reset_job_tables(&pool).await;
    let runner = Arc::new(JobRunner::default());
    runner.results.lock().expect("job runner lock").extend([
        Err(DeploymentError::Unavailable),
        Ok(json!({"deployment_id":"d-1","status":"requested"})),
    ]);
    let provider = PgValkeyDeploymentProvider::new(
        pool.clone(),
        redis::Client::open(valkey_url).expect("valid Valkey URL"),
        runner.clone(),
    );

    assert_eq!(
        provider.execute(create_call("retry-key")).await,
        Err(DeploymentError::Unavailable)
    );
    let committed = provider
        .execute(create_call("retry-key"))
        .await
        .expect("retry result");
    assert_eq!(
        committed,
        json!({"deployment_id":"d-1","status":"requested"})
    );
    assert_eq!(
        provider
            .execute(create_call("retry-key"))
            .await
            .expect("persisted retry result"),
        committed
    );
    assert_eq!(runner.calls.lock().expect("job runner lock").len(), 2);
    let (state, attempts): (String, i64) = sqlx::query_as(
        "SELECT state, attempts FROM deployment_request_journal WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3",
    )
    .bind(7_i64)
    .bind("create")
    .bind("retry-key")
    .fetch_one(&pool)
    .await
    .expect("durable job journal");
    assert_eq!((state, attempts), ("completed".to_owned(), 2));
}

async fn reset_job_tables(pool: &sqlx::PgPool) {
    for statement in [
        "DROP TABLE IF EXISTS deployment_jobs",
        "DROP TABLE IF EXISTS deployment_request_journal",
        "CREATE TABLE deployment_request_journal (actor_id BIGINT NOT NULL, operation TEXT NOT NULL, idempotency_key TEXT NOT NULL, request_hash TEXT NOT NULL, state TEXT NOT NULL, result JSONB, attempts BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (actor_id, operation, idempotency_key))",
        "CREATE TABLE deployment_jobs (actor_id BIGINT NOT NULL, operation TEXT NOT NULL, idempotency_key TEXT NOT NULL, state TEXT NOT NULL, attempts BIGINT NOT NULL DEFAULT 0, updated_at TIMESTAMPTZ NOT NULL DEFAULT CURRENT_TIMESTAMP, PRIMARY KEY (actor_id, operation, idempotency_key))",
    ] {
        sqlx::query(statement)
            .execute(pool)
            .await
            .expect("isolated deployment schema statement");
    }
}

struct BlockingRunner {
    active: Mutex<usize>,
    max_active: Mutex<usize>,
    started: Notify,
    release: Notify,
}

impl BlockingRunner {
    fn new() -> Self {
        Self {
            active: Mutex::new(0),
            max_active: Mutex::new(0),
            started: Notify::new(),
            release: Notify::new(),
        }
    }
}

#[async_trait]
impl DeploymentJobRunner for BlockingRunner {
    async fn run(&self, _call: DeploymentCall) -> Result<Value, DeploymentError> {
        {
            let mut active = self.active.lock().expect("active runner lock");
            *active += 1;
            let mut maximum = self.max_active.lock().expect("maximum runner lock");
            *maximum = (*maximum).max(*active);
        }
        self.started.notify_one();
        self.release.notified().await;
        *self.active.lock().expect("active runner lock") -= 1;
        Ok(json!({"deployment_id":"d-1"}))
    }
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; set LMM_DEPLOYMENT_TEST_DATABASE_URL and LMM_DEPLOYMENT_TEST_VALKEY_URL"]
async fn pg_valkey_lease_has_a_ttl_and_allows_only_one_live_writer_per_resource() {
    let database_url = env::var("LMM_DEPLOYMENT_TEST_DATABASE_URL")
        .expect("LMM_DEPLOYMENT_TEST_DATABASE_URL is required");
    let valkey_url = env::var("LMM_DEPLOYMENT_TEST_VALKEY_URL")
        .expect("LMM_DEPLOYMENT_TEST_VALKEY_URL is required");
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    reset_job_tables(&pool).await;
    let valkey = redis::Client::open(valkey_url).expect("valid Valkey URL");
    let runner = Arc::new(BlockingRunner::new());
    let provider = PgValkeyDeploymentProvider::new(pool, valkey.clone(), runner.clone())
        .with_lease_ttl(Duration::from_secs(5));
    let first_provider = provider.clone();
    let first = tokio::spawn(async move { first_provider.execute(create_call("first-key")).await });
    runner.started.notified().await;

    let mut connection = valkey
        .get_multiplexed_async_connection()
        .await
        .expect("connect isolated Valkey");
    let ttl: i64 = redis::cmd("PTTL")
        .arg("lmm:deployment:v1:lease:7:new-deployment")
        .query_async(&mut connection)
        .await
        .expect("lease TTL");
    assert!((1..=5_000).contains(&ttl));
    assert_eq!(
        provider.execute(create_call("second-key")).await,
        Err(DeploymentError::InProgress)
    );
    assert_eq!(*runner.max_active.lock().expect("maximum runner lock"), 1);

    runner.release.notify_one();
    assert!(first.await.expect("writer task").is_ok());
}

#[tokio::test]
#[ignore = "requires isolated PostgreSQL 18 and Valkey; set LMM_DEPLOYMENT_TEST_DATABASE_URL and LMM_DEPLOYMENT_TEST_VALKEY_URL"]
async fn expired_writer_lease_recovers_the_durable_job_without_a_second_live_owner() {
    let database_url = env::var("LMM_DEPLOYMENT_TEST_DATABASE_URL")
        .expect("LMM_DEPLOYMENT_TEST_DATABASE_URL is required");
    let valkey_url = env::var("LMM_DEPLOYMENT_TEST_VALKEY_URL")
        .expect("LMM_DEPLOYMENT_TEST_VALKEY_URL is required");
    let pool = PgPoolOptions::new()
        .acquire_timeout(Duration::from_secs(3))
        .connect(&database_url)
        .await
        .expect("connect isolated PostgreSQL");
    reset_job_tables(&pool).await;
    let runner = Arc::new(BlockingRunner::new());
    let provider = PgValkeyDeploymentProvider::new(
        pool.clone(),
        redis::Client::open(valkey_url).expect("valid Valkey URL"),
        runner.clone(),
    )
    .with_lease_ttl(Duration::from_secs(1));
    let crashed_provider = provider.clone();
    let crashed =
        tokio::spawn(async move { crashed_provider.execute(create_call("crash-key")).await });
    runner.started.notified().await;
    crashed.abort();
    sleep(Duration::from_millis(1_100)).await;

    let recovered_provider = provider.clone();
    let recovered =
        tokio::spawn(async move { recovered_provider.execute(create_call("crash-key")).await });
    runner.started.notified().await;
    runner.release.notify_one();
    assert!(recovered.await.expect("recovered writer task").is_ok());
    let (state, attempts): (String, i64) = sqlx::query_as(
        "SELECT state, attempts FROM deployment_request_journal WHERE actor_id = $1 AND operation = $2 AND idempotency_key = $3",
    )
    .bind(7_i64)
    .bind("create")
    .bind("crash-key")
    .fetch_one(&pool)
    .await
    .expect("recovered durable job");
    assert_eq!((state, attempts), ("completed".to_owned(), 2));
}
