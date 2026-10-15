-- OAuth v1: explicit fresh installation, with a read-only lmm public-client issuer.
-- The lmm-pi foundation remains unissued and is not advertised by this slice.
CREATE SCHEMA core_oauth;
REVOKE ALL ON SCHEMA core_oauth FROM PUBLIC;
CREATE TABLE core_oauth.contract (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version INTEGER NOT NULL CHECK (version = 1),
    ddl_fingerprint BYTEA NOT NULL CHECK (octet_length(ddl_fingerprint) = 32),
    catalog_fingerprint BYTEA NOT NULL CHECK (octet_length(catalog_fingerprint) = 32)
);
-- Pure format validation; no authority is created by this function.
CREATE FUNCTION core_oauth.readonly_scope_set(values_ TEXT[]) RETURNS BOOLEAN
LANGUAGE plpgsql IMMUTABLE STRICT SET search_path=pg_catalog AS $$
DECLARE value_ TEXT; encoded_ TEXT; raw_ BYTEA; group_ TEXT; reads_ INTEGER:=0; groups_ INTEGER:=0;
BEGIN
 IF cardinality(values_) NOT BETWEEN 2 AND 66 OR cardinality(values_)<>(SELECT count(DISTINCT v) FROM unnest(values_) v) THEN RETURN FALSE; END IF;
 FOREACH value_ IN ARRAY values_ LOOP
  IF value_ IN ('catalog:read','balance:read') THEN reads_:=reads_+1;
  ELSE
   IF value_ !~ '^group:[A-Za-z0-9_-]{2,86}$' THEN RETURN FALSE; END IF;
   encoded_:=substring(value_ FROM 7);
   raw_:=decode(translate(encoded_,'-_','+/')||repeat('=',(4-length(encoded_)%4)%4),'base64');
   IF octet_length(raw_) NOT BETWEEN 1 AND 64 OR rtrim(translate(replace(encode(raw_,'base64'),E'\n',''),'+/','-_'),'=')<>encoded_ THEN RETURN FALSE; END IF;
   group_:=convert_from(raw_,'UTF8');
   -- Match Rust char::is_control independent of the installation LC_CTYPE.
   -- PostgreSQL text/UTF8 conversion already rejects U+0000.
   IF group_ COLLATE "C" ~ U&'[\0001-\001F\007F-\009F]' THEN RETURN FALSE; END IF;
   groups_:=groups_+1;
  END IF;
 END LOOP;
 RETURN reads_ BETWEEN 1 AND 2 AND groups_ BETWEEN 1 AND 64;
EXCEPTION WHEN data_exception THEN RETURN FALSE;
END $$;
REVOKE ALL ON FUNCTION core_oauth.readonly_scope_set(TEXT[]) FROM PUBLIC;
CREATE TABLE core_oauth.families (
    id TEXT PRIMARY KEY CHECK (id ~ '^lmm_gr_[0-9a-f]{64}$'),
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    owner_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    user_version BIGINT NOT NULL CHECK (user_version > 0),
    consent_version BIGINT NOT NULL DEFAULT 1 CHECK (consent_version > 0),
    issuer TEXT NOT NULL CHECK (octet_length(issuer) BETWEEN 1 AND 256),
    resource TEXT NOT NULL CHECK (resource = issuer || '/api/oauth2'),
    client_id TEXT NOT NULL CHECK (client_id IN ('lmm-pi','lmm')),
    scopes TEXT[] NOT NULL CHECK ((client_id='lmm-pi' AND cardinality(scopes) BETWEEN 1 AND 7 AND scopes <@ ARRAY['catalog:read','balance:read','usage:read','models:invoke','mcp:bounties','mcp:drawing','remote:control']::text[]) OR (client_id='lmm' AND core_oauth.readonly_scope_set(scopes))),
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

-- Each browser stage has its own immutable binding, one CSRF and identity snapshot.
CREATE TABLE core_oauth.authorization_requests (
 binding_digest BYTEA PRIMARY KEY CHECK(octet_length(binding_digest)=32),
 csrf_digest BYTEA NOT NULL UNIQUE CHECK(octet_length(csrf_digest)=32),
 client_id TEXT NOT NULL CHECK(client_id='lmm'),
 issuer TEXT NOT NULL CHECK(octet_length(issuer) BETWEEN 1 AND 256),
 resource TEXT NOT NULL CHECK(resource=issuer||'/api/oauth2'),
 redirect_uri TEXT NOT NULL CHECK(octet_length(redirect_uri) BETWEEN 1 AND 256),
 state TEXT NOT NULL CHECK(octet_length(state) BETWEEN 1 AND 256),
 pkce_challenge TEXT NOT NULL CHECK(pkce_challenge ~ '^[A-Za-z0-9_-]{43}$'),
 requested_scopes TEXT[] NOT NULL CHECK(requested_scopes=ARRAY['catalog:read','balance:read']::TEXT[]),
 user_id BIGINT REFERENCES core_identity.users(id),
 owner_account_id BIGINT REFERENCES core_identity.accounts(id),
 user_version BIGINT CHECK(user_version>0),
 sid TEXT REFERENCES core_identity.web_sessions(sid),
 granted_scopes TEXT[] NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()+interval '300 seconds',
 consumed_at TIMESTAMPTZ,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '300 seconds'),
 CHECK((user_id IS NULL AND owner_account_id IS NULL AND user_version IS NULL AND sid IS NULL AND cardinality(granted_scopes)=0)
  OR (user_id IS NOT NULL AND owner_account_id IS NOT NULL AND user_version IS NOT NULL AND sid IS NOT NULL
      AND ARRAY['catalog:read','balance:read']::TEXT[] <@ granted_scopes AND core_oauth.readonly_scope_set(granted_scopes)))
);
CREATE INDEX authorization_request_expiry ON core_oauth.authorization_requests(expires_at);
CREATE TABLE core_oauth.authorization_codes (
 digest BYTEA PRIMARY KEY CHECK(octet_length(digest)=32),
 family_id TEXT NOT NULL REFERENCES core_oauth.families(id),
 user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
 owner_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
 user_version BIGINT NOT NULL CHECK(user_version>0),
 consent_version BIGINT NOT NULL CHECK(consent_version>0),
 issuer TEXT NOT NULL CHECK(octet_length(issuer) BETWEEN 1 AND 256),
 resource TEXT NOT NULL CHECK(resource=issuer||'/api/oauth2'),
 client_id TEXT NOT NULL CHECK(client_id='lmm'),
 redirect_uri TEXT NOT NULL CHECK(octet_length(redirect_uri) BETWEEN 1 AND 256),
 state TEXT NOT NULL CHECK(octet_length(state) BETWEEN 1 AND 256),
 pkce_challenge TEXT NOT NULL CHECK(pkce_challenge ~ '^[A-Za-z0-9_-]{43}$'),
 scopes TEXT[] NOT NULL CHECK(ARRAY['catalog:read','balance:read']::TEXT[] <@ scopes AND core_oauth.readonly_scope_set(scopes)),
 created_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
 expires_at TIMESTAMPTZ NOT NULL,
 consumed_at TIMESTAMPTZ,
 CHECK(expires_at>created_at AND expires_at<=created_at+interval '120 seconds')
);
CREATE INDEX authorization_code_family ON core_oauth.authorization_codes(family_id);
CREATE FUNCTION core_oauth.immutable_browser_authorization() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
 IF (to_jsonb(NEW)-'consumed_at') IS DISTINCT FROM (to_jsonb(OLD)-'consumed_at')
    OR OLD.consumed_at IS NOT NULL OR NEW.consumed_at IS NULL THEN
  RAISE EXCEPTION 'immutable authorization snapshot' USING ERRCODE='23514';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER authorization_request_immutable BEFORE UPDATE ON core_oauth.authorization_requests
 FOR EACH ROW EXECUTE FUNCTION core_oauth.immutable_browser_authorization();
CREATE TRIGGER authorization_code_immutable BEFORE UPDATE ON core_oauth.authorization_codes
 FOR EACH ROW EXECUTE FUNCTION core_oauth.immutable_browser_authorization();
REVOKE ALL ON core_oauth.authorization_requests,core_oauth.authorization_codes FROM PUBLIC;
REVOKE ALL ON FUNCTION core_oauth.immutable_browser_authorization() FROM PUBLIC;
