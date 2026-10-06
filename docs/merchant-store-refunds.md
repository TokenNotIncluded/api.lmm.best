# Merchant store refunds

This source change does not activate refunds, change a production schema, or
submit a payment-provider request. Refund writes require the protected writer
floor **4**. The central runtime and reviewed `activate-refunds` command must
also support capability 4 before exposing the feature. Unsupported historical
writers must not serve payment callbacks after activation: their payment-issue
rescue can turn an unknown terminal status into `reconciliation_pending`.

Activation must verify all columns and indexes of `merchant_store_refunds`,
`merchant_store_refund_items` and `merchant_store_refund_payment_bases`, as well
as the separately implemented purchase-limit columns. Existing order, payment,
wallet and delivery history must be retained. This patch adds no columns to
historical orders and performs no destructive migration.

Authenticated buyer, original seller and current root administrator may read
`GET /api/store/orders/:id/refunds`. Buyer requests use `POST` on the same path.
Seller/root decisions use `POST /:refund_id/decision`; proactive refunds use
`POST /proactive`. Only the buyer may cancel a requested refund through
`POST /:refund_id/cancel`. An ordinary administrator has no cross-owner authority.

Requests contain `request_key`, `reason` and `mode`: `full`, `quantity` or
`amount`. Quantity accepts `quantity` and optional exact `stock_ids`. Amount
accepts integer `amount_quota` for balance payments, or integer `amount_minor`
for a payment method with a verified native basis. Fields for other modes are
rejected. Retry with the same key and exact typed body; a different body conflicts.
Read `max_quantity`, eligible item IDs and remaining totals from the authorized
view. Item IDs never include plaintext card data. Eligible item positions are stable
original order ordinals, including retired cards, not product inventory positions.
The authorized Claim response pairs `items` with `item_stock_ids` and
`item_positions`; public claim metadata does not expose these item IDs.

Anonymous pickup access uses `POST /api/store/pickup/refunds/read` with
`order_id`, private `token` and optional `code`; requesting adds `input` at
`POST /request`. It respects the original login/code requirements, does not call
Claim, and never sets `claimed_at`. Refund beneficiaries are frozen by the
original order. This does not introduce anonymous checkout by itself.

Every requested or approved refund reserves its quota, verified native amount
and selected card IDs. Rejection/cancellation releases requested reservations.
Balance approval atomically returns the original frozen net principal from the
seller to the buyer, records a transfer tied to the original order, and retires
selected cards. When buyer equals seller, the wallet delta is zero with a full
refund audit record, including when that wallet is empty. Original sale and fee
transfers are retained; the seller bears the original fee. `retained_fee_quota`
on a refund is original-order policy metadata, not an additional fee transfer.
Do not sum it across partial refund records.

Quantity refunds revoke the exact original order/product/variant cards. Amount
adjustments retain cards until cumulative completed refunds reach the original
principal; the final completion retires all remaining cards. Retired stock has
state `refunded`, preserves its order and ciphertext, and never becomes
available inventory. Original quantity, price, paid timestamp and provider
receipt remain unchanged. Lifetime seller sales remain counted; the separate
buyer purchase limit may subtract actual retired quantity.

External approval is `awaiting_provider`, with order status `refund_pending`.
It never credits the buyer's platform wallet. Unselected cards remain claimable;
selected approved cards are held. Without a verified original *charged* native
basis, only a full request is accepted, with native amount zero meaning unknown.
A checkout quote or present-day exchange rate cannot supply that missing basis.
Verified Pancake bases permit partial native requests; generic Epay/LDC remain
full-only. These capabilities do not imply automatic provider submission.

Trusted adapters alone may record a verified payment basis, complete a verified
refund, or reject a verified terminal failed refund. There are no browser routes
for those primitives. Actual success proof is persisted before local platform
wallet reconciliation. Insufficient seller funds leave `reconciliation_required`
with the proof and reservations retained; retry the same proof locally, never
submit another provider refund. Missing evidence, timeouts and ticket-only
status changes do not release a reservation or mean completion.

Native completions allocate original quota using cumulative integer floor
`floor(original quota * cumulative returned native / original charged native)`.
Outstanding reservations are normalized after cancellation/completion; the
last full refund receives the exact remainder. Tiny native refunds may return
zero quota while still recording real returned cash. External merchant cash
refunds do not move wallet principal; platform cash refunds reverse only the
seller principal previously credited, keeping the original fee.

Partial completion restores `paid` unless another approved external refund is
outstanding. Full cumulative completion is terminal `refunded`. Both payment
completion and verified-payment-issue rescue acknowledge the same original
receipt in `refund_pending`/`refunded` without changing money, stock or status;
a different receipt conflicts.
