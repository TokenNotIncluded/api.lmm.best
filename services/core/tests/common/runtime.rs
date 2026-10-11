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
            GRANT SELECT,INSERT,UPDATE ON core_identity.accounts,core_identity.users,core_identity.teams,core_identity.memberships,core_identity.credentials,core_identity.invites,core_identity.password_logins,core_identity.user_profiles,core_identity.company_billing_profiles,core_identity.web_sessions,core_identity.api_key_settings TO {name};
            GRANT SELECT,INSERT ON core_identity.key_funding_rules,core_identity.credential_grants,core_identity.oauth_identities,core_identity.audit,core_identity.web_session_credentials TO {name};
            GRANT SELECT,INSERT,DELETE ON core_identity.oauth_flows TO {name};
            GRANT SELECT,INSERT,UPDATE,DELETE ON core_identity.auth_attempts TO {name};
            GRANT SELECT ON core_identity.developer_access,core_identity.user_routing_groups TO {name};
            GRANT USAGE ON ALL SEQUENCES IN SCHEMA core_identity TO {name};
            GRANT SELECT,INSERT,UPDATE ON core_billing.account_policies TO {name};
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
        if schema == "core_console" {
            sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA core_console TO {0}; GRANT SELECT ON core_console.options,core_console.initialization,core_console.announcement_reads TO {0}; GRANT INSERT ON core_console.announcement_reads TO {0}",self.name))
                .execute(&self.owner).await.unwrap();
            return;
        }
        if schema == "core_model" {
            sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA core_model TO {0}; GRANT EXECUTE ON FUNCTION core_model.checkpoint_output(text,bytea,bigint,bytea,bigint,jsonb,boolean),core_model.completed_streams(integer) TO {0}",self.name))
                .execute(&self.owner).await.unwrap();
            return;
        }
        if schema == "core_payments" {
            sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA core_payments TO {0}; GRANT SELECT,INSERT ON ALL TABLES IN SCHEMA core_payments TO {0}; GRANT UPDATE ON core_payments.intents,core_payments.refund_holds TO {0}; GRANT USAGE,SELECT ON SEQUENCE core_payments.intents_id_seq TO {0}",self.name))
                .execute(&self.owner).await.unwrap();
            return;
        }
        let sql = match schema {
            "web_identity" => format!(
                r#"
              GRANT USAGE ON SCHEMA public TO {0};
              GRANT SELECT ON public.routing_active_release,public.routing_groups,public.routing_routes TO {0};
              GRANT USAGE ON SCHEMA core_charging TO {0};
              GRANT SELECT ON core_charging.budgets,core_charging.requests,core_charging.external_consumption,core_charging.budget_opening_consumption TO {0};
              GRANT INSERT,UPDATE ON core_charging.budgets TO {0};
              GRANT INSERT ON core_charging.policy_events TO {0};
              GRANT USAGE ON SEQUENCE core_charging.budgets_id_seq,core_charging.policy_events_id_seq TO {0};
              GRANT EXECUTE ON FUNCTION core_charging.committed_credits(text,bigint,bigint,bigint) TO {0};
            "#,
                self.name
            ),
            "core_charging" => format!(
                r#"
              GRANT USAGE ON SCHEMA core_charging TO {0};
              GRANT SELECT ON ALL TABLES IN SCHEMA core_charging TO {0};
              GRANT INSERT,UPDATE ON core_charging.budgets,core_charging.subscriptions,core_charging.requests,core_charging.ledger_holds TO {0};
              GRANT INSERT ON core_charging.budget_checks,core_charging.usage_events,core_charging.refunds,core_charging.policy_events,core_charging.ledger_commands,core_charging.external_consumption TO {0};
              GRANT UPDATE(refunded) ON core_charging.external_consumption TO {0};
              GRANT USAGE ON SEQUENCE core_charging.budgets_id_seq,core_charging.subscriptions_id_seq,core_charging.policy_events_id_seq TO {0};
              GRANT EXECUTE ON FUNCTION core_charging.period_window(text,bigint,bigint,bigint),core_charging.subscription_window(bigint,bigint),core_charging.committed_credits(text,bigint,bigint,bigint) TO {0};
            "#,
                self.name
            ),
            "core_events" => format!(
                r#"
              GRANT USAGE ON SCHEMA core_events TO {0};
              GRANT SELECT ON core_events.capacity TO {0};
              GRANT UPDATE(pending_events,pending_bytes) ON core_events.capacity TO {0};
              GRANT SELECT,INSERT,UPDATE ON core_events.subscriptions,core_events.outbox,core_events.deliveries,core_events.inbox TO {0};
              GRANT USAGE ON SEQUENCE core_events.outbox_id_seq TO {0};
            "#,
                self.name
            ),
            "core_commerce" => format!(
                r#"
              GRANT USAGE ON SCHEMA core_commerce TO {0};
              GRANT SELECT,INSERT ON core_commerce.orders,core_commerce.operations,core_commerce.key_scopes TO {0};
              GRANT UPDATE(state,settlement_journal_id,revision,delivery_reference) ON core_commerce.orders TO {0};
              GRANT UPDATE(can_spend) ON core_commerce.key_scopes TO {0};
              GRANT USAGE ON SEQUENCE core_commerce.orders_id_seq TO {0};
            "#,
                self.name
            ),
            _ => panic!("unknown synthetic optional schema"),
        };
        sqlx::raw_sql(&sql).execute(&self.owner).await.unwrap();
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
