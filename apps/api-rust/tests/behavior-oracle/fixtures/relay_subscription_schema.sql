-- Minimal current-Go subscription tables used by real relay funding fixtures.
CREATE TABLE subscription_plans (
    id BIGINT PRIMARY KEY, title TEXT NOT NULL DEFAULT 'fixture plan', subtitle TEXT DEFAULT '',
    price_amount NUMERIC NOT NULL DEFAULT 1, currency TEXT DEFAULT 'CNY',
    duration_unit TEXT DEFAULT 'month', duration_value BIGINT DEFAULT 1, custom_seconds BIGINT DEFAULT 0,
    enabled BOOLEAN NOT NULL DEFAULT TRUE, sort_order BIGINT DEFAULT 0,
    allow_balance_pay BOOLEAN DEFAULT TRUE, allow_wallet_overflow BOOLEAN DEFAULT TRUE,
    stripe_price_id TEXT DEFAULT '', creem_product_id TEXT DEFAULT '', waffo_pancake_product_id TEXT DEFAULT '',
    waffo_pancake_product_type TEXT DEFAULT 'subscription', max_purchase_per_user BIGINT DEFAULT 0,
    total_amount BIGINT NOT NULL DEFAULT 100, upgrade_group TEXT DEFAULT '', downgrade_group TEXT DEFAULT '',
    quota_reset_period TEXT DEFAULT 'never', quota_reset_custom_seconds BIGINT DEFAULT 0,
    archived_at BIGINT DEFAULT 0, created_at BIGINT DEFAULT 0, updated_at BIGINT DEFAULT 0
);
CREATE TABLE user_subscriptions (
    id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, plan_id BIGINT NOT NULL,
    amount_total BIGINT NOT NULL DEFAULT 100, reset_amount BIGINT, renewal_amount BIGINT,
    amount_used BIGINT NOT NULL DEFAULT 0,
    start_time BIGINT NOT NULL DEFAULT 0, end_time BIGINT NOT NULL DEFAULT 0,
    status TEXT NOT NULL DEFAULT 'active', source TEXT NOT NULL DEFAULT 'order',
    last_reset_time BIGINT NOT NULL DEFAULT 0, next_reset_time BIGINT NOT NULL DEFAULT 0,
    upgrade_group TEXT NOT NULL DEFAULT '', prev_user_group TEXT NOT NULL DEFAULT '', downgrade_group TEXT NOT NULL DEFAULT '',
    allow_wallet_overflow BOOLEAN NOT NULL DEFAULT TRUE, created_at BIGINT NOT NULL DEFAULT 0, updated_at BIGINT NOT NULL DEFAULT 0
);
CREATE TABLE subscription_pre_consume_records (
    id BIGSERIAL PRIMARY KEY, request_id VARCHAR(64), user_id BIGINT, user_subscription_id BIGINT,
    pre_consumed BIGINT NOT NULL DEFAULT 0, status VARCHAR(32), created_at BIGINT, updated_at BIGINT
);
