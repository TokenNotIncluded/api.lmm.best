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

The quick case alone does not prove exact loss of the Core network reply, full Stripe ingress,
checkout/refund, double OS restart after 302-second expiry, or production rails.
Core ReleaseRefund remains unavailable and must not free ambiguous holds.

The quick regression passed again independently in about 58.9 seconds. Actual Core and Go main
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


## Explicit accepted-proof expiry regression

`--case accepted-proof-expiry` selects only the separately ignored
`real_core_and_go_payment_accepted_proof_survives_expiry_and_os_restart` test.
The default remains the original quick COMMIT-before-local-receipt case.
The long test waits at least 302 actual monotonic seconds and requires every
original signature, including a never-accepted third event, to be older than
300 wall-clock seconds. No timestamp, clock, job, due time or lease is rewritten.

An owner transaction locks the single synthetic order before the actual Go main
worker starts. Every five seconds, receipts record both original product PIDs
alive, owner transaction/row-lock witnesses, unchanged original job and signed
bytes, both durable proofs, and zero credit/journal/wallet. `GetProviderProof`
returns the original decoded proof fields on the real Unix socket after expiry;
the stored receipt-wire hash remains unchanged.
`AcceptProviderProof` rejects the original unaccepted third stale event and an
already-accepted stale event with PermissionDenied. This is the actual contract:
durable proof reads recover accepted facts; fresh acceptance still checks age.
The payment token and webhook secret stay unchanged throughout.

Both real products are then SIGKILLed and reaped. The test keeps only its own
order lock until both restarted sealed product PIDs are ready, confirms zero
money again, then releases the read-only lock. The Go main worker performs the
first credit using the original job/proof/key. `main-recovered.json` must precede
any later bootstrap replay. Exact receipt/intent/Go-job/journal association and
clearing -500000 / original wallet +500000 legs remain mandatory; original
payload replay leaves two proofs, two done inbox events and only one credit.

The explicit expiry case passed independently in 332.577 seconds: 302.071 seconds
of actual monotonic waiting, 62 consecutive observations (maximum gap 5.029
seconds), and a minimum original signature age of 304 seconds. Independent HMAC
verification confirmed all three original signatures and that the third event
was never accepted. All four product PIDs, the test/runner, resource scopes and
successful owned PostgreSQL fixture were verified absent afterward. The quick
case also passed after the shared-fixture extraction; the 37 offline runner
checks include missing/extra/ignored cases, insufficient waits, missing locks,
changed original proof/job evidence and integer/boolean type confusion.

The long case remains opt-in; source presence is not an execution receipt.
`test:core` still runs `cargo test --locked --all-targets`: it does not run the
ignored 302-second case, but the existing nonignored quick process case still
requires its sealed product/bootstrap paths and digest variables. No aggregate suite success is
claimed. The selected runner must discover both exact cases and only this
ignored case, then actually report one passed, zero failed/ignored, one filtered.
The explicit expiry run has a 480-second harness deadline. Ordinary quick
regression, production Stripe callbacks/checkout/refunds, and payment resource
acceptance remain separate scopes. Existing failed fixtures must remain intact.
