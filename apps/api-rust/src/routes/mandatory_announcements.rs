//! Current Go mandatory-announcement status and ordered acknowledgement contract.

use std::{collections::HashMap, sync::Arc};

use async_trait::async_trait;
use axum::{
    Router,
    body::to_bytes,
    extract::{Path, Request, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::Response,
    routing::{get, post},
};
use chrono::{DateTime, Utc};
use secrecy::SecretString;
use serde::{Deserialize, Serialize, de::DeserializeOwned};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Row};

use crate::{
    auth::{DashboardAuth, DashboardUserView, UserAuthPolicyError, enforce_user_auth_view},
    legacy_empty_response,
};

use super::legacy_http::{
    dashboard_credential, legacy_json, localized_dashboard_auth_error, localized_user_policy_error,
};

const BODY_LIMIT: usize = 1024;
const ORDER_ERROR: &str =
    "announcement changed or an earlier announcement must be acknowledged first";

#[derive(Clone)]
pub struct MandatoryAnnouncementState {
    store: Arc<dyn MandatoryAnnouncementStore>,
    auth: Arc<dyn DashboardAuth>,
}

impl MandatoryAnnouncementState {
    #[must_use]
    pub fn new(pool: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_store(Arc::new(PgMandatoryAnnouncementStore { pool }), auth)
    }

    #[must_use]
    pub fn with_store(
        store: Arc<dyn MandatoryAnnouncementStore>,
        auth: Arc<dyn DashboardAuth>,
    ) -> Self {
        Self { store, auth }
    }
}

pub fn router(state: MandatoryAnnouncementState) -> Router {
    Router::new()
        .route("/api/user/self/announcements", get(self_status))
        .route("/api/user/self/announcements/read", post(acknowledge))
        .route("/api/user/{id}/announcements", get(user_status))
        .with_state(state)
}

fn nullable_default<'de, D, T>(deserializer: D) -> Result<T, D::Error>
where
    D: serde::Deserializer<'de>,
    T: Deserialize<'de> + Default,
{
    Option::<T>::deserialize(deserializer).map(Option::unwrap_or_default)
}

#[derive(Clone, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(default)]
pub struct MandatoryAnnouncement {
    #[serde(deserialize_with = "nullable_default")]
    pub id: i64,
    #[serde(deserialize_with = "nullable_default")]
    pub content: String,
    #[serde(
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable_default"
    )]
    pub extra: String,
    #[serde(rename = "publishDate", deserialize_with = "nullable_default")]
    pub publish_date: String,
    #[serde(deserialize_with = "nullable_default")]
    pub mandatory: bool,
    #[serde(
        rename = "ackRevision",
        skip_serializing_if = "String::is_empty",
        deserialize_with = "nullable_default"
    )]
    pub ack_revision: String,
    #[serde(deserialize_with = "nullable_default")]
    pub revision: String,
    #[serde(deserialize_with = "nullable_default")]
    pub read_at: i64,
}

#[derive(Debug, thiserror::Error)]
pub enum AnnouncementError {
    #[error("unsupported data")]
    InvalidData,
    #[error("announcement changed or an earlier announcement must be acknowledged first")]
    Order,
    #[error("record not found")]
    NotFound,
    #[error("{0}")]
    Backend(String),
}

#[async_trait]
pub trait MandatoryAnnouncementStore: Send + Sync {
    async fn status(&self, user_id: i64) -> Result<Vec<MandatoryAnnouncement>, AnnouncementError>;
    async fn acknowledge(
        &self,
        user_id: i64,
        id: i64,
        revision: &str,
    ) -> Result<(), AnnouncementError>;
    async fn target_role(&self, user_id: i64) -> Result<i64, AnnouncementError>;
}

struct PgMandatoryAnnouncementStore {
    pool: PgPool,
}

fn backend_error(error: impl std::fmt::Display) -> AnnouncementError {
    AnnouncementError::Backend(error.to_string())
}

fn announcement_revision(item: &MandatoryAnnouncement) -> Result<String, AnnouncementError> {
    let generation = item.ack_revision.trim();
    let fields = if generation.is_empty() {
        json!([item.id, item.content, item.extra, item.publish_date])
    } else {
        json!([item.id, generation])
    };
    // Go encoding/json escapes these five characters even inside array fields.
    // Hashing serde_json's default encoding would invalidate existing reads.
    let encoded = serde_json::to_string(&fields)
        .map_err(backend_error)?
        .replace('&', "\\u0026")
        .replace('<', "\\u003c")
        .replace('>', "\\u003e")
        .replace('\u{2028}', "\\u2028")
        .replace('\u{2029}', "\\u2029");
    Ok(hex::encode(Sha256::digest(encoded.as_bytes())))
}

fn current_announcements(
    raw: &str,
    now: DateTime<Utc>,
) -> Result<Vec<MandatoryAnnouncement>, AnnouncementError> {
    if raw.is_empty() {
        return Ok(Vec::new());
    }
    let all: Option<Vec<Option<MandatoryAnnouncement>>> =
        serde_json::from_str(raw).map_err(backend_error)?;
    let mut published = Vec::new();
    for mut item in all.unwrap_or_default().into_iter().flatten() {
        if !item.mandatory {
            continue;
        }
        let timestamp = DateTime::parse_from_rfc3339(&item.publish_date).map_err(backend_error)?;
        if timestamp > now {
            continue;
        }
        item.revision = announcement_revision(&item)?;
        item.read_at = 0;
        published.push((timestamp, item));
    }
    published.sort_by(|(left_time, left), (right_time, right)| {
        left_time.cmp(right_time).then(left.id.cmp(&right.id))
    });
    Ok(published.into_iter().map(|(_, item)| item).collect())
}

async fn status_on_connection(
    connection: &mut PgConnection,
    user_id: i64,
) -> Result<Vec<MandatoryAnnouncement>, AnnouncementError> {
    let raw: Option<String> =
        sqlx::query_scalar("SELECT value FROM options WHERE key = 'console_setting.announcements'")
            .fetch_optional(&mut *connection)
            .await
            .map_err(backend_error)?;
    let mut items = current_announcements(raw.as_deref().unwrap_or_default(), Utc::now())?;
    if items.is_empty() {
        return Ok(items);
    }
    let ids: Vec<i64> = items.iter().map(|item| item.id).collect();
    let rows = sqlx::query(
        "SELECT revision, read_at::BIGINT AS read_at FROM announcement_reads \
         WHERE user_id = $1 AND announcement_id = ANY($2)",
    )
    .bind(user_id)
    .bind(ids)
    .fetch_all(&mut *connection)
    .await
    .map_err(backend_error)?;
    let mut read_by_revision = HashMap::new();
    for row in rows {
        let revision: String = row.try_get("revision").map_err(backend_error)?;
        let read_at: i64 = row.try_get("read_at").map_err(backend_error)?;
        read_by_revision.insert(revision, read_at);
    }
    for item in &mut items {
        item.read_at = read_by_revision
            .get(&item.revision)
            .copied()
            .unwrap_or_default();
    }
    Ok(items)
}

#[async_trait]
impl MandatoryAnnouncementStore for PgMandatoryAnnouncementStore {
    async fn status(&self, user_id: i64) -> Result<Vec<MandatoryAnnouncement>, AnnouncementError> {
        if user_id <= 0 {
            return Err(AnnouncementError::InvalidData);
        }
        let mut connection = self.pool.acquire().await.map_err(backend_error)?;
        status_on_connection(&mut connection, user_id).await
    }

    async fn acknowledge(
        &self,
        user_id: i64,
        id: i64,
        revision: &str,
    ) -> Result<(), AnnouncementError> {
        if user_id <= 0 || id <= 0 || revision.len() != 64 {
            return Err(AnnouncementError::InvalidData);
        }
        let mut transaction = self.pool.begin().await.map_err(backend_error)?;
        let user: Option<i64> = sqlx::query_scalar(
            "SELECT id::BIGINT FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
        )
        .bind(user_id)
        .fetch_optional(&mut *transaction)
        .await
        .map_err(backend_error)?;
        if user.is_none() {
            return Err(AnnouncementError::NotFound);
        }
        let items = status_on_connection(&mut transaction, user_id).await?;
        if items
            .iter()
            .any(|item| item.id == id && item.revision == revision && item.read_at > 0)
        {
            transaction.commit().await.map_err(backend_error)?;
            return Ok(());
        }
        let first = items
            .iter()
            .find(|item| item.read_at <= 0)
            .ok_or(AnnouncementError::Order)?;
        if first.id != id || first.revision != revision {
            return Err(AnnouncementError::Order);
        }
        sqlx::query(
            "INSERT INTO announcement_reads (user_id, announcement_id, revision, read_at) \
             VALUES ($1, $2, $3, $4) ON CONFLICT DO NOTHING",
        )
        .bind(user_id)
        .bind(id)
        .bind(revision)
        .bind(Utc::now().timestamp())
        .execute(&mut *transaction)
        .await
        .map_err(backend_error)?;
        transaction.commit().await.map_err(backend_error)?;
        Ok(())
    }

    async fn target_role(&self, user_id: i64) -> Result<i64, AnnouncementError> {
        if user_id == 0 {
            return Err(AnnouncementError::Backend("id 为空！".to_owned()));
        }
        sqlx::query_scalar::<_, i64>(
            "SELECT COALESCE(role, 1)::BIGINT FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pool)
        .await
        .map_err(backend_error)?
        .ok_or(AnnouncementError::NotFound)
    }
}

async fn principal(
    state: &MandatoryAnnouncementState,
    headers: &HeaderMap,
    admin: bool,
) -> Result<DashboardUserView, Response> {
    let credential = dashboard_credential(headers)
        .ok_or_else(|| localized_dashboard_auth_error(headers, None))?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| localized_dashboard_auth_error(headers, Some(error.kind)))?;
    if admin && !user.developer_access_granted {
        return Err(legacy_json(
            StatusCode::NOT_FOUND,
            json!({"message":"Not Found"}),
        ));
    }
    enforce_user_auth_view(&user).map_err(|error| localized_user_policy_error(headers, error))?;
    if admin && user.role < 10 {
        return Err(localized_user_policy_error(
            headers,
            UserAuthPolicyError::InsufficientPrivilege,
        ));
    }
    Ok(user)
}

async fn self_status(
    State(state): State<MandatoryAnnouncementState>,
    headers: HeaderMap,
) -> Response {
    let user = match principal(&state, &headers, false).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    authenticated_response(status_response(state.store.status(user.id).await))
}

#[derive(Default)]
struct Acknowledgement {
    id: i64,
    revision: String,
}

impl<'de> Deserialize<'de> for Acknowledgement {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = Acknowledgement;
            fn expecting(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                formatter.write_str("an announcement acknowledgement object or null")
            }
            fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
                Ok(Acknowledgement::default())
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut result = Acknowledgement::default();
                while let Some(key) = map.next_key::<String>()? {
                    if key.eq_ignore_ascii_case("id") {
                        if let Some(id) = map.next_value::<Option<i64>>()? {
                            result.id = id;
                        }
                    } else if key.eq_ignore_ascii_case("revision") {
                        if let Some(revision) = map.next_value::<Option<String>>()? {
                            result.revision = revision;
                        }
                    } else {
                        let _ = map.next_value::<serde::de::IgnoredAny>()?;
                    }
                }
                Ok(result)
            }
        }
        deserializer.deserialize_any(Visitor)
    }
}

fn decode_first_json<T: DeserializeOwned>(bytes: &[u8]) -> Result<T, serde_json::Error> {
    // Gin's ShouldBindJSON decodes one JSON value and does not demand EOF.
    T::deserialize(&mut serde_json::Deserializer::from_slice(bytes))
}

async fn acknowledge(
    State(state): State<MandatoryAnnouncementState>,
    request: Request,
) -> Response {
    let user = match principal(&state, request.headers(), false).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    if request
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<u64>().ok())
        .is_some_and(|length| length > BODY_LIMIT as u64)
    {
        return authenticated_response(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
    }
    let input: Acknowledgement = match to_bytes(request.into_body(), BODY_LIMIT).await {
        Ok(bytes) => match decode_first_json(&bytes) {
            Ok(input) => input,
            Err(_) => return authenticated_response(invalid_acknowledgement()),
        },
        Err(_) => return authenticated_response(invalid_acknowledgement()),
    };
    let response = match state
        .store
        .acknowledge(user.id, input.id, &input.revision)
        .await
    {
        Ok(()) => status_response(state.store.status(user.id).await),
        Err(AnnouncementError::Order) => legacy_json(
            StatusCode::CONFLICT,
            json!({"success":false,"message":ORDER_ERROR}),
        ),
        Err(error) => api_error(error.to_string()),
    };
    authenticated_response(response)
}

async fn user_status(
    State(state): State<MandatoryAnnouncementState>,
    Path(raw_id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let user = match principal(&state, &headers, true).await {
        Ok(user) => user,
        Err(response) => return response,
    };
    let target_id = match raw_id.parse::<i64>() {
        Ok(id) => id,
        Err(error) => {
            let reason = if matches!(
                error.kind(),
                std::num::IntErrorKind::PosOverflow | std::num::IntErrorKind::NegOverflow
            ) {
                "value out of range"
            } else {
                "invalid syntax"
            };
            return authenticated_response(api_error(format!(
                "strconv.Atoi: parsing {raw_id:?}: {reason}"
            )));
        }
    };
    let response = match state.store.target_role(target_id).await {
        Ok(role) if user.role == 100 || user.role > role => {
            status_response(state.store.status(target_id).await)
        }
        Ok(_) => legacy_empty_response(StatusCode::FORBIDDEN, None),
        Err(error) => api_error(error.to_string()),
    };
    authenticated_response(response)
}

fn invalid_acknowledgement() -> Response {
    legacy_json(
        StatusCode::BAD_REQUEST,
        json!({"success":false,"message":"Invalid announcement acknowledgement"}),
    )
}

fn status_response(status: Result<Vec<MandatoryAnnouncement>, AnnouncementError>) -> Response {
    match status {
        Ok(items) => legacy_json(
            StatusCode::OK,
            json!({"success":true,"message":"","data":items}),
        ),
        Err(error) => api_error(error.to_string()),
    }
}

fn api_error(message: String) -> Response {
    legacy_json(StatusCode::OK, json!({"success":false,"message":message}))
}

fn authenticated_response(mut response: Response) -> Response {
    for (name, value) in [
        (
            HeaderName::from_static("auth-version"),
            "864b7076dbcd0a3c01b5520316720ebf",
        ),
        (
            header::CACHE_CONTROL,
            "no-store, no-cache, must-revalidate, private, max-age=0",
        ),
        (header::PRAGMA, "no-cache"),
        (header::EXPIRES, "0"),
    ] {
        response
            .headers_mut()
            .insert(name, HeaderValue::from_static(value));
    }
    response
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn announcement_revision_matches_go_json_bytes_including_html_and_unicode() {
        let item = MandatoryAnnouncement {
            id: 7,
            content: "<notice>&\u{2028}\u{2029}".to_owned(),
            extra: "extra".to_owned(),
            publish_date: "2026-01-01T00:00:00Z".to_owned(),
            ..MandatoryAnnouncement::default()
        };
        let expected_bytes =
            br#"[7,"\u003cnotice\u003e\u0026\u2028\u2029","extra","2026-01-01T00:00:00Z"]"#;
        assert_eq!(
            announcement_revision(&item).unwrap(),
            hex::encode(Sha256::digest(expected_bytes))
        );
    }

    #[test]
    fn pinned_generation_survives_edits_and_bump_reopens_acknowledgement() {
        let mut item = MandatoryAnnouncement {
            id: 1,
            ack_revision: " 1 ".to_owned(),
            ..Default::default()
        };
        let revision = announcement_revision(&item).unwrap();
        item.content = "Corrected notice".to_owned();
        item.extra = "Typo fixed".to_owned();
        assert_eq!(announcement_revision(&item).unwrap(), revision);
        item.ack_revision = "2".to_owned();
        assert_ne!(announcement_revision(&item).unwrap(), revision);
        item.ack_revision.clear();
        let legacy = announcement_revision(&item).unwrap();
        item.content.push('!');
        assert_ne!(announcement_revision(&item).unwrap(), legacy);
    }

    #[test]
    fn status_filters_future_and_optional_then_sorts_instants_and_ids() {
        let raw = r#"[
          {"id":2,"publishDate":"2025-01-01T01:00:00+01:00","mandatory":true},
          {"id":1,"publishDate":"2025-01-01T00:00:00Z","mandatory":true,"read_at":123},
          {"id":3,"publishDate":"2099-01-01T00:00:00Z","mandatory":true},
          {"id":4,"publishDate":"invalid","mandatory":false}, null
        ]"#;
        let now = DateTime::parse_from_rfc3339("2026-01-01T00:00:00Z")
            .unwrap()
            .with_timezone(&Utc);
        let items = current_announcements(raw, now).unwrap();
        assert_eq!(items.iter().map(|item| item.id).collect::<Vec<_>>(), [1, 2]);
        assert!(
            items
                .iter()
                .all(|item| item.read_at == 0 && item.revision.len() == 64)
        );
        assert!(current_announcements("null", now).unwrap().is_empty());
        assert!(
            current_announcements(r#"[{"mandatory":true,"publishDate":"invalid"}]"#, now).is_err()
        );
    }

    #[test]
    fn acknowledgement_decode_matches_go_null_unknown_case_and_first_value() {
        let input: Acknowledgement = decode_first_json(
            br#"{"ID":1,"ReViSiOn":"first","revision":null,"id":2,"unknown":[]} {}"#,
        )
        .unwrap();
        assert_eq!(input.id, 2);
        assert_eq!(input.revision, "first");
        assert_eq!(decode_first_json::<Acknowledgement>(b"null").unwrap().id, 0);
        for malformed in [b"[]".as_slice(), br#"{"id":"1"}"#, br#"{"id":1.0}"#, b"{"] {
            assert!(decode_first_json::<Acknowledgement>(malformed).is_err());
        }
    }
}
