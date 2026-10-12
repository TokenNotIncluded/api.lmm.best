// SPDX-License-Identifier: AGPL-3.0-or-later
//! A transaction boundary, not an identity or storage-schema adapter.
use sqlx::{PgConnection, PgPool, Postgres, Transaction};

/// Authentication reads, row filters, counts, pages and projections must all
/// execute through this one connection. Do not hold it while awaiting a client
/// download: spool a bounded export, close the snapshot, then send its bytes.
pub struct ReadSnapshot<'a> {
    transaction: Transaction<'a, Postgres>,
}

impl<'a> ReadSnapshot<'a> {
    pub async fn begin(pool: &'a PgPool) -> Result<Self, sqlx::Error> {
        let mut transaction = pool.begin().await?;
        sqlx::query("SET TRANSACTION ISOLATION LEVEL REPEATABLE READ READ ONLY")
            .execute(&mut *transaction)
            .await?;
        sqlx::query("SET LOCAL statement_timeout = '5s'")
            .execute(&mut *transaction)
            .await?;
        sqlx::query("SET LOCAL idle_in_transaction_session_timeout = '5s'")
            .execute(&mut *transaction)
            .await?;
        // PostgreSQL 18 is the task's isolated database target. This also bounds
        // a succession of quick queries that never reaches statement_timeout.
        sqlx::query("SET LOCAL transaction_timeout = '30s'")
            .execute(&mut *transaction)
            .await?;
        Ok(Self { transaction })
    }

    pub fn connection(&mut self) -> &mut PgConnection {
        &mut self.transaction
    }

    pub async fn finish(self) -> Result<(), sqlx::Error> {
        self.transaction.rollback().await
    }
}
