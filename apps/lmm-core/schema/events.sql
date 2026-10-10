-- Fresh database component. Final integration must install this explicitly.
-- No automatic migration, core balance access, or dependency on a live Go host.
CREATE SCHEMA core_events;
CREATE TABLE core_events.capacity (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    max_events bigint NOT NULL CHECK (max_events > 0),
    max_bytes bigint NOT NULL CHECK (max_bytes > 0),
    pending_events bigint NOT NULL DEFAULT 0 CHECK (pending_events BETWEEN 0 AND max_events),
    pending_bytes bigint NOT NULL DEFAULT 0 CHECK (pending_bytes BETWEEN 0 AND max_bytes)
);
INSERT INTO core_events.capacity(singleton,max_events,max_bytes) VALUES (true,100000,268435456);
CREATE TABLE core_events.subscriptions (
    consumer_id text PRIMARY KEY CHECK (length(consumer_id) BETWEEN 1 AND 64),
    service_id text NOT NULL CHECK (length(service_id) BETWEEN 1 AND 64),
    event_types text[] NOT NULL CHECK (cardinality(event_types) BETWEEN 1 AND 32),
    max_pending bigint NOT NULL CHECK (max_pending > 0),
    pending bigint NOT NULL DEFAULT 0 CHECK (pending BETWEEN 0 AND max_pending)
);
CREATE TABLE core_events.outbox (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id > 0),
    event_key text NOT NULL UNIQUE CHECK (length(event_key) BETWEEN 1 AND 128),
    event_type text NOT NULL CHECK (length(event_type) BETWEEN 1 AND 96),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    resource_id bigint NOT NULL CHECK (resource_id > 0),
    resource_version bigint NOT NULL CHECK (resource_version >= 0),
    payload bytea CHECK (octet_length(payload) <= 16384),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256) = 32),
    pending_deliveries bigint NOT NULL DEFAULT 0 CHECK (pending_deliveries >= 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (payload IS NOT NULL OR pending_deliveries = 0)
);
CREATE INDEX outbox_retained_type ON core_events.outbox(event_type,id) WHERE payload IS NOT NULL;
CREATE TABLE core_events.deliveries (
    consumer_id text NOT NULL REFERENCES core_events.subscriptions(consumer_id),
    event_id bigint NOT NULL REFERENCES core_events.outbox(id),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    available_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    lease_digest bytea CHECK (octet_length(lease_digest) = 32),
    lease_until timestamptz,
    acknowledged_at timestamptz,
    retry_reason smallint CHECK (retry_reason BETWEEN 1 AND 3),
    PRIMARY KEY (consumer_id,event_id),
    CHECK (lease_until IS NULL OR lease_digest IS NOT NULL),
    CHECK (acknowledged_at IS NULL OR lease_digest IS NOT NULL)
);
CREATE INDEX deliveries_ready ON core_events.deliveries(consumer_id,available_at,event_id)
    WHERE acknowledged_at IS NULL;
-- Both event tombstones and acknowledgement receipts are retained. Deleting
-- these reopens the replay window. Capacity bounds UNACKNOWLEDGED payloads.
CREATE TABLE core_events.inbox (
    service_id text NOT NULL CHECK (length(service_id) BETWEEN 1 AND 64),
    actor_id bigint NOT NULL CHECK (actor_id > 0),
    method text NOT NULL CHECK (length(method) BETWEEN 1 AND 128),
    idempotency_key text NOT NULL CHECK (length(idempotency_key) BETWEEN 16 AND 128),
    request_sha256 bytea NOT NULL CHECK (octet_length(request_sha256) = 32),
    response bytea CHECK (octet_length(response) <= 16384),
    completed_at timestamptz,
    PRIMARY KEY(service_id,actor_id,method,idempotency_key),
    CHECK ((response IS NULL) = (completed_at IS NULL))
);
CREATE FUNCTION core_events.require_complete_inbox() RETURNS trigger
LANGUAGE plpgsql SET search_path = pg_catalog,core_events AS $$
BEGIN
    IF EXISTS (SELECT 1 FROM core_events.inbox WHERE service_id=NEW.service_id
        AND actor_id=NEW.actor_id AND method=NEW.method
        AND idempotency_key=NEW.idempotency_key AND response IS NULL) THEN
        RAISE EXCEPTION 'command inbox must complete in its business transaction'
            USING ERRCODE='23514';
    END IF;
    RETURN NULL;
END;
$$;
CREATE CONSTRAINT TRIGGER inbox_complete_at_commit
    AFTER INSERT OR UPDATE ON core_events.inbox DEFERRABLE INITIALLY DEFERRED
    FOR EACH ROW EXECUTE FUNCTION core_events.require_complete_inbox();
