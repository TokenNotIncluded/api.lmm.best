pub(super) mod support;

use super::*;
use axum::{body::Body, http::Request};
use serde::de::DeserializeOwned;
use std::{
    collections::VecDeque,
    error::Error,
    io,
    sync::{Mutex, MutexGuard},
};
use support::*;
use tower::ServiceExt;

use crate::auth::CriticalRateLimitOutcome;

type TestResult<T = ()> = Result<T, Box<dyn Error + Send + Sync>>;

fn test_error(message: impl Into<String>) -> Box<dyn Error + Send + Sync> {
    Box::new(io::Error::other(message.into()))
}

fn required<T>(value: Option<T>, context: &'static str) -> TestResult<T> {
    value.ok_or_else(|| test_error(context))
}

fn build_request(
    builder: axum::http::request::Builder,
    body: Body,
    context: &'static str,
) -> TestResult<Request<Body>> {
    builder
        .body(body)
        .map_err(|error| test_error(format!("{context}: {error}")))
}

fn json_from_value<T: DeserializeOwned>(value: Value, context: &'static str) -> TestResult<T> {
    serde_json::from_value(value).map_err(|error| test_error(format!("{context}: {error}")))
}

fn json_from_slice<T: DeserializeOwned>(value: &[u8], context: &'static str) -> TestResult<T> {
    serde_json::from_slice(value).map_err(|error| test_error(format!("{context}: {error}")))
}

fn json_from_str<T: DeserializeOwned>(value: &str, context: &'static str) -> TestResult<T> {
    serde_json::from_str(value).map_err(|error| test_error(format!("{context}: {error}")))
}

fn optional_header_str<'a>(
    value: Option<&'a axum::http::HeaderValue>,
    context: &'static str,
) -> TestResult<Option<&'a str>> {
    value
        .map(|value| {
            value
                .to_str()
                .map_err(|error| test_error(format!("{context}: {error}")))
        })
        .transpose()
}

fn lock_recover<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
    match mutex.lock() {
        Ok(guard) => guard,
        Err(poisoned) => poisoned.into_inner(),
    }
}

#[tokio::test]
async fn assistant_status_should_match_go_settings_for_personal_token() -> TestResult {
    let store = FixtureStore {
        settings: AssistantSettingsView {
            enabled: false,
            model: "assistant-model".to_owned(),
            agent_loop_enabled: false,
            max_steps: 4,
            timeout_seconds: 30,
            cache_enabled: false,
            cache_ttl_minutes: 15,
            ..AssistantSettingsView::default()
        },
        ..FixtureStore::default()
    };
    let response = fixture_router(store)
        .oneshot(build_request(
            Request::get("/api/assistant/status")
                .header(header::AUTHORIZATION, "Bearer user-token"),
            Body::empty(),
            "build assistant status request",
        )?)
        .await?;
    let status = response.status();
    let auth_version = response.headers().get("auth-version").cloned();
    let body = response_json(response).await;

    assert_eq!(
        (status, auth_version.as_ref(), body["data"].clone()),
        (
            StatusCode::OK,
            Some(&axum::http::HeaderValue::from_static(AUTH_VERSION)),
            json!({
                "enabled": false,
                "group": "default",
                "model": "assistant-model",
                "route_available": true,
                "funding": {"mode": "super_administrator"},
                "developer_access_granted": false,
                "agent": {
                    "enabled": false,
                    "max_steps": 4,
                    "timeout_seconds": 30,
                    "cache_enabled": false,
                    "cache_ttl_minutes": 15,
                },
            }),
        )
    );
    Ok(())
}

#[test]
fn assistant_setup_tool_should_describe_cc_switch_deep_link_import() -> TestResult {
    let settings = AssistantSettingsView {
        server_address: "https://api.example.com/".to_owned(),
        ..AssistantSettingsView::default()
    };
    let input = json_from_value::<Map<String, Value>>(
        json!({
            "platform": "windows",
            "topic": "cc-switch",
            "model_id": "deepseek-v4-flash",
        }),
        "deserialize assistant setup input",
    )?;

    let result = assistant_setup_tool(&settings, &input);
    assert_eq!(result["ok"], true);
    assert_eq!(result["service_root"], "https://api.example.com");
    assert_eq!(result["cc_switch_import"]["supported"], true);
    assert_eq!(
        result["cc_switch_import"]["protocol"],
        "ccswitch://v1/import"
    );
    assert_eq!(
        result["cc_switch_import"]["endpoint"],
        "https://api.example.com"
    );
    assert_eq!(
        result["official_releases"],
        "https://github.com/farion1231/cc-switch/releases"
    );
    assert!(result["steps"]
        .as_array()
        .is_some_and(|steps| steps.iter().any(|step| step == "Use Import to CC Switch from that private card (or the key's CC Switch action on /keys). The UI constructs the ccswitch:// link and CC Switch shows an import confirmation.")));
    Ok(())
}

#[tokio::test]
async fn assistant_chat_should_own_model_prompt_billing_and_intent() -> TestResult {
    let upstream_body = json!({
        "choices": [{"message": {"role": "assistant", "content": "Use the key page."}}]
    })
    .to_string()
    .into_bytes();
    let store = FixtureStore {
        settings: AssistantSettingsView {
            model: "server-owned-model".to_owned(),
            group: "vip".to_owned(),
            server_address: "https://api.example.com/".to_owned(),
            agent_loop_enabled: false,
            cache_enabled: false,
            ..AssistantSettingsView::default()
        },
        assistant_model_ids: Some(vec!["server-owned-model".to_owned()]),
        billing_result: Some(Ok(AssistantBillingAccount {
            id: 987,
            group: "default".to_owned(),
        })),
        ..FixtureStore::default()
    };
    let intent_calls = Arc::clone(&store.intent_calls);
    let turns = Arc::new(Mutex::new(Vec::new()));
    let backend = Arc::new(FixtureAgentBackend {
        responses: Mutex::new(VecDeque::from([Ok(AssistantAgentTurnResponse {
            status: StatusCode::OK,
            body: upstream_body.clone(),
        })])),
        turns: Arc::clone(&turns),
    });
    let response = fixture_router_with_agent(store, backend)
        .oneshot(build_request(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session"),
            Body::from(r#"{"message":"How do I create a key?","model":"client-model"}"#),
            "build assistant chat ownership request",
        )?)
        .await?;
    let status = response.status();
    let intent = response.headers().get("x-lmm-assistant-intent").cloned();
    let body = to_bytes(response.into_body(), usize::MAX).await?;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body.as_ref(), upstream_body);
    assert_eq!(
        intent,
        Some(axum::http::HeaderValue::from_static("api_key"))
    );
    assert_eq!(
        *lock_recover(&intent_calls),
        vec![(7, "api_key".to_owned())]
    );
    let turns = lock_recover(&turns);
    let turn = required(
        turns.first(),
        "assistant ownership request must record one agent turn",
    )?;
    assert_eq!(turns.len(), 1);
    assert_eq!(turn.billing.id, 987);
    let request: Value = json_from_slice(&turn.body, "deserialize agent ownership request")?;
    assert_eq!(request["model"], "server-owned-model");
    assert_eq!(request["messages"][1]["content"], "How do I create a key?");
    assert!(
        request["messages"][0]["content"]
            .as_str()
            .is_some_and(|prompt| prompt.contains("Never ask for or repeat passwords"))
    );
    assert!(
        request["messages"][0]["content"]
            .as_str()
            .is_some_and(|prompt| prompt.contains("ccswitch://v1/import"))
    );
    assert!(request.get("tools").is_none());
    Ok(())
}

#[tokio::test]
async fn assistant_chat_should_execute_bounded_tool_loop_then_force_final_answer() -> TestResult {
    let first = json!({
        "choices": [{"message": {
            "role": "assistant",
            "content": null,
            "tool_calls": [{
                "id": "cost-call",
                "type": "function",
                "function": {
                    "name": "calculate_cost",
                    "arguments": "{\"input_tokens\":1000,\"output_tokens\":500,\"input_usd_per_million\":1,\"output_usd_per_million\":2,\"group_ratio\":1.5}"
                }
            }]
        }}]
    })
    .to_string()
    .into_bytes();
    let second = json!({
        "choices": [{"message": {"role": "assistant", "content": "About $0.003."}}]
    })
    .to_string()
    .into_bytes();
    let store = FixtureStore {
        settings: AssistantSettingsView {
            model: "assistant-model".to_owned(),
            max_steps: 2,
            cache_enabled: false,
            ..AssistantSettingsView::default()
        },
        billing_result: Some(Ok(AssistantBillingAccount {
            id: 987,
            group: "default".to_owned(),
        })),
        ..FixtureStore::default()
    };
    let turns = Arc::new(Mutex::new(Vec::new()));
    let backend = Arc::new(FixtureAgentBackend {
        responses: Mutex::new(VecDeque::from([
            Ok(AssistantAgentTurnResponse {
                status: StatusCode::OK,
                body: first,
            }),
            Ok(AssistantAgentTurnResponse {
                status: StatusCode::OK,
                body: second,
            }),
        ])),
        turns: Arc::clone(&turns),
    });
    let response = fixture_router_with_agent(store, backend)
        .oneshot(build_request(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session"),
            Body::from(r#"{"message":"estimate cost"}"#),
            "build bounded tool-loop request",
        )?)
        .await?;

    assert_eq!(response.status(), StatusCode::OK);
    let turns = lock_recover(&turns);
    let first_turn = required(
        turns.first(),
        "bounded tool loop must record the initial agent turn",
    )?;
    let second_turn = required(
        turns.get(1),
        "bounded tool loop must record the forced final agent turn",
    )?;
    assert_eq!(turns.len(), 2);
    let first_request: Value =
        json_from_slice(&first_turn.body, "deserialize first bounded tool-loop request")?;
    let second_request: Value =
        json_from_slice(&second_turn.body, "deserialize second bounded tool-loop request")?;
    assert_eq!(first_request["tools"].as_array().map(Vec::len), Some(14));
    assert_eq!(first_request["tool_choice"], "auto");
    assert!(second_request.get("tools").is_none());
    assert!(second_request.get("tool_choice").is_none());
    let tool_result_content = required(
        second_request["messages"]
            .as_array()
            .and_then(|messages| messages.last())
            .and_then(|message| message["content"].as_str()),
        "final bounded tool-loop request must contain a string tool result",
    )?;
    let tool_result: Value =
        json_from_str(tool_result_content, "deserialize bounded tool-loop result")?;
    assert_eq!(tool_result["total_cost_usd"], 0.003);
    Ok(())
}

#[tokio::test]
async fn assistant_chat_should_attach_l1_confirmation_action_to_final_response() -> TestResult {
    let first = json!({
        "choices": [{"message": {
            "role": "assistant",
            "content": null,
            "tool_calls": [{
                "id": "l1-call",
                "type": "function",
                "function": {
                    "name": "prepare_l1_recommendation",
                    "arguments": json!({
                        "user_statement": "I want to connect Claude Code for an open-source Rust project.",
                        "recommendation": "The user described a concrete development workflow and the intended compatible client."
                    }).to_string()
                }
            }]
        }}]
    })
    .to_string()
    .into_bytes();
    let second = json!({
        "choices": [{"message": {"role": "assistant", "content": "Please confirm."}}]
    })
    .to_string()
    .into_bytes();
    let store = FixtureStore {
        settings: AssistantSettingsView {
            model: "assistant-model".to_owned(),
            max_steps: 2,
            cache_enabled: false,
            ..AssistantSettingsView::default()
        },
        billing_result: Some(Ok(AssistantBillingAccount {
            id: 987,
            group: "default".to_owned(),
        })),
        ..FixtureStore::default()
    };
    let response = fixture_router_with_agent(
        store,
        Arc::new(FixtureAgentBackend {
            responses: Mutex::new(VecDeque::from([
                Ok(AssistantAgentTurnResponse {
                    status: StatusCode::OK,
                    body: first,
                }),
                Ok(AssistantAgentTurnResponse {
                    status: StatusCode::OK,
                    body: second,
                }),
            ])),
            turns: Arc::new(Mutex::new(Vec::new())),
        }),
    )
    .oneshot(build_request(
        Request::post("/api/assistant/chat")
            .header(header::AUTHORIZATION, "Bearer browser-session"),
        Body::from(r#"{"message":"Please request L1"}"#),
        "build L1 confirmation action request",
    )?)
    .await?;

    assert_eq!(response.status(), StatusCode::OK);
    let body = response_json(response).await;
    assert_eq!(body["lmm_assistant_action"]["type"], "l1_recommendation");
    assert_eq!(
        body["lmm_assistant_action"]["confirmation_token"],
        "assistant-confirmation-token"
    );
    assert!(
        body["lmm_assistant_action"]["recommendation"]
            .as_str()
            .is_some_and(|value| value.contains("concrete development workflow"))
    );
    Ok(())
}

#[tokio::test]
async fn assistant_chat_cache_hit_should_precede_billing_and_relay() -> TestResult {
    let cached_body = br#"{"choices":[{"message":{"content":"cached"}}]}"#.to_vec();
    let store = FixtureStore {
        cached_response: Some(AssistantCachedResponse {
            status: StatusCode::OK,
            body: cached_body.clone(),
        }),
        ..FixtureStore::default()
    };
    let turns = Arc::new(Mutex::new(Vec::new()));
    let backend = Arc::new(FixtureAgentBackend {
        responses: Mutex::new(VecDeque::new()),
        turns: Arc::clone(&turns),
    });
    let response = fixture_router_with_agent(store, backend)
        .oneshot(build_request(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session"),
            Body::from(r#"{"message":"hello"}"#),
            "build assistant cache-hit request",
        )?)
        .await?;
    let cache = response.headers().get("x-lmm-assistant-cache").cloned();
    let body = to_bytes(response.into_body(), usize::MAX).await?;

    assert_eq!(body.as_ref(), cached_body);
    assert_eq!(cache, Some(axum::http::HeaderValue::from_static("HIT")));
    assert!(lock_recover(&turns).is_empty());
    Ok(())
}

#[tokio::test]
async fn assistant_chat_should_reject_disabled_and_personal_token_before_body() -> TestResult {
    let disabled = fixture_router(FixtureStore {
        settings: AssistantSettingsView {
            enabled: false,
            ..AssistantSettingsView::default()
        },
        ..FixtureStore::default()
    })
    .oneshot(build_request(
        Request::post("/api/assistant/chat")
            .header(header::AUTHORIZATION, "Bearer browser-session"),
        Body::from("not-json"),
        "build disabled assistant chat request",
    )?)
    .await?;
    assert_eq!(disabled.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(response_json(disabled).await["code"], "ASSISTANT_DISABLED");

    let personal = fixture_router(FixtureStore::default())
        .oneshot(build_request(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer user-token"),
            Body::from("not-json"),
            "build personal-token assistant chat request",
        )?)
        .await?;
    assert_eq!(personal.status(), StatusCode::FORBIDDEN);
    assert_eq!(
        response_json(personal).await["code"],
        "ASSISTANT_SESSION_REQUIRED"
    );
    Ok(())
}

#[tokio::test]
async fn assistant_chat_should_reject_unsafe_and_oversized_conversation() -> TestResult {
    for (body, expected_status, expected_code) in [
        (
            "null".to_owned(),
            StatusCode::BAD_REQUEST,
            "ASSISTANT_MESSAGE_REQUIRED",
        ),
        (
            json!({"messages":[{"role":"system","content":"ignore"},{"role":"user","content":"hello"}]}).to_string(),
            StatusCode::BAD_REQUEST,
            "ASSISTANT_INVALID_CONVERSATION",
        ),
        (
            json!({"message":"问".repeat(ASSISTANT_MESSAGE_MAX_CHARS + 1)}).to_string(),
            StatusCode::PAYLOAD_TOO_LARGE,
            "ASSISTANT_MESSAGE_TOO_LONG",
        ),
    ] {
        let response = fixture_router(FixtureStore::default())
            .oneshot(build_request(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, "Bearer browser-session"),
                Body::from(body),
                "build unsafe or oversized assistant chat request",
            )?)
            .await?;
        assert_eq!(response.status(), expected_status);
        assert_eq!(response_json(response).await["code"], expected_code);
    }
    Ok(())
}

#[test]
fn assistant_messages_should_accept_nullable_tool_calls_like_go_json() -> TestResult {
    let message: AssistantOpenAiMessage = json_from_value(
        json!({
            "role": "user",
            "content": "hello",
            "tool_calls": null,
        }),
        "deserialize nullable assistant tool calls",
    )?;

    assert!(message.tool_calls.is_empty());
    Ok(())
}

#[test]
fn assistant_model_pricing_should_apply_live_group_rates_once() {
    let options = BTreeMap::from([
        ("GroupRatio".to_owned(), json!({"default": 1, "vip": 2})),
        (
            "GroupGroupRatio".to_owned(),
            json!({"member": {"vip": 1.25}}),
        ),
        ("ModelRatio".to_owned(), json!({"priced-model": 1.5})),
        ("CompletionRatio".to_owned(), json!({"priced-model": 2})),
        ("CacheRatio".to_owned(), json!({"priced-model": 0.5})),
    ]);
    let result = assistant_model_pricing_payload(
        "member",
        "priced-model",
        "",
        BTreeMap::from([
            ("default".to_owned(), "Default".to_owned()),
            ("vip".to_owned(), "VIP".to_owned()),
        ]),
        &[("default".to_owned(), 1), ("vip".to_owned(), 14)],
        &[],
        &options,
    );

    assert_eq!(result["ok"], true);
    assert_eq!(result["quota_type"], 0);
    assert_eq!(result["prices"][0]["input_usd_per_million"], 3.0);
    assert_eq!(result["prices"][0]["output_usd_per_million"], 6.0);
    assert_eq!(result["prices"][1]["group_ratio"], 1.25);
    assert_eq!(result["prices"][1]["input_usd_per_million"], 3.75);
    assert_eq!(result["prices"][1]["cache_read_usd_per_million"], 1.875);
    assert_eq!(
        result["supported_endpoint_types"],
        json!(["openai", "anthropic"])
    );
}

#[tokio::test]
async fn self_handoff_should_return_latest_user_lead_for_personal_token() -> TestResult {
    let lead = fixture_lead();
    let response = fixture_router(FixtureStore {
        latest: Some(lead.clone()),
        ..FixtureStore::default()
    })
    .oneshot(build_request(
        Request::get("/api/assistant/handoffs/self")
            .header(header::AUTHORIZATION, "user-token"),
        Body::empty(),
        "build self handoff request",
    )?)
    .await?;
    let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
    let pragma = response.headers().get(header::PRAGMA).cloned();
    let expires = response.headers().get(header::EXPIRES).cloned();
    let body = response_json(response).await;

    assert_eq!(body["data"], json!(lead));
    assert_eq!(
        cache_control,
        Some(axum::http::HeaderValue::from_static(
            "no-store, no-cache, must-revalidate, private, max-age=0"
        ))
    );
    assert_eq!(
        pragma,
        Some(axum::http::HeaderValue::from_static("no-cache"))
    );
    assert_eq!(expires, Some(axum::http::HeaderValue::from_static("0")));
    Ok(())
}

#[tokio::test]
async fn admin_handoffs_should_forward_resolved_filter_and_flatten_user_view() -> TestResult {
    let mut lead = fixture_lead();
    lead.status = ASSISTANT_HANDOFF_RESOLVED.to_owned();
    let view = AssistantLeadView {
        lead,
        username: "assistant-user".to_owned(),
        email: "assistant@example.com".to_owned(),
    };
    let response = fixture_router(FixtureStore {
        handoffs: vec![view.clone()],
        expected_handoff_status: ASSISTANT_HANDOFF_RESOLVED,
        ..FixtureStore::default()
    })
    .oneshot(build_request(
        Request::get("/api/assistant/admin/handoffs?status=resolved")
            .header(header::AUTHORIZATION, "Bearer admin-token"),
        Body::empty(),
        "build filtered admin handoffs request",
    )?)
    .await?;
    let body = response_json(response).await;

    assert_eq!(body["data"], json!([view]));
    Ok(())
}

#[tokio::test]
async fn admin_intents_should_reject_out_of_range_days_after_admin_auth() -> TestResult {
    let response = fixture_router(FixtureStore::default())
        .oneshot(build_request(
            Request::get("/api/assistant/admin/intents?days=366")
                .header(header::AUTHORIZATION, "Bearer admin-token"),
            Body::empty(),
            "build out-of-range admin intents request",
        )?)
        .await?;
    let status = response.status();
    let body = response_json(response).await;

    assert_eq!(
        (status, body),
        (
            StatusCode::BAD_REQUEST,
            json!({
                "success": false,
                "code": "ASSISTANT_INTENT_DAYS_INVALID",
                "message": "days must be between 1 and 365",
            }),
        )
    );
    Ok(())
}

#[tokio::test]
async fn submit_handoff_should_rate_limit_redact_persist_and_disable_cache() -> TestResult {
    let raw_message = "password: hunter2 api_key=sk-secret-token-123 Bearer abcdefgh==";
    let redacted_message = redact_assistant_handoff_message(raw_message);
    let mut lead = fixture_lead();
    lead.message = redacted_message.clone();
    let store = FixtureStore {
        submit_result: Some(Ok(lead.clone())),
        ..FixtureStore::default()
    };
    let submit_calls = Arc::clone(&store.submit_calls);
    let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
    let limiter = Arc::new(FixtureUserRateLimiter {
        outcome: Ok(CriticalRateLimitOutcome::Allowed),
        calls: Arc::clone(&rate_limit_calls),
    });
    let response = fixture_router_with_user_rate_limiter(store, limiter)
        .oneshot(build_request(
            Request::post("/api/assistant/handoffs")
                .header(header::AUTHORIZATION, "Bearer browser-session"),
            Body::from(json!({"confirmed": true, "message": raw_message}).to_string()),
            "build confirmed handoff request",
        )?)
        .await?;
    let status = response.status();
    let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
    let body = response_json(response).await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(body["data"], json!(lead));
    assert_eq!(
        *lock_recover(&rate_limit_calls),
        vec![("assistant-handoff".to_owned(), 7)]
    );
    assert_eq!(
        *lock_recover(&submit_calls),
        vec![FixtureSubmitCall {
            user_id: 7,
            username: "assistant-user".to_owned(),
            message: redacted_message,
        }]
    );
    assert_eq!(
        cache_control,
        Some(axum::http::HeaderValue::from_static(
            "no-store, no-cache, must-revalidate, private, max-age=0"
        ))
    );
    Ok(())
}

#[tokio::test]
async fn submit_handoff_should_consume_user_limit_before_rejecting_personal_token() -> TestResult {
    let store = FixtureStore::default();
    let submit_calls = Arc::clone(&store.submit_calls);
    let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
    let limiter = Arc::new(FixtureUserRateLimiter {
        outcome: Ok(CriticalRateLimitOutcome::Allowed),
        calls: Arc::clone(&rate_limit_calls),
    });
    let response = fixture_router_with_user_rate_limiter(store, limiter)
        .oneshot(build_request(
            Request::post("/api/assistant/handoffs")
                .header(header::AUTHORIZATION, "Bearer user-token"),
            Body::from("not-json"),
            "build personal-token handoff request",
        )?)
        .await?;
    let status = response.status();
    let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
    let body = response_json(response).await;

    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED");
    assert_eq!(
        *lock_recover(&rate_limit_calls),
        vec![("assistant-handoff".to_owned(), 7)]
    );
    assert!(lock_recover(&submit_calls).is_empty());
    assert!(cache_control.is_some());
    Ok(())
}

#[tokio::test]
async fn submit_handoff_rate_limit_should_precede_session_body_and_no_store_middleware(
) -> TestResult {
    let store = FixtureStore::default();
    let submit_calls = Arc::clone(&store.submit_calls);
    let limiter = Arc::new(FixtureUserRateLimiter {
        outcome: Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds: 23,
        }),
        calls: Arc::new(Mutex::new(Vec::new())),
    });
    let response = fixture_router_with_user_rate_limiter(store, limiter)
        .oneshot(build_request(
            Request::post("/api/assistant/handoffs")
                .header(header::AUTHORIZATION, "Bearer user-token"),
            Body::from("not-json"),
            "build rate-limited handoff request",
        )?)
        .await?;

    assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(
        response.headers().get(header::RETRY_AFTER),
        Some(&axum::http::HeaderValue::from_static("23"))
    );
    assert!(!response.headers().contains_key(header::CACHE_CONTROL));
    let bytes = to_bytes(response.into_body(), usize::MAX).await?;
    assert!(bytes.is_empty());
    assert!(lock_recover(&submit_calls).is_empty());
    Ok(())
}

#[tokio::test]
async fn submit_handoff_null_json_should_require_confirmation_like_go() -> TestResult {
    let response = fixture_router(FixtureStore::default())
        .oneshot(build_request(
            Request::post("/api/assistant/handoffs")
                .header(header::AUTHORIZATION, "Bearer browser-session"),
            Body::from("null"),
            "build null handoff request",
        )?)
        .await?;
    let status = response.status();
    let body = response_json(response).await;

    assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(body["code"], "ASSISTANT_CONFIRMATION_REQUIRED");
    Ok(())
}

#[tokio::test]
async fn admin_intents_should_return_go_ordered_summary_shape() -> TestResult {
    let summary = vec![
        AssistantIntentSummary {
            intent: "plan_purchase".to_owned(),
            count: 4,
        },
        AssistantIntentSummary {
            intent: "api_key".to_owned(),
            count: 2,
        },
    ];
    let response = fixture_router(FixtureStore {
        summary: summary.clone(),
        ..FixtureStore::default()
    })
    .oneshot(build_request(
        Request::get("/api/assistant/admin/intents?days=7")
            .header(header::AUTHORIZATION, "Bearer admin-token"),
        Body::empty(),
        "build ordered admin intents request",
    )?)
    .await?;
    let body = response_json(response).await;

    assert_eq!(body["data"], json!(summary));
    Ok(())
}

#[tokio::test]
async fn admin_handoffs_should_reject_common_user_without_auth_version() -> TestResult {
    let response = fixture_router(FixtureStore::default())
        .oneshot(build_request(
            Request::get("/api/assistant/admin/handoffs")
                .header(header::AUTHORIZATION, "Bearer user-token"),
            Body::empty(),
            "build common-user admin handoffs request",
        )?)
        .await?;
    let status = response.status();
    let has_auth_version = response.headers().contains_key("auth-version");
    let body = response_json(response).await;

    assert_eq!(
        (status, has_auth_version, body["code"].clone()),
        (
            StatusCode::FORBIDDEN,
            false,
            json!("AUTH_INSUFFICIENT_PRIVILEGE"),
        )
    );
    Ok(())
}

#[tokio::test]
async fn admin_resolve_handoff_should_match_go_transaction_and_audit_contract() -> TestResult {
    let mut resolved = fixture_lead();
    resolved.status = ASSISTANT_HANDOFF_RESOLVED.to_owned();
    resolved.admin_user_id = 10;
    resolved.admin_note = "contacted user".to_owned();
    resolved.resolved_at = 1_700_000_100;
    let store = FixtureStore {
        resolve_result: Some(Ok(resolved.clone())),
        ..FixtureStore::default()
    };
    let resolve_calls = Arc::clone(&store.resolve_calls);
    let audits = Arc::clone(&store.audits);
    let response = fixture_router(store)
        .oneshot(build_request(
            Request::post("/api/assistant/admin/handoffs/3/resolve")
                .header(header::AUTHORIZATION, "Bearer admin-session")
                .extension(ClientIpKey("203.0.113.9".to_owned())),
            Body::from(r#"{"note":"  contacted user  "}"#),
            "build successful admin handoff resolution request",
        )?)
        .await?;
    let status = response.status();
    let auth_version = response.headers().get("auth-version").cloned();
    let body = response_json(response).await;

    assert_eq!(status, StatusCode::OK);
    assert_eq!(
        auth_version,
        Some(axum::http::HeaderValue::from_static(AUTH_VERSION))
    );
    assert_eq!(body["data"], json!(resolved));
    assert_eq!(
        *lock_recover(&resolve_calls),
        vec![FixtureResolveCall {
            admin_user_id: 10,
            admin_username: "assistant-admin".to_owned(),
            lead_id: 3,
            note: "contacted user".to_owned(),
        }]
    );
    assert_eq!(
        *lock_recover(&audits),
        vec![AssistantAdminAudit {
            actor_id: 10,
            actor_username: "assistant-admin".to_owned(),
            actor_role: ADMIN_ROLE,
            auth_method: "session",
            client_ip: "203.0.113.9".to_owned(),
            lead_id: "3".to_owned(),
            status: StatusCode::OK,
            success: true,
        }]
    );
    Ok(())
}

#[tokio::test]
async fn admin_resolve_handoff_should_accept_null_body_like_go_json_binding() -> TestResult {
    let mut resolved = fixture_lead();
    resolved.status = ASSISTANT_HANDOFF_RESOLVED.to_owned();
    let store = FixtureStore {
        resolve_result: Some(Ok(resolved)),
        ..FixtureStore::default()
    };
    let resolve_calls = Arc::clone(&store.resolve_calls);
    let response = fixture_router(store)
        .oneshot(build_request(
            Request::post("/api/assistant/admin/handoffs/3/resolve")
                .header(header::AUTHORIZATION, "Bearer admin-token"),
            Body::from("null"),
            "build null-body admin handoff resolution request",
        )?)
        .await?;

    assert_eq!(response.status(), StatusCode::OK);
    let resolve_calls = lock_recover(&resolve_calls);
    let resolve_call = required(
        resolve_calls.first(),
        "null-body handoff resolution must record a resolve call",
    )?;
    assert_eq!(resolve_call.note, "");
    Ok(())
}

#[tokio::test]
async fn admin_resolve_handoff_should_report_go_conflict_and_audit_failure() -> TestResult {
    let store = FixtureStore {
        resolve_result: Some(Err(ResolveHandoffError::AlreadyResolved)),
        ..FixtureStore::default()
    };
    let audits = Arc::clone(&store.audits);
    let response = fixture_router(store)
        .oneshot(build_request(
            Request::post("/api/assistant/admin/handoffs/3/resolve")
                .header(header::AUTHORIZATION, "Bearer admin-token"),
            Body::empty(),
            "build conflicting admin handoff resolution request",
        )?)
        .await?;
    let status = response.status();
    let body = response_json(response).await;

    assert_eq!(status, StatusCode::CONFLICT);
    assert_eq!(body["code"], "ASSISTANT_HANDOFF_ALREADY_RESOLVED");
    let audits = lock_recover(&audits);
    let audit = required(
        audits.first(),
        "conflicting handoff resolution must record an audit",
    )?;
    assert_eq!(
        (audit.auth_method, audit.status, audit.success),
        ("access_token", StatusCode::CONFLICT, false)
    );
    Ok(())
}

#[tokio::test]
async fn admin_resolve_handoff_should_rate_limit_before_id_and_body_validation() -> TestResult {
    for (rate_limit, expected_status, expected_retry_after) in [
        (
            FixtureRateLimit::Rejected(37),
            StatusCode::TOO_MANY_REQUESTS,
            Some("37"),
        ),
        (
            FixtureRateLimit::Failed,
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        ),
    ] {
        let store = FixtureStore::default();
        let resolve_calls = Arc::clone(&store.resolve_calls);
        let audits = Arc::clone(&store.audits);
        let response = fixture_router_with_auth(store, FixtureAuth { rate_limit })
            .oneshot(build_request(
                Request::post("/api/assistant/admin/handoffs/not-an-id/resolve")
                    .header(header::AUTHORIZATION, "Bearer admin-token")
                    .extension(ClientIpKey("198.51.100.8".to_owned())),
                Body::from("not-json"),
                "build rate-limited admin handoff resolution request",
            )?)
            .await?;

        assert_eq!(response.status(), expected_status);
        let retry_after = optional_header_str(
            response.headers().get(header::RETRY_AFTER),
            "decode rate-limit retry-after header as UTF-8",
        )?;
        assert_eq!(retry_after, expected_retry_after);
        assert_eq!(
            response.headers().get("auth-version"),
            Some(&axum::http::HeaderValue::from_static(AUTH_VERSION))
        );
        let bytes = to_bytes(response.into_body(), usize::MAX).await?;
        assert!(bytes.is_empty());
        assert!(lock_recover(&resolve_calls).is_empty());
        let audits = lock_recover(&audits);
        let audit = required(
            audits.first(),
            "rate-limited handoff resolution must record an audit",
        )?;
        assert_eq!((audit.status, audit.success), (expected_status, false));
    }
    Ok(())
}

#[test]
fn assistant_settings_should_keep_go_defaults_for_invalid_options() {
    let settings = AssistantSettingsView::from_options(&HashMap::from([
        ("AssistantEnabled".to_owned(), "TRUE".to_owned()),
        ("AssistantModel".to_owned(), "  ".to_owned()),
        ("AssistantMaxSteps".to_owned(), "13".to_owned()),
        ("AssistantTimeoutSeconds".to_owned(), "4".to_owned()),
    ]));

    assert_eq!(
        settings,
        AssistantSettingsView {
            enabled: false,
            ..AssistantSettingsView::default()
        }
    );
}

#[test]
fn assistant_handoff_redaction_should_match_go_patterns_and_be_idempotent() {
    let message = "登录失败 password: hunter2 token sk-secret-token-123; api-key=plainsecret Bearer abcdefgh== 密钥：中文秘密";
    let redacted = redact_assistant_handoff_message(message);

    assert!(!redacted.contains("hunter2"));
    assert!(!redacted.contains("sk-secret-token-123"));
    assert!(!redacted.contains("abcdefgh"));
    assert!(!redacted.contains("中文秘密"));
    assert!(redacted.contains("[REDACTED_API_KEY]"));
    assert!(redacted.contains("Bearer [REDACTED_TOKEN]"));
    assert!(redacted.contains("password: [REDACTED]"));
    assert_eq!(redact_assistant_handoff_message(&redacted), redacted);
}

#[test]
fn assistant_handoff_api_key_boundary_should_leave_trailing_punctuation() {
    assert_eq!(
        redact_assistant_handoff_message("sk-abcdef--"),
        "[REDACTED_API_KEY]--"
    );
    assert_eq!(
        redact_assistant_handoff_message("prefixsk-abcdef"),
        "prefixsk-abcdef"
    );
}

#[test]
fn assistant_user_rate_limit_key_should_match_go_fixed_window_namespace() {
    assert_eq!(
        assistant_user_rate_limit_key("assistant-handoff", 7),
        "rateLimit:v2:user:UC:assistant-handoff:7"
    );
}

#[test]
fn access_denied_offer_payload_should_hide_plans_and_discounts() {
    let result = access_denied_offer_payload();

    assert_eq!(result["ok"], false);
    assert_eq!(result["developer_access_granted"], false);
    assert_eq!(result["read_only"], false);
    assert_eq!(result["checkout_available"], false);
    assert_eq!(result["payment_hidden"], true);
    assert_eq!(result["plans"], json!([]));
    assert_eq!(result["topup_discounts"], json!({}));
    assert!(
        result["error"]
            .as_str()
            .is_some_and(|error| error.contains("L1 access"))
    );
}

#[test]
fn offer_payload_should_hide_payment_for_restricted_accounts() {
    let result = offer_payload(true, true, json!([{"plan": {"id": 1}}]), Map::new());

    assert_eq!(result["checkout_available"], false);
    assert_eq!(result["payment_hidden"], true);
    assert_eq!(result["topup_discounts"], json!({}));
}

#[test]
fn parse_amount_discounts_should_fall_back_to_registered_payment_object() {
    let values = HashMap::from([(
        "payment_setting".to_owned(),
        r#"{"amount_discount":{"100":0.8,"invalid":"bad"}}"#.to_owned(),
    )]);

    assert_eq!(
        Value::Object(parse_amount_discounts(&values)),
        json!({"100": 0.8})
    );
}

#[test]
fn linux_do_email_should_be_payment_restricted_case_insensitively() {
    assert!(is_linux_do_email(" Person@Linux.Do "));
}
