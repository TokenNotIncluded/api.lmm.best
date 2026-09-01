use std::{
    collections::BTreeSet,
    fs,
    path::{Path, PathBuf},
    process::Command,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
};

use async_trait::async_trait;
use axum::{
    body::Body,
    http::{Request, StatusCode},
    response::Response,
};
use lmm_api_rs::routes::relay_media::{RelayMediaHttpState, RelayMediaService, relay_media_router};
use tower::ServiceExt;

fn manifest_dir() -> PathBuf {
    PathBuf::from(env!("CARGO_MANIFEST_DIR"))
}

const NON_ROUTE_MODULES: &[&str] = &["legacy_http", "mod", "test_support"];

fn rust_stems(directory: &Path) -> BTreeSet<String> {
    fs::read_dir(directory)
        .expect("route directory")
        .map(|entry| entry.expect("route entry").path())
        .filter(|path| path.extension().is_some_and(|extension| extension == "rs"))
        .filter_map(|path| path.file_stem()?.to_str().map(str::to_owned))
        .collect()
}

fn router_constructors(source: &str) -> BTreeSet<&str> {
    source
        .lines()
        .filter_map(|line| line.trim().strip_prefix("pub fn "))
        .filter_map(|signature| signature.split_once('(').map(|(name, _)| name))
        .filter(|name| *name == "routes" || name.ends_with("router"))
        .collect()
}

/// These slices are composed by the isolated test-instance root instead of a
/// one-module-per-file integration test.  That is deliberate: several own
/// overlapping legacy paths and must be merged with their real neighbours to
/// prove Axum accepts the complete candidate surface.  Keep the constructor
/// marker beside the module so adding a new composed slice cannot silently
/// bypass this inventory gate.
const TEST_INSTANCE_COMPOSED_MODULES: &[(&str, &str)] = &[
    ("billing_dashboard", "billing_dashboard_router"),
    ("waffo_webhooks", "waffo_webhooks_router"),
    ("public_catalog", "public_catalog_router"),
    ("ratio_sync", "ratio_sync_router"),
    ("control_tasks", "control_tasks_router"),
    ("identity_catalog", "identity_catalog_router"),
    ("checkin_affiliate", "checkin_affiliate_router"),
    ("epay", "epay_router"),
    ("stripe_creem", "stripe_creem_router"),
    ("topup", "topup_router"),
    ("waffo", "waffo_router"),
    ("relay_compat", "relay_compat_router"),
    // `router_with_model_lookup` installs this module's GET method router on
    // the shared `/v1/models/{model}` route, alongside the relay POST/DELETE
    // methods. Mounting its standalone router would create an Axum conflict.
    ("model_lookup", "router_with_model_lookup"),
    ("relay_video", "relay_video_router"),
    // The legacy generic relay paths are intentionally test-instance-only
    // until their provider boundary obtains independent production approval.
    ("relay_misc", "relay_misc_routes"),
    // The relay-misc composite factory delegates to these two production
    // ownership slices, so the same candidate-root test exercises both.
    ("relay_misc_active", "relay_misc_routes"),
    ("relay_misc_frozen", "relay_misc_routes"),
];

/// Some route modules provide a production dependency boundary rather than an
/// HTTP route constructor. They still need a direct integration test so they
/// cannot silently disappear from the route inventory.
const ADAPTER_ONLY_MODULES: &[&str] = &["relay_anthropic_gemini_postgres", "sse"];

#[test]
fn every_route_module_should_have_a_router_integration_test() {
    let root = manifest_dir();
    let modules_dir = root.join("src/routes");
    let tests_dir = root.join("tests");
    let modules = rust_stems(&modules_dir)
        .into_iter()
        .filter(|name| !NON_ROUTE_MODULES.contains(&name.as_str()))
        .collect::<BTreeSet<_>>();
    let directly_tested_modules = rust_stems(&tests_dir)
        .into_iter()
        .filter(|name| modules.contains(name))
        .collect::<BTreeSet<_>>();
    let composed_modules = TEST_INSTANCE_COMPOSED_MODULES
        .iter()
        .map(|(module, _)| (*module).to_owned())
        .collect::<BTreeSet<_>>();
    let tested_modules = directly_tested_modules
        .union(&composed_modules)
        .cloned()
        .collect::<BTreeSet<_>>();

    assert_eq!(tested_modules, modules, "route module/test inventory drift");

    let test_instance = fs::read_to_string(root.join("src/test_instance.rs"))
        .expect("test-instance composition source");
    assert!(
        test_instance.contains("fn safe_candidate_surface"),
        "test-instance composition no longer exposes the candidate root"
    );

    for module in &modules {
        let source =
            fs::read_to_string(modules_dir.join(format!("{module}.rs"))).expect("route source");
        let constructors = router_constructors(&source);
        let direct_test = tests_dir.join(format!("{module}.rs"));
        if constructors.is_empty() && ADAPTER_ONLY_MODULES.contains(&module.as_str()) {
            assert!(
                direct_test.is_file(),
                "{module} is missing its adapter test"
            );
            continue;
        }
        assert!(
            !constructors.is_empty(),
            "{module} exposes no public router constructor"
        );
        let composed_marker = TEST_INSTANCE_COMPOSED_MODULES
            .iter()
            .find_map(|(covered_module, marker)| (*covered_module == module).then_some(*marker));

        if let Some(marker) = composed_marker {
            assert!(
                test_instance.contains(marker),
                "test-instance no longer composes {module} through {marker}"
            );
            continue;
        }

        let test = fs::read_to_string(direct_test).expect("route test");
        for constructor in constructors {
            assert!(
                test.contains(constructor),
                "{module}.rs does not exercise {constructor}"
            );
        }
    }
}

#[test]
fn all_candidate_route_shapes_should_pass_the_repository_gate() {
    let root = manifest_dir();
    let output = Command::new("bash")
        .arg(root.join("tests/scripts/check-draft-route-coverage.sh"))
        .current_dir(&root)
        .output()
        .expect("route coverage gate");
    assert!(
        output.status.success(),
        "route coverage gate failed:\n{}\n{}",
        String::from_utf8_lossy(&output.stdout),
        String::from_utf8_lossy(&output.stderr)
    );
}

struct RejectingMediaService {
    calls: AtomicUsize,
}

#[async_trait]
impl RelayMediaService for RejectingMediaService {
    async fn relay(&self, _: axum::extract::Request) -> Response {
        self.calls.fetch_add(1, Ordering::SeqCst);
        Response::builder()
            .status(StatusCode::UNAUTHORIZED)
            .body(Body::empty())
            .expect("auth rejection")
    }
}

#[tokio::test]
async fn relay_media_authenticates_before_inspecting_a_malformed_body() {
    let service = Arc::new(RejectingMediaService {
        calls: AtomicUsize::new(0),
    });
    let app = relay_media_router(RelayMediaHttpState::new(service.clone()));
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/images/generations")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("malformed media request"),
        )
        .await
        .expect("media response");

    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    // Frozen Go routing applies TokenAuth before Distribute parses a model.
    // The Rust HTTP boundary therefore rejects an absent bearer credential
    // without consuming malformed JSON or entering the relay service.
    assert_eq!(service.calls.load(Ordering::SeqCst), 0);

    let opaque_request_response = app
        .oneshot(
            Request::builder()
                .method("POST")
                .uri("/v1/images/generations")
                .header("authorization", "Bearer opaque-token")
                .header("content-type", "application/json")
                .body(Body::from("{"))
                .expect("opaque malformed media request"),
        )
        .await
        .expect("media response");

    assert_eq!(opaque_request_response.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(service.calls.load(Ordering::SeqCst), 1);
}
