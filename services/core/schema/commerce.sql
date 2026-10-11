-- Fresh installation, after identity, ledger, charging and events.
-- Core-owned immutable economic snapshots; Go has no access to this schema.
CREATE SCHEMA core_commerce;
CREATE TABLE core_commerce.key_scopes (
    credential_id BIGINT PRIMARY KEY REFERENCES core_identity.credentials(id),
    can_spend BOOLEAN NOT NULL DEFAULT FALSE
);
CREATE TABLE core_commerce.orders (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    shop_id TEXT NOT NULL CHECK (shop_id ~ '^[a-f0-9]{64}$'),
    order_id TEXT NOT NULL CHECK (order_id ~ '^[a-f0-9]{64}$'),
    economics JSONB NOT NULL CHECK (jsonb_typeof(economics)='object'),
    buyer_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    seller_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    key_id BIGINT NOT NULL REFERENCES core_identity.credentials(id),
    authorized_at BIGINT NOT NULL,
    credits BIGINT NOT NULL CHECK (credits > 0),
    consumption_id TEXT NOT NULL UNIQUE,
    capture_principal TEXT NOT NULL CHECK (capture_principal ~ '^[a-z0-9._-]{1,64}$'),
    payment_command_sha256 BYTEA NOT NULL CHECK (octet_length(payment_command_sha256)=32),
    delivery_reference TEXT CHECK (length(delivery_reference) BETWEEN 1 AND 128),
    state TEXT NOT NULL CHECK (state IN ('rejected','paid','settled','refunded')),
    reserve_journal_id BIGINT REFERENCES core_billing.ledger_journals(id),
    settlement_journal_id BIGINT REFERENCES core_billing.ledger_journals(id),
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    UNIQUE(shop_id,order_id),
    CHECK (buyer_account_id <> seller_account_id),
    CHECK ((state='rejected')=(reserve_journal_id IS NULL)),
    CHECK (state<>'settled' OR settlement_journal_id IS NOT NULL)
);
CREATE TABLE core_commerce.operations (
    operation_key TEXT PRIMARY KEY CHECK (operation_key ~ '^[a-f0-9]{64}$'),
    order_id BIGINT NOT NULL REFERENCES core_commerce.orders(id),
    kind TEXT NOT NULL CHECK (kind IN ('pay','settle','refund')),
    command_sha256 BYTEA NOT NULL CHECK (octet_length(command_sha256)=32),
    command JSONB NOT NULL,
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    delivery_reference TEXT CHECK (length(delivery_reference) BETWEEN 1 AND 128),
    receipt JSONB NOT NULL,
    UNIQUE(order_id,kind)
);
CREATE FUNCTION core_commerce.immutable() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN RAISE EXCEPTION 'commerce history is immutable' USING ERRCODE='55000'; END $$;
CREATE TRIGGER operations_immutable BEFORE UPDATE OR DELETE ON core_commerce.operations
    FOR EACH ROW EXECUTE FUNCTION core_commerce.immutable();
CREATE TRIGGER operations_no_truncate BEFORE TRUNCATE ON core_commerce.operations
    FOR EACH STATEMENT EXECUTE FUNCTION core_commerce.immutable();
CREATE FUNCTION core_commerce.guard_order() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN
        RAISE EXCEPTION 'commerce history is immutable' USING ERRCODE='55000';
    END IF;
    IF (to_jsonb(NEW)-'state'-'settlement_journal_id'-'revision'-'delivery_reference') IS DISTINCT FROM
       (to_jsonb(OLD)-'state'-'settlement_journal_id'-'revision'-'delivery_reference')
       OR NEW.revision<>OLD.revision+1
       OR NOT ((OLD.state='paid' AND NEW.state IN ('settled','refunded'))
           OR (OLD.state='settled' AND NEW.state='refunded'))
       OR (OLD.settlement_journal_id IS NOT NULL AND NEW.settlement_journal_id IS DISTINCT FROM OLD.settlement_journal_id) THEN
        RAISE EXCEPTION 'invalid commerce transition' USING ERRCODE='23514';
    END IF;
    IF OLD.delivery_reference IS NOT NULL AND NEW.delivery_reference IS DISTINCT FROM OLD.delivery_reference THEN
        RAISE EXCEPTION 'delivery evidence is immutable' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER order_identity BEFORE UPDATE OR DELETE ON core_commerce.orders
    FOR EACH ROW EXECUTE FUNCTION core_commerce.guard_order();
CREATE TRIGGER orders_no_truncate BEFORE TRUNCATE ON core_commerce.orders
    FOR EACH STATEMENT EXECUTE FUNCTION core_commerce.immutable();
REVOKE ALL ON ALL TABLES IN SCHEMA core_commerce FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA core_commerce FROM PUBLIC;
REVOKE ALL ON ALL FUNCTIONS IN SCHEMA core_commerce FROM PUBLIC;
