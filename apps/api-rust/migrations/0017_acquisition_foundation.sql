-- Current Go acquisition foundation and all data removed by privacy withdrawal.
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_links (
    id VARCHAR(32),
    name VARCHAR(80) NOT NULL,
    source VARCHAR(80),
    medium VARCHAR(80),
    campaign VARCHAR(80),
    content VARCHAR(80),
    target VARCHAR(80),
    archived BOOLEAN,
    created_at BIGINT,
    deleted_at BIGINT NOT NULL DEFAULT 0,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_links_source ON __LMM_APP_SCHEMA__.acquisition_links(source);
CREATE INDEX IF NOT EXISTS idx_acquisition_links_campaign ON __LMM_APP_SCHEMA__.acquisition_links(campaign);
CREATE INDEX IF NOT EXISTS idx_acquisition_links_created_at ON __LMM_APP_SCHEMA__.acquisition_links(created_at);
CREATE INDEX IF NOT EXISTS idx_acquisition_links_deleted_at ON __LMM_APP_SCHEMA__.acquisition_links(deleted_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_visitors (
    id VARCHAR(64),
    user_id BIGINT,
    created_at BIGINT,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_visitors_user_id ON __LMM_APP_SCHEMA__.acquisition_visitors(user_id);
CREATE INDEX IF NOT EXISTS idx_acquisition_visitors_created_at ON __LMM_APP_SCHEMA__.acquisition_visitors(created_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_visits (
    consent_version BIGINT,
    id BIGSERIAL,
    visitor_id VARCHAR(64) NOT NULL,
    nonce VARCHAR(64) NOT NULL,
    link_id VARCHAR(32),
    source VARCHAR(80),
    medium VARCHAR(80),
    campaign VARCHAR(80),
    content VARCHAR(80),
    referrer_host VARCHAR(253),
    landing VARCHAR(80),
    evidence VARCHAR(32),
    created_at BIGINT,
    PRIMARY KEY (id)
);
CREATE UNIQUE INDEX IF NOT EXISTS acquisition_visit_nonce ON __LMM_APP_SCHEMA__.acquisition_visits(visitor_id,nonce);
CREATE INDEX IF NOT EXISTS idx_acquisition_visits_visitor_id ON __LMM_APP_SCHEMA__.acquisition_visits(visitor_id);
CREATE INDEX IF NOT EXISTS idx_acquisition_visits_link_id ON __LMM_APP_SCHEMA__.acquisition_visits(link_id);
CREATE INDEX IF NOT EXISTS idx_acquisition_visits_source ON __LMM_APP_SCHEMA__.acquisition_visits(source);
CREATE INDEX IF NOT EXISTS idx_acquisition_visits_campaign ON __LMM_APP_SCHEMA__.acquisition_visits(campaign);
CREATE INDEX IF NOT EXISTS idx_acquisition_visits_created_at ON __LMM_APP_SCHEMA__.acquisition_visits(created_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_accounts (
    consent_version BIGINT,
    first_source VARCHAR(80),
    first_evidence VARCHAR(32),
    first_observed_at BIGINT,
    registration_inferred BOOLEAN,
    user_id BIGSERIAL,
    first_visit_id BIGINT,
    registration_visit_id BIGINT,
    registration_source VARCHAR(80),
    registration_link_id VARCHAR(32),
    registration_content VARCHAR(80),
    registration_campaign VARCHAR(80),
    registration_evidence VARCHAR(32),
    registration_at BIGINT,
    attribution_rule VARCHAR(40),
    lookback_days BIGINT,
    self_reported VARCHAR(80),
    self_reported_at BIGINT,
    created_at BIGINT,
    PRIMARY KEY (user_id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_accounts_registration_source ON __LMM_APP_SCHEMA__.acquisition_accounts(registration_source);
CREATE INDEX IF NOT EXISTS idx_acquisition_accounts_registration_link_id ON __LMM_APP_SCHEMA__.acquisition_accounts(registration_link_id);
CREATE INDEX IF NOT EXISTS idx_acquisition_accounts_registration_at ON __LMM_APP_SCHEMA__.acquisition_accounts(registration_at);
CREATE INDEX IF NOT EXISTS idx_acquisition_accounts_created_at ON __LMM_APP_SCHEMA__.acquisition_accounts(created_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_consents (
    user_id BIGINT,
    allowed BOOLEAN,
    version BIGINT,
    updated_at BIGINT,
    PRIMARY KEY (user_id)
);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_self_reports (
    user_id BIGSERIAL,
    source VARCHAR(32) NOT NULL,
    detail VARCHAR(160),
    updated_at BIGINT,
    PRIMARY KEY (user_id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_self_reports_updated_at ON __LMM_APP_SCHEMA__.acquisition_self_reports(updated_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_configs (
    id BIGSERIAL,
    started_at BIGINT,
    lookback_days BIGINT,
    last_cleanup_at BIGINT,
    payment_snapshot_updated_at BIGINT,
    payment_snapshot_status VARCHAR(32),
    PRIMARY KEY (id)
);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_attribution_policies (
    id BIGSERIAL,
    effective_at BIGINT NOT NULL,
    lookback_days BIGINT NOT NULL,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_attribution_policies_effective_at ON __LMM_APP_SCHEMA__.acquisition_attribution_policies(effective_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_first_payments (
    user_id BIGINT,
    first_paid_at BIGINT,
    source VARCHAR(80),
    evidence VARCHAR(40),
    link_id VARCHAR(32),
    campaign VARCHAR(80),
    medium VARCHAR(80),
    content VARCHAR(80),
    referrer_host VARCHAR(253),
    visit_id BIGINT,
    observed_at BIGINT,
    inferred BOOLEAN,
    lookback_days BIGINT,
    policy_effective_at BIGINT,
    rule VARCHAR(48),
    history_conflict_at BIGINT,
    conflicting_paid_at BIGINT,
    snapshotted_at BIGINT,
    PRIMARY KEY (user_id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_first_payments_first_paid_at ON __LMM_APP_SCHEMA__.acquisition_first_payments(first_paid_at);
CREATE INDEX IF NOT EXISTS idx_acquisition_first_payments_snapshotted_at ON __LMM_APP_SCHEMA__.acquisition_first_payments(snapshotted_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_activities (
    user_id BIGINT,
    day BIGINT,
    first_at BIGINT,
    last_at BIGINT,
    PRIMARY KEY (user_id,day)
);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_correction_heads (
    user_id BIGSERIAL,
    revision BIGINT,
    source VARCHAR(80),
    updated_at BIGINT,
    PRIMARY KEY (user_id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_correction_heads_updated_at ON __LMM_APP_SCHEMA__.acquisition_correction_heads(updated_at);
CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.acquisition_corrections (
    id BIGSERIAL,
    user_id BIGINT,
    previous_revision BIGINT,
    previous_source VARCHAR(253),
    source VARCHAR(80),
    reason VARCHAR(300),
    actor_id BIGINT,
    created_at BIGINT,
    PRIMARY KEY (id)
);
CREATE INDEX IF NOT EXISTS idx_acquisition_corrections_user_id ON __LMM_APP_SCHEMA__.acquisition_corrections(user_id);
CREATE INDEX IF NOT EXISTS idx_acquisition_corrections_created_at ON __LMM_APP_SCHEMA__.acquisition_corrections(created_at);
