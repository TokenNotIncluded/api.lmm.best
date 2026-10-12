//! Owner-seeded SQLx databases; ONLY the restricted role serves model traffic.
//! Reuse the real HTTP/provider/price helpers, not an owner-backed gateway.
use super::*;
use lmm_core::{
    accounts::TeamRole,
    identity::InviteRequest,
    runtime_database::{self, RequiredCapabilities, RuntimeDatabaseError},
};

#[path = "../common/runtime.rs"]
mod runtime;

fn requirements() -> RequiredCapabilities {
    RequiredCapabilities {
        models: true,
        ..RequiredCapabilities::default()
    }
}

async fn restricted(owner: &PgPool) -> runtime::RuntimeRole {
    let role = runtime::RuntimeRole::create(owner).await;
    assert_eq!(
        reqwest::Url::parse(&role.url).unwrap().username(),
        role.name
    );
    role.optional("core_charging").await;
    role.optional("core_model").await;
    runtime_database::check(&role.pool, requirements())
        .await
        .unwrap();
    for table in ["key_funding_rules", "credential_grants"] {
        let forbidden: bool = sqlx::query_scalar(
            "SELECT has_table_privilege($1,'UPDATE,DELETE') OR has_any_column_privilege($1,'UPDATE')",
        )
        .bind(format!("core_identity.{table}"))
        .fetch_one(&role.pool)
        .await
        .unwrap();
        assert!(!forbidden, "runtime may not amend {table}");
    }
    role
}

async fn assert_paid(
    owner: &PgPool,
    ledger: &Ledger,
    account: i64,
    id: &str,
    provider: &Provider,
    hits: usize,
    available: i64,
) {
    let charge: (String, i64, i64, i64, i64, i64, i64) = sqlx::query_as(
        "SELECT state,reserved,settled,payer_account_id,input_tokens,cached_tokens,output_tokens \
         FROM core_charging.requests WHERE id=$1",
    )
    .bind(id)
    .fetch_one(owner)
    .await
    .unwrap();
    assert_eq!(charge, ("settled".into(), 140, 49, account, 10, 2, 20));
    let price: Value = sqlx::query_scalar("SELECT price FROM core_charging.requests WHERE id=$1")
        .bind(id)
        .fetch_one(owner)
        .await
        .unwrap();
    assert_eq!(price["group"], "allowed");
    assert_eq!(price["input_per_million"], 1_000_000);
    assert_eq!(price["cached_per_million"], 250_000);
    assert_eq!(price["output_per_million"], 2_000_000);
    let holds: Vec<(i64, i64, bool)> = sqlx::query_as(
        "SELECT reserved,captured,closed FROM core_charging.ledger_holds WHERE request_id=$1",
    )
    .bind(id)
    .fetch_all(owner)
    .await
    .unwrap();
    // Keep the closed receipt; there must be no LIVE hold. Capture returns the
    // 91-credit surplus to available balance rather than billing the ceiling.
    assert_eq!(holds, vec![(140, 49, true)]);
    let surplus: i64 = sqlx::query_scalar(
        "SELECT e.delta_units FROM core_charging.ledger_holds h \
         JOIN core_billing.ledger_entries e ON e.journal_id=h.capture_journal_id \
         JOIN core_billing.ledger_accounts a ON a.id=e.ledger_account_id \
         WHERE h.request_id=$1 AND a.owner_account_id=$2 AND a.bucket='wallet'",
    )
    .bind(id)
    .bind(account)
    .fetch_one(owner)
    .await
    .unwrap();
    assert_eq!(surplus, 91);
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!(
        (balance.available_units, balance.reserved_units),
        (available, 0)
    );
    assert_eq!(provider.hits.load(Ordering::SeqCst), hits);
    balanced(owner).await;
}

async fn assert_no_admission(owner: &PgPool, provider: &Provider) {
    assert_eq!(provider.hits.load(Ordering::SeqCst), 0);
    for table in ["requests", "ledger_holds", "ledger_commands"] {
        let count: i64 = sqlx::query_scalar(&format!("SELECT count(*) FROM core_charging.{table}"))
            .fetch_one(owner)
            .await
            .unwrap();
        assert_eq!(count, 0, "denial retained {table}");
    }
    balanced(owner).await;
}

#[sqlx::test(migrations = false)]
async fn restricted_personal_chat_and_responses_reserve_and_settle_paid_quote(pool: PgPool) {
    let (_, ledger, _, key, key_id, account) = fixture(&pool, 1000).await;
    let role = restricted(&pool).await;
    let provider = Provider::start(true).await;
    let gateway = gateway(&role.pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    for (index, (path, body)) in [
        ("/v1/chat/completions", chat()),
        (
            "/v1/responses",
            json!({"model":"local-model","input":"hello","max_output_tokens":20}),
        ),
    ]
    .into_iter()
    .enumerate()
    {
        let (status, answer, id) =
            call_with_request_id(&gateway, Some(&key), path, body, None).await;
        assert_eq!(status, StatusCode::OK, "{answer}");
        assert_paid(
            &pool,
            &ledger,
            account,
            &id.unwrap(),
            &provider,
            index + 1,
            1000 - 49 * (index as i64 + 1),
        )
        .await;
    }
    drop(gateway);
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn restricted_personal_wrong_group_and_insufficient_funds_never_dispatch(pool: PgPool) {
    let (_, ledger, session, key, key_id, account) = fixture(&pool, 0).await;
    let role = restricted(&pool).await;
    let provider = Provider::start(true).await;
    // Seed a managed key restricted to a different group; the valid host
    // mapping/quote must not override its credential-level group restriction.
    sqlx::query("INSERT INTO core_identity.api_key_settings(credential_id,name,group_name) VALUES ($1,'synthetic-group-key','wrong-group')")
        .bind(key_id).execute(&pool).await.unwrap();
    sqlx::raw_sql("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES (7,'allowed','synthetic:model-runtime')")
        .execute(&pool).await.unwrap();
    let gateway = gateway(&role.pool, &provider, key_id, 0, 0, PayloadPolicy::Adapt);
    let (status, error) = call(&gateway, Some(&key), "/v1/chat/completions", chat(), None).await;
    assert_eq!(status, StatusCode::FORBIDDEN, "{error}");
    assert_no_admission(&pool, &provider).await;
    sqlx::query(
        "UPDATE core_identity.api_key_settings SET group_name='allowed' WHERE credential_id=$1",
    )
    .bind(key_id)
    .execute(&pool)
    .await
    .unwrap();
    assert_eq!(
        call(&gateway, None, "/v1/chat/completions", chat(), None)
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        call(
            &gateway,
            Some(&session),
            "/v1/chat/completions",
            chat(),
            None
        )
        .await
        .0,
        StatusCode::FORBIDDEN
    );
    let (status, error) = call(&gateway, Some(&key), "/v1/chat/completions", chat(), None).await;
    assert_eq!(status, StatusCode::PAYMENT_REQUIRED, "{error}");
    assert_eq!(error["error"]["code"], "insufficient_funds");
    assert_no_admission(&pool, &provider).await;
    let balance = ledger.balance(account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (0, 0));
    drop(gateway);
    role.cleanup().await;
}

struct TeamFixture {
    identity: IdentityStore,
    ledger: Ledger,
    owner_session: String,
    member_session: String,
    team: i64,
    account: i64,
    personal: i64,
    team_key: (String, i64),
    fallback_key: (String, i64),
}

fn invitation() -> InviteRequest {
    InviteRequest {
        recipient_user_id: 8,
        role: TeamRole::Member,
        can_spend: true,
        ttl_seconds: 3600,
    }
}

async fn team_fixture(owner: &PgPool) -> TeamFixture {
    let (identity, ledger, owner_session, _, _, _) = fixture(owner, 0).await;
    let member = identity.bootstrap_user(8, 0).await.unwrap();
    let personal = identity
        .authorize(&member.secret)
        .await
        .unwrap()
        .owner_account_id;
    ledger.open_wallet(personal).await.unwrap();
    let team = identity.create_team(&owner_session).await.unwrap();
    let invite = identity
        .invite(&owner_session, team, &invitation())
        .await
        .unwrap();
    identity
        .accept_invite(&member.secret, &invite.secret)
        .await
        .unwrap();
    let (team_key, fallback_key) = team_keys(&identity, &member.secret, team).await;
    let account = identity
        .authorize(&team_key.0)
        .await
        .unwrap()
        .owner_account_id;
    ledger.open_wallet(account).await.unwrap();
    ledger
        .execute(&LedgerRequest {
            scope: "model-http-runtime-team".into(),
            operation_key: "synthetic-team-funds".into(),
            actor_user_id: Some(7),
            reason: "Synthetic team funds in a disposable SQLx database".into(),
            action: Action::Credit {
                account_id: account,
                amount_units: Credits::try_from(1000).unwrap(),
                payment_reference: "synthetic:model-runtime-team".into(),
            },
        })
        .await
        .unwrap();
    TeamFixture {
        identity,
        ledger,
        owner_session,
        member_session: member.secret,
        team,
        account,
        personal,
        team_key,
        fallback_key,
    }
}

async fn team_keys(
    identity: &IdentityStore,
    session: &str,
    team: i64,
) -> ((String, i64), (String, i64)) {
    let team_account = Account {
        kind: AccountKind::Team,
        id: team,
    };
    let personal = Account {
        kind: AccountKind::Personal,
        id: 8,
    };
    let team_key = identity
        .issue_key(
            session,
            team_account,
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let mut policy = FundingPolicy::new_personal_key();
    // Empty member wallet FIRST: prove actual fallback, not merely team-first.
    policy.account_order = Some(vec![personal, team_account]);
    let fallback_key = identity
        .issue_key(session, personal, policy, 3600)
        .await
        .unwrap();
    (
        (team_key.secret, team_key.id),
        (fallback_key.secret, fallback_key.id),
    )
}

#[sqlx::test(migrations = false)]
async fn restricted_team_owned_and_personal_team_fallback_settle_paid_quote(pool: PgPool) {
    let seed = team_fixture(&pool).await;
    let role = restricted(&pool).await;
    let provider = Provider::start(true).await;
    for (index, (secret, key)) in [&seed.team_key, &seed.fallback_key].into_iter().enumerate() {
        let gateway = gateway(&role.pool, &provider, *key, 0, 0, PayloadPolicy::Adapt);
        let (status, answer, id) =
            call_with_request_id(&gateway, Some(secret), "/v1/chat/completions", chat(), None)
                .await;
        assert_eq!(status, StatusCode::OK, "{answer}");
        let id = id.unwrap();
        assert_paid(
            &pool,
            &seed.ledger,
            seed.account,
            &id,
            &provider,
            index + 1,
            1000 - 49 * (index as i64 + 1),
        )
        .await;
        let owner: i64 =
            sqlx::query_scalar("SELECT owner_account_id FROM core_charging.requests WHERE id=$1")
                .bind(&id)
                .fetch_one(&pool)
                .await
                .unwrap();
        assert_eq!(
            owner,
            if index == 0 {
                seed.account
            } else {
                seed.personal
            }
        );
        let personal = seed.ledger.balance(seed.personal).await.unwrap();
        assert_eq!((personal.available_units, personal.reserved_units), (0, 0));
        drop(gateway);
    }
    role.cleanup().await;
}

async fn deny_team_keys(
    owner: &PgPool,
    runtime: &PgPool,
    provider: &Provider,
    keys: &[&(String, i64)],
) {
    for (secret, key) in keys {
        let gateway = gateway(runtime, provider, *key, 0, 0, PayloadPolicy::Adapt);
        let (status, error) =
            call(&gateway, Some(secret), "/v1/chat/completions", chat(), None).await;
        assert_eq!(status, StatusCode::FORBIDDEN, "{error}");
    }
    assert_no_admission(owner, provider).await;
}

#[sqlx::test(migrations = false)]
async fn restricted_stale_revoked_and_nonspending_team_authority_denies_before_dispatch(
    pool: PgPool,
) {
    let seed = team_fixture(&pool).await;
    let role = restricted(&pool).await;
    let provider = Provider::start(true).await;
    // Owner-only fixture mutations isolate each mutable-authority predicate.
    // Generations only increase: never rewind them or disable schema guards.
    for (change, restore) in [
        ("active=false", "active=true"),
        ("can_spend=false", "can_spend=true"),
    ] {
        let (team, fallback) = team_keys(&seed.identity, &seed.member_session, seed.team).await;
        sqlx::query(&format!("UPDATE core_identity.memberships SET {change},version=version+1 WHERE team_id=$1 AND user_id=8"))
            .bind(seed.team).execute(&pool).await.unwrap();
        deny_team_keys(&pool, &role.pool, &provider, &[&team, &fallback]).await;
        sqlx::query(&format!("UPDATE core_identity.memberships SET {restore},version=version+1 WHERE team_id=$1 AND user_id=8"))
            .bind(seed.team).execute(&pool).await.unwrap();
    }
    let (member_team, member_fallback) =
        team_keys(&seed.identity, &seed.member_session, seed.team).await;
    sqlx::query(
        "UPDATE core_identity.memberships SET version=version+1 WHERE team_id=$1 AND user_id=8",
    )
    .bind(seed.team)
    .execute(&pool)
    .await
    .unwrap();
    deny_team_keys(
        &pool,
        &role.pool,
        &provider,
        &[&member_team, &member_fallback],
    )
    .await;
    let (stale_team, stale_fallback) =
        team_keys(&seed.identity, &seed.member_session, seed.team).await;
    sqlx::query("UPDATE core_identity.teams SET version=version+1 WHERE id=$1")
        .bind(seed.team)
        .execute(&pool)
        .await
        .unwrap();
    deny_team_keys(
        &pool,
        &role.pool,
        &provider,
        &[&stale_team, &stale_fallback],
    )
    .await;
    // Real removal/rejoin: old grant versions must never become current again.
    let (removed_team, removed_fallback) =
        team_keys(&seed.identity, &seed.member_session, seed.team).await;
    let version: i64 = sqlx::query_scalar(
        "SELECT version FROM core_identity.memberships WHERE team_id=$1 AND user_id=8",
    )
    .bind(seed.team)
    .fetch_one(&pool)
    .await
    .unwrap();
    seed.identity
        .remove_member(&seed.owner_session, seed.team, 8, version)
        .await
        .unwrap();
    deny_team_keys(
        &pool,
        &role.pool,
        &provider,
        &[&removed_team, &removed_fallback],
    )
    .await;
    let invite = seed
        .identity
        .invite(&seed.owner_session, seed.team, &invitation())
        .await
        .unwrap();
    seed.identity
        .accept_invite(&seed.member_session, &invite.secret)
        .await
        .unwrap();
    deny_team_keys(
        &pool,
        &role.pool,
        &provider,
        &[&removed_team, &removed_fallback],
    )
    .await;
    let (fresh_team, fresh_fallback) =
        team_keys(&seed.identity, &seed.member_session, seed.team).await;
    // Privileged offline grant removal coordinates through credential AND team
    // authority locks; runtime still has no evidence UPDATE/DELETE permission.
    let mut amendment = pool.begin().await.unwrap();
    sqlx::query(
        "SELECT id FROM core_identity.credentials WHERE id IN ($1,$2) ORDER BY id FOR UPDATE",
    )
    .bind(fresh_team.1)
    .bind(fresh_fallback.1)
    .fetch_all(&mut *amendment)
    .await
    .unwrap();
    sqlx::query("SELECT id FROM core_identity.teams WHERE id=$1 FOR UPDATE")
        .bind(seed.team)
        .fetch_one(&mut *amendment)
        .await
        .unwrap();
    sqlx::query("DELETE FROM core_identity.credential_grants WHERE credential_id IN ($1,$2)")
        .bind(fresh_team.1)
        .bind(fresh_fallback.1)
        .execute(&mut *amendment)
        .await
        .unwrap();
    amendment.commit().await.unwrap();
    deny_team_keys(
        &pool,
        &role.pool,
        &provider,
        &[&fresh_team, &fresh_fallback],
    )
    .await;
    let balance = seed.ledger.balance(seed.account).await.unwrap();
    assert_eq!((balance.available_units, balance.reserved_units), (1000, 0));
    role.cleanup().await;
}

#[sqlx::test(migrations = false)]
async fn restricted_model_startup_rejects_evidence_table_and_column_update_grants(pool: PgPool) {
    fixture(&pool, 0).await;
    let role = restricted(&pool).await;
    // Deliberately unsafe GRANTs exist ONLY in this disposable negative test;
    // revoke immediately. They are never a gateway workaround.
    for grant in [
        "UPDATE ON core_identity.key_funding_rules",
        "UPDATE(position) ON core_identity.key_funding_rules",
        "UPDATE ON core_identity.credential_grants",
        "UPDATE(team_version) ON core_identity.credential_grants",
    ] {
        sqlx::raw_sql(&format!("GRANT {grant} TO {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            runtime_database::check(&role.pool, requirements()).await,
            Err(RuntimeDatabaseError::UnsafePrivileges),
            "accepted {grant}"
        );
        sqlx::raw_sql(&format!("REVOKE {grant} FROM {}", role.name))
            .execute(&pool)
            .await
            .unwrap();
        runtime_database::check(&role.pool, requirements())
            .await
            .unwrap();
    }
    role.cleanup().await;
}
