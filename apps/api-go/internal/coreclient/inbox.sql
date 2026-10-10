-- Install explicitly in an EXTENSION-OWNED PostgreSQL database, never core DB.
CREATE SCHEMA extension_events;
CREATE TABLE extension_events.inbox (
    consumer_id text NOT NULL CHECK (length(consumer_id) BETWEEN 1 AND 64),
    event_id bigint NOT NULL CHECK (event_id > 0),
    content_sha256 bytea NOT NULL CHECK (octet_length(content_sha256)=32),
    completed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(consumer_id,event_id)
);
-- Do not expire receipts while events can still be replayed. The handler's
-- local effects and this row must commit together; no in-progress row is saved.
