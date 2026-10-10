//! Bounded local probes. Finding a path is evidence, not proof of installation.

use crate::catalog::Software;
use serde::Serialize;
use sha2::{Digest, Sha256};
use std::{
    collections::BTreeSet,
    fs,
    io::{self, Read},
    path::{Path, PathBuf},
};

const MAX_CONFIG_BYTES: u64 = 1024 * 1024;

/// The result of probing one exact path, without leaking file contents.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Observation {
    /// A regular file was found; version and provenance are not established.
    Present,
    /// The probed location does not exist. Other locations may exist.
    NotFound,
    /// Access, file type, symlink or size prevented a trustworthy check.
    Unknown,
}

/// Public evidence contains no configuration values or credentials.
#[derive(Debug, Serialize)]
pub struct Evidence {
    /// Exact local path inspected; redacted from shareable reports.
    pub path: PathBuf,
    /// File observation only, never an application health result.
    pub observation: Observation,
    /// Description of the limited probe.
    pub kind: &'static str,
}

/// Independent configuration, connectivity and application verification results.
#[derive(Debug, Serialize)]
pub struct Verification {
    /// Whether the selected configuration was semantically validated.
    pub configuration: &'static str,
    /// Whether LMM connectivity was tested.
    pub connectivity: &'static str,
    /// Whether the target application actually called LMM.
    pub target_call: &'static str,
    /// Separate advertised capability checks.
    pub streaming: &'static str,
    /// Tool invocation checks use an isolated fixture, never real tools.
    pub tool_calling: &'static str,
    /// Multi-turn scenario result.
    pub multi_turn: &'static str,
    /// Timestamp of an actual verification, not of discovery.
    pub verified_at: Option<u64>,
}

impl Default for Verification {
    fn default() -> Self {
        Self {
            configuration: "not_checked",
            connectivity: "not_checked",
            target_call: "not_checked",
            streaming: "not_checked",
            tool_calling: "not_checked",
            multi_turn: "not_checked",
            verified_at: None,
        }
    }
}

/// Local software status deliberately preserves uncertainty.
#[derive(Debug, Serialize)]
pub struct TargetStatus {
    /// Catalog identity.
    pub software: &'static str,
    /// Explicit instance root, when selected by the user.
    pub instance: Option<PathBuf>,
    /// Local evidence; no inference about Docker or remote installations.
    pub evidence: Vec<Evidence>,
    /// A binary/config path does not prove an installed compatible release.
    pub installation: &'static str,
    /// Installation manager cannot be inferred from configuration manager.
    pub installation_manager: &'static str,
    /// CC Switch presence never implies management of another target.
    pub configuration_manager: &'static str,
    /// Credential validity requires authoritative verification.
    pub authorization: &'static str,
    /// Provider existence is separate from active selection and route.
    pub lmm_added: &'static str,
    /// Current selection has not been read through a qualified adapter yet.
    pub lmm_enabled: &'static str,
    /// Local route cannot be inferred just from a base URL.
    pub actual_route: &'static str,
    /// Capability-specific verification.
    pub verification: Verification,
}

/// Inject paths into discovery to make environment boundaries testable.
pub struct ProbeContext {
    /// Current user's home; missing home does not trigger a fallback scan.
    pub home: Option<PathBuf>,
    /// Explicit, absolute PATH directories in this environment only.
    pub path: Vec<PathBuf>,
}

/// Inspect file type without following symlinks into an unrelated scope.
pub fn observe(path: &Path) -> Observation {
    match fs::symlink_metadata(path) {
        Ok(meta) if meta.is_file() => Observation::Present,
        Ok(_) => Observation::Unknown,
        Err(err) if err.kind() == io::ErrorKind::NotFound => Observation::NotFound,
        Err(_) => Observation::Unknown,
    }
}

/// Discover explicit targets without launching executables or contacting services.
pub fn discover(
    software: &'static Software,
    context: &ProbeContext,
    instance: Option<&Path>,
) -> TargetStatus {
    let mut evidence = Vec::new();
    let mut seen = BTreeSet::new();
    for directory in &context.path {
        // Ignore implicit current-directory PATH entries; discovery never searches a project.
        if !directory.is_absolute() {
            continue;
        }
        for executable in software.executables {
            let names = if cfg!(windows) {
                vec![
                    format!("{executable}.exe"),
                    format!("{executable}.cmd"),
                    format!("{executable}.bat"),
                ]
            } else {
                vec![(*executable).to_owned()]
            };
            for name in names {
                let path = directory.join(name);
                if !seen.insert(path.clone()) {
                    continue;
                }
                let observation = observe(&path);
                if observation != Observation::NotFound {
                    evidence.push(Evidence {
                        path,
                        observation,
                        kind: "path_candidate_not_executed",
                    });
                }
            }
        }
    }
    let config = match (software.id, instance, &context.home) {
        ("astrbot", Some(root), _) => Some(root.join("data/cmd_config.json")),
        ("cc-switch", Some(root), _) => Some(root.join("cc-switch.db")),
        ("cc-switch", None, Some(home)) => Some(home.join(".cc-switch/cc-switch.db")),
        _ => None,
    };
    if let Some(path) = config {
        evidence.push(Evidence {
            observation: observe(&path),
            path,
            kind: "configuration_location_only",
        });
    }
    TargetStatus {
        software: software.id,
        instance: instance.map(Path::to_path_buf),
        evidence,
        installation: "unknown",
        installation_manager: "unknown",
        configuration_manager: "unknown",
        authorization: "not_checked",
        lmm_added: "unknown",
        lmm_enabled: "unknown",
        actual_route: "not_checked",
        verification: Verification::default(),
    }
}

/// A bounded content fingerprint for later optimistic conflict checks.
///
/// This is not a cross-process lock or an atomic filesystem compare-and-swap.
/// Adapters still need a native transaction or an exclusive safe editing boundary.
pub fn fingerprint(path: &Path) -> io::Result<String> {
    let meta = fs::symlink_metadata(path)?;
    if !meta.is_file() || meta.len() > MAX_CONFIG_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "not a bounded regular configuration file",
        ));
    }
    let mut data = Vec::new();
    fs::File::open(path)?
        .take(MAX_CONFIG_BYTES + 1)
        .read_to_end(&mut data)?;
    if data.len() as u64 > MAX_CONFIG_BYTES {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "configuration exceeds size limit",
        ));
    }
    Ok(format!("{:x}", Sha256::digest(data)))
}
