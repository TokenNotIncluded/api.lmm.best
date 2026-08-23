//! Runtime-owned protocol capability catalog.
//!
//! The contracts crate describes route claims, but it cannot know which HTTP
//! handlers are actually mounted. This module is the application-owned
//! inventory for native passthrough routes and cortexfs-backed cross-protocol
//! conversions, and is checked against the contracts registry before the
//! listener starts.

use std::collections::BTreeSet;

use lmm_contracts::relay::{
    Direction, Fidelity, ModelConstraint, Protocol, Registry, RegistryValidationError,
    RouteRegistration, RuntimeCatalog, RuntimeRoute, SupportMatrix, ValidatedRegistry, protocols,
};

use crate::cortexfs_protocol_bridge::{converter_id, runtime_adaptor_id, stream_finalizer_id};

const RUNTIME_CATALOG_VERSION: &str = "api-rust-cortexfs-relay-v2";

/// Converter ID registered by the OpenAI Chat native passthrough handler.
pub const OPENAI_CHAT_RAW_CONVERTER: &str = "raw-openai-chat-v1";
/// Converter ID registered by the OpenAI Responses native passthrough handler.
pub const OPENAI_RESPONSES_RAW_CONVERTER: &str = "raw-openai-responses-v1";
/// Converter ID registered by the Claude native passthrough handler.
pub const CLAUDE_RAW_CONVERTER: &str = "raw-claude-messages-v1";
/// Converter ID registered by the Gemini native passthrough handler.
pub const GEMINI_RAW_CONVERTER: &str = "raw-gemini-generate-content-v1";

const RAW_RUNTIME_ROWS: [(Protocol, &str, &str); 4] = [
    (
        Protocol::OpenAi,
        OPENAI_CHAT_RAW_CONVERTER,
        "raw-openai-chat-v1-runtime",
    ),
    (
        Protocol::OpenAiResponses,
        OPENAI_RESPONSES_RAW_CONVERTER,
        "raw-openai-responses-v1-runtime",
    ),
    (
        Protocol::Claude,
        CLAUDE_RAW_CONVERTER,
        "raw-claude-messages-v1-runtime",
    ),
    (
        Protocol::Gemini,
        GEMINI_RAW_CONVERTER,
        "raw-gemini-generate-content-v1-runtime",
    ),
];

fn native_runtime_route(source: Protocol, converter: &str, adaptor: &str) -> RuntimeRoute {
    let mut route = RuntimeRoute::new(source, source);
    route.request_converter_id = Some(converter.to_owned());
    route.response_converter_id = Some(converter.to_owned());
    route.stream_converter_id = Some(converter.to_owned());
    route.stream_finalizer_id = Some(format!("{converter}-finalizer"));
    route.runtime_adaptors.insert(adaptor.to_owned());
    route
}

fn cortexfs_runtime_route(source: Protocol, target: Protocol) -> RuntimeRoute {
    let mut route = RuntimeRoute::new(source, target);
    route.request_converter_id = Some(converter_id(source, target, Direction::Request));
    route.response_converter_id = Some(converter_id(source, target, Direction::Response));
    route.stream_converter_id = Some(converter_id(source, target, Direction::Stream));
    route.stream_finalizer_id = Some(stream_finalizer_id(source, target));
    route
        .runtime_adaptors
        .insert(runtime_adaptor_id(source, target));
    route
}

/// Builds the catalog from the native passthrough handlers and cortexfs bridge.
#[must_use]
pub fn current_runtime_catalog() -> RuntimeCatalog {
    let mut converters = BTreeSet::new();
    let mut finalizers = BTreeSet::new();
    let mut adaptors = BTreeSet::new();
    let mut routes = Vec::new();

    for (protocol, converter, adaptor) in RAW_RUNTIME_ROWS {
        converters.insert((*converter).to_owned());
        finalizers.insert(format!("{converter}-finalizer"));
        adaptors.insert((*adaptor).to_owned());
        routes.push(native_runtime_route(protocol, converter, adaptor));
    }

    for source in protocols() {
        for target in protocols() {
            if source == target {
                continue;
            }
            let route = cortexfs_runtime_route(source, target);
            for converter in [
                route.request_converter_id.as_deref(),
                route.response_converter_id.as_deref(),
                route.stream_converter_id.as_deref(),
            ]
            .into_iter()
            .flatten()
            {
                converters.insert(converter.to_owned());
            }
            if let Some(finalizer) = route.stream_finalizer_id.as_ref() {
                finalizers.insert(finalizer.clone());
            }
            adaptors.extend(route.runtime_adaptors.iter().cloned());
            routes.push(route);
        }
    }

    RuntimeCatalog::new(
        RUNTIME_CATALOG_VERSION,
        converters,
        finalizers,
        adaptors,
        routes,
    )
}

/// Validates the current contracts registry against the mounted runtime.
pub fn validated_current_registry() -> Result<ValidatedRegistry, RegistryValidationError> {
    validate_registry_against_catalog(&current_runtime_catalog())
}

/// Validates the built-in claims against an explicitly supplied runtime
/// catalog.  Keeping this seam public makes startup and offline matrix checks
/// use exactly the same registry/runtime consistency rule; callers cannot
/// accidentally validate only converter IDs while skipping direction wiring.
pub fn validate_registry_against_catalog(
    catalog: &RuntimeCatalog,
) -> Result<ValidatedRegistry, RegistryValidationError> {
    Registry::current().validate_against_catalog(catalog)
}

/// Validates any explicit registry against live runtime metadata.
///
/// This is the test/deployment seam for registry changes: a newly generated
/// matrix is never considered executable until request, response, stream,
/// finalizer, adaptor, quality, and feature claims all agree with the runtime
/// catalog.
pub fn validate_explicit_registry_against_catalog(
    registry: &Registry,
    catalog: &RuntimeCatalog,
) -> Result<ValidatedRegistry, RegistryValidationError> {
    registry.validate_against_catalog(catalog)
}

/// Performs the startup fail-closed runtime drift check.
pub fn validate_protocol_runtime() -> Result<(), RegistryValidationError> {
    validated_current_registry().map(|_| ())
}

/// Returns the machine-readable matrix only after runtime validation passes.
pub fn current_support_matrix() -> Result<SupportMatrix, RegistryValidationError> {
    validated_current_registry().map(|registry| registry.support_matrix().clone())
}

/// The executable capability selected for one route and model family.
///
/// The contracts registry owns the quality, direction, and model-constraint
/// claims.  This value is a small application-facing projection of that
/// validated claim, so a caller can make a route decision without reconstructing
/// capability rules from converter IDs.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct RuntimeRouteCapability {
    /// Source protocol accepted by the route.
    pub source: Protocol,
    /// Target protocol emitted by the route.
    pub target: Protocol,
    /// Model family checked against the route constraints.
    pub model_family: String,
    /// Route fidelity/quality.
    pub quality: Fidelity,
    /// Whether the route uses raw same-protocol bytes.
    pub raw_passthrough: bool,
    /// Whether request conversion is wired.
    pub request_supported: bool,
    /// Whether response conversion is wired.
    pub response_supported: bool,
    /// Whether stream conversion is wired.
    pub stream_supported: bool,
    /// Request converter ID, if wired.
    pub request_converter_id: Option<String>,
    /// Response converter ID, if wired.
    pub response_converter_id: Option<String>,
    /// Stream converter ID, if wired.
    pub stream_converter_id: Option<String>,
    /// Stream finalizer ID, if wired.
    pub stream_finalizer_id: Option<String>,
}

impl RuntimeRouteCapability {
    /// Returns whether a direction is executable for this validated route.
    #[must_use]
    pub const fn supports(&self, direction: Direction) -> bool {
        match direction {
            Direction::Request => self.request_supported,
            Direction::Response => self.response_supported,
            Direction::Stream => self.stream_supported,
        }
    }

    /// Returns whether the route has a usable quality claim and direction.
    #[must_use]
    pub const fn is_executable(&self, direction: Direction) -> bool {
        self.quality.is_supported() && self.supports(direction)
    }
}

/// Fail-closed errors from selecting a validated route capability.
#[derive(Clone, Debug, Eq, PartialEq)]
pub enum RuntimeCapabilityError {
    /// The registry/runtime catalog did not validate.
    Registry(RegistryValidationError),
    /// A complete matrix unexpectedly had no route entry.
    MissingRoute { source: Protocol, target: Protocol },
    /// The model family is excluded by the route's model constraints.
    ModelConstraint {
        source: Protocol,
        target: Protocol,
        model_family: String,
    },
    /// The route exists but does not claim this direction.
    UnsupportedDirection {
        source: Protocol,
        target: Protocol,
        direction: Direction,
    },
}

impl std::fmt::Display for RuntimeCapabilityError {
    fn fmt(&self, formatter: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        match self {
            Self::Registry(error) => write!(formatter, "protocol registry invalid: {error}"),
            Self::MissingRoute { source, target } => {
                write!(
                    formatter,
                    "protocol route missing: {source:?} -> {target:?}"
                )
            }
            Self::ModelConstraint {
                source,
                target,
                model_family,
            } => write!(
                formatter,
                "model family {model_family:?} is not allowed for {source:?} -> {target:?}"
            ),
            Self::UnsupportedDirection {
                source,
                target,
                direction,
            } => write!(
                formatter,
                "direction {direction:?} is unsupported for {source:?} -> {target:?}"
            ),
        }
    }
}

impl std::error::Error for RuntimeCapabilityError {}

/// Selects one executable capability only after registry/runtime validation.
///
/// Model constraints and direction claims are checked before returning a
/// capability.  An unsupported cross-protocol route therefore cannot be
/// enabled merely because a caller supplied a converter name.
pub fn current_route_capability(
    source: Protocol,
    target: Protocol,
    model_family: &str,
    direction: Direction,
) -> Result<RuntimeRouteCapability, RuntimeCapabilityError> {
    let registry = validated_current_registry().map_err(RuntimeCapabilityError::Registry)?;
    route_capability_from_validated(&registry, source, target, model_family, direction)
}

/// Selects one route capability from an already validated registry snapshot.
pub fn route_capability_from_validated(
    registry: &ValidatedRegistry,
    source: Protocol,
    target: Protocol,
    model_family: &str,
    direction: Direction,
) -> Result<RuntimeRouteCapability, RuntimeCapabilityError> {
    let route = registry
        .route(source, target)
        .ok_or(RuntimeCapabilityError::MissingRoute { source, target })?;
    if !route.matches_model_family(model_family) {
        return Err(RuntimeCapabilityError::ModelConstraint {
            source,
            target,
            model_family: model_family.to_owned(),
        });
    }
    let direction_supported = match direction {
        Direction::Request => route.request_supported,
        Direction::Response => route.response_supported,
        Direction::Stream => route.stream_supported,
    };
    if !direction_supported {
        return Err(RuntimeCapabilityError::UnsupportedDirection {
            source,
            target,
            direction,
        });
    }
    Ok(capability_from_route(route, model_family))
}

/// Returns a route projection without requiring a direction to be enabled.
///
/// This is useful for support-matrix/ownership diagnostics, where an
/// unsupported route must be reported as closed rather than treated as a
/// conversion error with a guessed direction.
pub fn current_route_projection(
    source: Protocol,
    target: Protocol,
    model_family: &str,
) -> Result<RuntimeRouteCapability, RuntimeCapabilityError> {
    let registry = validated_current_registry().map_err(RuntimeCapabilityError::Registry)?;
    let route = registry
        .route(source, target)
        .ok_or(RuntimeCapabilityError::MissingRoute { source, target })?;
    if !route.matches_model_family(model_family) {
        return Err(RuntimeCapabilityError::ModelConstraint {
            source,
            target,
            model_family: model_family.to_owned(),
        });
    }
    Ok(capability_from_route(route, model_family))
}

fn capability_from_route(route: &RouteRegistration, model_family: &str) -> RuntimeRouteCapability {
    RuntimeRouteCapability {
        source: route.source,
        target: route.target,
        model_family: model_family.to_owned(),
        quality: route.quality,
        raw_passthrough: route.raw_passthrough,
        request_supported: route.request_supported,
        response_supported: route.response_supported,
        stream_supported: route.stream_supported,
        request_converter_id: route.request_converter_id.clone(),
        response_converter_id: route.response_converter_id.clone(),
        stream_converter_id: route.stream_converter_id.clone(),
        stream_finalizer_id: route.stream_finalizer_id.clone(),
    }
}

/// Returns whether a catalog direction is deliberately native raw passthrough.
pub const fn is_native_raw_direction(direction: Direction) -> bool {
    matches!(
        direction,
        Direction::Request | Direction::Response | Direction::Stream
    )
}

/// Returns whether a complete source/target route is native raw passthrough.
#[must_use]
pub fn is_native_raw_route(source: Protocol, target: Protocol) -> bool {
    source == target
}

/// Returns whether a particular direction belongs to a native raw route.
#[must_use]
pub fn is_native_raw_route_direction(
    source: Protocol,
    target: Protocol,
    direction: Direction,
) -> bool {
    is_native_raw_route(source, target) && is_native_raw_direction(direction)
}

/// Returns the model constraint claimed by a validated route.
pub fn current_model_constraints(
    source: Protocol,
    target: Protocol,
) -> Result<BTreeSet<ModelConstraint>, RuntimeCapabilityError> {
    let registry = validated_current_registry().map_err(RuntimeCapabilityError::Registry)?;
    let route = registry
        .route(source, target)
        .ok_or(RuntimeCapabilityError::MissingRoute { source, target })?;
    Ok(route.model_constraints.clone())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn current_catalog_validates_a_complete_sixteen_route_matrix() {
        let registry = validated_current_registry().expect("runtime catalog validates");
        assert_eq!(registry.support_matrix().routes.len(), 16);
    }

    #[test]
    fn current_catalog_has_four_native_and_twelve_cortexfs_routes() {
        let catalog = current_runtime_catalog();
        assert_eq!(catalog.routes.len(), 16);
        assert_eq!(
            catalog
                .routes
                .iter()
                .filter(|route| route.source == route.target)
                .count(),
            4
        );
        assert_eq!(
            catalog
                .routes
                .iter()
                .filter(|route| route.source != route.target)
                .count(),
            12
        );
    }

    #[test]
    fn openai_chat_to_responses_is_supported_with_cortexfs_converters() {
        let registry = validated_current_registry().expect("runtime catalog validates");
        let route = registry
            .route(Protocol::OpenAi, Protocol::OpenAiResponses)
            .expect("complete matrix route");
        assert!(route.request_supported);
        assert!(route.response_supported);
        assert!(route.stream_supported);
        assert_eq!(route.quality, Fidelity::Normalized);
    }

    #[test]
    fn native_route_capability_exposes_all_directions_and_raw_quality() {
        let capability =
            current_route_capability(Protocol::OpenAi, Protocol::OpenAi, "gpt", Direction::Stream)
                .expect("native route is executable");
        assert_eq!(capability.quality, Fidelity::Exact);
        assert!(capability.raw_passthrough);
        assert!(capability.is_executable(Direction::Request));
        assert!(capability.is_executable(Direction::Response));
        assert!(capability.is_executable(Direction::Stream));
        assert_eq!(
            capability.request_converter_id.as_deref(),
            Some(OPENAI_CHAT_RAW_CONVERTER)
        );
    }

    #[test]
    fn cross_protocol_capability_is_executable_when_registry_and_runtime_agree() {
        let capability = current_route_capability(
            Protocol::OpenAi,
            Protocol::Claude,
            "claude",
            Direction::Request,
        )
        .expect("cross route is executable");
        assert_eq!(capability.quality, Fidelity::Normalized);
        assert!(!capability.raw_passthrough);
        assert!(capability.is_executable(Direction::Request));
    }

    #[test]
    fn explicit_registry_validation_rejects_raw_converter_on_cross_route() {
        let registry = Registry::current();
        let mut catalog = current_runtime_catalog();
        let route = catalog
            .routes
            .iter_mut()
            .find(|route| route.source == Protocol::OpenAi && route.target == Protocol::Claude)
            .expect("cross runtime route");
        route.request_converter_id = Some(OPENAI_RESPONSES_RAW_CONVERTER.to_owned());
        let result = validate_explicit_registry_against_catalog(&registry, &catalog);
        assert!(matches!(
            result,
            Err(RegistryValidationError::RuntimeDirectionMismatch { .. })
        ));
    }
}
