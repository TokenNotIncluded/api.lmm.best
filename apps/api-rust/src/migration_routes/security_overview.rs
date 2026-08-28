//! Legacy-compatible public and administrator advanced-security overview routes.
//!
//! The public policy deliberately omits matcher patterns. Administrator routes
//! preserve the console discovery gate before the ordinary user/admin policy so
//! pre-activation (L0) accounts cannot discover this surface.

use std::{
    collections::HashMap,
    sync::Arc,
    time::{SystemTime, UNIX_EPOCH},
};

use async_trait::async_trait;
use axum::{
    Json, Router,
    extract::{RawQuery, State},
    http::{HeaderMap, HeaderName, HeaderValue, StatusCode, header},
    response::{IntoResponse, Response},
    routing::get,
};
use secrecy::SecretString;
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgPool, Postgres, QueryBuilder, Row, postgres::PgRow};

use crate::auth::{DashboardAuth, UserAuthPolicyError, enforce_user_auth_view};

const ADMIN_ROLE: i64 = 10;
const AUTH_VERSION: &str = "864b7076dbcd0a3c01b5520316720ebf";
const PUBLIC_STATS_WINDOW_SECONDS: i64 = 30 * 24 * 60 * 60;
const PUBLIC_STATS_MAX_WINDOW_SECONDS: i64 = 90 * 24 * 60 * 60;
const DEFAULT_PAGE_SIZE: i64 = 10;
const MAX_PAGE_SIZE: i64 = 100;

/// PostgreSQL and dashboard-auth dependencies for the five security routes.
#[derive(Clone)]
pub struct SecurityOverviewState {
    backend: Arc<dyn SecurityOverviewBackend>,
    auth: Arc<dyn DashboardAuth>,
}

impl SecurityOverviewState {
    /// Creates production state backed by the listener's shared PostgreSQL pool.
    #[must_use]
    pub fn new(pool: PgPool, auth: Arc<dyn DashboardAuth>) -> Self {
        Self::with_backend(Arc::new(PgSecurityOverviewBackend { pool }), auth)
    }

    /// Creates state around an alternate backend for contract tests or embedding.
    #[must_use]
    pub fn with_backend(
        backend: Arc<dyn SecurityOverviewBackend>,
        auth: Arc<dyn DashboardAuth>,
    ) -> Self {
        Self { backend, auth }
    }
}

/// Builds the two public and three administrator security overview routes.
pub fn router(state: SecurityOverviewState) -> Router {
    Router::new()
        .route("/api/security/policy", get(public_policy))
        .route("/api/security/stats", get(public_stats))
        .route("/api/security/admin/policy", get(admin_policy))
        .route("/api/security/admin/stats", get(admin_stats))
        .route("/api/security/admin/events", get(admin_events))
        .with_state(state)
}

/// Raw policy configuration obtained from durable options.
#[derive(Clone, Debug, PartialEq)]
pub struct SecurityPolicySnapshot {
    pub enabled: bool,
    pub on_prompt: bool,
    pub action: String,
    pub rules: Vec<SecurityRule>,
    pub grok_violation_fee_enabled: bool,
    pub grok_violation_fee_amount_usd: f64,
}

impl Default for SecurityPolicySnapshot {
    fn default() -> Self {
        Self {
            enabled: false,
            on_prompt: true,
            action: "block".to_owned(),
            rules: Vec::new(),
            grok_violation_fee_enabled: true,
            grok_violation_fee_amount_usd: 0.05,
        }
    }
}

/// One normalized operator-managed literal security rule.
#[derive(Clone, Debug, Deserialize, PartialEq, Serialize)]
pub struct SecurityRule {
    pub id: String,
    pub name: String,
    pub category: String,
    pub layer: String,
    pub severity: String,
    pub source: String,
    pub version: String,
    pub description: String,
    pub enabled: bool,
    pub patterns: Vec<String>,
}

/// Filters shared by administrator statistics and event listing queries.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct SecurityEventFilter {
    pub start_timestamp: i64,
    pub end_timestamp: i64,
    pub user_id: i64,
    pub rule_id: String,
    pub category: String,
    pub decision: String,
    pub model_name: String,
}

/// One grouped statistics bucket.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct SecurityStatBucket {
    pub key: String,
    pub count: i64,
}

/// Aggregate advanced-security event statistics.
#[derive(Clone, Debug, Default, Eq, PartialEq)]
pub struct SecurityStats {
    pub total_matches: i64,
    pub blocked_matches: i64,
    pub audited_matches: i64,
    pub affected_requests: i64,
    pub affected_users: i64,
    pub by_category: Vec<SecurityStatBucket>,
    pub by_rule: Vec<SecurityStatBucket>,
}

/// Safe event projection. Prompt text and matcher patterns have no representation.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct SecurityEvent {
    pub id: i64,
    pub created_at: i64,
    pub request_id: String,
    pub user_id: i64,
    pub username: String,
    pub token_id: i64,
    pub channel_id: i64,
    pub model_name: String,
    pub group: String,
    pub endpoint: String,
    pub decision: String,
    pub rule_id: String,
    pub rule_name: String,
    pub category: String,
    pub layer: String,
    pub severity: String,
    pub source: String,
    pub rule_version: String,
    pub pattern_digest: String,
    pub input_digest: String,
    pub match_count: i64,
}

/// Storage failure exposed through Go's legacy HTTP-200 error envelope.
#[derive(Clone, Debug, Eq, PartialEq, thiserror::Error)]
#[error("{0}")]
pub struct SecurityOverviewError(pub String);

/// Durable reads required by the security overview surface.
#[async_trait]
pub trait SecurityOverviewBackend: Send + Sync {
    async fn policy_snapshot(&self) -> Result<SecurityPolicySnapshot, SecurityOverviewError>;

    async fn stats(
        &self,
        filter: &SecurityEventFilter,
    ) -> Result<SecurityStats, SecurityOverviewError>;

    async fn events(
        &self,
        filter: &SecurityEventFilter,
        limit: i64,
        offset: i64,
    ) -> Result<(Vec<SecurityEvent>, i64), SecurityOverviewError>;
}

#[derive(Clone)]
struct PgSecurityOverviewBackend {
    pool: PgPool,
}

#[async_trait]
impl SecurityOverviewBackend for PgSecurityOverviewBackend {
    async fn policy_snapshot(&self) -> Result<SecurityPolicySnapshot, SecurityOverviewError> {
        const KEYS: [&str; 6] = [
            "AdvancedSecurityEnabled",
            "AdvancedSecurityOnPromptEnabled",
            "AdvancedSecurityAction",
            "AdvancedSecurityRules",
            "grok.violation_deduction_enabled",
            "grok.violation_deduction_amount",
        ];
        let rows = sqlx::query_as::<_, (String, String)>(
            "SELECT key, value FROM options WHERE key = ANY($1)",
        )
        .bind(KEYS.as_slice())
        .fetch_all(&self.pool)
        .await
        .map_err(database_error)?;
        let options = rows.into_iter().collect::<HashMap<_, _>>();
        Ok(policy_snapshot_from_options(&options))
    }

    async fn stats(
        &self,
        filter: &SecurityEventFilter,
    ) -> Result<SecurityStats, SecurityOverviewError> {
        let mut counts_query = filtered_query(
            "SELECT COUNT(*)::BIGINT AS total_matches, \
             COUNT(*) FILTER (WHERE decision = 'blocked')::BIGINT AS blocked_matches, \
             COUNT(*) FILTER (WHERE decision = 'audited')::BIGINT AS audited_matches, \
             COUNT(DISTINCT NULLIF(request_id, ''))::BIGINT AS affected_requests, \
             COUNT(DISTINCT CASE WHEN user_id > 0 THEN user_id END)::BIGINT AS affected_users \
             FROM advanced_security_events",
            filter,
        );
        let row = counts_query
            .build()
            .fetch_one(&self.pool)
            .await
            .map_err(database_error)?;
        let by_category = self.grouped_stats(filter, StatGroup::Category).await?;
        let by_rule = self.grouped_stats(filter, StatGroup::Rule).await?;
        Ok(SecurityStats {
            total_matches: row.try_get("total_matches").map_err(database_error)?,
            blocked_matches: row.try_get("blocked_matches").map_err(database_error)?,
            audited_matches: row.try_get("audited_matches").map_err(database_error)?,
            affected_requests: row.try_get("affected_requests").map_err(database_error)?,
            affected_users: row.try_get("affected_users").map_err(database_error)?,
            by_category,
            by_rule,
        })
    }

    async fn events(
        &self,
        filter: &SecurityEventFilter,
        limit: i64,
        offset: i64,
    ) -> Result<(Vec<SecurityEvent>, i64), SecurityOverviewError> {
        let mut total_query = filtered_query(
            "SELECT COUNT(*)::BIGINT FROM advanced_security_events",
            filter,
        );
        let total = total_query
            .build_query_scalar::<i64>()
            .fetch_one(&self.pool)
            .await
            .map_err(database_error)?;

        let effective_limit = if limit <= 0 { 50 } else { limit.min(200) };
        let effective_offset = offset.max(0);
        let mut events_query = filtered_query(
            "SELECT id::BIGINT AS id, COALESCE(created_at, 0)::BIGINT AS created_at, \
             COALESCE(request_id, '') AS request_id, COALESCE(user_id, 0)::BIGINT AS user_id, \
             COALESCE(username, '') AS username, COALESCE(token_id, 0)::BIGINT AS token_id, \
             COALESCE(channel_id, 0)::BIGINT AS channel_id, \
             COALESCE(model_name, '') AS model_name, COALESCE(\"group\", '') AS \"group\", \
             COALESCE(endpoint, '') AS endpoint, COALESCE(decision, '') AS decision, \
             COALESCE(rule_id, '') AS rule_id, COALESCE(rule_name, '') AS rule_name, \
             COALESCE(category, '') AS category, COALESCE(layer, '') AS layer, \
             COALESCE(severity, '') AS severity, COALESCE(source, '') AS source, \
             COALESCE(rule_version, '') AS rule_version, \
             COALESCE(pattern_digest, '') AS pattern_digest, \
             COALESCE(input_digest, '') AS input_digest, \
             COALESCE(match_count, 0)::BIGINT AS match_count \
             FROM advanced_security_events",
            filter,
        );
        events_query
            .push(" ORDER BY created_at DESC, id DESC LIMIT ")
            .push_bind(effective_limit)
            .push(" OFFSET ")
            .push_bind(effective_offset);
        let events = events_query
            .build()
            .fetch_all(&self.pool)
            .await
            .map_err(database_error)?
            .into_iter()
            .map(event_from_row)
            .collect::<Result<Vec<_>, _>>()?;
        Ok((events, total))
    }
}

impl PgSecurityOverviewBackend {
    async fn grouped_stats(
        &self,
        filter: &SecurityEventFilter,
        group: StatGroup,
    ) -> Result<Vec<SecurityStatBucket>, SecurityOverviewError> {
        let column = match group {
            StatGroup::Category => "category",
            StatGroup::Rule => "rule_id",
        };
        let mut query = QueryBuilder::<Postgres>::new("SELECT ");
        query
            .push(column)
            .push(" AS key, COUNT(*)::BIGINT AS count FROM advanced_security_events");
        push_filters(&mut query, filter);
        query
            .push(" AND ")
            .push(column)
            .push(" <> '' GROUP BY ")
            .push(column)
            .push(" ORDER BY count DESC, key ASC LIMIT 100");
        let rows = query
            .build()
            .fetch_all(&self.pool)
            .await
            .map_err(database_error)?;
        rows.into_iter()
            .map(|row| {
                Ok(SecurityStatBucket {
                    key: row.try_get("key").map_err(database_error)?,
                    count: row.try_get("count").map_err(database_error)?,
                })
            })
            .collect()
    }
}

#[derive(Clone, Copy)]
enum StatGroup {
    Category,
    Rule,
}

fn filtered_query<'a>(
    prefix: &'a str,
    filter: &'a SecurityEventFilter,
) -> QueryBuilder<'a, Postgres> {
    let mut query = QueryBuilder::new(prefix);
    push_filters(&mut query, filter);
    query
}

fn push_filters<'a>(query: &mut QueryBuilder<'a, Postgres>, filter: &'a SecurityEventFilter) {
    query.push(" WHERE TRUE");
    if filter.start_timestamp > 0 {
        query
            .push(" AND created_at >= ")
            .push_bind(filter.start_timestamp);
    }
    if filter.end_timestamp > 0 {
        query
            .push(" AND created_at <= ")
            .push_bind(filter.end_timestamp);
    }
    if filter.user_id > 0 {
        query.push(" AND user_id = ").push_bind(filter.user_id);
    }
    if !filter.rule_id.is_empty() {
        query.push(" AND rule_id = ").push_bind(&filter.rule_id);
    }
    if !filter.category.is_empty() {
        query.push(" AND category = ").push_bind(&filter.category);
    }
    if !filter.decision.is_empty() {
        query.push(" AND decision = ").push_bind(&filter.decision);
    }
    if !filter.model_name.is_empty() {
        query
            .push(" AND model_name = ")
            .push_bind(&filter.model_name);
    }
}

async fn public_policy(State(state): State<SecurityOverviewState>) -> Response {
    match state.backend.policy_snapshot().await {
        Ok(snapshot) => api_success(public_policy_value(&snapshot)),
        Err(error) => api_error(error.to_string()),
    }
}

async fn admin_policy(State(state): State<SecurityOverviewState>, headers: HeaderMap) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    with_auth_version(match state.backend.policy_snapshot().await {
        Ok(snapshot) => api_success(admin_policy_value(&snapshot)),
        Err(error) => api_error(error.to_string()),
    })
}

async fn public_stats(
    State(state): State<SecurityOverviewState>,
    RawQuery(raw_query): RawQuery,
) -> Response {
    let query = parse_query(raw_query.as_deref());
    let (start_timestamp, end_timestamp) = public_stats_bounds(&query, unix_timestamp());
    let filter = SecurityEventFilter {
        start_timestamp,
        end_timestamp,
        ..SecurityEventFilter::default()
    };
    match state.backend.stats(&filter).await {
        Ok(stats) => api_success(stats_value(stats, start_timestamp, end_timestamp, false)),
        Err(error) => api_error(error.to_string()),
    }
}

async fn admin_stats(
    State(state): State<SecurityOverviewState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw_query.as_deref());
    let response = match parse_admin_filter(&query) {
        Ok(filter) => match state.backend.stats(&filter).await {
            Ok(stats) => api_success(stats_value(
                stats,
                filter.start_timestamp,
                filter.end_timestamp,
                true,
            )),
            Err(error) => api_error(error.to_string()),
        },
        Err(message) => api_error(message.to_owned()),
    };
    with_auth_version(response)
}

async fn admin_events(
    State(state): State<SecurityOverviewState>,
    headers: HeaderMap,
    RawQuery(raw_query): RawQuery,
) -> Response {
    if let Err(response) = authenticated_admin(&state, &headers).await {
        return response;
    }
    let query = parse_query(raw_query.as_deref());
    let page = page_query(&query);
    let response = match parse_admin_filter(&query) {
        Ok(filter) => {
            let offset = page.page.wrapping_sub(1).wrapping_mul(page.page_size);
            match state.backend.events(&filter, page.page_size, offset).await {
                Ok((items, total)) => api_success(json!({
                    "page": page.page,
                    "page_size": page.page_size,
                    "total": total,
                    "items": items,
                })),
                Err(error) => api_error(error.to_string()),
            }
        }
        Err(message) => api_error(message.to_owned()),
    };
    with_auth_version(response)
}

async fn authenticated_admin(
    state: &SecurityOverviewState,
    headers: &HeaderMap,
) -> Result<(), Response> {
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
    Ok(())
}

fn policy_snapshot_from_options(options: &HashMap<String, String>) -> SecurityPolicySnapshot {
    let mut snapshot = SecurityPolicySnapshot::default();
    if let Some(value) = options.get("AdvancedSecurityEnabled") {
        snapshot.enabled = value == "true";
    }
    if let Some(value) = options.get("AdvancedSecurityOnPromptEnabled") {
        snapshot.on_prompt = value == "true";
    }
    if let Some(value) = options.get("AdvancedSecurityAction") {
        let normalized = value.trim().to_ascii_lowercase();
        if matches!(normalized.as_str(), "block" | "audit") {
            snapshot.action = normalized;
        }
    }
    if let Some(value) = options.get("AdvancedSecurityRules") {
        snapshot.rules = parse_rules(value);
    }
    if let Some(value) = options.get("grok.violation_deduction_enabled")
        && let Some(enabled) = parse_go_bool(value)
    {
        snapshot.grok_violation_fee_enabled = enabled;
    }
    if let Some(value) = options.get("grok.violation_deduction_amount")
        && let Ok(amount) = value.parse::<f64>()
    {
        snapshot.grok_violation_fee_amount_usd = amount;
    }
    snapshot
}

fn parse_rules(raw: &str) -> Vec<SecurityRule> {
    let Ok(value) = serde_json::from_str::<Value>(raw.trim()) else {
        return Vec::new();
    };
    let rules = if value.is_array() {
        value
    } else if value.get("version").and_then(Value::as_i64).unwrap_or(1) == 1 {
        value.get("rules").cloned().unwrap_or(Value::Null)
    } else {
        Value::Null
    };
    let Ok(raw_rules) = serde_json::from_value::<Vec<RawSecurityRule>>(rules) else {
        return Vec::new();
    };
    raw_rules.into_iter().filter_map(normalize_rule).collect()
}

#[derive(Deserialize)]
struct RawSecurityRule {
    #[serde(default)]
    id: String,
    #[serde(default)]
    name: String,
    #[serde(default)]
    category: String,
    #[serde(default)]
    layer: String,
    #[serde(default)]
    severity: String,
    #[serde(default)]
    source: String,
    #[serde(default)]
    version: String,
    #[serde(default)]
    description: String,
    #[serde(default)]
    enabled: bool,
    #[serde(default)]
    patterns: Vec<String>,
}

fn normalize_rule(raw: RawSecurityRule) -> Option<SecurityRule> {
    let id = raw.id.trim().to_owned();
    if id.is_empty() {
        return None;
    }
    let category = nonempty_or(raw.category.trim().to_ascii_lowercase(), "custom");
    let metadata = risk_category(&category).unwrap_or_else(custom_risk_category);
    let mut patterns = Vec::with_capacity(raw.patterns.len());
    for pattern in raw.patterns {
        let pattern = pattern.trim();
        if !pattern.is_empty()
            && !patterns
                .iter()
                .any(|existing: &String| existing.eq_ignore_ascii_case(pattern))
        {
            patterns.push(pattern.to_owned());
        }
    }
    Some(SecurityRule {
        name: nonempty_or(raw.name.trim().to_owned(), &id),
        id,
        category,
        layer: nonempty_or(raw.layer.trim().to_ascii_lowercase(), metadata.layer),
        severity: nonempty_or(raw.severity.trim().to_ascii_lowercase(), metadata.severity),
        source: nonempty_or(raw.source.trim().to_owned(), metadata.source),
        version: nonempty_or(raw.version.trim().to_owned(), "v1"),
        description: nonempty_or(raw.description.trim().to_owned(), metadata.description),
        enabled: raw.enabled,
        patterns,
    })
}

fn nonempty_or(value: String, fallback: &str) -> String {
    if value.is_empty() {
        fallback.to_owned()
    } else {
        value
    }
}

#[derive(Clone, Copy, Serialize)]
struct RiskCategory {
    id: &'static str,
    name: &'static str,
    layer: &'static str,
    severity: &'static str,
    description: &'static str,
    source: &'static str,
}

const RISK_CATEGORIES: [RiskCategory; 26] = [
    RiskCategory {
        id: "applicable_laws_illegal_activity",
        name: "Applicable laws and illegal activity",
        layer: "universal_standard",
        severity: "high",
        description: "Illegal activity, controlled goods, or infringement of third-party rights.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "critical_infrastructure",
        name: "Critical infrastructure",
        layer: "universal_standard",
        severity: "critical",
        description: "Unauthorized access to or disruption of critical systems and services.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "computer_network_compromise",
        name: "Computer and network compromise",
        layer: "universal_standard",
        severity: "high",
        description: "Unauthorized intrusion, malware, destructive cyber activity, or guardrail bypass.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "weapons",
        name: "Weapons and dangerous materials",
        layer: "universal_standard",
        severity: "critical",
        description: "Design, acquisition, or weaponization of harmful weapons or dangerous materials.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "violence_hate",
        name: "Violence and hateful behavior",
        layer: "universal_standard",
        severity: "high",
        description: "Violence, violent extremism, terrorism, intimidation, or hateful behavior.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "privacy_identity",
        name: "Privacy and identity rights",
        layer: "universal_standard",
        severity: "high",
        description: "Unauthorized use of private data, identity misuse, impersonation, or biometric inference.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "child_safety",
        name: "Children's safety",
        layer: "universal_standard",
        severity: "critical",
        description: "Child sexual exploitation, grooming, sextortion, or other abuse of minors.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "psychological_emotional_harm",
        name: "Psychological and emotional harm",
        layer: "universal_standard",
        severity: "high",
        description: "Self-harm, harassment, bullying, emotional abuse, or graphic and gratuitous harm.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "misinformation",
        name: "Misinformation",
        layer: "universal_standard",
        severity: "high",
        description: "Deceptive or misleading information, impersonation, or targeted conspiratorial narratives.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "democratic_processes_targeted_campaigns",
        name: "Democratic processes and targeted campaigns",
        layer: "universal_standard",
        severity: "high",
        description: "Deceptive political influence, vote suppression, or disruption of civic processes.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "criminal_justice_censorship_surveillance",
        name: "Criminal justice, censorship, and surveillance",
        layer: "universal_standard",
        severity: "critical",
        description: "Prohibited high-impact law-enforcement, censorship, surveillance, or biometric uses.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "fraudulent_abusive_predatory",
        name: "Fraudulent, abusive, and predatory practices",
        layer: "universal_standard",
        severity: "high",
        description: "Fraud, scams, spam, predatory practices, deceptive products, or exploitative conduct.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "platform_abuse",
        name: "Platform abuse",
        layer: "universal_standard",
        severity: "high",
        description: "Multi-account evasion, spam automation, ban circumvention, scraping, or jailbreak abuse.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "sexually_explicit_content",
        name: "Sexually explicit content",
        layer: "universal_standard",
        severity: "high",
        description: "Explicit sexual acts, erotic chats, sexual fetishes, incest, or bestiality.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_legal",
        name: "High-risk: legal",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Legal interpretation, guidance, or decisions with legal implications.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_healthcare",
        name: "High-risk: healthcare",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Healthcare decisions, diagnosis, patient care, therapy, or medical guidance.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_insurance",
        name: "High-risk: insurance",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Insurance underwriting, claims processing, or coverage decisions.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_finance",
        name: "High-risk: finance",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Investment advice, loan approval, or financial eligibility and credit decisions.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_employment_housing",
        name: "High-risk: employment and housing",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Hiring, resume screening, employability, housing eligibility, leases, or home loans.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_academic_testing_admissions",
        name: "High-risk: academic testing and admissions",
        layer: "high_risk_use_case",
        severity: "high",
        description: "Admissions, standardized testing, certification, or educational institution evaluation.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "high_risk_media_journalism",
        name: "High-risk: media and journalism",
        layer: "high_risk_use_case",
        severity: "medium",
        description: "Automatically generated media or professional journalistic content for external publication.",
        source: "anthropic_usage_policy",
    },
    RiskCategory {
        id: "chatbot_disclosure",
        name: "Chatbot disclosure",
        layer: "additional_guideline",
        severity: "medium",
        description: "Consumer-facing chatbots and interactive agents must clearly disclose that users are interacting with AI.",
        source: "anthropic_additional_guidelines",
    },
    RiskCategory {
        id: "minors_safety",
        name: "Products serving minors",
        layer: "additional_guideline",
        severity: "high",
        description: "Products serving minors require additional age-appropriate safety and privacy controls.",
        source: "anthropic_additional_guidelines",
    },
    RiskCategory {
        id: "agentic_use",
        name: "Agentic use",
        layer: "additional_guideline",
        severity: "high",
        description: "Agentic systems remain subject to the policy and need controls around delegated actions and tools.",
        source: "anthropic_additional_guidelines",
    },
    RiskCategory {
        id: "mcp_server",
        name: "Model Context Protocol servers",
        layer: "additional_guideline",
        severity: "high",
        description: "MCP servers and connectors need controls appropriate to their tools, data, and distribution context.",
        source: "anthropic_additional_guidelines",
    },
    RiskCategory {
        id: "custom",
        name: "Custom operator rule",
        layer: "custom",
        severity: "medium",
        description: "An operator-defined rule outside the standard public taxonomy.",
        source: "local_custom",
    },
];

fn risk_category(id: &str) -> Option<&'static RiskCategory> {
    RISK_CATEGORIES.iter().find(|category| category.id == id)
}

fn custom_risk_category() -> &'static RiskCategory {
    &RISK_CATEGORIES[RISK_CATEGORIES.len() - 1]
}

fn parse_go_bool(value: &str) -> Option<bool> {
    match value {
        "1" | "t" | "T" | "true" | "TRUE" | "True" => Some(true),
        "0" | "f" | "F" | "false" | "FALSE" | "False" => Some(false),
        _ => None,
    }
}

#[derive(Serialize)]
struct PublicRule<'a> {
    id: &'a str,
    name: &'a str,
    category: &'a str,
    layer: &'a str,
    severity: &'a str,
    source: &'a str,
    version: &'a str,
    description: &'a str,
}

fn public_rule(rule: &SecurityRule) -> PublicRule<'_> {
    PublicRule {
        id: &rule.id,
        name: &rule.name,
        category: &rule.category,
        layer: &rule.layer,
        severity: &rule.severity,
        source: &rule.source,
        version: &rule.version,
        description: &rule.description,
    }
}

fn public_policy_value(snapshot: &SecurityPolicySnapshot) -> Value {
    let rules = snapshot
        .rules
        .iter()
        .filter(|rule| rule.enabled)
        .map(public_rule)
        .collect::<Vec<_>>();
    json!({
        "policy_version": "anthropic-aligned-v1",
        "reference_effective_date": "2025-09-15",
        "reference_url": "https://www.anthropic.com/legal/aup",
        "alignment": "Anthropic public Usage Policy risk areas, adapted for this relay; not an official equivalent",
        "risk_categories": RISK_CATEGORIES,
        "rules": rules,
        "violation_fees": [{
            "code": "violation_fee.grok.csam",
            "provider": "Grok / xAI upstream",
            "trigger": "The upstream provider returns a content-safety violation marker.",
            "enabled": snapshot.grok_violation_fee_enabled,
            "amount_usd": snapshot.grok_violation_fee_amount_usd,
            "charge_unit": "per request",
            "retryable": false,
            "description": "An additional fee may be charged when the upstream provider classifies a request as a usage-policy violation.",
            "charging_notes": "amount_usd is the base amount before the request's group ratio; it is converted to quota and charged after the normal flow (including refund). It applies only when enabled and the upstream violation marker is present. Local advanced-security block/audit matches do not incur this fee.",
            "local_guardrail_fee": false,
        }],
    })
}

fn admin_policy_value(snapshot: &SecurityPolicySnapshot) -> Value {
    json!({
        "public": public_policy_value(snapshot),
        "settings": {
            "enabled": snapshot.enabled,
            "on_prompt": snapshot.on_prompt,
            "action": snapshot.action,
        },
        "rules": snapshot.rules,
    })
}

fn stats_value(
    stats: SecurityStats,
    start_timestamp: i64,
    end_timestamp: i64,
    include_rules: bool,
) -> Value {
    let mut value = json!({
        "start_timestamp": start_timestamp,
        "end_timestamp": end_timestamp,
        "total_matches": stats.total_matches,
        "blocked_matches": stats.blocked_matches,
        "audited_matches": stats.audited_matches,
        "affected_requests": stats.affected_requests,
        "affected_users": stats.affected_users,
        "by_category": stats.by_category,
    });
    if include_rules {
        value["by_rule"] = json!(stats.by_rule);
    }
    value
}

fn parse_query(raw: Option<&str>) -> HashMap<String, String> {
    let mut values = HashMap::new();
    for (key, value) in form_urlencoded::parse(raw.unwrap_or_default().as_bytes()) {
        values
            .entry(key.into_owned())
            .or_insert_with(|| value.into_owned());
    }
    values
}

fn public_stats_bounds(query: &HashMap<String, String>, now: i64) -> (i64, i64) {
    let default_start = now.saturating_sub(PUBLIC_STATS_WINDOW_SECONDS);
    let mut start = query
        .get("start_timestamp")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(default_start);
    let end = query
        .get("end_timestamp")
        .and_then(|value| value.parse::<i64>().ok())
        .filter(|value| *value > 0)
        .unwrap_or(now);
    if end < start {
        return (default_start, now);
    }
    start = start.max(end.saturating_sub(PUBLIC_STATS_MAX_WINDOW_SECONDS));
    (start, end)
}

fn parse_admin_filter(
    query: &HashMap<String, String>,
) -> Result<SecurityEventFilter, &'static str> {
    let start_timestamp = parse_nonnegative(query, "start_timestamp", "invalid start_timestamp")?;
    let end_timestamp = parse_nonnegative(query, "end_timestamp", "invalid end_timestamp")?;
    if start_timestamp > 0 && end_timestamp > 0 && end_timestamp < start_timestamp {
        return Err("end_timestamp must be greater than or equal to start_timestamp");
    }
    let user_id = match trimmed(query, "user_id") {
        "" => 0,
        value => value
            .parse::<i64>()
            .ok()
            .filter(|id| *id > 0)
            .ok_or("invalid user_id")?,
    };
    let decision = trimmed(query, "decision").to_ascii_lowercase();
    if !matches!(decision.as_str(), "" | "blocked" | "audited") {
        return Err("decision must be blocked or audited");
    }
    Ok(SecurityEventFilter {
        start_timestamp,
        end_timestamp,
        user_id,
        rule_id: trimmed(query, "rule_id").to_owned(),
        category: trimmed(query, "category").to_ascii_lowercase(),
        decision,
        model_name: trimmed(query, "model_name").to_owned(),
    })
}

fn parse_nonnegative(
    query: &HashMap<String, String>,
    key: &str,
    error: &'static str,
) -> Result<i64, &'static str> {
    let value = trimmed(query, key);
    if value.is_empty() {
        return Ok(0);
    }
    value
        .parse::<i64>()
        .ok()
        .filter(|value| *value >= 0)
        .ok_or(error)
}

fn trimmed<'a>(query: &'a HashMap<String, String>, key: &str) -> &'a str {
    query.get(key).map_or("", |value| value.trim())
}

#[derive(Clone, Copy)]
struct PageQuery {
    page: i64,
    page_size: i64,
}

fn page_query(query: &HashMap<String, String>) -> PageQuery {
    let parsed_page = query
        .get("p")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or(0);
    let page = if parsed_page == 0 { 1 } else { parsed_page };
    let mut page_size = query
        .get("page_size")
        .and_then(|value| value.parse::<i64>().ok())
        .unwrap_or(0);
    if page_size == 0 {
        page_size = query
            .get("ps")
            .and_then(|value| value.parse::<i64>().ok())
            .filter(|value| *value != 0)
            .or_else(|| {
                query
                    .get("size")
                    .and_then(|value| value.parse::<i64>().ok())
                    .filter(|value| *value != 0)
            })
            .unwrap_or(DEFAULT_PAGE_SIZE);
    }
    PageQuery {
        page,
        page_size: page_size.min(MAX_PAGE_SIZE),
    }
}

fn unix_timestamp() -> i64 {
    SystemTime::now()
        .duration_since(UNIX_EPOCH)
        .map_or(0, |duration| {
            i64::try_from(duration.as_secs()).unwrap_or(i64::MAX)
        })
}

fn api_success(data: Value) -> Response {
    legacy_json(
        StatusCode::OK,
        json!({"success": true, "message": "", "data": data}),
    )
}

fn api_error(message: String) -> Response {
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

fn with_auth_version(mut response: Response) -> Response {
    response.headers_mut().insert(
        HeaderName::from_static("auth-version"),
        HeaderValue::from_static(AUTH_VERSION),
    );
    response
}

fn database_error(error: impl std::fmt::Display) -> SecurityOverviewError {
    SecurityOverviewError(error.to_string())
}

fn event_from_row(row: PgRow) -> Result<SecurityEvent, SecurityOverviewError> {
    Ok(SecurityEvent {
        id: row.try_get("id").map_err(database_error)?,
        created_at: row.try_get("created_at").map_err(database_error)?,
        request_id: row.try_get("request_id").map_err(database_error)?,
        user_id: row.try_get("user_id").map_err(database_error)?,
        username: row.try_get("username").map_err(database_error)?,
        token_id: row.try_get("token_id").map_err(database_error)?,
        channel_id: row.try_get("channel_id").map_err(database_error)?,
        model_name: row.try_get("model_name").map_err(database_error)?,
        group: row.try_get("group").map_err(database_error)?,
        endpoint: row.try_get("endpoint").map_err(database_error)?,
        decision: row.try_get("decision").map_err(database_error)?,
        rule_id: row.try_get("rule_id").map_err(database_error)?,
        rule_name: row.try_get("rule_name").map_err(database_error)?,
        category: row.try_get("category").map_err(database_error)?,
        layer: row.try_get("layer").map_err(database_error)?,
        severity: row.try_get("severity").map_err(database_error)?,
        source: row.try_get("source").map_err(database_error)?,
        rule_version: row.try_get("rule_version").map_err(database_error)?,
        pattern_digest: row.try_get("pattern_digest").map_err(database_error)?,
        input_digest: row.try_get("input_digest").map_err(database_error)?,
        match_count: row.try_get("match_count").map_err(database_error)?,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn public_bounds_match_legacy_fallback_and_cap() {
        let mut query = HashMap::new();
        query.insert("start_timestamp".to_owned(), "1".to_owned());
        query.insert("end_timestamp".to_owned(), "10000000".to_owned());
        assert_eq!(
            public_stats_bounds(&query, 20_000_000),
            (10_000_000 - PUBLIC_STATS_MAX_WINDOW_SECONDS, 10_000_000)
        );

        query.insert("start_timestamp".to_owned(), "200".to_owned());
        query.insert("end_timestamp".to_owned(), "100".to_owned());
        assert_eq!(
            public_stats_bounds(&query, 1_000),
            (1_000 - PUBLIC_STATS_WINDOW_SECONDS, 1_000)
        );
    }

    #[test]
    fn admin_filter_error_order_matches_go() {
        let query = parse_query(Some(
            "start_timestamp=nope&end_timestamp=-1&user_id=0&decision=allow",
        ));
        assert_eq!(parse_admin_filter(&query), Err("invalid start_timestamp"));
    }

    #[test]
    fn page_aliases_and_negative_compatibility_match_go() {
        let query = parse_query(Some("p=-2&page_size=0&ps=-7&size=20"));
        let page = page_query(&query);
        assert_eq!(page.page, -2);
        assert_eq!(page.page_size, -7);
    }

    #[test]
    fn rules_are_normalized_without_exposing_disabled_public_rules() {
        let rules = parse_rules(
            r#"[{"id":" child ","name":"","category":"CHILD_SAFETY","enabled":true,"patterns":[" minor ","MINOR"]},{"id":"off","enabled":false,"patterns":["secret"]}]"#,
        );
        assert_eq!(rules[0].name, "child");
        assert_eq!(rules[0].severity, "critical");
        assert_eq!(rules[0].patterns, ["minor"]);
        let snapshot = SecurityPolicySnapshot {
            rules,
            ..SecurityPolicySnapshot::default()
        };
        let public = public_policy_value(&snapshot);
        assert_eq!(public["rules"].as_array().map(Vec::len), Some(1));
        assert!(public["rules"][0].get("patterns").is_none());
    }
}
