-- Persist whether a Waffo Pancake subscription-plan product is a one-time
-- purchase or a provider-managed recurring subscription.
DO $$
BEGIN
    IF to_regclass('public.subscription_plans') IS NOT NULL THEN
        ALTER TABLE subscription_plans
            ADD COLUMN IF NOT EXISTS waffo_pancake_product_type VARCHAR(16)
                NOT NULL DEFAULT 'subscription';
    END IF;
END
$$;
