//! Opt-in loopback ingress confirmation. Never retries a model request.
//!
//! The ingress registers a one-use ticket before returning a selected backend.
//! It must keep that backend accepting until the backend confirms admission,
//! or the ingress proves the client request has ended and revokes the ticket.
//! Confirmation alone is not billing readiness or a durable settlement record.
use reqwest::{Client, Url, header::HeaderValue, redirect::Policy};
use std::{
    env,
    error::Error,
    fs::File,
    io::{self, Read},
    net::IpAddr,
    sync::Arc,
    time::Duration,
};
use tokio::sync::Semaphore;

pub const TICKET_HEADER: &str = "x-dp22-admission";
const MAX_ACKS: usize = 64;

#[derive(Clone)]
pub struct IngressAcks {
    client: Client,
    endpoint: Url,
    token: HeaderValue,
    instance: HeaderValue,
    slots: Arc<Semaphore>,
}

fn invalid(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}

impl IngressAcks {
    /// No DNS names, environment proxy, redirect, or non-loopback endpoint.
    /// Use a unique instance name and a private token file. Two instances may
    /// share the same ingress ACK authority.
    pub fn new(endpoint: &str, token: &[u8], instance: &str) -> Result<Self, Box<dyn Error>> {
        let endpoint = Url::parse(endpoint)?;
        let loopback = endpoint
            .host_str()
            .and_then(|host| host.trim_matches(['[', ']']).parse::<IpAddr>().ok())
            .is_some_and(|address| address.is_loopback());
        if endpoint.scheme() != "http"
            || !loopback
            || endpoint.port().is_none_or(|port| port == 0)
            || !endpoint.username().is_empty()
            || endpoint.password().is_some()
            || endpoint.path() != "/__ack"
            || endpoint.query().is_some()
            || endpoint.fragment().is_some()
        {
            return Err(invalid("ingress ACK must be an explicit loopback HTTP /__ack endpoint").into());
        }
        if !(32..=256).contains(&token.len()) || !token.iter().all(u8::is_ascii_graphic) {
            return Err(invalid("invalid ingress service credential").into());
        }
        if instance.is_empty()
            || instance.len() > 64
            || !instance.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'-' | b'_'))
        {
            return Err(invalid("invalid core instance name").into());
        }
        let mut token = HeaderValue::from_bytes(token)?;
        token.set_sensitive(true);
        Ok(Self {
            client: Client::builder()
                .no_proxy()
                .redirect(Policy::none())
                .connect_timeout(Duration::from_millis(500))
                .timeout(Duration::from_secs(2))
                .pool_max_idle_per_host(2)
                .build()?,
            endpoint,
            token,
            instance: HeaderValue::from_str(instance)?,
            slots: Arc::new(Semaphore::new(MAX_ACKS)),
        })
    }

    /// All three settings are mandatory when any one is present. This opt-in
    /// does not change the public model-readiness endpoint.
    pub fn from_env() -> Result<Option<Self>, Box<dyn Error>> {
        match (
            env::var_os("LMM_CORE_INGRESS_ACK_URL"),
            env::var_os("LMM_CORE_INGRESS_TOKEN_FILE"),
            env::var_os("LMM_CORE_INSTANCE"),
        ) {
            (None, None, None) => Ok(None),
            (Some(endpoint), Some(file), Some(instance)) => {
                let mut bytes = Vec::new();
                File::open(file)?.take(4097).read_to_end(&mut bytes)?;
                if bytes.len() > 4096 {
                    return Err(invalid("ingress service credential file is too large").into());
                }
                let token = std::str::from_utf8(&bytes)?.trim().as_bytes();
                Ok(Some(Self::new(
                    endpoint.to_str().ok_or_else(|| invalid("ACK URL is not UTF-8"))?,
                    token,
                    instance.to_str().ok_or_else(|| invalid("instance name is not UTF-8"))?,
                )?))
            }
            _ => Err(invalid("configure ACK URL, ingress credential file and instance together").into()),
        }
    }

    /// Call only while owning the request's WorkLease, before invoking a
    /// business handler. A failed/unknown confirmation must not reach upstream.
    pub async fn confirm(&self, ticket: &str) -> bool {
        if ticket.len() != 32
            || !ticket.bytes().all(|b| b.is_ascii_digit() || matches!(b, b'a'..=b'f'))
        {
            return false;
        }
        let Ok(_permit) = self.slots.clone().try_acquire_owned() else {
            return false;
        };
        self.client
            .post(self.endpoint.clone())
            .header("x-dp22-admin", self.token.clone())
            .header("x-dp22-instance", self.instance.clone())
            .header(TICKET_HEADER, ticket)
            .body("")
            .send()
            .await
            .is_ok_and(|response| response.status() == reqwest::StatusCode::NO_CONTENT)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::lifecycle::{Admission, Lifecycle, Phase, admit};
    use axum::{
        Router, body::Body, http::{HeaderMap, Request, StatusCode},
        middleware, routing::post,
    };
    use std::sync::atomic::{AtomicUsize, Ordering};
    use tower::ServiceExt;

    const TOKEN: &[u8] = b"dp22-isolated-test-only-credential-000000";
    const TICKET: &str = "0123456789abcdef0123456789abcdef";

    async fn authority(router: Router) -> (String, tokio::task::JoinHandle<()>) {
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let endpoint = format!("http://{}/__ack", listener.local_addr().unwrap());
        let task = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        (endpoint, task)
    }

    fn business(lifecycle: Lifecycle, acks: IngressAcks, calls: Arc<AtomicUsize>) -> Router {
        Router::new()
            .route("/transport", post(move || {
                let calls = calls.clone();
                async move { calls.fetch_add(1, Ordering::SeqCst); "body" }
            }))
            .layer(middleware::from_fn_with_state(Admission::new(lifecycle, Some(acks)), admit))
    }

    fn call(ticket: Option<&str>) -> Request<Body> {
        let mut request = Request::builder().method("POST").uri("/transport");
        if let Some(ticket) = ticket { request = request.header(TICKET_HEADER, ticket); }
        request.body(Body::empty()).unwrap()
    }

    #[test]
    fn service_credential_cannot_leave_the_loopback_ack_endpoint() {
        for endpoint in [
            "http://example.invalid:8800/__ack", "https://127.0.0.1:8800/__ack",
            "http://192.0.2.1:8800/__ack", "http://127.0.0.1/__ack",
            "http://127.0.0.1:8800/wrong", "http://user@127.0.0.1:8800/__ack",
            "http://127.0.0.1:8800/__ack?ticket=x", "http://127.0.0.1:8800/__ack#x",
        ] {
            assert!(IngressAcks::new(endpoint, TOKEN, "a").is_err(), "{endpoint}");
        }
        assert!(IngressAcks::new("http://127.0.0.1:8800/__ack", TOKEN, "bad\nname").is_err());
        assert!(IngressAcks::new("http://127.0.0.1:8800/__ack", b"short", "a").is_err());
    }

    #[tokio::test]
    async fn lease_exists_before_ack_and_survives_drain_before_handler() {
        let lifecycle = Lifecycle::default();
        assert!(lifecycle.mark_ready());
        let observed = lifecycle.clone();
        let (endpoint, task) = authority(Router::new().route("/__ack", post(move |headers: HeaderMap| {
            let observed = observed.clone();
            async move {
                assert_eq!(observed.snapshot(), (Phase::Ready, 1));
                assert_eq!(headers[TICKET_HEADER], TICKET);
                assert_eq!(headers["x-dp22-admin"].as_bytes(), TOKEN);
                assert_eq!(headers["x-dp22-instance"], "a");
                observed.begin_drain();
                StatusCode::NO_CONTENT
            }
        }))).await;
        let calls = Arc::new(AtomicUsize::new(0));
        let app = business(lifecycle.clone(), IngressAcks::new(&endpoint, TOKEN, "a").unwrap(), calls.clone());
        let response = app.oneshot(call(Some(TICKET))).await.unwrap();
        assert_eq!(response.status(), StatusCode::OK);
        assert_eq!(calls.load(Ordering::SeqCst), 1);
        assert_eq!(lifecycle.snapshot(), (Phase::Draining, 1));
        drop(response);
        lifecycle.wait_idle().await;
        task.abort(); let _ = task.await;
    }

    #[tokio::test]
    async fn denied_or_unknown_ack_never_enters_business_handler() {
        for status in [StatusCode::FORBIDDEN, StatusCode::SERVICE_UNAVAILABLE] {
            let (endpoint, task) = authority(Router::new().route("/__ack", post(move || async move { status }))).await;
            let lifecycle = Lifecycle::default();
            assert!(lifecycle.mark_ready());
            let calls = Arc::new(AtomicUsize::new(0));
            let app = business(lifecycle.clone(), IngressAcks::new(&endpoint, TOKEN, "a").unwrap(), calls.clone());
            let response = app.oneshot(call(Some(TICKET))).await.unwrap();
            assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
            assert_eq!(calls.load(Ordering::SeqCst), 0);
            assert_eq!(lifecycle.snapshot().1, 0);
            assert_eq!(response.headers()["connection"], "close");
            task.abort(); let _ = task.await;
        }
    }

    #[tokio::test]
    async fn missing_duplicate_or_malformed_ticket_is_not_ordinary_admission() {
        let ack_calls = Arc::new(AtomicUsize::new(0));
        let observed = ack_calls.clone();
        let (endpoint, task) = authority(Router::new().route("/__ack", post(move || {
            let observed = observed.clone();
            async move { observed.fetch_add(1, Ordering::SeqCst); StatusCode::NO_CONTENT }
        }))).await;
        let lifecycle = Lifecycle::default();
        assert!(lifecycle.mark_ready());
        let calls = Arc::new(AtomicUsize::new(0));
        let app = business(lifecycle.clone(), IngressAcks::new(&endpoint, TOKEN, "a").unwrap(), calls.clone());
        let mut duplicate = call(Some(TICKET));
        duplicate.headers_mut().append(TICKET_HEADER, HeaderValue::from_static(TICKET));
        for request in [call(None), call(Some("not-a-ticket")), duplicate] {
            let response = app.clone().oneshot(request).await.unwrap();
            assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
        }
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        assert_eq!(ack_calls.load(Ordering::SeqCst), 0);
        assert_eq!(lifecycle.snapshot().1, 0);
        task.abort(); let _ = task.await;
    }

    #[tokio::test]
    async fn redirects_are_not_confirmation_and_are_not_followed() {
        let redirected_calls = Arc::new(AtomicUsize::new(0));
        let observed = redirected_calls.clone();
        let (target, target_task) = authority(Router::new().route("/__ack", post(move || {
            let observed = observed.clone();
            async move { observed.fetch_add(1, Ordering::SeqCst); StatusCode::NO_CONTENT }
        }))).await;
        let (endpoint, task) = authority(Router::new().route("/__ack", post(move || {
            let target = target.clone();
            async move { (StatusCode::TEMPORARY_REDIRECT, [("location", target)]) }
        }))).await;
        let acks = IngressAcks::new(&endpoint, TOKEN, "a").unwrap();
        assert!(!acks.confirm(TICKET).await);
        assert_eq!(redirected_calls.load(Ordering::SeqCst), 0);
        task.abort(); target_task.abort(); let _ = task.await; let _ = target_task.await;
    }

    #[tokio::test]
    async fn busy_ack_budget_rejects_without_unbounded_waiters() {
        let acks = IngressAcks::new("http://127.0.0.1:8800/__ack", TOKEN, "a").unwrap();
        let _all = acks.slots.clone().acquire_many_owned(MAX_ACKS as u32).await.unwrap();
        assert!(!acks.confirm(TICKET).await);
    }
}
