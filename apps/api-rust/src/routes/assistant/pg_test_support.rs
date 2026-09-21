use sqlx::PgPool;

pub(in crate::routes) struct IsolatedPgSchema {
    pub(in crate::routes) admin: PgPool,
    pub(in crate::routes) pool: PgPool,
    pub(in crate::routes) schema: String,
}
