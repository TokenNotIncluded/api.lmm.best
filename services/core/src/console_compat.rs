//! Persisted Web console contract; no runtime schema or initialization writes.
//!
//! Adapted from TokenNotIncluded/api.lmm.best main 7be6ee81a5089a558cad34d95851bcc8bb80061c:
//! apps/api-go/model/mandatory_announcement.go and controller/{mandatory_announcement,setup,misc}.go;
//! apps/api-rust/src/routes/mandatory_announcements.rs (AGPL-3.0-or-later).
//! Preserve the Go revision bytes and its transaction-captured acknowledgement response.
use crate::identity::{self, CredentialKind, IdentityError, IdentityStore, Principal};
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::{get, post},
};
use chrono::DateTime;
use serde::{Deserialize, Serialize};
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Row};
use std::collections::HashMap;

const BODY_LIMIT: usize = 1024;
const ORDER_ERROR: &str =
    "announcement changed or an earlier announcement must be acknowledged first";
// Canonical main apps/api-go/common/auth_version.txt, retained byte-for-byte.
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";

#[derive(Debug)]
pub enum ConsoleError {
    Identity(IdentityError),
    Invalid,
    Order,
    NotFound,
    Storage,
}
impl From<IdentityError> for ConsoleError {
    fn from(value: IdentityError) -> Self {
        Self::Identity(value)
    }
}
impl From<sqlx::Error> for ConsoleError {
    fn from(_: sqlx::Error) -> Self {
        Self::Storage
    }
}
impl std::fmt::Display for ConsoleError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "console operation failed: {self:?}")
    }
}
impl std::error::Error for ConsoleError {}

#[derive(Clone)]
pub struct ConsoleCompat {
    pool: PgPool,
    identity: IdentityStore,
}
impl ConsoleCompat {
    pub fn new(pool: PgPool, identity: IdentityStore) -> Self {
        Self { pool, identity }
    }
    async fn authorize(&self, secret: &str) -> Result<Principal, ConsoleError> {
        session(self.identity.authorize(secret).await?)
    }
    pub async fn announcements(
        &self,
        secret: &str,
        target: Option<i64>,
    ) -> Result<Vec<MandatoryAnnouncement>, ConsoleError> {
        let mut tx = self.pool.begin().await?;
        let actor = session(identity::authorize_in_transaction(&mut tx, secret).await?)?;
        let user = if let Some(target) = target {
            if actor.platform_level < 5 {
                return Err(IdentityError::Forbidden.into());
            }
            let role: String = sqlx::query_scalar(
                "SELECT platform_role FROM core_identity.users WHERE id=$1 FOR SHARE",
            )
            .bind(target)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or(ConsoleError::NotFound)?;
            // native levels 0..4 are user service tiers; only 5/6 are platform
            // admin/root. Main automatically grants both developer access.
            if actor.platform_level != 6 && role != "user" {
                return Err(IdentityError::Forbidden.into());
            }
            target
        } else {
            actor.user_id
        };
        let result = status_on_connection(&mut tx, user).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn acknowledge(
        &self,
        secret: &str,
        id: i64,
        revision: &str,
    ) -> Result<Vec<MandatoryAnnouncement>, ConsoleError> {
        let initial = self.authorize(secret).await?;
        if id <= 0 || revision.len() != 64 {
            return Err(ConsoleError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        // Acquire the account serialization lock before identity row locks.
        // No user SHARE -> UPDATE upgrade between concurrent acknowledgements.
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended('core_console.announcement:'||$1::text,0))")
            .bind(initial.user_id).execute(&mut *tx).await?;
        let actor = session(identity::authorize_in_transaction(&mut tx, secret).await?)?;
        let mut items = status_on_connection(&mut tx, actor.user_id).await?;
        if items
            .iter()
            .any(|item| item.id == id && item.revision == revision && item.read_at > 0)
        {
            tx.commit().await?;
            return Ok(items);
        }
        let first = items
            .iter_mut()
            .find(|item| item.read_at <= 0)
            .ok_or(ConsoleError::Order)?;
        if first.id != id || first.revision != revision {
            return Err(ConsoleError::Order);
        }
        let read_at: i64 = sqlx::query_scalar(
            "INSERT INTO core_console.announcement_reads(user_id,announcement_id,revision)
             VALUES ($1,$2,$3) RETURNING read_at",
        )
        .bind(actor.user_id)
        .bind(id)
        .bind(revision)
        .fetch_one(&mut *tx)
        .await?;
        first.read_at = read_at;
        // Return exactly this transaction's snapshot. Never query after commit.
        tx.commit().await?;
        Ok(items)
    }
    pub async fn setup_status(&self) -> Result<SetupStatus, ConsoleError> {
        crate::core_schema::check(&self.pool).await?;
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        let (records, roots): (i64, i64) = sqlx::query_as(
            "SELECT (SELECT count(*) FROM core_console.initialization),
             (SELECT count(*) FROM core_identity.users u JOIN core_identity.accounts a
              ON a.id=u.personal_account_id
              WHERE u.platform_role='superadmin' AND u.active AND a.active)",
        )
        .fetch_one(&mut *tx)
        .await?;
        if records > 1 || (records == 1 && roots == 0) {
            return Err(ConsoleError::Storage);
        }
        tx.commit().await?;
        Ok(SetupStatus {
            status: records == 1 && roots > 0,
            root_init: roots > 0,
            database_type: "postgres",
            initialization_method: "cli",
        })
    }
    pub async fn notice(&self) -> Result<String, ConsoleError> {
        Ok(
            sqlx::query_scalar("SELECT value FROM core_console.options WHERE key='Notice'")
                .fetch_optional(&self.pool)
                .await?
                .unwrap_or_default(),
        )
    }
}
fn session(actor: Principal) -> Result<Principal, ConsoleError> {
    if actor.credential_kind != CredentialKind::Session {
        return Err(IdentityError::Forbidden.into());
    }
    Ok(actor)
}

#[derive(Debug, Serialize)]
pub struct SetupStatus {
    pub status: bool,
    pub root_init: bool,
    pub database_type: &'static str,
    pub initialization_method: &'static str,
}
/// Offline owner-only completion after explicit init/import/bootstrap. Runtime
/// receives SELECT only on initialization and must never invoke this helper.
pub async fn record_initialization(pool: &PgPool, version: &str) -> Result<(), ConsoleError> {
    if version.is_empty() || version.len() > 128 {
        return Err(ConsoleError::Invalid);
    }
    crate::core_schema::check(pool).await?;
    let mut tx = pool.begin().await?;
    sqlx::query("SELECT pg_advisory_xact_lock(741803133)")
        .execute(&mut *tx)
        .await?;
    let roots: Vec<i64> = sqlx::query_scalar(
        "SELECT u.id FROM core_identity.users u JOIN core_identity.accounts a
         ON a.id=u.personal_account_id
         WHERE u.platform_role='superadmin' AND u.active AND a.active FOR SHARE OF u,a",
    )
    .fetch_all(&mut *tx)
    .await?;
    if roots.is_empty() {
        return Err(IdentityError::Forbidden.into());
    }
    let existing: Option<String> = sqlx::query_scalar(
        "SELECT version FROM core_console.initialization WHERE singleton FOR UPDATE",
    )
    .fetch_optional(&mut *tx)
    .await?;
    match existing {
        Some(existing) if existing != version => return Err(IdentityError::Conflict.into()),
        Some(_) => (),
        None => {
            sqlx::query("INSERT INTO core_console.initialization(version) VALUES ($1)")
                .bind(version)
                .execute(&mut *tx)
                .await?;
        }
    }
    tx.commit().await?;
    Ok(())
}

#[derive(Clone, Debug, Default, Serialize, PartialEq, Eq)]
pub struct MandatoryAnnouncement {
    pub id: i64,
    pub content: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pub extra: String,
    #[serde(rename = "publishDate")]
    pub publish_date: String,
    pub mandatory: bool,
    #[serde(rename = "ackRevision", skip_serializing_if = "String::is_empty")]
    pub ack_revision: String,
    pub revision: String,
    pub read_at: i64,
}
// Go encoding/json folds ASCII names, including the two non-ASCII SimpleFold
// members of ASCII letter groups. Null leaves an existing scalar unchanged.
fn go_field(key: &str) -> String {
    key.chars()
        .map(|c| match c {
            '\u{017f}' => 's',
            '\u{212a}' => 'k',
            _ => c.to_ascii_lowercase(),
        })
        .collect()
}
impl<'de> Deserialize<'de> for MandatoryAnnouncement {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = MandatoryAnnouncement;
            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("an announcement object")
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut item = MandatoryAnnouncement::default();
                macro_rules! scalar {
                    ($field:ident,$ty:ty) => {
                        if let Some(value) = map.next_value::<Option<$ty>>()? {
                            item.$field = value;
                        }
                    };
                }
                while let Some(key) = map.next_key::<String>()? {
                    match go_field(&key).as_str() {
                        "id" => {
                            scalar!(id, i64)
                        }
                        "content" => {
                            scalar!(content, String)
                        }
                        "extra" => {
                            scalar!(extra, String)
                        }
                        "publishdate" => {
                            scalar!(publish_date, String)
                        }
                        "mandatory" => {
                            scalar!(mandatory, bool)
                        }
                        "ackrevision" => {
                            scalar!(ack_revision, String)
                        }
                        "revision" => {
                            scalar!(revision, String)
                        }
                        "read_at" => {
                            scalar!(read_at, i64)
                        }
                        _ => {
                            let _ = map.next_value::<serde::de::IgnoredAny>()?;
                        }
                    }
                }
                Ok(item)
            }
        }
        deserializer.deserialize_map(Visitor)
    }
}
pub fn announcement_revision(item: &MandatoryAnnouncement) -> Result<String, ConsoleError> {
    let generation = item.ack_revision.trim();
    let fields = if generation.is_empty() {
        json!([item.id, item.content, item.extra, item.publish_date])
    } else {
        json!([item.id, generation])
    };
    let encoded = serde_json::to_string(&fields)
        .map_err(|_| ConsoleError::Storage)?
        .replace('&', "\\u0026")
        .replace('<', "\\u003c")
        .replace('>', "\\u003e")
        .replace('\u{2028}', "\\u2028")
        .replace('\u{2029}', "\\u2029");
    Ok(format!("{:x}", Sha256::digest(encoded.as_bytes())))
}
// Go time.Parse(RFC3339) also accepts comma fractions, one-digit hours,
// fractions beyond nine digits (truncated), and offset hour24/minute60.
// Normalize only for comparison; revision hashing always retains the raw text.
fn publication_instant(raw: &str) -> Result<(i64, u32), ConsoleError> {
    let invalid = || ConsoleError::Storage;
    let (body, offset) = if let Some(body) = raw.strip_suffix('Z') {
        (body, 0)
    } else {
        let suffix = raw
            .get(raw.len().checked_sub(6).ok_or_else(invalid)?..)
            .ok_or_else(invalid)?;
        let bytes = suffix.as_bytes();
        if !matches!(bytes[0], b'+' | b'-')
            || bytes[3] != b':'
            || !bytes[1..3]
                .iter()
                .chain(&bytes[4..6])
                .all(u8::is_ascii_digit)
        {
            return Err(ConsoleError::Storage);
        }
        let hours = suffix[1..3]
            .parse::<i64>()
            .map_err(|_| ConsoleError::Storage)?;
        let minutes = suffix[4..6]
            .parse::<i64>()
            .map_err(|_| ConsoleError::Storage)?;
        if hours > 24 || minutes > 60 {
            return Err(ConsoleError::Storage);
        }
        let sign = if bytes[0] == b'+' { 1 } else { -1 };
        (&raw[..raw.len() - 6], sign * (hours * 3600 + minutes * 60))
    };
    let (date, time) = body.split_once('T').ok_or_else(invalid)?;
    if date.len() != 10
        || date.as_bytes()[4] != b'-'
        || date.as_bytes()[7] != b'-'
        || !date
            .bytes()
            .enumerate()
            .all(|(i, b)| matches!(i, 4 | 7) || b.is_ascii_digit())
    {
        return Err(ConsoleError::Storage);
    }
    let (clock, fraction) = time
        .split_once(['.', ','])
        .map_or((time, None), |(a, b)| (a, Some(b)));
    let fields: Vec<_> = clock.split(':').collect();
    if fields.len() != 3
        || !(1..=2).contains(&fields[0].len())
        || fields[1].len() != 2
        || fields[2].len() != 2
        || !fields.iter().all(|s| s.bytes().all(|b| b.is_ascii_digit()))
    {
        return Err(ConsoleError::Storage);
    }
    let hour = fields[0].parse::<u8>().map_err(|_| ConsoleError::Storage)?;
    let second = fields[2].parse::<u8>().map_err(|_| ConsoleError::Storage)?;
    if second > 59 {
        return Err(ConsoleError::Storage);
    }
    let fraction = if let Some(fraction) = fraction {
        if fraction.is_empty() || !fraction.bytes().all(|b| b.is_ascii_digit()) {
            return Err(ConsoleError::Storage);
        }
        format!(".{}", &fraction[..fraction.len().min(9)])
    } else {
        String::new()
    };
    let normalized = format!("{date}T{hour:02}:{}:{}{fraction}Z", fields[1], fields[2]);
    let timestamp = DateTime::parse_from_rfc3339(&normalized).map_err(|_| ConsoleError::Storage)?;
    Ok((
        timestamp.timestamp() - offset,
        timestamp.timestamp_subsec_nanos(),
    ))
}
fn current_announcements(raw: &str, now: i64) -> Result<Vec<MandatoryAnnouncement>, ConsoleError> {
    if raw.is_empty() {
        return Ok(Vec::new());
    }
    let all: Option<Vec<Option<MandatoryAnnouncement>>> =
        serde_json::from_str(raw).map_err(|_| ConsoleError::Storage)?;
    let mut published = Vec::new();
    for mut item in all.unwrap_or_default().into_iter().flatten() {
        if !item.mandatory {
            continue;
        }
        let timestamp = publication_instant(&item.publish_date)?;
        if timestamp
            > (
                now.div_euclid(1_000_000),
                (now.rem_euclid(1_000_000) * 1000) as u32,
            )
        {
            continue;
        }
        item.revision = announcement_revision(&item)?;
        item.read_at = 0;
        published.push((timestamp, item));
    }
    published.sort_by(|(a_time, a), (b_time, b)| a_time.cmp(b_time).then(a.id.cmp(&b.id)));
    Ok(published.into_iter().map(|(_, item)| item).collect())
}
async fn status_on_connection(
    connection: &mut PgConnection,
    user: i64,
) -> Result<Vec<MandatoryAnnouncement>, ConsoleError> {
    let rows = sqlx::query(
        "SELECT key,value FROM core_console.options
         WHERE key IN ('console_setting.announcements','Announcements') ORDER BY key",
    )
    .fetch_all(&mut *connection)
    .await?;
    let options: HashMap<String, String> = rows
        .into_iter()
        .map(|row| Ok((row.try_get("key")?, row.try_get("value")?)))
        .collect::<Result<_, sqlx::Error>>()?;
    let now: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp())*1000000)::BIGINT")
            .fetch_one(&mut *connection)
            .await?;
    let mut items = current_announcements(
        options
            .get("console_setting.announcements")
            .or_else(|| options.get("Announcements"))
            .map_or("", String::as_str),
        now,
    )?;
    let rows = sqlx::query(
        "SELECT announcement_id,revision,read_at FROM core_console.announcement_reads WHERE user_id=$1",
    )
    .bind(user)
    .fetch_all(connection)
    .await?;
    let reads: HashMap<(i64, String), i64> = rows
        .into_iter()
        .map(|row| {
            Ok((
                (row.try_get("announcement_id")?, row.try_get("revision")?),
                row.try_get("read_at")?,
            ))
        })
        .collect::<Result<_, sqlx::Error>>()?;
    for item in &mut items {
        item.read_at = reads
            .get(&(item.id, item.revision.clone()))
            .copied()
            .unwrap_or_default();
    }
    Ok(items)
}

pub fn router(state: ConsoleCompat) -> Router {
    Router::new()
        .route("/api/user/self/announcements", get(self_announcements))
        .route("/api/user/self/announcements/read", post(acknowledge))
        .route("/api/user/{id}/announcements", get(user_announcements))
        .route("/api/setup", get(setup).post(setup_cli_only))
        .route("/api/notice", get(notice))
        .layer(middleware::from_fn(no_cache))
        .with_state(state)
}
async fn no_cache(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    for (name, value) in [
        (
            header::CACHE_CONTROL,
            "no-store, no-cache, must-revalidate, private, max-age=0",
        ),
        (header::PRAGMA, "no-cache"),
        (header::EXPIRES, "0"),
        (
            header::HeaderName::from_static("auth-version"),
            AUTH_VERSION,
        ),
    ] {
        response
            .headers_mut()
            .insert(name, HeaderValue::from_static(value));
    }
    response
}
fn bearer(headers: &HeaderMap) -> Result<&str, ConsoleError> {
    let mut values = headers.get_all(header::AUTHORIZATION).iter();
    let raw = values.next().ok_or(IdentityError::Unauthorized)?;
    if values.next().is_some() {
        return Err(IdentityError::Unauthorized.into());
    }
    let mut fields = raw
        .to_str()
        .map_err(|_| IdentityError::Unauthorized)?
        .split_whitespace();
    match (fields.next(), fields.next(), fields.next()) {
        (Some(scheme), Some(secret), None) if scheme.eq_ignore_ascii_case("Bearer") => Ok(secret),
        _ => Err(IdentityError::Unauthorized.into()),
    }
}
fn success(data: impl Serialize) -> Response {
    (
        StatusCode::OK,
        Json(json!({"success":true,"message":"","data":data})),
    )
        .into_response()
}
fn failure(status: StatusCode, code: &str, message: &str) -> Response {
    (
        status,
        Json(json!({"success":false,"code":code,"message":message})),
    )
        .into_response()
}
fn error(error: ConsoleError) -> Response {
    match error {
        ConsoleError::Identity(IdentityError::Unauthorized) => failure(
            StatusCode::UNAUTHORIZED,
            "AUTH_UNAUTHORIZED",
            "Authentication is required",
        ),
        ConsoleError::Identity(IdentityError::Forbidden) => failure(
            StatusCode::FORBIDDEN,
            "CONSOLE_FORBIDDEN",
            "Insufficient permission",
        ),
        ConsoleError::Identity(IdentityError::Conflict) | ConsoleError::Order => {
            failure(StatusCode::CONFLICT, "ANNOUNCEMENT_ORDER", ORDER_ERROR)
        }
        ConsoleError::Identity(IdentityError::Invalid) | ConsoleError::Invalid => failure(
            StatusCode::BAD_REQUEST,
            "CONSOLE_INVALID",
            "Invalid announcement acknowledgement",
        ),
        ConsoleError::NotFound => {
            failure(StatusCode::NOT_FOUND, "CONSOLE_NOT_FOUND", "User not found")
        }
        _ => failure(
            StatusCode::SERVICE_UNAVAILABLE,
            "CONSOLE_UNAVAILABLE",
            "Console state is temporarily unavailable",
        ),
    }
}
async fn self_announcements(State(state): State<ConsoleCompat>, headers: HeaderMap) -> Response {
    let result = async { state.announcements(bearer(&headers)?, None).await }.await;
    match result {
        Ok(items) => success(items),
        Err(e) => error(e),
    }
}
async fn user_announcements(
    State(state): State<ConsoleCompat>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let result = async {
        let secret = bearer(&headers)?;
        state.authorize(secret).await?;
        let id = id.parse::<i64>().map_err(|_| ConsoleError::Invalid)?;
        state.announcements(secret, Some(id)).await
    }
    .await;
    match result {
        Ok(items) => success(items),
        Err(e) => error(e),
    }
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
            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("an acknowledgement object or null")
            }
            fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
                Ok(Acknowledgement::default())
            }
            fn visit_map<M: serde::de::MapAccess<'de>>(
                self,
                mut map: M,
            ) -> Result<Self::Value, M::Error> {
                let mut item = Acknowledgement::default();
                while let Some(key) = map.next_key::<String>()? {
                    match go_field(&key).as_str() {
                        "id" => {
                            if let Some(id) = map.next_value::<Option<i64>>()? {
                                item.id = id;
                            }
                        }
                        "revision" => {
                            if let Some(revision) = map.next_value::<Option<String>>()? {
                                item.revision = revision;
                            }
                        }
                        _ => {
                            let _ = map.next_value::<serde::de::IgnoredAny>()?;
                        }
                    }
                }
                Ok(item)
            }
        }
        deserializer.deserialize_any(Visitor)
    }
}
async fn acknowledge(State(state): State<ConsoleCompat>, request: Request) -> Response {
    let secret = match bearer(request.headers()) {
        Ok(secret) => secret.to_owned(),
        Err(e) => return error(e),
    };
    if let Err(e) = state.authorize(&secret).await {
        return error(e);
    }
    if request
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|v| v.to_str().ok())
        .and_then(|v| v.parse::<u64>().ok())
        .is_some_and(|length| length > BODY_LIMIT as u64)
    {
        return failure(
            StatusCode::PAYLOAD_TOO_LARGE,
            "CONSOLE_BODY_TOO_LARGE",
            "Acknowledgement exceeds 1024 bytes",
        );
    }
    let bytes = match to_bytes(request.into_body(), BODY_LIMIT).await {
        Ok(bytes) => bytes,
        Err(_) => {
            return failure(
                StatusCode::PAYLOAD_TOO_LARGE,
                "CONSOLE_BODY_TOO_LARGE",
                "Acknowledgement exceeds 1024 bytes",
            );
        }
    };
    // Gin ShouldBindJSON reads the first value; configuration parsing above
    // deliberately still requires EOF, matching Go json.Unmarshal.
    let input =
        match Acknowledgement::deserialize(&mut serde_json::Deserializer::from_slice(&bytes)) {
            Ok(input) => input,
            Err(_) => return error(ConsoleError::Invalid),
        };
    match state.acknowledge(&secret, input.id, &input.revision).await {
        Ok(items) => success(items),
        Err(e) => error(e),
    }
}
async fn setup(State(state): State<ConsoleCompat>) -> Response {
    match state.setup_status().await {
        Ok(status) => success(status),
        Err(e) => error(e),
    }
}
async fn setup_cli_only() -> Response {
    failure(
        StatusCode::FORBIDDEN,
        "SETUP_CLI_ONLY",
        "Complete initialization with lmm-core complete-setup using the offline database owner.",
    )
}
async fn notice(State(state): State<ConsoleCompat>) -> Response {
    match state.notice().await {
        Ok(notice) => success(notice),
        Err(e) => error(e),
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn go_json_field_case_null_duplicates_and_first_value() {
        let item:MandatoryAnnouncement=serde_json::from_str(r#"{"ID":1,"Mandatory":true,"mandatory":null,"PublishDate":"2025-01-01T00:00:00Z","acKRevision":"g1"}"#).unwrap();
        assert!(item.mandatory);
        assert_eq!(item.id, 1);
        assert_eq!(item.ack_revision, "g1");
        let input = Acknowledgement::deserialize(&mut serde_json::Deserializer::from_str(
            r#"{"ID":1,"reviſion":"first","REVISION":null,"id":2} {}"#,
        ))
        .unwrap();
        assert_eq!(input.id, 2);
        assert_eq!(input.revision, "first");
        assert!(serde_json::from_str::<Vec<MandatoryAnnouncement>>(r#"[] {}"#).is_err());
    }
    #[test]
    fn publication_time_matches_go_compatible_forms_without_precision_loss() {
        let base = publication_instant("2025-01-01T00:00:00Z").unwrap();
        for raw in [
            "2025-01-01T0:00:00Z",
            "2025-01-01T01:00:00+01:00",
            "2025-01-01T00:00:00-00:00",
            "2025-01-02T01:00:00+24:60",
        ] {
            assert_eq!(publication_instant(raw).unwrap(), base, "{raw}");
        }
        assert_eq!(
            publication_instant("2025-01-01T00:00:00,12345678999Z").unwrap(),
            (base.0, 123456789)
        );
        assert!(publication_instant("2025-01-01T00:00:60Z").is_err());
        assert!(publication_instant("2025-01-01 00:00:00Z").is_err());
        assert!(publication_instant("2025-02-30T00:00:00Z").is_err());
        let raw = r#"[{"id":1,"mandatory":true,"publishDate":"2025-01-01T00:00:00.000000001Z"}]"#;
        assert!(
            current_announcements(raw, base.0 * 1_000_000)
                .unwrap()
                .is_empty()
        );
        assert_eq!(
            current_announcements(raw, base.0 * 1_000_000 + 1)
                .unwrap()
                .len(),
            1
        );
    }
}
