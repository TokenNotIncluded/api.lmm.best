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

use crate::provider_link::{Provider, ProviderLinkError, ProviderLinkStatus, os_manager};

pub const MANIFEST_FORMAT: u32 = 7;
pub const STATUS_FORMAT: u32 = 2;
pub const RELEASE_PLAN_FORMAT: u32 = 4;
pub const RELEASE_STATE_FORMAT: u32 = 3;
pub const MINIMUM_OBSERVATION_SECONDS: i64 = 120;
const MAXIMUM_OBSERVATION_SECONDS: i64 = 360;
const WORK_ROOT: &str = "/var/lib/lmm-api-go-deploy/work";
const TRANSACTION_LOCK: &str = "/var/lib/lmm-api-go-deploy/transaction.lock";
const EXPECTED_HOST: &str = "arch-dmit";
const SERVICE: &str = "lmm-api.service";
const FRONTEND_ROOT: &str = "/srv/lmm-api-frontend";

#[derive(Clone, Debug, Deserialize, Serialize)]
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
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub candidate_cli_phase: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub rollback_cli_phase: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct FrontendTransition {
    pub old_target: String,
    pub new_target: String,
    pub old_index_sha256: String,
    pub new_index_sha256: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
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
    pub backup_dir: PathBuf,
    pub backups_enabled: bool,
    #[serde(default)]
    pub database_backup_sha256: String,
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
pub struct ReleaseFilePlan {
    pub path: PathBuf,
    pub sha256: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
pub struct ReleasePackagePlan {
    pub package_path: PathBuf,
    pub package_sha256: String,
    pub name: String,
    pub version: String,
    pub identity: String,
    pub git_revision: String,
    pub contract_revision: String,
    #[serde(default)]
    pub cli_transition_phase: String,
    pub payload_sha256: String,
    pub release_asset: PathBuf,
    pub release_asset_sha256: String,
    pub signature_bundle: PathBuf,
    pub signature_bundle_sha256: String,
    pub release_tag: String,
    pub workflow: String,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
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
}

impl Workspace {
    pub fn open(path: &Path, require_root_owner: bool) -> Result<Self, DeploymentError> {
        let cleaned = clean_absolute(path)?;
        if cleaned.parent() != Some(Path::new(WORK_ROOT)) {
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
        require_real(&staging, true, require_root_owner)?;
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
        &plan.go_rollback.package_sha256,
        &plan.web_candidate.package_sha256,
        &plan.web_rollback.package_sha256,
        &plan.probe_binary.sha256,
    ] {
        require_sha256(digest)?;
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
    let workspace = Workspace::open(workspace_path, false)?;
    let _manifest = workspace.read_manifest(false)?;
    workspace.read_status(false)
}

pub async fn target_confirm(workspace_path: &Path) -> Result<ProductionStatus, DeploymentError> {
    require_production_identity()?;
    let workspace = Workspace::open(workspace_path, true)?;
    validate_transaction_lock(&workspace)?;
    let manifest = workspace.read_manifest(true)?;
    verify_manifest_evidence(&workspace, &manifest, true)?;
    let status = workspace.read_status(true)?;
    if status.phase == "CONFIRMED" {
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
    verify_active_provider(&manifest.go.candidate_package_name)?;
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
    verify_active_provider(&manifest.go.candidate_package_name)?;
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
    let manifest = workspace.read_manifest(true)?;
    verify_manifest_evidence(&workspace, &manifest, true)?;
    let status = workspace.read_status(true)?;
    if status.phase == "CONFIRMED" || status.phase == "ROLLED_BACK" {
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
        failed.failure = error.to_string();
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
    Ok(rolled_back)
}

async fn perform_rollback(manifest: &ProductionManifest) -> Result<(), DeploymentError> {
    if manifest.go.changed {
        run("/usr/bin/systemctl", &["stop", SERVICE])?;
        install_package(&manifest.go.rollback_path)?;
        restore_environment(manifest)?;
        let provider = provider_for_package(&manifest.go.rollback_package_name)?;
        os_manager().select(provider)?;
        run("/usr/bin/systemctl", &["daemon-reload"])?;
        run("/usr/bin/systemctl", &["enable", "--now", SERVICE])?;
    }
    if manifest.web.changed {
        install_package(&manifest.web.rollback_path)?;
    }
    verify_manifest_evidence_for_rollback(manifest, true)?;
    health_check(manifest, true).await
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
    const ALLOWLIST: &[&str] = &["/usr/bin/systemctl", "/usr/bin/runuser"];
    if !ALLOWLIST.contains(&program) {
        return Err(DeploymentError::Command(format!(
            "executable is not allowlisted: {program}"
        )));
    }
    let output = Command::new(program)
        .args(args)
        .env("LC_ALL", "C")
        .output()?;
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
        let encoded = value.to_string();
        if !encoded.contains(expected) {
            return Err(DeploymentError::Health(
                "status identity does not contain expected version".to_owned(),
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
        validate_transition(workspace, transition, require_root_owner)?;
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
    for digest in [
        &manifest.probe_binary_sha256,
        &manifest.operator_binary_sha256,
        &manifest.frontend.old_index_sha256,
        &manifest.frontend.new_index_sha256,
        &manifest.environment_restore_sha256,
    ] {
        require_sha256(digest)?;
    }
    validate_staged(
        workspace,
        &manifest.probe_binary,
        &manifest.probe_binary_sha256,
        require_root_owner,
    )?;
    validate_staged(
        workspace,
        &manifest.operator_binary,
        &manifest.operator_binary_sha256,
        require_root_owner,
    )?;
    Ok(())
}

fn validate_transition(
    workspace: &Workspace,
    transition: &PackageTransition,
    require_root_owner: bool,
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
    )?;
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

fn validate_staged(
    workspace: &Workspace,
    path: &Path,
    digest: &str,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    let clean = clean_absolute(path)?;
    if clean.parent() != Some(workspace.staging.as_path()) {
        return Err(DeploymentError::UnsafePath(
            "staged evidence escapes workspace".to_owned(),
        ));
    }
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
    Ok(())
}

fn verify_manifest_evidence_for_rollback(
    manifest: &ProductionManifest,
    require_root_owner: bool,
) -> Result<(), DeploymentError> {
    if sha256_file(&manifest.go.rollback_path, require_root_owner)? != manifest.go.rollback_sha256
        || sha256_file(&manifest.web.rollback_path, require_root_owner)?
            != manifest.web.rollback_sha256
    {
        return Err(DeploymentError::InvalidEvidence(
            "rollback package evidence changed during operation".to_owned(),
        ));
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

fn verify_active_provider(package: &str) -> Result<ProviderLinkStatus, DeploymentError> {
    let expected = provider_for_package(package)?;
    let status = os_manager().status()?;
    let expected_name = expected.package_prefix();
    if status.package != package
        && !(status.package.starts_with(expected_name)
            && package.starts_with(expected_name))
    {
        return Err(DeploymentError::InvalidEvidence(
            "active provider package does not match deployment manifest".to_owned(),
        ));
    }
    Ok(status)
}

fn provider_for_package(name: &str) -> Result<Provider, DeploymentError> {
    if name.starts_with("lmm-api-go") {
        Ok(Provider::Go)
    } else if name.starts_with("lmm-api-rs") {
        Ok(Provider::Rust)
    } else {
        Err(DeploymentError::InvalidSchema(format!(
            "rollback backend package {name:?} has no safe provider mapping"
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
        .is_some_and(|(name, version)| name == package && valid_version(version))
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
    use super::*;

    #[test]
    fn formats_match_manual_only_go_contract() {
        assert_eq!((MANIFEST_FORMAT, STATUS_FORMAT), (7, 2));
        assert_eq!((RELEASE_PLAN_FORMAT, RELEASE_STATE_FORMAT), (4, 3));
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
}
