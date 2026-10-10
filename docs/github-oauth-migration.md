# Legacy GitHub OAuth bindings

The Go backend stores current GitHub bindings as the immutable numeric GitHub account ID. Older records may contain a mutable GitHub login name. A matching login name selects a candidate account; it does not authorize login or a binding rewrite.

## Account evidence

For a non-numeric legacy candidate, the Go OAuth callback uses the account's existing security configuration:

- If 2FA is enabled, complete the existing 2FA login challenge. A matching verified email does not bypass the factor.
- Otherwise, if the account has a Passkey, complete a challenge restricted to its enrolled credentials with user verification required. An unavailable Passkey service does not fall back to email.
- If neither factor is enrolled, a GitHub-attested verified email must match the normalized account email. The callback fetches verified emails independently of the registration email-verification setting; public profile email is insufficient.

If there is no usable proof, sign in another way and relink GitHub from account settings. An authenticated settings bind checks the numeric identity and is not blocked by an unrelated legacy username collision. Soft-deleted username candidates are ignored. Existing numeric identities retain the deleted-account guard.

Verified migration, challenge consumption, successful factor usage, unique identity claim and limited session issuance commit in one database transaction. A failed migration or session insert returns no access or refresh token. Failed session issuance also preserves an unused backup code. Successful challenges cannot be replayed or completed twice. Invalid Passkey assertions consume that challenge, preserving the existing single-attempt rule; a verified assertion whose session transaction fails remains retryable.

Success and decline use the existing operation-audit path with provider, outcome and proof method. Audit records do not include OAuth tokens or email lists. The log store remains a separate, best-effort audit destination; its writes are not part of the account/session transaction.

## Numeric ownership claims and existing data

The migration initializer backfills numeric GitHub bindings into the existing `external_identity_claims` table. Unique provider/subject and provider/user constraints arbitrate concurrent migration, registration and binding. Duplicate numeric ownership fails initialization rather than choosing an account. Resolve such a conflict only after checking the affected accounts' ownership; do not bypass the claim constraints or treat the duplicate as permission to merge accounts.

All-digit values follow the existing numeric-ID convention and are never interpreted as legacy username candidates, including zero and leading-zero strings. The current schema does not retain whether a historical digit-only value originally came from a username or a numeric ID. Backfill preserves the existing convention; it does not prove every historical record's provenance.

Legacy account verification does not enable new registration. `RegisterEnabled`, `OAuthRegisterEnabled`, per-method registration controls, legal consent and registration email-verification requirements continue to gate account creation.

## Runtime scope and validation

This behavior is implemented in `apps/lmm-extensions` and the shared web OAuth callback. The Rust preview has not implemented this verified-email/challenge migration contract. Its normal listener currently constructs federation state without enabling external providers; its separately configurable GitHub adapter and binding implementation must not be assumed to provide this Go guarantee.

Focused Go tests cover numeric and legacy lookup, soft deletion, disabled registration, real TOTP/backup-code and signed Passkey verification, failed session issuance, account changes, concurrent completion and replay. PostgreSQL qualification exercises a real multi-connection pool, competing identity claims, backup-code rollback and session limits in isolated schemas. The server release qualification workflow runs those PostgreSQL tests with the required test DSN and schema opt-in. These tests do not establish deployment, live GitHub OAuth or physical-device acceptance.

## New core migration

The former Rust backend has been removed. Rust behavior described in older
implementation notes is not evidence for the replacement. The [new core](core-migration.md)
is not business-ready; the existing Go paths remain authoritative until parity
and migration checks pass.
