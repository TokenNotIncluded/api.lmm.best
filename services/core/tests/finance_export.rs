// SPDX-License-Identifier: AGPL-3.0-or-later
use lmm_core::finance_export::{
    ExportError, MAX_EXPORT_BYTES, MAX_EXPORT_ROWS, UsageExportInput, UsageJsonWriter,
};
use serde_json::Value;
use std::io::{self, Write};

fn row(id: i64, other: &str) -> UsageExportInput<'_> {
    UsageExportInput {
        log_id: id,
        user_id: 7,
        created_at: 1700000000,
        log_type: 2,
        username: "synthetic-user",
        token_name: "synthetic-token",
        model_name: "local-model",
        quota: Some(500000),
        prompt_tokens: 5,
        completion_tokens: 2,
        use_time: 1,
        is_stream: false,
        channel_id: Some(9),
        group: "default",
        other,
    }
}

#[test]
fn export_has_an_explicit_field_allowlist() {
    let input = row(
        1,
        r#"{"pricing_credits_per_usd":500000,"api_key":"SYNTHETIC_SECRET","admin_info":{"user_id":8},"provider_payload":"SYNTHETIC_PROVIDER_DATA"}"#,
    );
    let mut writer = UsageJsonWriter::new(Vec::new(), 10, 10000).unwrap();
    writer.push(&input).unwrap();
    let (bytes, receipt) = writer.finish().unwrap();
    let text = String::from_utf8(bytes).unwrap();
    assert!(!text.contains("SYNTHETIC_SECRET"));
    assert!(!text.contains("SYNTHETIC_PROVIDER_DATA"));
    let decoded: Value = serde_json::from_str(&text).unwrap();
    let mut fields: Vec<_> = decoded[0]
        .as_object()
        .unwrap()
        .keys()
        .map(String::as_str)
        .collect();
    fields.sort_unstable();
    let mut expected = vec![
        "log_id",
        "user_id",
        "created_at",
        "type",
        "username",
        "token_name",
        "model_name",
        "quota",
        "prompt_tokens",
        "completion_tokens",
        "use_time",
        "is_stream",
        "channel_id",
        "group",
        "quota_usd",
        "quota_money_available",
        "quota_money_basis",
    ];
    expected.sort_unstable();
    assert_eq!(fields, expected);
    assert_eq!(decoded[0]["quota"], 500000);
    assert_eq!(decoded[0]["quota_usd"], "1");
    assert_eq!(receipt.rows, 1);
    assert_eq!(receipt.bytes, text.len());
}

#[test]
fn export_null_and_refund_are_not_repriced_as_consumption() {
    let mut null_row = row(1, r#"{"pricing_credits_per_usd":500000}"#);
    null_row.quota = None;
    let mut refund = row(2, r#"{"pricing_credits_per_usd":500000}"#);
    refund.quota = Some(-50);
    refund.log_type = 6;
    let mut writer = UsageJsonWriter::new(Vec::new(), 2, 10000).unwrap();
    writer.push(&null_row).unwrap();
    writer.push(&refund).unwrap();
    let (bytes, _) = writer.finish().unwrap();
    let decoded: Value = serde_json::from_slice(&bytes).unwrap();
    assert!(decoded[0]["quota"].is_null());
    assert_eq!(decoded[0]["quota_money_basis"], "invalid_stored_quota");
    assert_eq!(decoded[1]["quota"], -50);
    assert_eq!(decoded[1]["quota_money_basis"], "not_consumption");
    assert!(decoded[1].get("quota_usd").is_none());
}

#[test]
fn export_round_trips_unicode_quotes_controls_and_formula_text_as_json_strings() {
    let mut input = row(1, "{}");
    input.username = "测试\n\"quote\"\u{0000}";
    input.token_name = "=1+1";
    input.model_name = "+cmd|'/C synthetic'!A0";
    let mut writer = UsageJsonWriter::new(Vec::new(), 1, 10000).unwrap();
    writer.push(&input).unwrap();
    let (bytes, _) = writer.finish().unwrap();
    let decoded: Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(decoded[0]["username"], input.username);
    assert_eq!(decoded[0]["token_name"], "=1+1");
    assert_eq!(decoded[0]["model_name"], input.model_name);
    // This is JSON in a ZIP, not CSV, an XLSX cell or executable JavaScript.
    // A later CSV adapter needs its own formula-injection protection.
}

#[test]
fn export_enforces_row_order_and_duplicate_rejection() {
    let mut writer = UsageJsonWriter::new(Vec::new(), 3, 10000).unwrap();
    writer.push(&row(2, "{}")).unwrap();
    assert_eq!(writer.push(&row(1, "{}")), Err(ExportError::OutOfOrder));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
    let mut writer = UsageJsonWriter::new(Vec::new(), 3, 10000).unwrap();
    writer.push(&row(1, "{}")).unwrap();
    assert_eq!(writer.push(&row(1, "{}")), Err(ExportError::OutOfOrder));
}

#[test]
fn export_limits_cannot_be_raised_past_the_server_ceiling() {
    for (rows, bytes) in [
        (0, 2),
        (1, 1),
        (MAX_EXPORT_ROWS + 1, 100),
        (1, MAX_EXPORT_BYTES + 1),
    ] {
        assert!(matches!(
            UsageJsonWriter::new(Vec::new(), rows, bytes),
            Err(ExportError::InvalidLimits)
        ));
    }
}

#[test]
fn export_row_limit_poisoning_does_not_claim_a_complete_partial_file() {
    let mut writer = UsageJsonWriter::new(Vec::new(), 1, 10000).unwrap();
    writer.push(&row(1, "{}")).unwrap();
    assert_eq!(writer.push(&row(2, "{}")), Err(ExportError::RowLimit));
    assert_eq!(writer.push(&row(3, "{}")), Err(ExportError::Aborted));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
}

#[test]
fn export_byte_limit_reserves_the_closing_bracket() {
    let mut sample = UsageJsonWriter::new(Vec::new(), 1, 10000).unwrap();
    sample.push(&row(1, "{}")).unwrap();
    let (bytes, _) = sample.finish().unwrap();
    let mut exact = UsageJsonWriter::new(Vec::new(), 1, bytes.len()).unwrap();
    exact.push(&row(1, "{}")).unwrap();
    assert_eq!(exact.finish().unwrap().0.len(), bytes.len());
    let mut short = UsageJsonWriter::new(Vec::new(), 1, bytes.len() - 1).unwrap();
    assert_eq!(short.push(&row(1, "{}")), Err(ExportError::ByteLimit));
    assert!(matches!(short.finish(), Err(ExportError::Aborted)));
}

#[test]
fn export_rejects_unbounded_text_before_encoding() {
    let long = "x".repeat(1025);
    let mut input = row(1, "{}");
    input.model_name = &long;
    let mut writer = UsageJsonWriter::new(Vec::new(), 1, 10000).unwrap();
    assert_eq!(writer.push(&input), Err(ExportError::InvalidRow));
}

#[test]
fn export_empty_result_is_a_real_empty_json_array() {
    let writer = UsageJsonWriter::new(Vec::new(), 1, 2).unwrap();
    let (bytes, receipt) = writer.finish().unwrap();
    assert_eq!(bytes, b"[]");
    assert_eq!(receipt.rows, 0);
    assert_eq!(receipt.bytes, 2);
}

struct BrokenSink {
    remaining: usize,
}
impl Write for BrokenSink {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        if self.remaining == 0 {
            return Err(io::Error::new(
                io::ErrorKind::BrokenPipe,
                "synthetic disconnect",
            ));
        }
        let written = self.remaining.min(bytes.len());
        self.remaining -= written;
        Ok(written)
    }
    fn flush(&mut self) -> io::Result<()> {
        Ok(())
    }
}

#[test]
fn export_writer_failure_cannot_be_retried_as_success() {
    let mut writer = UsageJsonWriter::new(BrokenSink { remaining: 10 }, 2, 10000).unwrap();
    assert_eq!(writer.push(&row(1, "{}")), Err(ExportError::Io));
    assert_eq!(writer.push(&row(2, "{}")), Err(ExportError::Aborted));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
}

struct FailingSink {
    writes: usize,
    fail_on_write: usize,
    kind: io::ErrorKind,
    fail_flush: bool,
}
impl Write for FailingSink {
    fn write(&mut self, bytes: &[u8]) -> io::Result<usize> {
        self.writes += 1;
        if self.writes == self.fail_on_write {
            return Err(io::Error::from(self.kind));
        }
        Ok(bytes.len())
    }
    fn flush(&mut self) -> io::Result<()> {
        if self.fail_flush {
            Err(io::Error::from(self.kind))
        } else {
            Ok(())
        }
    }
}

#[test]
fn export_distinguishes_sink_limits_from_other_io_failures() {
    for (kind, expected) in [
        (io::ErrorKind::FileTooLarge, ExportError::ByteLimit),
        (io::ErrorKind::StorageFull, ExportError::ByteLimit),
        (io::ErrorKind::QuotaExceeded, ExportError::ByteLimit),
        (io::ErrorKind::BrokenPipe, ExportError::Io),
        (io::ErrorKind::PermissionDenied, ExportError::Io),
    ] {
        for fail_on_write in 1..=3 {
            let sink = FailingSink {
                writes: 0,
                fail_on_write,
                kind,
                fail_flush: false,
            };
            let result = UsageJsonWriter::new(sink, 10, 10000);
            if fail_on_write == 1 {
                assert!(matches!(result, Err(error) if error == expected));
                continue;
            }
            let mut writer = result.unwrap();
            let result = writer.push(&row(1, "{}"));
            if fail_on_write == 2 {
                assert_eq!(result, Err(expected));
                assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
            } else {
                result.unwrap();
                assert!(matches!(writer.finish(), Err(error) if error == expected));
            }
        }
        let sink = FailingSink {
            writes: 0,
            fail_on_write: usize::MAX,
            kind,
            fail_flush: true,
        };
        let writer = UsageJsonWriter::new(sink, 10, 10000).unwrap();
        assert!(matches!(writer.finish(), Err(error) if error == expected));
    }
}
