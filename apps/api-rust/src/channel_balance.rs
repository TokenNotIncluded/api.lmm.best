//! Provider balance normalization shared by the advanced channel routes.
//!
//! Provider-specific network targets live here only when they are server-owned
//! constants. Caller input and persisted channel base URLs must never influence
//! the DeepSeek balance destination.

use std::time::Duration;

use rust_decimal::{
    Decimal,
    prelude::{FromPrimitive, ToPrimitive},
};
use secrecy::{ExposeSecret, SecretString};
use serde::Deserialize;

const DEEPSEEK_BALANCE_URL: &str = "https://api.deepseek.com/user/balance";
const DEFAULT_DEEPSEEK_CONNECT_TIMEOUT: Duration = Duration::from_secs(3);
const DEFAULT_DEEPSEEK_REQUEST_TIMEOUT: Duration = Duration::from_secs(15);
const DEFAULT_DEEPSEEK_RESPONSE_LIMIT: usize = 256 * 1024;

/// Failures that make a provider balance unsafe to persist.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum DeepSeekBalanceError {
    InvalidJson,
    MissingCurrency,
    InvalidNumber,
    NegativeBalance,
    NonFiniteBalance,
    InvalidExchangeRate,
    ConversionOverflow,
}

impl std::fmt::Display for DeepSeekBalanceError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        let message = match self {
            Self::InvalidJson => "invalid DeepSeek balance response",
            Self::MissingCurrency => "currency USD or CNY not found",
            Self::InvalidNumber => "balance is not a valid number",
            Self::NegativeBalance => "balance must be non-negative",
            Self::NonFiniteBalance => "balance must be finite",
            Self::InvalidExchangeRate => "USD exchange rate must be finite and positive",
            Self::ConversionOverflow => "converted USD balance must be finite",
        };
        formatter.write_str(message)
    }
}

impl std::error::Error for DeepSeekBalanceError {}

/// Safe error classes for the fixed-origin DeepSeek balance request.
///
/// Raw reqwest errors are intentionally not retained here because their debug
/// representation may contain the provider URL or transport details.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum DeepSeekBalanceFetchError {
    Configuration,
    EmptyCredential,
    Timeout,
    Transport,
    UpstreamStatus(u16),
    ResponseTooLarge,
    Parse(DeepSeekBalanceError),
}

impl std::fmt::Display for DeepSeekBalanceFetchError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Configuration => {
                formatter.write_str("DeepSeek balance client configuration failed")
            }
            Self::EmptyCredential => formatter.write_str("DeepSeek channel credential is empty"),
            Self::Timeout => formatter.write_str("DeepSeek balance request timed out"),
            Self::Transport => formatter.write_str("DeepSeek balance request failed"),
            Self::UpstreamStatus(status) => {
                write!(formatter, "DeepSeek balance upstream status: {status}")
            }
            Self::ResponseTooLarge => formatter.write_str("DeepSeek balance response is too large"),
            Self::Parse(error) => error.fmt(formatter),
        }
    }
}

impl std::error::Error for DeepSeekBalanceFetchError {}

#[derive(Deserialize)]
struct DeepSeekBalanceResponse {
    #[serde(default)]
    balance_infos: Vec<DeepSeekBalanceInfo>,
}

#[derive(Deserialize)]
struct DeepSeekBalanceInfo {
    #[serde(default)]
    currency: String,
    #[serde(default)]
    total_balance: String,
}

/// Bounded DeepSeek balance client with a server-owned production origin.
///
/// Production construction has no endpoint argument. That deliberate API shape
/// prevents request JSON or a persisted channel `base_url` from turning the
/// balance refresh route into a generic SSRF/DNS-rebinding primitive.
#[derive(Clone)]
pub(crate) struct DeepSeekBalanceClient {
    client: reqwest::Client,
    endpoint: reqwest::Url,
    timeout: Duration,
    max_response_bytes: usize,
}

impl DeepSeekBalanceClient {
    pub(crate) fn new() -> Result<Self, DeepSeekBalanceFetchError> {
        let endpoint = reqwest::Url::parse(DEEPSEEK_BALANCE_URL)
            .map_err(|_| DeepSeekBalanceFetchError::Configuration)?;
        let client = reqwest::Client::builder()
            .use_rustls_tls()
            .connect_timeout(DEFAULT_DEEPSEEK_CONNECT_TIMEOUT)
            .timeout(DEFAULT_DEEPSEEK_REQUEST_TIMEOUT)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| DeepSeekBalanceFetchError::Configuration)?;
        Ok(Self {
            client,
            endpoint,
            timeout: DEFAULT_DEEPSEEK_REQUEST_TIMEOUT,
            max_response_bytes: DEFAULT_DEEPSEEK_RESPONSE_LIMIT,
        })
    }

    #[cfg(test)]
    fn with_test_endpoint(
        endpoint: reqwest::Url,
        max_response_bytes: usize,
    ) -> Result<Self, DeepSeekBalanceFetchError> {
        validate_test_loopback_endpoint(&endpoint)?;
        let client = reqwest::Client::builder()
            .connect_timeout(DEFAULT_DEEPSEEK_CONNECT_TIMEOUT)
            .timeout(DEFAULT_DEEPSEEK_REQUEST_TIMEOUT)
            .redirect(reqwest::redirect::Policy::none())
            .build()
            .map_err(|_| DeepSeekBalanceFetchError::Configuration)?;
        Ok(Self {
            client,
            endpoint,
            timeout: DEFAULT_DEEPSEEK_REQUEST_TIMEOUT,
            max_response_bytes,
        })
    }

    /// Fetches and normalizes a DeepSeek balance using only the persisted key
    /// supplied by the caller and this client's fixed provider endpoint.
    pub(crate) async fn fetch_usd(
        &self,
        credential: &SecretString,
        cny_per_usd: f64,
    ) -> Result<f64, DeepSeekBalanceFetchError> {
        if self.max_response_bytes == 0 || self.timeout.is_zero() {
            return Err(DeepSeekBalanceFetchError::Configuration);
        }
        let key = credential.expose_secret().trim();
        if key.is_empty() {
            return Err(DeepSeekBalanceFetchError::EmptyCredential);
        }
        let request = self
            .client
            .get(self.endpoint.clone())
            .bearer_auth(key)
            .header(reqwest::header::ACCEPT, "application/json");
        let mut response = tokio::time::timeout(self.timeout, request.send())
            .await
            .map_err(|_| DeepSeekBalanceFetchError::Timeout)?
            .map_err(|_| DeepSeekBalanceFetchError::Transport)?;
        let status = response.status();
        if !status.is_success() {
            return Err(DeepSeekBalanceFetchError::UpstreamStatus(status.as_u16()));
        }
        if response
            .content_length()
            .is_some_and(|length| length > self.max_response_bytes as u64)
        {
            return Err(DeepSeekBalanceFetchError::ResponseTooLarge);
        }

        let read = async {
            let mut body = Vec::new();
            while let Some(chunk) = response
                .chunk()
                .await
                .map_err(|_| DeepSeekBalanceFetchError::Transport)?
            {
                if body.len().saturating_add(chunk.len()) > self.max_response_bytes {
                    return Err(DeepSeekBalanceFetchError::ResponseTooLarge);
                }
                body.extend_from_slice(&chunk);
            }
            Ok::<_, DeepSeekBalanceFetchError>(body)
        };
        let body = tokio::time::timeout(self.timeout, read)
            .await
            .map_err(|_| DeepSeekBalanceFetchError::Timeout)??;
        deepseek_balance_usd(&body, cny_per_usd).map_err(DeepSeekBalanceFetchError::Parse)
    }
}

#[cfg(test)]
fn validate_test_loopback_endpoint(
    endpoint: &reqwest::Url,
) -> Result<(), DeepSeekBalanceFetchError> {
    if endpoint.scheme() != "http"
        || !endpoint.username().is_empty()
        || endpoint.password().is_some()
        || endpoint.query().is_some()
        || endpoint.fragment().is_some()
    {
        return Err(DeepSeekBalanceFetchError::Configuration);
    }
    let host = endpoint
        .host_str()
        .ok_or(DeepSeekBalanceFetchError::Configuration)?;
    host.parse::<std::net::IpAddr>()
        .ok()
        .filter(std::net::IpAddr::is_loopback)
        .map(|_| ())
        .ok_or(DeepSeekBalanceFetchError::Configuration)
}

/// Returns the USD-denominated DeepSeek balance using the current Go oracle:
/// prefer the first explicit USD entry, otherwise convert the first CNY entry.
///
/// The exchange rate is intentionally consulted only for the CNY fallback.
/// This matches Go and prevents an unrelated invalid rate from rejecting a
/// provider response that already supplies a valid USD balance.
pub(crate) fn deepseek_balance_usd(
    body: &[u8],
    cny_per_usd: f64,
) -> Result<f64, DeepSeekBalanceError> {
    let response: DeepSeekBalanceResponse =
        serde_json::from_slice(body).map_err(|_| DeepSeekBalanceError::InvalidJson)?;

    let mut usd = None;
    let mut cny = None;
    for balance in response.balance_infos {
        let currency = balance.currency.trim();
        if usd.is_none() && currency.eq_ignore_ascii_case("USD") {
            usd = Some(balance.total_balance);
        } else if cny.is_none() && currency.eq_ignore_ascii_case("CNY") {
            cny = Some(balance.total_balance);
        }
    }

    if let Some(raw) = usd {
        return parse_non_negative_finite_balance(&raw);
    }
    let raw = cny.ok_or(DeepSeekBalanceError::MissingCurrency)?;
    let cny_balance = parse_non_negative_finite_balance(&raw)?;
    convert_cny_balance_to_usd(cny_balance, cny_per_usd)
}

fn parse_non_negative_finite_balance(raw: &str) -> Result<f64, DeepSeekBalanceError> {
    let balance = raw
        .trim()
        .parse::<f64>()
        .map_err(|_| DeepSeekBalanceError::InvalidNumber)?;
    if !balance.is_finite() {
        return Err(DeepSeekBalanceError::NonFiniteBalance);
    }
    if balance < 0.0 {
        return Err(DeepSeekBalanceError::NegativeBalance);
    }
    Ok(balance)
}

fn convert_cny_balance_to_usd(
    cny_balance: f64,
    cny_per_usd: f64,
) -> Result<f64, DeepSeekBalanceError> {
    if !cny_per_usd.is_finite() || cny_per_usd <= 0.0 {
        return Err(DeepSeekBalanceError::InvalidExchangeRate);
    }
    if !cny_balance.is_finite() {
        return Err(DeepSeekBalanceError::NonFiniteBalance);
    }

    // Go uses shopspring/decimal for this division rather than raw binary
    // floating-point arithmetic. Reuse the repository's decimal dependency so
    // ordinary monetary values follow the same decimal-first conversion path.
    let balance = Decimal::from_f64(cny_balance).ok_or(DeepSeekBalanceError::ConversionOverflow)?;
    let rate = Decimal::from_f64(cny_per_usd).ok_or(DeepSeekBalanceError::InvalidExchangeRate)?;
    let usd = balance
        .checked_div(rate)
        .and_then(|value| value.to_f64())
        .ok_or(DeepSeekBalanceError::ConversionOverflow)?;
    if !usd.is_finite() {
        return Err(DeepSeekBalanceError::ConversionOverflow);
    }
    if usd < 0.0 {
        return Err(DeepSeekBalanceError::NegativeBalance);
    }
    Ok(usd)
}

#[cfg(test)]
mod tests {
    use std::sync::{Arc, Mutex, MutexGuard};

    use axum::{
        Router,
        extract::{Request, State},
        http::{StatusCode, header},
        response::{IntoResponse, Response},
        routing::any,
    };
    use secrecy::SecretString;
    use serde_json::json;

    use super::{
        DeepSeekBalanceClient, DeepSeekBalanceError, DeepSeekBalanceFetchError,
        deepseek_balance_usd,
    };

    fn response(entries: &[(&str, &str)]) -> Vec<u8> {
        serde_json::to_vec(&json!({
            "is_available": true,
            "balance_infos": entries
                .iter()
                .map(|(currency, total)| json!({
                    "currency": currency,
                    "total_balance": total,
                    "granted_balance": "0",
                    "topped_up_balance": total,
                }))
                .collect::<Vec<_>>()
        }))
        .expect("DeepSeek balance fixture")
    }

    fn lock_unpoisoned<T>(mutex: &Mutex<T>) -> MutexGuard<'_, T> {
        match mutex.lock() {
            Ok(guard) => guard,
            Err(poisoned) => poisoned.into_inner(),
        }
    }

    type RecordedRequests = Arc<Mutex<Vec<(String, Option<String>)>>>;

    #[derive(Clone)]
    struct MockState {
        requests: RecordedRequests,
        body: Vec<u8>,
        redirect: bool,
    }

    async fn mock_balance(State(state): State<MockState>, request: Request) -> Response {
        let path = request.uri().path().to_owned();
        let authorization = request
            .headers()
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .map(str::to_owned);
        lock_unpoisoned(&state.requests).push((path, authorization));
        if state.redirect {
            return (StatusCode::FOUND, [(header::LOCATION, "/redirect-target")]).into_response();
        }
        (StatusCode::OK, state.body).into_response()
    }

    async fn local_balance_server(
        body: Vec<u8>,
        redirect: bool,
    ) -> (
        reqwest::Url,
        RecordedRequests,
        tokio::task::JoinHandle<std::io::Result<()>>,
    ) {
        let requests = Arc::new(Mutex::new(Vec::new()));
        let state = MockState {
            requests: Arc::clone(&requests),
            body,
            redirect,
        };
        let app = Router::new().fallback(any(mock_balance)).with_state(state);
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0")
            .await
            .expect("bind balance mock");
        let address = listener.local_addr().expect("balance mock address");
        let endpoint = format!("http://{address}/user/balance")
            .parse()
            .expect("balance mock URL");
        let task = tokio::spawn(async move { axum::serve(listener, app).await });
        (endpoint, requests, task)
    }

    #[test]
    fn explicit_usd_wins_over_cny_and_does_not_consult_exchange_rate() {
        let body = response(&[("CNY", "73"), ("USD", "12.5")]);
        assert_eq!(deepseek_balance_usd(&body, f64::NAN), Ok(12.5));
    }

    #[test]
    fn cny_fallback_uses_decimal_exchange_rate() {
        let body = response(&[(" CnY ", "73")]);
        assert_eq!(deepseek_balance_usd(&body, 7.3), Ok(10.0));
    }

    #[test]
    fn currency_matching_is_trimmed_case_insensitive_and_first_match_wins() {
        let body = response(&[(" usd ", "3.25"), ("USD", "99")]);
        assert_eq!(deepseek_balance_usd(&body, 7.3), Ok(3.25));
    }

    #[test]
    fn missing_supported_currency_is_rejected() {
        let body = response(&[("EUR", "5")]);
        assert_eq!(
            deepseek_balance_usd(&body, 7.3),
            Err(DeepSeekBalanceError::MissingCurrency)
        );
    }

    #[test]
    fn malformed_negative_and_non_finite_balances_are_rejected() {
        for (raw, expected) in [
            ("not-a-number", DeepSeekBalanceError::InvalidNumber),
            ("-0.01", DeepSeekBalanceError::NegativeBalance),
            ("NaN", DeepSeekBalanceError::NonFiniteBalance),
            ("inf", DeepSeekBalanceError::NonFiniteBalance),
        ] {
            let body = response(&[("USD", raw)]);
            assert_eq!(deepseek_balance_usd(&body, 7.3), Err(expected), "{raw}");
        }
    }

    #[test]
    fn invalid_exchange_rates_are_rejected_only_for_cny_fallback() {
        let body = response(&[("CNY", "73")]);
        for rate in [0.0, -1.0, f64::NAN, f64::INFINITY] {
            assert_eq!(
                deepseek_balance_usd(&body, rate),
                Err(DeepSeekBalanceError::InvalidExchangeRate)
            );
        }
    }

    #[test]
    fn overflowing_cny_conversion_is_rejected() {
        let body = response(&[("CNY", &f64::MAX.to_string())]);
        assert_eq!(
            deepseek_balance_usd(&body, f64::MIN_POSITIVE),
            Err(DeepSeekBalanceError::ConversionOverflow)
        );
    }

    #[test]
    fn invalid_json_is_rejected() {
        assert_eq!(
            deepseek_balance_usd(br#"{"balance_infos": ["#, 7.3),
            Err(DeepSeekBalanceError::InvalidJson)
        );
    }

    #[tokio::test]
    async fn fixed_origin_client_uses_bearer_auth_and_bounded_provider_path() {
        let (endpoint, requests, task) =
            local_balance_server(response(&[("USD", "12.5")]), false).await;
        let client = DeepSeekBalanceClient::with_test_endpoint(endpoint, 4096)
            .expect("test DeepSeek balance client");
        let balance = client
            .fetch_usd(&SecretString::from("persisted-secret".to_owned()), 7.3)
            .await
            .expect("DeepSeek balance request");
        assert_eq!(balance, 12.5);
        let captured = lock_unpoisoned(&requests).clone();
        assert_eq!(captured.len(), 1);
        assert_eq!(captured[0].0, "/user/balance");
        assert_eq!(captured[0].1.as_deref(), Some("Bearer persisted-secret"));
        task.abort();
    }

    #[tokio::test]
    async fn fixed_origin_client_never_follows_provider_redirects() {
        let (endpoint, requests, task) = local_balance_server(Vec::new(), true).await;
        let client = DeepSeekBalanceClient::with_test_endpoint(endpoint, 4096)
            .expect("test DeepSeek balance client");
        let result = client
            .fetch_usd(&SecretString::from("persisted-secret".to_owned()), 7.3)
            .await;
        assert_eq!(result, Err(DeepSeekBalanceFetchError::UpstreamStatus(302)));
        assert_eq!(lock_unpoisoned(&requests).len(), 1);
        task.abort();
    }

    #[tokio::test]
    async fn fixed_origin_client_rejects_oversized_balance_responses() {
        let (endpoint, _, task) = local_balance_server(vec![b'x'; 128], false).await;
        let client = DeepSeekBalanceClient::with_test_endpoint(endpoint, 32)
            .expect("test DeepSeek balance client");
        let result = client
            .fetch_usd(&SecretString::from("persisted-secret".to_owned()), 7.3)
            .await;
        assert_eq!(result, Err(DeepSeekBalanceFetchError::ResponseTooLarge));
        task.abort();
    }

    #[test]
    fn test_endpoint_rejects_non_loopback_or_credential_bearing_targets() {
        for raw in [
            "https://api.deepseek.com/user/balance",
            "http://example.com/user/balance",
            "http://user:pass@127.0.0.1/user/balance",
            "http://127.0.0.1/user/balance?next=attacker",
        ] {
            let endpoint = reqwest::Url::parse(raw).expect("test URL");
            assert_eq!(
                DeepSeekBalanceClient::with_test_endpoint(endpoint, 4096).err(),
                Some(DeepSeekBalanceFetchError::Configuration),
                "{raw}"
            );
        }
    }

    #[test]
    fn production_client_constructor_is_fixed_and_valid() {
        DeepSeekBalanceClient::new().expect("fixed DeepSeek balance endpoint should be valid");
    }
}
