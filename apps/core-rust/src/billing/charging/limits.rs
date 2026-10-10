use super::{authority::Payer, types::*};
use sqlx::{PgConnection, Row};

/// All writers use SERIALIZABLE isolation. Predicate reads include historical
/// requests, including requests created before a budget was configured. SSI
/// aborts conflicting consumption rather than permitting aggregate overspend.
pub(super) async fn budgets(c: &mut PgConnection, charge: &Charge, total: i64) -> Result<()> {
    let rules = sqlx::query(
        "SELECT * FROM core_charging.budgets WHERE \
         (scope='account' AND account_id=$1) OR \
         (scope='member' AND account_id=$1 AND user_id=$2) OR \
         (scope='self' AND user_id=$2) OR (scope='key' AND key_id=$3) \
         ORDER BY id FOR SHARE",
    )
    .bind(charge.payer_account_id)
    .bind(charge.actor_user_id)
    .bind(charge.key_id)
    .fetch_all(&mut *c)
    .await?;
    for rule in rules {
        let window = sqlx::query("SELECT * FROM core_charging.period_window($1,$2,$3,$4)")
            .bind(rule.try_get::<String, _>("period")?)
            .bind(rule.try_get::<i64, _>("anchor")?)
            .bind(rule.try_get::<i64, _>("seconds")?)
            .bind(charge.authorized_at)
            .fetch_one(&mut *c)
            .await?;
        let start: i64 = window.try_get("start_at")?;
        let end: i64 = window.try_get("end_at")?;
        let limit: i64 = rule.try_get("limit_credits")?;
        // SUM(bigint) is NUMERIC. Never truncate aggregate usage to i64.
        let fits: bool = sqlx::query_scalar(
            "SELECT coalesce(sum(core_charging.committed_credits(state,reserved,settled,refunded)),0) \
             + $8::bigint <= $9::bigint FROM core_charging.requests WHERE id<>$1 \
             AND authorized_at >= $2 AND authorized_at < $3 AND \
             (($4='account' AND payer_account_id=$5) OR \
              ($4='member' AND payer_account_id=$5 AND actor_user_id=$6) OR \
              ($4='self' AND actor_user_id=$6) OR ($4='key' AND key_id=$7))",
        )
        .bind(&charge.id)
        .bind(start)
        .bind(end)
        .bind(rule.try_get::<String, _>("scope")?)
        .bind(charge.payer_account_id)
        .bind(charge.actor_user_id)
        .bind(charge.key_id)
        .bind(total)
        .bind(limit)
        .fetch_one(&mut *c)
        .await?;
        if !fits {
            return Err(Error::BudgetExceeded);
        }
        sqlx::query(
            "INSERT INTO core_charging.budget_checks(request_id,request_revision,budget_id, \
             budget_revision,start_at,end_at,limit_credits,requested_credits) VALUES ($1,$2,$3,$4,$5,$6,$7,$8)",
        )
        .bind(&charge.id)
        .bind(charge.revision)
        .bind(rule.try_get::<i64, _>("id")?)
        .bind(rule.try_get::<i64, _>("revision")?)
        .bind(start)
        .bind(end)
        .bind(limit)
        .bind(total)
        .execute(&mut *c)
        .await?;
    }
    Ok(())
}

pub(super) async fn sources(
    c: &mut PgConnection,
    payer: &Payer,
    price: &Price,
    now: i64,
) -> Result<Vec<Source>> {
    let mut subscriptions = Vec::new();
    if payer.preference != "wallet_only" {
        let rows = sqlx::query(
            "SELECT s.id,w.start_at,least(w.end_at,s.ends_at) AS end_at \
             FROM core_charging.subscriptions s CROSS JOIN LATERAL \
             core_charging.period_window(s.period,s.anchor,s.seconds,$2) w \
             WHERE s.account_id=$1 AND s.active AND s.anchor<=$2 AND s.ends_at>$2 \
             AND ($3=ANY(s.models) OR '*'=ANY(s.models)) \
             AND ($4=ANY(s.groups) OR '*'=ANY(s.groups)) ORDER BY s.ends_at,s.id FOR SHARE OF s",
        )
        .bind(payer.id)
        .bind(now)
        .bind(&price.model)
        .bind(&price.group)
        .fetch_all(&mut *c)
        .await?;
        for row in rows {
            subscriptions.push(Source::Subscription {
                id: row.try_get("id")?,
                start: row.try_get("start_at")?,
                end: row.try_get("end_at")?,
            });
        }
    }
    match payer.preference.as_str() {
        "subscription_only" => Ok(subscriptions),
        "subscription_first" => {
            subscriptions.push(Source::Wallet);
            Ok(subscriptions)
        }
        "wallet_first" => {
            subscriptions.insert(0, Source::Wallet);
            Ok(subscriptions)
        }
        "wallet_only" => Ok(vec![Source::Wallet]),
        _ => Err(Error::Invalid),
    }
}

pub(super) async fn entitlement(
    c: &mut PgConnection,
    charge: &Charge,
    total: i64,
    now: i64,
) -> Result<()> {
    let Source::Subscription { id, start, end } = charge.source else {
        return Ok(());
    };
    // Renewal never moves an existing stream into the next quota window.
    if now >= end {
        return Err(Error::NoFunds);
    }
    let limit: Option<i64> = sqlx::query_scalar(
        "SELECT s.limit_credits FROM core_charging.subscriptions s CROSS JOIN LATERAL \
         core_charging.period_window(s.period,s.anchor,s.seconds,$3) w \
         WHERE s.id=$1 AND s.account_id=$2 AND s.active AND s.anchor<=$3 AND s.ends_at>$3 \
         AND w.start_at=$4 AND least(w.end_at,s.ends_at)=$5 \
         AND ($6=ANY(s.models) OR '*'=ANY(s.models)) AND ($7=ANY(s.groups) OR '*'=ANY(s.groups)) FOR SHARE OF s",
    )
    .bind(id)
    .bind(charge.payer_account_id)
    .bind(charge.authorized_at)
    .bind(start)
    .bind(end)
    .bind(&charge.price.model)
    .bind(&charge.price.group)
    .fetch_optional(&mut *c)
    .await?;
    let limit = limit.ok_or(Error::NoFunds)?;
    let fits: bool = sqlx::query_scalar(
        "SELECT coalesce(sum(core_charging.committed_credits(state,reserved,settled,refunded)),0) \
         + $4::bigint <= $5::bigint FROM core_charging.requests \
         WHERE source_subscription_id=$1 AND source_start=$2 AND id<>$3",
    )
    .bind(id)
    .bind(start)
    .bind(&charge.id)
    .bind(total)
    .bind(limit)
    .fetch_one(c)
    .await?;
    if fits { Ok(()) } else { Err(Error::NoFunds) }
}
