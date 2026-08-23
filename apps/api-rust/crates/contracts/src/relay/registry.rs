//! The single source of truth for relay route capability claims.
//!
//! A route is represented for every source/target protocol pair, even when it
//! is unsupported. This is deliberate: consumers can render a complete
//! machine-readable matrix without inferring support from the presence of a
//! converter. The built-in registry only enables same-protocol raw routes.
//! Runtime wiring is supplied by an independent [`RuntimeCatalog`] so this
//! crate cannot accidentally certify its own capability claims.

use std::{collections::BTreeSet, error::Error, fmt};

use serde::{Deserialize, Serialize};

use super::{Feature, Fidelity, Protocol};

const REGISTRY_VERSION: &str = "relay-capabilities-v2-cortexfs";

const CORTEXFS_CONVERTER_PREFIX: &str = "cortexfs";
const CORTEXFS_RUNTIME_PREFIX: &str = "cortexfs-runtime";

const RAW_OPENAI_CHAT: &str = "raw-openai-chat-v1";
const RAW_OPENAI_RESPONSES: &str = "raw-openai-responses-v1";
const RAW_CLAUDE: &str = "raw-claude-messages-v1";
const RAW_GEMINI: &str = "raw-gemini-generate-content-v1";

/// A model-family constraint attached to a route.
#[derive(Clone, Debug, Eq, Hash, Ord, PartialEq, PartialOrd, Serialize, Deserialize)]
pub struct ModelConstraint {
    /// Family name accepted by this route. '*' accepts every family.
    pub family: String,
    /// Optional model-family prefixes accepted in addition to 'family'.
    #[serde(default, skip_serializing_if = "BTreeSet::is_empty")]
    pub model_prefixes: BTreeSet<String>,
}

impl ModelConstraint {
    /// Creates a constraint that accepts every model family.
    pub fn any() -> Self {
        Self {
            family: "*".to_owned(),
            model_prefixes: BTreeSet::new(),
        }
    }

    /// Creates a constraint for one exact family.
    pub fn family(family: impl Into<String>) -> Self {
        Self {
            family: family.into(),
            model_prefixes: BTreeSet::new(),
        }
    }

    /// Returns whether a model-family string satisfies this constraint.
    pub fn matches(&self, model_family: &str) -> bool {
        if self.family == "*" || self.family.eq_ignore_ascii_case(model_family) {
            return true;
        }
        self.model_prefixes
            .iter()
            .any(|prefix| model_family.starts_with(prefix))
    }
}

/// A route registration containing independent request, response, and stream
/// support claims.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct RouteRegistration {
    /// Source protocol accepted by the route.
    pub source: Protocol,
    /// Target protocol emitted by the route.
    pub target: Protocol,
    /// Whether request conversion is connected at runtime.
    pub request_supported: bool,
    /// Whether response conversion is connected at runtime.
    pub response_supported: bool,
    /// Whether stream conversion is connected at runtime.
    pub stream_supported: bool,
    /// Request converter identifier, when request support is claimed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request_converter_id: Option<String>,
    /// Response converter identifier, when response support is claimed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub response_converter_id: Option<String>,
    /// Stream converter identifier, when stream support is claimed.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream_converter_id: Option<String>,
    /// Stream finalizer identifier, required for a supported stream route.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream_finalizer_id: Option<String>,
    /// Runtime adaptors that are known to execute this route.
    #[serde(default, skip_serializing_if = "Vec::is_empty")]
    pub runtime_adaptors: Vec<String>,
    /// Features required by the route's supported surface.
    #[serde(default, skip_serializing_if = "BTreeSet::is_empty")]
    pub feature_requirements: BTreeSet<Feature>,
    /// Features explicitly unavailable on this route.
    #[serde(default, skip_serializing_if = "BTreeSet::is_empty")]
    pub unsupported_features: BTreeSet<Feature>,
    /// Model-family constraints for this route.
    #[serde(default, skip_serializing_if = "BTreeSet::is_empty")]
    pub model_constraints: BTreeSet<ModelConstraint>,
    /// Quality advertised by this route.
    pub quality: Fidelity,
    /// Version of the converter/runtime contract.
    pub version: String,
    /// Whether all directions use original bytes without decoding.
    pub raw_passthrough: bool,
}

impl RouteRegistration {
    /// Creates an unsupported route with every feature explicitly unavailable.
    pub fn unsupported(source: Protocol, target: Protocol) -> Self {
        Self {
            source,
            target,
            request_supported: false,
            response_supported: false,
            stream_supported: false,
            request_converter_id: None,
            response_converter_id: None,
            stream_converter_id: None,
            stream_finalizer_id: None,
            runtime_adaptors: Vec::new(),
            feature_requirements: BTreeSet::new(),
            unsupported_features: all_features(),
            model_constraints: BTreeSet::new(),
            quality: Fidelity::Unsupported,
            version: REGISTRY_VERSION.to_owned(),
            raw_passthrough: false,
        }
    }

    /// Creates a raw passthrough route for one protocol.
    pub fn raw(protocol: Protocol) -> Self {
        let converter_id = raw_converter_id(protocol).to_owned();
        let finalizer_id = format!("{converter_id}-finalizer");
        let runtime = format!("{converter_id}-runtime");
        Self {
            source: protocol,
            target: protocol,
            request_supported: true,
            response_supported: true,
            stream_supported: true,
            request_converter_id: Some(converter_id.clone()),
            response_converter_id: Some(converter_id.clone()),
            stream_converter_id: Some(converter_id),
            stream_finalizer_id: Some(finalizer_id),
            runtime_adaptors: vec![runtime],
            feature_requirements: all_features(),
            unsupported_features: BTreeSet::new(),
            model_constraints: BtreeSetExt::one(ModelConstraint::any()),
            quality: Fidelity::Exact,
            version: REGISTRY_VERSION.to_owned(),
            raw_passthrough: true,
        }
    }

    /// Creates a cortexfs-backed cross-protocol route aligned with the Go relay matrix.
    pub fn cortexfs_cross(source: Protocol, target: Protocol) -> Self {
        let request_converter = cortexfs_converter_id(source, target, Direction::Request);
        let response_converter = cortexfs_converter_id(source, target, Direction::Response);
        let stream_converter = cortexfs_converter_id(source, target, Direction::Stream);
        let stream_finalizer = cortexfs_stream_finalizer_id(source, target);
        let runtime = cortexfs_runtime_adaptor_id(source, target);
        Self {
            source,
            target,
            request_supported: true,
            response_supported: true,
            stream_supported: true,
            request_converter_id: Some(request_converter),
            response_converter_id: Some(response_converter),
            stream_converter_id: Some(stream_converter),
            stream_finalizer_id: Some(stream_finalizer),
            runtime_adaptors: vec![runtime],
            feature_requirements: all_features(),
            unsupported_features: BTreeSet::new(),
            model_constraints: BtreeSetExt::one(ModelConstraint::any()),
            quality: cross_protocol_fidelity(source, target),
            version: REGISTRY_VERSION.to_owned(),
            raw_passthrough: false,
        }
    }

    /// Returns whether at least one execution direction is supported.
    pub const fn supports_any_direction(&self) -> bool {
        self.request_supported || self.response_supported || self.stream_supported
    }

    /// Returns the first runtime adaptor, if one is registered.
    pub fn runtime_adaptor(&self) -> Option<&str> {
        self.runtime_adaptors.first().map(String::as_str)
    }

    /// Returns all converter identifiers in deterministic field order.
    pub fn converter_ids(&self) -> Vec<&str> {
        [
            self.request_converter_id.as_deref(),
            self.response_converter_id.as_deref(),
            self.stream_converter_id.as_deref(),
        ]
        .into_iter()
        .flatten()
        .collect()
    }

    /// Returns whether the route accepts this model family.
    pub fn matches_model_family(&self, model_family: &str) -> bool {
        self.model_constraints.is_empty()
            || self
                .model_constraints
                .iter()
                .any(|constraint| constraint.matches(model_family))
    }

    /// Returns request support as a small machine-readable capability object.
    pub fn request(&self) -> DirectionCapability {
        DirectionCapability {
            supported: self.request_supported,
            converter_id: self.request_converter_id.clone(),
            finalizer_id: None,
        }
    }

    /// Returns response support as a small machine-readable capability object.
    pub fn response(&self) -> DirectionCapability {
        DirectionCapability {
            supported: self.response_supported,
            converter_id: self.response_converter_id.clone(),
            finalizer_id: None,
        }
    }

    /// Returns stream support as a small machine-readable capability object.
    pub fn stream(&self) -> DirectionCapability {
        DirectionCapability {
            supported: self.stream_supported,
            converter_id: self.stream_converter_id.clone(),
            finalizer_id: self.stream_finalizer_id.clone(),
        }
    }
}

/// Runtime-side direction metadata supplied by the application.
///
/// This is deliberately separate from [`RouteRegistration`]. The contracts
/// crate describes claims, while the application supplies the IDs that its
/// actual handlers register at runtime.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct RuntimeRoute {
    /// Source protocol accepted by the runtime adaptor.
    pub source: Protocol,
    /// Target protocol emitted by the runtime adaptor.
    pub target: Protocol,
    /// Request converter actually wired for this pair, if any.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub request_converter_id: Option<String>,
    /// Response converter actually wired for this pair, if any.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub response_converter_id: Option<String>,
    /// Stream converter actually wired for this pair, if any.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream_converter_id: Option<String>,
    /// Stream finalizer actually wired for this pair, if any.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub stream_finalizer_id: Option<String>,
    /// Runtime adaptors that own this route.
    #[serde(default, skip_serializing_if = "BTreeSet::is_empty")]
    pub runtime_adaptors: BTreeSet<String>,
}

impl RuntimeRoute {
    /// Creates explicit route metadata from independently registered IDs.
    #[must_use]
    pub fn new(source: Protocol, target: Protocol) -> Self {
        Self {
            source,
            target,
            request_converter_id: None,
            response_converter_id: None,
            stream_converter_id: None,
            stream_finalizer_id: None,
            runtime_adaptors: BTreeSet::new(),
        }
    }

    /// Returns whether at least one runtime direction is wired.
    pub fn supports_any_direction(&self) -> bool {
        self.request_converter_id.is_some()
            || self.response_converter_id.is_some()
            || self.stream_converter_id.is_some()
    }
}

/// Independently supplied inventory of converter, finalizer and adaptor IDs.
///
/// A catalog must be built by the application from real runtime handlers. It
/// is intentionally injectable so tests can remove one ID or alter one route
/// and prove that registry claims fail closed.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct RuntimeCatalog {
    /// Runtime catalog schema/version.
    pub version: String,
    /// Converter IDs registered by live runtime code.
    pub converters: BTreeSet<String>,
    /// Stream finalizer IDs registered by live runtime code.
    pub finalizers: BTreeSet<String>,
    /// Runtime adaptor IDs registered by live runtime code.
    pub adaptors: BTreeSet<String>,
    /// Per-route direction metadata from live runtime code.
    pub routes: Vec<RuntimeRoute>,
}

impl RuntimeCatalog {
    /// Creates an injectable runtime inventory.
    #[must_use]
    pub fn new(
        version: impl Into<String>,
        converters: impl IntoIterator<Item = String>,
        finalizers: impl IntoIterator<Item = String>,
        adaptors: impl IntoIterator<Item = String>,
        routes: impl IntoIterator<Item = RuntimeRoute>,
    ) -> Self {
        Self {
            version: version.into(),
            converters: converters.into_iter().collect(),
            finalizers: finalizers.into_iter().collect(),
            adaptors: adaptors.into_iter().collect(),
            routes: routes.into_iter().collect(),
        }
    }

    /// Finds runtime metadata for one source/target pair.
    pub fn route(&self, source: Protocol, target: Protocol) -> Option<&RuntimeRoute> {
        self.routes
            .iter()
            .find(|route| route.source == source && route.target == target)
    }

    fn validate(&self) -> Result<(), RegistryValidationError> {
        if self.version.trim().is_empty() {
            return Err(RegistryValidationError::EmptyRuntimeCatalogVersion);
        }
        let mut seen = BTreeSet::new();
        for route in &self.routes {
            let key = (protocol_rank(route.source), protocol_rank(route.target));
            if !seen.insert(key) {
                return Err(RegistryValidationError::DuplicateRuntimeRoute {
                    source: route.source,
                    target: route.target,
                });
            }
            validate_runtime_id(
                route.source,
                route.target,
                route.request_converter_id.as_deref(),
                &self.converters,
                RuntimeIdKind::Converter,
            )?;
            validate_runtime_id(
                route.source,
                route.target,
                route.response_converter_id.as_deref(),
                &self.converters,
                RuntimeIdKind::Converter,
            )?;
            validate_runtime_id(
                route.source,
                route.target,
                route.stream_converter_id.as_deref(),
                &self.converters,
                RuntimeIdKind::Converter,
            )?;
            validate_runtime_id(
                route.source,
                route.target,
                route.stream_finalizer_id.as_deref(),
                &self.finalizers,
                RuntimeIdKind::Finalizer,
            )?;
            if route.stream_finalizer_id.is_some() && route.stream_converter_id.is_none() {
                return Err(RegistryValidationError::RuntimeDirectionConflict {
                    source: route.source,
                    target: route.target,
                    detail: "stream finalizer without stream converter".to_owned(),
                });
            }
            for adaptor in &route.runtime_adaptors {
                if !self.adaptors.contains(adaptor) {
                    return Err(RegistryValidationError::UnknownRuntimeAdaptor {
                        source: route.source,
                        target: route.target,
                        runtime_adaptor: adaptor.clone(),
                    });
                }
            }
        }
        Ok(())
    }
}

/// A registry snapshot that has passed both structural and runtime checks.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct ValidatedRegistry {
    registry: Registry,
    matrix: SupportMatrix,
    runtime_catalog_version: String,
}

impl ValidatedRegistry {
    /// Returns the validated route registry used by plan compilation.
    pub fn registry(&self) -> &Registry {
        &self.registry
    }

    /// Returns the deterministic machine-readable matrix for this snapshot.
    pub fn support_matrix(&self) -> &SupportMatrix {
        &self.matrix
    }

    /// Returns the runtime catalog version that validated this snapshot.
    pub fn runtime_catalog_version(&self) -> &str {
        &self.runtime_catalog_version
    }

    /// Finds a validated route by source and target protocol.
    pub fn route(&self, source: Protocol, target: Protocol) -> Option<&RouteRegistration> {
        self.registry.route(source, target)
    }
}

/// One direction of route support as exposed by the generated matrix.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct DirectionCapability {
    /// Whether this direction is connected.
    pub supported: bool,
    /// Converter identifier, if connected.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub converter_id: Option<String>,
    /// Stream finalizer identifier, if this direction is a stream.
    #[serde(skip_serializing_if = "Option::is_none")]
    pub finalizer_id: Option<String>,
}

/// A deterministic, machine-readable snapshot of all route registrations.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct SupportMatrix {
    /// Registry version used to produce this snapshot.
    pub version: String,
    /// One entry for each source/target protocol pair.
    pub routes: Vec<RouteRegistration>,
}

impl SupportMatrix {
    /// Finds a matrix entry by source and target protocol.
    pub fn route(&self, source: Protocol, target: Protocol) -> Option<&RouteRegistration> {
        self.routes
            .iter()
            .find(|route| route.source == source && route.target == target)
    }

    /// Serializes the matrix for an admin endpoint or diagnostic snapshot.
    pub fn to_json(&self) -> Result<String, serde_json::Error> {
        serde_json::to_string(self)
    }
}

/// The route registry used by plan compilation and support-matrix generation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
pub struct Registry {
    /// Registered routes. Entries are validated as a complete 4×4 matrix.
    pub routes: Vec<RouteRegistration>,
    /// Registry schema version.
    pub version: String,
}

impl Default for Registry {
    fn default() -> Self {
        Self::current()
    }
}

impl Registry {
    /// Returns the built-in registry for the four supported protocols.
    pub fn current() -> Self {
        let protocols = protocols();
        let mut routes = Vec::with_capacity(protocols.len() * protocols.len());
        for source in protocols {
            for target in protocols {
                let route = if source == target {
                    RouteRegistration::raw(source)
                } else {
                    RouteRegistration::cortexfs_cross(source, target)
                };
                routes.push(route);
            }
        }
        Self {
            routes,
            version: REGISTRY_VERSION.to_owned(),
        }
    }

    /// Builds a registry from explicit routes, useful for isolated validation
    /// tests and future versioned registries.
    pub fn from_routes(routes: impl IntoIterator<Item = RouteRegistration>) -> Self {
        Self {
            routes: routes.into_iter().collect(),
            version: REGISTRY_VERSION.to_owned(),
        }
    }

    /// Appends a registration. Call Self::validate before exposing it.
    pub fn register(&mut self, route: RouteRegistration) {
        self.routes.push(route);
    }

    /// Returns all routes.
    pub fn routes(&self) -> &[RouteRegistration] {
        &self.routes
    }

    /// Returns mutable routes for controlled administrative/test updates.
    pub fn routes_mut(&mut self) -> &mut [RouteRegistration] {
        &mut self.routes
    }

    /// Finds one route registration.
    pub fn route(&self, source: Protocol, target: Protocol) -> Option<&RouteRegistration> {
        self.routes
            .iter()
            .find(|route| route.source == source && route.target == target)
    }

    /// Finds one mutable route registration.
    pub fn route_mut(
        &mut self,
        source: Protocol,
        target: Protocol,
    ) -> Option<&mut RouteRegistration> {
        self.routes
            .iter_mut()
            .find(|route| route.source == source && route.target == target)
    }

    /// Validates route completeness, converter/runtime wiring, and quality
    /// claims. The first violation is returned with machine-readable details.
    pub fn validate(&self) -> Result<(), RegistryValidationError> {
        if self.version.trim().is_empty() {
            return Err(RegistryValidationError::EmptyVersion);
        }

        let mut seen = BTreeSet::new();
        for route in &self.routes {
            let key = (protocol_rank(route.source), protocol_rank(route.target));
            if !seen.insert(key) {
                return Err(RegistryValidationError::DuplicateRoute {
                    source: route.source,
                    target: route.target,
                });
            }
            self.validate_route(route)?;
        }
        for source in protocols() {
            for target in protocols() {
                if !seen.contains(&(protocol_rank(source), protocol_rank(target))) {
                    return Err(RegistryValidationError::MissingRoute { source, target });
                }
            }
        }
        Ok(())
    }

    /// Validates this registry against independently supplied live runtime
    /// metadata and returns the only type allowed to generate a matrix.
    pub fn validate_against_catalog(
        &self,
        catalog: &RuntimeCatalog,
    ) -> Result<ValidatedRegistry, RegistryValidationError> {
        self.validate()?;
        catalog.validate()?;
        for route in &self.routes {
            let runtime = catalog.route(route.source, route.target);
            if !route.supports_any_direction() {
                if runtime.is_some_and(RuntimeRoute::supports_any_direction) {
                    return Err(RegistryValidationError::CatalogClaimsUnsupportedRoute {
                        source: route.source,
                        target: route.target,
                    });
                }
                continue;
            }
            let Some(runtime) = runtime else {
                return Err(RegistryValidationError::MissingRuntimeRoute {
                    source: route.source,
                    target: route.target,
                });
            };
            validate_direction(
                route,
                Direction::Request,
                route.request_supported,
                route.request_converter_id.as_deref(),
                runtime.request_converter_id.as_deref(),
                &catalog.converters,
            )?;
            validate_direction(
                route,
                Direction::Response,
                route.response_supported,
                route.response_converter_id.as_deref(),
                runtime.response_converter_id.as_deref(),
                &catalog.converters,
            )?;
            validate_direction(
                route,
                Direction::Stream,
                route.stream_supported,
                route.stream_converter_id.as_deref(),
                runtime.stream_converter_id.as_deref(),
                &catalog.converters,
            )?;
            if route.stream_supported {
                let expected = route.stream_finalizer_id.as_deref();
                let actual = runtime.stream_finalizer_id.as_deref();
                if expected != actual {
                    return Err(RegistryValidationError::RuntimeDirectionMismatch {
                        source: route.source,
                        target: route.target,
                        direction: Direction::Stream,
                        expected: expected.map(str::to_owned),
                        actual: actual.map(str::to_owned),
                    });
                }
                if let Some(finalizer) = expected {
                    if !catalog.finalizers.contains(finalizer) {
                        return Err(RegistryValidationError::UnknownFinalizer {
                            source: route.source,
                            target: route.target,
                            finalizer_id: finalizer.to_owned(),
                        });
                    }
                }
            }
            for adaptor in &route.runtime_adaptors {
                if !catalog.adaptors.contains(adaptor)
                    || !runtime.runtime_adaptors.contains(adaptor)
                {
                    return Err(RegistryValidationError::RuntimeAdaptorMismatch {
                        source: route.source,
                        target: route.target,
                        runtime_adaptor: adaptor.clone(),
                    });
                }
            }
        }
        let matrix = SupportMatrix {
            version: self.version.clone(),
            routes: sorted_routes(&self.routes),
        };
        Ok(ValidatedRegistry {
            registry: self.clone(),
            matrix,
            runtime_catalog_version: catalog.version.clone(),
        })
    }

    fn validate_route(&self, route: &RouteRegistration) -> Result<(), RegistryValidationError> {
        if route.version.trim().is_empty() {
            return Err(RegistryValidationError::EmptyRouteVersion {
                source: route.source,
                target: route.target,
            });
        }

        if route.request_supported && route.request_converter_id.is_none() {
            return Err(RegistryValidationError::MissingRequestConverter {
                source: route.source,
                target: route.target,
            });
        }
        if route.response_supported && route.response_converter_id.is_none() {
            return Err(RegistryValidationError::MissingResponseConverter {
                source: route.source,
                target: route.target,
            });
        }
        if route.stream_supported && route.stream_converter_id.is_none() {
            return Err(RegistryValidationError::MissingStreamConverter {
                source: route.source,
                target: route.target,
            });
        }
        if route.stream_supported && route.stream_finalizer_id.is_none() {
            return Err(RegistryValidationError::MissingStreamFinalizer {
                source: route.source,
                target: route.target,
            });
        }
        if !route.request_supported && route.request_converter_id.is_some() {
            return Err(RegistryValidationError::DirectionClaimWithoutSupport {
                source: route.source,
                target: route.target,
                direction: Direction::Request,
            });
        }
        if !route.response_supported && route.response_converter_id.is_some() {
            return Err(RegistryValidationError::DirectionClaimWithoutSupport {
                source: route.source,
                target: route.target,
                direction: Direction::Response,
            });
        }
        if !route.stream_supported
            && (route.stream_converter_id.is_some() || route.stream_finalizer_id.is_some())
        {
            return Err(RegistryValidationError::DirectionClaimWithoutSupport {
                source: route.source,
                target: route.target,
                direction: Direction::Stream,
            });
        }
        if route.supports_any_direction() && route.runtime_adaptors.is_empty() {
            return Err(RegistryValidationError::MissingRuntimeAdaptor {
                source: route.source,
                target: route.target,
            });
        }
        if !route.supports_any_direction() && route.quality != Fidelity::Unsupported {
            return Err(RegistryValidationError::QualityCapabilityConflict {
                source: route.source,
                target: route.target,
                quality: route.quality,
                detail: "unsupported directions must advertise unsupported quality".to_owned(),
            });
        }
        if route.supports_any_direction() && route.quality == Fidelity::Unsupported {
            return Err(RegistryValidationError::QualityCapabilityConflict {
                source: route.source,
                target: route.target,
                quality: route.quality,
                detail: "a connected direction cannot advertise unsupported quality".to_owned(),
            });
        }
        if route.quality == Fidelity::Exact && !route.raw_passthrough {
            return Err(RegistryValidationError::QualityCapabilityConflict {
                source: route.source,
                target: route.target,
                quality: route.quality,
                detail: "exact quality is reserved for raw passthrough routes".to_owned(),
            });
        }
        if route.raw_passthrough
            && (route.source != route.target
                || route.quality != Fidelity::Exact
                || !route.request_supported
                || !route.response_supported
                || !route.stream_supported)
        {
            return Err(RegistryValidationError::RawPassthroughConflict {
                source: route.source,
                target: route.target,
            });
        }
        if !route.supports_any_direction() && !route.feature_requirements.is_empty() {
            return Err(RegistryValidationError::FeatureRequirementWithoutSupport {
                source: route.source,
                target: route.target,
            });
        }
        if route
            .feature_requirements
            .iter()
            .any(|feature| route.unsupported_features.contains(feature))
        {
            return Err(RegistryValidationError::FeatureCapabilityConflict {
                source: route.source,
                target: route.target,
            });
        }

        Ok(())
    }
}

/// Generates a matrix from a runtime-validated registry.
pub fn generate_support_matrix(validated: &ValidatedRegistry) -> SupportMatrix {
    validated.support_matrix().clone()
}

/// A direction independently validated against runtime metadata.
#[derive(Clone, Copy, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Direction {
    /// Request conversion.
    Request,
    /// Complete response conversion.
    Response,
    /// Streaming response conversion.
    Stream,
}

#[derive(Clone, Copy)]
enum RuntimeIdKind {
    Converter,
    Finalizer,
}

fn validate_runtime_id(
    source: Protocol,
    target: Protocol,
    id: Option<&str>,
    registered: &BTreeSet<String>,
    kind: RuntimeIdKind,
) -> Result<(), RegistryValidationError> {
    let Some(id) = id else {
        return Ok(());
    };
    if id.trim().is_empty() || !registered.contains(id) {
        return Err(match kind {
            RuntimeIdKind::Converter => RegistryValidationError::UnknownConverter {
                source,
                target,
                converter_id: id.to_owned(),
            },
            RuntimeIdKind::Finalizer => RegistryValidationError::UnknownFinalizer {
                source,
                target,
                finalizer_id: id.to_owned(),
            },
        });
    }
    Ok(())
}

fn validate_direction(
    route: &RouteRegistration,
    direction: Direction,
    supported: bool,
    expected: Option<&str>,
    actual: Option<&str>,
    converters: &BTreeSet<String>,
) -> Result<(), RegistryValidationError> {
    if !supported {
        if actual.is_some() {
            return Err(
                RegistryValidationError::RuntimeDirectionClaimWithoutSupport {
                    source: route.source,
                    target: route.target,
                    direction,
                    runtime_converter_id: actual.map(str::to_owned),
                },
            );
        }
        return Ok(());
    }
    if let Some(converter) = expected {
        if !converters.contains(converter) {
            return Err(RegistryValidationError::UnknownConverter {
                source: route.source,
                target: route.target,
                converter_id: converter.to_owned(),
            });
        }
    }
    if expected != actual {
        return Err(RegistryValidationError::RuntimeDirectionMismatch {
            source: route.source,
            target: route.target,
            direction,
            expected: expected.map(str::to_owned),
            actual: actual.map(str::to_owned),
        });
    }
    Ok(())
}

fn sorted_routes(routes: &[RouteRegistration]) -> Vec<RouteRegistration> {
    let mut sorted = routes.to_owned();
    sorted.sort_by_key(|route| (protocol_rank(route.source), protocol_rank(route.target)));
    sorted
}

/// Validation failures for registry/runtime drift.
#[derive(Clone, Debug, Eq, PartialEq, Serialize, Deserialize)]
#[serde(tag = "code", rename_all = "snake_case")]
pub enum RegistryValidationError {
    /// Registry schema version is empty.
    EmptyVersion,
    /// Runtime catalog schema version is empty.
    EmptyRuntimeCatalogVersion,
    /// Route version is empty.
    EmptyRouteVersion { source: Protocol, target: Protocol },
    /// Two entries claim the same source/target pair.
    DuplicateRoute { source: Protocol, target: Protocol },
    /// A complete matrix entry is missing.
    MissingRoute { source: Protocol, target: Protocol },
    /// Runtime metadata contains a duplicate route pair.
    DuplicateRuntimeRoute { source: Protocol, target: Protocol },
    /// A supported registry route has no runtime metadata.
    MissingRuntimeRoute { source: Protocol, target: Protocol },
    /// Runtime metadata claims support for a registry-unsupported route.
    CatalogClaimsUnsupportedRoute { source: Protocol, target: Protocol },
    /// Request support has no converter.
    MissingRequestConverter { source: Protocol, target: Protocol },
    /// Response support has no converter.
    MissingResponseConverter { source: Protocol, target: Protocol },
    /// Stream support has no converter.
    MissingStreamConverter { source: Protocol, target: Protocol },
    /// Stream support has no finalizer.
    MissingStreamFinalizer { source: Protocol, target: Protocol },
    /// A supported direction has no runtime adaptor.
    MissingRuntimeAdaptor { source: Protocol, target: Protocol },
    /// The quality does not match directional capability claims.
    QualityCapabilityConflict {
        source: Protocol,
        target: Protocol,
        quality: Fidelity,
        detail: String,
    },
    /// Raw passthrough was claimed for a non-native or incomplete route.
    RawPassthroughConflict { source: Protocol, target: Protocol },
    /// Feature requirements were declared on an entirely unsupported route.
    FeatureRequirementWithoutSupport { source: Protocol, target: Protocol },
    /// A feature appears in both supported and unsupported declarations.
    FeatureCapabilityConflict { source: Protocol, target: Protocol },
    /// A registry direction has an ID despite being marked unsupported.
    DirectionClaimWithoutSupport {
        source: Protocol,
        target: Protocol,
        direction: Direction,
    },
    /// Runtime metadata has a finalizer without a stream converter.
    RuntimeDirectionConflict {
        source: Protocol,
        target: Protocol,
        detail: String,
    },
    /// Runtime metadata does not match a supported registry direction.
    RuntimeDirectionMismatch {
        source: Protocol,
        target: Protocol,
        direction: Direction,
        expected: Option<String>,
        actual: Option<String>,
    },
    /// Runtime metadata wires a direction that the registry did not claim.
    RuntimeDirectionClaimWithoutSupport {
        source: Protocol,
        target: Protocol,
        direction: Direction,
        runtime_converter_id: Option<String>,
    },
    /// A registry adaptor is absent from route metadata or the catalog set.
    RuntimeAdaptorMismatch {
        source: Protocol,
        target: Protocol,
        runtime_adaptor: String,
    },
    /// A converter id is not present in the runtime catalog.
    UnknownConverter {
        source: Protocol,
        target: Protocol,
        converter_id: String,
    },
    /// A stream finalizer id is not present in the runtime catalog.
    UnknownFinalizer {
        source: Protocol,
        target: Protocol,
        finalizer_id: String,
    },
    /// A runtime adaptor id is not present in the runtime catalog.
    UnknownRuntimeAdaptor {
        source: Protocol,
        target: Protocol,
        runtime_adaptor: String,
    },
}

impl fmt::Display for RegistryValidationError {
    fn fmt(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(formatter, "{self:?}")
    }
}

impl Error for RegistryValidationError {}

/// Returns the built-in protocol order used in matrix output.
pub const fn protocols() -> [Protocol; 4] {
    [
        Protocol::OpenAi,
        Protocol::OpenAiResponses,
        Protocol::Claude,
        Protocol::Gemini,
    ]
}

fn all_features() -> BTreeSet<Feature> {
    Feature::all().iter().copied().collect()
}

fn raw_converter_id(protocol: Protocol) -> &'static str {
    match protocol {
        Protocol::OpenAi => RAW_OPENAI_CHAT,
        Protocol::OpenAiResponses => RAW_OPENAI_RESPONSES,
        Protocol::Claude => RAW_CLAUDE,
        Protocol::Gemini => RAW_GEMINI,
    }
}

/// Returns the Go-aligned fidelity claim for one cross-protocol pair.
const fn cross_protocol_fidelity(source: Protocol, target: Protocol) -> Fidelity {
    match (source, target) {
        (Protocol::OpenAi, Protocol::OpenAiResponses)
        | (Protocol::OpenAiResponses, Protocol::OpenAi) => Fidelity::Normalized,
        (Protocol::Claude, Protocol::Gemini) | (Protocol::Gemini, Protocol::Claude) => {
            Fidelity::Lossy
        }
        _ => Fidelity::Normalized,
    }
}

fn cortexfs_converter_id(source: Protocol, target: Protocol, direction: Direction) -> String {
    format!(
        "{CORTEXFS_CONVERTER_PREFIX}-{}-to-{}-{}-v1",
        cortexfs_protocol_slug(source),
        cortexfs_protocol_slug(target),
        cortexfs_direction_slug(direction)
    )
}

fn cortexfs_stream_finalizer_id(source: Protocol, target: Protocol) -> String {
    format!(
        "{CORTEXFS_CONVERTER_PREFIX}-{}-to-{}-stream-finalizer-v1",
        cortexfs_protocol_slug(source),
        cortexfs_protocol_slug(target)
    )
}

fn cortexfs_runtime_adaptor_id(source: Protocol, target: Protocol) -> String {
    format!(
        "{CORTEXFS_RUNTIME_PREFIX}-{}-to-{}-v1",
        cortexfs_protocol_slug(source),
        cortexfs_protocol_slug(target)
    )
}

const fn cortexfs_protocol_slug(protocol: Protocol) -> &'static str {
    match protocol {
        Protocol::OpenAi => "openai-chat",
        Protocol::OpenAiResponses => "openai-responses",
        Protocol::Claude => "claude-messages",
        Protocol::Gemini => "gemini-generate-content",
    }
}

const fn cortexfs_direction_slug(direction: Direction) -> &'static str {
    match direction {
        Direction::Request => "request",
        Direction::Response => "response",
        Direction::Stream => "stream",
    }
}

fn protocol_rank(protocol: Protocol) -> u8 {
    match protocol {
        Protocol::OpenAi => 0,
        Protocol::OpenAiResponses => 1,
        Protocol::Claude => 2,
        Protocol::Gemini => 3,
    }
}

// BTreeSet has no tiny one-element constructor. Keeping this helper private
// makes route declarations concise without exposing an extra collection API.
struct BtreeSetExt;

impl BtreeSetExt {
    fn one<T: Ord>(value: T) -> BTreeSet<T> {
        [value].into_iter().collect()
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn built_in_registry_has_a_complete_four_by_four_matrix() {
        let registry = Registry::default();
        assert!(registry.validate().is_ok());
        let validated = registry
            .validate_against_catalog(&runtime_catalog())
            .expect("runtime catalog validates");
        assert_eq!(validated.support_matrix().routes.len(), 16);
    }

    #[test]
    fn native_routes_are_exact_raw_passthrough() {
        let registry = Registry::default();
        let route = registry
            .route(Protocol::Claude, Protocol::Claude)
            .expect("native route");
        assert_eq!(route.quality, Fidelity::Exact);
        assert!(route.raw_passthrough);
        assert!(route.request_supported && route.response_supported && route.stream_supported);
    }

    #[test]
    fn cross_protocol_routes_are_wired_through_cortexfs() {
        let registry = Registry::default();
        let bridge = registry
            .route(Protocol::OpenAi, Protocol::OpenAiResponses)
            .expect("bridge");
        assert_eq!(bridge.quality, Fidelity::Normalized);
        assert!(bridge.supports_any_direction());
        assert!(bridge
            .request_converter_id
            .as_deref()
            .is_some_and(|id| id.starts_with("cortexfs-")));

        let discouraged = registry
            .route(Protocol::Claude, Protocol::Gemini)
            .expect("discouraged bridge");
        assert_eq!(discouraged.quality, Fidelity::Lossy);
        assert!(discouraged.supports_any_direction());
    }

    #[test]
    fn missing_stream_finalizer_is_detected_as_runtime_drift() {
        let mut registry = Registry::default();
        registry
            .route_mut(Protocol::OpenAi, Protocol::OpenAi)
            .expect("native route")
            .stream_finalizer_id = None;
        assert!(matches!(
            registry.validate(),
            Err(RegistryValidationError::MissingStreamFinalizer { .. })
        ));
    }

    #[test]
    fn quality_conflict_is_detected_before_plan_compilation() {
        let mut registry = Registry::default();
        registry
            .route_mut(Protocol::Claude, Protocol::Gemini)
            .expect("route")
            .quality = Fidelity::Exact;
        assert!(matches!(
            registry.validate(),
            Err(RegistryValidationError::QualityCapabilityConflict { .. })
        ));
    }

    #[test]
    fn missing_converter_is_detected_against_the_independent_catalog() {
        let mut catalog = runtime_catalog();
        catalog.converters.clear();
        assert!(matches!(
            Registry::default().validate_against_catalog(&catalog),
            Err(RegistryValidationError::UnknownConverter { .. })
        ));
    }

    #[test]
    fn missing_finalizer_is_detected_against_the_independent_catalog() {
        let mut catalog = runtime_catalog();
        catalog.finalizers.clear();
        assert!(matches!(
            Registry::default().validate_against_catalog(&catalog),
            Err(RegistryValidationError::UnknownFinalizer { .. })
        ));
    }

    #[test]
    fn missing_runtime_adaptor_is_detected_against_the_independent_catalog() {
        let mut catalog = runtime_catalog();
        catalog.adaptors.clear();
        assert!(matches!(
            Registry::default().validate_against_catalog(&catalog),
            Err(RegistryValidationError::UnknownRuntimeAdaptor { .. })
        ));
    }

    #[test]
    fn missing_runtime_route_is_detected_before_matrix_generation() {
        let mut catalog = runtime_catalog();
        catalog
            .routes
            .retain(|route| route.source != Protocol::Gemini);
        assert!(matches!(
            Registry::default().validate_against_catalog(&catalog),
            Err(RegistryValidationError::MissingRuntimeRoute {
                source: Protocol::Gemini,
                target: Protocol::Gemini,
            })
        ));
    }

    #[test]
    fn catalog_cannot_claim_exact_quality_on_cross_protocol_routes() {
        let mut catalog = runtime_catalog();
        let bridge = catalog
            .routes
            .iter_mut()
            .find(|route| route.source == Protocol::OpenAi && route.target == Protocol::OpenAiResponses)
            .expect("cross route runtime metadata");
        bridge.request_converter_id = Some("raw-openai-chat-v1".to_owned());
        assert!(matches!(
            Registry::default().validate_against_catalog(&catalog),
            Err(RegistryValidationError::RuntimeDirectionMismatch { .. })
                | Err(RegistryValidationError::UnknownConverter { .. })
        ));
    }

    #[test]
    fn one_way_registry_claim_cannot_become_a_runtime_two_way_claim() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::OpenAi, Protocol::OpenAi)
            .expect("native route");
        route.response_supported = false;
        route.response_converter_id = None;
        route.raw_passthrough = false;
        route.quality = Fidelity::Normalized;
        assert!(matches!(
            registry.validate_against_catalog(&runtime_catalog()),
            Err(
                RegistryValidationError::RuntimeDirectionClaimWithoutSupport {
                    direction: Direction::Response,
                    ..
                }
            )
        ));
    }

    #[test]
    fn feature_requirement_conflict_is_structurally_rejected() {
        let mut registry = Registry::default();
        let route = registry
            .route_mut(Protocol::OpenAi, Protocol::OpenAi)
            .expect("native route");
        route.unsupported_features.insert(Feature::Text);
        assert!(matches!(
            registry.validate(),
            Err(RegistryValidationError::FeatureCapabilityConflict { .. })
        ));
    }

    #[test]
    fn generated_matrix_serializes_machine_readably() {
        let matrix = generate_support_matrix(
            &Registry::default()
                .validate_against_catalog(&runtime_catalog())
                .expect("runtime catalog validates"),
        );
        let json = matrix.to_json().expect("matrix json");
        assert!(json.contains("open_ai_responses"));
        assert!(json.contains("normalized"));
    }

    fn runtime_catalog() -> RuntimeCatalog {
        let mut converters = BTreeSet::new();
        let mut finalizers = BTreeSet::new();
        let mut adaptors = BTreeSet::new();
        let mut routes = Vec::new();
        for source in protocols() {
            for target in protocols() {
                let registration = if source == target {
                    RouteRegistration::raw(source)
                } else {
                    RouteRegistration::cortexfs_cross(source, target)
                };
                for converter in registration.converter_ids() {
                    converters.insert(converter.to_owned());
                }
                if let Some(finalizer) = registration.stream_finalizer_id.clone() {
                    finalizers.insert(finalizer);
                }
                for adaptor in &registration.runtime_adaptors {
                    adaptors.insert(adaptor.clone());
                }
                let mut runtime = RuntimeRoute::new(source, target);
                runtime.request_converter_id = registration.request_converter_id.clone();
                runtime.response_converter_id = registration.response_converter_id.clone();
                runtime.stream_converter_id = registration.stream_converter_id.clone();
                runtime.stream_finalizer_id = registration.stream_finalizer_id.clone();
                runtime
                    .runtime_adaptors
                    .extend(registration.runtime_adaptors.iter().cloned());
                routes.push(runtime);
            }
        }
        RuntimeCatalog::new("test-runtime-v2-cortexfs", converters, finalizers, adaptors, routes)
    }
}
