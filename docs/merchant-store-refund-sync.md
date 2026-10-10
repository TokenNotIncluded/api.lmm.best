# Store collection and provider-origin refund synchronization

This change applies to merchant-store orders on the current Go service. It is
not a rewrite of account top-up refunds, the native Waffo acquiring API, generic
Epay, or the separate Rust microkernel branch.

## Buyer and seller flow

A pickup link returns to its complete path, query and fragment after sign-in.
An explicit purchasing-account sign-in link displays the sign-in form even
when a stale or different account is still in browser memory. It does not
log out, refresh a cookie, or collect content automatically. Invalid pickup
codes and wrong purchasing accounts now have separate actionable errors.
Existing account, link lifetime, pickup-code, origin and restricted-cookie
checks remain in force. Refunded links show metadata/refund-history access,
not retired delivery contents.

Buyers use the order's refund panel (or the protected pickup refund panel).
Sellers review the request or initiate a refund from their order view. The
existing full, quantity and amount modes use remaining original principal.
Balance purchases refund platform balance atomically. Pancake purchases submit
a customer refund ticket with the original PAY, currency, amount and frozen
merchant/store/buyer scope. A ticket acceptance does not mean money returned.
The existing broker queries actual provider execution before local completion.

After capability eight is explicitly activated, the seller or current root
administrator can select **Sync with payment provider**. It calls:

```
POST /api/store/orders/:id/refunds/sync
{}
```

Ordinary buyers and ordinary administrators cannot call it. The body accepts
no amount, recipient, payment reference or completion evidence. The operation
performs read-only provider queries and local receipt accounting; it does not
create another provider refund ticket.

## Dashboard refunds and partial amounts

A signed Pancake refund event whose business reference is not a local refund
now follows the original payment's frozen credentials and verifies the actual
refund execution through GraphQL. The order/payment tree establishes PAY/ORD
ownership; the execution list provides actual refunded native money.
The execution list and its count are filtered by the frozen `paymentId`, as
documented by the provider. Every returned execution must repeat that PAY.
Refunds on another payment attempt must not enter this payment's count. IDs,
currency, environment and original business reference must agree. Missing,
truncated or ambiguous evidence fails closed. The bounded query rejects more
than 100 payment/refund records rather than silently accepting a partial list.

Native success is keyed by the stable provider refund execution ID, not the
notification delivery ID or refund ticket ID. Repeated notifications and manual
synchronization converge on the same local receipt. Full and successive partial
refund receipts use the original actual tax-inclusive charge. Native amounts are exact
integer minor units. Platform principal uses cumulative integer allocation, so
the last partial refund closes rounding remainders without exceeding the order.
There is no second buyer platform credit for money returned through a gateway.

Provider method support still controls whether a partial refund can execute.
Pancake's customer ticket API accepts a requested amount, but this does not
establish partial-refund support for every payment method or merchant account.
The current merchant API documentation also lists an existing successful refund
as a possible conflict. Local accounting handles multiple distinct successful
partial receipts, but this is not a promise that the gateway accepts a second
refund request for every charge. The integration retains the pinned SDK customer
ticket flow; it does not silently switch to a separately documented merchant
auto-approval endpoint with different authentication. A pending ticket remains
pending until actual execution is verified.
The native Waffo acquiring refund API is a different integration. Linux DO
remains full-only; arbitrary Epay remains explicitly provider-specific/manual.

## Conflicts and failures

A provider success is persisted before local balances change. An external
refund that conflicts with a local reservation uses `provider_review`. New
refund sends and collection are held until the records can be reconciled.
Only an unsent request (no attempt, or a ready attempt with submit_count=0)
can be superseded. A submitted/unknown request retains its reservation until
its own authoritative result is known. Empty results, timeouts and unrelated
notifications never permit another money-moving POST.

`reconciliation_required` means provider money has been returned but local
settlement has not finished, for example because the seller's platform balance
is insufficient. Its saved proof is retried locally by the existing worker;
no new provider request is sent. A full refund retires delivered inventory;
it never makes delivered secrets available for resale.

Independent dashboard refunds are discovered by signed callback or explicit
seller/root synchronization. This version does not periodically enumerate all
paid orders. A missed callback therefore requires the sync action. Old orders
without genuine charged-amount/PAY evidence remain unavailable for automatic
native refunds; current prices or an ORD are not substituted for missing proof.

## Release gate (not executed against production by this change)

There is no new table or column. The existing phase-seven schema is verified.
The new `provider_review` state nevertheless requires every serving/retained
writer to support capability eight. Retire incompatible writers and qualify
the deployment before the explicit command:

```
api-go merchant-store-writer-gate activate-refund-sync \
  --expected-current 7 --reviewed-refund-sync-ready
```

Use the actual installed binary name. A repeated command with expected-current
8 verifies the schema again. Neither startup, a callback, nor the browser action
activates the feature. An old writer must not be allowed to write after this
activation. Existing capabilities and original-channel requests retain their
separate release requirements.

## Verification boundary

The accompanying tests cover local refund accounting, repeated and concurrent
receipts, PostgreSQL row-lock contention, frozen native-money verification,
SDK HTTP parsing, dashboard/local mixed partial refunds, permission checks,
request-body rejection, pickup login returns and refund panel actions. They
use disposable databases and fake HTTP transports, never real payment funds.

Before production enablement, verify the GraphQL order/payment/refund fields
and count filters against the actual Pancake merchant environment, then test
full and successive partial refunds in its test environment. Check callback
redelivery, a missing callback followed by manual sync, an interrupted request,
unknown in-flight overlap and insufficient local seller funds. Source tests
are not evidence of live provider acceptance or a deployed fix.

Primary provider contracts (checked 2026-10-10):

- [Pinned SDK customer API](https://github.com/waffo-com/waffo-pancake-sdk-go/tree/v0.11.0), including its GraphQL guide sections 4–5.
- [Orders, payments and executed refunds](https://docs.waffo.ai/api-reference/endpoints/graphql/orders-and-payments): filter refunds by `paymentId`; use actual `pspAmountDetails`.
- [Refund webhook contracts](https://docs.waffo.ai/api-reference/webhooks): `refundedAmount` is the returned amount; `originalChargedAmount` is the original charge.
- [Merchant refund ticket API](https://docs.waffo.ai/api-reference/endpoints/refunds/create-refund-ticket): separate merchant approval rules and existing-refund conflicts.

The dependency remains pinned. The public guides and pinned customer SDK describe
different approval paths; test the configured customer flow in the merchant test
environment before enabling it. No undocumented money-moving endpoint or
credential substitution is added.
