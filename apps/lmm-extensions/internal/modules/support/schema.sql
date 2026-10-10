-- Fresh support database only. No core or store database joins or foreign keys.
BEGIN;
CREATE SCHEMA lmm_support;
CREATE TABLE lmm_support.customers (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object'),
 CHECK(body->'buyer'->>'kind' IN ('personal','team')),
 CHECK((body->'buyer'->>'id')::bigint>0)
);
CREATE TABLE lmm_support.conversations (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object'),
 CHECK((body->>'sequence')::bigint>=0)
);
CREATE TABLE lmm_support.messages (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object'),
 CHECK((body->>'author_user_id')::bigint>0),
 CHECK(octet_length(body->>'body') BETWEEN 1 AND 8192)
);
CREATE TABLE lmm_support.commands (
 shop_id text NOT NULL, id text NOT NULL, body jsonb NOT NULL,
 PRIMARY KEY(shop_id,id), CHECK(jsonb_typeof(body)='object')
);
CREATE INDEX support_conversations_customer ON lmm_support.conversations (shop_id,(body->>'customer_id'),id);
CREATE INDEX support_messages_conversation ON lmm_support.messages (shop_id,(body->>'conversation_id'),id);
COMMIT;
