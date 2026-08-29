//! Redacted administrator-only finance analysis export.
//!
//! Every production database query is an allowlisted projection. Secret-bearing
//! columns have no representation in this module and cannot enter either output
//! format by accidental whole-row serialization.

use std::{
    collections::{BTreeMap, BTreeSet, HashMap},
    io::{Cursor, Write},
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    body::Body,
    extract::{Request, State},
    http::{
        HeaderMap, HeaderName, HeaderValue, StatusCode,
        header::{self, InvalidHeaderValue},
    },
    response::{IntoResponse, Response},
    routing::get as route_get,
};
use secrecy::SecretString;
use serde::Serialize;
use serde_json::{Value, json};
use sqlx::{PgPool, Row, postgres::PgRow};
use zip::{CompressionMethod, ZipWriter, write::SimpleFileOptions};

use crate::{
    ClientIpKey, RequestContext,
    auth::{DashboardAuth, DashboardUserView, UserAuthPolicyError, enforce_user_auth_view},
};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const DEFAULT_WINDOW_SECONDS: i64 = 30 * 24 * 60 * 60;
const MAX_WINDOW_SECONDS: i64 = 90 * 24 * 60 * 60;
const MAX_ROWS: i64 = 200_000;
const PRICING_VERSION: &str = "5a90f2b86c08bd983a9a2e6d66c255f4eaef9c4bc934386d2b6ae84ef0ff1f1f";

const FILE_NAMES: [&str; 15] = [
    "manifest.json",
    "financial-options.json",
    "model-prices-and-ratios.json",
    "effective-model-pricing.json",
    "users-balances.json",
    "channels-pricing.json",
    "subscription-plans.json",
    "topups.json",
    "subscription-orders.json",
    "subscription-payment-events.json",
    "usage-billing-records.json",
    "bounty-ledger.json",
    "checkins.json",
    "redemptions.json",
    "user-subscriptions.json",
];

const OPTION_KEYS: [&str; 26] = [
    "ModelPrice",
    "ModelRatio",
    "CacheRatio",
    "CreateCacheRatio",
    "CompletionRatio",
    "ImageRatio",
    "AudioRatio",
    "AudioCompletionRatio",
    "GroupRatio",
    "GroupGroupRatio",
    "TopupGroupRatio",
    "UserUsableGroups",
    "QuotaPerUnit",
    "Price",
    "USDExchangeRate",
    "MinTopUp",
    "DataExportInterval",
    "payment_setting.amount_options",
    "payment_setting.amount_discount",
    "tool_price_setting.prices",
    "billing_setting.billing_mode",
    "billing_setting.billing_expr",
    "checkin_setting.enabled",
    "checkin_setting.min_quota",
    "checkin_setting.max_quota",
    "checkin_setting.level_multipliers",
];

/// PostgreSQL and dashboard-auth dependencies for the finance export route.
#[derive(Clone)]
pub struct FinanceExportState {
    backend: Arc<dyn FinanceExportBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl FinanceExportState {
    /// Creates production state backed by the listener's current PostgreSQL pool.
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgFinanceExportBackend { pg }), auth)
    }

    /// Creates state around an alternate backend for route contract tests.
    #[must_use]
    pub fn with_backend(
        backend: Arc<dyn FinanceExportBackend>,
        auth: Arc<dyn DashboardAuth>,
    ) -> Self {
        Self { backend, auth }
    }
}

/// Builds only `GET /api/finance/export`.
pub fn router(state: FinanceExportState) -> Router {
    Router::new()
        .route("/api/finance/export", route_get(export_finance))
        .with_state(state)
}

/// Fully rendered, redacted files returned by an export backend.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct FinanceExportArtifact {
    pub files: BTreeMap<String, Vec<u8>>,
    pub rows: BTreeMap<String, i64>,
}

/// Successful-export audit data. Audit persistence is deliberately best effort.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct FinanceExportAudit {
    pub actor_id: i64,
    pub actor_username: String,
    pub client_ip: String,
    pub format: String,
    pub start_timestamp: i64,
    pub end_timestamp: i64,
    pub rows: BTreeMap<String, i64>,
}

/// Durable export read failure rendered through Go's HTTP-200 error envelope.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct FinanceExportError(pub String);

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
enum FinanceExportFormat {
    Text,
    Zip,
}

impl FinanceExportFormat {
    fn parse(value: &str) -> Result<Self, FinanceExportRequestError> {
        match value {
            "text" => Ok(Self::Text),
            "zip" => Ok(Self::Zip),
            _ => Err(FinanceExportRequestError::UnsupportedFormat),
        }
    }

    const fn as_str(self) -> &'static str {
        match self {
            Self::Text => "text",
            Self::Zip => "zip",
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq, thiserror::Error)]
enum FinanceExportRequestError {
    #[error("format must be zip or text")]
    UnsupportedFormat,
}

#[derive(Debug, thiserror::Error)]
enum FinanceExportBuildError {
    #[error("finance export database query `{operation}` failed: {source}")]
    Database {
        operation: &'static str,
        #[source]
        source: sqlx::Error,
    },
    #[error("finance export database row column `{column}` failed: {source}")]
    Row {
        column: String,
        #[source]
        source: sqlx::Error,
    },
    #[error("finance export JSON serialization failed: {source}")]
    Json {
        #[source]
        source: serde_json::Error,
    },
    #[error("finance export base URI `{value}` is invalid")]
    Uri { value: String },
    #[error("finance export response header `{name}` is invalid: {source}")]
    Header {
        name: &'static str,
        #[source]
        source: InvalidHeaderValue,
    },
    #[error("finance export ZIP entry `{name}` could not be created: {source}")]
    ZipEntry {
        name: &'static str,
        #[source]
        source: zip::result::ZipError,
    },
    #[error("finance export ZIP entry `{name}` bytes could not be written: {source}")]
    ZipBytes {
        name: &'static str,
        #[source]
        source: std::io::Error,
    },
    #[error("finance export ZIP could not be finalized: {source}")]
    ZipFinish {
        #[source]
        source: zip::result::ZipError,
    },
}

impl From<FinanceExportBuildError> for FinanceExportError {
    fn from(error: FinanceExportBuildError) -> Self {
        Self(error.to_string())
    }
}

/// Storage boundary used to keep route tests independent from live finance data.
#[async_trait]
pub trait FinanceExportBackend: Send + Sync {
    async fn build(
        &self,
        start_timestamp: i64,
        end_timestamp: i64,
        generated_at: i64,
    ) -> Result<FinanceExportArtifact, FinanceExportError>;

    async fn record_audit(&self, audit: &FinanceExportAudit);
}

#[derive(Clone)]
struct PgFinanceExportBackend {
    pg: PgPool,
}

#[async_trait]
impl FinanceExportBackend for PgFinanceExportBackend {
    async fn build(
        &self,
        start_timestamp: i64,
        end_timestamp: i64,
        generated_at: i64,
    ) -> Result<FinanceExportArtifact, FinanceExportError> {
        let options = load_options(&self.pg).await?;
        let effective_pricing = load_effective_pricing(&self.pg, &options).await?;
        let mut users = load_users(&self.pg).await?;
        apply_user_ratios(&mut users, &options);
        let channels = load_channels(&self.pg).await?;
        let plans = load_plans(&self.pg).await?;
        let topups = load_topups(&self.pg, start_timestamp, end_timestamp).await?;
        let orders = load_subscription_orders(&self.pg, start_timestamp, end_timestamp).await?;
        let subscription_payments =
            load_subscription_payments(&self.pg, start_timestamp, end_timestamp).await?;
        let usage = load_usage(&self.pg, start_timestamp, end_timestamp).await?;
        let bounty_ledger = load_bounty_ledger(&self.pg, start_timestamp, end_timestamp).await?;
        let checkins = load_checkins(&self.pg, start_timestamp, end_timestamp).await?;
        let redemptions = load_redemptions(&self.pg).await?;
        let user_subscriptions = load_user_subscriptions(&self.pg).await?;

        let rows = BTreeMap::from([
            ("options".to_owned(), usize_to_i64(options.len())),
            (
                "effective_model_pricing".to_owned(),
                usize_to_i64(effective_pricing.len()),
            ),
            ("users".to_owned(), usize_to_i64(users.len())),
            ("channels".to_owned(), usize_to_i64(channels.len())),
            ("plans".to_owned(), usize_to_i64(plans.len())),
            ("topups".to_owned(), usize_to_i64(topups.len())),
            ("subscription_orders".to_owned(), usize_to_i64(orders.len())),
            (
                "subscription_payment_events".to_owned(),
                usize_to_i64(subscription_payments.len()),
            ),
            ("usage".to_owned(), usize_to_i64(usage.len())),
            (
                "bounty_ledger".to_owned(),
                usize_to_i64(bounty_ledger.len()),
            ),
            ("checkins".to_owned(), usize_to_i64(checkins.len())),
            ("redemptions".to_owned(), usize_to_i64(redemptions.len())),
            (
                "user_subscriptions".to_owned(),
                usize_to_i64(user_subscriptions.len()),
            ),
        ]);
        let truncated = BTreeMap::from([
            ("topups".to_owned(), usize_to_i64(topups.len()) == MAX_ROWS),
            (
                "subscription_orders".to_owned(),
                usize_to_i64(orders.len()) == MAX_ROWS,
            ),
            (
                "subscription_payment_events".to_owned(),
                usize_to_i64(subscription_payments.len()) == MAX_ROWS,
            ),
            ("usage".to_owned(), usize_to_i64(usage.len()) == MAX_ROWS),
            (
                "bounty_ledger".to_owned(),
                usize_to_i64(bounty_ledger.len()) == MAX_ROWS,
            ),
            (
                "checkins".to_owned(),
                usize_to_i64(checkins.len()) == MAX_ROWS,
            ),
            (
                "redemptions".to_owned(),
                usize_to_i64(redemptions.len()) == MAX_ROWS,
            ),
            (
                "user_subscriptions".to_owned(),
                usize_to_i64(user_subscriptions.len()) == MAX_ROWS,
            ),
        ]);
        let manifest = FinanceManifest {
            schema_version: "lmm-finance-export/v1",
            generated_at,
            start_timestamp,
            end_timestamp,
            redactions: [
                "user passwords, access tokens, API keys, channel keys, redemption keys, provider payloads, IPs, request bodies, opaque log fields, channel remarks, URL credentials/query strings, trade/provider event identifiers",
            ],
            rows: rows.clone(),
            truncated,
            notes: [
                "usage, top-up, subscription-order, check-in, and bounty-ledger rows are limited to the requested time window",
                "channels-pricing includes configured channel balances, model lists, and model mappings; it does not make live upstream requests",
                "redemptions exclude redemption keys and include all non-deleted codes subject to the row limit",
                "user-subscriptions contains quota entitlement snapshots; payment trade/provider payloads remain excluded",
                "the export is an analysis snapshot, not an accounting ledger of record",
            ],
        };
        let model_prices = ModelPricesFile {
            model_prices: decode_option(&options, "ModelPrice"),
            model_ratios: decode_option(&options, "ModelRatio"),
            completion_ratios: decode_option(&options, "CompletionRatio"),
            cache_ratios: decode_option(&options, "CacheRatio"),
            create_cache_ratios: decode_option(&options, "CreateCacheRatio"),
            image_ratios: decode_option(&options, "ImageRatio"),
            audio_ratios: decode_option(&options, "AudioRatio"),
            audio_completion_ratios: decode_option(&options, "AudioCompletionRatio"),
            tool_prices: decode_option(&options, "tool_price_setting.prices"),
            billing_modes: decode_option(&options, "billing_setting.billing_mode"),
            billing_expressions: decode_option(&options, "billing_setting.billing_expr"),
        };

        let files = BTreeMap::from([
            ("manifest.json".to_owned(), go_pretty_json(&manifest)?),
            (
                "financial-options.json".to_owned(),
                go_pretty_json(&options)?,
            ),
            (
                "model-prices-and-ratios.json".to_owned(),
                go_pretty_json(&model_prices)?,
            ),
            (
                "effective-model-pricing.json".to_owned(),
                go_pretty_json(&effective_pricing)?,
            ),
            ("users-balances.json".to_owned(), go_pretty_json(&users)?),
            (
                "channels-pricing.json".to_owned(),
                go_pretty_json(&channels)?,
            ),
            (
                "subscription-plans.json".to_owned(),
                go_pretty_json(&plans)?,
            ),
            ("topups.json".to_owned(), go_pretty_json(&topups)?),
            (
                "subscription-orders.json".to_owned(),
                go_pretty_json(&orders)?,
            ),
            (
                "subscription-payment-events.json".to_owned(),
                go_pretty_json(&subscription_payments)?,
            ),
            (
                "usage-billing-records.json".to_owned(),
                go_pretty_json(&usage)?,
            ),
            (
                "bounty-ledger.json".to_owned(),
                go_pretty_json(&bounty_ledger)?,
            ),
            ("checkins.json".to_owned(), go_pretty_json(&checkins)?),
            ("redemptions.json".to_owned(), go_pretty_json(&redemptions)?),
            (
                "user-subscriptions.json".to_owned(),
                go_pretty_json(&user_subscriptions)?,
            ),
        ]);
        Ok(FinanceExportArtifact { files, rows })
    }

    async fn record_audit(&self, audit: &FinanceExportAudit) {
        let other = json!({
            "op": {
                "action": "finance.export",
                "params": {
                    "format": audit.format,
                    "start_timestamp": audit.start_timestamp,
                    "end_timestamp": audit.end_timestamp,
                    "rows": audit.rows,
                },
            },
        });
        let _ = sqlx::query(
            "INSERT INTO logs (user_id, created_at, type, content, username, ip, other) \
             VALUES ($1, EXTRACT(EPOCH FROM NOW())::BIGINT, 3, \
             'Financial analysis export generated', $2, $3, $4)",
        )
        .bind(audit.actor_id)
        .bind(&audit.actor_username)
        .bind(&audit.client_ip)
        .bind(other.to_string())
        .execute(&self.pg)
        .await;
    }
}

#[derive(Serialize)]
struct FinanceManifest {
    schema_version: &'static str,
    generated_at: i64,
    start_timestamp: i64,
    end_timestamp: i64,
    redactions: [&'static str; 1],
    rows: BTreeMap<String, i64>,
    truncated: BTreeMap<String, bool>,
    notes: [&'static str; 5],
}

#[derive(Serialize)]
struct ModelPricesFile {
    model_prices: Value,
    model_ratios: Value,
    completion_ratios: Value,
    cache_ratios: Value,
    create_cache_ratios: Value,
    image_ratios: Value,
    audio_ratios: Value,
    audio_completion_ratios: Value,
    tool_prices: Value,
    billing_modes: Value,
    billing_expressions: Value,
}

#[derive(Debug, Serialize)]
struct FinanceUser {
    user_id: i64,
    username: String,
    role: i64,
    status: i64,
    group: String,
    quota: i64,
    used_quota: i64,
    affiliate_quota: i64,
    affiliate_history_quota: i64,
    request_count: i64,
    created_at: i64,
    last_api_activity_at: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    trust_level_override: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    effective_group_ratio: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    effective_topup_group_ratio: Option<f64>,
}

#[derive(Debug, Serialize)]
struct FinanceChannel {
    channel_id: i64,
    r#type: i64,
    status: i64,
    name: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    weight: Option<i64>,
    created_time: i64,
    test_time: i64,
    response_time: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    base_url: Option<String>,
    balance: f64,
    balance_updated_time: i64,
    models: String,
    #[serde(skip_serializing_if = "Option::is_none")]
    model_mapping: Option<String>,
    group: String,
    used_quota: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    priority: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    auto_ban: Option<i64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    tag: Option<String>,
}

#[derive(Debug, Serialize)]
struct FinancePlan {
    plan_id: i64,
    title: String,
    subtitle: String,
    price_amount: f64,
    currency: String,
    duration_unit: String,
    duration_value: i64,
    custom_seconds: i64,
    enabled: bool,
    sort_order: i64,
    #[serde(skip_serializing_if = "Option::is_none")]
    allow_balance_pay: Option<bool>,
    #[serde(skip_serializing_if = "Option::is_none")]
    allow_wallet_overflow: Option<bool>,
    max_purchase_per_user: i64,
    upgrade_group: String,
    downgrade_group: String,
    total_amount: i64,
    quota_reset_period: String,
    quota_reset_custom_seconds: i64,
    created_at: i64,
    updated_at: i64,
}

#[derive(Debug, Serialize)]
struct FinanceTopup {
    topup_id: i64,
    user_id: i64,
    amount: i64,
    platform_amount_micros: i64,
    credited_quota: i64,
    expected_amount_micros: i64,
    settled_amount_micros: i64,
    settlement_currency: String,
    money: f64,
    payment_method: String,
    payment_provider: String,
    create_time: i64,
    complete_time: i64,
    status: String,
}

#[derive(Debug, Serialize)]
struct FinanceSubscriptionOrder {
    order_id: i64,
    user_id: i64,
    plan_id: i64,
    #[serde(rename = "plan_price")]
    money: f64,
    plan_currency: String,
    expected_amount_micros: i64,
    settlement_currency: String,
    payment_method: String,
    payment_provider: String,
    provider_product_id: String,
    provider_subscription_state: String,
    current_period_start: i64,
    current_period_end: i64,
    refunded_amount_micros: i64,
    status: String,
    create_time: i64,
    complete_time: i64,
}

#[derive(Debug, Serialize)]
struct FinanceSubscriptionPayment {
    payment_event_id: i64,
    subscription_order_id: i64,
    payment_provider: String,
    settlement_currency: String,
    settlement_amount_micros: i64,
    period_start: i64,
    period_end: i64,
    created_time: i64,
}

#[derive(Debug, Serialize)]
struct FinanceUsage {
    log_id: i64,
    user_id: i64,
    created_at: i64,
    r#type: i64,
    username: String,
    token_name: String,
    model_name: String,
    quota: i64,
    prompt_tokens: i64,
    completion_tokens: i64,
    use_time: i64,
    is_stream: bool,
    channel_id: i64,
    group: String,
}

#[derive(Debug, Serialize)]
struct FinanceBountyLedger {
    ledger_id: i64,
    project_id: i64,
    challenge_id: i64,
    user_id: i64,
    counterparty_user_id: i64,
    kind: String,
    quota: i64,
    note: String,
    created_at: i64,
}

#[derive(Debug, Serialize)]
struct FinanceCheckin {
    checkin_id: i64,
    user_id: i64,
    checkin_date: String,
    quota_awarded: i64,
    created_at: i64,
}

#[derive(Debug, Serialize)]
struct FinanceRedemption {
    redemption_id: i64,
    created_by_user_id: i64,
    status: i64,
    name: String,
    quota: i64,
    created_time: i64,
    redeemed_time: i64,
    used_user_id: i64,
    expired_time: i64,
}

#[derive(Debug, Serialize)]
struct FinanceUserSubscription {
    subscription_id: i64,
    user_id: i64,
    plan_id: i64,
    amount_total: i64,
    amount_used: i64,
    start_time: i64,
    end_time: i64,
    status: String,
    source: String,
    last_reset_time: i64,
    next_reset_time: i64,
    upgrade_group: String,
    previous_user_group: String,
    downgrade_group: String,
    allow_wallet_overflow: bool,
    created_at: i64,
    updated_at: i64,
}

#[derive(Debug, Serialize)]
struct EffectivePricing {
    model_name: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    description: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    icon: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    tags: String,
    #[serde(skip_serializing_if = "is_zero")]
    vendor_id: i64,
    quota_type: i64,
    model_ratio: f64,
    model_price: f64,
    owner_by: String,
    completion_ratio: f64,
    #[serde(skip_serializing_if = "Option::is_none")]
    cache_ratio: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    create_cache_ratio: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    image_ratio: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    audio_ratio: Option<f64>,
    #[serde(skip_serializing_if = "Option::is_none")]
    audio_completion_ratio: Option<f64>,
    enable_groups: Vec<String>,
    supported_endpoint_types: Vec<String>,
    #[serde(skip_serializing_if = "String::is_empty")]
    billing_mode: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    billing_expr: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    pricing_version: String,
}

#[derive(Clone, Debug)]
struct ModelMeta {
    model_name: String,
    description: String,
    icon: String,
    tags: String,
    vendor_id: i64,
    endpoints: String,
    status: i64,
    name_rule: i64,
}

#[derive(Clone, Debug)]
struct Ability {
    group: String,
    model: String,
    channel_type: i64,
    channel_settings: String,
}

async fn export_finance(State(state): State<FinanceExportState>, request: Request) -> Response {
    let headers = request.headers().clone();
    let raw_query = request.uri().query().map(str::to_owned);
    let client_ip = request
        .extensions()
        .get::<ClientIpKey>()
        .map(|value| value.0.clone())
        .or_else(|| {
            request
                .extensions()
                .get::<RequestContext>()
                .and_then(|context| context.client_ip)
                .map(|ip| ip.to_string())
        })
        .map_or_else(String::new, |value| value);
    drop(request);

    let principal = match authenticated_admin(&state, &headers).await {
        Ok(principal) => principal,
        Err(response) => return response,
    };
    let query = parse_query(raw_query.as_deref());
    let raw_format = query
        .get("format")
        .map_or("zip", |value| value.trim())
        .to_ascii_lowercase();
    let format = match FinanceExportFormat::parse(&raw_format) {
        Ok(format) => format,
        Err(error) => return authenticated_handler_response(api_error(&error.to_string())),
    };
    let (start_timestamp, end_timestamp) = match export_window(&query, unix_timestamp()) {
        Ok(window) => window,
        Err(message) => return authenticated_handler_response(api_error(message)),
    };
    let artifact = match state
        .backend
        .build(start_timestamp, end_timestamp, unix_timestamp())
        .await
    {
        Ok(artifact) => artifact,
        Err(error) => return authenticated_handler_response(api_error(&error.to_string())),
    };
    state
        .backend
        .record_audit(&FinanceExportAudit {
            actor_id: principal.id,
            actor_username: principal.username,
            client_ip,
            format: format.as_str().to_owned(),
            start_timestamp,
            end_timestamp,
            rows: artifact.rows.clone(),
        })
        .await;
    if format == FinanceExportFormat::Text {
        return text_response(export_text(&artifact.files));
    }
    match export_zip(&artifact.files).and_then(zip_response) {
        Ok(response) => response,
        Err(error) => authenticated_handler_response(api_error(&error.to_string())),
    }
}

async fn authenticated_admin(
    state: &FinanceExportState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let Some(credential) = crate::migration_routes::legacy_http::dashboard_credential(headers)
    else {
        return Err(
            crate::migration_routes::legacy_http::localized_dashboard_auth_error(headers, None),
        );
    };
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|error| {
            crate::migration_routes::legacy_http::localized_dashboard_auth_error(
                headers,
                Some(error.kind),
            )
        })?;
    if !user.developer_access_granted {
        return Err(console_not_found());
    }
    enforce_user_auth_view(&user).map_err(|error| {
        crate::migration_routes::legacy_http::localized_user_policy_error(headers, error)
    })?;
    if user.role < ADMIN_ROLE {
        return Err(
            crate::migration_routes::legacy_http::localized_user_policy_error(
                headers,
                UserAuthPolicyError::InsufficientPrivilege,
            ),
        );
    }
    Ok(user)
}

fn export_window(query: &HashMap<String, String>, now: i64) -> Result<(i64, i64), &'static str> {
    let start = positive_timestamp(
        query,
        "start_timestamp",
        now.saturating_sub(DEFAULT_WINDOW_SECONDS),
    )?;
    let end = positive_timestamp(query, "end_timestamp", now)?;
    if start >= end {
        return Err("start_timestamp must be before end_timestamp");
    }
    if end - start > MAX_WINDOW_SECONDS {
        return Err("export window cannot exceed 90 days");
    }
    Ok((start, end))
}

fn positive_timestamp(
    query: &HashMap<String, String>,
    key: &'static str,
    fallback: i64,
) -> Result<i64, &'static str> {
    let Some(value) = query.get(key) else {
        return Ok(fallback);
    };
    let value = value.trim();
    if value.is_empty() {
        return Ok(fallback);
    }
    value
        .parse::<i64>()
        .ok()
        .filter(|value| *value > 0)
        .ok_or(match key {
            "start_timestamp" => "invalid start_timestamp",
            _ => "invalid end_timestamp",
        })
}

fn parse_query(raw: Option<&str>) -> HashMap<String, String> {
    let mut query = HashMap::new();
    for (key, value) in form_urlencoded::parse(raw.map_or("", |value| value).as_bytes()) {
        query
            .entry(key.into_owned())
            .or_insert_with(|| value.into_owned());
    }
    query
}

fn export_text(files: &BTreeMap<String, Vec<u8>>) -> Vec<u8> {
    let mut output =
        b"LMM Finance Analysis Export\n========================================\n\n".to_vec();
    for name in FILE_NAMES {
        output.extend_from_slice(b"## ");
        output.extend_from_slice(name.as_bytes());
        output.push(b'\n');
        if let Some(contents) = files.get(name) {
            output.extend_from_slice(contents);
        }
        output.extend_from_slice(b"\n\n");
    }
    output
}

fn export_zip(files: &BTreeMap<String, Vec<u8>>) -> Result<Vec<u8>, FinanceExportError> {
    let cursor = Cursor::new(Vec::new());
    let mut writer = ZipWriter::new(cursor);
    let options = SimpleFileOptions::default().compression_method(CompressionMethod::Deflated);
    for name in FILE_NAMES {
        writer.start_file(name, options).map_err(|source| {
            FinanceExportError::from(FinanceExportBuildError::ZipEntry { name, source })
        })?;
        if let Some(contents) = files.get(name) {
            writer.write_all(contents).map_err(|source| {
                FinanceExportError::from(FinanceExportBuildError::ZipBytes { name, source })
            })?;
        }
    }
    writer
        .finish()
        .map(Cursor::into_inner)
        .map_err(|source| FinanceExportError::from(FinanceExportBuildError::ZipFinish { source }))
}

fn text_response(bytes: Vec<u8>) -> Response {
    let mut response = Response::new(Body::from(bytes));
    *response.status_mut() = StatusCode::OK;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("text/plain; charset=utf-8"),
    );
    authenticated_handler_response(response)
}

fn zip_response(bytes: Vec<u8>) -> Result<Response, FinanceExportError> {
    let filename = format!(
        "lmm-finance-export-{}.zip",
        chrono::Utc::now().format("%Y%m%d-%H%M%S")
    );
    let disposition = content_disposition(&filename)?;
    let mut response = Response::new(Body::from(bytes));
    *response.status_mut() = StatusCode::OK;
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/zip"),
    );
    response
        .headers_mut()
        .insert(header::CONTENT_DISPOSITION, disposition);
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response.headers_mut().insert(
        HeaderName::from_static("x-content-type-options"),
        HeaderValue::from_static("nosniff"),
    );
    Ok(with_auth_version(
        with_disable_cache_preserving_cache_control(response),
    ))
}

fn content_disposition(filename: &str) -> Result<HeaderValue, FinanceExportError> {
    HeaderValue::from_str(&format!("attachment; filename=\"{filename}\"")).map_err(|source| {
        FinanceExportError::from(FinanceExportBuildError::Header {
            name: "content-disposition",
            source,
        })
    })
}

fn authenticated_handler_response(response: Response) -> Response {
    with_auth_version(with_disabled_cache(response))
}

fn with_disabled_cache(mut response: Response) -> Response {
    response.headers_mut().insert(
        header::CACHE_CONTROL,
        HeaderValue::from_static("no-store, no-cache, must-revalidate, private, max-age=0"),
    );
    with_disable_cache_preserving_cache_control(response)
}

fn with_disable_cache_preserving_cache_control(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert(header::PRAGMA, HeaderValue::from_static("no-cache"));
    response
        .headers_mut()
        .insert(header::EXPIRES, HeaderValue::from_static("0"));
    response
}

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn api_error(message: &str) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": false, "message": message}),
    )
}

fn console_not_found() -> Response {
    legacy_json(StatusCode::NOT_FOUND, json!({"message": "Not Found"}))
}

fn legacy_json(status: StatusCode, body: Value) -> Response {
    let mut response = (status, Json(body)).into_response();
    response.headers_mut().insert(
        header::CONTENT_TYPE,
        HeaderValue::from_static("application/json; charset=utf-8"),
    );
    response
}

async fn load_options(pg: &PgPool) -> Result<BTreeMap<String, String>, FinanceExportError> {
    let rows = sqlx::query_as::<_, (String, String)>(
        "SELECT key, COALESCE(value, '') FROM options WHERE key = ANY($1)",
    )
    .bind(OPTION_KEYS.as_slice())
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance options", source))?;
    Ok(rows.into_iter().collect())
}

async fn load_users(pg: &PgPool) -> Result<Vec<FinanceUser>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS user_id, COALESCE(username, '') AS username, \
         COALESCE(role, 0)::BIGINT AS role, COALESCE(status, 0)::BIGINT AS status, \
         COALESCE(\"group\", '') AS \"group\", COALESCE(quota, 0)::BIGINT AS quota, \
         COALESCE(used_quota, 0)::BIGINT AS used_quota, \
         COALESCE(aff_quota, 0)::BIGINT AS affiliate_quota, \
         COALESCE(aff_history, 0)::BIGINT AS affiliate_history_quota, \
         COALESCE(request_count, 0)::BIGINT AS request_count, \
         COALESCE(created_at, 0)::BIGINT AS created_at, \
         COALESCE(last_api_activity_at, 0)::BIGINT AS last_api_activity_at, \
         trust_level_override::BIGINT AS trust_level_override \
         FROM users WHERE deleted_at IS NULL ORDER BY id ASC",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance users", source))?;
    rows.into_iter().map(user_from_row).collect()
}

fn user_from_row(row: PgRow) -> Result<FinanceUser, FinanceExportError> {
    Ok(FinanceUser {
        user_id: get(&row, "user_id")?,
        username: get(&row, "username")?,
        role: get(&row, "role")?,
        status: get(&row, "status")?,
        group: get(&row, "group")?,
        quota: get(&row, "quota")?,
        used_quota: get(&row, "used_quota")?,
        affiliate_quota: get(&row, "affiliate_quota")?,
        affiliate_history_quota: get(&row, "affiliate_history_quota")?,
        request_count: get(&row, "request_count")?,
        created_at: get(&row, "created_at")?,
        last_api_activity_at: get(&row, "last_api_activity_at")?,
        trust_level_override: get(&row, "trust_level_override")?,
        effective_group_ratio: None,
        effective_topup_group_ratio: None,
    })
}

async fn load_channels(pg: &PgPool) -> Result<Vec<FinanceChannel>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS channel_id, COALESCE(type, 0)::BIGINT AS type, \
         COALESCE(status, 0)::BIGINT AS status, COALESCE(name, '') AS name, \
         weight::BIGINT AS weight, COALESCE(created_time, 0)::BIGINT AS created_time, \
         COALESCE(test_time, 0)::BIGINT AS test_time, \
         COALESCE(response_time, 0)::BIGINT AS response_time, base_url, \
         COALESCE(balance, 0)::DOUBLE PRECISION AS balance, \
         COALESCE(balance_updated_time, 0)::BIGINT AS balance_updated_time, \
         COALESCE(models, '') AS models, model_mapping, COALESCE(\"group\", '') AS \"group\", \
         COALESCE(used_quota, 0)::BIGINT AS used_quota, priority::BIGINT AS priority, \
         auto_ban::BIGINT AS auto_ban, tag FROM channels ORDER BY id ASC",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance channels", source))?;
    rows.into_iter().map(channel_from_row).collect()
}

fn channel_from_row(row: PgRow) -> Result<FinanceChannel, FinanceExportError> {
    let raw_url: Option<String> = get(&row, "base_url")?;
    Ok(FinanceChannel {
        channel_id: get(&row, "channel_id")?,
        r#type: get(&row, "type")?,
        status: get(&row, "status")?,
        name: get(&row, "name")?,
        weight: get(&row, "weight")?,
        created_time: get(&row, "created_time")?,
        test_time: get(&row, "test_time")?,
        response_time: get(&row, "response_time")?,
        base_url: raw_url
            .as_deref()
            .map(sanitize_base_url)
            .transpose()?
            .flatten(),
        balance: get(&row, "balance")?,
        balance_updated_time: get(&row, "balance_updated_time")?,
        models: get(&row, "models")?,
        model_mapping: get(&row, "model_mapping")?,
        group: get(&row, "group")?,
        used_quota: get(&row, "used_quota")?,
        priority: get(&row, "priority")?,
        auto_ban: get(&row, "auto_ban")?,
        tag: get(&row, "tag")?,
    })
}

async fn load_plans(pg: &PgPool) -> Result<Vec<FinancePlan>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS plan_id, COALESCE(title, '') AS title, \
         COALESCE(subtitle, '') AS subtitle, COALESCE(price_amount, 0)::DOUBLE PRECISION AS price_amount, \
         COALESCE(currency, '') AS currency, COALESCE(duration_unit, '') AS duration_unit, \
         COALESCE(duration_value, 0)::BIGINT AS duration_value, \
         COALESCE(custom_seconds, 0)::BIGINT AS custom_seconds, COALESCE(enabled, FALSE) AS enabled, \
         COALESCE(sort_order, 0)::BIGINT AS sort_order, allow_balance_pay, allow_wallet_overflow, \
         COALESCE(max_purchase_per_user, 0)::BIGINT AS max_purchase_per_user, \
         COALESCE(upgrade_group, '') AS upgrade_group, COALESCE(downgrade_group, '') AS downgrade_group, \
         COALESCE(total_amount, 0)::BIGINT AS total_amount, \
         COALESCE(quota_reset_period, '') AS quota_reset_period, \
         COALESCE(quota_reset_custom_seconds, 0)::BIGINT AS quota_reset_custom_seconds, \
         COALESCE(created_at, 0)::BIGINT AS created_at, COALESCE(updated_at, 0)::BIGINT AS updated_at \
         FROM subscription_plans ORDER BY sort_order ASC, id ASC",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance subscription plans", source))?;
    rows.into_iter().map(plan_from_row).collect()
}

fn plan_from_row(row: PgRow) -> Result<FinancePlan, FinanceExportError> {
    Ok(FinancePlan {
        plan_id: get(&row, "plan_id")?,
        title: get(&row, "title")?,
        subtitle: get(&row, "subtitle")?,
        price_amount: get(&row, "price_amount")?,
        currency: get(&row, "currency")?,
        duration_unit: get(&row, "duration_unit")?,
        duration_value: get(&row, "duration_value")?,
        custom_seconds: get(&row, "custom_seconds")?,
        enabled: get(&row, "enabled")?,
        sort_order: get(&row, "sort_order")?,
        allow_balance_pay: get(&row, "allow_balance_pay")?,
        allow_wallet_overflow: get(&row, "allow_wallet_overflow")?,
        max_purchase_per_user: get(&row, "max_purchase_per_user")?,
        upgrade_group: get(&row, "upgrade_group")?,
        downgrade_group: get(&row, "downgrade_group")?,
        total_amount: get(&row, "total_amount")?,
        quota_reset_period: get(&row, "quota_reset_period")?,
        quota_reset_custom_seconds: get(&row, "quota_reset_custom_seconds")?,
        created_at: get(&row, "created_at")?,
        updated_at: get(&row, "updated_at")?,
    })
}

async fn load_topups(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceTopup>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS topup_id, COALESCE(user_id, 0)::BIGINT AS user_id, \
         COALESCE(amount, 0)::BIGINT AS amount, \
         CASE WHEN COALESCE(to_jsonb(top_ups)->>'platform_amount_micros', '') ~ '^-?[0-9]+$' \
              THEN (to_jsonb(top_ups)->>'platform_amount_micros')::BIGINT ELSE 0 END AS platform_amount_micros, \
         COALESCE(credited_quota, 0)::BIGINT AS credited_quota, \
         COALESCE(expected_amount_micros, 0)::BIGINT AS expected_amount_micros, \
         COALESCE(settled_amount_micros, 0)::BIGINT AS settled_amount_micros, \
         COALESCE(settlement_currency, '') AS settlement_currency, \
         COALESCE(money, 0)::DOUBLE PRECISION AS money, COALESCE(payment_method, '') AS payment_method, \
         COALESCE(payment_provider, '') AS payment_provider, COALESCE(create_time, 0)::BIGINT AS create_time, \
         COALESCE(complete_time, 0)::BIGINT AS complete_time, COALESCE(status, '') AS status \
         FROM top_ups WHERE create_time >= $1 AND create_time <= $2 \
         ORDER BY create_time ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance top-ups", source))?;
    rows.into_iter().map(topup_from_row).collect()
}

fn topup_from_row(row: PgRow) -> Result<FinanceTopup, FinanceExportError> {
    Ok(FinanceTopup {
        topup_id: get(&row, "topup_id")?,
        user_id: get(&row, "user_id")?,
        amount: get(&row, "amount")?,
        platform_amount_micros: get(&row, "platform_amount_micros")?,
        credited_quota: get(&row, "credited_quota")?,
        expected_amount_micros: get(&row, "expected_amount_micros")?,
        settled_amount_micros: get(&row, "settled_amount_micros")?,
        settlement_currency: get(&row, "settlement_currency")?,
        money: get(&row, "money")?,
        payment_method: get(&row, "payment_method")?,
        payment_provider: get(&row, "payment_provider")?,
        create_time: get(&row, "create_time")?,
        complete_time: get(&row, "complete_time")?,
        status: get(&row, "status")?,
    })
}

async fn load_subscription_orders(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceSubscriptionOrder>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS order_id, COALESCE(user_id, 0)::BIGINT AS user_id, \
         COALESCE(plan_id, 0)::BIGINT AS plan_id, COALESCE(money, 0)::DOUBLE PRECISION AS money, \
         COALESCE(plan_currency, '') AS plan_currency, \
         COALESCE(expected_amount_micros, 0)::BIGINT AS expected_amount_micros, \
         COALESCE(settlement_currency, '') AS settlement_currency, \
         COALESCE(payment_method, '') AS payment_method, COALESCE(payment_provider, '') AS payment_provider, \
         COALESCE(provider_product_id, '') AS provider_product_id, \
         COALESCE(provider_subscription_state, '') AS provider_subscription_state, \
         COALESCE(current_period_start, 0)::BIGINT AS current_period_start, \
         COALESCE(current_period_end, 0)::BIGINT AS current_period_end, \
         COALESCE(refunded_amount_micros, 0)::BIGINT AS refunded_amount_micros, \
         COALESCE(status, '') AS status, COALESCE(create_time, 0)::BIGINT AS create_time, \
         COALESCE(complete_time, 0)::BIGINT AS complete_time FROM subscription_orders \
         WHERE create_time >= $1 AND create_time <= $2 ORDER BY create_time ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance subscription orders", source))?;
    rows.into_iter().map(order_from_row).collect()
}

fn order_from_row(row: PgRow) -> Result<FinanceSubscriptionOrder, FinanceExportError> {
    Ok(FinanceSubscriptionOrder {
        order_id: get(&row, "order_id")?,
        user_id: get(&row, "user_id")?,
        plan_id: get(&row, "plan_id")?,
        money: get(&row, "money")?,
        plan_currency: get(&row, "plan_currency")?,
        expected_amount_micros: get(&row, "expected_amount_micros")?,
        settlement_currency: get(&row, "settlement_currency")?,
        payment_method: get(&row, "payment_method")?,
        payment_provider: get(&row, "payment_provider")?,
        provider_product_id: get(&row, "provider_product_id")?,
        provider_subscription_state: get(&row, "provider_subscription_state")?,
        current_period_start: get(&row, "current_period_start")?,
        current_period_end: get(&row, "current_period_end")?,
        refunded_amount_micros: get(&row, "refunded_amount_micros")?,
        status: get(&row, "status")?,
        create_time: get(&row, "create_time")?,
        complete_time: get(&row, "complete_time")?,
    })
}

async fn load_subscription_payments(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceSubscriptionPayment>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS payment_event_id, COALESCE(subscription_order_id, 0)::BIGINT AS subscription_order_id, \
         COALESCE(payment_provider, '') AS payment_provider, \
         COALESCE(settlement_currency, '') AS settlement_currency, \
         COALESCE(settlement_amount_micros, 0)::BIGINT AS settlement_amount_micros, \
         COALESCE(period_start, 0)::BIGINT AS period_start, \
         COALESCE(period_end, 0)::BIGINT AS period_end, \
         COALESCE(created_time, 0)::BIGINT AS created_time \
         FROM subscription_payment_events WHERE created_time >= $1 AND created_time <= $2 \
         ORDER BY created_time ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance subscription payments", source))?;
    rows.into_iter()
        .map(|row| {
            Ok(FinanceSubscriptionPayment {
                payment_event_id: get(&row, "payment_event_id")?,
                subscription_order_id: get(&row, "subscription_order_id")?,
                payment_provider: get(&row, "payment_provider")?,
                settlement_currency: get(&row, "settlement_currency")?,
                settlement_amount_micros: get(&row, "settlement_amount_micros")?,
                period_start: get(&row, "period_start")?,
                period_end: get(&row, "period_end")?,
                created_time: get(&row, "created_time")?,
            })
        })
        .collect()
}

async fn load_usage(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceUsage>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS log_id, COALESCE(user_id, 0)::BIGINT AS user_id, \
         COALESCE(created_at, 0)::BIGINT AS created_at, COALESCE(type, 0)::BIGINT AS type, \
         COALESCE(username, '') AS username, COALESCE(token_name, '') AS token_name, \
         COALESCE(model_name, '') AS model_name, COALESCE(quota, 0)::BIGINT AS quota, \
         COALESCE(prompt_tokens, 0)::BIGINT AS prompt_tokens, \
         COALESCE(completion_tokens, 0)::BIGINT AS completion_tokens, \
         COALESCE(use_time, 0)::BIGINT AS use_time, COALESCE(is_stream, FALSE) AS is_stream, \
         COALESCE(channel_id, 0)::BIGINT AS channel_id, COALESCE(\"group\", '') AS \"group\" FROM logs \
         WHERE created_at >= $1 AND created_at <= $2 AND type IN (1, 2, 3, 4, 6) \
         ORDER BY created_at ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance usage", source))?;
    rows.into_iter().map(usage_from_row).collect()
}

fn usage_from_row(row: PgRow) -> Result<FinanceUsage, FinanceExportError> {
    Ok(FinanceUsage {
        log_id: get(&row, "log_id")?,
        user_id: get(&row, "user_id")?,
        created_at: get(&row, "created_at")?,
        r#type: get(&row, "type")?,
        username: get(&row, "username")?,
        token_name: get(&row, "token_name")?,
        model_name: get(&row, "model_name")?,
        quota: get(&row, "quota")?,
        prompt_tokens: get(&row, "prompt_tokens")?,
        completion_tokens: get(&row, "completion_tokens")?,
        use_time: get(&row, "use_time")?,
        is_stream: get(&row, "is_stream")?,
        channel_id: get(&row, "channel_id")?,
        group: get(&row, "group")?,
    })
}

async fn load_bounty_ledger(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceBountyLedger>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS ledger_id, project_id::BIGINT AS project_id, challenge_id::BIGINT AS challenge_id, \
         user_id::BIGINT AS user_id, counterparty_user_id::BIGINT AS counterparty_user_id, kind, quota::BIGINT AS quota, \
         note, created_at::BIGINT AS created_at FROM open_source_bounty_ledgers \
         WHERE created_at >= $1 AND created_at <= $2 ORDER BY created_at ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance bounty ledger", source))?;
    rows.into_iter().map(ledger_from_row).collect()
}

fn ledger_from_row(row: PgRow) -> Result<FinanceBountyLedger, FinanceExportError> {
    Ok(FinanceBountyLedger {
        ledger_id: get(&row, "ledger_id")?,
        project_id: get(&row, "project_id")?,
        challenge_id: get(&row, "challenge_id")?,
        user_id: get(&row, "user_id")?,
        counterparty_user_id: get(&row, "counterparty_user_id")?,
        kind: get(&row, "kind")?,
        quota: get(&row, "quota")?,
        note: get(&row, "note")?,
        created_at: get(&row, "created_at")?,
    })
}

async fn load_checkins(
    pg: &PgPool,
    start: i64,
    end: i64,
) -> Result<Vec<FinanceCheckin>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS checkin_id, user_id::BIGINT AS user_id, checkin_date, \
         quota_awarded::BIGINT AS quota_awarded, COALESCE(created_at, 0)::BIGINT AS created_at FROM checkins \
         WHERE created_at >= $1 AND created_at <= $2 ORDER BY created_at ASC, id ASC LIMIT 200000",
    )
    .bind(start)
    .bind(end)
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance check-ins", source))?;
    rows.into_iter().map(checkin_from_row).collect()
}

fn checkin_from_row(row: PgRow) -> Result<FinanceCheckin, FinanceExportError> {
    Ok(FinanceCheckin {
        checkin_id: get(&row, "checkin_id")?,
        user_id: get(&row, "user_id")?,
        checkin_date: get(&row, "checkin_date")?,
        quota_awarded: get(&row, "quota_awarded")?,
        created_at: get(&row, "created_at")?,
    })
}

async fn load_redemptions(pg: &PgPool) -> Result<Vec<FinanceRedemption>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS redemption_id, COALESCE(user_id, 0)::BIGINT AS created_by_user_id, \
         COALESCE(status, 0)::BIGINT AS status, COALESCE(name, '') AS name, COALESCE(quota, 0)::BIGINT AS quota, \
         COALESCE(created_time, 0)::BIGINT AS created_time, COALESCE(redeemed_time, 0)::BIGINT AS redeemed_time, \
         COALESCE(used_user_id, 0)::BIGINT AS used_user_id, COALESCE(expired_time, 0)::BIGINT AS expired_time \
         FROM redemptions WHERE deleted_at IS NULL ORDER BY id ASC LIMIT 200000",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance redemptions", source))?;
    rows.into_iter().map(redemption_from_row).collect()
}

fn redemption_from_row(row: PgRow) -> Result<FinanceRedemption, FinanceExportError> {
    Ok(FinanceRedemption {
        redemption_id: get(&row, "redemption_id")?,
        created_by_user_id: get(&row, "created_by_user_id")?,
        status: get(&row, "status")?,
        name: get(&row, "name")?,
        quota: get(&row, "quota")?,
        created_time: get(&row, "created_time")?,
        redeemed_time: get(&row, "redeemed_time")?,
        used_user_id: get(&row, "used_user_id")?,
        expired_time: get(&row, "expired_time")?,
    })
}

async fn load_user_subscriptions(
    pg: &PgPool,
) -> Result<Vec<FinanceUserSubscription>, FinanceExportError> {
    let rows = sqlx::query(
        "SELECT id::BIGINT AS subscription_id, COALESCE(user_id, 0)::BIGINT AS user_id, \
         COALESCE(plan_id, 0)::BIGINT AS plan_id, amount_total::BIGINT AS amount_total, amount_used::BIGINT AS amount_used, \
         COALESCE(start_time, 0)::BIGINT AS start_time, COALESCE(end_time, 0)::BIGINT AS end_time, \
         COALESCE(status, '') AS status, COALESCE(source, '') AS source, \
         COALESCE(last_reset_time, 0)::BIGINT AS last_reset_time, COALESCE(next_reset_time, 0)::BIGINT AS next_reset_time, \
         COALESCE(upgrade_group, '') AS upgrade_group, COALESCE(prev_user_group, '') AS previous_user_group, \
         COALESCE(downgrade_group, '') AS downgrade_group, COALESCE(allow_wallet_overflow, FALSE) AS allow_wallet_overflow, \
         COALESCE(created_at, 0)::BIGINT AS created_at, COALESCE(updated_at, 0)::BIGINT AS updated_at \
         FROM user_subscriptions ORDER BY id ASC LIMIT 200000",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance user subscriptions", source))?;
    rows.into_iter().map(user_subscription_from_row).collect()
}

fn user_subscription_from_row(row: PgRow) -> Result<FinanceUserSubscription, FinanceExportError> {
    Ok(FinanceUserSubscription {
        subscription_id: get(&row, "subscription_id")?,
        user_id: get(&row, "user_id")?,
        plan_id: get(&row, "plan_id")?,
        amount_total: get(&row, "amount_total")?,
        amount_used: get(&row, "amount_used")?,
        start_time: get(&row, "start_time")?,
        end_time: get(&row, "end_time")?,
        status: get(&row, "status")?,
        source: get(&row, "source")?,
        last_reset_time: get(&row, "last_reset_time")?,
        next_reset_time: get(&row, "next_reset_time")?,
        upgrade_group: get(&row, "upgrade_group")?,
        previous_user_group: get(&row, "previous_user_group")?,
        downgrade_group: get(&row, "downgrade_group")?,
        allow_wallet_overflow: get(&row, "allow_wallet_overflow")?,
        created_at: get(&row, "created_at")?,
        updated_at: get(&row, "updated_at")?,
    })
}

async fn load_effective_pricing(
    pg: &PgPool,
    options: &BTreeMap<String, String>,
) -> Result<Vec<EffectivePricing>, FinanceExportError> {
    let ability_rows = sqlx::query(
        "SELECT a.\"group\", a.model, COALESCE(c.type, 0)::BIGINT AS channel_type, \
         COALESCE(c.settings, '') AS channel_settings FROM abilities a \
         LEFT JOIN channels c ON c.id = a.channel_id WHERE a.enabled = TRUE",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance model abilities", source))?;
    let abilities = ability_rows
        .into_iter()
        .map(|row| {
            Ok(Ability {
                group: get(&row, "group")?,
                model: get(&row, "model")?,
                channel_type: get(&row, "channel_type")?,
                channel_settings: get(&row, "channel_settings")?,
            })
        })
        .collect::<Result<Vec<_>, FinanceExportError>>()?;
    let meta_rows = sqlx::query(
        "SELECT model_name, COALESCE(description, '') AS description, COALESCE(icon, '') AS icon, \
         COALESCE(tags, '') AS tags, COALESCE(vendor_id, 0)::BIGINT AS vendor_id, \
         COALESCE(endpoints, '') AS endpoints, COALESCE(status, 1)::BIGINT AS status, \
         COALESCE(name_rule, 0)::BIGINT AS name_rule FROM models WHERE deleted_at IS NULL",
    )
    .fetch_all(pg)
    .await
    .map_err(|source| database_error("load finance model metadata", source))?;
    let metas = meta_rows
        .into_iter()
        .map(|row| {
            Ok(ModelMeta {
                model_name: get(&row, "model_name")?,
                description: get(&row, "description")?,
                icon: get(&row, "icon")?,
                tags: get(&row, "tags")?,
                vendor_id: get(&row, "vendor_id")?,
                endpoints: get(&row, "endpoints")?,
                status: get(&row, "status")?,
                name_rule: get(&row, "name_rule")?,
            })
        })
        .collect::<Result<Vec<_>, FinanceExportError>>()?;
    Ok(build_effective_pricing(&abilities, &metas, options))
}

fn build_effective_pricing(
    abilities: &[Ability],
    metas: &[ModelMeta],
    options: &BTreeMap<String, String>,
) -> Vec<EffectivePricing> {
    let ratios = float_map(options, "ModelRatio");
    let prices = float_map(options, "ModelPrice");
    let completions = float_map(options, "CompletionRatio");
    let cache = float_map(options, "CacheRatio");
    let create_cache = float_map(options, "CreateCacheRatio");
    let image = float_map(options, "ImageRatio");
    let audio = float_map(options, "AudioRatio");
    let audio_completion = float_map(options, "AudioCompletionRatio");
    let billing_modes = string_map(options, "billing_setting.billing_mode");
    let billing_expressions = string_map(options, "billing_setting.billing_expr");
    let mut models = BTreeMap::<String, (BTreeSet<String>, Vec<String>)>::new();
    for ability in abilities {
        let entry = models.entry(ability.model.clone()).or_default();
        entry.0.insert(ability.group.clone());
        for endpoint in endpoints_for_ability(ability) {
            if !entry.1.contains(&endpoint) {
                entry.1.push(endpoint);
            }
        }
    }
    let mut result = Vec::with_capacity(models.len());
    for (model, (groups, mut endpoints)) in models {
        let metadata = matching_metadata(&model, metas);
        if metadata.is_some_and(|metadata| metadata.status != 1) {
            continue;
        }
        if let Some(metadata) = metadata {
            append_metadata_endpoints(&mut endpoints, &metadata.endpoints);
        }
        let matching_name = ratio_matching_name(&model);
        let price = prices.get(matching_name).copied().or_else(|| {
            model
                .ends_with("-openai-compact")
                .then(|| prices.get("*-openai-compact").copied())
                .flatten()
        });
        let (quota_type, model_price, model_ratio, completion_ratio) = price.map_or_else(
            || {
                (
                    0,
                    0.0,
                    ratios
                        .get(matching_name)
                        .copied()
                        .map_or(37.5, |value| value),
                    completions
                        .get(matching_name)
                        .copied()
                        .map_or(1.0, |value| value),
                )
            },
            |price| (1, price, 0.0, 0.0),
        );
        let billing_mode = billing_modes
            .get(&model)
            .cloned()
            .map_or_else(String::new, |value| value);
        let billing_expr = if billing_mode == "tiered_expr" {
            billing_expressions
                .get(&model)
                .filter(|value| !value.trim().is_empty())
                .cloned()
                .map_or_else(String::new, |value| value)
        } else {
            String::new()
        };
        result.push(EffectivePricing {
            model_name: model.clone(),
            description: metadata.map_or_else(String::new, |value| value.description.clone()),
            icon: metadata.map_or_else(String::new, |value| value.icon.clone()),
            tags: metadata.map_or_else(String::new, |value| value.tags.clone()),
            vendor_id: metadata.map_or(0, |value| value.vendor_id),
            quota_type,
            model_ratio,
            model_price,
            owner_by: String::new(),
            completion_ratio,
            cache_ratio: cache.get(matching_name).copied(),
            create_cache_ratio: create_cache.get(matching_name).copied(),
            image_ratio: image.get(matching_name).copied(),
            audio_ratio: audio.get(matching_name).copied(),
            audio_completion_ratio: audio_completion.get(matching_name).copied(),
            enable_groups: groups.into_iter().collect(),
            supported_endpoint_types: endpoints,
            billing_mode,
            billing_expr,
            pricing_version: String::new(),
        });
    }
    if let Some(first) = result.first_mut() {
        first.pricing_version = PRICING_VERSION.to_owned();
    }
    result
}

fn matching_metadata<'a>(model: &str, metas: &'a [ModelMeta]) -> Option<&'a ModelMeta> {
    metas
        .iter()
        .find(|meta| meta.name_rule == 0 && meta.model_name == model)
        .or_else(|| {
            metas
                .iter()
                .find(|meta| meta.name_rule == 1 && model.starts_with(&meta.model_name))
        })
        .or_else(|| {
            metas
                .iter()
                .find(|meta| meta.name_rule == 3 && model.ends_with(&meta.model_name))
        })
        .or_else(|| {
            metas
                .iter()
                .find(|meta| meta.name_rule == 2 && model.contains(&meta.model_name))
        })
}

fn endpoints_for_ability(ability: &Ability) -> Vec<String> {
    if ability.channel_type == 58
        && let Some(endpoints) =
            advanced_custom_endpoints(&ability.channel_settings, &ability.model)
    {
        return endpoints;
    }
    let mut endpoints = match ability.channel_type {
        38 => vec!["jina-rerank"],
        14 | 33 => vec!["anthropic", "openai"],
        24 | 41 => vec!["gemini", "openai"],
        20 => vec!["openai"],
        48 => vec!["openai", "openai-response"],
        55 => vec!["openai-video"],
        59 | 60 => vec![
            "openai",
            "openai-response",
            "openai-response-compact",
            "anthropic",
            "gemini",
            "openai-alpha-search",
        ],
        57 => vec![
            "openai-response",
            "openai-response-compact",
            "openai-alpha-search",
        ],
        _ if ["o3-pro", "o3-deep-research", "o4-mini-deep-research"]
            .iter()
            .any(|needle| ability.model.contains(needle)) =>
        {
            vec!["openai-response"]
        }
        _ => vec!["openai"],
    }
    .into_iter()
    .map(str::to_owned)
    .collect::<Vec<_>>();
    let lower = ability.model.to_ascii_lowercase();
    if [
        "dall-e-3",
        "dall-e-2",
        "gpt-image-1",
        "gpt-image-2",
        "flux-",
        "flux.1-",
    ]
    .iter()
    .any(|needle| lower.contains(needle))
        || lower.starts_with("imagen-")
    {
        endpoints.insert(0, "image-generation".to_owned());
    }
    endpoints
}

fn advanced_custom_endpoints(settings: &str, model: &str) -> Option<Vec<String>> {
    let settings = serde_json::from_str::<Value>(settings).ok()?;
    let config = settings.get("advanced_custom")?.as_object()?;
    let routes = config
        .get("advanced_routes")
        .and_then(|value| value.as_array().cloned())
        .map_or_else(Vec::new, |value| value);
    let mut endpoints = Vec::new();
    for route in routes {
        let models = route.get("models").and_then(Value::as_array);
        let allowed = models.is_none_or(|models| {
            models.is_empty() || models.iter().any(|value| value.as_str() == Some(model))
        });
        if !allowed {
            continue;
        }
        let Some(path) = route.get("incoming_path").and_then(Value::as_str) else {
            continue;
        };
        let endpoint = match path.trim() {
            "/v1/chat/completions" => Some("openai"),
            "/v1/responses" => Some("openai-response"),
            "/v1/responses/compact" => Some("openai-response-compact"),
            "/v1/alpha/search" => Some("openai-alpha-search"),
            "/v1/messages" => Some("anthropic"),
            "/v1/rerank" => Some("jina-rerank"),
            "/v1/images/generations" => Some("image-generation"),
            "/v1/embeddings" => Some("embeddings"),
            path if path.starts_with("/v1beta/models/")
                && (path.contains(":generateContent")
                    || path.contains(":streamGenerateContent")) =>
            {
                Some("gemini")
            }
            _ => None,
        };
        if let Some(endpoint) = endpoint
            && !endpoints.iter().any(|existing| existing == endpoint)
        {
            endpoints.push(endpoint.to_owned());
        }
    }
    Some(endpoints)
}

fn append_metadata_endpoints(endpoints: &mut Vec<String>, raw: &str) {
    let Some(keys) = serde_json::from_str::<Value>(raw)
        .ok()
        .and_then(|value| value.as_object().cloned())
    else {
        return;
    };
    for (key, value) in keys {
        if (value.is_string() || value.is_object()) && !endpoints.contains(&key) {
            endpoints.push(key);
        }
    }
}

fn ratio_matching_name(model: &str) -> &str {
    if model.starts_with("gemini-2.5-flash-lite") && model.contains("-thinking-") {
        "gemini-2.5-flash-lite-thinking-*"
    } else if model.starts_with("gemini-2.5-flash") && model.contains("-thinking-") {
        "gemini-2.5-flash-thinking-*"
    } else if model.starts_with("gemini-2.5-pro") && model.contains("-thinking-") {
        "gemini-2.5-pro-thinking-*"
    } else if model.starts_with("gpt-4o-gizmo") {
        "gpt-4o-gizmo-*"
    } else if model.starts_with("gpt-4-gizmo") {
        "gpt-4-gizmo-*"
    } else {
        model
    }
}

fn apply_user_ratios(users: &mut [FinanceUser], options: &BTreeMap<String, String>) {
    let group = float_map(options, "GroupRatio");
    let topup = float_map(options, "TopupGroupRatio");
    for user in users {
        user.effective_group_ratio = group.get(&user.group).copied();
        user.effective_topup_group_ratio = topup.get(&user.group).copied();
    }
}

fn decode_option(options: &BTreeMap<String, String>, key: &str) -> Value {
    let raw = options.get(key).map_or("", String::as_str);
    serde_json::from_str(raw).unwrap_or_else(|_| Value::String(raw.to_owned()))
}

fn float_map(options: &BTreeMap<String, String>, key: &str) -> BTreeMap<String, f64> {
    options
        .get(key)
        .and_then(|value| serde_json::from_str(value).ok())
        .map_or_else(BTreeMap::new, |value| value)
}

fn string_map(options: &BTreeMap<String, String>, key: &str) -> BTreeMap<String, String> {
    options
        .get(key)
        .and_then(|value| serde_json::from_str(value).ok())
        .map_or_else(BTreeMap::new, |value| value)
}

fn sanitize_base_url(value: &str) -> Result<Option<String>, FinanceExportError> {
    let value = value.trim();
    if value.is_empty() {
        return Ok(None);
    }
    let invalid_uri = || {
        FinanceExportError::from(FinanceExportBuildError::Uri {
            value: value.to_owned(),
        })
    };
    let (scheme, remainder) = value.split_once("://").ok_or_else(invalid_uri)?;
    if scheme.is_empty() {
        return Err(invalid_uri());
    }
    let authority_with_credentials = remainder
        .split(['/', '?', '#'])
        .next()
        .ok_or_else(&invalid_uri)?;
    let authority = authority_with_credentials
        .rsplit('@')
        .next()
        .ok_or_else(&invalid_uri)?;
    if authority.is_empty() {
        Err(invalid_uri())
    } else {
        Ok(Some(format!("{scheme}://{authority}")))
    }
}

fn go_pretty_json(value: &impl Serialize) -> Result<Vec<u8>, FinanceExportError> {
    serde_json::to_string_pretty(value)
        .map(|json| {
            json.replace('&', "\\u0026")
                .replace('<', "\\u003c")
                .replace('>', "\\u003e")
                .replace('\u{2028}', "\\u2028")
                .replace('\u{2029}', "\\u2029")
                .into_bytes()
        })
        .map_err(|source| FinanceExportError::from(FinanceExportBuildError::Json { source }))
}

fn get<'r, T>(row: &'r PgRow, column: &str) -> Result<T, FinanceExportError>
where
    T: sqlx::Decode<'r, sqlx::Postgres> + sqlx::Type<sqlx::Postgres>,
{
    row.try_get(column).map_err(|source| {
        FinanceExportError::from(FinanceExportBuildError::Row {
            column: column.to_owned(),
            source,
        })
    })
}

fn is_zero(value: &i64) -> bool {
    *value == 0
}

fn usize_to_i64(value: usize) -> i64 {
    i64::try_from(value).map_or(i64::MAX, |value| value)
}

fn unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).map_or(i64::MAX, |value| value)
        })
}

fn database_error(operation: &'static str, source: sqlx::Error) -> FinanceExportError {
    FinanceExportError::from(FinanceExportBuildError::Database { operation, source })
}

#[cfg(test)]
mod tests {
    use std::{
        error::Error,
        io::{Error as IoError, Read},
    };

    use serde::{Serializer, ser::Error as _};

    use super::*;

    type TestResult<T = ()> = Result<T, Box<dyn Error>>;

    fn test_error(message: impl Into<String>) -> Box<dyn Error> {
        Box::new(IoError::other(message.into()))
    }

    fn require_error<T, E>(result: Result<T, E>, context: &'static str) -> TestResult<E> {
        match result {
            Ok(_) => Err(test_error(context)),
            Err(error) => Ok(error),
        }
    }

    struct BrokenJson;

    impl Serialize for BrokenJson {
        fn serialize<S>(&self, _serializer: S) -> Result<S::Ok, S::Error>
        where
            S: Serializer,
        {
            Err(S::Error::custom("fixture JSON failure"))
        }
    }

    #[test]
    fn window_should_reject_more_than_ninety_days() -> TestResult {
        let query = parse_query(Some("start_timestamp=1&end_timestamp=7776002"));
        assert_eq!(
            export_window(&query, 10),
            Err("export window cannot exceed 90 days")
        );
        Ok(())
    }

    #[test]
    fn csv_format_should_keep_the_legacy_error() -> TestResult {
        let error = require_error(
            FinanceExportFormat::parse("csv"),
            "CSV format must be rejected",
        )?;
        assert_eq!(error, FinanceExportRequestError::UnsupportedFormat);
        assert_eq!(error.to_string(), "format must be zip or text");
        Ok(())
    }

    #[test]
    fn base_url_should_remove_credentials_path_and_query() -> TestResult {
        assert_eq!(
            sanitize_base_url("https://user:secret@example.com/v1?token=secret")?,
            Some("https://example.com".to_owned())
        );
        Ok(())
    }

    #[test]
    fn malformed_base_uri_should_return_an_explicit_error() -> TestResult {
        let error = require_error(
            sanitize_base_url("not-a-finance-uri"),
            "malformed finance URI must fail closed",
        )?;
        assert_eq!(
            error.to_string(),
            "finance export base URI `not-a-finance-uri` is invalid"
        );
        Ok(())
    }

    #[test]
    fn text_should_preserve_section_order() -> TestResult {
        let files = FILE_NAMES
            .into_iter()
            .map(|name| (name.to_owned(), name.as_bytes().to_vec()))
            .collect();
        let text = String::from_utf8(export_text(&files))
            .map_err(|source| test_error(format!("text export UTF-8 error: {source}")))?;
        assert!(text.find("manifest.json") < text.find("users-balances.json"));
        Ok(())
    }

    #[test]
    fn zip_should_preserve_file_order() -> TestResult {
        let files = FILE_NAMES
            .into_iter()
            .map(|name| (name.to_owned(), name.as_bytes().to_vec()))
            .collect();
        let bytes = export_zip(&files)
            .map_err(|source| test_error(format!("ZIP creation error: {source}")))?;
        let mut archive = zip::ZipArchive::new(Cursor::new(bytes))
            .map_err(|source| test_error(format!("ZIP read error: {source}")))?;
        let first_name = archive
            .by_index(0)
            .map_err(|source| test_error(format!("first ZIP row error: {source}")))?
            .name()
            .to_owned();
        assert_eq!(first_name, "manifest.json");
        let mut contents = String::new();
        archive
            .by_name("user-subscriptions.json")
            .map_err(|source| test_error(format!("last ZIP row error: {source}")))?
            .read_to_string(&mut contents)
            .map_err(|source| test_error(format!("ZIP entry byte error: {source}")))?;
        assert_eq!(contents, "user-subscriptions.json");
        Ok(())
    }

    #[test]
    fn fractional_topup_export_should_include_exact_platform_micros() -> TestResult {
        let value = serde_json::to_value(FinanceTopup {
            topup_id: 1,
            user_id: 2,
            amount: 6,
            platform_amount_micros: 6_800_000,
            credited_quota: 680,
            expected_amount_micros: 1_000_000,
            settled_amount_micros: 1_000_000,
            settlement_currency: "USD".to_owned(),
            money: 1.0,
            payment_method: "waffo_pancake".to_owned(),
            payment_provider: "waffo_pancake".to_owned(),
            create_time: 10,
            complete_time: 11,
            status: "success".to_owned(),
        })?;
        assert_eq!(value["amount"], json!(6));
        assert_eq!(value["platform_amount_micros"], json!(6_800_000));
        Ok(())
    }

    #[test]
    fn go_json_should_escape_html_sensitive_characters() -> TestResult {
        let bytes = go_pretty_json(&"<&>")
            .map_err(|source| test_error(format!("JSON encoding error: {source}")))?;
        assert_eq!(bytes, br#""\u003c\u0026\u003e""#);
        Ok(())
    }

    #[test]
    fn json_serialization_should_return_an_explicit_error() -> TestResult {
        let error = require_error(
            go_pretty_json(&BrokenJson),
            "broken JSON serializer must fail closed",
        )?;
        assert!(
            error
                .to_string()
                .contains("finance export JSON serialization failed")
        );
        assert!(error.to_string().contains("fixture JSON failure"));
        Ok(())
    }

    #[test]
    fn invalid_content_disposition_should_return_an_explicit_header_error() -> TestResult {
        let error = require_error(
            content_disposition("bad\nfilename.zip"),
            "invalid content-disposition header must fail closed",
        )?;
        assert!(
            error
                .to_string()
                .contains("finance export response header `content-disposition` is invalid")
        );
        Ok(())
    }

    #[test]
    fn row_decode_error_should_include_the_column() -> TestResult {
        let error = FinanceExportError::from(FinanceExportBuildError::Row {
            column: "quota".to_owned(),
            source: sqlx::Error::ColumnNotFound("quota".to_owned()),
        });
        assert!(
            error
                .to_string()
                .contains("finance export database row column `quota` failed")
        );
        Ok(())
    }

    #[test]
    fn pricing_endpoint_inference_should_follow_go_special_cases() -> TestResult {
        let open_router = Ability {
            group: "default".to_owned(),
            model: "o3-pro".to_owned(),
            channel_type: 20,
            channel_settings: String::new(),
        };
        assert_eq!(endpoints_for_ability(&open_router), ["openai"]);

        let ordinary = Ability {
            channel_type: 1,
            model: "prefix-imagen-model".to_owned(),
            ..open_router
        };
        assert_eq!(endpoints_for_ability(&ordinary), ["openai"]);
        Ok(())
    }

    #[test]
    fn missing_advanced_custom_config_should_use_channel_fallback() -> TestResult {
        let ability = Ability {
            group: "default".to_owned(),
            model: "gpt-4o".to_owned(),
            channel_type: 58,
            channel_settings: "{}".to_owned(),
        };
        assert_eq!(endpoints_for_ability(&ability), ["openai"]);
        Ok(())
    }

    #[test]
    fn exact_metadata_should_win_over_broader_rules() -> TestResult {
        let metadata = vec![
            ModelMeta {
                model_name: "gpt".to_owned(),
                description: "prefix".to_owned(),
                icon: String::new(),
                tags: String::new(),
                vendor_id: 0,
                endpoints: String::new(),
                status: 1,
                name_rule: 1,
            },
            ModelMeta {
                model_name: "gpt-4o".to_owned(),
                description: "exact".to_owned(),
                icon: String::new(),
                tags: String::new(),
                vendor_id: 0,
                endpoints: String::new(),
                status: 1,
                name_rule: 0,
            },
        ];
        assert_eq!(
            matching_metadata("gpt-4o", &metadata).map(|value| value.description.as_str()),
            Some("exact")
        );
        Ok(())
    }
}
