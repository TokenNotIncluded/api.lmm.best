-- Current Go payment order evidence and coupon/referral transaction state.
-- This never manufactures immutable evidence for historical orders. Their
-- zero/empty snapshots deliberately require reconciliation before settlement.
ALTER TABLE __LMM_APP_SCHEMA__.top_ups
    ADD COLUMN IF NOT EXISTS referral_excluded BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN IF NOT EXISTS platform_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS credited_quota BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS expected_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS settled_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS settlement_currency VARCHAR(16) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS refunded_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refunded_quota BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS discount_code_id BIGINT,
    ADD COLUMN IF NOT EXISTS discount_percent BIGINT,
    ADD COLUMN IF NOT EXISTS provider_product_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_store_id VARCHAR(255) NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS provider_event_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_transaction_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS payment_checked_at BIGINT NOT NULL DEFAULT 0;

CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_provider_event
    ON __LMM_APP_SCHEMA__.top_ups(payment_provider,provider_event_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_topup_provider_transaction
    ON __LMM_APP_SCHEMA__.top_ups(payment_provider,provider_transaction_id);
CREATE INDEX IF NOT EXISTS idx_top_ups_discount_code_id
    ON __LMM_APP_SCHEMA__.top_ups(discount_code_id);

ALTER TABLE __LMM_APP_SCHEMA__.users
    ADD COLUMN IF NOT EXISTS referral_first_top_up_id BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS payment_restriction_flags BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS linux_do_gamification_score DOUBLE PRECISION NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS linux_do_score_updated_at BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.discount_codes (
    id BIGSERIAL PRIMARY KEY,
    code VARCHAR(64),
    name VARCHAR(120),
    owner_user_id BIGINT,
    discount_percent BIGINT,
    min_amount BIGINT NOT NULL DEFAULT 0,
    status BIGINT NOT NULL DEFAULT 1,
    used_count BIGINT NOT NULL DEFAULT 0,
    max_uses BIGINT NOT NULL DEFAULT 0,
    created_by BIGINT,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL,
    starts_time BIGINT NOT NULL DEFAULT 0,
    expired_time BIGINT NOT NULL DEFAULT 0,
    deleted_at TIMESTAMPTZ
);
ALTER TABLE __LMM_APP_SCHEMA__.discount_codes
    ADD COLUMN IF NOT EXISTS owner_user_id BIGINT,
    ADD COLUMN IF NOT EXISTS max_uses BIGINT NOT NULL DEFAULT 0;
CREATE UNIQUE INDEX IF NOT EXISTS idx_discount_codes_code ON __LMM_APP_SCHEMA__.discount_codes(code);
CREATE INDEX IF NOT EXISTS idx_discount_codes_name ON __LMM_APP_SCHEMA__.discount_codes(name);
CREATE INDEX IF NOT EXISTS idx_discount_codes_owner_user_id ON __LMM_APP_SCHEMA__.discount_codes(owner_user_id);
CREATE INDEX IF NOT EXISTS idx_discount_codes_status ON __LMM_APP_SCHEMA__.discount_codes(status);
CREATE INDEX IF NOT EXISTS idx_discount_codes_created_by ON __LMM_APP_SCHEMA__.discount_codes(created_by);
CREATE INDEX IF NOT EXISTS idx_discount_codes_created_time ON __LMM_APP_SCHEMA__.discount_codes(created_time);
CREATE INDEX IF NOT EXISTS idx_discount_codes_deleted_at ON __LMM_APP_SCHEMA__.discount_codes(deleted_at);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.discount_code_reservations (
    id BIGSERIAL PRIMARY KEY,
    discount_code_id BIGINT NOT NULL,
    top_up_trade_no VARCHAR(255) NOT NULL,
    user_id BIGINT NOT NULL,
    status VARCHAR(16) NOT NULL,
    expires_time BIGINT NOT NULL,
    created_time BIGINT NOT NULL,
    updated_time BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_discount_code_reservations_top_up_trade_no ON __LMM_APP_SCHEMA__.discount_code_reservations(top_up_trade_no);
CREATE INDEX IF NOT EXISTS idx_discount_code_reservations_discount_code_id ON __LMM_APP_SCHEMA__.discount_code_reservations(discount_code_id);
CREATE INDEX IF NOT EXISTS idx_discount_code_reservations_user_id ON __LMM_APP_SCHEMA__.discount_code_reservations(user_id);
CREATE INDEX IF NOT EXISTS idx_discount_code_reservations_status ON __LMM_APP_SCHEMA__.discount_code_reservations(status);
CREATE INDEX IF NOT EXISTS idx_discount_code_reservations_expires_time ON __LMM_APP_SCHEMA__.discount_code_reservations(expires_time);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.referral_rewards (
    id BIGSERIAL PRIMARY KEY,
    invitee_id BIGINT NOT NULL,
    inviter_id BIGINT NOT NULL,
    top_up_id BIGINT NOT NULL,
    quota BIGINT NOT NULL,
    status VARCHAR(24) NOT NULL,
    revoked_quota BIGINT NOT NULL DEFAULT 0,
    penalty_quota BIGINT NOT NULL DEFAULT 0,
    penalty_percent BIGINT NOT NULL DEFAULT 0,
    max_penalty_quota BIGINT NOT NULL DEFAULT 0,
    revision BIGINT NOT NULL DEFAULT 0,
    reason VARCHAR(32) NOT NULL DEFAULT '',
    created_at BIGINT,
    updated_at BIGINT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_referral_rewards_invitee_id ON __LMM_APP_SCHEMA__.referral_rewards(invitee_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_referral_rewards_top_up_id ON __LMM_APP_SCHEMA__.referral_rewards(top_up_id);
CREATE INDEX IF NOT EXISTS idx_referral_rewards_inviter_id ON __LMM_APP_SCHEMA__.referral_rewards(inviter_id);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.referral_ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    reward_id BIGINT NOT NULL,
    user_id BIGINT NOT NULL,
    event_key VARCHAR(160) NOT NULL,
    kind VARCHAR(32) NOT NULL,
    quota BIGINT NOT NULL,
    reason VARCHAR(32) NOT NULL,
    created_at BIGINT
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_referral_ledger_entries_event_key ON __LMM_APP_SCHEMA__.referral_ledger_entries(event_key);
CREATE INDEX IF NOT EXISTS idx_referral_ledger_entries_reward_id ON __LMM_APP_SCHEMA__.referral_ledger_entries(reward_id);
CREATE INDEX IF NOT EXISTS idx_referral_ledger_entries_user_id ON __LMM_APP_SCHEMA__.referral_ledger_entries(user_id);
