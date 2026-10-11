//! Explicit administrative operations in the core executable. Service startup
//! never invokes these operations or creates database objects.
use lmm_core::{config, identity::IdentityStore};
use serde::Deserialize;
use std::{
    error::Error,
    io::{self, Read},
};

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Bootstrap {
    user_id: i64,
    platform_level: i16,
}

pub enum Operation {
    InitDb,
    BootstrapUser(Bootstrap),
}

impl Operation {
    pub fn bootstrap(input: impl Read) -> io::Result<Self> {
        let mut bytes = Vec::new();
        input.take(4097).read_to_end(&mut bytes)?;
        if bytes.len() > 4096 {
            return Err(io::Error::new(
                io::ErrorKind::InvalidInput,
                "bootstrap input is too large",
            ));
        }
        // serde errors can include caller-provided field names or values.
        // Validate before connecting and report only fixed, secret-free text.
        let request: Bootstrap = serde_json::from_slice(&bytes)
            .map_err(|_| io::Error::new(io::ErrorKind::InvalidInput, "invalid bootstrap JSON"))?;
        Ok(Self::BootstrapUser(request))
    }
}

pub async fn run(operation: Operation) -> Result<(), Box<dyn Error>> {
    let url = config::database_url()?.ok_or_else(|| {
        io::Error::new(
            io::ErrorKind::InvalidInput,
            "a core database URL is required",
        )
    })?;
    let store = IdentityStore::connect(&url).await?;
    match operation {
        Operation::InitDb => {
            store.init_database().await?;
            store.check_schema().await?;
            println!("Fresh core database initialized. No existing data was changed.");
        }
        Operation::BootstrapUser(request) => {
            store.check_schema().await?;
            let issued = store
                .bootstrap_user(request.user_id, request.platform_level)
                .await?;
            // One-time secret output. Operators must redirect to a private file.
            serde_json::to_writer(io::stdout().lock(), &issued)?;
            println!();
        }
    }
    Ok(())
}
