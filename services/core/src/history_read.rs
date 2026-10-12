// SPDX-License-Identifier: AGPL-3.0-or-later
//! Bounded, read-only display projections. These functions neither authenticate
//! a caller nor establish the provenance of an imported row. A storage adapter
//! must verify A01/A03 scope and source evidence before calling them.
//!
//! Keep legacy quota integers unchanged. Only consumption logs carry the
//! captured credits-per-USD meaning used here; refunds and aggregate buckets do
//! not acquire that meaning from a similar field name.
use num_rational::BigRational;
use serde::{Deserialize, Serialize};
use serde_json::value::RawValue;
use std::collections::BTreeMap;

pub mod snapshot;
pub mod usage;

pub const MAX_RECORDS: usize = 10_000;
pub const MAX_BASES: usize = 64;
pub const MAX_METADATA_BYTES: usize = 64 * 1024;
pub const MAX_CREDIT_BASIS: i64 = 9_007_199_254_740_991;
pub const LOG_TYPE_CONSUME: i32 = 2;

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct QuotaMoneyProjection {
    #[serde(skip_serializing_if = "Option::is_none")]
    pub quota_usd: Option<String>,
    pub quota_money_available: bool,
    pub quota_money_basis: &'static str,
}

impl QuotaMoneyProjection {
    pub fn unknown(basis: &'static str) -> Self {
        Self {
            quota_usd: None,
            quota_money_available: false,
            quota_money_basis: basis,
        }
    }

    fn known(amount: &BigRational, basis: &'static str) -> Self {
        Self {
            quota_usd: Some(decimal_usd(amount)),
            quota_money_available: true,
            quota_money_basis: basis,
        }
    }
}

/// A borrowed projection input, not a request body or an authorization token.
/// `None` preserves a stored NULL instead of silently converting it to zero.
#[derive(Clone, Copy)]
pub struct LogQuotaInput<'a> {
    pub quota: Option<i64>,
    pub log_type: i32,
    pub other: &'a str,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct QuotaStatisticProjection {
    #[serde(flatten)]
    pub money: QuotaMoneyProjection,
    pub unpriced_quota_records: usize,
    pub quota_projection_truncated: bool,
}

// Borrow the original number token: a JSON f64 would silently round a value
// such as 9007199254740991.1 into an apparently valid integer anchor.
#[derive(Deserialize)]
struct CapturedAnchor<'a> {
    #[serde(borrow)]
    pricing_credits_per_usd: &'a RawValue,
}

pub fn captured_credits_per_usd(other: &str) -> Option<i64> {
    // A derived struct can also accept a JSON sequence. The source contract
    // requires an object, as does main's map decoder.
    if other.len() > MAX_METADATA_BYTES || !other.trim_start().starts_with('{') {
        return None;
    }
    let object: CapturedAnchor<'_> = serde_json::from_str(other).ok()?;
    positive_integer_token(object.pricing_credits_per_usd.get())
}

fn positive_integer_token(raw: &str) -> Option<i64> {
    if raw.is_empty() || raw.len() > 64 || !raw.as_bytes()[0].is_ascii_digit() {
        return None;
    }
    let (mantissa, exponent) = match raw.find(['e', 'E']) {
        Some(at) => (&raw[..at], raw[at + 1..].parse::<i32>().ok()?),
        None => (raw, 0),
    };
    if !(-18..=18).contains(&exponent) {
        return None;
    }
    let (whole, fraction) = mantissa.split_once('.').unwrap_or((mantissa, ""));
    // The enclosing RawValue has already checked JSON number syntax. Check
    // digits here as well so this helper never relies on floating-point parsing.
    if !whole.bytes().chain(fraction.bytes()).all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut digits = format!("{whole}{fraction}");
    let shift = exponent - i32::try_from(fraction.len()).ok()?;
    if shift < 0 {
        let remove = usize::try_from(-shift).ok()?;
        if remove > digits.len() || !digits[digits.len() - remove..].bytes().all(|b| b == b'0') {
            return None;
        }
        digits.truncate(digits.len() - remove);
    }
    let digits = digits.trim_start_matches('0');
    let append = usize::try_from(shift.max(0)).ok()?;
    if digits.is_empty() || digits.len().checked_add(append)? > 16 {
        return None;
    }
    let value = digits.parse::<i64>().ok()?.checked_mul(10_i64.pow(append as u32))?;
    (value > 0 && value <= MAX_CREDIT_BASIS).then_some(value)
}

/// Round exactly once, to 18 decimal places, half away from zero. All callers
/// supply nonnegative consumption amounts. No float is used at any point.
fn decimal_usd(amount: &BigRational) -> String {
    let scaled = amount * BigRational::from_integer(1_000_000_000_000_000_000_u64.into());
    let mut units = scaled.to_integer();
    if scaled - BigRational::from_integer(units.clone()) >= BigRational::new(1.into(), 2.into()) {
        units += 1;
    }
    let mut decimal = format!("{:0>19}", units.to_string());
    decimal.insert(decimal.len() - 18, '.');
    decimal.trim_end_matches('0').trim_end_matches('.').to_owned()
}

pub fn project_log(input: LogQuotaInput<'_>) -> QuotaMoneyProjection {
    let Some(quota) = input.quota else {
        return QuotaMoneyProjection::unknown("invalid_stored_quota");
    };
    if input.log_type != LOG_TYPE_CONSUME {
        return QuotaMoneyProjection::unknown("not_consumption");
    }
    let Some(basis) = captured_credits_per_usd(input.other).filter(|_| quota >= 0) else {
        return QuotaMoneyProjection::unknown("missing_credit_basis");
    };
    QuotaMoneyProjection::known(
        &BigRational::new(quota.into(), basis.into()),
        "captured_credits_per_usd",
    )
}

/// Project a complete, authorized consumption selection. The repository must
/// use the same filters and snapshot for the raw sum, count, page and this scan.
/// Do not pass only a page and describe its sum as the all-history statistic.
/// Non-consumption rows in this input are invalid, not silently excluded.
pub fn project_consumption_statistic<'a>(
    rows: impl IntoIterator<Item = LogQuotaInput<'a>>,
    source_truncated: bool,
) -> QuotaStatisticProjection {
    let mut sums = BTreeMap::<i64, i128>::new();
    let mut count = 0;
    let mut unpriced = 0;
    let limited = |unknown| QuotaStatisticProjection {
        money: QuotaMoneyProjection::unknown("projection_limit"),
        unpriced_quota_records: unknown,
        quota_projection_truncated: true,
    };
    for input in rows {
        count += 1;
        if count > MAX_RECORDS {
            return limited(unpriced);
        }
        let quota = input.quota.filter(|value| *value >= 0);
        let basis = captured_credits_per_usd(input.other);
        let (Some(quota), Some(basis)) = (quota, basis) else {
            unpriced += 1;
            continue;
        };
        if input.log_type != LOG_TYPE_CONSUME {
            unpriced += 1;
            continue;
        }
        if !sums.contains_key(&basis) && sums.len() == MAX_BASES {
            return limited(unpriced);
        }
        let sum = sums.entry(basis).or_default();
        let Some(next) = sum.checked_add(i128::from(quota)) else {
            return limited(unpriced);
        };
        *sum = next;
    }
    if source_truncated {
        return limited(unpriced);
    }
    let money = if unpriced > 0 {
        QuotaMoneyProjection::unknown("aggregate_missing_credit_basis")
    } else {
        let total = sums.into_iter().fold(BigRational::from_integer(0.into()), |total, (basis, quota)| {
            total + BigRational::new(quota.into(), basis.into())
        });
        QuotaMoneyProjection::known(
            &total,
            if count == 0 { "empty_result" } else { "captured_credits_per_usd" },
        )
    };
    QuotaStatisticProjection {
        money,
        unpriced_quota_records: unpriced,
        quota_projection_truncated: false,
    }
}

pub fn project_bucket_without_components() -> QuotaMoneyProjection {
    QuotaMoneyProjection::unknown("bucket_without_credit_basis")
}
