//! Single-identity offline approval validation and an owner-only activation writer.
//!
//! Parsing alone grants nothing. The writer additionally requires a verified
//! HMAC, protected original-source evidence, a reviewed gate-manifest binding,
//! and matching target identity/policy versions. It changes only that identity's
//! policy, login flag, credential revocations, generation and activation receipt.
//! Source-proof staging, gate certification and CLI wiring are separate S11 work.
use super::permissions::{CONTRACT_VERSION, PlatformRole, PolicySnapshot};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};

const MAX_APPROVAL_BYTES: usize = 16 * 1024;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(tag = "state", rename_all = "snake_case", deny_unknown_fields)]
enum ExpectedPolicy {
    Absent,
    Present { version: i64, sha256: String },
}

/// One user only. Duplicate fields, unknown fields and a list of users reject.
/// JSON is an offline approval artifact, not a product runtime configuration.
#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct ApprovalDraft {
    format_version: u32,
    approval_id: String,
    source_archive_sha256: String,
    source_a2_fact_sha256: String,
    gate_manifest_sha256: String,
    source_user_id: i64,
    target_user_id: i64,
    target_instance: String,
    contract_version: String,
    platform_role: String,
    expected_auth_version: i64,
    expected_policy: ExpectedPolicy,
    proposed_policy_sha256: String,
    expires_at_unix_seconds: i64,
}

/// Shape checked only; deliberately not named Approved or Authorized.
#[derive(Clone)]
pub(crate) struct ParsedApproval {
    draft: ApprovalDraft,
    archive_hash: [u8; 32],
    a2_fact_hash: [u8; 32],
    gate_manifest_hash: [u8; 32],
    proposed_policy_hash: [u8; 32],
    expected_policy: Option<PolicyBinding>,
    role: PlatformRole,
    request_hash: [u8; 32],
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct PolicyBinding {
    pub(crate) version: i64,
    pub(crate) sha256: [u8; 32],
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum ApprovalError {
    Invalid,
    WrongContract,
    WrongSource,
    WrongTarget,
    RoleChange,
    OtherBlocker,
    AuthenticationUnproven,
    Expired,
    Conflict,
    PolicyMismatch,
    VersionExhausted,
    SignatureInvalid,
    OperatorDenied,
    GateProofMismatch,
    Storage,
}
fn hash_from_hex(value: &str) -> Result<[u8; 32], ApprovalError> {
    if value.len() != 64 { return Err(ApprovalError::Invalid); }
    fn nibble(byte: u8) -> Result<u8, ApprovalError> {
        match byte {
            b'0'..=b'9' => Ok(byte - b'0'),
            b'a'..=b'f' => Ok(byte - b'a' + 10),
            _ => Err(ApprovalError::Invalid),
        }
    }
    let mut result = [0; 32];
    for (index, pair) in value.as_bytes().chunks_exact(2).enumerate() {
        result[index] = nibble(pair[0])? * 16 + nibble(pair[1])?;
    }
    Ok(result)
}
fn valid_identifier(value: &str) -> bool {
    !value.is_empty() && value.len() <= 128
        && value.as_bytes()[0].is_ascii_alphanumeric()
        && value.bytes().all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-' | b'.'))
}

pub(crate) fn parse_approval(bytes: &[u8]) -> Result<ParsedApproval, ApprovalError> {
    if bytes.len() > MAX_APPROVAL_BYTES { return Err(ApprovalError::Invalid); }
    let draft: ApprovalDraft = serde_json::from_slice(bytes).map_err(|_| ApprovalError::Invalid)?;
    if draft.format_version != 2 || draft.contract_version != CONTRACT_VERSION {
        return Err(ApprovalError::WrongContract);
    }
    if !valid_identifier(&draft.approval_id) || !valid_identifier(&draft.target_instance)
        || draft.source_user_id <= 0 || draft.target_user_id <= 0
        || draft.expected_auth_version <= 0 || draft.expires_at_unix_seconds <= 0 {
        return Err(ApprovalError::Invalid);
    }
    draft.expected_auth_version.checked_add(2).ok_or(ApprovalError::VersionExhausted)?;
    let role = PlatformRole::from_storage(&draft.platform_role).map_err(|_| ApprovalError::Invalid)?;
    if !role.is_admin() { return Err(ApprovalError::RoleChange); }
    let archive_hash = hash_from_hex(&draft.source_archive_sha256)?;
    let a2_fact_hash = hash_from_hex(&draft.source_a2_fact_sha256)?;
    let gate_manifest_hash = hash_from_hex(&draft.gate_manifest_sha256)?;
    let proposed_policy_hash = hash_from_hex(&draft.proposed_policy_sha256)?;
    let expected_policy = match &draft.expected_policy {
        ExpectedPolicy::Absent => None,
        ExpectedPolicy::Present { version, sha256 } => {
            if *version <= 0 { return Err(ApprovalError::Invalid); }
            version.checked_add(1).ok_or(ApprovalError::VersionExhausted)?;
            Some(PolicyBinding { version: *version, sha256: hash_from_hex(sha256)? })
        }
    };
    // Declared struct/variant field order is part of approval format version 2.
    // Whitespace/key-order differences in input do not create a new approval.
    let canonical = serde_json::to_vec(&draft).map_err(|_| ApprovalError::Invalid)?;
    let mut hash = Sha256::new();
    hash.update(b"lmm-a01-approval-draft-v2\0");
    hash.update(canonical);
    let request_hash = hash.finalize().into();
    Ok(ParsedApproval { draft, archive_hash, a2_fact_hash, gate_manifest_hash, proposed_policy_hash,
        expected_policy, role, request_hash })
}

/// Construct only from the verified original archive/A2 material, not a request.
/// The A2 source fact is immutable. This preflight never reruns strict import.
pub(crate) struct SourceFacts<'a> {
    pub(crate) archive_hash: [u8; 32],
    pub(crate) a2_fact_hash: [u8; 32],
    pub(crate) source_user_id: i64,
    pub(crate) target_user_id: i64,
    pub(crate) imported_auth_version: i64,
    pub(crate) role: PlatformRole,
    pub(crate) blocked_reasons: &'a [&'a str],
    pub(crate) verifier_available: bool,
    pub(crate) second_factor_required: bool,
}
/// A transaction-consistent target snapshot; no caller-reported role or version.
pub(crate) struct TargetFacts<'a> {
    pub(crate) instance: &'a str,
    pub(crate) user_id: i64,
    pub(crate) role: PlatformRole,
    pub(crate) active: bool,
    pub(crate) account_active: bool,
    pub(crate) password_login_allowed: bool,
    pub(crate) second_factor_required: bool,
    pub(crate) auth_version: i64,
    pub(crate) policy: Option<PolicyBinding>,
}
/// Binding check result only. It has no database or authentication authority.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) struct CheckedBindings {
    pub(crate) target_user_id: i64,
    pub(crate) expected_auth_version: i64,
    pub(crate) next_auth_version: i64,
    pub(crate) expected_policy: Option<PolicyBinding>,
    pub(crate) next_policy: PolicyBinding,
    pub(crate) request_hash: [u8; 32],
}
pub(crate) fn check_bindings(
    approval: &ParsedApproval,
    source: &SourceFacts<'_>,
    target: &TargetFacts<'_>,
    proposed_policy: &PolicySnapshot,
    now_unix_seconds: i64,
) -> Result<CheckedBindings, ApprovalError> {
    let draft = &approval.draft;
    if now_unix_seconds < 0 { return Err(ApprovalError::Invalid); }
    if now_unix_seconds >= draft.expires_at_unix_seconds { return Err(ApprovalError::Expired); }
    if source.archive_hash != approval.archive_hash || source.a2_fact_hash != approval.a2_fact_hash
        || source.source_user_id != draft.source_user_id {
        return Err(ApprovalError::WrongSource);
    }
    if source.target_user_id != draft.target_user_id || target.user_id != draft.target_user_id
        || target.instance != draft.target_instance {
        return Err(ApprovalError::WrongTarget);
    }
    if source.role != approval.role || target.role != approval.role
        || proposed_policy.role() != approval.role {
        return Err(ApprovalError::RoleChange);
    }
    if source.blocked_reasons.len() != 1
        || source.blocked_reasons[0] != "privileged_permissions_unmapped"
    {
        return Err(ApprovalError::OtherBlocker);
    }
    if !source.verifier_available || source.second_factor_required
        || !target.account_active || target.password_login_allowed || target.second_factor_required {
        return Err(ApprovalError::AuthenticationUnproven);
    }
    if target.active || target.auth_version != draft.expected_auth_version
        || source.imported_auth_version != draft.expected_auth_version
        || target.policy != approval.expected_policy {
        return Err(ApprovalError::Conflict);
    }
    let next_policy_version = match target.policy {
        None => 1,
        Some(binding) => binding.version.checked_add(1).ok_or(ApprovalError::VersionExhausted)?,
    };
    if proposed_policy.subject_user_id() != draft.target_user_id
        || proposed_policy.version() != next_policy_version
        || proposed_policy.fingerprint() != approval.proposed_policy_hash {
        return Err(ApprovalError::PolicyMismatch);
    }
    Ok(CheckedBindings {
        target_user_id: target.user_id,
        expected_auth_version: target.auth_version,
        next_auth_version: target.auth_version.checked_add(2).ok_or(ApprovalError::VersionExhausted)?,
        expected_policy: target.policy,
        next_policy: PolicyBinding { version: next_policy_version, sha256: approval.proposed_policy_hash },
        request_hash: approval.request_hash,
    })
}

/// Read from the durable owner-only approval receipt store, not from HTTP JSON.
pub(crate) struct DurableReceipt<'a> {
    pub(crate) approval_id: &'a str,
    pub(crate) request_hash: [u8; 32],
    pub(crate) target_user_id: i64,
    pub(crate) new_auth_version: i64,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum WriterObservation {
    Unknown,
    /// Caller has proved the original writer ended AND acquired its DB fence.
    /// This enum does not itself acquire or prove that fence.
    FencedAfterWriterEnded,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub(crate) enum CommitResolution {
    Committed { new_auth_version: i64 },
    Unknown,
    NotApplied,
    Conflict,
}
/// Resolve the original approval ID. Never generate a replacement ID on timeout.
/// Re-querying absent data without the original writer fence cannot prove rollback.
pub(crate) fn resolve_outcome(
    approval: &ParsedApproval,
    receipt: Option<&DurableReceipt<'_>>,
    writer: WriterObservation,
) -> CommitResolution {
    match receipt {
        Some(receipt) if receipt.approval_id == approval.draft.approval_id
            && receipt.request_hash == approval.request_hash
            && receipt.target_user_id == approval.draft.target_user_id
            && Some(receipt.new_auth_version) == approval.draft.expected_auth_version.checked_add(2) => {
                CommitResolution::Committed { new_auth_version: receipt.new_auth_version }
            }
        Some(_) => CommitResolution::Conflict,
        None if writer == WriterObservation::FencedAfterWriterEnded => CommitResolution::NotApplied,
        None => CommitResolution::Unknown,
    }
}

impl From<sqlx::Error> for ApprovalError {
    fn from(error: sqlx::Error) -> Self {
        if let Some(db) = error.as_database_error()
            && matches!(db.code().as_deref(), Some("23505" | "40001" | "40P01")) {
            return Self::Conflict;
        }
        Self::Storage
    }
}

/// Authenticated *offline* envelope. The owner command must read the HMAC key
/// from a private file; that file loader is not implemented by this module.
/// Never send the key by HTTP or give it to the runtime. The database also checks
/// that its hash belongs to an enabled operator. HMAC is shared-key authentication,
/// not a public-key signature that permits an independent untrusted verifier.
#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct SignedEnvelope {
    operator_key_id: String,
    approval: ApprovalDraft,
    signature_sha256: String,
}

#[derive(Clone)]
pub(crate) struct VerifiedApproval {
    approval: ParsedApproval,
    operator_key_id: String,
    key_hash: [u8; 32],
}

pub(crate) fn verify_signed_approval(
    bytes: &[u8],
    owner_key: &[u8],
) -> Result<VerifiedApproval, ApprovalError> {
    use hmac::{Hmac, Mac};
    if bytes.len() > 20 * 1024 || owner_key.len() != 32 {
        return Err(ApprovalError::Invalid);
    }
    let envelope: SignedEnvelope = serde_json::from_slice(bytes)
        .map_err(|_| ApprovalError::Invalid)?;
    if !valid_identifier(&envelope.operator_key_id) {
        return Err(ApprovalError::Invalid);
    }
    let approval = parse_approval(&serde_json::to_vec(&envelope.approval)
        .map_err(|_| ApprovalError::Invalid)?)?;
    let signature = hash_from_hex(&envelope.signature_sha256)?;
    let mut mac = <Hmac<Sha256> as Mac>::new_from_slice(owner_key)
        .map_err(|_| ApprovalError::Invalid)?;
    mac.update(b"lmm-a01-signed-approval-v2\0");
    mac.update(&(envelope.operator_key_id.len() as u64).to_be_bytes());
    mac.update(envelope.operator_key_id.as_bytes());
    mac.update(&approval.request_hash);
    mac.verify_slice(&signature).map_err(|_| ApprovalError::SignatureInvalid)?;
    Ok(VerifiedApproval {
        approval, operator_key_id: envelope.operator_key_id,
        key_hash: Sha256::digest(owner_key).into(),
    })
}

/// Refuse even a runtime login that inherited some individual UPDATE grants.
/// All these relations must be owned by the *effective* offline database role.
/// This is not a replacement for the installer/restricted-runtime ACL checks.
pub(crate) async fn require_owner(c: &mut sqlx::PgConnection) -> Result<(), ApprovalError> {
    let owns: bool = sqlx::query_scalar(
        "SELECT count(*)=8 AND coalesce(bool_and(relowner=current_user::regrole::oid),false) \
         FROM pg_catalog.pg_class WHERE oid=ANY(ARRAY[ \
         'core_identity.users'::regclass, 'core_identity.user_profiles'::regclass, \
         'core_identity.password_logins'::regclass, 'core_identity.admin_policies'::regclass, \
         'core_migration.admin_activation_context'::regclass, \
         'core_migration.admin_activation_sources'::regclass, \
         'core_migration.admin_approval_keys'::regclass, \
         'core_migration.admin_activation_receipts'::regclass]::oid[])",
    ).fetch_one(c).await?;
    if !owns { return Err(ApprovalError::OperatorDenied); }
    Ok(())
}

async fn receipt(
    c: &mut sqlx::PgConnection,
    approval: &ParsedApproval,
) -> Result<CommitResolution, ApprovalError> {
    use sqlx::Row;
    let row = sqlx::query(
        "SELECT request_hash,target_user_id,new_auth_version \
         FROM core_migration.admin_activation_receipts WHERE approval_id=$1",
    ).bind(&approval.draft.approval_id).fetch_optional(c).await?;
    let Some(row) = row else { return Ok(CommitResolution::Unknown); };
    let hash: Vec<u8> = row.try_get("request_hash")?;
    let request_hash: [u8; 32] = hash.try_into().map_err(|_| ApprovalError::Storage)?;
    Ok(resolve_outcome(approval, Some(&DurableReceipt {
        approval_id: &approval.draft.approval_id,
        request_hash, target_user_id: row.try_get("target_user_id")?,
        new_auth_version: row.try_get("new_auth_version")?,
    }), WriterObservation::Unknown))
}

/// Resolve the original ID even after expiry or operator-key rotation.
/// An absent row stays UNKNOWN. This function never retries the write or
/// assumes that the original process/transaction has terminated.
pub(crate) async fn query_activation_outcome(
    pool: &sqlx::PgPool,
    approval: &ParsedApproval,
) -> Result<CommitResolution, ApprovalError> {
    let mut tx = pool.begin().await?;
    require_owner(&mut tx).await?;
    let outcome = receipt(&mut tx, approval).await?;
    tx.rollback().await?;
    Ok(outcome)
}

/// Apply exactly one owner-signed approval. No SQL DDL, bootstrap, source replay,
/// role promotion or financial write occurs here. The installer must provision
/// a reviewed source/gate certificate before this function can succeed.
///
/// Each missing prerequisite fails closed. Installation of a certificate is NOT
/// provided by this function and cannot be supplied by a network request.
pub(crate) async fn apply_approved_activation(
    pool: &sqlx::PgPool,
    signed: &VerifiedApproval,
    proposed: &PolicySnapshot,
) -> Result<CommitResolution, ApprovalError> {
    use sqlx::Row;
    let approval = &signed.approval;
    let draft = &approval.draft;
    let mut tx = pool.begin().await?;
    sqlx::query("SET LOCAL synchronous_commit=on").execute(&mut *tx).await?;
    sqlx::query("SET LOCAL lock_timeout='3s'").execute(&mut *tx).await?;
    require_owner(&mut tx).await?;
    // A stable, transaction-owned fence for this approval ID. No global lock,
    // no lock based on a password or bearer, no changing IDs on retry.
    sqlx::query(
        "SELECT pg_advisory_xact_lock(hashtextextended('a01-approval:'||$1,0))",
    ).bind(&draft.approval_id).execute(&mut *tx).await?;
    match receipt(&mut tx, approval).await? {
        outcome @ CommitResolution::Committed { .. } => {
            tx.rollback().await?;
            return Ok(outcome);
        }
        CommitResolution::Conflict => return Err(ApprovalError::Conflict),
        _ => (),
    }
    let authority: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM core_migration.admin_approval_keys \
         WHERE key_id=$1 AND key_hash=$2 AND enabled FOR SHARE)",
    ).bind(&signed.operator_key_id).bind(signed.key_hash.as_slice())
        .fetch_one(&mut *tx).await?;
    if !authority { return Err(ApprovalError::OperatorDenied); }
    let context = sqlx::query(
        "SELECT target_instance,contract_version,gate_manifest_sha256 \
         FROM core_migration.admin_activation_context WHERE singleton FOR SHARE",
    ).fetch_optional(&mut *tx).await?.ok_or(ApprovalError::GateProofMismatch)?;
    let instance: String = context.try_get("target_instance")?;
    if context.try_get::<String, _>("contract_version")? != CONTRACT_VERSION
        || context.try_get::<Vec<u8>, _>("gate_manifest_sha256")?.as_slice()
            != approval.gate_manifest_hash.as_slice() {
        return Err(ApprovalError::GateProofMismatch);
    }
    // User first, then policy/profile/verifier: consistent with live authority
    // checks. Competing approvals for the same user serialize on this row.
    let user = sqlx::query(
        "SELECT u.platform_role,u.active,u.auth_version,a.active AS account_active \
         FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id \
         WHERE u.id=$1 FOR UPDATE OF u FOR SHARE OF a",
    ).bind(draft.target_user_id).fetch_optional(&mut *tx).await?
        .ok_or(ApprovalError::WrongTarget)?;
    let role = PlatformRole::from_storage(&user.try_get::<String, _>("platform_role")?)
        .map_err(|_| ApprovalError::RoleChange)?;
    let source = sqlx::query(
        "SELECT s.source_user_id,s.source_archive_sha256,s.source_a2_fact_sha256, \
         s.imported_auth_version,s.platform_role,s.blocked_reasons,s.verifier_sha256, \
         s.second_factor_required,s.legacy_bcrypt_allowed \
         FROM core_migration.admin_activation_sources s \
         JOIN core_migration.import_runs r ON r.migration_id=s.migration_id \
         AND r.source_archive_sha256=s.source_archive_sha256 \
         AND r.mapping_sha256=s.mapping_sha256 \
         WHERE s.target_user_id=$1 FOR SHARE OF s,r",
    ).bind(draft.target_user_id).fetch_optional(&mut *tx).await?
        .ok_or(ApprovalError::WrongSource)?;
    let profile = sqlx::query(
        "SELECT p.password_login_allowed,p.second_factor_required,p.legacy_bcrypt_allowed, \
         sha256(convert_to(l.password_hash,'UTF8')) AS verifier_sha256 \
         FROM core_identity.user_profiles p \
         JOIN core_identity.password_logins l ON l.user_id=p.user_id \
         WHERE p.user_id=$1 FOR UPDATE OF p FOR SHARE OF l",
    ).bind(draft.target_user_id).fetch_optional(&mut *tx).await?
        .ok_or(ApprovalError::AuthenticationUnproven)?;
    let blockers: Vec<String> = source.try_get("blocked_reasons")?;
    let blocker_refs: Vec<&str> = blockers.iter().map(String::as_str).collect();
    let source_facts = SourceFacts {
        archive_hash: hash_from_hex(&source.try_get::<String, _>("source_archive_sha256")?)?,
        a2_fact_hash: source.try_get::<Vec<u8>, _>("source_a2_fact_sha256")?
            .try_into().map_err(|_| ApprovalError::Storage)?,
        source_user_id: source.try_get("source_user_id")?,
        target_user_id: draft.target_user_id,
        imported_auth_version: source.try_get("imported_auth_version")?,
        role: PlatformRole::from_storage(&source.try_get::<String, _>("platform_role")?)
            .map_err(|_| ApprovalError::RoleChange)?,
        blocked_reasons: &blocker_refs,
        verifier_available: profile.try_get::<Vec<u8>, _>("verifier_sha256")?
            == source.try_get::<Vec<u8>, _>("verifier_sha256")?
            && profile.try_get::<bool, _>("legacy_bcrypt_allowed")?
                == source.try_get::<bool, _>("legacy_bcrypt_allowed")?,
        second_factor_required: source.try_get("second_factor_required")?,
    };
    let policy_row = sqlx::query(
        "SELECT role,version,contract_version,effects,fingerprint \
         FROM core_identity.admin_policies WHERE user_id=$1 FOR UPDATE",
    ).bind(draft.target_user_id).fetch_optional(&mut *tx).await?;
    let old_policy = policy_row.map(|r| -> Result<PolicyBinding, ApprovalError> {
        let policy = PolicySnapshot::from_storage(
            draft.target_user_id, &r.try_get::<String, _>("role")?, r.try_get("version")?,
            &r.try_get::<String, _>("contract_version")?,
            &r.try_get::<Vec<i16>, _>("effects")?, &r.try_get::<Vec<u8>, _>("fingerprint")?,
        ).map_err(|_| ApprovalError::PolicyMismatch)?;
        Ok(PolicyBinding { version: policy.version(), sha256: policy.fingerprint() })
    }).transpose()?;
    let target = TargetFacts {
        instance: &instance, user_id: draft.target_user_id, role,
        active: user.try_get("active")?, account_active: user.try_get("account_active")?,
        password_login_allowed: profile.try_get("password_login_allowed")?,
        second_factor_required: profile.try_get("second_factor_required")?,
        auth_version: user.try_get("auth_version")?, policy: old_policy,
    };
    let now: i64 = sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
        .fetch_one(&mut *tx).await?;
    let checked = check_bindings(approval, &source_facts, &target, proposed, now)?;
    let query = if let Some(old) = old_policy {
        sqlx::query(
            "UPDATE core_identity.admin_policies SET role=$2,version=$3,contract_version=$4, \
             effects=$5,fingerprint=$6 WHERE user_id=$1 AND version=$7 AND fingerprint=$8",
        ).bind(draft.target_user_id).bind(proposed.role().storage_name())
            .bind(proposed.version()).bind(CONTRACT_VERSION).bind(proposed.stored_effects())
            .bind(proposed.fingerprint().to_vec()).bind(old.version).bind(old.sha256.to_vec())
    } else {
        sqlx::query(
            "INSERT INTO core_identity.admin_policies(user_id,role,version,contract_version,effects,fingerprint) \
             VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (user_id) DO NOTHING",
        ).bind(draft.target_user_id).bind(proposed.role().storage_name())
            .bind(proposed.version()).bind(CONTRACT_VERSION).bind(proposed.stored_effects())
            .bind(proposed.fingerprint().to_vec())
    };
    if query.execute(&mut *tx).await?.rows_affected() != 1 {
        return Err(ApprovalError::Conflict);
    }
    // These are the only credential changes; all history/bindings are retained.
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL")
        .bind(draft.target_user_id).execute(&mut *tx).await?;
    sqlx::query("UPDATE core_identity.web_sessions SET revoked_at=clock_timestamp() WHERE user_id=$1 AND revoked_at IS NULL")
        .bind(draft.target_user_id).execute(&mut *tx).await?;
    // Native profile_policy_generation increments auth_version once here.
    if sqlx::query(
        "UPDATE core_identity.user_profiles SET password_login_allowed=TRUE \
         WHERE user_id=$1 AND NOT password_login_allowed AND NOT second_factor_required",
    ).bind(draft.target_user_id).execute(&mut *tx).await?.rows_affected() != 1 {
        return Err(ApprovalError::Conflict);
    }
    // This append-only receipt is the offline audit. It identifies the real
    // database/operator roles rather than impersonating the dormant target user.
    sqlx::query(
        "INSERT INTO core_migration.admin_activation_receipts \
         (approval_id,request_hash,target_user_id,new_auth_version,policy_version,policy_fingerprint, \
          operator_key_id,database_role,source_archive_sha256,source_a2_fact_sha256,gate_manifest_sha256) \
         VALUES ($1,$2,$3,$4,$5,$6,$7,current_user,$8,$9,$10)",
    ).bind(&draft.approval_id).bind(checked.request_hash.as_slice()).bind(draft.target_user_id)
        .bind(checked.next_auth_version).bind(checked.next_policy.version)
        .bind(checked.next_policy.sha256.as_slice()).bind(&signed.operator_key_id)
        .bind(&draft.source_archive_sha256).bind(approval.a2_fact_hash.as_slice())
        .bind(approval.gate_manifest_hash.as_slice()).execute(&mut *tx).await?;
    // Activation LAST. The second generation increment is explicit, so the
    // existing protect_user trigger does not silently add another increment.
    let updated: Option<i64> = sqlx::query_scalar(
        "UPDATE core_identity.users SET active=TRUE,auth_version=$3 \
         WHERE id=$1 AND NOT active AND auth_version=$2 AND platform_role=$4 \
         AND clock_timestamp()<to_timestamp($5::double precision) RETURNING auth_version",
    ).bind(draft.target_user_id).bind(checked.expected_auth_version + 1)
        .bind(checked.next_auth_version).bind(role.storage_name())
        .bind(draft.expires_at_unix_seconds).fetch_optional(&mut *tx).await?;
    if updated != Some(checked.next_auth_version) { return Err(ApprovalError::Conflict); }
    match tx.commit().await {
        Ok(()) => Ok(CommitResolution::Committed { new_auth_version: checked.next_auth_version }),
        // Could be committed. Do not convert a missing ACK into failure/rollback.
        Err(_) => Ok(CommitResolution::Unknown),
    }
}

impl ParsedApproval {
    /// Public digest of the canonical request; not an authorization credential.
    pub(crate) fn request_hash(&self) -> [u8; 32] { self.request_hash }
}
