-- Current Go append-only finance ledger. Provider refunds bind a unique key
-- to immutable order evidence in the same transaction as wallet reversal.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.finance_ledger_entries (
    id BIGSERIAL PRIMARY KEY,
    entry_type VARCHAR(32) NOT NULL,
    category VARCHAR(64) NOT NULL DEFAULT '',
    amount_micros BIGINT NOT NULL,
    currency VARCHAR(8) NOT NULL DEFAULT 'USD',
    direction SMALLINT NOT NULL,
    payment_method VARCHAR(64) NOT NULL DEFAULT '',
    payment_provider VARCHAR(64) NOT NULL DEFAULT '',
    user_id BIGINT,
    source_type VARCHAR(32) NOT NULL,
    source_id VARCHAR(128) NOT NULL DEFAULT '',
    token_units BIGINT NOT NULL DEFAULT 0,
    note VARCHAR(500) NOT NULL DEFAULT '',
    occurred_at BIGINT NOT NULL,
    created_at BIGINT NOT NULL,
    created_by BIGINT NOT NULL,
    reversal_of_id BIGINT,
    idempotency_key VARCHAR(180)
);
CREATE UNIQUE INDEX IF NOT EXISTS idx_finance_ledger_entries_idempotency_key ON __LMM_APP_SCHEMA__.finance_ledger_entries(idempotency_key);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_entry_type ON __LMM_APP_SCHEMA__.finance_ledger_entries(entry_type);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_category ON __LMM_APP_SCHEMA__.finance_ledger_entries(category);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_payment_method ON __LMM_APP_SCHEMA__.finance_ledger_entries(payment_method);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_payment_provider ON __LMM_APP_SCHEMA__.finance_ledger_entries(payment_provider);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_user_id ON __LMM_APP_SCHEMA__.finance_ledger_entries(user_id);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_source_type ON __LMM_APP_SCHEMA__.finance_ledger_entries(source_type);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_source_id ON __LMM_APP_SCHEMA__.finance_ledger_entries(source_id);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_occurred_at ON __LMM_APP_SCHEMA__.finance_ledger_entries(occurred_at);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_created_at ON __LMM_APP_SCHEMA__.finance_ledger_entries(created_at);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_created_by ON __LMM_APP_SCHEMA__.finance_ledger_entries(created_by);
CREATE INDEX IF NOT EXISTS idx_finance_ledger_entries_reversal_of_id ON __LMM_APP_SCHEMA__.finance_ledger_entries(reversal_of_id);

-- Ensure payment evidence exists in the selected application schema. The
-- historical 0004 migration targeted public explicitly and cannot establish
-- these contracts for an adopted non-public schema.
ALTER TABLE __LMM_APP_SCHEMA__.subscription_orders
    ADD COLUMN IF NOT EXISTS plan_currency VARCHAR(8),
    ADD COLUMN IF NOT EXISTS plan_snapshot TEXT,
    ADD COLUMN IF NOT EXISTS user_subscription_id BIGINT DEFAULT 0,
    ADD COLUMN IF NOT EXISTS expected_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS settlement_currency VARCHAR(8),
    ADD COLUMN IF NOT EXISTS provider_product_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_store_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_subscription_id VARCHAR(255),
    ADD COLUMN IF NOT EXISTS provider_subscription_state VARCHAR(32),
    ADD COLUMN IF NOT EXISTS provider_event_time_millis BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS current_period_start BIGINT,
    ADD COLUMN IF NOT EXISTS current_period_end BIGINT,
    ADD COLUMN IF NOT EXISTS refunded_amount_micros BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS refunded_quota BIGINT NOT NULL DEFAULT 0;

CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_payment_events (
    id BIGSERIAL PRIMARY KEY,
    subscription_order_id BIGINT NOT NULL,
    payment_provider VARCHAR(64) NOT NULL,
    provider_event_id VARCHAR(255) NOT NULL,
    provider_transaction_id VARCHAR(255) NOT NULL,
    settlement_currency VARCHAR(8) NOT NULL,
    settlement_amount_micros BIGINT NOT NULL,
    period_start BIGINT,
    period_end BIGINT,
    created_time BIGINT
);
-- Modern provider events may not carry a concrete period. NULL preserves the
-- intended uniqueness semantics instead of collapsing all unknown periods to 0.
ALTER TABLE __LMM_APP_SCHEMA__.subscription_payment_events
    ALTER COLUMN period_start DROP NOT NULL,
    ALTER COLUMN period_start DROP DEFAULT,
    ALTER COLUMN period_end DROP NOT NULL,
    ALTER COLUMN period_end DROP DEFAULT;
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_payment_events_provider_event_id ON __LMM_APP_SCHEMA__.subscription_payment_events(provider_event_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_provider_transaction ON __LMM_APP_SCHEMA__.subscription_payment_events(payment_provider,provider_transaction_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_order_period ON __LMM_APP_SCHEMA__.subscription_payment_events(subscription_order_id,period_end);
CREATE INDEX IF NOT EXISTS idx_subscription_payment_events_subscription_order_id ON __LMM_APP_SCHEMA__.subscription_payment_events(subscription_order_id);
CREATE INDEX IF NOT EXISTS idx_subscription_payment_events_created_time ON __LMM_APP_SCHEMA__.subscription_payment_events(created_time);
