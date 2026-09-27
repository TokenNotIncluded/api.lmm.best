use super::data::*;
use chrono::Utc;
use serde::de::DeserializeOwned;
use serde_json::{Value, json};
use sqlx::{PgConnection, PgPool};

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("{0}")]
    Invalid(&'static str),
    #[error("{0}")]
    Database(#[from] sqlx::Error),
}
impl From<&'static str> for Error {
    fn from(value: &'static str) -> Self {
        Self::Invalid(value)
    }
}
pub type Result<T> = std::result::Result<T, Error>;
fn decode<T: DeserializeOwned>(mut row: Value) -> Result<T> {
    if let Some(values) = row.as_object_mut() {
        values.retain(|_, v| !v.is_null());
    }
    serde_json::from_value(row).map_err(|_| Error::Invalid("invalid acquisition storage"))
}
#[derive(Clone)]
pub struct PgAcquisitionStore {
    pub(super) pool: PgPool,
}
impl PgAcquisitionStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
    pub async fn permission(&self, user: i64, role: i64, action: &str) -> Result<bool> {
        if role >= 100 {
            return Ok(true);
        }
        if role < 10 {
            return Ok(false);
        }
        let subject = format!("user:{user}");
        let policies=sqlx::query_as::<_,(String,String)>("SELECT COALESCE(v0,''),COALESCE(v3,'') FROM casbin_rule WHERE ptype='p' AND v1='acquisition' AND v2=$1 AND v0 IN ('role:admin',$2)").bind(action).bind(&subject).fetch_all(&self.pool).await?;
        for who in [subject.as_str(), "role:admin"] {
            let effects = policies
                .iter()
                .filter(|(s, _)| s == who)
                .map(|(_, e)| e.as_str())
                .collect::<Vec<_>>();
            if effects.contains(&"deny") {
                return Ok(false);
            }
            if effects.iter().any(|v| v.is_empty() || *v == "allow") {
                return Ok(true);
            }
        }
        Ok(false)
    }
    pub async fn links(&self, page: i64, size: i64, status: &str, search: &str) -> Result<Value> {
        if !(1..=100000).contains(&page)
            || !(1..=100).contains(&size)
            || search.chars().count() > 80
        {
            return Err(INVALID.into());
        }
        let predicate = match status {
            "all" => "deleted_at=0",
            "active" => "deleted_at=0 AND archived=FALSE",
            "archived" => "deleted_at=0 AND archived=TRUE",
            "deleted" => "deleted_at>0",
            _ => return Err(INVALID.into()),
        };
        let search = search
            .trim()
            .to_lowercase()
            .replace('!', "!!")
            .replace('%', "!%")
            .replace('_', "!_");
        let pattern = format!("%{search}%");
        let predicate = format!(
            "{predicate} AND ($1='' OR LOWER(name) LIKE $2 ESCAPE '!' OR LOWER(source) LIKE $2 ESCAPE '!' OR LOWER(campaign) LIKE $2 ESCAPE '!')"
        );
        let total: i64 = sqlx::query_scalar(&format!(
            "SELECT COUNT(*) FROM acquisition_links WHERE {predicate}"
        ))
        .bind(&search)
        .bind(&pattern)
        .fetch_one(&self.pool)
        .await?;
        let rows=sqlx::query_scalar::<_,Value>(&format!("SELECT to_jsonb(acquisition_links) FROM acquisition_links WHERE {predicate} ORDER BY created_at DESC,id DESC LIMIT $3 OFFSET $4")).bind(search).bind(pattern).bind(size).bind((page-1)*size).fetch_all(&self.pool).await?;
        let links = rows
            .into_iter()
            .map(decode::<Link>)
            .collect::<Result<Vec<_>>>()?;
        Ok(json!({"items":links,"total":total,"page":page,"page_size":size}))
    }
    pub async fn save_link(&self, input: Input) -> Result<Link> {
        let name = label(input.text("name"));
        if name.is_empty() {
            return Err(INVALID.into());
        }
        if !input.text("id").is_empty() {
            let old=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_links) FROM acquisition_links WHERE id=$1 AND deleted_at=0").bind(input.text("id")).fetch_one(&self.pool).await?;
            let mut old: Link = decode(old)?;
            sqlx::query("UPDATE acquisition_links SET name=$1,archived=$2 WHERE id=$3")
                .bind(&name)
                .bind(input.bool("archived"))
                .bind(&old.id)
                .execute(&self.pool)
                .await?;
            old.name = name;
            old.archived = input.bool("archived");
            return Ok(old);
        }
        let link = Link {
            id: random_id(),
            name,
            source: label(input.text("source")),
            medium: label(input.text("medium")),
            campaign: label(input.text("campaign")),
            content: label(input.text("content")),
            target: target(input.text("target")).into(),
            archived: input.bool("archived"),
            created_at: Utc::now().timestamp(),
            deleted_at: input.int("deleted_at"),
        };
        if link.source.is_empty() || link.target.is_empty() {
            return Err(INVALID.into());
        }
        sqlx::query("INSERT INTO acquisition_links(id,name,source,medium,campaign,content,target,archived,created_at,deleted_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)").bind(&link.id).bind(&link.name).bind(&link.source).bind(&link.medium).bind(&link.campaign).bind(&link.content).bind(&link.target).bind(link.archived).bind(link.created_at).bind(link.deleted_at).execute(&self.pool).await?;
        Ok(link)
    }
    pub async fn delete_link(&self, id: &str) -> Result<()> {
        if !valid_id(id) {
            return Err(INVALID.into());
        }
        let result =
            sqlx::query("UPDATE acquisition_links SET deleted_at=$1 WHERE id=$2 AND deleted_at=0")
                .bind(Utc::now().timestamp())
                .bind(id)
                .execute(&self.pool)
                .await?;
        if result.rows_affected() == 0 {
            return Err(sqlx::Error::RowNotFound.into());
        }
        Ok(())
    }
    pub async fn preview(&self, id: &str) -> Result<Value> {
        if !valid_id(id) {
            return Err(INVALID.into());
        }
        let row=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_links) FROM acquisition_links WHERE id=$1 AND deleted_at=0").bind(id).fetch_one(&self.pool).await?;
        let link: Link = decode(row)?;
        if target(&link.target).is_empty() {
            return Err(INVALID.into());
        }
        Ok(
            json!({"link_id":link.id,"target":link.target,"source":link.source,"medium":link.medium,"campaign":link.campaign,"content":link.content,"evidence":"promotion_link","referrer_host":"","excluded":true,"archived":link.archived}),
        )
    }
    pub async fn observe(
        &self,
        visitor: &str,
        user: i64,
        input: &Input,
        own: &[&str],
    ) -> Result<Visit> {
        let now = Utc::now().timestamp();
        let mut visit = normalize(input, own, now)?;
        if visitor.len() != 64 {
            return Err(INVALID.into());
        }
        config(&mut *self.pool.acquire().await?).await?;
        let mut tx = self.pool.begin().await?;
        if user > 0 {
            let consent=sqlx::query_as::<_,(bool,i64)>("SELECT COALESCE(allowed,FALSE),COALESCE(version,0) FROM acquisition_consents WHERE user_id=$1").bind(user).fetch_optional(&mut *tx).await?;
            if consent.is_some_and(|(allowed, _)| !allowed)
                || (visit.consent_version >= 2 && consent.is_none_or(|(_, version)| version < 2))
            {
                return Err(INVALID.into());
            }
        }
        sqlx::query("INSERT INTO acquisition_visitors(id,user_id,created_at) VALUES($1,0,$2) ON CONFLICT DO NOTHING").bind(visitor).bind(now).execute(&mut *tx).await?;
        let owner: i64 = sqlx::query_scalar(
            "SELECT COALESCE(user_id,0) FROM acquisition_visitors WHERE id=$1 FOR UPDATE",
        )
        .bind(visitor)
        .fetch_one(&mut *tx)
        .await?;
        if owner != 0 && owner != user {
            return Err(INVALID.into());
        }
        if user > 0 {
            sqlx::query("UPDATE acquisition_visitors SET user_id=$1 WHERE id=$2 AND (user_id=0 OR user_id=$1)").bind(user).bind(visitor).execute(&mut *tx).await?;
        }
        if input.text("link_id").len() == 32 {
            let link=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_links) FROM acquisition_links WHERE id=$1 AND deleted_at=0").bind(input.text("link_id")).fetch_optional(&mut *tx).await?;
            if let Some(link) = link {
                let link: Link = decode(link)?;
                visit.link_id = link.id;
                visit.source = link.source;
                visit.medium = link.medium;
                visit.campaign = link.campaign;
                visit.content = link.content;
                visit.evidence = "promotion_link".into();
            }
        }
        if user > 0 && visit.consent_version >= 2 {
            sqlx::query("UPDATE acquisition_accounts SET consent_version=2 WHERE user_id=$1 AND consent_version<2").bind(user).execute(&mut *tx).await?;
        }
        sqlx::query("INSERT INTO acquisition_visits(consent_version,visitor_id,nonce,link_id,source,medium,campaign,content,referrer_host,landing,evidence,created_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) ON CONFLICT DO NOTHING").bind(visit.consent_version).bind(visitor).bind(&visit.nonce).bind(&visit.link_id).bind(&visit.source).bind(&visit.medium).bind(&visit.campaign).bind(&visit.content).bind(&visit.referrer_host).bind(&visit.landing).bind(&visit.evidence).bind(now).execute(&mut *tx).await?;
        let row=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_visits) FROM acquisition_visits WHERE visitor_id=$1 AND nonce=$2").bind(visitor).bind(&visit.nonce).fetch_one(&mut *tx).await?;
        let mut result: Visit = decode(row)?;
        result.visitor_id = visitor.into();
        result.nonce = visit.nonce;
        tx.commit().await?;
        Ok(result)
    }
    pub async fn grant(&self, user: i64) -> Result<()> {
        if user <= 0 {
            return Err(INVALID.into());
        }
        let mut tx = self.pool.begin().await?;
        let created: i64 = sqlx::query_scalar(
            "SELECT COALESCE(created_at,0) FROM users WHERE id=$1 AND deleted_at IS NULL",
        )
        .bind(user)
        .fetch_one(&mut *tx)
        .await?;
        let config = config(&mut tx).await?;
        let source = if created < config.0 {
            "historical_unrecorded"
        } else {
            "unknown"
        };
        let mut value = account(user, created, config.1);
        value["consent_version"] = json!(2);
        value["registration_source"] = json!(source);
        value["first_source"] = json!(source);
        value["first_evidence"] = json!("unavailable");
        insert_account(&mut tx, &value, true).await?;
        consent(&mut tx, user, true).await?;
        tx.commit().await?;
        Ok(())
    }
    pub async fn withdraw(&self, visitor: Option<&str>, user: i64) -> Result<()> {
        // Go performs cookie withdrawal and account withdrawal as two separate
        // transactions and only clears the cookie if both have succeeded.
        if let Some(visitor) = visitor {
            let mut tx = self.pool.begin().await?;
            let owner = sqlx::query_scalar::<_, i64>(
                "SELECT COALESCE(user_id,0) FROM acquisition_visitors WHERE id=$1 FOR UPDATE",
            )
            .bind(visitor)
            .fetch_optional(&mut *tx)
            .await?;
            if let Some(owner) = owner {
                if owner > 0 {
                    revoke(&mut tx, owner).await?;
                }
                sqlx::query("DELETE FROM acquisition_visits WHERE visitor_id=$1")
                    .bind(visitor)
                    .execute(&mut *tx)
                    .await?;
                sqlx::query("DELETE FROM acquisition_visitors WHERE id=$1")
                    .bind(visitor)
                    .execute(&mut *tx)
                    .await?;
            }
            tx.commit().await?;
        }
        if user > 0 {
            let mut tx = self.pool.begin().await?;
            revoke(&mut tx, user).await?;
            tx.commit().await?;
        }
        Ok(())
    }
    pub async fn report(&self, user: i64, input: Option<Input>, delete: bool) -> Result<Value> {
        if let Some(input) = input {
            let detail = self_report(input.text("source"), input.text("detail"))?;
            sqlx::query("INSERT INTO acquisition_self_reports(user_id,source,detail,updated_at) VALUES($1,$2,$3,$4) ON CONFLICT(user_id) DO UPDATE SET source=EXCLUDED.source,detail=EXCLUDED.detail,updated_at=EXCLUDED.updated_at").bind(user).bind(input.text("source")).bind(detail).bind(Utc::now().timestamp()).execute(&self.pool).await?;
        }
        if delete {
            sqlx::query("DELETE FROM acquisition_self_reports WHERE user_id=$1")
                .bind(user)
                .execute(&self.pool)
                .await?;
        }
        Ok(sqlx::query_scalar::<_,Value>("SELECT json_build_object('source',source,'detail',COALESCE(detail,''),'updated_at',COALESCE(updated_at,0)) FROM acquisition_self_reports WHERE user_id=$1 AND updated_at>=$2").bind(user).bind(Utc::now().timestamp()-ACCOUNT_DAYS*86400).fetch_optional(&self.pool).await?.unwrap_or(Value::Null))
    }
    pub async fn lookback(&self, days: i64) -> Result<()> {
        if !(1..=RAW_DAYS).contains(&days) {
            return Err(INVALID.into());
        }
        config(&mut *self.pool.acquire().await?).await?;
        let mut tx = self.pool.begin().await?;
        let current: i64 = sqlx::query_scalar(
            "SELECT COALESCE(lookback_days,0) FROM acquisition_configs WHERE id=1 FOR UPDATE",
        )
        .fetch_one(&mut *tx)
        .await?;
        let now = Utc::now().timestamp();
        sqlx::query("INSERT INTO acquisition_attribution_policies(effective_at,lookback_days) SELECT $1,$2 WHERE NOT EXISTS(SELECT 1 FROM acquisition_attribution_policies)").bind(now).bind(current).execute(&mut *tx).await?;
        if current != days {
            sqlx::query("INSERT INTO acquisition_attribution_policies(effective_at,lookback_days) VALUES($1,$2)").bind(now).bind(days).execute(&mut *tx).await?;
            sqlx::query("UPDATE acquisition_configs SET lookback_days=$1 WHERE id=1")
                .bind(days)
                .execute(&mut *tx)
                .await?;
        }
        tx.commit().await?;
        Ok(())
    }
    pub async fn audit(
        &self,
        user: i64,
        role: i64,
        username: &str,
        pat: bool,
        ip: &str,
        action: &str,
        params: Value,
    ) {
        let other=json!({"op":{"action":action,"params":params},"admin_info":{"admin_id":user,"admin_username":username,"admin_role":role,"auth_method":if pat{"access_token"}else{"session"}}}).to_string();
        let _=sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username,ip,other) VALUES($1,$2,3,$3,$4,$5,$6)").bind(user).bind(Utc::now().timestamp()).bind(action).bind(username).bind(ip).bind(other).execute(&self.pool).await;
    }
    pub async fn attribute_registration(&self, user: i64, visitor: &str) -> Result<()> {
        let mut tx = self.pool.begin().await?;
        let created: i64 = sqlx::query_scalar(
            "SELECT COALESCE(created_at,0) FROM users WHERE id=$1 AND deleted_at IS NULL",
        )
        .bind(user)
        .fetch_one(&mut *tx)
        .await?;
        let (_, lookback) = config(&mut tx).await?;
        let mut value = account(user, created, lookback);
        value["attribution_rule"] = json!(format!("current_or_last_external_{lookback}d"));
        if visitor.len() == 64 {
            sqlx::query("UPDATE acquisition_visitors SET user_id=$1 WHERE id=$2 AND (user_id=0 OR user_id=$1)").bind(user).bind(visitor).execute(&mut *tx).await?;
            let owner = sqlx::query_scalar::<_, i64>(
                "SELECT COALESCE(user_id,0) FROM acquisition_visitors WHERE id=$1",
            )
            .bind(visitor)
            .fetch_optional(&mut *tx)
            .await?;
            if owner == Some(user) {
                let latest=sqlx::query_as::<_,(i64,i64)>("SELECT id,COALESCE(consent_version,0) FROM acquisition_visits WHERE visitor_id=$1 AND created_at<=$2 ORDER BY created_at DESC,id DESC LIMIT 1").bind(visitor).bind(created).fetch_optional(&mut *tx).await?;
                if let Some((_, version)) = latest {
                    value["consent_version"] = json!(version);
                }
                let first=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_visits) FROM acquisition_visits WHERE visitor_id=$1 AND created_at>=$2 AND created_at<=$3 ORDER BY created_at,id LIMIT 1").bind(visitor).bind(created-RAW_DAYS*86400).bind(created).fetch_optional(&mut *tx).await?;
                if let Some(first) = first {
                    let first: Visit = decode(first)?;
                    value["first_visit_id"] = json!(first.id);
                    value["first_source"] = json!(first.source);
                    value["first_evidence"] = json!(first.evidence);
                    value["first_observed_at"] = json!(first.created_at);
                }
                let selected=sqlx::query_scalar::<_,Value>("SELECT to_jsonb(acquisition_visits) FROM acquisition_visits WHERE visitor_id=$1 AND created_at>=$2 AND created_at<=$3 AND source<>'unknown' ORDER BY created_at DESC,id DESC LIMIT 1").bind(visitor).bind(created-lookback*86400).bind(created).fetch_optional(&mut *tx).await?;
                if let Some(selected) = selected {
                    let selected: Visit = decode(selected)?;
                    value["registration_inferred"] =
                        json!(latest.is_some_and(|(id, _)| id != selected.id));
                    value["registration_visit_id"] = json!(selected.id);
                    value["registration_source"] = json!(selected.source);
                    value["registration_link_id"] = json!(selected.link_id);
                    value["registration_campaign"] = json!(selected.campaign);
                    value["registration_content"] = json!(selected.content);
                    value["registration_evidence"] = json!(selected.evidence);
                }
            }
        }
        insert_account(&mut tx, &value, false).await?;
        sqlx::query("SELECT user_id FROM acquisition_accounts WHERE user_id=$1 FOR UPDATE")
            .bind(user)
            .fetch_one(&mut *tx)
            .await?;
        let allowed = sqlx::query_scalar::<_, bool>(
            "SELECT COALESCE(allowed,FALSE) FROM acquisition_consents WHERE user_id=$1",
        )
        .bind(user)
        .fetch_optional(&mut *tx)
        .await?;
        if allowed == Some(false) {
            sqlx::query("DELETE FROM acquisition_accounts WHERE user_id=$1")
                .bind(user)
                .execute(&mut *tx)
                .await?;
        } else if value["consent_version"].as_i64().unwrap_or(0) >= 2 {
            sqlx::query("INSERT INTO acquisition_consents(user_id,allowed,version,updated_at) VALUES($1,TRUE,2,$2) ON CONFLICT DO NOTHING").bind(user).bind(Utc::now().timestamp()).execute(&mut *tx).await?;
        }
        tx.commit().await?;
        Ok(())
    }
}
async fn config(connection: &mut PgConnection) -> Result<(i64, i64)> {
    sqlx::query("INSERT INTO acquisition_configs(id,started_at,lookback_days,last_cleanup_at,payment_snapshot_updated_at,payment_snapshot_status) VALUES(1,$1,30,0,0,'') ON CONFLICT DO NOTHING").bind(Utc::now().timestamp()).execute(&mut *connection).await?;
    Ok(sqlx::query_as("SELECT COALESCE(started_at,0),COALESCE(lookback_days,0) FROM acquisition_configs WHERE id=1").fetch_one(connection).await?)
}
fn account(user: i64, registered: i64, lookback: i64) -> Value {
    json!({"user_id":user,"consent_version":0,"first_source":"","first_evidence":"","first_observed_at":0,"registration_inferred":false,"first_visit_id":0,"registration_visit_id":0,"registration_source":"unknown","registration_link_id":"","registration_content":"","registration_campaign":"","registration_evidence":"unavailable","registration_at":registered,"attribution_rule":"","lookback_days":lookback,"self_reported":"","self_reported_at":0,"created_at":Utc::now().timestamp()})
}
async fn insert_account(connection: &mut PgConnection, value: &Value, upgrade: bool) -> Result<()> {
    let conflict = if upgrade {
        "DO UPDATE SET consent_version=2"
    } else {
        "DO NOTHING"
    };
    sqlx::query(&format!("INSERT INTO acquisition_accounts SELECT * FROM jsonb_populate_record(NULL::acquisition_accounts,$1) ON CONFLICT(user_id) {conflict}")).bind(value).execute(connection).await?;
    Ok(())
}
async fn consent(connection: &mut PgConnection, user: i64, allowed: bool) -> Result<()> {
    sqlx::query("INSERT INTO acquisition_consents(user_id,allowed,version,updated_at) VALUES($1,$2,2,$3) ON CONFLICT(user_id) DO UPDATE SET allowed=EXCLUDED.allowed,version=EXCLUDED.version,updated_at=EXCLUDED.updated_at").bind(user).bind(allowed).bind(Utc::now().timestamp()).execute(connection).await?;
    Ok(())
}
async fn revoke(connection: &mut PgConnection, user: i64) -> Result<()> {
    sqlx::query("INSERT INTO acquisition_correction_heads(user_id,revision,source,updated_at) VALUES($1,0,'',0) ON CONFLICT DO NOTHING").bind(user).execute(&mut *connection).await?;
    sqlx::query("SELECT user_id FROM acquisition_correction_heads WHERE user_id=$1 FOR UPDATE")
        .bind(user)
        .fetch_one(&mut *connection)
        .await?;
    insert_account(connection, &account(user, 0, 0), false).await?;
    sqlx::query("SELECT user_id FROM acquisition_accounts WHERE user_id=$1 FOR UPDATE")
        .bind(user)
        .fetch_one(&mut *connection)
        .await?;
    consent(connection, user, false).await?;
    for table in [
        "acquisition_activities",
        "acquisition_corrections",
        "acquisition_correction_heads",
        "acquisition_first_payments",
        "acquisition_accounts",
    ] {
        sqlx::query(&format!("DELETE FROM {table} WHERE user_id=$1"))
            .bind(user)
            .execute(&mut *connection)
            .await?;
    }
    Ok(())
}
