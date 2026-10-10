-- Fresh PostgreSQL schema only. Run through the offline initializer in one transaction.
-- No credentials are stored here. References are registered after the secret service
-- has durably stored the corresponding immutable credential revision.
CREATE TABLE routing_credential_refs (
    id bigint NOT NULL CHECK (id > 0),
    revision bigint NOT NULL CHECK (revision > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (id, revision)
);

-- Documents are the immutable publication artifacts. Normalized projections below
-- are generated FROM these artifacts in the same transaction, never separately edited.
CREATE TABLE routing_price_versions (
    version bigint PRIMARY KEY CHECK (version > 0),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    sealed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (octet_length(document::text) <= 16777216),
    CHECK ((document->>'version')::bigint = version)
);
CREATE TABLE routing_config_versions (
    version bigint PRIMARY KEY CHECK (version > 0),
    price_version bigint NOT NULL REFERENCES routing_price_versions(version),
    document jsonb NOT NULL CHECK (jsonb_typeof(document) = 'object'),
    sealed boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (octet_length(document::text) <= 16777216),
    CHECK ((document->>'version')::bigint = version)
);
CREATE TABLE routing_active_release (
    singleton boolean PRIMARY KEY DEFAULT true CHECK (singleton),
    config_version bigint REFERENCES routing_config_versions(version)
);
INSERT INTO routing_active_release(singleton) VALUES (true);

CREATE TABLE routing_price_rules (
    version bigint NOT NULL REFERENCES routing_price_versions(version),
    id bigint NOT NULL CHECK (id > 0),
    model text NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    group_name text NOT NULL CHECK (length(group_name) BETWEEN 1 AND 64),
    speed text NOT NULL CHECK (speed IN ('standard', 'fast', 'ultrafast')),
    -- Integer micro-USD per million DISJOINT tokens; request_rate is per request.
    input_rate bigint NOT NULL CHECK (input_rate >= 0),
    output_rate bigint NOT NULL CHECK (output_rate >= 0),
    cache_read_rate bigint NOT NULL CHECK (cache_read_rate >= 0),
    cache_write_rate bigint NOT NULL CHECK (cache_write_rate >= 0),
    request_rate bigint NOT NULL CHECK (request_rate >= 0),
    group_multiplier_ppm bigint NOT NULL CHECK (group_multiplier_ppm BETWEEN 1 AND 1000000000),
    speed_multiplier_ppm bigint NOT NULL CHECK (speed_multiplier_ppm BETWEEN 1 AND 1000000000),
    PRIMARY KEY (version, id),
    UNIQUE (version, group_name, model, speed)
);
CREATE TABLE routing_groups (
    version bigint NOT NULL REFERENCES routing_config_versions(version),
    name text NOT NULL CHECK (length(name) BETWEEN 1 AND 64),
    enabled boolean NOT NULL,
    PRIMARY KEY (version, name)
);
CREATE TABLE routing_upstreams (
    version bigint NOT NULL REFERENCES routing_config_versions(version),
    id bigint NOT NULL CHECK (id > 0),
    endpoint text NOT NULL CHECK (endpoint ~ '^https://' AND endpoint !~ '[@?#\\[:space:]]'),
    protocol text NOT NULL CHECK (protocol IN ('openai_chat', 'openai_responses', 'anthropic_messages', 'gemini')),
    credential_id bigint NOT NULL,
    credential_revision bigint NOT NULL,
    enabled boolean NOT NULL,
    PRIMARY KEY (version, id),
    FOREIGN KEY (credential_id, credential_revision) REFERENCES routing_credential_refs(id, revision)
);
CREATE TABLE routing_routes (
    version bigint NOT NULL REFERENCES routing_config_versions(version),
    id bigint NOT NULL CHECK (id > 0),
    group_name text NOT NULL,
    model text NOT NULL CHECK (length(model) BETWEEN 1 AND 200),
    enabled boolean NOT NULL,
    speeds text[] NOT NULL CHECK (cardinality(speeds) BETWEEN 1 AND 3 AND speeds <@ ARRAY['standard','fast','ultrafast']::text[]),
    max_attempts smallint NOT NULL CHECK (max_attempts BETWEEN 1 AND 8),
    retry_on text[] NOT NULL CHECK (retry_on <@ ARRAY['connect','timeout','rate_limited','server_error']::text[]),
    allow_replay_after_send boolean NOT NULL,
    PRIMARY KEY (version, id),
    UNIQUE (version, group_name, model),
    FOREIGN KEY (version, group_name) REFERENCES routing_groups(version, name)
);
CREATE TABLE routing_targets (
    version bigint NOT NULL,
    id bigint NOT NULL CHECK (id > 0),
    route_id bigint NOT NULL,
    upstream_id bigint NOT NULL,
    upstream_model text NOT NULL CHECK (length(upstream_model) BETWEEN 1 AND 200),
    enabled boolean NOT NULL,
    priority integer NOT NULL CHECK (priority BETWEEN 0 AND 65535),
    weight bigint NOT NULL CHECK (weight BETWEEN 0 AND 4294967295),
    speeds text[] NOT NULL CHECK (cardinality(speeds) BETWEEN 1 AND 3 AND speeds <@ ARRAY['standard','fast','ultrafast']::text[]),
    payload text NOT NULL CHECK (payload IN ('passthrough', 'adapt')),
    timeout_ms integer NOT NULL CHECK (timeout_ms BETWEEN 1 AND 3600000),
    PRIMARY KEY (version, id),
    UNIQUE (version, route_id, upstream_id),
    FOREIGN KEY (version, route_id) REFERENCES routing_routes(version, id),
    FOREIGN KEY (version, upstream_id) REFERENCES routing_upstreams(version, id)
);
CREATE TABLE routing_aliases (
    version bigint NOT NULL,
    group_name text NOT NULL,
    alias text NOT NULL CHECK (length(alias) BETWEEN 1 AND 200),
    model text NOT NULL,
    PRIMARY KEY (version, group_name, alias),
    FOREIGN KEY (version, group_name, model) REFERENCES routing_routes(version, group_name, model)
);

-- Lock the parent before changing a projection. Publication cannot race an append.
CREATE FUNCTION routing_guard_projection() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
    v bigint;
    is_sealed boolean;
BEGIN
    IF TG_OP = 'UPDATE' AND NEW.version <> OLD.version THEN
        RAISE EXCEPTION 'routing version is immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN v := OLD.version; ELSE v := NEW.version; END IF;
    IF TG_ARGV[0] = 'price' THEN
        SELECT sealed INTO is_sealed FROM routing_price_versions WHERE version = v FOR SHARE;
    ELSE
        SELECT sealed INTO is_sealed FROM routing_config_versions WHERE version = v FOR SHARE;
    END IF;
    IF is_sealed IS DISTINCT FROM false THEN
        RAISE EXCEPTION 'routing snapshot is sealed' USING ERRCODE = '23514';
    END IF;
    IF TG_OP = 'DELETE' THEN RETURN OLD; ELSE RETURN NEW; END IF;
END;
$$;
CREATE TRIGGER routing_price_rules_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_price_rules
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('price');
CREATE TRIGGER routing_groups_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_groups
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('config');
CREATE TRIGGER routing_upstreams_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_upstreams
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('config');
CREATE TRIGGER routing_routes_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_routes
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('config');
CREATE TRIGGER routing_targets_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_targets
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('config');
CREATE TRIGGER routing_aliases_guard BEFORE INSERT OR UPDATE OR DELETE ON routing_aliases
    FOR EACH ROW EXECUTE FUNCTION routing_guard_projection('config');

CREATE FUNCTION routing_guard_version() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'routing snapshot is immutable' USING ERRCODE = '23514';
    END IF;
    IF OLD.sealed OR NEW.version <> OLD.version OR NEW.document IS DISTINCT FROM OLD.document THEN
        RAISE EXCEPTION 'routing snapshot is immutable' USING ERRCODE = '23514';
    END IF;
    IF TG_TABLE_NAME = 'routing_config_versions'
        AND to_jsonb(NEW)->'price_version' IS DISTINCT FROM to_jsonb(OLD)->'price_version' THEN
        RAISE EXCEPTION 'routing price reference is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER routing_price_versions_guard BEFORE UPDATE OR DELETE ON routing_price_versions
    FOR EACH ROW EXECUTE FUNCTION routing_guard_version();
CREATE TRIGGER routing_config_versions_guard BEFORE UPDATE OR DELETE ON routing_config_versions
    FOR EACH ROW EXECUTE FUNCTION routing_guard_version();

CREATE FUNCTION routing_guard_active() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE next_price bigint; old_price bigint; ready boolean;
BEGIN
    IF TG_OP = 'DELETE' THEN
        RAISE EXCEPTION 'routing active slot cannot be removed' USING ERRCODE = '23514';
    END IF;
    IF NEW.config_version IS NULL OR (OLD.config_version IS NOT NULL AND NEW.config_version <= OLD.config_version) THEN
        RAISE EXCEPTION 'routing version must increase' USING ERRCODE = '40001';
    END IF;
    SELECT c.price_version, c.sealed AND p.sealed INTO next_price, ready
        FROM routing_config_versions c JOIN routing_price_versions p ON p.version = c.price_version
        WHERE c.version = NEW.config_version;
    SELECT price_version INTO old_price FROM routing_config_versions WHERE version = OLD.config_version;
    IF ready IS DISTINCT FROM true OR next_price < old_price THEN
        RAISE EXCEPTION 'routing release is not ready' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END;
$$;
CREATE TRIGGER routing_active_guard BEFORE UPDATE OR DELETE ON routing_active_release
    FOR EACH ROW EXECUTE FUNCTION routing_guard_active();

-- Stage a complete independent price snapshot without changing active traffic.
CREATE FUNCTION routing_stage_prices(p_document jsonb) RETURNS void LANGUAGE plpgsql AS $$
DECLARE v bigint := (p_document->>'version')::bigint; existing jsonb; inserted bigint;
BEGIN
    INSERT INTO routing_price_versions(version, document) VALUES (v, p_document)
        ON CONFLICT (version) DO NOTHING RETURNING version INTO inserted;
    IF inserted IS NULL THEN
        SELECT document INTO existing FROM routing_price_versions WHERE version = v AND sealed;
        IF existing IS DISTINCT FROM p_document THEN
            RAISE EXCEPTION 'price version already exists' USING ERRCODE = '23505';
        END IF;
        RETURN;
    END IF;
    INSERT INTO routing_price_rules
        SELECT v, (r->>'id')::bigint, r->>'model', r->>'group', r->>'speed',
            (r->'rates'->>'input')::bigint, (r->'rates'->>'output')::bigint,
            (r->'rates'->>'cache_read')::bigint, (r->'rates'->>'cache_write')::bigint,
            (r->'rates'->>'request')::bigint,
            (r->>'group_multiplier_ppm')::bigint, (r->>'speed_multiplier_ppm')::bigint
        FROM jsonb_array_elements(p_document->'rules') r;
    UPDATE routing_price_versions SET sealed = true WHERE version = v;
END;
$$;

-- Rust MUST validate the full route + price combination before calling this function.
-- The active row lock serializes publication; stale writers cannot overwrite a release.
CREATE FUNCTION routing_publish_config(
    p_document jsonb, p_price bigint, p_expected_config bigint, p_expected_price bigint
) RETURNS void LANGUAGE plpgsql AS $$
DECLARE
    v bigint := (p_document->>'version')::bigint;
    old_config bigint;
    old_price bigint;
BEGIN
    SELECT config_version INTO old_config FROM routing_active_release WHERE singleton FOR UPDATE;
    SELECT price_version INTO old_price FROM routing_config_versions WHERE version = old_config;
    IF old_config IS DISTINCT FROM p_expected_config OR old_price IS DISTINCT FROM p_expected_price
        OR (old_config IS NOT NULL AND v <= old_config) THEN
        RAISE EXCEPTION 'routing publication conflict' USING ERRCODE = '40001';
    END IF;
    IF NOT EXISTS (SELECT 1 FROM routing_price_versions WHERE version = p_price AND sealed)
        OR (old_price IS NOT NULL AND p_price < old_price) THEN
        RAISE EXCEPTION 'routing price version is unavailable' USING ERRCODE = '23514';
    END IF;
    INSERT INTO routing_config_versions(version, price_version, document) VALUES (v, p_price, p_document);
    INSERT INTO routing_groups
        SELECT v, g->>'name', (g->>'enabled')::boolean FROM jsonb_array_elements(p_document->'groups') g;
    INSERT INTO routing_upstreams
        SELECT v, (u->>'id')::bigint, u->>'endpoint', u->>'protocol',
            (u->'credential'->>'id')::bigint, (u->'credential'->>'revision')::bigint,
            (u->>'enabled')::boolean FROM jsonb_array_elements(p_document->'upstreams') u;
    INSERT INTO routing_routes
        SELECT v, (r->>'id')::bigint, r->>'group', r->>'model', (r->>'enabled')::boolean,
            ARRAY(SELECT jsonb_array_elements_text(r->'speeds')),
            (r->'failover'->>'max_attempts')::smallint,
            ARRAY(SELECT jsonb_array_elements_text(r->'failover'->'retry_on')),
            (r->'failover'->>'allow_replay_after_send')::boolean
        FROM jsonb_array_elements(p_document->'routes') r;
    INSERT INTO routing_targets
        SELECT v, (t->>'id')::bigint, (r->>'id')::bigint, (t->>'upstream_id')::bigint,
            t->>'upstream_model', (t->>'enabled')::boolean, (t->>'priority')::integer,
            (t->>'weight')::bigint, ARRAY(SELECT jsonb_array_elements_text(t->'speeds')),
            t->>'payload', (t->>'timeout_ms')::integer
        FROM jsonb_array_elements(p_document->'routes') r,
            LATERAL jsonb_array_elements(r->'targets') t;
    INSERT INTO routing_aliases
        SELECT v, a->>'group', a->>'alias', a->>'model'
        FROM jsonb_array_elements(p_document->'aliases') a;
    UPDATE routing_config_versions SET sealed = true WHERE version = v;
    UPDATE routing_active_release SET config_version = v WHERE singleton;
END;
$$;

-- Go extensions never receive the core role. Grant only explicit Rust roles at install.
REVOKE ALL ON routing_credential_refs, routing_price_versions, routing_config_versions,
    routing_active_release, routing_price_rules, routing_groups, routing_upstreams,
    routing_routes, routing_targets, routing_aliases FROM PUBLIC;
REVOKE ALL ON FUNCTION routing_stage_prices(jsonb) FROM PUBLIC;
REVOKE ALL ON FUNCTION routing_publish_config(jsonb, bigint, bigint, bigint) FROM PUBLIC;
