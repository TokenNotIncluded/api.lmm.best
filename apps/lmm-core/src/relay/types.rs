//! Relay-local contracts. Routing and money implementations live outside this module.
use std::{fmt, future::Future, pin::Pin, time::Duration};

use reqwest::{Url, header::HeaderValue};
use serde::Serialize;
use serde_json::{Value, json};

pub type RelayFuture<'a, T> = Pin<Box<dyn Future<Output = Result<T, RelayError>> + Send + 'a>>;

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum ClientProtocol {
    Chat,
    Responses,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum UpstreamProtocol {
    Chat,
    Responses,
    Anthropic,
    Gemini,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Phase {
    Route,
    Reserve,
    Connect,
    Headers,
    Read,
    Write,
    Total,
    Settle,
}

/// Public errors deliberately contain neither upstream bodies nor credentials/URLs.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RelayError {
    Invalid(&'static str),
    Unsupported(&'static str),
    Protocol(&'static str),
    Limit(&'static str),
    Timeout(Phase),
    Cancelled,
    Draining,
    Busy,
    RouteUnavailable,
    BillingUnavailable,
    UpstreamStatus(u16),
    UpstreamConnection,
    Truncated,
    SettlementPending,
}

impl RelayError {
    pub fn code(&self) -> &'static str {
        match self {
            Self::Invalid(_) => "invalid_request",
            Self::Unsupported(_) => "unsupported_feature",
            Self::Protocol(_) | Self::Truncated => "invalid_upstream_response",
            Self::Limit(_) => "relay_limit_exceeded",
            Self::Timeout(_) => "relay_timeout",
            Self::Cancelled => "request_cancelled",
            Self::Draining => "relay_draining",
            Self::Busy => "relay_busy",
            Self::RouteUnavailable => "route_unavailable",
            Self::BillingUnavailable => "billing_unavailable",
            Self::UpstreamStatus(_) => "upstream_error",
            Self::UpstreamConnection => "upstream_connection_error",
            Self::SettlementPending => "settlement_pending",
        }
    }
    pub fn status(&self) -> u16 {
        match self {
            Self::Invalid(_) | Self::Unsupported(_) => 400,
            Self::Busy => 429,
            Self::Limit(_) => 413,
            Self::Timeout(_) => 504,
            Self::Cancelled => 499,
            Self::Draining
            | Self::RouteUnavailable
            | Self::BillingUnavailable
            | Self::SettlementPending => 503,
            _ => 502,
        }
    }
    pub fn json(&self) -> Value {
        json!({"error":{"type":self.code(),"code":self.code(),"message":self.to_string()}})
    }
}
impl fmt::Display for RelayError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Invalid(s) | Self::Unsupported(s) | Self::Protocol(s) | Self::Limit(s) => {
                write!(f, "{}: {s}", self.code())
            }
            Self::Timeout(p) => write!(f, "relay_timeout: {p:?}"),
            Self::UpstreamStatus(s) => write!(f, "upstream_error: HTTP {s}"),
            _ => f.write_str(self.code()),
        }
    }
}
impl std::error::Error for RelayError {}

#[derive(Clone, Debug)]
pub struct Limits {
    pub max_in_flight: usize,
    pub queue_chunks: usize,
    pub chunk_bytes: usize,
    pub request_bytes: usize,
    pub event_bytes: usize,
    pub output_bytes: usize,
    pub upstream_bytes: usize,
    pub transport_chunk_bytes: usize,
    pub max_blocks: usize,
    pub tool_argument_bytes: usize,
    pub max_output_tokens: u32,
    pub connect_timeout: Duration,
    pub header_timeout: Duration,
    pub read_timeout: Duration,
    pub write_timeout: Duration,
    pub total_timeout: Duration,
    pub hook_timeout: Duration,
    /// Only connect failures, before an HTTP request is sent, may be retried.
    pub connect_retries: u8,
    pub retry_backoff: Duration,
}
impl Default for Limits {
    fn default() -> Self {
        Self {
            max_in_flight: 16,
            queue_chunks: 8,
            chunk_bytes: 16 * 1024,
            request_bytes: 2 * 1024 * 1024,
            event_bytes: 256 * 1024,
            output_bytes: 1024 * 1024,
            upstream_bytes: 64 * 1024 * 1024,
            transport_chunk_bytes: 1024 * 1024,
            max_blocks: 128,
            tool_argument_bytes: 256 * 1024,
            max_output_tokens: 65_536,
            connect_timeout: Duration::from_secs(5),
            header_timeout: Duration::from_secs(30),
            read_timeout: Duration::from_secs(30),
            write_timeout: Duration::from_secs(10),
            total_timeout: Duration::from_secs(300),
            hook_timeout: Duration::from_secs(10),
            connect_retries: 0,
            retry_backoff: Duration::from_millis(200),
        }
    }
}
impl Limits {
    pub(crate) fn validate(&self) -> Result<(), RelayError> {
        let sizes = [
            self.max_in_flight,
            self.queue_chunks,
            self.chunk_bytes,
            self.request_bytes,
            self.event_bytes,
            self.output_bytes,
            self.upstream_bytes,
            self.transport_chunk_bytes,
            self.max_blocks,
            self.tool_argument_bytes,
        ];
        let times = [
            self.connect_timeout,
            self.header_timeout,
            self.read_timeout,
            self.write_timeout,
            self.total_timeout,
            self.hook_timeout,
            self.retry_backoff,
        ];
        if sizes.contains(&0)
            || times.iter().any(Duration::is_zero)
            || self.max_output_tokens == 0
            || self.max_in_flight > 4096
            || self.queue_chunks > 1024
            || self.chunk_bytes > 65_536
            || self.max_blocks > 1024
            || self.connect_retries > 2
            || self.output_bytes > 64 * 1024 * 1024
            || self.request_bytes > 16 * 1024 * 1024
            || self.event_bytes > self.output_bytes
            || self.tool_argument_bytes > self.output_bytes
            || self.transport_chunk_bytes > self.upstream_bytes
            || times.iter().any(|d| *d > Duration::from_secs(86_400))
        {
            return Err(RelayError::Invalid("invalid relay limits"));
        }
        Ok(())
    }
}

/// Construct this only after core identity verification. The billing adapter must
/// recheck the actor/key, account funding policy, budget and immutable price version.
#[derive(Clone, Debug)]
pub struct RequestContext {
    pub request_id: String,
    pub actor_user_id: i64,
    pub key_id: i64,
}
impl RequestContext {
    pub(crate) fn validate(&self) -> Result<(), RelayError> {
        if self.actor_user_id <= 0
            || self.key_id <= 0
            || self.request_id.is_empty()
            || self.request_id.len() > 128
            || !self
                .request_id
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b"-_".contains(&b))
        {
            return Err(RelayError::Invalid("invalid request identity"));
        }
        Ok(())
    }
}

/// An immutable route snapshot selected by task 04, not a URL from an HTTP caller.
/// `endpoint` is the complete operation URL. Gemini streaming uses `alt=sse`.
/// HTTPS is required except for explicit numeric loopback test/local upstreams.
#[derive(Clone)]
pub struct Route {
    pub id: String,
    pub protocol: UpstreamProtocol,
    pub model: String,
    pub price_version: u64,
    pub(crate) endpoint: Url,
    pub(crate) credential: HeaderValue,
}
impl fmt::Debug for Route {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Route")
            .field("id", &self.id)
            .field("protocol", &self.protocol)
            .field("price_version", &self.price_version)
            .finish_non_exhaustive()
    }
}
impl Route {
    pub fn new(
        id: String,
        protocol: UpstreamProtocol,
        endpoint: &str,
        model: String,
        api_key: &str,
        price_version: u64,
    ) -> Result<Self, RelayError> {
        let endpoint =
            Url::parse(endpoint).map_err(|_| RelayError::Invalid("invalid route URL"))?;
        let local = endpoint
            .host_str()
            .and_then(|h| h.trim_matches(['[', ']']).parse::<std::net::IpAddr>().ok())
            .is_some_and(|ip| ip.is_loopback());
        if id.is_empty()
            || id.len() > 128
            || model.is_empty()
            || model.len() > 256
            || price_version == 0
            || !endpoint.username().is_empty()
            || endpoint.password().is_some()
            || endpoint.fragment().is_some()
            || endpoint.host_str().is_none()
            || !(endpoint.scheme() == "https" || (endpoint.scheme() == "http" && local))
            || endpoint
                .query_pairs()
                .any(|(k, v)| protocol != UpstreamProtocol::Gemini || k != "alt" || v != "sse")
            || api_key.is_empty()
            || api_key.len() > 4096
        {
            return Err(RelayError::Invalid("invalid route configuration"));
        }
        let auth = if matches!(
            protocol,
            UpstreamProtocol::Chat | UpstreamProtocol::Responses
        ) {
            format!("Bearer {api_key}")
        } else {
            api_key.to_owned()
        };
        let mut credential = HeaderValue::from_str(&auth)
            .map_err(|_| RelayError::Invalid("invalid upstream credential"))?;
        credential.set_sensitive(true);
        Ok(Self {
            id,
            protocol,
            endpoint,
            model,
            credential,
            price_version,
        })
    }
}

pub trait RouteProvider: Send + Sync + 'static {
    /// Read an already active Rust-owned snapshot. Do not call the Go extension.
    fn select<'a>(
        &'a self,
        context: &'a RequestContext,
        request: &'a super::Request,
    ) -> RelayFuture<'a, Route>;
}

/// A durable reservation handle, never a live SQL transaction/connection.
#[derive(Clone, Debug)]
pub struct Reservation {
    pub id: String,
}

pub trait Billing: Send + Sync + 'static {
    /// Idempotent by context.request_id. Commit before returning. A timeout may
    /// mean the commit succeeded: durable recovery must resolve that ambiguity.
    fn reserve<'a>(
        &'a self,
        context: &'a RequestContext,
        route: &'a Route,
        request: &'a super::Request,
    ) -> RelayFuture<'a, Reservation>;
    /// Persist settlement or a reconciliation obligation, idempotently. Missing
    /// usage or a disconnected upstream must NEVER be interpreted as zero cost.
    /// This hook runs even after the client drops its body. Do not wait for Go.
    fn finalize<'a>(
        &'a self,
        reservation: &'a Reservation,
        report: &'a Report,
    ) -> RelayFuture<'a, ()>;
}

#[derive(Clone, Copy, Debug, Default, Eq, PartialEq, Serialize)]
pub struct Usage {
    pub input_tokens: Option<u64>,
    pub output_tokens: Option<u64>,
    pub cached_input_tokens: Option<u64>,
    pub cache_creation_input_tokens: Option<u64>,
    pub reasoning_output_tokens: Option<u64>,
}
impl Usage {
    pub(crate) fn merge(&mut self, next: Self) -> Result<(), RelayError> {
        fn field(old: &mut Option<u64>, next: Option<u64>) -> Result<(), RelayError> {
            if let Some(n) = next {
                if old.is_some_and(|o| n < o) {
                    return Err(RelayError::Protocol("usage counter decreased"));
                }
                *old = Some(n);
            }
            Ok(())
        }
        let mut combined = *self;
        field(&mut combined.input_tokens, next.input_tokens)?;
        field(&mut combined.output_tokens, next.output_tokens)?;
        field(&mut combined.cached_input_tokens, next.cached_input_tokens)?;
        field(
            &mut combined.cache_creation_input_tokens,
            next.cache_creation_input_tokens,
        )?;
        field(
            &mut combined.reasoning_output_tokens,
            next.reasoning_output_tokens,
        )?;
        if let (Some(i), Some(o)) = (combined.input_tokens, combined.output_tokens) {
            i.checked_add(o)
                .ok_or(RelayError::Protocol("usage overflow"))?;
        }
        *self = combined;
        Ok(())
    }
    pub(crate) fn wire(&self, client: ClientProtocol) -> Value {
        if *self == Self::default() {
            return Value::Null;
        }
        let mut out = serde_json::Map::new();
        let (input, output) = match client {
            ClientProtocol::Chat => ("prompt_tokens", "completion_tokens"),
            ClientProtocol::Responses => ("input_tokens", "output_tokens"),
        };
        if let Some(v) = self.input_tokens {
            out.insert(input.into(), json!(v));
        }
        if let Some(v) = self.output_tokens {
            out.insert(output.into(), json!(v));
        }
        if let (Some(i), Some(o)) = (self.input_tokens, self.output_tokens)
            && let Some(total) = i.checked_add(o)
        {
            out.insert("total_tokens".into(), json!(total));
        }
        if let Some(v) = self.cached_input_tokens {
            out.insert(format!("{input}_details"), json!({"cached_tokens":v}));
        }
        if let Some(v) = self.reasoning_output_tokens {
            out.insert(format!("{output}_details"), json!({"reasoning_tokens":v}));
        }
        if let Some(v) = self.cache_creation_input_tokens {
            out.insert("cache_creation_input_tokens".into(), json!(v));
        }
        Value::Object(out)
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum FinishReason {
    Stop,
    Length,
    ToolCalls,
    ContentFilter,
}
impl FinishReason {
    pub(crate) fn chat(self) -> &'static str {
        match self {
            Self::Stop => "stop",
            Self::Length => "length",
            Self::ToolCalls => "tool_calls",
            Self::ContentFilter => "content_filter",
        }
    }
}

#[derive(Clone, Debug)]
pub(crate) enum BlockKind {
    Text,
    Refusal,
    Thinking,
    Tool { id: String, name: String },
}
#[derive(Clone, Debug)]
pub(crate) enum Event {
    Start(String),
    Block {
        index: usize,
        kind: BlockKind,
    },
    Delta {
        index: usize,
        text: String,
    },
    Signature {
        index: usize,
        provider: UpstreamProtocol,
        value: String,
    },
    EndBlock(usize),
    Usage(Usage),
    Finish(FinishReason),
    Done,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Outcome {
    Completed,
    Cancelled,
    TimedOut,
    Failed,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize)]
#[serde(rename_all = "snake_case")]
pub enum Settlement {
    NotReserved,
    Confirmed,
    Pending,
}
#[derive(Clone, Debug, Serialize)]
pub struct Report {
    pub request_id: String,
    pub route_id: Option<String>,
    pub price_version: Option<u64>,
    pub outcome: Outcome,
    pub usage: Usage,
    pub finish_reason: Option<FinishReason>,
    /// True as soon as send starts. A connection error is not proof of no cost.
    pub upstream_attempted: bool,
    pub attempts: u8,
    pub upstream_status: Option<u16>,
    pub upstream_bytes: usize,
    pub emitted_bytes: usize,
}
#[derive(Clone, Debug)]
pub struct Completion {
    pub report: Report,
    pub settlement: Settlement,
    pub error: Option<RelayError>,
}
#[derive(Clone, Copy, Debug)]
pub struct ResponseHead {
    pub status: u16,
    pub content_type: &'static str,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct DrainReport {
    pub cancelled: usize,
    pub remaining: usize,
}
