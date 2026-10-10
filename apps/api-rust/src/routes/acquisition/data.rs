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
                        "consent_version" | "created_at" | "deleted_at" | "days"
                        | "expected_revision" => map.next_value::<Option<i64>>()?.map(Value::from),
                        "consent" | "test" | "archived" => {
                            map.next_value::<Option<bool>>()?.map(Value::from)
                        }
                        "id" | "name" | "source" | "medium" | "campaign" | "content" | "target"
                        | "landing" | "referrer" | "link_id" | "nonce" | "detail" | "reason" => {
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
    let detail = detail.trim();
    if !matches!(
        source,
        "search" | "community" | "social" | "documentation" | "friend" | "client" | "ai" | "other"
    ) || detail.chars().count() > 160
        || detail.contains(['\r', '\n', '\0'])
        || contains_secret(detail)
    {
        return Err(INVALID);
    }
    Ok(detail.into())
}

fn contains_secret(value: &str) -> bool {
    static SECRET: OnceLock<regex::Regex> = OnceLock::new();
    SECRET
        .get_or_init(|| {
            regex::Regex::new(
                r"(?i)(https?://|sk-[a-z0-9_-]{8,}|bearer\s+|(?:api[_-]?key|password|token|secret)\s*[:=])",
            )
            .unwrap()
        })
        .is_match(value)
}

pub fn correction_reason(reason: &str) -> Result<String, &'static str> {
    let reason = reason.trim();
    if !(3..=300).contains(&reason.chars().count())
        || reason.contains(['\r', '\n', '\0', '@'])
        || contains_secret(reason)
    {
        return Err(INVALID);
    }
    Ok(reason.into())
}

#[cfg(test)]
mod tests {
    use super::{
        Input, correction_reason, label, normalize, self_report, unescape, valid_id, visitor_hash,
    };
    use serde_json::json;

    #[test]
    fn input_decoder_preserves_go_field_matching_null_and_type_rules() {
        let input: Input = serde_json::from_str(
            r#"{
                "SOURCE":"first",
                "source":null,
                "linK_id":"campaign-link",
                "Expected_Revision":7,
                "unknown":{"wrong":["types",1,true]}
            }"#,
        )
        .unwrap();
        assert_eq!(input.text("source"), "first");
        assert_eq!(input.text("link_id"), "campaign-link");
        assert_eq!(input.int("expected_revision"), 7);
        assert_eq!(input.text("unknown"), "");
        assert!(!input.bool("missing"));

        let folded: Input = serde_json::from_str(r#"{"ſOURCE":"community"}"#).unwrap();
        assert_eq!(folded.text("source"), "community");

        for malformed in [
            r#"{"source":7}"#,
            r#"{"consent":"true"}"#,
            r#"{"days":false}"#,
        ] {
            assert!(
                serde_json::from_str::<Input>(malformed).is_err(),
                "known fields must reject the wrong JSON type: {malformed}"
            );
        }
        assert!(serde_json::from_str::<Input>("null").unwrap().0.is_empty());
    }

    #[test]
    fn attribution_helpers_reject_credentials_and_ambiguous_encodings() {
        assert_eq!(
            unescape("campaign%20launch+a", true).unwrap(),
            "campaign launch a"
        );
        for encoded in ["%", "%0", "%zz", "%ff"] {
            assert!(unescape(encoded, false).is_err(), "{encoded}");
        }
        assert!(valid_id("0123456789abcdef0123456789ABCDEF"));
        assert!(!valid_id("0123456789abcdef"));
        assert_eq!(
            visitor_hash("visitor"),
            "5f14f9e6d80f802a65269804f2552ef9889f2c7ccec5067214e58a1e48e0b3ff"
        );

        assert_eq!(label("  community  "), "community");
        for private in [
            "sk-privatevalue",
            "Bearer credential",
            "one.two.three",
            "01234567890123456789012345678901234567890",
        ] {
            assert!(label(private).is_empty(), "{private}");
        }
    }

    #[test]
    fn visit_normalization_prefers_safe_campaigns_then_external_referrers() {
        let base = |source: &str, referrer: &str| {
            serde_json::from_value::<Input>(json!({
                "consent": true,
                "consent_version": 2,
                "nonce": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
                "landing": "/pricing?utm_source=docs#plans",
                "source": source,
                "referrer": referrer
            }))
            .unwrap()
        };

        let campaign = normalize(
            &base("documentation", "https://external.example/path"),
            &["lmm.best"],
            123,
        )
        .unwrap();
        assert_eq!(campaign.source, "documentation");
        assert_eq!(campaign.evidence, "campaign_parameters");

        let referrer = normalize(
            &base("sk-privatevalue", "https://News.Example:443/path?q=1"),
            &["lmm.best"],
            124,
        )
        .unwrap();
        assert_eq!(referrer.source, "news.example");
        assert_eq!(referrer.referrer_host, "news.example");
        assert_eq!(referrer.evidence, "browser_referrer");

        for private_referrer in [
            "https://user@example.com/path",
            "https://127.0.0.1/path",
            "https://service.local/path",
            "https://example.com/%ff",
        ] {
            let visit = normalize(&base("", private_referrer), &[], 125).unwrap();
            assert_eq!(visit.source, "unknown", "{private_referrer}");
            assert_eq!(visit.referrer_host, "", "{private_referrer}");
            assert_eq!(visit.evidence, "unavailable", "{private_referrer}");
        }

        let own = normalize(
            &base("", "https://status.api.lmm.best/path"),
            &["api.lmm.best"],
            126,
        )
        .unwrap();
        assert_eq!(own.source, "unknown");
        assert!(own.referrer_host.is_empty());
    }

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
        assert!(self_report("unknown", "safe detail").is_err());
        assert!(self_report("community", &"x".repeat(161)).is_err());
        assert_eq!(
            self_report("community", "  verified by support  ").unwrap(),
            "verified by support"
        );
        assert_eq!(
            correction_reason("  verified source  ").unwrap(),
            "verified source"
        );
        assert_eq!(
            correction_reason(&"界".repeat(300))
                .unwrap()
                .chars()
                .count(),
            300
        );
        for reason in [
            "no".into(),
            "person@example.com".into(),
            "token=private".into(),
            "界".repeat(301),
        ] {
            assert!(correction_reason(&reason).is_err());
        }
    }
}
