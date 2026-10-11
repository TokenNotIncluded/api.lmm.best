-- Offline local import receipts. This schema is not writable by the app runtime.
CREATE SCHEMA core_migration;
CREATE TABLE core_migration.import_runs (
    migration_id TEXT PRIMARY KEY CHECK (length(migration_id) BETWEEN 1 AND 128),
    source_archive_sha256 TEXT NOT NULL CHECK (source_archive_sha256 ~ '^[0-9a-f]{64}$'),
    mapping_sha256 TEXT NOT NULL CHECK (mapping_sha256 ~ '^[0-9a-f]{64}$'),
    manifest JSONB NOT NULL CHECK (jsonb_typeof(manifest) = 'object'),
    completed_at TIMESTAMPTZ NOT NULL DEFAULT clock_timestamp()
);
REVOKE ALL ON SCHEMA core_migration FROM PUBLIC;
REVOKE ALL ON TABLE core_migration.import_runs FROM PUBLIC;
