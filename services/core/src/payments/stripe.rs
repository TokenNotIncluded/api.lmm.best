//! Verify the original Stripe webhook bytes against endpoint secrets owned by
//! the core. This does not trust the extension's interpretation of the event.
//! Event replay, transaction ownership and refund holds remain ledger concerns.

use std::{collections::BTreeMap, fmt};

use hmac::{Hmac, Mac};
use serde::{Deserialize, Deserializer, de};
use serde_json::Value;
use sha2::Sha256;

use super::{Channel, Error};

// Leave room for the RPC envelope and keep durable blocked evidence bounded.
const MAX_PAYLOAD: usize = 32 * 1024;
const MAX_HEADER: usize = 2048;
const MAX_SIGNATURES: usize = 8;
const TIMESTAMP_TOLERANCE: u64 = 300;

#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub(super) enum EventKind {
    Credit,
    RefundSucceeded,
    RefundFailed,
    RefundPending,
}

#[derive(Debug, PartialEq, Eq)]
pub(super) struct Verified {
    pub(super) event_id: String,
    pub(super) transaction: String,
    pub(super) kind: EventKind,
    pub(super) amount_minor: i64,
    pub(super) refund_id: Option<String>,
    pub(super) hold_receipt_id: Option<String>,
}

#[derive(Deserialize)]
struct Envelope {
    id: String,
    #[serde(rename = "type")]
    kind: String,
    #[serde(default)]
    account: String,
    livemode: bool,
    data: EventData,
}

#[derive(Deserialize)]
struct EventData {
    object: Object,
}

#[derive(Deserialize)]
struct Object {
    id: String,
    object: String,
    status: String,
    amount: i64,
    #[serde(default)]
    amount_received: Option<i64>,
    currency: String,
    #[serde(default)]
    livemode: Option<bool>,
    #[serde(default)]
    payment_intent: Option<Value>,
    metadata: BTreeMap<String, String>,
}

/// Secrets come from trusted core configuration for this merchant and mode.
/// Direct-account events can omit `account`; metadata alone never authenticates
/// a merchant. The endpoint secret is the merchant binding in that case.
pub(super) fn verify(
    payload: &[u8],
    signature: &[u8],
    channel: &Channel,
    intent_id: i64,
    expected_currency: &str,
    expected_amount_minor: i64,
    now: i64,
) -> Result<Verified, Error> {
    if payload.is_empty()
        || payload.len() > MAX_PAYLOAD
        || signature.is_empty()
        || signature.len() > MAX_HEADER
        || intent_id <= 0
        || expected_amount_minor <= 0
        || !currency(expected_currency)
    {
        return Err(Error::Invalid);
    }
    if channel.id <= 0
        || channel.provider != "stripe"
        || !identifier(&channel.merchant, "acct_")
        || !matches!(channel.environment.as_str(), "test" | "live")
        || !currency(&channel.currency)
        || channel.credits_per_minor <= 0
        || channel.max_amount_minor <= 0
        || !(1..=2).contains(&channel.webhook_secrets.len())
        || channel.webhook_secrets.iter().any(|secret| {
            !(16..=512).contains(&secret.len())
                || !secret.starts_with("whsec_")
                || !secret.bytes().all(|byte| byte.is_ascii_graphic())
        })
    {
        return Err(Error::Unavailable);
    }
    if !channel.currency.eq_ignore_ascii_case(expected_currency)
        || expected_amount_minor > channel.max_amount_minor
    {
        return Err(Error::Conflict);
    }
    verify_signature(payload, signature, &channel.webhook_secrets, now)?;

    // serde_json normally keeps the last duplicate map key. Check every map,
    // including ignored provider fields, before deserializing the typed event.
    let mut decoder = serde_json::Deserializer::from_slice(payload);
    let value = UniqueValue::deserialize(&mut decoder).map_err(|_| Error::Invalid)?;
    decoder.end().map_err(|_| Error::Invalid)?;
    let envelope: Envelope = serde_json::from_value(value.0).map_err(|_| Error::Invalid)?;
    if !identifier(&envelope.id, "evt_") {
        return Err(Error::Invalid);
    }
    let object = envelope.data.object;
    let live = channel.environment == "live";
    if envelope.livemode != live
        || object.livemode.is_some_and(|mode| mode != live)
        || (!envelope.account.is_empty() && envelope.account != channel.merchant)
    {
        return Err(Error::Denied);
    }
    if object.metadata.get("lmm_merchant") != Some(&channel.merchant) {
        return Err(Error::Denied);
    }
    if object.metadata.get("lmm_core_intent_id") != Some(&intent_id.to_string())
        || !currency(&object.currency)
        || !object.currency.eq_ignore_ascii_case(expected_currency)
        || object.amount <= 0
        || object.amount > expected_amount_minor
    {
        return Err(Error::Conflict);
    }

    match envelope.kind.as_str() {
        "payment_intent.succeeded" => {
            if object.object != "payment_intent" || !identifier(&object.id, "pi_") {
                return Err(Error::Invalid);
            }
            if object.status != "succeeded"
                || object.amount != expected_amount_minor
                || object.amount_received != Some(expected_amount_minor)
            {
                return Err(Error::Conflict);
            }
            Ok(Verified {
                event_id: envelope.id,
                transaction: object.id,
                kind: EventKind::Credit,
                amount_minor: object.amount,
                refund_id: None,
                hold_receipt_id: None,
            })
        }
        "refund.created" | "refund.updated" | "refund.failed" => {
            if object.object != "refund" || !identifier(&object.id, "re_") {
                return Err(Error::Invalid);
            }
            let transaction = payment_intent(object.payment_intent.as_ref())?;
            let kind = match object.status.as_str() {
                "succeeded" => EventKind::RefundSucceeded,
                "failed" | "canceled" => EventKind::RefundFailed,
                "pending" | "requires_action" => EventKind::RefundPending,
                _ => return Err(Error::Invalid),
            };
            if envelope.kind == "refund.failed" && kind != EventKind::RefundFailed {
                return Err(Error::Conflict);
            }
            let hold_receipt_id = object.metadata.get("lmm_hold_receipt_id").cloned();
            if hold_receipt_id.as_ref().is_some_and(|id| !opaque_id(id)) {
                return Err(Error::Invalid);
            }
            Ok(Verified {
                event_id: envelope.id,
                transaction,
                kind,
                amount_minor: object.amount,
                refund_id: Some(object.id),
                hold_receipt_id,
            })
        }
        _ => Err(Error::Invalid),
    }
}

fn verify_signature(
    payload: &[u8],
    header: &[u8],
    secrets: &[String],
    now: i64,
) -> Result<(), Error> {
    let header = std::str::from_utf8(header).map_err(|_| Error::Denied)?;
    if !header
        .bytes()
        .all(|byte| byte.is_ascii_graphic() || matches!(byte, b' ' | b'\t'))
    {
        return Err(Error::Denied);
    }
    let mut timestamp = None;
    let mut signatures = Vec::new();
    for part in header.split(',') {
        let (key, value) = part.trim().split_once('=').ok_or(Error::Denied)?;
        if key.is_empty()
            || value.is_empty()
            || !key.bytes().all(|byte| byte.is_ascii_alphanumeric())
        {
            return Err(Error::Denied);
        }
        match key {
            "t" => {
                if timestamp.replace(value).is_some() {
                    return Err(Error::Denied);
                }
            }
            "v1" => {
                if signatures.len() == MAX_SIGNATURES {
                    return Err(Error::Denied);
                }
                signatures.push(decode_signature(value).ok_or(Error::Denied)?);
            }
            _ => {} // Stripe can send signatures for other algorithm versions.
        }
    }
    let timestamp = timestamp.ok_or(Error::Denied)?;
    if !timestamp.bytes().all(|byte| byte.is_ascii_digit()) || signatures.is_empty() {
        return Err(Error::Denied);
    }
    let time = timestamp.parse::<i64>().map_err(|_| Error::Denied)?;
    if time <= 0 || now <= 0 || now.abs_diff(time) > TIMESTAMP_TOLERANCE {
        return Err(Error::Denied);
    }
    for secret in secrets {
        let mut mac =
            Hmac::<Sha256>::new_from_slice(secret.as_bytes()).map_err(|_| Error::Unavailable)?;
        mac.update(timestamp.as_bytes());
        mac.update(b".");
        mac.update(payload);
        for signature in &signatures {
            if mac.clone().verify_slice(signature).is_ok() {
                return Ok(());
            }
        }
    }
    Err(Error::Denied)
}

fn decode_signature(value: &str) -> Option<[u8; 32]> {
    if value.len() != 64 {
        return None;
    }
    let mut decoded = [0_u8; 32];
    for (index, pair) in value.as_bytes().as_chunks::<2>().0.iter().enumerate() {
        let nibble = |byte: u8| match byte {
            b'0'..=b'9' => Some(byte - b'0'),
            b'a'..=b'f' => Some(byte - b'a' + 10),
            b'A'..=b'F' => Some(byte - b'A' + 10),
            _ => None,
        };
        decoded[index] = (nibble(pair[0])? << 4) | nibble(pair[1])?;
    }
    Some(decoded)
}

fn currency(value: &str) -> bool {
    value.len() == 3 && value.bytes().all(|byte| byte.is_ascii_alphabetic())
}

fn identifier(value: &str, prefix: &str) -> bool {
    value.len() > prefix.len() && value.starts_with(prefix) && opaque_id(value)
}

fn opaque_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 255
        && value
            .bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || matches!(byte, b'_' | b'-'))
}

fn payment_intent(value: Option<&Value>) -> Result<String, Error> {
    let id = match value {
        Some(Value::String(id)) => Some(id.as_str()),
        Some(Value::Object(object)) => {
            if object
                .get("object")
                .is_some_and(|kind| kind.as_str() != Some("payment_intent"))
            {
                return Err(Error::Invalid);
            }
            object.get("id").and_then(Value::as_str)
        }
        _ => None,
    };
    id.filter(|id| identifier(id, "pi_"))
        .map(str::to_owned)
        .ok_or(Error::Invalid)
}

struct UniqueValue(Value);

impl<'de> Deserialize<'de> for UniqueValue {
    fn deserialize<D: Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> de::Visitor<'de> for Visitor {
            type Value = UniqueValue;

            fn expecting(&self, formatter: &mut fmt::Formatter<'_>) -> fmt::Result {
                formatter.write_str("JSON without duplicate object keys")
            }

            fn visit_bool<E: de::Error>(self, value: bool) -> Result<Self::Value, E> {
                Ok(UniqueValue(Value::Bool(value)))
            }

            fn visit_i64<E: de::Error>(self, value: i64) -> Result<Self::Value, E> {
                Ok(UniqueValue(Value::Number(value.into())))
            }

            fn visit_u64<E: de::Error>(self, value: u64) -> Result<Self::Value, E> {
                Ok(UniqueValue(Value::Number(value.into())))
            }

            fn visit_f64<E: de::Error>(self, value: f64) -> Result<Self::Value, E> {
                serde_json::Number::from_f64(value)
                    .map(|number| UniqueValue(Value::Number(number)))
                    .ok_or_else(|| E::custom("invalid JSON number"))
            }

            fn visit_str<E: de::Error>(self, value: &str) -> Result<Self::Value, E> {
                self.visit_string(value.to_owned())
            }

            fn visit_string<E: de::Error>(self, value: String) -> Result<Self::Value, E> {
                Ok(UniqueValue(Value::String(value)))
            }

            fn visit_unit<E: de::Error>(self) -> Result<Self::Value, E> {
                Ok(UniqueValue(Value::Null))
            }

            fn visit_seq<A: de::SeqAccess<'de>>(self, mut seq: A) -> Result<Self::Value, A::Error> {
                let mut values = Vec::new();
                while let Some(value) = seq.next_element::<UniqueValue>()? {
                    values.push(value.0);
                }
                Ok(UniqueValue(Value::Array(values)))
            }

            fn visit_map<A: de::MapAccess<'de>>(self, mut map: A) -> Result<Self::Value, A::Error> {
                let mut values = serde_json::Map::new();
                while let Some(key) = map.next_key::<String>()? {
                    if values.contains_key(&key) {
                        return Err(de::Error::custom("duplicate JSON key"));
                    }
                    values.insert(key, map.next_value::<UniqueValue>()?.0);
                }
                Ok(UniqueValue(Value::Object(values)))
            }
        }
        deserializer.deserialize_any(Visitor)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;

    const NOW: i64 = 1_900_000_000;
    const SECRET: &str = "whsec_synthetic_core_unit_test_secret";

    fn channel() -> Channel {
        Channel {
            id: 7,
            provider: "stripe".into(),
            merchant: "acct_local_test".into(),
            environment: "test".into(),
            currency: "USD".into(),
            webhook_secrets: vec![SECRET.into()],
            credits_per_minor: 5000,
            max_amount_minor: 1_000_000,
        }
    }

    fn event() -> Value {
        json!({
            "id": "evt_core_test", "object": "event", "type": "payment_intent.succeeded",
            "livemode": false, "data": {"object": {
                "id": "pi_core_test", "object": "payment_intent", "status": "succeeded",
                "amount": 100, "amount_received": 100, "currency": "usd", "livemode": false,
                "metadata": {"lmm_core_intent_id": "42", "lmm_merchant": "acct_local_test"}
            }}
        })
    }

    fn signed(raw: &[u8], secret: &str, time: i64) -> Vec<u8> {
        let mut mac = Hmac::<Sha256>::new_from_slice(secret.as_bytes()).unwrap();
        mac.update(time.to_string().as_bytes());
        mac.update(b".");
        mac.update(raw);
        let hash: String = mac
            .finalize()
            .into_bytes()
            .iter()
            .map(|v| format!("{v:02x}"))
            .collect();
        format!("t={time},v1={hash}").into_bytes()
    }

    fn check(value: Value) -> Result<Verified, Error> {
        let raw = serde_json::to_vec(&value).unwrap();
        verify(
            &raw,
            &signed(&raw, SECRET, NOW),
            &channel(),
            42,
            "USD",
            100,
            NOW,
        )
    }

    #[test]
    fn signed_payment_is_bound_to_intent_amount_and_endpoint() {
        let verified = check(event()).unwrap();
        assert_eq!(verified.kind, EventKind::Credit);
        assert_eq!(verified.event_id, "evt_core_test");
        assert_eq!(verified.transaction, "pi_core_test");
        assert_eq!(verified.amount_minor, 100);
        assert!(verified.refund_id.is_none());
        let mut value = event();
        value["account"] = json!("acct_local_test");
        assert!(check(value).is_ok());
    }

    #[test]
    fn signature_covers_exact_bytes_and_trusted_secret() {
        let raw = serde_json::to_vec(&event()).unwrap();
        let signature = signed(&raw, SECRET, NOW);
        let mut changed = raw.clone();
        changed.push(b' ');
        assert!(matches!(
            verify(&changed, &signature, &channel(), 42, "USD", 100, NOW),
            Err(Error::Denied)
        ));
        assert!(matches!(
            verify(
                &raw,
                &signed(&raw, "whsec_attacker_secret", NOW),
                &channel(),
                42,
                "USD",
                100,
                NOW
            ),
            Err(Error::Denied)
        ));
    }

    #[test]
    fn current_and_previous_endpoint_secrets_can_rotate() {
        let raw = serde_json::to_vec(&event()).unwrap();
        let mut config = channel();
        config
            .webhook_secrets
            .insert(0, "whsec_replacement_core_test".into());
        assert!(
            verify(
                &raw,
                &signed(&raw, SECRET, NOW),
                &config,
                42,
                "USD",
                100,
                NOW
            )
            .is_ok()
        );
    }

    #[test]
    fn stale_future_and_overflow_timestamps_are_denied() {
        let raw = serde_json::to_vec(&event()).unwrap();
        for time in [NOW - 301, NOW + 301, 0, i64::MAX] {
            assert!(matches!(
                verify(
                    &raw,
                    &signed(&raw, SECRET, time),
                    &channel(),
                    42,
                    "USD",
                    100,
                    NOW
                ),
                Err(Error::Denied)
            ));
        }
        for time in [NOW - 300, NOW + 300] {
            assert!(
                verify(
                    &raw,
                    &signed(&raw, SECRET, time),
                    &channel(),
                    42,
                    "USD",
                    100,
                    NOW
                )
                .is_ok()
            );
        }
    }

    #[test]
    fn ambiguous_or_excessive_signature_headers_are_denied() {
        let raw = serde_json::to_vec(&event()).unwrap();
        let signature = String::from_utf8(signed(&raw, SECRET, NOW)).unwrap();
        let hash = signature.split_once("v1=").unwrap().1;
        for header in [
            format!("{signature},t={NOW}"),
            format!("t={NOW}"),
            format!("t={NOW},v1=xyz"),
            format!("t={NOW},v1={hash}\n"),
            format!("t={NOW}{}", format!(",v1={hash}").repeat(9)),
            format!("t=+{NOW},v1={hash}"),
        ] {
            assert!(matches!(
                verify(&raw, header.as_bytes(), &channel(), 42, "USD", 100, NOW),
                Err(Error::Denied)
            ));
        }
        let zeros = "00".repeat(32);
        let multiple = format!("t={NOW},v0=ignored,v1={zeros},v1={hash}");
        assert!(verify(&raw, multiple.as_bytes(), &channel(), 42, "USD", 100, NOW).is_ok());
    }

    #[test]
    fn signed_wrong_amount_currency_and_intent_are_conflicts() {
        for (key, value) in [
            ("amount", json!(99)),
            ("amount_received", json!(99)),
            ("currency", json!("eur")),
            ("status", json!("processing")),
        ] {
            let mut envelope = event();
            envelope["data"]["object"][key] = value;
            assert!(matches!(check(envelope), Err(Error::Conflict)));
        }
        let mut value = event();
        value["data"]["object"]["metadata"]["lmm_core_intent_id"] = json!("43");
        assert!(matches!(check(value), Err(Error::Conflict)));
    }

    #[test]
    fn signed_wrong_merchant_or_environment_is_denied() {
        for key in ["account", "livemode"] {
            let mut value = event();
            value[key] = if key == "account" {
                json!("acct_other")
            } else {
                json!(true)
            };
            assert!(matches!(check(value), Err(Error::Denied)));
        }
        let mut value = event();
        value["data"]["object"]["metadata"]["lmm_merchant"] = json!("acct_other");
        assert!(matches!(check(value), Err(Error::Denied)));
        let mut value = event();
        value["data"]["object"]["livemode"] = json!(true);
        assert!(matches!(check(value), Err(Error::Denied)));
    }

    #[test]
    fn duplicate_json_keys_are_rejected_at_every_depth() {
        let base = serde_json::to_string(&event()).unwrap();
        let bodies = [
            base.replacen("{", "{\"livemode\":true,", 1),
            base.replace(
                "\"metadata\":{",
                "\"metadata\":{\"lmm_core_intent_id\":\"999\",",
            ),
            base.replacen("{", "{\"ignored\":[{\"key\":1,\"key\":2}],", 1),
            base.replacen("{", "{\"ignored\":{\"key\":1,\"\\u006bey\":2},", 1),
        ];
        for body in bodies {
            let raw = body.as_bytes();
            assert!(matches!(
                verify(
                    raw,
                    &signed(raw, SECRET, NOW),
                    &channel(),
                    42,
                    "USD",
                    100,
                    NOW
                ),
                Err(Error::Invalid)
            ));
        }
    }

    fn refund(status: &str) -> Value {
        let mut value = event();
        value["type"] = json!("refund.updated");
        let object = &mut value["data"]["object"];
        object["id"] = json!("re_core_test");
        object["object"] = json!("refund");
        object["status"] = json!(status);
        object["amount"] = json!(40);
        object["payment_intent"] = json!("pi_core_test");
        object["metadata"]["lmm_hold_receipt_id"] = json!("hold_local_test");
        value
    }

    #[test]
    fn refund_states_keep_original_payment_and_hold_evidence() {
        for (status, kind) in [
            ("succeeded", EventKind::RefundSucceeded),
            ("failed", EventKind::RefundFailed),
            ("canceled", EventKind::RefundFailed),
            ("pending", EventKind::RefundPending),
            ("requires_action", EventKind::RefundPending),
        ] {
            let verified = check(refund(status)).unwrap();
            assert_eq!(verified.kind, kind);
            assert_eq!(verified.transaction, "pi_core_test");
            assert_eq!(verified.refund_id.as_deref(), Some("re_core_test"));
            assert_eq!(verified.hold_receipt_id.as_deref(), Some("hold_local_test"));
            assert_eq!(verified.amount_minor, 40);
        }
        let mut value = refund("succeeded");
        value["data"]["object"]["payment_intent"] =
            json!({"id":"pi_core_test","object":"payment_intent"});
        assert!(check(value).is_ok());
    }

    #[test]
    fn refund_wrong_binding_amount_and_failed_event_status_are_rejected() {
        let mut value = refund("succeeded");
        value["data"]["object"]["amount"] = json!(101);
        assert!(matches!(check(value), Err(Error::Conflict)));
        let mut value = refund("succeeded");
        value["data"]["object"]["metadata"]["lmm_core_intent_id"] = json!("1");
        assert!(matches!(check(value), Err(Error::Conflict)));
        let mut value = refund("succeeded");
        value["data"]["object"]["payment_intent"] = json!("ch_wrong_kind");
        assert!(matches!(check(value), Err(Error::Invalid)));
        let mut value = refund("succeeded");
        value["type"] = json!("refund.failed");
        assert!(matches!(check(value), Err(Error::Conflict)));
    }

    #[test]
    fn invalid_json_types_bounds_and_missing_bindings_fail_closed() {
        for body in [b"[]".as_slice(), b"{} {}", b"null"] {
            assert!(matches!(
                verify(
                    body,
                    &signed(body, SECRET, NOW),
                    &channel(),
                    42,
                    "USD",
                    100,
                    NOW
                ),
                Err(Error::Invalid)
            ));
        }
        let oversized = vec![b' '; MAX_PAYLOAD + 1];
        assert!(matches!(
            verify(&oversized, b"x", &channel(), 42, "USD", 100, NOW),
            Err(Error::Invalid)
        ));
        let raw = serde_json::to_vec(&event()).unwrap();
        assert!(matches!(
            verify(
                &raw,
                &vec![b'x'; MAX_HEADER + 1],
                &channel(),
                42,
                "USD",
                100,
                NOW
            ),
            Err(Error::Invalid)
        ));
        for key in ["lmm_core_intent_id", "lmm_merchant"] {
            let mut value = event();
            value["data"]["object"]["metadata"]
                .as_object_mut()
                .unwrap()
                .remove(key);
            assert!(check(value).is_err());
        }
        let mut value = event();
        value["data"]["object"]["amount"] = json!(100.0);
        assert!(matches!(check(value), Err(Error::Invalid)));
    }
}
