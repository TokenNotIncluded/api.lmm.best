-- Isolated core-owned schema. Never migrate the live Go tables in place.
CREATE SCHEMA core_identity;
CREATE TABLE core_identity.schema_version (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version INTEGER NOT NULL CHECK (version > 0)
);
INSERT INTO core_identity.schema_version(version) VALUES (1);
CREATE TABLE core_identity.users (
    id BIGINT PRIMARY KEY CHECK (id > 0),
    platform_level SMALLINT NOT NULL CHECK (platform_level BETWEEN 0 AND 6),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    auth_version BIGINT NOT NULL DEFAULT 1 CHECK (auth_version > 0)
);
CREATE TABLE core_identity.teams (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL UNIQUE REFERENCES core_identity.users(id),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0)
);
-- Owner lives ONLY in teams.owner_user_id. Membership rows survive removal.
CREATE TABLE core_identity.memberships (
    team_id BIGINT NOT NULL REFERENCES core_identity.teams(id),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    can_spend BOOLEAN NOT NULL DEFAULT FALSE,
    active BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (team_id, user_id)
);
CREATE INDEX memberships_user ON core_identity.memberships(user_id, team_id);
CREATE TABLE core_identity.credentials (
    id BIGSERIAL PRIMARY KEY,
    digest BYTEA NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    kind TEXT NOT NULL CHECK (kind IN ('session', 'api_key')),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    user_version BIGINT NOT NULL CHECK (user_version > 0),
    team_id BIGINT REFERENCES core_identity.teams(id),
    funding_policy JSONB,
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK ((kind = 'session' AND team_id IS NULL AND funding_policy IS NULL)
        OR (kind = 'api_key' AND funding_policy IS NOT NULL))
);
CREATE INDEX credentials_user ON core_identity.credentials(user_id, id);
-- Bind grants to membership generations. Removal/rejoin cannot revive old keys.
CREATE TABLE core_identity.credential_grants (
    credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    team_id BIGINT NOT NULL REFERENCES core_identity.teams(id),
    team_version BIGINT NOT NULL CHECK (team_version > 0),
    membership_version BIGINT NOT NULL CHECK (membership_version >= 0),
    PRIMARY KEY (credential_id, team_id)
);
CREATE TABLE core_identity.invites (
    id BIGSERIAL PRIMARY KEY,
    digest BYTEA NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    team_id BIGINT NOT NULL REFERENCES core_identity.teams(id),
    team_version BIGINT NOT NULL,
    recipient_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    recipient_version BIGINT NOT NULL CHECK (recipient_version >= 0),
    inviter_credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    inviter_membership_version BIGINT NOT NULL CHECK (inviter_membership_version >= 0),
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    can_spend BOOLEAN NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ
);
CREATE TABLE core_identity.audit (
    id BIGSERIAL PRIMARY KEY,
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    credential_id BIGINT REFERENCES core_identity.credentials(id),
    action TEXT NOT NULL,
    target JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
