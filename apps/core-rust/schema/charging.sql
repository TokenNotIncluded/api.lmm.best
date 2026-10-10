-- Fresh installation only. Install after identity.sql in the final installer.
-- No wallet, balance, migration, Go write path, or automatic production install.
CREATE SCHEMA core_charging;

-- Half-open UTC windows. Calendar weeks start on Monday. Anniversary months
-- always derive from the original anchor (Jan 31 -> Feb 28 -> Mar 31).
CREATE FUNCTION core_charging.period_window(p_period TEXT, p_anchor BIGINT,
    p_seconds BIGINT, p_at BIGINT) RETURNS TABLE(start_at BIGINT, end_at BIGINT)
LANGUAGE plpgsql IMMUTABLE STRICT SET search_path = pg_catalog AS $$
DECLARE t TIMESTAMP; a TIMESTAMP; s TIMESTAMP; e TIMESTAMP; n INTEGER;
BEGIN
    t := to_timestamp(p_at) AT TIME ZONE 'UTC';
    CASE p_period
    WHEN 'day' THEN s := date_trunc('day', t); e := s + interval '1 day';
    WHEN 'week' THEN s := date_trunc('week', t); e := s + interval '1 week';
    WHEN 'month' THEN s := date_trunc('month', t); e := s + interval '1 month';
    WHEN 'anniversary_month' THEN
        a := to_timestamp(p_anchor) AT TIME ZONE 'UTC';
        n := (extract(year FROM t)::integer - extract(year FROM a)::integer) * 12
            + extract(month FROM t)::integer - extract(month FROM a)::integer;
        s := a + make_interval(months => n);
        IF s > t THEN n := n - 1; s := a + make_interval(months => n); END IF;
        e := a + make_interval(months => n + 1);
    WHEN 'custom' THEN
        IF p_seconds <= 0 THEN RAISE EXCEPTION 'invalid period' USING ERRCODE = '23514'; END IF;
        start_at := (p_anchor::numeric
            + floor((p_at::numeric - p_anchor) / p_seconds) * p_seconds)::bigint;
        end_at := start_at + p_seconds;
        RETURN NEXT; RETURN;
    ELSE RAISE EXCEPTION 'invalid period' USING ERRCODE = '23514';
    END CASE;
    start_at := extract(epoch FROM s AT TIME ZONE 'UTC')::bigint;
    end_at := extract(epoch FROM e AT TIME ZONE 'UTC')::bigint;
    RETURN NEXT;
END $$;

CREATE TABLE core_charging.budgets (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope TEXT NOT NULL CHECK (scope IN ('account', 'member', 'self', 'key')),
    account_id BIGINT REFERENCES core_identity.accounts(id),
    user_id BIGINT REFERENCES core_identity.users(id),
    key_id BIGINT REFERENCES core_identity.credentials(id),
    issuer_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    period TEXT NOT NULL CHECK (period IN ('day', 'week', 'month', 'custom')),
    anchor BIGINT NOT NULL DEFAULT 0 CHECK (anchor BETWEEN -62135596800 AND 253402300799),
    seconds BIGINT NOT NULL DEFAULT 0,
    limit_credits BIGINT NOT NULL CHECK (limit_credits >= 0),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    CHECK ((period = 'custom' AND seconds BETWEEN 1 AND 315576000)
        OR (period <> 'custom' AND anchor = 0 AND seconds = 0)),
    CHECK ((scope = 'account' AND account_id IS NOT NULL AND user_id IS NULL AND key_id IS NULL)
        OR (scope = 'member' AND account_id IS NOT NULL AND user_id IS NOT NULL AND key_id IS NULL)
        OR (scope = 'self' AND account_id IS NULL AND user_id IS NOT NULL AND key_id IS NULL)
        OR (scope = 'key' AND account_id IS NULL AND user_id IS NULL AND key_id IS NOT NULL)),
    UNIQUE NULLS NOT DISTINCT (scope, account_id, user_id, key_id, issuer_user_id, period, anchor, seconds)
);
-- Issuer is part of the identity: an admin's self-limit cannot overwrite a
-- founder's member limit. Every matching rule must pass, across every API key.
CREATE TABLE core_charging.subscriptions (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    grant_reference TEXT NOT NULL UNIQUE CHECK (length(grant_reference) BETWEEN 1 AND 200),
    period TEXT NOT NULL CHECK (period IN ('custom', 'anniversary_month')),
    anchor BIGINT NOT NULL CHECK (anchor BETWEEN 0 AND 253402300799),
    seconds BIGINT NOT NULL DEFAULT 0,
    ends_at BIGINT NOT NULL CHECK (ends_at <= 253402300799 AND ends_at > anchor),
    limit_credits BIGINT NOT NULL CHECK (limit_credits >= 0),
    models TEXT[] NOT NULL CHECK (cardinality(models) > 0),
    groups TEXT[] NOT NULL CHECK (cardinality(groups) > 0),
    active BOOLEAN NOT NULL DEFAULT TRUE,
    CHECK ((period = 'custom' AND seconds BETWEEN 1 AND 315576000)
        OR (period = 'anniversary_month' AND seconds = 0))
);
CREATE INDEX subscriptions_account ON core_charging.subscriptions(account_id, id);

CREATE TABLE core_charging.requests (
    id TEXT PRIMARY KEY CHECK (length(id) BETWEEN 1 AND 128),
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    key_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    owner_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    payer_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    fingerprint BYTEA NOT NULL CHECK (octet_length(fingerprint) = 32),
    worker_digest BYTEA NOT NULL CHECK (octet_length(worker_digest) = 32),
    spec_digest BYTEA NOT NULL CHECK (octet_length(spec_digest) = 32),
    price JSONB NOT NULL CHECK (jsonb_typeof(price) = 'object'),
    source_subscription_id BIGINT REFERENCES core_charging.subscriptions(id),
    source_start BIGINT,
    source_end BIGINT,
    authorized_at BIGINT NOT NULL,
    lease_until BIGINT NOT NULL,
    hard_deadline BIGINT NOT NULL CHECK (hard_deadline > authorized_at),
    state TEXT NOT NULL CHECK (state IN ('reserved', 'streaming', 'reconcile', 'settled', 'released')),
    reserved BIGINT NOT NULL CHECK (reserved >= 0),
    input_tokens BIGINT NOT NULL DEFAULT 0 CHECK (input_tokens >= 0),
    cached_tokens BIGINT NOT NULL DEFAULT 0 CHECK (cached_tokens BETWEEN 0 AND input_tokens),
    output_tokens BIGINT NOT NULL DEFAULT 0 CHECK (output_tokens >= 0),
    settled BIGINT NOT NULL DEFAULT 0 CHECK (settled BETWEEN 0 AND reserved),
    refunded BIGINT NOT NULL DEFAULT 0 CHECK (refunded BETWEEN 0 AND settled),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    outcome TEXT,
    CHECK (lease_until <= hard_deadline),
    CHECK ((source_subscription_id IS NULL AND source_start IS NULL AND source_end IS NULL)
        OR (source_subscription_id IS NOT NULL AND source_start IS NOT NULL
            AND source_end IS NOT NULL AND source_end > source_start)),
    CHECK ((state = 'settled' AND outcome IS NOT NULL)
        OR (state <> 'settled' AND settled = 0 AND refunded = 0 AND outcome IS NULL))
);
CREATE INDEX requests_payer_window ON core_charging.requests(payer_account_id, authorized_at);
CREATE INDEX requests_actor_window ON core_charging.requests(actor_user_id, authorized_at);
CREATE INDEX requests_key_window ON core_charging.requests(key_id, authorized_at);
CREATE INDEX requests_entitlement ON core_charging.requests(source_subscription_id, source_start);
CREATE INDEX requests_expired ON core_charging.requests(lease_until, id)
    WHERE state IN ('reserved', 'streaming');

-- These are audit snapshots, not mutable counters or monetary balances.
CREATE TABLE core_charging.budget_checks (
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    request_revision BIGINT NOT NULL,
    budget_id BIGINT NOT NULL REFERENCES core_charging.budgets(id),
    budget_revision BIGINT NOT NULL,
    start_at BIGINT NOT NULL,
    end_at BIGINT NOT NULL CHECK (end_at > start_at),
    limit_credits BIGINT NOT NULL,
    requested_credits BIGINT NOT NULL,
    PRIMARY KEY (request_id, request_revision, budget_id)
);
CREATE TABLE core_charging.usage_events (
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    event_id TEXT NOT NULL CHECK (length(event_id) BETWEEN 1 AND 128),
    detail JSONB NOT NULL CHECK (jsonb_typeof(detail) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (request_id, event_id)
);
CREATE TABLE core_charging.refunds (
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    refund_id TEXT NOT NULL CHECK (length(refund_id) BETWEEN 1 AND 128),
    amount BIGINT NOT NULL CHECK (amount > 0),
    evidence TEXT NOT NULL CHECK (length(evidence) BETWEEN 1 AND 500),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (request_id, refund_id)
);
CREATE TABLE core_charging.policy_events (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    budget_id BIGINT NOT NULL REFERENCES core_charging.budgets(id),
    detail JSONB NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);

-- Do not attach usage to membership versions, key digests, or policy revisions.
-- A cap added after consumption sees that consumption immediately.
CREATE FUNCTION core_charging.committed_credits(p_state TEXT, p_reserved BIGINT,
    p_settled BIGINT, p_refunded BIGINT) RETURNS BIGINT
LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN p_state IN ('reserved', 'streaming', 'reconcile') THEN p_reserved
        WHEN p_state = 'settled' THEN p_settled - p_refunded ELSE 0 END
$$;
CREATE FUNCTION core_charging.no_delete() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN RAISE EXCEPTION 'charging history is immutable' USING ERRCODE = '23514'; END $$;
CREATE TRIGGER requests_no_delete BEFORE DELETE ON core_charging.requests
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER budgets_no_delete BEFORE DELETE ON core_charging.budgets
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER subscriptions_no_delete BEFORE DELETE ON core_charging.subscriptions
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER checks_immutable BEFORE UPDATE OR DELETE ON core_charging.budget_checks
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER usage_immutable BEFORE UPDATE OR DELETE ON core_charging.usage_events
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER refunds_immutable BEFORE UPDATE OR DELETE ON core_charging.refunds
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE TRIGGER policies_immutable BEFORE UPDATE OR DELETE ON core_charging.policy_events
    FOR EACH ROW EXECUTE FUNCTION core_charging.no_delete();
CREATE FUNCTION core_charging.guard_budget() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF (to_jsonb(NEW) - 'limit_credits' - 'revision') IS DISTINCT FROM
       (to_jsonb(OLD) - 'limit_credits' - 'revision') OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'budget identity is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER budget_identity BEFORE UPDATE ON core_charging.budgets
    FOR EACH ROW EXECUTE FUNCTION core_charging.guard_budget();
CREATE FUNCTION core_charging.guard_subscription() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF (to_jsonb(NEW) - 'active') IS DISTINCT FROM (to_jsonb(OLD) - 'active') THEN
        RAISE EXCEPTION 'subscription grant is immutable' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER subscription_identity BEFORE UPDATE ON core_charging.subscriptions
    FOR EACH ROW EXECUTE FUNCTION core_charging.guard_subscription();
CREATE FUNCTION core_charging.guard_request() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
    IF (NEW.id, NEW.actor_user_id, NEW.key_id, NEW.owner_account_id, NEW.payer_account_id,
        NEW.fingerprint, NEW.worker_digest, NEW.spec_digest, NEW.price, NEW.source_subscription_id,
        NEW.source_start, NEW.source_end, NEW.authorized_at, NEW.hard_deadline)
        IS DISTINCT FROM
       (OLD.id, OLD.actor_user_id, OLD.key_id, OLD.owner_account_id, OLD.payer_account_id,
        OLD.fingerprint, OLD.worker_digest, OLD.spec_digest, OLD.price, OLD.source_subscription_id,
        OLD.source_start, OLD.source_end, OLD.authorized_at, OLD.hard_deadline)
       OR NEW.reserved < OLD.reserved OR NEW.refunded < OLD.refunded
       OR NEW.input_tokens < OLD.input_tokens OR NEW.cached_tokens < OLD.cached_tokens
       OR NEW.output_tokens < OLD.output_tokens OR NEW.revision <> OLD.revision + 1 THEN
        RAISE EXCEPTION 'invalid charging mutation' USING ERRCODE = '23514';
    END IF;
    IF OLD.state IN ('settled', 'released') AND
        (to_jsonb(NEW) - 'refunded' - 'revision') IS DISTINCT FROM
        (to_jsonb(OLD) - 'refunded' - 'revision') THEN
        RAISE EXCEPTION 'request is terminal' USING ERRCODE = '23514';
    END IF;
    IF NEW.state <> OLD.state AND NOT
       ((OLD.state = 'reserved' AND NEW.state IN ('streaming', 'released'))
        OR (OLD.state = 'streaming' AND NEW.state IN ('settled', 'reconcile'))
        OR (OLD.state = 'reconcile' AND NEW.state = 'settled')) THEN
        RAISE EXCEPTION 'invalid charging transition' USING ERRCODE = '23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER request_identity BEFORE UPDATE ON core_charging.requests
    FOR EACH ROW EXECUTE FUNCTION core_charging.guard_request();
