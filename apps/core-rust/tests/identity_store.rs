use lmm_core::{
    accounts::{Account, AccountKind, TeamRole},
    funding::FundingPolicy,
    identity::{IdentityError as E, IdentityStore, InviteRequest},
};
use sqlx::PgPool;

fn personal(id: i64) -> Account {
    Account {
        kind: AccountKind::Personal,
        id,
    }
}
fn team(id: i64) -> Account {
    Account {
        kind: AccountKind::Team,
        id,
    }
}
fn request(user: i64, role: TeamRole, spend: bool) -> InviteRequest {
    InviteRequest {
        recipient_user_id: user,
        role,
        can_spend: spend,
        ttl_seconds: 3600,
    }
}

#[sqlx::test]
async fn persisted_keys_are_hashed_scoped_and_revocable(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let outsider = store.bootstrap_user(2, 6).await.unwrap();
    let key = store
        .issue_key(
            &owner.secret,
            personal(1),
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    let persisted: String = sqlx::query_scalar(
        "SELECT row_to_json(c)::text FROM core_identity.credentials c WHERE id=$1",
    )
    .bind(key.id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert!(!persisted.contains(&key.secret));
    let length: i32 = sqlx::query_scalar(
        "SELECT octet_length(digest) FROM core_identity.credentials WHERE id=$1",
    )
    .bind(key.id)
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(length, 32);
    let reopened = IdentityStore::from_pool(pool.clone());
    let actor = reopened.authorize(&key.secret).await.unwrap();
    assert_eq!(actor.owner, personal(1));
    assert_eq!(actor.funding_accounts, vec![personal(1)]);
    assert!(matches!(
        store.create_team(&key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.revoke(&outsider.secret, key.id).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store
            .issue_key(
                &outsider.secret,
                personal(1),
                FundingPolicy::new_personal_key(),
                3600
            )
            .await,
        Err(E::Forbidden)
    ));
    store.revoke(&owner.secret, key.id).await.unwrap();
    store.revoke(&owner.secret, key.id).await.unwrap();
    assert!(matches!(
        reopened.authorize(&key.secret).await,
        Err(E::Unauthorized)
    ));
    let audit: Vec<String> = sqlx::query_scalar("SELECT target::text FROM core_identity.audit")
        .fetch_all(&pool)
        .await
        .unwrap();
    assert!(
        audit
            .iter()
            .all(|a| !a.contains(&key.secret) && !a.contains(&owner.secret))
    );
}

#[sqlx::test]
async fn platform_roles_and_team_management_do_not_grant_spending(pool: PgPool) {
    let store = IdentityStore::from_pool(pool);
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let admin = store.bootstrap_user(2, 6).await.unwrap();
    let member = store.bootstrap_user(3, 0).await.unwrap();
    store.bootstrap_user(4, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    assert!(matches!(
        store.create_team(&owner.secret).await,
        Err(E::Conflict)
    ));
    assert!(matches!(
        store
            .issue_key(
                &admin.secret,
                team(id),
                FundingPolicy::inherit_account_settings(),
                3600
            )
            .await,
        Err(E::Forbidden)
    ));
    let invite = store
        .invite(&owner.secret, id, &request(2, TeamRole::Admin, false))
        .await
        .unwrap();
    store
        .accept_invite(&admin.secret, &invite.secret)
        .await
        .unwrap();
    assert!(matches!(
        store
            .issue_key(
                &admin.secret,
                team(id),
                FundingPolicy::inherit_account_settings(),
                3600
            )
            .await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store
            .invite(&admin.secret, id, &request(4, TeamRole::Admin, false))
            .await,
        Err(E::Forbidden)
    ));
    let invite = store
        .invite(&admin.secret, id, &request(3, TeamRole::Member, true))
        .await
        .unwrap();
    store
        .accept_invite(&member.secret, &invite.secret)
        .await
        .unwrap();
    let key = store
        .issue_key(
            &member.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let actor = store.authorize(&key.secret).await.unwrap();
    assert_eq!(actor.platform_level, 0);
    assert_eq!(actor.user_id, 3);
    assert_eq!(actor.owner, team(id));
    assert_eq!(actor.funding_accounts, vec![team(id)]);
    let mut policy = FundingPolicy::inherit_account_settings();
    policy.account_order = Some(vec![team(id), personal(3)]);
    assert!(matches!(
        store
            .issue_key(&member.secret, team(id), policy, 3600)
            .await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.remove_member(&member.secret, id, 2, 1).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.remove_member(&owner.secret, id, 1, 1).await,
        Err(E::Forbidden)
    ));
}

#[sqlx::test]
async fn removal_and_rejoin_cannot_revive_old_keys_or_old_invites(pool: PgPool) {
    let store = IdentityStore::from_pool(pool);
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let member = store.bootstrap_user(2, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let first = store
        .invite(&owner.secret, id, &request(2, TeamRole::Member, true))
        .await
        .unwrap();
    let stale = store
        .invite(&owner.secret, id, &request(2, TeamRole::Member, true))
        .await
        .unwrap();
    store
        .accept_invite(&member.secret, &first.secret)
        .await
        .unwrap();
    let team_key = store
        .issue_key(
            &member.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    let mut policy = FundingPolicy::new_personal_key();
    policy.account_order = Some(vec![team(id), personal(2)]);
    let personal_key = store
        .issue_key(&member.secret, personal(2), policy, 3600)
        .await
        .unwrap();
    assert_eq!(
        store
            .authorize(&personal_key.secret)
            .await
            .unwrap()
            .funding_accounts,
        vec![team(id), personal(2)]
    );
    assert!(matches!(
        store.remove_member(&owner.secret, id, 2, 2).await,
        Err(E::Conflict)
    ));
    store.remove_member(&owner.secret, id, 2, 1).await.unwrap();
    assert!(matches!(
        store.authorize(&team_key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.authorize(&personal_key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.accept_invite(&member.secret, &stale.secret).await,
        Err(E::Conflict)
    ));
    let new_invite = store
        .invite(&owner.secret, id, &request(2, TeamRole::Member, true))
        .await
        .unwrap();
    store
        .accept_invite(&member.secret, &new_invite.secret)
        .await
        .unwrap();
    assert_eq!(
        store.list_teams(&member.secret, 0).await.unwrap()[0].membership_version,
        3
    );
    assert!(matches!(
        store.authorize(&team_key.secret).await,
        Err(E::Forbidden)
    ));
    assert!(matches!(
        store.authorize(&personal_key.secret).await,
        Err(E::Forbidden)
    ));
    let new_key = store
        .issue_key(
            &member.secret,
            team(id),
            FundingPolicy::inherit_account_settings(),
            3600,
        )
        .await
        .unwrap();
    assert!(store.authorize(&new_key.secret).await.is_ok());
}

#[sqlx::test]
async fn concurrent_invite_acceptance_has_one_result_and_one_audit(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let member = store.bootstrap_user(2, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let invite = store
        .invite(&owner.secret, id, &request(2, TeamRole::Member, true))
        .await
        .unwrap();
    let (a, b) = tokio::join!(
        store.accept_invite(&member.secret, &invite.secret),
        store.accept_invite(&member.secret, &invite.secret)
    );
    assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
    assert!(matches!(a, Err(E::Conflict)) || matches!(b, Err(E::Conflict)));
    let count: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_identity.memberships WHERE active")
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!(count, 1);
    let audit: i64 = sqlx::query_scalar(
        "SELECT count(*) FROM core_identity.audit WHERE action='team.invite.accept'",
    )
    .fetch_one(&pool)
    .await
    .unwrap();
    assert_eq!(audit, 1);
}

#[sqlx::test]
async fn revoked_inviter_session_and_removed_admin_invalidate_pending_invites(pool: PgPool) {
    let store = IdentityStore::from_pool(pool);
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let admin = store.bootstrap_user(2, 1).await.unwrap();
    let third = store.bootstrap_user(3, 1).await.unwrap();
    let fourth = store.bootstrap_user(4, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let invite = store
        .invite(&owner.secret, id, &request(2, TeamRole::Admin, true))
        .await
        .unwrap();
    store
        .accept_invite(&admin.secret, &invite.secret)
        .await
        .unwrap();
    let by_admin = store
        .invite(&admin.secret, id, &request(3, TeamRole::Member, true))
        .await
        .unwrap();
    let by_owner = store
        .invite(&owner.secret, id, &request(4, TeamRole::Member, true))
        .await
        .unwrap();
    store.remove_member(&owner.secret, id, 2, 1).await.unwrap();
    assert!(matches!(
        store.accept_invite(&third.secret, &by_admin.secret).await,
        Err(E::Forbidden)
    ));
    store.revoke(&owner.secret, owner.id).await.unwrap();
    assert!(matches!(
        store.accept_invite(&fourth.secret, &by_owner.secret).await,
        Err(E::Forbidden)
    ));
}

#[sqlx::test]
async fn audit_failure_rolls_back_membership_invite_consumption_and_key_issuance(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    let member = store.bootstrap_user(2, 1).await.unwrap();
    let id = store.create_team(&owner.secret).await.unwrap();
    let invite = store
        .invite(&owner.secret, id, &request(2, TeamRole::Member, true))
        .await
        .unwrap();
    sqlx::raw_sql("CREATE FUNCTION core_identity.reject_audit() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'test audit failure'; END $$; CREATE TRIGGER reject_audit BEFORE INSERT ON core_identity.audit FOR EACH ROW EXECUTE FUNCTION core_identity.reject_audit();").execute(&pool).await.unwrap();
    assert!(matches!(
        store.accept_invite(&member.secret, &invite.secret).await,
        Err(E::Storage)
    ));
    assert!(matches!(
        store
            .issue_key(
                &owner.secret,
                personal(1),
                FundingPolicy::new_personal_key(),
                3600
            )
            .await,
        Err(E::Storage)
    ));
    let members: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.memberships")
        .fetch_one(&pool)
        .await
        .unwrap();
    let keys: i64 =
        sqlx::query_scalar("SELECT count(*) FROM core_identity.credentials WHERE kind='api_key'")
            .fetch_one(&pool)
            .await
            .unwrap();
    let pending: bool =
        sqlx::query_scalar("SELECT accepted_at IS NULL FROM core_identity.invites WHERE id=$1")
            .bind(invite.id)
            .fetch_one(&pool)
            .await
            .unwrap();
    assert_eq!((members, keys, pending), (0, 0, true));
    sqlx::query("DROP TRIGGER reject_audit ON core_identity.audit")
        .execute(&pool)
        .await
        .unwrap();
    store
        .accept_invite(&member.secret, &invite.secret)
        .await
        .unwrap();
}

#[sqlx::test]
async fn expiry_disabling_and_user_epoch_are_checked_without_a_cache(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone());
    let owner = store.bootstrap_user(1, 1).await.unwrap();
    assert!(matches!(store.bootstrap_user(1, 6).await, Err(E::Conflict)));
    let key = store
        .issue_key(
            &owner.secret,
            personal(1),
            FundingPolicy::new_personal_key(),
            3600,
        )
        .await
        .unwrap();
    sqlx::query("UPDATE core_identity.credentials SET expires_at=clock_timestamp()-interval '1 second' WHERE id=$1").bind(key.id).execute(&pool).await.unwrap();
    assert!(matches!(
        store.authorize(&key.secret).await,
        Err(E::Unauthorized)
    ));
    sqlx::query("UPDATE core_identity.users SET active=FALSE WHERE id=1")
        .execute(&pool)
        .await
        .unwrap();
    assert!(matches!(
        store.authorize(&owner.secret).await,
        Err(E::Unauthorized)
    ));
    sqlx::query(
        "UPDATE core_identity.users SET active=TRUE,auth_version=auth_version+1 WHERE id=1",
    )
    .execute(&pool)
    .await
    .unwrap();
    assert!(matches!(
        store.authorize(&owner.secret).await,
        Err(E::Unauthorized)
    ));
}
