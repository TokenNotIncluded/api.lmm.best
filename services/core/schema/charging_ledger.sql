-- Installed with the complete fresh core schema. The charging transaction owns
-- these links; no independent transaction or Go database can update a hold.
CREATE TABLE core_charging.ledger_commands (
    operation_id TEXT PRIMARY KEY CHECK (length(operation_id) = 64),
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    command JSONB NOT NULL CHECK (jsonb_typeof(command) = 'object'),
    created_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
CREATE INDEX ledger_commands_request_operation
    ON core_charging.ledger_commands USING btree (request_id, operation_id);
CREATE TABLE core_charging.ledger_holds (
    request_id TEXT NOT NULL REFERENCES core_charging.requests(id),
    position INTEGER NOT NULL CHECK (position >= 0),
    reservation_journal_id BIGINT NOT NULL UNIQUE REFERENCES core_billing.ledger_journals(id),
    reserved BIGINT NOT NULL CHECK (reserved > 0),
    capture_journal_id BIGINT UNIQUE REFERENCES core_billing.ledger_journals(id),
    captured BIGINT NOT NULL DEFAULT 0 CHECK (captured BETWEEN 0 AND reserved),
    refunded BIGINT NOT NULL DEFAULT 0 CHECK (refunded BETWEEN 0 AND captured),
    closed BOOLEAN NOT NULL DEFAULT FALSE,
    PRIMARY KEY (request_id, position),
    CHECK (closed OR (capture_journal_id IS NULL AND captured = 0 AND refunded = 0)),
    CHECK (captured = 0 OR capture_journal_id IS NOT NULL)
);

-- SSI budget/entitlement safety requires every writer, including provisioning
-- and imports, to use SERIALIZABLE. Fail closed at the database boundary rather
-- than relying on each new caller remembering the application convention.
-- Identity and standalone controlled ledger operations may still use READ COMMITTED.
CREATE FUNCTION core_charging.require_serializable() RETURNS TRIGGER
LANGUAGE plpgsql SET search_path = pg_catalog AS $$
BEGIN
    IF current_setting('transaction_isolation') <> 'serializable' THEN
        RAISE EXCEPTION 'charging writes require serializable' USING ERRCODE = '25001';
    END IF;
    RETURN NULL;
END $$;
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.requests FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.budgets FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.subscriptions FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.budget_opening_consumption FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.subscription_opening_consumption FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.external_consumption FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.budget_checks FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.usage_events FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.ledger_commands FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
CREATE TRIGGER charging_serializable BEFORE INSERT OR UPDATE OR DELETE OR TRUNCATE
    ON core_charging.ledger_holds FOR EACH STATEMENT EXECUTE FUNCTION core_charging.require_serializable();
REVOKE ALL ON FUNCTION core_charging.require_serializable() FROM PUBLIC;
