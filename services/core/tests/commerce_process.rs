//! Two real product processes, private runtime roles and separate PostgreSQL
//! databases. Synthetic credits only; no provider, production account or mock RPC.
#![cfg(unix)]

use lmm_core::{
    billing::{
        Credits,
        ledger::{Action, Ledger, Outcome, Request as LedgerRequest},
    },
    core_schema,
    identity::IdentityStore,
};
use reqwest::{Client, StatusCode};
use serde_json::{Value, json};
use sqlx::{PgPool, postgres::PgPoolOptions};
use std::{
    fs,
    os::unix::fs::{OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
    process::{Child, Command, Stdio},
    time::{Duration, Instant},
};

#[path = "common/runtime.rs"]
mod runtime;

struct Process(Child);
impl Drop for Process {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

fn private(root: &Path, name: &str, bytes: &[u8]) -> PathBuf {
    use std::io::Write;
    let path = root.join(name);
    fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(&path)
        .unwrap()
        .write_all(bytes)
        .unwrap();
    path
}

fn spawn(binary: &Path, config: &Path, root: &Path, log: &str) -> Process {
    let output = fs::OpenOptions::new()
        .write(true)
        .create_new(true)
        .mode(0o600)
        .open(root.join(log))
        .unwrap();
    let mut command = Command::new(binary);
    // Neither process inherits a core DSN, caller-selected runtime settings or
    // unrelated credentials. File mode exercises the production config boundary.
    command
        .env_clear()
        .env("PATH", "/usr/bin:/bin")
        .env("GOMAXPROCS", "2")
        .arg("--config")
        .arg(config)
        .stdin(Stdio::null())
        .stdout(output.try_clone().unwrap())
        .stderr(output);
    Process(command.spawn().expect("product process must start"))
}

fn address() -> std::net::SocketAddr {
    std::net::TcpListener::bind("127.0.0.1:0")
        .unwrap()
        .local_addr()
        .unwrap()
}

async fn ready(client: &Client, endpoint: &str, process: &mut Process) -> Value {
    let deadline = Instant::now() + Duration::from_secs(15);
    loop {
        assert!(
            process.0.try_wait().unwrap().is_none(),
            "product exited before readiness; inspect private process log"
        );
        if let Ok(response) = client.get(format!("{endpoint}/health/ready")).send().await
            && response.status() == StatusCode::OK
        {
            return response.json().await.unwrap();
        }
        assert!(Instant::now() < deadline, "product readiness timed out");
        tokio::time::sleep(Duration::from_millis(25)).await;
    }
}

async fn post(
    client: &Client,
    endpoint: &str,
    host: &str,
    user: Option<&str>,
    path: &str,
    body: Value,
) -> (StatusCode, Value) {
    let mut request = client
        .post(format!("{endpoint}/extensions/v1/store{path}"))
        .bearer_auth(host)
        .json(&body);
    if let Some(user) = user {
        request = request.header("x-lmm-user-credential", user);
    }
    let response = request.send().await.unwrap();
    let status = response.status();
    let body = response.text().await.unwrap();
    let body = serde_json::from_str(&body).unwrap_or_else(|_| panic!("{path}: {status}: {body}"));
    (status, body)
}

async fn success(
    client: &Client,
    endpoint: &str,
    host: &str,
    user: &str,
    path: &str,
    body: Value,
) -> Value {
    let (status, response) = post(client, endpoint, host, Some(user), path, body).await;
    assert_eq!(status, StatusCode::OK, "{response}");
    assert!(response.get("error").is_none(), "{response}");
    response["data"].clone()
}

async fn balanced(pool: &PgPool) {
    let n: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM (SELECT journal_id FROM core_billing.ledger_entries \
         GROUP BY journal_id HAVING sum(delta_units)<>0) unbalanced",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    assert_eq!(n, 0, "all real ledger journals must balance");
}

#[sqlx::test(migrations = false)]
async fn real_core_and_go_store_replay_refund_and_restart_after_lost_ack(pool: PgPool) {
    let extension = PathBuf::from(
        std::env::var_os("LMM_RELAY_EXTENSION_BIN")
            .expect("build lmm-extensions and set LMM_RELAY_EXTENSION_BIN; no silent skip"),
    );
    assert!(extension.is_absolute() && extension.is_file());
    let root = std::env::temp_dir().join(format!(
        "lmm-commerce-process-{}-{:016x}",
        std::process::id(),
        rand::random::<u64>()
    ));
    fs::create_dir(&root).unwrap();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let buyer = identity.bootstrap_user(7, 1).await.unwrap().secret;
    let seller = identity.bootstrap_user(9, 1).await.unwrap().secret;
    let outsider = identity.bootstrap_user(11, 6).await.unwrap().secret;
    let buyer_account = identity.authorize(&buyer).await.unwrap().owner_account_id;
    let seller_account = identity.authorize(&seller).await.unwrap().owner_account_id;
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(buyer_account).await.unwrap();
    ledger.open_wallet(seller_account).await.unwrap();
    assert!(matches!(
        ledger
            .execute(&LedgerRequest {
                scope: "commerce-process-test".into(),
                operation_key: "synthetic-opening".into(),
                actor_user_id: Some(7),
                reason: "Synthetic credits in an owned disposable PostgreSQL cluster".into(),
                action: Action::Credit {
                    account_id: buyer_account,
                    amount_units: Credits::try_from(500_000).unwrap(),
                    payment_reference: "synthetic:commerce-process".into(),
                },
            })
            .await
            .unwrap(),
        Outcome::Posted { .. }
    ));
    let core_database = pool.connect_options().get_database().unwrap().to_owned();
    assert!(
        core_database
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_')
    );
    sqlx::query(&format!(
        "REVOKE CONNECT ON DATABASE \"{core_database}\" FROM PUBLIC"
    ))
    .execute(&pool)
    .await
    .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    for schema in ["core_charging", "core_events", "core_commerce"] {
        role.optional(schema).await;
    }

    // Separate extension owner and runtime: no inheritance, CREATE/TEMP or
    // core CONNECT. Schema installation happens here, never in either product.
    let database = format!("lmm_store_process_{:016x}", rand::random::<u64>());
    let owner = format!("{database}_owner");
    let store_role = format!("{database}_runtime");
    let password = format!("{:032x}", rand::random::<u128>());
    sqlx::raw_sql(&format!(
        "CREATE ROLE {owner} NOLOGIN; CREATE ROLE {store_role} LOGIN \
         NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD '{password}';"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::query(&format!(
        "CREATE DATABASE {database} OWNER {owner} TEMPLATE template0"
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(&format!(
        "REVOKE ALL ON DATABASE {database} FROM PUBLIC; \
         GRANT CONNECT ON DATABASE {database} TO {store_role};"
    ))
    .execute(&pool)
    .await
    .unwrap();
    let mut url = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
    assert!(matches!(url.host_str(), Some("127.0.0.1" | "localhost")));
    url.set_path(&database);
    let store_owner = PgPoolOptions::new()
        .max_connections(1)
        .connect(url.as_str())
        .await
        .unwrap();
    sqlx::raw_sql(&format!(
        "ALTER SCHEMA public OWNER TO {owner}; REVOKE ALL ON SCHEMA public FROM PUBLIC; \
         SET ROLE {owner};"
    ))
    .execute(&store_owner)
    .await
    .unwrap();
    sqlx::raw_sql(include_str!(
        "../../extensions/internal/modules/store/schema.sql"
    ))
    .execute(&store_owner)
    .await
    .unwrap();
    sqlx::raw_sql(&format!(
        "RESET ROLE; GRANT USAGE ON SCHEMA lmm_store TO {store_role}; \
         GRANT SELECT,INSERT,UPDATE ON ALL TABLES IN SCHEMA lmm_store TO {store_role};"
    ))
    .execute(&store_owner)
    .await
    .unwrap();
    url.set_username(&store_role).unwrap();
    url.set_password(Some(&password)).unwrap();
    url.set_query(Some("sslmode=disable")); // Disposable loopback cluster only.
    let store_url = private(&root, "store.url", url.as_str().as_bytes());
    let core_url = private(&root, "core.url", role.url.as_bytes());
    let host = "synthetic-host-012345678901234567890123456789";
    let host_file = private(&root, "host-token", host.as_bytes());
    let control = private(
        &root,
        "identity-token",
        b"synthetic-identity-012345678901234567890123456789",
    );
    let money = private(
        &root,
        "commerce-token",
        b"synthetic-commerce-012345678901234567890123456789",
    );
    let permissions = private(&root, "commerce.toml", b"version=1\npay=true\nsettle=true\nrefund=true\nread=true\ncomplete_delivery=true\nreconcile=true\n");
    let socket = root.join("core.sock");
    let core_address = address();
    let extension_address = address();
    let core_config = private(&root, "core.toml", format!(
        "version=1\n[server]\nlisten='{core_address}'\n[database]\nurl_file='{}'\n[rpc]\nsocket='{}'\ntoken_file='{}'\n[commerce]\nconfig_file='{}'\ntoken_file='{}'\n",
        core_url.display(), socket.display(), control.display(), permissions.display(), money.display()
    ).as_bytes());
    let go_config = private(&root, "go.toml", format!(
        "version=1\n[host]\nlisten='{extension_address}'\ntoken_file='{}'\nmodules=['identity','store']\n[core]\nsocket='{}'\nidentity_token_file='{}'\ncommerce_token_file='{}'\n[store]\ndatabase_name='{database}'\ndatabase_url_file='{}'\n",
        host_file.display(), socket.display(), control.display(), money.display(), store_url.display()
    ).as_bytes());
    let client = Client::builder()
        .no_proxy()
        .timeout(Duration::from_secs(10))
        .build()
        .unwrap();
    let core_endpoint = format!("http://{core_address}");
    let endpoint = format!("http://{extension_address}");
    let core_binary = Path::new(env!("CARGO_BIN_EXE_lmm-core"));
    let mut core = spawn(core_binary, &core_config, &root, "core.log");
    assert_eq!(
        ready(&client, &core_endpoint, &mut core).await["capabilities"]["commerce"],
        true
    );
    let mut go = spawn(&extension, &go_config, &root, "go.log");
    ready(&client, &endpoint, &mut go).await;

    let shop = success(&client, &endpoint, host, &seller, "/catalog", json!({"action":"create_shop","owner":{"kind":"personal","id":9},"title":"Synthetic shop","key":"shop"})).await;
    let shop_id = shop["id"].as_str().unwrap();
    let product = success(&client, &endpoint, host, &seller, "/catalog", json!({"action":"create_product","shop_id":shop_id,"title":"Synthetic item","key":"product"})).await;
    success(&client, &endpoint, host, &seller, "/catalog", json!({"action":"product_status","shop_id":shop_id,"id":product["id"],"published":true,"key":"publish"})).await;
    let variant = success(&client, &endpoint, host, &seller, "/catalog", json!({"action":"create_variant","shop_id":shop_id,"product_id":product["id"],"title":"Synthetic USD variant","price":{"amount_minor":1,"currency":"USD"},"quota":10,"key":"variant"})).await;
    let purchase = json!({"shop_id":shop_id,"variant_id":variant["id"],"buyer":{"kind":"personal","id":7},"quantity":1,"key":"purchase"});
    let order = success(
        &client,
        &endpoint,
        host,
        &buyer,
        "/orders",
        purchase.clone(),
    )
    .await;
    assert_eq!(
        success(&client, &endpoint, host, &buyer, "/orders", purchase).await,
        order
    );
    let order_id = order["id"].as_str().unwrap();
    let change = |action: &str| json!({"shop_id":shop_id,"order_id":order_id,"action":action,"text":"Synthetic test content"});
    assert_eq!(
        post(
            &client,
            &endpoint,
            host,
            None,
            "/orders/change",
            change("pay")
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        post(
            &client,
            &endpoint,
            host,
            Some(&outsider),
            "/orders/change",
            change("pay")
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let (one, two) = tokio::join!(
        post(
            &client,
            &endpoint,
            host,
            Some(&buyer),
            "/orders/change",
            change("pay")
        ),
        post(
            &client,
            &endpoint,
            host,
            Some(&buyer),
            "/orders/change",
            change("pay")
        )
    );
    assert_eq!(one.0, StatusCode::OK, "{}", one.1);
    assert_eq!(two.0, StatusCode::OK, "{}", two.1);
    assert_eq!(one.1, two.1);
    let balance = ledger.balance(buyer_account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (495_000, 5_000)
    );
    success(
        &client,
        &endpoint,
        host,
        &seller,
        "/orders/change",
        change("fulfill"),
    )
    .await;
    success(
        &client,
        &endpoint,
        host,
        &seller,
        "/orders/change",
        change("settle"),
    )
    .await;
    assert_eq!(
        ledger
            .balance(seller_account)
            .await
            .unwrap()
            .available_units,
        5_000
    );
    success(
        &client,
        &endpoint,
        host,
        &buyer,
        "/orders/change",
        change("request_refund"),
    )
    .await;

    // Lose only the Go receipt acknowledgement AFTER the real Rust refund has
    // committed. Both schemas keep their genuine transactions and adapters.
    sqlx::raw_sql("CREATE FUNCTION lmm_store.reject_refund_ack() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.body->>'done'='true' AND NEW.body->'command'->>'kind'='refund' THEN RAISE EXCEPTION 'synthetic lost refund acknowledgement'; END IF; RETURN NEW; END $$; CREATE TRIGGER reject_refund_ack BEFORE UPDATE ON lmm_store.money_operations FOR EACH ROW EXECUTE FUNCTION lmm_store.reject_refund_ack()")
        .execute(&store_owner).await.unwrap();
    assert_eq!(
        post(
            &client,
            &endpoint,
            host,
            Some(&seller),
            "/orders/change",
            change("refund")
        )
        .await
        .0,
        StatusCode::ACCEPTED
    );
    assert_eq!(
        ledger.balance(buyer_account).await.unwrap().available_units,
        500_000
    );
    assert_eq!(
        ledger
            .balance(seller_account)
            .await
            .unwrap()
            .available_units,
        0
    );
    let state: String =
        sqlx::query_scalar("SELECT body->>'state' FROM lmm_store.orders WHERE id=$1")
            .bind(order_id)
            .fetch_one(&store_owner)
            .await
            .unwrap();
    assert_eq!(state, "refund_pending");
    let journals: i64 = sqlx::query_scalar("SELECT count(*) FROM core_billing.ledger_journals")
        .fetch_one(&pool)
        .await
        .unwrap();
    let receipts: i64 = sqlx::query_scalar("SELECT count(*) FROM core_commerce.operations")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(receipts, 3);
    // A live, authorized caller may retry after losing an acknowledgement too.
    // This is distinct from the later revoked-credential denial.
    assert_eq!(
        post(
            &client,
            &endpoint,
            host,
            Some(&seller),
            "/orders/change",
            change("refund")
        )
        .await
        .0,
        StatusCode::ACCEPTED
    );
    assert_eq!(
        ledger.balance(buyer_account).await.unwrap().available_units,
        500_000
    );
    assert_eq!(
        ledger
            .balance(seller_account)
            .await
            .unwrap()
            .available_units,
        0
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_billing.ledger_journals")
            .fetch_one(&pool)
            .await
            .unwrap(),
        journals
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_commerce.operations")
            .fetch_one(&pool)
            .await
            .unwrap(),
        receipts
    );
    balanced(&pool).await;
    drop(go);
    drop(core);
    identity.logout(&seller).await.unwrap();
    sqlx::raw_sql("DROP TRIGGER reject_refund_ack ON lmm_store.money_operations; DROP FUNCTION lmm_store.reject_refund_ack()")
        .execute(&store_owner).await.unwrap();
    core = spawn(core_binary, &core_config, &root, "core-restart.log");
    ready(&client, &core_endpoint, &mut core).await;
    go = spawn(&extension, &go_config, &root, "go-restart.log");
    ready(&client, &endpoint, &mut go).await;
    // The production worker must use read-only service reconciliation, not a
    // stored user bearer or permission to issue another refund after logout.
    let deadline = Instant::now() + Duration::from_secs(20);
    loop {
        let state: String =
            sqlx::query_scalar("SELECT body->>'state' FROM lmm_store.orders WHERE id=$1")
                .bind(order_id)
                .fetch_one(&store_owner)
                .await
                .unwrap();
        if state == "refunded" {
            break;
        }
        assert!(
            Instant::now() < deadline,
            "restart worker did not reconcile real committed refund"
        );
        tokio::time::sleep(Duration::from_millis(50)).await;
    }
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_billing.ledger_journals")
            .fetch_one(&pool)
            .await
            .unwrap(),
        journals
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_commerce.operations")
            .fetch_one(&pool)
            .await
            .unwrap(),
        receipts
    );
    let inventory: (i64, i64) = sqlx::query_as("SELECT (body->>'reserved')::bigint,(body->>'claimed')::bigint FROM lmm_store.variants WHERE id=$1").bind(variant["id"].as_str().unwrap()).fetch_one(&store_owner).await.unwrap();
    assert_eq!(
        inventory,
        (0, 1),
        "financial refund is not a physical return"
    );
    assert_eq!(
        post(
            &client,
            &endpoint,
            host,
            Some(&seller),
            "/orders/change",
            change("refund")
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    balanced(&pool).await;
    drop(go);
    drop(core);
    store_owner.close().await;
    sqlx::query(&format!("DROP DATABASE {database}"))
        .execute(&pool)
        .await
        .unwrap();
    sqlx::raw_sql(&format!("DROP ROLE {store_role}; DROP ROLE {owner};"))
        .execute(&pool)
        .await
        .unwrap();
    role.cleanup().await;
    // Keep private process logs with the runner's receipt on success or failure.
}
