# Controller-only production backup evidence

This change replaces automatic target/controller/off-host backup creation for new release plans with an explicitly selected controller-owned import. Native source streaming is not part of this change. An independently authorized source-encrypted collection must finish before import. Target hosts receive only signed verification receipts; private keys, archives, dumps and ciphertext never leave the controller during import/stage/confirm.

## Modes and compatibility

New release plans use format 6 and `backup_mode` equal to `disabled` or `controller-only`. `with_backups` must agree with the mode. Disabled is valid for Go-only, Web-only and combined changes and must have no backup paths, keys or evidence; it performs no backup commands. Controller-only requires `controller_backup_dir` (absolute, private, no symlink components) and `controller_backup_public_key` (32-byte Ed25519 key, lowercase hex). Planning creates/reuses a private 64-byte Ed25519 signing key at `<workspace>/state/controller-backup.key`; the key is never staged. `--age-identity-file` remains a controller-only runtime argument, never a manifest value.

Format 5 plans and evidence formats 0/2 remain readable for already-created transactions/recovery. New plans must not silently select legacy target/off-host replication. Go and Rust must both read new production evidence and reject malformed mixed modes. Rust controller-side operations remain explicitly unsupported; no shell fallback is introduced.

Production manifest/status formats stay 8/2. New selected evidence uses `backup_evidence_format: 3`, `backups_enabled: true`, empty `backup_dir`, `target_backup_sha256` and `offhost_backup_sha256`. `controller_backup_sha256` is the imported backup-set.json digest; `database_backup_sha256` is the decrypted custom PostgreSQL archive digest. A new optional `controller_only_backup` object contains `public_key`, `plan_sha256`, `receipt_path`, `receipt_sha256`. Receipt path is exactly `<workspace>/state/controller-backup-receipt.json`. Disabled manifests have no such object/evidence. Retained rollback packages and small configuration rollback state remain independent of optional backup mode.

## Imported collection

A complete import directory contains exactly `backup-set.json`, `.complete`, `application.age`, `frontend.age`, `configuration.age`, `database.age`. Directory mode is 0700; members are private nonempty single-link regular files, owned by the invoking controller user; no symlink component is accepted. Unknown members, partials and keys are rejected. `.complete` contains the lowercase SHA-256 of the exact `backup-set.json` bytes plus a newline.

`backup-set.json` (strict JSON, no unknown/duplicate fields) has:

- `format`: 1
- `deployment_id`, `expected_host` (`arch-dmit`), `captured_utc` (RFC3339 UTC)
- `database_schema`, `environment_sha256`
- `go_rollback_sha256`, `web_rollback_sha256` (exact N-1 package digests)
- `go_rollback_payload_sha256`, `frontend_rollback_sha256`
- `archives`: exactly four keys `application`, `frontend`, `configuration`, `database`, each with `ciphertext_sha256`, `plaintext_sha256`, `ciphertext_bytes`, `plaintext_bytes`.

The collection belongs to the current deployment ID, not a relabelled old transaction. Its package/payload/index identities must match the frozen plan. Its schema must be a valid explicit PostgreSQL schema; its capture time cannot be future (>30s) or older than 24 hours. The final receipt reports the actual snapshot time, not a claim that decrypting old bytes creates a new snapshot.

Decrypt each member sequentially using age into a unique private workspace scratch directory. Enforce complete successful decryption/EOF, declared lengths and plaintext/ciphertext hashes. Never include age/PG stderr or secret content in errors. Remove successful plaintext scratch on exit; failed encrypted originals are not deleted. Full tar traversal is required for application/frontend/configuration (plain tar or gzip tar), with limits, unique canonical relative paths, no links/devices/traversal, and complete gzip checksum/EOF. Required regular members: application `usr/bin/lmm-api-go` matching N-1 provider payload; frontend `index.html` matching N-1 index; configuration `etc/lmm-api-go/lmm-api-go.env` matching `environment_sha256`. Validate configuration syntax with the existing non-evaluating parser and PostgreSQL authority. Validate the entire decrypted custom PostgreSQL archive with `pg_restore --file=/dev/null`, not only `--list`; no production database is contacted. No archive extraction or full plaintext upload is permitted.

## Signed receipt

The envelope is strict JSON `{ "format": 1, "payload": "<base64 raw JSON>", "signature": "<128 lowercase hex>" }`, capped at 64 KiB. Keys and signature points must be canonical, non-small-order Ed25519 points; required fields cannot be missing, null, duplicated or differently cased. Ed25519 signs the exact decoded payload bytes; consumers never reserialize them to verify. Payload fields are:

- `format`: 1, `purpose`: `prepare` or `confirm`
- `deployment_id`, `expected_host`, `plan_sha256`
- `verified_utc`, `captured_utc`
- `deployment_manifest_sha256`: empty for prepare, actual immutable manifest SHA for confirm
- `backup_set_sha256`, `database_schema`, `environment_sha256`
- `go_candidate_sha256`, `go_rollback_sha256`, `web_candidate_sha256`, `web_rollback_sha256`
- `go_rollback_payload_sha256`, `frontend_rollback_sha256`
- `archive_ciphertexts`, `archive_plaintexts`: exactly the four archive-kind-to-digest mappings above.

The controller publishes receipts from private temporary files. On the target, it verifies a unique root-owned `0600`, single-link `.partial` file before renaming it to the receipt path. Initial publication uses no-clobber semantics; confirmation may replace only the small confirmation receipt. A failed transfer leaves its partial metadata for audit and retries with a new temporary name. It does not leave incomplete bytes at the immutable receipt path.

The public key is fixed in the immutable release plan before receipt creation. The trusted controller dispatch carries that key and the plan SHA to the native target transaction. The target checks signature, exact deployment/host/plan, all package identities and schema/environment/index bindings against its verified live N-1/retained configuration state. Prepare verification age must be strictly below five minutes (allow <=30s future skew). The initial receipt is immutable, hashed into the deployment manifest, and never rewritten during confirm. Subsequent archive-evidence checks verify its signature/bindings/digest but do not expire the historical prepare timestamp.

Confirmation deep-verifies the original controller collection again, signs a fresh `purpose=confirm` receipt binding the exact immutable deployment-manifest SHA, and stages only `<workspace>/state/controller-backup-confirmation.json`. Target Go/Rust confirm verifies the same frozen public key and immutable bindings with verification age strictly below five minutes before becoming CONFIRMED. The snapshot must still be within 24 hours; re-verification does not renew its capture time. This prevents checksum-only claims and timestamp refresh without renewed controller verification. No circular digest exists: prepare payload binds the preexisting release-plan SHA; deployment manifest binds prepare receipt SHA; fresh confirm payload then binds deployment manifest SHA.

## Failure and recovery

Reject missing evidence, corrupt/truncated age or archive data, identity/schema/payload mismatch, forged/replayed signatures, cross-transaction receipts, stale confirmations, and unknown/mixed evidence before activation/confirmation. Do not create a production backup tree or contact an off-host. Post-mutation errors retain ROLLBACK_REQUIRED/locks and do not automatically roll back. Rollback uses exact N-1 packages and retained configuration without requiring controller availability, candidate bytes or backup receipts. The controller uses `/usr/bin/lmm-api` for recovery when its one-hop provider target and both path hashes match the frozen candidate package payload. Otherwise it requires the verified release-scoped operator. It refuses an unverified installed N-1 executable. Old binaries may reject the new evidence format; retain/re-stage a verified compatible operator for recovery rather than pretending Go 0.2.13 can read it. A Web-only plan that keeps an older unsupported Go CLI cannot use format-3 selected backups until a compatible provider has been released and validated.

An expired initial receipt stops preparation before decryption or SSH. Create a new deployment ID and collect evidence bound to that ID; the controller does not renew or relabel an immutable initial receipt in place. A missing persisted receipt requires restoration of the original evidence or a new transaction.
