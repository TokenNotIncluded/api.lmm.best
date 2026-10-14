//! Author regressions against isolated PostgreSQL; no production credentials.
use lmm_core::identity::{
    BrowserAuth, BrowserBundle, BrowserLoginOutcome, BrowserRegistration, BrowserSessionMetadata,
    IdentityError, IdentityStore, LoginOutcome, LoginRequest, MfaChallenge, MfaCompletion,
    MfaError, MfaSetup,
};
use serde_json::Value;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use totp_rs::{Algorithm, Builder, Secret};

const PASSWORD: &str = "test passphrase";
const NEW_PASSWORD: &str = "new test passphrase";
const BINDING: &str = "1111111111111111111111111111111111111111111111111111111111111111";
const OTHER_BINDING: &str = "2222222222222222222222222222222222222222222222222222222222222222";

async fn fixture(pool: &PgPool) -> (IdentityStore, BrowserAuth, BrowserBundle) {
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_mfa_keys("test".into(), vec![("test".into(), vec![47; 32])])
        .unwrap();
    store.init_database().await.unwrap();
    let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "alice",
            password: PASSWORD,
            email: "",
        })
        .await
        .unwrap();
    let bundle = browser
        .login("alice", PASSWORD, &BrowserSessionMetadata::default())
        .await
        .unwrap();
    (store, browser, bundle)
}

fn access(bundle: &BrowserBundle) -> &str {
    bundle.data["access_token"].as_str().unwrap()
}

fn sid(bundle: &BrowserBundle) -> &str {
    bundle.data["session"]["sid"].as_str().unwrap()
}

fn user(bundle: &BrowserBundle) -> i64 {
    bundle.data["user"]["id"].as_i64().unwrap()
}

async fn epoch(pool: &PgPool) -> u64 {
    let epoch: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(pool)
            .await
            .unwrap();
    u64::try_from(epoch).unwrap()
}

fn code_at(secret: &str, epoch: u64) -> String {
    let secret = Secret::try_from_base32(secret).unwrap();
    Builder::new()
        .with_algorithm(Algorithm::SHA1)
        .with_digits(6)
        .with_step_duration(30)
        .with_skew(1)
        .with_secret(secret.as_bytes())
        .build()
        .unwrap()
        .generate(epoch)
        .to_string()
}

async fn enable(
    pool: &PgPool,
    browser: &BrowserAuth,
    original: &BrowserBundle,
) -> (MfaSetup, BrowserBundle, String) {
    let setup = browser.mfa_setup(access(original)).await.unwrap();
    assert_eq!(setup.backup_codes.len(), 4);
    assert!(setup.qr_code_data.starts_with("otpauth://totp/"));
    let code = code_at(&setup.secret, epoch(pool).await);
    let enabled = browser.mfa_enable(access(original), &code).await.unwrap();
    (setup, enabled, code)
}

async fn native_challenge(pool: &PgPool, store: &IdentityStore, password: &str) -> MfaChallenge {
    let canonical: String = sqlx::query_scalar(
        "SELECT p.login_name FROM core_identity.password_logins p
         JOIN core_identity.user_profiles u ON u.user_id=p.user_id WHERE u.username='alice'",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    match store
        .login_with_mfa(&LoginRequest {
            login_name: canonical,
            password: password.into(),
        })
        .await
        .unwrap()
    {
        LoginOutcome::Challenge(challenge) => challenge,
        LoginOutcome::Session(_) => panic!("password alone bypassed the required factor"),
    }
}

fn completion(challenge: &MfaChallenge, code: &str) -> MfaCompletion {
    MfaCompletion {
        flow_token: challenge.flow_token.clone(),
        code: code.into(),
    }
}

async fn generation(pool: &PgPool, user: i64) -> i64 {
    sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
        .bind(user)
        .fetch_one(pool)
        .await
        .unwrap()
}

// Only nonsecret proof fields are compared or printed by assertion failures.
async fn assurance(pool: &PgPool, credential: i64) -> (i16, String, i64, i64, Option<i64>) {
    sqlx::query_as(
        "SELECT factor_level,method,factor_revision,
         floor(extract(epoch FROM authenticated_at))::bigint,
         floor(extract(epoch FROM mfa_verified_at))::bigint
         FROM core_identity.session_authentication WHERE credential_id=$1",
    )
    .bind(credential)
    .fetch_one(pool)
    .await
    .unwrap()
}

async fn browser_assurance(
    pool: &PgPool,
    store: &IdentityStore,
    bundle: &BrowserBundle,
) -> (i16, String, i64, i64, Option<i64>) {
    let credential = store.authorize(access(bundle)).await.unwrap().credential_id;
    assurance(pool, credential).await
}

async fn bounds(pool: &PgPool, sid: &str) -> Value {
    sqlx::query_scalar(
        "SELECT jsonb_build_array(created_at,expires_at,weekly_logout,ip,user_agent)
         FROM core_identity.web_sessions WHERE sid=$1",
    )
    .bind(sid)
    .fetch_one(pool)
    .await
    .unwrap()
}

#[sqlx::test(migrations = false)]
async fn enrollment_survives_same_family_refresh_and_enable_revokes_old_generations(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let other = browser
        .login("alice", PASSWORD, &BrowserSessionMetadata::default())
        .await
        .unwrap();
    let canonical: String =
        sqlx::query_scalar("SELECT login_name FROM core_identity.password_logins WHERE user_id=$1")
            .bind(user(&original))
            .fetch_one(&pool)
            .await
            .unwrap();
    let native = store
        .login(&LoginRequest {
            login_name: canonical,
            password: PASSWORD.into(),
        })
        .await
        .unwrap();
    let initial_generation = generation(&pool, user(&original)).await;
    let original_bounds = bounds(&pool, sid(&original)).await;
    let setup = browser.mfa_setup(access(&original)).await.unwrap();
    assert_eq!(setup.backup_codes.len(), 4);
    assert!(matches!(
        browser
            .mfa_enable(access(&other), &code_at(&setup.secret, epoch(&pool).await))
            .await,
        Err(MfaError::FlowExpired)
    ));
    let refreshed = browser
        .refresh(&original.refresh_token, Some(sid(&original)))
        .await
        .unwrap();
    assert_eq!(sid(&refreshed), sid(&original));
    let enabled = browser
        .mfa_enable(
            access(&refreshed),
            &code_at(&setup.secret, epoch(&pool).await),
        )
        .await
        .unwrap();
    assert_eq!(sid(&enabled), sid(&original));
    assert_eq!(bounds(&pool, sid(&enabled)).await, original_bounds);
    assert_eq!(
        generation(&pool, user(&enabled)).await,
        initial_generation + 1
    );
    for old in [
        access(&original),
        access(&refreshed),
        access(&other),
        &native.secret,
    ] {
        assert!(matches!(
            store.authorize(old).await,
            Err(IdentityError::Unauthorized)
        ));
    }
    for old in [&original, &refreshed, &other] {
        assert!(
            browser
                .refresh(&old.refresh_token, Some(sid(old)))
                .await
                .is_err()
        );
    }
    let proof = browser_assurance(&pool, &store, &enabled).await;
    assert_eq!(proof.0, 2);
    assert_eq!(proof.1, "totp");
    assert!(proof.4.is_some());
    let status = browser.mfa_status(access(&enabled)).await.unwrap();
    assert!(status.enabled);
    assert_eq!(status.backup_codes_remaining, 4);
}

#[sqlx::test(migrations = false)]
async fn challenges_are_bound_and_wrong_factor_attempts_commit_exactly_once(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let (setup, enabled, _) = enable(&pool, &browser, &original).await;
    let credentials_before: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials")
            .fetch_one(&pool)
            .await
            .unwrap();
    let challenge = match browser
        .login_with_mfa(
            "alice",
            PASSWORD,
            &BrowserSessionMetadata::default(),
            BINDING,
        )
        .await
        .unwrap()
    {
        BrowserLoginOutcome::Challenge(challenge) => challenge,
        BrowserLoginOutcome::Session(_) => panic!("browser password bypassed the required factor"),
    };
    assert!(challenge.require_2fa);
    assert!(store.authorize(&challenge.flow_token).await.is_err());
    let credentials_after: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(credentials_before, credentials_after);
    let valid = completion(&challenge, &setup.backup_codes[0]);
    assert!(matches!(
        browser.complete_mfa_login(&valid, OTHER_BINDING).await,
        Err(MfaError::FlowExpired)
    ));
    assert!(matches!(
        store.complete_mfa_login(&valid).await,
        Err(MfaError::FlowExpired)
    ));
    let failures: i32 = sqlx::query_scalar(
        "SELECT failed_attempts FROM core_identity.mfa_factors WHERE user_id=$1",
    )
    .bind(user(&enabled))
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(failures, 0);
    assert!(matches!(
        browser
            .complete_mfa_login(&completion(&challenge, "invalid"), BINDING)
            .await,
        Err(MfaError::CodeInvalid)
    ));
    let counts: (i32, i32, bool) = sqlx::query_as(
        "SELECT f.failed_attempts,c.attempts,c.consumed_at IS NOT NULL
         FROM core_identity.mfa_factors f JOIN core_identity.mfa_challenges c ON c.user_id=f.user_id
         WHERE c.digest=$1",
    )
    .bind(Sha256::digest(challenge.flow_token.as_bytes()).to_vec())
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(counts, (1, 1, false));
    let logged_in = browser.complete_mfa_login(&valid, BINDING).await.unwrap();
    assert_eq!(
        browser_assurance(&pool, &store, &logged_in).await.1,
        "recovery"
    );
    assert!(matches!(
        browser.complete_mfa_login(&valid, BINDING).await,
        Err(MfaError::FlowExpired)
    ));
    assert_eq!(
        browser
            .mfa_status(access(&logged_in))
            .await
            .unwrap()
            .backup_codes_remaining,
        3
    );
}

#[sqlx::test(migrations = false)]
async fn concurrent_recovery_logins_consume_one_code_and_issue_one_session(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let (setup, _, _) = enable(&pool, &browser, &original).await;
    let first = native_challenge(&pool, &store, PASSWORD).await;
    let second = native_challenge(&pool, &store, PASSWORD).await;
    let a = completion(&first, &setup.backup_codes[0]);
    let b = completion(&second, &setup.backup_codes[0]);
    let (a_result, b_result) =
        tokio::join!(store.complete_mfa_login(&a), store.complete_mfa_login(&b));
    assert_ne!(
        a_result.is_ok(),
        b_result.is_ok(),
        "exactly one recovery login must win"
    );
    let (winner, loser, loser_input) = match (a_result, b_result) {
        (Ok(winner), Err(loser)) => (winner, loser, &b),
        (Err(loser), Ok(winner)) => (winner, loser, &a),
        _ => unreachable!(),
    };
    assert!(matches!(
        loser,
        MfaError::CodeInvalid | MfaError::Identity(IdentityError::Conflict)
    ));
    // A NOWAIT loser may retry, but the already consumed recovery code cannot win.
    assert!(matches!(
        store.complete_mfa_login(loser_input).await,
        Err(MfaError::CodeInvalid)
    ));
    assert!(store.authorize(&winner.secret).await.is_ok());
    let consumed: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.mfa_backup_codes WHERE consumed_at IS NOT NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let sessions: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.session_authentication WHERE method='recovery'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!((consumed, sessions), (1, 1));
    assert_eq!(assurance(&pool, winner.id).await.1, "recovery");
}

#[sqlx::test(migrations = false)]
async fn totp_steps_are_consumed_by_enable_and_cannot_be_replayed_for_login(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let (setup, enabled, enable_code) = enable(&pool, &browser, &original).await;
    let challenge = native_challenge(&pool, &store, PASSWORD).await;
    let old_step: i64 =
        sqlx::query_scalar("SELECT last_used_step FROM core_identity.mfa_factors WHERE user_id=$1")
            .bind(user(&enabled))
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(matches!(
        store
            .complete_mfa_login(&completion(&challenge, &enable_code))
            .await,
        Err(MfaError::CodeInvalid)
    ));
    // The real verifier allows one future step. Use it without changing database state
    // or sleeping through a 30-second interval; the persisted replay guard still runs.
    let fresh_code = code_at(&setup.secret, epoch(&pool).await + 30);
    let logged_in = store
        .complete_mfa_login(&completion(&challenge, &fresh_code))
        .await
        .unwrap();
    assert_eq!(assurance(&pool, logged_in.id).await.1, "totp");
    let next_step: i64 =
        sqlx::query_scalar("SELECT last_used_step FROM core_identity.mfa_factors WHERE user_id=$1")
            .bind(user(&enabled))
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(next_step > old_step);
    let replay = native_challenge(&pool, &store, PASSWORD).await;
    assert!(matches!(
        store
            .complete_mfa_login(&completion(&replay, &fresh_code))
            .await,
        Err(MfaError::CodeInvalid)
    ));
}

#[sqlx::test(migrations = false)]
async fn native_refresh_and_password_change_preserve_factor_proof(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let (setup, _, _) = enable(&pool, &browser, &original).await;
    let challenge = native_challenge(&pool, &store, PASSWORD).await;
    let logged_in = store
        .complete_mfa_login(&completion(&challenge, &setup.backup_codes[0]))
        .await
        .unwrap();
    let original_proof = assurance(&pool, logged_in.id).await;
    assert_eq!(original_proof.0, 2);
    assert!(original_proof.4.is_some());
    let refreshed = store.refresh_session(&logged_in.secret).await.unwrap();
    assert_eq!(assurance(&pool, refreshed.id).await, original_proof);
    assert!(store.authorize(&logged_in.secret).await.is_err());
    let user = store.authorize(&refreshed.secret).await.unwrap().user_id;
    let before = generation(&pool, user).await;
    let changed = store
        .change_password(&refreshed.secret, PASSWORD, NEW_PASSWORD)
        .await
        .unwrap();
    assert_eq!(assurance(&pool, changed.id).await, original_proof);
    assert_eq!(generation(&pool, user).await, before + 1);
    assert!(store.authorize(&refreshed.secret).await.is_err());
    assert!(store.authorize(&changed.secret).await.is_ok());
    let challenge = native_challenge(&pool, &store, NEW_PASSWORD).await;
    assert!(store.authorize(&challenge.flow_token).await.is_err());
}

#[sqlx::test(migrations = false)]
async fn browser_rotation_preserves_proof_and_factor_mutations_revoke_old_authority(pool: PgPool) {
    let (store, browser, original) = fixture(&pool).await;
    let (setup, enabled, _) = enable(&pool, &browser, &original).await;
    let original_proof = browser_assurance(&pool, &store, &enabled).await;
    let original_bounds = bounds(&pool, sid(&enabled)).await;
    let refreshed = browser
        .refresh(&enabled.refresh_token, Some(sid(&enabled)))
        .await
        .unwrap();
    assert_eq!(
        browser_assurance(&pool, &store, &refreshed).await,
        original_proof
    );
    let changed = browser
        .change_password(
            access(&refreshed),
            PASSWORD,
            NEW_PASSWORD,
            None,
            Some(sid(&refreshed)),
        )
        .await
        .unwrap();
    assert_eq!(sid(&changed), sid(&enabled));
    assert_eq!(bounds(&pool, sid(&changed)).await, original_bounds);
    assert_eq!(
        browser_assurance(&pool, &store, &changed).await,
        original_proof
    );
    assert!(store.authorize(access(&refreshed)).await.is_err());
    assert!(
        browser
            .refresh(&refreshed.refresh_token, Some(sid(&refreshed)))
            .await
            .is_err()
    );
    let stale = native_challenge(&pool, &store, NEW_PASSWORD).await;
    let before = generation(&pool, user(&changed)).await;
    let regenerated = browser
        .mfa_regenerate(
            access(&changed),
            &code_at(&setup.secret, epoch(&pool).await + 30),
        )
        .await
        .unwrap();
    assert_eq!(sid(&regenerated), sid(&changed));
    assert_eq!(bounds(&pool, sid(&regenerated)).await, original_bounds);
    assert_eq!(generation(&pool, user(&regenerated)).await, before + 1);
    assert!(store.authorize(access(&changed)).await.is_err());
    assert!(
        browser
            .refresh(&changed.refresh_token, Some(sid(&changed)))
            .await
            .is_err()
    );
    assert!(matches!(
        store
            .complete_mfa_login(&completion(&stale, &setup.backup_codes[0]))
            .await,
        Err(MfaError::FlowExpired)
    ));
    let next = native_challenge(&pool, &store, NEW_PASSWORD).await;
    assert!(matches!(
        store
            .complete_mfa_login(&completion(&next, &setup.backup_codes[0]))
            .await,
        Err(MfaError::CodeInvalid)
    ));
    let codes = regenerated.data["backup_codes"].as_array().unwrap();
    assert_eq!(codes.len(), 4);
    let native = store
        .complete_mfa_login(&completion(&next, codes[0].as_str().unwrap()))
        .await
        .unwrap();
    let before = generation(&pool, user(&regenerated)).await;
    let disabled = browser
        .mfa_disable(access(&regenerated), codes[1].as_str().unwrap())
        .await
        .unwrap();
    assert_eq!(sid(&disabled), sid(&regenerated));
    assert_eq!(bounds(&pool, sid(&disabled)).await, original_bounds);
    assert_eq!(generation(&pool, user(&disabled)).await, before + 1);
    assert!(store.authorize(access(&regenerated)).await.is_err());
    assert!(store.authorize(&native.secret).await.is_err());
    assert!(
        browser
            .refresh(&regenerated.refresh_token, Some(sid(&regenerated)))
            .await
            .is_err()
    );
    let proof = browser_assurance(&pool, &store, &disabled).await;
    assert_eq!(proof.0, 1);
    assert_eq!(proof.1, "primary");
    assert!(proof.4.is_none());
    assert!(!browser.mfa_status(access(&disabled)).await.unwrap().enabled);
    assert!(matches!(
        browser
            .login_with_mfa(
                "alice",
                NEW_PASSWORD,
                &BrowserSessionMetadata::default(),
                BINDING
            )
            .await
            .unwrap(),
        BrowserLoginOutcome::Session(_)
    ));
}
