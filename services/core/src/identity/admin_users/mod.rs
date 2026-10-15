//! Core-owned administrator user read model. No writes or delegated grants.
mod http;
mod wallet;
use super::*;
pub use http::router;
use serde::{Deserialize, Serialize};
use sqlx::{Postgres, Transaction};
pub use wallet::WalletProjection;

pub const PROTOCOL: &str = "core.admin-users.v1";
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum AdminUsersError {
    Unauthorized,
    Forbidden,
    Invalid,
    UnsupportedFilter,
    UnsupportedSort,
    Unavailable,
}
type ReadResult<T> = std::result::Result<T, AdminUsersError>;
impl From<sqlx::Error> for AdminUsersError {
    fn from(_: sqlx::Error) -> Self {
        Self::Unavailable
    }
}
impl From<IdentityError> for AdminUsersError {
    fn from(value: IdentityError) -> Self {
        match value {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            IdentityError::Storage | IdentityError::Conflict => Self::Unavailable,
        }
    }
}
impl std::fmt::Display for AdminUsersError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for AdminUsersError {}

#[derive(Clone, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct UsersQuery {
    pub keyword: String,
    pub group: Option<String>,
    pub role: Option<String>,
    pub status: Option<String>,
    pub service_level: Option<i16>,
    pub page: Option<u32>,
    pub page_size: Option<u32>,
    pub sort_by: Option<String>,
    pub sort_order: Option<String>,
    pub trust_level: Option<String>,
    pub risk_min: Option<String>,
    pub risk_max: Option<String>,
    pub transfers: Option<String>,
    pub usage: Option<String>,
    pub funding: Option<String>,
    pub checkin: Option<String>,
}
#[derive(Clone, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct GroupsQuery {
    pub page: Option<u32>,
    pub page_size: Option<u32>,
}
struct ValidQuery {
    keyword: String,
    keyword_id: Option<i64>,
    group: Option<String>,
    role: Option<String>,
    active: Option<bool>,
    level: Option<i16>,
    page: u32,
    size: u32,
    offset: i64,
    order: String,
}
fn pagination(page: Option<u32>, size: Option<u32>) -> ReadResult<(u32, u32, i64)> {
    let page = page.unwrap_or(1);
    let size = size.unwrap_or(20);
    if page == 0 || page > 100_000 || size == 0 || size > 100 {
        return Err(AdminUsersError::Invalid);
    }
    Ok((page, size, i64::from(page - 1) * i64::from(size)))
}
impl UsersQuery {
    fn validate(&self) -> ReadResult<ValidQuery> {
        if self.keyword.len() > 256 || self.keyword.chars().any(char::is_control) {
            return Err(AdminUsersError::Invalid);
        }
        for value in [
            &self.trust_level,
            &self.risk_min,
            &self.risk_max,
            &self.transfers,
            &self.usage,
            &self.funding,
            &self.checkin,
        ] {
            if value.as_ref().is_some_and(|v| !v.is_empty()) {
                return Err(AdminUsersError::UnsupportedFilter);
            }
        }
        let group = self.group.as_ref().filter(|s| !s.is_empty()).cloned();
        if group.as_ref().is_some_and(|v| {
            v.chars().count() > 64 || v.len() > 256 || v.chars().any(char::is_control)
        }) {
            return Err(AdminUsersError::Invalid);
        }
        let role = self.role.as_ref().filter(|s| !s.is_empty()).cloned();
        if role
            .as_ref()
            .is_some_and(|v| !matches!(v.as_str(), "user" | "admin" | "superadmin"))
        {
            return Err(AdminUsersError::Invalid);
        }
        let active = match self.status.as_deref().filter(|s| !s.is_empty()) {
            None => None,
            Some("active") => Some(true),
            Some("disabled") => Some(false),
            Some("deleted" | "-1") => return Err(AdminUsersError::UnsupportedFilter),
            _ => return Err(AdminUsersError::Invalid),
        };
        if self.service_level.is_some_and(|v| !(0..=4).contains(&v)) {
            return Err(AdminUsersError::Invalid);
        }
        let (page, size, offset) = pagination(self.page, self.page_size)?;
        let sort = match self.sort_by.as_deref().unwrap_or("id") {
            "id" => "id",
            "username" => "username COLLATE \"C\"",
            "display_name" => "display_name COLLATE \"C\"",
            "core_created_at" => "core_created_at",
            "registered_at" => "registered_at",
            "groups" => "groups COLLATE \"C\"",
            "service_level" => "service_level",
            "role" => "role_number",
            "status" => "status_number",
            "quota"
            | "used_quota"
            | "request_count"
            | "last_login_at"
            | "group"
            | "created_at"
            | "topup_quota"
            | "topup_money"
            | "assistant_violations"
            | "risk_score"
            | "transferred_quota"
            | "received_quota"
            | "checkin_quota" => return Err(AdminUsersError::UnsupportedSort),
            _ => return Err(AdminUsersError::Invalid),
        };
        let direction = match self.sort_order.as_deref().unwrap_or("desc") {
            "asc" => "ASC",
            "desc" => "DESC",
            _ => return Err(AdminUsersError::Invalid),
        };
        // Only server-selected literals reach ORDER BY. Every filter stays bound.
        let order = format!("{sort} {direction} NULLS LAST,id {direction}");
        let keyword = self.keyword.trim().to_owned();
        let keyword_id = keyword.parse::<i64>().ok().filter(|id| *id > 0);
        Ok(ValidQuery {
            keyword,
            keyword_id,
            group,
            role,
            active,
            level: self.service_level,
            page,
            size,
            offset,
            order,
        })
    }
}
#[derive(Serialize)]
pub struct UserRow {
    pub id: String,
    pub personal_account_id: String,
    pub auth_version: String,
    pub profile_available: bool,
    pub username: Option<String>,
    pub display_name: Option<String>,
    pub email: Option<String>,
    pub platform_role: String,
    pub role: i16,
    pub active: bool,
    pub status: i16,
    pub account_active: bool,
    pub service_level: i16,
    pub developer_access_granted: bool,
    pub groups: Vec<String>,
    pub core_account_created_at_unix_ms: i64,
    pub registered_at_unix_ms: Option<i64>,
    pub wallet: WalletProjection,
}
#[derive(Serialize)]
pub struct ReadCapabilities {
    pub filters: &'static [&'static str],
    pub sorts: &'static [&'static str],
    pub implemented_write_actions: &'static [&'static str],
    pub unavailable_projections: &'static [&'static str],
    pub unavailable_write_actions: &'static [&'static str],
}
const JS_SAFE_INTEGER: i64 = 9_007_199_254_740_991;
fn safe_total(value: i64) -> ReadResult<u64> {
    if !(0..=JS_SAFE_INTEGER).contains(&value) {
        return Err(AdminUsersError::Unavailable);
    }
    Ok(value as u64)
}
fn safe_time(value: i64) -> ReadResult<i64> {
    if !(-JS_SAFE_INTEGER..=JS_SAFE_INTEGER).contains(&value) {
        return Err(AdminUsersError::Unavailable);
    }
    Ok(value)
}
fn capabilities() -> ReadCapabilities {
    ReadCapabilities {
        filters: &["keyword", "group", "role", "status", "service_level"],
        sorts: &[
            "id",
            "username",
            "display_name",
            "core_created_at",
            "registered_at",
            "groups",
            "service_level",
            "role",
            "status",
        ],
        implemented_write_actions: &[],
        unavailable_projections: &[
            "legacy_group",
            "legacy_trust",
            "used_quota",
            "normalized_used_quota",
            "request_count",
            "wallet_risk",
            "transfer_history",
            "checkin_history",
            "topup_summary",
            "referral_rewards",
            "referral_counts",
            "referral_history",
            "deleted_at",
            "last_login_at",
            "oauth_bindings",
            "assistant_profile",
            "assistant_conversations",
            "assistant_violations",
            "permission_matrix",
            "user_detail",
        ],
        unavailable_write_actions: &[
            "create",
            "update",
            "delete",
            "enable",
            "promote",
            "demote",
            "adjust_money",
            "set_trust_level",
            "reset_onboarding",
            "normal_ban",
            "ban_abuse",
            "restore_referral",
            "inviter_penalty",
            "revoke_rewards",
            "unbind",
            "reset_passkey",
            "reset_2fa",
            "assistant_policy",
            "reset_assistant_violations",
            "subscription_mutations",
            "account_action_requests",
        ],
    }
}
#[derive(Serialize)]
pub struct UsersPage {
    pub version: u8,
    pub items: Vec<UserRow>,
    pub total: u64,
    pub page: u32,
    pub page_size: u32,
    pub capabilities: ReadCapabilities,
}
#[derive(Serialize)]
pub struct GroupsPage {
    pub version: u8,
    pub items: Vec<String>,
    pub total: u64,
    pub page: u32,
    pub page_size: u32,
    pub source: &'static str,
}
#[derive(Clone)]
pub struct AdminUsersStore {
    identity: IdentityStore,
}
impl AdminUsersStore {
    pub fn new(identity: IdentityStore) -> Self {
        Self { identity }
    }
    async fn begin(&self) -> ReadResult<Transaction<'_, Postgres>> {
        let mut tx = self.identity.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SET LOCAL statement_timeout='5s'")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SET LOCAL lock_timeout='500ms'")
            .execute(&mut *tx)
            .await?;
        Ok(tx)
    }
    async fn authorize(c: &mut PgConnection, secret: &str) -> ReadResult<Principal> {
        let actor = super::authenticate(c, secret, false).await?;
        if actor.credential_kind != CredentialKind::Session
            || actor.owner.kind != AccountKind::Personal
            || actor.owner.id != actor.user_id
        {
            return Err(AdminUsersError::Forbidden);
        }
        let role: String =
            sqlx::query_scalar("SELECT platform_role FROM core_identity.users WHERE id=$1")
                .bind(actor.user_id)
                .fetch_one(c)
                .await?;
        if !matches!(role.as_str(), "admin" | "superadmin") {
            return Err(AdminUsersError::Forbidden);
        }
        Ok(actor)
    }
    pub async fn users(&self, secret: &str, input: &UsersQuery) -> ReadResult<UsersPage> {
        let q = input.validate()?;
        let mut tx = self.begin().await?;
        Self::authorize(&mut tx, secret).await?;
        let total: i64 = sqlx::query_scalar(&format!("{SELECTED} SELECT count(*) FROM selected"))
            .bind(&q.keyword)
            .bind(q.keyword_id)
            .bind(&q.group)
            .bind(&q.role)
            .bind(q.active)
            .bind(q.level)
            .fetch_one(&mut *tx)
            .await?;
        let rows=sqlx::query(&format!("{SELECTED} SELECT *,floor(extract(epoch FROM core_created_at)*1000)::bigint AS core_created_ms,floor(extract(epoch FROM registered_at)*1000)::bigint AS registered_ms FROM selected ORDER BY {} LIMIT $7 OFFSET $8",q.order))
            .bind(&q.keyword).bind(q.keyword_id).bind(&q.group).bind(&q.role).bind(q.active).bind(q.level).bind(i64::from(q.size)).bind(q.offset).fetch_all(&mut *tx).await?;
        let mut items = Vec::with_capacity(rows.len());
        for r in rows {
            let id: i64 = r.try_get("id")?;
            let account: i64 = r.try_get("personal_account_id")?;
            let generation: i64 = r.try_get("auth_version")?;
            if id <= 0 || account <= 0 || generation <= 0 {
                return Err(AdminUsersError::Unavailable);
            }
            items.push(UserRow {
                id: id.to_string(),
                personal_account_id: account.to_string(),
                auth_version: generation.to_string(),
                profile_available: r.try_get("profile_available")?,
                username: r.try_get("username")?,
                display_name: r.try_get("display_name")?,
                email: r.try_get("email")?,
                platform_role: r.try_get("platform_role")?,
                role: r.try_get("role_number")?,
                active: r.try_get("active")?,
                status: r.try_get("status_number")?,
                account_active: r.try_get("account_active")?,
                service_level: r.try_get("service_level")?,
                developer_access_granted: r.try_get("developer_access_granted")?,
                groups: r.try_get("groups")?,
                core_account_created_at_unix_ms: safe_time(r.try_get("core_created_ms")?)?,
                registered_at_unix_ms: r
                    .try_get::<Option<i64>, _>("registered_ms")?
                    .map(safe_time)
                    .transpose()?,
                wallet: wallet::read(&mut tx, id, account).await?,
            });
        }
        let result = UsersPage {
            version: 1,
            items,
            total: safe_total(total)?,
            page: q.page,
            page_size: q.size,
            capabilities: capabilities(),
        };
        tx.commit().await?;
        Ok(result)
    }
    pub async fn groups(&self, secret: &str, input: &GroupsQuery) -> ReadResult<GroupsPage> {
        let (page, size, offset) = pagination(input.page, input.page_size)?;
        let mut tx = self.begin().await?;
        Self::authorize(&mut tx, secret).await?;
        let total:i64=sqlx::query_scalar("SELECT count(DISTINCT group_name COLLATE \"C\") FROM core_identity.user_routing_groups WHERE enabled").fetch_one(&mut *tx).await?;
        let items=sqlx::query_scalar("SELECT DISTINCT group_name COLLATE \"C\" AS name FROM core_identity.user_routing_groups WHERE enabled ORDER BY name ASC LIMIT $1 OFFSET $2").bind(i64::from(size)).bind(offset).fetch_all(&mut *tx).await?;
        tx.commit().await?;
        Ok(GroupsPage {
            version: 1,
            items,
            total: safe_total(total)?,
            page,
            page_size: size,
            source: "enabled_explicit_user_routing_grants",
        })
    }
}
const SELECTED: &str = r#"
WITH selected AS (
 SELECT u.id,u.personal_account_id,u.auth_version,u.platform_role,u.active,
  a.active AS account_active,a.service_level,a.created_at AS core_created_at,
  p.user_id IS NOT NULL AS profile_available,p.username,p.display_name,p.email,
  o.registered_at,
  CASE u.platform_role WHEN 'user' THEN 1::smallint WHEN 'admin' THEN 10::smallint WHEN 'superadmin' THEN 100::smallint END AS role_number,
  CASE WHEN u.active THEN 1::smallint ELSE 2::smallint END AS status_number,
  (u.platform_role IN ('admin','superadmin') OR EXISTS(SELECT FROM core_identity.developer_access d WHERE d.user_id=u.id AND d.granted)) AS developer_access_granted,
  ARRAY(SELECT g.group_name FROM core_identity.user_routing_groups g WHERE g.user_id=u.id AND g.enabled ORDER BY g.group_name COLLATE "C") AS groups
 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id AND a.kind='personal'
 LEFT JOIN core_identity.user_profiles p ON p.user_id=u.id
 LEFT JOIN core_abuse.registration_observations o ON o.user_id=u.id
 WHERE ($1::text='' OR u.id=$2::bigint OR position(lower($1) in lower(COALESCE(p.username,'')))>0
   OR position(lower($1) in lower(COALESCE(p.display_name,'')))>0 OR position(lower($1) in lower(COALESCE(p.email,'')))>0)
  AND ($3::text IS NULL OR EXISTS(SELECT FROM core_identity.user_routing_groups g WHERE g.user_id=u.id AND g.enabled AND g.group_name COLLATE "C"=$3::text COLLATE "C"))
  AND ($4::text IS NULL OR u.platform_role=$4) AND ($5::boolean IS NULL OR u.active=$5)
  AND ($6::smallint IS NULL OR a.service_level=$6)
)
"#;
#[cfg(all(test, unix))]
mod tests;
