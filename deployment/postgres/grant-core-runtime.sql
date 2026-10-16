-- Explicit offline GRANTs after the complete fresh lmm-core init-db transaction.
-- No role passwords, automatic startup hook, wildcard grants or default ACLs.
\set ON_ERROR_STOP on
\if :{?runtime_role}
\else
\set runtime_role lmm_core_runtime
\endif
\if :{?enable_events}
\else
\set enable_events false
\endif
\if :{?enable_console}
\else
\set enable_console false
\endif
\if :{?enable_models}
\else
\set enable_models false
\endif
\if :{?enable_web_identity}
\else
\set enable_web_identity false
\endif
\if :{?enable_commerce}
\else
\set enable_commerce false
\endif
\if :{?enable_payments}
\else
\set enable_payments false
\endif

\if :{?enable_oauth2}
\else
\set enable_oauth2 false
\endif

-- @mark deployment.database-authority role=permissions
BEGIN;
SET LOCAL statement_timeout = '5s';
SELECT current_database() AS core_database \gset
SELECT EXISTS (
    SELECT FROM pg_roles r WHERE r.rolname = :'runtime_role' AND r.rolcanlogin
      AND NOT (r.rolsuper OR r.rolcreatedb OR r.rolcreaterole OR r.rolreplication OR r.rolbypassrls)
) AND EXISTS (
    SELECT FROM pg_roles r WHERE r.rolname = 'lmm_core_owner' AND NOT r.rolcanlogin
) AND NOT EXISTS (
    SELECT FROM pg_roles r WHERE r.rolname <> :'runtime_role' AND (
      pg_has_role(:'runtime_role', r.oid, 'SET')
      OR pg_has_role(:'runtime_role', r.oid, 'MEMBER WITH ADMIN OPTION')
      OR pg_has_role(:'runtime_role', r.oid, 'USAGE'))
) AS runtime_role_safe \gset
\if :runtime_role_safe
\else
\echo 'Refusing unsafe runtime role or login-enabled installation owner.'
ROLLBACK;
\quit 1
\endif

REVOKE CREATE, TEMPORARY ON DATABASE :"core_database" FROM PUBLIC;
REVOKE CREATE ON SCHEMA public FROM PUBLIC;
GRANT CONNECT ON DATABASE :"core_database" TO :"runtime_role";
GRANT USAGE ON SCHEMA core_meta, core_identity, core_billing TO :"runtime_role";
GRANT SELECT ON core_meta.schema_contract, core_meta.component_contract,
    core_meta.runtime_contract TO :"runtime_role";
GRANT SELECT ON core_billing.ledger_accounts, core_billing.balance_state,
    core_billing.ledger_operations, core_billing.ledger_journals,
    core_billing.ledger_entries, core_billing.opening_import_runs,
    core_billing.opening_imports, core_billing.account_opening_counters,
    core_billing.legacy_escrow_batch_runs, core_billing.legacy_escrow_projects,
    core_billing.legacy_escrow_claims, core_billing.legacy_escrow_disputes,
    core_billing.legacy_escrow_state, core_billing.legacy_escrow_operations TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE ON core_billing.account_policies TO :"runtime_role";
GRANT EXECUTE ON FUNCTION core_billing.open_ledger_wallet(bigint),
    core_billing.post_ledger(jsonb),
    core_billing.legacy_escrow_finalize(jsonb,bigint,bigint) TO :"runtime_role";

-- Abuse administration is checked against canonical superadmin Sessions in Rust.
-- Secrets are host-private references; audit/operations/previews are append-only.
GRANT USAGE ON SCHEMA core_abuse TO :"runtime_role";
GRANT SELECT ON ALL TABLES IN SCHEMA core_abuse TO :"runtime_role";
GRANT UPDATE (revision,document) ON core_abuse.settings TO :"runtime_role";
GRANT INSERT,UPDATE (marked,policy_id,revision) ON core_abuse.user_marks TO :"runtime_role";
GRANT INSERT ON core_abuse.previews,core_abuse.operations,core_abuse.audit,
    core_abuse.registration_observations TO :"runtime_role";
GRANT INSERT,UPDATE (attempts),DELETE ON core_abuse.model_counters TO :"runtime_role";
GRANT INSERT,UPDATE (finished_at),DELETE ON core_abuse.model_leases TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_abuse.audit_id_seq TO :"runtime_role";

-- Explicit identity-owner matrix; no writes to offline group/developer policy.
GRANT SELECT, INSERT, UPDATE ON core_identity.accounts, core_identity.users,
    core_identity.teams, core_identity.memberships, core_identity.credentials,
    core_identity.invites, core_identity.password_logins, core_identity.user_profiles,
    core_identity.company_billing_profiles, core_identity.web_sessions,
    core_identity.api_key_settings, core_identity.mfa_factors,
    core_identity.mfa_enrollments, core_identity.mfa_backup_codes,
    core_identity.mfa_challenges TO :"runtime_role";
GRANT SELECT, INSERT ON core_identity.key_funding_rules,
    core_identity.credential_grants, core_identity.oauth_identities,
    core_identity.audit, core_identity.web_session_credentials,
    core_identity.session_authentication TO :"runtime_role";
GRANT SELECT, INSERT, DELETE ON core_identity.oauth_flows TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE, DELETE ON core_identity.auth_attempts TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_identity.mfa_enrollments_id_seq,
    core_identity.mfa_backup_codes_id_seq TO :"runtime_role";
GRANT SELECT ON core_identity.developer_access,
    core_identity.user_routing_groups TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_identity.accounts_id_seq, core_identity.users_id_seq,
    core_identity.teams_id_seq, core_identity.credentials_id_seq,
    core_identity.invites_id_seq, core_identity.audit_id_seq TO :"runtime_role";

\if :enable_oauth2
-- Native OAuth issues no core credential or spending authority. Only the
-- immutable snapshot's consumption and family scope/revocation are mutable.
GRANT USAGE ON SCHEMA core_oauth TO :"runtime_role";
GRANT SELECT ON core_oauth.contract TO :"runtime_role";
GRANT SELECT, INSERT ON core_oauth.families, core_oauth.authorization_requests,
    core_oauth.authorization_codes, core_oauth.access_tokens,
    core_oauth.refresh_tokens TO :"runtime_role";
GRANT UPDATE (scopes, revoked_at) ON core_oauth.families TO :"runtime_role";
GRANT UPDATE (consumed_at) ON core_oauth.authorization_requests,
    core_oauth.authorization_codes, core_oauth.refresh_tokens TO :"runtime_role";
GRANT EXECUTE ON FUNCTION core_oauth.readonly_scope_set(text[]) TO :"runtime_role";
GRANT EXECUTE ON FUNCTION core_oauth.client_scope_set(text,text[],boolean) TO :"runtime_role";
\endif

\if :enable_commerce
-- Commerce performs charging and publishes events in the same core transaction.
\set enable_events true
\endif

\if :enable_events
GRANT USAGE ON SCHEMA core_events TO :"runtime_role";
GRANT SELECT, UPDATE (pending_events, pending_bytes) ON core_events.capacity TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE ON core_events.subscriptions, core_events.outbox,
    core_events.deliveries, core_events.inbox TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_events.outbox_id_seq TO :"runtime_role";
\endif

\if :enable_console
GRANT USAGE ON SCHEMA core_console TO :"runtime_role";
GRANT SELECT ON core_console.options, core_console.initialization,
    core_console.announcement_reads TO :"runtime_role";
GRANT INSERT ON core_console.announcement_reads TO :"runtime_role";
\endif

\if :enable_models
\set enable_charging true
\else
\set enable_charging false
\endif
\if :enable_commerce
\set enable_charging true
\endif
\if :enable_charging
GRANT USAGE ON SCHEMA core_charging TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE ON core_charging.budgets, core_charging.subscriptions,
    core_charging.requests, core_charging.ledger_holds TO :"runtime_role";
GRANT SELECT, INSERT ON core_charging.budget_checks, core_charging.usage_events,
    core_charging.refunds, core_charging.policy_events,
    core_charging.ledger_commands TO :"runtime_role";
GRANT SELECT ON core_charging.budget_opening_consumption,
    core_charging.subscription_opening_consumption TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE (refunded) ON core_charging.external_consumption TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_charging.budgets_id_seq,
    core_charging.subscriptions_id_seq, core_charging.policy_events_id_seq TO :"runtime_role";
GRANT EXECUTE ON FUNCTION core_charging.period_window(text,bigint,bigint,bigint),
    core_charging.subscription_window(bigint,bigint),
    core_charging.committed_credits(text,bigint,bigint,bigint) TO :"runtime_role";
\endif

\if :enable_web_identity
-- Legacy Web identity manages budgets, not model requests or reservation holds.
-- Without an enabled model gateway, its catalogue uses only these projections.
GRANT USAGE ON SCHEMA public TO :"runtime_role";
GRANT SELECT ON public.routing_active_release, public.routing_groups,
    public.routing_routes TO :"runtime_role";
GRANT USAGE ON SCHEMA core_charging TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE ON core_charging.budgets TO :"runtime_role";
GRANT SELECT, INSERT ON core_charging.policy_events TO :"runtime_role";
GRANT SELECT ON core_charging.requests, core_charging.external_consumption,
    core_charging.budget_opening_consumption TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_charging.budgets_id_seq,
    core_charging.policy_events_id_seq TO :"runtime_role";
GRANT EXECUTE ON FUNCTION core_charging.committed_credits(text,bigint,bigint,bigint)
    TO :"runtime_role";
\endif

\if :enable_models
GRANT USAGE ON SCHEMA core_model TO :"runtime_role";
GRANT EXECUTE ON FUNCTION
    core_model.checkpoint_output(text,bytea,bigint,bytea,bigint,jsonb,boolean),
    core_model.completed_streams(integer) TO :"runtime_role";
\endif

\if :enable_commerce
GRANT USAGE ON SCHEMA core_commerce TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE (can_spend) ON core_commerce.key_scopes TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE (state, settlement_journal_id, revision, delivery_reference)
    ON core_commerce.orders TO :"runtime_role";
GRANT SELECT, INSERT ON core_commerce.operations TO :"runtime_role";
GRANT USAGE ON SEQUENCE core_commerce.orders_id_seq TO :"runtime_role";
\endif

\if :enable_payments
GRANT USAGE ON SCHEMA core_payments TO :"runtime_role";
GRANT SELECT, INSERT, UPDATE ON core_payments.intents,
    core_payments.refund_holds TO :"runtime_role";
GRANT SELECT, INSERT ON core_payments.receipts, core_payments.provider_events,
    core_payments.provider_refunds, core_payments.blocked_refunds,
    core_payments.accepted_proofs TO :"runtime_role";
GRANT USAGE, SELECT ON SEQUENCE core_payments.intents_id_seq TO :"runtime_role";
\endif
COMMIT;
