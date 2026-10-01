//! Dashboard authentication wire contract shared with the Go backend.

/// Canonical marker, included without normalization so both backends emit identical bytes.
pub const AUTH_VERSION: &str = include_str!("../../api-go/common/auth_version.txt");

#[cfg(test)]
mod tests {
    use super::AUTH_VERSION;

    #[test]
    fn marker_is_valid_header_value() {
        let value = axum::http::HeaderValue::from_str(AUTH_VERSION).unwrap();
        assert_eq!(value.to_str().unwrap(), AUTH_VERSION);
        assert!(!value.as_bytes().is_empty());
        assert_eq!(AUTH_VERSION.trim(), AUTH_VERSION);
    }
}
