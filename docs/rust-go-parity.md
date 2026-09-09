# Rust / Go backend parity work

Baseline: `74384a66939cacb7d74a78ca3c7aa035900d1b24` (2026-09-09).
The Go source in this revision is the implementation reference for this patch;
it is **not** the independent frozen production oracle. Production ownership
continues to follow [the provider rollout contract](rust-blue-green.md).

## Configuration write compatibility

The Rust generic configuration surface now shares one filtering and validation
path for `PUT /api/option/`, `POST /api/option/bulk`,
`POST /api/option/validate`, and the existing model-ratio reset handler.

- `ModelPriceLock` uses the Go boolean-map representation. No migration is needed;
  an absent saved map means all models are unlocked.
- Locked entries retain their persisted values or absence in all ten Go pricing
  maps, including billing mode/expression maps. Filtering precedes entry-value
  validation, so invalid attempted edits to locked prices are also ignored.
- The five normalized pricing maps reuse the existing legacy model-name
  normalizer; cache, image and billing maps use exact names, matching Go.
- A batch that unlocks and edits a model still preserves the old price. A later
  request can edit the unlocked model. Unlocked models in the same batch save.
- Responses include sorted, deduplicated `warnings` while retaining `success:
  true` for skipped edits. Numerically equal prices such as `1` and `1.0` do not
  warn. Malformed lock maps and invalid editable price values fail validation.
- The lock decision uses PostgreSQL, never a potentially stale Valkey snapshot.
  The process write mutex covers the read, filter, validation and existing
  durable/runtime/cache update sequence. Dry runs do not write or refill caches.
- Generic batch and validation endpoints reject compliance metadata edits and
  removed frontend themes using the same rules as the single-write endpoint.
  The dedicated compliance confirmation endpoint retains its existing behavior.

Reference implementations: `apps/api-go/model/option_price_lock.go` and
`apps/api-go/controller/option.go`. This change does not advertise a new frontend
capability or transfer any production route ownership.

## Remaining migration blockers

This is a source review, not an end-to-end certification. At the baseline the
route gate lists 353 routes, all owned by Go: 349 candidate-surface routes have
unverified differentials and four are unmounted. These fixture mount states do
not imply production-root composition: the route checker separately reports 61
candidates unmounted from the production root. Mounted candidates are not
completed implementations merely because they answer HTTP requests.

| Area | Evidence / remaining work |
| --- | --- |
| Runtime composition | Inspect every dependency in `apps/api-rust/src/main.rs`; the listener still composes disabled payment checkout/verifier/processor implementations. Wire real dependencies only with persistence and failure-path tests. |
| Pancake lifecycle | RSA verification and lifecycle/payment/refund dispatch now have implementations and regression tests. The durable processor is still disabled: order/period association, out-of-order delivery, idempotent settlement and refunds must be implemented and independently verified before enabling the gateway. |
| Global pricing protection | This patch covers the system-config surface. Audit assistant, scheduled pricing, import/sync and independent writers. Cross-process Go/Rust concurrent writes need a shared database transaction/locking contract; a Rust process mutex alone does not provide that guarantee. |
| Pending frontend contract | Open PR #230 contains additional price-lock capability and per-model update contracts. Reconcile its final merged behavior before enabling Rust price-lock controls in the UI. |
| Reset defaults | This patch preserves the existing Rust reset target (`{}`). Compare it with Go's built-in default ratio catalogue before claiming reset response/data parity. |
| Authentication and money | Run the existing PostgreSQL/Valkey and independent Go differential suites for sessions, revocation, permissions, quota, ledger and streaming cancellation; route coverage alone is insufficient. |
| Cutover / rollback | Retain per-route approvals, N/N-1 schema compatibility, singleton background-job ownership and manual signed-provider rollback rehearsal. |

## Verification commands

```bash
cd apps/api-rust
cargo test --locked --lib routes::system_config
cargo test --locked --test system_config
# Dedicated disposable PostgreSQL 18 and Valkey instances only:
LMM_SYSTEM_CONFIG_TEST_DATABASE_URL=... LMM_SYSTEM_CONFIG_TEST_VALKEY_URL=... \
  cargo test --locked --test system_config -- --ignored --test-threads=1
```

The ignored integration regression checks authoritative lock reads despite a
stale unlocked cache, dry-run immutability, filtered runtime/DB values, mixed
updates, warnings and separate-request unlock behavior. It does not substitute
for the frozen Go differential oracle or approve production migration.

Local verification for this patch (Rust 1.91.0, incremental compilation disabled,
`CARGO_BUILD_JOBS=2`, dev/test debug information disabled): configuration module
unit tests passed (20); system-config HTTP tests passed (12), with both dedicated
PostgreSQL/Valkey tests ignored. Targeted Clippy (`--lib --test system_config --
-D warnings`), changed-file rustfmt checks, `git diff --check` and
`check-route-plan.sh` passed. PostgreSQL 18 could not be provisioned in this
execution environment, so no database integration or frozen-Go differential
pass is claimed. An initial incremental test link failed; the repository's CI
setting `CARGO_INCREMENTAL=0` produced the passing test run.


## Pancake verification and lifecycle boundary

The normal listener now composes the RSA-SHA256 verifier, with public keys pinned
from Pancake Go SDK v0.9.0. It verifies the exact timestamp-prefixed raw body,
retains the provider retry window (45 minutes) and limits future clock skew to
60 seconds. Go environment overrides are supported with PEM, escaped PEM and
raw base64 SPKI keys; parsing is local and errors never disclose key values.
The loopback acceptance listener retains the deny verifier.

The signed mode is bound to its own key; duplicate signature fields are rejected.
These deliberate security differences from SDK auto-detection are recorded in
the behavior deviation ledger and have no production approval credit.

Payment callbacks may omit period fields following the 2026-09-06 change.
Verified event/payment/order IDs and optional lifecycle boundaries are preserved
separately. Contradictory status fields never reach the durable processor.
Recognized lifecycle, payment and refund events reach an explicit processor
method; an incomplete adapter returns retry rather than acknowledging lost
payment evidence. Unknown event types remain acknowledgements without mutation.

**Availability and durable settlement remain disabled in the normal listener.**
This verifier does not itself grant entitlements, settle funds, validate the
merchant/order's amount, or establish production readiness.

The existing PostgreSQL 18 / Valkey CI job now runs the previously ignored
system-config tests explicitly via the integration runner. Missing environment
variables remain hard errors, not successful skipped verification.

Verification for the webhook boundary: 18 Rust webhook tests passed; targeted
Clippy for the library, production binary and system-config test passed. The
same OpenSSL fixtures passed the pinned Go SDK oracle:

```bash
cd apps/api-rust/tests/pancake-sdk-oracle
go test -mod=readonly -v ./...
```

The fixed historical signature time is checked with a test clock in Rust; the
SDK fixture oracle disables its wall-clock age check to verify the same fixed
cryptographic bytes. This is protocol evidence, not a payment-settlement or
whole-backend differential pass.

## Weekly session age

Access validation, refresh and security-proof operations now check the original
session creation time against seven days. Missing or malformed preferences
default to enabled; only a boolean `session_auto_logout: false` opts out.
For aged sessions the adapter locks the user and session, rereads the preference,
writes the shared Valkey revoking fence, then commits PostgreSQL revocation.
Refresh and recent activity do not reset creation time. The integration gate
covers independent access/refresh rejection, explicit opt-out, unchanged login
age and the shared cache tombstone. Session-list preferences, settings writes
and periodic cleanup remain separate parity gaps.

PR #239 initial commit `7f3e49a` passed the PostgreSQL 18 / Valkey CI job,
including the two system-config integration regressions and pinned Go SDK
signature verification (Actions run `34331195212`). This does not certify
payment settlement or authorize route ownership changes.

## Migration gate integrity

CI log review found an obsolete exact test filter selecting zero tests while
Cargo returned success. The subscription-reset runner now invokes the existing
contract-six schema verifier. Exact migration gates check the compiled ignored
test list before execution and fail if the requested test is absent. A stubbed
zero-test success regression protects this behavior without a database.
The full-copy migration/fault rollback test and contract-eight payment replay
schema test are now explicitly included in the real-dependency runner. Their
results must be checked on the new commit; earlier green jobs did not run them.
