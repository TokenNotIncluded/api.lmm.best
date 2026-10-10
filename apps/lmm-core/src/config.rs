use std::{env, fs, io, path::PathBuf};

pub fn database_url() -> io::Result<Option<String>> {
    let inline = env::var_os("LMM_CORE_DATABASE_URL")
        .map(|value| {
            value.into_string().map_err(|_| {
                io::Error::new(
                    io::ErrorKind::InvalidInput,
                    "database URL is not valid UTF-8",
                )
            })
        })
        .transpose()?;
    let file = env::var_os("LMM_CORE_DATABASE_URL_FILE").map(PathBuf::from);
    read_database_url(inline, file)
}
/// Resource limits for the independently deployed Rust process.
fn parse_limit(name: &str, value: Option<&str>, default: u32, maximum: u32) -> io::Result<u32> {
    let number = match value {
        Some(raw) => raw
            .parse::<u32>()
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, format!("invalid {name}")))?,
        None => default,
    };
    if number == 0 || number > maximum {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            format!("invalid {name}: expected 1..={maximum}"),
        ));
    }
    Ok(number)
}

fn configured_limit(name: &str, default: u32, maximum: u32) -> io::Result<u32> {
    match env::var(name) {
        Ok(value) => parse_limit(name, Some(&value), default, maximum),
        Err(env::VarError::NotPresent) => parse_limit(name, None, default, maximum),
        Err(env::VarError::NotUnicode(_)) => {
            Err(io::Error::new(io::ErrorKind::InvalidInput, format!("invalid {name}")))
        }
    }
}

pub fn worker_threads() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_WORKER_THREADS", 1, 4)? as usize)
}

pub fn database_connections() -> io::Result<u32> {
    configured_limit("LMM_CORE_DB_CONNECTIONS", 2, 8)
}

pub fn password_slots() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_PASSWORD_SLOTS", 1, 2)? as usize)
}

pub fn max_inflight() -> io::Result<usize> {
    Ok(configured_limit("LMM_CORE_MAX_INFLIGHT", 16, 64)? as usize)
}

fn read_database_url(inline: Option<String>, file: Option<PathBuf>) -> io::Result<Option<String>> {
    let value = match (inline, file) {
        (Some(_), Some(_)) => {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "configure exactly one database URL source",
            ));
        }
        (Some(value), None) => value,
        (None, Some(path)) => fs::read_to_string(path)?,
        (None, None) => return Ok(None),
    };
    let value = value.trim().to_owned();
    if value.is_empty() {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "database URL is empty",
        ));
    }
    Ok(Some(value))
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn resource_limits_reject_invalid_or_unbounded_settings() {
        assert_eq!(parse_limit("TEST_LIMIT", None, 2, 8).unwrap(), 2);
        assert_eq!(parse_limit("TEST_LIMIT", Some("1"), 2, 8).unwrap(), 1);
        for invalid in ["", "0", "9", "-1", "1.5", "not-a-number"] {
            assert!(parse_limit("TEST_LIMIT", Some(invalid), 2, 8).is_err());
        }
    }

    #[test]
    fn database_configuration_is_explicit_and_fails_closed() {
        assert_eq!(read_database_url(None, None).unwrap(), None);
        assert!(read_database_url(Some(" ".into()), None).is_err());
        assert!(read_database_url(Some("postgres://test".into()), Some("unused".into())).is_err());
        assert_eq!(
            read_database_url(Some("postgres://test\n".into()), None)
                .unwrap()
                .as_deref(),
            Some("postgres://test")
        );
    }
}
