//! Complete ZIP assembly, bounded across every document. Uses only the
//! authenticated snapshot and fixed document/field names. Never downloads
//! upstream data or fetches current FX to value historical records.
use super::{ExportError, MAX_EXPORT_BYTES, MAX_EXPORT_ROWS, UsageExportInput, UsageJsonWriter};
use crate::history_read::repository::{HistoryRead, ReadError, Result};
use serde_json::{Value, json};
use sqlx::Row;
use std::{
    collections::BTreeMap,
    io::{self, Cursor, Seek, SeekFrom, Write},
};
use zip::{CompressionMethod, ZipWriter, write::SimpleFileOptions};

/// Unknown source fields never enter an exported record. These are the main
/// finance DTO field names, not the underlying tables' entire JSON records.
pub struct Dataset {
    pub name: &'static str,
    pub fields: &'static str,
    pub timed: bool,
}
pub const DATASETS: &[Dataset] = &[
    Dataset {
        name: "users-balances",
        fields: "user_id username role status group quota used_quota affiliate_quota affiliate_history_quota request_count created_at last_api_activity_at trust_level_override normalized_used_quota usage_projection_available usage_projection_reason effective_group_ratio effective_topup_group_ratio",
        timed: false,
    },
    Dataset {
        name: "channels-pricing",
        fields: "channel_id type status name weight created_time test_time response_time base_url balance balance_updated_time models model_mapping group used_quota priority auto_ban tag",
        timed: false,
    },
    Dataset {
        name: "subscription-plans",
        fields: "plan_id title subtitle price_amount currency duration_unit duration_value custom_seconds enabled sort_order allow_balance_pay allow_wallet_overflow max_purchase_per_user upgrade_group downgrade_group total_amount quota_reset_period quota_reset_custom_seconds created_at updated_at",
        timed: false,
    },
    Dataset {
        name: "topups",
        fields: "topup_id user_id amount credited_quota expected_amount_micros settled_amount_micros settlement_currency money payment_method payment_provider create_time complete_time status",
        timed: true,
    },
    Dataset {
        name: "subscription-orders",
        fields: "order_id user_id plan_id plan_price plan_currency expected_amount_micros settlement_currency payment_method payment_provider provider_product_id provider_subscription_state current_period_start current_period_end refunded_amount_micros status create_time complete_time",
        timed: true,
    },
    Dataset {
        name: "subscription-payment-events",
        fields: "payment_event_id subscription_order_id payment_provider settlement_currency settlement_amount_micros period_start period_end created_time",
        timed: true,
    },
    Dataset {
        name: "bounty-ledger",
        fields: "ledger_id project_id challenge_id user_id counterparty_user_id kind quota note created_at",
        timed: true,
    },
    Dataset {
        name: "checkins",
        fields: "checkin_id user_id checkin_date quota_awarded created_at",
        timed: true,
    },
    Dataset {
        name: "redemptions",
        fields: "redemption_id created_by_user_id status name quota created_time redeemed_time used_user_id expired_time",
        timed: false,
    },
    Dataset {
        name: "user-subscriptions",
        fields: "user_subscription_id user_id plan_id amount_total amount_used start_time end_time status source last_reset_time next_reset_time upgrade_group prev_user_group downgrade_group allow_wallet_overflow created_at updated_at",
        timed: false,
    },
];
const OPTIONS: &[&str] = &[
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
const DERIVED: &[(&str, &str)] = &[
    ("model_prices", "ModelPrice"),
    ("model_ratios", "ModelRatio"),
    ("completion_ratios", "CompletionRatio"),
    ("cache_ratios", "CacheRatio"),
    ("create_cache_ratios", "CreateCacheRatio"),
    ("image_ratios", "ImageRatio"),
    ("audio_ratios", "AudioRatio"),
    ("audio_completion_ratios", "AudioCompletionRatio"),
    ("tool_prices", "tool_price_setting.prices"),
    ("billing_modes", "billing_setting.billing_mode"),
    ("billing_expressions", "billing_setting.billing_expr"),
];
const EFFECTIVE_FIELDS: &str = "model_name quota_type model_ratio model_price completion_ratio cache_ratio create_cache_ratio image_ratio audio_ratio audio_completion_ratio enable_groups supported_endpoint_types";

struct BoundedCursor {
    inner: Cursor<Vec<u8>>,
    maximum: u64,
}
impl Write for BoundedCursor {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        let end = self
            .inner
            .position()
            .checked_add(bytes.len() as u64)
            .ok_or_else(|| io::Error::new(io::ErrorKind::FileTooLarge, "export limit"))?;
        if end > self.maximum {
            return Err(io::Error::new(io::ErrorKind::FileTooLarge, "export limit"));
        }
        self.inner.write(bytes)
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}
impl Seek for BoundedCursor {
    fn seek(&mut self, to: SeekFrom) -> io::Result<u64> {
        let position = match to {
            SeekFrom::Start(n) => i128::from(n),
            SeekFrom::Current(n) => i128::from(self.inner.position()) + i128::from(n),
            SeekFrom::End(n) => self.inner.get_ref().len() as i128 + i128::from(n),
        };
        if position < 0 {
            return Err(io::Error::from(io::ErrorKind::InvalidInput));
        }
        if position > i128::from(self.maximum) {
            return Err(io::Error::new(io::ErrorKind::FileTooLarge, "export limit"));
        }
        self.inner.seek(SeekFrom::Start(position as u64))
    }
}
fn export_error(error: ExportError) -> ReadError {
    match error {
        ExportError::RowLimit | ExportError::ByteLimit => ReadError::Limit,
        ExportError::InvalidLimits => ReadError::Invalid,
        _ => ReadError::Unavailable,
    }
}
fn io_error(error: io::Error) -> ReadError {
    export_error(ExportError::from(error))
}
fn zip_error(error: zip::result::ZipError) -> ReadError {
    match error {
        zip::result::ZipError::Io(error) => io_error(error),
        _ => ReadError::Unavailable,
    }
}
fn start(zip: &mut ZipWriter<BoundedCursor>, name: &str) -> Result<()> {
    zip.start_file(
        format!("{name}.json"),
        SimpleFileOptions::default()
            .compression_method(CompressionMethod::Stored)
            .unix_permissions(0o600),
    )
    .map_err(zip_error)
}
fn write_json(zip: &mut ZipWriter<BoundedCursor>, value: &Value) -> Result<()> {
    serde_json::to_writer(zip, value).map_err(|error| match error.io_error_kind() {
        Some(kind) => io_error(io::Error::from(kind)),
        None => ReadError::Unavailable,
    })
}

/// Strip every non-allowlisted field before serializing; reject structured
/// values in scalar fields instead of exporting a nested provider payloads.
pub fn project_record(dataset: &Dataset, record: &Value) -> Result<Value> {
    let object = record.as_object().ok_or(ReadError::Unavailable)?;
    let mut output = serde_json::Map::new();
    for key in dataset.fields.split_whitespace() {
        let Some(value) = object.get(key) else {
            continue;
        };
        match value {
            Value::Null | Value::Bool(_) => {}
            Value::Number(n) if n.to_string().len() <= 64 => {}
            Value::String(text) if text.len() <= 4096 => {}
            Value::Array(items)
                if dataset.name == "effective-model-pricing"
                    && matches!(key, "enable_groups" | "supported_endpoint_types")
                    && items.len() <= 64
                    && items
                        .iter()
                        .all(|item| item.as_str().is_some_and(|s| s.len() <= 256)) => {}
            _ => return Err(ReadError::Unavailable),
        }
        output.insert(key.to_owned(), value.clone());
    }
    if dataset.name == "channels-pricing" {
        // URL parsing is local only. Userinfo, paths, query and fragment never
        // enter the archive, including percent-encoded credential forms.
        let value = output
            .remove("base_url")
            .and_then(|v| v.as_str().map(str::to_owned))
            .and_then(|v| reqwest::Url::parse(&v).ok())
            .filter(|v| matches!(v.scheme(), "http" | "https") && v.host().is_some())
            .map(|v| Value::String(v.origin().ascii_serialization()));
        if let Some(value) = value {
            output.insert("base_url".into(), value);
        }
    }
    if dataset.name == "users-balances"
        && output.get("usage_projection_available") != Some(&Value::Bool(true))
    {
        output.remove("normalized_used_quota");
        output.insert("usage_projection_available".into(), Value::Bool(false));
    }
    Ok(Value::Object(output))
}

pub async fn build(read: &mut HistoryRead<'_>, max_bytes: usize) -> Result<Vec<u8>> {
    if !read.administrator() {
        return Err(ReadError::Forbidden);
    }
    if !(1024..=MAX_EXPORT_BYTES).contains(&max_bytes) {
        return Err(ReadError::Invalid);
    }
    let (begin, end) = read.time_range();
    if begin <= 0 || end < begin || end - begin > 31_536_000 {
        return Err(ReadError::Invalid);
    }
    // All required datasets must be qualified. Missing datasets are not empty
    // arrays, and cannot silently produce a convincing partial ZIP.
    for name in DATASETS
        .iter()
        .map(|d| d.name)
        .chain(["financial-options", "effective-model-pricing"])
    {
        read.require_source(name).await?;
    }
    let mut zip = ZipWriter::new(BoundedCursor {
        inner: Cursor::new(Vec::new()),
        maximum: max_bytes as u64,
    });
    let mut counts = BTreeMap::new();
    let mut total = 0usize;
    for dataset in DATASETS {
        let count = write_dataset(read, &mut zip, dataset, begin, end, &mut total).await?;
        counts.insert(dataset.name, count);
    }
    let usage_count = usize::try_from(read.export_count().await?).map_err(|_| ReadError::Limit)?;
    if total
        .checked_add(usage_count)
        .is_none_or(|v| v > MAX_EXPORT_ROWS)
    {
        return Err(ReadError::Limit);
    }
    start(&mut zip, "usage-billing-records")?;
    let mut usage =
        UsageJsonWriter::new(&mut zip, MAX_EXPORT_ROWS, max_bytes).map_err(export_error)?;
    let mut after = (-1, 0);
    let mut seen = 0;
    loop {
        let rows = read.export_batch(after, 64).await?;
        if rows.is_empty() {
            break;
        }
        for row in rows {
            usage
                .push(&UsageExportInput {
                    log_id: row.id,
                    user_id: row.user_id,
                    created_at: row.created_at,
                    log_type: row.log_type,
                    username: &row.username,
                    token_name: &row.token_name,
                    model_name: &row.model_name,
                    quota: row.quota,
                    prompt_tokens: row.prompt_tokens,
                    completion_tokens: row.completion_tokens,
                    use_time: row.use_time,
                    is_stream: row.is_stream,
                    channel_id: row.channel_id,
                    group: &row.group_name,
                    other: &row.other,
                })
                .map_err(export_error)?;
            after = (row.created_at, row.id);
            seen += 1;
        }
    }
    if seen != usage_count {
        return Err(ReadError::Unavailable);
    }
    usage.finish().map_err(export_error)?;
    counts.insert("usage", seen);
    total += seen;
    let rows=sqlx::query("SELECT name,CASE WHEN octet_length(value)<=65536 THEN value END AS value FROM core_history.finance_options_v1 WHERE name=ANY($1) ORDER BY name LIMIT 64")
        .bind(OPTIONS).fetch_all(read.connection()).await?;
    let mut options = serde_json::Map::new();
    for row in rows {
        let name: String = row.try_get("name")?;
        let value: String = row.try_get("value")?;
        if value.len() > 65_536 || options.insert(name, Value::String(value)).is_some() {
            return Err(ReadError::Unavailable);
        }
    }
    counts.insert("financial-options", options.len());
    start(&mut zip, "financial-options")?;
    write_json(&mut zip, &Value::Object(options.clone()))?;
    let mut derived = serde_json::Map::new();
    for (field, key) in DERIVED {
        let value = options
            .get(*key)
            .and_then(Value::as_str)
            .map(serde_json::from_str::<Value>)
            .transpose()
            .map_err(|_| ReadError::Unavailable)?;
        derived.insert((*field).into(), value.unwrap_or(Value::Null));
    }
    start(&mut zip, "model-prices-and-ratios")?;
    write_json(&mut zip, &Value::Object(derived))?;
    // Effective prices use a separate source contract; they are not a fallback
    // for missing historical money. Reuse the same field whitelist gate.
    let effective = Dataset {
        name: "effective-model-pricing",
        fields: EFFECTIVE_FIELDS,
        timed: false,
    };
    let effective_count = write_dataset(read, &mut zip, &effective, begin, end, &mut total).await?;
    counts.insert("effective-model-pricing", effective_count);
    start(&mut zip, "manifest")?;
    let generated: i64 =
        sqlx::query_scalar("SELECT extract(epoch FROM transaction_timestamp())::bigint")
            .fetch_one(read.connection())
            .await?;
    write_json(
        &mut zip,
        &json!({"schema_version":"lmm-finance-export/v1","generated_at":generated,
        "start_timestamp":begin,"end_timestamp":end,"rows":counts,"truncated":{},
        "redactions":["credentials, provider payloads, IPs, raw requests, opaque log metadata, URL credentials and query strings"],
        "notes":["Raw quota is unchanged. Unknown historical money is not zero.","All datasets use one authorized read-only snapshot. Current pricing is not historical evidence."]}),
    )?;
    let output = zip
        .finish()
        .map_err(zip_error)?
        .inner
        .into_inner();
    Ok(output)
}

async fn write_dataset(
    read: &mut HistoryRead<'_>,
    zip: &mut ZipWriter<BoundedCursor>,
    dataset: &Dataset,
    begin: i64,
    end: i64,
    total: &mut usize,
) -> Result<usize> {
    let count=sqlx::query("SELECT count(*)::bigint AS n,count(DISTINCT id)::bigint AS ids FROM core_history.finance_records_v1 WHERE dataset=$1 AND (NOT $4 OR created_at BETWEEN $2 AND $3)")
        .bind(dataset.name).bind(begin).bind(end).bind(dataset.timed).fetch_one(read.connection()).await?;
    let n: i64 = count.try_get("n")?;
    if n != count.try_get::<i64, _>("ids")? {
        return Err(ReadError::Unavailable);
    }
    let expected = usize::try_from(n).map_err(|_| ReadError::Limit)?;
    if total
        .checked_add(expected)
        .is_none_or(|n| n > MAX_EXPORT_ROWS)
    {
        return Err(ReadError::Limit);
    }
    start(zip, dataset.name)?;
    zip.write_all(b"[").map_err(io_error)?;
    let mut last = 0i64;
    let mut seen = 0usize;
    let fields: Vec<&str> = dataset.fields.split_whitespace().collect();
    while seen < expected {
        let rows=sqlx::query("SELECT id,CASE WHEN octet_length(record::text)<=32768 THEN (SELECT jsonb_object_agg(k,record->k) FROM unnest($5::text[]) k WHERE record ? k) END AS record FROM core_history.finance_records_v1 WHERE dataset=$1 AND id>$2 AND (NOT $6 OR created_at BETWEEN $3 AND $4) ORDER BY id LIMIT 64")
            .bind(dataset.name).bind(last).bind(begin).bind(end).bind(&fields).bind(dataset.timed)
            .fetch_all(read.connection()).await?;
        if rows.is_empty() {
            return Err(ReadError::Unavailable);
        }
        for row in rows {
            let id: i64 = row.try_get("id")?;
            if id <= last || seen == expected {
                return Err(ReadError::Unavailable);
            }
            last = id;
            let record: Option<Value> = row.try_get("record")?;
            let record = project_record(dataset, &record.ok_or(ReadError::Unavailable)?)?;
            if seen > 0 {
                zip.write_all(b",").map_err(io_error)?;
            }
            write_json(zip, &record)?;
            seen += 1;
        }
    }
    zip.write_all(b"]\n").map_err(io_error)?;
    *total += seen;
    Ok(seen)
}
