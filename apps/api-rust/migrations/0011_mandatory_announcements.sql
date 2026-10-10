-- Additive current-Go acknowledgement evidence. Content remains in options.
-- Keep historical revisions and account separation; retries use the same key.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.announcement_reads (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    announcement_id BIGINT NOT NULL,
    revision VARCHAR(64) NOT NULL,
    read_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_announcement_read
    ON __LMM_APP_SCHEMA__.announcement_reads (user_id, announcement_id, revision);
