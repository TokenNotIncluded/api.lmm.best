# Task 02 / task 06 handoff — blockers to enabling payments

This file proposes integration requirements. It changes no Protobuf or Rust
ledger code. The task 10 Draft must remain disabled until these are resolved and
verified with the real Rust service, not just a mock.

## Existing contract used without alteration

`contracts/proto/lmm/core/v1/payments.proto` already declares `PrepareTopup`,
`CreditTopup`, `HoldRefund`, `CommitRefund`, `ReleaseRefund`,
`ApplyExternalRefund`, and `GetPaymentReceipt`. At this base the service is not
registered. Go calls those exact generated methods through the bounded Unix
`coreclient`; `UNIMPLEMENTED` remains dependency unavailable, never success.

## Required decisions

1. **Independent evidence without provider-specific Rust deployments.** The
   current contract requires Rust to verify original provider signatures and
   event semantics. Go cannot claim `verified=true`. To meet the microkernel
   goal, approve a generic trusted verification boundary: for example a
   separately scoped verifier issuing signed, bounded attestations over intent,
   provider, merchant, environment, transaction, unique event/refund ID, exact
   minor-unit amount/currency, event kind and original-evidence hash. Core must
   validate issuer scope, signature, account/intent binding and replay state.
   This is a proposal, not an implemented or approved attestation format.
   Alternatively, approve a controlled independent query/verifier contract.
   Arbitrary caller-selected endpoints, embedded secrets and service-token-only
   assertions are not acceptable. Do not move a provider parser into task 10's
   Go code and then silently trust its output as a ledger grant.

2. **Prepared-intent read and authorization.** `GetPaymentReceipt` returns a
   durable mutation receipt, but there is no prepared-intent read operation or
   prepared receipt state. Task 10 needs an explicit `topup.read` authorization
   and immutable intent lookup before a credit exists. Supply a read-intent RPC
   or define a valid, scoped prepared receipt read. The bridge currently uses the
   prepare key for the read and fails closed on NotFound. A creator check or
   cached team membership in Go is not a replacement. Refund admission also
   needs current refund permission before local pending reservations can be
   abused by a read-only caller. A denied hold is not proof that an earlier
   ambiguous attempt never committed; preserve its reconciliation record.

3. **Durable idempotency and receipt fingerprint.** Define the canonical request
   fingerprint across Go/Rust and its scope. Existing `request_sha256` has no
   canonical encoding definition. Keys bind immutable action/intent/amount;
   changed requests under one key must conflict. Core must deduplicate a payment
   transaction even under different provider event IDs or Go keys. A receipt
   lookup must be scoped to the service's configured channels/intents. Transport
   errors, receipt NotFound after a timeout and user revocation must not be
   treated as evidence of a failed provider payment.

4. **Delayed signed proof and reconciliation.** Stripe re-signs webhook
   deliveries, but a saved, already validated payload can sit in an inbox while
   Rust is down. Define how independently verified durable evidence can be
   replayed without weakening freshness/replay checks. Stripe/ePay query results
   have no webhook signature. A controlled provider read or approved verifier
   must independently confirm transaction/refund status, amount, currency and
   ownership. Task 10 retains those reads as observations and does not forge a
   signature. Legacy ePay needs provider-specific refund identity, idempotency
   and proof support before automatic settlement can be enabled.

5. **Atomic refund fences and ledger holds.** Rust must atomically handle a
   refund before credit, a lost credit response, cumulative partial refunds,
   existing local refund holds, external refunds and concurrent stale credits.
   The same provider refund cannot cause both a held commit and a second
   external debit. Never release a hold on timeout; release requires verified
   terminal failure. A refund fence must survive Go data loss and process
   restarts. Define the outcome for pre-credit partial refunds and return a
   durable receipt for that exact outcome. Go does not choose a substitute
   account, bypass a budget, calculate credit units or write core tables.

6. **Production composition and acceptance.** Provide an independently scoped
   payment service credential, channel allowlist/configuration and explicit
   database role isolation. Enable the module and callback-only ingress in the
   composition root only after those requirements and real Rust authorization
   are tested. No core/Go production deployment is part of task 10.

## Required joint acceptance before enabling

Use PostgreSQL-backed Rust and Go services and simulated providers. Verify an
explicit personal account and team account; revoked user and service scopes;
changing amount under the same key; 100 concurrent callback deliveries; distinct
event IDs for one payment; loss of each RPC response after commit; delayed proof;
external refunds before credit and after a held refund; partial refund caps;
provider retry-window expiry; and Go/core restarts between every durable step.
Check Rust journal entries and final balances directly in the test environment.
Passing Go state-machine tests alone is not proof of those ledger properties.
