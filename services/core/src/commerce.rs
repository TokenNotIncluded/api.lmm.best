//! Authenticated store escrow over the real ledger. Never calls Go or a provider.
//! A service credential only enables this port; the current user, exact frozen
//! order, key scope, funding grants and shared budgets authorize every mutation.
pub mod rpc;

use crate::{
    accounts::{Account, AccountKind},
    billing::{
        Credits, charging,
        ledger::{Action, Outcome, Rejection, Request as LedgerRequest, Wallet},
    },
    events::{EventStore, NewEvent},
    identity::{CredentialKind, IdentityError, Principal},
};
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Postgres, Row, Transaction, types::Json};
use std::{fmt, time::Duration};

pub const SCHEMA: &str = include_str!("../schema/commerce.sql");

#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(untagged)]
pub enum Price {
    Usd(UsdMinor),
    Credits(CreditPrice),
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct UsdMinor {
    pub amount_minor: i64,
    pub currency: String,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct CreditPrice {
    pub amount_credits: i64,
    pub unit: String,
}
impl Price {
    pub fn credits(&self) -> Result<i64> {
        match self {
            Self::Usd(v) if v.currency == "USD" && v.amount_minor > 0 => {
                v.amount_minor.checked_mul(5_000).ok_or(Error::Invalid)
            }
            Self::Credits(v) if v.unit == "credits" && v.amount_credits > 0 => Ok(v.amount_credits),
            _ => Err(Error::Invalid),
        }
    }
    fn times(&self, quantity: i64) -> Result<Self> {
        if quantity <= 0 {
            return Err(Error::Invalid);
        }
        Ok(match self {
            Self::Usd(v) => Self::Usd(UsdMinor {
                amount_minor: v.amount_minor.checked_mul(quantity).ok_or(Error::Invalid)?,
                currency: v.currency.clone(),
            }),
            Self::Credits(v) => Self::Credits(CreditPrice {
                amount_credits: v
                    .amount_credits
                    .checked_mul(quantity)
                    .ok_or(Error::Invalid)?,
                unit: v.unit.clone(),
            }),
        })
    }
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct OrderSnapshot {
    pub variant_id: String,
    pub quantity: i64,
    pub unit_price: Price,
    pub created_by: i64,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
pub struct MoneyCommand {
    pub key: String,
    pub kind: String,
    pub shop_id: String,
    pub order_id: String,
    pub buyer: Account,
    pub seller: Account,
    pub total: Price,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub payment_ref: String,
    #[serde(default, skip_serializing_if = "String::is_empty")]
    pub settlement_ref: String,
    pub order_snapshot: OrderSnapshot,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
#[serde(deny_unknown_fields)]
struct Economics {
    buyer: Account,
    seller: Account,
    total: Price,
    snapshot: OrderSnapshot,
}
#[derive(Clone, Debug, Deserialize, Eq, PartialEq, Serialize)]
pub struct Receipt {
    pub key: String,
    pub command_hash: String,
    pub reference: String,
    pub outcome: String,
    pub rejection_code: String,
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    Invalid,
    Unauthorized,
    Forbidden,
    Conflict,
    Missing,
    Pending,
    Retry,
    Storage,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "{self:?}")
    }
}
impl std::error::Error for Error {}
pub type Result<T> = std::result::Result<T, Error>;
impl From<sqlx::Error> for Error {
    fn from(e: sqlx::Error) -> Self {
        match e.as_database_error().and_then(|e| e.code()).as_deref() {
            Some("40001" | "40P01" | "55P03" | "57014") => Self::Retry,
            Some("23505" | "P1001") => Self::Conflict,
            _ => Self::Storage,
        }
    }
}
impl From<IdentityError> for Error {
    fn from(e: IdentityError) -> Self {
        match e {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            IdentityError::Conflict => Self::Retry,
            IdentityError::Storage => Self::Storage,
        }
    }
}
fn hex(bytes: &[u8]) -> String {
    bytes.iter().map(|b| format!("{b:02x}")).collect()
}
fn id(value: &serde_json::Value) -> String {
    hex(&Sha256::digest(
        serde_json::to_vec(value).expect("JSON value serialization"),
    ))
}
fn hash_id(s: &str) -> bool {
    s.len() == 64
        && s.bytes()
            .all(|b| b.is_ascii_digit() || (b'a'..=b'f').contains(&b))
}
impl MoneyCommand {
    fn validate(&self) -> Result<i64> {
        if !hash_id(&self.shop_id)
            || !hash_id(&self.order_id)
            || !hash_id(&self.order_snapshot.variant_id)
            || !self.buyer.is_valid()
            || !self.seller.is_valid()
            || self.buyer == self.seller
            || self.order_snapshot.created_by <= 0
            || !matches!(self.kind.as_str(), "pay" | "settle" | "refund")
            || self.payment_ref.len() > 256
            || self.settlement_ref.len() > 256
            || self.key
                != id(&serde_json::json!([
                    "store-money-v1",
                    self.shop_id,
                    self.order_id,
                    self.kind
                ]))
            || self
                .order_snapshot
                .unit_price
                .times(self.order_snapshot.quantity)?
                != self.total
        {
            return Err(Error::Invalid);
        }
        self.order_snapshot.unit_price.credits()?;
        self.total.credits()
    }
    fn economics(&self) -> Economics {
        Economics {
            buyer: self.buyer,
            seller: self.seller,
            total: self.total.clone(),
            snapshot: self.order_snapshot.clone(),
        }
    }
}
#[derive(Clone)]
pub struct CommerceStore {
    pool: PgPool,
    service_principal: String,
}
impl CommerceStore {
    pub fn from_pool(pool: PgPool) -> Self {
        Self {
            pool,
            service_principal: "store".into(),
        }
    }
    /// Trusted server configuration, never request data. Rotation of the
    /// transport token preserves this stable merchant service identity.
    pub fn with_service_principal(mut self, principal: &str) -> Result<Self> {
        if !crate::events::valid_name(principal, 64) {
            return Err(Error::Invalid);
        }
        self.service_principal = principal.into();
        Ok(self)
    }
    pub async fn connect(url: &str) -> Result<Self> {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .max_connections(2)
            .acquire_timeout(Duration::from_millis(400))
            .connect(url)
            .await?;
        sqlx::query("SELECT id FROM core_commerce.orders LIMIT 0")
            .execute(&pool)
            .await?;
        Ok(Self::from_pool(pool))
    }
    /// The exact Go command array is hashed before parsing. Typed serde records
    /// reject duplicate fields, unknown fields, floats and mixed monetary units.
    pub async fn execute(&self, secret: &str, bytes: &[u8]) -> Result<Receipt> {
        if bytes.len() > 8192 {
            return Err(Error::Invalid);
        }
        let [command]: [MoneyCommand; 1] =
            serde_json::from_slice(bytes).map_err(|_| Error::Invalid)?;
        let amount = command.validate()?;
        let digest = Sha256::digest(bytes).to_vec();
        for attempt in 0..4 {
            match self.once(secret, &command, &digest, amount).await {
                Err(Error::Retry) if attempt < 3 => tokio::task::yield_now().await,
                value => return value,
            }
        }
        Err(Error::Pending)
    }
    async fn begin(&self) -> Result<Transaction<'static, Postgres>> {
        let mut tx = self.pool.begin().await?;
        for q in [
            "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE",
            "SET LOCAL synchronous_commit=on",
            "SET LOCAL lock_timeout='250ms'",
            "SET LOCAL statement_timeout='1500ms'",
            "SET LOCAL idle_in_transaction_session_timeout='2s'",
        ] {
            sqlx::query(q).execute(&mut *tx).await?;
        }
        Ok(tx)
    }
    async fn once(
        &self,
        secret: &str,
        cmd: &MoneyCommand,
        digest: &[u8],
        amount: i64,
    ) -> Result<Receipt> {
        let mut tx = self.begin().await?;
        let actor = crate::identity::authorize_in_transaction(&mut tx, secret).await?;
        let pay = cmd.kind == "pay";
        // Accepted-order refunds and captures return/consume only the original
        // economic payer's money. Later buyer deactivation cannot redirect or
        // strand that return. New payments still require both parties active.
        let buyer = native(&mut tx, cmd.buyer, pay).await?;
        let seller = native(&mut tx, cmd.seller, true).await?;
        authority(
            &mut tx,
            &actor,
            if pay { cmd.buyer } else { cmd.seller },
            if pay { buyer } else { seller },
            pay,
        )
        .await?;
        if pay && actor.user_id != cmd.order_snapshot.created_by {
            return Err(Error::Forbidden);
        }
        let consumption_id = format!(
            "commerce:{}",
            id(&serde_json::json!([cmd.shop_id, cmd.order_id]))
        );
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")
            .bind(&consumption_id)
            .execute(&mut *tx)
            .await?;
        let order = sqlx::query(
            "SELECT * FROM core_commerce.orders WHERE shop_id=$1 AND order_id=$2 FOR UPDATE",
        )
        .bind(&cmd.shop_id)
        .bind(&cmd.order_id)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some(row) = &order {
            let Json(saved): Json<Economics> = row.try_get("economics")?;
            if saved != cmd.economics()
                || row.try_get::<i64, _>("buyer_account_id")? != buyer
                || row.try_get::<i64, _>("seller_account_id")? != seller
                || row.try_get::<i64, _>("credits")? != amount
            {
                return Err(Error::Conflict);
            }
        }
        if let Some(row) = sqlx::query(
            "SELECT command_sha256,receipt FROM core_commerce.operations WHERE operation_key=$1",
        )
        .bind(&cmd.key)
        .fetch_optional(&mut *tx)
        .await?
        {
            if row.try_get::<Vec<u8>, _>("command_sha256")? != digest {
                return Err(Error::Conflict);
            }
            let Json(receipt): Json<Receipt> = row.try_get("receipt")?;
            tx.commit().await?;
            return Ok(receipt);
        }
        let now: i64 =
            sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
                .fetch_one(&mut *tx)
                .await?;
        let mut budget_rejection = false;
        let action = if pay {
            if order.is_some() || !cmd.payment_ref.is_empty() || !cmd.settlement_ref.is_empty() {
                return Err(Error::Conflict);
            }
            sqlx::query("SAVEPOINT commerce_pay")
                .execute(&mut *tx)
                .await?;
            sqlx::query("INSERT INTO core_charging.external_consumption(id,domain,actor_user_id,key_id,payer_account_id,authorized_at,amount_credits) VALUES($1,'commerce',$2,$3,$4,$5,$6)")
                .bind(&consumption_id).bind(actor.user_id).bind(actor.credential_id).bind(buyer).bind(now).bind(amount).execute(&mut *tx).await?;
            match charging::external_budgets(&mut tx, &consumption_id).await {
                Ok(()) => {}
                Err(charging::Error::BudgetExceeded) => budget_rejection = true,
                Err(charging::Error::Retry | charging::Error::Conflict) => {
                    return Err(Error::Retry);
                }
                Err(_) => return Err(Error::Storage),
            }
            let _: Json<Wallet> = sqlx::query_scalar("SELECT core_billing.open_ledger_wallet($1)")
                .bind(buyer)
                .fetch_one(&mut *tx)
                .await?;
            let _: Json<Wallet> = sqlx::query_scalar("SELECT core_billing.open_ledger_wallet($1)")
                .bind(seller)
                .fetch_one(&mut *tx)
                .await?;
            Action::Reserve {
                account_id: buyer,
                amount_units: Credits::try_from(amount).map_err(|_| Error::Invalid)?,
            }
        } else {
            let row = order.as_ref().ok_or(Error::Missing)?;
            let reserve: i64 = row
                .try_get::<Option<i64>, _>("reserve_journal_id")?
                .ok_or(Error::Conflict)?;
            if cmd.payment_ref != reference(reserve) {
                return Err(Error::Conflict);
            }
            let settled: Option<i64> = row.try_get("settlement_journal_id")?;
            if cmd.settlement_ref != settled.map(reference).unwrap_or_default() {
                return Err(Error::Conflict);
            }
            let state: String = row.try_get("state")?;
            match (cmd.kind.as_str(), state.as_str(), settled) {
                ("settle", "paid", None) => Action::CommerceCapture {
                    reservation_journal_id: reserve,
                    destination_account_id: seller,
                    amount_units: Credits::try_from(amount).map_err(|_| Error::Invalid)?,
                },
                ("refund", "paid", None) => Action::Release {
                    reservation_journal_id: reserve,
                },
                ("refund", "settled", Some(journal_id)) => Action::RefundCommerce { journal_id },
                _ => return Err(Error::Conflict),
            }
        };
        let outcome = if budget_rejection {
            None
        } else {
            let request = LedgerRequest {
                scope: "core-commerce".into(),
                operation_key: cmd.key.clone(),
                actor_user_id: Some(actor.user_id),
                reason: format!("Store {} order {}", cmd.kind, cmd.order_id),
                action,
            };
            let Json(outcome): Json<Outcome> =
                sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
                    .bind(Json(request))
                    .fetch_one(&mut *tx)
                    .await?;
            Some(outcome)
        };
        let (journal, rejection) = match outcome {
            Some(Outcome::Posted { journal_id, .. }) => (Some(journal_id), String::new()),
            Some(Outcome::Rejected { code }) => {
                // Seller may temporarily lack funds after settlement. Keep the
                // original refund UNKNOWN and rollback its rejected ledger row;
                // retrying the same key after recovery can never double-refund.
                if !pay && code == Rejection::InsufficientFunds {
                    return Err(Error::Pending);
                }
                (
                    None,
                    serde_json::to_value(code)
                        .map_err(|_| Error::Storage)?
                        .as_str()
                        .ok_or(Error::Storage)?
                        .to_owned(),
                )
            }
            None => (None, "budget_exceeded".into()),
        };
        if pay {
            if journal.is_none() {
                sqlx::query("ROLLBACK TO SAVEPOINT commerce_pay")
                    .execute(&mut *tx)
                    .await?;
            }
            sqlx::query("RELEASE SAVEPOINT commerce_pay")
                .execute(&mut *tx)
                .await?;
        }
        let receipt = Receipt {
            key: cmd.key.clone(),
            command_hash: hex(digest),
            reference: journal
                .map(reference)
                .unwrap_or_else(|| format!("commerce:rejected:{}", cmd.key)),
            outcome: if journal.is_some() {
                "succeeded"
            } else {
                "rejected"
            }
            .into(),
            rejection_code: rejection,
        };
        let (order_id, revision) = if pay {
            let order_id:i64=sqlx::query_scalar("INSERT INTO core_commerce.orders(shop_id,order_id,economics,buyer_account_id,seller_account_id,actor_user_id,key_id,authorized_at,credits,consumption_id,state,reserve_journal_id,capture_principal,payment_command_sha256) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14) RETURNING id")
                .bind(&cmd.shop_id).bind(&cmd.order_id).bind(Json(cmd.economics())).bind(buyer).bind(seller).bind(actor.user_id).bind(actor.credential_id).bind(now).bind(amount).bind(&consumption_id).bind(if journal.is_some(){"paid"}else{"rejected"}).bind(journal).bind(&self.service_principal).bind(digest).fetch_one(&mut *tx).await?;
            (order_id, 1)
        } else {
            let row = order.as_ref().ok_or(Error::Missing)?;
            let oid: i64 = row.try_get("id")?;
            let revision: i64 = if journal.is_some() {
                sqlx::query_scalar("UPDATE core_commerce.orders SET state=$2,settlement_journal_id=CASE WHEN $2='settled' THEN $3 ELSE settlement_journal_id END,revision=revision+1 WHERE id=$1 RETURNING revision")
                    .bind(oid).bind(if cmd.kind=="settle"{"settled"}else{"refunded"}).bind(journal).fetch_one(&mut *tx).await?
            } else {
                row.try_get("revision")?
            };
            if cmd.kind == "refund" && journal.is_some() {
                let result=sqlx::query("UPDATE core_charging.external_consumption SET refunded=amount_credits WHERE id=$1 AND refunded=0")
                    .bind(&consumption_id).execute(&mut *tx).await?;
                if result.rows_affected() != 1 {
                    return Err(Error::Conflict);
                }
            }
            (oid, revision)
        };
        sqlx::query("INSERT INTO core_commerce.operations(operation_key,order_id,kind,command_sha256,command,actor_user_id,receipt) VALUES($1,$2,$3,$4,$5,$6,$7)")
            .bind(&cmd.key).bind(order_id).bind(&cmd.kind).bind(digest).bind(Json(cmd)).bind(actor.user_id).bind(Json(&receipt)).execute(&mut *tx).await?;
        let payload = serde_json::to_vec(&receipt).map_err(|_| Error::Storage)?;
        EventStore::publish(
            &mut tx,
            NewEvent {
                key: &format!("commerce:{}", cmd.key),
                event_type: "commerce.operation.v1",
                schema_version: 1,
                resource_id: order_id,
                resource_version: revision,
                payload: &payload,
            },
        )
        .await
        .map_err(|e| match e {
            crate::events::EventError::Busy => Error::Retry,
            _ => Error::Storage,
        })?;
        tx.commit().await?;
        Ok(receipt)
    }
    pub async fn query(
        &self,
        secret: &str,
        shop: &str,
        order: &str,
        key: &str,
    ) -> Result<Option<Receipt>> {
        if !hash_id(shop) || !hash_id(order) || !hash_id(key) {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        let actor = crate::identity::authorize_in_transaction(&mut tx, secret).await?;
        let row=sqlx::query("SELECT id,economics,buyer_account_id,seller_account_id FROM core_commerce.orders WHERE shop_id=$1 AND order_id=$2")
            .bind(shop).bind(order).fetch_optional(&mut *tx).await?;
        let Some(row) = row else {
            tx.commit().await?;
            return Ok(None);
        };
        let Json(econ): Json<Economics> = row.try_get("economics")?;
        let buyer = authority(
            &mut tx,
            &actor,
            econ.buyer,
            row.try_get("buyer_account_id")?,
            true,
        )
        .await;
        if let Err(Error::Forbidden) = buyer {
            authority(
                &mut tx,
                &actor,
                econ.seller,
                row.try_get("seller_account_id")?,
                false,
            )
            .await?;
        } else {
            buyer?;
        }
        let receipt: Option<Json<Receipt>> = sqlx::query_scalar(
            "SELECT receipt FROM core_commerce.operations WHERE operation_key=$1 AND order_id=$2",
        )
        .bind(key)
        .bind(row.try_get::<i64, _>("id")?)
        .fetch_optional(&mut *tx)
        .await?;
        tx.commit().await?;
        Ok(receipt.map(|Json(v)| v))
    }
    /// Read only an existing exact operation for this trusted merchant service.
    /// A lost pay acknowledgement can be recovered after the buyer logs out,
    /// without storing a bearer or granting permission to execute a new pay.
    pub async fn reconcile(&self, bytes: &[u8]) -> Result<Option<Receipt>> {
        if bytes.len() > 8192 {
            return Err(Error::Invalid);
        }
        let [cmd]: [MoneyCommand; 1] = serde_json::from_slice(bytes).map_err(|_| Error::Invalid)?;
        cmd.validate()?;
        let digest = Sha256::digest(bytes).to_vec();
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *tx)
            .await?;
        sqlx::query("SET LOCAL statement_timeout='1500ms'")
            .execute(&mut *tx)
            .await?;
        let order = sqlx::query("SELECT id,economics,capture_principal FROM core_commerce.orders WHERE shop_id=$1 AND order_id=$2")
            .bind(&cmd.shop_id).bind(&cmd.order_id).fetch_optional(&mut *tx).await?;
        let Some(order) = order else {
            tx.commit().await?;
            return Ok(None);
        };
        if order.try_get::<String, _>("capture_principal")? != self.service_principal {
            return Err(Error::Forbidden);
        }
        let Json(economics): Json<Economics> = order.try_get("economics")?;
        if economics != cmd.economics() {
            return Err(Error::Conflict);
        }
        let operation = sqlx::query("SELECT command_sha256,receipt FROM core_commerce.operations WHERE operation_key=$1 AND order_id=$2")
            .bind(&cmd.key).bind(order.try_get::<i64,_>("id")?).fetch_optional(&mut *tx).await?;
        let receipt = if let Some(operation) = operation {
            if operation.try_get::<Vec<u8>, _>("command_sha256")? != digest {
                return Err(Error::Conflict);
            }
            let Json(receipt): Json<Receipt> = operation.try_get("receipt")?;
            Some(receipt)
        } else {
            None
        };
        tx.commit().await?;
        Ok(receipt)
    }
    /// Merchant completion consumes the original buyer's durable fixed-order
    /// capture grant. Logout, bearer rotation or later key revocation prevents
    /// new spending but does not cancel an already accepted merchant order.
    /// The Go service must commit delivery before this call; its reference is
    /// audited here, not independently verified by reading Go's database.
    pub async fn complete_delivery(
        &self,
        bytes: &[u8],
        payment_hash: &str,
        delivery: &str,
    ) -> Result<Receipt> {
        if bytes.len() > 8192 || !hash_id(payment_hash) || !crate::events::valid_name(delivery, 128)
        {
            return Err(Error::Invalid);
        }
        let [cmd]: [MoneyCommand; 1] = serde_json::from_slice(bytes).map_err(|_| Error::Invalid)?;
        let amount = cmd.validate()?;
        if cmd.kind != "settle" || !cmd.settlement_ref.is_empty() {
            return Err(Error::Invalid);
        }
        let digest = Sha256::digest(bytes).to_vec();
        for attempt in 0..4 {
            match self
                .complete_once(&cmd, &digest, payment_hash, delivery, amount)
                .await
            {
                Err(Error::Retry) if attempt < 3 => tokio::task::yield_now().await,
                value => return value,
            }
        }
        Err(Error::Pending)
    }
    async fn complete_once(
        &self,
        cmd: &MoneyCommand,
        digest: &[u8],
        payment_hash: &str,
        delivery: &str,
        amount: i64,
    ) -> Result<Receipt> {
        let mut tx = self.begin().await?;
        let consumption_id = format!(
            "commerce:{}",
            id(&serde_json::json!([cmd.shop_id, cmd.order_id]))
        );
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")
            .bind(&consumption_id)
            .execute(&mut *tx)
            .await?;
        let row = sqlx::query(
            "SELECT * FROM core_commerce.orders WHERE shop_id=$1 AND order_id=$2 FOR UPDATE",
        )
        .bind(&cmd.shop_id)
        .bind(&cmd.order_id)
        .fetch_optional(&mut *tx)
        .await?
        .ok_or(Error::Missing)?;
        let Json(economics): Json<Economics> = row.try_get("economics")?;
        if row.try_get::<String, _>("capture_principal")? != self.service_principal {
            return Err(Error::Forbidden);
        }
        if economics != cmd.economics()
            || row.try_get::<i64, _>("credits")? != amount
            || hex(&row.try_get::<Vec<u8>, _>("payment_command_sha256")?) != payment_hash
        {
            return Err(Error::Conflict);
        }
        let reserve = row
            .try_get::<Option<i64>, _>("reserve_journal_id")?
            .ok_or(Error::Conflict)?;
        if cmd.payment_ref != reference(reserve) {
            return Err(Error::Conflict);
        }
        let oid: i64 = row.try_get("id")?;
        if let Some(op)=sqlx::query("SELECT command_sha256,delivery_reference,receipt FROM core_commerce.operations WHERE operation_key=$1 AND order_id=$2")
            .bind(&cmd.key).bind(oid).fetch_optional(&mut *tx).await?{
            if op.try_get::<Vec<u8>,_>("command_sha256")?!=digest||op.try_get::<Option<String>,_>("delivery_reference")?.as_deref()!=Some(delivery){return Err(Error::Conflict);}
            let Json(receipt):Json<Receipt>=op.try_get("receipt")?;tx.commit().await?;return Ok(receipt);
        }
        if row.try_get::<String, _>("state")? != "paid" {
            return Err(Error::Conflict);
        }
        let actor: i64 = row.try_get("actor_user_id")?;
        let request = LedgerRequest {
            scope: "core-commerce".into(),
            operation_key: cmd.key.clone(),
            actor_user_id: Some(actor),
            reason: format!("Store delivery {} order {}", delivery, cmd.order_id),
            action: Action::CommerceCapture {
                reservation_journal_id: reserve,
                destination_account_id: row.try_get("seller_account_id")?,
                amount_units: Credits::try_from(amount).map_err(|_| Error::Invalid)?,
            },
        };
        let Json(outcome): Json<Outcome> =
            sqlx::query_scalar("SELECT core_billing.post_ledger($1)")
                .bind(Json(request))
                .fetch_one(&mut *tx)
                .await?;
        let (journal, rejection) = match outcome {
            Outcome::Posted { journal_id, .. } => (Some(journal_id), String::new()),
            Outcome::Rejected {
                code: Rejection::InsufficientFunds,
            } => return Err(Error::Pending),
            Outcome::Rejected { code } => (
                None,
                serde_json::to_value(code)
                    .map_err(|_| Error::Storage)?
                    .as_str()
                    .ok_or(Error::Storage)?
                    .to_owned(),
            ),
        };
        let receipt = Receipt {
            key: cmd.key.clone(),
            command_hash: hex(digest),
            reference: journal
                .map(reference)
                .unwrap_or_else(|| format!("commerce:rejected:{}", cmd.key)),
            outcome: if journal.is_some() {
                "succeeded"
            } else {
                "rejected"
            }
            .into(),
            rejection_code: rejection,
        };
        // Permanent rejection records delivery evidence in the immutable
        // operation, without claiming seller payment or reducing buyer budget.
        let revision: i64 = if journal.is_some() {
            sqlx::query_scalar("UPDATE core_commerce.orders SET state='settled',settlement_journal_id=$2,delivery_reference=$3,revision=revision+1 WHERE id=$1 RETURNING revision")
                .bind(oid).bind(journal).bind(delivery).fetch_one(&mut *tx).await?
        } else {
            row.try_get("revision")?
        };
        sqlx::query("INSERT INTO core_commerce.operations(operation_key,order_id,kind,command_sha256,command,actor_user_id,delivery_reference,receipt) VALUES($1,$2,'settle',$3,$4,$5,$6,$7)")
            .bind(&cmd.key).bind(oid).bind(digest).bind(Json(cmd)).bind(actor).bind(delivery).bind(Json(&receipt)).execute(&mut *tx).await?;
        let payload = serde_json::to_vec(&receipt).map_err(|_| Error::Storage)?;
        EventStore::publish(
            &mut tx,
            NewEvent {
                key: &format!("commerce:{}", cmd.key),
                event_type: "commerce.operation.v1",
                schema_version: 1,
                resource_id: oid,
                resource_version: revision,
                payload: &payload,
            },
        )
        .await
        .map_err(|e| match e {
            crate::events::EventError::Busy => Error::Retry,
            _ => Error::Storage,
        })?;
        tx.commit().await?;
        Ok(receipt)
    }
}
fn reference(journal: i64) -> String {
    format!("commerce:journal:{journal}")
}
async fn native(c: &mut PgConnection, account: Account, require_active: bool) -> Result<i64> {
    let q = match account.kind {
        AccountKind::Personal => {
            "SELECT a.id FROM core_identity.accounts a JOIN core_identity.users u ON u.personal_account_id=a.id WHERE u.id=$1 AND (NOT $2 OR (a.active AND u.active)) FOR SHARE OF a,u"
        }
        AccountKind::Team => {
            "SELECT a.id FROM core_identity.accounts a JOIN core_identity.teams t ON t.account_id=a.id WHERE t.id=$1 AND (NOT $2 OR (a.active AND t.active)) FOR SHARE OF a,t"
        }
    };
    sqlx::query_scalar(q)
        .bind(account.id)
        .bind(require_active)
        .fetch_optional(c)
        .await?
        .ok_or(Error::Forbidden)
}
async fn authority(
    c: &mut PgConnection,
    actor: &Principal,
    account: Account,
    native: i64,
    spend: bool,
) -> Result<()> {
    if actor.credential_kind == CredentialKind::ApiKey {
        if !spend
            || !actor.funding_accounts.contains(&account)
            || !actor.funding_account_ids.contains(&native)
        {
            return Err(Error::Forbidden);
        }
        let allowed: Option<bool> = sqlx::query_scalar(
            "SELECT can_spend FROM core_commerce.key_scopes WHERE credential_id=$1 FOR SHARE",
        )
        .bind(actor.credential_id)
        .fetch_optional(&mut *c)
        .await?;
        if allowed != Some(true) {
            return Err(Error::Forbidden);
        }
    }
    match account.kind {
        AccountKind::Personal if account.id == actor.user_id => Ok(()),
        AccountKind::Personal => Err(Error::Forbidden),
        AccountKind::Team => {
            let row=sqlx::query("SELECT t.owner_user_id,m.role,m.can_spend,m.active FROM core_identity.teams t LEFT JOIN core_identity.memberships m ON m.team_id=t.id AND m.user_id=$2 WHERE t.id=$1 AND t.active FOR SHARE OF t")
                .bind(account.id).bind(actor.user_id).fetch_optional(&mut *c).await?.ok_or(Error::Forbidden)?;
            if row.try_get::<i64, _>("owner_user_id")? == actor.user_id {
                return Ok(());
            }
            let member=sqlx::query("SELECT role,can_spend FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2 AND active FOR SHARE")
                .bind(account.id).bind(actor.user_id).fetch_optional(c).await?.ok_or(Error::Forbidden)?;
            if (spend && member.try_get::<bool, _>("can_spend")?)
                || (!spend && member.try_get::<String, _>("role")? == "admin")
            {
                Ok(())
            } else {
                Err(Error::Forbidden)
            }
        }
    }
}
