//! Read-only Web projections of immutable personal-key model requests.
//! Self means actor + personal key owner, NOT wallet payer: team-funded personal
//! keys are included, team-owned keys are not. No provider/channel evidence is
//! exposed. Core credits are exact (500,000/USD); holds are never consumption.
use crate::{
    billing::{
        CREDITS_PER_USD,
        charging::{Change, LedgerCommand, Outcome, Price, Source, Usage},
    },
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
use std::collections::{BTreeMap, HashMap, HashSet};
use tokio_stream::StreamExt;

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

// Read at most 100,001 requests and 2,049 commands/refunds per request. The
// extra entry detects density rather than silently truncating successful data.
const MAX_REQUESTS: usize = 100_000;
const MAX_RECEIPTS: usize = 2048;
const JS_SAFE: i64 = 9_007_199_254_740_991;
const SELECT_REQUESTS: &str = r#"
 SELECT r.*, a.kind AS payer_kind, coalesce(k.name,r.oauth_source->>'client_id','') AS token_name,
   e.detail AS final_event,
   (SELECT coalesce(jsonb_agg(to_jsonb(c)), '[]'::jsonb) FROM
      (SELECT operation_id,command FROM core_charging.ledger_commands
       WHERE request_id=r.id ORDER BY operation_id LIMIT 2049) c) AS commands,
   (SELECT coalesce(jsonb_agg(to_jsonb(f)), '[]'::jsonb) FROM
      (SELECT refund_id,amount FROM core_charging.refunds
       WHERE request_id=r.id ORDER BY refund_id LIMIT 2049) f) AS refunds
 FROM core_charging.requests r
 JOIN core_identity.accounts a ON a.id=r.payer_account_id
 LEFT JOIN core_identity.api_key_settings k ON k.credential_id=r.key_id
 LEFT JOIN core_charging.usage_events e ON e.request_id=r.id AND e.event_id='final'
 WHERE r.actor_user_id=$1 AND r.owner_account_id=$2 AND r.authorized_at BETWEEN $3 AND $4
   AND ($5=0 OR CASE WHEN r.state='settled' AND r.outcome='completed' THEN 2 ELSE 5 END=$5)
   AND ($6='' OR r.price->>'model'=$6) AND ($7='' OR coalesce(k.name,r.oauth_source->>'client_id','')=$7)
   AND ($8='' OR r.price->>'group'=$8) AND ($9='' OR r.id=$9)
 ORDER BY r.authorized_at DESC,r.id DESC LIMIT 100001
"#;

fn safe(value: i64) -> Result<i64, Error> {
    if !(0..=JS_SAFE).contains(&value) {
        return Err(Error::Storage);
    }
    Ok(value)
}
fn add(total: &mut i64, value: i64) -> Result<(), Error> {
    *total = safe(total.checked_add(value).ok_or(Error::Storage)?)?;
    Ok(())
}
fn usd(credits: i64) -> String {
    let millionths = i128::from(credits) * 1_000_000 / i128::from(CREDITS_PER_USD);
    format!("{}.{:06}", millionths / 1_000_000, millionths % 1_000_000)
}
#[derive(Default, Clone, Copy)]
struct Amounts {
    gross: i64,
    refunded: i64,
}
impl Amounts {
    fn accumulate(&mut self, other: Self) -> Result<(), Error> {
        add(&mut self.gross, other.gross)?;
        add(&mut self.refunded, other.refunded)
    }
    fn net(self) -> i64 {
        self.gross - self.refunded
    }
    fn project(self, value: &mut Value) {
        value["gross_settled_credits"] = json!(self.gross);
        value["refunded_credits"] = json!(self.refunded);
        value["net_consumption_credits"] = json!(self.net());
        value["billing_basis"] = json!("net_consumption");
        value["billing_currency"] = json!("USD");
        // This API projects consumption, not ledger entries or wallet balances.
        value["wallet_debit_supported"] = json!(false);
        value["quota"] = json!(self.net());
        value["quota_usd"] = json!(usd(self.net()));
        value["quota_money_available"] = json!(true);
        value["quota_money_basis"] = json!("captured_credits_per_usd");
    }
}
// Presentation compatibility only. request_id is the sole canonical identity.
// Hashes can collide across pages; never use this number as storage authority.
fn presentation_id(request_id: &str) -> i64 {
    let hash = Sha256::digest(request_id.as_bytes());
    let n = u64::from_be_bytes(hash[..8].try_into().unwrap()) & ((1 << 52) - 1);
    n.max(1) as i64
}
// Matches the immutable Charging::command receipt identity, never a new command
// submission. Keeping the original request ID and tag prevents refund aliasing.
fn operation_id(request_id: &str, tag: &str) -> Result<String, Error> {
    let encoded =
        serde_json::to_vec(&("core-charging-v1", request_id, tag)).map_err(|_| Error::Storage)?;
    Ok(format!("{:x}", Sha256::digest(encoded)))
}
#[derive(Deserialize)]
struct StoredCommand {
    operation_id: String,
    command: Value,
}
#[derive(Deserialize)]
struct Refund {
    refund_id: String,
    amount: i64,
}
#[derive(Deserialize)]
struct FinalEvent {
    kind: String,
    usage: Usage,
    outcome: Outcome,
}
fn decode<T: serde::de::DeserializeOwned>(value: Value) -> Result<T, Error> {
    serde_json::from_value(value).map_err(|_| Error::Storage)
}
struct ValidatedRequest {
    id: String,
    user: i64,
    key: Option<i64>,
    oauth_source: Option<crate::identity::grants::OAuthSource>,
    at: i64,
    price: Price,
    token_name: String,
    state: String,
    outcome: Option<String>,
    payer_kind: String,
    source: Source,
    reserved: i64,
    usage: Usage,
    amounts: Amounts,
}
impl ValidatedRequest {
    fn from_row(row: PgRow) -> Result<Self, Error> {
        let id: String = row.try_get("id")?;
        let price: Price = decode(row.try_get("price")?)?;
        price.validate().map_err(|_| Error::Storage)?;
        let user = safe(row.try_get("actor_user_id")?)?;
        let key = row
            .try_get::<Option<i64>, _>("key_id")?
            .map(safe)
            .transpose()?;
        let oauth_source: Option<crate::identity::grants::OAuthSource> = row
            .try_get::<Option<Value>, _>("oauth_source")?
            .map(decode)
            .transpose()?;
        match (&key, &oauth_source) {
            (Some(key), None) if *key > 0 => (),
            (None, Some(source)) => source.validate_stored().map_err(|_| Error::Storage)?,
            _ => return Err(Error::Storage),
        }
        let owner: i64 = row.try_get("owner_account_id")?;
        let payer: i64 = row.try_get("payer_account_id")?;
        if let Some(source) = &oauth_source
            && (source.user_id != user
                || source.personal_account_id != owner
                || source.personal_account_id != payer)
        {
            return Err(Error::Storage);
        }
        let source = match row.try_get::<Option<i64>, _>("source_subscription_id")? {
            None => Source::Wallet,
            Some(id) => Source::Subscription {
                id,
                start: row.try_get("source_start")?,
                end: row.try_get("source_end")?,
            },
        };
        let state: String = row.try_get("state")?;
        let outcome: Option<String> = row.try_get("outcome")?;
        let reserved = safe(row.try_get("reserved")?)?;
        let amounts = Amounts {
            gross: safe(row.try_get("settled")?)?,
            refunded: safe(row.try_get("refunded")?)?,
        };
        let mut usage = Usage {
            input: safe(row.try_get("input_tokens")?)?,
            cached: safe(row.try_get("cached_tokens")?)?,
            output: safe(row.try_get("output_tokens")?)?,
            ..Default::default()
        };
        safe(
            usage
                .input
                .checked_add(usage.output)
                .ok_or(Error::Storage)?,
        )?;
        if !usage.valid() || amounts.refunded > amounts.gross || amounts.gross > reserved {
            return Err(Error::Storage);
        }
        let commands: Vec<StoredCommand> = decode(row.try_get("commands")?)?;
        let refunds: Vec<Refund> = decode(row.try_get("refunds")?)?;
        if commands.len() > MAX_RECEIPTS || refunds.len() > MAX_RECEIPTS {
            return Err(Error::TooManyBuckets);
        }
        let mut receipts = HashMap::new();
        for stored in commands {
            let command: LedgerCommand = decode(stored.command.clone())?;
            // LedgerCommand/Source deserialization alone ignores unknown fields.
            // Require the complete canonical receipt, including source shape.
            if serde_json::to_value(&command).map_err(|_| Error::Storage)? != stored.command
                || stored.operation_id != command.operation_id
                || command.request_id != id
                || command.actor_user_id != user
                || command.key_id != key
                || command.oauth_source != oauth_source
                || command.owner_account_id != owner
                || command.payer_account_id != payer
                || command.price_version != price.version
                || command.source != source
                || receipts
                    .insert(stored.operation_id, command.change)
                    .is_some()
            {
                return Err(Error::Storage);
            }
        }
        if state == "settled" {
            let event: FinalEvent = decode(
                row.try_get::<Option<Value>, _>("final_event")?
                    .ok_or(Error::Storage)?,
            )?;
            let final_outcome = match event.outcome {
                Outcome::Completed => "completed",
                Outcome::Cancelled => "cancelled",
                Outcome::Failed => "failed",
            };
            if event.kind != "final"
                || Some(final_outcome) != outcome.as_deref()
                || event.usage.input != usage.input
                || event.usage.cached != usage.cached
                || event.usage.output != usage.output
                || price.cost(event.usage).map_err(|_| Error::Storage)? != amounts.gross
            {
                return Err(Error::Storage);
            }
            usage = event.usage;
            if receipts.remove(&operation_id(&id, "settle")?)
                != Some(Change::Settle {
                    amount: amounts.gross,
                })
            {
                return Err(Error::Storage);
            }
            let mut refund_total = 0;
            for refund in refunds {
                if refund.amount <= 0
                    || receipts.remove(&operation_id(&id, &format!("refund:{}", refund.refund_id))?)
                        != Some(Change::Refund {
                            amount: refund.amount,
                        })
                {
                    return Err(Error::Storage);
                }
                add(&mut refund_total, refund.amount)?;
            }
            if refund_total != amounts.refunded {
                return Err(Error::Storage);
            }
        } else if amounts.gross != 0
            || amounts.refunded != 0
            || !refunds.is_empty()
            || outcome.is_some()
            || !matches!(
                state.as_str(),
                "reserved" | "streaming" | "reconcile" | "released"
            )
        {
            return Err(Error::Storage);
        }
        // A refund/settle command with no matching row/terminal record is corrupt,
        // even if it uses another operation ID or claims a different amount.
        if receipts
            .values()
            .any(|c| matches!(c, Change::Refund { .. } | Change::Settle { .. }))
        {
            return Err(Error::Storage);
        }
        Ok(Self {
            id,
            user,
            key,
            oauth_source,
            at: safe(row.try_get("authorized_at")?)?,
            price,
            token_name: row.try_get("token_name")?,
            state,
            outcome,
            payer_kind: row.try_get("payer_kind")?,
            source,
            reserved,
            usage,
            amounts,
        })
    }
    fn settled(&self) -> bool {
        self.state == "settled"
    }
    fn held(&self) -> bool {
        matches!(self.state.as_str(), "reserved" | "streaming" | "reconcile")
    }
    fn subscription(&self) -> bool {
        matches!(self.source, Source::Subscription { .. })
    }
    fn log(&self) -> Value {
        let mut other = json!({"credential_source":if self.oauth_source.is_some(){"oauth_grant"}else{"api_key"},"request_state":self.state,"usage_status":if self.settled() {"settled"}else{"incomplete"},
            "reserved_credits":if self.held() {self.reserved}else{0},
            "cache_tokens":if self.settled() {Some(self.usage.cached)}else{None},
            "billing_source":if self.subscription() {"subscription"}else{"wallet"},
            "payer_kind":self.payer_kind,"pricing_credits_per_usd":CREDITS_PER_USD,
            "billing_basis":"incomplete","wallet_debit_supported":false});
        let mut item = json!({"id":presentation_id(&self.id),"request_id":self.id,"user_id":self.user,"token_id":self.key,
            "created_at":self.at,"type":if self.settled() && self.outcome.as_deref()==Some("completed") {2}else{5},
            "content":self.outcome.clone().unwrap_or_else(||format!("Model request {}; no final usage receipt",self.state)),
            "model_name":self.price.model,"group":self.price.group,"token_name":self.token_name,"state":self.state,
            "payer_kind":self.payer_kind,"team_funded":self.payer_kind=="team","subscription_funded":self.subscription(),
            "quota":0,"quota_money_available":false,"billing_basis":"incomplete","wallet_debit_supported":false});
        if self.settled() {
            self.amounts.project(&mut item);
            self.amounts.project(&mut other);
            item["prompt_tokens"] = json!(self.usage.input);
            item["completion_tokens"] = json!(self.usage.output);
        }
        item["other"] = json!(other.to_string());
        item
    }
}
#[derive(Default)]
struct Summary {
    amounts: Amounts,
    reserved: i64,
    incomplete: i64,
    rpm: i64,
    tpm: i64,
    tokens: i64,
    team_net: i64,
    personal_wallet_source_net: i64,
}
fn recent(at: i64, now: i64) -> bool {
    at > now - 60 && at <= now
}
impl Summary {
    fn include(&mut self, r: &ValidatedRequest, now: i64) -> Result<(), Error> {
        if r.held() {
            add(&mut self.reserved, r.reserved)?;
            add(&mut self.incomplete, 1)?;
        }
        if r.settled() {
            self.amounts.accumulate(r.amounts)?;
            add(&mut self.tokens, r.usage.input + r.usage.output)?;
            if r.payer_kind == "team" {
                add(&mut self.team_net, r.amounts.net())?;
            }
            if r.payer_kind == "personal" && !r.subscription() {
                add(&mut self.personal_wallet_source_net, r.amounts.net())?;
            }
            if recent(r.at, now) {
                add(&mut self.rpm, 1)?;
                add(&mut self.tpm, r.usage.input + r.usage.output)?;
            }
        }
        Ok(())
    }
    fn project(&self) -> Value {
        let mut data = json!({"rpm":self.rpm,"tpm":self.tpm,"reserved_credits":self.reserved,"incomplete_count":self.incomplete,
            "usage_scope":"actor_personal_key","settlement_scope":"settled_requests","spending_scope":"all_payers","team_funded_quota":self.team_net,
            "personal_wallet_quota":self.personal_wallet_source_net,"personal_wallet_quota_basis":"wallet_source_net_consumption_not_debit"});
        self.amounts.project(&mut data);
        data
    }
}
#[derive(Default)]
struct Bucket {
    amounts: Amounts,
    tokens: i64,
    count: i64,
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
        let actor = identity::authorize_in_transaction(&mut tx, secret).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(IdentityError::Forbidden.into());
        }
        safe(actor.user_id)?;
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
        let mut summary = Summary::default();
        let mut total = 0;
        let mut items = Vec::new();
        let mut ids = HashSet::new();
        let mut buckets: BTreeMap<(i64, String, String, bool), Bucket> = BTreeMap::new();
        {
            let mut rows = sqlx::query(SELECT_REQUESTS)
                .bind(actor.user_id)
                .bind(owner)
                .bind(f.start)
                .bind(f.end)
                .bind(q.r#type.unwrap_or(0))
                .bind(q.model_name.unwrap_or_default())
                .bind(q.token_name.unwrap_or_default())
                .bind(q.group.unwrap_or_default())
                .bind(q.request_id.unwrap_or_default())
                .fetch(&mut *tx);
            while let Some(row) = rows.next().await {
                add(&mut total, 1)?;
                if total as usize > MAX_REQUESTS {
                    return Err(Error::TooManyBuckets);
                }
                // All endpoints validate every selected request BEFORE exposing
                // a page or aggregate, including explicit zero-cost receipts.
                let r = ValidatedRequest::from_row(row?)?;
                summary.include(&r, now)?;
                match projection {
                    Projection::Logs if total > f.offset && total <= f.offset + f.size => {
                        let item = r.log();
                        if !ids.insert(presentation_id(&r.id)) {
                            return Err(Error::Collision);
                        }
                        items.push(item);
                    }
                    Projection::Data if r.settled() => {
                        let key = (
                            r.at / 3600 * 3600,
                            r.price.model.clone(),
                            r.payer_kind.clone(),
                            r.subscription(),
                        );
                        let bucket = buckets.entry(key).or_default();
                        bucket.amounts.accumulate(r.amounts)?;
                        add(&mut bucket.tokens, r.usage.input + r.usage.output)?;
                        add(&mut bucket.count, 1)?;
                        if buckets.len() > MAX_BUCKETS {
                            return Err(Error::TooManyBuckets);
                        }
                    }
                    _ => (),
                }
            }
        }
        let result = match projection {
            Projection::Logs => {
                json!({"items":items,"total":total,"page":f.page,"page_size":f.size})
            }
            Projection::Stats => summary.project(),
            Projection::Data => {
                let mut data = Vec::new();
                for ((at, model, payer_kind, subscription), bucket) in buckets {
                    let mut item = json!({"user_id":actor.user_id,"model_name":model,"created_at":at,"token_used":bucket.tokens,
                        "count":bucket.count,"payer_kind":payer_kind,"subscription_funded":subscription,"usage_scope":"actor_personal_key","settlement_scope":"settled_requests"});
                    bucket.amounts.project(&mut item);
                    data.push(item);
                }
                json!(data)
            }
        };
        tx.commit().await?;
        Ok(result)
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
    let mut response = if request.uri().query().is_some_and(|q| q.len() > 4096) {
        (StatusCode::BAD_REQUEST, Json(json!({"success":false,"code":"USAGE_INVALID","message":"Usage query exceeds 4096 bytes"}))).into_response()
    } else {
        next.run(request).await
    };
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
                    "Use a smaller usage window or fewer receipts",
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

/// Scoped OAuth-family activity from real immutable, source-checked ledger
/// receipts. Bounded complete recent window; excessive density is unavailable,
/// never partial success or a fabricated zero. No other family/user or channel.
pub(crate) async fn read_oauth_usage(
    c: &mut sqlx::PgConnection,
    user: i64,
    account: i64,
    grant: &str,
) -> Result<Value, ()> {
    let now: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(&mut *c)
            .await
            .map_err(|_| ())?;
    let start = (now - WINDOW).max(0);
    let query = SELECT_REQUESTS
        .replace(
            "WHERE r.actor_user_id=$1",
            "WHERE r.oauth_family_id=$10 AND r.actor_user_id=$1",
        )
        .replace("LIMIT 100001", "LIMIT 1001");
    let rows = sqlx::query(&query)
        .bind(user)
        .bind(account)
        .bind(start)
        .bind(now)
        .bind(0i16)
        .bind("")
        .bind("")
        .bind("")
        .bind("")
        .bind(grant)
        .fetch_all(c)
        .await
        .map_err(|_| ())?;
    if rows.len() > 1000 {
        return Err(());
    }
    let mut summary = Summary::default();
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        let request = ValidatedRequest::from_row(row).map_err(|_| ())?;
        if request.oauth_source.as_ref().is_none_or(|source| {
            source.grant_id() != grant
                || source.user_id() != user
                || source.personal_account_id() != account
        }) {
            return Err(());
        }
        summary.include(&request, now).map_err(|_| ())?;
        items.push(request.log());
    }
    Ok(
        json!({"schema_version":1,"usage_scope":"oauth_family","start_timestamp":start,"end_timestamp":now,"items":items,"summary":summary.project()}),
    )
}

#[cfg(test)]
mod index_tests;

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn recent_window_is_open_at_sixty_seconds_and_closed_at_now() {
        let now = 1_700_000_000;
        assert!(!recent(now - 60, now));
        assert!(recent(now - 59, now));
        assert!(recent(now, now));
        assert!(!recent(now + 1, now));
    }
    #[test]
    fn safe_projection_integers_and_aggregate_overflow_are_rejected() {
        assert!(safe(JS_SAFE).is_ok());
        assert!(safe(JS_SAFE + 1).is_err());
        assert!(safe(-1).is_err());
        let mut total = JS_SAFE;
        assert!(add(&mut total, 1).is_err());
        let mut total = i64::MAX;
        assert!(add(&mut total, 1).is_err());
    }
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
