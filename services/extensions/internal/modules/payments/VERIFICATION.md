# Verification record — durable proof WIP, 2026-10-12

The current change moves proof acceptance before callback ACK and preserves a
Rust-issued proof reference through Go inbox and jobs. Go no longer holds Stripe
webhook secrets. The initial source base on `wip/rust-core-go-extensions` was
`013aa7df959530be7cb9470c396993c32c7fb7ca`. Results below bind the recorded source
hashes; they do not certify every concurrent change or the entire branch.

## Current local checks

At 2026-10-11 20:38 UTC, using mise Go 1.27.2 with the generated provider-proof
RPC contract and controlled client wrappers:

```sh
cd services/extensions
MISE_IGNORED_TOOLS=rust MISE_AUTO_INSTALL=false mise exec -- go test -mod=readonly -race ./internal/modules/payments/... -count=1 -timeout=90s -json
MISE_IGNORED_TOOLS=rust MISE_AUTO_INSTALL=false mise exec -- go vet ./internal/modules/payments/...
```

Both packages passed: **63 top-level tests, 160 tests including subtests**, zero
failures. Three explicit fixture-dependent tests skipped: the two standalone
mocklab adapters and `TestRustDurableProofAcrossGoRestart`. Vet and scoped gofmt
checks passed. The document gate and its 33 unit tests also passed. This tested
the uncommitted payment-source manifest based on the revision above; it is not
a claim that an untested commit or all repository changes passed.

The tests cover controlled local UDS RPC metadata and proof receipt mapping;
Rust-unavailable and invalid-proof non-ACK; lost inbox write recovery; unchanged
original bytes; stale queued proof lookup; concurrent proof upgrade; and
missing-proof repair of a done inbox with a quarantined credit job. Go state
fixtures remain synthetic and do not prove real Rust journals or balances.

The isolated nested PostgreSQL race test passed at 2026-10-11 20:46 UTC: one
top-level test and six recovery subtests, zero failures or skips. It used only a
new synthetic `lmm_payments_test` database, preserved source hashes and left the
separate joint database untouched. The scenarios covered durable evidence and
proof references, unchanged raw bytes, exact event deduplication, review repair,
a done inbox repairing its quarantined semantic job, and final money-job state
preservation. Its Core/provider fixtures are synthetic; this proves real Go
PostgreSQL storage behavior, not real Rust journals.

## Completed Rust/Go recovery acceptance

At 2026-10-11 21:23 UTC (2026-10-12 locally), the isolated runner passed
`real_go_http_uds_proof_survives_expiry_and_service_restart` in the Rust
`payments` integration target: **one passed, zero failed or ignored**, with
14 other tests filtered out. The runner recorded 330 compiler inputs unchanged
before and after execution. It used mise-selected Go 1.27.2 and pinned Rust
1.99.0, separate fresh synthetic Core/Go PostgreSQL databases, actual Go loopback
TCP HTTP callbacks, and the real UDS client, bridge and Rust payment service.
Provider API responses were simulated; signed webhook bodies were created by
the Rust runner, and no webhook secret or Core DSN was given to Go.

Four separate Go processes executed `TestRustDurableProofAcrossGoRestart`:

| Phase | Observed result |
| --- | --- |
| `recv` | Accepted first webhook and drained its inbox into a credit job, without posting money. |
| `recv-lost` | Rust durably accepted a second event; an injected Go inbox-insert failure returned HTTP 503. |
| `offline` | With Rust UDS stopped, callback delivery returned HTTP 503. |
| `settle` | After a real 302-second wait, webhook-key rotation and reconstruction of the Rust service, old signed callbacks recovered durable proofs and settled once. |

The runner also constructed a historical Go queue shape: an already-drained
inbox and review credit job with missing proof references, while Core already
retained the accepted proof and had no credit receipt. Identical redelivery
recovered that proof, reopened the inbox and repaired the same job. This is a
synthetic legacy queue-state test, not migration of real historical Core proofs.
Duplicate deliveries and 12 concurrent workers preserved **two accepted proofs,
one credit receipt and one journal**, whose entries sum to zero. Go and Rust
both retained the original event/payload bindings.

The Rust runner constructed two service instances in one operating-system
process: **`rust_os_process_restart=false`**. Go processes actually restarted;
the Rust OS process did not. The result does not establish Caddy or browser
acceptance, normal host mounting, real Stripe/ePay interoperability, live
channels, all refund flows, a deployed release or production readiness. Rust's
failed-refund release guard remains in place, and an ePay Rust verifier is still
missing. Neither standalone mocklab adapter test was enabled by this run.

The private local artifacts `checkpoint-06-payment-real-go-joint-receipt.json`
and `checkpoint-06-payment-joint-safe-result.json` record the exact command,
environment, stable input hashes and safe result. The Go payment report binds
those artifacts to its unchanged 17 Go source files. Only the three module
documents changed after the joint run; Go tests were not rerun for these wording
changes.

# Historical verification record — 2026-10-10

## Executed locally

The available local toolchain was Go 1.23.2 on Linux/amd64. The repository requires
Go 1.25.0 and its RPC dependencies were not available in this network-restricted
container. The standard-library payment package was tested without altering the
repository go.mod:

```sh
cd services/extensions/internal/modules/payments
GODEBUG=httpmuxgo121=0 GO111MODULE=off go test -race -count=10 .
GODEBUG=httpmuxgo121=0 GO111MODULE=off go test -coverprofile=coverage.out -json .
GODEBUG=httpmuxgo121=0 GO111MODULE=off go vet .
```

Results: **41 test functions, 71 tests/subtests passed; 10 consecutive race-test
runs passed; go vet passed. Statement coverage: 67.8% for this package.** The
explicit ServeMux setting is needed because module-off mode otherwise selects
legacy routing defaults. CI uses the repository's Go version normally.

The local checks exercised exact signatures and original callback bytes,
merchant/environment checks, stale/future delivery signatures, rotated secrets,
duplicate JSON/form fields, concurrent duplicate events, semantic duplicates,
provider transaction uniqueness, wrong amount/currency/account binding, explicit
account ownership, core outages, ambiguous or invalid receipts, callback-before-
order recovery, refunds before credit, partial/cumulative refunds, lost hold and
commit responses, no timeout-based release, provider retry-window expiry,
unsigned reconciliation, pagination limits, an in-flight evidence upgrade,
credential redaction, HTTP request validation and private-network address denial.

All provider calls used an in-process fake HTTP transport. No real Stripe/ePay
account, real funds, private credentials or production service was used.

## Added but not executed in the local container

- `rustbridge/bridge_test.go`: generated-contract receipt mapping, original
  evidence preservation, controlled Unix gRPC metadata, integer account IDs,
  user/service credential separation and unregistered-service failure.
- `pgtest/postgres_test.go`: actual PostgreSQL concurrent row updates, rollback,
  immutable ownership, unique provider transaction binding, event deduplication,
  restart/reopen persistence and dedicated-database guards.
- `.github/workflows/mk10-payments.yml`: repository-toolchain checks plus a
  disposable PostgreSQL 16 service. Consult the PR's check results for its
  execution status; the local result above is not a claim that CI passed.

## Still requires joint acceptance

The Rust CorePayments service is not registered at the starting base. Thus no
real Rust-ledger end-to-end acceptance, generic independent provider verifier,
prepared-intent read authorization or production host registration was verified.
The unresolved contracts are listed in `PROTOCOL_REQUIREMENTS.md`. Mock receipts
in unit tests prove Go state transitions only; they do not prove Rust journal or
balance correctness. The PR is intentionally Draft and must not be deployed.

## Upstream layout synchronization

During implementation, base commit `4179ea1acfa1172afbd0d16927acb1c3c3a93a10`
renamed `apps/api-go` to `apps/lmm-extensions`. The task branch was rebased onto
that commit and its files, commands and workflow follow the new directory.
The first CI attempts failed before tests: first a health-command quoting error,
then the obsolete Go module path. Both workflow issues were corrected; check the
latest PR run for the actual integration-test outcome.
