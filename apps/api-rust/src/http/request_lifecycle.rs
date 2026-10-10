//! Observe the complete response body, including trailers, errors and drops.

use std::{
    pin::Pin,
    task::{Context, Poll},
    time::Instant,
};

use axum::{
    body::{Body, Bytes, HttpBody},
    http::{Method, Uri},
    response::Response,
};
use http_body::{Frame, SizeHint};

use super::InflightGuard;

pub(super) struct RequestLifecycle {
    guard: Option<InflightGuard>,
    dispatcher: tracing::Dispatch,
    started: Instant,
    request_id: String,
    method: Method,
    path: String,
    route_tag: &'static str,
    client_ip: String,
    status: u16,
    bytes: u64,
    finished: bool,
}

impl RequestLifecycle {
    pub(super) fn new(
        guard: Option<InflightGuard>,
        request_id: String,
        method: Method,
        uri: &Uri,
        matched_path: Option<&str>,
        client_ip: String,
    ) -> Self {
        Self {
            guard,
            dispatcher: tracing::dispatcher::get_default(Clone::clone),
            started: Instant::now(),
            request_id,
            method,
            path: logged_path(uri),
            route_tag: route_tag(matched_path),
            client_ip,
            status: 0,
            bytes: 0,
            finished: false,
        }
    }

    pub(super) fn response(mut self, mut response: Response) -> Response {
        self.status = response.status().as_u16();
        let body = std::mem::replace(response.body_mut(), Body::empty());
        if body.is_end_stream() {
            // Drop the underlying body first: its cancellation/finalization
            // hooks may register a settlement task that shutdown must await.
            drop(body);
            self.finish("completed");
        } else {
            *response.body_mut() = Body::new(ObservedBody {
                inner: Some(body),
                lifecycle: self,
            });
        }
        response
    }

    fn finish(&mut self, outcome: &'static str) {
        if self.finished {
            return;
        }
        self.finished = true;
        self.guard.take();
        tracing::dispatcher::with_default(&self.dispatcher, || {
            tracing::info!(target: "lmm_api_access",
                request_id = %self.request_id,
                route_tag = self.route_tag,
                status = self.status,
                method = %self.method,
                path = %self.path,
                client_ip = %self.client_ip,
                elapsed_ms = self.started.elapsed().as_secs_f64() * 1000.0,
                response_bytes = self.bytes,
                outcome,
                "http request completed"
            );
        });
    }
}

impl Drop for RequestLifecycle {
    fn drop(&mut self) {
        self.finish(if self.status == 0 {
            "handler_cancelled"
        } else {
            "cancelled"
        });
    }
}

struct ObservedBody {
    inner: Option<Body>,
    lifecycle: RequestLifecycle,
}

impl ObservedBody {
    fn finish(&mut self, outcome: &'static str) {
        // Keep response-owned cancellation hooks inside the counted lifetime.
        self.inner.take();
        self.lifecycle.finish(outcome);
    }
}

impl Drop for ObservedBody {
    fn drop(&mut self) {
        self.finish("cancelled");
    }
}

impl HttpBody for ObservedBody {
    type Data = Bytes;
    type Error = axum::Error;

    fn poll_frame(
        self: Pin<&mut Self>,
        cx: &mut Context<'_>,
    ) -> Poll<Option<Result<Frame<Bytes>, Self::Error>>> {
        let this = self.get_mut();
        let Some(inner) = this.inner.as_mut() else {
            return Poll::Ready(None);
        };
        match Pin::new(inner).poll_frame(cx) {
            Poll::Ready(Some(Ok(frame))) => {
                if let Some(data) = frame.data_ref() {
                    this.lifecycle.bytes = this.lifecycle.bytes.saturating_add(data.len() as u64);
                }
                if this.inner.as_ref().is_some_and(HttpBody::is_end_stream) {
                    this.finish("completed");
                }
                Poll::Ready(Some(Ok(frame)))
            }
            Poll::Ready(Some(Err(error))) => {
                this.finish("body_error");
                Poll::Ready(Some(Err(error)))
            }
            Poll::Ready(None) => {
                this.finish("completed");
                Poll::Ready(None)
            }
            Poll::Pending => Poll::Pending,
        }
    }

    fn is_end_stream(&self) -> bool {
        self.inner.as_ref().is_none_or(HttpBody::is_end_stream)
    }

    fn size_hint(&self) -> SizeHint {
        self.inner
            .as_ref()
            .map_or_else(|| SizeHint::with_exact(0), HttpBody::size_hint)
    }
}

fn logged_path(uri: &Uri) -> String {
    // Go logs URL.Path (decoded) and suppresses the complete query on OAuth
    // flows. Decode before classifying, including percent-encoded prefixes.
    let path = if uri.path().contains('%') {
        let encoded = format!(
            "path={}",
            uri.path().replace('&', "%26").replace('+', "%2B")
        );
        form_urlencoded::parse(encoded.as_bytes())
            .next()
            .map(|(_, value)| value.into_owned())
            .unwrap_or_default()
    } else {
        uri.path().to_owned()
    };
    if ["/api/oauth2/", "/api/user/auth/oauth2/", "/oauth/"]
        .iter()
        .any(|prefix| path.starts_with(prefix))
    {
        return path;
    }
    let Some(query) = uri.query().filter(|value| !value.is_empty()) else {
        return path;
    };
    // API credentials can also arrive in query parameters (for example the
    // Gemini key parameter). Keep ordinary filters but never log credentials.
    if form_urlencoded::parse(query.as_bytes()).any(|(key, _)| {
        matches!(
            key.to_ascii_lowercase().as_str(),
            "key"
                | "api_key"
                | "apikey"
                | "access_token"
                | "refresh_token"
                | "client_secret"
                | "password"
                | "token"
        )
    }) {
        return path;
    }
    format!("{path}?{query}")
}

fn route_tag(matched_path: Option<&str>) -> &'static str {
    let Some(path) = matched_path else {
        return "web";
    };
    if path.starts_with("/dashboard/billing/") || path.starts_with("/v1/dashboard/billing/") {
        return "old_api";
    }
    if path == "/mcp" || path.starts_with("/mcp/") {
        return "mcp";
    }
    if path.starts_with("/api/assistant/")
        && ![
            "/api/assistant/presets",
            "/api/assistant/support",
            "/api/assistant/admin",
        ]
        .iter()
        .any(|prefix| path.starts_with(prefix))
    {
        return "relay";
    }
    if path.starts_with("/api/pg/") {
        return "relay";
    }
    if path.starts_with("/api/") {
        return "api";
    }
    if [
        "/v1/",
        "/v1beta/",
        "/v1beta1/",
        "/mj/",
        "/suno/",
        "/kling/",
        "/jimeng/",
        "/video/",
    ]
    .iter()
    .any(|prefix| path.starts_with(prefix))
        || path.starts_with("/{mode}/mj/")
    {
        return "relay";
    }
    "web"
}

#[cfg(test)]
mod tests {
    use super::*;
    use http_body_util::{BodyExt, StreamBody};
    use std::{
        collections::BTreeMap,
        sync::{Arc, Mutex, atomic::Ordering},
    };
    use tracing::{
        Event, Metadata, Subscriber,
        field::{Field, Visit},
        span::{Attributes, Id, Record},
    };

    #[derive(Clone, Default)]
    struct Events(Arc<Mutex<Vec<BTreeMap<String, String>>>>);
    struct Fields(BTreeMap<String, String>);
    impl Visit for Fields {
        fn record_debug(&mut self, field: &Field, value: &dyn std::fmt::Debug) {
            self.0.insert(field.name().to_owned(), format!("{value:?}"));
        }
    }
    impl Subscriber for Events {
        fn enabled(&self, metadata: &Metadata<'_>) -> bool {
            metadata.target() == "lmm_api_access"
        }
        fn new_span(&self, _: &Attributes<'_>) -> Id {
            Id::from_u64(1)
        }
        fn record(&self, _: &Id, _: &Record<'_>) {}
        fn record_follows_from(&self, _: &Id, _: &Id) {}
        fn event(&self, event: &Event<'_>) {
            let mut fields = Fields(BTreeMap::new());
            event.record(&mut fields);
            self.0.lock().unwrap().push(fields.0);
        }
        fn enter(&self, _: &Id) {}
        fn exit(&self, _: &Id) {}
    }

    fn observed(body: Body) -> (Response, super::super::RuntimeState, Events) {
        let runtime = super::super::RuntimeState::default();
        runtime.inflight.fetch_add(1, Ordering::AcqRel);
        let events = Events::default();
        let dispatcher = tracing::Dispatch::new(events.clone());
        let lifecycle = tracing::dispatcher::with_default(&dispatcher, || {
            RequestLifecycle::new(
                Some(InflightGuard(runtime.clone())),
                "trusted-request-id".into(),
                Method::GET,
                &"/api/about?language=en".parse().unwrap(),
                Some("/api/about"),
                "127.0.0.1".into(),
            )
        });
        (lifecycle.response(Response::new(body)), runtime, events)
    }

    #[tokio::test]
    async fn response_headers_do_not_finish_the_request_and_cancellation_releases_once() {
        let (response, runtime, events) =
            observed(Body::from_stream(futures_util::stream::pending::<
                Result<Bytes, std::io::Error>,
            >()));
        assert_eq!(runtime.inflight(), 1);
        assert!(events.0.lock().unwrap().is_empty());
        runtime.begin_drain();
        drop(response);
        assert_eq!(runtime.inflight(), 0);
        let entries = events.0.lock().unwrap();
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0]["outcome"], "\"cancelled\"");
        assert_eq!(entries[0]["request_id"], "trusted-request-id");
    }

    #[tokio::test]
    async fn data_and_trailers_pass_through_and_completion_is_logged_once() {
        let mut trailers = axum::http::HeaderMap::new();
        trailers.insert("x-provider-proof", "kept".parse().unwrap());
        let frames = futures_util::stream::iter([
            Ok::<_, std::io::Error>(Frame::data(Bytes::from_static(b"hello"))),
            Ok(Frame::trailers(trailers.clone())),
        ]);
        let (response, runtime, events) = observed(Body::new(StreamBody::new(frames)));
        assert_eq!(runtime.inflight(), 1);
        let mut body = response.into_body();
        assert_eq!(
            body.frame().await.unwrap().unwrap().into_data().unwrap(),
            "hello"
        );
        assert_eq!(runtime.inflight(), 1, "trailers have not been consumed");
        assert_eq!(
            body.frame()
                .await
                .unwrap()
                .unwrap()
                .into_trailers()
                .unwrap(),
            trailers
        );
        assert!(body.frame().await.is_none());
        assert_eq!(runtime.inflight(), 0);
        drop(body);
        let entries = events.0.lock().unwrap();
        assert_eq!(entries.len(), 1);
        assert_eq!(entries[0]["outcome"], "\"completed\"");
        assert_eq!(entries[0]["response_bytes"], "5");
    }

    #[tokio::test]
    async fn body_errors_and_empty_responses_release_inflight_without_rewriting_the_body() {
        let frames = futures_util::stream::iter([Err::<Bytes, _>(std::io::Error::other(
            "synthetic failure",
        ))]);
        let (response, runtime, events) = observed(Body::from_stream(frames));
        assert!(response.into_body().collect().await.is_err());
        assert_eq!(runtime.inflight(), 0);
        assert_eq!(events.0.lock().unwrap()[0]["outcome"], "\"body_error\"");
        let (response, runtime, events) = observed(Body::empty());
        assert_eq!(runtime.inflight(), 0);
        assert!(
            response
                .into_body()
                .collect()
                .await
                .unwrap()
                .to_bytes()
                .is_empty()
        );
        assert_eq!(events.0.lock().unwrap().len(), 1);
    }

    #[test]
    fn oauth_and_query_credentials_are_redacted_but_ordinary_queries_are_retained() {
        for (input, expected) in [
            (
                "/api/oauth2/authorize?state=secret",
                "/api/oauth2/authorize",
            ),
            (
                "/api/user/auth/oauth2/consent?csrf=secret",
                "/api/user/auth/oauth2/consent",
            ),
            ("/%6fauth/lmm/callback?code=secret", "/oauth/lmm/callback"),
            ("/v1beta/models?%6bey=secret", "/v1beta/models"),
            ("/api/models?filter=visible", "/api/models?filter=visible"),
        ] {
            assert_eq!(logged_path(&input.parse().unwrap()), expected);
        }
        assert_eq!(route_tag(Some("/v1/dashboard/billing/usage")), "old_api");
        assert_eq!(route_tag(Some("/api/assistant/chat")), "relay");
        assert_eq!(route_tag(Some("/api/assistant/support/self")), "api");
        assert_eq!(route_tag(None), "web");
    }
}
