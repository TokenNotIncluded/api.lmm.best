# 2026-09-20 recovery: source archive and retirement

This is a historical record, not an upgrade procedure or deployment approval.
It snapshots the written incident state recorded in issues #386 and #395 at
retirement. It does not independently attest to a currently running production
version, and no production connection or recovery command was used for cleanup.

## Why the emergency helper existed

The signed Go 0.2.52 to 0.2.55 upgrade failed after candidate migration but before
package replacement. The candidate added the built-in admin acquisition read
and write grants. The retained 0.2.52 verifier rejected that newer authorization
baseline; the managed-billing gate also prevented ordinary native rollback.
The original provider package was still installed.

The one-off helper was bound to deployment
`manual-go-v0.2.55-20260919T172635Z`. It required root on the expected host, the
exact candidate revision and rollback archive/package identities, unchanged
frontend identity, verified stopped-writer state, closed admission, matching
shutdown journal digest, transaction locks and signed rollback archives. It
saved the two exact authorization rows before a bounded migration-locked repair,
then verified and restarted the unchanged provider, checked health and admission,
and finalized the transaction. It was not a package installer or a supported
way to skip general rollback checks.

Issue #386 records the original provider restored and both failed transactions
terminal `ROLLED_BACK`. Its recorded backend was 0.2.52; the retained active web
build was 0.1.77-menu, distinct from installed web package metadata 0.1.81-1.
These are incident-record facts, not a live status check performed by this PR.

## Immutable source evidence

The implementation was introduced in commit
`92c8819ad4f692f564775d41881cf4d31704ae4d` and adjusted in
`8ee77b9f50360704a87b0e36484ef2a3b0ac4934`. All three retired files remain
reconstructible in Git history at
`fa2e0db9e62a6e59302a283081b1a2eddeb2c2b9`:

| Former path | Git blob SHA-1 |
| --- | --- |
| `apps/api-go/cmd/incident20260920-recovery/main.go` | `93cfadd54d27a29fc0f97000188d877f61389281` |
| `apps/api-go/internal/appcli/incident20260920_recovery_linux.go` | `c85ff5c0f726987b7704f70897352f152cd44145` |
| `apps/api-go/internal/appcli/incident20260920_recovery_linux_test.go` | `86b6e1cebec89b662e3dd46514dcd4c01ecb8385` |

The source binds candidate revision
`ae5bf90d7bf3cc97a8a1cb5d3a69ffec4b592267` and rollback archive SHA-256
`b957626852cd85c83690352f177e358f4bff7f6953d5b3ef6ddf8203c09daf46`.
Git blob hashes identify source bytes; they are not substitutes for production
artifact signatures, manifests, database snapshots or journal evidence. None
of those sensitive operational artifacts is copied into this document. Cleanup
does not delete operator-held transaction evidence or rewrite Git history.

## Retirement boundary

The dated executable, implementation and incident-only tests are removed from
normal Go build/test/package scope. No recovery program replaces them. Shared
production-runtime helpers, controller tests, authorization verification,
financial-data protection, signatures and health gates are unchanged.

The general versioned authorization-baseline upgrade/rollback work remains in
#386; managed-billing rollback qualification remains in #391. This cleanup does
not close either requirement or prove a safe direct upgrade. Those tasks still
need their own compatibility evidence and isolated database rehearsal. No new
production deployment is authorized by this archive or by the removal.
