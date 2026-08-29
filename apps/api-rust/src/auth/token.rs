use super::{AuthError, AuthErrorKind, ACCESS_TOKEN_TTL_SECONDS};
use base64::{engine::general_purpose::URL_SAFE_NO_PAD, Engine};
use hmac::{Hmac, Mac};
use jsonwebtoken::{
    decode, encode, errors::ErrorKind, Algorithm, DecodingKey, EncodingKey, Header, Validation,
};
use rand::{distr::Alphanumeric, Rng};
use secrecy::{ExposeSecret, SecretSlice, SecretString};
use serde::{Deserialize, Serialize};
use sha2::Sha256;
use std::time::UNIX_EPOCH;
use subtle::ConstantTimeEq;

type HmacSha256 = Hmac<Sha256>;

const ISSUER: &str = "new-api";
const AUDIENCE: &str = "new-api-dashboard";
const TOKEN_USE: &str = "access";
const SECURITY_PROOF_TOKEN_USE: &str = "security_proof";
const SECURITY_PROOF_TTL_SECONDS: i64 = 5 * 60;

#[derive(Clone, Debug, Eq, PartialEq)]
pub(super) struct AuthIdentity {
    pub user_id: i64,
    pub session_id: String,
    pub user_auth_version: i64,
    pub session_version: i64,
}

#[derive(Clone, Debug, Serialize, Deserialize)]
struct Claims {
    token_use: String,
    sid: String,
    uv: i64,
    sv: i64,
    iss: String,
    sub: String,
    aud: Vec<String>,
    exp: i64,
    nbf: i64,
    iat: i64,
    jti: String,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    method: Option<String>,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    scopes: Option<Vec<String>>,
}

pub(super) struct LegacyTokenCodec {
    access_key: SecretSlice<u8>,
    security_proof_key: SecretSlice<u8>,
    auth_flow_key: SecretSlice<u8>,
    refresh_key: SecretSlice<u8>,
    refresh_rotate_key: SecretSlice<u8>,
}

impl LegacyTokenCodec {
    pub fn new(session_secret: SecretString) -> Result<Self, AuthError> {
        let secret = session_secret.expose_secret().as_bytes();
        if !strong_session_secret(session_secret.expose_secret()) {
            return Err(AuthError::new(AuthErrorKind::Internal));
        }
        Ok(Self {
            access_key: SecretSlice::from(derive_key(secret, "access")?),
            security_proof_key: SecretSlice::from(derive_key(secret, "security_proof")?),
            auth_flow_key: SecretSlice::from(
                format!("auth-flow-v1:{}", session_secret.expose_secret()).into_bytes(),
            ),
            refresh_key: SecretSlice::from(derive_key(secret, "refresh")?),
            refresh_rotate_key: SecretSlice::from(derive_key(secret, "refresh-rotate")?),
        })
    }

    fn validate_identity(identity: &AuthIdentity) -> Result<(), AuthError> {
        if identity.user_id <= 0
            || identity.session_id.is_empty()
            || identity.user_auth_version <= 0
            || identity.session_version <= 0
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(())
    }

    fn claims(
        identity: &AuthIdentity,
        token_use: &str,
        now: i64,
        expires_at: i64,
        method: Option<String>,
        scopes: Option<Vec<String>>,
    ) -> Claims {
        Claims {
            token_use: token_use.to_owned(),
            sid: identity.session_id.clone(),
            uv: identity.user_auth_version,
            sv: identity.session_version,
            iss: ISSUER.to_owned(),
            sub: identity.user_id.to_string(),
            aud: vec![AUDIENCE.to_owned()],
            exp: expires_at,
            nbf: now - 5,
            iat: now,
            jti: uuid::Uuid::new_v4().to_string(),
            method,
            scopes,
        }
    }

    fn decode_claims(raw: &str, key: &[u8], required_claims: &[&str]) -> Result<Claims, AuthError> {
        let mut validation = Validation::new(Algorithm::HS256);
        validation.leeway = 5;
        validation.validate_nbf = true;
        validation.set_audience(&[AUDIENCE]);
        validation.set_issuer(&[ISSUER]);
        validation.set_required_spec_claims(required_claims);
        decode::<Claims>(raw, &DecodingKey::from_secret(key), &validation)
            .map(|data| data.claims)
            .map_err(|error| match error.kind() {
                ErrorKind::ExpiredSignature => AuthError::new(AuthErrorKind::TokenExpired),
                _ => AuthError::new(AuthErrorKind::Unauthorized),
            })
    }

    pub fn issue(&self, identity: &AuthIdentity) -> Result<(String, i64), AuthError> {
        Self::validate_identity(identity)?;
        let now = unix_now();
        let expires_at = now + ACCESS_TOKEN_TTL_SECONDS;
        let claims = Self::claims(identity, TOKEN_USE, now, expires_at, None, None);
        let token = encode(
            &Header::new(Algorithm::HS256),
            &claims,
            &EncodingKey::from_secret(self.access_key.expose_secret()),
        )
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
        Ok((token, expires_at))
    }

    pub fn parse(&self, raw: &SecretString) -> Result<AuthIdentity, AuthError> {
        let claims = Self::decode_claims(
            raw.expose_secret(),
            self.access_key.expose_secret(),
            &["exp", "nbf", "iss", "aud", "sub"],
        )?;
        let user_id = claims
            .sub
            .parse::<i64>()
            .map_err(|_| AuthError::new(AuthErrorKind::Unauthorized))?;
        if claims.token_use != TOKEN_USE
            || user_id <= 0
            || claims.sid.is_empty()
            || claims.uv <= 0
            || claims.sv <= 0
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(AuthIdentity {
            user_id,
            session_id: claims.sid,
            user_auth_version: claims.uv,
            session_version: claims.sv,
        })
    }

    /// Issues the Go-compatible short-lived proof used by sensitive dashboard
    /// operations. The caller must already have validated the live session;
    /// this codec only signs the server-derived identity and requested scope.
    pub fn issue_security_proof(
        &self,
        identity: &AuthIdentity,
        method: &str,
        scopes: &[String],
    ) -> Result<(String, i64), AuthError> {
        Self::validate_identity(identity)?;
        if method.trim().is_empty()
            || scopes.is_empty()
            || scopes.iter().any(|scope| scope.trim().is_empty())
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let now = unix_now();
        let expires_at = now + SECURITY_PROOF_TTL_SECONDS;
        let claims = Self::claims(
            identity,
            SECURITY_PROOF_TOKEN_USE,
            now,
            expires_at,
            Some(method.trim().to_owned()),
            Some(scopes.iter().map(|scope| scope.trim().to_owned()).collect()),
        );
        let token = encode(
            &Header::new(Algorithm::HS256),
            &claims,
            &EncodingKey::from_secret(self.security_proof_key()),
        )
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
        Ok((token, expires_at))
    }

    /// Validates a Go-compatible security proof against one live dashboard
    /// identity. Empty `allowed_methods` retains Go's allow-all convention;
    /// scope and method comparisons are constant-time.
    pub fn verify_security_proof(
        &self,
        raw: &SecretString,
        identity: &AuthIdentity,
        required_scope: &str,
        allowed_methods: &[String],
    ) -> Result<String, AuthError> {
        let claims = Self::decode_claims(
            raw.expose_secret().trim(),
            self.security_proof_key(),
            &["exp", "nbf", "iss", "aud", "sub", "iat"],
        )?;
        let user_id = claims
            .sub
            .parse::<i64>()
            .map_err(|_| AuthError::new(AuthErrorKind::Unauthorized))?;
        if claims.token_use != SECURITY_PROOF_TOKEN_USE
            || claims.jti.is_empty()
            || claims.sid.is_empty()
            || claims.uv <= 0
            || claims.sv <= 0
            || user_id != identity.user_id
            || claims.sid != identity.session_id
            || claims.uv != identity.user_auth_version
            || claims.sv != identity.session_version
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let method = claims
            .method
            .ok_or_else(|| AuthError::new(AuthErrorKind::Unauthorized))?;
        if !allowed_methods.is_empty()
            && !allowed_methods
                .iter()
                .any(|allowed| method.as_bytes().ct_eq(allowed.as_bytes()).into())
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        let scopes = claims
            .scopes
            .ok_or_else(|| AuthError::new(AuthErrorKind::Unauthorized))?;
        if !required_scope.is_empty()
            && !scopes
                .iter()
                .any(|scope| scope.as_bytes().ct_eq(required_scope.as_bytes()).into())
        {
            return Err(AuthError::new(AuthErrorKind::Unauthorized));
        }
        Ok(method)
    }

    fn security_proof_key(&self) -> &[u8] {
        // The proof key is derived from the same session secret namespace as
        // Go's authSigningKey("security_proof"). Keep it separate from the
        // access-token key so token-use confusion cannot cross the boundary.
        self.security_proof_key.expose_secret()
    }

    /// Returns true only for a credential that declares itself to be one of
    /// this application's dashboard JWTs.  This deliberately examines the
    /// unverified envelope first, matching Go's `ParseDashboardAccessToken`:
    /// a token with our issuer, audience and a known token-use is *internal*
    /// even if it is expired, revoked, or has an invalid signature.  Such a
    /// token must never fall through to the opaque personal-access-token
    /// lookup.
    pub fn is_dashboard_token_candidate(&self, raw: &SecretString) -> bool {
        dashboard_token_candidate(raw.expose_secret())
    }

    pub fn hash_refresh(&self, secret: &SecretString) -> Result<String, AuthError> {
        hmac_hex(
            self.refresh_key.expose_secret(),
            secret.expose_secret().as_bytes(),
        )
    }

    pub fn hash_auth_flow(&self, token: &SecretString) -> Result<String, AuthError> {
        hmac_hex(
            self.auth_flow_key.expose_secret(),
            token.expose_secret().as_bytes(),
        )
    }

    pub fn derive_next_refresh(
        &self,
        sid: &str,
        current: &SecretString,
    ) -> Result<SecretString, AuthError> {
        let data = format!("{sid}.{}", current.expose_secret());
        hmac_hex(self.refresh_rotate_key.expose_secret(), data.as_bytes()).map(SecretString::from)
    }
}

/// Classifies a credential using the same unverified JWT envelope boundary as
/// Go's `ParseUnverified`: the header must itself be a valid JWT header before
/// claims can make a token dashboard-looking.  Signature and time validity are
/// intentionally deferred to `parse`/session validation.
pub(crate) fn dashboard_token_candidate(raw: &str) -> bool {
    let raw = raw.trim();
    if raw.is_empty() {
        return false;
    }
    let mut segments = raw.split('.');
    let (Some(header), Some(payload), Some(_signature), None) = (
        segments.next(),
        segments.next(),
        segments.next(),
        segments.next(),
    ) else {
        return false;
    };
    let Ok(header_bytes) = URL_SAFE_NO_PAD.decode(header) else {
        return false;
    };
    let Ok(header_json) = serde_json::from_slice::<serde_json::Value>(&header_bytes) else {
        return false;
    };
    let Some(algorithm) = header_json.get("alg").and_then(serde_json::Value::as_str) else {
        return false;
    };
    // This is deliberately the frozen Go jwt/v5 registration set.  The
    // envelope classifier must recognize every registered algorithm (and
    // `none`) before signature verification, otherwise a malformed internal
    // JWT can incorrectly fall through to opaque PAT/anonymous handling.
    if !go_registered_algorithm(algorithm) {
        return false;
    }
    let Ok(payload) = URL_SAFE_NO_PAD.decode(payload) else {
        return false;
    };
    let Ok(claims) = serde_json::from_slice::<serde_json::Value>(&payload) else {
        return false;
    };
    let audience_matches = claims.get("aud").is_some_and(|audience| match audience {
        serde_json::Value::String(audience) => audience == AUDIENCE,
        serde_json::Value::Array(audiences) => audiences
            .iter()
            .any(|audience| audience.as_str() == Some(AUDIENCE)),
        _ => false,
    });
    claims.get("iss").and_then(serde_json::Value::as_str) == Some(ISSUER)
        && audience_matches
        && matches!(
            claims.get("token_use").and_then(serde_json::Value::as_str),
            Some(TOKEN_USE | SECURITY_PROOF_TOKEN_USE)
        )
}

fn go_registered_algorithm(algorithm: &str) -> bool {
    matches!(
        algorithm,
        "none"
            | "HS256"
            | "HS384"
            | "HS512"
            | "RS256"
            | "RS384"
            | "RS512"
            | "PS256"
            | "PS384"
            | "PS512"
            | "ES256"
            | "ES384"
            | "ES512"
            | "EdDSA"
    )
}

fn strong_session_secret(secret: &str) -> bool {
    let secret = secret.trim();
    if secret.len() < 32 || secret.eq_ignore_ascii_case("random_string") {
        return false;
    }
    let classes = [
        secret.bytes().any(|byte| byte.is_ascii_lowercase()),
        secret.bytes().any(|byte| byte.is_ascii_uppercase()),
        secret.bytes().any(|byte| byte.is_ascii_digit()),
        secret
            .bytes()
            .any(|byte| !byte.is_ascii_alphanumeric() && !byte.is_ascii_whitespace()),
    ];
    classes.into_iter().filter(|present| *present).count() >= 3
}

pub(super) fn random_refresh_secret() -> SecretString {
    SecretString::from(
        rand::rng()
            .sample_iter(Alphanumeric)
            .take(64)
            .map(char::from)
            .collect::<String>(),
    )
}

pub(super) fn split_refresh_token(raw: &SecretString) -> Option<(String, SecretString)> {
    let (sid, secret) = raw.expose_secret().trim().split_once('.')?;
    if sid.is_empty()
        || secret.is_empty()
        || secret.contains('.')
        || uuid::Uuid::parse_str(sid).is_err()
    {
        return None;
    }
    Some((sid.to_owned(), SecretString::from(secret.to_owned())))
}

fn derive_key(session_secret: &[u8], purpose: &str) -> Result<Vec<u8>, AuthError> {
    let data = format!("new-api/auth/{purpose}/v1");
    let mut mac = HmacSha256::new_from_slice(session_secret)
        .map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
    mac.update(data.as_bytes());
    Ok(mac.finalize().into_bytes().to_vec())
}

fn hmac_hex(key: &[u8], data: &[u8]) -> Result<String, AuthError> {
    let mut mac =
        HmacSha256::new_from_slice(key).map_err(|_| AuthError::new(AuthErrorKind::Internal))?;
    mac.update(data);
    Ok(mac
        .finalize()
        .into_bytes()
        .iter()
        .map(|byte| format!("{byte:02x}"))
        .collect())
}

fn unix_now() -> i64 {
    UNIX_EPOCH
        .elapsed()
        .map_or(0, |elapsed| elapsed.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn identity() -> AuthIdentity {
        AuthIdentity {
            user_id: 42,
            session_id: uuid::Uuid::new_v4().to_string(),
            user_auth_version: 3,
            session_version: 2,
        }
    }

    #[test]
    fn access_token_round_trips_legacy_claims() -> TestResult {
        let codec = LegacyTokenCodec::new(SecretString::from(
            "0123456789abcdef-SESSION-SECRET!".to_owned(),
        ))?;
        let expected = identity();
        let (token, expires_at) = codec.issue(&expected)?;
        assert!(expires_at > unix_now());
        assert_eq!(codec.parse(&SecretString::from(token))?, expected);
        Ok(())
    }

    #[test]
    fn access_token_rejects_signature_tampering() -> TestResult {
        let codec = LegacyTokenCodec::new(SecretString::from(
            "0123456789abcdef-SESSION-SECRET!".to_owned(),
        ))?;
        let (mut token, _) = codec.issue(&identity())?;
        token.push('x');
        let error = codec
            .parse(&SecretString::from(token))
            .err()
            .ok_or_else(|| std::io::Error::other("tampered token was accepted"))?;
        assert_eq!(error.kind, AuthErrorKind::Unauthorized);
        Ok(())
    }

    #[test]
    fn security_proof_round_trips_with_session_scope_and_method_binding() -> TestResult {
        let codec = LegacyTokenCodec::new(SecretString::from(
            "0123456789abcdef-SESSION-SECRET!".to_owned(),
        ))?;
        let identity = identity();
        let scopes = vec!["channel.key.read".to_owned()];
        let (token, expires_at) = codec.issue_security_proof(&identity, "email", &scopes)?;
        assert!(expires_at > unix_now());
        assert_eq!(
            codec.verify_security_proof(
                &SecretString::from(token.clone()),
                &identity,
                "channel.key.read",
                &["email".to_owned()],
            )?,
            "email"
        );
        assert!(codec
            .verify_security_proof(
                &SecretString::from(token.clone()),
                &identity,
                "passkey.delete",
                &["email".to_owned()],
            )
            .is_err());
        assert!(codec
            .verify_security_proof(
                &SecretString::from(token),
                &identity,
                "channel.key.read",
                &["passkey".to_owned()],
            )
            .is_err());
        Ok(())
    }

    #[test]
    fn dashboard_jwt_candidates_never_be_reclassified_as_opaque_credentials() -> TestResult {
        let codec = LegacyTokenCodec::new(SecretString::from(
            "0123456789abcdef-SESSION-SECRET!".to_owned(),
        ))?;
        let (token, _) = codec.issue(&identity())?;
        let mut tampered = token.clone();
        tampered.push('x');
        assert!(codec.is_dashboard_token_candidate(&SecretString::from(token)));
        assert!(
            codec.is_dashboard_token_candidate(&SecretString::from(tampered)),
            "a signature-invalid dashboard JWT is still internal"
        );
        assert!(!codec
            .is_dashboard_token_candidate(&SecretString::from("opaque.key.with-dots".to_owned())));

        let expired = encode(
            &Header::new(Algorithm::HS256),
            &Claims {
                token_use: TOKEN_USE.to_owned(),
                sid: uuid::Uuid::new_v4().to_string(),
                uv: 1,
                sv: 1,
                iss: ISSUER.to_owned(),
                sub: "42".to_owned(),
                aud: vec![AUDIENCE.to_owned()],
                exp: 1,
                nbf: 0,
                iat: 0,
                jti: uuid::Uuid::new_v4().to_string(),
                method: None,
                scopes: None,
            },
            &EncodingKey::from_secret(codec.access_key.expose_secret()),
        )?;
        assert!(codec.is_dashboard_token_candidate(&SecretString::from(expired)));

        let encode_segment = |value: serde_json::Value| URL_SAFE_NO_PAD.encode(value.to_string());
        let es512 = format!(
            "{}.{}.signature",
            encode_segment(serde_json::json!({"alg": "ES512", "typ": "JWT"})),
            encode_segment(serde_json::json!({
                "iss": ISSUER,
                "aud": [AUDIENCE],
                "token_use": TOKEN_USE,
            }))
        );
        assert!(codec.is_dashboard_token_candidate(&SecretString::from(es512)));
        for algorithm in [
            "none", "HS256", "HS384", "HS512", "RS256", "RS384", "RS512", "PS256", "PS384",
            "PS512", "ES256", "ES384", "ES512", "EdDSA",
        ] {
            let candidate = format!(
                "{}.{}.signature",
                encode_segment(serde_json::json!({"alg": algorithm, "typ": "JWT"})),
                encode_segment(serde_json::json!({
                    "iss": ISSUER,
                    "aud": [AUDIENCE],
                    "token_use": TOKEN_USE,
                }))
            );
            assert!(
                codec.is_dashboard_token_candidate(&SecretString::from(candidate)),
                "registered Go algorithm {algorithm} must remain internal"
            );
        }
        let unknown_algorithm = format!(
            "{}.{}.signature",
            encode_segment(serde_json::json!({"alg": "HS1024", "typ": "JWT"})),
            encode_segment(serde_json::json!({
                "iss": ISSUER,
                "aud": [AUDIENCE],
                "token_use": TOKEN_USE,
            }))
        );
        assert!(!codec.is_dashboard_token_candidate(&SecretString::from(unknown_algorithm,)));
        assert!(
            !codec.is_dashboard_token_candidate(&SecretString::from(format!(
                "{}.{}.signature",
                encode_segment(serde_json::json!({"typ": "JWT"})),
                encode_segment(serde_json::json!({
                    "iss": ISSUER,
                    "aud": [AUDIENCE],
                    "token_use": TOKEN_USE,
                }))
            )))
        );
        Ok(())
    }

    #[test]
    fn refresh_derivation_is_deterministic_for_retry_recovery() -> TestResult {
        let codec = LegacyTokenCodec::new(SecretString::from(
            "0123456789abcdef-SESSION-SECRET!".to_owned(),
        ))?;
        let current = SecretString::from("current".to_owned());
        assert_eq!(
            codec.derive_next_refresh("sid", &current)?.expose_secret(),
            codec.derive_next_refresh("sid", &current)?.expose_secret()
        );
        Ok(())
    }

    #[test]
    fn session_secret_rejects_placeholder_and_weak_values() {
        for weak in [
            "",
            "random_string",
            "short",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        ] {
            assert!(
                LegacyTokenCodec::new(SecretString::from(weak.to_owned())).is_err(),
                "weak secret must be rejected: {weak}"
            );
        }
        assert!(LegacyTokenCodec::new(SecretString::from(
            "correct-horse-battery-staple-2026!".to_owned()
        ))
        .is_ok());
    }
}
