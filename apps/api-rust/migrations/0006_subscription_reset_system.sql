-- Root-authorized subscription reset previews, idempotent operations, audit events, and vouchers.
-- This migration is additive and deliberately avoids deletion-blocking referential constraints,
-- preserving the Go service's archival and physical-deletion history rules.
ALTER TABLE __LMM_APP_SCHEMA__.subscription_plans
    ADD COLUMN IF NOT EXISTS archived_at BIGINT NOT NULL DEFAULT 0;

CREATE INDEX IF NOT EXISTS idx_subscription_plans_archived_at
    ON __LMM_APP_SCHEMA__.subscription_plans (archived_at);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_reset_vouchers (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    operation_id VARCHAR(64) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'available',
    expires_at BIGINT NOT NULL,
    redeemed_at BIGINT NOT NULL DEFAULT 0,
    created_by BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_voucher_operation
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (user_id, plan_id, operation_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_vouchers_user_id
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (user_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_vouchers_plan_id
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (plan_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_vouchers_status
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (status);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_vouchers_expires_at
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (expires_at);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_vouchers_created_by
    ON __LMM_APP_SCHEMA__.subscription_reset_vouchers (created_by);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_reset_events (
    id BIGSERIAL PRIMARY KEY,
    operation_id VARCHAR(64) NOT NULL,
    user_id BIGINT NOT NULL,
    plan_id BIGINT NOT NULL,
    mode VARCHAR(24) NOT NULL,
    actor_user_id BIGINT NOT NULL,
    voucher_id BIGINT NOT NULL DEFAULT 0,
    reset_count BIGINT NOT NULL DEFAULT 0,
    restored_quota BIGINT NOT NULL DEFAULT 0,
    voucher_expiry BIGINT NOT NULL DEFAULT 0,
    created_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_event_operation
    ON __LMM_APP_SCHEMA__.subscription_reset_events (operation_id, user_id, plan_id, mode);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_events_user_id
    ON __LMM_APP_SCHEMA__.subscription_reset_events (user_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_events_plan_id
    ON __LMM_APP_SCHEMA__.subscription_reset_events (plan_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_events_actor_user_id
    ON __LMM_APP_SCHEMA__.subscription_reset_events (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_events_created_at
    ON __LMM_APP_SCHEMA__.subscription_reset_events (created_at);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_reset_previews (
    token VARCHAR(64) PRIMARY KEY,
    actor_user_id BIGINT NOT NULL,
    mode VARCHAR(16) NOT NULL,
    targets_json TEXT NOT NULL,
    payload_hash VARCHAR(64) NOT NULL,
    target_count BIGINT NOT NULL,
    active_subscriptions BIGINT NOT NULL,
    quota_to_restore BIGINT NOT NULL,
    voucher_expires_at BIGINT NOT NULL DEFAULT 0,
    expires_at BIGINT NOT NULL,
    consumed_at BIGINT NOT NULL DEFAULT 0,
    operation_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at BIGINT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_previews_actor_user_id
    ON __LMM_APP_SCHEMA__.subscription_reset_previews (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_previews_expires_at
    ON __LMM_APP_SCHEMA__.subscription_reset_previews (expires_at);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_reset_operations (
    operation_id VARCHAR(64) PRIMARY KEY,
    preview_token VARCHAR(64) NOT NULL,
    actor_user_id BIGINT NOT NULL,
    mode VARCHAR(16) NOT NULL,
    payload_hash VARCHAR(64) NOT NULL,
    result_json TEXT NOT NULL,
    created_at BIGINT NOT NULL,
    completed_at BIGINT NOT NULL
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_operations_preview_token
    ON __LMM_APP_SCHEMA__.subscription_reset_operations (preview_token);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_operations_actor_user_id
    ON __LMM_APP_SCHEMA__.subscription_reset_operations (actor_user_id);
CREATE INDEX IF NOT EXISTS idx_subscription_reset_operations_completed_at
    ON __LMM_APP_SCHEMA__.subscription_reset_operations (completed_at);
