//! Existing dashboard key contracts backed by durable identity and charging.
//! No session, key, routing grant or financial balance is supplied by a client.
use super::*;
use aes_gcm::{
    Aes256Gcm, KeyInit, Nonce,
    aead::{Aead, Payload},
};
use hkdf::Hkdf;
use serde::Deserialize;
use std::{collections::HashSet, net::IpAddr};

const NEVER: i64 = 253_402_300_799;
#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DashboardKeyQuery {
    pub p: Option<i64>,
    pub size: Option<i64>,
    pub keyword: Option<String>,
    pub token: Option<String>,
    pub creation_mode: Option<String>,
    pub status_only: Option<bool>,
}
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DashboardKeyInput {
    pub id: Option<i64>,
    pub name: Option<String>,
    pub status: Option<i32>,
    pub one_time_reveal: Option<bool>,
    pub remain_quota: Option<i64>,
    pub expired_time: Option<i64>,
    pub unlimited_quota: Option<bool>,
    pub model_limits_enabled: Option<bool>,
    pub model_limits: Option<String>,
    pub allow_ips: Option<String>,
    pub group: Option<String>,
    pub auto_groups: Option<Vec<String>>,
    pub cross_group_retry: Option<bool>,
    pub group_warning_confirmations: Option<u32>,
}
#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ProfileUpdate {
    pub display_name: Option<String>,
    pub language: Option<String>,
    pub settlement_currency: Option<String>,
    pub wallet_display_currency: Option<String>,
    pub sidebar_modules: Option<String>,
}

pub(super) async fn developer(c: &mut PgConnection, actor: &Principal) -> Result<()> {
    if actor.credential_kind != CredentialKind::Session {
        return Err(IdentityError::Forbidden);
    }
    require_developer(c, actor.user_id).await
}
// This capability applies only to API-key management. It never supplies a
// developer grant or authorizes model routing, commerce or account balance.
pub(super) async fn marked_key_management(c: &mut PgConnection, user: i64) -> Result<bool> {
    crate::abuse::is_marked_in_transaction(c, user)
        .await
        .map_err(|error| match error {
            crate::abuse::AbuseError::Forbidden => IdentityError::Forbidden,
            crate::abuse::AbuseError::Conflict => IdentityError::Conflict,
            _ => IdentityError::Storage,
        })
}
pub(super) async fn key_management(c: &mut PgConnection, actor: &Principal) -> Result<bool> {
    if actor.credential_kind != CredentialKind::Session {
        return Err(IdentityError::Forbidden);
    }
    // Check the mark even for an actual developer: marked credentials are
    // restricted to the user's personal account and personal funding.
    let marked = marked_key_management(c, actor.user_id).await?;
    if !marked {
        developer(c, actor).await?;
    }
    Ok(marked)
}
async fn require_developer(c: &mut PgConnection, user: i64) -> Result<()> {
    let role: String =
        sqlx::query_scalar("SELECT platform_role FROM core_identity.users WHERE id=$1 FOR SHARE")
            .bind(user)
            .fetch_one(&mut *c)
            .await?;
    if matches!(role.as_str(), "admin" | "superadmin") {
        return Ok(());
    }
    let grant: Option<bool> =
        sqlx::query_scalar("SELECT granted FROM core_identity.developer_access WHERE user_id=$1")
            .bind(user)
            .fetch_optional(c)
            .await?;
    if grant != Some(true) {
        return Err(IdentityError::Forbidden);
    }
    Ok(())
}
async fn groups(
    c: &mut PgConnection,
    user: i64,
    routing: Option<&crate::routing::Router>,
) -> Result<Vec<String>> {
    if let Some(routing) = routing {
        let candidates:Vec<String>=sqlx::query_scalar("SELECT group_name FROM core_identity.user_routing_groups WHERE user_id=$1 AND enabled ORDER BY group_name").bind(user).fetch_all(&mut *c).await?;
        return Ok(candidates
            .into_iter()
            .filter(|g| routing.catalog(g).is_ok())
            .collect());
    }
    Ok(sqlx::query_scalar("SELECT ug.group_name FROM core_identity.user_routing_groups ug JOIN routing_active_release a ON a.singleton JOIN routing_groups g ON g.version=a.config_version AND g.name=ug.group_name WHERE ug.user_id=$1 AND ug.enabled AND g.enabled ORDER BY ug.group_name")
        .bind(user).fetch_all(c).await?)
}
fn list(input: &str, max: usize, item_max: usize) -> Result<Vec<String>> {
    let values: Vec<String> = input
        .split([',', '\n'])
        .map(str::trim)
        .filter(|v| !v.is_empty())
        .map(str::to_owned)
        .collect();
    if values.len() > max
        || values
            .iter()
            .any(|v| v.len() > item_max || v.chars().any(char::is_control))
        || values.iter().collect::<HashSet<_>>().len() != values.len()
    {
        return Err(IdentityError::Invalid);
    }
    Ok(values)
}
fn networks(input: &str) -> Result<Vec<String>> {
    let values = list(input, 64, 64)?;
    for value in &values {
        let (address, bits) = value
            .split_once('/')
            .map_or((value.as_str(), None), |(a, b)| (a, Some(b)));
        let ip: IpAddr = address.parse().map_err(|_| IdentityError::Invalid)?;
        if let Some(bits) = bits {
            let bits: u8 = bits.parse().map_err(|_| IdentityError::Invalid)?;
            if bits > if ip.is_ipv4() { 32 } else { 128 } {
                return Err(IdentityError::Invalid);
            }
        }
    }
    Ok(values)
}
fn cipher(key: &[u8]) -> Result<Aes256Gcm> {
    let mut derived = [0_u8; 32];
    Hkdf::<Sha256>::new(Some(b"lmm-api-key-secret-v1"), key)
        .expand(b"aes-256-gcm", &mut derived)
        .map_err(|_| IdentityError::Storage)?;
    Aes256Gcm::new_from_slice(&derived).map_err(|_| IdentityError::Storage)
}
fn aad(id: i64, user: i64) -> [u8; 24] {
    let mut data = [0; 24];
    data[..8].copy_from_slice(&1_u64.to_be_bytes());
    data[8..16].copy_from_slice(&id.to_be_bytes());
    data[16..].copy_from_slice(&user.to_be_bytes());
    data
}
fn encrypt(key: &[u8], id: i64, user: i64, secret: &str) -> Result<Vec<u8>> {
    let mut nonce = [0_u8; 12];
    OsRng
        .try_fill_bytes(&mut nonce)
        .map_err(|_| IdentityError::Storage)?;
    let encrypted = cipher(key)?
        .encrypt(
            Nonce::from_slice(&nonce),
            Payload {
                msg: secret.as_bytes(),
                aad: &aad(id, user),
            },
        )
        .map_err(|_| IdentityError::Storage)?;
    let mut output = nonce.to_vec();
    output.extend(encrypted);
    Ok(output)
}
fn decrypt(key: &[u8], id: i64, user: i64, encrypted: &[u8]) -> Result<String> {
    if encrypted.len() < 44 {
        return Err(IdentityError::Storage);
    }
    let bytes = cipher(key)?
        .decrypt(
            Nonce::from_slice(&encrypted[..12]),
            Payload {
                msg: &encrypted[12..],
                aad: &aad(id, user),
            },
        )
        .map_err(|_| IdentityError::Storage)?;
    String::from_utf8(bytes).map_err(|_| IdentityError::Storage)
}
struct ValidInput {
    name: String,
    expires: i64,
    models: Vec<String>,
    ips: Vec<String>,
    group: String,
    auto: Vec<String>,
    retry: bool,
    models_enabled: bool,
    remaining: i64,
    unlimited: bool,
}
async fn validate(
    c: &mut PgConnection,
    user: i64,
    input: &DashboardKeyInput,
    routing: Option<&crate::routing::Router>,
    marked: bool,
) -> Result<ValidInput> {
    let name = input
        .name
        .as_deref()
        .ok_or(IdentityError::Invalid)?
        .trim()
        .to_owned();
    let expires = input.expired_time.ok_or(IdentityError::Invalid)?;
    let expires = if expires == -1 { NEVER } else { expires };
    let now: i64 = sqlx::query_scalar("SELECT extract(epoch FROM clock_timestamp())::bigint")
        .fetch_one(&mut *c)
        .await?;
    let remaining = input.remain_quota.ok_or(IdentityError::Invalid)?;
    if name.is_empty()
        || name.len() > 128
        || name.chars().any(char::is_control)
        || (remaining < 0 && input.unlimited_quota != Some(true))
        || expires <= now
        || expires > NEVER
    {
        return Err(IdentityError::Invalid);
    }
    let models = list(
        input.model_limits.as_deref().unwrap_or(""),
        200,
        if marked { 256 } else { 200 },
    )?;
    let models_enabled = input.model_limits_enabled.unwrap_or(false);
    if models_enabled && models.is_empty() {
        return Err(IdentityError::Invalid);
    }
    let ips = networks(input.allow_ips.as_deref().unwrap_or(""))?;
    let group = input
        .group
        .as_deref()
        .ok_or(IdentityError::Invalid)?
        .trim()
        .to_owned();
    let allowed = if marked {
        let public = crate::abuse::public_models_in_transaction(c, user)
            .await
            .map_err(|_| IdentityError::Storage)?
            .ok_or(IdentityError::Storage)?;
        if models.iter().any(|model| !public.contains(model))
            || group != "auto"
            || input.cross_group_retry == Some(true)
        {
            return Err(IdentityError::Forbidden);
        }
        // A neutral UI setting only. No real routing-group grant is written.
        vec!["auto".to_owned()]
    } else {
        groups(c, user, routing).await?
    };
    let mut auto = input.auto_groups.clone().unwrap_or_default();
    if group == "auto" {
        if auto.is_empty() {
            auto = allowed.clone();
        }
        if auto.is_empty()
            || auto.len() > 16
            || auto.iter().collect::<HashSet<_>>().len() != auto.len()
            || auto.iter().any(|g| !allowed.contains(g))
        {
            return Err(IdentityError::Forbidden);
        }
    } else if !allowed.contains(&group) || !auto.is_empty() || input.cross_group_retry == Some(true)
    {
        return Err(IdentityError::Forbidden);
    }
    Ok(ValidInput {
        name,
        expires,
        models,
        ips,
        group,
        auto,
        retry: input.cross_group_retry.unwrap_or(false),
        models_enabled,
        remaining,
        unlimited: input.unlimited_quota.ok_or(IdentityError::Invalid)?,
    })
}
async fn quota(c: &mut PgConnection, id: i64, issuer: i64) -> Result<(i64, i64, bool)> {
    crate::billing::charging::key_quota_snapshot_for_issuer(c, id, issuer)
        .await
        .map_err(|_| IdentityError::Storage)
}
async fn set_quota(
    c: &mut PgConnection,
    actor: &Principal,
    id: i64,
    remaining: i64,
    unlimited: bool,
) -> Result<()> {
    let (used, _, _) = quota(c, id, actor.user_id).await?;
    let limit = used.checked_add(remaining).ok_or(IdentityError::Invalid)?;
    let budget:i64=sqlx::query_scalar("INSERT INTO core_charging.budgets AS b(scope,key_id,issuer_user_id,period,limit_credits,unlimited,display_limit_credits) VALUES ('key',$1,$2,'lifetime',$3,$4,$5) ON CONFLICT (scope,account_id,user_id,key_id,issuer_user_id,period,anchor,seconds) DO UPDATE SET limit_credits=EXCLUDED.limit_credits,unlimited=EXCLUDED.unlimited,display_limit_credits=EXCLUDED.display_limit_credits,revision=b.revision+1 RETURNING id")
        .bind(id).bind(actor.user_id).bind(if unlimited {0}else{limit}).bind(unlimited).bind(limit).fetch_one(&mut *c).await?;
    sqlx::query("INSERT INTO core_charging.policy_events(actor_user_id,budget_id,detail) VALUES ($1,$2,$3)")
        .bind(actor.user_id).bind(budget).bind(json!({"action":"dashboard.key.quota","key_id":id,"remaining":remaining,"unlimited":unlimited})).execute(c).await?;
    Ok(())
}
const KEY_SELECT: &str = "SELECT c.id,c.user_id,c.revoked_at IS NOT NULL AS revoked,c.expires_at<=clock_timestamp() AS expired,extract(epoch FROM c.expires_at)::bigint AS expires,extract(epoch FROM c.created_at)::bigint AS created,s.*,COALESCE(extract(epoch FROM s.accessed_at)::bigint,0) AS accessed,array_to_string(s.allow_ips,',') AS ips FROM core_identity.credentials c JOIN core_identity.api_key_settings s ON s.credential_id=c.id WHERE c.kind='api_key' AND c.owner_account_id=(SELECT personal_account_id FROM core_identity.users WHERE id=c.user_id)";
async fn owned_key(c: &mut PgConnection, user: i64, id: i64, lock: bool) -> Result<PgRow> {
    if id <= 0 {
        return Err(IdentityError::Invalid);
    }
    let suffix = if lock { " FOR UPDATE OF c,s" } else { "" };
    sqlx::query(&format!(
        "{KEY_SELECT} AND c.user_id=$1 AND c.id=$2{suffix}"
    ))
    .bind(user)
    .bind(id)
    .fetch_optional(c)
    .await?
    .ok_or(IdentityError::Forbidden)
}
async fn personal_key_funding(c: &mut PgConnection, actor: &Principal, id: i64) -> Result<()> {
    let personal_only: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM core_identity.credentials k WHERE k.id=$1 AND k.user_id=$2 AND k.owner_account_id=$3) AND EXISTS(SELECT 1 FROM core_identity.key_funding_rules f WHERE f.credential_id=$1) AND NOT EXISTS(SELECT 1 FROM core_identity.key_funding_rules f WHERE f.credential_id=$1 AND f.payer_account_id<>$3)",
    )
    .bind(id)
    .bind(actor.user_id)
    .bind(actor.owner_account_id)
    .fetch_one(c)
    .await?;
    if !personal_only {
        return Err(IdentityError::Forbidden);
    }
    Ok(())
}
async fn projection(c: &mut PgConnection, row: &PgRow) -> Result<Value> {
    let id: i64 = row.try_get("id")?;
    let (used, remaining, unlimited) = quota(c, id, row.try_get("user_id")?).await?;
    let status = if row.try_get::<bool, _>("revoked")? || !row.try_get::<bool, _>("enabled")? {
        2
    } else if row.try_get::<bool, _>("expired")? {
        3
    } else if !unlimited && remaining <= 0 {
        4
    } else {
        1
    };
    let expires: i64 = row.try_get("expires")?;
    Ok(
        json!({"id":id,"name":row.try_get::<String,_>("name")?,"key":"********","status":status,
        "one_time_reveal":row.try_get::<bool,_>("one_time_reveal")?,
        "remain_quota":remaining,"used_quota":used,"normalized_used_quota":used,"usage_projection_available":true,
        "unlimited_quota":unlimited,"expired_time":if expires==NEVER {-1}else{expires},
        "created_time":row.try_get::<i64,_>("created")?,"accessed_time":row.try_get::<i64,_>("accessed")?,
        "model_limits_enabled":row.try_get::<bool,_>("model_limits_enabled")?,
        "model_limits":row.try_get::<Vec<String>,_>("model_limits")?.join(","),
        "allow_ips":row.try_get::<String,_>("ips")?,"group":row.try_get::<String,_>("group_name")?,
        "auto_groups":row.try_get::<Vec<String>,_>("auto_groups")?,
        "cross_group_retry":row.try_get::<bool,_>("cross_group_retry")?,
        "account_balance_read":row.try_get::<bool,_>("account_balance_read")?,"creation_source":"manual"}),
    )
}
impl IdentityStore {
    /// Core-private raw master key, loaded from an operator-owned file by the
    /// typed host configuration. It is independent from browser refresh keys.
    pub fn with_api_key_encryption_key(mut self, key: &[u8]) -> Result<Self> {
        if key.len() != 32 {
            return Err(IdentityError::Invalid);
        }
        self.api_key_encryption_key = Some(key.into());
        Ok(self)
    }
    /// Full runtime opt-in. An identity-only host never queries financial data.
    pub fn with_financial_profile_enabled(mut self, enabled: bool) -> Self {
        self.financial_profile_enabled = enabled;
        self
    }
    /// Only groups stored for this key and still granted to its issuer. Empty
    /// means a native key requires the host's explicit key-group mapping.
    pub async fn api_key_routing_groups(&self, secret: &str) -> Result<Vec<String>> {
        let mut tx = self.pool.begin().await?;
        let actor = authorize_in_transaction(&mut tx, secret).await?;
        if actor.credential_kind != CredentialKind::ApiKey {
            return Err(IdentityError::Forbidden);
        }
        let row=sqlx::query("SELECT group_name,auto_groups FROM core_identity.api_key_settings WHERE credential_id=$1 FOR SHARE")
            .bind(actor.credential_id).fetch_optional(&mut *tx).await?;
        let result = if let Some(row) = row {
            require_developer(&mut tx, actor.user_id).await?;
            let selected: String = row.try_get("group_name")?;
            let candidates: Vec<String> = if selected == "auto" {
                row.try_get("auto_groups")?
            } else {
                vec![selected]
            };
            let allowed = groups(&mut tx, actor.user_id, self.dashboard_routing.as_deref()).await?;
            candidates
                .into_iter()
                .filter(|g| allowed.contains(g))
                .collect()
        } else {
            Vec::new()
        };
        tx.commit().await?;
        Ok(result)
    }
    pub fn with_commerce_scope_enabled(mut self, enabled: bool) -> Self {
        self.commerce_scope_enabled = enabled;
        self
    }
    pub fn with_dashboard_routing(
        mut self,
        routing: std::sync::Arc<crate::routing::Router>,
    ) -> Self {
        self.dashboard_routing = Some(routing);
        self
    }
    pub async fn dashboard_create_key(
        &self,
        secret: &str,
        input: &DashboardKeyInput,
    ) -> Result<Value> {
        if input.id.is_some() || input.status.is_some() {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await?;
        let actor = session(&mut tx, secret).await?;
        let marked = key_management(&mut tx, &actor).await?;
        let valid = validate(
            &mut tx,
            actor.user_id,
            input,
            self.dashboard_routing.as_deref(),
            marked,
        )
        .await?;
        let issued = new_secret("lmmk_")?;
        let id:i64=sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,personal_billing_preference,expires_at) VALUES ($1,'api_key',$2,$3,$4,'subscription_first',to_timestamp($5)) RETURNING id")
            .bind(digest(&issued)?).bind(actor.user_id).bind(actor.user_version).bind(actor.owner_account_id).bind(valid.expires as f64).fetch_one(&mut *tx).await?;
        sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) VALUES ($1,0,$2)")
            .bind(id).bind(actor.owner_account_id).execute(&mut *tx).await?;
        let once = input.one_time_reveal.unwrap_or(true);
        let encrypted = if once {
            None
        } else {
            Some(encrypt(
                self.api_key_encryption_key
                    .as_deref()
                    .ok_or(IdentityError::Storage)?,
                id,
                actor.user_id,
                &issued,
            )?)
        };
        sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,one_time_reveal,encrypted_secret,model_limits_enabled,model_limits,allow_ips,group_name,auto_groups,cross_group_retry) VALUES ($1,$2,$3,$4,$5,$6,$7::text[]::inet[],$8,$9,$10)")
            .bind(id).bind(&valid.name).bind(once).bind(encrypted).bind(valid.models_enabled).bind(&valid.models)
            .bind(&valid.ips).bind(&valid.group).bind(&valid.auto).bind(valid.retry).execute(&mut *tx).await?;
        set_quota(&mut tx, &actor, id, valid.remaining, valid.unlimited).await?;
        audit(
            &mut tx,
            &actor,
            "dashboard.key.create",
            json!({"credential_id":id,"one_time_reveal":once}),
        )
        .await?;
        let row = owned_key(&mut tx, actor.user_id, id, false).await?;
        let mut result = projection(&mut tx, &row).await?;
        result["key"] = json!(issued);
        tx.commit().await?;
        Ok(result)
    }
    pub async fn dashboard_keys(&self, secret: &str, query: &DashboardKeyQuery) -> Result<Value> {
        let page = query.p.unwrap_or(1);
        let size = query.size.unwrap_or(10);
        let keyword = query.keyword.as_deref().unwrap_or("");
        if page < 1
            || !(1..=100).contains(&size)
            || page > 1_000_000
            || keyword.len() > 128
            || query
                .creation_mode
                .as_deref()
                .is_some_and(|m| !matches!(m, "manual" | "automatic"))
        {
            return Err(IdentityError::Invalid);
        }
        let token = query
            .token
            .as_deref()
            .filter(|v| !v.is_empty())
            .map(digest)
            .transpose()?;
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
            .execute(&mut *tx)
            .await?;
        let actor = session(&mut tx, secret).await?;
        key_management(&mut tx, &actor).await?;
        let where_clause = " AND c.user_id=$1 AND c.revoked_at IS NULL AND ($2='' OR position(lower($2) in lower(s.name))>0) AND ($3::bytea IS NULL OR c.digest=$3) AND $4<>'automatic'";
        let rows = sqlx::query(&format!(
            "{KEY_SELECT}{where_clause} ORDER BY c.id DESC LIMIT $5 OFFSET $6"
        ))
        .bind(actor.user_id)
        .bind(keyword)
        .bind(&token)
        .bind(query.creation_mode.as_deref().unwrap_or("manual"))
        .bind(size)
        .bind((page - 1) * size)
        .fetch_all(&mut *tx)
        .await?;
        let total: i64 = sqlx::query_scalar(&format!(
            "SELECT count(*) FROM ({KEY_SELECT}{where_clause}) q"
        ))
        .bind(actor.user_id)
        .bind(keyword)
        .bind(&token)
        .bind(query.creation_mode.as_deref().unwrap_or("manual"))
        .fetch_one(&mut *tx)
        .await?;
        let mut items = Vec::with_capacity(rows.len());
        for row in &rows {
            items.push(projection(&mut tx, row).await?);
        }
        tx.commit().await?;
        Ok(json!({"items":items,"total":total,"page":page,"page_size":size}))
    }
    pub async fn dashboard_key(&self, secret: &str, id: i64) -> Result<Value> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        key_management(&mut tx, &actor).await?;
        let row = owned_key(&mut tx, actor.user_id, id, false).await?;
        let result = projection(&mut tx, &row).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn dashboard_update_key(
        &self,
        secret: &str,
        input: &DashboardKeyInput,
        status_only: bool,
    ) -> Result<Value> {
        let id = input.id.ok_or(IdentityError::Invalid)?;
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await?;
        let actor = session(&mut tx, secret).await?;
        let marked = key_management(&mut tx, &actor).await?;
        let old = owned_key(&mut tx, actor.user_id, id, true).await?;
        if marked {
            personal_key_funding(&mut tx, &actor, id).await?;
        }
        if old.try_get::<bool, _>("revoked")? {
            return Err(IdentityError::Forbidden);
        }
        if status_only {
            let enabled = match input.status {
                Some(1) => true,
                Some(2) => false,
                _ => return Err(IdentityError::Invalid),
            };
            sqlx::query(
                "UPDATE core_identity.api_key_settings SET enabled=$2 WHERE credential_id=$1",
            )
            .bind(id)
            .bind(enabled)
            .execute(&mut *tx)
            .await?;
        } else {
            if input.status.is_some() || input.one_time_reveal.is_some() {
                return Err(IdentityError::Invalid);
            }
            let valid = validate(
                &mut tx,
                actor.user_id,
                input,
                self.dashboard_routing.as_deref(),
                marked,
            )
            .await?;
            sqlx::query(
                "UPDATE core_identity.credentials SET expires_at=to_timestamp($2) WHERE id=$1",
            )
            .bind(id)
            .bind(valid.expires as f64)
            .execute(&mut *tx)
            .await?;
            sqlx::query("UPDATE core_identity.api_key_settings SET name=$2,model_limits_enabled=$3,model_limits=$4,allow_ips=$5::text[]::inet[],group_name=$6,auto_groups=$7,cross_group_retry=$8 WHERE credential_id=$1")
                .bind(id).bind(valid.name).bind(valid.models_enabled).bind(valid.models).bind(valid.ips).bind(valid.group).bind(valid.auto).bind(valid.retry).execute(&mut *tx).await?;
            set_quota(&mut tx, &actor, id, valid.remaining, valid.unlimited).await?;
        }
        audit(
            &mut tx,
            &actor,
            "dashboard.key.update",
            json!({"credential_id":id,"status_only":status_only}),
        )
        .await?;
        let row = owned_key(&mut tx, actor.user_id, id, false).await?;
        let result = projection(&mut tx, &row).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn dashboard_delete_keys(&self, secret: &str, ids: &[i64]) -> Result<usize> {
        if ids.is_empty()
            || ids.len() > 100
            || ids.iter().collect::<HashSet<_>>().len() != ids.len()
        {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        key_management(&mut tx, &actor).await?;
        // Sort locks to avoid an overlapping batch taking the opposite order.
        let mut ordered = ids.to_vec();
        ordered.sort_unstable();
        for id in &ordered {
            owned_key(&mut tx, actor.user_id, *id, true).await?;
        }
        sqlx::query("UPDATE core_identity.credentials SET revoked_at=COALESCE(revoked_at,clock_timestamp()) WHERE id=ANY($1)").bind(&ordered).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "dashboard.key.delete",
            json!({"credential_ids":ordered}),
        )
        .await?;
        tx.commit().await?;
        Ok(ids.len())
    }
    pub async fn dashboard_reveal_keys(&self, secret: &str, ids: &[i64]) -> Result<Value> {
        if ids.is_empty()
            || ids.len() > 100
            || ids.iter().collect::<HashSet<_>>().len() != ids.len()
        {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let marked = key_management(&mut tx, &actor).await?;
        let mut keys = serde_json::Map::new();
        for id in ids {
            let row = owned_key(&mut tx, actor.user_id, *id, false).await?;
            if marked {
                personal_key_funding(&mut tx, &actor, *id).await?;
            }
            if row.try_get::<bool, _>("one_time_reveal")?
                || row.try_get::<bool, _>("revoked")?
                || row.try_get::<bool, _>("expired")?
                || !row.try_get::<bool, _>("enabled")?
            {
                return Err(IdentityError::Forbidden);
            }
            let encrypted: Vec<u8> = row.try_get("encrypted_secret")?;
            keys.insert(
                id.to_string(),
                json!(decrypt(
                    self.api_key_encryption_key
                        .as_deref()
                        .ok_or(IdentityError::Storage)?,
                    *id,
                    actor.user_id,
                    &encrypted,
                )?),
            );
        }
        audit(
            &mut tx,
            &actor,
            "dashboard.key.reveal",
            json!({"credential_ids":ids}),
        )
        .await?;
        tx.commit().await?;
        Ok(json!({"keys":keys}))
    }
    pub async fn dashboard_balance_access(
        &self,
        secret: &str,
        id: i64,
        enabled: bool,
    ) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        developer(&mut tx, &actor).await?;
        let row = owned_key(&mut tx, actor.user_id, id, true).await?;
        if row.try_get::<bool, _>("revoked")? {
            return Err(IdentityError::Forbidden);
        }
        sqlx::query("UPDATE core_identity.api_key_settings SET account_balance_read=$2 WHERE credential_id=$1").bind(id).bind(enabled).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "dashboard.key.balance_access",
            json!({"credential_id":id,"enabled":enabled}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn dashboard_groups(&self, secret: &str) -> Result<Value> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let marked = key_management(&mut tx, &actor).await?;
        let allowed = if marked {
            vec!["auto".to_owned()]
        } else {
            groups(&mut tx, actor.user_id, self.dashboard_routing.as_deref()).await?
        };
        tx.commit().await?;
        Ok(json!(allowed))
    }
    pub async fn dashboard_models(&self, secret: &str) -> Result<Vec<String>> {
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let marked = key_management(&mut tx, &actor).await?;
        if marked {
            let public = crate::abuse::public_models_in_transaction(&mut tx, actor.user_id)
                .await
                .map_err(|_| IdentityError::Storage)?
                .ok_or(IdentityError::Storage)?;
            tx.commit().await?;
            return Ok(public);
        }
        if let Some(routing) = self.dashboard_routing.as_deref() {
            let allowed = groups(&mut tx, actor.user_id, Some(routing)).await?;
            let mut models = std::collections::BTreeSet::new();
            for group in allowed {
                for model in routing
                    .catalog(&group)
                    .map_err(|_| IdentityError::Storage)?
                {
                    models.insert(model.name);
                }
            }
            tx.commit().await?;
            return Ok(models.into_iter().collect());
        }
        let result=sqlx::query_scalar("SELECT DISTINCT r.model FROM routing_routes r JOIN routing_active_release a ON a.config_version=r.version JOIN routing_groups g ON g.version=r.version AND g.name=r.group_name JOIN core_identity.user_routing_groups ug ON ug.group_name=r.group_name AND ug.user_id=$1 WHERE r.enabled AND g.enabled AND ug.enabled ORDER BY r.model")
            .bind(actor.user_id).fetch_all(&mut *tx).await?;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn update_dashboard_profile(
        &self,
        secret: &str,
        input: &ProfileUpdate,
    ) -> Result<()> {
        if input
            .display_name
            .as_ref()
            .is_some_and(|v| v.len() > 256 || v.chars().any(char::is_control))
            || input.language.as_ref().is_some_and(|v| {
                v.len() > 32
                    || v.is_empty()
                    || !v
                        .bytes()
                        .all(|b| b.is_ascii_alphanumeric() || b == b'-' || b == b'_')
            })
            || input
                .settlement_currency
                .as_deref()
                .is_some_and(|v| !matches!(v, "" | "CNY" | "USD"))
            || input
                .wallet_display_currency
                .as_deref()
                .is_some_and(|v| !matches!(v, "" | "CREDIT" | "CNY" | "USD"))
            || input.sidebar_modules.as_ref().is_some_and(|v| {
                v.len() > 16384 || serde_json::from_str::<Value>(v).map_or(true, |v| !v.is_object())
            })
        {
            return Err(IdentityError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        let raw: String = sqlx::query_scalar(
            "SELECT setting FROM core_identity.user_profiles WHERE user_id=$1 FOR UPDATE",
        )
        .bind(actor.user_id)
        .fetch_one(&mut *tx)
        .await?;
        let mut settings: Value = serde_json::from_str(&raw).map_err(|_| IdentityError::Storage)?;
        if !settings.is_object() {
            return Err(IdentityError::Storage);
        }
        for (name, value) in [
            ("language", &input.language),
            ("settlement_currency", &input.settlement_currency),
            ("wallet_display_currency", &input.wallet_display_currency),
            ("sidebar_modules", &input.sidebar_modules),
        ] {
            if let Some(value) = value {
                settings[name] = json!(value);
            }
        }
        sqlx::query("UPDATE core_identity.user_profiles SET display_name=COALESCE($2,display_name),setting=$3 WHERE user_id=$1").bind(actor.user_id).bind(&input.display_name).bind(settings.to_string()).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "dashboard.profile.update",
            json!({"user_id":actor.user_id}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn set_commerce_key_scope(&self, secret: &str, id: i64, enabled: bool) -> Result<()> {
        if !self.commerce_scope_enabled {
            return Err(IdentityError::Storage);
        }
        let mut tx = self.pool.begin().await?;
        let actor = session(&mut tx, secret).await?;
        developer(&mut tx, &actor).await?;
        let target=sqlx::query("SELECT c.user_id,c.owner_account_id,c.kind,c.revoked_at IS NOT NULL AS revoked,t.id AS team_id FROM core_identity.credentials c LEFT JOIN core_identity.teams t ON t.account_id=c.owner_account_id WHERE c.id=$1 FOR UPDATE OF c").bind(id).fetch_optional(&mut *tx).await?.ok_or(IdentityError::Forbidden)?;
        if target.try_get::<String, _>("kind")? != "api_key"
            || target.try_get::<bool, _>("revoked")?
        {
            return Err(IdentityError::Forbidden);
        }
        if let Some(team_id) = target.try_get::<Option<i64>, _>("team_id")? {
            let team = lock_team(&mut tx, team_id).await?;
            let (role, _) = team_role(&mut tx, &team, team_id, actor.user_id).await?;
            let target_user: i64 = target.try_get("user_id")?;
            if role == TeamRole::Member {
                return Err(IdentityError::Forbidden);
            }
            if role == TeamRole::Admin && target_user != actor.user_id {
                let (target_role, _) = team_role(&mut tx, &team, team_id, target_user).await?;
                if target_role != TeamRole::Member {
                    return Err(IdentityError::Forbidden);
                }
            }
        } else if target.try_get::<i64, _>("user_id")? != actor.user_id {
            return Err(IdentityError::Forbidden);
        }
        sqlx::query("INSERT INTO core_commerce.key_scopes(credential_id,can_spend) VALUES ($1,$2) ON CONFLICT(credential_id) DO UPDATE SET can_spend=EXCLUDED.can_spend").bind(id).bind(enabled).execute(&mut *tx).await?;
        audit(
            &mut tx,
            &actor,
            "credential.commerce_scope",
            json!({"credential_id":id,"enabled":enabled}),
        )
        .await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn authorize_model_request(
        &self,
        secret: &str,
        model: &str,
        peer: Option<IpAddr>,
        group: &str,
    ) -> Result<Principal> {
        let mut tx = self.pool.begin().await?;
        let actor =
            authorize_model_request_in_transaction(&mut tx, secret, model, peer, group).await?;
        tx.commit().await?;
        Ok(actor)
    }
}
pub async fn authorize_model_request_in_transaction(
    c: &mut PgConnection,
    secret: &str,
    model: &str,
    peer: Option<IpAddr>,
    group: &str,
) -> Result<Principal> {
    let actor = authorize_in_transaction(c, secret).await?;
    if actor.credential_kind != CredentialKind::ApiKey {
        return Err(IdentityError::Forbidden);
    }
    if let Some(row)=sqlx::query("SELECT *, cardinality(allow_ips)=0 OR ($2::text IS NOT NULL AND $2::inet <<= ANY(allow_ips)) AS ip_allowed FROM core_identity.api_key_settings WHERE credential_id=$1 FOR SHARE").bind(actor.credential_id).bind(peer.map(|p|p.to_string())).fetch_optional(&mut *c).await? {
        let models:Vec<String>=row.try_get("model_limits")?;
        let selected:String=row.try_get("group_name")?;
        let auto:Vec<String>=row.try_get("auto_groups")?;
        if !row.try_get::<bool,_>("enabled")? || !row.try_get::<bool,_>("ip_allowed")?
            || (row.try_get::<bool,_>("model_limits_enabled")? && !models.iter().any(|m|m==model))
            || (selected!="auto" && selected!=group) || (selected=="auto" && !auto.iter().any(|g|g==group)) {return Err(IdentityError::Forbidden);}
        require_developer(c,actor.user_id).await?;
        let granted:Option<bool>=sqlx::query_scalar("SELECT enabled FROM core_identity.user_routing_groups WHERE user_id=$1 AND group_name=$2").bind(actor.user_id).bind(group).fetch_optional(c).await?;
        if granted!=Some(true) {return Err(IdentityError::Forbidden);}
    }
    Ok(actor)
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn recovery_aead_binds_version_credential_and_owner() {
        let secret = "lmmk_synthetic_secret_for_crypto_validation_only";
        let encrypted = encrypt(&[43; 32], 11, 7, secret).unwrap();
        assert_eq!(decrypt(&[43; 32], 11, 7, &encrypted).unwrap(), secret);
        assert!(decrypt(&[43; 32], 12, 7, &encrypted).is_err());
        assert!(decrypt(&[43; 32], 11, 8, &encrypted).is_err());
        assert!(decrypt(&[44; 32], 11, 7, &encrypted).is_err());
        let mut tampered = encrypted.clone();
        tampered[13] ^= 1;
        assert!(decrypt(&[43; 32], 11, 7, &tampered).is_err());
        assert_eq!(&aad(11, 7)[..8], &1_u64.to_be_bytes());
    }
    #[test]
    fn recovery_uses_fresh_nonce_for_identical_plaintext() {
        let a = encrypt(&[43; 32], 11, 7, "synthetic").unwrap();
        let b = encrypt(&[43; 32], 11, 7, "synthetic").unwrap();
        assert_ne!(&a[..12], &b[..12]);
        assert_ne!(a, b);
    }
}
