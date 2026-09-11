//! Shared outbound HTTP policy for future mounted control-plane routes.
//!
//! Callers must still validate their own destination allowlist before issuing a
//! request.  This builder provides the common transport baseline: rustls-only
//! HTTPS, bounded connect and whole-request deadlines, and no implicit
//! cross-origin redirect following.

use std::time::Duration;

use thiserror::Error;

const MAX_CONNECT_TIMEOUT: Duration = Duration::from_secs(3);

#[derive(Debug, Error)]
pub enum OutboundHttpError {
    #[error("outbound HTTP timeout must be greater than zero")]
    ZeroTimeout,
    #[error("unable to construct outbound HTTP client")]
    Build(#[source] reqwest::Error),
}

/// Builds the common HTTPS-only client for pinned/allowlisted control-plane
/// integrations. Redirects are disabled so destination validation cannot be
/// bypassed after the initial URL is checked.
pub fn client(timeout: Duration) -> Result<reqwest::Client, OutboundHttpError> {
    if timeout.is_zero() {
        return Err(OutboundHttpError::ZeroTimeout);
    }
    reqwest::Client::builder()
        .use_rustls_tls()
        .https_only(true)
        .connect_timeout(timeout.min(MAX_CONNECT_TIMEOUT))
        .timeout(timeout)
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(OutboundHttpError::Build)
}

#[cfg(test)]
mod tests {
    use super::client;
    use std::time::Duration;

    #[test]
    fn client_rejects_an_unbounded_zero_timeout() {
        assert!(client(Duration::ZERO).is_err());
    }

    #[test]
    fn client_accepts_a_bounded_timeout() {
        assert!(client(Duration::from_millis(1)).is_ok());
    }
}
