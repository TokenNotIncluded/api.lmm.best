//! Provider relay deadlines, independent of control-plane dependencies.

use std::{fmt, time::Duration};

use axum::{
    body::{Body, Bytes},
    http::{HeaderMap, HeaderName, HeaderValue, Method, StatusCode, header},
};
use futures_util::{
    Stream, StreamExt,
    stream::{self, BoxStream},
};
use thiserror::Error;

const OPENAI_FIRST_OUTPUT_TIMEOUT_ENV: &str = "OPENAI_FIRST_OUTPUT_TIMEOUT";
const OPENAI_FIRST_OUTPUT_MIN_SECONDS: u64 = 30;
const OPENAI_FIRST_OUTPUT_MAX_SECONDS: u64 = 600;
const OPENAI_FIRST_OUTPUT_BUFFER_LIMIT: usize = 1024 * 1024;

/// HTTP policy owned by the provider attempt, never by internal dependencies.
#[derive(Clone)]
pub struct RelayHttpClient {
    client: reqwest::Client,
    config: RelayTimeoutConfig,
    openai_first_output_timeout: Option<Duration>,
}

/// Provider request construction without access to a raw client or response.
#[must_use]
pub struct RelayRequestBuilder(reqwest::RequestBuilder);

impl RelayRequestBuilder {
    pub fn header<K, V>(self, key: K, value: V) -> Self
    where
        HeaderName: TryFrom<K>,
        <HeaderName as TryFrom<K>>::Error: Into<axum::http::Error>,
        HeaderValue: TryFrom<V>,
        <HeaderValue as TryFrom<V>>::Error: Into<axum::http::Error>,
    {
        Self(self.0.header(key, value))
    }

    pub fn body(self, body: impl Into<reqwest::Body>) -> Self {
        Self(self.0.body(body))
    }

    pub fn json<T: serde::Serialize + ?Sized>(self, body: &T) -> Self {
        Self(self.0.json(body))
    }
}

#[derive(Error)]
pub enum RelayHttpError {
    #[error("upstream response timed out")]
    ResponseHeaders,
    #[error("upstream first visible output timed out")]
    FirstOutput,
    #[error("upstream pre-output buffer exceeded the safety limit")]
    FirstOutputBufferLimit,
    #[error("upstream response read timed out")]
    Idle,
    #[error("upstream request failed")]
    Transport(#[source] reqwest::Error),
}

impl fmt::Debug for RelayHttpError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::ResponseHeaders => formatter.write_str("ResponseHeaders"),
            Self::FirstOutput => formatter.write_str("FirstOutput"),
            Self::FirstOutputBufferLimit => formatter.write_str("FirstOutputBufferLimit"),
            Self::Idle => formatter.write_str("Idle"),
            Self::Transport(_) => formatter.write_str("Transport(<redacted>)"),
        }
    }
}

impl RelayHttpClient {
    pub fn new(
        config: RelayTimeoutConfig,
    ) -> Result<Self, crate::outbound_http::OutboundHttpError> {
        use crate::outbound_http::OutboundHttpError;
        if config.idle.is_zero()
            || config.response_headers.is_some_and(|value| value.is_zero())
            || config.total.is_some_and(|value| value.is_zero())
        {
            return Err(OutboundHttpError::ZeroTimeout);
        }
        let mut builder = reqwest::Client::builder()
            .use_rustls_tls()
            .connect_timeout(Duration::from_secs(3))
            .redirect(reqwest::redirect::Policy::none());
        if let Some(total) = config.total {
            builder = builder.timeout(total);
        }
        Ok(Self {
            client: builder.build().map_err(OutboundHttpError::Build)?,
            config,
            openai_first_output_timeout: openai_first_output_timeout_from_lookup(|name| {
                std::env::var(name).ok()
            }),
        })
    }

    /// Overrides the OpenAI first-visible-output guard for an explicitly
    /// constructed client. The process-level environment parser remains
    /// Go-compatible (disabled unless configured to 30-600 seconds), while
    /// tests can inject short deterministic durations without mutating global
    /// environment state.
    #[must_use]
    pub fn with_openai_first_output_timeout(mut self, timeout: Option<Duration>) -> Self {
        self.openai_first_output_timeout = timeout.filter(|value| !value.is_zero());
        self
    }

    pub fn request(&self, method: Method, url: reqwest::Url) -> RelayRequestBuilder {
        RelayRequestBuilder(self.client.request(method, url))
    }

    /// Requests cannot send directly and bypass controlled response bodies.
    ///
    /// ```compile_fail,E0599
    /// use lmm_api_rs::relay_http::RelayHttpClient;
    /// let client = RelayHttpClient::new(Default::default()).unwrap();
    /// let request = client.post("http://example.invalid/".parse().unwrap());
    /// let _ = request.send();
    /// ```
    pub fn post(&self, url: reqwest::Url) -> RelayRequestBuilder {
        self.request(Method::POST, url)
    }

    pub async fn send(
        &self,
        request: RelayRequestBuilder,
    ) -> Result<RelayResponse, RelayHttpError> {
        let mut request = request.0.build().map_err(RelayHttpError::Transport)?;
        let openai_stream_candidate = is_openai_stream_candidate(&request);
        // The sending client owns the policy even for another relay client's builder.
        *request.timeout_mut() = self.config.total;
        let response = match self.config.response_headers {
            Some(deadline) => tokio::time::timeout(deadline, self.client.execute(request))
                .await
                .map_err(|_| RelayHttpError::ResponseHeaders)?,
            None => self.client.execute(request).await,
        }
        .map_err(RelayHttpError::Transport)?;
        let status = response.status();
        let headers = response.headers().clone();
        let mut body = idle_stream(response.bytes_stream(), self.config.idle);
        if status.is_success()
            && openai_stream_candidate
            && is_event_stream(&headers)
            && let Some(deadline) = self.openai_first_output_timeout
        {
            body = wait_for_openai_first_visible_output(body, deadline).await?;
        }
        Ok(RelayResponse {
            status,
            headers,
            body,
        })
    }
}

/// A response whose raw reqwest body cannot bypass idle protection.
pub struct RelayResponse {
    status: StatusCode,
    headers: HeaderMap,
    body: BoxStream<'static, Result<Bytes, RelayHttpError>>,
}

impl RelayResponse {
    pub fn status(&self) -> StatusCode {
        self.status
    }
    pub fn headers(&self) -> &HeaderMap {
        &self.headers
    }
    pub fn into_body(self) -> Body {
        Body::from_stream(self.body)
    }
    pub async fn chunk(&mut self) -> Result<Option<Bytes>, RelayHttpError> {
        self.body.next().await.transpose()
    }
}

fn idle_stream<S>(source: S, idle: Duration) -> BoxStream<'static, Result<Bytes, RelayHttpError>>
where
    S: Stream<Item = Result<Bytes, reqwest::Error>> + Send + 'static,
{
    stream::unfold(Some(Box::pin(source)), move |source| async move {
        let mut source = source?;
        // One timer survives every pending poll and empty frame of this read.
        let read = tokio::time::timeout(idle, async {
            loop {
                match source.next().await {
                    Some(Ok(bytes)) if bytes.is_empty() => tokio::task::yield_now().await,
                    item => return item,
                }
            }
        })
        .await;
        match read {
            Ok(Some(Ok(bytes))) => Some((Ok(bytes), Some(source))),
            Ok(Some(Err(error))) => Some((Err(RelayHttpError::Transport(error)), None)),
            Err(_) => Some((Err(RelayHttpError::Idle), None)),
            Ok(None) => None,
        }
    })
    .boxed()
}

fn is_openai_stream_candidate(request: &reqwest::Request) -> bool {
    request.url().path().ends_with("/v1/chat/completions")
}

fn is_event_stream(headers: &HeaderMap) -> bool {
    headers
        .get(header::CONTENT_TYPE)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.split(';').next())
        .is_some_and(|value| value.trim().eq_ignore_ascii_case("text/event-stream"))
}

async fn wait_for_openai_first_visible_output(
    mut body: BoxStream<'static, Result<Bytes, RelayHttpError>>,
    deadline: Duration,
) -> Result<BoxStream<'static, Result<Bytes, RelayHttpError>>, RelayHttpError> {
    let observed = tokio::time::timeout(deadline, async {
        let mut buffered = Vec::new();
        let mut scan_offset = 0usize;
        while let Some(item) = body.next().await {
            let bytes = item?;
            if bytes.len() > OPENAI_FIRST_OUTPUT_BUFFER_LIMIT.saturating_sub(buffered.len()) {
                return Err(RelayHttpError::FirstOutputBufferLimit);
            }
            buffered.extend_from_slice(&bytes);
            if scan_new_sse_events_for_visible_output(&buffered, &mut scan_offset) {
                let replay = stream::once(async move { Ok(Bytes::from(buffered)) });
                return Ok(replay.chain(body).boxed());
            }
        }
        let replay = stream::once(async move { Ok(Bytes::from(buffered)) });
        Ok(replay.chain(body).boxed())
    })
    .await;
    match observed {
        Ok(result) => result,
        Err(_) => Err(RelayHttpError::FirstOutput),
    }
}

fn scan_new_sse_events_for_visible_output(bytes: &[u8], offset: &mut usize) -> bool {
    while let Some((event_end, delimiter_len)) = next_sse_event_boundary(&bytes[*offset..]) {
        let event_start = *offset;
        let event_end = event_start + event_end;
        *offset = event_end + delimiter_len;
        if sse_event_has_visible_openai_output(&bytes[event_start..event_end]) {
            return true;
        }
    }
    false
}

fn next_sse_event_boundary(bytes: &[u8]) -> Option<(usize, usize)> {
    let lf = bytes
        .windows(2)
        .position(|window| window == b"\n\n")
        .map(|index| (index, 2));
    let crlf = bytes
        .windows(4)
        .position(|window| window == b"\r\n\r\n")
        .map(|index| (index, 4));
    match (lf, crlf) {
        (Some(left), Some(right)) => Some(if left.0 <= right.0 { left } else { right }),
        (Some(boundary), None) | (None, Some(boundary)) => Some(boundary),
        (None, None) => None,
    }
}

fn sse_event_has_visible_openai_output(event: &[u8]) -> bool {
    for line in event.split(|byte| *byte == b'\n') {
        let line = line.strip_suffix(b"\r").unwrap_or(line);
        let Some(data) = line.strip_prefix(b"data:") else {
            continue;
        };
        let data = data.strip_prefix(b" ").unwrap_or(data);
        if data == b"[DONE]" || data.is_empty() {
            continue;
        }
        let Ok(value) = serde_json::from_slice::<serde_json::Value>(data) else {
            continue;
        };
        let Some(choices) = value.get("choices").and_then(serde_json::Value::as_array) else {
            continue;
        };
        for choice in choices {
            let Some(delta) = choice.get("delta") else {
                continue;
            };
            if ["content", "reasoning_content", "reasoning"]
                .into_iter()
                .any(|field| {
                    delta
                        .get(field)
                        .and_then(serde_json::Value::as_str)
                        .is_some_and(|value| !value.is_empty())
                })
                || delta
                    .get("tool_calls")
                    .and_then(serde_json::Value::as_array)
                    .is_some_and(|calls| !calls.is_empty())
                || delta.get("function_call").is_some_and(|function| {
                    ["name", "arguments"].into_iter().any(|field| {
                        function
                            .get(field)
                            .and_then(serde_json::Value::as_str)
                            .is_some_and(|value| !value.is_empty())
                    })
                })
            {
                return true;
            }
        }
    }
    false
}

fn openai_first_output_timeout_from_lookup(
    mut lookup: impl FnMut(&'static str) -> Option<String>,
) -> Option<Duration> {
    let raw = lookup(OPENAI_FIRST_OUTPUT_TIMEOUT_ENV)?;
    if raw.is_empty() || !raw.bytes().all(|byte| byte.is_ascii_digit()) {
        return None;
    }
    let seconds = raw.parse::<u64>().ok()?;
    if seconds == 0 {
        return None;
    }
    (OPENAI_FIRST_OUTPUT_MIN_SECONDS..=OPENAI_FIRST_OUTPUT_MAX_SECONDS)
        .contains(&seconds)
        .then_some(Duration::from_secs(seconds))
}

/// Startup-owned deadlines for one provider attempt.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct RelayTimeoutConfig {
    pub response_headers: Option<Duration>,
    pub idle: Duration,
    pub total: Option<Duration>,
}

impl Default for RelayTimeoutConfig {
    fn default() -> Self {
        Self {
            response_headers: Some(Duration::from_secs(1800)),
            idle: Duration::from_secs(300),
            total: None,
        }
    }
}

#[derive(Debug, Error, Eq, PartialEq)]
#[error("environment variable {0} is invalid")]
pub struct RelayTimeoutConfigError(pub &'static str);

impl RelayTimeoutConfig {
    /// Lookup is injected so tests never mutate process-wide environment.
    pub fn from_lookup(
        mut lookup: impl FnMut(&'static str) -> Result<Option<String>, RelayTimeoutConfigError>,
    ) -> Result<Self, RelayTimeoutConfigError> {
        fn read(
            lookup: &mut impl FnMut(&'static str) -> Result<Option<String>, RelayTimeoutConfigError>,
            native: &'static str,
            legacy: &'static str,
            default: u64,
            allow_zero: bool,
        ) -> Result<Duration, RelayTimeoutConfigError> {
            let selected = match lookup(native)? {
                Some(value) => Some((native, value)),
                None => lookup(legacy)?.map(|value| (legacy, value)),
            };
            let (name, seconds) = match selected {
                None => (native, default),
                Some((name, value)) => {
                    if value.is_empty() || !value.bytes().all(|byte| byte.is_ascii_digit()) {
                        return Err(RelayTimeoutConfigError(name));
                    }
                    (
                        name,
                        value
                            .parse::<u64>()
                            .map_err(|_| RelayTimeoutConfigError(name))?,
                    )
                }
            };
            // Keep conversion portable and well within platform timer limits.
            if (!allow_zero && seconds == 0) || seconds > i64::MAX as u64 / 1_000_000_000 {
                return Err(RelayTimeoutConfigError(name));
            }
            let duration = Duration::from_secs(seconds);
            if std::time::Instant::now().checked_add(duration).is_none() {
                return Err(RelayTimeoutConfigError(name));
            }
            Ok(duration)
        }
        let response_headers = read(
            &mut lookup,
            "LMM_RELAY_RESPONSE_HEADER_TIMEOUT_SECONDS",
            "RELAY_RESPONSE_HEADER_TIMEOUT",
            1800,
            true,
        )?;
        let idle = read(
            &mut lookup,
            "LMM_RELAY_IDLE_TIMEOUT_SECONDS",
            "STREAMING_TIMEOUT",
            300,
            false,
        )?;
        let total = read(
            &mut lookup,
            "LMM_RELAY_TIMEOUT_SECONDS",
            "RELAY_TIMEOUT",
            0,
            true,
        )?;
        Ok(Self {
            response_headers: (!response_headers.is_zero()).then_some(response_headers),
            idle,
            total: (!total.is_zero()).then_some(total),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[tokio::test(start_paused = true)]
    async fn empty_frames_do_not_extend_idle_deadline() {
        let source = stream::unfold((), |()| async {
            tokio::time::sleep(Duration::from_secs(1)).await;
            Some((Ok(Bytes::new()), ()))
        });
        let mut body = idle_stream(source, Duration::from_secs(3));
        let start = tokio::time::Instant::now();
        assert!(matches!(body.next().await, Some(Err(RelayHttpError::Idle))));
        assert_eq!(start.elapsed(), Duration::from_secs(3));
        assert!(body.next().await.is_none());
    }

    #[tokio::test(start_paused = true)]
    async fn byte_fragments_reset_idle_without_requiring_a_line() {
        let source = stream::unfold(0, |index| async move {
            if index == 5 {
                return None;
            }
            tokio::time::sleep(Duration::from_secs(2)).await;
            Some((Ok(Bytes::from_static(b":")), index + 1))
        });
        let mut body = idle_stream(source, Duration::from_secs(3));
        let start = tokio::time::Instant::now();
        for _ in 0..5 {
            assert_eq!(
                body.next().await.unwrap().unwrap(),
                Bytes::from_static(b":")
            );
        }
        assert!(body.next().await.is_none());
        assert_eq!(start.elapsed(), Duration::from_secs(10));
    }

    #[tokio::test(start_paused = true)]
    async fn idle_budget_is_for_reads_not_consumer_pauses() {
        let source = stream::iter([Ok(Bytes::from_static(b"a")), Ok(Bytes::from_static(b"b"))]);
        let mut body = idle_stream(source, Duration::from_secs(3));
        assert_eq!(
            body.next().await.unwrap().unwrap(),
            Bytes::from_static(b"a")
        );
        tokio::time::sleep(Duration::from_secs(30)).await;
        assert_eq!(
            body.next().await.unwrap().unwrap(),
            Bytes::from_static(b"b")
        );
        assert!(body.next().await.is_none());
    }

    #[test]
    fn first_output_env_matches_go_bounds_and_disable_semantics() {
        for value in [
            None,
            Some("0"),
            Some("29"),
            Some("601"),
            Some("-1"),
            Some("bad"),
        ] {
            assert_eq!(
                openai_first_output_timeout_from_lookup(|_| value.map(str::to_owned)),
                None
            );
        }
        assert_eq!(
            openai_first_output_timeout_from_lookup(|_| Some("30".to_owned())),
            Some(Duration::from_secs(30))
        );
        assert_eq!(
            openai_first_output_timeout_from_lookup(|_| Some("600".to_owned())),
            Some(Duration::from_secs(600))
        );
    }

    #[test]
    fn visibility_matches_go_openai_delta_semantics() {
        assert!(!sse_event_has_visible_openai_output(
            br#"data: {"choices":[{"delta":{"role":"assistant"}}]}"#
        ));
        assert!(!sse_event_has_visible_openai_output(
            br#"data: {"usage":{"prompt_tokens":1},"choices":[]}"#
        ));
        for event in [
            br#"data: {"choices":[{"delta":{"content":"x"}}]}"#.as_slice(),
            br#"data: {"choices":[{"delta":{"reasoning_content":"x"}}]}"#.as_slice(),
            br#"data: {"choices":[{"delta":{"reasoning":"x"}}]}"#.as_slice(),
            br#"data: {"choices":[{"delta":{"tool_calls":[{}]}}]}"#.as_slice(),
            br#"data: {"choices":[{"delta":{"function_call":{"arguments":"{}"}}}]}"#.as_slice(),
        ] {
            assert!(sse_event_has_visible_openai_output(event));
        }
    }

    #[test]
    fn visibility_scanner_handles_split_sse_frames() {
        let mut buffered =
            b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n".to_vec();
        let mut offset = 0;
        assert!(!scan_new_sse_events_for_visible_output(
            &buffered,
            &mut offset
        ));
        buffered.extend_from_slice(b"data: {\"choices\":[{\"delta\":{\"content\":\"he");
        assert!(!scan_new_sse_events_for_visible_output(
            &buffered,
            &mut offset
        ));
        buffered.extend_from_slice(b"llo\"}}]}\n\n");
        assert!(scan_new_sse_events_for_visible_output(
            &buffered,
            &mut offset
        ));
    }

    #[tokio::test(start_paused = true)]
    async fn first_output_guard_times_out_role_only_stream() {
        let role_only =
            Bytes::from_static(b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n");
        let body = stream::iter([Ok(role_only)])
            .chain(stream::pending())
            .boxed();
        let result = wait_for_openai_first_visible_output(body, Duration::from_secs(30)).await;
        assert!(matches!(result, Err(RelayHttpError::FirstOutput)));
    }

    #[tokio::test(start_paused = true)]
    async fn first_output_guard_replays_exact_prefix_and_retires_after_visible_content() {
        let role_only =
            Bytes::from_static(b"data: {\"choices\":[{\"delta\":{\"role\":\"assistant\"}}]}\n\n");
        let visible =
            Bytes::from_static(b"data: {\"choices\":[{\"delta\":{\"content\":\"hello\"}}]}\n\n");
        let done = Bytes::from_static(b"data: [DONE]\n\n");
        let expected = [role_only.as_ref(), visible.as_ref(), done.as_ref()].concat();
        let body = stream::iter([Ok(role_only), Ok(visible), Ok(done)]).boxed();
        let mut guarded = wait_for_openai_first_visible_output(body, Duration::from_secs(30))
            .await
            .expect("visible output retires the first-output deadline");
        tokio::time::sleep(Duration::from_secs(60)).await;
        let mut actual = Vec::new();
        while let Some(item) = guarded.next().await {
            actual.extend_from_slice(&item.expect("replayed stream chunk"));
        }
        assert_eq!(actual, expected);
    }

    #[tokio::test]
    async fn first_output_guard_fails_closed_before_unbounded_buffering() {
        let oversized = Bytes::from(vec![b'x'; OPENAI_FIRST_OUTPUT_BUFFER_LIMIT + 1]);
        let body = stream::iter([Ok(oversized)]).boxed();
        let result = wait_for_openai_first_visible_output(body, Duration::from_secs(30)).await;
        assert!(matches!(
            result,
            Err(RelayHttpError::FirstOutputBufferLimit)
        ));
    }

    #[tokio::test]
    async fn transport_debug_redacts_provider_endpoint_details() {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let address = listener.local_addr().unwrap();
        drop(listener);
        let secret_path = "private-upstream-token";
        let url: reqwest::Url = format!("http://{address}/{secret_path}").parse().unwrap();
        let client = RelayHttpClient::new(RelayTimeoutConfig {
            response_headers: Some(Duration::from_secs(1)),
            idle: Duration::from_secs(1),
            total: Some(Duration::from_secs(1)),
        })
        .unwrap();

        match client.send(client.request(Method::GET, url)).await {
            Err(error @ RelayHttpError::Transport(_)) => {
                let debug = format!("{error:?}");
                assert_eq!(debug, "Transport(<redacted>)");
                assert!(!debug.contains(&address.to_string()));
                assert!(!debug.contains(secret_path));
                assert!(std::error::Error::source(&error).is_some());
            }
            Err(error) => panic!("expected transport error, got {error:?}"),
            Ok(_) => panic!("expected transport error"),
        }
    }

    fn config(values: &[(&str, &str)]) -> Result<RelayTimeoutConfig, RelayTimeoutConfigError> {
        RelayTimeoutConfig::from_lookup(|name| {
            Ok(values
                .iter()
                .find(|(key, _)| *key == name)
                .map(|(_, value)| (*value).to_owned()))
        })
    }

    #[test]
    fn default_deadlines_match_relay_defaults() {
        assert_eq!(config(&[]).unwrap(), RelayTimeoutConfig::default());
    }

    #[test]
    fn native_values_override_legacy_including_disabled_deadlines() {
        let parsed = config(&[
            ("RELAY_RESPONSE_HEADER_TIMEOUT", "20"),
            ("LMM_RELAY_RESPONSE_HEADER_TIMEOUT_SECONDS", "0"),
            ("STREAMING_TIMEOUT", "30"),
            ("LMM_RELAY_IDLE_TIMEOUT_SECONDS", "40"),
            ("RELAY_TIMEOUT", "50"),
            ("LMM_RELAY_TIMEOUT_SECONDS", "0"),
        ])
        .unwrap();
        assert_eq!(parsed.response_headers, None);
        assert_eq!(parsed.idle, Duration::from_secs(40));
        assert_eq!(parsed.total, None);
    }

    #[test]
    fn legacy_values_are_used_when_native_values_are_absent() {
        let parsed = config(&[
            ("RELAY_RESPONSE_HEADER_TIMEOUT", "10"),
            ("STREAMING_TIMEOUT", "20"),
            ("RELAY_TIMEOUT", "30"),
        ])
        .unwrap();
        assert_eq!(parsed.response_headers, Some(Duration::from_secs(10)));
        assert_eq!(parsed.idle, Duration::from_secs(20));
        assert_eq!(parsed.total, Some(Duration::from_secs(30)));
    }

    #[test]
    fn invalid_native_value_never_falls_back_or_leaks_its_value() {
        for value in [
            "",
            "-1",
            "+1",
            "1.5",
            " 2",
            "secret",
            "18446744073709551615",
        ] {
            let error = config(&[
                ("LMM_RELAY_TIMEOUT_SECONDS", value),
                ("RELAY_TIMEOUT", "20"),
            ])
            .unwrap_err();
            assert_eq!(
                error.to_string(),
                "environment variable LMM_RELAY_TIMEOUT_SECONDS is invalid"
            );
        }
        assert!(config(&[("STREAMING_TIMEOUT", "0")]).is_err());
    }
}
