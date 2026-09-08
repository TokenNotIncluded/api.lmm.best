use std::{
    os::unix::fs::symlink,
    sync::atomic::{AtomicUsize, Ordering},
};

use ed25519_dalek::{Signer, SigningKey};
use serde_json::{Value, json};

use super::*;

fn key() -> SigningKey {
    SigningKey::from_bytes(&[7; 32])
}

fn key_hex() -> String {
    hex::encode(key().verifying_key().to_bytes())
}

fn sign_raw(payload: &[u8]) -> Vec<u8> {
    serde_json::to_vec(&Envelope {
        format: 1,
        payload: STANDARD.encode(payload),
        signature: hex::encode(key().sign(payload).to_bytes()),
    })
    .unwrap()
}

fn sign(receipt: &Receipt) -> Vec<u8> {
    sign_raw(&serde_json::to_vec(receipt).unwrap())
}

fn now() -> DateTime<Utc> {
    DateTime::parse_from_rfc3339("2026-09-07T12:00:00Z")
        .unwrap()
        .with_timezone(&Utc)
}

fn workspace(root: PathBuf) -> Workspace {
    let state = root.join("state");
    Workspace {
        id: "signed-receipt-test".to_owned(),
        staging: root.join("staging"),
        manifest: state.join("deployment.json"),
        status: state.join("status.json"),
        probe_token: state.join("probe-token"),
        state,
        root,
    }
}

fn manifest(workspace: &Workspace) -> ProductionManifest {
    let transition = |name: &str| {
        json!({
            "candidate_package_name": name,
            "rollback_package_name": name,
            "changed": true,
            "candidate_path": workspace.staging.join(format!("{name}-new.pkg.tar.zst")),
            "rollback_path": workspace.staging.join(format!("{name}-old.pkg.tar.zst")),
            "candidate_identity": format!("{name} 0.2.14-1"),
            "rollback_identity": format!("{name} 0.2.13-1"),
            "candidate_sha256": "a".repeat(64),
            "rollback_sha256": "b".repeat(64),
            "candidate_git_revision": "a".repeat(40),
            "rollback_git_revision": "b".repeat(40),
            "candidate_contract_revision": "c".repeat(64),
            "rollback_contract_revision": "c".repeat(64),
        })
    };
    serde_json::from_value(json!({
        "format": 8,
        "deployment_id": workspace.id,
        "operator_user": "lmm-api-deploy",
        "go": transition("lmm-api-go-bin"),
        "web": transition("lmm-api-web-bin"),
        "frontend": {
            "old_target": "releases/old", "new_target": "releases/new",
            "old_index_sha256": "c".repeat(64), "new_index_sha256": "d".repeat(64),
        },
        "probe_binary": workspace.staging.join(GO_PROVIDER),
        "probe_binary_sha256": "d".repeat(64),
        "operator_binary": workspace.staging.join(GO_PROVIDER),
        "operator_binary_sha256": "d".repeat(64),
        "expected_version": "0.2.14", "old_version": "0.2.13",
        "previous_provider_target": GO_PROVIDER, "new_provider_target": GO_PROVIDER,
        "backup_dir": "", "backups_enabled": true, "backup_evidence_format": 3,
        "database_backup_sha256": "e".repeat(64),
        "target_backup_sha256": "", "controller_backup_sha256": "f".repeat(64),
        "offhost_backup_sha256": "",
        "controller_only_backup": {
            "public_key": key_hex(), "plan_sha256": "1".repeat(64),
            "receipt_path": workspace.state.join("controller-backup-receipt.json"),
            "receipt_sha256": "2".repeat(64),
        },
        "database_schema": "public", "observation_seconds": 120,
        "service_restart_baseline": 0,
        "config_restore_path": workspace.state.join("config-restore"),
        "environment_restore_sha256": "3".repeat(64),
    }))
    .unwrap()
}

fn receipt(manifest: &ProductionManifest) -> Receipt {
    Receipt {
        format: 1,
        purpose: "prepare".to_owned(),
        deployment_id: manifest.deployment_id.clone(),
        expected_host: EXPECTED_HOST.to_owned(),
        plan_sha256: manifest
            .controller_only_backup
            .as_ref()
            .unwrap()
            .plan_sha256
            .clone(),
        verified_utc: now(),
        captured_utc: now() - chrono::Duration::minutes(10),
        deployment_manifest_sha256: String::new(),
        backup_set_sha256: manifest.controller_backup_sha256.clone(),
        database_schema: manifest.database_schema.clone(),
        environment_sha256: manifest.environment_restore_sha256.clone(),
        go_candidate_sha256: manifest.go.candidate_sha256.clone(),
        go_rollback_sha256: manifest.go.rollback_sha256.clone(),
        web_candidate_sha256: manifest.web.candidate_sha256.clone(),
        web_rollback_sha256: manifest.web.rollback_sha256.clone(),
        go_rollback_payload_sha256: "4".repeat(64),
        frontend_rollback_sha256: manifest.frontend.old_index_sha256.clone(),
        archive_ciphertexts: ArchiveDigests {
            application: "5".repeat(64),
            frontend: "6".repeat(64),
            configuration: "7".repeat(64),
            database: "8".repeat(64),
        },
        archive_plaintexts: ArchiveDigests {
            application: "9".repeat(64),
            frontend: "a".repeat(64),
            configuration: "b".repeat(64),
            database: manifest.database_backup_sha256.clone(),
        },
    }
}

fn context() -> (Workspace, ProductionManifest, Receipt) {
    let workspace = workspace(Path::new(WORK_ROOT).join("signed-receipt-test"));
    let manifest = manifest(&workspace);
    let receipt = receipt(&manifest);
    (workspace, manifest, receipt)
}

fn confirmation(initial: &Receipt) -> Receipt {
    let mut confirmation = initial.clone();
    confirmation.purpose = "confirm".to_owned();
    confirmation.deployment_manifest_sha256 = "a".repeat(64);
    confirmation
}

#[test]
fn go_signed_receipt_and_immutable_manifest_are_compatible() {
    let Ok(root) = std::env::var("LMM_CONTROLLER_BACKUP_INTEROP_FIXTURE_DIR") else {
        return;
    };
    let root = PathBuf::from(root);
    let manifest: ProductionManifest =
        serde_json::from_slice(&fs::read(root.join("manifest.json")).unwrap()).unwrap();
    let evidence = manifest.controller_only_backup.as_ref().unwrap();
    let raw = fs::read(root.join("receipt.json")).unwrap();
    assert_eq!(sha256_bytes(&raw), evidence.receipt_sha256);
    let decoded = decode_receipt(&raw, &evidence.public_key).unwrap();
    validate_prepare(&decoded).unwrap();
    validate_bindings(&decoded, &manifest).unwrap();
}

#[test]
fn exact_signed_payload_bytes_accept_whitespace_without_reserialization() {
    let (_, manifest, receipt) = context();
    let pretty = serde_json::to_vec_pretty(&receipt).unwrap();
    let decoded = decode_receipt(&sign_raw(&pretty), &key_hex()).unwrap();
    assert!(validate_bindings(&decoded, &manifest).is_ok());
}

#[test]
fn whitespace_tampering_without_resigning_is_rejected() {
    let (_, _, receipt) = context();
    let mut envelope: Envelope = serde_json::from_slice(&sign(&receipt)).unwrap();
    let mut payload = STANDARD.decode(&envelope.payload).unwrap();
    payload.push(b' ');
    envelope.payload = STANDARD.encode(payload);
    assert!(decode_receipt(&serde_json::to_vec(&envelope).unwrap(), &key_hex()).is_err());
}

#[test]
fn forgery_and_wrong_key_are_rejected() {
    let (_, _, receipt) = context();
    let bytes = sign(&receipt);
    let wrong_key = hex::encode(SigningKey::from_bytes(&[8; 32]).verifying_key().to_bytes());
    assert!(decode_receipt(&bytes, &wrong_key).is_err());
    let mut envelope: Envelope = serde_json::from_slice(&bytes).unwrap();
    envelope.signature = "0".repeat(128);
    assert!(decode_receipt(&serde_json::to_vec(&envelope).unwrap(), &key_hex()).is_err());
}

#[test]
fn malformed_signature_key_and_base64_are_rejected() {
    let (_, _, receipt) = context();
    for signature in ["0".repeat(126), "A".repeat(128), "g".repeat(128)] {
        let mut envelope: Envelope = serde_json::from_slice(&sign(&receipt)).unwrap();
        envelope.signature = signature;
        assert!(decode_receipt(&serde_json::to_vec(&envelope).unwrap(), &key_hex()).is_err());
    }
    for key in [key_hex().to_uppercase(), "0".repeat(64), "a".repeat(62)] {
        assert!(decode_receipt(&sign(&receipt), &key).is_err());
    }
    let mut envelope: Envelope = serde_json::from_slice(&sign(&receipt)).unwrap();
    envelope.payload.push('!');
    assert!(decode_receipt(&serde_json::to_vec(&envelope).unwrap(), &key_hex()).is_err());
}

#[test]
fn strict_json_rejects_unknown_duplicate_and_trailing_fields() {
    let (_, _, receipt) = context();
    let json = String::from_utf8(serde_json::to_vec(&receipt).unwrap()).unwrap();
    for raw in [
        json.replacen('{', "{\"unknown\":true,", 1),
        json.replacen('{', "{\"format\":1,", 1),
        format!("{json} {{}}"),
    ] {
        assert!(decode_receipt(&sign_raw(raw.as_bytes()), &key_hex()).is_err());
    }
    let json = String::from_utf8(sign(&receipt)).unwrap();
    for raw in [
        json.replacen('{', "{\"unknown\":true,", 1),
        json.replacen('{', "{\"format\":1,", 1),
    ] {
        assert!(decode_receipt(raw.as_bytes(), &key_hex()).is_err());
    }
}

#[test]
fn archive_maps_require_exactly_four_unique_valid_digests() {
    let (_, manifest, receipt) = context();
    for map in ["archive_ciphertexts", "archive_plaintexts"] {
        for operation in ["missing", "extra", "uppercase", "duplicate"] {
            let mut value = serde_json::to_value(&receipt).unwrap();
            match operation {
                "missing" => {
                    value[map].as_object_mut().unwrap().remove("database");
                }
                "extra" => {
                    value[map]["extra"] = json!("a".repeat(64));
                }
                "uppercase" => {
                    value[map]["database"] = json!("A".repeat(64));
                }
                _ => {}
            }
            let mut raw = serde_json::to_string(&value).unwrap();
            if operation == "duplicate" {
                raw = raw.replacen(
                    &format!("\"{map}\":{{"),
                    &format!("\"{map}\":{{\"database\":\"{}\",", "a".repeat(64)),
                    1,
                );
            }
            let result = decode_receipt(&sign_raw(raw.as_bytes()), &key_hex())
                .and_then(|decoded| validate_bindings(&decoded, &manifest));
            assert!(result.is_err(), "accepted {map}/{operation}");
        }
    }
}

#[test]
fn valid_signatures_cannot_override_any_immutable_binding() {
    let (_, manifest, receipt) = context();
    for field in [
        "deployment_id",
        "expected_host",
        "plan_sha256",
        "backup_set_sha256",
        "database_schema",
        "environment_sha256",
        "go_candidate_sha256",
        "go_rollback_sha256",
        "web_candidate_sha256",
        "web_rollback_sha256",
        "frontend_rollback_sha256",
    ] {
        let mut value = serde_json::to_value(&receipt).unwrap();
        value[field] = json!("0".repeat(64));
        let decoded =
            decode_receipt(&sign_raw(&serde_json::to_vec(&value).unwrap()), &key_hex()).unwrap();
        assert!(
            validate_bindings(&decoded, &manifest).is_err(),
            "accepted {field}"
        );
    }
    let mut value = receipt;
    value.archive_plaintexts.database = "0".repeat(64);
    assert!(validate_bindings(&value, &manifest).is_err());
}

#[test]
fn historical_prepare_verification_does_not_expire() {
    let (_, manifest, mut receipt) = context();
    receipt.verified_utc -= chrono::Duration::days(90);
    receipt.captured_utc -= chrono::Duration::days(90);
    let decoded = decode_receipt(&sign(&receipt), &key_hex()).unwrap();
    validate_prepare(&decoded).unwrap();
    assert!(validate_bindings(&decoded, &manifest).is_ok());
}

#[test]
fn invalid_capture_times_and_non_utc_timestamps_are_rejected() {
    let (_, manifest, receipt) = context();
    for captured in [
        now() - chrono::Duration::hours(25),
        now() + chrono::Duration::seconds(31),
    ] {
        let mut changed = receipt.clone();
        changed.captured_utc = captured;
        assert!(validate_bindings(&changed, &manifest).is_err());
    }
    let mut value = serde_json::to_value(&receipt).unwrap();
    value["verified_utc"] = json!("2026-09-07T13:00:00+01:00");
    assert!(decode_receipt(&sign_raw(&serde_json::to_vec(&value).unwrap()), &key_hex()).is_err());
}

#[test]
fn confirmation_accepts_fresh_signature_with_exact_manifest_digest() {
    let (_, manifest, initial) = context();
    let confirmation = decode_receipt(&sign(&confirmation(&initial)), &key_hex()).unwrap();
    validate_bindings(&confirmation, &manifest).unwrap();
    assert!(validate_confirmation(&confirmation, &initial, &"a".repeat(64), now()).is_ok());
}

#[test]
fn fresh_confirmation_cannot_renew_a_snapshot_older_than_twenty_four_hours() {
    let (_, manifest, mut initial) = context();
    initial.verified_utc -= chrono::Duration::days(90);
    initial.captured_utc -= chrono::Duration::days(90);
    let mut fresh = confirmation(&initial);
    fresh.verified_utc = now();
    assert!(validate_bindings(&fresh, &manifest).is_err());
}

#[test]
fn confirmation_freshness_boundaries_are_enforced() {
    let (_, _, initial) = context();
    for seconds in [-301, -300, 31] {
        let mut confirmation = confirmation(&initial);
        confirmation.verified_utc = now() + chrono::Duration::seconds(seconds);
        assert!(validate_confirmation(&confirmation, &initial, &"a".repeat(64), now()).is_err());
    }
    for seconds in [-299, 0, 30] {
        let mut confirmation = confirmation(&initial);
        confirmation.verified_utc = now() + chrono::Duration::seconds(seconds);
        assert!(validate_confirmation(&confirmation, &initial, &"a".repeat(64), now()).is_ok());
    }
}

#[test]
fn confirmation_rejects_prepare_replay_wrong_manifest_and_collection_drift() {
    let (_, _, initial) = context();
    assert!(validate_confirmation(&initial, &initial, &"a".repeat(64), now()).is_err());
    let valid = confirmation(&initial);
    assert!(validate_prepare(&valid).is_err());
    assert!(validate_confirmation(&valid, &initial, &"b".repeat(64), now()).is_err());
    for field in [
        "captured_utc",
        "backup_set_sha256",
        "go_rollback_payload_sha256",
        "archive_ciphertexts",
        "archive_plaintexts",
    ] {
        let mut value = serde_json::to_value(&valid).unwrap();
        match field {
            "captured_utc" => value[field] = json!(now()),
            "archive_ciphertexts" | "archive_plaintexts" => {
                value[field]["application"] = json!("0".repeat(64))
            }
            _ => value[field] = json!("0".repeat(64)),
        }
        let changed: Receipt = serde_json::from_value(value).unwrap();
        assert!(
            validate_confirmation(&changed, &initial, &"a".repeat(64), now()).is_err(),
            "accepted {field}"
        );
    }
}

fn plan() -> ReleasePlan {
    let package = ReleasePackagePlan {
        package_path: PathBuf::from("/controller/package.pkg.tar.zst"),
        package_sha256: "a".repeat(64),
        name: "lmm-api-go-bin".to_owned(),
        version: "0.2.14".to_owned(),
        identity: "lmm-api-go-bin 0.2.14-1".to_owned(),
        git_revision: "b".repeat(40),
        contract_revision: "c".repeat(64),
        payload_sha256: "d".repeat(64),
        release_asset: PathBuf::from("/controller/release"),
        release_asset_sha256: "a".repeat(64),
        signature_bundle: PathBuf::from("/controller/signature"),
        signature_bundle_sha256: "b".repeat(64),
        release_tag: "go-v0.2.14".to_owned(),
        workflow: "release.yml".to_owned(),
    };
    let provider = ReleaseFilePlan {
        path: PathBuf::from("/controller/lmm-api-go"),
        sha256: "d".repeat(64),
    };
    ReleasePlan {
        format: 6,
        deployment_id: "signed-receipt-test".to_owned(),
        created_utc: now(),
        controller_workspace: PathBuf::from("/controller/work"),
        repository: "LightJunction/api.lmm.best".to_owned(),
        target_alias: "ArchDmit".to_owned(),
        expected_host: EXPECTED_HOST.to_owned(),
        operator_user: "lmm-api-deploy".to_owned(),
        expected_version: "0.2.14".to_owned(),
        go_candidate: package.clone(),
        go_rollback: package.clone(),
        web_candidate: package.clone(),
        web_rollback: package,
        probe_binary: provider.clone(),
        operator_binary: Some(provider),
        go_changed: true,
        web_changed: false,
        observation_seconds: 120,
        preserve_edge_policy: false,
        with_backups: false,
        backup_mode: "disabled".to_owned(),
        controller_backup_dir: PathBuf::new(),
        controller_backup_public_key: String::new(),
        age_recipient: None,
    }
}

#[test]
fn disabled_plan_accepts_go_web_and_combined_changes() {
    for (go, web) in [(true, false), (false, true), (true, true)] {
        let mut plan = plan();
        plan.go_changed = go;
        plan.web_changed = web;
        assert!(
            validate_release_plan(&plan).is_ok(),
            "rejected go={go}/web={web}"
        );
    }
}

#[test]
fn go_empty_legacy_recipient_struct_grants_no_backup_authority() {
    let mut plan = plan();
    plan.age_recipient = Some(ReleaseFilePlan {
        path: PathBuf::new(),
        sha256: String::new(),
    });
    assert!(validate_release_plan(&plan).is_ok());
}

#[test]
fn disabled_plan_rejects_stray_backup_fields() {
    for field in [
        "with_backups",
        "controller_backup_dir",
        "controller_backup_public_key",
        "age_recipient",
        "backup_mode",
    ] {
        let mut value = serde_json::to_value(plan()).unwrap();
        value[field] = match field {
            "with_backups" => json!(true),
            "controller_backup_dir" => json!("/controller/backups"),
            "controller_backup_public_key" => json!(key_hex()),
            "age_recipient" => json!({"path": "/controller/recipient", "sha256": "a".repeat(64)}),
            _ => json!(""),
        };
        let plan: ReleasePlan = serde_json::from_value(value).unwrap();
        assert!(validate_release_plan(&plan).is_err(), "accepted {field}");
    }
}

#[test]
fn controller_plan_requires_explicit_mode_absolute_path_and_key() {
    let mut plan = plan();
    plan.with_backups = true;
    plan.backup_mode = "controller-only".to_owned();
    plan.controller_backup_dir = PathBuf::from("/controller/backups");
    plan.controller_backup_public_key = key_hex();
    validate_release_plan(&plan).unwrap();
    for path in [
        "",
        "relative",
        "/controller/../backups",
        "/controller/./backups",
        "/controller/backups/",
    ] {
        plan.controller_backup_dir = PathBuf::from(path);
        assert!(validate_release_plan(&plan).is_err(), "accepted {path}");
    }
}

#[test]
fn format_five_remains_legacy_and_cannot_mix_new_policy() {
    let mut plan = plan();
    plan.format = 5;
    plan.backup_mode.clear();
    plan.with_backups = true;
    validate_release_plan(&plan).unwrap();
    plan.with_backups = false;
    assert!(validate_release_plan(&plan).is_err());
    plan.go_changed = false;
    validate_release_plan(&plan).unwrap();
    plan.backup_mode = "disabled".to_owned();
    assert!(validate_release_plan(&plan).is_err());
}

#[test]
fn schema_rejects_mixed_controller_evidence_and_unsafe_schema_names() {
    let (workspace, manifest, _) = context();
    validate_manifest_schema(&workspace, &manifest).unwrap();
    for field in [
        "backup_dir",
        "target_backup_sha256",
        "offhost_backup_sha256",
        "backup_evidence_format",
        "backups_enabled",
        "controller_only_backup",
    ] {
        let mut value = serde_json::to_value(&manifest).unwrap();
        value[field] = match field {
            "backup_dir" => json!("/target/backups"),
            "backup_evidence_format" => json!(2),
            "backups_enabled" => json!(false),
            "controller_only_backup" => Value::Null,
            _ => json!("a".repeat(64)),
        };
        let changed = serde_json::from_value(value).unwrap();
        assert!(
            validate_manifest_schema(&workspace, &changed).is_err(),
            "accepted {field}"
        );
    }
    for schema in [
        "",
        "Public",
        "pg_catalog",
        "pg_temp",
        "information_schema",
        "a,b",
        "a;DROP",
        "1app",
    ] {
        let mut changed = manifest.clone();
        changed.database_schema = schema.to_owned();
        assert!(
            validate_manifest_schema(&workspace, &changed).is_err(),
            "accepted {schema}"
        );
    }
    assert!(valid_database_schema("_application_2"));
}

#[test]
fn receipt_path_is_exact_not_just_a_path_within_workspace() {
    let (workspace, mut manifest, _) = context();
    for path in [
        workspace.state.join("other.json"),
        workspace.state.join("./controller-backup-receipt.json"),
    ] {
        manifest
            .controller_only_backup
            .as_mut()
            .unwrap()
            .receipt_path = path;
        assert!(validate_manifest_schema(&workspace, &manifest).is_err());
    }
}

#[test]
fn disabled_manifest_accepts_go_web_and_combined_changes() {
    for (go, web) in [(true, false), (false, true), (true, true)] {
        let (workspace, mut manifest, _) = context();
        manifest.backups_enabled = false;
        manifest.backup_evidence_format = 0;
        manifest.controller_only_backup = None;
        manifest.controller_backup_sha256.clear();
        manifest.database_backup_sha256.clear();
        for (transition, changed) in [(&mut manifest.go, go), (&mut manifest.web, web)] {
            transition.changed = changed;
            if !changed {
                transition.rollback_identity = transition.candidate_identity.clone();
                transition.rollback_sha256 = transition.candidate_sha256.clone();
            }
        }
        if !web {
            manifest.frontend.old_target = manifest.frontend.new_target.clone();
            manifest.frontend.old_index_sha256 = manifest.frontend.new_index_sha256.clone();
        }
        assert!(
            validate_manifest_schema(&workspace, &manifest).is_ok(),
            "rejected go={go}/web={web}"
        );
    }
}

#[test]
fn disabled_manifest_rejects_every_stray_evidence_field() {
    let (workspace, mut manifest, _) = context();
    manifest.backups_enabled = false;
    manifest.backup_evidence_format = 0;
    manifest.controller_only_backup = None;
    manifest.controller_backup_sha256.clear();
    manifest.database_backup_sha256.clear();
    validate_manifest_schema(&workspace, &manifest).unwrap();
    for field in [
        "backup_dir",
        "backup_evidence_format",
        "database_backup_sha256",
        "target_backup_sha256",
        "controller_backup_sha256",
        "offhost_backup_sha256",
        "controller_only_backup",
    ] {
        let mut value = serde_json::to_value(&manifest).unwrap();
        value[field] = match field {
            "backup_dir" => json!("/target/backups"),
            "backup_evidence_format" => json!(3),
            "controller_only_backup" => {
                serde_json::to_value(context().1.controller_only_backup).unwrap()
            }
            _ => json!("a".repeat(64)),
        };
        let changed = serde_json::from_value(value).unwrap();
        assert!(
            validate_manifest_schema(&workspace, &changed).is_err(),
            "accepted {field}"
        );
    }
}

#[test]
fn legacy_evidence_zero_and_two_schema_remain_readable() {
    for format in [0, 2] {
        let (workspace, mut manifest, _) = context();
        manifest.controller_only_backup = None;
        manifest.backup_evidence_format = format;
        manifest.backup_dir = Path::new(BACKUP_ROOT).join(&manifest.deployment_id);
        if format == 0 {
            manifest.controller_backup_sha256.clear();
        } else {
            manifest.target_backup_sha256 = "a".repeat(64);
            manifest.offhost_backup_sha256 = "b".repeat(64);
        }
        assert!(
            validate_manifest_schema(&workspace, &manifest).is_ok(),
            "rejected legacy {format}"
        );
    }
}

static SEQUENCE: AtomicUsize = AtomicUsize::new(0);

struct Files {
    workspace: Workspace,
    manifest: ProductionManifest,
}

impl Files {
    fn new() -> Self {
        let root = std::env::temp_dir().join(format!(
            "lmm-controller-backup-test-{}-{}",
            std::process::id(),
            SEQUENCE.fetch_add(1, Ordering::Relaxed)
        ));
        let workspace = workspace(root);
        fs::create_dir_all(&workspace.state).unwrap();
        fs::set_permissions(&workspace.state, fs::Permissions::from_mode(0o700)).unwrap();
        fs::create_dir(&workspace.staging).unwrap();
        let mut manifest = manifest(&workspace);
        let bytes = sign(&receipt(&manifest));
        let evidence = manifest.controller_only_backup.as_mut().unwrap();
        evidence.receipt_sha256 = sha256_bytes(&bytes);
        Self::write_private(&evidence.receipt_path, &bytes);
        Self {
            workspace,
            manifest,
        }
    }

    fn with_evidence() -> Self {
        let mut files = Self::new();
        for transition in [&mut files.manifest.go, &mut files.manifest.web] {
            Self::write_private(&transition.candidate_path, b"candidate-package");
            transition.candidate_sha256 = sha256_bytes(b"candidate-package");
            Self::write_private(&transition.rollback_path, b"rollback-package");
            transition.rollback_sha256 = sha256_bytes(b"rollback-package");
        }
        Self::write_private(&files.manifest.probe_binary, b"candidate-provider");
        symlink(GO_PROVIDER, files.workspace.staging.join("lmm-api")).unwrap();
        fs::set_permissions(
            &files.manifest.probe_binary,
            fs::Permissions::from_mode(0o700),
        )
        .unwrap();
        files.manifest.probe_binary_sha256 = sha256_bytes(b"candidate-provider");
        files.manifest.operator_binary_sha256 = files.manifest.probe_binary_sha256.clone();
        fs::create_dir(&files.manifest.config_restore_path).unwrap();
        Self::write_private(
            &files.manifest.config_restore_path.join("lmm-api-go.env"),
            b"SQL_DSN=postgres://fixture\n",
        );
        files.manifest.environment_restore_sha256 = sha256_bytes(b"SQL_DSN=postgres://fixture\n");
        let mut initial = receipt(&files.manifest);
        initial.verified_utc = Utc::now();
        initial.captured_utc = initial.verified_utc - chrono::Duration::minutes(10);
        let bytes = sign(&initial);
        let evidence = files.manifest.controller_only_backup.as_mut().unwrap();
        evidence.receipt_sha256 = sha256_bytes(&bytes);
        Self::write_private(&evidence.receipt_path, &bytes);
        Self::write_private(
            &files.workspace.manifest,
            &serde_json::to_vec(&files.manifest).unwrap(),
        );
        let mut fresh = confirmation(&initial);
        fresh.deployment_manifest_sha256 =
            sha256_bytes(&fs::read(&files.workspace.manifest).unwrap());
        Self::write_private(
            &files
                .workspace
                .state
                .join("controller-backup-confirmation.json"),
            &sign(&fresh),
        );
        files
    }

    fn write_private(path: &Path, bytes: &[u8]) {
        fs::write(path, bytes).unwrap();
        fs::set_permissions(path, fs::Permissions::from_mode(0o600)).unwrap();
    }
}

impl Drop for Files {
    fn drop(&mut self) {
        let _ = fs::remove_dir_all(&self.workspace.root);
    }
}

#[test]
fn native_manifest_and_confirmation_readers_verify_the_complete_evidence_chain() {
    let files = Files::with_evidence();
    let manifest = files.workspace.read_manifest(false).unwrap();
    assert!(verify_confirmation_evidence(&files.workspace, &manifest, false).is_ok());
}

#[test]
fn confirmation_pins_exact_manifest_bytes_including_whitespace() {
    let files = Files::with_evidence();
    let mut bytes = fs::read(&files.workspace.manifest).unwrap();
    bytes.push(b'\n');
    Files::write_private(&files.workspace.manifest, &bytes);
    assert!(verify_confirmation_evidence(&files.workspace, &files.manifest, false).is_err());
}

#[test]
fn confirmation_reader_rejects_a_missing_confirmation_file() {
    let files = Files::with_evidence();
    fs::remove_file(
        files
            .workspace
            .state
            .join("controller-backup-confirmation.json"),
    )
    .unwrap();
    assert!(verify_confirmation_evidence(&files.workspace, &files.manifest, false).is_err());
}

#[test]
fn full_manifest_reader_preserves_operator_proof_validation() {
    let files = Files::with_evidence();
    Files::write_private(&files.manifest.operator_binary, b"tampered-provider");
    assert!(files.workspace.read_manifest(false).is_err());
}

#[test]
fn pinned_initial_receipt_rejects_even_a_validly_resigned_replacement() {
    let files = Files::new();
    read_initial(&files.workspace, &files.manifest, false).unwrap();
    let mut changed = receipt(&files.manifest);
    changed.verified_utc += chrono::Duration::seconds(1);
    let path = &files
        .manifest
        .controller_only_backup
        .as_ref()
        .unwrap()
        .receipt_path;
    Files::write_private(path, &sign(&changed));
    assert!(read_initial(&files.workspace, &files.manifest, false).is_err());
}

#[test]
fn private_receipt_reader_rejects_links_public_modes_and_oversized_files() {
    for damage in [
        "hardlink",
        "symlink",
        "public",
        "public-parent",
        "oversized",
        "empty",
        "parent-symlink",
    ] {
        let files = Files::new();
        let path = &files
            .manifest
            .controller_only_backup
            .as_ref()
            .unwrap()
            .receipt_path;
        match damage {
            "hardlink" => fs::hard_link(path, files.workspace.state.join("linked.json")).unwrap(),
            "symlink" => {
                let destination = files.workspace.state.join("actual.json");
                fs::rename(path, &destination).unwrap();
                symlink(destination, path).unwrap();
            }
            "parent-symlink" => {
                let destination = files.workspace.root.join("real-state");
                fs::rename(&files.workspace.state, &destination).unwrap();
                symlink(destination, &files.workspace.state).unwrap();
            }
            "public" => fs::set_permissions(path, fs::Permissions::from_mode(0o644)).unwrap(),
            "public-parent" => {
                fs::set_permissions(&files.workspace.state, fs::Permissions::from_mode(0o755))
                    .unwrap()
            }
            "oversized" => Files::write_private(path, &vec![b' '; MAX_RECEIPT_BYTES as usize + 1]),
            _ => Files::write_private(path, b""),
        }
        assert!(
            read_initial(&files.workspace, &files.manifest, false).is_err(),
            "accepted {damage}"
        );
    }
}

#[test]
fn rollback_reader_does_not_require_receipts_controller_or_candidate_files() {
    let mut files = Files::new();
    for transition in [&mut files.manifest.go, &mut files.manifest.web] {
        Files::write_private(&transition.rollback_path, b"retained-rollback-package");
        transition.rollback_sha256 = sha256_bytes(b"retained-rollback-package");
    }
    fs::create_dir(&files.manifest.config_restore_path).unwrap();
    Files::write_private(
        &files.manifest.config_restore_path.join("lmm-api-go.env"),
        b"SQL_DSN=postgres://fixture\n",
    );
    files.manifest.environment_restore_sha256 = sha256_bytes(b"SQL_DSN=postgres://fixture\n");
    Files::write_private(
        &files.workspace.manifest,
        &serde_json::to_vec(&files.manifest).unwrap(),
    );
    fs::remove_file(
        &files
            .manifest
            .controller_only_backup
            .as_ref()
            .unwrap()
            .receipt_path,
    )
    .unwrap();
    assert!(files.workspace.read_manifest_for_rollback(false).is_ok());
}
