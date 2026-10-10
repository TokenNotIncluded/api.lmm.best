//! Describe the current process boundary without scanning remote systems.

use serde::Serialize;
use std::{env, fs, path::PathBuf};

/// Environmental signals are independent: WSL can also host containers.
#[derive(Debug, Serialize)]
pub struct Environment {
    /// Current process operating system.
    pub os: &'static str,
    /// Current process architecture.
    pub arch: &'static str,
    /// WSL marker observed, without implying access to the Windows host.
    pub wsl: bool,
    /// Container marker observed; false does not rule out a container.
    pub container_marker: bool,
    /// SSH marker observed; this still describes the machine running LMM.
    pub ssh_session: bool,
    /// No browser is launched during environment discovery.
    pub browser: &'static str,
    /// Scope of every probe in this release.
    pub scope: &'static str,
}

impl Environment {
    /// Inspect a few local process and OS markers only.
    pub fn detect() -> Self {
        let kernel = if cfg!(target_os = "linux") {
            fs::read_to_string("/proc/sys/kernel/osrelease").unwrap_or_default()
        } else {
            String::new()
        };
        Self {
            os: env::consts::OS,
            arch: env::consts::ARCH,
            wsl: env::var_os("WSL_DISTRO_NAME").is_some()
                || kernel.to_lowercase().contains("microsoft"),
            container_marker: PathBuf::from("/.dockerenv").exists()
                || PathBuf::from("/run/.containerenv").exists()
                || env::var_os("container").is_some(),
            ssh_session: env::var_os("SSH_CONNECTION").is_some(),
            browser: "not_checked",
            scope: "current_user_current_environment",
        }
    }
}

/// Obtain the current platform's user home; never fall back to the project root.
pub fn user_home() -> Option<PathBuf> {
    let key = if cfg!(windows) { "USERPROFILE" } else { "HOME" };
    env::var_os(key)
        .filter(|value| !value.is_empty())
        .map(PathBuf::from)
}
