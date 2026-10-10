//! Stripe one-time wallet refunds, including partial refunds and first-payment
//! referral clawback. Every financial effect shares the order transaction.

use super::*;

impl StripeWalletState {
    pub(super) async fn refund(&self, event: &Value) -> Result<(), SettlementError> {
        let object = event
            .pointer("/data/object")
            .ok_or(SettlementError::Conflict)?;
        if text(object, "status") != "succeeded" {
            return Ok(());
        }
        let refund_id = text(object, "id").trim().to_owned();
        let transaction = object
            .get("payment_intent")
            .and_then(|value| {
                value
                    .as_str()
                    .or_else(|| value.get("id").and_then(Value::as_str))
            })
            .unwrap_or("")
            .trim();
        if transaction.is_empty() || refund_id.is_empty() {
            return Err(SettlementError::Conflict);
        }
        let currency = text(object, "currency").trim().to_ascii_uppercase();
        let minor = object
            .get("amount")
            .and_then(Value::as_i64)
            .ok_or(SettlementError::Conflict)?;
        let requested = minor_to_micros(minor, &currency).map_err(|_| SettlementError::Conflict)?;
        if requested > 9_000_000_000_000_000 {
            return Err(SettlementError::Storage);
        }
        let mut tx = self.pg.begin().await.map_err(refund_db)?;
        let row=sqlx::query("SELECT id::bigint,user_id::bigint,trade_no,status,settlement_currency,settled_amount_micros,expected_amount_micros,money::text AS money,amount,credited_quota,refunded_amount_micros,refunded_quota FROM top_ups WHERE payment_provider='stripe' AND provider_transaction_id=$1 FOR UPDATE")
            .bind(transaction).fetch_optional(&mut *tx).await.map_err(refund_db)?.ok_or(SettlementError::Conflict)?;
        let user_id: i64 = row.try_get("user_id").map_err(refund_db)?;
        let order_id: i64 = row.try_get("id").map_err(refund_db)?;
        let trade: String = row.try_get("trade_no").map_err(refund_db)?;
        let stored_currency: String = row.try_get("settlement_currency").map_err(refund_db)?;
        if row.try_get::<String, _>("status").map_err(refund_db)? != "success"
            || stored_currency.trim().is_empty()
            || !stored_currency.trim().eq_ignore_ascii_case(&currency)
        {
            return Err(SettlementError::Conflict);
        }
        let key = format!("stripe:refund:{refund_id}");
        let ledger=sqlx::query("SELECT amount_micros,currency,payment_method,payment_provider,user_id::bigint,source_type,source_id,note FROM finance_ledger_entries WHERE idempotency_key=$1")
            .bind(&key).fetch_optional(&mut *tx).await.map_err(refund_db)?;
        if let Some(ledger) = &ledger {
            let bound = ledger
                .try_get::<String, _>("source_type")
                .map_err(refund_db)?
                == "refund"
                && ledger
                    .try_get::<String, _>("source_id")
                    .map_err(refund_db)?
                    == refund_id
                && ledger
                    .try_get::<Option<i64>, _>("user_id")
                    .map_err(refund_db)?
                    == Some(user_id)
                && ledger
                    .try_get::<String, _>("currency")
                    .map_err(refund_db)?
                    .trim()
                    .eq_ignore_ascii_case(&currency)
                && ledger
                    .try_get::<String, _>("payment_method")
                    .map_err(refund_db)?
                    .trim()
                    .eq_ignore_ascii_case("stripe")
                && ledger
                    .try_get::<String, _>("payment_provider")
                    .map_err(refund_db)?
                    .trim()
                    .eq_ignore_ascii_case("stripe")
                && ledger
                    .try_get::<String, _>("note")
                    .map_err(refund_db)?
                    .split_whitespace()
                    .any(|part| {
                        part == format!("trade_no={trade}")
                            || part == format!("refund_trade_no={trade}")
                    });
            if !bound {
                return Err(SettlementError::Storage);
            }
        }
        let applied = ledger
            .as_ref()
            .map(|ledger| ledger.try_get::<i64, _>("amount_micros"))
            .transpose()
            .map_err(refund_db)?
            .unwrap_or(requested);
        let refunded: i64 = row.try_get("refunded_amount_micros").map_err(refund_db)?;
        let refunded_quota: i64 = row.try_get("refunded_quota").map_err(refund_db)?;
        let already_applied = ledger.is_some() && refunded > 0;
        let mut paid: i64 = row.try_get("settled_amount_micros").map_err(refund_db)?;
        if paid <= 0 {
            paid = row.try_get("expected_amount_micros").map_err(refund_db)?;
        }
        if paid <= 0 {
            paid = Decimal::from_str_exact(&row.try_get::<String, _>("money").map_err(refund_db)?)
                .ok()
                .and_then(|value| value.checked_mul(Decimal::from(1_000_000)))
                .and_then(|value| {
                    value
                        .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
                        .to_i64()
                })
                .unwrap_or(0);
        }
        let mut credited: i64 = row.try_get("credited_quota").map_err(refund_db)?;
        if credited <= 0 {
            let per_unit: Option<String> =
                sqlx::query_scalar("SELECT value FROM options WHERE key='QuotaPerUnit'")
                    .fetch_optional(&mut *tx)
                    .await
                    .map_err(refund_db)?;
            credited = Decimal::from_str_exact(per_unit.as_deref().unwrap_or("500000"))
                .ok()
                .and_then(|value| {
                    value.checked_mul(Decimal::from(row.try_get::<i64, _>("amount").unwrap_or(0)))
                })
                .and_then(|value| {
                    value
                        .round_dp_with_strategy(0, RoundingStrategy::MidpointAwayFromZero)
                        .to_i64()
                })
                .unwrap_or(0);
        }
        let mut debit = 0;
        if !already_applied {
            let cumulative = refunded
                .checked_add(applied)
                .ok_or(SettlementError::Conflict)?;
            if applied <= 0 || refunded < 0 || paid > 0 && cumulative > paid {
                return Err(SettlementError::Conflict);
            }
            let target = proportional_target(credited, paid, cumulative);
            debit = target.checked_sub(refunded_quota).unwrap_or(0).max(0);
            if debit > MAX_WALLET_QUOTA {
                return Err(SettlementError::Conflict);
            }
            if debit > 0 {
                let changed=sqlx::query("UPDATE users SET quota=quota-$2 WHERE id=$1 AND deleted_at IS NULL AND quota>=$2 AND quota<=$3")
                    .bind(user_id).bind(debit).bind(MAX_WALLET_QUOTA).execute(&mut *tx).await.map_err(refund_db)?.rows_affected();
                if changed != 1 {
                    return Err(SettlementError::Storage);
                }
            }
            if cumulative >= paid {
                refund_referral(&mut tx, order_id, user_id).await?;
            }
            sqlx::query(
                "UPDATE top_ups SET refunded_amount_micros=$2,refunded_quota=$3 WHERE id=$1",
            )
            .bind(order_id)
            .bind(cumulative)
            .bind(
                refunded_quota
                    .checked_add(debit)
                    .ok_or(SettlementError::Storage)?,
            )
            .execute(&mut *tx)
            .await
            .map_err(refund_db)?;
        }
        let created = ledger.is_none();
        if created {
            let now = now_seconds().map_err(|_| SettlementError::Storage)?;
            let note = format!("Stripe refund.succeeded trade_no={trade} refund_id={refund_id}");
            sqlx::query("INSERT INTO finance_ledger_entries(entry_type,category,amount_micros,currency,direction,payment_method,payment_provider,user_id,source_type,source_id,note,occurred_at,created_at,created_by,idempotency_key) VALUES('revenue','refund',$1,$2,-1,'stripe','stripe',$3,'refund',$4,$5,$6,$6,$3,$7)")
                .bind(applied).bind(&currency).bind(user_id).bind(&refund_id).bind(note).bind(now).bind(&key).execute(&mut *tx).await.map_err(refund_db)?;
        }
        tx.commit().await.map_err(refund_db)?;
        if debit > 0 {
            self.wallet.invalidate_user(user_id).await;
        }
        if created {
            let content = format!(
                "Stripe refund.succeeded trade_no={trade} refund_id={refund_id} amount={minor}"
            );
            let _=sqlx::query("INSERT INTO logs(user_id,created_at,type,content,username) SELECT id,$2,6,$3,COALESCE(username,'') FROM users WHERE id=$1")
                .bind(user_id).bind(now_seconds().unwrap_or(0)).bind(content).execute(&self.pg).await;
        }
        Ok(())
    }
}

// All operands are bounded to the JS-safe wallet/payment domain. i128 keeps
// the exact product, avoiding Decimal's 96-bit intermediate overflow.
fn proportional_target(quota: i64, paid: i64, refunded: i64) -> i64 {
    if quota <= 0 || paid <= 0 || refunded <= 0 {
        return 0;
    }
    let numerator = i128::from(quota) * i128::from(refunded);
    let denominator = i128::from(paid);
    let rounded =
        numerator / denominator + i128::from((numerator % denominator) * 2 >= denominator);
    rounded.clamp(0, i128::from(quota)) as i64
}

async fn refund_referral(
    tx: &mut sqlx::Transaction<'_, sqlx::Postgres>,
    order_id: i64,
    user_id: i64,
) -> Result<(), SettlementError> {
    let user=sqlx::query("SELECT COALESCE(inviter_id,0)::bigint AS inviter_id,referral_first_top_up_id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE")
        .bind(user_id).fetch_one(&mut **tx).await.map_err(refund_db)?;
    if user.try_get::<i64, _>("inviter_id").map_err(refund_db)? <= 0
        || user
            .try_get::<i64, _>("referral_first_top_up_id")
            .map_err(refund_db)?
            != order_id
    {
        return Ok(());
    }
    let reward=sqlx::query("SELECT id::bigint,inviter_id::bigint,quota,status,revision FROM referral_rewards WHERE top_up_id=$1 FOR UPDATE")
        .bind(order_id).fetch_optional(&mut **tx).await.map_err(refund_db)?;
    let Some(reward) = reward else {
        return Ok(());
    };
    let id: i64 = reward.try_get("id").map_err(refund_db)?;
    if reward.try_get::<String, _>("status").map_err(refund_db)? != "earned" {
        sqlx::query("UPDATE referral_rewards SET reason='refund' WHERE id=$1")
            .bind(id)
            .execute(&mut **tx)
            .await
            .map_err(refund_db)?;
        return Ok(());
    }
    let quota: i64 = reward.try_get("quota").map_err(refund_db)?;
    if !(0..=MAX_WALLET_QUOTA).contains(&quota) {
        return Err(SettlementError::Storage);
    }
    let inviter: i64 = reward.try_get("inviter_id").map_err(refund_db)?;
    let revision = reward
        .try_get::<i64, _>("revision")
        .map_err(refund_db)?
        .checked_add(1)
        .ok_or(SettlementError::Storage)?;
    let now = now_seconds().map_err(|_| SettlementError::Storage)?;
    if quota > 0 {
        let changed=sqlx::query("UPDATE users SET aff_quota=aff_quota-$2 WHERE id=$1 AND aff_quota>=$3 AND aff_quota<=$4")
            .bind(inviter).bind(quota).bind(-MAX_WALLET_QUOTA+quota).bind(MAX_WALLET_QUOTA).execute(&mut **tx).await.map_err(refund_db)?.rows_affected();
        if changed != 1 {
            return Err(SettlementError::Storage);
        }
        sqlx::query("INSERT INTO referral_ledger_entries(reward_id,user_id,event_key,kind,quota,reason,created_at) VALUES($1,$2,$3,'clawback',$4,'refund',$5)")
            .bind(id).bind(inviter).bind(format!("referral:{id}:{revision}:clawback")).bind(-quota).bind(now).execute(&mut **tx).await.map_err(refund_db)?;
    }
    sqlx::query("UPDATE referral_rewards SET status='revoked',revision=$2,reason='refund',revoked_quota=quota,penalty_quota=0,updated_at=$3 WHERE id=$1")
        .bind(id).bind(revision).bind(now).execute(&mut **tx).await.map_err(refund_db)?;
    Ok(())
}

fn refund_db(_: sqlx::Error) -> SettlementError {
    SettlementError::Storage
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn partial_refunds_round_cumulatively_without_overflow() {
        assert_eq!(proportional_target(7, 100, 25), 2);
        assert_eq!(proportional_target(7, 100, 50), 4);
        assert_eq!(proportional_target(7, 100, 100), 7);
        assert_eq!(
            proportional_target(
                MAX_WALLET_QUOTA,
                9_000_000_000_000_000,
                9_000_000_000_000_000
            ),
            MAX_WALLET_QUOTA
        );
    }
}
