-- Single frozen private financial snapshot; READ ONLY, no authority guesses.
-- psql -X -qAt -v target_schema=... -f this.sql > a private chmod-600 file.
-- No emails, names, API tokens, redemption codes, trade numbers or payloads.
\set ON_ERROR_STOP on
\if :{?target_schema}
\else
  \echo 'target_schema is required'
  \quit 2
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '60s';
SELECT pg_catalog.to_regclass(pg_catalog.format('%I.wallet_credit_rebases', :'target_schema')) IS NOT NULL AS has_audit_table \gset
\if :has_audit_table
 SELECT COALESCE(jsonb_agg(migration_id ORDER BY migration_id),'[]'::jsonb)::text AS existing_migrations FROM :"target_schema".wallet_credit_rebases \gset
\else
 \set existing_migrations '[]'
\endif
WITH frozen AS (SELECT EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::bigint AS at),
user_sources AS (
 SELECT id,quota,aff_quota,used_quota,request_count,aff_history,aff_count,status,deleted_at
 FROM :"target_schema".users
), token_sources AS (
 SELECT id,user_id,remain_quota,used_quota,unlimited_quota,status,created_time,
        accessed_time,expired_time,deleted_at
 FROM :"target_schema".tokens
),
pending AS (
 SELECT id,user_id,status,credited_quota,amount,platform_amount_micros,
        settled_amount_micros,expected_amount_micros,refunded_quota,
        refunded_amount_micros,money::text,payment_provider,payment_method,
        settlement_currency,failure_reason_code,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_key','""'::jsonb) AS pending_credit_rebase_key,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_original_quota','0'::jsonb) AS pending_credit_rebase_original_quota,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_effective_quota','0'::jsonb) AS pending_credit_rebase_effective_quota
 FROM :"target_schema".top_ups t WHERE status='pending' OR
   (payment_provider='waffo_pancake' AND status='failed' AND failure_reason_code='checkout_timeout')
), referrals AS (
 SELECT id,inviter_id,invitee_id,top_up_id,quota,revoked_quota,penalty_quota,
        penalty_percent,max_penalty_quota,revision,created_at,updated_at,status,reason
 FROM :"target_schema".referral_rewards
), redemptions AS (
 SELECT id,user_id,used_user_id,quota,status,created_time,redeemed_time,expired_time,
        reward_type,deleted_at
 FROM :"target_schema".redemptions, frozen
 WHERE status=1 AND deleted_at IS NULL AND COALESCE(reward_type,'quota') IN ('','quota')
   AND (expired_time=0 OR expired_time>=frozen.at)
), projects AS (
 SELECT id,owner_user_id,escrow_quota,reward_quota,net_reward_quota,reward_slots,
        platform_fee_quota,platform_fee_rate_bps,created_at,updated_at,published_at,
        closed_at,archived_at,status
 FROM :"target_schema".open_source_bounty_projects WHERE status IN ('published','paused')
), bounty_disputes AS (
 SELECT d.id,d.challenge_id,d.project_id,d.opened_by_user_id,d.against_user_id,
        d.project_escrow_quota_snapshot,d.reward_quota_snapshot,d.tip_quota_snapshot,
        d.resolved_by_user_id,d.created_at,d.updated_at,d.resolved_at,
        d.challenge_status_snapshot,d.status
 FROM :"target_schema".open_source_bounty_disputes d JOIN projects p ON p.id=d.project_id
), challenges AS (
 SELECT c.id,c.project_id,c.participant_user_id,c.reward_quota,c.tip_quota,
        c.accepted_at,c.submitted_at,c.reviewed_at,c.rejected_at,c.paid_at,
        c.created_at,c.updated_at,c.status
 FROM :"target_schema".open_source_bounty_challenges c JOIN projects p ON p.id=c.project_id
 WHERE c.paid_at=0 AND (c.status IN ('accepted','submitted','rejected') OR EXISTS (SELECT 1 FROM bounty_disputes d WHERE d.challenge_id=c.id AND d.status='open'))
), subscriptions AS (
 SELECT id,user_id,plan_id,amount_total,amount_used,quota_version,start_time,end_time,
        status,source,last_reset_time,next_reset_time,created_at,updated_at,
        COALESCE(to_jsonb(s)->'reset_amount','null'::jsonb) AS reset_amount,
        COALESCE(to_jsonb(s)->'renewal_amount','null'::jsonb) AS renewal_amount
 FROM :"target_schema".user_subscriptions s
 WHERE status='active' OR id IN (
   SELECT user_subscription_id FROM :"target_schema".subscription_orders
   WHERE status='success' AND user_subscription_id>0)
), subscription_orders AS (
 SELECT id,user_id,plan_id,user_subscription_id,status,charged_quota,
        refunded_quota,refunded_amount_micros,money::text,plan_currency,
        create_time,complete_time,plan_snapshot,expected_amount_micros,
        settlement_currency,payment_method,payment_provider,current_period_start,
        current_period_end,provider_subscription_state,provider_event_time_millis
 FROM :"target_schema".subscription_orders
 WHERE status='pending' OR user_subscription_id IN (SELECT id FROM subscriptions)
), subscription_plans AS (
 SELECT id,total_amount,created_at,updated_at,enabled,archived_at,
        price_amount::text,currency,quota_reset_period,quota_reset_custom_seconds
 FROM :"target_schema".subscription_plans
), subscription_payment_events AS (
 SELECT id,subscription_order_id,payment_provider,provider_event_id,
        provider_transaction_id,settlement_currency,settlement_amount_micros,
        period_start,period_end,created_time
 FROM :"target_schema".subscription_payment_events
 WHERE subscription_order_id IN (SELECT id FROM subscription_orders)
), subscription_payment_refunds AS (
 SELECT id,subscription_order_id,subscription_payment_event_id,payment_provider,
        provider_event_id,currency,amount_micros,quota_revoked,
        finance_ledger_entry_id,created_time
 FROM :"target_schema".subscription_payment_refunds
 WHERE subscription_order_id IN (SELECT id FROM subscription_orders)
),
tips AS (
 SELECT id,user_id,tip_quota,withdrawn_quota,created_at,updated_at,status
 FROM :"target_schema".public_relay_contributions
), assistant_gifts AS (
 SELECT id,user_id,quota,amount_cents,created_at,claimed_at,status
 FROM :"target_schema".assistant_new_user_gifts
 WHERE status='offered' AND quota>0 AND amount_cents>0
), gifts AS (
 SELECT id,quota,start_at,end_at,min_used_quota,min_account_age_days,created_at,enabled
 FROM :"target_schema".gifts, frozen WHERE enabled=true AND end_at>frozen.at
), ads AS (
 SELECT id,owner_user_id,charged_quota,bid_cents,paid_at,expires_at,hidden_at,refunded_at,status
 FROM :"target_schema".ai_directory_ads, frozen WHERE status='active' AND expires_at>frozen.at
), fees AS (
 SELECT id,user_id,charged_quota,requested_quota,created_at,reversed_at,reversed_by,status,error_code
 FROM :"target_schema".violation_fee_records WHERE status='charged' AND charged_quota>0
), email_orders AS (
 SELECT o.id,o.user_id,o.charge_quota,o.refunded_quota,o.quantity,o.created_at,o.updated_at,o.status,o.operation,
        (SELECT COALESCE(MAX(l.id),0) FROM :"target_schema".hero_sms_email_quota_ledgers l WHERE l.order_id=o.id AND l.entry_type='refund') AS last_refund_ledger_id
 FROM :"target_schema".hero_sms_email_orders o WHERE o.charge_quota>o.refunded_quota
), email_activations AS (
 SELECT a.id,a.order_id,a.user_id,a.charge_quota,a.refund_quota,a.created_at,a.updated_at,
        a.refunded_at,a.cancelled_at,a.status,a.cancel_reason
 FROM :"target_schema".hero_sms_email_activations a JOIN email_orders o ON o.id=a.order_id
), options AS (
 SELECT key,value FROM :"target_schema".options WHERE key IN ('AudioCompletionRatio','AudioRatio','billing_setting.billing_expr','billing_setting.billing_mode','CacheRatio','CompletionRatio','CreateCacheRatio','CreditsPerUSD','ImageRatio','LegacyPricingQuotaPerUnit','ModelPrice','ModelPriceLock','ModelRatio','PublicCreditsPerUSD','QuotaPerUnit','USDExchangeRate','tool_price_setting.prices')
), wallet_users AS (
 SELECT id,quota,aff_quota FROM :"target_schema".users
), wallet_tokens AS (
 SELECT id,user_id,remain_quota,unlimited_quota FROM :"target_schema".tokens
), successful_topups AS (
 SELECT id,user_id,status,credited_quota,amount,platform_amount_micros,
        settled_amount_micros,expected_amount_micros,refunded_quota,
        refunded_amount_micros,money::text,payment_provider,payment_method,settlement_currency
 FROM :"target_schema".top_ups WHERE status='success' AND (credited_quota<>0 OR amount<>0)
)
SELECT jsonb_build_object('version',1,
 'target',jsonb_build_object('database',pg_catalog.current_database(),'database_oid',(SELECT oid::text FROM pg_catalog.pg_database WHERE datname=pg_catalog.current_database()),'schema',:'target_schema','schema_oid',pg_catalog.to_regnamespace(:'target_schema')::oid::text,'system_identifier',(SELECT system_identifier::text FROM pg_catalog.pg_control_system())),
 'applied_migration_ids',:'existing_migrations'::jsonb,
 'audited_option_keys','["AudioCompletionRatio","AudioRatio","billing_setting.billing_expr","billing_setting.billing_mode","CacheRatio","CompletionRatio","CreateCacheRatio","CreditsPerUSD","ImageRatio","LegacyPricingQuotaPerUnit","ModelPrice","ModelPriceLock","ModelRatio","PublicCreditsPerUSD","QuotaPerUnit","USDExchangeRate","tool_price_setting.prices"]'::jsonb,
 'options',COALESCE((SELECT jsonb_object_agg(key,value) FROM options),'{}'::jsonb),
 'users',COALESCE((SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM wallet_users u),'[]'::jsonb),
 'tokens',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM wallet_tokens t),'[]'::jsonb),
 'topups',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM successful_topups t),'[]'::jsonb)) || jsonb_build_object('snapshot_at',(SELECT at FROM frozen),
 'user_sources',COALESCE((SELECT jsonb_agg(to_jsonb(u) ORDER BY id) FROM user_sources u),'[]'::jsonb),
 'token_sources',COALESCE((SELECT jsonb_agg(to_jsonb(t) ORDER BY id) FROM token_sources t),'[]'::jsonb),
 'pending_topups',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM pending p),'[]'::jsonb),
 'referrals',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM referrals r),'[]'::jsonb),
 'entities',jsonb_build_object(
   'redemptions',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM redemptions r),'[]'::jsonb),
   'bounty_projects',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM projects p),'[]'::jsonb),
   'bounty_challenges',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM challenges c),'[]'::jsonb),
   'bounty_disputes',COALESCE((SELECT jsonb_agg(to_jsonb(d) ORDER BY id) FROM bounty_disputes d),'[]'::jsonb)),
 'subscriptions',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM subscriptions s),'[]'::jsonb),
 'subscription_orders',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM subscription_orders o),'[]'::jsonb),
 'subscription_plans',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM subscription_plans p),'[]'::jsonb),
 'subscription_payment_events',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM subscription_payment_events p),'[]'::jsonb),
 'subscription_payment_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM subscription_payment_refunds r),'[]'::jsonb)) || jsonb_build_object('snapshot_at',(SELECT at FROM frozen),'obligations',jsonb_build_object(
 'wallet_transfers_pending',(SELECT count(*) FROM :"target_schema".wallet_transfers WHERE status='pending'),
 'tool_market_held',(SELECT count(*) FROM :"target_schema".tool_market_calls WHERE settlement_status='held'),
 'tasks_unfinished',(SELECT count(*) FROM :"target_schema".tasks WHERE COALESCE(status,'') NOT IN ('SUCCESS','FAILURE')),
 'tasks_refund_pending',(SELECT count(*) FROM :"target_schema".tasks WHERE refund_status='PENDING' OR (status='FAILURE' AND COALESCE(refund_status,'')='' AND (quota<>0 OR refund_quota<>0) AND (submit_time<=0 OR submit_time>=1771718400))),
 'midjourney_unfinished',(SELECT count(*) FROM :"target_schema".midjourneys WHERE progress<>'100%'),
 'subscription_reservations',(SELECT count(*) FROM :"target_schema".subscription_pre_consume_records WHERE status IN ('consumed','settling')),
 'sms_unfinished',(SELECT count(*) FROM :"target_schema".hero_sms_sms_orders WHERE status IN ('pending_provider','purchase_unknown','active','cancel_pending')),
 'email_orders_unfinished',(SELECT count(*) FROM :"target_schema".hero_sms_email_orders WHERE status IN ('pending_provider','purchase_unknown','reconciling')),
 'email_activations_unfinished',(SELECT count(*) FROM :"target_schema".hero_sms_email_activations WHERE status IN ('pending_provider','active','reconciling','cancel_pending'))),
 'other_rights',jsonb_build_object(
 'public_relay_tip_pools',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM tips r),'[]'::jsonb),
 'assistant_gifts',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM assistant_gifts r),'[]'::jsonb),
 'grant_gifts',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM gifts r),'[]'::jsonb),
 'ai_directory_ad_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM ads r),'[]'::jsonb),
 'violation_fee_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM fees r),'[]'::jsonb),
 'hero_sms_email_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM email_orders r),'[]'::jsonb),
 'hero_sms_email_activations',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM email_activations r),'[]'::jsonb)));
ROLLBACK;
-- Offline Go authority enrichment is required for ALL successful and pending topups.
