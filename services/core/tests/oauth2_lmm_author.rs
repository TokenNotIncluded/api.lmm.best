//! Author regressions: SQLx-owned PostgreSQL and newly registered synthetic users.
//! No production account, preconfigured credential, network provider, or skipped case.
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use lmm_core::{
    core_schema,
    identity::{
        BrowserAuth, BrowserBundle, BrowserLoginOutcome, BrowserRegistration,
        BrowserSessionMetadata, IdentityError, IdentityStore,
        grants::{
            AuthorizationRequest, ConsentPage, LmmIssuer, LmmProfile, ReadError, RevokeForm,
            TokenForm, TokenWire,
        },
    },
    runtime_database::{self, RequiredCapabilities},
};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::time::Duration;
use totp_rs::{Algorithm, Builder, Secret};

#[path = "common/runtime.rs"]
mod runtime;

const ISSUER: &str = "https://issuer.example.test";
const RESOURCE: &str = "https://issuer.example.test/api/oauth2";
const REDIRECT: &str = "http://127.0.0.1:38423/oauth/lmm/callback";
const PASSWORD: &str = "test passphrase";
const VERIFIER: &str = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ-._~";

struct Fixture {
    store: IdentityStore,
    browser: BrowserAuth,
    session: BrowserBundle,
    issuer: LmmIssuer,
    user: i64,
}

fn profile() -> LmmProfile {
    LmmProfile::new(ISSUER, vec!["default".into(), "private".into()]).unwrap()
}

async fn fixture(pool: &PgPool) -> Fixture {
    core_schema::init(pool).await.unwrap();
    let store = IdentityStore::from_pool(pool.clone())
        .with_registration_enabled(true)
        .with_mfa_keys("test".into(), vec![("test".into(), vec![47; 32])])
        .unwrap();
    let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "alice",
            password: PASSWORD,
            email: "",
        })
        .await
        .unwrap();
    let user: i64 = sqlx::query_scalar(
        "SELECT user_id FROM core_identity.user_profiles WHERE username='alice'",
    )
    .fetch_one(pool)
    .await
    .unwrap();
    // Installation-owner fixture operations precede browser authentication.
    sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES ($1,TRUE,'synthetic-oauth-test')")
        .bind(user).execute(pool).await.unwrap();
    sqlx::raw_sql(r#"
        INSERT INTO routing_price_versions(version,document) VALUES(1,'{"version":1}');
        INSERT INTO routing_config_versions(version,price_version,document) VALUES(1,1,'{"version":1}');
        INSERT INTO routing_groups(version,name,enabled) VALUES
            (1,'default',TRUE),(1,'private',TRUE),(1,'disabled',FALSE);
        UPDATE routing_price_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_config_versions SET sealed=TRUE WHERE version=1;
        UPDATE routing_active_release SET config_version=1;
    "#).execute(pool).await.unwrap();
    for group in ["default", "private", "disabled"] {
        sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES ($1,$2,'synthetic-oauth-test')")
            .bind(user).bind(group).execute(pool).await.unwrap();
    }
    let session = browser
        .login("alice", PASSWORD, &BrowserSessionMetadata::default())
        .await
        .unwrap();
    let issuer = LmmIssuer::open(store.clone(), profile(), 900, 86400)
        .await
        .unwrap();
    Fixture {
        store,
        browser,
        session,
        issuer,
        user,
    }
}

fn request() -> AuthorizationRequest {
    AuthorizationRequest {
        response_type: "code".into(),
        client_id: "lmm".into(),
        redirect_uri: REDIRECT.into(),
        scope: "catalog:read balance:read".into(),
        resource: RESOURCE.into(),
        code_challenge: URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER.as_bytes())),
        code_challenge_method: "S256".into(),
        state: "synthetic-state".into(),
    }
}

fn group_scope(group: &str) -> String {
    format!("group:{}", URL_SAFE_NO_PAD.encode(group.as_bytes()))
}

async fn consent_page(issuer: &LmmIssuer, cookie: &str) -> ConsentPage {
    let first = issuer.begin(request()).await.unwrap();
    assert!(first.account.is_none());
    let page = issuer
        .continue_authorization(&first.binding, &first.csrf, Some(cookie))
        .await
        .unwrap();
    assert_eq!(page.account.as_deref(), Some("alice"));
    assert_eq!(page.groups, ["default", "private"]);
    page
}

async fn authorization_code(issuer: &LmmIssuer, cookie: &str) -> String {
    let page = consent_page(issuer, cookie).await;
    let result = issuer
        .consent(&page.binding, &page.csrf, Some(cookie), true)
        .await
        .unwrap();
    let url = reqwest::Url::parse(&result.uri).unwrap();
    assert_eq!(url.origin().ascii_serialization(), "http://127.0.0.1:38423");
    assert_eq!(url.path(), "/oauth/lmm/callback");
    let pairs: Vec<_> = url.query_pairs().collect();
    assert_eq!(pairs.iter().filter(|(k, _)| k == "code").count(), 1);
    assert_eq!(
        pairs.iter().find(|(k, _)| k == "state").unwrap().1,
        "synthetic-state"
    );
    assert_eq!(pairs.iter().find(|(k, _)| k == "iss").unwrap().1, ISSUER);
    pairs
        .iter()
        .find(|(k, _)| k == "code")
        .unwrap()
        .1
        .to_string()
}

fn code_form(code: &str) -> TokenForm {
    TokenForm {
        grant_type: "authorization_code".into(),
        client_id: "lmm".into(),
        resource: RESOURCE.into(),
        code: Some(code.into()),
        redirect_uri: Some(REDIRECT.into()),
        code_verifier: Some(VERIFIER.into()),
        refresh_token: None,
        scope: None,
    }
}

fn refresh_form(refresh: &str, scope: Option<String>) -> TokenForm {
    TokenForm {
        grant_type: "refresh_token".into(),
        client_id: "lmm".into(),
        resource: RESOURCE.into(),
        code: None,
        redirect_uri: None,
        code_verifier: None,
        refresh_token: Some(refresh.into()),
        scope,
    }
}

async fn exchange(issuer: &LmmIssuer, form: TokenForm) -> Result<TokenWire, IdentityError> {
    let input = form.validate(issuer.profile())?;
    issuer.token(input).await
}

async fn token(issuer: &LmmIssuer, cookie: &str) -> TokenWire {
    exchange(issuer, code_form(&authorization_code(issuer, cookie).await))
        .await
        .unwrap()
}

fn assert_canonical(value: &str, prefix: &str) {
    let raw = value
        .strip_prefix(prefix)
        .expect("wrong credential purpose prefix");
    assert_eq!(raw.len(), 43);
    let bytes = URL_SAFE_NO_PAD
        .decode(raw)
        .expect("noncanonical base64url credential");
    assert_eq!(bytes.len(), 32);
    // A boolean assertion avoids printing credentials if this fails.
    assert!(URL_SAFE_NO_PAD.encode(bytes) == raw);
}

async fn assert_consumed(pool: &PgPool, page: &ConsentPage) {
    let consumed: bool = sqlx::query_scalar(
        "SELECT consumed_at IS NOT NULL FROM core_oauth.authorization_requests WHERE binding_digest=$1",
    ).bind(Sha256::digest(page.binding.as_bytes()).to_vec()).fetch_one(pool).await.unwrap();
    assert!(consumed);
}

async fn assert_denied(issuer: &LmmIssuer, token: &TokenWire) {
    assert!(matches!(
        issuer.balance(&token.access_token).await,
        Err(ReadError::Identity(
            IdentityError::Unauthorized | IdentityError::Forbidden
        ))
    ));
    assert!(matches!(
        exchange(issuer, refresh_form(&token.refresh_token, None)).await,
        Err(IdentityError::Unauthorized | IdentityError::Forbidden)
    ));
}

async fn runtime_issuer(pool: &PgPool) -> (runtime::RuntimeRole, LmmIssuer) {
    let role = std::sync::Arc::new(runtime::RuntimeRole::create(pool).await);
    let setup_role = role.clone();
    let owner = pool.clone();
    let setup = tokio::spawn(async move {
        let role = setup_role;
        let (session_user, current_user): (String, String) =
            sqlx::query_as("SELECT session_user::text,current_user::text")
                .fetch_one(&role.pool)
                .await
                .expect("runtime identity query failed");
        assert!(
            session_user == role.name && current_user == role.name,
            "runtime session does not use the isolated role"
        );
        assert!(
            reqwest::Url::parse(&role.url)
                .map(|url| url.username() == role.name)
                .unwrap_or(false),
            "runtime URL does not select the isolated role"
        );
        role.optional("web_identity").await;
        // These are issuer-state privileges; policy tables remain SELECT-only.
        sqlx::raw_sql(&format!(
            r#"
        GRANT USAGE ON SCHEMA core_oauth TO {0};
        GRANT SELECT ON core_oauth.contract TO {0};
        GRANT SELECT,INSERT ON core_oauth.authorization_requests,core_oauth.authorization_codes,
            core_oauth.families,core_oauth.access_tokens,core_oauth.refresh_tokens TO {0};
        GRANT UPDATE(consumed_at) ON core_oauth.authorization_requests,
            core_oauth.authorization_codes,core_oauth.refresh_tokens TO {0};
        GRANT UPDATE(scopes,revoked_at) ON core_oauth.families TO {0};
        GRANT EXECUTE ON FUNCTION core_oauth.readonly_scope_set(text[]) TO {0};
        GRANT EXECUTE ON FUNCTION core_oauth.client_scope_set(text,text[],boolean) TO {0};
    "#,
            role.name
        ))
        .execute(&owner)
        .await
        .unwrap();
        runtime_database::check(
            &role.pool,
            RequiredCapabilities {
                web_identity: true,
                oauth2: true,
                ..RequiredCapabilities::default()
            },
        )
        .await
        .unwrap();
        LmmIssuer::open(
            IdentityStore::from_pool(role.pool.clone()),
            profile(),
            900,
            86400,
        )
        .await
        .unwrap()
    })
    .await;
    let role = match std::sync::Arc::try_unwrap(role) {
        Ok(role) => role,
        Err(_) => panic!("runtime setup retained an unexpected role reference"),
    };
    match setup {
        Ok(issuer) => (role, issuer),
        Err(error) => {
            role.cleanup().await;
            std::panic::resume_unwind(error.into_panic());
        }
    }
}

#[sqlx::test(migrations = false)]
async fn code_exchange_enforces_canonical_pkce_and_exact_client_resource_redirect(pool: PgPool) {
    let f = fixture(&pool).await;
    for wrong in ["scope", "pkce", "redirect"] {
        let mut input = request();
        match wrong {
            "scope" => input.scope = "catalog:read balance:read models:invoke".into(),
            "pkce" => input.code_challenge_method = "plain".into(),
            _ => input.redirect_uri = "http://localhost:38423/oauth/lmm/callback".into(),
        }
        let expected = if wrong == "scope" {
            IdentityError::Forbidden
        } else {
            IdentityError::Invalid
        };
        assert!(
            matches!(f.issuer.begin(input).await, Err(error) if error == expected),
            "wrong refusal for authorization {wrong}"
        );
    }
    let code = authorization_code(&f.issuer, &f.session.refresh_token).await;
    assert_canonical(&code, "lmm_oc_");
    for wrong in ["client", "resource", "redirect", "verifier", "noncanonical"] {
        let mut form = code_form(&code);
        match wrong {
            "client" => form.client_id = "lmm-pi".into(),
            "resource" => form.resource = "https://other.example.test/api/oauth2".into(),
            "redirect" => {
                form.redirect_uri = Some("http://127.0.0.1:38424/oauth/lmm/callback".into())
            }
            "verifier" => form.code_verifier = Some("x".repeat(64)),
            _ => form.code = Some(format!("lmm_oc_{}B", "A".repeat(42))),
        }
        assert!(
            matches!(
                exchange(&f.issuer, form).await,
                Err(IdentityError::Unauthorized)
            ),
            "accepted mismatched {wrong}"
        );
        let consumed: bool = sqlx::query_scalar(
            "SELECT consumed_at IS NOT NULL FROM core_oauth.authorization_codes WHERE digest=$1",
        )
        .bind(Sha256::digest(code.as_bytes()).to_vec())
        .fetch_one(&pool)
        .await
        .unwrap();
        assert!(!consumed, "rejected input consumed the valid code");
    }
    let issued = exchange(&f.issuer, code_form(&code)).await.unwrap();
    assert_canonical(&issued.access_token, "lmm_at_");
    assert_canonical(&issued.refresh_token, "lmm_rt_");
    assert_eq!(issued.token_type, "Bearer");
    assert!((1..=900).contains(&issued.expires_in));
    assert!(issued.scope.split(' ').any(|s| s == group_scope("default")));
    assert!(issued.scope.split(' ').any(|s| s == group_scope("private")));
    assert!(!issued.scope.contains("models:invoke"));
    assert!(matches!(
        f.store.authorize(&issued.access_token).await,
        Err(IdentityError::Unauthorized)
    ));
    assert!(matches!(
        exchange(&f.issuer, code_form(&code)).await,
        Err(IdentityError::Unauthorized)
    ));
    let balance =
        serde_json::to_value(f.issuer.balance(&issued.access_token).await.unwrap()).unwrap();
    assert_eq!(balance["schema_version"], 2);
    assert_eq!(balance["currency"], "USD");
    assert_eq!(balance["quota"], 0);
    assert_eq!(balance["public_credit_balance"], "0");
}

#[sqlx::test(migrations = false)]
async fn refresh_shrinks_authority_and_lineage_replay_revokes_the_family(pool: PgPool) {
    let f = fixture(&pool).await;
    let first = token(&f.issuer, &f.session.refresh_token).await;
    let narrowed = format!("balance:read {}", group_scope("default"));
    let next = exchange(
        &f.issuer,
        refresh_form(&first.refresh_token, Some(narrowed.clone())),
    )
    .await
    .unwrap();
    assert!(next.scope == narrowed);
    assert!(f.issuer.balance(&first.access_token).await.is_ok());
    assert!(matches!(
        f.issuer.catalog(&first.access_token).await,
        Err(ReadError::Identity(IdentityError::Forbidden))
    ));
    let expanded = format!(
        "catalog:read balance:read {} {}",
        group_scope("default"),
        group_scope("private")
    );
    assert!(matches!(
        exchange(&f.issuer, refresh_form(&next.refresh_token, Some(expanded))).await,
        Err(IdentityError::Forbidden)
    ));
    assert!(f.issuer.balance(&next.access_token).await.is_ok());
    assert!(matches!(
        exchange(&f.issuer, refresh_form(&first.refresh_token, None)).await,
        Err(IdentityError::Unauthorized)
    ));
    assert_denied(&f.issuer, &next).await;
    assert_denied(&f.issuer, &first).await;
}

#[sqlx::test(migrations = false)]
async fn revocation_is_idempotent_bound_to_issuer_and_accepts_access_or_refresh(pool: PgPool) {
    let f = fixture(&pool).await;
    let first = token(&f.issuer, &f.session.refresh_token).await;
    let second = token(&f.issuer, &f.session.refresh_token).await;
    let other = LmmIssuer::open(
        f.store.clone(),
        LmmProfile::new("https://other.example.test", vec!["default".into()]).unwrap(),
        900,
        86400,
    )
    .await
    .unwrap();
    assert!(matches!(
        other.revoke(&first.access_token).await,
        Err(IdentityError::Unauthorized)
    ));
    assert!(f.issuer.balance(&first.access_token).await.is_ok());
    assert!(matches!(
        RevokeForm {
            client_id: "other".into(),
            token: first.access_token.clone(),
            token_type_hint: Some("access_token".into())
        }
        .validate(),
        Err(IdentityError::Invalid)
    ));
    f.issuer.revoke(&first.access_token).await.unwrap();
    f.issuer.revoke(&first.access_token).await.unwrap();
    assert_denied(&f.issuer, &first).await;
    assert!(f.issuer.balance(&second.access_token).await.is_ok());
    f.issuer.revoke(&second.refresh_token).await.unwrap();
    assert_denied(&f.issuer, &second).await;
    f.issuer
        .revoke(&format!("lmm_rt_{}", URL_SAFE_NO_PAD.encode([0u8; 32])))
        .await
        .unwrap();
}

#[sqlx::test(migrations = false)]
async fn consent_consumes_csrf_and_refuses_missing_cookie_or_incomplete_mfa(pool: PgPool) {
    let f = fixture(&pool).await;
    let before_enable = consent_page(&f.issuer, &f.session.refresh_token).await;
    let setup = f
        .browser
        .mfa_setup(f.session.data["access_token"].as_str().unwrap())
        .await
        .unwrap();
    let epoch: i64 =
        sqlx::query_scalar("SELECT floor(extract(epoch FROM clock_timestamp()))::bigint")
            .fetch_one(&pool)
            .await
            .unwrap();
    let secret = Secret::try_from_base32(&setup.secret).unwrap();
    let code = Builder::new()
        .with_algorithm(Algorithm::SHA1)
        .with_digits(6)
        .with_step_duration(30)
        .with_skew(1)
        .with_secret(secret.as_bytes())
        .build()
        .unwrap()
        .generate(u64::try_from(epoch).unwrap())
        .to_string();
    let enabled = f
        .browser
        .mfa_enable(f.session.data["access_token"].as_str().unwrap(), &code)
        .await
        .unwrap();
    assert!(matches!(
        f.issuer
            .consent(
                &before_enable.binding,
                &before_enable.csrf,
                Some(&f.session.refresh_token),
                true
            )
            .await,
        Err(IdentityError::Unauthorized)
    ));
    assert_consumed(&pool, &before_enable).await;
    let page = consent_page(&f.issuer, &enabled.refresh_token).await;
    assert!(matches!(
        f.issuer
            .consent(&page.binding, &page.csrf, None, true)
            .await,
        Err(IdentityError::Unauthorized)
    ));
    assert_consumed(&pool, &page).await;
    assert!(matches!(
        f.issuer
            .consent(
                &page.binding,
                &page.csrf,
                Some(&enabled.refresh_token),
                true
            )
            .await,
        Err(IdentityError::Unauthorized)
    ));
    let challenge = match f
        .browser
        .login_with_mfa(
            "alice",
            PASSWORD,
            &BrowserSessionMetadata::default(),
            &"a".repeat(64),
        )
        .await
        .unwrap()
    {
        BrowserLoginOutcome::Challenge(challenge) => challenge,
        BrowserLoginOutcome::Session(_) => panic!("required factor was bypassed"),
    };
    let first = f.issuer.begin(request()).await.unwrap();
    let anonymous = f
        .issuer
        .continue_authorization(&first.binding, &first.csrf, Some(&challenge.flow_token))
        .await
        .unwrap();
    assert!(anonymous.account.is_none());
    assert!(matches!(
        f.issuer
            .consent(
                &anonymous.binding,
                &anonymous.csrf,
                Some(&challenge.flow_token),
                true
            )
            .await,
        Err(IdentityError::Unauthorized)
    ));
    assert_consumed(&pool, &anonymous).await;
    let families: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.families")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(families, 0);
    // Only the completed browser authentication can issue the final grant.
    assert_canonical(
        &authorization_code(&f.issuer, &enabled.refresh_token).await,
        "lmm_oc_",
    );
}

#[sqlx::test(migrations = false)]
async fn committed_group_revocation_denies_pending_code_and_existing_tokens(pool: PgPool) {
    let f = fixture(&pool).await;
    let issued = token(&f.issuer, &f.session.refresh_token).await;
    let code = authorization_code(&f.issuer, &f.session.refresh_token).await;
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE core_identity.user_routing_groups SET enabled=FALSE WHERE user_id=$1 AND group_name='private'")
        .bind(f.user).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    assert!(matches!(
        exchange(&f.issuer, code_form(&code)).await,
        Err(IdentityError::Unauthorized)
    ));
    assert_denied(&f.issuer, &issued).await;
}

#[sqlx::test(migrations = false)]
async fn committed_developer_revocation_denies_pending_code_and_existing_tokens(pool: PgPool) {
    let f = fixture(&pool).await;
    let issued = token(&f.issuer, &f.session.refresh_token).await;
    let code = authorization_code(&f.issuer, &f.session.refresh_token).await;
    let mut tx = pool.begin().await.unwrap();
    sqlx::query("UPDATE core_identity.developer_access SET granted=FALSE WHERE user_id=$1")
        .bind(f.user)
        .execute(&mut *tx)
        .await
        .unwrap();
    tx.commit().await.unwrap();
    assert!(matches!(
        exchange(&f.issuer, code_form(&code)).await,
        Err(IdentityError::Unauthorized)
    ));
    assert_denied(&f.issuer, &issued).await;
}

#[sqlx::test(migrations = false)]
async fn normal_runtime_role_uses_select_only_policy_acl_for_full_grant_lifecycle(pool: PgPool) {
    let f = fixture(&pool).await;
    let (role, issuer) = runtime_issuer(&pool).await;
    // Clean the role even when the assertions in this author test panic.
    let runtime_pool = role.pool.clone();
    let checks = tokio::spawn(async move {
        for (table, column) in [
            ("core_identity.developer_access", "granted"),
            ("core_identity.user_routing_groups", "enabled"),
            ("public.routing_active_release", "config_version"),
            ("public.routing_groups", "enabled"),
            ("public.routing_routes", "enabled"),
        ] {
            let acl: (bool, bool, bool) = sqlx::query_as(
                "SELECT has_table_privilege($1,'SELECT'),has_table_privilege($1,'INSERT,UPDATE,DELETE'),has_any_column_privilege($1,'UPDATE')",
            ).bind(table).fetch_one(&runtime_pool).await.unwrap();
            assert_eq!(acl, (true, false, false), "policy ACL expanded for {table}");
            let result = sqlx::query(&format!("UPDATE {table} SET {column}={column} WHERE FALSE"))
                .execute(&runtime_pool).await;
            assert!(matches!(result, Err(sqlx::Error::Database(ref error))
                if error.code().as_deref() == Some("42501")), "policy UPDATE unexpectedly allowed");
        }
        let issued = token(&issuer, &f.session.refresh_token).await;
        let balance = serde_json::to_value(issuer.balance(&issued.access_token).await.unwrap()).unwrap();
        assert_eq!(balance["quota"], 0);
        let next = exchange(&issuer, refresh_form(&issued.refresh_token, None)).await.unwrap();
        issuer.revoke(&next.access_token).await.unwrap();
        assert_denied(&issuer, &next).await;
    }).await;
    role.cleanup().await;
    checks.unwrap();
}

async fn blocked_issuer(
    pool: &PgPool,
    role: &str,
    barrier: i32,
    deadline: tokio::time::Instant,
) -> Option<i32> {
    tokio::time::timeout_at(deadline, async {
        loop {
            let pid: Option<i32> = sqlx::query_scalar(
                "SELECT a.pid FROM pg_stat_activity a JOIN pg_locks l ON l.pid=a.pid
                 WHERE a.datname=current_database() AND a.usename=$1
                   AND l.locktype='relation' AND l.relation='core_oauth.access_tokens'::regclass
                   AND l.mode='RowExclusiveLock' AND NOT l.granted
                   AND $2=ANY(pg_blocking_pids(a.pid)) LIMIT 1",
            )
            .bind(role)
            .bind(barrier)
            .fetch_optional(pool)
            .await
            .unwrap();
            if let Some(pid) = pid {
                break pid;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .ok()
}

async fn revoker_waits_for_user_authority(
    pool: &PgPool,
    revoker: i32,
    issuer: i32,
    deadline: tokio::time::Instant,
) -> bool {
    tokio::time::timeout_at(deadline, async {
        loop {
            let waits: bool = sqlx::query_scalar(
                "SELECT $2=ANY(pg_blocking_pids($1)) AND EXISTS (
                    SELECT 1 FROM pg_locks w JOIN pg_locks h ON
                        h.locktype='transactionid' AND h.transactionid=w.transactionid
                    WHERE w.pid=$1 AND w.locktype='transactionid' AND w.mode='ShareLock'
                        AND NOT w.granted AND h.pid=$2 AND h.mode='ExclusiveLock' AND h.granted)",
            )
            .bind(revoker)
            .bind(issuer)
            .fetch_one(pool)
            .await
            .unwrap();
            if waits {
                break;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .is_ok()
}

async fn issue_then_revoke(pool: &PgPool, f: Fixture, issuer: LmmIssuer, role: &str, group: bool) {
    let code = authorization_code(&issuer, &f.session.refresh_token).await;
    let version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
            .bind(f.user)
            .fetch_one(pool)
            .await
            .unwrap();
    let mut barrier = pool.begin().await.unwrap();
    let barrier_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *barrier)
        .await
        .unwrap();
    sqlx::query("LOCK TABLE core_oauth.access_tokens IN SHARE MODE")
        .execute(&mut *barrier)
        .await
        .unwrap();
    let token_issuer = issuer.clone();
    // Both observations share one budget below the issuer's 1s lock timeout.
    let deadline = tokio::time::Instant::now() + Duration::from_millis(600);
    let issuing = tokio::spawn(async move { exchange(&token_issuer, code_form(&code)).await });
    let issuer_pid = blocked_issuer(pool, role, barrier_pid, deadline).await;
    if issuer_pid.is_none() {
        barrier.rollback().await.unwrap();
        let _ = issuing.await;
        panic!("issuer did not reach the actual access-token insert barrier");
    }
    let mut revocation = pool.begin().await.unwrap();
    let revoker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *revocation)
        .await
        .unwrap();
    let revoke_user = f.user;
    let revoking = tokio::spawn(async move {
        let sql = if group {
            "UPDATE core_identity.user_routing_groups SET enabled=FALSE WHERE user_id=$1 AND group_name='private'"
        } else {
            "UPDATE core_identity.developer_access SET granted=FALSE WHERE user_id=$1"
        };
        let rows = sqlx::query(sql)
            .bind(revoke_user)
            .execute(&mut *revocation)
            .await
            .unwrap()
            .rows_affected();
        revocation.commit().await.unwrap();
        rows
    });
    let waits =
        revoker_waits_for_user_authority(pool, revoker_pid, issuer_pid.unwrap(), deadline).await;
    barrier.commit().await.unwrap();
    let issued = issuing.await.unwrap();
    let revoked = revoking.await.unwrap();
    assert!(
        waits,
        "revocation did not wait on the issuer's user authority transaction"
    );
    let issued = issued.expect("issuance holding authority should commit before revocation");
    assert_eq!(revoked, 1);
    let next_version: i64 =
        sqlx::query_scalar("SELECT auth_version FROM core_identity.users WHERE id=$1")
            .bind(f.user)
            .fetch_one(pool)
            .await
            .unwrap();
    assert_eq!(next_version, version + 1);
    assert_denied(&issuer, &issued).await;
}

#[sqlx::test(migrations = false)]
async fn developer_revocation_waits_for_runtime_issuance_then_denies_the_new_family(pool: PgPool) {
    let f = fixture(&pool).await;
    let (role, issuer) = runtime_issuer(&pool).await;
    let role_name = role.name.clone();
    let owner = pool.clone();
    let checks =
        tokio::spawn(async move { issue_then_revoke(&owner, f, issuer, &role_name, false).await })
            .await;
    role.cleanup().await;
    checks.unwrap();
}

#[sqlx::test(migrations = false)]
async fn group_revocation_waits_for_runtime_issuance_then_denies_the_new_family(pool: PgPool) {
    let f = fixture(&pool).await;
    let (role, issuer) = runtime_issuer(&pool).await;
    let role_name = role.name.clone();
    let owner = pool.clone();
    let checks =
        tokio::spawn(async move { issue_then_revoke(&owner, f, issuer, &role_name, true).await })
            .await;
    role.cleanup().await;
    checks.unwrap();
}

#[sqlx::test(migrations = false)]
async fn continue_cannot_renew_a_request_that_expires_while_browser_family_is_locked(pool: PgPool) {
    let f = fixture(&pool).await;
    let count_before: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_oauth.authorization_requests")
            .fetch_one(&pool)
            .await
            .unwrap();
    let mut barrier = pool.begin().await.unwrap();
    let barrier_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *barrier)
        .await
        .unwrap();
    sqlx::query("SELECT sid FROM core_identity.web_sessions WHERE sid=$1 FOR UPDATE")
        .bind(f.session.data["session"]["sid"].as_str().unwrap())
        .fetch_one(&mut *barrier)
        .await
        .unwrap();
    let mut observer = pool.acquire().await.unwrap();
    // Warm the continuation's connection before creating its 400ms lifetime.
    let ready_connection = pool.acquire().await.unwrap();
    drop(ready_connection);
    let binding = URL_SAFE_NO_PAD.encode(rand::random::<[u8; 32]>());
    let csrf = URL_SAFE_NO_PAD.encode(rand::random::<[u8; 32]>());
    let binding_digest = Sha256::digest(binding.as_bytes()).to_vec();
    let input = request();
    // The expiry is part of the original INSERT. Immutable rows are never
    // edited to simulate expiry, and server time decides both boundaries.
    let deadline = tokio::time::Instant::now() + Duration::from_millis(800);
    sqlx::query(
        "WITH born AS (SELECT statement_timestamp()-interval '299.6 seconds' AS at)
         INSERT INTO core_oauth.authorization_requests
           (binding_digest,csrf_digest,client_id,issuer,resource,redirect_uri,state,
            pkce_challenge,requested_scopes,granted_scopes,created_at,expires_at)
         SELECT $1,$2,$3,$4,$5,$6,$7,$8,
           ARRAY['catalog:read','balance:read']::text[],ARRAY[]::text[],at,at+interval '300 seconds'
         FROM born",
    )
    .bind(&binding_digest)
    .bind(Sha256::digest(csrf.as_bytes()).to_vec())
    .bind(input.client_id)
    .bind(ISSUER)
    .bind(input.resource)
    .bind(input.redirect_uri)
    .bind(input.state)
    .bind(input.code_challenge)
    .execute(&mut *observer)
    .await
    .unwrap();
    let continuing = tokio::spawn(async move {
        f.issuer
            .continue_authorization(&binding, &csrf, Some(&f.session.refresh_token))
            .await
    });
    let blocked_while_live = tokio::time::timeout_at(deadline, async {
        loop {
            let live: Option<bool> = sqlx::query_scalar(
                "SELECT r.expires_at>clock_timestamp()
                 FROM core_oauth.authorization_requests r
                 WHERE r.binding_digest=$1 AND EXISTS (
                   SELECT 1 FROM pg_stat_activity a
                   JOIN pg_locks w ON w.pid=a.pid
                   JOIN pg_locks h ON h.locktype='transactionid' AND h.transactionid=w.transactionid
                   WHERE a.datname=current_database()
                     AND a.query LIKE 'SELECT sid FROM core_identity.web_sessions%'
                     AND $2=ANY(pg_blocking_pids(a.pid))
                     AND w.locktype='transactionid' AND w.mode='ShareLock' AND NOT w.granted
                     AND h.pid=$2 AND h.mode='ExclusiveLock' AND h.granted)",
            )
            .bind(&binding_digest)
            .bind(barrier_pid)
            .fetch_optional(&mut *observer)
            .await
            .unwrap();
            if let Some(live) = live {
                break live;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .ok();
    let crossed_expiry = if blocked_while_live == Some(true) {
        tokio::time::timeout_at(deadline, async {
            loop {
                let expired: bool = sqlx::query_scalar(
                    "SELECT expires_at<=clock_timestamp() FROM core_oauth.authorization_requests WHERE binding_digest=$1",
                ).bind(&binding_digest).fetch_one(&mut *observer).await.unwrap();
                if expired { break; }
                tokio::time::sleep(Duration::from_millis(5)).await;
            }
        }).await.is_ok()
    } else {
        false
    };
    // Always release the owner barrier before evaluating the timing evidence.
    barrier.commit().await.unwrap();
    let result = continuing.await.unwrap();
    assert_eq!(
        blocked_while_live,
        Some(true),
        "continuation did not block before expiry"
    );
    assert!(
        crossed_expiry,
        "server clock did not cross expiry within the lock budget"
    );
    assert!(matches!(result, Err(IdentityError::Unauthorized)));
    let consumed: bool = sqlx::query_scalar(
        "SELECT consumed_at IS NOT NULL FROM core_oauth.authorization_requests WHERE binding_digest=$1",
    ).bind(binding_digest).fetch_one(&mut *observer).await.unwrap();
    let count_after: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_oauth.authorization_requests")
            .fetch_one(&mut *observer)
            .await
            .unwrap();
    assert!(
        consumed,
        "expired continuation rolled back its consumed CSRF"
    );
    assert_eq!(
        count_after,
        count_before + 1,
        "expired continuation created a replacement request"
    );
}

#[sqlx::test(migrations = false)]
async fn sql_scope_format_accepts_unicode_and_rejects_controls_and_noncanonical_groups(
    pool: PgPool,
) {
    core_schema::init(&pool).await.unwrap();
    let scoped = |group: String| vec!["catalog:read".to_owned(), group];
    let mut cases: Vec<(String, Vec<String>, bool)> = Vec::new();
    for (name, group) in [
        ("normal", "default".to_owned()),
        ("chinese", "中文模型".to_owned()),
        ("emoji", "模型🚀🧪".to_owned()),
        ("maximum_utf8_bytes", "🚀".repeat(16)),
    ] {
        cases.push((name.into(), scoped(group_scope(&group)), true));
    }
    // Encode the actual Unicode scalar values, not textual backslash escapes.
    // This exhausts Rust's C0/DEL/C1 control range, including LF and U+0085.
    // U+0000 reaches PostgreSQL as canonical ASCII base64url; UTF8 conversion
    // must reject the decoded NUL instead of relying on a text parameter error.
    for point in (0..=0x1f).chain(0x7f..=0x9f) {
        let group = char::from_u32(point).unwrap().to_string();
        cases.push((
            format!("control U+{point:04X}"),
            scoped(group_scope(&group)),
            false,
        ));
    }
    for (name, scopes) in [
        ("embedded_C1", scoped(group_scope("before\u{0085}after"))),
        (
            "invalid_utf8",
            scoped(format!("group:{}", URL_SAFE_NO_PAD.encode([0xffu8]))),
        ),
        ("too_many_utf8_bytes", scoped(group_scope(&"🚀".repeat(17)))),
        (
            "padded_base64",
            scoped(format!("{}=", group_scope("default"))),
        ),
        ("noncanonical_tail_bits", scoped("group:ZGVmYXVsdB".into())),
        (
            "duplicate_group",
            vec![
                "catalog:read".into(),
                group_scope("default"),
                group_scope("default"),
            ],
        ),
        (
            "duplicate_read",
            vec![
                "catalog:read".into(),
                "catalog:read".into(),
                group_scope("default"),
            ],
        ),
        (
            "missing_group",
            vec!["catalog:read".into(), "balance:read".into()],
        ),
        (
            "missing_read",
            vec![group_scope("default"), group_scope("private")],
        ),
        (
            "unsupported_read",
            vec!["models:invoke".into(), group_scope("default")],
        ),
    ] {
        cases.push((name.into(), scopes, false));
    }
    for (name, scopes, expected) in cases {
        let actual: bool = sqlx::query_scalar("SELECT core_oauth.readonly_scope_set($1)")
            .bind(scopes)
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(actual, expected, "scope format case {name}");
    }
}
