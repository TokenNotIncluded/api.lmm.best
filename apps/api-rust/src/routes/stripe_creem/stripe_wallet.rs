//! Native Stripe wallet checkout and authenticated PostgreSQL settlement.
//! Subscription events retain retry semantics until their durable
//! processors are composed; a valid signature is never itself an acknowledgement.

mod refund;
mod subscription;
mod subscription_pay;

use super::{
    stripe_provider::{
        StripeApiClient, StripeSessionRequest, minor_to_micros, verify_stripe_event,
    },
    *,
};
use crate::routes::epay::runtime::{
    MAX_WALLET_QUOTA, compliance_confirmed, consume_discount, grant_referral, monetary_micros,
    now_seconds, options,
};
use crate::routes::epay::{
    CreateTopup, PendingTopup as WalletOrder, PgEpayRepository, SettlementSnapshot,
    TopupAuthorizer, TopupError, TopupRepository,
};
use crate::{ClientIpKey, RequestContext, auth::CriticalRateLimitOutcome};
use rust_decimal::RoundingStrategy;

fn stripe_credit_fields(snapshot: &SettlementSnapshot, amount_unit: &str) -> Value {
    json!({
        "currency_unit": "credit",
        "amount_unit": amount_unit,
        "credited_quota": snapshot.credited_quota,
        "credit_amount": snapshot.credited_quota,
        "legacy_batch_units": Decimal::new(snapshot.platform_amount_micros, 6).normalize().to_string(),
        "settlement_currency": snapshot.settlement_currency.trim().to_ascii_uppercase(),
    })
}

#[derive(Clone)]
pub struct StripeWalletState {
    pg: PgPool,
    wallet: PgEpayRepository,
    provider: StripeApiClient,
    critical: Arc<dyn TopupAuthorizer>,
}

impl StripeWalletState {
    #[must_use]
    pub fn new(
        pg: PgPool,
        valkey: redis::Client,
        critical: Arc<dyn TopupAuthorizer>,
        provider: StripeApiClient,
    ) -> Self {
        Self {
            wallet: PgEpayRepository::new(pg.clone()).with_valkey(valkey),
            pg,
            provider,
            critical,
        }
    }

    pub(super) async fn quote(&self, user_id: i64, request: Request) -> Response {
        self.checkout_or_quote(user_id, request, false, &[]).await
    }

    pub(super) async fn pay(
        &self,
        user_id: i64,
        request: Request,
        trusted_redirect_domains: &[String],
    ) -> Response {
        self.checkout_or_quote(user_id, request, true, trusted_redirect_domains)
            .await
    }

    async fn checkout_or_quote(
        &self,
        user_id: i64,
        request: Request,
        pay: bool,
        trusted_redirect_domains: &[String],
    ) -> Response {
        if pay {
            let ip = request
                .extensions()
                .get::<ClientIpKey>()
                .map(|value| value.0.clone())
                .or_else(|| {
                    request
                        .extensions()
                        .get::<RequestContext>()
                        .and_then(|value| value.client_ip)
                        .map(|ip| ip.to_string())
                });
            let Some(ip) = ip else {
                return crate::legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None);
            };
            match self.critical.check_critical_rate_limit(&ip).await {
                Ok(CriticalRateLimitOutcome::Allowed) => {}
                Ok(CriticalRateLimitOutcome::Rejected {
                    retry_after_seconds,
                }) => {
                    return crate::legacy_empty_response(
                        StatusCode::TOO_MANY_REQUESTS,
                        Some(retry_after_seconds),
                    );
                }
                Err(_) => {
                    return crate::legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None);
                }
            }
        }
        let input: LiveRequest = match legacy_json(request).await {
            Ok(value) => value,
            Err(_) => return legacy("error", "参数错误"),
        };
        if pay && input.payment_method != "stripe" {
            return legacy("error", "不支持的支付渠道");
        }
        let amount=match Decimal::from_str_exact(&input.amount.to_string()){
            Ok(amount) if amount>Decimal::ZERO&&amount.normalize().scale()<=6&&monetary_micros(&amount.to_string()).is_ok()=>amount,
            _=>return Json(json!({"success":false,"message":if input.amount<=0.0{"充值数量无效"}else{"充值数量最多支持 6 位小数"}})).into_response(),
        };
        let mut quote = match self
            .wallet
            .quote_stripe(CreateTopup {
                user_id,
                amount,
                payment_method: "stripe".into(),
                provider: "stripe",
                discount_code: input.discount_code,
            })
            .await
        {
            Ok(quote) => quote,
            Err(TopupError::Message(message))
                if pay
                    && (message.starts_with("充值数量不能小于")
                        || message == "充值数量不能大于 10000") =>
            {
                return legacy(&message, 10);
            }
            Err(TopupError::Message(message)) => return legacy("error", message),
            Err(_) => return legacy("error", "获取用户分组失败"),
        };
        if quote.snapshot.expected_amount_micros <= 10_000 {
            return legacy("error", "充值金额过低");
        }
        if !pay {
            let mut response = stripe_credit_fields(&quote.snapshot, quote.amount_unit);
            response["message"] = json!("success");
            response["data"] = json!(quote.money);
            return Json(response).into_response();
        }
        for (redirect, label) in [
            (&input.success_url, "支付成功重定向URL不在可信任域名列表中"),
            (&input.cancel_url, "支付取消重定向URL不在可信任域名列表中"),
        ] {
            if !redirect_allowed(redirect, trusted_redirect_domains) {
                return (
                    StatusCode::BAD_REQUEST,
                    Json(json!({"message":label,"data":""})),
                )
                    .into_response();
            }
        }
        let values = match options(&self.pg).await {
            Ok(values) => values,
            Err(_) => return legacy("error", "拉起支付失败"),
        };
        if !compliance_confirmed(&values) {
            return legacy("error", "支付合规声明尚未确认");
        }
        let secret = SecretString::from(values.get("StripeApiSecret").cloned().unwrap_or_default());
        let price_id = values.get("StripePriceId").cloned().unwrap_or_default();
        // Never create a payable checkout that this listener cannot verify.
        if values
            .get("StripeWebhookSecret")
            .is_none_or(|secret| secret.trim().is_empty())
        {
            return legacy("error", "拉起支付失败");
        }
        let price = match self.provider.price(&secret, &price_id).await {
            Ok(price) => price,
            Err(_) => return legacy("error", "拉起支付失败"),
        };
        if super::stripe_provider::micros_to_minor(
            quote.snapshot.expected_amount_micros,
            &price.currency,
        )
        .is_err()
        {
            return legacy("error", "支付金额无效");
        }
        quote.snapshot.settlement_currency = price.currency.clone();
        let amount_unit = quote.amount_unit;
        let user=match sqlx::query("SELECT COALESCE(email,'') AS email,COALESCE(stripe_customer,'') AS customer FROM users WHERE id=$1 AND deleted_at IS NULL").bind(user_id).fetch_optional(&self.pg).await {
            Ok(Some(user))=>user,_=>return legacy("error","用户不存在"),
        };
        let order = WalletOrder {
            trade_no: legacy_trade_no("new-api-ref", user_id),
            user_id,
            amount: quote.stored_amount,
            money: quote.money,
            payment_method: "stripe".into(),
            provider: "stripe".into(),
            requested_amount: quote.requested_amount,
            snapshot: quote.snapshot,
        };
        if self
            .wallet
            .insert_prepared_pending(order.clone())
            .await
            .is_err()
        {
            return legacy("error", "创建订单失败");
        }
        let server = values
            .get("ServerAddress")
            .map(String::as_str)
            .unwrap_or("")
            .trim_end_matches('/');
        let session = StripeSessionRequest {
            trade_no: order.trade_no.clone(),
            expected_amount_micros: order.snapshot.expected_amount_micros,
            customer: user.try_get("customer").unwrap_or_default(),
            email: user.try_get("email").unwrap_or_default(),
            success_url: if input.success_url.is_empty() {
                format!("{server}/usage-logs")
            } else {
                input.success_url
            },
            cancel_url: if input.cancel_url.is_empty() {
                format!("{server}/wallet")
            } else {
                input.cancel_url
            },
            allow_promotion_codes: values
                .get("StripePromotionCodesEnabled")
                .is_some_and(|value| value == "true"),
        };
        match self
            .provider
            .create_checkout(&secret, &price, &session)
            .await
        {
            Ok(url) => {
                let mut data = stripe_credit_fields(&order.snapshot, amount_unit);
                data["pay_link"] = json!(url);
                data["trade_no"] = json!(order.trade_no);
                legacy("success", data)
            }
            // Provider acceptance may precede a transport error. The committed
            // order and coupon reservation must remain available to callbacks.
            Err(_) => legacy("error", "拉起支付失败"),
        }
    }

    pub async fn webhook(&self, request: Request) -> Response {
        let caller_ip = request
            .extensions()
            .get::<ClientIpKey>()
            .map(|value| value.0.clone())
            .unwrap_or_default();
        let values = match options(&self.pg).await {
            Ok(values) => values,
            Err(_) => return StatusCode::FORBIDDEN.into_response(),
        };
        let secret = values
            .get("StripeWebhookSecret")
            .map(String::as_str)
            .unwrap_or("");
        let api = values
            .get("StripeApiSecret")
            .map(String::as_str)
            .unwrap_or("");
        let wallet_enabled = !api.trim().is_empty()
            && values
                .get("StripePriceId")
                .is_some_and(|value| !value.trim().is_empty());
        let subscription_enabled = api.starts_with("sk_") || api.starts_with("rk_");
        if !compliance_confirmed(&values)
            || secret.trim().is_empty()
            || !(wallet_enabled || subscription_enabled)
        {
            return StatusCode::FORBIDDEN.into_response();
        }
        let signature = request
            .headers()
            .get("stripe-signature")
            .and_then(|value| value.to_str().ok())
            .unwrap_or("")
            .to_owned();
        let body = match to_bytes(request.into_body(), MAX_LEGACY_TOPUP_BODY_BYTES).await {
            Ok(body) => body,
            Err(_) => return StatusCode::SERVICE_UNAVAILABLE.into_response(),
        };
        let event = match verify_stripe_event(
            &body,
            &signature,
            &SecretString::from(secret.to_owned()),
            SystemTime::now(),
        ) {
            Ok(event) => event,
            Err(_) => return StatusCode::BAD_REQUEST.into_response(),
        };
        let kind = event.get("type").and_then(Value::as_str).unwrap_or("");
        let object = event.pointer("/data/object").unwrap_or(&Value::Null);
        let trade = text(object, "client_reference_id");
        let result = match kind {
            "checkout.session.completed"
                if text(object, "status") == "complete"
                    && text(object, "payment_status") == "paid" =>
            {
                self.settle(&event, &caller_ip).await
            }
            "checkout.session.async_payment_succeeded" => self.settle(&event, &caller_ip).await,
            "checkout.session.expired" if text(object, "status") == "expired" => {
                self.change_pending(&trade, "expired", true).await
            }
            "checkout.session.async_payment_failed" => {
                self.change_pending(&trade, "failed", false).await
            }
            "invoice.paid" | "invoice.payment_succeeded" => {
                self.subscription_invoice(&event, true).await
            }
            "invoice.payment_failed" => self.subscription_invoice(&event, false).await,
            "customer.subscription.updated" | "customer.subscription.deleted" => {
                self.subscription_state(&event).await
            }
            "refund.created" | "refund.updated" => self.refund(&event).await,
            _ => Ok(()),
        };
        match result {
            Ok(()) | Err(SettlementError::Conflict) => StatusCode::OK.into_response(),
            Err(_) => StatusCode::INTERNAL_SERVER_ERROR.into_response(),
        }
    }

    async fn change_pending(
        &self,
        trade: &str,
        status: &str,
        release_coupon: bool,
    ) -> Result<(), SettlementError> {
        if trade.is_empty() {
            return Ok(());
        }
        let mut tx = self.pg.begin().await.map_err(db)?;
        if subscription_exists(&mut tx, trade).await? {
            tx.rollback().await.map_err(db)?;
            return if status == "expired" {
                self.subscription_expire(trade).await
            } else {
                Ok(())
            };
        }
        let affected=sqlx::query("UPDATE top_ups SET status=$2 WHERE trade_no=$1 AND payment_provider='stripe' AND status='pending'").bind(trade).bind(status).execute(&mut *tx).await.map_err(db)?.rows_affected();
        if affected > 0 && release_coupon {
            sqlx::query("UPDATE discount_code_reservations SET status='released',updated_time=$2 WHERE top_up_trade_no=$1 AND status='reserved'").bind(trade).bind(now_seconds().map_err(|_|SettlementError::Storage)?).execute(&mut *tx).await.map_err(db)?;
        }
        tx.commit().await.map_err(db)
    }

    async fn settle(&self, event: &Value, caller_ip: &str) -> Result<(), SettlementError> {
        let object = event
            .pointer("/data/object")
            .ok_or(SettlementError::Conflict)?;
        let trade = text(object, "client_reference_id");
        if trade.is_empty() {
            return Ok(());
        }
        // Subscription checkouts have their own evidence contract and need
        // not contain wallet subtotal fields. Dispatch before wallet parsing.
        let mut tx = self.pg.begin().await.map_err(db)?;
        if subscription_exists(&mut tx, &trade).await? {
            tx.rollback().await.map_err(db)?;
            return self.subscription_checkout(event).await;
        }
        let currency = text(object, "currency").to_ascii_uppercase();
        let number = |key| {
            object
                .get(key)
                .and_then(|value| {
                    value
                        .as_i64()
                        .or_else(|| value.as_str().and_then(|value| value.parse().ok()))
                })
                .ok_or(SettlementError::Conflict)
        };
        let amount = minor_to_micros(number("amount_total")?, &currency)
            .map_err(|_| SettlementError::Conflict)?;
        let subtotal = minor_to_micros(number("amount_subtotal")?, &currency)
            .map_err(|_| SettlementError::Conflict)?;
        let event_id = text(event, "id");
        let mut transaction = text(object, "payment_intent");
        if transaction.is_empty() {
            transaction = text(object, "id");
        }
        if event_id.is_empty() && transaction.is_empty() {
            return Err(SettlementError::Storage);
        }
        let customer = text(object, "customer");
        let row=sqlx::query("SELECT id::bigint,user_id::bigint,amount,money::text AS money,status,payment_provider,settlement_currency,expected_amount_micros,credited_quota,settled_amount_micros,provider_event_id,provider_transaction_id,provider_product_id,provider_store_id,COALESCE(discount_code_id,0)::bigint AS discount_code_id,referral_excluded FROM top_ups WHERE trade_no=$1 FOR UPDATE")
            .bind(trade.trim()).fetch_optional(&mut *tx).await.map_err(db)?.ok_or(SettlementError::Conflict)?;
        let order_id: i64 = row.try_get("id").map_err(db)?;
        let user_id: i64 = row.try_get("user_id").map_err(db)?;
        if row.try_get::<String, _>("payment_provider").map_err(db)? != "stripe" {
            return Err(SettlementError::Conflict);
        }
        let money: String = row.try_get("money").map_err(db)?;
        let mut expected: i64 = row.try_get("expected_amount_micros").map_err(db)?;
        if expected <= 0 {
            expected = Decimal::from_str_exact(&money)
                .ok()
                .and_then(|value| value.checked_mul(Decimal::from(1_000_000)))
                .and_then(|value| {
                    value
                        .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
                        .to_i64()
                })
                .unwrap_or(0);
        }
        let stored_currency: String = row.try_get("settlement_currency").map_err(db)?;
        if expected <= 0
            || subtotal != expected
            || amount > subtotal
            || (!stored_currency.is_empty() && !stored_currency.eq_ignore_ascii_case(&currency))
            || !row
                .try_get::<String, _>("provider_product_id")
                .map_err(db)?
                .is_empty()
            || !row
                .try_get::<String, _>("provider_store_id")
                .map_err(db)?
                .is_empty()
        {
            return Err(SettlementError::Conflict);
        }
        let bound:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM top_ups WHERE id<>$1 AND payment_provider='stripe' AND (($2<>'' AND provider_event_id=$2) OR ($3<>'' AND provider_transaction_id=$3)))")
            .bind(order_id).bind(&event_id).bind(&transaction).fetch_one(&mut *tx).await.map_err(db)?;
        if bound {
            return Err(SettlementError::Conflict);
        }
        let status: String = row.try_get("status").map_err(db)?;
        let mut quota: i64 = row.try_get("credited_quota").map_err(db)?;
        if !(-MAX_WALLET_QUOTA..=MAX_WALLET_QUOTA).contains(&quota) {
            return Err(SettlementError::Storage);
        }
        if status == "success" {
            let prior_amount: i64 = row.try_get("settled_amount_micros").map_err(db)?;
            let prior_event: Option<String> = row.try_get("provider_event_id").map_err(db)?;
            let prior_transaction: Option<String> =
                row.try_get("provider_transaction_id").map_err(db)?;
            if prior_amount != 0 && prior_amount != amount
                || prior_event
                    .as_deref()
                    .is_some_and(|value| !value.trim().is_empty() && value.trim() != event_id)
                || prior_transaction
                    .as_deref()
                    .is_some_and(|value| !value.trim().is_empty() && value.trim() != transaction)
            {
                return Err(SettlementError::Conflict);
            }
        } else if status != "pending" {
            return Err(SettlementError::Conflict);
        } else {
            if quota <= 0 {
                let configured: Option<String> =
                    sqlx::query_scalar("SELECT value FROM options WHERE key='QuotaPerUnit'")
                        .fetch_optional(&mut *tx)
                        .await
                        .map_err(db)?;
                let per_unit = Decimal::from_str_exact(configured.as_deref().unwrap_or("500000"))
                    .map_err(|_| SettlementError::Storage)?;
                quota = Decimal::from(row.try_get::<i64, _>("amount").map_err(db)?)
                    .checked_mul(per_unit)
                    .and_then(|value| {
                        value
                            .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
                            .to_i64()
                    })
                    .filter(|value| (1..=MAX_WALLET_QUOTA).contains(value))
                    .ok_or(SettlementError::Storage)?;
            }
            let changed=sqlx::query("UPDATE users SET quota=quota+$2,stripe_customer=CASE WHEN $3='' THEN stripe_customer ELSE $3 END WHERE id=$1 AND deleted_at IS NULL AND quota>=$4 AND quota<=$5")
                .bind(user_id).bind(quota).bind(&customer).bind(-MAX_WALLET_QUOTA).bind(MAX_WALLET_QUOTA-quota).execute(&mut *tx).await.map_err(db)?.rows_affected();
            if changed != 1 {
                return Err(SettlementError::Storage);
            }
        }
        let now = now_seconds().map_err(|_| SettlementError::Storage)?;
        sqlx::query("UPDATE top_ups SET status='success',credited_quota=$2,expected_amount_micros=$3,settled_amount_micros=$4,settlement_currency=$5,provider_event_id=NULLIF($6,''),provider_transaction_id=NULLIF($7,''),payment_method='stripe',complete_time=CASE WHEN status='pending' THEN $8 ELSE complete_time END WHERE id=$1")
            .bind(order_id).bind(quota).bind(expected).bind(amount).bind(&currency).bind(&event_id).bind(&transaction).bind(now).execute(&mut *tx).await.map_err(db)?;
        if status == "pending" {
            if !row.try_get::<bool, _>("referral_excluded").map_err(db)? {
                grant_referral(&mut tx, order_id, user_id, quota, now)
                    .await
                    .map_err(|_| SettlementError::Storage)?;
            }
            consume_discount(
                &mut tx,
                &trade,
                row.try_get("discount_code_id").map_err(db)?,
                user_id,
                now,
            )
            .await
            .map_err(|_| SettlementError::Storage)?;
        }
        tx.commit().await.map_err(db)?;
        self.wallet.invalidate_user(user_id).await;
        // Go Stripe logs each compatible delivery (including replay), while
        // the ledger credit, coupon and referral grant remain exactly once.
        let _ = record_stripe_log(&self.pg, user_id, quota, &money, caller_ip).await;
        Ok(())
    }
}

async fn subscription_exists(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    trade: &str,
) -> Result<bool, SettlementError> {
    sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM subscription_orders WHERE trade_no=$1)")
        .bind(trade)
        .fetch_one(&mut **tx)
        .await
        .map_err(db)
}

#[derive(Debug)]
enum SettlementError {
    Conflict,
    Storage,
}
fn db(error: sqlx::Error) -> SettlementError {
    if error
        .as_database_error()
        .is_some_and(|error| error.code().as_deref() == Some("23505"))
    {
        SettlementError::Conflict
    } else {
        SettlementError::Storage
    }
}
fn text(value: &Value, key: &str) -> String {
    value
        .get(key)
        .and_then(Value::as_str)
        .unwrap_or("")
        .to_owned()
}

async fn record_stripe_log(
    pg: &PgPool,
    user: i64,
    quota: i64,
    money: &str,
    ip: &str,
) -> Result<(), sqlx::Error> {
    use crate::routes::topup::{format_quota, node_name, server_ip, service_version};
    let username: String =
        sqlx::query_scalar("SELECT COALESCE(username,'') FROM users WHERE id=$1")
            .bind(user)
            .fetch_optional(pg)
            .await?
            .unwrap_or_default();
    let quota_text = format_quota(pg, quota, false).await;
    let content = format!(
        "使用在线充值成功，充值金额: {quota_text}，支付金额：{:.2}",
        money.parse::<f64>().unwrap_or(0.0)
    );
    let other=json!({"admin_info":{"server_ip":server_ip(),"node_name":node_name(),"caller_ip":ip,"payment_method":"stripe","callback_payment_method":"stripe","version":service_version()}}).to_string();
    sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username,token_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,channel_id,token_id,\"group\",ip,other) VALUES($1,$2,1,$3,$4,'','',0,0,0,0,false,0,0,'',$5,$6)")
        .bind(user).bind(now_seconds().unwrap_or(0)).bind(content).bind(username).bind(ip).bind(other).execute(pg).await?;
    Ok(())
}

pub fn webhook_router(state: StripeWalletState) -> Router {
    async fn handle(State(state): State<StripeWalletState>, request: Request) -> Response {
        state.webhook(request).await
    }
    crate::routes::billing_payments::stripe_webhook_surface(handle).with_state(state)
}

#[derive(Default, Deserialize)]
struct LiveRequest {
    #[serde(default, deserialize_with = "null_f64_is_zero")]
    amount: f64,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    payment_method: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    discount_code: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    success_url: String,
    #[serde(default, deserialize_with = "null_string_is_empty")]
    cancel_url: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn credit_metadata_preserves_the_priced_snapshot_and_request_unit() {
        let legacy = SettlementSnapshot {
            platform_amount_micros: 14_600_000,
            credited_quota: 7_300_000,
            settlement_currency: "usd".into(),
            ..Default::default()
        };
        assert_eq!(
            stripe_credit_fields(&legacy, "LEGACY"),
            json!({
                "currency_unit": "credit", "amount_unit": "LEGACY",
                "credited_quota": 7_300_000, "credit_amount": 7_300_000,
                "legacy_batch_units": "14.6", "settlement_currency": "USD",
            })
        );
        let raw = SettlementSnapshot {
            platform_amount_micros: 1_200_002,
            credited_quota: 600_001,
            settlement_currency: "USD".into(),
            ..Default::default()
        };
        assert_eq!(
            stripe_credit_fields(&raw, "CREDIT"),
            json!({
                "currency_unit": "credit", "amount_unit": "CREDIT",
                "credited_quota": 600_001, "credit_amount": 600_001,
                "legacy_batch_units": "1.200002", "settlement_currency": "USD",
            })
        );
    }
}
