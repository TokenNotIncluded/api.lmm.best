//! Current Go-compatible assistant routes.

use crate::migration_routes::assistant_extended;

use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    sync::Arc,
    time::{Duration, SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, RawQuery, State},
    http::{HeaderMap, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::{Engine as _, engine::general_purpose::STANDARD as BASE64_STANDARD};
use lmm_contracts::relay::{OpenAiChatRequest, Protocol, openai_chat_request_to_canonical};
use rand::{Rng, distr::Alphanumeric};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Map, Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgRow};

use crate::auth::{
    AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, DashboardUserView, UserAuthPolicyError,
    enforce_user_auth_view, user_auth_message, user_auth_status,
};
use crate::{ClientIpKey, legacy_empty_response};

use super::billing_subscriptions::{enabled_plan_views, payment_compliance_confirmed};
use super::missing_identity_catalog::user_group_selection;
use super::relay_openai::{
    OpenAiRelayBody, OpenAiRelayEndpoint, OpenAiRelayRequest, OpenAiUpstreamClient,
    OpenAiUpstreamTarget,
};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const ADMIN_ROLE: i64 = 10;
const ASSISTANT_HANDOFF_SOURCE: &str = "handoff";
const ASSISTANT_HANDOFF_PENDING: &str = "pending";
const ASSISTANT_HANDOFF_RESOLVED: &str = "resolved";
const ASSISTANT_HANDOFF_INTENT: &str = "human_support";
const ASSISTANT_HANDOFF_MESSAGE_MAX_CHARS: usize = 2_000;
const ASSISTANT_ADMIN_NOTE_MAX_CHARS: usize = 2_000;
const ASSISTANT_KEY_NAME_MAX_CHARS: usize = 50;
const ASSISTANT_KEY_GROUP_MAX_CHARS: usize = 64;
const ASSISTANT_MESSAGE_MAX_CHARS: usize = 4_000;
const ASSISTANT_CONVERSATION_MAX_CHARS: usize = 12_000;
const ASSISTANT_CONVERSATION_MAX_ITEMS: usize = 12;
const ASSISTANT_TOOL_ARGUMENTS_MAX_BYTES: usize = 16 * 1_024;
const ASSISTANT_TOOL_CALLS_PER_TURN: usize = 4;
const ASSISTANT_RESPONSE_CACHE_NAMESPACE: &str = "new-api:assistant-response:v1";
const DEFAULT_MAX_USER_TOKENS: i64 = 1_000;
const ASSISTANT_BODY_LIMIT_BYTES: usize = 64 * 1_024;
const ASSISTANT_CHAT_BODY_LIMIT_BYTES: usize = 64 * 1_024 * 1_024;

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
struct AssistantOpenAiMessage {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    role: String,
    #[serde(
        default,
        deserialize_with = "deserialize_nullable_string",
        skip_serializing_if = "String::is_empty"
    )]
    content: String,
    #[serde(
        default,
        deserialize_with = "deserialize_nullable_string",
        skip_serializing_if = "String::is_empty"
    )]
    name: String,
    #[serde(
        default,
        deserialize_with = "deserialize_nullable_tool_calls",
        skip_serializing_if = "Vec::is_empty"
    )]
    tool_calls: Vec<AssistantOpenAiToolCall>,
    #[serde(
        default,
        deserialize_with = "deserialize_nullable_string",
        skip_serializing_if = "String::is_empty"
    )]
    tool_call_id: String,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
struct AssistantOpenAiToolCall {
    #[serde(
        default,
        deserialize_with = "deserialize_nullable_string",
        skip_serializing_if = "String::is_empty"
    )]
    id: String,
    #[serde(
        rename = "type",
        default,
        deserialize_with = "deserialize_nullable_string",
        skip_serializing_if = "String::is_empty"
    )]
    kind: String,
    #[serde(default)]
    function: AssistantOpenAiToolCallFunction,
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq)]
struct AssistantOpenAiToolCallFunction {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    name: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    arguments: String,
}

#[derive(Debug, Default, Deserialize)]
struct AssistantChatInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    message: String,
    #[serde(default, deserialize_with = "deserialize_nullable_messages")]
    messages: Vec<AssistantOpenAiMessage>,
}

#[derive(Clone, Debug, Serialize)]
struct AssistantOpenAiRequest {
    model: String,
    messages: Vec<AssistantOpenAiMessage>,
    stream: bool,
    temperature: f64,
    max_tokens: i64,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    tools: Vec<Value>,
    #[serde(skip_serializing_if = "Option::is_none")]
    tool_choice: Option<&'static str>,
}

#[derive(Debug, Deserialize)]
struct AssistantOpenAiResponse {
    #[serde(default)]
    choices: Vec<AssistantOpenAiResponseChoice>,
}

#[derive(Debug, Deserialize)]
struct AssistantOpenAiResponseChoice {
    message: AssistantOpenAiResponseMessage,
}

#[derive(Debug, Deserialize)]
struct AssistantOpenAiResponseMessage {
    #[serde(default)]
    content: Value,
    #[serde(default)]
    tool_calls: Vec<AssistantOpenAiToolCall>,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
struct AssistantLead {
    id: i64,
    user_id: i64,
    source: String,
    intent: String,
    message: String,
    status: String,
    admin_user_id: i64,
    admin_note: String,
    created_at: i64,
    resolved_at: i64,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
struct AssistantLeadView {
    #[serde(flatten)]
    lead: AssistantLead,
    username: String,
    email: String,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
struct AssistantIntentSummary {
    intent: String,
    count: i64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct AssistantAdminAudit {
    actor_id: i64,
    actor_username: String,
    actor_role: i64,
    auth_method: &'static str,
    client_ip: String,
    lead_id: String,
    status: StatusCode,
    success: bool,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum ResolveHandoffError {
    NotFound,
    AlreadyResolved,
    Unavailable(String),
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
struct AssistantKeyGroupOption {
    id: String,
    description: String,
    automatic: bool,
    #[serde(skip_serializing_if = "Vec::is_empty")]
    routing_groups: Vec<String>,
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
struct AssistantCreatedKey {
    id: i64,
    name: String,
    key: String,
    group: String,
    expired_time: i64,
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct AssistantCachedResponse {
    status: StatusCode,
    body: Vec<u8>,
}

#[derive(Clone, Debug, PartialEq, Eq)]
enum CreateAssistantKeyError {
    TokenLimit(i64),
    Unavailable(String),
}

#[derive(Clone, Copy, Debug)]
pub struct AssistantRateLimitConfig {
    pub enabled: bool,
    pub max_requests: u64,
    pub window: Duration,
    pub dependency_timeout: Duration,
}

#[async_trait]
trait AssistantUserRateLimiter: Send + Sync {
    async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()>;
}

#[derive(Clone)]
struct ValkeyAssistantUserRateLimiter {
    valkey: redis::Client,
    config: AssistantRateLimitConfig,
}

#[async_trait]
impl AssistantUserRateLimiter for ValkeyAssistantUserRateLimiter {
    async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()> {
        if !self.config.enabled {
            return Ok(CriticalRateLimitOutcome::Allowed);
        }
        let mut connection = tokio::time::timeout(
            self.config.dependency_timeout,
            self.valkey.get_multiplexed_async_connection(),
        )
        .await
        .map_err(|_| ())?
        .map_err(|_| ())?;
        let key = assistant_user_rate_limit_key(scope, user_id);
        let script = redis::Script::new(
            r#"
local count = redis.call('INCR', KEYS[1])
if count == 1 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
end
local ttl = redis.call('TTL', KEYS[1])
if ttl < 0 then
  redis.call('EXPIRE', KEYS[1], ARGV[2])
  ttl = redis.call('TTL', KEYS[1])
end
if count > tonumber(ARGV[1]) then
  return {0, count, ttl}
end
return {1, count, ttl}
"#,
        );
        let (allowed, _, ttl): (i64, i64, i64) = tokio::time::timeout(
            self.config.dependency_timeout,
            script
                .key(key)
                .arg(self.config.max_requests)
                .arg(self.config.window.as_secs())
                .invoke_async(&mut connection),
        )
        .await
        .map_err(|_| ())?
        .map_err(|_| ())?;
        if allowed == 1 {
            Ok(CriticalRateLimitOutcome::Allowed)
        } else {
            Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: ttl.max(0) as u64,
            })
        }
    }
}

fn assistant_user_rate_limit_key(scope: &str, user_id: i64) -> String {
    format!("rateLimit:v2:user:UC:{scope}:{user_id}")
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct AssistantBillingAccount {
    id: i64,
    group: String,
}

#[derive(Clone, Debug)]
struct AssistantAgentTurn {
    billing: AssistantBillingAccount,
    request_id: String,
    model: String,
    body: Vec<u8>,
}

#[derive(Clone, Debug)]
struct AssistantAgentTurnResponse {
    status: StatusCode,
    body: Vec<u8>,
}

#[async_trait]
trait AssistantAgentBackend: Send + Sync {
    async fn relay_turn(
        &self,
        turn: AssistantAgentTurn,
    ) -> Result<AssistantAgentTurnResponse, String>;
}

struct DisabledAssistantAgentBackend;

#[async_trait]
impl AssistantAgentBackend for DisabledAssistantAgentBackend {
    async fn relay_turn(
        &self,
        _: AssistantAgentTurn,
    ) -> Result<AssistantAgentTurnResponse, String> {
        Err("assistant relay backend is unavailable".to_owned())
    }
}

#[derive(Clone)]
struct PgAssistantAgentBackend {
    pg: PgPool,
    upstream: OpenAiUpstreamClient,
    quota_per_request: i64,
}

struct AssistantRelayReservation {
    channel_id: i64,
    target: OpenAiUpstreamTarget,
}

impl PgAssistantAgentBackend {
    async fn reserve(
        &self,
        turn: &AssistantAgentTurn,
    ) -> Result<AssistantRelayReservation, AssistantAgentTurnResponse> {
        let mut transaction = self.pg.begin().await.map_err(|_| {
            assistant_relay_error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "",
                "database error",
                &turn.request_id,
            )
        })?;
        let row = sqlx::query(
            "SELECT COALESCE(u.quota, 0)::BIGINT AS user_quota, c.id::BIGINT AS channel_id, COALESCE(c.base_url, '') AS base_url, COALESCE(c.key, '') AS channel_key FROM users u JOIN abilities a ON a.\"group\" = $2 AND a.model = $3 AND COALESCE(a.enabled, TRUE) JOIN channels c ON c.id = a.channel_id WHERE u.id = $1 AND u.role = 100 AND u.status = 1 AND u.deleted_at IS NULL AND COALESCE(c.status, 1) = 1 ORDER BY COALESCE(a.priority, 0) DESC, COALESCE(a.weight, 0) DESC, c.id LIMIT 1 FOR UPDATE OF u, c",
        )
        .bind(turn.billing.id)
        .bind(&turn.billing.group)
        .bind(&turn.model)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|_| assistant_relay_error(StatusCode::INTERNAL_SERVER_ERROR, "", "database error", &turn.request_id))?
        .ok_or_else(|| {
            assistant_relay_error(
                StatusCode::SERVICE_UNAVAILABLE,
                "no_available_channel",
                "no available channel",
                &turn.request_id,
            )
        })?;
        let quota = row.try_get::<i64, _>("user_quota").unwrap_or_default();
        if quota < self.quota_per_request {
            return Err(assistant_relay_error(
                StatusCode::FORBIDDEN,
                "insufficient_quota",
                "insufficient quota",
                &turn.request_id,
            ));
        }
        let channel_id = row.try_get::<i64, _>("channel_id").map_err(|_| {
            assistant_relay_error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "",
                "database error",
                &turn.request_id,
            )
        })?;
        let base_url = row.try_get::<String, _>("base_url").unwrap_or_default();
        let channel_key = row.try_get::<String, _>("channel_key").unwrap_or_default();
        let api_key = channel_key
            .lines()
            .map(str::trim)
            .find(|key| !key.is_empty())
            .unwrap_or_default()
            .to_owned();
        if base_url.trim().is_empty() || api_key.is_empty() {
            return Err(assistant_relay_error(
                StatusCode::SERVICE_UNAVAILABLE,
                "no_available_channel",
                "no available channel",
                &turn.request_id,
            ));
        }
        sqlx::query("UPDATE users SET quota = COALESCE(quota, 0) - $2, used_quota = COALESCE(used_quota, 0) + $2, request_count = COALESCE(request_count, 0) + 1 WHERE id = $1")
            .bind(turn.billing.id)
            .bind(self.quota_per_request)
            .execute(&mut *transaction)
            .await
            .map_err(|_| assistant_relay_error(StatusCode::INTERNAL_SERVER_ERROR, "", "database error", &turn.request_id))?;
        sqlx::query("UPDATE channels SET used_quota = COALESCE(used_quota, 0) + $2 WHERE id = $1")
            .bind(channel_id)
            .bind(self.quota_per_request)
            .execute(&mut *transaction)
            .await
            .map_err(|_| {
                assistant_relay_error(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "",
                    "database error",
                    &turn.request_id,
                )
            })?;
        transaction.commit().await.map_err(|_| {
            assistant_relay_error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "",
                "database error",
                &turn.request_id,
            )
        })?;
        Ok(AssistantRelayReservation {
            channel_id,
            target: OpenAiUpstreamTarget { base_url, api_key },
        })
    }

    async fn refund(&self, billing_user_id: i64, channel_id: i64) {
        let Ok(mut transaction) = self.pg.begin().await else {
            return;
        };
        if sqlx::query("UPDATE users SET quota = COALESCE(quota, 0) + $2, used_quota = GREATEST(COALESCE(used_quota, 0) - $2, 0), request_count = GREATEST(COALESCE(request_count, 0) - 1, 0) WHERE id = $1")
            .bind(billing_user_id)
            .bind(self.quota_per_request)
            .execute(&mut *transaction)
            .await
            .is_err()
        {
            return;
        }
        if sqlx::query("UPDATE channels SET used_quota = GREATEST(COALESCE(used_quota, 0) - $2, 0) WHERE id = $1")
            .bind(channel_id)
            .bind(self.quota_per_request)
            .execute(&mut *transaction)
            .await
            .is_err()
        {
            return;
        }
        let _ = transaction.commit().await;
    }

    async fn record_success(
        &self,
        turn: &AssistantAgentTurn,
        channel_id: i64,
    ) -> Result<(), sqlx::Error> {
        sqlx::query("INSERT INTO logs (user_id, created_at, type, content, model_name, quota, channel_id, token_id, \"group\", request_id, is_stream) VALUES ($1, $2, 2, '', $3, $4, $5, 0, $6, $7, FALSE)")
            .bind(turn.billing.id)
            .bind(unix_seconds())
            .bind(&turn.model)
            .bind(self.quota_per_request)
            .bind(channel_id)
            .bind(&turn.billing.group)
            .bind(&turn.request_id)
            .execute(&self.pg)
            .await
            .map(|_| ())
    }
}

#[async_trait]
impl AssistantAgentBackend for PgAssistantAgentBackend {
    async fn relay_turn(
        &self,
        turn: AssistantAgentTurn,
    ) -> Result<AssistantAgentTurnResponse, String> {
        let wire = serde_json::from_slice::<OpenAiChatRequest>(&turn.body)
            .map_err(|error| error.to_string())?;
        let canonical = openai_chat_request_to_canonical(wire)
            .map_err(|error| error.to_string())?
            .value;
        let reservation = match self.reserve(&turn).await {
            Ok(reservation) => reservation,
            Err(response) => return Ok(response),
        };
        let mut headers = HeaderMap::new();
        headers.insert(
            header::CONTENT_TYPE,
            axum::http::HeaderValue::from_static("application/json"),
        );
        let request = OpenAiRelayRequest {
            endpoint: OpenAiRelayEndpoint::ChatCompletions,
            protocol: Protocol::OpenAi,
            request_id: turn.request_id.clone(),
            headers,
            request: canonical,
            raw_body: turn.body.clone(),
        };
        let result = match self.upstream.forward(&reservation.target, &request).await {
            Ok(result) => result,
            Err(failure) => {
                self.refund(turn.billing.id, reservation.channel_id).await;
                return Ok(assistant_relay_error(
                    failure.status,
                    &failure.code,
                    &failure.message,
                    &turn.request_id,
                ));
            }
        };
        let body = match result.body {
            OpenAiRelayBody::Upstream { body, .. } => to_bytes(body, 64 * 1_024 * 1_024)
                .await
                .map(|body| body.to_vec()),
            OpenAiRelayBody::Complete(_) | OpenAiRelayBody::Stream(_) => Err(axum::Error::new(
                std::io::Error::other("unexpected converted assistant response"),
            )),
        };
        let body = match body {
            Ok(body) => body,
            Err(_) => {
                self.refund(turn.billing.id, reservation.channel_id).await;
                return Ok(assistant_relay_error(
                    StatusCode::BAD_GATEWAY,
                    "upstream_protocol_error",
                    "upstream response could not be read",
                    &turn.request_id,
                ));
            }
        };
        if self
            .record_success(&turn, reservation.channel_id)
            .await
            .is_err()
        {
            self.refund(turn.billing.id, reservation.channel_id).await;
            return Ok(assistant_relay_error(
                StatusCode::INTERNAL_SERVER_ERROR,
                "",
                "database error",
                &turn.request_id,
            ));
        }
        Ok(AssistantAgentTurnResponse {
            status: result.status,
            body,
        })
    }
}

fn assistant_relay_error(
    status: StatusCode,
    code: &str,
    message: &str,
    request_id: &str,
) -> AssistantAgentTurnResponse {
    AssistantAgentTurnResponse {
        status,
        body: serde_json::to_vec(&json!({
            "error": {
                "message": format!("{message} (request id: {request_id})"),
                "type": "new_api_error",
                "code": code,
            }
        }))
        .unwrap_or_default(),
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
struct AssistantSettingsView {
    enabled: bool,
    model: String,
    group: String,
    agent_loop_enabled: bool,
    max_steps: i64,
    timeout_seconds: i64,
    cache_enabled: bool,
    cache_ttl_minutes: i64,
    server_address: String,
    persona: String,
    system_prompt: String,
    search_url: String,
    search_api_key: String,
    skills: String,
}

impl Default for AssistantSettingsView {
    fn default() -> Self {
        Self {
            enabled: true,
            model: "deepseek-v4-flash".to_owned(),
            group: "default".to_owned(),
            agent_loop_enabled: true,
            max_steps: 6,
            timeout_seconds: 45,
            cache_enabled: true,
            cache_ttl_minutes: 1_440,
            server_address: String::new(),
            persona: String::new(),
            system_prompt: String::new(),
            search_url: String::new(),
            search_api_key: String::new(),
            skills: String::new(),
        }
    }
}

impl AssistantSettingsView {
    fn from_options(options: &HashMap<String, String>) -> Self {
        let defaults = Self::default();
        Self {
            enabled: options
                .get("AssistantEnabled")
                .map_or(defaults.enabled, |value| value == "true"),
            model: options
                .get("AssistantModel")
                .map(|value| value.trim())
                .filter(|value| !value.is_empty() && value.len() <= 128)
                .map_or(defaults.model, str::to_owned),
            group: options
                .get("AssistantGroup")
                .map(|value| value.trim())
                .filter(|value| !value.is_empty() && value.chars().count() <= 64)
                .map_or(defaults.group, str::to_owned),
            agent_loop_enabled: options
                .get("AssistantAgentLoopEnabled")
                .map_or(defaults.agent_loop_enabled, |value| value == "true"),
            max_steps: bounded_option(options, "AssistantMaxSteps", 1, 12)
                .unwrap_or(defaults.max_steps),
            timeout_seconds: bounded_option(options, "AssistantTimeoutSeconds", 5, 120)
                .unwrap_or(defaults.timeout_seconds),
            cache_enabled: options
                .get("AssistantCacheEnabled")
                .map_or(defaults.cache_enabled, |value| value == "true"),
            cache_ttl_minutes: bounded_option(options, "AssistantCacheTTLMinutes", 0, 10_080)
                .unwrap_or(defaults.cache_ttl_minutes),
            server_address: trimmed_option(options, "ServerAddress"),
            persona: trimmed_option(options, "AssistantPersona"),
            system_prompt: trimmed_option(options, "AssistantSystemPrompt"),
            search_url: trimmed_option(options, "AssistantSearchURL"),
            search_api_key: trimmed_option(options, "AssistantSearchAPIKey"),
            skills: trimmed_option(options, "AssistantSkills"),
        }
    }
}

fn trimmed_option(options: &HashMap<String, String>, key: &str) -> String {
    options
        .get(key)
        .map(|value| value.trim().to_owned())
        .unwrap_or_default()
}

fn bounded_option(
    options: &HashMap<String, String>,
    key: &str,
    minimum: i64,
    maximum: i64,
) -> Option<i64> {
    options
        .get(key)?
        .trim()
        .parse::<i64>()
        .ok()
        .filter(|value| (minimum..=maximum).contains(value))
}

fn build_assistant_system_prompt(settings: &AssistantSettingsView) -> String {
    let configured_root = settings.server_address.trim_end_matches('/');
    let (root, base) = if configured_root.is_empty() {
        (
            "the service root shown in the current console".to_owned(),
            "the /v1 endpoint shown in the current console".to_owned(),
        )
    } else {
        (configured_root.to_owned(), format!("{configured_root}/v1"))
    };
    let mut prompt = format!(
        "You are the built-in customer assistant for LMM, an AI API service.\n\
Answer in the user's language and be concise, accurate, and practical.\n\
You may explain onboarding review, plans, pricing, discounts, API keys, Base URL and model IDs, cost calculations, open-source bounties and tips, and setup for Claude Code, CC Switch, ChatGPT-compatible clients, Windows, Linux, and macOS.\n\n\
Current service connection facts:\n\
- Anthropic-compatible service root: {root}\n\
- OpenAI-compatible Base URL: {base}\n\
- Internal assistant model ID (never present this as the user's client model): {}\n\
- Existing API keys are private and unavailable to you. Direct the user to the connection details tool to create and copy a new key with explicit confirmation.",
        settings.model
    );
    for (heading, value) in [
        (
            "Administrator-configured personality:",
            settings.persona.trim(),
        ),
        (
            "Administrator-configured skills and playbooks:",
            settings.skills.trim(),
        ),
        (
            "Administrator-configured operating instructions:",
            settings.system_prompt.trim(),
        ),
    ] {
        if !value.is_empty() {
            prompt.push_str("\n\n");
            prompt.push_str(heading);
            prompt.push('\n');
            prompt.push_str(value);
        }
    }
    prompt.push_str(
        "\n\nNon-overridable safety and accuracy rules:\n\
- Never ask for or repeat passwords, API keys, session cookies, or other secrets.\n\
- Never claim that you created a key, changed an account, contacted an administrator, purchased a plan, or completed any other action unless a confirmed tool result says so.\n\
- Use live tools for account state, model availability, pricing, discounts, invitation rewards, usage statistics, and search results. If a tool is unavailable, say so instead of inventing a value.\n\
- Before estimating token cost, call get_model_pricing for the exact model and group, then pass its already-adjusted USD rates to calculate_cost with group_ratio=1.\n\
- L0 users can only browse public challenges and use this assistant. Do not expose payment, checkout, API-key creation, usage, or other console actions until an administrator grants L1.\n\
- For an L0 user asking for L1, first call get_account_access. Ask focused follow-up questions about their real use case, intended client, and what they plan to build. Do not prepare a recommendation from a greeting or a vague demand.\n\
- Once the L0 user has provided enough concrete information, call prepare_l1_recommendation. The user must explicitly confirm that draft in the UI before it is sent. Only an administrator can approve or reject it; never claim that the assistant granted L1.\n\
- When get_account_access reports a pending or reviewed L1 request, accurately relay its status and the administrator's note. A rejection is feedback for another conversation, not permission to activate the account.\n\
- Use the service root without /v1 for Anthropic-compatible clients such as Claude Code, and use the /v1 Base URL for OpenAI-compatible clients.\n\
- CC Switch supports one-click provider import through the ccswitch://v1/import deep-link protocol. Never say that CC Switch has no import link, and do not make manual field entry the default. For Claude, the generated link uses resource=provider, app=claude, the service root without /v1, the exact client model ID, and the newly created API key.\n\
- API keys must never enter the assistant context or chat transcript. After the user confirms key creation, use the shielded private card's Import to CC Switch action (or the CC Switch action for that key on /keys) to construct and open the real link in the browser; show manual values only as a fallback.\n\
- The official ChatGPT app does not accept a custom API Base URL or this service's API key. Recommend CC Switch or another compatible API client when the user wants to use this service.\n\
- Write actions require explicit confirmation in the UI. Explain the next step clearly and never hide a charge or a permission change.",
    );
    prompt
}

#[derive(Serialize)]
struct AssistantCacheFingerprint<'a> {
    version: &'static str,
    model: &'a str,
    group: &'a str,
    system_prompt: String,
    agent_loop_enabled: bool,
    max_steps: i64,
    timeout_seconds: i64,
    ttl_minutes: i64,
    conversation: &'a [AssistantOpenAiMessage],
}

fn assistant_cache_key(
    settings: &AssistantSettingsView,
    conversation: &[AssistantOpenAiMessage],
) -> String {
    if !settings.cache_enabled
        || settings.cache_ttl_minutes <= 0
        || conversation.len() != 1
        || conversation[0].role != "user"
    {
        return String::new();
    }
    let fingerprint = AssistantCacheFingerprint {
        version: "assistant-cache-v1",
        model: &settings.model,
        group: &settings.group,
        system_prompt: build_assistant_system_prompt(settings),
        agent_loop_enabled: settings.agent_loop_enabled,
        max_steps: settings.max_steps,
        timeout_seconds: settings.timeout_seconds,
        ttl_minutes: settings.cache_ttl_minutes,
        conversation,
    };
    serde_json::to_vec(&fingerprint)
        .map(|raw| hex::encode(Sha256::digest(raw)))
        .unwrap_or_default()
}

fn assistant_tool_definitions() -> Vec<Value> {
    let empty = || json!({"type":"object","properties":{},"additionalProperties":false});
    let object = |properties: Value, required: &[&str]| {
        let mut schema = json!({
            "type": "object",
            "properties": properties,
            "additionalProperties": false,
        });
        if !required.is_empty() {
            schema["required"] = json!(required);
        }
        schema
    };
    let tool = |name: &str, description: &str, parameters: Value| {
        json!({
            "type": "function",
            "function": {
                "name": name,
                "description": description,
                "parameters": parameters,
            },
        })
    };
    vec![
        tool(
            "get_service_facts",
            "Return the current public connection facts for this LMM console. Use this before explaining Base URL, compatible client endpoints, or where to manage private API keys.",
            empty(),
        ),
        tool(
            "calculate_cost",
            "Calculate an estimated USD cost from token counts and supplied per-million-token prices. Never invent prices; ask for missing prices when needed.",
            object(
                json!({
                    "input_tokens":{"type":"number","minimum":0},
                    "output_tokens":{"type":"number","minimum":0},
                    "input_usd_per_million":{"type":"number","minimum":0},
                    "output_usd_per_million":{"type":"number","minimum":0},
                    "group_ratio":{"type":"number","minimum":0}
                }),
                &[
                    "input_tokens",
                    "output_tokens",
                    "input_usd_per_million",
                    "output_usd_per_million",
                ],
            ),
        ),
        tool(
            "get_account_access",
            "Read the signed-in user's non-secret access state, such as trust level and whether developer features are unlocked.",
            empty(),
        ),
        tool(
            "get_available_models",
            "Return the model IDs and usable routing groups available to the signed-in user. Never invent a model ID.",
            empty(),
        ),
        tool(
            "get_model_pricing",
            "Return the signed-in user's live per-group prices for one exact model ID. Call this before calculating cost; if the user has not chosen a model, ask them or call get_available_models first.",
            object(
                json!({
                    "model_id":{"type":"string","minLength":1,"maxLength":200},
                    "group":{"type":"string","maxLength":64}
                }),
                &["model_id"],
            ),
        ),
        tool(
            "get_plan_offers",
            "Return current enabled subscription plans and configured top-up discounts for comparison. Use exact live values and do not invent promotions.",
            empty(),
        ),
        tool(
            "get_invitation_rewards",
            "Explain the signed-in user's invitation code, reward status, and current inviter/invitee reward configuration without exposing secrets.",
            empty(),
        ),
        tool(
            "get_bounty_guide",
            "Return the current safe workflow for publishing, funding, reviewing, tipping, and settling an open-source bounty.",
            empty(),
        ),
        tool(
            "get_usage_summary",
            "Summarize the signed-in user's historical consume calls by model and group. Use this for usage statistics instead of exposing raw logs.",
            object(
                json!({"days":{"type":"integer","minimum":1,"maximum":90}}),
                &[],
            ),
        ),
        tool(
            "search_web",
            "Search the administrator-configured web search API for current software installation or platform information. If no search API is configured, report that limitation.",
            object(
                json!({"query":{"type":"string","minLength":2,"maxLength":500}}),
                &["query"],
            ),
        ),
        tool(
            "get_setup_guide",
            "Return verified platform-specific install commands and gateway configuration for Claude Code, CC Switch, Claude Desktop, Codex, and compatible clients. Use this instead of guessing client capabilities or endpoint formats.",
            object(
                json!({
                    "platform":{"type":"string","enum":["windows","linux","macos"]},
                    "topic":{"type":"string","enum":["claude-code","cc-switch","claude-desktop","chatgpt-client","codex","cursor","open-webui","other-openai-compatible"]},
                    "model_id":{"type":"string","minLength":1,"maxLength":200}
                }),
                &["platform", "topic"],
            ),
        ),
        tool(
            "prepare_l1_recommendation",
            "Prepare an administrator recommendation for a concrete L0 user after a substantive onboarding conversation. This does not submit or approve anything; the user must explicitly confirm the draft in the UI.",
            object(
                json!({
                    "user_statement":{"type":"string","minLength":5,"maxLength":2000},
                    "recommendation":{"type":"string","minLength":20,"maxLength":2000}
                }),
                &["user_statement", "recommendation"],
            ),
        ),
        tool(
            "request_create_key",
            "Prepare creation of an API key. First call without a group to load the signed-in user's live group choices, then ask the user to choose one exact group. Only after that choice may you request explicit confirmation; never claim a key was created from this tool.",
            object(
                json!({
                    "name":{"type":"string","maxLength":50},
                    "group":{"type":"string","maxLength":64}
                }),
                &[],
            ),
        ),
        tool(
            "request_human_support",
            "Prepare a handoff to an administrator. This is a write action and requires an explicit confirmation in the UI.",
            object(
                json!({"message":{"type":"string","maxLength":4000}}),
                &["message"],
            ),
        ),
    ]
}

#[async_trait]
trait AssistantReadStore: Send + Sync {
    async fn settings(&self) -> Result<AssistantSettingsView, String>;
    async fn assistant_model_ids(&self, group: &str) -> Result<Vec<String>, String>;
    async fn latest_handoff(&self, user_id: i64) -> Result<Option<AssistantLead>, String>;
    async fn list_handoffs(
        &self,
        status: &str,
        limit: i64,
    ) -> Result<Vec<AssistantLeadView>, String>;
    async fn intent_summary(&self, since: i64) -> Result<Vec<AssistantIntentSummary>, String>;
    async fn key_group_options(
        &self,
        user_group: &str,
    ) -> Result<Vec<AssistantKeyGroupOption>, String>;
    async fn billing_account(&self) -> Result<AssistantBillingAccount, String>;
    async fn record_intent(&self, user_id: i64, intent: &str);
    async fn cached_response(&self, key: &str) -> Option<AssistantCachedResponse>;
    async fn store_cached_response(
        &self,
        key: &str,
        response: &AssistantCachedResponse,
        ttl: Duration,
    );
    async fn create_key(
        &self,
        user_id: i64,
        username: &str,
        name: &str,
        group: &str,
    ) -> Result<AssistantCreatedKey, CreateAssistantKeyError>;
    async fn submit_handoff(
        &self,
        user_id: i64,
        username: &str,
        message: &str,
    ) -> Result<AssistantLead, String>;
    async fn resolve_handoff(
        &self,
        admin_user_id: i64,
        admin_username: &str,
        lead_id: i64,
        note: &str,
    ) -> Result<AssistantLead, ResolveHandoffError>;
    async fn record_admin_audit(&self, audit: AssistantAdminAudit);
}

#[derive(Clone)]
struct PgAssistantReadStore {
    pg: PgPool,
    valkey: redis::Client,
}

impl PgAssistantReadStore {
    async fn record_system_log(&self, user_id: i64, username: &str, content: String) {
        let _ = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, request_id) \
             VALUES ($1, $2, 4, $3, $4, $5)",
        )
        .bind(user_id)
        .bind(unix_seconds())
        .bind(content)
        .bind(username)
        .bind(uuid::Uuid::new_v4().to_string())
        .execute(&self.pg)
        .await;
    }

    async fn evict_user_cache(&self, user_id: i64) {
        let Ok(mut connection) = self.valkey.get_multiplexed_async_connection().await else {
            return;
        };
        let _ = redis::cmd("DEL")
            .arg(format!("user:{user_id}"))
            .query_async::<()>(&mut connection)
            .await;
    }
}

#[async_trait]
impl AssistantReadStore for PgAssistantReadStore {
    async fn settings(&self) -> Result<AssistantSettingsView, String> {
        let rows = sqlx::query(
            "SELECT key, value FROM options WHERE key IN \
             ('AssistantEnabled', 'AssistantModel', 'AssistantGroup', 'AssistantAgentLoopEnabled', \
              'AssistantMaxSteps', 'AssistantTimeoutSeconds', 'AssistantCacheEnabled', \
              'AssistantCacheTTLMinutes', 'ServerAddress', 'AssistantPersona', \
              'AssistantSystemPrompt', 'AssistantSearchURL', 'AssistantSearchAPIKey', \
              'AssistantSkills')",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| error.to_string())?;
        let options = rows
            .into_iter()
            .map(|row| {
                Ok((
                    row.try_get::<String, _>("key")?,
                    row.try_get::<String, _>("value")?,
                ))
            })
            .collect::<Result<HashMap<_, _>, sqlx::Error>>()
            .map_err(|error| error.to_string())?;
        Ok(AssistantSettingsView::from_options(&options))
    }

    async fn assistant_model_ids(&self, group: &str) -> Result<Vec<String>, String> {
        sqlx::query_scalar::<_, String>(
            r#"SELECT DISTINCT model FROM abilities
               WHERE "group" = $1 AND COALESCE(enabled, TRUE) = TRUE
               ORDER BY model"#,
        )
        .bind(group)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| error.to_string())
    }

    async fn latest_handoff(&self, user_id: i64) -> Result<Option<AssistantLead>, String> {
        sqlx::query(
            "SELECT id::BIGINT AS id, user_id::BIGINT AS user_id, COALESCE(source, '') AS source, \
             COALESCE(intent, '') AS intent, COALESCE(message, '') AS message, \
             COALESCE(status, '') AS status, COALESCE(admin_user_id, 0)::BIGINT AS admin_user_id, \
             COALESCE(admin_note, '') AS admin_note, COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(resolved_at, 0)::BIGINT AS resolved_at \
             FROM assistant_leads WHERE user_id = $1 AND source = $2 ORDER BY id DESC LIMIT 1",
        )
        .bind(user_id)
        .bind(ASSISTANT_HANDOFF_SOURCE)
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| error.to_string())?
        .map(|row| assistant_lead_from_row(&row).map_err(|error| error.to_string()))
        .transpose()
    }

    async fn list_handoffs(
        &self,
        status: &str,
        limit: i64,
    ) -> Result<Vec<AssistantLeadView>, String> {
        sqlx::query(
            "SELECT lead.id::BIGINT AS id, lead.user_id::BIGINT AS user_id, \
             COALESCE(lead.source, '') AS source, COALESCE(lead.intent, '') AS intent, \
             COALESCE(lead.message, '') AS message, COALESCE(lead.status, '') AS status, \
             COALESCE(lead.admin_user_id, 0)::BIGINT AS admin_user_id, \
             COALESCE(lead.admin_note, '') AS admin_note, \
             COALESCE(lead.created_at, 0)::BIGINT AS created_at, \
             COALESCE(lead.resolved_at, 0)::BIGINT AS resolved_at, \
             COALESCE(users.username, '') AS username, COALESCE(users.email, '') AS email \
             FROM assistant_leads AS lead JOIN users ON users.id = lead.user_id \
             WHERE lead.source = $1 AND lead.status = $2 ORDER BY lead.id DESC LIMIT $3",
        )
        .bind(ASSISTANT_HANDOFF_SOURCE)
        .bind(status)
        .bind(limit)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| error.to_string())?
        .into_iter()
        .map(|row| {
            let lead = assistant_lead_from_row(&row).map_err(|error| error.to_string())?;
            Ok(AssistantLeadView {
                lead,
                username: row
                    .try_get("username")
                    .map_err(|error: sqlx::Error| error.to_string())?,
                email: row
                    .try_get("email")
                    .map_err(|error: sqlx::Error| error.to_string())?,
            })
        })
        .collect()
    }

    async fn intent_summary(&self, since: i64) -> Result<Vec<AssistantIntentSummary>, String> {
        sqlx::query(
            "SELECT COALESCE(intent, '') AS intent, COUNT(*)::BIGINT AS count \
             FROM assistant_leads WHERE created_at >= $1 \
             GROUP BY intent ORDER BY count DESC, intent ASC",
        )
        .bind(since)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| error.to_string())?
        .into_iter()
        .map(|row| {
            Ok(AssistantIntentSummary {
                intent: row
                    .try_get("intent")
                    .map_err(|error: sqlx::Error| error.to_string())?,
                count: row
                    .try_get("count")
                    .map_err(|error: sqlx::Error| error.to_string())?,
            })
        })
        .collect()
    }

    async fn key_group_options(
        &self,
        user_group: &str,
    ) -> Result<Vec<AssistantKeyGroupOption>, String> {
        let selection = user_group_selection(&self.pg, user_group).await?;
        let mut options = Vec::with_capacity(selection.selectable.len() + 1);
        if !selection.automatic.is_empty() {
            options.push(AssistantKeyGroupOption {
                id: "auto".to_owned(),
                description: "Automatic routing across the listed groups".to_owned(),
                automatic: true,
                routing_groups: selection.automatic,
            });
        }
        options.extend(selection.selectable.into_iter().map(|(id, description)| {
            AssistantKeyGroupOption {
                id,
                description,
                automatic: false,
                routing_groups: Vec::new(),
            }
        }));
        Ok(options)
    }

    async fn billing_account(&self) -> Result<AssistantBillingAccount, String> {
        let row = sqlx::query(
            "SELECT id::BIGINT AS id, COALESCE(\"group\", '') AS \"group\" FROM users \
             WHERE role = 100 AND status = 1 AND deleted_at IS NULL ORDER BY id ASC LIMIT 1",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| error.to_string())?
        .ok_or_else(|| "AI assistant billing account is unavailable".to_owned())?;
        Ok(AssistantBillingAccount {
            id: row
                .try_get("id")
                .map_err(|error: sqlx::Error| error.to_string())?,
            group: row
                .try_get("group")
                .map_err(|error: sqlx::Error| error.to_string())?,
        })
    }

    async fn record_intent(&self, user_id: i64, intent: &str) {
        let _ = sqlx::query(
            "INSERT INTO assistant_leads (user_id, source, intent, status, created_at) \
             VALUES ($1, 'chat', $2, 'observed', $3)",
        )
        .bind(user_id)
        .bind(intent)
        .bind(unix_seconds())
        .execute(&self.pg)
        .await;
    }

    async fn cached_response(&self, key: &str) -> Option<AssistantCachedResponse> {
        if key.is_empty() {
            return None;
        }
        let mut connection = self.valkey.get_multiplexed_async_connection().await.ok()?;
        let raw = redis::cmd("GET")
            .arg(format!("{ASSISTANT_RESPONSE_CACHE_NAMESPACE}:{key}"))
            .query_async::<Option<String>>(&mut connection)
            .await
            .ok()??;
        let value = serde_json::from_str::<Value>(&raw).ok()?;
        let status = value
            .get("status")?
            .as_u64()
            .and_then(|status| u16::try_from(status).ok())
            .and_then(|status| StatusCode::from_u16(status).ok())?;
        let body = value
            .get("body")?
            .as_str()
            .and_then(|body| BASE64_STANDARD.decode(body).ok())?;
        (status.is_success() && !body.is_empty())
            .then_some(AssistantCachedResponse { status, body })
    }

    async fn store_cached_response(
        &self,
        key: &str,
        response: &AssistantCachedResponse,
        ttl: Duration,
    ) {
        if key.is_empty() || !response.status.is_success() || response.body.is_empty() {
            return;
        }
        let Ok(mut connection) = self.valkey.get_multiplexed_async_connection().await else {
            return;
        };
        let value = json!({
            "status": response.status.as_u16(),
            "body": BASE64_STANDARD.encode(&response.body),
        })
        .to_string();
        let _ = redis::cmd("SETEX")
            .arg(format!("{ASSISTANT_RESPONSE_CACHE_NAMESPACE}:{key}"))
            .arg(ttl.as_secs().max(1))
            .arg(value)
            .query_async::<()>(&mut connection)
            .await;
    }

    async fn create_key(
        &self,
        user_id: i64,
        username: &str,
        name: &str,
        group: &str,
    ) -> Result<AssistantCreatedKey, CreateAssistantKeyError> {
        let max_tokens = sqlx::query_scalar::<_, String>(
            "SELECT value FROM options WHERE key = 'token_setting.max_user_tokens' LIMIT 1",
        )
        .fetch_optional(&self.pg)
        .await
        .ok()
        .flatten()
        .map_or(DEFAULT_MAX_USER_TOKENS, |value| {
            parse_max_user_tokens(&value, DEFAULT_MAX_USER_TOKENS)
        });
        let count = sqlx::query_scalar::<_, i64>(
            "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_one(&self.pg)
        .await
        .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        if count >= max_tokens {
            return Err(CreateAssistantKeyError::TokenLimit(max_tokens));
        }

        let key = generate_assistant_key();
        let now = unix_seconds();
        let has_auto_groups_column = sqlx::query_scalar::<_, bool>(
            "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'tokens' AND column_name = 'auto_groups')",
        )
        .fetch_one(&self.pg)
        .await
        .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        let mut transaction = self
            .pg
            .begin()
            .await
            .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        let id = if has_auto_groups_column {
            sqlx::query_scalar::<_, i64>(
                "INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry, auto_groups) VALUES ($1, $2, 1, $3, $4, $4, -1, 0, TRUE, FALSE, '', '', 0, $5, $6, '') RETURNING id::BIGINT",
            )
            .bind(user_id)
            .bind(&key)
            .bind(name)
            .bind(now)
            .bind(group)
            .bind(group == "auto")
            .fetch_one(&mut *transaction)
            .await
        } else {
            sqlx::query_scalar::<_, i64>(
                "INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry) VALUES ($1, $2, 1, $3, $4, $4, -1, 0, TRUE, FALSE, '', '', 0, $5, $6) RETURNING id::BIGINT",
            )
            .bind(user_id)
            .bind(&key)
            .bind(name)
            .bind(now)
            .bind(group)
            .bind(group == "auto")
            .fetch_one(&mut *transaction)
            .await
        }
        .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        sqlx::query(
            "UPDATE users SET console_activated_at = $2 WHERE id = $1 AND deleted_at IS NULL AND console_activated_at = 0",
        )
        .bind(user_id)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        transaction
            .commit()
            .await
            .map_err(|error| CreateAssistantKeyError::Unavailable(error.to_string()))?;
        self.evict_user_cache(user_id).await;
        self.record_system_log(
            user_id,
            username,
            format!("created API key {id} via assistant"),
        )
        .await;
        Ok(AssistantCreatedKey {
            id,
            name: name.to_owned(),
            key: format!("sk-{key}"),
            group: group.to_owned(),
            expired_time: -1,
        })
    }

    async fn submit_handoff(
        &self,
        user_id: i64,
        username: &str,
        message: &str,
    ) -> Result<AssistantLead, String> {
        let mut transaction = self.pg.begin().await.map_err(|error| error.to_string())?;
        sqlx::query("SELECT id FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE")
            .bind(user_id)
            .fetch_one(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
        let existing = sqlx::query(
            "SELECT id::BIGINT AS id, user_id::BIGINT AS user_id, \
             COALESCE(source, '') AS source, COALESCE(intent, '') AS intent, \
             COALESCE(message, '') AS message, COALESCE(status, '') AS status, \
             COALESCE(admin_user_id, 0)::BIGINT AS admin_user_id, \
             COALESCE(admin_note, '') AS admin_note, \
             COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(resolved_at, 0)::BIGINT AS resolved_at \
             FROM assistant_leads WHERE user_id = $1 AND source = $2 AND status = $3 \
             ORDER BY id DESC LIMIT 1",
        )
        .bind(user_id)
        .bind(ASSISTANT_HANDOFF_SOURCE)
        .bind(ASSISTANT_HANDOFF_PENDING)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|error| error.to_string())?;
        let lead = if let Some(row) = existing {
            assistant_lead_from_row(&row).map_err(|error| error.to_string())?
        } else {
            let created_at = unix_seconds();
            let row = sqlx::query(
                "INSERT INTO assistant_leads \
                 (user_id, source, intent, message, status, created_at) \
                 VALUES ($1, $2, $3, $4, $5, $6) \
                 RETURNING id::BIGINT AS id, user_id::BIGINT AS user_id, \
                 COALESCE(source, '') AS source, COALESCE(intent, '') AS intent, \
                 COALESCE(message, '') AS message, COALESCE(status, '') AS status, \
                 COALESCE(admin_user_id, 0)::BIGINT AS admin_user_id, \
                 COALESCE(admin_note, '') AS admin_note, \
                 COALESCE(created_at, 0)::BIGINT AS created_at, \
                 COALESCE(resolved_at, 0)::BIGINT AS resolved_at",
            )
            .bind(user_id)
            .bind(ASSISTANT_HANDOFF_SOURCE)
            .bind(ASSISTANT_HANDOFF_INTENT)
            .bind(message)
            .bind(ASSISTANT_HANDOFF_PENDING)
            .bind(created_at)
            .fetch_one(&mut *transaction)
            .await
            .map_err(|error| error.to_string())?;
            assistant_lead_from_row(&row).map_err(|error| error.to_string())?
        };
        transaction
            .commit()
            .await
            .map_err(|error| error.to_string())?;
        self.record_system_log(
            user_id,
            username,
            format!("submitted assistant support request {}", lead.id),
        )
        .await;
        Ok(lead)
    }

    async fn resolve_handoff(
        &self,
        admin_user_id: i64,
        admin_username: &str,
        lead_id: i64,
        note: &str,
    ) -> Result<AssistantLead, ResolveHandoffError> {
        let mut transaction = self
            .pg
            .begin()
            .await
            .map_err(|error| ResolveHandoffError::Unavailable(error.to_string()))?;
        let row = sqlx::query(
            "SELECT id::BIGINT AS id, user_id::BIGINT AS user_id, \
             COALESCE(source, '') AS source, COALESCE(intent, '') AS intent, \
             COALESCE(message, '') AS message, COALESCE(status, '') AS status, \
             COALESCE(admin_user_id, 0)::BIGINT AS admin_user_id, \
             COALESCE(admin_note, '') AS admin_note, \
             COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(resolved_at, 0)::BIGINT AS resolved_at \
             FROM assistant_leads WHERE id = $1 AND source = $2 FOR UPDATE",
        )
        .bind(lead_id)
        .bind(ASSISTANT_HANDOFF_SOURCE)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(|error| ResolveHandoffError::Unavailable(error.to_string()))?;
        let Some(row) = row else {
            return Err(ResolveHandoffError::NotFound);
        };
        let mut lead = assistant_lead_from_row(&row)
            .map_err(|error| ResolveHandoffError::Unavailable(error.to_string()))?;
        if lead.status != ASSISTANT_HANDOFF_PENDING {
            return Err(ResolveHandoffError::AlreadyResolved);
        }
        let resolved_at = unix_seconds();
        sqlx::query(
            "UPDATE assistant_leads SET status = $2, admin_user_id = $3, admin_note = $4, \
             resolved_at = $5 WHERE id = $1",
        )
        .bind(lead_id)
        .bind(ASSISTANT_HANDOFF_RESOLVED)
        .bind(admin_user_id)
        .bind(note)
        .bind(resolved_at)
        .execute(&mut *transaction)
        .await
        .map_err(|error| ResolveHandoffError::Unavailable(error.to_string()))?;
        transaction
            .commit()
            .await
            .map_err(|error| ResolveHandoffError::Unavailable(error.to_string()))?;

        lead.status = ASSISTANT_HANDOFF_RESOLVED.to_owned();
        lead.admin_user_id = admin_user_id;
        lead.admin_note = note.to_owned();
        lead.resolved_at = resolved_at;

        self.record_system_log(
            admin_user_id,
            admin_username,
            format!("resolved assistant support request {lead_id}"),
        )
        .await;
        Ok(lead)
    }

    async fn record_admin_audit(&self, audit: AssistantAdminAudit) {
        let route = "/api/assistant/admin/handoffs/:id/resolve";
        let path = format!("/api/assistant/admin/handoffs/{}/resolve", audit.lead_id);
        let other = json!({
            "op": {
                "action": "generic",
                "params": {"method": "POST", "route": route},
            },
            "admin_info": {
                "admin_id": audit.actor_id,
                "admin_username": audit.actor_username,
                "admin_role": audit.actor_role,
                "auth_method": audit.auth_method,
            },
            "audit_info": {
                "method": "POST",
                "route": route,
                "path": path,
                "status": audit.status.as_u16(),
                "success": audit.success,
                "params": {"id": audit.lead_id},
            },
        });
        let _ = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, ip, other, request_id) \
             VALUES ($1, $2, 3, $3, $4, $5, $6, $7)",
        )
        .bind(audit.actor_id)
        .bind(unix_seconds())
        .bind(format!("POST {route}"))
        .bind(audit.actor_username)
        .bind(audit.client_ip)
        .bind(other.to_string())
        .bind(uuid::Uuid::new_v4().to_string())
        .execute(&self.pg)
        .await;
    }
}

fn unix_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

fn parse_max_user_tokens(value: &str, fallback: i64) -> i64 {
    value.parse::<i64>().unwrap_or_else(|_| {
        value
            .parse::<f64>()
            .ok()
            .filter(|number| number.is_finite())
            .map_or(fallback, |number| number as i64)
    })
}

fn generate_assistant_key() -> String {
    rand::rng()
        .sample_iter(&Alphanumeric)
        .take(48)
        .map(char::from)
        .collect()
}

fn assistant_lead_from_row(row: &PgRow) -> Result<AssistantLead, sqlx::Error> {
    Ok(AssistantLead {
        id: row.try_get("id")?,
        user_id: row.try_get("user_id")?,
        source: row.try_get("source")?,
        intent: row.try_get("intent")?,
        message: row.try_get("message")?,
        status: row.try_get("status")?,
        admin_user_id: row.try_get("admin_user_id")?,
        admin_note: row.try_get("admin_note")?,
        created_at: row.try_get("created_at")?,
        resolved_at: row.try_get("resolved_at")?,
    })
}

#[derive(Clone)]
pub struct AssistantReadState {
    pub(crate) pg: PgPool,
    pub(crate) session_secret: SecretString,
    pub(crate) auth: Arc<dyn DashboardAuth>,
    store: Arc<dyn AssistantReadStore>,
    user_rate_limiter: Arc<dyn AssistantUserRateLimiter>,
    agent_backend: Arc<dyn AssistantAgentBackend>,
}

impl AssistantReadState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        valkey: redis::Client,
        auth: Arc<dyn DashboardAuth>,
        session_secret: SecretString,
        rate_limit_config: AssistantRateLimitConfig,
    ) -> Self {
        let store = Arc::new(PgAssistantReadStore {
            pg: pg.clone(),
            valkey: valkey.clone(),
        });
        let user_rate_limiter = Arc::new(ValkeyAssistantUserRateLimiter {
            valkey,
            config: rate_limit_config,
        });
        Self {
            pg,
            session_secret,
            auth,
            store,
            user_rate_limiter,
            agent_backend: Arc::new(DisabledAssistantAgentBackend),
        }
    }

    /// Enables the server-funded assistant relay on the normal listener.
    #[must_use]
    pub fn with_agent_relay(
        mut self,
        client: reqwest::Client,
        response_header_timeout: Duration,
    ) -> Self {
        self.agent_backend = Arc::new(PgAssistantAgentBackend {
            pg: self.pg.clone(),
            upstream: OpenAiUpstreamClient::new(client, response_header_timeout),
            quota_per_request: 1,
        });
        self
    }

    #[cfg(test)]
    fn with_store(mut self, store: Arc<dyn AssistantReadStore>) -> Self {
        self.store = store;
        self
    }

    #[cfg(test)]
    fn with_user_rate_limiter(mut self, limiter: Arc<dyn AssistantUserRateLimiter>) -> Self {
        self.user_rate_limiter = limiter;
        self
    }

    #[cfg(test)]
    fn with_agent_backend(mut self, backend: Arc<dyn AssistantAgentBackend>) -> Self {
        self.agent_backend = backend;
        self
    }
}

#[derive(Serialize)]
struct Envelope<T> {
    success: bool,
    message: &'static str,
    data: T,
}

pub fn assistant_read_router(state: AssistantReadState) -> Router {
    Router::new()
        .route("/api/assistant/status", get(assistant_status))
        .route("/api/assistant/models", get(assistant_models))
        .route("/api/assistant/offers", get(offers))
        .route("/api/assistant/chat", post(assistant_chat))
        .route(
            "/api/assistant/tools/create-key",
            post(create_assistant_key),
        )
        .route("/api/assistant/handoffs", post(submit_handoff))
        .route("/api/assistant/handoffs/self", get(self_handoff))
        .route("/api/assistant/admin/handoffs", get(admin_handoffs))
        .route(
            "/api/assistant/admin/handoffs/{id}/resolve",
            post(admin_resolve_handoff),
        )
        .route("/api/assistant/admin/intents", get(admin_intents))
        .merge(assistant_extended::extended_router())
        .with_state(state)
}

pub(crate) struct AssistantPrincipal {
    pub(crate) user: DashboardUserView,
    pub(crate) credential: String,
}

async fn assistant_route(
    state: &AssistantReadState,
    settings: &AssistantSettingsView,
) -> Result<(String, String), Response> {
    let group = if settings.group.trim().is_empty() {
        "default"
    } else {
        settings.group.trim()
    };
    let models = state.store.assistant_model_ids(group).await.map_err(|_| {
        assistant_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_MODEL_CATALOG_UNAVAILABLE",
            "assistant model catalog is temporarily unavailable",
        )
    })?;
    if models.is_empty() {
        return Err(assistant_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_ROUTING_GROUP_UNAVAILABLE",
            "assistant routing group has no enabled models",
        ));
    }
    let configured_model = settings.model.trim();
    if !configured_model.is_empty() {
        if models.iter().any(|model| model == configured_model) {
            return Ok((group.to_owned(), configured_model.to_owned()));
        }
        return Err(assistant_error_owned(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_ROUTING_GROUP_UNAVAILABLE",
            format!("assistant model is not enabled in routing group {group:?}"),
        ));
    }
    Ok((group.to_owned(), models[0].clone()))
}

async fn assistant_models(
    State(state): State<AssistantReadState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let settings = match state.store.settings().await {
        Ok(settings) => settings,
        Err(error) => return api_error(error),
    };
    let requested_group = query_value(raw_query.as_deref(), "group");
    let group = if requested_group.trim().is_empty() {
        settings.group
    } else {
        requested_group.trim().to_owned()
    };
    match state.store.assistant_model_ids(&group).await {
        Ok(models) => success(json!(models)),
        Err(_) => assistant_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_MODEL_CATALOG_UNAVAILABLE",
            "assistant model catalog is temporarily unavailable",
        ),
    }
}

async fn assistant_status(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let settings = match state.store.settings().await {
        Ok(settings) => settings,
        Err(error) => return api_error(error),
    };
    let (assistant_group, assistant_model, route_available) =
        match assistant_route(&state, &settings).await {
            Ok((group, model)) => (group, model, true),
            Err(_) => (settings.group.clone(), String::new(), false),
        };
    success(json!({
        "enabled": settings.enabled,
        "group": assistant_group,
        "model": assistant_model,
        "route_available": route_available,
        "funding": {
            "mode": "super_administrator",
        },
        "developer_access_granted": principal.user.developer_access_granted,
        "agent": {
            "enabled": settings.agent_loop_enabled,
            "max_steps": settings.max_steps,
            "timeout_seconds": settings.timeout_seconds,
            "cache_enabled": settings.cache_enabled,
            "cache_ttl_minutes": settings.cache_ttl_minutes,
        },
    }))
}

async fn self_handoff(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    let principal = match authenticated_user(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    with_no_store(match state.store.latest_handoff(principal.user.id).await {
        Ok(lead) => success(json!(lead)),
        Err(error) => api_error(error),
    })
}

async fn assistant_chat(
    State(state): State<AssistantReadState>,
    request: axum::extract::Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match state
        .user_rate_limiter
        .check("assistant", principal.user.id)
        .await
    {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::TOO_MANY_REQUESTS,
                Some(retry_after_seconds),
            ));
        }
        Err(()) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        }
    }
    let mut settings = match state.store.settings().await {
        Ok(settings) => settings,
        Err(error) => return api_error(error),
    };
    if !settings.enabled {
        return assistant_error(
            StatusCode::SERVICE_UNAVAILABLE,
            "ASSISTANT_DISABLED",
            "AI assistant is disabled",
        );
    }
    let session = match state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
    {
        Ok(session) => session,
        Err(_) => {
            return assistant_error(
                StatusCode::FORBIDDEN,
                "ASSISTANT_SESSION_REQUIRED",
                "AI assistant requires a browser login session",
            );
        }
    };
    let (assistant_group, assistant_model) = match assistant_route(&state, &settings).await {
        Ok(route) => route,
        Err(response) => return response,
    };
    settings.group = assistant_group;
    settings.model = assistant_model;
    let input = match assistant_chat_input(request).await {
        Ok(input) => input,
        Err(response) => return response,
    };
    let (conversation, latest_message) = match normalize_assistant_conversation(input) {
        Ok(conversation) => conversation,
        Err(response) => return *response,
    };
    let intent = classify_assistant_intent(&latest_message);
    state.store.record_intent(principal.user.id, intent).await;

    let cache_key = assistant_cache_key(&settings, &conversation);
    if let Some(cached) = state.store.cached_response(&cache_key).await {
        let mut response = assistant_raw_response(cached.status, cached.body, None);
        response.headers_mut().insert(
            "x-lmm-assistant-cache",
            axum::http::HeaderValue::from_static("HIT"),
        );
        set_assistant_intent_header(&mut response, intent);
        return response;
    }
    let billing = match state.store.billing_account().await {
        Ok(billing) => billing,
        Err(_) => {
            return assistant_error(
                StatusCode::SERVICE_UNAVAILABLE,
                "ASSISTANT_BILLING_ACCOUNT_UNAVAILABLE",
                "AI assistant billing account is unavailable",
            );
        }
    };
    let mut response = run_assistant_agent(
        &state,
        &principal.user,
        &session.session_id,
        settings,
        conversation,
        cache_key,
        billing,
    )
    .await;
    set_assistant_intent_header(&mut response, intent);
    response
}

async fn assistant_chat_input(
    request: axum::extract::Request,
) -> Result<AssistantChatInput, Response> {
    let body = to_bytes(request.into_body(), ASSISTANT_CHAT_BODY_LIMIT_BYTES)
        .await
        .map_err(|_| invalid_assistant_chat_request())?;
    if body.is_empty() {
        return Err(invalid_assistant_chat_request());
    }
    let value: Value =
        serde_json::from_slice(&body).map_err(|_| invalid_assistant_chat_request())?;
    if value.is_null() {
        return Ok(AssistantChatInput::default());
    }
    serde_json::from_value(value).map_err(|_| invalid_assistant_chat_request())
}

fn invalid_assistant_chat_request() -> Response {
    assistant_error(
        StatusCode::BAD_REQUEST,
        "ASSISTANT_INVALID_REQUEST",
        "invalid assistant request",
    )
}

fn normalize_assistant_conversation(
    mut input: AssistantChatInput,
) -> Result<(Vec<AssistantOpenAiMessage>, String), Box<Response>> {
    input.message = input.message.trim().to_owned();
    if input.messages.is_empty() {
        if input.message.is_empty() {
            return Err(Box::new(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_MESSAGE_REQUIRED",
                "assistant message is required",
            )));
        }
        if input.message.chars().count() > ASSISTANT_MESSAGE_MAX_CHARS {
            return Err(Box::new(assistant_error(
                StatusCode::PAYLOAD_TOO_LARGE,
                "ASSISTANT_MESSAGE_TOO_LONG",
                "assistant message must be at most 4000 characters",
            )));
        }
        let latest = input.message;
        return Ok((
            vec![AssistantOpenAiMessage {
                role: "user".to_owned(),
                content: latest.clone(),
                ..AssistantOpenAiMessage::default()
            }],
            latest,
        ));
    }
    if input.messages.len() > ASSISTANT_CONVERSATION_MAX_ITEMS {
        return Err(Box::new(assistant_error(
            StatusCode::PAYLOAD_TOO_LARGE,
            "ASSISTANT_CONVERSATION_TOO_LONG",
            "assistant conversation is too long",
        )));
    }
    let mut total_chars = 0;
    for (index, message) in input.messages.iter_mut().enumerate() {
        message.role = message.role.trim().to_owned();
        message.content = message.content.trim().to_owned();
        if !matches!(message.role.as_str(), "user" | "assistant") {
            return Err(Box::new(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_INVALID_CONVERSATION",
                "assistant conversation accepts only user and assistant roles",
            )));
        }
        if index == 0 && message.role != "user" {
            return Err(Box::new(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_INVALID_CONVERSATION",
                "assistant conversation must start with a user message",
            )));
        }
        if message.content.is_empty() {
            return Err(Box::new(assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_INVALID_CONVERSATION",
                "assistant conversation messages cannot be empty",
            )));
        }
        let message_chars = message.content.chars().count();
        total_chars += message_chars;
        if message_chars > ASSISTANT_MESSAGE_MAX_CHARS
            || total_chars > ASSISTANT_CONVERSATION_MAX_CHARS
        {
            return Err(Box::new(assistant_error(
                StatusCode::PAYLOAD_TOO_LARGE,
                "ASSISTANT_CONVERSATION_TOO_LONG",
                "assistant conversation is too long",
            )));
        }
    }
    let latest = input
        .messages
        .last()
        .map(|message| message.content.clone())
        .unwrap_or_default();
    if input
        .messages
        .last()
        .is_none_or(|message| message.role != "user")
    {
        return Err(Box::new(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_INVALID_CONVERSATION",
            "assistant conversation must end with the current user message",
        )));
    }
    if !input.message.is_empty() && input.message != latest {
        return Err(Box::new(assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_INVALID_CONVERSATION",
            "assistant message must match the latest conversation message",
        )));
    }
    Ok((input.messages, latest))
}

fn set_assistant_intent_header(response: &mut Response, intent: &'static str) {
    response.headers_mut().insert(
        "x-lmm-assistant-intent",
        axum::http::HeaderValue::from_static(intent),
    );
}

#[allow(clippy::too_many_arguments)]
async fn run_assistant_agent(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    settings: AssistantSettingsView,
    conversation: Vec<AssistantOpenAiMessage>,
    cache_key: String,
    billing: AssistantBillingAccount,
) -> Response {
    let timeout = Duration::from_secs(settings.timeout_seconds.max(5) as u64);
    match tokio::time::timeout(
        timeout,
        run_assistant_agent_inner(
            state,
            actor,
            session_id,
            &settings,
            conversation,
            cache_key,
            billing,
        ),
    )
    .await
    {
        Ok(response) => response,
        Err(_) => assistant_error(
            StatusCode::GATEWAY_TIMEOUT,
            "ASSISTANT_UPSTREAM_FAILED",
            "assistant request timed out",
        ),
    }
}

#[allow(clippy::too_many_arguments)]
async fn run_assistant_agent_inner(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    settings: &AssistantSettingsView,
    conversation: Vec<AssistantOpenAiMessage>,
    cache_key: String,
    billing: AssistantBillingAccount,
) -> Response {
    let mut messages = Vec::with_capacity(conversation.len() + 1);
    messages.push(AssistantOpenAiMessage {
        role: "system".to_owned(),
        content: build_assistant_system_prompt(settings),
        ..AssistantOpenAiMessage::default()
    });
    messages.extend(conversation);
    let max_steps = if settings.agent_loop_enabled {
        settings.max_steps.max(1)
    } else {
        1
    };
    let root_request_id = uuid::Uuid::new_v4().to_string();
    let mut used_tool = false;
    let mut client_action = None;

    for step in 0..max_steps {
        let allow_tools = settings.agent_loop_enabled && step < max_steps - 1;
        let request = AssistantOpenAiRequest {
            model: settings.model.clone(),
            messages: messages.clone(),
            stream: false,
            temperature: 0.2,
            max_tokens: 900,
            tools: if allow_tools {
                assistant_tool_definitions()
            } else {
                Vec::new()
            },
            tool_choice: allow_tools.then_some("auto"),
        };
        let body = match serde_json::to_vec(&request) {
            Ok(body) => body,
            Err(_) => {
                return assistant_error(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "ASSISTANT_REQUEST_BUILD_FAILED",
                    "failed to build assistant request",
                );
            }
        };
        let response = match state
            .agent_backend
            .relay_turn(AssistantAgentTurn {
                billing: billing.clone(),
                request_id: format!("{root_request_id}-assistant-{}", step + 1),
                model: settings.model.clone(),
                body,
            })
            .await
        {
            Ok(response) => response,
            Err(_) => {
                return assistant_error(
                    StatusCode::INTERNAL_SERVER_ERROR,
                    "ASSISTANT_REQUEST_BUILD_FAILED",
                    "failed to build assistant request",
                );
            }
        };
        if !response.status.is_success() {
            return assistant_raw_response(response.status, response.body, client_action);
        }
        let upstream = match serde_json::from_slice::<AssistantOpenAiResponse>(&response.body) {
            Ok(upstream) if !upstream.choices.is_empty() => upstream,
            _ => {
                return assistant_error(
                    StatusCode::BAD_GATEWAY,
                    "ASSISTANT_INVALID_UPSTREAM_RESPONSE",
                    "assistant upstream returned an invalid response",
                );
            }
        };
        let message = &upstream.choices[0].message;
        if message.tool_calls.is_empty() {
            if !used_tool && !cache_key.is_empty() {
                state
                    .store
                    .store_cached_response(
                        &cache_key,
                        &AssistantCachedResponse {
                            status: response.status,
                            body: response.body.clone(),
                        },
                        Duration::from_secs((settings.cache_ttl_minutes as u64) * 60),
                    )
                    .await;
            }
            let mut final_response =
                assistant_raw_response(response.status, response.body, client_action);
            if !used_tool && !cache_key.is_empty() {
                final_response.headers_mut().insert(
                    "x-lmm-assistant-cache",
                    axum::http::HeaderValue::from_static("STORE"),
                );
            }
            return final_response;
        }
        if !settings.agent_loop_enabled || step >= max_steps - 1 {
            return assistant_error(
                StatusCode::BAD_GATEWAY,
                "ASSISTANT_AGENT_MAX_STEPS",
                "assistant agent reached its step limit before producing a final answer",
            );
        }
        if message.tool_calls.len() > ASSISTANT_TOOL_CALLS_PER_TURN {
            return assistant_error(
                StatusCode::BAD_GATEWAY,
                "ASSISTANT_TOO_MANY_TOOL_CALLS",
                "assistant requested too many tools in one turn",
            );
        }
        messages.push(AssistantOpenAiMessage {
            role: "assistant".to_owned(),
            content: assistant_response_content(&message.content),
            tool_calls: message.tool_calls.clone(),
            ..AssistantOpenAiMessage::default()
        });
        used_tool = true;
        for (index, call) in message.tool_calls.iter().enumerate() {
            let outcome = execute_assistant_tool(state, actor, session_id, settings, call).await;
            if outcome.action.is_some() {
                client_action = outcome.action;
            }
            let content = serde_json::to_string(&outcome.result).unwrap_or_else(|_| {
                r#"{"ok":false,"error":"failed to encode tool result"}"#.to_owned()
            });
            let call_id = match call.id.trim() {
                "" => format!("assistant-call-{}-{}", step + 1, index + 1),
                id => id.to_owned(),
            };
            messages.push(AssistantOpenAiMessage {
                role: "tool".to_owned(),
                content,
                tool_call_id: call_id,
                ..AssistantOpenAiMessage::default()
            });
        }
    }
    assistant_error(
        StatusCode::BAD_GATEWAY,
        "ASSISTANT_AGENT_MAX_STEPS",
        "assistant agent reached its step limit",
    )
}

fn assistant_response_content(content: &Value) -> String {
    match content {
        Value::String(content) => content.clone(),
        Value::Array(parts) => parts
            .iter()
            .filter(|part| {
                part.get("type")
                    .and_then(Value::as_str)
                    .is_none_or(|kind| matches!(kind, "" | "text" | "output_text"))
            })
            .filter_map(|part| part.get("text").and_then(Value::as_str))
            .collect(),
        _ => String::new(),
    }
}

fn assistant_raw_response(
    status: StatusCode,
    mut body: Vec<u8>,
    action: Option<Value>,
) -> Response {
    if body.is_empty() {
        return assistant_error(
            StatusCode::BAD_GATEWAY,
            "ASSISTANT_UPSTREAM_FAILED",
            "assistant upstream returned an empty response",
        );
    }
    if let Some(action) = action
        && let Ok(mut payload) = serde_json::from_slice::<Map<String, Value>>(&body)
    {
        payload.insert("lmm_assistant_action".to_owned(), action);
        if let Ok(enriched) = serde_json::to_vec(&payload) {
            body = enriched;
        }
    }
    with_auth_version(
        (
            status,
            [(header::CONTENT_TYPE, "application/json; charset=utf-8")],
            body,
        )
            .into_response(),
    )
}

struct AssistantToolOutcome {
    result: Value,
    action: Option<Value>,
}

async fn execute_assistant_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    settings: &AssistantSettingsView,
    call: &AssistantOpenAiToolCall,
) -> AssistantToolOutcome {
    let arguments = match call.function.arguments.trim() {
        "" => "{}",
        arguments => arguments,
    };
    if arguments.len() > ASSISTANT_TOOL_ARGUMENTS_MAX_BYTES {
        return tool_result(json!({"ok":false,"error":"tool arguments are too large"}));
    }
    let input = match serde_json::from_str::<Value>(arguments) {
        Ok(Value::Object(input)) => input,
        Ok(Value::Null) => Map::new(),
        _ => {
            return tool_result(json!({"ok":false,"error":"tool arguments must be valid JSON"}));
        }
    };
    if call.function.name.trim() == "prepare_l1_recommendation" {
        return assistant_l1_recommendation_tool(state, actor, session_id, &input).await;
    }
    let result = match call.function.name.trim() {
        "get_service_facts" => assistant_service_facts(settings),
        "calculate_cost" => assistant_cost_tool(&input),
        "get_account_access" => assistant_account_tool(state, actor).await,
        "get_plan_offers" => assistant_plan_offers_tool(state, actor).await,
        "get_bounty_guide" => assistant_bounty_tool(state).await,
        "get_setup_guide" => assistant_setup_tool(settings, &input),
        "request_create_key" => assistant_create_key_request_tool(state, actor, &input).await,
        "request_human_support" => json!({
            "ok": true,
            "status": "confirmation_required",
            "action": "human_support",
            "ui_path": "/support",
            "message": "Ask the user to confirm sending this message to an administrator.",
            "draft_message": input_string(&input, "message"),
        }),
        "get_available_models" => assistant_models_tool(state, actor).await,
        "get_usage_summary" => assistant_usage_tool(state, actor.id, &input).await,
        "get_invitation_rewards" => assistant_invitation_tool(state, actor, settings).await,
        "get_model_pricing" => assistant_model_pricing_tool(state, actor, &input).await,
        "search_web" => assistant_search_tool(settings, &input).await,
        _ => json!({"ok":false,"error":"unknown assistant tool"}),
    };
    tool_result(result)
}

async fn assistant_l1_recommendation_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    session_id: &str,
    input: &Map<String, Value>,
) -> AssistantToolOutcome {
    if actor.developer_access_granted {
        return tool_result(json!({
            "ok": false,
            "status": "already_active",
            "error": "L1 access is already active"
        }));
    }
    if session_id.trim().is_empty() {
        return tool_result(json!({
            "ok": false,
            "error": "a browser login session is required to prepare an L1 recommendation"
        }));
    }
    let statement = input_string(input, "user_statement");
    let recommendation = input_string(input, "recommendation");
    if !(5..=2_000).contains(&statement.chars().count()) {
        return tool_result(json!({
            "ok": false,
            "status": "statement_invalid",
            "error": "user statement must contain 5 to 2000 characters"
        }));
    }
    if !(20..=2_000).contains(&recommendation.chars().count()) {
        return tool_result(json!({
            "ok": false,
            "status": "recommendation_invalid",
            "error": "AI recommendation must contain 20 to 2000 characters"
        }));
    }
    let payload = match serde_json::to_string(&json!({
        "user_statement": statement,
        "recommendation": recommendation,
    })) {
        Ok(payload) => payload,
        Err(_) => {
            return tool_result(json!({
                "ok": false,
                "error": "AI recommendation could not be prepared"
            }));
        }
    };
    let confirmation_token = match state
        .auth
        .create_assistant_l1_confirmation(
            actor.id,
            session_id,
            &payload,
            Duration::from_secs(30 * 60),
        )
        .await
    {
        Ok(token) => token,
        Err(_) => {
            return tool_result(json!({
                "ok": false,
                "error": "AI recommendation confirmation could not be created"
            }));
        }
    };
    AssistantToolOutcome {
        result: json!({
            "ok": true,
            "status": "confirmation_required",
            "action": "l1_recommendation",
            "message": "Explain that this recommendation is only a draft. Ask the user to review and explicitly confirm it in the UI; administrator approval is still required."
        }),
        action: Some(json!({
            "type": "l1_recommendation",
            "user_statement": statement,
            "recommendation": recommendation,
            "confirmation_token": confirmation_token,
        })),
    }
}

fn tool_result(result: Value) -> AssistantToolOutcome {
    AssistantToolOutcome {
        result,
        action: None,
    }
}

// These helpers describe the planned cross-version admin tool surface. They
// remain intentionally dormant in the Rust compatibility route until the Go
// implementation is exposed here; keep them out of the clippy gate without
// changing the public tool contract.
#[allow(dead_code)]
fn input_for_trace(arguments: &str) -> Map<String, Value> {
    serde_json::from_str::<Value>(arguments.trim())
        .ok()
        .and_then(|value| value.as_object().cloned())
        .unwrap_or_default()
}

#[allow(dead_code)]
fn assistant_tool_trace(
    call: &AssistantOpenAiToolCall,
    input: &Map<String, Value>,
    result: &Value,
) -> Value {
    const SAFE_KEYS: [&str; 13] = [
        "action",
        "days",
        "group",
        "identifier",
        "model_id",
        "page",
        "platform",
        "provider",
        "query",
        "section",
        "target_user_id",
        "title",
        "topic",
    ];
    let safe_input = SAFE_KEYS
        .into_iter()
        .filter_map(|key| {
            let value = input.get(key)?;
            if value.is_string() || value.is_number() || value.is_boolean() {
                Some((key.to_owned(), value.clone()))
            } else {
                None
            }
        })
        .collect::<Map<_, _>>();
    let status = if result.get("ok").and_then(Value::as_bool) == Some(false) {
        "output-error"
    } else if result.get("status").and_then(Value::as_str) == Some("confirmation_required") {
        "approval-requested"
    } else {
        "output-available"
    };
    json!({
        "name": call.function.name.trim(),
        "status": status,
        "input": safe_input,
    })
}

fn input_string(input: &Map<String, Value>, key: &str) -> String {
    input
        .get(key)
        .and_then(Value::as_str)
        .map(str::trim)
        .unwrap_or_default()
        .to_owned()
}

fn input_number(input: &Map<String, Value>, key: &str) -> Option<f64> {
    input
        .get(key)
        .and_then(Value::as_f64)
        .filter(|n| n.is_finite())
}

#[allow(dead_code)]
#[derive(Clone, Debug)]
struct AssistantTargetUser {
    id: i64,
    username: String,
    display_name: String,
    role: i64,
    status: i64,
    email: String,
    group: String,
    quota: i64,
    used_quota: i64,
    request_count: i64,
    created_at: i64,
    last_login_at: i64,
    oauth: BTreeMap<String, bool>,
}

#[allow(dead_code)]
async fn assistant_target_user(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> Result<AssistantTargetUser, String> {
    let identifier = input_string(input, "identifier");
    let requested_id = input
        .get("user_id")
        .and_then(Value::as_i64)
        .filter(|id| *id > 0)
        .or_else(|| identifier.parse::<i64>().ok().filter(|id| *id > 0));
    let is_admin = actor.role >= ADMIN_ROLE;
    if !is_admin {
        let is_self_identifier = identifier.is_empty()
            || identifier == actor.id.to_string()
            || identifier.eq_ignore_ascii_case(&actor.username)
            || (!actor.email.is_empty() && identifier.eq_ignore_ascii_case(&actor.email));
        if requested_id.is_some_and(|id| id != actor.id) || !is_self_identifier {
            return Err("regular users may inspect or manage only their own account".to_owned());
        }
    }
    let requested_id = if is_admin {
        requested_id.or(Some(actor.id))
    } else {
        Some(actor.id)
    };
    let row = sqlx::query(
        r#"SELECT id::BIGINT AS id, COALESCE(username, '') AS username,
                  COALESCE(display_name, '') AS display_name,
                  COALESCE(role, 1)::BIGINT AS role,
                  COALESCE(status, 1)::BIGINT AS status,
                  COALESCE(email, '') AS email,
                  COALESCE("group", 'default') AS "group",
                  COALESCE(quota, 0)::BIGINT AS quota,
                  COALESCE(used_quota, 0)::BIGINT AS used_quota,
                  COALESCE(request_count, 0)::BIGINT AS request_count,
                  COALESCE(created_at, 0)::BIGINT AS created_at,
                  COALESCE(last_login_at, 0)::BIGINT AS last_login_at,
                  COALESCE(github_id, '') AS github_id,
                  COALESCE(discord_id, '') AS discord_id,
                  COALESCE(oidc_id, '') AS oidc_id,
                  COALESCE(wechat_id, '') AS wechat_id,
                  COALESCE(telegram_id, '') AS telegram_id,
                  COALESCE(linux_do_id, '') AS linux_do_id
           FROM users
          WHERE deleted_at IS NULL
            AND (($1::BIGINT IS NOT NULL AND id = $1)
              OR (NULLIF($2, '') IS NOT NULL AND (username = $2 OR email = $2)))
          ORDER BY id
          LIMIT 1"#,
    )
    .bind(requested_id)
    .bind(&identifier)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| "user account could not be loaded".to_owned())?
    .ok_or_else(|| "the requested user could not be found".to_owned())?;
    let target = AssistantTargetUser {
        id: row
            .try_get("id")
            .map_err(|_| "user account could not be loaded")?,
        username: row
            .try_get("username")
            .map_err(|_| "user account could not be loaded")?,
        display_name: row
            .try_get("display_name")
            .map_err(|_| "user account could not be loaded")?,
        role: row
            .try_get("role")
            .map_err(|_| "user account could not be loaded")?,
        status: row
            .try_get("status")
            .map_err(|_| "user account could not be loaded")?,
        email: row
            .try_get("email")
            .map_err(|_| "user account could not be loaded")?,
        group: row
            .try_get("group")
            .map_err(|_| "user account could not be loaded")?,
        quota: row
            .try_get("quota")
            .map_err(|_| "user account could not be loaded")?,
        used_quota: row
            .try_get("used_quota")
            .map_err(|_| "user account could not be loaded")?,
        request_count: row
            .try_get("request_count")
            .map_err(|_| "user account could not be loaded")?,
        created_at: row
            .try_get("created_at")
            .map_err(|_| "user account could not be loaded")?,
        last_login_at: row
            .try_get("last_login_at")
            .map_err(|_| "user account could not be loaded")?,
        oauth: [
            ("github", "github_id"),
            ("discord", "discord_id"),
            ("oidc", "oidc_id"),
            ("wechat", "wechat_id"),
            ("telegram", "telegram_id"),
            ("linuxdo", "linux_do_id"),
        ]
        .into_iter()
        .map(|(provider, column)| {
            (
                provider.to_owned(),
                row.try_get::<String, _>(column)
                    .map(|value| !value.trim().is_empty())
                    .unwrap_or(false),
            )
        })
        .collect(),
    };
    if target.id != actor.id && (!is_admin || target.role >= actor.role) {
        return Err("administrators may target only permitted lower-role users".to_owned());
    }
    Ok(target)
}

#[allow(dead_code)]
fn assistant_target_action(target: &AssistantTargetUser, actor: &DashboardUserView) -> Value {
    json!({
        "requires_confirmation": true,
        "target_user_id": target.id,
        "target_username": target.username,
        "target_display_name": target.display_name,
        "target_role": target.role,
        "target_group": target.group,
        "target_is_self": target.id == actor.id,
    })
}

#[allow(dead_code)]
async fn assistant_user_overview_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> Value {
    let target = match assistant_target_user(state, actor, input).await {
        Ok(target) => target,
        Err(error) => return json!({"ok": false, "error": error}),
    };
    let custom_oauth_count = sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*)::BIGINT FROM user_oauth_bindings WHERE user_id = $1",
    )
    .bind(target.id)
    .fetch_one(&state.pg)
    .await
    .unwrap_or_default();
    let oauth = target
        .oauth
        .iter()
        .filter_map(|(provider, bound)| bound.then_some(provider.clone()))
        .collect::<Vec<_>>();
    json!({
        "ok": true,
        "user": {
            "id": target.id,
            "username": target.username,
            "display_name": target.display_name,
            "email": target.email,
            "role": target.role,
            "status": if target.status == 1 { "enabled" } else { "disabled" },
            "group": target.group,
            "quota": target.quota,
            "used_quota": target.used_quota,
            "request_count": target.request_count,
            "created_at": target.created_at,
            "last_login_at": target.last_login_at,
            "oauth_providers": oauth,
            "custom_oauth_binding_count": custom_oauth_count,
        },
        "privacy": "Passwords, API keys, access tokens, OAuth subject IDs, session IDs, and raw request content are omitted.",
    })
}

#[allow(dead_code)]
async fn assistant_user_usage_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> Value {
    let target = match assistant_target_user(state, actor, input).await {
        Ok(target) => target,
        Err(error) => return json!({"ok": false, "error": error}),
    };
    assistant_usage_tool(state, target.id, input).await
}

#[allow(dead_code)]
async fn assistant_navigation_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> AssistantToolOutcome {
    let page = input_string(input, "page");
    let path = match page.as_str() {
        "home" => "/".to_owned(),
        "getting-started" => "/getting-started".to_owned(),
        "pricing" => "/pricing".to_owned(),
        "wallet" => "/wallet".to_owned(),
        "usage-logs" => format!(
            "/usage-logs/{}",
            match input_string(input, "section").as_str() {
                "drawing" => "drawing",
                "task" => "task",
                _ => "common",
            }
        ),
        "keys" => "/keys".to_owned(),
        "profile" => "/profile".to_owned(),
        "support" => "/support".to_owned(),
        "open-source-bounties" => "/open-source-bounties".to_owned(),
        "users" if actor.role >= ADMIN_ROLE => "/users".to_owned(),
        "users" => {
            return tool_result(
                json!({"ok":false,"error":"the users page is available only to administrators"}),
            );
        }
        _ => {
            return tool_result(json!({"ok":false,"error":"page is not allowlisted"}));
        }
    };
    let identifier = input_string(input, "identifier");
    let mut query = Map::new();
    if !identifier.is_empty() {
        let target = match assistant_target_user(
            state,
            actor,
            &Map::from_iter([(String::from("identifier"), Value::String(identifier))]),
        )
        .await
        {
            Ok(target) => target,
            Err(error) => return tool_result(json!({"ok":false,"error":error})),
        };
        if path == "/users" {
            query.insert("filter".to_owned(), Value::String(target.username));
            query.insert("l0Only".to_owned(), Value::Bool(false));
        } else if path.starts_with("/usage-logs/") {
            query.insert("username".to_owned(), Value::String(target.username));
        }
    }
    let action = json!({"type":"navigate","path":path,"query":query});
    AssistantToolOutcome {
        result: json!({"ok":true,"status":"completed","page":page,"path":action["path"],"query":action["query"]}),
        action: Some(action),
    }
}

#[allow(dead_code)]
async fn assistant_user_action_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> AssistantToolOutcome {
    let action_name = input_string(input, "action");
    let target = match assistant_target_user(state, actor, input).await {
        Ok(target) => target,
        Err(error) => return tool_result(json!({"ok":false,"error":error})),
    };
    let target_action = assistant_target_action(&target, actor);
    match action_name.as_str() {
        "bind_oauth" => {
            if target.id != actor.id {
                return tool_result(
                    json!({"ok":false,"error":"OAuth binding must be completed by the target user in their own session"}),
                );
            }
            return AssistantToolOutcome {
                result: json!({"ok":true,"status":"completed","message":"Open the profile page and complete OAuth binding in the user's own authenticated session."}),
                action: Some(json!({"type":"navigate","path":"/profile","query":{}})),
            };
        }
        "change_password" => {
            let mut action = target_action;
            action["type"] = Value::String("user_password_change".to_owned());
            return AssistantToolOutcome {
                result: json!({"ok":true,"status":"confirmation_required","action":"change_password","message":"Ask the user to enter the password only in the secure confirmation card."}),
                action: Some(action),
            };
        }
        "disable" => {
            if target.id == actor.id {
                return tool_result(
                    json!({"ok":false,"error":"users cannot disable their own account through the assistant"}),
                );
            }
            let mut action = target_action;
            action["type"] = Value::String("user_account_action".to_owned());
            action["action"] = Value::String("disable".to_owned());
            return AssistantToolOutcome {
                result: json!({"ok":true,"status":"confirmation_required","action":"disable","message":"Review the target account and explicitly confirm disabling it."}),
                action: Some(action),
            };
        }
        "delete" => {
            let mut action = target_action;
            action["type"] = Value::String("user_account_action".to_owned());
            action["action"] = Value::String("delete".to_owned());
            return AssistantToolOutcome {
                result: json!({"ok":true,"status":"confirmation_required","action":"delete","message":"Review the target account and explicitly confirm deletion."}),
                action: Some(action),
            };
        }
        "unbind_oauth" => {}
        _ => return tool_result(json!({"ok":false,"error":"unsupported user action"})),
    }
    let provider = input_string(input, "provider").to_ascii_lowercase();
    if provider.is_empty() {
        return tool_result(json!({"ok":false,"error":"an OAuth provider is required"}));
    }
    let (provider_value, provider_kind, provider_label) = match provider.as_str() {
        "github" | "discord" | "oidc" | "wechat" | "telegram" | "linuxdo" => {
            (provider.clone(), "built_in", provider.clone())
        }
        value if value.starts_with("custom:") => {
            let id = value.trim_start_matches("custom:").parse::<i64>().ok();
            let Some(id) = id.filter(|id| *id > 0) else {
                return tool_result(
                    json!({"ok":false,"error":"custom OAuth provider ID is invalid"}),
                );
            };
            let label = sqlx::query_scalar::<_, String>(
                "SELECT COALESCE(NULLIF(name, ''), NULLIF(slug, ''), '') FROM custom_oauth_providers WHERE id = $1",
            )
            .bind(id)
            .fetch_optional(&state.pg)
            .await
            .ok()
            .flatten()
            .filter(|label| !label.is_empty())
            .unwrap_or_else(|| format!("Custom OAuth provider #{id}"));
            (provider, "custom", label)
        }
        _ => return tool_result(json!({"ok":false,"error":"OAuth provider is not allowlisted"})),
    };
    let mut action = target_action;
    action["type"] = Value::String("user_oauth_unbind".to_owned());
    action["provider"] = Value::String(provider_value);
    action["provider_kind"] = Value::String(provider_kind.to_owned());
    action["provider_label"] = Value::String(provider_label);
    AssistantToolOutcome {
        result: json!({"ok":true,"status":"confirmation_required","action":"unbind_oauth","message":"Review the OAuth binding and explicitly confirm unbinding it."}),
        action: Some(action),
    }
}

fn assistant_service_facts(settings: &AssistantSettingsView) -> Value {
    let configured_root = settings.server_address.trim_end_matches('/');
    let (root, base) = if configured_root.is_empty() {
        (
            "the service root shown in the current console".to_owned(),
            "the /v1 endpoint shown in the current console".to_owned(),
        )
    } else {
        (configured_root.to_owned(), format!("{configured_root}/v1"))
    };
    json!({
        "ok": true,
        "service_root": root,
        "openai_base_url": base,
        "client_model_instruction": "Call get_available_models and use an exact model_ids value; the assistant's own model is not a client default.",
        "api_keys_are_private": true,
        "key_management_path": "/keys",
        "cc_switch_import": {
            "supported": true,
            "protocol": "ccswitch://v1/import",
            "application": "claude",
            "requires_private_api_key": true,
            "ui_action": "Import to CC Switch"
        },
        "write_actions": "require explicit confirmation in the UI",
    })
}

fn assistant_cost_tool(input: &Map<String, Value>) -> Value {
    let values = [
        input_number(input, "input_tokens"),
        input_number(input, "output_tokens"),
        input_number(input, "input_usd_per_million"),
        input_number(input, "output_usd_per_million"),
    ];
    let [
        Some(input_tokens),
        Some(output_tokens),
        Some(input_price),
        Some(output_price),
    ] = values
    else {
        return json!({"ok":false,"error":"token counts and prices must be non-negative numbers"});
    };
    if [input_tokens, output_tokens, input_price, output_price]
        .iter()
        .any(|value| *value < 0.0)
    {
        return json!({"ok":false,"error":"token counts and prices must be non-negative numbers"});
    }
    let ratio = input_number(input, "group_ratio").unwrap_or(1.0);
    if ratio < 0.0 {
        return json!({"ok":false,"error":"group ratio must be a non-negative finite number"});
    }
    let input_cost = input_tokens / 1_000_000.0 * input_price;
    let output_cost = output_tokens / 1_000_000.0 * output_price;
    json!({
        "ok": true,
        "input_cost_usd": input_cost * ratio,
        "output_cost_usd": output_cost * ratio,
        "total_cost_usd": (input_cost + output_cost) * ratio,
        "group_ratio": ratio,
        "formula": "(input_tokens / 1,000,000 × input price + output_tokens / 1,000,000 × output price) × group ratio",
    })
}

async fn assistant_account_tool(state: &AssistantReadState, actor: &DashboardUserView) -> Value {
    let console_activated = match sqlx::query_scalar::<_, bool>(
        "SELECT COALESCE(console_activated_at, 0) > 0 FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(actor.id)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(Some(console_activated)) => console_activated,
        _ => return json!({"ok":false,"error":"account access could not be loaded"}),
    };
    let request = match sqlx::query(
        "SELECT status, source, reason, ai_recommendation, admin_note, created_at, reviewed_at FROM developer_access_requests WHERE user_id = $1 ORDER BY id DESC LIMIT 1",
    )
    .bind(actor.id)
    .fetch_optional(&state.pg)
    .await
    {
        Ok(request) => request,
        Err(_) => {
            return json!({"ok":false,"error":"L1 recommendation status could not be loaded"});
        }
    };
    let pending = request
        .as_ref()
        .and_then(|request| request.try_get::<String, _>("status").ok())
        .is_some_and(|status| status == "pending");
    let mut result = json!({
        "ok": true,
        "trust_level": actor.trust_level_info.level,
        "developer_access_granted": actor.developer_access_granted,
        "paid_activation_complete": actor.onboarding.paid_activation_complete,
        "console_activated": console_activated,
        "next_step": if actor.developer_access_granted {
            "Continue setup through the assistant; API-key creation still requires explicit UI confirmation."
        } else if pending {
            "Tell the user the recommendation is pending administrator review."
        } else {
            "Continue the onboarding conversation and prepare an L1 recommendation only after collecting a concrete use case."
        },
    });
    if let Some(request) = request {
        result["l1_request"] = json!({
            "status": request.try_get::<String, _>("status").unwrap_or_default(),
            "source": request.try_get::<String, _>("source").unwrap_or_default(),
            "user_statement": request.try_get::<String, _>("reason").unwrap_or_default(),
            "ai_recommendation": request
                .try_get::<String, _>("ai_recommendation")
                .unwrap_or_default(),
            "admin_note": request.try_get::<String, _>("admin_note").unwrap_or_default(),
            "created_at": request.try_get::<i64, _>("created_at").unwrap_or_default(),
            "reviewed_at": request.try_get::<i64, _>("reviewed_at").unwrap_or_default(),
        });
    }
    result
}

async fn assistant_create_key_request_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> Value {
    if !actor.developer_access_granted {
        return json!({"ok":false,"error":"L1 access is required to create an API key"});
    }
    let options = match state.store.key_group_options(&actor.group).await {
        Ok(options) => options,
        Err(_) => return json!({"ok":false,"error":"account access could not be loaded"}),
    };
    let group = input_string(input, "group");
    if group.is_empty() {
        return json!({
            "ok": true,
            "status": "group_required",
            "action": "create_key",
            "available_groups": options,
            "message": "Ask the user to choose one exact routing group before requesting confirmation.",
            "requested_name": input_string(input, "name"),
        });
    }
    if !options.iter().any(|option| option.id == group) {
        return json!({
            "ok": false,
            "status": "invalid_group",
            "error": "the selected group is not available for this account",
            "available_groups": options,
        });
    }
    json!({
        "ok": true,
        "status": "confirmation_required",
        "action": "create_key",
        "ui_path": "/keys",
        "message": "Ask the user to explicitly confirm creating the key with this exact group; do not claim that a key exists yet.",
        "requested_name": input_string(input, "name"),
        "requested_group": group,
    })
}

async fn assistant_plan_offers_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
) -> Value {
    if !actor.developer_access_granted {
        return access_denied_offer_payload();
    }
    let restricted = match payment_restricted(&state.pg, actor.id).await {
        Ok(Some(restricted)) => restricted,
        _ => return json!({"ok":false,"error":"account access could not be loaded"}),
    };
    let compliance = match payment_compliance_confirmed(&state.pg).await {
        Ok(compliance) => compliance,
        Err(_) => return json!({"ok":false,"error":"plan offers could not be loaded"}),
    };
    if !compliance {
        return offer_payload(false, restricted, json!([]), Map::new());
    }
    let plans = match enabled_plan_views(&state.pg).await {
        Ok(plans) => json!(plans),
        Err(_) => return json!({"ok":false,"error":"subscription plans could not be loaded"}),
    };
    let discounts = if restricted {
        Map::new()
    } else {
        match amount_discounts(&state.pg).await {
            Ok(discounts) => discounts,
            Err(_) => return json!({"ok":false,"error":"top-up discounts could not be loaded"}),
        }
    };
    offer_payload(true, restricted, plans, discounts)
}

async fn assistant_models_tool(state: &AssistantReadState, actor: &DashboardUserView) -> Value {
    let options = match state.store.key_group_options(&actor.group).await {
        Ok(options) => options,
        Err(_) => return json!({"ok":false,"error":"available models could not be loaded"}),
    };
    let mut groups = options
        .into_iter()
        .filter(|option| !option.automatic)
        .map(|option| option.id)
        .collect::<Vec<_>>();
    groups.sort();
    let models = match sqlx::query_scalar::<_, String>(
        "SELECT DISTINCT model FROM abilities WHERE \"group\" = ANY($1) AND COALESCE(enabled, TRUE) ORDER BY model",
    )
    .bind(&groups)
    .fetch_all(&state.pg)
    .await
    {
        Ok(models) => models,
        Err(_) => return json!({"ok":false,"error":"available models could not be loaded"}),
    };
    json!({
        "ok": true,
        "groups": groups,
        "model_ids": models,
        "model_list_path": "/models",
        "selection_required": true,
        "assistant_model_is_client": false,
    })
}

async fn assistant_usage_tool(
    state: &AssistantReadState,
    user_id: i64,
    input: &Map<String, Value>,
) -> Value {
    let days = input_number(input, "days").map_or(30, |days| days as i64);
    if !(1..=90).contains(&days) {
        return json!({"ok":false,"error":"days must be between 1 and 90"});
    }
    let end = unix_seconds();
    let start = end.saturating_sub(days * 24 * 60 * 60);
    let aggregate = match sqlx::query(
        "SELECT COUNT(*)::BIGINT AS requests, COALESCE(SUM(prompt_tokens), 0)::BIGINT AS prompt_tokens, COALESCE(SUM(completion_tokens), 0)::BIGINT AS completion_tokens, COALESCE(SUM(quota), 0)::BIGINT AS quota FROM logs WHERE user_id = $1 AND type = 2 AND created_at >= $2 AND created_at <= $3",
    )
    .bind(user_id)
    .bind(start)
    .bind(end)
    .fetch_one(&state.pg)
    .await
    {
        Ok(row) => row,
        Err(_) => return json!({"ok":false,"error":"historical usage could not be loaded"}),
    };
    let requests = aggregate.try_get::<i64, _>("requests").unwrap_or_default();
    let prompt_tokens = aggregate
        .try_get::<i64, _>("prompt_tokens")
        .unwrap_or_default();
    let completion_tokens = aggregate
        .try_get::<i64, _>("completion_tokens")
        .unwrap_or_default();
    let quota = aggregate.try_get::<i64, _>("quota").unwrap_or_default();
    let quota_per_unit = assistant_quota_per_unit(&state.pg).await;
    let models = match assistant_usage_breakdown(
        &state.pg,
        user_id,
        start,
        end,
        "model_name",
        quota_per_unit,
    )
    .await
    {
        Ok(rows) => rows,
        Err(_) => return json!({"ok":false,"error":"historical usage could not be loaded"}),
    };
    let groups =
        match assistant_usage_breakdown(&state.pg, user_id, start, end, "group", quota_per_unit)
            .await
        {
            Ok(rows) => rows,
            Err(_) => return json!({"ok":false,"error":"historical usage could not be loaded"}),
        };
    json!({
        "ok": true,
        "days": days,
        "source": "consume logs",
        "summary": {
            "start_timestamp": start,
            "end_timestamp": end,
            "requests": requests,
            "prompt_tokens": prompt_tokens,
            "completion_tokens": completion_tokens,
            "total_tokens": prompt_tokens + completion_tokens,
            "quota": quota,
            "cost_usd": quota_cost(quota, quota_per_unit),
            "models": models,
            "groups": groups,
        },
        "raw_logs": false,
    })
}

async fn assistant_quota_per_unit(pg: &PgPool) -> f64 {
    sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = 'QuotaPerUnit'")
        .fetch_optional(pg)
        .await
        .ok()
        .flatten()
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite())
        .unwrap_or(500_000.0)
}

fn quota_cost(quota: i64, quota_per_unit: f64) -> f64 {
    if quota_per_unit > 0.0 {
        quota as f64 / quota_per_unit
    } else {
        0.0
    }
}

async fn assistant_usage_breakdown(
    pg: &PgPool,
    user_id: i64,
    start: i64,
    end: i64,
    column: &str,
    quota_per_unit: f64,
) -> Result<Vec<Value>, sqlx::Error> {
    let column = if column == "group" {
        "\"group\""
    } else {
        "model_name"
    };
    let query = format!(
        "SELECT COALESCE({column}, '') AS name, COUNT(*)::BIGINT AS requests, COALESCE(SUM(prompt_tokens), 0)::BIGINT AS prompt_tokens, COALESCE(SUM(completion_tokens), 0)::BIGINT AS completion_tokens, COALESCE(SUM(quota), 0)::BIGINT AS quota FROM logs WHERE user_id = $1 AND type = 2 AND created_at >= $2 AND created_at <= $3 GROUP BY {column} ORDER BY requests DESC LIMIT 20"
    );
    let rows = sqlx::query(&query)
        .bind(user_id)
        .bind(start)
        .bind(end)
        .fetch_all(pg)
        .await?;
    let mut result = rows
        .into_iter()
        .map(|row| {
            let raw_name = row.try_get::<String, _>("name")?;
            let name = match raw_name.trim() {
                "" => "(unknown)",
                name => name,
            };
            let requests = row.try_get::<i64, _>("requests")?;
            let prompt_tokens = row.try_get::<i64, _>("prompt_tokens")?;
            let completion_tokens = row.try_get::<i64, _>("completion_tokens")?;
            let quota = row.try_get::<i64, _>("quota")?;
            Ok(json!({
                "name": name,
                "requests": requests,
                "prompt_tokens": prompt_tokens,
                "completion_tokens": completion_tokens,
                "total_tokens": prompt_tokens + completion_tokens,
                "quota": quota,
                "cost_usd": quota_cost(quota, quota_per_unit),
            }))
        })
        .collect::<Result<Vec<_>, sqlx::Error>>()?;
    result.sort_by(|left, right| {
        right["requests"]
            .as_i64()
            .cmp(&left["requests"].as_i64())
            .then_with(|| left["name"].as_str().cmp(&right["name"].as_str()))
    });
    Ok(result)
}

async fn assistant_invitation_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    settings: &AssistantSettingsView,
) -> Value {
    let rows =
        sqlx::query_as::<_, (String, String)>("SELECT key, value FROM options WHERE key = ANY($1)")
            .bind(vec!["QuotaPerUnit", "QuotaForInviter", "QuotaForInvitee"])
            .fetch_all(&state.pg)
            .await
            .unwrap_or_default()
            .into_iter()
            .collect::<HashMap<_, _>>();
    let quota_per_unit = rows
        .get("QuotaPerUnit")
        .and_then(|value| value.parse::<f64>().ok())
        .filter(|value| value.is_finite())
        .unwrap_or(500_000.0);
    let inviter = rows
        .get("QuotaForInviter")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or_default();
    let invitee = rows
        .get("QuotaForInvitee")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or_default();
    let compliance = payment_compliance_confirmed(&state.pg)
        .await
        .unwrap_or(false);
    let mut result = json!({
        "ok": true,
        "affiliate_code": actor.aff_code,
        "invited_count": actor.aff_count,
        "pending_reward_usd": quota_cost(actor.aff_quota, quota_per_unit),
        "total_reward_usd": quota_cost(actor.aff_history_quota, quota_per_unit),
        "reward_per_inviter_usd": quota_cost(inviter, quota_per_unit),
        "reward_per_invitee_usd": quota_cost(invitee, quota_per_unit),
        "payment_compliance_confirmed": compliance,
        "next_step": "Open the invitation page to generate or copy the current invitation code.",
    });
    if !actor.aff_code.is_empty() {
        let base = settings.server_address.trim_end_matches('/');
        if !base.is_empty() {
            result["affiliate_link"] =
                Value::String(format!("{base}/sign-up?aff={}", actor.aff_code));
        }
    }
    if !compliance {
        result["message"] = Value::String("Reward configuration is shown for explanation only; payment-related rewards remain subject to the platform compliance setting.".to_owned());
    }
    result
}

async fn assistant_bounty_tool(state: &AssistantReadState) -> Value {
    let rate = sqlx::query_scalar::<_, String>(
        "SELECT value FROM options WHERE key = 'OpenSourceBountyFeeRate'",
    )
    .fetch_optional(&state.pg)
    .await
    .ok()
    .flatten()
    .and_then(|value| value.trim().parse::<f64>().ok())
    .filter(|value| value.is_finite() && (0.0..=100.0).contains(value))
    .unwrap_or(1.0);
    json!({
        "ok": true,
        "steps": [
            "Open the open-source bounties page and choose create project.",
            "Provide the repository, issue or pull request, acceptance criteria, gross reward, and number of fixes.",
            "Review the platform fee, net escrow, and total balance debit before publishing.",
            "Publish only after explicitly confirming the funding action.",
            "Review submitted evidence; when work is accepted, settle the fix and optionally add a separate non-refundable tip.",
            "Use the dispute flow when publisher and contributor cannot agree; do not fabricate evidence."
        ],
        "platform_fee_percent": rate,
        "page": "/open-source-bounties",
        "message": "The public platform fee helps fund AI customer-service token costs. A bounty publisher may also give a contributor a separate tip; exact charges and escrow are shown before confirmation."
    })
}

async fn assistant_model_pricing_tool(
    state: &AssistantReadState,
    actor: &DashboardUserView,
    input: &Map<String, Value>,
) -> Value {
    let model = input_string(input, "model_id");
    if model.is_empty() {
        return json!({
            "ok": false,
            "status": "model_required",
            "error": "an exact model ID is required",
            "next_step": "Ask the user to choose a model or call get_available_models first."
        });
    }
    let selection = match user_group_selection(&state.pg, &actor.group).await {
        Ok(selection) => selection,
        Err(_) => {
            return json!({"ok":false,"error":"account pricing access could not be loaded"});
        }
    };
    let requested_group = input_string(input, "group");
    if !requested_group.is_empty() && !selection.selectable.contains_key(&requested_group) {
        return json!({
            "ok": false,
            "status": "invalid_group",
            "error": "the requested group is not available for this account"
        });
    }
    let abilities = match sqlx::query_as::<_, (String, i64)>(
        r#"SELECT a."group", COALESCE(c.type, 0)
             FROM abilities a LEFT JOIN channels c ON c.id = a.channel_id
            WHERE a.enabled = TRUE AND a.model = $1
            ORDER BY a.channel_id, a."group""#,
    )
    .bind(&model)
    .fetch_all(&state.pg)
    .await
    {
        Ok(abilities) => abilities,
        Err(_) => return json!({"ok":false,"error":"live pricing is temporarily unavailable"}),
    };
    let metadata = match sqlx::query_as::<
        _,
        (String, Option<String>, Option<i64>, Option<i64>),
    >(
        "SELECT model_name, endpoints, status, name_rule FROM models WHERE deleted_at IS NULL ORDER BY id",
    )
    .fetch_all(&state.pg)
    .await
    {
        Ok(metadata) => metadata
            .into_iter()
            .map(
                |(model_name, endpoints, status, name_rule)| AssistantPricingMetadata {
                    model_name,
                    endpoints: endpoints.unwrap_or_default(),
                    status: status.unwrap_or(1),
                    name_rule: name_rule.unwrap_or_default(),
                },
            )
            .collect::<Vec<_>>(),
        Err(_) => return json!({"ok":false,"error":"live pricing is temporarily unavailable"}),
    };
    let options = match sqlx::query_as::<_, (String, String)>(
        "SELECT key, value FROM options WHERE key = ANY($1)",
    )
    .bind(vec![
        "GroupRatio",
        "GroupGroupRatio",
        "ModelRatio",
        "ModelPrice",
        "CompletionRatio",
        "CacheRatio",
        "CreateCacheRatio",
        "billing_setting.billing_mode",
        "billing_setting.billing_expr",
    ])
    .fetch_all(&state.pg)
    .await
    {
        Ok(options) => options
            .into_iter()
            .filter_map(|(key, value)| serde_json::from_str(&value).ok().map(|value| (key, value)))
            .collect::<BTreeMap<_, _>>(),
        Err(_) => return json!({"ok":false,"error":"live pricing is temporarily unavailable"}),
    };
    assistant_model_pricing_payload(
        &actor.group,
        &model,
        &requested_group,
        selection.selectable,
        &abilities,
        &metadata,
        &options,
    )
}

struct AssistantPricingMetadata {
    model_name: String,
    endpoints: String,
    status: i64,
    name_rule: i64,
}

fn assistant_model_pricing_payload(
    actor_group: &str,
    model: &str,
    requested_group: &str,
    selectable_groups: BTreeMap<String, String>,
    abilities: &[(String, i64)],
    metadata: &[AssistantPricingMetadata],
    options: &BTreeMap<String, Value>,
) -> Value {
    let selected_metadata = assistant_pricing_metadata(model, metadata);
    let enabled_groups = abilities
        .iter()
        .map(|(group, _)| group.as_str())
        .collect::<BTreeSet<_>>();
    if abilities.is_empty()
        || selected_metadata.is_some_and(|metadata| metadata.status != 1)
        || !enabled_groups.contains("all")
            && !selectable_groups
                .keys()
                .any(|group| enabled_groups.contains(group.as_str()))
    {
        return json!({
            "ok": false,
            "status": "model_unavailable",
            "error": "the exact model ID is not available to this account",
            "next_step": "Call get_available_models and ask the user to choose one of the returned IDs."
        });
    }

    let model_price = assistant_model_option(options, "ModelPrice", model);
    let quota_type = i64::from(model_price.is_some());
    let model_ratio = assistant_model_option_number(options, "ModelRatio", model).unwrap_or(37.5);
    let completion_ratio =
        assistant_model_option_number(options, "CompletionRatio", model).unwrap_or(1.0);
    let billing_mode = assistant_model_option(options, "billing_setting.billing_mode", model)
        .and_then(Value::as_str)
        .filter(|mode| *mode == "tiered_expr")
        .unwrap_or_default();
    let billing_expression = assistant_model_option(options, "billing_setting.billing_expr", model)
        .and_then(Value::as_str)
        .unwrap_or_default();
    let group_ratios = options.get("GroupRatio").and_then(Value::as_object);
    let group_overrides = options
        .get("GroupGroupRatio")
        .and_then(Value::as_object)
        .and_then(|overrides| overrides.get(actor_group))
        .and_then(Value::as_object);
    let cache_ratio = assistant_model_option_number(options, "CacheRatio", model);
    let create_cache_ratio = assistant_model_option_number(options, "CreateCacheRatio", model);

    let mut prices = Vec::new();
    for (group, description) in selectable_groups {
        if !requested_group.is_empty() && group != requested_group {
            continue;
        }
        if !enabled_groups.contains("all") && !enabled_groups.contains(group.as_str()) {
            continue;
        }
        let group_ratio = group_overrides
            .and_then(|ratios| ratios.get(&group))
            .and_then(assistant_json_number)
            .or_else(|| {
                group_ratios
                    .and_then(|ratios| ratios.get(&group))
                    .and_then(assistant_json_number)
            })
            .unwrap_or(1.0);
        let mut entry = json!({
            "group": group,
            "group_description": description,
            "group_ratio": group_ratio,
        });
        if quota_type == 0 && billing_mode != "tiered_expr" {
            let input_rate = model_ratio * 2.0 * group_ratio;
            entry["input_usd_per_million"] = json!(input_rate);
            entry["output_usd_per_million"] = json!(input_rate * completion_ratio);
            if let Some(cache_ratio) = cache_ratio {
                entry["cache_read_usd_per_million"] = json!(input_rate * cache_ratio);
            }
            if let Some(create_cache_ratio) = create_cache_ratio {
                entry["cache_write_usd_per_million"] = json!(input_rate * create_cache_ratio);
            }
        } else if quota_type == 1 {
            entry["request_usd"] = json!(
                model_price
                    .and_then(assistant_json_number)
                    .unwrap_or_default()
                    * group_ratio
            );
        }
        prices.push(entry);
    }
    if prices.is_empty() {
        return json!({"ok":false,"error":"no usable pricing group was found for this model"});
    }

    json!({
        "ok": true,
        "model_id": model,
        "quota_type": quota_type,
        "billing_mode": billing_mode,
        "billing_expression": billing_expression,
        "prices": prices,
        "supported_endpoint_types": assistant_supported_endpoint_types(
            model,
            abilities,
            selected_metadata,
        ),
        "calculation_instruction": "The returned USD prices already include the group ratio. Pass group_ratio=1 to calculate_cost so the ratio is not applied twice.",
    })
}

fn assistant_model_option<'a>(
    options: &'a BTreeMap<String, Value>,
    key: &str,
    model: &str,
) -> Option<&'a Value> {
    options
        .get(key)
        .and_then(Value::as_object)
        .and_then(|values| values.get(model))
}

fn assistant_model_option_number(
    options: &BTreeMap<String, Value>,
    key: &str,
    model: &str,
) -> Option<f64> {
    assistant_model_option(options, key, model).and_then(assistant_json_number)
}

fn assistant_json_number(value: &Value) -> Option<f64> {
    value
        .as_f64()
        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
        .filter(|value| value.is_finite())
}

fn assistant_pricing_metadata<'a>(
    model: &str,
    metadata: &'a [AssistantPricingMetadata],
) -> Option<&'a AssistantPricingMetadata> {
    metadata
        .iter()
        .find(|metadata| metadata.name_rule == 0 && metadata.model_name == model)
        .or_else(|| {
            metadata
                .iter()
                .find(|metadata| metadata.name_rule == 1 && model.starts_with(&metadata.model_name))
        })
        .or_else(|| {
            metadata
                .iter()
                .find(|metadata| metadata.name_rule == 3 && model.ends_with(&metadata.model_name))
        })
        .or_else(|| {
            metadata
                .iter()
                .find(|metadata| metadata.name_rule == 2 && model.contains(&metadata.model_name))
        })
}

fn assistant_supported_endpoint_types(
    model: &str,
    abilities: &[(String, i64)],
    metadata: Option<&AssistantPricingMetadata>,
) -> Vec<String> {
    let mut endpoints = Vec::new();
    for (_, channel_type) in abilities {
        let channel_endpoints: &[&str] = match channel_type {
            38 => &["jina-rerank"],
            14 | 33 => &["anthropic", "openai"],
            24 | 41 => &["gemini", "openai"],
            20 => &["openai"],
            48 => &["openai", "openai-response"],
            55 => &["openai-video"],
            57 => &[
                "openai-response",
                "openai-response-compact",
                "openai-alpha-search",
            ],
            59 | 60 => &[
                "openai",
                "openai-response",
                "openai-response-compact",
                "anthropic",
                "gemini",
                "openai-alpha-search",
            ],
            _ if ["o3-pro", "o3-deep-research", "o4-mini-deep-research"]
                .iter()
                .any(|known| model.contains(known)) =>
            {
                &["openai-response"]
            }
            _ => &["openai"],
        };
        if assistant_is_image_model(model) {
            assistant_append_endpoint(&mut endpoints, "image-generation");
        }
        for endpoint in channel_endpoints {
            assistant_append_endpoint(&mut endpoints, endpoint);
        }
    }
    if let Some(metadata) = metadata
        && let Ok(Value::Object(custom)) = serde_json::from_str(&metadata.endpoints)
    {
        for (endpoint, value) in custom {
            if matches!(value, Value::String(_) | Value::Object(_)) {
                assistant_append_endpoint(&mut endpoints, &endpoint);
            }
        }
    }
    endpoints
}

fn assistant_is_image_model(model: &str) -> bool {
    let model = model.to_ascii_lowercase();
    ["dall-e-3", "dall-e-2", "gpt-image-1", "flux-", "flux.1-"]
        .iter()
        .any(|needle| model.contains(needle))
        || model.starts_with("imagen-")
}

fn assistant_append_endpoint(endpoints: &mut Vec<String>, endpoint: &str) {
    if !endpoint.is_empty() && !endpoints.iter().any(|known| known == endpoint) {
        endpoints.push(endpoint.to_owned());
    }
}

async fn assistant_search_tool(
    settings: &AssistantSettingsView,
    input: &Map<String, Value>,
) -> Value {
    let query = input_string(input, "query");
    if query.chars().count() < 2 {
        return json!({"ok":false,"error":"search query is required"});
    }
    if settings.search_url.is_empty() {
        return json!({"ok":false,"configured":false,"error":"web search is not configured by the administrator"});
    }
    let mut url = match reqwest::Url::parse(&settings.search_url) {
        Ok(url) if matches!(url.scheme(), "http" | "https") => url,
        _ => return json!({"ok":false,"error":"configured search URL is invalid"}),
    };
    url.query_pairs_mut().append_pair("q", &query);
    let client = match reqwest::Client::builder()
        .timeout(Duration::from_secs(10))
        .redirect(reqwest::redirect::Policy::none())
        .build()
    {
        Ok(client) => client,
        Err(_) => return json!({"ok":false,"error":"search request could not be created"}),
    };
    let mut request = client.get(url);
    if !settings.search_api_key.is_empty() {
        request = request
            .bearer_auth(&settings.search_api_key)
            .header("x-api-key", &settings.search_api_key);
    }
    let mut response = match request.send().await {
        Ok(response) => response,
        Err(_) => {
            return json!({"ok":false,"configured":true,"error":"search provider request failed"});
        }
    };
    let status = response.status();
    let mut body = Vec::with_capacity(64 * 1_024);
    while body.len() < 64 * 1_024 {
        let chunk = match response.chunk().await {
            Ok(Some(chunk)) => chunk,
            Ok(None) => break,
            Err(_) => {
                return json!({"ok":false,"configured":true,"error":"search provider response could not be read"});
            }
        };
        let remaining = 64 * 1_024 - body.len();
        body.extend_from_slice(&chunk[..chunk.len().min(remaining)]);
    }
    if !status.is_success() {
        return json!({"ok":false,"configured":true,"status":status.as_u16(),"error":"search provider returned an error"});
    }
    let results = serde_json::from_slice::<Value>(&body)
        .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&body).trim().to_owned()));
    json!({"ok":true,"configured":true,"query":query,"results":results})
}

fn assistant_setup_tool(settings: &AssistantSettingsView, input: &Map<String, Value>) -> Value {
    let platform = input_string(input, "platform").to_lowercase();
    let topic = input_string(input, "topic").to_lowercase();
    if !matches!(platform.as_str(), "windows" | "linux" | "macos") {
        return json!({"ok":false,"error":"platform must be windows, linux, or macos"});
    }
    if !matches!(
        topic.as_str(),
        "claude-code"
            | "cc-switch"
            | "claude-desktop"
            | "chatgpt-client"
            | "codex"
            | "cursor"
            | "open-webui"
            | "other-openai-compatible"
    ) {
        return json!({"ok":false,"error":"topic is not supported"});
    }
    let root = match settings.server_address.trim_end_matches('/') {
        "" => "<SERVICE_ROOT_URL>".to_owned(),
        root => root.to_owned(),
    };
    let openai_base = if root == "<SERVICE_ROOT_URL>" {
        "<OPENAI_BASE_URL>".to_owned()
    } else {
        format!("{root}/v1")
    };
    let model = match input_string(input, "model_id") {
        model if model.is_empty() => "<MODEL_ID_FROM_GET_AVAILABLE_MODELS>".to_owned(),
        model => model,
    };
    let mut result = json!({
        "ok": true,
        "platform": platform,
        "topic": topic,
        "service_root": root,
        "openai_base_url": openai_base,
        "client_model_id": model,
        "api_key": "<YOUR_API_KEY>",
        "security_note": "Create the key in this console, never paste an existing secret into chat, and test with a newly opened terminal or client session."
    });
    match topic.as_str() {
        "claude-code" => {
            let (install, configuration) = match platform.as_str() {
                "windows" => (
                    "winget install Anthropic.ClaudeCode".to_owned(),
                    format!(
                        "$env:ANTHROPIC_BASE_URL=\"{root}\"\n$env:ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'\n$env:ANTHROPIC_MODEL=\"{model}\"\nclaude"
                    ),
                ),
                "macos" => (
                    "brew install --cask claude-code".to_owned(),
                    format!(
                        "export ANTHROPIC_BASE_URL=\"{root}\"\nexport ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'\nexport ANTHROPIC_MODEL=\"{model}\"\nclaude"
                    ),
                ),
                _ => (
                    "curl -fsSL https://claude.ai/install.sh | bash".to_owned(),
                    format!(
                        "export ANTHROPIC_BASE_URL=\"{root}\"\nexport ANTHROPIC_AUTH_TOKEN='<YOUR_API_KEY>'\nexport ANTHROPIC_MODEL=\"{model}\"\nclaude"
                    ),
                ),
            };
            result["install_command"] = Value::String(install);
            result["configuration"] = Value::String(configuration);
            result["endpoint_format"] =
                Value::String("Anthropic Messages; use the service root without /v1".to_owned());
            result["steps"] = json!([
                "Install Claude Code with the command returned by this tool, then run claude --version.",
                "Create an API key in this console and replace only the <YOUR_API_KEY> placeholder.",
                "Apply the returned environment variables in a terminal opened for the project, then run claude."
            ]);
            result["official_docs"] =
                Value::String("https://code.claude.com/docs/en/setup".to_owned());
        }
        "cc-switch" => {
            let guide = match platform.as_str() {
                "macos" => "brew install --cask cc-switch",
                "linux" => {
                    "Download the official AppImage or distribution package; on Arch Linux use paru -S cc-switch-bin."
                }
                _ => {
                    "Download CC-Switch-v{version}-Windows.msi from the official GitHub Releases page."
                }
            };
            result["install_guide"] = Value::String(guide.to_owned());
            result["provider"] = json!({
                "application":"Claude",
                "env":{
                    "ANTHROPIC_BASE_URL":root,
                    "ANTHROPIC_AUTH_TOKEN":"<YOUR_API_KEY>",
                    "ANTHROPIC_MODEL":model
                }
            });
            result["endpoint_format"] =
                Value::String("Anthropic Messages; use the service root without /v1".to_owned());
            result["cc_switch_import"] = json!({
                "supported": true,
                "protocol": "ccswitch://v1/import",
                "resource": "provider",
                "application": "claude",
                "endpoint": root,
                "model": model,
                "api_key": "<PRIVATE_API_KEY>",
                "link_parameters": {
                    "resource": "provider",
                    "app": "claude",
                    "name": "LMM",
                    "endpoint": root,
                    "apiKey": "<PRIVATE_API_KEY>",
                    "model": model,
                    "homepage": root,
                    "enabled": true
                },
                "build_instructions": "After the user confirms and creates a key, the assistant UI replaces <PRIVATE_API_KEY> client-side and opens the CC Switch import confirmation. Never print the completed URL or ask the user to paste the key into chat.",
            });
            result["steps"] = json!([
                "Install CC Switch from the official GitHub Releases page, or use the macOS Homebrew command returned by this tool.",
                "Create or select an API key in this console; the key stays in a shielded private card.",
                "Use Import to CC Switch from that private card (or the key's CC Switch action on /keys). The UI constructs the ccswitch:// link and CC Switch shows an import confirmation.",
                "Confirm the import, enable the Claude provider, then open a new terminal and send a short Claude Code test message."
            ]);
            result["official_releases"] =
                Value::String("https://github.com/farion1231/cc-switch/releases".to_owned());
            result["official_docs"] =
                Value::String("https://github.com/farion1231/cc-switch".to_owned());
        }
        "claude-desktop" => {
            result["direct_custom_gateway_supported"] = Value::Bool(false);
            result["endpoint_format"] =
                Value::String("Anthropic Messages through CC Switch local routing".to_owned());
            if platform == "linux" {
                result["supported"] = Value::Bool(false);
                result["limitation"] = Value::String("CC Switch currently manages third-party Claude Desktop profiles on Windows and macOS; use Claude Code on Linux for this service.".to_owned());
            } else {
                result["supported"] = Value::Bool(true);
                result["steps"] = json!([
                    "Install and launch the official Claude Desktop app once.",
                    "In CC Switch, enable Claude Desktop and import the Claude Code provider or add a custom provider.",
                    "Map the Sonnet role to the returned model ID, enable local routing, then fully restart Claude Desktop."
                ]);
            }
            result["official_docs"] =
                Value::String("https://code.claude.com/docs/en/desktop-quickstart".to_owned());
            result["cc_switch_docs"] = Value::String("https://github.com/farion1231/cc-switch/blob/main/docs/user-manual/en/2-providers/2.6-claude-desktop.md".to_owned());
        }
        "chatgpt-client" => {
            result["supported"] = Value::Bool(false);
            result["direct_custom_gateway_supported"] = Value::Bool(false);
            result["limitation"] = Value::String("The official ChatGPT app uses OpenAI sign-in and does not accept this service's Base URL or API key as a custom provider.".to_owned());
            result["recommended_alternatives"] = json!([
                "CC Switch",
                "Codex CLI",
                "Open WebUI",
                "another client that explicitly supports custom OpenAI-compatible providers"
            ]);
            result["official_download"] = Value::String("https://chatgpt.com/download/".to_owned());
        }
        "codex" => {
            result["install_command"] = Value::String("npm install -g @openai/codex".to_owned());
            result["api_key_command"] = Value::String(if platform == "windows" {
                "$env:LMM_API_KEY='<YOUR_API_KEY>'".to_owned()
            } else {
                "export LMM_API_KEY='<YOUR_API_KEY>'".to_owned()
            });
            result["config_path"] = Value::String("~/.codex/config.toml".to_owned());
            result["config_toml"] = Value::String(format!(
                "model = \"{model}\"\nmodel_provider = \"lmm\"\n\n[model_providers.lmm]\nname = \"LMM\"\nbase_url = \"{openai_base}\"\nenv_key = \"LMM_API_KEY\"\nwire_api = \"responses\""
            ));
            result["endpoint_format"] =
                Value::String("OpenAI Responses API; use the /v1 Base URL".to_owned());
        }
        "cursor" => {
            result["endpoint_format"] = Value::String("OpenAI-compatible; use the /v1 Base URL only if the installed Cursor version exposes a custom Base URL".to_owned());
        }
        "open-webui" | "other-openai-compatible" => {
            result["endpoint_format"] =
                Value::String("OpenAI-compatible; use the /v1 Base URL".to_owned());
        }
        _ => {}
    }
    result
}

#[derive(Debug, Default, Deserialize)]
struct AssistantCreateKeyInput {
    #[serde(default, deserialize_with = "deserialize_nullable_bool")]
    confirmed: bool,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    name: String,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    group: String,
}

async fn create_assistant_key(
    State(state): State<AssistantReadState>,
    request: axum::extract::Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    if !principal.user.developer_access_granted {
        return assistant_console_not_found();
    }
    match state
        .user_rate_limiter
        .check("assistant-create-key", principal.user.id)
        .await
    {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::TOO_MANY_REQUESTS,
                Some(retry_after_seconds),
            ));
        }
        Err(()) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        }
    }
    if state
        .auth
        .current_session(SecretString::from(principal.credential))
        .await
        .is_err()
    {
        return with_no_store(assistant_session_required());
    }
    let input = match assistant_create_key_input(request).await {
        Ok(input) => input,
        Err(response) => return with_no_store(response),
    };
    let name = match input.name.trim() {
        "" => "AI assistant key",
        name => name,
    };
    if name.chars().count() > ASSISTANT_KEY_NAME_MAX_CHARS {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_KEY_NAME_TOO_LONG",
            "API key name must be at most 50 characters",
        ));
    }
    let group = input.group.trim();
    if group.chars().count() > ASSISTANT_KEY_GROUP_MAX_CHARS {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_KEY_GROUP_TOO_LONG",
            "API key group must be at most 64 characters",
        ));
    }
    let options = match state.store.key_group_options(&principal.user.group).await {
        Ok(options) => options,
        Err(error) => return with_no_store(api_error(error)),
    };
    if group.is_empty() {
        return with_no_store(assistant_key_group_required(options));
    }
    if !options.iter().any(|option| option.id == group) {
        let message = if group == "auto" {
            "automatic routing is not available for this account"
        } else {
            "the selected group is not available for this account"
        };
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_INVALID_GROUP",
            message,
        ));
    }
    if !input.confirmed {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_CONFIRMATION_REQUIRED",
            "explicit confirmation is required",
        ));
    }
    with_no_store(
        match state
            .store
            .create_key(principal.user.id, &principal.user.username, name, group)
            .await
        {
            Ok(key) => success(json!(key)),
            Err(CreateAssistantKeyError::TokenLimit(max_tokens)) => assistant_error_owned(
                StatusCode::CONFLICT,
                "ASSISTANT_KEY_LIMIT_REACHED",
                format!("API key limit reached ({max_tokens})"),
            ),
            Err(CreateAssistantKeyError::Unavailable(error)) => api_error(error),
        },
    )
}

async fn assistant_create_key_input(
    request: axum::extract::Request,
) -> Result<AssistantCreateKeyInput, Response> {
    let body = to_bytes(request.into_body(), ASSISTANT_BODY_LIMIT_BYTES)
        .await
        .map_err(|_| invalid_assistant_create_key_request())?;
    if body.is_empty() {
        return Err(invalid_assistant_create_key_request());
    }
    let value: Value =
        serde_json::from_slice(&body).map_err(|_| invalid_assistant_create_key_request())?;
    if value.is_null() {
        return Ok(AssistantCreateKeyInput::default());
    }
    serde_json::from_value(value).map_err(|_| invalid_assistant_create_key_request())
}

fn invalid_assistant_create_key_request() -> Response {
    assistant_error(
        StatusCode::BAD_REQUEST,
        "ASSISTANT_INVALID_REQUEST",
        "invalid key creation request",
    )
}

#[derive(Debug, Default, Deserialize)]
struct AssistantHandoffInput {
    #[serde(default, deserialize_with = "deserialize_nullable_bool")]
    confirmed: bool,
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    message: String,
}

async fn submit_handoff(
    State(state): State<AssistantReadState>,
    request: axum::extract::Request,
) -> Response {
    let principal = match authenticated_user(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    match state
        .user_rate_limiter
        .check("assistant-handoff", principal.user.id)
        .await
    {
        Ok(CriticalRateLimitOutcome::Allowed) => {}
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::TOO_MANY_REQUESTS,
                Some(retry_after_seconds),
            ));
        }
        Err(()) => {
            return with_auth_version(legacy_empty_response(
                StatusCode::INTERNAL_SERVER_ERROR,
                None,
            ));
        }
    }
    if state
        .auth
        .current_session(SecretString::from(principal.credential))
        .await
        .is_err()
    {
        return with_no_store(assistant_session_required());
    }
    let input = match assistant_handoff_input(request).await {
        Ok(input) => input,
        Err(response) => return with_no_store(response),
    };
    if !input.confirmed {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_CONFIRMATION_REQUIRED",
            "explicit confirmation is required",
        ));
    }
    let message = input.message.trim();
    if message.is_empty() {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_HANDOFF_INVALID_MESSAGE",
            "support message is required",
        ));
    }
    if message.chars().count() > ASSISTANT_HANDOFF_MESSAGE_MAX_CHARS {
        return with_no_store(assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_HANDOFF_INVALID_MESSAGE",
            "support message must be at most 2000 characters",
        ));
    }
    let message = redact_assistant_handoff_message(message);
    with_no_store(
        match state
            .store
            .submit_handoff(principal.user.id, &principal.user.username, &message)
            .await
        {
            Ok(lead) => success(json!(lead)),
            Err(error) => api_error(error),
        },
    )
}

async fn assistant_handoff_input(
    request: axum::extract::Request,
) -> Result<AssistantHandoffInput, Response> {
    let body = to_bytes(request.into_body(), ASSISTANT_BODY_LIMIT_BYTES)
        .await
        .map_err(|_| invalid_assistant_handoff_request())?;
    if body.is_empty() {
        return Err(invalid_assistant_handoff_request());
    }
    let value: Value =
        serde_json::from_slice(&body).map_err(|_| invalid_assistant_handoff_request())?;
    if value.is_null() {
        return Ok(AssistantHandoffInput::default());
    }
    serde_json::from_value(value).map_err(|_| invalid_assistant_handoff_request())
}

fn invalid_assistant_handoff_request() -> Response {
    assistant_error(
        StatusCode::BAD_REQUEST,
        "ASSISTANT_INVALID_REQUEST",
        "invalid support request",
    )
}

fn deserialize_nullable_bool<'de, D>(deserializer: D) -> Result<bool, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<bool>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn deserialize_nullable_messages<'de, D>(
    deserializer: D,
) -> Result<Vec<AssistantOpenAiMessage>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<Vec<AssistantOpenAiMessage>>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn deserialize_nullable_tool_calls<'de, D>(
    deserializer: D,
) -> Result<Vec<AssistantOpenAiToolCall>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<Vec<AssistantOpenAiToolCall>>::deserialize(deserializer).map(Option::unwrap_or_default)
}

fn classify_assistant_intent(message: &str) -> &'static str {
    let message = message.trim().to_lowercase();
    for (intent, terms) in [
        (
            "onboarding",
            &[
                "新手",
                "入门",
                "审核",
                "解锁",
                "l0",
                "l1",
                "onboarding",
                "review",
                "approval",
                "getting started",
            ][..],
        ),
        (
            "human_support",
            &[
                "人工",
                "客服",
                "管理员",
                "工单",
                "human",
                "support",
                "administrator",
                "agent",
            ][..],
        ),
        (
            "cost",
            &[
                "成本",
                "费用",
                "计费",
                "消耗",
                "cost",
                "estimate",
                "billing",
                "token price",
            ][..],
        ),
        (
            "usage",
            &[
                "历史调用",
                "调用数据",
                "调用统计",
                "用量统计",
                "使用统计",
                "调用记录",
                "usage",
                "usage logs",
                "request history",
                "statistics",
            ][..],
        ),
        (
            "models",
            &[
                "有哪些模型",
                "模型列表",
                "可用模型",
                "模型清单",
                "available models",
                "model list",
                "model ids",
            ][..],
        ),
        (
            "invitation",
            &[
                "邀请奖励",
                "邀请码",
                "邀请链接",
                "邀请用户",
                "affiliate",
                "referral",
                "invite reward",
            ][..],
        ),
        (
            "client_setup",
            &[
                "claude code",
                "cc switch",
                "cc-switch",
                "chatgpt",
                "windows",
                "linux",
                "macos",
                "mac os",
                "桌面版",
                "安装",
                "配置客户端",
            ][..],
        ),
        (
            "api_key",
            &[
                "api key",
                "api-key",
                "apikey",
                "base url",
                "base_url",
                "model id",
                "模型 id",
                "模型id",
                "密钥",
                "令牌",
                "token",
                "创建 key",
                "创建key",
                "create key",
                "create a key",
                "create my key",
            ][..],
        ),
        (
            "bounty",
            &[
                "开源",
                "悬赏",
                "挑战",
                "小费",
                "bounty",
                "tip",
                "challenge",
                "任务发布",
            ][..],
        ),
        (
            "plan_purchase",
            &[
                "套餐",
                "购买",
                "划算",
                "优惠",
                "折扣",
                "订阅",
                "plan",
                "purchase",
                "discount",
                "best value",
            ][..],
        ),
    ] {
        if terms.iter().any(|term| message.contains(term)) {
            return intent;
        }
    }
    "other"
}

pub(crate) fn redact_assistant_handoff_message(message: &str) -> String {
    let message = redact_api_keys(message);
    let message = redact_bearer_tokens(&message);
    redact_named_secrets(&message)
}

fn redact_api_keys(message: &str) -> String {
    let characters = message.chars().collect::<Vec<_>>();
    let mut redacted = String::with_capacity(message.len());
    let mut index = 0;
    while index < characters.len() {
        let starts_key = starts_ascii_case_insensitive(&characters, index, "sk-")
            && (index == 0 || !is_ascii_word(characters[index - 1]));
        if starts_key {
            let token_start = index + 3;
            let mut token_end = token_start;
            while token_end < characters.len() && is_api_key_character(characters[token_end]) {
                token_end += 1;
            }
            let boundary_end = (token_start..token_end)
                .rev()
                .find(|candidate| is_ascii_word(characters[*candidate]));
            if let Some(boundary_end) = boundary_end
                && boundary_end + 1 - token_start >= 6
            {
                redacted.push_str("[REDACTED_API_KEY]");
                index = boundary_end + 1;
                continue;
            }
        }
        redacted.push(characters[index]);
        index += 1;
    }
    redacted
}

fn redact_bearer_tokens(message: &str) -> String {
    let characters = message.chars().collect::<Vec<_>>();
    let mut redacted = String::with_capacity(message.len());
    let mut index = 0;
    while index < characters.len() {
        let starts_bearer = starts_ascii_case_insensitive(&characters, index, "bearer")
            && (index == 0 || !is_ascii_word(characters[index - 1]));
        if starts_bearer {
            let mut token_start = index + "bearer".len();
            let whitespace_start = token_start;
            while token_start < characters.len() && is_go_regexp_space(characters[token_start]) {
                token_start += 1;
            }
            if token_start > whitespace_start {
                let mut token_end = token_start;
                while token_end < characters.len()
                    && is_bearer_token_character(characters[token_end])
                {
                    token_end += 1;
                }
                if token_end - token_start >= 6 {
                    while token_end < characters.len() && characters[token_end] == '=' {
                        token_end += 1;
                    }
                    redacted.push_str("Bearer [REDACTED_TOKEN]");
                    index = token_end;
                    continue;
                }
            }
        }
        redacted.push(characters[index]);
        index += 1;
    }
    redacted
}

fn redact_named_secrets(message: &str) -> String {
    let characters = message.chars().collect::<Vec<_>>();
    let mut redacted = String::with_capacity(message.len());
    let mut index = 0;
    while index < characters.len() {
        if let Some(keyword_end) = secret_keyword_end(&characters, index) {
            let mut separator = keyword_end;
            while separator < characters.len() && is_go_regexp_space(characters[separator]) {
                separator += 1;
            }
            if separator < characters.len() && matches!(characters[separator], ':' | '=' | '：') {
                let mut value_start = separator + 1;
                while value_start < characters.len() && is_go_regexp_space(characters[value_start])
                {
                    value_start += 1;
                }
                let mut value_end = value_start;
                while value_end < characters.len() && !is_go_regexp_space(characters[value_end]) {
                    value_end += 1;
                }
                if value_end > value_start {
                    redacted.extend(&characters[index..keyword_end]);
                    redacted.push_str(": [REDACTED]");
                    index = value_end;
                    continue;
                }
            }
        }
        redacted.push(characters[index]);
        index += 1;
    }
    redacted
}

fn secret_keyword_end(characters: &[char], index: usize) -> Option<usize> {
    for keyword in ["password", "passwd"] {
        if starts_ascii_case_insensitive(characters, index, keyword) {
            return Some(index + keyword.len());
        }
    }
    if starts_ascii_case_insensitive(characters, index, "api") {
        let mut suffix = index + "api".len();
        if suffix < characters.len() && matches!(characters[suffix], ' ' | '_' | '-') {
            suffix += 1;
        }
        if starts_ascii_case_insensitive(characters, suffix, "key") {
            return Some(suffix + "key".len());
        }
    }
    if starts_ascii_case_insensitive(characters, index, "access") {
        let mut suffix = index + "access".len();
        if suffix < characters.len() && matches!(characters[suffix], ' ' | '_' | '-') {
            suffix += 1;
        }
        if starts_ascii_case_insensitive(characters, suffix, "token") {
            return Some(suffix + "token".len());
        }
    }
    for keyword in ["密码", "密钥", "令牌"] {
        let keyword = keyword.chars().collect::<Vec<_>>();
        if characters.get(index..index + keyword.len()) == Some(keyword.as_slice()) {
            return Some(index + keyword.len());
        }
    }
    None
}

fn starts_ascii_case_insensitive(characters: &[char], index: usize, expected: &str) -> bool {
    expected.chars().enumerate().all(|(offset, expected)| {
        characters
            .get(index + offset)
            .is_some_and(|actual| actual.is_ascii() && actual.eq_ignore_ascii_case(&expected))
    })
}

fn is_ascii_word(character: char) -> bool {
    character.is_ascii_alphanumeric() || character == '_'
}

fn is_api_key_character(character: char) -> bool {
    is_ascii_word(character) || matches!(character, '.' | '-')
}

fn is_bearer_token_character(character: char) -> bool {
    character.is_ascii_alphanumeric() || matches!(character, '.' | '_' | '~' | '+' | '/' | '-')
}

fn is_go_regexp_space(character: char) -> bool {
    matches!(character, ' ' | '\t' | '\n' | '\r' | '\u{000c}')
}

async fn admin_handoffs(
    State(state): State<AssistantReadState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let status = query_value(raw_query.as_deref(), "status")
        .trim()
        .to_owned();
    let status = if status.is_empty() {
        ASSISTANT_HANDOFF_PENDING
    } else if matches!(
        status.as_str(),
        ASSISTANT_HANDOFF_PENDING | ASSISTANT_HANDOFF_RESOLVED
    ) {
        status.as_str()
    } else {
        return assistant_error(
            StatusCode::BAD_REQUEST,
            "ASSISTANT_HANDOFF_INVALID_STATUS",
            "assistant support request status is invalid",
        );
    };
    match state.store.list_handoffs(status, 100).await {
        Ok(leads) => success(json!(leads)),
        Err(error) => api_error(error),
    }
}

async fn admin_intents(
    State(state): State<AssistantReadState>,
    RawQuery(raw_query): RawQuery,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let days = match intent_days(&query_value(raw_query.as_deref(), "days")) {
        Ok(days) => days,
        Err(message) => {
            return assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_INTENT_DAYS_INVALID",
                message,
            );
        }
    };
    let now = SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64);
    let since = now.saturating_sub(days.saturating_mul(24 * 60 * 60));
    match state.store.intent_summary(since).await {
        Ok(summary) => success(json!(summary)),
        Err(error) => api_error(error),
    }
}

#[derive(Debug, Default, Deserialize)]
struct AssistantResolveHandoffInput {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    note: String,
}

fn deserialize_nullable_string<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(Option::unwrap_or_default)
}

async fn admin_resolve_handoff(
    State(state): State<AssistantReadState>,
    Path(lead_id_raw): Path<String>,
    request: axum::extract::Request,
) -> Response {
    let principal = match authenticated_admin(&state, request.headers()).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map_or_else(|| "unknown".to_owned(), |key| key.0.clone());
    let auth_method = if state
        .auth
        .current_session(SecretString::from(principal.credential.clone()))
        .await
        .is_ok()
    {
        "session"
    } else {
        "access_token"
    };

    let rate_limit_response = match state.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(with_auth_version(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        ))),
        Err(_) => Some(with_auth_version(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        ))),
    };
    if let Some(response) = rate_limit_response {
        return audited_admin_resolve_response(
            &state,
            &principal,
            auth_method,
            client_ip,
            lead_id_raw,
            response,
            false,
        )
        .await;
    }

    let lead_id = match lead_id_raw.parse::<i64>() {
        Ok(lead_id) if lead_id > 0 => lead_id,
        _ => {
            let response = assistant_error(
                StatusCode::BAD_REQUEST,
                "ASSISTANT_HANDOFF_INVALID_ID",
                "invalid support request id",
            );
            return audited_admin_resolve_response(
                &state,
                &principal,
                auth_method,
                client_ip,
                lead_id_raw,
                response,
                false,
            )
            .await;
        }
    };
    let input = match assistant_resolve_input(request).await {
        Ok(input) => input,
        Err(response) => {
            return audited_admin_resolve_response(
                &state,
                &principal,
                auth_method,
                client_ip,
                lead_id_raw,
                response,
                false,
            )
            .await;
        }
    };
    let note = input.note.trim();
    if note.chars().count() > ASSISTANT_ADMIN_NOTE_MAX_CHARS {
        let response = assistant_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "ASSISTANT_HANDOFF_NOTE_TOO_LONG",
            "assistant support note must be at most 2000 characters",
        );
        return audited_admin_resolve_response(
            &state,
            &principal,
            auth_method,
            client_ip,
            lead_id_raw,
            response,
            false,
        )
        .await;
    }
    let (response, success) = match state
        .store
        .resolve_handoff(principal.user.id, &principal.user.username, lead_id, note)
        .await
    {
        Ok(lead) => (success(json!(lead)), true),
        Err(ResolveHandoffError::NotFound) => (
            assistant_error(
                StatusCode::NOT_FOUND,
                "ASSISTANT_HANDOFF_NOT_FOUND",
                "assistant support request not found",
            ),
            false,
        ),
        Err(ResolveHandoffError::AlreadyResolved) => (
            assistant_error(
                StatusCode::CONFLICT,
                "ASSISTANT_HANDOFF_ALREADY_RESOLVED",
                "assistant support request is already resolved",
            ),
            false,
        ),
        Err(ResolveHandoffError::Unavailable(error)) => (api_error(error), false),
    };
    audited_admin_resolve_response(
        &state,
        &principal,
        auth_method,
        client_ip,
        lead_id_raw,
        response,
        success,
    )
    .await
}

async fn assistant_resolve_input(
    request: axum::extract::Request,
) -> Result<AssistantResolveHandoffInput, Response> {
    let body = to_bytes(request.into_body(), ASSISTANT_BODY_LIMIT_BYTES)
        .await
        .map_err(|_| invalid_assistant_resolve_request())?;
    if body.is_empty() {
        return Ok(AssistantResolveHandoffInput::default());
    }
    let value: Value =
        serde_json::from_slice(&body).map_err(|_| invalid_assistant_resolve_request())?;
    if value.is_null() {
        return Ok(AssistantResolveHandoffInput::default());
    }
    serde_json::from_value(value).map_err(|_| invalid_assistant_resolve_request())
}

fn invalid_assistant_resolve_request() -> Response {
    assistant_error(
        StatusCode::BAD_REQUEST,
        "ASSISTANT_INVALID_REQUEST",
        "invalid support resolution",
    )
}

async fn audited_admin_resolve_response(
    state: &AssistantReadState,
    principal: &AssistantPrincipal,
    auth_method: &'static str,
    client_ip: String,
    lead_id: String,
    response: Response,
    success: bool,
) -> Response {
    state
        .store
        .record_admin_audit(AssistantAdminAudit {
            actor_id: principal.user.id,
            actor_username: principal.user.username.clone(),
            actor_role: principal.user.role,
            auth_method,
            client_ip,
            lead_id,
            status: response.status(),
            success,
        })
        .await;
    response
}

fn query_value(query: Option<&str>, target: &str) -> String {
    form_urlencoded::parse(query.unwrap_or_default().as_bytes())
        .find_map(|(key, value)| (key == target).then(|| value.into_owned()))
        .unwrap_or_default()
}

fn intent_days(raw: &str) -> Result<i64, &'static str> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Ok(30);
    }
    raw.parse::<i64>()
        .ok()
        .filter(|days| (1..=365).contains(days))
        .ok_or("days must be between 1 and 365")
}

async fn offers(State(state): State<AssistantReadState>, headers: HeaderMap) -> Response {
    let user = match browser_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    if !user.developer_access_granted {
        return success(access_denied_offer_payload());
    }
    let restricted = match payment_restricted(&state.pg, user.id).await {
        Ok(Some(restricted)) => restricted,
        Ok(None) | Err(_) => {
            return success(json!({
                "ok": false,
                "error": "account access could not be loaded",
            }));
        }
    };
    let compliance = match payment_compliance_confirmed(&state.pg).await {
        Ok(compliance) => compliance,
        Err(_) => {
            return success(json!({
                "ok": false,
                "error": "plan offers could not be loaded",
            }));
        }
    };
    if !compliance {
        return success(offer_payload(
            false,
            restricted,
            Value::Array(Vec::new()),
            Map::new(),
        ));
    }
    let plans = match enabled_plan_views(&state.pg).await {
        Ok(plans) => json!(plans),
        Err(_) => {
            return success(json!({
                "ok": false,
                "error": "subscription plans could not be loaded",
            }));
        }
    };
    let discounts = if restricted {
        Map::new()
    } else {
        match amount_discounts(&state.pg).await {
            Ok(discounts) => discounts,
            Err(_) => {
                return success(json!({
                    "ok": false,
                    "error": "top-up discounts could not be loaded",
                }));
            }
        }
    };
    success(offer_payload(true, restricted, plans, discounts))
}

pub(crate) async fn browser_user(
    state: &AssistantReadState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let principal = authenticated_user(state, headers).await?;
    state
        .auth
        .current_session(SecretString::from(principal.credential))
        .await
        .map_err(|_| assistant_session_required())?;
    Ok(principal.user)
}

pub(crate) async fn authenticated_user(
    state: &AssistantReadState,
    headers: &HeaderMap,
) -> Result<AssistantPrincipal, Response> {
    let credential =
        dashboard_credential(headers).ok_or_else(|| dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential.clone()))
        .await
        .map_err(|error| dashboard_auth_error(headers, Some(error.kind)))?;
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(AssistantPrincipal { user, credential })
}

pub(crate) async fn authenticated_admin(
    state: &AssistantReadState,
    headers: &HeaderMap,
) -> Result<AssistantPrincipal, Response> {
    let principal = authenticated_user(state, headers).await?;
    if principal.user.role < ADMIN_ROLE {
        return Err(user_auth_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(principal)
}

async fn payment_restricted(pg: &PgPool, user_id: i64) -> Result<Option<bool>, sqlx::Error> {
    let row = sqlx::query(
        "SELECT COALESCE(email, '') AS email, \
         COALESCE(to_jsonb(users)->>'payment_restriction_flags', '0') AS restriction_flags \
         FROM users WHERE id = $1 AND deleted_at IS NULL",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await?;
    row.map(|row| {
        let email: String = row.try_get("email")?;
        let flags = row
            .try_get::<String, _>("restriction_flags")?
            .parse::<i64>()
            .unwrap_or_default();
        Ok(flags != 0 || is_linux_do_email(&email))
    })
    .transpose()
}

async fn amount_discounts(pg: &PgPool) -> Result<Map<String, Value>, sqlx::Error> {
    let rows = sqlx::query(
        "SELECT key, value FROM options \
         WHERE key IN ('payment_setting.amount_discount', 'payment_setting')",
    )
    .fetch_all(pg)
    .await?;
    let values = rows
        .into_iter()
        .map(|row| {
            Ok((
                row.try_get::<String, _>("key")?,
                row.try_get::<String, _>("value")?,
            ))
        })
        .collect::<Result<HashMap<_, _>, sqlx::Error>>()?;
    Ok(parse_amount_discounts(&values))
}

fn parse_amount_discounts(values: &HashMap<String, String>) -> Map<String, Value> {
    let direct = values
        .get("payment_setting.amount_discount")
        .and_then(|raw| serde_json::from_str::<Map<String, Value>>(raw).ok());
    let legacy = values
        .get("payment_setting")
        .and_then(|raw| serde_json::from_str::<Value>(raw).ok())
        .and_then(|value| {
            value
                .get("amount_discount")
                .and_then(Value::as_object)
                .cloned()
        });
    direct
        .or(legacy)
        .unwrap_or_default()
        .into_iter()
        .filter(|(amount, multiplier)| {
            amount.parse::<i64>().is_ok() && multiplier.as_f64().is_some()
        })
        .collect()
}

fn offer_payload(
    compliance: bool,
    restricted: bool,
    plans: Value,
    discounts: Map<String, Value>,
) -> Value {
    let mut result = Map::from_iter([
        ("ok".to_owned(), Value::Bool(true)),
        ("developer_access_granted".to_owned(), Value::Bool(true)),
        ("read_only".to_owned(), Value::Bool(false)),
        (
            "checkout_available".to_owned(),
            Value::Bool(compliance && !restricted),
        ),
        ("payment_hidden".to_owned(), Value::Bool(restricted)),
        ("plans".to_owned(), plans),
        ("topup_discounts".to_owned(), Value::Object(discounts)),
        (
            "payment_compliance_confirmed".to_owned(),
            Value::Bool(compliance),
        ),
    ]);
    if restricted {
        result.insert(
            "message".to_owned(),
            Value::String(
                "Payment options are hidden for this account; do not direct the user to checkout."
                    .to_owned(),
            ),
        );
    }
    if !compliance {
        result.insert(
            "message".to_owned(),
            Value::String(
                "Current plan offers are unavailable until payment compliance is confirmed."
                    .to_owned(),
            ),
        );
    }
    Value::Object(result)
}

fn access_denied_offer_payload() -> Value {
    json!({
        "ok": false,
        "developer_access_granted": false,
        "read_only": false,
        "checkout_available": false,
        "payment_hidden": true,
        "plans": [],
        "topup_discounts": {},
        "payment_compliance_confirmed": false,
        "error": "L1 access is required to view plans and top-up discounts",
        "next_step": "Ask the user to submit an administrator L1 access request from the onboarding assistant."
    })
}

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

fn dashboard_auth_error(headers: &HeaderMap, kind: Option<AuthErrorKind>) -> Response {
    let (code, message) = match kind {
        Some(AuthErrorKind::TokenExpired) => (
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::SessionRevoked) => (
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        Some(AuthErrorKind::Internal) => (
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        Some(AuthErrorKind::UserDisabled) => ("AUTH_USER_DISABLED", "User has been banned"),
        _ => ("AUTH_UNAUTHORIZED", "Unauthorized, invalid access token"),
    };
    let status = if matches!(kind, Some(AuthErrorKind::Internal)) {
        StatusCode::INTERNAL_SERVER_ERROR
    } else {
        StatusCode::UNAUTHORIZED
    };
    let message = if headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
    {
        match code {
            "AUTH_INTERNAL_ERROR" => "数据库出错，请联系管理员",
            "AUTH_TOKEN_EXPIRED" | "AUTH_SESSION_REVOKED" => {
                "无权进行此操作，未登录且未提供 access token"
            }
            _ => "无权进行此操作，access token 无效",
        }
    } else {
        message
    };
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": message,
        })),
    )
        .into_response()
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers.get(header::ACCEPT_LANGUAGE).and_then(|value| value.to_str().ok()),
            ),
        })),
    )
        .into_response()
}

pub(crate) fn assistant_session_required() -> Response {
    with_auth_version(
        (
            StatusCode::FORBIDDEN,
            Json(json!({
                "success": false,
                "code": "ASSISTANT_SESSION_REQUIRED",
                "message": "assistant tools require a browser login session",
            })),
        )
            .into_response(),
    )
}

fn assistant_console_not_found() -> Response {
    with_auth_version(
        (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response(),
    )
}

fn assistant_key_group_required(options: Vec<AssistantKeyGroupOption>) -> Response {
    with_auth_version(
        (
            StatusCode::UNPROCESSABLE_ENTITY,
            Json(json!({
                "success": false,
                "code": "ASSISTANT_KEY_GROUP_REQUIRED",
                "message": "choose a routing group before confirming key creation",
                "available_groups": options,
            })),
        )
            .into_response(),
    )
}

pub(crate) fn success(data: Value) -> Response {
    with_auth_version(
        Json(Envelope {
            success: true,
            message: "",
            data,
        })
        .into_response(),
    )
}

pub(crate) fn api_error(message: String) -> Response {
    with_auth_version(
        Json(json!({
            "success": false,
            "message": message,
        }))
        .into_response(),
    )
}

pub(crate) fn assistant_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    with_auth_version(
        (
            status,
            Json(json!({
                "success": false,
                "code": code,
                "message": message,
            })),
        )
            .into_response(),
    )
}

pub(crate) fn assistant_error_owned(
    status: StatusCode,
    code: &'static str,
    message: String,
) -> Response {
    with_auth_version(
        (
            status,
            Json(json!({
                "success": false,
                "code": code,
                "message": message,
            })),
        )
            .into_response(),
    )
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        "auth-version",
        axum::http::HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

pub(crate) fn with_no_store(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        axum::http::HeaderValue::from_static(
            "no-store, no-cache, must-revalidate, private, max-age=0",
        ),
    );
    response.headers_mut().insert(
        header::PRAGMA,
        axum::http::HeaderValue::from_static("no-cache"),
    );
    response
        .headers_mut()
        .insert(header::EXPIRES, axum::http::HeaderValue::from_static("0"));
    response
}

fn is_linux_do_email(email: &str) -> bool {
    email
        .trim()
        .rsplit_once('@')
        .is_some_and(|(local, domain)| !local.is_empty() && domain.eq_ignore_ascii_case("linux.do"))
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use secrecy::{ExposeSecret, SecretString};
    use std::{collections::VecDeque, sync::Mutex};
    use tower::ServiceExt;

    use crate::auth::{
        AuthBundle, AuthError, CriticalRateLimitOutcome, DashboardSessionContext, DashboardUser,
        LoginOutcome, LoginRequest, LogoutRequest, LogoutResult, RequestMetadata,
        TwoFactorLoginRequest,
    };

    #[derive(Clone, Debug, PartialEq, Eq)]
    struct FixtureResolveCall {
        admin_user_id: i64,
        admin_username: String,
        lead_id: i64,
        note: String,
    }

    #[derive(Clone, Debug, PartialEq, Eq)]
    struct FixtureSubmitCall {
        user_id: i64,
        username: String,
        message: String,
    }

    #[derive(Clone, Debug, PartialEq, Eq)]
    struct FixtureCreateKeyCall {
        user_id: i64,
        username: String,
        name: String,
        group: String,
    }

    #[derive(Clone)]
    struct FixtureStore {
        settings: AssistantSettingsView,
        assistant_model_ids: Option<Vec<String>>,
        latest: Option<AssistantLead>,
        handoffs: Vec<AssistantLeadView>,
        expected_handoff_status: &'static str,
        summary: Vec<AssistantIntentSummary>,
        key_group_options_result: Option<Result<Vec<AssistantKeyGroupOption>, String>>,
        key_group_calls: Arc<Mutex<Vec<String>>>,
        billing_result: Option<Result<AssistantBillingAccount, String>>,
        intent_calls: Arc<Mutex<Vec<(i64, String)>>>,
        cached_response: Option<AssistantCachedResponse>,
        stored_cache: Arc<Mutex<Vec<(String, AssistantCachedResponse, Duration)>>>,
        create_key_result: Option<Result<AssistantCreatedKey, CreateAssistantKeyError>>,
        create_key_calls: Arc<Mutex<Vec<FixtureCreateKeyCall>>>,
        submit_result: Option<Result<AssistantLead, String>>,
        submit_calls: Arc<Mutex<Vec<FixtureSubmitCall>>>,
        resolve_result: Option<Result<AssistantLead, ResolveHandoffError>>,
        resolve_calls: Arc<Mutex<Vec<FixtureResolveCall>>>,
        audits: Arc<Mutex<Vec<AssistantAdminAudit>>>,
    }

    impl Default for FixtureStore {
        fn default() -> Self {
            Self {
                settings: AssistantSettingsView::default(),
                assistant_model_ids: None,
                latest: None,
                handoffs: Vec::new(),
                expected_handoff_status: ASSISTANT_HANDOFF_PENDING,
                summary: Vec::new(),
                key_group_options_result: None,
                key_group_calls: Arc::new(Mutex::new(Vec::new())),
                billing_result: None,
                intent_calls: Arc::new(Mutex::new(Vec::new())),
                cached_response: None,
                stored_cache: Arc::new(Mutex::new(Vec::new())),
                create_key_result: None,
                create_key_calls: Arc::new(Mutex::new(Vec::new())),
                submit_result: None,
                submit_calls: Arc::new(Mutex::new(Vec::new())),
                resolve_result: None,
                resolve_calls: Arc::new(Mutex::new(Vec::new())),
                audits: Arc::new(Mutex::new(Vec::new())),
            }
        }
    }

    #[async_trait]
    impl AssistantReadStore for FixtureStore {
        async fn settings(&self) -> Result<AssistantSettingsView, String> {
            Ok(self.settings.clone())
        }

        async fn assistant_model_ids(&self, group: &str) -> Result<Vec<String>, String> {
            if group != self.settings.group {
                return Ok(Vec::new());
            }
            Ok(self
                .assistant_model_ids
                .clone()
                .unwrap_or_else(|| vec![self.settings.model.clone()]))
        }

        async fn latest_handoff(&self, _: i64) -> Result<Option<AssistantLead>, String> {
            Ok(self.latest.clone())
        }

        async fn list_handoffs(
            &self,
            status: &str,
            limit: i64,
        ) -> Result<Vec<AssistantLeadView>, String> {
            if status != self.expected_handoff_status || limit != 100 {
                return Err("unexpected handoff query".to_owned());
            }
            Ok(self.handoffs.clone())
        }

        async fn intent_summary(&self, since: i64) -> Result<Vec<AssistantIntentSummary>, String> {
            if since <= 0 {
                return Err("invalid intent cutoff".to_owned());
            }
            Ok(self.summary.clone())
        }

        async fn key_group_options(
            &self,
            user_group: &str,
        ) -> Result<Vec<AssistantKeyGroupOption>, String> {
            self.key_group_calls
                .lock()
                .expect("key group call lock")
                .push(user_group.to_owned());
            self.key_group_options_result
                .clone()
                .unwrap_or_else(|| Err("unexpected key group call".to_owned()))
        }

        async fn billing_account(&self) -> Result<AssistantBillingAccount, String> {
            self.billing_result
                .clone()
                .unwrap_or_else(|| Err("unexpected billing account call".to_owned()))
        }

        async fn record_intent(&self, user_id: i64, intent: &str) {
            self.intent_calls
                .lock()
                .expect("intent call lock")
                .push((user_id, intent.to_owned()));
        }

        async fn cached_response(&self, _: &str) -> Option<AssistantCachedResponse> {
            self.cached_response.clone()
        }

        async fn store_cached_response(
            &self,
            key: &str,
            response: &AssistantCachedResponse,
            ttl: Duration,
        ) {
            self.stored_cache.lock().expect("stored cache lock").push((
                key.to_owned(),
                response.clone(),
                ttl,
            ));
        }

        async fn create_key(
            &self,
            user_id: i64,
            username: &str,
            name: &str,
            group: &str,
        ) -> Result<AssistantCreatedKey, CreateAssistantKeyError> {
            self.create_key_calls
                .lock()
                .expect("create key call lock")
                .push(FixtureCreateKeyCall {
                    user_id,
                    username: username.to_owned(),
                    name: name.to_owned(),
                    group: group.to_owned(),
                });
            self.create_key_result.clone().unwrap_or_else(|| {
                Err(CreateAssistantKeyError::Unavailable(
                    "unexpected create key call".to_owned(),
                ))
            })
        }

        async fn submit_handoff(
            &self,
            user_id: i64,
            username: &str,
            message: &str,
        ) -> Result<AssistantLead, String> {
            self.submit_calls
                .lock()
                .expect("submit call lock")
                .push(FixtureSubmitCall {
                    user_id,
                    username: username.to_owned(),
                    message: message.to_owned(),
                });
            self.submit_result
                .clone()
                .unwrap_or_else(|| Err("unexpected submit call".to_owned()))
        }

        async fn resolve_handoff(
            &self,
            admin_user_id: i64,
            admin_username: &str,
            lead_id: i64,
            note: &str,
        ) -> Result<AssistantLead, ResolveHandoffError> {
            self.resolve_calls
                .lock()
                .expect("resolve call lock")
                .push(FixtureResolveCall {
                    admin_user_id,
                    admin_username: admin_username.to_owned(),
                    lead_id,
                    note: note.to_owned(),
                });
            self.resolve_result.clone().unwrap_or_else(|| {
                Err(ResolveHandoffError::Unavailable(
                    "unexpected resolve call".to_owned(),
                ))
            })
        }

        async fn record_admin_audit(&self, audit: AssistantAdminAudit) {
            self.audits.lock().expect("audit lock").push(audit);
        }
    }

    #[derive(Clone, Copy, Default)]
    enum FixtureRateLimit {
        #[default]
        Allowed,
        Rejected(u64),
        Failed,
    }

    struct FixtureAuth {
        rate_limit: FixtureRateLimit,
    }

    impl Default for FixtureAuth {
        fn default() -> Self {
            Self {
                rate_limit: FixtureRateLimit::Allowed,
            }
        }
    }

    struct FixtureUserRateLimiter {
        outcome: Result<CriticalRateLimitOutcome, ()>,
        calls: Arc<Mutex<Vec<(String, i64)>>>,
    }

    struct FixtureAgentBackend {
        responses: Mutex<VecDeque<Result<AssistantAgentTurnResponse, String>>>,
        turns: Arc<Mutex<Vec<AssistantAgentTurn>>>,
    }

    #[async_trait]
    impl AssistantAgentBackend for FixtureAgentBackend {
        async fn relay_turn(
            &self,
            turn: AssistantAgentTurn,
        ) -> Result<AssistantAgentTurnResponse, String> {
            self.turns.lock().expect("agent turn lock").push(turn);
            self.responses
                .lock()
                .expect("agent response lock")
                .pop_front()
                .unwrap_or_else(|| Err("unexpected agent turn".to_owned()))
        }
    }

    #[async_trait]
    impl AssistantUserRateLimiter for FixtureUserRateLimiter {
        async fn check(&self, scope: &str, user_id: i64) -> Result<CriticalRateLimitOutcome, ()> {
            self.calls
                .lock()
                .expect("user rate limit call lock")
                .push((scope.to_owned(), user_id));
            self.outcome
        }
    }

    #[async_trait]
    impl DashboardAuth for FixtureAuth {
        async fn check_critical_rate_limit(
            &self,
            _: &str,
        ) -> Result<CriticalRateLimitOutcome, AuthError> {
            match self.rate_limit {
                FixtureRateLimit::Allowed => Ok(CriticalRateLimitOutcome::Allowed),
                FixtureRateLimit::Rejected(retry_after_seconds) => {
                    Ok(CriticalRateLimitOutcome::Rejected {
                        retry_after_seconds,
                    })
                }
                FixtureRateLimit::Failed => Err(AuthError::new(AuthErrorKind::Internal)),
            }
        }

        async fn login(
            &self,
            _: LoginRequest,
            _: RequestMetadata,
        ) -> Result<LoginOutcome, AuthError> {
            panic!("unused")
        }

        async fn login_2fa(
            &self,
            _: TwoFactorLoginRequest,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            panic!("unused")
        }

        async fn refresh(
            &self,
            _: SecretString,
            _: Option<String>,
            _: RequestMetadata,
        ) -> Result<AuthBundle, AuthError> {
            panic!("unused")
        }

        async fn self_user(&self, token: SecretString) -> Result<DashboardUser, AuthError> {
            let role = match token.expose_secret() {
                "user-token" | "browser-session" => 1,
                "admin-token" | "admin-session" => ADMIN_ROLE,
                _ => return Err(AuthError::new(AuthErrorKind::Unauthorized)),
            };
            Ok(fixture_user(role))
        }

        async fn current_session(
            &self,
            token: SecretString,
        ) -> Result<DashboardSessionContext, AuthError> {
            if !matches!(token.expose_secret(), "browser-session" | "admin-session") {
                return Err(AuthError::new(AuthErrorKind::Unauthorized));
            }
            let role = if token.expose_secret() == "admin-session" {
                ADMIN_ROLE
            } else {
                1
            };
            Ok(DashboardSessionContext {
                user: fixture_user(role),
                session_id: "assistant-session".to_owned(),
                client_ip: "127.0.0.1".to_owned(),
                user_agent: "assistant-test".to_owned(),
            })
        }

        async fn create_assistant_l1_confirmation(
            &self,
            _: i64,
            _: &str,
            _: &str,
            _: Duration,
        ) -> Result<String, AuthError> {
            Ok("assistant-confirmation-token".to_owned())
        }

        async fn logout(&self, _: LogoutRequest) -> Result<LogoutResult, AuthError> {
            panic!("unused")
        }

        async fn generate_personal_access_token(
            &self,
            _: SecretString,
        ) -> Result<String, AuthError> {
            panic!("unused")
        }
    }

    fn fixture_user(role: i64) -> DashboardUser {
        DashboardUser {
            id: if role >= ADMIN_ROLE { 10 } else { 7 },
            username: if role >= ADMIN_ROLE {
                "assistant-admin".to_owned()
            } else {
                "assistant-user".to_owned()
            },
            display_name: String::new(),
            role,
            status: 1,
            email: String::new(),
            github_id: String::new(),
            discord_id: String::new(),
            oidc_id: String::new(),
            wechat_id: String::new(),
            telegram_id: String::new(),
            group: "default".to_owned(),
            quota: 0,
            used_quota: 0,
            request_count: 0,
            aff_code: String::new(),
            aff_count: 0,
            aff_quota: 0,
            aff_history_quota: 0,
            inviter_id: 0,
            linux_do_id: String::new(),
            setting: "{}".to_owned(),
            stripe_customer: String::new(),
            sidebar_modules: json!({}),
            permissions: json!({}),
        }
    }

    fn fixture_lead() -> AssistantLead {
        AssistantLead {
            id: 3,
            user_id: 7,
            source: ASSISTANT_HANDOFF_SOURCE.to_owned(),
            intent: "human_support".to_owned(),
            message: "Need help".to_owned(),
            status: ASSISTANT_HANDOFF_PENDING.to_owned(),
            admin_user_id: 0,
            admin_note: String::new(),
            created_at: 1_700_000_000,
            resolved_at: 0,
        }
    }

    fn fixture_key_groups() -> Vec<AssistantKeyGroupOption> {
        vec![
            AssistantKeyGroupOption {
                id: "auto".to_owned(),
                description: "Automatic routing across the listed groups".to_owned(),
                automatic: true,
                routing_groups: vec!["default".to_owned()],
            },
            AssistantKeyGroupOption {
                id: "default".to_owned(),
                description: "默认分组".to_owned(),
                automatic: false,
                routing_groups: Vec::new(),
            },
        ]
    }

    fn fixture_router(store: FixtureStore) -> Router {
        fixture_router_with_auth(store, FixtureAuth::default())
    }

    fn fixture_router_with_auth(store: FixtureStore, auth: FixtureAuth) -> Router {
        fixture_router_with_dependencies(store, auth, None)
    }

    fn fixture_router_with_user_rate_limiter(
        store: FixtureStore,
        limiter: Arc<dyn AssistantUserRateLimiter>,
    ) -> Router {
        fixture_router_with_dependencies(store, FixtureAuth::default(), Some(limiter))
    }

    fn fixture_router_with_dependencies(
        store: FixtureStore,
        auth: FixtureAuth,
        limiter: Option<Arc<dyn AssistantUserRateLimiter>>,
    ) -> Router {
        let pg = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgres://postgres@127.0.0.1:1/assistant")
            .expect("valid lazy PostgreSQL URL");
        let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
        let mut state = AssistantReadState::new(
            pg,
            valkey,
            Arc::new(auth),
            SecretString::from("assistant-test-session-secret"),
            AssistantRateLimitConfig {
                enabled: false,
                max_requests: 1,
                window: Duration::from_secs(1),
                dependency_timeout: Duration::from_secs(1),
            },
        )
        .with_store(Arc::new(store));
        if let Some(limiter) = limiter {
            state = state.with_user_rate_limiter(limiter);
        }
        assistant_read_router(state)
    }

    fn fixture_router_with_agent(
        store: FixtureStore,
        backend: Arc<dyn AssistantAgentBackend>,
    ) -> Router {
        let pg = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgres://postgres@127.0.0.1:1/assistant")
            .expect("valid lazy PostgreSQL URL");
        let valkey = redis::Client::open("redis://127.0.0.1/").expect("valid Valkey URL");
        let state = AssistantReadState::new(
            pg,
            valkey,
            Arc::new(FixtureAuth::default()),
            SecretString::from("assistant-test-session-secret"),
            AssistantRateLimitConfig {
                enabled: false,
                max_requests: 1,
                window: Duration::from_secs(1),
                dependency_timeout: Duration::from_secs(1),
            },
        )
        .with_store(Arc::new(store))
        .with_agent_backend(backend);
        assistant_read_router(state)
    }

    async fn response_json(response: Response) -> Value {
        let bytes = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body");
        serde_json::from_slice(&bytes).expect("JSON response")
    }

    #[tokio::test]
    async fn assistant_status_should_match_go_settings_for_personal_token() {
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
            .oneshot(
                Request::get("/api/assistant/status")
                    .header(header::AUTHORIZATION, "Bearer user-token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
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
    }

    #[test]
    fn assistant_setup_tool_should_describe_cc_switch_deep_link_import() {
        let settings = AssistantSettingsView {
            server_address: "https://api.example.com/".to_owned(),
            ..AssistantSettingsView::default()
        };
        let input = serde_json::from_value::<Map<String, Value>>(json!({
            "platform": "windows",
            "topic": "cc-switch",
            "model_id": "deepseek-v4-flash",
        }))
        .expect("setup input");

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
    }

    #[tokio::test]
    async fn assistant_chat_should_own_model_prompt_billing_and_intent() {
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
            .oneshot(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from(
                        r#"{"message":"How do I create a key?","model":"client-model"}"#,
                    ))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let intent = response.headers().get("x-lmm-assistant-intent").cloned();
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body");

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body.as_ref(), upstream_body);
        assert_eq!(
            intent,
            Some(axum::http::HeaderValue::from_static("api_key"))
        );
        assert_eq!(
            *intent_calls.lock().expect("intent call lock"),
            vec![(7, "api_key".to_owned())]
        );
        let turns = turns.lock().expect("agent turn lock");
        assert_eq!(turns.len(), 1);
        assert_eq!(turns[0].billing.id, 987);
        let request: Value = serde_json::from_slice(&turns[0].body).expect("agent request JSON");
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
    }

    #[tokio::test]
    async fn assistant_chat_should_execute_bounded_tool_loop_then_force_final_answer() {
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
            .oneshot(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from(r#"{"message":"estimate cost"}"#))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        let turns = turns.lock().expect("agent turn lock");
        assert_eq!(turns.len(), 2);
        let first_request: Value =
            serde_json::from_slice(&turns[0].body).expect("first request JSON");
        let second_request: Value =
            serde_json::from_slice(&turns[1].body).expect("second request JSON");
        assert_eq!(first_request["tools"].as_array().map(Vec::len), Some(14));
        assert_eq!(first_request["tool_choice"], "auto");
        assert!(second_request.get("tools").is_none());
        assert!(second_request.get("tool_choice").is_none());
        let tool_result = second_request["messages"]
            .as_array()
            .and_then(|messages| messages.last())
            .and_then(|message| message["content"].as_str())
            .and_then(|content| serde_json::from_str::<Value>(content).ok())
            .expect("tool result");
        assert_eq!(tool_result["total_cost_usd"], 0.003);
    }

    #[tokio::test]
    async fn assistant_chat_should_attach_l1_confirmation_action_to_final_response() {
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
        .oneshot(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session")
                .body(Body::from(r#"{"message":"Please request L1"}"#))
                .expect("request"),
        )
        .await
        .expect("response");

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
    }

    #[tokio::test]
    async fn assistant_chat_cache_hit_should_precede_billing_and_relay() {
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
            .oneshot(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from(r#"{"message":"hello"}"#))
                    .expect("request"),
            )
            .await
            .expect("response");
        let cache = response.headers().get("x-lmm-assistant-cache").cloned();
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body");

        assert_eq!(body.as_ref(), cached_body);
        assert_eq!(cache, Some(axum::http::HeaderValue::from_static("HIT")));
        assert!(turns.lock().expect("agent turn lock").is_empty());
    }

    #[tokio::test]
    async fn assistant_chat_should_reject_disabled_and_personal_token_before_body() {
        let disabled = fixture_router(FixtureStore {
            settings: AssistantSettingsView {
                enabled: false,
                ..AssistantSettingsView::default()
            },
            ..FixtureStore::default()
        })
        .oneshot(
            Request::post("/api/assistant/chat")
                .header(header::AUTHORIZATION, "Bearer browser-session")
                .body(Body::from("not-json"))
                .expect("request"),
        )
        .await
        .expect("response");
        assert_eq!(disabled.status(), StatusCode::SERVICE_UNAVAILABLE);
        assert_eq!(response_json(disabled).await["code"], "ASSISTANT_DISABLED");

        let personal = fixture_router(FixtureStore::default())
            .oneshot(
                Request::post("/api/assistant/chat")
                    .header(header::AUTHORIZATION, "Bearer user-token")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");
        assert_eq!(personal.status(), StatusCode::FORBIDDEN);
        assert_eq!(
            response_json(personal).await["code"],
            "ASSISTANT_SESSION_REQUIRED"
        );
    }

    #[tokio::test]
    async fn assistant_chat_should_reject_unsafe_and_oversized_conversation() {
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
                .oneshot(
                    Request::post("/api/assistant/chat")
                        .header(header::AUTHORIZATION, "Bearer browser-session")
                        .body(Body::from(body))
                        .expect("request"),
                )
                .await
                .expect("response");
            assert_eq!(response.status(), expected_status);
            assert_eq!(response_json(response).await["code"], expected_code);
        }
    }

    #[test]
    fn assistant_messages_should_accept_nullable_tool_calls_like_go_json() {
        let message: AssistantOpenAiMessage = serde_json::from_value(json!({
            "role": "user",
            "content": "hello",
            "tool_calls": null,
        }))
        .expect("nullable tool calls");

        assert!(message.tool_calls.is_empty());
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
    async fn self_handoff_should_return_latest_user_lead_for_personal_token() {
        let lead = fixture_lead();
        let response = fixture_router(FixtureStore {
            latest: Some(lead.clone()),
            ..FixtureStore::default()
        })
        .oneshot(
            Request::get("/api/assistant/handoffs/self")
                .header(header::AUTHORIZATION, "user-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
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
    }

    #[tokio::test]
    async fn admin_handoffs_should_forward_resolved_filter_and_flatten_user_view() {
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
        .oneshot(
            Request::get("/api/assistant/admin/handoffs?status=resolved")
                .header(header::AUTHORIZATION, "Bearer admin-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        let body = response_json(response).await;

        assert_eq!(body["data"], json!([view]));
    }

    #[tokio::test]
    async fn admin_intents_should_reject_out_of_range_days_after_admin_auth() {
        let response = fixture_router(FixtureStore::default())
            .oneshot(
                Request::get("/api/assistant/admin/intents?days=366")
                    .header(header::AUTHORIZATION, "Bearer admin-token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
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
    }

    #[tokio::test]
    async fn create_key_should_conceal_l0_before_rate_limit_and_body_parsing() {
        let store = FixtureStore::default();
        let group_calls = Arc::clone(&store.key_group_calls);
        let create_calls = Arc::clone(&store.create_key_calls);
        let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Err(()),
            calls: Arc::clone(&rate_limit_calls),
        });
        let response = fixture_router_with_user_rate_limiter(store, limiter)
            .oneshot(
                Request::post("/api/assistant/tools/create-key")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let has_no_store = response.headers().contains_key(header::CACHE_CONTROL);
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::NOT_FOUND);
        assert_eq!(body, json!({"message": "Not Found"}));
        assert!(!has_no_store);
        assert!(
            rate_limit_calls
                .lock()
                .expect("rate limit call lock")
                .is_empty()
        );
        assert!(group_calls.lock().expect("key group call lock").is_empty());
        assert!(
            create_calls
                .lock()
                .expect("create key call lock")
                .is_empty()
        );
    }

    #[tokio::test]
    async fn create_key_should_return_sorted_group_choices_before_confirmation() {
        let options = fixture_key_groups();
        let store = FixtureStore {
            key_group_options_result: Some(Ok(options.clone())),
            ..FixtureStore::default()
        };
        let create_calls = Arc::clone(&store.create_key_calls);
        let response = fixture_router(store)
            .oneshot(
                Request::post("/api/assistant/tools/create-key")
                    .header(header::AUTHORIZATION, "Bearer admin-session")
                    .body(Body::from(r#"{"confirmed":true,"name":"my key"}"#))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], "ASSISTANT_KEY_GROUP_REQUIRED");
        assert_eq!(body["available_groups"], json!(options));
        assert!(cache_control.is_some());
        assert!(
            create_calls
                .lock()
                .expect("create key call lock")
                .is_empty()
        );
    }

    #[tokio::test]
    async fn create_key_should_rate_limit_validate_and_return_the_only_plaintext_key() {
        let created = AssistantCreatedKey {
            id: 42,
            name: "assistant-created".to_owned(),
            key: format!("sk-{}", "A".repeat(48)),
            group: "default".to_owned(),
            expired_time: -1,
        };
        let store = FixtureStore {
            key_group_options_result: Some(Ok(fixture_key_groups())),
            create_key_result: Some(Ok(created.clone())),
            ..FixtureStore::default()
        };
        let create_calls = Arc::clone(&store.create_key_calls);
        let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Allowed),
            calls: Arc::clone(&rate_limit_calls),
        });
        let response = fixture_router_with_user_rate_limiter(store, limiter)
            .oneshot(
                Request::post("/api/assistant/tools/create-key")
                    .header(header::AUTHORIZATION, "Bearer admin-session")
                    .body(Body::from(
                        r#"{"confirmed":true,"name":"  assistant-created  ","group":" default "}"#,
                    ))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["data"], json!(created));
        assert_eq!(
            *rate_limit_calls.lock().expect("rate limit call lock"),
            vec![("assistant-create-key".to_owned(), 10)]
        );
        assert_eq!(
            *create_calls.lock().expect("create key call lock"),
            vec![FixtureCreateKeyCall {
                user_id: 10,
                username: "assistant-admin".to_owned(),
                name: "assistant-created".to_owned(),
                group: "default".to_owned(),
            }]
        );
    }

    #[tokio::test]
    async fn create_key_should_consume_limit_before_rejecting_personal_token() {
        let store = FixtureStore::default();
        let group_calls = Arc::clone(&store.key_group_calls);
        let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Allowed),
            calls: Arc::clone(&rate_limit_calls),
        });
        let response = fixture_router_with_user_rate_limiter(store, limiter)
            .oneshot(
                Request::post("/api/assistant/tools/create-key")
                    .header(header::AUTHORIZATION, "Bearer admin-token")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::FORBIDDEN);
        assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED");
        assert_eq!(
            *rate_limit_calls.lock().expect("rate limit call lock"),
            vec![("assistant-create-key".to_owned(), 10)]
        );
        assert!(group_calls.lock().expect("key group call lock").is_empty());
    }

    #[tokio::test]
    async fn create_key_should_report_the_configured_token_limit() {
        let store = FixtureStore {
            key_group_options_result: Some(Ok(fixture_key_groups())),
            create_key_result: Some(Err(CreateAssistantKeyError::TokenLimit(12))),
            ..FixtureStore::default()
        };
        let response = fixture_router(store)
            .oneshot(
                Request::post("/api/assistant/tools/create-key")
                    .header(header::AUTHORIZATION, "Bearer admin-session")
                    .body(Body::from(
                        r#"{"confirmed":true,"name":"key","group":"default"}"#,
                    ))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::CONFLICT);
        assert_eq!(body["code"], "ASSISTANT_KEY_LIMIT_REACHED");
        assert_eq!(body["message"], "API key limit reached (12)");
    }

    #[tokio::test]
    async fn submit_handoff_should_rate_limit_redact_persist_and_disable_cache() {
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
            .oneshot(
                Request::post("/api/assistant/handoffs")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from(
                        json!({"confirmed": true, "message": raw_message}).to_string(),
                    ))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::OK);
        assert_eq!(body["data"], json!(lead));
        assert_eq!(
            *rate_limit_calls.lock().expect("rate limit call lock"),
            vec![("assistant-handoff".to_owned(), 7)]
        );
        assert_eq!(
            *submit_calls.lock().expect("submit call lock"),
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
    }

    #[tokio::test]
    async fn submit_handoff_should_consume_user_limit_before_rejecting_personal_token() {
        let store = FixtureStore::default();
        let submit_calls = Arc::clone(&store.submit_calls);
        let rate_limit_calls = Arc::new(Mutex::new(Vec::new()));
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Allowed),
            calls: Arc::clone(&rate_limit_calls),
        });
        let response = fixture_router_with_user_rate_limiter(store, limiter)
            .oneshot(
                Request::post("/api/assistant/handoffs")
                    .header(header::AUTHORIZATION, "Bearer user-token")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let cache_control = response.headers().get(header::CACHE_CONTROL).cloned();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::FORBIDDEN);
        assert_eq!(body["code"], "ASSISTANT_SESSION_REQUIRED");
        assert_eq!(
            *rate_limit_calls.lock().expect("rate limit call lock"),
            vec![("assistant-handoff".to_owned(), 7)]
        );
        assert!(submit_calls.lock().expect("submit call lock").is_empty());
        assert!(cache_control.is_some());
    }

    #[tokio::test]
    async fn submit_handoff_rate_limit_should_precede_session_body_and_no_store_middleware() {
        let store = FixtureStore::default();
        let submit_calls = Arc::clone(&store.submit_calls);
        let limiter = Arc::new(FixtureUserRateLimiter {
            outcome: Ok(CriticalRateLimitOutcome::Rejected {
                retry_after_seconds: 23,
            }),
            calls: Arc::new(Mutex::new(Vec::new())),
        });
        let response = fixture_router_with_user_rate_limiter(store, limiter)
            .oneshot(
                Request::post("/api/assistant/handoffs")
                    .header(header::AUTHORIZATION, "Bearer user-token")
                    .body(Body::from("not-json"))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
        assert_eq!(
            response.headers().get(header::RETRY_AFTER),
            Some(&axum::http::HeaderValue::from_static("23"))
        );
        assert!(!response.headers().contains_key(header::CACHE_CONTROL));
        let bytes = to_bytes(response.into_body(), usize::MAX)
            .await
            .expect("response body");
        assert!(bytes.is_empty());
        assert!(submit_calls.lock().expect("submit call lock").is_empty());
    }

    #[tokio::test]
    async fn submit_handoff_null_json_should_require_confirmation_like_go() {
        let response = fixture_router(FixtureStore::default())
            .oneshot(
                Request::post("/api/assistant/handoffs")
                    .header(header::AUTHORIZATION, "Bearer browser-session")
                    .body(Body::from("null"))
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::UNPROCESSABLE_ENTITY);
        assert_eq!(body["code"], "ASSISTANT_CONFIRMATION_REQUIRED");
    }

    #[tokio::test]
    async fn admin_intents_should_return_go_ordered_summary_shape() {
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
        .oneshot(
            Request::get("/api/assistant/admin/intents?days=7")
                .header(header::AUTHORIZATION, "Bearer admin-token")
                .body(Body::empty())
                .expect("request"),
        )
        .await
        .expect("response");
        let body = response_json(response).await;

        assert_eq!(body["data"], json!(summary));
    }

    #[tokio::test]
    async fn admin_handoffs_should_reject_common_user_without_auth_version() {
        let response = fixture_router(FixtureStore::default())
            .oneshot(
                Request::get("/api/assistant/admin/handoffs")
                    .header(header::AUTHORIZATION, "Bearer user-token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
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
    }

    #[tokio::test]
    async fn admin_resolve_handoff_should_match_go_transaction_and_audit_contract() {
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
            .oneshot(
                Request::post("/api/assistant/admin/handoffs/3/resolve")
                    .header(header::AUTHORIZATION, "Bearer admin-session")
                    .extension(ClientIpKey("203.0.113.9".to_owned()))
                    .body(Body::from(r#"{"note":"  contacted user  "}"#))
                    .expect("request"),
            )
            .await
            .expect("response");
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
            *resolve_calls.lock().expect("resolve call lock"),
            vec![FixtureResolveCall {
                admin_user_id: 10,
                admin_username: "assistant-admin".to_owned(),
                lead_id: 3,
                note: "contacted user".to_owned(),
            }]
        );
        assert_eq!(
            *audits.lock().expect("audit lock"),
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
    }

    #[tokio::test]
    async fn admin_resolve_handoff_should_accept_null_body_like_go_json_binding() {
        let mut resolved = fixture_lead();
        resolved.status = ASSISTANT_HANDOFF_RESOLVED.to_owned();
        let store = FixtureStore {
            resolve_result: Some(Ok(resolved)),
            ..FixtureStore::default()
        };
        let resolve_calls = Arc::clone(&store.resolve_calls);
        let response = fixture_router(store)
            .oneshot(
                Request::post("/api/assistant/admin/handoffs/3/resolve")
                    .header(header::AUTHORIZATION, "Bearer admin-token")
                    .body(Body::from("null"))
                    .expect("request"),
            )
            .await
            .expect("response");

        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(resolve_calls.lock().expect("resolve call lock")[0].note, "");
    }

    #[tokio::test]
    async fn admin_resolve_handoff_should_report_go_conflict_and_audit_failure() {
        let store = FixtureStore {
            resolve_result: Some(Err(ResolveHandoffError::AlreadyResolved)),
            ..FixtureStore::default()
        };
        let audits = Arc::clone(&store.audits);
        let response = fixture_router(store)
            .oneshot(
                Request::post("/api/assistant/admin/handoffs/3/resolve")
                    .header(header::AUTHORIZATION, "Bearer admin-token")
                    .body(Body::empty())
                    .expect("request"),
            )
            .await
            .expect("response");
        let status = response.status();
        let body = response_json(response).await;

        assert_eq!(status, StatusCode::CONFLICT);
        assert_eq!(body["code"], "ASSISTANT_HANDOFF_ALREADY_RESOLVED");
        let audit = audits.lock().expect("audit lock")[0].clone();
        assert_eq!(
            (audit.auth_method, audit.status, audit.success),
            ("access_token", StatusCode::CONFLICT, false)
        );
    }

    #[tokio::test]
    async fn admin_resolve_handoff_should_rate_limit_before_id_and_body_validation() {
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
                .oneshot(
                    Request::post("/api/assistant/admin/handoffs/not-an-id/resolve")
                        .header(header::AUTHORIZATION, "Bearer admin-token")
                        .extension(ClientIpKey("198.51.100.8".to_owned()))
                        .body(Body::from("not-json"))
                        .expect("request"),
                )
                .await
                .expect("response");

            assert_eq!(response.status(), expected_status);
            assert_eq!(
                response
                    .headers()
                    .get(header::RETRY_AFTER)
                    .and_then(|value| value.to_str().ok()),
                expected_retry_after
            );
            assert_eq!(
                response.headers().get("auth-version"),
                Some(&axum::http::HeaderValue::from_static(AUTH_VERSION))
            );
            let bytes = to_bytes(response.into_body(), usize::MAX)
                .await
                .expect("response body");
            assert!(bytes.is_empty());
            assert!(resolve_calls.lock().expect("resolve call lock").is_empty());
            let audit = audits.lock().expect("audit lock")[0].clone();
            assert_eq!((audit.status, audit.success), (expected_status, false));
        }
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
}
