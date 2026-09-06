//! PostgreSQL-backed Anthropic/Gemini relay adapter.
//!
//! The HTTP compatibility layer in `relay_anthropic_gemini` is intentionally
//! provider-neutral.  This adapter supplies the normal-listener authority for
//! the remaining `/v1/messages`, `/v1/engines/:model/embeddings`, and wildcard
//! Gemini routes: token and channel state come from PostgreSQL, while the
//! selected channel credential is injected only at the outbound boundary.
//!
//! Provider-specific channel adaptors can still rewrite the request upstream;
//! the adapter preserves the caller's JSON and response envelopes when a
//! channel is configured as a transparent Gemini/Anthropic endpoint.

use std::time::{Duration, SystemTime, UNIX_EPOCH};

use super::relay_anthropic_gemini::{
    NativeSseReply, RelayBackend, RelayChannel, RelayFailure, RelayIdentity, RelayOutcome,
    RelayProtocol, RelaySseEvent, UpstreamReply, UpstreamRequest,
};
use super::sse::{
    DEFAULT_MAX_FRAME_BYTES, JsonSseEvent, json_events_from_frames,
    parse_sse_frames_rejecting_unterminated,
};
use async_trait::async_trait;
use axum::{
    body::{Body, to_bytes},
    http::header,
};
use reqwest::Url;
use serde_json::Value;
use sqlx::{PgPool, Row};

const MAX_RESPONSE_BYTES: usize = 64 * 1024 * 1024;

/// PostgreSQL-backed authority and bounded transparent upstream client.
#[derive(Clone)]
pub struct PgAnthropicGeminiRelayBackend {
    pg: PgPool,
    client: reqwest::Client,
    response_header_timeout: Duration,
    sse_max_frame_bytes: usize,
}

impl PgAnthropicGeminiRelayBackend {
    /// Builds the adapter used by the normal Rust listener.
    #[must_use]
    pub fn new(pg: PgPool, client: reqwest::Client, response_header_timeout: Duration) -> Self {
        Self {
            pg,
            client,
            response_header_timeout,
            sse_max_frame_bytes: DEFAULT_MAX_FRAME_BYTES,
        }
    }

    /// Sets the independent maximum size for one upstream SSE frame.
    #[must_use]
    pub fn with_sse_max_frame_bytes(mut self, max_frame_bytes: usize) -> Self {
        self.sse_max_frame_bytes = max_frame_bytes;
        self
    }

    async fn channel_target(&self, channel_id: i64) -> Result<(String, String), RelayFailure> {
        let row = sqlx::query(
            "SELECT COALESCE(base_url,'') AS base_url, COALESCE(key,'') AS channel_key \
             FROM channels WHERE id=$1 AND COALESCE(status,1)=1",
        )
        .bind(channel_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| RelayFailure::Upstream)?
        .ok_or(RelayFailure::NoChannel)?;
        let base_url = row
            .try_get::<String, _>("base_url")
            .map_err(|_| RelayFailure::Upstream)?;
        let channel_key = row
            .try_get::<String, _>("channel_key")
            .map_err(|_| RelayFailure::Upstream)?
            .lines()
            .map(str::trim)
            .find(|value| !value.is_empty())
            .unwrap_or_default()
            .to_owned();
        if base_url.trim().is_empty() || channel_key.is_empty() {
            return Err(RelayFailure::NoChannel);
        }
        Ok((base_url, channel_key))
    }

    async fn token_context(
        &self,
        token: &str,
    ) -> Result<(i64, i64, String, String, String), RelayFailure> {
        let now = epoch_seconds();
        let row = sqlx::query(
            r#"SELECT t.id AS token_id, t.user_id,
                      COALESCE(t.status,1) AS token_status,
                      COALESCE(t.expired_time,-1) AS expired_time,
                      COALESCE(t.remain_quota,0) AS remain_quota,
                      COALESCE(t.unlimited_quota,FALSE) AS unlimited_quota,
                      COALESCE(t."group",'') AS token_group,
                      COALESCE(u."group",'default') AS user_group,
                      COALESCE(u.status,1) AS user_status
               FROM tokens t JOIN users u ON u.id=t.user_id
               WHERE t.key=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL"#,
        )
        .bind(token)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| RelayFailure::Upstream)?
        .ok_or(RelayFailure::ConcealedNotFound)?;
        let token_status = row.try_get::<i64, _>("token_status").unwrap_or_default();
        let user_status = row.try_get::<i64, _>("user_status").unwrap_or_default();
        let expired_time = row.try_get::<i64, _>("expired_time").unwrap_or_default();
        let unlimited = row.try_get::<bool, _>("unlimited_quota").unwrap_or(false);
        let remain_quota = row.try_get::<i64, _>("remain_quota").unwrap_or_default();
        if token_status != 1
            || user_status != 1
            || (expired_time != -1 && expired_time < now)
            || (!unlimited && remain_quota <= 0)
        {
            return Err(RelayFailure::Unauthorized);
        }
        Ok((
            row.try_get("token_id")
                .map_err(|_| RelayFailure::Upstream)?,
            row.try_get("user_id").map_err(|_| RelayFailure::Upstream)?,
            row.try_get("token_group").unwrap_or_default(),
            row.try_get("user_group")
                .unwrap_or_else(|_| "default".to_owned()),
            token.to_owned(),
        ))
    }

    async fn invoke_upstream(
        &self,
        channel: &RelayChannel,
        request: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        let (base_url, channel_key) = self.channel_target(channel.id).await?;
        let base = Url::parse(&base_url).map_err(|_| RelayFailure::Upstream)?;
        if !matches!(base.scheme(), "http" | "https") || base.host_str().is_none() {
            return Err(RelayFailure::Upstream);
        }
        let (request_path, raw_body) = prepare_upstream_request(&request, channel)?;
        let mut url = base
            .join(request_path.trim_start_matches('/'))
            .map_err(|_| RelayFailure::Upstream)?;
        if request.protocol == RelayProtocol::Gemini && request.streaming {
            // `alt=sse` is a request-side transport choice and is not retained
            // in the public request-path field by the legacy parser.
            let mut pairs = url
                .query_pairs()
                .filter(|(key, _)| key != "key")
                .map(|(key, value)| (key.into_owned(), value.into_owned()))
                .collect::<Vec<_>>();
            if !pairs.iter().any(|(key, _)| key == "alt") {
                pairs.push(("alt".to_owned(), "sse".to_owned()));
            }
            let query = form_urlencoded::Serializer::new(String::new())
                .extend_pairs(pairs)
                .finish();
            url.set_query((!query.is_empty()).then_some(query.as_str()));
        }

        let mut outbound = self
            .client
            .post(url)
            .header(header::CONTENT_TYPE, "application/json")
            .header(header::ACCEPT, upstream_accept_header(request.streaming));
        match request.protocol {
            RelayProtocol::Anthropic => {
                outbound = outbound
                    .header("x-api-key", &channel_key)
                    .header("anthropic-version", ANTHROPIC_VERSION);
            }
            RelayProtocol::Gemini => {
                outbound = outbound.header("x-goog-api-key", &channel_key);
            }
            RelayProtocol::OpenAi => {
                outbound = outbound.header(header::AUTHORIZATION, format!("Bearer {channel_key}"));
            }
        }
        let response =
            tokio::time::timeout(self.response_header_timeout, outbound.body(raw_body).send())
                .await
                .map_err(|_| RelayFailure::Upstream)?
                .map_err(|_| RelayFailure::Upstream)?;
        let status = response.status();
        let content_type_header = response.headers().get(header::CONTENT_TYPE).cloned();
        let content_type = content_type_header
            .as_ref()
            .and_then(|value| value.to_str().ok())
            .unwrap_or_default()
            .to_ascii_lowercase();
        let is_sse = content_type.contains("text/event-stream") || request.streaming;
        if !status.is_success() {
            let body = collect_bounded_body(self.response_header_timeout, response).await?;
            let value = serde_json::from_slice::<Value>(&body)
                .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&body).into_owned()));
            return Err(RelayFailure::Provider {
                status,
                body: value,
            });
        }

        // Anthropic and Gemini are native same-protocol routes here. Keep
        // their successful SSE response as a single-consumption body so new
        // provider fields/events remain byte-for-byte visible and downstream
        // backpressure controls upstream polling. OpenAI is intentionally not
        // included: it still requires the typed cross-protocol conversion
        // below rather than receiving a native body by assumption.
        if is_sse
            && matches!(
                request.protocol,
                RelayProtocol::Anthropic | RelayProtocol::Gemini
            )
        {
            return Ok(UpstreamReply::NativeSse(Box::new(NativeSseReply::new(
                status,
                Body::from_stream(response.bytes_stream()),
                content_type_header,
            ))));
        }

        let body = collect_bounded_body(self.response_header_timeout, response).await?;
        let value = serde_json::from_slice::<Value>(&body)
            .unwrap_or_else(|_| Value::String(String::from_utf8_lossy(&body).into_owned()));
        if is_sse {
            return Ok(UpstreamReply::Sse(parse_sse_events(
                &body,
                self.sse_max_frame_bytes,
            )?));
        }
        Ok(UpstreamReply::Json(value))
    }
}

async fn collect_bounded_body(
    timeout: Duration,
    response: reqwest::Response,
) -> Result<axum::body::Bytes, RelayFailure> {
    tokio::time::timeout(
        timeout,
        to_bytes(
            Body::from_stream(response.bytes_stream()),
            MAX_RESPONSE_BYTES,
        ),
    )
    .await
    .map_err(|_| RelayFailure::Upstream)?
    .map_err(|_| RelayFailure::Upstream)
}

/// Reproduces the current Go provider-boundary normalization for the two
/// protocol families.  The public paths are compatibility aliases; Gemini
/// channels always receive the native `v1beta/models/{model}:action` path and
/// its typed request envelope, while Anthropic receives `/v1/messages`.
fn prepare_upstream_request(
    request: &UpstreamRequest,
    channel: &RelayChannel,
) -> Result<(String, Vec<u8>), RelayFailure> {
    let mut body = request.body.clone();
    let path = match request.protocol {
        RelayProtocol::Anthropic => {
            if let Some(object) = body.as_object_mut()
                && let Some(model) = object.get_mut("model")
            {
                *model = Value::String(channel.upstream_model.clone());
            }
            "/v1/messages".to_owned()
        }
        RelayProtocol::Gemini => {
            if request.request_path.ends_with("/embeddings") {
                // The current Go alias parses this legacy `/v1/engines` form
                // through its Gemini embedding response path.  Its DTO emits
                // a native model name and an empty `content.parts` when the
                // caller supplied the chat-style `contents` envelope.
                let content = body
                    .get("content")
                    .cloned()
                    .unwrap_or_else(|| serde_json::json!({"parts": Value::Null}));
                body = serde_json::json!({
                    "model": format!("models/{}", channel.upstream_model),
                    "content": content,
                });
            } else if let Some(object) = body.as_object_mut() {
                // `GeminiChatRequest` marshals an empty generationConfig even
                // when the client omitted it.  Preserve that wire detail.
                if !object.contains_key("generationConfig") {
                    object.insert(
                        "generationConfig".to_owned(),
                        Value::Object(Default::default()),
                    );
                }
            }
            gemini_upstream_path(&request.request_path, &channel.upstream_model)
        }
        RelayProtocol::OpenAi => request.request_path.clone(),
    };
    let raw_body = serde_json::to_vec(&body).map_err(|_| RelayFailure::Upstream)?;
    Ok((path, raw_body))
}

fn gemini_upstream_path(request_path: &str, upstream_model: &str) -> String {
    let action = if request_path.contains("streamGenerateContent") {
        "streamGenerateContent?alt=sse"
    } else if request_path.contains("embedContent")
        || request_path.ends_with("/embeddings")
            && (upstream_model.starts_with("text-embedding")
                || upstream_model.starts_with("embedding")
                || upstream_model.starts_with("gemini-embedding"))
    {
        "embedContent"
    } else if request_path.contains(':') {
        request_path
            .rsplit_once(':')
            .map_or("generateContent", |(_, action)| action)
    } else {
        "generateContent"
    };
    format!("/v1beta/models/{upstream_model}:{action}")
}

#[async_trait]
impl RelayBackend for PgAnthropicGeminiRelayBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        let (token_id, _, _, _, _) = self.token_context(normalize_token(token)).await?;
        Ok(RelayIdentity {
            token_id: token_id.to_string(),
        })
    }

    async fn select_channel(
        &self,
        identity: &RelayIdentity,
        protocol: RelayProtocol,
        model: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        let token_id = identity
            .token_id
            .parse::<i64>()
            .map_err(|_| RelayFailure::Unauthorized)?;
        let row = sqlx::query(
            r#"SELECT c.id, COALESCE(c.model_mapping,'') AS model_mapping
               FROM tokens t
               JOIN users u ON u.id=t.user_id
               JOIN abilities a ON a."group"=COALESCE(NULLIF(t."group",''),u."group")
                                AND a.model=$2 AND COALESCE(a.enabled,TRUE)
               JOIN channels c ON c.id=a.channel_id AND COALESCE(c.status,1)=1
                                AND CASE WHEN $3 = 14
                                         THEN c.type IN (14,25,33,41,53,59,60)
                                         ELSE c.type IN (1,11,24,41,53,59,60)
                                    END
               WHERE t.id=$1 AND t.deleted_at IS NULL AND u.deleted_at IS NULL
               ORDER BY COALESCE(a.priority,0) DESC, COALESCE(a.weight,0) DESC, c.id ASC
               LIMIT 1"#,
        )
        .bind(token_id)
        .bind(model)
        .bind(match protocol {
            RelayProtocol::Anthropic => 14_i64,
            RelayProtocol::Gemini | RelayProtocol::OpenAi => 24_i64,
        })
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| RelayFailure::Upstream)?
        .ok_or(RelayFailure::NoChannel)?;
        let channel_id = row
            .try_get::<i64, _>("id")
            .map_err(|_| RelayFailure::Upstream)?;
        let mapping = row
            .try_get::<String, _>("model_mapping")
            .unwrap_or_default();
        let upstream_model = mapped_model(model, &mapping)?;
        Ok(RelayChannel {
            id: channel_id,
            upstream_model,
        })
    }

    async fn invoke(
        &self,
        channel: &RelayChannel,
        request: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        self.invoke_upstream(channel, request).await
    }

    async fn record_outcome(
        &self,
        _identity: Option<&RelayIdentity>,
        _channel: Option<&RelayChannel>,
        _outcome: RelayOutcome,
    ) {
        // The typed compatibility route records the outcome after the
        // provider response.  Usage/log settlement is intentionally kept in
        // the channel-specific relay adapters until their ratio calculator is
        // wired; this adapter never grants a provider response from memory.
    }
}

fn mapped_model(origin: &str, raw_mapping: &str) -> Result<String, RelayFailure> {
    if raw_mapping.trim().is_empty() {
        return Ok(origin.to_owned());
    }
    let mapping = serde_json::from_str::<std::collections::HashMap<String, String>>(raw_mapping)
        .map_err(|_| RelayFailure::NoChannel)?;
    let mut current = origin.to_owned();
    let mut seen = std::collections::HashSet::from([current.clone()]);
    while let Some(next) = mapping.get(&current) {
        if !seen.insert(next.clone()) {
            return Err(RelayFailure::NoChannel);
        }
        current = next.clone();
    }
    Ok(current)
}

fn normalize_token(raw: &str) -> &str {
    let raw = raw
        .strip_prefix("Bearer ")
        .or_else(|| raw.strip_prefix("bearer "))
        .unwrap_or(raw)
        .trim();
    raw.strip_prefix("sk-").unwrap_or(raw)
}

fn parse_sse_events(
    body: &[u8],
    max_frame_bytes: usize,
) -> Result<Vec<RelaySseEvent>, RelayFailure> {
    let frames = parse_sse_frames_rejecting_unterminated(body, max_frame_bytes)
        .map_err(RelayFailure::Sse)?;
    let events = json_events_from_frames(&frames).map_err(RelayFailure::Sse)?;
    Ok(events.into_iter().map(relay_sse_event).collect())
}

fn relay_sse_event(event: JsonSseEvent) -> RelaySseEvent {
    RelaySseEvent {
        kind: event.event,
        payload: event.payload,
    }
}

fn epoch_seconds() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

const ANTHROPIC_VERSION: &str = "2023-06-01";

fn upstream_accept_header(streaming: bool) -> &'static str {
    if streaming {
        "text/event-stream"
    } else {
        "application/json"
    }
}

#[cfg(test)]
mod tests {
    use super::{ANTHROPIC_VERSION, RelayFailure, parse_sse_events, upstream_accept_header};
    use crate::routes::sse::SseError;
    use serde_json::json;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    #[test]
    fn streaming_upstream_requests_advertise_sse_accept() {
        assert_eq!(upstream_accept_header(true), "text/event-stream");
        assert_eq!(upstream_accept_header(false), "application/json");
    }

    #[test]
    fn anthropic_upstream_requests_use_the_messages_api_version() {
        assert_eq!(ANTHROPIC_VERSION, "2023-06-01");
    }

    #[test]
    fn postgres_parser_keeps_unknown_json_event_names_and_multiline_data() -> TestResult {
        let events = parse_sse_events(
            b"event: future_event\r\ndata: {\r\ndata: \"value\": 1}\r\n\r\ndata: [DONE]\r\n\r\n",
            1024,
        )
        .map_err(|error| std::io::Error::other(format!("{error:?}")))?;
        assert_eq!(events.len(), 1);
        assert_eq!(events[0].kind.as_deref(), Some("future_event"));
        assert_eq!(events[0].payload, json!({"value": 1}));
        Ok(())
    }

    #[test]
    fn postgres_parser_returns_typed_error_for_unrepresentable_metadata() {
        assert_eq!(
            parse_sse_events(b"id: upstream-id\ndata: {}\n\n", 1024).err(),
            Some(RelayFailure::Sse(SseError::UnsupportedMetadata {
                frame: 0,
                field: "id",
            }))
        );
    }

    #[test]
    fn postgres_parser_returns_typed_error_for_non_json_data() {
        assert_eq!(
            parse_sse_events(b"data: plain text\n\n", 1024).err(),
            Some(RelayFailure::Sse(SseError::InvalidJson { frame: 0 }))
        );
    }

    #[test]
    fn postgres_parser_rejects_an_unterminated_frame_instead_of_dropping_it() {
        assert_eq!(
            parse_sse_events(b"data: {}\n", 1024).err(),
            Some(RelayFailure::Sse(SseError::UnterminatedFrame))
        );
    }

    #[test]
    fn postgres_parser_enforces_independent_frame_limit() {
        assert!(matches!(
            parse_sse_events(b"data: 123456789\n\n", 5),
            Err(RelayFailure::Sse(SseError::FrameTooLarge { limit: 5, .. }))
        ));
    }
}
