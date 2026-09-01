use std::{
    collections::BTreeMap,
    error::Error,
    io,
    sync::{Arc, Mutex, MutexGuard},
};

use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, Response, StatusCode, header},
};
use serde_json::{Value, json};
use tower::ServiceExt;

use super::{KEY_MUTATION_BODY_LIMIT_BYTES, domain::*};
use crate::auth::CriticalRateLimitOutcome;
use crate::routes::assistant::tests::support::{
    FixtureStore, FixtureUserRateLimiter, fixture_router, fixture_router_with_user_rate_limiter,
};
use crate::routes::identity_catalog::UserGroupSelection;

type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

fn test_error(message: impl Into<String>) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message.into()))
}

fn required<T>(value: Option<T>, context: impl Into<String>) -> TestResult<T> {
    value.ok_or_else(|| test_error(context))
}

fn lock_recover<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    match mutex.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    }
}

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

fn build_post_request(
    path: &str,
    authorization: Option<&str>,
    body: String,
) -> TestResult<Request<Body>> {
    let mut request = Request::post(path).header(header::CONTENT_TYPE, "application/json");
    if let Some(authorization) = authorization {
        request = request.header(header::AUTHORIZATION, authorization);
    }
    request.body(Body::from(body)).map_err(|error| {
        test_error(format!(
            "build POST request for URI `{path}` with JSON body: {error}"
        ))
    })
}

async fn json_response(response: Response<Body>, path: &str) -> TestResult<Value> {
    let bytes = to_bytes(response.into_body(), usize::MAX)
        .await
        .map_err(|error| test_error(format!("read response body for URI `{path}`: {error}")))?;
    serde_json::from_slice(&bytes)
        .map_err(|error| test_error(format!("decode response JSON for URI `{path}`: {error}")))
}

async fn route_json(
    store: FixtureStore,
    path: &str,
    body: Value,
) -> TestResult<(StatusCode, Value)> {
    let response = fixture_router(store)?
        .oneshot(build_post_request(
            path,
            Some("Bearer admin-session"),
            body.to_string(),
        )?)
        .await
        .map_err(|error| test_error(format!("send POST request to URI `{path}`: {error}")))?;
    let status = response.status();
    Ok((status, json_response(response, path).await?))
}

async fn raw_route(
    router: Router,
    path: &str,
    authorization: Option<&str>,
    body: String,
) -> TestResult<Response<Body>> {
    router
        .oneshot(build_post_request(path, authorization, body)?)
        .await
        .map_err(|error| test_error(format!("send POST request to URI `{path}`: {error}")))
}

fn json_padded_to_limit(value: Value) -> TestResult<String> {
    let mut body = value.to_string();
    let padding = KEY_MUTATION_BODY_LIMIT_BYTES
        .checked_sub(body.len())
        .ok_or_else(|| {
            test_error(format!(
                "JSON body is {} bytes, exceeding the {KEY_MUTATION_BODY_LIMIT_BYTES}-byte limit",
                body.len()
            ))
        })?;
    body.extend(std::iter::repeat_n(' ', padding));
    Ok(body)
}

fn assert_key_mutation_headers(response: &Response<Body>, path: &str) -> TestResult {
    required(
        response.headers().get("auth-version"),
        format!("response for URI `{path}` is missing auth-version"),
    )?;
    let cache_control = required(
        response.headers().get(header::CACHE_CONTROL),
        format!("response for URI `{path}` is missing cache-control"),
    )?
    .to_str()
    .map_err(|error| {
        test_error(format!(
            "response cache-control for URI `{path}` is not text: {error}"
        ))
    })?;
    assert!(cache_control.contains("no-store"), "{path}");
    Ok(())
}

#[tokio::test]
async fn anonymous_and_personal_token_requests_authenticate_before_body_validation() -> TestResult {
    for path in [
        "/api/assistant/tools/prepare-key",
        "/api/assistant/tools/create-key",
    ] {
        let oversized = "x".repeat(KEY_MUTATION_BODY_LIMIT_BYTES + 1);
        let anonymous = raw_route(
            fixture_router(FixtureStore::default())?,
            path,
            None,
            oversized.clone(),
        )
        .await?;
        assert_eq!(anonymous.status(), StatusCode::UNAUTHORIZED, "{path}");

        let personal_token = raw_route(
            fixture_router(FixtureStore::default())?,
            path,
            Some("Bearer user-token"),
            oversized,
        )
        .await?;
        assert_eq!(personal_token.status(), StatusCode::FORBIDDEN, "{path}");
        assert_key_mutation_headers(&personal_token, path)?;
        let body = json_response(personal_token, path).await?;
        assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED", "{path}");
    }
    Ok(())
}

#[tokio::test]
async fn l0_console_gate_runs_after_json_validation_and_before_rate_limit() -> TestResult {
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
                fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter)?,
                path,
                Some("Bearer browser-session"),
                body,
            )
            .await?;
            assert_eq!(response.status(), expected_status, "{path}");
            assert_key_mutation_headers(&response, path)?;
            assert!(lock_recover(&calls).is_empty(), "{path}");
        }
    }
    Ok(())
}

#[tokio::test]
async fn invalid_and_oversized_bodies_do_not_consume_key_mutation_rate_limit() -> TestResult {
    for path in [
        "/api/assistant/tools/prepare-key",
        "/api/assistant/tools/create-key",
    ] {
        let calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Allowed),
            calls: Arc::clone(&calls),
        });
        let router = fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter)?;

        let invalid = raw_route(
            router.clone(),
            path,
            Some("Bearer admin-session"),
            "not-json".to_owned(),
        )
        .await?;
        assert_eq!(invalid.status(), StatusCode::BAD_REQUEST, "{path}");
        assert_key_mutation_headers(&invalid, path)?;

        let oversized = raw_route(
            router,
            path,
            Some("Bearer admin-session"),
            "x".repeat(KEY_MUTATION_BODY_LIMIT_BYTES + 1),
        )
        .await?;
        assert_eq!(oversized.status(), StatusCode::PAYLOAD_TOO_LARGE, "{path}");
        assert_key_mutation_headers(&oversized, path)?;
        assert!(lock_recover(&calls).is_empty(), "{path}");
    }
    Ok(())
}

#[tokio::test]
async fn exact_16_kib_body_reaches_rate_limit_but_one_extra_byte_does_not() -> TestResult {
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
        let router = fixture_router_with_user_rate_limiter(FixtureStore::default(), limiter)?;
        let at_limit = json_padded_to_limit(value)?;

        let oversized = raw_route(
            router.clone(),
            path,
            Some("Bearer admin-session"),
            format!("{at_limit} "),
        )
        .await?;
        assert_eq!(oversized.status(), StatusCode::PAYLOAD_TOO_LARGE, "{path}");
        assert!(lock_recover(&calls).is_empty(), "{path}");

        let accepted = raw_route(router, path, Some("Bearer admin-session"), at_limit).await?;
        assert_eq!(accepted.status(), StatusCode::TOO_MANY_REQUESTS, "{path}");
        assert_key_mutation_headers(&accepted, path)?;
        assert_eq!(
            lock_recover(&calls).as_slice(),
            &[(scope.to_owned(), 10)],
            "{path}"
        );
    }
    Ok(())
}

#[test]
fn real_selectable_group_rejects_empty_and_virtual_auto() -> TestResult {
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
    Ok(())
}

#[test]
fn selectable_projection_never_exposes_automatic_or_malicious_auto() -> TestResult {
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
    Ok(())
}

#[tokio::test]
async fn prepare_route_returns_only_an_opaque_session_bound_action() -> TestResult {
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
    .await?;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"], json!(prepared_action()));
    assert!(!body.to_string().contains("sk-"));
    let calls = lock_recover(&calls);
    assert_eq!(calls.len(), 1);
    let call = required(calls.first(), "prepare-key repository call is missing")?;
    assert_eq!(call.user_id, 10);
    assert_eq!(call.session_id, "assistant-session");
    assert_eq!(call.draft.name, "assistant-created");
    assert_eq!(call.draft.group.as_str(), "default");
    assert_eq!(call.draft.conversation_id, 91);
    Ok(())
}

#[tokio::test]
async fn prepare_route_rejects_virtual_auto_and_empty_authoritative_options() -> TestResult {
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
        .await?;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], "ASSISTANT_INVALID_GROUP");
        assert!(lock_recover(&calls).is_empty());
    }
    Ok(())
}

#[tokio::test]
async fn prepare_route_strictly_rejects_client_confirmation_fields() -> TestResult {
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
    .await?;

    assert_eq!(status, StatusCode::BAD_REQUEST);
    assert!(lock_recover(&calls).is_empty());
    Ok(())
}

#[tokio::test]
async fn confirm_route_accepts_only_opaque_token_and_forwards_two_factor_code() -> TestResult {
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
    .await?;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"], json!(created_key()));
    assert!(!body.to_string().contains("api_key"));
    assert!(!body.to_string().contains("sk-"));
    let calls = lock_recover(&calls);
    assert_eq!(calls.len(), 1);
    let call = required(calls.first(), "create-key repository call is missing")?;
    assert_eq!(call.actor_id, 10);
    assert_eq!(call.session_id, "assistant-session");
    assert_eq!(call.expected_session_version, 1);
    assert_eq!(call.expected_user_auth_version, 1);
    assert_eq!(call.token.expose(), "opaque-flow-token");
    assert_eq!(call.two_factor_code, "123456");
    Ok(())
}

#[tokio::test]
async fn confirm_route_rejects_client_mutable_draft_fields_before_repository() -> TestResult {
    for extra in [
        json!({"name":"tampered"}),
        json!({"group":"auto"}),
        json!({"confirmed":true}),
    ] {
        let store = FixtureStore::default().with_confirm_result(Ok(created_key()));
        let calls = store.confirm_key_calls();
        let mut body = json!({"confirmation_token":"opaque-flow-token"});
        let extra = required(
            extra.as_object(),
            "client draft fixture must be a JSON object",
        )?;
        required(
            body.as_object_mut(),
            "confirmation request fixture must be a JSON object",
        )?
        .extend(extra.clone());
        let (status, _) = route_json(store, "/api/assistant/tools/create-key", body).await?;

        assert_eq!(status, StatusCode::BAD_REQUEST);
        assert!(lock_recover(&calls).is_empty());
    }
    Ok(())
}

#[tokio::test]
async fn confirm_route_preserves_database_failures_as_internal_errors() -> TestResult {
    let store = FixtureStore::default().with_confirm_result(Err(KeyCreationError::Unavailable(
        "database unavailable".to_owned(),
    )));
    let (status, body) = route_json(
        store,
        "/api/assistant/tools/create-key",
        json!({"confirmation_token":"opaque-flow-token"}),
    )
    .await?;

    assert_eq!(status, StatusCode::INTERNAL_SERVER_ERROR);
    assert_ne!(body["code"], "ASSISTANT_KEY_CONFIRMATION_INVALID");
    Ok(())
}

#[tokio::test]
async fn confirm_route_preserves_invalid_replay_and_two_factor_errors() -> TestResult {
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
        .await?;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], code);
    }
    Ok(())
}
