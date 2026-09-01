use std::{collections::BTreeMap, time::Duration};

use base64::{Engine as _, engine::general_purpose::URL_SAFE_NO_PAD};
use hmac::{Hmac, Mac};
use rand::{Rng, RngCore, distr::Alphanumeric, rng};
use secrecy::{ExposeSecret, SecretString};
use serde::Deserialize;
use serde_json::json;
use sha2::Sha256;
use sqlx::{Postgres, Row, Transaction};

use super::super::{ASSISTANT_KEY_NAME_MAX_CHARS, PgAssistantReadStore, unix_seconds};
use super::domain::*;
use crate::auth::{
    DashboardDeveloperAccessUserFacts, dashboard_developer_access_granted,
    dashboard_self_user_facts_in_transaction,
};
use crate::routes::assistant::secure_card::encrypt_payload;
use crate::routes::identity_2fa::{CriticalTwoFactorVerification, verify_critical_mutation_factor};
use crate::routes::identity_catalog::{user_group_selection, user_group_selection_in_transaction};

const FLOW_PURPOSE: &str = "assistant_key";
const CONFIRMATION_TTL: Duration = Duration::from_secs(10 * 60);
const SECURE_CARD_TTL_SECONDS: i64 = 10 * 60;
const DEFAULT_MAX_USER_TOKENS: i64 = 100;
pub(super) const ZERO_RATIO_WARNING_MESSAGE: &str = "This routing group is community-operated. Availability, model coverage, privacy handling, and billing behavior may be less predictable. Do not send secrets or sensitive data. Continue only if you accept these risks.";

type HmacSha256 = Hmac<Sha256>;

pub(in crate::routes::assistant) async fn load_pg_options(
    store: &PgAssistantReadStore,
    user_group: &str,
) -> Result<Vec<AssistantKeyGroupOption>, String> {
    let selection = user_group_selection(&store.pg, user_group).await?;
    let mut options = selectable_group_options(selection);
    let warnings = warning_snapshot_pool(store).await?;
    for option in &mut options {
        option.warning = warning_for_group(&warnings, &option.id);
    }
    Ok(options)
}

pub(in crate::routes::assistant) async fn prepare_pg(
    store: &PgAssistantReadStore,
    user_id: i64,
    session_id: &str,
    draft: PreparedKeyDraft,
) -> Result<PreparedKeyAction, KeyCreationError> {
    if session_id.trim().is_empty() {
        return Err(KeyCreationError::InvalidConfirmation);
    }
    let token = random_opaque_token();
    let token_hash = auth_flow_hash(&store.session_secret, &token)?;
    let payload = serde_json::to_string(&draft)
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    sqlx::query(
        "INSERT INTO auth_flows (token_hash, purpose, user_id, session_id, payload, created_at, expires_at) \
         VALUES ($1, $2, $3, $4, $5, NOW(), NOW() + INTERVAL '10 minutes')",
    )
    .bind(token_hash)
    .bind(FLOW_PURPOSE)
    .bind(user_id)
    .bind(session_id)
    .bind(payload)
    .execute(&store.pg)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    Ok(PreparedKeyAction {
        kind: "create_key",
        confirmation_token: token,
        requires_confirmation: true,
        expires_in_seconds: CONFIRMATION_TTL.as_secs(),
        name: draft.name,
        group: draft.group.into_inner(),
        conversation_id: draft.conversation_id,
        ui_path: "/keys",
    })
}

pub(in crate::routes::assistant) async fn confirm_pg(
    store: &PgAssistantReadStore,
    authorization_fence: AuthorizationFence,
    token: ConfirmationToken,
    two_factor_code: &str,
) -> Result<CreatedKey, KeyCreationError> {
    let token_hash = auth_flow_hash(&store.session_secret, token.expose())?;
    let has_auto_groups_column = sqlx::query_scalar::<_, bool>(
        "SELECT EXISTS (SELECT 1 FROM information_schema.columns WHERE table_schema = current_schema() AND table_name = 'tokens' AND column_name = 'auto_groups')",
    )
    .fetch_one(&store.pg)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    let mut transaction = store
        .pg
        .begin()
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    let flow = sqlx::query(
        "SELECT id::BIGINT AS id, payload FROM auth_flows \
         WHERE token_hash = $1 AND purpose = $2 AND user_id = $3 AND session_id = $4 \
           AND consumed_at IS NULL AND expires_at > NOW() FOR UPDATE",
    )
    .bind(token_hash)
    .bind(FLOW_PURPOSE)
    .bind(authorization_fence.actor_id())
    .bind(authorization_fence.session_id())
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    .ok_or(KeyCreationError::InvalidConfirmation)?;
    let flow_id: i64 = flow
        .try_get("id")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let payload: String = flow
        .try_get("payload")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let draft: PreparedKeyDraft =
        serde_json::from_str(&payload).map_err(|_| KeyCreationError::InvalidConfirmation)?;
    if draft.version != DRAFT_VERSION
        || draft.name.trim().is_empty()
        || draft.name.chars().count() > ASSISTANT_KEY_NAME_MAX_CHARS
    {
        return Err(KeyCreationError::InvalidConfirmation);
    }

    // The flow row is already locked. All authority rows follow one global
    // order: user -> session -> factor/backup -> paid policy facts -> options.
    let user = sqlx::query(
        "SELECT username, role::BIGINT AS role, status::BIGINT AS status, \
         COALESCE(auth_version, 0)::BIGINT AS auth_version, COALESCE(\"group\", '') AS user_group, \
         CASE WHEN to_jsonb(users) ? 'trust_level_override' AND NULLIF(to_jsonb(users)->>'trust_level_override', '') IS NOT NULL \
              THEN (to_jsonb(users)->>'trust_level_override')::BIGINT ELSE NULL END AS trust_level_override, \
         COALESCE(created_at, 0)::BIGINT AS created_at, \
         CASE WHEN to_jsonb(users) ? 'last_api_activity_at' THEN COALESCE(NULLIF(to_jsonb(users)->>'last_api_activity_at', '')::BIGINT, 0) ELSE 0 END AS last_api_activity_at \
         FROM users WHERE id = $1 AND deleted_at IS NULL FOR UPDATE",
    )
    .bind(authorization_fence.actor_id())
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    .ok_or(KeyCreationError::InvalidConfirmation)?;
    let status: i64 = user
        .try_get("status")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let current_auth_version: i64 = user
        .try_get("auth_version")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    if status != 1 || current_auth_version != authorization_fence.expected_user_auth_version() {
        return Err(KeyCreationError::InvalidConfirmation);
    }

    let session = sqlx::query(
        "SELECT status, COALESCE(revoked_at, 0)::BIGINT AS revoked_at, expires_at::BIGINT AS expires_at, \
         version::BIGINT AS version, user_auth_version::BIGINT AS user_auth_version \
         FROM user_sessions WHERE sid = $1 AND user_id = $2 FOR UPDATE",
    )
    .bind(authorization_fence.session_id())
    .bind(authorization_fence.actor_id())
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    .ok_or(KeyCreationError::InvalidConfirmation)?;
    let session_status: String = session
        .try_get("status")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let revoked_at: i64 = session
        .try_get("revoked_at")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let expires_at: i64 = session
        .try_get("expires_at")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let session_version: i64 = session
        .try_get("version")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let session_auth_version: i64 = session
        .try_get("user_auth_version")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    if session_status != "active"
        || revoked_at != 0
        || expires_at <= unix_seconds()
        || session_version != authorization_fence.expected_session_version()
        || session_auth_version != authorization_fence.expected_user_auth_version()
    {
        return Err(KeyCreationError::InvalidConfirmation);
    }

    match verify_critical_mutation_factor(
        &mut transaction,
        authorization_fence.actor_id(),
        two_factor_code,
    )
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    {
        CriticalTwoFactorVerification::NotRequired | CriticalTwoFactorVerification::Verified => {}
        CriticalTwoFactorVerification::Rejected => {
            transaction
                .commit()
                .await
                .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
            return Err(KeyCreationError::TwoFactorInvalid);
        }
    }

    let developer_facts = dashboard_self_user_facts_in_transaction(
        &mut transaction,
        DashboardDeveloperAccessUserFacts {
            user_id: authorization_fence.actor_id(),
            role: user
                .try_get("role")
                .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?,
            trust_level_override: user
                .try_get("trust_level_override")
                .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?,
            created_at: user
                .try_get("created_at")
                .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?,
            last_api_activity_at: user
                .try_get("last_api_activity_at")
                .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?,
        },
        authorization_fence.developer_access_policy(),
    )
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    let role = user
        .try_get("role")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    if !dashboard_developer_access_granted(role, developer_facts) {
        return Err(KeyCreationError::InvalidConfirmation);
    }
    let current_group: String = user
        .try_get("user_group")
        .map_err(|error: sqlx::Error| KeyCreationError::Unavailable(error.to_string()))?;
    let selection = user_group_selection_in_transaction(&mut transaction, &current_group)
        .await
        .map_err(KeyCreationError::Unavailable)?;
    if !selection.selectable.contains_key(draft.group.as_str()) {
        return Err(KeyCreationError::InvalidGroup);
    }
    let warnings = warning_snapshot_transaction(&mut transaction).await?;
    if warning_for_group(&warnings, draft.group.as_str()) != draft.warning {
        return Err(KeyCreationError::WarningChanged);
    }

    let max_tokens = sqlx::query_scalar::<_, String>(
        "SELECT value FROM options WHERE key = 'token_setting.max_user_tokens' LIMIT 1",
    )
    .fetch_optional(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    .map_or(DEFAULT_MAX_USER_TOKENS, |value| {
        parse_max_user_tokens(&value, DEFAULT_MAX_USER_TOKENS)
    });
    let token_count = sqlx::query_scalar::<_, i64>(
        "SELECT COUNT(*) FROM tokens WHERE user_id = $1 AND deleted_at IS NULL",
    )
    .bind(authorization_fence.actor_id())
    .fetch_one(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    if token_count >= max_tokens {
        return Err(KeyCreationError::TokenLimit(max_tokens));
    }

    let raw_key = generate_assistant_key();
    let now = unix_seconds();
    let id = if has_auto_groups_column {
        sqlx::query_scalar::<_, i64>(
            "INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry, auto_groups) \
             VALUES ($1, $2, 1, $3, $4, $4, -1, 0, TRUE, FALSE, '', '', 0, $5, FALSE, '') RETURNING id::BIGINT",
        )
        .bind(authorization_fence.actor_id())
        .bind(&raw_key)
        .bind(&draft.name)
        .bind(now)
        .bind(draft.group.as_str())
        .fetch_one(&mut *transaction)
        .await
    } else {
        sqlx::query_scalar::<_, i64>(
            "INSERT INTO tokens (user_id, key, status, name, created_time, accessed_time, expired_time, remain_quota, unlimited_quota, model_limits_enabled, model_limits, allow_ips, used_quota, \"group\", cross_group_retry) \
             VALUES ($1, $2, 1, $3, $4, $4, -1, 0, TRUE, FALSE, '', '', 0, $5, FALSE) RETURNING id::BIGINT",
        )
        .bind(authorization_fence.actor_id())
        .bind(&raw_key)
        .bind(&draft.name)
        .bind(now)
        .bind(draft.group.as_str())
        .fetch_one(&mut *transaction)
        .await
    }
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;

    let card = insert_secure_card(
        &mut transaction,
        &store.session_secret,
        authorization_fence.actor_id(),
        draft.conversation_id,
        &raw_key,
        now,
    )
    .await?;
    let consumed = sqlx::query(
        "UPDATE auth_flows SET consumed_at = NOW() WHERE id = $1 AND consumed_at IS NULL",
    )
    .bind(flow_id)
    .execute(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    if consumed.rows_affected() != 1 {
        return Err(KeyCreationError::InvalidConfirmation);
    }
    sqlx::query(
        "UPDATE users SET console_activated_at = $2 \
         WHERE id = $1 AND deleted_at IS NULL AND console_activated_at = 0",
    )
    .bind(authorization_fence.actor_id())
    .bind(now)
    .execute(&mut *transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    transaction
        .commit()
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;

    store.evict_user_cache(authorization_fence.actor_id()).await;
    store
        .record_system_log(
            authorization_fence.actor_id(),
            authorization_fence.actor_username(),
            format!("created API key {id} via assistant"),
        )
        .await;
    Ok(CreatedKey {
        id,
        name: draft.name,
        group: draft.group.into_inner(),
        expired_time: -1,
        card,
        privacy_notice: "Only deliberately saved assistant history is retained. API keys and provider secrets are never written to ordinary chat history; secure cards expire and can be revealed once by their owner.",
    })
}

async fn insert_secure_card(
    transaction: &mut Transaction<'_, Postgres>,
    session_secret: &SecretString,
    user_id: i64,
    conversation_id: i64,
    raw_key: &str,
    now: i64,
) -> Result<SecureCardView, KeyCreationError> {
    let summary = "已创建 API 凭证；仅你可一次性查看和复制".to_owned();
    let payload = json!({"api_key": format!("sk-{raw_key}")}).to_string();
    let ciphertext = encrypt_payload(session_secret, payload.as_bytes())
        .map_err(|()| KeyCreationError::Unavailable("secure card encryption failed".to_owned()))?;
    let mut message_id = 0_i64;
    if conversation_id > 0 {
        sqlx::query(
            "SELECT id FROM assistant_conversations \
             WHERE id = $1 AND user_id = $2 AND archived_at = 0 FOR UPDATE",
        )
        .bind(conversation_id)
        .bind(user_id)
        .fetch_optional(&mut **transaction)
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
        .ok_or(KeyCreationError::InvalidConfirmation)?;
        let sequence = sqlx::query_scalar::<_, i64>(
            "SELECT COALESCE(MAX(sequence), 0) + 1 FROM assistant_history_messages \
             WHERE conversation_id = $1",
        )
        .bind(conversation_id)
        .fetch_one(&mut **transaction)
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
        message_id = sqlx::query_scalar::<_, i64>(
            "INSERT INTO assistant_history_messages (conversation_id, sequence, role, content, created_at) \
             VALUES ($1, $2, 'card', $3, $4) RETURNING id::BIGINT",
        )
        .bind(conversation_id)
        .bind(sequence)
        .bind(&summary)
        .bind(now)
        .fetch_one(&mut **transaction)
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
        sqlx::query(
            "UPDATE assistant_conversations SET last_message_preview = $2, updated_at = $3 WHERE id = $1",
        )
        .bind(conversation_id)
        .bind(&summary)
        .bind(now)
        .execute(&mut **transaction)
        .await
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    }
    let id = random_card_id();
    let expires_at = now + SECURE_CARD_TTL_SECONDS;
    sqlx::query(
        "INSERT INTO assistant_secure_cards \
         (id, owner_user_id, conversation_id, message_id, type, summary, ciphertext, created_at, expires_at, revealed_at) \
         VALUES ($1, $2, $3, $4, 'api_key', $5, $6, $7, $8, 0)",
    )
    .bind(&id)
    .bind(user_id)
    .bind(conversation_id)
    .bind(message_id)
    .bind(&summary)
    .bind(ciphertext)
    .bind(now)
    .bind(expires_at)
    .execute(&mut **transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    Ok(SecureCardView {
        id,
        kind: "api_key",
        summary,
        created_at: now,
        expires_at,
        revealable: true,
    })
}

#[derive(Default, Deserialize)]
struct WarningSetting {
    #[serde(default)]
    group_warnings: BTreeMap<String, PreparedKeyWarning>,
}

#[derive(Default)]
struct GroupWarningSnapshot {
    configured: BTreeMap<String, PreparedKeyWarning>,
    ratios: BTreeMap<String, f64>,
}

async fn warning_snapshot_pool(
    store: &PgAssistantReadStore,
) -> Result<GroupWarningSnapshot, String> {
    let rows =
        sqlx::query_as::<_, (String, String)>("SELECT key, value FROM options WHERE key = ANY($1)")
            .bind(vec![
                "GroupRatio",
                "group_ratio_setting.group_warnings",
                "group_ratio_setting",
            ])
            .fetch_all(&store.pg)
            .await
            .map_err(|error| error.to_string())?;
    warning_snapshot(rows.into_iter().collect()).map_err(|error| error.to_string())
}

async fn warning_snapshot_transaction(
    transaction: &mut Transaction<'_, Postgres>,
) -> Result<GroupWarningSnapshot, KeyCreationError> {
    let rows = sqlx::query_as::<_, (String, String)>(
        "SELECT key, value FROM options WHERE key = ANY($1) FOR SHARE",
    )
    .bind(vec![
        "GroupRatio",
        "group_ratio_setting.group_warnings",
        "group_ratio_setting",
    ])
    .fetch_all(&mut **transaction)
    .await
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    warning_snapshot(rows.into_iter().collect())
}

fn warning_snapshot(
    values: BTreeMap<String, String>,
) -> Result<GroupWarningSnapshot, KeyCreationError> {
    let ratios = serde_json::from_str::<BTreeMap<String, f64>>(
        values
            .get("GroupRatio")
            .ok_or_else(|| KeyCreationError::Unavailable("GroupRatio is missing".to_owned()))?,
    )
    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    if ratios
        .values()
        .any(|ratio| !ratio.is_finite() || *ratio < 0.0)
    {
        return Err(KeyCreationError::Unavailable(
            "GroupRatio contains an invalid ratio".to_owned(),
        ));
    }
    let configured = if let Some(raw) = values.get("group_ratio_setting.group_warnings") {
        serde_json::from_str(raw)
            .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?
    } else {
        values
            .get("group_ratio_setting")
            .map_or(Ok(BTreeMap::new()), |raw| {
                serde_json::from_str::<WarningSetting>(raw)
                    .map(|setting| setting.group_warnings)
                    .map_err(|error| KeyCreationError::Unavailable(error.to_string()))
            })?
    };
    Ok(GroupWarningSnapshot { configured, ratios })
}

fn warning_for_group(snapshot: &GroupWarningSnapshot, group: &str) -> Option<PreparedKeyWarning> {
    if let Some((_, warning)) = snapshot
        .configured
        .iter()
        .find(|(configured, _)| configured.trim().eq_ignore_ascii_case(group))
    {
        return warning.enabled.then(|| warning.clone());
    }
    snapshot
        .ratios
        .get(group)
        .is_some_and(|ratio| *ratio == 0.0)
        .then(|| PreparedKeyWarning {
            enabled: true,
            message: ZERO_RATIO_WARNING_MESSAGE.to_owned(),
            mode: "modal".to_owned(),
            confirmations: 3,
        })
}

fn random_opaque_token() -> String {
    let mut bytes = [0_u8; 32];
    rng().fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}

fn random_card_id() -> String {
    let mut bytes = [0_u8; 24];
    rng().fill_bytes(&mut bytes);
    URL_SAFE_NO_PAD.encode(bytes)
}

fn auth_flow_hash(session_secret: &SecretString, token: &str) -> Result<String, KeyCreationError> {
    let key = format!("auth-flow-v1:{}", session_secret.expose_secret());
    let mut mac = <HmacSha256 as Mac>::new_from_slice(key.as_bytes())
        .map_err(|error| KeyCreationError::Unavailable(error.to_string()))?;
    mac.update(token.as_bytes());
    Ok(hex::encode(mac.finalize().into_bytes()))
}

fn generate_assistant_key() -> String {
    rng()
        .sample_iter(Alphanumeric)
        .take(48)
        .map(char::from)
        .collect()
}

fn parse_max_user_tokens(value: &str, fallback: i64) -> i64 {
    value
        .trim()
        .parse::<i64>()
        .ok()
        .filter(|value| *value > 0)
        .unwrap_or(fallback)
}
