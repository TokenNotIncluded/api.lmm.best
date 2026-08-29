//! Closed-by-default local shadow coordination.
//!
//! A shadow comparison is deliberately narrower than a relay.  This module
//! receives one already-owned input view and two local converter objects; it
//! has no HTTP client, request builder, response body owner, or upstream call
//! path.  Consequently a response can be shadowed only after the production
//! caller has received the one real upstream response.  Native raw responses
//! are never buffered or tee'd here.
//!
//! The rollout snapshot is supplied by the caller so the request and response
//! sides can share the exact same immutable decision.  No request key, model
//! name, input bytes, response bytes, or converter error text is retained in
//! a record or aggregate.
//!
//! This module is not connected to a router in this batch.  A streaming
//! [`SourceEventSummary`] is only a body-free structural shadow view; by
//! itself it is not ownership evidence and cannot certify differential or
//! rollout acceptance.

use std::{
    fmt,
    num::NonZeroUsize,
    sync::{
        atomic::{AtomicU64, AtomicUsize, Ordering},
        Arc,
    },
};

use lmm_contracts::relay::Protocol;
use sha2::{Digest, Sha256};

use crate::protocol_rollout::{
    bucket_is_in_rollout, LocalConversionError, LocalConversionErrorKind, LocalConversionSummary,
    LocalConverter, LocalRequest, ProtocolRolloutSnapshot, ProtocolRolloutSnapshotStatus,
    ShadowDifference, MAX_BASIS_POINTS,
};

const SHADOW_HASH_DOMAIN: &[u8] = b"lmm-protocol-shadow-v1\0";

/// Input limits and the switch controlling local shadow execution.
///
/// `None` for `max_concurrency` is intentionally not treated as unlimited:
/// an enabled coordinator with no finite bound fails closed with
/// [`ShadowSkipReason::UnboundedConcurrency`].
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct ShadowConfig {
    enabled: bool,
    max_input_bytes: usize,
    max_concurrency: Option<NonZeroUsize>,
    allowed_scopes: Vec<ShadowScope>,
    canary_basis_points: u16,
}

impl ShadowConfig {
    /// Creates a coordinator configuration.
    ///
    /// A disabled configuration may retain zero limits because it performs no
    /// work.  An enabled configuration must provide a positive byte limit;
    /// an absent concurrency limit remains valid configuration data but is
    /// rejected at admission with a typed skip reason.
    pub fn new(
        enabled: bool,
        max_input_bytes: usize,
        max_concurrency: Option<NonZeroUsize>,
        allowed_scopes: Vec<ShadowScope>,
        canary_basis_points: u16,
    ) -> Result<Self, ShadowConfigError> {
        if enabled && max_input_bytes == 0 {
            return Err(ShadowConfigError::ZeroMaxInputBytes);
        }
        if canary_basis_points > MAX_BASIS_POINTS {
            return Err(ShadowConfigError::CanaryOutOfRange);
        }
        Ok(Self {
            enabled,
            max_input_bytes,
            max_concurrency,
            allowed_scopes,
            canary_basis_points,
        })
    }

    /// Creates an enabled configuration with a finite concurrency bound.
    pub fn enabled(
        max_input_bytes: usize,
        max_concurrency: NonZeroUsize,
        allowed_scopes: Vec<ShadowScope>,
        canary_basis_points: u16,
    ) -> Result<Self, ShadowConfigError> {
        Self::new(
            true,
            max_input_bytes,
            Some(max_concurrency),
            allowed_scopes,
            canary_basis_points,
        )
    }

    /// Returns the safe default, which performs no shadow work.
    #[must_use]
    pub fn disabled() -> Self {
        Self::default()
    }

    /// Whether local shadow execution is configured.
    #[must_use]
    pub const fn is_enabled(&self) -> bool {
        self.enabled
    }

    /// Maximum bytes accepted from one request or response input view.
    #[must_use]
    pub const fn max_input_bytes(&self) -> usize {
        self.max_input_bytes
    }

    /// Finite maximum number of active local comparisons, when configured.
    #[must_use]
    pub const fn max_concurrency(&self) -> Option<NonZeroUsize> {
        self.max_concurrency
    }

    /// Returns the exact source/target/stream scopes allowed for shadowing.
    #[must_use]
    pub fn allowed_scopes(&self) -> &[ShadowScope] {
        &self.allowed_scopes
    }

    /// Returns the independent shadow canary allocation in basis points.
    #[must_use]
    pub const fn canary_basis_points(&self) -> u16 {
        self.canary_basis_points
    }
}

/// Configuration errors detected before a coordinator can be enabled.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ShadowConfigError {
    /// An enabled coordinator cannot accept an empty input limit.
    ZeroMaxInputBytes,
    /// A canary allocation above 100% is invalid.
    CanaryOutOfRange,
}

/// Closed reasons for a shadow comparison that did not execute.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum ShadowSkipReason {
    /// The local shadow feature flag is disabled in the coordinator config.
    FlagDisabled,
    /// The exact source/target/stream scope is not allowlisted.
    ScopeNotAllowed,
    /// The stable request key is outside the independent shadow canary.
    CanaryExcluded,
    /// An empty request key cannot be sampled safely.
    EmptyRequestKey,
    /// The immutable rollout snapshot is an explicit rollback.
    RollbackActive,
    /// The rollout control lock was poisoned and supplied a fail-closed view.
    RolloutControlPoisoned,
    /// The caller's input exceeds the configured hard limit.
    InputTooLarge,
    /// A finite concurrency bound is currently exhausted.
    ConcurrencyLimit,
    /// No finite concurrency bound was configured.
    UnboundedConcurrency,
    /// Native raw production responses are never eligible for shadowing.
    NativeRawNotEligible,
    /// Streaming shadowing accepts only a source event view, never bytes.
    StreamingBytesNotEligible,
    /// A source event view is invalid for a non-streaming scope.
    NonStreamingSourceEvents,
}

/// The exact low-cardinality route dimensions used for rollout selection.
///
/// The model family is intentionally not retained here. Shadow selection is
/// governed by this exact route scope and the independent canary policy.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ShadowScope {
    source: Protocol,
    target: Protocol,
    stream: bool,
}

impl ShadowScope {
    /// Creates one exact source/target/stream scope.
    #[must_use]
    pub const fn new(source: Protocol, target: Protocol, stream: bool) -> Self {
        Self {
            source,
            target,
            stream,
        }
    }

    /// Source protocol.
    #[must_use]
    pub const fn source(self) -> Protocol {
        self.source
    }

    /// Target protocol.
    #[must_use]
    pub const fn target(self) -> Protocol {
        self.target
    }

    /// Whether this scope is streaming.
    #[must_use]
    pub const fn stream(self) -> bool {
        self.stream
    }
}

fn shadow_bucket(request_key: &str, scope: ShadowScope) -> u16 {
    let mut hasher = Sha256::new();
    hasher.update(SHADOW_HASH_DOMAIN);
    hasher.update(request_key.as_bytes());
    hasher.update([protocol_code(scope.source), protocol_code(scope.target)]);
    hasher.update([u8::from(scope.stream)]);
    let digest = hasher.finalize();
    let mut prefix = [0_u8; 8];
    prefix.copy_from_slice(&digest[..8]);
    (u64::from_be_bytes(prefix) % u64::from(MAX_BASIS_POINTS)) as u16
}

const fn protocol_code(protocol: Protocol) -> u8 {
    match protocol {
        Protocol::OpenAi => 0,
        Protocol::OpenAiResponses => 1,
        Protocol::Claude => 2,
        Protocol::Gemini => 3,
    }
}

/// The side of a local comparison that failed.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum ShadowConverterSide {
    /// The old/v1 local converter.
    Old,
    /// The new/v2 local converter.
    New,
}

/// A converter failure with no error text or input content.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ShadowConverterFailure {
    /// Which local implementation failed.
    pub side: ShadowConverterSide,
    /// Closed reason safe for aggregation.
    pub kind: LocalConversionErrorKind,
}

/// The operation for which the two local converters were compared.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd)]
pub enum ShadowOperation {
    /// Two local request converters saw the same inbound bytes.
    Request,
    /// Two local response converters saw the same already-obtained input.
    Response,
}

/// A converter result held by a body-free shadow record.
#[derive(Clone, Eq, PartialEq)]
pub enum ConverterObservation {
    /// The converter returned a body-free semantic summary.
    Summary(LocalConversionSummary),
    /// The converter returned a typed error.
    Failed(LocalConversionError),
}

impl fmt::Debug for ConverterObservation {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::Summary(_) => formatter.write_str("Summary(..)"),
            Self::Failed(error) => formatter.debug_tuple("Failed").field(&error.kind).finish(),
        }
    }
}

/// A body-free local shadow comparison.
///
/// This type intentionally contains no input or output body, request key,
/// model name, response stream, or error text.  It is not serializable by
/// design; callers can export only the closed aggregate returned by
/// [`ProtocolShadowCoordinator::aggregate`].
#[derive(Clone, Eq, PartialEq)]
pub struct ShadowRecord {
    /// Whether this compares request or response conversion.
    pub operation: ShadowOperation,
    /// Closed source/target/stream dimensions.
    pub scope: ShadowScope,
    /// Immutable rollout generation used for this decision.
    pub rollout_generation: u64,
    /// Old/v1 local result.
    pub old: ConverterObservation,
    /// New/v2 local result.
    pub new: ConverterObservation,
    /// Closed semantic difference categories.
    pub differences: Vec<ShadowDifference>,
    /// Explicit typed converter failures, including which side failed.
    pub failures: Vec<ShadowConverterFailure>,
    /// Whether converter implementation identifiers differ; diagnostic only.
    pub converter_id_differed: bool,
}

impl ShadowRecord {
    /// Returns whether semantic outputs and typed errors are equivalent.
    #[must_use]
    pub fn semantic_identical(&self) -> bool {
        self.differences.is_empty()
    }

    /// Alias for [`Self::semantic_identical`].
    #[must_use]
    pub fn is_identical(&self) -> bool {
        self.semantic_identical()
    }
}

impl fmt::Debug for ShadowRecord {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        formatter
            .debug_struct("ShadowRecord")
            .field("operation", &self.operation)
            .field("scope", &self.scope)
            .field("rollout_generation", &self.rollout_generation)
            .field("differences", &self.differences)
            .field("failure_count", &self.failures.len())
            .finish()
    }
}

/// Result of an attempted shadow comparison.
#[allow(clippy::large_enum_variant)]
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum ShadowOutcome {
    /// Both local converters ran once and produced a body-free record.
    Recorded(ShadowRecord),
    /// Admission rejected the comparison before either converter ran.
    Skipped(ShadowSkipReason),
}

/// Input already owned by a response caller.
///
/// The coordinator borrows this view only for the duration of the two local
/// converter calls. `Bytes` is not copied and is intended only for a bounded,
/// non-stream response already owned by the caller. `SourceEvents` is an
/// independent source-event view, not a result from either converter. Native
/// raw input has an explicit rejected variant and carries no body.
pub enum ResponseShadowInput<'a> {
    /// The caller's already-obtained response bytes.
    Bytes(&'a [u8]),
    /// A body-free source event view for a streaming response.
    SourceEvents(&'a SourceEventSummary),
    /// Explicit marker for a native raw production response; always rejected.
    NativeRaw { input_bytes: usize },
}

impl<'a> ResponseShadowInput<'a> {
    /// Returns the input size used for admission without copying bytes.
    #[must_use]
    pub const fn input_bytes(&self) -> usize {
        match self {
            Self::Bytes(bytes) => bytes.len(),
            Self::SourceEvents(summary) => summary.input_bytes,
            Self::NativeRaw { input_bytes } => *input_bytes,
        }
    }

    /// Borrows response bytes when the caller supplied bytes.
    #[must_use]
    pub const fn bytes(&self) -> Option<&'a [u8]> {
        match self {
            Self::Bytes(bytes) => Some(*bytes),
            Self::SourceEvents(_) | Self::NativeRaw { .. } => None,
        }
    }

    /// Borrows an independent source event view when the caller supplied one.
    #[must_use]
    pub const fn source_events(&self) -> Option<&'a SourceEventSummary> {
        match self {
            Self::SourceEvents(summary) => Some(*summary),
            Self::Bytes(_) | Self::NativeRaw { .. } => None,
        }
    }

    fn is_native_raw(&self) -> bool {
        matches!(self, Self::NativeRaw { .. })
    }
}

/// Body-free source event metadata supplied by a streaming caller.
///
/// This is independent of either converter's output summary. A caller may
/// compute it while consuming the one production stream, then pass this
/// bounded view to both local response converters without buffering or teeing
/// the raw stream.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct SourceEventSummary {
    input_bytes: usize,
    event_count: u32,
    terminal: bool,
    error: bool,
    shape_fingerprint: [u8; 32],
}

impl SourceEventSummary {
    /// Creates a body-free source event view.
    #[must_use]
    pub const fn new(
        input_bytes: usize,
        event_count: u32,
        terminal: bool,
        error: bool,
        shape_fingerprint: [u8; 32],
    ) -> Self {
        Self {
            input_bytes,
            event_count,
            terminal,
            error,
            shape_fingerprint,
        }
    }

    /// Original source input byte count.
    #[must_use]
    pub const fn input_bytes(self) -> usize {
        self.input_bytes
    }

    /// Number of source events observed.
    #[must_use]
    pub const fn event_count(self) -> u32 {
        self.event_count
    }

    /// Whether the source stream reached a terminal event.
    #[must_use]
    pub const fn terminal(self) -> bool {
        self.terminal
    }

    /// Whether the source stream contained an error event.
    #[must_use]
    pub const fn error(self) -> bool {
        self.error
    }

    /// Opaque shape fingerprint supplied by the typed event collector.
    #[must_use]
    pub const fn shape_fingerprint(self) -> [u8; 32] {
        self.shape_fingerprint
    }
}

/// A local response converter. Implementations receive no network client or
/// route state and must return only a body-free summary. Converter failures
/// must be returned as [`LocalConversionError`]; process isolation for a
/// misbehaving converter belongs in an external worker boundary.
pub trait LocalResponseConverter {
    /// Converts one already-obtained response input locally.
    fn convert_response_local(
        &self,
        input: &ResponseShadowInput<'_>,
    ) -> Result<LocalConversionSummary, LocalConversionError>;
}

impl<F> LocalResponseConverter for F
where
    F: for<'a> Fn(&ResponseShadowInput<'a>) -> Result<LocalConversionSummary, LocalConversionError>,
{
    fn convert_response_local(
        &self,
        input: &ResponseShadowInput<'_>,
    ) -> Result<LocalConversionSummary, LocalConversionError> {
        self(input)
    }
}

/// Low-cardinality shadow totals.
///
/// No request key, model family, bytes, converter identifier, or fingerprint
/// is used as a metric dimension.  All counters saturate at `u64::MAX`.
#[derive(Clone, Copy, Debug, Default, Eq, PartialEq)]
pub struct ShadowAggregateSnapshot {
    /// Comparisons admitted and completed.
    pub compared: u64,
    /// Comparisons with equivalent summaries.
    pub identical: u64,
    /// Comparisons with semantic or conversion differences.
    pub differences: u64,
    /// Comparisons with at least one converter failure.
    pub converter_failures: u64,
    /// Comparisons where v1/v2 converter identifiers differ (diagnostic only).
    pub converter_id_differences: u64,
    /// Comparisons skipped before invoking either converter.
    pub skipped: u64,
    /// Skipped because the coordinator feature flag is disabled.
    pub skipped_flag_disabled: u64,
    /// Skipped because the exact route scope is not allowlisted.
    pub skipped_scope_not_allowed: u64,
    /// Skipped because the request key is outside the shadow canary.
    pub skipped_canary_excluded: u64,
    /// Skipped because the request key is empty.
    pub skipped_empty_request_key: u64,
    /// Skipped because an explicit rollback is active.
    pub skipped_rollback: u64,
    /// Skipped because rollout control is poisoned.
    pub skipped_rollout_control_poisoned: u64,
    /// Skipped because the input is over the hard byte limit.
    pub skipped_input_too_large: u64,
    /// Skipped because all finite concurrency slots are occupied.
    pub skipped_concurrency_limit: u64,
    /// Skipped because no finite concurrency limit was configured.
    pub skipped_unbounded_concurrency: u64,
    /// Skipped because the response was marked native raw.
    pub skipped_native_raw: u64,
    /// Skipped because stream input was supplied as bytes instead of events.
    pub skipped_streaming_bytes: u64,
    /// Skipped because source events were supplied for a non-stream scope.
    pub skipped_non_streaming_source_events: u64,
}

impl ShadowAggregateSnapshot {
    /// Maps shadow comparison failures into the pure rollout signal set.
    ///
    /// The existing rollout signal set has no separate shadow-difference bit.
    /// Treating a difference or converter failure as a silent-loss signal is
    /// deliberately conservative: it pauses further canary expansion until a
    /// caller has reviewed the body-free aggregate.
    #[must_use]
    pub fn rollback_signals(&self) -> crate::protocol_rollout::RollbackSignals {
        crate::protocol_rollout::RollbackSignals {
            silent_loss: self.differences > 0 || self.converter_failures > 0,
            ..crate::protocol_rollout::RollbackSignals::default()
        }
    }

    /// Records the aggregate counters using the bounded conversion observer.
    ///
    /// This snapshot is cumulative. Callers polling it must supply a delta (or
    /// emit it only once per rollout generation) to avoid double-counting.
    /// Canary and admission skips are unsupported shadow attempts, not unknown
    /// provider events, so they remain in the normal event counter with an
    /// `unsupported` result. Only actual comparison differences use the
    /// unknown-event counter.
    pub fn record_observability(
        &self,
        observer: &crate::conversion_observability::ConversionObserver,
        labels: crate::conversion_observability::MetricLabels,
    ) {
        observer.record_events(labels, self.compared);
        observer.record(
            crate::conversion_observability::MetricKind::ConversionFailuresTotal,
            labels
                .with_result(crate::conversion_observability::ConversionResult::Failure)
                .with_failure_reason(crate::conversion_observability::FailureReason::Unknown),
            self.converter_failures,
        );
        observer.record_events(
            labels.with_result(crate::conversion_observability::ConversionResult::Unsupported),
            self.skipped,
        );
        observer.record(
            crate::conversion_observability::MetricKind::ConversionUnknownEventsTotal,
            labels.with_feature_class(crate::conversion_observability::FeatureClass::Stream),
            self.differences,
        );
    }
}

#[derive(Default)]
struct ShadowCounters {
    compared: AtomicU64,
    identical: AtomicU64,
    differences: AtomicU64,
    converter_failures: AtomicU64,
    converter_id_differences: AtomicU64,
    skipped: AtomicU64,
    skipped_flag_disabled: AtomicU64,
    skipped_scope_not_allowed: AtomicU64,
    skipped_canary_excluded: AtomicU64,
    skipped_empty_request_key: AtomicU64,
    skipped_rollback: AtomicU64,
    skipped_rollout_control_poisoned: AtomicU64,
    skipped_input_too_large: AtomicU64,
    skipped_concurrency_limit: AtomicU64,
    skipped_unbounded_concurrency: AtomicU64,
    skipped_native_raw: AtomicU64,
    skipped_streaming_bytes: AtomicU64,
    skipped_non_streaming_source_events: AtomicU64,
}

impl ShadowCounters {
    fn increment(counter: &AtomicU64) {
        let _ = counter.fetch_update(Ordering::Relaxed, Ordering::Relaxed, |value| {
            Some(value.saturating_add(1))
        });
    }

    fn record(&self, record: &ShadowRecord) {
        Self::increment(&self.compared);
        if record.semantic_identical() {
            Self::increment(&self.identical);
        } else {
            Self::increment(&self.differences);
        }
        if !record.failures.is_empty() {
            Self::increment(&self.converter_failures);
        }
        if record.converter_id_differed {
            Self::increment(&self.converter_id_differences);
        }
    }

    fn record_skip(&self, reason: ShadowSkipReason) {
        Self::increment(&self.skipped);
        match reason {
            ShadowSkipReason::FlagDisabled => Self::increment(&self.skipped_flag_disabled),
            ShadowSkipReason::ScopeNotAllowed => Self::increment(&self.skipped_scope_not_allowed),
            ShadowSkipReason::CanaryExcluded => Self::increment(&self.skipped_canary_excluded),
            ShadowSkipReason::EmptyRequestKey => Self::increment(&self.skipped_empty_request_key),
            ShadowSkipReason::RollbackActive => Self::increment(&self.skipped_rollback),
            ShadowSkipReason::RolloutControlPoisoned => {
                Self::increment(&self.skipped_rollout_control_poisoned)
            }
            ShadowSkipReason::InputTooLarge => Self::increment(&self.skipped_input_too_large),
            ShadowSkipReason::ConcurrencyLimit => Self::increment(&self.skipped_concurrency_limit),
            ShadowSkipReason::UnboundedConcurrency => {
                Self::increment(&self.skipped_unbounded_concurrency)
            }
            ShadowSkipReason::NativeRawNotEligible => Self::increment(&self.skipped_native_raw),
            ShadowSkipReason::StreamingBytesNotEligible => {
                Self::increment(&self.skipped_streaming_bytes)
            }
            ShadowSkipReason::NonStreamingSourceEvents => {
                Self::increment(&self.skipped_non_streaming_source_events)
            }
        }
    }

    fn snapshot(&self) -> ShadowAggregateSnapshot {
        let load = |counter: &AtomicU64| counter.load(Ordering::Relaxed);
        ShadowAggregateSnapshot {
            compared: load(&self.compared),
            identical: load(&self.identical),
            differences: load(&self.differences),
            converter_failures: load(&self.converter_failures),
            converter_id_differences: load(&self.converter_id_differences),
            skipped: load(&self.skipped),
            skipped_flag_disabled: load(&self.skipped_flag_disabled),
            skipped_scope_not_allowed: load(&self.skipped_scope_not_allowed),
            skipped_canary_excluded: load(&self.skipped_canary_excluded),
            skipped_empty_request_key: load(&self.skipped_empty_request_key),
            skipped_rollback: load(&self.skipped_rollback),
            skipped_rollout_control_poisoned: load(&self.skipped_rollout_control_poisoned),
            skipped_input_too_large: load(&self.skipped_input_too_large),
            skipped_concurrency_limit: load(&self.skipped_concurrency_limit),
            skipped_unbounded_concurrency: load(&self.skipped_unbounded_concurrency),
            skipped_native_raw: load(&self.skipped_native_raw),
            skipped_streaming_bytes: load(&self.skipped_streaming_bytes),
            skipped_non_streaming_source_events: load(&self.skipped_non_streaming_source_events),
        }
    }
}

/// Immutable eligibility metadata for one admitted comparison.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ShadowEligibility {
    /// Exact route scope selected by the rollout decision.
    pub scope: ShadowScope,
    /// Snapshot generation used for the decision.
    pub rollout_generation: u64,
}

/// Cloneable coordinator for bounded, local-only shadow comparisons.
#[derive(Clone)]
pub struct ProtocolShadowCoordinator {
    config: ShadowConfig,
    active: Arc<AtomicUsize>,
    counters: Arc<ShadowCounters>,
}

impl Default for ProtocolShadowCoordinator {
    fn default() -> Self {
        Self::new(ShadowConfig::disabled())
    }
}

impl ProtocolShadowCoordinator {
    /// Creates a coordinator.  The safe default is disabled.
    #[must_use]
    pub fn new(config: ShadowConfig) -> Self {
        Self {
            config,
            active: Arc::new(AtomicUsize::new(0)),
            counters: Arc::new(ShadowCounters::default()),
        }
    }

    /// Returns this coordinator's immutable configuration.
    #[must_use]
    pub fn config(&self) -> &ShadowConfig {
        &self.config
    }

    /// Returns low-cardinality counters accumulated by all clones.
    #[must_use]
    pub fn aggregate(&self) -> ShadowAggregateSnapshot {
        self.counters.snapshot()
    }

    /// Evaluates only feature/rollout eligibility for one exact scope.
    ///
    /// The request key is borrowed solely while the independent deterministic
    /// shadow canary decision is computed. It is never retained.
    pub fn check_eligibility(
        &self,
        snapshot: &ProtocolRolloutSnapshot,
        scope: ShadowScope,
        request_key: &str,
    ) -> Result<ShadowEligibility, ShadowSkipReason> {
        if !self.config.enabled {
            return Err(ShadowSkipReason::FlagDisabled);
        }
        if !self.config.allowed_scopes.contains(&scope) {
            return Err(ShadowSkipReason::ScopeNotAllowed);
        }
        if request_key.is_empty() {
            return Err(ShadowSkipReason::EmptyRequestKey);
        }
        match snapshot.status() {
            ProtocolRolloutSnapshotStatus::Rollback => {
                return Err(ShadowSkipReason::RollbackActive);
            }
            ProtocolRolloutSnapshotStatus::LockPoisoned => {
                return Err(ShadowSkipReason::RolloutControlPoisoned);
            }
            ProtocolRolloutSnapshotStatus::Active => {}
        }
        if snapshot.is_fail_closed() {
            return Err(ShadowSkipReason::RollbackActive);
        }
        if !bucket_is_in_rollout(
            shadow_bucket(request_key, scope),
            self.config.canary_basis_points,
        ) {
            return Err(ShadowSkipReason::CanaryExcluded);
        }
        Ok(ShadowEligibility {
            scope,
            rollout_generation: snapshot.generation(),
        })
    }

    /// Runs old and new local request converters once each for the same input.
    ///
    /// The converters receive a borrowed [`LocalRequest`] and no upstream
    /// client.  If admission succeeds, each converter is invoked exactly once
    /// even when the other converter returns a typed error.
    pub fn shadow_request<Old, New>(
        &self,
        snapshot: &ProtocolRolloutSnapshot,
        scope: ShadowScope,
        request_key: &str,
        inbound_bytes: &[u8],
        old: &Old,
        new: &New,
    ) -> ShadowOutcome
    where
        Old: LocalConverter + ?Sized,
        New: LocalConverter + ?Sized,
    {
        if scope.source == scope.target {
            return self.skipped(ShadowSkipReason::NativeRawNotEligible);
        }
        let admission = match self.admit(snapshot, scope, request_key, inbound_bytes.len()) {
            Ok(eligibility) => eligibility,
            Err(reason) => return self.skipped(reason),
        };
        let request = LocalRequest::new(inbound_bytes);
        let old_result = invoke_request_converter(old, &request);
        let new_result = invoke_request_converter(new, &request);
        let record = build_record(
            ShadowOperation::Request,
            admission.eligibility,
            old_result,
            new_result,
        );
        drop(admission.permit);
        self.counters.record(&record);
        ShadowOutcome::Recorded(record)
    }

    /// Runs old and new local response converters once each for one input
    /// already obtained by the caller.
    ///
    /// This method never owns a response stream and never performs network
    /// I/O. Passing [`ResponseShadowInput::Bytes`] does not make the
    /// coordinator buffer or tee a production body; the caller owns that
    /// already-obtained bounded non-stream byte slice. Native raw production
    /// paths are rejected by [`ResponseShadowInput::NativeRaw`].
    pub fn shadow_response<Old, New>(
        &self,
        snapshot: &ProtocolRolloutSnapshot,
        scope: ShadowScope,
        request_key: &str,
        input: &ResponseShadowInput<'_>,
        old: &Old,
        new: &New,
    ) -> ShadowOutcome
    where
        Old: LocalResponseConverter + ?Sized,
        New: LocalResponseConverter + ?Sized,
    {
        if scope.source == scope.target {
            return self.skipped(ShadowSkipReason::NativeRawNotEligible);
        }
        if input.is_native_raw() {
            return self.skipped(ShadowSkipReason::NativeRawNotEligible);
        }
        if scope.stream && matches!(input, ResponseShadowInput::Bytes(_)) {
            return self.skipped(ShadowSkipReason::StreamingBytesNotEligible);
        }
        if !scope.stream && matches!(input, ResponseShadowInput::SourceEvents(_)) {
            return self.skipped(ShadowSkipReason::NonStreamingSourceEvents);
        }
        let admission = match self.admit(snapshot, scope, request_key, input.input_bytes()) {
            Ok(eligibility) => eligibility,
            Err(reason) => return self.skipped(reason),
        };
        let old_result = invoke_response_converter(old, input);
        let new_result = invoke_response_converter(new, input);
        let record = build_record(
            ShadowOperation::Response,
            admission.eligibility,
            old_result,
            new_result,
        );
        drop(admission.permit);
        self.counters.record(&record);
        ShadowOutcome::Recorded(record)
    }

    fn admit(
        &self,
        snapshot: &ProtocolRolloutSnapshot,
        scope: ShadowScope,
        request_key: &str,
        input_bytes: usize,
    ) -> Result<ShadowAdmission<'_>, ShadowSkipReason> {
        let eligibility = self.check_eligibility(snapshot, scope, request_key)?;
        if input_bytes > self.config.max_input_bytes {
            return Err(ShadowSkipReason::InputTooLarge);
        }
        let max_concurrency = self
            .config
            .max_concurrency
            .ok_or(ShadowSkipReason::UnboundedConcurrency)?;
        let permit = self
            .try_enter(max_concurrency)
            .ok_or(ShadowSkipReason::ConcurrencyLimit)?;
        Ok(ShadowAdmission {
            eligibility,
            permit,
        })
    }

    fn try_enter(&self, max_concurrency: NonZeroUsize) -> Option<ShadowPermit<'_>> {
        let mut active = self.active.load(Ordering::Acquire);
        loop {
            if active >= max_concurrency.get() {
                return None;
            }
            match self.active.compare_exchange_weak(
                active,
                active + 1,
                Ordering::AcqRel,
                Ordering::Acquire,
            ) {
                Ok(_) => {
                    return Some(ShadowPermit {
                        active: &self.active,
                    });
                }
                Err(observed) => active = observed,
            }
        }
    }

    fn skipped(&self, reason: ShadowSkipReason) -> ShadowOutcome {
        self.counters.record_skip(reason);
        ShadowOutcome::Skipped(reason)
    }
}

struct ShadowAdmission<'a> {
    eligibility: ShadowEligibility,
    permit: ShadowPermit<'a>,
}

struct ShadowPermit<'a> {
    active: &'a AtomicUsize,
}

impl Drop for ShadowPermit<'_> {
    fn drop(&mut self) {
        self.active.fetch_sub(1, Ordering::AcqRel);
    }
}

fn invoke_request_converter<C>(converter: &C, request: &LocalRequest<'_>) -> ConverterObservation
where
    C: LocalConverter + ?Sized,
{
    match converter.convert_local(request) {
        Ok(summary) => ConverterObservation::Summary(summary),
        Err(error) => ConverterObservation::Failed(error),
    }
}

fn invoke_response_converter<C>(
    converter: &C,
    input: &ResponseShadowInput<'_>,
) -> ConverterObservation
where
    C: LocalResponseConverter + ?Sized,
{
    match converter.convert_response_local(input) {
        Ok(summary) => ConverterObservation::Summary(summary),
        Err(error) => ConverterObservation::Failed(error),
    }
}

fn build_record(
    operation: ShadowOperation,
    eligibility: ShadowEligibility,
    old: ConverterObservation,
    new: ConverterObservation,
) -> ShadowRecord {
    let mut differences = Vec::new();
    let mut failures = Vec::new();
    if let ConverterObservation::Failed(error) = &old {
        failures.push(ShadowConverterFailure {
            side: ShadowConverterSide::Old,
            kind: error.kind,
        });
    }
    if let ConverterObservation::Failed(error) = &new {
        failures.push(ShadowConverterFailure {
            side: ShadowConverterSide::New,
            kind: error.kind,
        });
    }
    let converter_id_differed = match (&old, &new) {
        (ConverterObservation::Summary(old), ConverterObservation::Summary(new)) => {
            old.converter_id != new.converter_id
        }
        _ => false,
    };
    match (&old, &new) {
        (ConverterObservation::Summary(old), ConverterObservation::Summary(new)) => {
            if old.plan_fingerprint != new.plan_fingerprint {
                differences.push(ShadowDifference::Plan);
            }
            if old.semantic_fingerprint != new.semantic_fingerprint {
                differences.push(ShadowDifference::Semantic);
            }
            if old.losses != new.losses {
                differences.push(ShadowDifference::LossLedger);
            }
            if old.synthetic != new.synthetic {
                differences.push(ShadowDifference::SyntheticFields);
            }
        }
        (ConverterObservation::Failed(old), ConverterObservation::Failed(new))
            if old.kind == new.kind => {}
        _ => differences.push(ShadowDifference::ConversionFailure),
    }
    ShadowRecord {
        operation,
        scope: eligibility.scope,
        rollout_generation: eligibility.rollout_generation,
        old,
        new,
        differences,
        failures,
        converter_id_differed,
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use crate::protocol_rollout::{ProtocolRolloutControl, MAX_BASIS_POINTS};
    use std::{
        io,
        sync::{
            atomic::{AtomicUsize, Ordering},
            Arc,
        },
    };

    type TestResult<T = ()> = Result<T, io::Error>;

    fn enabled_snapshot() -> ProtocolRolloutSnapshot {
        ProtocolRolloutControl::default().snapshot()
    }

    fn scope() -> ShadowScope {
        ShadowScope::new(Protocol::OpenAi, Protocol::OpenAiResponses, false)
    }

    fn stream_scope() -> ShadowScope {
        ShadowScope::new(Protocol::OpenAi, Protocol::OpenAiResponses, true)
    }

    fn native_scope() -> ShadowScope {
        ShadowScope::new(Protocol::OpenAi, Protocol::OpenAi, false)
    }

    fn shadow_config(
        max_input_bytes: usize,
        max_concurrency: Option<usize>,
        allowed_scopes: Vec<ShadowScope>,
        canary_basis_points: u16,
    ) -> TestResult<ShadowConfig> {
        ShadowConfig::new(
            true,
            max_input_bytes,
            max_concurrency.and_then(NonZeroUsize::new),
            allowed_scopes,
            canary_basis_points,
        )
        .map_err(|error| {
            io::Error::new(
                io::ErrorKind::InvalidInput,
                format!("invalid shadow test config: {error:?}"),
            )
        })
    }

    fn config(max_input_bytes: usize, max_concurrency: Option<usize>) -> TestResult<ShadowConfig> {
        shadow_config(
            max_input_bytes,
            max_concurrency,
            vec![scope()],
            MAX_BASIS_POINTS,
        )
    }

    fn recorded(outcome: ShadowOutcome, context: &'static str) -> TestResult<ShadowRecord> {
        match outcome {
            ShadowOutcome::Recorded(record) => Ok(record),
            ShadowOutcome::Skipped(reason) => Err(io::Error::other(format!(
                "{context}: shadow comparison skipped: {reason:?}"
            ))),
        }
    }

    fn summary(id: &str) -> LocalConversionSummary {
        LocalConversionSummary {
            converter_id: id.to_owned(),
            plan_fingerprint: [1; 32],
            semantic_fingerprint: [2; 32],
            losses: Vec::new(),
            synthetic: Vec::new(),
        }
    }

    #[test]
    fn aggregate_projects_conservative_rollback_and_bounded_metrics() -> TestResult {
        let aggregate = ShadowAggregateSnapshot {
            compared: 3,
            identical: 1,
            differences: 1,
            converter_failures: 1,
            skipped: 2,
            ..ShadowAggregateSnapshot::default()
        };
        let signals = aggregate.rollback_signals();
        assert!(signals.silent_loss);

        let observer = crate::conversion_observability::ConversionObserver::default();
        let labels = crate::conversion_observability::MetricLabels::new(
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            crate::conversion_observability::ConverterVersion::ProtocolStreamV1,
            1,
            false,
            crate::conversion_observability::FeatureClass::Stream,
            crate::conversion_observability::ConversionResult::Success,
        );
        aggregate.record_observability(&observer, labels);
        let samples = observer.snapshot().samples;
        assert!(samples.iter().any(|sample| {
            sample.metric == crate::conversion_observability::MetricKind::ConversionEventsTotal
                && sample.labels.result
                    == crate::conversion_observability::ConversionResult::Success
                && sample.value == 3
        }));
        assert!(samples.iter().any(|sample| {
            sample.metric == crate::conversion_observability::MetricKind::ConversionEventsTotal
                && sample.labels.result
                    == crate::conversion_observability::ConversionResult::Unsupported
                && sample.value == 2
        }));
        assert!(samples.iter().any(|sample| {
            sample.metric == crate::conversion_observability::MetricKind::ConversionFailuresTotal
                && sample.value == 1
        }));
        assert!(samples.iter().any(|sample| {
            sample.metric
                == crate::conversion_observability::MetricKind::ConversionUnknownEventsTotal
                && sample.value == 1
        }));
        Ok(())
    }

    #[test]
    fn request_calls_each_local_converter_once_and_record_has_no_body() -> TestResult {
        let old_calls = AtomicUsize::new(0);
        let new_calls = AtomicUsize::new(0);
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(2))?);
        let old = |request: &LocalRequest<'_>| {
            assert_eq!(request.as_bytes(), b"secret prompt");
            old_calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("old"))
        };
        let new = |request: &LocalRequest<'_>| {
            assert_eq!(request.as_bytes(), b"secret prompt");
            new_calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("new"))
        };
        let outcome = coordinator.shadow_request(
            &enabled_snapshot(),
            scope(),
            "stable-request",
            b"secret prompt",
            &old,
            &new,
        );
        let record = recorded(outcome, "request comparison")?;
        assert_eq!(old_calls.load(Ordering::SeqCst), 1);
        assert_eq!(new_calls.load(Ordering::SeqCst), 1);
        assert!(matches!(&record.old, ConverterObservation::Summary(_)));
        assert!(record.semantic_identical());
        assert!(record.converter_id_differed);
        let debug = format!("{record:?}");
        assert!(!debug.contains("secret prompt"));
        assert!(!debug.contains("old"));
        assert!(!debug.contains("[1"));
        assert_eq!(coordinator.aggregate().compared, 1);
        assert_eq!(coordinator.aggregate().converter_id_differences, 1);
        Ok(())
    }

    #[test]
    fn response_uses_one_already_obtained_input_without_network_ownership() -> TestResult {
        let old_calls = AtomicUsize::new(0);
        let new_calls = AtomicUsize::new(0);
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(2))?);
        let input = ResponseShadowInput::Bytes(b"response secret");
        let old = |value: &ResponseShadowInput<'_>| {
            assert_eq!(value.bytes(), Some(b"response secret".as_slice()));
            old_calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("old"))
        };
        let new = |value: &ResponseShadowInput<'_>| {
            assert_eq!(value.bytes(), Some(b"response secret".as_slice()));
            new_calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("old"))
        };
        let outcome = coordinator.shadow_response(
            &enabled_snapshot(),
            scope(),
            "stable-response",
            &input,
            &old,
            &new,
        );
        assert!(matches!(outcome, ShadowOutcome::Recorded(_)));
        assert_eq!(old_calls.load(Ordering::SeqCst), 1);
        assert_eq!(new_calls.load(Ordering::SeqCst), 1);
        Ok(())
    }

    #[test]
    fn stream_uses_source_event_view_and_rejects_bytes_or_native_raw() -> TestResult {
        let coordinator = ProtocolShadowCoordinator::new(shadow_config(
            128,
            Some(2),
            vec![stream_scope()],
            MAX_BASIS_POINTS,
        )?);
        let source_events = SourceEventSummary::new(64, 3, true, false, [7; 32]);
        let input = ResponseShadowInput::SourceEvents(&source_events);
        let converter = |value: &ResponseShadowInput<'_>| {
            assert_eq!(value.source_events(), Some(&source_events));
            Ok::<_, LocalConversionError>(summary("response"))
        };
        assert!(matches!(
            coordinator.shadow_response(
                &enabled_snapshot(),
                stream_scope(),
                "stream-events",
                &input,
                &converter,
                &converter,
            ),
            ShadowOutcome::Recorded(_)
        ));
        let bytes = ResponseShadowInput::Bytes(b"stream bytes");
        assert_eq!(
            coordinator.shadow_response(
                &enabled_snapshot(),
                stream_scope(),
                "stream-bytes",
                &bytes,
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::StreamingBytesNotEligible)
        );
        let native = ResponseShadowInput::NativeRaw { input_bytes: 64 };
        assert_eq!(
            coordinator.shadow_response(
                &enabled_snapshot(),
                stream_scope(),
                "native-raw",
                &native,
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::NativeRawNotEligible)
        );
        Ok(())
    }

    #[test]
    fn non_stream_scope_rejects_source_events_before_converter_calls() -> TestResult {
        let calls = AtomicUsize::new(0);
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(2))?);
        let source_events = SourceEventSummary::new(32, 1, true, false, [9; 32]);
        let input = ResponseShadowInput::SourceEvents(&source_events);
        let converter = |_: &ResponseShadowInput<'_>| {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("must-not-run"))
        };
        assert_eq!(
            coordinator.shadow_response(
                &enabled_snapshot(),
                scope(),
                "non-stream-events",
                &input,
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::NonStreamingSourceEvents)
        );
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        assert_eq!(
            coordinator.aggregate().skipped_non_streaming_source_events,
            1
        );
        Ok(())
    }

    #[test]
    fn oversize_and_unbounded_inputs_skip_before_converter_calls() -> TestResult {
        let calls = AtomicUsize::new(0);
        let converter = |_: &LocalRequest<'_>| {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("unused"))
        };
        let oversize = ProtocolShadowCoordinator::new(config(4, Some(1))?);
        assert_eq!(
            oversize.shadow_request(
                &enabled_snapshot(),
                scope(),
                "oversize",
                b"12345",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::InputTooLarge)
        );
        let unbounded = ProtocolShadowCoordinator::new(config(128, None)?);
        assert_eq!(
            unbounded.shadow_request(
                &enabled_snapshot(),
                scope(),
                "unbounded",
                b"123",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::UnboundedConcurrency)
        );
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        Ok(())
    }

    #[test]
    fn disabled_scope_canary_and_rollback_skip() -> TestResult {
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(1))?);
        let converter = |_: &LocalRequest<'_>| Ok::<_, LocalConversionError>(summary("unused"));
        assert_eq!(
            ProtocolShadowCoordinator::default().shadow_request(
                &enabled_snapshot(),
                scope(),
                "disabled",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::FlagDisabled)
        );
        let scope_not_allowed = ProtocolShadowCoordinator::new(shadow_config(
            128,
            Some(1),
            Vec::new(),
            MAX_BASIS_POINTS,
        )?);
        assert_eq!(
            scope_not_allowed.shadow_request(
                &enabled_snapshot(),
                scope(),
                "scope-not-allowed",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::ScopeNotAllowed)
        );
        let canary_excluded =
            ProtocolShadowCoordinator::new(shadow_config(128, Some(1), vec![scope()], 0)?);
        assert_eq!(
            canary_excluded.shadow_request(
                &enabled_snapshot(),
                scope(),
                "canary-excluded",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::CanaryExcluded)
        );
        assert_eq!(
            coordinator.shadow_request(
                &enabled_snapshot().rolled_back(),
                scope(),
                "rollback",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::RollbackActive)
        );
        Ok(())
    }

    #[test]
    fn converter_failure_is_explicit_and_typed() -> TestResult {
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(2))?);
        let old = |_: &LocalRequest<'_>| {
            Err::<LocalConversionSummary, _>(LocalConversionError {
                kind: LocalConversionErrorKind::InvalidInput,
            })
        };
        let new = |_: &LocalRequest<'_>| {
            Err::<LocalConversionSummary, _>(LocalConversionError {
                kind: LocalConversionErrorKind::Unsupported,
            })
        };
        let outcome = coordinator.shadow_request(
            &enabled_snapshot(),
            scope(),
            "failure",
            b"body",
            &old,
            &new,
        );
        let record = recorded(outcome, "typed converter failure comparison")?;
        assert!(record
            .differences
            .contains(&ShadowDifference::ConversionFailure));
        assert_eq!(record.failures.len(), 2);
        assert!(record
            .failures
            .iter()
            .any(|failure| { failure.kind == LocalConversionErrorKind::InvalidInput }));
        assert!(record
            .failures
            .iter()
            .any(|failure| { failure.kind == LocalConversionErrorKind::Unsupported }));
        Ok(())
    }

    #[test]
    fn matching_typed_converter_failures_are_semantically_identical() -> TestResult {
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(2))?);
        let old = |_: &LocalRequest<'_>| {
            Err::<LocalConversionSummary, _>(LocalConversionError {
                kind: LocalConversionErrorKind::InvalidInput,
            })
        };
        let new = |_: &LocalRequest<'_>| {
            Err::<LocalConversionSummary, _>(LocalConversionError {
                kind: LocalConversionErrorKind::InvalidInput,
            })
        };
        let outcome = coordinator.shadow_request(
            &enabled_snapshot(),
            scope(),
            "matching-failure",
            b"body",
            &old,
            &new,
        );
        let record = recorded(outcome, "matching converter failure comparison")?;
        assert!(record.semantic_identical());
        assert!(record.differences.is_empty());
        assert_eq!(record.failures.len(), 2);
        assert_eq!(coordinator.aggregate().identical, 1);
        assert_eq!(coordinator.aggregate().converter_failures, 1);
        Ok(())
    }

    #[test]
    fn same_protocol_native_scope_skips_request_before_any_converter_call() -> TestResult {
        let calls = AtomicUsize::new(0);
        let coordinator = ProtocolShadowCoordinator::new(shadow_config(
            128,
            Some(2),
            vec![native_scope()],
            MAX_BASIS_POINTS,
        )?);
        let converter = |_: &LocalRequest<'_>| {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("must-not-run"))
        };
        assert_eq!(
            coordinator.shadow_request(
                &enabled_snapshot(),
                native_scope(),
                "native-request",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::NativeRawNotEligible)
        );
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        assert_eq!(coordinator.aggregate().skipped_native_raw, 1);

        let response_input = ResponseShadowInput::Bytes(b"body");
        let response_converter = |_: &ResponseShadowInput<'_>| {
            calls.fetch_add(1, Ordering::SeqCst);
            Ok::<_, LocalConversionError>(summary("must-not-run"))
        };
        assert_eq!(
            coordinator.shadow_response(
                &enabled_snapshot(),
                native_scope(),
                "native-response",
                &response_input,
                &response_converter,
                &response_converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::NativeRawNotEligible)
        );
        assert_eq!(calls.load(Ordering::SeqCst), 0);
        assert_eq!(coordinator.aggregate().skipped_native_raw, 2);
        Ok(())
    }

    #[test]
    fn exact_scope_decision_is_stable_for_one_snapshot() -> TestResult {
        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(1))?);
        let snapshot = enabled_snapshot();
        let first = coordinator.check_eligibility(&snapshot, scope(), "same-key");
        let second = coordinator.check_eligibility(&snapshot, scope(), "same-key");
        assert_eq!(first, second);
        let eligibility = first
            .map_err(|reason| io::Error::other(format!("unexpected shadow skip: {reason:?}")))?;
        assert_eq!(eligibility.rollout_generation, 0);
        Ok(())
    }

    #[test]
    fn concurrency_limit_skips_a_second_in_flight_comparison() -> TestResult {
        use std::{sync::Barrier, thread};

        let coordinator = ProtocolShadowCoordinator::new(config(128, Some(1))?);
        let entered = Arc::new(Barrier::new(2));
        let release = Arc::new(Barrier::new(2));
        let thread_coordinator = coordinator.clone();
        let thread_entered = Arc::clone(&entered);
        let thread_release = Arc::clone(&release);
        let handle = thread::spawn(move || {
            let converter_calls = AtomicUsize::new(0);
            let converter = move |_: &LocalRequest<'_>| {
                if converter_calls.fetch_add(1, Ordering::SeqCst) == 0 {
                    thread_entered.wait();
                    thread_release.wait();
                }
                Ok::<_, LocalConversionError>(summary("in-flight"))
            };
            thread_coordinator.shadow_request(
                &enabled_snapshot(),
                scope(),
                "first",
                b"body",
                &converter,
                &converter,
            )
        });
        entered.wait();
        let converter = |_: &LocalRequest<'_>| Ok::<_, LocalConversionError>(summary("second"));
        assert_eq!(
            coordinator.shadow_request(
                &enabled_snapshot(),
                scope(),
                "second",
                b"body",
                &converter,
                &converter,
            ),
            ShadowOutcome::Skipped(ShadowSkipReason::ConcurrencyLimit)
        );
        release.wait();
        let thread_outcome = handle
            .join()
            .map_err(|_| io::Error::other("shadow comparison thread panicked"))?;
        assert!(matches!(thread_outcome, ShadowOutcome::Recorded(_)));
        Ok(())
    }
}
