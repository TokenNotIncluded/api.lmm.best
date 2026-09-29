-- Current Go AI directory ad contract. Request IDs are global immutable replay keys.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.ai_directory_ads (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL,
    name VARCHAR(80) NOT NULL,
    url VARCHAR(2048) NOT NULL,
    summary VARCHAR(180) NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    bid_cents BIGINT NOT NULL,
    charged_quota BIGINT NOT NULL,
    request_id VARCHAR(80) NOT NULL,
    status VARCHAR(16) NOT NULL,
    paid_at BIGINT NOT NULL,
    expires_at BIGINT NOT NULL,
    hidden_at BIGINT NOT NULL DEFAULT 0,
    refunded_at BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS idx_ai_directory_ads_owner_user_id ON __LMM_APP_SCHEMA__.ai_directory_ads(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_ai_directory_ads_bid_cents ON __LMM_APP_SCHEMA__.ai_directory_ads(bid_cents);
CREATE UNIQUE INDEX IF NOT EXISTS idx_ai_directory_ads_request_id ON __LMM_APP_SCHEMA__.ai_directory_ads(request_id);
CREATE INDEX IF NOT EXISTS idx_ai_directory_ads_status ON __LMM_APP_SCHEMA__.ai_directory_ads(status);
CREATE INDEX IF NOT EXISTS idx_ai_directory_ads_paid_at ON __LMM_APP_SCHEMA__.ai_directory_ads(paid_at);
CREATE INDEX IF NOT EXISTS idx_ai_directory_ads_expires_at ON __LMM_APP_SCHEMA__.ai_directory_ads(expires_at);
