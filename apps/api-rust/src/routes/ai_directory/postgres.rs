use async_trait::async_trait;
use chrono::Utc;
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};
use std::{sync::Arc, time::Duration};

use super::{
    AD_DURATION_SECONDS, AIDirectoryAd, AIDirectoryAdInput, AIDirectoryStore, AdError,
    MAX_WALLET_QUOTA, charge_quota_with_credits_per_usd, charged_amount_usd, normalize_ad,
    validate_directory_links,
};
use crate::auth::DashboardUserView;

#[derive(Clone)]
pub struct PgAIDirectoryStore {
    pg: PgPool,
    log_pg: PgPool,
    valkey: Option<redis::Client>,
    dependency_timeout: Duration,
    runtime: Option<Arc<crate::routes::system_config::ProcessRuntimeOptions>>,
}

impl PgAIDirectoryStore {
    pub fn new(pg: PgPool, valkey: Option<redis::Client>) -> Self {
        Self {
            log_pg: pg.clone(),
            pg,
            valkey,
            dependency_timeout: Duration::from_secs(2),
            runtime: None,
        }
    }
    pub fn with_log_pool(mut self, pool: PgPool) -> Self {
        self.log_pg = pool;
        self
    }
    pub fn with_dependency_timeout(mut self, timeout: Duration) -> Self {
        self.dependency_timeout = timeout;
        self
    }
    pub fn with_runtime_options(
        mut self,
        runtime: Arc<crate::routes::system_config::ProcessRuntimeOptions>,
    ) -> Self {
        self.runtime = Some(runtime);
        self
    }

    async fn option(&self, key: &str) -> Result<Option<String>, AdError> {
        if let Some(runtime) = &self.runtime {
            return Ok(runtime.snapshot().await.get(key).cloned());
        }
        sqlx::query_scalar("SELECT value FROM options WHERE key=$1")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| AdError::Storage)
    }
    async fn annotate(&self, ads: &mut [AIDirectoryAd]) {
        // Metadata failure is not a failed payment/refund. No legacy fallback.
        let basis = self.option("CreditsPerUSD").await.ok().flatten();
        for ad in ads {
            ad.charged_amount_usd = basis
                .as_deref()
                .and_then(|k| charged_amount_usd(ad.charged_quota, k));
        }
    }
    async fn find_request(&self, id: &str) -> Result<Option<AIDirectoryAd>, AdError> {
        let mut ad = sqlx::query(&format!("{SELECT_AD} WHERE request_id=$1"))
            .bind(id)
            .fetch_optional(&self.pg)
            .await
            .map_err(|_| AdError::Storage)?
            .map(ad_from_row)
            .transpose()?;
        if let Some(value) = &mut ad {
            self.annotate(std::slice::from_mut(value)).await;
        }
        Ok(ad)
    }
    async fn post_commit(&self, ad: &AIDirectoryAd, refund: bool) {
        if let Some(valkey) = &self.valkey {
            let eviction = async {
                let mut connection = valkey.get_multiplexed_async_connection().await?;
                redis::cmd("DEL")
                    .arg(format!("user:{}", ad.owner_user_id))
                    .query_async::<()>(&mut connection)
                    .await
            };
            if !matches!(
                tokio::time::timeout(self.dependency_timeout, eviction).await,
                Ok(Ok(()))
            ) {
                tracing::warn!(
                    ad_id = ad.id,
                    "advertisement wallet cache invalidation failed after commit"
                );
            }
        }
        let content = if refund {
            format!(
                "Refunded {} quota for hidden AI directory advertisement {}",
                ad.charged_quota, ad.id
            )
        } else {
            format!(
                "Paid {} quota for AI directory advertisement {}",
                ad.charged_quota, ad.id
            )
        };
        self.log(
            ad.owner_user_id,
            if refund { 6 } else { 3 },
            &content,
            "",
            "",
        )
        .await;
    }
    async fn log(&self, user_id: i64, kind: i64, content: &str, ip: &str, other: &str) {
        let username =
            sqlx::query_scalar::<_, String>("SELECT COALESCE(username,'') FROM users WHERE id=$1")
                .bind(user_id)
                .fetch_optional(&self.pg)
                .await
                .ok()
                .flatten()
                .unwrap_or_default();
        if sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username,token_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,channel_id,token_id,\"group\",ip,other,request_id) VALUES($1,$2,$3,$4,$5,'','',0,0,0,0,false,0,0,'',$6,$7,$8)")
            .bind(user_id).bind(Utc::now().timestamp()).bind(kind).bind(content).bind(username).bind(ip).bind(other).bind(uuid::Uuid::new_v4().simple().to_string()).execute(&self.log_pg).await.is_err()
        {tracing::warn!(user_id,kind,"advertisement log write failed after commit");}
    }
}

const SELECT_AD: &str = "SELECT id::BIGINT AS id,owner_user_id::BIGINT AS owner_user_id,name,url,summary,description,bid_cents,charged_quota::BIGINT AS charged_quota,request_id,status,paid_at,expires_at,hidden_at,refunded_at FROM ai_directory_ads";

fn ad_from_row(row: PgRow) -> Result<AIDirectoryAd, AdError> {
    let decode = || -> Result<AIDirectoryAd, sqlx::Error> {
        Ok(AIDirectoryAd {
            id: row.try_get("id")?,
            owner_user_id: row.try_get("owner_user_id")?,
            name: row.try_get("name")?,
            url: row.try_get("url")?,
            summary: row.try_get("summary")?,
            description: row.try_get("description")?,
            bid_cents: row.try_get("bid_cents")?,
            charged_quota: row.try_get("charged_quota")?,
            charged_amount_usd: None,
            request_id: row.try_get("request_id")?,
            status: row.try_get("status")?,
            paid_at: row.try_get("paid_at")?,
            expires_at: row.try_get("expires_at")?,
            hidden_at: row.try_get("hidden_at")?,
            refunded_at: row.try_get("refunded_at")?,
        })
    };
    decode().map_err(|_| AdError::Storage)
}
fn same_request(ad: &AIDirectoryAd, owner: i64, input: &AIDirectoryAdInput) -> bool {
    ad.owner_user_id == owner
        && ad.name == input.name
        && ad.url == input.url
        && ad.summary == input.summary
        && ad.description == input.description
        && ad.bid_cents == input.bid_cents
        && ad.charged_quota == input.expected_quota
}

#[async_trait]
impl AIDirectoryStore for PgAIDirectoryStore {
    async fn links(&self) -> Result<Value, AdError> {
        Ok(self
            .option("AIDirectoryLinks")
            .await?
            .and_then(|raw| validate_directory_links(&raw).ok())
            .unwrap_or(Value::Null))
    }
    async fn quote(&self, bid: i64) -> Result<i64, AdError> {
        if !(super::MIN_BID_CENTS..=super::MAX_BID_CENTS).contains(&bid) {
            return Err(AdError::InvalidBid);
        }
        let basis = self
            .option("CreditsPerUSD")
            .await?
            .ok_or(AdError::CurrencyUnavailable)?;
        charge_quota_with_credits_per_usd(bid, &basis)
    }
    async fn active(&self, offset: i64) -> Result<(Vec<AIDirectoryAd>, bool), AdError> {
        if !(0..=100_000).contains(&offset) {
            return Err(AdError::InvalidInput);
        }
        let rows=sqlx::query(&format!("{SELECT_AD} WHERE status='active' AND expires_at>$1 ORDER BY charged_quota DESC,paid_at ASC,id ASC OFFSET $2 LIMIT 21")).bind(Utc::now().timestamp()).bind(offset).fetch_all(&self.pg).await.map_err(|_|AdError::Storage)?;
        let mut ads = rows
            .into_iter()
            .map(ad_from_row)
            .collect::<Result<Vec<_>, _>>()?;
        let more = ads.len() > 20;
        ads.truncate(20);
        self.annotate(&mut ads).await;
        Ok((ads, more))
    }
    async fn mine(&self, owner_id: i64) -> Result<Vec<AIDirectoryAd>, AdError> {
        if owner_id <= 0 {
            return Err(AdError::InvalidInput);
        }
        let mut ads = sqlx::query(&format!(
            "{SELECT_AD} WHERE owner_user_id=$1 ORDER BY paid_at DESC,id DESC LIMIT 100"
        ))
        .bind(owner_id)
        .fetch_all(&self.pg)
        .await
        .map_err(|_| AdError::Storage)?
        .into_iter()
        .map(ad_from_row)
        .collect::<Result<Vec<_>, _>>()?;
        self.annotate(&mut ads).await;
        Ok(ads)
    }
    async fn create(
        &self,
        owner: i64,
        input: AIDirectoryAdInput,
    ) -> Result<(AIDirectoryAd, bool), AdError> {
        if owner <= 0 {
            return Err(AdError::InvalidInput);
        }
        let input = normalize_ad(input)?;
        if let Some(ad) = self.find_request(&input.request_id).await? {
            return if same_request(&ad, owner, &input) {
                Ok((ad, false))
            } else {
                Err(AdError::Conflict)
            };
        }
        let charge = self.quote(input.bid_cents).await?;
        if charge != input.expected_quota {
            return Err(AdError::QuoteChanged);
        }
        let now = Utc::now().timestamp();
        let mut tx = self.pg.begin().await.map_err(|_| AdError::Storage)?;
        let outcome=async {
            let id:i64=sqlx::query_scalar("INSERT INTO ai_directory_ads(owner_user_id,name,url,summary,description,bid_cents,charged_quota,request_id,status,paid_at,expires_at,hidden_at,refunded_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,'active',$9,$10,0,0) RETURNING id::BIGINT")
                .bind(owner).bind(&input.name).bind(&input.url).bind(&input.summary).bind(&input.description).bind(input.bid_cents).bind(charge).bind(&input.request_id).bind(now).bind(now+AD_DURATION_SECONDS).fetch_one(&mut *tx).await.map_err(|_|AdError::Storage)?;
            let charged=sqlx::query("UPDATE users SET quota=quota-$2 WHERE id=$1 AND deleted_at IS NULL AND quota>=$2 AND quota<=$3").bind(owner).bind(charge).bind(MAX_WALLET_QUOTA).execute(&mut *tx).await.map_err(|_|AdError::Storage)?.rows_affected();
            if charged!=1 {return Err(AdError::Insufficient);}
            Ok(AIDirectoryAd{id,owner_user_id:owner,name:input.name.clone(),url:input.url.clone(),summary:input.summary.clone(),description:input.description.clone(),bid_cents:input.bid_cents,charged_quota:charge,charged_amount_usd:None,request_id:input.request_id.clone(),status:"active".into(),paid_at:now,expires_at:now+AD_DURATION_SECONDS,hidden_at:0,refunded_at:0})
        }.await;
        let mut ad = match outcome {
            Ok(ad) => {
                tx.commit().await.map_err(|_| AdError::Storage)?;
                ad
            }
            Err(error) => {
                tx.rollback().await.map_err(|_| AdError::Storage)?;
                if let Some(existing) = self.find_request(&input.request_id).await? {
                    return if same_request(&existing, owner, &input) {
                        Ok((existing, false))
                    } else {
                        Err(AdError::Conflict)
                    };
                }
                return Err(error);
            }
        };
        self.post_commit(&ad, false).await;
        self.annotate(std::slice::from_mut(&mut ad)).await;
        Ok((ad, true))
    }
    async fn hide(&self, id: i64) -> Result<(AIDirectoryAd, bool), AdError> {
        if id <= 0 {
            return Err(AdError::InvalidInput);
        }
        let now = Utc::now().timestamp();
        let mut tx = self.pg.begin().await.map_err(|_| AdError::Storage)?;
        let row = sqlx::query(&format!("{SELECT_AD} WHERE id=$1 FOR UPDATE"))
            .bind(id)
            .fetch_optional(&mut *tx)
            .await
            .map_err(|_| AdError::Storage)?
            .ok_or(AdError::NotFound)?;
        let mut ad = ad_from_row(row)?;
        if ad.status == "hidden" {
            tx.commit().await.map_err(|_| AdError::Storage)?;
            self.annotate(std::slice::from_mut(&mut ad)).await;
            return Ok((ad, false));
        }
        if ad.status != "active" || ad.expires_at <= now {
            return Err(AdError::NotFound);
        }
        let changed=sqlx::query("UPDATE ai_directory_ads SET status='hidden',hidden_at=$2,refunded_at=$2 WHERE id=$1 AND status='active' AND expires_at>$2").bind(id).bind(now).execute(&mut *tx).await.map_err(|_|AdError::Storage)?.rows_affected();
        if changed != 1 {
            return Err(AdError::NotFound);
        }
        if !(0..=MAX_WALLET_QUOTA).contains(&ad.charged_quota) {
            return Err(AdError::WalletRange);
        }
        let credited=sqlx::query("UPDATE users SET quota=quota+$2 WHERE id=$1 AND deleted_at IS NULL AND quota>=$3 AND quota<=$4").bind(ad.owner_user_id).bind(ad.charged_quota).bind(-MAX_WALLET_QUOTA).bind(MAX_WALLET_QUOTA-ad.charged_quota).execute(&mut *tx).await.map_err(|_|AdError::Storage)?.rows_affected();
        if credited != 1 {
            return Err(AdError::WalletRange);
        }
        tx.commit().await.map_err(|_| AdError::Storage)?;
        ad.status = "hidden".into();
        ad.hidden_at = now;
        ad.refunded_at = now;
        self.post_commit(&ad, true).await;
        self.annotate(std::slice::from_mut(&mut ad)).await;
        Ok((ad, true))
    }
    async fn audit_hide(
        &self,
        actor: &DashboardUserView,
        pat: bool,
        ip: &str,
        ad_id: i64,
        refunded: bool,
    ) {
        let other=json!({"op":{"action":"ai_directory_ad.hide","params":{"ad_id":ad_id,"refunded":refunded}},"admin_info":{"admin_id":actor.id,"admin_username":actor.username,"admin_role":actor.role,"auth_method":if pat{"access_token"}else{"session"}}}).to_string();
        self.log(actor.id, 3, "ai_directory_ad.hide", ip, &other)
            .await;
    }
}
