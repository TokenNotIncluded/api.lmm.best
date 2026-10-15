//! MFA-only authenticated encryption and RFC 6238 verification.
//!
//! TOTP is provided by the pinned, MIT-licensed `totp-rs` 6.0.0 implementation:
//! https://github.com/constantoine/totp-rs/tree/5a4851bd849b970d0731d59e95dd5c271c6529b8
//! The caller owns database time, replay prevention and plaintext lifetime.
use crate::identity::IdentityError;
use aes_gcm::{
    Aes256Gcm, KeyInit, Nonce,
    aead::{Aead, Payload},
};
use hkdf::Hkdf;
use rand::{RngCore, rngs::OsRng};
use sha2::Sha256;
use std::collections::HashMap;
use totp_rs::{Algorithm, Builder, Secret};
use zeroize::Zeroizing;

type Result<T> = std::result::Result<T, IdentityError>;

const KEY_BYTES: usize = 32;
const SECRET_BYTES: usize = 20;
const MAX_KEYS: usize = 8;
const MAX_LABEL_BYTES: usize = 64;
const MAX_PLAINTEXT_BYTES: usize = 1024;
const ENVELOPE_VERSION: u8 = 1;
const NONCE_BYTES: usize = 12;
const TAG_BYTES: usize = 16;
const HKDF_DOMAIN: &[u8] = b"api.lmm.best/core/mfa/v1";
const AAD_DOMAIN: &[u8] = b"api.lmm.best/core/mfa/secret";

/// No Debug implementation: key material must never enter diagnostics.
pub(crate) struct MfaKeyring {
    active_id: String,
    keys: HashMap<String, Zeroizing<[u8; KEY_BYTES]>>,
}

/// Deliberately not Debug, like the plaintext-bearing MFA response types.
pub(super) struct EncryptedSecret {
    pub(super) key_id: String,
    pub(super) ciphertext: Vec<u8>,
}

fn valid_label(label: &str) -> bool {
    (1..=MAX_LABEL_BYTES).contains(&label.len())
        && label
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || matches!(b, b'_' | b'-'))
}

fn additional_data(user: i64, revision: i64, purpose: &str, key_id: &str) -> Result<Vec<u8>> {
    if user <= 0 || revision <= 0 || !valid_label(purpose) || !valid_label(key_id) {
        return Err(IdentityError::Invalid);
    }
    let mut data = Vec::with_capacity(AAD_DOMAIN.len() + 21 + purpose.len() + key_id.len());
    data.extend_from_slice(AAD_DOMAIN);
    data.push(ENVELOPE_VERSION);
    data.extend_from_slice(&user.to_be_bytes());
    data.extend_from_slice(&revision.to_be_bytes());
    // Length prefixes prevent ambiguous boundaries between context components.
    data.extend_from_slice(&(purpose.len() as u16).to_be_bytes());
    data.extend_from_slice(purpose.as_bytes());
    data.extend_from_slice(&(key_id.len() as u16).to_be_bytes());
    data.extend_from_slice(key_id.as_bytes());
    Ok(data)
}

impl MfaKeyring {
    pub(super) fn new(active_id: String, keys: Vec<(String, Vec<u8>)>) -> Result<Self> {
        // Wrap every input before validation so early errors also wipe keys.
        let keys: Vec<_> = keys
            .into_iter()
            .map(|(id, key)| (id, Zeroizing::new(key)))
            .collect();
        if !valid_label(&active_id) || !(1..=MAX_KEYS).contains(&keys.len()) {
            return Err(IdentityError::Invalid);
        }
        let mut derived_keys = HashMap::with_capacity(keys.len());
        for (id, key) in keys {
            if !valid_label(&id) || key.len() != KEY_BYTES || derived_keys.contains_key(&id) {
                return Err(IdentityError::Invalid);
            }
            let mut derived = Zeroizing::new([0_u8; KEY_BYTES]);
            Hkdf::<Sha256>::new(Some(HKDF_DOMAIN), key.as_slice())
                .expand(b"aes-256-gcm", &mut *derived)
                .map_err(|_| IdentityError::Storage)?;
            derived_keys.insert(id, derived);
        }
        if !derived_keys.contains_key(&active_id) {
            return Err(IdentityError::Invalid);
        }
        Ok(Self {
            active_id,
            keys: derived_keys,
        })
    }

    pub(super) fn encrypt(
        &self,
        user: i64,
        revision: i64,
        purpose: &str,
        secret: &[u8],
    ) -> Result<EncryptedSecret> {
        if !(1..=MAX_PLAINTEXT_BYTES).contains(&secret.len()) {
            return Err(IdentityError::Invalid);
        }
        let aad = additional_data(user, revision, purpose, &self.active_id)?;
        let key = self
            .keys
            .get(&self.active_id)
            .ok_or(IdentityError::Storage)?;
        let cipher =
            Aes256Gcm::new_from_slice(key.as_slice()).map_err(|_| IdentityError::Storage)?;
        let mut nonce = [0_u8; NONCE_BYTES];
        OsRng
            .try_fill_bytes(&mut nonce)
            .map_err(|_| IdentityError::Storage)?;
        let encrypted = cipher
            .encrypt(
                Nonce::from_slice(&nonce),
                Payload {
                    msg: secret,
                    aad: &aad,
                },
            )
            .map_err(|_| IdentityError::Storage)?;
        let mut ciphertext = Vec::with_capacity(1 + NONCE_BYTES + encrypted.len());
        ciphertext.push(ENVELOPE_VERSION);
        ciphertext.extend_from_slice(&nonce);
        ciphertext.extend_from_slice(&encrypted);
        Ok(EncryptedSecret {
            key_id: self.active_id.clone(),
            ciphertext,
        })
    }

    pub(super) fn decrypt(
        &self,
        user: i64,
        revision: i64,
        purpose: &str,
        key_id: &str,
        ciphertext: &[u8],
    ) -> Result<Vec<u8>> {
        let aad = additional_data(user, revision, purpose, key_id)?;
        let minimum = 1 + NONCE_BYTES + TAG_BYTES + 1;
        let maximum = 1 + NONCE_BYTES + TAG_BYTES + MAX_PLAINTEXT_BYTES;
        if !(minimum..=maximum).contains(&ciphertext.len()) || ciphertext[0] != ENVELOPE_VERSION {
            return Err(IdentityError::Storage);
        }
        let key = self.keys.get(key_id).ok_or(IdentityError::Storage)?;
        Aes256Gcm::new_from_slice(key.as_slice())
            .map_err(|_| IdentityError::Storage)?
            .decrypt(
                Nonce::from_slice(&ciphertext[1..1 + NONCE_BYTES]),
                Payload {
                    msg: &ciphertext[1 + NONCE_BYTES..],
                    aad: &aad,
                },
            )
            .map_err(|_| IdentityError::Storage)
    }
}

/// Returned once to the enrollment flow; the caller should promptly encrypt it.
pub(super) fn create_secret() -> Result<Vec<u8>> {
    let mut secret = Zeroizing::new(vec![0_u8; SECRET_BYTES]);
    OsRng
        .try_fill_bytes(secret.as_mut_slice())
        .map_err(|_| IdentityError::Storage)?;
    Ok(std::mem::take(&mut *secret))
}

fn builder(secret: &[u8]) -> Result<Builder> {
    if secret.len() != SECRET_BYTES {
        return Err(IdentityError::Invalid);
    }
    Ok(Builder::new()
        .with_algorithm(Algorithm::SHA1)
        .with_digits(6)
        .with_step_duration(30)
        .with_skew(1)
        .with_secret(secret))
}

pub(super) fn secret_text(secret: &[u8]) -> Result<String> {
    if secret.len() != SECRET_BYTES {
        return Err(IdentityError::Invalid);
    }
    Ok(Secret::from(secret).to_base32())
}

pub(super) fn uri(secret: &[u8], user: i64) -> Result<String> {
    if user <= 0 {
        return Err(IdentityError::Invalid);
    }
    builder(secret)?
        .with_issuer(Some("LMM"))
        .with_account_name(user.to_string())
        .build()
        .map_err(|_| IdentityError::Storage)?
        .to_url()
        .map_err(|_| IdentityError::Storage)
}

/// Returns the matched time-step counter for a database replay-prevention CAS.
/// No wall-clock reads occur here: authentication uses transaction-owned time.
pub(super) fn verify(secret: &[u8], code: &str, db_epoch: i64) -> Result<Option<i64>> {
    let time = u64::try_from(db_epoch).map_err(|_| IdentityError::Storage)?;
    if code.len() != 6 || !code.bytes().all(|b| b.is_ascii_digit()) {
        return Ok(None);
    }
    // A nonnegative i64 timestamp also bounds the upstream skew arithmetic.
    builder(secret)?
        .build()
        .map_err(|_| IdentityError::Storage)?
        .check(code, time)
        .map(i64::try_from)
        .transpose()
        .map_err(|_| IdentityError::Storage)
}

#[cfg(test)]
mod tests {
    use super::*;

    const RFC_SECRET: &[u8; SECRET_BYTES] = b"12345678901234567890";

    fn keyring() -> MfaKeyring {
        MfaKeyring::new(
            "current".into(),
            vec![("current".into(), vec![7; KEY_BYTES])],
        )
        .expect("valid test keyring")
    }

    #[test]
    fn encryption_round_trip_and_unique_nonces() {
        let keys = keyring();
        let first = keys.encrypt(42, 3, "totp", RFC_SECRET).unwrap();
        let second = keys.encrypt(42, 3, "totp", RFC_SECRET).unwrap();
        assert_eq!(first.key_id, "current");
        assert_ne!(first.ciphertext, second.ciphertext);
        assert_eq!(
            keys.decrypt(42, 3, "totp", &first.key_id, &first.ciphertext)
                .unwrap(),
            RFC_SECRET
        );
    }

    #[test]
    fn ciphertext_is_bound_to_every_context_component() {
        let keys = MfaKeyring::new(
            "current".into(),
            vec![
                ("current".into(), vec![7; KEY_BYTES]),
                ("same-material".into(), vec![7; KEY_BYTES]),
            ],
        )
        .unwrap();
        let sealed = keys.encrypt(42, 3, "totp", RFC_SECRET).unwrap();
        for (user, revision, purpose, key_id) in [
            (43, 3, "totp", "current"),
            (42, 4, "totp", "current"),
            (42, 3, "pending-totp", "current"),
            (42, 3, "totp", "same-material"),
        ] {
            assert_eq!(
                keys.decrypt(user, revision, purpose, key_id, &sealed.ciphertext),
                Err(IdentityError::Storage)
            );
        }
    }

    #[test]
    fn tampered_and_truncated_envelopes_fail_closed() {
        let keys = keyring();
        let sealed = keys.encrypt(42, 3, "totp", RFC_SECRET).unwrap();
        for index in [
            0,
            1,
            NONCE_BYTES,
            NONCE_BYTES + 1,
            sealed.ciphertext.len() - 1,
        ] {
            let mut tampered = sealed.ciphertext.clone();
            tampered[index] ^= 1;
            assert_eq!(
                keys.decrypt(42, 3, "totp", &sealed.key_id, &tampered),
                Err(IdentityError::Storage)
            );
        }
        for length in 0..sealed.ciphertext.len() {
            assert_eq!(
                keys.decrypt(42, 3, "totp", &sealed.key_id, &sealed.ciphertext[..length]),
                Err(IdentityError::Storage)
            );
        }
    }

    #[test]
    fn key_rotation_retains_only_explicit_previous_keys() {
        let old = keyring();
        let sealed = old.encrypt(42, 3, "totp", RFC_SECRET).unwrap();
        let rotated = MfaKeyring::new(
            "next".into(),
            vec![
                ("next".into(), vec![8; KEY_BYTES]),
                ("current".into(), vec![7; KEY_BYTES]),
            ],
        )
        .unwrap();
        assert_eq!(
            rotated
                .decrypt(42, 3, "totp", &sealed.key_id, &sealed.ciphertext)
                .unwrap(),
            RFC_SECRET
        );
        assert_eq!(
            rotated.encrypt(42, 3, "totp", RFC_SECRET).unwrap().key_id,
            "next"
        );
        let retired =
            MfaKeyring::new("next".into(), vec![("next".into(), vec![8; KEY_BYTES])]).unwrap();
        assert_eq!(
            retired.decrypt(42, 3, "totp", &sealed.key_id, &sealed.ciphertext),
            Err(IdentityError::Storage)
        );
    }

    #[test]
    fn invalid_keyring_configurations_are_rejected() {
        for (active, keys) in [
            ("current", vec![]),
            ("current", vec![("current".into(), vec![7; 31])]),
            ("missing", vec![("current".into(), vec![7; KEY_BYTES])]),
            ("bad:id", vec![("bad:id".into(), vec![7; KEY_BYTES])]),
            (
                "current",
                vec![
                    ("current".into(), vec![7; KEY_BYTES]),
                    ("current".into(), vec![8; KEY_BYTES]),
                ],
            ),
        ] {
            assert!(matches!(
                MfaKeyring::new(active.into(), keys),
                Err(IdentityError::Invalid)
            ));
        }
        let nine = (0..9)
            .map(|n| (format!("key{n}"), vec![7; KEY_BYTES]))
            .collect();
        assert!(matches!(
            MfaKeyring::new("key0".into(), nine),
            Err(IdentityError::Invalid)
        ));
        assert!(matches!(
            MfaKeyring::new("x".repeat(65), vec![("x".repeat(65), vec![7; KEY_BYTES])]),
            Err(IdentityError::Invalid)
        ));
    }

    #[test]
    fn invalid_contexts_and_oversized_plaintexts_are_rejected() {
        let keys = keyring();
        for (user, revision, purpose) in [(0, 1, "totp"), (1, 0, "totp"), (1, 1, ""), (1, 1, "a:b")]
        {
            assert!(matches!(
                keys.encrypt(user, revision, purpose, RFC_SECRET),
                Err(IdentityError::Invalid)
            ));
        }
        for secret in [vec![], vec![1; MAX_PLAINTEXT_BYTES + 1]] {
            assert!(matches!(
                keys.encrypt(42, 3, "totp", &secret),
                Err(IdentityError::Invalid)
            ));
        }
    }

    #[test]
    fn rfc_6238_sha1_vectors_with_six_digit_tokens() {
        // RFC 6238 Appendix B uses eight digits; six digits retain the final six.
        for (time, code) in [
            (59, "287082"),
            (1_111_111_109, "081804"),
            (1_111_111_111, "050471"),
            (1_234_567_890, "005924"),
            (2_000_000_000, "279037"),
            (20_000_000_000, "353130"),
        ] {
            assert_eq!(verify(RFC_SECRET, code, time).unwrap(), Some(time / 30));
        }
    }

    #[test]
    fn totp_skew_returns_the_original_matched_step() {
        let time = 1_234_567_890;
        let code = "005924";
        for delta in [-30, 0, 30] {
            assert_eq!(
                verify(RFC_SECRET, code, time + delta).unwrap(),
                Some(time / 30)
            );
        }
        for delta in [-60, 60] {
            assert_eq!(verify(RFC_SECRET, code, time + delta).unwrap(), None);
        }
    }

    #[test]
    fn totp_rejects_malformed_codes_and_negative_database_time() {
        for code in [
            "",
            "12345",
            "1234567",
            " 05924",
            "005924 ",
            "+05924",
            "１２３４５６",
        ] {
            assert_eq!(verify(RFC_SECRET, code, 1_234_567_890).unwrap(), None);
        }
        assert_eq!(
            verify(RFC_SECRET, "005924", -1),
            Err(IdentityError::Storage)
        );
        assert_eq!(
            verify(&RFC_SECRET[..19], "005924", 1_234_567_890),
            Err(IdentityError::Invalid)
        );
    }

    #[test]
    fn totp_handles_zero_and_maximum_database_timestamps() {
        let totp = builder(RFC_SECRET).unwrap().build().unwrap();
        for time in [0, i64::MAX] {
            let code = totp.generate(time as u64).to_string();
            assert_eq!(verify(RFC_SECRET, &code, time).unwrap(), Some(time / 30));
        }
    }

    #[test]
    fn enrollment_uses_library_base32_and_user_id_label() {
        let encoded = secret_text(RFC_SECRET).unwrap();
        assert_eq!(encoded, "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ");
        assert_eq!(
            uri(RFC_SECRET, 42).unwrap(),
            format!("otpauth://totp/LMM:42?secret={encoded}&issuer=LMM")
        );
        assert_eq!(uri(RFC_SECRET, 0), Err(IdentityError::Invalid));
        assert_eq!(secret_text(&RFC_SECRET[..19]), Err(IdentityError::Invalid));
    }

    #[test]
    fn enrollment_generates_twenty_random_bytes() {
        let first = create_secret().unwrap();
        let second = create_secret().unwrap();
        assert_eq!(first.len(), SECRET_BYTES);
        assert_eq!(second.len(), SECRET_BYTES);
        assert_ne!(first, second);
    }
}
