//! Restricted HumanGrant Protocol 0.1 request-signature verification.
//! Signature material and credentials must never be written to logs.
use axum::http::{HeaderMap, Method, Uri};
use base64::{Engine, engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD}};
use ring::signature::{UnparsedPublicKey, ED25519};
use sha2::{Digest, Sha256};
use std::time::{SystemTime, UNIX_EPOCH};

#[derive(Clone)]
pub(super) struct Signed {
    pub actor_id: String,
    pub authorization: String,
    pub nonce: String,
    pub expires: i64,
}

fn only<'a>(headers: &'a HeaderMap, name: &str) -> Result<&'a str, ()> {
    let mut values = headers.get_all(name).iter();
    let first = values.next().ok_or(())?;
    if values.next().is_some() { return Err(()); }
    let text = first.to_str().map_err(|_| ())?;
    if text.is_empty() || text.trim() != text || text.contains('\n') || text.contains('\r') {
        return Err(());
    }
    Ok(text)
}

pub(super) fn now() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH)
        .unwrap_or_default().as_secs() as i64
}

fn canonical_b64(engine: &impl Engine, input: &str, size: usize) -> Result<Vec<u8>, ()> {
    let value = engine.decode(input).map_err(|_| ())?;
    if value.len() != size || engine.encode(&value) != input { return Err(()); }
    Ok(value)
}

pub(super) fn verify(
    headers: &HeaderMap,
    method: &Method,
    uri: &Uri,
    bytes: &[u8],
    origin: &str,
    clock: i64,
) -> Result<Signed, ()> {
    if uri.query().is_some() || uri.path().contains('%') || bytes.len() > 16 * 1024 {
        return Err(());
    }
    let content_type = only(headers, "content-type")?;
    if content_type != "application/json" { return Err(()); }
    let public_b64 = only(headers, "x-hg-key")?;
    let key = canonical_b64(&URL_SAFE_NO_PAD, public_b64, 32)?;
    let actor_id = format!("ed25519:{}", URL_SAFE_NO_PAD.encode(Sha256::digest(&key)));
    let authorization = only(headers, "authorization")?;
    let credential = authorization.strip_prefix("HumanGrant ").ok_or(())?;
    if credential != "anonymous"
        && (credential.len() != 43 || canonical_b64(&URL_SAFE_NO_PAD, credential, 32).is_err())
    {
        return Err(());
    }
    let expected_digest = format!("sha-256=:{}:", STANDARD.encode(Sha256::digest(bytes)));
    if only(headers, "content-digest")? != expected_digest { return Err(()); }

    let input = only(headers, "signature-input")?;
    const PREFIX: &str = "hg=(\"@method\" \"@target-uri\" \"content-digest\" \"content-type\" \"x-hg-key\" \"authorization\");created=";
    let body = input.strip_prefix(PREFIX).ok_or(())?;
    let (created, body) = body.split_once(";expires=").ok_or(())?;
    let (expires, body) = body.split_once(";nonce=\"").ok_or(())?;
    let (nonce, body) = body.split_once("\";keyid=\"").ok_or(())?;
    let (key_id, suffix) = body.split_once('"').ok_or(())?;
    if suffix != ";alg=\"ed25519\";tag=\"humangrant-0.1\""
        || key_id != actor_id || nonce.len() != 32
        || canonical_b64(&URL_SAFE_NO_PAD, nonce, 24).is_err()
        || created.starts_with('0') || expires.starts_with('0')
    {
        return Err(());
    }
    let created: i64 = created.parse().map_err(|_| ())?;
    let expires: i64 = expires.parse().map_err(|_| ())?;
    if created > clock + 30 || created < clock - 60
        || expires <= clock || expires <= created || expires > created + 60
    {
        return Err(());
    }
    let signature_header = only(headers, "signature")?;
    let signature = signature_header.strip_prefix("hg=:")
        .and_then(|s| s.strip_suffix(':')).ok_or(())?;
    let signature = canonical_b64(&STANDARD, signature, 64)?;
    let params = input.strip_prefix("hg=").ok_or(())?;
    let base = format!(
        "\"@method\": {}\n\"@target-uri\": {}{}\n\"content-digest\": {}\n\"content-type\": {}\n\"x-hg-key\": {}\n\"authorization\": {}\n\"@signature-params\": {}",
        method.as_str(), origin, uri.path(), expected_digest, content_type,
        public_b64, authorization, params
    );
    UnparsedPublicKey::new(&ED25519, &key)
        .verify(base.as_bytes(), &signature)
        .map_err(|_| ())?;
    Ok(Signed { actor_id, authorization: credential.to_owned(), nonce: nonce.to_owned(), expires })
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn public_signature_vector_and_mutations() {
        let body = br#"{"client_id":"example-cli","target_service":"notes","purpose":"Read a note","requested_permissions":[{"action":"notes.read","resource":"alice-note"}],"expected_human_id":"alice"}"#;
        let mut headers = HeaderMap::new();
        for (name,value) in [
          ("content-type","application/json"),
          ("content-digest","sha-256=:jN6odj5Tli9ZfoLdtLxtE/sUNtMKAKM2V7xq8/qri5Y=:"),
          ("x-hg-key","11qYAYKxCrfVS_7TyWQHOg7hcvPapiMlrwIaaPcHURo"),
          ("authorization","HumanGrant anonymous"),
          ("signature-input",r#"hg=("@method" "@target-uri" "content-digest" "content-type" "x-hg-key" "authorization");created=1700000000;expires=1700000060;nonce="AAECAwQFBgcICQoLDA0ODxAREhMUFRYX";keyid="ed25519:If4x36FUomFia_hUBG_SJxt77UtqvkWqWId-9H-XIbk";alg="ed25519";tag="humangrant-0.1""#),
          ("signature","hg=:jmjuzgV58zQY0A8N+T9sss24Jwa8Kah5bvs2VSdWA43TScypSfoPkzlERC6XLBOvx8+D+ZqQ0WOVKwS2/Gd3AA==:")
        ] { headers.insert(name, value.parse().unwrap()); }
        let path: Uri = "/v1/requests".parse().unwrap();
        assert!(verify(&headers,&Method::POST,&path,body,"https://auth.example.test",1700000001).is_ok());
        assert!(verify(&headers,&Method::GET,&path,body,"https://auth.example.test",1700000001).is_err());
        assert!(verify(&headers,&Method::POST,&path,body,"https://evil.example.test",1700000001).is_err());
        assert!(verify(&headers,&Method::POST,&path,body,"https://auth.example.test",1700000070).is_err());
        assert!(verify(&headers,&Method::POST,&path,b"{}","https://auth.example.test",1700000001).is_err());
        headers.append("signature", "hg=:invalid:".parse().unwrap());
        assert!(verify(&headers,&Method::POST,&path,body,"https://auth.example.test",1700000001).is_err());
    }
}
