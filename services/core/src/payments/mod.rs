//! Core-owned payment intents and independently verified provider transitions.
//! All money uses the existing controlled ledger, inside the metadata transaction.
//! Provider requests never run while this transaction is open.
pub mod rpc;
mod stripe;

use crate::{
    billing::{
        Credits,
        ledger::{Action, Outcome, Request as LedgerRequest},
    },
    identity::{self, CredentialKind, IdentityError},
    internal_rpc::pb,
};
use prost::Message;
use serde_json::json;
use sha2::{Digest, Sha256};
use sqlx::{PgConnection, PgPool, Postgres, Transaction, types::Json};
use std::{
    collections::BTreeMap,
    fmt,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

pub const SCHEMA: &str = include_str!("../../schema/payments.sql");
const CREDIT_UNIT: &str = "credit_500k_usd";
const PREPARED: i32 = 7;

/// Trusted private server configuration. Do not serialize or log webhook secrets.
/// A distinct endpoint secret is required for each merchant/environment namespace.
#[derive(Clone)]
pub struct Channel {
    pub id: i64,
    pub provider: String,
    pub merchant: String,
    pub environment: String,
    pub currency: String,
    pub webhook_secrets: Vec<String>,
    pub credits_per_minor: i64,
    pub max_amount_minor: i64,
}
impl Channel {
    /// First supported policy: USD cents, exactly 5000 integer credits per cent.
    pub fn stripe_test(id: i64, merchant: String, secret: String, maximum: i64) -> Self {
        Self {
            id,
            provider: "stripe".into(),
            merchant,
            environment: "test".into(),
            currency: "USD".into(),
            webhook_secrets: vec![secret],
            credits_per_minor: 5000,
            max_amount_minor: maximum,
        }
    }
    fn validate(&self) -> bool {
        self.id > 0
            && self.provider == "stripe"
            && valid_id(&self.merchant)
            && self.merchant.starts_with("acct_")
            && self.environment == "test"
            && self.currency == "USD"
            && self.credits_per_minor == 5000
            && self.max_amount_minor > 0
            && self.max_amount_minor.checked_mul(5000).is_some()
            && (1..=2).contains(&self.webhook_secrets.len())
            && self.webhook_secrets.iter().all(|s| {
                s.starts_with("whsec_")
                    && (16..=256).contains(&s.len())
                    && s.bytes().all(|b| b.is_ascii_graphic())
            })
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Error {
    Invalid,
    Denied,
    Conflict,
    Unavailable,
    NotFound,
    Storage,
}
impl fmt::Display for Error {
    fn fmt(&self, f: &mut fmt::Formatter<'_>) -> fmt::Result {
        write!(f, "payment {self:?}")
    }
}
impl std::error::Error for Error {}
impl From<sqlx::Error> for Error {
    fn from(error: sqlx::Error) -> Self {
        match error.as_database_error().and_then(|e| e.code()).as_deref() {
            // Transient transaction aborts are retried as a WHOLE call with its
            // unchanged key after receipt reconciliation, never one statement.
            Some("40001" | "40P01" | "55P03") => Self::Unavailable,
            Some("23505" | "P1001") => Self::Conflict,
            _ => Self::Storage,
        }
    }
}
impl From<IdentityError> for Error {
    fn from(error: IdentityError) -> Self {
        match error {
            IdentityError::Unauthorized | IdentityError::Forbidden => Self::Denied,
            IdentityError::Invalid => Self::Invalid,
            IdentityError::Conflict => Self::Conflict,
            IdentityError::Storage => Self::Storage,
        }
    }
}
type Result<T> = std::result::Result<T, Error>;

#[derive(Clone)]
pub struct PaymentsStore {
    pool: PgPool,
    channels: Arc<BTreeMap<i64, Channel>>,
}
#[derive(sqlx::FromRow)]
struct IntentRow {
    id: i64,
    account_id: i64,
    created_by_user_id: i64,
    channel_id: i64,
    provider: String,
    merchant: String,
    environment: String,
    currency: String,
    amount_minor: i64,
    credit_units: i64,
    credits_per_minor: i64,
    credit_journal_id: Option<i64>,
    provider_transaction: Option<String>,
    refund_fenced: bool,
    refunded_minor: i64,
}
#[derive(sqlx::FromRow)]
struct HoldRow {
    receipt_id: String,
    amount_minor: i64,
    credit_units: i64,
    reservation_journal_id: i64,
    state: String,
}

fn valid_id(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= 128
        && value
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b"_.-".contains(&b))
}
fn mutation(value: &Option<pb::MutationContext>) -> Result<&str> {
    let value = value.as_ref().ok_or(Error::Invalid)?;
    if !(16..=128).contains(&value.idempotency_key.len())
        || !value.idempotency_key.bytes().all(|b| b.is_ascii_graphic())
        || value.expected_version.is_some()
    {
        return Err(Error::Invalid);
    }
    Ok(&value.idempotency_key)
}
fn hash(value: serde_json::Value) -> Vec<u8> {
    Sha256::digest(value.to_string().as_bytes()).to_vec()
}
fn evidence_hash(
    method: &str,
    intent: i64,
    hold: &str,
    evidence: &pb::ProviderEvidence,
) -> Result<Vec<u8>> {
    let payment = evidence.payment.as_ref().ok_or(Error::Invalid)?;
    Ok(hash(json!([
        method,
        intent,
        hold,
        payment.provider,
        payment.merchant,
        payment.environment,
        payment.transaction,
        evidence.provider_event_id,
        Sha256::digest(&evidence.signed_payload).to_vec()
    ])))
}
fn receipt_id(intent: i64, key: &str) -> String {
    let digest = hash(json!([intent, key]));
    let mut id = "pay_".to_owned();
    use std::fmt::Write;
    for byte in digest {
        let _ = write!(id, "{byte:02x}");
    }
    id
}
fn now() -> Result<i64> {
    i64::try_from(
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .map_err(|_| Error::Storage)?
            .as_secs(),
    )
    .map_err(|_| Error::Storage)
}
fn units(amount: i64) -> Result<Credits> {
    Credits::try_from(amount).map_err(|_| Error::Invalid)
}

impl PaymentsStore {
    pub fn new(pool: PgPool, channels: Vec<Channel>) -> Result<Self> {
        let mut by_id = BTreeMap::new();
        let mut namespaces = std::collections::BTreeSet::new();
        let mut secrets = std::collections::BTreeSet::new();
        for channel in channels {
            if !channel.validate()
                || by_id.contains_key(&channel.id)
                || !namespaces.insert((
                    channel.provider.clone(),
                    channel.merchant.clone(),
                    channel.environment.clone(),
                ))
                || channel
                    .webhook_secrets
                    .iter()
                    .any(|s| !secrets.insert(s.clone()))
            {
                return Err(Error::Invalid);
            }
            by_id.insert(channel.id, channel);
        }
        Ok(Self {
            pool,
            channels: Arc::new(by_id),
        })
    }
    async fn begin(&self) -> Result<Transaction<'static, Postgres>> {
        let mut tx = self.pool.begin().await?;
        for statement in [
            "SET TRANSACTION ISOLATION LEVEL SERIALIZABLE",
            "SET LOCAL lock_timeout='1200ms'",
            "SET LOCAL statement_timeout='1500ms'",
            "SET LOCAL synchronous_commit=on",
        ] {
            sqlx::query(statement).execute(&mut *tx).await?;
        }
        Ok(tx)
    }
    fn channel<'a>(&'a self, intent: &IntentRow) -> Result<&'a Channel> {
        let channel = self
            .channels
            .get(&intent.channel_id)
            .ok_or(Error::Unavailable)?;
        if (
            channel.provider.as_str(),
            channel.merchant.as_str(),
            channel.environment.as_str(),
            channel.currency.as_str(),
            channel.credits_per_minor,
        ) != (
            intent.provider.as_str(),
            intent.merchant.as_str(),
            intent.environment.as_str(),
            intent.currency.as_str(),
            intent.credits_per_minor,
        ) {
            return Err(Error::Conflict);
        }
        Ok(channel)
    }
    async fn intent(connection: &mut PgConnection, id: i64) -> Result<IntentRow> {
        if id <= 0 {
            return Err(Error::Invalid);
        }
        sqlx::query_as("SELECT * FROM core_payments.intents WHERE id=$1 FOR UPDATE")
            .bind(id)
            .fetch_optional(connection)
            .await?
            .ok_or(Error::NotFound)
    }
    async fn authorize(
        connection: &mut PgConnection,
        credential: &str,
        account: i64,
    ) -> Result<i64> {
        let actor = identity::authorize_in_transaction(connection, credential).await?;
        if actor.credential_kind != CredentialKind::Session {
            return Err(Error::Denied);
        }
        if actor.owner_account_id == account {
            return Ok(actor.user_id);
        }
        let team: Option<(i64,i64)> = sqlx::query_as("SELECT t.id,t.owner_user_id FROM core_identity.teams t JOIN core_identity.accounts a ON a.id=t.account_id WHERE t.account_id=$1 AND t.active AND a.active FOR SHARE OF t,a")
            .bind(account).fetch_optional(&mut *connection).await?;
        let (team, owner) = team.ok_or(Error::Denied)?;
        if owner == actor.user_id {
            return Ok(actor.user_id);
        }
        let allowed: Option<String> = sqlx::query_scalar("SELECT role FROM core_identity.memberships WHERE team_id=$1 AND user_id=$2 AND active FOR SHARE")
            .bind(team).bind(actor.user_id).fetch_optional(connection).await?;
        if allowed.as_deref() != Some("admin") {
            return Err(Error::Denied);
        }
        Ok(actor.user_id)
    }
    async fn prior(
        connection: &mut PgConnection,
        intent: i64,
        key: &str,
        method: &str,
        request_hash: &[u8],
    ) -> Result<Option<pb::PaymentReceipt>> {
        let row: Option<(String,Vec<u8>,Vec<u8>)> = sqlx::query_as("SELECT method,request_hash,receipt_wire FROM core_payments.receipts WHERE intent_id=$1 AND operation_key=$2")
            .bind(intent).bind(key).fetch_optional(connection).await?;
        row.map(|(old_method, old_hash, wire)| {
            if old_method != method || old_hash != request_hash {
                return Err(Error::Conflict);
            }
            pb::PaymentReceipt::decode(wire.as_slice()).map_err(|_| Error::Storage)
        })
        .transpose()
    }
    #[allow(clippy::too_many_arguments)] // One private immutable receipt constructor.
    async fn save(
        connection: &mut PgConnection,
        intent: i64,
        key: &str,
        method: &str,
        request_hash: Vec<u8>,
        state: i32,
        journal_id: i64,
        rejection: &str,
    ) -> Result<pb::PaymentReceipt> {
        let receipt = pb::PaymentReceipt {
            receipt_id: receipt_id(intent, key),
            intent_id: intent,
            idempotency_key: key.into(),
            request_sha256: request_hash.clone(),
            state,
            journal_id,
            rejection_code: rejection.into(),
        };
        sqlx::query("INSERT INTO core_payments.receipts(id,intent_id,operation_key,method,request_hash,receipt_wire) VALUES($1,$2,$3,$4,$5,$6)")
            .bind(&receipt.receipt_id).bind(intent).bind(key).bind(method).bind(request_hash).bind(receipt.encode_to_vec()).execute(connection).await?;
        Ok(receipt)
    }
    async fn post(
        connection: &mut PgConnection,
        intent: i64,
        key: &str,
        phase: &str,
        actor: Option<i64>,
        action: Action,
    ) -> Result<Outcome> {
        // The external operation key is hashed, so all allowed visible ASCII keys
        // fit the narrower controlled ledger key grammar without collisions.
        let request = LedgerRequest {
            scope: "payments".into(),
            operation_key: format!("{intent}:{phase}:{}", &receipt_id(intent, key)[4..]),
            actor_user_id: actor,
            reason: format!("core payment {phase}"),
            action,
        };
        request.validate().map_err(|_| Error::Invalid)?;
        let Json(result) =
            sqlx::query_scalar::<_, Json<Outcome>>("SELECT core_billing.post_ledger($1)")
                .bind(Json(request))
                .fetch_one(connection)
                .await?;
        Ok(result)
    }
    async fn record_outcome(
        connection: &mut PgConnection,
        intent: i64,
        key: &str,
        method: &str,
        request_hash: Vec<u8>,
        state: i32,
        outcome: Outcome,
    ) -> Result<pb::PaymentReceipt> {
        match outcome {
            Outcome::Posted { journal_id, .. } => {
                Self::save(
                    connection,
                    intent,
                    key,
                    method,
                    request_hash,
                    state,
                    journal_id,
                    "",
                )
                .await
            }
            Outcome::Rejected { code } => {
                Self::save(
                    connection,
                    intent,
                    key,
                    method,
                    request_hash,
                    6,
                    0,
                    &format!("{code:?}"),
                )
                .await
            }
        }
    }
    fn verify(
        &self,
        intent: &IntentRow,
        evidence: &pb::ProviderEvidence,
    ) -> Result<stripe::Verified> {
        let channel = self.channel(intent)?;
        let payment = evidence.payment.as_ref().ok_or(Error::Invalid)?;
        if (
            payment.provider.as_str(),
            payment.merchant.as_str(),
            payment.environment.as_str(),
        ) != (
            intent.provider.as_str(),
            intent.merchant.as_str(),
            intent.environment.as_str(),
        ) {
            return Err(Error::Denied);
        }
        let verified = stripe::verify(
            &evidence.signed_payload,
            &evidence.signature,
            channel,
            intent.id,
            &intent.currency,
            intent.amount_minor,
            now()?,
        )?;
        if verified.event_id != evidence.provider_event_id
            || verified.transaction != payment.transaction
        {
            return Err(Error::Conflict);
        }
        if intent
            .provider_transaction
            .as_ref()
            .is_some_and(|id| id != &verified.transaction)
        {
            return Err(Error::Conflict);
        }
        Ok(verified)
    }

    /// Persist independently verified, fresh provider input before the extension
    /// acknowledges delivery. This receipt does not represent applied money.
    pub async fn accept_proof(
        &self,
        request: pb::AcceptProviderProofRequest,
    ) -> Result<pb::ProviderProofReceipt> {
        let evidence = request.evidence.as_ref().ok_or(Error::Invalid)?;
        if !evidence.accepted_proof_id.is_empty() {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        // Freshness is checked exclusively against Rust's own wall clock here.
        // Unknown commit results are recovered by get_proof, not stale re-signing.
        let verified = self.verify(&intent, evidence)?;
        if let Some(hold_id) = &verified.hold_receipt_id {
            let amount: Option<i64> = sqlx::query_scalar(
                "SELECT amount_minor FROM core_payments.refund_holds WHERE receipt_id=$1 AND intent_id=$2 FOR SHARE",
            )
            .bind(hold_id)
            .bind(intent.id)
            .fetch_optional(&mut *tx)
            .await?;
            if amount != Some(verified.amount_minor) {
                return Err(Error::Denied);
            }
        }
        let digest = Sha256::digest(&evidence.signed_payload).to_vec();
        let prior: Option<(i64, Vec<u8>, Vec<u8>)> = sqlx::query_as(
            "SELECT intent_id,payload_hash,receipt_wire FROM core_payments.accepted_proofs WHERE channel_id=$1 AND event_id=$2",
        )
        .bind(intent.channel_id)
        .bind(&verified.event_id)
        .fetch_optional(&mut *tx)
        .await?;
        if let Some((id, old_digest, wire)) = prior {
            if id != intent.id || old_digest != digest {
                return Err(Error::Conflict);
            }
            let receipt =
                pb::ProviderProofReceipt::decode(wire.as_slice()).map_err(|_| Error::Storage)?;
            tx.commit().await?;
            return Ok(receipt);
        }
        let proof_hash = hash(json!([
            intent.channel_id,
            intent.id,
            verified.event_id,
            digest
        ]));
        use std::fmt::Write;
        let mut proof_id = "proof_".to_owned();
        for byte in proof_hash {
            let _ = write!(proof_id, "{byte:02x}");
        }
        let receipt = pb::ProviderProofReceipt {
            proof_id,
            intent_id: intent.id,
            provider_event_id: verified.event_id.clone(),
            payload_sha256: digest.clone(),
            payment: evidence.payment.clone(),
            accepted_unix_seconds: now()?,
        };
        sqlx::query("INSERT INTO core_payments.accepted_proofs(id,channel_id,event_id,intent_id,payload_hash,signed_payload,signature,verified_facts,receipt_wire) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9)")
            .bind(&receipt.proof_id).bind(intent.channel_id).bind(verified.event_id.clone()).bind(intent.id)
            .bind(digest).bind(&evidence.signed_payload).bind(&evidence.signature).bind(Json(verified)).bind(receipt.encode_to_vec())
            .execute(&mut *tx).await?;
        tx.commit().await?;
        Ok(receipt)
    }

    /// Only a separately authorized service principal may reconcile ingress.
    /// The caller must already know the scoped intent, event and exact digest.
    pub async fn get_proof(
        &self,
        request: pb::GetProviderProofRequest,
    ) -> Result<pb::ProviderProofReceipt> {
        if request.intent_id <= 0
            || !valid_id(&request.provider_event_id)
            || request.payload_sha256.len() != 32
        {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        self.channel(&intent)?;
        let row: Option<(Vec<u8>, Vec<u8>)> = sqlx::query_as(
            "SELECT payload_hash,receipt_wire FROM core_payments.accepted_proofs WHERE intent_id=$1 AND channel_id=$2 AND event_id=$3",
        ).bind(intent.id).bind(intent.channel_id).bind(&request.provider_event_id)
            .fetch_optional(&mut *tx).await?;
        let (digest, wire) = row.ok_or(Error::NotFound)?;
        if digest != request.payload_sha256 {
            return Err(Error::Conflict);
        }
        let receipt =
            pb::ProviderProofReceipt::decode(wire.as_slice()).map_err(|_| Error::Storage)?;
        tx.commit().await?;
        Ok(receipt)
    }

    async fn accepted(
        &self,
        connection: &mut PgConnection,
        intent: &IntentRow,
        evidence: &pb::ProviderEvidence,
    ) -> Result<stripe::Verified> {
        if evidence.accepted_proof_id.len() != 70
            || !evidence.accepted_proof_id.starts_with("proof_")
            || evidence.signed_payload.is_empty()
            || evidence.signed_payload.len() > 32768
        {
            return Err(Error::Denied);
        }
        let row: Option<(Vec<u8>, Vec<u8>, Json<stripe::Verified>)> = sqlx::query_as(
            "SELECT payload_hash,receipt_wire,verified_facts FROM core_payments.accepted_proofs WHERE id=$1 AND intent_id=$2 AND channel_id=$3 AND event_id=$4",
        ).bind(&evidence.accepted_proof_id).bind(intent.id).bind(intent.channel_id)
            .bind(&evidence.provider_event_id).fetch_optional(connection).await?;
        let (digest, wire, Json(verified)) = row.ok_or(Error::Denied)?;
        let receipt =
            pb::ProviderProofReceipt::decode(wire.as_slice()).map_err(|_| Error::Storage)?;
        if digest != Sha256::digest(&evidence.signed_payload).to_vec()
            || receipt.payment != evidence.payment
            || intent
                .provider_transaction
                .as_ref()
                .is_some_and(|id| id != &verified.transaction)
        {
            return Err(Error::Conflict);
        }
        // No provider secret or current timestamp is consulted for a previously
        // accepted proof; its immutable Rust facts survive delay and rotation.
        Ok(verified)
    }
    async fn event(
        connection: &mut PgConnection,
        intent: &IntentRow,
        evidence: &pb::ProviderEvidence,
        receipt: &pb::PaymentReceipt,
    ) -> Result<()> {
        sqlx::query("INSERT INTO core_payments.provider_events(channel_id,event_id,intent_id,payload_hash,receipt_id) VALUES($1,$2,$3,$4,$5)")
            .bind(intent.channel_id).bind(&evidence.provider_event_id).bind(intent.id).bind(Sha256::digest(&evidence.signed_payload).to_vec()).bind(&receipt.receipt_id).execute(connection).await?;
        Ok(())
    }

    pub async fn prepare(
        &self,
        credential: &str,
        request: pb::PrepareTopupRequest,
    ) -> Result<pb::PaymentIntent> {
        let key = mutation(&request.mutation)?;
        let channel = self
            .channels
            .get(&request.payment_channel_id)
            .ok_or(Error::Unavailable)?;
        if request.currency != channel.currency
            || request.amount_minor <= 0
            || request.amount_minor > channel.max_amount_minor
        {
            return Err(Error::Invalid);
        }
        let credit_units = request
            .amount_minor
            .checked_mul(channel.credits_per_minor)
            .ok_or(Error::Invalid)?;
        let request_hash = hash(json!([
            request.account_id,
            request.payment_channel_id,
            request.currency,
            request.amount_minor
        ]));
        let mut tx = self.begin().await?;
        let actor = Self::authorize(&mut tx, credential, request.account_id).await?;
        sqlx::query("SELECT pg_advisory_xact_lock(hashtextextended($1,0))")
            .bind(format!("payment-prepare:{actor}:{key}"))
            .execute(&mut *tx)
            .await?;
        let prior: Option<(Vec<u8>,Option<Vec<u8>>)> = sqlx::query_as("SELECT prepare_hash,intent_wire FROM core_payments.intents WHERE created_by_user_id=$1 AND prepare_key=$2")
            .bind(actor).bind(key).fetch_optional(&mut *tx).await?;
        if let Some((old_hash, wire)) = prior {
            if old_hash != request_hash {
                return Err(Error::Conflict);
            }
            let intent = pb::PaymentIntent::decode(wire.ok_or(Error::Storage)?.as_slice())
                .map_err(|_| Error::Storage)?;
            if (
                intent.provider.as_str(),
                intent.merchant.as_str(),
                intent.environment.as_str(),
                intent.currency.as_str(),
                intent.credit_units,
            ) != (
                channel.provider.as_str(),
                channel.merchant.as_str(),
                channel.environment.as_str(),
                channel.currency.as_str(),
                credit_units,
            ) {
                return Err(Error::Conflict);
            }
            tx.commit().await?;
            return Ok(intent);
        }
        // Preserve original intent reconciliation and provider transitions.
        // Only creation of a new externally payable order is denied.
        identity::reject_marked_new_action(
            &mut tx,
            actor,
            identity::NewResourceAction::PaymentIntent,
        )
        .await?;
        let id: i64 = sqlx::query_scalar("INSERT INTO core_payments.intents(account_id,created_by_user_id,channel_id,provider,merchant,environment,currency,amount_minor,credit_units,credits_per_minor,prepare_key,prepare_hash) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12) RETURNING id")
            .bind(request.account_id).bind(actor).bind(channel.id).bind(&channel.provider).bind(&channel.merchant).bind(&channel.environment).bind(&channel.currency).bind(request.amount_minor).bind(credit_units).bind(channel.credits_per_minor).bind(key).bind(&request_hash).fetch_one(&mut *tx).await?;
        let intent = pb::PaymentIntent {
            intent_id: id,
            account_id: request.account_id,
            created_by_user_id: actor,
            provider: channel.provider.clone(),
            merchant: channel.merchant.clone(),
            environment: channel.environment.clone(),
            currency: channel.currency.clone(),
            amount_minor: request.amount_minor,
            credit_units,
            credit_unit: CREDIT_UNIT.into(),
            idempotency_key: key.into(),
            request_sha256: request_hash.clone(),
            receipt_id: receipt_id(id, key),
        };
        sqlx::query("UPDATE core_payments.intents SET intent_wire=$2 WHERE id=$1")
            .bind(id)
            .bind(intent.encode_to_vec())
            .execute(&mut *tx)
            .await?;
        Self::save(&mut tx, id, key, "prepare", request_hash, PREPARED, 0, "").await?;
        tx.commit().await?;
        Ok(intent)
    }

    pub async fn credit(&self, request: pb::CreditTopupRequest) -> Result<pb::PaymentReceipt> {
        let key = mutation(&request.mutation)?;
        let evidence = request.evidence.as_ref().ok_or(Error::Invalid)?;
        let request_hash = evidence_hash("credit", request.intent_id, "", evidence)?;
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        self.channel(&intent)?;
        let verified = self.accepted(&mut tx, &intent, evidence).await?;
        if let Some(prior) = Self::prior(&mut tx, intent.id, key, "credit", &request_hash).await? {
            tx.commit().await?;
            return Ok(prior);
        }
        if verified.kind != stripe::EventKind::Credit {
            return Err(Error::Invalid);
        }
        let receipt = if intent.refund_fenced || intent.credit_journal_id.is_some() {
            Self::save(
                &mut tx,
                intent.id,
                key,
                "credit",
                request_hash,
                6,
                0,
                if intent.refund_fenced {
                    "refund_fence"
                } else {
                    "already_credited"
                },
            )
            .await?
        } else {
            sqlx::query("SELECT core_billing.open_ledger_wallet($1)")
                .bind(intent.account_id)
                .execute(&mut *tx)
                .await?;
            let reference_digest = hash(json!([
                "stripe",
                intent.merchant,
                intent.environment,
                verified.transaction
            ]));
            use std::fmt::Write;
            let mut payment_reference = "stripe:".to_owned();
            for byte in reference_digest {
                let _ = write!(payment_reference, "{byte:02x}");
            }
            let outcome = Self::post(
                &mut tx,
                intent.id,
                key,
                "credit",
                Some(intent.created_by_user_id),
                Action::Credit {
                    account_id: intent.account_id,
                    amount_units: units(intent.credit_units)?,
                    payment_reference,
                },
            )
            .await?;
            let receipt =
                Self::record_outcome(&mut tx, intent.id, key, "credit", request_hash, 1, outcome)
                    .await?;
            if receipt.state == 1 {
                sqlx::query("UPDATE core_payments.intents SET credit_journal_id=$2,provider_transaction=$3 WHERE id=$1")
                    .bind(intent.id).bind(receipt.journal_id).bind(&verified.transaction).execute(&mut *tx).await?;
            }
            receipt
        };
        Self::event(&mut tx, &intent, evidence, &receipt).await?;
        tx.commit().await?;
        Ok(receipt)
    }

    pub async fn hold(
        &self,
        credential: &str,
        request: pb::HoldRefundRequest,
    ) -> Result<pb::PaymentReceipt> {
        let key = mutation(&request.mutation)?;
        let request_hash = hash(json!([
            "hold",
            request.intent_id,
            request.refund_amount_minor
        ]));
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        let actor = Self::authorize(&mut tx, credential, intent.account_id).await?;
        if let Some(prior) = Self::prior(&mut tx, intent.id, key, "hold", &request_hash).await? {
            tx.commit().await?;
            return Ok(prior);
        }
        if request.refund_amount_minor <= 0 || intent.credit_journal_id.is_none() {
            return Err(Error::Invalid);
        }
        let blocked: bool = sqlx::query_scalar(
            "SELECT EXISTS(SELECT 1 FROM core_payments.blocked_refunds WHERE intent_id=$1)",
        )
        .bind(intent.id)
        .fetch_one(&mut *tx)
        .await?;
        if blocked {
            return Err(Error::Unavailable);
        }
        let held: i64 = sqlx::query_scalar("SELECT COALESCE(sum(amount_minor),0)::bigint FROM core_payments.refund_holds WHERE intent_id=$1 AND state='held'")
            .bind(intent.id).fetch_one(&mut *tx).await?;
        if request.refund_amount_minor
            > intent
                .amount_minor
                .checked_sub(intent.refunded_minor)
                .and_then(|v| v.checked_sub(held))
                .ok_or(Error::Conflict)?
        {
            return Err(Error::Conflict);
        }
        let amount = request
            .refund_amount_minor
            .checked_mul(intent.credits_per_minor)
            .ok_or(Error::Invalid)?;
        let outcome = Self::post(
            &mut tx,
            intent.id,
            key,
            "hold",
            Some(actor),
            Action::Reserve {
                account_id: intent.account_id,
                amount_units: units(amount)?,
            },
        )
        .await?;
        let receipt =
            Self::record_outcome(&mut tx, intent.id, key, "hold", request_hash, 2, outcome).await?;
        if receipt.state == 2 {
            sqlx::query("INSERT INTO core_payments.refund_holds(receipt_id,intent_id,amount_minor,credit_units,reservation_journal_id) VALUES($1,$2,$3,$4,$5)")
                .bind(&receipt.receipt_id).bind(intent.id).bind(request.refund_amount_minor).bind(amount).bind(receipt.journal_id).execute(&mut *tx).await?;
        }
        tx.commit().await?;
        Ok(receipt)
    }

    /// Unknown/timeouts never enter here as a failure proof. Missing evidence is
    /// rejected and the original reservation remains held indefinitely.
    pub async fn resolve(
        &self,
        request: pb::ResolveRefundRequest,
        commit: bool,
    ) -> Result<pb::PaymentReceipt> {
        // A signed failed refund does not prove that another provider attempt
        // for this hold was never submitted. Until core owns/binds dispatch,
        // releasing on that evidence could free money already refunded by B.
        if !commit {
            return Err(Error::Unavailable);
        }
        let key = mutation(&request.mutation)?;
        let method = if commit { "commit" } else { "release" };
        let evidence = request.evidence.as_ref().ok_or(Error::Invalid)?;
        let request_hash = evidence_hash(
            method,
            request.intent_id,
            &request.hold_receipt_id,
            evidence,
        )?;
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        self.channel(&intent)?;
        let verified = self.accepted(&mut tx, &intent, evidence).await?;
        if let Some(prior) = Self::prior(&mut tx, intent.id, key, method, &request_hash).await? {
            tx.commit().await?;
            return Ok(prior);
        }
        let wanted = if commit {
            stripe::EventKind::RefundSucceeded
        } else {
            stripe::EventKind::RefundFailed
        };
        if verified.kind != wanted
            || verified.hold_receipt_id.as_deref() != Some(&request.hold_receipt_id)
        {
            return Err(Error::Denied);
        }
        let hold: HoldRow = sqlx::query_as("SELECT receipt_id,amount_minor,credit_units,reservation_journal_id,state FROM core_payments.refund_holds WHERE receipt_id=$1 AND intent_id=$2 FOR UPDATE")
            .bind(&request.hold_receipt_id).bind(intent.id).fetch_optional(&mut *tx).await?.ok_or(Error::NotFound)?;
        if hold.state != "held"
            || hold.amount_minor != verified.amount_minor
            || intent.credit_journal_id.is_none()
        {
            return Err(Error::Conflict);
        }
        let refund_id = verified.refund_id.ok_or(Error::Invalid)?;
        Self::release_hold(&mut tx, &intent, key, &hold).await?;
        let receipt = if commit {
            let outcome = Self::post(
                &mut tx,
                intent.id,
                key,
                "refund",
                None,
                Action::Refund {
                    journal_id: intent.credit_journal_id.ok_or(Error::Conflict)?,
                    amount_units: units(hold.credit_units)?,
                },
            )
            .await?;
            // Failure of the compensation rolls back the RELEASE as well. Never
            // leave the provider refund confirmed while its money becomes free.
            let Outcome::Posted { journal_id, .. } = outcome else {
                return Err(Error::Conflict);
            };
            sqlx::query("UPDATE core_payments.intents SET refunded_minor=refunded_minor+$2,refund_fenced=TRUE WHERE id=$1")
                .bind(intent.id).bind(hold.amount_minor).execute(&mut *tx).await?;
            Self::save(
                &mut tx,
                intent.id,
                key,
                method,
                request_hash,
                3,
                journal_id,
                "",
            )
            .await?
        } else {
            Self::save(
                &mut tx,
                intent.id,
                key,
                method,
                request_hash,
                4,
                hold.reservation_journal_id,
                "",
            )
            .await?
        };
        sqlx::query("UPDATE core_payments.refund_holds SET state=$2,provider_refund_id=$3 WHERE receipt_id=$1")
            .bind(&hold.receipt_id).bind(if commit {"committed"} else {"released"}).bind(&refund_id).execute(&mut *tx).await?;
        if commit {
            Self::refund_event(&mut tx, &intent, &refund_id, hold.amount_minor, &receipt).await?;
        }
        Self::event(&mut tx, &intent, evidence, &receipt).await?;
        tx.commit().await?;
        Ok(receipt)
    }
    async fn release_hold(
        connection: &mut PgConnection,
        intent: &IntentRow,
        key: &str,
        hold: &HoldRow,
    ) -> Result<()> {
        match Self::post(
            connection,
            intent.id,
            key,
            "unhold",
            None,
            Action::Release {
                reservation_journal_id: hold.reservation_journal_id,
            },
        )
        .await?
        {
            Outcome::Posted { .. } => Ok(()),
            Outcome::Rejected { .. } => Err(Error::Conflict),
        }
    }
    async fn refund_event(
        connection: &mut PgConnection,
        intent: &IntentRow,
        refund: &str,
        amount: i64,
        receipt: &pb::PaymentReceipt,
    ) -> Result<()> {
        sqlx::query("INSERT INTO core_payments.provider_refunds(channel_id,refund_id,intent_id,amount_minor,receipt_id) VALUES($1,$2,$3,$4,$5)")
            .bind(intent.channel_id).bind(refund).bind(intent.id).bind(amount).bind(&receipt.receipt_id).execute(connection).await?;
        Ok(())
    }
    async fn block_external(
        tx: &mut Transaction<'_, Postgres>,
        intent: &IntentRow,
        evidence: &pb::ProviderEvidence,
        verified: &stripe::Verified,
        reason: &str,
    ) -> Result<()> {
        // Retain the original independently accepted signature, rather than
        // trusting any altered delivery header carried by a later worker.
        let signature: Vec<u8> = sqlx::query_scalar(
            "SELECT signature FROM core_payments.accepted_proofs WHERE id=$1 AND intent_id=$2",
        )
        .bind(&evidence.accepted_proof_id)
        .bind(intent.id)
        .fetch_one(&mut **tx)
        .await?;
        sqlx::query("INSERT INTO core_payments.blocked_refunds(channel_id,event_id,refund_id,intent_id,transaction_id,amount_minor,payload_hash,signed_payload,signature,reason) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)")
            .bind(intent.channel_id).bind(&verified.event_id).bind(verified.refund_id.as_deref().ok_or(Error::Invalid)?).bind(intent.id).bind(&verified.transaction).bind(verified.amount_minor)
            .bind(Sha256::digest(&evidence.signed_payload).to_vec()).bind(&evidence.signed_payload).bind(signature).bind(reason).execute(&mut **tx).await?;
        sqlx::query("UPDATE core_payments.intents SET refund_fenced=TRUE WHERE id=$1")
            .bind(intent.id)
            .execute(&mut **tx)
            .await?;
        Ok(())
    }

    pub async fn external_refund(
        &self,
        request: pb::ExternalRefundRequest,
    ) -> Result<pb::PaymentReceipt> {
        let key = mutation(&request.mutation)?;
        let evidence = request.evidence.as_ref().ok_or(Error::Invalid)?;
        let request_hash = evidence_hash("external_refund", request.intent_id, "", evidence)?;
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        self.channel(&intent)?;
        let verified = self.accepted(&mut tx, &intent, evidence).await?;
        if let Some(prior) =
            Self::prior(&mut tx, intent.id, key, "external_refund", &request_hash).await?
        {
            tx.commit().await?;
            return Ok(prior);
        }
        if verified.kind != stripe::EventKind::RefundSucceeded {
            return Err(Error::Invalid);
        }
        let refund_id = verified.refund_id.as_deref().ok_or(Error::Invalid)?;
        let blocked: bool = sqlx::query_scalar("SELECT EXISTS(SELECT 1 FROM core_payments.blocked_refunds WHERE channel_id=$1 AND refund_id=$2)")
            .bind(intent.channel_id).bind(refund_id).fetch_one(&mut *tx).await?;
        if blocked {
            return Err(Error::Conflict);
        }
        if verified.amount_minor > intent.amount_minor - intent.refunded_minor {
            return Err(Error::Conflict);
        }
        if intent.credit_journal_id.is_none() && verified.amount_minor != intent.amount_minor {
            // A partial pre-credit refund leaves a nonzero paid remainder.
            // Until a core-owned net-credit policy exists, retain the fact and
            // fence stale full credit without claiming this payment is resolved.
            Self::block_external(
                &mut tx,
                &intent,
                evidence,
                &verified,
                "net_credit_reconciliation_required",
            )
            .await?;
            tx.commit().await?;
            return Err(Error::Conflict);
        }
        let mut journal_id = 0;
        if let Some(credit) = intent.credit_journal_id {
            // An event from our own held refund must resolve exactly that hold;
            // an unbound external refund cannot arbitrarily release other holds.
            let held: Vec<HoldRow> = sqlx::query_as("SELECT receipt_id,amount_minor,credit_units,reservation_journal_id,state FROM core_payments.refund_holds WHERE intent_id=$1 AND state='held' ORDER BY receipt_id FOR UPDATE")
                .bind(intent.id).fetch_all(&mut *tx).await?;
            if let Some(hold_id) = verified.hold_receipt_id.as_ref() {
                let hold = held
                    .iter()
                    .find(|h| &h.receipt_id == hold_id)
                    .ok_or(Error::Conflict)?;
                if hold.amount_minor != verified.amount_minor {
                    return Err(Error::Conflict);
                }
                Self::release_hold(&mut tx, &intent, key, hold).await?;
                sqlx::query("UPDATE core_payments.refund_holds SET state='committed',provider_refund_id=$2 WHERE receipt_id=$1")
                    .bind(hold_id).bind(refund_id).execute(&mut *tx).await?;
            } else if !held.is_empty() {
                Self::block_external(
                    &mut tx,
                    &intent,
                    evidence,
                    &verified,
                    "conflicting_hold_requires_reconciliation",
                )
                .await?;
                tx.commit().await?;
                return Err(Error::Conflict);
            }
            let amount = verified
                .amount_minor
                .checked_mul(intent.credits_per_minor)
                .ok_or(Error::Invalid)?;
            let outcome = Self::post(
                &mut tx,
                intent.id,
                key,
                "refund",
                None,
                Action::Refund {
                    journal_id: credit,
                    amount_units: units(amount)?,
                },
            )
            .await?;
            let Outcome::Posted {
                journal_id: posted, ..
            } = outcome
            else {
                // No own hold was released on this path. Held refunds resolve
                // through resolve() so a failed post always rolls back release.
                if verified.hold_receipt_id.is_some() {
                    return Err(Error::Conflict);
                }
                Self::block_external(
                    &mut tx,
                    &intent,
                    evidence,
                    &verified,
                    "refund_debt_recovery_required",
                )
                .await?;
                tx.commit().await?;
                return Err(Error::Conflict);
            };
            journal_id = posted;
        }
        // Before-credit refunds never create a fake ledger journal. A durable
        // fence prevents any later stale payment-success event from crediting.
        sqlx::query("UPDATE core_payments.intents SET refund_fenced=TRUE,provider_transaction=COALESCE(provider_transaction,$2),refunded_minor=refunded_minor+$3 WHERE id=$1")
            .bind(intent.id).bind(&verified.transaction).bind(verified.amount_minor).execute(&mut *tx).await?;
        let receipt = Self::save(
            &mut tx,
            intent.id,
            key,
            "external_refund",
            request_hash,
            5,
            journal_id,
            "",
        )
        .await?;
        Self::refund_event(&mut tx, &intent, refund_id, verified.amount_minor, &receipt).await?;
        Self::event(&mut tx, &intent, evidence, &receipt).await?;
        tx.commit().await?;
        Ok(receipt)
    }

    pub async fn receipt(
        &self,
        credential: Option<&str>,
        request: pb::GetPaymentReceiptRequest,
    ) -> Result<pb::PaymentReceipt> {
        if !(16..=128).contains(&request.idempotency_key.len()) {
            return Err(Error::Invalid);
        }
        let mut tx = self.begin().await?;
        let intent = Self::intent(&mut tx, request.intent_id).await?;
        if let Some(credential) = credential {
            Self::authorize(&mut tx, credential, intent.account_id).await?;
        }
        // None is allowed only by RPC's explicit reconcile service permission.
        let wire: Vec<u8> = sqlx::query_scalar("SELECT receipt_wire FROM core_payments.receipts WHERE intent_id=$1 AND operation_key=$2")
            .bind(intent.id).bind(&request.idempotency_key).fetch_optional(&mut *tx).await?.ok_or(Error::NotFound)?;
        let receipt = pb::PaymentReceipt::decode(wire.as_slice()).map_err(|_| Error::Storage)?;
        tx.commit().await?;
        Ok(receipt)
    }
}
