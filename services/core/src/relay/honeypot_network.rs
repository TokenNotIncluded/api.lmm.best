//! Origin-bound HTTPS connections. Validated DNS answers are the addresses the
//! connector receives; there is no second DNS lookup or unsafe TLS mode.
#[cfg(test)]
#[path = "honeypot_network/dns_tests.rs"]
mod dns_tests;
mod iana_ipv6;
mod svix_ip;
#[cfg(test)]
#[path = "honeypot_network/tls_tests.rs"]
mod tls_tests;

use super::{Limits, RelayError};
use ipnet::Ipv6Net;
use reqwest::{
    Certificate, Client, ClientBuilder, Url,
    dns::{Name, Resolve, Resolving},
};
use std::{
    collections::BTreeSet,
    io,
    net::{IpAddr, Ipv6Addr, SocketAddr, ToSocketAddrs},
    sync::Arc,
};
use tokio::sync::{Semaphore, oneshot};

const MAX_DNS_ADDRESSES: usize = 32;
const MAX_DNS_THREADS: usize = 2;

pub(super) fn dns_slots(max_in_flight: usize) -> Arc<Semaphore> {
    Arc::new(Semaphore::new(max_in_flight.min(MAX_DNS_THREADS)))
}

/// Conservative SSRF classification reuses Svix's maintained MIT implementation
/// plus IANA's allocated IPv6 prefixes. Protocol-special IPv6 assignments are
/// excluded even when a narrow anycast exception is globally reachable.
pub(crate) fn public_address(ip: IpAddr) -> bool {
    match ip {
        IpAddr::V4(_) => svix_ip::is_allowed(ip),
        IpAddr::V6(ip6) => {
            const SPECIAL: Ipv6Net =
                Ipv6Net::new_assert(Ipv6Addr::new(0x2001, 0, 0, 0, 0, 0, 0, 0), 23);
            !SPECIAL.contains(&ip6)
                && iana_ipv6::ALLOCATED
                    .iter()
                    .any(|range| range.contains(&ip6))
                && svix_ip::is_allowed(ip)
        }
    }
}

pub(crate) fn literal_ip(url: &Url) -> Option<IpAddr> {
    url.host_str()?.trim_matches(['[', ']']).parse().ok()
}

/// Shared by domain policy validation, host origin validation and dispatch.
/// DNS names resolving to local/private space are rejected by resolve_public;
/// the local switch only applies to numeric loopback URLs, never DNS names.
pub(crate) fn validate_base_url(raw: &str, local_http: bool) -> Result<Url, RelayError> {
    if raw.is_empty() || raw.len() > 2048 || raw.chars().any(char::is_control) || raw.contains('\\')
    {
        return Err(RelayError::Invalid("invalid upstream URL"));
    }
    let url = Url::parse(raw).map_err(|_| RelayError::Invalid("invalid upstream URL"))?;
    if !url.username().is_empty()
        || url.password().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.host_str().is_none()
        || url.port_or_known_default().is_none()
    {
        return Err(RelayError::Invalid("invalid upstream URL"));
    }
    match literal_ip(&url) {
        Some(ip) if ip.is_loopback() && local_http => {
            if !matches!(url.scheme(), "http" | "https") {
                return Err(RelayError::Invalid("invalid upstream URL"));
            }
        }
        Some(ip) => {
            if url.scheme() != "https" || !public_address(ip) {
                return Err(RelayError::Invalid("invalid upstream URL"));
            }
        }
        None => {
            if url.scheme() != "https" {
                return Err(RelayError::Invalid("invalid upstream URL"));
            }
            let host = url
                .host_str()
                .ok_or(RelayError::Invalid("invalid upstream URL"))?;
            if host.len() > 253
                || host.ends_with('.')
                || host.split('.').any(|label| {
                    label.is_empty()
                        || label.len() > 63
                        || label.starts_with('-')
                        || label.ends_with('-')
                        || !label
                            .bytes()
                            .all(|b| b.is_ascii_alphanumeric() || b == b'-')
                })
            {
                return Err(RelayError::Invalid("invalid upstream URL"));
            }
        }
    }
    Ok(url)
}

pub(crate) fn canonical_origin(url: &Url) -> String {
    url.origin().ascii_serialization()
}

pub(crate) fn validate_origin(raw: &str, local_http: bool) -> Result<String, RelayError> {
    let url = validate_base_url(raw, local_http)?;
    if url.path() != "/" {
        return Err(RelayError::Invalid("invalid upstream origin"));
    }
    Ok(canonical_origin(&url))
}

/// The same hardening is used for the existing explicit local transport and all
/// per-origin clients. Default TLS chain/hostname verification remains enabled.
pub(crate) fn client_builder(limits: &Limits) -> ClientBuilder {
    Client::builder()
        .connect_timeout(limits.connect_timeout)
        .redirect(reqwest::redirect::Policy::none())
        .retry(reqwest::retry::never())
        .referer(false)
        .no_proxy()
        .no_gzip()
        .no_brotli()
        .no_deflate()
        .no_zstd()
        .pool_max_idle_per_host(0)
        .http2_adaptive_window(false)
        .http2_initial_stream_window_size(65_536)
        .http2_initial_connection_window_size(262_144)
        .http2_max_frame_size(16_384)
        .http2_max_header_list_size(16_384)
        .min_tls_version(reqwest::tls::Version::TLS_1_2)
        .user_agent("lmm-core-relay/0.1")
}

#[derive(Clone)]
pub(crate) struct NetworkTarget {
    pub(crate) additional_root: Option<Certificate>,
    pub(crate) local_http: bool,
    pub(crate) allowed_origins: BTreeSet<String>,
}
pub(crate) struct PreparedConnection {
    pub(crate) client: Client,
    peers: BTreeSet<SocketAddr>,
}
impl PreparedConnection {
    pub(crate) fn matches_peer(&self, peer: Option<SocketAddr>) -> bool {
        peer.is_some_and(|peer| {
            self.peers
                .contains(&SocketAddr::new(peer.ip().to_canonical(), peer.port()))
        })
    }
}

struct RejectDns;
impl Resolve for RejectDns {
    fn resolve(&self, _: Name) -> Resolving {
        Box::pin(async {
            Err(Box::new(io::Error::other("unconfigured DNS lookup"))
                as Box<dyn std::error::Error + Send + Sync>)
        })
    }
}

/// Checked once, pinned once. DNS never enters Tokio's shared blocking pool,
/// where password Argon2/bcrypt jobs run. The dedicated thread owns its permit
/// until the resolver actually returns, even when its async waiter is cancelled.
async fn resolve_public(
    host: String,
    port: u16,
    slots: Arc<Semaphore>,
) -> Result<Vec<SocketAddr>, RelayError> {
    run_lookup(slots, move || {
        let addresses = (host.as_str(), port)
            .to_socket_addrs()
            .map_err(|_| RelayError::UpstreamConnection)?;
        checked_addresses(addresses)
    })
    .await
}

/// This private dependency boundary lets tests block only an owned lookup, not
/// the OS resolver or other processes. Production passes only getaddrinfo and
/// address validation. There is no work queue: exhausted capacity fails closed.
async fn run_lookup<F>(slots: Arc<Semaphore>, lookup: F) -> Result<Vec<SocketAddr>, RelayError>
where
    F: FnOnce() -> Result<Vec<SocketAddr>, RelayError> + Send + 'static,
{
    let permit = slots.try_acquire_owned().map_err(|_| RelayError::Busy)?;
    let (sender, receiver) = oneshot::channel();
    // Builder returns an error instead of panicking on OS thread exhaustion.
    // Dropping a join handle detaches this bounded, credential-free lookup;
    // cancellation drops only the receiver, not the thread's owned permit.
    std::thread::Builder::new()
        .name("lmm-honeypot-dns".into())
        .spawn(move || {
            let _permit = permit;
            let _ = sender.send(lookup());
        })
        .map_err(|_| RelayError::UpstreamConnection)?;
    receiver.await.map_err(|_| RelayError::UpstreamConnection)?
}

fn checked_addresses(
    addresses: impl IntoIterator<Item = SocketAddr>,
) -> Result<Vec<SocketAddr>, RelayError> {
    let mut checked = BTreeSet::new();
    // Reject a mixed safe/unsafe answer completely, rather than accidentally
    // retaining a private fallback or using a second resolver later.
    for (index, address) in addresses.into_iter().enumerate() {
        if index >= MAX_DNS_ADDRESSES || !public_address(address.ip()) {
            return Err(RelayError::RouteUnavailable);
        }
        checked.insert(SocketAddr::new(address.ip().to_canonical(), address.port()));
    }
    if checked.is_empty() {
        return Err(RelayError::RouteUnavailable);
    }
    Ok(checked.into_iter().collect())
}

impl NetworkTarget {
    pub(crate) async fn prepare(
        &self,
        url: &Url,
        limits: &Limits,
        dns_slots: Arc<Semaphore>,
    ) -> Result<PreparedConnection, RelayError> {
        // Recheck the complete operation URL's invariant without accepting a
        // client/configured query. Gemini's only query was server-derived.
        let mut base = url.clone();
        base.set_query(None);
        validate_base_url(base.as_str(), self.local_http)?;
        if !self.allowed_origins.contains(&canonical_origin(&base)) {
            return Err(RelayError::RouteUnavailable);
        }
        let host = url.host_str().ok_or(RelayError::RouteUnavailable)?;
        let port = url
            .port_or_known_default()
            .ok_or(RelayError::RouteUnavailable)?;
        let addresses = if let Some(ip) = literal_ip(url) {
            vec![SocketAddr::new(ip.to_canonical(), port)]
        } else {
            resolve_public(host.to_owned(), port, dns_slots).await?
        };
        let mut builder = client_builder(limits).dns_resolver(Arc::new(RejectDns));
        if !literal_ip(url).is_some_and(|ip| ip.is_loopback()) {
            builder = builder.https_only(true);
        }
        if let Some(root) = &self.additional_root {
            builder = builder.add_root_certificate(root.clone());
        }
        // The URL retains its original host for Host/SNI/certificate matching.
        // resolve_to_addrs overrides that host only at the connector boundary.
        builder = builder.resolve_to_addrs(host, &addresses);
        let client = builder.build().map_err(|_| RelayError::RouteUnavailable)?;
        Ok(PreparedConnection {
            client,
            peers: addresses.into_iter().collect(),
        })
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn reserved_addresses_and_ipv6_transition_ranges_are_rejected() {
        for address in [
            "0.0.0.0",
            "10.1.2.3",
            "100.64.0.1",
            "127.0.0.1",
            "169.254.169.254",
            "172.16.0.1",
            "192.0.0.9",
            "192.88.99.1",
            "192.168.1.1",
            "192.0.2.1",
            "198.18.0.1",
            "198.51.100.1",
            "203.0.113.1",
            "224.0.0.1",
            "240.0.0.1",
            "255.255.255.255",
            "::",
            "::1",
            "::ffff:8.8.8.8",
            "::ffff:127.0.0.1",
            "64:ff9b::808:808",
            "64:ff9b:1::1",
            "100::1",
            "2001::1",
            "2001:1::1",
            "2001:db8::1",
            "2002::1",
            "3fff::1",
            "3ffe::1",
            "3e00::1",
            "5f00::1",
            "fc00::1",
            "fe80::1",
            "ff02::1",
        ] {
            assert!(!public_address(address.parse().unwrap()), "{address}");
        }
        for address in [
            "1.1.1.1",
            "8.8.8.8",
            "2001:4860:4860::8888",
            "2606:4700:4700::1111",
            "2a00:1450:4001::1",
        ] {
            assert!(public_address(address.parse().unwrap()), "{address}");
        }
    }
    #[test]
    fn url_and_origin_rules_are_canonical_and_fail_closed() {
        assert_eq!(
            validate_origin("https://Example.COM:443/", false).unwrap(),
            "https://example.com"
        );
        assert!(validate_base_url("https://example.com/prefix/v1", false).is_ok());
        assert!(validate_origin("https://example.com/v1", false).is_err());
        for url in [
            "http://example.com/v1",
            "https://u:p@example.com/v1",
            "https://example.com/v1?",
            "https://example.com/v1#",
            "https://example.com/v1\n",
            "https://example.com\\evil/v1",
            "https://127.0.0.1/v1",
            "https://[::1]/v1",
            "https://2130706433/v1",
            "https://169.254.169.254/v1",
            "https://[::ffff:127.0.0.1]/v1",
        ] {
            assert!(validate_base_url(url, false).is_err(), "{url}");
        }
        assert!(validate_base_url("http://127.0.0.1:54321/v1", true).is_ok());
        assert!(validate_base_url("https://[::1]:54321/v1", true).is_ok());
        assert!(validate_base_url("http://localhost/v1", true).is_err());
    }
    #[test]
    fn mixed_or_empty_dns_answers_and_overlarge_sets_are_rejected() {
        let safe: SocketAddr = "8.8.8.8:443".parse().unwrap();
        let private: SocketAddr = "127.0.0.1:443".parse().unwrap();
        assert_eq!(checked_addresses([safe]).unwrap(), vec![safe]);
        assert!(checked_addresses([safe, private]).is_err());
        assert!(checked_addresses([]).is_err());
        assert!(checked_addresses(std::iter::repeat_n(safe, MAX_DNS_ADDRESSES + 1)).is_err());
    }
    #[test]
    fn configured_upstream_is_bound_before_credential_material_enters_transport() {
        use crate::relay::{UpstreamProtocol, honeypot::HoneypotUpstream};
        let network = NetworkTarget {
            additional_root: None,
            local_http: false,
            allowed_origins: ["https://upstream.example".to_owned()].into(),
        };
        for base in [
            "https://evil.example/v1",
            "https://upstream.example:444/v1",
            "http://upstream.example/v1",
            "https://127.0.0.1/v1",
        ] {
            assert!(
                HoneypotUpstream::configured(
                    UpstreamProtocol::Chat,
                    "synthetic".into(),
                    base,
                    "isolated-synthetic",
                    false,
                    network.clone(),
                )
                .is_err(),
                "{base}"
            );
        }
        for protocol in [
            UpstreamProtocol::Chat,
            UpstreamProtocol::Responses,
            UpstreamProtocol::Anthropic,
            UpstreamProtocol::Gemini,
        ] {
            for stream in [false, true] {
                assert!(
                    HoneypotUpstream::configured(
                        protocol,
                        "synthetic".into(),
                        "https://upstream.example/prefix/v1",
                        "isolated-synthetic",
                        stream,
                        network.clone(),
                    )
                    .is_ok()
                );
            }
        }
    }

    #[tokio::test]
    async fn dns_names_cannot_become_loopback_even_in_local_mode() {
        let network = NetworkTarget {
            additional_root: None,
            local_http: true,
            allowed_origins: ["https://localhost:1".to_owned()].into(),
        };
        let url = Url::parse("https://localhost:1/v1/responses").unwrap();
        assert!(
            network
                .prepare(&url, &Limits::default(), Arc::new(Semaphore::new(1)))
                .await
                .is_err()
        );
    }
}
