// SPDX-License-Identifier: AGPL-3.0-or-later
//! Reads committed native model charges, not legacy logs or opening counters.
//!
//! Do not mount this as a legacy `/api/log` or all-history statistics endpoint.
//! The explicit `includes_legacy = false` is part of every response. Historical
//! sources still require the A03 ownership, unit and duplicate-mapping contract.
use super::snapshot::ReadSnapshot;
use crate::financial_profile::{self, FinancialProfile};
use crate::identity::{self, CredentialKind, IdentityError};
use serde::Serialize;
use sqlx::{PgPool, Postgres, QueryBuilder, Row, postgres::PgRow};

pub const MAX_PAGE_SIZE: u32 = 200;
pub const MAX_PAGE_OFFSET: u64 = 1_000_000;
pub const MAX_FLOW_BUCKETS: i64 = 2_048;
const CREDITS_PER_USD: i128 = 500_000;
const SOURCE: &str = "core_charging.requests/v1";

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ReadError {
    Unauthorized,
    Forbidden,
    Invalid,
    Unavailable,
    Storage,
}
impl From<sqlx::Error> for ReadError {
    fn from(_: sqlx::Error) -> Self {
        Self::Storage
    }
}
impl From<IdentityError> for ReadError {
    fn from(error: IdentityError) -> Self {
        match error {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            _ => Self::Storage,
        }
    }
}

/// The server selects a mode for its route, never from a JSON permission field.
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ReadMode {
    SelfSession,
    AdministratorSession,
    ApiKey,
}

/// All filters only narrow server-derived authority. No payer/team expansion.
#[derive(Clone, Debug, Eq, PartialEq)]
pub struct NativeFilter {
    pub start: i64,
    pub end: i64,
    pub user_id: Option<i64>,
    pub key_id: Option<i64>,
    pub model: Option<String>,
    pub group: Option<String>,
}
impl NativeFilter {
    fn validate(&self) -> Result<(), ReadError> {
        if self.start < -62_135_596_800
            || self.end > 253_402_300_800
            || self.start >= self.end
            || [self.user_id, self.key_id].into_iter().flatten().any(|id| id <= 0)
            || [&self.model, &self.group]
                .into_iter()
                .flatten()
                .any(|text| text.is_empty() || text.len() > 128 || text.chars().any(char::is_control))
        {
            return Err(ReadError::Invalid);
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct NativePageRequest {
    pub page: u32,
    pub size: u32,
}
impl NativePageRequest {
    fn offset(self) -> Result<i64, ReadError> {
        if self.page == 0 || self.size == 0 || self.size > MAX_PAGE_SIZE {
            return Err(ReadError::Invalid);
        }
        let offset = u64::from(self.page - 1) * u64::from(self.size);
        if offset > MAX_PAGE_OFFSET {
            return Err(ReadError::Invalid);
        }
        i64::try_from(offset).map_err(|_| ReadError::Invalid)
    }
}

/// Native amounts are decimal strings so a browser cannot round BIGINT values.
/// Gross, refund and net amounts remain separate; a refund is not another call.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct NativeUsageTotals {
    pub source: &'static str,
    pub includes_legacy: bool,
    pub request_count: String,
    pub settled_credits: String,
    pub refunded_credits: String,
    pub net_credits: String,
    pub settled_usd: String,
    pub refunded_usd: String,
    pub net_usd: String,
    pub input_tokens: String,
    pub cached_tokens: String,
    pub output_tokens: String,
}

/// No raw price JSON, fingerprint, worker digest, provider metadata or payer ID.
#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct NativeUsageRow {
    pub id: String,
    pub authorized_at: i64,
    pub model: String,
    pub group: String,
    pub price_version: String,
    pub settled_credits: String,
    pub refunded_credits: String,
    pub net_credits: String,
    pub net_usd: String,
    pub input_tokens: String,
    pub cached_tokens: String,
    pub output_tokens: String,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct NativeUsagePage {
    pub totals: NativeUsageTotals,
    pub page: u32,
    pub page_size: u32,
    pub items: Vec<NativeUsageRow>,
}

#[derive(Clone, Debug, Eq, PartialEq, Serialize)]
pub struct NativeUsageBucket {
    pub start: i64,
    pub totals: NativeUsageTotals,
}

// This object cannot be constructed from caller-supplied actor/account values.
// Its connection and authorized filters stay private for its entire lifetime.
pub struct NativeUsageRead<'a> {
    snapshot: ReadSnapshot<'a>,
    filter: NativeFilter,
    owner_account_id: Option<i64>,
    actor_user_id: i64,
    mode: ReadMode,
}

const TOTAL_COLUMNS: &str = "count(*)::text AS request_count, \
    COALESCE(sum(r.settled::numeric),0)::text AS settled, \
    COALESCE(sum(r.refunded::numeric),0)::text AS refunded, \
    COALESCE(sum(r.input_tokens::numeric),0)::text AS input_tokens, \
    COALESCE(sum(r.cached_tokens::numeric),0)::text AS cached_tokens, \
    COALESCE(sum(r.output_tokens::numeric),0)::text AS output_tokens";

impl<'a> NativeUsageRead<'a> {
    pub async fn begin(
        pool: &'a PgPool,
        secret: &str,
        mode: ReadMode,
        mut filter: NativeFilter,
    ) -> Result<Self, ReadError> {
        filter.validate()?;
        let mut snapshot = ReadSnapshot::begin(pool).await?;
        // S11's narrow integration helper reuses the existing identity decoder,
        // including revocation/expiry/grants, without forbidden FOR SHARE locks.
        let actor = identity::authorize_read_in_transaction(snapshot.connection(), secret).await?;
        let owner_account_id = match mode {
            ReadMode::SelfSession => {
                if actor.credential_kind != CredentialKind::Session
                    || filter.user_id.is_some_and(|id| id != actor.user_id)
                {
                    return Err(ReadError::Forbidden);
                }
                filter.user_id = Some(actor.user_id);
                None
            }
            ReadMode::AdministratorSession => {
                if actor.credential_kind != CredentialKind::Session {
                    return Err(ReadError::Forbidden);
                }
                // A paid service level is NOT a platform administrator role.
                let allowed: bool = sqlx::query_scalar(
                    "SELECT platform_role IN ('admin','superadmin') FROM core_identity.users WHERE id=$1 AND active",
                )
                .bind(actor.user_id)
                .fetch_one(snapshot.connection())
                .await?;
                if !allowed {
                    return Err(ReadError::Forbidden);
                }
                None
            }
            ReadMode::ApiKey => {
                if actor.credential_kind != CredentialKind::ApiKey
                    || filter.user_id.is_some_and(|id| id != actor.user_id)
                    || filter.key_id.is_some_and(|id| id != actor.credential_id)
                {
                    return Err(ReadError::Forbidden);
                }
                filter.user_id = Some(actor.user_id);
                filter.key_id = Some(actor.credential_id);
                Some(actor.owner_account_id)
            }
        };
        Ok(Self {
            snapshot,
            filter,
            owner_account_id,
            actor_user_id: actor.user_id,
            mode,
        })
    }

    pub async fn totals(&mut self) -> Result<NativeUsageTotals, ReadError> {
        let mut query = QueryBuilder::<Postgres>::new("SELECT ");
        query.push(TOTAL_COLUMNS);
        selection(&mut query, &self.filter, self.owner_account_id);
        let row = query.build().fetch_one(self.snapshot.connection()).await?;
        totals_from_row(&row)
    }

    /// Count and page share the authority, filters AND database snapshot.
    pub async fn page(&mut self, page: NativePageRequest) -> Result<NativeUsagePage, ReadError> {
        let offset = page.offset()?;
        let totals = self.totals().await?;
        let mut query = QueryBuilder::<Postgres>::new(
            "SELECT r.id,r.authorized_at,r.settled,r.refunded,r.input_tokens,r.cached_tokens,r.output_tokens, \
             CASE WHEN octet_length(r.price->>'model')<=128 THEN r.price->>'model' END AS model, \
             CASE WHEN octet_length(r.price->>'group')<=128 THEN r.price->>'group' END AS group_name, \
             CASE WHEN octet_length(r.price->>'version')<=128 THEN r.price->>'version' END AS price_version",
        );
        selection(&mut query, &self.filter, self.owner_account_id);
        query.push(" ORDER BY r.authorized_at DESC,r.id COLLATE \"C\" DESC LIMIT ");
        query.push_bind(i64::from(page.size));
        query.push(" OFFSET ");
        query.push_bind(offset);
        let rows = query.build().fetch_all(self.snapshot.connection()).await?;
        let items = rows.iter().map(usage_from_row).collect::<Result<Vec<_>, _>>()?;
        Ok(NativeUsagePage { totals, page: page.page, page_size: page.size, items })
    }

    /// UTC fixed-duration buckets, not calendar/time-zone subscription periods.
    pub async fn flow(&mut self, seconds: i64) -> Result<Vec<NativeUsageBucket>, ReadError> {
        if ![900, 3600, 86400].contains(&seconds)
            || (self.filter.end - 1).div_euclid(seconds)
                - self.filter.start.div_euclid(seconds) + 1 > MAX_FLOW_BUCKETS
        {
            return Err(ReadError::Invalid);
        }
        let mut query = QueryBuilder::<Postgres>::new("SELECT (floor(r.authorized_at::numeric/");
        query.push_bind(seconds);
        query.push(")*");
        query.push_bind(seconds);
        query.push(")::bigint AS bucket_start,");
        query.push(TOTAL_COLUMNS);
        selection(&mut query, &self.filter, self.owner_account_id);
        query.push(" GROUP BY 1 ORDER BY 1");
        let rows = query.build().fetch_all(self.snapshot.connection()).await?;
        rows.iter().map(|row| Ok(NativeUsageBucket {
            start: row.try_get("bucket_start")?,
            totals: totals_from_row(row)?,
        })).collect()
    }

    /// A key authorizes key history only, never the user's complete wallet.
    pub async fn self_profile(&mut self) -> Result<FinancialProfile, ReadError> {
        if self.mode != ReadMode::SelfSession {
            return Err(ReadError::Forbidden);
        }
        financial_profile::read(self.snapshot.connection(), self.actor_user_id)
            .await
            .map_err(|error| match error {
                financial_profile::ProfileError::Invalid => ReadError::Invalid,
                financial_profile::ProfileError::Unavailable => ReadError::Unavailable,
                financial_profile::ProfileError::Storage => ReadError::Storage,
            })
    }

    pub async fn finish(self) -> Result<(), ReadError> {
        self.snapshot.finish().await.map_err(Into::into)
    }
}

fn selection<'q>(query: &mut QueryBuilder<'q, Postgres>, filter: &'q NativeFilter, owner: Option<i64>) {
    query.push(" FROM core_charging.requests r WHERE r.state='settled' AND r.authorized_at>=");
    query.push_bind(filter.start);
    query.push(" AND r.authorized_at<");
    query.push_bind(filter.end);
    for (name, value) in [("r.actor_user_id", filter.user_id), ("r.key_id", filter.key_id), ("r.owner_account_id", owner)] {
        if let Some(value) = value {
            query.push(" AND ").push(name).push("=").push_bind(value);
        }
    }
    for (name, value) in [("model", &filter.model), ("group", &filter.group)] {
        if let Some(value) = value {
            query.push(" AND r.price->>'").push(name).push("'=").push_bind(value);
        }
    }
}

fn nonnegative(value: &str) -> Result<i128, ReadError> {
    value.parse::<i128>().ok().filter(|amount| *amount >= 0).ok_or(ReadError::Unavailable)
}

fn credit_usd(value: i128) -> String {
    // All values are checked nonnegative first. 1/500000 USD is exactly 2e-6.
    let whole = value / CREDITS_PER_USD;
    let fraction = (value % CREDITS_PER_USD) * 2;
    if fraction == 0 {
        whole.to_string()
    } else {
        format!("{whole}.{fraction:06}").trim_end_matches('0').to_owned()
    }
}

fn totals_from_row(row: &PgRow) -> Result<NativeUsageTotals, ReadError> {
    let amount = |name| -> Result<i128, ReadError> { nonnegative(&row.try_get::<String, _>(name)?) };
    let settled = amount("settled")?;
    let refunded = amount("refunded")?;
    let net = settled.checked_sub(refunded).filter(|value| *value >= 0).ok_or(ReadError::Storage)?;
    Ok(NativeUsageTotals {
        source: SOURCE,
        includes_legacy: false,
        request_count: amount("request_count")?.to_string(),
        settled_credits: settled.to_string(),
        refunded_credits: refunded.to_string(),
        net_credits: net.to_string(),
        settled_usd: credit_usd(settled),
        refunded_usd: credit_usd(refunded),
        net_usd: credit_usd(net),
        input_tokens: amount("input_tokens")?.to_string(),
        cached_tokens: amount("cached_tokens")?.to_string(),
        output_tokens: amount("output_tokens")?.to_string(),
    })
}

fn usage_from_row(row: &PgRow) -> Result<NativeUsageRow, ReadError> {
    let amount = |name| -> Result<i128, ReadError> {
        let value: i64 = row.try_get(name)?;
        (value >= 0).then_some(i128::from(value)).ok_or(ReadError::Storage)
    };
    let text = |name| -> Result<String, ReadError> {
        let value: Option<String> = row.try_get(name)?;
        value.filter(|v| !v.is_empty() && v.len() <= 128 && !v.chars().any(char::is_control))
            .ok_or(ReadError::Storage)
    };
    let settled = amount("settled")?;
    let refunded = amount("refunded")?;
    let net = settled.checked_sub(refunded).filter(|value| *value >= 0).ok_or(ReadError::Storage)?;
    Ok(NativeUsageRow {
        id: text("id")?,
        authorized_at: row.try_get("authorized_at")?,
        model: text("model")?,
        group: text("group_name")?,
        price_version: text("price_version")?,
        settled_credits: settled.to_string(),
        refunded_credits: refunded.to_string(),
        net_credits: net.to_string(),
        net_usd: credit_usd(net),
        input_tokens: amount("input_tokens")?.to_string(),
        cached_tokens: amount("cached_tokens")?.to_string(),
        output_tokens: amount("output_tokens")?.to_string(),
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn native_money_is_exact_and_never_uses_a_legacy_divisor() {
        for (credits, expected) in [(0, "0"), (1, "0.000002"), (3, "0.000006"), (500_001, "1.000002")] {
            assert_eq!(credit_usd(credits), expected);
        }
        assert_eq!(nonnegative("-1"), Err(ReadError::Unavailable));
        assert_eq!(nonnegative("1.0"), Err(ReadError::Unavailable));
        assert_eq!(nonnegative("170141183460469231731687303715884105728"), Err(ReadError::Unavailable));
    }

    #[test]
    fn pagination_rejects_zero_and_offset_overflow() {
        for page in [NativePageRequest { page: 0, size: 1 }, NativePageRequest { page: 1, size: 0 },
            NativePageRequest { page: 1, size: MAX_PAGE_SIZE + 1 }, NativePageRequest { page: u32::MAX, size: 200 }] {
            assert_eq!(page.offset(), Err(ReadError::Invalid));
        }
        assert_eq!(NativePageRequest { page: 2, size: 200 }.offset(), Ok(200));
    }
}
