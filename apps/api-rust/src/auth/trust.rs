//! Shared current-Go paid-credit facts for dashboard trust and token pricing.

use sqlx::{PgConnection, Row};
use std::collections::BTreeMap;

#[derive(Clone, Copy, Debug, Default)]
pub(crate) struct PaymentSnapshot {
    pub(crate) paid_amount: f64,
    pub(crate) last_paid_complete_at: i64,
    pub(crate) paid_activation_complete: bool,
    pub(crate) console_activated: bool,
}

pub(crate) async fn payment_options(
    connection: &mut PgConnection,
) -> Result<BTreeMap<String, String>, sqlx::Error> {
    sqlx::query_as::<_, (String, String)>(
        "SELECT key,COALESCE(value,'') FROM options WHERE key = ANY($1)",
    )
    .bind(
        [
            "QuotaPerUnit",
            "developer_access_setting.paid_activation_enabled",
            "developer_access_setting.paid_activation_min_amount",
        ]
        .as_slice(),
    )
    .fetch_all(connection)
    .await
    .map(|rows| rows.into_iter().collect())
}

pub(crate) async fn payment_snapshot(
    connection: &mut PgConnection,
    user_id: i64,
    options: &BTreeMap<String, String>,
) -> Result<PaymentSnapshot, sqlx::Error> {
    let unit = options
        .get("QuotaPerUnit")
        .map_or(500000.0, |value| value.parse::<f64>().unwrap_or(0.0));
    let unit = if unit.is_finite() && unit > 0.0 {
        unit
    } else {
        0.0
    };
    // Match positiveNormalizedCreditedQuotaSQL + successfulExternalPaidTopUpQuery:
    // authoritative credited quota, legacy Creem quota, legacy other-provider
    // display units, and no LinuxDO/internal ePay credit progression.
    let aggregate=sqlx::query(r#"WITH raw AS (
        SELECT to_jsonb(top_ups) AS v FROM top_ups WHERE user_id=$1 AND status='success'
    ), parsed AS (
        SELECT COALESCE(v->>'payment_provider','') AS provider,COALESCE(v->>'payment_method','') AS method,
            COALESCE((v->>'settled_amount_micros')::BIGINT,0) AS settled,
            COALESCE((v->>'expected_amount_micros')::BIGINT,0) AS expected,
            COALESCE(v->>'settlement_currency','') AS currency,
            COALESCE((v->>'money')::DOUBLE PRECISION,0) AS money,
            COALESCE((v->>'credited_quota')::BIGINT,0) AS credited,
            COALESCE((v->>'amount')::BIGINT,0) AS amount,
            CASE WHEN COALESCE((v->>'complete_time')::BIGINT,0)>0 THEN (v->>'complete_time')::BIGINT ELSE COALESCE((v->>'create_time')::BIGINT,0) END AS active_at
        FROM raw
    ), eligible AS (
        SELECT *,CASE WHEN credited>0 THEN credited::DOUBLE PRECISION
            WHEN provider='creem' OR method='creem' THEN amount::DOUBLE PRECISION
            ELSE amount::DOUBLE PRECISION*$2::DOUBLE PRECISION END AS quota
        FROM parsed WHERE provider<>'balance' AND method<>'balance'
          AND (settled>0 OR (settled=0 AND money>0))
          AND (provider IN ('epay','stripe','creem','waffo','waffo_pancake') OR
               (provider='' AND method IN ('stripe','creem','waffo','waffo_pancake','alipay','wxpay')))
          AND NOT (LOWER(provider)='epay' AND LOWER(method) IN ('ldc','linuxdo','linux_do','linuxdo_credit'))
          AND NOT (LOWER(provider)='epay' AND LOWER(method)='epay' AND (UPPER(currency)<>'CNY' OR (expected<=0 AND settled<=0)))
    ) SELECT COALESCE(SUM(quota),0)::DOUBLE PRECISION AS credited,COUNT(*)::BIGINT AS paid_rows,
        COALESCE(MAX(active_at),0)::BIGINT AS active_at FROM eligible WHERE quota>0"#)
        .bind(user_id).bind(unit).fetch_one(&mut *connection).await?;
    let credited: f64 = aggregate.try_get("credited")?;
    let paid_rows: i64 = aggregate.try_get("paid_rows")?;
    let paid = if unit <= 0.0 || credited <= 0.0 || !credited.is_finite() {
        0.0
    } else {
        sqlx::query_scalar::<_,f64>("SELECT ROUND(ROUND($1::TEXT::NUMERIC/$2::TEXT::NUMERIC(1000,500),16)*1000000,0)::DOUBLE PRECISION/1000000")
            .bind(credited.to_string()).bind(unit.to_string()).fetch_one(&mut *connection).await?
    };
    let enabled = options
        .get("developer_access_setting.paid_activation_enabled")
        .is_none_or(|value| value == "true");
    let minimum = options
        .get("developer_access_setting.paid_activation_min_amount")
        .and_then(|value| value.parse::<f64>().ok())
        .unwrap_or(1.0);
    let minimum_micros = if minimum > 0.0 && minimum.is_finite() {
        (minimum * 1_000_000.0).round()
    } else {
        0.0
    };
    let console_activated=sqlx::query_scalar::<_,bool>("SELECT COALESCE((to_jsonb(users)->>'console_activated_at')::BIGINT,0)>0 FROM users WHERE id=$1 AND deleted_at IS NULL")
        .bind(user_id).fetch_optional(&mut *connection).await?.unwrap_or(false);
    Ok(PaymentSnapshot {
        paid_amount: paid,
        last_paid_complete_at: aggregate.try_get("active_at")?,
        paid_activation_complete: enabled
            && paid_rows > 0
            && (minimum_micros <= 0.0 || paid * 1_000_000.0 >= minimum_micros),
        console_activated,
    })
}
