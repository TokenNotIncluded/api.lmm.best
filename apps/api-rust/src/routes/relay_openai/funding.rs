//! Durable funding for one logical relay request. Lock order is always
//! payer -> reservation -> active subscriptions -> selected subscription ->
//! token. Provider I/O never runs while any of these locks are held.

use axum::http::StatusCode;
use serde_json::{Value, json};
use sqlx::{PgPool, Postgres, Row, Transaction};

use super::{OpenAiRelayFailure, epoch_seconds, internal_failure};

const MAX_WALLET: i64 = 9_007_199_254_740_991;

#[derive(Clone, Debug, sqlx::FromRow)]
pub(super) struct Record {
    pub reservation_id: String,
    pub request_id: String,
    pub user_id: i64,
    pub token_id: i64,
    pub channel_id: i64,
    pub model_name: String,
    pub using_group: String,
    pub is_stream: bool,
    pub funding_source: String,
    pub expected_quota: i64,
    pub wallet_reserved: i64,
    pub subscription_id: i64,
    pub subscription_reserved: i64,
    pub reserved_version: i64,
    pub token_reserved: i64,
    pub wallet_overflow: bool,
    pub wallet_settled: i64,
    pub subscription_settled: i64,
    pub actual_quota: Option<i64>,
    pub price_snapshot: Value,
    pub usage_snapshot: Option<Value>,
    pub log_metadata: Value,
    pub status: String,
}

pub(super) struct Request<'a> {
    pub request_id: &'a str,
    pub user_id: i64,
    pub token_id: i64,
    pub channel_id: i64,
    pub model_name: &'a str,
    pub using_group: &'a str,
    pub is_stream: bool,
    pub expected_quota: i64,
    pub free: bool,
    pub price_snapshot: Value,
    pub existing_id: Option<&'a str>,
}

#[derive(Clone, Debug, sqlx::FromRow)]
struct Subscription {
    id: i64,
    amount_total: i64,
    reset_amount: Option<i64>,
    renewal_amount: Option<i64>,
    amount_used: i64,
    quota_version: i64,
    allow_wallet_overflow: bool,
}

impl Subscription {
    fn has_finite_quota(&self) -> bool {
        self.amount_total > 0 || self.reset_amount.is_some() || self.renewal_amount.is_some()
    }
}

fn denied(message: &str) -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(StatusCode::FORBIDDEN, "insufficient_user_quota", message)
}

fn state_error() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(
        StatusCode::CONFLICT,
        "billing_state_mismatch",
        "billing request state mismatch",
    )
}

async fn user_lock(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
) -> Result<(i64, String), OpenAiRelayFailure> {
    sqlx::query_as("SELECT COALESCE(quota,0)::BIGINT,COALESCE(to_jsonb(u)->>'setting','') FROM users u WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
        .bind(user).fetch_optional(&mut **tx).await.map_err(|_|internal_failure())?.ok_or_else(internal_failure)
}

fn preference(setting: &str) -> &str {
    // Return a static spelling, so corrupt/unknown persisted preferences have
    // exactly the same subscription-first fallback as Go.
    let parsed = serde_json::from_str::<Value>(setting).unwrap_or_default();
    match parsed
        .get("billing_preference")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim()
    {
        "wallet_first" => "wallet_first",
        "wallet_only" => "wallet_only",
        "subscription_only" => "subscription_only",
        _ => "subscription_first",
    }
}

async fn active_subscriptions(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
    reset: bool,
) -> Result<Vec<Subscription>, OpenAiRelayFailure> {
    let now = epoch_seconds();
    let ids:Vec<i64>=sqlx::query_scalar("SELECT id FROM user_subscriptions WHERE user_id=$1 AND status='active' AND end_time>$2 ORDER BY end_time,id FOR UPDATE")
        .bind(user).bind(now).fetch_all(&mut **tx).await.map_err(|_|internal_failure())?;
    if reset {
        for id in &ids {
            crate::routes::billing_subscriptions::reset_subscription_for_relay(tx, *id, now)
                .await
                .map_err(|_| internal_failure())?;
        }
    }
    if ids.is_empty() {
        return Ok(Vec::new());
    }
    // Preserve the already locked ordering after request-triggered resets.
    sqlx::query_as("SELECT id,amount_total,reset_amount,renewal_amount,amount_used,quota_version,allow_wallet_overflow FROM user_subscriptions WHERE id=ANY($1) ORDER BY end_time,id")
        .bind(ids).fetch_all(&mut **tx).await.map_err(|_|internal_failure())
}

async fn pending_wallet(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
    excluded: &str,
) -> Result<i64, OpenAiRelayFailure> {
    sqlx::query_scalar("SELECT COALESCE(SUM(GREATEST(token_consumed-pre_consumed,0)),0)::BIGINT FROM subscription_pre_consume_records WHERE user_id=$1 AND billing_managed=TRUE AND wallet_overflow=TRUE AND status IN ('consumed','settling') AND request_id<>$2")
        .bind(user).bind(excluded).fetch_one(&mut **tx).await.map_err(|_|internal_failure())
}

async fn authorize_wallet(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
    excluded: &str,
    budget: i64,
) -> Result<bool, OpenAiRelayFailure> {
    let quota: i64 = sqlx::query_scalar("SELECT quota::BIGINT FROM users WHERE id=$1")
        .bind(user)
        .fetch_one(&mut **tx)
        .await
        .map_err(|_| internal_failure())?;
    let pending = pending_wallet(tx, user, excluded).await?;
    Ok(quota > 0
        && pending
            .checked_add(budget)
            .is_some_and(|needed| quota >= needed))
}

async fn wallet_delta(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
    delta: i64,
) -> Result<(), OpenAiRelayFailure> {
    if delta == 0 {
        return Ok(());
    }
    let low = (-MAX_WALLET)
        .checked_sub(delta)
        .ok_or_else(internal_failure)?;
    let high = MAX_WALLET.checked_sub(delta).ok_or_else(internal_failure)?;
    let result=sqlx::query("UPDATE users SET quota=quota+$2 WHERE id=$1 AND deleted_at IS NULL AND quota>=$3 AND quota<=$4")
        .bind(user).bind(delta).bind(low).bind(high).execute(&mut **tx).await.map_err(|_|internal_failure())?;
    if result.rows_affected() != 1 {
        return Err(denied("wallet quota is outside the supported range"));
    }
    Ok(())
}

/// Unlimited tokens bypass only the balance predicate; Go still updates both
/// remain_quota and used_quota. Finalization can update a soft-deleted token.
async fn token_delta(
    tx: &mut Transaction<'_, Postgres>,
    user: i64,
    token: i64,
    delta: i64,
    reserve: bool,
) -> Result<(), OpenAiRelayFailure> {
    if delta == 0 {
        return Ok(());
    }
    let row=sqlx::query("SELECT COALESCE(remain_quota,0)::BIGINT AS remaining,COALESCE(used_quota,0)::BIGINT AS used,COALESCE(unlimited_quota,FALSE) AS unlimited,COALESCE(status,1)::BIGINT AS status,COALESCE(expired_time,-1)::BIGINT AS expires,deleted_at IS NULL AS active FROM tokens WHERE id=$1 AND user_id=$2 FOR UPDATE")
        .bind(token).bind(user).fetch_optional(&mut **tx).await.map_err(|_|internal_failure())?.ok_or_else(internal_failure)?;
    let remaining: i64 = row.try_get("remaining").map_err(|_| internal_failure())?;
    let used: i64 = row.try_get("used").map_err(|_| internal_failure())?;
    let unlimited: bool = row.try_get("unlimited").map_err(|_| internal_failure())?;
    let expired: i64 = row.try_get("expires").map_err(|_| internal_failure())?;
    if reserve
        && (!row.try_get::<bool, _>("active").unwrap_or(false)
            || row.try_get::<i64, _>("status").ok() != Some(1)
            || expired != -1 && expired < epoch_seconds()
            || !unlimited && remaining < delta)
    {
        return Err(OpenAiRelayFailure::new(
            StatusCode::FORBIDDEN,
            "pre_consume_token_quota_failed",
            "token quota is not enough",
        ));
    }
    let remaining = remaining.checked_sub(delta).ok_or_else(internal_failure)?;
    let used = used.checked_add(delta).ok_or_else(internal_failure)?;
    sqlx::query("UPDATE tokens SET remain_quota=$3,used_quota=$4,accessed_time=$5 WHERE id=$1 AND user_id=$2")
        .bind(token).bind(user).bind(remaining).bind(used).bind(epoch_seconds()).execute(&mut **tx).await.map_err(|_|internal_failure())?;
    Ok(())
}

fn select_subscription(
    subscriptions: &[Subscription],
    budget: i64,
    allow_partial: bool,
) -> Option<(Subscription, i64)> {
    let partial_allowed =
        allow_partial && subscriptions.iter().all(|sub| sub.allow_wallet_overflow);
    let mut partial = None;
    let mut largest = 0;
    for sub in subscriptions {
        if !sub.has_finite_quota() || sub.amount_total.saturating_sub(sub.amount_used) >= budget {
            return Some((sub.clone(), budget));
        }
        let remaining = sub.amount_total.saturating_sub(sub.amount_used).max(0);
        if partial_allowed && remaining > largest {
            partial = Some(sub.clone());
            largest = remaining;
        }
    }
    partial.map(|sub| (sub, largest))
}

pub(super) async fn reserve(
    tx: &mut Transaction<'_, Postgres>,
    request: Request<'_>,
) -> Result<Record, OpenAiRelayFailure> {
    let (_, setting) = user_lock(tx, request.user_id).await?;
    if let Some(id) = request.existing_id {
        let mut record = load_locked(tx, id).await?;
        if record.user_id != request.user_id
            || record.token_id != request.token_id
            || record.request_id != request.request_id
            || record.status != "reserved"
        {
            return Err(state_error());
        }
        if record.funding_source == "free" && !request.free {
            // The first paid retry must choose the user's actual funding
            // preference, just as Go creates BillingSession on this retry.
            sqlx::query("UPDATE relay_settlement_records SET status='refunded',actual_quota=0,usage_snapshot='{\"refund\":true}'::JSONB,updated_at=$2 WHERE reservation_id=$1")
                .bind(id).bind(epoch_seconds()).execute(&mut **tx).await.map_err(|_|internal_failure())?;
        } else {
            grow(tx, &mut record, request.expected_quota).await?;
            record.channel_id = request.channel_id;
            record.price_snapshot = request.price_snapshot;
            sqlx::query("UPDATE relay_settlement_records SET channel_id=$2,price_snapshot=$3,expected_quota=$4,wallet_reserved=$5,subscription_reserved=$6,token_reserved=$7,updated_at=$8 WHERE reservation_id=$1")
            .bind(id).bind(record.channel_id).bind(&record.price_snapshot).bind(record.expected_quota).bind(record.wallet_reserved).bind(record.subscription_reserved).bind(record.token_reserved).bind(epoch_seconds())
            .execute(&mut **tx).await.map_err(|_|internal_failure())?;
            return Ok(record);
        }
    }
    let existing:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM relay_settlement_records WHERE user_id=$1 AND request_id=$2 AND status<>'refunded')")
        .bind(request.user_id).bind(request.request_id).fetch_one(&mut **tx).await.map_err(|_|internal_failure())?;
    if existing {
        return Err(state_error());
    }
    let mut record = Record {
        reservation_id: uuid::Uuid::new_v4().to_string(),
        request_id: request.request_id.to_owned(),
        user_id: request.user_id,
        token_id: request.token_id,
        channel_id: request.channel_id,
        model_name: request.model_name.to_owned(),
        using_group: request.using_group.to_owned(),
        is_stream: request.is_stream,
        funding_source: "wallet".to_owned(),
        expected_quota: request.expected_quota,
        wallet_reserved: 0,
        subscription_id: 0,
        subscription_reserved: 0,
        reserved_version: 0,
        token_reserved: 0,
        wallet_overflow: false,
        wallet_settled: 0,
        subscription_settled: 0,
        actual_quota: None,
        price_snapshot: request.price_snapshot,
        usage_snapshot: None,
        log_metadata: json!({}),
        status: "reserved".to_owned(),
    };
    if request.free {
        record.funding_source = "free".to_owned();
    } else {
        let budget = request.expected_quota.max(1);
        record.expected_quota = budget;
        let pref = preference(&setting);
        let prefer_wallet = matches!(pref, "wallet_only" | "wallet_first");
        let wallet_available = if prefer_wallet {
            authorize_wallet(tx, request.user_id, "", budget).await?
        } else {
            false
        };
        if wallet_available {
            record.wallet_reserved = budget;
        } else if pref == "wallet_only" {
            return Err(denied("用户额度不足"));
        } else {
            let subscriptions = active_subscriptions(tx, request.user_id, true).await?;
            if let Some((subscription, reserved)) =
                select_subscription(&subscriptions, budget, pref == "subscription_first")
            {
                let wallet = budget - reserved;
                if wallet > 0
                    && !authorize_wallet(tx, request.user_id, request.request_id, wallet).await?
                {
                    return Err(denied(
                        "订阅与钱包余额不足以预留本次预计费用: wallet quota insufficient for subscription overflow",
                    ));
                }
                record.funding_source = "subscription".to_owned();
                record.subscription_id = subscription.id;
                record.subscription_reserved = reserved;
                record.reserved_version = subscription.quota_version;
                record.wallet_overflow = pref == "subscription_first";
                sqlx::query("UPDATE user_subscriptions SET amount_used=amount_used+$2,updated_at=$3 WHERE id=$1")
                    .bind(subscription.id).bind(reserved).bind(epoch_seconds()).execute(&mut **tx).await.map_err(|_|internal_failure())?;
                sqlx::query("INSERT INTO subscription_pre_consume_records(request_id,user_id,user_subscription_id,pre_consumed,billing_managed,token_id,token_consumed,wallet_overflow,reserved_version,status,created_at,updated_at) VALUES($1,$2,$3,$4,TRUE,$5,$6,$7,$8,'consumed',$9,$9)")
                    .bind(request.request_id).bind(request.user_id).bind(subscription.id).bind(reserved).bind(request.token_id).bind(budget).bind(record.wallet_overflow).bind(subscription.quota_version).bind(epoch_seconds())
                    .execute(&mut **tx).await.map_err(|_|state_error())?;
            } else if pref == "subscription_first"
                && subscriptions.iter().all(|sub| sub.allow_wallet_overflow)
                && authorize_wallet(tx, request.user_id, "", budget).await?
            {
                record.wallet_reserved = budget;
            } else {
                return Err(denied(if subscriptions.is_empty() {
                    "订阅额度不足或未配置订阅: no active subscription"
                } else {
                    "订阅额度不足或未配置订阅: subscription quota insufficient"
                }));
            }
        }
        wallet_delta(tx, record.user_id, -record.wallet_reserved).await?;
        token_delta(tx, record.user_id, record.token_id, budget, true).await?;
        record.token_reserved = budget;
    }
    sqlx::query("INSERT INTO relay_settlement_records(reservation_id,request_id,user_id,token_id,channel_id,model_name,using_group,is_stream,funding_source,expected_quota,wallet_reserved,subscription_id,subscription_reserved,reserved_version,token_reserved,wallet_overflow,price_snapshot,status,created_at,updated_at) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,'reserved',$18,$18)")
        .bind(&record.reservation_id).bind(&record.request_id).bind(record.user_id).bind(record.token_id).bind(record.channel_id).bind(&record.model_name).bind(&record.using_group).bind(record.is_stream).bind(&record.funding_source)
        .bind(record.expected_quota).bind(record.wallet_reserved).bind(record.subscription_id).bind(record.subscription_reserved).bind(record.reserved_version).bind(record.token_reserved).bind(record.wallet_overflow).bind(&record.price_snapshot).bind(epoch_seconds())
        .execute(&mut **tx).await.map_err(|_|state_error())?;
    Ok(record)
}

async fn load_locked(
    tx: &mut Transaction<'_, Postgres>,
    id: &str,
) -> Result<Record, OpenAiRelayFailure> {
    sqlx::query_as("SELECT * FROM relay_settlement_records WHERE reservation_id=$1 FOR UPDATE")
        .bind(id)
        .fetch_optional(&mut **tx)
        .await
        .map_err(|_| internal_failure())?
        .ok_or_else(state_error)
}

async fn selected_subscription(
    tx: &mut Transaction<'_, Postgres>,
    record: &Record,
) -> Result<Subscription, OpenAiRelayFailure> {
    sqlx::query_as("SELECT id,amount_total,reset_amount,renewal_amount,amount_used,quota_version,allow_wallet_overflow FROM user_subscriptions WHERE id=$1 AND user_id=$2 FOR UPDATE")
        .bind(record.subscription_id).bind(record.user_id).fetch_optional(&mut **tx).await.map_err(|_|internal_failure())?.ok_or_else(state_error)
}

async fn grow(
    tx: &mut Transaction<'_, Postgres>,
    record: &mut Record,
    target: i64,
) -> Result<(), OpenAiRelayFailure> {
    if target <= record.expected_quota {
        return Ok(());
    }
    let delta = target - record.token_reserved;
    if record.funding_source == "subscription" {
        let active = active_subscriptions(tx, record.user_id, false).await?;
        let sub = selected_subscription(tx, record).await?;
        if sub.quota_version != record.reserved_version {
            return Err(state_error());
        }
        let mut extra = target - record.subscription_reserved;
        if sub.has_finite_quota() {
            let remaining = sub.amount_total.saturating_sub(sub.amount_used).max(0);
            if extra > remaining {
                if !(record.wallet_overflow
                    && sub.allow_wallet_overflow
                    && active.iter().all(|sub| sub.allow_wallet_overflow))
                {
                    return Err(denied("subscription quota insufficient"));
                }
                extra = remaining;
            }
        }
        let wallet = target - record.subscription_reserved - extra;
        if wallet > 0 && !authorize_wallet(tx, record.user_id, &record.request_id, wallet).await? {
            return Err(denied(
                "wallet quota insufficient for subscription overflow",
            ));
        }
        sqlx::query(
            "UPDATE user_subscriptions SET amount_used=amount_used+$2,updated_at=$3 WHERE id=$1",
        )
        .bind(record.subscription_id)
        .bind(extra)
        .bind(epoch_seconds())
        .execute(&mut **tx)
        .await
        .map_err(|_| internal_failure())?;
        record.subscription_reserved += extra;
        sqlx::query("UPDATE subscription_pre_consume_records SET pre_consumed=$3,token_consumed=$4,updated_at=$5 WHERE request_id=$1 AND user_id=$2 AND status='consumed' AND billing_managed=TRUE")
            .bind(&record.request_id).bind(record.user_id).bind(record.subscription_reserved).bind(target).bind(epoch_seconds()).execute(&mut **tx).await.map_err(|_|internal_failure())?;
    } else {
        // Go's in-flight wallet Reserve applies the whole delta; an actual
        // overage is debt, never a silently capped payment.
        wallet_delta(tx, record.user_id, -delta).await?;
        record.wallet_reserved += delta;
        record.funding_source = "wallet".to_owned();
    }
    token_delta(tx, record.user_id, record.token_id, delta, true).await?;
    record.token_reserved = target;
    record.expected_quota = target;
    Ok(())
}

/// Persist final usage intent before changing money. A database failure after
/// this commit can be reconciled by ID without regenerating model output.
pub(super) async fn intent(
    pg: &PgPool,
    id: &str,
    user: i64,
    actual: i64,
    usage: &Value,
    metadata: &Value,
    refund: bool,
) -> Result<(), OpenAiRelayFailure> {
    if actual < 0 || refund && actual != 0 {
        return Err(state_error());
    }
    let mut tx = pg.begin().await.map_err(|_| internal_failure())?;
    user_lock(&mut tx, user).await?;
    let record = load_locked(&mut tx, id).await?;
    if record.user_id != user {
        return Err(state_error());
    }
    let mut usage = usage.clone();
    usage["refund"] = json!(refund);
    if record.status != "reserved" {
        if record.actual_quota != Some(actual) || record.usage_snapshot.as_ref() != Some(&usage) {
            return Err(state_error());
        }
        return Ok(());
    }
    if record.funding_source == "subscription" && !refund {
        let result=sqlx::query("UPDATE subscription_pre_consume_records SET actual_quota=$3,status='settling',updated_at=$4 WHERE request_id=$1 AND user_id=$2 AND billing_managed=TRUE AND status='consumed'")
            .bind(&record.request_id).bind(user).bind(actual).bind(epoch_seconds()).execute(&mut *tx).await.map_err(|_|internal_failure())?;
        if result.rows_affected() != 1 {
            return Err(state_error());
        }
    }
    sqlx::query("UPDATE relay_settlement_records SET actual_quota=$2,usage_snapshot=$3,log_metadata=$4,status='settling',updated_at=$5 WHERE reservation_id=$1")
        .bind(id).bind(actual).bind(usage).bind(metadata).bind(epoch_seconds()).execute(&mut *tx).await.map_err(|_|internal_failure())?;
    tx.commit().await.map_err(|_| internal_failure())
}

/// The caller records counters/logs in this same transaction before commit.
/// None means another finalizer already committed this exact reservation.
pub(super) async fn finish(
    tx: &mut Transaction<'_, Postgres>,
    id: &str,
    user: i64,
) -> Result<Option<Record>, OpenAiRelayFailure> {
    user_lock(tx, user).await?;
    let mut record = load_locked(tx, id).await?;
    if record.user_id != user {
        return Err(state_error());
    }
    if matches!(record.status.as_str(), "settled" | "refunded") {
        return Ok(None);
    }
    if record.status != "settling" {
        return Err(state_error());
    }
    let actual = record.actual_quota.ok_or_else(state_error)?;
    let refund = record
        .usage_snapshot
        .as_ref()
        .and_then(|value| value.get("refund"))
        .and_then(Value::as_bool)
        .unwrap_or(false);
    if record.funding_source == "subscription" {
        let active = active_subscriptions(tx, user, false).await?;
        let sub = selected_subscription(tx, &record).await?;
        let delta = actual - record.subscription_reserved;
        let mut sub_delta = delta;
        let mut wallet = 0;
        if delta > 0 && sub.has_finite_quota() {
            let remaining = sub.amount_total.saturating_sub(sub.amount_used).max(0);
            if delta > remaining {
                if !(record.wallet_overflow
                    && sub.allow_wallet_overflow
                    && active.iter().all(|sub| sub.allow_wallet_overflow))
                {
                    return Err(denied("subscription quota insufficient"));
                }
                sub_delta = remaining;
                wallet = delta - remaining;
            }
        }
        if sub_delta < 0 && sub.quota_version != record.reserved_version {
            sub_delta = 0;
        }
        let used = sub
            .amount_used
            .checked_add(sub_delta)
            .ok_or_else(internal_failure)?
            .max(0);
        sqlx::query("UPDATE user_subscriptions SET amount_used=$2,updated_at=$3 WHERE id=$1")
            .bind(sub.id)
            .bind(used)
            .bind(epoch_seconds())
            .execute(&mut **tx)
            .await
            .map_err(|_| internal_failure())?;
        wallet_delta(tx, user, -wallet).await?;
        record.wallet_settled = wallet;
        record.subscription_settled = actual - wallet;
        let result=sqlx::query("UPDATE subscription_pre_consume_records SET status=$3,actual_quota=$4,wallet_consumed=$5,token_consumed=$6,updated_at=$7 WHERE request_id=$1 AND user_id=$2 AND billing_managed=TRUE AND status=$8")
            .bind(&record.request_id).bind(user).bind(if refund {"refunded"} else {"settled"}).bind(actual).bind(wallet).bind(actual).bind(epoch_seconds()).bind(if refund {"consumed"} else {"settling"})
            .execute(&mut **tx).await.map_err(|_|internal_failure())?;
        if result.rows_affected() != 1 {
            return Err(state_error());
        }
    } else {
        wallet_delta(tx, user, record.wallet_reserved - actual).await?;
        record.wallet_settled = actual;
    }
    token_delta(
        tx,
        user,
        record.token_id,
        actual - record.token_reserved,
        false,
    )
    .await?;
    record.status = if refund { "refunded" } else { "settled" }.to_owned();
    sqlx::query("UPDATE relay_settlement_records SET status=$2,wallet_settled=$3,subscription_settled=$4,updated_at=$5 WHERE reservation_id=$1")
        .bind(id).bind(&record.status).bind(record.wallet_settled).bind(record.subscription_settled).bind(epoch_seconds()).execute(&mut **tx).await.map_err(|_|internal_failure())?;
    Ok(Some(record))
}

pub(super) async fn pending(
    pg: &PgPool,
    limit: i64,
) -> Result<Vec<(String, i64)>, OpenAiRelayFailure> {
    sqlx::query_as("SELECT reservation_id,user_id FROM relay_settlement_records WHERE status='settling' ORDER BY updated_at,reservation_id LIMIT $1")
        .bind(limit.clamp(1,1000)).fetch_all(pg).await.map_err(|_|internal_failure())
}

#[cfg(test)]
#[path = "funding/go_oracle_tests.rs"]
mod go_oracle_tests;

#[cfg(test)]
mod tests {
    use super::*;

    fn sub(id: i64, remaining: i64, overflow: bool) -> Subscription {
        Subscription {
            id,
            amount_total: 100,
            reset_amount: None,
            renewal_amount: None,
            amount_used: 100 - remaining,
            quota_version: 0,
            allow_wallet_overflow: overflow,
        }
    }
    #[test]
    fn subscription_selection_uses_first_full_grant_then_largest_partial_and_never_splits_grants() {
        let subscriptions = vec![sub(1, 5, true), sub(2, 20, true)];
        assert_eq!(
            select_subscription(&subscriptions, 10, true).unwrap().0.id,
            2
        );
        assert_eq!(select_subscription(&subscriptions, 30, true).unwrap().1, 20);
        assert!(select_subscription(&subscriptions, 30, false).is_none());
        assert!(select_subscription(&[sub(1, 5, false), sub(2, 20, true)], 30, true).is_none());
    }
    #[test]
    fn migrated_zero_grants_are_finite_but_null_legacy_zero_remains_unlimited() {
        let mut zero = sub(1, 0, true);
        zero.amount_total = 0;
        zero.amount_used = 0;
        assert_eq!(
            select_subscription(&[zero.clone()], 10, false).unwrap().1,
            10
        );
        zero.reset_amount = Some(0);
        assert!(select_subscription(&[zero.clone()], 10, true).is_none());
        zero.reset_amount = None;
        zero.renewal_amount = Some(0);
        assert!(select_subscription(&[zero], 10, true).is_none());
    }
    #[test]
    fn malformed_or_unknown_billing_preferences_default_to_subscription_first() {
        assert_eq!(preference("garbage"), "subscription_first");
        assert_eq!(
            preference(r#"{"billing_preference":" wallet_only "}"#),
            "wallet_only"
        );
        assert_eq!(
            preference(r#"{"billing_preference":"none"}"#),
            "subscription_first"
        );
    }
}
