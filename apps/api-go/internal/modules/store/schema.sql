-- Apply only to a NEW, dedicated extension database, using an installation role.
-- Give the runtime role USAGE on lmm_store and SELECT/INSERT/UPDATE on its tables.
-- Never give this role credentials or grants for the Rust core database.
BEGIN;
CREATE SCHEMA lmm_store;
CREATE TABLE lmm_store.shops (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY (shop_id,id), CHECK (shop_id=id),
 CHECK (jsonb_typeof(body)='object'),
 CHECK (body->'owner'->>'kind' IN ('personal','team')),
 CHECK ((body->'owner'->>'id')::bigint>0)
);
CREATE TABLE lmm_store.products (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object')
);
CREATE TABLE lmm_store.variants (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object'),
 CHECK ((body->>'stock')::bigint>=0), CHECK ((body->>'reserved')::bigint>=0),
 CHECK ((body->>'claimed')::bigint>=0), CHECK ((body->>'quota')::bigint>=-1),
 CHECK ((body->'price'->>'amount_minor')::bigint>=0),
 CHECK (NOT (body->>'track_stock')::boolean OR (body->>'stock')::bigint >= (body->>'reserved')::bigint)
);
CREATE TABLE lmm_store.orders (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object'),
 CHECK ((body->>'quantity')::bigint>0),
 CHECK ((body->'total'->>'amount_minor')::numeric = (body->'unit_price'->>'amount_minor')::numeric * (body->>'quantity')::numeric),
 CHECK (body->>'state' IN ('awaiting_payment','payment_pending','paid','fulfilled','settlement_pending','settled','refund_pending','refunded','cancelled'))
);
CREATE TABLE lmm_store.commands (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object')
);
CREATE TABLE lmm_store.stock_movements (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object')
);
CREATE TABLE lmm_store.money_operations (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object')
);
-- Account IDs are opaque Rust references, NOT foreign keys into core tables.
CREATE INDEX store_orders_buyer ON lmm_store.orders (shop_id, (body->'buyer'->>'kind'), (body->'buyer'->>'id'),id);
CREATE INDEX store_variants_product ON lmm_store.variants (shop_id,(body->>'product_id'),id);
COMMIT;
