-- SYNTHETIC ONLY. This is a test provider for the A04 consumer contract.
-- It is not an import plan, source qualification proof, or production schema.
CREATE SCHEMA a04_fixture;
CREATE SCHEMA core_history;
CREATE TABLE a04_fixture.contract (
 dataset text PRIMARY KEY, version integer NOT NULL DEFAULT 1,
 complete boolean NOT NULL DEFAULT true,
 manifest_sha256 text NOT NULL DEFAULT repeat('a',64)
);
CREATE VIEW core_history.source_contract_v1 AS SELECT * FROM a04_fixture.contract;
CREATE TABLE a04_fixture.logs (
 id bigint PRIMARY KEY, event_key text NOT NULL, user_id bigint NOT NULL,
 owner_account_id bigint NOT NULL, credential_id bigint,
 created_at bigint NOT NULL DEFAULT 1700000000, log_type integer NOT NULL DEFAULT 2,
 username text NOT NULL DEFAULT 'synthetic-user', token_name text NOT NULL DEFAULT 'synthetic-key',
 model_name text NOT NULL DEFAULT 'local-model', quota bigint,
 prompt_tokens bigint NOT NULL DEFAULT 5, completion_tokens bigint NOT NULL DEFAULT 2,
 use_time bigint NOT NULL DEFAULT 1, is_stream boolean NOT NULL DEFAULT false,
 channel_id bigint, group_name text NOT NULL DEFAULT 'default', request_id text NOT NULL DEFAULT '',
 other text NOT NULL DEFAULT '{"pricing_credits_per_usd":500000}'
);
CREATE VIEW core_history.log_records_v1 AS SELECT * FROM a04_fixture.logs;
CREATE TABLE a04_fixture.quota_data (
 id bigint PRIMARY KEY, user_id bigint NOT NULL, owner_account_id bigint NOT NULL,
 username text NOT NULL, created_at bigint NOT NULL, model_name text NOT NULL,
 token_id bigint NOT NULL, token_name text NOT NULL, use_group text NOT NULL,
 channel_id bigint NOT NULL, channel_name text NOT NULL, node_name text NOT NULL,
 quota bigint, token_used bigint NOT NULL, count bigint NOT NULL
);
CREATE VIEW core_history.quota_data_v1 AS SELECT * FROM a04_fixture.quota_data;
CREATE TABLE a04_fixture.finance_records (
 dataset text NOT NULL, id bigint NOT NULL, created_at bigint NOT NULL DEFAULT 1700000000,
 record jsonb NOT NULL, PRIMARY KEY (dataset,id)
);
CREATE VIEW core_history.finance_records_v1 AS SELECT * FROM a04_fixture.finance_records;
CREATE TABLE a04_fixture.finance_options (name text PRIMARY KEY,value text NOT NULL);
CREATE VIEW core_history.finance_options_v1 AS SELECT * FROM a04_fixture.finance_options;
INSERT INTO a04_fixture.contract(dataset) VALUES
 ('logs'),('token_logs'),('quota_data'),('users-balances'),('channels-pricing'),('subscription-plans'),
 ('topups'),('subscription-orders'),('subscription-payment-events'),('bounty-ledger'),
 ('checkins'),('redemptions'),('user-subscriptions'),('financial-options'),('effective-model-pricing');
