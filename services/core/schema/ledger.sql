-- Fresh installation only; apply AFTER identity.sql in the installer's transaction.
-- Deliberately not wired into init-db until the microkernel integration step.
-- Unit: integer credit_500k_usd (500,000 units = USD 1). No floating point.
-- Install as a dedicated NOLOGIN schema owner, NOT as the Rust runtime role.

CREATE TABLE core_billing.ledger_accounts (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    owner_account_id BIGINT REFERENCES core_identity.accounts(id),
    bucket TEXT NOT NULL CHECK (bucket IN ('wallet', 'reserved', 'debt', 'affiliate', 'clearing', 'revenue')),
    unit TEXT NOT NULL DEFAULT 'credit_500k_usd' CHECK (unit = 'credit_500k_usd'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    CHECK ((owner_account_id IS NOT NULL) = (bucket IN ('wallet', 'reserved', 'debt', 'affiliate'))),
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
    result JSONB NOT NULL CHECK (COALESCE(result->>'status' IN ('posted', 'rejected'), FALSE)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (scope, operation_key)
);

CREATE TABLE core_billing.ledger_journals (
    id BIGINT GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    scope TEXT NOT NULL,
    operation_key TEXT NOT NULL,
    kind TEXT NOT NULL CHECK (kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'commerce_capture', 'claim_affiliate', 'opening', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse')),
    payer_id BIGINT NOT NULL REFERENCES core_billing.ledger_accounts(id),
    payee_id BIGINT NOT NULL REFERENCES core_billing.ledger_accounts(id),
    amount_units BIGINT NOT NULL CHECK (amount_units >= 0 AND (amount_units > 0 OR kind = 'capture')),
    parent_journal_id BIGINT REFERENCES core_billing.ledger_journals(id),
    payment_reference TEXT,
    refund_reference TEXT,
    actor_user_id BIGINT CHECK (actor_user_id > 0),
    reason TEXT NOT NULL CHECK (length(reason) BETWEEN 1 AND 512),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp(),
    created_xid XID8 NOT NULL DEFAULT pg_current_xact_id(),
    CHECK (payer_id <> payee_id),
    CHECK ((kind = 'credit') = (payment_reference IS NOT NULL)),
    CHECK ((kind = 'refund_topup') = (refund_reference IS NOT NULL)),
    CHECK ((kind IN ('capture', 'commerce_capture', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse')) = (parent_journal_id IS NOT NULL)),
    UNIQUE (scope, operation_key),
    FOREIGN KEY (scope, operation_key) REFERENCES core_billing.ledger_operations(scope, operation_key)
        DEFERRABLE INITIALLY DEFERRED
);
CREATE UNIQUE INDEX ledger_payment_once ON core_billing.ledger_journals(payment_reference)
    WHERE kind = 'credit';
CREATE UNIQUE INDEX ledger_refund_once ON core_billing.ledger_journals(refund_reference)
    WHERE kind = 'refund_topup';
CREATE UNIQUE INDEX ledger_reservation_terminal ON core_billing.ledger_journals(parent_journal_id)
    WHERE kind IN ('capture', 'commerce_capture', 'release');
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
DECLARE old_units BIGINT; old_revision BIGINT; account_bucket TEXT; journal_kind TEXT;
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
    SELECT kind INTO STRICT journal_kind FROM core_billing.ledger_journals WHERE id = NEW.journal_id;
    IF (account_bucket IN ('wallet', 'reserved', 'revenue') AND NEW.balance_after_units < 0)
        OR (account_bucket = 'debt' AND NEW.balance_after_units > 0)
        OR (account_bucket = 'debt' AND NEW.balance_after_units < old_units AND journal_kind NOT IN ('opening','refund_topup','refund_commerce'))
        OR (account_bucket = 'affiliate' AND NEW.balance_after_units < 0
            AND journal_kind <> 'opening' AND NEW.balance_after_units < old_units) THEN
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
    INSERT INTO core_billing.ledger_accounts(owner_account_id, bucket) VALUES (p_account_id, 'debt')
        ON CONFLICT (owner_account_id, bucket) DO NOTHING;
    INSERT INTO core_billing.ledger_accounts(owner_account_id, bucket) VALUES (p_account_id, 'affiliate')
        ON CONFLICT (owner_account_id, bucket) DO NOTHING;
    SELECT id INTO STRICT wallet_id FROM core_billing.ledger_accounts
        WHERE owner_account_id = p_account_id AND bucket = 'wallet';
    SELECT id INTO STRICT reserved_id FROM core_billing.ledger_accounts
        WHERE owner_account_id = p_account_id AND bucket = 'reserved';
    RETURN jsonb_build_object('account_id', p_account_id, 'wallet_id', wallet_id, 'reserved_id', reserved_id);
END $$;

-- Only Rust gets EXECUTE. There is no arbitrary-entry or set-balance interface.
-- Standalone calls use READ COMMITTED. Charging composes several calls in its
-- SERIALIZABLE transaction so wallet writes and aggregate budget checks commit
-- together. Serialization/deadlock failures must escape this function and the
-- caller must retry the ENTIRE transaction, never just one ledger statement.
-- REPEATABLE READ is deliberately unsupported: it has a fixed snapshot without
-- the SSI predicate-conflict checks used by charging. Lock order:
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
    payer BIGINT; payee BIGINT; held BIGINT; revenue BIGINT; clearing BIGINT; debt_id BIGINT;
    repayment BIGINT; debt_balance BIGINT; adjusted_legs JSONB;
    refund_ref TEXT; refund_wallet BIGINT; refund_debt BIGINT; refund_wallet_units BIGINT;
    refund_recipient_debt BIGINT; original_debt_repayment NUMERIC;
    amount BIGINT; parent_id BIGINT; posted_id BIGINT; refunded NUMERIC;
    payment_ref TEXT; legs JSONB; leg JSONB; result JSONB; err TEXT; violated TEXT;
BEGIN
    IF current_setting('transaction_isolation') NOT IN ('read committed', 'serializable') THEN
        RAISE EXCEPTION 'ledger requires read committed or serializable' USING ERRCODE = 'P1000';
    END IF;
    IF NOT COALESCE(jsonb_typeof(p_request) = 'object'
        AND octet_length(p_request::TEXT) <= 8192
        AND jsonb_typeof(p_request->'scope') = 'string'
        AND op_scope ~ '^[A-Za-z0-9._:/-]{1,64}$'
        AND jsonb_typeof(p_request->'operation_key') = 'string'
        AND op_key ~ '^[A-Za-z0-9._:/-]{1,128}$'
        AND jsonb_typeof(p_request->'reason') = 'string'
        AND length(p_request->>'reason') BETWEEN 1 AND 512
        AND action_kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'commerce_capture', 'claim_affiliate', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse'), FALSE)
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
        IF action_kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'commerce_capture', 'claim_affiliate', 'refund', 'refund_topup') THEN
            amount := (action->>'amount_units')::BIGINT;
            IF amount IS NULL OR amount < 0 OR (amount = 0 AND action_kind <> 'capture') THEN
                RAISE EXCEPTION 'invalid amount' USING ERRCODE = 'P1000';
            END IF;
        END IF;
        SELECT id INTO STRICT revenue FROM core_billing.ledger_accounts WHERE bucket = 'revenue';
        SELECT id INTO STRICT clearing FROM core_billing.ledger_accounts WHERE bucket = 'clearing';

        IF action_kind IN ('capture', 'commerce_capture', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse') THEN
            parent_id := CASE WHEN action_kind IN ('capture', 'commerce_capture', 'release')
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
        ELSIF action_kind = 'claim_affiliate' THEN
            SELECT id INTO payer FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'affiliate';
            SELECT id INTO payee FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'wallet';
        ELSIF action_kind IN ('charge', 'reserve') THEN
            SELECT id INTO payer FROM core_billing.ledger_accounts
                WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'wallet';
            IF action_kind = 'charge' THEN payee := revenue;
            ELSE
                SELECT id INTO payee FROM core_billing.ledger_accounts
                    WHERE owner_account_id = (action->>'account_id')::BIGINT AND bucket = 'reserved';
            END IF;
        ELSIF action_kind IN ('capture', 'commerce_capture', 'release') THEN
            IF original.kind <> 'reserve' THEN
                RAISE EXCEPTION 'not a reservation' USING ERRCODE = 'P1004';
            END IF;
            IF EXISTS (SELECT 1 FROM core_billing.ledger_journals
                WHERE parent_journal_id = parent_id AND kind IN ('capture', 'commerce_capture', 'release')) THEN
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
                IF action_kind = 'commerce_capture' THEN
                    SELECT id INTO payee FROM core_billing.ledger_accounts
                        WHERE owner_account_id = (action->>'destination_account_id')::BIGINT AND bucket = 'wallet';
                    IF payee IS NULL OR payee = payer THEN
                        RAISE EXCEPTION 'invalid commerce destination' USING ERRCODE = 'P1000';
                    END IF;
                END IF;
                legs := jsonb_build_array(
                    jsonb_build_object('id', held, 'units', -original.amount_units),
                    jsonb_build_object('id', payee, 'units', amount),
                    jsonb_build_object('id', payer, 'units', original.amount_units - amount));
            END IF;
        ELSE
            IF original.kind NOT IN ('credit', 'transfer', 'charge', 'capture', 'commerce_capture')
                OR (action_kind = 'reverse' AND original.kind IN ('capture', 'commerce_capture')) THEN
                RAISE EXCEPTION 'original cannot be compensated this way' USING ERRCODE = 'P1004';
            END IF;
            -- Restoring debt repaid by an incoming payment requires the original
            -- provider/refund basis. Fail visibly until that bridge is supplied;
            -- never replace a debt repayment with newly spendable wallet funds.
            IF action_kind NOT IN ('refund_topup','refund_commerce') AND original.kind IN ('credit', 'transfer', 'commerce_capture') AND EXISTS (
                SELECT 1 FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a
                    ON a.id=e.ledger_account_id WHERE e.journal_id=original.id
                    AND a.bucket='debt' AND e.delta_units>0
            ) THEN RAISE EXCEPTION 'debt repayment refund requires original basis' USING ERRCODE = 'P1004'; END IF;
            SELECT COALESCE(sum(amount_units), 0) INTO refunded FROM core_billing.ledger_journals
                WHERE parent_journal_id = parent_id AND kind IN ('refund', 'refund_topup', 'refund_commerce', 'reverse');
            IF action_kind IN ('reverse','refund_commerce') THEN
                IF action_kind = 'refund_commerce' AND original.kind <> 'commerce_capture' THEN
                    RAISE EXCEPTION 'not a commerce capture' USING ERRCODE='P1004';
                END IF;
                IF refunded <> 0 THEN RAISE EXCEPTION 'already compensated' USING ERRCODE = 'P1007'; END IF;
                amount := original.amount_units;
            END IF;
            IF amount > original.amount_units - refunded THEN
                RAISE EXCEPTION 'refund exceeds remaining amount' USING ERRCODE = 'P1005';
            END IF;
            payer := original.payee_id; payee := original.payer_id;
            IF action_kind IN ('refund_topup','refund_commerce') THEN
                IF action_kind = 'refund_topup' THEN
                    refund_ref := action->>'refund_reference';
                    IF original.kind <> 'credit' OR refund_ref IS NULL OR length(refund_ref) NOT BETWEEN 1 AND 256 THEN
                        RAISE EXCEPTION 'invalid verified topup refund' USING ERRCODE='P1000';
                    END IF;
                END IF;
                refund_wallet := original.payee_id;
                SELECT d.id INTO STRICT refund_debt FROM core_billing.ledger_accounts w
                    JOIN core_billing.ledger_accounts d ON d.owner_account_id=w.owner_account_id AND d.bucket='debt'
                    WHERE w.id=refund_wallet AND w.bucket='wallet';
                SELECT d.id INTO refund_recipient_debt FROM core_billing.ledger_accounts w
                    JOIN core_billing.ledger_accounts d ON d.owner_account_id=w.owner_account_id AND d.bucket='debt'
                    WHERE w.id=payee AND w.bucket='wallet';
                -- Lock these in the same order as every other ledger posting.
                PERFORM b.ledger_account_id FROM core_billing.balance_state b
                    WHERE b.ledger_account_id IN (refund_wallet,refund_debt,payee,refund_recipient_debt)
                    ORDER BY b.ledger_account_id FOR NO KEY UPDATE;
                SELECT least(balance_units,amount) INTO STRICT refund_wallet_units
                    FROM core_billing.balance_state WHERE ledger_account_id=refund_wallet;
                IF action_kind = 'refund_commerce' THEN
                    SELECT COALESCE(sum(delta_units),0) INTO original_debt_repayment
                        FROM core_billing.ledger_entries WHERE journal_id=parent_id
                            AND ledger_account_id=refund_debt AND delta_units>0;
                    IF amount-refund_wallet_units > original_debt_repayment THEN
                        RAISE EXCEPTION 'spent seller funds are not new debt' USING ERRCODE='P1003';
                    END IF;
                END IF;
                legs := jsonb_build_array(jsonb_build_object('id',refund_wallet,'units',-refund_wallet_units),
                    jsonb_build_object('id',refund_debt,'units',-(amount-refund_wallet_units)),
                    jsonb_build_object('id',payee,'units',amount));
            END IF;
        END IF;
        IF payer IS NULL OR payee IS NULL THEN
            RAISE EXCEPTION 'wallet not found' USING ERRCODE = 'P1002';
        END IF;
        IF payer = payee THEN RAISE EXCEPTION 'self transfer' USING ERRCODE = 'P1000'; END IF;
        IF legs IS NULL THEN
            legs := jsonb_build_array(jsonb_build_object('id', payer, 'units', -amount),
                jsonb_build_object('id', payee, 'units', amount));
        END IF;

        -- Lock incoming wallets AND their debt buckets before choosing the
        -- repayment split. Every writer uses the same ascending balance order.
        PERFORM b.ledger_account_id FROM core_billing.balance_state b
            WHERE b.ledger_account_id IN (SELECT (value->>'id')::BIGINT FROM jsonb_array_elements(legs)
                UNION SELECT d.id FROM jsonb_array_elements(legs) l
                    JOIN core_billing.ledger_accounts w ON w.id=(l.value->>'id')::BIGINT
                    JOIN core_billing.ledger_accounts d ON d.owner_account_id=w.owner_account_id AND d.bucket='debt'
                    WHERE w.bucket='wallet' AND (l.value->>'units')::BIGINT>0)
            ORDER BY b.ledger_account_id FOR NO KEY UPDATE;
        adjusted_legs := '[]'::JSONB;
        FOR leg IN SELECT value FROM jsonb_array_elements(legs) LOOP
            debt_id := NULL;
            IF (leg->>'units')::BIGINT>0 THEN
                SELECT d.id,b.balance_units INTO debt_id,debt_balance FROM core_billing.ledger_accounts w
                    JOIN core_billing.ledger_accounts d ON d.owner_account_id=w.owner_account_id AND d.bucket='debt'
                    JOIN core_billing.balance_state b ON b.ledger_account_id=d.id
                    WHERE w.id=(leg->>'id')::BIGINT AND w.bucket='wallet';
            END IF;
            IF debt_id IS NOT NULL AND debt_balance<0 THEN
                repayment := least((leg->>'units')::BIGINT, -debt_balance);
                adjusted_legs := adjusted_legs || jsonb_build_array(jsonb_build_object('id',debt_id,'units',repayment));
                leg := jsonb_set(leg,'{units}',to_jsonb((leg->>'units')::BIGINT-repayment));
            END IF;
            adjusted_legs := adjusted_legs || jsonb_build_array(leg);
        END LOOP;
        legs := adjusted_legs;
        INSERT INTO core_billing.ledger_journals(scope, operation_key, kind, payer_id, payee_id,
            amount_units, parent_journal_id, payment_reference, refund_reference, actor_user_id, reason)
            VALUES (op_scope, op_key, action_kind, payer, payee, amount, parent_id, payment_ref, refund_ref,
                (p_request->>'actor_user_id')::BIGINT, p_request->>'reason') RETURNING id INTO posted_id;
        FOR leg IN SELECT value FROM jsonb_array_elements(legs)
            WHERE (value->>'units')::BIGINT <> 0 ORDER BY (value->>'id')::BIGINT
        LOOP
            INSERT INTO core_billing.ledger_entries(journal_id, ledger_account_id, delta_units)
                VALUES (posted_id, (leg->>'id')::BIGINT, (leg->>'units')::BIGINT);
        END LOOP;
        SELECT jsonb_build_object('status', 'posted', 'journal_id', posted_id, 'entries',
            jsonb_agg(jsonb_build_object('ledger_account_id', e.ledger_account_id, 'delta_units', e.delta_units,
                'balance_after_units', e.balance_after_units, 'balance_revision', e.balance_revision)
                ORDER BY e.ledger_account_id)) INTO result
            FROM core_billing.ledger_entries e WHERE e.journal_id = posted_id;
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
            IF violated NOT IN ('ledger_payment_once','ledger_refund_once') THEN RAISE; END IF;
            result := jsonb_build_object('status', 'rejected', 'code', CASE violated
                WHEN 'ledger_refund_once' THEN 'duplicate_refund' ELSE 'duplicate_payment' END);
    END;
    INSERT INTO core_billing.ledger_operations(scope, operation_key, request, result)
        VALUES (op_scope, op_key, p_request, result);
    RETURN result;
EXCEPTION WHEN unique_violation THEN
    GET STACKED DIAGNOSTICS violated = CONSTRAINT_NAME;
    -- An advisory-lock wait does not refresh a SERIALIZABLE snapshot. If the
    -- permanent operation receipt was committed after that snapshot, its two
    -- identity constraints can conflict before SSI reports 40001. Retry the
    -- whole transaction so the normal replay/payload check sees the receipt.
    -- Payment, reservation-terminal and every other uniqueness error retain
    -- their existing business/error semantics.
    IF current_setting('transaction_isolation') = 'serializable'
        AND violated IN ('ledger_operations_pkey', 'ledger_journals_scope_operation_key_key') THEN
        RAISE EXCEPTION 'retry complete ledger transaction' USING ERRCODE = '40001';
    END IF;
    RAISE;
END $$;

-- Reviewed offline boundary, never a runtime API. Importers use the schema
-- owner in the same SERIALIZABLE transaction as their immutable source archive,
-- identity mappings and overall receipt. Historical payments are not replayed.
CREATE TABLE core_billing.opening_import_runs (
    migration_id TEXT PRIMARY KEY CHECK (migration_id ~ '^[A-Za-z0-9._:/-]{1,128}$'),
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
    observed_at BIGINT NOT NULL CHECK (observed_at BETWEEN -62135596800 AND 253402300799)
);
CREATE TABLE core_billing.opening_imports (
    migration_id TEXT NOT NULL REFERENCES core_billing.opening_import_runs(migration_id),
    source_entity_key TEXT NOT NULL CHECK (source_entity_key ~ '^[A-Za-z0-9._:/-]{1,200}$'),
    source_row_sha256 TEXT NOT NULL CHECK (source_row_sha256 ~ '^[0-9a-f]{64}$'),
    account_id BIGINT NOT NULL UNIQUE REFERENCES core_identity.accounts(id),
    request JSONB NOT NULL,
    result JSONB NOT NULL,
    PRIMARY KEY (migration_id, source_entity_key)
);
CREATE TRIGGER opening_runs_immutable BEFORE UPDATE OR DELETE ON core_billing.opening_import_runs
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER opening_runs_no_truncate BEFORE TRUNCATE ON core_billing.opening_import_runs
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER opening_imports_immutable BEFORE UPDATE OR DELETE ON core_billing.opening_imports
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER opening_imports_no_truncate BEFORE TRUNCATE ON core_billing.opening_imports
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();

CREATE FUNCTION core_billing.import_opening_balance(p_request JSONB) RETURNS JSONB
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE migration TEXT := p_request->>'migration_id'; entity TEXT := p_request->>'source_entity_key';
    run core_billing.opening_import_runs%ROWTYPE; prior core_billing.opening_imports%ROWTYPE;
    account BIGINT; available BIGINT; reserved BIGINT; affiliate BIGINT;
    wallet_id BIGINT; reserved_id BIGINT; debt_id BIGINT; affiliate_id BIGINT; clearing_id BIGINT;
    opening_journal_id BIGINT; op_key TEXT; legs JSONB; leg JSONB; result JSONB; total NUMERIC;
BEGIN
    IF current_user <> pg_get_userbyid((SELECT nspowner FROM pg_namespace WHERE nspname='core_billing')) THEN
        RAISE EXCEPTION 'opening imports require the offline schema owner' USING ERRCODE='42501';
    END IF;
    IF current_setting('transaction_isolation') <> 'serializable' OR current_setting('synchronous_commit') <> 'on' THEN
        RAISE EXCEPTION 'opening imports require serializable durable commit' USING ERRCODE='25001';
    END IF;
    IF NOT COALESCE(jsonb_typeof(p_request)='object' AND octet_length(p_request::TEXT)<=8192
        AND (SELECT array_agg(k ORDER BY k) FROM jsonb_object_keys(p_request) k) = ARRAY[
            'account_id','affiliate_units','available_units','mapping_sha256','migration_id','observed_at',
            'reserved_units','source_archive_sha256','source_entity_key','source_row_sha256']::TEXT[]
        AND migration ~ '^[A-Za-z0-9._:/-]{1,128}$' AND entity ~ '^[A-Za-z0-9._:/-]{1,200}$'
        AND jsonb_typeof(p_request->'migration_id')='string'
        AND jsonb_typeof(p_request->'source_entity_key')='string'
        AND jsonb_typeof(p_request->'source_archive_sha256')='string'
        AND jsonb_typeof(p_request->'mapping_sha256')='string'
        AND jsonb_typeof(p_request->'source_row_sha256')='string'
        AND p_request->>'source_archive_sha256' ~ '^[0-9a-f]{64}$'
        AND p_request->>'mapping_sha256' ~ '^[0-9a-f]{64}$'
        AND p_request->>'source_row_sha256' ~ '^[0-9a-f]{64}$'
        AND jsonb_typeof(p_request->'account_id')='number'
        AND jsonb_typeof(p_request->'available_units')='number'
        AND jsonb_typeof(p_request->'reserved_units')='number'
        AND jsonb_typeof(p_request->'affiliate_units')='number'
        AND jsonb_typeof(p_request->'observed_at')='number', FALSE) THEN
        RAISE EXCEPTION 'invalid opening import' USING ERRCODE='P1000';
    END IF;
    -- Text-to-bigint rejects fractions; JS-safe source bounds are preserved.
    account := (p_request->>'account_id')::BIGINT;
    available := (p_request->>'available_units')::BIGINT;
    reserved := (p_request->>'reserved_units')::BIGINT;
    affiliate := (p_request->>'affiliate_units')::BIGINT;
    IF account<=0 OR abs(available::NUMERIC)>9007199254740991 OR reserved NOT BETWEEN 0 AND 9007199254740991
        OR abs(affiliate::NUMERIC)>9007199254740991
        OR (p_request->>'observed_at')::BIGINT NOT BETWEEN -62135596800 AND 253402300799 THEN
        RAISE EXCEPTION 'invalid opening amounts' USING ERRCODE='P1000';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('lmm-opening-import-v1',0));
    SELECT * INTO run FROM core_billing.opening_import_runs WHERE migration_id=migration;
    IF FOUND AND (run.source_archive_sha256,run.mapping_sha256,run.observed_at) IS DISTINCT FROM
        (p_request->>'source_archive_sha256',p_request->>'mapping_sha256',(p_request->>'observed_at')::BIGINT) THEN
        RAISE EXCEPTION 'migration identity has different source parameters' USING ERRCODE='P1001';
    END IF;
    SELECT * INTO prior FROM core_billing.opening_imports WHERE migration_id=migration AND source_entity_key=entity;
    IF FOUND THEN
        IF prior.request IS DISTINCT FROM p_request THEN
            RAISE EXCEPTION 'opening identity has different parameters' USING ERRCODE='P1001';
        END IF;
        RETURN prior.result;
    END IF;
    IF EXISTS (SELECT 1 FROM core_billing.opening_imports WHERE account_id=account) THEN
        RAISE EXCEPTION 'account already belongs to an opening import' USING ERRCODE='P1001';
    END IF;
    PERFORM core_billing.open_ledger_wallet(account);
    PERFORM b.ledger_account_id FROM core_billing.balance_state b
        JOIN core_billing.ledger_accounts a ON a.id=b.ledger_account_id
        WHERE a.owner_account_id=account OR a.bucket='clearing' ORDER BY b.ledger_account_id FOR NO KEY UPDATE OF b;
    IF EXISTS (SELECT 1 FROM core_billing.balance_state b JOIN core_billing.ledger_accounts a ON a.id=b.ledger_account_id
        WHERE a.owner_account_id=account AND (b.revision<>0 OR b.balance_units<>0)) THEN
        RAISE EXCEPTION 'opening target account is not empty' USING ERRCODE='P1001';
    END IF;
    INSERT INTO core_billing.opening_import_runs VALUES(migration,p_request->>'source_archive_sha256',
        p_request->>'mapping_sha256',(p_request->>'observed_at')::BIGINT) ON CONFLICT DO NOTHING;
    SELECT id INTO STRICT wallet_id FROM core_billing.ledger_accounts WHERE owner_account_id=account AND bucket='wallet';
    SELECT id INTO STRICT reserved_id FROM core_billing.ledger_accounts WHERE owner_account_id=account AND bucket='reserved';
    SELECT id INTO STRICT debt_id FROM core_billing.ledger_accounts WHERE owner_account_id=account AND bucket='debt';
    SELECT id INTO STRICT affiliate_id FROM core_billing.ledger_accounts WHERE owner_account_id=account AND bucket='affiliate';
    SELECT id INTO STRICT clearing_id FROM core_billing.ledger_accounts WHERE bucket='clearing';
    total := available::NUMERIC + reserved + affiliate;
    legs := jsonb_build_array(jsonb_build_object('id',wallet_id,'units',greatest(available,0)),
        jsonb_build_object('id',debt_id,'units',least(available,0)),
        jsonb_build_object('id',reserved_id,'units',reserved),jsonb_build_object('id',affiliate_id,'units',affiliate),
        jsonb_build_object('id',clearing_id,'units',-total));
    op_key := encode(sha256(convert_to(jsonb_build_array(migration,entity)::TEXT,'UTF8')),'hex');
    IF available<>0 OR reserved<>0 OR affiliate<>0 THEN
        INSERT INTO core_billing.ledger_journals(scope,operation_key,kind,payer_id,payee_id,amount_units,reason)
            VALUES('offline-opening',op_key,'opening',clearing_id,wallet_id,
                (abs(available::NUMERIC)+reserved+abs(affiliate::NUMERIC))::BIGINT,
                'Reviewed offline opening; original facts retained by immutable import receipt') RETURNING id INTO opening_journal_id;
        FOR leg IN SELECT value FROM jsonb_array_elements(legs) WHERE (value->>'units')::BIGINT<>0 ORDER BY (value->>'id')::BIGINT LOOP
            INSERT INTO core_billing.ledger_entries(journal_id,ledger_account_id,delta_units)
                VALUES(opening_journal_id,(leg->>'id')::BIGINT,(leg->>'units')::BIGINT);
        END LOOP;
    END IF;
    SELECT jsonb_build_object('status','posted','journal_id',opening_journal_id,'entries',COALESCE(jsonb_agg(
        jsonb_build_object('ledger_account_id',e.ledger_account_id,'delta_units',e.delta_units,
        'balance_after_units',e.balance_after_units,'balance_revision',e.balance_revision) ORDER BY e.ledger_account_id),'[]'::JSONB))
        INTO result FROM core_billing.ledger_entries e WHERE e.journal_id=opening_journal_id;
    INSERT INTO core_billing.ledger_operations(scope,operation_key,request,result) VALUES('offline-opening',op_key,p_request,result);
    INSERT INTO core_billing.opening_imports VALUES(migration,entity,p_request->>'source_row_sha256',account,p_request,result);
    RETURN result;
EXCEPTION WHEN unique_violation THEN
    -- A lock acquired after the SSI snapshot cannot refresh that snapshot.
    -- Retry the complete import transaction, then compare immutable receipts.
    RAISE EXCEPTION 'retry complete opening import transaction' USING ERRCODE='40001';
END $$;

-- These are historical reporting/eligibility facts, never additional money.
-- A counter row must bind to the SAME source user as its reviewed opening.
CREATE TABLE core_billing.account_opening_counters (
    account_id BIGINT PRIMARY KEY REFERENCES core_identity.accounts(id),
    user_id BIGINT NOT NULL UNIQUE REFERENCES core_identity.users(id),
    migration_id TEXT NOT NULL REFERENCES core_billing.opening_import_runs(migration_id),
    source_entity_key TEXT NOT NULL,
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
    source_row_sha256 TEXT NOT NULL CHECK (source_row_sha256 ~ '^[0-9a-f]{64}$'),
    observed_at BIGINT NOT NULL,
    used_quota BIGINT NOT NULL,
    request_count BIGINT NOT NULL,
    aff_history BIGINT NOT NULL,
    request JSONB NOT NULL,
    UNIQUE (migration_id,source_entity_key),
    FOREIGN KEY (migration_id,source_entity_key) REFERENCES core_billing.opening_imports(migration_id,source_entity_key)
);
CREATE TRIGGER account_opening_counters_immutable BEFORE UPDATE OR DELETE ON core_billing.account_opening_counters
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER account_opening_counters_no_truncate BEFORE TRUNCATE ON core_billing.account_opening_counters
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();

CREATE FUNCTION core_billing.import_account_opening_counters(p_request JSONB) RETURNS JSONB
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
DECLARE prior core_billing.account_opening_counters%ROWTYPE; account BIGINT; usr BIGINT;
    used BIGINT; requests BIGINT; affiliate_history BIGINT; result JSONB;
BEGIN
    IF current_user <> pg_get_userbyid((SELECT nspowner FROM pg_namespace WHERE nspname='core_billing')) THEN
        RAISE EXCEPTION 'opening counters require the offline schema owner' USING ERRCODE='42501';
    END IF;
    IF current_setting('transaction_isolation') <> 'serializable' OR current_setting('synchronous_commit') <> 'on' THEN
        RAISE EXCEPTION 'opening counters require serializable durable commit' USING ERRCODE='25001';
    END IF;
    IF NOT COALESCE(jsonb_typeof(p_request)='object' AND octet_length(p_request::TEXT)<=8192
        AND (SELECT array_agg(k ORDER BY k) FROM jsonb_object_keys(p_request) k) = ARRAY[
            'account_id','aff_history','mapping_sha256','migration_id','observed_at','request_count',
            'source_archive_sha256','source_entity_key','source_row_sha256','used_quota','user_id']::TEXT[]
        AND jsonb_typeof(p_request->'account_id')='number' AND jsonb_typeof(p_request->'user_id')='number'
        AND jsonb_typeof(p_request->'used_quota')='number' AND jsonb_typeof(p_request->'request_count')='number'
        AND jsonb_typeof(p_request->'aff_history')='number' AND jsonb_typeof(p_request->'observed_at')='number'
        AND jsonb_typeof(p_request->'migration_id')='string' AND jsonb_typeof(p_request->'source_entity_key')='string'
        AND jsonb_typeof(p_request->'source_archive_sha256')='string' AND jsonb_typeof(p_request->'mapping_sha256')='string'
        AND jsonb_typeof(p_request->'source_row_sha256')='string',FALSE) THEN
        RAISE EXCEPTION 'invalid opening counters' USING ERRCODE='P1000';
    END IF;
    account := (p_request->>'account_id')::BIGINT; usr := (p_request->>'user_id')::BIGINT;
    used := (p_request->>'used_quota')::BIGINT; requests := (p_request->>'request_count')::BIGINT;
    affiliate_history := (p_request->>'aff_history')::BIGINT;
    IF account<=0 OR usr<=0 OR abs(used::NUMERIC)>9007199254740991
        OR abs(requests::NUMERIC)>9007199254740991 OR abs(affiliate_history::NUMERIC)>9007199254740991 THEN
        RAISE EXCEPTION 'invalid opening counters' USING ERRCODE='P1000';
    END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('lmm-opening-import-v1',0));
    SELECT * INTO prior FROM core_billing.account_opening_counters WHERE account_id=account;
    IF FOUND THEN
        IF prior.request IS DISTINCT FROM p_request THEN
            RAISE EXCEPTION 'opening counters have different parameters' USING ERRCODE='P1001';
        END IF;
    ELSE
        IF NOT EXISTS (SELECT 1 FROM core_billing.opening_imports i JOIN core_billing.opening_import_runs r
            ON r.migration_id=i.migration_id WHERE i.account_id=account AND i.migration_id=p_request->>'migration_id'
            AND i.source_entity_key=p_request->>'source_entity_key' AND i.source_row_sha256=p_request->>'source_row_sha256'
            AND r.source_archive_sha256=p_request->>'source_archive_sha256' AND r.mapping_sha256=p_request->>'mapping_sha256'
            AND r.observed_at=(p_request->>'observed_at')::BIGINT) THEN
            RAISE EXCEPTION 'opening counters source does not match the original opening' USING ERRCODE='P1001';
        END IF;
        PERFORM id FROM core_identity.users WHERE id=usr AND personal_account_id=account FOR SHARE;
        IF NOT FOUND THEN RAISE EXCEPTION 'opening counters identity mismatch' USING ERRCODE='P1001'; END IF;
        INSERT INTO core_billing.account_opening_counters VALUES(account,usr,p_request->>'migration_id',
            p_request->>'source_entity_key',p_request->>'source_archive_sha256',p_request->>'mapping_sha256',
            p_request->>'source_row_sha256',(p_request->>'observed_at')::BIGINT,used,requests,affiliate_history,p_request);
    END IF;
    result := jsonb_build_object('status','imported','account_id',account,'user_id',usr,
        'used_quota',used,'request_count',requests,'aff_history',affiliate_history);
    RETURN result;
EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'retry complete opening counters import transaction' USING ERRCODE='40001';
END $$;

-- Never grant table/sequence writes to the runtime or to Go extensions.
-- Integration grants USAGE, selected SELECTs and ONLY these two entry functions.
REVOKE ALL ON core_billing.ledger_accounts, core_billing.balance_state,
    core_billing.ledger_operations, core_billing.ledger_journals, core_billing.ledger_entries FROM PUBLIC;
REVOKE ALL ON FUNCTION core_billing.open_ledger_wallet(BIGINT), core_billing.post_ledger(JSONB),
    core_billing.ledger_immutable(), core_billing.ledger_new_account(),
    core_billing.ledger_apply_entry(), core_billing.ledger_check_journal() FROM PUBLIC;
REVOKE ALL ON core_billing.opening_import_runs,core_billing.opening_imports FROM PUBLIC;
REVOKE ALL ON FUNCTION core_billing.import_opening_balance(JSONB) FROM PUBLIC;
REVOKE ALL ON core_billing.account_opening_counters FROM PUBLIC;
REVOKE ALL ON FUNCTION core_billing.import_account_opening_counters(JSONB) FROM PUBLIC;
