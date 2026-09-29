-- Go-managed subscription billing state plus durable Rust relay settlement
-- intent. No provider credentials, request bodies or response text are stored.
ALTER TABLE __LMM_APP_SCHEMA__.user_subscriptions
    ADD COLUMN IF NOT EXISTS quota_version BIGINT NOT NULL DEFAULT 0;

ALTER TABLE __LMM_APP_SCHEMA__.subscription_pre_consume_records
    ADD COLUMN IF NOT EXISTS billing_managed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS token_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS token_consumed BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wallet_overflow BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS actual_quota BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS wallet_consumed BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS reserved_version BIGINT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_pre_consume_records_request_id
    ON __LMM_APP_SCHEMA__.subscription_pre_consume_records (request_id);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.relay_settlement_records (
    reservation_id TEXT PRIMARY KEY,
    request_id TEXT NOT NULL,
    user_id BIGINT NOT NULL,
    token_id BIGINT NOT NULL,
    channel_id BIGINT NOT NULL,
    model_name TEXT NOT NULL,
    using_group TEXT NOT NULL,
    is_stream BOOLEAN NOT NULL,
    funding_source TEXT NOT NULL CHECK (funding_source IN ('wallet','subscription','free')),
    expected_quota BIGINT NOT NULL CHECK (expected_quota >= 0),
    wallet_reserved BIGINT NOT NULL DEFAULT 0 CHECK (wallet_reserved >= 0),
    subscription_id BIGINT NOT NULL DEFAULT 0,
    subscription_reserved BIGINT NOT NULL DEFAULT 0 CHECK (subscription_reserved >= 0),
    reserved_version BIGINT NOT NULL DEFAULT 0,
    token_reserved BIGINT NOT NULL DEFAULT 0 CHECK (token_reserved >= 0),
    wallet_overflow BOOLEAN NOT NULL DEFAULT FALSE,
    wallet_settled BIGINT NOT NULL DEFAULT 0 CHECK (wallet_settled >= 0),
    subscription_settled BIGINT NOT NULL DEFAULT 0 CHECK (subscription_settled >= 0),
    actual_quota BIGINT CHECK (actual_quota >= 0),
    price_snapshot JSONB NOT NULL CHECK (jsonb_typeof(price_snapshot)='object'),
    usage_snapshot JSONB CHECK (usage_snapshot IS NULL OR jsonb_typeof(usage_snapshot)='object'),
    log_metadata JSONB NOT NULL DEFAULT '{}'::JSONB CHECK (jsonb_typeof(log_metadata)='object'),
    status TEXT NOT NULL CHECK (status IN ('reserved','settling','settled','refunded')),
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CHECK ((status='reserved' AND actual_quota IS NULL) OR
           (status IN ('settling','settled','refunded') AND actual_quota IS NOT NULL)),
    CHECK ((funding_source='subscription' AND subscription_id>0) OR
           (funding_source IN ('wallet','free') AND subscription_id=0))
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_relay_settlement_records_active_request
    ON __LMM_APP_SCHEMA__.relay_settlement_records (user_id,request_id)
    WHERE status <> 'refunded';
CREATE INDEX IF NOT EXISTS idx_relay_settlement_records_pending
    ON __LMM_APP_SCHEMA__.relay_settlement_records (updated_at,reservation_id)
    WHERE status='settling';
