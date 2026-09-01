//! Legacy-compatible personal access-IP management and edge policy routes.
//!
//! The policy endpoint is loopback-only and fail-closed for mainland-China
//! traffic. User management remains behind the console-discovery boundary and
//! uses the same shared critical-rate-limit authority as the Go service.

use std::{
    net::{IpAddr, Ipv4Addr, SocketAddr},
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{ConnectInfo, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::PgPool;

use crate::{
    ClientIpKey, RequestContext,
    auth::{
        AuthErrorKind, CriticalRateLimitOutcome, DashboardAuth, UserAuthPolicyError,
        dashboard_token_candidate, user_auth_message, user_auth_status,
    },
    legacy_empty_response,
};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 64 * 1024 * 1024;
const MINIMUM_TRUST_LEVEL: i64 = 1;
const ADMIN_ROLE: i64 = 10;

/// PostgreSQL and dashboard-auth dependencies for the four access-IP routes.
#[derive(Clone)]
pub struct AccessIpState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
    identities: Arc<dyn AccessIpIdentityResolver>,
}

impl AccessIpState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            pg: pg.clone(),
            auth: Arc::clone(&auth),
            identities: Arc::new(DashboardAccessIpIdentityResolver { pg, auth }),
        }
    }

    /// Replaces credential resolution for route-level contract tests.
    #[must_use]
    pub fn with_identity_resolver(mut self, identities: Arc<dyn AccessIpIdentityResolver>) -> Self {
        self.identities = identities;
        self
    }
}

/// Builds the compatibility policy endpoint and three user management routes.
///
/// The normal listener must mount this router inside its existing global API
/// rate-limit and console-access surface; this module intentionally does not
/// duplicate the listener-owned global limiter.
pub fn router(state: AccessIpState) -> Router {
    Router::new()
        .route(
            "/api/internal/access-ip-policy",
            get(check_access_ip_policy),
        )
        .route(
            "/api/user/access-ip",
            get(get_personal_access_ip)
                .put(set_personal_access_ip)
                .delete(delete_personal_access_ip),
        )
        .with_state(state)
}

/// Server-derived identity used by the route's TryUserAuth/UserAuth boundary.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct AccessIpIdentity {
    pub id: i64,
    pub username: String,
    pub role: i64,
    pub status: i64,
    pub trust_level_override: Option<i64>,
    pub developer_access_granted: bool,
}

/// Observable authentication failures preserved from dashboard middleware.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AccessIpAuthError {
    TokenExpired,
    SessionRevoked,
    UserDisabled,
    Unauthorized,
    Internal,
}

/// Injectable identity authority for deterministic route contract tests.
#[async_trait]
pub trait AccessIpIdentityResolver: Send + Sync {
    async fn resolve(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<AccessIpIdentity>, AccessIpAuthError>;
}

#[derive(Clone)]
struct DashboardAccessIpIdentityResolver {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
}

#[async_trait]
impl AccessIpIdentityResolver for DashboardAccessIpIdentityResolver {
    async fn resolve(
        &self,
        headers: &HeaderMap,
    ) -> Result<Option<AccessIpIdentity>, AccessIpAuthError> {
        let Some(credential) = crate::routes::legacy_http::dashboard_credential(headers) else {
            return Ok(None);
        };
        let internal = dashboard_token_candidate(&credential);
        match self
            .auth
            .self_user_view_for_optional(SecretString::from(credential.clone()))
            .await
        {
            Ok(user) => Ok(Some(AccessIpIdentity {
                id: user.id,
                username: user.username,
                role: user.role,
                status: user.status,
                trust_level_override: user.trust_level_info.override_level,
                developer_access_granted: user.developer_access_granted,
            })),
            Err(error) if !internal && error.kind == AuthErrorKind::UserDisabled => {
                self.disabled_personal_token_identity(&credential).await
            }
            Err(error)
                if !internal
                    && matches!(
                        error.kind,
                        AuthErrorKind::Unauthorized | AuthErrorKind::InvalidCredentials
                    ) =>
            {
                Ok(None)
            }
            Err(error) => Err(map_auth_error(error.kind)),
        }
    }
}

impl DashboardAccessIpIdentityResolver {
    async fn disabled_personal_token_identity(
        &self,
        credential: &str,
    ) -> Result<Option<AccessIpIdentity>, AccessIpAuthError> {
        let row = sqlx::query_as::<_, (i64, String, i64, i64, Option<i64>)>(
            r#"SELECT id::BIGINT,
                      COALESCE(username, ''),
                      role::BIGINT,
                      status::BIGINT,
                      CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
                           THEN (to_jsonb(users)->>'trust_level_override')::BIGINT END
               FROM users
               WHERE TRIM(TRAILING FROM access_token) = $1
                 AND deleted_at IS NULL
               LIMIT 1"#,
        )
        .bind(credential)
        .fetch_optional(&self.pg)
        .await
        .map_err(|_| AccessIpAuthError::Internal)?;
        Ok(row.map(
            |(id, username, role, status, trust_level_override)| AccessIpIdentity {
                id,
                username,
                role,
                status,
                trust_level_override,
                developer_access_granted: false,
            },
        ))
    }
}

fn map_auth_error(kind: AuthErrorKind) -> AccessIpAuthError {
    match kind {
        AuthErrorKind::TokenExpired => AccessIpAuthError::TokenExpired,
        AuthErrorKind::SessionRevoked => AccessIpAuthError::SessionRevoked,
        AuthErrorKind::UserDisabled => AccessIpAuthError::UserDisabled,
        AuthErrorKind::Internal => AccessIpAuthError::Internal,
        _ => AccessIpAuthError::Unauthorized,
    }
}

#[derive(Clone, Debug)]
struct AccessUser {
    id: i64,
    role: i64,
    status: i64,
    trust_level_override: Option<i64>,
    console_activated_at: i64,
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
struct AccessIpStoreError(String);

async fn load_user(pg: &PgPool, user_id: i64) -> Result<AccessUser, AccessIpStoreError> {
    sqlx::query_as::<_, (i64, i64, i64, Option<i64>, i64)>(
        r#"SELECT id::BIGINT,
                  role::BIGINT,
                  status::BIGINT,
                  CASE WHEN COALESCE(to_jsonb(users)->>'trust_level_override', '') ~ '^-?[0-9]+$'
                       THEN (to_jsonb(users)->>'trust_level_override')::BIGINT END,
                  CASE WHEN COALESCE(to_jsonb(users)->>'console_activated_at', '') ~ '^-?[0-9]+$'
                       THEN (to_jsonb(users)->>'console_activated_at')::BIGINT ELSE 0 END
           FROM users
           WHERE id = $1 AND deleted_at IS NULL"#,
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .map(
        |(id, role, status, trust_level_override, console_activated_at)| AccessUser {
            id,
            role,
            status,
            trust_level_override,
            console_activated_at,
        },
    )
    .ok_or_else(|| AccessIpStoreError("record not found".to_owned()))
}

async fn paid_activation_complete(pg: &PgPool, user_id: i64) -> Result<bool, AccessIpStoreError> {
    sqlx::query_scalar::<_, bool>(
        r#"WITH parsed AS (
               SELECT COALESCE(row_data->>'status', '') AS status,
                      COALESCE(row_data->>'payment_method', '') AS payment_method,
                      COALESCE(row_data->>'payment_provider', '') AS payment_provider,
                      CASE WHEN COALESCE(row_data->>'money', '') ~ '^-?[0-9]+([.][0-9]+)?$'
                           THEN (row_data->>'money')::DOUBLE PRECISION ELSE 0 END AS money,
                      CASE WHEN COALESCE(row_data->>'amount', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'amount')::BIGINT ELSE 0 END AS amount,
                      CASE WHEN COALESCE(row_data->>'credited_quota', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'credited_quota')::BIGINT ELSE 0 END AS credited_quota,
                      CASE WHEN COALESCE(row_data->>'settled_amount_micros', '') ~ '^-?[0-9]+$'
                           THEN (row_data->>'settled_amount_micros')::BIGINT ELSE 0 END AS settled_amount_micros
               FROM (
                   SELECT to_jsonb(top_ups) AS row_data
                   FROM top_ups
                   WHERE user_id = $1
               ) rows
           ), qualified AS (
               SELECT status = 'success'
                  AND payment_method <> 'balance'
                  AND payment_provider <> 'balance'
                  AND (settled_amount_micros > 0 OR (settled_amount_micros = 0 AND money > 0))
                  AND NOT (
                      LOWER(payment_provider) = 'epay'
                      AND LOWER(payment_method) IN ('epay', 'ldc', 'linuxdo', 'linux_do', 'linuxdo_credit')
                  )
                  AND (credited_quota > 0 OR amount > 0)
                  AND (
                      payment_provider IN ('epay', 'stripe', 'creem', 'waffo', 'waffo_pancake')
                      OR (
                          payment_provider = ''
                          AND payment_method IN ('stripe', 'creem', 'waffo', 'waffo_pancake', 'alipay', 'wxpay')
                      )
                  ) AS qualifies
               FROM parsed
           )
           SELECT COALESCE(BOOL_OR(qualifies), FALSE) FROM qualified"#,
    )
    .bind(user_id)
    .fetch_one(pg)
    .await
    .map_err(database_error)
}

async fn trust_eligible(pg: &PgPool, user: &AccessUser) -> Result<bool, AccessIpStoreError> {
    if user.role >= ADMIN_ROLE {
        return Ok(true);
    }
    if let Some(level) = user.trust_level_override {
        return Ok((MINIMUM_TRUST_LEVEL..=4).contains(&level));
    }
    if user.console_activated_at > 0 {
        return Ok(true);
    }
    paid_activation_complete(pg, user.id).await
}

async fn console_access(
    state: &AccessIpState,
    identity: &AccessIpIdentity,
) -> Result<bool, AccessIpStoreError> {
    let user = load_user(&state.pg, identity.id).await?;
    let access = trust_eligible(&state.pg, &user).await?;
    Ok(access || identity.developer_access_granted)
}

enum ResolvedIdentity {
    Anonymous,
    Authenticated(AccessIpIdentity),
    Rejected(AccessIpAuthError),
}

async fn console_gate(
    state: &AccessIpState,
    headers: &HeaderMap,
) -> Result<ResolvedIdentity, Response> {
    let resolved = match state.identities.resolve(headers).await {
        Ok(Some(identity)) => ResolvedIdentity::Authenticated(identity),
        Ok(None) => ResolvedIdentity::Anonymous,
        Err(error) => ResolvedIdentity::Rejected(error),
    };
    if let ResolvedIdentity::Authenticated(identity) = &resolved {
        match console_access(state, identity).await {
            Ok(true) => {}
            Ok(false) | Err(_) => return Err(console_not_found()),
        }
    }
    Ok(resolved)
}

async fn check_access_ip_policy(State(state): State<AccessIpState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let peer_is_loopback = loopback_peer(&request);
    let cn_source = trimmed_header(&headers, "x-lmm-cn-source") == Some("1");
    let original_ip = trimmed_header(&headers, "x-original-client-ip").map(str::to_owned);
    let resolved = match console_gate(&state, &headers).await {
        Ok(resolved) => resolved,
        Err(response) => return response,
    };
    let versioned = matches!(resolved, ResolvedIdentity::Authenticated(_));
    let response = match resolved {
        ResolvedIdentity::Rejected(error) => return dashboard_auth_error(&headers, error),
        ResolvedIdentity::Anonymous => {
            check_policy_for_identity(
                &state,
                peer_is_loopback,
                cn_source,
                original_ip.as_deref(),
                None,
            )
            .await
        }
        ResolvedIdentity::Authenticated(identity) => {
            check_policy_for_identity(
                &state,
                peer_is_loopback,
                cn_source,
                original_ip.as_deref(),
                Some(&identity),
            )
            .await
        }
    };
    let response = disable_cache(response);
    if versioned {
        with_auth_version(response)
    } else {
        response
    }
}

async fn check_policy_for_identity(
    state: &AccessIpState,
    peer_is_loopback: bool,
    cn_source: bool,
    original_ip: Option<&str>,
    identity: Option<&AccessIpIdentity>,
) -> Response {
    if !peer_is_loopback {
        return policy_error(
            StatusCode::FORBIDDEN,
            "INTERNAL_ONLY",
            "internal policy endpoint",
        );
    }
    if !cn_source {
        return StatusCode::NO_CONTENT.into_response();
    }
    let Some(identity) = identity else {
        return policy_error(
            StatusCode::UNAUTHORIZED,
            "AUTH_REQUIRED",
            "a valid account session is required",
        );
    };
    let Some(original_ip) = original_ip else {
        return policy_error(
            StatusCode::FORBIDDEN,
            "CLIENT_IP_REQUIRED",
            "original client IP is required",
        );
    };
    match personal_access_allowed(&state.pg, identity.id, original_ip).await {
        Ok(true) => StatusCode::NO_CONTENT.into_response(),
        Ok(false) => policy_error(
            StatusCode::FORBIDDEN,
            "CN_DIRECT_ACCESS_BLOCKED",
            "direct access is not allowed for this account",
        ),
        Err(_) => policy_error(
            StatusCode::FORBIDDEN,
            "POLICY_UNAVAILABLE",
            "access policy unavailable",
        ),
    }
}

async fn personal_access_allowed(
    pg: &PgPool,
    user_id: i64,
    raw_ip: &str,
) -> Result<bool, AccessIpStoreError> {
    if user_id <= 0 {
        return Err(AccessIpStoreError("invalid data".to_owned()));
    }
    let Ok(ip) = normalize_personal_access_ip(raw_ip) else {
        return Ok(false);
    };
    let exists = sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS(SELECT 1 FROM personal_access_ips WHERE user_id = $1 AND ip = $2)",
    )
    .bind(user_id)
    .bind(ip)
    .fetch_one(pg)
    .await
    .map_err(database_error)?;
    if !exists {
        return Ok(false);
    }
    let user = load_user(pg, user_id).await?;
    if user.status != 1 {
        return Ok(false);
    }
    trust_eligible(pg, &user).await
}

async fn get_personal_access_ip(State(state): State<AccessIpState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let identity = match required_identity(&state, &headers).await {
        Ok(identity) => identity,
        Err(response) => return response,
    };
    let current_ip = normalized_client_ip(&request);
    let response = match load_user(&state.pg, identity.id).await {
        Ok(user) => match personal_access_response(&state.pg, &user, current_ip).await {
            Ok(data) => success(data),
            Err(error) => api_error(error.to_string()),
        },
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(disable_cache(response))
}

async fn set_personal_access_ip(State(state): State<AccessIpState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let identity = match required_identity(&state, &headers).await {
        Ok(identity) => identity,
        Err(response) => return response,
    };
    let client_ip = client_ip_key(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let user = match load_user(&state.pg, identity.id).await {
        Ok(user) => user,
        Err(error) => {
            return with_auth_version(disable_cache(api_error(error.to_string())));
        }
    };
    let current_ip = normalized_client_ip(&request);
    let input = match parse_set_request(request).await {
        Ok(input) => input,
        Err(()) => {
            return with_auth_version(disable_cache(coded_error(
                StatusCode::UNPROCESSABLE_ENTITY,
                "INVALID_IP",
                "a public IP address is required",
            )));
        }
    };
    let response = match set_access_ip(&state.pg, &user, &input.ip).await {
        Ok(()) => match personal_access_response(&state.pg, &user, current_ip).await {
            Ok(data) => success(data),
            Err(error) => api_error(error.to_string()),
        },
        Err(SetAccessIpError::Ineligible) => coded_error(
            StatusCode::FORBIDDEN,
            "TRUST_LEVEL_REQUIRED",
            "personal IP allowlist requires trust level L1 or higher",
        ),
        Err(SetAccessIpError::InvalidIp) => coded_error(
            StatusCode::UNPROCESSABLE_ENTITY,
            "INVALID_IP",
            "IP address must be public and globally routable",
        ),
        Err(SetAccessIpError::Store(error)) => api_error(error.to_string()),
    };
    with_auth_version(disable_cache(response))
}

async fn delete_personal_access_ip(
    State(state): State<AccessIpState>,
    request: Request,
) -> Response {
    let headers = request.headers().clone();
    let identity = match required_identity(&state, &headers).await {
        Ok(identity) => identity,
        Err(response) => return response,
    };
    let client_ip = client_ip_key(&request);
    if let Some(response) = critical_rate_limit(&state, &client_ip).await {
        return with_auth_version(response);
    }
    let response = match sqlx::query("DELETE FROM personal_access_ips WHERE user_id = $1")
        .bind(identity.id)
        .execute(&state.pg)
        .await
    {
        Ok(_) => success(Value::Null),
        Err(error) => api_error(error.to_string()),
    };
    with_auth_version(disable_cache(response))
}

async fn required_identity(
    state: &AccessIpState,
    headers: &HeaderMap,
) -> Result<AccessIpIdentity, Response> {
    let resolved = console_gate(state, headers).await?;
    let identity = match resolved {
        ResolvedIdentity::Authenticated(identity) => identity,
        ResolvedIdentity::Anonymous => {
            return Err(dashboard_auth_error(
                headers,
                AccessIpAuthError::Unauthorized,
            ));
        }
        ResolvedIdentity::Rejected(error) => return Err(dashboard_auth_error(headers, error)),
    };
    enforce_user_auth(&identity).map_err(|error| user_policy_error(headers, error))?;
    Ok(identity)
}

fn enforce_user_auth(identity: &AccessIpIdentity) -> Result<(), UserAuthPolicyError> {
    if identity.status != 1 {
        return Err(UserAuthPolicyError::UserDisabled);
    }
    if identity.role < 1 {
        return Err(UserAuthPolicyError::InsufficientPrivilege);
    }
    if identity.id <= 0
        || identity.username.trim().is_empty()
        || !matches!(identity.role, 0 | 1 | 10 | 100)
    {
        return Err(UserAuthPolicyError::InvalidUserInfo);
    }
    Ok(())
}

async fn critical_rate_limit(state: &AccessIpState, client_ip: &str) -> Option<Response> {
    match state.auth.check_critical_rate_limit(client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

#[derive(Debug, Default, Deserialize)]
struct SetAccessIpRequest {
    #[serde(default, deserialize_with = "deserialize_nullable_string")]
    ip: String,
}

async fn parse_set_request(request: Request) -> Result<SetAccessIpRequest, ()> {
    let bytes = to_bytes(request.into_body(), BODY_LIMIT_BYTES)
        .await
        .map_err(|_| ())?;
    let value = serde_json::from_slice::<Value>(&bytes).map_err(|_| ())?;
    if value.is_null() {
        return Ok(SetAccessIpRequest::default());
    }
    serde_json::from_value(value).map_err(|_| ())
}

fn deserialize_nullable_string<'de, D>(deserializer: D) -> Result<String, D::Error>
where
    D: serde::Deserializer<'de>,
{
    Option::<String>::deserialize(deserializer).map(|value| value.unwrap_or_default())
}

enum SetAccessIpError {
    Ineligible,
    InvalidIp,
    Store(AccessIpStoreError),
}

async fn set_access_ip(
    pg: &PgPool,
    user: &AccessUser,
    raw_ip: &str,
) -> Result<(), SetAccessIpError> {
    if !trust_eligible(pg, user)
        .await
        .map_err(SetAccessIpError::Store)?
    {
        return Err(SetAccessIpError::Ineligible);
    }
    let ip = normalize_personal_access_ip(raw_ip).map_err(|()| SetAccessIpError::InvalidIp)?;
    let mut transaction = pg
        .begin()
        .await
        .map_err(database_error)
        .map_err(SetAccessIpError::Store)?;
    let exists = sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS(SELECT 1 FROM personal_access_ips WHERE user_id = $1)",
    )
    .bind(user.id)
    .fetch_one(&mut *transaction)
    .await
    .map_err(database_error)
    .map_err(SetAccessIpError::Store)?;
    let now = unix_now();
    if exists {
        sqlx::query("UPDATE personal_access_ips SET ip = $1, updated_at = $2 WHERE user_id = $3")
            .bind(&ip)
            .bind(now)
            .bind(user.id)
            .execute(&mut *transaction)
            .await
            .map_err(database_error)
            .map_err(SetAccessIpError::Store)?;
    } else {
        sqlx::query(
            "INSERT INTO personal_access_ips (user_id, ip, created_at, updated_at) VALUES ($1, $2, $3, $3)",
        )
        .bind(user.id)
        .bind(&ip)
        .bind(now)
        .execute(&mut *transaction)
        .await
        .map_err(database_error)
        .map_err(SetAccessIpError::Store)?;
    }
    transaction
        .commit()
        .await
        .map_err(database_error)
        .map_err(SetAccessIpError::Store)
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
struct PersonalAccessIpResponse {
    ip: String,
    current_ip: String,
    current_ip_allowed: bool,
    eligible: bool,
    minimum_trust_level: i64,
    production_cn_linkage: bool,
}

async fn personal_access_response(
    pg: &PgPool,
    user: &AccessUser,
    current_ip: String,
) -> Result<PersonalAccessIpResponse, AccessIpStoreError> {
    let eligible = trust_eligible(pg, user).await?;
    let ip = sqlx::query_scalar::<_, String>(
        "SELECT ip FROM personal_access_ips WHERE user_id = $1 LIMIT 1",
    )
    .bind(user.id)
    .fetch_optional(pg)
    .await
    .map_err(database_error)?
    .unwrap_or_default();
    let current_ip_allowed = !current_ip.is_empty()
        && !ip.is_empty()
        && current_ip == ip
        && eligible
        && user.status == 1;
    Ok(PersonalAccessIpResponse {
        ip,
        current_ip,
        current_ip_allowed,
        eligible,
        minimum_trust_level: MINIMUM_TRUST_LEVEL,
        production_cn_linkage: true,
    })
}

fn normalize_personal_access_ip(raw: &str) -> Result<String, ()> {
    let mut address = raw.trim().parse::<IpAddr>().map_err(|_| ())?;
    if let IpAddr::V6(ipv6) = address
        && let Some(ipv4) = ipv6.to_ipv4_mapped()
    {
        address = IpAddr::V4(ipv4);
    }
    if !public_global_unicast(address) || reserved_personal_access_ip(address) {
        return Err(());
    }
    Ok(address.to_string())
}

fn public_global_unicast(address: IpAddr) -> bool {
    match address {
        IpAddr::V4(ip) => {
            !ip.is_private()
                && !ip.is_loopback()
                && !ip.is_link_local()
                && !ip.is_multicast()
                && !ip.is_unspecified()
                && ip != Ipv4Addr::BROADCAST
        }
        IpAddr::V6(ip) => {
            !ip.is_loopback()
                && !ip.is_unique_local()
                && !ip.is_unicast_link_local()
                && !ip.is_multicast()
                && !ip.is_unspecified()
        }
    }
}

fn reserved_personal_access_ip(address: IpAddr) -> bool {
    match address {
        IpAddr::V4(ip) => {
            let octets = ip.octets();
            (octets[0] == 100 && (64..=127).contains(&octets[1]))
                || (octets[0] == 192 && octets[1] == 0 && octets[2] == 0)
                || (octets[0] == 192 && octets[1] == 0 && octets[2] == 2)
                || (octets[0] == 198 && (octets[1] == 18 || octets[1] == 19))
                || (octets[0] == 198 && octets[1] == 51 && octets[2] == 100)
                || (octets[0] == 203 && octets[1] == 0 && octets[2] == 113)
        }
        IpAddr::V6(ip) => {
            let segments = ip.segments();
            segments[0] == 0x2001 && segments[1] == 0x0db8
        }
    }
}

fn loopback_peer(request: &Request) -> bool {
    request
        .extensions()
        .get::<ConnectInfo<SocketAddr>>()
        .is_some_and(|ConnectInfo(address)| address.ip().is_loopback())
}

fn normalized_client_ip(request: &Request) -> String {
    let raw = request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        });
    raw.as_deref()
        .and_then(|value| normalize_personal_access_ip(value).ok())
        .unwrap_or_default()
}

fn client_ip_key(request: &Request) -> String {
    request
        .extensions()
        .get::<ClientIpKey>()
        .map(|key| key.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .unwrap_or_else(|| "unknown".to_owned())
}

fn trimmed_header<'a>(headers: &'a HeaderMap, name: &str) -> Option<&'a str> {
    headers
        .get(name)
        .and_then(|value| value.to_str().ok())
        .map(str::trim)
        .filter(|value| !value.is_empty())
}

fn dashboard_auth_error(headers: &HeaderMap, error: AccessIpAuthError) -> Response {
    let (status, code, english) = match error {
        AccessIpAuthError::TokenExpired => (
            StatusCode::UNAUTHORIZED,
            "AUTH_TOKEN_EXPIRED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AccessIpAuthError::SessionRevoked => (
            StatusCode::UNAUTHORIZED,
            "AUTH_SESSION_REVOKED",
            "Unauthorized, not logged in and no access token provided",
        ),
        AccessIpAuthError::Internal => (
            StatusCode::INTERNAL_SERVER_ERROR,
            "AUTH_INTERNAL_ERROR",
            "Database error, please contact the administrator",
        ),
        AccessIpAuthError::UserDisabled => (
            StatusCode::UNAUTHORIZED,
            "AUTH_USER_DISABLED",
            "User has been banned",
        ),
        AccessIpAuthError::Unauthorized => (
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Unauthorized, invalid access token",
        ),
    };
    let message = if accepts_chinese(headers) {
        match error {
            AccessIpAuthError::Internal => "数据库出错，请联系管理员",
            AccessIpAuthError::TokenExpired | AccessIpAuthError::SessionRevoked => {
                "无权进行此操作，未登录且未提供 access token"
            }
            AccessIpAuthError::UserDisabled => "用户已被封禁",
            AccessIpAuthError::Unauthorized => "无权进行此操作，access token 无效",
        }
    } else {
        english
    };
    coded_error(status, code, message)
}

fn user_policy_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    coded_error(
        status,
        code,
        user_auth_message(
            error,
            headers
                .get(header::ACCEPT_LANGUAGE)
                .and_then(|value| value.to_str().ok()),
        ),
    )
}

fn accepts_chinese(headers: &HeaderMap) -> bool {
    headers
        .get(header::ACCEPT_LANGUAGE)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value.to_ascii_lowercase().starts_with("zh"))
}

fn success<T: Serialize>(data: T) -> Response {
    Json(json!({"success": true, "message": "", "data": data})).into_response()
}

fn api_error(message: String) -> Response {
    Json(json!({"success": false, "message": message})).into_response()
}

fn coded_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    (
        status,
        Json(json!({"success": false, "code": code, "message": message})),
    )
        .into_response()
}

fn policy_error(status: StatusCode, code: &'static str, message: &'static str) -> Response {
    let mut response = coded_error(status, code, message);
    response
        .headers_mut()
        .insert("x-lmm-access-policy", HeaderValue::from_static("denied"));
    response
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

fn disable_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
}

fn database_error(error: sqlx::Error) -> AccessIpStoreError {
    AccessIpStoreError(error.to_string())
}

fn unix_now() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| duration.as_secs() as i64)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn normalizes_public_addresses_and_mapped_ipv4() {
        assert_eq!(
            normalize_personal_access_ip(" 8.8.8.8 "),
            Ok("8.8.8.8".to_owned())
        );
        assert_eq!(
            normalize_personal_access_ip("2001:4860:4860:0:0:0:0:8888"),
            Ok("2001:4860:4860::8888".to_owned())
        );
        assert_eq!(
            normalize_personal_access_ip("::ffff:8.8.4.4"),
            Ok("8.8.4.4".to_owned())
        );
    }

    #[test]
    fn rejects_private_reserved_cidr_and_zoned_addresses() {
        for input in [
            "",
            "127.0.0.1",
            "10.0.0.1",
            "100.64.0.1",
            "192.0.2.1",
            "198.51.100.1",
            "203.0.113.1",
            "2001:db8::1",
            "8.8.8.8/32",
            "fe80::1%eth0",
        ] {
            assert_eq!(normalize_personal_access_ip(input), Err(()), "{input}");
        }
    }
}
