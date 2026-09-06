//! Native deployment state and target-side recovery commands.
//!
//! Controller-side plan/stage/promote remain fail closed.  The schema readers
//! live here so a Rust operator can reject incompatible evidence without
//! delegating to the retired shell control plane.

use std::{
    collections::BTreeMap,
    fs::{self, File, OpenOptions},
    io::{self, Write},
    os::unix::fs::{MetadataExt, OpenOptionsExt, PermissionsExt},
    path::{Component, Path, PathBuf},
    process::Command,
    time::Duration,
};

use chrono::{DateTime, Utc};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use thiserror::Error;

use crate::provider_link::{
    GENERIC_BINARY, GO_PROVIDER, Provider, ProviderLinkError, ProviderLinkStatus, os_manager,
};

pub const MANIFEST_FORMAT: u32 = 8;
pub const STATUS_FORMAT: u32 = 2;
pub const RELEASE_PLAN_FORMAT: u32 = 5;
pub const RELEASE_STATE_FORMAT: u32 = 3;
pub const MINIMUM_OBSERVATION_SECONDS: i64 = 120;
const MAXIMUM_OBSERVATION_SECONDS: i64 = 360;
const WORK_ROOT: &str = "/var/lib/lmm-api-go-deploy/work";
const BACKUP_ROOT: &str = "/var/lib/lmm-api-go-deploy/backups";
const TRANSACTION_LOCK: &str = "/var/lib/lmm-api-go-deploy/transaction.lock";
const EXPECTED_HOST: &str = "arch-dmit";
const SERVICE: &str = "lmm-api.service";
const FRONTEND_ROOT: &str = "/srv/lmm-api-frontend";

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct PackageTransition {
    pub candidate_package_name: String,
    pub rollback_package_name: String,
    pub changed: bool,
    pub candidate_path: PathBuf,
    pub rollback_path: PathBuf,
    pub candidate_identity: String,
    pub rollback_identity: String,
    pub candidate_sha256: String,
    pub rollback_sha256: String,
    pub candidate_git_revision: String,
    pub rollback_git_revision: String,
    pub candidate_contract_revision: String,
    pub rollback_contract_revision: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct FrontendTransition {
    pub old_target: String,
    pub new_target: String,
    pub old_index_sha256: String,
    pub new_index_sha256: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ProductionManifest {
    pub format: u32,
    pub deployment_id: String,
    pub operator_user: String,
    #[serde(alias = "backend")]
    pub go: PackageTransition,
    pub web: PackageTransition,
    pub frontend: FrontendTransition,
    pub probe_binary: PathBuf,
    pub probe_binary_sha256: String,
    #[serde(default)]
    pub operator_binary: PathBuf,
    #[serde(default)]
    pub operator_binary_sha256: String,
    pub expected_version: String,
    pub old_version: String,
    #[serde(default)]
    pub previous_provider_target: String,
    #[serde(default)]
    pub new_provider_target: String,
    #[serde(default)]
    pub backup_dir: PathBuf,
    pub backups_enabled: bool,
    #[serde(default)]
    pub backup_evidence_format: u32,
    #[serde(default)]
    pub database_backup_sha256: String,
    #[serde(default)]
    pub target_backup_sha256: String,
    #[serde(default)]
    pub controller_backup_sha256: String,
    #[serde(default)]
    pub offhost_backup_sha256: String,
    pub database_schema: String,
    #[serde(default)]
    pub observation_started_utc: Option<DateTime<Utc>>,
    pub observation_seconds: i64,
    pub service_restart_baseline: i64,
    pub config_restore_path: PathBuf,
    pub environment_restore_sha256: String,
    #[serde(default)]
    pub nginx_edge_restore_sha256: String,
    #[serde(default)]
    pub preserve_edge_policy: bool,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ProductionStatus {
    pub format: u32,
    pub deployment_id: String,
    pub phase: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub version: String,
    #[serde(
        default,
        rename = "previous_version",
        skip_serializing_if = "String::is_empty"
    )]
    pub previous: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub reason: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub failure: String,
    pub updated_utc: DateTime<Utc>,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub observation_seconds: i64,
}

const fn is_zero(value: &i64) -> bool {
    *value == 0
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReleaseFilePlan {
    pub path: PathBuf,
    pub sha256: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReleasePackagePlan {
    pub package_path: PathBuf,
    pub package_sha256: String,
    pub name: String,
    pub version: String,
    pub identity: String,
    pub git_revision: String,
    pub contract_revision: String,
    pub payload_sha256: String,
    pub release_asset: PathBuf,
    pub release_asset_sha256: String,
    pub signature_bundle: PathBuf,
    pub signature_bundle_sha256: String,
    pub release_tag: String,
    pub workflow: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct ReleasePlan {
    pub format: u32,
    pub deployment_id: String,
    pub created_utc: DateTime<Utc>,
    pub controller_workspace: PathBuf,
    pub repository: String,
    pub target_alias: String,
    pub expected_host: String,
    pub operator_user: String,
    pub expected_version: String,
    pub go_candidate: ReleasePackagePlan,
    pub go_rollback: ReleasePackagePlan,
    pub web_candidate: ReleasePackagePlan,
    pub web_rollback: ReleasePackagePlan,
    pub probe_binary: ReleaseFilePlan,
    #[serde(default)]
    pub operator_binary: Option<ReleaseFilePlan>,
    pub go_changed: bool,
    pub web_changed: bool,
    pub observation_seconds: i64,
    pub preserve_edge_policy: bool,
    pub with_backups: bool,
    #[serde(default)]
    pub age_recipient: Option<ReleaseFilePlan>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ReleaseState {
    pub format: u32,
    pub deployment_id: String,
    #[serde(flatten)]
    pub fields: BTreeMap<String, serde_json::Value>,
}

#[derive(Debug, Error)]
pub enum DeploymentError {
    #[error(
        "unsupported controller-side operation {0:?}; Rust supports target-side status, confirm, and rollback only"
    )]
    UnsupportedController(String),
    #[error("deployment command must run as root")]
    RootRequired,
    #[error("production host identity mismatch: got {0:?}")]
    HostMismatch(String),
    #[error("unsafe deployment path: {0}")]
    UnsafePath(String),
    #[error("deployment schema is invalid: {0}")]
    InvalidSchema(String),
    #[error("deployment evidence is invalid: {0}")]
    InvalidEvidence(String),
    #[error("deployment phase {0} is not valid for this operation")]
    InvalidPhase(String),
    #[error("confirmation requires a completed observation window of at least 120 seconds")]
    ObservationIncomplete,
    #[error("deployment filesystem operation failed: {0}")]
    Io(#[from] io::Error),
    #[error("deployment JSON operation failed: {0}")]
    Json(#[from] serde_json::Error),
    #[error("deployment command failed: {0}")]
    Command(String),
    #[error("deployment health or identity check failed: {0}")]
    Health(String),
    #[error(transparent)]
    ProviderLink(#[from] ProviderLinkError),
}

#[derive(Clone, Debug)]
pub struct Workspace {
    pub root: PathBuf,
    pub id: String,
    pub state: PathBuf,
    pub staging: PathBuf,
    pub manifest: PathBuf,
    pub status: PathBuf,
    pub probe_token: PathBuf,
}

impl Workspace {
    pub fn open(path: &Path, require_root_owner: bool) -> Result<Self, DeploymentError> {
        Self::open_under(path, Path::new(WORK_ROOT), require_root_owner, true)
    }

    fn open_for_inspection(path: &Path, require_root_owner: bool) -> Result<Self, DeploymentError> {
        Self::open_under(path, Path::new(WORK_ROOT), require_root_owner, false)
    }

    fn open_under(
        path: &Path,
        work_root: &Path,
        require_root_owner: bool,
        require_staging: bool,
    ) -> Result<Self, DeploymentError> {
        let cleaned = clean_absolute(path)?;
        if cleaned.parent() != Some(work_root) {
            return Err(DeploymentError::UnsafePath(
                "workspace must be one direct child of the production work root".to_owned(),
            ));
        }
        let id = cleaned
            .file_name()
            .and_then(|name| name.to_str())
            .filter(|name| valid_identifier(name, 80))
            .ok_or_else(|| DeploymentError::UnsafePath("invalid deployment ID".to_owned()))?
            .to_owned();
        require_real(&cleaned, true, require_root_owner)?;
        let canonical = fs::canonicalize(&cleaned)?;
        if canonical != cleaned {
            return Err(DeploymentError::UnsafePath(
                "workspace contains symlink components".to_owned(),
            ));
        }
        let marker = cleaned.join(".lmm-deploy-workspace");
        let values = read_key_value(&marker, 16 * 1024, require_root_owner)?;
        if values.get("deployment_id") != Some(&id) {
            return Err(DeploymentError::InvalidEvidence(
                "workspace marker does not own deployment ID".to_owned(),
            ));
        }
        let state = cleaned.join("state");
        let staging = cleaned.join("staging");
        require_real(&state, true, require_root_owner)?;
        match fs::symlink_metadata(&staging) {
            Ok(_) => require_real(&staging, true, require_root_owner)?,
            Err(error) if !require_staging && error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        if fs::metadata(&state)?.permissions().mode() & 0o777 != 0o700 {
            return Err(DeploymentError::UnsafePath(
                "deployment state must remain root-only".to_owned(),
            ));
        }
        Ok(Self {
            root: cleaned,
            id,
            manifest: state.join("deployment.json"),
            status: state.join("status.json"),
            probe_token: state.join("probe-token"),
            state,
            staging,
        })
    }

    pub fn read_manifest(
        &self,
        require_root_owner: bool,
    ) -> Result<ProductionManifest, DeploymentError> {
        let bytes = read_private(&self.manifest, 256 * 1024, require_root_owner)?;
        let manifest: ProductionManifest = serde_json::from_slice(&bytes)?;
        validate_manifest(self, &manifest, require_root_owner)?;
        Ok(manifest)
    }

    fn read_manifest_for_rollback(
        &self,
        require_root_owner: bool,
    ) -> Result<ProductionManifest, DeploymentError> {
        let bytes = read_private(&self.manifest, 256 * 1024, require_root_owner)?;
        let manifest: ProductionManifest = serde_json::from_slice(&bytes)?;
        validate_manifest_schema(self, &manifest)?;
        verify_manifest_evidence_for_rollback(&manifest, require_root_owner)?;
        Ok(manifest)
    }

    pub fn read_status(
        &self,
        require_root_owner: bool,
    ) -> Result<ProductionStatus, DeploymentError> {
        let bytes = read_private(&self.status, 64 * 1024, require_root_owner)?;
        let status: ProductionStatus = serde_json::from_slice(&bytes)?;
        if status.format != STATUS_FORMAT
            || status.deployment_id != self.id
            || status.phase.is_empty()
        {
            return Err(DeploymentError::InvalidSchema(
                "deployment status identity is invalid".to_owned(),
            ));
        }
        Ok(status)
    }

    fn write_status(&self, mut status: ProductionStatus) -> Result<(), DeploymentError> {
        status.format = STATUS_FORMAT;
        status.deployment_id.clone_from(&self.id);
        status.updated_utc = Utc::now();
        let mut bytes = serde_json::to_vec_pretty(&status)?;
        bytes.push(b'\n');
        write_atomic_private(&self.status, &bytes)?;
        Ok(())
    }
}

pub fn validate_release_plan(plan: &ReleasePlan) -> Result<(), DeploymentError> {
    if plan.format != RELEASE_PLAN_FORMAT {
        return Err(DeploymentError::InvalidSchema(
            "unsupported release plan format".to_owned(),
        ));
    }
    if !valid_identifier(&plan.deployment_id, 80)
        || plan.target_alias != "ArchDmit"
        || plan.expected_host != EXPECTED_HOST
        || plan.operator_user != "lmm-api-deploy"
        || !(MINIMUM_OBSERVATION_SECONDS..=MAXIMUM_OBSERVATION_SECONDS)
            .contains(&plan.observation_seconds)
    {
        return Err(DeploymentError::InvalidSchema(
            "release plan identity or observation contract is invalid".to_owned(),
        ));
    }
    for digest in [
        &plan.go_candidate.package_sha256,
        &plan.go_candidate.payload_sha256,
        &plan.go_rollback.package_sha256,
        &plan.web_candidate.package_sha256,
        &plan.web_rollback.package_sha256,
        &plan.probe_binary.sha256,
    ] {
        require_sha256(digest)?;
    }
    let operator = plan.operator_binary.as_ref().ok_or_else(|| {
        DeploymentError::InvalidSchema("release plan operator identity is missing".to_owned())
    })?;
    if plan.probe_binary.path.file_name() != Some(std::ffi::OsStr::new(GO_PROVIDER))
        || operator.path != plan.probe_binary.path
        || operator.sha256 != plan.probe_binary.sha256
        || plan.probe_binary.sha256 != plan.go_candidate.payload_sha256
    {
        return Err(DeploymentError::InvalidSchema(
            "release plan candidate provider evidence is invalid".to_owned(),
        ));
    }
    if plan.go_changed && !plan.with_backups {
        return Err(DeploymentError::InvalidSchema(
            "backend changes require verified three-copy backups".to_owned(),
        ));
    }
    Ok(())
}

pub fn validate_release_state(state: &ReleaseState) -> Result<(), DeploymentError> {
    if state.format != RELEASE_STATE_FORMAT || !valid_identifier(&state.deployment_id, 80) {
        return Err(DeploymentError::InvalidSchema(
            "release state identity is invalid".to_owned(),
        ));
    }
    Ok(())
}

pub fn load_release_plan(
    path: &Path,
    expected_sha256: &str,
) -> Result<ReleasePlan, DeploymentError> {
    require_sha256(expected_sha256)?;
    let bytes = read_private(path, 1024 * 1024, false)?;
    if sha256_bytes(&bytes) != expected_sha256 {
        return Err(DeploymentError::InvalidEvidence(
            "release plan SHA-256 mismatch".to_owned(),
        ));
    }
    let plan: ReleasePlan = serde_json::from_slice(&bytes)?;
    validate_release_plan(&plan)?;
    Ok(plan)
}

pub fn target_status(workspace_path: &Path) -> Result<ProductionStatus, DeploymentError> {
    let workspace = Workspace::open_for_inspection(workspace_path, true)?;
    workspace.read_status(true)
}

pub async fn target_confirm(workspace_path: &Path) -> Result<ProductionStatus, DeploymentError> {
    require_production_identity()?;
    let workspace = Workspace::open(workspace_path, true)?;
    validate_transaction_lock(&workspace)?;
    let manifest = workspace.read_manifest(true)?;
    verify_manifest_evidence(&workspace, &manifest, true)?;
    if manifest.backups_enabled && manifest.backup_evidence_format == 2 {
        verify_backup_confirmation(&manifest, true)?;
    }
    let status = workspace.read_status(true)?;
    if status.phase == "CONFIRMED" {
        finalize_transaction(&workspace)?;
        return Ok(status);
    }
    if status.phase != "AWAITING_CONFIRMATION" && status.phase != "CONFIRMING" {
        return Err(DeploymentError::InvalidPhase(status.phase));
    }
    let started = manifest
        .observation_started_utc
        .ok_or(DeploymentError::ObservationIncomplete)?;
    if manifest.observation_seconds < MINIMUM_OBSERVATION_SECONDS
        || Utc::now() < started + chrono::Duration::seconds(manifest.observation_seconds)
    {
        return Err(DeploymentError::ObservationIncomplete);
    }
    verify_active_provider(
        &manifest.go.candidate_package_name,
        &manifest.new_provider_target,
    )?;
    health_check(&manifest, false).await?;
    let confirming = ProductionStatus {
        format: STATUS_FORMAT,
        deployment_id: workspace.id.clone(),
        phase: "CONFIRMING".to_owned(),
        version: manifest.expected_version.clone(),
        previous: manifest.old_version.clone(),
        reason: String::new(),
        failure: String::new(),
        updated_utc: Utc::now(),
        observation_seconds: manifest.observation_seconds,
    };
    workspace.write_status(confirming)?;
    verify_manifest_evidence(&workspace, &manifest, true)?;
    if manifest.backups_enabled && manifest.backup_evidence_format == 2 {
        verify_backup_confirmation(&manifest, true)?;
    }
    verify_active_provider(
        &manifest.go.candidate_package_name,
        &manifest.new_provider_target,
    )?;
    health_check(&manifest, false).await?;
    let confirmed = ProductionStatus {
        format: STATUS_FORMAT,
        deployment_id: workspace.id.clone(),
        phase: "CONFIRMED".to_owned(),
        version: manifest.expected_version,
        previous: manifest.old_version,
        reason: "native-cli-health-and-identity-gates-passed".to_owned(),
        failure: String::new(),
        updated_utc: Utc::now(),
        observation_seconds: manifest.observation_seconds,
    };
    workspace.write_status(confirmed.clone())?;
    finalize_transaction(&workspace)?;
    Ok(confirmed)
}

pub async fn target_rollback(
    workspace_path: &Path,
    reason: &str,
) -> Result<ProductionStatus, DeploymentError> {
    require_production_identity()?;
    if !valid_reason(reason) {
        return Err(DeploymentError::InvalidSchema(
            "rollback reason is not audit-safe".to_owned(),
        ));
    }
    let workspace = Workspace::open(workspace_path, true)?;
    validate_transaction_lock(&workspace)?;
    let manifest = workspace.read_manifest_for_rollback(true)?;
    let status = workspace.read_status(true)?;
    if status.phase == "CONFIRMED" || status.phase == "ROLLED_BACK" {
        finalize_transaction(&workspace)?;
        return Ok(status);
    }
    const ELIGIBLE: &[&str] = &[
        "MUTATION_PENDING",
        "MIGRATING",
        "DEPLOYING",
        "DEPLOYING_GO",
        "DEPLOYING_WEB",
        "OBSERVING",
        "AWAITING_CONFIRMATION",
        "CONFIRMING",
        "ROLLBACK_REQUIRED",
        "ROLLING_BACK",
    ];
    if !ELIGIBLE.contains(&status.phase.as_str()) {
        return Err(DeploymentError::InvalidPhase(status.phase));
    }
    let rolling = ProductionStatus {
        format: STATUS_FORMAT,
        deployment_id: workspace.id.clone(),
        phase: "ROLLING_BACK".to_owned(),
        version: manifest.expected_version.clone(),
        previous: manifest.old_version.clone(),
        reason: reason.to_owned(),
        failure: String::new(),
        updated_utc: Utc::now(),
        observation_seconds: 0,
    };
    workspace.write_status(rolling.clone())?;

    if let Err(error) = perform_rollback(&manifest).await {
        let mut failed = rolling;
        failed.phase = "ROLLBACK_REQUIRED".to_owned();
        failed.failure = rollback_failure_code(&error).to_owned();
        workspace.write_status(failed)?;
        return Err(error);
    }
    let rolled_back = ProductionStatus {
        format: STATUS_FORMAT,
        deployment_id: workspace.id.clone(),
        phase: "ROLLED_BACK".to_owned(),
        version: manifest.old_version,
        previous: manifest.expected_version,
        reason: reason.to_owned(),
        failure: String::new(),
        updated_utc: Utc::now(),
        observation_seconds: 0,
    };
    workspace.write_status(rolled_back.clone())?;
    finalize_transaction(&workspace)?;
    Ok(rolled_back)
}

async fn perform_rollback(manifest: &ProductionManifest) -> Result<(), DeploymentError> {
    if manifest.go.changed {
        run("/usr/bin/systemctl", &["stop", SERVICE])?;
        prepare_provider_rollback(manifest)?;
        install_package(&manifest.go.rollback_path)?;
        restore_environment(manifest)?;
        restore_provider_link(manifest)?;
        run("/usr/bin/systemctl", &["daemon-reload"])?;
        run("/usr/bin/systemctl", &["enable", "--now", SERVICE])?;
    }
    if manifest.web.changed {
        install_package(&manifest.web.rollback_path)?;
    }
    verify_manifest_evidence_for_rollback(manifest, true)?;
    health_check(manifest, true).await
}

fn rollback_failure_code(error: &DeploymentError) -> &'static str {
    match error {
        DeploymentError::ProviderLink(_) => "provider-link",
        DeploymentError::Health(_) => "health-identity",
        DeploymentError::InvalidEvidence(_) => "invalid-evidence",
        DeploymentError::UnsafePath(_) => "unsafe-path",
        DeploymentError::Command(_) => "package-or-service-command",
        DeploymentError::Io(_) => "filesystem",
        DeploymentError::Json(_) | DeploymentError::InvalidSchema(_) => "invalid-schema",
        DeploymentError::InvalidPhase(_) => "invalid-phase",
        DeploymentError::RootRequired | DeploymentError::HostMismatch(_) => "host-identity",
        DeploymentError::ObservationIncomplete => "observation-incomplete",
        DeploymentError::UnsupportedController(_) => "unsupported-controller",
    }
}

fn legacy_go_rollback(manifest: &ProductionManifest) -> bool {
    manifest.previous_provider_target == "legacy-regular"
        && manifest.go.rollback_package_name == "lmm-api-go-bin"
        && manifest.go.rollback_identity == "lmm-api-go-bin 0.1.69-1"
}

fn prepare_provider_rollback(manifest: &ProductionManifest) -> Result<(), DeploymentError> {
    if !legacy_go_rollback(manifest) {
        return Ok(());
    }
    let status = verify_active_provider(
        &manifest.go.candidate_package_name,
        &manifest.new_provider_target,
    )?;
    if status.target != manifest.new_provider_target {
        return Err(DeploymentError::InvalidEvidence(
            "active provider changed before legacy rollback".to_owned(),
        ));
    }
    fs::remove_file(GENERIC_BINARY)?;
    File::open("/usr/bin")?.sync_all()?;
    Ok(())
}

fn restore_provider_link(manifest: &ProductionManifest) -> Result<(), DeploymentError> {
    if legacy_go_rollback(manifest) {
        let generic = Path::new(GENERIC_BINARY);
        let metadata = fs::symlink_metadata(generic)?;
        if metadata.file_type().is_symlink()
            || !metadata.is_file()
            || metadata.uid() != 0
            || metadata.permissions().mode() & 0o111 == 0
            || metadata.permissions().mode() & 0o022 != 0
        {
            return Err(DeploymentError::InvalidEvidence(
                "legacy generic provider payload is unsafe".to_owned(),
            ));
        }
        if package_owner(generic)? != manifest.go.rollback_package_name {
            return Err(DeploymentError::InvalidEvidence(
                "legacy generic provider package does not match manifest".to_owned(),
            ));
        }
        let reverse = Path::new("/usr/bin").join(GO_PROVIDER);
        if !fs::symlink_metadata(&reverse)?.file_type().is_symlink()
            || fs::read_link(&reverse)? != Path::new("lmm-api")
        {
            return Err(DeploymentError::InvalidEvidence(
                "legacy Go reverse alias is invalid".to_owned(),
            ));
        }
        return Ok(());
    }
    let provider = manifest
        .previous_provider_target
        .parse::<Provider>()
        .map_err(|_| {
            DeploymentError::InvalidSchema(
                "previous provider target cannot be restored safely".to_owned(),
            )
        })?;
    if provider_for_package(&manifest.go.rollback_package_name)? != provider {
        return Err(DeploymentError::InvalidSchema(
            "rollback package and previous provider target disagree".to_owned(),
        ));
    }
    let status = os_manager().select(provider)?;
    if status.target != manifest.previous_provider_target
        || status.package != manifest.go.rollback_package_name
    {
        return Err(DeploymentError::InvalidEvidence(
            "restored provider link does not match rollback manifest".to_owned(),
        ));
    }
    Ok(())
}

fn package_owner(path: &Path) -> Result<String, DeploymentError> {
    let output = Command::new("/usr/bin/pacman")
        .args(["-Qqo", "--"])
        .arg(path)
        .env("LC_ALL", "C")
        .output()?;
    if !output.status.success() {
        return Err(DeploymentError::InvalidEvidence(
            "provider package ownership query failed".to_owned(),
        ));
    }
    let owner = String::from_utf8_lossy(&output.stdout).trim().to_owned();
    if owner.is_empty() || owner.lines().count() != 1 {
        return Err(DeploymentError::InvalidEvidence(
            "provider package ownership is ambiguous".to_owned(),
        ));
    }
    Ok(owner)
}

fn install_package(path: &Path) -> Result<(), DeploymentError> {
    let text = path
        .to_str()
        .ok_or_else(|| DeploymentError::UnsafePath("package path is not UTF-8".to_owned()))?;
    run(
        "/usr/bin/runuser",
        &[
            "--user",
            "lmm-api-deploy",
            "--",
            "/usr/bin/paru",
            "-U",
            "--noconfirm",
            "--",
            text,
        ],
    )
}

fn restore_environment(manifest: &ProductionManifest) -> Result<(), DeploymentError> {
    let source = manifest.config_restore_path.join("lmm-api-go.env");
    let target = Path::new("/etc/lmm-api-go/lmm-api-go.env");
    let bytes = read_private(&source, 1024 * 1024, true)?;
    if sha256_bytes(&bytes) != manifest.environment_restore_sha256 {
        return Err(DeploymentError::InvalidEvidence(
            "configuration rollback snapshot changed".to_owned(),
        ));
    }
    write_atomic_private(target, &bytes)
}

fn run(program: &str, args: &[&str]) -> Result<(), DeploymentError> {
    let mut command = match program {
        "/usr/bin/systemctl" => Command::new("/usr/bin/systemctl"),
        "/usr/bin/runuser" => Command::new("/usr/bin/runuser"),
        _ => {
            return Err(DeploymentError::Command(format!(
                "executable is not allowlisted: {program}"
            )));
        }
    };
    let output = command.args(args).env("LC_ALL", "C").output()?;
    if output.status.success() {
        return Ok(());
    }
    let mut detail = String::from_utf8_lossy(&output.stderr).trim().to_owned();
    detail.truncate(1024);
    Err(DeploymentError::Command(format!(
        "{} failed: {detail}",
        Path::new(program)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
    )))
}

async fn health_check(
    manifest: &ProductionManifest,
    rollback: bool,
) -> Result<(), DeploymentError> {
    run("/usr/bin/systemctl", &["is-active", "--quiet", SERVICE])?;
    let expected = if rollback {
        &manifest.old_version
    } else {
        &manifest.expected_version
    };
    let client = reqwest::Client::builder()
        .timeout(Duration::from_secs(8))
        .redirect(reqwest::redirect::Policy::none())
        .build()
        .map_err(|error| DeploymentError::Health(error.to_string()))?;
    for base in ["http://127.0.0.1:3000", "https://api.lmm.best"] {
        let response = client
            .get(format!("{base}/api/status"))
            .send()
            .await
            .map_err(|error| DeploymentError::Health(error.to_string()))?;
        if !response.status().is_success() {
            return Err(DeploymentError::Health(format!(
                "status probe returned {}",
                response.status()
            )));
        }
        let value: serde_json::Value = response
            .json()
            .await
            .map_err(|error| DeploymentError::Health(error.to_string()))?;
        if value
            .pointer("/data/version")
            .and_then(serde_json::Value::as_str)
            != Some(expected.as_str())
        {
            return Err(DeploymentError::Health(
                "status identity does not match expected version".to_owned(),
            ));
        }
    }
    let live = client
        .get("http://127.0.0.1:3000/api/livez")
        .send()
        .await
        .map_err(|error| DeploymentError::Health(error.to_string()))?;
    if !live.status().is_success() {
        return Err(DeploymentError::Health("live probe failed".to_owned()));
    }
    let target = if rollback {
        &manifest.frontend.old_target
    } else {
        &manifest.frontend.new_target
    };
    let digest = if rollback {
        &manifest.frontend.old_index_sha256
    } else {
        &manifest.frontend.new_index_sha256
    };
    verify_frontend_identity(target, digest)
}

fn verify_frontend_identity(target: &str, digest: &str) -> Result<(), DeploymentError> {
    let link = fs::read_link(Path::new(FRONTEND_ROOT).join("current"))?;
    if link != Path::new(target) {
        return Err(DeploymentError::InvalidEvidence(
            "frontend current link does not match manifest".to_owned(),
        ));
    }
    let index = Path::new(FRONTEND_ROOT).join(target).join("index.html");
    if sha256_file(&index, true)? != digest {
        return Err(DeploymentError::InvalidEvidence(
            "frontend index hash does not match manifest".to_owned(),
        ));
    }
    Ok(())
}

fn validate_manifest(
    workspace: &Workspace,
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    validate_manifest_schema(workspace, manifest)?;
    for transition in [&manifest.go, &manifest.web] {
        validate_transition_evidence(workspace, transition, require_root_owner)?;
    }
    let candidate_provider = provider_for_package(&manifest.go.candidate_package_name)?;
    validate_candidate_entrypoint(
        workspace,
        &manifest.probe_binary,
        &manifest.probe_binary_sha256,
        candidate_provider,
        require_root_owner,
    )?;
    Ok(())
}

fn validate_manifest_schema(
    workspace: &Workspace,
    manifest: &ProductionManifest,
) -> Result<(), DeploymentError> {
    if manifest.format != MANIFEST_FORMAT
        || manifest.deployment_id != workspace.id
        || manifest.operator_user != "lmm-api-deploy"
        || !(MINIMUM_OBSERVATION_SECONDS..=MAXIMUM_OBSERVATION_SECONDS)
            .contains(&manifest.observation_seconds)
        || !valid_version(&manifest.expected_version)
        || !valid_version(&manifest.old_version)
    {
        return Err(DeploymentError::InvalidSchema(
            "deployment manifest identity is invalid".to_owned(),
        ));
    }
    for transition in [&manifest.go, &manifest.web] {
        validate_transition_schema(workspace, transition)?;
    }
    let candidate_provider = provider_for_package(&manifest.go.candidate_package_name)?;
    if manifest.new_provider_target != candidate_provider.filename()
        || !matches!(
            manifest.previous_provider_target.as_str(),
            "lmm-api-go" | "lmm-api-rs" | "legacy-regular" | "missing"
        )
    {
        return Err(DeploymentError::InvalidSchema(
            "provider-link transition is invalid".to_owned(),
        ));
    }
    if manifest.go.candidate_contract_revision != manifest.web.candidate_contract_revision
        || manifest.go.rollback_contract_revision != manifest.web.rollback_contract_revision
    {
        return Err(DeploymentError::InvalidSchema(
            "backend/frontend contract pair mismatch".to_owned(),
        ));
    }
    if manifest.config_restore_path != workspace.state.join("config-restore") {
        return Err(DeploymentError::UnsafePath(
            "configuration restore path escapes deployment state".to_owned(),
        ));
    }
    if manifest.backups_enabled {
        if manifest.backup_dir != Path::new(BACKUP_ROOT).join(&manifest.deployment_id) {
            return Err(DeploymentError::UnsafePath(
                "target backup path is not release-scoped".to_owned(),
            ));
        }
        require_sha256(&manifest.database_backup_sha256)?;
        let bound = [
            &manifest.target_backup_sha256,
            &manifest.controller_backup_sha256,
            &manifest.offhost_backup_sha256,
        ]
        .into_iter()
        .all(|digest| require_sha256(digest).is_ok());
        let legacy = manifest.target_backup_sha256.is_empty()
            && manifest.controller_backup_sha256.is_empty()
            && manifest.offhost_backup_sha256.is_empty();
        if (manifest.backup_evidence_format == 2 && !bound)
            || (manifest.backup_evidence_format == 0 && !legacy)
            || !matches!(manifest.backup_evidence_format, 0 | 2)
        {
            return Err(DeploymentError::InvalidSchema(
                "external backup digest evidence is incomplete".to_owned(),
            ));
        }
    } else if !manifest.backup_dir.as_os_str().is_empty()
        || manifest.backup_evidence_format != 0
        || !manifest.database_backup_sha256.is_empty()
        || !manifest.target_backup_sha256.is_empty()
        || !manifest.controller_backup_sha256.is_empty()
        || !manifest.offhost_backup_sha256.is_empty()
    {
        return Err(DeploymentError::InvalidSchema(
            "manifest contains unauthorized backup evidence".to_owned(),
        ));
    }
    for digest in [
        &manifest.probe_binary_sha256,
        &manifest.operator_binary_sha256,
        &manifest.frontend.old_index_sha256,
        &manifest.frontend.new_index_sha256,
        &manifest.environment_restore_sha256,
    ] {
        require_sha256(digest)?;
    }
    if manifest.probe_binary != manifest.operator_binary
        || manifest.probe_binary_sha256 != manifest.operator_binary_sha256
    {
        return Err(DeploymentError::InvalidSchema(
            "candidate probe and operator evidence differ".to_owned(),
        ));
    }
    Ok(())
}

fn validate_candidate_entrypoint(
    workspace: &Workspace,
    provider_path: &Path,
    digest: &str,
    provider: Provider,
    require_root_owner: bool,
) -> Result<PathBuf, DeploymentError> {
    let expected_provider = workspace.staging.join(provider.filename());
    if provider_path != expected_provider {
        return Err(DeploymentError::UnsafePath(
            "candidate provider evidence is not the release-scoped provider file".to_owned(),
        ));
    }
    validate_staged(workspace, provider_path, digest, require_root_owner)?;
    let provider_metadata = fs::symlink_metadata(provider_path)?;
    if provider_metadata.permissions().mode() & 0o022 != 0
        || provider_metadata.permissions().mode() & 0o100 == 0
        || provider_metadata.nlink() != 1
    {
        return Err(DeploymentError::UnsafePath(
            "candidate provider target mode or link count is unsafe".to_owned(),
        ));
    }
    let entrypoint = workspace.staging.join("lmm-api");
    let entrypoint_metadata = fs::symlink_metadata(&entrypoint)?;
    if !entrypoint_metadata.file_type().is_symlink()
        || (require_root_owner && entrypoint_metadata.uid() != 0)
        || fs::read_link(&entrypoint)? != Path::new(provider.filename())
    {
        return Err(DeploymentError::UnsafePath(
            "candidate entrypoint is not a one-hop relative provider link".to_owned(),
        ));
    }
    Ok(entrypoint)
}

fn validate_transition_schema(
    workspace: &Workspace,
    transition: &PackageTransition,
) -> Result<(), DeploymentError> {
    if transition.candidate_package_name.is_empty()
        || transition.rollback_package_name.is_empty()
        || !valid_identity(
            &transition.candidate_identity,
            &transition.candidate_package_name,
        )
        || !valid_identity(
            &transition.rollback_identity,
            &transition.rollback_package_name,
        )
    {
        return Err(DeploymentError::InvalidSchema(
            "package transition identity is invalid".to_owned(),
        ));
    }
    require_sha256(&transition.candidate_sha256)?;
    require_sha256(&transition.rollback_sha256)?;
    validate_staged_path(workspace, &transition.candidate_path)?;
    validate_staged_path(workspace, &transition.rollback_path)?;
    if !transition.changed
        && (transition.candidate_package_name != transition.rollback_package_name
            || transition.candidate_identity != transition.rollback_identity
            || transition.candidate_sha256 != transition.rollback_sha256)
    {
        return Err(DeploymentError::InvalidSchema(
            "unchanged package evidence differs".to_owned(),
        ));
    }
    Ok(())
}

fn validate_transition_evidence(
    workspace: &Workspace,
    transition: &PackageTransition,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    validate_staged(
        workspace,
        &transition.candidate_path,
        &transition.candidate_sha256,
        require_root_owner,
    )?;
    validate_staged(
        workspace,
        &transition.rollback_path,
        &transition.rollback_sha256,
        require_root_owner,
    )
}

fn validate_staged_path(workspace: &Workspace, path: &Path) -> Result<PathBuf, DeploymentError> {
    let clean = clean_absolute(path)?;
    if clean.parent() != Some(workspace.staging.as_path()) {
        return Err(DeploymentError::UnsafePath(
            "staged evidence escapes workspace".to_owned(),
        ));
    }
    Ok(clean)
}

fn validate_staged(
    workspace: &Workspace,
    path: &Path,
    digest: &str,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let clean = validate_staged_path(workspace, path)?;
    if sha256_file(&clean, require_root_owner)? != digest {
        return Err(DeploymentError::InvalidEvidence(format!(
            "SHA-256 mismatch for {}",
            clean.display()
        )));
    }
    Ok(())
}

fn verify_manifest_evidence(
    workspace: &Workspace,
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    validate_manifest(workspace, manifest, require_root_owner)?;
    let environment = manifest.config_restore_path.join("lmm-api-go.env");
    if sha256_file(&environment, require_root_owner)? != manifest.environment_restore_sha256 {
        return Err(DeploymentError::InvalidEvidence(
            "configuration rollback snapshot no longer matches manifest".to_owned(),
        ));
    }
    verify_target_backup(manifest, require_root_owner)?;
    Ok(())
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ExternalBackupAttestation {
    format: u32,
    deployment_id: String,
    #[serde(default)]
    backup_evidence_format: u32,
    #[serde(default)]
    target_digest: String,
    controller_digest: String,
    offhost_digest: String,
    verified_utc: DateTime<Utc>,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ExternalBackupConfirmation {
    format: u32,
    deployment_id: String,
    target_digest: String,
    controller_digest: String,
    offhost_digest: String,
    verified_utc: DateTime<Utc>,
}

fn verify_target_backup(
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    if !manifest.backups_enabled {
        return Ok(());
    }
    let root = &manifest.backup_dir;
    require_real(root, true, require_root_owner)?;
    let root_metadata = fs::symlink_metadata(root)?;
    if root_metadata.permissions().mode() & 0o777 != 0o700 {
        return Err(DeploymentError::UnsafePath(
            "target backup root must remain private".to_owned(),
        ));
    }
    let checksum_names = [
        "application.archive",
        "frontend.archive",
        "configuration.archive",
        "database.archive",
        "rollback.package",
    ];
    let checksum_path = root.join("SHA256SUMS");
    let checksum_bytes = read_private(&checksum_path, 1024 * 1024, require_root_owner)?;
    let checksum_text = std::str::from_utf8(&checksum_bytes)
        .map_err(|_| DeploymentError::InvalidSchema("backup checksums are not UTF-8".to_owned()))?;
    let mut checksums = BTreeMap::new();
    for line in checksum_text.lines() {
        let mut fields = line.split_whitespace();
        let digest = fields.next().unwrap_or_default();
        let name = fields.next().unwrap_or_default().trim_start_matches('*');
        if fields.next().is_some()
            || !checksum_names.contains(&name)
            || checksums
                .insert(name.to_owned(), digest.to_owned())
                .is_some()
        {
            return Err(DeploymentError::InvalidSchema(
                "backup checksum inventory is invalid".to_owned(),
            ));
        }
        require_sha256(digest)?;
    }
    if checksums.len() != checksum_names.len() {
        return Err(DeploymentError::InvalidEvidence(
            "backup checksum inventory is incomplete".to_owned(),
        ));
    }
    let mut expected_members = vec![
        "SHA256SUMS",
        "application.archive",
        "configuration.archive",
        "database.archive",
        "external-copies.json",
        "frontend.archive",
        "manifest.env",
        "rollback.package",
    ];
    if root.join("external-confirmation.json").exists() {
        expected_members.push("external-confirmation.json");
    }
    expected_members.sort_unstable();
    let mut actual_members = fs::read_dir(root)?
        .map(|entry| {
            entry
                .map(|value| value.file_name().to_string_lossy().into_owned())
                .map_err(DeploymentError::from)
        })
        .collect::<Result<Vec<_>, _>>()?;
    actual_members.sort();
    if actual_members != expected_members {
        return Err(DeploymentError::InvalidEvidence(
            "target backup contains a missing or unexpected member".to_owned(),
        ));
    }
    for name in &expected_members {
        let metadata = fs::symlink_metadata(root.join(name))?;
        if metadata.file_type().is_symlink()
            || !metadata.is_file()
            || metadata.len() == 0
            || metadata.nlink() != 1
            || metadata.permissions().mode() & 0o077 != 0
            || (require_root_owner && metadata.uid() != 0)
        {
            return Err(DeploymentError::InvalidEvidence(format!(
                "target backup member is unsafe: {name}"
            )));
        }
    }
    for (name, expected_digest) in &checksums {
        let path = root.join(name);
        if sha256_file(&path, require_root_owner)? != *expected_digest {
            return Err(DeploymentError::InvalidEvidence(format!(
                "target backup member is unsafe or mismatched: {name}"
            )));
        }
    }
    if checksums.get("database.archive") != Some(&manifest.database_backup_sha256) {
        return Err(DeploymentError::InvalidEvidence(
            "database backup digest differs from deployment manifest".to_owned(),
        ));
    }
    let checksum_digest = sha256_bytes(&checksum_bytes);
    let attestation_bytes = read_private(
        &root.join("external-copies.json"),
        64 * 1024,
        require_root_owner,
    )?;
    let attestation: ExternalBackupAttestation = serde_json::from_slice(&attestation_bytes)?;
    if attestation.format != 1
        || attestation.deployment_id != manifest.deployment_id
        || attestation.verified_utc.timestamp() <= 0
        || require_sha256(&attestation.controller_digest).is_err()
        || require_sha256(&attestation.offhost_digest).is_err()
    {
        return Err(DeploymentError::InvalidEvidence(
            "external backup attestation is invalid".to_owned(),
        ));
    }
    if manifest.backup_evidence_format == 2 {
        if attestation.backup_evidence_format != 2
            || attestation.target_digest != manifest.target_backup_sha256
            || attestation.controller_digest != manifest.controller_backup_sha256
            || attestation.offhost_digest != manifest.offhost_backup_sha256
            || checksum_digest != manifest.target_backup_sha256
        {
            return Err(DeploymentError::InvalidEvidence(
                "backup attestation differs from immutable manifest evidence".to_owned(),
            ));
        }
    } else if attestation.backup_evidence_format != 0 || !attestation.target_digest.is_empty() {
        return Err(DeploymentError::InvalidEvidence(
            "legacy manifest cannot accept current backup evidence".to_owned(),
        ));
    }
    Ok(())
}

fn verify_backup_confirmation(
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let bytes = read_private(
        &manifest.backup_dir.join("external-confirmation.json"),
        64 * 1024,
        require_root_owner,
    )?;
    let confirmation: ExternalBackupConfirmation = serde_json::from_slice(&bytes)?;
    let now = Utc::now();
    if confirmation.format != 1
        || confirmation.deployment_id != manifest.deployment_id
        || confirmation.target_digest != manifest.target_backup_sha256
        || confirmation.controller_digest != manifest.controller_backup_sha256
        || confirmation.offhost_digest != manifest.offhost_backup_sha256
        || confirmation.verified_utc > now + chrono::Duration::seconds(30)
        || now - confirmation.verified_utc > chrono::Duration::minutes(5)
    {
        return Err(DeploymentError::InvalidEvidence(
            "external backup confirmation receipt is stale or mismatched".to_owned(),
        ));
    }
    Ok(())
}

fn verify_manifest_evidence_for_rollback(
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    if manifest.go.changed
        && (sha256_file(
            &manifest.config_restore_path.join("lmm-api-go.env"),
            require_root_owner,
        )? != manifest.environment_restore_sha256
            || sha256_file(&manifest.go.rollback_path, require_root_owner)?
                != manifest.go.rollback_sha256)
    {
        return Err(DeploymentError::InvalidEvidence(
            "Go rollback package or configuration evidence changed during operation".to_owned(),
        ));
    }
    if manifest.web.changed
        && sha256_file(&manifest.web.rollback_path, require_root_owner)?
            != manifest.web.rollback_sha256
    {
        return Err(DeploymentError::InvalidEvidence(
            "Web rollback package evidence changed during operation".to_owned(),
        ));
    }
    Ok(())
}

fn finalize_transaction(workspace: &Workspace) -> Result<(), DeploymentError> {
    match fs::symlink_metadata(&workspace.probe_token) {
        Ok(metadata) => {
            if metadata.file_type().is_symlink() || !metadata.is_file() || metadata.uid() != 0 {
                return Err(DeploymentError::UnsafePath(
                    "production probe token is unsafe".to_owned(),
                ));
            }
            fs::remove_file(&workspace.probe_token)?;
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    validate_transaction_lock(workspace)?;
    let lock = Path::new(TRANSACTION_LOCK);
    fs::remove_file(lock.join("deployment.env"))?;
    fs::remove_dir(lock)?;
    if let Some(parent) = lock.parent() {
        File::open(parent)?.sync_all()?;
    }
    Ok(())
}

fn validate_transaction_lock(workspace: &Workspace) -> Result<(), DeploymentError> {
    let root = Path::new(TRANSACTION_LOCK);
    require_real(root, true, true)?;
    if fs::metadata(root)?.permissions().mode() & 0o777 != 0o700 {
        return Err(DeploymentError::UnsafePath(
            "transaction lock must remain root-only".to_owned(),
        ));
    }
    let values = read_key_value(&root.join("deployment.env"), 16 * 1024, true)?;
    if values.get("format").map(String::as_str) != Some("1")
        || values.get("deployment_id") != Some(&workspace.id)
        || values.get("status").map(String::as_str) != Some("ACTIVE")
    {
        return Err(DeploymentError::InvalidEvidence(
            "transaction lock belongs to another or inactive deployment".to_owned(),
        ));
    }
    Ok(())
}

fn require_production_identity() -> Result<(), DeploymentError> {
    if rustix::process::geteuid().as_raw() != 0 {
        return Err(DeploymentError::RootRequired);
    }
    let hostname = fs::read_to_string("/proc/sys/kernel/hostname")?
        .trim()
        .to_owned();
    if hostname != EXPECTED_HOST {
        return Err(DeploymentError::HostMismatch(hostname));
    }
    Ok(())
}

fn verify_active_provider(
    package: &str,
    expected_target: &str,
) -> Result<ProviderLinkStatus, DeploymentError> {
    let expected = provider_for_package(package)?;
    let status = os_manager().status()?;
    if status.target != expected_target
        || status.package != package
        || !expected.accepts_package(&status.package)
    {
        return Err(DeploymentError::InvalidEvidence(
            "active provider package does not match deployment manifest".to_owned(),
        ));
    }
    Ok(status)
}

fn provider_for_package(name: &str) -> Result<Provider, DeploymentError> {
    if Provider::Go.accepts_package(name) {
        Ok(Provider::Go)
    } else if Provider::Rust.accepts_package(name) {
        Ok(Provider::Rust)
    } else {
        Err(DeploymentError::InvalidSchema(format!(
            "backend package {name:?} has no safe provider mapping"
        )))
    }
}

fn read_private(
    path: &Path,
    maximum: u64,
    require_root_owner: bool,
) -> Result<Vec<u8>, DeploymentError> {
    require_real(path, false, require_root_owner)?;
    let metadata = fs::metadata(path)?;
    if metadata.len() > maximum || metadata.permissions().mode() & 0o022 != 0 {
        return Err(DeploymentError::UnsafePath(path.display().to_string()));
    }
    Ok(fs::read(path)?)
}

fn read_key_value(
    path: &Path,
    maximum: u64,
    require_root_owner: bool,
) -> Result<BTreeMap<String, String>, DeploymentError> {
    let content = read_private(path, maximum, require_root_owner)?;
    let text = std::str::from_utf8(&content)
        .map_err(|_| DeploymentError::InvalidSchema("marker is not UTF-8".to_owned()))?;
    let mut values = BTreeMap::new();
    for line in text.lines() {
        let (key, value) = line
            .split_once('=')
            .ok_or_else(|| DeploymentError::InvalidSchema("invalid marker line".to_owned()))?;
        if key.is_empty()
            || value.is_empty()
            || !key
                .bytes()
                .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
            || values.insert(key.to_owned(), value.to_owned()).is_some()
        {
            return Err(DeploymentError::InvalidSchema(
                "invalid marker value".to_owned(),
            ));
        }
    }
    Ok(values)
}

fn require_real(
    path: &Path,
    directory: bool,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_type().is_symlink()
        || directory != metadata.is_dir()
        || (!directory && !metadata.is_file())
        || metadata.permissions().mode() & 0o022 != 0
        || (require_root_owner && metadata.uid() != 0)
    {
        return Err(DeploymentError::UnsafePath(path.display().to_string()));
    }
    Ok(())
}

fn clean_absolute(path: &Path) -> Result<PathBuf, DeploymentError> {
    if !path.is_absolute()
        || path == Path::new("/")
        || path
            .components()
            .any(|part| matches!(part, Component::ParentDir))
    {
        return Err(DeploymentError::UnsafePath(path.display().to_string()));
    }
    let clean = path.components().collect::<PathBuf>();
    if clean != path {
        return Err(DeploymentError::UnsafePath(path.display().to_string()));
    }
    Ok(clean)
}

fn sha256_file(path: &Path, require_root_owner: bool) -> Result<String, DeploymentError> {
    require_real(path, false, require_root_owner)?;
    let mut file = File::open(path)?;
    let mut digest = Sha256::new();
    io::copy(&mut file, &mut digest)?;
    Ok(format!("{:x}", digest.finalize()))
}

fn sha256_bytes(bytes: &[u8]) -> String {
    format!("{:x}", Sha256::digest(bytes))
}

fn write_atomic_private(path: &Path, bytes: &[u8]) -> Result<(), DeploymentError> {
    let parent = path
        .parent()
        .ok_or_else(|| DeploymentError::UnsafePath(path.display().to_string()))?;
    require_real(parent, true, true)?;
    let temporary = parent.join(format!(
        ".{}.{}.new",
        path.file_name().unwrap_or_default().to_string_lossy(),
        std::process::id()
    ));
    let _ = fs::remove_file(&temporary);
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&temporary)?;
    file.write_all(bytes)?;
    file.sync_all()?;
    fs::rename(&temporary, path)?;
    File::open(parent)?.sync_all()?;
    Ok(())
}

fn require_sha256(value: &str) -> Result<(), DeploymentError> {
    if value.len() != 64
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(DeploymentError::InvalidSchema(
            "SHA-256 must be 64 lowercase hexadecimal characters".to_owned(),
        ));
    }
    Ok(())
}

fn valid_identifier(value: &str, maximum: usize) -> bool {
    !value.is_empty()
        && value.len() <= maximum
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._-".contains(&byte))
        && value.as_bytes()[0].is_ascii_alphanumeric()
}

fn valid_version(value: &str) -> bool {
    !value.is_empty()
        && value.as_bytes()[0].is_ascii_digit()
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._+".contains(&byte))
}

fn valid_identity(identity: &str, package: &str) -> bool {
    identity
        .split_once(' ')
        .is_some_and(|(name, full_version)| {
            name == package
                && full_version
                    .rsplit_once('-')
                    .is_some_and(|(version, release)| {
                        valid_version(version)
                            && !release.is_empty()
                            && release.as_bytes()[0].is_ascii_digit()
                            && release.split_once('.').map_or_else(
                                || release.bytes().all(|byte| byte.is_ascii_digit()),
                                |(major, minor)| {
                                    !major.is_empty()
                                        && !minor.is_empty()
                                        && major.bytes().all(|byte| byte.is_ascii_digit())
                                        && minor.bytes().all(|byte| byte.is_ascii_digit())
                                },
                            )
                            && !release.starts_with('0')
                    })
        })
}

fn valid_reason(reason: &str) -> bool {
    !reason.is_empty()
        && reason.len() <= 128
        && reason.as_bytes()[0].is_ascii_alphanumeric()
        && reason
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._:-".contains(&byte))
}

#[cfg(test)]
mod tests {
    use std::{
        os::unix::fs::{PermissionsExt, symlink},
        sync::atomic::{AtomicUsize, Ordering},
    };

    use super::*;

    static TEST_WORKSPACE_ID: AtomicUsize = AtomicUsize::new(0);

    struct TestWorkspace {
        base: PathBuf,
        work_root: PathBuf,
        workspace: PathBuf,
    }

    impl TestWorkspace {
        fn new() -> Self {
            let sequence = TEST_WORKSPACE_ID.fetch_add(1, Ordering::Relaxed);
            let base = Path::new(env!("CARGO_MANIFEST_DIR"))
                .join("target/deployment-unit-tests")
                .join(format!("{}-{sequence}", std::process::id()));
            let work_root = base.join("work");
            let workspace = work_root.join("cleaned-terminal");
            let state = workspace.join("state");
            fs::create_dir_all(&state).expect("create deployment state fixture");
            fs::set_permissions(&state, fs::Permissions::from_mode(0o700))
                .expect("protect deployment state fixture");
            fs::write(
                workspace.join(".lmm-deploy-workspace"),
                "format=1\ndeployment_id=cleaned-terminal\nrole=target\n",
            )
            .expect("write workspace marker");
            let status = ProductionStatus {
                format: STATUS_FORMAT,
                deployment_id: "cleaned-terminal".to_owned(),
                phase: "CONFIRMED".to_owned(),
                version: "0.2.13".to_owned(),
                previous: "0.2.12".to_owned(),
                reason: "test".to_owned(),
                failure: String::new(),
                updated_utc: Utc::now(),
                observation_seconds: MINIMUM_OBSERVATION_SECONDS,
            };
            fs::write(
                state.join("status.json"),
                serde_json::to_vec(&status).expect("encode status fixture"),
            )
            .expect("write status fixture");
            Self {
                base,
                work_root,
                workspace,
            }
        }

        fn candidate_entrypoint(&self) -> (PathBuf, String) {
            let staging = self.workspace.join("staging");
            fs::create_dir(&staging).expect("create staging fixture");
            let provider = staging.join(GO_PROVIDER);
            fs::write(&provider, b"candidate-provider").expect("write provider fixture");
            fs::set_permissions(&provider, fs::Permissions::from_mode(0o700))
                .expect("protect provider fixture");
            symlink(GO_PROVIDER, staging.join("lmm-api"))
                .expect("create candidate entrypoint fixture");
            let digest = sha256_file(&provider, false).expect("hash provider fixture");
            (provider, digest)
        }

        fn go_rollback_manifest(&self) -> ProductionManifest {
            let (provider, provider_digest) = self.candidate_entrypoint();
            let staging = self.workspace.join("staging");
            let write_package = |name: &str, body: &[u8]| {
                let path = staging.join(name);
                fs::write(&path, body).expect("write package fixture");
                fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
                    .expect("protect package fixture");
                let digest = sha256_file(&path, false).expect("hash package fixture");
                (path, digest)
            };
            let (go_candidate, go_candidate_digest) =
                write_package("lmm-api-go-bin-new.pkg.tar.zst", b"go-candidate");
            let (go_rollback, go_rollback_digest) =
                write_package("lmm-api-go-bin-old.pkg.tar.zst", b"go-rollback");
            let (web_candidate, web_candidate_digest) =
                write_package("lmm-api-web-bin-new.pkg.tar.zst", b"web-candidate");
            let (web_rollback, _web_rollback_digest) =
                write_package("lmm-api-web-bin-old.pkg.tar.zst", b"web-candidate");
            let config_restore = self.workspace.join("state/config-restore");
            fs::create_dir(&config_restore).expect("create config restore fixture");
            fs::set_permissions(&config_restore, fs::Permissions::from_mode(0o700))
                .expect("protect config restore fixture");
            let environment = config_restore.join("lmm-api-go.env");
            fs::write(&environment, b"SQL_DSN=postgres://fixture\n")
                .expect("write environment restore fixture");
            fs::set_permissions(&environment, fs::Permissions::from_mode(0o600))
                .expect("protect environment restore fixture");
            let environment_digest =
                sha256_file(&environment, false).expect("hash environment restore fixture");
            let contract = "a".repeat(64);
            ProductionManifest {
                format: MANIFEST_FORMAT,
                deployment_id: "cleaned-terminal".to_owned(),
                operator_user: "lmm-api-deploy".to_owned(),
                go: PackageTransition {
                    candidate_package_name: "lmm-api-go-bin".to_owned(),
                    rollback_package_name: "lmm-api-go-bin".to_owned(),
                    changed: true,
                    candidate_path: go_candidate,
                    rollback_path: go_rollback,
                    candidate_identity: "lmm-api-go-bin 0.2.13-1".to_owned(),
                    rollback_identity: "lmm-api-go-bin 0.2.12-1".to_owned(),
                    candidate_sha256: go_candidate_digest,
                    rollback_sha256: go_rollback_digest,
                    candidate_git_revision: "2".repeat(40),
                    rollback_git_revision: "1".repeat(40),
                    candidate_contract_revision: contract.clone(),
                    rollback_contract_revision: contract.clone(),
                },
                web: PackageTransition {
                    candidate_package_name: "lmm-api-web-bin".to_owned(),
                    rollback_package_name: "lmm-api-web-bin".to_owned(),
                    changed: false,
                    candidate_path: web_candidate,
                    rollback_path: web_rollback,
                    candidate_identity: "lmm-api-web-bin 0.1.57-1".to_owned(),
                    rollback_identity: "lmm-api-web-bin 0.1.57-1".to_owned(),
                    candidate_sha256: web_candidate_digest.clone(),
                    rollback_sha256: web_candidate_digest,
                    candidate_git_revision: "3".repeat(40),
                    rollback_git_revision: "3".repeat(40),
                    candidate_contract_revision: contract.clone(),
                    rollback_contract_revision: contract,
                },
                frontend: FrontendTransition {
                    old_target: "releases/0.1.57-1.g333333333333".to_owned(),
                    new_target: "releases/0.1.57-1.g333333333333".to_owned(),
                    old_index_sha256: "4".repeat(64),
                    new_index_sha256: "4".repeat(64),
                },
                probe_binary: provider.clone(),
                probe_binary_sha256: provider_digest.clone(),
                operator_binary: provider,
                operator_binary_sha256: provider_digest,
                expected_version: "0.2.13".to_owned(),
                old_version: "0.2.12".to_owned(),
                previous_provider_target: GO_PROVIDER.to_owned(),
                new_provider_target: GO_PROVIDER.to_owned(),
                backup_dir: PathBuf::new(),
                backups_enabled: false,
                backup_evidence_format: 0,
                database_backup_sha256: String::new(),
                target_backup_sha256: String::new(),
                controller_backup_sha256: String::new(),
                offhost_backup_sha256: String::new(),
                database_schema: "public".to_owned(),
                observation_started_utc: Some(Utc::now()),
                observation_seconds: MINIMUM_OBSERVATION_SECONDS,
                service_restart_baseline: 0,
                config_restore_path: config_restore,
                environment_restore_sha256: environment_digest,
                nginx_edge_restore_sha256: String::new(),
                preserve_edge_policy: false,
            }
        }

        fn bound_backup_manifest(&self) -> ProductionManifest {
            let backup = self.base.join("backup");
            fs::create_dir(&backup).expect("create backup fixture");
            fs::set_permissions(&backup, fs::Permissions::from_mode(0o700))
                .expect("protect backup fixture");
            let checksummed = [
                "application.archive",
                "frontend.archive",
                "configuration.archive",
                "database.archive",
                "rollback.package",
            ];
            let mut checksums = String::new();
            let mut database_digest = String::new();
            for name in checksummed {
                let path = backup.join(name);
                fs::write(&path, format!("fixture-{name}")).expect("write backup member fixture");
                fs::set_permissions(&path, fs::Permissions::from_mode(0o600))
                    .expect("protect backup member fixture");
                let digest = sha256_file(&path, false).expect("hash backup member fixture");
                if name == "database.archive" {
                    database_digest.clone_from(&digest);
                }
                checksums.push_str(&format!("{digest}  {name}\n"));
            }
            let manifest_path = backup.join("manifest.env");
            fs::write(&manifest_path, "format=1\n").expect("write backup manifest fixture");
            fs::set_permissions(&manifest_path, fs::Permissions::from_mode(0o600))
                .expect("protect backup manifest fixture");
            let checksum_path = backup.join("SHA256SUMS");
            fs::write(&checksum_path, &checksums).expect("write backup checksums fixture");
            fs::set_permissions(&checksum_path, fs::Permissions::from_mode(0o600))
                .expect("protect backup checksums fixture");
            let target_digest =
                sha256_file(&checksum_path, false).expect("hash backup checksum fixture");
            let controller_digest = "c".repeat(64);
            let offhost_digest = "d".repeat(64);
            let attestation = serde_json::json!({
                "format": 1,
                "deployment_id": "cleaned-terminal",
                "backup_evidence_format": 2,
                "target_digest": target_digest,
                "controller_digest": controller_digest,
                "offhost_digest": offhost_digest,
                "verified_utc": Utc::now(),
            });
            let attestation_path = backup.join("external-copies.json");
            fs::write(
                &attestation_path,
                serde_json::to_vec(&attestation).expect("encode backup attestation fixture"),
            )
            .expect("write backup attestation fixture");
            fs::set_permissions(&attestation_path, fs::Permissions::from_mode(0o600))
                .expect("protect backup attestation fixture");
            let transition = PackageTransition {
                candidate_package_name: String::new(),
                rollback_package_name: String::new(),
                changed: false,
                candidate_path: PathBuf::new(),
                rollback_path: PathBuf::new(),
                candidate_identity: String::new(),
                rollback_identity: String::new(),
                candidate_sha256: String::new(),
                rollback_sha256: String::new(),
                candidate_git_revision: String::new(),
                rollback_git_revision: String::new(),
                candidate_contract_revision: String::new(),
                rollback_contract_revision: String::new(),
            };
            ProductionManifest {
                format: MANIFEST_FORMAT,
                deployment_id: "cleaned-terminal".to_owned(),
                operator_user: String::new(),
                go: transition.clone(),
                web: transition,
                frontend: FrontendTransition {
                    old_target: String::new(),
                    new_target: String::new(),
                    old_index_sha256: String::new(),
                    new_index_sha256: String::new(),
                },
                probe_binary: PathBuf::new(),
                probe_binary_sha256: String::new(),
                operator_binary: PathBuf::new(),
                operator_binary_sha256: String::new(),
                expected_version: String::new(),
                old_version: String::new(),
                previous_provider_target: String::new(),
                new_provider_target: String::new(),
                backup_dir: backup,
                backups_enabled: true,
                backup_evidence_format: 2,
                database_backup_sha256: database_digest,
                target_backup_sha256: target_digest,
                controller_backup_sha256: controller_digest,
                offhost_backup_sha256: offhost_digest,
                database_schema: String::new(),
                observation_started_utc: None,
                observation_seconds: MINIMUM_OBSERVATION_SECONDS,
                service_restart_baseline: 0,
                config_restore_path: PathBuf::new(),
                environment_restore_sha256: String::new(),
                nginx_edge_restore_sha256: String::new(),
                preserve_edge_policy: false,
            }
        }
    }

    impl Drop for TestWorkspace {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.base);
        }
    }

    #[test]
    fn formats_match_manual_only_go_contract() {
        assert_eq!((MANIFEST_FORMAT, STATUS_FORMAT), (8, 2));
        assert_eq!((RELEASE_PLAN_FORMAT, RELEASE_STATE_FORMAT), (5, 3));
    }

    #[test]
    fn rollback_phase_set_includes_every_manual_recovery_phase() {
        for phase in [
            "MUTATION_PENDING",
            "MIGRATING",
            "DEPLOYING",
            "OBSERVING",
            "AWAITING_CONFIRMATION",
            "CONFIRMING",
            "ROLLBACK_REQUIRED",
            "ROLLING_BACK",
        ] {
            assert!(!phase.is_empty());
        }
    }

    #[test]
    fn unsafe_rollback_reason_is_rejected() {
        assert!(!valid_reason("operator request"));
        assert!(valid_reason("operator-request:incident_42"));
    }

    #[test]
    fn go_arch_package_identities_include_pkgrel() {
        assert!(valid_identity("lmm-api-go-bin 0.2.13-1", "lmm-api-go-bin"));
        assert!(valid_identity(
            "lmm-api-web-bin 0.1.57-2.1",
            "lmm-api-web-bin"
        ));
        assert!(!valid_identity("lmm-api-go-bin 0.2.13", "lmm-api-go-bin"));
        assert!(!valid_identity("lmm-api-go-bin 0.2.13-0", "lmm-api-go-bin"));
    }

    #[test]
    fn inspection_accepts_terminal_workspace_without_staging() {
        let fixture = TestWorkspace::new();

        let workspace = Workspace::open_under(&fixture.workspace, &fixture.work_root, false, false)
            .expect("inspect cleaned terminal workspace");

        assert_eq!(
            workspace
                .read_status(false)
                .expect("read retained terminal status")
                .phase,
            "CONFIRMED"
        );
    }

    #[test]
    fn mutation_rejects_terminal_workspace_without_staging() {
        let fixture = TestWorkspace::new();

        let error = Workspace::open_under(&fixture.workspace, &fixture.work_root, false, true)
            .expect_err("mutation must require staging");

        assert!(
            matches!(error, DeploymentError::Io(ref source) if source.kind() == io::ErrorKind::NotFound)
        );
    }

    #[test]
    fn inspection_rejects_symlinked_staging() {
        let fixture = TestWorkspace::new();
        symlink("/etc", fixture.workspace.join("staging")).expect("create unsafe staging symlink");

        let error = Workspace::open_under(&fixture.workspace, &fixture.work_root, false, false)
            .expect_err("inspection must reject symlinked staging");

        assert!(matches!(error, DeploymentError::UnsafePath(_)));
    }

    #[test]
    fn candidate_entrypoint_accepts_one_hop_relative_provider_link() {
        let fixture = TestWorkspace::new();
        let (provider, digest) = fixture.candidate_entrypoint();
        let workspace = Workspace::open_under(&fixture.workspace, &fixture.work_root, false, true)
            .expect("open staged workspace");

        let entrypoint =
            validate_candidate_entrypoint(&workspace, &provider, &digest, Provider::Go, false)
                .expect("validate candidate entrypoint");

        assert_eq!(entrypoint, fixture.workspace.join("staging/lmm-api"));
    }

    #[test]
    fn candidate_entrypoint_rejects_absolute_or_chained_target() {
        for target in ["/usr/bin/lmm-api-go", "nested-provider"] {
            let fixture = TestWorkspace::new();
            let (provider, digest) = fixture.candidate_entrypoint();
            let entrypoint = fixture.workspace.join("staging/lmm-api");
            fs::remove_file(&entrypoint).expect("remove valid entrypoint fixture");
            symlink(target, &entrypoint).expect("create invalid entrypoint fixture");
            if target == "nested-provider" {
                symlink(
                    GO_PROVIDER,
                    fixture.workspace.join("staging/nested-provider"),
                )
                .expect("create chained provider fixture");
            }
            let workspace =
                Workspace::open_under(&fixture.workspace, &fixture.work_root, false, true)
                    .expect("open staged workspace");

            assert!(
                validate_candidate_entrypoint(&workspace, &provider, &digest, Provider::Go, false,)
                    .is_err(),
                "unsafe target was accepted: {target}"
            );
        }
    }

    #[test]
    fn rollback_reader_accepts_go_manifest_and_ignores_candidate_and_backup_evidence() {
        let fixture = TestWorkspace::new();
        let mut manifest = fixture.go_rollback_manifest();
        manifest.backups_enabled = true;
        manifest.backup_dir = Path::new(BACKUP_ROOT).join(&manifest.deployment_id);
        manifest.backup_evidence_format = 2;
        manifest.database_backup_sha256 = "b".repeat(64);
        manifest.target_backup_sha256 = "c".repeat(64);
        manifest.controller_backup_sha256 = "d".repeat(64);
        manifest.offhost_backup_sha256 = "e".repeat(64);
        fs::write(
            fixture.workspace.join("state/deployment.json"),
            serde_json::to_vec(&manifest).expect("encode Go-style deployment manifest"),
        )
        .expect("write Go-style deployment manifest");
        fs::set_permissions(
            fixture.workspace.join("state/deployment.json"),
            fs::Permissions::from_mode(0o600),
        )
        .expect("protect Go-style deployment manifest");
        fs::remove_file(fixture.workspace.join("staging/lmm-api"))
            .expect("remove candidate entrypoint fixture");
        fs::write(&manifest.probe_binary, b"damaged-candidate-provider")
            .expect("damage candidate provider fixture");
        fs::set_permissions(&manifest.probe_binary, fs::Permissions::from_mode(0o700))
            .expect("protect damaged candidate provider fixture");
        let workspace = Workspace::open_under(&fixture.workspace, &fixture.work_root, false, true)
            .expect("open rollback workspace");

        let loaded = workspace
            .read_manifest_for_rollback(false)
            .expect("read Go transaction using only rollback evidence");

        assert_eq!(loaded.go.rollback_identity, "lmm-api-go-bin 0.2.12-1");
        assert!(workspace.read_manifest(false).is_err());
    }

    #[test]
    fn rollback_reader_rejects_damaged_package_or_configuration() {
        for damage_configuration in [false, true] {
            let fixture = TestWorkspace::new();
            let manifest = fixture.go_rollback_manifest();
            fs::write(
                fixture.workspace.join("state/deployment.json"),
                serde_json::to_vec(&manifest).expect("encode deployment manifest"),
            )
            .expect("write deployment manifest");
            fs::set_permissions(
                fixture.workspace.join("state/deployment.json"),
                fs::Permissions::from_mode(0o600),
            )
            .expect("protect deployment manifest");
            let damaged = if damage_configuration {
                manifest.config_restore_path.join("lmm-api-go.env")
            } else {
                manifest.go.rollback_path.clone()
            };
            fs::write(&damaged, b"damaged-rollback-evidence")
                .expect("damage necessary rollback evidence");
            fs::set_permissions(&damaged, fs::Permissions::from_mode(0o600))
                .expect("protect damaged rollback evidence");
            let workspace =
                Workspace::open_under(&fixture.workspace, &fixture.work_root, false, true)
                    .expect("open rollback workspace");

            assert!(workspace.read_manifest_for_rollback(false).is_err());
        }
    }

    #[test]
    fn target_backup_rejects_self_consistent_member_rewrite() {
        let fixture = TestWorkspace::new();
        let manifest = fixture.bound_backup_manifest();
        verify_target_backup(&manifest, false).expect("verify bound target backup");
        let application = manifest.backup_dir.join("application.archive");
        fs::write(&application, "tampered-application")
            .expect("rewrite target backup member fixture");
        fs::set_permissions(&application, fs::Permissions::from_mode(0o600))
            .expect("protect rewritten target backup fixture");
        let replacement = sha256_file(&application, false).expect("hash rewritten member");
        let checksum_path = manifest.backup_dir.join("SHA256SUMS");
        let current = fs::read_to_string(&checksum_path).expect("read checksum fixture");
        let rewritten = current
            .lines()
            .map(|line| {
                if line.ends_with("  application.archive") {
                    format!("{replacement}  application.archive")
                } else {
                    line.to_owned()
                }
            })
            .collect::<Vec<_>>()
            .join("\n")
            + "\n";
        fs::write(&checksum_path, rewritten).expect("rewrite checksum fixture");
        fs::set_permissions(&checksum_path, fs::Permissions::from_mode(0o600))
            .expect("protect rewritten checksum fixture");

        assert!(verify_target_backup(&manifest, false).is_err());
    }

    #[test]
    fn backup_confirmation_requires_fresh_bound_receipt() {
        let fixture = TestWorkspace::new();
        let manifest = fixture.bound_backup_manifest();
        let receipt_path = manifest.backup_dir.join("external-confirmation.json");
        let write_receipt = |verified_utc: DateTime<Utc>| {
            let receipt = serde_json::json!({
                "format": 1,
                "deployment_id": manifest.deployment_id.clone(),
                "target_digest": manifest.target_backup_sha256.clone(),
                "controller_digest": manifest.controller_backup_sha256.clone(),
                "offhost_digest": manifest.offhost_backup_sha256.clone(),
                "verified_utc": verified_utc,
            });
            fs::write(
                &receipt_path,
                serde_json::to_vec(&receipt).expect("encode backup receipt fixture"),
            )
            .expect("write backup receipt fixture");
            fs::set_permissions(&receipt_path, fs::Permissions::from_mode(0o600))
                .expect("protect backup receipt fixture");
        };
        write_receipt(Utc::now());
        verify_backup_confirmation(&manifest, false).expect("verify fresh backup receipt");
        write_receipt(Utc::now() - chrono::Duration::minutes(6));

        assert!(verify_backup_confirmation(&manifest, false).is_err());
    }
}
