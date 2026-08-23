//! Explicitly opt-in candidate routes for the isolated test instance.
//!
//! This module is deliberately separate from production wiring.  It composes
//! only durable PostgreSQL/Valkey slices and replaces every optional outbound
//! boundary with a fail-closed policy, so a copied production snapshot cannot
//! trigger provider calls while acceptance tests exercise the local surface.

use std::{
    collections::{BTreeMap, BTreeSet},
    sync::Arc,
};

use async_trait::async_trait;
use axum::{
    Router,
    extract::Request,
    http::{HeaderMap, StatusCode, header},
    middleware::Next,
    response::{IntoResponse, Response},
};
use chrono::{Local, SecondsFormat, TimeZone, Utc};
use lmm_api_rs::{
    auth::DashboardAuth,
    migration_routes::{
        admin_catalog::{
            AdminCatalogAuthorizer, AdminCatalogState, CatalogError, CatalogUpstream,
            DashboardAdminCatalogAuthorizer, PgCatalogProvider, UpstreamCatalog,
        },
        billing_payments::{
            BillingConfig, BillingDependencies, BillingHttpState, DashboardBillingAuthorizer,
            DisabledCheckoutProvider, DisabledEpayVerifier, DisabledPaymentCompliance,
            DisabledStripeWebhookVerifier, PgBillingRepository, ValkeyBillingCache,
            billing_payments_router,
        },
        billing_subscriptions::BillingSubscriptionsState,
        channel_advanced::{
            ChannelAdvancedCall, ChannelAdvancedChannel, ChannelAdvancedError,
            ChannelAdvancedHttpState, ChannelAdvancedUpstream, DashboardChannelAdvancedAuthorizer,
            PgChannelAdvancedStore, StoreBackedChannelAdvancedProvider, channel_advanced_router,
        },
        channel_core::{ChannelAdminAuthorizer, ChannelCoreState},
        channel_ops::{ChannelOpsHttpState, DashboardChannelAuthorizer},
        control_admin::{
            ControlAdminState, DashboardControlAdminAuthorizer, OAuthDiscoveryClient,
            control_admin_router,
        },
        control_public::{
            ControlPublicError, ControlPublicHttpState, PgControlPublicRepository,
            UptimeHeartbeatPage, UptimeKumaClient, UptimeStatusPage, control_public_router,
        },
        deployment::{
            DeploymentState, DisabledDeploymentJobRunner, PgValkeyDeploymentProvider,
            router as deployment_router,
        },
        identity_2fa::{
            Identity2FAActor, Identity2FASession, Identity2FAState, SecuritySessionRotation,
            SecuritySessionRotator,
        },
        identity_admin::IdentityAdminState,
        identity_federation::{
            DashboardFederationIdentity, DisabledEmailCodeVerifier, FederationError,
            FederationIdentity, FederationPrincipal, FederationState,
            bindings_router as identity_federation_bindings_router,
            provider_router as identity_federation_provider_router,
        },
        identity_profile::ProfileState,
        identity_security::{IdentitySecurityState, PgValkeySecurityProvider},
        media_midjourney::{
            BufferedJsonReply, ImageReply, MidjourneyBackend, MidjourneyChannel, MidjourneyFailure,
            MidjourneyHttpState, MidjourneyIdentity, PgMidjourneyBackend, StoredImage, SubmitReply,
            TaskEffect, media_midjourney_dynamic_router,
        },
        media_tasks::{MediaTaskHttpState, MidjourneyMediaTaskService, media_task_router},
        missing_billing_dashboard::{
            BillingDashboardState, PgBillingDashboardAuthorizer, PgBillingDashboardStore,
            billing_dashboard_router,
        },
        missing_billing_webhooks::{
            DisabledPancakeWebhookVerifier, DisabledWaffoWebhookAvailability,
            DisabledWaffoWebhookProcessor, DisabledWaffoWebhookVerifier, WaffoWebhookState,
            missing_billing_webhooks_router,
        },
        missing_control_public::{
            DashboardMissingControlAuthorizer, DashboardMissingControlRateLimiter, HeaderNavAccess,
            MissingControlPublicState, MissingControlStore, MissingControlStoreError,
            missing_control_public_router, parse_header_nav_access,
        },
        missing_control_ratio_sync::{
            DashboardRatioSyncAuthorizer, PgRatioSyncRepository, RatioSyncHttpState,
            TestInstanceDisabledRatioSyncUpstream, ratio_sync_router,
        },
        missing_control_tasks::{
            ControlTaskStatusError, ControlTaskStatusProbe, MissingControlTasksState,
            PgControlTaskStore, missing_control_tasks_router,
        },
        missing_identity_catalog::{IdentityCatalogState, router as identity_catalog_router},
        missing_identity_checkin_aff::{
            IdentityCheckinAffState, PgValkeyCheckinEffects, router as identity_checkin_aff_router,
        },
        missing_identity_epay::{
            DashboardTopupAuthorizer, DisabledEpayGateway, DisabledTopupRepository, UserTopupState,
            router as identity_epay_router,
        },
        missing_identity_stripe_creem::{
            DashboardStripeCreemAuthorizer, DisabledStripeCreemGateway, IdentityStripeCreemState,
            PgStripeCreemStore, router as identity_stripe_creem_router,
        },
        missing_identity_topup::{IdentityTopupState, router as identity_topup_router},
        missing_identity_waffo::{
            DisabledTopUpGateway, WaffoTopUpState, router as identity_waffo_router,
        },
        missing_relay_misc_new::{
            MissingRelayAuthRejection, MissingRelayAuthorization, MissingRelayEndpoint,
            MissingRelayMiscState, MissingRelayService, missing_relay_misc_router,
        },
        missing_relay_models_billing::{ModelLookupState, PgStaticModelLookup},
        missing_relay_video::{
            RelayVideoAuthorization, RelayVideoHttpState, RelayVideoOperation, RelayVideoService,
            missing_relay_video_router,
        },
        observability::{
            DashboardObservabilityAuthorizer, ObservabilityState, PgObservabilityStore,
            PgReadOnlyObservabilityTokenAuthorizer, PostgresObservabilityMetrics,
            UnavailableObservabilityMaintenance, observability_router,
        },
        open_source_bounties::{OpenSourceBountyState, router as open_source_bounty_router},
        relay_anthropic_gemini::{
            RelayBackend, RelayChannel, RelayFailure, RelayHttpState, RelayIdentity, RelayOutcome,
            RelayProtocol, UpstreamReply, UpstreamRequest, router_with_model_lookup,
        },
        relay_media::{RelayMediaHttpState, RelayMediaService, relay_media_router},
        relay_misc::{
            RelayAuth, RelayMiscHttpState, RelayMiscService, RelayProtocol as RelayMiscProtocol,
            RelayRequestContext, routes as relay_misc_routes,
        },
        relay_openai::{
            OpenAiRelayAuthorization, OpenAiRelayFailure, OpenAiRelayHttpState, OpenAiRelayRequest,
            OpenAiRelayResult, OpenAiRelayService, openai_relay_router,
        },
        system_config::{
            DashboardRootAuthorizer, ProjectUpdateClient, SystemConfigHttpState,
            SystemConfigRuntimeWriter, TestInstanceDisabledWaffoPancakeGateway,
            system_config_router,
        },
    },
};
use secrecy::SecretString;
use serde_json::{Map, Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;

#[derive(Clone, Copy, Debug)]
struct TestInstanceSetupRuntimeWriter;

impl TestInstanceSetupRuntimeWriter {
    fn validate(changes: &[(String, String)]) -> Result<(), ()> {
        if changes.len() != 2 {
            return Err(());
        }

        let mut self_use_mode_enabled = false;
        let mut demo_site_enabled = false;
        for (key, value) in changes {
            if !matches!(value.as_str(), "true" | "false") {
                return Err(());
            }
            match key.as_str() {
                "SelfUseModeEnabled" if !self_use_mode_enabled => {
                    self_use_mode_enabled = true;
                }
                "DemoSiteEnabled" if !demo_site_enabled => {
                    demo_site_enabled = true;
                }
                _ => return Err(()),
            }
        }

        if self_use_mode_enabled && demo_site_enabled {
            Ok(())
        } else {
            Err(())
        }
    }
}

#[async_trait]
impl SystemConfigRuntimeWriter for TestInstanceSetupRuntimeWriter {
    async fn preflight(&self, changes: &[(String, String)]) -> Result<(), ()> {
        Self::validate(changes)
    }

    async fn apply_committed(&self, changes: &[(String, String)]) -> Result<(), ()> {
        Self::validate(changes)
    }
}

/// Builds the intentionally small, concrete candidate set approved for the
/// test instance.  It has no provider-capable HTTP client.
pub fn safe_control_public_surface(pg: PgPool) -> Router {
    control_public_router(ControlPublicHttpState::new(
        Arc::new(PgControlPublicRepository::new(pg)),
        Arc::new(DenyUptimeKuma),
    ))
}

/// Builds the PostgreSQL-backed dashboard/public control reads for a normal
/// listener.  This slice has no outbound provider boundary: pricing,
/// rankings, group discovery, ratio exposure, and token-usage reads all use
/// the durable store plus the listener's shared dashboard session authority.
/// Keep the constructor here shared with the isolated candidate surface so
/// the two listeners cannot silently drift in their database/query contract.
pub fn durable_missing_control_public_surface(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Router {
    missing_control_public_router(
        MissingControlPublicState::new(
            Arc::new(PgMissingControlStore::new(pg)),
            Arc::new(DashboardMissingControlAuthorizer::new(Arc::clone(&auth))),
        )
        .with_critical_rate_limiter(Arc::new(DashboardMissingControlRateLimiter::new(
            Arc::clone(&auth),
        )))
        .with_console_access_gate(auth),
    )
}

/// Builds the loopback-only system-config slice used by local acceptance.
/// Every outbound dependency is denied and setup writes are restricted to the
/// two local acceptance flags validated by `TestInstanceSetupRuntimeWriter`.
pub fn safe_system_config_surface(
    pg: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
) -> Router {
    system_config_router(
        SystemConfigHttpState::new(
            pg,
            valkey,
            Arc::new(DashboardRootAuthorizer::new(auth)),
            Arc::new(DenyProjectUpdate),
            Arc::new(TestInstanceDisabledWaffoPancakeGateway),
        )
        .with_runtime_writer(Arc::new(TestInstanceSetupRuntimeWriter)),
    )
}

/// Builds the intentionally small, concrete candidate set approved for the
/// test instance.  It has no provider-capable HTTP client.
pub fn safe_candidate_surface(
    pg: PgPool,
    valkey: redis::Client,
    auth: Arc<dyn DashboardAuth>,
) -> Router {
    let catalog_authorizer: Arc<dyn AdminCatalogAuthorizer> =
        Arc::new(DashboardAdminCatalogAuthorizer::new(Arc::clone(&auth)));
    let channel_authorizer: Arc<dyn ChannelAdminAuthorizer> =
        Arc::new(DashboardChannelAuthorizer::new(Arc::clone(&auth)));
    let observability_authorizer = Arc::new(DashboardObservabilityAuthorizer::new(
        Arc::clone(&auth),
        Arc::new(PgReadOnlyObservabilityTokenAuthorizer::new(pg.clone())),
    ));

    Router::new()
        .merge(billing_payments_router(BillingHttpState::new(
            BillingDependencies {
                repository: Arc::new(PgBillingRepository::new(pg.clone())),
                authorizer: Arc::new(DashboardBillingAuthorizer::new(Arc::clone(&auth))),
                checkout: Arc::new(DisabledCheckoutProvider),
                epay: Arc::new(DisabledEpayVerifier),
                stripe: Arc::new(DisabledStripeWebhookVerifier),
                cache: Arc::new(ValkeyBillingCache::new(valkey.clone())),
                compliance: Arc::new(DisabledPaymentCompliance),
            },
            BillingConfig::default(),
        )))
        .merge(channel_advanced_router(ChannelAdvancedHttpState::new(
            Arc::new(DashboardChannelAdvancedAuthorizer::new(Arc::clone(&auth))),
            Arc::new(StoreBackedChannelAdvancedProvider::new(
                Arc::new(PgChannelAdvancedStore::new(pg.clone())),
                Arc::new(DenyChannelAdvancedUpstream),
            )),
        )))
        .merge(control_admin_router(
            ControlAdminState::new(
                pg.clone(),
                Arc::new(DashboardControlAdminAuthorizer::new(Arc::clone(&auth))),
                Arc::new(DenyOAuthDiscovery),
            )
            .with_valkey(valkey.clone()),
        ))
        .merge(deployment_router(DeploymentState::new(Arc::new(
            PgValkeyDeploymentProvider::new(
                pg.clone(),
                valkey.clone(),
                Arc::new(DisabledDeploymentJobRunner),
            ),
        ))))
        .merge(lmm_api_rs::migration_routes::identity_security::router(
            IdentitySecurityState::new(
                Arc::new(PgValkeySecurityProvider::new(pg.clone(), valkey.clone())),
                Arc::new(lmm_api_rs::migration_routes::identity_security::DashboardSecurityAuthorizer::new(Arc::clone(&auth))),
            )
            .with_passkey_enabled(false),
        ))
        .merge(lmm_api_rs::migration_routes::identity_2fa::router(
            Identity2FAState::new(pg.clone(), valkey.clone(), Arc::new(DenySessionRotator)),
        ))
        .merge(identity_federation_provider_router(FederationState::new(
            pg.clone(),
            Arc::new(DenyFederationIdentity),
            b"test-federation-flow-key",
        )))
        .merge(identity_federation_bindings_router(FederationState::new(
            pg.clone(),
            Arc::new(
                DashboardFederationIdentity::new(
                    Arc::clone(&auth),
                    pg.clone(),
                    &SecretString::from("test-federation-session-secret"),
                    Arc::new(DisabledEmailCodeVerifier),
                )
                .expect("test federation identity secret"),
            ),
            b"test-federation-flow-key",
        )))
        .merge(observability_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::new(
                    pg.clone(),
                    Arc::new(
                        PostgresObservabilityMetrics::new(pg.clone())
                            .with_valkey(valkey.clone()),
                    ),
                    Arc::new(UnavailableObservabilityMaintenance),
                )),
                Arc::new(DashboardObservabilityAuthorizer::new(
                    Arc::clone(&auth),
                    Arc::new(PgReadOnlyObservabilityTokenAuthorizer::new(pg.clone())),
                )),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        ))
        .merge(open_source_bounty_router(OpenSourceBountyState::new(
            pg.clone(),
            Arc::clone(&auth),
        )))
        .merge(relay_media_router(RelayMediaHttpState::new(Arc::new(DenyRelayMedia))))
        .merge(openai_relay_router(OpenAiRelayHttpState::new(
            Arc::new(DenyOpenAiRelay),
            env!("CARGO_PKG_VERSION"),
        )))
        .merge(lmm_api_rs::migration_routes::admin_catalog::router(
            AdminCatalogState::new(
                Arc::new(PgCatalogProvider::new(
                    pg.clone(),
                    Arc::new(DenyCatalogUpstream),
                )),
                catalog_authorizer,
            ),
        ))
        .merge(lmm_api_rs::migration_routes::billing_subscriptions::router(
            BillingSubscriptionsState::new(pg.clone(), Some(valkey.clone()), Arc::clone(&auth)),
        ))
        .merge(lmm_api_rs::migration_routes::channel_core::router(
            ChannelCoreState {
                pg: pg.clone(),
                valkey: valkey.clone(),
                authorizer: Arc::clone(&channel_authorizer),
                retry_times: 0,
            },
        ))
        .merge(
            lmm_api_rs::migration_routes::channel_ops::channel_ops_router(
                ChannelOpsHttpState::new(pg.clone(), valkey.clone(), channel_authorizer),
            ),
        )
        .merge(lmm_api_rs::migration_routes::identity_admin::router(
            IdentityAdminState::new(pg.clone(), valkey.clone(), Arc::clone(&auth)),
        ))
        .merge(lmm_api_rs::migration_routes::identity_profile::router(
            ProfileState::new(pg.clone(), valkey.clone()).with_dashboard_auth(Arc::clone(&auth)),
        ))
        .merge(safe_control_public_surface(pg.clone()))
        // The remainder of the candidate surface uses the same PostgreSQL and
        // dashboard-session authorities. Provider and relay boundaries remain
        // deliberately fail-closed on the isolated test instance.
        .merge(durable_missing_control_public_surface(
            pg.clone(),
            Arc::clone(&auth),
        ))
        .merge(ratio_sync_router(RatioSyncHttpState::new(
            Arc::new(PgRatioSyncRepository::new(pg.clone())),
            Arc::new(TestInstanceDisabledRatioSyncUpstream),
            Arc::new(DashboardRatioSyncAuthorizer::new(Arc::clone(&auth))),
        )))
        .merge(missing_control_tasks_router(MissingControlTasksState::new(
            Arc::new(PgControlTaskStore::new(pg.clone())),
            observability_authorizer,
            Arc::new(PgTestStatusProbe::new(pg.clone())),
        )))
        .merge(identity_catalog_router(IdentityCatalogState::new(
            pg.clone(),
            Arc::clone(&auth),
        )))
        .merge(identity_checkin_aff_router(IdentityCheckinAffState::new(
            pg.clone(),
            Arc::clone(&auth),
        ).with_effects(Arc::new(PgValkeyCheckinEffects::new(pg.clone(), valkey.clone())))))
        .merge(identity_topup_router(IdentityTopupState::new(
            pg.clone(),
            Arc::clone(&auth),
        )))
        .merge(identity_epay_router(UserTopupState::new(
            Arc::new(DashboardTopupAuthorizer::new(Arc::clone(&auth))),
            Arc::new(DisabledTopupRepository),
            Arc::new(DisabledEpayGateway),
        )))
        .merge(identity_stripe_creem_router(IdentityStripeCreemState::new(
            Arc::new(PgStripeCreemStore::new(pg.clone())),
            Arc::new(DashboardStripeCreemAuthorizer::new(Arc::clone(&auth))),
            Arc::new(DisabledStripeCreemGateway),
        )))
        .merge(identity_waffo_router(WaffoTopUpState::new(
            pg.clone(),
            Arc::clone(&auth),
            Arc::new(DisabledTopUpGateway),
        )))
        .merge(billing_dashboard_router(BillingDashboardState::new(
            Arc::new(PgBillingDashboardStore::new(pg.clone())),
            Arc::new(PgBillingDashboardAuthorizer::new(pg.clone())),
        )))
        .merge(missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(DisabledWaffoWebhookAvailability),
            Arc::new(DisabledPancakeWebhookVerifier),
            Arc::new(DisabledWaffoWebhookVerifier),
            Arc::new(DisabledWaffoWebhookProcessor),
        )))
        .merge(missing_relay_video_router(RelayVideoHttpState::new(
            Arc::new(DenyRelayVideo),
        )))
        .merge(missing_relay_misc_router(MissingRelayMiscState::new(
            Arc::new(DenyRelayMisc),
        )))
        // These four legacy relay seams and the frozen files/fine-tunes 501
        // endpoints must be registered together.  The test-only service
        // authenticates only a fixture token and otherwise never selects an
        // upstream or opens a network connection.
        .merge(relay_misc_candidate_router())
        .merge(router_with_model_lookup(
            RelayHttpState::new(Arc::new(TestInstanceRelayBackend)),
            ModelLookupState::new(Arc::new(PgStaticModelLookup::new(pg.clone())), env!("CARGO_PKG_VERSION")),
        ))
        // Setup must be reachable before a test-only root account exists.
        // Privileged routes retain the shared root-session guard and every
        // optional remote dependency is explicitly fail-closed.
        .merge(safe_system_config_surface(
            pg.clone(),
            valkey.clone(),
            Arc::clone(&auth),
        ))
        // Media tasks retain their real PostgreSQL token authentication.  The
        // test adapter denies every provider protocol after authentication, so
        // an imported snapshot can exercise route/auth compatibility without
        // contacting a selected production channel.
        .merge(media_midjourney_dynamic_router(MidjourneyHttpState::new(Arc::new(
            TestInstanceMidjourneyBackend::new(pg.clone()),
        ))))
        .merge(media_task_router(MediaTaskHttpState::new(Arc::new(
            MidjourneyMediaTaskService::new(Arc::new(TestInstanceMidjourneyBackend::new(pg))),
        ))))
}

/// PostgreSQL-backed read adapter for the legacy public/control endpoints.
///
/// The legacy option payloads are intentionally retained as JSON so unknown
/// fields survive the staged migration. Missing configuration yields an empty
/// JSON value rather than an invented successful provider result.
#[derive(Clone)]
struct PgMissingControlStore {
    pg: PgPool,
}

// These two values are part of the legacy `GET /api/pricing` wire contract.
// They are version markers, not hashes of the current database snapshot.
const LEGACY_PRICING_RESPONSE_VERSION: &str = "a42d372ccf0b5dd13ecf71203521f9d2";
const LEGACY_PRICING_FIRST_MODEL_VERSION: &str =
    "5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f";

#[derive(Clone, Debug)]
struct PricingAbility {
    model_name: String,
    group: String,
    channel_type: i64,
}

#[derive(Clone, Debug)]
struct PricingModelMetadata {
    model_name: String,
    description: String,
    icon: String,
    tags: String,
    vendor_id: i64,
    endpoints: String,
    status: i64,
    name_rule: i64,
}

#[derive(Clone, Debug)]
struct PricingVendor {
    id: i64,
    name: String,
    description: String,
    icon: String,
}

impl PgMissingControlStore {
    fn new(pg: PgPool) -> Self {
        Self { pg }
    }

    async fn option_json(&self, key: &str) -> Result<Option<Value>, MissingControlStoreError> {
        let value = sqlx::query_scalar::<_, String>("SELECT value FROM options WHERE key = $1")
            .bind(key)
            .fetch_optional(&self.pg)
            .await
            .map_err(|error| MissingControlStoreError::new(error.to_string()))?;
        Ok(value.and_then(|value| serde_json::from_str(&value).ok()))
    }

    async fn ranking_totals(
        &self,
        start: i64,
        end: i64,
    ) -> Result<Vec<RankingQuotaTotal>, MissingControlStoreError> {
        sqlx::query_as::<_, (String, i64)>(
            "SELECT model_name, SUM(token_used)::BIGINT AS total_tokens \
             FROM quota_data WHERE model_name <> '' AND created_at >= $1 AND created_at <= $2 \
             GROUP BY model_name HAVING SUM(token_used) > 0 ORDER BY total_tokens DESC",
        )
        .bind(start)
        .bind(end)
        .fetch_all(&self.pg)
        .await
        .map(|rows| {
            rows.into_iter()
                .map(|(model_name, total_tokens)| RankingQuotaTotal {
                    model_name,
                    total_tokens,
                })
                .collect()
        })
        .map_err(|error| MissingControlStoreError::new(error.to_string()))
    }

    async fn ranking_buckets(
        &self,
        start: i64,
        end: i64,
        bucket_seconds: i64,
    ) -> Result<Vec<RankingQuotaBucket>, MissingControlStoreError> {
        sqlx::query_as::<_, (String, i64, i64)>(
            "SELECT model_name, (created_at / $3) * $3 AS bucket, SUM(token_used)::BIGINT AS tokens \
             FROM quota_data WHERE model_name <> '' AND created_at >= $1 AND created_at <= $2 \
             GROUP BY model_name, (created_at / $3) * $3 HAVING SUM(token_used) > 0 ORDER BY bucket ASC",
        )
        .bind(start)
        .bind(end)
        .bind(bucket_seconds)
        .fetch_all(&self.pg)
        .await
        .map(|rows| {
            rows.into_iter()
                .map(|(model_name, bucket, tokens)| RankingQuotaBucket {
                    model_name,
                    bucket,
                    tokens,
                })
                .collect()
        })
        .map_err(|error| MissingControlStoreError::new(error.to_string()))
    }

    async fn ranking_model_metadata(
        &self,
    ) -> Result<BTreeMap<String, RankingModelMeta>, MissingControlStoreError> {
        // Go's ranking metadata is built from its pricing cache: only models
        // served by an enabled ability may inherit model/vendor metadata.
        let active_models = sqlx::query_scalar::<_, String>(
            "SELECT DISTINCT a.model FROM abilities a JOIN channels c ON c.id = a.channel_id \
             WHERE a.enabled = TRUE AND c.status = 1",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .collect::<BTreeSet<_>>();
        if active_models.is_empty() {
            return Ok(BTreeMap::new());
        }

        let models = sqlx::query_as::<_, (i64, String, i64, i64, i64)>(
            "SELECT id, model_name, COALESCE(vendor_id, 0), COALESCE(name_rule, 0), COALESCE(status, 1) \
             FROM models WHERE deleted_at IS NULL ORDER BY id",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?;
        let vendor_by_id = sqlx::query_as::<_, (i64, String, Option<String>)>(
            "SELECT id, name, icon FROM vendors WHERE deleted_at IS NULL",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .map(|(id, name, icon)| (id, (name, icon.unwrap_or_default())))
        .collect::<BTreeMap<_, _>>();

        let mut metadata = BTreeMap::new();
        for model_name in active_models {
            let Some((_, _, vendor_id, _, status)) = models
                .iter()
                .find(|(_, metadata_name, _, name_rule, _)| {
                    *name_rule == 0 && metadata_name == &model_name
                })
                .or_else(|| {
                    models.iter().find(|(_, metadata_name, _, name_rule, _)| {
                        *name_rule == 1 && model_name.starts_with(metadata_name)
                    })
                })
                .or_else(|| {
                    models.iter().find(|(_, metadata_name, _, name_rule, _)| {
                        *name_rule == 3 && model_name.ends_with(metadata_name)
                    })
                })
                .or_else(|| {
                    models.iter().find(|(_, metadata_name, _, name_rule, _)| {
                        *name_rule == 2 && model_name.contains(metadata_name)
                    })
                })
            else {
                continue;
            };
            if *status != 1 {
                continue;
            }
            let Some((vendor, vendor_icon)) = vendor_by_id.get(vendor_id) else {
                continue;
            };
            metadata.insert(
                model_name,
                RankingModelMeta {
                    vendor: vendor.clone(),
                    vendor_icon: vendor_icon.clone(),
                },
            );
        }
        Ok(metadata)
    }
}

/// The Go dashboard endpoint returns the process-wide adaptor catalogue,
/// keyed by legacy channel type. It is not a query over the copied channels
/// table; retaining the checked-in frozen dataset keeps test-instance output
/// stable when the database contains unrelated or synthetic channels.
const FROZEN_DASHBOARD_CHANNEL_MODELS_SHA256: &str =
    "cb4e3e3eac50b4f9d251d8768bb2e8e6e347dcd4da6be55bdb6a70a51e2b270e";

fn frozen_dashboard_models() -> Value {
    let fixture = include_str!("../assets/channel-id2models-go-v1.json");
    let digest = Sha256::digest(fixture.as_bytes());
    assert_eq!(
        format!("{digest:x}"),
        FROZEN_DASHBOARD_CHANNEL_MODELS_SHA256,
        "pinned Go channelId2Models fixture changed"
    );
    serde_json::from_str(fixture)
        .expect("checked-in frozen dashboard model catalogue is valid JSON")
}

fn object_option<'a>(
    options: &'a BTreeMap<String, Value>,
    key: &str,
) -> Option<&'a Map<String, Value>> {
    options.get(key).and_then(Value::as_object)
}

fn configured_object(
    options: &BTreeMap<String, Value>,
    key: &str,
    default: Map<String, Value>,
) -> Map<String, Value> {
    object_option(options, key).cloned().unwrap_or(default)
}

fn configured_auto_groups(options: &BTreeMap<String, Value>) -> Vec<String> {
    options
        .get("AutoGroups")
        .and_then(Value::as_array)
        .map(|groups| {
            groups
                .iter()
                .filter_map(Value::as_str)
                .map(ToOwned::to_owned)
                .collect()
        })
        // `setting.autoGroups` starts as ["default"] before persisted
        // options are loaded. An explicit empty array stays explicit.
        .unwrap_or_else(|| vec!["default".to_owned()])
}

fn default_group_ratio() -> Map<String, Value> {
    [
        ("default".to_owned(), json!(1)),
        ("vip".to_owned(), json!(1)),
        ("svip".to_owned(), json!(1)),
    ]
    .into_iter()
    .collect()
}

fn default_usable_groups() -> Map<String, Value> {
    [
        ("default".to_owned(), json!("默认分组")),
        ("vip".to_owned(), json!("vip分组")),
    ]
    .into_iter()
    .collect()
}

fn default_group_group_ratio() -> Map<String, Value> {
    [("vip".to_owned(), json!({"edit_this": 0.9}))]
        .into_iter()
        .collect()
}

fn metadata_for_model<'a>(
    model_name: &str,
    metadata: &'a [PricingModelMetadata],
) -> Option<&'a PricingModelMetadata> {
    // The legacy cache first maps exact names, then applies prefix, suffix,
    // and contains metadata in database load order. The query above orders by
    // id, which makes this formerly database-dependent rule reproducible.
    metadata
        .iter()
        .find(|metadata| metadata.name_rule == 0 && metadata.model_name == model_name)
        .or_else(|| {
            metadata.iter().find(|metadata| {
                metadata.name_rule == 1 && model_name.starts_with(&metadata.model_name)
            })
        })
        .or_else(|| {
            metadata.iter().find(|metadata| {
                metadata.name_rule == 3 && model_name.ends_with(&metadata.model_name)
            })
        })
        .or_else(|| {
            metadata.iter().find(|metadata| {
                metadata.name_rule == 2 && model_name.contains(&metadata.model_name)
            })
        })
}

fn endpoint_types_for_channel(channel_type: i64, model_name: &str) -> Vec<&'static str> {
    let mut endpoint_types = match channel_type {
        38 => vec!["jina-rerank"],
        14 | 33 => vec!["anthropic", "openai"],
        24 | 41 => vec!["gemini", "openai"],
        20 => vec!["openai"],
        48 => vec!["openai", "openai-response"],
        55 => vec!["openai-video"],
        57 => vec![
            "openai-response",
            "openai-response-compact",
            "openai-alpha-search",
        ],
        59 | 60 => vec![
            "openai",
            "openai-response",
            "openai-response-compact",
            "anthropic",
            "gemini",
            "openai-alpha-search",
        ],
        _ if is_response_only_model(model_name) => vec!["openai-response"],
        _ => vec!["openai"],
    };
    if is_image_generation_model(model_name) {
        endpoint_types.insert(0, "image-generation");
    }
    endpoint_types
}

fn is_response_only_model(model_name: &str) -> bool {
    ["o3-pro", "o3-deep-research", "o4-mini-deep-research"]
        .iter()
        .any(|known| model_name.contains(known))
}

fn is_image_generation_model(model_name: &str) -> bool {
    let model_name = model_name.to_ascii_lowercase();
    ["dall-e-3", "dall-e-2", "gpt-image-1", "flux-", "flux.1-"]
        .iter()
        .any(|needle| model_name.contains(needle))
        || model_name.starts_with("imagen-")
}

fn default_endpoint_info(endpoint_type: &str) -> Option<Value> {
    let (path, method) = match endpoint_type {
        "openai" => ("/v1/chat/completions", "POST"),
        "openai-response" => ("/v1/responses", "POST"),
        "openai-response-compact" => ("/v1/responses/compact", "POST"),
        "openai-alpha-search" => ("/v1/alpha/search", "POST"),
        "anthropic" => ("/v1/messages", "POST"),
        "gemini" => ("/v1beta/models/{model}:generateContent", "POST"),
        "jina-rerank" => ("/v1/rerank", "POST"),
        "image-generation" => ("/v1/images/generations", "POST"),
        "embeddings" => ("/v1/embeddings", "POST"),
        _ => return None,
    };
    Some(json!({"path": path, "method": method}))
}

fn append_endpoint(endpoint_types: &mut Vec<String>, endpoint_type: impl Into<String>) {
    let endpoint_type = endpoint_type.into();
    if !endpoint_type.is_empty() && !endpoint_types.iter().any(|known| known == &endpoint_type) {
        endpoint_types.push(endpoint_type);
    }
}

fn custom_endpoints(metadata: Option<&PricingModelMetadata>) -> Vec<(String, Value)> {
    let Some(metadata) = metadata else {
        return Vec::new();
    };
    let Ok(Value::Object(endpoints)) = serde_json::from_str::<Value>(&metadata.endpoints) else {
        return Vec::new();
    };
    endpoints
        .into_iter()
        .filter(|(_, value)| matches!(value, Value::String(_) | Value::Object(_)))
        .collect()
}

fn usable_groups_for_actor(
    options: &BTreeMap<String, Value>,
    user_group: Option<&str>,
) -> Map<String, Value> {
    let mut groups = configured_object(options, "UserUsableGroups", default_usable_groups());
    if let Some(user_group) = user_group.filter(|group| !group.is_empty())
        && !groups.contains_key(user_group)
    {
        groups.insert(user_group.to_owned(), json!("用户分组"));
    }
    groups
}

fn group_ratio_for_actor(
    options: &BTreeMap<String, Value>,
    user_group: Option<&str>,
) -> Map<String, Value> {
    let mut ratios = configured_object(options, "GroupRatio", default_group_ratio());
    let overrides = configured_object(options, "GroupGroupRatio", default_group_group_ratio());
    if let Some(user_group) = user_group.filter(|group| !group.is_empty())
        && let Some(Value::Object(values)) = overrides.get(user_group)
    {
        ratios.extend(values.clone());
    }
    ratios
}

fn public_pricing_view(
    pricing: Vec<Value>,
    group_ratio: Map<String, Value>,
    configured_groups: &Map<String, Value>,
) -> (Vec<Value>, Map<String, Value>, Map<String, Value>) {
    let mut represented_groups = BTreeSet::new();
    let mut all_groups_enabled = false;
    for item in &pricing {
        for group in item["enable_groups"].as_array().into_iter().flatten() {
            let Some(group) = group.as_str() else {
                continue;
            };
            if group == "all" {
                all_groups_enabled = true;
            } else {
                represented_groups.insert(group.to_owned());
            }
        }
    }

    let mut disclosed_ratios = Map::new();
    let mut disclosed_groups = Map::new();
    for (group, ratio) in &group_ratio {
        if group == "all" || (!all_groups_enabled && !represented_groups.contains(group)) {
            continue;
        }
        disclosed_ratios.insert(group.clone(), ratio.clone());
        disclosed_groups.insert(
            group.clone(),
            configured_groups
                .get(group)
                .cloned()
                .unwrap_or_else(|| Value::from(group.clone())),
        );
    }
    if !all_groups_enabled {
        for group in represented_groups {
            if disclosed_ratios.contains_key(&group) {
                continue;
            }
            disclosed_ratios.insert(group.clone(), json!(1));
            disclosed_groups.insert(
                group.clone(),
                configured_groups
                    .get(&group)
                    .cloned()
                    .unwrap_or_else(|| Value::from(group)),
            );
        }
    }
    (pricing, disclosed_ratios, disclosed_groups)
}

fn authenticated_pricing_view(
    pricing: Vec<Value>,
    group_ratio: Map<String, Value>,
    usable_groups: Map<String, Value>,
) -> (Vec<Value>, Map<String, Value>, Map<String, Value>) {
    let pricing = pricing
        .into_iter()
        .filter(|item| {
            item["enable_groups"].as_array().is_some_and(|groups| {
                groups.iter().any(|group| {
                    group
                        .as_str()
                        .is_some_and(|group| group == "all" || usable_groups.contains_key(group))
                })
            })
        })
        .collect();
    let group_ratio = group_ratio
        .into_iter()
        .filter(|(group, _)| usable_groups.contains_key(group))
        .collect();
    (pricing, group_ratio, usable_groups)
}

fn option_for_model(
    options: &BTreeMap<String, Value>,
    key: &str,
    model_name: &str,
) -> Option<Value> {
    object_option(options, key)
        .and_then(|values| values.get(model_name))
        .cloned()
}

fn build_pricing_snapshot(
    abilities: Vec<PricingAbility>,
    metadata: Vec<PricingModelMetadata>,
    vendors: Vec<PricingVendor>,
    options: &BTreeMap<String, Value>,
    user_group: Option<&str>,
) -> Value {
    let mut abilities_by_model = BTreeMap::<String, Vec<PricingAbility>>::new();
    for ability in abilities {
        abilities_by_model
            .entry(ability.model_name.clone())
            .or_default()
            .push(ability);
    }

    let mut supported_endpoint = Map::new();
    let mut pricing = Vec::new();
    for (model_name, abilities) in abilities_by_model {
        let metadata = metadata_for_model(&model_name, &metadata);
        if metadata.is_some_and(|metadata| metadata.status != 1) {
            continue;
        }
        let mut enable_groups = BTreeSet::new();
        let mut endpoint_types = Vec::new();
        for ability in abilities {
            enable_groups.insert(ability.group);
            for endpoint_type in endpoint_types_for_channel(ability.channel_type, &model_name) {
                append_endpoint(&mut endpoint_types, endpoint_type);
            }
        }
        for (endpoint_type, endpoint) in custom_endpoints(metadata) {
            append_endpoint(&mut endpoint_types, endpoint_type.clone());
            let endpoint = match endpoint {
                Value::String(path) => json!({"path": path, "method": "POST"}),
                Value::Object(endpoint) => json!({
                    "path": endpoint.get("path").and_then(Value::as_str).unwrap_or_default(),
                    "method": endpoint
                        .get("method")
                        .and_then(Value::as_str)
                        .map(str::to_ascii_uppercase)
                        .unwrap_or_else(|| "POST".to_owned()),
                }),
                _ => continue,
            };
            supported_endpoint.insert(endpoint_type, endpoint);
        }
        for endpoint_type in &endpoint_types {
            if let Some(endpoint) = default_endpoint_info(endpoint_type) {
                supported_endpoint
                    .entry(endpoint_type.clone())
                    .or_insert(endpoint);
            }
        }

        let model_price = option_for_model(options, "ModelPrice", &model_name);
        let quota_type = i64::from(model_price.is_some());
        let mut item = Map::new();
        item.insert("model_name".to_owned(), Value::from(model_name.clone()));
        if let Some(metadata) = metadata {
            if !metadata.description.is_empty() {
                item.insert(
                    "description".to_owned(),
                    Value::from(metadata.description.clone()),
                );
            }
            if !metadata.icon.is_empty() {
                item.insert("icon".to_owned(), Value::from(metadata.icon.clone()));
            }
            if !metadata.tags.is_empty() {
                item.insert("tags".to_owned(), Value::from(metadata.tags.clone()));
            }
            if metadata.vendor_id != 0 {
                item.insert("vendor_id".to_owned(), Value::from(metadata.vendor_id));
            }
        }
        item.insert("quota_type".to_owned(), Value::from(quota_type));
        item.insert(
            "model_ratio".to_owned(),
            if quota_type == 0 {
                option_for_model(options, "ModelRatio", &model_name).unwrap_or_else(|| json!(37.5))
            } else {
                json!(0)
            },
        );
        item.insert(
            "model_price".to_owned(),
            model_price.unwrap_or_else(|| json!(0)),
        );
        item.insert("owner_by".to_owned(), Value::String(String::new()));
        item.insert(
            "completion_ratio".to_owned(),
            if quota_type == 0 {
                option_for_model(options, "CompletionRatio", &model_name)
                    .unwrap_or_else(|| json!(1))
            } else {
                json!(0)
            },
        );
        for (field, option) in [
            ("cache_ratio", "CacheRatio"),
            ("create_cache_ratio", "CreateCacheRatio"),
            ("image_ratio", "ImageRatio"),
            ("audio_ratio", "AudioRatio"),
            ("audio_completion_ratio", "AudioCompletionRatio"),
        ] {
            if let Some(value) = option_for_model(options, option, &model_name) {
                item.insert(field.to_owned(), value);
            }
        }
        if option_for_model(options, "billing_setting.billing_mode", &model_name)
            .and_then(|mode| mode.as_str().map(ToOwned::to_owned))
            .is_some_and(|mode| mode == "tiered_expr")
            && let Some(expression) =
                option_for_model(options, "billing_setting.billing_expr", &model_name)
                    .and_then(|expression| expression.as_str().map(ToOwned::to_owned))
                    .filter(|expression| !expression.trim().is_empty())
        {
            item.insert("billing_mode".to_owned(), Value::from("tiered_expr"));
            item.insert("billing_expr".to_owned(), Value::from(expression));
        }
        item.insert(
            "enable_groups".to_owned(),
            Value::Array(enable_groups.into_iter().map(Value::from).collect()),
        );
        item.insert(
            "supported_endpoint_types".to_owned(),
            Value::Array(endpoint_types.into_iter().map(Value::from).collect()),
        );
        pricing.push(Value::Object(item));
    }
    if let Some(Value::Object(first)) = pricing.first_mut() {
        first.insert(
            "pricing_version".to_owned(),
            Value::from(LEGACY_PRICING_FIRST_MODEL_VERSION),
        );
    }

    let configured_groups = usable_groups_for_actor(options, None);
    let group_ratio = group_ratio_for_actor(options, user_group);
    let (pricing, group_ratio, usable_group) = match user_group {
        Some(_) => authenticated_pricing_view(
            pricing,
            group_ratio,
            usable_groups_for_actor(options, user_group),
        ),
        None => public_pricing_view(pricing, group_ratio, &configured_groups),
    };
    let auto_groups = if options.is_empty() {
        // Go initializes setting.autoGroups to ["default"] before loading
        // persisted options. With no options at all, there is no usable-group
        // entry to filter that bootstrap value against.
        vec!["default".to_owned()]
    } else {
        configured_auto_groups(options)
            .into_iter()
            .filter(|group| usable_group.contains_key(group))
            .collect::<Vec<_>>()
    };

    json!({
        "success": true,
        "data": pricing,
        "vendors": vendors.into_iter().map(|vendor| json!({
            "id": vendor.id,
            "name": vendor.name,
            "description": vendor.description,
            "icon": vendor.icon,
        })).collect::<Vec<_>>(),
        "group_ratio": group_ratio,
        "usable_group": usable_group,
        "supported_endpoint": supported_endpoint,
        "auto_groups": auto_groups,
        "pricing_version": LEGACY_PRICING_RESPONSE_VERSION,
    })
}

#[async_trait]
impl MissingControlStore for PgMissingControlStore {
    async fn header_nav(&self, module: &str) -> Result<HeaderNavAccess, MissingControlStoreError> {
        let Some(Value::Object(modules)) = self.option_json("HeaderNavModules").await? else {
            return Ok(HeaderNavAccess::default());
        };
        Ok(parse_header_nav_access(modules.get(module)))
    }

    async fn groups(&self) -> Result<Vec<String>, MissingControlStoreError> {
        // `controller.GetGroups` enumerates the configured GroupRatio map,
        // not the currently enabled abilities. A group with no active channel
        // must remain visible to the administration UI.
        let Some(Value::Object(ratios)) = self.option_json("GroupRatio").await? else {
            return Ok(Vec::new());
        };
        Ok(ratios.into_iter().map(|(group, _)| group).collect())
    }

    async fn dashboard_models(&self) -> Value {
        frozen_dashboard_models()
    }

    async fn pricing(
        &self,
        actor: Option<
            lmm_api_rs::migration_routes::missing_control_public::MissingControlPrincipal,
        >,
    ) -> Result<Value, MissingControlStoreError> {
        // Go's pricing cache is built from every enabled ability.  In
        // particular, its cache refresh uses a left join and does not filter
        // a disabled channel here; changing that would silently remove a
        // pricing row that the legacy listener still exposes.
        let ability_rows = sqlx::query_as::<_, (String, String, i64)>(
            r#"SELECT a.model, a."group", COALESCE(c.type, 0)
                 FROM abilities a LEFT JOIN channels c ON c.id = a.channel_id
                WHERE a.enabled = TRUE
                ORDER BY a.model, a.channel_id, a."group""#,
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .map(|(model_name, group, channel_type)| PricingAbility {
            model_name,
            group,
            channel_type,
        })
        .collect();
        let metadata = sqlx::query_as::<
            _,
            (
                i64,
                String,
                Option<String>,
                Option<String>,
                Option<String>,
                Option<i64>,
                Option<String>,
                Option<i64>,
                Option<i64>,
            ),
        >(
            "SELECT id, model_name, description, icon, tags, vendor_id, endpoints, status, name_rule \
             FROM models WHERE deleted_at IS NULL ORDER BY id",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .map(
            |(
                _,
                model_name,
                description,
                icon,
                tags,
                vendor_id,
                endpoints,
                status,
                name_rule,
            )| PricingModelMetadata {
                model_name,
                description: description.unwrap_or_default(),
                icon: icon.unwrap_or_default(),
                tags: tags.unwrap_or_default(),
                vendor_id: vendor_id.unwrap_or_default(),
                endpoints: endpoints.unwrap_or_default(),
                status: status.unwrap_or(1),
                name_rule: name_rule.unwrap_or_default(),
            },
        )
        .collect();
        let vendors = sqlx::query_as::<_, (i64, String, Option<String>, Option<String>)>(
            "SELECT id, name, description, icon FROM vendors WHERE deleted_at IS NULL ORDER BY id",
        )
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .map(|(id, name, description, icon)| PricingVendor {
            id,
            name,
            description: description.unwrap_or_default(),
            icon: icon.unwrap_or_default(),
        })
        .collect();
        let options = sqlx::query_as::<_, (String, String)>(
            "SELECT key, value FROM options WHERE key = ANY($1)",
        )
        .bind(vec![
            "GroupRatio",
            "GroupGroupRatio",
            "UserUsableGroups",
            "AutoGroups",
            "ModelRatio",
            "ModelPrice",
            "CompletionRatio",
            "CacheRatio",
            "CreateCacheRatio",
            "ImageRatio",
            "AudioRatio",
            "AudioCompletionRatio",
            "billing_setting.billing_mode",
            "billing_setting.billing_expr",
        ])
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .into_iter()
        .filter_map(|(key, value)| serde_json::from_str(&value).ok().map(|value| (key, value)))
        .collect::<std::collections::BTreeMap<_, _>>();
        let user_group = if let Some(actor) = actor {
            sqlx::query_scalar::<_, String>("SELECT \"group\" FROM users WHERE id = $1")
                .bind(actor.user_id)
                .fetch_optional(&self.pg)
                .await
                .map_err(|error| MissingControlStoreError::new(error.to_string()))?
                .unwrap_or_default()
        } else {
            String::new()
        };
        Ok(build_pricing_snapshot(
            ability_rows,
            metadata,
            vendors,
            &options,
            (!user_group.is_empty()).then_some(user_group.as_str()),
        ))
    }

    async fn rankings(&self, period: &str) -> Result<Value, MissingControlStoreError> {
        let config = ranking_period(period).ok_or_else(|| {
            MissingControlStoreError::new(format!("invalid ranking period: {period}"))
        })?;
        let now = Utc::now().timestamp();
        let current_start = now - config.duration_seconds;
        let previous_start = current_start - config.duration_seconds;
        let previous_end = current_start - 1;

        let current_totals = self.ranking_totals(current_start, now).await?;
        let current_buckets = self
            .ranking_buckets(current_start, now, config.bucket_seconds)
            .await?;
        let previous_totals = self.ranking_totals(previous_start, previous_end).await?;
        let metadata = self.ranking_model_metadata().await?;

        Ok(build_rankings_snapshot(
            config,
            current_totals,
            previous_totals,
            current_buckets,
            &metadata,
        ))
    }

    async fn exposed_ratio(&self) -> Result<Option<Value>, MissingControlStoreError> {
        let enabled = sqlx::query_scalar::<_, String>(
            "SELECT value FROM options WHERE key = 'ExposeRatioEnabled'",
        )
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1");
        if !enabled {
            return Ok(None);
        }

        // Go builds this payload from its ratio_setting maps on every cache
        // refresh. Keep the exact five source options rather than a stale or
        // non-existent `RatioConfig` option.
        let rows = sqlx::query_as::<_, (String, String)>(
            "SELECT key, value FROM options WHERE key = ANY($1)",
        )
        .bind(vec![
            "ModelRatio",
            "CompletionRatio",
            "CacheRatio",
            "CreateCacheRatio",
            "ModelPrice",
        ])
        .fetch_all(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?;
        let values = rows
            .into_iter()
            .filter_map(|(key, value)| serde_json::from_str(&value).ok().map(|value| (key, value)))
            .collect::<std::collections::BTreeMap<_, _>>();
        Ok(Some(json!({
            "model_ratio": values.get("ModelRatio").cloned().unwrap_or_else(|| json!({})),
            "completion_ratio": values.get("CompletionRatio").cloned().unwrap_or_else(|| json!({})),
            "cache_ratio": values.get("CacheRatio").cloned().unwrap_or_else(|| json!({})),
            "create_cache_ratio": values.get("CreateCacheRatio").cloned().unwrap_or_else(|| json!({})),
            "model_price": values.get("ModelPrice").cloned().unwrap_or_else(|| json!({})),
        })))
    }

    async fn token_usage(&self, key: &str) -> Result<Option<Value>, MissingControlStoreError> {
        self.token_usage_for_owner(key, 0).await
    }

    async fn token_usage_for_owner(
        &self,
        key: &str,
        owner_id: i64,
    ) -> Result<Option<Value>, MissingControlStoreError> {
        let row = sqlx::query_as::<_, (String, i64, i64, bool, bool, String, i64)>(
            "SELECT name, used_quota, remain_quota, unlimited_quota, model_limits_enabled, \
             model_limits, expired_time FROM tokens WHERE key = $1 AND deleted_at IS NULL \
             AND user_id = $2",
        )
        .bind(key)
        .bind(owner_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?;
        Ok(row.map(
            |(name, used, remain, unlimited, limits_enabled, limits, expired)| {
                let model_limits = limits
                    .split(',')
                    .filter(|limit| !limit.is_empty())
                    .map(|limit| (limit.to_owned(), Value::Bool(true)))
                    .collect::<serde_json::Map<_, _>>();
                json!({
                    "object": "token_usage",
                    "name": name,
                    "total_granted": remain + used,
                    "total_used": used,
                    "total_available": remain,
                    "unlimited_quota": unlimited,
                    "model_limits": model_limits,
                    "model_limits_enabled": limits_enabled,
                    "expires_at": if expired == -1 { 0 } else { expired },
                })
            },
        ))
    }

    async fn token_auth_read_only(
        &self,
        key: &str,
    ) -> Result<
        Option<lmm_api_rs::migration_routes::missing_control_public::MissingControlToken>,
        MissingControlStoreError,
    > {
        let Some((status, user_id)) = sqlx::query_as::<_, (i64, i64)>(
            "SELECT COALESCE(status, 1), user_id FROM tokens \
             WHERE deleted_at IS NULL AND (key = $1 OR key LIKE $1 || '-%') \
             ORDER BY CASE WHEN key = $1 THEN 0 ELSE 1 END, id LIMIT 1",
        )
        .bind(key)
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        else {
            return Ok(None);
        };
        let Some((user_status, setting)) = sqlx::query_as::<_, (i64, String)>(
            "SELECT status, setting FROM users WHERE id = $1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(|error| MissingControlStoreError::new(error.to_string()))?
        else {
            return Err(MissingControlStoreError::new(format!(
                "token owner {user_id} is missing"
            )));
        };
        let saved_language = serde_json::from_str::<Value>(&setting)
            .ok()
            .and_then(|setting| setting.get("language")?.as_str().map(str::to_owned));
        Ok(Some(
            lmm_api_rs::migration_routes::missing_control_public::MissingControlToken {
                user_id,
                status,
                user_status,
                saved_language,
            },
        ))
    }
}

const RANKING_LEADERBOARD_LIMIT: usize = 20;
const RANKING_HISTORY_LIMIT: usize = 10;
const RANKING_VENDOR_LIMIT: usize = 5;
const RANKING_MOVER_LIMIT: usize = 6;
const RANKING_UNKNOWN_VENDOR: &str = "Unknown";
const RANKING_OTHERS_LABEL: &str = "Others";

#[derive(Clone, Copy)]
enum RankingLabel {
    Hour,
    Day,
}

#[derive(Clone, Copy)]
struct RankingPeriod {
    duration_seconds: i64,
    bucket_seconds: i64,
    label: RankingLabel,
}

struct RankingQuotaTotal {
    model_name: String,
    total_tokens: i64,
}

struct RankingQuotaBucket {
    model_name: String,
    bucket: i64,
    tokens: i64,
}

#[derive(Clone, Default)]
struct RankingModelMeta {
    vendor: String,
    vendor_icon: String,
}

#[derive(Clone)]
struct RankedModel {
    rank: usize,
    previous_rank: Option<usize>,
    model_name: String,
    vendor: String,
    vendor_icon: String,
    total_tokens: i64,
    share: f64,
    growth_pct: f64,
}

struct RankedVendor {
    rank: usize,
    vendor: String,
    vendor_icon: String,
    total_tokens: i64,
    share: f64,
    growth_pct: f64,
    models: BTreeSet<String>,
    top_model: String,
}

#[derive(Default)]
struct VendorAggregate {
    vendor_icon: String,
    total_tokens: i64,
    previous_tokens: i64,
    models: BTreeSet<String>,
    top_model: String,
    top_model_tokens: i64,
}

fn ranking_period(period: &str) -> Option<RankingPeriod> {
    match period {
        "today" => Some(RankingPeriod {
            duration_seconds: 24 * 60 * 60,
            bucket_seconds: 60 * 60,
            label: RankingLabel::Hour,
        }),
        "week" => Some(RankingPeriod {
            duration_seconds: 7 * 24 * 60 * 60,
            bucket_seconds: 24 * 60 * 60,
            label: RankingLabel::Day,
        }),
        "month" => Some(RankingPeriod {
            duration_seconds: 30 * 24 * 60 * 60,
            bucket_seconds: 24 * 60 * 60,
            label: RankingLabel::Day,
        }),
        "year" => Some(RankingPeriod {
            duration_seconds: 365 * 24 * 60 * 60,
            bucket_seconds: 7 * 24 * 60 * 60,
            label: RankingLabel::Day,
        }),
        _ => None,
    }
}

fn build_rankings_snapshot(
    period: RankingPeriod,
    current_totals: Vec<RankingQuotaTotal>,
    previous_totals: Vec<RankingQuotaTotal>,
    buckets: Vec<RankingQuotaBucket>,
    metadata: &BTreeMap<String, RankingModelMeta>,
) -> Value {
    let total_tokens = current_totals.iter().map(|row| row.total_tokens).sum();
    let previous_ranks = previous_totals
        .iter()
        .enumerate()
        .map(|(index, row)| (row.model_name.as_str(), index + 1))
        .collect::<BTreeMap<_, _>>();
    let previous_tokens = previous_totals
        .iter()
        .map(|row| (row.model_name.as_str(), row.total_tokens))
        .collect::<BTreeMap<_, _>>();

    let models = current_totals
        .iter()
        .enumerate()
        .map(|(index, row)| {
            let meta = metadata.get(&row.model_name).cloned().unwrap_or_default();
            RankedModel {
                rank: index + 1,
                previous_rank: previous_ranks.get(row.model_name.as_str()).copied(),
                model_name: row.model_name.clone(),
                vendor: nonempty_vendor(&meta.vendor).to_owned(),
                vendor_icon: meta.vendor_icon,
                total_tokens: row.total_tokens,
                share: ranking_share(row.total_tokens, total_tokens),
                growth_pct: ranking_growth(
                    row.total_tokens,
                    previous_tokens
                        .get(row.model_name.as_str())
                        .copied()
                        .unwrap_or_default(),
                ),
            }
        })
        .collect::<Vec<_>>();
    let vendors = ranked_vendors(&current_totals, &previous_totals, total_tokens, metadata);
    let (top_movers, top_droppers) = ranking_movers(&models);

    json!({
        "models": models.iter().take(RANKING_LEADERBOARD_LIMIT).map(ranked_model_json).collect::<Vec<_>>(),
        "vendors": vendors.iter().map(ranked_vendor_json).collect::<Vec<_>>(),
        "top_movers": top_movers,
        "top_droppers": top_droppers,
        "models_history": model_history(&buckets, &current_totals, metadata, period),
        "vendor_share_history": vendor_share_history(&buckets, &vendors, metadata, period),
    })
}

fn ranked_vendors(
    current: &[RankingQuotaTotal],
    previous: &[RankingQuotaTotal],
    total_tokens: i64,
    metadata: &BTreeMap<String, RankingModelMeta>,
) -> Vec<RankedVendor> {
    let mut aggregates = BTreeMap::<String, VendorAggregate>::new();
    for row in current {
        let meta = metadata.get(&row.model_name).cloned().unwrap_or_default();
        let vendor = nonempty_vendor(&meta.vendor).to_owned();
        let aggregate = aggregates.entry(vendor).or_default();
        if aggregate.vendor_icon.is_empty() {
            aggregate.vendor_icon = meta.vendor_icon;
        }
        aggregate.total_tokens += row.total_tokens;
        aggregate.models.insert(row.model_name.clone());
        if row.total_tokens > aggregate.top_model_tokens {
            aggregate.top_model = row.model_name.clone();
            aggregate.top_model_tokens = row.total_tokens;
        }
    }
    for row in previous {
        let meta = metadata.get(&row.model_name).cloned().unwrap_or_default();
        let vendor = nonempty_vendor(&meta.vendor).to_owned();
        let aggregate = aggregates.entry(vendor).or_default();
        if aggregate.vendor_icon.is_empty() {
            aggregate.vendor_icon = meta.vendor_icon;
        }
        aggregate.previous_tokens += row.total_tokens;
    }

    let mut vendors = aggregates
        .into_iter()
        .filter_map(|(vendor, aggregate)| {
            (aggregate.total_tokens > 0).then(|| RankedVendor {
                rank: 0,
                vendor,
                vendor_icon: aggregate.vendor_icon,
                total_tokens: aggregate.total_tokens,
                share: ranking_share(aggregate.total_tokens, total_tokens),
                growth_pct: ranking_growth(aggregate.total_tokens, aggregate.previous_tokens),
                models: aggregate.models,
                top_model: aggregate.top_model,
            })
        })
        .collect::<Vec<_>>();
    vendors.sort_by(|left, right| {
        right
            .total_tokens
            .cmp(&left.total_tokens)
            .then_with(|| left.vendor.cmp(&right.vendor))
    });
    for (index, vendor) in vendors.iter_mut().enumerate() {
        vendor.rank = index + 1;
    }
    vendors
}

fn model_history(
    buckets: &[RankingQuotaBucket],
    totals: &[RankingQuotaTotal],
    metadata: &BTreeMap<String, RankingModelMeta>,
    period: RankingPeriod,
) -> Value {
    let top_models = totals
        .iter()
        .take(RANKING_HISTORY_LIMIT)
        .map(|row| row.model_name.as_str())
        .collect::<BTreeSet<_>>();
    let mut model_rows = totals
        .iter()
        .take(RANKING_HISTORY_LIMIT)
        .map(|row| {
            let meta = metadata.get(&row.model_name).cloned().unwrap_or_default();
            json!({"name": row.model_name, "vendor": nonempty_vendor(&meta.vendor), "total": row.total_tokens})
        })
        .collect::<Vec<_>>();
    let other_total = totals
        .iter()
        .skip(RANKING_HISTORY_LIMIT)
        .map(|row| row.total_tokens)
        .sum::<i64>();
    if other_total > 0 {
        model_rows
            .push(json!({"name": RANKING_OTHERS_LABEL, "vendor": "Various", "total": other_total}));
    }

    let mut tokens = BTreeMap::<i64, BTreeMap<String, i64>>::new();
    for row in buckets {
        let model = if top_models.contains(row.model_name.as_str()) {
            row.model_name.clone()
        } else {
            RANKING_OTHERS_LABEL.to_owned()
        };
        *tokens
            .entry(row.bucket)
            .or_default()
            .entry(model)
            .or_default() += row.tokens;
    }
    let points = tokens
        .iter()
        .flat_map(|(bucket, by_model)| {
            model_rows.iter().filter_map(move |model| {
                let name = model.get("name")?.as_str()?;
                let token_count = *by_model.get(name).unwrap_or(&0);
                (token_count > 0).then(|| {
                    json!({
                        "ts": ranking_bucket_timestamp(*bucket),
                        "label": ranking_bucket_label(*bucket, period),
                        "model": name,
                        "vendor": model.get("vendor").and_then(Value::as_str).unwrap_or_default(),
                        "tokens": token_count,
                    })
                })
            })
        })
        .collect::<Vec<_>>();
    json!({"points": points, "models": model_rows, "buckets": tokens.len()})
}

fn vendor_share_history(
    buckets: &[RankingQuotaBucket],
    vendors: &[RankedVendor],
    metadata: &BTreeMap<String, RankingModelMeta>,
    period: RankingPeriod,
) -> Value {
    let top_vendors = vendors
        .iter()
        .take(RANKING_VENDOR_LIMIT)
        .map(|vendor| vendor.vendor.as_str())
        .collect::<BTreeSet<_>>();
    let mut vendor_rows = vendors
        .iter()
        .take(RANKING_VENDOR_LIMIT)
        .map(|vendor| json!({"name": vendor.vendor, "total": vendor.total_tokens, "share": vendor.share}))
        .collect::<Vec<_>>();
    let other_total = vendors
        .iter()
        .skip(RANKING_VENDOR_LIMIT)
        .map(|vendor| vendor.total_tokens)
        .sum::<i64>();
    if other_total > 0 {
        vendor_rows.push(json!({"name": RANKING_OTHERS_LABEL, "total": other_total, "share": ranking_share(other_total, vendors.iter().map(|vendor| vendor.total_tokens).sum())}));
    }

    let mut tokens = BTreeMap::<i64, BTreeMap<String, i64>>::new();
    let mut bucket_totals = BTreeMap::<i64, i64>::new();
    for row in buckets {
        let vendor = metadata
            .get(&row.model_name)
            .map(|meta| nonempty_vendor(&meta.vendor))
            .unwrap_or(RANKING_UNKNOWN_VENDOR);
        let vendor = if top_vendors.contains(vendor) {
            vendor.to_owned()
        } else {
            RANKING_OTHERS_LABEL.to_owned()
        };
        *tokens
            .entry(row.bucket)
            .or_default()
            .entry(vendor)
            .or_default() += row.tokens;
        *bucket_totals.entry(row.bucket).or_default() += row.tokens;
    }
    let points = tokens
        .iter()
        .flat_map(|(bucket, by_vendor)| {
            vendor_rows.iter().filter_map(|vendor| {
                let name = vendor.get("name")?.as_str()?;
                let token_count = *by_vendor.get(name).unwrap_or(&0);
                (token_count > 0).then(|| {
                    json!({
                        "ts": ranking_bucket_timestamp(*bucket),
                        "label": ranking_bucket_label(*bucket, period),
                        "vendor": name,
                        "share": ranking_share(token_count, *bucket_totals.get(bucket).unwrap_or(&0)),
                        "tokens": token_count,
                    })
                })
            })
        })
        .collect::<Vec<_>>();
    json!({"points": points, "vendors": vendor_rows, "buckets": tokens.len()})
}

fn ranking_movers(models: &[RankedModel]) -> (Vec<Value>, Vec<Value>) {
    let mut movers = Vec::new();
    let mut droppers = Vec::new();
    for model in models {
        let Some(previous_rank) = model.previous_rank else {
            continue;
        };
        let rank_delta = previous_rank as i64 - model.rank as i64;
        if rank_delta == 0 {
            continue;
        }
        let mut value = serde_json::Map::from_iter([
            ("model_name".to_owned(), json!(model.model_name)),
            ("vendor".to_owned(), json!(model.vendor)),
            ("rank_delta".to_owned(), json!(rank_delta)),
            ("current_rank".to_owned(), json!(model.rank)),
            ("growth_pct".to_owned(), json!(model.growth_pct)),
        ]);
        if !model.vendor_icon.is_empty() {
            value.insert("vendor_icon".to_owned(), json!(model.vendor_icon));
        }
        let value = Value::Object(value);
        if rank_delta > 0 {
            movers.push(value);
        } else {
            droppers.push(value);
        }
    }
    movers.sort_by(ranking_mover_order);
    droppers.sort_by(|left, right| ranking_mover_order(right, left));
    movers.truncate(RANKING_MOVER_LIMIT);
    droppers.truncate(RANKING_MOVER_LIMIT);
    (movers, droppers)
}

fn ranking_mover_order(left: &Value, right: &Value) -> std::cmp::Ordering {
    let left_delta = left
        .get("rank_delta")
        .and_then(Value::as_i64)
        .unwrap_or_default();
    let right_delta = right
        .get("rank_delta")
        .and_then(Value::as_i64)
        .unwrap_or_default();
    let left_growth = left
        .get("growth_pct")
        .and_then(Value::as_f64)
        .unwrap_or_default();
    let right_growth = right
        .get("growth_pct")
        .and_then(Value::as_f64)
        .unwrap_or_default();
    right_delta
        .cmp(&left_delta)
        .then_with(|| right_growth.total_cmp(&left_growth))
}

fn ranked_model_json(model: &RankedModel) -> Value {
    let mut value = serde_json::Map::from_iter([
        ("rank".to_owned(), json!(model.rank)),
        ("model_name".to_owned(), json!(model.model_name)),
        ("vendor".to_owned(), json!(model.vendor)),
        ("category".to_owned(), json!("all")),
        ("total_tokens".to_owned(), json!(model.total_tokens)),
        ("share".to_owned(), json!(model.share)),
        ("growth_pct".to_owned(), json!(model.growth_pct)),
    ]);
    if let Some(previous_rank) = model.previous_rank {
        value.insert("previous_rank".to_owned(), json!(previous_rank));
    }
    if !model.vendor_icon.is_empty() {
        value.insert("vendor_icon".to_owned(), json!(model.vendor_icon));
    }
    Value::Object(value)
}

fn ranked_vendor_json(vendor: &RankedVendor) -> Value {
    let mut value = serde_json::Map::from_iter([
        ("rank".to_owned(), json!(vendor.rank)),
        ("vendor".to_owned(), json!(vendor.vendor)),
        ("total_tokens".to_owned(), json!(vendor.total_tokens)),
        ("share".to_owned(), json!(vendor.share)),
        ("growth_pct".to_owned(), json!(vendor.growth_pct)),
        ("models_count".to_owned(), json!(vendor.models.len())),
        ("top_model".to_owned(), json!(vendor.top_model)),
    ]);
    if !vendor.vendor_icon.is_empty() {
        value.insert("vendor_icon".to_owned(), json!(vendor.vendor_icon));
    }
    Value::Object(value)
}

fn nonempty_vendor(vendor: &str) -> &str {
    if vendor.is_empty() {
        RANKING_UNKNOWN_VENDOR
    } else {
        vendor
    }
}

fn ranking_bucket_timestamp(bucket: i64) -> String {
    Utc.timestamp_opt(bucket, 0)
        .single()
        .map(|timestamp| timestamp.to_rfc3339_opts(SecondsFormat::Secs, true))
        .unwrap_or_default()
}

fn ranking_bucket_label(bucket: i64, period: RankingPeriod) -> String {
    let Some(timestamp) = Local.timestamp_opt(bucket, 0).single() else {
        return String::new();
    };
    match period.label {
        RankingLabel::Hour => timestamp.format("%H:%M").to_string(),
        RankingLabel::Day => timestamp.format("%b %-d").to_string(),
    }
}

fn ranking_growth(current: i64, previous: i64) -> f64 {
    if previous <= 0 {
        return if current > 0 { 100.0 } else { 0.0 };
    }
    round_ranking_float((current - previous) as f64 / previous as f64 * 100.0)
}

fn round_ranking_float(value: f64) -> f64 {
    (value * 10_000.0).round() / 10_000.0
}

fn ranking_share(value: i64, total: i64) -> f64 {
    if value <= 0 || total <= 0 {
        return 0.0;
    }
    round_ranking_float(value as f64 / total as f64)
}

#[derive(Clone)]
struct PgTestStatusProbe {
    pg: PgPool,
}

impl PgTestStatusProbe {
    fn new(pg: PgPool) -> Self {
        Self { pg }
    }
}

#[async_trait]
impl ControlTaskStatusProbe for PgTestStatusProbe {
    async fn test_status(&self) -> Result<Value, ControlTaskStatusError> {
        sqlx::query_scalar::<_, i32>("SELECT 1")
            .fetch_one(&self.pg)
            .await
            .map_err(|_| ControlTaskStatusError::DatabaseUnavailable)?;
        // The Go endpoint reports listener-owned counters.  This test-only
        // composition has no such collector, so it must not fabricate zeros.
        Err(ControlTaskStatusError::HttpStatsUnavailable)
    }
}

struct DenyRelayVideo;
#[async_trait]
impl RelayVideoService for DenyRelayVideo {
    async fn authorize(&self, _: &Request) -> RelayVideoAuthorization {
        RelayVideoAuthorization::Rejected {
            status: StatusCode::UNAUTHORIZED,
            message: "Invalid token".to_owned(),
        }
    }
    async fn relay(&self, _: RelayVideoOperation, _: Request) -> Response {
        StatusCode::SERVICE_UNAVAILABLE.into_response()
    }
}

struct DenyRelayMisc;
#[async_trait]
impl MissingRelayService for DenyRelayMisc {
    async fn authorize(
        &self,
        endpoint: MissingRelayEndpoint,
        _: &Request,
    ) -> MissingRelayAuthorization {
        MissingRelayAuthorization::Rejected(MissingRelayAuthRejection {
            status: StatusCode::UNAUTHORIZED,
            code: "AUTH_UNAUTHORIZED",
            message: match endpoint {
                MissingRelayEndpoint::Realtime | MissingRelayEndpoint::Edits => "Invalid token",
                MissingRelayEndpoint::PgChatCompletions => "Unauthorized, invalid access token",
                MissingRelayEndpoint::PgImagesGenerations | MissingRelayEndpoint::PgImagesEdits => {
                    "Invalid token"
                }
            }
            .to_owned(),
        })
    }
    async fn relay(&self, _: MissingRelayEndpoint, _: Request) -> Response {
        StatusCode::SERVICE_UNAVAILABLE.into_response()
    }
}

/// Fail-closed owner for the otherwise-unmounted legacy relay miscellany.
///
/// A single fixture credential reaches the relay boundary so route tests can
/// distinguish a real, provider-unavailable seam (503) from authentication
/// rejection.  No channel, URL, or HTTP client is retained here.
struct DenyRelayMiscV1;

#[async_trait]
impl RelayMiscService for DenyRelayMiscV1 {
    async fn system_performance(&self, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn authorize(&self, _: &Request) -> RelayAuth {
        // The enclosing test-only middleware has already accepted exactly the
        // fixture credential before this async boundary receives a request.
        RelayAuth::Authorized
    }

    async fn model_rate_limit(&self, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn distribute(&self, _: &RelayRequestContext, _: &Request) -> RelayAuth {
        RelayAuth::Authorized
    }

    async fn relay(&self, _: RelayMiscProtocol, _: Request) -> Response {
        StatusCode::SERVICE_UNAVAILABLE.into_response()
    }
}

async fn enforce_test_instance_relay_misc_auth(request: Request, next: Next) -> Response {
    let authorized = request
        .headers()
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .is_some_and(|value| value == "Bearer lmm-test-relay-fixture");
    if authorized {
        next.run(request).await
    } else {
        StatusCode::UNAUTHORIZED.into_response()
    }
}

fn relay_misc_candidate_router() -> Router {
    relay_misc_routes(RelayMiscHttpState::new(Arc::new(DenyRelayMiscV1))).layer(
        axum::middleware::from_fn(enforce_test_instance_relay_misc_auth),
    )
}

/// Test-only adapter for the Anthropic/Gemini candidate routes.
///
/// The fixture token permits exercising the authenticated legacy DELETE
/// endpoint. Every relay POST authenticates first, then receives `NoChannel`
/// before an upstream request can be constructed; this instance never holds a
/// provider URL, credential, or HTTP client.
struct TestInstanceRelayBackend;

#[async_trait]
impl RelayBackend for TestInstanceRelayBackend {
    async fn authenticate(&self, token: &str) -> Result<RelayIdentity, RelayFailure> {
        (token == "lmm-test-relay-fixture")
            .then(|| RelayIdentity {
                token_id: "test-relay-fixture".to_owned(),
            })
            .ok_or(RelayFailure::Unauthorized)
    }

    async fn select_channel(
        &self,
        _: &RelayIdentity,
        _: RelayProtocol,
        _: &str,
    ) -> Result<RelayChannel, RelayFailure> {
        Err(RelayFailure::NoChannel)
    }

    async fn invoke(
        &self,
        _: &RelayChannel,
        _: UpstreamRequest,
    ) -> Result<UpstreamReply, RelayFailure> {
        Err(RelayFailure::Upstream)
    }

    async fn record_outcome(
        &self,
        _: Option<&RelayIdentity>,
        _: Option<&RelayChannel>,
        _: RelayOutcome,
    ) {
    }
}

/// A test-instance-only Midjourney boundary.
///
/// Authentication intentionally delegates to the PostgreSQL implementation.
/// Everything that could select or contact an upstream fails closed before a
/// request is built. The loopback-only channel is defensive future-proofing:
/// if an explicit local mock is introduced, it must remain on loopback; it is
/// not used by this deny adapter today.
struct TestInstanceMidjourneyBackend {
    authentication: PgMidjourneyBackend,
}

impl TestInstanceMidjourneyBackend {
    fn new(pg: PgPool) -> Self {
        let client = reqwest::Client::builder()
            .no_proxy()
            .build()
            .unwrap_or_default();
        Self {
            authentication: PgMidjourneyBackend::new(
                pg,
                client,
                MidjourneyChannel {
                    id: 0,
                    base_url: "http://127.0.0.1:9/".to_owned(),
                    api_key: String::new(),
                    quota: 0,
                },
                std::time::Duration::from_secs(1),
                1024,
            ),
        }
    }
}

#[async_trait]
impl MidjourneyBackend for TestInstanceMidjourneyBackend {
    async fn authenticate(
        &self,
        headers: &HeaderMap,
        client_ip: Option<std::net::IpAddr>,
    ) -> Result<MidjourneyIdentity, MidjourneyFailure> {
        self.authentication.authenticate(headers, client_ip).await
    }

    async fn submit(
        &self,
        _: &MidjourneyIdentity,
        _: &str,
        _: &str,
        _: &HeaderMap,
        _: serde_json::Value,
    ) -> Result<SubmitReply, MidjourneyFailure> {
        Err(MidjourneyFailure::Upstream)
    }

    async fn record_submit(
        &self,
        _: &MidjourneyIdentity,
        _: TaskEffect,
    ) -> Result<(), MidjourneyFailure> {
        Err(MidjourneyFailure::Upstream)
    }

    async fn task_read(
        &self,
        _: &MidjourneyIdentity,
        _: &str,
        _: &str,
        _: &HeaderMap,
        _: Option<serde_json::Value>,
    ) -> Result<BufferedJsonReply, MidjourneyFailure> {
        Err(MidjourneyFailure::Upstream)
    }

    async fn image_for(&self, _: &str) -> Result<StoredImage, MidjourneyFailure> {
        Err(MidjourneyFailure::NotFound)
    }

    async fn fetch_image(&self, _: &str) -> Result<ImageReply, MidjourneyFailure> {
        Err(MidjourneyFailure::BlockedImage)
    }
}

/// Test instances must never call remote catalog metadata sources.
struct DenyCatalogUpstream;

#[async_trait]
impl CatalogUpstream for DenyCatalogUpstream {
    async fn fetch(&self, _: &str) -> Result<UpstreamCatalog, CatalogError> {
        Err(CatalogError::Unavailable)
    }
}

/// Blocks every advanced-channel provider operation before a socket can open.
struct DenyChannelAdvancedUpstream;

#[async_trait]
impl ChannelAdvancedUpstream for DenyChannelAdvancedUpstream {
    async fn execute(
        &self,
        _: ChannelAdvancedCall,
        _: Option<ChannelAdvancedChannel>,
    ) -> Result<Value, ChannelAdvancedError> {
        Err(ChannelAdvancedError::Provider)
    }
}

/// Test instances do not resolve operator-supplied OIDC discovery URLs.
struct DenyOAuthDiscovery;

#[async_trait]
impl OAuthDiscoveryClient for DenyOAuthDiscovery {
    async fn discover(&self, _: &str) -> Result<Value, String> {
        Err("test instance disables remote OAuth discovery".to_owned())
    }
}

struct DenySessionRotator;
#[async_trait]
impl SecuritySessionRotator for DenySessionRotator {
    async fn rotate_after_security_change(
        &self,
        _: Identity2FAActor,
        _: &Identity2FASession,
        _: &'static str,
        _: i64,
    ) -> Result<SecuritySessionRotation, String> {
        Err("test instance does not rotate sessions".to_owned())
    }
}

struct DenyFederationIdentity;
#[async_trait]
impl FederationIdentity for DenyFederationIdentity {
    async fn principal(&self, _: &HeaderMap) -> Result<FederationPrincipal, FederationError> {
        Err(FederationError::Unauthorized)
    }
    async fn verify_email_code(&self, _: &str, _: &str) -> Result<bool, FederationError> {
        Err(FederationError::Unauthorized)
    }
}

struct DenyRelayMedia;
#[async_trait]
impl RelayMediaService for DenyRelayMedia {
    async fn relay(&self, _: Request) -> Response {
        StatusCode::UNAUTHORIZED.into_response()
    }
}

struct DenyOpenAiRelay;
#[async_trait]
impl OpenAiRelayService for DenyOpenAiRelay {
    async fn authenticate(&self, _: OpenAiRelayAuthorization) -> Result<(), OpenAiRelayFailure> {
        Err(OpenAiRelayFailure::new(
            StatusCode::UNAUTHORIZED,
            "",
            "Unauthorized",
        ))
    }
    async fn relay(&self, _: OpenAiRelayRequest) -> Result<OpenAiRelayResult, OpenAiRelayFailure> {
        Err(OpenAiRelayFailure::new(
            StatusCode::SERVICE_UNAVAILABLE,
            "",
            "relay unavailable",
        ))
    }
}

/// Test instances must never call an operator-configured Uptime Kuma URL.
struct DenyUptimeKuma;

#[async_trait]
impl UptimeKumaClient for DenyUptimeKuma {
    async fn status_page(&self, _: &str) -> Result<UptimeStatusPage, ControlPublicError> {
        Err(ControlPublicError)
    }

    async fn heartbeat_page(&self, _: &str) -> Result<UptimeHeartbeatPage, ControlPublicError> {
        Err(ControlPublicError)
    }
}

/// The isolated test instance must not contact the upstream repository while
/// serving system-config routes.
struct DenyProjectUpdate;

#[async_trait]
impl ProjectUpdateClient for DenyProjectUpdate {
    async fn latest_main_commit(&self) -> Result<Value, ()> {
        Err(())
    }
}

#[cfg(test)]
mod tests {
    use std::{collections::BTreeMap, env, sync::Arc};

    use axum::{
        body::Body,
        http::{Request, StatusCode},
    };
    use chrono::Utc;
    use lmm_api_rs::auth::{AuthConfig, DashboardAuth, PgValkeyDashboardAuth};
    use lmm_api_rs::migration_routes::{
        admin_catalog::CatalogUpstream,
        control_public::UptimeKumaClient,
        media_midjourney::{
            MidjourneyBackend, MidjourneyFailure, MidjourneyHttpState,
            media_midjourney_dynamic_router,
        },
        media_tasks::{MediaTaskHttpState, MidjourneyMediaTaskService, media_task_router},
        relay_anthropic_gemini::{RelayHttpState, router as relay_anthropic_gemini_router},
        system_config::{ProjectUpdateClient, SystemConfigRuntimeWriter},
    };
    use secrecy::SecretString;
    use serde_json::json;
    use sqlx::{PgPool, postgres::PgPoolOptions};
    use std::time::Duration;
    use tower::ServiceExt;
    use uuid::Uuid;

    use super::{
        DenyCatalogUpstream, DenyProjectUpdate, DenyUptimeKuma, LEGACY_PRICING_FIRST_MODEL_VERSION,
        LEGACY_PRICING_RESPONSE_VERSION, PgMissingControlStore, PricingAbility,
        PricingModelMetadata, PricingVendor, TestInstanceMidjourneyBackend,
        TestInstanceRelayBackend, TestInstanceSetupRuntimeWriter, build_pricing_snapshot,
        frozen_dashboard_models, relay_misc_candidate_router, safe_candidate_surface,
    };
    use lmm_api_rs::migration_routes::missing_control_public::MissingControlStore;

    fn config_change(key: &str, value: &str) -> (String, String) {
        (key.to_owned(), value.to_owned())
    }

    #[tokio::test]
    async fn setup_runtime_writer_accepts_exact_boolean_pair_in_either_order() {
        let writer = TestInstanceSetupRuntimeWriter;
        for self_use_mode in ["true", "false"] {
            for demo_site in ["true", "false"] {
                for changes in [
                    vec![
                        config_change("SelfUseModeEnabled", self_use_mode),
                        config_change("DemoSiteEnabled", demo_site),
                    ],
                    vec![
                        config_change("DemoSiteEnabled", demo_site),
                        config_change("SelfUseModeEnabled", self_use_mode),
                    ],
                ] {
                    assert_eq!(writer.preflight(&changes).await, Ok(()));
                    assert_eq!(writer.apply_committed(&changes).await, Ok(()));
                }
            }
        }
    }

    #[tokio::test]
    async fn setup_runtime_writer_rejects_every_other_batch_shape() {
        let writer = TestInstanceSetupRuntimeWriter;
        let rejected = [
            vec![],
            vec![config_change("SelfUseModeEnabled", "true")],
            vec![
                config_change("SelfUseModeEnabled", "true"),
                config_change("SelfUseModeEnabled", "false"),
            ],
            vec![
                config_change("SelfUseModeEnabled", "true"),
                config_change("UnknownOption", "false"),
            ],
            vec![
                config_change("SelfUseModeEnabled", "true"),
                config_change("DemoSiteEnabled", "false"),
                config_change("UnknownOption", "true"),
            ],
            vec![
                config_change("SelfUseModeEnabled", " true"),
                config_change("DemoSiteEnabled", "false"),
            ],
            vec![
                config_change("SelfUseModeEnabled", "true"),
                config_change("DemoSiteEnabled", "1"),
            ],
        ];

        for changes in rejected {
            assert_eq!(writer.preflight(&changes).await, Err(()));
            assert_eq!(writer.apply_committed(&changes).await, Err(()));
        }
    }

    #[test]
    fn dashboard_models_use_the_frozen_go_catalogue_shape() {
        let models = frozen_dashboard_models();
        let object = models.as_object().expect("channel-type map");
        let advanced = object
            .get("58")
            .and_then(serde_json::Value::as_array)
            .expect("advanced custom channel list");
        assert!(!advanced.is_empty());
        assert!(advanced.iter().all(|model| model.is_string()));
        assert_eq!(
            object.get("46"),
            Some(&json!([
                "ernie-4.0-8k-latest",
                "ernie-4.0-8k-preview",
                "ernie-4.0-8k",
                "ernie-4.0-turbo-8k-latest",
                "ernie-4.0-turbo-8k-preview",
                "ernie-4.0-turbo-8k",
                "ernie-4.0-turbo-128k",
                "ernie-3.5-8k-preview",
                "ernie-3.5-8k",
                "ernie-3.5-128k",
                "ernie-speed-8k",
                "ernie-speed-128k",
                "ernie-speed-pro-128k",
                "ernie-lite-8k",
                "ernie-lite-pro-128k",
                "ernie-tiny-8k",
                "ernie-char-8k",
                "ernie-char-fiction-8k",
                "ernie-novel-8k",
                "deepseek-v3",
                "deepseek-r1",
                "deepseek-r1-distill-qwen-32b",
                "deepseek-r1-distill-qwen-14b"
            ]))
        );
        assert_ne!(object.get("46"), object.get("45"));
        assert!(!object.contains_key("999"));
    }

    #[tokio::test]
    #[ignore = "requires an isolated PostgreSQL database; set LMM_CONTROL_PUBLIC_TEST_DATABASE_URL"]
    async fn token_auth_pg_lookup_requires_an_active_owner_and_carries_owner_context() {
        let database_url = env::var("LMM_CONTROL_PUBLIC_TEST_DATABASE_URL").expect(
            "LMM_CONTROL_PUBLIC_TEST_DATABASE_URL is required for the isolated PostgreSQL harness",
        );
        let pool = PgPoolOptions::new()
            .max_connections(2)
            .connect(&database_url)
            .await
            .expect("isolated PostgreSQL must be reachable");
        let suffix = Uuid::new_v4().simple().to_string();
        let username = format!("control-token-owner-{suffix}");
        let key = format!("control-token-{suffix}");
        let user_id: i64 = sqlx::query_scalar(
            "INSERT INTO users (username, password, status, setting) \
             VALUES ($1, 'unused-password', 1, '{\"language\":\"zh-TW\"}') RETURNING id",
        )
        .bind(&username)
        .fetch_one(&pool)
        .await
        .expect("owner fixture");
        sqlx::query(
            "INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, \
             expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, \
             allow_ips, used_quota, \"group\", cross_group_retry) \
             VALUES ($1, $2, 1, 'fixture', 1, 1, -1, 0, FALSE, FALSE, '', '', 0, 'default', FALSE)",
        )
        .bind(user_id)
        .bind(&key)
        .execute(&pool)
        .await
        .expect("token fixture");

        let store = PgMissingControlStore::new(pool.clone());
        let prefix = key.split('-').next().expect("token prefix");
        let active = store
            .token_auth_read_only(prefix)
            .await
            .expect("active owner lookup")
            .expect("token");
        assert_eq!(active.user_id, user_id);
        assert_eq!(active.user_status, 1);
        assert_eq!(active.saved_language.as_deref(), Some("zh-TW"));
        assert!(
            store
                .token_usage_for_owner(&key, user_id + 1)
                .await
                .expect("owner-bound usage lookup")
                .is_none()
        );

        sqlx::query("UPDATE users SET status = 2 WHERE id = $1")
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("disable owner");
        let disabled = store
            .token_auth_read_only(&key)
            .await
            .expect("disabled owner lookup")
            .expect("token");
        assert_eq!(disabled.user_status, 2);

        sqlx::query("UPDATE users SET deleted_at = NOW() WHERE id = $1")
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("soft delete owner");
        let missing_owner = store
            .token_auth_read_only(&key)
            .await
            .expect_err("soft-deleted owner must fail closed");
        assert!(missing_owner.0.contains(&user_id.to_string()));

        sqlx::query("DELETE FROM tokens WHERE key = $1")
            .bind(&key)
            .execute(&pool)
            .await
            .expect("remove token fixture");
        sqlx::query("DELETE FROM users WHERE id = $1")
            .bind(user_id)
            .execute(&pool)
            .await
            .expect("remove owner fixture");
    }

    #[tokio::test]
    async fn outbound_adapters_fail_closed_without_a_network_client() {
        assert!(DenyCatalogUpstream.fetch("en").await.is_err());
        assert!(
            DenyUptimeKuma
                .status_page("https://example.invalid")
                .await
                .is_err()
        );
        assert!(
            DenyUptimeKuma
                .heartbeat_page("https://example.invalid")
                .await
                .is_err()
        );
    }

    #[test]
    fn pricing_snapshot_should_rebuild_the_public_go_contract_from_seeded_authorities() {
        let options = BTreeMap::from([
            ("GroupRatio".to_owned(), json!({"default": 1, "vip": 2})),
            (
                "UserUsableGroups".to_owned(),
                json!({"default": "Default", "vip": "VIP"}),
            ),
            (
                "AutoGroups".to_owned(),
                json!(["vip", "default", "missing"]),
            ),
            (
                "ModelRatio".to_owned(),
                json!({"gpt-pricing-fixture": 1.25, "claude-pricing-fixture": 2}),
            ),
            (
                "CompletionRatio".to_owned(),
                json!({"gpt-pricing-fixture": 3}),
            ),
            ("CacheRatio".to_owned(), json!({"gpt-pricing-fixture": 0.5})),
            (
                "billing_setting.billing_mode".to_owned(),
                json!({"gpt-pricing-fixture": "tiered_expr"}),
            ),
            (
                "billing_setting.billing_expr".to_owned(),
                json!({"gpt-pricing-fixture": "input * 1.25"}),
            ),
        ]);
        let snapshot = build_pricing_snapshot(
            vec![
                PricingAbility {
                    model_name: "gpt-pricing-fixture".to_owned(),
                    group: "default".to_owned(),
                    channel_type: 1,
                },
                PricingAbility {
                    model_name: "claude-pricing-fixture".to_owned(),
                    group: "vip".to_owned(),
                    channel_type: 14,
                },
            ],
            vec![
                PricingModelMetadata {
                    model_name: "gpt-pricing-fixture".to_owned(),
                    description: "fixture description".to_owned(),
                    icon: "OpenAI".to_owned(),
                    tags: "chat".to_owned(),
                    vendor_id: 7,
                    endpoints: r#"{"custom-chat":{"path":"/custom/chat","method":"put"}}"#
                        .to_owned(),
                    status: 1,
                    name_rule: 0,
                },
                PricingModelMetadata {
                    model_name: "claude-".to_owned(),
                    description: String::new(),
                    icon: String::new(),
                    tags: String::new(),
                    vendor_id: 0,
                    endpoints: String::new(),
                    status: 1,
                    name_rule: 1,
                },
            ],
            vec![PricingVendor {
                id: 7,
                name: "OpenAI".to_owned(),
                description: "provider".to_owned(),
                icon: "OpenAI".to_owned(),
            }],
            &options,
            None,
        );

        assert_eq!(
            snapshot,
            json!({
                "success": true,
                "data": [
                    {
                        "model_name": "claude-pricing-fixture",
                        "quota_type": 0,
                        "model_ratio": 2,
                        "model_price": 0,
                        "owner_by": "",
                        "completion_ratio": 1,
                        "enable_groups": ["vip"],
                        "supported_endpoint_types": ["anthropic", "openai"],
                        "pricing_version": LEGACY_PRICING_FIRST_MODEL_VERSION,
                    },
                    {
                        "model_name": "gpt-pricing-fixture",
                        "description": "fixture description",
                        "icon": "OpenAI",
                        "tags": "chat",
                        "vendor_id": 7,
                        "quota_type": 0,
                        "model_ratio": 1.25,
                        "model_price": 0,
                        "owner_by": "",
                        "completion_ratio": 3,
                        "cache_ratio": 0.5,
                        "billing_mode": "tiered_expr",
                        "billing_expr": "input * 1.25",
                        "enable_groups": ["default"],
                        "supported_endpoint_types": ["openai", "custom-chat"],
                    },
                ],
                "vendors": [{"id": 7, "name": "OpenAI", "description": "provider", "icon": "OpenAI"}],
                "group_ratio": {"default": 1, "vip": 2},
                "usable_group": {"default": "Default", "vip": "VIP"},
                "supported_endpoint": {
                    "anthropic": {"path": "/v1/messages", "method": "POST"},
                    "openai": {"path": "/v1/chat/completions", "method": "POST"},
                    "custom-chat": {"path": "/custom/chat", "method": "PUT"},
                },
                "auto_groups": ["vip", "default"],
                "pricing_version": LEGACY_PRICING_RESPONSE_VERSION,
            })
        );
    }

    #[test]
    fn pricing_snapshot_should_filter_rows_apply_group_override_and_filter_auto_groups_for_actor() {
        let options = BTreeMap::from([
            ("GroupRatio".to_owned(), json!({"default": 1, "vip": 2})),
            (
                "GroupGroupRatio".to_owned(),
                json!({"member": {"default": 0.5}}),
            ),
            ("UserUsableGroups".to_owned(), json!({"default": "Default"})),
            ("AutoGroups".to_owned(), json!(["vip", "default", "member"])),
        ]);
        let snapshot = build_pricing_snapshot(
            vec![
                PricingAbility {
                    model_name: "allowed".to_owned(),
                    group: "default".to_owned(),
                    channel_type: 1,
                },
                PricingAbility {
                    model_name: "hidden".to_owned(),
                    group: "vip".to_owned(),
                    channel_type: 1,
                },
            ],
            Vec::new(),
            Vec::new(),
            &options,
            Some("member"),
        );

        assert_eq!(
            snapshot,
            json!({
                "success": true,
                "data": [{
                    "model_name": "allowed",
                    "quota_type": 0,
                    "model_ratio": 37.5,
                    "model_price": 0,
                    "owner_by": "",
                    "completion_ratio": 1,
                    "enable_groups": ["default"],
                    "supported_endpoint_types": ["openai"],
                    "pricing_version": LEGACY_PRICING_FIRST_MODEL_VERSION,
                }],
                "vendors": [],
                "group_ratio": {"default": 0.5},
                "usable_group": {"default": "Default", "member": "用户分组"},
                "supported_endpoint": {"openai": {"path": "/v1/chat/completions", "method": "POST"}},
                "auto_groups": ["default", "member"],
                "pricing_version": LEGACY_PRICING_RESPONSE_VERSION,
            })
        );
    }

    #[test]
    fn pricing_snapshot_should_keep_go_empty_configuration_defaults_without_inventing_models() {
        let snapshot =
            build_pricing_snapshot(Vec::new(), Vec::new(), Vec::new(), &BTreeMap::new(), None);

        assert_eq!(
            snapshot,
            json!({
                "success": true,
                "data": [],
                "vendors": [],
                "group_ratio": {},
                "usable_group": {},
                "supported_endpoint": {},
                "auto_groups": ["default"],
                "pricing_version": LEGACY_PRICING_RESPONSE_VERSION,
            })
        );
    }

    fn media_backend() -> Arc<TestInstanceMidjourneyBackend> {
        Arc::new(TestInstanceMidjourneyBackend::new(
            PgPool::connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
                .expect("a lazy PostgreSQL URL is valid"),
        ))
    }

    #[tokio::test]
    async fn media_candidate_routes_are_reachable_and_reject_unauthenticated_requests() {
        let backend = media_backend();
        let dynamic_backend: Arc<dyn MidjourneyBackend> = backend.clone();
        // Building the combined surface also detects duplicate Axum route
        // registrations before a test instance can start.
        let app = media_midjourney_dynamic_router(MidjourneyHttpState::new(dynamic_backend)).merge(
            media_task_router(MediaTaskHttpState::new(Arc::new(
                MidjourneyMediaTaskService::new(backend),
            ))),
        );

        let dynamic = app
            .clone()
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/proxy/mj/submit/imagine")
                    .body(Body::from(r#"{"prompt":"test"}"#))
                    .expect("dynamic request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(dynamic.status(), StatusCode::UNAUTHORIZED);

        let static_route = app
            .oneshot(
                Request::builder()
                    .method("POST")
                    .uri("/mj/submit/imagine")
                    .body(Body::from(r#"{"prompt":"test"}"#))
                    .expect("static request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(static_route.status(), StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    async fn relay_misc_routes_are_auth_gated_fail_closed_and_do_not_shadow_model_delete() {
        let app = relay_misc_candidate_router().merge(relay_anthropic_gemini_router(
            RelayHttpState::new(Arc::new(TestInstanceRelayBackend)),
        ));

        let anonymous = app
            .clone()
            .oneshot(
                Request::post("/v1/alpha/search")
                    .body(Body::from("{}"))
                    .expect("relay request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(anonymous.status(), StatusCode::UNAUTHORIZED);

        let unavailable = app
            .clone()
            .oneshot(
                Request::post("/v1/alpha/search")
                    .header("authorization", "Bearer lmm-test-relay-fixture")
                    .body(Body::from("{}"))
                    .expect("fixture relay request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(unavailable.status(), StatusCode::SERVICE_UNAVAILABLE);

        // Frozen Go routes owned by RelayNotImplemented return 501 only after
        // the fixture credential passes every relay gate. They never select an
        // upstream or enter the fail-closed provider adapter.
        let frozen = app
            .clone()
            .oneshot(
                Request::get("/v1/files")
                    .header("authorization", "Bearer lmm-test-relay-fixture")
                    .body(Body::empty())
                    .expect("frozen request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(frozen.status(), StatusCode::NOT_IMPLEMENTED);

        let model_delete = app
            .oneshot(
                Request::delete("/v1/models/model-a")
                    .header("authorization", "Bearer lmm-test-relay-fixture")
                    .body(Body::empty())
                    .expect("model deletion request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(model_delete.status(), StatusCode::NOT_IMPLEMENTED);
    }

    #[tokio::test]
    async fn setup_route_is_mounted_without_enabling_remote_dependencies() {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
            .expect("a lazy PostgreSQL URL is valid");
        let valkey =
            redis::Client::open("redis://127.0.0.1:1/").expect("a lazy Valkey URL is valid");
        let auth: Arc<dyn DashboardAuth> = Arc::new(
            PgValkeyDashboardAuth::new(
                pg.clone(),
                valkey.clone(),
                AuthConfig {
                    session_secret: SecretString::from("TestR5!session-secret-with-entropy-123456"),
                    ..AuthConfig::default()
                },
            )
            .expect("test auth config is valid"),
        );
        let response = safe_candidate_surface(pg, valkey, auth)
            .oneshot(
                Request::get("/api/setup")
                    .body(Body::empty())
                    .expect("request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_ne!(response.status(), StatusCode::NOT_FOUND);
        assert!(DenyProjectUpdate.latest_main_commit().await.is_err());
    }

    #[tokio::test]
    async fn complete_test_surface_mounts_observability_routes_before_authentication() {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
            .expect("a lazy PostgreSQL URL is valid");
        let valkey =
            redis::Client::open("redis://127.0.0.1:1/").expect("a lazy Valkey URL is valid");
        let auth: Arc<dyn DashboardAuth> = Arc::new(
            PgValkeyDashboardAuth::new(
                pg.clone(),
                valkey.clone(),
                AuthConfig {
                    session_secret: SecretString::from("TestR5!session-secret-with-entropy-123456"),
                    ..AuthConfig::default()
                },
            )
            .expect("test auth config is valid"),
        );
        let app = safe_candidate_surface(pg, valkey, auth);
        for path in ["/api/data/self", "/api/perf-metrics/summary"] {
            let response = app
                .clone()
                .oneshot(
                    Request::get(path)
                        .body(Body::empty())
                        .expect("request is valid"),
                )
                .await
                .expect("router is infallible");
            assert_ne!(response.status(), StatusCode::NOT_FOUND, "{path}");
        }
    }

    #[tokio::test]
    async fn complete_test_surface_mounts_topup_read_routes_before_authentication() {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
            .expect("a lazy PostgreSQL URL is valid");
        let valkey =
            redis::Client::open("redis://127.0.0.1:1/").expect("a lazy Valkey URL is valid");
        let auth: Arc<dyn DashboardAuth> = Arc::new(
            PgValkeyDashboardAuth::new(
                pg.clone(),
                valkey.clone(),
                AuthConfig {
                    session_secret: SecretString::from("TestR5!session-secret-with-entropy-123456"),
                    ..AuthConfig::default()
                },
            )
            .expect("test auth config is valid"),
        );
        for path in ["/api/user/topup/info", "/api/user/topup/self"] {
            let response = safe_candidate_surface(pg.clone(), valkey.clone(), Arc::clone(&auth))
                .oneshot(
                    Request::get(path)
                        .body(Body::empty())
                        .expect("request is valid"),
                )
                .await
                .expect("router is infallible");
            assert_eq!(response.status(), StatusCode::UNAUTHORIZED, "{path}");
        }
    }

    #[tokio::test]
    async fn complete_test_surface_mounts_checkin_read_route_before_authentication() {
        let pg = PgPoolOptions::new()
            .acquire_timeout(Duration::from_millis(10))
            .connect_lazy("postgres://unused:unused@127.0.0.1:1/unused")
            .expect("a lazy PostgreSQL URL is valid");
        let valkey =
            redis::Client::open("redis://127.0.0.1:1/").expect("a lazy Valkey URL is valid");
        let auth: Arc<dyn DashboardAuth> = Arc::new(
            PgValkeyDashboardAuth::new(
                pg.clone(),
                valkey.clone(),
                AuthConfig {
                    session_secret: SecretString::from("TestR5!session-secret-with-entropy-123456"),
                    ..AuthConfig::default()
                },
            )
            .expect("test auth config is valid"),
        );
        let response = safe_candidate_surface(pg, valkey, auth)
            .oneshot(
                Request::get("/api/user/checkin?month=2026-08")
                    .body(Body::empty())
                    .expect("request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    }

    #[tokio::test]
    #[ignore = "requires an isolated PostgreSQL database; set LMM_RANKINGS_TEST_DATABASE_URL"]
    async fn rankings_pg_snapshot_keeps_history_previous_rank_and_vendor_metadata() {
        let database_url = env::var("LMM_RANKINGS_TEST_DATABASE_URL").expect(
            "LMM_RANKINGS_TEST_DATABASE_URL is required for the isolated PostgreSQL harness",
        );
        let pool = PgPoolOptions::new()
            .max_connections(3)
            .connect(&database_url)
            .await
            .expect("isolated PostgreSQL must be reachable");
        let prefix = format!("rankings-parity-{}", Uuid::new_v4().simple());
        let models = [
            format!("{prefix}-alpha"),
            format!("{prefix}-beta"),
            format!("{prefix}-gamma"),
        ];
        let group = format!("{prefix}-group");
        let vendor_name = format!("{prefix}-vendor");
        let now = Utc::now().timestamp();

        let test = async {
            let vendor_id: i64 = sqlx::query_scalar(
                "INSERT INTO vendors (name, description, icon, status, created_time, updated_time) \
                 VALUES ($1, '', 'https://example.test/vendor.svg', 1, $2, $2) RETURNING id",
            )
            .bind(&vendor_name)
            .bind(now)
            .fetch_one(&pool)
            .await?;
            let channel_id: i64 = sqlx::query_scalar(
                "INSERT INTO channels (type, key, status, name, created_time) \
                 VALUES (1, $1, 1, $1, $2) RETURNING id",
            )
            .bind(&prefix)
            .bind(now)
            .fetch_one(&pool)
            .await?;
            for model in &models {
                sqlx::query(
                    "INSERT INTO models (model_name, vendor_id, status, name_rule, created_time, updated_time) \
                     VALUES ($1, $2, 1, 0, $3, $3)",
                )
                .bind(model.as_str())
                .bind(vendor_id)
                .bind(now)
                .execute(&pool)
                .await?;
                sqlx::query(
                    "INSERT INTO abilities (\"group\", model, channel_id, enabled) VALUES ($1, $2, $3, TRUE)",
                )
                .bind(&group)
                .bind(model.as_str())
                .bind(channel_id)
                .execute(&pool)
                .await?;
            }
            for (model, created_at, token_used) in [
                (&models[0], now - 900, 200_i64),
                (&models[1], now - 10_800, 100_i64),
                (&models[2], now - 2 * 86_400, 50_i64),
                (&models[0], now - 7 * 86_400 - 3_600, 10_i64),
                (&models[1], now - 7 * 86_400 - 3_600, 300_i64),
            ] {
                sqlx::query(
                    "INSERT INTO quota_data (model_name, created_at, token_used) VALUES ($1, $2, $3)",
                )
                .bind(model.as_str())
                .bind(created_at)
                .bind(token_used)
                .execute(&pool)
                .await?;
            }

            let snapshot = PgMissingControlStore::new(pool.clone())
                .rankings("week")
                .await
                .map_err(|_| sqlx::Error::Protocol("rankings store unavailable".to_owned()))?;
            let rows = snapshot["models"].as_array().expect("models array");
            let alpha = rows
                .iter()
                .find(|row| row["model_name"] == models[0])
                .expect("current alpha model");
            assert_eq!(alpha["rank"], 1);
            assert_eq!(alpha["previous_rank"], 2);
            assert_eq!(alpha["vendor"], vendor_name);
            assert_eq!(alpha["vendor_icon"], "https://example.test/vendor.svg");
            assert_eq!(snapshot["top_movers"][0]["model_name"], models[0]);
            assert_eq!(snapshot["top_droppers"][0]["model_name"], models[1]);
            assert!(snapshot["models_history"]["buckets"].as_u64().unwrap_or_default() >= 2);
            assert!(snapshot["vendor_share_history"]["points"]
                .as_array()
                .is_some_and(|points| !points.is_empty()));
            Ok::<(), sqlx::Error>(())
        }
        .await;

        sqlx::query("DELETE FROM quota_data WHERE model_name = ANY($1)")
            .bind(models.to_vec())
            .execute(&pool)
            .await
            .expect("remove quota fixture");
        sqlx::query("DELETE FROM abilities WHERE \"group\" = $1")
            .bind(&group)
            .execute(&pool)
            .await
            .expect("remove ability fixture");
        sqlx::query("DELETE FROM models WHERE model_name = ANY($1)")
            .bind(models.to_vec())
            .execute(&pool)
            .await
            .expect("remove model fixture");
        sqlx::query("DELETE FROM channels WHERE key = $1")
            .bind(&prefix)
            .execute(&pool)
            .await
            .expect("remove channel fixture");
        sqlx::query("DELETE FROM vendors WHERE name = $1")
            .bind(&vendor_name)
            .execute(&pool)
            .await
            .expect("remove vendor fixture");
        test.expect("ranking snapshot should preserve the frozen Go contract");
    }

    #[tokio::test]
    async fn media_candidate_deny_adapter_never_attempts_upstream_egress() {
        let backend = media_backend();
        let identity = lmm_api_rs::migration_routes::media_midjourney::MidjourneyIdentity {
            user_id: 1,
            token_id: "1".to_owned(),
        };

        // These calls return before constructing a provider request. The
        // deferred PostgreSQL pool and loopback-only placeholder channel make
        // an accidental remote egress observable as a failing test.
        assert!(matches!(
            backend
                .submit(
                    &identity,
                    "mj",
                    "imagine",
                    &axum::http::HeaderMap::new(),
                    serde_json::json!({"prompt":"test"}),
                )
                .await,
            Err(MidjourneyFailure::Upstream)
        ));
        assert!(matches!(
            backend
                .fetch_image("https://example.invalid/image.png")
                .await,
            Err(MidjourneyFailure::BlockedImage)
        ));
    }

    #[tokio::test]
    async fn anthropic_gemini_candidate_is_fail_closed_and_keeps_authenticated_delete_frozen() {
        let app =
            relay_anthropic_gemini_router(RelayHttpState::new(Arc::new(TestInstanceRelayBackend)));

        let anonymous_post = app
            .clone()
            .oneshot(
                Request::post("/v1/messages")
                    .body(Body::from(r#"{\"model\":\"claude-test\"}"#))
                    .expect("request is valid"),
            )
            .await
            .expect("router is infallible");
        // The production Go compatibility boundary conceals missing relay
        // credentials behind the generic public 404 response.  Keep the
        // isolated candidate surface aligned with that contract.
        assert_eq!(anonymous_post.status(), StatusCode::NOT_FOUND);

        let frozen_delete = app
            .oneshot(
                Request::delete("/v1/models/gpt-test")
                    .header("authorization", "Bearer lmm-test-relay-fixture")
                    .body(Body::empty())
                    .expect("request is valid"),
            )
            .await
            .expect("router is infallible");
        assert_eq!(frozen_delete.status(), StatusCode::NOT_IMPLEMENTED);
    }
}
