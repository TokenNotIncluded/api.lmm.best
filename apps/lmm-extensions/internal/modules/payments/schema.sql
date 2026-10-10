-- Initial schema only. Install in a NEW dedicated payment database with its own
-- login role. This is not a core migration, and contains no balance table.
CREATE TABLE payment_storage_guard (
 singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
 contract text NOT NULL CHECK (contract = 'lmm-payments-v1')
);
INSERT INTO payment_storage_guard VALUES (true, 'lmm-payments-v1');
CREATE TABLE payment_orders (
 id text PRIMARY KEY,
 namespace text NOT NULL,
 payment_id text,
 version bigint NOT NULL CHECK (version > 0),
 snapshot jsonb NOT NULL CHECK (jsonb_typeof(snapshot) = 'object'),
 due_at timestamptz,
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (snapshot->>'id' = id),
 CHECK (snapshot->>'namespace' = namespace),
 CHECK (octet_length(snapshot::text) <= 1048576)
);
CREATE UNIQUE INDEX payment_transaction_owner ON payment_orders(namespace, payment_id) WHERE payment_id IS NOT NULL;
CREATE INDEX payment_work_due ON payment_orders(due_at, id) WHERE due_at IS NOT NULL;
CREATE TABLE payment_events (
 event_key text PRIMARY KEY,
 digest text NOT NULL,
 snapshot jsonb NOT NULL,
 status text NOT NULL CHECK (status IN ('pending', 'done', 'review')),
 error_code text NOT NULL DEFAULT '',
 next_attempt timestamptz NOT NULL,
 received_at timestamptz NOT NULL DEFAULT now(),
 updated_at timestamptz NOT NULL DEFAULT now(),
 CHECK (octet_length(snapshot::text) <= 131072)
);
CREATE INDEX payment_event_work ON payment_events(next_attempt, event_key) WHERE status = 'pending';
