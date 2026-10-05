//! Exact public denominations over the unchanged legacy wallet ledger.
//!
//! This module reads an options snapshot; it never initializes options or writes
//! balances. Public display strings are projections, not lossless debit inputs.

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
        BTreeMap::from([
            ("CreditsPerUSD".into(), "3359744".into()),
            ("LegacyPricingQuotaPerUnit".into(), "500000".into()),
            ("QuotaPerUnit".into(), "500000".into()),
            ("PublicCreditsPerUSD".into(), "100000".into()),
        ])
    }

    #[test]
    fn go_projection_vectors_preserve_signed_ledger_and_dust() {
        let units = PublicCreditDenomination::from_options(&options()).unwrap();
        assert_eq!(units.project_ledger_quota(3_359_744).unwrap(), "100000");
        let dust = "0.0297641725083815909783602560195062480950929594635781773849436148";
        assert_eq!(units.project_ledger_quota(1).unwrap(), dust);
        assert_eq!(units.project_ledger_quota(-1).unwrap(), format!("-{dust}"));
        assert_eq!(units.project_ledger_quota(0).unwrap(), "0");
        assert!(units.project_ledger_quota(MAX_WALLET_QUOTA + 1).is_err());
        assert!(units.project_ledger_quota(-MAX_WALLET_QUOTA - 1).is_err());
    }

    #[test]
    fn large_products_do_not_overflow_decimal_storage() {
        let mut values = options();
        values.insert("CreditsPerUSD".into(), "1".into());
        values.insert("PublicCreditsPerUSD".into(), MAX_WALLET_QUOTA.to_string());
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        assert_eq!(
            units.project_ledger_quota(MAX_WALLET_QUOTA).unwrap(),
            "81129638414606663681390495662081"
        );
        assert_eq!(
            units.project_ledger_quota(-MAX_WALLET_QUOTA).unwrap(),
            "-81129638414606663681390495662081"
        );
    }

    #[test]
    fn public_input_floors_and_guard_matches_captured_metadata() {
        let units = PublicCreditDenomination::from_options(&options()).unwrap();
        assert_eq!(units.resolve_public_credits("100000").unwrap(), 3_359_744);
        assert_eq!(units.resolve_public_credits("1").unwrap(), 33);
        assert_eq!(units.resolve_public_credits("1.001").unwrap(), 33);
        assert_eq!(
            units.resolve_amount("CREDIT", "1", Some("100000")).unwrap(),
            33
        );
        assert_eq!(
            units.resolve_amount("LEDGER_QUOTA", "33", None).unwrap(),
            33
        );
        assert_eq!(
            units.resolve_amount("LEDGER_QUOTA", "33.0", None).unwrap(),
            33
        );
        assert_eq!(
            units.resolve_amount("CREDIT", "1", Some("200000")),
            Err(PublicCreditError::DenominationChanged)
        );
        for (unit, amount, expected) in [
            ("CREDIT", "1", None),
            ("CREDIT", "1", Some("")),
            ("LEDGER_QUOTA", "33.5", None),
            ("LEDGER_QUOTA", "33", Some("100000")),
            ("credit", "1", Some("100000")),
        ] {
            assert_eq!(
                units.resolve_amount(unit, amount, expected),
                Err(PublicCreditError::InvalidAmount)
            );
        }
    }

    #[test]
    fn invalid_and_unrepresentable_inputs_fail_closed() {
        let units = PublicCreditDenomination::from_options(&options()).unwrap();
        for amount in [
            "0",
            "-0",
            "-1",
            "0.000000000000000001",
            "1.0000000000000000000",
            "1e19",
            "1e100000",
            "NaN",
            "inf",
            " 1",
            "1 ",
            "1e",
            ".",
            "++1",
            "1e1e1",
            "9007199254740991",
        ] {
            assert_eq!(
                units.resolve_public_credits(amount),
                Err(PublicCreditError::InvalidAmount),
                "{amount}"
            );
        }
        assert!(units.resolve_public_credits(&"1".repeat(81)).is_err());
        assert!(units.resolve_public_credits(&"1".repeat(129)).is_err());
        assert!(
            units
                .resolve_amount("LEDGER_QUOTA", "9007199254740992", None)
                .is_err()
        );
    }

    #[test]
    fn floor_maximum_and_public_display_are_not_lossless_debit_inputs() {
        let mut values = options();
        values.insert("CreditsPerUSD".into(), "1".into());
        values.insert("PublicCreditsPerUSD".into(), "1".into());
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        assert_eq!(
            units.resolve_public_credits("9007199254740991.9").unwrap(),
            MAX_WALLET_QUOTA
        );
        assert!(units.resolve_public_credits("9007199254740992").is_err());
        values.insert("CreditsPerUSD".into(), "3".into());
        values.insert("PublicCreditsPerUSD".into(), "2".into());
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        assert_eq!(
            units.project_ledger_quota(2).unwrap(),
            "1.3333333333333333333333333333333333333333333333333333333333333333"
        );
        assert_eq!(
            units
                .resolve_public_credits("1.333333333333333333")
                .unwrap(),
            1
        );
        assert!(
            units
                .resolve_public_credits(&units.project_ledger_quota(2).unwrap())
                .is_err()
        );
    }

    #[test]
    fn option_changes_do_not_mix_one_snapshots_metadata_and_amounts() {
        let mut values = options();
        let before = PublicCreditDenomination::from_options(&values).unwrap();
        values.insert("PublicCreditsPerUSD".into(), "200000".into());
        let after = PublicCreditDenomination::from_options(&values).unwrap();
        for (units, expected) in [(&before, "100000"), (&after, "200000")] {
            assert_eq!(units.metadata().public_credits_per_usd_exact, expected);
            assert_eq!(units.project_ledger_quota(3_359_744).unwrap(), expected);
        }
        let metadata = serde_json::to_value(before.metadata()).unwrap();
        assert_eq!(metadata["credit_unit_schema_version"], 2);
        assert_eq!(metadata["quota_unit"], "LEDGER_QUOTA");
        assert_eq!(metadata["legacy_credit_unit"], "LEDGER_QUOTA");
        assert_eq!(metadata["public_credit_unit"], "CREDIT");
        assert_eq!(metadata["ledger_quota_per_usd"], 3_359_744.0);
        assert_eq!(metadata["ledger_quota_per_usd_exact"], "3359744");
        assert_eq!(metadata["public_credits_per_usd"], 100_000.0);
        assert_eq!(metadata["public_credits_per_usd_exact"], "100000");
    }

    #[test]
    fn absent_public_option_is_read_only_legacy_compatibility() {
        let mut values = options();
        values.remove("PublicCreditsPerUSD");
        let before = values.clone();
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        assert_eq!(units.metadata().public_credits_per_usd_exact, "3359744");
        assert_eq!(units.project_ledger_quota(123).unwrap(), "123");
        assert_eq!(units.resolve_public_credits("123").unwrap(), 123);
        assert_eq!(values, before);
        values.insert("CreditsPerUSD".into(), "0.125".into());
        let fractional = PublicCreditDenomination::from_options(&values).unwrap();
        assert_eq!(fractional.metadata().public_credits_per_usd_exact, "0.125");
        assert_eq!(fractional.project_ledger_quota(123).unwrap(), "123");
        assert_eq!(fractional.project_ledger_quota(-123).unwrap(), "-123");
        assert_eq!(fractional.resolve_public_credits("123").unwrap(), 123);
    }

    #[test]
    fn invalid_options_and_calibration_drift_fail_closed() {
        for key in ["CreditsPerUSD", "LegacyPricingQuotaPerUnit"] {
            let mut values = options();
            values.remove(key);
            assert!(matches!(
                PublicCreditDenomination::from_options(&values),
                Err(PublicCreditError::UnitsUnavailable)
            ));
        }
        for (key, value) in [
            ("PublicCreditsPerUSD", "0"),
            ("PublicCreditsPerUSD", "-1"),
            ("PublicCreditsPerUSD", "1.5"),
            ("PublicCreditsPerUSD", "nonnumeric"),
            ("PublicCreditsPerUSD", "9007199254740992"),
            ("CreditsPerUSD", "0"),
            ("CreditsPerUSD", "NaN"),
            ("LegacyPricingQuotaPerUnit", "499999"),
            ("QuotaPerUnit", "500001"),
        ] {
            let mut values = options();
            values.insert(key.into(), value.into());
            assert!(
                matches!(
                    PublicCreditDenomination::from_options(&values),
                    Err(PublicCreditError::UnitsUnavailable)
                ),
                "{key}={value}"
            );
        }
        let mut values = options();
        for key in ["LegacyPricingQuotaPerUnit", "QuotaPerUnit"] {
            values.insert(key.into(), "1.0000000000000001".into());
        }
        assert!(matches!(
            PublicCreditDenomination::from_options(&values),
            Err(PublicCreditError::UnitsUnavailable)
        ));
        values.insert("LegacyPricingQuotaPerUnit".into(), "12.500".into());
        values.insert("QuotaPerUnit".into(), "1.25e1".into());
        values.insert("PublicCreditsPerUSD".into(), "1e5".into());
        assert!(PublicCreditDenomination::from_options(&values).is_ok());
        let mut values = options();
        values.remove("QuotaPerUnit");
        assert!(PublicCreditDenomination::from_options(&values).is_ok());
        values.insert("LegacyPricingQuotaPerUnit".into(), "500001".into());
        assert!(matches!(
            PublicCreditDenomination::from_options(&values),
            Err(PublicCreditError::UnitsUnavailable)
        ));
    }

    #[test]
    fn extreme_small_ledger_basis_preserves_large_exact_public_amounts() {
        let mut values = options();
        values.insert("CreditsPerUSD".into(), "1e-18".into());
        values.insert("PublicCreditsPerUSD".into(), MAX_WALLET_QUOTA.to_string());
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        let expected = "81129638414606663681390495662081000000000000000000";
        assert_eq!(
            units.project_ledger_quota(MAX_WALLET_QUOTA).unwrap(),
            expected
        );
        assert_eq!(
            units.resolve_public_credits(expected).unwrap(),
            MAX_WALLET_QUOTA
        );
        assert_eq!(
            units.metadata().ledger_quota_per_usd_exact,
            "0.000000000000000001"
        );
    }

    #[test]
    fn complete_projection_rounds_sixty_four_place_half_ties_away() {
        let mut values = options();
        // K=2^83/10^18 yields a half at the 65th fractional place.
        values.insert("CreditsPerUSD".into(), "9671406.556917033397649408".into());
        values.insert("PublicCreditsPerUSD".into(), "1".into());
        let units = PublicCreditDenomination::from_options(&values).unwrap();
        let expected = "0.0000001033975765691284593589260865087453566957265138626098632813";
        assert_eq!(units.project_ledger_quota(1).unwrap(), expected);
        assert_eq!(
            units.project_ledger_quota(-1).unwrap(),
            format!("-{expected}")
        );
    }

    #[test]
    fn signed_division_rounds_half_away_from_zero() {
        for (numerator, expected) in [(1, 1), (-1, -1), (3, 2), (-3, -2)] {
            assert_eq!(
                round_quotient_away(BigInt::from(numerator), BigInt::from(2)),
                BigInt::from(expected)
            );
        }
    }

    #[test]
    fn usd_log_text_uses_immutable_ledger_basis_and_preserves_small_nonzero_amounts() {
        assert_eq!(
            format_ledger_usd(7_300_000, "7300000").unwrap(),
            "1.000000 USD"
        );
        assert_eq!(
            format_ledger_usd(-7_300_000, "7300000").unwrap(),
            "-1.000000 USD"
        );
        assert_eq!(format_ledger_usd(0, "7300000").unwrap(), "0.000000 USD");
        assert_eq!(
            format_ledger_usd(1, "7300000").unwrap(),
            "0.0000001369863013698630136986301369863013698630136986301369863014 USD"
        );
        assert!(format_ledger_usd(1, "").is_err());
        assert!(format_ledger_usd(1, "0").is_err());
        // This API accepts no PublicP, UI display preference, or FX input.
        assert_eq!(format_ledger_usd(1, "500000").unwrap(), "0.000002 USD");
    }
}
