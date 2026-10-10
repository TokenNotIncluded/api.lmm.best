//! Test-only producer/server. Never built into lmm-core or lmm-core-admin.
use lmm_core::{
    events::{EventStore, NewEvent, SCHEMA},
    identity::IdentityStore,
    internal_rpc::{RpcServer, pb, read_service_token, readonly_store},
};
use prost::Message;
use sqlx::postgres::PgPoolOptions;
use std::{
    env, error::Error, fs::OpenOptions, io::Write, os::unix::fs::OpenOptionsExt, path::Path,
};
use tokio::sync::watch;

const LARGE: i64 = 9_007_199_254_740_993;
#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    if env::var("LMM_EVENTS_FIXTURE").as_deref() != Ok("1") {
        return Err("explicit test fixture opt-in required".into());
    }
    let url = env::var("DATABASE_URL")?;
    if !url
        .rsplit('/')
        .next()
        .unwrap_or("")
        .starts_with("mk06_core_")
    {
        return Err("only disposable mk06_core_ databases are accepted".into());
    }
    let mode = env::args().nth(1).ok_or("fixture mode is required")?;
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .connect(&url)
        .await?;
    match mode.as_str() {
        "init" => {
            let identity = IdentityStore::from_pool(pool.clone());
            identity.init_database().await?;
            let user = identity.bootstrap_user(LARGE, 1).await?;
            let mut file = OpenOptions::new()
                .write(true)
                .create_new(true)
                .mode(0o600)
                .open(env::var("MK06_USER_FILE")?)?;
            file.write_all(user.secret.as_bytes())?;
            sqlx::raw_sql(SCHEMA).execute(&pool).await?;
            sqlx::query(
                "ALTER TABLE core_events.outbox ALTER COLUMN id RESTART WITH 9007199254740993",
            )
            .execute(&pool)
            .await?;
            let mut tx = pool.begin().await?;
            EventStore::provision(
                &mut tx,
                "fixture.consumer",
                "extensions",
                &["test.counter.v1".into()],
                100,
            )
            .await?;
            EventStore::provision(
                &mut tx,
                "other.consumer",
                "other-service",
                &["private.test.v1".into()],
                100,
            )
            .await?;
            tx.commit().await?;
            println!("fixture database initialized");
        }
        "publish" => {
            let key = env::args().nth(2).ok_or("fixture event key required")?;
            let mut payload = pb::Account {
                kind: pb::AccountKind::Team as i32,
                id: i64::MAX,
            }
            .encode_to_vec();
            // Unknown field 99, wire type varint. The transport must keep it.
            payload.extend_from_slice(&[0x98, 0x06, 0x01]);
            let mut tx = pool.begin().await?;
            let id = EventStore::publish(
                &mut tx,
                NewEvent {
                    key: &key,
                    event_type: "test.counter.v1",
                    schema_version: 1,
                    resource_id: LARGE,
                    resource_version: i64::MAX,
                    payload: &payload,
                },
            )
            .await?;
            tx.commit().await?;
            println!("fixture event {id} committed without contacting Go");
        }
        "serve" => {
            pool.close().await;
            let token = read_service_token(Path::new(&env::var("MK06_SERVICE_FILE")?))?;
            let identity = readonly_store(&url).await?;
            let events = EventStore::connect(&url).await?.with_lease_seconds(1)?;
            let server =
                RpcServer::bind(Path::new(&env::var("MK06_SOCKET")?), &token, Some(identity))?
                    .with_events(events, "extensions")?;
            let (sender, receiver) = watch::channel(false);
            tokio::spawn(async move {
                if tokio::signal::ctrl_c().await.is_ok() {
                    let _ = sender.send(true);
                }
            });
            println!("fixture RPC ready");
            server.serve(receiver).await?;
        }
        _ => return Err("unknown fixture mode".into()),
    }
    Ok(())
}
