//! Public `lmm` client codec. Persisted authorization belongs to the issuer.
//! OAuth secrets are independent of the existing native credential generator.
use super::*;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::{RngCore, rngs::OsRng};
use serde::{Deserialize, Serialize};
use std::collections::BTreeSet;
use zeroize::Zeroizing;

pub(super) const CLIENT: &str = "lmm";
pub(super) const REQUESTED_SCOPE: &str = "catalog:read balance:read";
const MAX_GROUPS: usize = 64;

/// Configured at startup; never inferred from a request or forwarding header.
#[derive(Clone)]
pub struct LmmProfile {
    issuer: String,
    resource: String,
    groups: BTreeSet<String>,
}
impl LmmProfile {
    pub fn new(issuer: &str, groups: Vec<String>) -> Result<Self> {
        let url = reqwest::Url::parse(issuer).map_err(|_| IdentityError::Invalid)?;
        if issuer.len() > 256
            || url.scheme() != "https"
            || url.domain().is_none()
            || url.port().is_some()
            || !url.username().is_empty()
            || url.password().is_some()
            || url.path() != "/"
            || url.query().is_some()
            || url.fragment().is_some()
            || url.origin().ascii_serialization() != issuer
            || groups.is_empty()
            || groups.len() > MAX_GROUPS
        {
            return Err(IdentityError::Invalid);
        }
        let mut allowed = BTreeSet::new();
        for group in groups {
            group_scope(&group)?;
            if !allowed.insert(group) {
                return Err(IdentityError::Invalid);
            }
        }
        Ok(Self {
            issuer: issuer.into(),
            resource: format!("{issuer}/api/oauth2"),
            groups: allowed,
        })
    }
    pub(super) fn binding(&self, client: &str, resource: &str) -> Result<()> {
        if client != CLIENT || resource != self.resource {
            return Err(IdentityError::Unauthorized);
        }
        Ok(())
    }
    pub fn issuer(&self) -> &str {
        &self.issuer
    }
    pub fn resource(&self) -> &str {
        &self.resource
    }
    pub(super) fn allows_group(&self, group: &str) -> bool {
        self.groups.contains(group)
    }
    pub fn validate_authorization(
        &self,
        input: AuthorizationRequest,
    ) -> Result<AuthorizationRequest> {
        self.binding(&input.client_id, &input.resource)?;
        if input.response_type != "code"
            || input.code_challenge_method != "S256"
            || input.state.is_empty()
            || input.state.len() > 256
            || input.state.chars().any(char::is_control)
        {
            return Err(IdentityError::Invalid);
        }
        super::redirect(&input.redirect_uri)?;
        let mut scopes: Vec<_> = input.scope.split(' ').collect();
        scopes.sort_unstable();
        if scopes != ["balance:read", "catalog:read"] {
            return Err(IdentityError::Forbidden);
        }
        canonical(&input.code_challenge, "")?;
        Pkce::required()
            .challenge(
                Some("S256".into()),
                Some(input.code_challenge.clone().into()),
            )
            .map_err(|_| IdentityError::Invalid)?;
        Ok(input)
    }
}

/// Extractors must preserve duplicate/unknown-field errors; these are not maps.
/// No Debug: state, code verifier and credentials must not enter diagnostics.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct AuthorizationRequest {
    pub response_type: String,
    pub client_id: String,
    pub redirect_uri: String,
    pub scope: String,
    pub resource: String,
    pub code_challenge: String,
    pub code_challenge_method: String,
    pub state: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct TokenForm {
    pub grant_type: String,
    pub client_id: String,
    pub resource: String,
    pub code: Option<String>,
    pub redirect_uri: Option<String>,
    pub code_verifier: Option<String>,
    pub refresh_token: Option<String>,
    pub scope: Option<String>,
}
pub enum TokenRequest {
    Code {
        code: String,
        redirect: String,
        verifier: String,
    },
    Refresh {
        refresh: String,
        scope: Option<String>,
    },
}
impl TokenForm {
    pub fn validate(self, profile: &LmmProfile) -> Result<TokenRequest> {
        profile.binding(&self.client_id, &self.resource)?;
        match self.grant_type.as_str() {
            "authorization_code" if self.refresh_token.is_none() && self.scope.is_none() => {
                let code = self.code.ok_or(IdentityError::Invalid)?;
                let redirect = self.redirect_uri.ok_or(IdentityError::Invalid)?;
                let verifier = self.code_verifier.ok_or(IdentityError::Invalid)?;
                canonical(&code, "lmm_oc_")?;
                super::redirect(&redirect)?;
                if !(43..=128).contains(&verifier.len())
                    || !verifier
                        .bytes()
                        .all(|v| v.is_ascii_alphanumeric() || b"-._~".contains(&v))
                {
                    return Err(IdentityError::Invalid);
                }
                Ok(TokenRequest::Code {
                    code,
                    redirect,
                    verifier,
                })
            }
            "refresh_token"
                if self.code.is_none()
                    && self.redirect_uri.is_none()
                    && self.code_verifier.is_none() =>
            {
                let refresh = self.refresh_token.ok_or(IdentityError::Invalid)?;
                canonical(&refresh, "lmm_rt_")?;
                if self.scope.as_ref().is_some_and(|s| {
                    s.is_empty() || s.len() > 8192 || s.chars().any(char::is_control)
                }) {
                    return Err(IdentityError::Invalid);
                }
                Ok(TokenRequest::Refresh {
                    refresh,
                    scope: self.scope,
                })
            }
            _ => Err(IdentityError::Invalid),
        }
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct RevokeForm {
    pub client_id: String,
    pub token: String,
    pub token_type_hint: Option<String>,
}
impl RevokeForm {
    pub fn validate(&self) -> Result<()> {
        if self.client_id != CLIENT
            || self
                .token_type_hint
                .as_ref()
                .is_some_and(|s| s != "access_token" && s != "refresh_token")
        {
            return Err(IdentityError::Invalid);
        }
        if self.token.starts_with("lmm_at_") {
            canonical(&self.token, "lmm_at_")?;
        } else {
            canonical(&self.token, "lmm_rt_")?;
        }
        Ok(())
    }
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ContinueForm {
    pub csrf: String,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ConsentForm {
    pub csrf: String,
    pub decision: String,
}

#[derive(Serialize)]
pub struct TokenWire {
    pub access_token: String,
    pub refresh_token: String,
    pub token_type: &'static str,
    pub expires_in: i64,
    pub scope: String,
}
impl TokenWire {
    pub(super) fn from_bundle(bundle: TokenBundle) -> Result<Self> {
        canonical(&bundle.access_token, "lmm_at_")?;
        canonical(&bundle.refresh_token, "lmm_rt_")?;
        valid_ttl(bundle.expires_in, 86400)?;
        Ok(Self {
            access_token: bundle.access_token,
            refresh_token: bundle.refresh_token,
            token_type: "Bearer",
            expires_in: bundle.expires_in,
            scope: bundle.scopes.join(" "),
        })
    }
}

pub(super) fn canonical(value: &str, prefix: &str) -> Result<Vec<u8>> {
    let raw = value
        .strip_prefix(prefix)
        .ok_or(IdentityError::Unauthorized)?;
    if raw.len() != 43 {
        return Err(IdentityError::Unauthorized);
    }
    let decoded = URL_SAFE_NO_PAD
        .decode(raw)
        .map_err(|_| IdentityError::Unauthorized)?;
    if decoded.len() != 32 || URL_SAFE_NO_PAD.encode(&decoded) != raw {
        return Err(IdentityError::Unauthorized);
    }
    Ok(decoded)
}
pub(super) fn secret(prefix: &str) -> Result<String> {
    let mut bytes = Zeroizing::new([0u8; 32]);
    OsRng
        .try_fill_bytes(&mut *bytes)
        .map_err(|_| IdentityError::Storage)?;
    Ok(format!("{prefix}{}", URL_SAFE_NO_PAD.encode(*bytes)))
}
pub(super) fn group_scope(group: &str) -> Result<String> {
    if group.is_empty() || group.len() > 64 || group.chars().any(char::is_control) {
        return Err(IdentityError::Invalid);
    }
    Ok(format!(
        "group:{}",
        URL_SAFE_NO_PAD.encode(group.as_bytes())
    ))
}
pub(super) fn decode_group(value: &str) -> Result<String> {
    let raw = value.strip_prefix("group:").ok_or(IdentityError::Invalid)?;
    let bytes = URL_SAFE_NO_PAD
        .decode(raw)
        .map_err(|_| IdentityError::Invalid)?;
    if URL_SAFE_NO_PAD.encode(&bytes) != raw {
        return Err(IdentityError::Invalid);
    }
    let group = String::from_utf8(bytes).map_err(|_| IdentityError::Invalid)?;
    group_scope(&group)?;
    Ok(group)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn canonical_cli_tokens_and_group_scopes_reject_aliases() {
        let token = secret("lmm_at_").unwrap();
        assert_eq!(canonical(&token, "lmm_at_").unwrap().len(), 32);
        for bad in [
            format!("{token}="),
            format!("lmm_at_{}", "a".repeat(64)),
            token.replacen("lmm_at_", "lmms_", 1),
        ] {
            assert!(
                canonical(&bad, "lmm_at_").is_err(),
                "noncanonical token accepted"
            );
        }
        assert!(decode_group("group:ZGVmYXVsdA==").is_err());
        assert!(group_scope("").is_err());
        assert!(group_scope("bad\nname").is_err());
        for control in ['\u{7f}', '\u{80}', '\u{85}', '\u{9f}'] {
            assert!(group_scope(&control.to_string()).is_err());
        }
        for name in ["中文", "🦀"] {
            assert_eq!(decode_group(&group_scope(name).unwrap()).unwrap(), name);
        }
        assert_eq!(
            decode_group(&group_scope("default").unwrap()).unwrap(),
            "default"
        );
    }
    #[test]
    fn production_profile_rejects_issuer_downgrade_and_implicit_groups() {
        for issuer in [
            "http://api.lmm.best",
            "https://127.0.0.1",
            "https://api.lmm.best:443",
            "https://API.lmm.best",
            "https://api.lmm.best/",
        ] {
            assert!(
                LmmProfile::new(issuer, vec!["default".into()]).is_err(),
                "invalid issuer accepted"
            );
        }
        assert!(LmmProfile::new("https://api.lmm.best", vec![]).is_err());
        assert!(
            LmmProfile::new(
                "https://api.lmm.best",
                vec!["default".into(), "default".into()]
            )
            .is_err()
        );
        assert!(LmmProfile::new("https://api.lmm.best", vec!["default".into()]).is_ok());
    }
}
