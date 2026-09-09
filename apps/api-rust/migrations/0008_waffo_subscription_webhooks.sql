-- Permanent evidence for the payment/lifecycle split introduced on 2026-09-06.
-- This expansion does not infer historical periods or mutate existing payments.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.waffo_pancake_subscription_payments (
    id BIGSERIAL PRIMARY KEY,
    subscription_order_id BIGINT NOT NULL,
    event_id VARCHAR(255) NOT NULL,
    provider_order_id VARCHAR(255) NOT NULL,
    payment_id VARCHAR(255) NOT NULL,
    currency VARCHAR(8) NOT NULL,
    amount_micros BIGINT NOT NULL,
    payment_date BIGINT NOT NULL,
    period_start BIGINT NOT NULL DEFAULT 0,
    period_end BIGINT NOT NULL DEFAULT 0,
    payload TEXT NOT NULL,
    received_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_payments_subscription_order_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_payments(subscription_order_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_payments_event_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_payments(event_id);
CREATE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_payments_provider_order_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_payments(provider_order_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_payments_payment_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_payments(payment_id);

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.waffo_pancake_subscription_periods (
    id BIGSERIAL PRIMARY KEY,
    subscription_order_id BIGINT NOT NULL,
    event_id VARCHAR(255) NOT NULL,
    event_type VARCHAR(64) NOT NULL,
    provider_order_id VARCHAR(255) NOT NULL,
    billing_period VARCHAR(32) NOT NULL,
    currency VARCHAR(8) NOT NULL,
    amount_micros BIGINT NOT NULL,
    period_start BIGINT NOT NULL,
    period_end BIGINT NOT NULL,
    payload TEXT NOT NULL,
    received_at BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_periods_subscription_order_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_periods(subscription_order_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_periods_event_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_periods(event_id);
CREATE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_periods_provider_order_id
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_periods(provider_order_id);
CREATE INDEX IF NOT EXISTS idx_waffo_pancake_subscription_periods_period_end
    ON __LMM_APP_SCHEMA__.waffo_pancake_subscription_periods(period_end);
