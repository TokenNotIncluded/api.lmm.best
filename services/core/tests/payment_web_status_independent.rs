//! Independent read-only web status acceptance against real Rust RPC and fresh PG.
//! No provider request, proof acceptance, credit, refund or production DB is used.
#![cfg(unix)]
use lmm_core::{
    accounts::{Account, AccountKind, TeamRole},
    billing::ledger,
    funding::FundingPolicy,
    identity::{IdentityStore, InviteRequest},
    internal_rpc::{RpcServer, pb},
    payments::{
        Channel, PaymentsStore,
        rpc::{PaymentsRpc, Permissions},
    },
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::{
    fs,
    os::unix::fs::PermissionsExt,
    path::{Path, PathBuf},
    process::{Command, Stdio},
};
use tokio::sync::watch;

async fn money_snapshot(pool: &PgPool) -> Vec<(String, Value)> {
    let names: Vec<(String, String)> = sqlx::query_as(
        "SELECT schemaname,tablename FROM pg_tables WHERE schemaname IN ('core_billing','core_payments') ORDER BY schemaname,tablename"
    ).fetch_all(pool).await.unwrap();
    let mut rows = Vec::new();
    for (schema, table) in names {
        assert!(
            schema
                .chars()
                .all(|c| c.is_ascii_alphanumeric() || c == '_')
        );
        assert!(table.chars().all(|c| c.is_ascii_alphanumeric() || c == '_'));
        let value: Value = sqlx::query_scalar(&format!(
            "SELECT COALESCE(jsonb_agg(to_jsonb(r) ORDER BY to_jsonb(r)::text),'[]'::jsonb) FROM {schema}.{table} r"
        )).fetch_one(pool).await.unwrap();
        rows.push((format!("{schema}.{table}"), value));
    }
    let sequences: Value = sqlx::query_scalar(
        "SELECT COALESCE(jsonb_agg(jsonb_build_array(schemaname,sequencename,last_value) ORDER BY schemaname,sequencename),'[]'::jsonb) FROM pg_sequences WHERE schemaname IN ('core_billing','core_payments')"
    ).fetch_one(pool).await.unwrap();
    rows.push(("financial_sequences".into(), sequences));
    rows
}
fn write_private(path: &Path, raw: &[u8]) {
    fs::write(path, raw).unwrap();
    fs::set_permissions(path, fs::Permissions::from_mode(0o600)).unwrap();
}
fn run_go(
    binary: PathBuf,
    source: PathBuf,
    fixture: PathBuf,
    output: PathBuf,
    phase: &'static str,
) {
    let result = Command::new(binary)
        .args([
            "-test.run",
            "^TestPaymentWebStatusRealRust$",
            "-test.count=1",
            "-test.v",
            "-test.timeout=60s",
        ])
        .current_dir(source)
        .env("PAYMENT_WEB_STATUS_FIXTURE", fixture)
        .env("PAYMENT_WEB_STATUS_PHASE", phase)
        .stdin(Stdio::null())
        .output()
        .unwrap();
    write_private(
        &output.join(format!("go-{phase}.private.log")),
        &[&result.stdout[..], &result.stderr[..]].concat(),
    );
    assert!(
        result.status.success(),
        "real Go web status phase failed; inspect private owned log"
    );
    let log = String::from_utf8_lossy(&result.stdout);
    assert!(
        log.contains("--- PASS: TestPaymentWebStatusRealRust") && !log.contains("--- SKIP:"),
        "real Go selected tests were skipped or did not execute"
    );
}

#[sqlx::test(migrations = false)]
#[ignore = "requires fresh owned A09 cluster, private module runtime URL, and independently frozen Go test binary"]
async fn real_web_status_preserves_money_and_rechecks_live_authority(pool: PgPool) {
    let output =
        PathBuf::from(std::env::var("PAYMENT_WEB_STATUS_OUTPUT").expect("owned output required"));
    let socket_dir = PathBuf::from(
        std::env::var("PAYMENT_WEB_STATUS_SOCKET_DIR")
            .expect("owned short socket directory required"),
    );
    assert_eq!(
        fs::metadata(&output).unwrap().permissions().mode() & 0o777,
        0o700
    );
    fs::create_dir(&socket_dir).unwrap();
    fs::set_permissions(&socket_dir, fs::Permissions::from_mode(0o700)).unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    identity.init_database().await.unwrap();
    let core_db: String = sqlx::query_scalar("SELECT current_database()")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(
        core_db
            .chars()
            .all(|c| c.is_ascii_alphanumeric() || c == '_')
    );
    sqlx::raw_sql(&format!("REVOKE ALL ON DATABASE \"{core_db}\" FROM PUBLIC"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(lmm_core::abuse::SCHEMA)
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(ledger::SCHEMA).execute(&pool).await.unwrap();
    sqlx::raw_sql(lmm_core::payments::SCHEMA)
        .execute(&pool)
        .await
        .unwrap();
    let old = identity.bootstrap_user(7101, 0).await.unwrap();
    let owner = identity.refresh_session(&old.secret).await.unwrap();
    assert!(
        identity.authorize(&old.secret).await.is_err(),
        "rotation did not revoke the old credential"
    );
    let account = identity
        .authorize(&owner.secret)
        .await
        .unwrap()
        .owner_account_id;
    let other = identity.bootstrap_user(7102, 0).await.unwrap();
    let key = identity
        .issue_key(
            &owner.secret,
            Account {
                kind: AccountKind::Personal,
                id: 7101,
            },
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let store = PaymentsStore::new(
        pool.clone(),
        vec![Channel::stripe_test(
            1,
            "acct_web_status_fixture".into(),
            "whsec_web_status_fixture_only_00001".into(),
            10_000,
        )],
    )
    .unwrap();
    let intent = store
        .prepare(
            &owner.secret,
            pb::PrepareTopupRequest {
                mutation: Some(pb::MutationContext {
                    idempotency_key: "web_status_independent_prepare".into(),
                    expected_version: None,
                }),
                account_id: account,
                payment_channel_id: 1,
                currency: "USD".into(),
                amount_minor: 100,
            },
        )
        .await
        .unwrap();
    let team = identity.create_team(&owner.secret).await.unwrap();
    let invite = identity
        .invite(
            &owner.secret,
            team,
            &InviteRequest {
                recipient_user_id: 7102,
                role: TeamRole::Admin,
                can_spend: false,
                ttl_seconds: 3600,
            },
        )
        .await
        .unwrap();
    identity
        .accept_invite(&other.secret, &invite.secret)
        .await
        .unwrap();
    let team_account = identity
        .list_teams(&owner.secret, 0)
        .await
        .unwrap()
        .into_iter()
        .find(|t| t.id == team)
        .unwrap()
        .account_id;
    let team_intent = store
        .prepare(
            &owner.secret,
            pb::PrepareTopupRequest {
                mutation: Some(pb::MutationContext {
                    idempotency_key: "web_status_independent_team_prepare".into(),
                    expected_version: None,
                }),
                account_id: team_account,
                payment_channel_id: 1,
                currency: "USD".into(),
                amount_minor: 100,
            },
        )
        .await
        .unwrap();
    let socket = socket_dir.join("core.sock");
    let identity_token = "web_fixture_identity_service_credential_00001";
    let payment_token = "web_fixture_payment_service_credential_000001";
    let host_token = "web_fixture_host_service_credential_00000001";
    let rpc = PaymentsRpc::new(
        store,
        payment_token.as_bytes(),
        Permissions {
            read_user: true,
            ..Permissions::default()
        },
    )
    .unwrap();
    let server = RpcServer::bind(&socket, identity_token.as_bytes(), Some(identity.clone()))
        .unwrap()
        .with_payments(rpc);
    let (shutdown, receiver) = watch::channel(false);
    let handle = tokio::spawn(server.serve(receiver));
    let fixture = output.join("fixture.private.json");
    write_private(&fixture, &serde_json::to_vec(&json!({
        "socket":socket,"identity_token":identity_token,"payment_token":payment_token,"host_token":host_token,
        "owner":owner.secret,"old":old.secret,"other":other.secret,"api_key":key.secret,"user_id":7101,
        "intent":{"id":intent.intent_id,"account_id":intent.account_id,"created_by":intent.created_by_user_id,
            "provider":intent.provider,"merchant":intent.merchant,"environment":intent.environment,
            "currency":intent.currency,"amount_minor":intent.amount_minor,"credit_units":intent.credit_units,
            "credit_unit":intent.credit_unit,"key":intent.idempotency_key,"request_hash":intent.request_sha256},
        "team_intent":{"id":team_intent.intent_id,"account_id":team_intent.account_id,"created_by":team_intent.created_by_user_id,
            "provider":team_intent.provider,"merchant":team_intent.merchant,"environment":team_intent.environment,
            "currency":team_intent.currency,"amount_minor":team_intent.amount_minor,"credit_units":team_intent.credit_units,
            "credit_unit":team_intent.credit_unit,"key":team_intent.idempotency_key,"request_hash":team_intent.request_sha256},
        "database_url_file":std::env::var("PAYMENT_WEB_STATUS_MODULE_URL_FILE").unwrap(),
        "database_name":std::env::var("PAYMENT_WEB_STATUS_MODULE_DB").unwrap(),
    })).unwrap());
    let binary = PathBuf::from(std::env::var("PAYMENT_WEB_STATUS_GO_BINARY").unwrap());
    let source = PathBuf::from(std::env::var("PAYMENT_WEB_STATUS_GO_SOURCE").unwrap());
    let before = money_snapshot(&pool).await;
    let (b, s, f, o) = (
        binary.clone(),
        source.clone(),
        fixture.clone(),
        output.clone(),
    );
    let live = tokio::task::spawn_blocking(move || run_go(b, s, f, o, "live")).await;
    if live.is_err() {
        shutdown.send(true).unwrap();
        handle.await.unwrap().unwrap();
        assert_eq!(
            money_snapshot(&pool).await,
            before,
            "failed web phase mutated financial rows"
        );
        panic!("real Go live phase failed; private owned diagnostics retained");
    }
    assert_eq!(
        money_snapshot(&pool).await,
        before,
        "read-only web requests changed intent, receipts, proofs, journal, ledger or financial sequences"
    );
    identity
        .remove_member(&owner.secret, team, 7102, 1)
        .await
        .unwrap();
    assert!(
        identity.authorize(&other.secret).await.is_ok(),
        "team removal incorrectly revoked personal session"
    );
    identity.logout(&owner.secret).await.unwrap();
    let (b, s, f, o) = (binary, source, fixture, output.clone());
    let revoked = tokio::task::spawn_blocking(move || run_go(b, s, f, o, "revoked")).await;
    shutdown.send(true).unwrap();
    handle.await.unwrap().unwrap();
    assert_eq!(
        money_snapshot(&pool).await,
        before,
        "revoked web request changed financial rows or sequences"
    );
    revoked.unwrap();
    let journals: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(journals, 0, "status read minted money");
    write_private(&output.join("result.json"),&serde_json::to_vec_pretty(&json!({
        "status":"pass","core":"frozen committed Rust source","authority":"real CoreControl + CorePayments Unix RPC",
        "module_storage":"real isolated PG low-privilege runtime","provider_adapters":0,"app_workers_started":true,"queued_money_work":false,
        "journals":journals,"financial_rows_and_sequences_unchanged":true,
        "owner_prepared_read":true,"same_url_revoked_rejected":true,"old_rotated_credential_rejected":true,
        "other_account_rejected":true,"team_admin_noncreator_prepared_read":true,"team_membership_revoke_rejected":true,"host_anonymous_api_key_rejected":true,
        "default_disabled_503":true,"writes_forbidden":true,"host_lifecycle_admission":true,
        "review_rejected_unknown_preserved":true,"sensitive_dto_leakage":false,
        "app_injection":"actual app.Run subprocess; loopback HTTP; payments-only configuration","go_phases":2
    })).unwrap());
}
