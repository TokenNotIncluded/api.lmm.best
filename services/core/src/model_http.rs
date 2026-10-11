//! Rust-owned model HTTP entry. No Go proxy and no simulated money adapter.
//!
//! The first integration supports isolated numeric-loopback synthetic providers
//! only, with non-streaming OpenAI text/tool requests. External providers remain
//! disabled until versioned model context/input bounds are integrated and tested.
//! Streaming stays disabled until durable pre-output checkpoints are
//! wired into the relay. Unknown provider usage retains its durable hold.
mod billing;
mod routing;

use crate::{
    billing::{charging::Charging, transactional_ledger::TransactionalLedger},
    identity::{CredentialKind, IdentityError, IdentityStore, Principal},
    relay::{self, ClientProtocol, Limits, Relay, RelayError, RequestContext},
};
use axum::{
    Json, Router,
    body::{Body, Bytes},
    extract::{DefaultBodyLimit, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use rand::{RngCore, rngs::OsRng};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{
    collections::BTreeMap,
    sync::{Arc, Mutex},
    time::Duration,
};

pub use routing::UpstreamCredential;

/// Conservative, server-owned limits. The input ceiling must cover the
/// configured provider/model context, including tool and message framing.
#[derive(Clone, Copy)]
pub struct ChargePolicy {
    pub input_token_ceiling: i64,
    pub lease_seconds: i64,
}

struct Operation {
    secret: String,
    group: String,
    worker: [u8; 32],
    fingerprint: [u8; 32],
    selected: Option<routing::Selected>,
}

struct Bridge {
    charging: Charging,
    routing: Arc<crate::routing::Router>,
    credentials: BTreeMap<(u64, u64), String>,
    active: Mutex<BTreeMap<String, Operation>>,
    policy: ChargePolicy,
    total_timeout: Duration,
}

#[derive(Clone)]
pub struct ModelGateway {
    identity: IdentityStore,
    access: Arc<BTreeMap<i64, String>>,
    bridge: Arc<Bridge>,
    relay: Relay,
    limits: Limits,
}

impl ModelGateway {
    /// All credentials, group grants and routing snapshots are supplied by the
    /// Rust host. No client field can choose a URL, upstream key, payer or price.
    /// The real charging and ledger adapters share this exact PostgreSQL pool.
    pub fn new(
        pool: PgPool,
        routing: Arc<crate::routing::Router>,
        credentials: Vec<UpstreamCredential>,
        key_groups: BTreeMap<i64, String>,
        limits: Limits,
        policy: ChargePolicy,
    ) -> Result<Self, RelayError> {
        limits.validate()?;
        if policy.input_token_ceiling <= 0
            || policy.input_token_ceiling > 16_000_000
            || !(1..=300).contains(&policy.lease_seconds)
            || limits.connect_retries != 0
            || limits.total_timeout + limits.hook_timeout * 2 + Duration::from_secs(2)
                >= Duration::from_secs(policy.lease_seconds as u64)
            || key_groups
                .iter()
                .any(|(id, group)| *id <= 0 || group.is_empty())
        {
            return Err(RelayError::Invalid("unsafe model gateway policy"));
        }
        let credentials = routing::credential_map(credentials)?;
        for group in key_groups.values() {
            routing
                .catalog(group)
                .map_err(|_| RelayError::RouteUnavailable)?;
        }
        let bridge = Arc::new(Bridge {
            charging: Charging::new(pool.clone(), Arc::new(TransactionalLedger)),
            routing,
            credentials,
            active: Mutex::new(BTreeMap::new()),
            policy,
            total_timeout: limits.total_timeout,
        });
        let relay = Relay::new(bridge.clone(), bridge.clone(), limits.clone())?;
        Ok(Self {
            identity: IdentityStore::from_pool(pool),
            access: Arc::new(key_groups),
            bridge,
            relay,
            limits,
        })
    }

    pub fn router(&self) -> Router {
        Router::new()
            .route("/v1/models", get(models))
            .route("/v1/chat/completions", post(chat))
            .route("/v1/responses", post(responses))
            .layer(DefaultBodyLimit::max(self.limits.request_bytes))
            .with_state(self.clone())
    }

    pub async fn drain(&self, grace: Duration) -> relay::DrainReport {
        self.relay.drain(grace).await
    }

    /// Trusted host recovery only; never an HTTP/client/Go action. Expired
    /// dispatches keep their holds pending authoritative provider evidence.
    pub async fn recover_expired(&self, limit: i64) -> Result<usize, RelayError> {
        self.bridge
            .charging
            .recover_expired(limit)
            .await
            .map(|charges| charges.len())
            .map_err(billing::error)
    }

    async fn authenticate(
        &self,
        headers: &HeaderMap,
    ) -> Result<(Principal, String, String), RelayError> {
        let raw =
            one_header(headers, header::AUTHORIZATION.as_str())?.ok_or(RelayError::Unauthorized)?;
        let (scheme, secret) = raw.split_once(' ').ok_or(RelayError::Unauthorized)?;
        if !scheme.eq_ignore_ascii_case("Bearer") {
            return Err(RelayError::Unauthorized);
        }
        let principal = self.identity.authorize(secret).await.map_err(|e| match e {
            IdentityError::Unauthorized | IdentityError::Invalid => RelayError::Unauthorized,
            IdentityError::Forbidden => RelayError::Forbidden,
            _ => RelayError::BillingUnavailable,
        })?;
        if principal.credential_kind != CredentialKind::ApiKey {
            return Err(RelayError::Forbidden);
        }
        let group = self
            .access
            .get(&principal.credential_id)
            .ok_or(RelayError::Forbidden)?
            .clone();
        Ok((principal, secret.to_owned(), group))
    }
}

fn one_header<'a>(headers: &'a HeaderMap, name: &str) -> Result<Option<&'a str>, RelayError> {
    if headers.get_all(name).iter().count() > 1 {
        return Err(RelayError::Invalid("duplicate header"));
    }
    headers
        .get(name)
        .map(|v| {
            v.to_str()
                .map_err(|_| RelayError::Invalid("invalid header"))
        })
        .transpose()
}

fn public_error(error: RelayError) -> Response {
    let status = StatusCode::from_u16(error.status()).unwrap_or(StatusCode::SERVICE_UNAVAILABLE);
    let mut response = (status, Json(error.json())).into_response();
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    if status == StatusCode::UNAUTHORIZED {
        response
            .headers_mut()
            .insert(header::WWW_AUTHENTICATE, HeaderValue::from_static("Bearer"));
    }
    response
}

fn with_request_id(mut response: Response, id: &str) -> Response {
    response.headers_mut().insert(
        "x-request-id",
        HeaderValue::from_str(id).expect("hex request id"),
    );
    response
}

async fn models(State(gateway): State<ModelGateway>, headers: HeaderMap) -> Response {
    let (_, _, group) = match gateway.authenticate(&headers).await {
        Ok(v) => v,
        Err(e) => return public_error(e),
    };
    match gateway.bridge.routing.catalog(&group) {
        Ok(models) => {
            let data: Vec<_> = models
                .into_iter()
                .map(|m| {
                    json!({
                        "id":m.name,"object":"model","created":0,"owned_by":"lmm",
                    })
                })
                .collect();
            let mut response = Json(json!({"object":"list","data":data})).into_response();
            response
                .headers_mut()
                .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
            response
        }
        Err(_) => public_error(RelayError::RouteUnavailable),
    }
}
async fn chat(State(gateway): State<ModelGateway>, headers: HeaderMap, body: Bytes) -> Response {
    dispatch(gateway, headers, body, ClientProtocol::Chat).await
}
async fn responses(
    State(gateway): State<ModelGateway>,
    headers: HeaderMap,
    body: Bytes,
) -> Response {
    dispatch(gateway, headers, body, ClientProtocol::Responses).await
}

fn request_id(key_id: i64, headers: &HeaderMap) -> Result<String, RelayError> {
    let mut digest = Sha256::new();
    digest.update(b"lmm-model-request-v1\0");
    digest.update(key_id.to_be_bytes());
    if let Some(key) = one_header(headers, "idempotency-key")? {
        if key.is_empty() || key.len() > 128 || !key.bytes().all(|b| b.is_ascii_graphic()) {
            return Err(RelayError::Invalid("invalid idempotency key"));
        }
        digest.update(key.as_bytes());
    } else {
        let mut random = [0; 32];
        OsRng.fill_bytes(&mut random);
        digest.update(random);
    }
    Ok(format!("{:x}", digest.finalize()))
}

async fn dispatch(
    gateway: ModelGateway,
    headers: HeaderMap,
    body: Bytes,
    client: ClientProtocol,
) -> Response {
    let (principal, secret, group) = match gateway.authenticate(&headers).await {
        Ok(v) => v,
        Err(e) => return public_error(e),
    };
    let request = match relay::Request::parse(client, &body, &gateway.limits) {
        Ok(v) => v,
        Err(e) => return public_error(e),
    };
    if request.stream {
        return public_error(RelayError::Unsupported(
            "streaming requires durable output checkpoints",
        ));
    }
    let id = match request_id(principal.credential_id, &headers) {
        Ok(v) => v,
        Err(e) => return public_error(e),
    };
    let fail = |error| with_request_id(public_error(error), &id);
    match gateway.bridge.charging.resolve(&id).await {
        Ok(Some(_)) => return fail(RelayError::Conflict),
        Ok(None) => (),
        Err(e) => return fail(billing::error(e)),
    }
    let context = RequestContext {
        request_id: id.clone(),
        actor_user_id: principal.user_id,
        key_id: principal.credential_id,
    };
    let mut hash = Sha256::new();
    hash.update(b"lmm-model-fingerprint-v1\0");
    hash.update(principal.user_id.to_be_bytes());
    hash.update(principal.credential_id.to_be_bytes());
    hash.update(group.as_bytes());
    hash.update([client as u8]);
    hash.update(&body);
    let mut worker = [0; 32];
    OsRng.fill_bytes(&mut worker);
    {
        let Ok(mut active) = gateway.bridge.active.lock() else {
            return fail(RelayError::BillingUnavailable);
        };
        if active.contains_key(&id) {
            return fail(RelayError::Conflict);
        }
        if active.len() >= gateway.limits.max_in_flight {
            return fail(RelayError::Busy);
        }
        active.insert(
            id.clone(),
            Operation {
                secret,
                group,
                worker,
                fingerprint: hash.finalize().into(),
                selected: None,
            },
        );
    }
    let session = match gateway.relay.start(request, context) {
        Ok(v) => v,
        Err(e) => {
            if let Ok(mut active) = gateway.bridge.active.lock() {
                active.remove(&id);
            }
            return fail(e);
        }
    };
    let relay::Session {
        head,
        body,
        completion,
        cancel: _,
    } = session;
    let bridge = gateway.bridge.clone();
    let complete_id = id.clone();
    // Finalization is owned by the relay worker and survives body drop. Keeping
    // this receiver also bounds and removes process-local credential material.
    tokio::spawn(async move {
        let _ = completion.await;
        if let Ok(mut active) = bridge.active.lock() {
            active.remove(&complete_id);
        }
    });
    with_request_id(
        match head.await {
            Ok(Ok(head)) => {
                let mut response = Body::from_stream(body).into_response();
                *response.status_mut() =
                    StatusCode::from_u16(head.status).unwrap_or(StatusCode::BAD_GATEWAY);
                response.headers_mut().insert(
                    header::CONTENT_TYPE,
                    HeaderValue::from_static(head.content_type),
                );
                response
                    .headers_mut()
                    .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
                response.headers_mut().insert(
                    "x-request-id",
                    HeaderValue::from_str(&id).expect("hex request id"),
                );
                response
            }
            Ok(Err(e)) => public_error(e),
            Err(_) => public_error(RelayError::BillingUnavailable),
        },
        &id,
    )
}
