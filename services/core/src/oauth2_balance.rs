//! Purpose-limited OAuth Balance V2 projection from verified core facts.
//!
//! The caller must derive the subject from its private ReadAuthority in this
//! same transaction. This module authenticates no credentials and grants no
//! spending authority. It never initializes a wallet or changes its unit.
use crate::financial_profile::{self, ProfileError};
use serde::Serialize;
use serde_json::Number;
use sqlx::{PgConnection, Row};
use std::{fmt, str::FromStr};

const JS_SAFE_INTEGER: i128 = 9_007_199_254_740_991;
const CREDITS_PER_USD: u32 = 500_000;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum BalanceError {
    Unavailable,
    Storage,
}
impl fmt::Display for BalanceError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{self:?}")
    }
}
impl std::error::Error for BalanceError {}
impl From<sqlx::Error> for BalanceError {
    fn from(_: sqlx::Error) -> Self {
        Self::Storage
    }
}
impl From<ProfileError> for BalanceError {
    fn from(error: ProfileError) -> Self {
        match error {
            ProfileError::Unavailable => Self::Unavailable,
            ProfileError::Invalid | ProfileError::Storage => Self::Storage,
        }
    }
}

/// Constructed only after proving the installed unit and subject provenance.
/// Fields follow the actual Pi/VS Code V2 wire; optional denomination metadata
/// is omitted as a complete group, never emitted partially.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct BalanceV2 {
    schema_version: u8,
    currency: &'static str,
    balance: Number,
    quota: i64,
    public_credit_balance: String,
    credit_unit: u8,
    credits_per_usd: &'static str,
    quota_per_unit: u32,
    updated_at: u64,
    authorization_limit: Option<()>,
}

#[derive(Clone, Copy, Debug)]
enum VerifiedProvenance {
    NativeAudit,
    Opening,
}

// One final statement observes all monetary facts under READ COMMITTED. The
// preceding financial_profile::read supplies its existing historical-counter
// guard, but none of that earlier statement's amounts is returned here.
const OBSERVATION: &str = r#"
WITH person AS (
 SELECT u.id,u.personal_account_id,
   EXISTS(SELECT 1 FROM core_identity.audit a
     WHERE a.actor_user_id=u.id AND a.action IN ('user.register','user.bootstrap')
       AND a.target->>'user_id'=u.id::text) AS native_basis
 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id
 WHERE u.id=$1 AND u.personal_account_id=$2 AND u.active AND a.active AND a.kind='personal'
), unit_contract AS (
 SELECT EXISTS(
   SELECT 1 FROM pg_catalog.pg_constraint c
   JOIN pg_catalog.pg_attribute a ON a.attrelid=c.conrelid
     AND a.attname='unit' AND NOT a.attisdropped
   WHERE c.conrelid='core_billing.ledger_accounts'::pg_catalog.regclass
     AND c.conname='ledger_accounts_unit_check' AND c.contype='c'
     AND c.convalidated AND c.conenforced AND NOT c.condeferrable
     AND a.attnotnull AND a.atttypid='pg_catalog.text'::pg_catalog.regtype
     AND c.conkey=ARRAY[a.attnum]::smallint[]
     AND pg_catalog.pg_get_constraintdef(c.oid,false)=
       'CHECK ((unit = ''credit_500k_usd''::text))'
 ) AS valid
), opening AS (
 SELECT count(c.account_id)::bigint AS count,
   COALESCE(bool_and(c.user_id=p.id AND c.account_id=p.personal_account_id
     AND i.account_id=c.account_id AND i.source_row_sha256=c.source_row_sha256
     AND r.source_archive_sha256=c.source_archive_sha256
     AND r.mapping_sha256=c.mapping_sha256 AND r.observed_at=c.observed_at
     AND i.request @> jsonb_build_object('account_id',c.account_id,
       'migration_id',c.migration_id,'source_entity_key',c.source_entity_key,
       'source_row_sha256',c.source_row_sha256,'source_archive_sha256',c.source_archive_sha256,
       'mapping_sha256',c.mapping_sha256,'observed_at',c.observed_at)
     AND c.request=jsonb_build_object('account_id',c.account_id,'user_id',c.user_id,
       'migration_id',c.migration_id,'source_entity_key',c.source_entity_key,
       'source_row_sha256',c.source_row_sha256,'source_archive_sha256',c.source_archive_sha256,
       'mapping_sha256',c.mapping_sha256,'observed_at',c.observed_at,
       'used_quota',c.used_quota,'request_count',c.request_count,'aff_history',c.aff_history)
     AND i.result->>'status'='posted'
     AND EXISTS(SELECT 1 FROM core_billing.ledger_operations op
       WHERE op.scope='offline-opening' AND op.request=i.request AND op.result=i.result
         AND op.operation_key=encode(sha256(convert_to(
           jsonb_build_array(c.migration_id,c.source_entity_key)::text,'UTF8')),'hex'))
   ),FALSE) AS valid
 FROM person p LEFT JOIN core_billing.account_opening_counters c
   ON c.account_id=p.personal_account_id OR c.user_id=p.id
 LEFT JOIN core_billing.opening_imports i ON i.migration_id=c.migration_id
   AND i.source_entity_key=c.source_entity_key
 LEFT JOIN core_billing.opening_import_runs r ON r.migration_id=c.migration_id
), balances AS (
 SELECT count(l.id)::bigint AS buckets,count(b.ledger_account_id)::bigint AS balances,
   count(*) FILTER(WHERE l.bucket='wallet')::bigint AS wallets,
   count(*) FILTER(WHERE l.bucket='reserved')::bigint AS reservations,
   count(*) FILTER(WHERE l.bucket='debt')::bigint AS debts,
   count(*) FILTER(WHERE l.bucket='affiliate')::bigint AS affiliates,
   count(*) FILTER(WHERE l.unit='credit_500k_usd')::bigint AS units,
   count(*) FILTER(WHERE b.revision>=0)::bigint AS revisions,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='wallet'),0) AS wallet,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='reserved'),0) AS reserved,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='debt'),0) AS debt,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='affiliate'),0) AS affiliate
 FROM person p JOIN core_billing.ledger_accounts l ON l.owner_account_id=p.personal_account_id
 LEFT JOIN core_billing.balance_state b ON b.ledger_account_id=l.id
)
SELECT p.native_basis,u.valid AS unit_contract,o.count AS opening_count,o.valid AS opening_valid,
 b.buckets,b.balances,b.wallets,b.reservations,b.debts,b.affiliates,b.units,b.revisions,
 b.wallet::text AS wallet,b.reserved::text AS reserved,b.debt::text AS debt,
 b.affiliate::text AS affiliate,(b.wallet+b.debt)::text AS quota,
 floor(extract(epoch FROM clock_timestamp()))::bigint AS updated_at
FROM person p CROSS JOIN unit_contract u CROSS JOIN opening o CROSS JOIN balances b
"#;

fn safe_integer(value: &str) -> Result<i64, BalanceError> {
    let integer: i128 = value.parse().map_err(|_| BalanceError::Storage)?;
    if !(-JS_SAFE_INTEGER..=JS_SAFE_INTEGER).contains(&integer) {
        return Err(BalanceError::Unavailable);
    }
    i64::try_from(integer).map_err(|_| BalanceError::Storage)
}

fn exact_usd(quota: i64) -> Result<Number, BalanceError> {
    // The verified unit is 500,000 integer credits / USD: one credit is two
    // micro-USD. i128 protects the intermediate even at the signed i64 limits.
    let micro = i128::from(quota) * 2;
    let magnitude = micro.abs();
    let whole = magnitude / 1_000_000;
    let remainder = magnitude % 1_000_000;
    let sign = if micro < 0 { "-" } else { "" };
    let decimal = if remainder == 0 {
        format!("{sign}{whole}")
    } else {
        let fraction = format!("{remainder:06}");
        format!("{sign}{whole}.{}", fraction.trim_end_matches('0'))
    };
    Number::from_str(&decimal).map_err(|_| BalanceError::Storage)
}

fn project(
    quota: i64,
    updated_at: i64,
    _basis: VerifiedProvenance,
) -> Result<BalanceV2, BalanceError> {
    let quota = safe_integer(&quota.to_string())?;
    let updated_at = safe_integer(&updated_at.to_string())?;
    let updated_at = u64::try_from(updated_at).map_err(|_| BalanceError::Unavailable)?;
    Ok(BalanceV2 {
        schema_version: 2,
        currency: "USD",
        balance: exact_usd(quota)?,
        quota,
        public_credit_balance: quota.to_string(),
        credit_unit: 1,
        credits_per_usd: "500000",
        quota_per_unit: CREDITS_PER_USD,
        updated_at,
        authorization_limit: None,
    })
}

/// IDs are obtained from OAuth's private ReadAuthority, never a request body.
/// The caller owns this transaction and maps both errors to safe HTTP 503.
pub(crate) async fn read_balance(
    connection: &mut PgConnection,
    user_id: i64,
    personal_account_id: i64,
) -> Result<BalanceV2, BalanceError> {
    if user_id <= 0 || personal_account_id <= 0 {
        return Err(BalanceError::Unavailable);
    }
    // Preserve the established native/opening + complete exact-counter guard.
    // This is not the unknown-money-basis read_wallet compatibility projection.
    financial_profile::read(connection, user_id).await?;
    let row = sqlx::query(OBSERVATION)
        .bind(user_id)
        .bind(personal_account_id)
        .fetch_optional(connection)
        .await?
        .ok_or(BalanceError::Unavailable)?;
    if !row.try_get::<bool, _>("unit_contract")? {
        return Err(BalanceError::Unavailable);
    }
    let native: bool = row.try_get("native_basis")?;
    let imported: i64 = row.try_get("opening_count")?;
    let basis = match imported {
        0 if native => VerifiedProvenance::NativeAudit,
        1 if row.try_get::<bool, _>("opening_valid")? => VerifiedProvenance::Opening,
        _ => return Err(BalanceError::Unavailable),
    };
    match row.try_get::<i64, _>("buckets")? {
        0 if imported == 0 && native => {}
        4 => {
            for (name, expected) in [
                ("balances", 4),
                ("wallets", 1),
                ("reservations", 1),
                ("debts", 1),
                ("affiliates", 1),
                ("units", 4),
                ("revisions", 4),
            ] {
                if row.try_get::<i64, _>(name)? != expected {
                    return Err(BalanceError::Unavailable);
                }
            }
        }
        _ => return Err(BalanceError::Unavailable),
    }
    let amount =
        |name| -> Result<i64, BalanceError> { safe_integer(&row.try_get::<String, _>(name)?) };
    let wallet = amount("wallet")?;
    let reserved = amount("reserved")?;
    let debt = amount("debt")?;
    let _affiliate = amount("affiliate")?;
    let quota = amount("quota")?;
    if wallet < 0
        || reserved < 0
        || debt > 0
        || i128::from(wallet) + i128::from(debt) != i128::from(quota)
    {
        return Err(BalanceError::Unavailable);
    }
    project(quota, row.try_get("updated_at")?, basis)
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn usd_decimal_is_derived_exactly_without_binary_float() {
        for (quota, expected) in [
            (0, "0"),
            (1, "0.000002"),
            (-1, "-0.000002"),
            (5, "0.00001"),
            (499_999, "0.999998"),
            (500_000, "1"),
            (-500_000, "-1"),
            (9_007_199_254_740_991, "18014398509.481982"),
            (-9_007_199_254_740_991, "-18014398509.481982"),
        ] {
            assert_eq!(exact_usd(quota).unwrap().to_string(), expected);
        }
    }

    #[test]
    fn v2_preserves_integer_credit_truth_and_json_number_compatibility() {
        let payload = project(-70, 1_792_000_000, VerifiedProvenance::Opening).unwrap();
        let json = serde_json::to_string(&payload).unwrap();
        assert!(json.contains("\"balance\":-0.00014"));
        assert!(json.contains("\"quota\":-70"));
        assert!(json.contains("\"public_credit_balance\":\"-70\""));
        assert!(json.contains("\"credits_per_usd\":\"500000\""));
        assert!(json.contains("\"authorization_limit\":null"));
        let value = serde_json::to_value(payload).unwrap();
        assert_eq!(value["schema_version"], 2);
        assert_eq!(value["currency"], "USD");
        assert_eq!(value["credit_unit"], 1);
        assert_eq!(value["quota_per_unit"], 500_000);
        assert_eq!(value.as_object().unwrap().len(), 10);
        assert!(value.get("quota_unit").is_none());
    }

    #[test]
    fn unsafe_credit_or_time_projection_is_unavailable() {
        for quota in [
            9_007_199_254_740_992,
            -9_007_199_254_740_992,
            i64::MAX,
            i64::MIN,
        ] {
            assert_eq!(
                project(quota, 0, VerifiedProvenance::NativeAudit),
                Err(BalanceError::Unavailable)
            );
        }
        for time in [-1, 9_007_199_254_740_992, i64::MAX] {
            assert_eq!(
                project(0, time, VerifiedProvenance::NativeAudit),
                Err(BalanceError::Unavailable)
            );
        }
        assert!(project(0, 0, VerifiedProvenance::NativeAudit).is_ok());
    }
}
