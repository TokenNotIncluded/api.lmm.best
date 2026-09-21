use std::{collections::HashMap, sync::Arc};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    extract::Request,
    http::{HeaderValue, StatusCode, header},
};
use lmm_api_rs::{
    RequestContext,
    routes::kling_task_reads::{
        KlingTask, KlingTaskAccess, KlingTaskProperties, KlingTaskReadFailure,
        KlingTaskReadRequest, KlingTaskReadService, KlingTaskReadState, router,
    },
};
use serde_json::{Value, json};
use tokio::sync::Mutex;
use tower::ServiceExt;

struct StubService {
    access: KlingTaskAccess,
    tasks: Mutex<HashMap<(i64, String), KlingTask>>,
    auth_calls: Mutex<usize>,
    origin_calls: Mutex<Vec<(i64, String)>>,
    task_calls: Mutex<Vec<(i64, String)>>,
    channel_calls: Mutex<Vec<i64>>,
}

impl StubService {
    fn new(access: KlingTaskAccess, tasks: impl IntoIterator<Item = KlingTask>) -> Self {
        Self {
            access,
            tasks: Mutex::new(
                tasks
                    .into_iter()
                    .map(|task| ((task.user_id, task.task_id.clone()), task))
                    .collect(),
            ),
            auth_calls: Mutex::new(0),
            origin_calls: Mutex::new(Vec::new()),
            task_calls: Mutex::new(Vec::new()),
            channel_calls: Mutex::new(Vec::new()),
        }
    }
}

#[async_trait]
impl KlingTaskReadService for StubService {
    async fn authenticate(
        &self,
        request: &KlingTaskReadRequest,
    ) -> Result<KlingTaskAccess, KlingTaskReadFailure> {
        *self.auth_calls.lock().await += 1;
        let accepted = request.authorization() == Some("Bearer good");
        accepted
            .then(|| self.access.clone())
            .ok_or(KlingTaskReadFailure::ConcealedNotFound)
    }

    async fn validate_specific_channel(
        &self,
        channel_id: i64,
        _request: &KlingTaskReadRequest,
    ) -> Result<(), KlingTaskReadFailure> {
        self.channel_calls.lock().await.push(channel_id);
        Ok(())
    }

    async fn origin_model_for_owned_task(&self, user_id: i64, task_id: &str) -> Option<String> {
        self.origin_calls
            .lock()
            .await
            .push((user_id, task_id.to_owned()));
        self.tasks
            .lock()
            .await
            .get(&(user_id, task_id.to_owned()))
            .map(|task| task.properties.origin_model_name.clone())
    }

    async fn owned_task(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<Option<KlingTask>, KlingTaskReadFailure> {
        self.task_calls
            .lock()
            .await
            .push((user_id, task_id.to_owned()));
        Ok(self
            .tasks
            .lock()
            .await
            .get(&(user_id, task_id.to_owned()))
            .cloned())
    }
}

fn stored_task(user_id: i64, task_id: &str) -> KlingTask {
    KlingTask {
        id: 91,
        created_at: 1_700_000_001,
        updated_at: 1_700_000_002,
        task_id: task_id.to_owned(),
        platform: "foreign-platform-is-not-filtered".to_owned(),
        user_id,
        group: "default".to_owned(),
        channel_id: 17,
        quota: 42,
        action: "foreign-action-is-not-filtered".to_owned(),
        status: "SUCCESS".to_owned(),
        fail_reason: String::new(),
        result_url: Some("https://stored.invalid/video.mp4".to_owned()),
        submit_time: 1_700_000_003,
        start_time: 1_700_000_004,
        finish_time: 1_700_000_005,
        progress: "100%".to_owned(),
        properties: KlingTaskProperties {
            input: "stored input".to_owned(),
            upstream_model_name: "upstream-kling".to_owned(),
            origin_model_name: "kling-v1".to_owned(),
        },
        data: json!({"task_status":"succeed"}),
    }
}

fn app(service: Arc<StubService>) -> axum::Router {
    router(KlingTaskReadState::new(service))
}

fn request(path: &str, authorization: Option<&str>, body: &'static str) -> Request {
    let mut builder = Request::builder().uri(path);
    if let Some(authorization) = authorization {
        builder = builder.header(header::AUTHORIZATION, authorization);
    }
    let mut request = builder.body(Body::from(body)).expect("request");
    request.extensions_mut().insert(RequestContext {
        request_id: "kling-read-request".to_owned(),
        client_ip: Some("192.0.2.4".parse().expect("fixture IP")),
    });
    request
}

async fn body(response: axum::response::Response) -> Vec<u8> {
    to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("response body")
        .to_vec()
}

#[tokio::test]
async fn both_kling_aliases_return_the_same_owned_local_task_shape() {
    let service = Arc::new(StubService::new(
        KlingTaskAccess::new(7),
        [stored_task(7, "task-local")],
    ));
    let router = app(Arc::clone(&service));

    for path in [
        "/kling/v1/videos/image2video/task-local",
        "/kling/v1/videos/text2video/task-local",
    ] {
        let response = router
            .clone()
            .oneshot(request(path, Some("Bearer good"), ""))
            .await
            .expect("router response");
        assert_eq!(response.status(), StatusCode::OK, "{path}");
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8",
            "{path}"
        );
        assert_eq!(
            String::from_utf8(body(response).await).expect("JSON UTF-8"),
            r#"{"code":"success","message":"","data":{"id":91,"created_at":1700000001,"updated_at":1700000002,"task_id":"task-local","platform":"foreign-platform-is-not-filtered","user_id":7,"group":"default","channel_id":17,"quota":42,"action":"foreign-action-is-not-filtered","status":"SUCCESS","fail_reason":"","result_url":"https://stored.invalid/video.mp4","submit_time":1700000003,"start_time":1700000004,"finish_time":1700000005,"progress":"100%","properties":{"input":"stored input","upstream_model_name":"upstream-kling","origin_model_name":"kling-v1"},"data":{"task_status":"succeed"}}}"#,
            "{path}"
        );
    }

    assert_eq!(*service.auth_calls.lock().await, 2);
    assert!(service.origin_calls.lock().await.is_empty());
    assert_eq!(
        service.task_calls.lock().await.as_slice(),
        &[(7, "task-local".to_owned()), (7, "task-local".to_owned())]
    );
    assert!(service.channel_calls.lock().await.is_empty());
}

#[tokio::test]
async fn malformed_json_still_authenticates_before_the_distributor_error() {
    let service = Arc::new(StubService::new(
        KlingTaskAccess::new(7),
        [stored_task(7, "task-local")],
    ));
    let router = app(Arc::clone(&service));

    let mut anonymous = request("/kling/v1/videos/image2video/task-local", None, "{");
    anonymous.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    let response = router
        .clone()
        .oneshot(anonymous)
        .await
        .expect("anonymous response");
    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(body(response).await, br#"{"message":"Not Found"}"#);
    assert_eq!(*service.auth_calls.lock().await, 1);
    assert!(service.task_calls.lock().await.is_empty());

    let mut authenticated = request(
        "/kling/v1/videos/image2video/task-local",
        Some("Bearer good"),
        "{",
    );
    authenticated.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json"),
    );
    let response = router
        .oneshot(authenticated)
        .await
        .expect("authenticated response");
    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        serde_json::from_slice::<Value>(&body(response).await).expect("error JSON"),
        json!({
            "error": {
                "code": "",
                "message": "Invalid request: Invalid request: invalid JSON request body (request id: kling-read-request)",
                "type": "new_api_error"
            }
        })
    );
    assert_eq!(*service.auth_calls.lock().await, 2);
    assert!(service.task_calls.lock().await.is_empty());
}

#[tokio::test]
async fn task_lookup_is_user_scoped_and_hides_a_foreign_row_as_not_existing() {
    let service = Arc::new(StubService::new(
        KlingTaskAccess::new(7),
        [stored_task(8, "task-shared")],
    ));
    let response = app(Arc::clone(&service))
        .oneshot(request(
            "/kling/v1/videos/text2video/task-shared",
            Some("Bearer good"),
            "",
        ))
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    assert_eq!(
        body(response).await,
        br#"{"code":"task_not_exist","message":"task_not_exist","data":null}"#
    );
    assert_eq!(
        service.task_calls.lock().await.as_slice(),
        &[(7, "task-shared".to_owned())]
    );
}

#[tokio::test]
async fn model_limited_tokens_use_the_owned_origin_model_before_the_final_lookup() {
    let service = Arc::new(StubService::new(
        KlingTaskAccess::new(7).with_model_limits(vec!["different-model".to_owned()]),
        [stored_task(7, "task-local")],
    ));
    let response = app(Arc::clone(&service))
        .oneshot(request(
            "/kling/v1/videos/image2video/task-local",
            Some("Bearer good"),
            "",
        ))
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        serde_json::from_slice::<Value>(&body(response).await).expect("error JSON"),
        json!({
            "error": {
                "code": "",
                "message": "This token has no access to model kling-v1 (request id: kling-read-request)",
                "type": "new_api_error"
            }
        })
    );
    assert_eq!(
        service.origin_calls.lock().await.as_slice(),
        &[(7, "task-local".to_owned())]
    );
    assert!(service.task_calls.lock().await.is_empty());
}

#[tokio::test]
async fn a_specific_channel_is_validated_before_the_owned_task_read() {
    let service = Arc::new(StubService::new(
        KlingTaskAccess::new(7)
            .with_model_limits(vec!["different-model".to_owned()])
            .with_specific_channel(23),
        [stored_task(7, "task-local")],
    ));
    let response = app(Arc::clone(&service))
        .oneshot(request(
            "/kling/v1/videos/text2video/task-local",
            Some("Bearer good"),
            "",
        ))
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(service.channel_calls.lock().await.as_slice(), &[23]);
    assert!(service.origin_calls.lock().await.is_empty());
    assert_eq!(
        service.task_calls.lock().await.as_slice(),
        &[(7, "task-local".to_owned())]
    );
}
