use sqlx::PgPool;

pub(in crate::migration_routes) struct IsolatedPgSchema {
    pub(in crate::migration_routes) admin: PgPool,
    pub(in crate::migration_routes) pool: PgPool,
    pub(in crate::migration_routes) schema: String,
}
