use std::sync::Arc;

use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode, header},
};
use lmm_api_rs::{
    ClientIpKey,
    routes::public_catalog::{
        AccountBalanceSnapshot, AccountBalanceToken, MemoryPublicCatalogStore, PublicCatalogState,
        public_catalog_router,
    },
};
use serde_json::Value;
use tower::ServiceExt;

fn app(token: AccountBalanceToken, snapshot: AccountBalanceSnapshot) -> Router {
    let mut store = MemoryPublicCatalogStore::default();
    store.balance_tokens.insert("balance-key".to_owned(), token);
    store.balances.insert(7, snapshot);
    public_catalog_router(PublicCatalogState::new(
        Arc::new(store),
        Arc::new(NoDashboardAuth),
    ))
}

struct NoDashboardAuth;

#[async_trait::async_trait]
impl lmm_api_rs::routes::public_catalog::PublicCatalogAuthorizer for NoDashboardAuth {
    async fn principal(
        &self,
        _: &axum::http::HeaderMap,
    ) -> Result<
        lmm_api_rs::routes::public_catalog::PublicCatalogPrincipal,
        lmm_api_rs::routes::public_catalog::PublicCatalogAuthError,
    > {
        Err(lmm_api_rs::routes::public_catalog::PublicCatalogAuthError::Unauthorized)
    }
}

async fn json(response: axum::response::Response) -> (StatusCode, axum::http::HeaderMap, Value) {
    let status = response.status();
    let headers = response.headers().clone();
    let body = to_bytes(response.into_body(), usize::MAX)
        .await
        .expect("body");
    (
        status,
        headers,
        serde_json::from_slice(&body).expect("json"),
    )
}

fn enabled_token() -> AccountBalanceToken {
    AccountBalanceToken {
        user_id: 7,
        status: 1,
        expired_time: -1,
        user_status: 1,
        account_balance_read: true,
        ..Default::default()
    }
}

fn snapshot() -> AccountBalanceSnapshot {
    AccountBalanceSnapshot {
        remaining: -1.25,
        updated_at: 1_789_459_200,
    }
}

#[tokio::test]
async fn balance_requires_grant_and_is_never_cached() {
    let mut token = enabled_token();
    token.account_balance_read = false;
    let response = app(token, snapshot())
        .oneshot(
            Request::get("/v1/balance")
                .header(header::AUTHORIZATION, "Bearer balance-key")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    let (status, headers, body) = json(response).await;
    assert_eq!(status, StatusCode::FORBIDDEN);
    assert_eq!(headers.get(header::CACHE_CONTROL).unwrap(), "no-store");
    assert_eq!(
        body,
        serde_json::json!({
            "valid": false,
            "error": "account_balance_access_required"
        })
    );
}

#[tokio::test]
async fn balance_returns_persisted_usd_zero_and_negative_values() {
    let response = app(enabled_token(), snapshot())
        .oneshot(
            Request::get("/v1/balance")
                .header(header::AUTHORIZATION, "Bearer sk-balance-key")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
    let (status, headers, body) = json(response).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(headers.get(header::CACHE_CONTROL).unwrap(), "no-store");
    assert_eq!(body["valid"], true);
    assert_eq!(body["scope"], "account");
    assert_eq!(body["currency"], "USD");
    assert_eq!(body["remaining"], -1.25);
    assert_eq!(body["updated_at"], 1_789_459_200_i64);
    assert_eq!(body["consistency"], "persisted_snapshot");

    let zero = app(
        enabled_token(),
        AccountBalanceSnapshot {
            remaining: 0.0,
            updated_at: 1_789_459_201,
        },
    )
    .oneshot(
        Request::get("/v1/balance")
            .header(header::AUTHORIZATION, "Bearer balance-key")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    let (_, _, zero_body) = json(zero).await;
    assert_eq!(zero_body["remaining"], 0.0);
}

#[tokio::test]
async fn balance_rejects_invalid_lifecycle_oauth_and_ip() {
    let exhausted = app(
        AccountBalanceToken {
            status: 4,
            ..enabled_token()
        },
        snapshot(),
    )
    .oneshot(
        Request::get("/v1/balance")
            .header(header::AUTHORIZATION, "Bearer balance-key")
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(exhausted.status(), StatusCode::OK);

    for token in [
        AccountBalanceToken {
            status: 2,
            ..enabled_token()
        },
        AccountBalanceToken {
            expired_time: 1,
            ..enabled_token()
        },
        AccountBalanceToken {
            oauth_managed: true,
            ..enabled_token()
        },
        AccountBalanceToken {
            user_status: 2,
            ..enabled_token()
        },
    ] {
        let response = app(token.clone(), snapshot())
            .oneshot(
                Request::get("/v1/balance")
                    .header(header::AUTHORIZATION, "Bearer balance-key")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }

    let response = app(
        AccountBalanceToken {
            allow_ips: Some("10.0.0.0/8".to_owned()),
            ..enabled_token()
        },
        snapshot(),
    )
    .oneshot(
        Request::get("/v1/balance")
            .header(header::AUTHORIZATION, "Bearer balance-key")
            .extension(ClientIpKey("192.0.2.1".to_owned()))
            .body(Body::empty())
            .expect("request"),
    )
    .await
    .expect("response");
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
}
