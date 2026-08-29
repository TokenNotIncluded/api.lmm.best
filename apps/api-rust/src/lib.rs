//! Reusable HTTP slices for the Rust control-plane binary.

/// Native public command dispatch, run before service configuration.
pub mod cli;
/// Manual-only production deployment schemas and target recovery.
pub mod deployment;
/// Immutable frontend release publication.
pub mod frontend_deploy;
/// Strict generic-provider link management.
pub mod provider_link;
/// API-route contract revision generation and verification.
pub mod route_contract;

use std::net::IpAddr;

use axum::{
    body::Body,
    http::{HeaderValue, StatusCode, header},
    response::Response,
};

/// Trusted values established once at the listener request boundary.
#[derive(Clone, Debug)]
pub struct RequestContext {
    /// Server-generated identifier shared by legacy-compatible handlers.
    pub request_id: String,
    /// Client address after the listener's trusted-proxy policy is applied.
    pub client_ip: Option<IpAddr>,
}

/// Original trimmed client-address text used for legacy rate-limit keys.
#[derive(Clone, Debug)]
pub struct ClientIpKey(pub String);

/// Marks a response whose empty body is part of the legacy wire contract.
///
/// The listener boundary normally replaces non-JSON error bodies with its
/// standard error envelope.  Critical legacy rate-limit failures are the one
/// deliberate exception, so the marker is shared by every route slice that
/// can produce those responses.
#[derive(Clone, Copy, Debug, Default)]
pub struct PreserveLegacyEmptyError;

/// Build a legacy-compatible empty error response and protect it from the
/// listener's JSON error normalizer.
pub fn legacy_empty_response(status: StatusCode, retry_after_seconds: Option<u64>) -> Response {
    let mut response = Response::new(Body::empty());
    *response.status_mut() = status;
    if let Some(seconds) = retry_after_seconds.filter(|seconds| *seconds > 0)
        && let Ok(value) = HeaderValue::from_str(&seconds.to_string())
    {
        response.headers_mut().insert(header::RETRY_AFTER, value);
    }
    response.extensions_mut().insert(PreserveLegacyEmptyError);
    response
}

/// Dashboard authentication routes and their PostgreSQL/Valkey adapter.
pub mod auth;

/// Legacy-compatible OpenAI model discovery route.
pub mod models;

/// Hardened shared construction for outbound control-plane HTTP calls.
pub mod outbound_http;

/// Bounded low-cardinality protocol-conversion observability.
pub mod conversion_observability;

/// Deterministic protocol conversion rollout, shadow, and rollback decisions.
pub mod protocol_rollout;

/// Bounded local-only old/new protocol shadow coordination.
pub mod protocol_shadow;

/// Trusted import boundary for offline Go-vs-Rust differential evidence.
pub mod protocol_differential_gate;

/// Closed-by-default pure protocol-route admission decisions.
pub mod protocol_route_gate;

/// Closed-by-default typed streaming conversion pre-wiring.
pub mod protocol_stream_pipeline;

/// Closed-by-default route ownership evidence and review gate.
pub mod route_ownership;

/// Runtime-owned protocol capability catalog and startup drift validation.
pub mod protocol_runtime_registry;

/// Production protocol conversion backed by the cortexfs-protocol crate.
pub mod cortexfs_protocol_bridge;

/// Candidate route slices compiled for migration testing but not mounted.
pub mod migration_routes;

/// Focused candidate for the legacy model-deletion boundary.
#[cfg(test)]
pub(crate) mod missing_relay_model_delete_candidate;

/// Legacy-compatible public system status route.
pub mod status;
