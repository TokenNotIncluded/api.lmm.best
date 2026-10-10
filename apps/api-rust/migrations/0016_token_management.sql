-- Current Go token-management flags. Preserve already-managed credentials.
ALTER TABLE __LMM_APP_SCHEMA__.tokens
    ADD COLUMN IF NOT EXISTS oauth_managed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS one_time_reveal BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS creation_source VARCHAR(32) NOT NULL DEFAULT 'manual';
CREATE INDEX IF NOT EXISTS idx_tokens_oauth_managed
    ON __LMM_APP_SCHEMA__.tokens (oauth_managed);
CREATE INDEX IF NOT EXISTS idx_tokens_creation_source
    ON __LMM_APP_SCHEMA__.tokens (creation_source);

-- Go's startup backfill uses its normal (non-soft-deleted) model scope.
UPDATE __LMM_APP_SCHEMA__.tokens SET creation_source='manual'
    WHERE deleted_at IS NULL AND (creation_source IS NULL OR creation_source='');
UPDATE __LMM_APP_SCHEMA__.tokens SET creation_source='system'
    WHERE deleted_at IS NULL AND oauth_managed=FALSE AND creation_source='manual'
      AND name LIKE '%的初始令牌';
UPDATE __LMM_APP_SCHEMA__.tokens SET creation_source='drawing_mcp'
    WHERE deleted_at IS NULL AND oauth_managed=FALSE AND creation_source='manual'
      AND unlimited_quota=TRUE AND expired_time=-1
      AND name='drawing-image-2' AND "group"='image-2';
