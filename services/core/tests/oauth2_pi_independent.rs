//! Independently authored negative and financial oracles; disposable PG only.
use lmm_core::{
    billing::charging::{Error, Outcome, Usage},
    identity::IdentityError,
};
use serde_json::json;
use sqlx::PgPool;
use std::{sync::atomic::Ordering, time::Duration};
#[allow(dead_code)]
#[path = "common/model_fixture.rs"]
mod model_fixture;
#[path = "common/pi_oauth_fixture.rs"]
mod pi;
#[allow(dead_code)]
#[path = "common/runtime.rs"]
mod runtime;
use pi::with_fixture;

fn denied<T: std::fmt::Debug>(r: Result<T, Error>) {
    assert!(
        matches!(r, Err(Error::Unauthorized | Error::Forbidden)),
        "unexpected result: {r:?}"
    );
}

#[sqlx::test(migrations = false)]
async fn revoker_first_financial_waiters_never_create_new_cost(pool: PgPool) {
    with_fixture(pool,true,|f|async move {
        for operation in 0..3 {
            let token=f.token(200).await;
            let spec=f.spec(&format!("independent-revoke-{operation}"),40);
            let pending=if operation>0 {Some(f.reserve(&token.access_token,&spec,&[1;32]).await.unwrap())}else{None};
            let family=if let Some(c)=&pending {c.oauth_source.as_ref().unwrap().grant_id().to_owned()}else{
                sqlx::query_scalar("SELECT id FROM core_oauth.families ORDER BY created_at DESC LIMIT 1").fetch_one(&f.owner).await.unwrap()
            };
            let mut revoker=f.owner.begin().await.unwrap();
            sqlx::query("UPDATE core_oauth.families SET revoked_at=clock_timestamp() WHERE id=$1").bind(&family).execute(&mut *revoker).await.unwrap();
            let clone=f.clone();let access=token.access_token.clone();let id=spec.id.clone();
            let mut waiter=tokio::spawn(async move {
                match operation {
                    0=>clone.reserve(&access,&clone.spec(&id,40),&[1;32]).await.map(|_|()),
                    1=>clone.charging.start_oauth(&clone.issuer,&id,&[1;32]).await.map(|_|()),
                    _=>clone.charging.increase_oauth_with_context(&clone.issuer,&access,&id,&[1;32],50,None).await.map(|_|())
                }
            });
            assert!(tokio::time::timeout(Duration::from_millis(100),&mut waiter).await.is_err(),"operation escaped locked family");
            let waiting:bool=sqlx::query_scalar("SELECT EXISTS(SELECT FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query LIKE '%core_oauth.families%')").fetch_one(&f.owner).await.unwrap();
            assert!(waiting,"no independently observed PG family lock wait");
            revoker.commit().await.unwrap();
            let result=waiter.await.unwrap();
            if matches!(result,Err(Error::Retry)) {
                match operation {
                    0=>denied(f.reserve(&token.access_token,&spec,&[1;32]).await),
                    1=>denied(f.charging.start_oauth(&f.issuer,&spec.id,&[1;32]).await),
                    _=>denied(f.charging.increase_oauth_with_context(&f.issuer,&token.access_token,&spec.id,&[1;32],50,None).await)
                }
            }else{denied(result);}
            if let Some(pending)=pending {let current=f.charging.resolve(&pending.id).await.unwrap().unwrap();assert_eq!(current.state,"reserved");assert_eq!(current.reserved,40);f.charging.release(&pending.id,&[1;32]).await.unwrap();}
            else {assert!(f.charging.resolve(&spec.id).await.unwrap().is_none());}
        }
        assert_eq!(f.provider.hits.load(Ordering::SeqCst),0);
        let balance=f.ledger.balance(f.account).await.unwrap();assert_eq!(balance.available_units,1000);assert_eq!(balance.reserved_units,0);
    }).await;
}

#[sqlx::test(migrations = false)]
async fn refresh_can_extend_same_family_without_replacing_original_source(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let first = f.token(90).await;
        let next = f.refresh(&first.refresh_token, None).await.unwrap();
        let original = f
            .reserve(
                &first.access_token,
                &f.spec("independent-original", 40),
                &[1; 32],
            )
            .await
            .unwrap();
        let foreign = f.token(200).await;
        denied(
            f.charging
                .increase_oauth_with_context(
                    &f.issuer,
                    &foreign.access_token,
                    &original.id,
                    &[1; 32],
                    50,
                    None,
                )
                .await,
        );
        let increased = f
            .charging
            .increase_oauth_with_context(
                &f.issuer,
                &next.access_token,
                &original.id,
                &[1; 32],
                60,
                None,
            )
            .await
            .unwrap();
        assert_eq!(increased.oauth_source, original.oauth_source);
        assert_eq!(increased.payer_account_id, original.payer_account_id);
        let a_spec = f.spec("independent-cap-old", 20);
        let b_spec = f.spec("independent-cap-new", 20);
        let (a, b) = tokio::join!(
            f.reserve(&first.access_token, &a_spec, &[2; 32]),
            f.reserve(&next.access_token, &b_spec, &[3; 32])
        );
        assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
        let (winner, worker) = match (a, b) {
            (Ok(c), Err(Error::BudgetExceeded)) => (c, [2; 32]),
            (Err(Error::BudgetExceeded), Ok(c)) => (c, [3; 32]),
            other => panic!("unexpected cap outcome: {other:?}"),
        };
        assert_eq!(
            winner.oauth_source.as_ref().unwrap().grant_id(),
            original.oauth_source.as_ref().unwrap().grant_id()
        );
        f.charging.release(&winner.id, &worker).await.unwrap();
        f.charging.release(&original.id, &[1; 32]).await.unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            1000
        );
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}

#[sqlx::test(migrations = false)]
async fn disabled_user_cannot_dispatch_old_hold(pool: PgPool) {
    authority_withdrawal(pool, 0).await;
}
#[sqlx::test(migrations = false)]
async fn developer_withdrawal_cannot_dispatch_old_hold(pool: PgPool) {
    authority_withdrawal(pool, 1).await;
}
#[sqlx::test(migrations = false)]
async fn group_withdrawal_cannot_dispatch_old_hold(pool: PgPool) {
    authority_withdrawal(pool, 2).await;
}
#[sqlx::test(migrations = false)]
async fn direct_auth_epoch_change_cannot_dispatch_old_hold(pool: PgPool) {
    authority_withdrawal(pool, 3).await;
}
async fn authority_withdrawal(pool: PgPool, kind: u8) {
    with_fixture(pool, true, move |f| async move {
        let token = f.token(100).await;
        let held = f
            .reserve(
                &token.access_token,
                &f.spec(&format!("independent-withdraw-{kind}"), 30),
                &[1; 32],
            )
            .await
            .unwrap();
        match kind {
            0 => {
                sqlx::query("UPDATE core_identity.users SET active=false WHERE id=$1")
                    .bind(f.user)
                    .execute(&f.owner)
                    .await
                    .unwrap();
            }
            1 => {
                sqlx::query(
                    "UPDATE core_identity.developer_access SET granted=false WHERE user_id=$1",
                )
                .bind(f.user)
                .execute(&f.owner)
                .await
                .unwrap();
            }
            3 => {
                sqlx::query(
                    "UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=$1",
                )
                .bind(f.user)
                .execute(&f.owner)
                .await
                .unwrap();
            }
            _ => {
                sqlx::query(
                    "UPDATE core_identity.user_routing_groups SET enabled=false WHERE user_id=$1",
                )
                .bind(f.user)
                .execute(&f.owner)
                .await
                .unwrap();
            }
        }
        denied(f.charging.start_oauth(&f.issuer, &held.id, &[1; 32]).await);
        denied(
            f.reserve(
                &token.access_token,
                &f.spec(&format!("independent-denied-{kind}"), 10),
                &[2; 32],
            )
            .await,
        );
        f.charging.release(&held.id, &[1; 32]).await.unwrap();
        match kind {
            0 => {
                sqlx::query("UPDATE core_identity.users SET active=true WHERE id=$1")
                    .bind(f.user)
                    .execute(&f.owner)
                    .await
                    .unwrap();
            }
            1 => {
                sqlx::query(
                    "UPDATE core_identity.developer_access SET granted=true WHERE user_id=$1",
                )
                .bind(f.user)
                .execute(&f.owner)
                .await
                .unwrap();
            }
            3 => (),
            _ => {
                sqlx::query(
                    "UPDATE core_identity.user_routing_groups SET enabled=true WHERE user_id=$1",
                )
                .bind(f.user)
                .execute(&f.owner)
                .await
                .unwrap();
            }
        }
        // Old epoch never recovers merely because approval is restored.
        assert!(matches!(
            f.issuer.usage(&token.access_token).await,
            Err(lmm_core::identity::grants::ReadError::Identity(
                IdentityError::Unauthorized
            ))
        ));
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            1000
        );
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}

#[sqlx::test(migrations = false)]
async fn revoked_expired_stream_retains_original_hold_until_authoritative_reconciliation(
    pool: PgPool,
) {
    with_fixture(pool,true,|f|async move {
        let token=f.token(100).await;let mut spec=f.spec("independent-crash",60);spec.lease_seconds=1;spec.maximum_seconds=1;
        let held=f.reserve(&token.access_token,&spec,&[1;32]).await.unwrap();
        assert!(f.charging.start_oauth(&f.issuer,&held.id,&[1;32]).await.unwrap());
        f.issuer.revoke_for_client("lmm-pi",&token.access_token).await.unwrap();
        sqlx::query("UPDATE core_identity.users SET active=false WHERE id=$1").bind(f.user).execute(&f.owner).await.unwrap();
        tokio::time::sleep(Duration::from_secs(2)).await;
        let recovered=f.charging.recover_expired(10).await.unwrap();assert_eq!(recovered.len(),1);let recovered=&recovered[0];assert_eq!(recovered.state,"reconcile");assert_eq!(recovered.oauth_source,held.oauth_source);
        assert_eq!(f.ledger.balance(f.account).await.unwrap().reserved_units,60);
        let usage=Usage{input:25,..Default::default()};
        let settled=f.charging.reconcile(&held.id,recovered.revision,usage,Outcome::Completed,"synthetic authoritative provider receipt independent-v1").await.unwrap();
        assert_eq!(settled.settled,25);assert_eq!(settled.oauth_source,held.oauth_source);
        f.charging.refund(&held.id,"independent-refund",5,"synthetic provider refund independent-v1").await.unwrap();
        let balance=f.ledger.balance(f.account).await.unwrap();assert_eq!(balance.available_units,980);assert_eq!(balance.reserved_units,0);assert_eq!(f.provider.hits.load(Ordering::SeqCst),0);
        let source:serde_json::Value=sqlx::query_scalar("SELECT command->'oauth_source' FROM core_charging.ledger_commands WHERE request_id=$1 LIMIT 1").bind(&held.id).fetch_one(&f.owner).await.unwrap();assert_eq!(source,json!(held.oauth_source.unwrap()));
    }).await;
}

#[sqlx::test(migrations = false)]
async fn runtime_cannot_modify_or_delete_original_access_rows(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let token = f.token(100).await;
        let held = f
            .reserve(
                &token.access_token,
                &f.spec("independent-runtime-access", 30),
                &[1; 32],
            )
            .await
            .unwrap();
        denied(
            f.charging
                .increase(&f.native_key, &held.id, &[1; 32], 40)
                .await,
        );
        let id = held.oauth_source.as_ref().unwrap().access_id();
        for statement in [
            "UPDATE core_oauth.access_tokens SET scopes=scopes WHERE id=$1",
            "DELETE FROM core_oauth.access_tokens WHERE id=$1",
        ] {
            let error = sqlx::query(statement)
                .bind(id)
                .execute(&f.role.pool)
                .await
                .expect_err("runtime mutated immutable access");
            assert_eq!(
                error.as_database_error().and_then(|e| e.code()).as_deref(),
                Some("42501")
            );
        }
        assert!(
            f.charging
                .start_oauth(&f.issuer, &held.id, &[1; 32])
                .await
                .unwrap()
        );
        f.charging
            .settle(
                &held.id,
                &[1; 32],
                Usage {
                    input: 10,
                    ..Default::default()
                },
                Outcome::Completed,
            )
            .await
            .unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            990
        );
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}

#[sqlx::test(migrations = false)]
async fn global_self_cap_counts_native_settlement_and_new_oauth_families(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        use lmm_core::{
            accounts::{Account, AccountKind},
            billing::charging::Budget,
            funding::FundingPolicy,
            identity::{BrowserAuth, IdentityStore},
        };
        let store = IdentityStore::from_pool(f.role.pool.clone());
        let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
        let session = browser
            .login("pi-alice", "test passphrase", &Default::default())
            .await
            .unwrap();
        let access = session.data["access_token"].as_str().unwrap();
        let native = store
            .issue_key(
                access,
                Account {
                    kind: AccountKind::Personal,
                    id: f.user,
                },
                FundingPolicy::new_personal_key(),
                3600,
            )
            .await
            .unwrap();
        f.charging
            .set_budget(
                access,
                &Budget {
                    scope: "self".into(),
                    account_id: None,
                    user_id: Some(f.user),
                    key_id: None,
                    period: "day".into(),
                    anchor: 0,
                    seconds: 0,
                    limit: 60,
                },
            )
            .await
            .unwrap();
        let native_charge = f
            .charging
            .reserve(
                &native.secret,
                &f.spec("independent-native-history", 45),
                &[1; 32],
            )
            .await
            .unwrap();
        assert!(f.charging.start(&native_charge.id, &[1; 32]).await.unwrap());
        f.charging
            .settle(
                &native_charge.id,
                &[1; 32],
                Usage {
                    input: 40,
                    ..Default::default()
                },
                Outcome::Completed,
            )
            .await
            .unwrap();
        let first = f.token(200).await;
        let second = f.token(200).await;
        let pending = f
            .reserve(
                &first.access_token,
                &f.spec("independent-global-first", 15),
                &[2; 32],
            )
            .await
            .unwrap();
        assert!(matches!(
            f.reserve(
                &second.access_token,
                &f.spec("independent-global-other-family", 6),
                &[3; 32]
            )
            .await,
            Err(Error::BudgetExceeded)
        ));
        f.charging.release(&pending.id, &[2; 32]).await.unwrap();
        let exact = f
            .reserve(
                &second.access_token,
                &f.spec("independent-global-exact", 20),
                &[4; 32],
            )
            .await
            .unwrap();
        f.charging.release(&exact.id, &[4; 32]).await.unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            960
        );
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}

#[sqlx::test(migrations = false)]
async fn expired_original_access_cannot_start_and_refresh_extension_retains_original_source(
    pool: PgPool,
) {
    with_fixture(pool,true,|f|async move {
        let first=f.token(100).await;let next=f.refresh(&first.refresh_token,None).await.unwrap();
        let held=f.reserve(&first.access_token,&f.spec("independent-access-expiry",40),&[1;32]).await.unwrap();
        let source=held.oauth_source.as_ref().unwrap();
        // Owner-only offline expiry simulation coordinates the SAME family lock;
        // no runtime privileges or production credential changes are involved.
        let mut owner=f.owner.begin().await.unwrap();
        sqlx::query("SELECT id FROM core_oauth.families WHERE id=$1 FOR UPDATE").bind(source.grant_id()).fetch_one(&mut *owner).await.unwrap();
        sqlx::query("UPDATE core_oauth.access_tokens SET expires_at=clock_timestamp()+interval '1 second' WHERE id=$1").bind(source.access_id()).execute(&mut *owner).await.unwrap();owner.commit().await.unwrap();
        tokio::time::sleep(Duration::from_secs(2)).await;
        denied(f.charging.start_oauth(&f.issuer,&held.id,&[1;32]).await);
        denied(f.charging.increase_oauth_with_context(&f.issuer,&first.access_token,&held.id,&[1;32],50,None).await);
        let increased=f.charging.increase_oauth_with_context(&f.issuer,&next.access_token,&held.id,&[1;32],50,None).await.unwrap();
        assert_eq!(increased.oauth_source,held.oauth_source);
        denied(f.charging.start_oauth(&f.issuer,&held.id,&[1;32]).await);
        let current=f.charging.resolve(&held.id).await.unwrap().unwrap();assert_eq!(current.state,"reserved");assert_eq!(current.reserved,50);assert_eq!(current.oauth_source,held.oauth_source);
        f.charging.release(&held.id,&[1;32]).await.unwrap();assert_eq!(f.ledger.balance(f.account).await.unwrap().available_units,1000);assert_eq!(f.provider.hits.load(Ordering::SeqCst),0);
    }).await;
}

#[sqlx::test(migrations = false)]
async fn settled_oauth_refund_succeeds_but_terminal_and_original_source_mutations_fail(
    pool: PgPool,
) {
    with_fixture(pool, true, |f| async move {
        let token = f.token(200).await;
        let held = f
            .reserve(
                &token.access_token,
                &f.spec("independent-terminal", 60),
                &[1; 32],
            )
            .await
            .unwrap();
        assert!(
            f.charging
                .start_oauth(&f.issuer, &held.id, &[1; 32])
                .await
                .unwrap()
        );
        f.charging
            .settle(
                &held.id,
                &[1; 32],
                Usage {
                    input: 10,
                    ..Default::default()
                },
                Outcome::Completed,
            )
            .await
            .unwrap();
        f.charging
            .refund(
                &held.id,
                "independent-terminal-refund",
                2,
                "synthetic provider refund terminal-v1",
            )
            .await
            .unwrap();
        let before: serde_json::Value =
            sqlx::query_scalar("SELECT to_jsonb(r) FROM core_charging.requests r WHERE id=$1")
                .bind(&held.id)
                .fetch_one(&f.owner)
                .await
                .unwrap();
        for fragment in [
            "state='streaming'",
            "reserved=reserved+1",
            "lease_until=lease_until-1",
            "input_tokens=input_tokens+1",
            "cached_tokens=cached_tokens+1",
            "output_tokens=output_tokens+1",
            "settled=settled+1",
            "outcome='independent-tamper'",
            "authorized_at=authorized_at+1",
            "hard_deadline=hard_deadline+1",
            "id=id||'-changed'",
            "actor_user_id=actor_user_id+1",
            "key_id=1",
            "owner_account_id=owner_account_id+1",
            "payer_account_id=payer_account_id+1",
            "fingerprint=decode(repeat('01',32),'hex')",
            "worker_digest=decode(repeat('02',32),'hex')",
            "spec_digest=decode(repeat('03',32),'hex')",
            "price=jsonb_set(price,'{version}','\"independent-tamper\"'::jsonb)",
            "source_subscription_id=1",
            "source_start=1",
            "source_end=2",
            "refunded=refunded-1",
        ] {
            let mut tx = f.owner.begin().await.unwrap();
            sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
                .execute(&mut *tx)
                .await
                .unwrap();
            let statement = format!(
                "UPDATE core_charging.requests SET {fragment},revision=revision+1 WHERE id=$1"
            );
            let error = sqlx::query(&statement)
                .bind(&held.id)
                .execute(&mut *tx)
                .await
                .expect_err("terminal field mutation was accepted");
            let db = error.as_database_error().unwrap();
            assert_eq!(db.code().as_deref(), Some("23514"));
            assert!(
                matches!(
                    db.message(),
                    "request is terminal" | "invalid charging mutation"
                ),
                "unrelated negative oracle: {}",
                db.message()
            );
            tx.rollback().await.unwrap();
        }
        let other = f.token(200).await;
        let foreign = f
            .reserve(
                &other.access_token,
                &f.spec("independent-other-source", 10),
                &[2; 32],
            )
            .await
            .unwrap();
        let mut tx = f.owner.begin().await.unwrap();
        sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE")
            .execute(&mut *tx)
            .await
            .unwrap();
        let error = sqlx::query(
            "UPDATE core_charging.requests SET oauth_source=$2,revision=revision+1 WHERE id=$1",
        )
        .bind(&held.id)
        .bind(json!(foreign.oauth_source))
        .execute(&mut *tx)
        .await
        .expect_err("original source changed");
        assert_eq!(
            error.as_database_error().unwrap().message(),
            "invalid charging mutation"
        );
        tx.rollback().await.unwrap();
        let after: serde_json::Value =
            sqlx::query_scalar("SELECT to_jsonb(r) FROM core_charging.requests r WHERE id=$1")
                .bind(&held.id)
                .fetch_one(&f.owner)
                .await
                .unwrap();
        assert_eq!(before, after);
        f.charging.release(&foreign.id, &[2; 32]).await.unwrap();
        let balance = f.ledger.balance(f.account).await.unwrap();
        assert_eq!(balance.available_units, 992);
        assert_eq!(balance.reserved_units, 0);
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}
