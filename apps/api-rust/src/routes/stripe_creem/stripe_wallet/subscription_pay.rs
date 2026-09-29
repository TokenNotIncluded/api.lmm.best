//! Native recurring checkout: price evidence, durable purchased snapshot, then
//! external session creation. Provider failures retain the local pending order.

use super::*;
use crate::routes::epay::runtime::validate_configured_payment_access;
use crate::routes::stripe_creem::stripe_provider::StripeSubscriptionRequest;

impl StripeWalletState {
    pub(crate) async fn subscription_pay(&self, user_id: i64, request: Request) -> Response {
        // Go PaymentMethodAccessGate loads the audience profile before the
        // critical limiter and compliance/body processing.
        let user: Value = match sqlx::query_scalar(
            "SELECT to_jsonb(u) FROM users u WHERE id=$1 AND deleted_at IS NULL",
        )
        .bind(user_id)
        .fetch_optional(&self.pg)
        .await
        {
            Ok(Some(user)) => user,
            _ => {
                return (
                    StatusCode::INTERNAL_SERVER_ERROR,
                    Json(json!({
                        "success":false,"message":"Unable to verify payment access."
                    })),
                )
                    .into_response();
            }
        };
        let ip = request
            .extensions()
            .get::<ClientIpKey>()
            .map(|v| v.0.clone())
            .or_else(|| {
                request
                    .extensions()
                    .get::<RequestContext>()
                    .and_then(|v| v.client_ip)
                    .map(|v| v.to_string())
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
            Err(_) => return crate::legacy_empty_response(StatusCode::INTERNAL_SERVER_ERROR, None),
        }
        let values = match options(&self.pg).await {
            Ok(values) => values,
            Err(_) => return rejected("系统错误"),
        };
        if !compliance_confirmed(&values) {
            return rejected(crate::routes::billing_subscriptions::compliance_message(
                request.headers(),
            ));
        }
        let bytes = match to_bytes(request.into_body(), 16 * 1024).await {
            Ok(bytes) => bytes,
            Err(_) => return rejected("参数错误"),
        };
        let input =
            match SubscriptionInput::deserialize(&mut serde_json::Deserializer::from_slice(&bytes))
            {
                Ok(input) if input.plan_id > 0 => input,
                _ => return rejected("参数错误"),
            };
        let raw: Value =
            match sqlx::query_scalar("SELECT to_jsonb(p) FROM subscription_plans p WHERE id=$1")
                .bind(input.plan_id)
                .fetch_optional(&self.pg)
                .await
            {
                Ok(Some(plan)) => plan,
                Ok(None) => return rejected("record not found"),
                Err(_) => return rejected("系统错误"),
            };
        let plan = purchased_snapshot(&raw);
        if !plan["enabled"].as_bool().unwrap_or(false) {
            return rejected("套餐未启用");
        }
        let price_id = plan["stripe_price_id"].as_str().unwrap_or("");
        let api = values
            .get("StripeApiSecret")
            .map(String::as_str)
            .unwrap_or("");
        let webhook = values
            .get("StripeWebhookSecret")
            .map(String::as_str)
            .unwrap_or("");
        let now = match now_seconds() {
            Ok(now) => now,
            Err(_) => return rejected("系统错误"),
        };
        if price_id.trim().is_empty()
            || !(api.trim().starts_with("sk_") || api.trim().starts_with("rk_"))
            || webhook.trim().is_empty()
            || validate_configured_payment_access(&values, "stripe", &user, now).is_err()
        {
            return rejected("该支付方式不可用于此套餐");
        }
        if !(api.starts_with("sk_") || api.starts_with("rk_")) {
            return rejected("Stripe 未配置或密钥无效");
        }
        let maximum = plan["max_purchase_per_user"].as_i64().unwrap_or(0);
        if maximum > 0 {
            let count: i64 = match sqlx::query_scalar(
                "SELECT count(*) FROM user_subscriptions WHERE user_id=$1 AND plan_id=$2",
            )
            .bind(user_id)
            .bind(input.plan_id)
            .fetch_one(&self.pg)
            .await
            {
                Ok(count) => count,
                Err(_) => return rejected("系统错误"),
            };
            if count >= maximum {
                return rejected("已达到该套餐购买上限");
            }
        }
        let amount = match settlement_amount(&plan, &values) {
            Some(amount) => amount,
            None => return rejected("套餐结算金额或币种无效"),
        };
        let secret = SecretString::from(api.to_owned());
        let price = match self
            .provider
            .recurring_price(&secret, price_id.trim())
            .await
        {
            Ok(price) => price,
            Err(_) => return rejected("Stripe 套餐价格读取失败"),
        };
        if price.id.trim() != price_id.trim()
            || !price.active
            || !price.recurring
            || !price.currency.trim().eq_ignore_ascii_case("USD")
            || minor_to_micros(price.unit_amount, "USD").ok() != Some(amount)
        {
            return rejected("Stripe 套餐价格与本地套餐不一致");
        }
        let trade = format!("sub_{}", legacy_trade_no("sub-stripe-ref", user_id));
        // One INSERT is the transaction boundary used by Go. No provider POST
        // occurs until this immutable order is committed and callback-visible.
        if sqlx::query("INSERT INTO subscription_orders(user_id,plan_id,money,trade_no,payment_method,payment_provider,create_time,status,plan_snapshot,expected_amount_micros,settlement_currency,provider_product_id) VALUES($1,$2,CAST($3 AS NUMERIC),$4,'stripe','stripe',$5,'pending',$6,$7,'USD',$8)")
            .bind(user_id).bind(input.plan_id).bind(plan["price_amount"].to_string())
            .bind(&trade).bind(now).bind(plan.to_string()).bind(amount).bind(price_id.trim())
            .execute(&self.pg).await.is_err()
        {
            return legacy("error", "创建订单失败");
        }
        let server = values
            .get("ServerAddress")
            .map(String::as_str)
            .unwrap_or("")
            .trim_end_matches('/');
        let input = StripeSubscriptionRequest {
            trade_no: trade,
            price_id: price_id.to_owned(),
            customer: text(&user, "stripe_customer"),
            email: text(&user, "email"),
            return_url: format!("{server}/wallet"),
        };
        match self
            .provider
            .create_subscription_checkout(&secret, &input)
            .await
        {
            Ok(url) => legacy("success", json!({"pay_link":url})),
            Err(_) => legacy("error", "拉起支付失败"),
        }
    }
}

fn rejected(message: &str) -> Response {
    Json(json!({"success":false,"message":message})).into_response()
}

fn settlement_amount(plan: &Value, values: &BTreeMap<String, String>) -> Option<i64> {
    let rate = |key: &str, fallback: &str| {
        let v = values.get(key).map(String::as_str).unwrap_or(fallback);
        Decimal::from_str_exact(v)
            .or_else(|_| Decimal::from_scientific(v))
            .ok()
            .filter(|v| *v > Decimal::ZERO)
    };
    let cny = rate("USDExchangeRate", "7.3")?;
    rate("TopUpPlatformUnitsPerCNY", "1")?;
    let amount = Decimal::from_str_exact(&plan["price_amount"].as_f64()?.to_string()).ok()?;
    if amount <= Decimal::ZERO {
        return None;
    }
    let currency = plan["currency"].as_str()?.trim().to_ascii_uppercase();
    let converted = match currency.as_str() {
        "USD" => amount,
        "CNY" | "" => amount
            .checked_div(cny)?
            .round_dp_with_strategy(16, RoundingStrategy::MidpointAwayFromZero),
        _ => return None,
    }
    .round_dp_with_strategy(2, RoundingStrategy::MidpointAwayFromZero);
    converted
        .checked_mul(Decimal::from(1_000_000))?
        .to_i64()
        .filter(|v| *v > 0)
}

fn purchased_snapshot(raw: &Value) -> Value {
    let mut plan = serde_json::Map::new();
    for key in [
        "title",
        "subtitle",
        "currency",
        "duration_unit",
        "stripe_price_id",
        "creem_product_id",
        "waffo_pancake_product_id",
        "upgrade_group",
        "downgrade_group",
        "quota_reset_period",
    ] {
        plan.insert(key.into(), json!(raw[key].as_str().unwrap_or("")));
    }
    for key in [
        "id",
        "price_currency_version",
        "duration_value",
        "custom_seconds",
        "archived_at",
        "sort_order",
        "max_purchase_per_user",
        "total_amount",
        "quota_reset_custom_seconds",
        "created_at",
        "updated_at",
    ] {
        plan.insert(key.into(), json!(raw[key].as_i64().unwrap_or(0)));
    }
    let price = raw["price_amount"].as_f64().unwrap_or(0.0).to_string();
    plan.insert(
        "price_amount".into(),
        serde_json::from_str(&price).unwrap_or(json!(0)),
    );
    plan.insert(
        "enabled".into(),
        json!(raw["enabled"].as_bool().unwrap_or(false)),
    );
    for key in ["allow_balance_pay", "allow_wallet_overflow"] {
        plan.insert(key.into(), json!(raw[key].as_bool().unwrap_or(true)));
    }
    let pancake = raw["waffo_pancake_product_type"].as_str().unwrap_or("");
    plan.insert(
        "waffo_pancake_product_type".into(),
        json!(if pancake.trim().eq_ignore_ascii_case("one_time") {
            "one_time"
        } else {
            "subscription"
        }),
    );
    Value::Object(plan)
}

#[derive(Default)]
struct SubscriptionInput {
    plan_id: i64,
}

impl<'de> Deserialize<'de> for SubscriptionInput {
    fn deserialize<D: serde::Deserializer<'de>>(deserializer: D) -> Result<Self, D::Error> {
        struct Visitor;
        impl<'de> serde::de::Visitor<'de> for Visitor {
            type Value = SubscriptionInput;
            fn expecting(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
                f.write_str("a subscription request")
            }
            fn visit_unit<E: serde::de::Error>(self) -> Result<Self::Value, E> {
                Ok(SubscriptionInput::default())
            }
            fn visit_map<A: serde::de::MapAccess<'de>>(
                self,
                mut fields: A,
            ) -> Result<Self::Value, A::Error> {
                let mut value = SubscriptionInput::default();
                while let Some(key) = fields.next_key::<String>()? {
                    if key.eq_ignore_ascii_case("plan_id") {
                        if let Some(plan_id) = fields.next_value::<Option<i64>>()? {
                            value.plan_id = plan_id;
                        }
                    } else {
                        fields.next_value::<serde::de::IgnoredAny>()?;
                    }
                }
                Ok(value)
            }
        }
        deserializer.deserialize_any(Visitor)
    }
}
