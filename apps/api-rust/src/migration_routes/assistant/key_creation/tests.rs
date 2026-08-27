use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
};

use axum::{
    Router,
    body::Body,
    http::{Request, Response, StatusCode, header},
};
use serde_json::{Value, json};
use tower::ServiceExt;

use super::{KEY_MUTATION_BODY_LIMIT_BYTES, domain::*};
use crate::auth::CriticalRateLimitOutcome;
use crate::migration_routes::assistant::tests::support::{
    FixtureStore, FixtureUserRateLimiter, fixture_router, fixture_router_with_user_rate_limiter,
    response_json,
};
use crate::migration_routes::missing_identity_catalog::UserGroupSelection;

fn prepared_action() -> PreparedKeyAction {
    PreparedKeyAction {
        kind: "create_key",
        confirmation_token: "opaque-flow-token".to_owned(),
        requires_confirmation: true,
        expires_in_seconds: 300,
        name: "assistant-created".to_owned(),
        group: "default".to_owned(),
        conversation_id: 91,
        ui_path: "/keys",
    }
}

fn created_key() -> CreatedKey {
    CreatedKey {
        id: 42,
        name: "assistant-created".to_owned(),
        group: "default".to_owned(),
        expired_time: -1,
        card: SecureCardView {
            id: "secure-card-id".to_owned(),
            kind: "api_credential",
            summary: "已创建 API 凭证；仅你可一次性查看和复制".to_owned(),
            created_at: 1_700_000_000,
            expires_at: 1_700_000_300,
            revealable: true,
        },
        privacy_notice: "Only deliberately saved assistant history is retained.",
    }
}

async fn route_json(store: FixtureStore, path: &str, body: Value) -> (StatusCode, Value) {
    let response = fixture_router(store)
        .oneshot(
            Request::post(path)
                .header(header::AUTHORIZATION, "Bearer admin-session")
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(body.to_string()))
                .expect("request"),
        )
        .await
        .expect("response");
    let status = response.status();
    (status, response_json(response).await)
}

async fn raw_route(
    router: Router,
    path: &str,
    authorization: Option<&str>,
    body: String,
) -> Response<Body> {
    let mut request = Request::post(path).header(header::CONTENT_TYPE, "application/json");
    if let Some(authorization) = authorization {
        request = request.header(header::AUTHORIZATION, authorization);
    }
    router
        .oneshot(request.body(Body::from(body)).expect("request"))
        .await
        .expect("response")
}

fn json_padded_to_limit(value: Value) -> String {
    let mut body = value.to_string();
    assert!(body.len() <= KEY_MUTATION_BODY_LIMIT_BYTES);
    body.extend(std::iter::repeat_n(
        ' ',
        KEY_MUTATION_BODY_LIMIT_BYTES - body.len(),
    ));
    body
}

fn assert_key_mutation_headers(response: &Response<Body>) {
    assert!(response.headers().contains_key("auth-version"));
    assert!(
        response
            .headers()
            .get(header::CACHE_CONTROL)
            .and_then(|value| value.to_str().ok())
            .is_some_and(|value| value.contains("no-store"))
    );
}

#[tokio::test]
async fn anonymous_and_personal_token_requests_authenticate_before_body_validation() {
    for path in [
        "/api/assistant/tools/prepare-key",
        "/api/assistant/tools/create-key",
    ] {
        let oversized = "x".repeat(KEY_MUTATION_BODY_LIMIT_BYTES + 1);
        let anonymous = raw_route(
            fixture_router(FixtureStore::default()),
            path,
            None,
            oversized.clone(),
        )
        .await;
        assert_eq!(anonymous.status(), StatusCode::UNAUTHORIZED, "{path}");

        let personal_token = raw_route(
            fixture_router(FixtureStore::default()),
            path,
            Some("Bearer user-token"),
            oversized,
        )
        .await;
        assert_eq!(personal_token.status(), StatusCode::FORBIDDEN, "{path}");
        assert_key_mutation_headers(&personal_token);
        let body = response_json(personal_token).await;
        assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED", "{path}");
    }
}

#[tokio::test]
async fn l0_console_gate_runs_after_json_validation_and_before_rate_limit() {
    for (path, valid_body) in [
        (
            "/api/assistant/tools/prepare-key",
            json!({"name":"key","group":"default"}).to_string(),
        ),
        (
            "/api/assistant/tools/create-key",
            json!({"confirmation_token":"opaque-flow-token"}).to_string(),
        ),
    ] {
        for (body, expected_status) in [
            ("not-json".to_owned(), StatusCode::BAD_REQUEST),
            (valid_body.clone(), StatusCode::NOT_FOUND),
        ] {
            let calls = Arc::new(Mutex::new(Vec::new()));
            let limiter = Arc::new(FixtureUserRateLimiter {
                outcome: Ok(CriticalRateLimitOutcome::Allowed),
                calls: Arc::clone(&calls),
            });
            let response = raw_route(
                fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter),
                path,
                Some("Bearer browser-session"),
                body,
            )
            .await;
            assert_eq!(response.status(), expected_status, "{path}");
            assert_key_mutation_headers(&response);
            assert!(calls.lock().expect("rate limit calls").is_empty(), "{path}");
        }
    }
}

#[tokio::test]
async fn invalid_and_oversized_bodies_do_not_consume_key_mutation_rate_limit() {
    for path in [
        "/api/assistant/tools/prepare-key",
        "/api/assistant/tools/create-key",
    ] {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Allowed),
            calls: Arc::clone(&calls),
        });
        let router = fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter);

        let invalid = raw_route(
            router.clone(),
            path,
            Some("Bearer admin-session"),
            "not-json".to_owned(),
        )
        .await;
        assert_eq!(invalid.status(), StatusCode::BAD_REQUEST, "{path}");
        assert_key_mutation_headers(&invalid);

        let oversized = raw_route(
            router,
            path,
            Some("Bearer admin-session"),
            "x".repeat(KEY_MUTATION_BODY_LIMIT_BYTES + 1),
        )
        .await;
        assert_eq!(oversized.status(), StatusCode::PAYLOAD_TOO_LARGE, "{path}");
        assert_key_mutation_headers(&oversized);
        assert!(calls.lock().expect("rate limit calls").is_empty(), "{path}");
    }
}

#[tokio::test]
async fn exact_16_kib_body_reaches_rate_limit_but_one_extra_byte_does_not() {
    for (path, scope, value) in [
        (
            "/api/assistant/tools/prepare-key",
            "assistant-prepare-key",
            json!({"name":"key","group":"default"}),
        ),
        (
            "/api/assistant/tools/create-key",
            "assistant-create-key",
            json!({"confirmation_token":"opaque-flow-token"}),
        ),
    ] {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 17,
            }),
            calls: Arc::clone(&calls),
        });
        let router = fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter);
        let at_limit = json_padded_to_limit(value);

        let oversized = raw_route(
            router.clone(),
            path,
            Some("Bearer admin-session"),
            format!("{at_limit} "),
        )
        .await;
        assert_eq!(oversized.status(), StatusCode::PAYLOAD_TOO_LARGE, "{path}");
        assert!(calls.lock().expect("rate limit calls").is_empty(), "{path}");

        let accepted = raw_route(router, path, Some("Bearer admin-session"), at_limit).await;
        assert_eq!(accepted.status(), StatusCode::TOO_MANY_REQUESTS, "{path}");
        assert_key_mutation_headers(&accepted);
        assert_eq!(
            calls.lock().expect("rate limit calls").as_slice(),
            &[(scope.to_owned(), 10)],
            "{path}"
        );
    }
}

#[test]
fn real_selectable_group_rejects_empty_and_virtual_auto() {
    assert_eq!(
        RealSelectableGroup::parse(""),
        Err(KeyCreationError::InvalidGroup)
    );
    assert_eq!(
        RealSelectableGroup::parse(" auto "),
        Err(KeyCreationError::InvalidGroup)
    );
    assert_eq!(
        RealSelectableGroup::parse("vip").map(|group| group.into_inner()),
        Ok("vip".to_owned())
    );
}

#[test]
fn selectable_projection_never_exposes_automatic_or_malicious_auto() {
    let options = selectable_group_options(UserGroupSelection {
        selectable: BTreeMap::from([
            ("auto".to_owned(), "must not leak".to_owned()),
            ("vip".to_owned(), "VIP".to_owned()),
        ]),
        automatic: vec!["vip".to_owned()],
    });
    assert_eq!(
        options,
        vec![AssistantKeyGroupOption {
            id: "vip".to_owned(),
            description: "VIP".to_owned(),
            warning: None,
        }]
    );
}

#[tokio::test]
async fn prepare_route_returns_only_an_opaque_session_bound_action() {
    let store = FixtureStore::default()
        .with_key_groups(vec![AssistantKeyGroupOption::selectable(
            "default",
            "默认分组",
        )])
        .with_prepare_result(Ok(prepared_action()));
    let calls = store.prepare_key_calls();
    let (status, body) = route_json(
        store,
        "/api/assistant/tools/prepare-key",
        json!({
            "name": "  assistant-created  ",
            "group": "default",
            "conversation_id": 91,
        }),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"], json!(prepared_action()));
    assert!(!body.to_string().contains("sk-"));
    let calls = calls.lock().expect("prepare key call lock");
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].user_id, 10);
    assert_eq!(calls[0].session_id, "assistant-session");
    assert_eq!(calls[0].draft.name, "assistant-created");
    assert_eq!(calls[0].draft.group.as_str(), "default");
    assert_eq!(calls[0].draft.conversation_id, 91);
}

#[tokio::test]
async fn prepare_route_rejects_virtual_auto_and_empty_authoritative_options() {
    for (options, group) in [
        (
            vec![AssistantKeyGroupOption::selectable(
                "auto",
                "must never be usable",
            )],
            "auto",
        ),
        (Vec::new(), "default"),
    ] {
        let store = FixtureStore::default().with_key_groups(options);
        let calls = store.prepare_key_calls();
        let (status, body) = route_json(
            store,
            "/api/assistant/tools/prepare-key",
            json!({"name":"key","group":group}),
        )
        .await;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], "ASSISTANT_INVALID_GROUP");
        assert!(calls.lock().expect("prepare key call lock").is_empty());
    }
}

#[tokio::test]
async fn prepare_route_strictly_rejects_client_confirmation_fields() {
    let store = FixtureStore::default().with_key_groups(vec![AssistantKeyGroupOption::selectable(
        "default",
        "默认分组",
    )]);
    let calls = store.prepare_key_calls();
    let (status, _) = route_json(
        store,
        "/api/assistant/tools/prepare-key",
        json!({
            "name":"key",
            "group":"default",
            "confirmed":true,
            "confirmation_token":"client-token",
        }),
    )
    .await;

    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert!(calls.lock().expect("prepare key call lock").is_empty());
}

#[tokio::test]
async fn confirm_route_accepts_only_opaque_token_and_forwards_two_factor_code() {
    let store = FixtureStore::default().with_confirm_result(Ok(created_key()));
    let calls = store.confirm_key_calls();
    let (status, body) = route_json(
        store,
        "/api/assistant/tools/create-key",
        json!({
            "confirmation_token":"opaque-flow-token",
            "two_factor_code":" 123456 ",
        }),
    )
    .await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"], json!(created_key()));
    assert!(!body.to_string().contains("api_key"));
    assert!(!body.to_string().contains("sk-"));
    let calls = calls.lock().expect("confirm key call lock");
    assert_eq!(calls.len(), 1);
    assert_eq!(calls[0].actor_id, 10);
    assert_eq!(calls[0].session_id, "assistant-session");
    assert_eq!(calls[0].expected_session_version, 1);
    assert_eq!(calls[0].expected_user_auth_version, 1);
    assert_eq!(calls[0].token.expose(), "opaque-flow-token");
    assert_eq!(calls[0].two_factor_code, "123456");
}

#[tokio::test]
async fn confirm_route_rejects_client_mutable_draft_fields_before_repository() {
    for extra in [
        json!({"name":"tampered"}),
        json!({"group":"auto"}),
        json!({"confirmed":true}),
    ] {
        let store = FixtureStore::default().with_confirm_result(Ok(created_key()));
        let calls = store.confirm_key_calls();
        let mut body = json!({"confirmation_token":"opaque-flow-token"});
        body.as_object_mut()
            .expect("confirmation body object")
            .extend(extra.as_object().expect("extra object").clone());
        let (status, _) = route_json(store, "/api/assistant/tools/create-key", body).await;

        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert!(calls.lock().expect("confirm key call lock").is_empty());
    }
}

#[tokio::test]
async fn confirm_route_preserves_database_failures_as_internal_errors() {
    let store = FixtureStore::default().with_confirm_result(Err(KeyCreationError::Unavailable(
        "database unavailable".to_owned(),
    )));
    let (status, body) = route_json(
        store,
        "/api/assistant/tools/create-key",
        json!({"confirmation_token":"opaque-flow-token"}),
    )
    .await;

    assert_eq!(status, StatusCode::INTERNAL_SERVER_ERROR);
    assert_ne!(body["code"], "ASSISTANT_KEY_CONFIRMATION_INVALID");
}

#[tokio::test]
async fn confirm_route_preserves_invalid_replay_and_two_factor_errors() {
    for (error, code) in [
        (
            KeyCreationError::InvalidConfirmation,
            "ASSISTANT_KEY_CONFIRMATION_INVALID",
        ),
        (
            KeyCreationError::TwoFactorInvalid,
            "ASSISTANT_TWO_FACTOR_INVALID",
        ),
    ] {
        let store = FixtureStore::default().with_confirm_result(Err(error));
        let (status, body) = route_json(
            store,
            "/api/assistant/tools/create-key",
            json!({"confirmation_token":"opaque-flow-token"}),
        )
        .await;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], code);
    }
}
