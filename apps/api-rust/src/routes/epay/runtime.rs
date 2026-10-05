//! Database-backed ePay adapters. Provider authentication and settlement use
//! the live Go option names and immutable order evidence, never callback quota.

mod checkout;
pub(crate) use checkout::validate_configured_payment_access;

use super::*;
use crate::routes::billing_payments::md5_hex;
use rust_decimal::{Decimal, prelude::ToPrimitive};
use sqlx::{PgPool, Postgres, Row, Transaction};
use subtle::ConstantTimeEq;

pub(crate) const MAX_WALLET_QUOTA: i64 = 9_007_199_254_740_991;
const MICROS: i64 = 1_000_000;

#[derive(Clone)]
pub struct PgEpayGateway {
    pg: PgPool,
}

impl PgEpayGateway {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg }
    }

    async fn configuration(&self) -> Result<EpayConfiguration, TopupError> {
        EpayConfiguration::from_options(&options(&self.pg).await?)
    }
}

struct EpayConfiguration {
    address: reqwest::Url,
    partner_id: String,
    key: String,
    server_address: String,
    callback_address: String,
}

impl EpayConfiguration {
    fn from_options(values: &BTreeMap<String, String>) -> Result<Self, TopupError> {
        let configured = |key: &str| {
            values
                .get(key)
                .filter(|value| !value.trim().is_empty())
                .cloned()
        };
        if !compliance_confirmed(values) {
            return Err(TopupError::ProviderFrozen);
        }
        let methods: Vec<Value> = values
            .get("PayMethods")
            .and_then(|raw| serde_json::from_str(raw).ok())
            .unwrap_or_default();
        if methods.is_empty() {
            return Err(TopupError::ProviderFrozen);
        }
        let address =
            reqwest::Url::parse(&configured("PayAddress").ok_or(TopupError::ProviderFrozen)?)
                .map_err(|_| TopupError::Provider)?;
        if !matches!(address.scheme(), "http" | "https") || address.host_str().is_none() {
            return Err(TopupError::Provider);
        }
        let server_address = configured("ServerAddress").unwrap_or_default();
        Ok(Self {
            address,
            partner_id: configured("EpayId").ok_or(TopupError::ProviderFrozen)?,
            key: configured("EpayKey").ok_or(TopupError::ProviderFrozen)?,
            callback_address: configured("CustomCallbackAddress")
                .unwrap_or_else(|| server_address.clone()),
            server_address,
        })
    }

    fn checkout(&self, order: &PendingTopup, name: String) -> Result<Checkout, TopupError> {
        let mut address = self.address.clone();
        address.set_path(&format!(
            "{}/submit.php",
            address.path().trim_end_matches('/')
        ));
        let mut fields = BTreeMap::from([
            ("pid".to_owned(), self.partner_id.clone()),
            ("type".to_owned(), order.payment_method.clone()),
            ("out_trade_no".to_owned(), order.trade_no.clone()),
            (
                "notify_url".to_owned(),
                format!("{}/api/user/epay/notify", self.callback_address),
            ),
            (
                "return_url".to_owned(),
                format!("{}/usage-logs", self.server_address.trim_end_matches('/')),
            ),
            ("name".to_owned(), name),
            ("money".to_owned(), order.money.clone()),
            ("device".to_owned(), "pc".to_owned()),
            ("sign_type".to_owned(), "MD5".to_owned()),
        ]);
        let bytes = fields
            .iter()
            .map(|(key, value)| (key.as_bytes().to_vec(), value.as_bytes().to_vec()))
            .collect();
        fields.insert("sign".into(), epay_signature(&bytes, self.key.as_bytes()));
        Ok(Checkout {
            url: address.to_string(),
            data: json!(fields),
        })
    }

    fn verify(&self, fields: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
        let values = fields.values();
        let signature = values.get(b"sign".as_slice()).ok_or(TopupError::Provider)?;
        let expected = epay_signature(values, self.key.as_bytes());
        if !bool::from(expected.as_bytes().ct_eq(signature)) {
            return Err(TopupError::Provider);
        }
        let value = |key: &[u8]| -> Result<String, TopupError> {
            String::from_utf8(values.get(key).cloned().unwrap_or_default())
                .map_err(|_| TopupError::Provider)
        };
        Ok(EpayCallback {
            verified: true,
            trade_success: value(b"trade_status")? == "TRADE_SUCCESS",
            trade_no: value(b"out_trade_no")?,
            payment_method: value(b"type")?,
            money: value(b"money")?,
            provider_transaction_id: value(b"trade_no")?,
        })
    }
}

/// Matches go-epay v0.0.4: sort the original bytes, omit only the two
/// signature fields and empty values, then append the merchant key.
fn epay_signature(fields: &BTreeMap<Vec<u8>, Vec<u8>>, key: &[u8]) -> String {
    let mut canonical = Vec::new();
    for (name, value) in fields {
        if name == b"sign" || name == b"sign_type" || value.is_empty() {
            continue;
        }
        if !canonical.is_empty() {
            canonical.push(b'&');
        }
        canonical.extend_from_slice(name);
        canonical.push(b'=');
        canonical.extend_from_slice(value);
    }
    canonical.extend_from_slice(key);
    md5_hex(&canonical)
}

#[async_trait]
impl EpayGateway for PgEpayGateway {
    async fn available(&self) -> Result<(), TopupError> {
        self.configuration().await.map(|_| ())
    }

    async fn begin(&self, order: &PendingTopup) -> Result<Checkout, TopupError> {
        self.configuration().await?.checkout(
            order,
            format!(
                "TUC{}",
                Decimal::new(order.snapshot.platform_amount_micros, 6).normalize()
            ),
        )
    }

    async fn verify(&self, fields: &EpayCallbackFields) -> Result<EpayCallback, TopupError> {
        self.configuration().await?.verify(fields)
    }
}

#[derive(Clone)]
pub struct PgEpayRepository {
    pg: PgPool,
    valkey: Option<redis::Client>,
}

impl PgEpayRepository {
    #[must_use]
    pub fn new(pg: PgPool) -> Self {
        Self { pg, valkey: None }
    }

    #[must_use]
    pub fn with_valkey(mut self, valkey: redis::Client) -> Self {
        self.valkey = Some(valkey);
        self
    }

    pub(crate) async fn invalidate_user(&self, user_id: i64) {
        if let Some(client) = &self.valkey {
            let result = async {
                let mut connection = client.get_multiplexed_async_connection().await?;
                redis::cmd("DEL")
                    .arg(format!("user:{user_id}"))
                    .query_async::<()>(&mut connection)
                    .await
            }
            .await;
            if result.is_err() {
                tracing::warn!(user_id, "ePay settled but user cache invalidation failed");
            }
        }
    }

    async fn settle(
        &self,
        callback: &EpayCallback,
        caller_ip: &str,
    ) -> Result<Completion, TopupError> {
        if !callback.verified
            || !callback.trade_success
            || callback.trade_no.trim().is_empty()
            || callback.provider_transaction_id.trim().is_empty()
        {
            return Err(TopupError::Provider);
        }
        let money = monetary_micros(&callback.money)?;
        let mut tx = self.pg.begin().await.map_err(storage)?;
        let row = sqlx::query("SELECT id::bigint, user_id::bigint, money::text AS money, payment_provider, payment_method, status, expected_amount_micros, settled_amount_micros, credited_quota, settlement_currency, provider_transaction_id, provider_event_id, COALESCE(discount_code_id,0)::bigint AS discount_code_id, referral_excluded FROM top_ups WHERE trade_no=$1 FOR UPDATE")
            .bind(&callback.trade_no).fetch_optional(&mut *tx).await.map_err(storage)?
            .ok_or(TopupError::Provider)?;
        let order_id: i64 = row.try_get("id").map_err(storage)?;
        let user_id: i64 = row.try_get("user_id").map_err(storage)?;
        let provider: String = row.try_get("payment_provider").map_err(storage)?;
        let method: String = row.try_get("payment_method").map_err(storage)?;
        let status: String = row.try_get("status").map_err(storage)?;
        let expected: i64 = row.try_get("expected_amount_micros").map_err(storage)?;
        let settled: i64 = row.try_get("settled_amount_micros").map_err(storage)?;
        let quota: i64 = row.try_get("credited_quota").map_err(storage)?;
        let currency: String = row.try_get("settlement_currency").map_err(storage)?;
        let transaction: Option<String> =
            row.try_get("provider_transaction_id").map_err(storage)?;
        let event: Option<String> = row.try_get("provider_event_id").map_err(storage)?;
        let actual_transaction = callback.provider_transaction_id.trim();
        if provider != EPAY
            || method != callback.payment_method
            || expected <= 0
            || money != expected
            || !(1..=MAX_WALLET_QUOTA).contains(&quota)
            || currency.trim().is_empty()
            || transaction
                .as_deref()
                .is_some_and(|value| !value.trim().is_empty() && value.trim() != actual_transaction)
        {
            return Err(TopupError::Provider);
        }
        // The provider transaction is globally unique across orders, including
        // concurrent callbacks to two different order rows. The schema unique
        // index is the final arbiter after this diagnostic read.
        let bound: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM top_ups WHERE payment_provider='epay' AND provider_transaction_id=$1 AND id<>$2)")
            .bind(actual_transaction).bind(order_id).fetch_one(&mut *tx).await.map_err(storage)?;
        if bound {
            return Err(TopupError::Provider);
        }
        if status == "success" {
            if settled != 0 && settled != money {
                return Err(TopupError::Provider);
            }
            // Go may backfill evidence on a successful historical/manual row;
            // that must never repeat the wallet, referral or coupon mutation.
            sqlx::query("UPDATE top_ups SET settled_amount_micros=$2, provider_transaction_id=$3 WHERE id=$1")
                .bind(order_id).bind(money).bind(actual_transaction).execute(&mut *tx).await.map_err(storage)?;
            tx.commit().await.map_err(storage)?;
            self.invalidate_user(user_id).await;
            return Ok(Completion::AlreadySucceeded);
        }
        if status != "pending"
            || event
                .as_deref()
                .is_some_and(|value| !value.trim().is_empty())
        {
            return Err(TopupError::Provider);
        }
        let credited = sqlx::query("UPDATE users SET quota=quota+$2 WHERE id=$1 AND deleted_at IS NULL AND quota >= $3 AND quota <= $4")
            .bind(user_id).bind(quota).bind(-MAX_WALLET_QUOTA).bind(MAX_WALLET_QUOTA-quota)
            .execute(&mut *tx).await.map_err(storage)?.rows_affected();
        if credited != 1 {
            return Err(TopupError::Provider);
        }
        let now = now_seconds()?;
        sqlx::query("UPDATE top_ups SET status='success', settled_amount_micros=$2, provider_transaction_id=$3, complete_time=$4 WHERE id=$1")
            .bind(order_id).bind(money).bind(actual_transaction).bind(now).execute(&mut *tx).await.map_err(storage)?;
        let excluded: bool = row.try_get("referral_excluded").map_err(storage)?;
        if !excluded && currency.eq_ignore_ascii_case("CNY") && financial_method(&method) {
            grant_referral(&mut tx, order_id, user_id, quota, now).await?;
        }
        let discount_id: i64 = row.try_get("discount_code_id").map_err(storage)?;
        consume_discount(&mut tx, callback.trade_no.trim(), discount_id, user_id, now).await?;
        let stored_money: String = row.try_get("money").map_err(storage)?;
        tx.commit().await.map_err(storage)?;
        self.invalidate_user(user_id).await;
        if record_topup_log(&self.pg, user_id, quota, &stored_money, &method, caller_ip)
            .await
            .is_err()
        {
            tracing::warn!(
                user_id,
                "ePay settled but top-up audit log could not be written"
            );
        }
        Ok(Completion::Completed)
    }
}

#[async_trait]
impl TopupRepository for PgEpayRepository {
    async fn minimum_amount(&self) -> Result<i64, TopupError> {
        self.current_minimum().await
    }
    async fn payment_method_allowed(&self, method: &str) -> Result<bool, TopupError> {
        self.current_method_allowed(method).await
    }
    async fn quote(&self, input: CreateTopup) -> Result<QuotedTopup, TopupError> {
        self.quote_current(input).await
    }
    async fn create_pending(&self, input: CreateTopup) -> Result<PendingTopup, TopupError> {
        self.create_quoted_pending(self.quote_current(input).await?)
            .await
    }
    async fn insert_prepared_pending(&self, order: PendingTopup) -> Result<(), TopupError> {
        self.persist_pending(order).await
    }
    async fn complete(
        &self,
        _: &str,
        _: &str,
        _: Option<&str>,
        _: &str,
    ) -> Result<Completion, TopupError> {
        // A trade number without signed evidence can never settle this store.
        Err(TopupError::ProviderFrozen)
    }
    async fn complete_verified(
        &self,
        callback: &EpayCallback,
        _: &str,
    ) -> Result<Completion, TopupError> {
        self.settle(callback, "").await
    }
    async fn complete_verified_at(
        &self,
        callback: &EpayCallback,
        _: &str,
        caller_ip: &str,
    ) -> Result<Completion, TopupError> {
        self.settle(callback, caller_ip).await
    }
}

async fn record_topup_log(
    pg: &PgPool,
    user_id: i64,
    quota: i64,
    money: &str,
    method: &str,
    caller_ip: &str,
) -> Result<(), sqlx::Error> {
    use crate::routes::topup::{format_quota, node_name, server_ip, service_version};
    let username: String =
        sqlx::query_scalar("SELECT COALESCE(username,'') FROM users WHERE id=$1")
            .bind(user_id)
            .fetch_optional(pg)
            .await?
            .unwrap_or_default();
    let quota_text = format_quota(pg, quota, true).await;
    let money = money.parse::<f64>().unwrap_or(0.0);
    let other = json!({"admin_info":{"server_ip":server_ip(),"node_name":node_name(),"caller_ip":caller_ip,"payment_method":method,"callback_payment_method":"epay","version":service_version()}}).to_string();
    sqlx::query("INSERT INTO logs (user_id,created_at,type,content,username,token_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,channel_id,token_id,\"group\",ip,other) VALUES($1,$2,1,$3,$4,'','',0,0,0,0,false,0,0,'',$5,$6)")
        .bind(user_id).bind(now_seconds().unwrap_or(0))
        .bind(format!("使用在线充值成功，充值金额: {quota_text}，支付金额：{money:.6}"))
        .bind(username).bind(caller_ip).bind(other).execute(pg).await?;
    Ok(())
}

pub(crate) async fn consume_discount(
    tx: &mut Transaction<'_, Postgres>,
    trade_no: &str,
    discount_id: i64,
    user_id: i64,
    now: i64,
) -> Result<(), TopupError> {
    if discount_id <= 0 {
        return Ok(());
    }
    let reservation = sqlx::query("SELECT id::bigint, discount_code_id::bigint, user_id::bigint, status FROM discount_code_reservations WHERE top_up_trade_no=$1 FOR UPDATE")
        .bind(trade_no).fetch_optional(&mut **tx).await.map_err(storage)?;
    if let Some(row) = &reservation {
        if row.try_get::<i64, _>("discount_code_id").map_err(storage)? != discount_id
            || row.try_get::<i64, _>("user_id").map_err(storage)? != user_id
        {
            return Err(TopupError::Provider);
        }
        if row.try_get::<String, _>("status").map_err(storage)? == "consumed" {
            return Ok(());
        }
    }
    // Capacity/expiry were validated before checkout. An already-paid order
    // must still credit after a coupon expires, is released or is deleted.
    let changed = sqlx::query("UPDATE discount_codes SET used_count=used_count+1 WHERE id=$1")
        .bind(discount_id)
        .execute(&mut **tx)
        .await
        .map_err(storage)?
        .rows_affected();
    if changed == 0 {
        return Ok(());
    }
    if reservation.is_some() {
        sqlx::query("UPDATE discount_code_reservations SET status='consumed', updated_time=$2 WHERE top_up_trade_no=$1")
            .bind(trade_no).bind(now).execute(&mut **tx).await.map_err(storage)?;
    } else {
        sqlx::query("INSERT INTO discount_code_reservations(discount_code_id,top_up_trade_no,user_id,status,expires_time,created_time,updated_time) VALUES($1,$2,$3,'consumed',$4,$4,$4)")
            .bind(discount_id).bind(trade_no).bind(user_id).bind(now).execute(&mut **tx).await.map_err(storage)?;
    }
    Ok(())
}

pub(crate) async fn grant_referral(
    tx: &mut Transaction<'_, Postgres>,
    top_up_id: i64,
    user_id: i64,
    credited_quota: i64,
    now: i64,
) -> Result<(), TopupError> {
    let invitee = sqlx::query("SELECT COALESCE(inviter_id,0)::bigint AS inviter_id,COALESCE(status,0)::bigint AS status,COALESCE(email,'') AS email,COALESCE(stripe_customer,'') AS stripe_customer,referral_first_top_up_id::bigint FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
        .bind(user_id).fetch_one(&mut **tx).await.map_err(storage)?;
    if invitee
        .try_get::<i64, _>("referral_first_top_up_id")
        .map_err(storage)?
        != 0
    {
        return Ok(());
    }
    sqlx::query(
        "UPDATE users SET referral_first_top_up_id=$2 WHERE id=$1 AND referral_first_top_up_id=0",
    )
    .bind(user_id)
    .bind(top_up_id)
    .execute(&mut **tx)
    .await
    .map_err(storage)?;
    let prior: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM top_ups WHERE user_id=$1 AND id<>$2 AND referral_excluded=false AND status='success' AND (settled_amount_micros>0 OR (settled_amount_micros=0 AND money>0)) AND COALESCE(payment_method,'')<>'balance' AND COALESCE(payment_provider,'')<>'balance' AND NOT(LOWER(COALESCE(payment_provider,''))='epay' AND LOWER(COALESCE(payment_method,'')) IN ('ldc','linuxdo','linux_do','linuxdo_credit')) AND NOT(LOWER(COALESCE(payment_provider,''))='epay' AND LOWER(COALESCE(payment_method,''))='epay' AND (UPPER(COALESCE(settlement_currency,''))<>'CNY' OR (expected_amount_micros<=0 AND settled_amount_micros<=0))) AND (COALESCE(payment_provider,'') IN ('epay','stripe','creem','waffo','waffo_pancake') OR (COALESCE(payment_provider,'')='' AND payment_method IN ('stripe','creem','waffo','waffo_pancake','alipay','wxpay'))) AND (credited_quota>0 OR amount>0))")
        .bind(user_id).bind(top_up_id).fetch_one(&mut **tx).await.map_err(storage)?;
    let inviter_id: i64 = invitee.try_get("inviter_id").map_err(storage)?;
    let email: String = invitee.try_get("email").map_err(storage)?;
    if prior
        || inviter_id <= 0
        || inviter_id == user_id
        || invitee.try_get::<i64, _>("status").map_err(storage)? != 1
        || disposable_email(&email)
    {
        return Ok(());
    }
    let values = options_tx(tx).await?;
    let reward = option_quota(&values, "QuotaForInviter", 0);
    if !compliance_confirmed(&values)
        || reward <= 0
        || credited_quota < option_quota(&values, "ReferralMinTopUpQuota", 0)
    {
        return Ok(());
    }
    let inviter = sqlx::query("SELECT COALESCE(status,0)::bigint AS status,COALESCE(email,'') AS email,COALESCE(stripe_customer,'') AS stripe_customer,COALESCE(aff_quota,0)::bigint AS aff_quota,COALESCE(aff_history,0)::bigint AS aff_history FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
        .bind(inviter_id).fetch_optional(&mut **tx).await.map_err(storage)?;
    let Some(inviter) = inviter else {
        return Ok(());
    };
    let inviter_email: String = inviter.try_get("email").map_err(storage)?;
    let customer: String = invitee.try_get("stripe_customer").map_err(storage)?;
    let inviter_customer: String = inviter.try_get("stripe_customer").map_err(storage)?;
    if inviter.try_get::<i64, _>("status").map_err(storage)? != 1
        || disposable_email(&inviter_email)
        || (!email.is_empty() && email.trim().eq_ignore_ascii_case(inviter_email.trim()))
        || (!customer.is_empty() && customer == inviter_customer)
    {
        return Ok(());
    }
    let maximum = option_quota(&values, "ReferralMaxRewardQuota", 0);
    let quota = if maximum > 0 {
        reward.min(maximum)
    } else {
        reward
    };
    let quota = quota
        .min(
            MAX_WALLET_QUOTA
                - inviter
                    .try_get::<i64, _>("aff_quota")
                    .map_err(storage)?
                    .max(0),
        )
        .min(
            MAX_WALLET_QUOTA
                - inviter
                    .try_get::<i64, _>("aff_history")
                    .map_err(storage)?
                    .max(0),
        );
    if quota <= 0 {
        return Ok(());
    }
    let penalty = option_quota(&values, "ReferralPenaltyPercent", 20);
    let penalty = if penalty > 100 { 20 } else { penalty };
    let reward_id: i64 = sqlx::query_scalar("INSERT INTO referral_rewards(invitee_id,inviter_id,top_up_id,quota,status,penalty_percent,max_penalty_quota,created_at) VALUES($1,$2,$3,$4,'earned',$5,$6,$7) RETURNING id::bigint")
        .bind(user_id).bind(inviter_id).bind(top_up_id).bind(quota).bind(penalty)
        .bind(option_quota(&values, "ReferralMaxPenaltyQuota", 0)).bind(now)
        .fetch_one(&mut **tx).await.map_err(storage)?;
    let updated = sqlx::query("UPDATE users SET aff_quota=aff_quota+$2,aff_history=aff_history+$2 WHERE id=$1 AND aff_quota>=$3 AND aff_quota<=$4")
        .bind(inviter_id).bind(quota).bind(-MAX_WALLET_QUOTA).bind(MAX_WALLET_QUOTA-quota)
        .execute(&mut **tx).await.map_err(storage)?.rows_affected();
    if updated != 1 {
        return Err(TopupError::Provider);
    }
    sqlx::query("INSERT INTO referral_ledger_entries(reward_id,user_id,event_key,kind,quota,reason,created_at) VALUES($1,$2,$3,'reward',$4,'first_top_up',$5)")
        .bind(reward_id).bind(inviter_id).bind(format!("referral:{reward_id}:0:reward")).bind(quota).bind(now)
        .execute(&mut **tx).await.map_err(storage)?;
    Ok(())
}

fn financial_method(method: &str) -> bool {
    !matches!(
        method.trim().to_ascii_lowercase().as_str(),
        "balance"
            | "ldc"
            | "gift"
            | "bonus"
            | "checkin"
            | "invite"
            | "bounty"
            | "linuxdo"
            | "linux_do"
            | "linuxdo_credit"
            | "internal"
            | "admin"
    )
}

fn disposable_email(email: &str) -> bool {
    let email = email.trim().to_ascii_lowercase();
    let Some((local, domain)) = email.rsplit_once('@') else {
        return false;
    };
    !local.is_empty()
        && matches!(
            domain,
            "10minutemail.com"
                | "disposablemail.com"
                | "emailondeck.com"
                | "fakeinbox.com"
                | "getnada.com"
                | "guerrillamail.com"
                | "maildrop.cc"
                | "mailinator.com"
                | "sharklasers.com"
                | "tempmail.com"
                | "temp-mail.org"
                | "yopmail.com"
        )
}

fn option_quota(options: &BTreeMap<String, String>, key: &str, fallback: i64) -> i64 {
    options
        .get(key)
        .and_then(|value| value.trim().parse().ok())
        .filter(|value| (0..=MAX_WALLET_QUOTA).contains(value))
        .unwrap_or(fallback)
}

pub(crate) fn compliance_confirmed(options: &BTreeMap<String, String>) -> bool {
    options
        .get("payment_setting.compliance_confirmed")
        .is_some_and(|value| value.eq_ignore_ascii_case("true") || value == "1")
        && options
            .get("payment_setting.compliance_terms_version")
            .is_some_and(|value| value == "v1")
}

pub(crate) async fn options(pg: &PgPool) -> Result<BTreeMap<String, String>, TopupError> {
    sqlx::query_as::<_, (String, String)>("SELECT key,COALESCE(value,'') FROM options")
        .fetch_all(pg)
        .await
        .map(|rows| rows.into_iter().collect())
        .map_err(storage)
}

async fn options_tx(
    tx: &mut Transaction<'_, Postgres>,
) -> Result<BTreeMap<String, String>, TopupError> {
    sqlx::query_as::<_, (String, String)>("SELECT key,COALESCE(value,'') FROM options")
        .fetch_all(&mut **tx)
        .await
        .map(|rows| rows.into_iter().collect())
        .map_err(storage)
}

pub(crate) fn monetary_micros(value: &str) -> Result<i64, TopupError> {
    let value = Decimal::from_str_exact(value.trim()).map_err(|_| TopupError::Provider)?;
    let value = value
        .checked_mul(Decimal::from(MICROS))
        .ok_or(TopupError::Provider)?;
    if value <= Decimal::ZERO || !value.fract().is_zero() {
        return Err(TopupError::Provider);
    }
    value.to_i64().ok_or(TopupError::Provider)
}

pub(crate) fn now_seconds() -> Result<i64, TopupError> {
    i64::try_from(
        std::time::SystemTime::now()
            .duration_since(std::time::UNIX_EPOCH)
            .map_err(|_| TopupError::Storage)?
            .as_secs(),
    )
    .map_err(|_| TopupError::Storage)
}

fn storage(_: sqlx::Error) -> TopupError {
    TopupError::Storage
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn epay_signature_matches_go_sdk_fixture_and_preserves_raw_bytes() {
        let fields = BTreeMap::from([
            (b"a".to_vec(), b"1".to_vec()),
            (b"b".to_vec(), b"two + &".to_vec()),
            (b"empty".to_vec(), vec![]),
            (b"sign".to_vec(), b"ignored".to_vec()),
            (b"sign_type".to_vec(), b"MD5".to_vec()),
        ]);
        assert_eq!(
            epay_signature(&fields, b"secret"),
            "686a3250646153a1d697ef488bad6be4"
        );
        let mut bytes = fields.clone();
        bytes.insert(b"raw".to_vec(), vec![0xff]);
        assert_ne!(
            epay_signature(&bytes, b"secret"),
            epay_signature(&fields, b"secret")
        );
        assert_eq!(
            epay_signature(&bytes, b"secret"),
            md5_hex(b"a=1&b=two + &&raw=\xffsecret")
        );
    }

    #[test]
    fn settlement_money_must_be_positive_exact_and_fit_int64() {
        assert_eq!(monetary_micros(" 1.000001 ").unwrap(), 1_000_001);
        assert_eq!(monetary_micros("1.0000010").unwrap(), 1_000_001);
        for invalid in [
            "0",
            "-1",
            "1.0000001",
            "NaN",
            "1e99",
            "9223372036854.775808",
        ] {
            assert!(monetary_micros(invalid).is_err(), "{invalid}");
        }
    }

    #[test]
    fn signing_rejects_tampering_and_missing_secrets() {
        let mut settings = BTreeMap::from([
            ("PayAddress".into(), "https://epay.example/base/".into()),
            ("EpayId".into(), "merchant".into()),
            ("EpayKey".into(), "test-secret".into()),
            ("ServerAddress".into(), "https://console.example/".into()),
            ("PayMethods".into(), "[{\"type\":\"alipay\"}]".into()),
            ("payment_setting.compliance_confirmed".into(), "true".into()),
            (
                "payment_setting.compliance_terms_version".into(),
                "v1".into(),
            ),
        ]);
        let config = EpayConfiguration::from_options(&settings).unwrap();
        let order = PendingTopup {
            trade_no: "USR42NOtest".into(),
            user_id: 42,
            amount: 1,
            money: "1.00".into(),
            payment_method: "alipay".into(),
            provider: EPAY.into(),
            requested_amount: Decimal::ONE,
            snapshot: SettlementSnapshot::default(),
        };
        let checkout = config.checkout(&order, "TUC1".into()).unwrap();
        assert_eq!(checkout.url, "https://epay.example/base/submit.php");
        let mut fields = EpayCallbackFields::default();
        for (key, value) in checkout.data.as_object().unwrap() {
            fields.insert_first(
                key.as_bytes().to_vec(),
                value.as_str().unwrap().as_bytes().to_vec(),
            );
        }
        assert!(config.verify(&fields).unwrap().verified);
        fields.0.insert(b"money".to_vec(), b"100.00".to_vec());
        assert!(config.verify(&fields).is_err());
        settings.insert("EpayKey".into(), String::new());
        assert!(EpayConfiguration::from_options(&settings).is_err());
    }
}
