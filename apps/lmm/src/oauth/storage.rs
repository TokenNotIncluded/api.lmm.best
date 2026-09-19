use super::{AuthError, protocol::Credential};
use fs2::FileExt;
use serde::{Deserialize, Serialize};
use std::{
    env,
    fs::{self, File, OpenOptions},
    path::PathBuf,
};
use zeroize::Zeroizing;

#[derive(Deserialize, Serialize)]
pub(super) struct StoredSession {
    pub credential: Credential,
    pub refresh_blocked: bool,
}

pub(super) trait SecretStore {
    fn load(&self) -> Result<Option<StoredSession>, AuthError>;
    fn save(&self, session: &StoredSession) -> Result<(), AuthError>;
    fn delete(&self) -> Result<(), AuthError>;
}

pub(super) struct SystemStore {
    entry: keyring::Entry,
    issuer: String,
}

impl SystemStore {
    pub fn new(issuer: &str) -> Result<Self, AuthError> {
        Ok(Self {
            entry: keyring::Entry::new("best.lmm.cli.oauth", issuer)
                .map_err(|_| AuthError::CredentialStore)?,
            issuer: issuer.to_owned(),
        })
    }
}

impl SecretStore for SystemStore {
    fn load(&self) -> Result<Option<StoredSession>, AuthError> {
        let raw = match self.entry.get_password() {
            Ok(raw) => Zeroizing::new(raw),
            Err(keyring::Error::NoEntry) => return Ok(None),
            Err(_) => return Err(AuthError::CredentialStore),
        };
        if raw.len() > 64 * 1024 {
            return Err(AuthError::CredentialStore);
        }
        let session: StoredSession =
            serde_json::from_str(&raw).map_err(|_| AuthError::CredentialStore)?;
        if !session.credential.valid_for(&self.issuer) {
            return Err(AuthError::CredentialStore);
        }
        Ok(Some(session))
    }

    fn save(&self, session: &StoredSession) -> Result<(), AuthError> {
        if !session.credential.valid_for(&self.issuer) {
            return Err(AuthError::CredentialStore);
        }
        let raw =
            Zeroizing::new(serde_json::to_string(session).map_err(|_| AuthError::CredentialStore)?);
        self.entry
            .set_password(&raw)
            .map_err(|_| AuthError::CredentialStore)
    }

    fn delete(&self) -> Result<(), AuthError> {
        match self.entry.delete_credential() {
            Ok(()) | Err(keyring::Error::NoEntry) => Ok(()),
            Err(_) => Err(AuthError::CredentialStore),
        }
    }
}

fn lock_directory() -> Result<PathBuf, AuthError> {
    let base = if cfg!(windows) {
        env::var_os("LOCALAPPDATA").map(PathBuf::from)
    } else if cfg!(target_os = "macos") {
        crate::environment::user_home().map(|home| home.join("Library/Application Support"))
    } else {
        env::var_os("XDG_STATE_HOME")
            .map(PathBuf::from)
            .or_else(|| crate::environment::user_home().map(|home| home.join(".local/state")))
    }
    .filter(|path| path.is_absolute())
    .ok_or(AuthError::Busy)?;
    Ok(base.join("lmm"))
}

/// The lock stores no secrets; OS process exit releases it even after cancellation.
pub(super) fn lock() -> Result<File, AuthError> {
    lock_at(&lock_directory()?)
}

fn lock_at(directory: &std::path::Path) -> Result<File, AuthError> {
    let mut builder = fs::DirBuilder::new();
    builder.recursive(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::DirBuilderExt;
        builder.mode(0o700);
    }
    builder.create(directory).map_err(|_| AuthError::Busy)?;
    let metadata = fs::symlink_metadata(directory).map_err(|_| AuthError::Busy)?;
    if !metadata.is_dir() || metadata.file_type().is_symlink() {
        return Err(AuthError::Busy);
    }
    #[cfg(unix)]
    {
        use std::os::unix::fs::PermissionsExt;
        if metadata.permissions().mode() & 0o077 != 0 {
            return Err(AuthError::Busy);
        }
    }
    let path = directory.join("oauth.lock");
    if fs::symlink_metadata(&path).is_ok_and(|meta| !meta.is_file()) {
        return Err(AuthError::Busy);
    }
    let mut options = OpenOptions::new();
    options.create(true).truncate(false).read(true).write(true);
    #[cfg(unix)]
    {
        use std::os::unix::fs::OpenOptionsExt;
        options.mode(0o600);
    }
    let file = options.open(path).map_err(|_| AuthError::Busy)?;
    file.try_lock_exclusive().map_err(|_| AuthError::Busy)?;
    Ok(file)
}

#[cfg(test)]
mod tests {
    #![allow(clippy::unwrap_used)]
    use super::*;

    #[test]
    fn lock_serializes_processes_and_releases_after_drop() {
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("state");
        let lock = lock_at(&path).unwrap();
        assert!(matches!(lock_at(&path), Err(AuthError::Busy)));
        drop(lock);
        assert!(lock_at(&path).is_ok());
    }

    #[cfg(unix)]
    #[test]
    fn lock_refuses_symlink_and_shared_directory() {
        use std::os::unix::fs::{PermissionsExt, symlink};
        let root = tempfile::tempdir().unwrap();
        let path = root.path().join("state");
        fs::create_dir(&path).unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o755)).unwrap();
        assert!(matches!(lock_at(&path), Err(AuthError::Busy)));
        fs::set_permissions(&path, fs::Permissions::from_mode(0o700)).unwrap();
        symlink(root.path().join("elsewhere"), path.join("oauth.lock")).unwrap();
        assert!(matches!(lock_at(&path), Err(AuthError::Busy)));
    }
}
