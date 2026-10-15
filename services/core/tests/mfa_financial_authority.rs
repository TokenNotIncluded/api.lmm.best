//! Durable financial-authority regressions migrated from independent MFA acceptance.
//! SQLx-owned synthetic PostgreSQL and private runtime credentials only.
#[path = "common/runtime.rs"]
mod runtime;
use lmm_core::{
    billing::{
        charging::{Budget, Charging, Error},
        transactional_ledger::TransactionalLedger,
    },
    core_schema,
    identity::{BrowserAuth, BrowserRegistration, BrowserSessionMetadata, IdentityStore},
};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::sync::Arc;
use totp_rs::{Algorithm, Builder, Secret};
fn budget(user: i64, limit: i64) -> Budget {
    Budget {
        scope: "self".into(),
        account_id: None,
        user_id: Some(user),
        key_id: None,
        period: "day".into(),
        anchor: 0,
        seconds: 0,
        limit,
    }
}
async fn assert_runtime_identity(role: &runtime::RuntimeRole) {
    let (session_user, current_user): (String, String) =
        sqlx::query_as("SELECT session_user::text,current_user::text")
            .fetch_one(&role.pool)
            .await
            .expect("runtime connection identity query failed");
    let url = reqwest::Url::parse(&role.url).expect("invalid synthetic runtime URL");
    assert!(
        url.username() == role.name,
        "runtime URL must identify the freshly created restricted role"
    );
    assert!(
        session_user == role.name && current_user == role.name,
        "financial regression must use the freshly created restricted connection"
    );
}
async fn counts(pool: &PgPool) -> (i64, i64, i64) {
    sqlx::query_as("SELECT (SELECT count(*) FROM core_charging.budgets),(SELECT count(*) FROM core_charging.policy_events),coalesce((SELECT sum(revision) FROM core_charging.budgets),0)::bigint").fetch_one(pool).await.unwrap()
}
async fn naked(pool: &PgPool, user: i64, secret: &str) -> i64 {
    sqlx::query_scalar("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at) SELECT $1,'session',id,auth_version,personal_account_id,clock_timestamp()+interval '10 minutes' FROM core_identity.users WHERE id=$2 RETURNING id").bind(Sha256::digest(secret.as_bytes()).to_vec()).bind(user).fetch_one(pool).await.unwrap()
}
#[sqlx::test(migrations = false)]
async fn independent_budget_rejects_missing_proof_without_policy_mutation(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let identity = IdentityStore::from_pool(pool.clone());
    let genuine = identity.bootstrap_user(71, 1).await.unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_runtime_identity(&role).await;
    role.optional("core_charging").await;
    let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
    charging
        .set_budget(&genuine.secret, &budget(71, 17))
        .await
        .unwrap();
    let before = counts(&pool).await;
    let secret = "lmms_independent_no_assurance_credential_0001";
    let id = naked(&pool, 71, secret).await;
    let proofs: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.session_authentication WHERE credential_id=$1",
    )
    .bind(id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(proofs, 0);
    assert_eq!(
        charging
            .set_budget(secret, &budget(71, 999))
            .await
            .unwrap_err(),
        Error::Unauthorized
    );
    assert_eq!(counts(&pool).await, before);
    let current: i64 =
        sqlx::query_scalar("SELECT limit_credits FROM core_charging.budgets WHERE user_id=71")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(current, 17);
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn independent_enforced_budget_rejects_primary_and_wrong_revision_proofs(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let owner = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_mfa_keys(
            "independent".into(),
            vec![("independent".into(), vec![53; 32])],
        )
        .unwrap();
    let browser = BrowserAuth::new(owner.clone(), &[61; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "billing-mfa",
            password: "independent pass",
            email: "",
        })
        .await
        .unwrap();
    let login = browser
        .login(
            "billing-mfa",
            "independent pass",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    let setup = browser
        .mfa_setup(login.data["access_token"].as_str().unwrap())
        .await
        .unwrap();
    let now: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(&pool)
            .await
            .unwrap();
    let secret = Secret::try_from_base32(&setup.secret).unwrap();
    let otp = Builder::new()
        .with_algorithm(Algorithm::SHA1)
        .with_digits(6)
        .with_step_duration(30)
        .with_skew(1)
        .with_secret(secret.as_bytes())
        .build()
        .unwrap()
        .generate(now as u64)
        .to_string();
    let live = browser
        .mfa_enable(login.data["access_token"].as_str().unwrap(), &otp)
        .await
        .unwrap();
    let user = live.data["user"]["id"].as_i64().unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_runtime_identity(&role).await;
    role.optional("core_charging").await;
    let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
    charging
        .set_budget(
            live.data["access_token"].as_str().unwrap(),
            &budget(user, 23),
        )
        .await
        .unwrap();
    let before = counts(&pool).await;
    for (suffix, level, revision_delta) in [("primary", 1, 0), ("wrong_revision", 2, 1)] {
        let token = format!("lmms_independent_{suffix}_assurance_000000000000");
        let id = naked(&pool, user, &token).await;
        sqlx::query("INSERT INTO core_identity.session_authentication(credential_id,user_id,user_version,factor_revision,factor_level,method,authenticated_at,mfa_verified_at) SELECT $1,u.id,u.auth_version,CASE WHEN $2=1 THEN 0 ELSE f.revision+$3 END,$2,CASE WHEN $2=1 THEN 'primary' ELSE 'totp' END,clock_timestamp(),CASE WHEN $2=1 THEN NULL ELSE clock_timestamp() END FROM core_identity.users u JOIN core_identity.mfa_factors f ON f.user_id=u.id WHERE u.id=$4").bind(id).bind(level as i16).bind(revision_delta as i64).bind(user).execute(&pool).await.unwrap();
        assert_eq!(
            charging
                .set_budget(&token, &budget(user, 999))
                .await
                .unwrap_err(),
            Error::Unauthorized
        );
        assert_eq!(counts(&pool).await, before);
    }
    role.cleanup().await;
}
#[sqlx::test(migrations = false)]
async fn independent_expired_browser_family_cannot_change_budget(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let owner = IdentityStore::from_pool(pool.clone()).with_registration_enabled(true);
    let browser = BrowserAuth::new(owner.clone(), &[61; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "expired-budget",
            password: "independent pass",
            email: "",
        })
        .await
        .unwrap();
    let live = browser
        .login(
            "expired-budget",
            "independent pass",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    let user = live.data["user"]["id"].as_i64().unwrap();
    let access = live.data["access_token"].as_str().unwrap();
    let sid = live.data["session"]["sid"].as_str().unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_runtime_identity(&role).await;
    role.optional("core_charging").await;
    let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
    charging
        .set_budget(access, &budget(user, 31))
        .await
        .unwrap();
    let before = counts(&pool).await;
    sqlx::query("UPDATE core_identity.web_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE sid=$1").bind(sid).execute(&pool).await.unwrap();
    assert!(
        owner.authorize(access).await.is_err(),
        "Identity must reject the expired browser family as the positive control"
    );
    assert_eq!(
        charging
            .set_budget(access, &budget(user, 999))
            .await
            .unwrap_err(),
        Error::Unauthorized
    );
    assert_eq!(counts(&pool).await, before);
    role.cleanup().await;
}

async fn oracle(pool: &PgPool, id: i64) -> (i64, i64, i64) {
    sqlx::query_as("SELECT b.limit_credits,b.revision,(SELECT count(*) FROM core_charging.policy_events e WHERE e.budget_id=b.id) FROM core_charging.budgets b WHERE b.id=$1").bind(id).fetch_one(pool).await.unwrap()
}
#[sqlx::test(migrations = false)]
async fn independent_refresh_revokes_old_budget_bearer_and_expiry_rejects_both(pool: PgPool) {
    core_schema::init(&pool).await.unwrap();
    let owner = IdentityStore::from_pool(pool.clone()).with_registration_enabled(true);
    let browser = BrowserAuth::new(owner.clone(), &[73; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "budget-refresh",
            password: "independent pass",
            email: "",
        })
        .await
        .unwrap();
    let original = browser
        .login(
            "budget-refresh",
            "independent pass",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    let role = runtime::RuntimeRole::create(&pool).await;
    assert_runtime_identity(&role).await;
    role.optional("core_charging").await;
    let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
    let result=tokio::spawn(async move {
  let user=original.data["user"]["id"].as_i64().unwrap();
  let sid=original.data["session"]["sid"].as_str().unwrap().to_owned();
  let old=original.data["access_token"].as_str().unwrap().to_owned();
  assert!(owner.authorize(&old).await.is_ok());
  let id=charging.set_budget(&old,&budget(user,17)).await.unwrap();
  assert_eq!(oracle(&pool,id).await,(17,1,1));
  let refreshed=browser.refresh(&original.refresh_token,Some(&sid)).await.unwrap();
  assert!(refreshed.data["session"]["sid"].as_str().unwrap() == sid, "refresh must preserve the browser family");
  let new=refreshed.data["access_token"].as_str().unwrap();assert!(old != new, "refresh must rotate the access bearer");
  assert!(owner.authorize(&old).await.is_err());assert!(owner.authorize(new).await.is_ok());
  assert_eq!(charging.set_budget(&old,&budget(user,999)).await.unwrap_err(),Error::Unauthorized);
  assert_eq!(oracle(&pool,id).await,(17,1,1));
  assert_eq!(charging.set_budget(new,&budget(user,23)).await.unwrap(),id);
  assert_eq!(oracle(&pool,id).await,(23,2,2));
  let mapped:i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.web_session_credentials WHERE sid=$1").bind(&sid).fetch_one(&pool).await.unwrap();assert_eq!(mapped,2);
  let updated=sqlx::query("UPDATE core_identity.web_sessions SET expires_at=clock_timestamp()-interval '1 second' WHERE sid=$1").bind(&sid).execute(&pool).await.unwrap();assert_eq!(updated.rows_affected(),1);
  for bearer in [&old,new] {
   assert!(owner.authorize(bearer).await.is_err());
   assert_eq!(charging.set_budget(bearer,&budget(user,999)).await.unwrap_err(),Error::Unauthorized);
   assert_eq!(oracle(&pool,id).await,(23,2,2));
  }
 }).await;
    role.cleanup().await;
    result.unwrap();
}
