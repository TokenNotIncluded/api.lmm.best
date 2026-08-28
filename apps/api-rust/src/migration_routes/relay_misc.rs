//! Legacy relay routes which do not belong to the OpenAI chat, media, or
//! Anthropic/Gemini protocol slices.
//!
//! This file is deliberately self-contained while the migration router is
//! being assembled.  `routes` must be merged behind the same token-auth,
//! performance, rate-limit and channel-distribution adapters as legacy
//! `SetRelayRouter`; do not mount it as a public router.

use std::io::{Cursor, Read};
use std::sync::Arc;

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::{Body, Bytes, to_bytes},
    extract::{Request, State},
    http::{HeaderMap, HeaderName, StatusCode, header},
    response::{IntoResponse, Response},
};
use brotli::{BrotliDecompressStream, BrotliResult, BrotliState, Decompressor, enc::StandardAlloc};
use flate2::read::GzDecoder;
use serde::{Deserialize, Serialize};
use zstd::stream::read::Decoder as ZstdDecoder;

const MAX_RELAY_BODY_BYTES: usize = 128 * 1024 * 1024;

/// The four relay formats owned by this migration slice.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelayProtocol {
    AlphaSearch,
    Embedding,
    Rerank,
    OpenAi,
}

/// Request metadata produced at the same point as legacy `Distribute`.
///
/// The selected-channel adapter consumes this value from request extensions;
/// it must not re-derive a different model or upstream path later.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RelayRequestContext {
    pub protocol: RelayProtocol,
    pub path: String,
    pub model: Option<String>,
    pub stream: bool,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum RelayBodyEncoding {
    Identity,
    Gzip,
    Brotli,
    Zstd,
}

#[derive(Debug, Eq, PartialEq, thiserror::Error)]
enum DecodeBodyError {
    #[error("invalid body encoding")]
    Invalid,
    #[error("decoded body exceeds the configured limit")]
    TooLarge,
    #[error("brotli decode failed: {0}")]
    Brotli(String),
    #[error("zstd decode failed: {0}")]
    Zstd(String),
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct RelayAccounting {
    pub protocol: RelayProtocol,
    pub status: StatusCode,
    pub upstream_succeeded: bool,
}

/// Outcome of the outer legacy token-auth and channel-distribution pipeline.
///
/// The concrete implementation is intentionally supplied by the eventual
/// PostgreSQL/Valkey adapter.  Valkey is only a cache: an outage must fall
/// back to PostgreSQL instead of granting or denying access from stale state.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RelayAuth {
    Authorized,
    Rejected {
        status: StatusCode,
        message: String,
    },
    /// Current-Go token policy conceals invalid or untrusted relay
    /// credentials behind the same small 404 document as an unknown route.
    ConcealedNotFound,
    /// Current OpenAI-shaped middleware error. The older fixture variant above
    /// retains its historical `param` member; this variant intentionally does
    /// not add one because current Go's middleware envelope has only message,
    /// type, and code.
    RejectedOpenAi {
        status: StatusCode,
        message: String,
        code: String,
    },
    /// Current-Go middleware errors whose OpenAI envelope retains the legacy
    /// empty `param` member. Performance load shedding uses this shape and,
    /// unlike authenticated relay errors, does not append a request ID.
    RejectedOpenAiWithParam {
        status: StatusCode,
        message: String,
        code: &'static str,
    },
}

#[async_trait]
pub trait RelayMiscService: Send + Sync {
    /// Legacy `SystemPerformanceCheck`. The default is deliberately closed so
    /// an adapter cannot accidentally skip load shedding.
    async fn system_performance(&self, _request: &Request) -> RelayAuth {
        missing_stage("system performance")
    }

    /// Legacy `TokenAuth`.
    async fn authorize(&self, request: &Request) -> RelayAuth;

    /// Mutable form of [`Self::authorize`] used by production executors.
    ///
    /// The default preserves the historical fixture contract. A concrete
    /// executor may override this hook to attach an authenticated principal to
    /// the request extensions; that keeps per-request state out of global maps
    /// and prevents concurrent relay requests from sharing credentials.
    async fn authorize_prepared(&self, request: &mut Request) -> RelayAuth {
        self.authorize(request).await
    }

    /// Legacy `ModelRequestRateLimit`, after token authentication and before
    /// request parsing/channel selection.
    async fn model_rate_limit(&self, _request: &Request) -> RelayAuth {
        missing_stage("model rate limit")
    }

    /// Mutable form of [`Self::model_rate_limit`] for implementations which
    /// consume the authenticated principal stored by
    /// [`Self::authorize_prepared`].
    async fn model_rate_limit_prepared(&self, request: &mut Request) -> RelayAuth {
        self.model_rate_limit(request).await
    }

    /// Legacy `Distribute`, including model access and channel selection.
    async fn distribute(&self, _context: &RelayRequestContext, _request: &Request) -> RelayAuth {
        missing_stage("channel distribution")
    }

    /// Mutable channel-distribution hook for attaching the selected channel
    /// and billing reservation to this request only.
    async fn distribute_prepared(
        &self,
        context: &RelayRequestContext,
        request: &mut Request,
    ) -> RelayAuth {
        self.distribute(context, request).await
    }

    /// Decode a request body exactly once. Invalid compressed input is mapped
    /// to the legacy 400 boundary by [`decoded_request`].
    async fn decode_body(
        &self,
        encoding: RelayBodyEncoding,
        body: Bytes,
    ) -> Result<Bytes, RelayAuth> {
        decode_body_bytes(encoding, body).map_err(|error| RelayAuth::Rejected {
            status: StatusCode::BAD_REQUEST,
            message: match error {
                DecodeBodyError::Invalid => "invalid compressed request body".to_owned(),
                DecodeBodyError::TooLarge => "http: request body too large".to_owned(),
                DecodeBodyError::Brotli(message) | DecodeBodyError::Zstd(message) => message,
            },
        })
    }

    /// Apply selected-channel credentials and header overrides to the safe
    /// legacy baseline. The input never contains caller credentials,
    /// `Accept-Encoding`, or hop-by-hop headers.
    async fn provider_headers(
        &self,
        _context: &RelayRequestContext,
        _headers: &HeaderMap,
    ) -> Result<HeaderMap, RelayAuth> {
        Err(missing_stage("provider header override"))
    }

    /// Send an already-authorized request to the selected upstream.  Returning
    /// `Response` rather than decoded JSON is intentional: it preserves binary
    /// replies and chunked/SSE relay streams byte-for-byte. The response body
    /// must own the upstream cancellation guard so dropping it cancels I/O.
    async fn relay(&self, protocol: RelayProtocol, request: Request) -> Response;

    /// Commit/refund quota, usage logs, rate-limit success state, and channel
    /// health for both successful and failed upstream responses.
    async fn account(
        &self,
        _context: &RelayRequestContext,
        _accounting: RelayAccounting,
    ) -> RelayAuth {
        missing_stage("relay accounting")
    }

    /// Executes the selected upstream and its accounting lifecycle.
    ///
    /// The default exactly retains the original fixture-stage sequence.
    /// Production implementations may override the whole lifecycle so the
    /// selected channel, provider I/O, quota settlement, and audit log share a
    /// single request-owned transaction context.
    async fn execute_prepared(
        &self,
        context: &RelayRequestContext,
        mut request: Request,
    ) -> Response {
        let baseline = upstream_request_headers(context, request.headers());
        let headers = match self.provider_headers(context, &baseline).await {
            Ok(headers) => sanitize_provider_headers(&headers),
            Err(rejection) => return rejected(rejection),
        };
        *request.headers_mut() = headers;
        request.extensions_mut().insert(context.clone());

        let response = filtered_upstream_response(self.relay(context.protocol, request).await);
        let accounting = RelayAccounting {
            protocol: context.protocol,
            status: response.status(),
            upstream_succeeded: response.status().is_success(),
        };
        if let Err(response) = accepted(self.account(context, accounting).await) {
            return response;
        }
        response
    }
}

#[derive(Clone)]
pub struct RelayMiscHttpState {
    service: Arc<dyn RelayMiscService>,
}

impl RelayMiscHttpState {
    #[must_use]
    pub fn new(service: Arc<dyn RelayMiscService>) -> Self {
        Self { service }
    }
}

/// Complete miscellaneous relay surface for candidate and integration roots.
///
/// Route ownership is split between the production `active` and `frozen`
/// modules so the migration ledger can distinguish provider-capable paths from
/// legacy unavailable endpoints without declaring either route twice.
/// Included legacy paths:
/// - pass-through: alpha search, embeddings, rerank, and moderations;
/// - conditionally-501 endpoints: files, fine-tunes, and image variations.
///
/// The latter are 501 only after every legacy gate accepts them; auth,
/// rate-limit, malformed-model, and no-channel responses take precedence.
pub fn routes(state: RelayMiscHttpState) -> Router {
    super::relay_misc_active::router(state.clone()).merge(super::relay_misc_frozen::router(state))
}

pub async fn alpha_search(State(state): State<RelayMiscHttpState>, request: Request) -> Response {
    relay(state, RelayProtocol::AlphaSearch, request).await
}

pub async fn embeddings(State(state): State<RelayMiscHttpState>, request: Request) -> Response {
    relay(state, RelayProtocol::Embedding, request).await
}

pub async fn rerank(State(state): State<RelayMiscHttpState>, request: Request) -> Response {
    relay(state, RelayProtocol::Rerank, request).await
}

pub async fn moderations(State(state): State<RelayMiscHttpState>, request: Request) -> Response {
    relay(state, RelayProtocol::OpenAi, request).await
}

async fn relay(state: RelayMiscHttpState, protocol: RelayProtocol, request: Request) -> Response {
    execute(state, protocol, request, false).await
}

async fn execute(
    state: RelayMiscHttpState,
    protocol: RelayProtocol,
    mut request: Request,
    frozen_not_implemented: bool,
) -> Response {
    let body_encoding = relay_body_encoding(request.headers());
    let deferred_decode = matches!(
        body_encoding,
        RelayBodyEncoding::Brotli | RelayBodyEncoding::Zstd
    );
    if deferred_decode {
        // Current Go installs br/zstd readers and removes the transport header
        // before entering authentication, but those readers surface malformed
        // input only when Distribute later parses the body. Retain that gate
        // ordering while keeping the encoded bytes request-local.
        request.headers_mut().remove(header::CONTENT_ENCODING);
        request
            .extensions_mut()
            .insert(DeferredRelayBodyEncoding(body_encoding));
    } else {
        request = match decoded_request(state.service.as_ref(), request).await {
            Ok(request) => request,
            Err(response) => return response,
        };
    };
    if let Err(response) = accepted(state.service.system_performance(&request).await) {
        return response;
    }
    if let Err(response) = accepted(state.service.authorize_prepared(&mut request).await) {
        return response;
    }
    if let Err(response) = accepted(state.service.model_rate_limit_prepared(&mut request).await) {
        return response;
    }
    if deferred_decode {
        request = match decoded_request(state.service.as_ref(), request).await {
            Ok(request) => request,
            Err(response) => return response,
        };
    }
    let context = match request_context(protocol, &request) {
        Ok(context) => context,
        Err(response) => return response,
    };
    if let Err(response) = accepted(
        state
            .service
            .distribute_prepared(&context, &mut request)
            .await,
    ) {
        return response;
    }
    if frozen_not_implemented {
        return not_implemented_response();
    }

    state.service.execute_prepared(&context, request).await
}

/// Recreates the global legacy decompression boundary. Encoded bytes are never
/// forwarded with `Content-Encoding` removed: the header is deleted only after
/// the injected decoder succeeds, and the 128 MiB cap is checked again on the
/// decompressed representation.
async fn decoded_request(
    service: &dyn RelayMiscService,
    request: Request,
) -> Result<Request, Response> {
    let (mut parts, body) = request.into_parts();
    let encoded = to_bytes(body, MAX_RELAY_BODY_BYTES + 1)
        .await
        .map_err(|_| {
            legacy_request_error(StatusCode::PAYLOAD_TOO_LARGE, "request body too large")
        })?;
    if encoded.len() > MAX_RELAY_BODY_BYTES {
        return Err(legacy_request_error(
            StatusCode::PAYLOAD_TOO_LARGE,
            "request body too large",
        ));
    }
    let encoding = parts
        .extensions
        .remove::<DeferredRelayBodyEncoding>()
        .map_or_else(
            || relay_body_encoding(&parts.headers),
            |encoding| encoding.0,
        );
    let decoded = match service.decode_body(encoding, encoded).await {
        Ok(decoded) => decoded,
        Err(RelayAuth::Rejected { message, .. })
            if matches!(
                encoding,
                RelayBodyEncoding::Brotli | RelayBodyEncoding::Zstd
            ) =>
        {
            let request_id = relay_request_id(&parts);
            return Err(rejected(RelayAuth::RejectedOpenAi {
                status: StatusCode::BAD_REQUEST,
                message: format!(
                    "Invalid request: Invalid request: {message} (request id: {request_id})"
                ),
                code: String::new(),
            }));
        }
        Err(_) => {
            // Go's gzip middleware aborts before the relay/auth chain and
            // writes an empty 400 response when the gzip header is malformed.
            // Preserve that empty body through the listener-wide JSON normalizer
            // without adding a content-type header Go does not emit.
            return Err(crate::legacy_empty_response(StatusCode::BAD_REQUEST, None));
        }
    };
    if decoded.len() > MAX_RELAY_BODY_BYTES {
        return Err(legacy_request_error(
            StatusCode::PAYLOAD_TOO_LARGE,
            "request body too large",
        ));
    }
    if encoding != RelayBodyEncoding::Identity {
        parts.headers.remove(header::CONTENT_ENCODING);
    }
    parts.extensions.insert(PreparedRelayBody(decoded.clone()));
    Ok(Request::from_parts(parts, Body::from(decoded)))
}

fn relay_body_encoding(headers: &HeaderMap) -> RelayBodyEncoding {
    match headers
        .get(header::CONTENT_ENCODING)
        .and_then(|value| value.to_str().ok())
    {
        Some("gzip") => RelayBodyEncoding::Gzip,
        Some("br") => RelayBodyEncoding::Brotli,
        Some("zstd") => RelayBodyEncoding::Zstd,
        _ => RelayBodyEncoding::Identity,
    }
}

#[derive(Clone, Copy)]
struct DeferredRelayBodyEncoding(RelayBodyEncoding);

fn relay_request_id(parts: &axum::http::request::Parts) -> String {
    parts.extensions.get::<crate::RequestContext>().map_or_else(
        || {
            parts
                .headers
                .get("x-oneapi-request-id")
                .and_then(|value| value.to_str().ok())
                .unwrap_or("unknown")
                .to_owned()
        },
        |context| context.request_id.clone(),
    )
}

fn decode_body_bytes(encoding: RelayBodyEncoding, body: Bytes) -> Result<Bytes, DecodeBodyError> {
    match encoding {
        RelayBodyEncoding::Identity => Ok(body),
        RelayBodyEncoding::Gzip => read_limited(GzDecoder::new(Cursor::new(body))),
        RelayBodyEncoding::Brotli => {
            let compressed = body.clone();
            read_limited(Decompressor::new(Cursor::new(body), 4096)).map_err(|error| match error {
                DecodeBodyError::TooLarge => DecodeBodyError::TooLarge,
                DecodeBodyError::Invalid
                | DecodeBodyError::Brotli(_)
                | DecodeBodyError::Zstd(_) => {
                    DecodeBodyError::Brotli(go_brotli_decode_error(&compressed))
                }
            })
        }
        RelayBodyEncoding::Zstd => decode_zstd(body),
    }
}

fn read_limited(reader: impl Read) -> Result<Bytes, DecodeBodyError> {
    let mut output = Vec::new();
    let mut limited = reader.take((MAX_RELAY_BODY_BYTES + 1) as u64);
    limited
        .read_to_end(&mut output)
        .map_err(|_| DecodeBodyError::Invalid)?;
    if output.len() > MAX_RELAY_BODY_BYTES {
        return Err(DecodeBodyError::TooLarge);
    }
    Ok(Bytes::from(output))
}

fn decode_zstd(body: Bytes) -> Result<Bytes, DecodeBodyError> {
    let compressed = body.clone();
    let decoder = ZstdDecoder::new(Cursor::new(body)).map_err(|error| {
        DecodeBodyError::Zstd(go_zstd_decode_error(&compressed, &error.to_string()))
    })?;
    let mut output = Vec::new();
    let mut limited = decoder.take((MAX_RELAY_BODY_BYTES + 1) as u64);
    limited.read_to_end(&mut output).map_err(|error| {
        DecodeBodyError::Zstd(go_zstd_decode_error(&compressed, &error.to_string()))
    })?;
    if output.len() > MAX_RELAY_BODY_BYTES {
        return Err(DecodeBodyError::TooLarge);
    }
    Ok(Bytes::from(output))
}

fn go_zstd_decode_error(input: &[u8], rust_error: &str) -> String {
    // klauspost/zstd accepts both ordinary and skippable frames. Walk past
    // complete skippable frames before classifying the next frame prefix so
    // malformed input gets the same stable error as current Go, independent
    // of the libzstd wording used by the Rust decoder.
    let mut remaining = input;
    loop {
        if remaining.is_empty() {
            return "unexpected EOF".to_owned();
        }
        if remaining.len() < 4 {
            return "unexpected EOF".to_owned();
        }

        let magic = u32::from_le_bytes([remaining[0], remaining[1], remaining[2], remaining[3]]);
        if magic == 0xfd2f_b528 {
            break;
        }
        if (0x184d_2a50..=0x184d_2a5f).contains(&magic) {
            if remaining.len() < 8 {
                return "unexpected EOF".to_owned();
            }
            let payload_len =
                u32::from_le_bytes([remaining[4], remaining[5], remaining[6], remaining[7]])
                    as usize;
            let Some(frame_len) = 8_usize.checked_add(payload_len) else {
                return "unexpected EOF".to_owned();
            };
            if remaining.len() < frame_len {
                return "unexpected EOF".to_owned();
            }
            remaining = &remaining[frame_len..];
            continue;
        }
        return "invalid input: magic number mismatch".to_owned();
    }

    let normalized = rust_error.to_ascii_lowercase();
    if normalized.contains("unknown frame descriptor") {
        "invalid input: magic number mismatch".to_owned()
    } else if normalized.contains("frame requires too much memory") {
        "window size exceeded".to_owned()
    } else if normalized.contains("dictionary mismatch") {
        "unknown dictionary".to_owned()
    } else if normalized.contains("doesn't match checksum") || normalized.contains("checksum") {
        "CRC check failed".to_owned()
    } else if normalized.contains("src size is incorrect")
        || normalized.contains("unexpected eof")
        || normalized.contains("incomplete")
    {
        "unexpected EOF".to_owned()
    } else {
        rust_error.to_owned()
    }
}

fn go_brotli_decode_error(input: &[u8]) -> String {
    let mut state = BrotliState::new(
        StandardAlloc::default(),
        StandardAlloc::default(),
        StandardAlloc::default(),
    );
    let mut available_in = input.len();
    let mut input_offset = 0;
    let mut total_out = 0;
    let mut output = [0_u8; 4096];

    loop {
        let before = (available_in, input_offset, total_out);
        let mut available_out = output.len();
        let mut output_offset = 0;
        match BrotliDecompressStream(
            &mut available_in,
            &mut input_offset,
            input,
            &mut available_out,
            &mut output_offset,
            &mut output,
            &mut total_out,
            &mut state,
        ) {
            BrotliResult::ResultFailure => {
                let raw = format!("{:?}", state.error_code);
                let code = raw
                    .strip_prefix("BROTLI_DECODER_ERROR_FORMAT_")
                    .or_else(|| raw.strip_prefix("BROTLI_DECODER_ERROR_ALLOC_"))
                    .or_else(|| raw.strip_prefix("BROTLI_DECODER_ERROR_"))
                    .unwrap_or("INVALID");
                return format!("brotli: {code}");
            }
            BrotliResult::ResultSuccess => {
                return if available_in == 0 {
                    "brotli: INVALID".to_owned()
                } else {
                    "brotli: excessive input".to_owned()
                };
            }
            BrotliResult::NeedsMoreInput if available_in == 0 => {
                return "unexpected EOF".to_owned();
            }
            BrotliResult::NeedsMoreInput | BrotliResult::NeedsMoreOutput => {
                if before == (available_in, input_offset, total_out) {
                    return "brotli: invalid state".to_owned();
                }
            }
        }
    }
}

#[derive(Clone)]
struct PreparedRelayBody(Bytes);

#[derive(Deserialize)]
struct ModelRequest {
    model: Option<serde_json::Value>,
    stream: Option<bool>,
}

// Relay authentication failures carry the exact legacy HTTP response through
// this parsing boundary, including localized payloads and response headers.
#[allow(clippy::result_large_err)]
fn request_context(
    protocol: RelayProtocol,
    request: &Request,
) -> Result<RelayRequestContext, Response> {
    let path = request.uri().path().to_owned();
    let bytes = request
        .extensions()
        .get::<PreparedRelayBody>()
        .map_or_else(Bytes::new, |body| body.0.clone());
    let parsed = if bytes.is_empty() {
        ModelRequest {
            model: None,
            stream: None,
        }
    } else {
        serde_json::from_slice::<ModelRequest>(&bytes).map_err(|_| {
            legacy_request_error(StatusCode::BAD_REQUEST, "invalid JSON request body")
        })?
    };
    let body_model = match parsed.model {
        Some(serde_json::Value::String(model)) => Some(model),
        Some(serde_json::Value::Null) | None => None,
        Some(_) => {
            return Err(legacy_request_error(
                StatusCode::BAD_REQUEST,
                "field model must be a string",
            ));
        }
    };
    let model = body_model.or_else(|| {
        (protocol == RelayProtocol::OpenAi && path == "/v1/moderations")
            .then(|| "text-moderation-stable".to_owned())
    });
    Ok(RelayRequestContext {
        protocol,
        path,
        model,
        stream: parsed.stream.unwrap_or(false),
    })
}

fn upstream_request_headers(context: &RelayRequestContext, headers: &HeaderMap) -> HeaderMap {
    let mut forwarded = filter_headers(headers, |name| {
        matches!(name.as_str(), "accept" | "content-type")
    });
    if context.stream && !forwarded.contains_key(header::ACCEPT) {
        forwarded.insert(
            header::ACCEPT,
            axum::http::HeaderValue::from_static("text/event-stream"),
        );
    }
    forwarded
}

fn sanitize_provider_headers(headers: &HeaderMap) -> HeaderMap {
    let connection_named = connection_named_headers(headers);
    filter_headers(headers, |name| {
        !connection_named.iter().any(|candidate| candidate == name) && !is_hop_by_hop(name)
    })
}

pub(super) fn filtered_upstream_response(mut response: Response) -> Response {
    let connection_named = connection_named_headers(response.headers());
    let headers = filter_headers(response.headers(), |name| {
        !connection_named
            .iter()
            .any(|connection_name| connection_name == name)
            && !is_hop_by_hop(name)
    });
    *response.headers_mut() = headers;
    response
}

fn connection_named_headers(headers: &HeaderMap) -> Vec<HeaderName> {
    headers
        .get_all(header::CONNECTION)
        .iter()
        .filter_map(|value| value.to_str().ok())
        .flat_map(|value| value.split(','))
        .filter_map(|value| HeaderName::try_from(value.trim()).ok())
        .collect()
}

fn is_hop_by_hop(name: &HeaderName) -> bool {
    matches!(
        name.as_str(),
        "connection"
            | "keep-alive"
            | "proxy-authenticate"
            | "proxy-authorization"
            | "te"
            | "trailer"
            | "transfer-encoding"
            | "upgrade"
    )
}

fn filter_headers(headers: &HeaderMap, include: impl Fn(&HeaderName) -> bool) -> HeaderMap {
    headers
        .iter()
        .filter(|(name, _)| include(name))
        .map(|(name, value)| (name.clone(), value.clone()))
        .collect()
}

async fn not_implemented(State(state): State<RelayMiscHttpState>, request: Request) -> Response {
    execute(state, RelayProtocol::OpenAi, request, true).await
}

/// Public adapter for the frozen file/fine-tune/image-variation mounts.
///
/// The normal listener composes these routes in a separate router so the
/// production route ledger can distinguish the explicit legacy 501 boundary
/// from the four active relay protocols.  The shared executor still performs
/// the same auth, performance, model-limit, and distribution ordering before
/// selecting the frozen response.
pub async fn legacy_not_implemented(
    State(state): State<RelayMiscHttpState>,
    request: Request,
) -> Response {
    not_implemented(State(state), request).await
}

fn not_implemented_response() -> Response {
    let mut response = (
        StatusCode::NOT_IMPLEMENTED,
        Json(LegacyNotImplementedEnvelope {
            error: LegacyOpenAiError {
                message: "API not implemented",
                kind: "new_api_error",
                param: "",
                code: "api_not_implemented",
            },
        }),
    )
        .into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        axum::http::HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

// The response is intentionally propagated intact to the mounted route.
#[allow(clippy::result_large_err)]
fn accepted(outcome: RelayAuth) -> Result<(), Response> {
    match outcome {
        RelayAuth::Authorized => Ok(()),
        rejection => Err(rejected(rejection)),
    }
}

fn rejected(rejection: RelayAuth) -> Response {
    match rejection {
        RelayAuth::Authorized => legacy_request_error(
            StatusCode::INTERNAL_SERVER_ERROR,
            "invalid authorized rejection",
        ),
        RelayAuth::Rejected { status, message } => legacy_auth_error(status, message),
        RelayAuth::ConcealedNotFound => (
            StatusCode::NOT_FOUND,
            Json(ConcealedNotFoundEnvelope {
                message: "Not Found",
            }),
        )
            .into_response(),
        RelayAuth::RejectedOpenAi {
            status,
            message,
            code,
        } => current_openai_response(
            (
                status,
                Json(CurrentOpenAiErrorEnvelope {
                    error: CurrentOpenAiError {
                        code: &code,
                        message: &message,
                        kind: "new_api_error",
                    },
                }),
            )
                .into_response(),
        ),
        RelayAuth::RejectedOpenAiWithParam {
            status,
            message,
            code,
        } => current_openai_response(
            (
                status,
                Json(LegacyErrorEnvelope {
                    error: LegacyOpenAiError {
                        message: &message,
                        kind: "new_api_error",
                        param: "",
                        code,
                    },
                }),
            )
                .into_response(),
        ),
    }
}

fn current_openai_response(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        axum::http::HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn missing_stage(stage: &'static str) -> RelayAuth {
    RelayAuth::Rejected {
        status: StatusCode::SERVICE_UNAVAILABLE,
        message: format!("Rust relay-misc {stage} adapter is unavailable"),
    }
}

fn legacy_auth_error(status: StatusCode, message: String) -> Response {
    (
        status,
        Json(LegacyErrorEnvelope {
            error: LegacyOpenAiError {
                message: &message,
                kind: "new_api_error",
                param: "",
                code: "",
            },
        }),
    )
        .into_response()
}

fn legacy_request_error(status: StatusCode, message: &'static str) -> Response {
    (
        status,
        Json(LegacyErrorEnvelope {
            error: LegacyOpenAiError {
                message,
                kind: "invalid_request_error",
                param: "",
                code: "",
            },
        }),
    )
        .into_response()
}

#[derive(Serialize)]
struct LegacyNotImplementedEnvelope {
    error: LegacyOpenAiError<&'static str>,
}

#[derive(Serialize)]
struct LegacyErrorEnvelope<'a> {
    error: LegacyOpenAiError<&'a str>,
}

#[derive(Serialize)]
struct ConcealedNotFoundEnvelope {
    message: &'static str,
}

#[derive(Serialize)]
struct CurrentOpenAiErrorEnvelope<'a> {
    error: CurrentOpenAiError<'a>,
}

#[derive(Serialize)]
struct CurrentOpenAiError<'a> {
    code: &'a str,
    message: &'a str,
    #[serde(rename = "type")]
    kind: &'static str,
}

#[derive(Serialize)]
struct LegacyOpenAiError<T: Serialize> {
    message: T,
    #[serde(rename = "type")]
    kind: &'static str,
    param: &'static str,
    code: &'static str,
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::io::Write;
    use std::sync::atomic::{AtomicUsize, Ordering};

    use axum::{
        body::Body,
        http::{HeaderValue, Request as HttpRequest},
    };
    use tower::ServiceExt;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[cfg(test)]
    use axum::http::header;

    #[derive(Clone)]
    struct TestService {
        auth: RelayAuth,
        status: StatusCode,
        headers: Vec<(&'static str, &'static str)>,
        body: Vec<u8>,
    }

    impl TestService {
        fn allowed() -> RelayAuth {
            RelayAuth::Authorized
        }
    }

    #[async_trait]
    impl RelayMiscService for TestService {
        async fn system_performance(&self, _: &Request) -> RelayAuth {
            Self::allowed()
        }
        async fn authorize(&self, _: &Request) -> RelayAuth {
            self.auth.clone()
        }
        async fn model_rate_limit(&self, _: &Request) -> RelayAuth {
            Self::allowed()
        }
        async fn distribute(&self, _: &RelayRequestContext, _: &Request) -> RelayAuth {
            Self::allowed()
        }
        async fn provider_headers(
            &self,
            _: &RelayRequestContext,
            headers: &HeaderMap,
        ) -> Result<HeaderMap, RelayAuth> {
            Ok(headers.clone())
        }
        async fn relay(&self, _: RelayProtocol, _: Request) -> Response {
            let mut reply = Response::new(Body::from(self.body.clone()));
            *reply.status_mut() = self.status;
            for (name, value) in &self.headers {
                reply
                    .headers_mut()
                    .insert(*name, HeaderValue::from_static(value));
            }
            reply
        }
        async fn account(&self, _: &RelayRequestContext, _: RelayAccounting) -> RelayAuth {
            RelayAuth::Authorized
        }
    }

    fn app(
        auth: RelayAuth,
        status: StatusCode,
        headers: Vec<(&'static str, &'static str)>,
        body: Vec<u8>,
    ) -> Router {
        routes(RelayMiscHttpState::new(Arc::new(TestService {
            auth,
            status,
            headers,
            body,
        })))
    }

    #[test]
    fn route_construction_has_no_duplicate_misc_paths() {
        let result = std::panic::catch_unwind(|| {
            let _router = app(RelayAuth::Authorized, StatusCode::OK, vec![], vec![]);
        });
        assert!(result.is_ok());
    }

    #[test]
    fn request_decoder_supports_legacy_encodings_and_rejects_invalid_input() -> TestResult {
        let plain = Bytes::from_static(br#"{"model":"fixture"}"#);

        let mut gzip = flate2::write::GzEncoder::new(Vec::new(), flate2::Compression::default());
        gzip.write_all(&plain)?;
        let gzip = gzip.finish()?;
        assert_eq!(
            decode_body_bytes(RelayBodyEncoding::Gzip, gzip.into())?,
            plain
        );

        let mut brotli = brotli::CompressorWriter::new(Vec::new(), 4096, 5, 22);
        brotli.write_all(&plain)?;
        let brotli = brotli.into_inner();
        assert_eq!(
            decode_body_bytes(RelayBodyEncoding::Brotli, brotli.into())?,
            plain
        );

        let zstd = zstd::stream::encode_all(plain.as_ref(), 0)?;
        assert_eq!(
            decode_body_bytes(RelayBodyEncoding::Zstd, zstd.into())?,
            plain
        );
        assert!(
            decode_body_bytes(RelayBodyEncoding::Gzip, Bytes::from_static(b"not-gzip")).is_err()
        );
        assert_eq!(
            decode_body_bytes(
                RelayBodyEncoding::Brotli,
                Bytes::from_static(b"not-a-valid-compressed-stream"),
            ),
            Err(DecodeBodyError::Brotli("brotli: PADDING_2".to_owned()))
        );
        assert_eq!(
            decode_body_bytes(
                RelayBodyEncoding::Zstd,
                Bytes::from_static(b"not-a-valid-compressed-stream"),
            ),
            Err(DecodeBodyError::Zstd(
                "invalid input: magic number mismatch".to_owned()
            ))
        );
        Ok(())
    }

    #[tokio::test]
    async fn malformed_lazy_decoders_keep_current_go_distributor_error_shape() -> TestResult {
        for (encoding, message) in [
            ("br", "brotli: PADDING_2"),
            ("zstd", "invalid input: magic number mismatch"),
        ] {
            let response = app(RelayAuth::Authorized, StatusCode::OK, vec![], vec![])
                .oneshot(
                    HttpRequest::post("/v1/embeddings")
                        .header(header::CONTENT_TYPE, "application/json")
                        .header(header::CONTENT_ENCODING, encoding)
                        .header("x-oneapi-request-id", "fixture-request")
                        .body(Body::from("not-a-valid-compressed-stream"))?,
                )
                .await?;
            assert_eq!(response.status(), StatusCode::BAD_REQUEST);
            assert_eq!(
                response.headers()[header::CONTENT_TYPE],
                "application/json; charset=utf-8"
            );
            let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
            let expected = format!(
                r#"{{"error":{{"code":"","message":"Invalid request: Invalid request: {message} (request id: fixture-request)","type":"new_api_error"}}}}"#
            );
            assert_eq!(body.as_ref(), expected.as_bytes(),);
        }
        Ok(())
    }

    #[tokio::test]
    async fn malformed_lazy_decoders_run_after_authentication_like_current_go() -> TestResult {
        for encoding in ["br", "zstd"] {
            let response = app(
                RelayAuth::Rejected {
                    status: StatusCode::UNAUTHORIZED,
                    message: "authentication runs before lazy decoding".to_owned(),
                },
                StatusCode::OK,
                vec![],
                vec![],
            )
            .oneshot(
                HttpRequest::post("/v1/embeddings")
                    .header(header::CONTENT_TYPE, "application/json")
                    .header(header::CONTENT_ENCODING, encoding)
                    .body(Body::from("not-a-valid-compressed-stream"))?,
            )
            .await?;
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        }
        Ok(())
    }

    #[derive(Clone)]
    struct AuthMarker(String);

    #[derive(Clone)]
    struct ChannelMarker(String);

    struct RequestLocalStateService;

    #[async_trait]
    impl RelayMiscService for RequestLocalStateService {
        async fn system_performance(&self, _: &Request) -> RelayAuth {
            RelayAuth::Authorized
        }

        async fn authorize(&self, _: &Request) -> RelayAuth {
            RelayAuth::Rejected {
                status: StatusCode::INTERNAL_SERVER_ERROR,
                message: "immutable compatibility authorization hook was called".to_owned(),
            }
        }

        async fn authorize_prepared(&self, request: &mut Request) -> RelayAuth {
            let marker = request
                .headers()
                .get("x-auth-marker")
                .and_then(|value| value.to_str().ok())
                .unwrap_or_default()
                .to_owned();
            request.extensions_mut().insert(AuthMarker(marker));
            RelayAuth::Authorized
        }

        async fn model_rate_limit_prepared(&self, request: &mut Request) -> RelayAuth {
            if request.extensions().get::<AuthMarker>().is_none() {
                return RelayAuth::Rejected {
                    status: StatusCode::INTERNAL_SERVER_ERROR,
                    message: "authenticated request marker is unavailable".to_owned(),
                };
            }
            RelayAuth::Authorized
        }

        async fn distribute(&self, _: &RelayRequestContext, _: &Request) -> RelayAuth {
            RelayAuth::Rejected {
                status: StatusCode::INTERNAL_SERVER_ERROR,
                message: "immutable compatibility distribution hook was called".to_owned(),
            }
        }

        async fn distribute_prepared(
            &self,
            context: &RelayRequestContext,
            request: &mut Request,
        ) -> RelayAuth {
            let Some(auth) = request
                .extensions()
                .get::<AuthMarker>()
                .map(|marker| marker.0.clone())
            else {
                return RelayAuth::Rejected {
                    status: StatusCode::INTERNAL_SERVER_ERROR,
                    message: "authenticated request marker is unavailable".to_owned(),
                };
            };
            request
                .extensions_mut()
                .insert(ChannelMarker(format!("{auth}:{}", context.path)));
            RelayAuth::Authorized
        }

        async fn relay(&self, _: RelayProtocol, _: Request) -> Response {
            StatusCode::INTERNAL_SERVER_ERROR.into_response()
        }

        async fn execute_prepared(&self, _: &RelayRequestContext, request: Request) -> Response {
            let Some(channel) = request
                .extensions()
                .get::<ChannelMarker>()
                .map(|marker| marker.0.clone())
            else {
                return StatusCode::INTERNAL_SERVER_ERROR.into_response();
            };
            Response::new(Body::from(channel))
        }
    }

    #[tokio::test]
    async fn mutable_hooks_keep_authenticated_and_selected_state_request_local() -> TestResult {
        let app = routes(RelayMiscHttpState::new(Arc::new(RequestLocalStateService)));
        let request = |marker: &'static str| {
            HttpRequest::post("/v1/embeddings")
                .header("x-auth-marker", marker)
                .header(header::CONTENT_TYPE, "application/json")
                .body(Body::from(r#"{"model":"fixture"}"#))
        };

        let first_request = request("principal-a")?;
        let second_request = request("principal-b")?;
        let (first, second) = tokio::join!(
            app.clone().oneshot(first_request),
            app.oneshot(second_request),
        );
        let first = first?;
        let second = second?;
        assert_eq!(
            axum::body::to_bytes(first.into_body(), usize::MAX).await?,
            "principal-a:/v1/embeddings"
        );
        assert_eq!(
            axum::body::to_bytes(second.into_body(), usize::MAX).await?,
            "principal-b:/v1/embeddings"
        );
        Ok(())
    }

    #[tokio::test]
    async fn unsupported_routes_keep_legacy_501_json_after_authentication() -> TestResult {
        let response = app(RelayAuth::Authorized, StatusCode::OK, vec![], vec![])
            .oneshot(HttpRequest::delete("/v1/files/file-1").body(Body::empty())?)
            .await?;
        assert_eq!(response.status(), StatusCode::NOT_IMPLEMENTED);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert_eq!(
            body,
            r#"{"error":{"message":"API not implemented","type":"new_api_error","param":"","code":"api_not_implemented"}}"#
        );
        Ok(())
    }

    #[tokio::test]
    async fn performance_errors_keep_current_go_param_and_field_order() -> TestResult {
        let response = rejected(RelayAuth::RejectedOpenAiWithParam {
            status: StatusCode::SERVICE_UNAVAILABLE,
            message: "system memory overloaded (current: 91.2%, threshold: 90%)".to_owned(),
            code: "system_memory_overloaded",
        });
        assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(
            response.headers()[header::CONTENT_TYPE],
            "application/json; charset=utf-8"
        );
        assert_eq!(
            axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            r#"{"error":{"message":"system memory overloaded (current: 91.2%, threshold: 90%)","type":"new_api_error","param":"","code":"system_memory_overloaded"}}"#
        );
        Ok(())
    }

    #[tokio::test]
    async fn authorization_failure_prevents_an_upstream_call() -> TestResult {
        let response = app(
            RelayAuth::Rejected {
                status: StatusCode::UNAUTHORIZED,
                message: "Invalid token".into(),
            },
            StatusCode::OK,
            vec![],
            b"must not be forwarded".to_vec(),
        )
        .oneshot(HttpRequest::post("/v1/rerank").body(Body::empty())?)
        .await?;
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
        let body = axum::body::to_bytes(response.into_body(), usize::MAX).await?;
        assert!(std::str::from_utf8(&body)?.contains("Invalid token"));
        Ok(())
    }

    #[tokio::test]
    async fn upstream_error_binary_body_and_headers_are_untouched() -> TestResult {
        let response = app(
            RelayAuth::Authorized,
            StatusCode::BAD_GATEWAY,
            vec![
                ("content-type", "audio/mpeg"),
                ("x-upstream-request-id", "up-1"),
            ],
            vec![0, 255, 7],
        )
        .oneshot(HttpRequest::post("/v1/embeddings").body(Body::empty())?)
        .await?;
        assert_eq!(response.status(), StatusCode::BAD_GATEWAY);
        assert_eq!(response.headers()[header::CONTENT_TYPE], "audio/mpeg");
        assert_eq!(response.headers()["x-upstream-request-id"], "up-1");
        assert_eq!(
            axum::body::to_bytes(response.into_body(), usize::MAX).await?,
            vec![0, 255, 7]
        );
        Ok(())
    }

    #[test]
    fn forwarded_request_headers_exclude_client_credentials_and_transport_state() {
        let mut headers = HeaderMap::new();
        headers.insert(header::ACCEPT, HeaderValue::from_static("application/json"));
        headers.insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        headers.insert(
            header::AUTHORIZATION,
            HeaderValue::from_static("Bearer client-secret"),
        );
        headers.insert(header::CONNECTION, HeaderValue::from_static("x-client-hop"));
        headers.insert("x-client-hop", HeaderValue::from_static("do-not-forward"));
        headers.insert("x-request-id", HeaderValue::from_static("request-1"));

        let forwarded = upstream_request_headers(
            &RelayRequestContext {
                protocol: RelayProtocol::Embedding,
                path: "/v1/embeddings".to_owned(),
                model: Some("text-embedding-3-small".to_owned()),
                stream: false,
            },
            &headers,
        );

        assert_eq!(forwarded[header::ACCEPT], "application/json");
        assert_eq!(forwarded[header::CONTENT_TYPE], "application/json");
        assert!(forwarded.get("x-request-id").is_none());
        assert!(forwarded.get(header::AUTHORIZATION).is_none());
        assert!(forwarded.get(header::CONNECTION).is_none());
        assert!(forwarded.get("x-client-hop").is_none());
    }

    #[test]
    fn upstream_response_headers_exclude_standard_and_connection_named_hops() {
        let mut response = Response::new(Body::empty());
        response.headers_mut().insert(
            header::CONNECTION,
            HeaderValue::from_static("x-provider-hop"),
        );
        response
            .headers_mut()
            .insert("x-provider-hop", HeaderValue::from_static("remove"));
        response.headers_mut().insert(
            header::CONTENT_TYPE,
            HeaderValue::from_static("application/json"),
        );
        response
            .headers_mut()
            .insert("x-upstream-request-id", HeaderValue::from_static("up-1"));

        let response = filtered_upstream_response(response);

        assert!(response.headers().get(header::CONNECTION).is_none());
        assert!(response.headers().get("x-provider-hop").is_none());
        assert_eq!(response.headers()[header::CONTENT_TYPE], "application/json");
        assert_eq!(response.headers()["x-upstream-request-id"], "up-1");
    }

    struct LoopbackService {
        base_url: String,
        accounted: AtomicUsize,
    }

    #[async_trait]
    impl RelayMiscService for LoopbackService {
        async fn system_performance(&self, _: &Request) -> RelayAuth {
            RelayAuth::Authorized
        }

        async fn authorize(&self, _: &Request) -> RelayAuth {
            RelayAuth::Authorized
        }

        async fn model_rate_limit(&self, _: &Request) -> RelayAuth {
            RelayAuth::Authorized
        }

        async fn distribute(&self, context: &RelayRequestContext, _: &Request) -> RelayAuth {
            if context.path == "/v1/moderations" {
                assert_eq!(context.model.as_deref(), Some("text-moderation-stable"));
            }
            RelayAuth::Authorized
        }

        async fn provider_headers(
            &self,
            context: &RelayRequestContext,
            headers: &HeaderMap,
        ) -> Result<HeaderMap, RelayAuth> {
            let mut headers = headers.clone();
            headers.insert(
                header::AUTHORIZATION,
                HeaderValue::from_static("Bearer provider-owned-secret"),
            );
            if context.protocol == RelayProtocol::AlphaSearch {
                headers.insert("x-fixture-mode", HeaderValue::from_static("sse"));
            } else if context.protocol == RelayProtocol::Rerank {
                headers.insert("x-fixture-mode", HeaderValue::from_static("error"));
            }
            Ok(headers)
        }

        async fn relay(&self, _: RelayProtocol, request: Request) -> Response {
            let Some(context) = request.extensions().get::<RelayRequestContext>().cloned() else {
                return StatusCode::INTERNAL_SERVER_ERROR.into_response();
            };
            let (parts, body) = request.into_parts();
            let body = match to_bytes(body, MAX_RELAY_BODY_BYTES).await {
                Ok(body) => body,
                Err(_) => return StatusCode::PAYLOAD_TOO_LARGE.into_response(),
            };
            let client = reqwest::Client::new();
            let mut outbound = client
                .post(format!("{}{}", self.base_url, context.path))
                .body(body);
            for (name, value) in &parts.headers {
                outbound = outbound.header(name, value);
            }
            let upstream = match outbound.send().await {
                Ok(upstream) => upstream,
                Err(_) => return StatusCode::BAD_GATEWAY.into_response(),
            };
            let status = upstream.status();
            let headers = upstream.headers().clone();
            let mut response = Response::new(Body::from_stream(upstream.bytes_stream()));
            *response.status_mut() = status;
            *response.headers_mut() = headers;
            response
        }

        async fn account(&self, _: &RelayRequestContext, _: RelayAccounting) -> RelayAuth {
            self.accounted.fetch_add(1, Ordering::SeqCst);
            RelayAuth::Authorized
        }
    }

    /// Invoked by `relay-misc-differential.sh` against its loopback-only
    /// provider. It is ignored so ordinary unit tests never perform network I/O.
    #[tokio::test]
    #[ignore = "requires the differential runner's loopback provider"]
    async fn loopback_provider_contract() -> TestResult {
        let base_url = std::env::var("LMM_RELAY_MISC_PROVIDER_URL")?;
        assert!(base_url.starts_with("http://127.0.0.1:"));
        let service = Arc::new(LoopbackService {
            base_url,
            accounted: AtomicUsize::new(0),
        });
        let app = routes(RelayMiscHttpState::new(service.clone()));
        let cases = [
            (
                "/v1/alpha/search",
                r#"{"model":"gpt-test","query":"hello","stream":true}"#,
                StatusCode::OK,
                Some("text/event-stream"),
            ),
            (
                "/v1/embeddings",
                r#"{"model":"text-embedding-3-small","input":"hello"}"#,
                StatusCode::OK,
                Some("application/json"),
            ),
            (
                "/v1/rerank",
                r#"{"model":"rerank-v3","query":"hello","documents":["hello"]}"#,
                StatusCode::TOO_MANY_REQUESTS,
                Some("application/json"),
            ),
            (
                "/v1/moderations",
                r#"{"input":"hello"}"#,
                StatusCode::OK,
                Some("application/json"),
            ),
        ];
        for (path, body, expected_status, expected_content_type) in cases {
            let response = app
                .clone()
                .oneshot(
                    HttpRequest::post(path)
                        .header(header::AUTHORIZATION, "Bearer caller-secret")
                        .header(header::CONTENT_TYPE, "application/json")
                        .body(Body::from(body))?,
                )
                .await?;
            assert_eq!(response.status(), expected_status, "{path}");
            assert_eq!(
                response
                    .headers()
                    .get(header::CONTENT_TYPE)
                    .and_then(|value| value.to_str().ok()),
                expected_content_type,
                "{path}",
            );
            let _body = to_bytes(response.into_body(), MAX_RELAY_BODY_BYTES).await?;
        }
        assert_eq!(service.accounted.load(Ordering::SeqCst), cases.len());
        Ok(())
    }
}
