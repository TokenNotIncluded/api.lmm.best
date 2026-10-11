use sqlx::{PgPool, postgres::PgPoolOptions};

/// Synthetic role scoped to this test's SQLx-owned database. Never used by a
/// developer/production database, and removed explicitly after closing children.
pub struct RuntimeRole {
    pub pool: PgPool,
    pub url: String,
    pub name: String,
    owner: PgPool,
}
impl RuntimeRole {
    pub async fn create(owner: &PgPool) -> Self {
        let name = format!(
            "lmm_runtime_test_{}_{:016x}",
            std::process::id(),
            rand::random::<u64>()
        );
        let password = format!(
            "{:032x}{:032x}",
            rand::random::<u128>(),
            rand::random::<u128>()
        );
        let database = owner.connect_options().get_database().unwrap().to_owned();
        assert!(
            database
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_')
        );
        sqlx::raw_sql(&format!(r#"
            CREATE ROLE {name} LOGIN PASSWORD '{password}' NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
            REVOKE CREATE,TEMPORARY ON DATABASE "{database}" FROM PUBLIC;
            REVOKE CREATE ON SCHEMA public FROM PUBLIC;
            GRANT CONNECT ON DATABASE "{database}" TO {name};
            GRANT USAGE ON SCHEMA core_meta,core_identity,core_billing TO {name};
            GRANT SELECT ON ALL TABLES IN SCHEMA core_meta,core_billing TO {name};
            GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA core_identity TO {name};
            GRANT USAGE ON ALL SEQUENCES IN SCHEMA core_identity TO {name};
            GRANT SELECT,INSERT,UPDATE,DELETE ON core_billing.account_policies TO {name};
            GRANT EXECUTE ON FUNCTION core_billing.open_ledger_wallet(bigint),core_billing.post_ledger(jsonb) TO {name};
        "#)).execute(owner).await.unwrap();
        let mut url = reqwest::Url::parse(&std::env::var("DATABASE_URL").unwrap()).unwrap();
        assert!(matches!(url.host_str(), Some("localhost" | "127.0.0.1")));
        url.set_path(&database);
        url.set_username(&name).unwrap();
        url.set_password(Some(&password)).unwrap();
        let url = url.to_string();
        let pool = PgPoolOptions::new()
            .max_connections(2)
            .connect(&url)
            .await
            .unwrap();
        Self {
            pool,
            url,
            name,
            owner: owner.clone(),
        }
    }
    pub async fn optional(&self, schema: &str) {
        assert!(matches!(
            schema,
            "core_charging" | "core_events" | "core_payments"
        ));
        sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA {schema} TO {}; GRANT SELECT,INSERT,UPDATE,DELETE ON ALL TABLES IN SCHEMA {schema} TO {}; GRANT USAGE ON ALL SEQUENCES IN SCHEMA {schema} TO {}",self.name,self.name,self.name))
            .execute(&self.owner).await.unwrap();
    }
    pub async fn cleanup(self) {
        self.pool.close().await;
        sqlx::raw_sql(&format!(
            "DROP OWNED BY {}; DROP ROLE {}",
            self.name, self.name
        ))
        .execute(&self.owner)
        .await
        .unwrap();
    }
}
