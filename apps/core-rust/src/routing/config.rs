use std::fmt;

use axum::http::Uri;
use serde::{Deserialize, Serialize};

use super::RoutingError;

pub const MAX_ROUTES: usize = 100_000;
pub const MAX_TARGETS_PER_ROUTE: usize = 64;
pub const MAX_ATTEMPTS: u8 = 8;
pub const MAX_DOCUMENT_BYTES: usize = 16 * 1024 * 1024;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq, Hash, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum Speed {
    #[default]
    Standard,
    Fast,
    Ultrafast,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
pub enum Protocol {
    #[serde(rename = "openai_chat")]
    OpenAiChat,
    #[serde(rename = "openai_responses")]
    OpenAiResponses,
    #[serde(rename = "anthropic_messages")]
    AnthropicMessages,
    #[serde(rename = "gemini")]
    Gemini,
}

/// A base URL, never a URL carrying credentials. This type is INTERNAL config.
#[derive(Clone, Deserialize, Serialize, PartialEq, Eq)]
#[serde(try_from = "String", into = "String")]
pub struct Endpoint(String);

impl Endpoint {
    pub fn as_str(&self) -> &str {
        &self.0
    }
}

impl TryFrom<String> for Endpoint {
    type Error = RoutingError;

    fn try_from(value: String) -> Result<Self, Self::Error> {
        let uri = value
            .parse::<Uri>()
            .map_err(|_| RoutingError::InvalidConfig("endpoint"))?;
        if value.len() > 2048
            || value.bytes().any(|b| b.is_ascii_whitespace() || b.is_ascii_control())
            || value.contains(['@', '?', '#', '\\'])
            || uri.scheme_str() != Some("https")
            || uri.host().is_none_or(str::is_empty)
            || uri.authority().is_none()
        {
            return Err(RoutingError::InvalidConfig("endpoint"));
        }
        Ok(Self(value))
    }
}

impl From<Endpoint> for String {
    fn from(endpoint: Endpoint) -> Self {
        endpoint.0
    }
}

impl fmt::Debug for Endpoint {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("Endpoint([internal])")
    }
}

/// An immutable secret-service reference. A raw token cannot inhabit this type.
#[derive(Clone, Copy, Deserialize, Serialize, PartialEq, Eq, Hash)]
#[serde(deny_unknown_fields)]
pub struct CredentialRef {
    pub id: u64,
    pub revision: u64,
}

impl fmt::Debug for CredentialRef {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.write_str("CredentialRef([internal])")
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct UpstreamConfig {
    pub id: u64,
    pub endpoint: Endpoint,
    pub protocol: Protocol,
    pub credential: CredentialRef,
    pub enabled: bool,
}

impl UpstreamConfig {
    pub(super) fn same_destination(&self, other: &Self) -> bool {
        self.id == other.id
            && self.endpoint == other.endpoint
            && self.protocol == other.protocol
            && self.credential == other.credential
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ModelGroup {
    pub name: String,
    pub enabled: bool,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum PayloadPolicy {
    /// Same wire protocol AND exact model name. Do not alter the request body.
    Passthrough,
    /// Task 05 may use its explicit protocol/model-name adapter. No parameter downgrade.
    Adapt,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct RouteTarget {
    pub id: u64,
    pub upstream_id: u64,
    pub upstream_model: String,
    pub enabled: bool,
    /// Smaller priority wins. Weights only compete within the same priority.
    pub priority: u16,
    pub weight: u32,
    pub speeds: Vec<Speed>,
    pub payload: PayloadPolicy,
    pub timeout_ms: u32,
}

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, Hash)]
#[serde(rename_all = "snake_case")]
pub enum RetryCause {
    Connect,
    Timeout,
    RateLimited,
    ServerError,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct FailoverPolicy {
    /// Includes the first attempt. One means no retry.
    pub max_attempts: u8,
    pub retry_on: Vec<RetryCause>,
    /// Also requires explicit per-request permission. Never permits replay after output.
    pub allow_replay_after_send: bool,
}

impl Default for FailoverPolicy {
    fn default() -> Self {
        Self {
            max_attempts: 1,
            retry_on: Vec::new(),
            allow_replay_after_send: false,
        }
    }
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ModelRoute {
    pub id: u64,
    pub model: String,
    pub group: String,
    pub enabled: bool,
    pub speeds: Vec<Speed>,
    pub targets: Vec<RouteTarget>,
    pub failover: FailoverPolicy,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ModelAlias {
    pub group: String,
    pub alias: String,
    /// Must name a route, not another alias. Alias chains are not accepted.
    pub model: String,
}

/// Trusted internal DTO. Never expose this document through a public model API.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct RoutingConfig {
    pub version: u64,
    pub groups: Vec<ModelGroup>,
    pub upstreams: Vec<UpstreamConfig>,
    pub routes: Vec<ModelRoute>,
    pub aliases: Vec<ModelAlias>,
}

pub(super) fn valid_id(value: u64) -> bool {
    value > 0 && value <= i64::MAX as u64
}

pub(super) fn valid_group(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 64
        && value.bytes().all(|b| b.is_ascii_alphanumeric() || b"._-".contains(&b))
}

pub(super) fn valid_model(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 200
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"._-/:@".contains(&b))
        && !value.ends_with("/fast")
        && !value.ends_with("/ultrafast")
}

pub(super) fn requested_model(
    model: &str,
    explicit: Option<Speed>,
) -> Result<(&str, Speed), RoutingError> {
    let (base, suffix) = if let Some(base) = model.strip_suffix("/ultrafast") {
        (base, Some(Speed::Ultrafast))
    } else if let Some(base) = model.strip_suffix("/fast") {
        (base, Some(Speed::Fast))
    } else {
        (model, None)
    };
    if !valid_model(base) {
        return Err(RoutingError::InvalidRequest);
    }
    if explicit.zip(suffix).is_some_and(|(a, b)| a != b) {
        return Err(RoutingError::ConflictingSpeed);
    }
    Ok((base, explicit.or(suffix).unwrap_or_default()))
}
