# Local payment process recovery

This explicit regression uses a fresh owned Docker PostgreSQL cluster, sealed
Core and Go product binaries, a sealed Rust test executable, and the existing
sealed Go payment test executable as a bootstrap only. No external request or
real payment is allowed. Bootstrap substitutes only the synthetic Stripe HTTP
transport; signature acceptance and money use the real Core process and database.
Bootstrap exits before the Go main process and its actual StartWorkers run.

The owned trigger witnesses Core credit COMMIT before Go local credit_receipt
persistence, then both real product PIDs are SIGKILLed and reaped. Recovery honors
the original 30-second lease and job key, confirms exactly one credit journal,
balanced two-leg entries, 500000 synthetic credits and zero reserved credits.
Decoded Core receipt bytes must match the receipt row, original job key and
intent; the same journal ID must match the Go receipt and intent. Its exact legs
are the original account wallet +500000 and system clearing -500000. The immutable
association is saved and compared at crash, main recovery and bootstrap replay.
A second signed event's accepted proof persists despite a real inbox insertion
failure and HTTP503. `main-recovered.json` freezes actual Go main-worker
recovery before bootstrap runs again. The later bootstrap `settle` phase replays
both original payloads over loopback HTTP, recovering that second inbox entry:
two accepted proofs, two done inbox events, the same credit job key/receipt and
still one journal. This later phase is explicitly bootstrap replay, not the
production callback ingress; it cannot perform the first credit for main.
Another automatic main-worker tick checks the money remains unchanged.
Identity receipts bind every actual process to SHA256,
inode and startticks before and after readiness.

This does not prove exact loss of the Core network reply, full Stripe ingress,
checkout/refund, double OS restart after 302-second expiry, or production rails.
Core ReleaseRefund remains unavailable and must not free ambiguous holds.

The local independent run passed in about 58.2 seconds. Actual Core and Go main
processes were each killed and restarted; the original 30-second lease and job
key remained unchanged. Core receipt protobuf, intent, Go receipt and the one
credit journal were directly linked, including exact clearing -500000 and the
original account wallet +500000 entries. Replaying both original payloads left one credit receipt
and journal. Successful products, scope and owned database were verified absent;
the earlier fixture TLS and short-bootstrap identity failures remain preserved
as private evidence. This is the limited recovery scope described above.

Navigation: `markref context payments.process-recovery`.

Freeze `verify_process.sources()` before compiling target `payments_process`
and Go `go test -c ./internal/modules/payments`; pass that JSON as
`--compiled-inputs`. Build is owned separately; this runner never builds.
Run through agcc with output, process and cache roots. Required flags:
`--output-root` (new repo/.local-tests directory), `--process-root` (new private
absolute directory at most 35 bytes), `--core-binding` (release source-binding
JSON), `--core-build-receipt` (successful release-build.json binding the exact
Core SHA and source-binding digest), `--go-reference` (original PASS acceptance
binding the exact Go SHA), and four paired
`--{core,go,test,bootstrap}-binary` / `--{core,go,test,bootstrap}-sha256` flags.
The runner checks exact complete product subsets and frozen test sources before
creating its fixture. Failure preserves the fresh database and private logs;
only a fully verified success destroys its own cluster. Never point this test
at an existing DSN, fixture, account or provider. Private evidence is not committed.
