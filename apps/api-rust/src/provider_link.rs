//! Safe management of the generic backend provider link.
//!
//! Deployment code must execute `/usr/bin/lmm-api`.  This module is the only
//! place allowed to change which real provider that generic entry point names.

use std::{
    fs::{self, File, Metadata},
    io,
    os::unix::fs::{MetadataExt, PermissionsExt},
    path::{Path, PathBuf},
    process::Command,
};

use serde::Serialize;
use thiserror::Error;

pub const GENERIC_BINARY: &str = "/usr/bin/lmm-api";
pub const GO_PROVIDER: &str = "lmm-api-go";
pub const RUST_PROVIDER: &str = "lmm-api-rs";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Provider {
    Go,
    Rust,
}

impl Provider {
    #[must_use]
    pub const fn filename(self) -> &'static str {
        match self {
            Self::Go => GO_PROVIDER,
            Self::Rust => RUST_PROVIDER,
        }
    }

    #[must_use]
    pub const fn package_prefix(self) -> &'static str {
        match self {
            Self::Go => "lmm-api-go",
            Self::Rust => "lmm-api-rs",
        }
    }

    #[must_use]
    pub fn accepts_package(self, package: &str) -> bool {
        match self {
            Self::Go => matches!(package, "lmm-api-go" | "lmm-api-go-bin" | "lmm-api-go-git"),
            Self::Rust => matches!(package, "lmm-api-rs" | "lmm-api-rs-bin" | "lmm-api-rs-git"),
        }
    }
}

impl std::str::FromStr for Provider {
    type Err = ProviderLinkError;

    fn from_str(value: &str) -> Result<Self, Self::Err> {
        match value {
            "go" | GO_PROVIDER => Ok(Self::Go),
            "rust" | "rs" | RUST_PROVIDER => Ok(Self::Rust),
            _ => Err(ProviderLinkError::InvalidProvider(value.to_owned())),
        }
    }
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct ProviderLinkStatus {
    pub link: String,
    pub target: String,
    pub provider: String,
    pub real_provider: String,
    pub package: String,
}

#[derive(Debug, Error)]
pub enum ProviderLinkError {
    #[error("backend provider selection must run as root")]
    RootRequired,
    #[error("unknown backend provider {0:?}; choose go or rust")]
    InvalidProvider(String),
    #[error("generic backend entry must be a one-hop relative symlink")]
    UnsafeLink,
    #[error("backend provider is not a root-owned executable regular file")]
    UnsafeProvider,
    #[error("backend provider escapes the generic binary directory")]
    ProviderEscapesDirectory,
    #[error("backend provider package ownership is invalid: {0}")]
    InvalidPackageOwner(String),
    #[error("backend provider filesystem operation failed: {0}")]
    Io(#[from] io::Error),
    #[error("package ownership command failed: {0}")]
    PackageCommand(String),
}

/// Injectable operating-system boundary used by provider-link unit tests.
pub trait ProviderLinkSystem {
    fn effective_uid(&self) -> u32;
    fn symlink_metadata(&self, path: &Path) -> io::Result<Metadata>;
    fn metadata(&self, path: &Path) -> io::Result<Metadata>;
    fn owner_uid(&self, metadata: &Metadata) -> u32;
    fn read_link(&self, path: &Path) -> io::Result<PathBuf>;
    fn canonicalize(&self, path: &Path) -> io::Result<PathBuf>;
    fn remove_file(&self, path: &Path) -> io::Result<()>;
    fn symlink(&self, target: &Path, link: &Path) -> io::Result<()>;
    fn rename(&self, from: &Path, to: &Path) -> io::Result<()>;
    fn sync_directory(&self, path: &Path) -> io::Result<()>;
    fn package_owner(&self, path: &Path) -> Result<String, ProviderLinkError>;
}

#[derive(Clone, Copy, Debug, Default)]
pub struct OsProviderLinkSystem;

impl ProviderLinkSystem for OsProviderLinkSystem {
    fn effective_uid(&self) -> u32 {
        rustix::process::geteuid().as_raw()
    }

    fn symlink_metadata(&self, path: &Path) -> io::Result<Metadata> {
        fs::symlink_metadata(path)
    }

    fn metadata(&self, path: &Path) -> io::Result<Metadata> {
        fs::metadata(path)
    }

    fn owner_uid(&self, metadata: &Metadata) -> u32 {
        metadata.uid()
    }

    fn read_link(&self, path: &Path) -> io::Result<PathBuf> {
        fs::read_link(path)
    }

    fn canonicalize(&self, path: &Path) -> io::Result<PathBuf> {
        fs::canonicalize(path)
    }

    fn remove_file(&self, path: &Path) -> io::Result<()> {
        fs::remove_file(path)
    }

    fn symlink(&self, target: &Path, link: &Path) -> io::Result<()> {
        std::os::unix::fs::symlink(target, link)
    }

    fn rename(&self, from: &Path, to: &Path) -> io::Result<()> {
        fs::rename(from, to)
    }

    fn sync_directory(&self, path: &Path) -> io::Result<()> {
        File::open(path)?.sync_all()
    }

    fn package_owner(&self, path: &Path) -> Result<String, ProviderLinkError> {
        let output = Command::new("/usr/bin/pacman")
            .args(["-Qqo", "--"])
            .arg(path)
            .env("LC_ALL", "C")
            .output()
            .map_err(ProviderLinkError::Io)?;
        if !output.status.success() {
            return Err(ProviderLinkError::PackageCommand(
                String::from_utf8_lossy(&output.stderr).trim().to_owned(),
            ));
        }
        let owner = String::from_utf8_lossy(&output.stdout).trim().to_owned();
        if owner.is_empty() || owner.lines().count() != 1 {
            return Err(ProviderLinkError::InvalidPackageOwner(owner));
        }
        Ok(owner)
    }
}

pub struct ProviderLinkManager<S> {
    system: S,
    generic: PathBuf,
}

impl<S: ProviderLinkSystem> ProviderLinkManager<S> {
    #[must_use]
    pub fn new(system: S, generic: PathBuf) -> Self {
        Self { system, generic }
    }

    pub fn status(&self) -> Result<ProviderLinkStatus, ProviderLinkError> {
        let link_metadata = self.system.symlink_metadata(&self.generic)?;
        if !link_metadata.file_type().is_symlink() {
            return Err(ProviderLinkError::UnsafeLink);
        }
        let target = self.system.read_link(&self.generic)?;
        let target_text = target.to_str().ok_or(ProviderLinkError::UnsafeLink)?;
        if target.is_absolute()
            || target.components().count() != 1
            || (target_text != GO_PROVIDER && target_text != RUST_PROVIDER)
        {
            return Err(ProviderLinkError::UnsafeLink);
        }
        let provider = target_text.parse::<Provider>()?;
        let directory = self.generic.parent().ok_or(ProviderLinkError::UnsafeLink)?;
        let provider_path = directory.join(&target);
        let provider_metadata = self.system.symlink_metadata(&provider_path)?;
        if provider_metadata.file_type().is_symlink()
            || !provider_metadata.is_file()
            || self.system.owner_uid(&provider_metadata) != 0
            || provider_metadata.permissions().mode() & 0o111 == 0
            || provider_metadata.permissions().mode() & 0o022 != 0
        {
            return Err(ProviderLinkError::UnsafeProvider);
        }
        let canonical_directory = self.system.canonicalize(directory)?;
        let canonical_provider = self.system.canonicalize(&provider_path)?;
        if canonical_provider.parent() != Some(canonical_directory.as_path()) {
            return Err(ProviderLinkError::ProviderEscapesDirectory);
        }
        let followed_metadata = self.system.metadata(&provider_path)?;
        if followed_metadata.dev() != provider_metadata.dev()
            || followed_metadata.ino() != provider_metadata.ino()
        {
            return Err(ProviderLinkError::UnsafeProvider);
        }
        let package = self.system.package_owner(&provider_path)?;
        if !provider.accepts_package(&package) {
            return Err(ProviderLinkError::InvalidPackageOwner(package));
        }
        Ok(ProviderLinkStatus {
            link: self.generic.display().to_string(),
            target: target_text.to_owned(),
            provider: match provider {
                Provider::Go => "go",
                Provider::Rust => "rust",
            }
            .to_owned(),
            real_provider: canonical_provider.display().to_string(),
            package,
        })
    }

    pub fn select(&self, provider: Provider) -> Result<ProviderLinkStatus, ProviderLinkError> {
        if self.system.effective_uid() != 0 {
            return Err(ProviderLinkError::RootRequired);
        }
        let directory = self.generic.parent().ok_or(ProviderLinkError::UnsafeLink)?;
        let provider_path = directory.join(provider.filename());
        self.validate_candidate(provider, &provider_path)?;

        let temporary = directory.join(format!(".lmm-api.{}.new", std::process::id()));
        match self.system.remove_file(&temporary) {
            Ok(()) => {}
            Err(error) if error.kind() == io::ErrorKind::NotFound => {}
            Err(error) => return Err(error.into()),
        }
        self.system
            .symlink(Path::new(provider.filename()), &temporary)?;
        let temporary_metadata = self.system.symlink_metadata(&temporary)?;
        if !temporary_metadata.file_type().is_symlink()
            || self.system.read_link(&temporary)? != Path::new(provider.filename())
        {
            let _ = self.system.remove_file(&temporary);
            return Err(ProviderLinkError::UnsafeLink);
        }
        if let Err(error) = self.system.rename(&temporary, &self.generic) {
            let _ = self.system.remove_file(&temporary);
            return Err(error.into());
        }
        self.system.sync_directory(directory)?;
        self.status()
    }

    fn validate_candidate(
        &self,
        provider: Provider,
        provider_path: &Path,
    ) -> Result<(), ProviderLinkError> {
        let metadata = self.system.symlink_metadata(provider_path)?;
        if metadata.file_type().is_symlink()
            || !metadata.is_file()
            || self.system.owner_uid(&metadata) != 0
            || metadata.permissions().mode() & 0o111 == 0
            || metadata.permissions().mode() & 0o022 != 0
        {
            return Err(ProviderLinkError::UnsafeProvider);
        }
        let directory = self.generic.parent().ok_or(ProviderLinkError::UnsafeLink)?;
        let canonical_directory = self.system.canonicalize(directory)?;
        let canonical_provider = self.system.canonicalize(provider_path)?;
        if canonical_provider.parent() != Some(canonical_directory.as_path()) {
            return Err(ProviderLinkError::ProviderEscapesDirectory);
        }
        let package = self.system.package_owner(provider_path)?;
        if !provider.accepts_package(&package) {
            return Err(ProviderLinkError::InvalidPackageOwner(package));
        }
        Ok(())
    }
}

#[must_use]
pub fn os_manager() -> ProviderLinkManager<OsProviderLinkSystem> {
    ProviderLinkManager::new(OsProviderLinkSystem, PathBuf::from(GENERIC_BINARY))
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::sync::Mutex;

    struct TestSystem {
        selected_owner: Mutex<String>,
    }

    impl ProviderLinkSystem for TestSystem {
        fn effective_uid(&self) -> u32 {
            0
        }

        fn symlink_metadata(&self, path: &Path) -> io::Result<Metadata> {
            fs::symlink_metadata(path)
        }

        fn metadata(&self, path: &Path) -> io::Result<Metadata> {
            fs::metadata(path)
        }

        fn owner_uid(&self, _metadata: &Metadata) -> u32 {
            0
        }

        fn read_link(&self, path: &Path) -> io::Result<PathBuf> {
            fs::read_link(path)
        }

        fn canonicalize(&self, path: &Path) -> io::Result<PathBuf> {
            fs::canonicalize(path)
        }

        fn remove_file(&self, path: &Path) -> io::Result<()> {
            fs::remove_file(path)
        }

        fn symlink(&self, target: &Path, link: &Path) -> io::Result<()> {
            std::os::unix::fs::symlink(target, link)
        }

        fn rename(&self, from: &Path, to: &Path) -> io::Result<()> {
            fs::rename(from, to)
        }

        fn sync_directory(&self, path: &Path) -> io::Result<()> {
            File::open(path)?.sync_all()
        }

        fn package_owner(&self, path: &Path) -> Result<String, ProviderLinkError> {
            let filename = path
                .file_name()
                .and_then(|value| value.to_str())
                .unwrap_or_default();
            let owner = if filename == GO_PROVIDER {
                "lmm-api-go-bin"
            } else {
                "lmm-api-rs-git"
            };
            *self.selected_owner.lock().map_err(|_| {
                ProviderLinkError::PackageCommand("test owner lock poisoned".to_owned())
            })? = owner.to_owned();
            Ok(owner.to_owned())
        }
    }

    fn test_root() -> io::Result<PathBuf> {
        let root = std::env::temp_dir().join(format!(
            "lmm-provider-link-{}-{}",
            std::process::id(),
            std::thread::current().name().unwrap_or("test")
        ));
        let _ = fs::remove_dir_all(&root);
        fs::create_dir(&root)?;
        for provider in [GO_PROVIDER, RUST_PROVIDER] {
            let path = root.join(provider);
            fs::write(&path, b"provider")?;
            fs::set_permissions(&path, fs::Permissions::from_mode(0o755))?;
        }
        std::os::unix::fs::symlink(GO_PROVIDER, root.join("lmm-api"))?;
        Ok(root)
    }

    #[test]
    fn select_uses_relative_one_hop_atomic_link() -> Result<(), Box<dyn std::error::Error>> {
        let root = test_root()?;
        let manager = ProviderLinkManager::new(
            TestSystem {
                selected_owner: Mutex::new(String::new()),
            },
            root.join("lmm-api"),
        );
        let status = manager.select(Provider::Rust)?;
        assert_eq!(status.target, RUST_PROVIDER);
        assert_eq!(
            fs::read_link(root.join("lmm-api"))?,
            Path::new(RUST_PROVIDER)
        );
        assert!(
            !root
                .join(format!(".lmm-api.{}.new", std::process::id()))
                .exists()
        );
        fs::remove_dir_all(root)?;
        Ok(())
    }

    #[test]
    fn provider_package_allowlist_rejects_prefix_spoofing() {
        assert!(Provider::Go.accepts_package("lmm-api-go-bin"));
        assert!(Provider::Rust.accepts_package("lmm-api-rs-git"));
        assert!(!Provider::Go.accepts_package("lmm-api-go-evil"));
        assert!(!Provider::Rust.accepts_package("lmm-api-rs-git-evil"));
        assert!(!Provider::Go.accepts_package("lmm-api-rs-git"));
    }

    #[test]
    fn status_rejects_absolute_generic_link() -> Result<(), Box<dyn std::error::Error>> {
        let root = test_root()?;
        fs::remove_file(root.join("lmm-api"))?;
        std::os::unix::fs::symlink(root.join(GO_PROVIDER), root.join("lmm-api"))?;
        let manager = ProviderLinkManager::new(
            TestSystem {
                selected_owner: Mutex::new(String::new()),
            },
            root.join("lmm-api"),
        );
        assert!(matches!(
            manager.status(),
            Err(ProviderLinkError::UnsafeLink)
        ));
        fs::remove_dir_all(root)?;
        Ok(())
    }
}
