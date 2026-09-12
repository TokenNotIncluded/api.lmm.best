//! Provider relay deadlines, independent of control-plane dependencies.

use std::time::Duration;

use axum::{
    body::{Body, Bytes},
    http::{HeaderMap, HeaderName, HeaderValue, Method, StatusCode},
};
use futures_util::{
    Stream, StreamExt,
    stream::{self, BoxStream},
};
use thiserror::Error;

/// HTTP policy owned by the provider attempt, never by internal dependencies.
#[derive(Clone)]
pub struct RelayHttpClient {
    client: reqwest::Client,
    config: RelayTimeoutConfig,
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

#[derive(Debug, Error)]
pub enum RelayHttpError {
    #[error("upstream response timed out")]
    ResponseHeaders,
    #[error("upstream response read timed out")]
    Idle,
    #[error("upstream request failed")]
    Transport(#[source] reqwest::Error),
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
        })
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
        Ok(RelayResponse {
            status,
            headers,
            body: idle_stream(response.bytes_stream(), self.config.idle),
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
