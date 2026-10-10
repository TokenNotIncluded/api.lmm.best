use std::collections::{HashMap, HashSet};
use std::fmt;
use std::sync::atomic::{AtomicBool, AtomicU8, Ordering};
use std::sync::{Arc, RwLock};

use rand::Rng;
use serde::Serialize;

use super::config::{
    MAX_ATTEMPTS, MAX_ROUTES, MAX_TARGETS_PER_ROUTE, requested_model, valid_group, valid_id,
    valid_model,
};
use super::{
    CredentialRef, ModelRoute, PayloadPolicy, PriceQuote, PriceSnapshot, Protocol, RetryCause,
    RouteTarget, RoutingConfig, RoutingError, Speed, UpstreamConfig,
};

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct ReleaseVersion {
    pub routing: u64,
    pub pricing: u64,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
#[repr(u8)]
pub enum HealthStatus {
    Unknown = 0,
    Healthy = 1,
    Unhealthy = 2,
}

struct RuntimeHealth {
    status: AtomicU8,
    enabled: AtomicBool,
}

impl RuntimeHealth {
    fn available(&self) -> bool {
        self.enabled.load(Ordering::Acquire)
            && self.status.load(Ordering::Acquire) == HealthStatus::Healthy as u8
    }
}

struct Upstream {
    config: UpstreamConfig,
    health: Arc<RuntimeHealth>,
}

struct Target {
    config: RouteTarget,
    upstream: Arc<Upstream>,
}

impl Target {
    fn available(&self) -> bool {
        self.config.enabled
            && self.config.weight > 0
            && self.upstream.config.enabled
            && self.upstream.health.available()
    }
}

struct Route {
    config: ModelRoute,
    targets: Vec<Arc<Target>>,
}

struct Group {
    enabled: bool,
    routes: HashMap<String, Arc<Route>>,
    aliases: HashMap<String, String>,
}

struct Snapshot {
    version: ReleaseVersion,
    prices: Arc<PriceSnapshot>,
    upstreams: HashMap<u64, Arc<Upstream>>,
    groups: HashMap<String, Group>,
}

impl Snapshot {
    fn compile(
        config: RoutingConfig,
        prices: Arc<PriceSnapshot>,
        previous: Option<&Self>,
    ) -> Result<Self, RoutingError> {
        if !valid_id(config.version)
            || config.routes.len() > MAX_ROUTES
            || config.upstreams.len() > MAX_ROUTES
            || config.groups.len() > MAX_ROUTES
            || config.aliases.len() > MAX_ROUTES * 4
        {
            return Err(RoutingError::InvalidConfig("routing version or size"));
        }
        let mut groups = HashMap::new();
        for group in config.groups {
            if !valid_group(&group.name)
                || groups
                    .insert(
                        group.name,
                        Group {
                            enabled: group.enabled,
                            routes: HashMap::new(),
                            aliases: HashMap::new(),
                        },
                    )
                    .is_some()
            {
                return Err(RoutingError::InvalidConfig("group"));
            }
        }
        let mut upstreams = HashMap::new();
        for upstream in config.upstreams {
            if !valid_id(upstream.id)
                || !valid_id(upstream.credential.id)
                || !valid_id(upstream.credential.revision)
                || upstreams.contains_key(&upstream.id)
            {
                return Err(RoutingError::InvalidConfig("upstream or credential reference"));
            }
            let health = previous
                .and_then(|old| old.upstreams.get(&upstream.id))
                .filter(|old| old.config.same_destination(&upstream))
                .map(|old| Arc::clone(&old.health))
                .unwrap_or_else(|| {
                    Arc::new(RuntimeHealth {
                        // Optimistic bootstrap. Rust probes can explicitly set Unknown.
                        status: AtomicU8::new(HealthStatus::Healthy as u8),
                        enabled: AtomicBool::new(true),
                    })
                });
            upstreams.insert(
                upstream.id,
                Arc::new(Upstream {
                    config: upstream,
                    health,
                }),
            );
        }
        let mut route_ids = HashSet::new();
        let mut target_ids = HashSet::new();
        for route in config.routes {
            let unique_speeds: HashSet<_> = route.speeds.iter().copied().collect();
            let retry_causes: HashSet<_> = route.failover.retry_on.iter().copied().collect();
            if !valid_id(route.id)
                || !route_ids.insert(route.id)
                || !valid_model(&route.model)
                || !valid_group(&route.group)
                || unique_speeds.is_empty()
                || unique_speeds.len() != route.speeds.len()
                || route.targets.len() > MAX_TARGETS_PER_ROUTE
                || !(1..=MAX_ATTEMPTS).contains(&route.failover.max_attempts)
                || retry_causes.len() != route.failover.retry_on.len()
            {
                return Err(RoutingError::InvalidConfig("route"));
            }
            let group = groups
                .get_mut(&route.group)
                .ok_or(RoutingError::InvalidConfig("route group"))?;
            if group.routes.contains_key(&route.model) {
                return Err(RoutingError::InvalidConfig("duplicate route"));
            }
            if route.enabled
                && route
                    .speeds
                    .iter()
                    .any(|&speed| prices.quote(&route.model, &route.group, speed).is_none())
            {
                return Err(RoutingError::MissingPrice);
            }
            let mut targets = Vec::with_capacity(route.targets.len());
            let mut destinations = HashSet::new();
            for target in &route.targets {
                let target_speeds: HashSet<_> = target.speeds.iter().copied().collect();
                if !valid_id(target.id)
                    || !target_ids.insert(target.id)
                    || !destinations.insert(target.upstream_id)
                    || !valid_model(&target.upstream_model)
                    || target_speeds.is_empty()
                    || target_speeds.len() != target.speeds.len()
                    || !target_speeds.is_subset(&unique_speeds)
                    || !(1..=3_600_000).contains(&target.timeout_ms)
                {
                    return Err(RoutingError::InvalidConfig("route target"));
                }
                let upstream = upstreams
                    .get(&target.upstream_id)
                    .ok_or(RoutingError::InvalidConfig("target upstream"))?;
                targets.push(Arc::new(Target {
                    config: target.clone(),
                    upstream: Arc::clone(upstream),
                }));
            }
            targets.sort_by_key(|target| target.config.priority);
            group.routes.insert(route.model.clone(), Arc::new(Route { config: route, targets }));
        }
        for alias in config.aliases {
            if !valid_model(&alias.alias) || !valid_model(&alias.model) {
                return Err(RoutingError::InvalidConfig("alias"));
            }
            let group = groups
                .get_mut(&alias.group)
                .ok_or(RoutingError::InvalidConfig("alias group"))?;
            if group.routes.contains_key(&alias.alias)
                || !group.routes.contains_key(&alias.model)
                || group.aliases.insert(alias.alias, alias.model).is_some()
            {
                return Err(RoutingError::InvalidConfig("alias collision or chain"));
            }
        }
        Ok(Self {
            version: ReleaseVersion { routing: config.version, pricing: prices.version() },
            prices,
            upstreams,
            groups,
        })
    }
}

/// One short read lock per new request. No locks are held while selecting or sending.
/// Price and routing state ALWAYS switch together, never through two pointers.
pub struct Router {
    active: RwLock<Arc<Snapshot>>,
}

impl Router {
    pub fn new(config: RoutingConfig, prices: Arc<PriceSnapshot>) -> Result<Self, RoutingError> {
        Ok(Self { active: RwLock::new(Arc::new(Snapshot::compile(config, prices, None)?)) })
    }

    fn snapshot(&self) -> Result<Arc<Snapshot>, RoutingError> {
        self.active
            .read()
            .map(|active| Arc::clone(&active))
            .map_err(|_| RoutingError::LockPoisoned)
    }

    pub fn version(&self) -> Result<ReleaseVersion, RoutingError> {
        Ok(self.snapshot()?.version)
    }

    /// Validate outside the write lock, then compare-and-swap the complete release.
    /// To roll back content, publish it with NEW version numbers.
    pub fn publish(
        &self,
        expected: ReleaseVersion,
        config: RoutingConfig,
        prices: Arc<PriceSnapshot>,
    ) -> Result<ReleaseVersion, RoutingError> {
        let previous = self.snapshot()?;
        if previous.version != expected {
            return Err(RoutingError::VersionConflict);
        }
        if config.version <= expected.routing {
            return Err(RoutingError::VersionNotIncreasing);
        }
        if prices.version() < expected.pricing
            || (prices.version() == expected.pricing && !prices.same_content(&previous.prices))
        {
            return Err(RoutingError::PriceVersionConflict);
        }
        let next = Arc::new(Snapshot::compile(config, prices, Some(&previous))?);
        let mut active = self.active.write().map_err(|_| RoutingError::LockPoisoned)?;
        if active.version != expected {
            return Err(RoutingError::VersionConflict);
        }
        let version = next.version;
        // Drop retired allocations outside the publication lock.
        let retired = std::mem::replace(&mut *active, next);
        drop(active);
        drop(retired);
        Ok(version)
    }

    /// Call with an authenticated group from task 01/03, not a raw client group header.
    pub fn route(&self, request: RouteRequest<'_>) -> Result<RoutePlan, RoutingError> {
        self.route_with_rng(request, &mut rand::thread_rng())
    }

    pub(super) fn route_with_rng<R: Rng + ?Sized>(
        &self,
        request: RouteRequest<'_>,
        rng: &mut R,
    ) -> Result<RoutePlan, RoutingError> {
        if !valid_group(request.group) {
            return Err(RoutingError::InvalidRequest);
        }
        let (name, speed) = requested_model(request.model, request.speed)?;
        let snapshot = self.snapshot()?;
        let group = snapshot.groups.get(request.group).ok_or(RoutingError::UnknownGroup)?;
        if !group.enabled {
            return Err(RoutingError::GroupDisabled);
        }
        let canonical = group.aliases.get(name).map_or(name, String::as_str);
        let route = group.routes.get(canonical).ok_or(RoutingError::UnknownModel)?;
        if !route.config.enabled {
            return Err(RoutingError::RouteDisabled);
        }
        if !route.config.speeds.contains(&speed) {
            return Err(RoutingError::SpeedDisabled);
        }
        let quote = snapshot
            .prices
            .quote(canonical, request.group, speed)
            .ok_or(RoutingError::MissingPrice)?;
        let mut pool: Vec<_> = route
            .targets
            .iter()
            .filter(|target| {
                target.available()
                    && target.config.speeds.contains(&speed)
                    && (target.config.payload == PayloadPolicy::Adapt
                        || (target.upstream.config.protocol == request.protocol
                            && target.config.upstream_model == request.model))
            })
            .cloned()
            .collect();
        if pool.is_empty() {
            return Err(RoutingError::NoEligibleUpstream);
        }
        let capacity = usize::from(route.config.failover.max_attempts).min(pool.len());
        let mut candidates = Vec::with_capacity(capacity);
        for _ in 0..capacity {
            let priority = pool[0].config.priority;
            let total: u64 = pool
                .iter()
                .take_while(|target| target.config.priority == priority)
                .map(|target| u64::from(target.config.weight))
                .sum();
            let mut ticket = rng.gen_range(0..total);
            let mut selected = 0;
            for (index, target) in pool.iter().enumerate() {
                let weight = u64::from(target.config.weight);
                if ticket < weight {
                    selected = index;
                    break;
                }
                ticket -= weight;
            }
            candidates.push(pool.remove(selected));
        }
        let route = Arc::clone(route);
        Ok(RoutePlan {
            snapshot,
            route,
            quote,
            candidates,
            requested_model: request.model.into(),
            speed,
            replay: request.replay,
            next_index: 0,
            attempts: 0,
            current: None,
            stopped: false,
        })
    }

    /// Rust health probes use a release lease; stale probes cannot revive new endpoints.
    pub fn set_health(
        &self,
        expected: ReleaseVersion,
        upstream_id: u64,
        health: HealthStatus,
    ) -> Result<(), RoutingError> {
        let active = self.active.read().map_err(|_| RoutingError::LockPoisoned)?;
        if active.version != expected {
            return Err(RoutingError::VersionConflict);
        }
        let upstream = active.upstreams.get(&upstream_id).ok_or(RoutingError::NoEligibleUpstream)?;
        upstream.health.status.store(health as u8, Ordering::Release);
        Ok(())
    }

    /// Emergency operational stop, including not-yet-sent attempts in existing plans.
    /// Health reports never clear this flag. It survives route/price-only publication.
    pub fn set_operational_enabled(
        &self,
        expected: ReleaseVersion,
        upstream_id: u64,
        enabled: bool,
    ) -> Result<(), RoutingError> {
        let active = self.active.read().map_err(|_| RoutingError::LockPoisoned)?;
        if active.version != expected {
            return Err(RoutingError::VersionConflict);
        }
        let upstream = active.upstreams.get(&upstream_id).ok_or(RoutingError::NoEligibleUpstream)?;
        upstream.health.enabled.store(enabled, Ordering::Release);
        Ok(())
    }

    /// The only public configuration projection. Contains no endpoints or credentials.
    pub fn catalog(&self, authorized_group: &str) -> Result<Vec<PublicModel>, RoutingError> {
        let active = self.snapshot()?;
        let group = active.groups.get(authorized_group).ok_or(RoutingError::UnknownGroup)?;
        if !group.enabled {
            return Err(RoutingError::GroupDisabled);
        }
        let mut models = Vec::new();
        for route in group.routes.values().filter(|route| route.config.enabled) {
            models.push(PublicModel {
                name: route.config.model.clone(),
                canonical_model: route.config.model.clone(),
                speeds: route.config.speeds.clone(),
                routing_version: active.version.routing,
                price_version: active.version.pricing,
            });
        }
        for (alias, canonical) in &group.aliases {
            if let Some(route) = group.routes.get(canonical).filter(|route| route.config.enabled) {
                models.push(PublicModel {
                    name: alias.clone(),
                    canonical_model: canonical.clone(),
                    speeds: route.config.speeds.clone(),
                    routing_version: active.version.routing,
                    price_version: active.version.pricing,
                });
            }
        }
        models.sort_by(|a, b| a.name.cmp(&b.name));
        Ok(models)
    }
}

#[derive(Clone, Copy, Debug, Default, PartialEq, Eq)]
pub enum ReplayPermission {
    #[default]
    Denied,
    /// Granted by the caller's explicit replay/idempotency contract, never inferred.
    ExplicitlyAllowed,
}

#[derive(Clone, Copy, Debug)]
pub struct RouteRequest<'a> {
    pub model: &'a str,
    pub group: &'a str,
    /// Prefer a path/header speed with an unchanged model body for passthrough.
    pub speed: Option<Speed>,
    pub protocol: Protocol,
    pub replay: ReplayPermission,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum DeliveryState {
    NotSent,
    /// Provider definitively rejected the request without accepting work.
    Rejected,
    PossiblyAccepted,
    /// Any response body has been exposed downstream, including streaming output.
    ResponseStarted,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum FailureKind {
    Connect,
    Timeout,
    RateLimited,
    ServerError,
    Authentication,
    ClientError,
    InvalidResponse,
    Cancelled,
}

impl FailureKind {
    fn retry_cause(self) -> Option<RetryCause> {
        match self {
            Self::Connect => Some(RetryCause::Connect),
            Self::Timeout => Some(RetryCause::Timeout),
            Self::RateLimited => Some(RetryCause::RateLimited),
            Self::ServerError => Some(RetryCause::ServerError),
            _ => None,
        }
    }
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub struct Failure {
    pub kind: FailureKind,
    pub delivery: DeliveryState,
}

/// A request owns this plan until completion. Do not call Router::route on retry.
/// Routing version, price, model, group, speed and all allowed candidates are pinned.
pub struct RoutePlan {
    snapshot: Arc<Snapshot>,
    route: Arc<Route>,
    quote: PriceQuote,
    candidates: Vec<Arc<Target>>,
    requested_model: Box<str>,
    speed: Speed,
    replay: ReplayPermission,
    next_index: usize,
    attempts: u8,
    current: Option<usize>,
    stopped: bool,
}

impl RoutePlan {
    pub fn version(&self) -> ReleaseVersion {
        self.snapshot.version
    }

    pub fn quote(&self) -> PriceQuote {
        self.quote
    }

    pub fn canonical_model(&self) -> &str {
        &self.route.config.model
    }

    pub fn requested_model(&self) -> &str {
        &self.requested_model
    }

    pub fn group(&self) -> &str {
        &self.route.config.group
    }

    pub fn speed(&self) -> Speed {
        self.speed
    }

    pub fn start(&mut self) -> Result<Attempt, RoutingError> {
        if self.stopped || self.current.is_some() {
            return Err(RoutingError::InvalidTransition);
        }
        self.dispatch_next()
    }

    pub fn retry(&mut self, failure: Failure) -> Result<Attempt, RoutingError> {
        if self.stopped || self.current.take().is_none() {
            return Err(RoutingError::InvalidTransition);
        }
        // Make every rejection terminal. A second call cannot bypass a denied retry.
        self.stopped = true;
        if failure.delivery == DeliveryState::ResponseStarted {
            return Err(RoutingError::ResponseAlreadyStarted);
        }
        let permitted = failure
            .kind
            .retry_cause()
            .is_some_and(|cause| self.route.config.failover.retry_on.contains(&cause));
        if !permitted {
            return Err(RoutingError::RetryDisallowed);
        }
        if failure.delivery == DeliveryState::PossiblyAccepted
            && !(self.route.config.failover.allow_replay_after_send
                && self.replay == ReplayPermission::ExplicitlyAllowed)
        {
            return Err(RoutingError::UnsafeReplay);
        }
        self.stopped = false;
        self.dispatch_next()
    }

    fn dispatch_next(&mut self) -> Result<Attempt, RoutingError> {
        while self.next_index < self.candidates.len() {
            let index = self.next_index;
            self.next_index += 1;
            let target = &self.candidates[index];
            // This can REMOVE a candidate. It can never add or re-resolve a route.
            if !target.available() {
                continue;
            }
            self.attempts += 1;
            self.current = Some(index);
            return Ok(Attempt {
                target: Arc::clone(target),
                route: Arc::clone(&self.route),
                version: self.snapshot.version,
                quote: self.quote,
                number: self.attempts,
                speed: self.speed,
            });
        }
        self.stopped = true;
        Err(RoutingError::AttemptsExhausted)
    }
}

impl fmt::Debug for RoutePlan {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("RoutePlan")
            .field("version", &self.version())
            .field("route_id", &self.route.config.id)
            .field("speed", &self.speed)
            .field("attempts", &self.attempts)
            .finish_non_exhaustive()
    }
}

/// Private fields prevent a relay from accidentally rebuilding an unapproved target.
/// No Serialize implementation: never send this object to a public response or log.
pub struct Attempt {
    target: Arc<Target>,
    route: Arc<Route>,
    version: ReleaseVersion,
    quote: PriceQuote,
    number: u8,
    speed: Speed,
}

impl Attempt {
    pub fn version(&self) -> ReleaseVersion { self.version }
    pub fn quote(&self) -> PriceQuote { self.quote }
    pub fn number(&self) -> u8 { self.number }
    pub fn speed(&self) -> Speed { self.speed }
    pub fn route_id(&self) -> u64 { self.route.config.id }
    pub fn target_id(&self) -> u64 { self.target.config.id }
    pub fn upstream_id(&self) -> u64 { self.target.upstream.config.id }
    pub fn canonical_model(&self) -> &str { &self.route.config.model }
    pub fn group(&self) -> &str { &self.route.config.group }
    pub fn endpoint(&self) -> &str { self.target.upstream.config.endpoint.as_str() }
    pub fn upstream_model(&self) -> &str { &self.target.config.upstream_model }
    pub fn protocol(&self) -> Protocol { self.target.upstream.config.protocol }
    pub fn payload_policy(&self) -> PayloadPolicy { self.target.config.payload }
    pub fn credential_ref(&self) -> CredentialRef { self.target.upstream.config.credential }
    pub fn timeout_ms(&self) -> u32 { self.target.config.timeout_ms }

    /// Check again immediately before send; no API can revoke bytes already sent.
    pub fn is_available(&self) -> bool { self.target.available() }

    /// Infrastructure failures may close this destination. Reopening requires a Rust probe.
    /// A retired credential/endpoint has separate state and cannot poison its replacement.
    pub fn mark_unhealthy(&self) {
        self.target.upstream.health.status.store(HealthStatus::Unhealthy as u8, Ordering::Release);
    }
}

impl fmt::Debug for Attempt {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        f.debug_struct("Attempt")
            .field("version", &self.version)
            .field("route_id", &self.route_id())
            .field("target_id", &self.target_id())
            .field("upstream_id", &self.upstream_id())
            .field("number", &self.number)
            .finish_non_exhaustive()
    }
}

#[derive(Clone, Debug, Serialize, PartialEq, Eq)]
pub struct PublicModel {
    pub name: String,
    pub canonical_model: String,
    pub speeds: Vec<Speed>,
    pub routing_version: u64,
    pub price_version: u64,
}
