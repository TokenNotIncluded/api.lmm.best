use super::{authority::Payer, types::*};
use sqlx::{PgConnection, Row};

/// All writers use SERIALIZABLE isolation. Predicate reads include historical
/// requests, opening facts, and commerce spending, across budget revisions.
struct BudgetSubject<'a> {
    request_id: Option<&'a str>,
    external_id: Option<&'a str>,
    actor: i64,
    key: i64,
    payer: i64,
    authorized_at: i64,
    total: i64,
}
struct BudgetSnapshot {
    id: i64,
    revision: i64,
    start: i64,
    end: i64,
    limit: i64,
    unlimited: bool,
}
async fn check_budgets(
    c: &mut PgConnection,
    subject: &BudgetSubject<'_>,
) -> Result<Vec<BudgetSnapshot>> {
    let rules = sqlx::query(
        "SELECT * FROM core_charging.budgets WHERE \
         (scope='account' AND account_id=$1) OR \
         (scope='member' AND account_id=$1 AND user_id=$2) OR \
         (scope='self' AND user_id=$2) OR (scope='key' AND key_id=$3) \
         ORDER BY id FOR SHARE",
    )
    .bind(subject.payer)
    .bind(subject.actor)
    .bind(subject.key)
    .fetch_all(&mut *c)
    .await?;
    let mut snapshots = Vec::with_capacity(rules.len());
    for rule in rules {
        let period: String = rule.try_get("period")?;
        let anchor: i64 = rule.try_get("anchor")?;
        let seconds: i64 = rule.try_get("seconds")?;
        let scope: String = rule.try_get("scope")?;
        let window = sqlx::query("SELECT * FROM core_charging.period_window($1,$2,$3,$4)")
            .bind(&period)
            .bind(anchor)
            .bind(seconds)
            .bind(subject.authorized_at)
            .fetch_one(&mut *c)
            .await?;
        let start: i64 = window.try_get("start_at")?;
        let end: i64 = window.try_get("end_at")?;
        let limit: i64 = rule.try_get("limit_credits")?;
        let unlimited: bool = rule.try_get("unlimited")?;
        let opening: i64 = sqlx::query_scalar(
            "SELECT coalesce((SELECT consumed_credits FROM core_charging.budget_opening_consumption \
             WHERE scope=$1 AND account_id IS NOT DISTINCT FROM $2 \
             AND user_id IS NOT DISTINCT FROM $3 AND key_id IS NOT DISTINCT FROM $4 \
             AND period=$5 AND anchor=$6 AND seconds=$7 AND start_at=$8 AND end_at=$9),0)",
        )
        .bind(&scope)
        .bind(rule.try_get::<Option<i64>, _>("account_id")?)
        .bind(rule.try_get::<Option<i64>, _>("user_id")?)
        .bind(rule.try_get::<Option<i64>, _>("key_id")?)
        .bind(&period)
        .bind(anchor)
        .bind(seconds)
        .bind(start)
        .bind(end)
        .fetch_one(&mut *c)
        .await?;
        // SUM(bigint) is NUMERIC. Add independently aggregated domains and the
        // single opening fact without joining rows (which would multiply usage).
        let fits: bool = sqlx::query_scalar(
            "SELECT $12::boolean OR ((SELECT coalesce(sum(core_charging.committed_credits( \
             state,reserved,settled,refunded)),0) FROM core_charging.requests \
             WHERE ($1::text IS NULL OR id<>$1) AND authorized_at >= $2 AND authorized_at < $3 AND \
             (($4='account' AND payer_account_id=$5) OR \
              ($4='member' AND payer_account_id=$5 AND actor_user_id=$6) OR \
              ($4='self' AND actor_user_id=$6) OR ($4='key' AND key_id=$7))) \
             + (SELECT coalesce(sum(amount_credits-refunded),0) FROM core_charging.external_consumption \
             WHERE ($10::text IS NULL OR id<>$10) AND authorized_at >= $2 AND authorized_at < $3 AND \
             (($4='account' AND payer_account_id=$5) OR \
              ($4='member' AND payer_account_id=$5 AND actor_user_id=$6) OR \
              ($4='self' AND actor_user_id=$6) OR ($4='key' AND key_id=$7))) \
             + $11::bigint + $8::bigint <= $9::bigint)",
        )
        .bind(subject.request_id)
        .bind(start)
        .bind(end)
        .bind(&scope)
        .bind(subject.payer)
        .bind(subject.actor)
        .bind(subject.key)
        .bind(subject.total)
        .bind(limit)
        .bind(subject.external_id)
        .bind(opening)
        .bind(unlimited)
        .fetch_one(&mut *c)
        .await?;
        if !fits {
            return Err(Error::BudgetExceeded);
        }
        snapshots.push(BudgetSnapshot {
            id: rule.try_get("id")?,
            revision: rule.try_get("revision")?,
            start,
            end,
            limit,
            unlimited,
        });
    }
    Ok(snapshots)
}

pub(super) async fn budgets(c: &mut PgConnection, charge: &Charge, total: i64) -> Result<()> {
    let snapshots = check_budgets(
        c,
        &BudgetSubject {
            request_id: Some(&charge.id),
            external_id: None,
            actor: charge.actor_user_id,
            key: charge.key_id,
            payer: charge.payer_account_id,
            authorized_at: charge.authorized_at,
            total,
        },
    )
    .await?;
    for snapshot in snapshots {
        sqlx::query(
            "INSERT INTO core_charging.budget_checks(request_id,request_revision,budget_id, \
             budget_revision,start_at,end_at,limit_credits,unlimited,requested_credits) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)",
        )
        .bind(&charge.id)
        .bind(charge.revision)
        .bind(snapshot.id)
        .bind(snapshot.revision)
        .bind(snapshot.start)
        .bind(snapshot.end)
        .bind(snapshot.limit)
        .bind(snapshot.unlimited)
        .bind(total)
        .execute(&mut *c)
        .await?;
    }
    Ok(())
}

/// Validate a just-inserted commerce row in its caller's SERIALIZABLE
/// transaction. Failure must roll back that transaction and its wallet posting.
/// The stored actor/key/payer are authoritative; callers cannot supply substitutes.
pub async fn external_budgets(c: &mut PgConnection, external_id: &str) -> Result<()> {
    let serializable: bool =
        sqlx::query_scalar("SELECT current_setting('transaction_isolation')='serializable'")
            .fetch_one(&mut *c)
            .await?;
    if !serializable {
        return Err(Error::Invalid);
    }
    let row =
        sqlx::query("SELECT * FROM core_charging.external_consumption WHERE id=$1 FOR UPDATE")
            .bind(external_id)
            .fetch_optional(&mut *c)
            .await?
            .ok_or(Error::NotFound)?;
    check_budgets(
        c,
        &BudgetSubject {
            request_id: None,
            external_id: Some(external_id),
            actor: row.try_get("actor_user_id")?,
            key: row.try_get("key_id")?,
            payer: row.try_get("payer_account_id")?,
            authorized_at: row.try_get("authorized_at")?,
            total: row.try_get::<i64, _>("amount_credits")? - row.try_get::<i64, _>("refunded")?,
        },
    )
    .await?;
    Ok(())
}

async fn key_consumed(c: &mut PgConnection, key: i64) -> Result<i64> {
    Ok(sqlx::query_scalar(
        "SELECT ((SELECT coalesce(sum(core_charging.committed_credits(state,reserved,settled,refunded)),0) \
         FROM core_charging.requests WHERE key_id=$1) \
         + (SELECT coalesce(sum(amount_credits-refunded),0) FROM core_charging.external_consumption WHERE key_id=$1) \
         + (SELECT coalesce(sum(consumed_credits),0) FROM core_charging.budget_opening_consumption \
            WHERE scope='key' AND key_id=$1 AND period='lifetime'))::bigint",
    ).bind(key).fetch_one(c).await?)
}

fn signed_remaining(basis: Option<i64>, used: i64) -> Result<i64> {
    match basis {
        Some(basis) => basis.checked_sub(used).ok_or(Error::Invalid),
        None => Ok(0),
    }
}

/// Effective lifetime summary across all issuers. The strictest display basis
/// is retained even for unlimited policies; admission remains unlimited only
/// when every lifetime policy is unlimited. Periodic caps remain additional.
pub async fn key_quota_snapshot(c: &mut PgConnection, key: i64) -> Result<(i64, i64, bool)> {
    let row = sqlx::query(
        "SELECT min(coalesce(display_limit_credits,limit_credits)) AS display_basis, \
         coalesce(bool_and(unlimited),true) AS unlimited FROM core_charging.budgets \
         WHERE scope='key' AND key_id=$1 AND period='lifetime'",
    )
    .bind(key)
    .fetch_one(&mut *c)
    .await?;
    let used = key_consumed(c, key).await?;
    Ok((
        used,
        signed_remaining(row.try_get("display_basis")?, used)?,
        row.try_get("unlimited")?,
    ))
}

/// An editor must display its issuer's own policy, not another issuer's cap.
/// No matching issuer policy defaults to unlimited with displayed remaining 0.
/// Consumption is still shared and counted once across all policies.
pub async fn key_quota_snapshot_for_issuer(
    c: &mut PgConnection,
    key: i64,
    issuer: i64,
) -> Result<(i64, i64, bool)> {
    let row = sqlx::query(
        "SELECT coalesce(display_limit_credits,limit_credits) AS display_basis,unlimited \
         FROM core_charging.budgets WHERE scope='key' AND key_id=$1 \
         AND issuer_user_id=$2 AND period='lifetime'",
    )
    .bind(key)
    .bind(issuer)
    .fetch_optional(&mut *c)
    .await?;
    let used = key_consumed(c, key).await?;
    let Some(row) = row else {
        return Ok((used, 0, true));
    };
    Ok((
        used,
        signed_remaining(Some(row.try_get("display_basis")?), used)?,
        row.try_get("unlimited")?,
    ))
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
             core_charging.subscription_window(s.id,$2) w \
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
    let grant = sqlx::query(
        "SELECT w.limit_credits,w.unlimited FROM core_charging.subscriptions s CROSS JOIN LATERAL \
         core_charging.subscription_window(s.id,$3) w \
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
    let grant = grant.ok_or(Error::NoFunds)?;
    let limit: i64 = grant.try_get("limit_credits")?;
    let unlimited: bool = grant.try_get("unlimited")?;
    let fits: bool = sqlx::query_scalar(
        "SELECT $6::boolean OR ((SELECT coalesce(sum(core_charging.committed_credits(state,reserved,settled,refunded)),0) \
         FROM core_charging.requests WHERE source_subscription_id=$1 AND source_start=$2 AND id<>$3) \
         + coalesce((SELECT consumed_credits FROM core_charging.subscription_opening_consumption \
                     WHERE subscription_id=$1 AND start_at=$2),0) + $4::bigint <= $5::bigint)",
    )
    .bind(id)
    .bind(start)
    .bind(&charge.id)
    .bind(total)
    .bind(limit)
    .bind(unlimited)
    .fetch_one(c)
    .await?;
    if fits { Ok(()) } else { Err(Error::NoFunds) }
}
