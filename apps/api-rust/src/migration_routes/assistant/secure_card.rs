use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead, KeyInit},
};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::{RngCore, rng};
use secrecy::{ExposeSecret, SecretString};
use serde_json::Value;
use sha2::{Digest, Sha256};

const MAX_SECURE_CARD_PAYLOAD_BYTES: usize = 16 * 1024;

pub(super) fn encrypt_payload(
    session_secret: &SecretString,
    plaintext: &[u8],
) -> Result<String, ()> {
    if plaintext.is_empty() || plaintext.len() > MAX_SECURE_CARD_PAYLOAD_BYTES {
        return Err(());
    }
    let key = encryption_key(session_secret);
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|_| ())?;
    let mut nonce_bytes = [0_u8; 12];
    rng().fill_bytes(&mut nonce_bytes);
    let ciphertext = cipher
        .encrypt(Nonce::from_slice(&nonce_bytes), plaintext)
        .map_err(|_| ())?;
    let mut combined = Vec::with_capacity(nonce_bytes.len() + ciphertext.len());
    combined.extend_from_slice(&nonce_bytes);
    combined.extend_from_slice(&ciphertext);
    Ok(URL_SAFE_NO_PAD.encode(combined))
}

pub(super) fn decrypt_payload(
    session_secret: &SecretString,
    ciphertext: &str,
) -> Result<Value, ()> {
    let combined = URL_SAFE_NO_PAD.decode(ciphertext).map_err(|_| ())?;
    if combined.len() <= 12 {
        return Err(());
    }
    let key = encryption_key(session_secret);
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|_| ())?;
    let plaintext = cipher
        .decrypt(Nonce::from_slice(&combined[..12]), &combined[12..])
        .map_err(|_| ())?;
    serde_json::from_slice(&plaintext).map_err(|_| ())
}

fn encryption_key(session_secret: &SecretString) -> [u8; 32] {
    Sha256::digest(
        format!(
            "assistant-secure-card-v1:{}",
            session_secret.expose_secret()
        )
        .as_bytes(),
    )
    .into()
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ciphertext_is_opaque_and_authenticated() {
        let secret = SecretString::from("test-session-secret".to_owned());
        let ciphertext = encrypt_payload(&secret, br#"{"api_key":"sk-secret"}"#)
            .expect("test encryption should succeed");
        assert!(!ciphertext.contains("sk-secret"));
        assert_eq!(
            decrypt_payload(&secret, &ciphertext).expect("test decryption should succeed")["api_key"],
            "sk-secret"
        );
        let mut tampered = ciphertext.into_bytes();
        let last = tampered.len() - 1;
        tampered[last] = if tampered[last] == b'A' { b'B' } else { b'A' };
        assert!(
            decrypt_payload(
                &secret,
                std::str::from_utf8(&tampered).expect("base64 remains utf8"),
            )
            .is_err()
        );
    }
}
