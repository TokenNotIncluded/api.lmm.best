//! Target-side verification only. Controller archives and signing keys are never read here.

use std::io::Read;

use base64::{Engine as _, engine::general_purpose::STANDARD};
use ed25519_dalek::{Signature, VerifyingKey};

use super::*;

const MAX_RECEIPT_BYTES: u64 = 64 * 1024;

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ControllerOnlyBackup {
    pub public_key: String,
    pub plan_sha256: String,
    pub receipt_path: PathBuf,
    pub receipt_sha256: String,
}

#[derive(Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Envelope {
    format: u32,
    payload: String,
    signature: String,
}

// A struct, not a map: serde rejects duplicate, unknown AND missing archive kinds.
#[derive(Clone, Debug, Deserialize, Serialize, PartialEq, Eq)]
#[serde(deny_unknown_fields)]
struct ArchiveDigests {
    application: String,
    frontend: String,
    configuration: String,
    database: String,
}

impl ArchiveDigests {
    fn validate(&self) -> Result<(), DeploymentError> {
        for digest in [
            &self.application,
            &self.frontend,
            &self.configuration,
            &self.database,
        ] {
            require_sha256(digest)?;
        }
        Ok(())
    }
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
struct Receipt {
    format: u32,
    purpose: String,
    deployment_id: String,
    expected_host: String,
    plan_sha256: String,
    #[serde(deserialize_with = "utc_timestamp")]
    verified_utc: DateTime<Utc>,
    #[serde(deserialize_with = "utc_timestamp")]
    captured_utc: DateTime<Utc>,
    deployment_manifest_sha256: String,
    backup_set_sha256: String,
    database_schema: String,
    environment_sha256: String,
    go_candidate_sha256: String,
    go_rollback_sha256: String,
    web_candidate_sha256: String,
    web_rollback_sha256: String,
    go_rollback_payload_sha256: String,
    frontend_rollback_sha256: String,
    archive_ciphertexts: ArchiveDigests,
    archive_plaintexts: ArchiveDigests,
}

fn utc_timestamp<'de, D>(deserializer: D) -> Result<DateTime<Utc>, D::Error>
where
    D: serde::Deserializer<'de>,
{
    let value = String::deserialize(deserializer)?;
    let time = DateTime::parse_from_rfc3339(&value).map_err(serde::de::Error::custom)?;
    if time.offset().local_minus_utc() != 0 {
        return Err(serde::de::Error::custom("receipt timestamp must be UTC"));
    }
    Ok(time.with_timezone(&Utc))
}

fn invalid(message: &str) -> DeploymentError {
    DeploymentError::InvalidEvidence(message.to_owned())
}

fn public_key(value: &str) -> Result<VerifyingKey, DeploymentError> {
    require_sha256(value)?;
    let mut bytes = [0_u8; 32];
    hex::decode_to_slice(value, &mut bytes)
        .map_err(|_| invalid("controller backup public key is invalid"))?;
    let key = VerifyingKey::from_bytes(&bytes)
        .map_err(|_| invalid("controller backup public key is invalid"))?;
    if key.is_weak() {
        return Err(invalid("controller backup public key is weak"));
    }
    Ok(key)
}

pub(super) fn validate_plan_mode(plan: &ReleasePlan) -> Result<(), DeploymentError> {
    let no_controller_fields = plan.controller_backup_dir.as_os_str().is_empty()
        && plan.controller_backup_public_key.is_empty();
    if plan.format == LEGACY_RELEASE_PLAN_FORMAT {
        if !plan.backup_mode.is_empty() || !no_controller_fields {
            return Err(DeploymentError::InvalidSchema(
                "legacy release plan contains controller-only backup policy".to_owned(),
            ));
        }
        if plan.go_changed && !plan.with_backups {
            return Err(DeploymentError::InvalidSchema(
                "backend changes require verified three-copy backups".to_owned(),
            ));
        }
        return Ok(());
    }
    // Go's legacy value-struct field serializes the unselected recipient as
    // {"path":"","sha256":""}, even with omitempty. It grants no authority.
    let legacy_recipient_present = plan.age_recipient.as_ref().is_some_and(|recipient| {
        !recipient.path.as_os_str().is_empty() || !recipient.sha256.is_empty()
    });
    if plan.format != RELEASE_PLAN_FORMAT || legacy_recipient_present {
        return Err(DeploymentError::InvalidSchema(
            "release plan contains unsupported backup policy".to_owned(),
        ));
    }
    match plan.backup_mode.as_str() {
        "disabled" if !plan.with_backups && no_controller_fields => Ok(()),
        "controller-only" if plan.with_backups => {
            // This is a controller path, not a target path. Target readers must not
            // access it; the importing controller enforces ownership and no links.
            clean_absolute(&plan.controller_backup_dir)?;
            let normalized: PathBuf = plan.controller_backup_dir.components().collect();
            if normalized.as_os_str() != plan.controller_backup_dir.as_os_str() {
                return Err(DeploymentError::UnsafePath(
                    "controller backup directory must be a clean absolute path".to_owned(),
                ));
            }
            public_key(&plan.controller_backup_public_key)?;
            require_sha256(&plan.go_rollback.payload_sha256)?;
            Ok(())
        }
        _ => Err(DeploymentError::InvalidSchema(
            "release plan backup mode and evidence disagree".to_owned(),
        )),
    }
}

pub(super) fn validate_schema(
    workspace: &Workspace,
    manifest: &ProductionManifest,
) -> Result<(), DeploymentError> {
    let evidence = manifest
        .controller_only_backup
        .as_ref()
        .ok_or_else(|| invalid("controller-only backup metadata is missing"))?;
    if !manifest.backups_enabled
        || manifest.backup_evidence_format != 3
        || !manifest.backup_dir.as_os_str().is_empty()
        || !manifest.target_backup_sha256.is_empty()
        || !manifest.offhost_backup_sha256.is_empty()
        || evidence.receipt_path.as_os_str()
            != workspace
                .state
                .join("controller-backup-receipt.json")
                .as_os_str()
        || !valid_database_schema(&manifest.database_schema)
    {
        return Err(DeploymentError::InvalidSchema(
            "controller-only backup evidence is mixed or invalid".to_owned(),
        ));
    }
    public_key(&evidence.public_key)?;
    for digest in [
        &evidence.plan_sha256,
        &evidence.receipt_sha256,
        &manifest.controller_backup_sha256,
        &manifest.database_backup_sha256,
    ] {
        require_sha256(digest)?;
    }
    Ok(())
}

fn valid_database_schema(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 63
        && value != "information_schema"
        && !value.starts_with("pg_")
        && (value.as_bytes()[0].is_ascii_lowercase() || value.as_bytes()[0] == b'_')
        && value
            .bytes()
            .all(|byte| byte.is_ascii_lowercase() || byte.is_ascii_digit() || byte == b'_')
}

fn decode_receipt(bytes: &[u8], key: &str) -> Result<Receipt, DeploymentError> {
    if bytes.is_empty() || bytes.len() > MAX_RECEIPT_BYTES as usize {
        return Err(invalid("controller backup receipt size is invalid"));
    }
    let envelope: Envelope = serde_json::from_slice(bytes)?;
    if envelope.format != 1
        || envelope.signature.len() != 128
        || !envelope
            .signature
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(invalid("controller backup signature envelope is invalid"));
    }
    let payload = STANDARD
        .decode(&envelope.payload)
        .map_err(|_| invalid("controller backup payload encoding is invalid"))?;
    let mut signature = [0_u8; 64];
    hex::decode_to_slice(&envelope.signature, &mut signature)
        .map_err(|_| invalid("controller backup signature encoding is invalid"))?;
    // Verify the original decoded bytes, never a reserialized JSON representation.
    public_key(key)?
        .verify_strict(&payload, &Signature::from_bytes(&signature))
        .map_err(|_| invalid("controller backup signature verification failed"))?;
    Ok(serde_json::from_slice(&payload)?)
}

fn validate_bindings(
    receipt: &Receipt,
    manifest: &ProductionManifest,
) -> Result<(), DeploymentError> {
    let evidence = manifest
        .controller_only_backup
        .as_ref()
        .ok_or_else(|| invalid("controller-only backup metadata is missing"))?;
    receipt.archive_ciphertexts.validate()?;
    receipt.archive_plaintexts.validate()?;
    for digest in [
        &receipt.plan_sha256,
        &receipt.backup_set_sha256,
        &receipt.environment_sha256,
        &receipt.go_candidate_sha256,
        &receipt.go_rollback_sha256,
        &receipt.web_candidate_sha256,
        &receipt.web_rollback_sha256,
        &receipt.go_rollback_payload_sha256,
        &receipt.frontend_rollback_sha256,
    ] {
        require_sha256(digest)?;
    }
    if receipt.format != 1
        || receipt.deployment_id != manifest.deployment_id
        || receipt.expected_host != EXPECTED_HOST
        || receipt.plan_sha256 != evidence.plan_sha256
        || receipt.backup_set_sha256 != manifest.controller_backup_sha256
        || receipt.database_schema != manifest.database_schema
        || !valid_database_schema(&receipt.database_schema)
        || receipt.environment_sha256 != manifest.environment_restore_sha256
        || receipt.go_candidate_sha256 != manifest.go.candidate_sha256
        || receipt.go_rollback_sha256 != manifest.go.rollback_sha256
        || receipt.web_candidate_sha256 != manifest.web.candidate_sha256
        || receipt.web_rollback_sha256 != manifest.web.rollback_sha256
        || receipt.frontend_rollback_sha256 != manifest.frontend.old_index_sha256
        || receipt.archive_plaintexts.database != manifest.database_backup_sha256
    {
        return Err(invalid(
            "controller backup receipt bindings differ from immutable deployment",
        ));
    }
    // Historical prepare evidence does not expire. Its verification still must
    // describe a recent capture at the time it was originally signed.
    if receipt.captured_utc.timestamp() <= 0
        || receipt.verified_utc.timestamp() <= 0
        || receipt.captured_utc > receipt.verified_utc + chrono::Duration::seconds(30)
        || receipt.verified_utc - receipt.captured_utc > chrono::Duration::hours(24)
    {
        return Err(invalid("controller backup capture time is invalid"));
    }
    Ok(())
}

fn validate_prepare(receipt: &Receipt) -> Result<(), DeploymentError> {
    if receipt.purpose != "prepare" || !receipt.deployment_manifest_sha256.is_empty() {
        return Err(invalid(
            "controller backup initial receipt is not prepare evidence",
        ));
    }
    Ok(())
}

fn validate_confirmation(
    confirmation: &Receipt,
    initial: &Receipt,
    manifest_sha256: &str,
    now: DateTime<Utc>,
) -> Result<(), DeploymentError> {
    require_sha256(manifest_sha256)?;
    if confirmation.purpose != "confirm"
        || confirmation.deployment_manifest_sha256 != manifest_sha256
        || confirmation.verified_utc > now + chrono::Duration::seconds(30)
        || now - confirmation.verified_utc >= chrono::Duration::minutes(5)
        || confirmation.captured_utc != initial.captured_utc
        || confirmation.backup_set_sha256 != initial.backup_set_sha256
        || confirmation.go_rollback_payload_sha256 != initial.go_rollback_payload_sha256
        || confirmation.archive_ciphertexts != initial.archive_ciphertexts
        || confirmation.archive_plaintexts != initial.archive_plaintexts
    {
        return Err(invalid(
            "controller backup confirmation is stale or mismatched",
        ));
    }
    Ok(())
}

fn read_receipt(path: &Path, require_root_owner: bool) -> Result<Vec<u8>, DeploymentError> {
    clean_absolute(path)?;
    if fs::canonicalize(path)?.as_os_str() != path.as_os_str() {
        return Err(DeploymentError::UnsafePath(
            "receipt contains symlink components".to_owned(),
        ));
    }
    let parent = path
        .parent()
        .ok_or_else(|| invalid("receipt has no state directory"))?;
    require_real(parent, true, require_root_owner)?;
    if fs::metadata(parent)?.permissions().mode() & 0o777 != 0o700 {
        return Err(DeploymentError::UnsafePath(
            "receipt state must remain private".to_owned(),
        ));
    }
    let descriptor = rustix::fs::open(
        path,
        rustix::fs::OFlags::RDONLY
            | rustix::fs::OFlags::NOFOLLOW
            | rustix::fs::OFlags::CLOEXEC
            | rustix::fs::OFlags::NONBLOCK,
        rustix::fs::Mode::empty(),
    )
    .map_err(io::Error::from)?;
    let file = File::from(descriptor);
    let metadata = file.metadata()?;
    if !metadata.is_file()
        || metadata.nlink() != 1
        || metadata.len() == 0
        || metadata.len() > MAX_RECEIPT_BYTES
        || metadata.permissions().mode() & 0o7777 != 0o600
        || (require_root_owner && metadata.uid() != 0)
    {
        return Err(DeploymentError::UnsafePath(
            "receipt must be a private single-link file".to_owned(),
        ));
    }
    let mut bytes = Vec::new();
    file.take(MAX_RECEIPT_BYTES + 1).read_to_end(&mut bytes)?;
    if bytes.len() as u64 > MAX_RECEIPT_BYTES || bytes.len() as u64 != metadata.len() {
        return Err(invalid("controller backup receipt changed during read"));
    }
    Ok(bytes)
}

fn read_initial(
    workspace: &Workspace,
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<Receipt, DeploymentError> {
    validate_schema(workspace, manifest)?;
    let evidence = manifest
        .controller_only_backup
        .as_ref()
        .ok_or_else(|| invalid("controller-only backup metadata is missing"))?;
    let bytes = read_receipt(&evidence.receipt_path, require_root_owner)?;
    if sha256_bytes(&bytes) != evidence.receipt_sha256 {
        return Err(invalid("controller backup initial receipt digest changed"));
    }
    let receipt = decode_receipt(&bytes, &evidence.public_key)?;
    validate_prepare(&receipt)?;
    Ok(receipt)
}

pub(super) fn verify_initial(
    workspace: &Workspace,
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let receipt = read_initial(workspace, manifest, require_root_owner)?;
    // Preparation bound the signed payload digest to the then-active N-1 binary.
    // Historical reads must not compare it to the now-active candidate provider.
    validate_bindings(&receipt, manifest)
}

pub(super) fn verify_confirmation(
    workspace: &Workspace,
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let initial = read_initial(workspace, manifest, require_root_owner)?;
    let evidence = manifest
        .controller_only_backup
        .as_ref()
        .ok_or_else(|| invalid("controller-only backup metadata is missing"))?;
    validate_bindings(&initial, manifest)?;
    let bytes = read_receipt(
        &workspace.state.join("controller-backup-confirmation.json"),
        require_root_owner,
    )?;
    let confirmation = decode_receipt(&bytes, &evidence.public_key)?;
    validate_bindings(&confirmation, manifest)?;
    // Pin exact on-disk manifest bytes, including whitespace, not serialization.
    let manifest_bytes = read_receipt(&workspace.manifest, require_root_owner)?;
    let current: ProductionManifest = serde_json::from_slice(&manifest_bytes)?;
    if serde_json::to_vec(&current)? != serde_json::to_vec(manifest)? {
        return Err(invalid("deployment manifest changed during confirmation"));
    }
    validate_confirmation(
        &confirmation,
        &initial,
        &sha256_bytes(&manifest_bytes),
        Utc::now(),
    )
}

#[cfg(test)]
mod tests;
