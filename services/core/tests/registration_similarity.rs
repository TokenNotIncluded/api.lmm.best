//! Disposable real PostgreSQL evidence tests. These do not contact a provider or production.
use lmm_core::{
    abuse::{
        AbuseError, AbuseStore,
        registration::{self, RegistrationSource, RegistrationSourceKind, SimilarityQuery},
    },
    accounts::{Account, AccountKind},
    core_schema,
    funding::FundingPolicy,
    identity::{IdentityStore, IssuedCredential},
};
use sqlx::{PgPool, Row};
use std::collections::BTreeSet;
async fn fixture(
    pool: &PgPool,
) -> (
    IdentityStore,
    AbuseStore,
    IssuedCredential,
    IssuedCredential,
) {
    core_schema::init(pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let root = identity.bootstrap_user(1000, 6).await.unwrap();
    let user = identity.bootstrap_user(1001, 0).await.unwrap();
    (
        identity,
        AbuseStore::new(pool.clone(), BTreeSet::new()),
        root,
        user,
    )
}
#[sqlx::test(migrations = false)]
async fn unknown_history_is_never_reconstructed_and_requires_root_session(pool: PgPool) {
    let (identity, store, root, user) = fixture(&pool).await;
    let response = store
        .registration_similarity(&root.secret, 1001, &SimilarityQuery::default())
        .await
        .unwrap();
    assert!(response["subject"]["registered_at"].is_null());
    assert!(response["subject"]["source_ip"].is_null());
    assert_eq!(response["subject"]["comparison_basis"], "current_profile");
    assert_eq!(response["assessment"]["account_actions_authorized"], false);
    assert!(response["assessment"]["probability"].is_null());
    assert_eq!(
        store
            .registration_similarity(&user.secret, 1001, &SimilarityQuery::default())
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
    let admin = identity.bootstrap_user(1002, 5).await.unwrap();
    assert_eq!(
        store
            .registration_similarity(&admin.secret, 1001, &SimilarityQuery::default())
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
    let key = identity
        .issue_key(
            &root.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1000,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    assert_eq!(
        store
            .registration_similarity(&key.secret, 1001, &SimilarityQuery::default())
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
    identity.revoke(&root.secret, root.id).await.unwrap();
    assert_eq!(
        store
            .registration_similarity(&root.secret, 1001, &SimilarityQuery::default())
            .await
            .unwrap_err(),
        AbuseError::Unauthorized
    );
}
#[sqlx::test(migrations = false)]
async fn observations_are_atomic_preserve_snapshots_and_fail_duplicate(pool: PgPool) {
    let (_, store, root, _) = fixture(&pool).await;
    sqlx::query("UPDATE core_identity.user_profiles SET username='template1234',email='sample1234@example.invalid' WHERE user_id=1001").execute(&pool).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    registration::record(&mut tx, 1001, None).await.unwrap();
    tx.rollback().await.unwrap();
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_abuse.registration_observations")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
    let mut tx = pool.begin().await.unwrap();
    registration::record(
        &mut tx,
        1001,
        Some(RegistrationSource {
            address: "192.0.2.10".parse().unwrap(),
            kind: RegistrationSourceKind::TrustedXForwardedFor,
        }),
    )
    .await
    .unwrap();
    tx.commit().await.unwrap();
    sqlx::query("UPDATE core_identity.user_profiles SET username='renamed',email='renamed@example.invalid' WHERE user_id=1001").execute(&pool).await.unwrap();
    let v = store
        .registration_similarity(&root.secret, 1001, &SimilarityQuery::default())
        .await
        .unwrap();
    assert_eq!(v["subject"]["username"], "renamed");
    assert_eq!(v["subject"]["registration_username"], "template1234");
    assert_eq!(
        v["subject"]["registration_email"],
        "sample1234@example.invalid"
    );
    assert_eq!(v["subject"]["source_ip"], "192.0.2.10");
    assert_eq!(v["subject"]["source_policy_version"], 1);
    assert!(v["subject"]["registered_at"].as_i64().unwrap() > 0);
    let mut tx = pool.begin().await.unwrap();
    assert!(registration::record(&mut tx, 1001, None).await.is_err());
    tx.rollback().await.unwrap();
    let saved=sqlx::query("SELECT source_kind,username_at_registration FROM core_abuse.registration_observations WHERE user_id=1001").fetch_one(&pool).await.unwrap();
    assert_eq!(
        saved.try_get::<String, _>("source_kind").unwrap(),
        "trusted_x_forwarded_for"
    );
    assert_eq!(
        saved
            .try_get::<String, _>("username_at_registration")
            .unwrap(),
        "template1234"
    );
}
#[sqlx::test(migrations = false)]
async fn composite_candidates_paginate_and_do_not_change_money_or_marks(pool: PgPool) {
    let (identity, store, root, _) = fixture(&pool).await;
    identity.bootstrap_user(1002, 0).await.unwrap();
    identity.bootstrap_user(1003, 0).await.unwrap();
    let before:serde_json::Value=sqlx::query_scalar("SELECT jsonb_build_object('marks',(SELECT count(*) FROM core_abuse.user_marks),'requests',(SELECT count(*) FROM core_charging.requests),'ledger',(SELECT count(*) FROM core_billing.ledger_entries))").fetch_one(&pool).await.unwrap();
    for (id, name, email) in [
        (1001, "template1234", "sample1234@example.invalid"),
        (1002, "template5678", "sample5678@example.invalid"),
        (1003, "template9999", "sample9999@example.invalid"),
    ] {
        sqlx::query("UPDATE core_identity.user_profiles SET username=$2,email=$3 WHERE user_id=$1")
            .bind(id)
            .bind(name)
            .bind(email)
            .execute(&pool)
            .await
            .unwrap();
        let mut tx = pool.begin().await.unwrap();
        registration::record(
            &mut tx,
            id,
            Some(RegistrationSource {
                address: "192.0.2.10".parse().unwrap(),
                kind: RegistrationSourceKind::Socket,
            }),
        )
        .await
        .unwrap();
        tx.commit().await.unwrap();
    }
    let first = store
        .registration_similarity(
            &root.secret,
            1003,
            &SimilarityQuery {
                scan_limit: Some(1),
                limit: Some(1),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(first["candidates"][0]["user"]["user_id"], 1002);
    assert_eq!(first["candidates"][0]["review_priority"], "high");
    assert_eq!(first["coverage"]["scan_truncated"], true);
    assert_eq!(first["coverage"]["next_before_user_id"], 1002);
    let next = store
        .registration_similarity(
            &root.secret,
            1003,
            &SimilarityQuery {
                before_user_id: Some(1002),
                ..Default::default()
            },
        )
        .await
        .unwrap();
    assert_eq!(next["candidates"][0]["user"]["user_id"], 1001);
    assert!(next["coverage"]["next_before_user_id"].is_null());
    let after:serde_json::Value=sqlx::query_scalar("SELECT jsonb_build_object('marks',(SELECT count(*) FROM core_abuse.user_marks),'requests',(SELECT count(*) FROM core_charging.requests),'ledger',(SELECT count(*) FROM core_billing.ledger_entries))").fetch_one(&pool).await.unwrap();
    assert_eq!(before, after);
}
#[sqlx::test(migrations = false)]
async fn missing_observation_table_fails_closed_not_empty_success(pool: PgPool) {
    let (_, store, root, _) = fixture(&pool).await;
    sqlx::query("DROP TABLE core_abuse.registration_observations")
        .execute(&pool)
        .await
        .unwrap();
    assert_eq!(
        store
            .registration_similarity(&root.secret, 1001, &SimilarityQuery::default())
            .await
            .unwrap_err(),
        AbuseError::Unavailable
    );
}
#[sqlx::test(migrations = false)]
async fn platform_model_metadata_requires_actual_owned_finite_key_policy(pool: PgPool) {
    use lmm_core::billing::{
        charging::{Budget, Charging},
        transactional_ledger::TransactionalLedger,
    };
    use std::sync::Arc;
    let (identity, store, _, user) = fixture(&pool).await;
    assert_eq!(
        store
            .assistant_model_credential(&user.secret)
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
    let mut policy = FundingPolicy::new_personal_key();
    policy.personal_billing_preference = Some(lmm_core::funding::BillingPreference::WalletOnly);
    let key = identity
        .issue_key(
            &user.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1001,
            },
            policy,
            3600,
        )
        .await
        .unwrap();
    assert_eq!(
        store
            .assistant_model_credential(&key.secret)
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,model_limits_enabled,model_limits,group_name) VALUES($1,'Platform registration review',TRUE,ARRAY['review-model'],'default')").bind(key.id).execute(&pool).await.unwrap();
    let charging = Charging::new(pool.clone(), Arc::new(TransactionalLedger));
    charging
        .set_budget(
            &user.secret,
            &Budget {
                scope: "key".into(),
                account_id: None,
                user_id: None,
                key_id: Some(key.id),
                period: "lifetime".into(),
                anchor: 0,
                seconds: 0,
                limit: 100,
            },
        )
        .await
        .unwrap();
    let v = store.assistant_model_credential(&key.secret).await.unwrap();
    assert_eq!(v["credential_kind"], "api_key");
    assert_eq!(v["owner"]["id"], 1001);
    assert_eq!(v["funding_accounts"].as_array().unwrap().len(), 1);
    assert_eq!(v["budget"]["limit_credits"], 100);
    assert_eq!(v["budget"]["unlimited"], false);
    assert!(v["purpose_grant"].is_null());
    sqlx::query("UPDATE core_identity.api_key_settings SET model_limits_enabled=FALSE WHERE credential_id=$1").bind(key.id).execute(&pool).await.unwrap();
    assert_eq!(
        store
            .assistant_model_credential(&key.secret)
            .await
            .unwrap_err(),
        AbuseError::Forbidden
    );
}
