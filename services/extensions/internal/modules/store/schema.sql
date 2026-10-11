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
-- Schema v2: money units are mutually exclusive; integer credits are never
-- accepted through the external minor-unit branch.
ALTER TABLE lmm_store.variants ADD CONSTRAINT store_variant_price_unit CHECK (COALESCE((jsonb_typeof(body->'price')='object' AND (((body->'price'->>'unit')='credits' AND (body->'price'->>'amount_credits') ~ '^(0|[1-9][0-9]*)$' AND (body->'price'->>'amount_credits')::numeric<=9223372036854775807 AND ((body->'price')-ARRAY['amount_credits','unit'])='{}'::jsonb) OR ((body->'price'->>'currency') ~ '^[A-Z]{3}$' AND (body->'price'->>'amount_minor') ~ '^(0|[1-9][0-9]*)$' AND (body->'price'->>'amount_minor')::numeric<=9223372036854775807 AND ((body->'price')-ARRAY['amount_minor','currency'])='{}'::jsonb))),false));
ALTER TABLE lmm_store.orders ADD CONSTRAINT store_order_price_units CHECK (COALESCE((jsonb_typeof(body->'total')='object' AND (((body->'total'->>'unit')='credits' AND (body->'total'->>'amount_credits') ~ '^(0|[1-9][0-9]*)$' AND (body->'total'->>'amount_credits')::numeric<=9223372036854775807 AND ((body->'total')-ARRAY['amount_credits','unit'])='{}'::jsonb) OR ((body->'total'->>'currency') ~ '^[A-Z]{3}$' AND (body->'total'->>'amount_minor') ~ '^(0|[1-9][0-9]*)$' AND (body->'total'->>'amount_minor')::numeric<=9223372036854775807 AND ((body->'total')-ARRAY['amount_minor','currency'])='{}'::jsonb))),false) AND COALESCE((jsonb_typeof(body->'unit_price')='object' AND (((body->'unit_price'->>'unit')='credits' AND (body->'unit_price'->>'amount_credits') ~ '^(0|[1-9][0-9]*)$' AND (body->'unit_price'->>'amount_credits')::numeric<=9223372036854775807 AND ((body->'unit_price')-ARRAY['amount_credits','unit'])='{}'::jsonb) OR ((body->'unit_price'->>'currency') ~ '^[A-Z]{3}$' AND (body->'unit_price'->>'amount_minor') ~ '^(0|[1-9][0-9]*)$' AND (body->'unit_price'->>'amount_minor')::numeric<=9223372036854775807 AND ((body->'unit_price')-ARRAY['amount_minor','currency'])='{}'::jsonb))),false) AND CASE WHEN body->'total'->>'unit'='credits' THEN body->'unit_price'->>'unit'='credits' AND (body->'total'->>'amount_credits')::numeric=(body->'unit_price'->>'amount_credits')::numeric*(body->>'quantity')::numeric ELSE body->'total'->>'currency'=body->'unit_price'->>'currency' AND (body->'total'->>'amount_minor')::numeric=(body->'unit_price'->>'amount_minor')::numeric*(body->>'quantity')::numeric END);
-- Public resource and buyer request identities are globally unambiguous.
CREATE UNIQUE INDEX store_product_public_id ON lmm_store.products (id);
CREATE UNIQUE INDEX store_variant_public_id ON lmm_store.variants (id);
CREATE UNIQUE INDEX store_order_public_id ON lmm_store.orders (id);
COMMIT;
