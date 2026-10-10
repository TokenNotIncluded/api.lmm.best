//! Version 1 routing decision contract for the Rust relay and control plane.
//!
//! This module performs no network forwarding, ledger writes, or Go calls.
//! See README.md for publication, retry, credential, and task 05/06 boundaries.

mod config;
mod engine;
mod pricing;
pub mod store;

pub use config::{
    CredentialRef, Endpoint, FailoverPolicy, ModelAlias, ModelGroup, ModelRoute, PayloadPolicy,
    Protocol, RetryCause, RouteTarget, RoutingConfig, Speed, UpstreamConfig,
};
pub use engine::{
    Attempt, DeliveryState, Failure, FailureKind, HealthStatus, PublicModel, ReleaseVersion,
    ReplayPermission, RoutePlan, RouteRequest, Router,
};
pub use pricing::{
    MAX_MULTIPLIER, MULTIPLIER_ONE, MicroUsd, PriceConfig, PriceQuote, PriceRule, PriceSnapshot,
    TokenRates, Usage,
};

pub const CONTRACT_VERSION: u32 = 1;

/// Stable, sanitized errors. Never attach upstream response text, URLs, or tokens.
#[derive(Clone, Debug, PartialEq, Eq)]
pub enum RoutingError {
    InvalidConfig(&'static str),
    InvalidRequest,
    ConflictingSpeed,
    VersionConflict,
    VersionNotIncreasing,
    PriceVersionConflict,
    UnknownGroup,
    GroupDisabled,
    UnknownModel,
    RouteDisabled,
    SpeedDisabled,
    MissingPrice,
    NoEligibleUpstream,
    RetryDisallowed,
    UnsafeReplay,
    ResponseAlreadyStarted,
    AttemptsExhausted,
    InvalidTransition,
    PriceOverflow,
    LockPoisoned,
}

impl std::fmt::Display for RoutingError {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        write!(f, "routing: {self:?}")
    }
}

impl std::error::Error for RoutingError {}

#[cfg(test)]
mod tests;
