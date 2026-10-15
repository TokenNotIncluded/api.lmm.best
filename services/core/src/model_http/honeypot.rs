//! Core admission precedes the isolated, explicitly uncharged transport.
//! No mark/policy/lease field is exposed to the model client.
use super::{one_header, public_error, request_id, with_request_id};
use crate::relay::honeypot_network::NetworkTarget;
use crate::{
    abuse::{self, AbuseError, AbuseStore, Mode},
    relay::{
        self, ClientProtocol, Limits, RelayError, UpstreamProtocol,
        honeypot::{HoneypotTarget, HoneypotTransport, HoneypotUpstream},
    },
};
use axum::{
    Json,
    body::Body,
    http::{HeaderMap, HeaderValue, header},
    response::{IntoResponse, Response},
};
use serde_json::json;
use sha2::{Digest, Sha256};
use std::{
    collections::{BTreeMap, BTreeSet},
    net::IpAddr,
    sync::Arc,
    time::Duration,
};
use tokio::time::{Instant, timeout};

/// Host-only credential and its exact permitted origins. No secret getter,
/// Debug or Serialize implementation exists; added roots affect only this alias.
#[derive(Clone)]
pub struct HoneypotCredential {
    secret: String,
    origins: BTreeSet<String>,
    additional_root: Option<reqwest::Certificate>,
    legacy_local: bool,
}
impl HoneypotCredential {
    pub fn new(
        secret: String,
        allowed_origins: Vec<String>,
        additional_root: Option<reqwest::Certificate>,
    ) -> Result<Self, RelayError> {
        if secret.is_empty()
            || secret.len() > 4096
            || HeaderValue::from_str(&secret).is_err()
            || allowed_origins.is_empty()
            || allowed_origins.len() > 16
        {
            return Err(RelayError::Invalid("invalid upstream credential policy"));
        }
        let mut origins = BTreeSet::new();
        for origin in allowed_origins {
            if !origins.insert(crate::relay::honeypot::validate_origin(&origin, true)?) {
                return Err(RelayError::Invalid("duplicate upstream origin"));
            }
        }
        Ok(Self {
            secret,
            origins,
            additional_root,
            legacy_local: false,
        })
    }
}

/// Host-only moderation credential registry, separate from normal providers.
/// Config and admin HTTP carry aliases only; credentials never serialize.
#[derive(Clone)]
pub struct HoneypotGateway {
    store: AbuseStore,
    credentials: Arc<BTreeMap<String, HoneypotCredential>>,
    local_http: bool,
    transport: HoneypotTransport,
    ceiling: Limits,
}
impl HoneypotGateway {
    pub(super) async fn drain(&self, grace: Duration) -> relay::DrainReport {
        self.transport.drain(grace).await
    }

    /// Compatibility constructor for explicitly local synthetic callers.
    /// The host uses new_with_network_policy; this cannot reach external hosts.
    pub fn new(
        store: AbuseStore,
        credentials: BTreeMap<String, String>,
        ceiling: Limits,
    ) -> Result<Self, RelayError> {
        let credentials = credentials
            .into_iter()
            .map(|(alias, secret)| {
                (
                    alias,
                    HoneypotCredential {
                        secret,
                        origins: BTreeSet::new(),
                        additional_root: None,
                        legacy_local: true,
                    },
                )
            })
            .collect();
        Self::build(store, credentials, true, ceiling)
    }

    pub fn new_with_network_policy(
        store: AbuseStore,
        credentials: BTreeMap<String, HoneypotCredential>,
        local_http: bool,
        ceiling: Limits,
    ) -> Result<Self, RelayError> {
        if credentials.values().any(|credential| {
            credential.legacy_local
                || credential.origins.iter().any(|origin| {
                    crate::relay::honeypot::validate_origin(origin, local_http).is_err()
                })
        }) {
            return Err(RelayError::Invalid("invalid upstream policy"));
        }
        Self::build(store, credentials, local_http, ceiling)
    }

    fn build(
        store: AbuseStore,
        credentials: BTreeMap<String, HoneypotCredential>,
        local_http: bool,
        ceiling: Limits,
    ) -> Result<Self, RelayError> {
        if credentials.iter().any(|(alias, credential)| {
            alias.is_empty()
                || alias.len() > 64
                || !alias
                    .bytes()
                    .all(|byte| byte.is_ascii_alphanumeric() || b"_.-".contains(&byte))
                || credential.secret.is_empty()
                || credential.secret.len() > 4096
        }) {
            return Err(RelayError::Invalid("invalid upstream credentials"));
        }
        Ok(Self {
            store,
            credentials: Arc::new(credentials),
            local_http,
            transport: HoneypotTransport::new(ceiling.clone())?,
            ceiling,
        })
    }

    pub(super) async fn models(
        &self,
        headers: &HeaderMap,
        peer: Option<IpAddr>,
    ) -> Result<Option<Response>, RelayError> {
        let secret = secret(headers)?;
        let Some(catalog) = timeout(
            self.ceiling.hook_timeout,
            self.store.lookup_model_policy(secret, peer),
        )
        .await
        .map_err(|_| RelayError::RouteUnavailable)?
        .map_err(error)?
        else {
            return Ok(None);
        };
        let data: Vec<_> = catalog
            .public_models
            .into_iter()
            .map(|model| {
                json!({
                    "id":model,"object":"model","created":0,"owned_by":"lmm"
                })
            })
            .collect();
        let mut response = Json(json!({"object":"list","data":data})).into_response();
        response
            .headers_mut()
            .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
        Ok(Some(response))
    }

    pub(super) async fn dispatch(
        &self,
        headers: &HeaderMap,
        body: &[u8],
        client: ClientProtocol,
        peer: Option<IpAddr>,
    ) -> Result<Option<Response>, RelayError> {
        let secret = secret(headers)?;
        // Preserve normal request parsing/authentication order. This lookup
        // cannot grant groups or spend and is rechecked during admission.
        if timeout(
            self.ceiling.hook_timeout,
            self.store.lookup_model_policy(secret, peer),
        )
        .await
        .map_err(|_| RelayError::RouteUnavailable)?
        .map_err(error)?
        .is_none()
        {
            return Ok(None);
        }
        let request = relay::Request::parse(client, body, &self.ceiling)?;
        // Admission authenticates current API-key policy itself. It cannot
        // manufacture ordinary developer grants, model routes or spending.
        let started = Instant::now();
        // Bind the idempotency nonce to this opaque, high-entropy API key
        // before admission returns its numeric credential identity. This ID is
        // also used in the public response and durable admission evidence.
        let nonce = request_id(0, headers)?;
        let mut digest = Sha256::new();
        digest.update(b"lmm-model-admission-v1\0");
        digest.update(secret.as_bytes());
        digest.update(nonce.as_bytes());
        let id = format!("{:x}", digest.finalize());
        let Some(admission) = timeout(
            self.ceiling.hook_timeout,
            self.store.admit_model(
                secret,
                match client {
                    ClientProtocol::Chat => abuse::ClientProtocol::OpenaiChat,
                    ClientProtocol::Responses => abuse::ClientProtocol::OpenaiResponses,
                },
                &request.model,
                peer,
                &id,
            ),
        )
        .await
        .map_err(|_| RelayError::RouteUnavailable)?
        .map_err(error)?
        else {
            return Ok(None);
        };
        let guard = Arc::new(AdmissionLease {
            store: self.store.clone(),
            lease_id: admission.lease_id.clone(),
        });
        let fail = |error| Some(with_request_id(public_error(error), &id));
        if body.len() > admission.limits.max_input_bytes as usize {
            return Ok(fail(RelayError::Limit("request body")));
        }
        let target = match admission.mode {
            Mode::FixedText => match admission.fixed_text {
                Some(text) => HoneypotTarget::FixedText(text),
                None => return Ok(fail(RelayError::RouteUnavailable)),
            },
            Mode::CustomUpstream => {
                let Some(route) = admission.upstream else {
                    return Ok(fail(RelayError::RouteUnavailable));
                };
                let Some(credential) = self.credentials.get(&route.credential_reference) else {
                    return Ok(fail(RelayError::RouteUnavailable));
                };
                let protocol = match route.upstream_protocol {
                    abuse::UpstreamProtocol::OpenaiChat => UpstreamProtocol::Chat,
                    abuse::UpstreamProtocol::OpenaiResponses => UpstreamProtocol::Responses,
                    abuse::UpstreamProtocol::AnthropicMessages => UpstreamProtocol::Anthropic,
                    abuse::UpstreamProtocol::Gemini => UpstreamProtocol::Gemini,
                };
                let selected = if credential.legacy_local {
                    HoneypotUpstream::local(
                        protocol,
                        route.model,
                        &route.base_url,
                        &credential.secret,
                        request.stream,
                    )
                } else {
                    HoneypotUpstream::configured(
                        protocol,
                        route.model,
                        &route.base_url,
                        &credential.secret,
                        request.stream,
                        NetworkTarget {
                            local_http: self.local_http,
                            additional_root: credential.additional_root.clone(),
                            allowed_origins: credential.origins.clone(),
                        },
                    )
                };
                match selected {
                    Ok(route) => HoneypotTarget::Upstream(route),
                    Err(_) => return Ok(fail(RelayError::RouteUnavailable)),
                }
            }
        };
        let mut limits = self.ceiling.clone();
        limits.request_bytes = limits
            .request_bytes
            .min(admission.limits.max_input_bytes as usize);
        limits.output_bytes = limits
            .output_bytes
            .min(admission.limits.max_output_bytes as usize);
        limits.event_bytes = limits.event_bytes.min(limits.output_bytes);
        limits.tool_argument_bytes = limits.tool_argument_bytes.min(limits.output_bytes);
        limits.max_output_tokens = limits
            .max_output_tokens
            .min(admission.limits.max_output_tokens);
        let remaining = Duration::from_secs(u64::from(admission.limits.total_timeout_seconds))
            .checked_sub(started.elapsed())
            .filter(|time| !time.is_zero());
        let Some(remaining) = remaining else {
            return Ok(fail(RelayError::Timeout(relay::Phase::Total)));
        };
        limits.total_timeout = limits.total_timeout.min(remaining);
        let session = match self.transport.start(
            request,
            &id,
            target,
            limits,
            admission.limits.output_bytes_per_second,
            guard,
        ) {
            Ok(session) => session,
            Err(error) => return Ok(fail(error)),
        };
        tokio::spawn(async move {
            let _ = session.completion.await;
        });
        let response = match session.head.await {
            Ok(Ok(head)) => {
                let mut response = Body::from_stream(session.body).into_response();
                response.headers_mut().insert(
                    header::CONTENT_TYPE,
                    HeaderValue::from_static(head.content_type),
                );
                response
                    .headers_mut()
                    .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
                response
                    .headers_mut()
                    .insert("x-accel-buffering", HeaderValue::from_static("no"));
                response
            }
            Ok(Err(error)) => public_error(error),
            Err(_) => public_error(RelayError::RouteUnavailable),
        };
        Ok(Some(with_request_id(response, &id)))
    }
}

struct AdmissionLease {
    store: AbuseStore,
    lease_id: String,
}
impl Drop for AdmissionLease {
    fn drop(&mut self) {
        // On failed release/process death, the database lease still expires.
        // Shutdown must not panic or invent a successful release receipt.
        if let Ok(runtime) = tokio::runtime::Handle::try_current() {
            let store = self.store.clone();
            let lease_id = self.lease_id.clone();
            runtime.spawn(async move {
                let _ = timeout(Duration::from_secs(2), store.finish_model(&lease_id)).await;
            });
        }
    }
}

fn secret(headers: &HeaderMap) -> Result<&str, RelayError> {
    let raw =
        one_header(headers, header::AUTHORIZATION.as_str())?.ok_or(RelayError::Unauthorized)?;
    let (scheme, secret) = raw.split_once(' ').ok_or(RelayError::Unauthorized)?;
    if !scheme.eq_ignore_ascii_case("Bearer") || secret.is_empty() {
        return Err(RelayError::Unauthorized);
    }
    Ok(secret)
}

fn error(error: AbuseError) -> RelayError {
    match error {
        AbuseError::Unauthorized => RelayError::Unauthorized,
        AbuseError::Forbidden => RelayError::Forbidden,
        AbuseError::RateLimited => RelayError::Busy,
        AbuseError::Invalid => RelayError::Invalid("invalid request"),
        AbuseError::Conflict => RelayError::Conflict,
        _ => RelayError::RouteUnavailable,
    }
}
