# Configurable minimum product unit price

The minimum product unit price is stored in `merchant_store_configs.minimum_unit_price_quota` as integer CREDIT. The initial policy is 500,000 CREDIT, which is one USD under the unchanged fixed credit denomination. It is an initial default, not a permanently hardcoded checkout threshold.

The single additive column is `BIGINT NOT NULL DEFAULT 500000`, with no new table or index. PostgreSQL 18 also records its NOT NULL constraint. Existing configuration rows receive the initial value when the column is added. Existing product prices, accounts, orders and financial records are not rewritten. If the configuration singleton has not been created, the existing read-only config mechanism returns the initial policy without creating a row. An explicitly stored zero remains zero and cancels the extra price floor; product unit prices must still be positive.

`GET /api/store/config` exposes `minimum_unit_price_quota`. Only Root can supply the optional field in `PUT /api/store/config`, using a nonnegative JavaScript-safe integer. A partial fee/promotion edit merges its supplied fields under the configuration transaction and preserves an omitted minimum price. Explicit zero is included in the map upsert, including on the first configuration insert; GORM struct defaults must not replace it with 500,000.

New product saves, submissions and new orders check the current configured minimum against the product's unit price. Multiplying a below-minimum unit price by quantity cannot bypass the policy. Public product availability reflects the same minimum. Raising the minimum may pause new transactions for a previously approved low-price product; its stored price, review history and inventory remain intact. The merchant can adjust the price and submit it for review, or Root can lower the configured minimum.

An existing order is returned before the current minimum check. Frozen payment initiation/retry, signed callbacks, settlement and pickup continue using the original order price and payment snapshot. Raising the minimum does not reprice or invalidate an already created order.

Price validation locks the existing config row after the product row, and after the already-existing sorted user locks in checkout. Save/submit do not acquire a new wallet lock. Root config writes keep users before config; no config lock is introduced into the early checkout configuration read. Missing-row reads remain read-only.

An older backend ignores this runtime policy even though the additive schema is readable. Do not claim policy enforcement during mixed-version routing. Before rollback while the minimum must remain enforced, stop new store trading through the real payment channel switches; preserve frozen-order fulfillment. The existing merchant master and market-AI rollback constraints continue to apply.
