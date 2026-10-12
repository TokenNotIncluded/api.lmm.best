//! Read the authenticated user's Web wallet from committed core facts.
//!
//! The caller must authenticate a session in the same transaction before this
//! function. Enable it explicitly in the assembled core; identity-only servers
//! must not infer that missing financial tables or imported facts mean zero.
use serde::Serialize;
use sqlx::{PgConnection, Row};
use std::fmt;

const JS_SAFE_INTEGER: i128 = 9_007_199_254_740_991;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ProfileError {
    Invalid,
    /// The identity exists, but its historical opening is not established.
    Unavailable,
    Storage,
}
impl fmt::Display for ProfileError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{self:?}")
    }
}
impl std::error::Error for ProfileError {}
impl From<sqlx::Error> for ProfileError {
    fn from(_: sqlx::Error) -> Self {
        Self::Storage
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct FinancialProfile {
    pub financial_projection_available: bool,
    /// Signed economic position; spendable funds and debt are also explicit.
    pub quota: i64,
    pub wallet_spendable_quota: i64,
    pub wallet_reserved_quota: i64,
    pub wallet_debt: i64,
    /// Preserve the legacy raw counter without valuing it at today's FX.
    pub used_quota: i64,
    pub normalized_used_quota: Option<i64>,
    pub usage_projection_available: bool,
    pub request_count: i64,
    pub aff_quota: i64,
    pub aff_debt: i64,
    pub aff_history_quota: i64,
}

fn exact_number(value: &str) -> Result<i64, ProfileError> {
    let integer: i128 = value.parse().map_err(|_| ProfileError::Storage)?;
    if !(-JS_SAFE_INTEGER..=JS_SAFE_INTEGER).contains(&integer) {
        // These legacy DTOs use JavaScript numbers. Never silently round money
        // or change a historical counter to make it fit the browser.
        return Err(ProfileError::Unavailable);
    }
    i64::try_from(integer).map_err(|_| ProfileError::Storage)
}

/// Wallet facts do not depend on imported historical usage counters or FX.
/// `quota` excludes reservations and includes signed debt, exactly as the native
/// ledger's available_units. No total balance is substituted for spendable funds.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct WalletProfile {
    pub financial_projection_available: bool,
    pub quota: i64,
    pub wallet_spendable_quota: i64,
    pub wallet_reserved_quota: i64,
    pub wallet_debt: i64,
    pub wallet_unit: &'static str,
    pub wallet_money_basis: &'static str,
    pub wallet_money_available: bool,
}

/// Caller must first authenticate a native personal Web session. Reads only
/// committed balance_state, never opening counters, receipts or request counts.
/// An absent wallet is zero only for an audited native registration/bootstrap.
pub async fn read_wallet(
    connection: &mut PgConnection,
    user_id: i64,
) -> Result<WalletProfile, ProfileError> {
    if user_id <= 0 {
        return Err(ProfileError::Invalid);
    }
    let row = sqlx::query(
        r#"
WITH person AS (
 SELECT u.id,u.personal_account_id,
   EXISTS(SELECT 1 FROM core_identity.audit a
     WHERE a.actor_user_id=u.id AND a.action IN ('user.register','user.bootstrap')
       AND a.target->>'user_id'=u.id::text) AS native_zero_basis
 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id
 WHERE u.id=$1 AND u.active AND a.active AND a.kind='personal'
), balances AS (
 SELECT count(l.id)::bigint AS buckets,count(b.ledger_account_id)::bigint AS balances,
   count(DISTINCT l.bucket)::bigint AS distinct_buckets,
   count(*) FILTER(WHERE l.bucket IN ('wallet','reserved','debt','affiliate'))::bigint AS known_buckets,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='wallet'),0) AS wallet,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='reserved'),0) AS reserved,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='debt'),0) AS debt
 FROM person p JOIN core_billing.ledger_accounts l ON l.owner_account_id=p.personal_account_id
 LEFT JOIN core_billing.balance_state b ON b.ledger_account_id=l.id
)
SELECT p.native_zero_basis,b.buckets,b.balances,b.distinct_buckets,b.known_buckets,
 b.wallet::text AS wallet,b.reserved::text AS reserved,(-b.debt)::text AS debt,
 (b.wallet+b.debt)::text AS quota
FROM person p CROSS JOIN balances b
"#,
    )
    .bind(user_id)
    .fetch_optional(connection)
    .await?
    .ok_or(ProfileError::Unavailable)?;
    let buckets: i64 = row.try_get("buckets")?;
    if buckets == 0 {
        if !row.try_get::<bool, _>("native_zero_basis")? {
            return Err(ProfileError::Unavailable);
        }
    } else if buckets != 4
        || ["balances", "distinct_buckets", "known_buckets"]
            .into_iter()
            .any(|name| row.try_get::<i64, _>(name).ok() != Some(4))
    {
        return Err(ProfileError::Storage);
    }
    let amount =
        |name| -> Result<i64, ProfileError> { exact_number(&row.try_get::<String, _>(name)?) };
    let wallet = amount("wallet")?;
    let reserved = amount("reserved")?;
    let debt = amount("debt")?;
    if wallet < 0 || reserved < 0 || debt < 0 {
        return Err(ProfileError::Storage);
    }
    Ok(WalletProfile {
        financial_projection_available: true,
        quota: amount("quota")?,
        wallet_spendable_quota: wallet,
        wallet_reserved_quota: reserved,
        wallet_debt: debt,
        wallet_unit: "core_credit",
        wallet_money_basis: "unknown",
        wallet_money_available: false,
    })
}

/// Read one MVCC snapshot with no ledger creation, mutation or inferred grant.
pub async fn read(
    connection: &mut PgConnection,
    user_id: i64,
) -> Result<FinancialProfile, ProfileError> {
    if user_id <= 0 {
        return Err(ProfileError::Invalid);
    }
    let row = sqlx::query(
        r#"
WITH person AS (
 SELECT u.id,u.personal_account_id,
   EXISTS(SELECT 1 FROM core_identity.audit a
     WHERE a.actor_user_id=u.id AND a.action IN ('user.register','user.bootstrap')
       AND a.target->>'user_id'=u.id::text) AS native_zero_basis
 FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id
 WHERE u.id=$1 AND u.active AND a.active AND a.kind='personal'
), opening AS (
 SELECT c.*,TRUE AS present,
   (i.account_id=c.account_id AND i.source_row_sha256=c.source_row_sha256
    AND r.source_archive_sha256=c.source_archive_sha256
    AND r.mapping_sha256=c.mapping_sha256 AND r.observed_at=c.observed_at) AS valid
 FROM core_billing.account_opening_counters c
 JOIN person p ON p.id=c.user_id AND p.personal_account_id=c.account_id
 LEFT JOIN core_billing.opening_imports i ON i.migration_id=c.migration_id
   AND i.source_entity_key=c.source_entity_key
 LEFT JOIN core_billing.opening_import_runs r ON r.migration_id=c.migration_id
), balances AS (
 SELECT count(l.id)::bigint AS bucket_count,count(b.ledger_account_id)::bigint AS balance_count,
   count(*) FILTER(WHERE l.bucket='wallet')::bigint AS wallet_count,
   count(*) FILTER(WHERE l.bucket='reserved')::bigint AS reserved_count,
   count(*) FILTER(WHERE l.bucket='debt')::bigint AS debt_count,
   count(*) FILTER(WHERE l.bucket='affiliate')::bigint AS affiliate_count,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='wallet'),0) AS wallet,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='reserved'),0) AS reserved,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='debt'),0) AS debt,
   COALESCE(sum(b.balance_units::numeric) FILTER(WHERE l.bucket='affiliate'),0) AS affiliate
 FROM person p JOIN core_billing.ledger_accounts l ON l.owner_account_id=p.personal_account_id
 LEFT JOIN core_billing.balance_state b ON b.ledger_account_id=l.id
), usage AS (
 SELECT COALESCE(sum(r.settled::numeric-r.refunded::numeric),0) AS used,
   count(*)::numeric AS requests
 FROM core_charging.requests r JOIN person p ON p.id=r.actor_user_id WHERE r.state='settled'
)
SELECT p.native_zero_basis,COALESCE(o.present,FALSE) AS opening_present,
 COALESCE(o.valid,FALSE) AS opening_valid,
 b.bucket_count,b.balance_count,b.wallet_count,b.reserved_count,b.debt_count,b.affiliate_count,
 (b.wallet+b.debt)::text AS quota,b.wallet::text AS wallet,b.reserved::text AS reserved,
 (-b.debt)::text AS debt,b.affiliate::text AS affiliate,
 (COALESCE(o.used_quota,0)::numeric+u.used)::text AS used,
 COALESCE(o.used_quota,0)::text AS opening_used,u.used::text AS new_used,
 (COALESCE(o.request_count,0)::numeric+u.requests)::text AS requests,
 COALESCE(o.aff_history,0)::text AS affiliate_history
FROM person p CROSS JOIN balances b CROSS JOIN usage u LEFT JOIN opening o ON TRUE
"#,
    )
    .bind(user_id)
    .fetch_optional(connection)
    .await?
    .ok_or(ProfileError::Unavailable)?;
    let imported: bool = row.try_get("opening_present")?;
    if imported {
        if !row.try_get::<bool, _>("opening_valid")? {
            return Err(ProfileError::Storage);
        }
    } else if !row.try_get::<bool, _>("native_zero_basis")? {
        return Err(ProfileError::Unavailable);
    }
    let buckets: i64 = row.try_get("bucket_count")?;
    if buckets == 0 {
        if imported {
            return Err(ProfileError::Storage);
        }
    } else if buckets != 4
        || row.try_get::<i64, _>("balance_count")? != 4
        || [
            "wallet_count",
            "reserved_count",
            "debt_count",
            "affiliate_count",
        ]
        .into_iter()
        .any(|name| row.try_get::<i64, _>(name).ok() != Some(1))
    {
        return Err(ProfileError::Storage);
    }
    let amount =
        |name| -> Result<i64, ProfileError> { exact_number(&row.try_get::<String, _>(name)?) };
    let wallet = amount("wallet")?;
    let reserved = amount("reserved")?;
    let debt = amount("debt")?;
    let affiliate = amount("affiliate")?;
    let new_used = amount("new_used")?;
    if wallet < 0 || reserved < 0 || debt < 0 || new_used < 0 {
        return Err(ProfileError::Storage);
    }
    let normalized = (amount("opening_used")? == 0).then_some(new_used);
    Ok(FinancialProfile {
        financial_projection_available: true,
        quota: amount("quota")?,
        wallet_spendable_quota: wallet,
        wallet_reserved_quota: reserved,
        wallet_debt: debt,
        used_quota: amount("used")?,
        normalized_used_quota: normalized,
        usage_projection_available: normalized.is_some(),
        request_count: amount("requests")?,
        aff_quota: affiliate.max(0),
        aff_debt: (-affiliate).max(0),
        aff_history_quota: amount("affiliate_history")?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn browser_numbers_are_exact_or_unavailable() {
        assert_eq!(
            exact_number("-9007199254740991"),
            Ok(-9_007_199_254_740_991)
        );
        assert_eq!(exact_number("9007199254740991"), Ok(9_007_199_254_740_991));
        for value in [
            "9007199254740992",
            "-9007199254740992",
            "9223372036854775808",
        ] {
            assert_eq!(exact_number(value), Err(ProfileError::Unavailable));
        }
        for value in ["1.5", "NaN", "1e3", "not-a-number"] {
            assert_eq!(exact_number(value), Err(ProfileError::Storage));
        }
    }
}
