//! Import and validation of offline Go-vs-Rust protocol differential evidence.
//!
//! This module is deliberately an evidence boundary, not a route switch.  It
//! accepts a versioned, body-free document containing hashes and aggregate
//! results, validates every required field against one already validated
//! capability registry and a host-provided [`EvidenceTrustPolicy`], and only
//! then constructs [`OwnershipEvidence`]. No parser in this module can make a
//! route eligible from a missing field, a default value, or a self-supplied
//! approval/signature, and no function here mutates a router or rollout
//! control.

use std::{collections::BTreeSet, error::Error, fmt};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
// The existing jsonwebtoken rust_crypto EdDSA path delegates to ed25519-dalek;
// this keeps the current Cargo manifest unchanged while using that verifier.
use jsonwebtoken::{Algorithm, DecodingKey, crypto};
use lmm_contracts::relay::{Feature, Fidelity, Protocol, ValidatedRegistry};
use serde::{Deserialize, Serialize, de::Deserializer};
use sha2::{Digest, Sha256};

use crate::route_ownership::{
    DifferentialClass, MIN_REVIEW_CANARY_BASIS_POINTS, OwnershipBlocker, OwnershipDecision,
    OwnershipEvidence, OwnershipGate, RouteOwnershipScope,
};

/// Version of the evidence document schema.
pub const EVIDENCE_SCHEMA_VERSION: &str = "protocol-differential-evidence-v1";

/// Minimum observation window accepted by the review gate.
pub const MIN_OBSERVATION_WINDOW_SECONDS: u64 = 60;

/// Maximum UTF-8 byte length accepted for one JSON evidence input.
pub const MAX_EVIDENCE_JSON_BYTES: usize = 64 * 1024;

/// Maximum number of route-scoped documents accepted in one bundle.
pub const MAX_BUNDLE_DOCUMENTS: usize = 16;

const MAX_IDENTIFIER_LENGTH: usize = 128;
const MAX_VERSION_LENGTH: usize = 128;
const SHA256_HEX_LENGTH: usize = 64;
const ED25519_PUBLIC_KEY_HEX_LENGTH: usize = 64;
const ED25519_SIGNATURE_HEX_LENGTH: usize = 128;
const MAX_CLOCK_SKEW_SECONDS: u64 = 86_400;
const EXPECTED_DIFFERENTIAL_COUNT: usize = 5;
const MAX_FEATURE_CLASSES: usize = 64;
/// Hard upper bound for one attestation's validity interval.
pub const MAX_POLICY_EVIDENCE_LIFETIME_SECONDS: u64 = 30 * 24 * 60 * 60;
/// Domain separator prepended to every typed unsigned evidence payload.
pub const EVIDENCE_SIGNATURE_DOMAIN: &[u8] =
    b"api.lmm.best/protocol-differential-evidence/signature/v1\0";

/// Stable field names used by closed-set validation errors.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EvidenceField {
    SchemaVersion,
    BaselineGoSha,
    CandidateRustSha,
    RegistryFingerprint,
    RegistryVersion,
    RuntimeCatalogVersion,
    ModelFamily,
    EvidenceId,
    ReviewerId,
    ApprovalReference,
    SignerId,
    DifferentialFixtureDigest,
    DifferentialResultDigest,
    ShadowFixtureDigest,
    ShadowResultDigest,
    UsageBillingDigest,
}

impl EvidenceField {
    const fn name(self) -> &'static str {
        match self {
            Self::SchemaVersion => "schema_version",
            Self::BaselineGoSha => "baseline_go_sha",
            Self::CandidateRustSha => "candidate_rust_sha",
            Self::RegistryFingerprint => "registry_fingerprint",
            Self::RegistryVersion => "registry_version",
            Self::RuntimeCatalogVersion => "runtime_catalog_version",
            Self::ModelFamily => "model_family",
            Self::EvidenceId => "evidence_id",
            Self::ReviewerId => "reviewer_id",
            Self::ApprovalReference => "approval_reference",
            Self::SignerId => "signer_id",
            Self::DifferentialFixtureDigest => "differential_fixture_digest",
            Self::DifferentialResultDigest => "differential_result_digest",
            Self::ShadowFixtureDigest => "shadow_fixture_digest",
            Self::ShadowResultDigest => "shadow_result_digest",
            Self::UsageBillingDigest => "usage_billing_digest",
        }
    }
}

/// Closed errors returned by parsing or validating differential evidence.
///
/// Variants contain only stable field names, enum values, or route dimensions;
/// they never retain a request body, fixture body, model name, SHA, or digest.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum DifferentialEvidenceError {
    InvalidJson,
    InvalidSchemaVersion,
    InvalidString {
        field: EvidenceField,
    },
    InvalidSha {
        field: EvidenceField,
    },
    InvalidDigest {
        field: EvidenceField,
    },
    PolicyEmptyBaselineAllowlist,
    PolicyEmptyCandidateAllowlist,
    PolicyEmptyTrustedSigners,
    PolicyEmptyTrustedReviewers,
    PolicyDuplicateSha,
    PolicyDuplicateSigner,
    PolicyDuplicateReviewer,
    PolicyInvalidVerifyingKey,
    PolicyInvalidThreshold,
    PolicyInvalidClockSkew,
    PolicyInvalidEvidenceLifetime,
    BaselineShaNotAllowed,
    CandidateShaNotAllowed,
    EvidenceIdReplay,
    ReplayGuardUnavailable,
    InvalidEvidenceLifetime,
    EvidenceLifetimeExceedsPolicy,
    ApprovalAfterIssue,
    EvidenceNotYetValid,
    EvidenceExpired,
    ObservationWindowBelowPolicyMinimum,
    CanaryBelowPolicyMinimum,
    UnknownSigner,
    UnknownReviewer,
    ReviewerSignerMismatch,
    InvalidSignatureEncoding,
    BadSignature,
    CanonicalizationUnavailable,
    InputTooLarge,
    TooManyBundleDocuments,
    InvalidDifferentialCount {
        expected: usize,
        actual: usize,
    },
    EmptyFeatureClasses,
    TooManyFeatureClasses,
    DuplicateFeatureClass {
        feature: Feature,
    },
    FeatureClassSetMismatch,
    MissingDifferential {
        class: DifferentialClass,
    },
    DuplicateDifferential {
        class: DifferentialClass,
    },
    DuplicateRoute {
        source: Protocol,
        target: Protocol,
        stream: bool,
    },
    InvalidCaseCount {
        class: DifferentialClass,
    },
    DifferentialDifference {
        class: DifferentialClass,
    },
    UsageBillingDifference,
    UsageBillingDigestMismatch,
    InvalidShadowCaseCount,
    ShadowScopeMismatch,
    ShadowDifference,
    InvalidObservationWindow,
    ObservationWindowTooShort,
    InvalidCanary,
    ReviewerNotApproved,
    ApprovalBeforeObservationEnd,
    RegistryFingerprintUnavailable,
    RegistryFingerprintMismatch,
    RegistryVersionMismatch,
    RuntimeCatalogVersionMismatch,
    RouteUnavailable,
    RouteQualityUnsupported,
    RouteDirectionUnsupported,
    ModelConstraintMismatch,
    FeatureUnsupported,
    EmptyBundle,
}

impl fmt::Display for DifferentialEvidenceError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::InvalidJson => formatter.write_str("invalid differential evidence JSON"),
            Self::InvalidSchemaVersion => {
                formatter.write_str("unsupported evidence schema version")
            }
            Self::InvalidString { field } => {
                write!(formatter, "invalid evidence string field {}", field.name())
            }
            Self::InvalidSha { field } => {
                write!(formatter, "invalid commit SHA field {}", field.name())
            }
            Self::InvalidDigest { field } => {
                write!(formatter, "invalid SHA-256 digest field {}", field.name())
            }
            Self::PolicyEmptyBaselineAllowlist => {
                formatter.write_str("trust policy has no allowed baseline SHA")
            }
            Self::PolicyEmptyCandidateAllowlist => {
                formatter.write_str("trust policy has no allowed candidate SHA")
            }
            Self::PolicyEmptyTrustedSigners => {
                formatter.write_str("trust policy has no trusted signer")
            }
            Self::PolicyEmptyTrustedReviewers => {
                formatter.write_str("trust policy has no trusted reviewer")
            }
            Self::PolicyDuplicateSha => {
                formatter.write_str("trust policy contains a duplicate release SHA")
            }
            Self::PolicyDuplicateSigner => {
                formatter.write_str("trust policy contains a duplicate signer")
            }
            Self::PolicyDuplicateReviewer => {
                formatter.write_str("trust policy contains a duplicate reviewer")
            }
            Self::PolicyInvalidVerifyingKey => {
                formatter.write_str("trust policy contains an invalid Ed25519 key")
            }
            Self::PolicyInvalidThreshold => {
                formatter.write_str("trust policy contains an invalid gate threshold")
            }
            Self::PolicyInvalidClockSkew => {
                formatter.write_str("trust policy contains an invalid clock skew")
            }
            Self::PolicyInvalidEvidenceLifetime => {
                formatter.write_str("trust policy contains an invalid evidence lifetime")
            }
            Self::BaselineShaNotAllowed => {
                formatter.write_str("baseline SHA is not allowed by the trust policy")
            }
            Self::CandidateShaNotAllowed => {
                formatter.write_str("candidate SHA is not allowed by the trust policy")
            }
            Self::EvidenceIdReplay => formatter.write_str("evidence identifier was already used"),
            Self::ReplayGuardUnavailable => {
                formatter.write_str("evidence replay guard is unavailable")
            }
            Self::InvalidEvidenceLifetime => {
                formatter.write_str("evidence validity interval is invalid")
            }
            Self::EvidenceLifetimeExceedsPolicy => {
                formatter.write_str("evidence validity interval exceeds the trust policy")
            }
            Self::ApprovalAfterIssue => {
                formatter.write_str("reviewer approval is not before evidence issuance")
            }
            Self::EvidenceNotYetValid => formatter.write_str("evidence is not yet valid"),
            Self::EvidenceExpired => formatter.write_str("evidence has expired"),
            Self::ObservationWindowBelowPolicyMinimum => {
                formatter.write_str("observation window is below the trust policy minimum")
            }
            Self::CanaryBelowPolicyMinimum => {
                formatter.write_str("canary allocation is below the trust policy minimum")
            }
            Self::UnknownSigner => formatter.write_str("evidence signer is not trusted"),
            Self::UnknownReviewer => formatter.write_str("evidence reviewer is not trusted"),
            Self::ReviewerSignerMismatch => {
                formatter.write_str("reviewer is not bound to the evidence signer")
            }
            Self::InvalidSignatureEncoding => {
                formatter.write_str("invalid Ed25519 signature encoding")
            }
            Self::BadSignature => formatter.write_str("evidence signature verification failed"),
            Self::CanonicalizationUnavailable => {
                formatter.write_str("evidence signing payload could not be canonicalized")
            }
            Self::InputTooLarge => formatter.write_str("differential evidence JSON is too large"),
            Self::TooManyBundleDocuments => {
                formatter.write_str("differential evidence bundle has too many documents")
            }
            Self::InvalidDifferentialCount { expected, actual } => write!(
                formatter,
                "differential evidence has {actual} classes; expected {expected}"
            ),
            Self::EmptyFeatureClasses => formatter.write_str("evidence feature class set is empty"),
            Self::TooManyFeatureClasses => {
                formatter.write_str("evidence feature class set is too large")
            }
            Self::DuplicateFeatureClass { feature } => {
                write!(formatter, "duplicate evidence feature class {feature:?}")
            }
            Self::FeatureClassSetMismatch => {
                formatter.write_str("evidence feature class set does not match the registry")
            }
            Self::MissingDifferential { class } => {
                write!(formatter, "missing differential class {class:?}")
            }
            Self::DuplicateDifferential { class } => {
                write!(formatter, "duplicate differential class {class:?}")
            }
            Self::DuplicateRoute {
                source,
                target,
                stream,
            } => write!(
                formatter,
                "duplicate route scope {source:?}->{target:?} stream={stream}"
            ),
            Self::InvalidCaseCount { class } => {
                write!(formatter, "differential class {class:?} has no cases")
            }
            Self::DifferentialDifference { class } => {
                write!(formatter, "differential class {class:?} is not identical")
            }
            Self::UsageBillingDifference => {
                formatter.write_str("usage and billing differential is not identical")
            }
            Self::UsageBillingDigestMismatch => {
                formatter.write_str("usage and billing digest does not match its differential")
            }
            Self::InvalidShadowCaseCount => formatter.write_str("shadow aggregate has no cases"),
            Self::ShadowScopeMismatch => {
                formatter.write_str("shadow aggregate scope does not match route scope")
            }
            Self::ShadowDifference => formatter.write_str("shadow aggregate is not identical"),
            Self::InvalidObservationWindow => {
                formatter.write_str("canary observation window is not ordered")
            }
            Self::ObservationWindowTooShort => {
                formatter.write_str("canary observation window is too short")
            }
            Self::InvalidCanary => formatter.write_str("invalid canary basis points"),
            Self::ReviewerNotApproved => formatter.write_str("reviewer approval is missing"),
            Self::ApprovalBeforeObservationEnd => {
                formatter.write_str("reviewer approval predates the observation window")
            }
            Self::RegistryFingerprintUnavailable => {
                formatter.write_str("registry fingerprint could not be computed")
            }
            Self::RegistryFingerprintMismatch => {
                formatter.write_str("registry fingerprint does not match")
            }
            Self::RegistryVersionMismatch => formatter.write_str("registry version does not match"),
            Self::RuntimeCatalogVersionMismatch => {
                formatter.write_str("runtime catalog version does not match")
            }
            Self::RouteUnavailable => formatter.write_str("route is not present in the registry"),
            Self::RouteQualityUnsupported => formatter.write_str("route quality is unsupported"),
            Self::RouteDirectionUnsupported => {
                formatter.write_str("route direction is unsupported")
            }
            Self::ModelConstraintMismatch => {
                formatter.write_str("model family is outside route constraints")
            }
            Self::FeatureUnsupported => formatter.write_str("feature is unsupported by route"),
            Self::EmptyBundle => formatter.write_str("differential evidence bundle is empty"),
        }
    }
}

impl Error for DifferentialEvidenceError {}

/// Closed outcomes from the host-owned replay store.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum EvidenceReplayGuardError {
    /// The evidence identifier was atomically consumed by an earlier caller.
    AlreadyConsumed,
    /// The host could not complete the atomic consume operation.
    Unavailable,
}

impl fmt::Display for EvidenceReplayGuardError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::AlreadyConsumed => formatter.write_str("evidence identifier was already used"),
            Self::Unavailable => formatter.write_str("evidence replay guard is unavailable"),
        }
    }
}

impl Error for EvidenceReplayGuardError {}

/// Host-owned atomic consume-once boundary for route admissions.
///
/// The implementation must perform a durable compare-and-insert (or
/// equivalent) on `evidence_id` that is atomic across all relevant threads and
/// processes. A process-local set or mutex is not sufficient for production,
/// and this module intentionally provides no default or in-memory
/// implementation that could be mistaken for that guarantee. The host should
/// keep the same current registry snapshot and route-selection transaction
/// around this call and the resulting admission use.
pub trait EvidenceReplayGuard: Send + Sync {
    /// Consumes an evidence identifier exactly once, or fails closed.
    fn consume_once(&self, evidence_id: &str) -> Result<(), EvidenceReplayGuardError>;
}

/// Result asserted by one Go-vs-Rust differential class.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DifferentialResult {
    Match,
    Difference,
}

/// Result asserted by the body-free shadow aggregate.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ShadowResult {
    Identical,
    Difference,
}

/// One required differential class and its immutable aggregate evidence.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DifferentialClassEvidence {
    pub class: DifferentialClass,
    pub case_count: u64,
    pub fixture_digest: String,
    pub result_digest: String,
    pub result: DifferentialResult,
}

/// Body-free aggregate of old/new local shadow conversion results.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ShadowAggregate {
    #[serde(deserialize_with = "deserialize_strict_scope")]
    pub scope: RouteOwnershipScope,
    pub case_count: u64,
    pub fixture_digest: String,
    pub result_digest: String,
    pub result: ShadowResult,
}

/// Canary allocation and the bounded interval in which it was observed.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct CanaryObservationWindow {
    pub started_at_unix_seconds: u64,
    pub ended_at_unix_seconds: u64,
    pub basis_points: u16,
}

/// Explicit human approval attached to a complete evidence document.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct ReviewerApproval {
    pub approved: bool,
    pub reviewer_id: String,
    pub approved_at_unix_seconds: u64,
    pub approval_reference: String,
}

/// One operator-supplied Ed25519 verification key trusted for attestations.
/// The key is lowercase raw-public-key hex (exactly 32 bytes); it is never
/// accepted from the evidence document itself.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TrustedSigner {
    pub signer_id: String,
    pub verifying_key_hex: String,
}

/// Host-provided trust anchors for differential evidence.
///
/// This type intentionally has no `Default` implementation. A caller must
/// supply non-empty release allowlists, trusted reviewers, and trusted keys.
/// `consumed_evidence_ids` is a snapshot for replay checks; callers must still
/// atomically consume [`VerifiedDifferentialEvidence::evidence_id`] before
/// applying any external ownership decision.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct EvidenceTrustPolicy {
    pub allowed_baseline_go_shas: Vec<String>,
    pub allowed_candidate_rust_shas: Vec<String>,
    pub trusted_reviewers: Vec<String>,
    pub trusted_signers: Vec<TrustedSigner>,
    pub minimum_observation_window_seconds: u64,
    pub minimum_canary_basis_points: u16,
    pub maximum_evidence_lifetime_seconds: u64,
    pub now_unix_seconds: u64,
    pub clock_skew_seconds: u64,
    pub consumed_evidence_ids: BTreeSet<String>,
}

impl EvidenceTrustPolicy {
    /// Validates policy-owned release, reviewer, key, threshold, and replay
    /// anchors before any evidence can become verified.
    pub fn validate(&self) -> Result<(), DifferentialEvidenceError> {
        validate_policy_sha_allowlist(
            &self.allowed_baseline_go_shas,
            EvidenceField::BaselineGoSha,
            DifferentialEvidenceError::PolicyEmptyBaselineAllowlist,
        )?;
        validate_policy_sha_allowlist(
            &self.allowed_candidate_rust_shas,
            EvidenceField::CandidateRustSha,
            DifferentialEvidenceError::PolicyEmptyCandidateAllowlist,
        )?;
        if self.trusted_signers.is_empty() {
            return Err(DifferentialEvidenceError::PolicyEmptyTrustedSigners);
        }
        if self.trusted_reviewers.is_empty() {
            return Err(DifferentialEvidenceError::PolicyEmptyTrustedReviewers);
        }
        if self.minimum_observation_window_seconds == 0
            || self.minimum_canary_basis_points == 0
            || self.minimum_canary_basis_points > 10_000
        {
            return Err(DifferentialEvidenceError::PolicyInvalidThreshold);
        }
        if self.maximum_evidence_lifetime_seconds == 0
            || self.maximum_evidence_lifetime_seconds > MAX_POLICY_EVIDENCE_LIFETIME_SECONDS
        {
            return Err(DifferentialEvidenceError::PolicyInvalidEvidenceLifetime);
        }
        if self.clock_skew_seconds > MAX_CLOCK_SKEW_SECONDS {
            return Err(DifferentialEvidenceError::PolicyInvalidClockSkew);
        }

        let mut reviewers = BTreeSet::new();
        for reviewer in &self.trusted_reviewers {
            validate_identifier(reviewer, EvidenceField::ReviewerId, MAX_IDENTIFIER_LENGTH)?;
            if !reviewers.insert(reviewer) {
                return Err(DifferentialEvidenceError::PolicyDuplicateReviewer);
            }
        }

        let mut signers = BTreeSet::new();
        for signer in &self.trusted_signers {
            validate_identifier(
                &signer.signer_id,
                EvidenceField::SignerId,
                MAX_IDENTIFIER_LENGTH,
            )?;
            if !signers.insert(&signer.signer_id) {
                return Err(DifferentialEvidenceError::PolicyDuplicateSigner);
            }
            if decode_fixed_hex(&signer.verifying_key_hex, ED25519_PUBLIC_KEY_HEX_LENGTH).is_none()
            {
                return Err(DifferentialEvidenceError::PolicyInvalidVerifyingKey);
            }
        }

        for evidence_id in &self.consumed_evidence_ids {
            validate_identifier(
                evidence_id,
                EvidenceField::EvidenceId,
                MAX_IDENTIFIER_LENGTH,
            )?;
        }
        Ok(())
    }
}

/// Versioned, body-free Go-vs-Rust differential evidence document.
///
/// The hashes identify the compared releases. Registry metadata binds the
/// document to one exact validated capability snapshot. Five entries in
/// [`differentials`] and the complete route feature set in [`feature_classes`]
/// are mandatory; no class, feature, or route is inferred when absent.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DifferentialEvidenceDocument {
    pub schema_version: String,
    pub evidence_id: String,
    pub baseline_go_sha: String,
    pub candidate_rust_sha: String,
    pub registry_fingerprint: String,
    pub registry_version: String,
    pub runtime_catalog_version: String,
    #[serde(deserialize_with = "deserialize_strict_scope")]
    pub scope: RouteOwnershipScope,
    pub model_family: String,
    pub feature_classes: Vec<Feature>,
    pub differentials: Vec<DifferentialClassEvidence>,
    pub shadow: ShadowAggregate,
    pub usage_billing_digest: String,
    pub canary: CanaryObservationWindow,
    pub reviewer_approval: ReviewerApproval,
    pub issued_at_unix_seconds: u64,
    pub valid_until_unix_seconds: u64,
    pub signer_id: String,
    pub signature: String,
}

#[derive(Serialize)]
struct UnsignedEvidencePayload<'a> {
    schema_version: &'a str,
    evidence_id: &'a str,
    baseline_go_sha: &'a str,
    candidate_rust_sha: &'a str,
    registry_fingerprint: &'a str,
    registry_version: &'a str,
    runtime_catalog_version: &'a str,
    scope: RouteOwnershipScope,
    model_family: &'a str,
    feature_classes: &'a [Feature],
    differentials: &'a [DifferentialClassEvidence],
    shadow: &'a ShadowAggregate,
    usage_billing_digest: &'a str,
    canary: CanaryObservationWindow,
    reviewer_approval: &'a ReviewerApproval,
    issued_at_unix_seconds: u64,
    valid_until_unix_seconds: u64,
    signer_id: &'a str,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct StrictRouteOwnershipScope {
    source: Protocol,
    target: Protocol,
    stream: bool,
}

fn deserialize_strict_scope<'de, D>(deserializer: D) -> Result<RouteOwnershipScope, D::Error>
where
    D: Deserializer<'de>,
{
    let scope = StrictRouteOwnershipScope::deserialize(deserializer)?;
    Ok(RouteOwnershipScope {
        source: scope.source,
        target: scope.target,
        stream: scope.stream,
    })
}

impl DifferentialEvidenceDocument {
    /// Parses one document while rejecting malformed or unknown JSON fields.
    pub fn from_json(input: &str) -> Result<Self, DifferentialEvidenceError> {
        if input.len() > MAX_EVIDENCE_JSON_BYTES {
            return Err(DifferentialEvidenceError::InputTooLarge);
        }
        let document: Self =
            serde_json::from_str(input).map_err(|_| DifferentialEvidenceError::InvalidJson)?;
        validate_differential_count(document.differentials.len())?;
        validate_feature_class_shape(&document.feature_classes)?;
        Ok(document)
    }

    /// Parses and fully verifies one document against a validated registry.
    pub fn parse_and_verify(
        input: &str,
        registry: &ValidatedRegistry,
        trust_policy: &EvidenceTrustPolicy,
    ) -> Result<VerifiedDifferentialEvidence, DifferentialEvidenceError> {
        Self::from_json(input)?.verify(registry, trust_policy)
    }

    /// Returns the domain-separated, length-prefixed canonical bytes that the
    /// trusted signer must attest. The typed field order is independent of
    /// input JSON object ordering and excludes only the signature itself.
    pub fn signing_payload(&self) -> Result<Vec<u8>, DifferentialEvidenceError> {
        let canonical_feature_classes = canonical_feature_classes(&self.feature_classes)?;
        let mut canonical_differentials = Vec::with_capacity(DifferentialClass::all().len());
        for class in DifferentialClass::all() {
            let mut matching = self
                .differentials
                .iter()
                .filter(|differential| differential.class == *class);
            let Some(differential) = matching.next() else {
                return Err(DifferentialEvidenceError::CanonicalizationUnavailable);
            };
            if matching.next().is_some() {
                return Err(DifferentialEvidenceError::CanonicalizationUnavailable);
            }
            canonical_differentials.push(differential.clone());
        }
        if canonical_differentials.len() != self.differentials.len() {
            return Err(DifferentialEvidenceError::CanonicalizationUnavailable);
        }
        let unsigned = UnsignedEvidencePayload {
            schema_version: &self.schema_version,
            evidence_id: &self.evidence_id,
            baseline_go_sha: &self.baseline_go_sha,
            candidate_rust_sha: &self.candidate_rust_sha,
            registry_fingerprint: &self.registry_fingerprint,
            registry_version: &self.registry_version,
            runtime_catalog_version: &self.runtime_catalog_version,
            scope: self.scope,
            model_family: &self.model_family,
            feature_classes: &canonical_feature_classes,
            differentials: &canonical_differentials,
            shadow: &self.shadow,
            usage_billing_digest: &self.usage_billing_digest,
            canary: self.canary,
            reviewer_approval: &self.reviewer_approval,
            issued_at_unix_seconds: self.issued_at_unix_seconds,
            valid_until_unix_seconds: self.valid_until_unix_seconds,
            signer_id: &self.signer_id,
        };
        let encoded = serde_json::to_vec(&unsigned)
            .map_err(|_| DifferentialEvidenceError::CanonicalizationUnavailable)?;
        let encoded_len = u64::try_from(encoded.len())
            .map_err(|_| DifferentialEvidenceError::CanonicalizationUnavailable)?;
        let mut payload = Vec::with_capacity(
            EVIDENCE_SIGNATURE_DOMAIN.len() + std::mem::size_of::<u64>() + encoded.len(),
        );
        payload.extend_from_slice(EVIDENCE_SIGNATURE_DOMAIN);
        payload.extend_from_slice(&encoded_len.to_be_bytes());
        payload.extend_from_slice(&encoded);
        Ok(payload)
    }

    /// Validates all hashes, aggregates, route claims, and review metadata.
    ///
    /// `OwnershipEvidence` is constructed only at the final return point,
    /// after every required class and registry check has passed.
    pub fn verify(
        &self,
        registry: &ValidatedRegistry,
        trust_policy: &EvidenceTrustPolicy,
    ) -> Result<VerifiedDifferentialEvidence, DifferentialEvidenceError> {
        validate_differential_count(self.differentials.len())?;
        validate_feature_class_shape(&self.feature_classes)?;
        trust_policy.validate()?;
        validate_document(self, registry)?;
        validate_policy_claims(self, trust_policy)?;
        verify_attestation(self, trust_policy)?;

        let mut ownership = OwnershipEvidence::closed(self.scope);
        for class in DifferentialClass::all() {
            ownership.mark_green(*class);
        }
        ownership
            .set_canary_basis_points(self.canary.basis_points)
            .map_err(|_| DifferentialEvidenceError::InvalidCanary)?;
        ownership.set_shadow_identical(true);
        ownership.approve_rollout();
        ownership.seal_trusted();

        Ok(VerifiedDifferentialEvidence {
            document: self.clone(),
            ownership,
            minimum_canary_basis_points: trust_policy
                .minimum_canary_basis_points
                .max(MIN_REVIEW_CANARY_BASIS_POINTS),
        })
    }
}

/// A versioned collection used by offline importers to reject duplicate route
/// scopes before any individual document can be considered for review.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct DifferentialEvidenceBundle {
    pub schema_version: String,
    pub documents: Vec<DifferentialEvidenceDocument>,
}

impl DifferentialEvidenceBundle {
    /// Parses a bundle with closed unknown-field behavior.
    pub fn from_json(input: &str) -> Result<Self, DifferentialEvidenceError> {
        if input.len() > MAX_EVIDENCE_JSON_BYTES {
            return Err(DifferentialEvidenceError::InputTooLarge);
        }
        let bundle: Self =
            serde_json::from_str(input).map_err(|_| DifferentialEvidenceError::InvalidJson)?;
        validate_bundle_document_count(bundle.documents.len())?;
        for document in &bundle.documents {
            validate_differential_count(document.differentials.len())?;
            validate_feature_class_shape(&document.feature_classes)?;
        }
        Ok(bundle)
    }

    /// Verifies every document and rejects duplicate route scopes.
    pub fn verify(
        &self,
        registry: &ValidatedRegistry,
        trust_policy: &EvidenceTrustPolicy,
    ) -> Result<Vec<VerifiedDifferentialEvidence>, DifferentialEvidenceError> {
        validate_bundle_document_count(self.documents.len())?;
        for document in &self.documents {
            validate_differential_count(document.differentials.len())?;
            validate_feature_class_shape(&document.feature_classes)?;
        }
        trust_policy.validate()?;
        validate_schema_version(&self.schema_version)?;
        if self.documents.is_empty() {
            return Err(DifferentialEvidenceError::EmptyBundle);
        }
        let mut routes = Vec::new();
        let mut evidence_ids = BTreeSet::new();
        let mut verified = Vec::with_capacity(self.documents.len());
        for document in &self.documents {
            let scope = document.scope;
            if routes.contains(&scope) {
                return Err(DifferentialEvidenceError::DuplicateRoute {
                    source: scope.source,
                    target: scope.target,
                    stream: scope.stream,
                });
            }
            routes.push(scope);
            if !evidence_ids.insert(&document.evidence_id) {
                return Err(DifferentialEvidenceError::EvidenceIdReplay);
            }
            verified.push(document.verify(registry, trust_policy)?);
        }
        Ok(verified)
    }
}

/// Evidence that has passed every document and registry invariant.
///
/// This value is the only public result that can yield green
/// [`OwnershipEvidence`]. It remains a pure review result: it does not mount a
/// route, alter a registry, or replace a rollout configuration.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct VerifiedDifferentialEvidence {
    document: DifferentialEvidenceDocument,
    ownership: OwnershipEvidence,
    minimum_canary_basis_points: u16,
}

impl VerifiedDifferentialEvidence {
    /// Returns the nonce-like identifier the host must atomically consume.
    #[must_use]
    pub fn evidence_id(&self) -> &str {
        &self.document.evidence_id
    }

    /// Returns the validated source document without exposing request bodies.
    #[must_use]
    pub const fn document(&self) -> &DifferentialEvidenceDocument {
        &self.document
    }

    /// Atomically consumes this evidence and returns the only route-admission
    /// view exposed by this module.
    ///
    /// The caller must provide a host-owned [`EvidenceReplayGuard`] whose
    /// compare-and-insert is atomic across every process that can select the
    /// route. Before consumption this method revalidates the current clock
    /// (`issued_at`/`valid_until`), trust policy, signature, registry
    /// fingerprint/version/runtime catalog, route directions and quality, the
    /// requested model family, and the complete requested feature set. A
    /// failed check never calls the replay guard, while an unavailable or
    /// already-used guard fails closed. The returned view has no public
    /// constructor or mutator and must be used with the same current registry
    /// snapshot supplied here; this method does not alter a router or rollout
    /// control.
    pub fn consume_for_route_admission(
        &self,
        replay_guard: &dyn EvidenceReplayGuard,
        registry: &ValidatedRegistry,
        trust_policy: &EvidenceTrustPolicy,
        model_family: &str,
        feature_classes: &[Feature],
        now_unix_seconds: u64,
    ) -> Result<ConsumedRouteAdmission, DifferentialEvidenceError> {
        validate_differential_count(self.document.differentials.len())?;
        validate_feature_class_shape(&self.document.feature_classes)?;
        trust_policy.validate()?;
        validate_document(&self.document, registry)?;
        validate_policy_claims_at(&self.document, trust_policy, now_unix_seconds)?;
        verify_attestation(&self.document, trust_policy)?;

        if model_family != self.document.model_family {
            return Err(DifferentialEvidenceError::ModelConstraintMismatch);
        }
        let evidence_feature_classes = canonical_feature_classes(&self.document.feature_classes)?;
        let requested_feature_classes = canonical_feature_classes(feature_classes)?;
        if requested_feature_classes != evidence_feature_classes {
            return Err(DifferentialEvidenceError::FeatureClassSetMismatch);
        }

        replay_guard
            .consume_once(&self.document.evidence_id)
            .map_err(|error| match error {
                EvidenceReplayGuardError::AlreadyConsumed => {
                    DifferentialEvidenceError::EvidenceIdReplay
                }
                EvidenceReplayGuardError::Unavailable => {
                    DifferentialEvidenceError::ReplayGuardUnavailable
                }
            })?;

        Ok(ConsumedRouteAdmission {
            evidence_id: self.document.evidence_id.clone(),
            ownership: self.ownership.clone(),
            scope: self.document.scope,
            model_family: self.document.model_family.clone(),
            feature_classes: evidence_feature_classes,
            registry_fingerprint: self.document.registry_fingerprint.clone(),
            registry_version: self.document.registry_version.clone(),
            runtime_catalog_version: self.document.runtime_catalog_version.clone(),
            issued_at_unix_seconds: self.document.issued_at_unix_seconds,
            valid_until_unix_seconds: self.document.valid_until_unix_seconds,
            clock_skew_seconds: trust_policy.clock_skew_seconds,
            minimum_canary_basis_points: trust_policy
                .minimum_canary_basis_points
                .max(MIN_REVIEW_CANARY_BASIS_POINTS),
        })
    }

    /// Evaluates the policy-bound, closed-by-default ownership gate.
    ///
    /// `true` means only “eligible for an independent ownership review”; it
    /// does not authorize router construction or traffic takeover. This type
    /// intentionally exposes no owned route-gate evidence: a future host must
    /// first integrate atomic replay consumption and re-check expiry at the
    /// traffic-selection boundary.
    #[must_use]
    pub fn review_decision(&self) -> OwnershipDecision {
        match OwnershipGate::new(self.minimum_canary_basis_points) {
            Ok(gate) => gate.evaluate(&self.ownership),
            Err(_) => OwnershipDecision::ClosedByDefault {
                scope: self.document.scope,
                blockers: vec![OwnershipBlocker::InvalidCanary],
            },
        }
    }

    /// Returns whether the document is eligible for independent review.
    #[must_use]
    pub fn eligible_for_review(&self) -> bool {
        matches!(
            self.review_decision(),
            OwnershipDecision::EligibleForOwnershipReview { .. }
        )
    }
}

/// A consumed, route-bound admission view.
///
/// Instances can only be constructed by
/// [`VerifiedDifferentialEvidence::consume_for_route_admission`] after all
/// current binding checks pass and the host replay store atomically consumes
/// the evidence identifier. The private fields and absence of a public
/// constructor prevent callers from assembling a trusted view from defaults
/// or from a different route. This type is an admission observation only; it
/// does not connect a router or open cross-protocol traffic.
#[derive(Debug, Eq, PartialEq)]
pub struct ConsumedRouteAdmission {
    evidence_id: String,
    ownership: OwnershipEvidence,
    scope: RouteOwnershipScope,
    model_family: String,
    feature_classes: Vec<Feature>,
    registry_fingerprint: String,
    registry_version: String,
    runtime_catalog_version: String,
    issued_at_unix_seconds: u64,
    valid_until_unix_seconds: u64,
    clock_skew_seconds: u64,
    minimum_canary_basis_points: u16,
}

impl ConsumedRouteAdmission {
    /// Returns the atomically consumed evidence identifier.
    #[must_use]
    pub fn evidence_id(&self) -> &str {
        &self.evidence_id
    }

    /// Returns the route identity bound by the evidence document and current
    /// registry check.
    #[must_use]
    pub const fn scope(&self) -> RouteOwnershipScope {
        self.scope
    }

    /// Returns the exact model family bound by the admission.
    #[must_use]
    pub fn model_family(&self) -> &str {
        &self.model_family
    }

    /// Returns the complete, canonical feature set bound by the admission.
    #[must_use]
    pub fn feature_classes(&self) -> &[Feature] {
        &self.feature_classes
    }

    /// Returns the current registry fingerprint checked at consumption.
    #[must_use]
    pub fn registry_fingerprint(&self) -> &str {
        &self.registry_fingerprint
    }

    /// Returns the current support-matrix version checked at consumption.
    #[must_use]
    pub fn registry_version(&self) -> &str {
        &self.registry_version
    }

    /// Returns the current runtime catalog version checked at consumption.
    #[must_use]
    pub fn runtime_catalog_version(&self) -> &str {
        &self.runtime_catalog_version
    }

    /// Evaluates this consumed admission at the traffic-selection clock.
    ///
    /// A consumed nonce is not a perpetual authorization: callers must pass
    /// the current time at every selection boundary. An admission outside its
    /// signed validity window fails closed even though it was valid when the
    /// replay store consumed it.
    pub fn review_decision_at(
        &self,
        now_unix_seconds: u64,
    ) -> Result<OwnershipDecision, DifferentialEvidenceError> {
        if now_unix_seconds.saturating_add(self.clock_skew_seconds) < self.issued_at_unix_seconds {
            return Err(DifferentialEvidenceError::EvidenceNotYetValid);
        }
        if now_unix_seconds
            > self
                .valid_until_unix_seconds
                .saturating_add(self.clock_skew_seconds)
        {
            return Err(DifferentialEvidenceError::EvidenceExpired);
        }
        Ok(match OwnershipGate::new(self.minimum_canary_basis_points) {
            Ok(gate) => gate.evaluate(&self.ownership),
            Err(_) => OwnershipDecision::ClosedByDefault {
                scope: self.scope,
                blockers: vec![OwnershipBlocker::InvalidCanary],
            },
        })
    }

    /// Returns whether the sealed view is eligible for the independent
    /// ownership review stage at the supplied current time. This is not a
    /// router takeover command.
    pub fn eligible_for_route_admission_at(
        &self,
        now_unix_seconds: u64,
    ) -> Result<bool, DifferentialEvidenceError> {
        Ok(matches!(
            self.review_decision_at(now_unix_seconds)?,
            OwnershipDecision::EligibleForOwnershipReview { .. }
        ))
    }
}

/// Computes the exact registry fingerprint bound into evidence documents.
///
/// The canonical payload is the validated support matrix plus the runtime
/// catalog version that checked its live wiring. Callers should persist this
/// returned lowercase SHA-256 string, not a hand-written version label.
pub fn registry_fingerprint(
    registry: &ValidatedRegistry,
) -> Result<String, DifferentialEvidenceError> {
    let canonical = serde_json::to_vec(&(
        registry.support_matrix(),
        registry.runtime_catalog_version(),
    ))
    .map_err(|_| DifferentialEvidenceError::RegistryFingerprintUnavailable)?;
    Ok(hex::encode(Sha256::digest(canonical)))
}

fn validate_document(
    document: &DifferentialEvidenceDocument,
    registry: &ValidatedRegistry,
) -> Result<(), DifferentialEvidenceError> {
    validate_schema_version(&document.schema_version)?;
    validate_identifier(
        &document.evidence_id,
        EvidenceField::EvidenceId,
        MAX_IDENTIFIER_LENGTH,
    )?;
    validate_commit_sha(&document.baseline_go_sha, EvidenceField::BaselineGoSha)?;
    validate_commit_sha(
        &document.candidate_rust_sha,
        EvidenceField::CandidateRustSha,
    )?;
    validate_digest(
        &document.registry_fingerprint,
        EvidenceField::RegistryFingerprint,
    )?;
    validate_identifier(
        &document.registry_version,
        EvidenceField::RegistryVersion,
        MAX_VERSION_LENGTH,
    )?;
    validate_identifier(
        &document.runtime_catalog_version,
        EvidenceField::RuntimeCatalogVersion,
        MAX_VERSION_LENGTH,
    )?;
    validate_identifier(
        &document.model_family,
        EvidenceField::ModelFamily,
        MAX_IDENTIFIER_LENGTH,
    )?;
    validate_feature_class_shape(&document.feature_classes)?;
    validate_digest(
        &document.usage_billing_digest,
        EvidenceField::UsageBillingDigest,
    )?;
    validate_identifier(
        &document.signer_id,
        EvidenceField::SignerId,
        MAX_IDENTIFIER_LENGTH,
    )?;
    if decode_fixed_hex(&document.signature, ED25519_SIGNATURE_HEX_LENGTH).is_none() {
        return Err(DifferentialEvidenceError::InvalidSignatureEncoding);
    }
    if document.valid_until_unix_seconds <= document.issued_at_unix_seconds {
        return Err(DifferentialEvidenceError::InvalidEvidenceLifetime);
    }
    validate_canary(&document.canary)?;
    validate_reviewer(
        &document.reviewer_approval,
        &document.canary,
        document.issued_at_unix_seconds,
    )?;
    validate_shadow(&document.shadow, document.scope)?;
    validate_differentials(&document.differentials, &document.usage_billing_digest)?;
    validate_registry_binding(document, registry)?;
    Ok(())
}

fn validate_schema_version(value: &str) -> Result<(), DifferentialEvidenceError> {
    if value != EVIDENCE_SCHEMA_VERSION {
        return Err(DifferentialEvidenceError::InvalidSchemaVersion);
    }
    Ok(())
}

fn validate_differential_count(count: usize) -> Result<(), DifferentialEvidenceError> {
    if count != DifferentialClass::all().len() || count != EXPECTED_DIFFERENTIAL_COUNT {
        return Err(DifferentialEvidenceError::InvalidDifferentialCount {
            expected: DifferentialClass::all().len(),
            actual: count,
        });
    }
    Ok(())
}

fn validate_bundle_document_count(count: usize) -> Result<(), DifferentialEvidenceError> {
    if count > MAX_BUNDLE_DOCUMENTS {
        return Err(DifferentialEvidenceError::TooManyBundleDocuments);
    }
    Ok(())
}

fn canonical_feature_classes(
    features: &[Feature],
) -> Result<Vec<Feature>, DifferentialEvidenceError> {
    if features.is_empty() {
        return Err(DifferentialEvidenceError::EmptyFeatureClasses);
    }
    if features.len() > MAX_FEATURE_CLASSES {
        return Err(DifferentialEvidenceError::TooManyFeatureClasses);
    }
    let mut unique = BTreeSet::new();
    for feature in features {
        if !unique.insert(*feature) {
            return Err(DifferentialEvidenceError::DuplicateFeatureClass { feature: *feature });
        }
    }
    Ok(unique.into_iter().collect())
}

fn validate_feature_class_shape(features: &[Feature]) -> Result<(), DifferentialEvidenceError> {
    canonical_feature_classes(features).map(|_| ())
}

fn validate_policy_sha_allowlist(
    values: &[String],
    field: EvidenceField,
    empty_error: DifferentialEvidenceError,
) -> Result<(), DifferentialEvidenceError> {
    if values.is_empty() {
        return Err(empty_error);
    }
    let mut seen = BTreeSet::new();
    for value in values {
        validate_commit_sha(value, field)?;
        if !seen.insert(value) {
            return Err(DifferentialEvidenceError::PolicyDuplicateSha);
        }
    }
    Ok(())
}

fn decode_fixed_hex(value: &str, expected_hex_length: usize) -> Option<Vec<u8>> {
    if value.len() != expected_hex_length
        || !value
            .bytes()
            .all(|byte| matches!(byte, b'0'..=b'9' | b'a'..=b'f'))
    {
        return None;
    }
    hex::decode(value).ok()
}

fn validate_policy_claims(
    document: &DifferentialEvidenceDocument,
    trust_policy: &EvidenceTrustPolicy,
) -> Result<(), DifferentialEvidenceError> {
    validate_policy_claims_at(document, trust_policy, trust_policy.now_unix_seconds)
}

fn validate_policy_claims_at(
    document: &DifferentialEvidenceDocument,
    trust_policy: &EvidenceTrustPolicy,
    now: u64,
) -> Result<(), DifferentialEvidenceError> {
    if !trust_policy
        .allowed_baseline_go_shas
        .iter()
        .any(|allowed| allowed == &document.baseline_go_sha)
    {
        return Err(DifferentialEvidenceError::BaselineShaNotAllowed);
    }
    if !trust_policy
        .allowed_candidate_rust_shas
        .iter()
        .any(|allowed| allowed == &document.candidate_rust_sha)
    {
        return Err(DifferentialEvidenceError::CandidateShaNotAllowed);
    }
    if trust_policy
        .consumed_evidence_ids
        .contains(&document.evidence_id)
    {
        return Err(DifferentialEvidenceError::EvidenceIdReplay);
    }
    if !trust_policy
        .trusted_signers
        .iter()
        .any(|signer| signer.signer_id == document.signer_id)
    {
        return Err(DifferentialEvidenceError::UnknownSigner);
    }
    if !trust_policy
        .trusted_reviewers
        .iter()
        .any(|reviewer| reviewer == &document.reviewer_approval.reviewer_id)
    {
        return Err(DifferentialEvidenceError::UnknownReviewer);
    }
    if document.reviewer_approval.reviewer_id != document.signer_id {
        return Err(DifferentialEvidenceError::ReviewerSignerMismatch);
    }
    if document
        .valid_until_unix_seconds
        .saturating_sub(document.issued_at_unix_seconds)
        > trust_policy.maximum_evidence_lifetime_seconds
    {
        return Err(DifferentialEvidenceError::EvidenceLifetimeExceedsPolicy);
    }
    let observation_seconds = document
        .canary
        .ended_at_unix_seconds
        .saturating_sub(document.canary.started_at_unix_seconds);
    if observation_seconds < trust_policy.minimum_observation_window_seconds {
        return Err(DifferentialEvidenceError::ObservationWindowBelowPolicyMinimum);
    }
    if document.canary.basis_points < trust_policy.minimum_canary_basis_points {
        return Err(DifferentialEvidenceError::CanaryBelowPolicyMinimum);
    }
    if now.saturating_add(trust_policy.clock_skew_seconds) < document.issued_at_unix_seconds {
        return Err(DifferentialEvidenceError::EvidenceNotYetValid);
    }
    if now
        > document
            .valid_until_unix_seconds
            .saturating_add(trust_policy.clock_skew_seconds)
    {
        return Err(DifferentialEvidenceError::EvidenceExpired);
    }
    Ok(())
}

fn verify_attestation(
    document: &DifferentialEvidenceDocument,
    trust_policy: &EvidenceTrustPolicy,
) -> Result<(), DifferentialEvidenceError> {
    let signer = trust_policy
        .trusted_signers
        .iter()
        .find(|signer| signer.signer_id == document.signer_id)
        .ok_or(DifferentialEvidenceError::UnknownSigner)?;
    let public_key = decode_fixed_hex(&signer.verifying_key_hex, ED25519_PUBLIC_KEY_HEX_LENGTH)
        .ok_or(DifferentialEvidenceError::PolicyInvalidVerifyingKey)?;
    let signature = decode_fixed_hex(&document.signature, ED25519_SIGNATURE_HEX_LENGTH)
        .ok_or(DifferentialEvidenceError::InvalidSignatureEncoding)?;
    let public_key_b64 = URL_SAFE_NO_PAD.encode(public_key);
    let signature_b64 = URL_SAFE_NO_PAD.encode(signature);
    let decoding_key = DecodingKey::from_ed_components(&public_key_b64)
        .map_err(|_| DifferentialEvidenceError::BadSignature)?;
    let payload = document.signing_payload()?;
    let valid = crypto::verify(&signature_b64, &payload, &decoding_key, Algorithm::EdDSA)
        .map_err(|_| DifferentialEvidenceError::BadSignature)?;
    if !valid {
        return Err(DifferentialEvidenceError::BadSignature);
    }
    Ok(())
}

fn validate_commit_sha(value: &str, field: EvidenceField) -> Result<(), DifferentialEvidenceError> {
    let valid_length = value.len() == 40;
    if !valid_length
        || !value
            .bytes()
            .all(|byte| matches!(byte, b'0'..=b'9' | b'a'..=b'f'))
    {
        return Err(DifferentialEvidenceError::InvalidSha { field });
    }
    Ok(())
}

fn validate_digest(value: &str, field: EvidenceField) -> Result<(), DifferentialEvidenceError> {
    if value.len() != SHA256_HEX_LENGTH
        || !value
            .bytes()
            .all(|byte| matches!(byte, b'0'..=b'9' | b'a'..=b'f'))
    {
        return Err(DifferentialEvidenceError::InvalidDigest { field });
    }
    Ok(())
}

fn validate_identifier(
    value: &str,
    field: EvidenceField,
    max_length: usize,
) -> Result<(), DifferentialEvidenceError> {
    if value.is_empty()
        || value.len() > max_length
        || !value.bytes().all(|byte| {
            byte.is_ascii_alphanumeric()
                || matches!(byte, b'.' | b'_' | b':' | b'-' | b'/' | b'@' | b'+')
        })
    {
        return Err(DifferentialEvidenceError::InvalidString { field });
    }
    Ok(())
}

fn validate_canary(canary: &CanaryObservationWindow) -> Result<(), DifferentialEvidenceError> {
    if canary.basis_points > 10_000 {
        return Err(DifferentialEvidenceError::InvalidCanary);
    }
    if canary.ended_at_unix_seconds <= canary.started_at_unix_seconds {
        return Err(DifferentialEvidenceError::InvalidObservationWindow);
    }
    let duration = canary
        .ended_at_unix_seconds
        .saturating_sub(canary.started_at_unix_seconds);
    if duration < MIN_OBSERVATION_WINDOW_SECONDS {
        return Err(DifferentialEvidenceError::ObservationWindowTooShort);
    }
    Ok(())
}

fn validate_reviewer(
    reviewer: &ReviewerApproval,
    canary: &CanaryObservationWindow,
    issued_at_unix_seconds: u64,
) -> Result<(), DifferentialEvidenceError> {
    validate_identifier(
        &reviewer.reviewer_id,
        EvidenceField::ReviewerId,
        MAX_IDENTIFIER_LENGTH,
    )?;
    validate_identifier(
        &reviewer.approval_reference,
        EvidenceField::ApprovalReference,
        MAX_IDENTIFIER_LENGTH,
    )?;
    if !reviewer.approved {
        return Err(DifferentialEvidenceError::ReviewerNotApproved);
    }
    if reviewer.approved_at_unix_seconds < canary.ended_at_unix_seconds {
        return Err(DifferentialEvidenceError::ApprovalBeforeObservationEnd);
    }
    if reviewer.approved_at_unix_seconds >= issued_at_unix_seconds {
        return Err(DifferentialEvidenceError::ApprovalAfterIssue);
    }
    Ok(())
}

fn validate_shadow(
    shadow: &ShadowAggregate,
    scope: RouteOwnershipScope,
) -> Result<(), DifferentialEvidenceError> {
    if shadow.case_count == 0 {
        return Err(DifferentialEvidenceError::InvalidShadowCaseCount);
    }
    if shadow.scope != scope {
        return Err(DifferentialEvidenceError::ShadowScopeMismatch);
    }
    validate_digest(&shadow.fixture_digest, EvidenceField::ShadowFixtureDigest)?;
    validate_digest(&shadow.result_digest, EvidenceField::ShadowResultDigest)?;
    if shadow.result != ShadowResult::Identical {
        return Err(DifferentialEvidenceError::ShadowDifference);
    }
    Ok(())
}

fn validate_differentials(
    differentials: &[DifferentialClassEvidence],
    usage_billing_digest: &str,
) -> Result<(), DifferentialEvidenceError> {
    let mut seen = BTreeSet::new();
    for differential in differentials {
        if !seen.insert(differential.class) {
            return Err(DifferentialEvidenceError::DuplicateDifferential {
                class: differential.class,
            });
        }
        if differential.case_count == 0 {
            return Err(DifferentialEvidenceError::InvalidCaseCount {
                class: differential.class,
            });
        }
        validate_digest(
            &differential.fixture_digest,
            EvidenceField::DifferentialFixtureDigest,
        )?;
        validate_digest(
            &differential.result_digest,
            EvidenceField::DifferentialResultDigest,
        )?;
        if differential.class == DifferentialClass::UsageBilling {
            if differential.result_digest != usage_billing_digest {
                return Err(DifferentialEvidenceError::UsageBillingDigestMismatch);
            }
            if differential.result != DifferentialResult::Match {
                return Err(DifferentialEvidenceError::UsageBillingDifference);
            }
        } else if differential.result != DifferentialResult::Match {
            return Err(DifferentialEvidenceError::DifferentialDifference {
                class: differential.class,
            });
        }
    }
    for class in DifferentialClass::all() {
        if !seen.contains(class) {
            return Err(DifferentialEvidenceError::MissingDifferential { class: *class });
        }
    }
    Ok(())
}

fn validate_registry_binding(
    document: &DifferentialEvidenceDocument,
    registry: &ValidatedRegistry,
) -> Result<(), DifferentialEvidenceError> {
    let expected_fingerprint = registry_fingerprint(registry)?;
    if document.registry_fingerprint != expected_fingerprint {
        return Err(DifferentialEvidenceError::RegistryFingerprintMismatch);
    }
    if document.registry_version != registry.support_matrix().version {
        return Err(DifferentialEvidenceError::RegistryVersionMismatch);
    }
    if document.runtime_catalog_version != registry.runtime_catalog_version() {
        return Err(DifferentialEvidenceError::RuntimeCatalogVersionMismatch);
    }

    let Some(route) = registry.route(document.scope.source, document.scope.target) else {
        return Err(DifferentialEvidenceError::RouteUnavailable);
    };
    if route.quality == Fidelity::Unsupported {
        return Err(DifferentialEvidenceError::RouteQualityUnsupported);
    }
    if !route.request_supported
        || !route.response_supported
        || (document.scope.stream && !route.stream_supported)
    {
        return Err(DifferentialEvidenceError::RouteDirectionUnsupported);
    }
    if !route.matches_model_family(&document.model_family) {
        return Err(DifferentialEvidenceError::ModelConstraintMismatch);
    }
    let feature_classes = canonical_feature_classes(&document.feature_classes)?;
    if feature_classes
        .iter()
        .any(|feature| route.unsupported_features.contains(feature))
    {
        return Err(DifferentialEvidenceError::FeatureUnsupported);
    }
    let required_features = route
        .feature_requirements
        .iter()
        .copied()
        .collect::<Vec<_>>();
    if feature_classes != required_features {
        return Err(DifferentialEvidenceError::FeatureClassSetMismatch);
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol_runtime_registry::{
        current_runtime_catalog, validate_explicit_registry_against_catalog,
        validated_current_registry,
    };
    use lmm_contracts::relay::Registry;
    const PRIVATE_ED25519_KEY_PK8: &[u8] = &[
        0x30, 0x2e, 0x02, 0x01, 0x00, 0x30, 0x05, 0x06, 0x03, 0x2b, 0x65, 0x70, 0x04, 0x22, 0x04,
        0x20, 0x6a, 0xc3, 0xfd, 0xee, 0xee, 0x29, 0x8a, 0x92, 0x63, 0x8b, 0x70, 0x0c, 0x4b, 0x11,
        0x7c, 0xc3, 0x2e, 0x2d, 0x2a, 0xce, 0x0d, 0xfd, 0x78, 0x76, 0x94, 0xe2, 0x4c, 0xae, 0x8a,
        0xd5, 0x82, 0x34,
    ];
    const PUBLIC_ED25519_KEY_HEX: &str =
        "dbe263d94bcd0af42250f3584604a2d1c2523e2248e91b3a0f4513784a50563f";

    type TestResult<T = ()> = Result<T, Box<dyn std::error::Error>>;

    fn test_error(message: &'static str) -> Box<dyn std::error::Error> {
        std::io::Error::other(message).into()
    }

    fn digest(seed: u8) -> String {
        format!("{seed:02x}").repeat(32)
    }

    fn valid_document() -> TestResult<DifferentialEvidenceDocument> {
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::OpenAi,
            stream: true,
        };
        let classes = DifferentialClass::all()
            .iter()
            .enumerate()
            .map(|(index, class)| DifferentialClassEvidence {
                class: *class,
                case_count: 1,
                fixture_digest: digest(index as u8 + 1),
                result_digest: digest(index as u8 + 10),
                result: DifferentialResult::Match,
            })
            .collect::<Vec<_>>();
        let usage_billing_digest = classes
            .iter()
            .find(|value| value.class == DifferentialClass::UsageBilling)
            .map(|value| value.result_digest.clone())
            .ok_or_else(|| test_error("required usage/billing class"))?;
        let registry = validated_current_registry()?;
        let fingerprint = registry_fingerprint(&registry)?;
        let feature_classes = registry
            .route(scope.source, scope.target)
            .ok_or_else(|| test_error("built-in route does not exist"))?
            .feature_requirements
            .iter()
            .copied()
            .collect::<Vec<_>>();
        let mut document = DifferentialEvidenceDocument {
            schema_version: EVIDENCE_SCHEMA_VERSION.to_owned(),
            evidence_id: "evidence-1".to_owned(),
            baseline_go_sha: "a".repeat(40),
            candidate_rust_sha: "b".repeat(40),
            registry_fingerprint: fingerprint,
            registry_version: registry.support_matrix().version.clone(),
            runtime_catalog_version: registry.runtime_catalog_version().to_owned(),
            scope,
            model_family: "gpt-4o".to_owned(),
            feature_classes,
            differentials: classes,
            shadow: ShadowAggregate {
                scope,
                case_count: 1,
                fixture_digest: digest(50),
                result_digest: digest(51),
                result: ShadowResult::Identical,
            },
            usage_billing_digest,
            canary: CanaryObservationWindow {
                started_at_unix_seconds: 1_000,
                ended_at_unix_seconds: 1_060,
                basis_points: MIN_REVIEW_CANARY_BASIS_POINTS,
            },
            reviewer_approval: ReviewerApproval {
                approved: true,
                reviewer_id: "reviewer-1".to_owned(),
                approved_at_unix_seconds: 1_061,
                approval_reference: "review-1".to_owned(),
            },
            issued_at_unix_seconds: 1_062,
            valid_until_unix_seconds: 2_000,
            signer_id: "reviewer-1".to_owned(),
            signature: String::new(),
        };
        sign_document(&mut document)?;
        Ok(document)
    }

    fn trusted_policy() -> EvidenceTrustPolicy {
        EvidenceTrustPolicy {
            allowed_baseline_go_shas: vec!["a".repeat(40)],
            allowed_candidate_rust_shas: vec!["b".repeat(40)],
            trusted_reviewers: vec!["reviewer-1".to_owned()],
            trusted_signers: vec![TrustedSigner {
                signer_id: "reviewer-1".to_owned(),
                verifying_key_hex: PUBLIC_ED25519_KEY_HEX.to_owned(),
            }],
            minimum_observation_window_seconds: MIN_OBSERVATION_WINDOW_SECONDS,
            minimum_canary_basis_points: MIN_REVIEW_CANARY_BASIS_POINTS,
            maximum_evidence_lifetime_seconds: MAX_POLICY_EVIDENCE_LIFETIME_SECONDS,
            now_unix_seconds: 1_500,
            clock_skew_seconds: 0,
            consumed_evidence_ids: BTreeSet::new(),
        }
    }

    /// Test-only guard. Production callers must supply a durable,
    /// cross-process atomic implementation of [`EvidenceReplayGuard`].
    struct TestOnlyReplayGuard {
        consumed: std::sync::Mutex<BTreeSet<String>>,
    }

    impl TestOnlyReplayGuard {
        fn new() -> Self {
            Self {
                consumed: std::sync::Mutex::new(BTreeSet::new()),
            }
        }
    }

    impl EvidenceReplayGuard for TestOnlyReplayGuard {
        fn consume_once(&self, evidence_id: &str) -> Result<(), EvidenceReplayGuardError> {
            let mut consumed = self
                .consumed
                .lock()
                .map_err(|_| EvidenceReplayGuardError::Unavailable)?;
            if consumed.insert(evidence_id.to_owned()) {
                Ok(())
            } else {
                Err(EvidenceReplayGuardError::AlreadyConsumed)
            }
        }
    }

    struct UnavailableReplayGuard;

    impl EvidenceReplayGuard for UnavailableReplayGuard {
        fn consume_once(&self, _evidence_id: &str) -> Result<(), EvidenceReplayGuardError> {
            Err(EvidenceReplayGuardError::Unavailable)
        }
    }

    fn sign_document(document: &mut DifferentialEvidenceDocument) -> TestResult {
        let payload = document.signing_payload()?;
        let signature = jsonwebtoken::crypto::sign(
            &payload,
            &jsonwebtoken::EncodingKey::from_ed_der(PRIVATE_ED25519_KEY_PK8),
            Algorithm::EdDSA,
        )?;
        let signature_bytes = URL_SAFE_NO_PAD.decode(signature)?;
        document.signature = hex::encode(signature_bytes);
        Ok(())
    }

    #[test]
    fn missing_differential_class_stays_closed_at_the_count_boundary() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document
            .differentials
            .retain(|value| value.class != DifferentialClass::Stream);
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::InvalidDifferentialCount {
                expected: DifferentialClass::all().len(),
                actual: DifferentialClass::all().len() - 1,
            })
        );
        Ok(())
    }

    #[test]
    fn malformed_digest_is_rejected_without_body_details() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        let malformed = document
            .differentials
            .first_mut()
            .ok_or_else(|| test_error("fixture has no differential classes"))?;
        malformed.fixture_digest = "not-a-digest".to_owned();
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::InvalidDigest {
                field: EvidenceField::DifferentialFixtureDigest
            })
        );
        Ok(())
    }

    #[test]
    fn shadow_scope_mismatch_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.shadow.scope.stream = false;
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::ShadowScopeMismatch)
        );
        Ok(())
    }

    #[test]
    fn usage_billing_difference_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        let usage = document
            .differentials
            .iter_mut()
            .find(|value| value.class == DifferentialClass::UsageBilling)
            .ok_or_else(|| test_error("required usage/billing class"))?;
        usage.result = DifferentialResult::Difference;
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::UsageBillingDifference)
        );
        Ok(())
    }

    #[test]
    fn shadow_difference_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.shadow.result = ShadowResult::Difference;
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::ShadowDifference)
        );
        Ok(())
    }

    #[test]
    fn supported_cross_protocol_registry_route_validates_with_signed_document() -> TestResult {
        let registry = validated_current_registry()?;
        let scope = RouteOwnershipScope {
            source: Protocol::OpenAi,
            target: Protocol::Claude,
            stream: true,
        };
        let mut document = valid_document()?;
        document.scope = scope;
        document.shadow.scope = scope;
        document.model_family = "claude".to_owned();
        sign_document(&mut document)?;
        document
            .verify(&registry, &trusted_policy())?;
        Ok(())
    }

    #[test]
    fn complete_fixture_only_yields_review_eligibility() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        assert!(verified.eligible_for_review());
        assert_eq!(
            verified.review_decision(),
            OwnershipDecision::EligibleForOwnershipReview {
                scope: verified.document().scope
            }
        );
        Ok(())
    }

    #[test]
    fn atomic_consumption_returns_only_bound_admission_view() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        let feature_classes = verified.document().feature_classes.clone();
        let guard = TestOnlyReplayGuard::new();
        let admission = verified
            .consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            )?;

        assert_eq!(admission.evidence_id(), "evidence-1");
        assert_eq!(admission.scope(), verified.document().scope);
        assert_eq!(admission.model_family(), "gpt-4o");
        assert_eq!(admission.feature_classes(), feature_classes.as_slice());
        assert_eq!(
            admission.registry_fingerprint(),
            verified.document().registry_fingerprint
        );
        assert_eq!(
            admission.registry_version(),
            verified.document().registry_version
        );
        assert_eq!(
            admission.runtime_catalog_version(),
            verified.document().runtime_catalog_version
        );
        assert_eq!(admission.eligible_for_route_admission_at(1_500), Ok(true));
        assert_eq!(
            admission.eligible_for_route_admission_at(2_001),
            Err(DifferentialEvidenceError::EvidenceExpired)
        );
        assert!(admission.ownership.rollout_approved());
        Ok(())
    }

    #[test]
    fn replay_guard_allows_one_admission_only() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        let feature_classes = verified.document().feature_classes.clone();
        let guard = TestOnlyReplayGuard::new();
        verified
            .consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            )?;
        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            ),
            Err(DifferentialEvidenceError::EvidenceIdReplay)
        );
        Ok(())
    }

    #[test]
    fn admission_rechecks_not_before_and_expiry_before_consuming() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        let feature_classes = verified.document().feature_classes.clone();
        let guard = TestOnlyReplayGuard::new();

        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                900,
            ),
            Err(DifferentialEvidenceError::EvidenceNotYetValid)
        );
        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                2_001,
            ),
            Err(DifferentialEvidenceError::EvidenceExpired)
        );

        verified
            .consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            )?;
        Ok(())
    }

    #[test]
    fn admission_rechecks_model_and_complete_feature_binding() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        let feature_classes = verified.document().feature_classes.clone();
        let guard = TestOnlyReplayGuard::new();

        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "other-model",
                &feature_classes,
                1_500,
            ),
            Err(DifferentialEvidenceError::ModelConstraintMismatch)
        );
        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &[Feature::Text],
                1_500,
            ),
            Err(DifferentialEvidenceError::FeatureClassSetMismatch)
        );

        verified
            .consume_for_route_admission(
                &guard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            )?;
        Ok(())
    }

    #[test]
    fn admission_rejects_changed_registry_snapshot() -> TestResult {
        let original_registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&original_registry, &trusted_policy())?;
        let mut registry_definition = Registry::current();
        registry_definition.version = "relay-capabilities-test-v2".to_owned();
        let changed_registry = validate_explicit_registry_against_catalog(
            &registry_definition,
            &current_runtime_catalog(),
        )?;
        let guard = TestOnlyReplayGuard::new();
        let feature_classes = verified.document().feature_classes.clone();

        assert_eq!(
            verified.consume_for_route_admission(
                &guard,
                &changed_registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            ),
            Err(DifferentialEvidenceError::RegistryFingerprintMismatch)
        );
        Ok(())
    }

    #[test]
    fn unavailable_replay_store_stays_closed() -> TestResult {
        let registry = validated_current_registry()?;
        let verified = valid_document()?
            .verify(&registry, &trusted_policy())?;
        let feature_classes = verified.document().feature_classes.clone();

        assert_eq!(
            verified.consume_for_route_admission(
                &UnavailableReplayGuard,
                &registry,
                &trusted_policy(),
                "gpt-4o",
                &feature_classes,
                1_500,
            ),
            Err(DifferentialEvidenceError::ReplayGuardUnavailable)
        );
        Ok(())
    }

    #[test]
    fn text_only_feature_evidence_cannot_green_raw_route() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.feature_classes = vec![Feature::Text];
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::FeatureClassSetMismatch)
        );
        Ok(())
    }

    #[test]
    fn feature_class_sets_are_bounded_and_unique() -> TestResult {
        let registry = validated_current_registry()?;
        let mut empty = valid_document()?;
        empty.feature_classes.clear();
        assert_eq!(
            empty.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::EmptyFeatureClasses)
        );

        let mut duplicate = valid_document()?;
        duplicate.feature_classes = vec![Feature::Text, Feature::Text];
        assert_eq!(
            duplicate.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::DuplicateFeatureClass {
                feature: Feature::Text
            })
        );

        let mut oversized = valid_document()?;
        oversized.feature_classes = vec![Feature::Text; MAX_FEATURE_CLASSES + 1];
        assert_eq!(
            oversized.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::TooManyFeatureClasses)
        );
        Ok(())
    }

    #[test]
    fn self_filled_green_without_signature_stays_closed() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.signature.clear();
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::InvalidSignatureEncoding)
        );
        Ok(())
    }

    #[test]
    fn trusted_policy_without_keys_stays_closed() -> TestResult {
        let registry = validated_current_registry()?;
        let mut policy = trusted_policy();
        policy.trusted_signers.clear();
        assert_eq!(
            valid_document()?.verify(&registry, &policy),
            Err(DifferentialEvidenceError::PolicyEmptyTrustedSigners)
        );
        Ok(())
    }

    #[test]
    fn unknown_signer_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.signer_id = "unknown-signer".to_owned();
        document.reviewer_approval.reviewer_id = "unknown-signer".to_owned();
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::UnknownSigner)
        );
        Ok(())
    }

    #[test]
    fn signed_field_tampering_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.reviewer_approval.approval_reference = "review-2".to_owned();
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::BadSignature)
        );
        Ok(())
    }

    #[test]
    fn policy_sha_mismatch_is_rejected_before_signature() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        document.baseline_go_sha = "c".repeat(40);
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::BaselineShaNotAllowed)
        );
        Ok(())
    }

    #[test]
    fn expired_signed_evidence_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut policy = trusted_policy();
        policy.now_unix_seconds = 2_001;
        assert_eq!(
            valid_document()?.verify(&registry, &policy),
            Err(DifferentialEvidenceError::EvidenceExpired)
        );
        Ok(())
    }

    #[test]
    fn policy_rejects_zero_or_overlong_evidence_lifetime() -> TestResult {
        let mut zero = trusted_policy();
        zero.maximum_evidence_lifetime_seconds = 0;
        assert_eq!(
            zero.validate(),
            Err(DifferentialEvidenceError::PolicyInvalidEvidenceLifetime)
        );

        let mut too_long = trusted_policy();
        too_long.maximum_evidence_lifetime_seconds = MAX_POLICY_EVIDENCE_LIFETIME_SECONDS + 1;
        assert_eq!(
            too_long.validate(),
            Err(DifferentialEvidenceError::PolicyInvalidEvidenceLifetime)
        );
        Ok(())
    }

    #[test]
    fn evidence_lifetime_over_policy_is_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut policy = trusted_policy();
        policy.maximum_evidence_lifetime_seconds = 60;
        assert_eq!(
            valid_document()?.verify(&registry, &policy),
            Err(DifferentialEvidenceError::EvidenceLifetimeExceedsPolicy)
        );
        Ok(())
    }

    #[test]
    fn consumed_evidence_id_is_rejected_for_replay() -> TestResult {
        let registry = validated_current_registry()?;
        let mut policy = trusted_policy();
        policy.consumed_evidence_ids.insert("evidence-1".to_owned());
        assert_eq!(
            valid_document()?.verify(&registry, &policy),
            Err(DifferentialEvidenceError::EvidenceIdReplay)
        );
        Ok(())
    }

    #[test]
    fn duplicate_class_and_route_are_rejected() -> TestResult {
        let registry = validated_current_registry()?;
        let mut duplicate = valid_document()?;
        duplicate
            .differentials
            .retain(|value| value.class != DifferentialClass::Stream);
        let duplicate_entry = duplicate
            .differentials
            .first()
            .ok_or_else(|| test_error("fixture has no differential classes"))?
            .clone();
        duplicate.differentials.push(duplicate_entry);
        assert_eq!(
            duplicate.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::DuplicateDifferential {
                class: DifferentialClass::NonStream
            })
        );

        let bundle = DifferentialEvidenceBundle {
            schema_version: EVIDENCE_SCHEMA_VERSION.to_owned(),
            documents: vec![valid_document()?, valid_document()?],
        };
        assert_eq!(
            bundle.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::DuplicateRoute {
                source: Protocol::OpenAi,
                target: Protocol::OpenAi,
                stream: true,
            })
        );
        Ok(())
    }

    #[test]
    fn oversized_json_is_rejected_before_deserialization() -> TestResult {
        let input = "{".repeat(MAX_EVIDENCE_JSON_BYTES + 1);
        assert_eq!(
            DifferentialEvidenceDocument::from_json(&input),
            Err(DifferentialEvidenceError::InputTooLarge)
        );
        Ok(())
    }

    #[test]
    fn too_many_differentials_are_rejected_before_deep_validation() -> TestResult {
        let registry = validated_current_registry()?;
        let mut document = valid_document()?;
        let extra_differential = document
            .differentials
            .first()
            .ok_or_else(|| test_error("fixture has no differential classes"))?
            .clone();
        document.differentials.push(extra_differential);
        assert_eq!(
            document.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::InvalidDifferentialCount {
                expected: DifferentialClass::all().len(),
                actual: DifferentialClass::all().len() + 1,
            })
        );
        Ok(())
    }

    #[test]
    fn too_many_bundle_documents_are_rejected_before_deep_validation() -> TestResult {
        let registry = validated_current_registry()?;
        let bundle = DifferentialEvidenceBundle {
            schema_version: EVIDENCE_SCHEMA_VERSION.to_owned(),
            documents: (0..=MAX_BUNDLE_DOCUMENTS)
                .map(|_| valid_document())
                .collect::<TestResult<Vec<_>>>()?,
        };
        assert_eq!(
            bundle.verify(&registry, &trusted_policy()),
            Err(DifferentialEvidenceError::TooManyBundleDocuments)
        );
        Ok(())
    }

    #[test]
    fn too_many_bundle_documents_are_rejected_on_import() -> TestResult {
        let bundle = DifferentialEvidenceBundle {
            schema_version: EVIDENCE_SCHEMA_VERSION.to_owned(),
            documents: (0..=MAX_BUNDLE_DOCUMENTS)
                .map(|_| valid_document())
                .collect::<TestResult<Vec<_>>>()?,
        };
        let input = serde_json::to_string(&bundle)?;
        assert_eq!(
            DifferentialEvidenceBundle::from_json(&input),
            Err(DifferentialEvidenceError::TooManyBundleDocuments)
        );
        Ok(())
    }

    #[test]
    fn unknown_json_fields_are_rejected() -> TestResult {
        let document = valid_document()?;
        let mut value = serde_json::to_value(document)?;
        value
            .as_object_mut()
            .ok_or_else(|| test_error("serialized document is not an object"))?
            .insert(
                "verifying_key_hex".to_owned(),
                serde_json::Value::String(PUBLIC_ED25519_KEY_HEX.to_owned()),
            );
        assert_eq!(
            DifferentialEvidenceDocument::from_json(&value.to_string()),
            Err(DifferentialEvidenceError::InvalidJson)
        );
        Ok(())
    }

    #[test]
    fn unknown_nested_scope_fields_are_rejected() -> TestResult {
        let document = valid_document()?;
        let mut value = serde_json::to_value(document)?;
        value
            .get_mut("scope")
            .and_then(serde_json::Value::as_object_mut)
            .ok_or_else(|| test_error("serialized scope is not an object"))?
            .insert("unexpected".to_owned(), serde_json::Value::Null);
        assert_eq!(
            DifferentialEvidenceDocument::from_json(&value.to_string()),
            Err(DifferentialEvidenceError::InvalidJson)
        );
        Ok(())
    }
}
