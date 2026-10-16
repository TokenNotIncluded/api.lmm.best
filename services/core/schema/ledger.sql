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
    kind TEXT NOT NULL CHECK (kind IN ('credit', 'transfer', 'charge', 'reserve', 'capture', 'commerce_capture', 'claim_affiliate', 'opening', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse', 'legacy_escrow_opening', 'legacy_escrow_payout', 'legacy_escrow_release')),
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
    CHECK ((kind IN ('capture', 'commerce_capture', 'release', 'refund', 'refund_topup', 'refund_commerce', 'reverse', 'legacy_escrow_payout', 'legacy_escrow_release')) = (parent_journal_id IS NOT NULL)),
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

-- Historical bounty custody, not a wallet Reserve or merchant order.
-- @mark migration.bounty-custody role=implementation
-- Lifecycle policy: project AGPL-3.0 revision
-- 3948a0a1ee1cefdd016a7aa45a11851545bb4b85, apps/api-go/model/open_source_bounty.go.
-- No source unit conversion, submission transition or dispute adjudication here.
CREATE TABLE core_billing.legacy_escrow_batch_runs (
    migration_id TEXT PRIMARY KEY,
    source_archive_sha256 TEXT NOT NULL CHECK(source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK(mapping_sha256 ~ '^[0-9a-f]{64}$'),
    review_proof_sha256 TEXT NOT NULL CHECK(review_proof_sha256 ~ '^[0-9a-f]{64}$'),
    unit TEXT NOT NULL CHECK(unit='credit_500k_usd'),
    observed_at BIGINT NOT NULL CHECK(observed_at>0),
    request JSONB NOT NULL,
    result JSONB NOT NULL
);
CREATE TABLE core_billing.legacy_escrow_projects (
    migration_id TEXT NOT NULL REFERENCES core_billing.legacy_escrow_batch_runs(migration_id) DEFERRABLE INITIALLY DEFERRED,
    source_project_id BIGINT NOT NULL CHECK(source_project_id>0),
    source_archive_sha256 TEXT NOT NULL,
    owner_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    payer_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    source_row_sha256 TEXT NOT NULL CHECK(source_row_sha256 ~ '^[0-9a-f]{64}$'),
    source_parent_plan_sha256 TEXT NOT NULL CHECK(source_parent_plan_sha256 ~ '^[0-9a-f]{64}$'),
    escrow_units BIGINT NOT NULL CHECK(escrow_units BETWEEN 1 AND 9007199254740991),
    status TEXT NOT NULL CHECK(status IN ('published','paused')),
    opening_journal_id BIGINT NOT NULL UNIQUE REFERENCES core_billing.ledger_journals(id),
    source JSONB NOT NULL,
    PRIMARY KEY(migration_id,source_project_id),
    UNIQUE(source_archive_sha256,source_project_id)
);
CREATE TABLE core_billing.legacy_escrow_claims (
    migration_id TEXT NOT NULL,
    source_project_id BIGINT NOT NULL,
    source_challenge_id BIGINT NOT NULL CHECK(source_challenge_id>0),
    participant_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    recipient_account_id BIGINT NOT NULL REFERENCES core_identity.accounts(id),
    source_row_sha256 TEXT NOT NULL CHECK(source_row_sha256 ~ '^[0-9a-f]{64}$'),
    rights_status TEXT NOT NULL CHECK(rights_status IN ('active_future_reward','historical_rejection_guard_only')),
    source_reward_quota BIGINT NOT NULL CHECK(source_reward_quota BETWEEN 0 AND 9007199254740991),
    payout_units BIGINT CHECK(payout_units BETWEEN 1 AND 9007199254740991),
    status TEXT NOT NULL CHECK(status IN ('accepted','submitted','approved','rejected','withdrawn','cancelled')),
    rejected_at BIGINT NOT NULL CHECK(rejected_at>=0),
    paid_at BIGINT NOT NULL CHECK(paid_at>=0),
    source JSONB NOT NULL,
    CHECK((rights_status='active_future_reward')=(payout_units IS NOT NULL)),
    CHECK(rights_status<>'historical_rejection_guard_only' OR status='rejected'),
    PRIMARY KEY(migration_id,source_challenge_id),
    UNIQUE(migration_id,source_project_id,source_challenge_id),
    FOREIGN KEY(migration_id,source_project_id) REFERENCES core_billing.legacy_escrow_projects(migration_id,source_project_id)
);
CREATE TABLE core_billing.legacy_escrow_disputes (
    migration_id TEXT NOT NULL,
    source_project_id BIGINT NOT NULL,
    source_dispute_id BIGINT NOT NULL CHECK(source_dispute_id>0),
    source_challenge_id BIGINT NOT NULL,
    source_row_sha256 TEXT NOT NULL CHECK(source_row_sha256 ~ '^[0-9a-f]{64}$'),
    status TEXT NOT NULL CHECK(status IN ('open','resolved_paid','resolved_denied')),
    source JSONB NOT NULL,
    PRIMARY KEY(migration_id,source_dispute_id),
    FOREIGN KEY(migration_id,source_project_id,source_challenge_id)
        REFERENCES core_billing.legacy_escrow_claims(migration_id,source_project_id,source_challenge_id)
);
CREATE TABLE core_billing.legacy_escrow_state (
    migration_id TEXT NOT NULL,
    source_project_id BIGINT NOT NULL,
    remaining_units BIGINT NOT NULL CHECK(remaining_units>=0),
    revision BIGINT NOT NULL CHECK(revision>0),
    closed BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY(migration_id,source_project_id),
    FOREIGN KEY(migration_id,source_project_id) REFERENCES core_billing.legacy_escrow_projects(migration_id,source_project_id),
    CHECK(NOT closed OR remaining_units=0)
);
CREATE TABLE core_billing.legacy_escrow_operations (
    operation_key TEXT PRIMARY KEY CHECK(operation_key ~ '^[A-Za-z0-9._:/-]{1,128}$'),
    migration_id TEXT NOT NULL,
    source_project_id BIGINT NOT NULL,
    source_challenge_id BIGINT,
    kind TEXT NOT NULL CHECK(kind IN ('payout','close')),
    actor_user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    request JSONB NOT NULL,
    result JSONB NOT NULL,
    FOREIGN KEY(migration_id,source_project_id) REFERENCES core_billing.legacy_escrow_projects(migration_id,source_project_id),
    FOREIGN KEY(migration_id,source_project_id,source_challenge_id)
        REFERENCES core_billing.legacy_escrow_claims(migration_id,source_project_id,source_challenge_id),
    CHECK((kind='payout')=(source_challenge_id IS NOT NULL))
);
CREATE UNIQUE INDEX legacy_escrow_claim_paid_once ON core_billing.legacy_escrow_operations(migration_id,source_challenge_id)
    WHERE kind='payout' AND result->>'status'='posted';
CREATE UNIQUE INDEX legacy_escrow_close_once ON core_billing.legacy_escrow_operations(migration_id,source_project_id)
    WHERE kind='close' AND result->>'status'='posted';

CREATE TRIGGER legacy_escrow_batch_immutable BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_batch_runs FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_batch_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_batch_runs FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_projects_immutable BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_projects FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_projects_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_projects FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_claims_immutable BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_claims FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_claims_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_claims FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_disputes_immutable BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_disputes FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_disputes_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_disputes FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_operations_immutable BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_operations FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER legacy_escrow_operations_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_operations FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE FUNCTION core_billing.legacy_escrow_guard_state() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
BEGIN
    IF TG_OP IN ('DELETE','TRUNCATE') THEN RAISE EXCEPTION 'escrow state cannot be removed' USING ERRCODE='55000'; END IF;
    IF NEW.migration_id IS DISTINCT FROM OLD.migration_id OR NEW.source_project_id<>OLD.source_project_id
        OR NEW.revision<>OLD.revision+1 OR NEW.remaining_units>OLD.remaining_units OR OLD.closed THEN
        RAISE EXCEPTION 'invalid escrow state transition' USING ERRCODE='23514';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER legacy_escrow_state_guard BEFORE UPDATE OR DELETE ON core_billing.legacy_escrow_state FOR EACH ROW EXECUTE FUNCTION core_billing.legacy_escrow_guard_state();
CREATE TRIGGER legacy_escrow_state_no_truncate BEFORE TRUNCATE ON core_billing.legacy_escrow_state FOR EACH STATEMENT EXECUTE FUNCTION core_billing.legacy_escrow_guard_state();
CREATE FUNCTION core_billing.legacy_escrow_check_remaining() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE expected NUMERIC; actual BIGINT;
BEGIN
    SELECT p.escrow_units-COALESCE((SELECT sum(j.amount_units) FROM core_billing.ledger_journals j
        WHERE j.parent_journal_id=p.opening_journal_id AND j.kind IN ('legacy_escrow_payout','legacy_escrow_release')),0),s.remaining_units
        INTO STRICT expected,actual FROM core_billing.legacy_escrow_projects p JOIN core_billing.legacy_escrow_state s USING(migration_id,source_project_id)
        WHERE p.migration_id=NEW.migration_id AND p.source_project_id=NEW.source_project_id;
    IF expected IS DISTINCT FROM actual::NUMERIC THEN RAISE EXCEPTION 'escrow journal/state mismatch' USING ERRCODE='23514'; END IF;
    RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER legacy_escrow_remaining_balanced AFTER INSERT OR UPDATE ON core_billing.legacy_escrow_state
    DEFERRABLE INITIALLY DEFERRED FOR EACH ROW EXECUTE FUNCTION core_billing.legacy_escrow_check_remaining();

CREATE FUNCTION core_billing.legacy_escrow_keys(value JSONB, expected TEXT[]) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
    SELECT CASE WHEN jsonb_typeof(value)='object' THEN
        (SELECT array_agg(k ORDER BY k) FROM jsonb_object_keys(value) k)=(SELECT array_agg(k ORDER BY k) FROM unnest(expected) k)
        ELSE FALSE END
$$;
CREATE FUNCTION core_billing.legacy_escrow_uint(value JSONB, positive BOOLEAN DEFAULT FALSE) RETURNS BIGINT
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
BEGIN
    IF NOT COALESCE(jsonb_typeof(value)='number' AND value::TEXT ~ '^(0|[1-9][0-9]*)$'
        AND (value::TEXT)::NUMERIC BETWEEN 0 AND 9007199254740991
        AND (NOT positive OR (value::TEXT)::NUMERIC>0),FALSE) THEN
        RAISE EXCEPTION 'escrow requires exact nonnegative integer' USING ERRCODE='P1000';
    END IF;
    RETURN (value::TEXT)::BIGINT;
END $$;
CREATE FUNCTION core_billing.legacy_escrow_id(value JSONB) RETURNS BIGINT
LANGUAGE plpgsql IMMUTABLE SET search_path=pg_catalog AS $$
BEGIN
    -- IDs are exact positive BIGINT, not monetary JS-safe quota values.
    IF jsonb_typeof(value) IS DISTINCT FROM 'number' THEN
        RAISE EXCEPTION 'escrow requires exact positive BIGINT identifier' USING ERRCODE='P1000';
    END IF;
    IF value::TEXT !~ '^[1-9][0-9]*$' THEN
        RAISE EXCEPTION 'escrow requires exact positive BIGINT identifier' USING ERRCODE='P1000';
    END IF;
    IF (value::TEXT)::NUMERIC>9223372036854775807 THEN
        RAISE EXCEPTION 'escrow identifier exceeds BIGINT' USING ERRCODE='P1000';
    END IF;
    RETURN (value::TEXT)::BIGINT;
END $$;
CREATE FUNCTION core_billing.legacy_escrow_hash(value JSONB) RETURNS BOOLEAN
LANGUAGE sql IMMUTABLE SET search_path=pg_catalog AS $$
    SELECT COALESCE(jsonb_typeof(value)='string' AND (value#>>'{}') ~ '^[0-9a-f]{64}$',FALSE)
$$;

-- OFFLINE owner only. Entire reviewed batch is one identity, not an append API.
CREATE FUNCTION core_billing.import_legacy_escrow_batch(p_request JSONB) RETURNS JSONB
LANGUAGE plpgsql SET search_path=pg_catalog AS $$
DECLARE migration TEXT:=p_request->>'migration_id'; archive TEXT:=p_request->>'source_archive_sha256';
    mapping TEXT:=p_request->>'mapping_sha256'; observed BIGINT; prior core_billing.legacy_escrow_batch_runs%ROWTYPE;
    project JSONB; claim JSONB; dispute JSONB; project_id BIGINT; owner_id BIGINT; payer_account_id_var BIGINT; amount BIGINT;
    challenge_id BIGINT; participant BIGINT; recipient BIGINT; paid BIGINT; rejected BIGINT; now_units BIGINT;
    owed NUMERIC; held BIGINT; clearing BIGINT; journal BIGINT; op_key TEXT; result JSONB; item_result JSONB; results JSONB:='[]'::JSONB;
BEGIN
    IF current_user<>pg_get_userbyid((SELECT nspowner FROM pg_namespace WHERE nspname='core_billing')) THEN
        RAISE EXCEPTION 'escrow import requires offline schema owner' USING ERRCODE='42501';
    END IF;
    IF current_setting('transaction_isolation')<>'serializable' OR current_setting('synchronous_commit')<>'on' THEN
        RAISE EXCEPTION 'escrow import requires durable serializable transaction' USING ERRCODE='25001';
    END IF;
    IF NOT COALESCE(core_billing.legacy_escrow_keys(p_request,ARRAY['migration_id','source_archive_sha256','mapping_sha256','review_proof_sha256','unit','observed_at','projects'])
        AND octet_length(p_request::TEXT)<=1048576 AND jsonb_typeof(p_request->'migration_id')='string'
        AND migration ~ '^[A-Za-z0-9._:/-]{1,128}$' AND p_request->>'unit'='credit_500k_usd'
        AND core_billing.legacy_escrow_hash(p_request->'source_archive_sha256') AND core_billing.legacy_escrow_hash(p_request->'mapping_sha256')
        AND core_billing.legacy_escrow_hash(p_request->'review_proof_sha256') AND jsonb_typeof(p_request->'projects')='array'
        AND jsonb_array_length(p_request->'projects') BETWEEN 1 AND 128,FALSE) THEN
        RAISE EXCEPTION 'invalid escrow import batch' USING ERRCODE='P1000';
    END IF;
    observed:=core_billing.legacy_escrow_uint(p_request->'observed_at',TRUE);
    IF observed>253402300799 THEN RAISE EXCEPTION 'invalid escrow observation time' USING ERRCODE='P1000'; END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended('lmm-opening-import-v1',0));
    SELECT * INTO prior FROM core_billing.legacy_escrow_batch_runs WHERE migration_id=migration;
    IF FOUND THEN
        IF prior.request IS DISTINCT FROM p_request THEN RAISE EXCEPTION 'escrow batch parameters changed' USING ERRCODE='P1001'; END IF;
        RETURN prior.result;
    END IF;
    IF NOT EXISTS(SELECT FROM core_billing.opening_import_runs r WHERE r.migration_id=migration
        AND r.source_archive_sha256=archive AND r.mapping_sha256=mapping AND r.observed_at=observed) THEN
        RAISE EXCEPTION 'escrow requires same reviewed wallet opening run' USING ERRCODE='P1001';
    END IF;
    -- Validate the WHOLE batch before opening wallets, nextval or money writes.
    FOR project IN SELECT value FROM jsonb_array_elements(p_request->'projects') LOOP
        IF NOT COALESCE(core_billing.legacy_escrow_keys(project,ARRAY['source_project_id','owner_user_id','payer_account_id','source_row_sha256','source_parent_plan_sha256','escrow_units','status','claims','disputes'])
            AND core_billing.legacy_escrow_hash(project->'source_row_sha256') AND core_billing.legacy_escrow_hash(project->'source_parent_plan_sha256')
            AND project->>'status' IN ('published','paused') AND jsonb_typeof(project->'claims')='array' AND jsonb_typeof(project->'disputes')='array'
            AND jsonb_array_length(project->'claims')<=2048 AND jsonb_array_length(project->'disputes')<=2048,FALSE) THEN
            RAISE EXCEPTION 'invalid frozen escrow project' USING ERRCODE='P1000';
        END IF;
        project_id:=core_billing.legacy_escrow_id(project->'source_project_id');
        owner_id:=core_billing.legacy_escrow_id(project->'owner_user_id');
        payer_account_id_var:=core_billing.legacy_escrow_id(project->'payer_account_id');
        amount:=core_billing.legacy_escrow_uint(project->'escrow_units',TRUE);
        PERFORM u.id FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id
            WHERE u.id=owner_id AND u.personal_account_id=payer_account_id_var AND a.kind='personal' FOR SHARE OF u,a;
        IF NOT FOUND THEN RAISE EXCEPTION 'escrow original payer identity mismatch' USING ERRCODE='P1001'; END IF;
        IF NOT EXISTS(SELECT FROM core_billing.opening_imports i WHERE i.migration_id=migration AND i.account_id=payer_account_id_var
            AND i.request->>'source_archive_sha256'=archive AND i.request->>'mapping_sha256'=mapping
            AND i.request->>'reserved_units'='0') OR EXISTS(SELECT FROM core_billing.legacy_escrow_projects p WHERE p.payer_account_id=payer_account_id_var) THEN
            RAISE EXCEPTION 'escrow payer opening absent, nonzero reserves or prior escrow batch' USING ERRCODE='P1001';
        END IF;
        IF EXISTS(SELECT FROM core_billing.ledger_entries e JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id
            JOIN core_billing.ledger_journals j ON j.id=e.journal_id WHERE a.owner_account_id=payer_account_id_var AND j.kind<>'opening') THEN
            RAISE EXCEPTION 'escrow import target already has runtime financial activity' USING ERRCODE='P1001';
        END IF;
        IF EXISTS(SELECT FROM core_billing.legacy_escrow_projects p WHERE p.source_archive_sha256=archive AND p.source_project_id=project_id) THEN
            RAISE EXCEPTION 'escrow source project already imported' USING ERRCODE='P1001';
        END IF;
        owed:=0;
        FOR claim IN SELECT value FROM jsonb_array_elements(project->'claims') LOOP
            IF NOT COALESCE(core_billing.legacy_escrow_keys(claim,ARRAY['source_challenge_id','participant_user_id','recipient_account_id','source_row_sha256','rights_status','source_reward_quota','payout_units','status','rejected_at','paid_at'])
                AND core_billing.legacy_escrow_hash(claim->'source_row_sha256') AND claim->>'status' IN ('accepted','submitted','rejected')
                AND claim->>'rights_status' IN ('active_future_reward','historical_rejection_guard_only'),FALSE) THEN
                RAISE EXCEPTION 'invalid frozen escrow claim' USING ERRCODE='P1000';
            END IF;
            challenge_id:=core_billing.legacy_escrow_id(claim->'source_challenge_id');
            participant:=core_billing.legacy_escrow_id(claim->'participant_user_id');
            recipient:=core_billing.legacy_escrow_id(claim->'recipient_account_id');
            PERFORM core_billing.legacy_escrow_uint(claim->'source_reward_quota');
            IF claim->>'rights_status'='active_future_reward' THEN
                now_units:=core_billing.legacy_escrow_uint(claim->'payout_units',TRUE);
            ELSE
                IF jsonb_typeof(claim->'payout_units') IS DISTINCT FROM 'null' OR claim->>'status'<>'rejected' THEN
                    RAISE EXCEPTION 'historical rejection evidence is not current-unit payout money' USING ERRCODE='P1000';
                END IF;
                now_units:=NULL;
            END IF;
            paid:=core_billing.legacy_escrow_uint(claim->'paid_at'); rejected:=core_billing.legacy_escrow_uint(claim->'rejected_at');
            IF participant=owner_id OR recipient=payer_account_id_var OR paid>observed OR rejected>observed
                OR (claim->>'status' IN ('accepted','submitted','rejected') AND paid<>0)
                OR (claim->>'status'='rejected' AND rejected=0)
                OR (claim->>'status' IN ('accepted','submitted') AND claim->>'rights_status'<>'active_future_reward') THEN
                RAISE EXCEPTION 'invalid frozen escrow claim state' USING ERRCODE='P1000';
            END IF;
            PERFORM u.id FROM core_identity.users u JOIN core_identity.accounts a ON a.id=u.personal_account_id
                WHERE u.id=participant AND u.personal_account_id=recipient AND a.kind='personal' FOR SHARE OF u,a;
            IF NOT FOUND THEN RAISE EXCEPTION 'escrow recipient identity mismatch' USING ERRCODE='P1001'; END IF;
            IF (SELECT count(*) FROM core_billing.ledger_accounts a WHERE a.owner_account_id=recipient AND a.bucket IN ('wallet','debt'))<>2 THEN
                RAISE EXCEPTION 'escrow recipient wallet must be prepared before import' USING ERRCODE='P1001';
            END IF;
            IF claim->>'status' IN ('accepted','submitted') THEN owed:=owed+now_units; END IF;
        END LOOP;
        FOR dispute IN SELECT value FROM jsonb_array_elements(project->'disputes') LOOP
            IF NOT COALESCE(core_billing.legacy_escrow_keys(dispute,ARRAY['source_dispute_id','source_challenge_id','source_row_sha256','status','reward_quota_snapshot','project_escrow_quota_snapshot','tip_quota_snapshot'])
                AND core_billing.legacy_escrow_hash(dispute->'source_row_sha256') AND dispute->>'status' IN ('open','resolved_paid','resolved_denied'),FALSE) THEN
                RAISE EXCEPTION 'invalid frozen escrow dispute' USING ERRCODE='P1000';
            END IF;
            PERFORM core_billing.legacy_escrow_id(dispute->'source_dispute_id');
            challenge_id:=core_billing.legacy_escrow_id(dispute->'source_challenge_id');
            PERFORM core_billing.legacy_escrow_uint(dispute->'reward_quota_snapshot');
            PERFORM core_billing.legacy_escrow_uint(dispute->'project_escrow_quota_snapshot');
            PERFORM core_billing.legacy_escrow_uint(dispute->'tip_quota_snapshot');
            IF NOT EXISTS(SELECT FROM jsonb_array_elements(project->'claims') c WHERE c->>'source_challenge_id'=challenge_id::TEXT) THEN
                RAISE EXCEPTION 'escrow dispute does not bind frozen project claim' USING ERRCODE='P1001';
            END IF;
        END LOOP;
        FOR claim IN SELECT value FROM jsonb_array_elements(project->'claims') LOOP
            IF EXISTS(SELECT FROM jsonb_array_elements(project->'disputes') d
                WHERE d->>'source_challenge_id'=claim->>'source_challenge_id' AND d->>'status'='resolved_paid') THEN
                RAISE EXCEPTION 'resolved paid dispute cannot bind an unpaid imported claim' USING ERRCODE='P1001';
            END IF;
            IF claim->>'rights_status'='historical_rejection_guard_only' AND
                (EXISTS(SELECT FROM jsonb_array_elements(project->'disputes') d WHERE d->>'source_challenge_id'=claim->>'source_challenge_id' AND d->>'status'='open') OR
                 (core_billing.legacy_escrow_uint(claim->'rejected_at')>observed-604800 AND NOT EXISTS(
                    SELECT FROM jsonb_array_elements(project->'disputes') d WHERE d->>'source_challenge_id'=claim->>'source_challenge_id' AND d->>'status'='resolved_denied'))) THEN
                RAISE EXCEPTION 'active appeal/dispute requires reviewed current-unit payout basis' USING ERRCODE='P1001';
            END IF;
        END LOOP;
        -- Rejected rights remain protected by an open dispute or the exact
        -- historical seven-day appeal window. Never count historical snapshots.
        SELECT owed+COALESCE(sum(core_billing.legacy_escrow_uint(c->'payout_units',TRUE)),0) INTO owed
            FROM jsonb_array_elements(project->'claims') c WHERE c->>'status'='rejected' AND c->>'rights_status'='active_future_reward' AND
            (EXISTS(SELECT FROM jsonb_array_elements(project->'disputes') d WHERE d->>'source_challenge_id'=c->>'source_challenge_id' AND d->>'status'='open')
             OR (core_billing.legacy_escrow_uint(c->'rejected_at')>observed-604800 AND NOT EXISTS(SELECT FROM jsonb_array_elements(project->'disputes') d
                WHERE d->>'source_challenge_id'=c->>'source_challenge_id' AND d->>'status' IN ('resolved_paid','resolved_denied'))));
        IF owed>amount THEN RAISE EXCEPTION 'escrow cannot cover frozen unpaid rights' USING ERRCODE='P1001'; END IF;
    END LOOP;
    IF EXISTS(SELECT FROM jsonb_array_elements(p_request->'projects') p GROUP BY p->>'source_project_id' HAVING count(*)>1)
        OR EXISTS(SELECT FROM jsonb_array_elements(p_request->'projects') p CROSS JOIN LATERAL jsonb_array_elements(p->'claims') c GROUP BY c->>'source_challenge_id' HAVING count(*)>1)
        OR EXISTS(SELECT FROM jsonb_array_elements(p_request->'projects') p CROSS JOIN LATERAL jsonb_array_elements(p->'disputes') d GROUP BY d->>'source_dispute_id' HAVING count(*)>1) THEN
        RAISE EXCEPTION 'duplicate escrow source identity in batch' USING ERRCODE='P1001';
    END IF;
    SELECT id INTO STRICT clearing FROM core_billing.ledger_accounts WHERE bucket='clearing';
    -- Sorted project and balance order, with the global offline opening lock.
    FOR project IN SELECT value FROM jsonb_array_elements(p_request->'projects') ORDER BY (value->>'source_project_id')::BIGINT LOOP
        project_id:=(project->>'source_project_id')::BIGINT; payer_account_id_var:=(project->>'payer_account_id')::BIGINT; amount:=(project->>'escrow_units')::BIGINT;
        SELECT id INTO STRICT held FROM core_billing.ledger_accounts WHERE owner_account_id=payer_account_id_var AND bucket='reserved';
        PERFORM b.ledger_account_id FROM core_billing.balance_state b WHERE b.ledger_account_id IN(held,clearing) ORDER BY b.ledger_account_id FOR NO KEY UPDATE;
        op_key:=encode(sha256(convert_to(jsonb_build_array(migration,project_id)::TEXT,'UTF8')),'hex');
        INSERT INTO core_billing.ledger_journals(scope,operation_key,kind,payer_id,payee_id,amount_units,reason)
            VALUES('legacy-escrow-opening',op_key,'legacy_escrow_opening',clearing,held,amount,'Reviewed historical bounty escrow; no new wallet debit') RETURNING id INTO journal;
        INSERT INTO core_billing.ledger_entries(journal_id,ledger_account_id,delta_units)
            SELECT journal,id,units FROM (VALUES(clearing,-amount),(held,amount)) AS legs(id,units) ORDER BY id;
        item_result:=jsonb_build_object('status','posted','source_project_id',project_id,'journal_id',journal,'remaining_units',amount,'revision',1);
        INSERT INTO core_billing.ledger_operations(scope,operation_key,request,result) VALUES('legacy-escrow-opening',op_key,project,item_result);
        INSERT INTO core_billing.legacy_escrow_projects VALUES(migration,project_id,archive,(project->>'owner_user_id')::BIGINT,payer_account_id_var,
            project->>'source_row_sha256',project->>'source_parent_plan_sha256',amount,project->>'status',journal,project);
        INSERT INTO core_billing.legacy_escrow_state VALUES(migration,project_id,amount,1,FALSE);
        FOR claim IN SELECT value FROM jsonb_array_elements(project->'claims') LOOP
            INSERT INTO core_billing.legacy_escrow_claims VALUES(migration,project_id,(claim->>'source_challenge_id')::BIGINT,
                (claim->>'participant_user_id')::BIGINT,(claim->>'recipient_account_id')::BIGINT,claim->>'source_row_sha256',
                claim->>'rights_status',(claim->>'source_reward_quota')::BIGINT,(claim->>'payout_units')::BIGINT,claim->>'status',(claim->>'rejected_at')::BIGINT,(claim->>'paid_at')::BIGINT,claim);
        END LOOP;
        FOR dispute IN SELECT value FROM jsonb_array_elements(project->'disputes') LOOP
            INSERT INTO core_billing.legacy_escrow_disputes VALUES(migration,project_id,(dispute->>'source_dispute_id')::BIGINT,
                (dispute->>'source_challenge_id')::BIGINT,dispute->>'source_row_sha256',dispute->>'status',dispute);
        END LOOP;
        results:=results||jsonb_build_array(item_result);
    END LOOP;
    result:=jsonb_build_object('status','posted','migration_id',migration,'unit','credit_500k_usd','projects',results);
    INSERT INTO core_billing.legacy_escrow_batch_runs VALUES(migration,archive,mapping,p_request->>'review_proof_sha256','credit_500k_usd',observed,p_request,result);
    RETURN result;
EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'retry complete escrow import transaction with original batch' USING ERRCODE='40001';
END $$;

-- Trusted Core-only function. Rust authenticates/locks current Session authority
-- in THIS transaction. IDs here are derived from that principal, not HTTP/Go.
CREATE FUNCTION core_billing.legacy_escrow_finalize(p_request JSONB,p_actor_user_id BIGINT,p_actor_account_id BIGINT) RETURNS JSONB
LANGUAGE plpgsql SECURITY DEFINER SET search_path=pg_catalog AS $$
DECLARE migration TEXT:=p_request->>'migration_id'; project_id BIGINT; op_key TEXT:=p_request->>'operation_key'; kind TEXT:=p_request->>'kind';
    project core_billing.legacy_escrow_projects%ROWTYPE; state core_billing.legacy_escrow_state%ROWTYPE;
    claim core_billing.legacy_escrow_claims%ROWTYPE; prior core_billing.legacy_escrow_operations%ROWTYPE;
    challenge_id BIGINT; journal BIGINT; amount BIGINT; held BIGINT; original_wallet BIGINT; recipient_wallet BIGINT; recipient_debt BIGINT;
    debt_balance BIGINT; repayment BIGINT:=0; rejection TEXT; now_at BIGINT; result JSONB; request JSONB;
BEGIN
    IF current_setting('transaction_isolation')<>'serializable' OR current_setting('synchronous_commit')<>'on' THEN
        RAISE EXCEPTION 'escrow finalize requires durable serializable transaction' USING ERRCODE='25001';
    END IF;
    IF NOT COALESCE(octet_length(p_request::TEXT)<=8192 AND jsonb_typeof(p_request->'migration_id')='string'
        AND migration ~ '^[A-Za-z0-9._:/-]{1,128}$' AND jsonb_typeof(p_request->'operation_key')='string'
        AND op_key ~ '^[A-Za-z0-9._:/-]{1,128}$' AND
        ((kind='payout' AND core_billing.legacy_escrow_keys(p_request,ARRAY['kind','migration_id','source_project_id','source_challenge_id','operation_key']))
        OR (kind='close' AND core_billing.legacy_escrow_keys(p_request,ARRAY['kind','migration_id','source_project_id','operation_key']))),FALSE) THEN
        RAISE EXCEPTION 'invalid escrow command' USING ERRCODE='P1000';
    END IF;
    project_id:=core_billing.legacy_escrow_id(p_request->'source_project_id');
    IF kind='payout' THEN challenge_id:=core_billing.legacy_escrow_id(p_request->'source_challenge_id'); END IF;
    PERFORM pg_advisory_xact_lock(hashtextextended(jsonb_build_array('core-legacy-escrow',op_key)::TEXT,0));
    SELECT * INTO project FROM core_billing.legacy_escrow_projects WHERE migration_id=migration AND source_project_id=project_id;
    IF NOT FOUND THEN RAISE EXCEPTION 'escrow project not found' USING ERRCODE='P1002'; END IF;
    IF (project.owner_user_id,project.payer_account_id) IS DISTINCT FROM (p_actor_user_id,p_actor_account_id) THEN
        RAISE EXCEPTION 'escrow original owner required' USING ERRCODE='42501';
    END IF;
    request:=jsonb_build_object('command',p_request,'actor_user_id',p_actor_user_id,'actor_account_id',p_actor_account_id);
    SELECT * INTO prior FROM core_billing.legacy_escrow_operations WHERE operation_key=op_key;
    IF FOUND THEN
        IF prior.request IS DISTINCT FROM request THEN RAISE EXCEPTION 'escrow operation parameters changed' USING ERRCODE='P1001'; END IF;
        RETURN prior.result;
    END IF;
    SELECT * INTO STRICT state FROM core_billing.legacy_escrow_state WHERE migration_id=migration AND source_project_id=project_id FOR UPDATE;
    -- New commands preserve the first rejection; prior exact-key replays returned above.
    IF state.closed THEN rejection:='already_closed'; END IF;
    IF rejection IS NULL AND EXISTS(SELECT FROM core_billing.legacy_escrow_disputes d WHERE d.migration_id=migration AND d.source_project_id=project_id AND d.status='open') THEN
        rejection:='open_dispute';
    END IF;
    now_at:=floor(extract(epoch FROM clock_timestamp()))::BIGINT;
    IF kind='payout' THEN
        SELECT * INTO claim FROM core_billing.legacy_escrow_claims c WHERE c.migration_id=migration AND c.source_project_id=project_id AND c.source_challenge_id=challenge_id;
        IF NOT FOUND THEN RAISE EXCEPTION 'escrow frozen claim not found' USING ERRCODE='P1002'; END IF;
        IF rejection IS NULL AND (claim.status<>'submitted' OR claim.paid_at<>0 OR claim.rights_status<>'active_future_reward' OR claim.payout_units IS NULL) THEN rejection:='claim_not_submitted'; END IF;
        IF rejection IS NULL AND EXISTS(SELECT FROM core_billing.legacy_escrow_operations o WHERE o.migration_id=migration AND o.source_challenge_id=challenge_id
            AND o.kind='payout' AND o.result->>'status'='posted') THEN rejection:='claim_already_paid'; END IF;
        amount:=COALESCE(claim.payout_units,0);
        IF rejection IS NULL AND (amount<=0 OR amount>state.remaining_units) THEN rejection:='insufficient_escrow'; END IF;
        SELECT id INTO STRICT recipient_wallet FROM core_billing.ledger_accounts WHERE owner_account_id=claim.recipient_account_id AND bucket='wallet';
    ELSE
        IF rejection IS NULL AND EXISTS(SELECT FROM core_billing.legacy_escrow_claims c WHERE c.migration_id=migration AND c.source_project_id=project_id
            AND c.paid_at=0 AND c.status IN ('accepted','submitted') AND NOT EXISTS(SELECT FROM core_billing.legacy_escrow_operations o
                WHERE o.migration_id=migration AND o.source_challenge_id=c.source_challenge_id AND o.kind='payout' AND o.result->>'status'='posted')) THEN
            rejection:='active_claim';
        END IF;
        IF rejection IS NULL AND EXISTS(SELECT FROM core_billing.legacy_escrow_claims c WHERE c.migration_id=migration AND c.source_project_id=project_id AND c.status='rejected'
            AND c.rejected_at>now_at-604800 AND NOT EXISTS(SELECT FROM core_billing.legacy_escrow_disputes d WHERE d.migration_id=migration
                AND d.source_challenge_id=c.source_challenge_id AND d.status IN ('resolved_paid','resolved_denied'))) THEN rejection:='appeal_window'; END IF;
        amount:=state.remaining_units;
        SELECT id INTO STRICT recipient_wallet FROM core_billing.ledger_accounts WHERE owner_account_id=project.payer_account_id AND bucket='wallet';
    END IF;
    IF rejection IS NULL THEN
        SELECT id INTO STRICT held FROM core_billing.ledger_accounts WHERE owner_account_id=project.payer_account_id AND bucket='reserved';
        SELECT id INTO STRICT original_wallet FROM core_billing.ledger_accounts WHERE owner_account_id=project.payer_account_id AND bucket='wallet';
        SELECT id INTO STRICT recipient_debt FROM core_billing.ledger_accounts WHERE owner_account_id=(CASE WHEN kind='payout' THEN claim.recipient_account_id ELSE project.payer_account_id END) AND bucket='debt';
        PERFORM b.ledger_account_id FROM core_billing.balance_state b WHERE b.ledger_account_id IN(held,recipient_wallet,recipient_debt) ORDER BY b.ledger_account_id FOR NO KEY UPDATE;
        SELECT balance_units INTO STRICT debt_balance FROM core_billing.balance_state WHERE ledger_account_id=recipient_debt;
        repayment:=least(amount::NUMERIC,greatest(-debt_balance::NUMERIC,0))::BIGINT;
        IF amount>0 THEN
            INSERT INTO core_billing.ledger_journals(scope,operation_key,kind,payer_id,payee_id,amount_units,parent_journal_id,actor_user_id,reason)
                VALUES('core-legacy-escrow',op_key,CASE WHEN kind='payout' THEN 'legacy_escrow_payout' ELSE 'legacy_escrow_release' END,
                    CASE WHEN kind='payout' THEN original_wallet ELSE held END,recipient_wallet,amount,project.opening_journal_id,p_actor_user_id,
                    CASE WHEN kind='payout' THEN 'Original owner approved frozen historical bounty claim' ELSE 'Historical bounty remainder returned to original payer' END) RETURNING id INTO journal;
            INSERT INTO core_billing.ledger_entries(journal_id,ledger_account_id,delta_units)
                SELECT journal,id,units FROM (VALUES(held,-amount),(recipient_wallet,amount-repayment),(recipient_debt,repayment)) AS legs(id,units) WHERE units<>0 ORDER BY id;
        END IF;
        UPDATE core_billing.legacy_escrow_state SET remaining_units=remaining_units-amount,revision=revision+1,closed=(kind='close')
            WHERE migration_id=migration AND source_project_id=project_id RETURNING * INTO state;
    END IF;
    result:=jsonb_build_object('status',CASE WHEN rejection IS NULL THEN 'posted' ELSE 'rejected' END,'code',rejection,
        'operation_key',op_key,'journal_id',journal,'remaining_units',state.remaining_units,'revision',state.revision);
    IF journal IS NOT NULL THEN INSERT INTO core_billing.ledger_operations(scope,operation_key,request,result) VALUES('core-legacy-escrow',op_key,request,result); END IF;
    INSERT INTO core_billing.legacy_escrow_operations VALUES(op_key,migration,project_id,challenge_id,kind,p_actor_user_id,request,result);
    RETURN result;
EXCEPTION WHEN unique_violation THEN
    RAISE EXCEPTION 'retry complete escrow finalize transaction with original key' USING ERRCODE='40001';
END $$;
REVOKE ALL ON core_billing.legacy_escrow_batch_runs,core_billing.legacy_escrow_projects,core_billing.legacy_escrow_claims,
    core_billing.legacy_escrow_disputes,core_billing.legacy_escrow_state,core_billing.legacy_escrow_operations FROM PUBLIC;
REVOKE ALL ON FUNCTION core_billing.import_legacy_escrow_batch(JSONB),core_billing.legacy_escrow_finalize(JSONB,BIGINT,BIGINT),
    core_billing.legacy_escrow_keys(JSONB,TEXT[]),core_billing.legacy_escrow_uint(JSONB,BOOLEAN),core_billing.legacy_escrow_id(JSONB),core_billing.legacy_escrow_hash(JSONB),
    core_billing.legacy_escrow_guard_state(),core_billing.legacy_escrow_check_remaining() FROM PUBLIC;
