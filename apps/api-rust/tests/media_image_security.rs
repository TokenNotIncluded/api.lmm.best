use std::sync::{Arc, Mutex};

use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::{HeaderMap, HeaderValue, Request, StatusCode},
};
use hmac::{Hmac, Mac};
use lmm_api_rs::routes::{
    media_midjourney::{
        BufferedJsonReply, ImageReply, MidjourneyBackend, MidjourneyFailure, MidjourneyHttpState,
        MidjourneyIdentity, StoredImage, SubmitReply, TaskEffect, media_midjourney_router,
    },
    media_tasks::{MediaTaskHttpState, MidjourneyMediaTaskService, media_task_router},
};
use serde_json::Value;
use sha2::Sha256;
use tower::ServiceExt;

const SECRET: &[u8] = b"image-signing-contract-secret";
const TASK_ID: &str = "task-owned-by-a";

type HmacSha256 = Hmac<Sha256>;

#[derive(Default)]
struct ImageBackend {
    lookups: Mutex<Vec<i64>>,
    fetches: Mutex<usize>,
}

#[async_trait]
impl MidjourneyBackend for ImageBackend {
    async fn authenticate(
        &self,
        _headers: &HeaderMap,
        _client_ip: Option<std::net::IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure> {
        Err(MidjourneyFailure::Unauthorized)
    }

    async fn submit(
        &self,
        _identity: &MidjourneyIdentity,
        _mode: &str,
        _operation: &str,
        _headers: &HeaderMap,
        _body: Value,
    ) -> Result<SubmitReply, MidjourneyFailure> {
        Err(MidjourneyFailure::Upstream)
    }

    async fn record_submit(
        &self,
        _identity: &MidjourneyIdentity,
        _effect: TaskEffect,
    ) -> Result<(), MidjourneyFailure> {
        Err(MidjourneyFailure::Storage)
    }

    async fn task_read(
        &self,
        _identity: &MidjourneyIdentity,
        _operation: &str,
        _task_id: &str,
        _headers: &HeaderMap,
        _body: Option<Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        Err(MidjourneyFailure::NotFound)
    }

    async fn image_for(&self, _task_id: &str) -> Result<StoredImage, MidjourneyFailure> {
        panic!("unscoped image lookup must never be used by an HTTP route")
    }

    async fn image_for_owned(
        &self,
        user_id: i64,
        task_id: &str,
    ) -> Result<StoredImage, MidjourneyFailure> {
        self.lookups.lock().expect("lookup lock").push(user_id);
        if user_id != 101 || task_id != TASK_ID {
            return Err(MidjourneyFailure::NotFound);
        }
        Ok(StoredImage {
            url: "https://images.example.test/task.png".to_owned(),
        })
    }

    async fn fetch_image(&self, _url: &str) -> Result<ImageReply, MidjourneyFailure> {
        *self.fetches.lock().expect("fetch lock") += 1;
        Ok(ImageReply::Stream {
            content_type: HeaderValue::from_static("image/png"),
            body: Body::from("fixture-image"),
        })
    }
}

fn signed_path(prefix: &str, user_id: i64, task_id: &str) -> String {
    let mut mac = HmacSha256::new_from_slice(SECRET).expect("HMAC key");
    mac.update(format!("midjourney-image-v1:{user_id}:{task_id}").as_bytes());
    format!(
        "{prefix}/{task_id}?uid={user_id}&sig={}",
        hex::encode(mac.finalize().into_bytes())
    )
}

fn dynamic_app(backend: Arc<ImageBackend>) -> axum::Router {
    media_midjourney_router(MidjourneyHttpState::new(backend).with_image_signing_secret(SECRET))
}

#[tokio::test]
async fn dynamic_image_route_rejects_unsigned_and_invalid_signatures() {
    let backend = Arc::new(ImageBackend::default());
    let app = dynamic_app(Arc::clone(&backend));
    for path in [
        format!("/proxy/mj/image/{TASK_ID}"),
        format!("/proxy/mj/image/{TASK_ID}?uid=101&sig=00"),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri(path)
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        assert_eq!(backend.lookups.lock().expect("lookup lock").len(), 0);
        assert_eq!(*backend.fetches.lock().expect("fetch lock"), 0);
    }
}

#[tokio::test]
async fn dynamic_image_route_rejects_a_valid_signature_for_the_wrong_owner() {
    let backend = Arc::new(ImageBackend::default());
    let response = dynamic_app(Arc::clone(&backend))
        .oneshot(
            Request::builder()
                .uri(signed_path("/proxy/mj/image", 202, TASK_ID))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::NOT_FOUND);
    assert_eq!(
        backend.lookups.lock().expect("lookup lock").as_slice(),
        [202]
    );
    assert_eq!(*backend.fetches.lock().expect("fetch lock"), 0);
}

#[tokio::test]
async fn dynamic_image_route_allows_a_valid_owner_signature() {
    let backend = Arc::new(ImageBackend::default());
    let response = dynamic_app(Arc::clone(&backend))
        .oneshot(
            Request::builder()
                .uri(signed_path("/proxy/mj/image", 101, TASK_ID))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");

    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["content-type"], "image/png");
    assert_eq!(
        to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("image body"),
        b"fixture-image".as_slice()
    );
    assert_eq!(
        backend.lookups.lock().expect("lookup lock").as_slice(),
        [101]
    );
    assert_eq!(*backend.fetches.lock().expect("fetch lock"), 1);
}

#[tokio::test]
async fn static_image_route_uses_the_same_signature_and_owner_boundary() {
    let backend = Arc::new(ImageBackend::default());
    let service =
        MidjourneyMediaTaskService::new(Arc::clone(&backend)).with_image_signing_secret(SECRET);
    let app = media_task_router(MediaTaskHttpState::new(Arc::new(service)));

    let unsigned = app
        .clone()
        .oneshot(
            Request::builder()
                .uri(format!("/mj/image/{TASK_ID}"))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(unsigned.status(), StatusCode::UNAUTHORIZED);

    let valid = app
        .oneshot(
            Request::builder()
                .uri(signed_path("/mj/image", 101, TASK_ID))
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    assert_eq!(valid.status(), StatusCode::OK);
    assert_eq!(
        to_bytes(valid.into_body(), usize::MAX)
            .await
            .expect("image body"),
        b"fixture-image".as_slice()
    );
}
