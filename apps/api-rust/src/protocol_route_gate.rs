//! Pure, closed-by-default admission for protocol routes.
//!
//! The gate is deliberately separate from router construction.  It combines
//! the validated runtime capability snapshot, deterministic rollout decision,
//! and exact route ownership evidence into one value that a future adapter can
//! inspect before a request or stream starts.  No handler is mounted and no
//! upstream call is made here.

use lmm_contracts::relay::{Direction, Fidelity, LossPolicy, Protocol, ValidatedRegistry};

use crate::{
    protocol_rollout::{FlagDecision, ProtocolRolloutConfig, RolloutContext, RolloutFlag},
    protocol_runtime_registry::{
        RuntimeCapabilityError, RuntimeRouteCapability, route_capability_from_validated,
    },
    route_ownership::{
        OwnershipBlocker, OwnershipDecision, OwnershipEvidence, OwnershipGate, RouteOwnershipScope,
    },
};

/// A closed set of reasons why a route cannot be admitted.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum RouteGateBlocker {
    /// The validated registry has no entry for the requested source/target.
    RouteUnavailable,
    /// The requested model family is excluded by the registry route.
    ModelConstraintMismatch,
    /// The requested direction is not wired by the registry/runtime snapshot.
    DirectionUnsupported,
    /// The registry explicitly marks the route as unsupported.
    RouteQualityUnsupported,
    /// A same-protocol route is not validated raw passthrough.
    NativeRawUnavailable,
    /// The conversion-engine v2 rollout decision is disabled for this scope.
    ConversionEngineDisabled,
    /// The rollout configuration failed its complete invariant validation.
    RolloutConfigInvalid,
    /// The configuration rollback switch is active.
    RolloutRollbackActive,
    /// Ownership evidence belongs to another exact route or stream scope.
    OwnershipScopeMismatch,
    /// A closed ownership-evidence condition prevented admission.
    Ownership(OwnershipBlocker),
    /// A capability lookup reported an invalid registry snapshot.
    RegistryUnavailable,
}

/// Metadata shared by every route-gate outcome.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RouteGateDetails {
    /// Exact source/target/stream scope evaluated by the gate.
    pub scope: RouteOwnershipScope,
    /// Loss policy which the eventual converter must apply.
    pub loss_policy: LossPolicy,
    /// Deterministic conversion-engine v2 decision for this request scope.
    pub flag_decision: FlagDecision,
    /// Validated capability, when the requested model/direction was available.
    pub capability: Option<RuntimeRouteCapability>,
}

/// Result of the pure protocol-route admission gate.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RouteGateDecision {
    /// Same-protocol bytes may use the validated native passthrough path.
    NativeRaw {
        /// Shared decision metadata.
        details: RouteGateDetails,
    },
    /// A cross-protocol converter may be selected by a later router boundary.
    CrossProtocol {
        /// Shared decision metadata.
        details: RouteGateDetails,
    },
    /// The route remains closed and must not be mounted or selected.
    Closed {
        /// Shared decision metadata, including any available capability.
        details: RouteGateDetails,
        /// Closed-set reasons for the decision.
        blockers: Vec<RouteGateBlocker>,
    },
}

impl RouteGateDecision {
    /// Returns metadata for this decision without exposing mutable state.
    #[must_use]
    pub const fn details(&self) -> &RouteGateDetails {
        match self {
            Self::NativeRaw { details }
            | Self::CrossProtocol { details }
            | Self::Closed { details, .. } => details,
        }
    }

    /// Returns the closed-set blockers, or an empty slice for an admitted route.
    #[must_use]
    pub fn blockers(&self) -> &[RouteGateBlocker] {
        match self {
            Self::Closed { blockers, .. } => blockers,
            Self::NativeRaw { .. } | Self::CrossProtocol { .. } => &[],
        }
    }

    /// Returns whether this decision keeps the route closed.
    #[must_use]
    pub const fn is_closed(&self) -> bool {
        matches!(self, Self::Closed { .. })
    }
}

/// Decides whether one validated route may be selected.
///
/// Native same-protocol routes require only a validated raw-passthrough
/// capability for the requested model and direction.  Cross-protocol routes
/// additionally require an enabled conversion-engine v2 flag and complete
/// ownership evidence for the exact source/target/stream scope.
#[must_use]
pub fn decide_route(
    config: &ProtocolRolloutConfig,
    context: &RolloutContext<'_>,
    registry: &ValidatedRegistry,
    direction: Direction,
    evidence: &OwnershipEvidence,
) -> RouteGateDecision {
    decide_route_with_ownership_gate(
        config,
        context,
        registry,
        direction,
        evidence,
        &OwnershipGate::default(),
    )
}

/// Variant of [`decide_route`] with an explicitly configured ownership gate.
///
/// This remains pure and is useful to callers that use a stricter canary
/// threshold for a particular deployment stage.
#[must_use]
pub fn decide_route_with_ownership_gate(
    config: &ProtocolRolloutConfig,
    context: &RolloutContext<'_>,
    registry: &ValidatedRegistry,
    direction: Direction,
    evidence: &OwnershipEvidence,
    ownership_gate: &OwnershipGate,
) -> RouteGateDecision {
    let scope = RouteOwnershipScope {
        source: context.source,
        target: context.target,
        stream: context.stream,
    };
    let flag_decision = config.decide(RolloutFlag::ConversionEngineV2, context);
    let (capability, mut blockers) = capability_for_scope(
        registry,
        context.source,
        context.target,
        context.model_family,
        direction,
    );
    if context.source == context.target {
        if capability
            .as_ref()
            .is_some_and(|value| value.quality != Fidelity::Exact || !value.raw_passthrough)
        {
            push_unique(&mut blockers, RouteGateBlocker::NativeRawUnavailable);
        }
        let details = RouteGateDetails {
            scope,
            loss_policy: config.loss_policy(),
            flag_decision,
            capability,
        };
        return if blockers.is_empty() {
            RouteGateDecision::NativeRaw { details }
        } else {
            RouteGateDecision::Closed { details, blockers }
        };
    }

    if !flag_decision.enabled {
        push_unique(&mut blockers, RouteGateBlocker::ConversionEngineDisabled);
    }
    if config.validate().is_err() {
        push_unique(&mut blockers, RouteGateBlocker::RolloutConfigInvalid);
    }
    if config.rollback_enabled() {
        push_unique(&mut blockers, RouteGateBlocker::RolloutRollbackActive);
    }
    if evidence.scope() != scope {
        push_unique(&mut blockers, RouteGateBlocker::OwnershipScopeMismatch);
    } else if let OwnershipDecision::ClosedByDefault {
        blockers: ownership_blockers,
        ..
    } = ownership_gate.evaluate(evidence)
    {
        for blocker in ownership_blockers {
            push_unique(&mut blockers, RouteGateBlocker::Ownership(blocker));
        }
    }

    let details = RouteGateDetails {
        scope,
        loss_policy: config.loss_policy(),
        flag_decision,
        capability,
    };
    if blockers.is_empty() {
        RouteGateDecision::CrossProtocol { details }
    } else {
        RouteGateDecision::Closed { details, blockers }
    }
}

fn capability_for_scope(
    registry: &ValidatedRegistry,
    source: Protocol,
    target: Protocol,
    model_family: &str,
    direction: Direction,
) -> (Option<RuntimeRouteCapability>, Vec<RouteGateBlocker>) {
    match route_capability_from_validated(registry, source, target, model_family, direction) {
        Ok(capability) => {
            let mut blockers = Vec::new();
            if !capability.quality.is_supported() {
                blockers.push(RouteGateBlocker::RouteQualityUnsupported);
            }
            (Some(capability), blockers)
        }
        Err(error) => {
            let mut blockers = vec![map_capability_error(error)];
            if registry
                .route(source, target)
                .is_some_and(|route| route.quality == Fidelity::Unsupported)
            {
                push_unique(&mut blockers, RouteGateBlocker::RouteQualityUnsupported);
            }
            (None, blockers)
        }
    }
}

fn map_capability_error(error: RuntimeCapabilityError) -> RouteGateBlocker {
    match error {
        RuntimeCapabilityError::Registry(_) => RouteGateBlocker::RegistryUnavailable,
        RuntimeCapabilityError::MissingRoute { .. } => RouteGateBlocker::RouteUnavailable,
        RuntimeCapabilityError::ModelConstraint { .. } => RouteGateBlocker::ModelConstraintMismatch,
        RuntimeCapabilityError::UnsupportedDirection { .. } => {
            RouteGateBlocker::DirectionUnsupported
        }
    }
}

fn push_unique(blockers: &mut Vec<RouteGateBlocker>, blocker: RouteGateBlocker) {
    if !blockers.contains(&blocker) {
        blockers.push(blocker);
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::{
        protocol_rollout::{FlagConfig, MAX_BASIS_POINTS, ShadowDifference, ShadowRecord},
        protocol_runtime_registry::validated_current_registry,
        route_ownership::{DifferentialClass, MIN_REVIEW_CANARY_BASIS_POINTS, OwnershipBlocker},
    };
    use lmm_contracts::relay::protocols;

    fn enabled_config() -> ProtocolRolloutConfig {
        ProtocolRolloutConfig {
            conversion_engine_v2: FlagConfig::enabled(MAX_BASIS_POINTS)
                .expect("full conversion rollout is bounded"),
            ..ProtocolRolloutConfig::default()
        }
    }

    fn context(source: Protocol, target: Protocol, stream: bool) -> RolloutContext<'static> {
        RolloutContext::new("route-gate-test", source, target, "test-model", stream)
    }

    fn complete_evidence(scope: RouteOwnershipScope) -> OwnershipEvidence {
        let mut evidence = OwnershipEvidence::closed(scope);
        for class in DifferentialClass::all() {
            evidence.mark_green(*class);
        }
        evidence.record_shadow(&ShadowRecord {
            source: scope.source,
            target: scope.target,
            stream: scope.stream,
            old_converter_id: Some("old-local".to_owned()),
            new_converter_id: Some("new-local".to_owned()),
            differences: Vec::<ShadowDifference>::new(),
        });
        evidence
            .set_canary_basis_points(MIN_REVIEW_CANARY_BASIS_POINTS)
            .expect("review canary is bounded");
        evidence.approve_rollout();
        evidence
    }

    #[test]
    fn current_sixteen_route_registry_keeps_cross_protocol_closed_without_trusted_evidence(
    ) {
        let registry = validated_current_registry().expect("current registry validates");
        let config = enabled_config();
        for source in protocols() {
            for target in protocols() {
                if source == target {
                    continue;
                }
                let request_context = context(source, target, false);
                let scope = RouteOwnershipScope {
                    source,
                    target,
                    stream: false,
                };
                let decision = decide_route(
                    &config,
                    &request_context,
                    &registry,
                    Direction::Response,
                    &complete_evidence(scope),
                );
                assert!(decision.is_closed(), "{source:?} -> {target:?}");
                assert!(
                    decision.blockers().iter().any(|blocker| matches!(
                        blocker,
                        RouteGateBlocker::Ownership(OwnershipBlocker::UntrustedEvidence)
                    ))
                );
            }
        }
    }

    #[test]
    fn validated_native_raw_routes_are_admitted_without_v2_or_ownership_evidence() {
        let registry = validated_current_registry().expect("current registry validates");
        for protocol in protocols() {
            let config = ProtocolRolloutConfig::default();
            let request_context = context(protocol, protocol, true);
            let scope = RouteOwnershipScope {
                source: protocol,
                target: protocol,
                stream: true,
            };
            let decision = decide_route(
                &config,
                &request_context,
                &registry,
                Direction::Stream,
                &OwnershipEvidence::closed(scope),
            );
            let RouteGateDecision::NativeRaw { details } = decision else {
                panic!("validated native raw route was not admitted for {protocol:?}");
            };
            assert_eq!(details.scope, scope);
            assert!(
                details
                    .capability
                    .as_ref()
                    .is_some_and(|capability| capability.raw_passthrough)
            );
        }
    }

    #[test]
    fn ownership_scope_mismatch_closes_a_cross_protocol_route() {
        let registry = validated_current_registry().expect("current registry validates");
        let config = enabled_config();
        let request_context = context(Protocol::OpenAi, Protocol::Claude, false);
        let wrong_scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: true,
        };
        let decision = decide_route(
            &config,
            &request_context,
            &registry,
            Direction::Response,
            &complete_evidence(wrong_scope),
        );
        assert!(decision.is_closed());
        assert!(
            decision
                .blockers()
                .contains(&RouteGateBlocker::OwnershipScopeMismatch)
        );
    }

    #[test]
    fn configuration_rollback_closes_even_complete_cross_protocol_evidence() {
        let registry = validated_current_registry().expect("current registry validates");
        let mut config = enabled_config();
        config.rollback = true;
        let request_context = context(Protocol::OpenAi, Protocol::Claude, false);
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: false,
        };
        let decision = decide_route(
            &config,
            &request_context,
            &registry,
            Direction::Response,
            &complete_evidence(scope),
        );
        assert!(decision.is_closed());
        assert!(
            decision
                .blockers()
                .contains(&RouteGateBlocker::RolloutRollbackActive)
        );
        assert!(
            decision
                .blockers()
                .contains(&RouteGateBlocker::ConversionEngineDisabled)
        );
    }

    #[test]
    fn ownership_rollback_closes_even_complete_cross_protocol_evidence() {
        let registry = validated_current_registry().expect("current registry validates");
        let config = enabled_config();
        let request_context = context(Protocol::OpenAi, Protocol::Claude, false);
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: false,
        };
        let mut evidence = complete_evidence(scope);
        evidence.set_rollback_active(true);
        let decision = decide_route(
            &config,
            &request_context,
            &registry,
            Direction::Response,
            &evidence,
        );
        assert!(decision.is_closed());
        assert!(decision.blockers().contains(&RouteGateBlocker::Ownership(
            OwnershipBlocker::RollbackActive
        )));
    }

    #[test]
    fn incomplete_exact_cross_protocol_evidence_remains_closed() {
        let registry = validated_current_registry().expect("current registry validates");
        let config = enabled_config();
        let request_context = context(Protocol::OpenAi, Protocol::Claude, false);
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: false,
        };
        let decision = decide_route(
            &config,
            &request_context,
            &registry,
            Direction::Response,
            &OwnershipEvidence::closed(scope),
        );
        assert!(decision.is_closed());
        assert!(decision.blockers().contains(&RouteGateBlocker::Ownership(
            OwnershipBlocker::DifferentialNotGreen
        )));
        assert!(decision.blockers().contains(&RouteGateBlocker::Ownership(
            OwnershipBlocker::ShadowDifference
        )));
        assert!(decision.blockers().contains(&RouteGateBlocker::Ownership(
            OwnershipBlocker::CanaryBelowMinimum
        )));
        assert!(decision.blockers().contains(&RouteGateBlocker::Ownership(
            OwnershipBlocker::RolloutNotApproved
        )));
    }

    #[test]
    fn invalid_rollout_configuration_cannot_admit_cross_protocol_traffic() {
        let registry = validated_current_registry().expect("current registry validates");
        let mut config = enabled_config();
        config
            .conversion_engine_v2
            .overrides
            .push(crate::protocol_rollout::FlagOverride {
                selector: crate::protocol_rollout::RolloutSelector {
                    model_family: Some(String::new()),
                    ..crate::protocol_rollout::RolloutSelector::default()
                },
                enabled: true,
                canary_basis_points: MAX_BASIS_POINTS,
            });
        let request_context = context(Protocol::OpenAi, Protocol::Claude, false);
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: false,
        };

        let decision = decide_route(
            &config,
            &request_context,
            &registry,
            Direction::Response,
            &complete_evidence(scope),
        );

        assert!(decision.is_closed());
        assert!(
            decision
                .blockers()
                .contains(&RouteGateBlocker::RolloutConfigInvalid)
        );
    }
}
