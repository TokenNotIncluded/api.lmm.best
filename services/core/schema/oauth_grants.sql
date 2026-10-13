-- OAuth v1 foundation. Explicit private fixture installation only in phase 1.
CREATE SCHEMA core_oauth;
REVOKE ALL ON SCHEMA core_oauth FROM PUBLIC;
CREATE TABLE core_oauth.contract (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version INTEGER NOT NULL CHECK (version = 1),
    ddl_fingerprint BYTEA NOT NULL CHECK (octet_length(ddl_fingerprint) = 32),
    catalog_fingerprint BYTEA NOT NULL CHECK (octet_length(catalog_fingerprint) = 32)
);
CREATE TABLE core_oauth.families (
    id TEXT PRIMARY KEY CHECK (id ~ '^lmm_gr_[0-9a-f]{64}$'),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    owner_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    user_version BIGINT NOT NULL CHECK (user_version > 0),
    consent_version BIGINT NOT NULL DEFAULT 1 CHECK (consent_version > 0),
    issuer TEXT NOT NULL CHECK (octet_length(issuer) BETWEEN 1 AND 256),
    resource TEXT NOT NULL CHECK (resource = issuer || '/api/oauth2'),
    client_id TEXT NOT NULL CHECK (client_id = 'lmm-pi'),
    scopes TEXT[] NOT NULL CHECK (cardinality(scopes) BETWEEN 1 AND 7 AND scopes <@ ARRAY['catalog:read','balance:read','usage:read','models:invoke','mcp:bounties','mcp:drawing','remote:control']::text[]),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '365 days')
);
CREATE TABLE core_oauth.codes (
    digest BYTEA PRIMARY KEY CHECK (octet_length(digest) = 32),
    family_id TEXT NOT NULL REFERENCES core_oauth.families(id),
    consent_version BIGINT NOT NULL CHECK (consent_version > 0),
    redirect_uri TEXT NOT NULL CHECK (octet_length(redirect_uri) BETWEEN 1 AND 256),
    state TEXT NOT NULL CHECK (octet_length(state) BETWEEN 1 AND 256),
    pkce_challenge TEXT NOT NULL CHECK (pkce_challenge ~ '^[A-Za-z0-9_-]{43}$'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '300 seconds')
);
CREATE TABLE core_oauth.access_tokens (
    id TEXT PRIMARY KEY CHECK (id ~ '^lmm_ac_[0-9a-f]{64}$'),
    digest BYTEA NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    family_id TEXT NOT NULL REFERENCES core_oauth.families(id),
    consent_version BIGINT NOT NULL CHECK (consent_version > 0),
    scopes TEXT[] NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    expires_at TIMESTAMPTZ NOT NULL,
    CHECK (expires_at > created_at AND expires_at <= created_at + interval '86400 seconds')
);
CREATE TABLE core_oauth.refresh_tokens (
    digest BYTEA PRIMARY KEY CHECK (octet_length(digest) = 32),
    family_id TEXT NOT NULL REFERENCES core_oauth.families(id),
    consent_version BIGINT NOT NULL CHECK (consent_version > 0),
    scopes TEXT[] NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    consumed_at TIMESTAMPTZ
);
CREATE INDEX codes_family ON core_oauth.codes(family_id);
CREATE INDEX access_family ON core_oauth.access_tokens(family_id);
CREATE INDEX refresh_family ON core_oauth.refresh_tokens(family_id);
REVOKE ALL ON ALL TABLES IN SCHEMA core_oauth FROM PUBLIC;
