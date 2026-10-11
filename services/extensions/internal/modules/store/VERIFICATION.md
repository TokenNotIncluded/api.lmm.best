# Task 07 verification evidence

Base inspected through GitHub: b83723cdac3f148140806b8025cb5360a2f6a26d.
Changes are confined to internal/modules/store/ and internal/modules/support/.
The host entry point, registration, protobuf definitions, Rust code and root
Go dependency files are unchanged.

## Executed locally

The available compiler was Go 1.23.2, not the repository's Go 1.25.0.
The two standard-library business packages were tested with a temporary,
uncommitted alternate module file containing only the original module path and
`go 1.23.0`. No business implementation was stubbed or rewritten for this run.

- `go test -modfile=offline.mod -race -count=10 ./internal/modules/store ./internal/modules/support`: PASS.
- `go vet -modfile=offline.mod ./internal/modules/store ./internal/modules/support`: PASS.
- `gofmt` applied to all new Go files.

Tests use test-only repositories and a replay-safe fake Rust funds port. They
cover catalog/variants, quota versus physical stock, paid-unavailable behavior,
price snapshots, concurrent purchase limits, cancellation, fulfillment, refund
requests/approval, seller settlement, ownership and revoked access, command replay,
response loss after a money effect, commit failure after a money effect, service
restart, invalid receipts, customer projection, private notes, conversation
isolation, concurrent message replay, close/reopen and HTTP validation.

These results demonstrate local state/operation-key behavior under the declared
interfaces. They are NOT evidence that a real Rust ledger has executed payments.

## Not executed in this environment

- Repository-native Go 1.25 dependency build and rustauth protobuf-adapter tests.
  The toolchain download was attempted but the container cannot resolve external
  hosts. Existing gRPC/protobuf dependencies are not cached locally.
- Real PostgreSQL acceptance tests in `pgtest/`. No PostgreSQL server/driver or
  test database was available. The runner is included but its presence is not a pass.
- End-to-end Rust funds RPCs: the inspected core has no such interface. All
  production paid operations remain disabled, with HTTP 503.
- Final host registration and order-event consumer wiring: reserved for the
  integration task. No production deployment or merge was performed.

Before marking this PR ready, run the native tests and the disposable PostgreSQL
runner documented in README.md. Enable paid operations only after the Rust funds
port meets every documented authorization and replay requirement.
