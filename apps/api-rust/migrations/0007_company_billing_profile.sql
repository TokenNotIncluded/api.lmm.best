-- One invoice identity per user. PostgreSQL owns deletion with the user lifecycle.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.company_billing_profiles (
    user_id BIGINT PRIMARY KEY,
    country CHAR(2) NOT NULL,
    is_business BOOLEAN NOT NULL,
    postcode VARCHAR(32) NOT NULL DEFAULT '',
    state VARCHAR(128) NOT NULL DEFAULT '',
    business_name VARCHAR(255) NOT NULL DEFAULT '',
    tax_id VARCHAR(64) NOT NULL DEFAULT '',
    use_for_invoices BOOLEAN NOT NULL DEFAULT FALSE,
    created_at BIGINT NOT NULL,
    updated_at BIGINT NOT NULL,
    CONSTRAINT company_billing_profiles_country_format
        CHECK (char_length(country) = 2 AND country = upper(country)),
    CONSTRAINT company_billing_profiles_user_id_fkey
        FOREIGN KEY (user_id) REFERENCES __LMM_APP_SCHEMA__.users(id) ON DELETE CASCADE
        DEFERRABLE INITIALLY DEFERRED
);

-- Post-session provider validation failures must never remain pending. These
-- columns persist only allowlisted reason codes; invoice identity values are
-- deliberately excluded.
ALTER TABLE __LMM_APP_SCHEMA__.top_ups
    ADD COLUMN IF NOT EXISTS failure_reason_code VARCHAR(64) NOT NULL DEFAULT '';

ALTER TABLE __LMM_APP_SCHEMA__.subscription_orders
    ADD COLUMN IF NOT EXISTS failure_reason_code VARCHAR(64) NOT NULL DEFAULT '';
