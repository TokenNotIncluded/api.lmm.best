//! Native OAuth login, OS credential storage and read-only model discovery.
//! No model invocation permission is requested and no client secret is embedded.

mod callback;
mod protocol;
mod storage;
#[cfg(test)]
mod tests;

use protocol::{Credential, TokenResponse};
use reqwest::blocking::Client;
use serde::{Deserialize, Serialize};
use std::{
    io::Read,
    time::{Duration, SystemTime, UNIX_EPOCH},
};
use storage::{SecretStore, StoredSession, SystemStore};
use thiserror::Error;
use zeroize::Zeroizing;

/// Default trusted deployment issuer; never taken from HTTP redirects.
pub const DEFAULT_ISSUER: &str = "https://api.lmm.best";
const CLIENT_ID: &str = "lmm";
const SCOPE: &str = "catalog:read balance:read";

/// Errors contain fixed messages only: never HTTP bodies, URLs or credentials.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum AuthError {
    #[error("issuer 必须是规范的 HTTPS 域名 origin，不含端口、路径、查询或凭据")]
    InvalidIssuer,
    #[error("LMM 网络请求失败；请检查网络和服务端是否已启用 lmm 客户端")]
    Network,
    #[error("LMM OAuth 响应不符合约定，已停止授权")]
    InvalidResponse,
    #[error("授权已取消或被拒绝")]
    Denied,
    #[error("登录等待超时；没有继续发起请求")]
    Timeout,
    #[error("无法绑定本机 OAuth 回调端口")]
    Callback,
    #[error(
        "无法访问系统凭据库；请解锁 Keychain、Credential Manager 或 Secret Service，不会回退到明文文件"
    )]
    CredentialStore,
    #[error("尚未登录；请先运行 lmm login")]
    NotLoggedIn,
    #[error("已有 CLI 登录；切换账号请先 lmm logout，不会迁移应用授权")]
    AlreadyLoggedIn,
    #[error("上次刷新未可靠完成；不会重放旧刷新令牌，请先 logout 再 login")]
    RefreshBlocked,
    #[error("另一项 LMM 授权操作正在运行，或无法建立当前用户的安全锁")]
    Busy,
    #[error("服务端返回未授权；请重新登录，或检查 lmm 客户端是否已部署")]
    Unauthorized,
    #[error("新授权未能保存，且无法确认已撤销；请在 LMM 授权页检查 LMM CLI 授权")]
    UnstoredGrant,
    #[error("无法输出登录入口；登录已停止")]
    Output,
}

/// Login information deliberately excludes tokens, account secrets and scopes with group IDs.
#[derive(Debug, Serialize)]
pub struct LoginStatus {
    pub outcome: &'static str,
    pub issuer: String,
    pub client_id: &'static str,
    pub expires_at: u64,
    pub permissions: [&'static str; 2],
    pub applications_changed: bool,
}

/// Validated catalog metadata; optional/unknown prices stay unknown.
#[derive(Debug, Deserialize, Serialize)]
pub struct ModelCatalog {
    pub schema_version: u32,
    pub resource: String,
    pub updated_at: u64,
    pub models: Vec<Model>,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Model {
    pub id: String,
    pub group: String,
    pub upstream_model: String,
    pub name: String,
    pub apis: Vec<String>,
    pub pricing: Pricing,
}

#[derive(Debug, Deserialize, Serialize)]
pub struct Pricing {
    pub currency: String,
    pub unit: String,
    pub price_basis: String,
    pub input: Option<f64>,
    pub output: Option<f64>,
    pub request: Option<f64>,
    pub final_cost_depends_on_usage: bool,
}

struct Transport {
    issuer: String,
    resource: String,
    client: Client,
}

impl Transport {
    fn new(issuer: &str) -> Result<Self, AuthError> {
        protocol::validate_issuer(issuer)?;
        Ok(Self {
            issuer: issuer.to_owned(),
            resource: format!("{issuer}/api/oauth2"),
            client: Client::builder()
                .https_only(true)
                .redirect(reqwest::redirect::Policy::none())
                .connect_timeout(Duration::from_secs(5))
                .timeout(Duration::from_secs(15))
                .build()
                .map_err(|_| AuthError::Network)?,
        })
    }

    fn request(
        &self,
        request: reqwest::blocking::RequestBuilder,
        limit: u64,
    ) -> Result<Zeroizing<Vec<u8>>, AuthError> {
        let response = request.send().map_err(|_| AuthError::Network)?;
        if matches!(response.status().as_u16(), 401 | 403) {
            return Err(AuthError::Unauthorized);
        }
        if !response.status().is_success() {
            return Err(AuthError::Network);
        }
        if response.content_length().is_some_and(|len| len > limit) {
            return Err(AuthError::InvalidResponse);
        }
        let mut body = Zeroizing::new(Vec::new());
        response
            .take(limit + 1)
            .read_to_end(&mut body)
            .map_err(|_| AuthError::Network)?;
        if body.len() as u64 > limit {
            return Err(AuthError::InvalidResponse);
        }
        Ok(body)
    }

    fn discover(&self) -> Result<(), AuthError> {
        let authorization = self.request(
            self.client.get(format!(
                "{}/.well-known/oauth-authorization-server",
                self.issuer
            )),
            64 * 1024,
        )?;
        let resource = self.request(
            self.client.get(format!(
                "{}/.well-known/oauth-protected-resource/api/oauth2",
                self.issuer
            )),
            64 * 1024,
        )?;
        protocol::validate_metadata(&authorization, &resource, &self.issuer)
    }

    fn token(&self, fields: &[(&str, &str)]) -> Result<Credential, AuthError> {
        let started = now();
        let body = self.request(
            self.client
                .post(format!("{}/token", self.resource))
                .form(fields),
            64 * 1024,
        )?;
        let token: TokenResponse =
            serde_json::from_slice(&body).map_err(|_| AuthError::InvalidResponse)?;
        token.validate(&self.issuer, started)
    }

    fn revoke(&self, credential: &Credential) -> Result<(), AuthError> {
        self.request(
            self.client
                .post(format!("{}/revoke", self.resource))
                .form(&[
                    ("client_id", CLIENT_ID),
                    ("token", credential.refresh.as_str()),
                    ("token_type_hint", "refresh_token"),
                ]),
            64 * 1024,
        )?;
        Ok(())
    }
}

fn now() -> u64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |time| time.as_secs())
}

fn refresh(
    transport: &Transport,
    store: &dyn SecretStore,
    session: StoredSession,
) -> Result<StoredSession, AuthError> {
    if session.refresh_blocked {
        return Err(AuthError::RefreshBlocked);
    }
    if session.credential.expires_at > now().saturating_add(30) {
        return Ok(session);
    }
    let mut pending = session;
    pending.refresh_blocked = true;
    // Persist before sending. A crash or ambiguous response never retries this refresh.
    store.save(&pending)?;
    let next = transport.token(&[
        ("grant_type", "refresh_token"),
        ("client_id", CLIENT_ID),
        ("refresh_token", &pending.credential.refresh),
        ("resource", &transport.resource),
    ])?;
    if next.refresh == pending.credential.refresh
        || !next.scope.split_whitespace().all(|scope| {
            pending
                .credential
                .scope
                .split_whitespace()
                .any(|old| old == scope)
        })
    {
        let _ = transport.revoke(&next);
        return Err(AuthError::InvalidResponse);
    }
    let ready = StoredSession {
        credential: next,
        refresh_blocked: false,
    };
    if store.save(&ready).is_err() {
        return match transport.revoke(&ready.credential) {
            Ok(()) => Err(AuthError::CredentialStore),
            Err(_) => Err(AuthError::UnstoredGrant),
        };
    }
    Ok(ready)
}

/// Log in using a browser and bounded loopback callback, storing only in the OS vault.
/// `show_url` is called only after the loopback listener has bound successfully.
pub fn login(
    issuer: &str,
    timeout: Duration,
    show_url: impl FnOnce(&str) -> Result<(), AuthError>,
) -> Result<LoginStatus, AuthError> {
    let transport = Transport::new(issuer)?;
    let _lock = storage::lock()?;
    let store = SystemStore::new(issuer)?;
    login_with(&transport, &store, timeout, show_url)
}

fn login_with(
    transport: &Transport,
    store: &dyn SecretStore,
    timeout: Duration,
    show_url: impl FnOnce(&str) -> Result<(), AuthError>,
) -> Result<LoginStatus, AuthError> {
    let issuer = &transport.issuer;
    if store.load()?.is_some() {
        return Err(AuthError::AlreadyLoggedIn);
    }
    transport.discover()?;
    let callback = callback::Callback::bind()?;
    let handshake = protocol::Handshake::new()?;
    let redirect = callback.redirect_uri()?;
    let authorization = handshake.authorization_url(issuer, &redirect)?;
    show_url(authorization.as_str())?;
    let code = Zeroizing::new(callback.wait(issuer, &handshake.state, timeout)?);
    let credential = transport.token(&[
        ("grant_type", "authorization_code"),
        ("client_id", CLIENT_ID),
        ("code", &code),
        ("redirect_uri", &redirect),
        ("code_verifier", &handshake.verifier),
        ("resource", &transport.resource),
    ])?;
    let status = LoginStatus {
        outcome: "logged_in",
        issuer: issuer.to_owned(),
        client_id: CLIENT_ID,
        expires_at: credential.expires_at,
        permissions: ["catalog:read", "balance:read"],
        applications_changed: false,
    };
    let session = StoredSession {
        credential,
        refresh_blocked: false,
    };
    if store.save(&session).is_err() {
        return match transport.revoke(&session.credential) {
            Ok(()) => Err(AuthError::CredentialStore),
            Err(_) => Err(AuthError::UnstoredGrant),
        };
    }
    Ok(status)
}

/// Revoke only this CLI's family, then remove local credentials.
/// Network errors retain the local record so revocation can be retried.
pub fn logout(issuer: &str) -> Result<&'static str, AuthError> {
    let transport = Transport::new(issuer)?;
    let _lock = storage::lock()?;
    let store = SystemStore::new(issuer)?;
    logout_from(&transport, &store)
}

fn logout_from(transport: &Transport, store: &dyn SecretStore) -> Result<&'static str, AuthError> {
    let Some(session) = store.load()? else {
        return Ok("already_logged_out");
    };
    transport.revoke(&session.credential)?;
    store.delete()?;
    Ok("logged_out_cli_grant_revoked")
}

/// Read the authorized model catalog without invoking or selecting any model.
pub fn models(issuer: &str) -> Result<ModelCatalog, AuthError> {
    let transport = Transport::new(issuer)?;
    let _lock = storage::lock()?;
    let store = SystemStore::new(issuer)?;
    let session = store.load()?.ok_or(AuthError::NotLoggedIn)?;
    let session = refresh(&transport, &store, session)?;
    let body = transport.request(
        transport
            .client
            .get(format!("{}/catalog", transport.resource))
            .bearer_auth(&session.credential.access),
        2 * 1024 * 1024,
    )?;
    let catalog: ModelCatalog =
        serde_json::from_slice(&body).map_err(|_| AuthError::InvalidResponse)?;
    if catalog.schema_version != 1 || catalog.resource != transport.resource {
        return Err(AuthError::InvalidResponse);
    }
    Ok(catalog)
}
