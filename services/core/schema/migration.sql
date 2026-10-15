-- Offline local import receipts. This schema is not writable by the app runtime.
CREATE SCHEMA core_migration;
CREATE TABLE core_migration.import_runs (
    migration_id TEXT PRIMARY KEY CHECK (length(migration_id) BETWEEN 1 AND 128),
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
-- Offline receipts are append-only, including the complete financial batch
-- seal. The assembled installer installs ledger.sql before this component.
CREATE TRIGGER import_runs_immutable BEFORE UPDATE OR DELETE ON core_migration.import_runs
    FOR EACH ROW EXECUTE FUNCTION core_billing.ledger_immutable();
CREATE TRIGGER import_runs_no_truncate BEFORE TRUNCATE ON core_migration.import_runs
    FOR EACH STATEMENT EXECUTE FUNCTION core_billing.ledger_immutable();

REVOKE ALL ON SCHEMA core_migration FROM PUBLIC;
REVOKE ALL ON TABLE core_migration.import_runs FROM PUBLIC;
