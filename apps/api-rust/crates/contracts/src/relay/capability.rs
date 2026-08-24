//! Capability and loss contracts shared by relay planning and registration.
//!
//! These types intentionally contain no provider-specific conversion logic.  A
//! registry entry describes what a route can do, while a [`ConversionPlan`]
//! freezes that decision before a request (and, in particular, before a stream)
//! starts.  Losses are structured so callers can choose to reject, warn, or
//! explicitly allow a known degradation without silently dropping data.

use std::{error::Error, fmt};

use serde::{Deserialize, Serialize};

use super::{Protocol, Registry, RegistryValidationError, RouteRegistration, ValidatedRegistry};

/// A semantic feature that may be carried by a relay request or response.
///
/// The order is stable and is used when generating deterministic support
/// matrices and loss ledgers.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Feature {
    /// Plain text content.
    Text,
    /// A system message.
    SystemMessage,
    /// A developer message.
    DeveloperMessage,
    /// An image input or output.
    Image,
    /// Audio content.
    Audio,
    /// Video content.
    Video,
    /// A file input or output.
    File,
    /// JSON object response mode.
    JsonObject,
    /// JSON schema response mode.
    JsonSchema,
    /// A function/tool call.
    FunctionCall,
    /// Multiple function/tool calls in one turn.
    ParallelFunctionCall,
    /// A stable function/tool call identifier.
    FunctionCallId,
    /// A multimodal function result.
    FunctionResultMultimodal,
    /// Automatic tool selection.
    ToolChoiceAuto,
    /// Required tool selection.
    ToolChoiceRequired,
    /// Named tool selection.
    ToolChoiceNamed,
    /// A provider built-in web-search tool.
    BuiltinWebSearch,
    /// A provider built-in file-search tool.
    BuiltinFileSearch,
    /// A provider built-in code-execution tool.
    BuiltinCodeExecution,
    /// Model Context Protocol support.
    Mcp,
    /// Computer-use control.
    ComputerUse,
    /// Stateful conversation storage or continuation.
    StatefulConversation,
    /// OpenAI Responses previous-response continuation.
    PreviousResponseId,
    /// A provider prompt template reference.
    PromptTemplate,
    /// A human-readable reasoning summary.
    ReasoningSummary,
    /// An opaque provider reasoning signature.
    OpaqueReasoningSignature,
    /// Redacted provider reasoning.
    RedactedReasoning,
    /// Provider cache-control instructions.
    CacheControl,
    /// Citation data.
    Citations,
    /// Grounding metadata.
    GroundingMetadata,
    /// Safety metadata.
    SafetyMetadata,
    /// Token usage accounting.
    Usage,
    /// Cached-token usage accounting.
    CacheUsage,
    /// Streaming response events.
    Streaming,
    /// Forwarding an event that the current DTO does not know.
    UnknownEventPassthrough,
}

impl Feature {
    /// Every feature in the stable PLAN order.
    pub const ALL: [Self; 35] = [
        Self::Text,
        Self::SystemMessage,
        Self::DeveloperMessage,
        Self::Image,
        Self::Audio,
        Self::Video,
        Self::File,
        Self::JsonObject,
        Self::JsonSchema,
        Self::FunctionCall,
        Self::ParallelFunctionCall,
        Self::FunctionCallId,
        Self::FunctionResultMultimodal,
        Self::ToolChoiceAuto,
        Self::ToolChoiceRequired,
        Self::ToolChoiceNamed,
        Self::BuiltinWebSearch,
        Self::BuiltinFileSearch,
        Self::BuiltinCodeExecution,
        Self::Mcp,
        Self::ComputerUse,
        Self::StatefulConversation,
        Self::PreviousResponseId,
        Self::PromptTemplate,
        Self::ReasoningSummary,
        Self::OpaqueReasoningSignature,
        Self::RedactedReasoning,
        Self::CacheControl,
        Self::Citations,
        Self::GroundingMetadata,
        Self::SafetyMetadata,
        Self::Usage,
        Self::CacheUsage,
        Self::Streaming,
        Self::UnknownEventPassthrough,
    ];

    /// Returns all features as a deterministic slice.
    pub const fn all() -> &'static [Self] {
        &Self::ALL
    }
}

/// The semantic fidelity of a route.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Fidelity {
    /// The source representation can be restored byte-for-byte or by raw
    /// passthrough, including provider state and event ordering.
    Exact,
    /// Semantics are preserved while names, defaults, or event granularity are
    /// normalized.
    Normalized,
    /// Some information is intentionally unavailable but a caller may opt in.
    Lossy,
    /// The route cannot safely express the source semantics.
    Unsupported,
}

impl Fidelity {
    /// Whether this fidelity level can represent a usable route.
    pub const fn is_supported(self) -> bool {
        !matches!(self, Self::Unsupported)
    }
}

/// Stable codes for information loss and gateway-synthesized fields.
///
/// The serialized names are snake_case versions of the stable code names in
/// PLAN (for example, `LOSS_TOOL_CALL_ID` becomes
/// `loss_tool_call_id`). [`Self::stable_code`] retains the uppercase form for
/// metrics and logs that use the original code spelling.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum LossCode {
    /// Stateful conversation context could not be carried over.
    LossStatefulContext,
    /// A built-in provider tool could not be carried over.
    LossBuiltinTool,
    /// A custom tool could not be carried over.
    LossCustomTool,
    /// A tool call identifier could not be preserved.
    LossToolCallId,
    /// An opaque reasoning signature could not be preserved.
    LossOpaqueReasoning,
    /// Redacted reasoning could not be preserved.
    LossRedactedReasoning,
    /// Content ordering could not be preserved.
    LossContentOrder,
    /// Citation data could not be preserved.
    LossCitation,
    /// Grounding metadata could not be preserved.
    LossGroundingMetadata,
    /// Safety metadata could not be preserved.
    LossSafetyMetadata,
    /// Cache-control instructions could not be preserved.
    LossCacheControl,
    /// An unknown event could not be carried over.
    LossUnknownEvent,
    /// A tool call identifier was synthesized by the gateway.
    SyntheticToolCallId,
    /// A thought signature was synthesized by the gateway.
    SyntheticThoughtSignature,
}

impl LossCode {
    /// All stable loss codes in declaration order.
    pub const ALL: [Self; 14] = [
        Self::LossStatefulContext,
        Self::LossBuiltinTool,
        Self::LossCustomTool,
        Self::LossToolCallId,
        Self::LossOpaqueReasoning,
        Self::LossRedactedReasoning,
        Self::LossContentOrder,
        Self::LossCitation,
        Self::LossGroundingMetadata,
        Self::LossSafetyMetadata,
        Self::LossCacheControl,
        Self::LossUnknownEvent,
        Self::SyntheticToolCallId,
        Self::SyntheticThoughtSignature,
    ];

    /// Returns every stable code in deterministic order.
    pub const fn all() -> &'static [Self] {
        &Self::ALL
    }

    /// Returns the uppercase stable code used by metrics and diagnostics.
    pub const fn stable_code(self) -> &'static str {
        match self {
            Self::LossStatefulContext => "LOSS_STATEFUL_CONTEXT",
            Self::LossBuiltinTool => "LOSS_BUILTIN_TOOL",
            Self::LossCustomTool => "LOSS_CUSTOM_TOOL",
            Self::LossToolCallId => "LOSS_TOOL_CALL_ID",
            Self::LossOpaqueReasoning => "LOSS_OPAQUE_REASONING",
            Self::LossRedactedReasoning => "LOSS_REDACTED_REASONING",
            Self::LossContentOrder => "LOSS_CONTENT_ORDER",
            Self::LossCitation => "LOSS_CITATION",
            Self::LossGroundingMetadata => "LOSS_GROUNDING_METADATA",
            Self::LossSafetyMetadata => "LOSS_SAFETY_METADATA",
            Self::LossCacheControl => "LOSS_CACHE_CONTROL",
            Self::LossUnknownEvent => "LOSS_UNKNOWN_EVENT",
            Self::SyntheticToolCallId => "SYNTHETIC_TOOL_CALL_ID",
            Self::SyntheticThoughtSignature => "SYNTHETIC_THOUGHT_SIGNATURE",
        }
    }

    /// Whether this code represents a semantic loss that reject policy must
    /// refuse by default.
    pub const fn is_critical(self) -> bool {
        matches!(
            self,
            Self::LossStatefulContext
                | Self::LossBuiltinTool
                | Self::LossCustomTool
                | Self::LossToolCallId
                | Self::LossOpaqueReasoning
                | Self::LossRedactedReasoning
                | Self::LossContentOrder
                | Self::LossSafetyMetadata
                | Self::LossUnknownEvent
        )
    }

    // Short aliases make the API pleasant for callers while the prefixed
    // variants preserve the stable wire spelling.
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossStatefulContext`].
    pub const StatefulContext: Self = Self::LossStatefulContext;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossBuiltinTool`].
    pub const BuiltinTool: Self = Self::LossBuiltinTool;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossCustomTool`].
    pub const CustomTool: Self = Self::LossCustomTool;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossToolCallId`].
    pub const ToolCallId: Self = Self::LossToolCallId;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossOpaqueReasoning`].
    pub const OpaqueReasoning: Self = Self::LossOpaqueReasoning;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossRedactedReasoning`].
    pub const RedactedReasoning: Self = Self::LossRedactedReasoning;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossContentOrder`].
    pub const ContentOrder: Self = Self::LossContentOrder;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossCitation`].
    pub const Citation: Self = Self::LossCitation;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossGroundingMetadata`].
    pub const GroundingMetadata: Self = Self::LossGroundingMetadata;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossSafetyMetadata`].
    pub const SafetyMetadata: Self = Self::LossSafetyMetadata;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossCacheControl`].
    pub const CacheControl: Self = Self::LossCacheControl;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::LossUnknownEvent`].
    pub const UnknownEvent: Self = Self::LossUnknownEvent;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::SyntheticToolCallId`].
    pub const SyntheticToolCall: Self = Self::SyntheticToolCallId;
    #[allow(non_upper_case_globals)]
    /// Alias for [`Self::SyntheticThoughtSignature`].
    pub const SyntheticThought: Self = Self::SyntheticThoughtSignature;
}

/// Policy selected by an administrator for known information loss.
#[derive(
    Clone, Copy, Debug, Default, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize,
)]
#[serde(rename_all = "snake_case")]
pub enum LossPolicy {
    /// Reject plans containing critical losses.
    #[default]
    Reject,
    /// Continue with losses recorded as structured warnings.
    Warn,
    /// Continue with explicit opt-in to all non-unsupported losses.
    Allow,
}

/// Whether a loss is critical or can be treated as metadata degradation.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum LossSeverity {
    /// The route changes behavior or may make a provider request invalid.
    Critical,
    /// The route drops metadata while retaining core semantics.
    Warning,
}

/// One structured, de-duplicable loss entry.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub struct Loss {
    /// Stable loss code.
    pub code: LossCode,
    /// Feature affected by the loss, when known.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub feature: Option<Feature>,
    /// Source request/response path, such as `tools[1]`.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub path: Option<String>,
    /// Safe explanatory detail.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub message: Option<String>,
    /// Explicit severity retained in the ledger for audit and policy checks.
    pub severity: LossSeverity,
}

impl Loss {
    /// Creates a loss with severity inferred from its stable code.
    pub fn new(code: LossCode, feature: Option<Feature>) -> Self {
        Self {
            severity: if code.is_critical() {
                LossSeverity::Critical
            } else {
                LossSeverity::Warning
            },
            code,
            feature,
            path: None,
            message: None,
        }
    }

    /// Adds a source path to this loss.
    pub fn at(mut self, path: impl Into<String>) -> Self {
        self.path = Some(path.into());
        self
    }

    /// Adds safe explanatory detail to this loss.
    pub fn with_message(mut self, message: impl Into<String>) -> Self {
        self.message = Some(message.into());
        self
    }

    /// Overrides the inferred severity for a route-specific decision.
    pub fn with_severity(mut self, severity: LossSeverity) -> Self {
        self.severity = severity;
        self
    }

    /// Returns whether policy should treat this loss as critical.
    pub const fn is_critical(&self) -> bool {
        matches!(self.severity, LossSeverity::Critical)
    }
}

/// An ordered ledger of unique losses.
#[derive(Clone, Debug, Default, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub struct LossLedger {
    /// Unique losses, kept in deterministic order after insertion.
    pub losses: Vec<Loss>,
}

impl LossLedger {
    /// Creates an empty ledger.
    pub const fn new() -> Self {
        Self { losses: Vec::new() }
    }

    /// Creates a ledger and de-duplicates its entries.
    pub fn from_losses(losses: impl IntoIterator<Item = Loss>) -> Self {
        let mut ledger = Self::new();
        for loss in losses {
            ledger.record(loss);
        }
        ledger
    }

    /// Records a loss exactly once.
    pub fn record(&mut self, loss: Loss) {
        if !self.losses.contains(&loss) {
            self.losses.push(loss);
            self.losses.sort();
        }
    }

    /// Records all entries from another ledger.
    pub fn extend(&mut self, other: impl IntoIterator<Item = Loss>) {
        for loss in other {
            self.record(loss);
        }
    }

    /// Returns the number of unique loss entries.
    pub fn len(&self) -> usize {
        self.losses.len()
    }

    /// Returns whether this ledger has no loss entries.
    pub fn is_empty(&self) -> bool {
        self.losses.is_empty()
    }

    /// Returns whether any critical loss is present.
    pub fn has_critical(&self) -> bool {
        self.losses.iter().any(Loss::is_critical)
    }

    /// Returns a de-duplicated view containing only critical losses.
    pub fn critical(&self) -> Self {
        Self::from_losses(
            self.losses
                .iter()
                .filter(|loss| loss.is_critical())
                .cloned(),
        )
    }

    /// Returns the ledger entries as a slice.
    pub fn as_slice(&self) -> &[Loss] {
        &self.losses
    }
}

/// A field synthesized by the gateway because a provider did not supply it.
#[derive(Clone, Copy, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum SyntheticField {
    /// A stable synthetic tool call identifier.
    ToolCallId,
    /// A synthetic provider thought signature.
    ThoughtSignature,
}

impl SyntheticField {
    /// All currently supported synthetic fields.
    pub const ALL: [Self; 2] = [Self::ToolCallId, Self::ThoughtSignature];

    /// Returns all synthetic fields in deterministic order.
    pub const fn all() -> &'static [Self] {
        &Self::ALL
    }
}

/// A compiled route decision made before request or stream execution.
#[derive(Clone, Debug, PartialEq, Serialize, Deserialize)]
pub struct ConversionPlan {
    /// Source protocol received by the gateway.
    pub source: Protocol,
    /// Target protocol sent to the upstream provider.
    pub target: Protocol,
    /// Normalized model-family identifier used for route constraints.
    pub model_family: String,
    /// Converter identifiers selected by the registry.
    pub converter_ids: Vec<String>,
    /// Number of conversion hops (one for a direct route, zero for raw).
    pub hop_count: usize,
    /// Route-level fidelity.
    pub fidelity: Fidelity,
    /// Features that cannot be represented on this route.
    pub unsupported: Vec<Feature>,
    /// Structured losses discovered while compiling or inspecting the input.
    pub losses: Vec<Loss>,
    /// Fields that a converter will synthesize.
    pub synthetic: Vec<SyntheticField>,
}

impl ConversionPlan {
    /// Compiles a plan from the process-wide built-in registry.
    pub fn compile(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
    ) -> Result<Self, PlanCompileError> {
        Self::compile_for_features(source, target, model_family, std::iter::empty::<Feature>())
    }

    /// Compiles a plan from an explicit registry snapshot.
    pub fn compile_with_registry(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        registry: &Registry,
    ) -> Result<Self, PlanCompileError> {
        Self::compile_with_registry_for_features(
            source,
            target,
            model_family,
            registry,
            std::iter::empty::<Feature>(),
        )
    }

    /// Compiles a plan for the features actually present in a request.
    ///
    /// Supported routes report only the intersection of the request features
    /// and the route's explicitly unsupported features.  A route whose
    /// quality is [`Fidelity::Unsupported`] remains non-executable even when
    /// the caller supplies an empty feature set, so a compatibility caller
    /// cannot accidentally turn an unsupported pair into a successful plan.
    pub fn compile_for_features(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        features: impl IntoIterator<Item = Feature>,
    ) -> Result<Self, PlanCompileError> {
        Self::compile_with_registry_for_features(
            source,
            target,
            model_family,
            &Registry::default(),
            features,
        )
    }

    /// Compiles a feature-aware plan from an explicit registry snapshot.
    ///
    /// The feature iterator is consumed before the returned plan is built;
    /// duplicate features are removed and the unsupported list is stable.
    pub fn compile_with_registry_for_features(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        registry: &Registry,
        features: impl IntoIterator<Item = Feature>,
    ) -> Result<Self, PlanCompileError> {
        registry
            .validate()
            .map_err(PlanCompileError::RegistryInvalid)?;
        Self::compile_from_registry_for_features(source, target, model_family, registry, features)
    }

    /// Compiles a plan from a registry that already passed runtime catalog
    /// validation. This is the production path for avoiding a second,
    /// independent capability decision.
    pub fn compile_with_validated_registry(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        registry: &ValidatedRegistry,
    ) -> Result<Self, PlanCompileError> {
        Self::compile_with_validated_registry_for_features(
            source,
            target,
            model_family,
            registry,
            std::iter::empty::<Feature>(),
        )
    }

    /// Compiles a feature-aware plan from a runtime-validated registry.
    pub fn compile_with_validated_registry_for_features(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        registry: &ValidatedRegistry,
        features: impl IntoIterator<Item = Feature>,
    ) -> Result<Self, PlanCompileError> {
        Self::compile_from_registry_for_features(
            source,
            target,
            model_family,
            registry.registry(),
            features,
        )
    }

    fn compile_from_registry_for_features(
        source: Protocol,
        target: Protocol,
        model_family: impl Into<String>,
        registry: &Registry,
        features: impl IntoIterator<Item = Feature>,
    ) -> Result<Self, PlanCompileError> {
        let model_family = model_family.into();
        let route = registry
            .route(source, target)
            .ok_or(PlanCompileError::MissingRoute { source, target })?;
        if !route.matches_model_family(&model_family) {
            return Err(PlanCompileError::ModelConstraint {
                source,
                target,
                model_family,
            });
        }

        let requested_features = features.into_iter().collect::<Vec<_>>();
        let mut unsupported = if route.quality == Fidelity::Unsupported {
            let declared = route
                .unsupported_features
                .iter()
                .copied()
                .collect::<Vec<_>>();
            if declared.is_empty() {
                Feature::all().to_vec()
            } else {
                declared
            }
        } else {
            requested_features
                .into_iter()
                .filter(|feature| route.unsupported_features.contains(feature))
                .collect()
        };
        unsupported.sort();
        unsupported.dedup();

        let mut converter_ids = route
            .converter_ids()
            .into_iter()
            .map(str::to_owned)
            .collect::<Vec<_>>();
        converter_ids.sort();
        converter_ids.dedup();

        Ok(Self {
            source,
            target,
            model_family,
            hop_count: if source == target { 0 } else { 1 },
            fidelity: route.quality,
            converter_ids,
            unsupported,
            losses: Vec::new(),
            synthetic: Vec::new(),
        })
    }

    /// Adds one input-specific loss to this already compiled plan.
    pub fn add_loss(&mut self, loss: Loss) {
        if !self.losses.contains(&loss) {
            self.losses.push(loss);
            self.losses.sort();
        }
        if matches!(self.fidelity, Fidelity::Exact | Fidelity::Normalized) {
            self.fidelity = Fidelity::Lossy;
        }
    }

    /// Adds one synthesized field to this plan exactly once.
    pub fn add_synthetic(&mut self, field: SyntheticField) {
        if !self.synthetic.contains(&field) {
            self.synthetic.push(field);
            self.synthetic.sort();
        }
    }

    /// Returns this plan's losses as a de-duplicated ledger.
    pub fn loss_ledger(&self) -> LossLedger {
        LossLedger::from_losses(self.losses.iter().cloned())
    }

    /// Applies an administrator policy before execution starts.
    ///
    /// Unsupported route features are always rejected.  `reject` additionally
    /// rejects critical losses; `warn` also refuses critical semantic loss but
    /// permits metadata loss with a structured warning; `allow` explicitly
    /// opts into all non-unsupported losses.
    pub fn enforce(&self, policy: LossPolicy) -> Result<PolicyOutcome, ConversionPolicyError> {
        if let Some(feature) = self.unsupported.first().copied() {
            return Err(ConversionPolicyError::unsupported(
                self.source,
                self.target,
                policy,
                self.unsupported.clone(),
                feature,
            ));
        }

        let ledger = self.loss_ledger();
        if matches!(policy, LossPolicy::Reject | LossPolicy::Warn) && ledger.has_critical() {
            return Err(ConversionPolicyError::loss_rejected(
                self.source,
                self.target,
                policy,
                ledger,
            ));
        }

        Ok(PolicyOutcome {
            accepted: true,
            policy,
            warnings: ledger,
        })
    }

    /// Alias for [`Self::enforce`] that reads naturally at a request boundary.
    pub fn apply_policy(&self, policy: LossPolicy) -> Result<PolicyOutcome, ConversionPolicyError> {
        self.enforce(policy)
    }
}

/// The result of applying a loss policy to a compiled plan.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct PolicyOutcome {
    /// Whether the request may proceed.
    pub accepted: bool,
    /// Policy that produced this decision.
    pub policy: LossPolicy,
    /// Losses retained for warning/metric/audit emission.
    pub warnings: LossLedger,
}

/// Stable machine-readable error returned when a plan cannot be executed.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct ConversionPolicyError {
    /// Machine-readable error code.
    pub code: &'static str,
    /// Source protocol.
    pub source_format: Protocol,
    /// Target protocol.
    pub target_format: Protocol,
    /// Policy that was being applied.
    pub policy: LossPolicy,
    /// First unsupported feature, when one exists.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub feature: Option<Feature>,
    /// All unsupported features discovered by the registry.
    pub unsupported: Vec<Feature>,
    /// Structured losses that caused rejection.
    pub losses: LossLedger,
    /// This error is not retryable without changing the route or policy.
    pub retryable: bool,
}

impl ConversionPolicyError {
    fn unsupported(
        source_format: Protocol,
        target_format: Protocol,
        policy: LossPolicy,
        unsupported: Vec<Feature>,
        feature: Feature,
    ) -> Self {
        Self {
            code: "conversion_unsupported_feature",
            source_format,
            target_format,
            policy,
            feature: Some(feature),
            unsupported,
            losses: LossLedger::new(),
            retryable: false,
        }
    }

    fn loss_rejected(
        source_format: Protocol,
        target_format: Protocol,
        policy: LossPolicy,
        losses: LossLedger,
    ) -> Self {
        Self {
            code: "conversion_loss_rejected",
            source_format,
            target_format,
            policy,
            feature: losses.losses.first().and_then(|loss| loss.feature),
            unsupported: Vec::new(),
            losses,
            retryable: false,
        }
    }
}

impl fmt::Display for ConversionPolicyError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{}: {:?} -> {:?}",
            self.code, self.source_format, self.target_format
        )
    }
}

impl Error for ConversionPolicyError {}

/// Errors encountered while compiling a plan from a registry snapshot.
#[derive(Debug)]
pub enum PlanCompileError {
    /// The registry contains an invalid claim.
    RegistryInvalid(RegistryValidationError),
    /// No registration exists for this pair.
    MissingRoute { source: Protocol, target: Protocol },
    /// The route excludes the requested model family.
    ModelConstraint {
        source: Protocol,
        target: Protocol,
        model_family: String,
    },
}

impl fmt::Display for PlanCompileError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        match self {
            Self::RegistryInvalid(error) => write!(formatter, "invalid relay registry: {error}"),
            Self::MissingRoute { source, target } => {
                write!(formatter, "no relay route from {source:?} to {target:?}")
            }
            Self::ModelConstraint {
                source,
                target,
                model_family,
            } => write!(
                formatter,
                "model family {model_family:?} is not allowed for {source:?} -> {target:?}"
            ),
        }
    }
}

impl Error for PlanCompileError {}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn feature_serialization_uses_stable_snake_case() {
        let json = serde_json::to_string(&Feature::UnknownEventPassthrough).expect("serialize");
        assert_eq!(json, "\"unknown_event_passthrough\"");
    }

    #[test]
    fn loss_code_retains_uppercase_metric_name() {
        assert_eq!(LossCode::LossToolCallId.stable_code(), "LOSS_TOOL_CALL_ID");
    }

    #[test]
    fn loss_ledger_sorts_and_deduplicates_entries() {
        let loss = Loss::new(LossCode::LossCitation, Some(Feature::Citations));
        let ledger = LossLedger::from_losses([loss.clone(), loss]);
        assert_eq!(ledger.len(), 1);
        assert_eq!(ledger.as_slice()[0].code, LossCode::LossCitation);
    }

    #[test]
    fn reject_warn_and_allow_have_distinct_critical_loss_behavior() {
        let mut plan = ConversionPlan::compile(Protocol::OpenAi, Protocol::OpenAi, "gpt")
            .expect("registered raw route");
        plan.add_loss(Loss::new(
            LossCode::LossToolCallId,
            Some(Feature::FunctionCallId),
        ));

        assert!(plan.enforce(LossPolicy::Reject).is_err());
        assert!(plan.enforce(LossPolicy::Warn).is_err());
        assert!(plan.enforce(LossPolicy::Allow).expect("allow").accepted);
    }

    #[test]
    fn warn_accepts_noncritical_metadata_loss_and_keeps_a_warning_ledger() {
        let mut plan = ConversionPlan::compile(Protocol::OpenAi, Protocol::OpenAi, "gpt")
            .expect("registered raw route");
        plan.add_loss(Loss::new(LossCode::LossCitation, Some(Feature::Citations)));
        let outcome = plan.enforce(LossPolicy::Warn).expect("metadata warning");
        assert_eq!(outcome.warnings.len(), 1);
    }

    #[test]
    fn adding_a_loss_downgrades_normalized_but_not_unsupported_fidelity() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::OpenAi, Protocol::OpenAiResponses)
            .expect("complete matrix route");
        route.request_supported = true;
        route.response_supported = true;
        route.stream_supported = true;
        route.request_converter_id = Some("test-request-v1".to_owned());
        route.response_converter_id = Some("test-response-v1".to_owned());
        route.stream_converter_id = Some("test-stream-v1".to_owned());
        route.stream_finalizer_id = Some("test-finalizer-v1".to_owned());
        route.runtime_adaptors = vec!["test-runtime-v1".to_owned()];
        route.feature_requirements = [Feature::Text].into_iter().collect();
        route.unsupported_features = [Feature::Citations].into_iter().collect();
        route.quality = Fidelity::Normalized;
        let mut normalized = ConversionPlan::compile_with_registry(
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            "gpt",
            &registry,
        )
        .expect("registered normalized route");
        assert_eq!(normalized.fidelity, Fidelity::Normalized);
        normalized.add_loss(Loss::new(LossCode::LossCitation, Some(Feature::Citations)));
        assert_eq!(normalized.fidelity, Fidelity::Lossy);

        let mut unsupported_registry = Registry::default();
        let unsupported_route = unsupported_registry
            .route_mut(Protocol::Claude, Protocol::Gemini)
            .expect("complete matrix route");
        *unsupported_route = RouteRegistration::unsupported(Protocol::Claude, Protocol::Gemini);
        let mut unsupported = ConversionPlan::compile_with_registry(
            Protocol::Claude,
            Protocol::Gemini,
            "claude",
            &unsupported_registry,
        )
        .expect("registered unsupported route");
        unsupported.add_loss(Loss::new(LossCode::LossCitation, Some(Feature::Citations)));
        assert_eq!(unsupported.fidelity, Fidelity::Unsupported);
    }

    #[test]
    fn feature_aware_compilation_only_reports_requested_unsupported_features() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::OpenAi, Protocol::OpenAiResponses)
            .expect("complete matrix route");
        route.request_supported = true;
        route.response_supported = true;
        route.stream_supported = true;
        route.request_converter_id = Some("test-request-v1".to_owned());
        route.response_converter_id = Some("test-response-v1".to_owned());
        route.stream_converter_id = Some("test-stream-v1".to_owned());
        route.stream_finalizer_id = Some("test-finalizer-v1".to_owned());
        route.runtime_adaptors = vec!["test-runtime-v1".to_owned()];
        route.feature_requirements = [Feature::Text].into_iter().collect();
        route.unsupported_features = [Feature::Citations].into_iter().collect();
        route.quality = Fidelity::Normalized;
        let plan = ConversionPlan::compile_with_registry_for_features(
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            "gpt",
            &registry,
            [Feature::Citations, Feature::Text, Feature::Citations],
        )
        .expect("registered normalized route");

        assert_eq!(plan.unsupported, vec![Feature::Citations]);
        assert!(!plan.unsupported.contains(&Feature::Text));

        let compatibility_plan = ConversionPlan::compile_with_registry(
            Protocol::OpenAi,
            Protocol::OpenAiResponses,
            "gpt",
            &registry,
        )
        .expect("registered normalized route");
        assert!(compatibility_plan.unsupported.is_empty());
    }

    #[test]
    fn feature_aware_compilation_keeps_unsupported_routes_non_executable() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::Claude, Protocol::Gemini)
            .expect("complete matrix route");
        *route = RouteRegistration::unsupported(Protocol::Claude, Protocol::Gemini);
        let plan = ConversionPlan::compile_with_registry_for_features(
            Protocol::Claude,
            Protocol::Gemini,
            "claude",
            &registry,
            std::iter::empty::<Feature>(),
        )
        .expect("registered unsupported route");

        assert_eq!(plan.fidelity, Fidelity::Unsupported);
        assert!(!plan.unsupported.is_empty());
        assert!(plan.enforce(LossPolicy::Allow).is_err());
    }

    #[test]
    fn unsupported_route_is_rejected_even_when_loss_policy_allows_losses() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::Claude, Protocol::Gemini)
            .expect("complete matrix route");
        *route = RouteRegistration::unsupported(Protocol::Claude, Protocol::Gemini);
        let plan = ConversionPlan::compile_with_registry(
            Protocol::Claude,
            Protocol::Gemini,
            "claude",
            &registry,
        )
        .expect("all protocol pairs are registered");
        let error = plan.enforce(LossPolicy::Allow).expect_err("unsupported");
        assert_eq!(error.code, "conversion_unsupported_feature");
    }
}
