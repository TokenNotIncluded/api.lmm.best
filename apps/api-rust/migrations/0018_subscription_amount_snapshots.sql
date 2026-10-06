-- Optional subscription amount snapshots preserve the legacy NULL semantics.
-- An explicit zero is a finite quota; do not backfill historical rows or add a
-- zero default that would turn legacy unlimited subscriptions into finite ones.
ALTER TABLE __LMM_APP_SCHEMA__.user_subscriptions
    ADD COLUMN IF NOT EXISTS reset_amount BIGINT,
    ADD COLUMN IF NOT EXISTS renewal_amount BIGINT;
