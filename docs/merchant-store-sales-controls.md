# Merchant inventory and sales controls

`merchant_store_products.sale_limit` is a nullable BIGINT with no default,
NOT NULL constraint or new index. This is the only persistent schema addition
for these controls. The existing listing status column also accepts `off_shelf`.
No inventory, orders, payments, delivery links or wallets are reset or deleted.

The limit is this product's cumulative sales ceiling, not a batch allowance:

- `null` allows all otherwise available inventory.
- `0` stops new orders.
- A limit of `10` with 100 inventory items permits at most ten paid or reserved
  units. Raising the limit to `20` permits ten more once the first ten are sold.
- Lowering a limit below its already paid or reserved quantity blocks new orders
  while preserving existing orders and their frozen fulfillment obligations.

The authoritative usage is each order's quantity, counted once when it is paid,
has verified payment evidence awaiting resolution, or still holds reserved stock.
Payment state alone does not establish a stock reservation. Verified payment
evidence remains a paid obligation even when it arrives after an earlier confirmed
closure. Such exceptions do not silently consume fresh stock or lose their money
record. A true cancellation or attested provider closure releases unpaid usage;
unknown issued payments keep their holds.

Checkout checks the limit under the product row lock after returning an existing
idempotent order and before reserving new stock or moving money. Payment,
cancellation and verified-payment evidence use the same product-to-order lock
order. Evidence can still be recorded if the original product disappeared, since
there can be no new checkout for that product. Completing existing orders and
collecting their contents never recheck a later sales limit or listing status.

The authenticated owner or an administrator may set a limit using
`PUT /api/store/products/{id}/sale-limit`, with an explicit body such as
`{"sale_limit":10}` or `{"sale_limit":null}`. Omitted values, fractional numbers,
negative numbers and integers beyond JavaScript's safe range are rejected.
Saving product content through an older client preserves the separate limit.
Changing a limit does not change the approved product's content or require a
new content review.

Private product responses return actual unreserved inventory as `available_stock`,
paid obligations as `paid_quantity`, unpaid held quantities as `reserved_quantity`,
and the current purchasable quantity as `sale_available`. Public responses cap
`available_stock` at `sale_available`, so existing clients cannot advertise the
warehouse's hidden excess as purchasable stock. Paused or off-shelf products remain
unavailable for new checkout; the existing pause visibility behavior is retained.

`PUT /api/store/products/{id}/listing` requires an explicit `{"listed":false}`
or `{"listed":true}`. Taking an approved listing off the shelf preserves its
unedited approval, inventory and fulfillment data, but retires its active AI review
token. A completed old review cannot republish the listing. An unchanged off-shelf
listing can be explicitly republished. Editing it first creates a draft and clears
approval; it must then go through the normal submission and review flow.

The schema addition alone does not provide functional rollback compatibility.
Unmodified Go85 ignores `sale_limit` and can continue taking orders above it.
All writers must enforce the limit before any non-null limits are enabled. A
rollback must use a cap-aware compatibility binary, or keep a real shared
new-checkout gate closed while the old binary runs. Merely pausing listings is
insufficient because old pause/resume, edit and review operations can reopen them.
Schema verification, independent full-data preservation, and N-1 runtime policy
are separate release gates. Never replay historical credit rebases for this change.
