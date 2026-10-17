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

## Explicit host Web status read acceptance

The normal Go host can explicitly assemble `payments` and
`host.web_compat = true` with `NewPaymentStatusReader`. Its read-only `GET` / `HEAD`
`/api/user/payments/orders/{top_id}` route goes through `Mount` admission and
drain, then passes the original user bearer through `Service.Get` to Rust order
and account authorization. A mounted route without the reader authenticates
before `503 PAYMENT_DISABLED`; write methods remain forbidden.

The independent [status runner](verify_web_status.py) archives an explicit
committed Core revision, overlays the independent Rust test, freezes current Go
and helper inputs, and compiles offline under the shared heavy-build lease. It
uses a fresh owned PostgreSQL fixture and restricted module runtime credentials;
no external DSN, image pull or provider request is accepted. Installed pinned
tools, cached crates/modules and the pinned PostgreSQL image are prerequisites.

Prepare existing, owned 0700 absolute output and short socket directories without
symlink components. Set `PAYMENT_STATUS_OUTPUT`, `PAYMENT_STATUS_SOCKET_ROOT` and
`PAYMENT_STATUS_CORE_REVISION` to those directories and an immutable Core commit.
Track the real Cargo cache, shared target and selected output/socket roots with
agcc. The runner keeps its temporary and Go/mise caches under the output root:

```sh
agcc run --agent payment-web-status --root services/core/target \
  --root "$HOME/.cargo" --root "$HOME/go/pkg/mod" --root /tmp/lmm-delegate \
  --root "${PAYMENT_STATUS_OUTPUT:?Set an existing private absolute output root}" \
  --root "${PAYMENT_STATUS_SOCKET_ROOT:?Set an existing private short socket root}" -- \
  env MISE_DISABLE_TOOLS=rust MISE_AUTO_INSTALL=false RUSTUP_TOOLCHAIN=1.99.0 \
  PYTHONDONTWRITEBYTECODE=1 MISE_CACHE_DIR="$PAYMENT_STATUS_OUTPUT/cache/mise" \
  mise exec -- python3 -B tests/payments/verify_web_status.py \
  --output "$PAYMENT_STATUS_OUTPUT" --socket-root "$PAYMENT_STATUS_SOCKET_ROOT" \
  --core-revision "${PAYMENT_STATUS_CORE_REVISION:?Set an immutable Core commit}" \
  --min-free-gib 100
```

`--min-free-gib` defaults to 10; the accepted local rerun explicitly used 100.
This is a local build-protection budget, not a product capacity acceptance
threshold. A first run omits `--reuse-build` and gets a unique run name. An
optional `--reuse-build <previous-private-run>` must match the frozen committed
Core/contracts/schema inputs; it still binds the current test and creates a
fresh PostgreSQL fixture. Keep all previous failed runs and private receipts.

The final independent run passed against Core commit
`5e8053e2c42536244c57e793bf048c3f9db98097`, using `--run-name run06` and an eligible
prior `run05` archive for reuse. One real Rust test, both Go phases and the actual
`app.Run` subprocess helper executed successfully without skips. The path used
real CoreControl/CorePayments Unix RPC, isolated PostgreSQL and actual loopback
HTTP. A team administrator who did not create the order could read it; removing
membership denied the same URL with 403 while the personal session remained
valid, and logout then returned 401. Foreign-account, rotated credential, API
Key, host-token and anonymous reads were refused.

The run also checked the disabled reader, forbidden methods, admission,
saturation/recovery/drain, `HEAD`, `no-store`, redacted DTOs and malformed local
projections. Stopped/rejected work and `unknown` dispatch were synthetic local
projection cases. Reads and app lifecycle left the checked Go rows unchanged;
Core billing/payment tables and sequences stayed unchanged with zero journals,
zero provider adapters and no queued money work. This is a real Rust-authorized
`PREPARED` status-read acceptance, not real credit or refund settlement. The
module `Service.Step` tests for historical stopped refund review use development
Core/provider fixtures and remain a separate level of evidence.

Each run keeps `inputs.json`, `runner-result.json` and `result.json` with the
source/binary bindings and observed scope. The final run verified stable full
inputs, physical PostgreSQL identity and no remaining client backends before
removing its own fresh fixture. Private logs and credentials must not be
committed. Checkout, actual provider/webhook compatibility, complete refund/hold
release recovery, browser/Caddy flows, sustained payment resource acceptance and
server deployment remain open gates. `core_credit_confirmed` records only the
original completed credit, never the current balance or spendable value.
