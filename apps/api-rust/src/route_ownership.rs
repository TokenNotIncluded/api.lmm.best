//! Closed-by-default Rust ownership eligibility evidence.
//!
//! This module records a route-specific readiness decision without mounting a
//! handler or transferring production traffic.  A caller must prove every
//! required Go-vs-Rust differential class, an identical shadow result, a
//! minimum canary allocation, and an explicit rollout approval before the
//! result can become eligible for an independent ownership review.

use std::{collections::BTreeSet, error::Error, fmt};

use lmm_contracts::relay::{Fidelity, Protocol, ValidatedRegistry};
use serde::{Deserialize, Serialize};

use crate::protocol_rollout::ShadowRecord;

/// Maximum canary allocation represented in basis points.
pub const MAX_CANARY_BASIS_POINTS: u16 = 10_000;

/// Minimum canary allocation required before an ownership review can open.
pub const MIN_REVIEW_CANARY_BASIS_POINTS: u16 = 100;

/// Differential classes which must all be green for one route.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DifferentialClass {
    /// Non-streaming request/response behavior.
    NonStream,
    /// Streaming framing, ordering, and termination behavior.
    Stream,
    /// Error status and error-body behavior.
    Error,
    /// Usage, cache, and billing semantics.
    UsageBilling,
    /// Client disconnect and cancellation behavior.
    ClientAbort,
}

impl DifferentialClass {
    /// Stable declaration-order list of required differential classes.
    pub const ALL: [Self; 5] = [
        Self::NonStream,
        Self::Stream,
        Self::Error,
        Self::UsageBilling,
        Self::ClientAbort,
    ];

    /// Returns every class in deterministic gate order.
    pub const fn all() -> &'static [Self] {
        &Self::ALL
    }
}

/// A route identity attached to ownership evidence.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct RouteOwnershipScope {
    /// Protocol accepted by the candidate route.
    pub source: Protocol,
    /// Protocol emitted by the candidate route.
    pub target: Protocol,
    /// Whether the route's contract includes a streaming response.
    pub stream: bool,
}

/// Safe reasons why a route remains closed to ownership review.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum OwnershipBlocker {
    /// One or more differential classes have not passed.
    DifferentialNotGreen,
    /// Shadow conversion did not produce identical body-free summaries.
    ShadowDifference,
    /// Canary allocation is below the review threshold.
    CanaryBelowMinimum,
    /// A human/operator rollout approval has not been recorded.
    RolloutNotApproved,
    /// Evidence contains a canary value outside the closed range.
    InvalidCanary,
    /// An emergency rollback configuration is active.
    RollbackActive,
    /// The route is absent from the validated capability matrix.
    RouteUnavailable,
    /// The route does not claim all directions required by this scope.
    RouteDirectionUnsupported,
    /// The route quality is explicitly unsupported.
    RouteQualityUnsupported,
    /// The model family is excluded by the route constraint.
    ModelConstraintMismatch,
    /// Cross-protocol evidence was assembled without a trusted attestation.
    UntrustedEvidence,
}

/// Evidence collected for one specific source/target route.
#[derive(Clone, Eq, PartialEq, Serialize)]
pub struct OwnershipEvidence {
    scope: RouteOwnershipScope,
    green: BTreeSet<DifferentialClass>,
    shadow_identical: bool,
    canary_basis_points: u16,
    rollout_approved: bool,
    rollback_active: bool,
    #[serde(skip)]
    trusted_attestation: bool,
}

impl fmt::Debug for OwnershipEvidence {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("OwnershipEvidence")
            .field("scope", &self.scope)
            .field("green", &self.green)
            .field("shadow_identical", &self.shadow_identical)
            .field("canary_basis_points", &self.canary_basis_points)
            .field("rollout_approved", &self.rollout_approved)
            .field("rollback_active", &self.rollback_active)
            .field("trusted_attestation", &self.trusted_attestation)
            .finish()
    }
}

impl OwnershipEvidence {
    /// Creates closed evidence for one route. No class or approval is green.
    #[must_use]
    pub fn closed(scope: RouteOwnershipScope) -> Self {
        Self {
            scope,
            green: BTreeSet::new(),
            shadow_identical: false,
            canary_basis_points: 0,
            rollout_approved: false,
            rollback_active: false,
            trusted_attestation: false,
        }
    }

    /// Returns the route identity attached to this evidence.
    #[must_use]
    pub const fn scope(&self) -> RouteOwnershipScope {
        self.scope
    }

    /// Marks one differential class green after its external comparison passes.
    pub fn mark_green(&mut self, class: DifferentialClass) {
        self.trusted_attestation = false;
        self.green.insert(class);
    }

    /// Returns whether a differential class has passed.
    #[must_use]
    pub fn is_green(&self, class: DifferentialClass) -> bool {
        self.green.contains(&class)
    }

    /// Records whether body-free old/new shadow summaries were identical.
    pub fn set_shadow_identical(&mut self, identical: bool) {
        self.trusted_attestation = false;
        self.shadow_identical = identical;
    }

    /// Records a body-free local shadow result for this exact route.
    ///
    /// Both converters must succeed for the same source/target/stream scope.
    /// Scope mismatches and matching failures therefore fail closed instead of
    /// trusting an empty difference list as positive evidence.
    pub fn record_shadow(&mut self, shadow: &ShadowRecord) {
        self.set_shadow_identical(
            shadow.source == self.scope.source
                && shadow.target == self.scope.target
                && shadow.stream == self.scope.stream
                && shadow.old_converter_id.is_some()
                && shadow.new_converter_id.is_some()
                && shadow.is_identical(),
        );
    }

    /// Records a bounded canary allocation.
    pub fn set_canary_basis_points(
        &mut self,
        basis_points: u16,
    ) -> Result<(), OwnershipEvidenceError> {
        if basis_points > MAX_CANARY_BASIS_POINTS {
            return Err(OwnershipEvidenceError::InvalidCanary { basis_points });
        }
        self.trusted_attestation = false;
        self.canary_basis_points = basis_points;
        Ok(())
    }

    /// Records the explicit operator approval required by the gate.
    pub fn approve_rollout(&mut self) {
        self.trusted_attestation = false;
        self.rollout_approved = true;
    }

    /// Returns the classes currently recorded as green.
    #[must_use]
    pub fn green_classes(&self) -> &BTreeSet<DifferentialClass> {
        &self.green
    }

    /// Returns the canary allocation in basis points.
    #[must_use]
    pub const fn canary_basis_points(&self) -> u16 {
        self.canary_basis_points
    }

    /// Returns whether explicit rollout approval was recorded.
    #[must_use]
    pub const fn rollout_approved(&self) -> bool {
        self.rollout_approved
    }

    /// Records that a configuration rollback is active for this route.
    pub fn set_rollback_active(&mut self, active: bool) {
        self.trusted_attestation = false;
        self.rollback_active = active;
    }

    /// Returns whether a rollback configuration is active.
    #[must_use]
    pub const fn rollback_active(&self) -> bool {
        self.rollback_active
    }

    /// Seals evidence after the differential trust policy and signature have
    /// passed.  This crate-private seam is intentionally the only way to set
    /// the trusted marker; public evidence mutators remain untrusted.
    pub(crate) fn seal_trusted(&mut self) {
        self.trusted_attestation = true;
    }
}

/// Configures the minimum proof required for one ownership review.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct OwnershipGate {
    minimum_canary_basis_points: u16,
}

impl Default for OwnershipGate {
    fn default() -> Self {
        Self {
            minimum_canary_basis_points: MIN_REVIEW_CANARY_BASIS_POINTS,
        }
    }
}

impl OwnershipGate {
    /// Creates a gate with a bounded canary threshold.
    pub fn new(minimum_canary_basis_points: u16) -> Result<Self, OwnershipEvidenceError> {
        if minimum_canary_basis_points > MAX_CANARY_BASIS_POINTS {
            return Err(OwnershipEvidenceError::InvalidCanary {
                basis_points: minimum_canary_basis_points,
            });
        }
        Ok(Self {
            minimum_canary_basis_points,
        })
    }

    /// Evaluates evidence without mounting or taking ownership of any route.
    #[must_use]
    pub fn evaluate(&self, evidence: &OwnershipEvidence) -> OwnershipDecision {
        let mut blockers = Vec::new();
        if evidence.canary_basis_points > MAX_CANARY_BASIS_POINTS {
            blockers.push(OwnershipBlocker::InvalidCanary);
        }
        if evidence.rollback_active {
            blockers.push(OwnershipBlocker::RollbackActive);
        }
        if evidence.scope.source != evidence.scope.target && !evidence.trusted_attestation {
            blockers.push(OwnershipBlocker::UntrustedEvidence);
        }
        if DifferentialClass::all()
            .iter()
            .any(|class| !evidence.green.contains(class))
        {
            blockers.push(OwnershipBlocker::DifferentialNotGreen);
        }
        if !evidence.shadow_identical {
            blockers.push(OwnershipBlocker::ShadowDifference);
        }
        if evidence.canary_basis_points < self.minimum_canary_basis_points {
            blockers.push(OwnershipBlocker::CanaryBelowMinimum);
        }
        if !evidence.rollout_approved {
            blockers.push(OwnershipBlocker::RolloutNotApproved);
        }
        if blockers.is_empty() {
            OwnershipDecision::EligibleForOwnershipReview {
                scope: evidence.scope,
            }
        } else {
            OwnershipDecision::ClosedByDefault {
                scope: evidence.scope,
                blockers,
            }
        }
    }

    /// Evaluates evidence plus the validated registry/model capability.
    ///
    /// This screens evidence before an ownership review; it does not itself
    /// authorize mounting or selecting a business route. A complete
    /// differential and canary proof cannot qualify a route that the registry
    /// marks unsupported, that lacks a required request/response/stream
    /// direction, or that excludes the requested model family.
    #[must_use]
    pub fn evaluate_with_registry(
        &self,
        evidence: &OwnershipEvidence,
        registry: &ValidatedRegistry,
        model_family: &str,
    ) -> OwnershipDecision {
        let mut blockers = match self.evaluate(evidence) {
            OwnershipDecision::ClosedByDefault { blockers, .. } => blockers,
            OwnershipDecision::EligibleForOwnershipReview { .. } => Vec::new(),
        };
        let scope = evidence.scope;
        let Some(route) = registry.route(scope.source, scope.target) else {
            blockers.push(OwnershipBlocker::RouteUnavailable);
            return OwnershipDecision::ClosedByDefault { scope, blockers };
        };
        if !route.matches_model_family(model_family) {
            blockers.push(OwnershipBlocker::ModelConstraintMismatch);
        }
        if route.quality == Fidelity::Unsupported {
            blockers.push(OwnershipBlocker::RouteQualityUnsupported);
        }
        if !route.request_supported
            || !route.response_supported
            || (scope.stream && !route.stream_supported)
        {
            blockers.push(OwnershipBlocker::RouteDirectionUnsupported);
        }
        if blockers.is_empty() {
            OwnershipDecision::EligibleForOwnershipReview { scope }
        } else {
            OwnershipDecision::ClosedByDefault { scope, blockers }
        }
    }

    /// Returns whether the route is eligible to be reviewed for ownership.
    ///
    /// The method is deliberately side-effect free: it does not alter a
    /// router, process configuration, or production ownership state.
    #[must_use]
    pub fn route_is_eligible_for_review(
        &self,
        evidence: &OwnershipEvidence,
        registry: &ValidatedRegistry,
        model_family: &str,
    ) -> bool {
        matches!(
            self.evaluate_with_registry(evidence, registry, model_family),
            OwnershipDecision::EligibleForOwnershipReview { .. }
        )
    }
}

/// Result of the closed ownership gate.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub enum OwnershipDecision {
    /// Evidence is incomplete; production ownership remains closed.
    ClosedByDefault {
        /// Route whose evidence was evaluated.
        scope: RouteOwnershipScope,
        /// Every blocker found by the pure evaluation.
        blockers: Vec<OwnershipBlocker>,
    },
    /// Evidence is sufficient to open a separate ownership review.
    ///
    /// This is not a route takeover command and has no side effect.
    EligibleForOwnershipReview {
        /// Route which may be reviewed by an independent deployment process.
        scope: RouteOwnershipScope,
    },
}

/// Errors from constructing bounded ownership evidence.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub enum OwnershipEvidenceError {
    /// Canary basis points exceeded the closed range.
    InvalidCanary {
        /// Rejected value.
        basis_points: u16,
    },
}

impl fmt::Display for OwnershipEvidenceError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidCanary { basis_points } => write!(
                formatter,
                "ownership canary basis points {basis_points} exceed {MAX_CANARY_BASIS_POINTS}"
            ),
        }
    }
}

impl Error for OwnershipEvidenceError {}

#[cfg(test)]
mod tests {
    use super::*;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    fn scope() -> RouteOwnershipScope {
        RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::OpenAi,
            stream: true,
        }
    }

    fn complete_for_scope(
        scope: RouteOwnershipScope,
    ) -> Result<OwnershipEvidence, OwnershipEvidenceError> {
        let mut evidence = OwnershipEvidence::closed(scope);
        for class in DifferentialClass::all() {
            evidence.mark_green(*class);
        }
        evidence.set_shadow_identical(true);
        evidence.set_canary_basis_points(MIN_REVIEW_CANARY_BASIS_POINTS)?;
        evidence.approve_rollout();
        Ok(evidence)
    }

    fn complete() -> Result<OwnershipEvidence, OwnershipEvidenceError> {
        complete_for_scope(scope())
    }

    fn complete_cross_protocol() -> Result<OwnershipEvidence, OwnershipEvidenceError> {
        complete_for_scope(RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: true,
        })
    }

    fn shadow_record(
        source: Protocol,
        target: Protocol,
        old_converter_id: Option<&str>,
        new_converter_id: Option<&str>,
        differences: Vec<crate::protocol_rollout::ShadowDifference>,
    ) -> ShadowRecord {
        ShadowRecord {
            source,
            target,
            stream: true,
            old_converter_id: old_converter_id.map(str::to_owned),
            new_converter_id: new_converter_id.map(str::to_owned),
            differences,
        }
    }

    fn evaluate_shadow(record: ShadowRecord) -> Result<OwnershipDecision, OwnershipEvidenceError> {
        let mut evidence = complete()?;
        evidence.record_shadow(&record);
        Ok(OwnershipGate::default().evaluate(&evidence))
    }

    #[test]
    fn default_is_closed_and_complete_evidence_only_opens_review() -> TestResult {
        let gate = OwnershipGate::default();
        assert!(matches!(
            gate.evaluate(&OwnershipEvidence::closed(scope())),
            OwnershipDecision::ClosedByDefault { .. }
        ));
        assert_eq!(
            gate.evaluate(&complete()?),
            OwnershipDecision::EligibleForOwnershipReview { scope: scope() }
        );
        Ok(())
    }

    #[test]
    fn cross_protocol_manual_green_evidence_requires_private_seal() -> TestResult {
        let gate = OwnershipGate::default();
        let mut evidence = complete_cross_protocol()?;
        assert!(matches!(
            gate.evaluate(&evidence),
            OwnershipDecision::ClosedByDefault { blockers, .. }
                if blockers.contains(&OwnershipBlocker::UntrustedEvidence)
        ));

        evidence.seal_trusted();
        assert!(matches!(
            gate.evaluate(&evidence),
            OwnershipDecision::EligibleForOwnershipReview { .. }
        ));

        evidence.set_canary_basis_points(MIN_REVIEW_CANARY_BASIS_POINTS + 1)?;
        assert!(matches!(
            gate.evaluate(&evidence),
            OwnershipDecision::ClosedByDefault { blockers, .. }
                if blockers.contains(&OwnershipBlocker::UntrustedEvidence)
        ));
        Ok(())
    }

    #[test]
    fn invalid_canary_is_rejected_without_opening_review() -> TestResult {
        assert!(OwnershipGate::new(MAX_CANARY_BASIS_POINTS + 1).is_err());
        let mut evidence = complete()?;
        assert!(
            evidence
                .set_canary_basis_points(MAX_CANARY_BASIS_POINTS + 1)
                .is_err()
        );
        assert_eq!(
            evidence.canary_basis_points(),
            MIN_REVIEW_CANARY_BASIS_POINTS
        );
        Ok(())
    }

    #[test]
    fn registry_gate_keeps_cross_protocol_closed_without_trusted_evidence() -> TestResult {
        let mut evidence = complete()?;
        evidence.set_shadow_identical(true);
        let registry = crate::protocol_runtime_registry::validated_current_registry()?;
        let cross_scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: true,
        };
        evidence.scope = cross_scope;
        let decision =
            OwnershipGate::default().evaluate_with_registry(&evidence, &registry, "claude");
        assert!(matches!(
            decision,
            OwnershipDecision::ClosedByDefault { blockers, .. }
                if blockers.contains(&OwnershipBlocker::UntrustedEvidence)
        ));
        Ok(())
    }

    #[test]
    fn rollback_marker_closes_even_an_otherwise_complete_native_route() -> TestResult {
        let mut evidence = complete()?;
        evidence.set_rollback_active(true);
        let decision = OwnershipGate::default().evaluate(&evidence);
        assert!(matches!(
            decision,
            OwnershipDecision::ClosedByDefault { blockers, .. }
                if blockers.contains(&OwnershipBlocker::RollbackActive)
        ));
        Ok(())
    }

    #[test]
    fn mismatched_shadow_evidence_fails_closed() -> TestResult {
        for record in [
            shadow_record(
                Protocol::Claude,
                Protocol::Claude,
                Some("raw-claude-v1"),
                Some("raw-claude-v1"),
                Vec::new(),
            ),
            shadow_record(Protocol::OpenAi, Protocol::OpenAi, None, None, Vec::new()),
        ] {
            let decision = evaluate_shadow(record)?;
            assert!(matches!(
                decision,
                OwnershipDecision::ClosedByDefault { blockers, .. }
                    if blockers.contains(&OwnershipBlocker::ShadowDifference)
            ));
        }
        Ok(())
    }

    #[test]
    fn different_converter_versions_can_prove_identical_shadow_semantics() -> TestResult {
        let decision = evaluate_shadow(shadow_record(
            Protocol::OpenAi,
            Protocol::OpenAi,
            Some("openai-v1"),
            Some("openai-v2"),
            vec![crate::protocol_rollout::ShadowDifference::ConverterId],
        ))?;

        assert!(matches!(
            decision,
            OwnershipDecision::EligibleForOwnershipReview { .. }
        ));
        Ok(())
    }
}
