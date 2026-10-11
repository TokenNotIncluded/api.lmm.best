-- Explicit installation-owner migration. No automatic startup DDL.
-- Duplicate IDs must be reviewed before installation; do not delete records
-- to force this unique index build to succeed. Any failure rolls back.
BEGIN;
CREATE UNIQUE INDEX store_product_public_id ON lmm_store.products (id);
CREATE UNIQUE INDEX store_variant_public_id ON lmm_store.variants (id);
CREATE UNIQUE INDEX store_order_public_id ON lmm_store.orders (id);
COMMIT;
