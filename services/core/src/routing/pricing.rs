use std::collections::{BTreeMap, HashMap, HashSet};

use serde::{Deserialize, Serialize};

use super::config::{MAX_ROUTES, valid_group, valid_id, valid_model};
use super::{RoutingError, Speed};

pub const MULTIPLIER_ONE: u32 = 1_000_000;
pub const MAX_MULTIPLIER: u32 = 1_000_000_000;
const PER_MILLION: u128 = 1_000_000;
const DENOMINATOR: u128 = PER_MILLION * MULTIPLIER_ONE as u128 * MULTIPLIER_ONE as u128;

/// All token rates are integer micro-USD per 1,000,000 tokens.
#[derive(Clone, Copy, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct TokenRates {
    pub input: u64,
    pub output: u64,
    pub cache_read: u64,
    pub cache_write: u64,
    /// Integer micro-USD per logical request, not per attempt.
    pub request: u64,
}

#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PriceRule {
    pub id: u64,
    pub model: String,
    pub group: String,
    pub speed: Speed,
    pub rates: TokenRates,
    pub group_multiplier_ppm: u32,
    pub speed_multiplier_ppm: u32,
}

/// Independently versioned prices. A route release pins exactly one price version.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct PriceConfig {
    pub version: u64,
    pub rules: Vec<PriceRule>,
}

/// Disjoint counters. Input MUST exclude both cache_read and cache_write tokens.
#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub struct Usage {
    pub input: u64,
    pub output: u64,
    pub cache_read: u64,
    pub cache_write: u64,
}

impl Usage {
    /// Normalize an authoritative total into the disjoint pricing counters.
    /// Callers must resolve missing counters under a versioned provider contract;
    /// an absent total is not zero. Cache reads and writes are disjoint subsets.
    pub fn from_totals(
        input: u64,
        output: u64,
        cache_read: u64,
        cache_write: u64,
    ) -> Result<Self, RoutingError> {
        let input = input
            .checked_sub(cache_read)
            .and_then(|value| value.checked_sub(cache_write))
            .ok_or(RoutingError::InvalidRequest)?;
        Ok(Self {
            input,
            output,
            cache_read,
            cache_write,
        })
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct MicroUsd(pub u64);

/// Integer ledger units. Producing a quote does not reserve or settle funds.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Credits(pub u64);

/// Server-owned conversion, captured with the price before reservation.
/// The revision must change when the conversion changes. This is not HTTP input.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct CreditRate {
    version: u64,
    credits_per_usd: u64,
}

impl CreditRate {
    pub fn new(version: u64, credits_per_usd: u64) -> Result<Self, RoutingError> {
        if !valid_id(version) || !valid_id(credits_per_usd) {
            return Err(RoutingError::InvalidConfig("credit conversion"));
        }
        Ok(Self {
            version,
            credits_per_usd,
        })
    }

    pub fn version(&self) -> u64 {
        self.version
    }

    pub fn credits_per_usd(&self) -> u64 {
        self.credits_per_usd
    }
}

/// One immutable price and conversion for the entire logical request.
/// Persist both with the original operation; never obtain a new quote on retry.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct CreditQuote {
    price: PriceQuote,
    conversion: CreditRate,
}

impl CreditQuote {
    pub fn price(&self) -> PriceQuote {
        self.price
    }

    pub fn conversion(&self) -> CreditRate {
        self.conversion
    }

    /// Sum all dimensions and multipliers, then round up once in ledger units.
    /// Do not round the token rates or the intermediate micro-USD amount.
    pub fn charge(&self, usage: Usage) -> Result<Credits, RoutingError> {
        let mut factor = u128::from(self.conversion.credits_per_usd);
        let mut divisor = PER_MILLION;
        // Reduce the conversion before multiplying. For 500000 credits/USD this
        // is exactly 1/2, including fractional per-million token prices.
        let (mut a, mut b) = (factor, divisor);
        while b != 0 {
            (a, b) = (b, a % b);
        }
        factor /= a;
        divisor /= a;
        let numerator = self
            .price
            .numerator(usage)?
            .checked_mul(factor)
            .ok_or(RoutingError::PriceOverflow)?;
        let denominator = DENOMINATOR
            .checked_mul(divisor)
            .ok_or(RoutingError::PriceOverflow)?;
        Ok(Credits(rounded_amount(numerator, denominator)?))
    }
}

fn rounded_amount(numerator: u128, denominator: u128) -> Result<u64, RoutingError> {
    let amount = numerator.div_ceil(denominator);
    if amount > i64::MAX as u128 {
        return Err(RoutingError::PriceOverflow);
    }
    Ok(amount as u64)
}

/// Immutable request-local price evidence. No floating point or database lookup.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PriceQuote {
    version: u64,
    rule_id: u64,
    rates: TokenRates,
    group_multiplier_ppm: u32,
    speed_multiplier_ppm: u32,
}

impl PriceQuote {
    pub fn version(&self) -> u64 {
        self.version
    }

    pub fn rule_id(&self) -> u64 {
        self.rule_id
    }

    pub fn rates(&self) -> TokenRates {
        self.rates
    }

    pub fn multipliers_ppm(&self) -> (u32, u32) {
        (self.group_multiplier_ppm, self.speed_multiplier_ppm)
    }

    /// Round UP once, after summing all dimensions and both multipliers.
    /// Task 03 must apply this once to a logical request; retries are not new fees.
    pub fn charge(&self, usage: Usage) -> Result<MicroUsd, RoutingError> {
        Ok(MicroUsd(rounded_amount(self.numerator(usage)?, DENOMINATOR)?))
    }

    /// Capture an already validated server conversion without a new price lookup.
    pub fn with_credit_rate(self, conversion: CreditRate) -> CreditQuote {
        CreditQuote {
            price: self,
            conversion,
        }
    }

    fn numerator(&self, usage: Usage) -> Result<u128, RoutingError> {
        let mut numerator = u128::from(self.rates.request) * PER_MILLION;
        for (count, rate) in [
            (usage.input, self.rates.input),
            (usage.output, self.rates.output),
            (usage.cache_read, self.rates.cache_read),
            (usage.cache_write, self.rates.cache_write),
        ] {
            numerator = numerator
                .checked_add(u128::from(count) * u128::from(rate))
                .ok_or(RoutingError::PriceOverflow)?;
        }
        numerator = numerator
            .checked_mul(u128::from(self.group_multiplier_ppm))
            .and_then(|n| n.checked_mul(u128::from(self.speed_multiplier_ppm)))
            .ok_or(RoutingError::PriceOverflow)?;
        Ok(numerator)
    }
}

type ModelPrices = HashMap<String, BTreeMap<Speed, PriceQuote>>;

#[derive(Clone, Debug)]
pub struct PriceSnapshot {
    config: PriceConfig,
    groups: HashMap<String, ModelPrices>,
}

impl PriceSnapshot {
    pub fn compile(config: PriceConfig) -> Result<Self, RoutingError> {
        if !valid_id(config.version) || config.rules.len() > MAX_ROUTES * 3 {
            return Err(RoutingError::InvalidConfig("price version or size"));
        }
        let mut ids = HashSet::new();
        let mut groups: HashMap<String, ModelPrices> = HashMap::new();
        for rule in &config.rules {
            if !valid_id(rule.id)
                || !ids.insert(rule.id)
                || !valid_group(&rule.group)
                || !valid_model(&rule.model)
                || !(1..=MAX_MULTIPLIER).contains(&rule.group_multiplier_ppm)
                || !(1..=MAX_MULTIPLIER).contains(&rule.speed_multiplier_ppm)
                || [
                    rule.rates.input,
                    rule.rates.output,
                    rule.rates.cache_read,
                    rule.rates.cache_write,
                    rule.rates.request,
                ]
                .iter()
                .any(|&rate| rate > i64::MAX as u64)
            {
                return Err(RoutingError::InvalidConfig("price rule"));
            }
            let quote = PriceQuote {
                version: config.version,
                rule_id: rule.id,
                rates: rule.rates,
                group_multiplier_ppm: rule.group_multiplier_ppm,
                speed_multiplier_ppm: rule.speed_multiplier_ppm,
            };
            if groups
                .entry(rule.group.clone())
                .or_default()
                .entry(rule.model.clone())
                .or_default()
                .insert(rule.speed, quote)
                .is_some()
            {
                return Err(RoutingError::InvalidConfig("duplicate price rule"));
            }
        }
        Ok(Self { config, groups })
    }

    pub fn version(&self) -> u64 {
        self.config.version
    }

    pub fn quote(&self, model: &str, group: &str, speed: Speed) -> Option<PriceQuote> {
        self.groups.get(group)?.get(model)?.get(&speed).copied()
    }

    pub fn config(&self) -> &PriceConfig {
        &self.config
    }

    pub(super) fn same_content(&self, other: &Self) -> bool {
        self.groups == other.groups
    }
}

#[cfg(test)]
mod a07_tests {
    use super::*;

    fn quote(rates: TokenRates) -> PriceQuote {
        PriceQuote {
            version: 3,
            rule_id: 7,
            rates,
            group_multiplier_ppm: MULTIPLIER_ONE,
            speed_multiplier_ppm: MULTIPLIER_ONE,
        }
    }

    fn rates(input: u64) -> TokenRates {
        TokenRates {
            input,
            output: 0,
            cache_read: 0,
            cache_write: 0,
            request: 0,
        }
    }

    fn credits(price: PriceQuote) -> CreditQuote {
        price.with_credit_rate(CreditRate::new(9, 500_000).unwrap())
    }

    #[test]
    fn fractional_credit_token_rates_are_not_rounded_before_usage() {
        let frozen = credits(quote(rates(1)));
        for (input, expected) in [(0, 0), (1, 1), (1_000_000, 1), (2_000_000, 1)] {
            assert_eq!(
                frozen.charge(Usage { input, ..Usage::default() }),
                Ok(Credits(expected))
            );
        }
    }

    #[test]
    fn all_dimensions_and_multipliers_are_summed_before_rounding() {
        let mut price = quote(TokenRates {
            input: 1,
            output: 3,
            cache_read: 2,
            cache_write: 5,
            request: 1,
        });
        price.group_multiplier_ppm = 1_250_000;
        price.speed_multiplier_ppm = 800_000;
        let usage = Usage {
            input: 1_000_000,
            output: 2_000_000,
            cache_read: 3_000_000,
            cache_write: 4_000_000,
        };
        assert_eq!(price.charge(usage), Ok(MicroUsd(34)));
        assert_eq!(credits(price).charge(usage), Ok(Credits(17)));
    }

    #[test]
    fn cache_categories_are_not_double_charged_as_uncached_input() {
        let usage = Usage::from_totals(10, 2, 4, 3).unwrap();
        assert_eq!(usage, Usage { input: 3, output: 2, cache_read: 4, cache_write: 3 });
        let price = quote(TokenRates {
            input: 2_000_000,
            output: 4_000_000,
            cache_read: 500_000,
            cache_write: 3_000_000,
            request: 0,
        });
        assert_eq!(credits(price).charge(usage), Ok(Credits(13)));
    }

    #[test]
    fn invalid_total_and_cache_subsets_are_rejected() {
        for (input, read, write) in [(1, 2, 0), (1, 0, 2), (3, 2, 2), (u64::MAX, u64::MAX, 1)] {
            assert_eq!(Usage::from_totals(input, 0, read, write), Err(RoutingError::InvalidRequest));
        }
    }

    #[test]
    fn conversion_is_applied_before_any_currency_rounding() {
        let price = quote(rates(1_100_000));
        let frozen = price.with_credit_rate(CreditRate::new(10, 700_000).unwrap());
        let usage = Usage { input: 1, ..Usage::default() };
        assert_eq!(price.charge(usage), Ok(MicroUsd(2)));
        // 1.1 micro-USD * 0.7 credits/micro-USD = 0.77 credits, rounded to 1.
        // Rounding micro-USD first would incorrectly produce 2 credits.
        assert_eq!(frozen.charge(usage), Ok(Credits(1)));
    }

    #[test]
    fn request_fee_is_once_per_logical_quote_not_per_token_dimension() {
        let price = quote(TokenRates {
            input: 1_000_000,
            output: 1_000_000,
            cache_read: 1_000_000,
            cache_write: 1_000_000,
            request: 1,
        });
        let usage = Usage { input: 1, output: 1, cache_read: 1, cache_write: 1 };
        assert_eq!(credits(price).charge(usage), Ok(Credits(3)));
        assert_eq!(credits(price).charge(Usage::default()), Ok(Credits(1)));
    }

    #[test]
    fn captured_price_and_conversion_are_immutable_values() {
        let mut original = quote(rates(2_000_000));
        let frozen = credits(original);
        original.version = 4;
        original.rates.input = 4_000_000;
        let later = original.with_credit_rate(CreditRate::new(10, 1_000_000).unwrap());
        let usage = Usage { input: 1, ..Usage::default() };
        assert_eq!(frozen.price().version(), 3);
        assert_eq!(frozen.price().rule_id(), 7);
        assert_eq!(frozen.conversion().version(), 9);
        assert_eq!(frozen.conversion().credits_per_usd(), 500_000);
        assert_eq!(frozen.charge(usage), Ok(Credits(1)));
        assert_eq!(later.charge(usage), Ok(Credits(4)));
    }

    #[test]
    fn zero_price_is_zero_but_conversion_must_be_valid() {
        assert_eq!(credits(quote(rates(0))).charge(Usage::default()), Ok(Credits(0)));
        for (version, rate) in [(0, 500_000), (1, 0), (u64::MAX, 1), (1, u64::MAX)] {
            assert!(CreditRate::new(version, rate).is_err());
        }
    }

    #[test]
    fn signed_ledger_limit_and_intermediate_overflow_fail_closed() {
        let mut request_price = quote(rates(0));
        request_price.rates.request = i64::MAX as u64;
        let equal = request_price.with_credit_rate(CreditRate::new(1, 1_000_000).unwrap());
        assert_eq!(equal.charge(Usage::default()), Ok(Credits(i64::MAX as u64)));
        let doubled = request_price.with_credit_rate(CreditRate::new(2, 2_000_000).unwrap());
        assert_eq!(doubled.charge(Usage::default()), Err(RoutingError::PriceOverflow));
        let huge = credits(quote(rates(i64::MAX as u64)));
        assert_eq!(
            huge.charge(Usage { input: u64::MAX, ..Usage::default() }),
            Err(RoutingError::PriceOverflow)
        );
    }
}
