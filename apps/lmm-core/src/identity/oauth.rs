//! Google authorization-code login. Provider credentials and endpoints are
//! server-owned; no caller may supply an issuer, token URL, key URL or redirect.
use super::*;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use jsonwebtoken::{Algorithm, DecodingKey, Validation, decode, decode_header};
use reqwest::{Client, Url, redirect::Policy};
use serde::Deserialize;
use std::time::{SystemTime, UNIX_EPOCH};
use subtle::ConstantTimeEq;

const ISSUER: &str = "https://accounts.google.com";
const CALLBACK_PATH: &str = "/core/v1/auth/oauth/google/callback";
const MAX_PROVIDER_BODY: usize = 65_536;

/// No Debug implementation: this contains a client secret.
pub struct GoogleOAuth {
    client_id: String,
    client_secret: String,
    redirect_uri: String,
    client: Client,
    token_endpoint: Url,
    keys_endpoint: Url,
}
impl GoogleOAuth {
    pub fn new(client_id: String, client_secret: String, redirect_uri: String) -> Result<Self> {
        if client_id.is_empty()
            || client_id.len() > 512
            || client_secret.is_empty()
            || client_secret.len() > 4096
            || redirect_uri.len() > 2048
        {
            return Err(IdentityError::Invalid);
        }
        let redirect = Url::parse(&redirect_uri).map_err(|_| IdentityError::Invalid)?;
        if redirect.scheme() != "https"
            || redirect.host_str().is_none()
            || !redirect.username().is_empty()
            || redirect.password().is_some()
            || redirect.query().is_some()
            || redirect.fragment().is_some()
            || redirect.path() != CALLBACK_PATH
        {
            return Err(IdentityError::Invalid);
        }
        let client = Client::builder()
            .https_only(true)
            .redirect(Policy::none())
            .connect_timeout(Duration::from_secs(3))
            .timeout(Duration::from_secs(10))
            .build()
            .map_err(|_| IdentityError::Storage)?;
        Ok(Self {
            client_id,
            client_secret,
            redirect_uri,
            client,
            token_endpoint: Url::parse("https://oauth2.googleapis.com/token")
                .map_err(|_| IdentityError::Storage)?,
            keys_endpoint: Url::parse("https://www.googleapis.com/oauth2/v3/certs")
                .map_err(|_| IdentityError::Storage)?,
        })
    }
}
#[derive(Deserialize)]
struct TokenReply {
    id_token: String,
}
#[derive(Deserialize)]
struct KeySet {
    keys: Vec<PublicKey>,
}
#[derive(Deserialize)]
struct PublicKey {
    kid: String,
    kty: String,
    n: String,
    e: String,
    alg: Option<String>,
    #[serde(rename = "use")]
    usage: Option<String>,
}
#[derive(Deserialize)]
struct Claims {
    sub: String,
    aud: String,
    nonce: String,
    exp: u64,
    iat: u64,
    azp: Option<String>,
}
fn now() -> Result<u64> {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|d| d.as_secs())
        .map_err(|_| IdentityError::Storage)
}
fn verify_token(
    token: &str,
    keys: &KeySet,
    expected_nonce: &[u8],
    client_id: &str,
) -> Result<Claims> {
    if token.len() > 16_384 || keys.keys.len() > 32 {
        return Err(IdentityError::Unauthorized);
    }
    let header = decode_header(token).map_err(|_| IdentityError::Unauthorized)?;
    if header.alg != Algorithm::RS256 {
        return Err(IdentityError::Unauthorized);
    }
    let kid = header.kid.ok_or(IdentityError::Unauthorized)?;
    let mut matching = keys.keys.iter().filter(|k| k.kid == kid);
    let key = matching.next().ok_or(IdentityError::Unauthorized)?;
    if matching.next().is_some()
        || key.kty != "RSA"
        || key.n.len() > 8192
        || key.e.len() > 16
        || key.alg.as_deref().is_some_and(|a| a != "RS256")
        || key.usage.as_deref().is_some_and(|u| u != "sig")
    {
        return Err(IdentityError::Unauthorized);
    }
    let key = DecodingKey::from_rsa_components(&key.n, &key.e)
        .map_err(|_| IdentityError::Unauthorized)?;
    let mut validation = Validation::new(Algorithm::RS256);
    validation.set_audience(&[client_id]);
    validation.set_issuer(&[ISSUER, "accounts.google.com"]);
    validation.set_required_spec_claims(&["iss", "sub", "aud", "exp"]);
    validation.validate_nbf = true;
    validation.leeway = 60;
    let claims = decode::<Claims>(token, &key, &validation)
        .map_err(|_| IdentityError::Unauthorized)?
        .claims;
    let nonce = digest(&claims.nonce)?;
    let current = now()?;
    if claims.sub.is_empty()
        || claims.sub.len() > 255
        || claims.sub.chars().any(char::is_control)
        || claims.aud != client_id
        || claims.azp.as_deref().is_some_and(|a| a != client_id)
        || claims.iat > current.saturating_add(60)
        || claims.iat >= claims.exp
        || !bool::from(nonce.as_slice().ct_eq(expected_nonce))
    {
        return Err(IdentityError::Unauthorized);
    }
    Ok(claims)
}
async fn bounded_json<T: serde::de::DeserializeOwned>(
    mut response: reqwest::Response,
) -> Result<T> {
    if !response.status().is_success() {
        return Err(IdentityError::Unauthorized);
    }
    if response
        .content_length()
        .is_some_and(|n| n > MAX_PROVIDER_BODY as u64)
    {
        return Err(IdentityError::Unauthorized);
    }
    let mut bytes = Vec::new();
    while let Some(chunk) = response.chunk().await.map_err(|_| IdentityError::Storage)? {
        if bytes.len().saturating_add(chunk.len()) > MAX_PROVIDER_BODY {
            return Err(IdentityError::Unauthorized);
        }
        bytes.extend_from_slice(&chunk);
    }
    serde_json::from_slice(&bytes).map_err(|_| IdentityError::Unauthorized)
}
impl IdentityStore {
    pub async fn begin_google_login(&self, browser_binding: &str) -> Result<String> {
        let config = self.google.as_ref().ok_or(IdentityError::Forbidden)?;
        let binding = digest(browser_binding)?;
        let state = new_secret("lmmo_")?;
        let nonce = new_secret("")?;
        let verifier = new_secret("")?;
        let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()));
        sqlx::query("DELETE FROM core_identity.oauth_flows WHERE state_digest IN (SELECT state_digest FROM core_identity.oauth_flows WHERE expires_at<=clock_timestamp() LIMIT 100)")
            .execute(&self.pool).await?;
        sqlx::query("INSERT INTO core_identity.oauth_flows(state_digest,binding_digest,nonce_digest,pkce_verifier) VALUES ($1,$2,$3,$4)")
            .bind(digest(&state)?).bind(binding).bind(digest(&nonce)?).bind(verifier).execute(&self.pool).await?;
        let mut url = Url::parse("https://accounts.google.com/o/oauth2/v2/auth")
            .map_err(|_| IdentityError::Storage)?;
        url.query_pairs_mut().extend_pairs([
            ("client_id", config.client_id.as_str()),
            ("redirect_uri", config.redirect_uri.as_str()),
            ("response_type", "code"),
            ("scope", "openid"),
            ("state", &state),
            ("nonce", &nonce),
            ("code_challenge", &challenge),
            ("code_challenge_method", "S256"),
        ]);
        Ok(url.into())
    }
    /// The binding is held in an HttpOnly host cookie, not in the redirect URL.
    /// Consume before the network request: retries and parallel callbacks cannot
    /// exchange a state twice, even when the provider fails or the core restarts.
    pub async fn finish_google_login(
        &self,
        state: &str,
        browser_binding: &str,
        code: &str,
    ) -> Result<IssuedCredential> {
        let config = self.google.as_ref().ok_or(IdentityError::Forbidden)?;
        if code.is_empty() || code.len() > 4096 || code.chars().any(char::is_control) {
            return Err(IdentityError::Unauthorized);
        }
        let flow = self.take_google_flow(state, browser_binding).await?;
        let verifier: String = flow.try_get("pkce_verifier")?;
        let nonce: Vec<u8> = flow.try_get("nonce_digest")?;
        let response = config
            .client
            .post(config.token_endpoint.clone())
            .form(&[
                ("client_id", config.client_id.as_str()),
                ("client_secret", config.client_secret.as_str()),
                ("redirect_uri", config.redirect_uri.as_str()),
                ("grant_type", "authorization_code"),
                ("code", code),
                ("code_verifier", verifier.as_str()),
            ])
            .send()
            .await
            .map_err(|_| IdentityError::Storage)?;
        let tokens: TokenReply = bounded_json(response).await?;
        let response = config
            .client
            .get(config.keys_endpoint.clone())
            .send()
            .await
            .map_err(|_| IdentityError::Storage)?;
        let keys: KeySet = bounded_json(response).await?;
        let claims = verify_token(&tokens.id_token, &keys, &nonce, &config.client_id)?;
        let mut tx = self.pool.begin().await?;
        auth::lock_registration(&mut tx).await?;
        let existing: Option<i64> = sqlx::query_scalar(
            "SELECT user_id FROM core_identity.oauth_identities WHERE issuer=$1 AND subject=$2",
        )
        .bind(ISSUER)
        .bind(&claims.sub)
        .fetch_optional(&mut *tx)
        .await?;
        let user_id = match existing {
            Some(id) => id,
            None => {
                if !self.registration_enabled {
                    return Err(IdentityError::Forbidden);
                }
                let id = auth::create_user(&mut tx).await?;
                sqlx::query("INSERT INTO core_identity.oauth_identities(issuer,subject,user_id) VALUES ($1,$2,$3)")
                    .bind(ISSUER).bind(&claims.sub).bind(id).execute(&mut *tx).await?;
                id
            }
        };
        let issued = auth::issue_session(&mut tx, user_id, "session.google_login").await?;
        tx.commit().await?;
        Ok(issued)
    }
    async fn take_google_flow(&self, state: &str, binding: &str) -> Result<PgRow> {
        sqlx::query("DELETE FROM core_identity.oauth_flows WHERE state_digest=$1 AND binding_digest=$2 AND expires_at>clock_timestamp() RETURNING nonce_digest,pkce_verifier")
            .bind(digest(state)?).bind(digest(binding)?).fetch_optional(&self.pool).await?
            .ok_or(IdentityError::Unauthorized)
    }
}

#[cfg(test)]
mod tests;
