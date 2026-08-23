//! Pre-wired, closed-by-default typed streaming pipeline.
//!
//! The pipeline consumes complete [`SseFrame`] values from the existing
//! incremental parser.  It deliberately does not own a router, an HTTP body,
//! or a provider decoder.  Same-protocol traffic is an opaque raw passthrough
//! and borrows the parser's bytes.  Cross-protocol traffic is admitted only
//! after the route gate and an explicitly supplied source-to-canonical-
//! to-target adaptor are both present.  The default adaptor registry is backed
//! by [`CortexFsStreamAdaptorRegistry`], so cross-protocol streams can compile
//! once rollout and ownership evidence admit the route.

use std::{collections::BTreeSet, time::Instant};

use lmm_contracts::relay::{
    CanonicalStreamEvent, ConversionPlan, Direction, LossCode, Protocol, TokenUsage,
    ValidatedRegistry,
};

use crate::{
    conversion_observability::{
        ClientAbortGuard, ConversionObserver, ConversionResult, ConverterVersion, FailureReason,
        FeatureClass, MetricLabels, QueueDepthGuard, StreamTiming,
    },
    cortexfs_protocol_bridge::CortexFsStreamAdaptorRegistry,
    migration_routes::sse::{
        DEFAULT_MAX_FRAME_BYTES, LOSS_UNKNOWN_EVENT, SseFrame, UnknownEventClass,
        UnknownEventDecision, unknown_event_decision,
    },
    protocol_rollout::{ProtocolRolloutConfig, ProtocolRolloutSnapshot, RolloutContext},
    protocol_route_gate::{RouteGateBlocker, RouteGateDecision, RouteGateDetails, decide_route},
    route_ownership::{OwnershipEvidence, RouteOwnershipScope},
};

/// Maximum number of bytes retained by the default typed-session boundary.
pub const DEFAULT_MAX_TYPED_FRAME_BYTES: usize = DEFAULT_MAX_FRAME_BYTES;

/// Maximum canonical content-block index accepted by the state machine.
pub const DEFAULT_MAX_BLOCK_INDEX: usize = 16_384;

/// Maximum typed output items emitted for one input frame by default.
pub const DEFAULT_MAX_TYPED_OUTPUT_ITEMS: usize = 32;

/// Maximum aggregate output bytes accounted for one input frame by default.
pub const DEFAULT_MAX_TYPED_OUTPUT_BYTES: usize = DEFAULT_MAX_TYPED_FRAME_BYTES;

/// Why a stream decision remains closed after route admission was evaluated.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum StreamCloseReason {
    /// The validated route/rollout/ownership gate returned a closed decision.
    GateClosed,
    /// No complete source-to-canonical-to-target adaptor was supplied.
    TypedAdaptorUnavailable,
}

/// The explicit decision made once before a stream is processed.
#[derive(Clone, Debug, PartialEq)]
pub enum StreamSessionDecision {
    /// Same-protocol bytes are forwarded without decoding or payload copying.
    RawPassthrough {
        /// Route metadata captured during admission.
        details: RouteGateDetails,
    },
    /// A supplied typed adaptor may process one complete frame at a time.
    Typed {
        /// Route metadata captured during admission.
        details: RouteGateDetails,
    },
    /// No stream state or adaptor is created for this route.
    Closed {
        /// Route metadata captured during admission.
        details: RouteGateDetails,
        /// Closed-set reasons returned by the route gate, if any.
        blockers: Vec<RouteGateBlocker>,
        /// The local pipeline reason for remaining closed.
        reason: StreamCloseReason,
    },
}

impl StreamSessionDecision {
    /// Returns the immutable route metadata for this decision.
    #[must_use]
    pub const fn details(&self) -> &RouteGateDetails {
        match self {
            Self::RawPassthrough { details }
            | Self::Typed { details }
            | Self::Closed { details, .. } => details,
        }
    }

    /// Returns gate blockers, or an empty slice for an admitted route.
    #[must_use]
    pub fn blockers(&self) -> &[RouteGateBlocker] {
        match self {
            Self::Closed { blockers, .. } => blockers,
            Self::RawPassthrough { .. } | Self::Typed { .. } => &[],
        }
    }

    /// Returns the local closed reason, if this decision is closed.
    #[must_use]
    pub const fn close_reason(&self) -> Option<StreamCloseReason> {
        match self {
            Self::Closed { reason, .. } => Some(*reason),
            Self::RawPassthrough { .. } | Self::Typed { .. } => None,
        }
    }

    /// Returns whether this is an opaque same-protocol route.
    #[must_use]
    pub const fn is_raw_passthrough(&self) -> bool {
        matches!(self, Self::RawPassthrough { .. })
    }

    /// Returns whether this is an admitted typed route.
    #[must_use]
    pub const fn is_typed(&self) -> bool {
        matches!(self, Self::Typed { .. })
    }

    /// Returns whether this route is closed.
    #[must_use]
    pub const fn is_closed(&self) -> bool {
        matches!(self, Self::Closed { .. })
    }
}

/// Input dimensions used to compile exactly one stream session.
pub struct StreamSessionSpec<'a> {
    /// Stable request key used by the deterministic rollout snapshot.
    pub request_key: &'a str,
    /// Optional low-cardinality rollout channel.
    pub channel: Option<&'a str>,
    /// Source protocol represented by incoming SSE frames.
    pub source: Protocol,
    /// Target protocol expected from the adaptor.
    pub target: Protocol,
    /// Normalized model-family label checked by the registry.
    pub model_family: &'a str,
    /// Runtime-validated route registry snapshot.
    pub registry: &'a ValidatedRegistry,
    /// One immutable rollout snapshot shared by the whole stream.
    pub rollout: &'a ProtocolRolloutSnapshot,
    /// Exact route and stream ownership evidence.
    pub ownership: &'a OwnershipEvidence,
    /// Independent bound for one complete input frame and target output.
    pub max_frame_bytes: usize,
    /// Maximum number of typed output items emitted for one input frame.
    pub max_output_items: usize,
    /// Maximum aggregate output bytes accounted for one input frame.
    pub max_output_bytes: usize,
}

impl<'a> StreamSessionSpec<'a> {
    /// Creates a specification with the conservative frame bound.
    #[must_use]
    pub const fn new(
        request_key: &'a str,
        source: Protocol,
        target: Protocol,
        model_family: &'a str,
        registry: &'a ValidatedRegistry,
        rollout: &'a ProtocolRolloutSnapshot,
        ownership: &'a OwnershipEvidence,
    ) -> Self {
        Self {
            request_key,
            channel: None,
            source,
            target,
            model_family,
            registry,
            rollout,
            ownership,
            max_frame_bytes: DEFAULT_MAX_TYPED_FRAME_BYTES,
            max_output_items: DEFAULT_MAX_TYPED_OUTPUT_ITEMS,
            max_output_bytes: DEFAULT_MAX_TYPED_OUTPUT_BYTES,
        }
    }

    /// Sets the optional rollout channel dimension.
    #[must_use]
    pub const fn with_channel(mut self, channel: &'a str) -> Self {
        self.channel = Some(channel);
        self
    }

    /// Sets the independent input/output byte bound.
    #[must_use]
    pub const fn with_max_frame_bytes(mut self, max_frame_bytes: usize) -> Self {
        self.max_frame_bytes = max_frame_bytes;
        self
    }

    /// Sets per-input typed output item and aggregate byte bounds.
    #[must_use]
    pub const fn with_output_limits(
        mut self,
        max_output_items: usize,
        max_output_bytes: usize,
    ) -> Self {
        self.max_output_items = max_output_items;
        self.max_output_bytes = max_output_bytes;
        self
    }
}

/// Failures that happen before a typed output has been emitted.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub enum StreamSetupFailure {
    /// The validated route plan could not be compiled.
    #[error("stream conversion plan is unavailable")]
    PlanUnavailable,
    /// The compiled plan violates the configured loss policy.
    #[error("stream conversion plan is rejected by the loss policy")]
    PlanRejected,
    /// The route gate did not admit this stream.
    #[error("stream route is closed")]
    RouteClosed,
    /// No complete source-to-canonical-to-target adaptor is registered.
    #[error("typed stream adaptor is unavailable")]
    TypedAdaptorUnavailable,
    /// The input frame or target frame exceeded the configured bound.
    #[error("typed stream frame exceeded {limit} bytes (observed {observed})")]
    FrameTooLarge {
        /// Maximum permitted bytes.
        limit: usize,
        /// Observed bytes.
        observed: usize,
    },
    /// A transition was out of order before output began.
    #[error("typed stream event is out of order")]
    OutOfOrder,
    /// A terminal event was repeated before output began.
    #[error("typed stream emitted a terminal event more than once")]
    DuplicateTerminal,
    /// Data arrived after terminal state before output began.
    #[error("typed stream emitted data after terminal state")]
    AfterTerminal,
    /// Client cancellation happened before a typed output was emitted.
    #[error("typed stream was cancelled")]
    Cancelled,
    /// A prior processing error poisoned this session.
    #[error("typed stream session is poisoned")]
    Poisoned,
    /// An adaptor emitted too many output items for one input frame.
    #[error("typed stream emitted {observed} output items (limit {limit})")]
    OutputItemsExceeded {
        /// Maximum permitted output items.
        limit: usize,
        /// Number of output items returned by the adaptor.
        observed: usize,
    },
    /// An adaptor emitted too many aggregate owned output bytes.
    #[error("typed stream emitted {observed} output bytes (limit {limit})")]
    OutputBytesExceeded {
        /// Maximum permitted aggregate bytes.
        limit: usize,
        /// Aggregate owned output bytes observed.
        observed: usize,
    },
    /// An adaptor returned an event that the state machine rejected.
    #[error("typed stream adaptor returned an invalid transition")]
    InvalidTransition,
    /// An unknown content or termination event could not be continued.
    #[error("unknown typed stream event cannot be continued")]
    UnknownEvent,
}

/// Failures after at least one typed output or loss record has been emitted.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub enum TypedStreamFailure {
    /// A later input or target frame exceeded the configured bound.
    #[error("typed stream frame exceeded {limit} bytes (observed {observed})")]
    FrameTooLarge {
        /// Maximum permitted bytes.
        limit: usize,
        /// Observed bytes.
        observed: usize,
    },
    /// A canonical event violated started/block ordering.
    #[error("typed stream event is out of order")]
    OutOfOrder,
    /// A terminal event was emitted more than once.
    #[error("typed stream emitted a terminal event more than once")]
    DuplicateTerminal,
    /// A frame arrived after terminal state.
    #[error("typed stream emitted data after terminal state")]
    AfterTerminal,
    /// Client cancellation happened after typed output; this is not success.
    #[error("typed stream was cancelled")]
    Cancelled,
    /// A prior processing error poisoned this session.
    #[error("typed stream session is poisoned")]
    Poisoned,
    /// An adaptor emitted too many output items for one input frame.
    #[error("typed stream emitted {observed} output items (limit {limit})")]
    OutputItemsExceeded {
        /// Maximum permitted output items.
        limit: usize,
        /// Number of output items returned by the adaptor.
        observed: usize,
    },
    /// An adaptor emitted too many aggregate owned output bytes.
    #[error("typed stream emitted {observed} output bytes (limit {limit})")]
    OutputBytesExceeded {
        /// Maximum permitted aggregate bytes.
        limit: usize,
        /// Aggregate owned output bytes observed.
        observed: usize,
    },
    /// An adaptor returned an invalid canonical transition.
    #[error("typed stream adaptor returned an invalid transition")]
    InvalidTransition,
    /// An unknown content or termination event could not be continued.
    #[error("unknown typed stream event cannot be continued")]
    UnknownEvent,
}

/// A processing error preserves whether failure happened before or after output.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
pub enum StreamProcessError {
    /// Setup failed before output began.
    #[error("stream setup failed: {0}")]
    Setup(#[from] StreamSetupFailure),
    /// The stream failed after output began.
    #[error("typed stream failed: {0}")]
    Stream(#[from] TypedStreamFailure),
}

/// A bounded, low-cardinality unknown-event loss record.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct StreamLoss {
    /// Stable loss code from the existing SSE policy.
    pub code: &'static str,
    /// Conservative event classification.
    pub class: UnknownEventClass,
}

/// Returns the existing explicit policy for an unknown event.
#[must_use]
pub fn unknown_stream_event_policy(
    same_protocol: bool,
    event_name: Option<&str>,
) -> UnknownEventDecision {
    unknown_event_decision(same_protocol, event_name)
}

/// One canonical event plus its target representation from a typed adaptor.
#[derive(Debug, PartialEq)]
pub enum StreamAdaptorItem {
    /// Canonical transition and already-framed target-protocol bytes.
    TargetFramed {
        /// One source-to-canonical transition represented by this frame.
        event: CanonicalStreamEvent,
        /// Target-protocol SSE bytes, including the dispatch delimiter.
        bytes: Vec<u8>,
    },
    /// Canonical output for a later host encoder.
    Canonical {
        /// One canonical transition represented by this frame.
        event: CanonicalStreamEvent,
    },
    /// Metadata loss which the existing policy permits the adaptor to record.
    Loss(StreamLoss),
}

/// A typed adaptor's bounded `0..N` output for one complete parsed frame.
#[derive(Debug, PartialEq)]
pub struct StreamAdaptorOutput {
    items: Vec<StreamAdaptorItem>,
}

impl StreamAdaptorOutput {
    /// Creates a batch; the session enforces its configured limits.
    #[must_use]
    pub fn new(items: Vec<StreamAdaptorItem>) -> Self {
        Self { items }
    }

    /// Creates an empty, non-outputting batch.
    #[must_use]
    pub const fn empty() -> Self {
        Self { items: Vec::new() }
    }

    /// Returns the number of adaptor items in this batch.
    #[must_use]
    pub fn len(&self) -> usize {
        self.items.len()
    }

    /// Returns whether this batch has no output items.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    /// Borrows adaptor items in their source-frame order.
    #[must_use]
    pub fn items(&self) -> &[StreamAdaptorItem] {
        &self.items
    }
}

/// One target/canonical/loss item returned to a typed host after validation.
#[derive(Debug, PartialEq)]
pub enum TypedStreamOutput {
    /// Target-protocol framed bytes.
    TargetFramed {
        /// Bounded target bytes.
        bytes: Vec<u8>,
    },
    /// Canonical event, never a source-provider DTO.
    Canonical {
        /// One validated canonical transition.
        event: CanonicalStreamEvent,
    },
    /// One bounded loss record.
    Loss(StreamLoss),
}

/// A typed host output batch with aggregate byte accounting.
#[derive(Debug, PartialEq)]
pub struct TypedStreamBatch {
    items: Vec<TypedStreamOutput>,
    aggregate_bytes: usize,
}

impl TypedStreamBatch {
    fn new(items: Vec<TypedStreamOutput>, aggregate_bytes: usize) -> Self {
        Self {
            items,
            aggregate_bytes,
        }
    }

    /// Returns the validated output items in order.
    #[must_use]
    pub fn items(&self) -> &[TypedStreamOutput] {
        &self.items
    }

    /// Returns the number of output items in this batch.
    #[must_use]
    pub fn len(&self) -> usize {
        self.items.len()
    }

    /// Returns whether this batch emitted no items.
    #[must_use]
    pub fn is_empty(&self) -> bool {
        self.items.is_empty()
    }

    /// Returns aggregate owned output bytes accounted for this batch.
    #[must_use]
    pub const fn aggregate_bytes(&self) -> usize {
        self.aggregate_bytes
    }
}

/// One result from processing one complete parsed frame.
#[derive(Debug, PartialEq)]
pub enum StreamFrameOutput<'a> {
    /// Original frame bytes borrowed without decoding or copying.
    RawPassthrough {
        /// Original bytes, including their original line endings.
        bytes: &'a [u8],
    },
    /// Validated typed output batch, possibly empty.
    Typed {
        /// Ordered target/canonical/loss items for this input frame.
        batch: TypedStreamBatch,
    },
}

/// Strict canonical stream state shared by an admitted typed adaptor.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct TypedStreamState {
    source: Protocol,
    target: Protocol,
    started: bool,
    terminal: bool,
    cancelled: bool,
    terminal_error_seen: bool,
    terminal_cancelled_seen: bool,
    open_blocks: BTreeSet<usize>,
    seen_blocks: BTreeSet<usize>,
    usage: Option<TokenUsage>,
    usage_finalized: bool,
}

impl TypedStreamState {
    fn new(source: Protocol, target: Protocol) -> Self {
        Self {
            source,
            target,
            started: false,
            terminal: false,
            cancelled: false,
            terminal_error_seen: false,
            terminal_cancelled_seen: false,
            open_blocks: BTreeSet::new(),
            seen_blocks: BTreeSet::new(),
            usage: None,
            usage_finalized: false,
        }
    }

    /// Returns the source protocol carried by this state.
    #[must_use]
    pub const fn source(&self) -> Protocol {
        self.source
    }

    /// Returns the target protocol carried by this state.
    #[must_use]
    pub const fn target(&self) -> Protocol {
        self.target
    }

    /// Returns whether a response-start transition has occurred.
    #[must_use]
    pub const fn started(&self) -> bool {
        self.started
    }

    /// Returns whether a response, error, or cancellation terminal transition
    /// has occurred.
    #[must_use]
    pub const fn terminal(&self) -> bool {
        self.terminal
    }

    /// Returns whether cancellation occurred.
    #[must_use]
    pub const fn cancelled(&self) -> bool {
        self.cancelled
    }

    /// Returns whether usage was finalized by a canonical response end.
    #[must_use]
    pub const fn usage_finalized(&self) -> bool {
        self.usage_finalized
    }

    /// Returns the retained usage summary, if one was supplied.
    #[must_use]
    pub fn usage(&self) -> Option<&TokenUsage> {
        self.usage.as_ref()
    }

    /// Returns the number of currently open canonical content blocks.
    #[must_use]
    pub fn open_block_count(&self) -> usize {
        self.open_blocks.len()
    }

    /// Applies exactly one canonical transition.
    pub fn apply(&mut self, event: &CanonicalStreamEvent) -> Result<(), TypedStreamFailure> {
        match event {
            CanonicalStreamEvent::ResponseStart { .. } => self.start(),
            CanonicalStreamEvent::ContentStart { index, .. } => self.block_start(*index),
            CanonicalStreamEvent::TextDelta { index, .. }
            | CanonicalStreamEvent::ReasoningDelta { index, .. }
            | CanonicalStreamEvent::ToolCallStart { index, .. }
            | CanonicalStreamEvent::ToolArgumentsDelta { index, .. } => self.block_delta(*index),
            CanonicalStreamEvent::ContentEnd { index } => self.block_end(*index),
            CanonicalStreamEvent::ResponseEnd { usage, .. } => {
                self.terminal_with_usage(usage.clone())
            }
            CanonicalStreamEvent::Error { .. } => self.mark_error(),
            CanonicalStreamEvent::Cancelled => self.mark_cancelled(),
        }
    }

    /// Marks client cancellation without fabricating success or usage.
    pub fn cancel(&mut self) -> Result<(), TypedStreamFailure> {
        if self.terminal || self.cancelled {
            return Err(TypedStreamFailure::Cancelled);
        }
        self.cancelled = true;
        Ok(())
    }

    fn start(&mut self) -> Result<(), TypedStreamFailure> {
        if self.started || self.terminal || self.cancelled {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        self.started = true;
        Ok(())
    }

    fn require_active(&self) -> Result<(), TypedStreamFailure> {
        if self.started && !self.terminal && !self.cancelled {
            Ok(())
        } else if self.terminal {
            Err(TypedStreamFailure::DuplicateTerminal)
        } else if self.cancelled {
            Err(TypedStreamFailure::Cancelled)
        } else {
            Err(TypedStreamFailure::OutOfOrder)
        }
    }

    fn require_content_active(&self) -> Result<(), TypedStreamFailure> {
        if self.started && !self.terminal && !self.cancelled {
            Ok(())
        } else if self.terminal {
            Err(TypedStreamFailure::AfterTerminal)
        } else if self.cancelled {
            Err(TypedStreamFailure::Cancelled)
        } else {
            Err(TypedStreamFailure::OutOfOrder)
        }
    }

    fn block_start(&mut self, index: usize) -> Result<(), TypedStreamFailure> {
        self.require_content_active()?;
        if index > DEFAULT_MAX_BLOCK_INDEX
            || self.open_blocks.contains(&index)
            || self.seen_blocks.contains(&index)
            || self.seen_blocks.len() >= DEFAULT_MAX_BLOCK_INDEX
        {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        self.seen_blocks.insert(index);
        self.open_blocks.insert(index);
        Ok(())
    }

    fn block_delta(&self, index: usize) -> Result<(), TypedStreamFailure> {
        self.require_content_active()?;
        if self.open_blocks.contains(&index) {
            Ok(())
        } else {
            Err(TypedStreamFailure::OutOfOrder)
        }
    }

    fn block_end(&mut self, index: usize) -> Result<(), TypedStreamFailure> {
        self.require_content_active()?;
        if self.open_blocks.remove(&index) {
            Ok(())
        } else {
            Err(TypedStreamFailure::OutOfOrder)
        }
    }

    fn terminal_with_usage(&mut self, usage: Option<TokenUsage>) -> Result<(), TypedStreamFailure> {
        self.require_active()?;
        if !self.open_blocks.is_empty() {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        if let Some(usage) = usage {
            self.usage = Some(usage);
        }
        self.terminal = true;
        self.usage_finalized = true;
        Ok(())
    }

    fn mark_error(&mut self) -> Result<(), TypedStreamFailure> {
        if !self.started {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        if !self.open_blocks.is_empty() {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        if self.cancelled && !self.terminal {
            return Err(TypedStreamFailure::Cancelled);
        }
        if self.terminal_error_seen {
            return Err(TypedStreamFailure::DuplicateTerminal);
        }

        // A canonical Error is a terminal outcome when it is independent of
        // ResponseEnd.  When it follows ResponseEnd, it is the one checked
        // error postlude permitted by the contracts state machine.
        self.terminal_error_seen = true;
        self.terminal = true;
        Ok(())
    }

    fn mark_cancelled(&mut self) -> Result<(), TypedStreamFailure> {
        if !self.started {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        if !self.open_blocks.is_empty() {
            return Err(TypedStreamFailure::OutOfOrder);
        }
        if self.cancelled && !self.terminal {
            return Err(TypedStreamFailure::Cancelled);
        }
        if self.terminal_cancelled_seen {
            return Err(TypedStreamFailure::DuplicateTerminal);
        }

        // Cancellation can be the primary terminal event or the bounded
        // postlude emitted after a provider response terminal.
        self.terminal_cancelled_seen = true;
        self.cancelled = true;
        self.terminal = true;
        Ok(())
    }
}

/// Runtime source-to-canonical-to-target stream session.
pub trait StreamAdaptorSession {
    /// Decodes one already complete frame and emits one bounded `0..N` batch.
    /// The session enforces item and owned-output byte limits; the adaptor must
    /// apply the existing unknown-event policy and must not rescan the stream
    /// as raw lines or retain an unbounded event queue.
    fn process_frame(
        &mut self,
        frame: &SseFrame,
    ) -> Result<StreamAdaptorOutput, TypedStreamFailure>;

    /// Propagates client cancellation without fabricating a terminal success.
    fn cancel(&mut self) -> Result<(), TypedStreamFailure>;
}

/// Explicit adaptor factory for one source/target pair.
pub trait StreamAdaptor {
    /// Source protocol accepted by this adaptor.
    fn source(&self) -> Protocol;

    /// Target protocol emitted by this adaptor.
    fn target(&self) -> Protocol;

    /// Compiles one session-specific decoder/state/encoder from the one plan.
    fn compile(
        &self,
        plan: &ConversionPlan,
    ) -> Result<Box<dyn StreamAdaptorSession>, StreamSetupFailure>;
}

/// Runtime registry for explicit typed adaptors.
pub trait StreamAdaptorRegistry {
    /// Finds a complete adaptor for the exact source/target pair.
    fn for_route(&self, source: Protocol, target: Protocol) -> Option<&dyn StreamAdaptor>;
}

/// Empty default registry.  No cross-protocol stream is executable through it.
#[derive(Clone, Copy, Debug, Default)]
pub struct EmptyStreamAdaptorRegistry;

impl StreamAdaptorRegistry for EmptyStreamAdaptorRegistry {
    fn for_route(&self, _source: Protocol, _target: Protocol) -> Option<&dyn StreamAdaptor> {
        None
    }
}

/// Private route-admission seam used by the production gate and module tests.
trait StreamRouteAdmission {
    /// Evaluates the immutable context against the validated route evidence.
    fn decide(
        &self,
        config: &ProtocolRolloutConfig,
        context: &RolloutContext<'_>,
        registry: &ValidatedRegistry,
        ownership: &OwnershipEvidence,
    ) -> RouteGateDecision;
}

/// Production route admission backed by the shared protocol route gate.
#[derive(Clone, Copy, Debug, Default)]
struct ValidatedStreamRouteAdmission;

impl StreamRouteAdmission for ValidatedStreamRouteAdmission {
    fn decide(
        &self,
        config: &ProtocolRolloutConfig,
        context: &RolloutContext<'_>,
        registry: &ValidatedRegistry,
        ownership: &OwnershipEvidence,
    ) -> RouteGateDecision {
        decide_route(config, context, registry, Direction::Stream, ownership)
    }
}

/// Session-owned stream telemetry guards.
///
/// The queue and client-abort guards live for exactly as long as the admitted
/// session unless the host explicitly completes or cancels it. This keeps a
/// dropped or poisoned session from leaving a stale queue depth, while a
/// normal completion cannot be mistaken for a client abort. Labels are built
/// from the immutable route decision and contain no request or model text.
struct StreamSessionTelemetry {
    observer: ConversionObserver,
    labels: MetricLabels,
    queue_guard: QueueDepthGuard,
    abort_guard: ClientAbortGuard,
    timing: StreamTiming,
    ttft_recorded: bool,
    failure_recorded: bool,
}

impl StreamSessionTelemetry {
    fn new(observer: &ConversionObserver, decision: &StreamSessionDecision) -> Option<Self> {
        if decision.is_closed() {
            return None;
        }
        let scope = decision.details().scope;
        let converter_version = if scope.source == scope.target {
            ConverterVersion::NativeRawV1
        } else {
            ConverterVersion::ProtocolStreamV1
        };
        let labels = MetricLabels::for_route(
            scope.source,
            scope.target,
            converter_version,
            1,
            true,
            FeatureClass::Stream,
            ConversionResult::Success,
        );
        Some(Self {
            observer: observer.clone(),
            labels,
            queue_guard: observer.enter_queue(labels),
            abort_guard: ClientAbortGuard::new(observer.clone(), labels),
            timing: StreamTiming::default(),
            ttft_recorded: false,
            failure_recorded: false,
        })
    }

    fn mark_upstream_event(&mut self) {
        self.timing.mark_upstream_event();
    }

    fn record_downstream_write(&mut self) {
        self.timing.mark_downstream_write();
        if !self.ttft_recorded
            && let Some(duration) = self.timing.gateway_ttft_tax() {
                self.observer.record_gateway_ttft(self.labels, duration);
                self.ttft_recorded = true;
            }
    }

    fn record_raw_frame(&self, frame_bytes: usize, duration: std::time::Duration) {
        self.observer
            .record_conversion_duration(self.labels, duration);
        self.observer.record_events(self.labels, 1);
        self.observer.record_input_bytes(self.labels, frame_bytes);
        self.observer.record_output_bytes(self.labels, frame_bytes);
    }

    fn record_typed_frame(
        &self,
        input_bytes: usize,
        output_bytes: usize,
        output_items: usize,
        loss_count: usize,
        unknown_event_count: usize,
        duration: std::time::Duration,
    ) {
        self.observer
            .record_conversion_duration(self.labels, duration);
        let event_count = if output_items > u64::MAX as usize {
            u64::MAX
        } else {
            output_items as u64
        };
        self.observer.record_events(self.labels, event_count);
        self.observer.record_input_bytes(self.labels, input_bytes);
        self.observer.record_output_bytes(self.labels, output_bytes);
        // Adaptors expose only closed loss categories; the stream metric keeps
        // the existing low-cardinality unknown-event code and never records
        // an event name or payload.
        for _ in 0..loss_count {
            self.observer
                .record_loss(self.labels, LossCode::LossUnknownEvent);
        }
        for _ in 0..unknown_event_count {
            self.observer.record_unknown_event(self.labels);
        }
    }

    fn record_failure(&mut self, failure: &TypedStreamFailure) {
        if self.failure_recorded {
            return;
        }
        let reason = if matches!(failure, TypedStreamFailure::Cancelled) {
            FailureReason::Cancelled
        } else {
            FailureReason::Stream
        };
        self.observer
            .record_failure_with_reason(self.labels, reason);
        self.failure_recorded = true;
        if matches!(failure, TypedStreamFailure::Cancelled) {
            self.abort();
        } else {
            self.complete();
        }
    }

    fn complete(&mut self) {
        self.queue_guard.complete();
        self.abort_guard.complete();
    }

    fn abort(&mut self) {
        self.queue_guard.complete();
        self.abort_guard.abort();
    }
}

/// The one compiled stream session.
pub struct StreamSession {
    decision: StreamSessionDecision,
    plan: Option<ConversionPlan>,
    state: Option<TypedStreamState>,
    adaptor: Option<Box<dyn StreamAdaptorSession>>,
    max_frame_bytes: usize,
    max_output_items: usize,
    max_output_bytes: usize,
    output_started: bool,
    cancelled: bool,
    poisoned: bool,
    telemetry: Option<StreamSessionTelemetry>,
}

impl StreamSession {
    /// Returns the immutable route decision.
    #[must_use]
    pub const fn decision(&self) -> &StreamSessionDecision {
        &self.decision
    }

    /// Returns the one conversion plan, or `None` for raw/closed sessions.
    #[must_use]
    pub const fn plan(&self) -> Option<&ConversionPlan> {
        self.plan.as_ref()
    }

    /// Returns typed state only for an admitted typed session.
    #[must_use]
    pub const fn typed_state(&self) -> Option<&TypedStreamState> {
        self.state.as_ref()
    }

    /// Returns whether any raw frame, typed output, or loss was emitted.
    #[must_use]
    pub const fn output_started(&self) -> bool {
        self.output_started
    }

    /// Returns whether a prior adaptor, limit, or transition error poisoned it.
    #[must_use]
    pub const fn is_poisoned(&self) -> bool {
        self.poisoned
    }

    /// Attaches bounded route-specific stream telemetry to this admitted
    /// session. Calling this more than once keeps the first lifecycle guards;
    /// closed sessions remain uninstrumented. The observer is cloned only as
    /// a handle to the shared bounded recorder.
    #[must_use]
    pub fn with_observer(mut self, observer: &ConversionObserver) -> Self {
        if self.telemetry.is_none() {
            self.telemetry = StreamSessionTelemetry::new(observer, &self.decision);
        }
        self
    }

    /// Marks the first downstream write for gateway-only TTFT measurement.
    /// The host should call this after bytes are accepted by its downstream
    /// writer; calling it repeatedly records only the first ordered interval.
    pub fn mark_downstream_write(&mut self) {
        if let Some(telemetry) = self.telemetry.as_mut() {
            telemetry.record_downstream_write();
        }
    }

    /// Marks a normal host-observed stream completion.
    ///
    /// Raw same-protocol sessions intentionally do not inspect terminal event
    /// names, so the host must call this after the downstream stream completes.
    /// Omitting it leaves the abort guard active and therefore fails closed as
    /// a client-aborted/dropped session when the session is dropped.
    pub fn complete(&mut self) {
        if let Some(telemetry) = self.telemetry.as_mut() {
            telemetry.complete();
        }
    }

    /// Cancels the session without creating a successful terminal event.
    /// The host remains responsible for aborting the upstream I/O operation.
    pub fn cancel(&mut self) -> Result<(), StreamProcessError> {
        if self.decision.is_closed() {
            return Err(StreamProcessError::Setup(StreamSetupFailure::RouteClosed));
        }
        if self.poisoned {
            return Err(self.stage_failure(TypedStreamFailure::Poisoned));
        }
        if self.cancelled {
            return Err(self.stage_failure(TypedStreamFailure::Cancelled));
        }

        let adaptor_result = match self.adaptor.as_mut() {
            Some(adaptor) => adaptor.cancel(),
            None => Ok(()),
        };
        if let Err(failure) = adaptor_result {
            return Err(self.poison(failure));
        }
        let state_result = match self.state.as_mut() {
            Some(state) => state.cancel(),
            None => Ok(()),
        };
        if let Err(failure) = state_result {
            return Err(self.poison(failure));
        }
        self.cancelled = true;
        if let Some(telemetry) = self.telemetry.as_mut() {
            telemetry.record_failure(&TypedStreamFailure::Cancelled);
        }
        Ok(())
    }

    /// Processes one complete parsed frame.
    ///
    /// Raw same-protocol sessions intentionally do not inspect event names,
    /// `[DONE]`, metadata, or terminal repetition.  The parser already owns
    /// the frame bound; every raw call simply borrows and forwards its bytes.
    pub fn process_frame<'a>(
        &mut self,
        frame: &'a SseFrame,
    ) -> Result<StreamFrameOutput<'a>, StreamProcessError> {
        if self.decision.is_closed() {
            return Err(StreamProcessError::Setup(StreamSetupFailure::RouteClosed));
        }
        if self.poisoned {
            return Err(self.stage_failure(TypedStreamFailure::Poisoned));
        }
        if self.cancelled {
            return Err(self.stage_failure(TypedStreamFailure::Cancelled));
        }
        let frame_started = Instant::now();
        if let Some(telemetry) = self.telemetry.as_mut() {
            telemetry.mark_upstream_event();
        }
        if self.decision.is_raw_passthrough() {
            self.output_started = true;
            if let Some(telemetry) = self.telemetry.as_ref() {
                telemetry.record_raw_frame(frame.raw.len(), frame_started.elapsed());
            }
            return Ok(StreamFrameOutput::RawPassthrough { bytes: &frame.raw });
        }
        if frame.raw.len() > self.max_frame_bytes {
            return Err(self.poison(TypedStreamFailure::FrameTooLarge {
                limit: self.max_frame_bytes,
                observed: frame.raw.len(),
            }));
        }
        let adaptor_result = if let Some(adaptor) = self.adaptor.as_mut() {
            adaptor.process_frame(frame)
        } else {
            self.poisoned = true;
            return Err(self.stage_failure(TypedStreamFailure::InvalidTransition));
        };
        let output = match adaptor_result {
            Ok(output) => output,
            Err(failure) => return Err(self.poison(failure)),
        };
        let terminal_failure = match self.state.as_ref() {
            Some(state) if state.terminal() => validate_terminal_postlude(state, &output).err(),
            _ => None,
        };
        if let Some(failure) = terminal_failure {
            return Err(self.poison(failure));
        }
        self.finish_adaptor_output(output, frame.raw.len(), frame_started)
    }

    fn finish_adaptor_output(
        &mut self,
        output: StreamAdaptorOutput,
        input_bytes: usize,
        frame_started: Instant,
    ) -> Result<StreamFrameOutput<'static>, StreamProcessError> {
        if output.len() > self.max_output_items {
            return Err(self.poison(TypedStreamFailure::OutputItemsExceeded {
                limit: self.max_output_items,
                observed: output.len(),
            }));
        }

        let mut aggregate_bytes = 0_usize;
        for item in output.items() {
            let item_bytes = match item {
                StreamAdaptorItem::TargetFramed { event, bytes } => canonical_event_bytes(event)
                    .and_then(|event_bytes| bytes.len().checked_add(event_bytes)),
                StreamAdaptorItem::Canonical { event } => canonical_event_bytes(event),
                StreamAdaptorItem::Loss(_) => Some(0),
            };
            let Some(item_bytes) = item_bytes else {
                return Err(self.poison(TypedStreamFailure::OutputBytesExceeded {
                    limit: self.max_output_bytes,
                    observed: usize::MAX,
                }));
            };
            aggregate_bytes = match aggregate_bytes.checked_add(item_bytes) {
                Some(value) if value <= self.max_output_bytes => value,
                Some(value) => {
                    return Err(self.poison(TypedStreamFailure::OutputBytesExceeded {
                        limit: self.max_output_bytes,
                        observed: value,
                    }));
                }
                None => {
                    return Err(self.poison(TypedStreamFailure::OutputBytesExceeded {
                        limit: self.max_output_bytes,
                        observed: usize::MAX,
                    }));
                }
            };
        }

        let StreamAdaptorOutput { items } = output;
        let output_item_count = items.len();
        let loss_count = items
            .iter()
            .filter(|item| matches!(item, StreamAdaptorItem::Loss(_)))
            .count();
        let unknown_event_count = items
            .iter()
            .filter(|item| {
                matches!(
                    item,
                    StreamAdaptorItem::Loss(StreamLoss {
                        code: LOSS_UNKNOWN_EVENT,
                        ..
                    })
                )
            })
            .count();
        for item in &items {
            if let StreamAdaptorItem::TargetFramed { event, .. }
            | StreamAdaptorItem::Canonical { event } = item
                && let Err(failure) = self.apply_canonical_event(event) {
                    return Err(self.poison(failure));
                }
        }

        let output_items = items
            .into_iter()
            .map(|item| match item {
                StreamAdaptorItem::TargetFramed { bytes, .. } => {
                    TypedStreamOutput::TargetFramed { bytes }
                }
                StreamAdaptorItem::Canonical { event } => TypedStreamOutput::Canonical { event },
                StreamAdaptorItem::Loss(loss) => TypedStreamOutput::Loss(loss),
            })
            .collect::<Vec<_>>();
        let batch = TypedStreamBatch::new(output_items, aggregate_bytes);
        if !batch.is_empty() {
            self.output_started = true;
        }
        if let Some(telemetry) = self.telemetry.as_ref() {
            telemetry.record_typed_frame(
                input_bytes,
                aggregate_bytes,
                output_item_count,
                loss_count,
                unknown_event_count,
                frame_started.elapsed(),
            );
        }
        Ok(StreamFrameOutput::Typed { batch })
    }

    fn apply_canonical_event(
        &mut self,
        event: &CanonicalStreamEvent,
    ) -> Result<(), TypedStreamFailure> {
        match self.state.as_mut() {
            Some(state) => state.apply(event),
            None => Err(TypedStreamFailure::InvalidTransition),
        }
    }

    fn stage_failure(&self, failure: TypedStreamFailure) -> StreamProcessError {
        if self.output_started {
            StreamProcessError::Stream(failure)
        } else {
            StreamProcessError::Setup(map_setup_failure(failure))
        }
    }

    fn poison(&mut self, failure: TypedStreamFailure) -> StreamProcessError {
        self.poisoned = true;
        if let Some(telemetry) = self.telemetry.as_mut() {
            telemetry.record_failure(&failure);
        }
        self.stage_failure(failure)
    }
}

fn validate_terminal_postlude(
    state: &TypedStreamState,
    output: &StreamAdaptorOutput,
) -> Result<(), TypedStreamFailure> {
    let [item] = output.items() else {
        return Err(TypedStreamFailure::AfterTerminal);
    };
    let event = match item {
        StreamAdaptorItem::TargetFramed { event, .. } | StreamAdaptorItem::Canonical { event } => {
            event
        }
        StreamAdaptorItem::Loss(_) => return Err(TypedStreamFailure::AfterTerminal),
    };
    match event {
        CanonicalStreamEvent::Error { .. } if !state.terminal_error_seen => Ok(()),
        CanonicalStreamEvent::Cancelled if !state.terminal_cancelled_seen => Ok(()),
        CanonicalStreamEvent::Error { .. } | CanonicalStreamEvent::Cancelled => {
            Err(TypedStreamFailure::DuplicateTerminal)
        }
        CanonicalStreamEvent::ResponseEnd { .. } => Err(TypedStreamFailure::DuplicateTerminal),
        CanonicalStreamEvent::ResponseStart { .. }
        | CanonicalStreamEvent::ContentStart { .. }
        | CanonicalStreamEvent::TextDelta { .. }
        | CanonicalStreamEvent::ReasoningDelta { .. }
        | CanonicalStreamEvent::ToolCallStart { .. }
        | CanonicalStreamEvent::ToolArgumentsDelta { .. }
        | CanonicalStreamEvent::ContentEnd { .. } => Err(TypedStreamFailure::AfterTerminal),
    }
}

fn closed_session(
    details: RouteGateDetails,
    blockers: Vec<RouteGateBlocker>,
    reason: StreamCloseReason,
    max_frame_bytes: usize,
    max_output_items: usize,
    max_output_bytes: usize,
) -> StreamSession {
    StreamSession {
        decision: StreamSessionDecision::Closed {
            details,
            blockers,
            reason,
        },
        plan: None,
        state: None,
        adaptor: None,
        max_frame_bytes,
        max_output_items,
        max_output_bytes,
        output_started: false,
        cancelled: false,
        poisoned: false,
        telemetry: None,
    }
}

/// Compiles a session with the cortexfs-backed adaptor registry and route gate.
pub fn compile_stream_session(
    spec: StreamSessionSpec<'_>,
) -> Result<StreamSession, StreamSetupFailure> {
    let compiler = ValidatedRegistryPlanCompiler;
    let adaptors = CortexFsStreamAdaptorRegistry;
    let admission = ValidatedStreamRouteAdmission;
    compile_stream_session_with_runtime(spec, &compiler, &adaptors, &admission)
}

/// Compiles a session with an injected typed-adaptor registry.
pub fn compile_stream_session_with_adaptors(
    spec: StreamSessionSpec<'_>,
    adaptors: &impl StreamAdaptorRegistry,
) -> Result<StreamSession, StreamSetupFailure> {
    let compiler = ValidatedRegistryPlanCompiler;
    let admission = ValidatedStreamRouteAdmission;
    compile_stream_session_with_runtime(spec, &compiler, adaptors, &admission)
}

/// Compiles one session from one rollout snapshot, gate, plan, and adaptor.
///
/// The order is intentional: a closed route returns before plan compilation;
/// a cross route without an explicit adaptor returns closed before plan
/// compilation; only an admitted route with a matching adaptor compiles one
/// plan and one adaptor session.
fn compile_stream_session_with_runtime(
    spec: StreamSessionSpec<'_>,
    compiler: &impl StreamPlanCompiler,
    adaptors: &impl StreamAdaptorRegistry,
    admission: &impl StreamRouteAdmission,
) -> Result<StreamSession, StreamSetupFailure> {
    let mut context = RolloutContext::new(
        spec.request_key,
        spec.source,
        spec.target,
        spec.model_family,
        true,
    );
    if let Some(channel) = spec.channel {
        context = context.with_channel(channel);
    }
    let gate = admission.decide(
        spec.rollout.config(),
        &context,
        spec.registry,
        spec.ownership,
    );
    let expected_scope = RouteOwnershipScope {
        source: spec.source,
        target: spec.target,
        stream: true,
    };

    match gate {
        RouteGateDecision::NativeRaw { details } => {
            if spec.source != spec.target || details.scope != expected_scope {
                return Ok(closed_session(
                    details,
                    vec![RouteGateBlocker::OwnershipScopeMismatch],
                    StreamCloseReason::GateClosed,
                    spec.max_frame_bytes,
                    spec.max_output_items,
                    spec.max_output_bytes,
                ));
            }
            Ok(StreamSession {
                decision: StreamSessionDecision::RawPassthrough { details },
                plan: None,
                state: None,
                adaptor: None,
                max_frame_bytes: spec.max_frame_bytes,
                max_output_items: spec.max_output_items,
                max_output_bytes: spec.max_output_bytes,
                output_started: false,
                cancelled: false,
                poisoned: false,
                telemetry: None,
            })
        }
        RouteGateDecision::Closed { details, blockers } => Ok(closed_session(
            details,
            blockers,
            StreamCloseReason::GateClosed,
            spec.max_frame_bytes,
            spec.max_output_items,
            spec.max_output_bytes,
        )),
        RouteGateDecision::CrossProtocol { details } => {
            if spec.source == spec.target || details.scope != expected_scope {
                return Ok(closed_session(
                    details,
                    vec![RouteGateBlocker::OwnershipScopeMismatch],
                    StreamCloseReason::GateClosed,
                    spec.max_frame_bytes,
                    spec.max_output_items,
                    spec.max_output_bytes,
                ));
            }

            let Some(adaptor) = adaptors.for_route(spec.source, spec.target) else {
                return Ok(closed_session(
                    details,
                    Vec::new(),
                    StreamCloseReason::TypedAdaptorUnavailable,
                    spec.max_frame_bytes,
                    spec.max_output_items,
                    spec.max_output_bytes,
                ));
            };
            if adaptor.source() != spec.source || adaptor.target() != spec.target {
                return Ok(closed_session(
                    details,
                    Vec::new(),
                    StreamCloseReason::TypedAdaptorUnavailable,
                    spec.max_frame_bytes,
                    spec.max_output_items,
                    spec.max_output_bytes,
                ));
            }

            let plan =
                compiler.compile(spec.source, spec.target, spec.model_family, spec.registry)?;
            if plan.enforce(spec.rollout.config().loss_policy()).is_err() {
                return Err(StreamSetupFailure::PlanRejected);
            }
            let adaptor_session = adaptor.compile(&plan)?;
            Ok(StreamSession {
                decision: StreamSessionDecision::Typed { details },
                plan: Some(plan),
                state: Some(TypedStreamState::new(spec.source, spec.target)),
                adaptor: Some(adaptor_session),
                max_frame_bytes: spec.max_frame_bytes,
                max_output_items: spec.max_output_items,
                max_output_bytes: spec.max_output_bytes,
                output_started: false,
                cancelled: false,
                poisoned: false,
                telemetry: None,
            })
        }
    }
}

/// Plan compiler backed by the validated contracts registry.
#[derive(Clone, Copy, Debug, Default)]
struct ValidatedRegistryPlanCompiler;

/// Plan compiler seam used by the runtime path and deterministic tests.
trait StreamPlanCompiler {
    /// Compiles exactly one plan for the supplied validated route.
    fn compile(
        &self,
        source: Protocol,
        target: Protocol,
        model_family: &str,
        registry: &ValidatedRegistry,
    ) -> Result<ConversionPlan, StreamSetupFailure>;
}

impl StreamPlanCompiler for ValidatedRegistryPlanCompiler {
    fn compile(
        &self,
        source: Protocol,
        target: Protocol,
        model_family: &str,
        registry: &ValidatedRegistry,
    ) -> Result<ConversionPlan, StreamSetupFailure> {
        ConversionPlan::compile_with_validated_registry(source, target, model_family, registry)
            .map_err(|_| StreamSetupFailure::PlanUnavailable)
    }
}

/// Accounts for owned canonical string payloads without serializing them.
///
/// The session retains canonical events in typed output, while target-framed
/// items may transiently retain both their canonical event and encoded bytes.
/// Counting these selected string fields keeps the aggregate bound meaningful
/// for both output representations and reports arithmetic overflow as a limit
/// violation instead of wrapping.
fn canonical_event_bytes(event: &CanonicalStreamEvent) -> Option<usize> {
    fn add(total: &mut usize, bytes: usize) -> bool {
        let Some(next) = total.checked_add(bytes) else {
            return false;
        };
        *total = next;
        true
    }

    let mut total = 0_usize;
    let accounted = match event {
        CanonicalStreamEvent::ResponseStart { id, model } => {
            add(&mut total, id.len()) && add(&mut total, model.len())
        }
        CanonicalStreamEvent::TextDelta { delta, .. }
        | CanonicalStreamEvent::ReasoningDelta { delta, .. }
        | CanonicalStreamEvent::ToolArgumentsDelta { delta, .. } => add(&mut total, delta.len()),
        CanonicalStreamEvent::ToolCallStart { id, name, .. } => {
            add(&mut total, id.len()) && add(&mut total, name.len())
        }
        CanonicalStreamEvent::ResponseEnd { model, .. } => match model {
            Some(model) => add(&mut total, model.len()),
            None => true,
        },
        CanonicalStreamEvent::Error { code, message } => {
            let code_ok = match code {
                Some(code) => add(&mut total, code.len()),
                None => true,
            };
            code_ok && add(&mut total, message.len())
        }
        CanonicalStreamEvent::ContentStart { .. }
        | CanonicalStreamEvent::ContentEnd { .. }
        | CanonicalStreamEvent::Cancelled => true,
    };

    accounted.then_some(total)
}

fn map_setup_failure(failure: TypedStreamFailure) -> StreamSetupFailure {
    match failure {
        TypedStreamFailure::FrameTooLarge { limit, observed } => {
            StreamSetupFailure::FrameTooLarge { limit, observed }
        }
        TypedStreamFailure::OutOfOrder => StreamSetupFailure::OutOfOrder,
        TypedStreamFailure::DuplicateTerminal => StreamSetupFailure::DuplicateTerminal,
        TypedStreamFailure::AfterTerminal => StreamSetupFailure::AfterTerminal,
        TypedStreamFailure::Cancelled => StreamSetupFailure::Cancelled,
        TypedStreamFailure::Poisoned => StreamSetupFailure::Poisoned,
        TypedStreamFailure::OutputItemsExceeded { limit, observed } => {
            StreamSetupFailure::OutputItemsExceeded { limit, observed }
        }
        TypedStreamFailure::OutputBytesExceeded { limit, observed } => {
            StreamSetupFailure::OutputBytesExceeded { limit, observed }
        }
        TypedStreamFailure::InvalidTransition => StreamSetupFailure::InvalidTransition,
        TypedStreamFailure::UnknownEvent => StreamSetupFailure::UnknownEvent,
    }
}

#[cfg(test)]
mod tests {
    use std::sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    };

    use super::*;
    use crate::{
        conversion_observability::{ConversionObserver, MetricKind},
        migration_routes::sse::{LOSS_UNKNOWN_EVENT, UnknownEventAction, parse_sse_frames},
        protocol_rollout::{ProtocolRolloutControl, RolloutFlag},
        protocol_runtime_registry::validated_current_registry,
    };

    fn registry() -> ValidatedRegistry {
        validated_current_registry().expect("current registry validates")
    }

    fn spec<'a>(
        registry: &'a ValidatedRegistry,
        rollout: &'a ProtocolRolloutSnapshot,
        source: Protocol,
        target: Protocol,
        ownership: &'a OwnershipEvidence,
    ) -> StreamSessionSpec<'a> {
        StreamSessionSpec::new(
            "pipeline-test",
            source,
            target,
            "test-model",
            registry,
            rollout,
            ownership,
        )
    }

    fn ownership(source: Protocol, target: Protocol) -> OwnershipEvidence {
        OwnershipEvidence::closed(RouteOwnershipScope {
            source,
            target,
            stream: true,
        })
    }

    fn frame(input: &[u8]) -> SseFrame {
        parse_sse_frames(input, DEFAULT_MAX_FRAME_BYTES)
            .expect("valid frame")
            .into_iter()
            .next()
            .expect("one frame")
    }

    #[test]
    fn native_is_zero_conversion_and_opaque_even_for_done_and_repeated_frames() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::Claude);
        let compiler = CountingCompiler::default();
        let adaptors = EmptyStreamAdaptorRegistry;
        let admission = ValidatedStreamRouteAdmission;
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::Claude,
                &evidence,
            )
            .with_output_limits(0, 0),
            &compiler,
            &adaptors,
            &admission,
        )
        .expect("native route admits raw passthrough");
        assert!(session.decision().is_raw_passthrough());
        assert!(session.plan().is_none());
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);

        let done = frame(b"data: [DONE]\n\n");
        let future = frame(b"event: future\ndata: opaque\n\n");
        for input in [&done, &done, &future] {
            let output = session
                .process_frame(input)
                .expect("raw bytes remain opaque");
            let StreamFrameOutput::RawPassthrough { bytes } = output else {
                panic!("native route returned a typed output");
            };
            assert!(std::ptr::eq(bytes.as_ptr(), input.raw.as_ptr()));
            assert_eq!(bytes, input.raw.as_slice());
        }
    }

    #[test]
    fn current_validated_registry_keeps_every_cross_stream_closed_before_plan_compile() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let compiler = CountingCompiler::default();
        let adaptors = EmptyStreamAdaptorRegistry;
        let admission = ValidatedStreamRouteAdmission;
        for source in [
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            Protocol::Claude,
            Protocol::Gemini,
        ] {
            for target in [
                Protocol::OpenAi,
                Protocol::OpenAiResponses,
                Protocol::Claude,
                Protocol::Gemini,
            ] {
                if source == target {
                    continue;
                }
                let evidence = ownership(source, target);
                let session = compile_stream_session_with_runtime(
                    spec(&registry, &rollout, source, target, &evidence),
                    &compiler,
                    &adaptors,
                    &admission,
                )
                .expect("closed route returns a diagnostic session");
                assert!(session.decision().is_closed());
                assert!(session.typed_state().is_none());
                assert!(session.plan().is_none());
            }
        }
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn fake_open_without_adaptor_is_closed_with_typed_adaptor_reason() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let adaptors = EmptyStreamAdaptorRegistry;
        let admission = AlwaysOpenAdmission;
        let session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &compiler,
            &adaptors,
            &admission,
        )
        .expect("missing adaptor is a closed diagnostic");
        assert!(session.decision().is_closed());
        assert_eq!(
            session.decision().close_reason(),
            Some(StreamCloseReason::TypedAdaptorUnavailable)
        );
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn native_shape_mismatch_is_closed_before_plan_compile() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &compiler,
            &EmptyStreamAdaptorRegistry,
            &ShapeAdmission {
                kind: AdmissionShape::Native,
                wrong_scope: false,
            },
        )
        .expect("invalid native shape is a closed diagnostic");
        assert!(session.decision().is_closed());
        assert_eq!(
            session.decision().close_reason(),
            Some(StreamCloseReason::GateClosed)
        );
        assert!(
            session
                .decision()
                .blockers()
                .contains(&RouteGateBlocker::OwnershipScopeMismatch)
        );
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn cross_shape_mismatch_is_closed_before_plan_compile() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::Claude);
        let compiler = CountingCompiler::default();
        let session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::Claude,
                &evidence,
            ),
            &compiler,
            &EmptyStreamAdaptorRegistry,
            &ShapeAdmission {
                kind: AdmissionShape::Cross,
                wrong_scope: false,
            },
        )
        .expect("invalid cross shape is a closed diagnostic");
        assert!(session.decision().is_closed());
        assert!(
            session
                .decision()
                .blockers()
                .contains(&RouteGateBlocker::OwnershipScopeMismatch)
        );
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn admission_scope_mismatch_is_closed_before_plan_compile() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::Claude);
        let compiler = CountingCompiler::default();
        let session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::Claude,
                &evidence,
            ),
            &compiler,
            &EmptyStreamAdaptorRegistry,
            &ShapeAdmission {
                kind: AdmissionShape::Native,
                wrong_scope: true,
            },
        )
        .expect("scope mismatch is a closed diagnostic");
        assert!(session.decision().is_closed());
        assert!(
            session
                .decision()
                .blockers()
                .contains(&RouteGateBlocker::OwnershipScopeMismatch)
        );
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 0);
    }

    #[test]
    fn native_multiline_data_is_forwarded_from_the_single_parsed_frame() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Gemini, Protocol::Gemini);
        let mut session = compile_stream_session(spec(
            &registry,
            &rollout,
            Protocol::Gemini,
            Protocol::Gemini,
            &evidence,
        ))
        .expect("native route admits raw passthrough");
        let input = frame(b"data: first\ndata: second\n\n");
        assert_eq!(input.data, "first\nsecond");
        let StreamFrameOutput::RawPassthrough { bytes } =
            session.process_frame(&input).expect("raw multiline frame")
        else {
            panic!("expected raw output");
        };
        assert_eq!(bytes, input.raw.as_slice());
    }

    #[test]
    fn observer_tracks_stream_lifecycle_without_counting_completed_abort() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Gemini, Protocol::Gemini);
        let observer = ConversionObserver::default();
        let mut session = compile_stream_session(spec(
            &registry,
            &rollout,
            Protocol::Gemini,
            Protocol::Gemini,
            &evidence,
        ))
        .expect("native route admits raw passthrough")
        .with_observer(&observer);

        session
            .process_frame(&frame(b"data: telemetry\n\n"))
            .expect("raw frame is observed");
        session.mark_downstream_write();
        session.complete();
        drop(session);

        let snapshot = observer.snapshot();
        assert!(snapshot.samples.iter().any(|sample| {
            sample.metric == MetricKind::ConversionEventsTotal && sample.value == 1
        }));
        assert!(
            snapshot
                .samples
                .iter()
                .any(|sample| { sample.metric == MetricKind::StreamGatewayTtftSeconds })
        );
        assert!(
            snapshot.samples.iter().any(|sample| {
                sample.metric == MetricKind::StreamQueueDepth && sample.value == 0
            })
        );
        assert!(
            !snapshot
                .samples
                .iter()
                .any(|sample| { sample.metric == MetricKind::StreamClientAbortTotal })
        );
    }

    #[test]
    fn unknown_event_policy_covers_metadata_content_and_termination() {
        let metadata = unknown_stream_event_policy(false, Some("message_metadata"));
        assert_eq!(metadata.class, UnknownEventClass::Metadata);
        assert_eq!(metadata.action, UnknownEventAction::RecordLossAndContinue);
        assert_eq!(metadata.loss_code, Some(LOSS_UNKNOWN_EVENT));

        let content = unknown_stream_event_policy(false, Some("future_content"));
        assert_eq!(content.class, UnknownEventClass::Content);
        assert_eq!(content.action, UnknownEventAction::DegradedOrError);

        let termination = unknown_stream_event_policy(false, Some("metadata.complete"));
        assert_eq!(termination.class, UnknownEventClass::Termination);
        assert_eq!(termination.action, UnknownEventAction::DegradedOrError);
    }

    #[test]
    fn canonical_state_supports_parallel_blocks_and_rejects_bad_order_or_duplicate_terminal() {
        let mut state = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        let start = CanonicalStreamEvent::ResponseStart {
            id: "response".to_owned(),
            model: "model".to_owned(),
        };
        assert_eq!(
            state.apply(&CanonicalStreamEvent::TextDelta {
                index: 0,
                delta: "before start".to_owned(),
            }),
            Err(TypedStreamFailure::OutOfOrder)
        );
        state.apply(&start).expect("response start");
        state
            .apply(&CanonicalStreamEvent::ContentStart {
                index: 0,
                kind: lmm_contracts::relay::StreamContentKind::Text,
            })
            .expect("first block start");
        state
            .apply(&CanonicalStreamEvent::ContentStart {
                index: 1,
                kind: lmm_contracts::relay::StreamContentKind::Reasoning,
            })
            .expect("parallel block start");
        state
            .apply(&CanonicalStreamEvent::TextDelta {
                index: 0,
                delta: "one".to_owned(),
            })
            .expect("first delta");
        state
            .apply(&CanonicalStreamEvent::ContentEnd { index: 0 })
            .expect("first block end");
        assert_eq!(state.open_block_count(), 1);
        state
            .apply(&CanonicalStreamEvent::ContentEnd { index: 1 })
            .expect("second block end");
        state
            .apply(&CanonicalStreamEvent::ResponseEnd {
                finish_reason: lmm_contracts::relay::FinishReason::Stop,
                usage: None,
                model: None,
            })
            .expect("response end");
        assert!(state.terminal());
        assert!(state.usage_finalized());
        assert_eq!(
            state.apply(&CanonicalStreamEvent::ResponseEnd {
                finish_reason: lmm_contracts::relay::FinishReason::Stop,
                usage: None,
                model: None,
            }),
            Err(TypedStreamFailure::DuplicateTerminal)
        );
    }

    #[test]
    fn canonical_terminal_postludes_are_bounded_and_content_stays_closed() {
        let mut state = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        state
            .apply(&CanonicalStreamEvent::ResponseStart {
                id: "response".to_owned(),
                model: "model".to_owned(),
            })
            .expect("response start");
        state
            .apply(&CanonicalStreamEvent::ResponseEnd {
                finish_reason: lmm_contracts::relay::FinishReason::Error,
                usage: None,
                model: None,
            })
            .expect("error response end");
        state
            .apply(&CanonicalStreamEvent::Error {
                code: Some("upstream".to_owned()),
                message: "failed".to_owned(),
            })
            .expect("one checked error postlude");
        state
            .apply(&CanonicalStreamEvent::Cancelled)
            .expect("one checked cancellation postlude");
        assert!(state.terminal());
        assert!(state.cancelled());
        assert_eq!(
            state.apply(&CanonicalStreamEvent::Error {
                code: None,
                message: "duplicate".to_owned(),
            }),
            Err(TypedStreamFailure::DuplicateTerminal)
        );
        assert_eq!(
            state.apply(&CanonicalStreamEvent::Cancelled),
            Err(TypedStreamFailure::DuplicateTerminal)
        );
        assert_eq!(
            state.apply(&CanonicalStreamEvent::TextDelta {
                index: 0,
                delta: "late".to_owned(),
            }),
            Err(TypedStreamFailure::AfterTerminal)
        );
    }

    #[test]
    fn independent_error_and_cancellation_are_terminal_without_success_usage() {
        let mut error = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        error
            .apply(&CanonicalStreamEvent::ResponseStart {
                id: "error-response".to_owned(),
                model: "model".to_owned(),
            })
            .expect("response start");
        error
            .apply(&CanonicalStreamEvent::Error {
                code: None,
                message: "failed".to_owned(),
            })
            .expect("standalone error");
        assert!(error.terminal());
        assert!(!error.usage_finalized());
        assert_eq!(
            error.apply(&CanonicalStreamEvent::ResponseEnd {
                finish_reason: lmm_contracts::relay::FinishReason::Error,
                usage: None,
                model: None,
            }),
            Err(TypedStreamFailure::DuplicateTerminal)
        );

        let mut cancelled = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        cancelled
            .apply(&CanonicalStreamEvent::ResponseStart {
                id: "cancelled-response".to_owned(),
                model: "model".to_owned(),
            })
            .expect("response start");
        cancelled
            .apply(&CanonicalStreamEvent::Cancelled)
            .expect("standalone cancellation");
        assert!(cancelled.terminal());
        assert!(cancelled.cancelled());
        assert!(!cancelled.usage_finalized());
        assert_eq!(
            cancelled.apply(&CanonicalStreamEvent::ResponseEnd {
                finish_reason: lmm_contracts::relay::FinishReason::Cancelled,
                usage: None,
                model: None,
            }),
            Err(TypedStreamFailure::DuplicateTerminal)
        );
    }

    #[test]
    fn cancellation_and_drop_do_not_finalize_successful_usage() {
        let mut state = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        state
            .apply(&CanonicalStreamEvent::ResponseStart {
                id: "response".to_owned(),
                model: "model".to_owned(),
            })
            .expect("start");
        state.cancel().expect("cancel");
        assert!(state.cancelled());
        assert!(!state.terminal());
        assert!(!state.usage_finalized());
        let dropped = TypedStreamState::new(Protocol::Claude, Protocol::OpenAi);
        drop(dropped);
    }

    #[test]
    fn injected_adaptor_emits_one_frame_as_ordered_multi_event_batch() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let compile_calls = Arc::new(AtomicUsize::new(0));
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::clone(&compile_calls),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::Multi,
            },
        };
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("injected adaptor route");
        assert!(session.decision().is_typed());
        assert!(session.plan().is_some());
        assert_eq!(compiler.calls.load(Ordering::Relaxed), 1);
        assert_eq!(compile_calls.load(Ordering::Relaxed), 1);

        let input = frame(b"data: source-event\n\n");
        let output = session.process_frame(&input).expect("target output batch");
        let StreamFrameOutput::Typed { batch } = output else {
            panic!("typed adaptor did not return a typed batch");
        };
        assert_eq!(batch.len(), 3);
        assert!(batch.aggregate_bytes() > 0);
        assert!(matches!(
            &batch.items()[0],
            TypedStreamOutput::TargetFramed { .. }
        ));
        assert_eq!(process_calls.load(Ordering::Relaxed), 1);
        assert!(session.typed_state().is_some_and(TypedStreamState::started));
        assert_eq!(
            session
                .typed_state()
                .map(TypedStreamState::open_block_count),
            Some(1)
        );
    }

    #[test]
    fn terminal_postludes_are_admitted_across_frames_but_late_content_poisoned() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);

        let error_process_calls = Arc::new(AtomicUsize::new(0));
        let error_adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&error_process_calls),
                mode: MockBatchMode::ErrorPostlude,
            },
        };
        let mut error_session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &CountingCompiler::default(),
            &error_adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("error postlude route");
        let input = frame(b"data: first\n\n");
        error_session
            .process_frame(&input)
            .expect("terminal error batch");
        error_session
            .process_frame(&frame(b"data: error\n\n"))
            .expect("error postlude on the next frame");
        assert!(
            error_session
                .typed_state()
                .is_some_and(TypedStreamState::terminal)
        );
        assert_eq!(error_process_calls.load(Ordering::Relaxed), 2);
        assert_eq!(
            error_session.process_frame(&frame(b"data: duplicate-error\n\n")),
            Err(StreamProcessError::Stream(
                TypedStreamFailure::DuplicateTerminal
            ))
        );

        let cancelled_adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::new(AtomicUsize::new(0)),
                mode: MockBatchMode::CancelledPostlude,
            },
        };
        let mut cancelled_session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &CountingCompiler::default(),
            &cancelled_adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("cancellation postlude route");
        cancelled_session
            .process_frame(&input)
            .expect("terminal response batch");
        cancelled_session
            .process_frame(&frame(b"data: cancelled\n\n"))
            .expect("cancellation postlude on the next frame");
        assert!(
            cancelled_session
                .typed_state()
                .is_some_and(TypedStreamState::cancelled)
        );

        let late_content_adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::new(AtomicUsize::new(0)),
                mode: MockBatchMode::ContentAfterTerminal,
            },
        };
        let mut late_content_session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &CountingCompiler::default(),
            &late_content_adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("late content route");
        late_content_session
            .process_frame(&input)
            .expect("terminal response batch");
        assert_eq!(
            late_content_session.process_frame(&frame(b"data: late\n\n")),
            Err(StreamProcessError::Stream(
                TypedStreamFailure::AfterTerminal
            ))
        );
        assert!(late_content_session.is_poisoned());
        assert_eq!(
            late_content_session.process_frame(&frame(b"data: later\n\n")),
            Err(StreamProcessError::Stream(TypedStreamFailure::Poisoned))
        );
    }

    #[test]
    fn terminal_frames_reject_empty_or_loss_only_adaptor_batches() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let input = frame(b"data: first\n\n");

        for mode in [
            MockBatchMode::EmptyAfterTerminal,
            MockBatchMode::LossAfterTerminal,
        ] {
            let adaptors = MockAdaptorRegistry {
                adaptor: MockAdaptor {
                    compile_calls: Arc::new(AtomicUsize::new(0)),
                    process_calls: Arc::new(AtomicUsize::new(0)),
                    mode,
                },
            };
            let mut session = compile_stream_session_with_runtime(
                spec(
                    &registry,
                    &rollout,
                    Protocol::Claude,
                    Protocol::OpenAi,
                    &evidence,
                ),
                &CountingCompiler::default(),
                &adaptors,
                &AlwaysOpenAdmission,
            )
            .expect("terminal batch route");
            session
                .process_frame(&input)
                .expect("initial terminal batch");
            assert_eq!(
                session.process_frame(&frame(b"data: postlude\n\n")),
                Err(StreamProcessError::Stream(
                    TypedStreamFailure::AfterTerminal
                ))
            );
            assert!(session.is_poisoned());
        }
    }

    #[test]
    fn empty_typed_batch_does_not_start_output_or_poison_session() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::Empty,
            },
        };
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("empty batch adaptor route");
        let input = frame(b"data: source-event\n\n");
        let output = session.process_frame(&input).expect("empty typed batch");
        let StreamFrameOutput::Typed { batch } = output else {
            panic!("expected typed batch");
        };
        assert!(batch.is_empty());
        assert_eq!(batch.aggregate_bytes(), 0);
        assert!(!session.output_started());
        assert!(!session.is_poisoned());
        assert_eq!(process_calls.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn typed_output_item_limit_poison_is_setup_then_fail_closed() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::Multi,
            },
        };
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            )
            .with_output_limits(2, DEFAULT_MAX_TYPED_OUTPUT_BYTES),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("limited adaptor route");
        assert_eq!(
            session.process_frame(&frame(b"data: source-event\n\n")),
            Err(StreamProcessError::Setup(
                StreamSetupFailure::OutputItemsExceeded {
                    limit: 2,
                    observed: 3,
                }
            ))
        );
        assert!(session.is_poisoned());
        assert_eq!(
            session.process_frame(&frame(b"data: later\n\n")),
            Err(StreamProcessError::Setup(StreamSetupFailure::Poisoned))
        );
        assert_eq!(process_calls.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn typed_output_byte_limit_poison_is_setup_then_fail_closed() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::Multi,
            },
        };
        let max_bytes = 1;
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            )
            .with_output_limits(DEFAULT_MAX_TYPED_OUTPUT_ITEMS, max_bytes),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("limited adaptor route");
        let input = frame(b"data: source-event\n\n");
        let error = session.process_frame(&input);
        assert!(matches!(
            error,
            Err(StreamProcessError::Setup(
                StreamSetupFailure::OutputBytesExceeded { limit, observed }
            )) if limit == max_bytes && observed > max_bytes
        ));
        assert!(session.is_poisoned());
        assert_eq!(
            session.process_frame(&frame(b"data: later\n\n")),
            Err(StreamProcessError::Setup(StreamSetupFailure::Poisoned))
        );
        assert_eq!(process_calls.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn canonical_string_bytes_are_counted_and_poison_on_limit() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::CanonicalOversized,
            },
        };
        let max_bytes = 8;
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            )
            .with_output_limits(DEFAULT_MAX_TYPED_OUTPUT_ITEMS, max_bytes),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("canonical adaptor route");

        let input = frame(b"data: source-event\n\n");
        let error = session.process_frame(&input);
        assert!(matches!(
            error,
            Err(StreamProcessError::Setup(
                StreamSetupFailure::OutputBytesExceeded { limit, observed }
            )) if limit == max_bytes && observed > max_bytes
        ));
        assert!(session.is_poisoned());
        assert_eq!(
            session.process_frame(&frame(b"data: later\n\n")),
            Err(StreamProcessError::Setup(StreamSetupFailure::Poisoned))
        );
        assert_eq!(process_calls.load(Ordering::Relaxed), 1);
    }

    #[test]
    fn transition_error_after_first_batch_is_stream_stage_and_poisoned() {
        let registry = registry();
        let rollout = ProtocolRolloutControl::default().snapshot();
        let evidence = ownership(Protocol::Claude, Protocol::OpenAi);
        let compiler = CountingCompiler::default();
        let process_calls = Arc::new(AtomicUsize::new(0));
        let adaptors = MockAdaptorRegistry {
            adaptor: MockAdaptor {
                compile_calls: Arc::new(AtomicUsize::new(0)),
                process_calls: Arc::clone(&process_calls),
                mode: MockBatchMode::FailAfterFirst,
            },
        };
        let mut session = compile_stream_session_with_runtime(
            spec(
                &registry,
                &rollout,
                Protocol::Claude,
                Protocol::OpenAi,
                &evidence,
            ),
            &compiler,
            &adaptors,
            &AlwaysOpenAdmission,
        )
        .expect("poison test adaptor route");
        let first_input = frame(b"data: first\n\n");
        let first = session.process_frame(&first_input).expect("first batch");
        assert!(matches!(first, StreamFrameOutput::Typed { .. }));
        assert!(session.output_started());
        assert_eq!(
            session.process_frame(&frame(b"data: second\n\n")),
            Err(StreamProcessError::Stream(TypedStreamFailure::OutOfOrder))
        );
        assert!(session.is_poisoned());
        assert_eq!(
            session.process_frame(&frame(b"data: third\n\n")),
            Err(StreamProcessError::Stream(TypedStreamFailure::Poisoned))
        );
        assert_eq!(process_calls.load(Ordering::Relaxed), 2);
    }

    struct CountingCompiler {
        calls: AtomicUsize,
        delegate: ValidatedRegistryPlanCompiler,
    }

    impl Default for CountingCompiler {
        fn default() -> Self {
            Self {
                calls: AtomicUsize::new(0),
                delegate: ValidatedRegistryPlanCompiler,
            }
        }
    }

    impl StreamPlanCompiler for CountingCompiler {
        fn compile(
            &self,
            source: Protocol,
            target: Protocol,
            model_family: &str,
            registry: &ValidatedRegistry,
        ) -> Result<ConversionPlan, StreamSetupFailure> {
            self.calls.fetch_add(1, Ordering::Relaxed);
            if source != target {
                return Ok(ConversionPlan {
                    source,
                    target,
                    model_family: model_family.to_owned(),
                    converter_ids: vec!["mock-adaptor".to_owned()],
                    hop_count: 1,
                    fidelity: lmm_contracts::relay::Fidelity::Exact,
                    unsupported: Vec::new(),
                    losses: Vec::new(),
                    synthetic: Vec::new(),
                });
            }
            self.delegate
                .compile(source, target, model_family, registry)
        }
    }

    struct AlwaysOpenAdmission;

    #[derive(Clone, Copy)]
    enum AdmissionShape {
        Native,
        Cross,
    }

    struct ShapeAdmission {
        kind: AdmissionShape,
        wrong_scope: bool,
    }

    impl StreamRouteAdmission for ShapeAdmission {
        fn decide(
            &self,
            config: &ProtocolRolloutConfig,
            context: &RolloutContext<'_>,
            _registry: &ValidatedRegistry,
            _ownership: &OwnershipEvidence,
        ) -> RouteGateDecision {
            let scope = if self.wrong_scope {
                RouteOwnershipScope {
                    source: context.target,
                    target: context.source,
                    stream: !context.stream,
                }
            } else {
                RouteOwnershipScope {
                    source: context.source,
                    target: context.target,
                    stream: context.stream,
                }
            };
            let details = RouteGateDetails {
                scope,
                loss_policy: config.loss_policy(),
                flag_decision: config.decide(RolloutFlag::ConversionEngineV2, context),
                capability: None,
            };
            match self.kind {
                AdmissionShape::Native => RouteGateDecision::NativeRaw { details },
                AdmissionShape::Cross => RouteGateDecision::CrossProtocol { details },
            }
        }
    }

    impl StreamRouteAdmission for AlwaysOpenAdmission {
        fn decide(
            &self,
            config: &ProtocolRolloutConfig,
            context: &RolloutContext<'_>,
            _registry: &ValidatedRegistry,
            _ownership: &OwnershipEvidence,
        ) -> RouteGateDecision {
            RouteGateDecision::CrossProtocol {
                details: RouteGateDetails {
                    scope: RouteOwnershipScope {
                        source: context.source,
                        target: context.target,
                        stream: context.stream,
                    },
                    loss_policy: config.loss_policy(),
                    flag_decision: config.decide(RolloutFlag::ConversionEngineV2, context),
                    capability: None,
                },
            }
        }
    }

    struct MockAdaptorRegistry {
        adaptor: MockAdaptor,
    }

    impl StreamAdaptorRegistry for MockAdaptorRegistry {
        fn for_route(&self, source: Protocol, target: Protocol) -> Option<&dyn StreamAdaptor> {
            (source == Protocol::Claude && target == Protocol::OpenAi)
                .then_some(&self.adaptor as &dyn StreamAdaptor)
        }
    }

    struct MockAdaptor {
        compile_calls: Arc<AtomicUsize>,
        process_calls: Arc<AtomicUsize>,
        mode: MockBatchMode,
    }

    #[derive(Clone, Copy)]
    enum MockBatchMode {
        Multi,
        Empty,
        FailAfterFirst,
        CanonicalOversized,
        ErrorPostlude,
        CancelledPostlude,
        ContentAfterTerminal,
        EmptyAfterTerminal,
        LossAfterTerminal,
    }

    impl StreamAdaptor for MockAdaptor {
        fn source(&self) -> Protocol {
            Protocol::Claude
        }

        fn target(&self) -> Protocol {
            Protocol::OpenAi
        }

        fn compile(
            &self,
            _plan: &ConversionPlan,
        ) -> Result<Box<dyn StreamAdaptorSession>, StreamSetupFailure> {
            self.compile_calls.fetch_add(1, Ordering::Relaxed);
            Ok(Box::new(MockAdaptorSession {
                process_calls: Arc::clone(&self.process_calls),
                mode: self.mode,
                emitted: false,
            }))
        }
    }

    struct MockAdaptorSession {
        process_calls: Arc<AtomicUsize>,
        mode: MockBatchMode,
        emitted: bool,
    }

    impl StreamAdaptorSession for MockAdaptorSession {
        fn process_frame(
            &mut self,
            _frame: &SseFrame,
        ) -> Result<StreamAdaptorOutput, TypedStreamFailure> {
            self.process_calls.fetch_add(1, Ordering::Relaxed);
            if matches!(
                self.mode,
                MockBatchMode::ErrorPostlude
                    | MockBatchMode::CancelledPostlude
                    | MockBatchMode::ContentAfterTerminal
                    | MockBatchMode::EmptyAfterTerminal
                    | MockBatchMode::LossAfterTerminal
            ) {
                if self.emitted {
                    let event = match self.mode {
                        MockBatchMode::ErrorPostlude => CanonicalStreamEvent::Error {
                            code: Some("upstream".to_owned()),
                            message: "failed".to_owned(),
                        },
                        MockBatchMode::CancelledPostlude => CanonicalStreamEvent::Cancelled,
                        MockBatchMode::ContentAfterTerminal => CanonicalStreamEvent::TextDelta {
                            index: 0,
                            delta: "late".to_owned(),
                        },
                        MockBatchMode::EmptyAfterTerminal => {
                            return Ok(StreamAdaptorOutput::empty());
                        }
                        MockBatchMode::LossAfterTerminal => {
                            return Ok(StreamAdaptorOutput::new(vec![StreamAdaptorItem::Loss(
                                StreamLoss {
                                    code: LOSS_UNKNOWN_EVENT,
                                    class: UnknownEventClass::Metadata,
                                },
                            )]));
                        }
                        MockBatchMode::Multi
                        | MockBatchMode::Empty
                        | MockBatchMode::FailAfterFirst
                        | MockBatchMode::CanonicalOversized => unreachable!(
                            "terminal postlude branch only handles terminal-postlude modes"
                        ),
                    };
                    return Ok(StreamAdaptorOutput::new(vec![
                        StreamAdaptorItem::Canonical { event },
                    ]));
                }
                self.emitted = true;
                let finish_reason = if matches!(self.mode, MockBatchMode::ErrorPostlude) {
                    lmm_contracts::relay::FinishReason::Error
                } else {
                    lmm_contracts::relay::FinishReason::Stop
                };
                return Ok(StreamAdaptorOutput::new(vec![
                    StreamAdaptorItem::Canonical {
                        event: CanonicalStreamEvent::ResponseStart {
                            id: "response".to_owned(),
                            model: "model".to_owned(),
                        },
                    },
                    StreamAdaptorItem::Canonical {
                        event: CanonicalStreamEvent::ResponseEnd {
                            finish_reason,
                            usage: None,
                            model: None,
                        },
                    },
                ]));
            }
            if matches!(self.mode, MockBatchMode::Empty) {
                return Ok(StreamAdaptorOutput::empty());
            }
            if matches!(self.mode, MockBatchMode::CanonicalOversized) {
                return Ok(StreamAdaptorOutput::new(vec![
                    StreamAdaptorItem::Canonical {
                        event: CanonicalStreamEvent::ResponseStart {
                            id: "canonical-id-too-large".to_owned(),
                            model: "canonical-model-too-large".to_owned(),
                        },
                    },
                ]));
            }
            if self.emitted && matches!(self.mode, MockBatchMode::FailAfterFirst) {
                return Ok(StreamAdaptorOutput::new(vec![
                    StreamAdaptorItem::Canonical {
                        event: CanonicalStreamEvent::TextDelta {
                            index: 99,
                            delta: "invalid-index".to_owned(),
                        },
                    },
                ]));
            }
            self.emitted = true;
            Ok(StreamAdaptorOutput::new(vec![
                StreamAdaptorItem::TargetFramed {
                    event: CanonicalStreamEvent::ResponseStart {
                        id: "target".to_owned(),
                        model: "target-model".to_owned(),
                    },
                    bytes: b"data: target-start\n\n".to_vec(),
                },
                StreamAdaptorItem::TargetFramed {
                    event: CanonicalStreamEvent::ContentStart {
                        index: 0,
                        kind: lmm_contracts::relay::StreamContentKind::Text,
                    },
                    bytes: b"data: target-content-start\n\n".to_vec(),
                },
                StreamAdaptorItem::TargetFramed {
                    event: CanonicalStreamEvent::TextDelta {
                        index: 0,
                        delta: "target".to_owned(),
                    },
                    bytes: b"data: target-delta\n\n".to_vec(),
                },
            ]))
        }

        fn cancel(&mut self) -> Result<(), TypedStreamFailure> {
            Ok(())
        }
    }
}
