//! Private, bounded registration evidence. Similarity is never an identity or
//! spending grant. Only account-creation code may supply a server-verified source.
use super::{AbuseError, AbuseStore, Result};
use axum::{
    Json,
    extract::{Path, Query, State},
    http::HeaderMap,
};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};
use sqlx::{PgConnection, Row};
use std::{collections::BTreeSet, net::IpAddr};

#[derive(Clone, Copy)]
pub enum RegistrationSourceKind {
    Socket,
    TrustedXForwardedFor,
}
#[derive(Clone, Copy)]
pub struct RegistrationSource {
    pub address: IpAddr,
    pub kind: RegistrationSourceKind,
}
/// Call after inserting the profile and before committing account creation.
/// A duplicate fails the transaction; observations are never overwritten.
pub async fn record(
    c: &mut PgConnection,
    user_id: i64,
    source: Option<RegistrationSource>,
) -> Result<()> {
    let (ip, kind, version) = match source {
        None => (None, "unknown", None),
        Some(s) => (
            Some(s.address.to_string()),
            match s.kind {
                RegistrationSourceKind::Socket => "socket",
                RegistrationSourceKind::TrustedXForwardedFor => "trusted_x_forwarded_for",
            },
            Some(1_i32),
        ),
    };
    let result = sqlx::query("INSERT INTO core_abuse.registration_observations(user_id,username_at_registration,email_at_registration,source_ip,source_kind,source_policy_version) SELECT user_id,username,email,$2::text::inet,$3,$4 FROM core_identity.user_profiles WHERE user_id=$1")
        .bind(user_id).bind(ip).bind(kind).bind(version).execute(c).await?;
    if result.rows_affected() != 1 {
        return Err(AbuseError::Unavailable);
    }
    Ok(())
}
#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct SimilarityQuery {
    pub before_user_id: Option<i64>,
    pub scan_limit: Option<i64>,
    pub limit: Option<i64>,
    pub window_seconds: Option<i64>,
}
#[derive(Clone, Debug, Serialize)]
pub struct EvidenceUser {
    pub user_id: i64,
    pub username: String,
    pub email: String,
    pub registration_username: Option<String>,
    pub registration_email: Option<String>,
    pub registered_at: Option<i64>,
    pub source_ip: Option<String>,
    pub source_kind: String,
    pub source_policy_version: Option<i32>,
    pub comparison_basis: &'static str,
    pub missing: Vec<&'static str>,
}
const SELECT: &str = "SELECT u.id,COALESCE(p.username,'') AS username,COALESCE(p.email,'') AS email,o.username_at_registration,o.email_at_registration,extract(epoch FROM o.registered_at)::bigint AS registered_at,host(o.source_ip) AS source_ip,COALESCE(o.source_kind,'unknown') AS source_kind,o.source_policy_version FROM core_identity.users u LEFT JOIN core_identity.user_profiles p ON p.user_id=u.id LEFT JOIN core_abuse.registration_observations o ON o.user_id=u.id";
fn evidence(r: &sqlx::postgres::PgRow) -> Result<EvidenceUser> {
    let registered_at: Option<i64> = r.try_get("registered_at")?;
    let source_ip: Option<String> = r.try_get("source_ip")?;
    let registration_username: Option<String> = r.try_get("username_at_registration")?;
    let mut missing = vec![];
    if registered_at.is_none() {
        missing.push("registration_time");
    }
    if source_ip.is_none() {
        missing.push("trusted_registration_ip");
    }
    if registration_username.is_none() {
        missing.push("registration_identity_snapshot");
    }
    Ok(EvidenceUser {
        user_id: r.try_get("id")?,
        username: r.try_get("username")?,
        email: r.try_get("email")?,
        registration_username,
        registration_email: r.try_get("email_at_registration")?,
        registered_at,
        source_ip,
        source_kind: r.try_get("source_kind")?,
        source_policy_version: r.try_get("source_policy_version")?,
        comparison_basis: if registered_at.is_some() {
            "registration_snapshot"
        } else {
            "current_profile"
        },
        missing,
    })
}
#[derive(Debug, Serialize)]
pub struct Signal {
    pub kind: &'static str,
    pub dimension: &'static str,
    pub strength: &'static str,
    pub detail: Value,
}
#[derive(Debug, Serialize)]
pub struct Candidate {
    pub user: EvidenceUser,
    pub signals: Vec<Signal>,
    pub review_priority: &'static str,
}
fn email_parts(v: &str) -> Option<(&str, String)> {
    let (local, domain) = v.split_once('@')?;
    if local.is_empty() || domain.is_empty() || domain.contains('@') {
        return None;
    }
    // No provider-specific dot/plus rewriting; local-part case is preserved.
    Some((local, domain.to_ascii_lowercase()))
}
fn common_fragment(a: &str, b: &str) -> usize {
    let a: Vec<char> = a.chars().collect();
    let b: Vec<char> = b.chars().collect();
    let mut prev = vec![0; b.len() + 1];
    let mut best = 0;
    for x in a {
        let mut next = vec![0; b.len() + 1];
        for (j, y) in b.iter().enumerate() {
            if x == *y {
                next[j + 1] = prev[j] + 1;
                best = best.max(next[j + 1]);
            }
        }
        prev = next;
    }
    best
}
fn digit_template(v: &str) -> Option<String> {
    if !v.bytes().any(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut out = String::new();
    let mut digits = false;
    for c in v.chars() {
        if c.is_ascii_digit() {
            if !digits {
                out.push('#');
            }
            digits = true;
        } else {
            out.push(c.to_ascii_lowercase());
            digits = false;
        }
    }
    Some(out)
}
fn shape(v: &str) -> Option<String> {
    if v.len() < 8
        || !v.bytes().all(|b| b.is_ascii_alphanumeric())
        || !v.bytes().any(|b| b.is_ascii_digit())
        || !v.bytes().any(|b| b.is_ascii_alphabetic())
    {
        return None;
    }
    Some(
        v.bytes()
            .map(|b| if b.is_ascii_digit() { '0' } else { 'a' })
            .collect(),
    )
}
fn compare(subject: &EvidenceUser, other: &EvidenceUser, window: i64) -> Option<Candidate> {
    let a = subject
        .registration_username
        .as_deref()
        .unwrap_or(&subject.username);
    let b = other
        .registration_username
        .as_deref()
        .unwrap_or(&other.username);
    let ae = subject
        .registration_email
        .as_deref()
        .unwrap_or(&subject.email);
    let be = other.registration_email.as_deref().unwrap_or(&other.email);
    let mut signals = vec![];
    let mut add = |kind, dimension, strength, detail| {
        signals.push(Signal {
            kind,
            dimension,
            strength,
            detail,
        })
    };
    if subject.source_ip.is_some() && subject.source_ip == other.source_ip {
        add(
            "same_registration_ip",
            "network",
            "contextual",
            json!({"shared_network_is_not_identity":true}),
        );
    }
    if let (Some(x), Some(y)) = (subject.registered_at, other.registered_at) {
        let seconds = x.abs_diff(y);
        if seconds <= window as u64 {
            add(
                "registration_time_cluster",
                "time",
                "contextual",
                json!({"seconds_apart":seconds,"window_seconds":window}),
            );
        }
    }
    if !a.is_empty() && a == b {
        add("username_exact", "username", "value_match", json!({}));
    } else {
        let n = common_fragment(&a.to_lowercase(), &b.to_lowercase());
        if n >= 6 && n * 2 >= a.chars().count().min(b.chars().count()) {
            add(
                "username_shared_fragment",
                "username",
                "weak",
                json!({"characters":n,"case_insensitive":true}),
            );
        }
        if let (Some(x), Some(y)) = (digit_template(a), digit_template(b))
            && x == y
        {
            add(
                "username_digit_template",
                "username",
                "weak",
                json!({"template":x}),
            );
        }
        if let (Some(x), Some(y)) = (shape(a), shape(b))
            && x == y
        {
            add(
                "username_character_shape",
                "username",
                "weak",
                json!({"shape":x,"does_not_measure_meaning":true}),
            );
        }
    }
    if let (Some((al, ad)), Some((bl, bd))) = (email_parts(ae), email_parts(be)) {
        if al == bl && ad == bd {
            add(
                "email_exact_preserving_local_part",
                "email",
                "value_match",
                json!({"provider_canonicalization":false}),
            );
        } else {
            let n = common_fragment(&al.to_lowercase(), &bl.to_lowercase());
            if n >= 6 && n * 2 >= al.chars().count().min(bl.chars().count()) {
                add(
                    "email_local_shared_fragment",
                    "email",
                    "weak",
                    json!({"characters":n,"case_insensitive":true,"same_domain":ad==bd}),
                );
            }
            if let (Some(x), Some(y)) = (digit_template(al), digit_template(bl))
                && x == y
                && x.chars().filter(|c| *c != '#').count() >= 4
            {
                add(
                    "email_local_digit_template",
                    "email",
                    "weak",
                    json!({"template":x,"same_domain":ad==bd}),
                );
            }
        }
    }
    // Time or common username character shape alone does not create a hit.
    if !signals.iter().any(|s| {
        s.dimension == "network"
            || s.dimension == "email"
            || s.kind == "username_exact"
            || s.kind == "username_shared_fragment"
            || s.kind == "username_digit_template"
    }) {
        return None;
    }
    let dimensions: BTreeSet<_> = signals.iter().map(|s| s.dimension).collect();
    let priority = if dimensions.contains("network")
        && dimensions.contains("time")
        && (dimensions.contains("username") || dimensions.contains("email"))
    {
        "high"
    } else if dimensions.len() >= 2 {
        "medium"
    } else {
        "low"
    };
    Some(Candidate {
        user: other.clone(),
        signals,
        review_priority: priority,
    })
}
impl AbuseStore {
    pub async fn registration_similarity(
        &self,
        secret: &str,
        id: i64,
        q: &SimilarityQuery,
    ) -> Result<Value> {
        let before = q.before_user_id.unwrap_or(id);
        let scan = q.scan_limit.unwrap_or(500);
        let limit = q.limit.unwrap_or(5);
        let window = q.window_seconds.unwrap_or(600);
        if id <= 0
            || before <= 0
            || before > id
            || !(1..=500).contains(&scan)
            || !(1..=20).contains(&limit)
            || !(1..=86400).contains(&window)
        {
            return Err(AbuseError::Invalid);
        }
        let mut tx = self.pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ")
            .execute(&mut *tx)
            .await?;
        super::admin::administrator(&mut tx, secret).await?;
        let row = sqlx::query(&format!("{SELECT} WHERE u.id=$1"))
            .bind(id)
            .fetch_optional(&mut *tx)
            .await?
            .ok_or(AbuseError::NotFound)?;
        let subject = evidence(&row)?;
        let rows = sqlx::query(&format!(
            "{SELECT} WHERE u.id<$1 ORDER BY u.id DESC LIMIT $2"
        ))
        .bind(before)
        .bind(scan + 1)
        .fetch_all(&mut *tx)
        .await?;
        let scanned = rows.len().min(scan as usize);
        let more = rows.len() > scan as usize;
        let next = if more {
            Some(rows[scanned - 1].try_get::<i64, _>("id")?)
        } else {
            None
        };
        let mut matches = vec![];
        for r in rows.iter().take(scanned) {
            if let Some(m) = compare(&subject, &evidence(r)?, window) {
                matches.push(m);
            }
        }
        matches.sort_by_key(|m| {
            (
                match m.review_priority {
                    "high" => 0,
                    "medium" => 1,
                    _ => 2,
                },
                std::cmp::Reverse(m.signals.len()),
                std::cmp::Reverse(m.user.user_id),
            )
        });
        let matched = matches.len();
        matches.truncate(limit as usize);
        tx.commit().await?;
        Ok(
            json!({"version":1,"subject":subject,"candidates":matches,"coverage":{"before_user_id":before,"scanned_users":scanned,"matched_users":matched,"returned_candidates":matches.len(),"candidate_limit":limit,"candidates_truncated":matched>limit as usize,"next_before_user_id":next,"scan_truncated":more,"window_seconds":window,"scope":"previous_user_ids"},"assessment":{"type":"review_candidates_only","probability":null,"account_actions_authorized":false,"limitations":["Shared IP may be a household, organization, proxy or carrier NAT.","Name patterns and shared email fragments do not establish common ownership or abuse.","Missing historical registration evidence is unknown, never reconstructed from sessions.","Review priority is a heuristic ordering, not a calibrated probability.","Current profiles may differ from registration snapshots; the comparison basis is explicit."]}}),
        )
    }
    /// Read-only metadata for a host-owned platform key; not a purpose grant.
    /// Authorization and the actual financial engine still run on each model call.
    pub async fn assistant_model_credential(&self, secret: &str) -> Result<Value> {
        use crate::{
            accounts::{Account, AccountKind},
            identity::CredentialKind,
        };
        let mut tx = self.pool.begin().await?;
        let actor = crate::identity::authorize_in_transaction(&mut tx, secret).await?;
        let personal = Account {
            kind: AccountKind::Personal,
            id: actor.user_id,
        };
        if actor.credential_kind != CredentialKind::ApiKey
            || actor.owner != personal
            || actor.funding_accounts != vec![personal]
        {
            return Err(AbuseError::Forbidden);
        }
        let row=sqlx::query("SELECT s.enabled,s.model_limits_enabled,s.model_limits,c.personal_billing_preference,b.limit_credits,b.unlimited,b.revision FROM core_identity.api_key_settings s JOIN core_identity.credentials c ON c.id=s.credential_id JOIN core_charging.budgets b ON b.scope='key' AND b.key_id=c.id AND b.issuer_user_id=c.user_id AND b.period='lifetime' WHERE c.id=$1 FOR SHARE OF s,c,b")
            .bind(actor.credential_id).fetch_optional(&mut *tx).await?.ok_or(AbuseError::Forbidden)?;
        if super::is_marked_in_transaction(&mut tx, actor.user_id).await? {
            return Err(AbuseError::Forbidden);
        }
        let models: Vec<String> = row.try_get("model_limits")?;
        let cap: i64 = row.try_get("limit_credits")?;
        if !row.try_get::<bool, _>("enabled")?
            || !row.try_get::<bool, _>("model_limits_enabled")?
            || models.is_empty()
            || models.len() > 200
            || row.try_get::<bool, _>("unlimited")?
            || cap <= 0
            || row
                .try_get::<Option<String>, _>("personal_billing_preference")?
                .as_deref()
                != Some("wallet_only")
        {
            return Err(AbuseError::Forbidden);
        }
        let (used, _, _) = crate::billing::charging::key_quota_snapshot_for_issuer(
            &mut tx,
            actor.credential_id,
            actor.user_id,
        )
        .await
        .map_err(|_| AbuseError::Unavailable)?;
        tx.commit().await?;
        Ok(
            json!({"version":1,"credential_kind":"api_key","credential_id":actor.credential_id,"user_id":actor.user_id,"owner":personal,"funding_accounts":[personal],"billing_preference":"wallet_only","model_limits_enabled":true,"allowed_models":models,"budget":{"period":"lifetime","limit_credits":cap,"consumed_credits":used,"unlimited":false,"revision":row.try_get::<i64,_>("revision")?},"purpose_grant":null}),
        )
    }
}
pub(super) async fn get_similarity(
    State(s): State<AbuseStore>,
    h: HeaderMap,
    Path(id): Path<String>,
    q: std::result::Result<Query<SimilarityQuery>, axum::extract::rejection::QueryRejection>,
) -> Result<Json<Value>> {
    let Query(q) = q.map_err(|_| AbuseError::Invalid)?;
    let id = id.parse().map_err(|_| AbuseError::Invalid)?;
    Ok(Json(
        s.registration_similarity(super::admin::bearer(&h)?, id, &q)
            .await?,
    ))
}
pub(super) async fn get_model_credential(
    State(s): State<AbuseStore>,
    h: HeaderMap,
) -> Result<Json<Value>> {
    Ok(Json(
        s.assistant_model_credential(super::admin::bearer(&h)?)
            .await?,
    ))
}
#[cfg(test)]
mod tests {
    use super::*;
    fn user(id: i64, name: &str, email: &str) -> EvidenceUser {
        EvidenceUser {
            user_id: id,
            username: name.into(),
            email: email.into(),
            registration_username: None,
            registration_email: None,
            registered_at: None,
            source_ip: None,
            source_kind: "unknown".into(),
            source_policy_version: None,
            comparison_basis: "current_profile",
            missing: vec![],
        }
    }
    #[test]
    fn no_provider_rewriting() {
        let a = user(2, "Alice", "a.b+one@gmail.com");
        let b = user(1, "Bob", "ab@gmail.com");
        assert!(compare(&a, &b, 600).is_none());
        let b = user(1, "Bob", "a.b+one@GMAIL.COM");
        assert!(
            compare(&a, &b, 600)
                .unwrap()
                .signals
                .iter()
                .any(|s| s.kind == "email_exact_preserving_local_part")
        );
        let b = user(1, "Bob", "A.b+one@gmail.com");
        assert!(
            !compare(&a, &b, 600)
                .unwrap()
                .signals
                .iter()
                .any(|s| s.kind == "email_exact_preserving_local_part")
        );
    }
    #[test]
    fn domain_and_shape_alone_are_not_hits() {
        assert!(
            compare(
                &user(2, "a1b2c3d4", "first@gmail.com"),
                &user(1, "x9y8z7w6", "other@gmail.com"),
                600
            )
            .is_none()
        );
    }
    #[test]
    fn composite_evidence_is_not_probability() {
        let mut a = user(2, "robot1234", "sample1234@example.org");
        let mut b = user(1, "robot5678", "sample5678@example.org");
        a.source_ip = Some("192.0.2.10".into());
        b.source_ip = a.source_ip.clone();
        a.registered_at = Some(1000);
        b.registered_at = Some(990);
        let c = compare(&a, &b, 600).unwrap();
        assert_eq!(c.review_priority, "high");
        assert!(
            c.signals
                .iter()
                .any(|s| s.kind == "email_local_digit_template")
        );
    }
    #[test]
    fn snapshot_not_later_rename() {
        let mut a = user(2, "renamed", "new@example.org");
        let mut b = user(1, "other", "unrelated@example.org");
        a.registration_username = Some("bot1234".into());
        b.registration_username = Some("bot5678".into());
        assert!(compare(&a, &b, 600).is_some());
    }
    #[test]
    fn unicode_fragment_counts_characters() {
        assert_eq!(common_fragment("注册模板一号", "注册模板二号"), 4);
        assert_eq!(common_fragment("abcdef123", "xxabcdefyy"), 6);
    }
}
