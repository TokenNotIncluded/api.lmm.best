-- READ ONLY private supplement. Redirect psql -X -qAt output to a chmod-600 file.
-- Do not print this artifact: it contains exact financial facts and internal IDs.
-- No token secrets, trade numbers, provider payloads, user names or emails.
-- All users (including deleted users) are in scope for this all-user correction.
\set ON_ERROR_STOP on
\if :{?target_schema}
\else
  \echo 'target_schema is required'
  \quit 2
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
WITH frozen AS (SELECT EXTRACT(EPOCH FROM CURRENT_TIMESTAMP)::bigint AS at),
pending AS (
 SELECT id,user_id,status,credited_quota,amount,platform_amount_micros,
        settled_amount_micros,expected_amount_micros,refunded_quota,
        refunded_amount_micros,money::text,payment_provider,payment_method,
        settlement_currency,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_key','""'::jsonb) AS pending_credit_rebase_key,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_original_quota','0'::jsonb) AS pending_credit_rebase_original_quota,
        COALESCE(to_jsonb(t)->'pending_credit_rebase_effective_quota','0'::jsonb) AS pending_credit_rebase_effective_quota
 FROM :"target_schema".top_ups t WHERE status='pending'
), referrals AS (
 SELECT id,inviter_id,invitee_id,top_up_id,quota,revoked_quota,penalty_quota,
        penalty_percent,max_penalty_quota,revision,created_at,updated_at,status,reason
 FROM :"target_schema".referral_rewards
), redemptions AS (
 SELECT id,user_id,used_user_id,quota,status,created_time,redeemed_time,expired_time,
        COALESCE(reward_type,'quota') AS reward_type,deleted_at
 FROM :"target_schema".redemptions, frozen
 WHERE status=1 AND deleted_at IS NULL AND COALESCE(reward_type,'quota') IN ('','quota')
   AND (expired_time=0 OR expired_time>=frozen.at)
), projects AS (
 SELECT id,owner_user_id,escrow_quota,reward_quota,net_reward_quota,reward_slots,
        platform_fee_quota,platform_fee_rate_bps,created_at,updated_at,published_at,
        closed_at,archived_at,status
 FROM :"target_schema".open_source_bounty_projects WHERE status IN ('published','paused')
), challenges AS (
 SELECT c.id,c.project_id,c.participant_user_id,c.reward_quota,c.tip_quota,
        c.accepted_at,c.submitted_at,c.reviewed_at,c.rejected_at,c.paid_at,
        c.created_at,c.updated_at,c.status
 FROM :"target_schema".open_source_bounty_challenges c JOIN projects p ON p.id=c.project_id
 WHERE c.status IN ('accepted','submitted','rejected') AND c.paid_at=0
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
)
SELECT jsonb_build_object(
 'snapshot_at',(SELECT at FROM frozen),
 'pending_topups',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM pending p),'[]'::jsonb),
 'referrals',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM referrals r),'[]'::jsonb),
 'entities',jsonb_build_object(
   'redemptions',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM redemptions r),'[]'::jsonb),
   'bounty_projects',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM projects p),'[]'::jsonb),
   'bounty_challenges',COALESCE((SELECT jsonb_agg(to_jsonb(c) ORDER BY id) FROM challenges c),'[]'::jsonb)),
 'subscriptions',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY id) FROM subscriptions s),'[]'::jsonb),
 'subscription_orders',COALESCE((SELECT jsonb_agg(to_jsonb(o) ORDER BY id) FROM subscription_orders o),'[]'::jsonb),
 'subscription_plans',COALESCE((SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM subscription_plans p),'[]'::jsonb));
ROLLBACK;
-- pending_topups must also be enriched by ExportWalletTopUpCreditRebaseFacts.
-- Re-export every wallet/topup/options/right together after writers are frozen;
-- this supplement alone is not a final production snapshot.
