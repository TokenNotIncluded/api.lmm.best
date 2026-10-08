# Merchant store original-channel refund evidence

This candidate adds a read-only provider evidence layer and request builders.
It does not dispatch refunds, expose a provider-completion HTTP endpoint, alter
wallets/orders/stock, or add a schema migration. Refund approval and accounting
belong to the independent refund core. The SDK remains pinned to v0.11.0.

## Documented capabilities

| Original method | Submission contract | Partial refund | Execution query | Signed result notification |
| --- | --- | --- | --- | --- |
| Platform balance | Core atomic wallet refund | Supported by core | Local ledger | Not a payment provider |
| Platform Waffo Pancake | Customer refund ticket, followed by provider review/execution | Requested amount supported; actual payment method may limit it | GraphQL ticket and PSP refund records | `refund.succeeded` / `refund.failed` |
| Merchant Waffo Pancake | Same API, scoped to the original merchant credentials/store/customer | Same condition | Same, using the encrypted original order snapshot | Same, checked against the original merchant/store/order |
| Platform Linux DO | POST `https://credit.linux.do/epay/api.php` using original credentials, `trade_no` and original LDC `money` | **Full only** | No documented independent refund-status query | No documented refund callback |
| Arbitrary merchant Epay | Gateway-specific; no universal refund endpoint established | Unknown; manual/provider-specific verification required | Not established | Not established |

These are provider capabilities, not a claim that automatic dispatch is wired
or available. The service's `MerchantStoreRefundProviderSupport` has
`submission`, `partial`, `execution_query`, `signed_callback` and
`requires_payment_basis`. No field exposes credentials or enables dispatch.

Current merchant Epay configuration only accepts CNY. Platform Linux DO has its
own explicit LDC configuration. An arbitrary Epay gateway must not inherit LDC
capabilities or a currency conversion merely from its name or URL.

Primary evidence:

- [Pinned Pancake customer API](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.11.0/customer.go): `CreateRefundTicket` takes a `PAY_` identifier, a native requested amount/currency and an immutable refund-ticket business reference. The response is a ticket, not money returned.
- [Pinned customer transport](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.11.0/customer_http_client.go): customer calls use Bearer plus `X-Environment`. This version does not set an idempotency header for these calls.
- [Pinned GraphQL guide](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.11.0/docs/graphql-guide.md): `refundTickets` provides requested amount and payment/original-order linkage; `refunds` provides the business references and `pspAmountDetails`. The adapter selects these documented fields only.
- [Pinned webhook guide](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.11.0/docs/webhook-guide.md): signed refund events carry original order and refund-ticket business references. A delivery ID is not a stable PSP refund ID.
- [Official Waffo billing capabilities](https://waffo.com/en/billing): full/partial refunds depend on the payment method.
- [Official Linux DO API](https://credit.linux.do/docs/api), sections 3.1–3.3: refund `money` must equal the original LDC payment, while ordinary payment-query status 0/404 is ambiguous. The payment success callback does not prove a refund.

The SDK's Epay library links `payment.moe/doc.html` as its payment documentation.
That page could not be retrieved in this investigation, and the pinned library
only implements purchase/payment-signature verification. No generic Epay refund
path is inferred from third-party examples.

## Exact frozen money and identity

The original order's `ProviderTradeID` is a Pancake **ORD** reference. A refund
ticket requires the actual **PAY** reference. The order's `AmountMinor` is the
frozen pre-tax checkout quote; it is not a verified actual charged amount.
Likewise `priceSnapshot.total` is not accepted as actual channel charge evidence.

The refund core owns an immutable payment basis: original receipt reference,
payment reference, actual charged native minor amount, currency and evidence
hash. Its refund request freezes the partial/full native amount. This service
takes those records through `MerchantStoreRefundNativePayment` and
`MerchantStoreRefundNativeRequest`; it never derives a refund from current FX,
gateway options, a USD display value, or current product price.

The original encrypted gateway snapshot must still match the order's provider,
quote, currency, frozen FX and account scope. Subsequent merchant method disable,
product unlisting/deletion, or price changes do not replace the original
credentials or payment identity needed to honor existing orders.

All currently supported store contracts use 100 native minor units per display
unit (USD/CNY cents or LDC hundredths). Parsing is integer/decimal checked;
unsupported precision, nonpositive amounts and amounts above the original
actual charge are rejected. Platform credits remain independently authoritative
for platform principal accounting; this layer neither credits nor debits them.

## Signed original payment basis

`VerifyMerchantStorePancakeRefundPaymentBasis` uses the pinned SDK to verify the
entire raw body with the frozen environment's key and existing timestamp
window. It then compares the original ORD receipt, store, buyer identity, native
currency, trade number and four store metadata keys.

After verification, it additionally decodes `chargedAmount` from the signed raw
data. It requires `paymentStatus=succeeded`, a valid PAY ID and a positive
actual charge. The field is documented in the official
[v0.16.0 webhook type](https://github.com/waffo-com/waffo-pancake-sdk-go/blob/v0.16.0/types.go)
alongside `refundedAmount` and `originalChargedAmount`. The dependency is not
upgraded: raw-data verification in v0.11.0 supports decoding additive fields.

Missing actual charge/PAY evidence is unavailable. The adapter does not fall
back to deprecated `amount`, list price, quote plus estimated tax, or a guessed
PAY ID. Existing orders can remain pending/manual until genuine evidence is
available. The basis verifier does not settle the original order or write rows.

## Provider observations and actual completion

The five service states are observations:

- `refund_requested`: a matching ticket is pending/reviewed or returned.
- `pending`: approved/processing, or an execution query is still required.
- `succeeded`: a verified native PSP execution and its stable refund ID exist.
- `failed`: a matching provider ticket/execution reports failure; this is not completed-refund evidence and does not itself release a core reservation.
- `unknown`: timeout, GraphQL error, missing/ambiguous records, inconsistent identity/amount, or unknown provider status. Keep the approved request held.

A ticket saying `succeeded` is still `pending` until the separate execution
record is verified. A signed refund success notification only wakes a query:
v0.11.0 does not establish its delivery UUID/event ID as the PSP refund ID.
The query checks exactly one ticket linked to the frozen PAY/original ORD,
requested native amount/currency and refund business reference, plus exactly one
PSP execution with matching original trade number, business reference and
actual refunded native amount/currency. Multiple candidates and GraphQL errors
remain unresolved. No buyer email is selected or returned.

Only `result.VerifiedEvidence()` can expose completion input (PAY, stable refund
reference, native amount/currency, evidence hash) to the core's server-only
completion primitive. Creating a plain `State: succeeded` result does not create
evidence. Public result JSON excludes the native evidence and provider IDs.
The evidence hash identifies verified immutable payment/refund facts, bound to
the original account/order scope. It is stable when a delivery UUID, timestamp
or ticket status changes; it is not a claim to be the original raw-body hash.
Retain raw signed evidence separately if a later audit archive requires it;
never put credentials, buyer data or raw provider bodies in public/log output.

The core records verified provider success durably before attempting local
merchant-wallet reconciliation. Its `reconciliation_required` means funds have
already been returned by the provider but local accounting is incomplete.
Retry that same proof locally; never issue a second provider refund.
`VerifiedFailure()` is separate and only comes from a matching terminal failed
PSP execution query with the exact ticket/PAY/ORD/request linkage. The core may
use it through its server-only rejection primitive if it has no success proof.
A ticket-only rejection, notification, timeout or empty query does not produce
this evidence. Failed executions may report zero actually moved; their requested
native amount still comes from the verified matching ticket.

## Dispatch handoff still required

`BuildMerchantStorePancakeRefundTicket` is pure: it never makes HTTP requests.
The execution owner must atomically claim an approved durable refund before
sending it. Use the immutable refund ID as
`RefundTicketMerchantExternalID`. A timeout is unknown, never permission to
rotate this ID or create another ticket. Even an empty subsequent query is not
proof that the original request never reached an eventually consistent provider.
Rejected-ticket resubmission uses the known TKT and original PAY; it is an
explicit decision, not an automatic retry loop.

Before dispatch integration, add a dedicated customer transport rather than
relaxing the checkout transport. It must pin HTTPS `api.waffo.ai:443`, only the
documented create/resubmit-ticket paths, original customer/store/environment,
a newly minted server-only session token, no redirect/proxy/private-IP bypass,
and bounded request/response bodies. Preserve only this minted Bearer on those
paths; strip cookies/proxy authorization. Existing merchant RSA GraphQL queries
retain their separate protected transport and fresh reads.

Linux DO's documented synchronous `code=1` response can acknowledge an
authenticated full-refund request, but a timeout cannot be resolved through the
ordinary payment status-0/not-found response. Its unavailable partial/query/
callback/timeout-idempotency contract must remain explicit; no platform-wallet
credit substitutes for an original-channel partial refund.

## Scope and verification

New source: two service files, focused local service tests and this document.
No controller/router/model/schema/SDK dependency/current payment callback change.
Tests exercise real pinned SDK signature/GraphQL parsing through local fake
transports, immutable native money, tenant/buyer/order/PAY/refund linkage, ticket
versus execution, partial mismatches, repeated reads and ambiguous errors. They
do not perform real payment/refund/email or use a production database.

Production readiness additionally requires core integration, durable dispatch
ownership/unknown recovery, actual provider account/method capability checks,
and the ordinary release/preservation gates. Source and fake-transport tests
are not evidence of a real refund or a deployed automatic worker.
