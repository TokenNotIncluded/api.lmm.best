//! Credit compatibility metadata over the raw integer wallet ledger.
//!
//! This module reads an options snapshot; it never initializes options or writes
//! balances. One USD is always 500,000 raw credits; incompatible old bases fail closed.

use std::collections::BTreeMap;

use num_bigint::BigInt;
use num_traits::{Signed, ToPrimitive, Zero};
use serde::{Serialize, Serializer};
use thiserror::Error;

pub const MAX_WALLET_QUOTA: i64 = (1_i64 << 53) - 1;
pub const LEDGER_QUOTA_UNIT: &str = "LEDGER_QUOTA";
pub const PUBLIC_CREDIT_UNIT: &str = "CREDIT";
pub const PUBLIC_CREDIT_OPTION_KEYS: [&str; 4] = [
    "CreditsPerUSD",
    "LegacyPricingQuotaPerUnit",
    "QuotaPerUnit",
    "PublicCreditsPerUSD",
];
pub const CREDITS_PER_USD: i64 = 500_000;
const PROJECTION_PRECISION: u32 = 64;

/// New monetary log text is always real USD. Public P and UI/FX preferences
/// never participate; a missing durable ledger basis is explicitly unavailable.
pub fn format_ledger_usd(quota: i64, ledger_per_usd: &str) -> Result<String, PublicCreditError> {
    if !(-MAX_WALLET_QUOTA..=MAX_WALLET_QUOTA).contains(&quota) {
        return Err(PublicCreditError::InvalidAmount);
    }
    let ledger = ExactDecimal::parse(ledger_per_usd, 80)
        .and_then(ExactDecimal::positive_safe_rate)
        .map_err(|_| PublicCreditError::UnitsUnavailable)?;
    let fixed = ExactDecimal::parse(&CREDITS_PER_USD.to_string(), 80)?;
    if !ledger.equal_value(&fixed) {
        return Err(PublicCreditError::UnitsUnavailable);
    }
    let (ledger_num, ledger_den) = ledger.ratio();
    let scaled = round_quotient_away(
        BigInt::from(quota) * ledger_den * BigInt::from(10_u8).pow(PROJECTION_PRECISION),
        ledger_num,
    );
    let six = round_quotient_away(
        scaled.clone(),
        BigInt::from(10_u8).pow(PROJECTION_PRECISION - 6),
    );
    let text = if !scaled.is_zero() && six.is_zero() {
        format_scaled(scaled, PROJECTION_PRECISION as usize)
    } else {
        let canonical = format_scaled(six, 6);
        let (integer, fractional) = canonical.split_once('.').unwrap_or((&canonical, ""));
        format!("{integer}.{fractional:0<6}")
    };
    Ok(format!("{text} USD"))
}

#[derive(Clone, Copy, Debug, Eq, Error, PartialEq)]
pub enum PublicCreditError {
    #[error("credit currency units are unavailable")]
    UnitsUnavailable,
    #[error("credit amount is invalid or outside the safe wallet domain")]
    InvalidAmount,
    #[error("public credit denomination changed")]
    DenominationChanged,
}

/// Wire metadata matches Go's schema-2 `common.CreditDenomination`.
/// Numeric values are compatibility fields; exact strings define the basis.
#[derive(Clone, Debug, PartialEq, Serialize)]
pub struct CreditDenominationMetadata {
    pub credit_unit_schema_version: u8,
    pub quota_unit: &'static str,
    pub public_credit_unit: &'static str,
    pub legacy_credit_unit: &'static str,
    #[serde(serialize_with = "serialize_compatibility_rate")]
    pub ledger_quota_per_usd: f64,
    pub ledger_quota_per_usd_exact: String,
    #[serde(serialize_with = "serialize_compatibility_rate")]
    pub public_credits_per_usd: f64,
    pub public_credits_per_usd_exact: String,
}

fn serialize_compatibility_rate<S: Serializer>(
    value: &f64,
    serializer: S,
) -> Result<S::Ok, S::Error> {
    if value.is_finite()
        && value.fract() == 0.0
        && *value >= 0.0
        && *value <= MAX_WALLET_QUOTA as f64
    {
        serializer.serialize_i64(*value as i64)
    } else {
        serializer.serialize_f64(*value)
    }
}

/// An immutable request-local basis, shared by input resolution and receipts.
#[derive(Clone, Debug)]
pub struct PublicCreditDenomination {
    ledger: ExactDecimal,
    public: ExactDecimal,
    metadata: CreditDenominationMetadata,
}

impl PublicCreditDenomination {
    pub fn from_options(options: &BTreeMap<String, String>) -> Result<Self, PublicCreditError> {
        let read = |key: &str| {
            options
                .get(key)
                .ok_or(PublicCreditError::UnitsUnavailable)
                .and_then(|raw| ExactDecimal::parse(raw, 80))
                .and_then(ExactDecimal::positive_safe_rate)
                .map_err(|_| PublicCreditError::UnitsUnavailable)
        };
        let ledger = read("CreditsPerUSD")?;
        let legacy = read("LegacyPricingQuotaPerUnit")?;
        let current = ExactDecimal::parse(
            options.get("QuotaPerUnit").map_or("500000", String::as_str),
            80,
        )
        .and_then(ExactDecimal::positive_safe_rate)
        .map_err(|_| PublicCreditError::UnitsUnavailable)?;
        if !legacy.equal_value(&current) {
            return Err(PublicCreditError::UnitsUnavailable);
        }
        // Go retains Q as float64 and validates its shortest decimal against
        // the durable calibration. Do not accept drift hidden by float parsing.
        let runtime_q = current.compatibility_number()?;
        let runtime_decimal = ExactDecimal::parse(&runtime_q.to_string(), 80)
            .map_err(|_| PublicCreditError::UnitsUnavailable)?;
        if !legacy.equal_value(&runtime_decimal) {
            return Err(PublicCreditError::UnitsUnavailable);
        }
        let public = match options.get("PublicCreditsPerUSD") {
            Some(raw) => {
                let value = ExactDecimal::parse(raw, 80)
                    .and_then(ExactDecimal::positive_safe_rate)
                    .map_err(|_| PublicCreditError::UnitsUnavailable)?;
                if !value.is_integer() {
                    return Err(PublicCreditError::UnitsUnavailable);
                }
                value
            }
            // A pre-schema-2 source remains readable without creating a default
            // option that would silently reinterpret legacy quota integers.
            None => ledger.clone(),
        };
        // A denomination change must migrate raw balances, never reinterpret
        // their USD value or introduce a different displayed credit unit.
        let fixed = ExactDecimal::parse(&CREDITS_PER_USD.to_string(), 80)?;
        if [&ledger, &public, &legacy, &current]
            .iter()
            .any(|rate| !rate.equal_value(&fixed))
        {
            return Err(PublicCreditError::UnitsUnavailable);
        }
        let metadata = CreditDenominationMetadata {
            credit_unit_schema_version: 2,
            quota_unit: LEDGER_QUOTA_UNIT,
            public_credit_unit: PUBLIC_CREDIT_UNIT,
            legacy_credit_unit: LEDGER_QUOTA_UNIT,
            ledger_quota_per_usd: ledger.compatibility_number()?,
            ledger_quota_per_usd_exact: ledger.canonical(),
            public_credits_per_usd: public.compatibility_number()?,
            public_credits_per_usd_exact: public.canonical(),
        };
        Ok(Self {
            ledger,
            public,
            metadata,
        })
    }

    pub fn metadata(&self) -> &CreditDenominationMetadata {
        &self.metadata
    }

    /// Signed display projection, rounded to 64 fractional places with Go's
    /// ties-away-from-zero rule. All intermediate arithmetic is arbitrary precision.
    pub fn project_ledger_quota(&self, quota: i64) -> Result<String, PublicCreditError> {
        if !(-MAX_WALLET_QUOTA..=MAX_WALLET_QUOTA).contains(&quota) {
            return Err(PublicCreditError::InvalidAmount);
        }
        let (ledger_num, ledger_den) = self.ledger.ratio();
        let (public_num, public_den) = self.public.ratio();
        let numerator = BigInt::from(quota)
            * public_num
            * ledger_den
            * BigInt::from(10_u8).pow(PROJECTION_PRECISION);
        let denominator = public_den * ledger_num;
        Ok(format_scaled(
            round_quotient_away(numerator, denominator),
            PROJECTION_PRECISION as usize,
        ))
    }

    /// Positive explicit CREDIT inputs are floored once into the ledger.
    /// Zero, sub-unit dust, and out-of-domain results never become a debit.
    pub fn resolve_public_credits(&self, amount: &str) -> Result<i64, PublicCreditError> {
        let amount = ExactDecimal::parse(amount, 128)?;
        if !amount.coefficient.is_positive() {
            return Err(PublicCreditError::InvalidAmount);
        }
        let (amount_num, amount_den) = amount.ratio();
        let (ledger_num, ledger_den) = self.ledger.ratio();
        let (public_num, public_den) = self.public.ratio();
        let quota = (amount_num * ledger_num * public_den) / (amount_den * ledger_den * public_num);
        positive_wallet_integer(quota)
    }

    pub fn check_public_credits_per_usd(&self, expected: &str) -> Result<(), PublicCreditError> {
        if expected.is_empty() {
            return Err(PublicCreditError::InvalidAmount);
        }
        if expected != self.metadata.public_credits_per_usd_exact {
            return Err(PublicCreditError::DenominationChanged);
        }
        Ok(())
    }

    /// Resolve a versioned amount after the route has checked schema/presence.
    /// CREDIT requires the exact public basis the user reviewed; LEDGER_QUOTA
    /// cannot carry that guard and is always interpreted as a ledger integer.
    pub fn resolve_amount(
        &self,
        unit: &str,
        amount: &str,
        expected_public_credits_per_usd_exact: Option<&str>,
    ) -> Result<i64, PublicCreditError> {
        match unit {
            PUBLIC_CREDIT_UNIT => {
                self.check_public_credits_per_usd(
                    expected_public_credits_per_usd_exact
                        .ok_or(PublicCreditError::InvalidAmount)?,
                )?;
                self.resolve_public_credits(amount)
            }
            LEDGER_QUOTA_UNIT if expected_public_credits_per_usd_exact.is_none() => {
                let amount = ExactDecimal::parse(amount, 128)?;
                let (numerator, denominator) = amount.ratio();
                if !(&numerator % &denominator).is_zero() {
                    return Err(PublicCreditError::InvalidAmount);
                }
                positive_wallet_integer(numerator / denominator)
            }
            _ => Err(PublicCreditError::InvalidAmount),
        }
    }
}

/// Bounded decimal representation retains the input exponent, matching Go's
/// 18-place boundary even when trailing zeroes do not change the numeric value.
#[derive(Clone, Debug)]
struct ExactDecimal {
    coefficient: BigInt,
    exponent: i32,
}

impl ExactDecimal {
    fn parse(raw: &str, max_bytes: usize) -> Result<Self, PublicCreditError> {
        let invalid = || PublicCreditError::InvalidAmount;
        if raw.is_empty() || raw.len() > max_bytes || raw.trim() != raw {
            return Err(invalid());
        }
        let mut parts = raw.split(['e', 'E']);
        let mantissa = parts.next().ok_or_else(invalid)?;
        let explicit_exponent = match parts.next() {
            Some(value) => value.parse::<i32>().map_err(|_| invalid())?,
            None => 0,
        };
        if parts.next().is_some() {
            return Err(invalid());
        }
        let (negative, mantissa) = if let Some(value) = mantissa.strip_prefix('-') {
            (true, value)
        } else {
            (false, mantissa.strip_prefix('+').unwrap_or(mantissa))
        };
        let mut digits = String::new();
        let mut decimal_point = false;
        let mut fractional_digits = 0_i32;
        for byte in mantissa.bytes() {
            match byte {
                b'.' if !decimal_point => decimal_point = true,
                b'0'..=b'9' => {
                    digits.push(char::from(byte));
                    if decimal_point {
                        fractional_digits += 1;
                    }
                }
                _ => return Err(invalid()),
            }
        }
        if digits.is_empty() {
            return Err(invalid());
        }
        let exponent = explicit_exponent
            .checked_sub(fractional_digits)
            .filter(|value| (-18..=18).contains(value))
            .ok_or_else(invalid)?;
        let mut coefficient = BigInt::parse_bytes(digits.as_bytes(), 10).ok_or_else(invalid)?;
        if coefficient.to_string().len() > 80 {
            return Err(invalid());
        }
        if negative {
            coefficient = -coefficient;
        }
        Ok(Self {
            coefficient,
            exponent,
        })
    }

    fn ratio(&self) -> (BigInt, BigInt) {
        let scale = BigInt::from(10_u8).pow(self.exponent.unsigned_abs());
        if self.exponent >= 0 {
            (&self.coefficient * scale, BigInt::from(1_u8))
        } else {
            (self.coefficient.clone(), scale)
        }
    }

    fn positive_safe_rate(self) -> Result<Self, PublicCreditError> {
        let (numerator, denominator) = self.ratio();
        if !numerator.is_positive() || numerator > BigInt::from(MAX_WALLET_QUOTA) * denominator {
            return Err(PublicCreditError::UnitsUnavailable);
        }
        Ok(self)
    }

    fn equal_value(&self, other: &Self) -> bool {
        let (left_num, left_den) = self.ratio();
        let (right_num, right_den) = other.ratio();
        left_num * right_den == right_num * left_den
    }

    fn is_integer(&self) -> bool {
        let (numerator, denominator) = self.ratio();
        (numerator % denominator).is_zero()
    }

    fn canonical(&self) -> String {
        if self.exponent >= 0 {
            (&self.coefficient * BigInt::from(10_u8).pow(self.exponent as u32)).to_string()
        } else {
            format_scaled(
                self.coefficient.clone(),
                self.exponent.unsigned_abs() as usize,
            )
        }
    }

    fn compatibility_number(&self) -> Result<f64, PublicCreditError> {
        self.canonical()
            .parse::<f64>()
            .ok()
            .filter(|value| value.is_finite() && *value > 0.0)
            .ok_or(PublicCreditError::UnitsUnavailable)
    }
}

fn positive_wallet_integer(value: BigInt) -> Result<i64, PublicCreditError> {
    value
        .to_i64()
        .filter(|quota| (1..=MAX_WALLET_QUOTA).contains(quota))
        .ok_or(PublicCreditError::InvalidAmount)
}

fn round_quotient_away(numerator: BigInt, denominator: BigInt) -> BigInt {
    let negative = numerator.is_negative();
    let numerator = numerator.abs();
    let mut result = &numerator / &denominator;
    if (numerator % &denominator) * 2_u8 >= denominator {
        result += 1_u8;
    }
    if negative { -result } else { result }
}

fn format_scaled(value: BigInt, fractional_places: usize) -> String {
    if value.is_zero() {
        return "0".to_owned();
    }
    let negative = value.is_negative();
    let digits = value.abs().to_string();
    let result = if fractional_places == 0 {
        digits
    } else if digits.len() > fractional_places {
        let split = digits.len() - fractional_places;
        format!("{}.{}", &digits[..split], &digits[split..])
    } else {
        format!(
            "0.{}{}",
            "0".repeat(fractional_places - digits.len()),
            digits
        )
    };
    let result = if fractional_places == 0 {
        result
    } else {
        result
            .trim_end_matches('0')
            .trim_end_matches('.')
            .to_owned()
    };
    if negative {
        format!("-{result}")
    } else {
        result
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    fn options() -> BTreeMap<String, String> {
        PUBLIC_CREDIT_OPTION_KEYS
            .into_iter()
            .map(|key| (key.to_owned(), "500000".to_owned()))
            .collect()
    }

    #[test]
    fn fixed_credits_preserve_raw_integer_balances_and_model_prices() {
        let units = PublicCreditDenomination::from_options(&options()).unwrap();
        for quota in [0, 1, -1, 500_000, 5_000_000, MAX_WALLET_QUOTA] {
            assert_eq!(
                units.project_ledger_quota(quota).unwrap(),
                quota.to_string()
            );
        }
        for amount in [1, 500_000, 5_000_000, MAX_WALLET_QUOTA] {
            assert_eq!(
                units.resolve_public_credits(&amount.to_string()).unwrap(),
                amount
            );
            assert_eq!(
                units
                    .resolve_amount("CREDIT", &amount.to_string(), Some("500000"))
                    .unwrap(),
                amount
            );
            assert_eq!(
                units
                    .resolve_amount("LEDGER_QUOTA", &amount.to_string(), None)
                    .unwrap(),
                amount
            );
        }
        assert_eq!(units.metadata().ledger_quota_per_usd_exact, "500000");
        assert_eq!(units.metadata().public_credits_per_usd_exact, "500000");
    }

    #[test]
    fn old_revalued_or_public_denominations_are_explicitly_unavailable() {
        for key in PUBLIC_CREDIT_OPTION_KEYS {
            for value in ["100000", "3359744", "500001", "0", "NaN"] {
                let mut values = options();
                values.insert(key.to_owned(), value.to_owned());
                assert!(
                    matches!(
                        PublicCreditDenomination::from_options(&values),
                        Err(PublicCreditError::UnitsUnavailable)
                    ),
                    "{key}={value}"
                );
            }
        }
        let mut legacy = options();
        legacy.remove("PublicCreditsPerUSD");
        let before = legacy.clone();
        assert_eq!(
            PublicCreditDenomination::from_options(&legacy)
                .unwrap()
                .project_ledger_quota(123)
                .unwrap(),
            "123"
        );
        assert_eq!(legacy, before);
        legacy.insert("CreditsPerUSD".into(), "3359744".into());
        assert!(PublicCreditDenomination::from_options(&legacy).is_err());
    }

    #[test]
    fn explicit_credit_inputs_retain_guards_and_wallet_domain() {
        let units = PublicCreditDenomination::from_options(&options()).unwrap();
        assert_eq!(units.resolve_public_credits("1.9").unwrap(), 1);
        assert_eq!(
            units.resolve_amount("CREDIT", "1", Some("100000")),
            Err(PublicCreditError::DenominationChanged)
        );
        for (unit, amount, expected) in [
            ("CREDIT", "1", None),
            ("CREDIT", "1", Some("")),
            ("LEDGER_QUOTA", "1.5", None),
            ("LEDGER_QUOTA", "1", Some("500000")),
        ] {
            assert_eq!(
                units.resolve_amount(unit, amount, expected),
                Err(PublicCreditError::InvalidAmount)
            );
        }
        for amount in ["0", "-1", "0.5", "NaN", "1e100000", "9007199254740992"] {
            assert_eq!(
                units.resolve_public_credits(amount),
                Err(PublicCreditError::InvalidAmount)
            );
        }
        assert!(units.project_ledger_quota(MAX_WALLET_QUOTA + 1).is_err());
    }

    #[test]
    fn usd_log_text_uses_fixed_credit_basis() {
        assert_eq!(
            format_ledger_usd(500_000, "500000").unwrap(),
            "1.000000 USD"
        );
        assert_eq!(
            format_ledger_usd(5_000_000, "500000").unwrap(),
            "10.000000 USD"
        );
        assert_eq!(
            format_ledger_usd(-500_000, "500000").unwrap(),
            "-1.000000 USD"
        );
        assert_eq!(format_ledger_usd(1, "500000").unwrap(), "0.000002 USD");
        assert!(format_ledger_usd(1, "3359744").is_err());
    }
}
