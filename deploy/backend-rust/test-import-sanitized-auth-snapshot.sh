#!/usr/bin/env bash
set -Eeuo pipefail

HERE=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
SCRIPT="$HERE/import-sanitized-auth-snapshot.sh"
SCHEMA_CONTRACT="$HERE/sanitized-auth-snapshot-v1.tsv.schema"
[[ -f $SCRIPT && ! -L $SCRIPT ]] || {
  echo 'missing auth snapshot importer' >&2
  exit 1
}
[[ -f $SCHEMA_CONTRACT && ! -L $SCHEMA_CONTRACT ]] || {
  echo 'missing snapshot schema contract' >&2
  exit 1
}
bash -n "$SCRIPT"

tmp=$(mktemp -d "${TMPDIR:-/tmp}/lmm-auth-snapshot-test.XXXXXXXX")
trap 'rm -rf -- "$tmp"' EXIT
mkdir -p "$tmp/bin"
cat >"$tmp/bin/psql" <<'EOF'
#!/usr/bin/env bash
[[ ${1:-} == postgres://destination-only ]] || { echo 'psql received an unexpected DATABASE_URL' >&2; exit 92; }
shift
if [[ $* == *'current_database()'* ]]; then printf 'lmm_test_auth|lmm_test_auth|t|f|t\n'; exit 0; fi
echo 'psql must not be reached during dry-run' >&2; exit 91
EOF
chmod 0755 "$tmp/bin/psql"

# The literal below is a synthetic bcrypt verifier.
# shellcheck disable=SC2016
hash='$2b$12$aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa' # nosemgrep: generic.secrets.security.detected-bcrypt-hash.detected-bcrypt-hash
cat >"$tmp/snapshot.tsv" <<EOF
id	username	password_bcrypt	display_name	role	status	group	quota	used_quota	request_count	auth_version
7	test-admin	$hash	Test Admin	10	1	default	1000	0	0	1
EOF
printf '7\n' >"$tmp/allowlist.txt"
openssl genpkey -algorithm RSA -pkeyopt rsa_keygen_bits:2048 -out "$tmp/private.pem" >/dev/null 2>&1
openssl pkey -in "$tmp/private.pem" -pubout -out "$tmp/public.pem" >/dev/null 2>&1
openssl dgst -sha256 -sign "$tmp/private.pem" -out "$tmp/snapshot.sig" "$tmp/snapshot.tsv"
sha256sum "$tmp/snapshot.tsv" | awk '{print $1}' >"$tmp/snapshot.sha256"
chmod 0600 "$tmp/snapshot.tsv" "$tmp/allowlist.txt" "$tmp/snapshot.sig" "$tmp/snapshot.sha256"
chmod 0644 "$tmp/public.pem"

args=(--schema lmm_test_auth --expected-database lmm_test_auth --expected-role lmm_test_auth
  --snapshot "$tmp/snapshot.tsv" --sha256 "$tmp/snapshot.sha256" --signature "$tmp/snapshot.sig"
  --public-key "$tmp/public.pem" --allow-user-ids "$tmp/allowlist.txt" --dry-run)

if PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}" >/dev/null 2>&1; then
  echo 'missing test-instance guard unexpectedly succeeded' >&2
  exit 1
fi
output=$(LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}")
grep -Fxq 'DRY_RUN schema=lmm_test_auth database=lmm_test_auth role=lmm_test_auth users=1 source=offline-signed-snapshot credentials=redacted' <<<"$output"
if grep -Fq "$hash" <<<"$output"; then
  echo 'password verifier leaked in importer output' >&2
  exit 1
fi

printf '8\n' >"$tmp/allowlist.txt"
if LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}" >/dev/null 2>&1; then
  echo 'allowlist mismatch unexpectedly succeeded' >&2
  exit 1
fi
printf '7\n' >"$tmp/allowlist.txt"
chmod 0644 "$tmp/snapshot.sig"
if LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}" >/dev/null 2>&1; then
  echo 'non-0600 signature unexpectedly succeeded' >&2
  exit 1
fi
chmod 0600 "$tmp/snapshot.sig"
printf '0000000000000000000000000000000000000000000000000000000000000000\n' >"$tmp/snapshot.sha256"
if LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}" >/dev/null 2>&1; then
  echo 'checksum mismatch unexpectedly succeeded' >&2
  exit 1
fi
sha256sum "$tmp/snapshot.tsv" | awk '{print $1}' >"$tmp/snapshot.sha256"
printf 'not a detached signature\n' >"$tmp/snapshot.sig"
if LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" "${args[@]}" >/dev/null 2>&1; then
  echo 'signature verification failure unexpectedly succeeded' >&2
  exit 1
fi
if LMM_RS_TEST_INSTANCE=1 LMM_SANITIZED_AUTH_IMPORT_TEST_ALLOW_NONROOT=1 PATH="$tmp/bin:$PATH" DATABASE_URL='postgres://destination-only' "$SCRIPT" --source-database-url postgres://production.invalid "${args[@]}" >/dev/null 2>&1; then
  echo 'source database option unexpectedly succeeded' >&2
  exit 1
fi

grep -Fq 'never reads a source database' "$SCRIPT"
grep -Fq 'snapshot signature verification failed' "$SCRIPT"
grep -Fq 'credentials=redacted' "$SCRIPT"
grep -Fq 'access_token = NULL' "$SCRIPT"
grep -Fq "'@invalid.test'" "$SCRIPT"
grep -Fq "'RegisterEnabled','false'" "$SCRIPT"
grep -Fq "'TurnstileCheckEnabled','false'" "$SCRIPT"
grep -Fq "'payment_setting'" "$SCRIPT"
grep -Fq "'EpayEnabled','false'" "$SCRIPT"
grep -Fq 'target options are not the empty sanitized-schema baseline' "$SCRIPT"
grep -Fq 'test-only option allowlist' "$SCRIPT"
grep -Fq 'user_sessions' "$SCRIPT"
grep -Fq 'setval' "$SCRIPT"
grep -Fq 'password_bcrypt' "$SCHEMA_CONTRACT"
grep -Fq 'plaintext passwords are forbidden' "$SCHEMA_CONTRACT"
echo 'sanitized auth snapshot importer static guards verified'
