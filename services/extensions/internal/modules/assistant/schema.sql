-- Fresh installation only. Run explicitly using a module-only database role.
CREATE SCHEMA lmm_assistant;
CREATE TABLE lmm_assistant.contract (singleton boolean PRIMARY KEY CHECK (singleton), version integer NOT NULL CHECK (version = 2));
INSERT INTO lmm_assistant.contract VALUES (true, 2);
CREATE TABLE lmm_assistant.records (
    scope text NOT NULL CHECK (length(scope) BETWEEN 1 AND 256),
    bucket text NOT NULL CHECK (bucket IN ('sessions','calls','preferences','entries')),
    id text NOT NULL CHECK (length(id) BETWEEN 1 AND 256),
    body jsonb NOT NULL CHECK (jsonb_typeof(body) = 'object' AND octet_length(body::text) <= 16384),
    PRIMARY KEY (scope, bucket, id)
);
-- There are no user, credential, wallet, balance or ledger tables here.

-- Version 2 is a fresh-only transcript contract, not an automatic upgrade.
-- Numeric IDs stay within JavaScript's exact integer range for the legacy Web.
CREATE TABLE lmm_assistant.conversations (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id BETWEEN 1 AND 9007199254740991),
    user_id bigint NOT NULL CHECK (user_id > 0),
    title text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 120),
    last_message_preview text NOT NULL CHECK (char_length(last_message_preview) BETWEEN 1 AND 120),
    created_at bigint NOT NULL CHECK (created_at > 0),
    updated_at bigint NOT NULL CHECK (updated_at >= created_at),
    archived_at bigint NOT NULL DEFAULT 0 CHECK (archived_at >= 0),
    restricted_at bigint NOT NULL DEFAULT 0 CHECK (restricted_at >= 0)
);
CREATE INDEX assistant_history_owner_page ON lmm_assistant.conversations(user_id, updated_at DESC, id DESC);
CREATE TABLE lmm_assistant.history_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY CHECK (id BETWEEN 1 AND 9007199254740991),
    conversation_id bigint NOT NULL REFERENCES lmm_assistant.conversations(id),
    sequence bigint NOT NULL CHECK (sequence > 0),
    role text NOT NULL CHECK (role IN ('user','assistant')),
    content text NOT NULL CHECK (char_length(content) BETWEEN 1 AND 4000),
    created_at bigint NOT NULL CHECK (created_at > 0),
    UNIQUE(conversation_id, sequence)
);
CREATE TABLE lmm_assistant.turn_receipts (
    user_id bigint NOT NULL CHECK (user_id > 0),
    client_turn_id text NOT NULL CHECK (length(client_turn_id) BETWEEN 1 AND 64),
    conversation_id bigint NOT NULL REFERENCES lmm_assistant.conversations(id),
    input_hash bytea NOT NULL CHECK (octet_length(input_hash)=32),
    answer_hash bytea NOT NULL CHECK (octet_length(answer_hash)=32),
    PRIMARY KEY(user_id, client_turn_id)
);
CREATE TABLE lmm_assistant.preset_stats (
    preset_id text PRIMARY KEY CHECK (preset_id IN ('ai_recommendation','getting_started','new_user_gift','weekly_discount')),
    clicks bigint NOT NULL DEFAULT 0 CHECK (clicks BETWEEN 0 AND 9007199254740991)
);
INSERT INTO lmm_assistant.preset_stats(preset_id) VALUES
    ('ai_recommendation'),('getting_started'),('new_user_gift'),('weekly_discount');
