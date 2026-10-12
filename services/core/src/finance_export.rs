// SPDX-License-Identifier: AGPL-3.0-or-later
//! Allowlisted usage JSON for the finance ZIP. This is not an HTTP endpoint or
//! a complete finance bundle. The caller must authorize and select rows under
//! the same read snapshot; no raw storage object implements Serialize here.
pub mod archive;

use crate::history_read::{LogQuotaInput, QuotaMoneyProjection, project_log};
use serde::Serialize;
use std::io::{self, Write};

pub const MAX_EXPORT_ROWS: usize = 200_000;
pub const MAX_EXPORT_BYTES: usize = 64 * 1024 * 1024;
pub const MAX_EXPORT_ROW_BYTES: usize = 32 * 1024;
const MAX_TEXT_BYTES: usize = 1024;

/// Deliberately no Serialize/Debug: `other` can contain private provider data.
pub struct UsageExportInput<'a> {
    pub log_id: i64,
    pub user_id: i64,
    pub created_at: i64,
    pub log_type: i32,
    pub username: &'a str,
    pub token_name: &'a str,
    pub model_name: &'a str,
    pub quota: Option<i64>,
    pub prompt_tokens: i64,
    pub completion_tokens: i64,
    pub use_time: i64,
    pub is_stream: bool,
    pub channel_id: Option<i64>,
    pub group: &'a str,
    pub other: &'a str,
}

#[derive(Serialize)]
struct UsageExportRow<'a> {
    #[serde(flatten)]
    money: QuotaMoneyProjection,
    log_id: i64,
    user_id: i64,
    created_at: i64,
    #[serde(rename = "type")]
    log_type: i32,
    username: &'a str,
    token_name: &'a str,
    model_name: &'a str,
    // Preserve NULL as NULL. Never manufacture a zero stored quota.
    quota: Option<i64>,
    prompt_tokens: i64,
    completion_tokens: i64,
    use_time: i64,
    is_stream: bool,
    channel_id: Option<i64>,
    group: &'a str,
}

impl<'a> From<&UsageExportInput<'a>> for UsageExportRow<'a> {
    fn from(row: &UsageExportInput<'a>) -> Self {
        Self {
            money: project_log(LogQuotaInput {
                quota: row.quota,
                log_type: row.log_type,
                other: row.other,
            }),
            log_id: row.log_id,
            user_id: row.user_id,
            created_at: row.created_at,
            log_type: row.log_type,
            username: row.username,
            token_name: row.token_name,
            model_name: row.model_name,
            quota: row.quota,
            prompt_tokens: row.prompt_tokens,
            completion_tokens: row.completion_tokens,
            use_time: row.use_time,
            is_stream: row.is_stream,
            channel_id: row.channel_id,
            group: row.group,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum ExportError {
    InvalidLimits,
    InvalidRow,
    RowLimit,
    ByteLimit,
    OutOfOrder,
    Encoding,
    Io,
    Aborted,
}

impl From<io::Error> for ExportError {
    fn from(error: io::Error) -> Self {
        match error.kind() {
            io::ErrorKind::FileTooLarge
            | io::ErrorKind::StorageFull
            | io::ErrorKind::QuotaExceeded => Self::ByteLimit,
            _ => Self::Io,
        }
    }
}

#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub struct ExportReceipt {
    pub rows: usize,
    pub bytes: usize,
}

/// Bounded UTF-8 JSON array writer. Input is pull-driven, so no producer task or
/// unbounded queue is needed. Use an owned 0600 spool file in integration, not
/// a live network writer while holding a database transaction.
///
/// An error poisons the writer: finish cannot turn an incomplete export into a
/// valid-looking complete array. Delete only the caller's own failed spool.
pub struct UsageJsonWriter<W: Write> {
    writer: W,
    max_rows: usize,
    max_bytes: usize,
    rows: usize,
    bytes: usize,
    last_order: Option<(i64, i64)>,
    failed: bool,
}

impl<W: Write> UsageJsonWriter<W> {
    pub fn new(mut writer: W, max_rows: usize, max_bytes: usize) -> Result<Self, ExportError> {
        if max_rows == 0
            || max_rows > MAX_EXPORT_ROWS
            || !(2..=MAX_EXPORT_BYTES).contains(&max_bytes)
        {
            return Err(ExportError::InvalidLimits);
        }
        writer.write_all(b"[").map_err(ExportError::from)?;
        Ok(Self {
            writer,
            max_rows,
            max_bytes,
            rows: 0,
            bytes: 1,
            last_order: None,
            failed: false,
        })
    }

    pub fn push(&mut self, row: &UsageExportInput<'_>) -> Result<(), ExportError> {
        if self.failed {
            return Err(ExportError::Aborted);
        }
        let result = self.push_checked(row);
        if result.is_err() {
            self.failed = true;
        }
        result
    }

    fn push_checked(&mut self, row: &UsageExportInput<'_>) -> Result<(), ExportError> {
        if self.rows == self.max_rows {
            return Err(ExportError::RowLimit);
        }
        if row.log_id <= 0
            || row.user_id <= 0
            || row.created_at < 0
            || ![1, 2, 3, 4, 6].contains(&row.log_type)
            || [row.username, row.token_name, row.model_name, row.group]
                .into_iter()
                .any(|value| value.len() > MAX_TEXT_BYTES)
        {
            return Err(ExportError::InvalidRow);
        }
        let order = (row.created_at, row.log_id);
        if self.last_order.is_some_and(|last| order <= last) {
            return Err(ExportError::OutOfOrder);
        }
        let mut encoded = BoundedRow { bytes: Vec::new() };
        serde_json::to_writer(&mut encoded, &UsageExportRow::from(row))
            .map_err(|error| match error.io_error_kind() {
                Some(kind) => ExportError::from(io::Error::from(kind)),
                None => ExportError::Encoding,
            })?;
        let separator = usize::from(self.rows > 0);
        let required = self
            .bytes
            .checked_add(separator)
            .and_then(|value| value.checked_add(encoded.bytes.len()))
            .and_then(|value| value.checked_add(1)) // Reserve the closing bracket.
            .ok_or(ExportError::ByteLimit)?;
        if required > self.max_bytes {
            return Err(ExportError::ByteLimit);
        }
        if separator != 0 {
            self.writer.write_all(b",").map_err(ExportError::from)?;
        }
        self.writer
            .write_all(&encoded.bytes)
            .map_err(ExportError::from)?;
        self.bytes = required - 1;
        self.rows += 1;
        self.last_order = Some(order);
        Ok(())
    }

    /// The outer ZIP manifest must separately mark a deliberate source row
    /// truncation after fetching a cap+1 sentinel in the same snapshot.
    pub fn finish(mut self) -> Result<(W, ExportReceipt), ExportError> {
        if self.failed {
            return Err(ExportError::Aborted);
        }
        self.writer.write_all(b"]").map_err(ExportError::from)?;
        self.writer.flush().map_err(ExportError::from)?;
        Ok((
            self.writer,
            ExportReceipt {
                rows: self.rows,
                bytes: self.bytes + 1,
            },
        ))
    }
}

struct BoundedRow {
    bytes: Vec<u8>,
}

impl Write for BoundedRow {
    fn write(&mut self, data: &[u8]) -> io::Result<usize> {
        if data.len() > MAX_EXPORT_ROW_BYTES.saturating_sub(self.bytes.len()) {
            return Err(io::Error::new(
                io::ErrorKind::FileTooLarge,
                "finance export row limit",
            ));
        }
        self.bytes.extend_from_slice(data);
        Ok(data.len())
    }

    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}
