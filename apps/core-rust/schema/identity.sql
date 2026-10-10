-- Fresh installation only. The installer supplies the transaction and contract hash.
CREATE SCHEMA core_meta;
CREATE TABLE core_meta.schema_contract (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version INTEGER NOT NULL CHECK (version > 0),
    fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint) = 32),
    installed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

CREATE SCHEMA core_identity;
CREATE TABLE core_identity.accounts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind TEXT NOT NULL CHECK (kind IN ('personal', 'team')),
    service_level SMALLINT NOT NULL DEFAULT 0 CHECK (service_level BETWEEN 0 AND 4),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (id, kind)
);
CREATE TABLE core_identity.users (
    id BIGINT PRIMARY KEY CHECK (id > 0),
    personal_account_id BIGINT NOT NULL UNIQUE,
    account_kind TEXT GENERATED ALWAYS AS ('personal'::text) STORED,
    platform_role TEXT NOT NULL DEFAULT 'user'
        CHECK (platform_role IN ('user', 'admin', 'superadmin')),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    auth_version BIGINT NOT NULL DEFAULT 1 CHECK (auth_version > 0),
    FOREIGN KEY (personal_account_id, account_kind) REFERENCES core_identity.accounts(id, kind)
);
CREATE TABLE core_identity.teams (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL UNIQUE,
    account_kind TEXT GENERATED ALWAYS AS ('team'::text) STORED,
    owner_user_id BIGINT NOT NULL UNIQUE REFERENCES core_identity.users(id),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    version BIGINT NOT NULL DEFAULT 1 CHECK (version > 0),
    FOREIGN KEY (account_id, account_kind) REFERENCES core_identity.accounts(id, kind)
);
-- This row is an authorization generation, not a budget counter. Never delete it
-- on removal, and never reset financial usage when the generation changes.
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
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    digest BYTEA NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    kind TEXT NOT NULL CHECK (kind IN ('session', 'api_key')),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    user_version BIGINT NOT NULL CHECK (user_version > 0),
    owner_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    personal_billing_preference TEXT
        CHECK (personal_billing_preference IN ('subscription_first', 'wallet_first', 'subscription_only', 'wallet_only')),
    expires_at TIMESTAMPTZ NOT NULL,
    revoked_at TIMESTAMPTZ,
    CHECK (kind = 'api_key' OR personal_billing_preference IS NULL)
);
CREATE INDEX credentials_user ON core_identity.credentials(user_id, id);
CREATE INDEX credentials_owner ON core_identity.credentials(owner_account_id, id);
CREATE TABLE core_identity.key_funding_rules (
    credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    position SMALLINT NOT NULL CHECK (position BETWEEN 0 AND 15),
    payer_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    PRIMARY KEY (credential_id, position),
    UNIQUE (credential_id, payer_account_id)
);
CREATE TABLE core_identity.credential_grants (
    credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    team_id BIGINT NOT NULL REFERENCES core_identity.teams(id),
    team_version BIGINT NOT NULL CHECK (team_version > 0),
    membership_version BIGINT NOT NULL CHECK (membership_version >= 0),
    PRIMARY KEY (credential_id, team_id)
);
CREATE TABLE core_identity.invites (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    digest BYTEA NOT NULL UNIQUE CHECK (octet_length(digest) = 32),
    team_id BIGINT NOT NULL REFERENCES core_identity.teams(id),
    team_version BIGINT NOT NULL CHECK (team_version > 0),
    recipient_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    recipient_version BIGINT NOT NULL CHECK (recipient_version >= 0),
    inviter_credential_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    inviter_membership_version BIGINT NOT NULL CHECK (inviter_membership_version >= 0),
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    can_spend BOOLEAN NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    accepted_at TIMESTAMPTZ
);
CREATE INDEX invites_recipient ON core_identity.invites(recipient_user_id, id)
    WHERE accepted_at IS NULL;
CREATE TABLE core_identity.audit (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    credential_id BIGINT REFERENCES core_identity.credentials(id),
    action TEXT NOT NULL CHECK (length(action) BETWEEN 1 AND 128),
    target JSONB NOT NULL CHECK (jsonb_typeof(target) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX audit_actor ON core_identity.audit(actor_user_id, id);

-- Account-local subscription/wallet order is not a second ordered list of payers.
-- This is policy only, not a balance table or a completed billing engine.
CREATE SCHEMA core_billing;
CREATE TABLE core_billing.account_policies (
    account_id BIGINT PRIMARY KEY REFERENCES core_identity.accounts(id),
    preference TEXT NOT NULL DEFAULT 'wallet_only'
        CHECK (preference IN ('subscription_first', 'wallet_first', 'subscription_only', 'wallet_only')),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0)
);

-- Enforce ownership at the storage boundary. Current permissions are still
-- checked by Rust on every request; these constraints do not grant authority.
CREATE FUNCTION core_identity.check_credential_owner() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE owner_kind TEXT; personal_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE' AND (NEW.user_id, NEW.owner_account_id, NEW.kind, NEW.digest)
        IS DISTINCT FROM (OLD.user_id, OLD.owner_account_id, OLD.kind, OLD.digest) THEN
        RAISE EXCEPTION 'credential identity is immutable' USING ERRCODE = '23514';
    END IF;
    SELECT kind INTO STRICT owner_kind FROM core_identity.accounts WHERE id = NEW.owner_account_id;
    SELECT personal_account_id INTO STRICT personal_id FROM core_identity.users WHERE id = NEW.user_id;
    IF (owner_kind = 'personal' AND NEW.owner_account_id <> personal_id)
        OR (NEW.kind = 'session' AND NEW.owner_account_id <> personal_id)
        OR (owner_kind = 'team' AND NEW.personal_billing_preference IS NOT NULL) THEN
        RAISE EXCEPTION 'invalid credential owner' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER credential_owner BEFORE INSERT OR UPDATE ON core_identity.credentials
    FOR EACH ROW EXECUTE FUNCTION core_identity.check_credential_owner();

CREATE FUNCTION core_identity.check_funding_rule() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE key_kind TEXT; owner_id BIGINT; owner_kind TEXT; payer_kind TEXT;
BEGIN
    SELECT c.kind, c.owner_account_id, a.kind INTO STRICT key_kind, owner_id, owner_kind
        FROM core_identity.credentials c JOIN core_identity.accounts a ON a.id = c.owner_account_id
        WHERE c.id = NEW.credential_id;
    SELECT kind INTO STRICT payer_kind FROM core_identity.accounts WHERE id = NEW.payer_account_id;
    IF key_kind <> 'api_key'
        OR (owner_kind = 'team' AND (NEW.position <> 0 OR NEW.payer_account_id <> owner_id))
        OR (payer_kind = 'personal' AND NEW.payer_account_id <> owner_id) THEN
        RAISE EXCEPTION 'invalid funding rule' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER funding_rule BEFORE INSERT OR UPDATE ON core_identity.key_funding_rules
    FOR EACH ROW EXECUTE FUNCTION core_identity.check_funding_rule();
