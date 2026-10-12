// SPDX-License-Identifier: AGPL-3.0-or-later
use super::{BrokenSink, row};
use lmm_core::finance_export::{
    ExportError, MAX_EXPORT_METADATA_BYTES, UsageJsonWriter, csv::UsageCsvWriter,
};

#[test]
fn csv_quotes_unicode_commas_quotes_and_embedded_newlines() {
    let mut input = row(1, r#"{"pricing_credits_per_usd":500000,"secret":"PRIVATE"}"#);
    input.username = "用户,\"test\"\nline";
    let mut writer = UsageCsvWriter::new(Vec::new(), 10, 10_000, true).unwrap();
    writer.push(&input).unwrap();
    let (bytes, receipt) = writer.finish().unwrap();
    assert!(bytes.starts_with(b"\xef\xbb\xbf"));
    assert_eq!(receipt.rows, 1);
    assert_eq!(receipt.bytes, bytes.len());
    let text = String::from_utf8(bytes).unwrap();
    assert!(text.contains("\"用户,\"\"test\"\"\nline\""));
    assert!(text.ends_with("\r\n"));
    assert!(!text.contains("PRIVATE"));
    assert!(!text.contains("secret"));
}

#[test]
fn csv_formula_guard_cannot_be_bypassed_by_leading_space_or_control() {
    for text in ["=1+1", "+1+1", "-1+1", "@SUM(A1)", "  =1", "\t=1", "\r@SUM(A1)", "\n+1", "\u{feff}=1"] {
        let mut input = row(1, "{}");
        input.username = text;
        let mut writer = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
        writer.push(&input).unwrap();
        let (bytes, _) = writer.finish().unwrap();
        let decoded = String::from_utf8(bytes).unwrap();
        assert!(decoded.contains(&format!("\"'{text}\"")), "{text:?}");
        assert_eq!(input.username, text);
        let mut json = UsageJsonWriter::new(Vec::new(), 10, 10_000).unwrap();
        json.push(&input).unwrap();
        let value: serde_json::Value = serde_json::from_slice(&json.finish().unwrap().0).unwrap();
        assert_eq!(value[0]["username"], text);
    }
}

#[test]
fn csv_does_not_change_a_typed_negative_raw_quota_into_a_formula_or_zero() {
    let mut input = row(1, "{}");
    input.log_type = 3;
    input.quota = Some(-17);
    let mut writer = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
    writer.push(&input).unwrap();
    let text = String::from_utf8(writer.finish().unwrap().0).unwrap();
    assert!(text.contains(",\"-17\","));
    assert!(!text.contains("\"'-17\""));
    assert!(!text.contains("captured_credits_per_usd"));
}

#[test]
fn csv_unknown_money_is_blank_with_an_explicit_unavailable_flag() {
    let mut writer = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
    writer.push(&row(1, "{}")).unwrap();
    let text = String::from_utf8(writer.finish().unwrap().0).unwrap();
    assert!(text.ends_with("\"\",\"false\",\"missing_credit_basis\"\r\n"));
}

#[test]
fn csv_limits_and_duplicate_order_abort_the_whole_export() {
    let mut writer = UsageCsvWriter::new(Vec::new(), 1, 10_000, false).unwrap();
    writer.push(&row(1, "{}")).unwrap();
    assert_eq!(writer.push(&row(2, "{}")), Err(ExportError::RowLimit));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
    let mut writer = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
    writer.push(&row(1, "{}")).unwrap();
    assert_eq!(writer.push(&row(1, "{}")), Err(ExportError::OutOfOrder));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
    assert!(matches!(UsageCsvWriter::new(Vec::new(), 10, 1, false), Err(ExportError::InvalidLimits)));
}

#[test]
fn csv_final_byte_and_header_bom_are_included_in_the_limit() {
    let mut complete = UsageCsvWriter::new(Vec::new(), 1, 10_000, true).unwrap();
    complete.push(&row(1, "{}")).unwrap();
    let expected = complete.finish().unwrap().0;
    let mut exact = UsageCsvWriter::new(Vec::new(), 1, expected.len(), true).unwrap();
    exact.push(&row(1, "{}")).unwrap();
    assert_eq!(exact.finish().unwrap().0, expected);
    let mut small = UsageCsvWriter::new(Vec::new(), 1, expected.len() - 1, true).unwrap();
    assert_eq!(small.push(&row(1, "{}")), Err(ExportError::ByteLimit));
    assert!(matches!(small.finish(), Err(ExportError::Aborted)));
}

#[test]
fn csv_io_failure_never_produces_a_completion_receipt() {
    let header = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap().finish().unwrap().0;
    let mut writer = UsageCsvWriter::new(BrokenSink { remaining: header.len() + 5 }, 10, 10_000, false).unwrap();
    assert_eq!(writer.push(&row(1, "{}")), Err(ExportError::Io));
    assert_eq!(writer.push(&row(2, "{}")), Err(ExportError::Aborted));
    assert!(matches!(writer.finish(), Err(ExportError::Aborted)));
}

#[test]
fn both_formats_bound_total_metadata_work_and_reject_invalid_scalars() {
    let padding = "x".repeat(MAX_EXPORT_METADATA_BYTES + 1);
    let input = row(1, &padding);
    let mut csv = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
    assert_eq!(csv.push(&input), Err(ExportError::ByteLimit));
    let mut json = UsageJsonWriter::new(Vec::new(), 10, 10_000).unwrap();
    assert_eq!(json.push(&input), Err(ExportError::ByteLimit));
    for which in 0..6 {
        let mut invalid = row(1, "{}");
        match which {
            0 => invalid.prompt_tokens = -1,
            1 => invalid.completion_tokens = -1,
            2 => invalid.use_time = -1,
            3 => invalid.channel_id = Some(-1),
            4 => invalid.created_at = 253_402_300_800,
            _ => invalid.username = "nul\0name",
        }
        let mut csv = UsageCsvWriter::new(Vec::new(), 10, 10_000, false).unwrap();
        assert_eq!(csv.push(&invalid), Err(ExportError::InvalidRow));
        let mut json = UsageJsonWriter::new(Vec::new(), 10, 10_000).unwrap();
        assert_eq!(json.push(&invalid), Err(ExportError::InvalidRow));
    }
}
