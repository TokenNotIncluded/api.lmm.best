# Product test mode

`merchant_store_products.test_mode BOOLEAN NOT NULL DEFAULT false` is the only
schema addition for this feature. Existing products stay in normal mode. No
table or index is added and historical balances, orders and inventory are retained.

The existing product POST/PUT accepts optional boolean `test_mode`. New products
default to false; omitting the field during an edit preserves the existing flag.
Explicit JSON null or other non-boolean values return 422 `STORE_INVALID_INPUT`.
Saving a change clears the AI review token and creates a draft requiring a fresh
normal review after test mode is disabled. An explicit mode change can withdraw
a pending submission. Editing a paused or off-shelf test keeps its trading stop.

Only the product's authenticated seller can read a test product or open a new
order for it. Other users, including administrators and root, cannot read its
private editor, inventory, AI results or preview and do not see it in review
queues. Public lists, search, pagination and direct details exclude test products
even when an old or stale writer leaves their status published. The authenticated
owner preview is `GET /api/store/my/products/:id/preview`; it uses the normal safe
product/payment projection and never returns inventory contents or gateway secrets.
Public `GET /api/store/config` declares `product_test_mode_supported: true` so a
frontend talking to an older backend can hide the unsupported control.

A test product in draft, pending or published status can be purchased by its
seller. Paused, off-shelf and rejected products cannot open a new test order.
Submitting, reviewing, publicly relisting or promoting a test product is rejected;
it does not enqueue an AI listing review. Non-owner admin actions are denied
without disclosing the private product. Normal products also allow seller
self-purchase, according to the same checkout rules.

Test mode does not simulate settlement. Minimum price, full-price affordability,
merchant fee, sales cap, inventory, selected payment channels, category masters,
disclaimer, pickup protection and real provider receipt checks all remain active.
Buyer, seller and fee-recipient wallet locks are sorted and deduplicated when IDs
coincide. Existing order idempotency is checked before current test-mode policy;
changing the flag cannot invalidate a frozen invoice, paid claim or same-input
retry. Frozen delivery templates and payment-provider identity remain unchanged.

All backend nodes must support test mode before it is used. Older backends ignore
the new column and may expose or sell a published test; additive schema compatibility
alone does not establish safe mixed-version runtime behavior. Rollback to an old backend is not safe while an enabled test product is reachable
by it: even a draft can be submitted by the old writer, which ignores this flag.
Keep a test-aware backend, or fence the older backend from all product browsing,
editing, submission/review/publication and new checkout until compatibility is
restored; retain frozen payment and paid claim fulfillment. Do not automatically
clear private flags to make rollback possible. The frontend capability flag does
not enforce privacy across mixed backend versions.
