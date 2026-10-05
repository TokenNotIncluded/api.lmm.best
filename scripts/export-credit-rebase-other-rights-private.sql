-- READ ONLY private supplement; psql -X -qAt output belongs in a chmod-600 file.
-- Includes exact financial facts and internal IDs. No names, emails, secrets,
-- provider IDs, request IDs, payloads or redemption codes are selected.
-- snapshot_at must equal the timestamp used by the joint stopped-writer snapshot.
\set ON_ERROR_STOP on
\if :{?target_schema}
\else
  \echo 'target_schema is required'
  \quit 2
\endif
\if :{?snapshot_at}
\else
  \echo 'snapshot_at is required'
  \quit 2
\endif
BEGIN TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY;
SET LOCAL statement_timeout = '30s';
WITH frozen AS (SELECT :'snapshot_at'::bigint AS at),
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
)
SELECT jsonb_build_object('snapshot_at',(SELECT at FROM frozen),'other_rights',jsonb_build_object(
 'public_relay_tip_pools',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM tips r),'[]'::jsonb),
 'assistant_gifts',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM assistant_gifts r),'[]'::jsonb),
 'grant_gifts',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM gifts r),'[]'::jsonb),
 'ai_directory_ad_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM ads r),'[]'::jsonb),
 'violation_fee_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM fees r),'[]'::jsonb),
 'hero_sms_email_refunds',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM email_orders r),'[]'::jsonb),
 'hero_sms_email_activations',COALESCE((SELECT jsonb_agg(to_jsonb(r) ORDER BY id) FROM email_activations r),'[]'::jsonb)));
ROLLBACK;
