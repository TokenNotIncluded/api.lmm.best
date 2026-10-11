# Payment money contract

This is the financial safety boundary for the fresh-install microservice WIP, not
an assertion that payments are enabled. Rust owns identity, authorization, payer
selection, balances and ledger receipts. Go owns provider orders, evidence and
module-local business state; it never chooses credit amounts or accesses the core
database. The assembled Go host registers only `identity`; the core still keeps
public billing/model readiness unavailable. See [architecture](core-migration.md)
and the [payment module](../apps/lmm-extensions/internal/modules/payments/README.md).

## Units and compatibility

A bare number or `$` symbol is not a money type. Every boundary must identify its
unit, currency, scale and protocol version.

| Value | Meaning |
| --- | --- |
| Legacy Web `CREDIT` | Integer raw wallet credits, historically 500000 credits per USD. Not model tokens or legacy batches. |
| Current core `credit_500k_usd` | The unit currently declared by Rust billing, the ledger schema and Protobuf: 500000 integer units per USD. |
| Provider `amount_minor` | Integer smallest units of the explicitly named ISO fiat currency; not wallet credits. |
| Fiat FX and price multipliers | Separate conversion/pricing policy, never a new ledger denomination. |

The browser declaration does not authorize a core monetary operation. Its unit
must match the explicit core type and versioned protocol. Any boundary with a
different unit requires a validated, exact adapter; never pass an unchanged integer
between scales or infer a conversion from an untyped field.

Amounts and arithmetic must remain exact integers or decimals, with checked
bounds and overflow. Do not pass 64-bit values beyond JavaScript's exact integer
range through `Number`. Display currency, language, FX, model tokens, coupons and
recharge prices never alter the ledger unit. USD and CNY totals are kept separate.

This branch installs empty databases; it does not run the retired Go balance or
subscription migrations. Historical corrections require separately authorized
scope, snapshots, reviewed integer rounding, writer quiescence, durable duplicate
protection and cache invalidation. Do not relabel balances, alter the denomination
or rewrite paid orders, prices or usage history to simulate a correction. Missing
or inconsistent calibration blocks financial operations. Historical procedures
remain in the [archive](archive.md).

## Legacy Web input boundary

The retained Web wallet sends `{amount:q,amount_unit:"CREDIT"}` to
`/api/user/topup/currency/v2/` quote, checkout and discount-validation routes.
These are legacy frontend compatibility calls, not enabled native core payment
routes. A missing route or capability must disable checkout rather than fall back
to an endpoint that might interpret `q` as legacy batches or another scale.

`X-LMM-Credit-Unit: 500000` declares that browser input contract; it is not an
identity or authorization credential. It never rescales request bodies. An input
unit mismatch or an uncertain mutation must not trigger automatic replay.

The selected amount, payment method, discount code and input revision bind the
quote and confirmation. Changing any of them invalidates the old confirmation.
Use current authoritative limits and a matching quote; unavailable or failed
validation is not permission to pay. The server independently recalculates and
enforces every amount, provider and promotion limit. A top-up navigation link
only prefills a form; it does not place an order or grant value.

## Payment evidence, receipts and refunds

Before provider checkout, persist the economic account and actor, exact credited
amount and unit, expected settlement amount and ISO currency, provider, merchant,
environment, product/store binding, stable order key and applied pricing policy.
Callbacks can grant value only after independent verification against that frozen
intent. Go cannot assert verification with a boolean, service token or supplied
user ID. A callback acknowledgement or locally queued job is not a posted credit.

Confirm browser payment by the exact order ID in the authenticated owner's order
history. A larger balance, similar amount, newer history ID or checkout URL
parameter is not payment evidence. Credentials, provider signatures and bearer
links never belong in public status, logs or presentation receipts.

An exact retry retains its authenticated scope, original key and complete payload;
a changed request under that key is rejected. Missing responses and timeouts may
mean the commit succeeded. Query/reconcile that operation before any new attempt;
do not invent a fresh debit, refund, release or compensating transfer. Validate
receipt identity, unit, committed state and original-operation binding. A pending
result is a recovery handle, not usable value.

Refunds use the captured charge, fiat conversion and entitlement snapshot, and
return value only to the original economic payer. Enforce the cumulative original
amount cap. Hold funds before a provider refund; independently verified terminal
success commits it, verified terminal failure releases the hold. Timeout, missing
callback or unsigned success proves neither. A pre-credit refund must fence later
stale credit replay. Ledger refund and external provider payout are separate steps.

## Funding, transfers and subscription lifecycle

Store actor, Key owner and payer separately. A personal Key defaults to its own
account; a team Key uses only its team. Current membership and explicit spending
grants are required. Platform roles, host credentials and tool installation do
not grant authority to spend someone else's funds.

Fix the payer, price version, budget window and funding components when accepting
a request. Retries, streaming completion, callbacks and refunds retain them even
after membership or Key changes. Select only explicitly authorized sources; an
unknown result, permission failure or database error cannot fall back to another
payer. One request does not split payment across unrelated accounts.

Authorization, applicable budgets, pending reservations, ledger movements and
audit/outbox records need a consistent transaction boundary. Leave/rejoin, role
changes and new Keys cannot reset accumulated budget. Do not hold database
transactions over provider calls or streams. A dependency outage must fail closed,
never allow an uncharged request or manufacture a zero balance.

Transfer debit, claim/cancel transition, credit/refund and receipt commit together;
repeat requests cannot move funds twice. Links and QR codes are bearer credentials:
keep secrets in URL fragments and request bodies, never public query strings or
third-party QR services. Ordinary GET must not claim. Tool writes require the exact
active grant and explicit confirmation bound to actor, auth version, tool/version,
request and amount, rechecked on dispatch/resume. Resume the original uncertain
request. Retain financial history and tombstones; do not delete or refund records
outside the state machine.

Subscription orders freeze plan, currency, entitlement and provider bindings.
Renewals have unique event/transaction keys and recorded periods; failed or
`past_due` payments grant no new period. Cancel-at-period-end preserves paid access.
Immediate cancellation/refund follows verified evidence and policy. Do not reprice
existing orders from today's plan, FX or settings. Round only at the defined
provider boundary and persist the exact rounded amount to verify later.

## Pricing, rewards and safety review

Price reads and unchanged saves preserve stored bytes, expressions and captured
calibration. Missing historical calibration is unknown, not permission to use
current values. Monetary edits require an explicit unit/version, selective keys
and a revision check; stale revisions need a fresh read, not a blind overwrite or
USD fallback to a legacy endpoint. Every writer respects exact model-price locks.
Validate complete committed snapshots and requested lock state; malformed replies
or transport errors do not prove rollback or authorize a stale fallback.

Rewards need independently verified eligibility, a funded authorized treasury,
durable entitlement uniqueness, bounded policy and a matching ledger receipt.
Gifts, transfers and manual order completion are not proof of cash payment. Keep
promised policy snapshots, append-only grants/reversals and first-payment markers;
callbacks, refunds or appeals cannot silently reset eligibility. The promotions
module's unavailable Rust reward adapter must remain closed until that authority
exists; fake top-ups and generic transfers are not substitutes.

Risk scores and model confidence are review heuristics, not proof of abuse or
authority to ban, change roles or move funds. Shared networks or ordinary language
alone do not authorize sanctions. Recheck actor ownership, durable evidence and
permissions for sensitive actions; keep protected-role boundaries and audit.
Expose only owner-scoped, minimized/redacted data, never foreign private messages,
identities or credentials. Review resets do not delete historical receipts.

Content moderation defaults off and requires explicit published scope and policy.
Failed, pending, partial or unreviewed content and model output cannot become a
user violation or penalty. At most one explicitly configured, capped penalty may
apply per original request; never create negative debt or collect a shortfall from
future top-ups. Preserve deduplication, exact actual-charge receipts, human appeal,
privacy and applicable notice requirements. These constraints do not enable the
retired moderation or referral APIs in the current host.
