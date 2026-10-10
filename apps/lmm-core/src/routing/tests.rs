use std::sync::{Arc, Barrier};
use std::thread;
use std::time::Instant;

use rand::{SeedableRng, rngs::StdRng};
use sqlx::PgPool;

use super::store::{PostgresRoutingStore, ReleaseDocument, StoreError};
use super::*;

fn fixture(version: u64) -> (RoutingConfig, PriceConfig) {
    let speeds = vec![Speed::Standard, Speed::Fast, Speed::Ultrafast];
    let config = RoutingConfig {
        version,
        groups: vec![ModelGroup {
            name: "default".into(),
            enabled: true,
        }],
        upstreams: (1..=2)
            .map(|id| UpstreamConfig {
                id,
                endpoint: format!("https://upstream-{id}.invalid/v1")
                    .try_into()
                    .unwrap(),
                protocol: Protocol::OpenAiChat,
                credential: CredentialRef {
                    id: id * 100,
                    revision: 1,
                },
                enabled: true,
            })
            .collect(),
        routes: vec![ModelRoute {
            id: 1,
            model: "model-a".into(),
            group: "default".into(),
            enabled: true,
            speeds: speeds.clone(),
            targets: (1..=2)
                .map(|id| RouteTarget {
                    id,
                    upstream_id: id,
                    upstream_model: "model-a".into(),
                    enabled: true,
                    priority: 0,
                    weight: if id == 1 { 1 } else { 3 },
                    speeds: speeds.clone(),
                    payload: PayloadPolicy::Adapt,
                    timeout_ms: 30_000,
                })
                .collect(),
            failover: FailoverPolicy {
                max_attempts: 2,
                retry_on: vec![
                    RetryCause::Connect,
                    RetryCause::Timeout,
                    RetryCause::RateLimited,
                    RetryCause::ServerError,
                ],
                allow_replay_after_send: false,
            },
        }],
        aliases: vec![ModelAlias {
            group: "default".into(),
            alias: "preferred".into(),
            model: "model-a".into(),
        }],
    };
    let prices = PriceConfig {
        version,
        rules: speeds
            .into_iter()
            .enumerate()
            .map(|(index, speed)| PriceRule {
                id: index as u64 + 1,
                model: "model-a".into(),
                group: "default".into(),
                speed,
                rates: TokenRates {
                    input: 3_000_000,
                    output: 12_000_000,
                    cache_read: 300_000,
                    cache_write: 3_750_000,
                    request: 10,
                },
                group_multiplier_ppm: MULTIPLIER_ONE,
                speed_multiplier_ppm: MULTIPLIER_ONE * (index as u32 + 1),
            })
            .collect(),
    };
    (config, prices)
}

fn build(config: RoutingConfig, prices: PriceConfig) -> Router {
    Router::new(config, Arc::new(PriceSnapshot::compile(prices).unwrap())).unwrap()
}

fn request() -> RouteRequest<'static> {
    RouteRequest {
        model: "model-a",
        group: "default",
        speed: None,
        protocol: Protocol::OpenAiChat,
        replay: ReplayPermission::Denied,
    }
}

fn failure() -> Failure {
    Failure {
        kind: FailureKind::Connect,
        delivery: DeliveryState::NotSent,
    }
}

fn invalid(config: RoutingConfig, prices: PriceConfig) {
    let result =
        PriceSnapshot::compile(prices).and_then(|prices| Router::new(config, Arc::new(prices)));
    assert!(result.is_err());
}

#[test]
fn seeded_weights_match_configured_ratio_without_replacement() {
    let (config, prices) = fixture(1);
    let router = build(config, prices);
    let mut rng = StdRng::seed_from_u64(0x4c4d4d);
    let mut count = [0_u64; 2];
    for _ in 0..40_000 {
        let mut plan = router.route_with_rng(request(), &mut rng).unwrap();
        let first = plan.start().unwrap();
        count[first.upstream_id() as usize - 1] += 1;
        let second = plan.retry(failure()).unwrap();
        assert_ne!(first.upstream_id(), second.upstream_id());
        assert_eq!(first.quote(), second.quote());
        assert_eq!(first.version(), second.version());
        assert_eq!(second.canonical_model(), "model-a");
        assert_eq!(
            plan.retry(failure()).unwrap_err(),
            RoutingError::AttemptsExhausted
        );
    }
    // Fixed-seed algorithm regression, not a benchmark or a production SLA.
    assert!((9_400..=10_600).contains(&count[0]), "counts={count:?}");
}

#[test]
fn priorities_are_strict_and_failover_reaches_only_the_pinned_backup() {
    let (mut config, prices) = fixture(1);
    config.routes[0].targets[0].weight = 1;
    config.routes[0].targets[1].weight = u32::MAX;
    config.routes[0].targets[1].priority = 1;
    let router = build(config, prices);
    for _ in 0..100 {
        let mut plan = router.route(request()).unwrap();
        assert_eq!(plan.start().unwrap().upstream_id(), 1);
        assert_eq!(plan.retry(failure()).unwrap().upstream_id(), 2);
    }
}

#[test]
fn disabled_upstream_target_zero_weight_and_health_are_excluded() {
    for variant in 0..5 {
        let (mut config, prices) = fixture(1);
        match variant {
            0 => config.upstreams[0].enabled = false,
            1 => config.routes[0].targets[0].enabled = false,
            2 => config.routes[0].targets[0].weight = 0,
            _ => {}
        }
        let router = build(config, prices);
        let version = router.version().unwrap();
        if variant == 3 {
            router
                .set_health(version, 1, HealthStatus::Unhealthy)
                .unwrap();
        }
        if variant == 4 {
            router
                .set_health(version, 1, HealthStatus::Unknown)
                .unwrap();
        }
        for _ in 0..100 {
            assert_eq!(
                router
                    .route(request())
                    .unwrap()
                    .start()
                    .unwrap()
                    .upstream_id(),
                2
            );
        }
        router
            .set_health(version, 2, HealthStatus::Unhealthy)
            .unwrap();
        assert_eq!(
            router.route(request()).unwrap_err(),
            RoutingError::NoEligibleUpstream
        );
    }
}

#[test]
fn alias_group_and_speed_resolution_are_explicit() {
    let (mut config, prices) = fixture(1);
    config.routes[0].targets[0].speeds = vec![Speed::Standard, Speed::Fast];
    config.routes[0].targets[1].speeds = vec![Speed::Standard, Speed::Ultrafast];
    let router = build(config, prices);
    let fast = RouteRequest {
        model: "preferred/fast",
        ..request()
    };
    let mut plan = router.route(fast).unwrap();
    assert_eq!(plan.canonical_model(), "model-a");
    assert_eq!(plan.requested_model(), "preferred/fast");
    assert_eq!(plan.speed(), Speed::Fast);
    assert_eq!(plan.start().unwrap().upstream_id(), 1);
    assert_eq!(
        plan.quote().multipliers_ppm(),
        (MULTIPLIER_ONE, 2 * MULTIPLIER_ONE)
    );
    let mut ultra = router
        .route(RouteRequest {
            model: "model-a/ultrafast",
            ..request()
        })
        .unwrap();
    assert_eq!(ultra.start().unwrap().upstream_id(), 2);
    assert_eq!(
        router
            .route(RouteRequest {
                speed: Some(Speed::Ultrafast),
                ..fast
            })
            .unwrap_err(),
        RoutingError::ConflictingSpeed
    );
    assert_eq!(
        router
            .route(RouteRequest {
                group: "private",
                ..request()
            })
            .unwrap_err(),
        RoutingError::UnknownGroup
    );
    assert_eq!(
        router
            .route(RouteRequest {
                model: "other-model",
                ..request()
            })
            .unwrap_err(),
        RoutingError::UnknownModel
    );
}

#[test]
fn passthrough_never_silently_rewrites_model_or_protocol() {
    let (mut config, prices) = fixture(1);
    for target in &mut config.routes[0].targets {
        target.payload = PayloadPolicy::Passthrough;
    }
    let router = build(config, prices);
    let attempt = router
        .route(RouteRequest {
            speed: Some(Speed::Fast),
            ..request()
        })
        .unwrap()
        .start()
        .unwrap();
    assert_eq!(attempt.payload_policy(), PayloadPolicy::Passthrough);
    assert_eq!(attempt.upstream_model(), "model-a");
    for req in [
        RouteRequest {
            model: "preferred",
            ..request()
        },
        RouteRequest {
            model: "model-a/fast",
            ..request()
        },
        RouteRequest {
            protocol: Protocol::AnthropicMessages,
            ..request()
        },
    ] {
        assert_eq!(
            router.route(req).unwrap_err(),
            RoutingError::NoEligibleUpstream
        );
    }
}

#[test]
fn disabled_group_route_and_speed_fail_closed() {
    let (mut config, prices) = fixture(1);
    config.groups[0].enabled = false;
    assert_eq!(
        build(config, prices).route(request()).unwrap_err(),
        RoutingError::GroupDisabled
    );
    let (mut config, prices) = fixture(1);
    config.routes[0].enabled = false;
    let router = build(config, prices);
    assert_eq!(
        router.route(request()).unwrap_err(),
        RoutingError::RouteDisabled
    );
    assert!(router.catalog("default").unwrap().is_empty());
    let (mut config, prices) = fixture(1);
    config.routes[0].speeds = vec![Speed::Standard];
    for target in &mut config.routes[0].targets {
        target.speeds = vec![Speed::Standard];
    }
    assert_eq!(
        build(config, prices)
            .route(RouteRequest {
                speed: Some(Speed::Fast),
                ..request()
            })
            .unwrap_err(),
        RoutingError::SpeedDisabled
    );
}

#[test]
fn fixed_point_prices_sum_disjoint_usage_and_round_once() {
    let (_, prices) = fixture(1);
    let snapshot = PriceSnapshot::compile(prices).unwrap();
    let usage = Usage {
        input: 1_000_000,
        output: 10,
        cache_read: 100,
        cache_write: 4,
    };
    assert_eq!(
        snapshot
            .quote("model-a", "default", Speed::Standard)
            .unwrap()
            .charge(usage)
            .unwrap(),
        MicroUsd(3_000_175)
    );
    assert_eq!(
        snapshot
            .quote("model-a", "default", Speed::Fast)
            .unwrap()
            .charge(usage)
            .unwrap(),
        MicroUsd(6_000_350)
    );
    let (_, mut prices) = fixture(1);
    for rule in &mut prices.rules {
        rule.rates = TokenRates {
            input: 250_000,
            output: 250_000,
            cache_read: 0,
            cache_write: 0,
            request: 0,
        };
    }
    let snapshot = PriceSnapshot::compile(prices).unwrap();
    let quote = snapshot
        .quote("model-a", "default", Speed::Standard)
        .unwrap();
    assert_eq!(
        quote
            .charge(Usage {
                input: 1,
                output: 1,
                ..Usage::default()
            })
            .unwrap(),
        MicroUsd(1)
    );
    assert_eq!(quote.charge(Usage::default()).unwrap(), MicroUsd(0));
}

#[test]
fn price_overflow_is_an_error_never_a_saturated_or_negative_charge() {
    let (_, mut prices) = fixture(1);
    prices.rules[0].rates.input = i64::MAX as u64;
    prices.rules[0].group_multiplier_ppm = MAX_MULTIPLIER;
    prices.rules[0].speed_multiplier_ppm = MAX_MULTIPLIER;
    let snapshot = PriceSnapshot::compile(prices).unwrap();
    assert_eq!(
        snapshot
            .quote("model-a", "default", Speed::Standard)
            .unwrap()
            .charge(Usage {
                input: u64::MAX,
                ..Usage::default()
            }),
        Err(RoutingError::PriceOverflow)
    );
}

#[test]
fn publication_is_atomic_monotonic_and_does_not_change_in_flight_prices() {
    let (config, prices) = fixture(1);
    let router = build(config, prices.clone());
    let mut old = router.route(request()).unwrap();
    let first = old.start().unwrap();
    let expected = router.version().unwrap();
    let (config, mut next_prices) = fixture(2);
    next_prices.rules[0].rates.input = 9_000_000;
    router
        .publish(
            expected,
            config,
            Arc::new(PriceSnapshot::compile(next_prices).unwrap()),
        )
        .unwrap();
    let second = old.retry(failure()).unwrap();
    assert_eq!(first.quote(), second.quote());
    assert_eq!(old.quote().rates().input, 3_000_000);
    let current = router.route(request()).unwrap();
    assert_eq!(
        current.version(),
        ReleaseVersion {
            routing: 2,
            pricing: 2
        }
    );
    assert_eq!(current.quote().rates().input, 9_000_000);
    let (config, _) = fixture(3);
    assert_eq!(
        router.publish(
            expected,
            config,
            Arc::new(PriceSnapshot::compile(prices).unwrap())
        ),
        Err(RoutingError::VersionConflict)
    );
}

#[test]
fn invalid_publish_preserves_last_good_config_and_reused_price_versions_are_rejected() {
    let (config, prices) = fixture(1);
    let router = build(config, prices.clone());
    let version = router.version().unwrap();
    let (mut config, _) = fixture(2);
    let mut changed_prices = prices.clone();
    changed_prices.rules[0].rates.input += 1;
    assert_eq!(
        router.publish(
            version,
            config.clone(),
            Arc::new(PriceSnapshot::compile(changed_prices).unwrap())
        ),
        Err(RoutingError::PriceVersionConflict)
    );
    config.routes[0].targets[0].upstream_id = 999;
    assert!(
        router
            .publish(
                version,
                config,
                Arc::new(PriceSnapshot::compile(prices.clone()).unwrap())
            )
            .is_err()
    );
    assert_eq!(router.version().unwrap(), version);
    let (config, _) = fixture(2);
    router
        .publish(
            version,
            config,
            Arc::new(PriceSnapshot::compile(prices).unwrap()),
        )
        .unwrap();
    assert_eq!(
        router.version().unwrap(),
        ReleaseVersion {
            routing: 2,
            pricing: 1
        }
    );
}

#[test]
fn only_one_simultaneous_publisher_can_win() {
    let (config, prices) = fixture(1);
    let router = Arc::new(build(config, prices));
    let expected = router.version().unwrap();
    let barrier = Arc::new(Barrier::new(3));
    let handles: Vec<_> = (2..=3)
        .map(|version| {
            let router = Arc::clone(&router);
            let barrier = Arc::clone(&barrier);
            thread::spawn(move || {
                let (config, prices) = fixture(version);
                let prices = Arc::new(PriceSnapshot::compile(prices).unwrap());
                barrier.wait();
                router.publish(expected, config, prices)
            })
        })
        .collect();
    barrier.wait();
    let results: Vec<_> = handles
        .into_iter()
        .map(|handle| handle.join().unwrap())
        .collect();
    assert_eq!(results.iter().filter(|r| r.is_ok()).count(), 1);
    assert_eq!(
        results
            .iter()
            .filter(|r| matches!(r, Err(RoutingError::VersionConflict)))
            .count(),
        1
    );
}

#[test]
fn concurrent_readers_never_observe_mixed_route_and_price_versions() {
    let (config, prices) = fixture(1);
    let router = Arc::new(build(config, prices));
    let barrier = Arc::new(Barrier::new(5));
    let readers: Vec<_> = (0..4)
        .map(|_| {
            let router = Arc::clone(&router);
            let barrier = Arc::clone(&barrier);
            thread::spawn(move || {
                barrier.wait();
                for _ in 0..5_000 {
                    let mut plan = router.route(request()).unwrap();
                    let version = plan.version();
                    assert_eq!(version.routing, version.pricing);
                    assert_eq!(plan.quote().version(), version.pricing);
                    assert_eq!(plan.start().unwrap().version(), version);
                    assert_eq!(plan.retry(failure()).unwrap().version(), version);
                }
            })
        })
        .collect();
    barrier.wait();
    for next in 2..=100 {
        let (config, prices) = fixture(next);
        router
            .publish(
                router.version().unwrap(),
                config,
                Arc::new(PriceSnapshot::compile(prices).unwrap()),
            )
            .unwrap();
    }
    for reader in readers {
        reader.join().unwrap();
    }
}

#[test]
fn unsafe_replay_partial_output_and_terminal_errors_cannot_be_bypassed() {
    let (config, prices) = fixture(1);
    let router = build(config, prices);
    for (report, expected) in [
        (
            Failure {
                kind: FailureKind::Timeout,
                delivery: DeliveryState::PossiblyAccepted,
            },
            RoutingError::UnsafeReplay,
        ),
        (
            Failure {
                kind: FailureKind::ServerError,
                delivery: DeliveryState::ResponseStarted,
            },
            RoutingError::ResponseAlreadyStarted,
        ),
        (
            Failure {
                kind: FailureKind::Authentication,
                delivery: DeliveryState::Rejected,
            },
            RoutingError::RetryDisallowed,
        ),
        (
            Failure {
                kind: FailureKind::ClientError,
                delivery: DeliveryState::Rejected,
            },
            RoutingError::RetryDisallowed,
        ),
        (
            Failure {
                kind: FailureKind::Cancelled,
                delivery: DeliveryState::NotSent,
            },
            RoutingError::RetryDisallowed,
        ),
        (
            Failure {
                kind: FailureKind::InvalidResponse,
                delivery: DeliveryState::PossiblyAccepted,
            },
            RoutingError::RetryDisallowed,
        ),
    ] {
        let mut plan = router.route(request()).unwrap();
        plan.start().unwrap();
        assert_eq!(plan.retry(report).unwrap_err(), expected);
        assert_eq!(
            plan.retry(failure()).unwrap_err(),
            RoutingError::InvalidTransition
        );
        assert_eq!(plan.start().unwrap_err(), RoutingError::InvalidTransition);
    }
}

#[test]
fn replay_after_send_needs_both_permissions_and_rejections_can_retry() {
    for (configured, requested, allowed) in [
        (false, false, false),
        (false, true, false),
        (true, false, false),
        (true, true, true),
    ] {
        let (mut config, prices) = fixture(1);
        config.routes[0].failover.allow_replay_after_send = configured;
        let router = build(config, prices);
        let mut plan = router
            .route(RouteRequest {
                replay: if requested {
                    ReplayPermission::ExplicitlyAllowed
                } else {
                    ReplayPermission::Denied
                },
                ..request()
            })
            .unwrap();
        plan.start().unwrap();
        assert_eq!(
            plan.retry(Failure {
                kind: FailureKind::Timeout,
                delivery: DeliveryState::PossiblyAccepted
            })
            .is_ok(),
            allowed
        );
        let mut plan = router.route(request()).unwrap();
        plan.start().unwrap();
        assert!(
            plan.retry(Failure {
                kind: FailureKind::RateLimited,
                delivery: DeliveryState::Rejected
            })
            .is_ok()
        );
    }
}

#[test]
fn default_policy_does_not_retry_and_plan_must_start_once() {
    let (mut config, prices) = fixture(1);
    config.routes[0].failover = FailoverPolicy::default();
    let mut plan = build(config, prices).route(request()).unwrap();
    assert_eq!(
        plan.retry(failure()).unwrap_err(),
        RoutingError::InvalidTransition
    );
    plan.start().unwrap();
    assert_eq!(plan.start().unwrap_err(), RoutingError::InvalidTransition);
    assert_eq!(
        plan.retry(failure()).unwrap_err(),
        RoutingError::RetryDisallowed
    );
}

#[test]
fn operational_stop_survives_health_reports_and_route_only_publication() {
    let (config, prices) = fixture(1);
    let router = build(config, prices.clone());
    let mut plan = router.route(request()).unwrap();
    let first = plan.start().unwrap();
    let other = if first.upstream_id() == 1 { 2 } else { 1 };
    let expected = router.version().unwrap();
    router
        .set_operational_enabled(expected, other, false)
        .unwrap();
    router
        .set_health(expected, other, HealthStatus::Healthy)
        .unwrap();
    let (config, _) = fixture(2);
    router
        .publish(
            expected,
            config,
            Arc::new(PriceSnapshot::compile(prices).unwrap()),
        )
        .unwrap();
    assert_eq!(
        plan.retry(failure()).unwrap_err(),
        RoutingError::AttemptsExhausted
    );
    assert_eq!(
        router.set_health(expected, other, HealthStatus::Healthy),
        Err(RoutingError::VersionConflict)
    );
    for _ in 0..50 {
        assert_eq!(
            router
                .route(request())
                .unwrap()
                .start()
                .unwrap()
                .upstream_id(),
            first.upstream_id()
        );
    }
}

#[test]
fn old_failures_do_not_poison_a_replaced_credential_or_endpoint() {
    let (config, prices) = fixture(1);
    let router = build(config, prices);
    let old = router.route(request()).unwrap().start().unwrap();
    let (mut config, prices) = fixture(2);
    for upstream in &mut config.upstreams {
        upstream.credential.revision = 2;
        upstream.endpoint = format!("https://replacement-{}.invalid", upstream.id)
            .try_into()
            .unwrap();
    }
    router
        .publish(
            router.version().unwrap(),
            config,
            Arc::new(PriceSnapshot::compile(prices).unwrap()),
        )
        .unwrap();
    old.mark_unhealthy();
    let new = router.route(request()).unwrap().start().unwrap();
    assert_eq!(new.credential_ref().revision, 2);
    assert_eq!(old.credential_ref().revision, 1);
    assert!(new.endpoint().contains("replacement"));
    assert!(!old.endpoint().contains("replacement"));
}

#[test]
fn malformed_duplicate_and_unpriced_configuration_is_rejected() {
    for variant in 0..11 {
        let (mut config, mut prices) = fixture(1);
        match variant {
            0 => config.routes.push(config.routes[0].clone()),
            1 => config.aliases[0].model = "preferred".into(),
            2 => config.aliases[0].alias = "model-a".into(),
            3 => config.routes[0].targets[0].upstream_id = 999,
            4 => config.routes[0].failover.max_attempts = 9,
            5 => config.upstreams[0].credential.revision = 0,
            6 => {
                prices.rules.pop();
            }
            7 => prices.rules.push(prices.rules[0].clone()),
            8 => prices.rules[0].speed_multiplier_ppm = 0,
            9 => config.routes[0].targets[1].upstream_id = 1,
            10 => config.routes[0].model = "model-a/fast".into(),
            _ => unreachable!(),
        }
        invalid(config, prices);
    }
}

#[test]
fn credential_values_and_endpoint_secrets_cannot_enter_logs_or_public_config() {
    for endpoint in [
        "https://user:sk-secret@example.invalid/v1",
        "https://example.invalid/v1?api_key=sk-secret",
        "https://example.invalid/v1#sk-secret",
        "http://example.invalid/v1",
        "https://example.invalid/\nsk-secret",
    ] {
        let error = Endpoint::try_from(endpoint.to_owned()).unwrap_err();
        assert!(!format!("{error:?}").contains("sk-secret"));
    }
    let (config, prices) = fixture(1);
    assert!(!format!("{config:?}").contains("upstream-1.invalid"));
    let router = build(config.clone(), prices.clone());
    let mut plan = router.route(request()).unwrap();
    let attempt = plan.start().unwrap();
    let debug = format!("{plan:?} {attempt:?} {:?}", attempt.credential_ref());
    let public = serde_json::to_string(&router.catalog("default").unwrap()).unwrap();
    for text in [&debug, &public] {
        assert!(!text.contains("upstream-1.invalid"));
        assert!(!text.contains("credential_id"));
        assert!(!text.contains("credential_revision"));
    }
    assert!(!public.contains("credential"));
    assert!(!public.contains("endpoint"));
    let document = ReleaseDocument {
        contract_version: CONTRACT_VERSION,
        routing: config,
        pricing: prices,
    };
    let mut value = serde_json::to_value(document).unwrap();
    value["routing"]["upstreams"][0]["api_key"] = "sk-secret".into();
    let error = ReleaseDocument::decode(&serde_json::to_vec(&value).unwrap()).unwrap_err();
    assert_eq!(error, StoreError::InvalidDocument);
    let error = StoreError::from(sqlx::Error::Protocol("sk-secret".into()));
    assert!(!format!("{error:?}").contains("sk-secret"));
}

#[test]
fn a_saved_release_recovers_without_go_or_any_live_control_plane() {
    let (config, prices) = fixture(1);
    let bytes = ReleaseDocument {
        contract_version: CONTRACT_VERSION,
        routing: config,
        pricing: prices,
    }
    .encode()
    .unwrap();
    let recovered = ReleaseDocument::decode(&bytes)
        .unwrap()
        .into_router()
        .unwrap();
    let mut plan = recovered.route(request()).unwrap();
    // Scripted upstream one returns a transport failure; the approved peer succeeds.
    let first = plan.start().unwrap();
    first.mark_unhealthy();
    let successful = plan.retry(failure()).unwrap();
    assert_ne!(first.upstream_id(), successful.upstream_id());
    assert_eq!(first.canonical_model(), successful.canonical_model());
    assert_eq!(successful.quote().version(), 1);
}

async fn database(pool: &PgPool) -> PostgresRoutingStore {
    sqlx::raw_sql(include_str!("../../schema/routing.sql"))
        .execute(pool)
        .await
        .unwrap();
    let store = PostgresRoutingStore::new(pool.clone());
    for id in [100, 200] {
        store
            .register_credential_reference(CredentialRef { id, revision: 1 })
            .await
            .unwrap();
    }
    store
}

#[sqlx::test]
async fn postgres_publish_recover_and_sealed_rows_are_immutable(pool: PgPool) {
    let store = database(&pool).await;
    assert_eq!(
        store.load_active().await.unwrap_err(),
        StoreError::NotConfigured
    );
    let (config, prices) = fixture(1);
    store.stage_prices(prices.clone()).await.unwrap();
    store.stage_prices(prices.clone()).await.unwrap();
    let published = store.publish(config, 1, None).await.unwrap();
    let recovered = store.load_active().await.unwrap();
    assert_eq!(published, recovered);
    let router = recovered.into_router().unwrap();
    assert!(router.route(request()).unwrap().start().is_ok());
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM routing_targets")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
    for query in [
        "UPDATE routing_price_rules SET input_rate = 0 WHERE version = 1",
        "UPDATE routing_upstreams SET endpoint = 'https://changed.invalid' WHERE version = 1",
        "UPDATE routing_config_versions SET sealed = false WHERE version = 1",
        "UPDATE routing_price_versions SET document = '{}'::jsonb WHERE version = 1",
        "DELETE FROM routing_targets WHERE version = 1",
        "INSERT INTO routing_groups VALUES (1, 'late-group', true)",
        "UPDATE routing_active_release SET config_version = NULL WHERE singleton",
        "DELETE FROM routing_active_release",
    ] {
        assert!(
            sqlx::query(query).execute(&pool).await.is_err(),
            "mutation unexpectedly accepted"
        );
    }
    let mut changed = prices;
    changed.rules[0].rates.input += 1;
    assert_eq!(
        store.stage_prices(changed).await.unwrap_err(),
        StoreError::VersionAlreadyExists
    );
    assert_eq!(store.load_active().await.unwrap().routing.version, 1);
}

#[sqlx::test]
async fn postgres_independent_price_staging_and_compare_and_swap(pool: PgPool) {
    let store = database(&pool).await;
    let (config, prices) = fixture(1);
    store.stage_prices(prices).await.unwrap();
    store.publish(config, 1, None).await.unwrap();
    let (config2, prices2) = fixture(2);
    let (config3, _) = fixture(3);
    store.stage_prices(prices2).await.unwrap();
    assert_eq!(store.load_active().await.unwrap().pricing.version, 1);
    let expected = Some(ReleaseVersion {
        routing: 1,
        pricing: 1,
    });
    let (a, b) = tokio::join!(
        store.publish(config2, 2, expected),
        store.publish(config3, 2, expected)
    );
    assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
    assert!(
        matches!(a, Err(StoreError::VersionConflict))
            || matches!(b, Err(StoreError::VersionConflict))
    );
    let active = store.load_active().await.unwrap();
    assert_eq!(active.pricing.version, 2);
    let active_version = ReleaseVersion {
        routing: active.routing.version,
        pricing: active.pricing.version,
    };
    let (mut bad, _) = fixture(active_version.routing + 1);
    bad.upstreams[0].credential.revision = 999;
    assert_eq!(
        store
            .publish(bad, 2, Some(active_version))
            .await
            .unwrap_err(),
        StoreError::Database
    );
    let current = store.load_active().await.unwrap();
    assert_eq!(current, active);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM routing_config_versions")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2, "failed publication left a partial snapshot");
}

/// Reproducible, opt-in ROUTING-ONLY benchmark. No HTTP, database, or billing claims.
#[test]
#[ignore = "run explicitly with --release and --nocapture; see routing/README.md"]
fn routing_benchmark() {
    // Keep this runtime guard so debug builds can still compile the ignored benchmark.
    assert!(
        !std::hint::black_box(cfg!(debug_assertions)),
        "benchmark requires --release"
    );
    fn parameter(name: &str, default: usize, max: usize) -> usize {
        let value = std::env::var(name)
            .ok()
            .map(|s| s.parse::<usize>().expect("integer parameter"))
            .unwrap_or(default);
        assert!((1..=max).contains(&value), "invalid benchmark parameter");
        value
    }
    let requests = parameter("ROUTING_BENCH_REQUESTS", 1_000_000, 100_000_000);
    let threads = parameter("ROUTING_BENCH_THREADS", 4, 256).min(requests);
    let models = parameter("ROUTING_BENCH_MODELS", 1_000, 100_000);
    let (mut config, mut prices) = fixture(1);
    let template = config.routes[0].clone();
    let price_template = prices.rules.clone();
    config.aliases.clear();
    config.routes.clear();
    prices.rules.clear();
    let names: Arc<Vec<String>> =
        Arc::new((0..models).map(|index| format!("model-{index}")).collect());
    for (index, name) in names.iter().enumerate() {
        let mut route = template.clone();
        route.id = index as u64 + 1;
        route.model = name.clone();
        for (offset, target) in route.targets.iter_mut().enumerate() {
            target.id = (index * 2 + offset + 1) as u64;
            target.upstream_model = name.clone();
        }
        config.routes.push(route);
        for (offset, template) in price_template.iter().enumerate() {
            let mut price = template.clone();
            price.id = (index * 3 + offset + 1) as u64;
            price.model = name.clone();
            prices.rules.push(price);
        }
    }
    let router = Arc::new(build(config, prices));
    let barrier = Arc::new(Barrier::new(threads + 1));
    let ready = Arc::new(Barrier::new(threads + 1));
    let handles: Vec<_> = (0..threads)
        .map(|worker| {
            let router = Arc::clone(&router);
            let barrier = Arc::clone(&barrier);
            let names = Arc::clone(&names);
            let ready = Arc::clone(&ready);
            let count = requests / threads + usize::from(worker < requests % threads);
            thread::spawn(move || {
                // Warm up the thread-local RNG and instruction/data paths outside timing.
                for index in 0..1_000 {
                    std::hint::black_box(
                        router
                            .route(RouteRequest {
                                model: &names[index % models],
                                ..request()
                            })
                            .unwrap()
                            .start()
                            .unwrap(),
                    );
                }
                let mut samples = Vec::with_capacity(count / 100 + 1);
                ready.wait();
                barrier.wait();
                for index in 0..count {
                    let sampled = index % 100 == 0;
                    let started = sampled.then(Instant::now);
                    let mut plan = router
                        .route(RouteRequest {
                            model: &names[(index + worker) % models],
                            ..request()
                        })
                        .unwrap();
                    std::hint::black_box(plan.start().unwrap());
                    if let Some(started) = started {
                        samples.push(started.elapsed().as_nanos());
                    }
                }
                samples
            })
        })
        .collect();
    ready.wait();
    let start = Instant::now();
    barrier.wait();
    let mut samples = Vec::new();
    for handle in handles {
        samples.extend(handle.join().unwrap());
    }
    let elapsed = start.elapsed();
    samples.sort_unstable();
    let percentile = |p: usize| samples[(samples.len() - 1) * p / 100];
    println!(
        "routing-only; os={}; arch={}; threads={threads}; models={models}; targets=2; requests={requests}; elapsed_s={:.6}; requests_per_s={:.0}; sampled_p50_ns={}; sampled_p95_ns={}; sampled_p99_ns={}; samples={}",
        std::env::consts::OS,
        std::env::consts::ARCH,
        elapsed.as_secs_f64(),
        requests as f64 / elapsed.as_secs_f64(),
        percentile(50),
        percentile(95),
        percentile(99),
        samples.len()
    );
}
