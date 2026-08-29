mod config;
mod http;
mod probes;
mod public_content;
mod rate_limit;
mod test_instance;
use async_trait::async_trait;
use config::Config;
use http::{ApiTokenMount, AppState, RuntimeState};
use lmm_api_rs::{
    auth::{
        AuthConfig, AuthHttpState, DashboardAuth, DashboardDeveloperAccessPolicy,
        PgValkeyDashboardAuth,
    },
    migration_routes::{
        access_ip::{AccessIpState, router as access_ip_router},
        account_action::{AccountActionState, router as account_action_router},
        admin_catalog::{
            AdminCatalogState, DashboardAdminCatalogAuthorizer, PgCatalogProvider,
            router as admin_catalog_router,
        },
        api_token::{ApiTokenHttpState, PgValkeyApiTokenService},
        assistant::{AssistantRateLimitConfig, AssistantReadState, assistant_read_router},
        billing_payments::{
            BillingConfig, BillingDependencies, BillingHttpState, DashboardBillingAuthorizer,
            DisabledCheckoutProvider, DisabledEpayVerifier, DisabledStripeWebhookVerifier,
            PgBillingPaymentAccess, PgBillingRepository, PgPaymentCompliance,
            SubscriptionBalancePayState, ValkeyBillingCache, billing_provider_payments_router,
            subscription_balance_pay_router,
        },
        billing_subscriptions::{
            BillingSubscriptionsState, router as billing_subscriptions_router,
            spawn_maintenance as spawn_subscription_maintenance,
        },
        channel_advanced::{
            ChannelAdvancedHttpState, DashboardChannelAdvancedAuthorizer, PgChannelAdvancedStore,
            ReqwestChannelAdvancedUpstream, StoreBackedChannelAdvancedProvider,
            channel_advanced_router,
        },
        channel_core::{ChannelCoreState, router as channel_core_router},
        channel_ops::{ChannelOpsHttpState, DashboardChannelAuthorizer, channel_ops_router},
        control_admin::{
            ControlAdminState, DashboardControlAdminAuthorizer, HttpOAuthDiscoveryClient,
            control_admin_router,
        },
        control_public::{
            ControlPublicHttpState, PgControlPublicRepository, ReqwestUptimeKumaClient,
            control_public_router,
        },
        deployment::{
            DeploymentJobRunner, DeploymentState, DisabledDeploymentJobRunner,
            IoNetDeploymentJobRunner, PgValkeyDeploymentProvider, router as deployment_router,
        },
        developer_access::{DeveloperAccessState, router as developer_access_router},
        discount_code::{DiscountCodeState, router as discount_code_router},
        dynamic_pricing::{DynamicPricingState, router as dynamic_pricing_router},
        finance::{FinanceState, router as finance_router},
        finance_export::{FinanceExportState, router as finance_export_router},
        gifts::{GiftState, router as gift_router},
        hero_sms::{
            DisabledHeroSmsGateway, HeroSmsRateLimitConfig, HeroSmsState, ReqwestHeroSmsGateway,
            router as hero_sms_router,
        },
        identity_2fa::{Identity2FAState, router as identity_2fa_router},
        identity_admin::{IdentityAdminState, router as identity_admin_router},
        identity_federation::{
            DashboardFederationIdentity, FederationState, ValkeyEmailCodeVerifier,
            ValkeyFederationMutationPublisher,
            bindings_router as identity_federation_bindings_router,
            oauth_email_bind_router as identity_federation_oauth_email_bind_router,
            oauth_external_provider_router as identity_federation_oauth_external_router,
            oauth_login_start_router as identity_federation_oauth_login_start_router,
            oauth_state_router as identity_federation_oauth_state_router,
        },
        identity_profile::{ProfileState, router as identity_profile_router},
        identity_security::{
            DashboardSecurityAuthorizer, IdentitySecurityState, PgValkeySecurityProvider,
        },
        kling_task_reads::{
            KlingTaskReadState, PgKlingTaskReadService, router as kling_task_read_router,
        },
        mcp::{McpHttpState, mcp_router},
        media_midjourney::{
            MidjourneyHttpState, PgMidjourneyDispatchBackend, media_midjourney_router,
        },
        media_tasks::{MediaTaskHttpState, MidjourneyMediaTaskService, media_provider_task_router},
        missing_billing_dashboard::{
            BillingDashboardState, PgBillingDashboardAuthorizer, PgBillingDashboardStore,
            billing_dashboard_router,
        },
        missing_billing_webhooks::{
            DisabledPancakeWebhookVerifier, DisabledWaffoWebhookAvailability,
            DisabledWaffoWebhookProcessor, DisabledWaffoWebhookVerifier, WaffoWebhookState,
            missing_billing_webhooks_router,
        },
        missing_control_ratio_sync::{
            DashboardRatioSyncAuthorizer, HttpRatioSyncUpstream, PgRatioSyncRepository,
            RatioSyncHttpState, ratio_sync_router,
        },
        missing_control_tasks::{
            ControlTaskStatusError, ControlTaskStatusProbe, MissingControlTasksState,
            PgControlTaskStore, missing_control_tasks_router,
        },
        missing_identity_catalog::{IdentityCatalogState, router as identity_catalog_router},
        missing_identity_checkin_aff::{
            IdentityCheckinAffState, router as identity_checkin_aff_router,
        },
        missing_identity_epay::{
            DashboardTopupAuthorizer, DisabledEpayGateway, DisabledTopupRepository, UserTopupState,
            router as identity_epay_router,
        },
        missing_identity_stripe_creem::{
            DashboardStripeCreemAuthorizer, DisabledStripeCreemGateway, IdentityStripeCreemState,
            PgStripeCreemStore, amount_router as identity_stripe_amount_router,
            pay_router as identity_stripe_pay_router,
        },
        missing_identity_topup::{IdentityTopupState, router as identity_topup_router},
        missing_identity_waffo::{
            DisabledTopUpGateway, WaffoTopUpState, router as identity_waffo_router,
        },
        missing_relay_misc_new::{
            FailClosedRelayMiscService, MissingRelayMiscState, missing_relay_misc_router,
        },
        missing_relay_models_billing::{ModelLookupState, PgStaticModelLookup},
        missing_relay_video::{
            FailClosedRelayVideoService, RelayVideoHttpState, missing_relay_video_router,
        },
        observability::{
            DashboardObservabilityAuthorizer, ObservabilityAuthorizer, ObservabilityState,
            PgDiskCacheMaintenance, PgObservabilityStore, PgReadOnlyObservabilityTokenAuthorizer,
            PostgresObservabilityMetrics, UnavailableObservabilityMetrics,
            ValkeyObservabilityMetrics, observability_disk_cache_router,
            observability_force_gc_router, observability_metrics_router,
            observability_performance_router, observability_read_router,
        },
        open_source_bounties::{OpenSourceBountyState, router as open_source_bounty_router},
        public_relay::{PublicRelayState, router as public_relay_router},
        relay_anthropic_gemini::{
            RelayHttpState as AnthropicGeminiHttpState, router_with_model_lookup,
        },
        relay_anthropic_gemini_postgres::PgAnthropicGeminiRelayBackend,
        relay_media::{
            MediaUpstreamClient, PgRelayMediaService, RelayMediaHttpState, relay_media_router,
        },
        relay_misc::RelayMiscHttpState,
        relay_misc_active::router as relay_misc_active_router,
        relay_misc_frozen::router as relay_misc_frozen_router,
        relay_misc_postgres::PgRelayMiscService,
        relay_openai::{
            OpenAiRelayHttpState, OpenAiUpstreamClient, PgOpenAiRelayService, openai_relay_router,
        },
        release_notes::{ReleaseNoteState, router as release_note_router},
        responses_websocket::{
            ResponsesWebSocketState, UnconfiguredResponsesWebSocketService,
            router as responses_websocket_router,
        },
        security_admin::{SecurityAdminState, router as security_admin_router},
        security_overview::{SecurityOverviewState, router as security_overview_router},
        system_config::{
            DashboardRootAuthorizer, HttpProjectUpdateClient, HttpWaffoPancakeGateway,
            ProcessRuntimeOptions, SystemConfigHttpState, system_config_router,
        },
        unified_todo::{UnifiedTodoState, router as unified_todo_router},
        user_assistant_admin::{UserAssistantAdminState, router as user_assistant_admin_router},
        user_rankings::{UserRankingsState, router as user_rankings_router},
        verify_email::{
            PgSmtpSecurityEmailSender, ValkeyVerificationCodeStore, VerifyEmailState,
            router as verify_email_router,
        },
    },
    models::{ModelsHttpState, ModelsListenerMode, PgModelsService},
    protocol_runtime_registry::validated_current_registry,
    status::{PgStatusRepository, StatusHttpState, StatusRepository},
};
use lmm_application::{GlobalApiRateLimiter, ValkeyReadinessPolicy};
use probes::InfrastructureProbe;
use public_content::{PgPublicContentRepository, ValkeyPublicContentCache};
use rate_limit::ValkeyGlobalApiRateLimiter;
use secrecy::ExposeSecret;
use sqlx::postgres::PgPoolOptions;
use std::{
    future::IntoFuture,
    io,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};
use tokio::{net::TcpListener, sync::watch};

#[derive(Clone)]
struct ListenerControlTaskStatusProbe {
    pg: sqlx::PgPool,
    runtime: RuntimeState,
}

#[async_trait]
impl ControlTaskStatusProbe for ListenerControlTaskStatusProbe {
    async fn test_status(&self) -> Result<serde_json::Value, ControlTaskStatusError> {
        sqlx::query_scalar::<_, i64>("SELECT 1::bigint")
            .fetch_one(&self.pg)
            .await
            .map_err(|error| {
                tracing::warn!(%error, "control task status database probe failed");
                ControlTaskStatusError::DatabaseUnavailable
            })?;
        Ok(serde_json::json!({
            // The status request itself is already inside Rust's listener
            // boundary. Go's StatsMiddleware reports the pre-handler count
            // for this route, so exclude this request while retaining any
            // concurrently active work.
            "active_connections": self.runtime.inflight().saturating_sub(1),
        }))
    }
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn std::error::Error>> {
    match lmm_api_rs::cli::dispatch_environment().await {
        lmm_api_rs::cli::DispatchOutcome::Serve => {}
        lmm_api_rs::cli::DispatchOutcome::Exit(0) => return Ok(()),
        lmm_api_rs::cli::DispatchOutcome::Exit(code) => std::process::exit(code),
    }

    lmm_observability::init()?;
    let config = Config::from_env()?;
    let protocol_registry = Arc::new(validated_current_registry().map_err(|error| {
        io::Error::other(format!("protocol runtime registry invalid: {error}"))
    })?);
    // Local acceptance is an explicit loopback-only development policy. It
    // must never alter the isolated frozen listener's historical contract.
    let local_acceptance = config.local_acceptance && !config.test_instance;
    if config.trusted_proxies.uses_compatibility_defaults() {
        tracing::warn!(
            "TRUSTED_PROXIES is unset or blank; trusting loopback, RFC 1918, and IPv6 ULA proxy addresses for compatibility"
        );
    }
    let version = std::env::var("VERSION").unwrap_or_else(|_| "v0.0.0".to_owned());
    let start_time = i64::try_from(SystemTime::now().duration_since(UNIX_EPOCH)?.as_secs())?;
    let pg = PgPoolOptions::new().connect_lazy(&config.database_url)?;
    let status_repository =
        Arc::new(PgStatusRepository::new(pg.clone()).with_local_acceptance(local_acceptance));
    let initial_options = status_repository.snapshot().await?.options;
    let passkey_enabled = initial_options
        .get("passkey.enabled")
        .and_then(|value| value.parse::<bool>().ok())
        .unwrap_or(false);
    let turnstile = config.auth_turnstile.resolve_public(&initial_options)?;
    let runtime_options = ProcessRuntimeOptions::new(initial_options)
        .with_protocol_rollout(config.protocol_rollout.clone())
        .await
        .map_err(|_| io::Error::other("protocol rollout configuration invalid"))?;
    let protocol_rollout = runtime_options
        .protocol_rollout()
        .ok_or_else(|| io::Error::other("protocol rollout control unavailable"))?;
    let runtime_options = Arc::new(runtime_options);
    let valkey = redis::Client::open(config.valkey_url.as_str())?;
    let probe = InfrastructureProbe::new(
        pg.clone(),
        valkey.clone(),
        config.schema_contract,
        config.dependency_timeout,
    );
    let global_api_rate_limiter: Arc<dyn GlobalApiRateLimiter> =
        Arc::new(ValkeyGlobalApiRateLimiter::new(
            valkey.clone(),
            config.valkey_readiness_policy,
            config.global_api_rate_limit,
            config.global_api_rate_limit_window,
            config.dependency_timeout,
        ));
    let auth_impl = Arc::new(
        PgValkeyDashboardAuth::new(
            pg.clone(),
            valkey.clone(),
            AuthConfig {
                session_secret: config.auth_session_secret.clone(),
                active_session_limit: config.auth_active_session_limit,
                issuance_limit: config.auth_issuance_limit,
                issuance_window: config.auth_issuance_window,
                session_cache_ttl: config.auth_session_cache_ttl,
                dependency_timeout: config.dependency_timeout,
                critical_rate_limit_enabled: config.auth_critical_rate_limit_enabled,
                critical_rate_limit: config.auth_critical_rate_limit,
                critical_rate_limit_window: config.auth_critical_rate_limit_window,
            },
        )?
        .with_local_acceptance(local_acceptance),
    );
    let auth: Arc<dyn DashboardAuth> = auth_impl.clone();
    let auth_http = AuthHttpState::new(Arc::clone(&auth), config.auth_cookie_secure)
        .with_password_login_enabled(config.auth_password_login_enabled)
        .with_trusted_origins(&config.auth_trusted_origins)
        .with_anonymous_body_limit_bytes(config.auth_anonymous_body_limit_bytes)
        .with_turnstile_check(turnstile.enabled, config.auth_turnstile.secret_key())
        .with_version(version.clone());
    let models_service = Arc::new(
        PgModelsService::with_valkey(
            pg.clone(),
            valkey.clone(),
            config.crypto_secret.expose_secret(),
            config.models_cache_ttl,
        )
        .with_local_acceptance(local_acceptance),
    );
    let models_http = ModelsHttpState::new(
        Arc::clone(&models_service) as Arc<dyn lmm_api_rs::models::ModelsService>,
        version.clone(),
    );
    let api_token_service = PgValkeyApiTokenService::new(pg.clone(), valkey.clone())
        .with_cache_ttl(config.models_cache_ttl)
        .with_crypto_secret(config.crypto_secret.expose_secret())
        .with_console_activation_on_create(!config.test_instance)
        .with_auto_groups_cache(!config.test_instance);
    let api_token = ApiTokenMount::new(
        ApiTokenHttpState::new(Arc::new(api_token_service)),
        Arc::clone(&auth),
        valkey.clone(),
        config.dependency_timeout,
        config.api_token_search_rate_limit_enabled,
        config.api_token_search_rate_limit,
        config.api_token_search_rate_limit_window,
    );
    let listener = TcpListener::bind(config.listen_addr).await?;
    let runtime = RuntimeState::default();
    tracing::info!(
        listen_addr = %config.listen_addr,
        slot = %config.slot,
        revision = option_env!("LMM_BUILD_REVISION").unwrap_or("unknown"),
        "Rust migration edge listening"
    );
    if local_acceptance {
        tracing::warn!(
            "LMM_LOCAL_ACCEPTANCE enabled; developer access granted without paid activation"
        );
    }
    let (shutdown_tx, shutdown_rx) = watch::channel(false);
    let app_state = AppState {
        readiness: Arc::new(probe),
        // Dashboard sessions always depend on Valkey even when the
        // separate global API limiter is disabled.
        valkey_readiness_policy: ValkeyReadinessPolicy::RequiredForRateLimiting,
        global_api_rate_limiter: Arc::clone(&global_api_rate_limiter),
        public_content: Arc::new(lmm_application::PublicContentService::new(
            Arc::new(PgPublicContentRepository::new(pg.clone())),
            Arc::new(ValkeyPublicContentCache::new(valkey.clone())),
            config.dependency_timeout,
        )),
        status: StatusHttpState::new(status_repository, version, start_time)
            .with_dashboard_auth(Arc::clone(&auth))
            .with_turnstile_config(turnstile.enabled, turnstile.site_key),
        slot: config.slot.clone(),
        runtime: runtime.clone(),
        trusted_proxies: config.trusted_proxies.clone(),
    };
    let router = if config.test_instance {
        // Only the explicitly isolated historical listener may use the frozen
        // Go 5418ce6 model/API-token contract.
        let models_http = models_http.with_listener_mode(ModelsListenerMode::FrozenGo5418ce6);
        tracing::warn!(
            "test-instance candidate surface enabled; remote catalog and uptime clients are denied"
        );
        let candidates = http::migration_candidate_test_surface(
            &app_state,
            test_instance::safe_candidate_surface(pg.clone(), valkey.clone(), Arc::clone(&auth)),
        );
        http::router_with_api_token_and_extra(
            app_state,
            auth_http,
            models_http,
            Some(api_token.with_historical_frozen_go_parity()),
            Some(candidates),
        )
    } else {
        // C1 exercises the current-Go API-token policy on the normal Rust
        // listener. This does not transfer production ownership from Go.
        let models_http = models_http.with_listener_mode(ModelsListenerMode::CurrentTrustPolicy);
        let identity_profile = identity_profile_router(
            ProfileState::new(pg.clone(), valkey.clone()).with_dashboard_auth(Arc::clone(&auth)),
        );
        let identity_catalog = http::api_global_rate_limited_surface(
            &app_state,
            identity_catalog_router(IdentityCatalogState::new(pg.clone(), Arc::clone(&auth))),
        );
        let catalog_http = reqwest::Client::builder()
            .timeout(config.dependency_timeout)
            .build()
            .map_err(|_| io::Error::other("failed to initialize catalog HTTP client"))?;
        let admin_catalog = http::api_global_rate_limited_surface(
            &app_state,
            admin_catalog_router(AdminCatalogState::new(
                Arc::new(
                    PgCatalogProvider::with_official_upstream(pg.clone(), catalog_http)
                        .map_err(|_| io::Error::other("failed to initialize catalog provider"))?,
                ),
                Arc::new(DashboardAdminCatalogAuthorizer::new(Arc::clone(&auth))),
            )),
        );
        // Administrator user-management routes use the same PostgreSQL and
        // Valkey-backed auth/session fences as the isolated candidate surface.
        // Keep them a Rust candidate while Go retains production ownership.
        let identity_admin = http::api_global_rate_limited_surface(
            &app_state,
            identity_admin_router(IdentityAdminState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
            )),
        );
        let identity_topup = http::api_global_rate_limited_surface(
            &app_state,
            identity_topup_router(IdentityTopupState::new(pg.clone(), Arc::clone(&auth))),
        );
        // Stripe amount quoting is a deterministic PostgreSQL/configuration
        // calculation.  Checkout is mounted with the same authorizer and a
        // fail-closed gateway so the path cannot 404 through to Go.
        let identity_stripe_creem_state = IdentityStripeCreemState::new(
            Arc::new(PgStripeCreemStore::new(pg.clone())),
            Arc::new(DashboardStripeCreemAuthorizer::new(Arc::clone(&auth))),
            Arc::new(DisabledStripeCreemGateway),
        );
        let identity_stripe_amount = http::api_global_rate_limited_surface(
            &app_state,
            identity_stripe_amount_router(identity_stripe_creem_state.clone()),
        );
        let identity_stripe_pay = http::api_global_rate_limited_surface(
            &app_state,
            identity_stripe_pay_router(identity_stripe_creem_state),
        );
        let identity_epay = http::api_global_rate_limited_surface(
            &app_state,
            identity_epay_router(UserTopupState::new(
                Arc::new(DashboardTopupAuthorizer::new(Arc::clone(&auth))),
                Arc::new(DisabledTopupRepository),
                Arc::new(DisabledEpayGateway),
            )),
        );
        let identity_waffo = http::api_global_rate_limited_surface(
            &app_state,
            identity_waffo_router(WaffoTopUpState::new(
                pg.clone(),
                Arc::clone(&auth),
                Arc::new(DisabledTopUpGateway),
            )),
        );
        let identity_checkin = http::api_global_rate_limited_surface(
            &app_state,
            identity_checkin_aff_router(
                IdentityCheckinAffState::new(pg.clone(), Arc::clone(&auth))
                    .with_effects(Arc::new(
                        lmm_api_rs::migration_routes::missing_identity_checkin_aff::PgValkeyCheckinEffects::new(
                            pg.clone(),
                            valkey.clone(),
                        ),
                    )),
            ),
        );
        // Account-security routes share the same PostgreSQL/Valkey session
        // authority as the four core auth routes. The registration policy is
        // injected here so the full security router cannot accidentally expose
        // an anonymous account-creation path without the listener-owned
        // body/critical-rate/Turnstile boundary.
        let identity_security = lmm_api_rs::migration_routes::identity_security::router(
            IdentitySecurityState::new(
                Arc::new(PgValkeySecurityProvider::new(pg.clone(), valkey.clone())),
                Arc::new(DashboardSecurityAuthorizer::new(Arc::clone(&auth))),
            )
            .with_passkey_enabled(passkey_enabled)
            .with_registration_security(auth_http.anonymous_request_security()),
        );
        let identity_security =
            http::api_global_rate_limited_surface(&app_state, identity_security);
        // The security-email sender and `/api/verify` consumer share the same
        // purpose-scoped Valkey store. Codes are atomically consumed before
        // the dashboard auth adapter issues a session-bound proof.
        let verify_email = http::api_global_rate_limited_surface(
            &app_state,
            verify_email_router(
                VerifyEmailState::new(pg.clone(), valkey.clone(), Arc::clone(&auth))
                    .with_mailer(Arc::new(PgSmtpSecurityEmailSender::new(pg.clone())))
                    .with_code_store(Arc::new(ValkeyVerificationCodeStore::new(valkey.clone()))),
            ),
        );
        let federation_identity = Arc::new(
            DashboardFederationIdentity::new(
                Arc::clone(&auth),
                pg.clone(),
                &config.auth_session_secret,
                Arc::new(ValkeyEmailCodeVerifier::new(valkey.clone())),
            )
            .map_err(|_| io::Error::other("failed to initialize federation identity"))?,
        );
        let federation_state = FederationState::new(
            pg.clone(),
            federation_identity,
            config.auth_session_secret.expose_secret(),
        )
        .with_mutation_publisher(Arc::new(ValkeyFederationMutationPublisher::new(
            valkey.clone(),
        )));
        let identity_federation_bindings = http::api_global_rate_limited_surface(
            &app_state,
            identity_federation_bindings_router(federation_state.clone()),
        );
        let identity_federation_oauth_state = http::api_global_rate_limited_surface(
            &app_state,
            identity_federation_oauth_state_router(
                federation_state.clone(),
                Arc::clone(&auth),
                config.auth_anonymous_body_limit_bytes,
            ),
        );
        let identity_federation_oauth_email_bind = http::api_global_rate_limited_surface(
            &app_state,
            identity_federation_oauth_email_bind_router(
                federation_state.clone(),
                Arc::clone(&auth),
            ),
        );
        let identity_federation_oauth_external = http::api_global_rate_limited_surface(
            &app_state,
            identity_federation_oauth_external_router(federation_state.clone()),
        );
        let identity_federation_oauth_login_start = http::api_global_rate_limited_surface(
            &app_state,
            identity_federation_oauth_login_start_router(federation_state),
        );
        let identity_2fa = identity_2fa_router(
            Identity2FAState::new(pg.clone(), valkey.clone(), auth_impl.clone())
                .with_dashboard_auth(Arc::clone(&auth))
                .with_cookie_secure(config.auth_cookie_secure),
        );
        let channel_authorizer = Arc::new(DashboardChannelAuthorizer::new(Arc::clone(&auth)));
        let channel_core = channel_core_router(ChannelCoreState {
            pg: pg.clone(),
            valkey: valkey.clone(),
            authorizer: channel_authorizer.clone(),
            retry_times: 0,
        });
        let channel_ops = channel_ops_router(ChannelOpsHttpState::new(
            pg.clone(),
            valkey.clone(),
            channel_authorizer,
        ));
        // Advanced channel management owns its persisted channel lookup and
        // outbound protocol boundary in Rust.  The adapter still fails closed
        // for destinations that do not satisfy its target policy; mounting it
        // here makes the normal listener's route surface explicit instead of
        // silently falling through to Go.
        let channel_advanced = channel_advanced_router(ChannelAdvancedHttpState::new(
            Arc::new(DashboardChannelAdvancedAuthorizer::new(Arc::clone(&auth))),
            Arc::new(StoreBackedChannelAdvancedProvider::new(
                Arc::new(PgChannelAdvancedStore::new(pg.clone())),
                Arc::new(
                    ReqwestChannelAdvancedUpstream::new()
                        .map_err(|_| {
                            io::Error::other("failed to initialize advanced channel client")
                        })?
                        .with_pg_pool(pg.clone()),
                ),
            )),
        ));
        // Deployment management is mounted on the normal listener with the
        // durable PostgreSQL/Valkey coordinator.  A missing server-owned
        // io.net key deliberately selects the fail-closed runner; it must not
        // turn an incomplete deployment into a fabricated success.
        let deployment_runner: Arc<dyn DeploymentJobRunner> = match std::env::var("IONET_API_KEY") {
            Ok(api_key) if !api_key.trim().is_empty() => Arc::new(
                IoNetDeploymentJobRunner::new(secrecy::SecretString::from(api_key))
                    .map_err(|_| io::Error::other("failed to initialize io.net client"))?,
            ),
            _ => Arc::new(DisabledDeploymentJobRunner),
        };
        let deployment = deployment_router(
            DeploymentState::new(Arc::new(PgValkeyDeploymentProvider::new(
                pg.clone(),
                valkey.clone(),
                deployment_runner,
            )))
            .with_dashboard_auth(Arc::clone(&auth)),
        );
        let control_admin = control_admin_router(
            ControlAdminState::new(
                pg.clone(),
                Arc::new(DashboardControlAdminAuthorizer::new(Arc::clone(&auth))),
                Arc::new(HttpOAuthDiscoveryClient::production().map_err(|_| {
                    io::Error::other("failed to initialize OAuth discovery client")
                })?),
            )
            .with_valkey(valkey.clone()),
        );
        let billing_subscriptions = billing_subscriptions_router(
            BillingSubscriptionsState::new(pg.clone(), Some(valkey.clone()), Arc::clone(&auth))
                .with_console_access_gate(),
        );
        let _subscription_maintenance =
            spawn_subscription_maintenance(pg.clone(), Some(valkey.clone()));
        let relay_client = lmm_api_rs::outbound_http::relay_client(config.dependency_timeout)
            .map_err(|_| io::Error::other("failed to initialize relay HTTP client"))?;
        let assistant_reads = assistant_read_router(
            AssistantReadState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
                config.auth_session_secret.clone(),
                AssistantRateLimitConfig {
                    enabled: config.auth_critical_rate_limit_enabled,
                    max_requests: config.auth_critical_rate_limit,
                    window: config.auth_critical_rate_limit_window,
                    dependency_timeout: config.dependency_timeout,
                },
                DashboardDeveloperAccessPolicy::new(local_acceptance),
            )
            .with_agent_relay(relay_client.clone(), config.dependency_timeout),
        );
        let developer_access = http::api_global_rate_limited_surface(
            &app_state,
            developer_access_router(DeveloperAccessState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
            )),
        );
        let account_action = http::api_global_rate_limited_surface(
            &app_state,
            account_action_router(AccountActionState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
                config.auth_session_secret.clone(),
            )),
        );
        let hero_sms_gateway: Arc<dyn lmm_api_rs::migration_routes::hero_sms::HeroSmsGateway> =
            if local_acceptance {
                Arc::new(DisabledHeroSmsGateway)
            } else {
                Arc::new(
                    ReqwestHeroSmsGateway::production(config.dependency_timeout)
                        .map_err(|_| io::Error::other("failed to initialize HeroSMS client"))?,
                )
            };
        let hero_sms = http::api_global_rate_limited_surface(
            &app_state,
            hero_sms_router(
                HeroSmsState::new(pg.clone(), Arc::clone(&auth), hero_sms_gateway)
                    .with_sms_user_rate_limit(
                        valkey.clone(),
                        HeroSmsRateLimitConfig {
                            enabled: config.auth_critical_rate_limit_enabled,
                            max_requests: config.auth_critical_rate_limit,
                            window: config.auth_critical_rate_limit_window,
                            dependency_timeout: config.dependency_timeout,
                        },
                    ),
            ),
        );
        let finance_export = http::api_global_rate_limited_surface(
            &app_state,
            finance_export_router(FinanceExportState::new(pg.clone(), Arc::clone(&auth))),
        );
        let finance = http::api_global_rate_limited_surface(
            &app_state,
            finance_router(FinanceState::new(pg.clone(), Arc::clone(&auth))),
        );
        let release_notes = http::api_global_rate_limited_surface(
            &app_state,
            release_note_router(ReleaseNoteState::new(pg.clone(), Arc::clone(&auth))),
        );
        let security_overview = http::api_global_rate_limited_surface(
            &app_state,
            security_overview_router(SecurityOverviewState::new(pg.clone(), Arc::clone(&auth))),
        );
        let security_admin = http::api_global_rate_limited_surface(
            &app_state,
            security_admin_router(SecurityAdminState::new(pg.clone(), Arc::clone(&auth))),
        );
        let unified_todo = http::api_global_rate_limited_surface(
            &app_state,
            unified_todo_router(UnifiedTodoState::new(pg.clone(), Arc::clone(&auth))),
        );
        let user_assistant_admin = http::api_global_rate_limited_surface(
            &app_state,
            user_assistant_admin_router(UserAssistantAdminState::new(
                pg.clone(),
                Arc::clone(&auth),
            )),
        );
        let access_ip = http::api_global_rate_limited_surface(
            &app_state,
            access_ip_router(AccessIpState::new(pg.clone(), Arc::clone(&auth))),
        );
        let user_rankings = http::api_global_rate_limited_surface(
            &app_state,
            user_rankings_router(UserRankingsState::new(pg.clone(), Arc::clone(&auth))),
        );
        let gifts = http::api_global_rate_limited_surface(
            &app_state,
            gift_router(GiftState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
            )),
        );
        let discount_code = http::api_global_rate_limited_surface(
            &app_state,
            discount_code_router(DiscountCodeState::new(pg.clone(), Arc::clone(&auth))),
        );
        let dynamic_pricing = http::api_global_rate_limited_surface(
            &app_state,
            dynamic_pricing_router(DynamicPricingState::new(
                pg.clone(),
                valkey.clone(),
                Arc::clone(&auth),
            )),
        );
        // Balance purchases are the first payment write family whose complete
        // ledger is local to PostgreSQL. The provider-capable checkout and
        // callback routes remain separately frozen until their Go SDK
        // contracts are ported and configured. The repository re-reads the
        // persisted QuotaPerUnit option per transaction; this value is only
        // the legacy default used when that option is absent.
        let subscription_balance_pay = http::api_global_rate_limited_surface(
            &app_state,
            subscription_balance_pay_router(
                SubscriptionBalancePayState::new(
                    Arc::new(PgBillingRepository::new(pg.clone())),
                    Arc::new(DashboardBillingAuthorizer::new(Arc::clone(&auth))),
                    Arc::new(ValkeyBillingCache::new(valkey.clone())),
                    Arc::new(PgPaymentCompliance::new(pg.clone())),
                    500_000,
                )
                .with_dashboard_auth(Arc::clone(&auth))
                .with_payment_access(Arc::new(PgBillingPaymentAccess::new(pg.clone()))),
            ),
        );
        // Provider checkout and callback routes stay fail-closed until a live
        // gateway is configured. Mounting them here keeps the frozen Go
        // surface from 404ing through the Rust listener.
        let billing_provider_payments = http::api_global_rate_limited_surface(
            &app_state,
            billing_provider_payments_router(BillingHttpState::new(
                BillingDependencies {
                    repository: Arc::new(PgBillingRepository::new(pg.clone())),
                    authorizer: Arc::new(DashboardBillingAuthorizer::new(Arc::clone(&auth))),
                    checkout: Arc::new(DisabledCheckoutProvider),
                    epay: Arc::new(DisabledEpayVerifier),
                    stripe: Arc::new(DisabledStripeWebhookVerifier),
                    cache: Arc::new(ValkeyBillingCache::new(valkey.clone())),
                    compliance: Arc::new(PgPaymentCompliance::new(pg.clone())),
                },
                BillingConfig::default(),
            )),
        );
        let billing_webhooks = missing_billing_webhooks_router(WaffoWebhookState::new(
            Arc::new(DisabledWaffoWebhookAvailability),
            Arc::new(DisabledPancakeWebhookVerifier),
            Arc::new(DisabledWaffoWebhookVerifier),
            Arc::new(DisabledWaffoWebhookProcessor),
        ));
        let kling_task_reads = kling_task_read_router(KlingTaskReadState::new(Arc::new(
            PgKlingTaskReadService::new(pg.clone(), Arc::clone(&models_service)),
        )));
        let billing_dashboard = billing_dashboard_router(BillingDashboardState::new(
            Arc::new(PgBillingDashboardStore::new(pg.clone())),
            Arc::new(PgBillingDashboardAuthorizer::new(pg.clone())),
        ));
        let observability_authorizer: Arc<dyn ObservabilityAuthorizer> =
            Arc::new(DashboardObservabilityAuthorizer::new(
                Arc::clone(&auth),
                Arc::new(PgReadOnlyObservabilityTokenAuthorizer::new(pg.clone())),
            ));
        let control_tasks = missing_control_tasks_router(
            MissingControlTasksState::new(
                Arc::new(PgControlTaskStore::new(pg.clone())),
                Arc::clone(&observability_authorizer),
                Arc::new(ListenerControlTaskStatusProbe {
                    pg: pg.clone(),
                    runtime: runtime.clone(),
                }),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let ratio_sync = ratio_sync_router(RatioSyncHttpState::new(
            Arc::new(PgRatioSyncRepository::new(pg.clone())),
            Arc::new(HttpRatioSyncUpstream),
            Arc::new(DashboardRatioSyncAuthorizer::new(Arc::clone(&auth))),
        ));
        let observability_read = observability_read_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::postgres_read_only(
                    pg.clone(),
                    Arc::new(ValkeyObservabilityMetrics::new(valkey.clone())),
                )),
                Arc::clone(&observability_authorizer),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let observability_metrics = observability_metrics_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::postgres_read_only(
                    pg.clone(),
                    Arc::new(
                        PostgresObservabilityMetrics::new(pg.clone()).with_valkey(valkey.clone()),
                    ),
                )),
                Arc::clone(&observability_authorizer),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let observability_disk_cache = observability_disk_cache_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::new(
                    pg.clone(),
                    Arc::new(UnavailableObservabilityMetrics),
                    Arc::new(PgDiskCacheMaintenance::new(pg.clone())),
                )),
                Arc::clone(&observability_authorizer),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let observability_performance = observability_performance_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::new(
                    pg.clone(),
                    Arc::new(UnavailableObservabilityMetrics),
                    Arc::new(PgDiskCacheMaintenance::new(pg.clone())),
                )),
                Arc::clone(&observability_authorizer),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let observability_force_gc = observability_force_gc_router(
            ObservabilityState::new(
                Arc::new(PgObservabilityStore::new(
                    pg.clone(),
                    Arc::new(UnavailableObservabilityMetrics),
                    Arc::new(PgDiskCacheMaintenance::new(pg.clone())),
                )),
                Arc::clone(&observability_authorizer),
            )
            .with_console_access_gate(Arc::clone(&auth)),
        );
        let open_source_bounties = http::api_global_rate_limited_surface(
            &app_state,
            open_source_bounty_router(OpenSourceBountyState::new(pg.clone(), Arc::clone(&auth))),
        );
        let public_relays = http::api_global_rate_limited_surface(
            &app_state,
            public_relay_router(PublicRelayState::new(pg.clone(), Arc::clone(&auth))),
        );
        // OpenAI-compatible and media relay routes use the same PostgreSQL
        // token/channel authority as the rest of the normal listener.  Keep
        // the upstream client bounded and let each executor own its billing
        // transaction; these routes must not fall through to a legacy Go
        // process once the Rust listener is selected.
        let relay_openai = openai_relay_router(
            OpenAiRelayHttpState::new(
                Arc::new(PgOpenAiRelayService::new(
                    pg.clone(),
                    OpenAiUpstreamClient::new(relay_client.clone(), config.dependency_timeout),
                    1,
                )),
                app_state.status.version().to_owned(),
            )
            .with_protocol_runtime(protocol_rollout.clone(), protocol_registry.clone()),
        );
        // Midjourney submissions need an outer distributor because the
        // request-scoped backend intentionally holds one selected channel.
        // The distributor resolves token/user group and `abilities` before
        // any provider request, while child actions retain their persisted
        // origin channel inside `PgMidjourneyBackend`.
        let relay_midjourney = media_midjourney_router(MidjourneyHttpState::new(Arc::new(
            PgMidjourneyDispatchBackend::new(
                pg.clone(),
                relay_client.clone(),
                config.dependency_timeout,
                64 * 1024 * 1024,
            ),
        )));
        let relay_media_tasks = media_provider_task_router(MediaTaskHttpState::new(Arc::new(
            MidjourneyMediaTaskService::new(Arc::new(PgMidjourneyDispatchBackend::new(
                pg.clone(),
                relay_client.clone(),
                config.dependency_timeout,
                64 * 1024 * 1024,
            ))),
        )));
        let relay_video = missing_relay_video_router(RelayVideoHttpState::new(Arc::new(
            FailClosedRelayVideoService::new(),
        )));
        let relay_misc_new = missing_relay_misc_router(MissingRelayMiscState::new(Arc::new(
            FailClosedRelayMiscService::new(),
        )));
        let responses_websocket = responses_websocket_router(ResponsesWebSocketState::new(
            Arc::new(UnconfiguredResponsesWebSocketService),
        ));
        let mcp = http::api_global_rate_limited_surface(
            &app_state,
            mcp_router(McpHttpState::new(
                pg.clone(),
                valkey.clone(),
                config.dependency_timeout,
            )),
        );
        let model_lookup_state = ModelLookupState::new(
            Arc::new(PgStaticModelLookup::with_current_policy(
                pg.clone(),
                local_acceptance,
            )),
            app_state.status.version().to_owned(),
        );
        let relay_anthropic_gemini = router_with_model_lookup(
            AnthropicGeminiHttpState::new(Arc::new(PgAnthropicGeminiRelayBackend::new(
                pg.clone(),
                relay_client.clone(),
                config.dependency_timeout,
            )))
            .with_protocol_runtime(protocol_rollout.clone(), protocol_registry.clone()),
            model_lookup_state.clone(),
        );
        let relay_media = relay_media_router(RelayMediaHttpState::new(Arc::new(
            PgRelayMediaService::new(
                pg.clone(),
                MediaUpstreamClient::new(relay_client.clone(), config.dependency_timeout),
                1,
            ),
        )));
        let relay_misc_service = Arc::new(
            PgRelayMiscService::new(
                pg.clone(),
                Arc::clone(&models_service),
                relay_client.clone(),
                config.dependency_timeout,
            )
            .with_model_rate_limit_valkey(valkey.clone(), config.dependency_timeout),
        );
        let relay_misc_state = RelayMiscHttpState::new(relay_misc_service);
        let relay_misc_active = relay_misc_active_router(relay_misc_state.clone());
        let relay_misc_frozen = relay_misc_frozen_router(relay_misc_state);
        // The single-model GET is a read-only static catalogue lookup. Keep
        // it separate from provider relay methods, and apply the current Go
        // trust gate before exposing whether a model exists.
        // The combined Anthropic/Gemini router owns both the static single
        // model GET and its overlapping POST/DELETE compatibility methods.
        let control_public = if local_acceptance {
            // Local acceptance must never contact an operator-configured
            // uptime service; the test adapter fails closed instead.
            test_instance::safe_control_public_surface(pg.clone())
        } else {
            let uptime_kuma = ReqwestUptimeKumaClient::new()
                .map_err(|_| io::Error::other("failed to initialize uptime status client"))?;
            control_public_router(ControlPublicHttpState::new(
                Arc::new(PgControlPublicRepository::new(pg.clone())),
                Arc::new(uptime_kuma),
            ))
        };
        // These anonymous `/api` routes are outside the authenticated and
        // API-token mounts, so give them their own single Go-compatible
        // GlobalAPIRateLimit boundary before merging them into the listener.
        let control_public = http::api_global_rate_limited_surface(&app_state, control_public);
        // These seven dashboard/public reads are backed only by PostgreSQL
        // and the shared session authority.  They have no provider-capable
        // outbound client, so the normal listener can own their route
        // boundary while their positive behavior is independently diffed.
        let missing_control_public =
            test_instance::durable_missing_control_public_surface(pg.clone(), Arc::clone(&auth));
        let system_config = if local_acceptance {
            // Loopback developer access must retain the no-egress adapter;
            // production provider clients are composed only for normal
            // blue/green listeners.
            test_instance::safe_system_config_surface(pg.clone(), valkey.clone(), Arc::clone(&auth))
        } else {
            let project_update_client =
                lmm_api_rs::outbound_http::client(config.dependency_timeout)
                    .map_err(|_| io::Error::other("failed to initialize project update client"))?;
            let pancake = HttpWaffoPancakeGateway::production(config.dependency_timeout)
                .map_err(|_| io::Error::other("failed to initialize Waffo Pancake client"))?;
            system_config_router(
                SystemConfigHttpState::new(
                    pg.clone(),
                    valkey.clone(),
                    Arc::new(DashboardRootAuthorizer::new(Arc::clone(&auth))),
                    Arc::new(HttpProjectUpdateClient::new(project_update_client)),
                    Arc::new(pancake),
                )
                .with_anonymous_body_limit_bytes(config.auth_anonymous_body_limit_bytes)
                .with_runtime_writer(runtime_options.clone()),
            )
        };
        let system_config = http::api_global_rate_limited_surface(&app_state, system_config);
        let extra_surface = identity_profile
            .merge(identity_catalog)
            .merge(admin_catalog)
            .merge(identity_admin)
            .merge(identity_topup)
            .merge(identity_checkin)
            .merge(identity_stripe_amount)
            .merge(identity_stripe_pay)
            .merge(identity_epay)
            .merge(identity_waffo)
            .merge(identity_security)
            .merge(verify_email)
            .merge(identity_federation_bindings)
            .merge(identity_federation_oauth_state)
            .merge(identity_federation_oauth_email_bind)
            .merge(identity_federation_oauth_external)
            .merge(identity_federation_oauth_login_start)
            .merge(identity_2fa)
            .merge(channel_core)
            .merge(channel_ops)
            .merge(channel_advanced)
            .merge(deployment)
            .merge(control_admin)
            .merge(assistant_reads)
            .merge(developer_access)
            .merge(account_action)
            .merge(hero_sms)
            .merge(finance_export)
            .merge(finance)
            .merge(release_notes)
            .merge(security_overview)
            .merge(security_admin)
            .merge(unified_todo)
            .merge(user_assistant_admin)
            .merge(access_ip)
            .merge(user_rankings)
            .merge(gifts)
            .merge(discount_code)
            .merge(dynamic_pricing)
            .merge(subscription_balance_pay)
            .merge(billing_provider_payments)
            .merge(billing_webhooks)
            .merge(kling_task_reads)
            .merge(billing_subscriptions)
            .merge(billing_dashboard)
            .merge(control_tasks)
            .merge(ratio_sync)
            .merge(observability_read)
            .merge(observability_metrics)
            .merge(observability_disk_cache)
            .merge(observability_performance)
            .merge(observability_force_gc)
            .merge(open_source_bounties)
            .merge(public_relays)
            .merge(relay_openai)
            .merge(relay_midjourney)
            .merge(relay_media_tasks)
            .merge(relay_video)
            .merge(relay_misc_new)
            .merge(responses_websocket)
            .merge(mcp)
            .merge(relay_anthropic_gemini)
            .merge(relay_media)
            .merge(relay_misc_active)
            .merge(relay_misc_frozen)
            .merge(control_public)
            .merge(missing_control_public)
            .merge(system_config);
        http::router_with_api_token_and_extra(
            app_state,
            auth_http,
            models_http,
            Some(api_token.with_current_dashboard_discovery_policy(
                Arc::clone(&models_service) as Arc<dyn lmm_api_rs::models::ModelsService>
            )),
            Some(extra_surface),
        )
    };
    let server = axum::serve(
        listener,
        router.into_make_service_with_connect_info::<std::net::SocketAddr>(),
    )
    .with_graceful_shutdown(wait_for_shutdown(shutdown_rx))
    .into_future();
    tokio::pin!(server);
    tokio::select! {
        result = &mut server => result?,
        () = shutdown_signal() => {
            runtime.begin_drain();
            tracing::info!(
                slot = %config.slot,
                inflight = runtime.inflight(),
                drain_timeout_seconds = config.drain_timeout.as_secs(),
                "shutdown requested; readiness closed, listener closing and in-flight requests draining"
            );
            let _ = shutdown_tx.send(true);
            bounded_drain(config.drain_timeout, &mut server).await??;
            tracing::info!(slot = %config.slot, remaining_inflight = runtime.inflight(), "slot drain completed");
        }
    }
    Ok(())
}
async fn bounded_drain<F, T>(timeout: std::time::Duration, drain: F) -> io::Result<T>
where
    F: std::future::Future<Output = T>,
{
    tokio::time::timeout(timeout, drain)
        .await
        .map_err(|_| io::Error::new(io::ErrorKind::TimedOut, "in-flight request drain timed out"))
}
async fn wait_for_shutdown(mut receiver: watch::Receiver<bool>) {
    while !*receiver.borrow_and_update() {
        if receiver.changed().await.is_err() {
            break;
        }
    }
}
async fn shutdown_signal() {
    let terminate = async {
        #[cfg(unix)]
        if let Ok(mut signal) =
            tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        {
            signal.recv().await;
        }
    };
    tokio::select! { result = tokio::signal::ctrl_c() => { if let Err(error) = result { tracing::error!(%error, "failed to install ctrl-c handler"); } } () = terminate => {} }
}

#[cfg(test)]
mod tests {
    use super::bounded_drain;
    use std::{future, io, time::Duration};

    #[tokio::test]
    async fn bounded_drain_should_complete_finished_work() {
        let result = bounded_drain(Duration::from_secs(1), future::ready(7)).await;
        assert_eq!(result.ok(), Some(7));
    }

    #[tokio::test]
    async fn bounded_drain_should_time_out_stuck_work() {
        let result = bounded_drain(Duration::from_millis(1), future::pending::<()>()).await;
        assert_eq!(
            result.err().map(|error| error.kind()),
            Some(io::ErrorKind::TimedOut)
        );
    }
}
