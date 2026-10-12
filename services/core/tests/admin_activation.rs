//! A01 pure and PostgreSQL domain tests, using only a new synthetic SQLx database.
//! Wire-level COMMIT-ACK loss and final S11 HTTP/browser integration are separate.
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
        "format_version": 2,
        "approval_id": "synthetic-approval-01",
        "source_archive_sha256": hex([1; 32]),
        "source_a2_fact_sha256": hex([2; 32]),
        "gate_manifest_sha256": hex([3; 32]),
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
        active: false, account_active: true, password_login_allowed: false,
        second_factor_required: false, auth_version: 7, policy: None }
}

#[test]
fn matching_single_identity_produces_bindings_not_an_activation() {
    let p = policy(1);
    let checked = check_bindings(&parsed(&draft(&p)), &source(), &target(), &p, 100).unwrap();
    assert_eq!(checked.target_user_id, 41);
    assert_eq!(checked.expected_auth_version, 7);
    assert_eq!(checked.next_auth_version, 9);
    assert_eq!(checked.next_policy.version, 1);
    // This remains a binding-only test. The PG tests below exercise the write.
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
        ("expected_auth_version", json!(i64::MAX - 1)),
        ("expected_auth_version", json!("7")), ("platform_role", json!("user")),
        ("platform_role", json!("root")), ("format_version", json!(1)),
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
        target_user_id: 41, new_auth_version: 9 };
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown),
        CommitResolution::Committed { new_auth_version: 9 });
    receipt.target_user_id = 42;
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown), CommitResolution::Conflict);
    receipt.target_user_id = 41;
    receipt.request_hash[0] ^= 1;
    assert_eq!(resolve_outcome(&approval, Some(&receipt), WriterObservation::Unknown), CommitResolution::Conflict);
}

fn signed_bytes(value: &Value, key: &[u8; 32], operator: &str) -> Vec<u8> {
    use hmac::{Hmac, Mac};
    use sha2::Sha256;
    let digest = parsed(value).request_hash();
    let mut mac = <Hmac<Sha256> as Mac>::new_from_slice(key).unwrap();
    mac.update(b"lmm-a01-signed-approval-v2\0");
    mac.update(&(operator.len() as u64).to_be_bytes());
    mac.update(operator.as_bytes());
    mac.update(&digest);
    let signature: [u8; 32] = mac.finalize().into_bytes().into();
    serde_json::to_vec(&json!({"operator_key_id":operator,"approval":value,"signature_sha256":hex(signature)})).unwrap()
}

#[test]
fn signature_binds_operator_request_and_key() {
    let original = draft(&policy(1));
    let bytes = signed_bytes(&original, &[7; 32], "synthetic-operator");
    assert!(verify_signed_approval(&bytes, &[7; 32]).is_ok());
    assert!(matches!(verify_signed_approval(&bytes, &[8; 32]), Err(ApprovalError::SignatureInvalid)));
    for mutate_operator in [true, false] {
        let mut value: Value = serde_json::from_slice(&bytes).unwrap();
        if mutate_operator { value["operator_key_id"] = json!("different-operator"); }
        else { value["approval"]["target_user_id"] = json!(42); }
        assert!(matches!(verify_signed_approval(&serde_json::to_vec(&value).unwrap(), &[7; 32]), Err(ApprovalError::SignatureInvalid)));
    }
    assert!(verify_signed_approval(&bytes, &[7; 31]).is_err());
}

#[test]
fn signature_envelope_rejects_batch_unknown_duplicate_and_large_input() {
    let bytes = signed_bytes(&draft(&policy(1)), &[7; 32], "synthetic-operator");
    let mut value: Value = serde_json::from_slice(&bytes).unwrap();
    assert!(verify_signed_approval(&serde_json::to_vec(&vec![&value]).unwrap(), &[7; 32]).is_err());
    value["host_token"] = json!("not-a-user");
    assert!(verify_signed_approval(&serde_json::to_vec(&value).unwrap(), &[7; 32]).is_err());
    let text = String::from_utf8(bytes).unwrap();
    let duplicate = format!("{},\"operator_key_id\":\"other\"}}", &text[..text.len()-1]);
    assert!(verify_signed_approval(duplicate.as_bytes(), &[7; 32]).is_err());
    assert!(verify_signed_approval(&vec![b' '; 20*1024+1], &[7; 32]).is_err());
}

#[test]
fn native_profile_and_activation_generations_are_both_reserved() {
    let p = policy(1);
    let a = parsed(&draft(&p));
    let binding = check_bindings(&a, &source(), &target(), &p, 100).unwrap();
    assert_eq!(binding.next_auth_version - binding.expected_auth_version, 2);
    for kind in 0..3 {
        let mut t = target();
        match kind { 0 => t.account_active=false, 1 => t.password_login_allowed=true, _ => t.second_factor_required=true }
        assert_eq!(check_bindings(&a, &source(), &t, &p, 100), Err(ApprovalError::AuthenticationUnproven));
    }
}

// The following tests use real PostgreSQL transactions when explicitly run in
// A09's fresh synthetic test cluster. They do not contact any real source/target.
// The fixture installs the real base core schema, then the isolated A01 schema.
// This domain fixture does not attest to S11's final assembled schema catalog.
async fn pg_fixture(pool: &sqlx::PgPool) -> (Value, String) {
    use sha2::{Digest, Sha256};
    lmm_core::core_schema::init(pool).await.unwrap();
    let missing: bool = sqlx::query_scalar("SELECT to_regclass('core_identity.admin_policies') IS NULL")
        .fetch_one(pool).await.unwrap();
    if missing { sqlx::raw_sql(ISOLATED_A01_SCHEMA).execute(pool).await.unwrap(); }
    let account: i64 = sqlx::query_scalar("INSERT INTO core_identity.accounts(kind) VALUES ('personal') RETURNING id")
        .fetch_one(pool).await.unwrap();
    sqlx::query("INSERT INTO core_billing.account_policies(account_id) VALUES ($1)")
        .bind(account).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.users(id,personal_account_id,platform_role,active,auth_version) VALUES (41,$1,'admin',FALSE,7)")
        .bind(account).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.user_profiles(user_id,username,password_login_allowed,legacy_bcrypt_allowed) VALUES (41,'a01-fixture',FALSE,TRUE)")
        .execute(pool).await.unwrap();
    let password = "synthetic-a01-test-password-only";
    let verifier = bcrypt::hash(password, 4).unwrap();
    sqlx::query("INSERT INTO core_identity.password_logins(user_id,login_name,password_hash) VALUES (41,'a01-fixture',$1)")
        .bind(&verifier).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES (41,TRUE,'synthetic-dormant-admin')")
        .execute(pool).await.unwrap();
    let secret = format!("lmms_{}", "a".repeat(64));
    let credential: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) VALUES ($1,'session',41,7,$2,clock_timestamp()+interval '1 day') RETURNING id")
        .bind(Sha256::digest(secret.as_bytes()).to_vec()).bind(account)
        .fetch_one(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.web_sessions(sid,user_id,user_version,current_credential_id,refresh_digest,ip,user_agent) VALUES ($1,41,7,$2,$3,'127.0.0.1','synthetic-a01')")
        .bind("b".repeat(64)).bind(credential).bind(vec![4_u8; 32]).execute(pool).await.unwrap();
    let key_id: i64 = sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) VALUES ($1,'api_key',41,7,$2,clock_timestamp()+interval '1 day') RETURNING id")
        .bind(Sha256::digest(format!("lmmk_{}", "c".repeat(64)).as_bytes()).to_vec()).bind(account)
        .fetch_one(pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.key_funding_rules(credential_id,position,payer_account_id) VALUES ($1,0,$2)")
        .bind(key_id).bind(account).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_migration.import_runs(migration_id,source_archive_sha256,mapping_sha256,manifest) VALUES ('synthetic-a2',$1,$2,'{}')")
        .bind(hex([1; 32])).bind(hex([4; 32])).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_migration.admin_activation_context(target_instance,contract_version,gate_manifest_sha256) VALUES ('synthetic-target-a01',$1,$2)")
        .bind(CONTRACT_VERSION).bind(vec![3_u8;32]).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_migration.admin_approval_keys(key_id,key_hash) VALUES ('synthetic-operator',$1)")
        .bind(Sha256::digest([7_u8;32]).to_vec()).execute(pool).await.unwrap();
    sqlx::query("INSERT INTO core_migration.admin_activation_sources(target_user_id,source_user_id,migration_id,source_archive_sha256,mapping_sha256,source_a2_fact_sha256,imported_auth_version,platform_role,blocked_reasons,verifier_sha256,second_factor_required,legacy_bcrypt_allowed) VALUES (41,41,'synthetic-a2',$1,$2,$3,7,'admin',ARRAY['privileged_permissions_unmapped'],$4,FALSE,TRUE)")
        .bind(hex([1;32])).bind(hex([4;32])).bind(vec![2_u8;32])
        .bind(Sha256::digest(verifier.as_bytes()).to_vec()).execute(pool).await.unwrap();
    let mut value = draft(&policy(1));
    let now: i64 = sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
        .fetch_one(pool).await.unwrap();
    value["expires_at_unix_seconds"] = json!(now + 600);
    (value, secret)
}

// Capture every application table and sequence, not just receipt counts.
// Names are read from this synthetic database's catalog and quoted as identifiers.
async fn pg_snapshot(pool: &sqlx::PgPool) -> std::collections::BTreeMap<String, Vec<String>> {
    use sqlx::Row;
    let mut snapshot = std::collections::BTreeMap::new();
    let tables = sqlx::query("SELECT schemaname,tablename FROM pg_catalog.pg_tables WHERE schemaname NOT IN ('pg_catalog','information_schema') ORDER BY schemaname,tablename")
        .fetch_all(pool).await.unwrap();
    for table in tables {
        let schema: String = table.try_get("schemaname").unwrap();
        let name: String = table.try_get("tablename").unwrap();
        let quoted = format!("\"{}\".\"{}\"", schema.replace('"', "\"\""), name.replace('"', "\"\""));
        let mut rows: Vec<String> = sqlx::query_scalar(&format!("SELECT to_jsonb(t)::text FROM {quoted} t"))
            .fetch_all(pool).await.unwrap();
        rows.sort();
        snapshot.insert(format!("{schema}.{name}"), rows);
    }
    let rows: Vec<String> = sqlx::query_scalar("SELECT jsonb_build_array(schemaname,sequencename,last_value)::text FROM pg_catalog.pg_sequences ORDER BY schemaname,sequencename")
        .fetch_all(pool).await.unwrap();
    snapshot.insert("ALL_SEQUENCES".into(), rows);
    snapshot
}

#[sqlx::test]
async fn pg_activation_is_single_scoped_durable_and_uses_native_login(pool: sqlx::PgPool) {
    let (value, old_session) = pg_fixture(&pool).await;
    let signed = verify_signed_approval(&signed_bytes(&value, &[7;32], "synthetic-operator"), &[7;32]).unwrap();
    let before = pg_snapshot(&pool).await;
    assert_eq!(apply_approved_activation(&pool, &signed, &policy(1)).await.unwrap(), CommitResolution::Committed { new_auth_version:9 });
    let after = pg_snapshot(&pool).await;
    let changed: Vec<&str> = before.iter().filter_map(|(name, rows)| (after.get(name)!=Some(rows)).then_some(name.as_str())).collect();
    assert_eq!(changed, vec!["core_identity.admin_policies", "core_identity.credentials", "core_identity.user_profiles", "core_identity.users", "core_identity.web_sessions", "core_migration.admin_activation_receipts"]);
    assert_eq!(before["ALL_SEQUENCES"], after["ALL_SEQUENCES"]);
    // Idempotent retry changes nothing, including the immutable audit timestamp.
    assert_eq!(apply_approved_activation(&pool, &signed, &policy(1)).await.unwrap(), CommitResolution::Committed { new_auth_version:9 });
    assert_eq!(after, pg_snapshot(&pool).await);
    let store = identity::IdentityStore::from_pool(pool.clone());
    assert!(store.authorize(&old_session).await.is_err());
    assert!(store.authorize(&format!("lmmk_{}", "c".repeat(64))).await.is_err());
    let issued = store.login(&identity::LoginRequest { login_name:"a01-fixture".into(), password:"synthetic-a01-test-password-only".into() }).await.unwrap();
    let actor = store.authorize(&issued.secret).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert!(permissions::require_platform_in_transaction(&mut tx, &actor,
        permissions::Permission::ChannelRead, permissions::ResourceScope::ChannelCollection).await.is_ok());
    assert!(permissions::require_platform_in_transaction(&mut tx, &actor,
        permissions::Permission::ChannelRead, permissions::ResourceScope::CompanyProfile(41)).await.is_err());
    tx.rollback().await.unwrap();
    // An active policy change invalidates the previously issued native session.
    let denied = PolicySnapshot::new(41, PlatformRole::Admin, 2,
        &[(permissions::Permission::ChannelRead, permissions::Effect::Deny)]).unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SELECT id FROM core_identity.users WHERE id=41 FOR UPDATE")
        .execute(&mut *tx).await.unwrap();
    sqlx::query("UPDATE core_identity.admin_policies SET version=2,effects=$1,fingerprint=$2 WHERE user_id=41 AND version=1")
        .bind(denied.stored_effects()).bind(denied.fingerprint().to_vec())
        .execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    assert!(store.authorize(&issued.secret).await.is_err());
    let fresh = store.login(&identity::LoginRequest { login_name:"a01-fixture".into(), password:"synthetic-a01-test-password-only".into() }).await.unwrap();
    let actor = store.authorize(&fresh.secret).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert!(matches!(permissions::require_platform_in_transaction(&mut tx, &actor,
        permissions::Permission::ChannelRead, permissions::ResourceScope::ChannelCollection).await,
        Err(identity::IdentityError::Forbidden)));
    tx.rollback().await.unwrap();
}

#[sqlx::test]
async fn pg_competing_approval_ids_cannot_activate_twice(pool: sqlx::PgPool) {
    let (first, _) = pg_fixture(&pool).await;
    let mut second = first.clone();
    second["approval_id"] = json!("synthetic-competing-id");
    let a = verify_signed_approval(&signed_bytes(&first,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    let b = verify_signed_approval(&signed_bytes(&second,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    let policy = policy(1);
    let (a,b) = tokio::join!(apply_approved_activation(&pool,&a,&policy),apply_approved_activation(&pool,&b,&policy));
    assert_eq!([&a,&b].iter().filter(|r| matches!(r,Ok(CommitResolution::Committed { new_auth_version:9 }))).count(),1);
    assert_eq!([&a,&b].iter().filter(|r| matches!(r,Err(ApprovalError::Conflict))).count(),1);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_migration.admin_activation_receipts").fetch_one(&pool).await.unwrap();
    assert_eq!(count,1);
}

#[sqlx::test]
async fn pg_final_write_failure_rolls_back_every_table_and_sequence(pool: sqlx::PgPool) {
    let (value, _) = pg_fixture(&pool).await;
    sqlx::raw_sql("CREATE FUNCTION public.reject_a01_activation() RETURNS TRIGGER LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'synthetic late failure'; END $$; CREATE TRIGGER reject_a01 BEFORE UPDATE ON core_identity.users FOR EACH ROW WHEN (NEW.active AND NOT OLD.active) EXECUTE FUNCTION public.reject_a01_activation();")
        .execute(&pool).await.unwrap();
    let signed = verify_signed_approval(&signed_bytes(&value,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    let before = pg_snapshot(&pool).await;
    assert!(apply_approved_activation(&pool,&signed,&policy(1)).await.is_err());
    assert_eq!(before,pg_snapshot(&pool).await);
    assert_eq!(query_activation_outcome(&pool,&parsed(&value)).await.unwrap(),CommitResolution::Unknown);
}

#[sqlx::test]
async fn pg_bad_gate_operator_and_policy_cas_do_not_write(pool: sqlx::PgPool) {
    let (value, _) = pg_fixture(&pool).await;
    let before = pg_snapshot(&pool).await;
    let mut bad = value.clone();
    bad["gate_manifest_sha256"] = json!(hex([9;32]));
    let signed = verify_signed_approval(&signed_bytes(&bad,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    assert_eq!(apply_approved_activation(&pool,&signed,&policy(1)).await,Err(ApprovalError::GateProofMismatch));
    let signed = verify_signed_approval(&signed_bytes(&value,&[8;32],"synthetic-operator"),&[8;32]).unwrap();
    assert_eq!(apply_approved_activation(&pool,&signed,&policy(1)).await,Err(ApprovalError::OperatorDenied));
    bad = value.clone();
    bad["expected_policy"] = json!({"state":"present","version":1,"sha256":hex([6;32])});
    let signed = verify_signed_approval(&signed_bytes(&bad,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    assert_eq!(apply_approved_activation(&pool,&signed,&policy(1)).await,Err(ApprovalError::Conflict));
    assert_eq!(before,pg_snapshot(&pool).await);
}

#[sqlx::test]
async fn pg_receipt_lookup_binds_the_original_request_after_lost_caller_result(pool: sqlx::PgPool) {
    let (value, _) = pg_fixture(&pool).await;
    let signed = verify_signed_approval(&signed_bytes(&value,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    // Discard the result, then resolve from durable storage. This exercises a
    // caller losing the result, NOT a wire-level lost COMMIT ACK or process kill.
    let _lost_result = apply_approved_activation(&pool,&signed,&policy(1)).await.unwrap();
    assert_eq!(query_activation_outcome(&pool,&parsed(&value)).await.unwrap(),CommitResolution::Committed { new_auth_version:9 });
    let mut changed = value.clone();
    changed["expires_at_unix_seconds"] = json!(100);
    assert_eq!(query_activation_outcome(&pool,&parsed(&changed)).await.unwrap(),CommitResolution::Conflict);
    changed["approval_id"] = json!("unknown-original-id");
    assert_eq!(query_activation_outcome(&pool,&parsed(&changed)).await.unwrap(),CommitResolution::Unknown);
}

#[sqlx::test]
async fn pg_non_owner_database_role_cannot_enter_activation(pool: sqlx::PgPool) {
    let _ = pg_fixture(&pool).await;
    let role = format!("a01_synthetic_runtime_{}",rand::random::<u64>());
    let mut tx = pool.begin().await.unwrap();
    // Role/USAGE grants are transaction-local and rolled back with this test.
    sqlx::raw_sql(&format!("CREATE ROLE {role} NOLOGIN; GRANT USAGE ON SCHEMA core_identity,core_migration TO {role}; SET LOCAL ROLE {role};"))
        .execute(&mut *tx).await.unwrap();
    assert_eq!(admin_approval::require_owner(&mut tx).await,Err(ApprovalError::OperatorDenied));
    tx.rollback().await.unwrap();
}

#[sqlx::test]
async fn pg_live_gate_needs_no_write_privilege_on_policy(pool: sqlx::PgPool) {
    let (value, _) = pg_fixture(&pool).await;
    let signed = verify_signed_approval(&signed_bytes(&value,&[7;32],"synthetic-operator"),&[7;32]).unwrap();
    apply_approved_activation(&pool,&signed,&policy(1)).await.unwrap();
    let store = identity::IdentityStore::from_pool(pool.clone());
    let issued = store.login(&identity::LoginRequest {
        login_name:"a01-fixture".into(), password:"synthetic-a01-test-password-only".into(),
    }).await.unwrap();
    let actor = store.authorize(&issued.secret).await.unwrap();
    let role = format!("a01_policy_reader_{}",rand::random::<u64>());
    let mut tx = pool.begin().await.unwrap();
    sqlx::raw_sql(&format!("CREATE ROLE {role} NOLOGIN; GRANT USAGE ON SCHEMA core_identity TO {role}; GRANT SELECT ON ALL TABLES IN SCHEMA core_identity TO {role}; GRANT UPDATE ON core_identity.users,core_identity.accounts,core_identity.credentials TO {role}; SET LOCAL ROLE {role};"))
        .execute(&mut *tx).await.unwrap();
    let can_write: bool = sqlx::query_scalar("SELECT has_table_privilege('core_identity.admin_policies','INSERT,UPDATE,DELETE')")
        .fetch_one(&mut *tx).await.unwrap();
    assert!(!can_write);
    assert!(permissions::require_platform_in_transaction(&mut tx,&actor,
        permissions::Permission::ChannelRead,permissions::ResourceScope::ChannelCollection).await.is_ok());
    tx.rollback().await.unwrap();
}

// Authentication happened before these state changes. The operation must not
// trust that earlier result, even though the caller has a genuine Principal.
async fn pg_active_principal(pool: &sqlx::PgPool) -> identity::Principal {
    let (value, _) = pg_fixture(pool).await;
    let signed = verify_signed_approval(
        &signed_bytes(&value, &[7; 32], "synthetic-operator"),
        &[7; 32],
    ).unwrap();
    apply_approved_activation(pool, &signed, &policy(1)).await.unwrap();
    let store = identity::IdentityStore::from_pool(pool.clone());
    let issued = store.login(&identity::LoginRequest {
        login_name: "a01-fixture".into(),
        password: "synthetic-a01-test-password-only".into(),
    }).await.unwrap();
    store.authorize(&issued.secret).await.unwrap()
}

#[sqlx::test]
async fn pg_live_gate_rechecks_revoked_principal(pool: sqlx::PgPool) {
    let actor = pg_active_principal(&pool).await;
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(actor.credential_id).execute(&pool).await.unwrap();
    let before = pg_snapshot(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(permissions::require_platform_in_transaction(
        &mut tx, &actor, permissions::Permission::ChannelRead,
        permissions::ResourceScope::ChannelCollection,
    ).await, Err(identity::IdentityError::Unauthorized));
    tx.rollback().await.unwrap();
    assert_eq!(before, pg_snapshot(&pool).await);
}

#[sqlx::test]
async fn pg_live_gate_rechecks_disabled_account_and_expiry(pool: sqlx::PgPool) {
    let actor = pg_active_principal(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE core_identity.accounts SET active=FALSE WHERE id=$1")
        .bind(actor.owner_account_id).execute(&mut *tx).await.unwrap();
    assert_eq!(permissions::require_platform_in_transaction(
        &mut tx, &actor, permissions::Permission::ChannelRead,
        permissions::ResourceScope::ChannelCollection,
    ).await, Err(identity::IdentityError::Unauthorized));
    tx.rollback().await.unwrap();
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1")
        .bind(actor.credential_id).execute(&pool).await.unwrap();
    let before = pg_snapshot(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(permissions::require_platform_in_transaction(
        &mut tx, &actor, permissions::Permission::ChannelRead,
        permissions::ResourceScope::ChannelCollection,
    ).await, Err(identity::IdentityError::Unauthorized));
    tx.rollback().await.unwrap();
    assert_eq!(before, pg_snapshot(&pool).await);
}

#[sqlx::test]
async fn pg_live_gate_rechecks_browser_revocation(pool: sqlx::PgPool) {
    let actor = pg_active_principal(&pool).await;
    let version: i64 = sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
        .bind(actor.user_id).fetch_one(&pool).await.unwrap();
    sqlx::query("INSERT INTO core_identity.web_sessions(sid,user_id,user_version,current_credential_id,refresh_digest,ip,user_agent,revoked_at) VALUES ($1,$2,$3,$4,$5,'127.0.0.1','synthetic-a01-revoked',clock_timestamp())")
        .bind("d".repeat(64)).bind(actor.user_id).bind(version)
        .bind(actor.credential_id).bind(vec![5_u8; 32]).execute(&pool).await.unwrap();
    let before = pg_snapshot(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    assert_eq!(permissions::require_platform_in_transaction(
        &mut tx, &actor, permissions::Permission::ChannelRead,
        permissions::ResourceScope::ChannelCollection,
    ).await, Err(identity::IdentityError::Unauthorized));
    tx.rollback().await.unwrap();
    assert_eq!(before, pg_snapshot(&pool).await);
}

// Byte-identical to the S11 schema patch; never installed by production code here.
const ISOLATED_A01_SCHEMA: &str = r#"-- S11-only additive fresh-install component. Never run on a frozen/live target.
-- No context, approval key, source proof or administrator is seeded here.
CREATE TABLE core_identity.admin_policies (
    user_id BIGINT PRIMARY KEY REFERENCES core_identity.users(id),
    role TEXT NOT NULL CHECK (role IN ('admin','superadmin')),
    version BIGINT NOT NULL CHECK (version > 0),
    contract_version TEXT NOT NULL CHECK (contract_version = 'a01-permissions-v2'),
    effects SMALLINT[] NOT NULL CHECK (
        array_ndims(effects)=1 AND array_lower(effects,1)=1 AND cardinality(effects)=9
        AND array_position(effects,NULL) IS NULL AND effects <@ ARRAY[0,1,2]::SMALLINT[]),
    fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint)=32)
);
CREATE TABLE core_migration.admin_activation_context (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    target_instance TEXT NOT NULL CHECK (target_instance ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'),
    contract_version TEXT NOT NULL CHECK (contract_version='a01-permissions-v2'),
    gate_manifest_sha256 BYTEA NOT NULL CHECK (octet_length(gate_manifest_sha256)=32)
);
-- Key HASH only. The corresponding 32-byte HMAC key is in an owner-only file.
CREATE TABLE core_migration.admin_approval_keys (
    key_id TEXT PRIMARY KEY CHECK (key_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'),
    key_hash BYTEA NOT NULL CHECK (octet_length(key_hash)=32),
    enabled BOOLEAN NOT NULL DEFAULT TRUE
);
-- Staging must verify the original complete A2 mapping against import_runs
-- before selecting one record. This schema does not implement that staging step.
CREATE TABLE core_migration.admin_activation_sources (
    target_user_id BIGINT PRIMARY KEY REFERENCES core_identity.users(id),
    source_user_id BIGINT NOT NULL CHECK (source_user_id>0),
    migration_id TEXT NOT NULL REFERENCES core_migration.import_runs(migration_id),
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
    source_a2_fact_sha256 BYTEA NOT NULL CHECK (octet_length(source_a2_fact_sha256)=32),
    imported_auth_version BIGINT NOT NULL CHECK (imported_auth_version>0),
    platform_role TEXT NOT NULL CHECK (platform_role IN ('admin','superadmin')),
    blocked_reasons TEXT[] NOT NULL CHECK (array_position(blocked_reasons,NULL) IS NULL),
    verifier_sha256 BYTEA NOT NULL CHECK (octet_length(verifier_sha256)=32),
    second_factor_required BOOLEAN NOT NULL,
    legacy_bcrypt_allowed BOOLEAN NOT NULL
);
-- This immutable receipt is the offline operator audit. No identity sequence is
-- used, and the dormant target is not misrepresented as the approving actor.
CREATE TABLE core_migration.admin_activation_receipts (
    approval_id TEXT PRIMARY KEY CHECK (approval_id ~ '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash)=32),
    target_user_id BIGINT NOT NULL UNIQUE REFERENCES core_migration.admin_activation_sources(target_user_id),
    new_auth_version BIGINT NOT NULL CHECK (new_auth_version>2),
    policy_version BIGINT NOT NULL CHECK (policy_version>0),
    policy_fingerprint BYTEA NOT NULL CHECK (octet_length(policy_fingerprint)=32),
    operator_key_id TEXT NOT NULL REFERENCES core_migration.admin_approval_keys(key_id),
    database_role TEXT NOT NULL,
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    source_a2_fact_sha256 BYTEA NOT NULL CHECK (octet_length(source_a2_fact_sha256)=32),
    gate_manifest_sha256 BYTEA NOT NULL CHECK (octet_length(gate_manifest_sha256)=32),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE FUNCTION core_identity.guard_admin_policy() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        RAISE EXCEPTION 'admin policy history is retained' USING ERRCODE='23514';
    END IF;
    IF TG_OP='INSERT' AND NEW.version<>1 THEN
        RAISE EXCEPTION 'first admin policy version must be one' USING ERRCODE='23514';
    ELSIF TG_OP='UPDATE' AND (NEW.user_id<>OLD.user_id OR NEW.version<>OLD.version+1) THEN
        RAISE EXCEPTION 'admin policy version conflict' USING ERRCODE='23514';
    END IF;
    IF NOT EXISTS(SELECT 1 FROM core_identity.users WHERE id=NEW.user_id AND platform_role=NEW.role) THEN
        RAISE EXCEPTION 'admin policy role mismatch' USING ERRCODE='23514';
    END IF;
    -- Offline writers must lock users before touching a policy row. An active
    -- policy change invalidates old credentials in the same transaction.
    -- A dormant imported user's activation has its own final generation change.
    IF TG_OP IN ('INSERT','UPDATE') THEN
        UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=NEW.user_id AND active;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER admin_policy_guard BEFORE INSERT OR UPDATE OR DELETE
    ON core_identity.admin_policies FOR EACH ROW EXECUTE FUNCTION core_identity.guard_admin_policy();
CREATE TRIGGER admin_policy_truncate_guard BEFORE TRUNCATE
    ON core_identity.admin_policies FOR EACH STATEMENT EXECUTE FUNCTION core_identity.guard_admin_policy();
CREATE FUNCTION core_migration.admin_proof_immutable() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'admin activation proof is immutable' USING ERRCODE='23514';
END $$;
CREATE TRIGGER admin_source_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON core_migration.admin_activation_sources FOR EACH STATEMENT EXECUTE FUNCTION core_migration.admin_proof_immutable();
CREATE TRIGGER admin_receipt_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON core_migration.admin_activation_receipts FOR EACH STATEMENT EXECUTE FUNCTION core_migration.admin_proof_immutable();
CREATE TRIGGER admin_context_immutable BEFORE UPDATE OR DELETE OR TRUNCATE
    ON core_migration.admin_activation_context FOR EACH STATEMENT EXECUTE FUNCTION core_migration.admin_proof_immutable();
REVOKE ALL ON core_identity.admin_policies FROM PUBLIC;
REVOKE ALL ON core_migration.admin_activation_context, core_migration.admin_approval_keys,
    core_migration.admin_activation_sources, core_migration.admin_activation_receipts FROM PUBLIC;
REVOKE ALL ON FUNCTION core_identity.guard_admin_policy(), core_migration.admin_proof_immutable() FROM PUBLIC;
-- S11's runtime ACL may add SELECT only on admin_policies. No runtime grants on
-- core_migration, no sequence grants, no SECURITY DEFINER activation function.
"#;
