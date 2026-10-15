use super::{AbuseError, Result};
use serde::{Deserialize, Serialize};
use std::collections::{BTreeMap, BTreeSet};

#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq, PartialOrd, Ord)]
#[serde(rename_all = "snake_case")]
pub enum ClientProtocol {
    OpenaiChat,
    OpenaiResponses,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum UpstreamProtocol {
    OpenaiChat,
    OpenaiResponses,
    AnthropicMessages,
    Gemini,
}
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Mode {
    FixedText,
    CustomUpstream,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct UpstreamTarget {
    pub base_url: String,
    pub upstream_protocol: UpstreamProtocol,
    pub model: String,
    pub credential_reference: String,
}
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Limits {
    pub requests_per_minute_per_user: u32,
    pub requests_per_minute_global: u32,
    pub max_concurrent_per_user: u32,
    pub max_concurrent_global: u32,
    pub max_input_bytes: u32,
    pub max_output_bytes: u32,
    pub max_output_tokens: u32,
    pub output_bytes_per_second: u32,
    pub total_timeout_seconds: u32,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Policy {
    pub id: String,
    pub name: String,
    pub mode: Mode,
    #[serde(
        default,
        skip_serializing_if = "Option::is_none",
        deserialize_with = "fixed_text"
    )]
    pub fixed_text: Option<String>,
    pub public_models: Vec<String>,
    pub clients: BTreeMap<ClientProtocol, UpstreamTarget>,
    pub limits: Limits,
}
#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PolicyConfig {
    pub version: u32,
    pub policies: Vec<Policy>,
}
fn fixed_text<'de, D>(deserializer: D) -> std::result::Result<Option<String>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    // Absence defaults to None; explicit null is not a normalized config and
    // would otherwise make cross-client policy receipt hashes ambiguous.
    String::deserialize(deserializer).map(Some)
}

pub(super) fn identifier(value: &str, min: usize, max: usize) -> bool {
    (min..=max).contains(&value.len())
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b))
}
fn bounded_text(value: &str, max: usize) -> bool {
    !value.is_empty() && value.len() <= max && !value.chars().any(char::is_control)
}
impl Limits {
    pub fn validate(&self) -> Result<()> {
        let good = (1..=100_000).contains(&self.requests_per_minute_per_user)
            && (1..=100_000).contains(&self.requests_per_minute_global)
            && self.requests_per_minute_per_user <= self.requests_per_minute_global
            && (1..=256).contains(&self.max_concurrent_per_user)
            && (1..=256).contains(&self.max_concurrent_global)
            && self.max_concurrent_per_user <= self.max_concurrent_global
            && (1..=1_048_576).contains(&self.max_input_bytes)
            && (1..=1_048_576).contains(&self.max_output_bytes)
            && (1..=65_536).contains(&self.max_output_tokens)
            && self.output_bytes_per_second <= 1_048_576
            && (1..=240).contains(&self.total_timeout_seconds);
        if good {
            Ok(())
        } else {
            Err(AbuseError::Invalid)
        }
    }
}
impl PolicyConfig {
    pub fn validate(&self, aliases: &BTreeSet<String>) -> Result<()> {
        self.validate_structure(aliases)?;
        // Compatibility validation is explicitly local-only. Host runtime uses
        // AbuseStore::new_with_network_policy and its exact origin bindings.
        for target in self
            .policies
            .iter()
            .flat_map(|policy| policy.clients.values())
        {
            if crate::routing::Endpoint::local_loopback(target.base_url.clone()).is_err() {
                return Err(AbuseError::Invalid);
            }
        }
        Ok(())
    }

    pub(crate) fn validate_structure(&self, aliases: &BTreeSet<String>) -> Result<()> {
        if self.version != 1
            || self.policies.len() > 32
            || serde_json::to_vec(self)
                .map_err(|_| AbuseError::Invalid)?
                .len()
                > 65_536
        {
            return Err(AbuseError::Invalid);
        }
        let mut ids = BTreeSet::new();
        for policy in &self.policies {
            if !identifier(&policy.id, 1, 64)
                || !ids.insert(&policy.id)
                || !bounded_text(&policy.name, 512)
                || policy.name.chars().count() > 128
                || policy.public_models.is_empty()
                || policy.public_models.len() > 200
                || policy.public_models.iter().any(|m| !bounded_text(m, 256))
                || policy.public_models.iter().collect::<BTreeSet<_>>().len()
                    != policy.public_models.len()
            {
                return Err(AbuseError::Invalid);
            }
            policy.limits.validate()?;
            match policy.mode {
                Mode::FixedText => {
                    let text = policy.fixed_text.as_deref().ok_or(AbuseError::Invalid)?;
                    if text.len() > 16_384
                        || text.len() > policy.limits.max_output_bytes as usize
                        || !policy.clients.is_empty()
                    {
                        return Err(AbuseError::Invalid);
                    }
                }
                Mode::CustomUpstream => {
                    if policy.fixed_text.is_some()
                        || policy.clients.len() != 2
                        || !policy.clients.contains_key(&ClientProtocol::OpenaiChat)
                        || !policy
                            .clients
                            .contains_key(&ClientProtocol::OpenaiResponses)
                    {
                        return Err(AbuseError::Invalid);
                    }
                    for target in policy.clients.values() {
                        if !bounded_text(&target.model, 256)
                            || !identifier(&target.credential_reference, 1, 64)
                            || !aliases.contains(&target.credential_reference)
                            || crate::relay::honeypot::validate_base_url(&target.base_url, true)
                                .is_err()
                        {
                            return Err(AbuseError::Invalid);
                        }
                    }
                }
            }
        }
        Ok(())
    }
}
