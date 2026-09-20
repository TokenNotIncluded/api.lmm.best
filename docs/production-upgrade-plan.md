# Production upgrade plan (draft, pending manual approval)

Status: **not approved for deployment**. This document consolidates the
current evidence for upgrading the live backend away from the retained
signed `go-v0.2.52` executable, and states exactly what is proven versus
still outstanding. It supersedes no safety gate; every native check
described in [`go052-authorization-bridge.md`](./go052-authorization-bridge.md)
still runs at upgrade/rollback time regardless of this plan.

## 1. Why an upgrade is blocked today

A prior direct upgrade attempt (`#386`) failed after candidate migration:
the retained 0.2.52 rollback executable rejected the newer built-in
`role:admin/acquisition/read|write/allow` authorization rows, and native
rollback was separately refused by the managed-billing guard even though
the original provider package was still installed. The original provider
was restored; both transactions are terminal `ROLLED_BACK`. Live backend
remains `0.2.52`; active frontend remains the retained `0.1.77-menu` build
(installed Arch Web package metadata `0.1.81-1`).

## 2. What has been fixed and verified since

| Gate | Issue / PR | State | Verification |
|---|---|---|---|
| Authorization baseline compatibility (unknown-resource admin defaults) | #386 code fix via #392 | Merged (`b4265c28e`) | Isolated snapshot rehearsal, see §3 |
| Bridge source (0.2.52 business logic + current native CLI/tooling) | #392 | Merged | Same rehearsal |
| Ali image task polling bound + cancellation | #389 | Merged (#398) | Unit tests, CI |
| Assistant chat body limit (Rust vs Go) | #388 | Merged (#396) | Unit tests, CI |
| Legacy CLI bootstrap path | #387 | Merged | CI |
| Managed-billing rollback gate qualified by verified capability marker | #391 | Merged (#406), just now | Unit tests + CI only — **not yet exercised in the isolated DB rehearsal** |

Unrelated PR backlog also cleared this pass (not on the upgrade critical
path, included for completeness): relaykit null/empty-choice handling
(#402), multi-image Ali choices (#403), dialog viewport clipping (#407),
upstream-model-removal confirmation safety (#404).

## 3. What the isolated rehearsal already proved (2026-09-19)

Recorded in detail in `go052-authorization-bridge.md`. Summary:

- An encrypted production snapshot (captured 2026-09-19 17:27 UTC) was
  decrypted and restored into a private PostgreSQL 18 + Valkey cluster,
  entirely inside a loopback-only network namespace. No production
  credentials, database connection, or outbound traffic were used.
- The actual retained 0.2.52 executable (SHA-256
  `24d89d25b1e0ad56e9b7601b664e8d3f57c1a32fbdffa09d73b79a64be8233ea`) was
  used for every "original" step, not a rebuilt substitute.
- Negative control reproduced #386 exactly: original verify -> candidate
  migrate/verify passed, then the *original* rollback executable rejected
  the new baseline.
- Positive control: original -> bridge migrate/verify -> original rollback
  verify passed; bridge -> main migrate/verify -> bridge rollback verify
  passed; candidate migration replay passed.
- All 126 pre-existing tables were compared by sorted row hashes over
  their original columns. Only `casbin_rule` changed. Business rows and
  user-specific permission rows (including IDs) were byte-for-byte
  unchanged.

This proves the **authorization/migration** half of the upgrade path end
to end against real data. It does **not** cover the managed-billing
capability gate, because that fix did not exist yet on 2026-09-19.

## 4. What is still outstanding before any deployment decision

1. **Re-run (or extend) the isolated rehearsal to cover `#391`/#406.**
   The new `MANAGED_BILLING_SETTLEMENT_CAPABILITY` marker has full unit
   coverage (`TestProductionBillingGateBlocksManagedBillingRollbackBeforeWriterStop`,
   `TestManagedBillingSettlementCapabilityMarkerProtocol`) and passing CI,
   but no package has ever actually been built with the marker embedded,
   and no rollback has been attempted against real `subscription_pre_consume_records`
   rows with `billing_managed=true` in an isolated environment. This is
   the same class of evidence §3 already produced for the authz gate and
   should be produced the same way before the billing gate is trusted for
   a real rollback decision.
2. **Frontend/package drift reconciliation.** Live frontend
   (`0.1.77-menu`, package metadata `0.1.81-1`) and backend (`0.2.52`)
   versions must be reconciled through the signed native frontend path
   and tested against both backend versions, as already called out in
   `go052-authorization-bridge.md` §"Not yet a complete deployment
   approval".
3. **Final combined-source qualification** of the bridge -> main path as
   a single rollout (not just the two isolated migrate/verify legs
   already tested).
4. **Owner manual approval of the concrete rollout plan** (this document,
   once the above are closed).

## 5. Recommended path once §4 is closed

Two explicit steps, matching the bridge document's stated boundary — do
not attempt a single direct 0.2.52 -> main deploy:

1. `0.2.52` -> bridge release: preserves the original authorization
   baseline and business logic, adds current native CLI/tooling only.
   Native rollback to real 0.2.52 must stay verified at this step.
2. bridge -> current `main`: accepts the newer authorization defaults.
   Native rollback to the bridge must stay verified at this step,
   including under live managed-billing rows now that #406 is merged.

Both steps go through the existing native package transaction manager
(`apps/api-go/internal/appcli`) exactly as today — no operator bypass
flags, no version-string shortcuts. Each step's rollback path must be
re-verified against the actual signed artifact for that step, not a
locally rebuilt stand-in, before it is treated as safe.

## 6. Explicit non-goals of this document

- This document does **not** authorize any deployment. No release or
  production deployment has been performed as part of producing it.
- It does not weaken, bypass, or replace any existing verification,
  signature, billing-protection, or health-check gate.
- Backlog issues unrelated to the upgrade path (#393, #395, #384, #385,
  #397 draft PR #401, and the remaining feature/tracking issues) are not
  blocking and are left for normal iteration.
