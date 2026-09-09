//! Pancake v0.9.0 RSA-SHA256 webhook boundary. Public keys and protocol are
//! sourced from waffo-com/waffo-pancake-sdk-go at
//! 799135cbe07c45819da0ab4bf777c64fcc956220 (MIT; see assets/pancake/LICENSE).
use async_trait::async_trait;
use base64::{Engine as _, engine::general_purpose::STANDARD};
use rsa::{
    RsaPublicKey,
    pkcs1::DecodeRsaPublicKey,
    pkcs1v15::{Signature, VerifyingKey},
    pkcs8::DecodePublicKey,
    signature::Verifier,
};
use serde::Deserialize;
use sha2::Sha256;

use super::{PancakeEvent, PancakeWebhookVerifier, WebhookFailure};

const PAST_TOLERANCE_MS: i64 = 45 * 60 * 1000;
const FUTURE_TOLERANCE_MS: i64 = 60 * 1000;

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum PancakeAction {
    OrderCompleted,
    SubscriptionState,
    SubscriptionPayment,
    RefundSucceeded,
    RefundFailed,
    Ignore,
}

impl PancakeEvent {
    pub fn action(&self) -> PancakeAction {
        match self.event_type.trim() {
            "order.completed" => PancakeAction::OrderCompleted,
            "subscription.activated"
            | "subscription.renewed"
            | "subscription.canceling"
            | "subscription.uncanceled"
            | "subscription.updated"
            | "subscription.past_due"
            | "subscription.canceled" => PancakeAction::SubscriptionState,
            "subscription.payment_succeeded" => PancakeAction::SubscriptionPayment,
            "refund.succeeded" => PancakeAction::RefundSucceeded,
            "refund.failed" => PancakeAction::RefundFailed,
            _ => PancakeAction::Ignore,
        }
    }

    /// Signature validity does not make contradictory payment statuses valid.
    /// Optional fields removed by the provider must remain optional.
    pub fn validate_status(&self) -> Result<(), WebhookFailure> {
        let check = |value: &Option<String>, expected: &str| {
            if value.as_deref().is_none_or(|value| {
                value.trim().is_empty() || value.trim().eq_ignore_ascii_case(expected)
            }) {
                Ok(())
            } else {
                Err(WebhookFailure::InvalidPayload)
            }
        };
        match self.action() {
            PancakeAction::OrderCompleted => {
                check(&self.data.order_status, "completed")?;
                check(&self.data.payment_status, "succeeded")
            }
            PancakeAction::SubscriptionPayment => check(&self.data.payment_status, "succeeded"),
            PancakeAction::RefundSucceeded => check(&self.data.refund_status, "succeeded"),
            PancakeAction::RefundFailed => check(&self.data.refund_status, "failed"),
            _ => Ok(()),
        }
    }
}

/// Verified evidence stays separate: paymentDate belongs to the payment;
/// currentPeriodStart/currentPeriodEnd belong to activated/renewed events.
#[derive(Clone, Debug, Default, Deserialize, Eq, PartialEq)]
#[serde(rename_all = "camelCase", default)]
pub struct PancakeEventData {
    pub order_id: Option<String>,
    pub order_status: Option<String>,
    pub order_merchant_external_id: Option<String>,
    pub merchant_provided_buyer_identity: Option<String>,
    pub refund_ticket_merchant_external_id: Option<String>,
    pub payment_id: Option<String>,
    pub payment_status: Option<String>,
    pub payment_date: Option<String>,
    pub payment_method: Option<String>,
    pub billing_period: Option<String>,
    pub current_period_start: Option<String>,
    pub current_period_end: Option<String>,
    pub canceled_at: Option<String>,
    pub refund_status: Option<String>,
    pub refund_created_at: Option<String>,
    pub currency: Option<String>,
    pub amount: Option<String>,
    pub total: Option<String>,
}

#[derive(Default, Deserialize)]
#[serde(rename_all = "camelCase", default)]
struct Envelope {
    id: Option<String>,
    event_id: Option<String>,
    event_type: Option<String>,
    store_id: Option<String>,
    mode: Option<String>,
    data: PancakeEventData,
}

/// Keys are parsed once. No network access, payload logging, or unverified
/// business side effects occur during verification.
pub struct RsaPancakeWebhookVerifier {
    test: VerifyingKey<Sha256>,
    prod: VerifyingKey<Sha256>,
}

impl RsaPancakeWebhookVerifier {
    pub fn new(test_pem: &str, prod_pem: &str) -> Result<Self, WebhookFailure> {
        fn parse(pem: &str) -> Result<VerifyingKey<Sha256>, WebhookFailure> {
            let pem = pem.trim().replace("\\n", "\n");
            let pkcs1 = pem.contains("-----BEGIN RSA PUBLIC KEY-----");
            let mut encoded = pem;
            for marker in [
                "-----BEGIN PUBLIC KEY-----",
                "-----END PUBLIC KEY-----",
                "-----BEGIN RSA PUBLIC KEY-----",
                "-----END RSA PUBLIC KEY-----",
            ] {
                encoded = encoded.replace(marker, "");
            }
            encoded.retain(|ch| !ch.is_whitespace());
            let der = STANDARD
                .decode(encoded)
                .map_err(|_| WebhookFailure::Unavailable)?;
            let key = if pkcs1 {
                RsaPublicKey::from_pkcs1_der(&der).map_err(|_| WebhookFailure::Unavailable)?
            } else {
                RsaPublicKey::from_public_key_der(&der).map_err(|_| WebhookFailure::Unavailable)?
            };
            Ok(VerifyingKey::new(key))
        }
        Ok(Self {
            test: parse(test_pem)?,
            prod: parse(prod_pem)?,
        })
    }

    pub fn from_environment() -> Result<Self, WebhookFailure> {
        fn key(name: &str, builtin: &str) -> Result<String, WebhookFailure> {
            for name in [name, "WAFFO_WEBHOOK_PUBLIC_KEY"] {
                match std::env::var(name) {
                    Ok(value) if !value.is_empty() => return Ok(value),
                    Ok(_) | Err(std::env::VarError::NotPresent) => {}
                    Err(_) => return Err(WebhookFailure::Unavailable),
                }
            }
            Ok(builtin.to_owned())
        }
        Self::new(
            &key(
                "WAFFO_WEBHOOK_TEST_PUBLIC_KEY",
                include_str!("../../../assets/pancake/test.pem"),
            )?,
            &key(
                "WAFFO_WEBHOOK_PROD_PUBLIC_KEY",
                include_str!("../../../assets/pancake/prod.pem"),
            )?,
        )
    }

    fn verify_at(
        &self,
        payload: &[u8],
        header: &str,
        now_ms: i64,
    ) -> Result<PancakeEvent, WebhookFailure> {
        let mut timestamp = None;
        let mut encoded_signature = None;
        for pair in header.split(',') {
            let Some((key, value)) = pair.split_once('=') else {
                continue;
            };
            let slot = match key.trim() {
                "t" => &mut timestamp,
                "v1" => &mut encoded_signature,
                _ => continue,
            };
            // Reject ambiguous duplicate fields rather than allowing a proxy
            // and application to select different signature/timestamp pairs.
            if slot.replace(value.trim()).is_some() {
                return Err(WebhookFailure::InvalidSignature);
            }
        }
        let timestamp = timestamp
            .filter(|s| !s.is_empty())
            .ok_or(WebhookFailure::InvalidSignature)?;
        let ts = timestamp
            .parse::<i64>()
            .map_err(|_| WebhookFailure::InvalidSignature)?;
        let age = now_ms
            .checked_sub(ts)
            .ok_or(WebhookFailure::InvalidSignature)?;
        if !(-FUTURE_TOLERANCE_MS..=PAST_TOLERANCE_MS).contains(&age) {
            return Err(WebhookFailure::InvalidSignature);
        }
        let raw = STANDARD
            .decode(encoded_signature.ok_or(WebhookFailure::InvalidSignature)?)
            .map_err(|_| WebhookFailure::InvalidSignature)?;
        let signature =
            Signature::try_from(raw.as_slice()).map_err(|_| WebhookFailure::InvalidSignature)?;
        let envelope: Envelope =
            serde_json::from_slice(payload).map_err(|_| WebhookFailure::InvalidPayload)?;
        let mode = envelope.mode.unwrap_or_default();
        // Bind the signed mode to its own key. Go's auto-detection tries both
        // keys, which must not allow a test key to authenticate a prod payload.
        let key = match mode.as_str() {
            "test" => &self.test,
            "prod" => &self.prod,
            _ => return Err(WebhookFailure::InvalidPayload),
        };
        let mut signed = Vec::with_capacity(timestamp.len() + 1 + payload.len());
        signed.extend_from_slice(timestamp.as_bytes());
        signed.push(b'.');
        signed.extend_from_slice(payload);
        key.verify(&signed, &signature)
            .map_err(|_| WebhookFailure::InvalidSignature)?;
        Ok(PancakeEvent {
            id: envelope.id.unwrap_or_default(),
            event_id: envelope.event_id.unwrap_or_default(),
            event_type: envelope.event_type.unwrap_or_default(),
            store_id: envelope.store_id.unwrap_or_default(),
            mode,
            order_merchant_external_id: envelope
                .data
                .order_merchant_external_id
                .clone()
                .unwrap_or_default(),
            merchant_provided_buyer_identity: envelope
                .data
                .merchant_provided_buyer_identity
                .clone()
                .unwrap_or_default(),
            data: envelope.data,
        })
    }
}

#[async_trait]
impl PancakeWebhookVerifier for RsaPancakeWebhookVerifier {
    async fn verify(
        &self,
        payload: &[u8],
        signature: &str,
    ) -> Result<PancakeEvent, WebhookFailure> {
        self.verify_at(payload, signature, chrono::Utc::now().timestamp_millis())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    const NOW: i64 = 1_700_000_000_000;
    const BODY: &[u8] = include_bytes!("../../../tests/fixtures/pancake/test.json");
    const HEADER: &str = include_str!("../../../tests/fixtures/pancake/test.header");

    fn verifier() -> RsaPancakeWebhookVerifier {
        RsaPancakeWebhookVerifier::new(
            include_str!("../../../tests/fixtures/pancake/test-public.pem"),
            include_str!("../../../assets/pancake/prod.pem"),
        )
        .unwrap()
    }

    #[test]
    fn accepts_openssl_signed_payment_without_removed_period_fields() {
        let event = verifier().verify_at(BODY, HEADER, NOW).unwrap();
        assert_eq!(event.action(), PancakeAction::SubscriptionPayment);
        assert_eq!(event.event_id, "payment-event-1");
        assert_eq!(event.store_id, "store-1");
        assert_eq!(event.data.payment_id.as_deref(), Some("PAY_1"));
        assert!(event.data.current_period_start.is_none());
        assert!(event.data.current_period_end.is_none());
        assert!(event.data.billing_period.is_none());
        assert!(event.data.order_status.is_none());
        assert!(event.validate_status().is_ok());
    }

    #[test]
    fn binds_exact_raw_bytes_and_environment_to_the_signing_key() {
        let verifier = verifier();
        let mut altered = BODY.to_vec();
        altered.push(b' ');
        assert_eq!(
            verifier.verify_at(&altered, HEADER, NOW),
            Err(WebhookFailure::InvalidSignature)
        );
        // This prod envelope has a VALID signature made by the test key.
        assert_eq!(
            verifier.verify_at(
                include_bytes!("../../../tests/fixtures/pancake/prod.json"),
                include_str!("../../../tests/fixtures/pancake/prod.header"),
                NOW,
            ),
            Err(WebhookFailure::InvalidSignature)
        );
    }

    #[test]
    fn honors_provider_retry_window_and_bounds_clock_skew() {
        let verifier = verifier();
        for now in [
            NOW,
            NOW + 31 * 60 * 1000,
            NOW + PAST_TOLERANCE_MS,
            NOW - FUTURE_TOLERANCE_MS,
        ] {
            assert!(verifier.verify_at(BODY, HEADER, now).is_ok());
        }
        for now in [
            NOW + PAST_TOLERANCE_MS + 1,
            NOW - FUTURE_TOLERANCE_MS - 1,
            i64::MIN,
        ] {
            assert_eq!(
                verifier.verify_at(BODY, HEADER, now),
                Err(WebhookFailure::InvalidSignature)
            );
        }
    }

    #[test]
    fn rejects_missing_duplicate_invalid_and_overflowed_signature_fields() {
        let verifier = verifier();
        for header in [
            "",
            "t=1",
            "v1=x",
            "t=not-a-number,v1=x",
            "t=9223372036854775808,v1=x",
            "t=1700000000000,v1=not!base64!",
            "t=1700000000000,v1=",
            "t=1700000000000,t=1700000000000,v1=x",
        ] {
            assert!(verifier.verify_at(BODY, header, NOW).is_err(), "{header}");
        }
        assert!(
            verifier
                .verify_at(BODY, &format!("{HEADER},v1=x"), NOW)
                .is_err()
        );
        assert!(RsaPancakeWebhookVerifier::new("invalid", "invalid").is_err());
    }

    #[test]
    fn built_in_and_escaped_pem_keys_parse_without_network_access() {
        let test = include_str!("../../../assets/pancake/test.pem");
        let prod = include_str!("../../../assets/pancake/prod.pem");
        assert!(RsaPancakeWebhookVerifier::new(test, prod).is_ok());
        let raw = test
            .lines()
            .filter(|line| !line.starts_with("-----"))
            .collect::<String>();
        assert!(RsaPancakeWebhookVerifier::new(&raw, prod).is_ok());

        assert!(RsaPancakeWebhookVerifier::new(&test.replace('\n', "\\n"), prod).is_ok());
    }

    #[test]
    fn status_validation_covers_go_event_families_without_treating_lifecycle_as_payment() {
        for name in [
            "subscription.activated",
            "subscription.renewed",
            "subscription.canceling",
            "subscription.uncanceled",
            "subscription.updated",
            "subscription.past_due",
            "subscription.canceled",
        ] {
            let event = PancakeEvent {
                event_type: name.into(),
                ..Default::default()
            };
            assert_eq!(event.action(), PancakeAction::SubscriptionState);
            assert!(event.validate_status().is_ok());
        }
        for (name, field) in [
            ("order.completed", "orderStatus"),
            ("order.completed", "paymentStatus"),
            ("subscription.payment_succeeded", "paymentStatus"),
            ("refund.succeeded", "refundStatus"),
            ("refund.failed", "refundStatus"),
        ] {
            let data = serde_json::from_value(serde_json::json!({field:"contradiction"})).unwrap();
            let event = PancakeEvent {
                event_type: name.into(),
                data,
                ..Default::default()
            };
            assert!(event.validate_status().is_err(), "{name} {field}");
        }
        assert_eq!(
            PancakeEvent {
                event_type: "new.event".into(),
                ..Default::default()
            }
            .action(),
            PancakeAction::Ignore
        );
    }
}
