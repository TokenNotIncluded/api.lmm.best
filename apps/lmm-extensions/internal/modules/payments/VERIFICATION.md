# Verification record — 2026-10-10

## Executed locally

The available local toolchain was Go 1.23.2 on Linux/amd64. The repository requires
Go 1.25.0 and its RPC dependencies were not available in this network-restricted
container. The standard-library payment package was tested without altering the
repository go.mod:

```sh
cd apps/lmm-extensions/internal/modules/payments
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
