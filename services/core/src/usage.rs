//! Read-only Web projections of immutable personal-key model requests.
//! Self means actor + personal key owner, NOT wallet payer: team-funded personal
//! keys are included, team-owned keys are not. No provider/channel evidence is
//! exposed. Core credits are exact (500,000/USD); holds are never consumption.
use crate::{
    billing::{CREDITS_PER_USD, charging::Price},
    identity::{self, CredentialKind, IdentityError},
};
use axum::{
    Json, Router,
    extract::Request,
    extract::{Query, State},
    http::{HeaderMap, StatusCode, header},
    middleware::{self, Next},
    response::{IntoResponse, Response},
    routing::get,
};
use serde::Deserialize;
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row, postgres::PgRow};
use std::collections::HashSet;

const WINDOW: i64 = 31 * 86400;
const MAX_OFFSET: i64 = 100_000;
const MAX_BUCKETS: usize = 10_000;

#[derive(Clone)]
pub struct UsageStore {
    pool: PgPool,
}
impl UsageStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }
}

#[derive(Debug)]
enum Error {
    Identity(IdentityError),
    Invalid,
    Storage,
    Collision,
    TooManyBuckets,
}
impl From<sqlx::Error> for Error {
    fn from(_: sqlx::Error) -> Self {
        Self::Storage
    }
}
impl From<IdentityError> for Error {
    fn from(e: IdentityError) -> Self {
        Self::Identity(e)
    }
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct UsageQuery {
    p: Option<i64>,
    page_size: Option<i64>,
    r#type: Option<i16>,
    start_timestamp: Option<i64>,
    end_timestamp: Option<i64>,
    model_name: Option<String>,
    token_name: Option<String>,
    group: Option<String>,
    request_id: Option<String>,
    default_time: Option<String>,
    username: Option<String>,
    channel: Option<i64>,
    upstream_request_id: Option<String>,
}
struct Filter {
    start: i64,
    end: i64,
    page: i64,
    size: i64,
    offset: i64,
}
impl UsageQuery {
    fn validate(&self, now: i64) -> Result<Filter, Error> {
        let end = self.end_timestamp.unwrap_or(now);
        let start = self
            .start_timestamp
            .unwrap_or(end.saturating_sub(WINDOW).max(0));
        let page = self.p.unwrap_or(1);
        let size = self.page_size.unwrap_or(20);
        let offset = page
            .checked_sub(1)
            .and_then(|p| p.checked_mul(size))
            .ok_or(Error::Invalid)?;
        if start < 0
            || end < start
            || end > 253402300799
            || end - start > WINDOW
            || page < 1
            || !(1..=100).contains(&size)
            || !(0..=MAX_OFFSET).contains(&offset)
            || self.r#type.is_some_and(|t| !(0..=7).contains(&t))
            || self
                .default_time
                .as_deref()
                .is_some_and(|t| !matches!(t, "hour" | "day" | "week" | "month"))
            || self.channel.is_some_and(|c| c != 0)
            || [&self.username, &self.upstream_request_id]
                .iter()
                .any(|s| s.as_deref().is_some_and(|v| !v.is_empty()))
            || [
                &self.model_name,
                &self.token_name,
                &self.group,
                &self.request_id,
            ]
            .iter()
            .any(|s| {
                s.as_deref()
                    .is_some_and(|v| v.len() > 128 || v.chars().any(char::is_control))
            })
        {
            return Err(Error::Invalid);
        }
        Ok(Filter {
            start,
            end,
            page,
            size,
            offset,
        })
    }
}

// These are durable settlement receipts written by Charging::finish and the
// TransactionalLedger in one transaction, not estimates or provider checkpoints.
// Fail closed on missing/inconsistent receipts, including legitimate zero cost.
const SCOPED: &str = r#"WITH scoped AS (
 SELECT r.*, a.kind AS payer_kind, coalesce(k.name,'') AS token_name,
   CASE WHEN r.state='settled' AND r.outcome='completed' THEN 2 ELSE 5 END AS log_type,
   (r.settled>=r.refunded AND r.settled<=r.reserved AND r.input_tokens>=0
     AND r.cached_tokens BETWEEN 0 AND r.input_tokens AND r.output_tokens>=0
     AND coalesce(length(r.price->>'model'),0)>0 AND coalesce(length(r.price->>'group'),0)>0
     AND r.outcome IN ('completed','cancelled','failed')
     AND EXISTS (SELECT 1 FROM core_charging.usage_events e WHERE e.request_id=r.id AND e.event_id='final'
       AND e.detail @> jsonb_build_object('kind','final','outcome',r.outcome,'usage',
          jsonb_build_object('input',r.input_tokens,'cached',r.cached_tokens,'output',r.output_tokens)))
     AND EXISTS (SELECT 1 FROM core_charging.ledger_commands c WHERE c.request_id=r.id
       AND c.command @> jsonb_build_object('request_id',r.id,'actor_user_id',r.actor_user_id,
          'key_id',r.key_id,'owner_account_id',r.owner_account_id,'payer_account_id',r.payer_account_id,
          'price_version',r.price->>'version','change',jsonb_build_object('operation','settle','amount',r.settled),
          'source',CASE WHEN r.source_subscription_id IS NULL THEN jsonb_build_object('kind','wallet')
             ELSE jsonb_build_object('kind','subscription','id',r.source_subscription_id,'start',r.source_start,'end',r.source_end) END))
     AND r.refunded=coalesce((SELECT sum(f.amount) FROM core_charging.refunds f WHERE f.request_id=r.id),0)
   ) AS receipt_valid
 FROM core_charging.requests r
 JOIN core_identity.accounts a ON a.id=r.payer_account_id
 LEFT JOIN core_identity.api_key_settings k ON k.credential_id=r.key_id
 WHERE r.actor_user_id=$1 AND r.owner_account_id=$2 AND r.authorized_at BETWEEN $3 AND $4
), filtered AS (SELECT * FROM scoped WHERE ($5=0 OR log_type=$5)
 AND ($6='' OR price->>'model'=$6) AND ($7='' OR token_name=$7)
 AND ($8='' OR price->>'group'=$8) AND ($9='' OR id=$9)) "#;

fn bind<'q>(
    sql: &'q str,
    actor: i64,
    owner: i64,
    f: &Filter,
    q: &UsageQuery,
) -> sqlx::query::Query<'q, sqlx::Postgres, sqlx::postgres::PgArguments> {
    sqlx::query(sql)
        .bind(actor)
        .bind(owner)
        .bind(f.start)
        .bind(f.end)
        .bind(q.r#type.unwrap_or(0))
        .bind(q.model_name.clone().unwrap_or_default())
        .bind(q.token_name.clone().unwrap_or_default())
        .bind(q.group.clone().unwrap_or_default())
        .bind(q.request_id.clone().unwrap_or_default())
}
fn usd(credits: i64) -> String {
    let millionths = i128::from(credits) * 1_000_000 / i128::from(CREDITS_PER_USD);
    format!("{}.{:06}", millionths / 1_000_000, millionths % 1_000_000)
}
fn money(value: &mut Value, credits: i64) {
    value["quota"] = json!(credits);
    value["quota_usd"] = json!(usd(credits));
    value["quota_money_available"] = json!(true);
    // Native core credits have an immutable USD scale; use the Web's existing
    // receipt-projection basis rather than a new, unrecognized currency label.
    value["quota_money_basis"] = json!("captured_credits_per_usd");
}
// Presentation compatibility only. request_id is the sole canonical identity.
// Hashes can collide across pages; never use this number as storage authority.
fn presentation_id(request_id: &str) -> i64 {
    let hash = Sha256::digest(request_id.as_bytes());
    let n = u64::from_be_bytes(hash[..8].try_into().unwrap()) & ((1 << 52) - 1);
    n.max(1) as i64
}
fn check_receipts(row: &PgRow) -> Result<(), Error> {
    if row.try_get::<i64, _>("invalid_receipts")? != 0 {
        return Err(Error::Storage);
    }
    Ok(())
}
#[derive(Clone, Copy)]
enum Projection {
    Logs,
    Stats,
    Data,
}
impl UsageStore {
    async fn read(
        &self,
        secret: &str,
        q: UsageQuery,
        projection: Projection,
    ) -> Result<Value, Error> {
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
            .execute(&mut *tx)
            .await?;
        // Identity locks keep revocation and read authority in one snapshot.
        let actor = identity::authorize_in_transaction(&mut tx, secret).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        let owner: i64 =
            sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=$1")
                .bind(actor.user_id)
                .fetch_one(&mut *tx)
                .await?;
        let now: i64 =
            sqlx::query_scalar("SELECT floor(extract(epoch FROM transaction_timestamp()))::bigint")
                .fetch_one(&mut *tx)
                .await?;
        let f = q.validate(now)?;
        let aggregate = format!("{SCOPED} SELECT count(*)::bigint AS total,
            count(*) FILTER(WHERE state='settled' AND receipt_valid IS NOT TRUE)::bigint AS invalid_receipts,
            coalesce(sum(settled-refunded) FILTER(WHERE state='settled'),0)::bigint AS quota,
            coalesce(sum(settled-refunded) FILTER(WHERE state='settled' AND payer_kind='team'),0)::bigint AS team_funded_quota,
            coalesce(sum(settled-refunded) FILTER(WHERE state='settled' AND payer_kind='personal' AND source_subscription_id IS NULL),0)::bigint AS personal_wallet_quota,
            coalesce(sum(reserved) FILTER(WHERE state IN ('reserved','streaming','reconcile')),0)::bigint AS reserved_credits,
            count(*) FILTER(WHERE state IN ('reserved','streaming','reconcile'))::bigint AS incomplete_count,
            count(*) FILTER(WHERE state='settled' AND authorized_at>$10-60 AND authorized_at<=$10)::bigint AS rpm,
            coalesce(sum(input_tokens::numeric+output_tokens) FILTER(WHERE state='settled' AND authorized_at>$10-60 AND authorized_at<=$10),0)::bigint AS tpm
            FROM filtered");
        let summary = bind(&aggregate, actor.user_id, owner, &f, &q)
            .bind(now)
            .fetch_one(&mut *tx)
            .await?;
        check_receipts(&summary)?;
        let data = match projection {
            Projection::Stats => {
                let mut data = json!({"rpm":summary.try_get::<i64,_>("rpm")?,"tpm":summary.try_get::<i64,_>("tpm")?,
                    "reserved_credits":summary.try_get::<i64,_>("reserved_credits")?,"incomplete_count":summary.try_get::<i64,_>("incomplete_count")?,
                    "usage_scope":"actor_personal_key","spending_scope":"all_payers",
                    "team_funded_quota":summary.try_get::<i64,_>("team_funded_quota")?,
                    "personal_wallet_quota":summary.try_get::<i64,_>("personal_wallet_quota")?});
                money(&mut data, summary.try_get("quota")?);
                data
            }
            Projection::Data => {
                // Web consumers aggregate hourly rows into day/week/month locally.
                let sql=format!("{SCOPED} SELECT price->>'model' AS model_name,
                    (authorized_at/3600)*3600 AS created_at, payer_kind, source_subscription_id IS NOT NULL AS subscription_funded,
                    sum(input_tokens::numeric+output_tokens)::bigint AS token_used,count(*)::bigint AS count,
                    sum(settled-refunded)::bigint AS quota FROM filtered WHERE state='settled'
                    GROUP BY 1,2,3,4 ORDER BY 2,1,3,4 LIMIT 10001");
                let rows = bind(&sql, actor.user_id, owner, &f, &q)
                    .fetch_all(&mut *tx)
                    .await?;
                if rows.len() > MAX_BUCKETS {
                    return Err(Error::TooManyBuckets);
                }
                let mut items = Vec::new();
                for row in rows {
                    let mut item = json!({"user_id":actor.user_id,"model_name":row.try_get::<String,_>("model_name")?,
                        "created_at":row.try_get::<i64,_>("created_at")?,"token_used":row.try_get::<i64,_>("token_used")?,
                        "count":row.try_get::<i64,_>("count")?,"payer_kind":row.try_get::<String,_>("payer_kind")?,
                        "subscription_funded":row.try_get::<bool,_>("subscription_funded")?,"usage_scope":"actor_personal_key"});
                    money(&mut item, row.try_get("quota")?);
                    items.push(item);
                }
                json!(items)
            }
            Projection::Logs => {
                let sql = format!(
                    "{SCOPED} SELECT * FROM filtered ORDER BY authorized_at DESC,id DESC LIMIT $10 OFFSET $11"
                );
                let rows = bind(&sql, actor.user_id, owner, &f, &q)
                    .bind(f.size)
                    .bind(f.offset)
                    .fetch_all(&mut *tx)
                    .await?;
                let mut items = Vec::new();
                let mut ids = HashSet::new();
                for row in rows {
                    let request_id: String = row.try_get("id")?;
                    let id = presentation_id(&request_id);
                    if !ids.insert(id) {
                        return Err(Error::Collision);
                    }
                    let price: Price = serde_json::from_value(row.try_get("price")?)
                        .map_err(|_| Error::Storage)?;
                    price.validate().map_err(|_| Error::Storage)?;
                    let state: String = row.try_get("state")?;
                    let settled = state == "settled";
                    let mut item = json!({"id":id,"request_id":request_id,"user_id":actor.user_id,
                        "created_at":row.try_get::<i64,_>("authorized_at")?,"type":row.try_get::<i32,_>("log_type")?,
                        "content":if settled {row.try_get::<Option<String>,_>("outcome")?.ok_or(Error::Storage)?} else {format!("Model request {state}; no final usage receipt")},
                        "model_name":price.model,"group":price.group,"token_id":row.try_get::<i64,_>("key_id")?,
                        "token_name":row.try_get::<String,_>("token_name")?,"state":state,
                        "payer_kind":row.try_get::<String,_>("payer_kind")?,"team_funded":row.try_get::<String,_>("payer_kind")?=="team",
                        "subscription_funded":row.try_get::<Option<i64>,_>("source_subscription_id")?.is_some(),
                        "quota_money_available":false,"quota":0,
                        "other":json!({"request_state":state,"usage_status":if settled {"settled"}else{"incomplete"},
                            "reserved_credits":if matches!(state.as_str(),"reserved"|"streaming"|"reconcile") {row.try_get::<i64,_>("reserved")?} else {0},
                            "cache_tokens":if settled {Some(row.try_get::<i64,_>("cached_tokens")?)} else {None},
                            "billing_source":if row.try_get::<Option<i64>,_>("source_subscription_id")?.is_some() {"subscription"} else {"wallet"},
                            "payer_kind":row.try_get::<String,_>("payer_kind")?,
                            "pricing_credits_per_usd":CREDITS_PER_USD}).to_string()});
                    if settled {
                        item["prompt_tokens"] = json!(row.try_get::<i64, _>("input_tokens")?);
                        item["completion_tokens"] = json!(row.try_get::<i64, _>("output_tokens")?);
                        money(
                            &mut item,
                            row.try_get::<i64, _>("settled")?
                                - row.try_get::<i64, _>("refunded")?,
                        );
                    }
                    items.push(item);
                }
                json!({"items":items,"total":summary.try_get::<i64,_>("total")?,"page":f.page,"page_size":f.size})
            }
        };
        tx.commit().await?;
        Ok(data)
    }
}

pub fn router(store: UsageStore) -> Router {
    Router::new()
        .route("/api/log/self", get(logs))
        .route("/api/log/self/stat", get(stats))
        .route("/api/data/self", get(data))
        .with_state(store)
        .layer(middleware::from_fn(no_store))
}
async fn no_store(request: Request, next: Next) -> Response {
    let mut response = next.run(request).await;
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        axum::http::HeaderValue::from_static("no-store"),
    );
    response
}
async fn respond(
    store: UsageStore,
    headers: HeaderMap,
    query: Result<Query<UsageQuery>, axum::extract::rejection::QueryRejection>,
    projection: Projection,
) -> Response {
    let result = async {
        if headers.get_all(header::AUTHORIZATION).iter().count() != 1 {
            return Err(IdentityError::Unauthorized.into());
        }
        let (scheme, secret) = headers
            .get(header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .and_then(|v| v.split_once(' '))
            .ok_or(Error::Identity(IdentityError::Unauthorized))?;
        if !scheme.eq_ignore_ascii_case("Bearer") || secret.is_empty() {
            return Err(IdentityError::Unauthorized.into());
        }
        let Query(query) = query.map_err(|_| Error::Invalid)?;
        store.read(secret, query, projection).await
    }
    .await;
    match result {
        Ok(data) => (
            StatusCode::OK,
            Json(json!({"success":true,"message":"","data":data})),
        )
            .into_response(),
        Err(e) => {
            let (status, code, message) = match e {
                Error::Identity(IdentityError::Unauthorized) => (
                    StatusCode::UNAUTHORIZED,
                    "AUTH_UNAUTHORIZED",
                    "Authentication is required",
                ),
                Error::Identity(IdentityError::Forbidden) => (
                    StatusCode::FORBIDDEN,
                    "USAGE_FORBIDDEN",
                    "A browser session is required",
                ),
                Error::Invalid => (
                    StatusCode::BAD_REQUEST,
                    "USAGE_INVALID",
                    "Invalid usage filters, pagination or time window",
                ),
                Error::Collision => (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "USAGE_ID_COLLISION",
                    "Log presentation identifiers collide",
                ),
                Error::TooManyBuckets => (
                    StatusCode::BAD_REQUEST,
                    "USAGE_WINDOW_TOO_DENSE",
                    "Use a smaller usage window",
                ),
                _ => (
                    StatusCode::SERVICE_UNAVAILABLE,
                    "USAGE_UNAVAILABLE",
                    "Persisted usage is temporarily unavailable",
                ),
            };
            (
                status,
                Json(json!({"success":false,"code":code,"message":message})),
            )
                .into_response()
        }
    }
}
async fn logs(
    State(s): State<UsageStore>,
    h: HeaderMap,
    q: Result<Query<UsageQuery>, axum::extract::rejection::QueryRejection>,
) -> Response {
    respond(s, h, q, Projection::Logs).await
}
async fn stats(
    State(s): State<UsageStore>,
    h: HeaderMap,
    q: Result<Query<UsageQuery>, axum::extract::rejection::QueryRejection>,
) -> Response {
    respond(s, h, q, Projection::Stats).await
}
async fn data(
    State(s): State<UsageStore>,
    h: HeaderMap,
    q: Result<Query<UsageQuery>, axum::extract::rejection::QueryRejection>,
) -> Response {
    respond(s, h, q, Projection::Data).await
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_usd_and_presentation_only_ids() {
        assert_eq!(usd(1), "0.000002");
        assert_eq!(usd(500_000), "1.000000");
        assert_eq!(usd(i64::MAX), "18446744073709.551614");
        for id in ["one", "two"] {
            assert!((1..(1 << 52)).contains(&presentation_id(id)));
        }
        assert_eq!(presentation_id("one"), presentation_id("one"));
    }
}
