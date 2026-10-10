# Go payment extension — task 10

Parent integration PR: #675. Work branch: `wip/mk-10-payments`.
Initial base: `e71f6028ac110420a55bda5d0bb0243a80d4e541`.
Rebased onto `4179ea1acfa1172afbd0d16927acb1c3c3a93a10` after the upstream rename
from `apps/api-go` to `apps/lmm-extensions`. All current paths use the new layout.

**Draft integration, not production-enabled.** The base declares `CorePayments`
but does not register its Rust implementation. This module does not implement a
Go ledger, edit Rust/Protobuf, migrate old data or enable production routes.
[PROTOCOL_REQUIREMENTS.md](PROTOCOL_REQUIREMENTS.md) records the task 02/06
requirements that must be resolved before funds can be connected.

## Ownership and construction

Go owns provider orders, callback evidence and processing state. Rust owns user
and team permissions, immutable payment intents, conversion quotes, balances,
refund holds and ledger receipts. An explicit Rust `account_id` is required; it
is not a team ID or the creator's user ID. Go never chooses a substitute account,
calculates credit units or accesses a core database.

`Service` implements the host's `Name` / `Handler` interface. Inject a dedicated
`Postgres`, `rustbridge.Bridge` and configured adapters using `New`. Construction
starts no worker and makes no provider call. The composition root remains
**disabled** pending real Rust authorization, independent evidence verification,
service scopes and an isolated database role. Other extensions and the Rust core
must remain available if this module fails.

Install `schema.sql` explicitly into a **new payment-only PostgreSQL database**.
Its login role must have no core database/schema access. `NewPostgres` checks the
expected database name, payment marker and absence of `core_*` schemas, but those
checks do not replace database permissions. It performs no automatic DDL. The
payment package uses `database/sql`; the composition root supplies the driver.

## Routes and execution

Mount `Handler()` through `modules.New` for private user routes:

| Method / module-relative route | Result |
| --- | --- |
| `POST /orders` | Rust intent, durable order and queued checkout; returns 202. |
| `GET /orders/{id}` | Local order status after current Rust authorization. |
| `POST /orders/{id}/refunds` | Requests a Rust hold before a provider refund. |

User routes require `X-LMM-User-Credential`; a host service token is not user
permission. Mutation routes also require `Idempotency-Key` (16–128 letters,
digits, underscore, hyphen or dot). Credentials are never stored in orders,
events or jobs. Authorization is repeated on retries. Bodies reject unknown and
duplicate fields, fractional minor units and trailing JSON.

```json
{"account_id":99,"channel_id":1,"currency":"USD","amount_minor":1000}
```

Expose `WebhookHandler()` on a **separate callback-only ingress**, at
`/callbacks/{channel}`. Do not expose user routes or give providers the host
credential. Apply request/concurrency limits and disable body/query logging;
ePay signatures and query credentials are sensitive. Internal host limits do not
replace public callback ingress limits.

A bounded, cancelable host worker calls `Work(ctx, limit)` with limit 1–100.
Each iteration drains stored events and performs at most one job per selected
order. No unbounded goroutine or retry loop is created by this package.
`Reconcile(ctx, orderID)` is a trusted worker operation, not a public endpoint.

## State and recovery

```text
callback -> signature / merchant checks -> durable event inbox
         -> order row lock -> durable job -> controlled Rust RPC
         -> validated durable receipt -> public status
```

Callback success acknowledges durable receipt of an event, **not account credit**.
Invalid signatures or channel identity never enter the inbox. Authenticated
irrelevant event types are ignored. Unknown orders remain pending for delayed
creation. Wrong amounts or conflicting ownership go to review without a ledger
call. Original evidence is private; public status exposes no signatures or jobs.

| Event state | Meaning |
| --- | --- |
| `pending` | Saved; awaiting its order or retry. |
| `done` | Applied to local state or a durable job, not necessarily posted by Rust. |
| `review` | Conflicting identity, amount or event meaning. |

| Job state | Meaning |
| --- | --- |
| `pending` | Waiting for a bounded attempt or dependency recovery. |
| `running` | Claimed for 30 seconds; only the matching claim may save its result. |
| `done` | Provider step or validated Rust receipt completed; provider steps do not settle funds. |
| `review` | Missing independent evidence, conflicting receipt or unsafe provider retry. |
| `rejected` | Explicit durable Rust rejection. |
| `fenced` | An earlier refund prevents local credit replay; Rust must enforce this atomically too. |

| Public order state | Meaning |
| --- | --- |
| `checkout_pending` / `awaiting_payment` | Checkout not ready / waiting for payment. |
| `credit_pending` | Provider paid; no confirmed Rust credit. |
| `credited` | Rust posted the original credit, not a statement of current spendable balance. |
| `refund_pending` | Awaiting hold, provider evidence or Rust refund confirmation. |
| `partially_refunded` / `refunded` | Rust confirmed the corresponding refund records. |
| `review` | Order-level terminal-state conflict. |

`core_credit_confirmed` refers to the original credit. Clients must read current
balances from Rust, including after refunds. A job in review leaves the user
status pending instead of inventing success; inspect private jobs for its reason.

Event deduplication uses provider + merchant + environment + event ID. A changed
meaning under one event ID is rejected. Distinct paid events for one transaction
use the same credit job. A database unique index prevents that transaction from
binding to two orders. Late failure cannot erase terminal success.

PostgreSQL row locks protect order updates; network calls run outside database
transactions. Durable claims recover after restart, and expired workers cannot
overwrite newer claims. After a lost Rust response, query the same scoped receipt
before retrying the original key. Timeout never creates a receipt. Receipt checks
require matching intent/action key, known state, a 32-byte fingerprint and a
journal ID for posted operations. The canonical fingerprint format remains a
protocol requirement, not a locally invented cross-language contract.

## Refunds

The user refund flow is `hold_pending -> held -> awaiting_evidence -> committed`.
Rust first holds funds. Go then sends a provider refund with a stable request key.
Only independently verified success plus Rust `REFUND_COMMITTED` completes it.
Only verified terminal failure plus `REFUND_RELEASED` releases a hold. Timeout,
missing callback, API error or unsigned provider success never releases funds.
Lost hold responses use receipt lookup; background jobs never save user sessions.

Partial refunds have separate IDs and request keys. Local locked checks cap the
cumulative requested amount; Rust enforces the authoritative cap and holds.
Stripe dashboard refunds use the external-refund path. Conflicting terminal
states go to review. Refunds before credit set a local fence, while Rust must
atomically prevent stale credit and reconcile overlaps with existing holds.

## Provider support and limits

**Stripe direct accounts:** fixed-amount hosted Checkout, PaymentIntent success
and failure, refund created/updated/failed, partial refunds, authenticated lookup
and bounded refund pagination. Connect-account events are rejected in this basic
adapter. The required pinned API version has no changing default. Checkout
disables adaptive pricing, promotions and automatic tax to preserve the intent.

HMAC verification uses the exact body, a five-minute delivery-signature window
and up to two rotating secrets. An old event with a fresh delivery signature is
accepted. The API key must identify the configured merchant and retrieve the same
signed event. Provider mutation retries retain the same key and stop after
23 hours from the first attempt, rather than assuming indefinite key retention.
API observations have `source=api` and no fabricated signature. The current Rust
bridge rejects them as settlement proof. Later signed callbacks replace those
observations, including while an older job is in flight.

**Legacy ePay MD5 profile:** deterministic signed checkout links, precise CNY
minor-unit amounts, GET/form callback verification and authenticated order query.
The wire format contains no currency or environment: only CNY is enabled, and
each environment requires a separate merchant key. Duplicate form fields,
ambiguous signing inputs and unexpected merchant/payment type are rejected.
There is no callback timestamp check, so replay keys must remain durable.

Legacy ePay automatic refunds are **disabled by default**: its common documented
API lacks stable refund IDs, refund idempotency keys and signed terminal proof.
An audited provider may explicitly enable a send-once legacy request and,
separately, partial amounts. Unknown writes are never automatically resent.
Even a reported success remains pending until independent evidence and a Rust
receipt exist. Complete settlement requires a provider-specific verified refund
or query adapter; this is not a promise that every ePay clone supports it.

Provider connections require HTTPS, block redirects and proxy environment use,
reject private/link-local/metadata addresses and pin checked DNS addresses.
Evidence and responses are bounded. No credential-bearing HTTP/SQL error escapes
in a user response. Adding adapters changes Go; enabling arbitrary new providers
without a Rust redeploy also requires the independent verification boundary in
the protocol handoff. No Go `verified=true` flag grants ledger authority.

## Tests and evidence

```sh
cd apps/lmm-extensions
go test -race -count=3 ./internal/modules/payments/... ./internal/coreclient ./internal/modules
go vet ./internal/modules/payments/... ./internal/coreclient
cd internal/modules/payments/pgtest
PAYMENT_TEST_DSN='postgres://payments_test:disposable_test_password@localhost:5432/lmm_payments_test?sslmode=disable' go test -race -v ./...
```

The PostgreSQL test refuses any database name except `lmm_payments_test` and resets
only its disposable schema. Never pass production credentials. The isolated
`pgtest` module keeps the driver out of production module dependencies. The
`mk10-payment-safety` workflow runs repository-toolchain, controlled RPC, host and
PostgreSQL tests without production secrets or deployment. See
[VERIFICATION.md](VERIFICATION.md) for executed checks and remaining limits.

Provider references checked during implementation:
- https://docs.stripe.com/webhooks
- https://docs.stripe.com/api/checkout/sessions/create
- https://docs.stripe.com/api/refunds/create
- https://docs.stripe.com/api/refunds/list
- https://docs.stripe.com/api/idempotent_requests
- https://pay.gggua.com/doc.html (one legacy ePay profile, not a universal guarantee)
