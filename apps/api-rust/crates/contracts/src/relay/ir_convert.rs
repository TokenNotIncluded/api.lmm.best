// Protocol mappers deliberately receive explicit source/target/path and
// ordering context; bundling those values would obscure audit call sites.
#![allow(clippy::too_many_arguments)]

//! Direct v2 conversions through the ordered relay IR.
//!
//! This module is intentionally independent from the legacy conversion
//! module.  A direct route has exactly one source decode/map and one target
//! encode/map.  In particular, Gemini and Claude never use an OpenAI DTO as
//! an intermediate representation here.

use std::{
    collections::{BTreeMap, VecDeque},
    error::Error,
    fmt,
};

use serde::{Deserialize, Serialize};

use super::{
    BillingUsage, CacheUsage, ClaudeContentBlock, ClaudeMediaSource, ClaudeMessage, ClaudeRequest,
    ClaudeResponse, ClaudeTool, ClaudeToolChoice, ClaudeUsage, ConversionPlan,
    ConversionPolicyError, Envelope, Feature, Fidelity, FunctionData, FunctionKind,
    GeminiCandidate, GeminiContent, GeminiFunctionCall, GeminiFunctionCallingConfig,
    GeminiFunctionDeclaration, GeminiFunctionResponse, GeminiGenerationConfig, GeminiInlineData,
    GeminiPart, GeminiRequest, GeminiResponse, GeminiTool, GeminiToolConfig, GeminiUsage,
    GenerationControls, IncompleteDetails, Item, ItemKind, JsonData, Loss, LossCode, LossLedger,
    LossPolicy, Media, MediaKind, OpaqueId, OpaqueIdProvenance, OpaqueProviderState,
    OpaqueStateProvenance, OpenAiAnthropicBlock, OpenAiAnthropicExtraContent,
    OpenAiChatContentPart, OpenAiChatMessage, OpenAiChatRequest, OpenAiChatResponse,
    OpenAiChatTool, OpenAiChoice, OpenAiExtraContent, OpenAiFunction, OpenAiGoogleExtraContent,
    OpenAiResponsesRequest, OpenAiToolCall, Part, PartKind, PolicyOutcome, Protocol, Provenance,
    ReasoningConfig, ResponsesContentPart, ResponsesInput, ResponsesInputItem,
    ResponsesOutputContent, ResponsesOutputItem, ResponsesResponse, ResponsesTool, Role,
    SemanticBillingUsage, SemanticUsage, StringOrParts, TokenDetails, Tool, ToolChoice, ToolKind,
    WireUsage,
};

/// The default gate for candidate v2 direct routes.  The functions in this
/// module are available for offline differential tests, but production route
/// ownership remains with the existing registry until an explicit rollout.
pub const DIRECT_IR_V2_DEFAULT_ENABLED: bool = false;

/// OpenAI Responses direct routes remain disabled until an explicit runtime
/// rollout registers both their request and response adaptors.
pub const OPENAI_RESPONSES_DIRECT_IR_V2_DEFAULT_ENABLED: bool = false;

/// A machine-readable route descriptor for one direct IR candidate route.
///
/// The descriptor is deliberately not a [`crate::relay::RuntimeCatalog`]
/// entry.  It makes the candidate's disabled-by-default state and one-hop
/// shape inspectable without changing runtime registration.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct DirectIrRouteDescriptor {
    /// Source wire protocol.
    pub source: Protocol,
    /// Target wire protocol.
    pub target: Protocol,
    /// Candidate route is disabled until an explicit rollout enables it.
    pub enabled: bool,
    /// Request converter identifier.
    pub request_converter_id: String,
    /// Response converter identifier.
    pub response_converter_id: String,
    /// Direct routes always have one cross-protocol hop.
    pub hop_count: usize,
    /// Candidate route fidelity before input-specific losses.
    pub fidelity: Fidelity,
}

/// Returns the disabled-by-default descriptor for a direct IR route.
#[must_use]
pub fn direct_ir_route_descriptor(source: Protocol, target: Protocol) -> DirectIrRouteDescriptor {
    DirectIrRouteDescriptor {
        source,
        target,
        enabled: DIRECT_IR_V2_DEFAULT_ENABLED,
        request_converter_id: format!(
            "{}-to-{}-ir-v2-request",
            protocol_name(source),
            protocol_name(target)
        ),
        response_converter_id: format!(
            "{}-to-{}-ir-v2-response",
            protocol_name(source),
            protocol_name(target)
        ),
        hop_count: if source == target { 0 } else { 1 },
        fidelity: if source == target {
            Fidelity::Exact
        } else {
            Fidelity::Normalized
        },
    }
}

/// Alias retained for route-oriented callers.
#[must_use]
pub fn direct_ir_route(source: Protocol, target: Protocol) -> DirectIrRouteDescriptor {
    direct_ir_route_descriptor(source, target)
}

/// Returns the disabled-by-default descriptor for an OpenAI Responses route.
///
/// This named seam keeps Responses route discovery explicit while retaining
/// the generic descriptor as the single shape and converter-id generator.
#[must_use]
pub fn openai_responses_direct_ir_route_descriptor(target: Protocol) -> DirectIrRouteDescriptor {
    let mut descriptor = direct_ir_route_descriptor(Protocol::OpenAiResponses, target);
    descriptor.enabled = OPENAI_RESPONSES_DIRECT_IR_V2_DEFAULT_ENABLED;
    descriptor
}

/// Alias used by route-oriented capability consumers.
#[must_use]
pub fn responses_direct_ir_route_descriptor(target: Protocol) -> DirectIrRouteDescriptor {
    openai_responses_direct_ir_route_descriptor(target)
}

/// A direct conversion result with enough information for audit, policy, and
/// offline differential comparison.
#[derive(Clone, Debug, PartialEq)]
pub struct DirectIrConversion<T> {
    /// Encoded target wire value (or the Envelope for decode functions).
    pub value: T,
    /// Ordered intermediate envelope used by the conversion.
    pub envelope: Envelope,
    /// One-hop conversion decision for this source/target pair.
    pub plan: ConversionPlan,
    /// De-duplicated losses, including synthetic-field markers.
    pub losses: LossLedger,
}

impl<T> DirectIrConversion<T> {
    fn new(value: T, envelope: Envelope, plan: ConversionPlan) -> Self {
        let mut losses = envelope.loss_ledger().clone();
        losses.extend(plan.losses.iter().cloned());
        Self {
            value,
            envelope,
            plan,
            losses,
        }
    }

    /// Returns the audit ledger under the conventional name used by relay
    /// policy code.
    #[must_use]
    pub fn loss_ledger(&self) -> &LossLedger {
        &self.losses
    }

    /// Applies the same administrator-selected policy used by compiled relay
    /// plans before a direct conversion is executed.
    pub fn enforce_loss_policy(
        &self,
        policy: LossPolicy,
    ) -> Result<PolicyOutcome, ConversionPolicyError> {
        self.plan.enforce(policy)
    }
}

/// Applies a direct conversion's loss policy without exposing the plan as a
/// mutable escape hatch to route adapters.
pub fn enforce_direct_ir_loss_policy<T>(
    conversion: &DirectIrConversion<T>,
    policy: LossPolicy,
) -> Result<PolicyOutcome, ConversionPolicyError> {
    conversion.enforce_loss_policy(policy)
}

/// Safe, typed failure from a direct source/target conversion.
///
/// The error carries only structural routing information.  Source body text,
/// argument bytes, and provider payloads are intentionally absent from its
/// [`Display`] implementation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct DirectIrError {
    /// Stable error code.
    pub code: String,
    /// Source wire protocol.
    pub source: Protocol,
    /// Target wire protocol.
    pub target: Protocol,
    /// Stable feature name, such as `function_call_id`.
    pub feature: String,
    /// Structural source path, such as `contents[1].parts[0]`.
    pub path: String,
    /// Direct conversion failures are not fixed by retrying unchanged input.
    pub retryable: bool,
    /// A safe machine-readable reason, never raw provider content.
    pub reason: DirectIrReason,
}

/// Stable reasons for a direct conversion failure.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum DirectIrReason {
    /// A required field was absent.
    MissingField,
    /// An opaque identifier was empty.
    EmptyId,
    /// An identifier was duplicated.
    DuplicateId,
    /// A result did not match a prior call.
    Mismatch,
    /// A result had no prior call.
    Orphan,
    /// The source shape is not valid for this protocol.
    InvalidShape,
    /// The source feature cannot be represented by the target.
    Unsupported,
    /// A provider signature or opaque state was malformed.
    InvalidOpaqueState,
    /// A JSON argument/schema field could not be decoded as JSON.
    InvalidJsonField,
}

impl DirectIrError {
    fn new(
        source: Protocol,
        target: Protocol,
        feature: impl Into<String>,
        path: impl Into<String>,
        reason: DirectIrReason,
    ) -> Self {
        Self {
            code: "direct_ir_conversion_error".to_owned(),
            source,
            target,
            feature: feature.into(),
            path: path.into(),
            retryable: false,
            reason,
        }
    }

    fn missing(source: Protocol, target: Protocol, feature: &str, path: &str) -> Self {
        Self::new(source, target, feature, path, DirectIrReason::MissingField)
    }

    fn unsupported(source: Protocol, target: Protocol, feature: &str, path: &str) -> Self {
        Self::new(source, target, feature, path, DirectIrReason::Unsupported)
    }
}

impl fmt::Display for DirectIrError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(
            formatter,
            "{}: {:?} -> {:?}, feature {}, path {}",
            self.code, self.source, self.target, self.feature, self.path
        )
    }
}

impl Error for DirectIrError {}

fn protocol_name(protocol: Protocol) -> &'static str {
    match protocol {
        Protocol::OpenAi => "openai-chat",
        Protocol::OpenAiResponses => "openai-responses",
        Protocol::Claude => "claude",
        Protocol::Gemini => "gemini",
    }
}

fn plan_for(
    source: Protocol,
    target: Protocol,
    model: &str,
    envelope: &Envelope,
) -> ConversionPlan {
    let descriptor = direct_ir_route_descriptor(source, target);
    // A ConversionPlan describes this one direct source→target execution.  It
    // intentionally contains one opaque pair ID rather than advertising both
    // a request and response converter at the same time.
    let mut plan = ConversionPlan {
        source,
        target,
        model_family: model.to_owned(),
        converter_ids: vec![format!(
            "{}-to-{}-ir-v2",
            protocol_name(source),
            protocol_name(target)
        )],
        hop_count: descriptor.hop_count,
        fidelity: descriptor.fidelity,
        unsupported: Vec::new(),
        losses: envelope.loss_ledger().as_slice().to_vec(),
        synthetic: Vec::new(),
    };
    for loss in envelope.loss_ledger().as_slice() {
        match loss.code {
            LossCode::SyntheticToolCallId => {
                plan.add_synthetic(super::SyntheticField::ToolCallId);
            }
            LossCode::SyntheticThoughtSignature => {
                plan.add_synthetic(super::SyntheticField::ThoughtSignature);
            }
            _ => {}
        }
    }
    if !plan.losses.is_empty() {
        plan.fidelity = Fidelity::Lossy;
    }
    plan
}

fn finish<T>(
    value: T,
    envelope: Envelope,
    source: Protocol,
    target: Protocol,
) -> DirectIrConversion<T> {
    let plan = plan_for(source, target, &envelope.model, &envelope);
    DirectIrConversion::new(value, envelope, plan)
}

fn map_validation<T>(
    result: Result<T, super::IrValidationError>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<T, DirectIrError> {
    result.map_err(|error| {
        let (feature, reason) = match error {
            super::IrValidationError::MissingToolCallId { .. } => {
                ("function_call_id", DirectIrReason::MissingField)
            }
            super::IrValidationError::OrphanToolResult { .. } => {
                ("function_call_id", DirectIrReason::Orphan)
            }
            super::IrValidationError::DuplicateToolResult { .. }
            | super::IrValidationError::DuplicateToolCallId { .. } => {
                ("function_call_id", DirectIrReason::DuplicateId)
            }
            super::IrValidationError::EmptyOpaqueId { .. } => {
                ("function_call_id", DirectIrReason::EmptyId)
            }
            super::IrValidationError::EmptyStateId { .. } => {
                ("state_reference", DirectIrReason::EmptyId)
            }
            super::IrValidationError::InvalidPartShape { .. }
            | super::IrValidationError::InvalidFunctionShape { .. }
            | super::IrValidationError::InvalidItemShape { .. }
            | super::IrValidationError::InvalidOpaqueState { .. } => {
                ("ordered_item", DirectIrReason::InvalidShape)
            }
        };
        DirectIrError::new(source, target, feature, path, reason)
    })
}

fn push_item(
    envelope: &mut Envelope,
    item: Item,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    map_validation(envelope.push_item(item), source, target, path)
}

fn record_loss(
    envelope: &mut Envelope,
    code: LossCode,
    feature: Option<Feature>,
    path: &str,
    message: &str,
) {
    envelope.record_loss(Loss::new(code, feature).at(path).with_message(message));
}

fn role_from_wire(
    role: Option<&str>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Role, DirectIrError> {
    match role {
        None => Ok(Role::User),
        Some("system") => Ok(Role::System),
        Some("developer") => Ok(Role::Developer),
        Some("user") => Ok(Role::User),
        Some("assistant") => Ok(Role::Assistant),
        Some("tool") | Some("function") => Ok(Role::Tool),
        Some("model") => Ok(Role::Model),
        Some(_) => Err(DirectIrError::unsupported(
            source,
            target,
            "message_role",
            path,
        )),
    }
}

fn chat_role(role: Role) -> &'static str {
    match role {
        Role::System => "system",
        Role::Developer => "developer",
        Role::User => "user",
        Role::Assistant | Role::Model => "assistant",
        Role::Tool => "tool",
    }
}

fn provider_state(
    provider: &str,
    kind: &str,
    raw: JsonData,
    provenance: OpaqueStateProvenance,
    model: Option<String>,
) -> OpaqueProviderState {
    OpaqueProviderState {
        provider: provider.to_owned(),
        kind: kind.to_owned(),
        raw,
        provenance,
        model,
    }
}

fn object(entries: impl IntoIterator<Item = (String, JsonData)>) -> JsonData {
    JsonData::Object(entries.into_iter().collect())
}

fn insert_extra_fields(
    target: &mut BTreeMap<String, JsonData>,
    extra: &BTreeMap<String, JsonData>,
    known: &[&str],
) {
    for (key, value) in extra {
        if !known.contains(&key.as_str()) {
            target.entry(key.clone()).or_insert_with(|| value.clone());
        }
    }
}

fn extensions_with_prefix(
    extensions: &BTreeMap<String, JsonData>,
    prefix: &str,
) -> BTreeMap<String, JsonData> {
    extensions
        .iter()
        .filter_map(|(key, value)| {
            key.strip_prefix(prefix)
                .map(|suffix| (suffix.to_owned(), value.clone()))
        })
        .collect()
}

fn json_string(value: &JsonData) -> Option<&str> {
    match value {
        JsonData::String(value) => Some(value),
        _ => None,
    }
}

fn json_object(value: &JsonData) -> Option<&BTreeMap<String, JsonData>> {
    match value {
        JsonData::Object(value) => Some(value),
        _ => None,
    }
}

fn parse_json_field(
    text: &str,
    source: Protocol,
    target: Protocol,
    feature: &str,
    path: &str,
) -> Result<JsonData, DirectIrError> {
    let source_text = if text.trim().is_empty() { "{}" } else { text };
    serde_json::from_str(source_text).map_err(|_| {
        DirectIrError::new(
            source,
            target,
            feature,
            path,
            DirectIrReason::InvalidJsonField,
        )
    })
}

fn compact_json(value: &JsonData) -> Result<String, DirectIrError> {
    value.compact_string().map_err(|_| {
        DirectIrError::new(
            Protocol::OpenAi,
            Protocol::OpenAi,
            "json_field",
            "envelope",
            DirectIrReason::InvalidJsonField,
        )
    })
}

fn empty_extensions() -> BTreeMap<String, JsonData> {
    BTreeMap::new()
}

fn authentic_id(
    value: Option<String>,
    source: Protocol,
    target: Protocol,
    feature: &str,
    path: &str,
) -> Result<Option<OpaqueId>, DirectIrError> {
    match value {
        None => Ok(None),
        Some(value) if value.is_empty() => Err(DirectIrError::new(
            source,
            target,
            feature,
            path,
            DirectIrReason::EmptyId,
        )),
        Some(value) => Ok(Some(OpaqueId::authentic(value, source))),
    }
}

fn synthetic_gemini_id(kind: &str, index: usize, part_index: usize, name: &str) -> String {
    format!("gemini_call_synthetic_{kind}_{index}_{part_index}_{name}")
}

fn id_value(id: &OpaqueId) -> Result<String, DirectIrError> {
    if id.is_empty() {
        return Err(DirectIrError::new(
            id.source(),
            id.source(),
            "function_call_id",
            "envelope.items[].call_id",
            DirectIrReason::EmptyId,
        ));
    }
    Ok(id.value.clone())
}

fn function_call_part(name: String, arguments: JsonData) -> Part {
    Part::function(FunctionData {
        kind: FunctionKind::Call,
        name: Some(name),
        arguments: Some(arguments),
        output: None,
        extensions: empty_extensions(),
    })
}

fn function_result_part(name: Option<String>, output: JsonData) -> Part {
    Part::function(FunctionData {
        kind: FunctionKind::Result,
        name,
        arguments: None,
        output: Some(output),
        extensions: empty_extensions(),
    })
}

fn text_item(role: Role, text: String, source: Protocol, path: &str) -> Item {
    let mut item = Item::new(ItemKind::Message, role, Provenance::new(source));
    item.provenance.source_path = Some(path.to_owned());
    item.push_part(Part::text(text));
    item
}

fn single_item_part<'a>(
    item: &'a Item,
    source: Protocol,
    target: Protocol,
    path: &str,
    feature: &str,
) -> Result<&'a Part, DirectIrError> {
    if item.ordered_parts().len() != 1 {
        return Err(DirectIrError::new(
            source,
            target,
            feature,
            path,
            DirectIrReason::InvalidShape,
        ));
    }
    item.ordered_parts()
        .first()
        .ok_or_else(|| DirectIrError::missing(source, target, feature, path))
}

fn attach_openai_name(item: &mut Item, name: Option<&str>) {
    if let Some(name) = name {
        item.provenance
            .extensions
            .insert("openai.name".to_owned(), JsonData::String(name.to_owned()));
    }
}

fn attach_anthropic_model(item: &mut Item, model: Option<&str>) {
    if let Some(model) = model {
        item.provenance.extensions.insert(
            "anthropic.model".to_owned(),
            JsonData::String(model.to_owned()),
        );
    }
}

fn validate_anthropic_block_shape(
    block: &OpenAiAnthropicBlock,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    let invalid_field = match block.kind.as_str() {
        "text" => {
            if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.synthetic.is_some() {
                Some("synthetic")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.image_url.is_some() {
                Some("image_url")
            } else {
                None
            }
        }
        "thinking" => {
            if block.text.is_some() {
                Some("text")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.image_url.is_some() {
                Some("image_url")
            } else {
                None
            }
        }
        "redacted_thinking" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.image_url.is_some() {
                Some("image_url")
            } else if block.data.is_some() && block.content.is_some() {
                Some("content")
            } else {
                None
            }
        }
        "tool_use" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.synthetic.is_some() {
                Some("synthetic")
            } else if block.data.is_some() {
                Some("data")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.image_url.is_some() {
                Some("image_url")
            } else {
                None
            }
        }
        "tool_result" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.synthetic.is_some() {
                Some("synthetic")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.input.is_some() {
                Some("input")
            } else if block.image_url.is_some() {
                Some("image_url")
            } else {
                None
            }
        }
        "image" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.synthetic.is_some() {
                Some("synthetic")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else {
                None
            }
        }
        _ => None,
    };
    if let Some(field) = invalid_field {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "anthropic_block_field",
            &format!("{path}.{field}"),
        ));
    }
    Ok(())
}

fn required_claude_thinking_signature(
    signature: Option<&str>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<String, DirectIrError> {
    let Some(signature) = signature else {
        return Err(DirectIrError::missing(
            source,
            target,
            "thinking_signature",
            path,
        ));
    };
    if signature.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "thinking_signature",
            path,
            DirectIrReason::EmptyId,
        ));
    }
    Ok(signature.to_owned())
}

fn opaque_item(role: Role, state: OpaqueProviderState, source: Protocol, path: &str) -> Item {
    let mut item = Item::new(ItemKind::Reasoning, role, Provenance::new(source));
    item.provenance.source_path = Some(path.to_owned());
    item.push_part(Part::opaque(state));
    item
}

fn media_item(role: Role, media: Media, source: Protocol, path: &str) -> Item {
    let mut item = Item::new(ItemKind::Message, role, Provenance::new(source));
    item.provenance.source_path = Some(path.to_owned());
    item.push_part(Part::media(media));
    item
}

fn tool_call_item(
    call_id: OpaqueId,
    name: String,
    arguments: JsonData,
    source: Protocol,
    path: &str,
) -> Item {
    let mut item = Item::tool_call(
        call_id,
        vec![function_call_part(name, arguments)],
        Provenance::new(source),
    );
    item.provenance.source_path = Some(path.to_owned());
    item
}

fn tool_result_item(
    call_id: OpaqueId,
    name: Option<String>,
    output: JsonData,
    source: Protocol,
    path: &str,
) -> Item {
    let mut item = Item::tool_result(
        call_id,
        vec![function_result_part(name, output)],
        Provenance::new(source),
    );
    item.provenance.source_path = Some(path.to_owned());
    item
}

fn chat_content_to_parts(
    content: Option<StringOrParts<OpenAiChatContentPart>>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Vec<Part>, DirectIrError> {
    let Some(content) = content else {
        return Ok(Vec::new());
    };
    match content {
        StringOrParts::String(text) => Ok(vec![Part::text(text)]),
        StringOrParts::Parts(parts) => parts
            .into_iter()
            .enumerate()
            .map(|(index, part)| {
                let part_path = format!("{path}[{index}]");
                match part.kind.as_str() {
                    "text" => {
                        if part.image_url.is_some() {
                            return Err(DirectIrError::unsupported(
                                source,
                                target,
                                "content_part.image_url",
                                &format!("{part_path}.image_url"),
                            ));
                        }
                        part.text.map(Part::text).ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "text",
                                &format!("{part_path}.text"),
                            )
                        })
                    }
                    "image_url" => {
                        if part.text.is_some() {
                            return Err(DirectIrError::unsupported(
                                source,
                                target,
                                "content_part.text",
                                &format!("{part_path}.text"),
                            ));
                        }
                        let image = part.image_url.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "image",
                                &format!("{part_path}.image_url"),
                            )
                        })?;
                        Ok(Part::media(chat_image_url_to_media(
                            image,
                            source,
                            target,
                            &format!("{part_path}.image_url"),
                        )?))
                    }
                    _ => Err(DirectIrError::unsupported(
                        source,
                        target,
                        "content_part",
                        &part_path,
                    )),
                }
            })
            .collect(),
    }
}

fn chat_image_url_to_media(
    image: JsonData,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Media, DirectIrError> {
    let mut media = Media::new(MediaKind::Image);
    match image {
        JsonData::String(uri) => {
            media.uri = Some(uri);
        }
        JsonData::Object(mut object) => {
            let Some(url) = object.remove("url") else {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "image_url.url",
                    &format!("{path}.url"),
                ));
            };
            let JsonData::String(url) = url else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "image_url.url",
                    format!("{path}.url"),
                    DirectIrReason::InvalidShape,
                ));
            };
            media.uri = Some(url);
            media.extensions = object;
        }
        _ => {
            return Err(DirectIrError::new(
                source,
                target,
                "image_url",
                path,
                DirectIrReason::InvalidShape,
            ));
        }
    }
    Ok(media)
}

fn chat_content_to_json(
    content: Option<StringOrParts<OpenAiChatContentPart>>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<JsonData, DirectIrError> {
    let Some(content) = content else {
        return Ok(JsonData::String(String::new()));
    };
    match content {
        StringOrParts::String(value) => Ok(JsonData::String(value)),
        StringOrParts::Parts(parts) => {
            let values = parts
                .into_iter()
                .enumerate()
                .map(|(index, part)| {
                    let part_path = format!("{path}[{index}]");
                    let mut entries = BTreeMap::new();
                    entries.insert("type".to_owned(), JsonData::String(part.kind));
                    match entries
                        .get("type")
                        .and_then(json_string)
                        .unwrap_or_default()
                    {
                        "text" => {
                            if part.image_url.is_some() {
                                return Err(DirectIrError::unsupported(
                                    source,
                                    target,
                                    "content_part.image_url",
                                    &format!("{part_path}.image_url"),
                                ));
                            }
                            let Some(text) = part.text else {
                                return Err(DirectIrError::missing(
                                    source,
                                    target,
                                    "text",
                                    &format!("{part_path}.text"),
                                ));
                            };
                            entries.insert("text".to_owned(), JsonData::String(text));
                        }
                        "image_url" => {
                            if part.text.is_some() {
                                return Err(DirectIrError::unsupported(
                                    source,
                                    target,
                                    "content_part.text",
                                    &format!("{part_path}.text"),
                                ));
                            }
                            let Some(image) = part.image_url else {
                                return Err(DirectIrError::missing(
                                    source,
                                    target,
                                    "image",
                                    &format!("{part_path}.image_url"),
                                ));
                            };
                            let _ = chat_image_url_to_media(
                                image.clone(),
                                source,
                                target,
                                &format!("{part_path}.image_url"),
                            )?;
                            entries.insert("image_url".to_owned(), image);
                        }
                        _ => {
                            return Err(DirectIrError::unsupported(
                                source,
                                target,
                                "content_part",
                                &format!("{part_path}.type"),
                            ));
                        }
                    }
                    if entries.len() == 1 {
                        Err(DirectIrError::missing(
                            source,
                            target,
                            "content_part",
                            &part_path,
                        ))
                    } else {
                        Ok(JsonData::Object(entries))
                    }
                })
                .collect::<Result<Vec<_>, _>>()?;
            Ok(JsonData::Array(values))
        }
    }
}

fn chat_parts_to_content(
    parts: &[Part],
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Option<StringOrParts<OpenAiChatContentPart>>, DirectIrError> {
    if parts.is_empty() {
        return Ok(None);
    }
    if parts
        .iter()
        .all(|part| matches!(&part.kind, PartKind::Text))
    {
        if parts.len() > 1 {
            let output = parts
                .iter()
                .map(|part| {
                    let Some(value) = part.text.as_ref() else {
                        return Err(DirectIrError::new(
                            source,
                            target,
                            "text",
                            path,
                            DirectIrReason::InvalidShape,
                        ));
                    };
                    Ok(OpenAiChatContentPart {
                        kind: "text".to_owned(),
                        text: Some(value.clone()),
                        image_url: None,
                    })
                })
                .collect::<Result<Vec<_>, _>>()?;
            return Ok(Some(StringOrParts::Parts(output)));
        }
        let mut text = String::new();
        for part in parts {
            let Some(value) = part.text.as_ref() else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "text",
                    path,
                    DirectIrReason::InvalidShape,
                ));
            };
            text.push_str(value);
        }
        return Ok(Some(StringOrParts::String(text)));
    }
    let mut output = Vec::new();
    for (index, part) in parts.iter().enumerate() {
        match &part.kind {
            PartKind::Text => output.push(OpenAiChatContentPart {
                kind: "text".to_owned(),
                text: part.text.clone(),
                image_url: None,
            }),
            PartKind::Media => {
                let Some(media) = part.media.as_ref() else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "media",
                        format!("{path}[{index}]"),
                        DirectIrReason::InvalidShape,
                    ));
                };
                if !matches!(&media.kind, MediaKind::Image) {
                    return Err(DirectIrError::unsupported(
                        source,
                        target,
                        "media",
                        &format!("{path}[{index}]"),
                    ));
                }
                let image_url = if let Some(uri) = media.uri.as_ref() {
                    if !media.extensions.is_empty() {
                        let mut value = media.extensions.clone();
                        value.insert("url".to_owned(), JsonData::String(uri.clone()));
                        JsonData::Object(value)
                    } else {
                        JsonData::String(uri.clone())
                    }
                } else if let Some(data) = media.data.as_ref() {
                    data.clone()
                } else {
                    return Err(DirectIrError::missing(
                        source,
                        target,
                        "image_url",
                        &format!("{path}[{index}]"),
                    ));
                };
                output.push(OpenAiChatContentPart {
                    kind: "image_url".to_owned(),
                    text: None,
                    image_url: Some(image_url),
                });
            }
            PartKind::Unknown(_) | PartKind::Opaque | PartKind::Function => {
                return Err(DirectIrError::unsupported(
                    source,
                    target,
                    "content_part",
                    &format!("{path}[{index}]"),
                ));
            }
        }
    }
    Ok(Some(StringOrParts::Parts(output)))
}

fn tool_choice_from_json(
    choice: Option<JsonData>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<ToolChoice, DirectIrError> {
    let Some(choice) = choice else {
        return Ok(ToolChoice::Auto);
    };
    match choice {
        JsonData::String(value) => match value.as_str() {
            "auto" => Ok(ToolChoice::Auto),
            "none" => Ok(ToolChoice::None),
            "required" | "any" => Ok(ToolChoice::Required),
            _ => Err(DirectIrError::unsupported(
                source,
                target,
                "tool_choice",
                path,
            )),
        },
        JsonData::Object(object) => {
            let name = object.get("name").and_then(json_string).or_else(|| {
                object
                    .get("function")
                    .and_then(json_object)
                    .and_then(|value| value.get("name").and_then(json_string))
            });
            name.map(|name| ToolChoice::Named {
                name: name.to_owned(),
            })
            .ok_or_else(|| DirectIrError::missing(source, target, "tool_choice.name", path))
        }
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "tool_choice",
            path,
        )),
    }
}

fn responses_tool_choice_from_json(
    choice: Option<JsonData>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<ToolChoice, DirectIrError> {
    let Some(choice) = choice else {
        return Ok(ToolChoice::Auto);
    };
    match choice {
        JsonData::String(value) => match value.as_str() {
            "auto" => Ok(ToolChoice::Auto),
            "none" => Ok(ToolChoice::None),
            "required" => Ok(ToolChoice::Required),
            _ => Err(DirectIrError::unsupported(
                source,
                target,
                "tool_choice",
                path,
            )),
        },
        JsonData::Object(object) => {
            if let Some(field) = object
                .keys()
                .find(|field| field.as_str() != "type" && field.as_str() != "name")
            {
                return Err(responses_unknown_field(
                    source,
                    target,
                    format!("{path}.{field}"),
                ));
            }
            match object.get("type").and_then(json_string) {
                Some("function") => {}
                Some(_) => {
                    return Err(responses_option_error(
                        source,
                        target,
                        "tool_choice.type",
                        path,
                    ));
                }
                None => {
                    return Err(DirectIrError::missing(
                        source,
                        target,
                        "tool_choice.type",
                        path,
                    ));
                }
            }
            let name = object
                .get("name")
                .and_then(json_string)
                .filter(|name| !name.trim().is_empty())
                .ok_or_else(|| DirectIrError::missing(source, target, "tool_choice.name", path))?;
            Ok(ToolChoice::Named {
                name: name.to_owned(),
            })
        }
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "tool_choice",
            path,
        )),
    }
}

fn responses_tool_choice_to_json(
    choice: &ToolChoice,
    source: Protocol,
    target: Protocol,
) -> Result<JsonData, DirectIrError> {
    let value = match choice {
        ToolChoice::Auto => JsonData::String("auto".to_owned()),
        ToolChoice::None => JsonData::String("none".to_owned()),
        ToolChoice::Required => JsonData::String("required".to_owned()),
        ToolChoice::Named { name } => object([
            ("type".to_owned(), JsonData::String("function".to_owned())),
            ("name".to_owned(), JsonData::String(name.clone())),
        ]),
        ToolChoice::Provider { raw } => raw.clone(),
    };
    if matches!(choice, ToolChoice::Provider { .. }) {
        responses_tool_choice_from_json(Some(value.clone()), source, target, "tool_choice")?;
    }
    Ok(value)
}

fn tool_choice_to_json(choice: &ToolChoice) -> JsonData {
    match choice {
        ToolChoice::Auto => JsonData::String("auto".to_owned()),
        ToolChoice::None => JsonData::String("none".to_owned()),
        ToolChoice::Required => JsonData::String("required".to_owned()),
        ToolChoice::Named { name } => object([
            ("type".to_owned(), JsonData::String("function".to_owned())),
            (
                "function".to_owned(),
                object([("name".to_owned(), JsonData::String(name.clone()))]),
            ),
        ]),
        ToolChoice::Provider { raw } => raw.clone(),
    }
}

fn tool_to_ir(
    kind: String,
    name: String,
    description: Option<String>,
    schema: Option<JsonData>,
    strict: Option<bool>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Tool, DirectIrError> {
    let kind = match kind.as_str() {
        "function" => ToolKind::Function,
        "builtin" => ToolKind::Builtin,
        "mcp" => ToolKind::Mcp,
        "custom" => ToolKind::Custom,
        _ => {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_type",
                path,
            ));
        }
    };
    if name.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "tool.name",
            format!("{path}.name"),
            DirectIrReason::EmptyId,
        ));
    }
    let mut extensions = empty_extensions();
    if let Some(strict) = strict {
        extensions.insert("openai.strict".to_owned(), JsonData::Bool(strict));
    }
    Ok(Tool {
        kind,
        name: Some(name),
        description,
        input_schema: schema,
        extensions,
    })
}

fn validate_openai_tool_call(
    call: &OpenAiToolCall,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    if call.kind != "function" {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "tool_call_type",
            &format!("{path}.kind"),
        ));
    }
    if call.function.description.is_some() {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "tool_call.description",
            &format!("{path}.function.description"),
        ));
    }
    if call.function.parameters.is_some() {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "tool_call.parameters",
            &format!("{path}.function.parameters"),
        ));
    }
    if call.function.strict.is_some() {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "tool_call.strict",
            &format!("{path}.function.strict"),
        ));
    }
    Ok(())
}

fn tool_name_for_target(
    tool: &Tool,
    index: usize,
    source: Protocol,
    target: Protocol,
) -> Result<String, DirectIrError> {
    let path = format!("tools[{index}]");
    if !matches!(&tool.kind, ToolKind::Function) {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "tool_type",
            &path,
        ));
    }
    let name = tool
        .name
        .clone()
        .ok_or_else(|| DirectIrError::missing(source, target, "tool.name", &path))?;
    if name.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "tool.name",
            format!("{path}.name"),
            DirectIrReason::EmptyId,
        ));
    }
    Ok(name)
}

fn openai_strict_for_tool(
    tool: &Tool,
    source: Protocol,
    target: Protocol,
    index: usize,
) -> Result<Option<bool>, DirectIrError> {
    let Some(value) = tool.extensions.get("openai.strict") else {
        return Ok(None);
    };
    match value {
        JsonData::Bool(value) => Ok(Some(*value)),
        _ => Err(DirectIrError::new(
            source,
            target,
            "tool.strict",
            format!("tools[{index}].strict"),
            DirectIrReason::InvalidShape,
        )),
    }
}

fn openai_request_controls(request: &OpenAiChatRequest, envelope: &mut Envelope) {
    envelope.controls = GenerationControls {
        max_output_tokens: request.max_completion_tokens.or(request.max_tokens),
        temperature: request.temperature,
        top_p: request.top_p,
        stream: request.stream,
        reasoning_effort: request.reasoning_effort.clone(),
        response_format: request.response_format.clone(),
        parallel_tool_calls: request.parallel_tool_calls,
        extensions: BTreeMap::new(),
    };
    if request.max_tokens.is_some() {
        envelope.extensions.insert(
            "openai.max_tokens".to_owned(),
            JsonData::Number(serde_json::Number::from(request.max_tokens.unwrap_or(0))),
        );
    }
    if request.max_completion_tokens.is_some() {
        envelope.extensions.insert(
            "openai.max_completion_tokens".to_owned(),
            JsonData::Number(serde_json::Number::from(
                request.max_completion_tokens.unwrap_or(0),
            )),
        );
    }
    let extensions = [
        ("user", request.user.clone()),
        ("store", request.store.clone()),
        ("metadata", request.metadata.clone()),
        ("stream_options", request.stream_options.clone()),
        (
            "top_logprobs",
            request
                .top_logprobs
                .map(|value| JsonData::Number(value.into())),
        ),
        ("safety_identifier", request.safety_identifier.clone()),
        (
            "prompt_cache_retention",
            request.prompt_cache_retention.clone(),
        ),
        (
            "prompt_cache_key",
            request.prompt_cache_key.clone().map(JsonData::String),
        ),
        (
            "service_tier",
            request.service_tier.clone().map(JsonData::String),
        ),
        ("enable_thinking", request.enable_thinking.clone()),
        ("thinking_budget", request.thinking_budget.clone()),
        ("n", request.n.map(|value| JsonData::Number(value.into()))),
    ];
    for (name, value) in extensions {
        if let Some(value) = value {
            envelope.controls.extensions.insert(name.to_owned(), value);
        }
    }
}

fn record_request_projection_losses(envelope: &mut Envelope, target: Protocol) {
    if target != Protocol::OpenAi {
        let conflicting_limits = match (
            envelope.extensions.get("openai.max_tokens"),
            envelope.extensions.get("openai.max_completion_tokens"),
        ) {
            (Some(max_tokens), Some(max_completion_tokens)) => max_tokens != max_completion_tokens,
            _ => false,
        };
        if conflicting_limits {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "openai.max_tokens",
                "OpenAI max_tokens and max_completion_tokens conflict; one target limit is selected",
            );
        }
    }
    if target != Protocol::OpenAi {
        let strict_tools = envelope
            .tools
            .iter()
            .enumerate()
            .filter(|(_, tool)| tool.extensions.contains_key("openai.strict"))
            .map(|(index, _)| index)
            .collect::<Vec<_>>();
        for index in strict_tools {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                format!("tools[{index}].strict").as_str(),
                "OpenAI strict tool schema has no equivalent target field",
            );
        }
    }
    if matches!(target, Protocol::Claude | Protocol::Gemini) {
        if envelope.controls.top_p.is_some() {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "controls.top_p",
                "target request has no top_p control; the source value remains in IR",
            );
        }
        if envelope.controls.reasoning_effort.is_some() {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::ReasoningSummary),
                "controls.reasoning_effort",
                "target request has no reasoning_effort control; the source value remains in IR",
            );
        }
        if envelope.controls.response_format.is_some() {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "controls.response_format",
                "target request has no response_format control; the source value remains in IR",
            );
        }
        if envelope.controls.parallel_tool_calls.is_some() {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::ParallelFunctionCall),
                "controls.parallel_tool_calls",
                "target request has no parallel_tool_calls control; the source value remains in IR",
            );
        }
    }
    if target != Protocol::Gemini && envelope.extensions.contains_key("gemini.safety_settings") {
        record_loss(
            envelope,
            LossCode::LossSafetyMetadata,
            Some(Feature::SafetyMetadata),
            "gemini.safety_settings",
            "Gemini safety settings are retained in IR but not expressible on target request",
        );
    }
    let unsupported_controls = envelope
        .controls
        .extensions
        .keys()
        .filter(|key| match target {
            Protocol::OpenAi => false,
            Protocol::Claude => matches!(
                key.as_str(),
                "user"
                    | "store"
                    | "metadata"
                    | "stream_options"
                    | "top_logprobs"
                    | "safety_identifier"
                    | "prompt_cache_retention"
                    | "prompt_cache_key"
                    | "service_tier"
                    | "enable_thinking"
                    | "thinking_budget"
                    | "n"
            ),
            Protocol::Gemini => matches!(
                key.as_str(),
                "user"
                    | "store"
                    | "metadata"
                    | "stream_options"
                    | "top_logprobs"
                    | "safety_identifier"
                    | "prompt_cache_retention"
                    | "prompt_cache_key"
                    | "service_tier"
                    | "enable_thinking"
                    | "thinking_budget"
                    | "n"
            ),
            Protocol::OpenAiResponses => true,
        })
        .cloned()
        .collect::<Vec<_>>();
    for key in unsupported_controls {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            format!("controls.extensions.{key}").as_str(),
            "request control retained in IR but not expressible on target",
        );
    }
}

fn record_response_projection_losses(envelope: &mut Envelope, target: Protocol) {
    let has_responses_id = envelope.extensions.contains_key("openai.responses.id");
    let has_responses_created = envelope
        .extensions
        .contains_key("openai.responses.created_at");
    if (target == Protocol::Gemini && (has_responses_id || has_responses_created))
        || (target == Protocol::Claude && has_responses_created)
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "openai.responses.metadata",
            "Responses id or creation timestamp is not fully expressible on target response",
        );
    }
    if target != Protocol::OpenAiResponses
        && extension_string(envelope, "openai.responses.status").as_deref() == Some("incomplete")
        && extension_string(envelope, "openai.responses.incomplete_reason").is_some_and(|reason| {
            !matches!(reason.as_str(), "content_filter" | "max_output_tokens")
        })
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "incomplete_details.reason",
            "unknown Responses incomplete reason is not expressible on target response",
        );
    }
    if target != Protocol::Gemini
        && envelope
            .extensions
            .keys()
            .any(|key| key.starts_with("gemini.candidate[") && key.ends_with(".index"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "gemini.candidate[].index",
            "Gemini candidate indexes are retained in IR but not expressible on target response",
        );
    }
    if target != Protocol::OpenAi
        && envelope
            .extensions
            .keys()
            .any(|key| key.starts_with("openai.choice[") && key.ends_with(".index"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "openai.choice[].index",
            "OpenAI choice indexes are retained in IR but not expressible on target response",
        );
    }
    let has_unrepresented_gemini_safety = envelope.extensions.keys().any(|key| {
        key.starts_with("gemini.candidate[")
            && key.ends_with(".safety_ratings")
            && (target != Protocol::Gemini || !key.starts_with("gemini.candidate[0]."))
    });
    if has_unrepresented_gemini_safety {
        record_loss(
            envelope,
            LossCode::LossSafetyMetadata,
            Some(Feature::SafetyMetadata),
            "gemini.candidate[].safetyRatings",
            "Gemini safety ratings are retained in IR but not expressible on target response",
        );
    }
    if target != Protocol::Claude
        && (envelope.extensions.contains_key("claude.response_id")
            || envelope.extensions.contains_key("claude.response_type"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "claude.response_metadata",
            "Claude response id/type are retained in IR but not expressible on target response",
        );
    }
    if target != Protocol::OpenAi
        && (envelope.extensions.contains_key("openai.response_id")
            || envelope.extensions.contains_key("openai.response_object")
            || envelope.extensions.contains_key("openai.response_created"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "openai.response_metadata",
            "OpenAI response metadata is retained in IR but not fully expressible on target",
        );
    }
}

fn record_item_projection_losses(envelope: &mut Envelope, target: Protocol) {
    if envelope.source == target {
        return;
    }
    let metadata = envelope
        .ordered_items()
        .iter()
        .enumerate()
        .flat_map(|(index, item)| {
            let mut paths = Vec::new();
            if item.id.is_some() {
                paths.push(format!("items[{index}].id"));
            }
            if item.raw.is_some() {
                paths.push(format!("items[{index}].raw"));
            }
            if !item.extensions.is_empty() {
                paths.push(format!("items[{index}].extensions"));
            }
            paths
        })
        .collect::<Vec<_>>();
    for path in metadata {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            &path,
            "item metadata is retained in IR but not expressible on target wire",
        );
    }
}

fn chat_extra_google_state(
    extra: Option<OpenAiExtraContent>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Option<OpaqueProviderState>, DirectIrError> {
    let Some(extra) = extra else {
        return Ok(None);
    };
    let Some(google) = extra.google else {
        return Ok(None);
    };
    let Some(signature) = google.thought_signature else {
        return Err(DirectIrError::missing(
            source,
            target,
            "thought_signature",
            path,
        ));
    };
    if signature.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::EmptyId,
        ));
    }
    Ok(Some(provider_state(
        "google",
        "thought_signature",
        JsonData::String(signature),
        if google.synthetic.unwrap_or(false) {
            OpaqueStateProvenance::Synthetic
        } else {
            OpaqueStateProvenance::Authentic
        },
        None,
    )))
}

fn chat_anthropic_blocks_to_items(
    blocks: Vec<OpenAiAnthropicBlock>,
    role: Role,
    source: Protocol,
    target: Protocol,
    path: &str,
    message_name: Option<&str>,
    model: Option<&str>,
    envelope: &mut Envelope,
) -> Result<(), DirectIrError> {
    for (index, block) in blocks.into_iter().enumerate() {
        let block_path = format!("{path}[{index}]");
        validate_anthropic_block_shape(&block, source, target, &block_path)?;
        match block.kind.as_str() {
            "text" => {
                let text = block
                    .text
                    .ok_or_else(|| DirectIrError::missing(source, target, "text", &block_path))?;
                let mut part = Part::text(text);
                part.extensions = block.extra;
                let mut item = Item::new(ItemKind::Message, role.clone(), Provenance::new(source));
                item.provenance.source_path = Some(block_path.clone());
                item.push_part(part);
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            "thinking" => {
                let thinking = block.thinking.ok_or_else(|| {
                    DirectIrError::missing(source, target, "thinking", &block_path)
                })?;
                let signature = required_claude_thinking_signature(
                    block.signature.as_deref(),
                    source,
                    target,
                    &format!("{block_path}.signature"),
                )?;
                let raw = object([
                    ("type".to_owned(), JsonData::String("thinking".to_owned())),
                    ("thinking".to_owned(), JsonData::String(thinking)),
                    ("signature".to_owned(), JsonData::String(signature)),
                    ("extra".to_owned(), JsonData::Object(block.extra)),
                ]);
                let state = provider_state(
                    "anthropic",
                    "thinking",
                    raw,
                    if block.synthetic.unwrap_or(false) {
                        OpaqueStateProvenance::Synthetic
                    } else {
                        OpaqueStateProvenance::Authentic
                    },
                    model.map(str::to_owned),
                );
                let mut item = opaque_item(role.clone(), state, source, &block_path);
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            "redacted_thinking" => {
                let data = block.data.or(block.content).ok_or_else(|| {
                    DirectIrError::missing(source, target, "redacted_thinking.data", &block_path)
                })?;
                let state = provider_state(
                    "anthropic",
                    "redacted_thinking",
                    object([
                        (
                            "type".to_owned(),
                            JsonData::String("redacted_thinking".to_owned()),
                        ),
                        ("data".to_owned(), data),
                        ("extra".to_owned(), JsonData::Object(block.extra)),
                    ]),
                    if block.synthetic.unwrap_or(false) {
                        OpaqueStateProvenance::Synthetic
                    } else {
                        OpaqueStateProvenance::Authentic
                    },
                    model.map(str::to_owned),
                );
                let mut item = opaque_item(role.clone(), state, source, &block_path);
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            "tool_use" => {
                let id = block.id.ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_use.id", &block_path)
                })?;
                let call_id = authentic_id(
                    Some(id),
                    source,
                    target,
                    "function_call_id",
                    &format!("{block_path}.id"),
                )?
                .ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_use.id", &block_path)
                })?;
                let name = block.name.ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_use.name", &block_path)
                })?;
                let input = block.input.ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_use.input", &block_path)
                })?;
                let mut function = FunctionData::call(name, input);
                function.extensions = block.extra;
                let mut item = Item::tool_call(
                    call_id,
                    vec![Part::function(function)],
                    Provenance::new(source),
                );
                item.provenance.source_path = Some(block_path.clone());
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            "tool_result" => {
                let id = block.tool_use_id.ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_result.tool_use_id", &block_path)
                })?;
                let call_id = authentic_id(
                    Some(id),
                    source,
                    target,
                    "function_call_id",
                    &format!("{block_path}.tool_use_id"),
                )?
                .ok_or_else(|| {
                    DirectIrError::missing(source, target, "tool_result.tool_use_id", &block_path)
                })?;
                let mut function =
                    FunctionData::result(block.name, block.content.unwrap_or(JsonData::Null));
                function.extensions = block.extra;
                let mut item = Item::tool_result(
                    call_id,
                    vec![Part::function(function)],
                    Provenance::new(source),
                );
                item.provenance.source_path = Some(block_path.clone());
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            "image" => {
                let image = block.image_url.ok_or_else(|| {
                    DirectIrError::missing(source, target, "image.image_url", &block_path)
                })?;
                let (uri, data) = match image {
                    JsonData::String(uri) => (Some(uri), None),
                    value => (None, Some(value)),
                };
                let mut media = Media::new(MediaKind::Image);
                media.uri = uri;
                media.data = data;
                media.extensions = block.extra;
                let mut item = media_item(role.clone(), media, source, &block_path);
                attach_openai_name(&mut item, message_name);
                attach_anthropic_model(&mut item, model);
                push_item(envelope, item, source, target, &block_path)?;
            }
            _ => {
                return Err(DirectIrError::unsupported(
                    source,
                    target,
                    "anthropic_block",
                    &block_path,
                ));
            }
        }
    }
    Ok(())
}

fn chat_message_to_envelope(
    message: OpenAiChatMessage,
    index: usize,
    envelope: &mut Envelope,
    source: Protocol,
    target: Protocol,
) -> Result<(), DirectIrError> {
    let path = format!("messages[{index}]");
    let role = role_from_wire(Some(&message.role), source, target, &format!("{path}.role"))?;
    if message.tool_call_id.is_some() && !matches!(&role, Role::Tool) {
        return Err(DirectIrError::new(
            source,
            target,
            "tool_result.role",
            format!("{path}.role"),
            DirectIrReason::Mismatch,
        ));
    }
    let is_tool_result = matches!(&role, Role::Tool) || message.tool_call_id.is_some();
    if is_tool_result {
        if !message.tool_calls.is_empty() {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_result.tool_calls",
                &format!("{path}.tool_calls"),
            ));
        }
        if message.reasoning_content.is_some() {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_result.reasoning_content",
                &format!("{path}.reasoning_content"),
            ));
        }
        if message.content.is_none() {
            return Err(DirectIrError::missing(
                source,
                target,
                "tool_result.content",
                &format!("{path}.content"),
            ));
        }
        if message
            .extra_content
            .as_ref()
            .is_some_and(|extra| extra.google.is_some() || extra.anthropic.is_some())
        {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_result.extra_content",
                &format!("{path}.extra_content"),
            ));
        }
        let call_id = authentic_id(
            message.tool_call_id,
            source,
            target,
            "function_call_id",
            &format!("{path}.tool_call_id"),
        )?
        .ok_or_else(|| {
            DirectIrError::missing(
                source,
                target,
                "function_call_id",
                &format!("{path}.tool_call_id"),
            )
        })?;
        let output =
            chat_content_to_json(message.content, source, target, &format!("{path}.content"))?;
        let function = FunctionData::result(message.name, output);
        let mut item = Item::tool_result(
            call_id,
            vec![Part::function(function)],
            Provenance::new(source),
        );
        item.provenance.source_path = Some(path.clone());
        push_item(envelope, item, source, target, &path)?;
        return Ok(());
    }
    let message_google_state = chat_extra_google_state(
        message.extra_content.clone(),
        source,
        target,
        &format!("{path}.extra_content.google.thought_signature"),
    )?;
    let has_anthropic_extension = message
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .is_some();
    if has_anthropic_extension
        && message
            .extra_content
            .as_ref()
            .and_then(|extra| extra.anthropic.as_ref())
            .is_some_and(|value| value.blocks.is_empty())
    {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "anthropic.blocks",
            &format!("{path}.extra_content.anthropic.blocks"),
        ));
    }
    let has_anthropic_blocks = message
        .extra_content
        .as_ref()
        .and_then(|extra| extra.anthropic.as_ref())
        .is_some_and(|value| !value.blocks.is_empty());
    if has_anthropic_blocks && !message.tool_calls.is_empty() {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "mixed_content",
            &format!("{path}.tool_calls"),
        ));
    }
    let mut message_item_is_emitted = has_anthropic_blocks || message.tool_calls.is_empty();
    if has_anthropic_blocks {
        let anthropic_model = message
            .extra_content
            .as_ref()
            .and_then(|extra| extra.anthropic.as_ref())
            .and_then(|value| value.model.as_deref());
        let blocks = message
            .extra_content
            .as_ref()
            .and_then(|extra| extra.anthropic.as_ref())
            .map(|value| value.blocks.clone())
            .unwrap_or_default();
        chat_anthropic_blocks_to_items(
            blocks,
            role.clone(),
            source,
            target,
            &format!("{path}.extra_content.anthropic.blocks"),
            message.name.as_deref(),
            anthropic_model,
            envelope,
        )?;
    } else {
        let parts =
            chat_content_to_parts(message.content, source, target, &format!("{path}.content"))?;
        let mut item = Item::new(ItemKind::Message, role.clone(), Provenance::new(source));
        item.provenance.source_path = Some(path.clone());
        if let Some(name) = message.name.clone() {
            item.provenance
                .extensions
                .insert("openai.name".to_owned(), JsonData::String(name));
        }
        for part in parts {
            item.push_part(part);
        }
        message_item_is_emitted = !item.ordered_parts().is_empty() || message.tool_calls.is_empty();
        if message_item_is_emitted {
            push_item(envelope, item, source, target, &path)?;
        }
        if let Some(reasoning) = message.reasoning_content {
            push_item(
                envelope,
                {
                    let mut item =
                        Item::new(ItemKind::Reasoning, role.clone(), Provenance::new(source));
                    item.provenance.source_path = Some(format!("{path}.reasoning_content"));
                    item.push_part(Part::text(reasoning));
                    item
                },
                source,
                target,
                &format!("{path}.reasoning_content"),
            )?;
        }
    }
    let mut message_google_state = message_google_state;
    for (call_index, call) in message.tool_calls.into_iter().enumerate() {
        let call_path = format!("{path}.tool_calls[{call_index}]");
        validate_openai_tool_call(&call, source, target, &call_path)?;
        if call
            .extra_content
            .as_ref()
            .is_some_and(|extra| extra.anthropic.is_some())
        {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_call.extra_content.anthropic",
                &format!("{call_path}.extra_content"),
            ));
        }
        let call_google_state = chat_extra_google_state(
            call.extra_content.clone(),
            source,
            target,
            &format!("{call_path}.extra_content.google.thought_signature"),
        )?;
        if call.id.is_empty() {
            return Err(DirectIrError::new(
                source,
                target,
                "function_call_id",
                format!("{call_path}.id"),
                DirectIrReason::EmptyId,
            ));
        }
        let arguments = parse_json_field(
            &call.function.arguments,
            source,
            target,
            "function_arguments",
            &format!("{call_path}.function.arguments"),
        )?;
        let call_id = OpaqueId::authentic(call.id, source);
        let call_name = call.function.name;
        let mut call_item = tool_call_item(call_id, call_name, arguments, source, &call_path);
        if call_index == 0
            && !message_item_is_emitted
            && let Some(name) = message.name.clone()
        {
            call_item
                .provenance
                .extensions
                .insert("openai.name".to_owned(), JsonData::String(name));
        }
        push_item(envelope, call_item, source, target, &call_path)?;
        let state = if call_index == 0 {
            match (message_google_state.take(), call_google_state) {
                (Some(_), Some(_)) => {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "thought_signature",
                        &call_path,
                        DirectIrReason::Mismatch,
                    ));
                }
                (Some(state), None) | (None, Some(state)) => Some(state),
                (None, None) => None,
            }
        } else {
            call_google_state
        };
        if let Some(state) = state {
            let state_path = format!("{call_path}.extra_content.google.thought_signature");
            if state.provenance == OpaqueStateProvenance::Synthetic {
                record_loss(
                    envelope,
                    LossCode::SyntheticThoughtSignature,
                    Some(Feature::OpaqueReasoningSignature),
                    &state_path,
                    "synthetic provider signature retained explicitly",
                );
            }
            push_item(
                envelope,
                opaque_item(Role::Assistant, state, source, &state_path),
                source,
                target,
                &state_path,
            )?;
        }
    }
    if let Some(state) = message_google_state {
        let state_path = format!("{path}.extra_content.google.thought_signature");
        if state.provenance == OpaqueStateProvenance::Synthetic {
            record_loss(
                envelope,
                LossCode::SyntheticThoughtSignature,
                Some(Feature::OpaqueReasoningSignature),
                &state_path,
                "synthetic provider signature retained explicitly",
            );
        }
        push_item(
            envelope,
            opaque_item(role, state, source, &state_path),
            source,
            target,
            &state_path,
        )?;
    }
    Ok(())
}

/// Decodes an OpenAI Chat request into an ordered IR Envelope.
pub fn openai_chat_request_to_envelope_v2(
    request: OpenAiChatRequest,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let model = request.model.clone();
    let source = Protocol::OpenAi;
    let target = Protocol::OpenAi;
    let mut envelope = Envelope::new(source, model);
    openai_request_controls(&request, &mut envelope);
    envelope.tool_choice =
        tool_choice_from_json(request.tool_choice.clone(), source, target, "tool_choice")?;
    for (index, tool) in request.tools.iter().enumerate() {
        if !tool.function.arguments.is_empty() {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool.arguments",
                &format!("tools[{index}].function.arguments"),
            ));
        }
        envelope.tools.push(tool_to_ir(
            tool.kind.clone(),
            tool.function.name.clone(),
            tool.function.description.clone(),
            tool.function.parameters.clone(),
            tool.function.strict,
            source,
            target,
            &format!("tools[{index}]"),
        )?);
    }
    for (index, message) in request.messages.into_iter().enumerate() {
        chat_message_to_envelope(message, index, &mut envelope, source, target)?;
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

fn controls_extension(envelope: &Envelope, key: &str) -> Option<JsonData> {
    envelope.controls.extensions.get(key).cloned()
}

fn number_u64(value: &JsonData) -> Option<u64> {
    match value {
        JsonData::Number(number) => number.as_u64(),
        _ => None,
    }
}

fn empty_anthropic_block(kind: &str) -> OpenAiAnthropicBlock {
    OpenAiAnthropicBlock {
        kind: kind.to_owned(),
        text: None,
        thinking: None,
        signature: None,
        synthetic: None,
        data: None,
        id: None,
        name: None,
        input: None,
        tool_use_id: None,
        content: None,
        image_url: None,
        extra: BTreeMap::new(),
    }
}

fn claude_item_to_anthropic_blocks(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Vec<OpenAiAnthropicBlock>, DirectIrError> {
    let mut blocks = Vec::new();
    for (index, part) in item.ordered_parts().iter().enumerate() {
        let part_path = format!("{path}.parts[{index}]");
        let block = match &part.kind {
            PartKind::Text => {
                let mut block = empty_anthropic_block("text");
                block.text = part.text.clone();
                block.extra = part.extensions.clone();
                block
            }
            PartKind::Media => {
                let media = part.media.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "media",
                        &part_path,
                        DirectIrReason::InvalidShape,
                    )
                })?;
                if !matches!(&media.kind, MediaKind::Image) {
                    return Err(DirectIrError::unsupported(
                        source,
                        target,
                        "claude_media",
                        &part_path,
                    ));
                }
                let mut block = empty_anthropic_block("image");
                block.image_url = if let Some(uri) = media.uri.as_ref() {
                    Some(JsonData::String(uri.clone()))
                } else {
                    media.data.clone()
                };
                block.extra = media.extensions.clone();
                block
            }
            PartKind::Function => {
                let function = part.function.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "function",
                        &part_path,
                        DirectIrReason::InvalidShape,
                    )
                })?;
                let call_id = item.call_id.as_ref().ok_or_else(|| {
                    DirectIrError::missing(source, target, "function_call_id", &part_path)
                })?;
                match &function.kind {
                    FunctionKind::Call => {
                        let mut block = empty_anthropic_block("tool_use");
                        block.id = Some(id_value(call_id)?);
                        block.name = function.name.clone();
                        block.input =
                            Some(function.arguments.clone().unwrap_or_else(|| object([])));
                        block.extra = function.extensions.clone();
                        block
                    }
                    FunctionKind::Result => {
                        let mut block = empty_anthropic_block("tool_result");
                        block.tool_use_id = Some(id_value(call_id)?);
                        block.name = function.name.clone();
                        block.content = Some(function.output.clone().unwrap_or(JsonData::Null));
                        block.extra = function.extensions.clone();
                        block
                    }
                    FunctionKind::Unknown(_) => {
                        return Err(DirectIrError::unsupported(
                            source,
                            target,
                            "function_kind",
                            &part_path,
                        ));
                    }
                }
            }
            PartKind::Opaque => {
                let state = part.opaque.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "opaque_state",
                        &part_path,
                        DirectIrReason::InvalidOpaqueState,
                    )
                })?;
                if state.provider != "anthropic" {
                    continue;
                }
                opaque_state_to_anthropic_block(state, source, target, &part_path)?
            }
            PartKind::Unknown(_) => {
                return Err(DirectIrError::unsupported(
                    source,
                    target,
                    "part_kind",
                    &part_path,
                ));
            }
        };
        blocks.push(block);
    }
    Ok(blocks)
}

fn google_state_to_extra_content(
    state: &OpaqueProviderState,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<OpenAiExtraContent, DirectIrError> {
    if state.provider != "google" || state.kind != "thought_signature" {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "thought_signature",
            path,
        ));
    }
    let signature = json_string(&state.raw).ok_or_else(|| {
        DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::InvalidOpaqueState,
        )
    })?;
    if signature.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::EmptyId,
        ));
    }
    Ok(OpenAiExtraContent {
        google: Some(OpenAiGoogleExtraContent {
            thought_signature: Some(signature.to_owned()),
            synthetic: (state.provenance == OpaqueStateProvenance::Synthetic).then_some(true),
        }),
        anthropic: None,
    })
}

fn openai_message_from_item(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
    bound_google_state: Option<&OpaqueProviderState>,
) -> Result<OpenAiChatMessage, DirectIrError> {
    let mut message = OpenAiChatMessage {
        role: chat_role(item.role.clone()).to_owned(),
        content: None,
        reasoning_content: None,
        name: item
            .provenance
            .extensions
            .get("openai.name")
            .and_then(json_string)
            .map(str::to_owned),
        tool_call_id: None,
        tool_calls: Vec::new(),
        extra_content: None,
    };
    match &item.kind {
        ItemKind::Message => {
            message.content = chat_parts_to_content(
                item.ordered_parts(),
                source,
                target,
                &format!("{path}.content"),
            )?;
            let anthropic_model = item
                .provenance
                .extensions
                .get("anthropic.model")
                .and_then(json_string)
                .map(str::to_owned);
            let needs_anthropic_rebuild = anthropic_model.is_some()
                || item
                    .ordered_parts()
                    .iter()
                    .any(|part| !part.extensions.is_empty());
            if needs_anthropic_rebuild {
                let mut anthropic_blocks = Vec::new();
                for (index, part) in item.ordered_parts().iter().enumerate() {
                    let part_path = format!("{path}.parts[{index}]");
                    match &part.kind {
                        PartKind::Text => {
                            let text = part.text.clone().ok_or_else(|| {
                                DirectIrError::new(
                                    source,
                                    target,
                                    "text",
                                    &part_path,
                                    DirectIrReason::InvalidShape,
                                )
                            })?;
                            let mut block = empty_anthropic_block("text");
                            block.text = Some(text);
                            block.extra = part.extensions.clone();
                            anthropic_blocks.push(block);
                        }
                        PartKind::Media => {
                            let media = part.media.as_ref().ok_or_else(|| {
                                DirectIrError::new(
                                    source,
                                    target,
                                    "media",
                                    &part_path,
                                    DirectIrReason::InvalidShape,
                                )
                            })?;
                            if !matches!(&media.kind, MediaKind::Image) {
                                return Err(DirectIrError::unsupported(
                                    source,
                                    target,
                                    "anthropic_media",
                                    &part_path,
                                ));
                            }
                            let image_url = media
                                .uri
                                .as_ref()
                                .map(|value| JsonData::String(value.clone()))
                                .or_else(|| media.data.clone())
                                .ok_or_else(|| {
                                    DirectIrError::missing(source, target, "image_url", &part_path)
                                })?;
                            let mut block = empty_anthropic_block("image");
                            block.image_url = Some(image_url);
                            block.extra = part.extensions.clone();
                            anthropic_blocks.push(block);
                        }
                        PartKind::Function | PartKind::Opaque | PartKind::Unknown(_) => {
                            return Err(DirectIrError::unsupported(
                                source,
                                target,
                                "anthropic_part_extensions",
                                &part_path,
                            ));
                        }
                    }
                }
                message.extra_content = Some(OpenAiExtraContent {
                    google: None,
                    anthropic: Some(OpenAiAnthropicExtraContent {
                        blocks: anthropic_blocks,
                        model: anthropic_model,
                    }),
                });
            }
        }
        ItemKind::Reasoning => {
            let part = single_item_part(item, source, target, path, "reasoning")?;
            match &part.kind {
                PartKind::Text => {
                    message.reasoning_content = part.text.clone();
                }
                PartKind::Opaque => {
                    let Some(state) = part.opaque.as_ref() else {
                        return Err(DirectIrError::new(
                            source,
                            target,
                            "opaque_state",
                            path,
                            DirectIrReason::InvalidOpaqueState,
                        ));
                    };
                    if state.provider == "google" && state.kind == "thought_signature" {
                        message.extra_content =
                            Some(google_state_to_extra_content(state, source, target, path)?);
                    } else if state.provider == "anthropic" {
                        let block = opaque_state_to_anthropic_block(state, source, target, path)?;
                        message.extra_content = Some(OpenAiExtraContent {
                            google: None,
                            anthropic: Some(OpenAiAnthropicExtraContent {
                                blocks: vec![block],
                                model: state.model.clone(),
                            }),
                        });
                    } else {
                        return Err(DirectIrError::unsupported(
                            source,
                            target,
                            "opaque_state",
                            path,
                        ));
                    }
                }
                _ => {
                    return Err(DirectIrError::unsupported(
                        source,
                        target,
                        "reasoning_part",
                        path,
                    ));
                }
            }
        }
        ItemKind::ToolCall => {
            let part = single_item_part(item, source, target, path, "tool_call.parts")?;
            let call_id = item
                .call_id
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function_call_id", path))?;
            let function = part
                .function
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function", path))?;
            let name = function
                .name
                .clone()
                .ok_or_else(|| DirectIrError::missing(source, target, "function.name", path))?;
            let arguments = function.arguments.clone().unwrap_or(object([]));
            message.tool_calls.push(OpenAiToolCall {
                id: id_value(call_id)?,
                kind: "function".to_owned(),
                function: OpenAiFunction {
                    name,
                    arguments: compact_json(&arguments).map_err(|_| {
                        DirectIrError::new(
                            source,
                            target,
                            "function_arguments",
                            path,
                            DirectIrReason::InvalidJsonField,
                        )
                    })?,
                    description: None,
                    parameters: None,
                    strict: None,
                },
                extra_content: bound_google_state
                    .map(|state| google_state_to_extra_content(state, source, target, path))
                    .transpose()?,
            });
        }
        ItemKind::ToolResult => {
            let part = single_item_part(item, source, target, path, "tool_result.parts")?;
            let call_id = item
                .call_id
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function_call_id", path))?;
            let function = part
                .function
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function", path))?;
            message.tool_call_id = Some(id_value(call_id)?);
            message.name = function.name.clone();
            let output = function.output.clone().unwrap_or(JsonData::Null);
            let content = match output {
                JsonData::String(value) => value,
                value => compact_json(&value)?,
            };
            message.content = Some(StringOrParts::String(content));
        }
        ItemKind::Unknown(_) => {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "item_kind",
                path,
            ));
        }
    }
    if item.provenance.source == Protocol::Claude {
        let blocks = claude_item_to_anthropic_blocks(item, source, target, path)?;
        if !blocks.is_empty() {
            let model = item
                .ordered_parts()
                .iter()
                .filter_map(|part| part.opaque.as_ref())
                .find_map(|state| state.model.clone());
            let mut extra = message.extra_content.take().unwrap_or(OpenAiExtraContent {
                google: None,
                anthropic: None,
            });
            extra.anthropic = Some(OpenAiAnthropicExtraContent { blocks, model });
            message.extra_content = Some(extra);
        }
    }
    Ok(message)
}

fn opaque_state_to_anthropic_block(
    state: &OpaqueProviderState,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<OpenAiAnthropicBlock, DirectIrError> {
    let object = json_object(&state.raw).ok_or_else(|| {
        DirectIrError::new(
            source,
            target,
            "opaque_state",
            path,
            DirectIrReason::InvalidOpaqueState,
        )
    })?;
    let kind = object
        .get("type")
        .and_then(json_string)
        .ok_or_else(|| DirectIrError::missing(source, target, "opaque_state.type", path))?;
    let extra = object
        .get("extra")
        .and_then(json_object)
        .cloned()
        .unwrap_or_default();
    match kind {
        "thinking" => {
            let signature = required_claude_thinking_signature(
                object.get("signature").and_then(json_string),
                source,
                target,
                &format!("{path}.signature"),
            )?;
            Ok(OpenAiAnthropicBlock {
                kind: "thinking".to_owned(),
                text: None,
                thinking: object
                    .get("thinking")
                    .and_then(json_string)
                    .map(str::to_owned),
                signature: Some(signature),
                synthetic: (state.provenance == OpaqueStateProvenance::Synthetic).then_some(true),
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                image_url: None,
                extra,
            })
        }
        "redacted_thinking" => Ok(OpenAiAnthropicBlock {
            kind: "redacted_thinking".to_owned(),
            text: None,
            thinking: None,
            signature: None,
            synthetic: (state.provenance == OpaqueStateProvenance::Synthetic).then_some(true),
            data: object.get("data").cloned(),
            id: None,
            name: None,
            input: None,
            tool_use_id: None,
            content: None,
            image_url: None,
            extra,
        }),
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "anthropic_opaque_state",
            path,
        )),
    }
}

/// Encodes an ordered Envelope as an OpenAI Chat request.
pub fn envelope_to_openai_chat_request_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::OpenAi;
    record_request_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    let mut output = OpenAiChatRequest {
        model: envelope.model.clone(),
        messages: Vec::new(),
        stream: envelope.controls.stream,
        max_tokens: controls_extension(&envelope, "openai.max_tokens")
            .and_then(|value| number_u64(&value)),
        max_completion_tokens: controls_extension(&envelope, "openai.max_completion_tokens")
            .and_then(|value| number_u64(&value)),
        temperature: envelope.controls.temperature,
        tools: Vec::new(),
        tool_choice: Some(tool_choice_to_json(&envelope.tool_choice)),
        top_p: envelope.controls.top_p,
        n: controls_extension(&envelope, "n").and_then(|value| number_u64(&value)),
        reasoning_effort: envelope.controls.reasoning_effort.clone(),
        response_format: envelope.controls.response_format.clone(),
        parallel_tool_calls: envelope.controls.parallel_tool_calls,
        user: controls_extension(&envelope, "user"),
        store: controls_extension(&envelope, "store"),
        metadata: controls_extension(&envelope, "metadata"),
        stream_options: controls_extension(&envelope, "stream_options"),
        top_logprobs: controls_extension(&envelope, "top_logprobs").and_then(|value| match value {
            JsonData::Number(number) => number.as_i64(),
            _ => None,
        }),
        safety_identifier: controls_extension(&envelope, "safety_identifier"),
        prompt_cache_retention: controls_extension(&envelope, "prompt_cache_retention"),
        prompt_cache_key: controls_extension(&envelope, "prompt_cache_key")
            .and_then(|value| json_string(&value).map(str::to_owned)),
        service_tier: controls_extension(&envelope, "service_tier")
            .and_then(|value| json_string(&value).map(str::to_owned)),
        enable_thinking: controls_extension(&envelope, "enable_thinking"),
        thinking_budget: controls_extension(&envelope, "thinking_budget"),
    };
    if let Some(max_output_tokens) = envelope.controls.max_output_tokens
        && output.max_tokens.is_none()
        && output.max_completion_tokens.is_none()
    {
        output.max_completion_tokens = Some(max_output_tokens);
    }
    for (index, tool) in envelope.tools.iter().enumerate() {
        let name = tool_name_for_target(tool, index, source, target)?;
        output.tools.push(OpenAiChatTool {
            kind: "function".to_owned(),
            function: OpenAiFunction {
                name,
                arguments: String::new(),
                description: tool.description.clone(),
                parameters: tool.input_schema.clone(),
                strict: openai_strict_for_tool(tool, source, target, index)?,
            },
        });
    }
    output.messages = openai_response_message_groups(&mut envelope, source, target)?;
    Ok(finish(output, envelope, source, target))
}

/// Protocol-first alias for the OpenAI Chat request encoder.
pub fn envelope_to_openai_chat_request(
    envelope: Envelope,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    envelope_to_openai_chat_request_v2(envelope)
}

#[derive(Default)]
struct GeminiCallLinks {
    by_id: BTreeMap<String, (String, OpaqueIdProvenance)>,
    by_name: BTreeMap<String, VecDeque<(String, OpaqueIdProvenance)>>,
    next: usize,
}

impl GeminiCallLinks {
    fn remember(
        &mut self,
        name: &str,
        id: String,
        provenance: OpaqueIdProvenance,
        source: Protocol,
        target: Protocol,
        path: &str,
    ) -> Result<(), DirectIrError> {
        if id.is_empty() {
            return Err(DirectIrError::new(
                source,
                target,
                "function_call_id",
                path,
                DirectIrReason::EmptyId,
            ));
        }
        if self.by_id.contains_key(&id) {
            return Err(DirectIrError::new(
                source,
                target,
                "function_call_id",
                path,
                DirectIrReason::DuplicateId,
            ));
        }
        self.by_name
            .entry(name.to_owned())
            .or_default()
            .push_back((id.clone(), provenance));
        self.by_id.insert(id, (name.to_owned(), provenance));
        Ok(())
    }

    fn synthetic_call_id(&mut self, index: usize, part_index: usize, name: &str) -> String {
        loop {
            let id = synthetic_gemini_id("call", index, part_index, name);
            self.next = self.next.saturating_add(1);
            if !self.by_id.contains_key(&id) {
                return id;
            }
        }
    }

    fn take_result(
        &mut self,
        name: &str,
        id: Option<String>,
        source: Protocol,
        target: Protocol,
        path: &str,
    ) -> Result<(String, OpaqueIdProvenance), DirectIrError> {
        if let Some(id) = id {
            if id.is_empty() {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::EmptyId,
                ));
            }
            let Some(expected_name) = self.by_id.get(&id) else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Orphan,
                ));
            };
            if expected_name.0 != name {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_result_name",
                    path,
                    DirectIrReason::Mismatch,
                ));
            }
            let Some(queue) = self.by_name.get_mut(name) else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Orphan,
                ));
            };
            let Some(position) = queue.iter().position(|candidate| candidate.0 == id) else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Mismatch,
                ));
            };
            let Some((_, provenance)) = queue.remove(position) else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Mismatch,
                ));
            };
            self.by_id.remove(&id);
            Ok((id, provenance))
        } else {
            let Some(queue) = self.by_name.get_mut(name) else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Orphan,
                ));
            };
            let Some((id, provenance)) = queue.front().cloned() else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    path,
                    DirectIrReason::Orphan,
                ));
            };
            let _ = queue.pop_front();
            self.by_id.remove(&id);
            Ok((id, provenance))
        }
    }
}

fn gemini_media_kind(mime_type: &str) -> MediaKind {
    if mime_type.starts_with("image/") {
        MediaKind::Image
    } else if mime_type.starts_with("audio/") {
        MediaKind::Audio
    } else if mime_type.starts_with("video/") {
        MediaKind::Video
    } else {
        MediaKind::File
    }
}

fn gemini_part_to_items(
    part: GeminiPart,
    content_index: usize,
    part_index: usize,
    role: Role,
    envelope: &mut Envelope,
    links: &mut GeminiCallLinks,
    source: Protocol,
    target: Protocol,
    model: &str,
) -> Result<(), DirectIrError> {
    let path = format!("contents[{content_index}].parts[{part_index}]");
    let payload_count = usize::from(part.text.is_some())
        + usize::from(part.inline_data.is_some())
        + usize::from(part.function_call.is_some())
        + usize::from(part.function_response.is_some());
    if payload_count > 1 {
        return Err(DirectIrError::new(
            source,
            target,
            "gemini_part_shape",
            &path,
            DirectIrReason::InvalidShape,
        ));
    }
    if let Some(text) = part.text {
        push_item(
            envelope,
            text_item(role.clone(), text, source, &path),
            source,
            target,
            &path,
        )?;
    }
    if let Some(inline) = part.inline_data {
        let mut media = Media::new(gemini_media_kind(&inline.mime_type));
        media.mime_type = Some(inline.mime_type);
        media.data = Some(JsonData::String(inline.data));
        push_item(
            envelope,
            media_item(role.clone(), media, source, &path),
            source,
            target,
            &path,
        )?;
    }
    if let Some(call) = part.function_call {
        let GeminiFunctionCall { id, name, args, .. } = call;
        let (id, provenance) = match id {
            Some(id) if id.is_empty() => {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "function_call_id",
                    format!("{path}.functionCall.id"),
                    DirectIrReason::EmptyId,
                ));
            }
            Some(id) => (id, OpaqueIdProvenance::Authentic),
            None => {
                let id = links.synthetic_call_id(content_index, part_index, &name);
                record_loss(
                    envelope,
                    LossCode::SyntheticToolCallId,
                    Some(Feature::FunctionCallId),
                    &format!("{path}.functionCall.id"),
                    "Gemini omitted function call id; deterministic id retained",
                );
                (id, OpaqueIdProvenance::Synthetic)
            }
        };
        links.remember(
            &name,
            id.clone(),
            provenance,
            source,
            target,
            &format!("{path}.functionCall.id"),
        )?;
        push_item(
            envelope,
            tool_call_item(
                OpaqueId {
                    value: id,
                    provenance,
                    source,
                },
                name,
                args.unwrap_or_else(|| object([])),
                source,
                &path,
            ),
            source,
            target,
            &path,
        )?;
    }
    if let Some(result) = part.function_response {
        let GeminiFunctionResponse {
            id, name, response, ..
        } = result;
        let (call_id, provenance) = links.take_result(
            &name,
            id,
            source,
            target,
            &format!("{path}.functionResponse.id"),
        )?;
        let id = OpaqueId {
            value: call_id,
            provenance,
            source,
        };
        push_item(
            envelope,
            tool_result_item(id, Some(name), response, source, &path),
            source,
            target,
            &path,
        )?;
    }
    if let Some(signature) = part.thought_signature {
        if signature.is_empty() {
            return Err(DirectIrError::new(
                source,
                target,
                "thought_signature",
                format!("{path}.thoughtSignature"),
                DirectIrReason::EmptyId,
            ));
        }
        if payload_count == 0 {
            push_item(
                envelope,
                text_item(role.clone(), String::new(), source, &format!("{path}.text")),
                source,
                target,
                &path,
            )?;
        }
        let state = provider_state(
            "google",
            "thought_signature",
            JsonData::String(signature),
            OpaqueStateProvenance::Authentic,
            Some(model.to_owned()),
        );
        push_item(
            envelope,
            opaque_item(role, state, source, &format!("{path}.thoughtSignature")),
            source,
            target,
            &path,
        )?;
    }
    Ok(())
}

fn gemini_tools_to_ir(request: &GeminiRequest) -> Vec<Tool> {
    request
        .tools
        .iter()
        .flat_map(|tool| tool.function_declarations.iter())
        .map(|tool| Tool {
            kind: ToolKind::Function,
            name: Some(tool.name.clone()),
            description: tool.description.clone(),
            input_schema: Some(tool.parameters.clone()),
            extensions: empty_extensions(),
        })
        .collect()
}

fn gemini_tool_choice_to_ir(
    config: Option<&GeminiToolConfig>,
    source: Protocol,
    target: Protocol,
) -> Result<ToolChoice, DirectIrError> {
    let Some(config) = config else {
        return Ok(ToolChoice::Auto);
    };
    match config.function_calling_config.mode.as_str() {
        "AUTO" => Ok(ToolChoice::Auto),
        "ANY" => Ok(ToolChoice::Required),
        "NONE" => Ok(ToolChoice::None),
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "tool_choice",
            "toolConfig.functionCallingConfig.mode",
        )),
    }
}

fn reject_gemini_request_nested_extra(
    request: &GeminiRequest,
    source: Protocol,
    target: Protocol,
) -> Result<(), DirectIrError> {
    reject_wire_extra(source, target, &request.extra, "")?;
    if let Some(config) = request.generation_config.as_ref() {
        reject_wire_extra(source, target, &config.extra, "generationConfig")?;
    }
    for (index, setting) in request.safety_settings.iter().enumerate() {
        reject_wire_extra(
            source,
            target,
            &setting.extra,
            &format!("safetySettings[{index}]"),
        )?;
    }
    for (tool_index, tool) in request.tools.iter().enumerate() {
        let tool_path = format!("tools[{tool_index}]");
        reject_wire_extra(source, target, &tool.extra, &tool_path)?;
        for (declaration_index, declaration) in tool.function_declarations.iter().enumerate() {
            reject_wire_extra(
                source,
                target,
                &declaration.extra,
                &format!("{tool_path}.functionDeclarations[{declaration_index}]"),
            )?;
        }
    }
    if let Some(config) = request.tool_config.as_ref() {
        reject_wire_extra(source, target, &config.extra, "toolConfig")?;
        reject_wire_extra(
            source,
            target,
            &config.function_calling_config.extra,
            "toolConfig.functionCallingConfig",
        )?;
    }
    Ok(())
}

/// Decodes a Gemini request.  Gemini does not put its model in the JSON body,
/// so callers supply the route model explicitly.
pub fn gemini_request_to_envelope_v2(
    request: GeminiRequest,
    model: impl Into<String>,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let model = model.into();
    let source = Protocol::Gemini;
    let target = Protocol::Gemini;
    let mut envelope = Envelope::new(source, model.clone());
    reject_gemini_request_nested_extra(&request, source, target)?;
    let tools = gemini_tools_to_ir(&request);
    let tool_choice = gemini_tool_choice_to_ir(request.tool_config.as_ref(), source, target)?;
    let GeminiRequest {
        system_instruction,
        contents,
        generation_config,
        safety_settings,
        ..
    } = request;
    if let Some(system) = system_instruction.as_ref() {
        reject_gemini_content_extra(system, source, target, "systemInstruction")?;
    }
    for (content_index, content) in contents.iter().enumerate() {
        reject_gemini_content_extra(
            content,
            source,
            target,
            &format!("contents[{content_index}]"),
        )?;
    }
    let has_system_instruction = system_instruction.is_some();
    if let Some(system) = system_instruction {
        let role = Role::System;
        let mut links = GeminiCallLinks::default();
        for (part_index, part) in system.parts.into_iter().enumerate() {
            gemini_part_to_items(
                part,
                0,
                part_index,
                role.clone(),
                &mut envelope,
                &mut links,
                source,
                target,
                &model,
            )?;
        }
    }
    let mut links = GeminiCallLinks::default();
    let system_offset = usize::from(has_system_instruction);
    for (content_index, content) in contents.into_iter().enumerate() {
        let role = if content
            .parts
            .iter()
            .any(|part| part.function_response.is_some())
        {
            Role::Tool
        } else {
            role_from_wire(
                content.role.as_deref(),
                source,
                target,
                &format!("contents[{content_index}].role"),
            )?
        };
        let content_is_empty = content.parts.is_empty();
        for (part_index, part) in content.parts.into_iter().enumerate() {
            gemini_part_to_items(
                part,
                content_index.saturating_add(system_offset),
                part_index,
                role.clone(),
                &mut envelope,
                &mut links,
                source,
                target,
                &model,
            )?;
        }
        if content_is_empty {
            push_item(
                &mut envelope,
                Item::new(ItemKind::Message, role, Provenance::new(source)),
                source,
                target,
                &format!("contents[{content_index}].parts"),
            )?;
        }
    }
    envelope.tools = tools;
    envelope.tool_choice = tool_choice;
    if let Some(config) = generation_config {
        envelope.controls.max_output_tokens = config.max_output_tokens;
        envelope.controls.temperature = config.temperature;
    }
    if !safety_settings.is_empty() {
        let settings = safety_settings
            .into_iter()
            .map(|setting| {
                object([
                    ("category".to_owned(), JsonData::String(setting.category)),
                    ("threshold".to_owned(), JsonData::String(setting.threshold)),
                ])
            })
            .collect();
        envelope.extensions.insert(
            "gemini.safety_settings".to_owned(),
            JsonData::Array(settings),
        );
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

/// Explicitly named model variant for route adapters.
pub fn gemini_request_to_envelope_v2_for_model(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    gemini_request_to_envelope_v2(request, model)
}

fn gemini_signature_from_state(
    state: &OpaqueProviderState,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<String, DirectIrError> {
    if state.provider != "google" || state.kind != "thought_signature" {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "thought_signature",
            path,
        ));
    }
    let Some(signature) = json_string(&state.raw) else {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::InvalidOpaqueState,
        ));
    };
    if signature.is_empty() {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::EmptyId,
        ));
    }
    Ok(signature.to_owned())
}

fn gemini_model_is_25(model: &str) -> bool {
    let model = model.to_ascii_lowercase();
    model.contains("gemini-2.5") || model.contains("gemini_2_5")
}

fn gemini_model_is_3(model: &str) -> bool {
    model.to_ascii_lowercase().contains("gemini-3")
}

fn media_to_gemini_part(
    media: &Media,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<GeminiPart, DirectIrError> {
    let Some(data) = media.data.as_ref() else {
        return Err(DirectIrError::unsupported(source, target, "media", path));
    };
    let Some(data) = json_string(data) else {
        return Err(DirectIrError::unsupported(source, target, "media", path));
    };
    let Some(mime_type) = media.mime_type.as_ref() else {
        return Err(DirectIrError::missing(source, target, "mime_type", path));
    };
    Ok(GeminiPart {
        text: None,
        inline_data: Some(GeminiInlineData {
            mime_type: mime_type.clone(),
            data: data.to_owned(),
            extra: BTreeMap::new(),
        }),
        function_call: None,
        function_response: None,
        thought_signature: None,
        ..GeminiPart::default()
    })
}

fn gemini_part_for_item(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<GeminiPart, DirectIrError> {
    let part = single_item_part(item, source, target, path, "part")?;
    match &part.kind {
        PartKind::Text => Ok(GeminiPart {
            text: Some(part.text.clone().unwrap_or_default()),
            inline_data: None,
            function_call: None,
            function_response: None,
            thought_signature: None,
            ..GeminiPart::default()
        }),
        PartKind::Media => part
            .media
            .as_ref()
            .ok_or_else(|| {
                DirectIrError::new(source, target, "media", path, DirectIrReason::InvalidShape)
            })
            .and_then(|media| media_to_gemini_part(media, source, target, path)),
        PartKind::Function => {
            let function = part.function.as_ref().ok_or_else(|| {
                DirectIrError::new(
                    source,
                    target,
                    "function",
                    path,
                    DirectIrReason::InvalidShape,
                )
            })?;
            let call_id = item
                .call_id
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function_call_id", path))?;
            let id = id_value(call_id)?;
            match &function.kind {
                FunctionKind::Call => Ok(GeminiPart {
                    text: None,
                    inline_data: None,
                    function_call: Some(GeminiFunctionCall {
                        id: Some(id),
                        name: function.name.clone().ok_or_else(|| {
                            DirectIrError::missing(source, target, "function.name", path)
                        })?,
                        args: Some(function.arguments.clone().unwrap_or_else(|| object([]))),
                        extra: BTreeMap::new(),
                    }),
                    function_response: None,
                    thought_signature: None,
                    ..GeminiPart::default()
                }),
                FunctionKind::Result => Ok(GeminiPart {
                    text: None,
                    inline_data: None,
                    function_call: None,
                    function_response: Some(GeminiFunctionResponse {
                        id: Some(id),
                        name: function.name.clone().unwrap_or_default(),
                        response: function.output.clone().unwrap_or(JsonData::Null),
                        extra: BTreeMap::new(),
                    }),
                    thought_signature: None,
                    ..GeminiPart::default()
                }),
                FunctionKind::Unknown(_) => Err(DirectIrError::unsupported(
                    source,
                    target,
                    "function_kind",
                    path,
                )),
            }
        }
        PartKind::Opaque | PartKind::Unknown(_) => Err(DirectIrError::unsupported(
            source,
            target,
            "opaque_part",
            path,
        )),
    }
}

fn append_gemini_state_signature(
    destination: &mut GeminiPart,
    state: &OpaqueProviderState,
    source: Protocol,
    target: Protocol,
    path: &str,
    envelope: &mut Envelope,
) -> Result<(), DirectIrError> {
    if state.provenance == OpaqueStateProvenance::Synthetic
        && state.raw != JsonData::String(super::GEMINI_SYNTHETIC_THOUGHT_SIGNATURE.to_owned())
    {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "synthetic_thought_signature",
            path,
        ));
    }
    if destination.thought_signature.is_some() {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature",
            path,
            DirectIrReason::DuplicateId,
        ));
    }
    let signature = gemini_signature_from_state(state, source, target, path)?;
    destination.thought_signature = Some(signature);
    if state.provenance == OpaqueStateProvenance::Synthetic {
        record_loss(
            envelope,
            LossCode::SyntheticThoughtSignature,
            Some(Feature::OpaqueReasoningSignature),
            path,
            "synthetic Gemini history signature emitted by explicit policy",
        );
    }
    Ok(())
}

fn ordinary_reasoning_text(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
    envelope: &mut Envelope,
) -> Result<GeminiPart, DirectIrError> {
    let part = single_item_part(item, source, target, path, "reasoning")?;
    match &part.kind {
        PartKind::Text => {
            record_loss(
                envelope,
                LossCode::LossOpaqueReasoning,
                Some(Feature::ReasoningSummary),
                path,
                "reasoning emitted as ordinary Gemini text",
            );
            Ok(GeminiPart {
                text: Some(part.text.clone().unwrap_or_default()),
                inline_data: None,
                function_call: None,
                function_response: None,
                thought_signature: None,
                ..GeminiPart::default()
            })
        }
        PartKind::Opaque => {
            let Some(state) = part.opaque.as_ref() else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "opaque_state",
                    path,
                    DirectIrReason::InvalidOpaqueState,
                ));
            };
            if state.provider == "google" && state.kind == "thought_signature" {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "thought_signature_position",
                    path,
                    DirectIrReason::Mismatch,
                ));
            }
            let object = json_object(&state.raw);
            let thinking = object
                .and_then(|value| value.get("thinking"))
                .and_then(json_string);
            if state.provider == "anthropic" && state.kind == "thinking" {
                let Some(thinking) = thinking else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "thinking",
                        path,
                        DirectIrReason::InvalidOpaqueState,
                    ));
                };
                record_loss(
                    envelope,
                    LossCode::LossOpaqueReasoning,
                    Some(Feature::OpaqueReasoningSignature),
                    path,
                    "Claude thinking emitted as ordinary Gemini text without fabricated signature",
                );
                return Ok(GeminiPart {
                    text: Some(thinking.to_owned()),
                    inline_data: None,
                    function_call: None,
                    function_response: None,
                    thought_signature: None,
                    ..GeminiPart::default()
                });
            }
            Err(DirectIrError::unsupported(
                source,
                target,
                "opaque_reasoning",
                path,
            ))
        }
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "reasoning_part",
            path,
        )),
    }
}

fn gemini_content_role(item: &Item) -> &'static str {
    match &item.kind {
        ItemKind::ToolCall | ItemKind::Reasoning => "model",
        ItemKind::ToolResult => "user",
        ItemKind::Message => match &item.role {
            Role::Assistant | Role::Model => "model",
            _ => "user",
        },
        ItemKind::Unknown(_) => "user",
    }
}

fn append_gemini_content(
    item: &Item,
    contents: &mut Vec<GeminiContent>,
    source: Protocol,
    target: Protocol,
    path: &str,
    envelope: &mut Envelope,
    pending_call: &mut Option<(String, bool)>,
    synthetic_history: bool,
    model: &str,
) -> Result<(), DirectIrError> {
    if matches!(&item.kind, ItemKind::Reasoning) {
        let part = ordinary_reasoning_text(item, source, target, path, envelope)?;
        contents.push(GeminiContent {
            role: Some("model".to_owned()),
            parts: vec![part],
            ..GeminiContent::default()
        });
        return Ok(());
    }
    let part = gemini_part_for_item(item, source, target, path)?;
    if matches!(&item.kind, ItemKind::ToolCall) {
        item.call_id
            .as_ref()
            .ok_or_else(|| DirectIrError::missing(source, target, "function_call_id", path))?;
        let name = item
            .ordered_parts()
            .first()
            .and_then(|value| value.function.as_ref())
            .and_then(|function| function.name.as_ref())
            .ok_or_else(|| DirectIrError::missing(source, target, "function.name", path))?
            .clone();
        *pending_call = Some((name, false));
        contents.push(GeminiContent {
            role: Some("model".to_owned()),
            parts: vec![part],
            ..GeminiContent::default()
        });
        return Ok(());
    }
    if matches!(&item.kind, ItemKind::ToolResult) {
        *pending_call = None;
    }
    if matches!(&item.kind, ItemKind::Message) {
        *pending_call = None;
    }
    if let Some((_, saw_signature)) = pending_call.as_mut() {
        if *saw_signature {
            return Err(DirectIrError::new(
                source,
                target,
                "thought_signature",
                path,
                DirectIrReason::DuplicateId,
            ));
        }
        let _ = model;
        let _ = synthetic_history;
    }
    contents.push(GeminiContent {
        role: Some(gemini_content_role(item).to_owned()),
        parts: vec![part],
        ..GeminiContent::default()
    });
    Ok(())
}

fn append_gemini_opaque_item(
    item: &Item,
    contents: &mut [GeminiContent],
    source: Protocol,
    target: Protocol,
    path: &str,
    envelope: &mut Envelope,
    pending_call: &mut Option<(String, bool)>,
) -> Result<(), DirectIrError> {
    let part = single_item_part(item, source, target, path, "opaque_state")?;
    let Some(state) = part.opaque.as_ref() else {
        return Err(DirectIrError::new(
            source,
            target,
            "opaque_state",
            path,
            DirectIrReason::InvalidOpaqueState,
        ));
    };
    if state.provider != "google" || state.kind != "thought_signature" {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "opaque_reasoning",
            path,
        ));
    }
    let Some(last) = contents.last_mut() else {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature_order",
            path,
            DirectIrReason::Mismatch,
        ));
    };
    let Some(last_part) = last.parts.last_mut() else {
        return Err(DirectIrError::new(
            source,
            target,
            "thought_signature_order",
            path,
            DirectIrReason::Mismatch,
        ));
    };
    if last_part.function_call.is_none() {
        // A signature on ordinary text is retained at that exact Part.
        if last_part.text.is_none() && last_part.inline_data.is_none() {
            return Err(DirectIrError::new(
                source,
                target,
                "thought_signature_order",
                path,
                DirectIrReason::Mismatch,
            ));
        }
    }
    if pending_call.is_some() {
        let Some((_, saw_signature)) = pending_call.as_mut() else {
            return Err(DirectIrError::new(
                source,
                target,
                "thought_signature_order",
                path,
                DirectIrReason::Mismatch,
            ));
        };
        if *saw_signature {
            return Err(DirectIrError::new(
                source,
                target,
                "thought_signature",
                path,
                DirectIrReason::DuplicateId,
            ));
        }
        *saw_signature = true;
    }
    append_gemini_state_signature(last_part, state, source, target, path, envelope)
}

fn gemini_generation_config(envelope: &Envelope) -> Option<GeminiGenerationConfig> {
    if envelope.controls.max_output_tokens.is_none() && envelope.controls.temperature.is_none() {
        None
    } else {
        Some(GeminiGenerationConfig {
            max_output_tokens: envelope.controls.max_output_tokens,
            temperature: envelope.controls.temperature,
            extra: BTreeMap::new(),
        })
    }
}

fn gemini_safety_settings(envelope: &Envelope) -> Vec<super::GeminiSafetySetting> {
    let Some(JsonData::Array(values)) = envelope.extensions.get("gemini.safety_settings") else {
        return Vec::new();
    };
    values
        .iter()
        .filter_map(|value| {
            let object = json_object(value)?;
            Some(super::GeminiSafetySetting {
                category: object.get("category").and_then(json_string)?.to_owned(),
                threshold: object.get("threshold").and_then(json_string)?.to_owned(),
                extra: BTreeMap::new(),
            })
        })
        .collect()
}

fn gemini_tool_choice(envelope: &mut Envelope) -> Option<GeminiToolConfig> {
    let mode = match envelope.tool_choice.clone() {
        ToolChoice::None => "NONE",
        ToolChoice::Required => "ANY",
        ToolChoice::Named { .. } => {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::ToolChoiceNamed),
                "tool_choice.name",
                "Gemini function-calling config has no named-tool field; mode ANY is emitted",
            );
            "ANY"
        }
        ToolChoice::Auto => "AUTO",
        ToolChoice::Provider { .. } => {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "tool_choice",
                "provider tool choice cannot be represented by Gemini; mode AUTO is emitted",
            );
            "AUTO"
        }
    };
    Some(GeminiToolConfig {
        function_calling_config: GeminiFunctionCallingConfig {
            mode: mode.to_owned(),
            extra: BTreeMap::new(),
        },
        extra: BTreeMap::new(),
    })
}

/// Encodes an Envelope as a Gemini request using the envelope model.
///
/// Synthetic history signatures are enabled only for Gemini 2.5.  Gemini 3
/// requires an authentic signature unless the caller explicitly supplied a
/// synthetic state and opted into history compatibility via the model variant.
pub fn envelope_to_gemini_request_v2(
    envelope: Envelope,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    let model = envelope.model.clone();
    envelope_to_gemini_request_v2_for_model(envelope, &model, true)
}

/// Encodes an Envelope as a Gemini request with explicit signature policy.
pub fn envelope_to_gemini_request_v2_for_model(
    mut envelope: Envelope,
    model: &str,
    synthetic_history: bool,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::Gemini;
    record_request_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    let mut system_parts = Vec::new();
    let mut contents = Vec::new();
    let mut pending_call: Option<(String, bool)> = None;
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        let path = format!("items[{index}]");
        if matches!(&item.role, Role::System | Role::Developer) {
            if matches!(&item.kind, ItemKind::Reasoning) {
                let part = ordinary_reasoning_text(&item, source, target, &path, &mut envelope)?;
                system_parts.push(part);
            } else {
                let part = gemini_part_for_item(&item, source, target, &path)?;
                if matches!(&item.kind, ItemKind::ToolCall) {
                    return Err(DirectIrError::unsupported(
                        source,
                        target,
                        "system_tool_call",
                        &path,
                    ));
                }
                system_parts.push(part);
            }
            continue;
        }
        if matches!(&item.kind, ItemKind::Reasoning)
            && item
                .ordered_parts()
                .first()
                .and_then(|part| part.opaque.as_ref())
                .is_some_and(|state| {
                    state.provider == "google" && state.kind == "thought_signature"
                })
        {
            append_gemini_opaque_item(
                &item,
                &mut contents,
                source,
                target,
                &path,
                &mut envelope,
                &mut pending_call,
            )?;
            continue;
        }
        append_gemini_content(
            &item,
            &mut contents,
            source,
            target,
            &path,
            &mut envelope,
            &mut pending_call,
            synthetic_history,
            model,
        )?;
        if matches!(&item.kind, ItemKind::ToolCall) {
            let has_signature = envelope
                .ordered_items()
                .get(index.saturating_add(1))
                .filter(|next| next.ordered_parts().len() == 1)
                .and_then(|next| next.ordered_parts().first())
                .and_then(|part| part.opaque.as_ref())
                .is_some_and(|state| {
                    state.provider == "google" && state.kind == "thought_signature"
                });
            if !has_signature && gemini_model_is_3(model) {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "thought_signature",
                    &path,
                    DirectIrReason::Unsupported,
                ));
            }
            // The documented dummy is only a compatibility value for history
            // synthesized by a non-Gemini source.  A Gemini-origin envelope is
            // already provider history: injecting the literal there would
            // turn an omitted authentic signature into gateway-authored state
            // and could change the provider's tool-loop validation semantics.
            if !has_signature
                && synthetic_history
                && source != Protocol::Gemini
                && gemini_model_is_25(model)
            {
                let Some(last) = contents.last_mut() else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "thought_signature",
                        &path,
                        DirectIrReason::Mismatch,
                    ));
                };
                let Some(last_part) = last.parts.last_mut() else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "thought_signature",
                        &path,
                        DirectIrReason::Mismatch,
                    ));
                };
                last_part.thought_signature =
                    Some(super::GEMINI_SYNTHETIC_THOUGHT_SIGNATURE.to_owned());
                record_loss(
                    &mut envelope,
                    LossCode::SyntheticThoughtSignature,
                    Some(Feature::OpaqueReasoningSignature),
                    &path,
                    "Gemini 2.5 history signature generated by explicit policy",
                );
            }
        }
    }
    let mut declarations = Vec::new();
    for (index, tool) in envelope.tools.iter().enumerate() {
        let name = tool_name_for_target(tool, index, source, target)?;
        declarations.push(GeminiFunctionDeclaration {
            name,
            description: tool.description.clone(),
            parameters: tool.input_schema.clone().unwrap_or_else(|| object([])),
            extra: BTreeMap::new(),
        });
    }
    let output = GeminiRequest {
        contents,
        system_instruction: (!system_parts.is_empty()).then_some(GeminiContent {
            role: None,
            parts: system_parts,
            ..GeminiContent::default()
        }),
        generation_config: gemini_generation_config(&envelope),
        safety_settings: gemini_safety_settings(&envelope),
        tools: if declarations.is_empty() {
            Vec::new()
        } else {
            vec![GeminiTool {
                function_declarations: declarations,
                extra: BTreeMap::new(),
            }]
        },
        tool_config: gemini_tool_choice(&mut envelope),
        extra: BTreeMap::new(),
    };
    Ok(finish(output, envelope, source, target))
}

fn validate_claude_block_shape(
    block: &ClaudeContentBlock,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    let invalid_field = match block.kind.as_str() {
        "text" => {
            if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.source.is_some() {
                Some("source")
            } else {
                None
            }
        }
        "thinking" => {
            if block.text.is_some() {
                Some("text")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.source.is_some() {
                Some("source")
            } else {
                None
            }
        }
        "redacted_thinking" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.source.is_some() {
                Some("source")
            } else if block.data.is_some() && block.content.is_some() {
                Some("content")
            } else {
                None
            }
        }
        "tool_use" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.data.is_some() {
                Some("data")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else if block.source.is_some() {
                Some("source")
            } else {
                None
            }
        }
        "tool_result" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.input.is_some() {
                Some("input")
            } else if block.source.is_some() {
                Some("source")
            } else {
                None
            }
        }
        "image" => {
            if block.text.is_some() {
                Some("text")
            } else if block.thinking.is_some() {
                Some("thinking")
            } else if block.signature.is_some() {
                Some("signature")
            } else if block.data.is_some() {
                Some("data")
            } else if block.id.is_some() {
                Some("id")
            } else if block.name.is_some() {
                Some("name")
            } else if block.input.is_some() {
                Some("input")
            } else if block.tool_use_id.is_some() {
                Some("tool_use_id")
            } else if block.content.is_some() {
                Some("content")
            } else {
                None
            }
        }
        _ => None,
    };
    if let Some(field) = invalid_field {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "claude_block_field",
            &format!("{path}.{field}"),
        ));
    }
    Ok(())
}

fn claude_block_to_items(
    block: ClaudeContentBlock,
    role: Role,
    message_index: usize,
    block_index: usize,
    envelope: &mut Envelope,
    source: Protocol,
    target: Protocol,
    model: &str,
) -> Result<(), DirectIrError> {
    let path = format!("messages[{message_index}].content[{block_index}]");
    validate_claude_block_shape(&block, source, target, &path)?;
    match block.kind.as_str() {
        "text" => {
            let text = block.text.ok_or_else(|| {
                DirectIrError::missing(source, target, "text", &format!("{path}.text"))
            })?;
            let mut item = text_item(role, text, source, &path);
            item.provenance.extensions = block.extra;
            push_item(envelope, item, source, target, &path)?;
        }
        "thinking" => {
            let thinking = block.thinking.ok_or_else(|| {
                DirectIrError::missing(source, target, "thinking", &format!("{path}.thinking"))
            })?;
            let signature = required_claude_thinking_signature(
                block.signature.as_deref(),
                source,
                target,
                &format!("{path}.signature"),
            )?;
            let raw = object([
                ("type".to_owned(), JsonData::String("thinking".to_owned())),
                ("thinking".to_owned(), JsonData::String(thinking)),
                ("signature".to_owned(), JsonData::String(signature)),
                ("extra".to_owned(), JsonData::Object(block.extra)),
            ]);
            push_item(
                envelope,
                opaque_item(
                    role,
                    provider_state(
                        "anthropic",
                        "thinking",
                        raw,
                        OpaqueStateProvenance::Authentic,
                        Some(model.to_owned()),
                    ),
                    source,
                    &path,
                ),
                source,
                target,
                &path,
            )?;
        }
        "redacted_thinking" => {
            let data = block.data.or(block.content).ok_or_else(|| {
                DirectIrError::missing(source, target, "redacted_thinking.data", &path)
            })?;
            push_item(
                envelope,
                opaque_item(
                    role,
                    provider_state(
                        "anthropic",
                        "redacted_thinking",
                        object([
                            (
                                "type".to_owned(),
                                JsonData::String("redacted_thinking".to_owned()),
                            ),
                            ("data".to_owned(), data),
                            ("extra".to_owned(), JsonData::Object(block.extra)),
                        ]),
                        OpaqueStateProvenance::Authentic,
                        Some(model.to_owned()),
                    ),
                    source,
                    &path,
                ),
                source,
                target,
                &path,
            )?;
        }
        "tool_use" => {
            let id = block
                .id
                .ok_or_else(|| DirectIrError::missing(source, target, "tool_use.id", &path))?;
            let call_id = authentic_id(
                Some(id),
                source,
                target,
                "function_call_id",
                &format!("{path}.id"),
            )?
            .ok_or_else(|| DirectIrError::missing(source, target, "tool_use.id", &path))?;
            let name = block
                .name
                .ok_or_else(|| DirectIrError::missing(source, target, "tool_use.name", &path))?;
            let mut function = FunctionData::call(name, block.input.unwrap_or_else(|| object([])));
            function.extensions = block.extra;
            let mut item = Item::tool_call(
                call_id,
                vec![Part::function(function)],
                Provenance::new(source),
            );
            item.provenance.source_path = Some(path.clone());
            push_item(envelope, item, source, target, &path)?;
        }
        "tool_result" => {
            let id = block.tool_use_id.ok_or_else(|| {
                DirectIrError::missing(source, target, "tool_result.tool_use_id", &path)
            })?;
            let call_id = authentic_id(
                Some(id),
                source,
                target,
                "function_call_id",
                &format!("{path}.tool_use_id"),
            )?
            .ok_or_else(|| {
                DirectIrError::missing(source, target, "tool_result.tool_use_id", &path)
            })?;
            let mut function =
                FunctionData::result(block.name, block.content.unwrap_or(JsonData::Null));
            function.extensions = block.extra;
            let mut item = Item::tool_result(
                call_id,
                vec![Part::function(function)],
                Provenance::new(source),
            );
            item.provenance.source_path = Some(path.clone());
            push_item(envelope, item, source, target, &path)?;
        }
        "image" => {
            let media_source = block
                .source
                .ok_or_else(|| DirectIrError::missing(source, target, "image.source", &path))?;
            let mut media = Media::new(MediaKind::Image);
            match media_source.kind.as_str() {
                "url" => media.uri = Some(media_source.data),
                "base64" => {
                    media.mime_type = Some(media_source.media_type);
                    media.data = Some(JsonData::String(media_source.data));
                }
                _ => {
                    return Err(DirectIrError::unsupported(
                        source,
                        target,
                        "image_source",
                        &path,
                    ));
                }
            }
            media.extensions = block.extra;
            push_item(
                envelope,
                media_item(role, media, source, &path),
                source,
                target,
                &path,
            )?;
        }
        _ => {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "claude_block",
                &path,
            ));
        }
    }
    Ok(())
}

fn claude_tool_choice_to_ir(
    choice: Option<&ClaudeToolChoice>,
    source: Protocol,
    target: Protocol,
) -> Result<ToolChoice, DirectIrError> {
    let Some(choice) = choice else {
        return Ok(ToolChoice::Auto);
    };
    match choice.kind.as_str() {
        "auto" => Ok(ToolChoice::Auto),
        "any" => Ok(ToolChoice::Required),
        "tool" => Ok(ToolChoice::Named {
            name: choice.name.clone().ok_or_else(|| {
                DirectIrError::missing(source, target, "tool_choice.name", "tool_choice")
            })?,
        }),
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "tool_choice",
            "tool_choice.type",
        )),
    }
}

fn reject_claude_request_nested_extra(
    request: &ClaudeRequest,
    source: Protocol,
    target: Protocol,
) -> Result<(), DirectIrError> {
    reject_wire_extra(source, target, &request.extra, "")?;
    if let Some(StringOrParts::Parts(parts)) = request.system.as_ref() {
        for (index, block) in parts.iter().enumerate() {
            if let Some(media_source) = block.source.as_ref() {
                reject_wire_extra(
                    source,
                    target,
                    &media_source.extra,
                    &format!("system.parts[{index}].source"),
                )?;
            }
        }
    }
    for (message_index, message) in request.messages.iter().enumerate() {
        let message_path = format!("messages[{message_index}]");
        reject_wire_extra(source, target, &message.extra, &message_path)?;
        if let StringOrParts::Parts(parts) = &message.content {
            for (block_index, block) in parts.iter().enumerate() {
                if let Some(media_source) = block.source.as_ref() {
                    reject_wire_extra(
                        source,
                        target,
                        &media_source.extra,
                        &format!("{message_path}.content[{block_index}].source"),
                    )?;
                }
            }
        }
    }
    for (index, tool) in request.tools.iter().enumerate() {
        reject_wire_extra(source, target, &tool.extra, &format!("tools[{index}]"))?;
    }
    if let Some(tool_choice) = request.tool_choice.as_ref() {
        reject_wire_extra(source, target, &tool_choice.extra, "tool_choice")?;
    }
    Ok(())
}

/// Decodes a Claude Messages request into an ordered IR Envelope.
pub fn claude_request_to_envelope_v2(
    request: ClaudeRequest,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let model = request.model.clone();
    let source = Protocol::Claude;
    let target = Protocol::Claude;
    reject_claude_request_nested_extra(&request, source, target)?;
    let mut envelope = Envelope::new(source, model.clone());
    envelope.controls.max_output_tokens = Some(request.max_tokens);
    envelope.controls.temperature = request.temperature;
    envelope.controls.stream = request.stream;
    if let Some(system) = request.system {
        match system {
            StringOrParts::String(text) => {
                envelope
                    .extensions
                    .insert("claude.system_string".to_owned(), JsonData::Bool(true));
                push_item(
                    &mut envelope,
                    text_item(Role::System, text, source, "system"),
                    source,
                    target,
                    "system",
                )?;
            }
            StringOrParts::Parts(parts) => {
                for (index, block) in parts.into_iter().enumerate() {
                    claude_block_to_items(
                        block,
                        Role::System,
                        0,
                        index,
                        &mut envelope,
                        source,
                        target,
                        &model,
                    )?;
                }
            }
        }
    }
    for (message_index, message) in request.messages.into_iter().enumerate() {
        let role = role_from_wire(
            Some(&message.role),
            source,
            target,
            &format!("messages[{message_index}].role"),
        )?;
        match message.content {
            StringOrParts::String(text) => {
                push_item(
                    &mut envelope,
                    text_item(
                        if role == Role::Tool { Role::Tool } else { role },
                        text,
                        source,
                        &format!("messages[{message_index}].content"),
                    ),
                    source,
                    target,
                    &format!("messages[{message_index}].content"),
                )?;
            }
            StringOrParts::Parts(parts) => {
                let is_tool_result = parts.iter().any(|part| part.kind == "tool_result");
                let role = if is_tool_result { Role::Tool } else { role };
                for (block_index, block) in parts.into_iter().enumerate() {
                    claude_block_to_items(
                        block,
                        role.clone(),
                        message_index,
                        block_index,
                        &mut envelope,
                        source,
                        target,
                        &model,
                    )?;
                }
            }
        }
    }
    envelope.tools = request
        .tools
        .into_iter()
        .map(|tool| Tool {
            kind: ToolKind::Function,
            name: Some(tool.name),
            description: tool.description,
            input_schema: Some(tool.input_schema),
            extensions: empty_extensions(),
        })
        .collect();
    envelope.tool_choice = claude_tool_choice_to_ir(request.tool_choice.as_ref(), source, target)?;
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

fn opaque_state_to_claude_block(
    state: &OpaqueProviderState,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<ClaudeContentBlock, DirectIrError> {
    if state.provenance == OpaqueStateProvenance::Synthetic {
        return Err(DirectIrError::unsupported(
            source,
            target,
            "synthetic_claude_opaque_state",
            path,
        ));
    }
    let object = json_object(&state.raw).ok_or_else(|| {
        DirectIrError::new(
            source,
            target,
            "opaque_state",
            path,
            DirectIrReason::InvalidOpaqueState,
        )
    })?;
    let extra = object
        .get("extra")
        .and_then(json_object)
        .cloned()
        .unwrap_or_default();
    match state.kind.as_str() {
        "thinking" => {
            let signature = required_claude_thinking_signature(
                object.get("signature").and_then(json_string),
                source,
                target,
                &format!("{path}.signature"),
            )?;
            Ok(ClaudeContentBlock {
                kind: "thinking".to_owned(),
                text: None,
                thinking: object
                    .get("thinking")
                    .and_then(json_string)
                    .map(str::to_owned),
                signature: Some(signature),
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra,
            })
        }
        "redacted_thinking" => Ok(ClaudeContentBlock {
            kind: "redacted_thinking".to_owned(),
            text: None,
            thinking: None,
            signature: None,
            data: object.get("data").cloned(),
            id: None,
            name: None,
            input: None,
            tool_use_id: None,
            content: None,
            source: None,
            extra,
        }),
        _ => Err(DirectIrError::unsupported(
            source,
            target,
            "claude_opaque_state",
            path,
        )),
    }
}

fn claude_block_from_item(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<ClaudeContentBlock, DirectIrError> {
    let part = single_item_part(item, source, target, path, "part")?;
    match &part.kind {
        PartKind::Text => Ok(ClaudeContentBlock {
            kind: "text".to_owned(),
            text: Some(part.text.clone().unwrap_or_default()),
            thinking: None,
            signature: None,
            data: None,
            id: None,
            name: None,
            input: None,
            tool_use_id: None,
            content: None,
            source: None,
            extra: part.extensions.clone(),
        }),
        PartKind::Media => {
            let media = part.media.as_ref().ok_or_else(|| {
                DirectIrError::new(source, target, "media", path, DirectIrReason::InvalidShape)
            })?;
            let source = if let Some(uri) = media.uri.as_ref() {
                ClaudeMediaSource {
                    kind: "url".to_owned(),
                    media_type: media
                        .mime_type
                        .clone()
                        .unwrap_or_else(|| "image/*".to_owned()),
                    data: uri.clone(),
                    extra: BTreeMap::new(),
                }
            } else if let Some(JsonData::String(data)) = media.data.as_ref() {
                ClaudeMediaSource {
                    kind: "base64".to_owned(),
                    media_type: media
                        .mime_type
                        .clone()
                        .unwrap_or_else(|| "application/octet-stream".to_owned()),
                    data: data.clone(),
                    extra: BTreeMap::new(),
                }
            } else {
                return Err(DirectIrError::unsupported(source, target, "media", path));
            };
            Ok(ClaudeContentBlock {
                kind: "image".to_owned(),
                text: None,
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: Some(source),
                extra: media.extensions.clone(),
            })
        }
        PartKind::Function => {
            let function = part.function.as_ref().ok_or_else(|| {
                DirectIrError::new(
                    source,
                    target,
                    "function",
                    path,
                    DirectIrReason::InvalidShape,
                )
            })?;
            let call_id = item
                .call_id
                .as_ref()
                .ok_or_else(|| DirectIrError::missing(source, target, "function_call_id", path))?;
            match &function.kind {
                FunctionKind::Call => Ok(ClaudeContentBlock {
                    kind: "tool_use".to_owned(),
                    text: None,
                    thinking: None,
                    signature: None,
                    data: None,
                    id: Some(id_value(call_id)?),
                    name: function.name.clone(),
                    input: Some(function.arguments.clone().unwrap_or_else(|| object([]))),
                    tool_use_id: None,
                    content: None,
                    source: None,
                    extra: function.extensions.clone(),
                }),
                FunctionKind::Result => Ok(ClaudeContentBlock {
                    kind: "tool_result".to_owned(),
                    text: None,
                    thinking: None,
                    signature: None,
                    data: None,
                    id: None,
                    name: function.name.clone(),
                    input: None,
                    tool_use_id: Some(id_value(call_id)?),
                    content: Some(function.output.clone().unwrap_or(JsonData::Null)),
                    source: None,
                    extra: function.extensions.clone(),
                }),
                FunctionKind::Unknown(_) => Err(DirectIrError::unsupported(
                    source,
                    target,
                    "function_kind",
                    path,
                )),
            }
        }
        PartKind::Opaque => {
            let state = part.opaque.as_ref().ok_or_else(|| {
                DirectIrError::new(
                    source,
                    target,
                    "opaque_state",
                    path,
                    DirectIrReason::InvalidOpaqueState,
                )
            })?;
            if state.provider != "anthropic" {
                return Err(DirectIrError::unsupported(
                    source,
                    target,
                    "opaque_reasoning",
                    path,
                ));
            }
            opaque_state_to_claude_block(state, source, target, path)
        }
        PartKind::Unknown(_) => Err(DirectIrError::unsupported(
            source,
            target,
            "part_kind",
            path,
        )),
    }
}

/// Encodes an ordered Envelope as a Claude Messages request.
pub fn envelope_to_claude_request_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::Claude;
    record_request_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    let mut system = Vec::new();
    let mut messages = Vec::new();
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        let path = format!("items[{index}]");
        let foreign_opaque_reasoning = item
            .ordered_parts()
            .first()
            .and_then(|part| part.opaque.as_ref())
            .is_some_and(|state| state.provider != "anthropic");
        if matches!(&item.kind, ItemKind::Reasoning) && foreign_opaque_reasoning {
            record_loss(
                &mut envelope,
                LossCode::LossOpaqueReasoning,
                Some(Feature::OpaqueReasoningSignature),
                &path,
                "foreign provider opaque reasoning has no legal Claude signature representation",
            );
            continue;
        }
        let block = if matches!(&item.kind, ItemKind::Reasoning)
            && item
                .ordered_parts()
                .first()
                .and_then(|part| part.opaque.as_ref())
                .is_none()
        {
            let part = single_item_part(&item, source, target, &path, "reasoning")?;
            let text = part.text.clone().unwrap_or_default();
            record_loss(
                &mut envelope,
                LossCode::LossOpaqueReasoning,
                Some(Feature::ReasoningSummary),
                &path,
                "ordinary reasoning emitted as Claude text without fabricated thinking signature",
            );
            ClaudeContentBlock {
                kind: "text".to_owned(),
                text: Some(text),
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra: BTreeMap::new(),
            }
        } else {
            claude_block_from_item(&item, source, target, &path)?
        };
        if matches!(&item.role, Role::System | Role::Developer) {
            system.push(block);
        } else {
            messages.push(ClaudeMessage {
                role: if item.role == Role::Tool {
                    "user".to_owned()
                } else if item.role == Role::Model {
                    "assistant".to_owned()
                } else {
                    chat_role(item.role.clone()).to_owned()
                },
                content: StringOrParts::Parts(vec![block]),
                extra: BTreeMap::new(),
            });
        }
    }
    let mut tools = Vec::new();
    for (index, tool) in envelope.tools.iter().enumerate() {
        let name = tool_name_for_target(tool, index, source, target)?;
        tools.push(ClaudeTool {
            name,
            description: tool.description.clone(),
            input_schema: tool.input_schema.clone().unwrap_or_else(|| object([])),
            extra: BTreeMap::new(),
        });
    }
    let tool_choice = match envelope.tool_choice.clone() {
        ToolChoice::Auto => Some(ClaudeToolChoice {
            kind: "auto".to_owned(),
            name: None,
            extra: BTreeMap::new(),
        }),
        ToolChoice::Required => Some(ClaudeToolChoice {
            kind: "any".to_owned(),
            name: None,
            extra: BTreeMap::new(),
        }),
        ToolChoice::Named { name } => Some(ClaudeToolChoice {
            kind: "tool".to_owned(),
            name: Some(name),
            extra: BTreeMap::new(),
        }),
        ToolChoice::None => {
            record_loss(
                &mut envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::ToolChoiceAuto),
                "tool_choice",
                "Claude has no none tool-choice mode; auto is emitted explicitly with loss",
            );
            Some(ClaudeToolChoice {
                kind: "auto".to_owned(),
                name: None,
                extra: BTreeMap::new(),
            })
        }
        ToolChoice::Provider { .. } => {
            return Err(DirectIrError::unsupported(
                source,
                target,
                "tool_choice",
                "tool_choice",
            ));
        }
    };
    let max_tokens = match envelope.controls.max_output_tokens {
        Some(value) => value,
        None => {
            record_loss(
                &mut envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "controls.max_output_tokens",
                "Claude requires max_tokens; compatibility default 1 is synthesized",
            );
            1
        }
    };
    let output = ClaudeRequest {
        model: envelope.model.clone(),
        max_tokens,
        stream: envelope.controls.stream,
        system: (!system.is_empty()).then_some(StringOrParts::Parts(system)),
        messages,
        temperature: envelope.controls.temperature,
        tools,
        tool_choice,
        extra: BTreeMap::new(),
    };
    Ok(finish(output, envelope, source, target))
}

fn token_details_to_json(details: &TokenDetails) -> JsonData {
    let mut value = BTreeMap::from([
        (
            "cached_tokens".to_owned(),
            JsonData::Number(details.cached_tokens.into()),
        ),
        (
            "cached_creation_tokens".to_owned(),
            JsonData::Number(details.cached_creation_tokens.into()),
        ),
        (
            "cache_write_tokens".to_owned(),
            JsonData::Number(details.cache_write_tokens.into()),
        ),
        (
            "text_tokens".to_owned(),
            JsonData::Number(details.text_tokens.into()),
        ),
        (
            "audio_tokens".to_owned(),
            JsonData::Number(details.audio_tokens.into()),
        ),
        (
            "image_tokens".to_owned(),
            JsonData::Number(details.image_tokens.into()),
        ),
        (
            "reasoning_tokens".to_owned(),
            JsonData::Number(details.reasoning_tokens.into()),
        ),
    ]);
    insert_extra_fields(
        &mut value,
        &details.extra,
        &[
            "cached_tokens",
            "cached_creation_tokens",
            "cache_write_tokens",
            "text_tokens",
            "audio_tokens",
            "image_tokens",
            "reasoning_tokens",
        ],
    );
    JsonData::Object(value)
}

fn wire_usage_to_json(usage: &WireUsage) -> JsonData {
    let mut value = BTreeMap::new();
    value.insert(
        "prompt_tokens".to_owned(),
        JsonData::Number(usage.prompt_tokens.into()),
    );
    value.insert(
        "completion_tokens".to_owned(),
        JsonData::Number(usage.completion_tokens.into()),
    );
    value.insert(
        "total_tokens".to_owned(),
        JsonData::Number(usage.total_tokens.into()),
    );
    value.insert(
        "input_tokens".to_owned(),
        JsonData::Number(usage.input_tokens.into()),
    );
    value.insert(
        "output_tokens".to_owned(),
        JsonData::Number(usage.output_tokens.into()),
    );
    if let Some(details) = usage.prompt_tokens_details.as_ref() {
        value.insert(
            "prompt_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    if let Some(details) = usage.completion_tokens_details.as_ref() {
        value.insert(
            "completion_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    if let Some(details) = usage.input_tokens_details.as_ref() {
        value.insert(
            "input_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    value.insert(
        "claude_cache_creation_5_m_tokens".to_owned(),
        JsonData::Number(usage.claude_cache_creation_5_m_tokens.into()),
    );
    value.insert(
        "claude_cache_creation_1_h_tokens".to_owned(),
        JsonData::Number(usage.claude_cache_creation_1_h_tokens.into()),
    );
    if let Some(source) = usage.usage_source.as_ref() {
        value.insert("usage_source".to_owned(), JsonData::String(source.clone()));
    }
    if let Some(semantic) = usage.usage_semantic.as_ref() {
        value.insert(
            "usage_semantic".to_owned(),
            JsonData::String(semantic.clone()),
        );
    }
    if let Some(billing) = usage.billing_usage.as_ref() {
        value.insert("billing_usage".to_owned(), billing_usage_to_json(billing));
    }
    insert_extra_fields(
        &mut value,
        &usage.extra,
        &[
            "prompt_tokens",
            "completion_tokens",
            "total_tokens",
            "input_tokens",
            "output_tokens",
            "prompt_tokens_details",
            "completion_tokens_details",
            "input_tokens_details",
            "claude_cache_creation_5_m_tokens",
            "claude_cache_creation_1_h_tokens",
            "usage_source",
            "usage_semantic",
            "billing_usage",
        ],
    );
    JsonData::Object(value)
}

fn billing_usage_to_json(value: &BillingUsage) -> JsonData {
    let mut entries = BTreeMap::new();
    entries.insert("source".to_owned(), JsonData::String(value.source.clone()));
    entries.insert(
        "semantic".to_owned(),
        JsonData::String(value.semantic.clone()),
    );
    if let Some(usage) = value.openai_usage.as_ref() {
        entries.insert("openai_usage".to_owned(), wire_usage_to_json(usage));
    }
    if let Some(usage) = value.claude_usage.as_ref() {
        let mut usage_entries = BTreeMap::from([
            (
                "input_tokens".to_owned(),
                JsonData::Number(usage.input_tokens.into()),
            ),
            (
                "output_tokens".to_owned(),
                JsonData::Number(usage.output_tokens.into()),
            ),
            (
                "cache_read_input_tokens".to_owned(),
                JsonData::Number(usage.cache_read_input_tokens.into()),
            ),
            (
                "cache_creation_input_tokens".to_owned(),
                JsonData::Number(usage.cache_creation_input_tokens.into()),
            ),
            (
                "claude_cache_creation_5_m_tokens".to_owned(),
                JsonData::Number(usage.claude_cache_creation_5_m_tokens.into()),
            ),
            (
                "claude_cache_creation_1_h_tokens".to_owned(),
                JsonData::Number(usage.claude_cache_creation_1_h_tokens.into()),
            ),
        ]);
        if let Some(billing) = usage.billing_usage.as_ref() {
            usage_entries.insert("billing_usage".to_owned(), billing_usage_to_json(billing));
        }
        insert_extra_fields(
            &mut usage_entries,
            &usage.extra,
            &[
                "input_tokens",
                "output_tokens",
                "cache_read_input_tokens",
                "cache_creation_input_tokens",
                "claude_cache_creation_5_m_tokens",
                "claude_cache_creation_1_h_tokens",
                "billing_usage",
            ],
        );
        entries.insert("claude_usage".to_owned(), JsonData::Object(usage_entries));
    }
    if let Some(usage) = value.gemini_usage_metadata.as_ref() {
        let mut usage_entries = BTreeMap::from([
            (
                "prompt_token_count".to_owned(),
                JsonData::Number(usage.prompt_token_count.into()),
            ),
            (
                "candidates_token_count".to_owned(),
                JsonData::Number(usage.candidates_token_count.into()),
            ),
            (
                "thoughts_token_count".to_owned(),
                JsonData::Number(usage.thoughts_token_count.into()),
            ),
            (
                "total_token_count".to_owned(),
                JsonData::Number(usage.total_token_count.into()),
            ),
            (
                "cached_content_token_count".to_owned(),
                JsonData::Number(usage.cached_content_token_count.into()),
            ),
            (
                "tool_use_prompt_token_count".to_owned(),
                JsonData::Number(usage.tool_use_prompt_token_count.into()),
            ),
        ]);
        if let Some(details) = usage.prompt_tokens_details.as_ref() {
            usage_entries.insert("promptTokensDetails".to_owned(), details.clone());
        }
        if let Some(details) = usage.candidates_tokens_details.as_ref() {
            usage_entries.insert("candidatesTokensDetails".to_owned(), details.clone());
        }
        if let Some(details) = usage.tool_use_prompt_tokens_details.as_ref() {
            usage_entries.insert("toolUsePromptTokensDetails".to_owned(), details.clone());
        }
        if let Some(billing) = usage.billing_usage.as_ref() {
            usage_entries.insert("billing_usage".to_owned(), billing_usage_to_json(billing));
        }
        insert_extra_fields(
            &mut usage_entries,
            &usage.extra,
            &[
                "promptTokenCount",
                "candidatesTokenCount",
                "thoughtsTokenCount",
                "totalTokenCount",
                "cachedContentTokenCount",
                "toolUsePromptTokenCount",
                "promptTokensDetails",
                "candidatesTokensDetails",
                "toolUsePromptTokensDetails",
                "billing_usage",
            ],
        );
        entries.insert(
            "gemini_usage_metadata".to_owned(),
            JsonData::Object(usage_entries),
        );
    }
    insert_extra_fields(
        &mut entries,
        &value.extra,
        &[
            "source",
            "semantic",
            "openai_usage",
            "claude_usage",
            "gemini_usage_metadata",
        ],
    );
    JsonData::Object(entries)
}

fn cache_extensions_from_wire(usage: &WireUsage) -> BTreeMap<String, JsonData> {
    let mut extensions = BTreeMap::new();
    if let Some(details) = usage.prompt_tokens_details.as_ref() {
        extensions.insert(
            "openai.prompt_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    if let Some(details) = usage.completion_tokens_details.as_ref() {
        extensions.insert(
            "openai.completion_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    if let Some(details) = usage.input_tokens_details.as_ref() {
        extensions.insert(
            "openai.input_tokens_details".to_owned(),
            token_details_to_json(details),
        );
    }
    if usage.claude_cache_creation_5_m_tokens != 0 {
        extensions.insert(
            "claude.cache_creation_5m".to_owned(),
            JsonData::Number(usage.claude_cache_creation_5_m_tokens.into()),
        );
    }
    if usage.claude_cache_creation_1_h_tokens != 0 {
        extensions.insert(
            "claude.cache_creation_1h".to_owned(),
            JsonData::Number(usage.claude_cache_creation_1_h_tokens.into()),
        );
    }
    extensions
}

fn usage_extensions_from_wire(usage: &WireUsage) -> BTreeMap<String, JsonData> {
    usage
        .extra
        .iter()
        .map(|(key, value)| (format!("openai.usage.{key}"), value.clone()))
        .collect()
}

fn usage_from_wire(usage: &WireUsage) -> SemanticUsage {
    let input_tokens = if usage.input_tokens != 0 {
        usage.input_tokens
    } else {
        usage.prompt_tokens
    };
    let output_tokens = if usage.output_tokens != 0 {
        usage.output_tokens
    } else {
        usage.completion_tokens
    };
    let total_tokens = if usage.total_tokens != 0 {
        usage.total_tokens
    } else {
        input_tokens.saturating_add(output_tokens)
    };
    let cache = CacheUsage {
        read_input_tokens: usage
            .input_tokens_details
            .as_ref()
            .map(|details| details.cached_tokens)
            .or_else(|| {
                usage
                    .prompt_tokens_details
                    .as_ref()
                    .map(|details| details.cached_tokens)
            })
            .unwrap_or(0),
        write_input_tokens: usage
            .input_tokens_details
            .as_ref()
            .map(|details| details.cache_write_tokens)
            .unwrap_or(0),
        creation_input_tokens: usage
            .claude_cache_creation_5_m_tokens
            .saturating_add(usage.claude_cache_creation_1_h_tokens),
        extensions: cache_extensions_from_wire(usage),
    };
    let reasoning_tokens = usage
        .completion_tokens_details
        .as_ref()
        .map(|details| details.reasoning_tokens)
        .unwrap_or(0);
    let billing = usage
        .billing_usage
        .as_ref()
        .map(|value| SemanticBillingUsage {
            source: Some(value.source.clone()),
            semantic: Some(value.semantic.clone()),
            input_tokens,
            output_tokens,
            cached_input_tokens: cache.read_input_tokens,
            total_tokens,
            cost: None,
            extensions: BTreeMap::from([(
                "billing.provider_payload".to_owned(),
                billing_usage_to_json(value),
            )]),
        });
    let mut extensions = usage_extensions_from_wire(usage);
    extensions.extend(cache_extensions_from_wire(usage));
    if let Some(source) = usage.usage_source.as_ref() {
        extensions.insert("usage_source".to_owned(), JsonData::String(source.clone()));
    }
    if let Some(semantic) = usage.usage_semantic.as_ref() {
        extensions.insert(
            "usage_semantic".to_owned(),
            JsonData::String(semantic.clone()),
        );
    }
    SemanticUsage {
        input_tokens,
        output_tokens,
        total_tokens,
        reasoning_tokens,
        cache,
        billing,
        extensions,
    }
}

fn token_details_from_json(value: &JsonData) -> Option<TokenDetails> {
    let object = json_object(value)?;
    let number = |name: &str| object.get(name).and_then(number_u64).unwrap_or(0);
    let mut extra = BTreeMap::new();
    for (key, value) in object {
        if ![
            "cached_tokens",
            "cached_creation_tokens",
            "cache_write_tokens",
            "text_tokens",
            "audio_tokens",
            "image_tokens",
            "reasoning_tokens",
        ]
        .contains(&key.as_str())
        {
            extra.insert(key.clone(), value.clone());
        }
    }
    Some(TokenDetails {
        cached_tokens: number("cached_tokens"),
        cached_creation_tokens: number("cached_creation_tokens"),
        cache_write_tokens: number("cache_write_tokens"),
        text_tokens: number("text_tokens"),
        audio_tokens: number("audio_tokens"),
        image_tokens: number("image_tokens"),
        reasoning_tokens: number("reasoning_tokens"),
        extra,
    })
}

fn wire_usage_without_billing(usage: &SemanticUsage) -> WireUsage {
    let prompt_details = TokenDetails {
        cached_tokens: usage.cache.read_input_tokens,
        cached_creation_tokens: usage.cache.creation_input_tokens,
        cache_write_tokens: usage.cache.write_input_tokens,
        reasoning_tokens: usage.reasoning_tokens,
        ..TokenDetails::default()
    };
    let completion_details = TokenDetails {
        reasoning_tokens: usage.reasoning_tokens,
        ..TokenDetails::default()
    };
    let prompt_details = usage
        .extensions
        .get("openai.prompt_tokens_details")
        .and_then(token_details_from_json)
        .unwrap_or(prompt_details);
    let completion_details = usage
        .extensions
        .get("openai.completion_tokens_details")
        .and_then(token_details_from_json)
        .unwrap_or(completion_details);
    let input_details = usage
        .extensions
        .get("openai.input_tokens_details")
        .and_then(token_details_from_json)
        .unwrap_or_else(|| prompt_details.clone());
    WireUsage {
        prompt_tokens: usage.input_tokens,
        completion_tokens: usage.output_tokens,
        total_tokens: usage.total_tokens,
        input_tokens: usage.input_tokens,
        output_tokens: usage.output_tokens,
        prompt_tokens_details: Some(prompt_details),
        completion_tokens_details: Some(completion_details),
        input_tokens_details: Some(input_details),
        claude_cache_creation_5_m_tokens: usage
            .cache
            .extensions
            .get("claude.cache_creation_5m")
            .and_then(number_u64)
            .unwrap_or(0),
        claude_cache_creation_1_h_tokens: usage
            .cache
            .extensions
            .get("claude.cache_creation_1h")
            .and_then(number_u64)
            .unwrap_or(0),
        usage_source: usage
            .billing
            .as_ref()
            .and_then(|value| value.source.clone())
            .or_else(|| {
                usage
                    .extensions
                    .get("usage_source")
                    .and_then(json_string)
                    .map(str::to_owned)
            }),
        usage_semantic: usage
            .billing
            .as_ref()
            .and_then(|value| value.semantic.clone())
            .or_else(|| {
                usage
                    .extensions
                    .get("usage_semantic")
                    .and_then(json_string)
                    .map(str::to_owned)
            }),
        billing_usage: None,
        extra: extensions_with_prefix(&usage.extensions, "openai.usage."),
    }
}

fn billing_extra_from_semantic(usage: &SemanticUsage) -> BTreeMap<String, JsonData> {
    usage
        .billing
        .as_ref()
        .and_then(|billing| billing.extensions.get("billing.provider_payload"))
        .and_then(json_object)
        .map(|payload| {
            let mut extra = BTreeMap::new();
            for (key, value) in payload {
                if ![
                    "source",
                    "semantic",
                    "openai_usage",
                    "claude_usage",
                    "gemini_usage_metadata",
                ]
                .contains(&key.as_str())
                {
                    extra.insert(key.clone(), value.clone());
                }
            }
            extra
        })
        .unwrap_or_default()
}

fn billing_usage_for_target(usage: &SemanticUsage, target: Protocol) -> Option<Box<BillingUsage>> {
    let billing = usage.billing.as_ref()?;
    let mut output = BillingUsage {
        source: billing.source.clone().unwrap_or_else(|| "relay".to_owned()),
        semantic: billing
            .semantic
            .clone()
            .unwrap_or_else(|| "semantic_usage".to_owned()),
        openai_usage: None,
        claude_usage: None,
        gemini_usage_metadata: None,
        extra: billing_extra_from_semantic(usage),
    };
    match target {
        Protocol::OpenAi | Protocol::OpenAiResponses => {
            output.openai_usage = Some(Box::new(wire_usage_without_billing(usage)));
        }
        Protocol::Claude => {
            output.claude_usage = Some(ClaudeUsage {
                input_tokens: usage.input_tokens,
                output_tokens: usage.output_tokens,
                cache_read_input_tokens: usage.cache.read_input_tokens,
                cache_creation_input_tokens: usage.cache.creation_input_tokens,
                claude_cache_creation_5_m_tokens: usage
                    .cache
                    .extensions
                    .get("claude.cache_creation_5m")
                    .and_then(number_u64)
                    .unwrap_or(0),
                claude_cache_creation_1_h_tokens: usage
                    .cache
                    .extensions
                    .get("claude.cache_creation_1h")
                    .and_then(number_u64)
                    .unwrap_or(0),
                billing_usage: None,
                extra: extensions_with_prefix(&usage.extensions, "claude.usage."),
            });
        }
        Protocol::Gemini => {
            output.gemini_usage_metadata = Some(gemini_usage_without_billing(usage));
        }
    }
    Some(Box::new(output))
}

fn usage_to_wire(usage: &SemanticUsage, target: Protocol) -> WireUsage {
    let mut output = wire_usage_without_billing(usage);
    output.billing_usage = billing_usage_for_target(usage, target);
    output
}

fn record_usage_projection_losses(envelope: &mut Envelope, target: Protocol) {
    let Some(usage) = envelope.usage.clone() else {
        return;
    };
    let supported_prefix = match target {
        Protocol::OpenAi | Protocol::OpenAiResponses => "openai.",
        Protocol::Claude => "claude.",
        Protocol::Gemini => "gemini.",
    };
    let unsupported = usage
        .extensions
        .keys()
        .filter(|key| {
            !key.starts_with(supported_prefix) && *key != "usage_source" && *key != "usage_semantic"
        })
        .cloned()
        .collect::<Vec<_>>();
    for key in unsupported {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::Usage),
            format!("usage.extensions.{key}").as_str(),
            "usage extension retained in IR but not expressible on target wire",
        );
    }
    let unsupported_cache = usage
        .cache
        .extensions
        .keys()
        .filter(|key| {
            !key.starts_with(supported_prefix) && !key.starts_with("claude.cache_creation_")
        })
        .cloned()
        .collect::<Vec<_>>();
    let billing_loss = usage
        .billing
        .as_ref()
        .is_some_and(|billing| !billing.extensions.is_empty() || billing.cost.is_some());
    for key in unsupported_cache {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::CacheUsage),
            format!("usage.cache.extensions.{key}").as_str(),
            "cache usage extension retained in IR but not expressible on target wire",
        );
    }
    if billing_loss {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::Usage),
            "usage.billing",
            "billing extension/cost retained in IR but not expressible on target wire",
        );
    }
}

fn semantic_from_claude_usage(usage: &ClaudeUsage) -> SemanticUsage {
    let mut cache_extensions = BTreeMap::new();
    if usage.claude_cache_creation_5_m_tokens != 0 {
        cache_extensions.insert(
            "claude.cache_creation_5m".to_owned(),
            JsonData::Number(usage.claude_cache_creation_5_m_tokens.into()),
        );
    }
    if usage.claude_cache_creation_1_h_tokens != 0 {
        cache_extensions.insert(
            "claude.cache_creation_1h".to_owned(),
            JsonData::Number(usage.claude_cache_creation_1_h_tokens.into()),
        );
    }
    let billing = usage
        .billing_usage
        .as_ref()
        .map(|value| SemanticBillingUsage {
            source: Some(value.source.clone()),
            semantic: Some(value.semantic.clone()),
            input_tokens: usage.input_tokens,
            output_tokens: usage.output_tokens,
            cached_input_tokens: usage.cache_read_input_tokens,
            total_tokens: usage.input_tokens.saturating_add(usage.output_tokens),
            cost: None,
            extensions: BTreeMap::from([(
                "billing.provider_payload".to_owned(),
                billing_usage_to_json(value),
            )]),
        });
    SemanticUsage {
        input_tokens: usage.input_tokens,
        output_tokens: usage.output_tokens,
        total_tokens: usage.input_tokens.saturating_add(usage.output_tokens),
        reasoning_tokens: 0,
        cache: CacheUsage {
            read_input_tokens: usage.cache_read_input_tokens,
            write_input_tokens: 0,
            creation_input_tokens: usage.cache_creation_input_tokens,
            extensions: cache_extensions,
        },
        billing,
        extensions: usage
            .extra
            .iter()
            .map(|(key, value)| (format!("claude.usage.{key}"), value.clone()))
            .collect(),
    }
}

fn semantic_from_gemini_usage(usage: &GeminiUsage) -> SemanticUsage {
    let mut extensions = usage
        .extra
        .iter()
        .map(|(key, value)| (format!("gemini.usage.{key}"), value.clone()))
        .collect::<BTreeMap<_, _>>();
    if usage.tool_use_prompt_token_count != 0 {
        extensions.insert(
            "gemini.tool_use_prompt_token_count".to_owned(),
            JsonData::Number(usage.tool_use_prompt_token_count.into()),
        );
    }
    if let Some(details) = usage.prompt_tokens_details.as_ref() {
        extensions.insert("gemini.prompt_tokens_details".to_owned(), details.clone());
    }
    if let Some(details) = usage.candidates_tokens_details.as_ref() {
        extensions.insert(
            "gemini.candidates_tokens_details".to_owned(),
            details.clone(),
        );
    }
    if let Some(details) = usage.tool_use_prompt_tokens_details.as_ref() {
        extensions.insert(
            "gemini.tool_use_prompt_tokens_details".to_owned(),
            details.clone(),
        );
    }
    let billing = usage
        .billing_usage
        .as_ref()
        .map(|value| SemanticBillingUsage {
            source: Some(value.source.clone()),
            semantic: Some(value.semantic.clone()),
            input_tokens: usage.prompt_token_count,
            output_tokens: usage.candidates_token_count,
            cached_input_tokens: usage.cached_content_token_count,
            total_tokens: usage.total_token_count,
            cost: None,
            extensions: BTreeMap::from([(
                "billing.provider_payload".to_owned(),
                billing_usage_to_json(value),
            )]),
        });
    SemanticUsage {
        input_tokens: usage.prompt_token_count,
        output_tokens: usage.candidates_token_count,
        total_tokens: if usage.total_token_count != 0 {
            usage.total_token_count
        } else {
            usage
                .prompt_token_count
                .saturating_add(usage.candidates_token_count)
                .saturating_add(usage.thoughts_token_count)
        },
        reasoning_tokens: usage.thoughts_token_count,
        cache: CacheUsage {
            read_input_tokens: usage.cached_content_token_count,
            write_input_tokens: 0,
            creation_input_tokens: 0,
            extensions: BTreeMap::new(),
        },
        billing,
        extensions,
    }
}

fn openai_chat_message_to_response_envelope(
    message: OpenAiChatMessage,
    envelope: &mut Envelope,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    // The message helper uses the same ordered item mapping as requests.  A
    // response path is retained in the outer ledger, so no DTO is re-encoded
    // merely to change protocols.
    let index = envelope.ordered_items().len();
    chat_message_to_envelope(message, index, envelope, source, target).map_err(|error| {
        DirectIrError {
            path: if error.path == format!("messages[{index}]") {
                path.to_owned()
            } else {
                error.path
            },
            ..error
        }
    })
}

/// Decodes an OpenAI Chat non-stream response into an ordered IR Envelope.
pub fn openai_chat_response_to_envelope_v2(
    response: OpenAiChatResponse,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let source = Protocol::OpenAi;
    let target = Protocol::OpenAi;
    reject_wire_extra(source, target, &response.extra, "")?;
    let model = response.model.clone();
    let mut envelope = Envelope::new(source, model);
    envelope.extensions.insert(
        "openai.response_id".to_owned(),
        JsonData::String(response.id),
    );
    envelope.extensions.insert(
        "openai.response_object".to_owned(),
        JsonData::String(response.object),
    );
    envelope.extensions.insert(
        "openai.response_created".to_owned(),
        JsonData::Number(response.created.into()),
    );
    envelope.extensions.insert(
        "openai.choice_count".to_owned(),
        JsonData::Number(response.choices.len().into()),
    );
    for (index, choice) in response.choices.into_iter().enumerate() {
        reject_wire_extra(source, target, &choice.extra, &format!("choices[{index}]"))?;
        envelope.extensions.insert(
            format!("openai.choice[{index}].index"),
            JsonData::Number(choice.index.into()),
        );
        if let Some(reason) = choice.finish_reason {
            envelope.extensions.insert(
                format!("openai.choice[{index}].finish_reason"),
                JsonData::String(reason),
            );
        }
        openai_chat_message_to_response_envelope(
            choice.message,
            &mut envelope,
            source,
            target,
            &format!("choices[{index}].message"),
        )?;
    }
    if let Some(usage) = response.usage {
        envelope.usage = Some(usage_from_wire(&usage));
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

fn gemini_response_part_to_envelope(
    part: GeminiPart,
    candidate_index: usize,
    part_index: usize,
    envelope: &mut Envelope,
    links: &mut GeminiCallLinks,
    source: Protocol,
    target: Protocol,
    model: &str,
) -> Result<(), DirectIrError> {
    gemini_part_to_items(
        part,
        candidate_index,
        part_index,
        Role::Model,
        envelope,
        links,
        source,
        target,
        model,
    )
}

/// Decodes a Gemini non-stream response into an ordered IR Envelope.
pub fn gemini_response_to_envelope_v2(
    response: GeminiResponse,
    model: impl Into<String>,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let model = model.into();
    let source = Protocol::Gemini;
    let target = Protocol::Gemini;
    reject_wire_extra(source, target, &response.extra, "")?;
    let mut envelope = Envelope::new(source, model.clone());
    let candidate_count = response.candidates.len();
    envelope.extensions.insert(
        "gemini.candidate_count".to_owned(),
        JsonData::Number(candidate_count.into()),
    );
    let mut links = GeminiCallLinks::default();
    for (candidate_index, candidate) in response.candidates.into_iter().enumerate() {
        let candidate_path = format!("candidates[{candidate_index}]");
        reject_wire_extra(source, target, &candidate.extra, &candidate_path)?;
        reject_gemini_content_extra(
            &candidate.content,
            source,
            target,
            &format!("{candidate_path}.content"),
        )?;
        if let Some(index) = candidate.index {
            envelope.extensions.insert(
                format!("gemini.candidate[{candidate_index}].index"),
                JsonData::Number(index.into()),
            );
        }
        if let Some(reason) = candidate.finish_reason {
            envelope.extensions.insert(
                format!("gemini.candidate[{candidate_index}].finish_reason"),
                JsonData::String(reason),
            );
        }
        if let Some(safety_ratings) = candidate.safety_ratings {
            envelope.extensions.insert(
                format!("gemini.candidate[{candidate_index}].safety_ratings"),
                safety_ratings,
            );
        }
        for (part_index, part) in candidate.content.parts.into_iter().enumerate() {
            gemini_response_part_to_envelope(
                part,
                candidate_index,
                part_index,
                &mut envelope,
                &mut links,
                source,
                target,
                &model,
            )?;
        }
    }
    if let Some(usage) = response.usage_metadata {
        envelope.usage = Some(semantic_from_gemini_usage(&usage));
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

/// Explicitly named Gemini model variant for response adapters.
pub fn gemini_response_to_envelope_v2_for_model(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    gemini_response_to_envelope_v2(response, model)
}

fn reject_claude_response_nested_extra(
    response: &ClaudeResponse,
    source: Protocol,
    target: Protocol,
) -> Result<(), DirectIrError> {
    for (index, block) in response.content.iter().enumerate() {
        if let Some(media_source) = block.source.as_ref() {
            reject_wire_extra(
                source,
                target,
                &media_source.extra,
                &format!("response.content[{index}].source"),
            )?;
        }
    }
    Ok(())
}

/// Decodes a Claude non-stream response into an ordered IR Envelope.
pub fn claude_response_to_envelope_v2(
    response: ClaudeResponse,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    let source = Protocol::Claude;
    let target = Protocol::Claude;
    reject_wire_extra(source, target, &response.extra, "")?;
    reject_claude_response_nested_extra(&response, source, target)?;
    let model = response.model.clone();
    let mut envelope = Envelope::new(source, model.clone());
    envelope.extensions.insert(
        "claude.response_id".to_owned(),
        JsonData::String(response.id),
    );
    envelope.extensions.insert(
        "claude.response_type".to_owned(),
        JsonData::String(response.kind),
    );
    if let Some(reason) = response.stop_reason {
        envelope
            .extensions
            .insert("claude.stop_reason".to_owned(), JsonData::String(reason));
    }
    for (index, block) in response.content.into_iter().enumerate() {
        claude_block_to_items(
            block,
            Role::Assistant,
            0,
            index,
            &mut envelope,
            source,
            target,
            &model,
        )?;
    }
    if let Some(usage) = response.usage {
        envelope.usage = Some(semantic_from_claude_usage(&usage));
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

fn extension_string(envelope: &Envelope, key: &str) -> Option<String> {
    envelope
        .extensions
        .get(key)
        .and_then(json_string)
        .map(str::to_owned)
}

fn extension_i64(envelope: &Envelope, key: &str) -> Option<i64> {
    match envelope.extensions.get(key) {
        Some(JsonData::Number(value)) => value.as_i64(),
        _ => None,
    }
}

fn response_finish_reason(envelope: &Envelope, index: usize) -> Option<String> {
    extension_string(envelope, &format!("openai.choice[{index}].finish_reason"))
        .or_else(|| extension_string(envelope, "claude.stop_reason"))
        .or_else(|| extension_string(envelope, "gemini.candidate[0].finish_reason"))
        .or_else(|| {
            let status = extension_string(envelope, "openai.responses.status")?;
            Some(match status.as_str() {
                "incomplete" => extension_string(envelope, "openai.responses.incomplete_reason")
                    .map_or_else(
                        || "length".to_owned(),
                        |reason| match reason.as_str() {
                            "max_output_tokens" => "length".to_owned(),
                            "content_filter" => "content_filter".to_owned(),
                            _ => "other".to_owned(),
                        },
                    ),
                "failed" => "error".to_owned(),
                "cancelled" => "cancelled".to_owned(),
                _ if envelope
                    .ordered_items()
                    .iter()
                    .any(|item| matches!(&item.kind, ItemKind::ToolCall)) =>
                {
                    "tool_calls".to_owned()
                }
                _ => "stop".to_owned(),
            })
        })
}

fn openai_response_message_groups(
    envelope: &mut Envelope,
    source: Protocol,
    target: Protocol,
) -> Result<Vec<OpenAiChatMessage>, DirectIrError> {
    let mut groups = Vec::new();
    let items = envelope.ordered_items().to_vec();
    for (index, item) in items.iter().enumerate() {
        let is_bound_signature = matches!(&item.kind, ItemKind::Reasoning)
            && item
                .ordered_parts()
                .first()
                .and_then(|part| part.opaque.as_ref())
                .is_some_and(|state| {
                    state.provider == "google" && state.kind == "thought_signature"
                })
            && index > 0
            && matches!(&items[index - 1].kind, ItemKind::ToolCall)
            && items[index - 1].ordered_parts().len() == 1;
        if is_bound_signature {
            continue;
        }
        let bound_google_state = if matches!(&item.kind, ItemKind::ToolCall) {
            items
                .get(index.saturating_add(1))
                .filter(|next| next.ordered_parts().len() == 1)
                .and_then(|next| next.ordered_parts().first())
                .and_then(|part| part.opaque.as_ref())
                .filter(|state| state.provider == "google" && state.kind == "thought_signature")
        } else {
            None
        };
        let message = openai_message_from_item(
            item,
            source,
            target,
            &format!("items[{index}]"),
            bound_google_state,
        )?;
        let can_merge = groups.last().is_some_and(|last: &OpenAiChatMessage| {
            last.role == message.role && message.role != "tool"
        });
        if can_merge {
            let Some(last) = groups.last_mut() else {
                return Err(DirectIrError::new(
                    source,
                    target,
                    "response_order",
                    format!("items[{index}]"),
                    DirectIrReason::Mismatch,
                ));
            };
            merge_openai_messages(last, message, envelope, source, target, index)?;
        } else {
            groups.push(message);
        }
    }
    Ok(groups)
}

fn merge_openai_messages(
    target_message: &mut OpenAiChatMessage,
    incoming: OpenAiChatMessage,
    envelope: &mut Envelope,
    source: Protocol,
    target: Protocol,
    index: usize,
) -> Result<(), DirectIrError> {
    match (target_message.content.take(), incoming.content) {
        (None, content) => target_message.content = content,
        (Some(StringOrParts::String(left)), Some(StringOrParts::String(right))) => {
            target_message.content = Some(StringOrParts::Parts(vec![
                OpenAiChatContentPart {
                    kind: "text".to_owned(),
                    text: Some(left),
                    image_url: None,
                },
                OpenAiChatContentPart {
                    kind: "text".to_owned(),
                    text: Some(right),
                    image_url: None,
                },
            ]));
        }
        (Some(StringOrParts::String(left)), Some(StringOrParts::Parts(mut right))) => {
            right.insert(
                0,
                OpenAiChatContentPart {
                    kind: "text".to_owned(),
                    text: Some(left),
                    image_url: None,
                },
            );
            target_message.content = Some(StringOrParts::Parts(right));
        }
        (Some(StringOrParts::Parts(mut left)), Some(StringOrParts::String(right))) => {
            left.push(OpenAiChatContentPart {
                kind: "text".to_owned(),
                text: Some(right),
                image_url: None,
            });
            target_message.content = Some(StringOrParts::Parts(left));
        }
        (Some(StringOrParts::Parts(mut left)), Some(StringOrParts::Parts(right))) => {
            left.extend(right);
            target_message.content = Some(StringOrParts::Parts(left));
        }
        (Some(content), None) => target_message.content = Some(content),
    }
    if let Some(reasoning) = incoming.reasoning_content {
        match target_message.reasoning_content.as_mut() {
            Some(value) => {
                record_loss(
                    envelope,
                    LossCode::LossContentOrder,
                    Some(Feature::ReasoningSummary),
                    format!("items[{index}].reasoning_content").as_str(),
                    "adjacent reasoning fields were merged into one OpenAI message",
                );
                value.push_str(&reasoning);
            }
            None => target_message.reasoning_content = Some(reasoning),
        }
    }
    if target_message.name.is_none() {
        target_message.name = incoming.name.clone();
    } else if incoming.name.is_some() && incoming.name != target_message.name {
        record_loss(
            envelope,
            LossCode::LossContentOrder,
            Some(Feature::UnknownEventPassthrough),
            format!("items[{index}].name").as_str(),
            "adjacent Chat messages had different names and share one merged message",
        );
    }
    target_message.tool_calls.extend(incoming.tool_calls);
    let mut existing = target_message
        .extra_content
        .take()
        .unwrap_or(OpenAiExtraContent {
            google: None,
            anthropic: None,
        });
    if let Some(incoming) = incoming.extra_content {
        if let Some(google) = incoming.google {
            let mut pending_google = Some(google);
            let mut attached_to_call = false;
            if let Some(call) = target_message.tool_calls.last_mut() {
                let mut call_extra = call.extra_content.take().unwrap_or(OpenAiExtraContent {
                    google: None,
                    anthropic: None,
                });
                if call_extra.google.is_some() {
                    record_loss(
                        envelope,
                        LossCode::LossContentOrder,
                        Some(Feature::OpaqueReasoningSignature),
                        format!("items[{index}].extra_content.google").as_str(),
                        "multiple Google signatures cannot share one ToolCall",
                    );
                    attached_to_call = true;
                } else {
                    call_extra.google = pending_google.take();
                    attached_to_call = true;
                }
                call.extra_content = Some(call_extra);
            }
            if !attached_to_call {
                if existing.google.is_some() {
                    record_loss(
                        envelope,
                        LossCode::LossContentOrder,
                        Some(Feature::OpaqueReasoningSignature),
                        format!("items[{index}].extra_content.google").as_str(),
                        "multiple Google signatures cannot share one Chat message slot",
                    );
                } else if let Some(google) = pending_google {
                    existing.google = Some(google);
                }
            }
        }
        if let Some(anthropic) = incoming.anthropic {
            match existing.anthropic.as_mut() {
                Some(value) => value.blocks.extend(anthropic.blocks),
                None => existing.anthropic = Some(anthropic),
            }
        }
    }
    if existing.google.is_some() || existing.anthropic.is_some() {
        target_message.extra_content = Some(existing);
    }
    let _ = source;
    let _ = target;
    Ok(())
}

/// Encodes an ordered Envelope as an OpenAI Chat non-stream response.
pub fn envelope_to_openai_chat_response_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::OpenAi;
    record_response_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    if extension_i64(&envelope, "gemini.candidate_count").is_some_and(|count| count > 1)
        || extension_i64(&envelope, "openai.choice_count").is_some_and(|count| count > 1)
    {
        record_loss(
            &mut envelope,
            LossCode::LossContentOrder,
            Some(Feature::Text),
            "response.choices",
            "target Chat response has one ordered choice; multiple source choices were merged",
        );
    }
    record_usage_projection_losses(&mut envelope, target);
    let messages = openai_response_message_groups(&mut envelope, source, target)?;
    let choices = messages
        .into_iter()
        .enumerate()
        .map(|(index, message)| OpenAiChoice {
            index: extension_i64(&envelope, &format!("openai.choice[{index}].index"))
                .and_then(|value| usize::try_from(value).ok())
                .unwrap_or(index),
            message,
            finish_reason: response_finish_reason(&envelope, index),
            extra: BTreeMap::new(),
        })
        .collect();
    let response_id = extension_string(&envelope, "openai.response_id")
        .or_else(|| extension_string(&envelope, "claude.response_id"))
        .or_else(|| extension_string(&envelope, "openai.responses.id"));
    if response_id.is_none() {
        record_loss(
            &mut envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "response.id",
            "OpenAI response id is synthesized because no source id is expressible",
        );
    }
    let output = OpenAiChatResponse {
        id: response_id.unwrap_or_else(|| "direct-ir-response".to_owned()),
        model: envelope.model.clone(),
        object: extension_string(&envelope, "openai.response_object")
            .unwrap_or_else(|| "chat.completion".to_owned()),
        created: extension_i64(&envelope, "openai.response_created")
            .or_else(|| extension_i64(&envelope, "openai.responses.created_at"))
            .unwrap_or(0),
        choices,
        usage: envelope
            .usage
            .as_ref()
            .map(|usage| usage_to_wire(usage, Protocol::OpenAi)),
        extra: BTreeMap::new(),
    };
    Ok(finish(output, envelope, source, target))
}

fn gemini_usage_without_billing(usage: &SemanticUsage) -> GeminiUsage {
    GeminiUsage {
        prompt_token_count: usage.input_tokens,
        candidates_token_count: usage.output_tokens,
        thoughts_token_count: usage.reasoning_tokens,
        total_token_count: usage.total_tokens,
        cached_content_token_count: usage.cache.read_input_tokens,
        tool_use_prompt_token_count: usage
            .extensions
            .get("gemini.tool_use_prompt_token_count")
            .and_then(number_u64)
            .unwrap_or(0),
        prompt_tokens_details: usage
            .extensions
            .get("gemini.prompt_tokens_details")
            .cloned(),
        candidates_tokens_details: usage
            .extensions
            .get("gemini.candidates_tokens_details")
            .cloned(),
        tool_use_prompt_tokens_details: usage
            .extensions
            .get("gemini.tool_use_prompt_tokens_details")
            .cloned(),
        billing_usage: None,
        extra: extensions_with_prefix(&usage.extensions, "gemini.usage."),
    }
}

fn gemini_usage_from_semantic(usage: &SemanticUsage) -> GeminiUsage {
    let mut output = gemini_usage_without_billing(usage);
    output.billing_usage = billing_usage_for_target(usage, Protocol::Gemini);
    output
}

fn append_response_gemini_item(
    item: &Item,
    parts: &mut Vec<GeminiPart>,
    source: Protocol,
    target: Protocol,
    path: &str,
    envelope: &mut Envelope,
) -> Result<(), DirectIrError> {
    if matches!(&item.kind, ItemKind::Reasoning) {
        let part = single_item_part(item, source, target, path, "reasoning")?;
        match &part.kind {
            PartKind::Opaque => {
                let Some(state) = part.opaque.as_ref() else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "opaque_state",
                        path,
                        DirectIrReason::InvalidOpaqueState,
                    ));
                };
                if state.provider != "google" || state.kind != "thought_signature" {
                    let object = json_object(&state.raw);
                    let thinking = object
                        .and_then(|value| value.get("thinking"))
                        .and_then(json_string);
                    let Some(thinking) = thinking else {
                        return Err(DirectIrError::unsupported(
                            source,
                            target,
                            "opaque_reasoning",
                            path,
                        ));
                    };
                    record_loss(
                        envelope,
                        LossCode::LossOpaqueReasoning,
                        Some(Feature::OpaqueReasoningSignature),
                        path,
                        "foreign opaque reasoning emitted as ordinary Gemini text",
                    );
                    parts.push(GeminiPart {
                        text: Some(thinking.to_owned()),
                        inline_data: None,
                        function_call: None,
                        function_response: None,
                        thought_signature: None,
                        ..GeminiPart::default()
                    });
                    return Ok(());
                }
                let Some(last) = parts.last_mut() else {
                    return Err(DirectIrError::new(
                        source,
                        target,
                        "thought_signature_order",
                        path,
                        DirectIrReason::Mismatch,
                    ));
                };
                append_gemini_state_signature(last, state, source, target, path, envelope)?;
            }
            PartKind::Text => {
                record_loss(
                    envelope,
                    LossCode::LossOpaqueReasoning,
                    Some(Feature::ReasoningSummary),
                    path,
                    "ordinary reasoning emitted as Gemini response text",
                );
                parts.push(GeminiPart {
                    text: Some(part.text.clone().unwrap_or_default()),
                    inline_data: None,
                    function_call: None,
                    function_response: None,
                    thought_signature: None,
                    ..GeminiPart::default()
                });
            }
            _ => {
                return Err(DirectIrError::unsupported(
                    source,
                    target,
                    "reasoning_part",
                    path,
                ));
            }
        }
        return Ok(());
    }
    parts.push(gemini_part_for_item(item, source, target, path)?);
    Ok(())
}

/// Encodes an ordered Envelope as a Gemini non-stream response.
pub fn envelope_to_gemini_response_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::Gemini;
    record_response_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    if extension_i64(&envelope, "openai.choice_count").is_some_and(|count| count > 1)
        || extension_i64(&envelope, "gemini.candidate_count").is_some_and(|count| count > 1)
    {
        record_loss(
            &mut envelope,
            LossCode::LossContentOrder,
            Some(Feature::Text),
            "response.candidates",
            "target Gemini response has one ordered candidate; multiple source candidates were merged",
        );
    }
    record_usage_projection_losses(&mut envelope, target);
    let mut parts = Vec::new();
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        append_response_gemini_item(
            &item,
            &mut parts,
            source,
            target,
            &format!("items[{index}]"),
            &mut envelope,
        )?;
    }
    let output = GeminiResponse {
        candidates: vec![GeminiCandidate {
            index: extension_i64(&envelope, "gemini.candidate[0].index")
                .and_then(|value| usize::try_from(value).ok())
                .or(Some(0)),
            finish_reason: extension_string(&envelope, "gemini.candidate[0].finish_reason")
                .or_else(|| response_finish_reason(&envelope, 0)),
            content: GeminiContent {
                role: Some("model".to_owned()),
                parts,
                ..GeminiContent::default()
            },
            safety_ratings: envelope
                .extensions
                .get("gemini.candidate[0].safety_ratings")
                .cloned(),
            ..GeminiCandidate::default()
        }],
        usage_metadata: envelope.usage.as_ref().map(gemini_usage_from_semantic),
        ..GeminiResponse::default()
    };
    Ok(finish(output, envelope, source, target))
}

fn claude_usage_without_billing(usage: &SemanticUsage) -> ClaudeUsage {
    ClaudeUsage {
        input_tokens: usage.input_tokens,
        output_tokens: usage.output_tokens,
        cache_read_input_tokens: usage.cache.read_input_tokens,
        cache_creation_input_tokens: usage.cache.creation_input_tokens,
        claude_cache_creation_5_m_tokens: usage
            .cache
            .extensions
            .get("claude.cache_creation_5m")
            .and_then(number_u64)
            .unwrap_or(0),
        claude_cache_creation_1_h_tokens: usage
            .cache
            .extensions
            .get("claude.cache_creation_1h")
            .and_then(number_u64)
            .unwrap_or(0),
        extra: extensions_with_prefix(&usage.extensions, "claude.usage."),
        ..ClaudeUsage::default()
    }
}

fn claude_usage_from_semantic(usage: &SemanticUsage) -> ClaudeUsage {
    let mut output = claude_usage_without_billing(usage);
    output.billing_usage = billing_usage_for_target(usage, Protocol::Claude);
    output
}

/// Encodes an ordered Envelope as a Claude non-stream response.
pub fn envelope_to_claude_response_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::Claude;
    record_response_projection_losses(&mut envelope, target);
    record_item_projection_losses(&mut envelope, target);
    if extension_i64(&envelope, "openai.choice_count").is_some_and(|count| count > 1)
        || extension_i64(&envelope, "gemini.candidate_count").is_some_and(|count| count > 1)
    {
        record_loss(
            &mut envelope,
            LossCode::LossContentOrder,
            Some(Feature::Text),
            "response.content",
            "target Claude response has one ordered content list; multiple source choices were merged",
        );
    }
    record_usage_projection_losses(&mut envelope, target);
    let mut content = Vec::new();
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        let path = format!("items[{index}]");
        let foreign_opaque_reasoning = item
            .ordered_parts()
            .first()
            .and_then(|part| part.opaque.as_ref())
            .is_some_and(|state| state.provider != "anthropic");
        if matches!(&item.kind, ItemKind::Reasoning) && foreign_opaque_reasoning {
            record_loss(
                &mut envelope,
                LossCode::LossOpaqueReasoning,
                Some(Feature::OpaqueReasoningSignature),
                &path,
                "foreign provider opaque reasoning has no legal Claude signature representation",
            );
            continue;
        }
        let block = if matches!(&item.kind, ItemKind::Reasoning)
            && item
                .ordered_parts()
                .first()
                .and_then(|part| part.opaque.as_ref())
                .is_none()
        {
            let part = single_item_part(&item, source, target, &path, "reasoning")?;
            record_loss(
                &mut envelope,
                LossCode::LossOpaqueReasoning,
                Some(Feature::ReasoningSummary),
                &path,
                "ordinary reasoning emitted as Claude response text",
            );
            ClaudeContentBlock {
                kind: "text".to_owned(),
                text: Some(part.text.clone().unwrap_or_default()),
                thinking: None,
                signature: None,
                data: None,
                id: None,
                name: None,
                input: None,
                tool_use_id: None,
                content: None,
                source: None,
                extra: BTreeMap::new(),
            }
        } else {
            claude_block_from_item(&item, source, target, &path)?
        };
        content.push(block);
    }
    let response_id = extension_string(&envelope, "claude.response_id")
        .or_else(|| extension_string(&envelope, "openai.response_id"))
        .or_else(|| extension_string(&envelope, "openai.responses.id"));
    if response_id.is_none() {
        record_loss(
            &mut envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "response.id",
            "Claude response id is synthesized because no source id is expressible",
        );
    }
    let output = ClaudeResponse {
        id: response_id.unwrap_or_else(|| "direct-ir-response".to_owned()),
        kind: extension_string(&envelope, "claude.response_type")
            .unwrap_or_else(|| "message".to_owned()),
        role: "assistant".to_owned(),
        model: envelope.model.clone(),
        content,
        stop_reason: response_finish_reason(&envelope, 0),
        usage: envelope.usage.as_ref().map(claude_usage_from_semantic),
        extra: BTreeMap::new(),
    };
    Ok(finish(output, envelope, source, target))
}

fn direct_request<T, U, Decode, Encode>(
    value: T,
    source: Protocol,
    target: Protocol,
    decode: Decode,
    encode: Encode,
) -> Result<DirectIrConversion<U>, DirectIrError>
where
    Decode: FnOnce(T) -> Result<DirectIrConversion<Envelope>, DirectIrError>,
    Encode: FnOnce(Envelope) -> Result<DirectIrConversion<U>, DirectIrError>,
{
    let decoded = decode(value).map_err(|error| remap_direct_error(error, source, target))?;
    let mut encoded =
        encode(decoded.envelope).map_err(|error| remap_direct_error(error, source, target))?;
    encoded.plan.source = source;
    encoded.plan.target = target;
    encoded.plan.hop_count = if source == target { 0 } else { 1 };
    encoded.plan.converter_ids =
        vec![direct_ir_route_descriptor(source, target).request_converter_id];
    Ok(encoded)
}

fn direct_response<T, U, Decode, Encode>(
    value: T,
    source: Protocol,
    target: Protocol,
    decode: Decode,
    encode: Encode,
) -> Result<DirectIrConversion<U>, DirectIrError>
where
    Decode: FnOnce(T) -> Result<DirectIrConversion<Envelope>, DirectIrError>,
    Encode: FnOnce(Envelope) -> Result<DirectIrConversion<U>, DirectIrError>,
{
    let decoded = decode(value).map_err(|error| remap_direct_error(error, source, target))?;
    let mut encoded =
        encode(decoded.envelope).map_err(|error| remap_direct_error(error, source, target))?;
    encoded.plan.source = source;
    encoded.plan.target = target;
    encoded.plan.hop_count = if source == target { 0 } else { 1 };
    encoded.plan.converter_ids =
        vec![direct_ir_route_descriptor(source, target).response_converter_id];
    Ok(encoded)
}

fn remap_direct_error(
    mut error: DirectIrError,
    source: Protocol,
    target: Protocol,
) -> DirectIrError {
    error.source = source;
    error.target = target;
    error
}

fn responses_unknown_field(
    source: Protocol,
    target: Protocol,
    path: impl Into<String>,
) -> DirectIrError {
    DirectIrError::unsupported(source, target, "unknown_field", &path.into())
}

fn reject_wire_extra(
    source: Protocol,
    target: Protocol,
    extra: &BTreeMap<String, JsonData>,
    path: &str,
) -> Result<(), DirectIrError> {
    if let Some(field) = extra.keys().next() {
        let path = if path.is_empty() {
            field.clone()
        } else {
            format!("{path}.{field}")
        };
        return Err(responses_unknown_field(source, target, path));
    }
    Ok(())
}

fn reject_gemini_content_extra(
    content: &GeminiContent,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<(), DirectIrError> {
    reject_wire_extra(source, target, &content.extra, path)?;
    for (index, part) in content.parts.iter().enumerate() {
        let part_path = format!("{path}.parts[{index}]");
        reject_wire_extra(source, target, &part.extra, &part_path)?;
        if let Some(inline_data) = part.inline_data.as_ref() {
            reject_wire_extra(
                source,
                target,
                &inline_data.extra,
                &format!("{part_path}.inlineData"),
            )?;
        }
        if let Some(function_call) = part.function_call.as_ref() {
            reject_wire_extra(
                source,
                target,
                &function_call.extra,
                &format!("{part_path}.functionCall"),
            )?;
        }
        if let Some(function_response) = part.function_response.as_ref() {
            reject_wire_extra(
                source,
                target,
                &function_response.extra,
                &format!("{part_path}.functionResponse"),
            )?;
        }
    }
    Ok(())
}

fn responses_option_error(
    source: Protocol,
    target: Protocol,
    feature: &str,
    path: impl Into<String>,
) -> DirectIrError {
    DirectIrError::unsupported(source, target, feature, &path.into())
}

fn responses_request_content_to_parts(
    content: Option<StringOrParts<ResponsesContentPart>>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Vec<Part>, DirectIrError> {
    let Some(content) = content else {
        return Ok(Vec::new());
    };
    match content {
        StringOrParts::String(text) => Ok(vec![Part::text(text)]),
        StringOrParts::Parts(parts) => parts
            .into_iter()
            .enumerate()
            .map(|(index, part)| {
                let part_path = format!("{path}[{index}]");
                if let Some(field) = part.extra.keys().next() {
                    return Err(responses_unknown_field(
                        source,
                        target,
                        format!("{part_path}.{field}"),
                    ));
                }
                match part.kind.as_str() {
                    "input_text" | "text" => {
                        if part.image_url.is_some() {
                            return Err(responses_option_error(
                                source,
                                target,
                                "content_part",
                                format!("{part_path}.image_url"),
                            ));
                        }
                        part.text.map(Part::text).ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "text",
                                &format!("{part_path}.text"),
                            )
                        })
                    }
                    "input_image" => {
                        if part.text.is_some() {
                            return Err(responses_option_error(
                                source,
                                target,
                                "content_part",
                                format!("{part_path}.text"),
                            ));
                        }
                        let image_url = part.image_url.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "image_url",
                                &format!("{part_path}.image_url"),
                            )
                        })?;
                        let mut media = Media::new(MediaKind::Image);
                        media.uri = Some(image_url);
                        Ok(Part::media(media))
                    }
                    _ => Err(responses_option_error(
                        source,
                        target,
                        "content_part",
                        part_path,
                    )),
                }
            })
            .collect(),
    }
}

fn validate_responses_request_item(
    item: &ResponsesInputItem,
    index: usize,
    source: Protocol,
    target: Protocol,
) -> Result<(), DirectIrError> {
    let path = format!("input[{index}]");
    if let Some(field) = item.extra.keys().next() {
        return Err(responses_unknown_field(
            source,
            target,
            format!("{path}.{field}"),
        ));
    }
    match item.kind.as_deref() {
        None | Some("message") => {
            if item.call_id.is_some()
                || item.name.is_some()
                || item.arguments.is_some()
                || item.output.is_some()
            {
                return Err(responses_option_error(source, target, "input_field", path));
            }
            let _ = role_from_wire(
                item.role.as_deref(),
                source,
                target,
                &format!("{path}.role"),
            )?;
            responses_request_content_to_parts(
                item.content.clone(),
                source,
                target,
                &format!("{path}.content"),
            )?;
        }
        Some("function_call") => {
            if item.call_id.as_deref().is_none_or(|value| value.is_empty()) {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "function_call_id",
                    &format!("{path}.call_id"),
                ));
            }
            if item.name.as_deref().is_none_or(|value| value.is_empty()) {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "function_call_name",
                    &format!("{path}.name"),
                ));
            }
            if item.arguments.is_none() {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "function_call_arguments",
                    &format!("{path}.arguments"),
                ));
            }
            if item.role.is_some() || item.content.is_some() || item.output.is_some() {
                return Err(responses_option_error(source, target, "input_field", path));
            }
        }
        Some("function_call_output") => {
            if item.call_id.as_deref().is_none_or(|value| value.is_empty()) {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "function_call_output_id",
                    &format!("{path}.call_id"),
                ));
            }
            if item.output.is_none() {
                return Err(DirectIrError::missing(
                    source,
                    target,
                    "function_call_output",
                    &format!("{path}.output"),
                ));
            }
            if item.role.is_some()
                || item.content.is_some()
                || item.name.is_some()
                || item.arguments.is_some()
            {
                return Err(responses_option_error(source, target, "input_field", path));
            }
        }
        Some("custom_tool_call") | Some("custom_tool_call_output") => {
            return Err(responses_option_error(source, target, "custom_tool", path));
        }
        Some(_) => {
            return Err(responses_option_error(
                source,
                target,
                "responses_input_item",
                path,
            ));
        }
    }
    Ok(())
}

fn validate_responses_request_surface(
    request: &OpenAiResponsesRequest,
) -> Result<(), DirectIrError> {
    let source = Protocol::OpenAiResponses;
    let target = Protocol::OpenAiResponses;
    if request.model.trim().is_empty() {
        return Err(DirectIrError::missing(source, target, "model", "model"));
    }
    if let Some(field) = request.extra.keys().next() {
        return Err(responses_unknown_field(source, target, field.as_str()));
    }
    for (field, present) in [
        ("conversation", request.conversation.is_some()),
        (
            "previous_response_id",
            request.previous_response_id.is_some(),
        ),
        ("prompt", request.prompt.is_some()),
        ("context_management", request.context_management.is_some()),
    ] {
        if present {
            return Err(responses_option_error(
                source,
                target,
                "state_reference",
                field,
            ));
        }
    }
    for (field, present) in [
        ("include", request.include.is_some()),
        ("moderation", request.moderation.is_some()),
        ("max_tool_calls", request.max_tool_calls.is_some()),
        ("client_metadata", request.client_metadata.is_some()),
    ] {
        if present {
            return Err(responses_option_error(
                source,
                target,
                "responses_option",
                field,
            ));
        }
    }
    if let Some(reasoning) = request.reasoning.as_ref() {
        if let Some(field) = reasoning.extra.keys().next() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("reasoning.{field}"),
            ));
        }
        for (field, present) in [
            ("summary", reasoning.summary.is_some()),
            ("mode", reasoning.mode.is_some()),
            ("context", reasoning.context.is_some()),
        ] {
            if present {
                return Err(responses_option_error(
                    source,
                    target,
                    "reasoning_summary",
                    format!("reasoning.{field}"),
                ));
            }
        }
    }
    for (index, tool) in request.tools.iter().enumerate() {
        let path = format!("tools[{index}]");
        if tool.kind != "function" {
            return Err(responses_option_error(source, target, "tool_type", path));
        }
        if tool
            .name
            .as_deref()
            .is_none_or(|value| value.trim().is_empty())
        {
            return Err(DirectIrError::missing(
                source,
                target,
                "tool.name",
                &format!("{path}.name"),
            ));
        }
        if let Some(field) = tool.extra.keys().next() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("{path}.{field}"),
            ));
        }
    }
    if let Some(ResponsesInput::Items(items)) = request.input.as_ref() {
        for (index, item) in items.iter().enumerate() {
            validate_responses_request_item(item, index, source, target)?;
        }
    }
    if matches!(request.input.as_ref(), Some(ResponsesInput::Json(_))) {
        return Err(responses_option_error(
            source,
            target,
            "input_shape",
            "input",
        ));
    }
    Ok(())
}

fn responses_store_request_controls(request: &OpenAiResponsesRequest, envelope: &mut Envelope) {
    envelope.controls = GenerationControls {
        max_output_tokens: request.max_output_tokens,
        temperature: request.temperature,
        top_p: request.top_p,
        stream: request.stream,
        reasoning_effort: request
            .reasoning
            .as_ref()
            .and_then(|reasoning| reasoning.effort.clone()),
        response_format: request.text.clone(),
        parallel_tool_calls: request.parallel_tool_calls,
        extensions: BTreeMap::new(),
    };
    let extensions = [
        ("user", request.user.clone()),
        ("store", request.store.clone()),
        ("metadata", request.metadata.clone()),
        ("stream_options", request.stream_options.clone()),
        (
            "top_logprobs",
            request
                .top_logprobs
                .map(|value| JsonData::Number(value.into())),
        ),
        ("safety_identifier", request.safety_identifier.clone()),
        (
            "prompt_cache_retention",
            request.prompt_cache_retention.clone(),
        ),
        ("prompt_cache_key", request.prompt_cache_key.clone()),
        (
            "service_tier",
            request.service_tier.clone().map(JsonData::String),
        ),
        ("enable_thinking", request.enable_thinking.clone()),
        ("thinking_budget", request.thinking_budget.clone()),
    ];
    for (name, value) in extensions {
        if let Some(value) = value {
            envelope.controls.extensions.insert(name.to_owned(), value);
        }
    }
}

/// Decodes an OpenAI Responses request into the ordered direct IR.
pub fn openai_responses_request_to_envelope_v2(
    request: OpenAiResponsesRequest,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    validate_responses_request_surface(&request)?;
    let source = Protocol::OpenAiResponses;
    let target = Protocol::OpenAiResponses;
    let model = request.model.clone();
    let mut envelope = Envelope::new(source, model);
    responses_store_request_controls(&request, &mut envelope);
    envelope.tool_choice = responses_tool_choice_from_json(
        request.tool_choice.clone(),
        source,
        target,
        "tool_choice",
    )?;
    for (index, tool) in request.tools.iter().enumerate() {
        let name = tool.name.clone().ok_or_else(|| {
            DirectIrError::missing(source, target, "tool.name", &format!("tools[{index}].name"))
        })?;
        envelope.tools.push(tool_to_ir(
            tool.kind.clone(),
            name,
            tool.description.clone(),
            tool.parameters.clone(),
            tool.strict,
            source,
            target,
            &format!("tools[{index}]"),
        )?);
    }
    if let Some(instructions) = request.instructions.clone() {
        let mut item = Item::new(ItemKind::Message, Role::System, Provenance::new(source));
        item.push_part(Part::text(instructions));
        push_item(&mut envelope, item, source, target, "instructions")?;
    }
    match request.input {
        None => {}
        Some(ResponsesInput::String(text)) => {
            let mut item = Item::new(ItemKind::Message, Role::User, Provenance::new(source));
            item.push_part(Part::text(text));
            push_item(&mut envelope, item, source, target, "input")?;
        }
        Some(ResponsesInput::Items(items)) => {
            for (index, item) in items.into_iter().enumerate() {
                let path = format!("input[{index}]");
                match item.kind.as_deref() {
                    None | Some("message") => {
                        let role = role_from_wire(
                            item.role.as_deref(),
                            source,
                            target,
                            &format!("{path}.role"),
                        )?;
                        let parts = responses_request_content_to_parts(
                            item.content,
                            source,
                            target,
                            &format!("{path}.content"),
                        )?;
                        let mut output =
                            Item::new(ItemKind::Message, role, Provenance::new(source));
                        for part in parts {
                            output.push_part(part);
                        }
                        push_item(&mut envelope, output, source, target, &path)?;
                    }
                    Some("function_call") => {
                        let id = item.call_id.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "function_call_id",
                                &format!("{path}.call_id"),
                            )
                        })?;
                        let name = item.name.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "function_call_name",
                                &format!("{path}.name"),
                            )
                        })?;
                        let arguments = item.arguments.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "function_call_arguments",
                                &format!("{path}.arguments"),
                            )
                        })?;
                        let arguments = parse_json_field(
                            &arguments,
                            source,
                            target,
                            "function_arguments",
                            &format!("{path}.arguments"),
                        )?;
                        push_item(
                            &mut envelope,
                            tool_call_item(
                                OpaqueId::authentic(id, source),
                                name,
                                arguments,
                                source,
                                &path,
                            ),
                            source,
                            target,
                            &path,
                        )?;
                    }
                    Some("function_call_output") => {
                        let id = item.call_id.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "function_call_output_id",
                                &format!("{path}.call_id"),
                            )
                        })?;
                        let output = item.output.ok_or_else(|| {
                            DirectIrError::missing(
                                source,
                                target,
                                "function_call_output",
                                &format!("{path}.output"),
                            )
                        })?;
                        push_item(
                            &mut envelope,
                            tool_result_item(
                                OpaqueId::authentic(id, source),
                                None,
                                output,
                                source,
                                &path,
                            ),
                            source,
                            target,
                            &path,
                        )?;
                    }
                    Some(_) => {
                        return Err(responses_option_error(
                            source,
                            target,
                            "responses_input_item",
                            path,
                        ));
                    }
                }
            }
        }
        Some(ResponsesInput::Json(_)) => {
            return Err(responses_option_error(
                source,
                target,
                "input_shape",
                "input",
            ));
        }
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

/// OpenAI Chat request → Gemini request, directly through one ordered IR.
pub fn openai_chat_request_to_gemini_v2(
    request: OpenAiChatRequest,
    model: &str,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAi,
        Protocol::Gemini,
        openai_chat_request_to_envelope_v2,
        |envelope| envelope_to_gemini_request_v2_for_model(envelope, model, true),
    )
}

/// Gemini request → OpenAI Chat request, directly through one ordered IR.
pub fn gemini_request_to_openai_chat_v2(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Gemini,
        Protocol::OpenAi,
        |request| gemini_request_to_envelope_v2(request, model),
        envelope_to_openai_chat_request_v2,
    )
}

/// Claude request → OpenAI Chat request, directly through one ordered IR.
pub fn claude_request_to_openai_chat_v2(
    request: ClaudeRequest,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Claude,
        Protocol::OpenAi,
        claude_request_to_envelope_v2,
        envelope_to_openai_chat_request_v2,
    )
}

/// OpenAI Chat request → Claude request, directly through one ordered IR.
pub fn openai_chat_request_to_claude_v2(
    request: OpenAiChatRequest,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAi,
        Protocol::Claude,
        openai_chat_request_to_envelope_v2,
        envelope_to_claude_request_v2,
    )
}

/// Gemini request → Claude request, directly through one ordered IR.
pub fn gemini_request_to_claude_v2(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Gemini,
        Protocol::Claude,
        |request| gemini_request_to_envelope_v2(request, model),
        envelope_to_claude_request_v2,
    )
}

/// Claude request → Gemini request, directly through one ordered IR.
pub fn claude_request_to_gemini_v2(
    request: ClaudeRequest,
    model: &str,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Claude,
        Protocol::Gemini,
        claude_request_to_envelope_v2,
        |envelope| envelope_to_gemini_request_v2_for_model(envelope, model, false),
    )
}

/// OpenAI Chat response → Gemini response, directly through one ordered IR.
pub fn openai_chat_response_to_gemini_v2(
    response: OpenAiChatResponse,
    model: &str,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAi,
        Protocol::Gemini,
        openai_chat_response_to_envelope_v2,
        |envelope| {
            let mut envelope = envelope;
            envelope.model = model.to_owned();
            envelope_to_gemini_response_v2(envelope)
        },
    )
}

/// Gemini response → OpenAI Chat response, directly through one ordered IR.
pub fn gemini_response_to_openai_chat_v2(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Gemini,
        Protocol::OpenAi,
        |response| gemini_response_to_envelope_v2(response, model),
        envelope_to_openai_chat_response_v2,
    )
}

/// Claude response → OpenAI Chat response, directly through one ordered IR.
pub fn claude_response_to_openai_chat_v2(
    response: ClaudeResponse,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Claude,
        Protocol::OpenAi,
        claude_response_to_envelope_v2,
        envelope_to_openai_chat_response_v2,
    )
}

/// OpenAI Chat response → Claude response, directly through one ordered IR.
pub fn openai_chat_response_to_claude_v2(
    response: OpenAiChatResponse,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAi,
        Protocol::Claude,
        openai_chat_response_to_envelope_v2,
        envelope_to_claude_response_v2,
    )
}

/// Gemini response → Claude response, directly through one ordered IR.
pub fn gemini_response_to_claude_v2(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Gemini,
        Protocol::Claude,
        |response| gemini_response_to_envelope_v2(response, model),
        envelope_to_claude_response_v2,
    )
}

/// Claude response → Gemini response, directly through one ordered IR.
pub fn claude_response_to_gemini_v2(
    response: ClaudeResponse,
    model: &str,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Claude,
        Protocol::Gemini,
        claude_response_to_envelope_v2,
        |envelope| {
            let mut envelope = envelope;
            envelope.model = model.to_owned();
            envelope_to_gemini_response_v2(envelope)
        },
    )
}

/// Source-protocol aliases used by route adapters and acceptance fixtures.
pub fn openai_chat_to_gemini_request_v2(
    request: OpenAiChatRequest,
    model: &str,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    openai_chat_request_to_gemini_v2(request, model)
}

pub fn gemini_to_openai_chat_request_v2(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    gemini_request_to_openai_chat_v2(request, model)
}

pub fn claude_to_openai_chat_request_v2(
    request: ClaudeRequest,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    claude_request_to_openai_chat_v2(request)
}

pub fn openai_chat_to_claude_request_v2(
    request: OpenAiChatRequest,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    openai_chat_request_to_claude_v2(request)
}

pub fn gemini_to_claude_request_v2(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    gemini_request_to_claude_v2(request, model)
}

pub fn claude_to_gemini_request_v2(
    request: ClaudeRequest,
    model: &str,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    claude_request_to_gemini_v2(request, model)
}

pub fn openai_chat_to_gemini_response_v2(
    response: OpenAiChatResponse,
    model: &str,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    openai_chat_response_to_gemini_v2(response, model)
}

pub fn gemini_to_openai_chat_response_v2(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    gemini_response_to_openai_chat_v2(response, model)
}

pub fn claude_to_openai_chat_response_v2(
    response: ClaudeResponse,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    claude_response_to_openai_chat_v2(response)
}

pub fn openai_chat_to_claude_response_v2(
    response: OpenAiChatResponse,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    openai_chat_response_to_claude_v2(response)
}

pub fn gemini_to_claude_response_v2(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    gemini_response_to_claude_v2(response, model)
}

pub fn claude_to_gemini_response_v2(
    response: ClaudeResponse,
    model: &str,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    claude_response_to_gemini_v2(response, model)
}

fn responses_request_parts_from_item(
    item: &Item,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Option<StringOrParts<ResponsesContentPart>>, DirectIrError> {
    if item.ordered_parts().is_empty() {
        return Ok(None);
    }
    let mut parts = Vec::new();
    for (index, part) in item.ordered_parts().iter().enumerate() {
        let part_path = format!("{path}.content[{index}]");
        if let Some(field) = part.extensions.keys().next() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("{part_path}.{field}"),
            ));
        }
        match &part.kind {
            PartKind::Text => parts.push(ResponsesContentPart {
                kind: "input_text".to_owned(),
                text: Some(part.text.clone().unwrap_or_default()),
                image_url: None,
                extra: BTreeMap::new(),
            }),
            PartKind::Media => {
                let media = part.media.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "media",
                        &part_path,
                        DirectIrReason::InvalidShape,
                    )
                })?;
                if !matches!(&media.kind, MediaKind::Image) || media.data.is_some() {
                    return Err(responses_option_error(
                        source,
                        target,
                        "input_image",
                        part_path,
                    ));
                }
                parts.push(ResponsesContentPart {
                    kind: "input_image".to_owned(),
                    text: None,
                    image_url: Some(media.uri.clone().ok_or_else(|| {
                        DirectIrError::missing(source, target, "image_url", &part_path)
                    })?),
                    extra: BTreeMap::new(),
                });
            }
            _ => {
                return Err(responses_option_error(
                    source,
                    target,
                    "content_part",
                    part_path,
                ));
            }
        }
    }
    if parts.len() == 1 && parts[0].kind == "input_text" {
        return Ok(Some(StringOrParts::String(
            parts[0].text.clone().unwrap_or_default(),
        )));
    }
    Ok(Some(StringOrParts::Parts(parts)))
}

fn responses_control_value(envelope: &Envelope, key: &str) -> Option<JsonData> {
    envelope.controls.extensions.get(key).cloned()
}

fn responses_require_request_shape(envelope: &Envelope) -> Result<(), DirectIrError> {
    let source = envelope.source;
    let target = Protocol::OpenAiResponses;
    if envelope.state.conversation_id.is_some()
        || envelope.state.previous_response_id.is_some()
        || envelope.state.provider.is_some()
        || !envelope.state.extensions.is_empty()
    {
        return Err(responses_option_error(
            source,
            target,
            "state_reference",
            "state",
        ));
    }
    if envelope.extensions.keys().any(|key| {
        !matches!(
            key.as_str(),
            "openai.max_tokens" | "openai.max_completion_tokens"
        )
    }) {
        return Err(responses_unknown_field(
            source,
            target,
            "envelope.extensions",
        ));
    }
    if let (Some(max_tokens), Some(max_completion_tokens)) = (
        envelope.extensions.get("openai.max_tokens"),
        envelope.extensions.get("openai.max_completion_tokens"),
    ) && max_tokens != max_completion_tokens
    {
        return Err(responses_option_error(
            source,
            target,
            "conflicting_token_limits",
            "openai.max_tokens",
        ));
    }
    let allowed = [
        "user",
        "store",
        "metadata",
        "stream_options",
        "top_logprobs",
        "safety_identifier",
        "prompt_cache_retention",
        "prompt_cache_key",
        "service_tier",
        "enable_thinking",
        "thinking_budget",
    ];
    if let Some(key) = envelope
        .controls
        .extensions
        .keys()
        .find(|key| !allowed.contains(&key.as_str()))
    {
        return Err(responses_unknown_field(
            source,
            target,
            format!("controls.extensions.{key}"),
        ));
    }
    Ok(())
}

/// Encodes an ordered IR envelope as an OpenAI Responses request.
pub fn envelope_to_openai_responses_request_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<OpenAiResponsesRequest>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::OpenAiResponses;
    map_validation(envelope.validate(), source, target, "envelope")?;
    responses_require_request_shape(&envelope)?;
    let mut instructions = Vec::new();
    let mut input = Vec::new();
    let mut input_seen = false;
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        let path = format!("items[{index}]");
        if !item.extensions.is_empty() || item.raw.is_some() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("{path}.extensions"),
            ));
        }
        match &item.kind {
            ItemKind::Message => {
                let content = responses_request_parts_from_item(&item, source, target, &path)?;
                if matches!(&item.role, Role::System) {
                    let Some(StringOrParts::String(text)) = content else {
                        return Err(responses_option_error(
                            source,
                            target,
                            "instructions",
                            format!("{path}.content"),
                        ));
                    };
                    if input_seen || !instructions.is_empty() {
                        record_loss(
                            &mut envelope,
                            LossCode::LossContentOrder,
                            Some(Feature::Text),
                            &path,
                            "multiple or interleaved system messages are normalized into Responses instructions",
                        );
                    }
                    instructions.push(text);
                } else {
                    input_seen = true;
                    let role = match item.role {
                        Role::Developer => "developer",
                        Role::User => "user",
                        Role::Assistant | Role::Model => "assistant",
                        Role::System => "system",
                        Role::Tool => {
                            return Err(responses_option_error(
                                source,
                                target,
                                "input_role",
                                format!("{path}.role"),
                            ));
                        }
                    };
                    input.push(ResponsesInputItem {
                        kind: Some("message".to_owned()),
                        role: Some(role.to_owned()),
                        content,
                        call_id: None,
                        name: None,
                        arguments: None,
                        output: None,
                        extra: BTreeMap::new(),
                    });
                }
            }
            ItemKind::ToolCall | ItemKind::ToolResult => {
                input_seen = true;
                let call_id = item.call_id.as_ref().ok_or_else(|| {
                    DirectIrError::missing(source, target, "function_call_id", &path)
                })?;
                let part = single_item_part(&item, source, target, &path, "function")?;
                let function = part.function.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "function",
                        &path,
                        DirectIrReason::InvalidShape,
                    )
                })?;
                let id = id_value(call_id)?;
                if call_id.provenance == OpaqueIdProvenance::Synthetic {
                    record_loss(
                        &mut envelope,
                        LossCode::SyntheticToolCallId,
                        Some(Feature::FunctionCallId),
                        &path,
                        "synthetic tool-call id emitted explicitly",
                    );
                }
                match &function.kind {
                    FunctionKind::Call if matches!(&item.kind, ItemKind::ToolCall) => {
                        input.push(ResponsesInputItem {
                            kind: Some("function_call".to_owned()),
                            role: None,
                            content: None,
                            call_id: Some(id),
                            name: Some(function.name.clone().ok_or_else(|| {
                                DirectIrError::missing(source, target, "function.name", &path)
                            })?),
                            arguments: Some(compact_json(
                                &function.arguments.clone().unwrap_or_else(|| object([])),
                            )?),
                            output: None,
                            extra: BTreeMap::new(),
                        });
                    }
                    FunctionKind::Result if matches!(&item.kind, ItemKind::ToolResult) => {
                        input.push(ResponsesInputItem {
                            kind: Some("function_call_output".to_owned()),
                            role: None,
                            content: None,
                            call_id: Some(id),
                            name: None,
                            arguments: None,
                            output: Some(function.output.clone().unwrap_or(JsonData::Null)),
                            extra: BTreeMap::new(),
                        });
                    }
                    _ => {
                        return Err(responses_option_error(
                            source,
                            target,
                            "function_kind",
                            path,
                        ));
                    }
                }
            }
            ItemKind::Reasoning | ItemKind::Unknown(_) => {
                return Err(responses_option_error(source, target, "input_item", path));
            }
        }
    }
    let tools = envelope
        .tools
        .iter()
        .enumerate()
        .map(|(index, tool)| {
            let name = tool_name_for_target(tool, index, source, target)?;
            let strict = openai_strict_for_tool(tool, source, target, index)?;
            Ok(ResponsesTool {
                kind: "function".to_owned(),
                name: Some(name),
                description: tool.description.clone(),
                parameters: tool.input_schema.clone(),
                strict,
                extra: BTreeMap::new(),
            })
        })
        .collect::<Result<Vec<_>, DirectIrError>>()?;
    let output = OpenAiResponsesRequest {
        model: envelope.model.clone(),
        input: (!input.is_empty()).then_some(ResponsesInput::Items(input)),
        instructions: (!instructions.is_empty()).then(|| instructions.join("\n\n")),
        max_output_tokens: envelope.controls.max_output_tokens,
        stream: envelope.controls.stream,
        temperature: envelope.controls.temperature,
        tools,
        tool_choice: Some(responses_tool_choice_to_json(
            &envelope.tool_choice,
            source,
            target,
        )?),
        top_p: envelope.controls.top_p,
        reasoning: envelope
            .controls
            .reasoning_effort
            .clone()
            .map(|effort| ReasoningConfig {
                effort: Some(effort),
                summary: None,
                mode: None,
                context: None,
                extra: BTreeMap::new(),
            }),
        text: envelope.controls.response_format.clone(),
        parallel_tool_calls: envelope.controls.parallel_tool_calls,
        user: responses_control_value(&envelope, "user"),
        store: responses_control_value(&envelope, "store"),
        metadata: responses_control_value(&envelope, "metadata"),
        stream_options: responses_control_value(&envelope, "stream_options"),
        top_logprobs: responses_control_value(&envelope, "top_logprobs").and_then(|value| {
            match value {
                JsonData::Number(number) => number.as_i64(),
                _ => None,
            }
        }),
        safety_identifier: responses_control_value(&envelope, "safety_identifier"),
        prompt_cache_retention: responses_control_value(&envelope, "prompt_cache_retention"),
        prompt_cache_key: responses_control_value(&envelope, "prompt_cache_key"),
        service_tier: responses_control_value(&envelope, "service_tier")
            .and_then(|value| json_string(&value).map(str::to_owned)),
        enable_thinking: responses_control_value(&envelope, "enable_thinking"),
        thinking_budget: responses_control_value(&envelope, "thinking_budget"),
        conversation: None,
        previous_response_id: None,
        prompt: None,
        context_management: None,
        include: None,
        moderation: None,
        max_tool_calls: None,
        client_metadata: None,
        extra: BTreeMap::new(),
    };
    Ok(finish(output, envelope, source, target))
}
fn responses_output_content_to_parts(
    content: Option<Vec<ResponsesOutputContent>>,
    source: Protocol,
    target: Protocol,
    path: &str,
) -> Result<Vec<Part>, DirectIrError> {
    let Some(content) = content else {
        return Ok(Vec::new());
    };
    content
        .into_iter()
        .enumerate()
        .map(|(index, part)| {
            let part_path = format!("{path}[{index}]");
            if let Some(field) = part.extra.keys().next() {
                return Err(responses_unknown_field(
                    source,
                    target,
                    format!("{part_path}.{field}"),
                ));
            }
            if part
                .annotations
                .as_ref()
                .is_some_and(|annotations| !annotations.is_empty())
            {
                return Err(responses_option_error(
                    source,
                    target,
                    "annotations",
                    format!("{part_path}.annotations"),
                ));
            }
            if !matches!(part.kind.as_str(), "output_text" | "summary_text" | "text") {
                return Err(responses_option_error(
                    source,
                    target,
                    "output_content",
                    part_path,
                ));
            }
            part.text.map(Part::text).ok_or_else(|| {
                DirectIrError::missing(
                    source,
                    target,
                    "output_text",
                    format!("{path}[{index}].text").as_str(),
                )
            })
        })
        .collect()
}

fn validate_responses_response_surface(response: &ResponsesResponse) -> Result<(), DirectIrError> {
    let source = Protocol::OpenAiResponses;
    let target = Protocol::OpenAiResponses;
    if response.object != "response" {
        return Err(responses_option_error(
            source,
            target,
            "response_object",
            "object",
        ));
    }
    if !matches!(
        response.status.as_str(),
        "completed" | "incomplete" | "failed" | "cancelled"
    ) {
        return Err(responses_option_error(
            source,
            target,
            "response_status",
            "status",
        ));
    }
    if response.previous_response_id.is_some() {
        return Err(responses_option_error(
            source,
            target,
            "state_reference",
            "previous_response_id",
        ));
    }
    for (path, present) in [
        ("instructions", response.instructions.is_some()),
        ("max_output_tokens", response.max_output_tokens.is_some()),
        (
            "parallel_tool_calls",
            response.parallel_tool_calls.is_some(),
        ),
        ("reasoning", response.reasoning.is_some()),
        ("store", response.store.is_some()),
        ("temperature", response.temperature.is_some()),
        ("tool_choice", response.tool_choice.is_some()),
        ("tools", response.tools.is_some()),
        ("top_p", response.top_p.is_some()),
        ("truncation", response.truncation.is_some()),
        ("user", response.user.is_some()),
        ("metadata", response.metadata.is_some()),
        ("error", response.error.is_some()),
    ] {
        if present {
            return Err(responses_option_error(
                source,
                target,
                "response_option",
                path,
            ));
        }
    }
    if response.status != "incomplete" && response.incomplete_details.is_some() {
        return Err(responses_option_error(
            source,
            target,
            "incomplete_details",
            "incomplete_details",
        ));
    }
    if let Some(details) = response.incomplete_details.as_ref()
        && let Some(field) = details.extra.keys().next()
    {
        return Err(responses_unknown_field(
            source,
            target,
            format!("incomplete_details.{field}"),
        ));
    }
    if let Some(field) = response.extra.keys().next() {
        return Err(responses_unknown_field(source, target, field.as_str()));
    }
    for (index, item) in response.output.iter().enumerate() {
        let path = format!("output[{index}]");
        if !item.status.is_empty() && item.status != "completed" {
            return Err(responses_option_error(
                source,
                target,
                "output_status",
                format!("{path}.status"),
            ));
        }
        if !item.role.is_empty() && item.role != "assistant" {
            return Err(responses_option_error(
                source,
                target,
                "output_role",
                format!("{path}.role"),
            ));
        }
        if !item.quality.is_empty() || !item.size.is_empty() {
            return Err(responses_option_error(
                source,
                target,
                "output_metadata",
                path,
            ));
        }
        if let Some(field) = item.extra.keys().next() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("{path}.{field}"),
            ));
        }
        match item.kind.as_str() {
            "message" => {
                responses_output_content_to_parts(
                    item.content.clone(),
                    source,
                    target,
                    &format!("{path}.content"),
                )?;
                if item.summary.is_some()
                    || item.call_id.is_some()
                    || item.name.is_some()
                    || item.arguments.is_some()
                {
                    return Err(responses_option_error(source, target, "output_field", path));
                }
            }
            "reasoning" => {
                if item.content.is_some() && item.summary.is_some() {
                    return Err(responses_option_error(
                        source,
                        target,
                        "reasoning_content",
                        path,
                    ));
                }
                responses_output_content_to_parts(
                    item.content.clone().or_else(|| item.summary.clone()),
                    source,
                    target,
                    &format!("{path}.content"),
                )?;
                if item.call_id.is_some() || item.name.is_some() || item.arguments.is_some() {
                    return Err(responses_option_error(source, target, "output_field", path));
                }
            }
            "function_call" => {
                if item.call_id.as_deref().is_none_or(|value| value.is_empty()) {
                    return Err(DirectIrError::missing(
                        source,
                        target,
                        "function_call_id",
                        &format!("{path}.call_id"),
                    ));
                }
                if item.name.as_deref().is_none_or(|value| value.is_empty()) {
                    return Err(DirectIrError::missing(
                        source,
                        target,
                        "function_call_name",
                        &format!("{path}.name"),
                    ));
                }
                if item.arguments.is_none() {
                    return Err(DirectIrError::missing(
                        source,
                        target,
                        "function_call_arguments",
                        &format!("{path}.arguments"),
                    ));
                }
                if item.content.is_some() || item.summary.is_some() {
                    return Err(responses_option_error(source, target, "output_field", path));
                }
            }
            _ => {
                return Err(responses_option_error(
                    source,
                    target,
                    "responses_output_item",
                    path,
                ));
            }
        }
    }
    Ok(())
}

/// Decodes an OpenAI Responses response into the ordered direct IR.
pub fn openai_responses_response_to_envelope_v2(
    response: ResponsesResponse,
) -> Result<DirectIrConversion<Envelope>, DirectIrError> {
    validate_responses_response_surface(&response)?;
    let source = Protocol::OpenAiResponses;
    let target = Protocol::OpenAiResponses;
    let mut envelope = Envelope::new(source, response.model.clone());
    envelope.extensions.insert(
        "openai.responses.id".to_owned(),
        JsonData::String(response.id),
    );
    envelope.extensions.insert(
        "openai.responses.object".to_owned(),
        JsonData::String(response.object),
    );
    envelope.extensions.insert(
        "openai.responses.created_at".to_owned(),
        JsonData::Number(response.created_at.into()),
    );
    envelope.extensions.insert(
        "openai.responses.status".to_owned(),
        JsonData::String(response.status.clone()),
    );
    if let Some(details) = response.incomplete_details {
        envelope.extensions.insert(
            "openai.responses.incomplete_reason".to_owned(),
            JsonData::String(details.reason),
        );
    }
    if let Some(usage) = response.usage {
        envelope.usage = Some(usage_from_wire(&usage));
    }
    for (index, output) in response.output.into_iter().enumerate() {
        let path = format!("output[{index}]");
        let output_id = output.id.clone();
        let mut item = match output.kind.as_str() {
            "message" => {
                let mut item =
                    Item::new(ItemKind::Message, Role::Assistant, Provenance::new(source));
                for part in responses_output_content_to_parts(
                    output.content,
                    source,
                    target,
                    &format!("{path}.content"),
                )? {
                    item.push_part(part);
                }
                item
            }
            "reasoning" => {
                let mut item = Item::new(
                    ItemKind::Reasoning,
                    Role::Assistant,
                    Provenance::new(source),
                );
                for part in responses_output_content_to_parts(
                    output.content.or(output.summary),
                    source,
                    target,
                    &format!("{path}.content"),
                )? {
                    item.push_part(part);
                }
                item
            }
            "function_call" => {
                let id = output.call_id.ok_or_else(|| {
                    DirectIrError::missing(
                        source,
                        target,
                        "function_call_id",
                        &format!("{path}.call_id"),
                    )
                })?;
                let name = output.name.ok_or_else(|| {
                    DirectIrError::missing(
                        source,
                        target,
                        "function_call_name",
                        &format!("{path}.name"),
                    )
                })?;
                let arguments = output.arguments.ok_or_else(|| {
                    DirectIrError::missing(
                        source,
                        target,
                        "function_call_arguments",
                        &format!("{path}.arguments"),
                    )
                })?;
                let arguments = parse_json_field(
                    &arguments,
                    source,
                    target,
                    "function_arguments",
                    &format!("{path}.arguments"),
                )?;
                tool_call_item(
                    OpaqueId::authentic(id, source),
                    name,
                    arguments,
                    source,
                    &path,
                )
            }
            _ => {
                return Err(responses_option_error(
                    source,
                    target,
                    "responses_output_item",
                    path,
                ));
            }
        };
        if !output_id.is_empty() {
            item.id = Some(OpaqueId::authentic(output_id, source));
        }
        push_item(&mut envelope, item, source, target, &path)?;
    }
    map_validation(envelope.validate(), source, target, "envelope")?;
    Ok(finish(envelope.clone(), envelope, source, target))
}

fn responses_response_item_id(
    item: &Item,
    index: usize,
    envelope: &mut Envelope,
    path: &str,
) -> String {
    if let Some(id) = item.id.as_ref() {
        return id.value.clone();
    }
    let id = format!("direct-ir-item-{index}");
    record_loss(
        envelope,
        LossCode::LossUnknownEvent,
        Some(Feature::UnknownEventPassthrough),
        path,
        "Responses output item id synthesized explicitly",
    );
    id
}

fn responses_response_extension_is_known(key: &str) -> bool {
    matches!(
        key,
        "openai.responses.id"
            | "openai.responses.object"
            | "openai.responses.created_at"
            | "openai.responses.status"
            | "openai.responses.incomplete_reason"
            | "openai.response_id"
            | "openai.response_object"
            | "openai.response_created"
            | "openai.choice_count"
            | "claude.response_id"
            | "claude.response_type"
            | "claude.stop_reason"
            | "gemini.candidate_count"
    ) || (key.starts_with("openai.choice[")
        && (key.ends_with(".index") || key.ends_with(".finish_reason")))
        || (key.starts_with("gemini.candidate[")
            && (key.ends_with(".index")
                || key.ends_with(".finish_reason")
                || key.ends_with(".safety_ratings")))
}

fn record_responses_response_projection_losses(envelope: &mut Envelope) {
    if extension_i64(envelope, "openai.choice_count").is_some_and(|count| count > 1)
        || extension_i64(envelope, "gemini.candidate_count").is_some_and(|count| count > 1)
    {
        record_loss(
            envelope,
            LossCode::LossContentOrder,
            Some(Feature::Text),
            "response.output",
            "multiple source alternatives are flattened into ordered Responses output items",
        );
    }
    if envelope
        .extensions
        .keys()
        .any(|key| key.starts_with("openai.choice[") && key.ends_with(".index"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "openai.choice[].index",
            "Chat choice indexes are not representable as Responses output indexes",
        );
    }
    if envelope
        .extensions
        .keys()
        .any(|key| key.starts_with("gemini.candidate[") && key.ends_with(".index"))
    {
        record_loss(
            envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "gemini.candidate[].index",
            "Gemini candidate indexes are not representable as Responses output indexes",
        );
    }
    if envelope
        .extensions
        .keys()
        .any(|key| key.starts_with("gemini.candidate[") && key.ends_with(".safety_ratings"))
    {
        record_loss(
            envelope,
            LossCode::LossSafetyMetadata,
            Some(Feature::SafetyMetadata),
            "gemini.candidate[].safetyRatings",
            "Gemini safety ratings are not representable in Responses",
        );
    }
}

fn responses_status_from_envelope(envelope: &mut Envelope) -> (String, Option<IncompleteDetails>) {
    if let Some(status) = extension_string(envelope, "openai.responses.status") {
        let details =
            extension_string(envelope, "openai.responses.incomplete_reason").map(|reason| {
                IncompleteDetails {
                    reason,
                    extra: BTreeMap::new(),
                }
            });
        return (status, details);
    }
    let Some(reason) = response_finish_reason(envelope, 0) else {
        return ("completed".to_owned(), None);
    };
    match reason.to_ascii_lowercase().as_str() {
        "length" | "max_tokens" | "max_output_tokens" => (
            "incomplete".to_owned(),
            Some(IncompleteDetails {
                reason: "max_output_tokens".to_owned(),
                extra: BTreeMap::new(),
            }),
        ),
        "content_filter" | "safety" | "recitation" => (
            "incomplete".to_owned(),
            Some(IncompleteDetails {
                reason: "content_filter".to_owned(),
                extra: BTreeMap::new(),
            }),
        ),
        "error" | "failed" => ("failed".to_owned(), None),
        "cancelled" | "canceled" => ("cancelled".to_owned(), None),
        "stop" | "end_turn" | "tool_calls" | "tool_use" => ("completed".to_owned(), None),
        _ => {
            record_loss(
                envelope,
                LossCode::LossUnknownEvent,
                Some(Feature::UnknownEventPassthrough),
                "response.finish_reason",
                "source finish reason is not representable in Responses status",
            );
            ("completed".to_owned(), None)
        }
    }
}

/// Encodes an ordered IR envelope as an OpenAI Responses response.
pub fn envelope_to_openai_responses_response_v2(
    mut envelope: Envelope,
) -> Result<DirectIrConversion<ResponsesResponse>, DirectIrError> {
    let source = envelope.source;
    let target = Protocol::OpenAiResponses;
    map_validation(envelope.validate(), source, target, "envelope")?;
    if envelope.state.conversation_id.is_some()
        || envelope.state.previous_response_id.is_some()
        || envelope.state.provider.is_some()
        || !envelope.state.extensions.is_empty()
    {
        return Err(responses_option_error(
            source,
            target,
            "state_reference",
            "state",
        ));
    }
    if let Some(field) = envelope
        .extensions
        .keys()
        .find(|key| !responses_response_extension_is_known(key))
    {
        return Err(responses_unknown_field(
            source,
            target,
            format!("envelope.extensions.{field}"),
        ));
    }
    record_responses_response_projection_losses(&mut envelope);
    let mut output = Vec::new();
    for (index, item) in envelope.ordered_items().to_vec().into_iter().enumerate() {
        let path = format!("items[{index}]");
        if !item.extensions.is_empty() || item.raw.is_some() {
            return Err(responses_unknown_field(
                source,
                target,
                format!("{path}.extensions"),
            ));
        }
        let id = responses_response_item_id(&item, index, &mut envelope, &path);
        match &item.kind {
            ItemKind::Message | ItemKind::Reasoning => {
                let mut content = Vec::new();
                for (part_index, part) in item.ordered_parts().iter().enumerate() {
                    let part_path = format!("{path}.parts[{part_index}]");
                    if !part.extensions.is_empty() || !matches!(&part.kind, PartKind::Text) {
                        return Err(responses_option_error(
                            source,
                            target,
                            "output_content",
                            part_path,
                        ));
                    }
                    content.push(ResponsesOutputContent {
                        kind: if matches!(&item.kind, ItemKind::Reasoning) {
                            "summary_text".to_owned()
                        } else {
                            "output_text".to_owned()
                        },
                        text: Some(part.text.clone().unwrap_or_default()),
                        annotations: Some(Vec::new()),
                        extra: BTreeMap::new(),
                    });
                }
                let reasoning = matches!(&item.kind, ItemKind::Reasoning);
                let (content, summary) = if reasoning {
                    (None, Some(content))
                } else {
                    (Some(content), None)
                };
                output.push(ResponsesOutputItem {
                    kind: if reasoning {
                        "reasoning".to_owned()
                    } else {
                        "message".to_owned()
                    },
                    id,
                    status: "completed".to_owned(),
                    role: if matches!(&item.kind, ItemKind::Message) {
                        "assistant".to_owned()
                    } else {
                        String::new()
                    },
                    content,
                    summary,
                    quality: String::new(),
                    size: String::new(),
                    call_id: None,
                    name: None,
                    arguments: None,
                    extra: BTreeMap::new(),
                });
            }
            ItemKind::ToolCall => {
                let call_id = item.call_id.as_ref().ok_or_else(|| {
                    DirectIrError::missing(source, target, "function_call_id", &path)
                })?;
                let part = single_item_part(&item, source, target, &path, "function_call")?;
                let function = part.function.as_ref().ok_or_else(|| {
                    DirectIrError::new(
                        source,
                        target,
                        "function_call",
                        &path,
                        DirectIrReason::InvalidShape,
                    )
                })?;
                let name = function.name.clone().ok_or_else(|| {
                    DirectIrError::missing(source, target, "function.name", &path)
                })?;
                let arguments =
                    compact_json(&function.arguments.clone().unwrap_or_else(|| object([])))?;
                output.push(ResponsesOutputItem {
                    kind: "function_call".to_owned(),
                    id,
                    status: "completed".to_owned(),
                    role: String::new(),
                    content: None,
                    summary: None,
                    quality: String::new(),
                    size: String::new(),
                    call_id: Some(id_value(call_id)?),
                    name: Some(name),
                    arguments: Some(arguments),
                    extra: BTreeMap::new(),
                });
            }
            ItemKind::ToolResult => {
                return Err(responses_option_error(
                    source,
                    target,
                    "output_tool_result",
                    path,
                ));
            }
            ItemKind::Unknown(_) => {
                return Err(responses_option_error(
                    source,
                    target,
                    "responses_output_item",
                    path,
                ));
            }
        }
    }
    let (status, incomplete_details) = responses_status_from_envelope(&mut envelope);
    if !matches!(
        status.as_str(),
        "completed" | "incomplete" | "failed" | "cancelled"
    ) {
        return Err(responses_option_error(
            source,
            target,
            "response_status",
            "status",
        ));
    }
    let response_id = extension_string(&envelope, "openai.responses.id")
        .or_else(|| extension_string(&envelope, "openai.response_id"))
        .or_else(|| extension_string(&envelope, "claude.response_id"));
    let response_id = response_id.unwrap_or_else(|| {
        record_loss(
            &mut envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "response.id",
            "Responses response id synthesized explicitly",
        );
        "direct-ir-response".to_owned()
    });
    let created_at = extension_i64(&envelope, "openai.responses.created_at")
        .or_else(|| extension_i64(&envelope, "openai.response_created"));
    let created_at = created_at.unwrap_or_else(|| {
        record_loss(
            &mut envelope,
            LossCode::LossUnknownEvent,
            Some(Feature::UnknownEventPassthrough),
            "response.created_at",
            "Responses creation timestamp synthesized explicitly",
        );
        0
    });
    record_usage_projection_losses(&mut envelope, target);
    Ok(finish(
        ResponsesResponse {
            id: response_id,
            object: "response".to_owned(),
            created_at,
            status,
            instructions: None,
            max_output_tokens: None,
            model: envelope.model.clone(),
            output,
            parallel_tool_calls: None,
            previous_response_id: None,
            reasoning: None,
            store: None,
            temperature: None,
            tool_choice: None,
            tools: None,
            top_p: None,
            truncation: None,
            usage: envelope
                .usage
                .as_ref()
                .map(|usage| usage_to_wire(usage, Protocol::OpenAiResponses)),
            user: None,
            metadata: None,
            incomplete_details,
            error: None,
            extra: BTreeMap::new(),
        },
        envelope,
        source,
        target,
    ))
}
/// OpenAI Responses request → OpenAI Chat request through one ordered IR hop.
pub fn openai_responses_request_to_openai_chat_v2(
    request: OpenAiResponsesRequest,
) -> Result<DirectIrConversion<OpenAiChatRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAiResponses,
        Protocol::OpenAi,
        openai_responses_request_to_envelope_v2,
        envelope_to_openai_chat_request_v2,
    )
}

/// OpenAI Chat request → OpenAI Responses request through one ordered IR hop.
pub fn openai_chat_request_to_openai_responses_v2(
    request: OpenAiChatRequest,
) -> Result<DirectIrConversion<OpenAiResponsesRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        openai_chat_request_to_envelope_v2,
        envelope_to_openai_responses_request_v2,
    )
}

/// OpenAI Responses request → Claude request through one ordered IR hop.
pub fn openai_responses_request_to_claude_v2(
    request: OpenAiResponsesRequest,
) -> Result<DirectIrConversion<ClaudeRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAiResponses,
        Protocol::Claude,
        openai_responses_request_to_envelope_v2,
        envelope_to_claude_request_v2,
    )
}

/// OpenAI Responses request → Gemini request through one ordered IR hop.
pub fn openai_responses_request_to_gemini_v2(
    request: OpenAiResponsesRequest,
    model: &str,
) -> Result<DirectIrConversion<GeminiRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::OpenAiResponses,
        Protocol::Gemini,
        openai_responses_request_to_envelope_v2,
        |envelope| envelope_to_gemini_request_v2_for_model(envelope, model, true),
    )
}

/// OpenAI Chat response → OpenAI Responses response through one ordered IR hop.
pub fn openai_chat_response_to_openai_responses_v2(
    response: OpenAiChatResponse,
) -> Result<DirectIrConversion<ResponsesResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        openai_chat_response_to_envelope_v2,
        envelope_to_openai_responses_response_v2,
    )
}

/// OpenAI Responses response → OpenAI Chat response through one ordered IR hop.
pub fn openai_responses_response_to_openai_chat_v2(
    response: ResponsesResponse,
) -> Result<DirectIrConversion<OpenAiChatResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAiResponses,
        Protocol::OpenAi,
        openai_responses_response_to_envelope_v2,
        envelope_to_openai_chat_response_v2,
    )
}

/// OpenAI Responses response → Claude response through one ordered IR hop.
pub fn openai_responses_response_to_claude_v2(
    response: ResponsesResponse,
) -> Result<DirectIrConversion<ClaudeResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAiResponses,
        Protocol::Claude,
        openai_responses_response_to_envelope_v2,
        envelope_to_claude_response_v2,
    )
}

/// OpenAI Responses response → Gemini response through one ordered IR hop.
pub fn openai_responses_response_to_gemini_v2(
    response: ResponsesResponse,
    model: &str,
) -> Result<DirectIrConversion<GeminiResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::OpenAiResponses,
        Protocol::Gemini,
        openai_responses_response_to_envelope_v2,
        |envelope| {
            let mut envelope = envelope;
            envelope.model = model.to_owned();
            envelope_to_gemini_response_v2(envelope)
        },
    )
}

/// Claude request → OpenAI Responses request through one ordered IR hop.
pub fn claude_request_to_openai_responses_v2(
    request: ClaudeRequest,
) -> Result<DirectIrConversion<OpenAiResponsesRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Claude,
        Protocol::OpenAiResponses,
        claude_request_to_envelope_v2,
        envelope_to_openai_responses_request_v2,
    )
}

/// Gemini request → OpenAI Responses request through one ordered IR hop.
pub fn gemini_request_to_openai_responses_v2(
    request: GeminiRequest,
    model: &str,
) -> Result<DirectIrConversion<OpenAiResponsesRequest>, DirectIrError> {
    direct_request(
        request,
        Protocol::Gemini,
        Protocol::OpenAiResponses,
        |request| gemini_request_to_envelope_v2(request, model),
        envelope_to_openai_responses_request_v2,
    )
}

/// Claude response → OpenAI Responses response through one ordered IR hop.
pub fn claude_response_to_openai_responses_v2(
    response: ClaudeResponse,
) -> Result<DirectIrConversion<ResponsesResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Claude,
        Protocol::OpenAiResponses,
        claude_response_to_envelope_v2,
        envelope_to_openai_responses_response_v2,
    )
}

/// Gemini response → OpenAI Responses response through one ordered IR hop.
pub fn gemini_response_to_openai_responses_v2(
    response: GeminiResponse,
    model: &str,
) -> Result<DirectIrConversion<ResponsesResponse>, DirectIrError> {
    direct_response(
        response,
        Protocol::Gemini,
        Protocol::OpenAiResponses,
        |response| gemini_response_to_envelope_v2(response, model),
        envelope_to_openai_responses_response_v2,
    )
}
#[cfg(test)]
mod responses_direct_ir_tests {
    use super::*;

    fn responses_request_fixture() -> OpenAiResponsesRequest {
        serde_json::from_str(
            r#"{
              "model":"gpt-responses",
              "instructions":"be concise",
              "input":[{"type":"message","role":"user","content":"hello"}],
              "tools":[{"type":"function","name":"lookup","parameters":{"type":"object"}}]
            }"#,
        )
        .expect("Responses request fixture")
    }

    #[test]
    fn responses_direct_request_exact_subset_round_trips_without_loss() {
        let decoded = openai_responses_request_to_envelope_v2(responses_request_fixture())
            .expect("Responses request decode");
        assert_eq!(decoded.envelope.ordered_items().len(), 2);
        assert_eq!(decoded.envelope.tools.len(), 1);
        assert!(decoded.loss_ledger().is_empty());

        let encoded = envelope_to_openai_responses_request_v2(decoded.envelope)
            .expect("Responses request encode");
        assert_eq!(encoded.value.model, "gpt-responses");
        assert_eq!(encoded.value.instructions.as_deref(), Some("be concise"));
        assert!(matches!(
            encoded.value.input,
            Some(ResponsesInput::Items(ref items))
                if items.len() == 1
                    && items[0].kind.as_deref() == Some("message")
        ));
        assert!(encoded.loss_ledger().is_empty());
        assert!(encoded.enforce_loss_policy(LossPolicy::Reject).is_ok());
    }

    #[test]
    fn responses_direct_request_rejects_state_and_non_function_tools() {
        let stateful: OpenAiResponsesRequest = serde_json::from_str(
            r#"{"model":"gpt-responses","input":[],"previous_response_id":"resp-old"}"#,
        )
        .expect("stateful Responses request");
        let state_error = openai_responses_request_to_envelope_v2(stateful)
            .expect_err("state must not enter stateless IR");
        assert_eq!(state_error.feature, "state_reference");
        assert_eq!(state_error.path, "previous_response_id");

        let builtin: OpenAiResponsesRequest =
            serde_json::from_str(r#"{"model":"gpt-responses","tools":[{"type":"web_search"}]}"#)
                .expect("Responses builtin tool request");
        let tool_error = openai_responses_request_to_envelope_v2(builtin)
            .expect_err("builtin tools must be explicitly unsupported");
        assert_eq!(tool_error.feature, "tool_type");
        assert_eq!(tool_error.path, "tools[0]");
    }

    #[test]
    fn responses_named_tool_choice_is_flat_and_unknown_fields_fail_closed() {
        let mut request = responses_request_fixture();
        request.tool_choice = Some(object([
            ("type".to_owned(), JsonData::String("function".to_owned())),
            ("name".to_owned(), JsonData::String("lookup".to_owned())),
        ]));
        let decoded = openai_responses_request_to_envelope_v2(request)
            .expect("flat Responses tool choice decodes");
        let encoded = envelope_to_openai_responses_request_v2(decoded.envelope)
            .expect("flat Responses tool choice encodes");
        assert_eq!(
            encoded.value.tool_choice,
            Some(object([
                ("type".to_owned(), JsonData::String("function".to_owned())),
                ("name".to_owned(), JsonData::String("lookup".to_owned())),
            ]))
        );

        let nested: OpenAiResponsesRequest = serde_json::from_str(
            r#"{
              "model":"gpt-responses",
              "tool_choice":{"type":"function","name":"lookup","function":{}}
            }"#,
        )
        .expect("nested tool choice fixture");
        let error = openai_responses_request_to_envelope_v2(nested)
            .expect_err("unknown Responses tool-choice fields must be rejected");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "tool_choice.function");
    }

    #[test]
    fn responses_direct_response_round_trip_retains_ids_and_usage() {
        let response: ResponsesResponse = serde_json::from_str(
            r#"{
              "id":"resp-1","object":"response","created_at":7,
              "status":"completed","model":"gpt-responses",
              "output":[
                {"type":"message","id":"msg-1","status":"completed","role":"assistant",
                 "content":[{"type":"output_text","text":"answer"}]}
              ],
              "usage":{"input_tokens":3,"output_tokens":2,"total_tokens":5}
            }"#,
        )
        .expect("Responses response fixture");
        let decoded =
            openai_responses_response_to_envelope_v2(response).expect("Responses response decode");
        assert_eq!(
            decoded.envelope.ordered_items()[0]
                .id
                .as_ref()
                .map(|id| id.value.as_str()),
            Some("msg-1")
        );
        assert_eq!(
            decoded
                .envelope
                .usage
                .as_ref()
                .map(|usage| usage.total_tokens),
            Some(5)
        );
        let encoded = envelope_to_openai_responses_response_v2(decoded.envelope)
            .expect("Responses response encode");
        assert_eq!(encoded.value.id, "resp-1");
        assert_eq!(encoded.value.output[0].id, "msg-1");
        assert_eq!(
            encoded.value.usage.as_ref().map(|usage| usage.total_tokens),
            Some(5)
        );
        assert!(encoded.loss_ledger().is_empty());
    }

    #[test]
    fn responses_incomplete_details_retain_unknown_fields_for_typed_rejection() {
        let response: ResponsesResponse = serde_json::from_str(
            r#"{
              "id":"resp-1","object":"response","created_at":7,
              "status":"incomplete","model":"gpt-responses","output":[],
              "incomplete_details":{"reason":"max_output_tokens","future":true}
            }"#,
        )
        .expect("unknown Responses detail is retained by the wire DTO");
        assert_eq!(
            response
                .incomplete_details
                .as_ref()
                .and_then(|details| details.extra.get("future")),
            Some(&JsonData::Bool(true))
        );
        let error = openai_responses_response_to_envelope_v2(response)
            .expect_err("direct IR must reject an unrepresentable detail");
        assert_eq!(error.path, "incomplete_details.future");
        assert_eq!(error.feature, "unknown_field");
    }

    #[test]
    fn responses_direct_synthetic_id_enters_loss_ledger_as_a_warning() {
        let mut envelope = Envelope::new(Protocol::OpenAi, "gpt-responses");
        let item = Item::tool_call(
            OpaqueId::synthetic("call-synthetic", Protocol::OpenAi),
            vec![function_call_part("lookup".to_owned(), object([]))],
            Provenance::new(Protocol::OpenAi),
        );
        envelope.push_item(item).expect("valid synthetic call");
        let converted = envelope_to_openai_responses_request_v2(envelope)
            .expect("synthetic id remains explicit");
        assert!(
            converted
                .loss_ledger()
                .as_slice()
                .iter()
                .any(|loss| loss.code == LossCode::SyntheticToolCallId)
        );
        let outcome = converted
            .enforce_loss_policy(LossPolicy::Reject)
            .expect("a deterministic synthetic identifier is warning-level metadata");
        assert!(
            outcome
                .warnings
                .as_slice()
                .iter()
                .any(|loss| { loss.code == LossCode::SyntheticToolCallId })
        );
    }

    #[test]
    fn responses_descriptor_is_disabled_but_has_one_hop_converter_ids() {
        let descriptor = openai_responses_direct_ir_route_descriptor(Protocol::OpenAi);
        assert!(!descriptor.enabled);
        assert_eq!(descriptor.hop_count, 1);
        assert!(descriptor.request_converter_id.contains("openai-responses"));
        assert!(descriptor.response_converter_id.contains("openai-chat"));
    }
}

#[cfg(test)]
mod direct_ir_tests {
    use super::*;

    fn chat_request() -> OpenAiChatRequest {
        serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"user","content":"hello"}]}"#,
        )
        .expect("test Chat request")
    }

    fn gemini_request() -> GeminiRequest {
        serde_json::from_str(r#"{"contents":[{"role":"user","parts":[{"text":"hello"}]}]}"#)
            .expect("test Gemini request")
    }

    fn claude_request() -> ClaudeRequest {
        serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[{"role":"user","content":[{"type":"text","text":"hello"}]}]}"#,
        )
        .expect("test Claude request")
    }

    fn chat_response() -> OpenAiChatResponse {
        serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"finish_reason":"stop"}],"usage":{"prompt_tokens":2,"completion_tokens":1,"total_tokens":3}}"#,
        )
        .expect("test Chat response")
    }

    fn gemini_response() -> GeminiResponse {
        serde_json::from_str(
            r#"{"candidates":[{"index":0,"finishReason":"STOP","content":{"role":"model","parts":[{"text":"hello"}]}}],"usageMetadata":{"promptTokenCount":2,"candidatesTokenCount":1,"totalTokenCount":3}}"#,
        )
        .expect("test Gemini response")
    }

    fn claude_response() -> ClaudeResponse {
        serde_json::from_str(
            r#"{"id":"claude-id","type":"message","role":"assistant","model":"claude-test","content":[{"type":"text","text":"hello"}],"stop_reason":"end_turn","usage":{"input_tokens":2,"output_tokens":1}}"#,
        )
        .expect("test Claude response")
    }

    fn assert_one_hop<T>(conversion: &DirectIrConversion<T>, source: Protocol, target: Protocol) {
        assert_eq!(conversion.plan.source, source);
        assert_eq!(conversion.plan.target, target);
        assert_eq!(conversion.plan.hop_count, 1);
        assert_eq!(conversion.plan.converter_ids.len(), 1);
        assert!(!direct_ir_route_descriptor(source, target).enabled);
    }

    #[test]
    fn all_direct_request_pairs_use_one_ir_hop_and_one_converter_id() {
        let openai_to_gemini = openai_chat_request_to_gemini_v2(chat_request(), "gemini-2.5-pro");
        assert!(openai_to_gemini.is_ok());
        if let Ok(value) = openai_to_gemini {
            assert_one_hop(&value, Protocol::OpenAi, Protocol::Gemini);
        }

        let gemini_to_openai = gemini_request_to_openai_chat_v2(gemini_request(), "gemini-2.5-pro");
        assert!(gemini_to_openai.is_ok());
        if let Ok(value) = gemini_to_openai {
            assert_one_hop(&value, Protocol::Gemini, Protocol::OpenAi);
        }

        let claude_to_openai = claude_request_to_openai_chat_v2(claude_request());
        assert!(claude_to_openai.is_ok());
        if let Ok(value) = claude_to_openai {
            assert_one_hop(&value, Protocol::Claude, Protocol::OpenAi);
        }

        let openai_to_claude = openai_chat_request_to_claude_v2(chat_request());
        assert!(openai_to_claude.is_ok());
        if let Ok(value) = openai_to_claude {
            assert_one_hop(&value, Protocol::OpenAi, Protocol::Claude);
        }

        let gemini_to_claude = gemini_request_to_claude_v2(gemini_request(), "gemini-2.5-pro");
        assert!(gemini_to_claude.is_ok());
        if let Ok(value) = gemini_to_claude {
            assert_one_hop(&value, Protocol::Gemini, Protocol::Claude);
        }

        let claude_to_gemini = claude_request_to_gemini_v2(claude_request(), "gemini-2.5-pro");
        assert!(claude_to_gemini.is_ok());
        if let Ok(value) = claude_to_gemini {
            assert_one_hop(&value, Protocol::Claude, Protocol::Gemini);
        }
    }

    #[test]
    fn all_direct_response_pairs_use_one_ir_hop_and_one_converter_id() {
        let openai_to_gemini = openai_chat_response_to_gemini_v2(chat_response(), "gemini-2.5-pro");
        assert!(openai_to_gemini.is_ok());
        if let Ok(value) = openai_to_gemini {
            assert_one_hop(&value, Protocol::OpenAi, Protocol::Gemini);
        }

        let gemini_to_openai =
            gemini_response_to_openai_chat_v2(gemini_response(), "gemini-2.5-pro");
        assert!(gemini_to_openai.is_ok());
        if let Ok(value) = gemini_to_openai {
            assert_one_hop(&value, Protocol::Gemini, Protocol::OpenAi);
        }

        let claude_to_openai = claude_response_to_openai_chat_v2(claude_response());
        assert!(claude_to_openai.is_ok());
        if let Ok(value) = claude_to_openai {
            assert_one_hop(&value, Protocol::Claude, Protocol::OpenAi);
        }

        let openai_to_claude = openai_chat_response_to_claude_v2(chat_response());
        assert!(openai_to_claude.is_ok());
        if let Ok(value) = openai_to_claude {
            assert_one_hop(&value, Protocol::OpenAi, Protocol::Claude);
        }

        let gemini_to_claude = gemini_response_to_claude_v2(gemini_response(), "gemini-2.5-pro");
        assert!(gemini_to_claude.is_ok());
        if let Ok(value) = gemini_to_claude {
            assert_one_hop(&value, Protocol::Gemini, Protocol::Claude);
        }

        let claude_to_gemini = claude_response_to_gemini_v2(claude_response(), "gemini-2.5-pro");
        assert!(claude_to_gemini.is_ok());
        if let Ok(value) = claude_to_gemini {
            assert_one_hop(&value, Protocol::Claude, Protocol::Gemini);
        }
    }

    #[test]
    fn gemini_claude_direct_wire_results_have_no_chat_intermediate_marker() {
        let request = gemini_request_to_claude_v2(gemini_request(), "gemini-2.5-pro");
        assert!(request.is_ok());
        let Some(request) = request.ok() else { return };
        let encoded_request = serde_json::to_string(&request.value).expect("Claude request JSON");
        assert!(!encoded_request.contains("chat.completion"));
        let response = claude_response_to_gemini_v2(claude_response(), "gemini-2.5-pro");
        assert!(response.is_ok());
        let Some(response) = response.ok() else {
            return;
        };
        let encoded_response =
            serde_json::to_string(&response.value).expect("Gemini response JSON");
        assert!(!encoded_response.contains("chat.completion"));
    }

    #[test]
    fn gemini_request_unknown_top_level_and_content_fields_are_typed_rejections() {
        let top_level: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"user","parts":[{"text":"hello"}]}],"futureRequestField":true}"#,
        )
        .expect("unknown Gemini request field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(top_level, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini request field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "futureRequestField");

        let content: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"user","futureContentField":true,"parts":[{"text":"hello"}]}]}"#,
        )
        .expect("unknown Gemini content field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(content, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini content field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "contents[0].futureContentField");

        let part: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"user","parts":[{"text":"hello","futurePartField":1}]}]}"#,
        )
        .expect("unknown Gemini part field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(part, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini part field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "contents[0].parts[0].futurePartField");
    }

    #[test]
    fn gemini_nested_function_and_media_fields_are_typed_rejections() {
        let inline_data: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"user","parts":[{"inlineData":{"mimeType":"image/png","data":"AA==","futureInlineField":true}}]}]}"#,
        )
        .expect("unknown Gemini inlineData field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(inline_data, "gemini-test")
            .expect_err("direct IR must not silently drop an inlineData field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "contents[0].parts[0].inlineData.futureInlineField"
        );

        let function_call: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{},"futureCallField":1}}]}]}"#,
        )
        .expect("unknown Gemini functionCall field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(function_call, "gemini-test")
            .expect_err("direct IR must not silently drop a functionCall field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "contents[0].parts[0].functionCall.futureCallField"
        );

        let function_response: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"lookup","response":{},"futureResponseField":1}}]}]}"#,
        )
        .expect("unknown Gemini functionResponse field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(function_response, "gemini-test")
            .expect_err("direct IR must not silently drop a functionResponse field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "contents[1].parts[0].functionResponse.futureResponseField"
        );
    }

    #[test]
    fn gemini_request_nested_configuration_fields_are_typed_rejections() {
        let generation_config: GeminiRequest = serde_json::from_str(
            r#"{"generationConfig":{"maxOutputTokens":128,"futureGenerationField":true}}"#,
        )
        .expect("unknown Gemini generationConfig field is retained by the wire DTO");
        assert!(
            generation_config
                .generation_config
                .as_ref()
                .is_some_and(|config| config.extra.contains_key("futureGenerationField"))
        );
        let error = gemini_request_to_envelope_v2(generation_config, "gemini-test")
            .expect_err("direct IR must not silently drop a generationConfig field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "generationConfig.futureGenerationField");

        let safety_settings: GeminiRequest = serde_json::from_str(
            r#"{"safetySettings":[{"category":"HARM_CATEGORY_HATE_SPEECH","threshold":"BLOCK_NONE","futureSafetyField":true}]}"#,
        )
        .expect("unknown Gemini safetySettings field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(safety_settings, "gemini-test")
            .expect_err("direct IR must not silently drop a safetySettings field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "safetySettings[0].futureSafetyField");

        let tool: GeminiRequest = serde_json::from_str(
            r#"{"tools":[{"functionDeclarations":[],"futureToolField":true}]}"#,
        )
        .expect("unknown Gemini tool field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(tool, "gemini-test")
            .expect_err("direct IR must not silently drop a tool field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "tools[0].futureToolField");

        let declaration: GeminiRequest = serde_json::from_str(
            r#"{"tools":[{"functionDeclarations":[{"name":"lookup","parameters":{},"futureDeclarationField":true}]}]}"#,
        )
        .expect("unknown Gemini function declaration field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(declaration, "gemini-test")
            .expect_err("direct IR must not silently drop a function declaration field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "tools[0].functionDeclarations[0].futureDeclarationField"
        );

        let tool_config: GeminiRequest = serde_json::from_str(
            r#"{"toolConfig":{"futureToolConfigField":true,"functionCallingConfig":{"mode":"AUTO"}}}"#,
        )
        .expect("unknown Gemini toolConfig field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(tool_config, "gemini-test")
            .expect_err("direct IR must not silently drop a toolConfig field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "toolConfig.futureToolConfigField");

        let function_calling_config: GeminiRequest = serde_json::from_str(
            r#"{"toolConfig":{"functionCallingConfig":{"mode":"AUTO","futureModeField":true}}}"#,
        )
        .expect("unknown Gemini functionCallingConfig field is retained by the wire DTO");
        let error = gemini_request_to_envelope_v2(function_calling_config, "gemini-test")
            .expect_err("direct IR must not silently drop a functionCallingConfig field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "toolConfig.functionCallingConfig.futureModeField"
        );
    }

    #[test]
    fn gemini_response_unknown_fields_are_typed_rejections_at_each_wire_level() {
        let top_level: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"content":{"parts":[{"text":"hello"}]}}],"futureResponseField":true}"#,
        )
        .expect("unknown Gemini response field is retained by the wire DTO");
        let error = gemini_response_to_envelope_v2(top_level, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini response field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "futureResponseField");

        let candidate: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"content":{"parts":[{"text":"hello"}]},"futureCandidateField":true}]}"#,
        )
        .expect("unknown Gemini candidate field is retained by the wire DTO");
        let error = gemini_response_to_envelope_v2(candidate, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini candidate field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "candidates[0].futureCandidateField");

        let content: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"content":{"parts":[{"text":"hello"}],"futureContentField":true}}]}"#,
        )
        .expect("unknown Gemini content field is retained by the wire DTO");
        let error = gemini_response_to_envelope_v2(content, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini content field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "candidates[0].content.futureContentField");

        let part: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"content":{"parts":[{"text":"hello","futurePartField":1}]}}]}"#,
        )
        .expect("unknown Gemini part field is retained by the wire DTO");
        let error = gemini_response_to_envelope_v2(part, "gemini-test")
            .expect_err("direct IR must not silently drop a Gemini part field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "candidates[0].content.parts[0].futurePartField");
    }

    #[test]
    fn claude_top_level_unknown_fields_are_typed_rejections() {
        let request: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[],"futureRequestField":true}"#,
        )
        .expect("unknown Claude request field is retained by the wire DTO");
        let error = claude_request_to_envelope_v2(request)
            .expect_err("direct IR must not silently drop a Claude request field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "futureRequestField");

        let response: ClaudeResponse = serde_json::from_str(
            r#"{"id":"claude-id","type":"message","role":"assistant","model":"claude-test","content":[],"futureResponseField":true}"#,
        )
        .expect("unknown Claude response field is retained by the wire DTO");
        let error = claude_response_to_envelope_v2(response)
            .expect_err("direct IR must not silently drop a Claude response field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "futureResponseField");
    }

    #[test]
    fn claude_response_media_source_unknown_fields_are_typed_rejections() {
        let response: ClaudeResponse = serde_json::from_str(
            r#"{"id":"claude-id","type":"message","role":"assistant","model":"claude-test","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA==","futureSourceField":true}}]}"#,
        )
        .expect("unknown Claude response media source field is retained by the wire DTO");
        assert!(
            response.content[0]
                .source
                .as_ref()
                .is_some_and(|source| source.extra.contains_key("futureSourceField"))
        );
        let error = claude_response_to_envelope_v2(response)
            .expect_err("direct IR must not silently drop a Claude response source field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "response.content[0].source.futureSourceField");
    }

    #[test]
    fn claude_request_nested_unknown_fields_are_typed_rejections() {
        let message: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[{"role":"user","content":"hello","futureMessageField":true}]}"#,
        )
        .expect("unknown Claude message field is retained by the wire DTO");
        assert!(message.messages[0].extra.contains_key("futureMessageField"));
        let error = claude_request_to_envelope_v2(message)
            .expect_err("direct IR must not silently drop a Claude message field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "messages[0].futureMessageField");

        let system_source: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"system":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA==","futureSystemSourceField":true}}]}"#,
        )
        .expect("unknown Claude system media source field is retained by the wire DTO");
        let error = claude_request_to_envelope_v2(system_source)
            .expect_err("direct IR must not silently drop a system media source field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "system.parts[0].source.futureSystemSourceField");

        let message_source: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[{"role":"user","content":[{"type":"image","source":{"type":"base64","media_type":"image/png","data":"AA==","futureMessageSourceField":true}}]}]}"#,
        )
        .expect("unknown Claude message media source field is retained by the wire DTO");
        let error = claude_request_to_envelope_v2(message_source)
            .expect_err("direct IR must not silently drop a message media source field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(
            error.path,
            "messages[0].content[0].source.futureMessageSourceField"
        );

        let tool: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"tools":[{"name":"lookup","input_schema":{},"futureToolField":true}]}"#,
        )
        .expect("unknown Claude tool field is retained by the wire DTO");
        let error = claude_request_to_envelope_v2(tool)
            .expect_err("direct IR must not silently drop a Claude tool field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "tools[0].futureToolField");

        let tool_choice: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"tool_choice":{"type":"auto","futureToolChoiceField":true}}"#,
        )
        .expect("unknown Claude tool_choice field is retained by the wire DTO");
        let error = claude_request_to_envelope_v2(tool_choice)
            .expect_err("direct IR must not silently drop a Claude tool_choice field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "tool_choice.futureToolChoiceField");
    }

    #[test]
    fn openai_chat_response_unknown_fields_are_typed_rejections() {
        let top_level: OpenAiChatResponse = serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[],"futureResponseField":true}"#,
        )
        .expect("unknown OpenAI response field is retained by the wire DTO");
        let error = openai_chat_response_to_envelope_v2(top_level)
            .expect_err("direct IR must not silently drop an OpenAI response field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "futureResponseField");

        let choice: OpenAiChatResponse = serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"hello"},"futureChoiceField":true}]}"#,
        )
        .expect("unknown OpenAI choice field is retained by the wire DTO");
        let error = openai_chat_response_to_envelope_v2(choice)
            .expect_err("direct IR must not silently drop an OpenAI choice field");
        assert_eq!(error.feature, "unknown_field");
        assert_eq!(error.path, "choices[0].futureChoiceField");
    }

    #[test]
    fn gemini_same_name_parallel_calls_match_explicit_ids_out_of_order() {
        let request: GeminiRequest = serde_json::from_str(
            r#"{"contents":[
              {"role":"model","parts":[
                {"functionCall":{"id":"call-a","name":"lookup","args":{"n":1}}},
                {"functionCall":{"id":"call-b","name":"lookup","args":{"n":2}}}
              ]},
              {"role":"user","parts":[
                {"functionResponse":{"id":"call-b","name":"lookup","response":{"ok":2}}},
                {"functionResponse":{"id":"call-a","name":"lookup","response":{"ok":1}}}
              ]}
            ]}"#,
        )
        .expect("parallel Gemini request");
        let result = gemini_request_to_envelope_v2(request, "gemini-2.5-pro");
        assert!(result.is_ok());
        let Ok(result) = result else { return };
        let ids = result
            .envelope
            .ordered_items()
            .iter()
            .filter_map(|item| item.call_id.as_ref().map(|id| id.value.clone()))
            .collect::<Vec<_>>();
        assert_eq!(ids, ["call-a", "call-b", "call-b", "call-a"]);
    }

    #[test]
    fn gemini_tool_link_errors_are_typed_for_orphan_duplicate_and_mismatch() {
        let orphan: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"user","parts":[{"functionResponse":{"id":"missing","name":"lookup","response":{}}}]}]}"#,
        )
        .expect("orphan Gemini result fixture");
        let orphan_error = gemini_request_to_envelope_v2(orphan, "gemini-test").err();
        assert!(orphan_error.is_some());
        let Some(orphan_error) = orphan_error else {
            return;
        };
        assert_eq!(orphan_error.reason, DirectIrReason::Orphan);

        let duplicate: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"same","name":"lookup","args":{}}},{"functionCall":{"id":"same","name":"lookup","args":{}}}]}]}"#,
        )
        .expect("duplicate Gemini call fixture");
        let duplicate_error = gemini_request_to_envelope_v2(duplicate, "gemini-test").err();
        assert!(duplicate_error.is_some());
        let Some(duplicate_error) = duplicate_error else {
            return;
        };
        assert_eq!(duplicate_error.reason, DirectIrReason::DuplicateId);

        let mismatch: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"call-1","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"id":"call-1","name":"other","response":{}}}]}]}"#,
        )
        .expect("mismatch Gemini result fixture");
        let mismatch_error = gemini_request_to_envelope_v2(mismatch, "gemini-test").err();
        assert!(mismatch_error.is_some());
        let Some(mismatch_error) = mismatch_error else {
            return;
        };
        assert_eq!(mismatch_error.reason, DirectIrReason::Mismatch);
    }

    #[test]
    fn gemini_missing_ids_are_deterministic_and_marked_synthetic() {
        let request: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"ok":true}}}]}]}"#,
        )
        .expect("synthetic Gemini id fixture");
        let result = gemini_request_to_envelope_v2(request, "gemini-test");
        assert!(result.is_ok());
        let Some(result) = result.ok() else { return };
        assert!(result.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::SyntheticToolCallId
                && loss.path.as_deref() == Some("contents[0].parts[0].functionCall.id")
        }));
        let call = result
            .envelope
            .ordered_items()
            .iter()
            .find(|item| matches!(&item.kind, ItemKind::ToolCall));
        assert!(call.is_some());
        let Some(call) = call else { return };
        assert_eq!(
            call.call_id.as_ref().map(|id| id.provenance),
            Some(OpaqueIdProvenance::Synthetic)
        );
    }

    #[test]
    fn gemini_missing_result_id_inherits_authentic_call_provenance() {
        let request: GeminiRequest = serde_json::from_str(
            r#"{"contents":[{"role":"model","parts":[{"functionCall":{"id":"auth-call","name":"lookup","args":{}}}]},{"role":"user","parts":[{"functionResponse":{"name":"lookup","response":{"ok":true}}}]}]}"#,
        )
        .expect("authentic Gemini call with omitted result id");
        let result = gemini_request_to_envelope_v2(request, "gemini-test")
            .expect("Gemini result id should link to its authentic call");
        let tool_result = result
            .envelope
            .ordered_items()
            .iter()
            .find(|item| matches!(&item.kind, ItemKind::ToolResult));
        assert!(tool_result.is_some());
        let Some(tool_result) = tool_result else {
            return;
        };
        assert_eq!(
            tool_result.call_id.as_ref().map(|id| id.provenance),
            Some(OpaqueIdProvenance::Authentic)
        );
        assert!(!result.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::SyntheticToolCallId
                && loss.path.as_deref() == Some("contents[1].parts[0].functionResponse.id")
        }));
    }

    #[test]
    fn claude_thinking_redacted_and_ordered_blocks_remain_opaque() {
        let request: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[{"role":"assistant","content":[
              {"type":"thinking","thinking":"plan","signature":"sig-auth"},
              {"type":"redacted_thinking","data":"opaque-bytes"},
              {"type":"text","text":"answer"}
            ]}]}"#,
        )
        .expect("Claude thinking request");
        let result = claude_request_to_openai_chat_v2(request);
        assert!(result.is_ok());
        let Ok(result) = result else { return };
        let blocks = result
            .value
            .messages
            .iter()
            .filter_map(|message| message.extra_content.as_ref())
            .filter_map(|extra| extra.anthropic.as_ref())
            .flat_map(|extra| extra.blocks.iter())
            .collect::<Vec<_>>();
        assert_eq!(blocks.len(), 3);
        assert_eq!(blocks[0].kind, "thinking");
        assert_eq!(blocks[0].signature.as_deref(), Some("sig-auth"));
        assert_eq!(blocks[1].kind, "redacted_thinking");
        assert_eq!(
            blocks[1].data,
            Some(JsonData::String("opaque-bytes".to_owned()))
        );
        assert_eq!(blocks[2].kind, "text");
        assert_eq!(blocks[2].text.as_deref(), Some("answer"));
    }

    #[test]
    fn claude_thinking_without_a_non_empty_signature_is_rejected() {
        for (signature, reason) in [
            ("", DirectIrReason::EmptyId),
            ("missing", DirectIrReason::MissingField),
        ] {
            let thinking = if signature == "missing" {
                r#"{"type":"thinking","thinking":"plan"}"#.to_owned()
            } else {
                format!(r#"{{"type":"thinking","thinking":"plan","signature":"{signature}"}}"#)
            };
            let request: ClaudeRequest = serde_json::from_str(&format!(
                r#"{{"model":"claude-test","max_tokens":128,"messages":[{{"role":"assistant","content":[{thinking}]}}]}}"#
            ))
            .expect("Claude thinking signature fixture");
            let error = claude_request_to_openai_chat_v2(request)
                .expect_err("incomplete Claude thinking must fail closed");
            assert_eq!(error.feature, "thinking_signature");
            assert_eq!(error.reason, reason);
        }
    }

    #[test]
    fn openai_anthropic_outer_model_and_text_extensions_roundtrip() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","extra_content":{"anthropic":{"model":"claude-source","blocks":[{"type":"text","text":"answer","vendor_marker":"keep"},{"type":"thinking","thinking":"plan","signature":"sig"}]}}}]}"#,
        )
        .expect("OpenAI Anthropic extension fixture");
        let decoded = openai_chat_request_to_envelope_v2(request);
        assert!(decoded.is_ok());
        let Some(decoded) = decoded.ok() else { return };
        let text_item = decoded
            .envelope
            .ordered_items()
            .iter()
            .find(|item| matches!(&item.kind, ItemKind::Message));
        assert!(text_item.is_some());
        let Some(text_item) = text_item else { return };
        assert_eq!(
            text_item
                .ordered_parts()
                .first()
                .and_then(|part| part.extensions.get("vendor_marker"))
                .and_then(json_string),
            Some("keep")
        );
        assert_eq!(
            text_item
                .provenance
                .extensions
                .get("anthropic.model")
                .and_then(json_string),
            Some("claude-source")
        );
        let encoded = envelope_to_openai_chat_request_v2(decoded.envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        let anthropic = encoded
            .value
            .messages
            .iter()
            .find_map(|message| message.extra_content.as_ref())
            .and_then(|extra| extra.anthropic.as_ref());
        assert!(anthropic.is_some());
        let Some(anthropic) = anthropic else { return };
        assert_eq!(anthropic.model.as_deref(), Some("claude-source"));
        assert_eq!(
            anthropic.blocks[0]
                .extra
                .get("vendor_marker")
                .and_then(json_string),
            Some("keep")
        );
    }

    #[test]
    fn openai_tool_call_kind_and_non_call_metadata_are_typed_rejections() {
        let invalid_kind: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","type":"custom","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
        )
        .expect("invalid tool call kind fixture");
        let invalid_kind_error = openai_chat_request_to_envelope_v2(invalid_kind).err();
        assert!(invalid_kind_error.is_some());
        let Some(invalid_kind_error) = invalid_kind_error else {
            return;
        };
        assert_eq!(invalid_kind_error.path, "messages[0].tool_calls[0].kind");
        assert_eq!(invalid_kind_error.reason, DirectIrReason::Unsupported);

        let description: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}","description":"unexpected"}}]}]}"#,
        )
        .expect("tool call description fixture");
        let description_error = openai_chat_request_to_envelope_v2(description).err();
        assert!(description_error.is_some());
        let Some(description_error) = description_error else {
            return;
        };
        assert_eq!(
            description_error.path,
            "messages[0].tool_calls[0].function.description"
        );

        let parameters: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}","parameters":{"type":"object"}}}]}]}"#,
        )
        .expect("tool call parameters fixture");
        let parameters_error = openai_chat_request_to_envelope_v2(parameters).err();
        assert!(parameters_error.is_some());
        let Some(parameters_error) = parameters_error else {
            return;
        };
        assert_eq!(
            parameters_error.path,
            "messages[0].tool_calls[0].function.parameters"
        );

        let strict: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}","strict":true}}]}]}"#,
        )
        .expect("tool call strict fixture");
        let strict_error = openai_chat_request_to_envelope_v2(strict).err();
        assert!(strict_error.is_some());
        let Some(strict_error) = strict_error else {
            return;
        };
        assert_eq!(
            strict_error.path,
            "messages[0].tool_calls[0].function.strict"
        );
    }

    #[test]
    fn openai_tool_declaration_arguments_and_tool_reasoning_are_rejected() {
        let declaration: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","tools":[{"type":"function","function":{"name":"lookup","arguments":"{}"}}]}"#,
        )
        .expect("tool declaration arguments fixture");
        let declaration_error = openai_chat_request_to_envelope_v2(declaration).err();
        assert!(declaration_error.is_some());
        let Some(declaration_error) = declaration_error else {
            return;
        };
        assert_eq!(declaration_error.path, "tools[0].function.arguments");

        let tool_result: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"tool","tool_call_id":"call-1","reasoning_content":"must-not-drop","content":"ok"}]}"#,
        )
        .expect("tool result reasoning fixture");
        let tool_result_error = openai_chat_request_to_envelope_v2(tool_result).err();
        assert!(tool_result_error.is_some());
        let Some(tool_result_error) = tool_result_error else {
            return;
        };
        assert_eq!(tool_result_error.path, "messages[0].reasoning_content");

        let mismatched_role: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","tool_call_id":"call-1","content":"ok"}]}"#,
        )
        .expect("mismatched tool result role fixture");
        let mismatched_role_error = openai_chat_request_to_envelope_v2(mismatched_role).err();
        assert!(mismatched_role_error.is_some());
        let Some(mismatched_role_error) = mismatched_role_error else {
            return;
        };
        assert_eq!(mismatched_role_error.path, "messages[0].role");
        assert_eq!(mismatched_role_error.reason, DirectIrReason::Mismatch);

        let missing_content: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"tool","tool_call_id":"call-1"}]}"#,
        )
        .expect("missing tool result content fixture");
        let missing_content_error = openai_chat_request_to_envelope_v2(missing_content).err();
        assert!(missing_content_error.is_some());
        let Some(missing_content_error) = missing_content_error else {
            return;
        };
        assert_eq!(missing_content_error.path, "messages[0].content");
    }

    #[test]
    fn openai_empty_assistant_content_binds_name_to_first_tool_call() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"assistant","name":"worker","tool_calls":[{"id":"call-1","type":"function","function":{"name":"lookup","arguments":"{}"}}]}]}"#,
        )
        .expect("empty assistant tool call fixture");
        let decoded = openai_chat_request_to_envelope_v2(request);
        assert!(decoded.is_ok());
        let Some(decoded) = decoded.ok() else { return };
        let call = decoded
            .envelope
            .ordered_items()
            .iter()
            .find(|item| matches!(&item.kind, ItemKind::ToolCall));
        assert!(call.is_some());
        let Some(call) = call else { return };
        assert_eq!(
            call.provenance
                .extensions
                .get("openai.name")
                .and_then(json_string),
            Some("worker")
        );
        let encoded = envelope_to_openai_chat_request_v2(decoded.envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        assert_eq!(encoded.value.messages[0].name.as_deref(), Some("worker"));
    }

    #[test]
    fn openai_content_shape_and_text_boundaries_are_explicit() {
        let mixed_text: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"text","text":"hello","image_url":{"url":"unexpected"}}]}]}"#,
        )
        .expect("mixed text content fixture");
        let mixed_error = openai_chat_request_to_envelope_v2(mixed_text).err();
        assert!(mixed_error.is_some());
        let Some(mixed_error) = mixed_error else {
            return;
        };
        assert_eq!(mixed_error.path, "messages[0].content[0].image_url");

        let missing_url: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"user","content":[{"type":"image_url","image_url":{"detail":"high"}}]}]}"#,
        )
        .expect("missing image URL fixture");
        let missing_url_error = openai_chat_request_to_envelope_v2(missing_url).err();
        assert!(missing_url_error.is_some());
        let Some(missing_url_error) = missing_url_error else {
            return;
        };
        assert_eq!(
            missing_url_error.path,
            "messages[0].content[0].image_url.url"
        );

        let mut envelope = Envelope::new(Protocol::OpenAi, "gpt-test");
        let mut item = Item::new(
            ItemKind::Message,
            Role::Assistant,
            Provenance::new(Protocol::OpenAi),
        );
        item.push_part(Part::text("first"));
        item.push_part(Part::text("second"));
        assert!(envelope.push_item(item).is_ok());
        let encoded = envelope_to_openai_chat_request_v2(envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        let Some(StringOrParts::Parts(parts)) = encoded.value.messages[0].content.as_ref() else {
            return;
        };
        assert_eq!(parts.len(), 2);
        assert_eq!(parts[0].text.as_deref(), Some("first"));
        assert_eq!(parts[1].text.as_deref(), Some("second"));
    }

    #[test]
    fn cross_protocol_item_metadata_gets_projection_loss() {
        let mut envelope = Envelope::new(Protocol::OpenAi, "gpt-test");
        let mut item = Item::new(
            ItemKind::Message,
            Role::User,
            Provenance::new(Protocol::OpenAi),
        );
        item.id = Some(OpaqueId::authentic("item-1", Protocol::OpenAi));
        item.raw = Some(JsonData::String("raw-item".to_owned()));
        item.extensions
            .insert("vendor.item".to_owned(), JsonData::Bool(true));
        item.push_part(Part::text("hello"));
        assert!(envelope.push_item(item).is_ok());
        let encoded = envelope_to_gemini_request_v2(envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        assert!(encoded.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("items[0].id") && loss.code == LossCode::LossUnknownEvent
        }));
        assert!(encoded.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("items[0].raw") && loss.code == LossCode::LossUnknownEvent
        }));
        assert!(encoded.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("items[0].extensions")
                && loss.code == LossCode::LossUnknownEvent
        }));
    }

    #[test]
    fn openai_tool_result_preserves_call_link_name_and_call_level_signatures() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[
              {"role":"assistant","tool_calls":[
                {"id":"call-a","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig-a"}}},
                {"id":"call-b","type":"function","function":{"name":"lookup","arguments":"{}"},"extra_content":{"google":{"thought_signature":"sig-b"}}}
              ]},
              {"role":"tool","tool_call_id":"call-b","name":"lookup","content":"result-b"}
            ]}"#,
        )
        .expect("OpenAI tool loop fixture");
        let direct_gemini = openai_chat_request_to_gemini_v2(request.clone(), "gemini-2.5-pro");
        assert!(direct_gemini.is_ok());
        let Some(direct_gemini) = direct_gemini.ok() else {
            return;
        };
        assert_eq!(
            direct_gemini.value.contents[0].parts[0]
                .function_call
                .as_ref()
                .and_then(|call| call.id.as_deref()),
            Some("call-a")
        );
        assert_eq!(
            direct_gemini.value.contents[2].parts[0]
                .function_response
                .as_ref()
                .and_then(|result| result.id.as_deref()),
            Some("call-b")
        );
        let direct_claude = openai_chat_request_to_claude_v2(request.clone());
        assert!(direct_claude.is_ok());
        let Some(direct_claude) = direct_claude.ok() else {
            return;
        };
        assert!(direct_claude.value.messages.iter().any(|message| {
            match &message.content {
                StringOrParts::Parts(blocks) => blocks
                    .iter()
                    .any(|block| block.kind == "tool_use" && block.id.as_deref() == Some("call-a")),
                StringOrParts::String(_) => false,
            }
        }));
        let decoded = openai_chat_request_to_envelope_v2(request);
        assert!(decoded.is_ok());
        let Some(decoded) = decoded.ok() else { return };
        let result_item = decoded
            .envelope
            .ordered_items()
            .iter()
            .find(|item| matches!(&item.kind, ItemKind::ToolResult));
        assert!(result_item.is_some());
        let Some(result_item) = result_item else {
            return;
        };
        assert_eq!(
            result_item.call_id.as_ref().map(|id| id.value.as_str()),
            Some("call-b")
        );
        assert_eq!(
            result_item
                .ordered_parts()
                .first()
                .and_then(|part| part.function.as_ref())
                .and_then(|function| function.name.as_deref()),
            Some("lookup")
        );
        let encoded = envelope_to_openai_chat_request_v2(decoded.envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        let assistant = encoded
            .value
            .messages
            .iter()
            .find(|message| message.role == "assistant");
        assert!(assistant.is_some());
        let Some(assistant) = assistant else { return };
        assert_eq!(assistant.tool_calls.len(), 2);
        assert_eq!(
            assistant.tool_calls[0]
                .extra_content
                .as_ref()
                .and_then(|extra| extra.google.as_ref())
                .and_then(|google| google.thought_signature.as_deref()),
            Some("sig-a")
        );
        assert_eq!(
            assistant.tool_calls[1]
                .extra_content
                .as_ref()
                .and_then(|extra| extra.google.as_ref())
                .and_then(|google| google.thought_signature.as_deref()),
            Some("sig-b")
        );
        let tool_message = encoded
            .value
            .messages
            .iter()
            .find(|message| message.role == "tool");
        assert_eq!(
            tool_message.and_then(|message| message.tool_call_id.as_deref()),
            Some("call-b")
        );
        assert_eq!(
            tool_message.and_then(|message| message.name.as_deref()),
            Some("lookup")
        );
        assert_eq!(
            tool_message.and_then(|message| match &message.content {
                Some(StringOrParts::String(value)) => Some(value.as_str()),
                _ => None,
            }),
            Some("result-b")
        );
    }

    #[test]
    fn openai_tool_result_without_id_is_a_typed_error() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"tool","content":"orphan"}]}"#,
        )
        .expect("missing tool id fixture");
        let error = openai_chat_request_to_envelope_v2(request).err();
        assert!(error.is_some());
        let Some(error) = error else { return };
        assert_eq!(error.feature, "function_call_id");
        assert_eq!(error.reason, DirectIrReason::MissingField);
        assert_eq!(error.path, "messages[0].tool_call_id");
    }

    #[test]
    fn response_usage_cache_and_billing_are_retained_with_projection_loss() {
        let response: OpenAiChatResponse = serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[{"index":0,"message":{"role":"assistant","content":"answer"},"finish_reason":"stop"}],"usage":{
              "prompt_tokens":10,"completion_tokens":5,"total_tokens":15,
              "prompt_tokens_details":{"cached_tokens":3,"cache_write_tokens":2},
              "completion_tokens_details":{"reasoning_tokens":1},
              "usage_source":"gateway","usage_semantic":"billable",
              "billing_usage":{"source":"gateway","semantic":"billable"}
            }}"#,
        )
        .expect("Chat response usage");
        let result = openai_chat_response_to_claude_v2(response);
        assert!(result.is_ok());
        let Ok(result) = result else { return };
        let Some(usage) = result.value.usage else {
            return;
        };
        assert_eq!(usage.input_tokens, 10);
        assert_eq!(usage.output_tokens, 5);
        assert_eq!(usage.cache_read_input_tokens, 3);
        assert!(usage.billing_usage.is_some());
        assert!(!result.losses.is_empty());
    }

    #[test]
    fn response_usage_unknown_fields_round_trip_through_ir_extensions() {
        let response: OpenAiChatResponse = serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[],"usage":{
              "prompt_tokens":10,"completion_tokens":5,"total_tokens":15,
              "prompt_tokens_details":{"cached_tokens":3,"future_cache_tokens":7},
              "future_usage":{"unit":"tokens"},
              "billing_usage":{"source":"gateway","semantic":"billable","future_charge":{"amount":"0.01"}}
            }}"#,
        )
        .expect("Chat response usage with provider extensions");
        let decoded = openai_chat_response_to_envelope_v2(response)
            .expect("decode usage extensions")
            .envelope;
        assert!(matches!(
            decoded
                .usage
                .as_ref()
                .and_then(|usage| usage.extensions.get("openai.usage.future_usage")),
            Some(JsonData::Object(_))
        ));
        assert!(matches!(
            decoded
                .usage
                .as_ref()
                .and_then(|usage| usage.cache.extensions.get("openai.prompt_tokens_details")),
            Some(JsonData::Object(details))
                if details.get("future_cache_tokens") == Some(&JsonData::Number(7.into()))
        ));
        assert!(matches!(
            decoded
                .usage
                .as_ref()
                .and_then(|usage| usage.billing.as_ref())
                .and_then(|billing| billing.extensions.get("billing.provider_payload")),
            Some(JsonData::Object(payload))
                if payload.contains_key("future_charge")
        ));

        let encoded = envelope_to_openai_chat_response_v2(decoded)
            .expect("encode usage extensions")
            .value;
        let usage = encoded.usage.expect("encoded usage");
        assert_eq!(
            usage.extra.get("future_usage"),
            Some(&JsonData::Object(BTreeMap::from([(
                "unit".to_owned(),
                JsonData::String("tokens".to_owned()),
            )])))
        );
        assert_eq!(
            usage
                .prompt_tokens_details
                .as_ref()
                .and_then(|details| details.extra.get("future_cache_tokens")),
            Some(&JsonData::Number(7.into()))
        );
        assert!(
            usage
                .billing_usage
                .as_ref()
                .and_then(|billing| billing.extra.get("future_charge"))
                .is_some()
        );
    }

    #[test]
    fn direct_claude_usage_round_trip_preserves_cache_creation_windows() {
        let response: ClaudeResponse = serde_json::from_str(
            r#"{"id":"msg-cache","type":"message","role":"assistant","model":"claude-test","content":[],"usage":{"input_tokens":10,"output_tokens":4,"cache_read_input_tokens":2,"cache_creation_input_tokens":9,"claude_cache_creation_5_m_tokens":6,"claude_cache_creation_1_h_tokens":3}}"#,
        )
        .expect("Claude usage response");
        let decoded = claude_response_to_envelope_v2(response).expect("decode Claude response");
        let encoded = envelope_to_claude_response_v2(decoded.envelope)
            .expect("encode Claude response")
            .value;
        let usage = encoded.usage.expect("Claude usage");

        assert_eq!(
            (
                usage.input_tokens,
                usage.output_tokens,
                usage.cache_read_input_tokens,
                usage.cache_creation_input_tokens,
                usage.claude_cache_creation_5_m_tokens,
                usage.claude_cache_creation_1_h_tokens,
            ),
            (10, 4, 2, 9, 6, 3)
        );
    }

    #[test]
    fn unsupported_stable_request_controls_are_recorded_for_targets() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"top_p":0.4,"reasoning_effort":"high","response_format":{"type":"json_object"},"parallel_tool_calls":true}"#,
        )
        .expect("request controls fixture");
        let claude = openai_chat_request_to_claude_v2(request.clone());
        assert!(claude.is_ok());
        let Some(claude) = claude.ok() else { return };
        assert!(claude.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent
                && loss.path.as_deref() == Some("controls.top_p")
        }));
        let gemini = openai_chat_request_to_gemini_v2(request, "gemini-2.5-pro");
        assert!(gemini.is_ok());
        let Some(gemini) = gemini.ok() else { return };
        assert!(gemini.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent
                && loss.path.as_deref() == Some("controls.parallel_tool_calls")
        }));
    }

    #[test]
    fn openai_strict_tool_roundtrips_and_cross_protocol_loss_is_explicit() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","tools":[{"type":"function","function":{"name":"lookup","parameters":{"type":"object"},"strict":true}}],"messages":[]}"#,
        )
        .expect("strict tool fixture");
        let decoded = openai_chat_request_to_envelope_v2(request);
        assert!(decoded.is_ok());
        let Some(decoded) = decoded.ok() else { return };
        let openai = envelope_to_openai_chat_request_v2(decoded.envelope.clone());
        assert!(openai.is_ok());
        let Some(openai) = openai.ok() else { return };
        assert_eq!(openai.value.tools[0].function.strict, Some(true));
        let claude = envelope_to_claude_request_v2(decoded.envelope);
        assert!(claude.is_ok());
        let Some(claude) = claude.ok() else { return };
        assert!(claude.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent
                && loss.path.as_deref() == Some("tools[0].strict")
        }));
    }

    #[test]
    fn target_tool_encoder_rejects_non_function_or_empty_names() {
        let mut builtin = Envelope::new(Protocol::OpenAi, "target-test");
        builtin.tools.push(Tool {
            kind: ToolKind::Builtin,
            name: Some("search".to_owned()),
            description: None,
            input_schema: None,
            extensions: BTreeMap::new(),
        });
        let builtin_error = envelope_to_gemini_request_v2(builtin).err();
        assert!(builtin_error.is_some());
        let Some(builtin_error) = builtin_error else {
            return;
        };
        assert_eq!(builtin_error.path, "tools[0]");
        assert_eq!(builtin_error.reason, DirectIrReason::Unsupported);

        let mut empty = Envelope::new(Protocol::OpenAi, "target-test");
        empty.tools.push(Tool {
            kind: ToolKind::Function,
            name: Some(String::new()),
            description: None,
            input_schema: None,
            extensions: BTreeMap::new(),
        });
        let empty_error = envelope_to_claude_request_v2(empty).err();
        assert!(empty_error.is_some());
        let Some(empty_error) = empty_error else {
            return;
        };
        assert_eq!(empty_error.path, "tools[0].name");
        assert_eq!(empty_error.reason, DirectIrReason::EmptyId);
    }

    #[test]
    fn conflicting_openai_token_limits_are_recorded_cross_protocol() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","max_tokens":32,"max_completion_tokens":64,"messages":[{"role":"user","content":"hello"}]}"#,
        )
        .expect("conflicting token limits fixture");
        let result = openai_chat_request_to_gemini_v2(request, "gemini-2.5-pro");
        assert!(result.is_ok());
        let Some(result) = result.ok() else { return };
        assert!(result.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent
                && loss.path.as_deref() == Some("openai.max_tokens")
        }));
    }

    #[test]
    fn claude_required_controls_do_not_silently_change_tool_choice_or_max_tokens() {
        let request: OpenAiChatRequest = serde_json::from_str(
            r#"{"model":"gpt-test","messages":[{"role":"user","content":"hello"}],"tool_choice":"none"}"#,
        )
        .expect("none tool choice fixture");
        let result = openai_chat_request_to_claude_v2(request);
        assert!(result.is_ok());
        let Some(result) = result.ok() else { return };
        assert_eq!(result.value.max_tokens, 1);
        assert_eq!(
            result
                .value
                .tool_choice
                .as_ref()
                .map(|choice| choice.kind.as_str()),
            Some("auto")
        );
        assert!(result.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("tool_choice") && loss.code == LossCode::LossUnknownEvent
        }));
        assert!(result.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("controls.max_output_tokens")
                && loss.code == LossCode::LossUnknownEvent
        }));
    }

    #[test]
    fn gemini_named_and_provider_tool_choices_record_projection_loss() {
        let mut named = Envelope::new(Protocol::OpenAi, "gemini-test");
        named.tool_choice = ToolChoice::Named {
            name: "lookup".to_owned(),
        };
        let named_result = envelope_to_gemini_request_v2(named);
        assert!(named_result.is_ok());
        let Some(named_result) = named_result.ok() else {
            return;
        };
        assert_eq!(
            named_result
                .value
                .tool_config
                .as_ref()
                .map(|config| config.function_calling_config.mode.as_str()),
            Some("ANY")
        );
        assert!(named_result.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("tool_choice.name")
                && loss.code == LossCode::LossUnknownEvent
        }));

        let mut provider = Envelope::new(Protocol::OpenAi, "gemini-test");
        provider.tool_choice = ToolChoice::Provider {
            raw: JsonData::String("provider-choice".to_owned()),
        };
        let provider_result = envelope_to_gemini_request_v2(provider);
        assert!(provider_result.is_ok());
        let Some(provider_result) = provider_result.ok() else {
            return;
        };
        assert_eq!(
            provider_result
                .value
                .tool_config
                .as_ref()
                .map(|config| config.function_calling_config.mode.as_str()),
            Some("AUTO")
        );
        assert!(provider_result.losses.as_slice().iter().any(|loss| {
            loss.path.as_deref() == Some("tool_choice") && loss.code == LossCode::LossUnknownEvent
        }));
    }

    #[test]
    fn gemini_multiple_candidate_safety_ratings_record_safety_loss() {
        let response: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"index":0,"content":{"role":"model","parts":[{"text":"first"}]},"safetyRatings":{"blocked":false}},{"index":1,"content":{"role":"model","parts":[{"text":"second"}]},"safetyRatings":{"blocked":true}}]}"#,
        )
        .expect("multi-candidate Gemini response");
        let decoded = gemini_response_to_envelope_v2(response, "gemini-test");
        assert!(decoded.is_ok());
        let Some(decoded) = decoded.ok() else { return };
        let encoded = envelope_to_gemini_response_v2(decoded.envelope);
        assert!(encoded.is_ok());
        let Some(encoded) = encoded.ok() else { return };
        assert_eq!(encoded.value.candidates.len(), 1);
        assert!(encoded.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossSafetyMetadata
                && loss.path.as_deref() == Some("gemini.candidate[].safetyRatings")
        }));
    }

    #[test]
    fn nonzero_openai_choice_and_gemini_candidate_indices_roundtrip() {
        let openai: OpenAiChatResponse = serde_json::from_str(
            r#"{"id":"chat-id","object":"chat.completion","created":1,"model":"gpt-test","choices":[{"index":7,"message":{"role":"assistant","content":"answer"}}]}"#,
        )
        .expect("nonzero Chat choice fixture");
        let openai_decoded = openai_chat_response_to_envelope_v2(openai);
        assert!(openai_decoded.is_ok());
        let Some(openai_decoded) = openai_decoded.ok() else {
            return;
        };
        let openai_encoded = envelope_to_openai_chat_response_v2(openai_decoded.envelope);
        assert!(openai_encoded.is_ok());
        let Some(openai_encoded) = openai_encoded.ok() else {
            return;
        };
        assert_eq!(openai_encoded.value.choices[0].index, 7);

        let gemini: GeminiResponse = serde_json::from_str(
            r#"{"candidates":[{"index":9,"content":{"role":"model","parts":[{"text":"answer"}]}}]}"#,
        )
        .expect("nonzero Gemini candidate fixture");
        let gemini_decoded = gemini_response_to_envelope_v2(gemini, "gemini-test");
        assert!(gemini_decoded.is_ok());
        let Some(gemini_decoded) = gemini_decoded.ok() else {
            return;
        };
        let gemini_encoded = envelope_to_gemini_response_v2(gemini_decoded.envelope);
        assert!(gemini_encoded.is_ok());
        let Some(gemini_encoded) = gemini_encoded.ok() else {
            return;
        };
        assert_eq!(gemini_encoded.value.candidates[0].index, Some(9));
    }

    #[test]
    fn unrepresentable_response_metadata_and_synthetic_ids_are_recorded() {
        let claude_to_gemini = claude_response_to_gemini_v2(claude_response(), "gemini-test");
        assert!(claude_to_gemini.is_ok());
        let Some(claude_to_gemini) = claude_to_gemini.ok() else {
            return;
        };
        assert!(claude_to_gemini.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent
                && loss.path.as_deref() == Some("claude.response_metadata")
        }));

        let gemini_to_openai = gemini_response_to_openai_chat_v2(gemini_response(), "gemini-test");
        assert!(gemini_to_openai.is_ok());
        let Some(gemini_to_openai) = gemini_to_openai.ok() else {
            return;
        };
        assert_eq!(gemini_to_openai.value.id, "direct-ir-response");
        assert!(gemini_to_openai.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent && loss.path.as_deref() == Some("response.id")
        }));

        let gemini_to_claude = gemini_response_to_claude_v2(gemini_response(), "gemini-test");
        assert!(gemini_to_claude.is_ok());
        let Some(gemini_to_claude) = gemini_to_claude.ok() else {
            return;
        };
        assert_eq!(gemini_to_claude.value.id, "direct-ir-response");
        assert!(gemini_to_claude.losses.as_slice().iter().any(|loss| {
            loss.code == LossCode::LossUnknownEvent && loss.path.as_deref() == Some("response.id")
        }));
    }

    #[test]
    fn direct_error_has_path_without_source_body() {
        let request: ClaudeRequest = serde_json::from_str(
            r#"{"model":"claude-test","max_tokens":128,"messages":[{"role":"assistant","content":[{"type":"tool_use","name":"secret-name","input":{"secret":"do-not-display"}}]}]}"#,
        )
        .expect("invalid Claude request fixture");
        let error = claude_request_to_openai_chat_v2(request).err();
        assert!(error.is_some());
        let Some(error) = error else { return };
        let display = error.to_string();
        assert!(display.contains("tool_use.id"));
        assert!(!display.contains("do-not-display"));
        assert!(!display.contains("secret-name"));
        assert_eq!(error.source, Protocol::Claude);
        assert_eq!(error.target, Protocol::OpenAi);
        assert!(!error.retryable);
    }

    #[test]
    fn direct_module_has_no_legacy_converter_dependency() {
        let source = include_str!("ir_convert.rs");
        let legacy_function_marker = ["to", "_", "canonical"].concat();
        let legacy_prefix_marker = ["canonical", "_"].concat();
        assert!(!source.contains(&legacy_function_marker));
        assert!(!source.contains(&legacy_prefix_marker));
        assert!(source.contains("wire→Envelope→wire") || source.contains("source decode/map"));
    }
}
