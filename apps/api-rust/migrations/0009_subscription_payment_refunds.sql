-- Bind each provider refund and finance debit to its immutable payment evidence.
-- No historical refund ownership is inferred or backfilled by this expansion.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_payment_refunds (
    id BIGSERIAL PRIMARY KEY,
    subscription_order_id BIGINT NOT NULL,
    subscription_payment_event_id BIGINT NOT NULL,
    payment_provider VARCHAR(64) NOT NULL,
    provider_event_id VARCHAR(255) NOT NULL,
    currency VARCHAR(8) NOT NULL,
    amount_micros BIGINT NOT NULL,
    quota_revoked BIGINT NOT NULL,
    finance_ledger_entry_id BIGINT NOT NULL,
    created_time BIGINT NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_subscription_payment_refunds_subscription_order_id
    ON __LMM_APP_SCHEMA__.subscription_payment_refunds(subscription_order_id);
CREATE INDEX IF NOT EXISTS idx_subscription_payment_refunds_subscription_payment_event_id
    ON __LMM_APP_SCHEMA__.subscription_payment_refunds(subscription_payment_event_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_provider_refund
    ON __LMM_APP_SCHEMA__.subscription_payment_refunds(payment_provider, provider_event_id);
CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_payment_refunds_finance_ledger_entry_id
    ON __LMM_APP_SCHEMA__.subscription_payment_refunds(finance_ledger_entry_id);
