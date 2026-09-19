-- Forward-only additive migration for Rust/Go account-wallet balance parity.
-- Existing keys remain denied until an owner explicitly enables the flag.
ALTER TABLE __LMM_APP_SCHEMA__.tokens
    ADD COLUMN IF NOT EXISTS account_balance_read BOOLEAN NOT NULL DEFAULT FALSE;
