# Payment contracts and remaining acceptance

This is the current WIP integration boundary for PR #675. It neither authorizes
live channels nor makes a production-readiness claim.

## Implemented contracts

- `PrepareTopup` freezes explicit Rust account, channel, provider, merchant,
  environment, currency, external minor amount and integer credit quote.
- `GetPaymentReceipt` can return `PREPARED` for authorized intent reads. That
  state has no money journal and cannot become credit success in Go.
- `AcceptProviderProof` asks Rust to independently verify original webhook bytes
  and signature before persisting proof. `GetProviderProof` requires the exact
  expected intent, provider event ID and payload SHA-256. The receipt contains
  no payment-success or journal state.
- Go sends `accepted_proof_id` and the original evidence for `CreditTopup`,
  `CommitRefund`, `ReleaseRefund` and `ApplyExternalRefund`. A Go service token,
  API response, parser result or caller-provided timestamp is insufficient.
- Payment RPCs use the controlled Unix `coreclient`. User actions require current
  user authorization; provider workers do not forward user credentials. Unknown
  RPCs and dependency failures fail closed.

Stripe webhook secrets remain in Rust. Go's deprecated `WebhookSecrets` config
field must be empty. Raw payloads and signatures are private and immutable;
Go's authenticated provider API reads remain observations. Rust decides provider
scope, freshness on first acceptance, transaction binding and replay semantics.
Adding another provider still requires an independent Rust verifier or another
explicitly designed trusted boundary; no generic attestation is assumed here.

## Recovery and finality

Ingress looks up accepted proof before trying fresh acceptance. Go ACK requires
both Rust durable acceptance and Go inbox persistence. A lost response or old
queue can therefore recover the accepted proof without weakening freshness for
unverified events. Same-event payload changes conflict. Missing proof can repair
pending/review work and already-drained inbox entries for the same immutable meaning; done/rejected/fenced jobs do
not reopen. A stale worker cannot erase stronger proof or a final receipt.

Ledger idempotency and refunds remain Rust responsibilities: original transaction
uniqueness, request fingerprint, original payer, cumulative partial refunds,
refund-before-credit fences, hold overlap, and no release on timeout. Go must not
compute credit or infer balance correctness from local states.

## Executed recovery acceptance

The four-phase simulated Stripe acceptance passed locally on 2026-10-12 using
four real Go processes, loopback TCP HTTP callbacks, UDS RPC, the Rust payment
service and separate synthetic PostgreSQL databases. It covered durable proof
acceptance followed by a failed Go inbox write, non-ACK during a Core outage,
old-proof recovery after a real 302-second wait and webhook-key rotation,
synthetic legacy done-inbox/review-job repair, duplicate delivery and 12
concurrent workers. Two proofs produced one credit receipt and one balanced
journal. Two Rust service objects were constructed in one OS process; a Rust
OS process restart was not tested. See [VERIFICATION.md](VERIFICATION.md).

## Remaining acceptance before enabling

Extend the fresh isolated PostgreSQL-backed Rust/Go tests with simulated
providers. Inspect real Rust journals and exact integer balances for:

- Personal and team account authorization, revoked users/services and channel
  scope; changed amount under the same key and cross-intent proof reuse.
- Loss of each ledger/refund RPC response after commit, beyond the accepted-proof
  recovery and inbox-write failure already tested.
- A full Rust operating-system process restart, distinct from reconstructing
  the Rust service in the completed test.
- External/pre-credit refunds, local holds, partial/cumulative caps, conflicting
  terminal outcomes and finite provider retry windows.
- Normal host owner/runtime database grants, callback-only ingress, explicit
  host configuration and its unavailable-dependency behavior.

The joint test constructs the module directly; it does not establish normal
host mounting or public proxy/browser acceptance. Rust's failed-refund release
operation remains guarded until dispatch is bound to a hold. Go's ePay adapter
has no corresponding Rust proof verifier yet. Passing the top-up recovery path
does not remove these limits or verify all provider and refund operations.

Unit fixtures do not prove the ledger. The joint fixture contains a private Go
DSN file reference, UDS/service/user test credentials and already signed payloads;
it must never contain a core DSN or Stripe webhook secret. Record executed and
skipped checks in [VERIFICATION.md](VERIFICATION.md). No production deployment is
part of these tests.
