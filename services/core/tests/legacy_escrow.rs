//! Independent PostgreSQL acceptance for the historical escrow slice.
//! All identities and financial material are synthetic and installation-owned.

use std::{env, fs::OpenOptions, io::Write, os::unix::fs::OpenOptionsExt, path::PathBuf};

use lmm_core::{
    accounts::{Account, AccountKind},
    billing::{
        Credits,
        ledger::{Action, Ledger, Outcome, Request},
        legacy_escrow::{CloseCommand, Error, LegacyEscrowStore, PayoutCommand, Receipt},
    },
    funding::FundingPolicy,
    identity::IdentityStore,
};
use serde_json::Value;
use sqlx::{PgPool, postgres::PgPoolOptions, types::Json};

const MIGRATION: &str = "independent-synthetic-escrow-v1";
const SOURCE_PROJECT: i64 = 9_007_199_254_740_993;
const SOURCE_CLAIM: i64 = 9_007_199_254_740_995;
const ARCHIVE: &str = "1111111111111111111111111111111111111111111111111111111111111111";
const MAPPING: &str = "2222222222222222222222222222222222222222222222222222222222222222";

fn private_fixture_url(name: &str) -> String {
    let value = env::var(name).expect("owned fixture URL is required");
    let url = reqwest::Url::parse(&value).expect("invalid owned fixture URL");
    assert_eq!(url.scheme(), "postgresql");
    assert_eq!(url.host_str(), Some("127.0.0.1"));
    let port = url.port().expect("explicit random fixture port required");
    assert!(port >= 1024 && ![18080, 18081, 50837, 47399].contains(&port));
    value
}

async fn owner_pool() -> PgPool {
    let role = env::var("LMM_ESCROW_OWNER_ROLE").expect("NOLOGIN owner role required");
    assert!(!role.is_empty());
    assert!(
        role.bytes()
            .all(|byte| byte.is_ascii_alphanumeric() || byte == b'_')
    );
    PgPoolOptions::new()
        .max_connections(4)
        .after_connect(move |connection, _| {
            let set_role = format!("SET ROLE {role}");
            Box::pin(async move {
                sqlx::query(&set_role).execute(connection).await?;
                Ok(())
            })
        })
        .connect(&private_fixture_url("LMM_ESCROW_OWNER_URL"))
        .await
        .expect("connect to fresh owned installation fixture")
}

async fn owner_import(pool: &PgPool, function: &str, body: &Value) -> Result<Value, sqlx::Error> {
    assert!(matches!(
        function,
        "import_opening_balance" | "import_legacy_escrow_batch"
    ));
    let mut transaction = pool.begin().await?;
    sqlx::raw_sql("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE; SET LOCAL synchronous_commit=on")
        .execute(&mut *transaction)
        .await?;
    let result =
        sqlx::query_scalar::<_, Json<Value>>(&format!("SELECT core_billing.{function}($1)"))
            .bind(Json(body))
            .fetch_one(&mut *transaction)
            .await;
    match result {
        Ok(Json(value)) => {
            transaction.commit().await?;
            Ok(value)
        }
        Err(error) => {
            transaction.rollback().await?;
            Err(error)
        }
    }
}

async fn ledger_readback(pool: &PgPool) {
    let unbalanced: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM (SELECT j.id FROM core_billing.ledger_journals j \
         LEFT JOIN core_billing.ledger_entries e ON e.journal_id=j.id GROUP BY j.id \
         HAVING count(e.journal_id)<2 OR sum(e.delta_units)<>0) invalid",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(unbalanced, 0, "each actual financial journal must balance");
    let stale: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.balance_state b LEFT JOIN \
         (SELECT ledger_account_id,sum(delta_units) AS units,count(*) AS n \
         FROM core_billing.ledger_entries GROUP BY ledger_account_id) e \
         ON e.ledger_account_id=b.ledger_account_id \
         WHERE b.balance_units::numeric<>COALESCE(e.units,0) OR b.revision<>COALESCE(e.n,0)",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(stale, 0, "projected balances must match immutable entries");
}

fn opening(account_id: i64, source_user_id: i64, available_units: i64, observed_at: i64) -> Value {
    serde_json::json!({
        "migration_id": MIGRATION, "source_archive_sha256": ARCHIVE,
        "mapping_sha256": MAPPING, "source_entity_key": format!("users:{source_user_id}"),
        "source_row_sha256": "33".repeat(32), "account_id": account_id,
        "available_units": available_units, "reserved_units": 0,
        "affiliate_units": 0, "observed_at": observed_at,
    })
}

async fn financial_snapshot(pool: &PgPool) -> Value {
    let mut snapshot = serde_json::Map::new();
    for table in [
        "ledger_accounts",
        "balance_state",
        "ledger_operations",
        "ledger_journals",
        "ledger_entries",
        "opening_import_runs",
        "opening_imports",
        "legacy_escrow_batch_runs",
        "legacy_escrow_projects",
        "legacy_escrow_claims",
        "legacy_escrow_disputes",
        "legacy_escrow_state",
        "legacy_escrow_operations",
    ] {
        let Json(value): Json<Value> = sqlx::query_scalar(&format!(
            "SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) \
             FROM core_billing.{table} t"
        ))
        .fetch_one(pool)
        .await
        .unwrap();
        snapshot.insert(table.into(), value);
    }
    Value::Object(snapshot)
}

async fn remaining(pool: &PgPool, project: i64) -> (i64, i64, bool) {
    sqlx::query_as(
        "SELECT remaining_units,revision,closed FROM core_billing.legacy_escrow_state \
                    WHERE migration_id=$1 AND source_project_id=$2",
    )
    .bind(MIGRATION)
    .bind(project)
    .fetch_one(pool)
    .await
    .unwrap()
}

fn receipt(name: &str, value: &Value) {
    let root = PathBuf::from(env::var("LMM_ESCROW_OUTPUT").expect("private output required"));
    assert!(root.is_absolute() && root.is_dir());
    let mut file = OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join(name))
        .expect("fresh private receipt required");
    serde_json::to_writer(&mut file, value).unwrap();
    file.write_all(b"\n").unwrap();
    file.sync_all().unwrap();
}

#[tokio::test]
#[ignore = "requires the newly allocated, private, installation-owned PostgreSQL fixture"]
async fn initialize_owned_schema() {
    let pool = owner_pool().await;
    let (current, owner, login): (String, String, bool) = sqlx::query_as(
        "SELECT current_user::text,r.rolname::text,r.rolcanlogin FROM pg_database d \
         JOIN pg_roles r ON r.oid=d.datdba WHERE d.datname=current_database()",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(current, owner);
    assert!(!login, "actual database owner must be NOLOGIN");
    lmm_core::core_schema::init(&pool).await.unwrap();
    lmm_core::core_schema::check(&pool).await.unwrap();
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_meta.component_contract")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 13);
    receipt(
        "schema-ready.json",
        &serde_json::json!({"status":"PASS", "components":count,
        "current_user_matches_actual_database_owner":true,"owner_nologin":true}),
    );
    pool.close().await;
}

fn payout(project: i64, challenge: i64, key: &str) -> PayoutCommand {
    PayoutCommand {
        migration_id: MIGRATION.into(),
        source_project_id: project,
        source_challenge_id: challenge,
        operation_key: key.into(),
    }
}

fn close(project: i64, key: &str) -> CloseCommand {
    CloseCommand {
        migration_id: MIGRATION.into(),
        source_project_id: project,
        operation_key: key.into(),
    }
}

fn frozen_claim(
    id: i64,
    user: i64,
    account: i64,
    units: i64,
    status: &str,
    rejected_at: i64,
) -> Value {
    let historical = status == "rejected";
    serde_json::json!({"source_challenge_id":id,"participant_user_id":user,"recipient_account_id":account,
        "source_row_sha256":"44".repeat(32),"source_reward_quota":if historical {1_000_000_000_000_i64} else {units*1000},
        "rights_status":if historical {"historical_rejection_guard_only"} else {"active_future_reward"},
        "payout_units":if historical {None} else {Some(units)},"status":status,
        "rejected_at":rejected_at,"paid_at":0})
}

fn frozen_project(
    id: i64,
    owner: i64,
    payer: i64,
    units: i64,
    claims: Vec<Value>,
    disputes: Vec<Value>,
) -> Value {
    serde_json::json!({"source_project_id":id,"owner_user_id":owner,"payer_account_id":payer,
        "source_row_sha256":"55".repeat(32),"source_parent_plan_sha256":"66".repeat(32),
        "escrow_units":units,"status":"published","claims":claims,"disputes":disputes})
}

fn assert_posted(receipt: &Receipt, expected_remaining: i64) -> i64 {
    assert_eq!(receipt.status, "posted");
    assert!(receipt.code.is_none());
    assert_eq!(receipt.remaining_units, expected_remaining);
    receipt
        .journal_id
        .expect("success requires an actual financial journal")
}

fn assert_business_rejection(receipt: &Receipt, code: &str) {
    assert_eq!(receipt.status, "rejected");
    assert_eq!(receipt.code.as_deref(), Some(code));
    assert!(receipt.journal_id.is_none());
}

fn native_request(key: &str, action: Action) -> Request {
    Request {
        scope: "independent-native-ledger".into(),
        operation_key: key.into(),
        actor_user_id: Some(101),
        reason: "Synthetic legacy/native custody isolation".into(),
        action,
    }
}

// @mark migration.bounty-custody role=test
#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
#[ignore = "requires the sealed fresh fixture, prior installation case and official runtime grants"]
async fn independent_legacy_escrow_postgres() {
    let owner = owner_pool().await;
    lmm_core::core_schema::check(&owner).await.unwrap();
    let runtime = PgPoolOptions::new()
        .max_connections(8)
        .connect(&private_fixture_url("LMM_ESCROW_RUNTIME_URL"))
        .await
        .unwrap();
    lmm_core::runtime_database::check(
        &runtime,
        lmm_core::runtime_database::RequiredCapabilities {
            web_identity: true,
            ..Default::default()
        },
    )
    .await
    .unwrap();
    let setup = IdentityStore::from_pool(owner.clone());
    let payer = setup.bootstrap_user(101, 1).await.unwrap();
    let recipient = setup.bootstrap_user(202, 1).await.unwrap();
    let admin = setup.bootstrap_user(303, 6).await.unwrap();
    let stranger = setup.bootstrap_user(404, 1).await.unwrap();
    let debt_payer = setup.bootstrap_user(505, 1).await.unwrap();
    let reserved_payer = setup.bootstrap_user(606, 1).await.unwrap();
    let payer_account = setup
        .authorize(&payer.secret)
        .await
        .unwrap()
        .owner_account_id;
    let recipient_account = setup
        .authorize(&recipient.secret)
        .await
        .unwrap()
        .owner_account_id;
    let admin_account = setup
        .authorize(&admin.secret)
        .await
        .unwrap()
        .owner_account_id;
    let stranger_account = setup
        .authorize(&stranger.secret)
        .await
        .unwrap()
        .owner_account_id;
    let debt_account = setup
        .authorize(&debt_payer.secret)
        .await
        .unwrap()
        .owner_account_id;
    let reserved_account = setup
        .authorize(&reserved_payer.secret)
        .await
        .unwrap()
        .owner_account_id;
    assert_ne!(
        payer_account, 101,
        "source user IDs must differ from native account IDs"
    );
    let now: i64 = sqlx::query_scalar("SELECT extract(epoch from clock_timestamp())::bigint")
        .fetch_one(&owner)
        .await
        .unwrap();
    for (account, user, amount) in [
        (payer_account, 101, 100),
        (recipient_account, 202, -10),
        (admin_account, 303, 0),
        (stranger_account, 404, 0),
        (debt_account, 505, -8),
    ] {
        owner_import(
            &owner,
            "import_opening_balance",
            &opening(account, user, amount, now),
        )
        .await
        .unwrap();
    }
    let mut unattributed_opening = opening(reserved_account, 606, 0, now);
    unattributed_opening["reserved_units"] = 5.into();
    owner_import(&owner, "import_opening_balance", &unattributed_opening)
        .await
        .unwrap();
    let disputed = frozen_claim(501, 202, recipient_account, 10, "submitted", 0);
    let mut recent_rejection =
        frozen_claim(401, 202, recipient_account, 10, "rejected", now - 3600);
    recent_rejection["rights_status"] = "active_future_reward".into();
    recent_rejection["payout_units"] = 10.into();
    recent_rejection["source_reward_quota"] = 10000.into();
    let batch = serde_json::json!({"migration_id":MIGRATION,"source_archive_sha256":ARCHIVE,
    "mapping_sha256":MAPPING,"review_proof_sha256":"77".repeat(32),"unit":"credit_500k_usd",
    "observed_at":now,"projects":[
        frozen_project(SOURCE_PROJECT,101,payer_account,100,vec![frozen_claim(SOURCE_CLAIM,202,recipient_account,30,"submitted",0),frozen_claim(102,202,recipient_account,20,"submitted",0)],vec![]),
        frozen_project(200,101,payer_account,70,vec![frozen_claim(201,404,stranger_account,20,"submitted",0)],vec![]),
        frozen_project(300,101,payer_account,10,vec![frozen_claim(301,202,recipient_account,10,"accepted",0)],vec![]),
        frozen_project(400,101,payer_account,10,vec![recent_rejection],vec![]),
        frozen_project(450,101,payer_account,10,vec![frozen_claim(451,202,recipient_account,10,"rejected",now-8*86400)],vec![]),
        frozen_project(500,101,payer_account,10,vec![disputed],vec![serde_json::json!({"source_dispute_id":501,"source_challenge_id":501,
            "source_row_sha256":"88".repeat(32),"status":"open","reward_quota_snapshot":10000,
            "project_escrow_quota_snapshot":20000,"tip_quota_snapshot":3000})]),
        frozen_project(600,505,debt_account,12,vec![],vec![]),
        frozen_project(700,101,payer_account,40,vec![frozen_claim(701,202,recipient_account,15,"submitted",0)],vec![]),
        frozen_project(800,101,payer_account,30,vec![frozen_claim(801,202,recipient_account,10,"submitted",0)],vec![]),
    ]});
    let ledger = Ledger::from_pool(owner.clone());
    let initial = financial_snapshot(&owner).await;
    let mut bad_inputs = Vec::new();
    for field in ["source_archive_sha256", "mapping_sha256"] {
        let mut bad = batch.clone();
        bad[field] = Value::String("ff".repeat(32));
        bad_inputs.push(bad);
    }
    let mut bad = batch.clone();
    bad["unit"] = "legacy-quota".into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"].as_array_mut().unwrap().push(frozen_project(
        900,
        606,
        reserved_account,
        5,
        vec![],
        vec![],
    ));
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][1]["claims"][0]["recipient_account_id"] = payer_account.into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][0]["escrow_units"] = (-1).into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][0]["escrow_units"] = true.into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][0]["claims"][0]["payout_units"] = 200.into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][1]["source_project_id"] = SOURCE_PROJECT.into();
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][3]["claims"][0]["rights_status"] = "historical_rejection_guard_only".into();
    bad["projects"][3]["claims"][0]["payout_units"] = Value::Null;
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][5]["claims"][0]["status"] = "rejected".into();
    bad["projects"][5]["claims"][0]["rejected_at"] = (now - 8 * 86400).into();
    bad["projects"][5]["claims"][0]["rights_status"] = "historical_rejection_guard_only".into();
    bad["projects"][5]["claims"][0]["payout_units"] = Value::Null;
    bad_inputs.push(bad);
    let mut bad = batch.clone();
    bad["projects"][5]["disputes"][0]["status"] = "resolved_paid".into();
    bad_inputs.push(bad);
    for invalid_id in [Value::Bool(true), serde_json::json!(1.5)] {
        let mut bad = batch.clone();
        bad["projects"][0]["source_project_id"] = invalid_id;
        bad_inputs.push(bad);
    }
    let mut bad = batch.clone();
    bad["projects"][0]["escrow_units"] = 9_007_199_254_740_992_i64.into();
    bad_inputs.push(bad);
    for bad in bad_inputs {
        let error = owner_import(&owner, "import_legacy_escrow_batch", &bad)
            .await
            .unwrap_err();
        assert!(
            matches!(
                error.as_database_error().and_then(|e| e.code()).as_deref(),
                Some("P1000" | "P1001" | "23505")
            ),
            "invalid batch must fail with a domain/database rejection, not a driver outage"
        );
        assert_eq!(
            financial_snapshot(&owner).await,
            initial,
            "invalid batch must leave no partial import"
        );
    }
    let imported = owner_import(&owner, "import_legacy_escrow_batch", &batch)
        .await
        .unwrap();
    let exact_ids: (i64, i64) = sqlx::query_as(
        "SELECT source_project_id,source_challenge_id FROM core_billing.legacy_escrow_claims \
        WHERE migration_id=$1 AND source_challenge_id=$2",
    )
    .bind(MIGRATION)
    .bind(SOURCE_CLAIM)
    .fetch_one(&owner)
    .await
    .unwrap();
    assert_eq!(exact_ids, (SOURCE_PROJECT, SOURCE_CLAIM));
    assert_eq!(
        ledger.balance(payer_account).await.unwrap().spendable_units,
        100,
        "historical escrow import must not debit the already-opened wallet"
    );
    assert_eq!(ledger.balance(debt_account).await.unwrap().debt_units, -8);
    let after_import = financial_snapshot(&owner).await;
    assert_eq!(
        owner_import(&owner, "import_legacy_escrow_batch", &batch)
            .await
            .unwrap(),
        imported
    );
    assert_eq!(financial_snapshot(&owner).await, after_import);
    let mut conflict = batch.clone();
    conflict["review_proof_sha256"] = "99".repeat(32).into();
    assert!(
        owner_import(&owner, "import_legacy_escrow_batch", &conflict)
            .await
            .is_err()
    );
    assert_eq!(financial_snapshot(&owner).await, after_import);
    let store = LegacyEscrowStore::from_pool(runtime.clone());
    let api = setup
        .issue_key(
            &payer.secret,
            Account {
                kind: AccountKind::Personal,
                id: 101,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    for credential in [&api.secret, &admin.secret, &stranger.secret] {
        assert!(matches!(
            store
                .payout(
                    credential,
                    &payout(SOURCE_PROJECT, SOURCE_CLAIM, "denied-actor")
                )
                .await,
            Err(Error::Forbidden)
        ));
        assert_eq!(financial_snapshot(&owner).await, after_import);
    }
    for project in [SOURCE_PROJECT, 200, 300, 400, 500] {
        let baseline = financial_snapshot(&owner).await;
        let protected_state = remaining(&owner, project).await;
        let result = store
            .close(&payer.secret, &close(project, &format!("guard-{project}")))
            .await
            .unwrap();
        assert_business_rejection(
            &result,
            match project {
                400 => "appeal_window",
                500 => "open_dispute",
                _ => "active_claim",
            },
        );
        // A business rejection may persist its immutable operation receipt, but no funds can move.
        assert_eq!(
            remaining(&owner, project).await.0,
            match project {
                SOURCE_PROJECT => 100,
                200 => 70,
                _ => 10,
            }
        );
        assert_eq!(
            remaining(&owner, project).await,
            protected_state,
            "a protected close must not close or revise the project"
        );
        assert_eq!(
            ledger.balance(payer_account).await.unwrap().spendable_units,
            100
        );
        assert_eq!(
            financial_snapshot(&owner).await["ledger_entries"],
            baseline["ledger_entries"]
        );
    }
    for (project, claim) in [(300, 301), (400, 401), (450, 451), (500, 501)] {
        let before = remaining(&owner, project).await;
        let result = store
            .payout(
                &payer.secret,
                &payout(project, claim, &format!("guard-pay-{project}")),
            )
            .await
            .unwrap();
        assert_business_rejection(
            &result,
            if project == 500 {
                "open_dispute"
            } else {
                "claim_not_submitted"
            },
        );
        assert_eq!(remaining(&owner, project).await, before);
    }
    let first_command = payout(SOURCE_PROJECT, SOURCE_CLAIM, "partial-one");
    let first = store.payout(&payer.secret, &first_command).await.unwrap();
    let first_journal = assert_posted(&first, 70);
    let Json(original_command): Json<Value>=sqlx::query_scalar("SELECT request->'command' FROM core_billing.legacy_escrow_operations WHERE operation_key='partial-one'")
        .fetch_one(&owner).await.unwrap();
    assert_eq!(
        original_command["source_project_id"].as_i64(),
        Some(SOURCE_PROJECT)
    );
    assert_eq!(
        original_command["source_challenge_id"].as_i64(),
        Some(SOURCE_CLAIM)
    );
    assert_eq!(
        ledger.balance(recipient_account).await.unwrap().debt_units,
        0
    );
    assert_eq!(
        ledger
            .balance(recipient_account)
            .await
            .unwrap()
            .spendable_units,
        20
    );
    assert_eq!(remaining(&owner, 200).await.0, 70);
    assert_eq!(
        store.payout(&payer.secret, &first_command).await.unwrap(),
        first
    );
    let isolated = financial_snapshot(&owner).await;
    assert!(matches!(
        store.payout(&admin.secret, &first_command).await,
        Err(Error::Forbidden)
    ));
    assert!(matches!(
        store
            .payout(
                &payer.secret,
                &payout(SOURCE_PROJECT, 201, "foreign-project-claim")
            )
            .await,
        Err(Error::Missing)
    ));
    assert_eq!(financial_snapshot(&owner).await, isolated);
    assert_eq!(remaining(&owner, SOURCE_PROJECT).await.0, 70);
    let mut collision = first_command.clone();
    collision.source_challenge_id = 102;
    assert!(matches!(
        store.payout(&payer.secret, &collision).await,
        Err(Error::Conflict)
    ));
    let second = store
        .payout(&payer.secret, &payout(SOURCE_PROJECT, 102, "partial-two"))
        .await
        .unwrap();
    assert_posted(&second, 50);
    assert_eq!(
        ledger
            .balance(recipient_account)
            .await
            .unwrap()
            .spendable_units,
        40
    );
    let refund = store
        .close(
            &payer.secret,
            &close(SOURCE_PROJECT, "close-original-payer"),
        )
        .await
        .unwrap();
    assert_posted(&refund, 0);
    assert_eq!(
        ledger.balance(payer_account).await.unwrap().spendable_units,
        150
    );
    assert!(remaining(&owner, SOURCE_PROJECT).await.2);
    assert_eq!(
        store
            .close(
                &payer.secret,
                &close(SOURCE_PROJECT, "close-original-payer")
            )
            .await
            .unwrap(),
        refund
    );
    let historical_close = store
        .close(&payer.secret, &close(450, "close-historical-guard"))
        .await
        .unwrap();
    assert_posted(&historical_close, 0);
    assert_eq!(
        ledger.balance(payer_account).await.unwrap().spendable_units,
        160,
        "unpayable original-unit guard must not become a new monetary liability"
    );
    let debt_refund = store
        .close(&debt_payer.secret, &close(600, "close-debt-payer"))
        .await
        .unwrap();
    assert_posted(&debt_refund, 0);
    assert_eq!(ledger.balance(debt_account).await.unwrap().debt_units, 0);
    assert_eq!(
        ledger.balance(debt_account).await.unwrap().spendable_units,
        4
    );
    // Late write failure must roll back ledger legs, project state and idempotency together.
    sqlx::raw_sql("CREATE FUNCTION core_billing.independent_escrow_fail() RETURNS trigger LANGUAGE plpgsql AS $$ \
        BEGIN IF NEW.operation_key='fault-original-key' THEN RAISE EXCEPTION 'synthetic late escrow fault'; END IF; RETURN NEW; END $$; \
        CREATE TRIGGER independent_escrow_fail BEFORE INSERT ON core_billing.legacy_escrow_operations \
        FOR EACH ROW EXECUTE FUNCTION core_billing.independent_escrow_fail()")
        .execute(&owner).await.unwrap();
    let baseline = financial_snapshot(&owner).await;
    let fault = payout(700, 701, "fault-original-key");
    assert!(matches!(
        store.payout(&payer.secret, &fault).await,
        Err(Error::Storage)
    ));
    assert_eq!(financial_snapshot(&owner).await, baseline);
    sqlx::raw_sql(
        "DROP TRIGGER independent_escrow_fail ON core_billing.legacy_escrow_operations; \
        DROP FUNCTION core_billing.independent_escrow_fail()",
    )
    .execute(&owner)
    .await
    .unwrap();
    let recovered = store.payout(&payer.secret, &fault).await.unwrap();
    assert_posted(&recovered, 25);
    let concurrent = payout(800, 801, "concurrent-original-key");
    let (a, b) = tokio::join!(
        store.payout(&payer.secret, &concurrent),
        store.payout(&payer.secret, &concurrent)
    );
    let a = a.unwrap();
    let b = b.unwrap();
    assert_eq!(a, b);
    assert_posted(&a, 20);
    let replay = store
        .payout(&payer.secret, &payout(800, 801, "different-key-same-claim"))
        .await
        .unwrap();
    assert_business_rejection(&replay, "claim_already_paid");
    assert_eq!(remaining(&owner, 800).await.0, 20);
    let native = Ledger::from_pool(runtime.clone());
    let legacy_opening: i64=sqlx::query_scalar("SELECT opening_journal_id FROM core_billing.legacy_escrow_projects WHERE migration_id=$1 AND source_project_id=200")
        .bind(MIGRATION).fetch_one(&owner).await.unwrap();
    let baseline = financial_snapshot(&owner).await;
    for (key, action) in [
        (
            "legacy-not-capture",
            Action::Capture {
                reservation_journal_id: legacy_opening,
                amount_units: Credits::try_from(1).unwrap(),
            },
        ),
        (
            "legacy-not-release",
            Action::Release {
                reservation_journal_id: legacy_opening,
            },
        ),
        (
            "legacy-not-reverse",
            Action::Reverse {
                journal_id: legacy_opening,
            },
        ),
    ] {
        assert!(matches!(
            native.execute(&native_request(key, action)).await.unwrap(),
            Outcome::Rejected { .. }
        ));
        assert_eq!(
            financial_snapshot(&owner).await["ledger_entries"],
            baseline["ledger_entries"]
        );
        assert_eq!(
            financial_snapshot(&owner).await["legacy_escrow_state"],
            baseline["legacy_escrow_state"]
        );
    }
    let native_hold = match native
        .execute(&native_request(
            "native-hold",
            Action::Reserve {
                account_id: payer_account,
                amount_units: Credits::try_from(5).unwrap(),
            },
        ))
        .await
        .unwrap()
    {
        Outcome::Posted { journal_id, .. } => journal_id,
        other => panic!("native hold rejected: {other:?}"),
    };
    assert_eq!(
        ledger.balance(payer_account).await.unwrap().spendable_units,
        155
    );
    assert_eq!(
        financial_snapshot(&owner).await["legacy_escrow_state"],
        baseline["legacy_escrow_state"]
    );
    assert!(matches!(
        native
            .execute(&native_request(
                "native-cannot-borrow-legacy",
                Action::Capture {
                    reservation_journal_id: native_hold,
                    amount_units: Credits::try_from(8).unwrap()
                }
            ))
            .await
            .unwrap(),
        Outcome::Rejected { .. }
    ));
    assert!(matches!(
        native
            .execute(&native_request(
                "native-release",
                Action::Release {
                    reservation_journal_id: native_hold
                }
            ))
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
    assert_eq!(
        ledger.balance(payer_account).await.unwrap().spendable_units,
        160
    );
    assert_eq!(
        financial_snapshot(&owner).await["legacy_escrow_state"],
        baseline["legacy_escrow_state"]
    );
    let current_auth_baseline = financial_snapshot(&owner).await;
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1")
        .bind(payer.id)
        .execute(&owner)
        .await
        .unwrap();
    assert!(matches!(
        store.payout(&payer.secret, &first_command).await,
        Err(Error::Unauthorized)
    ));
    assert_eq!(financial_snapshot(&owner).await, current_auth_baseline);
    sqlx::query("UPDATE core_identity.users SET active=false WHERE id=505")
        .execute(&owner)
        .await
        .unwrap();
    assert!(matches!(
        store
            .close(&debt_payer.secret, &close(600, "close-debt-payer"))
            .await,
        Err(Error::Unauthorized)
    ));
    assert_eq!(financial_snapshot(&owner).await, current_auth_baseline);
    // Runtime may use narrow financial functions, never offline imports or direct custody writes.
    for statement in [
        "UPDATE core_billing.legacy_escrow_state SET remaining_units=0",
        "DELETE FROM core_billing.legacy_escrow_operations",
        "SELECT core_billing.import_legacy_escrow_batch('{}'::jsonb)",
        "SELECT core_billing.import_opening_balance('{}'::jsonb)",
    ] {
        let error = sqlx::query(statement).execute(&runtime).await.unwrap_err();
        assert_eq!(
            error.as_database_error().and_then(|e| e.code()).as_deref(),
            Some("42501")
        );
    }
    assert_eq!(financial_snapshot(&owner).await, current_auth_baseline);
    ledger_readback(&owner).await;
    let first_legs: Vec<(String,i64)> = sqlx::query_as("SELECT a.bucket,e.delta_units FROM core_billing.ledger_entries e \
        JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id WHERE e.journal_id=$1 ORDER BY a.bucket,e.delta_units")
        .bind(first_journal).fetch_all(&owner).await.unwrap();
    assert!(
        first_legs
            .iter()
            .any(|(bucket, amount)| bucket == "debt" && *amount == 10)
    );
    assert!(
        first_legs
            .iter()
            .any(|(bucket, amount)| bucket == "wallet" && *amount == 20)
    );
    let wrong_pools: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM (SELECT p.payer_account_id,sum(s.remaining_units) AS remaining \
         FROM core_billing.legacy_escrow_projects p \
         JOIN core_billing.legacy_escrow_state s USING(migration_id,source_project_id) GROUP BY p.payer_account_id) expected \
         JOIN core_billing.ledger_accounts a ON a.owner_account_id=expected.payer_account_id AND a.bucket='reserved' \
         JOIN core_billing.balance_state b ON b.ledger_account_id=a.id WHERE b.balance_units::numeric<>expected.remaining",
    ).fetch_one(&owner).await.unwrap();
    assert_eq!(
        wrong_pools, 0,
        "actual payer custody must equal the sum of its projects' remaining amounts when no native holds exist"
    );
    let own_pool_debit: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_billing.legacy_escrow_projects p \
         JOIN core_billing.ledger_entries funding ON funding.journal_id=p.opening_journal_id AND funding.delta_units=p.escrow_units \
         JOIN core_billing.ledger_entries paid ON paid.ledger_account_id=funding.ledger_account_id \
         JOIN core_billing.ledger_journals j ON j.id=paid.journal_id AND j.parent_journal_id=p.opening_journal_id \
         WHERE p.migration_id=$1 AND p.source_project_id=$3 AND paid.journal_id=$2 AND paid.delta_units=-30",
    ).bind(MIGRATION).bind(first_journal).bind(SOURCE_PROJECT).fetch_one(&owner).await.unwrap();
    assert_eq!(
        own_pool_debit, 1,
        "payout must debit the original project's funded pool exactly once"
    );
    lmm_core::core_schema::check(&owner).await.unwrap();
    receipt("financial-readback.json", &financial_snapshot(&owner).await);
    receipt("synthetic-import.json", &batch);
    receipt(
        "synthetic-identity-map.json",
        &serde_json::json!([
            {"user_id":101,"account_id":payer_account},{"user_id":202,"account_id":recipient_account},
            {"user_id":303,"account_id":admin_account},{"user_id":404,"account_id":stranger_account},
            {"user_id":505,"account_id":debt_account},{"user_id":606,"account_id":reserved_account}
        ]),
    );
    receipt(
        "acceptance.json",
        &serde_json::json!({"status":"PASS","scope":"synthetic-core-legacy-escrow-pg-slice",
        "independent_test":true,"actual_components":13,"opening_archive_mapping_bound":true,
        "import_does_not_debit_wallet":true,"frozen_claim_partial_project_payouts":true,
        "native_account_ids_differ_from_source_users":true,"project_isolation":true,
        "positive_bigint_source_ids_preserved":true,"bool_fraction_ids_and_oversized_money_rejected":true,
        "close_original_payer_and_guards":true,"debt_first_payout_and_close":true,
        "idempotency_collision_concurrency":true,"late_fault_atomic_rollback":true,
        "runtime_acl_and_current_revocation":true,"immutable_ledger_balanced":true,
        "whole_bounty_acceptance":false,"real_source_material_used":false}),
    );
    runtime.close().await;
    owner.close().await;
}

#[tokio::test]
#[ignore = "requires the sealed owned PostgreSQL fixture and actual canonical runtime grants"]
async fn legacy_escrow_runtime_authority_probe() {
    use lmm_core::runtime_database::{self, RequiredCapabilities, RuntimeDatabaseError};
    let owner = owner_pool().await;
    let url = private_fixture_url("LMM_ESCROW_RUNTIME_URL");
    assert_eq!(
        reqwest::Url::parse(&url).unwrap().username(),
        "lmm_core_runtime"
    );
    let runtime = PgPoolOptions::new()
        .max_connections(2)
        .connect(&url)
        .await
        .unwrap();
    let requirements = RequiredCapabilities {
        web_identity: true,
        ..Default::default()
    };
    runtime_database::check(&runtime, requirements)
        .await
        .unwrap();
    let initial = financial_snapshot(&owner).await;
    let mutations = [
        (
            "escrow-update-column",
            "GRANT UPDATE(remaining_units) ON core_billing.legacy_escrow_state TO lmm_core_runtime",
            "REVOKE UPDATE(remaining_units) ON core_billing.legacy_escrow_state FROM lmm_core_runtime",
        ),
        (
            "escrow-insert",
            "GRANT INSERT ON core_billing.legacy_escrow_claims TO lmm_core_runtime",
            "REVOKE INSERT ON core_billing.legacy_escrow_claims FROM lmm_core_runtime",
        ),
        (
            "escrow-delete",
            "GRANT DELETE ON core_billing.legacy_escrow_operations TO lmm_core_runtime",
            "REVOKE DELETE ON core_billing.legacy_escrow_operations FROM lmm_core_runtime",
        ),
        (
            "escrow-trigger",
            "GRANT TRIGGER ON core_billing.legacy_escrow_projects TO lmm_core_runtime",
            "REVOKE TRIGGER ON core_billing.legacy_escrow_projects FROM lmm_core_runtime",
        ),
        (
            "owner-import-execute",
            "GRANT EXECUTE ON FUNCTION core_billing.import_legacy_escrow_batch(jsonb) TO lmm_core_runtime",
            "REVOKE EXECUTE ON FUNCTION core_billing.import_legacy_escrow_batch(jsonb) FROM lmm_core_runtime",
        ),
        (
            "public-definer-execute",
            "GRANT EXECUTE ON FUNCTION core_billing.legacy_escrow_finalize(jsonb,bigint,bigint) TO PUBLIC",
            "REVOKE EXECUTE ON FUNCTION core_billing.legacy_escrow_finalize(jsonb,bigint,bigint) FROM PUBLIC",
        ),
        (
            "unsafe-definer-search-path",
            "ALTER FUNCTION core_billing.legacy_escrow_finalize(jsonb,bigint,bigint) SET search_path=public,pg_catalog",
            "ALTER FUNCTION core_billing.legacy_escrow_finalize(jsonb,bigint,bigint) SET search_path=pg_catalog",
        ),
    ];
    let mut passed = Vec::new();
    for (name, grant, revoke) in mutations {
        sqlx::raw_sql(grant).execute(&owner).await.unwrap();
        let observed = runtime_database::check(&runtime, requirements).await;
        // Restore the exact original permission/definition before asserting the result.
        // Even a wrong checker outcome therefore cannot leave our deliberate grant behind.
        let restored = sqlx::raw_sql(revoke).execute(&owner).await;
        let baseline = runtime_database::check(&runtime, requirements).await;
        restored.unwrap();
        assert_eq!(
            observed,
            Err(RuntimeDatabaseError::UnsafePrivileges),
            "{name}"
        );
        baseline.unwrap();
        assert_eq!(
            financial_snapshot(&owner).await,
            initial,
            "permission probe must never change financial data"
        );
        passed.push(name);
    }
    // This pure IMMUTABLE parser does not grant money authority. Check the real
    // startup contract instead of inventing a rule that all helper EXECUTE is unsafe.
    sqlx::raw_sql("GRANT EXECUTE ON FUNCTION core_billing.legacy_escrow_uint(jsonb,boolean) TO lmm_core_runtime")
        .execute(&owner).await.unwrap();
    let harmless = runtime_database::check(&runtime, requirements).await;
    sqlx::raw_sql("REVOKE EXECUTE ON FUNCTION core_billing.legacy_escrow_uint(jsonb,boolean) FROM lmm_core_runtime")
        .execute(&owner).await.unwrap();
    harmless.unwrap();
    runtime_database::check(&runtime, requirements)
        .await
        .unwrap();
    lmm_core::core_schema::check(&owner).await.unwrap();
    assert_eq!(financial_snapshot(&owner).await, initial);
    receipt(
        "authority-acceptance.json",
        &serde_json::json!({"status":"PASS",
        "actual_runtime_startup_check":true,"unsafe_permission_cases":passed,
        "unsafe_permission_count":7,"pure_parser_execute_not_misclassified":true,
        "all_deliberate_grants_restored":true,"financial_data_unchanged":true}),
    );
    runtime.close().await;
    owner.close().await;
}
