-- PostgreSQL only. Apply to a development database first.
-- Run once before starting Go API nodes with the dual-product change.
-- NULL preserves the old single-product binding; [] means explicitly disabled.
BEGIN;
ALTER TABLE subscription_plans
    ADD COLUMN IF NOT EXISTS waffo_pancake_products text;
COMMIT;
