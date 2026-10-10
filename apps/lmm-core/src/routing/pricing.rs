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

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct MicroUsd(pub u64);

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
