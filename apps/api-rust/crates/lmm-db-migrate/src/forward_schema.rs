//! Forward-only schema checks for mounted Rust business routes.
//!
//! Contract 1 remains the frozen 34-table SQLite baseline. The Go-owned bounty tables are an
//! expand step and become required only once a release advances to contract 2.

use postgres::Transaction;

use crate::MigrationError;

/// The first schema contract that requires the bounty expand step.
pub const BOUNTY_SCHEMA_CONTRACT_ID: i64 = 2;
/// The first schema contract that supports current dashboard workflow data.
pub const CURRENT_DASHBOARD_SCHEMA_CONTRACT_ID: i64 = 3;
/// The first schema contract that requires the subscription reset subsystem.
pub const SUBSCRIPTION_RESET_SCHEMA_CONTRACT_ID: i64 = 6;
/// The first schema contract that requires invoice identity and failed-payment reasons.
pub const COMPANY_BILLING_PROFILE_SCHEMA_CONTRACT_ID: i64 = 7;
/// The first schema contract that persists split subscription webhook evidence.
pub const WAFFO_SUBSCRIPTION_SCHEMA_CONTRACT_ID: i64 = 8;
/// The first schema contract that binds subscription refunds to immutable payment evidence.
pub const SUBSCRIPTION_PAYMENT_REFUND_SCHEMA_CONTRACT_ID: i64 = 9;
/// The first schema contract that exposes owner-granted account-wallet balance reads.
pub const ACCOUNT_BALANCE_ACCESS_SCHEMA_CONTRACT_ID: i64 = 10;
/// The first schema contract that persists ordered mandatory-announcement reads.
pub const MANDATORY_ANNOUNCEMENT_SCHEMA_CONTRACT_ID: i64 = 11;
/// The first schema contract with current top-up evidence and referral/coupon state.
pub const PAYMENT_RUNTIME_SCHEMA_CONTRACT_ID: i64 = 12;
/// Current Go immutable payment/refund finance ledger.
pub const PAYMENT_EXTENSIONS_SCHEMA_CONTRACT_ID: i64 = 13;
/// Durable relay reservation/settlement and Go subscription version fields.
pub const RELAY_SETTLEMENT_SCHEMA_CONTRACT_ID: i64 = 14;
/// The current catalogue expansion adds immutable wallet-funded ad placements.
pub const CURRENT_CATALOG_SCHEMA_CONTRACT_ID: i64 = 15;
/// The first contract that supports current token-management flags.
pub const TOKEN_MANAGEMENT_SCHEMA_CONTRACT_ID: i64 = 16;

#[derive(Clone, Copy)]
struct ColumnRequirement {
    name: &'static str,
    data_type: &'static str,
    character_maximum_length: Option<i32>,
    nullable: bool,
}

#[derive(Clone, Copy)]
struct IndexRequirement<'a> {
    table: &'a str,
    name: &'a str,
    unique: bool,
    columns: &'a [&'a str],
    predicate: Option<&'a str>,
}

#[derive(Clone, Copy)]
struct PrimaryKeyRequirement {
    table: &'static str,
    columns: &'static [&'static str],
}

#[derive(Clone, Copy)]
struct SerialRequirement {
    table: &'static str,
    column: &'static str,
    sequence: &'static str,
}

#[derive(Clone, Copy)]
enum LiteralDefault {
    BigintZero,
    Varchar(&'static str),
}

#[derive(Clone, Copy)]
struct DefaultRequirement {
    table: &'static str,
    column: &'static str,
    value: LiteralDefault,
}

const fn column(
    name: &'static str,
    data_type: &'static str,
    character_maximum_length: Option<i32>,
) -> ColumnRequirement {
    ColumnRequirement {
        name,
        data_type,
        character_maximum_length,
        nullable: false,
    }
}

const fn nullable_column(
    name: &'static str,
    data_type: &'static str,
    character_maximum_length: Option<i32>,
) -> ColumnRequirement {
    ColumnRequirement {
        name,
        data_type,
        character_maximum_length,
        nullable: true,
    }
}

const COMPANY_BILLING_PROFILE_COLUMNS: &[ColumnRequirement] = &[
    column("user_id", "bigint", None),
    column("country", "character", Some(2)),
    column("is_business", "boolean", None),
    column("postcode", "character varying", Some(32)),
    column("state", "character varying", Some(128)),
    column("business_name", "character varying", Some(255)),
    column("tax_id", "character varying", Some(64)),
    column("use_for_invoices", "boolean", None),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
];

const PROJECT_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("owner_user_id", "bigint", None),
    column("repository_url", "character varying", Some(512)),
    column("title", "character varying", Some(120)),
    column("description", "text", None),
    column("rules", "text", None),
    column("reward_quota", "bigint", None),
    column("net_reward_quota", "bigint", None),
    column("reward_slots", "bigint", None),
    column("escrow_quota", "bigint", None),
    column("platform_fee_rate_bps", "bigint", None),
    column("platform_fee_quota", "bigint", None),
    column("status", "character varying", Some(20)),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
    column("published_at", "bigint", None),
    column("closed_at", "bigint", None),
];

const CHALLENGE_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("project_id", "bigint", None),
    column("participant_user_id", "bigint", None),
    column("github_handle", "character varying", Some(100)),
    column("status", "character varying", Some(20)),
    column("issue_url", "character varying", Some(512)),
    column("pull_request_url", "character varying", Some(512)),
    column("submission_note", "text", None),
    column("review_note", "text", None),
    column("reward_quota", "bigint", None),
    column("tip_quota", "bigint", None),
    column("owner_rating_score", "bigint", None),
    column("owner_rating_comment", "character varying", Some(1000)),
    column("owner_rated_at", "bigint", None),
    column("contributor_rating_score", "bigint", None),
    column(
        "contributor_rating_comment",
        "character varying",
        Some(1000),
    ),
    column("contributor_rated_at", "bigint", None),
    column("owner_rating_overturned", "boolean", None),
    column("accepted_at", "bigint", None),
    column("submitted_at", "bigint", None),
    column("reviewed_at", "bigint", None),
    column("rejected_at", "bigint", None),
    column("paid_at", "bigint", None),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
];

const LEDGER_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("project_id", "bigint", None),
    column("challenge_id", "bigint", None),
    column("user_id", "bigint", None),
    column("counterparty_user_id", "bigint", None),
    column("kind", "character varying", Some(32)),
    column("quota", "bigint", None),
    column("note", "character varying", Some(500)),
    nullable_column("reward_payout_key", "character varying", Some(64)),
    column("recipient_read_at", "bigint", None),
    column("thanked_at", "bigint", None),
    column("created_at", "bigint", None),
];

const DISPUTE_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("challenge_id", "bigint", None),
    column("project_id", "bigint", None),
    column("opened_by_user_id", "bigint", None),
    column("against_user_id", "bigint", None),
    column("case_key", "character varying", Some(96)),
    nullable_column("open_key", "character varying", Some(64)),
    column("reason", "character varying", Some(64)),
    column("statement", "text", None),
    column("project_title_snapshot", "character varying", Some(120)),
    column("repository_url_snapshot", "character varying", Some(512)),
    column("project_rules_snapshot", "text", None),
    column("project_escrow_quota_snapshot", "bigint", None),
    column("challenge_status_snapshot", "character varying", Some(20)),
    column("issue_url_snapshot", "character varying", Some(512)),
    column("pull_request_url_snapshot", "character varying", Some(512)),
    column("submission_note_snapshot", "text", None),
    column("review_note_snapshot", "text", None),
    column("reward_quota_snapshot", "bigint", None),
    column("tip_quota_snapshot", "bigint", None),
    column("owner_rating_score_snapshot", "bigint", None),
    column(
        "owner_rating_comment_snapshot",
        "character varying",
        Some(1000),
    ),
    column("contributor_rating_score_snapshot", "bigint", None),
    column(
        "contributor_rating_comment_snapshot",
        "character varying",
        Some(1000),
    ),
    column("status", "character varying", Some(32)),
    column("resolution", "text", None),
    column("resolved_by_user_id", "bigint", None),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
    column("resolved_at", "bigint", None),
];

const MCP_TOKEN_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("user_id", "bigint", None),
    column("token_hash", "character", Some(64)),
    column("token_hint", "character varying", Some(24)),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
    column("last_used_at", "bigint", None),
];

const MCP_CONFIRMATION_COLUMNS: &[ColumnRequirement] = &[
    column("id", "character varying", Some(80)),
    column("user_id", "bigint", None),
    column("tool_name", "character varying", Some(128)),
    column("payload_hash", "character", Some(64)),
    column("expires_at", "bigint", None),
    column("consumed_at", "bigint", None),
    column("created_at", "bigint", None),
];

const MCP_OPERATION_COLUMNS: &[ColumnRequirement] = &[
    column("id", "character varying", Some(80)),
    column("user_id", "bigint", None),
    column("tool_name", "character varying", Some(128)),
    column("payload_hash", "character", Some(64)),
    column("result_json", "text", None),
    column("created_at", "bigint", None),
];

const REST_OPERATION_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("user_id", "bigint", None),
    column("operation", "character varying", Some(64)),
    column("idempotency_key_hash", "character", Some(64)),
    column("payload_hash", "character", Some(64)),
    column("result_json", "text", None),
    column("created_at", "bigint", None),
    column("completed_at", "bigint", None),
];

const TABLES: &[(&str, &[ColumnRequirement])] = &[
    ("open_source_bounty_projects", PROJECT_COLUMNS),
    ("open_source_bounty_challenges", CHALLENGE_COLUMNS),
    ("open_source_bounty_ledgers", LEDGER_COLUMNS),
    ("open_source_bounty_disputes", DISPUTE_COLUMNS),
    ("open_source_bounty_mcp_tokens", MCP_TOKEN_COLUMNS),
    (
        "open_source_bounty_mcp_confirmations",
        MCP_CONFIRMATION_COLUMNS,
    ),
    ("open_source_bounty_mcp_operations", MCP_OPERATION_COLUMNS),
    ("open_source_bounty_rest_operations", REST_OPERATION_COLUMNS),
];

/// Verifies the table/column contract needed by the Rust bounty routes.
pub fn verify_open_source_bounty_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    for &(table, columns) in TABLES {
        let table_exists: bool = transaction
            .query_one(
                "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace WHERE n.nspname = $1 AND c.relname = $2 AND c.relkind = 'r')",
                &[&schema, &table],
            )?
            .get(0);
        if !table_exists {
            return Err(MigrationError::Manifest(format!(
                "forward schema is missing table {table}"
            )));
        }
        for requirement in columns {
            let row = transaction.query_opt(
                "SELECT data_type, character_maximum_length, is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3",
                &[&schema, &table, &requirement.name],
            )?.ok_or_else(|| {
                MigrationError::Manifest(format!(
                    "forward schema is missing column {table}.{}",
                    requirement.name
                ))
            })?;
            let data_type: String = row.get(0);
            let length: Option<i32> = row.get(1);
            let is_nullable: String = row.get(2);
            if data_type != requirement.data_type
                || length != requirement.character_maximum_length
                || (is_nullable == "YES") != requirement.nullable
            {
                return Err(MigrationError::Manifest(format!(
                    "forward schema column mismatch for {table}.{}",
                    requirement.name
                )));
            }
        }
    }
    Ok(())
}

const DEVELOPER_ACCESS_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("user_id", "bigint", None),
    column("status", "character varying", Some(20)),
    column("source", "character varying", Some(40)),
    nullable_column("reason", "text", None),
    nullable_column("ai_recommendation", "text", None),
    nullable_column("admin_user_id", "bigint", None),
    nullable_column("admin_note", "text", None),
    column("created_at", "bigint", None),
    column("reviewed_at", "bigint", None),
];

const RELEASE_NOTE_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("version", "character varying", Some(128)),
    column("revision", "bigint", None),
    column("content", "text", None),
    column("published_at", "bigint", None),
    column("published_by", "bigint", None),
];

const RELEASE_NOTE_READ_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("release_note_id", "bigint", None),
    column("user_id", "bigint", None),
    column("read_at", "bigint", None),
];

const GIFT_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("title", "character varying", Some(64)),
    nullable_column("description", "character varying", Some(255)),
    column("quota", "bigint", None),
    column("start_at", "bigint", None),
    column("end_at", "bigint", None),
    column("min_used_quota", "bigint", None),
    column("min_account_age_days", "bigint", None),
    column("enabled", "boolean", None),
    nullable_column("created_at", "bigint", None),
];

const GIFT_CLAIM_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("gift_id", "bigint", None),
    column("user_id", "bigint", None),
    nullable_column("username", "character varying", Some(64)),
    column("quota", "bigint", None),
    nullable_column("created_at", "bigint", None),
];

const ADVANCED_SECURITY_EVENT_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    nullable_column("created_at", "bigint", None),
    nullable_column("request_id", "text", None),
    nullable_column("user_id", "bigint", None),
    nullable_column("username", "text", None),
    nullable_column("token_id", "bigint", None),
    nullable_column("channel_id", "bigint", None),
    nullable_column("model_name", "text", None),
    nullable_column("group", "text", None),
    nullable_column("endpoint", "text", None),
    nullable_column("decision", "text", None),
    nullable_column("rule_id", "text", None),
    nullable_column("rule_name", "text", None),
    nullable_column("category", "text", None),
    nullable_column("layer", "text", None),
    nullable_column("severity", "text", None),
    nullable_column("source", "text", None),
    nullable_column("rule_version", "text", None),
    nullable_column("pattern_digest", "text", None),
    nullable_column("input_digest", "text", None),
    nullable_column("match_count", "bigint", None),
];

const RESET_VOUCHER_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("user_id", "bigint", None),
    column("plan_id", "bigint", None),
    column("operation_id", "character varying", Some(64)),
    column("status", "character varying", Some(16)),
    column("expires_at", "bigint", None),
    column("redeemed_at", "bigint", None),
    column("created_by", "bigint", None),
    column("created_at", "bigint", None),
    column("updated_at", "bigint", None),
];
const RESET_EVENT_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("operation_id", "character varying", Some(64)),
    column("user_id", "bigint", None),
    column("plan_id", "bigint", None),
    column("mode", "character varying", Some(24)),
    column("actor_user_id", "bigint", None),
    column("voucher_id", "bigint", None),
    column("reset_count", "bigint", None),
    column("restored_quota", "bigint", None),
    column("voucher_expiry", "bigint", None),
    column("created_at", "bigint", None),
];
const RESET_PREVIEW_COLUMNS: &[ColumnRequirement] = &[
    column("token", "character varying", Some(64)),
    column("actor_user_id", "bigint", None),
    column("mode", "character varying", Some(16)),
    column("targets_json", "text", None),
    column("payload_hash", "character varying", Some(64)),
    column("target_count", "bigint", None),
    column("active_subscriptions", "bigint", None),
    column("quota_to_restore", "bigint", None),
    column("voucher_expires_at", "bigint", None),
    column("expires_at", "bigint", None),
    column("consumed_at", "bigint", None),
    column("operation_id", "character varying", Some(64)),
    column("created_at", "bigint", None),
];
const RESET_OPERATION_COLUMNS: &[ColumnRequirement] = &[
    column("operation_id", "character varying", Some(64)),
    column("preview_token", "character varying", Some(64)),
    column("actor_user_id", "bigint", None),
    column("mode", "character varying", Some(16)),
    column("payload_hash", "character varying", Some(64)),
    column("result_json", "text", None),
    column("created_at", "bigint", None),
    column("completed_at", "bigint", None),
];

const RESET_PRIMARY_KEYS: &[PrimaryKeyRequirement] = &[
    PrimaryKeyRequirement {
        table: "subscription_reset_vouchers",
        columns: &["id"],
    },
    PrimaryKeyRequirement {
        table: "subscription_reset_events",
        columns: &["id"],
    },
    PrimaryKeyRequirement {
        table: "subscription_reset_previews",
        columns: &["token"],
    },
    PrimaryKeyRequirement {
        table: "subscription_reset_operations",
        columns: &["operation_id"],
    },
];

const RESET_SERIAL_COLUMNS: &[SerialRequirement] = &[
    SerialRequirement {
        table: "subscription_reset_vouchers",
        column: "id",
        sequence: "subscription_reset_vouchers_id_seq",
    },
    SerialRequirement {
        table: "subscription_reset_events",
        column: "id",
        sequence: "subscription_reset_events_id_seq",
    },
];

const RESET_DEFAULTS: &[DefaultRequirement] = &[
    DefaultRequirement {
        table: "subscription_reset_vouchers",
        column: "status",
        value: LiteralDefault::Varchar("available"),
    },
    DefaultRequirement {
        table: "subscription_reset_vouchers",
        column: "redeemed_at",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_events",
        column: "voucher_id",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_events",
        column: "reset_count",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_events",
        column: "restored_quota",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_events",
        column: "voucher_expiry",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_previews",
        column: "voucher_expires_at",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_previews",
        column: "consumed_at",
        value: LiteralDefault::BigintZero,
    },
    DefaultRequirement {
        table: "subscription_reset_previews",
        column: "operation_id",
        value: LiteralDefault::Varchar(""),
    },
];

const RESET_INDEXES: &[IndexRequirement<'_>] = &[
    IndexRequirement {
        table: "subscription_plans",
        name: "idx_subscription_plans_archived_at",
        unique: false,
        columns: &["archived_at"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_voucher_operation",
        unique: true,
        columns: &["user_id", "plan_id", "operation_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_vouchers_user_id",
        unique: false,
        columns: &["user_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_vouchers_plan_id",
        unique: false,
        columns: &["plan_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_vouchers_status",
        unique: false,
        columns: &["status"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_vouchers_expires_at",
        unique: false,
        columns: &["expires_at"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_vouchers",
        name: "idx_subscription_reset_vouchers_created_by",
        unique: false,
        columns: &["created_by"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_events",
        name: "idx_subscription_reset_event_operation",
        unique: true,
        columns: &["operation_id", "user_id", "plan_id", "mode"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_events",
        name: "idx_subscription_reset_events_user_id",
        unique: false,
        columns: &["user_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_events",
        name: "idx_subscription_reset_events_plan_id",
        unique: false,
        columns: &["plan_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_events",
        name: "idx_subscription_reset_events_actor_user_id",
        unique: false,
        columns: &["actor_user_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_events",
        name: "idx_subscription_reset_events_created_at",
        unique: false,
        columns: &["created_at"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_previews",
        name: "idx_subscription_reset_previews_actor_user_id",
        unique: false,
        columns: &["actor_user_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_previews",
        name: "idx_subscription_reset_previews_expires_at",
        unique: false,
        columns: &["expires_at"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_operations",
        name: "idx_subscription_reset_operations_preview_token",
        unique: true,
        columns: &["preview_token"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_operations",
        name: "idx_subscription_reset_operations_actor_user_id",
        unique: false,
        columns: &["actor_user_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_reset_operations",
        name: "idx_subscription_reset_operations_completed_at",
        unique: false,
        columns: &["completed_at"],
        predicate: None,
    },
];

const PERSONAL_ACCESS_IP_COLUMNS: &[ColumnRequirement] = &[
    column("user_id", "bigint", None),
    column("ip", "character varying", Some(45)),
    nullable_column("created_at", "bigint", None),
    nullable_column("updated_at", "bigint", None),
];

/// Verifies the contract-3 dashboard tables and bounty archival column.
pub fn verify_current_dashboard_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    let row = transaction
        .query_opt(
            "SELECT data_type, is_nullable, column_default FROM information_schema.columns WHERE table_schema = $1 AND table_name = 'open_source_bounty_projects' AND column_name = 'archived_at'",
            &[&schema],
        )?
        .ok_or_else(|| {
            MigrationError::Manifest(
                "forward schema is missing column open_source_bounty_projects.archived_at"
                    .to_owned(),
            )
        })?;
    let data_type: String = row.get(0);
    let is_nullable: String = row.get(1);
    let default: Option<String> = row.get(2);
    if data_type != "bigint"
        || is_nullable != "NO"
        || !default.as_deref().is_some_and(|value| value.contains('0'))
    {
        return Err(MigrationError::Manifest(
            "forward schema column mismatch for open_source_bounty_projects.archived_at".to_owned(),
        ));
    }
    let index_exists: bool = transaction
        .query_one(
            "SELECT EXISTS (SELECT 1 FROM pg_catalog.pg_indexes WHERE schemaname = $1 AND tablename = 'open_source_bounty_projects' AND indexname = 'idx_open_source_bounty_projects_archived_at')",
            &[&schema],
        )?
        .get(0);
    if !index_exists {
        return Err(MigrationError::Manifest(
            "forward schema is missing index idx_open_source_bounty_projects_archived_at"
                .to_owned(),
        ));
    }
    for &(table, columns) in &[
        ("developer_access_requests", DEVELOPER_ACCESS_COLUMNS),
        ("release_notes", RELEASE_NOTE_COLUMNS),
        ("release_note_reads", RELEASE_NOTE_READ_COLUMNS),
        ("gifts", GIFT_COLUMNS),
        ("gift_claims", GIFT_CLAIM_COLUMNS),
        ("advanced_security_events", ADVANCED_SECURITY_EVENT_COLUMNS),
        ("personal_access_ips", PERSONAL_ACCESS_IP_COLUMNS),
    ] {
        for requirement in columns {
            let row = transaction
                .query_opt(
                    "SELECT data_type, character_maximum_length, is_nullable FROM information_schema.columns WHERE table_schema = $1 AND table_name = $2 AND column_name = $3",
                    &[&schema, &table, &requirement.name],
                )?
                .ok_or_else(|| {
                    MigrationError::Manifest(format!(
                        "forward schema is missing column {table}.{}",
                        requirement.name
                    ))
                })?;
            let data_type: String = row.get(0);
            let length: Option<i32> = row.get(1);
            let is_nullable: String = row.get(2);
            if data_type != requirement.data_type
                || length != requirement.character_maximum_length
                || (is_nullable == "YES") != requirement.nullable
            {
                return Err(MigrationError::Manifest(format!(
                    "forward schema column mismatch for {table}.{}",
                    requirement.name
                )));
            }
        }
    }
    for &(table, index, unique) in &[
        (
            "developer_access_requests",
            "idx_developer_access_requests_source",
            false,
        ),
        ("release_notes", "idx_release_note_version_revision", true),
        (
            "release_note_reads",
            "idx_release_note_read_user_note",
            true,
        ),
        ("gift_claims", "idx_gift_user", true),
        (
            "advanced_security_events",
            "idx_advanced_security_events_created_at",
            false,
        ),
        ("personal_access_ips", "idx_personal_access_ips_ip", false),
        ("personal_access_ips", "personal_access_ips_pkey", true),
    ] {
        let index_definition: Option<String> = transaction
            .query_opt(
                "SELECT indexdef FROM pg_catalog.pg_indexes WHERE schemaname = $1 AND tablename = $2 AND indexname = $3",
                &[&schema, &table, &index],
            )?
            .map(|row| row.get(0));
        if index_definition.is_none()
            || (unique
                && !index_definition
                    .as_deref()
                    .is_some_and(|definition| definition.starts_with("CREATE UNIQUE INDEX")))
        {
            return Err(MigrationError::Manifest(format!(
                "forward schema is missing compatible index {index}"
            )));
        }
    }
    Ok(())
}

fn bigint_default_is_exact_zero(default: Option<&str>) -> bool {
    default.is_some_and(|value| {
        matches!(
            value.trim(),
            "0" | "0::bigint" | "(0)::bigint" | "'0'::bigint"
        )
    })
}

fn varchar_default_is_exact(default: Option<&str>, expected: &str) -> bool {
    let Some(value) = default.map(str::trim) else {
        return false;
    };
    let literal = ["::character varying", "::varchar", "::text"]
        .into_iter()
        .find_map(|suffix| value.strip_suffix(suffix))
        .unwrap_or(value)
        .trim();
    literal
        .strip_prefix('\'')
        .and_then(|value| value.strip_suffix('\''))
        .is_some_and(|value| value.replace("''", "'") == expected)
}

/// Verifies the complete contract-6 reset-table catalog and archival index.
pub fn verify_subscription_reset_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    let archived = transaction.query_opt(
        "SELECT data_type,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='subscription_plans' AND column_name='archived_at'",
        &[&schema],
    )?.ok_or_else(|| MigrationError::Manifest(
        "forward schema is missing column subscription_plans.archived_at".to_owned(),
    ))?;
    let data_type: String = archived.get(0);
    let nullable: String = archived.get(1);
    let default: Option<String> = archived.get(2);
    if data_type != "bigint"
        || nullable != "NO"
        || !bigint_default_is_exact_zero(default.as_deref())
    {
        return Err(MigrationError::Manifest(
            "forward schema column mismatch for subscription_plans.archived_at".to_owned(),
        ));
    }
    for &(table, columns) in &[
        ("subscription_reset_vouchers", RESET_VOUCHER_COLUMNS),
        ("subscription_reset_events", RESET_EVENT_COLUMNS),
        ("subscription_reset_previews", RESET_PREVIEW_COLUMNS),
        ("subscription_reset_operations", RESET_OPERATION_COLUMNS),
    ] {
        for requirement in columns {
            let row = transaction.query_opt(
                "SELECT data_type,character_maximum_length,is_nullable FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3",
                &[&schema, &table, &requirement.name],
            )?.ok_or_else(|| MigrationError::Manifest(format!(
                "forward schema is missing column {table}.{}", requirement.name
            )))?;
            let found_type: String = row.get(0);
            let found_length: Option<i32> = row.get(1);
            let found_nullable: String = row.get(2);
            if found_type != requirement.data_type
                || found_length != requirement.character_maximum_length
                || (found_nullable == "YES") != requirement.nullable
            {
                return Err(MigrationError::Manifest(format!(
                    "forward schema column mismatch for {table}.{}",
                    requirement.name
                )));
            }
        }
    }
    for requirement in RESET_PRIMARY_KEYS {
        let definition = transaction.query_opt(
            r#"SELECT metadata.indisvalid,
                ARRAY(
                    SELECT attribute.attname::TEXT
                    FROM pg_catalog.unnest(metadata.indkey::SMALLINT[]) WITH ORDINALITY AS key(attribute_number, ordinality)
                    JOIN pg_catalog.pg_attribute AS attribute
                      ON attribute.attrelid=metadata.indrelid
                     AND attribute.attnum=key.attribute_number
                    WHERE key.ordinality <= metadata.indnkeyatts
                    ORDER BY key.ordinality
                )
               FROM pg_catalog.pg_index AS metadata
               JOIN pg_catalog.pg_class AS table_class ON table_class.oid=metadata.indrelid
               JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=table_class.relnamespace
              WHERE namespace.nspname=$1 AND table_class.relname=$2 AND metadata.indisprimary"#,
            &[&schema, &requirement.table],
        )?;
        let compatible = definition.is_some_and(|row| {
            let valid: bool = row.get(0);
            let columns: Vec<String> = row.get(1);
            valid
                && columns.len() == requirement.columns.len()
                && columns
                    .iter()
                    .map(String::as_str)
                    .eq(requirement.columns.iter().copied())
        });
        if !compatible {
            return Err(MigrationError::Manifest(format!(
                "forward schema primary key mismatch for {}",
                requirement.table
            )));
        }
    }
    for requirement in RESET_SERIAL_COLUMNS {
        let compatible: Option<bool> = transaction
            .query_one(
                r#"SELECT
                    to_regclass(pg_get_serial_sequence(format('%I.%I',$1::TEXT,$2::TEXT),$3::TEXT)) =
                        to_regclass(format('%I.%I',$1::TEXT,$4::TEXT))
                    AND EXISTS (
                        SELECT 1
                        FROM pg_catalog.pg_class AS table_class
                        JOIN pg_catalog.pg_namespace AS table_namespace ON table_namespace.oid=table_class.relnamespace
                        JOIN pg_catalog.pg_attribute AS attribute ON attribute.attrelid=table_class.oid
                        JOIN pg_catalog.pg_attrdef AS default_value ON default_value.adrelid=table_class.oid AND default_value.adnum=attribute.attnum
                        WHERE table_namespace.nspname=$1 AND table_class.relname=$2 AND attribute.attname=$3
                          AND pg_catalog.pg_get_expr(default_value.adbin, default_value.adrelid, false) =
                              format('nextval(%L::regclass)', to_regclass(format('%I.%I',$1::TEXT,$4::TEXT))::TEXT)
                    )"#,
                &[&schema, &requirement.table, &requirement.column, &requirement.sequence],
            )?
            .get(0);
        if compatible != Some(true) {
            return Err(MigrationError::Manifest(format!(
                "forward schema sequence/default mismatch for {}.{}",
                requirement.table, requirement.column
            )));
        }
    }
    for requirement in RESET_DEFAULTS {
        let default: Option<String> = transaction
            .query_opt(
                "SELECT column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3",
                &[&schema, &requirement.table, &requirement.column],
            )?
            .and_then(|row| row.get(0));
        let compatible = match requirement.value {
            LiteralDefault::BigintZero => bigint_default_is_exact_zero(default.as_deref()),
            LiteralDefault::Varchar(expected) => {
                varchar_default_is_exact(default.as_deref(), expected)
            }
        };
        if !compatible {
            return Err(MigrationError::Manifest(format!(
                "forward schema default mismatch for {}.{}",
                requirement.table, requirement.column
            )));
        }
    }
    verify_indexes(transaction, schema, RESET_INDEXES)
}

fn verify_indexes(
    transaction: &mut Transaction<'_>,
    schema: &str,
    requirements: &[IndexRequirement<'_>],
) -> Result<(), MigrationError> {
    for requirement in requirements {
        let definition = transaction.query_opt(
            r#"SELECT metadata.indisunique,
                metadata.indisvalid,
                metadata.indisready,
                metadata.indisprimary,
                metadata.indisexclusion,
                metadata.indexprs IS NULL,
                metadata.indnkeyatts::INT,
                metadata.indnatts::INT,
                access_method.amname::TEXT,
                ARRAY(
                    SELECT attribute.attname::TEXT
                    FROM pg_catalog.unnest(metadata.indkey::SMALLINT[]) WITH ORDINALITY AS key(attribute_number, ordinality)
                    JOIN pg_catalog.pg_attribute AS attribute
                      ON attribute.attrelid=metadata.indrelid
                     AND attribute.attnum=key.attribute_number
                    WHERE key.ordinality <= metadata.indnkeyatts
                    ORDER BY key.ordinality
                ),
                pg_catalog.pg_get_expr(metadata.indpred, metadata.indrelid)
               FROM pg_catalog.pg_index AS metadata
               JOIN pg_catalog.pg_class AS index_class ON index_class.oid=metadata.indexrelid
               JOIN pg_catalog.pg_am AS access_method ON access_method.oid=index_class.relam
               JOIN pg_catalog.pg_class AS table_class ON table_class.oid=metadata.indrelid
               JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=table_class.relnamespace
              WHERE namespace.nspname=$1 AND table_class.relname=$2 AND index_class.relname=$3"#,
            &[&schema, &requirement.table, &requirement.name],
        )?;
        let compatible = definition.is_some_and(|row| {
            let found_unique: bool = row.get(0);
            let found_valid: bool = row.get(1);
            let found_ready: bool = row.get(2);
            let found_primary: bool = row.get(3);
            let found_exclusion: bool = row.get(4);
            let found_no_expressions: bool = row.get(5);
            let found_key_attribute_count: i32 = row.get(6);
            let found_attribute_count: i32 = row.get(7);
            let found_method: String = row.get(8);
            let found_columns: Vec<String> = row.get(9);
            let found_predicate: Option<String> = row.get(10);
            found_unique == requirement.unique
                && found_valid
                && found_ready
                && !found_primary
                && !found_exclusion
                && found_no_expressions
                && found_key_attribute_count == requirement.columns.len() as i32
                && found_attribute_count == requirement.columns.len() as i32
                && found_method == "btree"
                && found_columns.len() == requirement.columns.len()
                && found_columns
                    .iter()
                    .map(String::as_str)
                    .eq(requirement.columns.iter().copied())
                && found_predicate.as_deref() == requirement.predicate
        });
        if !compatible {
            return Err(MigrationError::Manifest(format!(
                "forward schema is missing compatible index {}",
                requirement.name
            )));
        }
    }
    Ok(())
}

fn verify_serial_table_columns(
    transaction: &mut Transaction<'_>,
    schema: &str,
    table: &str,
    requirements: &[ColumnRequirement],
    default_matches: impl Fn(&str, Option<&str>) -> bool,
) -> Result<(), MigrationError> {
    let rows = transaction.query(
        "SELECT column_name,data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 ORDER BY ordinal_position",
        &[&schema, &table],
    )?;
    let columns_match = rows.len() == requirements.len()
        && rows.iter().zip(requirements).all(|(row, requirement)| {
            let name: String = row.get(0);
            let data_type: String = row.get(1);
            let length: Option<i32> = row.get(2);
            let nullable: String = row.get(3);
            let default: Option<String> = row.get(4);
            // Serial ownership and the exact id default expression are checked below.
            let valid_default = name == "id" || default_matches(&name, default.as_deref());
            name == requirement.name
                && data_type == requirement.data_type
                && length == requirement.character_maximum_length
                && (nullable == "YES") == requirement.nullable
                && valid_default
        });
    if !columns_match {
        return Err(MigrationError::Manifest(format!(
            "forward schema column/default contract mismatch for {table}"
        )));
    }
    verify_serial_table_identity(transaction, schema, table)
}

fn verify_serial_table_identity(
    transaction: &mut Transaction<'_>,
    schema: &str,
    table: &str,
) -> Result<(), MigrationError> {
    let key_matches: bool = transaction
        .query_one(
            r#"SELECT EXISTS (
            SELECT 1 FROM pg_catalog.pg_index AS metadata
            JOIN pg_catalog.pg_class AS table_class ON table_class.oid=metadata.indrelid
            JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=table_class.relnamespace
            JOIN pg_catalog.pg_attribute AS attribute
              ON attribute.attrelid=table_class.oid AND attribute.attname='id'
            JOIN pg_catalog.pg_attrdef AS default_value
              ON default_value.adrelid=table_class.oid AND default_value.adnum=attribute.attnum
            WHERE namespace.nspname=$1 AND table_class.relname=$2
              AND metadata.indisprimary AND metadata.indisvalid AND metadata.indisready
              AND metadata.indnkeyatts=1 AND metadata.indnatts=1
              AND metadata.indkey[0]=attribute.attnum
              AND pg_catalog.to_regclass(pg_catalog.pg_get_serial_sequence(
                  pg_catalog.format('%I.%I',$1::TEXT,$2::TEXT),'id')) =
                  pg_catalog.to_regclass(pg_catalog.format('%I.%I',$1::TEXT,$2::TEXT || '_id_seq'))
              AND pg_catalog.pg_get_expr(default_value.adbin,default_value.adrelid,false) =
                  pg_catalog.format('nextval(%L::regclass)', pg_catalog.to_regclass(
                      pg_catalog.format('%I.%I',$1::TEXT,$2::TEXT || '_id_seq'))::TEXT)
        )"#,
            &[&schema, &table],
        )?
        .get(0);
    if !key_matches {
        return Err(MigrationError::Manifest(format!(
            "forward schema primary key/sequence mismatch for {table}.id"
        )));
    }
    Ok(())
}

fn verify_added_columns(
    transaction: &mut Transaction<'_>,
    schema: &str,
    table: &str,
    requirements: &[ColumnRequirement],
    shape_matches: impl Fn(&str, bool, Option<&str>) -> bool,
) -> Result<(), MigrationError> {
    for requirement in requirements {
        let row=transaction.query_opt("SELECT data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name=$3",&[&schema,&table,&requirement.name])?;
        let matches = row.is_some_and(|row| {
            let data_type: String = row.get(0);
            let length: Option<i32> = row.get(1);
            let nullable: String = row.get(2);
            let default: Option<String> = row.get(3);
            let currency_compat =
                matches!(table, "subscription_orders" | "subscription_payment_events")
                    && matches!(requirement.name, "plan_currency" | "settlement_currency")
                    && matches!(length, Some(8 | 16));
            data_type == requirement.data_type
                && (length == requirement.character_maximum_length || currency_compat)
                && shape_matches(requirement.name, nullable == "YES", default.as_deref())
        });
        if !matches {
            return Err(MigrationError::Manifest(format!(
                "forward additive schema column/default mismatch for {table}.{}",
                requirement.name
            )));
        }
    }
    Ok(())
}

pub fn verify_relay_settlement_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_added_columns(
        transaction,
        schema,
        "user_subscriptions",
        &[column("quota_version", "bigint", None)],
        |_, nullable, default| !nullable && bigint_default_is_exact_zero(default),
    )?;
    verify_added_columns(
        transaction,
        schema,
        "subscription_pre_consume_records",
        &[
            column("billing_managed", "boolean", None),
            column("token_id", "bigint", None),
            column("token_consumed", "bigint", None),
            column("wallet_overflow", "boolean", None),
            column("actual_quota", "bigint", None),
            column("wallet_consumed", "bigint", None),
            column("reserved_version", "bigint", None),
        ],
        |name, nullable, default| {
            !nullable
                && if matches!(name, "billing_managed" | "wallet_overflow") {
                    default == Some("false")
                } else {
                    bigint_default_is_exact_zero(default)
                }
        },
    )?;
    let columns = &[
        column("reservation_id", "text", None),
        column("request_id", "text", None),
        column("user_id", "bigint", None),
        column("token_id", "bigint", None),
        column("channel_id", "bigint", None),
        column("model_name", "text", None),
        column("using_group", "text", None),
        column("is_stream", "boolean", None),
        column("funding_source", "text", None),
        column("expected_quota", "bigint", None),
        column("wallet_reserved", "bigint", None),
        column("subscription_id", "bigint", None),
        column("subscription_reserved", "bigint", None),
        column("reserved_version", "bigint", None),
        column("token_reserved", "bigint", None),
        column("wallet_overflow", "boolean", None),
        column("wallet_settled", "bigint", None),
        column("subscription_settled", "bigint", None),
        nullable_column("actual_quota", "bigint", None),
        column("price_snapshot", "jsonb", None),
        nullable_column("usage_snapshot", "jsonb", None),
        column("log_metadata", "jsonb", None),
        column("status", "text", None),
        column("created_at", "bigint", None),
        column("updated_at", "bigint", None),
    ];
    verify_added_columns(
        transaction,
        schema,
        "relay_settlement_records",
        columns,
        |name, nullable, default| {
            if nullable != matches!(name, "actual_quota" | "usage_snapshot") {
                return false;
            }
            match name {
                "wallet_reserved"
                | "subscription_id"
                | "subscription_reserved"
                | "reserved_version"
                | "token_reserved"
                | "wallet_settled"
                | "subscription_settled" => bigint_default_is_exact_zero(default),
                "wallet_overflow" => default == Some("false"),
                "log_metadata" => default == Some("'{}'::jsonb"),
                _ => default.is_none(),
            }
        },
    )?;
    let primary:bool=transaction.query_one("SELECT EXISTS(SELECT 1 FROM pg_catalog.pg_index i JOIN pg_catalog.pg_class t ON t.oid=i.indrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace JOIN pg_catalog.pg_attribute a ON a.attrelid=t.oid AND a.attname='reservation_id' WHERE n.nspname=$1 AND t.relname='relay_settlement_records' AND i.indisprimary AND i.indisunique AND i.indisvalid AND i.indisready AND i.indnkeyatts=1 AND i.indnatts=1 AND i.indkey[0]=a.attnum AND i.indexprs IS NULL AND i.indpred IS NULL)",&[&schema])?.get(0);
    if !primary {
        return Err(MigrationError::Manifest(
            "relay settlement reservation primary key is missing or altered".into(),
        ));
    }
    verify_indexes(
        transaction,
        schema,
        &[
            IndexRequirement {
                table: "subscription_pre_consume_records",
                name: "idx_subscription_pre_consume_records_request_id",
                unique: true,
                columns: &["request_id"],
                predicate: None,
            },
            IndexRequirement {
                table: "relay_settlement_records",
                name: "idx_relay_settlement_records_active_request",
                unique: true,
                columns: &["user_id", "request_id"],
                predicate: Some("(status <> 'refunded'::text)"),
            },
            IndexRequirement {
                table: "relay_settlement_records",
                name: "idx_relay_settlement_records_pending",
                unique: false,
                columns: &["updated_at", "reservation_id"],
                predicate: Some("(status = 'settling'::text)"),
            },
        ],
    )?;
    let expected: std::collections::BTreeMap<String, String> =
        serde_json::from_str(include_str!("../schema/relay-settlement-checks.json"))?;
    let rows=transaction.query("SELECT c.conname,pg_catalog.pg_get_constraintdef(c.oid),c.convalidated FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class t ON t.oid=c.conrelid JOIN pg_catalog.pg_namespace n ON n.oid=t.relnamespace WHERE n.nspname=$1 AND t.relname='relay_settlement_records' AND c.contype='c'",&[&schema])?;
    let mut found = std::collections::BTreeMap::new();
    for row in rows {
        if !row.get::<_, bool>(2) {
            return Err(MigrationError::Manifest(
                "relay settlement has an unvalidated check constraint".into(),
            ));
        }
        found.insert(row.get::<_, String>(0), row.get::<_, String>(1));
    }
    if found != expected {
        return Err(MigrationError::Manifest(
            "relay settlement money/state/JSON check constraints do not match contract 14".into(),
        ));
    }
    Ok(())
}

const WAFFO_PAYMENT_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("subscription_order_id", "bigint", None),
    column("event_id", "character varying", Some(255)),
    column("provider_order_id", "character varying", Some(255)),
    column("payment_id", "character varying", Some(255)),
    column("currency", "character varying", Some(8)),
    column("amount_micros", "bigint", None),
    column("payment_date", "bigint", None),
    column("period_start", "bigint", None),
    column("period_end", "bigint", None),
    column("payload", "text", None),
    column("received_at", "bigint", None),
];

const WAFFO_PERIOD_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("subscription_order_id", "bigint", None),
    column("event_id", "character varying", Some(255)),
    column("event_type", "character varying", Some(64)),
    column("provider_order_id", "character varying", Some(255)),
    column("billing_period", "character varying", Some(32)),
    column("currency", "character varying", Some(8)),
    column("amount_micros", "bigint", None),
    column("period_start", "bigint", None),
    column("period_end", "bigint", None),
    column("payload", "text", None),
    column("received_at", "bigint", None),
];

/// Verifies contract-8 evidence columns, serial primary keys and replay/matching indexes.
pub fn verify_waffo_subscription_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    for (table, requirements, indexes) in [
        (
            "waffo_pancake_subscription_payments",
            WAFFO_PAYMENT_COLUMNS,
            [
                ("subscription_order_id", false),
                ("event_id", true),
                ("provider_order_id", false),
                ("payment_id", true),
            ],
        ),
        (
            "waffo_pancake_subscription_periods",
            WAFFO_PERIOD_COLUMNS,
            [
                ("subscription_order_id", false),
                ("event_id", true),
                ("provider_order_id", false),
                ("period_end", false),
            ],
        ),
    ] {
        verify_serial_table_columns(transaction, schema, table, requirements, |name, default| {
            if table.ends_with("payments") && matches!(name, "period_start" | "period_end") {
                bigint_default_is_exact_zero(default)
            } else {
                default.is_none()
            }
        })?;
        for (name, unique) in indexes {
            verify_indexes(
                transaction,
                schema,
                &[IndexRequirement {
                    table,
                    name: &format!("idx_{table}_{name}"),
                    unique,
                    columns: &[name],
                    predicate: None,
                }],
            )?;
        }
    }
    Ok(())
}

const SUBSCRIPTION_PAYMENT_REFUND_COLUMNS: &[ColumnRequirement] = &[
    column("id", "bigint", None),
    column("subscription_order_id", "bigint", None),
    column("subscription_payment_event_id", "bigint", None),
    column("payment_provider", "character varying", Some(64)),
    column("provider_event_id", "character varying", Some(255)),
    column("currency", "character varying", Some(8)),
    column("amount_micros", "bigint", None),
    column("quota_revoked", "bigint", None),
    column("finance_ledger_entry_id", "bigint", None),
    column("created_time", "bigint", None),
];

const SUBSCRIPTION_PAYMENT_REFUND_INDEXES: &[IndexRequirement<'_>] = &[
    IndexRequirement {
        table: "subscription_payment_refunds",
        name: "idx_subscription_payment_refunds_subscription_order_id",
        unique: false,
        columns: &["subscription_order_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_payment_refunds",
        name: "idx_subscription_payment_refunds_subscription_payment_event_id",
        unique: false,
        columns: &["subscription_payment_event_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_payment_refunds",
        name: "idx_subscription_provider_refund",
        unique: true,
        columns: &["payment_provider", "provider_event_id"],
        predicate: None,
    },
    IndexRequirement {
        table: "subscription_payment_refunds",
        name: "idx_subscription_payment_refunds_finance_ledger_entry_id",
        unique: true,
        columns: &["finance_ledger_entry_id"],
        predicate: None,
    },
];

/// Verifies contract-9 refund evidence, serial identity and provider/ledger uniqueness.
pub fn verify_subscription_payment_refund_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_serial_table_columns(
        transaction,
        schema,
        "subscription_payment_refunds",
        SUBSCRIPTION_PAYMENT_REFUND_COLUMNS,
        |_name, default| default.is_none(),
    )?;
    verify_indexes(transaction, schema, SUBSCRIPTION_PAYMENT_REFUND_INDEXES)?;
    let has_foreign_keys: bool = transaction
        .query_one(
            r#"SELECT EXISTS (
                SELECT 1 FROM pg_catalog.pg_constraint AS constraint_metadata
                JOIN pg_catalog.pg_class AS table_class ON table_class.oid=constraint_metadata.conrelid
                JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=table_class.relnamespace
                WHERE namespace.nspname=$1 AND table_class.relname='subscription_payment_refunds'
                  AND constraint_metadata.contype='f'
            )"#,
            &[&schema],
        )?
        .get(0);
    if has_foreign_keys {
        return Err(MigrationError::Manifest(
            "forward schema foreign key mismatch for subscription_payment_refunds".to_owned(),
        ));
    }
    Ok(())
}

/// Verifies the additive contract-10 account-balance permission column.
///
/// The column lives on the existing `tokens` table, so this check deliberately
/// validates only its exact type, nullability, and default. It does not require
/// any historical key to be granted access.
pub fn verify_account_balance_access_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    let row = transaction.query_opt(
        "SELECT data_type,is_nullable,column_default FROM information_schema.columns \
         WHERE table_schema=$1 AND table_name='tokens' AND column_name='account_balance_read'",
        &[&schema],
    )?;
    let compatible = row.is_some_and(|row| {
        let data_type: String = row.get(0);
        let nullable: String = row.get(1);
        let default: Option<String> = row.get(2);
        data_type == "boolean"
            && nullable == "NO"
            && default.is_some_and(|value| {
                matches!(
                    value.trim().to_ascii_lowercase().as_str(),
                    "false" | "false::boolean"
                )
            })
    });
    if !compatible {
        return Err(MigrationError::Manifest(
            "forward schema account-balance access column/default contract mismatch".to_owned(),
        ));
    }
    Ok(())
}

/// Verifies contract-12 payment evidence, coupon/referral state and replay keys.
/// Existing Go tables may have additional columns; only the additive runtime
/// contract is required here, and existing rows are never rewritten.
pub fn verify_payment_runtime_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    let tables: &[(&str, &[ColumnRequirement])] = &[
        (
            "top_ups",
            &[
                column("referral_excluded", "boolean", None),
                column("platform_amount_micros", "bigint", None),
                column("credited_quota", "bigint", None),
                column("expected_amount_micros", "bigint", None),
                column("settled_amount_micros", "bigint", None),
                column("settlement_currency", "character varying", Some(16)),
                column("refunded_amount_micros", "bigint", None),
                column("refunded_quota", "bigint", None),
                nullable_column("discount_code_id", "bigint", None),
                nullable_column("discount_percent", "bigint", None),
                column("provider_product_id", "character varying", Some(255)),
                column("provider_store_id", "character varying", Some(255)),
                nullable_column("provider_event_id", "character varying", Some(255)),
                nullable_column("provider_transaction_id", "character varying", Some(255)),
                column("payment_checked_at", "bigint", None),
            ],
        ),
        (
            "users",
            &[
                column("referral_first_top_up_id", "bigint", None),
                column("payment_restriction_flags", "bigint", None),
                column("linux_do_gamification_score", "double precision", None),
                column("linux_do_score_updated_at", "bigint", None),
            ],
        ),
        (
            "discount_codes",
            &[
                column("id", "bigint", None),
                nullable_column("code", "character varying", Some(64)),
                nullable_column("name", "character varying", Some(120)),
                nullable_column("owner_user_id", "bigint", None),
                nullable_column("discount_percent", "bigint", None),
                column("min_amount", "bigint", None),
                column("status", "bigint", None),
                column("used_count", "bigint", None),
                column("max_uses", "bigint", None),
                nullable_column("created_by", "bigint", None),
                column("created_time", "bigint", None),
                column("updated_time", "bigint", None),
                column("starts_time", "bigint", None),
                column("expired_time", "bigint", None),
                nullable_column("deleted_at", "timestamp with time zone", None),
            ],
        ),
        (
            "discount_code_reservations",
            &[
                column("id", "bigint", None),
                column("discount_code_id", "bigint", None),
                column("top_up_trade_no", "character varying", Some(255)),
                column("user_id", "bigint", None),
                column("status", "character varying", Some(16)),
                column("expires_time", "bigint", None),
                column("created_time", "bigint", None),
                column("updated_time", "bigint", None),
            ],
        ),
        (
            "referral_rewards",
            &[
                column("id", "bigint", None),
                column("invitee_id", "bigint", None),
                column("inviter_id", "bigint", None),
                column("top_up_id", "bigint", None),
                column("quota", "bigint", None),
                column("status", "character varying", Some(24)),
                column("revoked_quota", "bigint", None),
                column("penalty_quota", "bigint", None),
                column("penalty_percent", "bigint", None),
                column("max_penalty_quota", "bigint", None),
                column("revision", "bigint", None),
                column("reason", "character varying", Some(32)),
                nullable_column("created_at", "bigint", None),
                nullable_column("updated_at", "bigint", None),
            ],
        ),
        (
            "referral_ledger_entries",
            &[
                column("id", "bigint", None),
                column("reward_id", "bigint", None),
                column("user_id", "bigint", None),
                column("event_key", "character varying", Some(160)),
                column("kind", "character varying", Some(32)),
                column("quota", "bigint", None),
                column("reason", "character varying", Some(32)),
                nullable_column("created_at", "bigint", None),
            ],
        ),
    ];
    for &(table, requirements) in tables {
        let rows = transaction.query(
            "SELECT column_name,data_type,character_maximum_length,is_nullable,column_default \
             FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2",
            &[&schema, &table],
        )?;
        for requirement in requirements {
            let matches = rows
                .iter()
                .find(|row| row.get::<_, String>(0) == requirement.name)
                .is_some_and(|row| {
                    let data_type: String = row.get(1);
                    let length: Option<i32> = row.get(2);
                    let nullable: String = row.get(3);
                    let default: Option<String> = row.get(4);
                    data_type == requirement.data_type
                        && length == requirement.character_maximum_length
                        && (nullable == "YES") == requirement.nullable
                        && (requirement.name == "id"
                            || payment_runtime_default_matches(
                                table,
                                requirement.name,
                                default.as_deref(),
                            ))
                });
            if !matches {
                return Err(MigrationError::Manifest(format!(
                    "forward payment schema column/default mismatch for {table}.{}",
                    requirement.name,
                )));
            }
        }
        if !matches!(table, "top_ups" | "users") {
            verify_serial_table_identity(transaction, schema, table)?;
        }
    }
    type PaymentIndex<'a> = (&'a str, &'a str, bool, &'a [&'a str]);
    let definitions: &[PaymentIndex<'_>] = &[
        (
            "top_ups",
            "idx_topup_provider_event",
            true,
            &["payment_provider", "provider_event_id"],
        ),
        (
            "top_ups",
            "idx_topup_provider_transaction",
            true,
            &["payment_provider", "provider_transaction_id"],
        ),
        (
            "top_ups",
            "idx_top_ups_discount_code_id",
            false,
            &["discount_code_id"],
        ),
        ("discount_codes", "idx_discount_codes_code", true, &["code"]),
        (
            "discount_codes",
            "idx_discount_codes_name",
            false,
            &["name"],
        ),
        (
            "discount_codes",
            "idx_discount_codes_owner_user_id",
            false,
            &["owner_user_id"],
        ),
        (
            "discount_codes",
            "idx_discount_codes_status",
            false,
            &["status"],
        ),
        (
            "discount_codes",
            "idx_discount_codes_created_by",
            false,
            &["created_by"],
        ),
        (
            "discount_codes",
            "idx_discount_codes_created_time",
            false,
            &["created_time"],
        ),
        (
            "discount_codes",
            "idx_discount_codes_deleted_at",
            false,
            &["deleted_at"],
        ),
        (
            "discount_code_reservations",
            "idx_discount_code_reservations_top_up_trade_no",
            true,
            &["top_up_trade_no"],
        ),
        (
            "discount_code_reservations",
            "idx_discount_code_reservations_discount_code_id",
            false,
            &["discount_code_id"],
        ),
        (
            "discount_code_reservations",
            "idx_discount_code_reservations_user_id",
            false,
            &["user_id"],
        ),
        (
            "discount_code_reservations",
            "idx_discount_code_reservations_status",
            false,
            &["status"],
        ),
        (
            "discount_code_reservations",
            "idx_discount_code_reservations_expires_time",
            false,
            &["expires_time"],
        ),
        (
            "referral_rewards",
            "idx_referral_rewards_invitee_id",
            true,
            &["invitee_id"],
        ),
        (
            "referral_rewards",
            "idx_referral_rewards_top_up_id",
            true,
            &["top_up_id"],
        ),
        (
            "referral_rewards",
            "idx_referral_rewards_inviter_id",
            false,
            &["inviter_id"],
        ),
        (
            "referral_ledger_entries",
            "idx_referral_ledger_entries_event_key",
            true,
            &["event_key"],
        ),
        (
            "referral_ledger_entries",
            "idx_referral_ledger_entries_reward_id",
            false,
            &["reward_id"],
        ),
        (
            "referral_ledger_entries",
            "idx_referral_ledger_entries_user_id",
            false,
            &["user_id"],
        ),
    ];
    let indexes = definitions
        .iter()
        .map(|&(table, name, unique, columns)| IndexRequirement {
            table,
            name,
            unique,
            columns,
            predicate: None,
        })
        .collect::<Vec<_>>();
    verify_indexes(transaction, schema, &indexes)
}

fn payment_runtime_default_matches(table: &str, name: &str, default: Option<&str>) -> bool {
    match (table, name) {
        ("top_ups", "referral_excluded") => matches!(default, Some("false" | "false::boolean")),
        ("top_ups", "settlement_currency" | "provider_product_id" | "provider_store_id")
        | ("referral_rewards", "reason") => varchar_default_is_exact(default, ""),
        ("discount_codes", "status") => matches!(default, Some("1" | "1::bigint")),
        (
            "top_ups",
            "platform_amount_micros"
            | "credited_quota"
            | "expected_amount_micros"
            | "settled_amount_micros"
            | "refunded_amount_micros"
            | "refunded_quota"
            | "payment_checked_at",
        )
        | (
            "users",
            "referral_first_top_up_id" | "payment_restriction_flags" | "linux_do_score_updated_at",
        )
        | (
            "discount_codes",
            "min_amount" | "used_count" | "max_uses" | "starts_time" | "expired_time",
        )
        | (
            "referral_rewards",
            "revoked_quota" | "penalty_quota" | "penalty_percent" | "max_penalty_quota"
            | "revision",
        ) => bigint_default_is_exact_zero(default),
        ("users", "linux_do_gamification_score") => matches!(
            default,
            Some("0" | "0::double precision" | "'0'::double precision")
        ),
        _ => default.is_none(),
    }
}

pub fn verify_payment_extensions_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_serial_table_columns(
        transaction,
        schema,
        "finance_ledger_entries",
        &[
            column("id", "bigint", None),
            column("entry_type", "character varying", Some(32)),
            column("category", "character varying", Some(64)),
            column("amount_micros", "bigint", None),
            column("currency", "character varying", Some(8)),
            column("direction", "smallint", None),
            column("payment_method", "character varying", Some(64)),
            column("payment_provider", "character varying", Some(64)),
            nullable_column("user_id", "bigint", None),
            column("source_type", "character varying", Some(32)),
            column("source_id", "character varying", Some(128)),
            column("token_units", "bigint", None),
            column("note", "character varying", Some(500)),
            column("occurred_at", "bigint", None),
            column("created_at", "bigint", None),
            column("created_by", "bigint", None),
            nullable_column("reversal_of_id", "bigint", None),
            nullable_column("idempotency_key", "character varying", Some(180)),
        ],
        |name, default| match name {
            "category" | "payment_method" | "payment_provider" | "source_id" | "note" => {
                varchar_default_is_exact(default, "")
            }
            "currency" => varchar_default_is_exact(default, "USD"),
            "token_units" => bigint_default_is_exact_zero(default),
            _ => default.is_none(),
        },
    )?;
    let names = [
        ("idempotency_key", true),
        ("entry_type", false),
        ("category", false),
        ("payment_method", false),
        ("payment_provider", false),
        ("user_id", false),
        ("source_type", false),
        ("source_id", false),
        ("occurred_at", false),
        ("created_at", false),
        ("created_by", false),
        ("reversal_of_id", false),
    ];
    for (column, unique) in names {
        let name = format!("idx_finance_ledger_entries_{column}");
        verify_indexes(
            transaction,
            schema,
            &[IndexRequirement {
                table: "finance_ledger_entries",
                name: &name,
                unique,
                columns: &[column],
                predicate: None,
            }],
        )?;
    }
    verify_added_columns(
        transaction,
        schema,
        "subscription_orders",
        &[
            nullable_column("plan_currency", "character varying", Some(8)),
            nullable_column("plan_snapshot", "text", None),
            nullable_column("user_subscription_id", "bigint", None),
            column("expected_amount_micros", "bigint", None),
            nullable_column("settlement_currency", "character varying", Some(8)),
            nullable_column("provider_product_id", "character varying", Some(255)),
            nullable_column("provider_store_id", "character varying", Some(255)),
            nullable_column("provider_subscription_id", "character varying", Some(255)),
            nullable_column("provider_subscription_state", "character varying", Some(32)),
            column("provider_event_time_millis", "bigint", None),
            nullable_column("current_period_start", "bigint", None),
            nullable_column("current_period_end", "bigint", None),
            column("refunded_amount_micros", "bigint", None),
            column("refunded_quota", "bigint", None),
        ],
        |name, nullable, default| match name {
            "expected_amount_micros"
            | "provider_event_time_millis"
            | "refunded_amount_micros"
            | "refunded_quota" => !nullable && bigint_default_is_exact_zero(default),
            "user_subscription_id" => bigint_default_is_exact_zero(default),
            "current_period_start" | "current_period_end" => {
                (nullable && default.is_none())
                    || (!nullable && bigint_default_is_exact_zero(default))
            }
            "provider_store_id" => nullable && default.is_none(),
            _ => {
                (nullable && default.is_none())
                    || (!nullable && varchar_default_is_exact(default, ""))
            }
        },
    )?;
    verify_added_columns(
        transaction,
        schema,
        "subscription_payment_events",
        &[
            column("id", "bigint", None),
            column("subscription_order_id", "bigint", None),
            column("payment_provider", "character varying", Some(64)),
            column("provider_event_id", "character varying", Some(255)),
            column("provider_transaction_id", "character varying", Some(255)),
            column("settlement_currency", "character varying", Some(8)),
            column("settlement_amount_micros", "bigint", None),
            nullable_column("period_start", "bigint", None),
            nullable_column("period_end", "bigint", None),
            nullable_column("created_time", "bigint", None),
        ],
        |name, nullable, default| match name {
            "id" => !nullable,
            "period_start" | "period_end" => nullable && default.is_none(),
            "created_time" => {
                (nullable && default.is_none())
                    || (!nullable && bigint_default_is_exact_zero(default))
            }
            "settlement_amount_micros" => {
                !nullable && (default.is_none() || bigint_default_is_exact_zero(default))
            }
            _ => !nullable && default.is_none(),
        },
    )?;
    verify_serial_table_identity(transaction, schema, "subscription_payment_events")?;
    verify_indexes(
        transaction,
        schema,
        &[
            IndexRequirement {
                table: "subscription_payment_events",
                name: "idx_subscription_payment_events_provider_event_id",
                unique: true,
                columns: &["provider_event_id"],
                predicate: None,
            },
            IndexRequirement {
                table: "subscription_payment_events",
                name: "idx_subscription_provider_transaction",
                unique: true,
                columns: &["payment_provider", "provider_transaction_id"],
                predicate: None,
            },
            IndexRequirement {
                table: "subscription_payment_events",
                name: "idx_subscription_order_period",
                unique: true,
                columns: &["subscription_order_id", "period_end"],
                predicate: None,
            },
            IndexRequirement {
                table: "subscription_payment_events",
                name: "idx_subscription_payment_events_subscription_order_id",
                unique: false,
                columns: &["subscription_order_id"],
                predicate: None,
            },
            IndexRequirement {
                table: "subscription_payment_events",
                name: "idx_subscription_payment_events_created_time",
                unique: false,
                columns: &["created_time"],
                predicate: None,
            },
        ],
    )?;
    Ok(())
}

pub fn verify_current_catalog_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_serial_table_columns(
        transaction,
        schema,
        "ai_directory_ads",
        &[
            column("id", "bigint", None),
            column("owner_user_id", "bigint", None),
            column("name", "character varying", Some(80)),
            column("url", "character varying", Some(2048)),
            column("summary", "character varying", Some(180)),
            column("description", "text", None),
            column("bid_cents", "bigint", None),
            column("charged_quota", "bigint", None),
            column("request_id", "character varying", Some(80)),
            column("status", "character varying", Some(16)),
            column("paid_at", "bigint", None),
            column("expires_at", "bigint", None),
            column("hidden_at", "bigint", None),
            column("refunded_at", "bigint", None),
        ],
        |name, default| match name {
            "summary" | "description" => varchar_default_is_exact(default, ""),
            "hidden_at" | "refunded_at" => bigint_default_is_exact_zero(default),
            _ => default.is_none(),
        },
    )?;
    let indexes = [
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_owner_user_id",
            unique: false,
            columns: &["owner_user_id"],
            predicate: None,
        },
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_bid_cents",
            unique: false,
            columns: &["bid_cents"],
            predicate: None,
        },
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_request_id",
            unique: true,
            columns: &["request_id"],
            predicate: None,
        },
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_status",
            unique: false,
            columns: &["status"],
            predicate: None,
        },
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_paid_at",
            unique: false,
            columns: &["paid_at"],
            predicate: None,
        },
        IndexRequirement {
            table: "ai_directory_ads",
            name: "idx_ai_directory_ads_expires_at",
            unique: false,
            columns: &["expires_at"],
            predicate: None,
        },
    ];
    verify_indexes(transaction, schema, &indexes)
}

/// Verifies token flags required by the mounted current-management routes.
pub fn verify_token_management_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_added_columns(
        transaction,
        schema,
        "tokens",
        &[
            column("oauth_managed", "boolean", None),
            column("one_time_reveal", "boolean", None),
            column("creation_source", "character varying", Some(32)),
        ],
        |name, nullable, default| {
            !nullable
                && match name {
                    "oauth_managed" | "one_time_reveal" => default == Some("false"),
                    "creation_source" => varchar_default_is_exact(default, "manual"),
                    _ => false,
                }
        },
    )?;
    verify_indexes(
        transaction,
        schema,
        &[
            IndexRequirement {
                table: "tokens",
                name: "idx_tokens_oauth_managed",
                unique: false,
                columns: &["oauth_managed"],
                predicate: None,
            },
            IndexRequirement {
                table: "tokens",
                name: "idx_tokens_creation_source",
                unique: false,
                columns: &["creation_source"],
                predicate: None,
            },
        ],
    )
}

fn company_column_default_is_exact(name: &str, default: Option<&str>) -> bool {
    match name {
        "postcode" | "state" | "business_name" | "tax_id" => varchar_default_is_exact(default, ""),
        "use_for_invoices" => default == Some("false"),
        _ => default.is_none(),
    }
}

/// Verifies current-Go durable acknowledgement keys without rewriting old reads.
pub fn verify_mandatory_announcement_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    verify_serial_table_columns(
        transaction,
        schema,
        "announcement_reads",
        &[
            column("id", "bigint", None),
            column("user_id", "bigint", None),
            column("announcement_id", "bigint", None),
            column("revision", "character varying", Some(64)),
            column("read_at", "bigint", None),
        ],
        |_, default| default.is_none(),
    )?;
    verify_indexes(
        transaction,
        schema,
        &[IndexRequirement {
            table: "announcement_reads",
            name: "idx_announcement_read",
            unique: true,
            columns: &["user_id", "announcement_id", "revision"],
            predicate: None,
        }],
    )
}

/// Verifies the complete contract-7 company billing profile and payment-failure catalog.
pub fn verify_company_billing_profile_schema(
    transaction: &mut Transaction<'_>,
    schema: &str,
) -> Result<(), MigrationError> {
    let rows = transaction.query(
        "SELECT column_name,data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name='company_billing_profiles' ORDER BY ordinal_position",
        &[&schema],
    )?;
    let columns_match = rows.len() == COMPANY_BILLING_PROFILE_COLUMNS.len()
        && rows
            .iter()
            .zip(COMPANY_BILLING_PROFILE_COLUMNS)
            .all(|(row, requirement)| {
                let name: String = row.get(0);
                let data_type: String = row.get(1);
                let length: Option<i32> = row.get(2);
                let nullable: String = row.get(3);
                let default: Option<String> = row.get(4);
                name == requirement.name
                    && data_type == requirement.data_type
                    && length == requirement.character_maximum_length
                    && nullable == "NO"
                    && company_column_default_is_exact(&name, default.as_deref())
            });
    if !columns_match {
        return Err(MigrationError::Manifest(
            "forward schema column contract mismatch for company_billing_profiles".to_owned(),
        ));
    }

    for table in ["top_ups", "subscription_orders"] {
        let row = transaction.query_opt(
            "SELECT data_type,character_maximum_length,is_nullable,column_default FROM information_schema.columns WHERE table_schema=$1 AND table_name=$2 AND column_name='failure_reason_code'",
            &[&schema, &table],
        )?;
        let compatible = row.is_some_and(|row| {
            let data_type: String = row.get(0);
            let length: Option<i32> = row.get(1);
            let nullable: String = row.get(2);
            let default: Option<String> = row.get(3);
            data_type == "character varying"
                && length == Some(64)
                && nullable == "NO"
                && varchar_default_is_exact(default.as_deref(), "")
        });
        if !compatible {
            return Err(MigrationError::Manifest(format!(
                "forward schema column mismatch for {table}.failure_reason_code"
            )));
        }
    }

    let primary = transaction.query_opt(
        r#"SELECT index_class.relname::TEXT,
                  metadata.indisunique,
                  metadata.indisvalid,
                  metadata.indisready,
                  metadata.indisprimary,
                  metadata.indisexclusion,
                  metadata.indexprs IS NULL,
                  metadata.indnkeyatts::INT,
                  metadata.indnatts::INT,
                  access_method.amname::TEXT,
                  ARRAY(
                      SELECT attribute.attname::TEXT
                      FROM pg_catalog.unnest(metadata.indkey::SMALLINT[]) WITH ORDINALITY AS key(attribute_number, ordinality)
                      JOIN pg_catalog.pg_attribute AS attribute
                        ON attribute.attrelid=metadata.indrelid
                       AND attribute.attnum=key.attribute_number
                      WHERE key.ordinality <= metadata.indnkeyatts
                      ORDER BY key.ordinality
                  ),
                  pg_catalog.pg_get_expr(metadata.indpred, metadata.indrelid)
             FROM pg_catalog.pg_index AS metadata
             JOIN pg_catalog.pg_class AS index_class ON index_class.oid=metadata.indexrelid
             JOIN pg_catalog.pg_am AS access_method ON access_method.oid=index_class.relam
             JOIN pg_catalog.pg_class AS table_class ON table_class.oid=metadata.indrelid
             JOIN pg_catalog.pg_namespace AS namespace ON namespace.oid=table_class.relnamespace
            WHERE namespace.nspname=$1
              AND table_class.relname='company_billing_profiles'
              AND metadata.indisprimary"#,
        &[&schema],
    )?;
    let primary_matches = primary.is_some_and(|row| {
        let name: String = row.get(0);
        let unique: bool = row.get(1);
        let valid: bool = row.get(2);
        let ready: bool = row.get(3);
        let primary: bool = row.get(4);
        let exclusion: bool = row.get(5);
        let no_expressions: bool = row.get(6);
        let key_count: i32 = row.get(7);
        let attribute_count: i32 = row.get(8);
        let method: String = row.get(9);
        let columns: Vec<String> = row.get(10);
        let predicate: Option<String> = row.get(11);
        name == "company_billing_profiles_pkey"
            && unique
            && valid
            && ready
            && primary
            && !exclusion
            && no_expressions
            && key_count == 1
            && attribute_count == 1
            && method == "btree"
            && columns == ["user_id"]
            && predicate.is_none()
    });
    if !primary_matches {
        return Err(MigrationError::Manifest(
            "forward schema primary key mismatch for company_billing_profiles".to_owned(),
        ));
    }

    let foreign_keys = transaction.query(
        r#"SELECT constraint_name.conname::TEXT,
                  constraint_name.convalidated,
                  constraint_name.condeferrable,
                  constraint_name.condeferred,
                  constraint_name.confdeltype::TEXT,
                  referenced_namespace.nspname::TEXT,
                  referenced_table.relname::TEXT,
                  ARRAY(
                      SELECT attribute.attname::TEXT
                      FROM pg_catalog.unnest(constraint_name.conkey) WITH ORDINALITY AS key(attribute_number, ordinality)
                      JOIN pg_catalog.pg_attribute AS attribute
                        ON attribute.attrelid=constraint_name.conrelid
                       AND attribute.attnum=key.attribute_number
                      ORDER BY key.ordinality
                  ),
                  ARRAY(
                      SELECT attribute.attname::TEXT
                      FROM pg_catalog.unnest(constraint_name.confkey) WITH ORDINALITY AS key(attribute_number, ordinality)
                      JOIN pg_catalog.pg_attribute AS attribute
                        ON attribute.attrelid=constraint_name.confrelid
                       AND attribute.attnum=key.attribute_number
                      ORDER BY key.ordinality
                  )
             FROM pg_catalog.pg_constraint AS constraint_name
             JOIN pg_catalog.pg_class AS source_table ON source_table.oid=constraint_name.conrelid
             JOIN pg_catalog.pg_namespace AS source_namespace ON source_namespace.oid=source_table.relnamespace
             JOIN pg_catalog.pg_class AS referenced_table ON referenced_table.oid=constraint_name.confrelid
             JOIN pg_catalog.pg_namespace AS referenced_namespace ON referenced_namespace.oid=referenced_table.relnamespace
            WHERE source_namespace.nspname=$1
              AND source_table.relname='company_billing_profiles'
              AND constraint_name.contype='f'"#,
        &[&schema],
    )?;
    let foreign_key_matches = foreign_keys.len() == 1
        && foreign_keys.first().is_some_and(|row| {
            let name: String = row.get(0);
            let validated: bool = row.get(1);
            let deferrable: bool = row.get(2);
            let deferred: bool = row.get(3);
            let delete_action: String = row.get(4);
            let referenced_schema: String = row.get(5);
            let referenced_table: String = row.get(6);
            let columns: Vec<String> = row.get(7);
            let referenced_columns: Vec<String> = row.get(8);
            name == "company_billing_profiles_user_id_fkey"
                && validated
                && deferrable
                && deferred
                && delete_action == "c"
                && referenced_schema == schema
                && referenced_table == "users"
                && columns == ["user_id"]
                && referenced_columns == ["id"]
        });
    if !foreign_key_matches {
        return Err(MigrationError::Manifest(
            "forward schema foreign key mismatch for company_billing_profiles.user_id".to_owned(),
        ));
    }
    Ok(())
}

#[cfg(test)]
mod tests {
    use std::fs;

    use super::*;

    #[test]
    fn contract_two_inventory_covers_every_mounted_bounty_table() {
        assert_eq!(TABLES.len(), 8);
        assert!(TABLES.iter().all(|(_, columns)| !columns.is_empty()));
        assert!(
            TABLES
                .iter()
                .flat_map(|(_, columns)| columns.iter())
                .any(|column| column.name == "reward_payout_key" && column.nullable)
        );
        assert!(
            TABLES
                .iter()
                .flat_map(|(_, columns)| columns.iter())
                .any(|column| column.name == "open_key" && column.nullable)
        );
    }

    #[test]
    fn contract_two_sql_is_schema_bound_and_lists_the_inventory() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../migrations/0002_open_source_bounty_schema.sql");
        let sql = fs::read_to_string(path).expect("read contract-2 migration");
        assert!(sql.contains("__LMM_APP_SCHEMA__"));
        assert!(!sql.contains("public."));
        for &(table, _) in TABLES {
            assert!(
                sql.contains(&format!("__LMM_APP_SCHEMA__.{table}")),
                "contract-2 SQL does not mention {table}"
            );
        }
    }

    #[test]
    fn contract_six_archived_default_requires_exact_zero() {
        for value in ["0", "0::bigint", "(0)::bigint", "'0'::bigint"] {
            assert!(bigint_default_is_exact_zero(Some(value)), "{value}");
        }
        for value in ["10", "100", "now()", "0 + 1", "'10'::bigint"] {
            assert!(!bigint_default_is_exact_zero(Some(value)), "{value}");
        }
        assert!(!bigint_default_is_exact_zero(None));
    }

    #[test]
    fn contract_six_varchar_defaults_are_exact() {
        for value in [
            "'available'::character varying",
            "'available'::varchar",
            "'available'::text",
        ] {
            assert!(varchar_default_is_exact(Some(value), "available"));
        }
        assert!(varchar_default_is_exact(Some("''::character varying"), ""));
        for value in ["'invalid'::character varying", "available", "NULL"] {
            assert!(!varchar_default_is_exact(Some(value), "available"));
        }
        assert!(!varchar_default_is_exact(None, "available"));
    }

    #[test]
    fn contract_six_verifier_inventory_covers_every_declared_key_and_index() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../migrations/0006_subscription_reset_system.sql");
        let sql = fs::read_to_string(path).expect("read contract-6 migration");

        assert_eq!(RESET_PRIMARY_KEYS.len(), 4);
        assert_eq!(RESET_SERIAL_COLUMNS.len(), 2);
        assert_eq!(RESET_DEFAULTS.len(), 9);
        assert_eq!(RESET_INDEXES.len(), 17);
        for requirement in RESET_INDEXES {
            assert!(
                sql.contains(requirement.name),
                "contract-6 SQL does not declare {}",
                requirement.name
            );
        }
    }

    #[test]
    fn contract_six_sql_is_additive_idempotent_and_has_no_deletion_blocking_foreign_keys() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../migrations/0006_subscription_reset_system.sql");
        let sql = fs::read_to_string(path).expect("read contract-6 migration");

        assert!(sql.contains("ADD COLUMN IF NOT EXISTS archived_at BIGINT NOT NULL DEFAULT 0"));
        assert_eq!(
            sql.matches("CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.subscription_reset_")
                .count(),
            4
        );
        assert!(sql.contains("ALTER TABLE __LMM_APP_SCHEMA__.subscription_plans"));
        assert!(sql.contains(
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_voucher_operation"
        ));
        assert!(
            sql.contains(
                "CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_event_operation"
            )
        );
        assert!(sql.contains(
            "CREATE UNIQUE INDEX IF NOT EXISTS idx_subscription_reset_operations_preview_token"
        ));
        // `forward` executes each content-addressed contract through Transaction::batch_execute;
        // PostgreSQL therefore forbids CREATE INDEX CONCURRENTLY in this replay-safe artifact.
        assert!(!sql.to_ascii_uppercase().contains("CONCURRENTLY"));
        assert!(!sql.to_ascii_uppercase().contains("FOREIGN KEY"));
    }

    #[test]
    fn contract_seven_sql_binds_profile_lifecycle_and_failure_reasons() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../migrations/0007_company_billing_profile.sql");
        let sql = fs::read_to_string(path).expect("read contract-7 migration");

        assert_eq!(COMPANY_BILLING_PROFILE_COLUMNS.len(), 10);
        assert!(
            sql.contains("CREATE TABLE IF NOT EXISTS __LMM_APP_SCHEMA__.company_billing_profiles")
        );
        assert!(sql.contains("CONSTRAINT company_billing_profiles_user_id_fkey"));
        assert!(sql.contains("REFERENCES __LMM_APP_SCHEMA__.users(id) ON DELETE CASCADE"));
        assert!(sql.contains("DEFERRABLE INITIALLY DEFERRED"));
        assert_eq!(
            sql.matches("ADD COLUMN IF NOT EXISTS failure_reason_code")
                .count(),
            2
        );
        assert!(!sql.contains("public."));
    }

    #[test]
    fn contract_three_sql_is_schema_bound_and_adds_current_dashboard_schema() {
        let path = std::path::Path::new(env!("CARGO_MANIFEST_DIR"))
            .join("../../migrations/0003_current_dashboard_schema.sql");
        let sql = fs::read_to_string(path).expect("read contract-3 migration");

        assert!(sql.contains("__LMM_APP_SCHEMA__.open_source_bounty_projects"));
        assert!(sql.contains("ADD COLUMN IF NOT EXISTS archived_at BIGINT"));
        assert!(sql.contains("idx_open_source_bounty_projects_archived_at"));
        assert!(sql.contains("idx_developer_access_requests_source"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.developer_access_requests"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.release_notes"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.release_note_reads"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.gifts"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.gift_claims"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.advanced_security_events"));
        assert!(sql.contains("__LMM_APP_SCHEMA__.personal_access_ips"));
        assert!(sql.contains("idx_release_note_version_revision"));
        assert!(sql.contains("idx_release_note_read_user_note"));
        assert!(sql.contains("idx_gift_user"));
        assert!(sql.contains("idx_advanced_security_events_created_at"));
        assert!(sql.contains("idx_personal_access_ips_ip"));
        assert!(sql.contains("user_id BIGINT PRIMARY KEY"));
        assert!(!sql.contains("public."));
    }
}
