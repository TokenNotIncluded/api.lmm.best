use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
    response::Response,
};
use lmm_api_rs::routes::media_tasks::{
    MediaTaskHttpState, MediaTaskOperation, MediaTaskService, media_provider_task_router,
    media_task_router,
};
use serde_json::{Value, json};
use tower::ServiceExt;

#[derive(Clone, Debug, Eq, PartialEq)]
struct Call {
    operation: MediaTaskOperation,
    path: String,
    body: Vec<u8>,
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
struct Effects {
    task_rows: usize,
    consume_logs: usize,
    counter_updates: usize,
    valkey_writes: usize,
}

#[derive(Default)]
struct StubTaskService {
    calls: Mutex<Vec<Call>>,
    effects: Mutex<Effects>,
}

#[async_trait]
impl MediaTaskService for StubTaskService {
    async fn protected(
        &self,
        operation: MediaTaskOperation,
        request: axum::extract::Request,
    ) -> Response {
        if request.headers().get(header::AUTHORIZATION).is_none() {
            return json_response(
                StatusCode::UNAUTHORIZED,
                json!({"error":{"message":"Invalid token","type":"new_api_error","code":""}}),
            );
        }
        let (parts, body) = request.into_parts();
        let body = to_bytes(body, usize::MAX)
            .await
            .expect("stub body")
            .to_vec();
        self.calls.lock().expect("calls lock").push(Call {
            operation,
            path: parts.uri.path().to_owned(),
            body: body.clone(),
        });
        match operation {
            MediaTaskOperation::Submit("IMAGINE") if body == b"{}" => json_response(
                StatusCode::BAD_REQUEST,
                json!({"code":4,"description":"prompt_is_required ","type":"upstream_error"}),
            ),
            MediaTaskOperation::Submit(_)
                if body
                    .windows(b"timeout".len())
                    .any(|part| part == b"timeout") =>
            {
                json_response(
                    StatusCode::BAD_REQUEST,
                    json!({"code":5,"description":"do_request_failed ","type":"upstream_error"}),
                )
            }
            MediaTaskOperation::Submit(_) => {
                let replay = body.windows(b"replay".len()).any(|part| part == b"replay");
                let mut effects = self.effects.lock().expect("effects lock");
                effects.task_rows += 1;
                effects.consume_logs += 1;
                effects.counter_updates += 2;
                // Midjourney task writes deliberately do not populate Valkey.
                drop(effects);
                json_response(
                    StatusCode::OK,
                    if replay {
                        json!({"code":1,"description":"exists","result":"stub-job-1","properties":{"status":"SUCCESS","imageUrl":"http://stub.invalid/image.png"}})
                    } else {
                        json!({"code":1,"description":"ok","result":"stub-job-1"})
                    },
                )
            }
            MediaTaskOperation::Fetch
            | MediaTaskOperation::ImageSeed
            | MediaTaskOperation::ListByCondition
            | MediaTaskOperation::SunoSubmit
            | MediaTaskOperation::SunoFetch
            | MediaTaskOperation::SunoFetchById
            | MediaTaskOperation::KlingImageToVideo
            | MediaTaskOperation::KlingImageToVideoFetch
            | MediaTaskOperation::KlingTextToVideo
            | MediaTaskOperation::KlingTextToVideoFetch
            | MediaTaskOperation::JimengSubmit => json_response(
                StatusCode::OK,
                json!({"code":1,"description":"ok","result":[]}),
            ),
        }
    }

    async fn public_image(&self, task_id: String, _request: axum::extract::Request) -> Response {
        match task_id.as_str() {
            "stub-image-job" => Response::builder()
                .status(StatusCode::OK)
                .header(header::CONTENT_TYPE, "image/png")
                .body(Body::from(b"\x89PNG\r\n\x1a\n".to_vec()))
                .expect("image response"),
            "upstream-failure" => json_response(
                StatusCode::SERVICE_UNAVAILABLE,
                json!({"error":"temporary image failure"}),
            ),
            _ => json_response(
                StatusCode::BAD_REQUEST,
                json!({"error":"midjourney_task_not_found"}),
            ),
        }
    }
}

fn json_response(status: StatusCode, body: Value) -> Response {
    Response::builder()
        .status(status)
        .header(header::CONTENT_TYPE, "application/json; charset=utf-8")
        .body(Body::from(body.to_string()))
        .expect("JSON response")
}

fn app(service: Arc<StubTaskService>) -> axum::Router {
    media_task_router(MediaTaskHttpState::new(service))
}

async fn call(router: &axum::Router, method: &str, path: &str, body: Body) -> Response {
    router
        .clone()
        .oneshot(
            Request::builder()
                .method(method)
                .uri(path)
                .header(header::AUTHORIZATION, "Bearer oracle-task-token")
                .body(body)
                .expect("request"),
        )
        .await
        .expect("router response")
}

#[tokio::test]
async fn public_image_is_unauthenticated_and_preserves_binary_response_without_effects() {
    let service = Arc::new(StubTaskService::default());
    let response = app(Arc::clone(&service))
        .oneshot(
            Request::builder()
                .uri("/mj/image/stub-image-job")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()[header::CONTENT_TYPE], "image/png");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("image")
            .as_ref(),
        b"\x89PNG\r\n\x1a\n"
    );
    assert_eq!(
        *service.effects.lock().expect("effects lock"),
        Effects::default()
    );
}

#[tokio::test]
async fn protected_routes_require_auth_before_any_task_effect() {
    let service = Arc::new(StubTaskService::default());
    let response = app(Arc::clone(&service))
        .oneshot(
            Request::builder()
                .uri("/mj/task/stub-job-1/fetch")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(service.calls.lock().expect("calls lock").is_empty());
    assert_eq!(
        *service.effects.lock().expect("effects lock"),
        Effects::default()
    );
}

#[tokio::test]
async fn malformed_bearer_credentials_are_rejected_by_the_http_boundary() {
    let service = Arc::new(StubTaskService::default());
    let router = app(Arc::clone(&service));
    for authorization in [None, Some("Basic oracle-task-token"), Some("Bearer ")] {
        let mut request = Request::builder().method("POST").uri("/mj/submit/imagine");
        if let Some(authorization) = authorization {
            request = request.header(header::AUTHORIZATION, authorization);
        }
        let response = router
            .clone()
            .oneshot(
                request
                    .body(Body::from(r#"{"prompt":"ok"}"#))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(
            to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body")
                .as_ref(),
            br#"{"error":{"message":"Invalid token","type":"new_api_error","code":""}}"#,
        );
    }
    assert!(service.calls.lock().expect("calls lock").is_empty());
    assert_eq!(
        *service.effects.lock().expect("effects lock"),
        Effects::default()
    );
}

#[tokio::test]
async fn imagine_input_and_upstream_failure_have_no_postgres_or_valkey_effects() {
    let service = Arc::new(StubTaskService::default());
    let router = app(Arc::clone(&service));
    for body in [Body::from("{}"), Body::from(r#"{"prompt":"timeout"}"#)] {
        let response = call(&router, "POST", "/mj/submit/imagine", body).await;
        assert_eq!(response.status(), StatusCode::BAD_REQUEST);
    }
    assert_eq!(
        *service.effects.lock().expect("effects lock"),
        Effects::default()
    );
}

#[tokio::test]
async fn replay_is_a_success_and_each_concurrent_submit_records_its_own_pg_effects() {
    let service = Arc::new(StubTaskService::default());
    let router = app(Arc::clone(&service));
    let first = call(
        &router,
        "POST",
        "/mj/submit/imagine",
        Body::from(r#"{"prompt":"replay"}"#),
    );
    let second = call(
        &router,
        "POST",
        "/mj/submit/imagine",
        Body::from(r#"{"prompt":"replay"}"#),
    );
    let (first, second) = tokio::join!(first, second);
    for response in [first, second] {
        let body: Value = serde_json::from_slice(
            &to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("body"),
        )
        .expect("JSON");
        assert_eq!(body["code"], 1);
        assert_eq!(body["description"], "exists");
    }
    assert_eq!(
        *service.effects.lock().expect("effects lock"),
        Effects {
            task_rows: 2,
            consume_logs: 2,
            counter_updates: 4,
            valkey_writes: 0
        }
    );
}

#[tokio::test]
async fn every_selected_submit_and_task_route_reaches_its_exact_operation() {
    let service = Arc::new(StubTaskService::default());
    let router = app(Arc::clone(&service));
    for path in [
        "/mj/insight-face/swap",
        "/mj/submit/action",
        "/mj/submit/blend",
        "/mj/submit/change",
        "/mj/submit/describe",
        "/mj/submit/edits",
        "/mj/submit/imagine",
        "/mj/submit/modal",
        "/mj/submit/shorten",
        "/mj/submit/simple-change",
        "/mj/submit/upload-discord-images",
        "/mj/submit/video",
    ] {
        assert_eq!(
            call(&router, "POST", path, Body::from(r#"{"prompt":"ok"}"#))
                .await
                .status(),
            StatusCode::OK,
            "{path}"
        );
    }
    for path in [
        "/mj/task/stub-job/fetch",
        "/mj/task/stub-job/image-seed",
        "/mj/task/list-by-condition",
        "/suno/fetch",
        "/suno/fetch/suno-job",
        "/suno/submit/extend",
        "/kling/v1/videos/image2video",
        "/kling/v1/videos/text2video",
        "/jimeng/",
    ] {
        let method = if (path.ends_with("/fetch") && path != "/suno/fetch")
            || path.ends_with("image-seed")
            || path.ends_with("suno-job")
        {
            "GET"
        } else {
            "POST"
        };
        assert_eq!(
            call(&router, method, path, Body::from("{}")).await.status(),
            StatusCode::OK,
            "{path}"
        );
    }
    let calls = service.calls.lock().expect("calls lock");
    assert_eq!(calls.len(), 21);
    assert_eq!(calls[12].operation, MediaTaskOperation::Fetch);
    assert_eq!(calls[13].operation, MediaTaskOperation::ImageSeed);
    assert_eq!(calls[14].operation, MediaTaskOperation::ListByCondition);
    assert_eq!(calls[15].operation, MediaTaskOperation::SunoFetch);
    assert_eq!(calls[16].operation, MediaTaskOperation::SunoFetchById);
    assert_eq!(calls[17].operation, MediaTaskOperation::SunoSubmit);
    assert_eq!(calls[18].operation, MediaTaskOperation::KlingImageToVideo);
    assert_eq!(calls[19].operation, MediaTaskOperation::KlingTextToVideo);
    assert_eq!(calls[20].operation, MediaTaskOperation::JimengSubmit);
}

#[tokio::test]
async fn media_provider_task_router_mounts_suno_kling_and_jimeng_without_midjourney() {
    let service = Arc::new(StubTaskService::default());
    let router = media_provider_task_router(MediaTaskHttpState::new(
        Arc::clone(&service) as Arc<dyn MediaTaskService>
    ));
    for path in [
        "/suno/fetch",
        "/suno/fetch/suno-job",
        "/suno/submit/extend",
        "/kling/v1/videos/image2video",
        "/kling/v1/videos/text2video",
        "/jimeng/",
    ] {
        let method = if path.ends_with("suno-job") {
            "GET"
        } else {
            "POST"
        };
        assert_eq!(
            call(&router, method, path, Body::from("{}")).await.status(),
            StatusCode::OK,
            "{path}"
        );
    }
    let midjourney = call(
        &router,
        "POST",
        "/mj/submit/imagine",
        Body::from(r#"{"prompt":"ok"}"#),
    )
    .await
    .status();
    assert_eq!(midjourney, StatusCode::NOT_FOUND);
    assert_eq!(service.calls.lock().expect("calls lock").len(), 6);
}
