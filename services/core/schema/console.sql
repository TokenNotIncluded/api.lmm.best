-- Fresh-install console compatibility facts, not a runtime migration.
-- Announcement semantics originate in main 7be6ee81 model/mandatory_announcement.go.
CREATE SCHEMA core_console;
CREATE TABLE core_console.options (
    key TEXT PRIMARY KEY CHECK (length(key) BETWEEN 1 AND 128),
    value TEXT NOT NULL,
    revision BIGINT NOT NULL DEFAULT 1 CHECK (revision > 0),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
);
CREATE TABLE core_console.announcement_reads (
    user_id BIGINT NOT NULL REFERENCES core_identity.users(id),
    announcement_id BIGINT NOT NULL CHECK (announcement_id > 0),
    revision TEXT NOT NULL CHECK (revision ~ '^[a-f0-9]{64}$'),
    read_at BIGINT NOT NULL DEFAULT floor(extract(epoch FROM statement_timestamp()))::BIGINT
        CHECK (read_at > 0),
    PRIMARY KEY (user_id, announcement_id, revision)
);
CREATE TABLE core_console.initialization (
    singleton BOOLEAN PRIMARY KEY DEFAULT TRUE CHECK (singleton),
    version TEXT NOT NULL CHECK (length(version) BETWEEN 1 AND 128),
    initialized_at TIMESTAMPTZ NOT NULL DEFAULT statement_timestamp()
);
REVOKE ALL ON SCHEMA core_console FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA core_console FROM PUBLIC;
REVOKE ALL ON ALL SEQUENCES IN SCHEMA core_console FROM PUBLIC;
