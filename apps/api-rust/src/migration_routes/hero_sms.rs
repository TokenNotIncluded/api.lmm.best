//! Legacy-compatible HeroSMS email activation and option routes.
//!
//! Provider calls sit behind [`HeroSmsGateway`]. The router never issues an
//! upstream request without a configured API key, so incomplete composition
//! fails closed rather than proxying purchases to HeroSMS accidentally.

use std::{
    collections::BTreeMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use aes_gcm::{
    Aes256Gcm, Nonce,
    aead::{Aead, KeyInit},
};
use async_trait::async_trait;
use axum::{
    Json, Router,
    body::to_bytes,
    extract::{Path, Query, Request, State},
    http::{HeaderMap, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::{delete, get, post},
};
use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use rand::RngCore;
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::{PgPool, Row};
use uuid::Uuid;

mod sms;

use super::system_config::{DashboardRootAuthorizer, SystemConfigAuthorizer};
use crate::{
    auth::{
        CriticalRateLimitOutcome, DashboardAuth, DashboardUserView, UserAuthPolicyError,
        enforce_user_auth_view, user_auth_message, user_auth_status,
    },
    legacy_empty_response, outbound_http,
};

const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const BODY_LIMIT_BYTES: usize = 16 * 1024;
const ROOT_ROLE: i64 = 100;
const DEFAULT_QUOTA_PER_UNIT: i64 = 500_000;
const HERO_SMS_BASE_URL: &str = "https://hero-sms.com/api/v1";
const HERO_SMS_CURRENCY: &str = "USD";
const HERO_SMS_CURRENCY_CODE: i32 = 840;
const DEFAULT_PRICE_MULTIPLIER: &str = "1";
const PERSISTENT_CIPHER_ENVELOPE: &str = "v1:";

const OPTION_ENABLED: &str = "hero_sms.enabled";
const OPTION_EMAIL_ENABLED: &str = "hero_sms.email_enabled";
const OPTION_SMS_ENABLED: &str = "hero_sms.sms_enabled";
const OPTION_API_KEY: &str = "hero_sms.api_key";
const OPTION_CURRENCY: &str = "hero_sms.currency";
const OPTION_CODE: &str = "hero_sms.currency_code";
const OPTION_MULTIPLIER: &str = "hero_sms.price_multiplier";

const ORDER_STATUS_PENDING: &str = "pending_provider";
const ORDER_STATUS_COMPLETED: &str = "completed";
const ORDER_STATUS_FAILED: &str = "failed";
const ORDER_STATUS_RECONCILING: &str = "reconciling";
const ORDER_STATUS_UNKNOWN: &str = "purchase_unknown";

const ACTIVATION_PENDING: &str = "pending_provider";
const ACTIVATION_ACTIVE: &str = "active";
const ACTIVATION_COMPLETED: &str = "completed";
const ACTIVATION_RECONCILING: &str = "reconciling";
const ACTIVATION_CANCEL_PENDING: &str = "cancel_pending";
const ACTIVATION_CANCELLED: &str = "cancelled";
const ACTIVATION_REFUNDED: &str = "refunded";

/// PostgreSQL, dashboard-auth, and HeroSMS gateway dependencies.
#[derive(Clone)]
pub struct HeroSmsState {
    pg: PgPool,
    auth: Arc<dyn DashboardAuth>,
    root: Arc<DashboardRootAuthorizer>,
    gateway: Arc<dyn HeroSmsGateway>,
    sms_user_rate_limiter: Arc<dyn sms::SmsUserRateLimiter>,
}

impl HeroSmsState {
    #[must_use]
    pub fn new(pg: PgPool, auth: Arc<dyn DashboardAuth>, gateway: Arc<dyn HeroSmsGateway>) -> Self {
        Self {
            pg,
            root: Arc::new(DashboardRootAuthorizer::new(Arc::clone(&auth))),
            auth,
            gateway,
            sms_user_rate_limiter: Arc::new(sms::AllowSmsUserRateLimiter),
        }
    }

    /// Enables the Go-compatible per-user critical limiter for SMS mutations.
    #[must_use]
    pub fn with_sms_user_rate_limit(
        mut self,
        valkey: redis::Client,
        config: HeroSmsRateLimitConfig,
    ) -> Self {
        self.sms_user_rate_limiter = Arc::new(sms::ValkeySmsUserRateLimiter::new(valkey, config));
        self
    }
}

pub fn router(state: HeroSmsState) -> Router {
    Router::new()
        .merge(sms::routes())
        .route("/api/option/hero-sms", get(get_options).put(put_options))
        .route("/api/option/hero-sms/test", post(test_options))
        .route("/api/option/hero-sms/key", delete(delete_api_key))
        .route("/api/hero-sms/email/products", get(list_products))
        .route(
            "/api/hero-sms/email/activations",
            get(list_activations).post(create_activations),
        )
        .route(
            "/api/hero-sms/email/activations/current",
            get(current_activation),
        )
        .route("/api/hero-sms/email/activations/{id}", get(get_activation))
        .route(
            "/api/hero-sms/email/activations/{id}/refresh",
            post(refresh_activation),
        )
        .route(
            "/api/hero-sms/email/activations/{id}/cancel",
            post(cancel_activation),
        )
        .route(
            "/api/hero-sms/email/activations/{id}/reorder",
            post(reorder_activation),
        )
        .with_state(state)
}

#[derive(Clone, Debug, Serialize)]
struct HeroSmsSettingsView {
    enabled: bool,
    email_enabled: bool,
    sms_enabled: bool,
    api_key_configured: bool,
    pending_work: bool,
    currency: String,
    currency_code: i32,
    price_multiplier: String,
}

#[derive(Default, Deserialize)]
struct HeroSmsSettingsUpdate {
    enabled: Option<bool>,
    email_enabled: Option<bool>,
    sms_enabled: Option<bool>,
    #[serde(default)]
    api_key: String,
    #[serde(default)]
    price_multiplier: String,
}

#[derive(Default, Deserialize)]
struct HeroSmsTestInput {
    #[serde(default)]
    api_key: String,
}

#[derive(Clone, Serialize)]
struct HeroSmsProduct {
    id: String,
    site: String,
    domain: String,
    count: i32,
    available: bool,
    cost_usd: String,
    customer_price_usd: String,
    charge_quota: i64,
}

#[derive(Serialize)]
struct HeroSmsProductPage {
    items: Vec<HeroSmsProduct>,
    page: i32,
    size: i32,
    total: i32,
    price_multiplier: String,
    currency: String,
    currency_code: i32,
}

#[derive(Default, Deserialize)]
struct PurchaseRequest {
    #[serde(default)]
    domain_id: String,
    #[serde(default)]
    quantity: i32,
}

#[derive(Default, Deserialize)]
struct ReorderRequest {
    #[serde(default)]
    domain_id: String,
}

#[derive(Clone, Debug, Serialize)]
struct ActivationView {
    id: String,
    order_id: String,
    status: String,
    domain_id: String,
    site: String,
    domain: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    email: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    code: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    message: String,
    charge_quota: i64,
    #[serde(skip_serializing_if = "String::is_empty")]
    cost_usd: String,
    #[serde(skip_serializing_if = "String::is_empty")]
    currency: String,
    #[serde(skip_serializing_if = "is_zero")]
    currency_code: i32,
    #[serde(skip_serializing_if = "String::is_empty")]
    cancel_reason: String,
    created_at: i64,
    updated_at: i64,
}

fn is_zero(value: &i32) -> bool {
    *value == 0
}

#[derive(Serialize)]
struct ActivationPage {
    items: Vec<ActivationView>,
    page: i32,
    size: i32,
    total: i64,
}

#[derive(Serialize)]
struct OrderView {
    id: String,
    operation: String,
    status: String,
    domain_id: String,
    site: String,
    domain: String,
    quantity: i32,
    price_multiplier: String,
    reserved_cost_usd: String,
    customer_price_usd: String,
    charge_quota: i64,
    refunded_quota: i64,
    created_at: i64,
    updated_at: i64,
    activations: Vec<ActivationView>,
}

#[derive(Clone, Copy, Debug)]
pub struct HeroSmsRateLimitConfig {
    pub enabled: bool,
    pub max_requests: u64,
    pub window: std::time::Duration,
    pub dependency_timeout: std::time::Duration,
}

pub struct HeroDomain {
    pub name: String,
    pub count: i32,
    pub cost_micros: i64,
}

#[derive(Clone, Debug)]
pub struct HeroEmailRecord {
    pub id: String,
    pub email: String,
    pub code: String,
    pub message: String,
    pub status: String,
    pub cost_micros: i64,
    pub currency_code: i32,
}

#[derive(Debug, thiserror::Error)]
pub enum HeroSmsProviderError {
    #[error("unauthorized")]
    Unauthorized,
    #[error("not found")]
    NotFound,
    #[error("invalid request")]
    InvalidRequest,
    #[error("rate limited")]
    RateLimited,
    #[error("upstream busy")]
    UpstreamBusy,
    #[error("upstream timeout")]
    UpstreamTimeout,
    #[error("bad response")]
    BadResponse,
    #[error("batch count mismatch")]
    BatchCountMismatch,
}

#[derive(Debug, thiserror::Error)]
#[error("{code}: {message}")]
struct HeroSmsApiError {
    status: StatusCode,
    code: &'static str,
    message: &'static str,
}

#[async_trait]
pub trait HeroSmsGateway: Send + Sync {
    async fn list_domains(
        &self,
        api_key: &str,
        site: &str,
    ) -> Result<Vec<HeroDomain>, HeroSmsProviderError>;
    async fn create_email(
        &self,
        api_key: &str,
        site: &str,
        domain: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError>;
    async fn create_email_batch(
        &self,
        api_key: &str,
        site: &str,
        domain: &str,
        count: i32,
    ) -> Result<Vec<HeroEmailRecord>, HeroSmsProviderError>;
    async fn get_email(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError>;
    async fn delete_email(&self, api_key: &str, id: &str) -> Result<(), HeroSmsProviderError>;
    async fn reorder_email(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError>;

    async fn list_sms_countries(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsCountry>, HeroSmsProviderError> {
        let _ = api_key;
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn list_sms_services(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsService>, HeroSmsProviderError> {
        let _ = api_key;
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn list_sms_operators(
        &self,
        api_key: &str,
        country_id: i32,
    ) -> Result<Vec<String>, HeroSmsProviderError> {
        let _ = (api_key, country_id);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn get_sms_offer(
        &self,
        api_key: &str,
        country_id: i32,
        service: &str,
    ) -> Result<sms::SmsOffer, HeroSmsProviderError> {
        let _ = (api_key, country_id, service);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn purchase_sms_activation(
        &self,
        api_key: &str,
        request: sms::SmsPurchase,
    ) -> Result<sms::SmsActivation, HeroSmsProviderError> {
        let _ = (api_key, request);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn list_active_sms_activations(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsActiveActivation>, HeroSmsProviderError> {
        let _ = api_key;
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn get_sms_activation_status(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<sms::SmsStatus, HeroSmsProviderError> {
        let _ = (api_key, id);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn get_sms_activation_state(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<String, HeroSmsProviderError> {
        let _ = (api_key, id);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn set_sms_activation_status(
        &self,
        api_key: &str,
        id: &str,
        status: i32,
    ) -> Result<(), HeroSmsProviderError> {
        let _ = (api_key, id, status);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn submit_sms_complaint(
        &self,
        api_key: &str,
        id: &str,
        reason: &str,
    ) -> Result<(), HeroSmsProviderError> {
        let _ = (api_key, id, reason);
        Err(HeroSmsProviderError::UpstreamBusy)
    }
}

pub struct DisabledHeroSmsGateway;

#[async_trait]
impl HeroSmsGateway for DisabledHeroSmsGateway {
    async fn list_domains(
        &self,
        _: &str,
        _: &str,
    ) -> Result<Vec<HeroDomain>, HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn create_email(
        &self,
        _: &str,
        _: &str,
        _: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn create_email_batch(
        &self,
        _: &str,
        _: &str,
        _: &str,
        _: i32,
    ) -> Result<Vec<HeroEmailRecord>, HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn get_email(&self, _: &str, _: &str) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn delete_email(&self, _: &str, _: &str) -> Result<(), HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
    async fn reorder_email(
        &self,
        _: &str,
        _: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        Err(HeroSmsProviderError::UpstreamBusy)
    }
}

pub struct ReqwestHeroSmsGateway {
    client: reqwest::Client,
    base_url: String,
}

impl ReqwestHeroSmsGateway {
    pub fn production(timeout: std::time::Duration) -> Result<Self, ()> {
        let client = outbound_http::client(timeout).map_err(|_| ())?;
        Ok(Self {
            client,
            base_url: HERO_SMS_BASE_URL.to_owned(),
        })
    }
}

#[async_trait]
impl HeroSmsGateway for ReqwestHeroSmsGateway {
    async fn list_domains(
        &self,
        api_key: &str,
        site: &str,
    ) -> Result<Vec<HeroDomain>, HeroSmsProviderError> {
        let mut url = format!("{}/emails/domains", self.base_url.trim_end_matches('/'));
        if !site.trim().is_empty() {
            url.push_str("?site=");
            url.push_str(&urlencoding(site.trim()));
        }
        let response = self
            .client
            .get(url)
            .header("Accept", "application/json")
            .header("ApiKey", api_key)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        map_status(response.status())?;
        let body: Value = response
            .json()
            .await
            .map_err(|_| HeroSmsProviderError::BadResponse)?;
        let Some(items) = body.get("data").and_then(Value::as_array) else {
            return Err(HeroSmsProviderError::BadResponse);
        };
        items
            .iter()
            .map(decode_domain)
            .collect::<Result<Vec<_>, _>>()
    }

    async fn create_email(
        &self,
        api_key: &str,
        site: &str,
        domain: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        post_email_record(
            &self.client,
            &self.base_url,
            api_key,
            "/emails",
            json!({"site": site, "domain": domain}),
        )
        .await
    }

    async fn create_email_batch(
        &self,
        api_key: &str,
        site: &str,
        domain: &str,
        count: i32,
    ) -> Result<Vec<HeroEmailRecord>, HeroSmsProviderError> {
        let response = self
            .client
            .post(format!(
                "{}/emails/batch",
                self.base_url.trim_end_matches('/')
            ))
            .header("Accept", "application/json")
            .header("ApiKey", api_key)
            .header(header::CONTENT_TYPE, "application/json")
            .json(&json!({"site": site, "domain": domain, "count": count}))
            .send()
            .await
            .map_err(map_reqwest_error)?;
        map_status(response.status())?;
        let body: Value = response
            .json()
            .await
            .map_err(|_| HeroSmsProviderError::BadResponse)?;
        let items = body
            .get("data")
            .and_then(Value::as_array)
            .ok_or(HeroSmsProviderError::BadResponse)?;
        let meta_count = body
            .pointer("/meta/count")
            .and_then(Value::as_i64)
            .unwrap_or(items.len() as i64) as i32;
        let records = items
            .iter()
            .map(decode_email_record)
            .collect::<Result<Vec<_>, _>>()?;
        if records.len() as i32 != count || meta_count != count {
            return Err(HeroSmsProviderError::BatchCountMismatch);
        }
        Ok(records)
    }

    async fn get_email(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        let response = self
            .client
            .get(format!(
                "{}/emails/{}",
                self.base_url.trim_end_matches('/'),
                urlencoding(id)
            ))
            .header("Accept", "application/json")
            .header("ApiKey", api_key)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        map_status(response.status())?;
        let body: Value = response
            .json()
            .await
            .map_err(|_| HeroSmsProviderError::BadResponse)?;
        decode_email_record(body.get("data").unwrap_or(&body))
    }

    async fn delete_email(&self, api_key: &str, id: &str) -> Result<(), HeroSmsProviderError> {
        let response = self
            .client
            .delete(format!(
                "{}/emails/{}",
                self.base_url.trim_end_matches('/'),
                urlencoding(id)
            ))
            .header("Accept", "application/json")
            .header("ApiKey", api_key)
            .send()
            .await
            .map_err(map_reqwest_error)?;
        if response.status() == StatusCode::NOT_FOUND {
            return Err(HeroSmsProviderError::NotFound);
        }
        map_status(response.status())?;
        Ok(())
    }

    async fn reorder_email(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<HeroEmailRecord, HeroSmsProviderError> {
        post_email_record(
            &self.client,
            &self.base_url,
            api_key,
            &format!("/emails/{}/reorder", urlencoding(id)),
            json!({}),
        )
        .await
    }

    async fn list_sms_countries(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsCountry>, HeroSmsProviderError> {
        sms::reqwest_list_countries(self, api_key).await
    }
    async fn list_sms_services(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsService>, HeroSmsProviderError> {
        sms::reqwest_list_services(self, api_key).await
    }
    async fn list_sms_operators(
        &self,
        api_key: &str,
        country_id: i32,
    ) -> Result<Vec<String>, HeroSmsProviderError> {
        sms::reqwest_list_operators(self, api_key, country_id).await
    }
    async fn get_sms_offer(
        &self,
        api_key: &str,
        country_id: i32,
        service: &str,
    ) -> Result<sms::SmsOffer, HeroSmsProviderError> {
        sms::reqwest_get_offer(self, api_key, country_id, service).await
    }
    async fn purchase_sms_activation(
        &self,
        api_key: &str,
        request: sms::SmsPurchase,
    ) -> Result<sms::SmsActivation, HeroSmsProviderError> {
        sms::reqwest_purchase(self, api_key, request).await
    }
    async fn list_active_sms_activations(
        &self,
        api_key: &str,
    ) -> Result<Vec<sms::SmsActiveActivation>, HeroSmsProviderError> {
        sms::reqwest_list_active(self, api_key).await
    }
    async fn get_sms_activation_status(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<sms::SmsStatus, HeroSmsProviderError> {
        sms::reqwest_status(self, api_key, id).await
    }
    async fn get_sms_activation_state(
        &self,
        api_key: &str,
        id: &str,
    ) -> Result<String, HeroSmsProviderError> {
        sms::reqwest_state(self, api_key, id).await
    }
    async fn set_sms_activation_status(
        &self,
        api_key: &str,
        id: &str,
        status: i32,
    ) -> Result<(), HeroSmsProviderError> {
        sms::reqwest_set_status(self, api_key, id, status).await
    }
    async fn submit_sms_complaint(
        &self,
        api_key: &str,
        id: &str,
        reason: &str,
    ) -> Result<(), HeroSmsProviderError> {
        sms::reqwest_complaint(self, api_key, id, reason).await
    }
}

async fn post_email_record(
    client: &reqwest::Client,
    base_url: &str,
    api_key: &str,
    path: &str,
    payload: Value,
) -> Result<HeroEmailRecord, HeroSmsProviderError> {
    let response = client
        .post(format!("{}{}", base_url.trim_end_matches('/'), path))
        .header("Accept", "application/json")
        .header("ApiKey", api_key)
        .header(header::CONTENT_TYPE, "application/json")
        .json(&payload)
        .send()
        .await
        .map_err(map_reqwest_error)?;
    map_status(response.status())?;
    let body: Value = response
        .json()
        .await
        .map_err(|_| HeroSmsProviderError::BadResponse)?;
    decode_email_record(body.get("data").unwrap_or(&body))
}

fn decode_domain(value: &Value) -> Result<HeroDomain, HeroSmsProviderError> {
    let name = value
        .get("name")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim()
        .to_owned();
    if name.is_empty() {
        return Err(HeroSmsProviderError::BadResponse);
    }
    let count = value
        .get("count")
        .and_then(parse_i32)
        .ok_or(HeroSmsProviderError::BadResponse)?;
    let cost_micros = decimal_to_micros(
        value
            .get("cost")
            .and_then(decimal_string)
            .ok_or(HeroSmsProviderError::BadResponse)?
            .as_str(),
    )
    .ok_or(HeroSmsProviderError::BadResponse)?;
    Ok(HeroDomain {
        name,
        count,
        cost_micros,
    })
}

fn decode_email_record(value: &Value) -> Result<HeroEmailRecord, HeroSmsProviderError> {
    let email = value
        .get("email")
        .and_then(Value::as_str)
        .unwrap_or_default()
        .trim()
        .to_owned();
    if email.is_empty() || email.len() > 320 {
        return Err(HeroSmsProviderError::BadResponse);
    }
    let cost_micros = value
        .get("cost")
        .and_then(decimal_string)
        .map(|value| decimal_to_micros(&value).unwrap_or(0))
        .unwrap_or(0);
    Ok(HeroEmailRecord {
        id: value
            .get("id")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .trim()
            .to_owned(),
        email,
        code: value
            .get("value")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned(),
        message: value
            .get("message")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned(),
        status: value
            .get("status")
            .and_then(Value::as_str)
            .unwrap_or_default()
            .to_owned(),
        cost_micros,
        currency_code: value
            .get("currency")
            .and_then(parse_i32)
            .unwrap_or(HERO_SMS_CURRENCY_CODE),
    })
}

fn map_reqwest_error(error: reqwest::Error) -> HeroSmsProviderError {
    if error.is_timeout() {
        HeroSmsProviderError::UpstreamTimeout
    } else {
        HeroSmsProviderError::UpstreamBusy
    }
}

fn map_status(status: StatusCode) -> Result<(), HeroSmsProviderError> {
    match status {
        StatusCode::OK | StatusCode::CREATED | StatusCode::ACCEPTED | StatusCode::NO_CONTENT => {
            Ok(())
        }
        StatusCode::UNAUTHORIZED => Err(HeroSmsProviderError::Unauthorized),
        StatusCode::NOT_FOUND => Err(HeroSmsProviderError::NotFound),
        StatusCode::BAD_REQUEST | StatusCode::UNPROCESSABLE_ENTITY => {
            Err(HeroSmsProviderError::InvalidRequest)
        }
        StatusCode::TOO_MANY_REQUESTS => Err(HeroSmsProviderError::RateLimited),
        _ if status.is_server_error() => Err(HeroSmsProviderError::UpstreamBusy),
        _ => Err(HeroSmsProviderError::BadResponse),
    }
}

fn map_provider_error(error: HeroSmsProviderError) -> HeroSmsApiError {
    match error {
        HeroSmsProviderError::Unauthorized => HeroSmsApiError {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "NOT_CONFIGURED",
            message: "HeroSMS credentials are invalid",
        },
        HeroSmsProviderError::NotFound => HeroSmsApiError {
            status: StatusCode::NOT_FOUND,
            code: "NOT_FOUND",
            message: "HeroSMS resource not found",
        },
        HeroSmsProviderError::InvalidRequest => HeroSmsApiError {
            status: StatusCode::BAD_REQUEST,
            code: "INVALID_REQUEST",
            message: "HeroSMS request was rejected",
        },
        HeroSmsProviderError::RateLimited => HeroSmsApiError {
            status: StatusCode::TOO_MANY_REQUESTS,
            code: "RATE_LIMITED",
            message: "HeroSMS rate limited the request",
        },
        HeroSmsProviderError::UpstreamBusy => HeroSmsApiError {
            status: StatusCode::BAD_GATEWAY,
            code: "UPSTREAM_BUSY",
            message: "HeroSMS upstream is busy",
        },
        HeroSmsProviderError::UpstreamTimeout => HeroSmsApiError {
            status: StatusCode::ACCEPTED,
            code: "UPSTREAM_TIMEOUT",
            message: "HeroSMS purchase timed out and is pending reconciliation",
        },
        HeroSmsProviderError::BadResponse => HeroSmsApiError {
            status: StatusCode::BAD_GATEWAY,
            code: "BAD_UPSTREAM_RESPONSE",
            message: "HeroSMS returned an invalid response",
        },
        HeroSmsProviderError::BatchCountMismatch => HeroSmsApiError {
            status: StatusCode::BAD_GATEWAY,
            code: "BAD_UPSTREAM_RESPONSE",
            message: "HeroSMS returned an unexpected batch count",
        },
    }
}

fn urlencoding(value: &str) -> String {
    value
        .chars()
        .map(|character| {
            if character.is_ascii_alphanumeric() || matches!(character, '-' | '_' | '.' | '~') {
                character.to_string()
            } else {
                let mut encoded = String::new();
                for byte in character.to_string().as_bytes() {
                    encoded.push('%');
                    encoded.push_str(&format!("{byte:02X}"));
                }
                encoded
            }
        })
        .collect()
}

async fn get_options(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    if let Err(response) = require_root(&state, &headers).await {
        return disable_cache(response);
    }
    match settings_view(&state.pg).await {
        Ok(view) => disable_cache(hero_success(json!(view))),
        Err(error) => hero_error(error),
    }
}

async fn put_options(
    State(state): State<HeroSmsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    if let Err(response) = require_root(&state, &headers).await {
        return disable_cache(response);
    }
    let update = match parse_json::<HeroSmsSettingsUpdate>(request).await {
        Ok(update) => update,
        Err(response) => return disable_cache(response),
    };
    match update_settings(&state.pg, update).await {
        Ok(view) => disable_cache(hero_success(json!(view))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn test_options(
    State(state): State<HeroSmsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    if let Err(response) = require_root(&state, &headers).await {
        return disable_cache(response);
    }
    let input = parse_json::<HeroSmsTestInput>(request)
        .await
        .unwrap_or_default();
    match check_configuration(&state, input.api_key.trim()).await {
        Ok(()) => disable_cache(hero_success(json!({"ok": true}))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn delete_api_key(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    if let Err(response) = require_root(&state, &headers).await {
        return disable_cache(response);
    }
    match clear_api_key(&state.pg).await {
        Ok(view) => disable_cache(hero_success(json!(view))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn list_products(
    State(state): State<HeroSmsState>,
    Query(query): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    if let Err(response) = require_user(&state, &headers).await {
        return disable_cache(response);
    }
    let page = query
        .get("page")
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(1);
    let size = query
        .get("size")
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|value| *value > 0)
        .map(|value| value.min(100))
        .unwrap_or(20);
    let site = query.get("site").map(String::as_str).unwrap_or_default();
    match list_product_page(&state, page, size, site).await {
        Ok(page) => disable_cache(hero_success(json!(page))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn create_activations(
    State(state): State<HeroSmsState>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    if let Some(response) = user_critical_limit(&state, &headers).await {
        return disable_cache(response);
    }
    let idempotency = headers
        .get("Idempotency-Key")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .trim()
        .to_owned();
    let body = match parse_json::<PurchaseRequest>(request).await {
        Ok(body) => body,
        Err(response) => return disable_cache(response),
    };
    match create_purchase(&state, user.id, &idempotency, body, "purchase", None).await {
        Ok((order, status)) => {
            let activations = order.activations.clone();
            disable_cache(hero_success_status(
                status,
                json!({"order": order, "activations": activations}),
            ))
        }
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn list_activations(
    State(state): State<HeroSmsState>,
    Query(query): Query<BTreeMap<String, String>>,
    headers: HeaderMap,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    let page = query
        .get("page")
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(1);
    let size = query
        .get("size")
        .and_then(|value| value.parse::<i32>().ok())
        .filter(|value| *value > 0)
        .map(|value| value.min(100))
        .unwrap_or(20);
    let status = query.get("status").map(String::as_str).unwrap_or_default();
    match list_activation_page(&state.pg, user.id, page, size, status).await {
        Ok(page) => disable_cache(hero_success(json!(page))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn current_activation(State(state): State<HeroSmsState>, headers: HeaderMap) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    match current_activation_for_user(&state.pg, user.id).await {
        Ok(Some(activation)) => disable_cache(hero_success(json!(activation))),
        Ok(None) => disable_cache(hero_success(Value::Null)),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn get_activation(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    match activation_view_for_user(&state.pg, user.id, &id).await {
        Ok(activation) => disable_cache(hero_success(json!(activation))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn refresh_activation(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    if let Some(response) = user_critical_limit(&state, &headers).await {
        return disable_cache(response);
    }
    match refresh_activation_for_user(&state, user.id, &id).await {
        Ok(activation) => disable_cache(hero_success(json!(activation))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn cancel_activation(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    if let Some(response) = user_critical_limit(&state, &headers).await {
        return disable_cache(response);
    }
    match cancel_activation_for_user(&state, user.id, &id).await {
        Ok(activation) => disable_cache(hero_success(json!(activation))),
        Err(error) => disable_cache(hero_error(error)),
    }
}

async fn reorder_activation(
    State(state): State<HeroSmsState>,
    Path(id): Path<String>,
    headers: HeaderMap,
    request: Request,
) -> Response {
    let user = match require_user(&state, &headers).await {
        Ok(user) => user,
        Err(response) => return disable_cache(response),
    };
    if let Some(response) = user_critical_limit(&state, &headers).await {
        return disable_cache(response);
    }
    let idempotency = headers
        .get("Idempotency-Key")
        .and_then(|value| value.to_str().ok())
        .unwrap_or_default()
        .trim()
        .to_owned();
    let body = match parse_json::<ReorderRequest>(request).await {
        Ok(body) => body,
        Err(response) => return disable_cache(response),
    };
    if body.domain_id.trim().is_empty() {
        return disable_cache(hero_error(HeroSmsApiError {
            status: StatusCode::BAD_REQUEST,
            code: "INVALID_REQUEST",
            message: "a fresh HeroSMS reorder quote is required",
        }));
    }
    match reorder_activation_for_user(&state, user.id, &id, &idempotency, body.domain_id.trim())
        .await
    {
        Ok((order, status)) => disable_cache(hero_success_status(
            status,
            json!({"order": order, "activations": order.activations}),
        )),
        Err(error) => disable_cache(hero_error(error)),
    }
}

// Remaining implementation continues in the same module below.

#[derive(Clone, Debug)]
struct StoredOrder {
    id: String,
    user_id: i64,
    operation: String,
    idempotency_key_hash: String,
    request_payload_hash: String,
    domain_id: String,
    site: String,
    domain: String,
    quantity: i32,
    status: String,
    price_multiplier: String,
    reserved_unit_cost_micros: i64,
    reserved_unit_cost_decimal: String,
    customer_unit_price_micros: i64,
    charge_quota: i64,
    refunded_quota: i64,
    created_at: i64,
    updated_at: i64,
}

#[derive(Clone, Debug)]
struct StoredActivation {
    id: String,
    order_id: String,
    user_id: i64,
    slot: i32,
    status: String,
    domain_id: String,
    site: String,
    domain: String,
    provider_id: Option<String>,
    provider_email_ciphertext: String,
    provider_code_ciphertext: String,
    provider_message_ciphertext: String,
    provider_cost_micros: i64,
    charge_quota: i64,
    currency: String,
    currency_code: i32,
    cancel_reason: String,
    created_at: i64,
    updated_at: i64,
}

impl<'row> sqlx::FromRow<'row, sqlx::postgres::PgRow> for StoredOrder {
    fn from_row(row: &'row sqlx::postgres::PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            user_id: row.try_get("user_id")?,
            operation: row.try_get("operation")?,
            idempotency_key_hash: row.try_get("idempotency_key_hash")?,
            request_payload_hash: row.try_get("request_payload_hash")?,
            domain_id: row.try_get("domain_id")?,
            site: row.try_get("site")?,
            domain: row.try_get("domain")?,
            quantity: row.try_get("quantity")?,
            status: row.try_get("status")?,
            price_multiplier: row.try_get("price_multiplier")?,
            reserved_unit_cost_micros: row.try_get("reserved_unit_cost_micros")?,
            reserved_unit_cost_decimal: row.try_get("reserved_unit_cost_decimal")?,
            customer_unit_price_micros: row.try_get("customer_unit_price_micros")?,
            charge_quota: row.try_get("charge_quota")?,
            refunded_quota: row.try_get("refunded_quota")?,
            created_at: row.try_get("created_at")?,
            updated_at: row.try_get("updated_at")?,
        })
    }
}

impl<'row> sqlx::FromRow<'row, sqlx::postgres::PgRow> for StoredActivation {
    fn from_row(row: &'row sqlx::postgres::PgRow) -> Result<Self, sqlx::Error> {
        Ok(Self {
            id: row.try_get("id")?,
            order_id: row.try_get("order_id")?,
            user_id: row.try_get("user_id")?,
            slot: row.try_get("slot")?,
            status: row.try_get("status")?,
            domain_id: row.try_get("domain_id")?,
            site: row.try_get("site")?,
            domain: row.try_get("domain")?,
            provider_id: row.try_get("provider_id")?,
            provider_email_ciphertext: row.try_get("provider_email_ciphertext")?,
            provider_code_ciphertext: row.try_get("provider_code_ciphertext")?,
            provider_message_ciphertext: row.try_get("provider_message_ciphertext")?,
            provider_cost_micros: row.try_get("provider_cost_micros")?,
            charge_quota: row.try_get("charge_quota")?,
            currency: row.try_get("currency")?,
            currency_code: row.try_get("currency_code")?,
            cancel_reason: row.try_get("cancel_reason")?,
            created_at: row.try_get("created_at")?,
            updated_at: row.try_get("updated_at")?,
        })
    }
}

#[derive(Serialize, Deserialize)]
struct QuoteToken {
    s: String,
    d: String,
    c: String,
    m: String,
    iat: i64,
}

async fn require_root(state: &HeroSmsState, headers: &HeaderMap) -> Result<(), Response> {
    state
        .root
        .require_root_dashboard_session(headers)
        .await
        .map_err(|_| console_not_found())
        .map(|_| ())
}

async fn require_user(
    state: &HeroSmsState,
    headers: &HeaderMap,
) -> Result<DashboardUserView, Response> {
    let credential = dashboard_credential(headers).ok_or_else(console_not_found)?;
    let user = state
        .auth
        .self_user_view_for_optional(SecretString::from(credential))
        .await
        .map_err(|_| console_not_found())?;
    enforce_user_auth_view(&user).map_err(|error| user_auth_error(headers, error))?;
    Ok(user)
}

async fn user_critical_limit(state: &HeroSmsState, headers: &HeaderMap) -> Option<Response> {
    let client_ip = headers
        .get("x-forwarded-for")
        .and_then(|value| value.to_str().ok())
        .unwrap_or("unknown")
        .to_owned();
    match state.auth.check_critical_rate_limit(&client_ip).await {
        Ok(CriticalRateLimitOutcome::Allowed) => None,
        Ok(CriticalRateLimitOutcome::Rejected {
            retry_after_seconds,
        }) => Some(legacy_empty_response(
            StatusCode::TOO_MANY_REQUESTS,
            Some(retry_after_seconds),
        )),
        Err(_) => Some(legacy_empty_response(
            StatusCode::INTERNAL_SERVER_ERROR,
            None,
        )),
    }
}

fn dashboard_credential(headers: &HeaderMap) -> Option<String> {
    headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .map(str::to_owned)
}

fn user_auth_error(headers: &HeaderMap, error: UserAuthPolicyError) -> Response {
    let code = match error {
        UserAuthPolicyError::UserDisabled => "AUTH_USER_DISABLED",
        UserAuthPolicyError::InsufficientPrivilege => "AUTH_INSUFFICIENT_PRIVILEGE",
        UserAuthPolicyError::InvalidUserInfo => "AUTH_USER_INVALID",
    };
    let status = StatusCode::from_u16(user_auth_status(error)).unwrap_or(StatusCode::UNAUTHORIZED);
    (
        status,
        Json(json!({
            "success": false,
            "code": code,
            "message": user_auth_message(
                error,
                headers
                    .get(header::ACCEPT_LANGUAGE)
                    .and_then(|value| value.to_str().ok()),
            ),
        })),
    )
        .into_response()
}

fn console_not_found() -> Response {
    (StatusCode::NOT_FOUND, Json(json!({"message": "Not Found"}))).into_response()
}

async fn bounded_body(request: Request) -> Result<axum::body::Bytes, Response> {
    if request
        .headers()
        .get(header::CONTENT_LENGTH)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.parse::<usize>().ok())
        .is_some_and(|length| length > BODY_LIMIT_BYTES)
    {
        return Err(legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None));
    }
    to_bytes(request.into_body(), BODY_LIMIT_BYTES)
        .await
        .map_err(|_| legacy_empty_response(StatusCode::PAYLOAD_TOO_LARGE, None))
}

async fn parse_json<T>(request: Request) -> Result<T, Response>
where
    T: for<'de> Deserialize<'de> + Default,
{
    let bytes = bounded_body(request).await?;
    if bytes.is_empty() {
        return Ok(T::default());
    }
    serde_json::from_slice(&bytes).map_err(|_| hero_error(invalid_request()))
}

fn invalid_request() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::BAD_REQUEST,
        code: "INVALID_REQUEST",
        message: "invalid HeroSMS request",
    }
}

fn hero_success(data: Value) -> Response {
    Json(json!({"success": true, "data": data})).into_response()
}

fn hero_success_status(status: StatusCode, data: Value) -> Response {
    (status, Json(json!({"success": true, "data": data}))).into_response()
}

fn hero_error(error: HeroSmsApiError) -> Response {
    (
        error.status,
        Json(json!({
            "success": false,
            "code": error.code,
            "message": error.message,
        })),
    )
        .into_response()
}

fn disable_cache(mut response: Response) -> Response {
    response
        .headers_mut()
        .insert("auth-version", HeaderValue::from_static(AUTH_VERSION));
    response
        .headers_mut()
        .insert(header::CACHE_CONTROL, HeaderValue::from_static("no-store"));
    response
}

async fn load_options(pg: &PgPool) -> Result<BTreeMap<String, String>, HeroSmsApiError> {
    let rows = sqlx::query("SELECT key, value FROM options WHERE key LIKE 'hero_sms.%'")
        .fetch_all(pg)
        .await
        .map_err(|_| internal_error())?;
    let mut options = BTreeMap::new();
    for row in rows {
        let key: String = row.try_get("key").map_err(|_| internal_error())?;
        let value: String = row.try_get("value").map_err(|_| internal_error())?;
        options.insert(key, value);
    }
    Ok(options)
}

fn option_value(options: &BTreeMap<String, String>, key: &str, fallback: &str) -> String {
    options
        .get(key)
        .map(String::as_str)
        .map(str::trim)
        .filter(|value| !value.is_empty())
        .unwrap_or(fallback)
        .to_owned()
}

fn purchasing_enabled(options: &BTreeMap<String, String>) -> bool {
    option_value(options, OPTION_ENABLED, "false") == "true"
}

async fn configured_api_key(options: &BTreeMap<String, String>) -> Result<String, HeroSmsApiError> {
    let ciphertext = option_value(options, OPTION_API_KEY, "");
    if ciphertext.is_empty() {
        return Ok(String::new());
    }
    decrypt_persistent("hero_sms.api_key", &ciphertext).map_err(|_| not_configured())
}

async fn settings_view(pg: &PgPool) -> Result<HeroSmsSettingsView, HeroSmsApiError> {
    let options = load_options(pg).await?;
    let pending = has_pending_work(pg).await.map_err(|_| internal_error())?;
    let api_key = configured_api_key(&options).await?;
    Ok(HeroSmsSettingsView {
        enabled: purchasing_enabled(&options),
        email_enabled: option_value(&options, OPTION_EMAIL_ENABLED, "true") == "true",
        sms_enabled: option_value(&options, OPTION_SMS_ENABLED, "false") == "true",
        api_key_configured: !api_key.is_empty(),
        pending_work: pending,
        currency: HERO_SMS_CURRENCY.to_owned(),
        currency_code: HERO_SMS_CURRENCY_CODE,
        price_multiplier: option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER),
    })
}

async fn has_pending_work(pg: &PgPool) -> Result<bool, sqlx::Error> {
    sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM hero_sms_email_activations WHERE status IN \
         ('pending_provider','active','reconciling','cancel_pending')) OR \
         EXISTS(SELECT 1 FROM hero_sms_sms_orders WHERE status IN \
         ('pending_provider','purchase_unknown','active'))",
    )
    .fetch_one(pg)
    .await
}

async fn update_settings(
    pg: &PgPool,
    update: HeroSmsSettingsUpdate,
) -> Result<HeroSmsSettingsView, HeroSmsApiError> {
    let options = load_options(pg).await?;
    let mut multiplier = option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER);
    if !update.price_multiplier.trim().is_empty() {
        multiplier = parse_multiplier(&update.price_multiplier)?;
    }
    let mut enabled = purchasing_enabled(&options);
    if let Some(value) = update.enabled {
        enabled = value;
    }
    let email_enabled = update
        .email_enabled
        .unwrap_or_else(|| option_value(&options, OPTION_EMAIL_ENABLED, "true") == "true");
    let sms_enabled = update
        .sms_enabled
        .unwrap_or_else(|| option_value(&options, OPTION_SMS_ENABLED, "false") == "true");
    let mut effective_key = configured_api_key(&options).await?;
    if !update.api_key.trim().is_empty() {
        let candidate = update.api_key.trim();
        if candidate.len() < 16 || candidate.len() > 1024 {
            return Err(invalid_request());
        }
        if has_pending_work(pg).await.map_err(|_| internal_error())? {
            return Err(HeroSmsApiError {
                status: StatusCode::CONFLICT,
                code: "ACTIVE_ORDERS",
                message: "finish or reconcile active HeroSMS orders before replacing the API key",
            });
        }
        effective_key = candidate.to_owned();
        let encrypted = encrypt_persistent("hero_sms.api_key", candidate)?;
        upsert_option(pg, OPTION_API_KEY, &encrypted).await?;
    }
    if enabled && effective_key.is_empty() {
        return Err(HeroSmsApiError {
            status: StatusCode::BAD_REQUEST,
            code: "NOT_CONFIGURED",
            message: "configure the HeroSMS API key before enabling the service",
        });
    }
    encrypt_persistent("hero_sms.runtime_check", "configured")?;
    upsert_option(pg, OPTION_ENABLED, if enabled { "true" } else { "false" }).await?;
    upsert_option(
        pg,
        OPTION_EMAIL_ENABLED,
        if email_enabled { "true" } else { "false" },
    )
    .await?;
    upsert_option(
        pg,
        OPTION_SMS_ENABLED,
        if sms_enabled { "true" } else { "false" },
    )
    .await?;
    upsert_option(pg, OPTION_CURRENCY, HERO_SMS_CURRENCY).await?;
    upsert_option(pg, OPTION_CODE, &HERO_SMS_CURRENCY_CODE.to_string()).await?;
    upsert_option(pg, OPTION_MULTIPLIER, &multiplier).await?;
    settings_view(pg).await
}

async fn clear_api_key(pg: &PgPool) -> Result<HeroSmsSettingsView, HeroSmsApiError> {
    let options = load_options(pg).await?;
    if purchasing_enabled(&options) {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "INVALID_REQUEST",
            message: "disable HeroSMS before clearing the API key",
        });
    }
    if has_pending_work(pg).await.map_err(|_| internal_error())? {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "ACTIVE_ORDERS",
            message: "finish or reconcile active HeroSMS orders before clearing the API key",
        });
    }
    sqlx::query("DELETE FROM options WHERE key = $1")
        .bind(OPTION_API_KEY)
        .execute(pg)
        .await
        .map_err(|_| internal_error())?;
    settings_view(pg).await
}

async fn check_configuration(state: &HeroSmsState, candidate: &str) -> Result<(), HeroSmsApiError> {
    let options = load_options(&state.pg).await?;
    let api_key = if candidate.is_empty() {
        configured_api_key(&options).await?
    } else {
        candidate.to_owned()
    };
    if api_key.len() < 16 || api_key.len() > 1024 {
        return Err(HeroSmsApiError {
            status: StatusCode::BAD_REQUEST,
            code: "NOT_CONFIGURED",
            message: "configure a valid HeroSMS API key first",
        });
    }
    state
        .gateway
        .list_domains(&api_key, "")
        .await
        .map_err(map_provider_error)?;
    Ok(())
}

async fn upsert_option(pg: &PgPool, key: &str, value: &str) -> Result<(), HeroSmsApiError> {
    sqlx::query(
        "INSERT INTO options (key, value) VALUES ($1, $2) \
         ON CONFLICT (key) DO UPDATE SET value = EXCLUDED.value",
    )
    .bind(key)
    .bind(value)
    .execute(pg)
    .await
    .map_err(|_| internal_error())?;
    Ok(())
}

fn parse_multiplier(raw: &str) -> Result<String, HeroSmsApiError> {
    let micros = decimal_to_micros(raw.trim()).ok_or(invalid_request())?;
    if micros <= 0 || micros > 1_000_000_000 {
        return Err(invalid_request());
    }
    Ok(raw.trim().to_owned())
}

async fn operations_api_key(state: &HeroSmsState) -> Result<String, HeroSmsApiError> {
    let options = load_options(&state.pg).await?;
    let api_key = configured_api_key(&options).await?;
    if api_key.is_empty() {
        return Err(not_configured());
    }
    Ok(api_key)
}

async fn purchasing_api_key(state: &HeroSmsState) -> Result<String, HeroSmsApiError> {
    let options = load_options(&state.pg).await?;
    if !purchasing_enabled(&options)
        || option_value(&options, OPTION_EMAIL_ENABLED, "true") != "true"
    {
        return Err(HeroSmsApiError {
            status: StatusCode::SERVICE_UNAVAILABLE,
            code: "NOT_CONFIGURED",
            message: "HeroSMS purchasing is disabled",
        });
    }
    operations_api_key(state).await
}

async fn list_product_page(
    state: &HeroSmsState,
    page: i32,
    size: i32,
    site: &str,
) -> Result<HeroSmsProductPage, HeroSmsApiError> {
    let normalized_site = normalize_name(site).ok_or(invalid_request())?;
    let api_key = purchasing_api_key(state).await?;
    let domains = state
        .gateway
        .list_domains(&api_key, &normalized_site)
        .await
        .map_err(map_provider_error)?;
    let options = load_options(&state.pg).await?;
    let multiplier = option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER);
    let multiplier_micros = decimal_to_micros(&multiplier).ok_or(invalid_request())?;
    let mut products = Vec::new();
    for domain in domains {
        let normalized_domain = normalize_name(&domain.name)
            .ok_or_else(|| map_provider_error(HeroSmsProviderError::BadResponse))?;
        let customer_micros = mul_micros(domain.cost_micros, multiplier_micros);
        let charge_quota = charge_quota_from_price(customer_micros, &options)?;
        let product_id = encode_quote_id(
            &normalized_site,
            &normalized_domain,
            domain.cost_micros,
            &multiplier,
        )?;
        products.push(HeroSmsProduct {
            id: product_id,
            site: normalized_site.clone(),
            domain: normalized_domain,
            count: domain.count,
            available: domain.count > 0,
            cost_usd: micros_to_decimal(domain.cost_micros),
            customer_price_usd: micros_to_decimal(customer_micros),
            charge_quota,
        });
    }
    let start = ((page - 1) * size).max(0) as usize;
    let end = (start + size as usize).min(products.len());
    let slice = if start >= products.len() {
        &[][..]
    } else {
        &products[start..end]
    };
    Ok(HeroSmsProductPage {
        items: slice.to_vec(),
        page,
        size,
        total: products.len() as i32,
        price_multiplier: multiplier,
        currency: HERO_SMS_CURRENCY.to_owned(),
        currency_code: HERO_SMS_CURRENCY_CODE,
    })
}

async fn list_activation_page(
    pg: &PgPool,
    user_id: i64,
    page: i32,
    size: i32,
    status: &str,
) -> Result<ActivationPage, HeroSmsApiError> {
    let total: i64 = if status.trim().is_empty() {
        sqlx::query_scalar("SELECT COUNT(*) FROM hero_sms_email_activations WHERE user_id = $1")
            .bind(user_id)
            .fetch_one(pg)
            .await
    } else {
        sqlx::query_scalar(
            "SELECT COUNT(*) FROM hero_sms_email_activations WHERE user_id = $1 AND status = $2",
        )
        .bind(user_id)
        .bind(status.trim())
        .fetch_one(pg)
        .await
    }
    .map_err(|_| internal_error())?;
    let rows = if status.trim().is_empty() {
        sqlx::query_as::<_, StoredActivation>(
            "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
             COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
             COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
             COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
             provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
             COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
             FROM hero_sms_email_activations WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3",
        )
        .bind(user_id)
        .bind(size as i64)
        .bind(((page - 1) * size) as i64)
        .fetch_all(pg)
        .await
    } else {
        sqlx::query_as::<_, StoredActivation>(
            "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
             COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
             COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
             COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
             provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
             COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
             FROM hero_sms_email_activations WHERE user_id = $1 AND status = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4",
        )
        .bind(user_id)
        .bind(status.trim())
        .bind(size as i64)
        .bind(((page - 1) * size) as i64)
        .fetch_all(pg)
        .await
    }
    .map_err(|_| internal_error())?;
    let mut items = Vec::with_capacity(rows.len());
    for row in rows {
        let mut view = activation_view(&row).await?;
        view.message = String::new();
        items.push(view);
    }
    Ok(ActivationPage {
        items,
        page,
        size,
        total,
    })
}

async fn current_activation_for_user(
    pg: &PgPool,
    user_id: i64,
) -> Result<Option<ActivationView>, HeroSmsApiError> {
    let row = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE user_id = $1 AND status IN \
         ('pending_provider','active','reconciling','cancel_pending') ORDER BY created_at DESC LIMIT 1",
    )
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|_| internal_error())?;
    match row {
        Some(row) => Ok(Some(activation_view(&row).await?)),
        None => Ok(None),
    }
}

async fn activation_view_for_user(
    pg: &PgPool,
    user_id: i64,
    activation_id: &str,
) -> Result<ActivationView, HeroSmsApiError> {
    let row = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE id = $1 AND user_id = $2",
    )
    .bind(activation_id)
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|_| internal_error())?
    .ok_or(not_found())?;
    activation_view(&row).await
}

async fn activation_view(row: &StoredActivation) -> Result<ActivationView, HeroSmsApiError> {
    Ok(ActivationView {
        id: row.id.clone(),
        order_id: row.order_id.clone(),
        status: row.status.clone(),
        domain_id: row.domain_id.clone(),
        site: row.site.clone(),
        domain: row.domain.clone(),
        email: decrypt_payload(&row.provider_email_ciphertext).unwrap_or_default(),
        code: decrypt_payload(&row.provider_code_ciphertext).unwrap_or_default(),
        message: decrypt_payload(&row.provider_message_ciphertext).unwrap_or_default(),
        charge_quota: row.charge_quota,
        cost_usd: micros_to_decimal(row.provider_cost_micros),
        currency: row.currency.clone(),
        currency_code: row.currency_code,
        cancel_reason: row.cancel_reason.clone(),
        created_at: row.created_at,
        updated_at: row.updated_at,
    })
}

async fn create_purchase(
    state: &HeroSmsState,
    user_id: i64,
    idempotency_key: &str,
    request: PurchaseRequest,
    operation: &str,
    reorder_of: Option<&str>,
) -> Result<(OrderView, StatusCode), HeroSmsApiError> {
    if idempotency_key.is_empty() || idempotency_key.len() > 128 {
        return Err(invalid_request());
    }
    if request.domain_id.trim().is_empty() || request.quantity < 1 || request.quantity > 10 {
        return Err(invalid_request());
    }
    let payload_hash = payload_hash(operation, &request, reorder_of);
    let idempotency_hash = hash_string(&format!("{user_id}:{operation}:{idempotency_key}"));
    if let Some(existing) =
        load_order_by_idempotency(&state.pg, user_id, operation, &idempotency_hash).await?
    {
        if existing.request_payload_hash != payload_hash {
            return Err(HeroSmsApiError {
                status: StatusCode::CONFLICT,
                code: "IDEMPOTENCY_MISMATCH",
                message: "idempotent request payload mismatch",
            });
        }
        let view = order_view(&state.pg, &existing).await?;
        let status = if existing.status == ORDER_STATUS_COMPLETED {
            StatusCode::CREATED
        } else {
            StatusCode::ACCEPTED
        };
        return Ok((view, status));
    }
    let api_key = purchasing_api_key(state).await?;
    let quote = lookup_quote(state, &api_key, request.domain_id.trim()).await?;
    if quote.count < request.quantity {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "PRICE_CHANGED",
            message: "HeroSMS inventory changed; refresh the quote",
        });
    }
    let options = load_options(&state.pg).await?;
    let multiplier = option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER);
    let customer_unit = mul_micros(
        quote.cost_micros,
        decimal_to_micros(&multiplier).ok_or(invalid_request())?,
    );
    let charge_quota =
        charge_quota_from_price(customer_unit * i64::from(request.quantity), &options)?;
    let order_id = format!("hseord_{}", Uuid::new_v4());
    let now = now_unix();
    reserve_order(
        &state.pg,
        &order_id,
        user_id,
        operation,
        &idempotency_hash,
        &payload_hash,
        &request,
        &quote,
        &multiplier,
        customer_unit,
        charge_quota,
        now,
    )
    .await?;
    let records = if request.quantity == 1 {
        vec![
            state
                .gateway
                .create_email(&api_key, &quote.site, &quote.domain)
                .await
                .map_err(map_provider_error)?,
        ]
    } else {
        state
            .gateway
            .create_email_batch(&api_key, &quote.site, &quote.domain, request.quantity)
            .await
            .map_err(map_provider_error)?
    };
    finalize_order(&state.pg, &order_id, &records).await?;
    let order = load_order_by_id(&state.pg, user_id, &order_id)
        .await?
        .ok_or(not_found())?;
    let view = order_view(&state.pg, &order).await?;
    Ok((view, StatusCode::CREATED))
}

async fn reorder_activation_for_user(
    state: &HeroSmsState,
    user_id: i64,
    activation_id: &str,
    idempotency_key: &str,
    domain_id: &str,
) -> Result<(OrderView, StatusCode), HeroSmsApiError> {
    let activation = activation_view_for_user(&state.pg, user_id, activation_id).await?;
    if !matches!(
        activation.status.as_str(),
        ACTIVATION_COMPLETED | ACTIVATION_CANCELLED | "expired" | ACTIVATION_REFUNDED
    ) {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "INVALID_REQUEST",
            message: "only terminal HeroSMS activations can be reordered",
        });
    }
    let request = PurchaseRequest {
        domain_id: domain_id.to_owned(),
        quantity: 1,
    };
    create_purchase(
        state,
        user_id,
        idempotency_key,
        request,
        "reorder",
        Some(activation_id),
    )
    .await
}

async fn refresh_activation_for_user(
    state: &HeroSmsState,
    user_id: i64,
    activation_id: &str,
) -> Result<ActivationView, HeroSmsApiError> {
    let row = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE id = $1 AND user_id = $2",
    )
    .bind(activation_id)
    .bind(user_id)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| internal_error())?
    .ok_or(not_found())?;
    let api_key = operations_api_key(state).await?;
    if row.provider_id.as_deref().unwrap_or("").is_empty() {
        return activation_view(&row).await;
    }
    let record = state
        .gateway
        .get_email(&api_key, row.provider_id.as_deref().unwrap_or_default())
        .await
        .map_err(map_provider_error)?;
    persist_record(&state.pg, &row.id, &record).await?;
    activation_view_for_user(&state.pg, user_id, activation_id).await
}

async fn cancel_activation_for_user(
    state: &HeroSmsState,
    user_id: i64,
    activation_id: &str,
) -> Result<ActivationView, HeroSmsApiError> {
    let row = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE id = $1 AND user_id = $2",
    )
    .bind(activation_id)
    .bind(user_id)
    .fetch_optional(&state.pg)
    .await
    .map_err(|_| internal_error())?
    .ok_or(not_found())?;
    if matches!(
        row.status.as_str(),
        ACTIVATION_COMPLETED | "expired" | ACTIVATION_CANCELLED | ACTIVATION_REFUNDED
    ) {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "INVALID_REQUEST",
            message: "terminal HeroSMS activation cannot be cancelled",
        });
    }
    sqlx::query(
        "UPDATE hero_sms_email_activations SET status = $2, cancel_reason = 'user_cancel', updated_at = $3 WHERE id = $1",
    )
    .bind(activation_id)
    .bind(ACTIVATION_CANCEL_PENDING)
    .bind(now_unix())
    .execute(&state.pg)
    .await
    .map_err(|_| internal_error())?;
    if let Some(provider_id) = row.provider_id.as_deref().filter(|value| !value.is_empty()) {
        let api_key = operations_api_key(state).await?;
        let _ = state.gateway.delete_email(&api_key, provider_id).await;
        sqlx::query(
            "UPDATE hero_sms_email_activations SET status = $2, cancelled_at = $3, updated_at = $3 WHERE id = $1",
        )
        .bind(activation_id)
        .bind(ACTIVATION_CANCELLED)
        .bind(now_unix())
        .execute(&state.pg)
        .await
        .map_err(|_| internal_error())?;
    }
    activation_view_for_user(&state.pg, user_id, activation_id).await
}

#[derive(Clone)]
struct DomainQuote {
    site: String,
    domain: String,
    count: i32,
    cost_micros: i64,
}

async fn lookup_quote(
    state: &HeroSmsState,
    api_key: &str,
    domain_id: &str,
) -> Result<DomainQuote, HeroSmsApiError> {
    let token = decode_quote_id(domain_id)?;
    let now = now_unix();
    if token.iat > now + 60 || now - token.iat > 300 {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "PRICE_CHANGED",
            message: "HeroSMS quote expired; refresh the quote",
        });
    }
    let options = load_options(&state.pg).await?;
    let current_multiplier = option_value(&options, OPTION_MULTIPLIER, DEFAULT_PRICE_MULTIPLIER);
    if token.m != current_multiplier {
        return Err(HeroSmsApiError {
            status: StatusCode::CONFLICT,
            code: "PRICE_CHANGED",
            message: "HeroSMS price changed; refresh the quote",
        });
    }
    let quoted_cost = decimal_to_micros(&token.c).ok_or(invalid_request())?;
    let domains = state
        .gateway
        .list_domains(api_key, &token.s)
        .await
        .map_err(map_provider_error)?;
    for domain in domains {
        if normalize_name(&domain.name) == Some(token.d.clone())
            && domain.count > 0
            && domain.cost_micros == quoted_cost
        {
            return Ok(DomainQuote {
                site: token.s,
                domain: token.d,
                count: domain.count,
                cost_micros: domain.cost_micros,
            });
        }
    }
    Err(not_found())
}

async fn reserve_order(
    pg: &PgPool,
    order_id: &str,
    user_id: i64,
    operation: &str,
    idempotency_hash: &str,
    payload_hash: &str,
    request: &PurchaseRequest,
    quote: &DomainQuote,
    multiplier: &str,
    customer_unit: i64,
    charge_quota: i64,
    now: i64,
) -> Result<(), HeroSmsApiError> {
    let mut tx = pg.begin().await.map_err(|_| internal_error())?;
    let quota: i64 = sqlx::query_scalar(
        "SELECT quota FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(user_id)
    .fetch_one(&mut *tx)
    .await
    .map_err(|_| internal_error())?;
    if quota < charge_quota {
        return Err(HeroSmsApiError {
            status: StatusCode::PAYMENT_REQUIRED,
            code: "INSUFFICIENT_BALANCE",
            message: "insufficient quota balance",
        });
    }
    let updated = sqlx::query("UPDATE users SET quota = quota - $2 WHERE id = $1 AND quota >= $2")
        .bind(user_id)
        .bind(charge_quota)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?
        .rows_affected();
    if updated != 1 {
        return Err(HeroSmsApiError {
            status: StatusCode::PAYMENT_REQUIRED,
            code: "INSUFFICIENT_BALANCE",
            message: "insufficient quota balance",
        });
    }
    sqlx::query(
        "INSERT INTO hero_sms_email_orders \
         (id, user_id, operation, idempotency_key_hash, request_payload_hash, domain_id, site, domain, quantity, status, \
          price_multiplier, reserved_unit_cost_micros, reserved_unit_cost_decimal, customer_unit_price_micros, charge_quota, \
          refunded_quota, currency, currency_code, last_error_code, last_error_message, created_at, updated_at) \
         VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,0,$16,$17,$18,$19,$20,$20)",
    )
    .bind(order_id)
    .bind(user_id)
    .bind(operation)
    .bind(idempotency_hash)
    .bind(payload_hash)
    .bind(request.domain_id.trim())
    .bind(&quote.site)
    .bind(&quote.domain)
    .bind(request.quantity)
    .bind(ORDER_STATUS_PENDING)
    .bind(multiplier)
    .bind(quote.cost_micros)
    .bind(micros_to_decimal(quote.cost_micros))
    .bind(customer_unit)
    .bind(charge_quota)
    .bind(HERO_SMS_CURRENCY)
    .bind(HERO_SMS_CURRENCY_CODE)
    .bind("PROVIDER_INTENT_PENDING")
    .bind("provider purchase intent is reserved but not started")
    .bind(now)
    .execute(&mut *tx)
    .await
    .map_err(|_| internal_error())?;
    for slot in 1..=request.quantity {
        let activation_id = format!("hseact_{}", Uuid::new_v4());
        sqlx::query(
            "INSERT INTO hero_sms_email_activations \
             (id, order_id, user_id, slot, status, domain_id, site, domain, charge_quota, created_at, updated_at) \
             VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$10)",
        )
        .bind(activation_id)
        .bind(order_id)
        .bind(user_id)
        .bind(slot)
        .bind(ACTIVATION_PENDING)
        .bind(request.domain_id.trim())
        .bind(&quote.site)
        .bind(&quote.domain)
        .bind(quota_for_slot(charge_quota, request.quantity, slot))
        .bind(now)
        .execute(&mut *tx)
        .await
        .map_err(|_| internal_error())?;
    }
    sqlx::query(
        "INSERT INTO hero_sms_email_quota_ledgers (user_id, order_id, entry_type, amount_quota, idempotency_key, created_at) \
         VALUES ($1,$2,'reserve',-$3,$4,$5)",
    )
    .bind(user_id)
    .bind(order_id)
    .bind(charge_quota)
    .bind(format!("hero_sms:reserve:{order_id}"))
    .bind(now)
    .execute(&mut *tx)
    .await
    .map_err(|_| internal_error())?;
    tx.commit().await.map_err(|_| internal_error())?;
    Ok(())
}

async fn finalize_order(
    pg: &PgPool,
    order_id: &str,
    records: &[HeroEmailRecord],
) -> Result<(), HeroSmsApiError> {
    let activations = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE order_id = $1 ORDER BY slot ASC",
    )
    .bind(order_id)
    .fetch_all(pg)
    .await
    .map_err(|_| internal_error())?;
    let now = now_unix();
    for (index, activation) in activations.iter().enumerate() {
        let record = records.get(index);
        let status = if record
            .map(|value| !value.code.trim().is_empty() || !value.message.trim().is_empty())
            .unwrap_or(false)
        {
            ACTIVATION_COMPLETED
        } else {
            ACTIVATION_ACTIVE
        };
        if let Some(record) = record {
            persist_record(pg, &activation.id, record).await?;
            sqlx::query(
                "UPDATE hero_sms_email_activations SET status = $2, updated_at = $3 WHERE id = $1",
            )
            .bind(&activation.id)
            .bind(status)
            .bind(now)
            .execute(pg)
            .await
            .map_err(|_| internal_error())?;
        }
    }
    sqlx::query(
        "UPDATE hero_sms_email_orders SET status = $2, last_error_code = '', last_error_message = '', updated_at = $3 WHERE id = $1",
    )
    .bind(order_id)
    .bind(ORDER_STATUS_COMPLETED)
    .bind(now)
    .execute(pg)
    .await
    .map_err(|_| internal_error())?;
    Ok(())
}

async fn persist_record(
    pg: &PgPool,
    activation_id: &str,
    record: &HeroEmailRecord,
) -> Result<(), HeroSmsApiError> {
    let email = encrypt_payload(&record.email)?;
    let code = encrypt_payload(&record.code)?;
    let message = encrypt_payload(&record.message)?;
    sqlx::query(
        "UPDATE hero_sms_email_activations SET provider_id = $2, provider_email_ciphertext = $3, \
         provider_code_ciphertext = $4, provider_message_ciphertext = $5, provider_cost_micros = $6, \
         currency = $7, currency_code = $8, updated_at = $9 WHERE id = $1",
    )
    .bind(activation_id)
    .bind(record.id.trim())
    .bind(email)
    .bind(code)
    .bind(message)
    .bind(record.cost_micros)
    .bind(HERO_SMS_CURRENCY)
    .bind(record.currency_code)
    .bind(now_unix())
    .execute(pg)
    .await
    .map_err(|_| internal_error())?;
    Ok(())
}

async fn load_order_by_idempotency(
    pg: &PgPool,
    user_id: i64,
    operation: &str,
    hash: &str,
) -> Result<Option<StoredOrder>, HeroSmsApiError> {
    sqlx::query_as::<_, StoredOrder>(
        "SELECT id, user_id, operation, idempotency_key_hash, request_payload_hash, domain_id, site, domain, quantity, status, \
         price_multiplier, reserved_unit_cost_micros, reserved_unit_cost_decimal, customer_unit_price_micros, charge_quota, \
         refunded_quota, created_at, updated_at FROM hero_sms_email_orders \
         WHERE user_id = $1 AND operation = $2 AND idempotency_key_hash = $3",
    )
    .bind(user_id)
    .bind(operation)
    .bind(hash)
    .fetch_optional(pg)
    .await
    .map_err(|_| internal_error())
}

async fn load_order_by_id(
    pg: &PgPool,
    user_id: i64,
    order_id: &str,
) -> Result<Option<StoredOrder>, HeroSmsApiError> {
    sqlx::query_as::<_, StoredOrder>(
        "SELECT id, user_id, operation, idempotency_key_hash, request_payload_hash, domain_id, site, domain, quantity, status, \
         price_multiplier, reserved_unit_cost_micros, reserved_unit_cost_decimal, customer_unit_price_micros, charge_quota, \
         refunded_quota, created_at, updated_at FROM hero_sms_email_orders WHERE id = $1 AND user_id = $2",
    )
    .bind(order_id)
    .bind(user_id)
    .fetch_optional(pg)
    .await
    .map_err(|_| internal_error())
}

async fn order_view(pg: &PgPool, order: &StoredOrder) -> Result<OrderView, HeroSmsApiError> {
    let activations = sqlx::query_as::<_, StoredActivation>(
        "SELECT id, order_id, user_id, slot, status, domain_id, site, domain, provider_id, \
         COALESCE(provider_email_ciphertext,'') AS provider_email_ciphertext, \
         COALESCE(provider_code_ciphertext,'') AS provider_code_ciphertext, \
         COALESCE(provider_message_ciphertext,'') AS provider_message_ciphertext, \
         provider_cost_micros, charge_quota, COALESCE(currency,'') AS currency, currency_code, \
         COALESCE(cancel_reason,'') AS cancel_reason, created_at, updated_at \
         FROM hero_sms_email_activations WHERE order_id = $1 ORDER BY slot ASC",
    )
    .bind(&order.id)
    .fetch_all(pg)
    .await
    .map_err(|_| internal_error())?;
    let mut views = Vec::with_capacity(activations.len());
    for activation in activations {
        views.push(activation_view(&activation).await?);
    }
    Ok(OrderView {
        id: order.id.clone(),
        operation: order.operation.clone(),
        status: order.status.clone(),
        domain_id: order.domain_id.clone(),
        site: order.site.clone(),
        domain: order.domain.clone(),
        quantity: order.quantity,
        price_multiplier: order.price_multiplier.clone(),
        reserved_cost_usd: order.reserved_unit_cost_decimal.clone(),
        customer_price_usd: micros_to_decimal(order.customer_unit_price_micros),
        charge_quota: order.charge_quota,
        refunded_quota: order.refunded_quota,
        created_at: order.created_at,
        updated_at: order.updated_at,
        activations: views,
    })
}

fn encode_quote_id(
    site: &str,
    domain: &str,
    cost_micros: i64,
    multiplier: &str,
) -> Result<String, HeroSmsApiError> {
    let token = QuoteToken {
        s: site.to_owned(),
        d: domain.to_owned(),
        c: micros_to_decimal(cost_micros),
        m: multiplier.to_owned(),
        iat: now_unix(),
    };
    let payload = serde_json::to_string(&token).map_err(|_| internal_error())?;
    let ciphertext = encrypt_persistent("hero_sms.quote", &payload)?;
    Ok(format!(
        "hsq_{}",
        URL_SAFE_NO_PAD.encode(ciphertext.as_bytes())
    ))
}

fn decode_quote_id(value: &str) -> Result<QuoteToken, HeroSmsApiError> {
    if !value.starts_with("hsq_") || value.len() > 2048 {
        return Err(invalid_request());
    }
    let encoded = URL_SAFE_NO_PAD
        .decode(value.trim_start_matches("hsq_"))
        .map_err(|_| invalid_request())?;
    let plaintext = decrypt_persistent(
        "hero_sms.quote",
        std::str::from_utf8(&encoded).map_err(|_| invalid_request())?,
    )?;
    let mut token: QuoteToken = serde_json::from_str(&plaintext).map_err(|_| invalid_request())?;
    token.s = normalize_name(&token.s).ok_or(invalid_request())?;
    token.d = normalize_name(&token.d).ok_or(invalid_request())?;
    Ok(token)
}

fn payload_hash(operation: &str, request: &PurchaseRequest, reorder_of: Option<&str>) -> String {
    let mut body = json!({
        "operation": operation,
        "domain_id": request.domain_id,
        "quantity": request.quantity,
    });
    if let Some(value) = reorder_of {
        body["reorder_of_activation_id"] = json!(value);
    }
    hash_string(&body.to_string())
}

fn hash_string(value: &str) -> String {
    hex::encode(Sha256::digest(value.as_bytes()))
}

fn quota_for_slot(total: i64, quantity: i32, slot: i32) -> i64 {
    if quantity <= 1 {
        return total;
    }
    let base = total / i64::from(quantity);
    let remainder = total % i64::from(quantity);
    if slot >= 1 && slot <= remainder as i32 {
        base + 1
    } else {
        base
    }
}

fn charge_quota_from_price(
    price_micros: i64,
    options: &BTreeMap<String, String>,
) -> Result<i64, HeroSmsApiError> {
    let quota_per_unit = option_value(options, "QuotaPerUnit", &DEFAULT_QUOTA_PER_UNIT.to_string())
        .parse::<i64>()
        .unwrap_or(DEFAULT_QUOTA_PER_UNIT);
    let numerator = price_micros.saturating_mul(quota_per_unit);
    let quota = (numerator + 999_999) / 1_000_000;
    if quota <= 0 || quota > i64::MAX / 2 {
        return Err(invalid_request());
    }
    Ok(quota)
}

fn normalize_name(value: &str) -> Option<String> {
    let normalized = value.trim().trim_end_matches('.').to_ascii_lowercase();
    if normalized.is_empty() || normalized.len() > 253 {
        return None;
    }
    if normalized.starts_with('.') || normalized.ends_with("..") {
        return None;
    }
    if !normalized
        .chars()
        .all(|character| character.is_ascii_alphanumeric() || matches!(character, '.' | '-'))
    {
        return None;
    }
    Some(normalized)
}

fn decimal_string(value: &Value) -> Option<String> {
    match value {
        Value::String(text) => Some(text.trim().to_owned()),
        Value::Number(number) => Some(number.to_string()),
        _ => None,
    }
}

fn parse_i32(value: &Value) -> Option<i32> {
    match value {
        Value::Number(number) => number.as_i64().map(|value| value as i32),
        Value::String(text) => text.trim().parse().ok(),
        _ => None,
    }
}

fn decimal_to_micros(raw: &str) -> Option<i64> {
    let raw = raw.trim();
    if raw.is_empty() {
        return Some(0);
    }
    let negative = raw.starts_with('-');
    let unsigned = raw.trim_start_matches(['-', '+']);
    let (whole, fraction) = unsigned.split_once('.').unwrap_or((unsigned, ""));
    if !whole.bytes().all(|byte| byte.is_ascii_digit())
        || !fraction.bytes().all(|byte| byte.is_ascii_digit())
    {
        return None;
    }
    let whole: i64 = if whole.is_empty() {
        0
    } else {
        whole.parse().ok()?
    };
    let mut fraction_value = fraction.to_owned();
    while fraction_value.len() < 6 {
        fraction_value.push('0');
    }
    fraction_value.truncate(6);
    let fraction: i64 = if fraction_value.is_empty() {
        0
    } else {
        fraction_value.parse().ok()?
    };
    let micros = whole * 1_000_000 + fraction;
    Some(if negative { -micros } else { micros })
}

fn micros_to_decimal(micros: i64) -> String {
    let negative = micros < 0;
    let value = micros.unsigned_abs();
    let whole = value / 1_000_000;
    let fraction = value % 1_000_000;
    if negative {
        format!("-{whole}.{fraction:06}")
    } else {
        format!("{whole}.{fraction:06}")
    }
}

fn mul_micros(left: i64, right: i64) -> i64 {
    ((left as i128 * right as i128 + 999_999) / 1_000_000) as i64
}

fn now_unix() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map(|duration| duration.as_secs() as i64)
        .unwrap_or(0)
}

fn not_configured() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::SERVICE_UNAVAILABLE,
        code: "NOT_CONFIGURED",
        message: "HeroSMS encryption key is unavailable",
    }
}

fn not_found() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::NOT_FOUND,
        code: "NOT_FOUND",
        message: "HeroSMS activation not found",
    }
}

fn internal_error() -> HeroSmsApiError {
    HeroSmsApiError {
        status: StatusCode::INTERNAL_SERVER_ERROR,
        code: "INTERNAL_ERROR",
        message: "HeroSMS operation failed",
    }
}

fn persistent_key(purpose: &str) -> Result<[u8; 32], HeroSmsApiError> {
    let secret = std::env::var("HERO_SMS_ENCRYPTION_KEY")
        .ok()
        .filter(|value| !value.trim().is_empty())
        .or_else(|| std::env::var("CRYPTO_SECRET").ok())
        .ok_or(not_configured())?;
    if secret.len() < 32 || weak_key_material(&secret) {
        return Err(not_configured());
    }
    Ok(Sha256::digest(format!("{purpose}:{secret}").as_bytes()).into())
}

fn weak_key_material(secret: &str) -> bool {
    let lower = secret.to_ascii_lowercase();
    for marker in [
        "replace_with",
        "random_string",
        "your_secret",
        "example-secret",
        "change-me",
        "changeme",
    ] {
        if lower.contains(marker) {
            return true;
        }
    }
    secret
        .chars()
        .collect::<std::collections::BTreeSet<_>>()
        .len()
        < 4
}

fn encrypt_persistent(purpose: &str, plaintext: &str) -> Result<String, HeroSmsApiError> {
    let key = persistent_key(purpose)?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|_| not_configured())?;
    let mut nonce_bytes = [0_u8; 12];
    rand::rng().fill_bytes(&mut nonce_bytes);
    let nonce = Nonce::from_slice(&nonce_bytes);
    let ciphertext = cipher
        .encrypt(nonce, plaintext.as_bytes())
        .map_err(|_| not_configured())?;
    let mut payload = nonce_bytes.to_vec();
    payload.extend(ciphertext);
    Ok(format!(
        "{PERSISTENT_CIPHER_ENVELOPE}{}",
        URL_SAFE_NO_PAD.encode(payload)
    ))
}

fn decrypt_persistent(purpose: &str, ciphertext: &str) -> Result<String, HeroSmsApiError> {
    let trimmed = ciphertext.trim();
    if trimmed.is_empty() {
        return Ok(String::new());
    }
    if !trimmed.starts_with(PERSISTENT_CIPHER_ENVELOPE) {
        return Err(not_configured());
    }
    let encoded = URL_SAFE_NO_PAD
        .decode(trimmed.trim_start_matches(PERSISTENT_CIPHER_ENVELOPE))
        .map_err(|_| not_configured())?;
    if encoded.len() < 12 {
        return Err(not_configured());
    }
    let key = persistent_key(purpose)?;
    let cipher = Aes256Gcm::new_from_slice(&key).map_err(|_| not_configured())?;
    let nonce = Nonce::from_slice(&encoded[..12]);
    let plaintext = cipher
        .decrypt(nonce, &encoded[12..])
        .map_err(|_| not_configured())?;
    String::from_utf8(plaintext).map_err(|_| not_configured())
}

fn encrypt_payload(value: &str) -> Result<String, HeroSmsApiError> {
    if value.trim().is_empty() {
        return Ok(String::new());
    }
    encrypt_persistent("hero_sms.payload", value)
}

fn decrypt_payload(value: &str) -> Result<String, HeroSmsApiError> {
    decrypt_persistent("hero_sms.payload", value)
}
