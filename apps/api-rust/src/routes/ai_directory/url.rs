//! The directory stores Go net/url's spelling, not a browser URL canonicalization.
//! In particular host case, explicit ports, absent trailing slash, escaped paths,
//! and Unicode query strings must survive request-id replay comparisons.

use super::AdError;

fn unreserved(byte: u8) -> bool {
    byte.is_ascii_alphanumeric() || b"-_.~".contains(&byte)
}
fn host_byte(byte: u8) -> bool {
    unreserved(byte) || b"!$&'()*+,;=:[]<>\"".contains(&byte)
}
fn unescape(raw: &str, host: bool) -> Result<Vec<u8>, AdError> {
    let bytes = raw.as_bytes();
    let mut decoded = Vec::with_capacity(bytes.len());
    let mut index = 0;
    while index < bytes.len() {
        if bytes[index] == b'%' {
            let Some(pair) = bytes.get(index + 1..index + 3) else {
                return Err(AdError::InvalidInput);
            };
            let high = (pair[0] as char)
                .to_digit(16)
                .ok_or(AdError::InvalidInput)?;
            let low = (pair[1] as char)
                .to_digit(16)
                .ok_or(AdError::InvalidInput)?;
            let byte = (high * 16 + low) as u8;
            if host && byte < 128 && byte != b'%' {
                return Err(AdError::InvalidInput);
            }
            decoded.push(byte);
            index += 3;
        } else {
            if host && bytes[index] < 128 && !host_byte(bytes[index]) {
                return Err(AdError::InvalidInput);
            }
            decoded.push(bytes[index]);
            index += 1;
        }
    }
    Ok(decoded)
}
fn escape(bytes: &[u8], allowed: impl Fn(u8) -> bool) -> String {
    const HEX: &[u8; 16] = b"0123456789ABCDEF";
    let mut encoded = String::new();
    for &byte in bytes {
        if allowed(byte) {
            encoded.push(byte as char)
        } else {
            encoded.push('%');
            encoded.push(HEX[(byte >> 4) as usize] as char);
            encoded.push(HEX[(byte & 15) as usize] as char);
        }
    }
    encoded
}
fn component(raw: &str, fragment: bool) -> Result<String, AdError> {
    let decoded = unescape(raw, false)?;
    let raw_allowed = |byte| {
        unreserved(byte) || b"!$&'()*+,;=:@[]%/".contains(&byte) || (fragment && byte == b'?')
    };
    if raw.bytes().all(raw_allowed) {
        return Ok(raw.to_owned());
    }
    Ok(escape(&decoded, |byte| {
        unreserved(byte) || b"$&+,/:;=@".contains(&byte) || (fragment && b"?!()*".contains(&byte))
    }))
}

pub(super) fn parse(raw: &str, https_only: bool) -> Result<(String, String), AdError> {
    if raw.is_empty() || raw.len() > 2048 || raw.bytes().any(|byte| byte <= b' ' || byte == 127) {
        return Err(AdError::InvalidInput);
    }
    let (scheme, rest) = raw.split_once("://").ok_or(AdError::InvalidInput)?;
    let scheme = scheme.to_ascii_lowercase();
    if scheme != "https" && (https_only || scheme != "http") {
        return Err(AdError::InvalidInput);
    }
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let authority = &rest[..end];
    if authority.is_empty() || authority.contains('@') {
        return Err(AdError::InvalidInput);
    }
    let authority =
        String::from_utf8(unescape(authority, true)?).map_err(|_| AdError::InvalidInput)?;
    let host = if authority.starts_with('[') {
        let close = authority.find(']').ok_or(AdError::InvalidInput)?;
        let port = &authority[close + 1..];
        if !port.is_empty()
            && (!port.starts_with(':') || !port[1..].bytes().all(|byte| byte.is_ascii_digit()))
        {
            return Err(AdError::InvalidInput);
        }
        &authority[1..close]
    } else if let Some((host, port)) = authority.rsplit_once(':') {
        if !port.bytes().all(|byte| byte.is_ascii_digit()) {
            return Err(AdError::InvalidInput);
        }
        host
    } else {
        authority.as_str()
    };
    if host.is_empty() {
        return Err(AdError::InvalidInput);
    }
    let (before_fragment, fragment) = rest[end..]
        .split_once('#')
        .map_or((&rest[end..], None), |(left, right)| (left, Some(right)));
    let (path, query) = before_fragment
        .split_once('?')
        .map_or((before_fragment, None), |(left, right)| (left, Some(right)));
    let mut canonical = format!(
        "{scheme}://{}{}",
        escape(authority.as_bytes(), host_byte),
        component(path, false)?
    );
    if let Some(query) = query {
        canonical.push('?');
        canonical.push_str(query);
    }
    if let Some(fragment) = fragment.filter(|value| !value.is_empty()) {
        canonical.push('#');
        canonical.push_str(&component(fragment, true)?);
    }
    Ok((host.to_owned(), canonical))
}
