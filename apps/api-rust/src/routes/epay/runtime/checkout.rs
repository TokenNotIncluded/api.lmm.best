//! Current Go ePay pricing, per-method access and coupon reservation rules.

use super::*;
use crate::public_credit_units::{CREDITS_PER_USD, PublicCreditDenomination};
use rust_decimal::RoundingStrategy;

type Method = BTreeMap<String, String>;

fn message(value: impl Into<String>) -> TopupError {
    TopupError::Message(value.into())
}
fn invalid() -> TopupError {
    message("支付方式配置无效")
}
fn decimal(raw: &str) -> Result<Decimal, TopupError> {
    Decimal::from_str_exact(raw)
        .or_else(|_| Decimal::from_scientific(raw))
        .map_err(|_| invalid())
}
fn mul(a: Decimal, b: Decimal) -> Result<Decimal, TopupError> {
    a.checked_mul(b).ok_or_else(invalid)
}
fn div(a: Decimal, b: Decimal) -> Result<Decimal, TopupError> {
    // shopspring/decimal defaults to 16 decimal places for division.
    a.checked_div(b)
        .map(|v| v.round_dp_with_strategy(16, RoundingStrategy::MidpointAwayFromZero))
        .ok_or_else(invalid)
}
fn round(value: Decimal, digits: u32) -> Decimal {
    value.round_dp_with_strategy(digits, RoundingStrategy::MidpointAwayFromZero)
}
fn opt_decimal(
    values: &BTreeMap<String, String>,
    key: &str,
    fallback: &str,
) -> Result<Decimal, TopupError> {
    let value = decimal(values.get(key).map(String::as_str).unwrap_or(fallback))?;
    if value <= Decimal::ZERO {
        return Err(invalid());
    }
    Ok(value)
}
fn methods(values: &BTreeMap<String, String>) -> Result<Vec<Method>, TopupError> {
    match values.get("PayMethods") {
        Some(value) => serde_json::from_str::<Option<Vec<Method>>>(value)
            .map(Option::unwrap_or_default)
            .map_err(|_| invalid()),
        None => Ok(vec![]),
    }
}
fn field<'a>(map: &'a Method, key: &str) -> &'a str {
    map.get(key).map(String::as_str).unwrap_or("")
}
fn user_text<'a>(user: &'a Value, key: &str) -> &'a str {
    user.get(key).and_then(Value::as_str).unwrap_or("")
}
fn user_i64(user: &Value, key: &str) -> i64 {
    user.get(key).and_then(Value::as_i64).unwrap_or(0)
}

impl PgEpayRepository {
    pub(super) async fn current_minimum(&self) -> Result<i64, TopupError> {
        let values = options(&self.pg).await?;
        let minimum = values
            .get("MinTopUp")
            .and_then(|raw| raw.parse::<i64>().ok())
            .unwrap_or(1);
        if values
            .get("general_setting.quota_display_type")
            .is_some_and(|value| value == "TOKENS")
        {
            let quota = match opt_decimal(&values, "QuotaPerUnit", "500000") {
                Ok(v) => v,
                Err(_) => return Ok(MAX_WALLET_QUOTA),
            };
            Ok(mul(Decimal::from(minimum), quota)
                .ok()
                .and_then(|v| v.trunc().to_i64())
                .filter(|v| (0..=MAX_WALLET_QUOTA).contains(v))
                .unwrap_or(MAX_WALLET_QUOTA))
        } else {
            Ok(minimum)
        }
    }

    pub(super) async fn current_method_allowed(&self, method: &str) -> Result<bool, TopupError> {
        let values = options(&self.pg).await?;
        let methods = methods(&values)?;
        Ok(methods.iter().any(|entry| field(entry, "type") == method)
            && method_enabled(&methods, method)?)
    }

    pub(super) async fn quote_current(
        &self,
        input: CreateTopup,
    ) -> Result<QuotedTopup, TopupError> {
        self.quote_currency(input, None)
            .await
            .map(|(quote, _)| quote)
    }

    pub(crate) async fn quote_stripe(
        &self,
        input: CreateTopup,
    ) -> Result<(QuotedTopup, PublicCreditDenomination), TopupError> {
        let (quote, denomination) = self.quote_currency(input, Some("USD")).await?;
        Ok((
            quote,
            denomination.ok_or_else(|| message("定价货币单位不可用"))?,
        ))
    }

    async fn quote_currency(
        &self,
        input: CreateTopup,
        dedicated_currency: Option<&str>,
    ) -> Result<(QuotedTopup, Option<PublicCreditDenomination>), TopupError> {
        let values = options(&self.pg).await?;
        let denomination = dedicated_currency
            .map(|_| PublicCreditDenomination::from_options(&values))
            .transpose()
            .map_err(|_| message("定价货币单位不可用"))?;
        let methods = methods(&values)?;
        let user: Value = sqlx::query_scalar(
            "SELECT to_jsonb(u) FROM users u WHERE id=$1 AND deleted_at IS NULL",
        )
        .bind(input.user_id)
        .fetch_optional(&self.pg)
        .await
        .map_err(storage)?
        .ok_or_else(|| message("获取用户信息失败"))?;
        let now = now_seconds()?;
        validate_access(&methods, &input.payment_method, &user, now)?;
        if dedicated_currency.is_some() {
            let mut minimum = Decimal::from(
                values
                    .get("StripeMinTopUp")
                    .and_then(|raw| raw.parse::<i64>().ok())
                    .unwrap_or(1),
            );
            if values
                .get("general_setting.quota_display_type")
                .is_some_and(|value| value == "TOKENS")
            {
                minimum = mul(
                    minimum,
                    opt_decimal(&values, "QuotaPerUnit", "500000")?.trunc(),
                )?;
            }
            let minimum = minimum.trunc().to_i64().unwrap_or(MAX_WALLET_QUOTA);
            if input.amount < Decimal::from(minimum) {
                return Err(message(format!("充值数量不能小于 {minimum}")));
            }
        }
        let default_method = Method::new();
        let method = if dedicated_currency.is_some() {
            &default_method
        } else {
            methods
                .iter()
                .find(|method| field(method, "type") == input.payment_method)
                .ok_or_else(invalid)?
        };
        let quota_per_unit = opt_decimal(&values, "QuotaPerUnit", "500000")
            .map_err(|_| message("充值额度配置无效"))?;
        if quota_per_unit != Decimal::from(CREDITS_PER_USD) {
            return Err(message("充值额度配置无效"));
        }
        if input.amount <= Decimal::ZERO || input.amount.normalize().scale() > 6 {
            return Err(message("充值数量最多支持 6 位小数"));
        }
        let tokens = values
            .get("general_setting.quota_display_type")
            .is_some_and(|value| value == "TOKENS");
        // Raw TOKENS credits are integers, not money micros. Current Go floors
        // only the legacy batch conversion and checks wallet bounds before
        // creating the compatibility platform-micros projection.
        let quota = if tokens {
            if !input.amount.fract().is_zero() {
                return Err(message("CREDIT 必须为整数"));
            }
            input.amount
        } else {
            mul(input.amount, quota_per_unit)?.floor()
        };
        let quota = quota
            .to_i64()
            .filter(|quota| (1..=MAX_WALLET_QUOTA).contains(quota))
            .ok_or_else(|| message("充值额度超出系统可表示范围"))?;
        let platform_amount = if tokens {
            div(input.amount, quota_per_unit)?
        } else {
            input.amount
        };
        let cny_per_usd = opt_decimal(&values, "USDExchangeRate", "7.3")
            .map_err(|_| message("充值汇率配置无效"))?;
        // A legacy batch is 500,000 raw credits, hence exactly one USD.
        // Custom gateways may provide their own explicit settlement rate.
        let platform_per_usd = Decimal::ONE;
        // FX converts the actual payment currency, never the credit basis.
        // Use the floored raw grant for quote limits and standard USD/CNY prices.
        let amount_usd = div(Decimal::from(quota), Decimal::from(CREDITS_PER_USD))?;
        validate_limits(&methods, &input.payment_method, amount_usd)?;
        if dedicated_currency.is_some() && input.amount > Decimal::from(10_000) {
            return Err(message("充值数量不能大于 10000"));
        }
        let currency = match dedicated_currency {
            Some(currency) => currency.to_owned(),
            None => settlement_currency(method, &input.payment_method)?,
        };
        let stored_amount = platform_amount
            .trunc()
            .to_i64()
            .ok_or_else(|| message("充值数量超出系统可表示范围"))?;
        let platform_amount_micros = monetary_micros(&platform_amount.to_string())
            .map_err(|_| message("平台充值数量最多支持 6 位小数"))?;
        let current_quota = user_i64(&user, "quota");
        if !(-MAX_WALLET_QUOTA..=MAX_WALLET_QUOTA - quota).contains(&current_quota) {
            return Err(message("充值后余额将超过账户额度上限"));
        }
        let base = settlement_amount(
            method,
            &input.payment_method,
            &currency,
            amount_usd,
            platform_amount,
            cny_per_usd,
            platform_per_usd,
        )?;
        let group_ratios: BTreeMap<String, f64> = values
            .get("TopupGroupRatio")
            .and_then(|raw| serde_json::from_str(raw).ok())
            .unwrap_or_default();
        let group = user_text(&user, "group");
        let group_ratio = group_ratios
            .get(group)
            .copied()
            .filter(|ratio| *ratio != 0.0)
            .unwrap_or(1.0);
        let group_ratio = decimal(&group_ratio.to_string())?;
        let method_ratio = match method.get("topup_ratio") {
            Some(raw) => positive_rate(raw)?,
            None => Decimal::ONE,
        };
        let discounts: BTreeMap<i64, f64> = values
            .get("payment_setting.amount_discount")
            .and_then(|raw| serde_json::from_str(raw).ok())
            .unwrap_or_default();
        let discount = if input.amount.fract().is_zero() {
            input
                .amount
                .to_i64()
                .and_then(|amount| discounts.get(&amount).copied())
                .filter(|value| *value > 0.0)
                .unwrap_or(1.0)
        } else {
            1.0
        };
        let mut money = round(
            mul(
                mul(mul(base, group_ratio)?, method_ratio)?,
                decimal(&discount.to_string())?,
            )?,
            2,
        );
        let mut snapshot = SettlementSnapshot {
            platform_amount_micros,
            credited_quota: quota,
            expected_amount_micros: 0,
            settlement_currency: currency,
            discount_code_id: 0,
            discount_percent: 0,
        };
        let coupon_result: Result<(), TopupError> = async {
        if !input.discount_code.trim().is_empty() {
            let code = sqlx::query("SELECT to_jsonb(c) AS code FROM discount_codes c WHERE code=$1 AND deleted_at IS NULL")
                .bind(input.discount_code.trim().to_ascii_uppercase()).fetch_optional(&self.pg).await.map_err(storage)?.ok_or_else(invalid)?;
            let code: Value = code.try_get("code").map_err(storage)?;
            validate_coupon(&code, input.amount, input.user_id, now)?;
            let code_id = user_i64(&code, "id");
            let active: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM discount_code_reservations WHERE discount_code_id=$1 AND status='reserved' AND expires_time>$2")
                .bind(code_id).bind(now).fetch_one(&self.pg).await.map_err(storage)?;
            coupon_capacity(&code, active)?;
            let percent = user_i64(&code, "discount_percent");
            money = round(
                mul(
                    money,
                    div(
                        Decimal::from(100_i64.checked_sub(percent).ok_or_else(invalid)?),
                        Decimal::from(100),
                    )?,
                )?,
                2,
            );
            snapshot.discount_code_id = code_id;
            snapshot.discount_percent = percent;
        }
        Ok(())
        }.await;
        coupon_result.map_err(|error| {
            if dedicated_currency.is_some() {
                message("优惠码无效")
            } else {
                error
            }
        })?;
        if money < Decimal::new(1, 2) {
            return Err(message("充值金额过低"));
        }
        snapshot.expected_amount_micros =
            monetary_micros(&money.to_string()).map_err(|_| message("支付金额无效"))?;
        Ok((
            QuotedTopup {
                user_id: input.user_id,
                requested_amount: input.amount,
                amount_unit: if tokens { "CREDIT" } else { "LEGACY" },
                stored_amount,
                money: format!("{money:.2}"),
                payment_method: input.payment_method,
                provider: input.provider,
                snapshot,
            },
            denomination,
        ))
    }

    pub(super) async fn persist_pending(&self, order: PendingTopup) -> Result<(), TopupError> {
        let snapshot = &order.snapshot;
        if !matches!(order.provider.as_str(), EPAY | "stripe")
            || order.trade_no.trim().is_empty()
            || order.user_id <= 0
            || monetary_micros(&order.money)? != snapshot.expected_amount_micros
            || snapshot.platform_amount_micros <= 0
            || !(1..=MAX_WALLET_QUOTA).contains(&snapshot.credited_quota)
            || snapshot.settlement_currency.is_empty()
        {
            return Err(TopupError::Provider);
        }
        let mut tx = self.pg.begin().await.map_err(storage)?;
        let now = now_seconds()?;
        if snapshot.discount_code_id > 0 {
            let code: Value = sqlx::query_scalar("SELECT to_jsonb(c) FROM discount_codes c WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
                .bind(snapshot.discount_code_id).fetch_optional(&mut *tx).await.map_err(storage)?.ok_or_else(invalid)?;
            validate_coupon(
                &code,
                Decimal::new(snapshot.platform_amount_micros, 6),
                order.user_id,
                now,
            )?;
            let active: i64 = sqlx::query_scalar("SELECT COUNT(*) FROM discount_code_reservations WHERE discount_code_id=$1 AND status='reserved' AND expires_time>$2")
                .bind(snapshot.discount_code_id).bind(now).fetch_one(&mut *tx).await.map_err(storage)?;
            coupon_capacity(&code, active)?;
            sqlx::query("INSERT INTO discount_code_reservations(discount_code_id,top_up_trade_no,user_id,status,expires_time,created_time,updated_time) VALUES($1,$2,$3,'reserved',$4,$5,$5)")
                .bind(snapshot.discount_code_id).bind(&order.trade_no).bind(order.user_id).bind(now+86400).bind(now)
                .execute(&mut *tx).await.map_err(storage)?;
        }
        sqlx::query("INSERT INTO top_ups(user_id,amount,money,trade_no,payment_method,payment_provider,create_time,status,platform_amount_micros,credited_quota,expected_amount_micros,settlement_currency,discount_code_id,discount_percent) VALUES($1,$2,CAST($3 AS NUMERIC),$4,$5,$6,$7,'pending',$8,$9,$10,$11,$12,$13)")
            .bind(order.user_id).bind(order.amount).bind(&order.money).bind(&order.trade_no).bind(&order.payment_method).bind(&order.provider).bind(now)
            .bind(snapshot.platform_amount_micros).bind(snapshot.credited_quota).bind(snapshot.expected_amount_micros).bind(&snapshot.settlement_currency)
            .bind(snapshot.discount_code_id).bind(snapshot.discount_percent).execute(&mut *tx).await.map_err(storage)?;
        tx.commit().await.map_err(storage)?;
        Ok(())
    }
}

fn positive_rate(raw: &str) -> Result<Decimal, TopupError> {
    let valid = !raw.is_empty()
        && raw.bytes().all(|c| c.is_ascii_digit() || c == b'.')
        && !raw.starts_with('.')
        && !raw.ends_with('.')
        && raw.matches('.').count() <= 1;
    let rate = decimal(raw)?;
    if !valid || rate <= Decimal::ZERO {
        return Err(invalid());
    }
    Ok(rate)
}

fn settlement_currency(method: &Method, name: &str) -> Result<String, TopupError> {
    let mut currency = field(method, "settlement_currency")
        .trim()
        .to_ascii_uppercase();
    if currency.is_empty() {
        currency = field(method, "settlement_unit").trim().to_ascii_uppercase();
    }
    let display_name = field(method, "name").trim().to_ascii_lowercase();
    if name.eq_ignore_ascii_case("epay")
        && currency.is_empty()
        && ["ldc", "linuxdo", "linux do"]
            .iter()
            .any(|marker| display_name.contains(marker))
    {
        return Err(invalid());
    }
    if matches!(name, "alipay" | "wxpay") {
        return Ok("CNY".into());
    }
    if currency.is_empty() {
        currency = "CNY".into();
    }
    if currency.len() > 16
        || !currency
            .bytes()
            .all(|c| c.is_ascii_alphanumeric() || b"._-".contains(&c))
    {
        return Err(invalid());
    }
    Ok(currency)
}

fn settlement_amount(
    method: &Method,
    name: &str,
    currency: &str,
    amount_usd: Decimal,
    legacy_amount: Decimal,
    cny_per_usd: Decimal,
    platform_per_usd: Decimal,
) -> Result<Decimal, TopupError> {
    let explicit = [
        "platform_units_per_usd",
        "settlement_units_per_usd",
        "settlement_units_per_platform_unit",
        "unit_price",
    ]
    .iter()
    .any(|key| method.contains_key(*key));
    if matches!(name, "alipay" | "wxpay") || !explicit {
        let rate = match currency {
            "CNY" => cny_per_usd,
            "USD" => Decimal::ONE,
            _ => return Err(invalid()),
        };
        return mul(amount_usd, rate);
    }
    let platform = method.get("platform_units_per_usd");
    let settlement = method.get("settlement_units_per_usd");
    let direct = method.get("settlement_units_per_platform_unit");
    let legacy = method.get("unit_price");
    if platform.is_some() && settlement.is_none()
        || settlement.is_some() && (direct.is_some() || legacy.is_some())
    {
        return Err(invalid());
    }
    if let Some(settlement) = settlement {
        let platform = platform
            .map(|raw| positive_rate(raw))
            .transpose()?
            .unwrap_or(platform_per_usd);
        return mul(div(legacy_amount, platform)?, positive_rate(settlement)?);
    }
    let rate = positive_rate(direct.or(legacy).ok_or_else(invalid)?)?;
    if let (Some(_), Some(legacy)) = (direct, legacy)
        && positive_rate(legacy)? != rate
    {
        return Err(invalid());
    }
    mul(legacy_amount, rate)
}

fn validate_limits(methods: &[Method], name: &str, amount_usd: Decimal) -> Result<(), TopupError> {
    let mut minimum: Option<Decimal> = None;
    let mut maximum: Option<Decimal> = None;
    for method in methods
        .iter()
        .filter(|entry| field(entry, "type").trim() == name.trim())
    {
        let min = field(method, "min_topup").trim();
        let max = field(method, "max_topup").trim();
        if !min.is_empty() {
            let value = if min.bytes().all(|c| c == b'0' || c == b'.')
                && !min.starts_with('.')
                && !min.ends_with('.')
                && min.matches('.').count() <= 1
            {
                Decimal::ZERO
            } else {
                positive_rate(min)?
            };
            minimum = Some(minimum.map_or(value, |current| current.max(value)));
        }
        if !max.is_empty() {
            let value = positive_rate(max)?;
            maximum = Some(maximum.map_or(value, |current| current.min(value)));
        }
    }
    if let Some(value) = minimum
        && amount_usd < value
    {
        return Err(message(format!(
            "该支付方式单笔最少充值 {} 美元到账余额",
            value.normalize()
        )));
    }
    if let Some(value) = maximum
        && amount_usd > value
    {
        return Err(message(format!(
            "该支付方式单笔最多充值 {} 美元到账余额",
            value.normalize()
        )));
    }
    Ok(())
}

fn method_enabled(methods: &[Method], name: &str) -> Result<bool, TopupError> {
    for method in methods
        .iter()
        .filter(|entry| field(entry, "type").trim() == name.trim())
    {
        match field(method, "enabled").trim() {
            "" | "1" | "t" | "T" | "TRUE" | "true" | "True" => {}
            "0" | "f" | "F" | "FALSE" | "false" | "False" => return Ok(false),
            _ => return Err(invalid()),
        }
    }
    Ok(true)
}

fn validate_access(
    methods: &[Method],
    name: &str,
    user: &Value,
    now: i64,
) -> Result<(), TopupError> {
    if !method_enabled(methods, name)? {
        return Err(message("该支付方式不可用"));
    }
    let mut explicit = false;
    for method in methods
        .iter()
        .filter(|entry| field(entry, "type").trim() == name.trim())
    {
        let days = field(method, "unlock_after_days").trim();
        if !days.is_empty() {
            let days = days
                .parse::<i64>()
                .ok()
                .filter(|v| *v >= 0)
                .ok_or_else(invalid)?;
            if days > 0 {
                let created = user_i64(user, "created_at");
                let unlock = days
                    .checked_mul(86400)
                    .and_then(|duration| created.checked_add(duration))
                    .filter(|_| created > 0)
                    .ok_or_else(invalid)?;
                if now < unlock {
                    return Err(message("该支付方式尚未解锁"));
                }
            }
        }
        let mode = field(method, "audience_mode").trim().to_ascii_lowercase();
        if mode.is_empty() || mode == "legacy" {
            continue;
        }
        explicit = true;
        if !matches!(mode.as_str(), "all" | "include" | "exclude") {
            return Err(invalid());
        }
        let matching = field(method, "audience_match").trim().to_ascii_lowercase();
        if !matches!(matching.as_str(), "" | "any" | "all") {
            return Err(invalid());
        }
        let mut conditions = vec![];
        let email = field(method, "audience_email_contains")
            .trim()
            .to_ascii_lowercase();
        if !email.is_empty() {
            conditions.push(
                user_text(user, "email")
                    .trim()
                    .to_ascii_lowercase()
                    .contains(&email),
            );
        }
        let provider = field(method, "audience_oauth_provider")
            .trim()
            .to_ascii_lowercase()
            .replace('.', "");
        if !provider.is_empty() {
            let key = match provider.as_str() {
                "linuxdo" => "linux_do_id",
                "github" => "github_id",
                "discord" => "discord_id",
                "oidc" => "oidc_id",
                "wechat" => "wechat_id",
                "telegram" => "telegram_id",
                _ => return Err(invalid()),
            };
            conditions.push(!user_text(user, key).trim().is_empty());
        }
        let score = |key: &str| -> Result<Option<f64>, TopupError> {
            let raw = field(method, key).trim();
            if raw.is_empty() {
                Ok(None)
            } else {
                raw.parse::<f64>()
                    .ok()
                    .filter(|v| v.is_finite() && *v >= 0.0)
                    .map(Some)
                    .ok_or_else(invalid)
            }
        };
        let min = score("audience_linuxdo_score_min")?;
        let max = score("audience_linuxdo_score_max")?;
        if let (Some(min), Some(max)) = (min, max)
            && min > max
        {
            return Err(invalid());
        }
        if min.is_some() || max.is_some() {
            let value = if user_i64(user, "linux_do_score_updated_at") > 0 {
                user.get("linux_do_gamification_score")
                    .and_then(Value::as_f64)
            } else if user_i64(user, "payment_restriction_flags") & 2 != 0 {
                Some(10001.0)
            } else {
                None
            };
            conditions.push(value.is_some_and(|value| {
                min.is_none_or(|min| value >= min) && max.is_none_or(|max| value <= max)
            }));
        }
        let groups = field(method, "audience_user_group").trim();
        if !groups.is_empty() {
            let group = user_text(user, "group").trim();
            conditions.push(
                !group.is_empty()
                    && groups
                        .split(',')
                        .any(|value| value.trim().eq_ignore_ascii_case(group)),
            );
        }
        let role = field(method, "audience_role").trim().to_ascii_lowercase();
        if !role.is_empty() && role != "none" {
            let value = user_i64(user, "role");
            conditions.push(match role.as_str() {
                "common" => value == 1,
                "admin" => (10..100).contains(&value),
                "root" => value >= 100,
                _ => return Err(invalid()),
            });
        }
        if mode == "all" {
            continue;
        }
        if conditions.is_empty() {
            return Err(invalid());
        }
        let matches = if matching == "all" {
            conditions.iter().all(|v| *v)
        } else {
            conditions.iter().any(|v| *v)
        };
        if (mode == "include" && !matches) || (mode == "exclude" && matches) {
            return Err(message("该支付方式不可用"));
        }
    }
    let linuxdo_email = user_text(user, "email")
        .trim()
        .rsplit_once('@')
        .is_some_and(|(local, domain)| {
            !local.is_empty() && domain.eq_ignore_ascii_case("linux.do")
        });
    if !explicit && (user_i64(user, "payment_restriction_flags") != 0 || linuxdo_email) {
        return Err(message("该支付方式不可用"));
    }
    Ok(())
}

/// Subscription checkout uses the same method audience/unlock policy as
/// wallet checkout, without applying wallet minimums, coupons or multipliers.
pub(crate) fn validate_configured_payment_access(
    values: &BTreeMap<String, String>,
    method: &str,
    user: &Value,
    now: i64,
) -> Result<(), TopupError> {
    validate_access(&methods(values)?, method, user, now)
}

fn validate_coupon(
    code: &Value,
    amount: Decimal,
    user_id: i64,
    now: i64,
) -> Result<(), TopupError> {
    if user_i64(code, "status") != 1
        || user_i64(code, "starts_time") > now
        || (user_i64(code, "expired_time") > 0 && user_i64(code, "expired_time") < now)
        || amount < Decimal::from(user_i64(code, "min_amount"))
        || (user_i64(code, "owner_user_id") != 0 && user_i64(code, "owner_user_id") != user_id)
    {
        return Err(invalid());
    }
    Ok(())
}
fn coupon_capacity(code: &Value, active: i64) -> Result<(), TopupError> {
    let max = user_i64(code, "max_uses");
    let used = user_i64(code, "used_count");
    if max > 0 && used.checked_add(active).is_none_or(|total| total >= max) {
        return Err(invalid());
    }
    Ok(())
}
