use lmm_core::{config, identity::IdentityStore};
use serde::Deserialize;
use std::{
    env,
    error::Error,
    io::{self, Read},
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Bootstrap {
    user_id: i64,
    platform_level: i16,
}

#[tokio::main]
async fn main() -> Result<(), Box<dyn Error>> {
    let args: Vec<String> = env::args().skip(1).collect();
    if args.len() != 1 || !matches!(args[0].as_str(), "init-db" | "bootstrap-user") {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "usage: lmm-core-admin init-db | bootstrap-user (JSON on stdin)",
        )
        .into());
    }
    let url = config::database_url()?.ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "a core database URL is required",
        )
    })?;
    let store = IdentityStore::connect(&url).await?;
    if args[0] == "init-db" {
        store.init_database().await?;
        store.check_schema().await?;
        println!("Fresh core database initialized. No existing data was changed.");
    } else {
        store.check_schema().await?;
        let mut input = String::new();
        io::stdin().lock().take(4097).read_to_string(&mut input)?;
        if input.len() > 4096 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "bootstrap input is too large",
            )
            .into());
        }
        let request: Bootstrap = serde_json::from_str(&input)?;
        let issued = store
            .bootstrap_user(request.user_id, request.platform_level)
            .await?;
        // One-time secret output. Operators must redirect to a private file.
        serde_json::to_writer(io::stdout().lock(), &issued)?;
        println!();
    }
    Ok(())
}
