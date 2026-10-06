-- Read-only aggregate inventory. Invoke psql with explicit -v target_schema=...
-- Contains no user ids, order ids, tokens, messages, email or provider payloads.
\set ON_ERROR_STOP on
\if :{?target_schema}
\else
  \echo 'target_schema is required; no public/default schema is assumed'
  \quit 2
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
SELECT current_database() AS database, :'target_schema' AS target_schema;
SELECT 'wallet_transfer' AS scope, status, COUNT(*) AS rows, COALESCE(SUM(quota),0) AS credit
FROM :"target_schema".wallet_transfers GROUP BY status;
SELECT 'topup' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(credited_quota),0) AS credited_credit,
       COALESCE(SUM(refunded_quota),0) AS refunded_credit,
       COUNT(*) FILTER (WHERE credited_quota=0 AND amount<>0) AS legacy_amount_fallback_rows
FROM :"target_schema".top_ups GROUP BY status;
SELECT 'usable_redemption' AS scope, COUNT(*) AS rows, COALESCE(SUM(quota),0) AS credit
FROM :"target_schema".redemptions
WHERE status=1 AND deleted_at IS NULL AND COALESCE(reward_type,'quota') IN ('','quota')
  AND (expired_time=0 OR expired_time>=EXTRACT(EPOCH FROM CURRENT_TIMESTAMP));
SELECT 'unclaimed_red_packet_items' AS scope, item_type, COUNT(*) AS rows
FROM :"target_schema".red_packet_items WHERE claimed_by=0 GROUP BY item_type;
SELECT 'bounty_project' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(escrow_quota),0) AS escrow_credit,
       COALESCE(SUM(reward_quota),0) AS advertised_reward_credit
FROM :"target_schema".open_source_bounty_projects GROUP BY status;
SELECT 'bounty_challenge' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(reward_quota),0) AS reward_credit,
       COALESCE(SUM(tip_quota),0) AS tip_credit
FROM :"target_schema".open_source_bounty_challenges GROUP BY status;
SELECT 'tool_call' AS scope, settlement_status, execution_status, COUNT(*) AS rows,
       COALESCE(SUM(price_quota),0) AS credit
FROM :"target_schema".tool_market_calls GROUP BY settlement_status,execution_status;
SELECT 'task' AS scope, status, refund_status, COUNT(*) AS rows,
       COALESCE(SUM(quota),0) AS current_credit,
       COALESCE(SUM(refund_quota),0) AS refund_credit
FROM :"target_schema".tasks GROUP BY status,refund_status;
-- Exact delayed-refund match from GetUnrefundedFailedTasks; a failed task is
-- not necessarily financially terminal even when no PENDING marker exists yet.
SELECT 'task_delayed_refund_blocker' AS scope, COUNT(*) AS rows,
       COALESCE(SUM(CASE WHEN refund_quota>0 THEN refund_quota ELSE quota END),0) AS credit
FROM :"target_schema".tasks
WHERE refund_status='PENDING' OR
 (status='FAILURE' AND COALESCE(refund_status,'')='' AND (quota<>0 OR refund_quota<>0)
  AND (submit_time<=0 OR submit_time>=1771718400));
SELECT 'midjourney_unfinished_blocker' AS scope, COUNT(*) AS rows,
       COALESCE(SUM(quota),0) AS credit
FROM :"target_schema".midjourneys WHERE progress<>'100%';
SELECT 'subscription_reservation' AS scope, status, recovery_state, COUNT(*) AS rows,
       COALESCE(SUM(pre_consumed),0) AS subscription_reserved_credit,
       COALESCE(SUM(wallet_consumed),0) AS wallet_credit,
       COALESCE(SUM(token_consumed),0) AS token_credit
FROM :"target_schema".subscription_pre_consume_records GROUP BY status,recovery_state;
SELECT 'subscription_order' AS scope, status, payment_method, COUNT(*) AS rows,
       COALESCE(SUM(charged_quota),0) AS historically_charged_credit,
       COALESCE(SUM(refunded_quota),0) AS historically_refunded_credit
FROM :"target_schema".subscription_orders GROUP BY status,payment_method;
SELECT 'user_subscription' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(amount_total),0) AS package_total_credit,
       COALESCE(SUM(amount_used),0) AS package_used_credit,
       COALESCE(SUM(GREATEST(amount_total-amount_used,0)),0) AS package_remaining_credit
FROM :"target_schema".user_subscriptions GROUP BY status;
SELECT 'sms_order' AS scope, status, complaint_status, COUNT(*) AS rows,
       COALESCE(SUM(reserved_quota),0) AS reserved_credit,
       COALESCE(SUM(charge_quota),0) AS charged_credit,
       COALESCE(SUM(refunded_quota),0) AS refunded_credit
FROM :"target_schema".hero_sms_sms_orders GROUP BY status,complaint_status;
SELECT 'email_order' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(charge_quota-refunded_quota),0) AS possible_future_refund_credit
FROM :"target_schema".hero_sms_email_orders GROUP BY status;
SELECT 'email_activation' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(charge_quota),0) AS original_charge_credit
FROM :"target_schema".hero_sms_email_activations GROUP BY status;
SELECT 'referral_reward' AS scope, status, COUNT(*) AS rows,
       COALESCE(SUM(quota),0) AS original_reward_credit,
       COALESCE(SUM(revoked_quota),0) AS original_revoked_credit,
       COALESCE(SUM(penalty_quota),0) AS original_penalty_credit
FROM :"target_schema".referral_rewards GROUP BY status;
ROLLBACK;
