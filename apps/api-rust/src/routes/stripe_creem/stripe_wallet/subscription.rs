//! Stripe's shared webhook also serves purchased subscriptions. Payment
//! receipts, period resets and cancellation are bound to immutable orders.

use super::*;
use crate::routes::billing_subscriptions::provider_subscription_schedule;
use sqlx::{Postgres, Transaction};

#[derive(Default)]
struct Effects {
    user_id: i64,
    purchase: Option<String>,
    renewal: Option<String>,
}

impl StripeWalletState {
    pub(super) async fn subscription_checkout(&self, event: &Value) -> Result<(), SettlementError> {
        let object = event
            .pointer("/data/object")
            .ok_or(SettlementError::Conflict)?;
        let trade = text(object, "client_reference_id");
        let mut tx = self.pg.begin().await.map_err(db)?;
        let (order, plan) = locked_order_and_plan(&mut tx, &trade).await?;
        let amount = minor_to_micros(integer(object, "amount_total")?, &text(object, "currency"))
            .map_err(|_| SettlementError::Conflict)?;
        let subscription = identifier(object.get("subscription"));
        if text(&order, "payment_provider") != "stripe"
            || text(object, "mode") != "subscription"
            || text(object, "payment_status") != "paid"
            || !text(&order, "settlement_currency").eq_ignore_ascii_case(&text(object, "currency"))
            || integer(&order, "expected_amount_micros")? != amount
            || text(&order, "provider_product_id")
                != object
                    .pointer("/metadata/subscription_price_id")
                    .and_then(Value::as_str)
                    .unwrap_or("")
            || subscription.is_empty()
        {
            return Err(SettlementError::Conflict);
        }
        let mut effects = Effects::default();
        complete_order(&mut tx, order, &plan, &object.to_string(), &mut effects).await?;
        tx.commit().await.map_err(db)?;
        self.subscription_effects(effects).await;
        // Match Go's two durable stages: a verified purchase remains granted
        // if provider-identity persistence needs a retry. Reload under the
        // second lock so a concurrent cancellation cannot be overwritten.
        let mut tx = self.pg.begin().await.map_err(db)?;
        let completed = locked_order(&mut tx, &trade).await?;
        let mut effects = Effects::default();
        state_update(
            &mut tx,
            &completed,
            &subscription,
            "active",
            0,
            0,
            0,
            &mut effects,
        )
        .await?;
        tx.commit().await.map_err(db)?;
        self.subscription_effects(effects).await;
        Ok(())
    }

    pub(super) async fn subscription_invoice(
        &self,
        event: &Value,
        paid: bool,
    ) -> Result<(), SettlementError> {
        let invoice = event.pointer("/data/object").unwrap_or(&Value::Null);
        let trade = invoice
            .pointer("/subscription_details/metadata/subscription_trade_no")
            .or_else(|| invoice.pointer("/subscription/metadata/subscription_trade_no"))
            .and_then(Value::as_str)
            .unwrap_or("")
            .trim();
        if trade.is_empty() {
            return Ok(());
        }
        let subscription = identifier(invoice.get("subscription"));
        let mut tx = self.pg.begin().await.map_err(db)?;
        if !subscription_exists(&mut tx, trade).await? {
            return Ok(());
        }
        let (order, plan) = locked_order_and_plan(&mut tx, trade).await?;
        let period = invoice_period(invoice, &text(&order, "provider_product_id"));
        let mut effects = Effects::default();
        if !paid {
            if subscription.is_empty() {
                return Ok(());
            }
            let (start, end) = period.unwrap_or((0, 0));
            state_update(
                &mut tx,
                &order,
                &subscription,
                "past_due",
                start,
                end,
                0,
                &mut effects,
            )
            .await?;
        } else {
            let (start, end) = period?;
            let currency = text(invoice, "currency").trim().to_ascii_uppercase();
            let amount = minor_to_micros(integer(invoice, "total")?, &currency)
                .map_err(|_| SettlementError::Conflict)?;
            if text(&order, "payment_provider") != "stripe"
                || text(invoice, "status") != "paid"
                || !text(&order, "settlement_currency").eq_ignore_ascii_case(&currency)
                || integer(&order, "expected_amount_micros")? != amount
                || subscription.is_empty()
                || text(invoice, "id").trim().is_empty()
            {
                return Err(SettlementError::Conflict);
            }
            complete_order(&mut tx, order, &plan, &invoice.to_string(), &mut effects).await?;
            tx.commit().await.map_err(db)?;
            self.subscription_effects(effects).await;
            // The Go controller completes the order before its recurring
            // receipt transaction. Retrying this stage reuses that one grant.
            tx = self.pg.begin().await.map_err(db)?;
            let mut completed = locked_order(&mut tx, trade).await?;
            let plan = receipt_plan(&mut tx, &completed).await?;
            effects = Effects::default();
            apply_receipt(
                &mut tx,
                &mut completed,
                &plan,
                &text(event, "id"),
                &text(invoice, "id"),
                &subscription,
                &currency,
                amount,
                start,
                end,
                &mut effects,
            )
            .await?;
        }
        tx.commit().await.map_err(db)?;
        self.subscription_effects(effects).await;
        Ok(())
    }

    pub(super) async fn subscription_state(&self, event: &Value) -> Result<(), SettlementError> {
        let object = event.pointer("/data/object").unwrap_or(&Value::Null);
        let trade = object
            .pointer("/metadata/subscription_trade_no")
            .and_then(Value::as_str)
            .unwrap_or("")
            .trim();
        if trade.is_empty() {
            return Ok(());
        }
        let mut state = text(object, "status").trim().to_ascii_lowercase();
        if text(event, "type") == "customer.subscription.deleted" {
            state = "canceled".into();
        } else if object.get("cancel_at_period_end").and_then(Value::as_bool) == Some(true)
            && state == "active"
        {
            state = "canceling".into();
        }
        let mut tx = self.pg.begin().await.map_err(db)?;
        let order = locked_order(&mut tx, trade).await?;
        let mut effects = Effects::default();
        state_update(
            &mut tx,
            &order,
            &text(object, "id"),
            &state,
            integer(object, "current_period_start")?,
            integer(object, "current_period_end")?,
            integer(object, "canceled_at")?,
            &mut effects,
        )
        .await?;
        tx.commit().await.map_err(db)?;
        self.subscription_effects(effects).await;
        Ok(())
    }

    pub(super) async fn subscription_expire(&self, trade: &str) -> Result<(), SettlementError> {
        let mut tx = self.pg.begin().await.map_err(db)?;
        let order = locked_order(&mut tx, trade).await?;
        if text(&order, "payment_provider") != "stripe" {
            return Err(SettlementError::Conflict);
        }
        if text(&order, "status") == "pending" {
            sqlx::query(
                "UPDATE subscription_orders SET status='expired',complete_time=$2 WHERE id=$1",
            )
            .bind(integer(&order, "id")?)
            .bind(now_seconds().map_err(|_| SettlementError::Storage)?)
            .execute(&mut *tx)
            .await
            .map_err(db)?;
        }
        tx.commit().await.map_err(db)
    }

    async fn subscription_effects(&self, effects: Effects) {
        if effects.user_id <= 0 {
            return;
        }
        self.wallet.invalidate_user(effects.user_id).await;
        for content in [effects.purchase, effects.renewal].into_iter().flatten() {
            let _=sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username) SELECT id,$2,1,$3,COALESCE(username,'') FROM users WHERE id=$1")
                .bind(effects.user_id).bind(now_seconds().unwrap_or(0)).bind(content).execute(&self.pg).await;
        }
    }
}

fn integer(value: &Value, key: &str) -> Result<i64, SettlementError> {
    match value.get(key) {
        None | Some(Value::Null) => Ok(0),
        Some(value) => value.as_i64().ok_or(SettlementError::Storage),
    }
}
fn identifier(value: Option<&Value>) -> String {
    value
        .and_then(|value| {
            value
                .as_str()
                .or_else(|| value.get("id").and_then(Value::as_str))
        })
        .unwrap_or("")
        .trim()
        .to_owned()
}
fn terminal(state: &str) -> bool {
    matches!(state, "canceled" | "expired" | "paused" | "unpaid")
}

async fn locked_order(
    tx: &mut Transaction<'_, Postgres>,
    trade: &str,
) -> Result<Value, SettlementError> {
    sqlx::query_scalar("SELECT to_jsonb(o) FROM subscription_orders o WHERE trade_no=$1 FOR UPDATE")
        .bind(trade)
        .fetch_optional(&mut **tx)
        .await
        .map_err(db)?
        .ok_or(SettlementError::Storage)
}

async fn locked_order_and_plan(
    tx: &mut Transaction<'_, Postgres>,
    trade: &str,
) -> Result<(Value, Value), SettlementError> {
    let plan_id: i64 =
        sqlx::query_scalar("SELECT plan_id::bigint FROM subscription_orders WHERE trade_no=$1")
            .bind(trade)
            .fetch_optional(&mut **tx)
            .await
            .map_err(db)?
            .ok_or(SettlementError::Storage)?;
    let current: Value =
        sqlx::query_scalar("SELECT to_jsonb(p) FROM subscription_plans p WHERE id=$1 FOR UPDATE")
            .bind(plan_id)
            .fetch_one(&mut **tx)
            .await
            .map_err(db)?;
    let order = locked_order(tx, trade).await?;
    if integer(&order, "plan_id")? != plan_id {
        return Err(SettlementError::Storage);
    }
    let raw = text(&order, "plan_snapshot");
    let plan = if raw.trim().is_empty() {
        current
    } else {
        let plan: Value = serde_json::from_str(&raw).map_err(|_| SettlementError::Storage)?;
        if integer(&plan, "id")? != plan_id
            || plan.get("price_amount").and_then(Value::as_f64)
                != order.get("money").and_then(Value::as_f64)
        {
            return Err(SettlementError::Storage);
        }
        plan
    };
    Ok((order, plan))
}

async fn receipt_plan(
    tx: &mut Transaction<'_, Postgres>,
    order: &Value,
) -> Result<Value, SettlementError> {
    let snapshot = text(order, "plan_snapshot");
    if !snapshot.trim().is_empty() {
        return serde_json::from_str(&snapshot).map_err(|_| SettlementError::Storage);
    }
    sqlx::query_scalar("SELECT to_jsonb(p) FROM subscription_plans p WHERE id=$1")
        .bind(integer(order, "plan_id")?)
        .fetch_one(&mut **tx)
        .await
        .map_err(db)
}

async fn complete_order(
    tx: &mut Transaction<'_, Postgres>,
    mut order: Value,
    plan: &Value,
    payload: &str,
    effects: &mut Effects,
) -> Result<Value, SettlementError> {
    if text(&order, "payment_provider") != "stripe" {
        return Err(SettlementError::Conflict);
    }
    if text(&order, "status") == "success" {
        return Ok(order);
    }
    if text(&order, "status") != "pending" {
        return Err(SettlementError::Conflict);
    }
    let user_id = integer(&order, "user_id")?;
    let plan_id = integer(&order, "plan_id")?;
    let group: String = sqlx::query_scalar(
        "SELECT COALESCE(\"group\",'') FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(db)?;
    let maximum = integer(plan, "max_purchase_per_user")?;
    if maximum > 0 {
        let count: i64 = sqlx::query_scalar(
            "SELECT COUNT(*) FROM user_subscriptions WHERE user_id=$1 AND plan_id=$2",
        )
        .bind(user_id)
        .bind(plan_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(db)?;
        if count >= maximum {
            return Err(SettlementError::Storage);
        }
    }
    let start: i64 = sqlx::query_scalar("SELECT EXTRACT(EPOCH FROM NOW())::bigint")
        .fetch_one(&mut **tx)
        .await
        .map_err(db)?;
    let (end, next) =
        provider_subscription_schedule(plan, start, None).map_err(|_| SettlementError::Storage)?;
    let upgrade = text(plan, "upgrade_group").trim().to_owned();
    let downgrade = text(plan, "downgrade_group").trim().to_owned();
    let changed = !upgrade.is_empty() && upgrade != group;
    if changed {
        sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
            .bind(user_id)
            .bind(&upgrade)
            .execute(&mut **tx)
            .await
            .map_err(db)?;
    }
    let overflow = match plan.get("allow_wallet_overflow") {
        None | Some(Value::Null) => true,
        Some(value) => value.as_bool().ok_or(SettlementError::Storage)?,
    };
    let sub_id:i64=sqlx::query_scalar("INSERT INTO user_subscriptions(user_id,plan_id,amount_total,amount_used,start_time,end_time,status,source,last_reset_time,next_reset_time,upgrade_group,prev_user_group,downgrade_group,allow_wallet_overflow,created_at,updated_at) VALUES($1,$2,$3,0,$4,$5,'active','order',$6,$7,$8,$9,$10,$11,$4,$4) RETURNING id::bigint")
        .bind(user_id).bind(plan_id).bind(integer(plan,"total_amount")?).bind(start).bind(end).bind(if next>0{start}else{0}).bind(next).bind(&upgrade).bind(if changed{&group}else{""}).bind(&downgrade).bind(overflow).fetch_one(&mut **tx).await.map_err(db)?;
    let expected = integer(&order, "expected_amount_micros")?;
    let money = if expected > 0 {
        Decimal::new(expected, 6).to_string()
    } else {
        order.get("money").unwrap_or(&Value::Null).to_string()
    };
    let trade = text(&order, "trade_no");
    let method = text(&order, "payment_method");
    let currency = text(&order, "settlement_currency")
        .trim()
        .to_ascii_uppercase();
    let existing=sqlx::query("SELECT COALESCE(payment_provider,'') AS provider,COALESCE(payment_method,'') AS method FROM top_ups WHERE trade_no=$1 FOR UPDATE").bind(&trade).fetch_optional(&mut **tx).await.map_err(db)?;
    if let Some(row) = existing {
        let prior_provider: String = row.try_get("provider").map_err(db)?;
        let prior_method: String = row.try_get("method").map_err(db)?;
        if !prior_provider.is_empty() && prior_provider != "stripe"
            || !prior_method.is_empty() && prior_method != method
        {
            return Err(SettlementError::Conflict);
        }
        sqlx::query("UPDATE top_ups SET money=CAST($2 AS numeric),expected_amount_micros=$3,settled_amount_micros=$3,settlement_currency=$4,payment_provider='stripe',payment_method=$5,status='success',complete_time=$6,create_time=CASE WHEN COALESCE(create_time,0)=0 THEN $7 ELSE create_time END WHERE trade_no=$1")
            .bind(&trade).bind(&money).bind(expected).bind(&currency).bind(&method).bind(start).bind(integer(&order,"create_time")?).execute(&mut **tx).await.map_err(db)?;
    } else {
        sqlx::query("INSERT INTO top_ups(user_id,amount,money,trade_no,payment_method,payment_provider,create_time,complete_time,status,expected_amount_micros,settled_amount_micros,settlement_currency) VALUES($1,0,CAST($2 AS numeric),$3,$4,'stripe',$5,$6,'success',$7,$7,$8)")
            .bind(user_id).bind(&money).bind(&trade).bind(&method).bind(integer(&order,"create_time")?).bind(start).bind(expected).bind(&currency).execute(&mut **tx).await.map_err(db)?;
    }
    sqlx::query("UPDATE subscription_orders SET user_subscription_id=$2,status='success',complete_time=$3,provider_payload=CASE WHEN $4='' THEN provider_payload ELSE $4 END WHERE id=$1")
        .bind(integer(&order,"id")?).bind(sub_id).bind(start).bind(payload).execute(&mut **tx).await.map_err(db)?;
    order["user_subscription_id"] = json!(sub_id);
    order["status"] = json!("success");
    effects.user_id = user_id;
    effects.purchase = Some(format!(
        "订阅购买成功，套餐: {}，支付金额: {:.2}，支付方式: {method}",
        text(plan, "title"),
        order.get("money").and_then(Value::as_f64).unwrap_or(0.0)
    ));
    Ok(order)
}

fn invoice_period(invoice: &Value, price: &str) -> Result<(i64, i64), SettlementError> {
    let lines = invoice
        .pointer("/lines/data")
        .and_then(Value::as_array)
        .ok_or(SettlementError::Conflict)?;
    for line in lines {
        let id = identifier(line.get("price"));
        if id.is_empty() || (!price.is_empty() && id != price) {
            continue;
        }
        let Some(period) = line.get("period") else {
            continue;
        };
        let start = integer(period, "start")?;
        let end = integer(period, "end")?;
        if end <= start {
            return Err(SettlementError::Conflict);
        }
        return Ok((start, end));
    }
    Err(SettlementError::Conflict)
}

#[allow(clippy::too_many_arguments)]
async fn apply_receipt(
    tx: &mut Transaction<'_, Postgres>,
    order: &mut Value,
    plan: &Value,
    event: &str,
    invoice: &str,
    provider_subscription: &str,
    currency: &str,
    amount: i64,
    start: i64,
    end: i64,
    effects: &mut Effects,
) -> Result<(), SettlementError> {
    if event.trim().is_empty() || invoice.trim().is_empty() {
        return Err(SettlementError::Storage);
    }
    let order_id = integer(order, "id")?;
    let user_id = integer(order, "user_id")?;
    let sub_id = integer(order, "user_subscription_id")?;
    if text(order, "payment_provider") != "stripe"
        || text(order, "status") != "success"
        || sub_id <= 0
    {
        return Err(SettlementError::Conflict);
    }
    let expected = integer(order, "expected_amount_micros")?;
    if expected > 0 && expected != amount
        || !text(order, "settlement_currency").is_empty()
            && !text(order, "settlement_currency").eq_ignore_ascii_case(currency)
    {
        return Err(SettlementError::Storage);
    }
    let duplicate:Vec<Value>=sqlx::query_scalar("SELECT to_jsonb(e) FROM subscription_payment_events e WHERE provider_event_id=$1 OR (payment_provider='stripe' AND provider_transaction_id=$2) OR (subscription_order_id=$3 AND period_end=$4)")
        .bind(event).bind(invoice).bind(order_id).bind(end).fetch_all(&mut **tx).await.map_err(db)?;
    for receipt in &duplicate {
        if integer(receipt, "subscription_order_id")? == order_id
            && text(receipt, "payment_provider") == "stripe"
            && text(receipt, "provider_transaction_id") == invoice
            && text(receipt, "settlement_currency") == currency
            && integer(receipt, "settlement_amount_micros")? == amount
            && integer(receipt, "period_start")? == start
            && integer(receipt, "period_end")? == end
        {
            return Ok(());
        }
    }
    if !duplicate.is_empty() {
        return Err(SettlementError::Conflict);
    }
    let (count,previous_end):(i64,i64)=sqlx::query_as("SELECT COUNT(*),COALESCE(MAX(period_end),0) FROM subscription_payment_events WHERE subscription_order_id=$1").bind(order_id).fetch_one(&mut **tx).await.map_err(db)?;
    let now = now_seconds().map_err(|_| SettlementError::Storage)?;
    sqlx::query("INSERT INTO subscription_payment_events(subscription_order_id,payment_provider,provider_event_id,provider_transaction_id,settlement_currency,settlement_amount_micros,period_start,period_end,created_time) VALUES($1,'stripe',$2,$3,$4,$5,$6,$7,$8)")
        .bind(order_id).bind(event).bind(invoice).bind(currency).bind(amount).bind(start).bind(end).bind(now).execute(&mut **tx).await.map_err(db)?;
    if count > 0 && end <= previous_end {
        return Ok(());
    }
    let group: String = sqlx::query_scalar(
        "SELECT COALESCE(\"group\",'') FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(db)?;
    let subscription: Value = sqlx::query_scalar(
        "SELECT to_jsonb(s) FROM user_subscriptions s WHERE id=$1 AND user_id=$2 FOR UPDATE",
    )
    .bind(sub_id)
    .bind(user_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(db)?;
    if text(&subscription, "status") == "cancelled"
        && start <= integer(&subscription, "updated_at")?
    {
        return Ok(());
    }
    if end <= now {
        if count == 0 {
            sqlx::query("UPDATE user_subscriptions SET start_time=$2,end_time=$3,status='expired',next_reset_time=0,updated_at=$4 WHERE id=$1").bind(sub_id).bind(start).bind(end).bind(now).execute(&mut **tx).await.map_err(db)?;
            downgrade(tx, &subscription, now).await?;
            effects.user_id = user_id;
        }
        sqlx::query("UPDATE subscription_orders SET provider_subscription_id=$2,current_period_start=$3,current_period_end=$4 WHERE id=$1").bind(order_id).bind(provider_subscription).bind(start).bind(end).execute(&mut **tx).await.map_err(db)?;
        return Ok(());
    }
    let (_, mut next) = provider_subscription_schedule(plan, start, Some(end))
        .map_err(|_| SettlementError::Storage)?;
    if count > 0 {
        if next == 0 || next > end {
            next = end;
        }
        let total = integer(plan, "total_amount")?;
        sqlx::query("UPDATE user_subscriptions SET amount_used=0,quota_version=quota_version+1,amount_total=COALESCE(renewal_amount,reset_amount,CASE WHEN $2>0 THEN $2 ELSE amount_total END),reset_amount=COALESCE(renewal_amount,reset_amount),status='active',last_reset_time=$3,next_reset_time=$4,end_time=$5,updated_at=$6 WHERE id=$1")
            .bind(sub_id).bind(total).bind(start).bind(next).bind(end).bind(now).execute(&mut **tx).await.map_err(db)?;
        let upgrade = text(&subscription, "upgrade_group").trim().to_owned();
        if !upgrade.is_empty() && upgrade != group {
            sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
                .bind(user_id)
                .bind(upgrade)
                .execute(&mut **tx)
                .await
                .map_err(db)?;
        }
        sqlx::query(
            "UPDATE subscription_orders SET refunded_amount_micros=0,refunded_quota=0 WHERE id=$1",
        )
        .bind(order_id)
        .execute(&mut **tx)
        .await
        .map_err(db)?;
        effects.renewal = Some(format!(
            "订阅续费成功，结算金额: {:.2} {currency}",
            amount as f64 / 1_000_000.0
        ));
    } else {
        sqlx::query("UPDATE user_subscriptions SET start_time=$2,end_time=$3,next_reset_time=$4,last_reset_time=CASE WHEN $4>0 THEN $2 ELSE last_reset_time END,updated_at=$5 WHERE id=$1")
            .bind(sub_id).bind(start).bind(end).bind(next).bind(now).execute(&mut **tx).await.map_err(db)?;
    }
    sqlx::query("UPDATE subscription_orders SET provider_subscription_id=$2,provider_subscription_state='active',current_period_start=$3,current_period_end=$4 WHERE id=$1")
        .bind(order_id).bind(provider_subscription).bind(start).bind(end).execute(&mut **tx).await.map_err(db)?;
    effects.user_id = user_id;
    Ok(())
}

#[allow(clippy::too_many_arguments)]
async fn state_update(
    tx: &mut Transaction<'_, Postgres>,
    order: &Value,
    subscription: &str,
    state: &str,
    start: i64,
    end: i64,
    canceled_at: i64,
    effects: &mut Effects,
) -> Result<(), SettlementError> {
    if canceled_at < 0 {
        return Err(SettlementError::Storage);
    }
    if text(order, "payment_provider") != "stripe"
        || subscription.trim().is_empty()
        || (!text(order, "provider_subscription_id").is_empty()
            && text(order, "provider_subscription_id") != subscription.trim())
    {
        return Err(SettlementError::Conflict);
    }
    if integer(order, "provider_event_time_millis")? > 0
        || (end > 0 && end < integer(order, "current_period_end")?)
        || terminal(&text(order, "provider_subscription_state")) && !terminal(state)
    {
        return Ok(());
    }
    sqlx::query("UPDATE subscription_orders SET provider_subscription_id=$2,provider_subscription_state=$3,provider_event_time_millis=0,current_period_start=CASE WHEN COALESCE(current_period_start,0)=0 AND $4>0 THEN $4 ELSE current_period_start END,current_period_end=CASE WHEN COALESCE(current_period_end,0)=0 AND $5>0 THEN $5 ELSE current_period_end END WHERE id=$1")
        .bind(integer(order,"id")?).bind(subscription.trim()).bind(state).bind(start).bind(end).execute(&mut **tx).await.map_err(db)?;
    let sub_id = integer(order, "user_subscription_id")?;
    if !terminal(state) || sub_id <= 0 {
        return Ok(());
    }
    let user_id = integer(order, "user_id")?;
    sqlx::query("SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
        .bind(user_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(db)?;
    let snapshot: Value = sqlx::query_scalar(
        "SELECT to_jsonb(s) FROM user_subscriptions s WHERE id=$1 AND user_id=$2 FOR UPDATE",
    )
    .bind(sub_id)
    .bind(user_id)
    .fetch_one(&mut **tx)
    .await
    .map_err(db)?;
    let now = now_seconds().map_err(|_| SettlementError::Storage)?;
    sqlx::query("UPDATE user_subscriptions SET end_time=CASE WHEN end_time=0 OR end_time>$2 THEN $2 ELSE end_time END,status='cancelled',next_reset_time=0,updated_at=$2 WHERE id=$1")
        .bind(sub_id).bind(now).execute(&mut **tx).await.map_err(db)?;
    downgrade(tx, &snapshot, now).await?;
    effects.user_id = user_id;
    Ok(())
}

async fn downgrade(
    tx: &mut Transaction<'_, Postgres>,
    subscription: &Value,
    now: i64,
) -> Result<(), SettlementError> {
    let upgrade = text(subscription, "upgrade_group").trim().to_owned();
    let explicit = text(subscription, "downgrade_group").trim().to_owned();
    if upgrade.is_empty() && explicit.is_empty() {
        return Ok(());
    }
    let user_id = integer(subscription, "user_id")?;
    let active:bool=sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM user_subscriptions WHERE user_id=$1 AND id<>$2 AND status='active' AND end_time>$3 AND upgrade_group<>'')")
        .bind(user_id).bind(integer(subscription,"id")?).bind(now).fetch_one(&mut **tx).await.map_err(db)?;
    if active {
        return Ok(());
    }
    let group: String = sqlx::query_scalar("SELECT COALESCE(\"group\",'') FROM users WHERE id=$1")
        .bind(user_id)
        .fetch_one(&mut **tx)
        .await
        .map_err(db)?;
    let target = if explicit.is_empty() {
        if group != upgrade {
            return Ok(());
        }
        text(subscription, "prev_user_group").trim().to_owned()
    } else {
        explicit
    };
    if !target.is_empty() && target != group {
        sqlx::query("UPDATE users SET \"group\"=$2 WHERE id=$1")
            .bind(user_id)
            .bind(target)
            .execute(&mut **tx)
            .await
            .map_err(db)?;
    }
    Ok(())
}
