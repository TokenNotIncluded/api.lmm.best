//! Consumer-side read contract. The offline source owner supplies the versioned
//! views; missing, writable, duplicate or incomplete sources fail closed.
//! No runtime DDL, import, money mutation or best-effort partial history.
use super::{
    LogQuotaInput, MAX_METADATA_BYTES, MAX_RECORDS, QuotaMoneyProjection, QuotaStatisticProjection,
    captured_credits_per_usd, project_consumption_statistic, project_log, snapshot::ReadSnapshot,
};
use crate::identity::{self, CredentialKind, IdentityError};
use serde::{Deserialize, Serialize};
use sqlx::{PgConnection, PgPool, Postgres, QueryBuilder, Row};

pub const MAX_PAGE: i64 = 100;
pub const MAX_OFFSET: i64 = 1_000_000;
const SAFE_INTEGER: i128 = 9_007_199_254_740_991;
const BATCH: i64 = 64;
const COLUMNS: &str = "id,event_key,user_id,owner_account_id,credential_id,created_at,log_type,username,token_name,model_name,quota,prompt_tokens,completion_tokens,use_time,is_stream,channel_id,group_name,request_id,CASE WHEN octet_length(other)<=65536 THEN other ELSE '' END AS other";

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum ReadError {
    Unauthorized,
    Forbidden,
    Invalid,
    Unavailable,
    Limit,
    Busy,
}
impl From<sqlx::Error> for ReadError {
    fn from(_: sqlx::Error) -> Self {
        Self::Unavailable
    }
}
impl From<IdentityError> for ReadError {
    fn from(e: IdentityError) -> Self {
        match e {
            IdentityError::Unauthorized => Self::Unauthorized,
            IdentityError::Forbidden => Self::Forbidden,
            IdentityError::Invalid => Self::Invalid,
            _ => Self::Unavailable,
        }
    }
}
pub type Result<T> = std::result::Result<T, ReadError>;

#[derive(Clone, Copy)]
pub enum Audience {
    Administrator,
    SelfUser,
    Token,
}

/// HTTP filters are not authority. User/key/account constraints are resolved
/// from the stored credential before these filters reach a query.
#[derive(Clone, Default, Deserialize)]
#[serde(default, deny_unknown_fields)]
pub struct Filter {
    #[serde(rename = "type")]
    pub log_type: i32,
    pub start_timestamp: i64,
    pub end_timestamp: i64,
    pub user_id: Option<i64>,
    pub team_id: Option<i64>,
    pub username: String,
    pub token_name: String,
    pub model_name: String,
    pub channel: Option<i64>,
    pub group: String,
    pub request_id: String,
    pub p: i64,
    pub page_size: i64,
}
impl Filter {
    pub fn validate(&self) -> Result<()> {
        if !(0..=6).contains(&self.log_type)
            || self.start_timestamp < 0
            || self.end_timestamp < 0
            || (self.end_timestamp != 0 && self.start_timestamp > self.end_timestamp)
            || self.user_id.is_some_and(|id| id <= 0)
            || self.team_id.is_some_and(|id| id <= 0)
            || self.channel.is_some_and(|id| id < 0)
            || self.p < 0
            || self.page_size < 0
            || [
                &self.username,
                &self.token_name,
                &self.model_name,
                &self.group,
                &self.request_id,
            ]
            .iter()
            .any(|s| s.len() > 256 || s.contains('\0'))
        {
            return Err(ReadError::Invalid);
        }
        self.offset()?;
        Ok(())
    }
    pub fn page(&self) -> i64 {
        self.p.max(1)
    }
    pub fn size(&self) -> i64 {
        if self.page_size == 0 {
            10
        } else {
            self.page_size.clamp(10, MAX_PAGE)
        }
    }
    fn offset(&self) -> Result<i64> {
        self.page()
            .checked_sub(1)
            .and_then(|v| v.checked_mul(self.size()))
            .filter(|v| *v <= MAX_OFFSET)
            .ok_or(ReadError::Limit)
    }
}

#[derive(Serialize, sqlx::FromRow)]
pub struct LogRow {
    pub id: i64,
    #[serde(skip)]
    pub event_key: String,
    pub user_id: i64,
    #[serde(skip)]
    pub owner_account_id: i64,
    #[serde(skip)]
    pub credential_id: Option<i64>,
    pub created_at: i64,
    #[serde(rename = "type")]
    pub log_type: i32,
    pub username: String,
    pub token_name: String,
    pub model_name: String,
    pub quota: Option<i64>,
    pub prompt_tokens: i64,
    pub completion_tokens: i64,
    pub use_time: i64,
    pub is_stream: bool,
    #[serde(skip_serializing_if = "Option::is_none")]
    pub channel_id: Option<i64>,
    #[serde(rename = "group")]
    pub group_name: String,
    pub request_id: String,
    #[serde(skip)]
    pub other: String,
}
impl LogRow {
    pub fn projection_input(&self) -> LogQuotaInput<'_> {
        LogQuotaInput {
            quota: self.quota,
            log_type: self.log_type,
            other: &self.other,
        }
    }
    fn validate(&self) -> Result<()> {
        if self.id <= 0
            || self.user_id <= 0
            || self.owner_account_id <= 0
            || self.credential_id.is_some_and(|id| id <= 0)
            || self.created_at < 0
            || self.prompt_tokens < 0
            || self.completion_tokens < 0
            || self.use_time < 0
            || !(1..=6).contains(&self.log_type)
            || self.event_key.is_empty()
            || self.event_key.len() > 256
            || self.other.len() > MAX_METADATA_BYTES
            || [
                &self.username,
                &self.token_name,
                &self.model_name,
                &self.group_name,
                &self.request_id,
            ]
            .iter()
            .any(|v| v.len() > 1024)
            || [
                self.quota.unwrap_or(0),
                self.prompt_tokens,
                self.completion_tokens,
                self.use_time,
            ]
            .into_iter()
            .any(|v| i128::from(v).abs() > SAFE_INTEGER)
        {
            return Err(ReadError::Unavailable);
        }
        Ok(())
    }
}
#[derive(Serialize)]
pub struct DisplayLog {
    #[serde(flatten)]
    pub row: LogRow,
    #[serde(flatten)]
    pub money: QuotaMoneyProjection,
}
#[derive(Serialize)]
pub struct Page {
    pub page: i64,
    pub page_size: i64,
    pub total: i64,
    pub items: Vec<DisplayLog>,
}
#[derive(Serialize)]
pub struct Statistics {
    pub quota: Option<i64>,
    pub rpm: i64,
    pub tpm: i64,
    pub total: i64,
    #[serde(flatten)]
    pub projection: QuotaStatisticProjection,
}
/// Its only connection is the read-only transaction. It cannot construct a
/// second snapshot accidentally when running counts, pages, or projections.
pub struct HistoryRead<'a> {
    snapshot: ReadSnapshot<'a>,
    filter: Filter,
    user: Option<i64>,
    credential: Option<i64>,
    account: Option<i64>,
    administrator: bool,
    root: bool,
}
impl<'a> HistoryRead<'a> {
    pub async fn begin(
        pool: &'a PgPool,
        secret: &str,
        audience: Audience,
        filter: Filter,
    ) -> Result<Self> {
        filter.validate()?;
        let mut snapshot = ReadSnapshot::begin(pool).await?;
        let actor = identity::authorize_read_in_transaction(snapshot.connection(), secret).await?;
        let administrator = matches!(audience, Audience::Administrator);
        match audience {
            Audience::Administrator
                if actor.credential_kind != CredentialKind::Session || actor.platform_level < 5 =>
            {
                return Err(ReadError::Forbidden);
            }
            Audience::SelfUser if actor.credential_kind != CredentialKind::Session => {
                return Err(ReadError::Forbidden);
            }
            Audience::Token if actor.credential_kind != CredentialKind::ApiKey => {
                return Err(ReadError::Forbidden);
            }
            _ => {}
        }
        if !administrator
            && (filter.user_id.is_some_and(|id| id != actor.user_id)
                || !filter.username.is_empty()
                || filter.channel.is_some())
        {
            return Err(ReadError::Forbidden);
        }
        let mut account = matches!(audience, Audience::Token).then_some(actor.owner_account_id);
        if let Some(team) = filter.team_id {
            let team_account:Option<i64>=sqlx::query_scalar(
                "SELECT t.account_id FROM core_identity.teams t JOIN core_identity.accounts a ON a.id=t.account_id WHERE t.id=$1 AND t.active AND a.active AND ($3 OR t.owner_user_id=$2 OR EXISTS(SELECT 1 FROM core_identity.memberships m WHERE m.team_id=t.id AND m.user_id=$2 AND m.active))")
                .bind(team).bind(actor.user_id).bind(administrator).fetch_optional(snapshot.connection()).await?;
            let team_account = team_account.ok_or(ReadError::Forbidden)?;
            if account.is_some_and(|id| id != team_account) {
                return Err(ReadError::Forbidden);
            }
            account = Some(team_account);
        }
        let user = if administrator {
            filter.user_id
        } else {
            Some(actor.user_id)
        };
        let credential = matches!(audience, Audience::Token).then_some(actor.credential_id);
        let root = administrator && actor.platform_level == 6;
        let mut this = Self {
            snapshot,
            filter,
            user,
            credential,
            account,
            administrator,
            root,
        };
        this.require_source("logs").await?;
        if credential.is_some() {
            this.require_source("token_logs").await?;
        }
        Ok(this)
    }
    pub fn connection(&mut self) -> &mut PgConnection {
        self.snapshot.connection()
    }
    pub fn administrator(&self) -> bool {
        self.administrator
    }
    pub async fn finish(self) -> Result<()> {
        self.snapshot.finish().await.map_err(Into::into)
    }

    /// Receipts are supplied by the offline importer. The network role must not
    /// be able to manufacture them, or mutate any of the read contract objects.
    pub async fn require_source(&mut self, dataset: &str) -> Result<()> {
        let exists: bool =
            sqlx::query_scalar("SELECT to_regclass('core_history.source_contract_v1') IS NOT NULL")
                .fetch_one(self.connection())
                .await?;
        if !exists {
            return Err(ReadError::Unavailable);
        }
        self.require_read_only("core_history.source_contract_v1").await?;
        let receipt: bool = sqlx::query_scalar(
            "SELECT count(*)=1 AND coalesce(bool_and(version=1 AND complete AND manifest_sha256~'^[0-9a-f]{64}$'),false) FROM core_history.source_contract_v1 WHERE dataset=$1",
        )
        .bind(dataset)
        .fetch_one(self.connection())
        .await?;
        if !receipt {
            return Err(ReadError::Unavailable);
        }
        let relation = match dataset {
            "logs" | "token_logs" => "core_history.log_records_v1",
            "quota_data" => "core_history.quota_data_v1",
            "financial-options" => "core_history.finance_options_v1",
            _ => "core_history.finance_records_v1",
        };
        self.require_read_only(relation).await
    }

    async fn require_read_only(&mut self, relation: &str) -> Result<()> {
        // A table-level check alone misses INSERT/UPDATE grants on individual
        // columns. Neither source rows nor their receipt may be writable.
        let writable: bool = sqlx::query_scalar(
            "SELECT has_schema_privilege(current_user,'core_history','CREATE') OR has_table_privilege(current_user,$1,'INSERT,UPDATE,DELETE,TRUNCATE') OR has_any_column_privilege(current_user,$1,'INSERT,UPDATE')",
        )
        .bind(relation)
        .fetch_one(self.connection())
        .await?;
        if writable {
            return Err(ReadError::Unavailable);
        }
        Ok(())
    }
    fn selection(&self, columns: &str, consumption: bool) -> QueryBuilder<'static, Postgres> {
        let mut q = QueryBuilder::new(format!(
            "SELECT {columns} FROM core_history.log_records_v1 WHERE TRUE"
        ));
        if let Some(id) = self.user {
            q.push(" AND user_id=").push_bind(id);
        }
        if let Some(id) = self.credential {
            q.push(" AND credential_id=").push_bind(id);
        }
        if let Some(id) = self.account {
            q.push(" AND owner_account_id=").push_bind(id);
        }
        if consumption {
            q.push(" AND log_type=2");
        } else if self.filter.log_type != 0 {
            q.push(" AND log_type=").push_bind(self.filter.log_type);
        }
        if self.filter.start_timestamp != 0 {
            q.push(" AND created_at>=")
                .push_bind(self.filter.start_timestamp);
        }
        if self.filter.end_timestamp != 0 {
            q.push(" AND created_at<=")
                .push_bind(self.filter.end_timestamp);
        }
        for (column, value) in [
            ("username", &self.filter.username),
            ("token_name", &self.filter.token_name),
            ("group_name", &self.filter.group),
            ("request_id", &self.filter.request_id),
        ] {
            if !value.is_empty() {
                q.push(format!(" AND {column}=")).push_bind(value.clone());
            }
        }
        if !self.filter.model_name.is_empty() {
            // Literal substring, not a caller-controlled LIKE expression.
            q.push(" AND strpos(model_name,")
                .push_bind(self.filter.model_name.clone())
                .push(")>0");
        }
        if let Some(id) = self.filter.channel {
            q.push(" AND channel_id=").push_bind(id);
        }
        q
    }
    async fn count(&mut self, consumption: bool) -> Result<i64> {
        let mut q=self.selection("count(*)::bigint AS total,count(DISTINCT id)::bigint AS ids,count(DISTINCT event_key)::bigint AS events",consumption);
        let row = q.build().fetch_one(self.connection()).await?;
        let count: i64 = row.try_get("total")?;
        if count != row.try_get::<i64, _>("ids")? || count != row.try_get::<i64, _>("events")? {
            // Never arbitrarily pick a legacy or native duplicate as authoritative.
            return Err(ReadError::Unavailable);
        }
        Ok(count)
    }
    pub async fn logs(&mut self) -> Result<Page> {
        let total = self.count(false).await?;
        let mut query = self.selection(COLUMNS, false);
        query
            .push(" ORDER BY id DESC LIMIT ")
            .push_bind(self.filter.size())
            .push(" OFFSET ")
            .push_bind(self.filter.offset()?);
        let rows: Vec<LogRow> = query.build_query_as().fetch_all(self.connection()).await?;
        let mut items = Vec::with_capacity(rows.len());
        for mut row in rows {
            row.validate()?;
            let money = project_log(row.projection_input());
            if !self.administrator {
                row.channel_id = None;
            }
            items.push(DisplayLog { row, money });
        }
        Ok(Page {
            page: self.filter.page(),
            page_size: self.filter.size(),
            total,
            items,
        })
    }
    pub async fn statistics(&mut self) -> Result<Statistics> {
        let total = self.count(true).await?;
        let mut query=self.selection("sum(quota::numeric)::text AS quota,count(quota)::bigint AS stored_count,count(*) FILTER(WHERE created_at>=extract(epoch FROM transaction_timestamp())::bigint-60)::bigint AS rpm,coalesce(sum(prompt_tokens::numeric+completion_tokens::numeric) FILTER(WHERE created_at>=extract(epoch FROM transaction_timestamp())::bigint-60),0)::text AS tpm",true);
        let sums = query.build().fetch_one(self.connection()).await?;
        let raw: Option<String> = sums.try_get("quota")?;
        let quota = if total == 0 {
            Some(0)
        } else if sums.try_get::<i64, _>("stored_count")? != total {
            None
        } else {
            raw.as_deref().map(exact).transpose()?
        };
        let tpm = exact(&sums.try_get::<String, _>("tpm")?)?;
        let mut inputs = Vec::new();
        let mut cursor = 0i64;
        while inputs.len() < MAX_RECORDS && (inputs.len() as i64) < total {
            let mut query = self.selection(
                "id,quota,CASE WHEN octet_length(other)<=65536 THEN other ELSE '' END AS other",
                true,
            );
            query
                .push(" AND id>")
                .push_bind(cursor)
                .push(" ORDER BY id LIMIT ")
                .push_bind(BATCH.min((MAX_RECORDS - inputs.len()) as i64));
            let rows = query.build().fetch_all(self.connection()).await?;
            if rows.is_empty() {
                return Err(ReadError::Unavailable);
            }
            for row in rows {
                cursor = row.try_get("id")?;
                let quota: Option<i64> = row.try_get("quota")?;
                let other: String = row.try_get("other")?;
                let basis = captured_credits_per_usd(&other)
                    .map(|b| format!("{{\"pricing_credits_per_usd\":{b}}}"))
                    .unwrap_or_default();
                inputs.push((quota, basis));
            }
        }
        let projection = project_consumption_statistic(
            inputs.iter().map(|(quota, basis)| LogQuotaInput {
                quota: *quota,
                log_type: 2,
                other: basis,
            }),
            total > MAX_RECORDS as i64,
        );
        Ok(Statistics {
            quota,
            rpm: sums.try_get("rpm")?,
            tpm,
            total,
            projection,
        })
    }
    /// Used by export without retaining opaque metadata or an unbounded result.
    pub async fn export_batch(&mut self, after: (i64, i64), limit: i64) -> Result<Vec<LogRow>> {
        if !(1..=BATCH).contains(&limit) {
            return Err(ReadError::Invalid);
        }
        let mut q = self.selection(COLUMNS, false);
        q.push(" AND log_type IN (1,2,3,4,6) AND (created_at,id)>(")
            .push_bind(after.0)
            .push(",")
            .push_bind(after.1)
            .push(") ORDER BY created_at,id LIMIT ")
            .push_bind(limit);
        let rows: Vec<LogRow> = q.build_query_as().fetch_all(self.connection()).await?;
        for row in &rows {
            row.validate()?;
        }
        Ok(rows)
    }
    pub async fn export_count(&mut self) -> Result<i64> {
        let mut q=self.selection("count(*)::bigint AS total,count(DISTINCT id)::bigint AS ids,count(DISTINCT event_key)::bigint AS events",false);
        q.push(" AND log_type IN (1,2,3,4,6)");
        let row = q.build().fetch_one(self.connection()).await?;
        let count: i64 = row.try_get("total")?;
        if count != row.try_get::<i64, _>("ids")? || count != row.try_get::<i64, _>("events")? {
            return Err(ReadError::Unavailable);
        }
        Ok(count)
    }
    pub fn time_range(&self) -> (i64, i64) {
        (self.filter.start_timestamp, self.filter.end_timestamp)
    }

    pub async fn buckets(&mut self, by_user: bool, flow: bool) -> Result<Vec<serde_json::Value>> {
        if self.credential.is_some() || (by_user && !self.administrator) {
            return Err(ReadError::Forbidden);
        }
        if self.filter.start_timestamp == 0
            || self.filter.end_timestamp == 0
            || (!self.administrator
                && self.filter.end_timestamp - self.filter.start_timestamp > 2_592_000)
            || self.filter.log_type != 0
            || !self.filter.token_name.is_empty()
            || !self.filter.request_id.is_empty()
        {
            return Err(ReadError::Invalid);
        }
        self.require_source("quota_data").await?;
        let group = if flow {
            if self.root {
                "user_id,username,node_name,token_id,token_name,use_group,model_name,channel_id,channel_name"
            } else if self.administrator {
                "user_id,username,use_group,model_name,channel_id,channel_name"
            } else {
                "token_id,token_name,use_group,model_name"
            }
        } else if by_user {
            "user_id,username,created_at"
        } else {
            "created_at,model_name"
        };
        let mut q = QueryBuilder::<Postgres>::new(format!(
            "SELECT {group},CASE WHEN count(quota)=count(*) THEN sum(quota::numeric)::text END AS quota,sum(token_used::numeric)::text AS token_used,sum(count::numeric)::text AS count FROM core_history.quota_data_v1 WHERE created_at>="
        ));
        q.push_bind(self.filter.start_timestamp)
            .push(" AND created_at<=")
            .push_bind(self.filter.end_timestamp);
        if flow {
            q.push(" AND use_group<>''");
        }
        if let Some(user) = self.user {
            q.push(" AND user_id=").push_bind(user);
        }
        if let Some(account) = self.account {
            q.push(" AND owner_account_id=").push_bind(account);
        }
        for (column, value) in [
            ("username", &self.filter.username),
            ("use_group", &self.filter.group),
        ] {
            if !value.is_empty() {
                q.push(format!(" AND {column}=")).push_bind(value.clone());
            }
        }
        if !self.filter.model_name.is_empty() {
            q.push(" AND strpos(model_name,")
                .push_bind(self.filter.model_name.clone())
                .push(")>0");
        }
        if let Some(channel) = self.filter.channel {
            q.push(" AND channel_id=").push_bind(channel);
        }
        let order = if flow {
            format!("sum(quota::numeric) DESC NULLS LAST,{group}")
        } else {
            group.to_owned()
        };
        q.push(format!(" GROUP BY {group} ORDER BY {order} LIMIT 10001"));
        let rows = q.build().fetch_all(self.connection()).await?;
        if rows.len() > MAX_RECORDS {
            return Err(ReadError::Limit);
        }
        let mut output = Vec::with_capacity(rows.len());
        for row in rows {
            let mut value = serde_json::Map::new();
            for name in group.split(',') {
                if ["user_id", "token_id", "channel_id", "created_at"].contains(&name) {
                    value.insert(
                        name.to_owned(),
                        serde_json::json!(row.try_get::<i64, _>(name)?),
                    );
                } else {
                    let text: String = row.try_get(name)?;
                    if text.len() > 1024 {
                        return Err(ReadError::Unavailable);
                    }
                    value.insert(name.to_owned(), serde_json::json!(text));
                }
            }
            let quota: Option<String> = row.try_get("quota")?;
            value.insert(
                "quota".into(),
                serde_json::json!(quota.as_deref().map(exact).transpose()?),
            );
            for name in ["token_used", "count"] {
                value.insert(
                    name.into(),
                    serde_json::json!(exact(&row.try_get::<String, _>(name)?)?),
                );
            }
            value.insert("quota_money_available".into(), serde_json::json!(false));
            value.insert(
                "quota_money_basis".into(),
                serde_json::json!("bucket_without_credit_basis"),
            );
            output.push(serde_json::Value::Object(value));
        }
        Ok(output)
    }
}
fn exact(value: &str) -> Result<i64> {
    let value: i128 = value.parse().map_err(|_| ReadError::Unavailable)?;
    if value.abs() > SAFE_INTEGER {
        return Err(ReadError::Unavailable);
    }
    i64::try_from(value).map_err(|_| ReadError::Unavailable)
}
