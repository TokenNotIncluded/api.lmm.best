use serde::{
    Deserialize, Serialize,
    de::{IgnoredAny, MapAccess, Visitor},
};
use serde_json::{Map, Value};
use sha2::{Digest, Sha256};
use std::{fmt, net::IpAddr, sync::OnceLock};

pub const INVALID: &str = "invalid acquisition data";
pub const RAW_DAYS: i64 = 90;
pub const ACCOUNT_DAYS: i64 = 365;

/// Go's struct decoder preserves the earlier value when a repeated field is
/// null, accepts ASCII case variants, and rejects wrong types for known fields.
#[derive(Clone, Debug, Default)]
pub struct Input(pub Map<String, Value>);
impl Input {
    pub fn text(&self, key: &str) -> &str {
        self.0.get(key).and_then(Value::as_str).unwrap_or("")
    }
    pub fn int(&self, key: &str) -> i64 {
        self.0.get(key).and_then(Value::as_i64).unwrap_or(0)
    }
    pub fn bool(&self, key: &str) -> bool {
        self.0.get(key).and_then(Value::as_bool).unwrap_or(false)
    }
}
impl<'de> Deserialize<'de> for Input {
    fn deserialize<D: serde::Deserializer<'de>>(d: D) -> Result<Self, D::Error> {
        struct V;
        impl<'de> Visitor<'de> for V {
            type Value = Input;
            fn expecting(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
                f.write_str("acquisition object")
            }
            fn visit_unit<E: serde::de::Error>(self) -> Result<Input, E> {
                Ok(Input::default())
            }
            fn visit_map<M: MapAccess<'de>>(self, mut map: M) -> Result<Input, M::Error> {
                let mut result = Input::default();
                while let Some(key) = map.next_key::<String>()? {
                    let key = key
                        .chars()
                        .map(|c| match c {
                            'ſ' => 's',
                            'K' => 'k',
                            _ => c.to_ascii_lowercase(),
                        })
                        .collect::<String>();
                    let value = match key.as_str() {
                        "consent_version" | "created_at" | "deleted_at" | "days" => {
                            map.next_value::<Option<i64>>()?.map(Value::from)
                        }
                        "consent" | "test" | "archived" => {
                            map.next_value::<Option<bool>>()?.map(Value::from)
                        }
                        "id" | "name" | "source" | "medium" | "campaign" | "content" | "target"
                        | "landing" | "referrer" | "link_id" | "nonce" | "detail" => {
                            map.next_value::<Option<String>>()?.map(Value::from)
                        }
                        _ => {
                            let _ = map.next_value::<IgnoredAny>()?;
                            None
                        }
                    };
                    if let Some(value) = value {
                        result.0.insert(key, value);
                    }
                }
                Ok(result)
            }
        }
        d.deserialize_any(V)
    }
}

#[derive(Clone, Debug, Default, Serialize, Deserialize)]
#[serde(default)]
pub struct Link {
    pub id: String,
    pub name: String,
    pub source: String,
    pub medium: String,
    pub campaign: String,
    pub content: String,
    pub target: String,
    pub archived: bool,
    pub created_at: i64,
    #[serde(default, skip_serializing_if = "is_zero")]
    pub deleted_at: i64,
}
fn is_zero(value: &i64) -> bool {
    *value == 0
}
#[derive(Clone, Debug, Default, Serialize, Deserialize, PartialEq)]
#[serde(default)]
pub struct Visit {
    pub consent_version: i64,
    pub id: i64,
    #[serde(skip)]
    pub visitor_id: String,
    #[serde(skip)]
    pub nonce: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub link_id: String,
    pub source: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub medium: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub campaign: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub content: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub referrer_host: String,
    pub landing: String,
    pub evidence: String,
    pub created_at: i64,
}

pub fn label(raw: &str) -> String {
    static LABEL: OnceLock<regex::Regex> = OnceLock::new();
    let raw = raw.trim();
    let lower = raw.to_lowercase();
    let allowed = LABEL.get_or_init(|| regex::Regex::new(r"^[\pL\pN _.-]{1,80}$").unwrap());
    if !allowed.is_match(raw)
        || lower.contains("sk-")
        || lower.contains("bearer")
        || raw.matches('.').count() >= 2
        || (raw.len() >= 41 && raw.bytes().all(|b| b.is_ascii_alphanumeric()))
    {
        String::new()
    } else {
        raw.into()
    }
}
pub fn target(raw: &str) -> &str {
    if matches!(
        raw,
        "/" | "/guide" | "/pricing" | "/challenges" | "/sign-up" | "/sign-in"
    ) {
        raw
    } else {
        ""
    }
}
pub fn valid_id(raw: &str) -> bool {
    raw.len() == 32 && raw.bytes().all(|b| b.is_ascii_hexdigit())
}
pub fn random_id() -> String {
    hex::encode(rand::random::<[u8; 16]>())
}
pub fn visitor_hash(raw: &str) -> String {
    hex::encode(Sha256::digest(raw.as_bytes()))
}
pub fn unescape(raw: &str, plus: bool) -> Result<String, &'static str> {
    let bytes = raw.as_bytes();
    let mut decoded = Vec::new();
    let mut i = 0;
    while i < bytes.len() {
        if bytes[i] == b'%' {
            if i + 2 >= bytes.len() {
                return Err(INVALID);
            }
            let a = (bytes[i + 1] as char).to_digit(16).ok_or(INVALID)?;
            let b = (bytes[i + 2] as char).to_digit(16).ok_or(INVALID)?;
            decoded.push((a * 16 + b) as u8);
            i += 3;
        } else {
            decoded.push(if plus && bytes[i] == b'+' {
                b' '
            } else {
                bytes[i]
            });
            i += 1;
        }
    }
    String::from_utf8(decoded).map_err(|_| INVALID)
}
fn referrer_host(raw: &str) -> String {
    if raw.len() > 2048 || raw.bytes().any(|b| b < 32 || b == 127) {
        return String::new();
    }
    let Some((scheme, rest)) = raw.split_once("://") else {
        return String::new();
    };
    if !scheme.eq_ignore_ascii_case("https") && !scheme.eq_ignore_ascii_case("http") {
        return String::new();
    }
    let end = rest.find(['/', '?', '#']).unwrap_or(rest.len());
    let authority = &rest[..end];
    if authority.contains('@') {
        return String::new();
    }
    let host = if authority.starts_with('[') {
        let Some(end) = authority.find(']') else {
            return String::new();
        };
        &authority[1..end]
    } else if let Some((host, port)) = authority.rsplit_once(':') {
        if !port.bytes().all(|b| b.is_ascii_digit()) {
            return String::new();
        }
        host
    } else {
        authority
    };
    if host.bytes().any(|b| b == b'%' || b == b'\\') {
        return String::new();
    }
    let rest = &rest[end..];
    let (path_query, fragment) = rest.split_once('#').unwrap_or((rest, ""));
    if unescape(path_query.split('?').next().unwrap_or(""), false).is_err()
        || unescape(fragment, false).is_err()
    {
        return String::new();
    }
    let host = host.to_lowercase();
    if host.len() > 253
        || host.contains([' ', '@', ':', '%'])
        || host.parse::<IpAddr>().is_ok()
        || !host.contains('.')
        || host.ends_with(".local")
    {
        String::new()
    } else {
        host
    }
}
pub fn normalize(input: &Input, own_hosts: &[&str], now: i64) -> Result<Visit, &'static str> {
    if !input.bool("consent") || input.bool("test") || !valid_id(input.text("nonce")) {
        return Err(INVALID);
    }
    let landing = input.text("landing");
    if landing.bytes().any(|b| b < 32 || b == 127) || landing.starts_with("//") {
        return Err(INVALID);
    }
    let (without_fragment, fragment) = landing.split_once('#').unwrap_or((landing, ""));
    unescape(fragment, false)?;
    let (path, query) = without_fragment
        .split_once('?')
        .unwrap_or((without_fragment, ""));
    let path = unescape(path, false)?;
    let page = target(&path);
    if page.is_empty() {
        return Err(INVALID);
    }
    for part in query.split('&') {
        if part.contains(';') {
            continue;
        }
        let (key, value) = part.split_once('=').unwrap_or((part, ""));
        if let (Ok(key), Ok(_)) = (unescape(key, true), unescape(value, true))
            && matches!(
                key.as_str(),
                "code" | "state" | "session_id" | "payment_intent" | "trade_no" | "redirect_status"
            )
        {
            return Err(INVALID);
        }
    }
    let mut host = referrer_host(input.text("referrer"));
    for own in own_hosts {
        if host.eq_ignore_ascii_case(own) || host.ends_with(&format!(".{}", own.to_lowercase())) {
            host.clear();
        }
    }
    let version = if input.int("consent_version") == 0 {
        1
    } else {
        input.int("consent_version")
    };
    if !(1..=2).contains(&version) {
        return Err(INVALID);
    }
    let mut result = Visit {
        consent_version: version,
        nonce: input.text("nonce").into(),
        landing: page.into(),
        referrer_host: host.clone(),
        created_at: now,
        source: "unknown".into(),
        evidence: "unavailable".into(),
        ..Default::default()
    };
    let source = label(input.text("source"));
    if !source.is_empty() {
        result.source = source;
        result.evidence = "campaign_parameters".into();
        result.medium = label(input.text("medium"));
        result.campaign = label(input.text("campaign"));
        result.content = label(input.text("content"));
    } else if !host.is_empty() {
        result.source = host;
        result.evidence = "browser_referrer".into();
    }
    Ok(result)
}
pub fn self_report(source: &str, detail: &str) -> Result<String, &'static str> {
    static SECRET: OnceLock<regex::Regex> = OnceLock::new();
    let pattern=SECRET.get_or_init(||regex::Regex::new(r"(?i)(https?://|sk-[a-z0-9_-]{8,}|bearer\s+|(?:api[_-]?key|password|token|secret)\s*[:=])").unwrap());
    let detail = detail.trim();
    if !matches!(
        source,
        "search" | "community" | "social" | "documentation" | "friend" | "client" | "ai" | "other"
    ) || detail.chars().count() > 160
        || detail.contains(['\r', '\n', '\0'])
        || pattern.is_match(detail)
    {
        return Err(INVALID);
    }
    Ok(detail.into())
}

#[cfg(test)]
mod tests {
    use super::{Input, normalize, self_report};
    use serde_json::json;

    #[test]
    fn visit_keeps_only_safe_attribution_fields() {
        let input: Input = serde_json::from_value(json!({
            "consent": true,
            "consent_version": 2,
            "nonce": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "landing": "/guide?api_key=private#fragment",
            "referrer": "https://api.lmm.best/guide?token=private",
            "source": " community ",
            "campaign": "readme"
        }))
        .unwrap();
        let visit = normalize(&input, &["api.lmm.best", "lmm.best"], 123).unwrap();
        assert_eq!(visit.landing, "/guide");
        assert_eq!(visit.referrer_host, "");
        assert_eq!(visit.source, "community");
        assert_eq!(visit.campaign, "readme");
        assert_eq!(visit.evidence, "campaign_parameters");
        assert_eq!(visit.created_at, 123);
    }

    #[test]
    fn visit_and_self_report_reject_private_inputs() {
        let base = json!({
            "consent": true,
            "nonce": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "landing": "/guide"
        });
        for landing in ["/guide?code=private", "//example.com/guide"] {
            let mut value = base.clone();
            value["landing"] = json!(landing);
            let input: Input = serde_json::from_value(value).unwrap();
            assert!(normalize(&input, &[], 123).is_err());
        }
        let mut value = base;
        value["consent"] = json!(false);
        let input: Input = serde_json::from_value(value).unwrap();
        assert!(normalize(&input, &[], 123).is_err());
        assert!(self_report("community", "token=private").is_err());
    }
}
