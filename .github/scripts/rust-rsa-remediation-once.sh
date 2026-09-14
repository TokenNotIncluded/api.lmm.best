#!/usr/bin/env bash
set -euo pipefail

if grep -q 'use ring::{rand::SystemRandom, rsa, signature};' apps/api-rust/src/routes/system_config.rs \
  && grep -q 'features = \["aws_lc_rs"\]' apps/api-rust/Cargo.toml; then
  echo 'RSA remediation already applied'
  exit 0
fi

python3 - <<'PY'
from pathlib import Path
import re

cargo = Path('apps/api-rust/Cargo.toml')
text = cargo.read_text()
text, n = re.subn(
    r'jsonwebtoken = \{ version = "[^"]+", default-features = false, features = \["rust_crypto"\] \}',
    'jsonwebtoken = { version = "10.4.0", default-features = false, features = ["aws_lc_rs"] }',
    text,
    count=1,
)
if n != 1:
    raise SystemExit(f'jsonwebtoken backend replacement count={n}')
text, n = re.subn(
    r'rust_decimal = \{ version = "[^"]+", default-features = false, features = \["std"\] \}',
    'rust_decimal = { version = "1.43.0", default-features = false, features = ["std"] }',
    text,
    count=1,
)
if n != 1:
    raise SystemExit(f'rust_decimal replacement count={n}')
text, n = re.subn(
    r'sha2 = "[^"]+"',
    'sha2 = { version = "0.10.9", features = ["oid"] }',
    text,
    count=1,
)
if n != 1:
    raise SystemExit(f'sha2 OID feature replacement count={n}')
if 'ring = "0.17.14"' not in text:
    text = text.replace(
        'redis = { version = "0.32.4", features = ["tokio-comp"] }\n',
        'redis = { version = "0.32.4", features = ["tokio-comp"] }\nring = "0.17.14"\n',
        1,
    )
if 'ring.workspace = true' not in text:
    text = text.replace('reqwest.workspace = true\n', 'reqwest.workspace = true\nring.workspace = true\n', 1)
cargo.write_text(text)

system = Path('apps/api-rust/src/routes/system_config.rs')
text = system.read_text()
text, n = re.subn(
    r'use rsa::\{\n\s*RsaPrivateKey,\n\s*pkcs1::DecodeRsaPrivateKey,\n\s*pkcs1v15::SigningKey,\n\s*pkcs8::DecodePrivateKey,\n\s*signature::\{SignatureEncoding as _, Signer as _\},\n\};',
    'use ring::{rand::SystemRandom, rsa, signature};',
    text,
    count=1,
)
if n != 1:
    raise SystemExit(f'rsa signing import replacement count={n}')

parser = r'''fn parse_pancake_private_key(raw: &str) -> Result<rsa::KeyPair, ()> {
    let normalized = raw.replace("\\n", "\n").replace("\r\n", "\n");
    let normalized = normalized.trim();
    if normalized.is_empty() {
        return Err(());
    }

    fn pem_der(input: &str, label: &str) -> Result<Vec<u8>, ()> {
        let begin = format!("-----BEGIN {label}-----");
        let end = format!("-----END {label}-----");
        let start = input.find(&begin).ok_or(())? + begin.len();
        let finish = input[start..].find(&end).ok_or(())? + start;
        let encoded: String = input[start..finish]
            .chars()
            .filter(|ch| !ch.is_ascii_whitespace())
            .collect();
        BASE64.decode(encoded.as_bytes()).map_err(|_| ())
    }

    if normalized.contains("-----BEGIN RSA PRIVATE KEY-----") {
        // gitleaks:allow -- PEM boundary marker
        let der = pem_der(normalized, "RSA PRIVATE KEY")?;
        return rsa::KeyPair::from_der(&der).map_err(|_| ());
    }
    if normalized.contains("-----BEGIN PRIVATE KEY-----") {
        // gitleaks:allow -- PEM boundary marker
        let der = pem_der(normalized, "PRIVATE KEY")?;
        return rsa::KeyPair::from_pkcs8(&der).map_err(|_| ());
    }
    let der = BASE64.decode(normalized).map_err(|_| ())?;
    rsa::KeyPair::from_pkcs8(&der)
        .or_else(|_| rsa::KeyPair::from_der(&der))
        .map_err(|_| ())
}'''
text, n = re.subn(
    r'fn parse_pancake_private_key\(raw: &str\) -> Result<RsaPrivateKey, \(\)> \{.*?\n\}\n\nfn normalize_pancake_catalog',
    lambda _: parser + '\n\nfn normalize_pancake_catalog',
    text,
    count=1,
    flags=re.S,
)
if n != 1:
    raise SystemExit(f'private-key parser replacement count={n}')

signer = r'''fn pancake_signature(
    path: &str,
    timestamp: &str,
    body: &[u8],
    private_key: &rsa::KeyPair,
) -> Result<String, ()> {
    let body_hash = BASE64.encode(Sha256::digest(body));
    let canonical = format!("POST\n{path}\n{timestamp}\n{body_hash}");
    let rng = SystemRandom::new();
    let mut output = vec![0_u8; private_key.public().modulus_len()];
    private_key
        .sign(
            &signature::RSA_PKCS1_SHA256,
            &rng,
            canonical.as_bytes(),
            &mut output,
        )
        .map_err(|_| ())?;
    Ok(BASE64.encode(output))
}'''
text, n = re.subn(
    r'fn pancake_signature\(.*?\n\}\n\nfn pancake_idempotency_key',
    lambda _: signer + '\n\nfn pancake_idempotency_key',
    text,
    count=1,
    flags=re.S,
)
if n != 1:
    raise SystemExit(f'signer replacement count={n}')
system.write_text(text)

# `main` currently carries this identity map as pre-existing lint debt. The
# strict remediation gate intentionally runs the repository's documented
# `lint:rust` command, so remove the no-op adapter rather than weakening Clippy.
sms = Path('apps/api-rust/src/routes/hero_sms/sms.rs')
text = sms.read_text()
old = '''    let user = authenticated(state, headers)\n        .await\n        .map_err(|response| response)?;'''
new = '''    let user = authenticated(state, headers).await?;'''
if old not in text:
    raise SystemExit('HeroSMS inherited identity map not found')
sms.write_text(text.replace(old, new, 1))

workflow = Path('.github/workflows/rust-security-audit.yml')
text = workflow.read_text()
start_marker = '          # RUSTSEC-2026-0235 currently has no resolved workspace path:'
end_marker = '          ignore: RUSTSEC-2026-0235\n'
start = text.find(start_marker)
end = text.find(end_marker, start)
if start < 0 or end < 0:
    raise SystemExit('old RustSec exception block not found')
end += len(end_marker)
replacement = '''          # RUSTSEC-2023-0071 affects RSA private-key operations. After #276's
          # first remediation slice, Pancake private-key signing uses ring and
          # jsonwebtoken uses aws-lc-rs; the remaining resolved `rsa` use is
          # public-key webhook verification only. Keep this exact exception
          # only until that verification path also migrates and `rsa` leaves
          # the lockfile. #276 tracks removal.
          ignore: RUSTSEC-2023-0071
'''
workflow.write_text(text[:start] + replacement + text[end:])
PY

cd apps/api-rust
cargo update -p jsonwebtoken --precise 10.4.0
cargo update -p rust_decimal --precise 1.43.0

# Private signing and jsonwebtoken must no longer account for resolved rsa paths.
cargo tree --locked -i rsa
if cargo tree --locked --target all -i rkyv >/tmp/rkyv-tree 2>&1; then
  cat /tmp/rkyv-tree
  if grep -q '^rkyv ' /tmp/rkyv-tree; then
    echo 'rkyv remains resolved after rust_decimal upgrade' >&2
    exit 1
  fi
fi

cargo fmt --all --check
cargo check --workspace --all-targets --all-features --locked
cargo test --locked routes::system_config -- --nocapture
cargo test --locked routes::waffo_webhooks -- --nocapture
cargo install cargo-audit --locked --version 0.22.1
cargo audit --ignore RUSTSEC-2023-0071
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
cargo test --workspace --all-targets --all-features --locked
cargo test --workspace --doc --all-features --locked
cd ../..

git add apps/api-rust/Cargo.toml apps/api-rust/Cargo.lock apps/api-rust/src/routes/system_config.rs apps/api-rust/src/routes/hero_sms/sms.rs .github/workflows/rust-security-audit.yml
git diff --cached --check
git config user.name 'github-actions[bot]'
git config user.email '41898282+github-actions[bot]@users.noreply.github.com'
git commit -m 'fix(rust): remove network RSA private signing exposure'
git push origin HEAD:codex/rust-rustsec-audit
