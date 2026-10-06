# Merchant store variants: candidate contract

This is an independent feature candidate, based on Go87 plus the normal merchant
self-purchase fix. It does not authorize a production migration or activation.

## Product, variants, and compatibility

A product is a listing container. Merchants use arbitrary combination names,
for example `Basic · 1 month` and `Plus · 2 months`; no Cartesian matrix is
generated. Each variant has its own positive integer credit price, delivery
template, and inventory. Payment selection, pickup protections, random or
sequential delivery, listing review, pause/off-shelf status, promotion, and the
lifetime cumulative sale limit remain product-level rules.

Every product retains one stable default variant. Its ID is a deterministic UUID
derived from the product ID. A GET returns a virtual default when no durable
default exists; it never inserts or updates rows. A product-locked write can
lazily create that row. Existing stock with NULL or empty `variant_id` belongs
logically to this default. No old stock ID, product ID, ciphertext, state, order
ID, position, or delivered plaintext is rewritten. Legacy product price/template
edits update only the default variant; they never overwrite other variants.

New multi-variant purchases must send an exact `variant_id`. A missing ID is
accepted only for the legacy single-default product, or when replaying an
already existing historic/default request. A nondefault order replay still
requires its exact variant ID. An unknown, wrong-product, disabled, or sold-out
variant never falls back to default or another variant. Default requests retain
the old digest; a nondefault ID is part of the digest, so changing the selected
variant while reusing a request key conflicts.

There may be up to 200 durable variants per product, including default and
disabled variants. Variants are disabled instead of deleted. Creating or editing
a variant's name/price/template retires the product to draft and invalidates its
previous AI/manual review. An enabled toggle only starts/stops an already
reviewed variant. A listing submission needs at least one enabled valid variant,
but does not require imported stock. Zero available stock dynamically pauses
sales and never prevents replenishing inventory. Every newly saved variant price
and every newly purchased enabled variant must satisfy the current configured
minimum unit price; quantity never conceals an underpriced unit.

## Schema increment

One new table `merchant_store_variants`:

| Column | Type | Rule |
| --- | --- | --- |
| id | varchar(36) | primary key |
| product_id | varchar(36) | not null, index |
| name | varchar(200) | not null; nonempty, at most 200 UTF-8 bytes |
| price_quota | bigint | not null, positive integer credits |
| template | varchar(32) | not null, existing supported template enum |
| enabled | boolean | not null; explicitly written |
| created_at | bigint | timestamp |
| updated_at | bigint | timestamp |

Existing stock receives only nullable `variant_id varchar(36)`. A new lookup
index `store_stock_variant_available` contains
`product_id,variant_id,state,position`. The existing stock index is retained.
There is no stock backfill or new foreign-key rewrite.

Existing orders receive `variant_id varchar(36) NOT NULL DEFAULT ''` and
`variant_name varchar(200) NOT NULL DEFAULT ''`. Existing `unit_price_quota` and
`delivery_template` already freeze unit price and format. New orders freeze
variant ID/name and that variant's price/template. Historic empty fields stay
empty and are shown as historic/default delivery, never guessed from the
product's current variants. Order, claim metadata, and claim responses expose
these safe frozen fields; metadata never contains inventory secrets.

## Response and routes

Product responses add `default_variant_id`, `variants`, `inventory_total`,
`inventory_available`, `price_min_quota`, and `price_max_quota`. Active variant
prices determine the range; legacy `price_quota` remains the default price.
Merchant views include all variants. Public views include enabled variants only.

Each variant includes `id`, `product_id`, `name`, `price_quota`, `template`,
`enabled`, `created_at`, `updated_at`, and derived `is_default`,
`inventory_total`, `inventory_available`, `reserved_stock`, `sale_available`,
`trading_paused`. Secret text is never part of these projections.

`inventory_total` counts unconsumed actual items: available plus reserved.
`inventory_available` counts currently unreserved items. Merchant product counts
equal the sum across all variants, including disabled ones. Product
`sale_available` is the total currently eligible available stock clipped once by
the shared remaining sale limit. Each variant's `sale_available` is that
variant's current purchase capacity under the same shared remaining limit;
adding these values is not a valid way to calculate product capacity. Existing
public `available_stock` retains its purchasable-stock semantics.

Existing public product detail contains the variants needed for checkout.
Authenticated merchant endpoints are:

| Method | Path | Body/result |
| --- | --- | --- |
| POST | `/api/store/products/:id/variants` | `{name,price_quota,template,enabled}`; variant |
| PUT | `/api/store/products/:id/variants/:variant_id` | same complete body; variant |
| PUT | `/api/store/products/:id/variants/:variant_id/enabled` | `{enabled}`; variant |
| GET | `/api/store/products/:id/variants/:variant_id/inventory` | existing paginated secret-free stock metadata |
| POST | `/api/store/products/:id/variants/:variant_id/inventory` | `{items:string[]}`; `{added}` |
| DELETE | `/api/store/products/:id/variants/:variant_id/inventory/:stock_id` | available item only |

Existing product inventory import remains a default-only compatibility endpoint.
Existing product inventory listing includes `variant_id` and lists all product
stock. Existing stock deletion keeps its ownership/state guards. All new imports
retain the existing 2 MiB request, 10,000 item, and 32,768 byte/item limits and
validate typed text against the selected variant's template. The shared order
input adds `variant_id`; all ordinary login/disclaimer/payment constraints stay.

## Money, reservation, and historical obligations

Credit is the only wallet/price authority; 1 USD remains 500,000 credits. The
selected variant's exact integer price is multiplied with overflow guards, then
the existing fee and settlement snapshots are created. All variant changes,
stock imports/removals, and purchases lock the product first. Consequently
different variants compete safely for the one shared product sale limit.

Reservation queries select only the exact variant (or NULL/empty/default ID for
the logical default), and claim still selects only the frozen order's delivered
rows. A pending order's existing reservation is honored after variant edits,
disablement, price/minimum changes, pause, off-shelf, and payment-method changes.
Callbacks compare frozen gateway amount/currency and receipt scope and never
read current variant prices. Cancellation or authoritative expiry releases only
the matching order's rows without changing their variant association.

## Activation and N-1 rollback boundary

The old binary reserves stock by product/state without checking variant ID.
Therefore additive DDL alone does not make mixed old/new writers or an active
downgrade safe: an old checkout could deliver another variant's secret.

During gate 1, the default is always virtual and uses the compatibility
product price/template. Neither default checkout/import nor the legacy editor
materializes a default row. This prevents a still-compatible old writer's
product edit from leaving a stale second price/template. Unexpected durable
variant rows at gate 1 fail closed rather than silently overwrite inventory or
pick one of two conflicting prices. Gate 2 activation waits for old shared-lock
transactions; subsequent product-locked writes lazily materialize the stable
default from the latest product snapshot. All variant CRUD, including default
custom name/template/enable controls, explicitly requires gate 2.

Proposed executable rollout is a separate compatibility shim before activation:
an existing Option row records minimum merchant-store writer capability. The
shim supports capability 1; the variant binary supports capability 2. Product
and inventory mutations, submit/review/AI publication, and new checkout acquire
a shared lock on this durable gate inside their transaction and reject a higher
required capability. Missing or malformed gate values fail closed, and reads
use the transaction's database connection rather than the Option cache. A
capability-2 binary additionally requires the persisted gate to equal 2 before
nondefault variant writes, inventory imports, publication, or new orders;
merely having a newer binary is insufficient. Activation exclusively
locks/updates that row to 2, waits for old transactions, and only then permits
variant inventory or orders.
Callbacks, cancellation/reconciliation of frozen orders, and private claim are
not rejected by this new-write gate. The exact shim and deployment commands
must be reviewed separately; this document is not proof that it is installed.

A downgrade may target only a proven shim-equipped N-1 binary, which rejects all
new merchant/product/inventory writes and new orders while preserving frozen
fulfillment. The activation key is reserved: both shim and feature versions
must reject generic Option edits/deletion of this key. The dedicated activation
transaction validates a monotonic increase under its exclusive row lock. Never
lower the gate after variant activation. An older unshimmed
Go86/Go87 binary must be prevented from starting as a writer by the release
transaction. Merely taking listings off shelf is insufficient because an old
writer could publish them again.

Required evidence includes empty-schema DDL, legacy-stock/order preservation,
old-digest replay, wrong-product variant rejection, independent secret delivery,
actual PostgreSQL contention across variants and the shared cap, and a real
shim N-1 gate exercise. Unit/schema fixtures alone do not prove production
preservation or safe rollback.
