//! Native immutable frontend release publication.

use std::{
    fs::{self, File, OpenOptions},
    io,
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Component, Path, PathBuf},
    time::SystemTime,
};

use fs2::FileExt;
use serde::Serialize;
use thiserror::Error;

#[derive(Debug, Error)]
pub enum FrontendDeployError {
    #[error("frontend path is unsafe: {0}")]
    UnsafePath(String),
    #[error("frontend release tree is invalid: {0}")]
    InvalidTree(String),
    #[error("frontend release {0:?} already exists")]
    ReleaseExists(String),
    #[error("no previous frontend release is available")]
    NoPreviousRelease,
    #[error("another frontend release operation is running")]
    Busy,
    #[error("frontend package activation must run as root")]
    RootRequired,
    #[error("frontend command failed: {0}")]
    Command(String),
    #[error("frontend filesystem operation failed: {0}")]
    Io(#[from] io::Error),
    #[error("frontend state encoding failed: {0}")]
    Json(#[from] serde_json::Error),
}

pub fn prepare(root: &Path) -> Result<(), FrontendDeployError> {
    require_absolute_non_root(root)?;
    match fs::symlink_metadata(root) {
        Ok(metadata) if metadata.file_type().is_symlink() || !metadata.is_dir() => {
            return Err(FrontendDeployError::UnsafePath(root.display().to_string()));
        }
        Ok(_) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => fs::create_dir_all(root)?,
        Err(error) => return Err(error.into()),
    }
    fs::set_permissions(root, fs::Permissions::from_mode(0o755))?;
    for (name, mode) in [("releases", 0o755), (".staging", 0o700), ("assets", 0o755)] {
        let path = root.join(name);
        fs::create_dir_all(&path)?;
        let metadata = fs::symlink_metadata(&path)?;
        if metadata.file_type().is_symlink() || !metadata.is_dir() {
            return Err(FrontendDeployError::UnsafePath(path.display().to_string()));
        }
        fs::set_permissions(path, fs::Permissions::from_mode(mode))?;
    }
    File::open(root)?.sync_all()?;
    Ok(())
}

pub fn publish(
    root: &Path,
    source: &Path,
    release: &str,
    keep: usize,
) -> Result<String, FrontendDeployError> {
    validate_release(release)?;
    if keep == 0 {
        return Err(FrontendDeployError::InvalidTree(
            "keep must be positive".to_owned(),
        ));
    }
    prepare(root)?;
    let _lock = lock(root)?;
    validate_tree(source)?;
    let target = root.join("releases").join(release);
    if fs::symlink_metadata(&target).is_ok() {
        return Err(FrontendDeployError::ReleaseExists(release.to_owned()));
    }
    let stage = root
        .join(".staging")
        .join(format!("{release}.{}.new", std::process::id()));
    if fs::symlink_metadata(&stage).is_ok() {
        fs::remove_dir_all(&stage)?;
    }
    fs::create_dir(&stage)?;
    if let Err(error) = copy_tree(source, &stage)
        .and_then(|()| normalize_tree(&stage))
        .and_then(|()| validate_tree(&stage))
        .and_then(|()| fs::rename(&stage, &target).map_err(FrontendDeployError::from))
        .and_then(|()| switch_current(root, release))
    {
        let _ = fs::remove_dir_all(&stage);
        return Err(error);
    }
    prune(root, keep)?;
    Ok(release.to_owned())
}

pub fn rollback(
    root: &Path,
    requested: Option<&str>,
    keep: usize,
) -> Result<String, FrontendDeployError> {
    if keep == 0 {
        return Err(FrontendDeployError::InvalidTree(
            "keep must be positive".to_owned(),
        ));
    }
    prepare(root)?;
    let _lock = lock(root)?;
    let current = current(root)?;
    let release = match requested {
        Some(value) => {
            validate_release(value)?;
            value.to_owned()
        }
        None => releases(root, Some(&current))?
            .into_iter()
            .next()
            .map(|entry| entry.0)
            .ok_or(FrontendDeployError::NoPreviousRelease)?,
    };
    validate_tree(&root.join("releases").join(&release))?;
    switch_current(root, &release)?;
    prune(root, keep)?;
    Ok(release)
}

#[derive(Clone, Debug, Serialize)]
struct PackageActivationStatus {
    format: u32,
    phase: String,
    package_version: String,
    release: String,
    previous_release: String,
    revision: String,
    failure: String,
}

/// Activates the frontend tree installed by `lmm-api-web-bin`.
///
/// Once `current` changes, every later error is persisted as
/// `ROLLBACK_REQUIRED`; this function deliberately never restores the previous
/// link. Recovery is an explicit `deploy frontend rollback --release ...`.
pub fn package_activate(package_version: &str) -> Result<String, FrontendDeployError> {
    if rustix::process::geteuid().as_raw() != 0 {
        return Err(FrontendDeployError::RootRequired);
    }
    let root = PathBuf::from(
        std::env::var_os("LMM_API_WEB_ROOT").unwrap_or_else(|| "/srv/lmm-api-frontend".into()),
    );
    let source = PathBuf::from(
        std::env::var_os("LMM_API_WEB_SOURCE")
            .unwrap_or_else(|| "/usr/share/lmm-api-web/frontend-dist".into()),
    );
    let revision_file = PathBuf::from(
        std::env::var_os("LMM_API_WEB_REVISION_FILE")
            .unwrap_or_else(|| "/usr/share/doc/lmm-api-web-bin/REVISION".into()),
    );
    let keep = std::env::var("LMM_API_WEB_KEEP")
        .map_or(Ok(3), |value| value.parse::<usize>())
        .map_err(|_| FrontendDeployError::InvalidTree("retention must be positive".to_owned()))?;
    if keep == 0 {
        return Err(FrontendDeployError::InvalidTree(
            "retention must be positive".to_owned(),
        ));
    }
    let revision = installed_revision(&revision_file)?;
    let release = package_release_id(package_version, &revision)?;
    validate_tree(&source)?;
    prepare(&root)?;
    run_command("/usr/bin/nginx", &["-t"])?;

    let previous = match fs::symlink_metadata(root.join("current")) {
        Ok(metadata) if metadata.file_type().is_symlink() => current(&root)?,
        Ok(_) => {
            return Err(FrontendDeployError::UnsafePath(
                root.join("current").display().to_string(),
            ));
        }
        Err(error) if error.kind() == io::ErrorKind::NotFound => String::new(),
        Err(error) => return Err(error.into()),
    };
    let state_dir = root.join(".activation-state");
    fs::create_dir_all(&state_dir)?;
    fs::set_permissions(&state_dir, fs::Permissions::from_mode(0o700))?;
    let state_path = state_dir.join(format!("{release}.json"));
    let mut status = PackageActivationStatus {
        format: 1,
        phase: "MUTATION_PENDING".to_owned(),
        package_version: package_version.to_owned(),
        release: release.clone(),
        previous_release: previous,
        revision,
        failure: String::new(),
    };
    write_activation_status(&state_path, &status)?;

    let result = (|| {
        let target = root.join("releases").join(&release);
        if fs::symlink_metadata(&target).is_ok() {
            validate_tree(&target)?;
            if !trees_equal(&source, &target)? {
                return Err(FrontendDeployError::InvalidTree(
                    "existing immutable release has different content".to_owned(),
                ));
            }
            if current(&root)? != release {
                switch_current(&root, &release)?;
            }
        } else {
            publish(&root, &source, &release, keep)?;
        }
        run_command("/usr/bin/nginx", &["-t"])?;
        run_command("/usr/bin/systemctl", &["reload", "nginx.service"])?;
        run_command(
            "/usr/bin/systemctl",
            &["is-active", "--quiet", "nginx.service"],
        )?;
        if current(&root)? != release || !trees_equal(&source, &target)? {
            return Err(FrontendDeployError::InvalidTree(
                "activated frontend identity check failed".to_owned(),
            ));
        }
        Ok(())
    })();
    match result {
        Ok(()) => {
            status.phase = "ACTIVATED".to_owned();
            write_activation_status(&state_path, &status)?;
            Ok(release)
        }
        Err(error) => {
            status.phase = "ROLLBACK_REQUIRED".to_owned();
            status.failure = error.to_string();
            if let Err(state_error) = write_activation_status(&state_path, &status) {
                return Err(FrontendDeployError::Command(format!(
                    "{error}; preserve ROLLBACK_REQUIRED state: {state_error}"
                )));
            }
            Err(error)
        }
    }
}

pub fn current(root: &Path) -> Result<String, FrontendDeployError> {
    let target = fs::read_link(root.join("current"))?;
    let mut components = target.components();
    if components.next() != Some(Component::Normal("releases".as_ref())) {
        return Err(FrontendDeployError::UnsafePath(
            target.display().to_string(),
        ));
    }
    let Some(Component::Normal(release)) = components.next() else {
        return Err(FrontendDeployError::UnsafePath(
            target.display().to_string(),
        ));
    };
    if components.next().is_some() {
        return Err(FrontendDeployError::UnsafePath(
            target.display().to_string(),
        ));
    }
    let release = release
        .to_str()
        .ok_or_else(|| FrontendDeployError::UnsafePath(target.display().to_string()))?;
    validate_release(release)?;
    Ok(release.to_owned())
}

fn lock(root: &Path) -> Result<File, FrontendDeployError> {
    let file = OpenOptions::new()
        .read(true)
        .write(true)
        .create(true)
        .truncate(false)
        .mode(0o600)
        .open(root.join(".release.lock"))?;
    file.try_lock_exclusive().map_err(|error| {
        if error.kind() == io::ErrorKind::WouldBlock {
            FrontendDeployError::Busy
        } else {
            FrontendDeployError::Io(error)
        }
    })?;
    fs::set_permissions(
        root.join(".release.lock"),
        fs::Permissions::from_mode(0o600),
    )?;
    Ok(file)
}

fn installed_revision(path: &Path) -> Result<String, FrontendDeployError> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.file_type().is_symlink() || !metadata.is_file() || metadata.len() > 128 {
        return Err(FrontendDeployError::InvalidTree(
            "installed REVISION is missing or unsafe".to_owned(),
        ));
    }
    let bytes = fs::read(path)?;
    if bytes.last() != Some(&b'\n') || bytes[..bytes.len().saturating_sub(1)].contains(&b'\n') {
        return Err(FrontendDeployError::InvalidTree(
            "installed REVISION must contain one newline-terminated value".to_owned(),
        ));
    }
    let revision = std::str::from_utf8(&bytes[..bytes.len() - 1]).map_err(|_| {
        FrontendDeployError::InvalidTree("installed REVISION is invalid".to_owned())
    })?;
    if !(40..=64).contains(&revision.len())
        || !revision
            .bytes()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(&byte))
    {
        return Err(FrontendDeployError::InvalidTree(
            "installed REVISION is invalid".to_owned(),
        ));
    }
    Ok(revision.to_owned())
}

fn package_release_id(
    package_version: &str,
    revision: &str,
) -> Result<String, FrontendDeployError> {
    let normalized = package_version.replace(':', "-");
    let release = format!("{normalized}.g{}", &revision[..12]);
    validate_release(&release)?;
    Ok(release)
}

fn run_command(program: &str, args: &[&str]) -> Result<(), FrontendDeployError> {
    if !matches!(program, "/usr/bin/nginx" | "/usr/bin/systemctl") {
        return Err(FrontendDeployError::Command(
            "executable is not allowlisted".to_owned(),
        ));
    }
    let output = std::process::Command::new(program)
        .args(args)
        .env("LC_ALL", "C")
        .output()?;
    if output.status.success() {
        return Ok(());
    }
    let mut detail = String::from_utf8_lossy(&output.stderr).trim().to_owned();
    detail.truncate(1024);
    Err(FrontendDeployError::Command(format!(
        "{} failed: {detail}",
        Path::new(program)
            .file_name()
            .unwrap_or_default()
            .to_string_lossy()
    )))
}

fn write_activation_status(
    path: &Path,
    status: &PackageActivationStatus,
) -> Result<(), FrontendDeployError> {
    let mut bytes = serde_json::to_vec_pretty(status)?;
    bytes.push(b'\n');
    let parent = path.parent().ok_or_else(|| {
        FrontendDeployError::UnsafePath("activation state has no parent".to_owned())
    })?;
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
    use std::io::Write as _;
    file.write_all(&bytes)?;
    file.sync_all()?;
    fs::rename(&temporary, path)?;
    File::open(parent)?.sync_all()?;
    Ok(())
}

fn trees_equal(left: &Path, right: &Path) -> Result<bool, FrontendDeployError> {
    let left_entries = fs::read_dir(left)?.collect::<Result<Vec<_>, _>>()?;
    let right_entries = fs::read_dir(right)?.collect::<Result<Vec<_>, _>>()?;
    if left_entries.len() != right_entries.len() {
        return Ok(false);
    }
    for entry in left_entries {
        let right_path = right.join(entry.file_name());
        let left_metadata = fs::symlink_metadata(entry.path())?;
        let right_metadata = match fs::symlink_metadata(&right_path) {
            Ok(metadata) => metadata,
            Err(error) if error.kind() == io::ErrorKind::NotFound => return Ok(false),
            Err(error) => return Err(error.into()),
        };
        if left_metadata.file_type().is_symlink()
            || right_metadata.file_type().is_symlink()
            || left_metadata.is_dir() != right_metadata.is_dir()
            || left_metadata.is_file() != right_metadata.is_file()
        {
            return Ok(false);
        }
        if left_metadata.is_dir() {
            if !trees_equal(&entry.path(), &right_path)? {
                return Ok(false);
            }
        } else {
            let mut left_bytes = Vec::new();
            let mut right_bytes = Vec::new();
            use std::io::Read as _;
            File::open(entry.path())?.read_to_end(&mut left_bytes)?;
            File::open(right_path)?.read_to_end(&mut right_bytes)?;
            if left_bytes != right_bytes {
                return Ok(false);
            }
        }
    }
    Ok(true)
}

fn validate_tree(root: &Path) -> Result<(), FrontendDeployError> {
    let metadata = fs::symlink_metadata(root)?;
    if metadata.file_type().is_symlink() || !metadata.is_dir() {
        return Err(FrontendDeployError::InvalidTree(root.display().to_string()));
    }
    visit(root, root)?;
    let index = root.join("index.html");
    let metadata = fs::symlink_metadata(&index)?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(FrontendDeployError::InvalidTree(
            "index.html is missing or unsafe".to_owned(),
        ));
    }
    Ok(())
}

fn visit(root: &Path, directory: &Path) -> Result<(), FrontendDeployError> {
    for entry in fs::read_dir(directory)? {
        let entry = entry?;
        let path = entry.path();
        let metadata = fs::symlink_metadata(&path)?;
        if metadata.file_type().is_symlink()
            || (!metadata.is_dir() && !metadata.is_file())
            || !path.starts_with(root)
        {
            return Err(FrontendDeployError::InvalidTree(path.display().to_string()));
        }
        if metadata.is_dir() {
            visit(root, &path)?;
        }
    }
    Ok(())
}

fn copy_tree(source: &Path, destination: &Path) -> Result<(), FrontendDeployError> {
    for entry in fs::read_dir(source)? {
        let entry = entry?;
        let from = entry.path();
        let to = destination.join(entry.file_name());
        let metadata = fs::symlink_metadata(&from)?;
        if metadata.file_type().is_symlink() {
            return Err(FrontendDeployError::InvalidTree(from.display().to_string()));
        }
        if metadata.is_dir() {
            fs::create_dir(&to)?;
            copy_tree(&from, &to)?;
        } else if metadata.is_file() {
            let mut input = File::open(&from)?;
            let mut output = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(&to)?;
            io::copy(&mut input, &mut output)?;
            output.sync_all()?;
        } else {
            return Err(FrontendDeployError::InvalidTree(from.display().to_string()));
        }
    }
    Ok(())
}

fn normalize_tree(root: &Path) -> Result<(), FrontendDeployError> {
    visit_mut(root)?;
    File::open(root)?.sync_all()?;
    Ok(())
}

fn visit_mut(path: &Path) -> Result<(), FrontendDeployError> {
    let metadata = fs::symlink_metadata(path)?;
    if metadata.is_dir() {
        for entry in fs::read_dir(path)? {
            visit_mut(&entry?.path())?;
        }
        fs::set_permissions(path, fs::Permissions::from_mode(0o755))?;
    } else if metadata.is_file() {
        fs::set_permissions(path, fs::Permissions::from_mode(0o444))?;
    } else {
        return Err(FrontendDeployError::InvalidTree(path.display().to_string()));
    }
    Ok(())
}

fn switch_current(root: &Path, release: &str) -> Result<(), FrontendDeployError> {
    let temporary = root.join(format!(".current.{}", std::process::id()));
    let _ = fs::remove_file(&temporary);
    std::os::unix::fs::symlink(Path::new("releases").join(release), &temporary)?;
    if let Err(error) = fs::rename(&temporary, root.join("current")) {
        let _ = fs::remove_file(&temporary);
        return Err(error.into());
    }
    File::open(root)?.sync_all()?;
    Ok(())
}

fn releases(
    root: &Path,
    exclude: Option<&str>,
) -> Result<Vec<(String, SystemTime)>, FrontendDeployError> {
    let mut values = Vec::new();
    for entry in fs::read_dir(root.join("releases"))? {
        let entry = entry?;
        let name = entry.file_name().to_string_lossy().into_owned();
        let metadata = fs::symlink_metadata(entry.path())?;
        if metadata.is_dir()
            && !metadata.file_type().is_symlink()
            && exclude != Some(name.as_str())
            && validate_release(&name).is_ok()
        {
            values.push((name, metadata.modified()?));
        }
    }
    values.sort_by(|left, right| right.1.cmp(&left.1).then_with(|| right.0.cmp(&left.0)));
    Ok(values)
}

fn prune(root: &Path, keep: usize) -> Result<(), FrontendDeployError> {
    let current = current(root)?;
    let mut retained = 1usize;
    for (release, _) in releases(root, Some(&current))? {
        if retained < keep {
            retained += 1;
        } else {
            fs::remove_dir_all(root.join("releases").join(release))?;
        }
    }
    File::open(root.join("releases"))?.sync_all()?;
    Ok(())
}

fn validate_release(value: &str) -> Result<(), FrontendDeployError> {
    if value.is_empty()
        || value.len() > 128
        || !value.as_bytes()[0].is_ascii_alphanumeric()
        || !value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || b"._+-".contains(&byte))
    {
        return Err(FrontendDeployError::UnsafePath(value.to_owned()));
    }
    Ok(())
}

fn require_absolute_non_root(path: &Path) -> Result<(), FrontendDeployError> {
    if !path.is_absolute()
        || path == Path::new("/")
        || path
            .components()
            .any(|part| matches!(part, Component::ParentDir))
    {
        return Err(FrontendDeployError::UnsafePath(path.display().to_string()));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn release_ids_are_bounded() {
        assert!(validate_release("2026.08.29-1").is_ok());
        assert!(validate_release("../release").is_err());
    }

    #[test]
    fn package_release_id_is_deterministic_and_colon_safe() -> Result<(), FrontendDeployError> {
        let revision = "0123456789abcdef0123456789abcdef01234567";
        assert_eq!(
            package_release_id("2:1.2.3-1", revision)?,
            "2-1.2.3-1.g0123456789ab",
        );
        Ok(())
    }
}
