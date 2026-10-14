use axum::{
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    abuse::{
        self, AbuseError as E, AbuseStore, ClientProtocol, PolicyConfig,
        admin::{self, Action, CommitRequest, PolicyUpdate, PreviewRequest, Target},
    },
    accounts::{Account, AccountKind},
    core_schema,
    funding::FundingPolicy,
    identity::{IdentityStore, IssuedCredential},
};
use serde_json::{Value, json};
use sqlx::PgPool;
use std::collections::BTreeSet;
use tower::ServiceExt;
#[path = "common/runtime.rs"]
mod runtime;

struct Fixture {
    identity: IdentityStore,
    abuse: AbuseStore,
    root: IssuedCredential,
    user: IssuedCredential,
    other: IssuedCredential,
    key: IssuedCredential,
}
fn config() -> PolicyConfig {
    serde_json::from_value(json!({"version":1,"policies":[{"id":"trap","name":"Synthetic isolation","mode":"fixed_text","fixed_text":"configured response","public_models":["public-model","second-model"],"clients":{},"limits":{"requests_per_minute_per_user":10,"requests_per_minute_global":20,"max_concurrent_per_user":1,"max_concurrent_global":2,"max_input_bytes":65536,"max_output_bytes":65536,"max_output_tokens":1000,"output_bytes_per_second":0,"total_timeout_seconds":10}}]})).unwrap()
}
async fn fresh(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let root = identity.bootstrap_user(1000, 6).await.unwrap();
    let user = identity.bootstrap_user(1001, 0).await.unwrap();
    let other = identity.bootstrap_user(1002, 0).await.unwrap();
    let key = identity
        .issue_key(
            &user.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1001,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let abuse = AbuseStore::new(pool.clone(), BTreeSet::new());
    abuse
        .update_policies(
            &root.secret,
            &PolicyUpdate {
                operation_id: "initial-policy".into(),
                reason: "Synthetic local acceptance".into(),
                expected_revision: 0,
                config: config(),
            },
        )
        .await
        .unwrap();
    Fixture {
        identity,
        abuse,
        root,
        user,
        other,
        key,
    }
}
fn request(op: &str, action: Action, ids: &[(i64, i64)]) -> PreviewRequest {
    PreviewRequest {
        operation_id: op.into(),
        reason: "Synthetic local acceptance".into(),
        action,
        policy_id: if action == Action::Unmark {
            None
        } else {
            Some("trap".into())
        },
        expected_policy_revision: 1,
        targets: ids
            .iter()
            .map(|&(user_id, expected_revision)| Target {
                user_id,
                expected_revision,
            })
            .collect(),
    }
}
async fn preview(f: &Fixture, input: &PreviewRequest) -> CommitRequest {
    let p = f.abuse.preview_users(&f.root.secret, input).await.unwrap();
    CommitRequest {
        operation_id: input.operation_id.clone(),
        preview_id: p["preview_id"].as_str().unwrap().into(),
        preview_hash: p["preview_hash"].as_str().unwrap().into(),
    }
}
async fn mark(f: &Fixture, id: i64) -> Value {
    let input = request(&format!("mark-user-{id}"), Action::Mark, &[(id, 0)]);
    let commit = preview(f, &input).await;
    f.abuse.commit_users(&f.root.secret, &commit).await.unwrap()
}
async fn financial_snapshot(pool: &PgPool) -> Value {
    let mut out = serde_json::Map::new();
    for table in [
        "core_billing.balance_state",
        "core_billing.ledger_operations",
        "core_billing.ledger_journals",
        "core_billing.ledger_entries",
        "core_charging.requests",
        "core_charging.ledger_holds",
        "core_charging.usage_events",
        "core_charging.refunds",
    ] {
        let rows:Value=sqlx::query_scalar(&format!("SELECT COALESCE(jsonb_agg(row_to_json(t) ORDER BY row_to_json(t)::text),'[]'::jsonb) FROM {table} t")).fetch_one(pool).await.unwrap();
        out.insert(table.into(), rows);
    }
    Value::Object(out)
}

#[sqlx::test(migrations = false)]
async fn policy_cas_and_operation_identity_are_durable_and_audited_once(pool: PgPool) {
    let f = fresh(&pool).await;
    let p = PolicyUpdate {
        operation_id: "policy-update-2".into(),
        reason: "reviewed synthetic change".into(),
        expected_revision: 1,
        config: config(),
    };
    let first = f.abuse.update_policies(&f.root.secret, &p).await.unwrap();
    assert_eq!(first["policy_revision"], 2);
    assert_eq!(
        first,
        f.abuse.update_policies(&f.root.secret, &p).await.unwrap()
    );
    assert_eq!(
        first,
        f.abuse
            .operation(&f.root.secret, &p.operation_id)
            .await
            .unwrap()
    );
    let mut changed = p.clone();
    changed.reason = "different payload".into();
    assert!(matches!(
        f.abuse.update_policies(&f.root.secret, &changed).await,
        Err(E::Conflict)
    ));
    let mut stale = p;
    stale.operation_id = "policy-stale-cas".into();
    assert!(matches!(
        f.abuse.update_policies(&f.root.secret, &stale).await,
        Err(E::Conflict)
    ));
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.audit")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
}

#[sqlx::test(migrations = false)]
async fn admin_api_requires_canonical_superadmin_session_not_key_or_service_level(pool: PgPool) {
    let f = fresh(&pool).await;
    let lower = f.identity.bootstrap_user(1003, 5).await.unwrap();
    let level = f.identity.bootstrap_user(1004, 4).await.unwrap();
    let root_key = f
        .identity
        .issue_key(
            &f.root.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1000,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let app = admin::router(f.abuse.clone());
    for (secret, expected) in [
        (&f.root.secret, StatusCode::OK),
        (&lower.secret, StatusCode::FORBIDDEN),
        (&level.secret, StatusCode::FORBIDDEN),
        (&root_key.secret, StatusCode::FORBIDDEN),
        (&f.user.secret, StatusCode::FORBIDDEN),
    ] {
        let response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/core/v1/admin/abuse/policies")
                    .header("authorization", format!("Bearer {secret}"))
                    .body(Body::empty())
                    .unwrap(),
            )
            .await
            .unwrap();
        assert_eq!(response.status(), expected);
    }
    let response = app
        .clone()
        .oneshot(
            Request::builder()
                .uri("/core/v1/admin/abuse/policies")
                .header("authorization", format!("Bearer {}", f.root.secret))
                .header("authorization", format!("Bearer {}", f.root.secret))
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    let bytes = to_bytes(response.into_body(), 65536).await.unwrap();
    let text = String::from_utf8(bytes.to_vec()).unwrap();
    assert!(!text.contains(&f.root.secret));
    assert!(matches!(
        f.abuse
            .preview_users(
                &f.root.secret,
                &request("self-target-reject", Action::Mark, &[(1000, 0)])
            )
            .await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        f.abuse
            .preview_users(
                &f.root.secret,
                &request("admin-target-reject", Action::Mark, &[(1003, 0)])
            )
            .await,
        Err(E::Forbidden)
    ));
}

#[sqlx::test(migrations = false)]
async fn batch_mark_unmark_preserves_real_ledger_grants_and_sessions(pool: PgPool) {
    let f = fresh(&pool).await;
    let before = financial_snapshot(&pool).await;
    let auth_before: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=1001")
            .fetch_one(&pool)
            .await
            .unwrap();
    let commit = preview(
        &f,
        &request(
            "batch-mark-1001-1002",
            Action::Mark,
            &[(1002, 0), (1001, 0)],
        ),
    )
    .await;
    assert!(
        !f.abuse
            .inspect_user(&f.root.secret, 1001)
            .await
            .unwrap()
            .marked
    );
    let result = f.abuse.commit_users(&f.root.secret, &commit).await.unwrap();
    assert_eq!(result["users"].as_array().unwrap().len(), 2);
    assert_eq!(result["users"][0]["user_id"], 1001);
    assert_eq!(result["users"][0]["revision"], 1);
    assert_eq!(
        result,
        f.abuse.commit_users(&f.root.secret, &commit).await.unwrap()
    );
    assert_eq!(
        f.identity.authorize(&f.user.secret).await.unwrap().user_id,
        1001
    );
    assert_eq!(
        f.identity.authorize(&f.key.secret).await.unwrap().user_id,
        1001
    );
    let auth_after: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=1001")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(auth_before, auth_after);
    let grant: bool =
        sqlx::query_scalar("SELECT granted FROM core_identity.developer_access WHERE user_id=1001")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(grant);
    assert_eq!(before, financial_snapshot(&pool).await);
    let mut tx = pool.begin().await.unwrap();
    assert!(matches!(
        abuse::reject_normal_model_in_transaction(&mut tx, 1001).await,
        Err(E::Forbidden)
    ));
    assert_eq!(
        abuse::public_models_in_transaction(&mut tx, 1001)
            .await
            .unwrap()
            .unwrap(),
        vec!["public-model", "second-model"]
    );
    tx.rollback().await.unwrap();
    let clear = preview(
        &f,
        &request(
            "batch-unmark-1001-1002",
            Action::Unmark,
            &[(1001, 1), (1002, 1)],
        ),
    )
    .await;
    let result = f.abuse.commit_users(&f.root.secret, &clear).await.unwrap();
    assert_eq!(result["users"][0]["revision"], 2);
    assert_eq!(result["users"][0]["marked"], false);
    assert_eq!(before, financial_snapshot(&pool).await);
}

#[sqlx::test(migrations = false)]
async fn stale_preview_disabled_conversion_and_policy_removal_do_not_partially_apply(pool: PgPool) {
    let f = fresh(&pool).await;
    let stale = preview(
        &f,
        &request("batch-stale-preview", Action::Mark, &[(1001, 0), (1002, 0)]),
    )
    .await;
    sqlx::query("UPDATE core_identity.users SET active=FALSE WHERE id=1002")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        f.abuse.commit_users(&f.root.secret, &stale).await,
        Err(E::Conflict)
    ));
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.user_marks")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
    assert!(matches!(
        f.abuse
            .preview_users(
                &f.root.secret,
                &request("disabled-mark-reject", Action::Mark, &[(1002, 0)])
            )
            .await,
        Err(E::Conflict)
    ));
    let conversion = preview(
        &f,
        &request(
            "explicit-disabled-conversion",
            Action::ConvertDisabled,
            &[(1002, 0)],
        ),
    )
    .await;
    let result = f
        .abuse
        .commit_users(&f.root.secret, &conversion)
        .await
        .unwrap();
    assert_eq!(result["users"][0]["active"], true);
    assert_eq!(result["users"][0]["marked"], true);
    let removal = PolicyUpdate {
        operation_id: "remove-live-policy".into(),
        reason: "must fail".into(),
        expected_revision: 1,
        config: PolicyConfig {
            version: 1,
            policies: vec![],
        },
    };
    assert!(matches!(
        f.abuse.update_policies(&f.root.secret, &removal).await,
        Err(E::Conflict)
    ));
    assert_eq!(
        f.abuse.policies(&f.root.secret).await.unwrap()["revision"],
        1
    );
}

#[sqlx::test(migrations = false)]
async fn expiry_actor_hash_and_policy_revision_are_bound_before_any_user_write(pool: PgPool) {
    let f = fresh(&pool).await;
    let second = f.identity.bootstrap_user(1003, 6).await.unwrap();
    let commit = preview(
        &f,
        &request("binding-preview-test", Action::Mark, &[(1001, 0)]),
    )
    .await;
    assert!(matches!(
        f.abuse.commit_users(&second.secret, &commit).await,
        Err(E::NotFound)
    ));
    let mut wrong = commit.clone();
    wrong.preview_hash = "0".repeat(64);
    assert!(matches!(
        f.abuse.commit_users(&f.root.secret, &wrong).await,
        Err(E::Conflict)
    ));
    sqlx::query("UPDATE core_abuse.previews SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1").bind(&commit.preview_id).execute(&pool).await.unwrap();
    assert!(matches!(
        f.abuse.commit_users(&f.root.secret, &commit).await,
        Err(E::PreviewExpired)
    ));
    let fresh = preview(
        &f,
        &request("policy-drift-preview", Action::Mark, &[(1001, 0)]),
    )
    .await;
    f.abuse
        .update_policies(
            &f.root.secret,
            &PolicyUpdate {
                operation_id: "policy-revision-two".into(),
                reason: "invalidate pending preview".into(),
                expected_revision: 1,
                config: config(),
            },
        )
        .await
        .unwrap();
    assert!(matches!(
        f.abuse.commit_users(&f.root.secret, &fresh).await,
        Err(E::Conflict)
    ));
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.user_marks")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
}

#[sqlx::test(migrations = false)]
async fn marked_key_catalog_admission_and_concurrency_never_create_financial_operations(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    assert!(
        f.abuse
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "unmarked"
            )
            .await
            .unwrap()
            .is_none()
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.model_leases")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
    mark(&f, 1001).await;
    let before = financial_snapshot(&pool).await;
    assert_eq!(
        f.abuse
            .lookup_model_policy(&f.key.secret, None)
            .await
            .unwrap()
            .unwrap()
            .public_models
            .len(),
        2
    );
    assert!(matches!(
        f.abuse
            .admit_model(
                &f.user.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "session-reject"
            )
            .await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        f.abuse
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "private-group-model",
                None,
                "private-reject"
            )
            .await,
        Err(E::Forbidden)
    ));
    let a = f
        .abuse
        .admit_model(
            &f.key.secret,
            ClientProtocol::OpenaiChat,
            "public-model",
            None,
            "admission-one",
        )
        .await
        .unwrap()
        .unwrap();
    assert_eq!(a.fixed_text.as_deref(), Some("configured response"));
    assert!(a.upstream.is_none());
    assert!(matches!(
        f.abuse
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiResponses,
                "public-model",
                None,
                "admission-two"
            )
            .await,
        Err(E::RateLimited)
    ));
    f.abuse.finish_model(&a.lease_id).await.unwrap();
    f.abuse.finish_model(&a.lease_id).await.unwrap();
    assert!(matches!(
        f.abuse
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "admission-one"
            )
            .await,
        Err(E::Conflict)
    ));
    let b = f
        .abuse
        .admit_model(
            &f.key.secret,
            ClientProtocol::OpenaiResponses,
            "public-model",
            None,
            "admission-two",
        )
        .await
        .unwrap()
        .unwrap();
    f.abuse.finish_model(&b.lease_id).await.unwrap();
    assert_eq!(before, financial_snapshot(&pool).await);
}

#[sqlx::test(migrations = false)]
async fn mark_commit_waits_for_existing_user_authority_then_blocks_new_normal_admission(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    let commit = preview(
        &f,
        &request("authority-lock-mark", Action::Mark, &[(1001, 0)]),
    )
    .await;
    let mut authority = pool.begin().await.unwrap();
    sqlx::query("SELECT id FROM core_identity.users WHERE id=1001 FOR SHARE")
        .fetch_one(&mut *authority)
        .await
        .unwrap();
    let store = f.abuse.clone();
    let secret = f.root.secret.clone();
    let mut pending = tokio::spawn(async move { store.commit_users(&secret, &commit).await });
    assert!(
        tokio::time::timeout(std::time::Duration::from_millis(100), &mut pending)
            .await
            .is_err(),
        "mark bypassed an existing user authority lock"
    );
    authority.commit().await.unwrap();
    pending.await.unwrap().unwrap();
    let mut next = pool.begin().await.unwrap();
    sqlx::query("SELECT id FROM core_identity.users WHERE id=1001 FOR SHARE")
        .fetch_one(&mut *next)
        .await
        .unwrap();
    assert!(matches!(
        abuse::reject_normal_model_in_transaction(&mut next, 1001).await,
        Err(E::Forbidden)
    ));
    next.rollback().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn marked_managed_key_still_obeys_ip_model_enabled_and_expiry_restrictions(pool: PgPool) {
    let f = fresh(&pool).await;
    mark(&f, 1001).await;
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,model_limits_enabled,model_limits,allow_ips,group_name) VALUES ($1,'Synthetic key',TRUE,ARRAY['public-model'],ARRAY['127.0.0.1/32']::inet[],'auto')").bind(f.key.id).execute(&pool).await.unwrap();
    assert!(matches!(
        f.abuse.lookup_model_policy(&f.key.secret, None).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        f.abuse
            .lookup_model_policy(&f.key.secret, Some("127.0.0.2".parse().unwrap()))
            .await,
        Err(E::Forbidden)
    ));
    let ip = Some("127.0.0.1".parse().unwrap());
    assert_eq!(
        f.abuse
            .lookup_model_policy(&f.key.secret, ip)
            .await
            .unwrap()
            .unwrap()
            .public_models,
        vec!["public-model"]
    );
    assert!(matches!(
        f.abuse
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "second-model",
                ip,
                "model-policy-denied"
            )
            .await,
        Err(E::Forbidden)
    ));
    let a = f
        .abuse
        .admit_model(
            &f.key.secret,
            ClientProtocol::OpenaiChat,
            "public-model",
            ip,
            "model-policy-allowed",
        )
        .await
        .unwrap()
        .unwrap();
    f.abuse.finish_model(&a.lease_id).await.unwrap();
    sqlx::query("UPDATE core_identity.api_key_settings SET enabled=FALSE WHERE credential_id=$1")
        .bind(f.key.id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        f.abuse.lookup_model_policy(&f.key.secret, ip).await,
        Err(E::Unauthorized)
    ));
}

#[sqlx::test(migrations = false)]
async fn shared_global_limit_counts_other_users_and_expired_leases_do_not_hold_capacity(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    let other_key = f
        .identity
        .issue_key(
            &f.other.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1002,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    mark(&f, 1001).await;
    mark(&f, 1002).await;
    let mut updated = config();
    updated.policies[0].limits.max_concurrent_global = 1;
    f.abuse
        .update_policies(
            &f.root.secret,
            &PolicyUpdate {
                operation_id: "global-one-policy".into(),
                reason: "Synthetic global bound".into(),
                expected_revision: 1,
                config: updated,
            },
        )
        .await
        .unwrap();
    let a = f
        .abuse
        .admit_model(
            &f.key.secret,
            ClientProtocol::OpenaiChat,
            "public-model",
            None,
            "user-one-live",
        )
        .await
        .unwrap()
        .unwrap();
    assert!(matches!(
        f.abuse
            .admit_model(
                &other_key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "user-two-blocked"
            )
            .await,
        Err(E::RateLimited)
    ));
    sqlx::query("UPDATE core_abuse.model_leases SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1").bind(&a.lease_id).execute(&pool).await.unwrap();
    let b = f
        .abuse
        .admit_model(
            &other_key.secret,
            ClientProtocol::OpenaiChat,
            "public-model",
            None,
            "user-two-allowed",
        )
        .await
        .unwrap()
        .unwrap();
    f.abuse.finish_model(&b.lease_id).await.unwrap();
    assert!(
        matches!(
            f.abuse
                .admit_model(
                    &f.key.secret,
                    ClientProtocol::OpenaiChat,
                    "public-model",
                    None,
                    "user-one-live"
                )
                .await,
            Err(E::Conflict)
        ),
        "expired ID must not dispatch a second time within retention"
    );
}

#[sqlx::test(migrations = false)]
async fn marking_blocks_new_real_reservations_but_preserves_existing_real_settlement(pool: PgPool) {
    use lmm_core::billing::{
        Credits,
        charging::{Charging, Error, Outcome, Price, Request as ChargeRequest, Usage},
        ledger::{Action as LedgerAction, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    };
    let f = fresh(&pool).await;
    let account: i64 =
        sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=1001")
            .fetch_one(&pool)
            .await
            .unwrap();
    let ledger = Ledger::from_pool(pool.clone());
    ledger.open_wallet(account).await.unwrap();
    ledger
        .execute(&LedgerRequest {
            scope: "abuse-financial-acceptance".into(),
            operation_key: "synthetic-funding".into(),
            actor_user_id: None,
            reason: "Isolated synthetic funds".into(),
            action: LedgerAction::Credit {
                account_id: account,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic-abuse-test".into(),
            },
        })
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    role.optional("core_charging").await;
    let runtime_pool = sqlx::postgres::PgPoolOptions::new()
        .max_connections(2)
        .connect(&role.url)
        .await
        .unwrap();
    let (session_user, current_user): (String, String) =
        sqlx::query_as("SELECT session_user::text,current_user::text")
            .fetch_one(&runtime_pool)
            .await
            .unwrap();
    assert_eq!(session_user, role.name);
    assert_eq!(current_user, role.name);
    let charging = Charging::new(
        runtime_pool.clone(),
        std::sync::Arc::new(TransactionalLedger),
    );
    let worker = [37; 32];
    let old = ChargeRequest {
        id: "before-mark-real-charge".into(),
        fingerprint: [39; 32],
        reserve: 50,
        lease_seconds: 60,
        maximum_seconds: 120,
        price: Price {
            upstream_cost_group_ppm: None,
            version: "synthetic-v1".into(),
            model: "public-model".into(),
            group: "synthetic-group".into(),
            input_per_million: 1_000_000,
            cached_per_million: 0,
            output_per_million: 1_000_000,
        },
    };
    charging
        .reserve(&f.key.secret, &old, &worker)
        .await
        .unwrap();
    charging.start(&old.id, &worker).await.unwrap();
    mark(&f, 1001).await;
    let before = financial_snapshot(&pool).await;
    let existing = charging.resolve(&old.id).await.unwrap().unwrap();
    let replay = charging
        .reserve(&f.key.secret, &old, &worker)
        .await
        .unwrap();
    assert_eq!(
        serde_json::to_value(existing).unwrap(),
        serde_json::to_value(replay).unwrap()
    );
    assert_eq!(
        before,
        financial_snapshot(&pool).await,
        "existing reservation replay changed financial state"
    );
    let new = ChargeRequest {
        id: "after-mark-denied-charge".into(),
        ..old.clone()
    };
    assert_eq!(
        charging
            .reserve(&f.key.secret, &new, &worker)
            .await
            .unwrap_err(),
        Error::Forbidden
    );
    assert_eq!(
        before,
        financial_snapshot(&pool).await,
        "new denied reservation mutated real financial state"
    );
    charging
        .increase(&f.key.secret, &old.id, &worker, 100)
        .await
        .unwrap();
    let settled = charging
        .settle(
            &old.id,
            &worker,
            Usage {
                output: 75,
                ..Usage::default()
            },
            Outcome::Completed,
        )
        .await
        .unwrap();
    assert_eq!(settled.settled, 75);
    assert_eq!(settled.state, "settled");
    let missing:bool=sqlx::query_scalar("SELECT NOT EXISTS(SELECT 1 FROM core_charging.requests WHERE id='after-mark-denied-charge')").fetch_one(&pool).await.unwrap();
    assert!(missing);
    runtime_pool.close().await;
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn identity_only_runtime_fixture_preserves_its_original_schema_scope(pool: PgPool) {
    let identity = IdentityStore::from_pool(pool.clone());
    identity.init_database().await.unwrap();
    sqlx::raw_sql(include_str!("../schema/ledger.sql"))
        .execute(&pool)
        .await
        .unwrap();
    let user = identity.bootstrap_user(1001, 0).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    let absent: bool = sqlx::query_scalar("SELECT to_regnamespace('core_abuse') IS NULL")
        .fetch_one(&role.pool)
        .await
        .unwrap();
    assert!(absent);
    assert_eq!(
        IdentityStore::from_pool(role.pool.clone())
            .authorize(&user.secret)
            .await
            .unwrap()
            .user_id,
        1001
    );
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn duplicate_concurrency_and_rpm_denials_persist_attempts_without_new_leases(pool: PgPool) {
    let f = fresh(&pool).await;
    mark(&f, 1001).await;
    let before = financial_snapshot(&pool).await;
    let role = runtime::RuntimeRole::create(&pool).await;
    let store = AbuseStore::new(role.pool.clone(), BTreeSet::new());
    let sums = |pool: PgPool| async move {
        sqlx::query_as::<_,(i64,i64)>("SELECT COALESCE(sum(attempts) FILTER (WHERE subject_id=0),0)::bigint,COALESCE(sum(attempts) FILTER (WHERE subject_id=1001),0)::bigint FROM core_abuse.model_counters").fetch_one(&pool).await.unwrap()
    };
    let initial = sums(pool.clone()).await;
    let first = store
        .admit_model(
            &f.key.secret,
            ClientProtocol::OpenaiChat,
            "public-model",
            None,
            "durable-first",
        )
        .await
        .unwrap()
        .unwrap();
    assert_eq!(sums(pool.clone()).await, (initial.0 + 1, initial.1 + 1));
    assert!(matches!(
        store
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "durable-first"
            )
            .await,
        Err(E::Conflict)
    ));
    assert_eq!(sums(pool.clone()).await, (initial.0 + 2, initial.1 + 2));
    assert!(matches!(
        store
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "durable-concurrency-denial"
            )
            .await,
        Err(E::RateLimited)
    ));
    assert_eq!(sums(pool.clone()).await, (initial.0 + 3, initial.1 + 3));
    let leases: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.model_leases")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(leases, 1);
    store.finish_model(&first.lease_id).await.unwrap();
    // Seed both adjacent real fixed windows beyond the configured quota so this
    // rejection oracle is stable across a minute rollover without changing time.
    sqlx::query("INSERT INTO core_abuse.model_counters AS c(subject_id,minute,attempts) SELECT 1001,floor(extract(epoch FROM clock_timestamp()))::bigint/60+delta,10 FROM generate_series(0,1) AS steps(delta) ON CONFLICT(subject_id,minute) DO UPDATE SET attempts=GREATEST(c.attempts,10)").execute(&pool).await.unwrap();
    let rpm_before = sums(pool.clone()).await;
    assert!(matches!(
        store
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "durable-rpm-denial"
            )
            .await,
        Err(E::RateLimited)
    ));
    assert_eq!(
        sums(pool.clone()).await,
        (rpm_before.0 + 1, rpm_before.1 + 1)
    );
    let leases_after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.model_leases")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(leases_after, leases);
    assert_eq!(before, financial_snapshot(&pool).await);
    sqlx::raw_sql(&format!(
        "REVOKE INSERT ON core_abuse.model_counters FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    assert!(matches!(
        store
            .admit_model(
                &f.key.secret,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "counter-storage-denial"
            )
            .await,
        Err(E::Unavailable)
    ));
    let after_storage: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.model_leases")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(after_storage, leases);
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn mark_changes_authority_tuple_without_changing_generation_and_old_snapshot_fails_closed(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    let mut stale = pool.begin().await.unwrap();
    sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
        .execute(&mut *stale)
        .await
        .unwrap();
    let generation: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=1001")
            .fetch_one(&mut *stale)
            .await
            .unwrap();
    mark(&f, 1001).await;
    let error = sqlx::query("SELECT id FROM core_identity.users WHERE id=1001 FOR SHARE")
        .fetch_one(&mut *stale)
        .await
        .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("40001")
    );
    stale.rollback().await.unwrap();
    let after: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=1001")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(generation, after);
    assert_eq!(
        f.identity.authorize(&f.user.secret).await.unwrap().user_id,
        1001
    );
    let mut fresh = pool.begin().await.unwrap();
    sqlx::query("SELECT id FROM core_identity.users WHERE id=1001 FOR SHARE")
        .fetch_one(&mut *fresh)
        .await
        .unwrap();
    assert!(matches!(
        abuse::reject_normal_model_in_transaction(&mut fresh, 1001).await,
        Err(E::Forbidden)
    ));
    fresh.rollback().await.unwrap();
}

async fn assert_component_failure(store: &AbuseStore, pool: &PgPool, key: &str) {
    assert!(matches!(
        store.lookup_model_policy(key, None).await,
        Err(E::Unavailable)
    ));
    assert!(matches!(
        store
            .admit_model(
                key,
                ClientProtocol::OpenaiChat,
                "public-model",
                None,
                "missing-component-admission"
            )
            .await,
        Err(E::Unavailable)
    ));
    let mut tx = pool.begin().await.unwrap();
    assert!(matches!(
        abuse::is_marked_in_transaction(&mut tx, 1001).await,
        Err(E::Unavailable)
    ));
    tx.rollback().await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert!(matches!(
        abuse::public_models_in_transaction(&mut tx, 1001).await,
        Err(E::Unavailable)
    ));
    tx.rollback().await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    assert!(matches!(
        abuse::reject_normal_model_in_transaction(&mut tx, 1001).await,
        Err(E::Unavailable)
    ));
    tx.rollback().await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn missing_mark_table_or_settings_singleton_never_means_unmarked(pool: PgPool) {
    let f = fresh(&pool).await;
    let mut tx = pool.begin().await.unwrap();
    assert!(
        !abuse::is_marked_in_transaction(&mut tx, 1001)
            .await
            .unwrap()
    );
    assert!(
        abuse::public_models_in_transaction(&mut tx, 1001)
            .await
            .unwrap()
            .is_none()
    );
    tx.rollback().await.unwrap();
    let before = financial_snapshot(&pool).await;
    sqlx::query("DELETE FROM core_abuse.settings WHERE singleton")
        .execute(&pool)
        .await
        .unwrap();
    assert_component_failure(&f.abuse, &pool, &f.key.secret).await;
    sqlx::query("INSERT INTO core_abuse.settings(singleton,revision,document) VALUES (TRUE,1,$1)")
        .bind(serde_json::to_value(config()).unwrap())
        .execute(&pool)
        .await
        .unwrap();
    sqlx::query("DROP TABLE core_abuse.user_marks")
        .execute(&pool)
        .await
        .unwrap();
    assert_component_failure(&f.abuse, &pool, &f.key.secret).await;
    assert_eq!(before, financial_snapshot(&pool).await);
}

#[sqlx::test(migrations = false)]
async fn missing_policy_read_permission_and_invalid_policy_fail_closed_for_unmarked_users(
    pool: PgPool,
) {
    let f = fresh(&pool).await;
    let role = runtime::RuntimeRole::create(&pool).await;
    let store = AbuseStore::new(role.pool.clone(), BTreeSet::new());
    let before = financial_snapshot(&pool).await;
    sqlx::raw_sql(&format!(
        "REVOKE SELECT ON core_abuse.settings FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    assert_component_failure(&store, &role.pool, &f.key.secret).await;
    sqlx::raw_sql(&format!(
        "GRANT SELECT ON core_abuse.settings TO {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    sqlx::raw_sql(&format!(
        "REVOKE SELECT ON core_abuse.user_marks FROM {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    assert_component_failure(&store, &role.pool, &f.key.secret).await;
    sqlx::raw_sql(&format!(
        "GRANT SELECT ON core_abuse.user_marks TO {}",
        role.name
    ))
    .execute(&pool)
    .await
    .unwrap();
    let mut invalid = serde_json::to_value(config()).unwrap();
    invalid["policies"][0]["limits"]["max_concurrent_global"] = json!(0);
    sqlx::query("UPDATE core_abuse.settings SET document=$1 WHERE singleton")
        .bind(invalid)
        .execute(&pool)
        .await
        .unwrap();
    assert_component_failure(&store, &role.pool, &f.key.secret).await;
    assert_eq!(before, financial_snapshot(&pool).await);
    let leases: i64 = sqlx::query_scalar("SELECT count(*) FROM core_abuse.model_leases")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(leases, 0);
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn restricted_runtime_preview_commit_and_replay_preserve_immutable_evidence(pool: PgPool) {
    use axum::{Router, http::Method};
    async fn call(
        app: &Router,
        secret: &str,
        method: Method,
        path: &str,
        input: Option<Value>,
    ) -> (StatusCode, Value) {
        let mut request = Request::builder()
            .method(method)
            .uri(path)
            .header("authorization", format!("Bearer {secret}"));
        let body = if let Some(value) = input {
            request = request.header("content-type", "application/json");
            Body::from(serde_json::to_vec(&value).unwrap())
        } else {
            Body::empty()
        };
        let response = app
            .clone()
            .oneshot(request.body(body).unwrap())
            .await
            .unwrap();
        let status = response.status();
        let value =
            serde_json::from_slice(&to_bytes(response.into_body(), 131072).await.unwrap()).unwrap();
        (status, value)
    }
    let f = fresh(&pool).await;
    let role = runtime::RuntimeRole::create(&pool).await;
    lmm_core::runtime_database::check(
        &role.pool,
        lmm_core::runtime_database::RequiredCapabilities::default(),
    )
    .await
    .unwrap();
    let (session_user, current_user): (String, String) =
        sqlx::query_as("SELECT session_user::text,current_user::text")
            .fetch_one(&role.pool)
            .await
            .unwrap();
    assert_eq!(session_user, role.name);
    assert_eq!(current_user, role.name);
    let (update, delete, column_update): (bool, bool, bool) = sqlx::query_as(
        "SELECT has_table_privilege('core_abuse.previews','UPDATE'),has_table_privilege('core_abuse.previews','DELETE'),has_any_column_privilege('core_abuse.previews','UPDATE')",
    ).fetch_one(&role.pool).await.unwrap();
    assert!(!update && !delete && !column_update);
    let store = AbuseStore::new(role.pool.clone(), BTreeSet::new());
    let app = admin::router(store);
    let before = financial_snapshot(&pool).await;
    let policy = PolicyUpdate {
        operation_id: "restricted-policy-update".into(),
        reason: "Restricted runtime regression".into(),
        expected_revision: 1,
        config: config(),
    };
    let (status, policy_result) = call(
        &app,
        &f.root.secret,
        Method::PUT,
        "/core/v1/admin/abuse/policies",
        Some(serde_json::to_value(policy).unwrap()),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(policy_result["policy_revision"], 2);
    let mut input = request(
        "restricted-preview-commit",
        Action::Mark,
        &[(1001, 0), (1002, 0)],
    );
    input.expected_policy_revision = 2;
    let (status, preview_result) = call(
        &app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/preview",
        Some(serde_json::to_value(&input).unwrap()),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let preview_id = preview_result["preview_id"].as_str().unwrap();
    let evidence_query = "SELECT jsonb_build_object('snapshot',snapshot,'response',response,'preview_hash',encode(preview_hash,'hex'),'expires_at',expires_at) FROM core_abuse.previews WHERE id=$1";
    let evidence_before: Value = sqlx::query_scalar(evidence_query)
        .bind(preview_id)
        .fetch_one(&pool)
        .await
        .unwrap();
    for forbidden in [
        "UPDATE core_abuse.previews SET snapshot=snapshot WHERE id=$1",
        "UPDATE core_abuse.previews SET response=response WHERE id=$1",
        "UPDATE core_abuse.previews SET preview_hash=preview_hash WHERE id=$1",
        "UPDATE core_abuse.previews SET expires_at=expires_at WHERE id=$1",
        "DELETE FROM core_abuse.previews WHERE id=$1",
        "SELECT id FROM core_abuse.previews WHERE id=$1 FOR SHARE",
    ] {
        let error = sqlx::query(forbidden)
            .bind(preview_id)
            .execute(&role.pool)
            .await
            .unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("42501")
        );
    }
    let commit = CommitRequest {
        operation_id: input.operation_id.clone(),
        preview_id: preview_id.into(),
        preview_hash: preview_result["preview_hash"].as_str().unwrap().into(),
    };
    let commit_json = serde_json::to_value(&commit).unwrap();
    let (status, committed) = call(
        &app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/commit",
        Some(commit_json.clone()),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(committed["kind"], "users");
    assert_eq!(committed["operation_id"], input.operation_id);
    assert_eq!(committed["users"].as_array().unwrap().len(), 2);
    for (index, id) in [1001, 1002].into_iter().enumerate() {
        assert_eq!(committed["users"][index]["user_id"], id);
        assert_eq!(committed["users"][index]["revision"], 1);
        assert_eq!(committed["users"][index]["marked"], true);
        let (status, read) = call(
            &app,
            &f.root.secret,
            Method::GET,
            &format!("/core/v1/admin/abuse/users/{id}"),
            None,
        )
        .await;
        assert_eq!(status, StatusCode::OK);
        assert_eq!(read, committed["users"][index]);
    }
    let (status, recorded) = call(
        &app,
        &f.root.secret,
        Method::GET,
        "/core/v1/admin/abuse/operations/restricted-preview-commit",
        None,
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(recorded, committed);
    let (status, replay) = call(
        &app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/commit",
        Some(commit_json.clone()),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(replay, committed);
    let mut conflict = commit_json;
    conflict["preview_hash"] = json!("0".repeat(64));
    let (status, _) = call(
        &app,
        &f.root.secret,
        Method::POST,
        "/core/v1/admin/abuse/users/commit",
        Some(conflict),
    )
    .await;
    assert_eq!(status, StatusCode::CONFLICT);
    let events: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_abuse.audit WHERE operation_id='restricted-preview-commit'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(events, 1);
    let evidence_after: Value = sqlx::query_scalar(evidence_query)
        .bind(preview_id)
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(evidence_before, evidence_after);
    assert_eq!(before, financial_snapshot(&pool).await);
    role.cleanup().await;
}
