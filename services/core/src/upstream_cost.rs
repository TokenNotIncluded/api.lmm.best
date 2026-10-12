//! Exact OpenRouter account charges. Never infer a router's cost from the
//! selected model or add token/tool fees to an already inclusive account charge.

pub const ATTO_USD: u128 = 1_000_000_000_000_000_000;
pub const CREDITS_PER_USD: u128 = 500_000;
pub const MULTIPLIER_ONE: u32 = 1_000_000;

pub fn supported_router(model: &str) -> bool {
    matches!(
        model,
        "nvidia/switchyard" | "openrouter/auto-beta" | "openrouter/fusion" | "typesafe/jev-router"
    )
}

/// Parse a JSON number without passing through f64. Values finer than 10^-18
/// USD are accepted only when excess digits are zero; no intermediate rounding.
pub fn parse_usd(number: &str) -> Option<u128> {
    if number.is_empty() || number.len() > 96 || number.starts_with(['-', '+']) {
        return None;
    }
    let (mantissa, exponent) = match number.split_once(['e', 'E']) {
        Some((m, e)) => (m, e.parse::<i32>().ok()?),
        None => (number, 0),
    };
    if !(-100..=100).contains(&exponent) {
        return None;
    }
    let (whole, fraction) = mantissa.split_once('.').unwrap_or((mantissa, ""));
    if whole.is_empty()
        || !whole
            .bytes()
            .chain(fraction.bytes())
            .all(|c| c.is_ascii_digit())
    {
        return None;
    }
    let coefficient = whole
        .bytes()
        .chain(fraction.bytes())
        .try_fold(0_u128, |v, c| {
            v.checked_mul(10)?.checked_add(u128::from(c - b'0'))
        })?;
    let shift = 18 + exponent - i32::try_from(fraction.len()).ok()?;
    if coefficient == 0 {
        return Some(0);
    }
    if shift >= 0 {
        coefficient.checked_mul(10_u128.checked_pow(shift as u32)?)
    } else {
        let divisor = 10_u128.checked_pow((-shift) as u32)?;
        (coefficient % divisor == 0).then_some(coefficient / divisor)
    }
}

/// Round up once in integer credits, after applying the frozen group ratio.
pub fn credits(atto_usd: u128, group_ppm: u32) -> Option<i64> {
    if !(1..=1_000_000_000).contains(&group_ppm) {
        return None;
    }
    let numerator = atto_usd
        .checked_mul(CREDITS_PER_USD)?
        .checked_mul(u128::from(group_ppm))?;
    i64::try_from(numerator.div_ceil(ATTO_USD * u128::from(MULTIPLIER_ONE))).ok()
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn exact_cost_and_one_final_rounding() {
        assert_eq!(parse_usd("0.000002000000000001"), Some(2_000_000_000_001));
        assert_eq!(
            credits(parse_usd("0.000002000000000001").unwrap(), MULTIPLIER_ONE),
            Some(2)
        );
        assert_eq!(
            credits(parse_usd("0.123456789").unwrap(), 1_250_000),
            Some(77_161)
        );
        assert_eq!(parse_usd("1e-18"), Some(1));
        assert_eq!(parse_usd("1.0000000000000000000"), Some(ATTO_USD));
        for invalid in [
            "-1",
            "NaN",
            "inf",
            "1e-19",
            "1e999999999",
            "1e100",
            "",
            "0.1.2",
        ] {
            assert_eq!(parse_usd(invalid), None, "{invalid}");
        }
        assert_eq!(credits(u128::MAX, MULTIPLIER_ONE), None);
        assert_eq!(credits(ATTO_USD, 0), None);
    }
}
