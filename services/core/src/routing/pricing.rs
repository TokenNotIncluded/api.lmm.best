use std::collections::{BTreeMap, HashMap, HashSet};

use serde::{Deserialize, Serialize};

use super::config::{MAX_ROUTES, valid_group, valid_id, valid_model};
use super::{RoutingError, Speed};

pub const MULTIPLIER_ONE: u32 = 1_000_000;
pub const MAX_MULTIPLIER: u32 = 1_000_000_000;
const PER_MILLION: u128 = 1_000_000;
const DENOMINATOR: u128 = PER_MILLION * MULTIPLIER_ONE as u128 * MULTIPLIER_ONE as u128;

#[derive(Clone, Copy, Debug, Default, Deserialize, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum BillingMode {
    #[default]
    Tokens,
    UpstreamCost,
}
impl BillingMode {
    fn is_tokens(&self) -> bool {
        *self == Self::Tokens
    }
}

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
    #[serde(default, skip_serializing_if = "BillingMode::is_tokens")]
    pub billing_mode: BillingMode,
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

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct MicroUsd(pub u64);

/// Immutable request-local price evidence. No floating point or database lookup.
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct PriceQuote {
    billing_mode: BillingMode,
    version: u64,
    rule_id: u64,
    rates: TokenRates,
    group_multiplier_ppm: u32,
    speed_multiplier_ppm: u32,
}

impl PriceQuote {
    pub fn billing_mode(&self) -> BillingMode {
        self.billing_mode
    }
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
        if self.billing_mode == BillingMode::UpstreamCost {
            return Err(RoutingError::InvalidConfig(
                "upstream cost cannot be inferred from token counts",
            ));
        }
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
        let amount = numerator.div_ceil(DENOMINATOR);
        if amount > i64::MAX as u128 {
            return Err(RoutingError::PriceOverflow);
        }
        Ok(MicroUsd(amount as u64))
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
            if rule.billing_mode == BillingMode::UpstreamCost
                && (!crate::upstream_cost::supported_router(&rule.model)
                    || rule.speed != Speed::Standard
                    || rule.speed_multiplier_ppm != MULTIPLIER_ONE
                    || [
                        rule.rates.input,
                        rule.rates.output,
                        rule.rates.cache_read,
                        rule.rates.cache_write,
                        rule.rates.request,
                    ]
                    .iter()
                    .any(|v| *v != 0))
            {
                return Err(RoutingError::InvalidConfig("upstream-cost router price"));
            }
            let quote = PriceQuote {
                billing_mode: rule.billing_mode,
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
mod upstream_cost_tests {
    use super::*;
    #[test]
    fn upstream_cost_rules_reject_fixed_rates_unknown_models_and_speed_fees() {
        let mut rule = PriceRule {
            billing_mode: BillingMode::UpstreamCost,
            id: 1,
            model: "openrouter/fusion".into(),
            group: "default".into(),
            speed: Speed::Standard,
            rates: TokenRates {
                input: 0,
                output: 0,
                cache_read: 0,
                cache_write: 0,
                request: 0,
            },
            group_multiplier_ppm: 1_250_000,
            speed_multiplier_ppm: MULTIPLIER_ONE,
        };
        let compile = |rule: &PriceRule| {
            PriceSnapshot::compile(PriceConfig {
                version: 1,
                rules: vec![rule.clone()],
            })
        };
        let prices = compile(&rule).unwrap();
        let quote = prices.quote(&rule.model, &rule.group, rule.speed).unwrap();
        assert_eq!(quote.billing_mode(), BillingMode::UpstreamCost);
        assert!(quote.charge(Usage::default()).is_err());
        rule.rates.request = 1;
        assert!(compile(&rule).is_err());
        rule.rates.request = 0;
        rule.speed = Speed::Fast;
        assert!(compile(&rule).is_err());
        rule.speed = Speed::Standard;
        rule.model = "arbitrary-model".into();
        assert!(compile(&rule).is_err());
    }
}
