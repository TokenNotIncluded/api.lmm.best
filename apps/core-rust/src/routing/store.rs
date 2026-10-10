//! Durable Rust-owned publication. No schema changes run during normal startup.
//! The offline initializer later includes schema/routing.sql explicitly.

use std::fmt;
use std::sync::Arc;

use serde::{Deserialize, Serialize};
use sqlx::PgPool;

use super::config::{MAX_DOCUMENT_BYTES, valid_id};
use super::{
    CONTRACT_VERSION, CredentialRef, PriceConfig, PriceSnapshot, ReleaseVersion, Router,
    RoutingConfig, RoutingError,
};

/// INTERNAL publication/recovery document. Only catalog() is safe for public clients.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
pub struct ReleaseDocument {
    pub contract_version: u32,
    pub routing: RoutingConfig,
    pub pricing: PriceConfig,
}

impl ReleaseDocument {
    pub fn decode(bytes: &[u8]) -> Result<Self, StoreError> {
        if bytes.len() > MAX_DOCUMENT_BYTES {
            return Err(StoreError::InvalidDocument);
        }
        let document: Self = serde_json::from_slice(bytes).map_err(|_| StoreError::InvalidDocument)?;
        document.check_contract()?;
        Ok(document)
    }

    pub fn encode(&self) -> Result<Vec<u8>, StoreError> {
        self.check_contract()?;
        let bytes = serde_json::to_vec(self).map_err(|_| StoreError::InvalidDocument)?;
        if bytes.len() > MAX_DOCUMENT_BYTES {
            return Err(StoreError::InvalidDocument);
        }
        Ok(bytes)
    }

    fn check_contract(&self) -> Result<(), StoreError> {
        if self.contract_version != CONTRACT_VERSION {
            return Err(StoreError::InvalidDocument);
        }
        Ok(())
    }

    pub fn into_router(self) -> Result<Router, StoreError> {
        self.check_contract()?;
        let prices = Arc::new(PriceSnapshot::compile(self.pricing)?);
        Ok(Router::new(self.routing, prices)?)
    }

    /// Install after durable publication. In-flight requests still own their old snapshot.
    /// A conflict means another publisher won: reload the durable active version.
    pub fn apply(self, router: &Router) -> Result<ReleaseVersion, StoreError> {
        self.check_contract()?;
        let expected = router.version()?;
        let prices = Arc::new(PriceSnapshot::compile(self.pricing)?);
        Ok(router.publish(expected, self.routing, prices)?)
    }
}

#[derive(Clone, Debug, PartialEq, Eq)]
pub enum StoreError {
    NotConfigured,
    InvalidDocument,
    VersionConflict,
    VersionAlreadyExists,
    Database,
    Routing(RoutingError),
}

impl From<RoutingError> for StoreError {
    fn from(error: RoutingError) -> Self {
        Self::Routing(error)
    }
}

impl From<sqlx::Error> for StoreError {
    fn from(error: sqlx::Error) -> Self {
        // PostgreSQL diagnostics can contain a rejected value. Never retain that text.
        match error.as_database_error().and_then(|error| error.code()).as_deref() {
            Some("40001") => Self::VersionConflict,
            Some("23505") => Self::VersionAlreadyExists,
            _ => Self::Database,
        }
    }
}

impl fmt::Display for StoreError {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "routing store: {self:?}")
    }
}

impl std::error::Error for StoreError {}

#[derive(Clone)]
pub struct PostgresRoutingStore {
    pool: PgPool,
}

impl PostgresRoutingStore {
    pub fn new(pool: PgPool) -> Self {
        Self { pool }
    }

    /// Called ONLY by the Rust secret manager after storing the immutable secret revision.
    /// This registers a reference, not a credential, and does not verify secret availability.
    pub async fn register_credential_reference(&self, reference: CredentialRef) -> Result<(), StoreError> {
        if !valid_id(reference.id) || !valid_id(reference.revision) {
            return Err(StoreError::InvalidDocument);
        }
        sqlx::query("INSERT INTO routing_credential_refs(id, revision) VALUES ($1, $2) ON CONFLICT DO NOTHING")
            .bind(reference.id as i64)
            .bind(reference.revision as i64)
            .execute(&self.pool)
            .await?;
        Ok(())
    }

    /// Validate and stage immutable prices without moving the active route pointer.
    pub async fn stage_prices(&self, config: PriceConfig) -> Result<Arc<PriceSnapshot>, StoreError> {
        let prices = Arc::new(PriceSnapshot::compile(config)?);
        let document = serde_json::to_value(prices.config()).map_err(|_| StoreError::InvalidDocument)?;
        sqlx::query("SELECT routing_stage_prices($1)")
            .bind(document)
            .execute(&self.pool)
            .await?;
        Ok(prices)
    }

    /// The database transaction seals projections and moves the pointer as one operation.
    /// Call ReleaseDocument::apply on each Rust node after commit; a restart loads from PG.
    pub async fn publish(
        &self,
        config: RoutingConfig,
        price_version: u64,
        expected: Option<ReleaseVersion>,
    ) -> Result<ReleaseDocument, StoreError> {
        if !valid_id(price_version)
            || expected.is_some_and(|v| !valid_id(v.routing) || !valid_id(v.pricing))
        {
            return Err(StoreError::InvalidDocument);
        }
        let value: serde_json::Value = sqlx::query_scalar(
            "SELECT document FROM routing_price_versions WHERE version = $1 AND sealed",
        )
        .bind(price_version as i64)
        .fetch_optional(&self.pool)
        .await?
        .ok_or(StoreError::NotConfigured)?;
        let pricing: PriceConfig = serde_json::from_value(value).map_err(|_| StoreError::InvalidDocument)?;
        let prices = Arc::new(PriceSnapshot::compile(pricing.clone())?);
        // Validate all references and all route/price combinations BEFORE any durable write.
        Router::new(config.clone(), prices)?;
        let document = ReleaseDocument { contract_version: CONTRACT_VERSION, routing: config, pricing };
        document.encode()?;
        let routing = serde_json::to_value(&document.routing).map_err(|_| StoreError::InvalidDocument)?;
        sqlx::query("SELECT routing_publish_config($1, $2, $3, $4)")
            .bind(routing)
            .bind(price_version as i64)
            .bind(expected.map(|v| v.routing as i64))
            .bind(expected.map(|v| v.pricing as i64))
            .execute(&self.pool)
            .await?;
        Ok(document)
    }

    /// One statement reads one coherent active route/price pair. No Go service is involved.
    pub async fn load_active(&self) -> Result<ReleaseDocument, StoreError> {
        let value: serde_json::Value = sqlx::query_scalar(
            "SELECT jsonb_build_object('contract_version', 1, 'routing', c.document, 'pricing', p.document) \
             FROM routing_active_release a \
             JOIN routing_config_versions c ON c.version = a.config_version AND c.sealed \
             JOIN routing_price_versions p ON p.version = c.price_version AND p.sealed \
             WHERE a.singleton",
        )
        .fetch_optional(&self.pool)
        .await?
        .ok_or(StoreError::NotConfigured)?;
        let bytes = serde_json::to_vec(&value).map_err(|_| StoreError::InvalidDocument)?;
        ReleaseDocument::decode(&bytes)
    }
}
