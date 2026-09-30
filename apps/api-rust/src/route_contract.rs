//! Native API-route contract revision tool.

use std::{
    fs::{self, File, OpenOptions},
    io::{self, Write},
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
};

use sha2::{Digest, Sha256};
use thiserror::Error;

#[derive(Debug, Error)]
pub enum RouteContractError {
    #[error("route contract is missing or unsafe")]
    UnsafeContract,
    #[error(
        "route contract must contain exactly one stable semantic version followed by one newline"
    )]
    InvalidVersion,
    #[error("revision output must be a non-symlink file path")]
    UnsafeOutput,
    #[error("revision file is missing or unsafe")]
    UnsafeRevision,
    #[error("revision file must contain exactly one lowercase SHA-256 line")]
    InvalidRevision,
    #[error("revision does not match the API route contract")]
    RevisionMismatch,
    #[error("route contract filesystem operation failed: {0}")]
    Io(#[from] io::Error),
}

#[must_use]
pub fn default_contract_path() -> PathBuf {
    let repository = PathBuf::from("contracts/api-route/VERSION");
    if repository.exists() {
        return repository;
    }
    PathBuf::from("/usr/share/lmm-api/contracts/api-route/VERSION")
}

pub fn revision(path: &Path) -> Result<String, RouteContractError> {
    let metadata = fs::symlink_metadata(path).map_err(|_| RouteContractError::UnsafeContract)?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(RouteContractError::UnsafeContract);
    }
    let bytes = fs::read(path)?;
    if !valid_contract(&bytes) {
        return Err(RouteContractError::InvalidVersion);
    }
    Ok(format!("{:x}", Sha256::digest(&bytes)))
}

pub fn generate(contract: &Path, output: &Path) -> Result<String, RouteContractError> {
    if output.as_os_str().is_empty() || output.is_dir() {
        return Err(RouteContractError::UnsafeOutput);
    }
    match fs::symlink_metadata(output) {
        Ok(metadata) if metadata.file_type().is_symlink() => {
            return Err(RouteContractError::UnsafeOutput);
        }
        Ok(_) => {}
        Err(error) if error.kind() == io::ErrorKind::NotFound => {}
        Err(error) => return Err(error.into()),
    }
    let digest = revision(contract)?;
    let parent = output.parent().unwrap_or_else(|| Path::new("."));
    fs::create_dir_all(parent)?;
    let temporary = parent.join(format!(
        ".{}.{}.new",
        output.file_name().unwrap_or_default().to_string_lossy(),
        std::process::id()
    ));
    let _ = fs::remove_file(&temporary);
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o644)
        .open(&temporary)?;
    file.write_all(digest.as_bytes())?;
    file.write_all(b"\n")?;
    file.sync_all()?;
    fs::set_permissions(&temporary, fs::Permissions::from_mode(0o644))?;
    if let Err(error) = fs::rename(&temporary, output) {
        let _ = fs::remove_file(&temporary);
        return Err(error.into());
    }
    File::open(parent)?.sync_all()?;
    Ok(digest)
}

pub fn verify(contract: &Path, revision_file: &Path) -> Result<String, RouteContractError> {
    let metadata =
        fs::symlink_metadata(revision_file).map_err(|_| RouteContractError::UnsafeRevision)?;
    if metadata.file_type().is_symlink() || !metadata.is_file() {
        return Err(RouteContractError::UnsafeRevision);
    }
    let bytes = fs::read(revision_file)?;
    if bytes.len() != 65
        || bytes[64] != b'\n'
        || !bytes[..64]
            .iter()
            .all(|byte| byte.is_ascii_digit() || (b'a'..=b'f').contains(byte))
    {
        return Err(RouteContractError::InvalidRevision);
    }
    let expected =
        std::str::from_utf8(&bytes[..64]).map_err(|_| RouteContractError::InvalidRevision)?;
    let actual = revision(contract)?;
    if expected != actual {
        return Err(RouteContractError::RevisionMismatch);
    }
    Ok(actual)
}

fn valid_contract(bytes: &[u8]) -> bool {
    if bytes.len() < 6 || bytes.last() != Some(&b'\n') || bytes[..bytes.len() - 1].contains(&b'\n')
    {
        return false;
    }
    let Ok(value) = std::str::from_utf8(&bytes[..bytes.len() - 1]) else {
        return false;
    };
    let mut parts = value.split('.');
    let valid = |part: &str| {
        !part.is_empty()
            && part.bytes().all(|byte| byte.is_ascii_digit())
            && (part == "0" || !part.starts_with('0'))
    };
    valid(parts.next().unwrap_or_default())
        && valid(parts.next().unwrap_or_default())
        && valid(parts.next().unwrap_or_default())
        && parts.next().is_none()
}

#[cfg(test)]
mod tests {
    use super::*;
    use std::os::unix::fs::symlink;

    type TestResult = Result<(), Box<dyn std::error::Error>>;

    struct TestDirectory(PathBuf);

    impl TestDirectory {
        fn new() -> io::Result<Self> {
            let path = std::env::temp_dir()
                .join(format!("lmm-route-contract-test-{}", uuid::Uuid::new_v4()));
            fs::create_dir(&path)?;
            Ok(Self(path))
        }

        fn path(&self) -> &Path {
            &self.0
        }
    }

    impl Drop for TestDirectory {
        fn drop(&mut self) {
            let _ = fs::remove_dir_all(&self.0);
        }
    }

    #[test]
    fn stable_semver_requires_one_final_newline() {
        for valid in [b"0.0.0\n".as_slice(), b"1.20.300\n".as_slice()] {
            assert!(valid_contract(valid));
        }
        for invalid in [
            b"".as_slice(),
            b"1.0.0".as_slice(),
            b"1.0.0\n\n".as_slice(),
            b"1.0\n".as_slice(),
            b"1.0.0.1\n".as_slice(),
            b"1..0\n".as_slice(),
            b"1.a.0\n".as_slice(),
            b"01.0.0\n".as_slice(),
            b"1.00.0\n".as_slice(),
            b"1.0.00\n".as_slice(),
            b"1.0.0-rc.1\n".as_slice(),
            b"\xff.0.0\n".as_slice(),
        ] {
            assert!(!valid_contract(invalid));
        }
    }

    #[test]
    fn shared_revision_fixture_matches_repository_contract()
    -> Result<(), Box<dyn std::error::Error>> {
        let root = Path::new(env!("CARGO_MANIFEST_DIR")).join("../..");
        let fixture: serde_json::Value = serde_json::from_slice(&fs::read(
            root.join("contracts/api-route/revision-fixtures.json"),
        )?)?;
        let expected = fixture["cases"][0]["revision"]
            .as_str()
            .ok_or("fixture revision is missing")?;
        assert_eq!(
            revision(&root.join("contracts/api-route/VERSION"))?,
            expected
        );
        Ok(())
    }

    #[test]
    fn generated_revision_round_trips_and_detects_contract_changes() -> TestResult {
        let directory = TestDirectory::new()?;
        let contract = directory.path().join("VERSION");
        let output = directory.path().join("nested/route-contract.sha256");
        fs::write(&contract, b"2.3.4\n")?;

        let digest = generate(&contract, &output)?;
        assert_eq!(fs::read_to_string(&output)?, format!("{digest}\n"));
        assert_eq!(verify(&contract, &output)?, digest);
        assert_eq!(fs::metadata(&output)?.permissions().mode() & 0o777, 0o644);

        fs::write(&contract, b"2.3.5\n")?;
        assert!(matches!(
            verify(&contract, &output),
            Err(RouteContractError::RevisionMismatch)
        ));
        let regenerated = generate(&contract, &output)?;
        assert_ne!(regenerated, digest);
        assert_eq!(verify(&contract, &output)?, regenerated);

        fs::write(&contract, b"2.3\n")?;
        assert!(matches!(
            revision(&contract),
            Err(RouteContractError::InvalidVersion)
        ));
        Ok(())
    }

    #[test]
    fn contract_and_revision_symlinks_are_rejected() -> TestResult {
        let directory = TestDirectory::new()?;
        let contract = directory.path().join("VERSION");
        let contract_link = directory.path().join("VERSION.link");
        let revision_file = directory.path().join("route-contract.sha256");
        let revision_link = directory.path().join("route-contract.link");
        fs::write(&contract, b"1.0.0\n")?;
        generate(&contract, &revision_file)?;
        symlink(&contract, &contract_link)?;
        symlink(&revision_file, &revision_link)?;

        assert!(matches!(
            revision(&contract_link),
            Err(RouteContractError::UnsafeContract)
        ));
        assert!(matches!(
            verify(&contract, &revision_link),
            Err(RouteContractError::UnsafeRevision)
        ));
        assert!(matches!(
            generate(&contract, &revision_link),
            Err(RouteContractError::UnsafeOutput)
        ));
        assert!(matches!(
            generate(&contract, directory.path()),
            Err(RouteContractError::UnsafeOutput)
        ));
        assert!(matches!(
            verify(&contract, &directory.path().join("missing.sha256")),
            Err(RouteContractError::UnsafeRevision)
        ));
        Ok(())
    }

    #[test]
    fn malformed_revision_is_rejected_before_comparison() -> TestResult {
        let directory = TestDirectory::new()?;
        let contract = directory.path().join("VERSION");
        let revision_file = directory.path().join("route-contract.sha256");
        fs::write(&contract, b"1.0.0\n")?;

        for malformed in [
            "a".repeat(64),
            format!("{}\n", "A".repeat(64)),
            format!("{}\n\n", "a".repeat(64)),
        ] {
            fs::write(&revision_file, malformed)?;
            assert!(matches!(
                verify(&contract, &revision_file),
                Err(RouteContractError::InvalidRevision)
            ));
        }
        Ok(())
    }
}
