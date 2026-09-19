#![allow(clippy::unwrap_used, clippy::expect_used)]

use lmm::{
    catalog,
    discovery::{self, Observation, ProbeContext},
    plan::{self, Activation, Intent, PlanError, Selection},
    restore::{self, FieldChange, RestoreError},
};
use serde_json::json;
use std::{fs, path::PathBuf};

#[test]
fn repeated_setup_and_maintenance_preserve_a_later_provider_choice() {
    for _ in 0..3 {
        for intent in [Intent::Default, Intent::AddOnly, Intent::Maintain] {
            assert_eq!(
                plan::activation(Selection::Other, intent),
                Ok(Activation::Preserve)
            );
        }
    }
}

#[test]
fn new_configuration_can_propose_lmm_but_add_only_never_switches() {
    assert_eq!(
        plan::activation(Selection::Unconfigured, Intent::Default),
        Ok(Activation::ProposeLmm)
    );
    assert_eq!(
        plan::activation(Selection::Unconfigured, Intent::AddOnly),
        Ok(Activation::Preserve)
    );
}

#[test]
fn unknown_current_selection_blocks_activation_even_when_requested() {
    for intent in [Intent::Default, Intent::Activate] {
        assert_eq!(
            plan::activation(Selection::Unknown, intent),
            Err(PlanError::UnknownSelection)
        );
    }
}

#[test]
fn revoked_authorization_requires_explicit_reauthorization() {
    assert_eq!(
        plan::check_revocation(true, false),
        Err(PlanError::RevokedGrant)
    );
    assert!(plan::check_revocation(true, true).is_ok());
}

#[test]
fn paid_verification_requires_consent_known_price_and_enforced_cap() {
    for (consent, quote, limit) in [
        (false, Some(1), Some(10)),
        (true, None, Some(10)),
        (true, Some(1), None),
        (true, Some(11), Some(10)),
        (true, Some(0), Some(0)),
    ] {
        assert_eq!(
            plan::check_paid_verification(consent, quote, limit),
            Err(PlanError::PaidVerificationNotAuthorized)
        );
    }
    assert!(plan::check_paid_verification(true, Some(10), Some(10)).is_ok());
}

#[test]
fn cc_switch_presence_does_not_establish_management_of_another_app() {
    let home = tempfile::tempdir().unwrap();
    fs::create_dir(home.path().join(".cc-switch")).unwrap();
    fs::write(
        home.path().join(".cc-switch/cc-switch.db"),
        "unread database",
    )
    .unwrap();
    let context = ProbeContext {
        home: Some(home.path().to_path_buf()),
        path: vec![],
    };
    let status = discovery::discover(catalog::find("codex").unwrap(), &context, None);
    assert_eq!(status.configuration_manager, "unknown");
    assert!(status.evidence.is_empty());
}

#[test]
fn discovery_does_not_read_or_publish_configuration_values() {
    let instance = tempfile::tempdir().unwrap();
    fs::create_dir(instance.path().join("data")).unwrap();
    fs::write(
        instance.path().join("data/cmd_config.json"),
        r#"{"secret":"DO_NOT_LEAK"}"#,
    )
    .unwrap();
    let context = ProbeContext {
        home: None,
        path: vec![],
    };
    let status = discovery::discover(
        catalog::find("astrbot").unwrap(),
        &context,
        Some(instance.path()),
    );
    assert_eq!(status.evidence[0].observation, Observation::Present);
    assert_eq!(status.installation, "unknown");
    assert_eq!(status.verification.target_call, "not_checked");
    assert!(
        !serde_json::to_string(&status)
            .unwrap()
            .contains("DO_NOT_LEAK")
    );
}

#[test]
fn discovery_does_not_search_relative_path_entries_or_infer_remote_instances() {
    let context = ProbeContext {
        home: None,
        path: vec![PathBuf::from("."), PathBuf::from("bin")],
    };
    let status = discovery::discover(catalog::find("astrbot").unwrap(), &context, None);
    assert!(status.evidence.is_empty());
    assert_eq!(status.installation, "unknown");
}

#[test]
fn multiple_binary_locations_remain_separate_evidence_without_launching() {
    let root = tempfile::tempdir().unwrap();
    let mut directories = vec![];
    for name in ["first", "second"] {
        let directory = root.path().join(name);
        fs::create_dir(&directory).unwrap();
        fs::write(
            directory.join(if cfg!(windows) { "codex.exe" } else { "codex" }),
            "not executable",
        )
        .unwrap();
        directories.push(directory);
    }
    let context = ProbeContext {
        home: None,
        path: directories,
    };
    let status = discovery::discover(catalog::find("codex").unwrap(), &context, None);
    assert_eq!(status.evidence.len(), 2);
    assert_eq!(status.installation, "unknown");
}

#[test]
fn directories_and_symlinks_are_not_reported_as_installed() {
    let root = tempfile::tempdir().unwrap();
    assert_eq!(discovery::observe(root.path()), Observation::Unknown);
    #[cfg(unix)]
    {
        let link = root.path().join("link");
        std::os::unix::fs::symlink("missing", &link).unwrap();
        assert_eq!(discovery::observe(&link), Observation::Unknown);
        assert!(discovery::fingerprint(&link).is_err());
    }
}

#[test]
fn fingerprints_detect_edits_and_reject_oversize_files() {
    let root = tempfile::tempdir().unwrap();
    let path = root.path().join("config");
    fs::write(&path, "first").unwrap();
    let before = discovery::fingerprint(&path).unwrap();
    fs::write(&path, "second").unwrap();
    assert_ne!(before, discovery::fingerprint(&path).unwrap());
    fs::File::create(&path)
        .unwrap()
        .set_len(1024 * 1024 + 1)
        .unwrap();
    assert!(discovery::fingerprint(&path).is_err());
}

fn change(
    pointer: &str,
    before: Option<serde_json::Value>,
    after: Option<serde_json::Value>,
) -> FieldChange {
    FieldChange {
        pointer: pointer.into(),
        before,
        after,
    }
}

#[test]
fn restore_preserves_later_unrelated_user_settings_and_is_repeatable() {
    let current = json!({"providers":{"lmm":"new"},"theme":"user-choice","plugins":["keep"]});
    let changes = [change("/providers/lmm", None, Some(json!("new")))];
    let restored = restore::restore_fields(&current, &changes).unwrap();
    assert_eq!(
        restored,
        json!({"providers":{},"theme":"user-choice","plugins":["keep"]})
    );
    assert_eq!(
        restore::restore_fields(&restored, &changes).unwrap(),
        restored
    );
}

#[test]
fn restore_refuses_conflicting_user_edits_without_partially_mutating_input() {
    let current = json!({"first":"lmm-value","second":"user-value"});
    let changes = [
        change("/first", None, Some(json!("lmm-value"))),
        change("/second", None, Some(json!("lmm-value"))),
    ];
    assert_eq!(
        restore::restore_fields(&current, &changes),
        Err(RestoreError::Conflict)
    );
    assert_eq!(current["first"], "lmm-value");
}

#[test]
fn restore_does_not_replace_whole_objects_or_arrays() {
    for after in [json!({"lmm":"token"}), json!([1, 2])] {
        let current = json!({"providers": after});
        assert_eq!(
            restore::restore_fields(&current, &[change("/providers", None, Some(after))]),
            Err(RestoreError::UnsupportedShape)
        );
    }
}

#[test]
fn restore_distinguishes_null_from_absent() {
    let changes = [change("/value", None, Some(serde_json::Value::Null))];
    assert_eq!(
        restore::restore_fields(&json!({"value":null}), &changes).unwrap(),
        json!({})
    );
}

#[test]
fn restore_rejects_array_indices_because_reordering_changes_identity() {
    let current = json!({"providers": [{"model":"new"}]});
    let changes = [change(
        "/providers/0/model",
        Some(json!("old")),
        Some(json!("new")),
    )];
    assert_eq!(
        restore::restore_fields(&current, &changes),
        Err(RestoreError::UnsupportedShape)
    );
}

#[test]
fn restore_supports_escaped_keys_but_rejects_invalid_and_duplicate_pointers() {
    let current = json!({"a/b":{"~key":"new"}});
    let changes = [change(
        "/a~1b/~0key",
        Some(json!("old")),
        Some(json!("new")),
    )];
    assert_eq!(
        restore::restore_fields(&current, &changes).unwrap(),
        json!({"a/b":{"~key":"old"}})
    );
    assert_eq!(
        restore::restore_fields(&current, &[change("/a~2b", None, None)]),
        Err(RestoreError::InvalidPointer)
    );
    assert_eq!(
        restore::restore_fields(
            &json!({}),
            &[change("/a", None, None), change("/a", None, None)]
        ),
        Err(RestoreError::DuplicateField)
    );
}
