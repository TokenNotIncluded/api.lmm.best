//! HumanGrant: separate key-bound AI authorization, never a replacement for
//! the browser login or an unrestricted LMM API key.
mod agent;
mod crypto;
mod human;

use crate::identity::{BrowserAuth, IdentityStore};
use axum::{
    Json, Router,
    body::to_bytes,
    extract::Request,
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::{RngCore, rngs::OsRng};
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::PgPool;

#[derive(Clone)]
struct HgState {
    pool: PgPool,
    browser: BrowserAuth,
    store: IdentityStore,
    origin: String,
}

pub fn router(pool: PgPool, store: IdentityStore, browser: BrowserAuth, origin: String) -> Router {
    let state = HgState {
        pool,
        store,
        browser,
        origin,
    };
    Router::new()
        .route("/v1/requests", post(agent::create))
        .route("/v1/requests/{id}", get(agent::status_json))
        .route("/v1/requests/{id}/status.md", get(agent::status_markdown))
        .route("/v1/requests/{id}/claim", post(agent::claim))
        .route("/v1/requests/{id}/cancel", post(agent::cancel))
        .route("/v1/requests/{id}/email", post(agent::email_unavailable))
        .route("/v1/session", get(agent::session))
        .route("/v1/humangrant/profile", get(agent::profile))
        .route("/human", get(human::landing_page))
        .route("/human/{link}", get(human::review_page))
        .route("/human/{link}/details", get(human::details))
        .route("/human/{link}/begin", post(human::begin))
        .route("/human/{link}/approve", post(human::approve))
        .route("/human/{link}/reject", post(human::reject))
        .route("/human/grants", get(human::grants_page))
        .route("/human-grant/review.js", get(human::script))
        .route("/human-grant/review.css", get(human::css))
        .route(
            "/api/humangrant/settings",
            get(human::settings).put(human::set_settings),
        )
        .route("/api/humangrant/grants", get(human::grants))
        .route("/api/humangrant/grants/{id}/revoke", post(human::revoke))
        .layer(axum::extract::DefaultBodyLimit::max(16 * 1024))
        .layer(middleware::from_fn(no_store))
        .with_state(state)
}

async fn no_store(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    for (name, value) in [
        (header::CACHE_CONTROL, "no-store"),
        (header::PRAGMA, "no-cache"),
        (header::REFERRER_POLICY, "no-referrer"),
        (header::X_CONTENT_TYPE_OPTIONS, "nosniff"),
    ] {
        response
            .headers_mut()
            .insert(name, HeaderValue::from_static(value));
    }
    response
}

#[derive(Debug)]
struct Error {
    status: StatusCode,
    code: &'static str,
}
impl Error {
    fn invalid() -> Self {
        Self {
            status: StatusCode::BAD_REQUEST,
            code: "invalid_request",
        }
    }
    fn unauthorized() -> Self {
        Self {
            status: StatusCode::UNAUTHORIZED,
            code: "invalid_signature",
        }
    }
    fn forbidden() -> Self {
        Self {
            status: StatusCode::FORBIDDEN,
            code: "forbidden",
        }
    }
    fn not_found() -> Self {
        Self {
            status: StatusCode::NOT_FOUND,
            code: "not_found",
        }
    }
    fn state() -> Self {
        Self {
            status: StatusCode::CONFLICT,
            code: "invalid_state",
        }
    }
    fn expired() -> Self {
        Self {
            status: StatusCode::GONE,
            code: "expired",
        }
    }
    fn internal() -> Self {
        Self {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "service_unavailable",
        }
    }
}
impl From<sqlx::Error> for Error {
    fn from(_: sqlx::Error) -> Self {
        Self::internal()
    }
}
impl IntoResponse for Error {
    fn into_response(self) -> Response {
        (self.status, Json(json!({"error":self.code,"message":"Request could not be completed","retry_after_seconds":0}))).into_response()
    }
}
type Result<T> = std::result::Result<T, Error>;

#[derive(Clone, Debug, Deserialize, Serialize, Eq, PartialEq)]
#[serde(deny_unknown_fields)]
struct Permission {
    action: String,
    resource: String,
}
fn permissions(input: &[Permission]) -> Result<()> {
    if input.is_empty()
        || input.len() > 16
        || input
            .iter()
            .any(|p| p.action != "lmm.profile.read" || p.resource != "self")
    {
        return Err(Error::invalid());
    }
    for (i, permission) in input.iter().enumerate() {
        if input[..i].contains(permission) {
            return Err(Error::invalid());
        }
    }
    Ok(())
}
fn token(size: usize) -> String {
    let mut bytes = vec![0; size];
    OsRng.fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}
fn digest(bytes: &[u8]) -> Vec<u8> {
    Sha256::digest(bytes).to_vec()
}
fn exact_header<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str> {
    let mut values = headers.get_all(name).iter();
    let first = values.next().ok_or_else(Error::unauthorized)?;
    if values.next().is_some() {
        return Err(Error::unauthorized());
    }
    first.to_str().map_err(|_| Error::unauthorized())
}
fn human_bearer(headers: &HeaderMap, origin: &str, write: bool) -> Result<String> {
    if write && exact_header(headers, "origin").map_err(|_| Error::forbidden())? != origin {
        return Err(Error::forbidden());
    }
    let value = exact_header(headers, "authorization")?;
    let bearer = value
        .strip_prefix("Bearer ")
        .ok_or_else(Error::unauthorized)?;
    if bearer.len() < 32
        || bearer.len() > 256
        || !bearer
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_-. ".contains(&b))
        || bearer.contains(' ')
    {
        return Err(Error::unauthorized());
    }
    Ok(bearer.to_owned())
}

impl HgState {
    async fn enabled(&self) -> Result<bool> {
        Ok(sqlx::query_scalar::<_, bool>(
            "SELECT enabled FROM core_identity.hg_config WHERE singleton=TRUE",
        )
        .fetch_one(&self.pool)
        .await?)
    }
    async fn require_enabled(&self) -> Result<()> {
        if !self.enabled().await? {
            return Err(Error::forbidden());
        }
        Ok(())
    }
    async fn signed(&self, request: Request) -> Result<(crypto::Signed, Vec<u8>)> {
        let (parts, body) = request.into_parts();
        let body = to_bytes(body, 16 * 1024)
            .await
            .map_err(|_| Error::invalid())?;
        let proof = crypto::verify(
            &parts.headers,
            &parts.method,
            &parts.uri,
            &body,
            &self.origin,
            crypto::now(),
        )
        .map_err(|_| Error::unauthorized())?;
        // Bound signed traffic by its actual socket peer and by actor. Never
        // trust client-supplied forwarded IP headers or unauthenticated IDs.
        let peer = parts
            .extensions
            .get::<axum::extract::ConnectInfo<std::net::SocketAddr>>()
            .map(|peer| peer.0.ip().to_string())
            .unwrap_or_else(|| "unknown-peer".to_owned());
        let peer_ok = self
            .store
            .auth_rate_limit("hg_peer", &peer, 240)
            .await
            .map_err(|_| Error::internal())?;
        let actor_ok = self
            .store
            .auth_rate_limit("hg_actor", &proof.actor_id, 180)
            .await
            .map_err(|_| Error::internal())?;
        if !peer_ok || !actor_ok {
            return Err(Error {
                status: StatusCode::TOO_MANY_REQUESTS,
                code: "rate_limited",
            });
        }
        sqlx::query("DELETE FROM core_identity.hg_nonces WHERE (actor_id,nonce) IN (SELECT actor_id,nonce FROM core_identity.hg_nonces WHERE expires_at<clock_timestamp() LIMIT 100)")
            .execute(&self.pool).await?;
        let inserted: Option<String> = sqlx::query_scalar(
            "INSERT INTO core_identity.hg_nonces(actor_id,nonce,expires_at) VALUES ($1,$2,to_timestamp($3)) ON CONFLICT DO NOTHING RETURNING nonce")
            .bind(&proof.actor_id).bind(&proof.nonce).bind(proof.expires)
            .fetch_optional(&self.pool).await?;
        if inserted.is_none() {
            return Err(Error {
                status: StatusCode::UNAUTHORIZED,
                code: "signature_replay",
            });
        }
        Ok((proof, body.to_vec()))
    }
    async fn browser_user(&self, headers: &HeaderMap, write: bool) -> Result<(i64, String, i64)> {
        let bearer = human_bearer(headers, &self.origin, write)?;
        let user = self
            .browser
            .current_user(&bearer)
            .await
            .map_err(|_| Error::unauthorized())?;
        let user_id = user
            .get("id")
            .and_then(|v| v.as_i64())
            .ok_or_else(Error::unauthorized)?;
        let role = user.get("role").and_then(|v| v.as_i64()).unwrap_or(0);
        Ok((user_id, bearer, role))
    }
    async fn request_by_link(&self, link: &str) -> Result<sqlx::postgres::PgRow> {
        if link.len() != 43
            || URL_SAFE_NO_PAD
                .decode(link)
                .ok()
                .is_none_or(|v| v.len() != 32)
        {
            return Err(Error::not_found());
        }
        sqlx::query("SELECT r.* FROM core_identity.hg_requests r WHERE r.link_digest=$1")
            .bind(digest(link.as_bytes()))
            .fetch_optional(&self.pool)
            .await?
            .ok_or_else(Error::not_found)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn permissions_are_positive_allowlist_only() {
        let read = Permission {
            action: "lmm.profile.read".into(),
            resource: "self".into(),
        };
        assert!(permissions(std::slice::from_ref(&read)).is_ok());
        assert!(permissions(&[]).is_err());
        assert!(permissions(&[read.clone(), read]).is_err());
        assert!(
            permissions(&[Permission {
                action: "lmm.profile.write".into(),
                resource: "self".into()
            }])
            .is_err()
        );
    }
}
