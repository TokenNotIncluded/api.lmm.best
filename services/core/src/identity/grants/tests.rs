//! Fixture-seeded OAuth state, real native IdentityStore and private PostgreSQL.
//! These tests do NOT stand in for consent, CSRF or capability/HTTP acceptance.
use super::*;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};

const ISSUER: &str = "https://api.lmm.best";
const RESOURCE: &str = "https://api.lmm.best/api/oauth2";
const REDIRECT: &str = "http://127.0.0.1:38123/oauth/lmm/callback";
const VERIFIER: &str = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGH";
fn profile() -> GrantProfile {
    GrantProfile::new(ISSUER, IssuerMode::Production).unwrap()
}
pub(crate) async fn fresh(pool: &PgPool) -> (IdentityStore, GrantStore, IssuedCredential) {
    let identity = IdentityStore::from_pool(pool.clone());
    identity.init_database().await.unwrap();
    sqlx::raw_sql(crate::abuse::SCHEMA)
        .execute(pool)
        .await
        .unwrap();
    let session = identity.bootstrap_user(1, 1).await.unwrap();
    let mut tx = pool.begin().await.unwrap();
    sqlx::raw_sql(SCHEMA).execute(&mut *tx).await.unwrap();
    let fp = catalog(&mut tx).await.unwrap();
    sqlx::query("INSERT INTO core_oauth.contract(version,ddl_fingerprint,catalog_fingerprint) VALUES (1,$1,$2)").bind(Sha256::digest(SCHEMA.as_bytes()).to_vec()).bind(fp).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    let store = GrantStore::open(identity.clone(), profile()).await.unwrap();
    (identity, store, session)
}
async fn seed(store: &GrantStore, native: &str, selected: &[&str]) -> String {
    let mut tx = transaction(&store.identity.pool).await.unwrap();
    // No fixed-success native authorizer; lock real current Session/user/owner.
    let actor = session(&mut tx, native).await.unwrap();
    assert_eq!(actor.owner.kind, AccountKind::Personal);
    let id = new_secret("lmm_gr_").unwrap();
    let code = new_secret("lmm_oc_").unwrap();
    let selected: Vec<String> = selected.iter().map(|s| (*s).into()).collect();
    scopes(&selected).unwrap();
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER));
    pkce(&challenge, Some("S256"), Some(VERIFIER)).unwrap();
    sqlx::query("INSERT INTO core_oauth.families(id,user_id,owner_account_id,user_version,issuer,resource,client_id,scopes,expires_at) VALUES ($1,$2,$3,$4,$5,$6,'lmm-pi',$7,clock_timestamp()+interval '1 day')")
        .bind(&id).bind(actor.user_id).bind(actor.owner_account_id).bind(actor.user_version).bind(ISSUER).bind(RESOURCE).bind(selected).execute(&mut *tx).await.unwrap();
    sqlx::query("INSERT INTO core_oauth.codes(digest,family_id,consent_version,redirect_uri,state,pkce_challenge,expires_at) VALUES ($1,$2,1,$3,'fixture-state',$4,statement_timestamp()+interval '5 minutes')")
        .bind(digest(&code).unwrap()).bind(id).bind(REDIRECT).bind(challenge).execute(&mut *tx).await.unwrap();
    tx.commit().await.unwrap();
    code
}
fn exchange(code: &str) -> CodeExchange<'_> {
    CodeExchange {
        code,
        issuer: ISSUER,
        resource: RESOURCE,
        client_id: "lmm-pi",
        redirect_uri: REDIRECT,
        verifier: Some(VERIFIER),
    }
}
fn refresh(token: &str) -> RefreshExchange<'_> {
    RefreshExchange {
        refresh: token,
        issuer: ISSUER,
        resource: RESOURCE,
        client_id: "lmm-pi",
        scopes: None,
    }
}
pub(crate) async fn tokens(store: &GrantStore, native: &str) -> TokenBundle {
    let code = seed(
        store,
        native,
        &[
            "catalog:read",
            "balance:read",
            "usage:read",
            "models:invoke",
            "remote:control",
        ],
    )
    .await;
    store.exchange(exchange(&code), 3600).await.unwrap()
}
#[test]
fn profile_pkce_and_bounded_inputs() {
    for issuer in [
        "http://api.lmm.best",
        "https://api.lmm.best/",
        "https://api.lmm.best/path",
        "https://u@api.lmm.best",
        "https://api.lmm.best?x=1",
        "https://api.lmm.best#f",
    ] {
        assert!(GrantProfile::new(issuer, IssuerMode::Production).is_err());
    }
    assert!(GrantProfile::new("http://127.0.0.1:3000", IssuerMode::LoopbackDevelopment).is_ok());
    assert!(GrantProfile::new("http://localhost:3000", IssuerMode::LoopbackDevelopment).is_err());
    for uri in [
        "http://localhost:38123/oauth/lmm/callback",
        "http://127.0.0.1:0/oauth/lmm/callback",
        "http://127.0.0.1/oauth/lmm/callback",
        "http://127.0.0.1:38123/oauth/lmm/callback?x=1",
        "https://127.0.0.1:38123/oauth/lmm/callback",
        "http://127.0.0.1:38123/other",
    ] {
        assert!(redirect(uri).is_err());
    }
    let challenge = URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER));
    assert!(pkce(&challenge, None, Some(VERIFIER)).is_err());
    assert!(pkce(&challenge, Some("plain"), Some(VERIFIER)).is_err());
    assert!(pkce(&challenge, Some("S256"), None).is_err());
    assert!(
        Pkce::required()
            .challenge(Some("S256".into()), None)
            .is_err()
    );
    assert!(pkce(&challenge, Some("S256"), Some(&"a".repeat(129))).is_err());
    assert!(scopes(&[]).is_err());
    assert!(scopes(&["remote:control".into(), "remote:control".into()]).is_err());
    assert!(scopes(&["group:host".into()]).is_err());
    assert!(token_digest(&"a".repeat(10000), "lmm_at_").is_err());
}
#[sqlx::test(migrations = false)]
async fn grants_binding_code_consumption_and_native_rejection(pool: PgPool) {
    let (identity, store, native) = fresh(&pool).await;
    let code = seed(&store, &native.secret, &["remote:control"]).await;
    for field in 0..6 {
        let mut input = exchange(&code);
        match field {
            0 => input.issuer = "https://other.invalid",
            1 => input.resource = "https://api.lmm.best/other",
            2 => input.client_id = "other",
            3 => input.redirect_uri = "http://127.0.0.1:38124/oauth/lmm/callback",
            4 => input.verifier = None,
            _ => input.verifier = Some("AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA"),
        }
        assert!(store.exchange(input, 3600).await.is_err());
    }
    for ttl in [0, -1, 86401, i64::MAX] {
        assert!(store.exchange(exchange(&code), ttl).await.is_err());
    }
    let (a, b) = tokio::join!(
        store.exchange(exchange(&code), 3600),
        store.exchange(exchange(&code), 3600)
    );
    assert_ne!(a.is_ok(), b.is_ok());
    let bundle = a.or(b).unwrap();
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 1);
    assert!(identity.authorize(&bundle.access_token).await.is_err());
    assert!(identity.create_team(&bundle.access_token).await.is_err());
    assert!(
        identity
            .authorize_model_request(&bundle.access_token, "fixture", None, "default")
            .await
            .is_err()
    );
    assert!(
        identity
            .authorize_remote_native(&bundle.access_token)
            .await
            .is_err()
    );
    assert!(store.authorize_remote(&native.secret).await.is_err());
    let key = identity
        .issue_key(
            &native.secret,
            Account {
                kind: AccountKind::Personal,
                id: 1,
            },
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    assert!(identity.authorize(&key.secret).await.is_ok());
    assert!(identity.authorize_remote_native(&key.secret).await.is_err());
    for bad in [
        &key.secret,
        "lmm_host_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "group:host",
    ] {
        assert!(store.authorize_remote(bad).await.is_err());
    }
    let current = identity
        .authorize_remote_native(&native.secret)
        .await
        .unwrap();
    assert!(matches!(
        current.source,
        RemoteSource::NativePersonal { .. }
    ));
    let authority = store.authorize_remote(&bundle.access_token).await.unwrap();
    assert!(matches!(authority.source, RemoteSource::OAuthGrant { .. }));
    let rows: Vec<String> =
        sqlx::query_scalar("SELECT row_to_json(t)::text FROM core_oauth.access_tokens t")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert!(rows.iter().all(|s| !s.contains(&bundle.access_token)));
    let rows: Vec<String> =
        sqlx::query_scalar("SELECT row_to_json(t)::text FROM core_oauth.refresh_tokens t")
            .fetch_all(&pool)
            .await
            .unwrap();
    assert!(rows.iter().all(|s| !s.contains(&bundle.refresh_token)));
}
#[sqlx::test(migrations = false)]
async fn grants_rotation_shrink_replay_and_reopen(pool: PgPool) {
    let (identity, store, native) = fresh(&pool).await;
    let initial = tokens(&store, &native.secret).await;
    let selected = vec!["catalog:read".into(), "remote:control".into()];
    let mut request = refresh(&initial.refresh_token);
    request.scopes = Some(&selected);
    let rotated = store.refresh(request, 3600).await.unwrap();
    assert_ne!(initial.refresh_token, rotated.refresh_token);
    assert_eq!(rotated.scopes, selected);
    let widen = vec![
        "catalog:read".into(),
        "remote:control".into(),
        "models:invoke".into(),
    ];
    let mut request = refresh(&rotated.refresh_token);
    request.scopes = Some(&widen);
    assert!(store.refresh(request, 3600).await.is_err());
    let reopened = GrantStore::open(identity, profile()).await.unwrap();
    assert!(
        reopened
            .authorize_remote(&rotated.access_token)
            .await
            .is_ok()
    );
    // Expired consumed lineage still revokes a currently live family.
    sqlx::query("UPDATE core_oauth.refresh_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE digest=$1").bind(digest(&initial.refresh_token).unwrap()).execute(&pool).await.unwrap();
    assert!(
        reopened
            .refresh(refresh(&initial.refresh_token), 3600)
            .await
            .is_err()
    );
    let reopened = GrantStore::open(IdentityStore::from_pool(pool.clone()), profile())
        .await
        .unwrap();
    assert!(
        reopened
            .authorize_remote(&rotated.access_token)
            .await
            .is_err()
    );
    assert!(
        reopened
            .refresh(refresh(&rotated.refresh_token), 3600)
            .await
            .is_err()
    );
    let count: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_oauth.refresh_tokens WHERE consumed_at IS NOT NULL",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(count, 1);
}
#[sqlx::test(migrations = false)]
async fn grants_concurrent_refresh_loser_commits_family_revocation(pool: PgPool) {
    let (_, store, native) = fresh(&pool).await;
    let bundle = tokens(&store, &native.secret).await;
    let (a, b) = tokio::join!(
        store.refresh(refresh(&bundle.refresh_token), 3600),
        store.refresh(refresh(&bundle.refresh_token), 3600)
    );
    assert_ne!(a.is_ok(), b.is_ok());
    let winner = a.or(b).unwrap();
    assert!(store.authorize_remote(&winner.access_token).await.is_err());
    assert!(
        store
            .refresh(refresh(&winner.refresh_token), 3600)
            .await
            .is_err()
    );
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
}
#[sqlx::test(migrations = false)]
async fn grants_live_authority_expiry_consent_and_revoke(pool: PgPool) {
    let (identity, store, _) = fresh(&pool).await;
    for (index, mutation) in [
        "UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=1",
        "UPDATE core_identity.users SET active=false WHERE id=1",
        "UPDATE core_identity.accounts SET active=false WHERE id=(SELECT personal_account_id FROM core_identity.users WHERE id=1)",
        "UPDATE core_oauth.families SET consent_version=consent_version+1",
        "UPDATE core_oauth.families SET created_at=clock_timestamp()-interval '1 day',expires_at=clock_timestamp()-interval '1 second'",
        "UPDATE core_oauth.access_tokens SET expires_at=created_at+interval '0.001 second'",
    ].into_iter().enumerate() {
        // Each mutation gets independent native authority; generations never rewind.
        let user_id = index as i64 + 2;
        let native = identity.bootstrap_user(user_id, 1).await.unwrap();
        let bundle = tokens(&store, &native.secret).await;
        let mutation = mutation.replace("id=1", &format!("id={user_id}"));
        let mut tx = pool.begin().await.unwrap();
        sqlx::raw_sql(&mutation).execute(&mut *tx).await.unwrap();
        tx.commit().await.unwrap();
        assert!(
            store.authorize_remote(&bundle.access_token).await.is_err(),
            "{mutation}"
        );
    }
    let native = identity.bootstrap_user(20, 1).await.unwrap();
    let bundle = tokens(&store, &native.secret).await;
    store
        .revoke_access(&bundle.access_token, "lmm-pi")
        .await
        .unwrap();
    store
        .revoke_access(&bundle.access_token, "lmm-pi")
        .await
        .unwrap();
    assert!(store.authorize_remote(&bundle.access_token).await.is_err());
    assert!(
        store
            .refresh(refresh(&bundle.refresh_token), 3600)
            .await
            .is_err()
    );
    let bundle = tokens(&store, &native.secret).await;
    let selected = vec!["catalog:read".into()];
    let mut request = refresh(&bundle.refresh_token);
    request.scopes = Some(&selected);
    store.refresh(request, 3600).await.unwrap();
    assert!(store.authorize_remote(&bundle.access_token).await.is_err());
}
#[sqlx::test(migrations = false)]
async fn grants_owner_rebinding_and_transaction_failure_retry(pool: PgPool) {
    let (_, store, native) = fresh(&pool).await;
    let code = seed(&store, &native.secret, &["remote:control"]).await;
    // PostgreSQL injected persistence failure must roll back code consumption.
    sqlx::raw_sql("CREATE FUNCTION core_oauth.fail_insert() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture issuance failure'; END $$; CREATE TRIGGER fail_insert BEFORE INSERT ON core_oauth.refresh_tokens FOR EACH ROW EXECUTE FUNCTION core_oauth.fail_insert()").execute(&pool).await.unwrap();
    assert!(store.exchange(exchange(&code), 3600).await.is_err());
    let count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_oauth.codes WHERE consumed_at IS NOT NULL")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(count, 0);
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 0);
    sqlx::raw_sql("DROP TRIGGER fail_insert ON core_oauth.refresh_tokens; DROP FUNCTION core_oauth.fail_insert()").execute(&pool).await.unwrap();
    let bundle = store.exchange(exchange(&code), 3600).await.unwrap();
    let replacement: i64 = sqlx::query_scalar(
        "INSERT INTO core_identity.accounts(kind) VALUES ('personal') RETURNING id",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    let error = sqlx::query("UPDATE core_identity.users SET personal_account_id=$1 WHERE id=1")
        .bind(replacement)
        .execute(&pool)
        .await
        .unwrap_err();
    assert_eq!(
        error.as_database_error().unwrap().code().as_deref(),
        Some("23514")
    );
    // Native identity remains immutable; attempted rebinding does not revoke it.
    assert!(store.authorize_remote(&bundle.access_token).await.is_ok());
    // Separately corrupt a family's binding: defensive authority checks must deny.
    sqlx::query("UPDATE core_oauth.families SET owner_account_id=$1")
        .bind(replacement)
        .execute(&pool)
        .await
        .unwrap();
    assert!(store.authorize_remote(&bundle.access_token).await.is_err());
    assert!(
        store
            .refresh(refresh(&bundle.refresh_token), 3600)
            .await
            .is_err()
    );
}
#[sqlx::test(migrations = false)]
async fn grants_contract_rejects_drift_without_repair(pool: PgPool) {
    let (identity, _, _) = fresh(&pool).await;
    sqlx::raw_sql("ALTER TABLE core_oauth.families ADD COLUMN unexpected TEXT")
        .execute(&pool)
        .await
        .unwrap();
    assert!(GrantStore::open(identity, profile()).await.is_err());
    let exists:bool=sqlx::query_scalar("SELECT EXISTS(SELECT FROM information_schema.columns WHERE table_schema='core_oauth' AND column_name='unexpected')").fetch_one(&pool).await.unwrap();
    assert!(exists);
}
#[sqlx::test(migrations = false)]
async fn grants_browser_refresh_does_not_revoke_independent_family(pool: PgPool) {
    let (identity, store, _) = fresh(&pool).await;
    let browser = BrowserAuth::new(identity.with_registration_enabled(true), &[42; 32]).unwrap();
    browser
        .register(&BrowserRegistration {
            username: "oauthfixture",
            password: "fixture_password",
            email: "",
        })
        .await
        .unwrap();
    let login = browser
        .login(
            "oauthfixture",
            "fixture_password",
            &BrowserSessionMetadata::default(),
        )
        .await
        .unwrap();
    let access = login.data["access_token"].as_str().unwrap();
    let bundle = tokens(&store, access).await;
    browser.refresh(&login.refresh_token, None).await.unwrap();
    assert!(store.identity.authorize(access).await.is_err());
    assert!(store.authorize_remote(&bundle.access_token).await.is_ok());
}

#[sqlx::test(migrations = false)]
async fn grants_ttl_boundaries_and_refresh_failure_retry(pool: PgPool) {
    let (_, store, native) = fresh(&pool).await;
    for ttl in [1, 86400] {
        let code = seed(&store, &native.secret, &["remote:control"]).await;
        let bundle = store.exchange(exchange(&code), ttl).await.unwrap();
        assert!((1..=ttl).contains(&bundle.expires_in));
        assert!(store.authorize_remote(&bundle.access_token).await.is_ok());
    }
    let bundle = tokens(&store, &native.secret).await;
    sqlx::raw_sql("CREATE FUNCTION core_oauth.fail_refresh() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'fixture refresh failure'; END $$; CREATE TRIGGER fail_refresh BEFORE INSERT ON core_oauth.refresh_tokens FOR EACH ROW EXECUTE FUNCTION core_oauth.fail_refresh()").execute(&pool).await.unwrap();
    let before: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert!(
        store
            .refresh(refresh(&bundle.refresh_token), 3600)
            .await
            .is_err()
    );
    let after: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(before, after);
    let consumed: bool = sqlx::query_scalar(
        "SELECT consumed_at IS NOT NULL FROM core_oauth.refresh_tokens WHERE digest=$1",
    )
    .bind(digest(&bundle.refresh_token).unwrap())
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(!consumed);
    sqlx::raw_sql("DROP TRIGGER fail_refresh ON core_oauth.refresh_tokens; DROP FUNCTION core_oauth.fail_refresh()").execute(&pool).await.unwrap();
    assert!(
        store
            .refresh(refresh(&bundle.refresh_token), 3600)
            .await
            .is_ok()
    );
    tokio::time::sleep(std::time::Duration::from_millis(1100)).await;
    let expired: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_oauth.access_tokens WHERE expires_at<=clock_timestamp()",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(expired >= 1);
}
#[sqlx::test(migrations = false)]
async fn grants_restricted_runtime_permissions(pool: PgPool) {
    let (_, store, native) = fresh(&pool).await;
    let code = seed(&store, &native.secret, &["remote:control"]).await;
    let suffix = new_secret("").unwrap();
    let role = format!("oauth_fixture_runtime_{}", &suffix[..16]);
    let login = format!("oauth_fixture_login_{}", &suffix[..16]);
    let password = new_secret("").unwrap();
    let database = pool.connect_options().get_database().unwrap().to_owned();
    assert!(
        database
            .bytes()
            .all(|b| b.is_ascii_alphanumeric() || b == b'_')
    );
    // Own both cluster roles in this isolated SQLx database. The LOGIN connector
    // has no inherited authority; SET ROLE selects the original NOLOGIN runtime.
    // Using owner credentials here would allow SET ROLE back to a superuser.
    let mut setup = pool.begin().await.unwrap();
    sqlx::raw_sql(&format!(
        r#"CREATE ROLE {role} NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
        CREATE ROLE {login} LOGIN PASSWORD '{password}' NOINHERIT NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS;
        GRANT CONNECT ON DATABASE "{database}" TO {login};
        GRANT {role} TO {login} WITH INHERIT FALSE, SET TRUE;
        GRANT USAGE ON SCHEMA core_meta,core_identity,core_oauth TO {role};
        GRANT SELECT ON core_meta.schema_contract,core_identity.users,core_identity.accounts TO {role};
        GRANT UPDATE ON core_identity.users,core_identity.accounts TO {role};
        GRANT SELECT ON ALL TABLES IN SCHEMA core_oauth TO {role};
        GRANT INSERT,UPDATE ON core_oauth.families,core_oauth.codes,core_oauth.access_tokens,core_oauth.refresh_tokens TO {role};
        -- The table CHECK references this pure format function. Match the
        -- existing production runtime grant; PUBLIC execute remains revoked.
        GRANT EXECUTE ON FUNCTION core_oauth.readonly_scope_set(text[]) TO {role};"#
    )).execute(&mut *setup).await.unwrap();
    setup.commit().await.unwrap();
    let selected_role = role.clone();
    let restricted = sqlx::postgres::PgPoolOptions::new()
        .max_connections(2)
        .after_connect(move |c, _| {
            let sql = format!("SET ROLE {selected_role}");
            Box::pin(async move {
                sqlx::query(&sql).execute(c).await?;
                Ok(())
            })
        })
        .connect_lazy_with(
            (*pool.connect_options())
                .clone()
                .username(&login)
                .password(&password),
        );
    let checked_pool = restricted.clone();
    let checked_role = role.clone();
    let checked_login = login.clone();
    // Capture assertion panics so the owner closes connections and removes the
    // synthetic roles before propagating the original failure to SQLx.
    let checks = tokio::spawn(async move {
        let restricted = checked_pool;
        let role = checked_role;
        let authenticated_as: String = sqlx::query_scalar("SELECT session_user::text")
            .fetch_one(&restricted)
            .await
            .unwrap();
        assert_eq!(authenticated_as, checked_login);
        let current: String = sqlx::query_scalar("SELECT current_user::text")
            .fetch_one(&restricted)
            .await
            .unwrap();
        assert_eq!(current, role);
        let privileged: bool = sqlx::query_scalar(
            "SELECT rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls OR rolcanlogin FROM pg_roles WHERE rolname=current_user",
        )
        .fetch_one(&restricted)
        .await
        .unwrap();
        assert!(!privileged);
        let format_execute:bool=sqlx::query_scalar("SELECT has_function_privilege(current_user,'core_oauth.readonly_scope_set(text[])','EXECUTE')").fetch_one(&restricted).await.unwrap();
        assert!(format_execute);
        let runtime = GrantStore::open(IdentityStore::from_pool(restricted.clone()), profile())
            .await
            .unwrap();
        let bundle = runtime.exchange(exchange(&code), 3600).await.unwrap();
        assert!(runtime.authorize_remote(&bundle.access_token).await.is_ok());
        let rotated = runtime
            .refresh(refresh(&bundle.refresh_token), 3600)
            .await
            .unwrap();
        runtime
            .revoke_access(&rotated.access_token, "lmm-pi")
            .await
            .unwrap();
        for sql in [
            "CREATE TABLE core_oauth.forbidden(id int)",
            "UPDATE core_oauth.contract SET version=1",
            "DELETE FROM core_oauth.refresh_tokens",
            "ALTER TABLE core_oauth.families ADD COLUMN forbidden int",
            "DROP TABLE core_oauth.families",
            "INSERT INTO core_meta.schema_contract(version,fingerprint) VALUES (3,decode('00','hex'))",
            "SELECT nextval('core_identity.users_id_seq')",
        ] {
            let error = sqlx::raw_sql(sql).execute(&restricted).await.unwrap_err();
            assert_eq!(
                error.as_database_error().unwrap().code().as_deref(),
                Some("42501"),
                "{sql}: {error}"
            );
        }
        let foreign: String =
            sqlx::query_scalar("SELECT quote_ident(rolname) FROM pg_roles WHERE rolsuper LIMIT 1")
                .fetch_one(&restricted)
                .await
                .unwrap();
        let error = sqlx::query(&format!("SET ROLE {foreign}"))
            .execute(&restricted)
            .await
            .unwrap_err();
        assert_eq!(
            error.as_database_error().unwrap().code().as_deref(),
            Some("42501")
        );
    })
    .await;
    restricted.close().await;
    let cleanup = sqlx::raw_sql(&format!(
        "DROP OWNED BY {role},{login}; DROP ROLE {login}; DROP ROLE {role}"
    ))
    .execute(&pool)
    .await;
    if let Err(error) = checks {
        cleanup.expect("synthetic role cleanup after a failed assertion");
        if error.is_panic() {
            std::panic::resume_unwind(error.into_panic());
        }
        panic!("restricted permission check task did not finish: {error}");
    }
    cleanup.unwrap();
    let remaining: i64 =
        sqlx::query_scalar("SELECT count(*) FROM pg_roles WHERE rolname=$1 OR rolname=$2")
            .bind(&role)
            .bind(&login)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(remaining, 0);
}

#[sqlx::test(migrations = false)]
async fn grants_foreign_profile_replay_cannot_revoke_family(pool: PgPool) {
    let (identity, store, native) = fresh(&pool).await;
    let initial = tokens(&store, &native.secret).await;
    let rotated = store
        .refresh(refresh(&initial.refresh_token), 3600)
        .await
        .unwrap();
    let foreign_profile =
        GrantProfile::new("https://other.invalid", IssuerMode::Production).unwrap();
    let foreign = GrantStore::open(identity, foreign_profile).await.unwrap();
    let request = RefreshExchange {
        refresh: &initial.refresh_token,
        issuer: "https://other.invalid",
        resource: "https://other.invalid/api/oauth2",
        client_id: "lmm-pi",
        scopes: None,
    };
    assert!(matches!(
        foreign.refresh(request, 3600).await,
        Err(IdentityError::Unauthorized)
    ));
    let revoked: bool =
        sqlx::query_scalar("SELECT revoked_at IS NOT NULL FROM core_oauth.families")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(
        !revoked,
        "foreign-profile replay must not mutate the family"
    );
    assert!(store.authorize_remote(&rotated.access_token).await.is_ok());
    // Bound replay must still revoke, even if the consumed lineage has expired.
    sqlx::query("UPDATE core_oauth.refresh_tokens SET expires_at=clock_timestamp()-interval '1 second' WHERE digest=$1")
        .bind(digest(&initial.refresh_token).unwrap()).execute(&pool).await.unwrap();
    assert!(matches!(
        store.refresh(refresh(&initial.refresh_token), 3600).await,
        Err(IdentityError::Unauthorized)
    ));
    let revoked: bool =
        sqlx::query_scalar("SELECT revoked_at IS NOT NULL FROM core_oauth.families")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert!(revoked);
    assert!(store.authorize_remote(&rotated.access_token).await.is_err());
}

// Wait for a real PostgreSQL authority-lock waiter, then cross the exact DB
// expiry while holding that lock. No success mocks or fixed startup sleep.
async fn release_after_expiry(pool: &PgPool, mut blocker: Transaction<'_, Postgres>, table: &str) {
    let blocker_pid: i32 = sqlx::query_scalar("SELECT pg_backend_pid()")
        .fetch_one(&mut *blocker)
        .await
        .unwrap();
    tokio::time::timeout(Duration::from_millis(400), async {
        loop {
            let waiting: bool = sqlx::query_scalar(
                "SELECT EXISTS(SELECT FROM pg_stat_activity WHERE $1=ANY(pg_blocking_pids(pid)))",
            )
            .bind(blocker_pid)
            .fetch_one(pool)
            .await
            .unwrap();
            if waiting {
                break;
            }
            tokio::time::sleep(Duration::from_millis(5)).await;
        }
    })
    .await
    .expect("operation must reach the held authority lock before expiry");
    let still_live: bool = sqlx::query_scalar(&format!(
        "SELECT expires_at>clock_timestamp() FROM core_oauth.{table}"
    ))
    .fetch_one(pool)
    .await
    .unwrap();
    assert!(still_live, "wait must begin before exact expiry");
    sqlx::query(&format!("SELECT pg_sleep(GREATEST(0,extract(epoch FROM expires_at-clock_timestamp()))::double precision+0.03) FROM core_oauth.{table}"))
        .execute(pool).await.unwrap();
    let expired: bool = sqlx::query_scalar(&format!(
        "SELECT expires_at<=clock_timestamp() FROM core_oauth.{table}"
    ))
    .fetch_one(pool)
    .await
    .unwrap();
    assert!(expired);
    blocker.rollback().await.unwrap();
}

async fn expiry_lock_regression(pool: PgPool, mode: &str) {
    let (_, store, native) = fresh(&pool).await;
    let code = seed(&store, &native.secret, &["remote:control"]).await;
    let is_code = mode == "code_native";
    let bundle = if is_code {
        None
    } else {
        Some(store.exchange(exchange(&code), 3600).await.unwrap())
    };
    let table = match mode {
        "code_native" => "codes",
        "family_native" => "families",
        "refresh_native" => "refresh_tokens",
        _ => "access_tokens",
    };
    sqlx::query(&format!(
        "UPDATE core_oauth.{table} SET expires_at=clock_timestamp()+interval '500 milliseconds'"
    ))
    .execute(&pool)
    .await
    .unwrap();
    let mut blocker = pool.begin().await.unwrap();
    let lock = if mode == "access_family" {
        "SELECT id FROM core_oauth.families FOR UPDATE"
    } else {
        "SELECT id FROM core_identity.users WHERE id=1 FOR UPDATE"
    };
    sqlx::query(lock).execute(&mut *blocker).await.unwrap();
    let owned_store = store.clone();
    let is_refresh = mode == "refresh_native";
    let pending = tokio::spawn(async move {
        if is_code {
            owned_store
                .exchange(exchange(&code), 3600)
                .await
                .map(|_| ())
        } else {
            let bundle = bundle.unwrap();
            if is_refresh {
                owned_store
                    .refresh(refresh(&bundle.refresh_token), 3600)
                    .await
                    .map(|_| ())
            } else {
                owned_store
                    .authorize_remote(&bundle.access_token)
                    .await
                    .map(|_| ())
            }
        }
    });
    release_after_expiry(&pool, blocker, table).await;
    let result = pending.await.unwrap();
    assert!(
        matches!(
            result,
            Err(IdentityError::Unauthorized | IdentityError::Forbidden)
        ),
        "expired authority must deny after a lock wait, not succeed or time out: {result:?}"
    );
    if is_code || is_refresh {
        let consumed: i64 = sqlx::query_scalar(&format!(
            "SELECT count(*) FROM core_oauth.{table} WHERE consumed_at IS NOT NULL"
        ))
        .fetch_one(&pool)
        .await
        .unwrap();
        assert_eq!(consumed, 0, "expired lineage must remain unconsumed");
        let issued: i64 = sqlx::query_scalar("SELECT count(*) FROM core_oauth.access_tokens")
            .fetch_one(&pool)
            .await
            .unwrap();
        assert_eq!(issued, if is_code { 0 } else { 1 });
    }
}

#[sqlx::test(migrations = false)]
async fn grants_access_expiry_during_family_lock(pool: PgPool) {
    expiry_lock_regression(pool, "access_family").await;
}
#[sqlx::test(migrations = false)]
async fn grants_access_expiry_during_native_lock(pool: PgPool) {
    expiry_lock_regression(pool, "access_native").await;
}
#[sqlx::test(migrations = false)]
async fn grants_family_expiry_during_native_lock(pool: PgPool) {
    expiry_lock_regression(pool, "family_native").await;
}
#[sqlx::test(migrations = false)]
async fn grants_code_expiry_during_native_lock(pool: PgPool) {
    expiry_lock_regression(pool, "code_native").await;
}
#[sqlx::test(migrations = false)]
async fn grants_refresh_expiry_during_native_lock(pool: PgPool) {
    expiry_lock_regression(pool, "refresh_native").await;
}
