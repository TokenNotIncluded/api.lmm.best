# Fixed delivery content with unlimited supply

The `fixed-content` delivery template sells one shared private document without
inventory rows. `text` and `custom-text` keep their existing per-item inventory
behavior. Sellers configure content for each variant; the default variant can
also be configured through the product editor. Prices and variants remain
merchant-defined.

`fixed_content` is a write-only product/variant input, limited to 128 KiB of valid
UTF-8. Empty, NUL-containing and oversized content is rejected. Omitting it when
editing preserves the saved document. Sellers read it through the authenticated
`GET /api/store/products/:id/variants/:variant_id/fixed-content` endpoint. Root
accounts and other buyers cannot read another seller's configuration.

Public products and variants expose `unlimited_supply`, never the document or
ciphertext. Physical inventory totals stay zero. With no sales cap,
`sale_available` is zero and `unlimited_supply` expresses capacity; no inventory
sentinel is used. An explicit sales cap still counts lifetime paid quantities
and pending payment obligations. Per-order and per-buyer limits, visibility,
review, pauses, payment methods, seller terms and login requirements still apply.

Each checkout saves a separately encrypted immutable content snapshot bound to
its order ID. Edits affect later orders. Balance purchases, full discounts and
verified provider payments reuse the existing settlement path while skipping all
stock reservation, delivery and release mutations. Authorized paid pickup returns
one `fixed_content` document and the remaining purchased `quantity`, with empty
item and stock ID arrays. Metadata and order JSON do not reveal the document.

Quantity refunds use the existing refund ledger and do not create card IDs or
refund-item rows. Pending provider refunds withhold their quantity from pickup;
only completed refunds release buyer capacity. Amount adjustments preserve
quantity until all principal is refunded. Refunds never reset lifetime sales.
Native refunds require the existing verified original-charge evidence and use
its currency, integer minor amount and cumulative rounding. Credit principal
remains the frozen order amount; no exchange-rate rebasing is introduced.

## Separate phase-seven installation

Writer capability seven adds exactly four tables. Two contain private content:

- `merchant_store_fixed_contents`: variant-owned encrypted configuration.
- `merchant_store_order_fixed_deliveries`: order-bound encrypted delivery copy.

The other two, `merchant_store_product_traffic_days` and
`merchant_store_product_traffic_receipts`, contain anonymous, bounded-retention
product traffic. See [product analytics](merchant-store-analytics.md).

No columns are added to phase-five or phase-six product/order models. Historical
preparation and runtime verification filter these tables by the durable floor.
Installation does not raise that floor. After qualifying every serving and
retained writer, the private operator commands are:

```sh
lmm-api merchant-store-writer-gate prepare-fixed-content --expected-current=6 --reviewed-fixed-content-ready
lmm-api merchant-store-writer-gate verify-fixed-content
lmm-api merchant-store-writer-gate activate-fixed-content --expected-current=6 --reviewed-fixed-content-ready
```

Preparation is shop-only transactional DDL. Verification is read-only. Activation
checks the complete installed schema and performs only the adjacent 6-to-7
compare-and-swap; it never repairs missing schema. Deployment advisory locks,
durable owners and existing writer floor locks continue to fence these actions.
The existing capability-one and phase-six qualifiers are unchanged. Passing local
phase-seven tests is not proof of production installation or activation.

Run `TestMerchantStoreFixedContent*` for stockless payment, privacy, snapshots,
refunds and real PostgreSQL lock/DDL invariants. PostgreSQL tests require an
explicit disposable literal-loopback `MERCHANT_STORE_POSTGRES_TEST_DSN` and own a
new schema for each fixture; they never use the runtime production DSN.
