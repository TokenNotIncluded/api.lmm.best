use std::collections::BTreeMap;

use axum::{
    body::Body,
    http::{Request, StatusCode, header},
};
use serde_json::{Value, json};
use tower::ServiceExt;

use super::domain::*;
use crate::migration_routes::assistant::tests::support::{
    FixtureStore, fixture_router, response_json,
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
