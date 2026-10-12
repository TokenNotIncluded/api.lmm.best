// SPDX-License-Identifier: AGPL-3.0-or-later
//! UTF-8 CSV from the same allowlist as JSON. Raw source rows are never serialized.
use super::{
    BoundedRow, ExportError, ExportReceipt, MAX_EXPORT_BYTES, MAX_EXPORT_METADATA_BYTES,
    MAX_EXPORT_ROWS, UsageExportInput, UsageExportRow, validate_row,
};
use std::io::Write;

const COLUMNS: [&str; 17] = [
    "log_id", "user_id", "created_at", "type", "username", "token_name", "model_name",
    "quota", "prompt_tokens", "completion_tokens", "use_time", "is_stream", "channel_id",
    "group", "quota_usd", "quota_money_available", "quota_money_basis",
];

/// The caller discards its spool on ANY error. CSV has no end marker, so the
/// receipt, not whether an early row can be parsed, determines completion.
pub struct UsageCsvWriter<W: Write> {
    writer: W,
    max_rows: usize,
    max_bytes: usize,
    rows: usize,
    bytes: usize,
    metadata_bytes: usize,
    last_order: Option<(i64, i64)>,
    failed: bool,
}

impl<W: Write> UsageCsvWriter<W> {
    pub fn new(mut writer: W, max_rows: usize, max_bytes: usize, utf8_bom: bool) -> Result<Self, ExportError> {
        if max_rows == 0 || max_rows > MAX_EXPORT_ROWS || max_bytes > MAX_EXPORT_BYTES {
            return Err(ExportError::InvalidLimits);
        }
        let mut header = BoundedRow { bytes: Vec::new() };
        if utf8_bom {
            header.write_all(b"\xef\xbb\xbf").map_err(|_| ExportError::Encoding)?;
        }
        let columns: Vec<_> = COLUMNS.iter().map(|name| (*name, false)).collect();
        encode_record(&mut header, &columns)?;
        if header.bytes.len() > max_bytes {
            return Err(ExportError::InvalidLimits);
        }
        writer.write_all(&header.bytes).map_err(|_| ExportError::Io)?;
        Ok(Self {
            writer, max_rows, max_bytes, rows: 0, bytes: header.bytes.len(),
            metadata_bytes: 0, last_order: None, failed: false,
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
        validate_row(row)?;
        // JSON may safely escape NUL; CSV clients can truncate at it.
        if [row.username, row.token_name, row.model_name, row.group]
            .into_iter()
            .any(|value| value.contains('\0'))
        {
            return Err(ExportError::InvalidRow);
        }
        let order = (row.created_at, row.log_id);
        if self.last_order.is_some_and(|last| order <= last) {
            return Err(ExportError::OutOfOrder);
        }
        let metadata_bytes = self.metadata_bytes.checked_add(row.other.len())
            .filter(|bytes| *bytes <= MAX_EXPORT_METADATA_BYTES).ok_or(ExportError::ByteLimit)?;
        // The bounded serializer runs BEFORE Value conversion. This guarantees
        // the map allocation cannot contain opaque or unlimited source metadata.
        let mut json = BoundedRow { bytes: Vec::new() };
        serde_json::to_writer(&mut json, &UsageExportRow::from(row)).map_err(|_| ExportError::Encoding)?;
        let value: serde_json::Value = serde_json::from_slice(&json.bytes).map_err(|_| ExportError::Encoding)?;
        let mut strings = Vec::with_capacity(COLUMNS.len());
        for key in COLUMNS {
            strings.push(match &value[key] {
                serde_json::Value::Null => (String::new(), false),
                serde_json::Value::String(text) => (text.clone(), true),
                serde_json::Value::Number(number) => (number.to_string(), false),
                serde_json::Value::Bool(flag) => (flag.to_string(), false),
                _ => return Err(ExportError::Encoding),
            });
        }
        let cells: Vec<_> = strings.iter().map(|(text, protect)| (text.as_str(), *protect)).collect();
        let mut encoded = BoundedRow { bytes: Vec::new() };
        encode_record(&mut encoded, &cells)?;
        let bytes = self.bytes.checked_add(encoded.bytes.len())
            .filter(|bytes| *bytes <= self.max_bytes).ok_or(ExportError::ByteLimit)?;
        self.writer.write_all(&encoded.bytes).map_err(|_| ExportError::Io)?;
        self.bytes = bytes;
        self.rows += 1;
        self.metadata_bytes = metadata_bytes;
        self.last_order = Some(order);
        Ok(())
    }

    pub fn finish(mut self) -> Result<(W, ExportReceipt), ExportError> {
        if self.failed {
            return Err(ExportError::Aborted);
        }
        self.writer.flush().map_err(|_| ExportError::Io)?;
        Ok((self.writer, ExportReceipt { rows: self.rows, bytes: self.bytes }))
    }
}

fn encode_record(writer: &mut BoundedRow, cells: &[(&str, bool)]) -> Result<(), ExportError> {
    for (index, (cell, protect)) in cells.iter().enumerate() {
        if index != 0 {
            writer.write_all(b",").map_err(|_| ExportError::Encoding)?;
        }
        writer.write_all(b"\"").map_err(|_| ExportError::Encoding)?;
        let visible = cell.trim_start_matches(|c: char| c.is_whitespace() || c.is_control() || c == '\u{feff}');
        // Quoting alone does not stop spreadsheet formulas. Prefix before the
        // ORIGINAL text; never remove or normalize raw stored text for JSON.
        if *protect && (visible.starts_with(['=', '+', '-', '@']) || cell.starts_with(['\t', '\r', '\n'])) {
            writer.write_all(b"'").map_err(|_| ExportError::Encoding)?;
        }
        for part in cell.split_inclusive('"') {
            writer.write_all(part.as_bytes()).map_err(|_| ExportError::Encoding)?;
            if part.ends_with('"') {
                writer.write_all(b"\"").map_err(|_| ExportError::Encoding)?;
            }
        }
        writer.write_all(b"\"").map_err(|_| ExportError::Encoding)?;
    }
    writer.write_all(b"\r\n").map_err(|_| ExportError::Encoding)
}
