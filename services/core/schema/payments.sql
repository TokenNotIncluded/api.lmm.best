-- Apply after identity.sql and ledger.sql in the offline installer transaction.
-- These are payment authority/evidence records, never another balance ledger.
CREATE SCHEMA core_payments;
CREATE TABLE core_payments.intents (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    created_by_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    channel_id BIGINT NOT NULL CHECK (channel_id > 0),
    provider TEXT NOT NULL CHECK (provider = 'stripe'),
    merchant TEXT NOT NULL CHECK (length(merchant) BETWEEN 1 AND 128),
    environment TEXT NOT NULL CHECK (environment IN ('test','live')),
    currency TEXT NOT NULL CHECK (currency = 'USD'),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    credit_units BIGINT NOT NULL CHECK (credit_units > 0),
    credits_per_minor BIGINT NOT NULL CHECK (credits_per_minor = 5000),
    prepare_key TEXT NOT NULL CHECK (length(prepare_key) BETWEEN 16 AND 128),
    prepare_hash BYTEA NOT NULL CHECK (octet_length(prepare_hash)=32),
    intent_wire BYTEA,
    credit_journal_id BIGINT REFERENCES core_billing.ledger_journals(id),
    provider_transaction TEXT,
    refund_fenced BOOLEAN NOT NULL DEFAULT FALSE,
    refunded_minor BIGINT NOT NULL DEFAULT 0 CHECK (refunded_minor >= 0 AND refunded_minor <= amount_minor),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (created_by_user_id,prepare_key),
    UNIQUE (channel_id,provider_transaction),
    CHECK ((credit_journal_id IS NULL) OR provider_transaction IS NOT NULL),
    CHECK (credit_units::numeric = amount_minor::numeric * credits_per_minor::numeric)
);

CREATE TABLE core_payments.receipts (
    id TEXT PRIMARY KEY CHECK (id ~ '^pay_[a-f0-9]{64}$'),
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    operation_key TEXT NOT NULL CHECK (length(operation_key) BETWEEN 16 AND 128),
    method TEXT NOT NULL CHECK (method IN ('prepare','credit','hold','commit','release','external_refund')),
    request_hash BYTEA NOT NULL CHECK (octet_length(request_hash)=32),
    receipt_wire BYTEA NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (intent_id,operation_key)
);
CREATE TABLE core_payments.refund_holds (
    receipt_id TEXT PRIMARY KEY REFERENCES core_payments.receipts(id),
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    credit_units BIGINT NOT NULL CHECK (credit_units > 0),
    reservation_journal_id BIGINT NOT NULL UNIQUE REFERENCES core_billing.ledger_journals(id),
    state TEXT NOT NULL DEFAULT 'held' CHECK (state IN ('held','committed','released')),
    provider_refund_id TEXT,
    UNIQUE (intent_id,provider_refund_id)
);
CREATE TABLE core_payments.provider_events (
    channel_id BIGINT NOT NULL,
    event_id TEXT NOT NULL CHECK (length(event_id) BETWEEN 1 AND 128),
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash)=32),
    receipt_id TEXT NOT NULL REFERENCES core_payments.receipts(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (channel_id,event_id)
);
-- Distinct Stripe events for one refund cannot apply the refund twice.
CREATE TABLE core_payments.provider_refunds (
    channel_id BIGINT NOT NULL,
    refund_id TEXT NOT NULL CHECK (length(refund_id) BETWEEN 1 AND 128),
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    receipt_id TEXT NOT NULL REFERENCES core_payments.receipts(id),
    PRIMARY KEY (channel_id,refund_id)
);
-- A provider refund is a financial fact even when the current nonnegative
-- ledger cannot represent the resulting debt. Retain its independently verified
-- original evidence and fence stale credit. No success receipt is manufactured.
-- Resolution requires the separately implemented debt/reconciliation policy.
CREATE TABLE core_payments.blocked_refunds (
    channel_id BIGINT NOT NULL,
    event_id TEXT NOT NULL,
    refund_id TEXT NOT NULL,
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    transaction_id TEXT NOT NULL,
    amount_minor BIGINT NOT NULL CHECK (amount_minor > 0),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash)=32),
    signed_payload BYTEA NOT NULL CHECK (octet_length(signed_payload) BETWEEN 1 AND 32768),
    signature BYTEA NOT NULL CHECK (octet_length(signature) BETWEEN 1 AND 2048),
    reason TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (channel_id,refund_id),
    UNIQUE (channel_id,event_id)
);

-- Independent webhook ingress is committed before an extension may ACK. These
-- rows certify authentic input only; application receipts/ledger remain separate.
-- Proofs remain valid after queue delays and endpoint-secret rotation because
-- settlement reuses Rust's immutable verified facts, not a caller's timestamp.
CREATE TABLE core_payments.accepted_proofs (
    id TEXT PRIMARY KEY CHECK (id ~ '^proof_[a-f0-9]{64}$'),
    channel_id BIGINT NOT NULL,
    event_id TEXT NOT NULL CHECK (length(event_id) BETWEEN 1 AND 128),
    intent_id BIGINT NOT NULL REFERENCES core_payments.intents(id),
    payload_hash BYTEA NOT NULL CHECK (octet_length(payload_hash)=32),
    signed_payload BYTEA NOT NULL CHECK (octet_length(signed_payload) BETWEEN 1 AND 32768),
    signature BYTEA NOT NULL CHECK (octet_length(signature) BETWEEN 1 AND 2048),
    verified_facts JSONB NOT NULL,
    receipt_wire BYTEA NOT NULL,
    accepted_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp(),
    UNIQUE (channel_id,event_id)
);
CREATE TRIGGER proofs_immutable BEFORE UPDATE OR DELETE ON core_payments.accepted_proofs
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();

CREATE FUNCTION core_payments.protect_intent() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP='DELETE' OR (NEW.id,NEW.account_id,NEW.created_by_user_id,NEW.channel_id,
        NEW.provider,NEW.merchant,NEW.environment,NEW.currency,NEW.amount_minor,
        NEW.credit_units,NEW.credits_per_minor,NEW.prepare_key,NEW.prepare_hash,NEW.created_at)
        IS DISTINCT FROM (OLD.id,OLD.account_id,OLD.created_by_user_id,OLD.channel_id,
        OLD.provider,OLD.merchant,OLD.environment,OLD.currency,OLD.amount_minor,
        OLD.credit_units,OLD.credits_per_minor,OLD.prepare_key,OLD.prepare_hash,OLD.created_at)
        OR (OLD.intent_wire IS NOT NULL AND NEW.intent_wire IS DISTINCT FROM OLD.intent_wire)
        OR (OLD.credit_journal_id IS NOT NULL AND NEW.credit_journal_id IS DISTINCT FROM OLD.credit_journal_id)
        OR (OLD.provider_transaction IS NOT NULL AND NEW.provider_transaction IS DISTINCT FROM OLD.provider_transaction)
        OR (OLD.refund_fenced AND NOT NEW.refund_fenced)
        OR NEW.refunded_minor<OLD.refunded_minor THEN
        RAISE EXCEPTION 'immutable payment identity or decreasing refund fence' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER protect_intent BEFORE UPDATE OR DELETE ON core_payments.intents
    FOR EACH ROW EXECUTE FUNCTION core_payments.protect_intent();
CREATE TRIGGER receipts_immutable BEFORE UPDATE OR DELETE ON core_payments.receipts
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER events_immutable BEFORE UPDATE OR DELETE ON core_payments.provider_events
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER refunds_immutable BEFORE UPDATE OR DELETE ON core_payments.provider_refunds
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER blocked_refunds_immutable BEFORE UPDATE OR DELETE ON core_payments.blocked_refunds
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
REVOKE ALL ON SCHEMA core_payments FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA core_payments FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA core_payments FROM PUBLIC;
REVOKE ALL ON FUNCTION core_payments.protect_intent() FROM PUBLIC;
