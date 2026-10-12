//! Single-identity offline approval bindings, NOT an activation implementation.
//!
//! Parsing and preflight do not authenticate an operator, verify a signature,
//! acquire database locks, revoke credentials or prove business-gate equivalence.
//! No function in this module updates users, accounts, passwords or setup state.
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
    if draft.format_version != 1 || draft.contract_version != CONTRACT_VERSION {
        return Err(ApprovalError::WrongContract);
    }
    if !valid_identifier(&draft.approval_id) || !valid_identifier(&draft.target_instance)
        || draft.source_user_id <= 0 || draft.target_user_id <= 0
        || draft.expected_auth_version <= 0 || draft.expires_at_unix_seconds <= 0 {
        return Err(ApprovalError::Invalid);
    }
    draft.expected_auth_version.checked_add(1).ok_or(ApprovalError::VersionExhausted)?;
    let role = PlatformRole::from_storage(&draft.platform_role).map_err(|_| ApprovalError::Invalid)?;
    if !role.is_admin() { return Err(ApprovalError::RoleChange); }
    let archive_hash = hash_from_hex(&draft.source_archive_sha256)?;
    let a2_fact_hash = hash_from_hex(&draft.source_a2_fact_sha256)?;
    let proposed_policy_hash = hash_from_hex(&draft.proposed_policy_sha256)?;
    let expected_policy = match &draft.expected_policy {
        ExpectedPolicy::Absent => None,
        ExpectedPolicy::Present { version, sha256 } => {
            if *version <= 0 { return Err(ApprovalError::Invalid); }
            version.checked_add(1).ok_or(ApprovalError::VersionExhausted)?;
            Some(PolicyBinding { version: *version, sha256: hash_from_hex(sha256)? })
        }
    };
    // Declared struct/variant field order is part of approval format version 1.
    // Whitespace/key-order differences in input do not create a new approval.
    let canonical = serde_json::to_vec(&draft).map_err(|_| ApprovalError::Invalid)?;
    let mut hash = Sha256::new();
    hash.update(b"lmm-a01-approval-draft-v1\0");
    hash.update(canonical);
    let request_hash = hash.finalize().into();
    Ok(ParsedApproval { draft, archive_hash, a2_fact_hash, proposed_policy_hash,
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
    if !source.verifier_available || source.second_factor_required {
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
        next_auth_version: target.auth_version.checked_add(1).ok_or(ApprovalError::VersionExhausted)?,
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
            && Some(receipt.new_auth_version) == approval.draft.expected_auth_version.checked_add(1) => {
                CommitResolution::Committed { new_auth_version: receipt.new_auth_version }
            }
        Some(_) => CommitResolution::Conflict,
        None if writer == WriterObservation::FencedAfterWriterEnded => CommitResolution::NotApplied,
        None => CommitResolution::Unknown,
    }
}
