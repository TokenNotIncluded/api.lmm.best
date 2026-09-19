use super::{AuthError, CLIENT_ID, SCOPE};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::{RngCore, rngs::OsRng};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use std::collections::BTreeSet;
use url::{Host, Url};
use zeroize::{Zeroize, ZeroizeOnDrop};

pub(super) fn validate_issuer(issuer: &str) -> Result<(), AuthError> {
    let url = Url::parse(issuer).map_err(|_| AuthError::InvalidIssuer)?;
    if url.scheme() != "https"
        || !matches!(url.host(), Some(Host::Domain(_)))
        || !url.username().is_empty()
        || url.password().is_some()
        || url.port().is_some()
        || url.query().is_some()
        || url.fragment().is_some()
        || url.path() != "/"
        || url.origin().ascii_serialization() != issuer
    {
        return Err(AuthError::InvalidIssuer);
    }
    Ok(())
}

pub(super) fn validate_metadata(
    auth: &[u8],
    resource: &[u8],
    issuer: &str,
) -> Result<(), AuthError> {
    let auth: serde_json::Value =
        serde_json::from_slice(auth).map_err(|_| AuthError::InvalidResponse)?;
    let resource: serde_json::Value =
        serde_json::from_slice(resource).map_err(|_| AuthError::InvalidResponse)?;
    let expected = format!("{issuer}/api/oauth2");
    let contains = |field: &str, item: &str| {
        auth[field]
            .as_array()
            .is_some_and(|values| values.iter().any(|value| value.as_str() == Some(item)))
    };
    if auth["issuer"] != issuer
        || auth["authorization_endpoint"] != format!("{expected}/authorize")
        || auth["token_endpoint"] != format!("{expected}/token")
        || auth["revocation_endpoint"] != format!("{expected}/revoke")
        || auth["authorization_response_iss_parameter_supported"] != true
        || !contains("code_challenge_methods_supported", "S256")
        || !contains("response_types_supported", "code")
        || !contains("token_endpoint_auth_methods_supported", "none")
        || resource["resource"] != expected
        || resource["authorization_servers"] != serde_json::json!([issuer])
    {
        return Err(AuthError::InvalidResponse);
    }
    Ok(())
}

fn secret(prefix: &str, value: &str) -> bool {
    value.strip_prefix(prefix).is_some_and(|raw| {
        URL_SAFE_NO_PAD
            .decode(raw)
            .is_ok_and(|bytes| bytes.len() == 32 && URL_SAFE_NO_PAD.encode(bytes) == raw)
    })
}

fn scope_valid(scope: &str) -> bool {
    if scope.len() > 8192 {
        return false;
    }
    let parts: Vec<_> = scope.split(' ').collect();
    let unique: BTreeSet<_> = parts.iter().copied().collect();
    parts.len() == unique.len()
        && unique.contains("catalog:read")
        && unique.contains("balance:read")
        && unique.iter().any(|part| part.starts_with("group:"))
        && unique.iter().all(|part| {
            matches!(*part, "catalog:read" | "balance:read")
                || part.strip_prefix("group:").is_some_and(|encoded| {
                    URL_SAFE_NO_PAD.decode(encoded).is_ok_and(|bytes| {
                        !bytes.is_empty()
                            && bytes.len() <= 64
                            && URL_SAFE_NO_PAD.encode(&bytes) == encoded
                            && std::str::from_utf8(&bytes)
                                .is_ok_and(|group| !group.chars().any(char::is_control))
                    })
                })
        })
}

// No Debug implementations: credentials must never be included in ordinary diagnostics.
#[derive(Deserialize, Serialize, Zeroize, ZeroizeOnDrop)]
pub(super) struct Credential {
    pub issuer: String,
    pub access: String,
    pub refresh: String,
    pub scope: String,
    pub expires_at: u64,
}

impl Credential {
    pub fn valid_for(&self, issuer: &str) -> bool {
        self.issuer == issuer
            && secret("lmm_at_", &self.access)
            && secret("lmm_rt_", &self.refresh)
            && scope_valid(&self.scope)
            && self.expires_at > 0
    }
}

#[derive(Deserialize, Zeroize, ZeroizeOnDrop)]
pub(super) struct TokenResponse {
    access_token: String,
    refresh_token: String,
    token_type: String,
    expires_in: u64,
    scope: String,
}

impl TokenResponse {
    pub fn validate(&self, issuer: &str, started: u64) -> Result<Credential, AuthError> {
        if self.token_type != "Bearer" || !(1..=86400).contains(&self.expires_in) {
            return Err(AuthError::InvalidResponse);
        }
        let credential = Credential {
            issuer: issuer.to_owned(),
            access: self.access_token.clone(),
            refresh: self.refresh_token.clone(),
            scope: self.scope.clone(),
            expires_at: started.saturating_add(self.expires_in),
        };
        if !credential.valid_for(issuer) {
            return Err(AuthError::InvalidResponse);
        }
        Ok(credential)
    }
}

#[derive(Zeroize, ZeroizeOnDrop)]
pub(super) struct Handshake {
    pub state: String,
    pub verifier: String,
}

impl Handshake {
    pub fn new() -> Result<Self, AuthError> {
        let mut state = [0u8; 32];
        let mut verifier = [0u8; 32];
        OsRng
            .try_fill_bytes(&mut state)
            .map_err(|_| AuthError::Callback)?;
        OsRng
            .try_fill_bytes(&mut verifier)
            .map_err(|_| AuthError::Callback)?;
        let result = Self {
            state: URL_SAFE_NO_PAD.encode(state),
            verifier: URL_SAFE_NO_PAD.encode(verifier),
        };
        state.zeroize();
        verifier.zeroize();
        Ok(result)
    }

    pub fn authorization_url(&self, issuer: &str, redirect: &str) -> Result<Url, AuthError> {
        let mut url = Url::parse(&format!("{issuer}/api/oauth2/authorize"))
            .map_err(|_| AuthError::InvalidIssuer)?;
        url.query_pairs_mut().extend_pairs([
            ("response_type", "code"),
            ("client_id", CLIENT_ID),
            ("redirect_uri", redirect),
            ("scope", SCOPE),
            ("resource", &format!("{issuer}/api/oauth2")),
            (
                "code_challenge",
                &URL_SAFE_NO_PAD.encode(Sha256::digest(self.verifier.as_bytes())),
            ),
            ("code_challenge_method", "S256"),
            ("state", &self.state),
        ]);
        Ok(url)
    }
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used)]
    use super::*;

    #[test]
    fn discovery_rejects_mismatched_issuer_endpoints_and_missing_pkce() {
        let issuer = "https://api.lmm.best";
        let mut metadata = serde_json::json!({
            "issuer":issuer, "authorization_endpoint":format!("{issuer}/api/oauth2/authorize"),
            "token_endpoint":format!("{issuer}/api/oauth2/token"), "revocation_endpoint":format!("{issuer}/api/oauth2/revoke"),
            "authorization_response_iss_parameter_supported":true,"code_challenge_methods_supported":["S256"],
            "response_types_supported":["code"], "token_endpoint_auth_methods_supported":["none"]
        });
        let resource = serde_json::json!({"resource":format!("{issuer}/api/oauth2"),"authorization_servers":[issuer]}).to_string();
        assert!(
            validate_metadata(metadata.to_string().as_bytes(), resource.as_bytes(), issuer).is_ok()
        );
        metadata["token_endpoint"] = serde_json::json!("https://attacker.example/token");
        assert_eq!(
            validate_metadata(metadata.to_string().as_bytes(), resource.as_bytes(), issuer),
            Err(AuthError::InvalidResponse)
        );
        metadata["token_endpoint"] = serde_json::json!(format!("{issuer}/api/oauth2/token"));
        metadata["code_challenge_methods_supported"] = serde_json::json!(["plain"]);
        assert_eq!(
            validate_metadata(metadata.to_string().as_bytes(), resource.as_bytes(), issuer),
            Err(AuthError::InvalidResponse)
        );
    }

    #[test]
    fn issuer_rejects_redirect_and_credential_ambiguity() {
        for issuer in [
            "http://api.lmm.best",
            "https://api.lmm.best/",
            "https://api.lmm.best:443",
            "https://user@api.lmm.best",
            "https://127.0.0.1",
            "https://api.lmm.best?x",
            "https://api.lmm.best/path",
            "https://API.lmm.best",
        ] {
            assert_eq!(
                validate_issuer(issuer),
                Err(AuthError::InvalidIssuer),
                "{issuer}"
            );
        }
        assert!(validate_issuer("https://api.lmm.best").is_ok());
    }

    #[test]
    fn tokens_reject_invoke_scope_and_invalid_lifetime() {
        let mut response = TokenResponse {
            access_token: format!("lmm_at_{}", URL_SAFE_NO_PAD.encode([1u8; 32])),
            refresh_token: format!("lmm_rt_{}", URL_SAFE_NO_PAD.encode([2u8; 32])),
            token_type: "Bearer".into(),
            expires_in: 900,
            scope: "catalog:read balance:read group:ZGVmYXVsdA".into(),
        };
        assert!(response.validate("https://api.lmm.best", 100).is_ok());
        response.scope.push_str(" models:invoke");
        assert!(matches!(
            response.validate("https://api.lmm.best", 100),
            Err(AuthError::InvalidResponse)
        ));
        response.scope = "catalog:read balance:read group:ZGVmYXVsdA".into();
        response.expires_in = 0;
        assert!(matches!(
            response.validate("https://api.lmm.best", 100),
            Err(AuthError::InvalidResponse)
        ));
    }

    #[test]
    fn handshake_uses_distinct_entropy_and_s256_without_invocation_scope() {
        let handshake = Handshake::new().unwrap();
        assert_ne!(handshake.state, handshake.verifier);
        let url = handshake
            .authorization_url(
                "https://api.lmm.best",
                "http://127.0.0.1:12345/oauth/lmm/callback",
            )
            .unwrap();
        let query: std::collections::HashMap<_, _> = url.query_pairs().collect();
        assert_eq!(
            query["code_challenge"],
            URL_SAFE_NO_PAD.encode(Sha256::digest(handshake.verifier.as_bytes()))
        );
        assert_eq!(query["scope"], SCOPE);
        assert!(!url.as_str().contains(&handshake.verifier));
    }
}
