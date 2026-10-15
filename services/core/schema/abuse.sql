-- Explicit fresh-install component. Startup never installs or upgrades it.
CREATE SCHEMA core_abuse;
CREATE TABLE core_abuse.settings (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0),
    document JSONB NOT NULL DEFAULT '{"version":1,"policies":[]}'::jsonb
        CHECK (jsonb_typeof(document)='object' AND octet_length(document::text)<=131072)
);
INSERT INTO core_abuse.settings(singleton) VALUES (TRUE);
CREATE TABLE core_abuse.user_marks (
    user_id BIGINT PRIMARY KEY REFERENCES core_identity.users(id),
    marked BOOLEAN NOT NULL,
    policy_id TEXT,
    revision BIGINT NOT NULL CHECK (revision > 0),
    CHECK ((marked AND policy_id IS NOT NULL) OR (NOT marked AND policy_id IS NULL)),
    CHECK (policy_id IS NULL OR length(policy_id) BETWEEN 1 AND 64)
);
CREATE TABLE core_abuse.previews (
    id TEXT PRIMARY KEY CHECK (length(id)=64),
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    operation_id TEXT NOT NULL CHECK (length(operation_id) BETWEEN 8 AND 128),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest)=32),
    preview_hash BYTEA NOT NULL CHECK (octet_length(preview_hash)=32),
    snapshot JSONB NOT NULL CHECK (jsonb_typeof(snapshot)='object'),
    response JSONB NOT NULL CHECK (jsonb_typeof(response)='object'),
    expires_at TIMESTAMPTZ NOT NULL,
    UNIQUE(actor_user_id,operation_id)
);
CREATE TABLE core_abuse.operations (
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    operation_id TEXT NOT NULL CHECK (length(operation_id) BETWEEN 8 AND 128),
    request_digest BYTEA NOT NULL CHECK (octet_length(request_digest)=32),
    result JSONB NOT NULL CHECK (jsonb_typeof(result)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY(actor_user_id,operation_id)
);
CREATE TABLE core_abuse.audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id BIGINT NOT NULL,
    operation_id TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('policies','users')),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 1000),
    detail JSONB NOT NULL CHECK (jsonb_typeof(detail)='object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    FOREIGN KEY(actor_user_id,operation_id)
        REFERENCES core_abuse.operations(actor_user_id,operation_id),
    UNIQUE(actor_user_id,operation_id)
);
-- Ephemeral resource controls, never wallets, reservations or model charges.
CREATE TABLE core_abuse.model_counters (
    subject_id BIGINT NOT NULL CHECK (subject_id >= 0),
    minute BIGINT NOT NULL CHECK (minute > 0),
    attempts INTEGER NOT NULL CHECK (attempts BETWEEN 1 AND 100001),
    PRIMARY KEY(subject_id,minute)
);
CREATE TABLE core_abuse.model_leases (
    id TEXT PRIMARY KEY CHECK (length(id)=64),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    request_id TEXT NOT NULL CHECK (length(request_id) BETWEEN 1 AND 128),
    expires_at TIMESTAMPTZ NOT NULL,
    finished_at TIMESTAMPTZ,
    UNIQUE(credential_id,request_id)
);
CREATE INDEX abuse_live_leases ON core_abuse.model_leases(expires_at,user_id)
    WHERE finished_at IS NULL;

-- Private evidence captured in the successful account-creation transaction.
-- Historical accounts must not be backfilled from log guesses or sessions.
CREATE TABLE core_abuse.registration_observations (
 user_id BIGINT PRIMARY KEY REFERENCES core_identity.users(id),
 registered_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
 username_at_registration TEXT NOT NULL CHECK(length(username_at_registration) BETWEEN 1 AND 128),
 email_at_registration TEXT NOT NULL CHECK(length(email_at_registration)<=320),
 source_ip INET,
 source_kind TEXT NOT NULL CHECK(source_kind IN ('unknown','socket','trusted_x_forwarded_for')),
 source_policy_version INTEGER,
 CHECK ((source_kind='unknown' AND source_ip IS NULL AND source_policy_version IS NULL)
  OR (source_kind<>'unknown' AND source_ip IS NOT NULL AND source_policy_version=1)),
 CHECK (source_ip IS NULL OR masklen(source_ip)=CASE family(source_ip) WHEN 4 THEN 32 ELSE 128 END)
);
CREATE INDEX registration_observations_time ON core_abuse.registration_observations(registered_at,user_id);
CREATE INDEX registration_observations_ip ON core_abuse.registration_observations(source_ip,registered_at,user_id)
 WHERE source_ip IS NOT NULL;
