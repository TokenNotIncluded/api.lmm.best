use sqlx::{PgPool, postgres::PgPoolOptions};

pub(in crate::migration_routes) struct IsolatedPgSchema {
    pub(in crate::migration_routes) admin: PgPool,
    pub(in crate::migration_routes) pool: PgPool,
    pub(in crate::migration_routes) schema: String,
}

impl IsolatedPgSchema {
    pub(in crate::migration_routes) async fn new(
        prefix: &str,
        max_connections: u32,
    ) -> Option<Self> {
        let database_url = std::env::var("LMM_TEST_DATABASE_URL").ok()?;
        let admin = PgPool::connect(&database_url)
            .await
            .expect("connect isolated PostgreSQL test database");
        let schema = format!("{prefix}_{}", uuid::Uuid::new_v4().simple());
        sqlx::query(&format!("CREATE SCHEMA {schema}"))
            .execute(&admin)
            .await
            .expect("create isolated PostgreSQL test schema");
        let pool = PgPoolOptions::new()
            .max_connections(max_connections)
            .after_connect({
                let schema = schema.clone();
                move |connection, _metadata| {
                    let statement = format!("SET search_path TO {schema}");
                    Box::pin(async move {
                        sqlx::query(&statement).execute(connection).await?;
                        Ok(())
                    })
                }
            })
            .connect(&database_url)
            .await
            .expect("connect isolated PostgreSQL test schema");
        Some(Self {
            admin,
            pool,
            schema,
        })
    }

    pub(in crate::migration_routes) async fn cleanup(self) {
        self.pool.close().await;
        sqlx::query(&format!("DROP SCHEMA {} CASCADE", self.schema))
            .execute(&self.admin)
            .await
            .expect("drop isolated PostgreSQL test schema");
        self.admin.close().await;
    }
}
