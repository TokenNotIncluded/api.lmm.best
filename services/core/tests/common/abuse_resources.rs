//! Only SQLx-owned local fixture databases. All mutations use the real admin
//! preview/commit API; no production identity, money or provider is contacted.
use lmm_core::{
    abuse::{
        AbuseStore, PolicyConfig,
        admin::{Action, CommitRequest, PolicyUpdate, PreviewRequest, Target},
    },
    identity::IdentityStore,
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::collections::BTreeSet;

pub struct Marker {
    pub abuse: AbuseStore,
    pub root_secret: String,
}
pub async fn marker(pool: &PgPool, identity: &IdentityStore) -> Marker {
    let exists: bool =
        sqlx::query_scalar("SELECT to_regclass('core_abuse.user_marks') IS NOT NULL")
            .fetch_one(pool)
            .await
            .unwrap();
    if !exists {
        sqlx::raw_sql(lmm_core::abuse::SCHEMA)
            .execute(pool)
            .await
            .unwrap();
    }
    let root = identity.bootstrap_user(1_000_000, 6).await.unwrap();
    let abuse = AbuseStore::new(pool.clone(), BTreeSet::new());
    let config:PolicyConfig=serde_json::from_value(json!({"version":1,"policies":[{"id":"resource-isolation","name":"Synthetic resource isolation","mode":"fixed_text","fixed_text":"configured response","public_models":["public-model"],"clients":{},"limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":20,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":65536,"max_output_bytes":65536,"max_output_tokens":1000,"output_bytes_per_second":0,"total_timeout_seconds":10}}]})).unwrap();
    abuse
        .update_policies(
            &root.secret,
            &PolicyUpdate {
                operation_id: "resource-policy-setup".into(),
                reason: "Synthetic isolated resource tests".into(),
                expected_revision: 0,
                config,
            },
        )
        .await
        .unwrap();
    Marker {
        abuse,
        root_secret: root.secret,
    }
}
impl Marker {
    pub async fn preview(&self, id: i64, operation: &str) -> CommitRequest {
        let current = self
            .abuse
            .inspect_user(&self.root_secret, id)
            .await
            .unwrap();
        let preview = self
            .abuse
            .preview_users(
                &self.root_secret,
                &PreviewRequest {
                    operation_id: operation.into(),
                    reason: "Synthetic isolated resource tests".into(),
                    action: Action::Mark,
                    policy_id: Some("resource-isolation".into()),
                    expected_policy_revision: 1,
                    targets: vec![Target {
                        user_id: id,
                        expected_revision: current.revision,
                    }],
                },
            )
            .await
            .unwrap();
        CommitRequest {
            operation_id: operation.into(),
            preview_id: preview["preview_id"].as_str().unwrap().into(),
            preview_hash: preview["preview_hash"].as_str().unwrap().into(),
        }
    }
    pub async fn mark(&self, id: i64) {
        let commit = self.preview(id, &format!("resource-mark-{id}")).await;
        let result = self
            .abuse
            .commit_users(&self.root_secret, &commit)
            .await
            .unwrap();
        assert_eq!(result["users"][0]["marked"], true);
    }
}

pub async fn financial_snapshot(pool: &PgPool) -> Value {
    let mut output = serde_json::Map::new();
    for table in [
        "core_billing.balance_state",
        "core_billing.ledger_operations",
        "core_billing.ledger_journals",
        "core_billing.ledger_entries",
        "core_charging.requests",
        "core_charging.ledger_holds",
        "core_charging.external_consumption",
        "core_charging.budget_checks",
        "core_commerce.orders",
        "core_commerce.operations",
        "core_payments.intents",
        "core_payments.receipts",
        "core_payments.provider_events",
        "core_payments.refund_holds",
    ] {
        let exists: bool = sqlx::query_scalar("SELECT to_regclass($1) IS NOT NULL")
            .bind(table)
            .fetch_one(pool)
            .await
            .unwrap();
        if exists {
            let rows:Value=sqlx::query_scalar(&format!("SELECT COALESCE(jsonb_agg(to_jsonb(t) ORDER BY to_jsonb(t)::text),'[]'::jsonb) FROM {table} t")).fetch_one(pool).await.unwrap();
            output.insert(table.into(), rows);
        }
    }
    let sequences:Value=sqlx::query_scalar("SELECT COALESCE(jsonb_agg(to_jsonb(s) ORDER BY schemaname,sequencename),'[]'::jsonb) FROM pg_sequences s WHERE schemaname IN ('core_billing','core_charging','core_commerce','core_payments')").fetch_one(pool).await.unwrap();
    output.insert("financial_sequences".into(), sequences);
    Value::Object(output)
}
