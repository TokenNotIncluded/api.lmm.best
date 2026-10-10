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
