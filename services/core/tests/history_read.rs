// SPDX-License-Identifier: AGPL-3.0-or-later
//! Projection tests are synthetic. The PG tests use SQLx-created disposable
//! databases; run them only under A09's exclusive synthetic resource lease.
use lmm_core::history_read::{
    LogQuotaInput, MAX_BASES, MAX_CREDIT_BASIS, MAX_METADATA_BYTES, MAX_RECORDS,
    captured_credits_per_usd, project_bucket_without_components, project_consumption_statistic,
    project_log,
    snapshot::ReadSnapshot,
    usage::{UsageArithmeticInput, UsageError, UsageRounding, project_migrated_usage},
};
use serde_json::Value;
use sqlx::PgPool;

fn consumption(quota: i64, other: &str) -> LogQuotaInput<'_> {
    LogQuotaInput {
        quota: Some(quota),
        log_type: 2,
        other,
    }
}

#[test]
fn projection_accepts_exact_integer_json_forms() {
    for raw in [
        "500000",
        "5e5",
        "500000.0",
        "500000000e-3",
        "0.0000000000005e18",
    ] {
        let other = format!(r#"{{"pricing_credits_per_usd":{raw}}}"#);
        assert_eq!(captured_credits_per_usd(&other), Some(500000), "{raw}");
    }
    assert_eq!(
        captured_credits_per_usd(&format!(
            r#"{{"pricing_credits_per_usd":{MAX_CREDIT_BASIS}}}"#
        )),
        Some(MAX_CREDIT_BASIS)
    );
}

#[test]
fn projection_rejects_unbounded_ambiguous_or_inexact_anchors() {
    for raw in [
        "0",
        "-1",
        "true",
        "null",
        "[]",
        "{}",
        r#""500000""#,
        "1.5",
        "9007199254740991.1",
        "9007199254740992",
        "1e19",
        "1e-19",
        "1e999999999999999999999",
        "0.000000000000000001",
    ] {
        assert_eq!(
            captured_credits_per_usd(&format!(r#"{{"pricing_credits_per_usd":{raw}}}"#)),
            None,
            "{raw}"
        );
    }
    for other in [
        "{}",
        "[]",
        "[500000]",
        "null",
        "not JSON",
        r#"{"pricing_credits_per_usd":1,"pricing_credits_per_usd":2}"#,
        r#"{"pricing_credits_per_usd":1} {}"#,
    ] {
        assert_eq!(captured_credits_per_usd(other), None, "{other}");
    }
    let large = format!(
        r#"{{"pricing_credits_per_usd":1,"padding":"{}"}}"#,
        "x".repeat(MAX_METADATA_BYTES)
    );
    assert_eq!(captured_credits_per_usd(&large), None);
}

#[test]
fn projection_preserves_raw_quota_and_unknown_semantics() {
    let input = consumption(1, r#"{"pricing_credits_per_usd":3}"#);
    assert_eq!(
        project_log(input).quota_usd.as_deref(),
        Some("0.333333333333333333")
    );
    assert_eq!(input.quota, Some(1));
    assert_eq!(project_log(consumption(0, "{}")).quota_usd, None);
    assert_eq!(
        project_log(consumption(-1, r#"{"pricing_credits_per_usd":3}"#)).quota_money_basis,
        "missing_credit_basis"
    );
    assert_eq!(
        project_log(LogQuotaInput {
            quota: None,
            ..input
        })
        .quota_money_basis,
        "invalid_stored_quota"
    );
    for log_type in [0, 1, 3, 4, 5, 6, 7] {
        assert_eq!(
            project_log(LogQuotaInput { log_type, ..input }).quota_money_basis,
            "not_consumption"
        );
    }
    assert_eq!(
        project_bucket_without_components().quota_money_basis,
        "bucket_without_credit_basis"
    );
}

#[test]
fn projection_rounds_only_once_after_adding_different_bases() {
    let result = project_consumption_statistic(
        [
            consumption(1, r#"{"pricing_credits_per_usd":3}"#),
            consumption(1, r#"{"pricing_credits_per_usd":6}"#),
        ],
        false,
    );
    assert_eq!(result.money.quota_usd.as_deref(), Some("0.5"));
    let result = project_consumption_statistic(
        [consumption(1, r#"{"pricing_credits_per_usd":3}"#); 3],
        false,
    );
    assert_eq!(result.money.quota_usd.as_deref(), Some("1"));
}

#[test]
fn projection_rounds_an_exact_mixed_basis_half_tie_away_from_zero() {
    let rows = [
        consumption(1, r#"{"pricing_credits_per_usd":524288}"#),
        consumption(1, r#"{"pricing_credits_per_usd":3814697265625}"#),
    ];
    assert_eq!(
        project_consumption_statistic(rows, false)
            .money
            .quota_usd
            .as_deref(),
        Some("0.000001907348894957")
    );
    assert_eq!(
        project_consumption_statistic(rows.into_iter().rev(), false)
            .money
            .quota_usd
            .as_deref(),
        Some("0.000001907348894957")
    );
}

#[test]
fn projection_handles_maximum_raw_sums_without_i64_overflow() {
    let row = consumption(i64::MAX, r#"{"pricing_credits_per_usd":1}"#);
    let result = project_consumption_statistic(std::iter::repeat_n(row, MAX_RECORDS), false);
    assert_eq!(
        result.money.quota_usd.as_deref(),
        Some("92233720368547758070000")
    );
}

#[test]
fn projection_does_not_publish_partial_known_totals() {
    let result = project_consumption_statistic(
        [
            consumption(500000, r#"{"pricing_credits_per_usd":500000}"#),
            consumption(12, "{}"),
        ],
        false,
    );
    assert!(!result.money.quota_money_available);
    assert_eq!(result.money.quota_usd, None);
    assert_eq!(result.unpriced_quota_records, 1);
    assert!(!result.quota_projection_truncated);
    assert_eq!(
        result.money.quota_money_basis,
        "aggregate_missing_credit_basis"
    );
}

#[test]
fn projection_distinguishes_empty_zero_and_truncated() {
    let empty = project_consumption_statistic([], false);
    assert_eq!(empty.money.quota_usd.as_deref(), Some("0"));
    assert_eq!(empty.money.quota_money_basis, "empty_result");
    let zero = project_log(consumption(0, r#"{"pricing_credits_per_usd":1}"#));
    assert_eq!(zero.quota_usd.as_deref(), Some("0"));
    assert_eq!(zero.quota_money_basis, "captured_credits_per_usd");
    let truncated = project_consumption_statistic([], true);
    assert!(truncated.quota_projection_truncated);
    assert_eq!(truncated.money.quota_usd, None);
}

#[test]
fn projection_bounds_rows_and_number_of_bases() {
    let input = consumption(1, r#"{"pricing_credits_per_usd":1}"#);
    assert!(
        project_consumption_statistic(std::iter::repeat_n(input, MAX_RECORDS + 1), false)
            .quota_projection_truncated
    );
    let metadata: Vec<_> = (1..=MAX_BASES + 1)
        .map(|basis| format!(r#"{{"pricing_credits_per_usd":{basis}}}"#))
        .collect();
    assert!(
        !project_consumption_statistic(
            metadata[..MAX_BASES]
                .iter()
                .map(|other| consumption(1, other)),
            false
        )
        .quota_projection_truncated
    );
    assert!(
        project_consumption_statistic(metadata.iter().map(|other| consumption(1, other)), false)
            .quota_projection_truncated
    );
}

#[test]
fn projection_matches_versioned_golden_contract() {
    let fixtures: Value = serde_json::from_str(include_str!(
        "../src/history_read/fixtures/projection-v1.json"
    ))
    .unwrap();
    for case in fixtures["log_cases"].as_array().unwrap() {
        let input = LogQuotaInput {
            quota: case["quota"].as_i64(),
            log_type: case["log_type"].as_i64().unwrap() as i32,
            other: case["other"].as_str().unwrap(),
        };
        assert_eq!(
            serde_json::to_value(project_log(input)).unwrap(),
            case["expected"],
            "{}",
            case["name"]
        );
    }
    for case in fixtures["statistic_cases"].as_array().unwrap() {
        let rows = case["rows"]
            .as_array()
            .unwrap()
            .iter()
            .map(|row| LogQuotaInput {
                quota: row["quota"].as_i64(),
                log_type: row["log_type"].as_i64().unwrap() as i32,
                other: row["other"].as_str().unwrap(),
            });
        let result =
            project_consumption_statistic(rows, case["source_truncated"].as_bool().unwrap());
        assert_eq!(
            serde_json::to_value(result).unwrap(),
            case["expected"],
            "{}",
            case["name"]
        );
    }
}

#[test]
fn usage_preserves_separate_baseline_and_refund_units() {
    let input = UsageArithmeticInput {
        current_used: 250,
        historical_raw: 300,
        reversed_raw: 60,
        reversed_normalized: 20,
        divisor: "3",
        rounding: UsageRounding::HalfAwayFromZero,
    };
    let result = project_migrated_usage(&input).unwrap();
    assert_eq!(result.normalized_used_quota, 90);
    assert_eq!(result.historical_raw_quota, 240);
    assert_eq!(result.historical_normalized_quota, 80);
    assert_eq!(result.post_migration_quota, 10);
    // Dividing current_used by either a log's captured USD anchor or the
    // migration divisor is not the normalized cumulative-usage calculation.
    assert_ne!(result.normalized_used_quota, input.current_used / 3);
}

#[test]
fn usage_rejects_unproven_reductions_and_bad_bases() {
    let mut input = UsageArithmeticInput {
        current_used: 299,
        historical_raw: 300,
        reversed_raw: 0,
        reversed_normalized: 0,
        divisor: "3",
        rounding: UsageRounding::TowardZero,
    };
    assert_eq!(
        project_migrated_usage(&input),
        Err(UsageError::UnprovenReduction)
    );
    input.current_used = 300;
    for divisor in [
        "",
        "1",
        "0",
        "-3",
        "3e0",
        "3.",
        "3.0000000000000000001",
        "NaN",
    ] {
        input.divisor = divisor;
        assert_eq!(
            project_migrated_usage(&input),
            Err(UsageError::InvalidBasis)
        );
    }
    input.divisor = "3";
    input.reversed_raw = 301;
    assert_eq!(
        project_migrated_usage(&input),
        Err(UsageError::InvalidBasis)
    );
}

#[test]
fn usage_obeys_the_captured_rounding_policy() {
    let mut input = UsageArithmeticInput {
        current_used: 3,
        historical_raw: 3,
        reversed_raw: 0,
        reversed_normalized: 0,
        divisor: "2",
        rounding: UsageRounding::TowardZero,
    };
    assert_eq!(
        project_migrated_usage(&input)
            .unwrap()
            .normalized_used_quota,
        1
    );
    input.rounding = UsageRounding::HalfAwayFromZero;
    assert_eq!(
        project_migrated_usage(&input)
            .unwrap()
            .normalized_used_quota,
        2
    );
}

async fn prepare(pool: &PgPool) {
    sqlx::query("CREATE TABLE a04_history_fixture (id BIGINT PRIMARY KEY, user_id BIGINT NOT NULL, quota BIGINT NOT NULL)")
        .execute(pool).await.unwrap();
    sqlx::query("INSERT INTO a04_history_fixture VALUES (1,7,100),(2,8,999)")
        .execute(pool)
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn pg_default_snapshot_exposes_the_count_page_counterexample(pool: PgPool) {
    prepare(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL READ COMMITTED")
        .execute(&mut *tx)
        .await
        .unwrap();
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM a04_history_fixture WHERE user_id=7")
        .fetch_one(&mut *tx)
        .await
        .unwrap();
    sqlx::query("INSERT INTO a04_history_fixture VALUES (3,7,200)")
        .execute(&pool)
        .await
        .unwrap();
    let rows: Vec<(i64,)> =
        sqlx::query_as("SELECT quota FROM a04_history_fixture WHERE user_id=7 ORDER BY id")
            .fetch_all(&mut *tx)
            .await
            .unwrap();
    assert_eq!(count, 1);
    assert_eq!(rows.len(), 2);
    tx.rollback().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn pg_read_snapshot_keeps_count_page_sum_consistent(pool: PgPool) {
    prepare(&pool).await;
    let mut snapshot = ReadSnapshot::begin(&pool).await.unwrap();
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM a04_history_fixture WHERE user_id=7")
        .fetch_one(snapshot.connection())
        .await
        .unwrap();
    sqlx::query("INSERT INTO a04_history_fixture VALUES (3,7,200)")
        .execute(&pool)
        .await
        .unwrap();
    let rows: Vec<(i64,)> =
        sqlx::query_as("SELECT quota FROM a04_history_fixture WHERE user_id=7 ORDER BY id")
            .fetch_all(snapshot.connection())
            .await
            .unwrap();
    let sum: String =
        sqlx::query_scalar("SELECT sum(quota)::text FROM a04_history_fixture WHERE user_id=7")
            .fetch_one(snapshot.connection())
            .await
            .unwrap();
    assert_eq!(count, 1);
    assert_eq!(rows, [(100,)]);
    assert_eq!(sum, "100");
    snapshot.finish().await.unwrap();
    let current: i64 =
        sqlx::query_scalar("SELECT count(*) FROM a04_history_fixture WHERE user_id=7")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(current, 2);
}

#[sqlx::test(migrations = false)]
async fn pg_read_snapshot_cannot_mutate_persistent_rows(pool: PgPool) {
    prepare(&pool).await;
    let mut snapshot = ReadSnapshot::begin(&pool).await.unwrap();
    let error = sqlx::query("UPDATE a04_history_fixture SET quota=0")
        .execute(snapshot.connection())
        .await
        .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("25006")
    );
    snapshot.finish().await.unwrap();
    let values: Vec<(i64,)> = sqlx::query_as("SELECT quota FROM a04_history_fixture ORDER BY id")
        .fetch_all(&pool)
        .await
        .unwrap();
    assert_eq!(values, [(100,), (999,)]);
}

#[path = "history_read/native.rs"]
mod native;

#[test]
fn metadata_work_is_bounded_even_below_the_record_ceiling() {
    use lmm_core::history_read::{MAX_PROJECTION_METADATA_BYTES, QuotaStatisticAccumulator};
    let prefix = r#"{"pricing_credits_per_usd":1,"padding":""#;
    let metadata = format!("{}{}{}", prefix, "x".repeat(MAX_METADATA_BYTES - prefix.len() - 2), "\"}");
    assert_eq!(metadata.len(), MAX_METADATA_BYTES);
    let mut accumulator = QuotaStatisticAccumulator::default();
    for _ in 0..MAX_PROJECTION_METADATA_BYTES / MAX_METADATA_BYTES {
        assert!(accumulator.push(consumption(1, &metadata)));
    }
    assert!(!accumulator.push(consumption(1, &metadata)));
    assert!(!accumulator.push(consumption(1, r#"{"pricing_credits_per_usd":1}"#)));
    let result = accumulator.finish(false);
    assert!(result.quota_projection_truncated);
    assert!(!result.money.quota_money_available);
    assert_eq!(result.money.quota_usd, None);
}

#[test]
fn streamed_projection_matches_the_same_complete_selection() {
    use lmm_core::history_read::QuotaStatisticAccumulator;
    let rows = [
        consumption(1, r#"{"pricing_credits_per_usd":3}"#),
        consumption(1, r#"{"pricing_credits_per_usd":6}"#),
    ];
    let mut accumulator = QuotaStatisticAccumulator::default();
    for row in rows {
        assert!(accumulator.push(row));
    }
    assert_eq!(accumulator.finish(false), project_consumption_statistic(rows, false));
}
