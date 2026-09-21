-- Immutable subscription settlement evidence and recurring-payment ledger.
-- The legacy subscription tables predate the Rust migration set, so guard the
-- additive migration for fresh databases that do not contain them yet.
DO $$
BEGIN
    IF to_regclass('public.subscription_orders') IS NOT NULL THEN
        ALTER TABLE subscription_orders
            ADD COLUMN IF NOT EXISTS plan_snapshot TEXT NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS plan_currency VARCHAR(16) NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS expected_amount_micros BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS settlement_currency VARCHAR(16) NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS provider_product_id VARCHAR(255) NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS provider_subscription_id VARCHAR(255) NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS provider_subscription_state VARCHAR(32) NOT NULL DEFAULT '',
            ADD COLUMN IF NOT EXISTS current_period_start BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS current_period_end BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS user_subscription_id BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS refunded_amount_micros BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS canceled_at BIGINT NOT NULL DEFAULT 0,
            ADD COLUMN IF NOT EXISTS updated_at BIGINT NOT NULL DEFAULT 0;

        CREATE INDEX IF NOT EXISTS idx_subscription_orders_provider_subscription
            ON subscription_orders (provider_subscription_id);
        CREATE INDEX IF NOT EXISTS idx_subscription_orders_provider_state
            ON subscription_orders (provider_subscription_state);

        CREATE TABLE IF NOT EXISTS subscription_payment_events (
            id BIGSERIAL PRIMARY KEY,
            subscription_order_id BIGINT NOT NULL,
            payment_provider VARCHAR(64) NOT NULL,
            provider_event_id VARCHAR(255) NOT NULL,
            provider_transaction_id VARCHAR(255) NOT NULL,
            settlement_currency VARCHAR(16) NOT NULL,
            settlement_amount_micros BIGINT NOT NULL DEFAULT 0,
            period_start BIGINT NOT NULL DEFAULT 0,
            period_end BIGINT NOT NULL DEFAULT 0,
            provider_payload TEXT NOT NULL DEFAULT '',
            created_time BIGINT NOT NULL DEFAULT 0
        );
        CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_payment_event
            ON subscription_payment_events (provider_event_id);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_provider_transaction
            ON subscription_payment_events (payment_provider, provider_transaction_id);
        CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_order_period
            ON subscription_payment_events (subscription_order_id, period_end);
        CREATE INDEX IF NOT EXISTS idx_subscription_payment_order
            ON subscription_payment_events (subscription_order_id);
        CREATE INDEX IF NOT EXISTS idx_subscription_payment_created
            ON subscription_payment_events (created_time);
    END IF;
END
$$;
