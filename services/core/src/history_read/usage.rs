// SPDX-License-Identifier: AGPL-3.0-or-later
//! The arithmetic part of main's UsageProjector, deliberately separate from
//! per-log USD conversion. This does not validate migration records or refunds.
//! A03 must supply a complete, verified baseline and reversal set before use.
use num_rational::BigRational;
use serde::Serialize;

const MAX_QUOTA: i64 = 9_007_199_254_740_991;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum UsageRounding {
    HalfAwayFromZero,
    TowardZero,
}

/// Arithmetic inputs only. Do not derive Deserialize or use this as evidence
/// that a client-selected divisor, historical owner or refund is authorized.
pub struct UsageArithmeticInput<'a> {
    pub current_used: i64,
    pub historical_raw: i64,
    pub reversed_raw: i64,
    pub reversed_normalized: i64,
    pub divisor: &'a str,
    pub rounding: UsageRounding,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct UsageProjection {
    pub normalized_used_quota: i64,
    pub historical_raw_quota: i64,
    pub historical_normalized_quota: i64,
    pub post_migration_quota: i64,
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum UsageError {
    InvalidBasis,
    InvalidCounter,
    UnprovenReduction,
    OutOfRange,
}

fn parse_divisor(text: &str) -> Option<BigRational> {
    // Bound allocation before parsing; main permits decimal divisors with up
    // to 18 fractional digits. Neither scientific notation nor floats apply.
    if text.is_empty() || text.len() > 64 {
        return None;
    }
    let (whole, fraction) = text.split_once('.').unwrap_or((text, ""));
    if whole.is_empty()
        || !whole.bytes().all(|b| b.is_ascii_digit())
        || !fraction.bytes().all(|b| b.is_ascii_digit())
        || fraction.len() > 18
        || (text.contains('.') && fraction.is_empty())
    {
        return None;
    }
    let denominator = 10_i64.pow(fraction.len() as u32);
    let numerator = format!("{whole}{fraction}").parse().ok()?;
    let divisor = BigRational::new(numerator, denominator.into());
    (divisor > BigRational::from_integer(1.into())).then_some(divisor)
}

pub fn project_migrated_usage(input: &UsageArithmeticInput<'_>) -> Result<UsageProjection, UsageError> {
    for value in [input.current_used, input.historical_raw, input.reversed_raw, input.reversed_normalized] {
        if !(0..=MAX_QUOTA).contains(&value) {
            return Err(UsageError::InvalidCounter);
        }
    }
    if input.reversed_raw > input.historical_raw {
        return Err(UsageError::InvalidBasis);
    }
    let divisor = parse_divisor(input.divisor).ok_or(UsageError::InvalidBasis)?;
    let scaled = BigRational::from_integer(input.historical_raw.into()) / divisor;
    let mut normalized = scaled.to_integer();
    if input.rounding == UsageRounding::HalfAwayFromZero
        && scaled - BigRational::from_integer(normalized.clone()) >= BigRational::new(1.into(), 2.into())
    {
        normalized += 1;
    }
    // Conversion happens only after exact rounding. The divisor is > 1 and
    // the raw baseline is bounded, so no valid result needs a wider DTO.
    let normalized: i64 = normalized.to_string().parse().map_err(|_| UsageError::OutOfRange)?;
    let historical = normalized.checked_sub(input.reversed_normalized).ok_or(UsageError::OutOfRange)?;
    let delta = input.current_used.checked_sub(input.historical_raw)
        .and_then(|value| value.checked_add(input.reversed_raw))
        .ok_or(UsageError::OutOfRange)?;
    if delta < 0 {
        return Err(UsageError::UnprovenReduction);
    }
    let total = historical.checked_add(delta).ok_or(UsageError::OutOfRange)?;
    if historical < 0 || !(0..=MAX_QUOTA).contains(&total) {
        return Err(UsageError::OutOfRange);
    }
    Ok(UsageProjection {
        normalized_used_quota: total,
        historical_raw_quota: input.historical_raw - input.reversed_raw,
        historical_normalized_quota: historical,
        post_migration_quota: delta,
    })
}
