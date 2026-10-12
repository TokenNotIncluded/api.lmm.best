//! Approval binding/parser tests only. No administrator is activated here.
//! Concurrency, real COMMIT loss, ACL and credential revocation need real PG/A02.
use lmm_core::{accounts, identity};
#[allow(dead_code)]
#[path = "../src/identity/permissions.rs"]
mod permissions;
#[allow(dead_code)]
#[path = "../src/identity/admin_approval.rs"]
mod admin_approval;
use admin_approval::*;
use permissions::{CONTRACT_VERSION, PlatformRole, PolicySnapshot};
use serde_json::{Value, json};

fn hex(bytes: [u8; 32]) -> String {
    bytes.iter().map(|byte| format!("{byte:02x}")).collect()
}
fn policy(version: i64) -> PolicySnapshot {
    PolicySnapshot::new(41, PlatformRole::Admin, version, &[]).unwrap()
}
fn draft(p: &PolicySnapshot) -> Value {
    json!({
        "format_version": 1,
        "approval_id": "synthetic-approval-01",
        "source_archive_sha256": hex([1; 32]),
        "source_a2_fact_sha256": hex([2; 32]),
        "source_user_id": 41,
        "target_user_id": 41,
        "target_instance": "synthetic-target-a01",
        "contract_version": CONTRACT_VERSION,
        "platform_role": "admin",
        "expected_auth_version": 7,
        "expected_policy": {"state": "absent"},
        "proposed_policy_sha256": hex(p.fingerprint()),
        "expires_at_unix_seconds": 200,
    })
}
fn parsed(v: &Value) -> ParsedApproval {
    parse_approval(&serde_json::to_vec(v).unwrap()).unwrap()
}
fn source() -> SourceFacts<'static> {
    SourceFacts { archive_hash: [1; 32], a2_fact_hash: [2; 32], source_user_id: 41,
        target_user_id: 41, imported_auth_version: 7, role: PlatformRole::Admin,
        blocked_reasons: &["privileged_permissions_unmapped"],
        verifier_available: true, second_factor_required: false }
}
fn target() -> TargetFacts<'static> {
    TargetFacts { instance: "synthetic-target-a01", user_id: 41, role: PlatformRole::Admin,
        active: false, auth_version: 7, policy: None }
}

#[test]
fn matching_single_identity_produces_bindings_not_an_activation() {
    let p = policy(1);
    let checked = check_bindings(&parsed(&draft(&p)), &source(), &target(), &p, 100).unwrap();
    assert_eq!(checked.target_user_id, 41);
    assert_eq!(checked.expected_auth_version, 7);
    assert_eq!(checked.next_auth_version, 8);
    assert_eq!(checked.next_policy.version, 1);
    // There is deliberately no activate(), DB write, session or key result.
}

#[test]
fn batch_unknown_and_duplicate_fields_are_rejected() {
    let p = policy(1);
    let base = draft(&p);
    assert!(parse_approval(&serde_json::to_vec(&json!([base.clone()])).unwrap()).is_err());
    for (key, value) in [("users", json!([41, 42])), ("allow_all_admins", json!(true)),
        ("host_token", json!("synthetic-not-a-user"))] {
        let mut v = base.clone();
        v[key] = value;
        assert!(parse_approval(&serde_json::to_vec(&v).unwrap()).is_err());
    }
    let text = serde_json::to_string(&base).unwrap();
    let duplicate = format!("{},\"target_user_id\":42}}", &text[..text.len()-1]);
    assert!(parse_approval(duplicate.as_bytes()).is_err());
}

#[test]
fn explicit_absent_policy_field_is_required() {
    let mut v = draft(&policy(1));
    v.as_object_mut().unwrap().remove("expected_policy");
    assert!(parse_approval(&serde_json::to_vec(&v).unwrap()).is_err());
}

#[test]
fn malformed_hashes_versions_identifiers_and_roles_reject() {
    for (key, value) in [
        ("approval_id", json!("*")), ("target_instance", json!("")),
        ("source_archive_sha256", json!("A".repeat(64))),
        ("source_a2_fact_sha256", json!("a".repeat(63))),
        ("source_user_id", json!(0)), ("target_user_id", json!(-1)),
        ("expected_auth_version", json!(0)), ("expected_auth_version", json!(i64::MAX)),
        ("expected_auth_version", json!("7")), ("platform_role", json!("user")),
        ("platform_role", json!("root")), ("format_version", json!(2)),
        ("contract_version", json!("a01-permissions-v0")),
    ] {
        let mut v = draft(&policy(1));
        v[key] = value;
        assert!(parse_approval(&serde_json::to_vec(&v).unwrap()).is_err(), "{key}");
    }
    assert!(parse_approval(&vec![b' '; 16 * 1024 + 1]).is_err());
}

#[test]
fn original_archive_and_original_a2_fact_must_both_match() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    let mut s = source();
    s.archive_hash[0] ^= 1;
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::WrongSource));
    let mut s = source();
    s.a2_fact_hash[0] ^= 1;
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::WrongSource));
    let mut s = source();
    s.source_user_id = 42;
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::WrongSource));
}

#[test]
fn approval_cannot_move_to_another_user_or_instance() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    let mut t = target();
    t.instance = "another-synthetic-target";
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::WrongTarget));
    let mut t = target();
    t.user_id = 42;
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::WrongTarget));
}

#[test]
fn ordinary_users_and_role_promotions_do_not_pass_activation_bindings() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    for role in [PlatformRole::User, PlatformRole::Superadmin] {
        let mut s = source();
        s.role = role;
        assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::RoleChange));
    }
}

#[test]
fn other_blockers_and_missing_authentication_prerequisites_remain_blocked() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    let mut s = source();
    s.blocked_reasons = &["privileged_permissions_unmapped", "source_disabled"];
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::OtherBlocker));
    let mut s = source();
    s.verifier_available = false;
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::AuthenticationUnproven));
    let mut s = source();
    s.second_factor_required = true;
    assert_eq!(check_bindings(&approval, &s, &target(), &p, 100), Err(ApprovalError::AuthenticationUnproven));
}

#[test]
fn expired_approval_or_changed_auth_version_conflicts() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    assert_eq!(check_bindings(&approval, &source(), &target(), &p, 200), Err(ApprovalError::Expired));
    let mut t = target();
    t.auth_version = 8;
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::Conflict));
    let mut t = target();
    t.active = true;
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::Conflict));
}

#[test]
fn policy_version_and_hash_cas_are_both_bound() {
    let p = policy(3);
    let mut v = draft(&p);
    v["expected_policy"] = json!({"state": "present", "version": 2, "sha256": hex([3; 32])});
    let approval = parsed(&v);
    let mut t = target();
    t.policy = Some(PolicyBinding { version: 2, sha256: [3; 32] });
    assert!(check_bindings(&approval, &source(), &t, &p, 100).is_ok());
    t.policy = Some(PolicyBinding { version: 3, sha256: [3; 32] });
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::Conflict));
    t.policy = Some(PolicyBinding { version: 2, sha256: [4; 32] });
    assert_eq!(check_bindings(&approval, &source(), &t, &p, 100), Err(ApprovalError::Conflict));
    let wrong_policy = PolicySnapshot::new(42, PlatformRole::Admin, 3, &[]).unwrap();
    t.policy = Some(PolicyBinding { version: 2, sha256: [3; 32] });
    assert_eq!(check_bindings(&approval, &source(), &t, &wrong_policy, 100), Err(ApprovalError::PolicyMismatch));
}

#[test]
fn input_whitespace_is_not_a_new_approval() {
    let p = policy(1);
    let v = draft(&p);
    let compact = parsed(&v);
    let pretty = parse_approval(&serde_json::to_vec_pretty(&v).unwrap()).unwrap();
    let a = check_bindings(&compact, &source(), &target(), &p, 100).unwrap();
    let b = check_bindings(&pretty, &source(), &target(), &p, 100).unwrap();
    assert_eq!(a.request_hash, b.request_hash);
}

#[test]
fn missing_commit_receipt_stays_unknown_until_the_original_writer_is_fenced() {
    let approval = parsed(&draft(&policy(1)));
    assert_eq!(resolve_outcome(&approval, None, WriterObservation::Unknown), CommitResolution::Unknown);
    assert_eq!(resolve_outcome(&approval, None, WriterObservation::FencedAfterWriterEnded), CommitResolution::NotApplied);
    // This test does not create a real database fence or kill a writer process.
}

#[test]
fn only_an_exact_durable_receipt_resolves_a_lost_acknowledgement() {
    let p = policy(1);
    let approval = parsed(&draft(&p));
    let checked = check_bindings(&approval, &source(), &target(), &p, 100).unwrap();
    let mut receipt = DurableReceipt { approval_id: "synthetic-approval-01", request_hash: checked.request_hash,
        target_user_id: 41, new_auth_version: 8 };
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown),
        CommitResolution::Committed { new_auth_version: 8 });
    receipt.target_user_id = 42;
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown), CommitResolution::Conflict);
    receipt.target_user_id = 41;
    receipt.request_hash[0] ^= 1;
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown), CommitResolution::Conflict);
}
