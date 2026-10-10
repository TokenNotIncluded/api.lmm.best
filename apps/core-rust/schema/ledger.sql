-- Fresh installation only; apply AFTER identity.sql in the installer's transaction.
-- Deliberately not wired into init-db until the microkernel integration step.
-- Unit: integer credit_500k_usd (500,000 units = USD 1). No floating point.
-- Install as a dedicated NOLOGIN schema owner, NOT as the Rust runtime role.

CREATE TABLE core_billing.ledger_accounts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_account_id BIGINT REFERENCES core_identity.accounts(id),
    bucket TEXT NOT NULL CHECK (bucket IN ('wallet', 'reserved', 'clearing', 'revenue')),
    unit TEXT NOT NULL DEFAULT 'credit_500k_usd' CHECK (unit = 'credit_500k_usd'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK ((owner_account_id IS NOT NULL) = (bucket IN ('wallet', 'reserved'))),
    UNIQUE (owner_account_id, bucket)
);
CREATE UNIQUE INDEX ledger_system_bucket ON core_billing.ledger_accounts(bucket)
    WHERE owner_account_id IS NULL;

CREATE TABLE core_billing.balance_state (
    ledger_account_id BIGINT PRIMARY KEY REFERENCES core_billing.ledger_accounts(id),
    balance_units BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 0 CHECK (revision >= 0)
);

-- Both successful and business-rejected requests have a permanent, immutable result.
-- No pending row can survive a rollback; no operation key has a TTL.
CREATE TABLE core_billing.ledger_operations (
    scope TEXT NOT NULL CHECK (length(scope) BETWEEN 1 AND 64),
    operation_key TEXT NOT NULL CHECK (length(operation_key) BETWEEN 1 AND 128),
    request JSONB NOT NULL CHECK (jsonb_typeof(request) = 'object'),
    result JSONB NOT NULL CHECK (result->>'status' IN ('posted', 'rejected')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (scope, operation_key)
);

CREATE TABLE core_billing.ledger_journals (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope TEXT NOT NULL,
    operation_key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'release', 'refund', 'reverse')),
    payer_id BIGINT NOT NULL REFERENCES core_billing.ledger_accounts(id),
    payee_id BIGINT NOT NULL REFERENCES core_billing.ledger_accounts(id),
    amount_units BIGINT NOT NULL CHECK (amount_units >= 0 AND (amount_units > 0 OR kind = 'capture')),
    parent_journal_id BIGINT REFERENCES core_billing.ledger_journals(id),
    payment_reference TEXT,
    actor_user_id BIGINT CHECK (actor_user_id > 0),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
    CHECK (payer_id <> payee_id),
    CHECK ((kind = 'credit') = (payment_reference IS NOT NULL)),
    CHECK ((kind IN ('capture', 'release', 'refund', 'reverse')) = (parent_journal_id IS NOT NULL)),
    UNIQUE (scope, operation_key),
    FOREIGN KEY (scope, operation_key) REFERENCES core_billing.ledger_operations(scope, operation_key)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX ledger_payment_once ON core_billing.ledger_journals(payment_reference)
    WHERE kind = 'credit';
CREATE UNIQUE INDEX ledger_reservation_terminal ON core_billing.ledger_journals(parent_journal_id)
    WHERE kind IN ('capture', 'release');
CREATE INDEX ledger_journal_parent ON core_billing.ledger_journals(parent_journal_id);

CREATE TABLE core_billing.ledger_entries (
    journal_id BIGINT NOT NULL REFERENCES core_billing.ledger_journals(id),
    ledger_account_id BIGINT NOT NULL REFERENCES core_billing.ledger_accounts(id),
    delta_units BIGINT NOT NULL CHECK (delta_units <> 0),
    balance_after_units BIGINT NOT NULL,
    balance_revision BIGINT NOT NULL CHECK (balance_revision > 0),
    PRIMARY KEY (journal_id, ledger_account_id),
    UNIQUE (ledger_account_id, balance_revision)
);
CREATE INDEX ledger_entries_account ON core_billing.ledger_entries(ledger_account_id, journal_id);

CREATE FUNCTION core_billing.ledger_immutable() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    RAISE EXCEPTION 'ledger history is append-only' USING ERRCODE = '55000';
END $$;
CREATE TRIGGER ledger_accounts_immutable BEFORE UPDATE OR DELETE ON core_billing.ledger_accounts
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_accounts_no_truncate BEFORE TRUNCATE ON core_billing.ledger_accounts
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_journals_immutable BEFORE UPDATE OR DELETE ON core_billing.ledger_journals
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_journals_no_truncate BEFORE TRUNCATE ON core_billing.ledger_journals
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_entries_immutable BEFORE UPDATE OR DELETE ON core_billing.ledger_entries
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_entries_no_truncate BEFORE TRUNCATE ON core_billing.ledger_entries
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_operations_immutable BEFORE UPDATE OR DELETE ON core_billing.ledger_operations
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_operations_no_truncate BEFORE TRUNCATE ON core_billing.ledger_operations
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_balance_no_delete BEFORE DELETE ON core_billing.balance_state
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER ledger_balance_no_truncate BEFORE TRUNCATE ON core_billing.balance_state
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();

CREATE FUNCTION core_billing.ledger_new_account() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    INSERT INTO core_billing.balance_state(ledger_account_id) VALUES (NEW.id);
    RETURN NEW;
END $$;
CREATE TRIGGER ledger_account_balance AFTER INSERT ON core_billing.ledger_accounts
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_new_account();
INSERT INTO core_billing.ledger_accounts(bucket) VALUES ('clearing'), ('revenue');

CREATE FUNCTION core_billing.ledger_apply_entry() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE old_units BIGINT; old_revision BIGINT; account_bucket TEXT;
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM core_billing.ledger_journals j
        WHERE j.id = NEW.journal_id AND j.created_xid = pg_current_xact_id()
          AND NOT EXISTS (SELECT 1 FROM core_billing.ledger_operations o
              WHERE o.scope = j.scope AND o.operation_key = j.operation_key)
    ) THEN
        RAISE EXCEPTION 'journal is already sealed' USING ERRCODE = '55000';
    END IF;
    SELECT b.balance_units, b.revision, a.bucket INTO STRICT old_units, old_revision, account_bucket
        FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a ON a.id = b.ledger_account_id
        WHERE b.ledger_account_id = NEW.ledger_account_id FOR NO KEY UPDATE OF b;
    NEW.balance_after_units := old_units + NEW.delta_units;
    NEW.balance_revision := old_revision + 1;
    IF account_bucket <> 'clearing' AND NEW.balance_after_units < 0 THEN
        RAISE EXCEPTION 'insufficient funds' USING ERRCODE = 'P1003';
    END IF;
    UPDATE core_billing.balance_state SET balance_units = NEW.balance_after_units, revision = NEW.balance_revision
        WHERE ledger_account_id = NEW.ledger_account_id;
    RETURN NEW;
END $$;
CREATE TRIGGER ledger_entry_balance BEFORE INSERT ON core_billing.ledger_entries
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_apply_entry();

CREATE FUNCTION core_billing.ledger_check_journal() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE entry_count BIGINT; net NUMERIC;
BEGIN
    -- SUM(BIGINT) uses exact NUMERIC, avoiding overflow in the conservation check.
    SELECT count(*), sum(delta_units) INTO entry_count, net
        FROM core_billing.ledger_entries WHERE journal_id = NEW.id;
    IF entry_count < 2 OR net IS DISTINCT FROM 0::NUMERIC THEN
        RAISE EXCEPTION 'unbalanced ledger journal' USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER ledger_journal_balanced AFTER INSERT ON core_billing.ledger_journals
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_check_journal();

CREATE FUNCTION core_billing.open_ledger_wallet(p_account_id BIGINT) RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE wallet_id BIGINT; reserved_id BIGINT;
BEGIN
    IF p_account_id IS NULL OR p_account_id <= 0 OR NOT EXISTS (
        SELECT 1 FROM core_identity.accounts WHERE id = p_account_id
    ) THEN
        RAISE EXCEPTION 'account not found' USING ERRCODE = 'P1002';
    END IF;
    -- All callers create the two buckets in the same order. This never credits them.
    INSERT INTO core_billing.ledger_accounts(owner_account_id, bucket) VALUES (p_account_id, 'wallet')
        ON CONFLICT (owner_account_id, bucket) DO NOTHING;
    INSERT INTO core_billing.ledger_accounts(owner_account_id, bucket) VALUES (p_account_id, 'reserved')
        ON CONFLICT (owner_account_id, bucket) DO NOTHING;
    SELECT id INTO STRICT wallet_id FROM core_billing.ledger_accounts
        WHERE owner_account_id = p_account_id AND bucket = 'wallet';
    SELECT id INTO STRICT reserved_id FROM core_billing.ledger_accounts
        WHERE owner_account_id = p_account_id AND bucket = 'reserved';
    RETURN jsonb_build_object('account_id', p_account_id, 'wallet_id', wallet_id, 'reserved_id', reserved_id);
END $$;

-- Only Rust gets EXECUTE. There is no arbitrary-entry or set-balance interface.
-- One call per READ COMMITTED transaction. Lock order:
-- operation key -> original journal (if any) -> balance IDs ascending.
CREATE FUNCTION core_billing.post_ledger(p_request JSONB) RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER SET search_path = pg_catalog AS $$
DECLARE
    op_scope TEXT := p_request->>'scope';
    op_key TEXT := p_request->>'operation_key';
    action JSONB := p_request->'action';
    action_kind TEXT := p_request->'action'->>'kind';
    prior core_billing.ledger_operations%ROWTYPE;
    original core_billing.ledger_journals%ROWTYPE;
    payer BIGINT; payee BIGINT; held BIGINT; revenue BIGINT; clearing BIGINT;
    amount BIGINT; parent_id BIGINT; journal_id BIGINT; refunded NUMERIC;
    payment_ref TEXT; legs JSONB; leg JSONB; result JSONB; err TEXT; violated TEXT;
BEGIN
    IF current_setting('transaction_isolation') <> 'read committed' THEN
        RAISE EXCEPTION 'ledger requires read committed' USING ERRCODE = 'P1000';
    END IF;
    IF NOT COALESCE(jsonb_typeof(p_request) = 'object'
        AND octet_length(p_request::TEXT) <= 8192
        AND jsonb_typeof(p_request->'scope') = 'string'
        AND op_scope ~ '^[A-Za-z0-9._:/-]{1,64}$'
        AND jsonb_typeof(p_request->'operation_key') = 'string'
        AND op_key ~ '^[A-Za-z0-9._:/-]{1,128}$'
        AND jsonb_typeof(p_request->'reason') = 'string'
        AND length(p_request->>'reason') BETWEEN 1 AND 512
        AND action_kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'release', 'refund', 'reverse'), FALSE)
    THEN
        RAISE EXCEPTION 'invalid ledger request' USING ERRCODE = 'P1000';
    END IF;
    -- JSON array encoding makes the key unambiguous. Hash collisions only serialize work.
    PERFORM pg_advisory_xact_lock(hashtextextended(jsonb_build_array('lmm-ledger-v1', op_scope, op_key)::TEXT, 0));
    SELECT * INTO prior FROM core_billing.ledger_operations WHERE scope = op_scope AND operation_key = op_key;
    IF FOUND THEN
        IF prior.request IS DISTINCT FROM p_request THEN
            RAISE EXCEPTION 'operation key has different parameters' USING ERRCODE = 'P1001';
        END IF;
        RETURN prior.result;
    END IF;

    -- Expected business failures roll back every posting in this subtransaction,
    -- then store a rejected result outside it. Unexpected failures abort everything.
    BEGIN
        IF action_kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'refund') THEN
            amount := (action->>'amount_units')::BIGINT;
            IF amount IS NULL OR amount < 0 OR (amount = 0 AND action_kind <> 'capture') THEN
                RAISE EXCEPTION 'invalid amount' USING ERRCODE = 'P1000';
            END IF;
        END IF;
        SELECT id INTO STRICT revenue FROM core_billing.ledger_accounts WHERE bucket = 'revenue';
        SELECT id INTO STRICT clearing FROM core_billing.ledger_accounts WHERE bucket = 'clearing';

        IF action_kind IN ('capture', 'release', 'refund', 'reverse') THEN
            parent_id := CASE WHEN action_kind IN ('capture', 'release')
                THEN (action->>'reservation_journal_id')::BIGINT ELSE (action->>'journal_id')::BIGINT END;
            SELECT * INTO original FROM core_billing.ledger_journals WHERE id = parent_id FOR NO KEY UPDATE;
            IF NOT FOUND THEN RAISE EXCEPTION 'original not found' USING ERRCODE = 'P1002'; END IF;
        END IF;

        IF action_kind = 'credit' THEN
            payer := clearing;
            SELECT id INTO payee FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'wallet';
            payment_ref := action->>'payment_reference';
            IF payment_ref IS NULL OR length(payment_ref) NOT BETWEEN 1 AND 256 THEN
                RAISE EXCEPTION 'invalid payment reference' USING ERRCODE = 'P1000';
            END IF;
        ELSIF action_kind = 'transfer' THEN
            SELECT id INTO payer FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'from_account_id')::BIGINT AND bucket = 'wallet';
            SELECT id INTO payee FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'to_account_id')::BIGINT AND bucket = 'wallet';
        ELSIF action_kind IN ('charge', 'reserve') THEN
            SELECT id INTO payer FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'wallet';
            IF action_kind = 'charge' THEN payee := revenue;
            ELSE
                SELECT id INTO payee FROM core_billing.ledger_accounts
                    WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'reserved';
            END IF;
        ELSIF action_kind IN ('capture', 'release') THEN
            IF original.kind <> 'reserve' THEN
                RAISE EXCEPTION 'not a reservation' USING ERRCODE = 'P1004';
            END IF;
            IF EXISTS (SELECT 1 FROM core_billing.ledger_journals
                WHERE parent_journal_id = parent_id AND kind IN ('capture', 'release')) THEN
                RAISE EXCEPTION 'reservation finalized' USING ERRCODE = 'P1006';
            END IF;
            IF action_kind = 'release' THEN
                payer := original.payee_id; payee := original.payer_id; amount := original.amount_units;
            ELSE
                IF amount > original.amount_units THEN
                    RAISE EXCEPTION 'capture exceeds reservation' USING ERRCODE = 'P1005';
                END IF;
                -- Economic payer stays the ORIGINAL wallet, so refunds never go to a held bucket.
                payer := original.payer_id; payee := revenue; held := original.payee_id;
                legs := jsonb_build_array(
                    jsonb_build_object('id', held, 'units', -original.amount_units),
                    jsonb_build_object('id', payee, 'units', amount),
                    jsonb_build_object('id', payer, 'units', original.amount_units - amount));
            END IF;
        ELSE
            IF original.kind NOT IN ('credit', 'transfer', 'charge', 'capture')
                OR (action_kind = 'reverse' AND original.kind = 'capture') THEN
                RAISE EXCEPTION 'original cannot be compensated this way' USING ERRCODE = 'P1004';
            END IF;
            SELECT COALESCE(sum(amount_units), 0) INTO refunded FROM core_billing.ledger_journals
                WHERE parent_journal_id = parent_id AND kind IN ('refund', 'reverse');
            IF action_kind = 'reverse' THEN
                IF refunded <> 0 THEN RAISE EXCEPTION 'already compensated' USING ERRCODE = 'P1007'; END IF;
                amount := original.amount_units;
            END IF;
            IF amount > original.amount_units - refunded THEN
                RAISE EXCEPTION 'refund exceeds remaining amount' USING ERRCODE = 'P1005';
            END IF;
            payer := original.payee_id; payee := original.payer_id;
        END IF;
        IF payer IS NULL OR payee IS NULL THEN
            RAISE EXCEPTION 'wallet not found' USING ERRCODE = 'P1002';
        END IF;
        IF payer = payee THEN RAISE EXCEPTION 'self transfer' USING ERRCODE = 'P1000'; END IF;
        IF legs IS NULL THEN
            legs := jsonb_build_array(jsonb_build_object('id', payer, 'units', -amount),
                jsonb_build_object('id', payee, 'units', amount));
        END IF;

        PERFORM b.ledger_account_id FROM core_billing.balance_state b
            WHERE b.ledger_account_id IN (SELECT (value->>'id')::BIGINT FROM jsonb_array_elements(legs))
            ORDER BY b.ledger_account_id FOR NO KEY UPDATE;
        INSERT INTO core_billing.ledger_journals(scope, operation_key, kind, payer_id, payee_id,
            amount_units, parent_journal_id, payment_reference, actor_user_id, reason)
            VALUES (op_scope, op_key, action_kind, payer, payee, amount, parent_id, payment_ref,
                (p_request->>'actor_user_id')::BIGINT, p_request->>'reason') RETURNING id INTO journal_id;
        FOR leg IN SELECT value FROM jsonb_array_elements(legs)
            WHERE (value->>'units')::BIGINT <> 0 ORDER BY (value->>'id')::BIGINT
        LOOP
            INSERT INTO core_billing.ledger_entries(journal_id, ledger_account_id, delta_units)
                VALUES (journal_id, (leg->>'id')::BIGINT, (leg->>'units')::BIGINT);
        END LOOP;
        SELECT jsonb_build_object('status', 'posted', 'journal_id', journal_id, 'entries',
            jsonb_agg(jsonb_build_object('ledger_account_id', e.ledger_account_id, 'delta_units', e.delta_units,
                'balance_after_units', e.balance_after_units, 'balance_revision', e.balance_revision)
                ORDER BY e.ledger_account_id)) INTO result
            FROM core_billing.ledger_entries e WHERE e.journal_id = post_ledger.journal_id;
    EXCEPTION
        WHEN SQLSTATE 'P1002' OR SQLSTATE 'P1003' OR SQLSTATE 'P1004'
            OR SQLSTATE 'P1005' OR SQLSTATE 'P1006' OR SQLSTATE 'P1007' THEN
            GET STACKED DIAGNOSTICS err = RETURNED_SQLSTATE;
            result := jsonb_build_object('status', 'rejected', 'code', CASE err
                WHEN 'P1002' THEN 'not_found' WHEN 'P1003' THEN 'insufficient_funds'
                WHEN 'P1004' THEN 'invalid_original' WHEN 'P1005' THEN 'exceeds_remaining'
                WHEN 'P1006' THEN 'already_finalized' WHEN 'P1007' THEN 'already_compensated' END);
        WHEN numeric_value_out_of_range THEN
            result := jsonb_build_object('status', 'rejected', 'code', 'amount_overflow');
        WHEN unique_violation THEN
            GET STACKED DIAGNOSTICS violated = CONSTRAINT_NAME;
            IF violated <> 'ledger_payment_once' THEN RAISE; END IF;
            result := jsonb_build_object('status', 'rejected', 'code', 'duplicate_payment');
    END;
    INSERT INTO core_billing.ledger_operations(scope, operation_key, request, result)
        VALUES (op_scope, op_key, p_request, result);
    RETURN result;
END $$;

-- Never grant table/sequence writes to the runtime or to Go extensions.
-- Integration grants USAGE, selected SELECTs and ONLY these two entry functions.
REVOKE ALL ON core_billing.ledger_accounts, core_billing.balance_state,
    core_billing.ledger_operations, core_billing.ledger_journals, core_billing.ledger_entries FROM PUBLIC;
REVOKE ALL ON FUNCTION core_billing.open_ledger_wallet(BIGINT), core_billing.post_ledger(JSONB),
    core_billing.ledger_immutable(), core_billing.ledger_new_account(),
    core_billing.ledger_apply_entry(), core_billing.ledger_check_journal() FROM PUBLIC;
