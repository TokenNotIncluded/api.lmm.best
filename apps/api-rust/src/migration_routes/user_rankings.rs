//! Legacy-compatible user usage leaderboard.
//!
//! The route remains behind the console discovery boundary, then applies the
//! dynamic `HeaderNavModules.rankings` policy before reading usage totals.

use std::{
    collections::BTreeMap,
    sync::{Arc, RwLock},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{RawQuery, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use secrecy::SecretString;
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::PgPool;

use super::missing_control_public::{HeaderNavAccess, parse_header_nav_access};
use crate::auth::{DashboardAuth, UserAuthPolicyError, user_auth_message, user_auth_status};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const LEADERBOARD_LIMIT: usize = 20;

/// PostgreSQL and dashboard-auth dependencies for the user leaderboard.
#[derive(Clone)]
pub struct UserRankingsState {
    store: Arc<dyn UserRankingsStore>,
    authorizer: Arc<dyn UserRankingsAuthorizer>,
    last_good_nav: Arc<RwLock<Option<HeaderNavAccess>>>,
}

impl UserRankingsState {
    /// Creates production state backed by the listener's shared dependencies.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self {
            store: Arc::new(PgUserRankingsStore { pg }),
            authorizer: Arc::new(DashboardUserRankingsAuthorizer { auth }),
            last_good_nav: Arc::new(RwLock::new(None)),
        }
    }

    #[cfg(test)]
    fn with_dependencies(
        store: Arc<dyn UserRankingsStore>,
        authorizer: Arc<dyn UserRankingsAuthorizer>,
    ) -> Self {
        Self {
            store,
            authorizer,
            last_good_nav: Arc::new(RwLock::new(None)),
        }
    }

    async fn header_nav(&self) -> HeaderNavAccess {
        match self.store.header_nav().await {
            Ok(access) => {
                let mut last_good_nav = match self.last_good_nav.write() {
                    Ok(guard) => guard,
                    Err(poisoned) => poisoned.into_inner(),
                };
                *last_good_nav = Some(access);
                access
            }
            Err(_) => {
                let last_good_nav = match self.last_good_nav.read() {
                    Ok(guard) => guard,
                    Err(poisoned) => poisoned.into_inner(),
                };
                last_good_nav
                    .as_ref()
                    .copied()
                    .map_or_else(HeaderNavAccess::default, std::convert::identity)
            }
        }
    }
}

/// Builds `GET /api/rankings/users` for the normal listener.
pub fn router(state: UserRankingsState) -> Router {
    Router::new()
        .route("/api/rankings/users", get(user_usage_rankings))
        .with_state(state)
}

#[derive(Clone, Debug)]
struct RankingActor {
    id: i64,
    username: String,
    role: i64,
    status: i64,
    developer_access_granted: bool,
}

#[async_trait]
trait UserRankingsAuthorizer: Send + Sync {
    async fn actor(&self, headers: &HeaderMap) -> Result<RankingActor, ()>;
}

#[derive(Clone)]
struct DashboardUserRankingsAuthorizer {
    auth: Arc<dyn DashboardAuth>,
}

#[async_trait]
impl UserRankingsAuthorizer for DashboardUserRankingsAuthorizer {
    async fn actor(&self, headers: &HeaderMap) -> Result<RankingActor, ()> {
        let credential = dashboard_credential(headers).ok_or(())?;
        let user = self
            .auth
            .self_user_view_for_optional(SecretString::from(credential))
            .await
            .map_err(|_| ())?;
        Ok(RankingActor {
            id: user.id,
            username: user.username,
            role: user.role,
            status: user.status,
            developer_access_granted: user.developer_access_granted,
        })
    }
}

#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
struct UserRankingsError(String);

#[async_trait]
trait UserRankingsStore: Send + Sync {
    async fn header_nav(&self) -> Result<HeaderNavAccess, UserRankingsError>;

    async fn snapshot(
        &self,
        period: RankingPeriod,
        updated_at: i64,
    ) -> Result<UserUsageRankingsResponse, UserRankingsError>;
}

#[derive(Clone)]
struct PgUserRankingsStore {
    pg: PgPool,
}

#[async_trait]
impl UserRankingsStore for PgUserRankingsStore {
    async fn header_nav(&self) -> Result<HeaderNavAccess, UserRankingsError> {
        let raw = sqlx::query_scalar::<_, String>(
            "SELECT value FROM options WHERE key = 'HeaderNavModules'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| database_error("query HeaderNavModules option row", error))?;
        let access = raw
            .filter(|value| !value.trim().is_empty())
            .and_then(|value| serde_json::from_str::<Value>(&value).ok())
            .and_then(|value| value.as_object().cloned())
            .map_or_else(HeaderNavAccess::default, |modules| {
                parse_header_nav_access(modules.get("rankings"))
            });
        Ok(access)
    }

    async fn snapshot(
        &self,
        period: RankingPeriod,
        updated_at: i64,
    ) -> Result<UserUsageRankingsResponse, UserRankingsError> {
        let start_time = updated_at - period.duration_seconds();
        let totals = sqlx::query_as::<_, (i64, i64, i64)>(
            r#"SELECT user_id::BIGINT,
                      COALESCE(SUM(count), 0)::BIGINT AS requests,
                      COALESCE(SUM(token_used), 0)::BIGINT AS total_tokens
               FROM quota_data
               WHERE user_id > 0 AND created_at >= $1 AND created_at <= $2
               GROUP BY user_id
               HAVING COALESCE(SUM(count), 0) > 0
                   OR COALESCE(SUM(token_used), 0) > 0
               ORDER BY total_tokens DESC, requests DESC"#,
        )
        .bind(start_time)
        .bind(updated_at)
        .fetch_all(&self.pg)
        .await
        .map_err(|error| database_error("query usage ranking aggregate rows", error))?
        .into_iter()
        .map(|(user_id, requests, total_tokens)| UserRankingTotal {
            user_id,
            requests,
            total_tokens,
        })
        .collect::<Vec<_>>();

        let user_ids = totals.iter().map(|total| total.user_id).collect::<Vec<_>>();
        let users = if user_ids.is_empty() {
            Vec::new()
        } else {
            sqlx::query_as::<_, (i64, String, String, i64, String)>(
                r#"SELECT id::BIGINT,
                          COALESCE(username, ''),
                          COALESCE(display_name, ''),
                          COALESCE(status, 0)::BIGINT,
                          COALESCE(setting, '')
                   FROM users
                   WHERE id = ANY($1) AND deleted_at IS NULL"#,
            )
            .bind(&user_ids)
            .fetch_all(&self.pg)
            .await
            .map_err(|error| database_error("query ranking user projection rows", error))?
            .into_iter()
            .map(
                |(id, username, display_name, status, setting)| RankingUser {
                    id,
                    username,
                    display_name,
                    status,
                    setting,
                },
            )
            .collect()
        };

        Ok(build_snapshot(period, updated_at, &totals, users))
    }
}

async fn user_usage_rankings(
    State(state): State<UserRankingsState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    let actor = match state.authorizer.actor(&headers).await {
        Ok(actor) if actor.developer_access_granted => actor,
        Ok(_) | Err(()) => return not_found(),
    };

    let access = state.header_nav().await;
    if !access.enabled {
        return failure(StatusCode::FORBIDDEN, "rankings is disabled");
    }
    if access.require_auth
        && let Err(error) = enforce_user_auth(&actor)
    {
        return user_policy_error(&headers, error);
    }

    let raw_period = parse_ranking_period(raw_query.as_deref());
    let Some(period) = RankingPeriod::parse(&raw_period) else {
        return with_auth_version(failure(
            StatusCode::BAD_REQUEST,
            &format!("invalid ranking period: {raw_period}"),
        ));
    };
    let updated_at = chrono::Utc::now().timestamp();
    let response = match state.store.snapshot(period, updated_at).await {
        Ok(snapshot) => success(snapshot),
        Err(error) => failure(StatusCode::BAD_REQUEST, &error.to_string()),
    };
    with_auth_version(response)
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum RankingPeriod {
    Today,
    Week,
    Month,
    Year,
}

impl RankingPeriod {
    fn parse(value: &str) -> Option<Self> {
        match value {
            "today" => Some(Self::Today),
            "week" => Some(Self::Week),
            "month" => Some(Self::Month),
            "year" => Some(Self::Year),
            _ => None,
        }
    }

    const fn id(self) -> &'static str {
        match self {
            Self::Today => "today",
            Self::Week => "week",
            Self::Month => "month",
            Self::Year => "year",
        }
    }

    const fn duration_seconds(self) -> i64 {
        match self {
            Self::Today => 24 * 60 * 60,
            Self::Week => 7 * 24 * 60 * 60,
            Self::Month => 30 * 24 * 60 * 60,
            Self::Year => 365 * 24 * 60 * 60,
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct UserRankingTotal {
    user_id: i64,
    requests: i64,
    total_tokens: i64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct RankingUser {
    id: i64,
    username: String,
    display_name: String,
    status: i64,
    setting: String,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
struct UserUsageRankingsResponse {
    period: &'static str,
    updated_at: i64,
    total_tokens: i64,
    total_requests: i64,
    participant_count: usize,
    anonymous_participant_count: usize,
    users: Vec<RankedUserUsage>,
}

#[derive(Clone, Debug, PartialEq, Serialize)]
struct RankedUserUsage {
    rank: usize,
    #[serde(skip_serializing_if = "Option::is_none")]
    name: Option<String>,
    anonymous: bool,
    total_tokens: i64,
    requests: i64,
    share: f64,
}

#[derive(Clone, Debug, Eq, PartialEq)]
struct UsageCandidate {
    user_id: i64,
    name: Option<String>,
    anonymous: bool,
    requests: i64,
    tokens: i64,
}

fn build_snapshot(
    period: RankingPeriod,
    updated_at: i64,
    totals: &[UserRankingTotal],
    users: Vec<RankingUser>,
) -> UserUsageRankingsResponse {
    let users_by_id = users
        .into_iter()
        .map(|user| (user.id, user))
        .collect::<BTreeMap<_, _>>();
    let mut candidates = Vec::<UsageCandidate>::new();
    let mut anonymous_index = None;
    let mut total_tokens = 0_i64;
    let mut total_requests = 0_i64;
    let mut participant_count = 0_usize;
    let mut anonymous_participant_count = 0_usize;

    for total in totals {
        let Some(user) = users_by_id.get(&total.user_id) else {
            continue;
        };
        if user.status != 1 {
            continue;
        }
        let visibility = usage_visibility(&user.setting);
        if visibility == UsageVisibility::Hidden {
            continue;
        }

        participant_count += 1;
        total_tokens += total.total_tokens;
        total_requests += total.requests;

        if visibility == UsageVisibility::Anonymous {
            anonymous_participant_count += 1;
            let index = *anonymous_index.get_or_insert_with(|| {
                candidates.push(UsageCandidate {
                    user_id: 0,
                    name: None,
                    anonymous: true,
                    requests: 0,
                    tokens: 0,
                });
                candidates.len() - 1
            });
            candidates[index].requests += total.requests;
            candidates[index].tokens += total.total_tokens;
            continue;
        }

        let display_name = user.display_name.trim();
        let username = user.username.trim();
        let name = if display_name.is_empty() {
            username
        } else {
            display_name
        };
        if name.is_empty() {
            continue;
        }
        candidates.push(UsageCandidate {
            user_id: user.id,
            name: Some(name.to_owned()),
            anonymous: false,
            requests: total.requests,
            tokens: total.total_tokens,
        });
    }

    candidates.sort_by(|left, right| {
        right
            .tokens
            .cmp(&left.tokens)
            .then_with(|| right.requests.cmp(&left.requests))
            .then_with(|| left.anonymous.cmp(&right.anonymous))
            .then_with(|| left.name.cmp(&right.name))
            .then_with(|| left.user_id.cmp(&right.user_id))
    });
    let users = candidates
        .into_iter()
        .take(LEADERBOARD_LIMIT)
        .enumerate()
        .map(|(index, candidate)| RankedUserUsage {
            rank: index + 1,
            name: candidate.name,
            anonymous: candidate.anonymous,
            total_tokens: candidate.tokens,
            requests: candidate.requests,
            share: if total_tokens > 0 {
                candidate.tokens as f64 / total_tokens as f64
            } else {
                0.0
            },
        })
        .collect();

    UserUsageRankingsResponse {
        period: period.id(),
        updated_at,
        total_tokens,
        total_requests,
        participant_count,
        anonymous_participant_count,
        users,
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum UsageVisibility {
    Public,
    Anonymous,
    Hidden,
}

fn usage_visibility(setting: &str) -> UsageVisibility {
    let raw = serde_json::from_str::<Value>(setting)
        .ok()
        .and_then(|setting| {
            setting
                .get("usage_leaderboard_visibility")
                .and_then(Value::as_str)
                .map(str::to_owned)
        })
        .map_or_else(String::new, std::convert::identity);
    match raw.trim().to_ascii_lowercase().as_str() {
        "public" => UsageVisibility::Public,
        "hidden" => UsageVisibility::Hidden,
        _ => UsageVisibility::Anonymous,
    }
}

fn enforce_user_auth(actor: &RankingActor) -> Result<(), UserAuthPolicyError> {
    if actor.status != 1 {
        return Err(UserAuthPolicyError::UserDisabled);
    }
    if actor.role < 1 {
        return Err(UserAuthPolicyError::InsufficientPrivilege);
    }
    if actor.id <= 0 || actor.username.trim().is_empty() || !matches!(actor.role, 0 | 1 | 10 | 100)
    {
        return Err(UserAuthPolicyError::InvalidUserInfo);
    }
    Ok(())
}

fn parse_ranking_period(raw_query: Option<&str>) -> String {
    let Some(raw_query) = raw_query else {
        return "week".to_owned();
    };
    let mut first_period = None;
    for pair in raw_query.split('&') {
        if pair.contains(';') {
            continue;
        }
        let (raw_key, raw_value) = pair
            .split_once('=')
            .map_or((pair, ""), std::convert::identity);
        let (Ok(key), Ok(value)) = (
            percent_decode_query(raw_key),
            percent_decode_query(raw_value),
        ) else {
            continue;
        };
        if key == b"period" && first_period.is_none() {
            first_period = Some(value);
        }
    }
    let Some(period) = first_period else {
        return "week".to_owned();
    };
    if period.is_empty() {
        return "week".to_owned();
    }
    String::from_utf8_lossy(&period).into_owned()
}

fn percent_decode_query(value: &str) -> Result<Vec<u8>, ()> {
    let bytes = value.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        match bytes[index] {
            b'%' => {
                let Some((high, low)) = bytes.get(index + 1).zip(bytes.get(index + 2)) else {
                    return Err(());
                };
                decoded.push((hex_value(*high)? << 4) | hex_value(*low)?);
                index += 3;
            }
            b'+' => {
                decoded.push(b' ');
                index += 1;
            }
            byte => {
                decoded.push(byte);
                index += 1;
            }
        }
    }
    Ok(decoded)
}

const fn hex_value(value: u8) -> Result<u8, ()> {
    match value {
        b'0'..=b'9' => Ok(value - b'0'),
        b'a'..=b'f' => Ok(value - b'a' + 10),
        b'A'..=b'F' => Ok(value - b'A' + 10),
        _ => Err(()),
    }
}

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    let value = headers.get(header::AUTHORIZATION)?.to_str().ok()?.trim();
    let mut fields = value.split_whitespace();
    let first = fields.next()?;
    let second = fields.next();
    if fields.next().is_some() {
        return None;
    }
    match second {
        Some(token) if first.eq_ignore_ascii_case("bearer") && !token.is_empty() => {
            Some(token.to_owned())
        }
        None if !first.is_empty() => Some(first.to_owned()),
        _ => None,
    }
}

fn user_policy_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    legacy_json(
        StatusCode::from_u16(user_auth_status(error))
            .map_or(StatusCode::UNAUTHORIZED, std::convert::identity),
        json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok()),
            ),
        }),
    )
}

fn success(snapshot: UserUsageRankingsResponse) -> Response {
    legacy_json(StatusCode::OK, json!({"success": true, "data": snapshot}))
}

fn failure(status: StatusCode, message: &str) -> Response {
    legacy_json(status, json!({"success": false, "message": message}))
}

fn not_found() -> Response {
    legacy_json(StatusCode::NOT_FOUND, json!({"message": "Not Found"}))
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn legacy_json(status: StatusCode, value: Value) -> Response {
    let mut response = (status, Json(value)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

fn database_error(context: &'static str, error: sqlx::Error) -> UserRankingsError {
    UserRankingsError(format!("{context}: {error}"))
}

#[cfg(test)]
mod tests {
    use std::sync::Arc;

    use axum::{
        body::{Body, to_bytes},
        http::{Request, header},
    };
    use serde_json::{Value, json};
    use tower::ServiceExt;

    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error + Send + Sync>>;

    fn test_error(
        context: &'static str,
        error: impl std::fmt::Display,
    ) -> Box<dyn std::error::Error + Send + Sync> {
        Box::new(std::io::Error::other(format!("{context}: {error}")))
    }

    #[derive(Clone)]
    struct FixedAuthorizer(Result<RankingActor, ()>);

    #[async_trait]
    impl UserRankingsAuthorizer for FixedAuthorizer {
        async fn actor(&self, _: &HeaderMap) -> Result<RankingActor, ()> {
            self.0.clone()
        }
    }

    #[derive(Clone)]
    struct FixedStore {
        nav: Result<HeaderNavAccess, UserRankingsError>,
        snapshot: Result<UserUsageRankingsResponse, UserRankingsError>,
    }

    #[async_trait]
    impl UserRankingsStore for FixedStore {
        async fn header_nav(&self) -> Result<HeaderNavAccess, UserRankingsError> {
            self.nav.clone()
        }

        async fn snapshot(
            &self,
            period: RankingPeriod,
            updated_at: i64,
        ) -> Result<UserUsageRankingsResponse, UserRankingsError> {
            self.snapshot.clone().map(|mut snapshot| {
                snapshot.period = period.id();
                snapshot.updated_at = updated_at;
                snapshot
            })
        }
    }

    fn activated_actor() -> RankingActor {
        RankingActor {
            id: 7,
            username: "ranked-user".to_owned(),
            role: 1,
            status: 1,
            developer_access_granted: true,
        }
    }

    fn empty_snapshot() -> UserUsageRankingsResponse {
        UserUsageRankingsResponse {
            period: "week",
            updated_at: 0,
            total_tokens: 0,
            total_requests: 0,
            participant_count: 0,
            anonymous_participant_count: 0,
            users: Vec::new(),
        }
    }

    fn test_app(
        actor: Result<RankingActor, ()>,
        nav: Result<HeaderNavAccess, UserRankingsError>,
    ) -> Router {
        router(UserRankingsState::with_dependencies(
            Arc::new(FixedStore {
                nav,
                snapshot: Ok(empty_snapshot()),
            }),
            Arc::new(FixedAuthorizer(actor)),
        ))
    }

    async fn response_json(response: Response) -> TestResult<Value> {
        let body = to_bytes(response.into_body(), usize::MAX)
            .await
            .map_err(|error| test_error("read user-ranking response body", error))?;
        serde_json::from_slice(&body)
            .map_err(|error| test_error("decode user-ranking response JSON", error))
    }

    #[test]
    fn build_snapshot_should_match_go_visibility_aggregation_and_sorting() -> TestResult {
        let totals = vec![
            UserRankingTotal {
                user_id: 1,
                requests: 2,
                total_tokens: 100,
            },
            UserRankingTotal {
                user_id: 2,
                requests: 4,
                total_tokens: 300,
            },
            UserRankingTotal {
                user_id: 3,
                requests: 10,
                total_tokens: 1_000,
            },
            UserRankingTotal {
                user_id: 4,
                requests: 8,
                total_tokens: 500,
            },
            UserRankingTotal {
                user_id: 5,
                requests: 1,
                total_tokens: 50,
            },
            UserRankingTotal {
                user_id: 6,
                requests: 1,
                total_tokens: 25,
            },
        ];
        let users = vec![
            RankingUser {
                id: 1,
                username: "anonymous-one".to_owned(),
                display_name: String::new(),
                status: 1,
                setting: String::new(),
            },
            RankingUser {
                id: 2,
                username: "public-user".to_owned(),
                display_name: " Public user ".to_owned(),
                status: 1,
                setting: r#"{"usage_leaderboard_visibility":"public"}"#.to_owned(),
            },
            RankingUser {
                id: 3,
                username: "hidden-user".to_owned(),
                display_name: String::new(),
                status: 1,
                setting: r#"{"usage_leaderboard_visibility":"hidden"}"#.to_owned(),
            },
            RankingUser {
                id: 4,
                username: "disabled-user".to_owned(),
                display_name: String::new(),
                status: 2,
                setting: String::new(),
            },
            RankingUser {
                id: 5,
                username: "anonymous-two".to_owned(),
                display_name: String::new(),
                status: 1,
                setting: r#"{"usage_leaderboard_visibility":7}"#.to_owned(),
            },
            RankingUser {
                id: 6,
                username: "  ".to_owned(),
                display_name: "  ".to_owned(),
                status: 1,
                setting: r#"{"usage_leaderboard_visibility":"PUBLIC"}"#.to_owned(),
            },
        ];

        let snapshot = build_snapshot(RankingPeriod::Week, 123, &totals, users);

        let snapshot_json = serde_json::to_value(snapshot)
            .map_err(|error| test_error("serialize user-ranking rows as JSON", error))?;
        assert_eq!(
            snapshot_json,
            json!({
                "period": "week",
                "updated_at": 123,
                "total_tokens": 475,
                "total_requests": 8,
                "participant_count": 4,
                "anonymous_participant_count": 2,
                "users": [
                    {"rank": 1, "name": "Public user", "anonymous": false, "total_tokens": 300, "requests": 4, "share": 300.0 / 475.0},
                    {"rank": 2, "anonymous": true, "total_tokens": 150, "requests": 3, "share": 150.0 / 475.0}
                ]
            })
        );
        Ok(())
    }

    #[test]
    fn parse_ranking_period_should_keep_go_defaults_and_first_value() -> TestResult {
        let periods = [
            (None, "week"),
            (Some("period="), "week"),
            (Some("period=month&period=year"), "month"),
            (Some("period=%6Donth"), "month"),
            (Some("period=%&period=today"), "today"),
            (Some("period=%FF"), "�"),
        ];

        assert_eq!(
            periods
                .into_iter()
                .map(|(query, _)| parse_ranking_period(query))
                .collect::<Vec<_>>(),
            periods
                .into_iter()
                .map(|(_, expected)| expected.to_owned())
                .collect::<Vec<_>>()
        );
        Ok(())
    }

    #[tokio::test]
    async fn route_should_conceal_anonymous_and_l0_requests_before_header_nav() -> TestResult {
        for actor in [
            Err(()),
            Ok(RankingActor {
                developer_access_granted: false,
                ..activated_actor()
            }),
        ] {
            let request = Request::get("/api/rankings/users")
                .body(Body::empty())
                .map_err(|error| test_error("build concealed user-ranking query request", error))?;
            let response = test_app(
                actor,
                Ok(HeaderNavAccess {
                    enabled: true,
                    require_auth: false,
                }),
            )
            .oneshot(request)
            .await
            .map_err(|error| test_error("serve concealed user-ranking query request", error))?;

            assert_eq!(response.status(), StatusCode::NOT_FOUND);
        }
        Ok(())
    }

    #[tokio::test]
    async fn route_should_reject_disabled_header_nav_without_auth_version() -> TestResult {
        let request = Request::get("/api/rankings/users")
            .body(Body::empty())
            .map_err(|error| test_error("build disabled user-ranking query request", error))?;
        let response = test_app(
            Ok(activated_actor()),
            Ok(HeaderNavAccess {
                enabled: false,
                require_auth: false,
            }),
        )
        .oneshot(request)
        .await
        .map_err(|error| test_error("serve disabled user-ranking query request", error))?;

        assert_eq!(
            (response.status(), response.headers().get("auth-version")),
            (StatusCode::FORBIDDEN, None)
        );
        Ok(())
    }

    #[tokio::test]
    async fn route_should_use_go_envelope_and_auth_version_for_valid_actor() -> TestResult {
        let request = Request::get("/api/rankings/users?period=year")
            .header(header::AUTHORIZATION, "Bearer dashboard")
            .body(Body::empty())
            .map_err(|error| test_error("build valid user-ranking query request", error))?;
        let response = test_app(
            Ok(activated_actor()),
            Ok(HeaderNavAccess {
                enabled: true,
                require_auth: true,
            }),
        )
        .oneshot(request)
        .await
        .map_err(|error| test_error("serve valid user-ranking query request", error))?;
        let status = response.status();
        let auth_version = response.headers().get("auth-version").cloned();
        let body = response_json(response).await?;

        assert_eq!(
            (
                status,
                auth_version,
                body["success"].clone(),
                body["data"]["period"].clone()
            ),
            (
                StatusCode::OK,
                Some(HeaderValue::from_static(AUTH_VERSION)),
                json!(true),
                json!("year")
            )
        );
        Ok(())
    }
}
