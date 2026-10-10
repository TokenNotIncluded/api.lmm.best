use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use lmm_core::{
    accounts::{Account, AccountKind, TeamRole},
    funding::FundingPolicy,
    identity::{IdentityError as E, IdentityStore, InviteRequest, LoginRequest, MemberUpdate},
    identity_http,
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use tower::ServiceExt;

fn login(name: &str) -> LoginRequest {
    LoginRequest {
        login_name: name.into(),
        password: "a real test passphrase".into(),
    }
}
fn team(id: i64) -> Account {
    Account {
        kind: AccountKind::Team,
        id,
    }
}
fn personal(id: i64) -> Account {
    Account {
        kind: AccountKind::Personal,
        id,
    }
}
fn invitation(user: i64, role: TeamRole, can_spend: bool) -> InviteRequest {
    InviteRequest {
        recipient_user_id: user,
        role,
        can_spend,
        ttl_seconds: 3600,
    }
}
async fn fresh(pool: PgPool) -> IdentityStore {
    let store = IdentityStore::from_pool(pool).with_registration_enabled(true);
    store.init_database().await.unwrap();
    store.check_schema().await.unwrap();
    store
}
async fn join(
    store: &IdentityStore,
    owner: &str,
    id: i64,
    member: &str,
    user: i64,
    role: TeamRole,
    spend: bool,
) {
    let invite = store
        .invite(owner, id, &invitation(user, role, spend))
        .await
        .unwrap();
    store.accept_invite_by_id(member, invite.id).await.unwrap();
}

#[sqlx::test(migrations = false)]
async fn native_registration_login_rotation_and_logout_preserve_personal_identity(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    store.bootstrap_user(100, 6).await.unwrap();
    let original = store.register(&login("Alice")).await.unwrap();
    let actor = store.authorize(&original.secret).await.unwrap();
    assert_eq!(actor.user_id, 101);
    assert_eq!(actor.platform_level, 0);
    assert_eq!(actor.owner, personal(actor.user_id));
    assert!(matches!(
        store.register(&login("ALICE")).await,
        Err(E::Conflict)
    ));
    let bob = store.register(&login("bob")).await.unwrap();
    let hashes: Vec<String> = sqlx::query_scalar(
        "SELECT password_hash FROM core_identity.password_logins ORDER BY login_name",
    )
    .fetch_all(&pool)
    .await
    .unwrap();
    assert!(
        hashes
            .iter()
            .all(|h| h.starts_with("$argon2id$v=19$m=19456,t=2,p=1$"))
    );
    assert_ne!(hashes[0], hashes[1]);
    let mut wrong = login("alice");
    wrong.password = "wrong password".into();
    assert!(matches!(store.login(&wrong).await, Err(E::Unauthorized)));
    assert!(matches!(
        store.login(&login("unknown")).await,
        Err(E::Unauthorized)
    ));
    let second = store.login(&login("aLiCe")).await.unwrap();
    let key = store
        .issue_key(
            &second.secret,
            personal(actor.user_id),
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    assert!(matches!(
        store.refresh_session(&key.secret).await,
        Err(E::Forbidden)
    ));
    let renewed = store.refresh_session(&second.secret).await.unwrap();
    assert_ne!(second.secret, renewed.secret);
    assert!(matches!(
        store.authorize(&second.secret).await,
        Err(E::Unauthorized)
    ));
    assert!(store.refresh_session(&second.secret).await.is_err());
    assert_eq!(
        store.authorize(&renewed.secret).await.unwrap().user_id,
        actor.user_id
    );
    assert!(
        sqlx::query("UPDATE core_identity.credentials SET revoked_at=NULL WHERE id=$1")
            .bind(second.id)
            .execute(&pool)
            .await
            .is_err()
    );
    store.logout_all(&renewed.secret).await.unwrap();
    for secret in [&original.secret, &renewed.secret] {
        assert!(store.authorize(secret).await.is_err());
    }
    assert!(store.authorize(&key.secret).await.is_ok());
    assert!(store.authorize(&bob.secret).await.is_ok());
    let back = store.login(&login("alice")).await.unwrap();
    store.logout(&back.secret).await.unwrap();
    assert!(store.authorize(&back.secret).await.is_err());
    let audit: Vec<String> = sqlx::query_scalar("SELECT target::text FROM core_identity.audit")
        .fetch_all(&pool)
        .await
        .unwrap();
    assert!(audit.iter().all(|s| !s.contains(&login("alice").password)
        && !s.contains(&original.secret)
        && !s.contains(&key.secret)));
    let accounts: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.accounts")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(accounts, 3);
}

#[sqlx::test(migrations = false)]
async fn rotation_has_one_winner_and_never_extends_the_absolute_login_limit(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let first = store.register(&login("alice")).await.unwrap();
    let actor = store.authorize(&first.secret).await.unwrap();
    let (a, b) = tokio::join!(
        store.refresh_session(&first.secret),
        store.refresh_session(&first.secret)
    );
    assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
    let rotated = a.or(b).unwrap();
    assert!(store.authorize(&rotated.secret).await.is_ok());
    let audit: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='session.refresh'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(audit, 1);
    let near_limit = format!("lmms_{}", "b".repeat(64));
    sqlx::query("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at,session_started_at) VALUES ($1,'session',$2,1,$3,clock_timestamp()+interval '1 hour',clock_timestamp()-interval '29 days 23 hours')")
        .bind(Sha256::digest(near_limit.as_bytes()).to_vec()).bind(actor.user_id).bind(actor.owner_account_id).execute(&pool).await.unwrap();
    let bounded = store.refresh_session(&near_limit).await.unwrap();
    let duration:i64=sqlx::query_scalar("SELECT EXTRACT(EPOCH FROM expires_at-session_started_at)::bigint FROM core_identity.credentials WHERE id=$1")
        .bind(bounded.id).fetch_one(&pool).await.unwrap();
    assert_eq!(duration, 30 * 86400);
    let expired = format!("lmms_{}", "c".repeat(64));
    sqlx::query("INSERT INTO core_identity.credentials(digest,kind,user_id,user_version,owner_account_id,expires_at,session_started_at) VALUES ($1,'session',$2,1,$3,clock_timestamp()+interval '1 hour',clock_timestamp()-interval '31 days')")
        .bind(Sha256::digest(expired.as_bytes()).to_vec()).bind(actor.user_id).bind(actor.owner_account_id).execute(&pool).await.unwrap();
    assert!(matches!(
        store.refresh_session(&expired).await,
        Err(E::Unauthorized)
    ));
}

#[sqlx::test(migrations = false)]
async fn invitations_have_recipient_bound_terminal_states_and_atomic_races(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let recipient = store.bootstrap_user(2, 0).await.unwrap();
    let outsider = store.bootstrap_user(3, 6).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let first = store
        .invite(&owner.secret, id, &invitation(2, TeamRole::Member, true))
        .await
        .unwrap();
    assert!(matches!(
        store.reject_invite(&outsider.secret, first.id).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.accept_invite_by_id(&outsider.secret, first.id).await,
        Err(E::Forbidden)
    ));
    let inbox = store
        .list_invites(&recipient.secret, None, 0)
        .await
        .unwrap();
    assert_eq!(inbox.len(), 1);
    assert_eq!(inbox[0].status, "pending");
    let serialized = serde_json::to_string(&inbox).unwrap();
    assert!(!serialized.contains(&first.secret));
    store
        .reject_invite(&recipient.secret, first.id)
        .await
        .unwrap();
    assert!(matches!(
        store.accept_invite(&recipient.secret, &first.secret).await,
        Err(E::Conflict)
    ));
    assert_eq!(
        store
            .list_invites(&recipient.secret, None, 0)
            .await
            .unwrap()[0]
            .status,
        "rejected"
    );
    assert!(
        sqlx::query("UPDATE core_identity.invites SET rejected_at=NULL WHERE id=$1")
            .bind(first.id)
            .execute(&pool)
            .await
            .is_err()
    );
    let withdrawn = store
        .invite(&owner.secret, id, &invitation(2, TeamRole::Member, true))
        .await
        .unwrap();
    store
        .withdraw_invite(&owner.secret, withdrawn.id)
        .await
        .unwrap();
    assert!(matches!(
        store
            .accept_invite(&recipient.secret, &withdrawn.secret)
            .await,
        Err(E::Conflict)
    ));
    let racing = store
        .invite(&owner.secret, id, &invitation(2, TeamRole::Member, true))
        .await
        .unwrap();
    let (accepted, rejected) = tokio::join!(
        store.accept_invite_by_id(&recipient.secret, racing.id),
        store.reject_invite(&recipient.secret, racing.id)
    );
    assert_eq!(
        usize::from(accepted.is_ok()) + usize::from(rejected.is_ok()),
        1
    );
    let audit:i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.audit WHERE target->>'invite_id'=$1 AND action IN ('team.invite.accept','team.invite.reject')")
        .bind(racing.id.to_string()).fetch_one(&pool).await.unwrap();
    assert_eq!(audit, 1);
}

#[sqlx::test(migrations = false)]
async fn member_edits_cannot_escalate_or_revive_old_spending_grants(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let owner = store.bootstrap_user(1, 0).await.unwrap();
    let admin = store.bootstrap_user(2, 5).await.unwrap();
    let member = store.bootstrap_user(3, 0).await.unwrap();
    let outsider = store.bootstrap_user(4, 6).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    join(
        &store,
        &owner.secret,
        id,
        &admin.secret,
        2,
        TeamRole::Admin,
        false,
    )
    .await;
    join(
        &store,
        &owner.secret,
        id,
        &member.secret,
        3,
        TeamRole::Member,
        true,
    )
    .await;
    let key = store
        .issue_key(
            &member.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let owner_key = store
        .issue_key(
            &owner.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let upgrade = MemberUpdate {
        role: TeamRole::Admin,
        can_spend: true,
        expected_version: 1,
    };
    assert!(matches!(
        store.update_member(&admin.secret, id, 3, &upgrade).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.update_member(&admin.secret, id, 2, &upgrade).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.update_member(&outsider.secret, id, 3, &upgrade).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.revoke(&member.secret, owner_key.id).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.revoke(&outsider.secret, key.id).await,
        Err(E::Forbidden)
    ));
    let disable = MemberUpdate {
        role: TeamRole::Member,
        can_spend: false,
        expected_version: 1,
    };
    store
        .update_member(&admin.secret, id, 3, &disable)
        .await
        .unwrap();
    assert!(matches!(
        store.authorize(&key.secret).await,
        Err(E::Forbidden)
    ));
    let restore = MemberUpdate {
        role: TeamRole::Member,
        can_spend: true,
        expected_version: 2,
    };
    store
        .update_member(&owner.secret, id, 3, &restore)
        .await
        .unwrap();
    assert!(matches!(
        store.authorize(&key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.update_member(&owner.secret, id, 3, &restore).await,
        Err(E::Conflict)
    ));
    let fresh_key = store
        .issue_key(
            &member.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    store.revoke(&admin.secret, fresh_key.id).await.unwrap();
    assert!(matches!(
        store.authorize(&fresh_key.secret).await,
        Err(E::Unauthorized)
    ));
    let rows = store.list_members(&member.secret, id, 0).await.unwrap();
    assert_eq!(rows.len(), 3);
    assert_eq!(
        rows.iter()
            .find(|r| r.user_id == 3)
            .unwrap()
            .membership_version,
        3
    );
    assert!(
        sqlx::query(
            "UPDATE core_identity.memberships SET version=1 WHERE team_id=$1 AND user_id=3"
        )
        .bind(id)
        .execute(&pool)
        .await
        .is_err()
    );
    assert!(
        sqlx::query("DELETE FROM core_identity.memberships WHERE team_id=$1 AND user_id=3")
            .bind(id)
            .execute(&pool)
            .await
            .is_err()
    );
}

#[sqlx::test(migrations = false)]
async fn creator_slot_survives_transfer_and_closure_without_conflating_owned_teams(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let owner = store.bootstrap_user(1, 0).await.unwrap();
    let next = store.bootstrap_user(2, 0).await.unwrap();
    let outside = store.bootstrap_user(3, 6).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let other = store.create_team(&next.secret).await.unwrap();
    join(
        &store,
        &owner.secret,
        id,
        &next.secret,
        2,
        TeamRole::Member,
        true,
    )
    .await;
    let owner_key = store
        .issue_key(
            &owner.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let member_key = store
        .issue_key(
            &next.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    assert!(matches!(
        store.transfer_team(&outside.secret, id, 2, 1).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.transfer_team(&owner.secret, id, 3, 1).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.transfer_team(&owner.secret, id, 2, 99).await,
        Err(E::Conflict)
    ));
    store.transfer_team(&owner.secret, id, 2, 1).await.unwrap();
    assert!(matches!(
        store.create_team(&owner.secret).await,
        Err(E::Conflict)
    ));
    assert!(matches!(
        store.authorize(&owner_key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.authorize(&member_key.secret).await,
        Err(E::Forbidden)
    ));
    let former = store.list_teams(&owner.secret, 0).await.unwrap();
    assert_eq!(former[0].role, TeamRole::Admin);
    assert!(!former[0].can_spend);
    assert_eq!(former[0].created_by_user_id, 1);
    assert_eq!(former[0].version, 2);
    let owned = store.list_teams(&next.secret, 0).await.unwrap();
    assert_eq!(owned.len(), 2);
    assert!(owned.iter().all(|t| t.role == TeamRole::Owner));
    assert!(matches!(
        store.close_team(&owner.secret, id, 2).await,
        Err(E::Forbidden)
    ));
    let live = store
        .issue_key(
            &next.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let account = store
        .authorize(&live.secret)
        .await
        .unwrap()
        .owner_account_id;
    store.close_team(&next.secret, id, 2).await.unwrap();
    assert!(store.authorize(&live.secret).await.is_err());
    assert!(matches!(
        store.create_team(&owner.secret).await,
        Err(E::Conflict)
    ));
    assert_eq!(
        store.list_teams(&next.secret, 0).await.unwrap()[0].id,
        other
    );
    let retained: bool = sqlx::query_scalar(
        "SELECT EXISTS(SELECT 1 FROM core_identity.accounts WHERE id=$1 AND NOT active)",
    )
    .bind(account)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(retained);
    assert!(
        sqlx::query("UPDATE core_identity.teams SET created_by_user_id=3 WHERE id=$1")
            .bind(id)
            .execute(&pool)
            .await
            .is_err()
    );
    assert!(
        sqlx::query("DELETE FROM core_identity.teams WHERE id=$1")
            .bind(id)
            .execute(&pool)
            .await
            .is_err()
    );
}

#[sqlx::test(migrations = false)]
async fn failed_transfer_audit_rolls_back_owner_memberships_and_epoch(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let next = store.bootstrap_user(2, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    join(
        &store,
        &owner.secret,
        id,
        &next.secret,
        2,
        TeamRole::Member,
        true,
    )
    .await;
    sqlx::raw_sql("CREATE FUNCTION core_identity.reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$; CREATE TRIGGER reject_audit BEFORE INSERT ON core_identity.audit FOR EACH ROW EXECUTE FUNCTION core_identity.reject_audit();").execute(&pool).await.unwrap();
    assert!(matches!(
        store.transfer_team(&owner.secret, id, 2, 1).await,
        Err(E::Storage)
    ));
    let teams = store.list_teams(&owner.secret, 0).await.unwrap();
    assert_eq!(teams[0].role, TeamRole::Owner);
    assert_eq!(teams[0].version, 1);
    let members = store.list_members(&owner.secret, id, 0).await.unwrap();
    assert_eq!(members.len(), 2);
    assert_eq!(members[1].membership_version, 1);
}

async fn call(
    app: &Router,
    method: &str,
    path: &str,
    bearer: Option<&str>,
    body: Option<Value>,
) -> axum::response::Response {
    let mut request = Request::builder().method(method).uri(path);
    if let Some(token) = bearer {
        request = request.header("authorization", format!("Bearer {token}"));
    }
    let body = match body {
        Some(b) => {
            request = request.header("content-type", "application/json");
            Body::from(b.to_string())
        }
        None => Body::empty(),
    };
    app.clone()
        .oneshot(request.body(body).unwrap())
        .await
        .unwrap()
}
async fn body(response: axum::response::Response) -> Value {
    serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap()
}
#[sqlx::test(migrations = false)]
async fn public_http_registration_session_and_team_lifecycle_need_no_go_service(pool: PgPool) {
    let store = fresh(pool).await;
    let app = identity_http::router(store.clone());
    let rejected=call(&app,"POST","/core/v1/auth/register",None,Some(json!({"login_name":"alice","password":"a real test passphrase","platform_role":"superadmin"}))).await;
    assert_eq!(rejected.status(), StatusCode::UNPROCESSABLE_ENTITY);
    assert_eq!(rejected.headers()["cache-control"], "no-store");
    let registered = call(
        &app,
        "POST",
        "/core/v1/auth/register",
        None,
        Some(json!({"login_name":"alice","password":"a real test passphrase"})),
    )
    .await;
    assert_eq!(registered.status(), StatusCode::CREATED);
    let registered = body(registered).await;
    let token = registered["secret"].as_str().unwrap();
    let created = call(&app, "POST", "/core/v1/teams", Some(token), None).await;
    assert_eq!(created.status(), StatusCode::CREATED);
    let id = body(created).await["id"].as_i64().unwrap();
    let members = call(
        &app,
        "GET",
        &format!("/core/v1/teams/{id}/members"),
        Some(token),
        None,
    )
    .await;
    assert_eq!(members.status(), StatusCode::OK);
    assert_eq!(body(members).await[0]["role"], "owner");
    let renamed = call(
        &app,
        "PATCH",
        &format!("/core/v1/teams/{id}"),
        Some(token),
        Some(json!({"name":"Test team","expected_version":1})),
    )
    .await;
    assert_eq!(renamed.status(), StatusCode::NO_CONTENT);
    let closed = call(
        &app,
        "DELETE",
        &format!("/core/v1/teams/{id}?version=2"),
        Some(token),
        None,
    )
    .await;
    assert_eq!(closed.status(), StatusCode::NO_CONTENT);
    let refreshed = call(
        &app,
        "POST",
        "/core/v1/auth/session/refresh",
        Some(token),
        None,
    )
    .await;
    assert_eq!(refreshed.status(), StatusCode::OK);
    let renewed = body(refreshed).await;
    assert_eq!(
        call(&app, "GET", "/core/v1/identity", Some(token), None)
            .await
            .status(),
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(
        call(
            &app,
            "POST",
            "/core/v1/auth/logout",
            Some(renewed["secret"].as_str().unwrap()),
            None
        )
        .await
        .status(),
        StatusCode::NO_CONTENT
    );
    let no_provider = call(&app, "GET", "/core/v1/auth/oauth/google", None, None).await;
    assert_eq!(no_provider.status(), StatusCode::FORBIDDEN);
}
#[sqlx::test(migrations = false)]
async fn registration_is_explicit_and_forwarded_headers_cannot_bypass_auth_limits(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    store.init_database().await.unwrap();
    assert!(matches!(
        store.register(&login("alice")).await,
        Err(E::Forbidden)
    ));
    let app = identity_http::router(store);
    for n in 0..61 {
        let request = Request::builder()
            .method("POST")
            .uri("/core/v1/auth/login")
            .header("x-forwarded-for", format!("203.0.113.{n}"))
            .body(Body::empty())
            .unwrap();
        let response = app.clone().oneshot(request).await.unwrap();
        if n == 60 {
            assert_eq!(response.status(), StatusCode::TOO_MANY_REQUESTS);
            assert_eq!(response.headers()["retry-after"], "60");
        }
    }
}

#[sqlx::test(migrations = false)]
async fn session_rotation_preserves_pending_invites_but_explicit_revocation_does_not(pool: PgPool) {
    let store = fresh(pool.clone()).await;
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let recipient = store.bootstrap_user(2, 1).await.unwrap();
    let other = store.bootstrap_user(3, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let keep = store
        .invite(&owner.secret, id, &invitation(2, TeamRole::Member, true))
        .await
        .unwrap();
    let invalidate = store
        .invite(&owner.secret, id, &invitation(3, TeamRole::Member, true))
        .await
        .unwrap();
    let rotated = store.refresh_session(&owner.secret).await.unwrap();
    store
        .accept_invite(&recipient.secret, &keep.secret)
        .await
        .unwrap();
    store.revoke(&rotated.secret, rotated.id).await.unwrap();
    assert!(matches!(
        store.accept_invite(&other.secret, &invalidate.secret).await,
        Err(E::Forbidden)
    ));
    assert!(sqlx::query("UPDATE core_identity.credentials SET expires_at=expires_at+interval '1 second' WHERE id=$1").bind(rotated.id).execute(&pool).await.is_err());
    sqlx::query("UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=2")
        .execute(&pool)
        .await
        .unwrap();
    assert!(store.authorize(&recipient.secret).await.is_err());
    assert!(
        sqlx::query("UPDATE core_identity.users SET auth_version=1 WHERE id=2")
            .execute(&pool)
            .await
            .is_err()
    );
}
