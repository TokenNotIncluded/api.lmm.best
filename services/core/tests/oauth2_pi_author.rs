//! Author PG/TCP cases, UNRUN until host compiles and independent tester runs.
use lmm_core::{
    billing::charging::{Budget, Error, Outcome, Usage},
    identity::{IdentityError, grants::ReadError},
};
use serde_json::json;
use sqlx::PgPool;
use std::sync::atomic::Ordering;
// This shared fixture also serves protocol/process tests with additional fields.
#[allow(dead_code)]
#[path = "common/model_fixture.rs"]
mod model_fixture;
#[path = "common/pi_oauth_fixture.rs"]
mod pi;
// This shared fixture also serves protocol/process tests with additional fields.
#[allow(dead_code)]
#[path = "common/runtime.rs"]
mod runtime;
use pi::with_fixture;

#[sqlx::test(migrations = false)]
async fn real_pi_browser_consent_is_bounded_without_issuing_native_keys(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let before: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_identity.credentials WHERE kind='api_key'",
        )
        .fetch_one(&f.owner)
        .await
        .unwrap();
        for cap in [None, Some(0), Some(1001)] {
            let first = f.issuer.begin(f.request()).await.unwrap();
            let page = f
                .issuer
                .continue_authorization(&first.binding, &first.csrf, Some(&f.refresh))
                .await
                .unwrap();
            assert!(matches!(
                f.issuer
                    .consent_with_budget(&page.binding, &page.csrf, Some(&f.refresh), true, cap)
                    .await,
                Err(IdentityError::Invalid)
            ));
        }
        let token = f.token(100).await;
        for sql in [
            "UPDATE core_oauth.access_tokens SET expires_at=expires_at WHERE false",
            "DELETE FROM core_oauth.access_tokens WHERE false",
        ] {
            let error = sqlx::query(sql).execute(&f.role.pool).await.unwrap_err();
            assert_eq!(
                error.as_database_error().and_then(|e| e.code()).as_deref(),
                Some("42501")
            );
        }

        for required in [
            "catalog:read",
            "balance:read",
            "usage:read",
            "models:invoke",
            "group:YWxsb3dlZA",
        ] {
            assert!(token.scope.split(' ').any(|v| v == required));
        }
        assert_eq!(token.scope.split(' ').count(), 5);
        assert!(!token.scope.contains("mcp:"));
        assert!(!token.scope.contains("remote:"));
        let after: i64 = sqlx::query_scalar(
            "SELECT count(*) FROM core_identity.credentials WHERE kind='api_key'",
        )
        .fetch_one(&f.owner)
        .await
        .unwrap();
        assert_eq!(after, before);
        assert_eq!(
            f.issuer
                .catalog(&token.access_token)
                .await
                .unwrap()
                .models
                .len(),
            1
        );
        assert!(matches!(
            f.refresh(
                &token.refresh_token,
                Some(format!("{} remote:control", token.scope))
            )
            .await,
            Err(IdentityError::Invalid) | Err(IdentityError::Forbidden)
        ));
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
    })
    .await;
}
#[sqlx::test(migrations = false)]
async fn l0_unmarked_cannot_self_grant_pi_developer_or_spending(pool: PgPool) {
    with_fixture(pool,false,|f|async move{
        let first=f.issuer.begin(f.request()).await.unwrap();
        assert!(matches!(f.issuer.continue_authorization(&first.binding,&first.csrf,Some(&f.refresh)).await,Err(IdentityError::Forbidden)));
        let count:i64=sqlx::query_scalar("SELECT count(*) FROM core_oauth.families WHERE client_id='lmm-pi'").fetch_one(&f.owner).await.unwrap();assert_eq!(count,0);
        let granted:bool=sqlx::query_scalar("SELECT EXISTS(SELECT FROM core_identity.developer_access WHERE user_id=$1 AND granted)").bind(f.user).fetch_one(&f.owner).await.unwrap();assert!(!granted);
        sqlx::query("UPDATE core_identity.users SET platform_role='admin' WHERE id=$1").bind(f.user).execute(&f.owner).await.unwrap();
        let browser=lmm_core::identity::BrowserAuth::new(lmm_core::identity::IdentityStore::from_pool(f.role.pool.clone()),&[43;32]).unwrap();
        let admin=browser.login("pi-alice","test passphrase",&Default::default()).await.unwrap();let first=f.issuer.begin(f.request()).await.unwrap();
        assert!(matches!(f.issuer.continue_authorization(&first.binding,&first.csrf,Some(&admin.refresh_token)).await,Err(IdentityError::Forbidden)));
        assert_eq!(f.provider.hits.load(Ordering::SeqCst),0);assert_eq!(f.ledger.balance(f.account).await.unwrap().available_units,1000);
    }).await;
}
#[sqlx::test(migrations = false)]
async fn family_cap_serializes_pending_and_committed_spending_across_refresh(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let first = f.token(70).await;
        let next = f.refresh(&first.refresh_token, None).await.unwrap();
        let a = f.spec("pi-cap-a", 50);
        let b = f.spec("pi-cap-b", 50);
        let (a, b) = tokio::join!(
            f.reserve(&first.access_token, &a, &[1; 32]),
            f.reserve(&next.access_token, &b, &[2; 32])
        );
        assert_eq!(usize::from(a.is_ok()) + usize::from(b.is_ok()), 1);
        let (charge, worker) = if let Ok(a) = a {
            assert!(matches!(b, Err(Error::BudgetExceeded)));
            (a, [1; 32])
        } else {
            assert!(matches!(a, Err(Error::BudgetExceeded)));
            (b.unwrap(), [2; 32])
        };
        assert!(charge.key_id.is_none());
        assert!(charge.oauth_source.is_some());
        assert_eq!(charge.payer_account_id, f.account);
        assert_eq!(charge.actor_user_id, f.user);
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().reserved_units,
            50
        );
        assert!(
            f.charging
                .start_oauth(&f.issuer, &charge.id, &worker)
                .await
                .unwrap()
        );
        f.charging
            .settle(
                &charge.id,
                &worker,
                Usage {
                    input: 40,
                    ..Default::default()
                },
                Outcome::Completed,
            )
            .await
            .unwrap();
        assert!(matches!(
            f.reserve(&next.access_token, &f.spec("pi-over-cap", 31), &[3; 32])
                .await,
            Err(Error::BudgetExceeded)
        ));
        f.charging
            .refund(
                &charge.id,
                "pi-refund",
                10,
                "synthetic verified provider refund",
            )
            .await
            .unwrap();
        let exact = f
            .reserve(&next.access_token, &f.spec("pi-after-refund", 40), &[4; 32])
            .await
            .unwrap();
        f.charging.release(&exact.id, &[4; 32]).await.unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            970
        );
        assert_eq!(f.provider.hits.load(Ordering::SeqCst), 0);
        let usage = f.issuer.usage(&next.access_token).await.unwrap();
        assert_eq!(usage["usage_scope"], "oauth_family");
        assert_eq!(usage["summary"]["net_consumption_credits"], 30);
    })
    .await;
}
#[sqlx::test(migrations = false)]
async fn revoke_blocks_new_start_increase_but_preserves_old_hold_settle_refund(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let token = f.token(200).await;
        let started = f
            .reserve(&token.access_token, &f.spec("pi-started", 60), &[1; 32])
            .await
            .unwrap();
        let pending = f
            .reserve(&token.access_token, &f.spec("pi-pending", 50), &[2; 32])
            .await
            .unwrap();
        assert!(
            f.charging
                .start_oauth(&f.issuer, &started.id, &[1; 32])
                .await
                .unwrap()
        );
        f.issuer
            .revoke_for_client("lmm-pi", &token.access_token)
            .await
            .unwrap();
        assert!(matches!(
            f.charging
                .start_oauth(&f.issuer, &pending.id, &[2; 32])
                .await,
            Err(Error::Unauthorized) | Err(Error::Forbidden)
        ));
        assert!(matches!(
            f.charging
                .increase_oauth_with_context(
                    &f.issuer,
                    &token.access_token,
                    &started.id,
                    &[1; 32],
                    70,
                    None
                )
                .await,
            Err(Error::Unauthorized) | Err(Error::Forbidden)
        ));
        let settled = f
            .charging
            .settle(
                &started.id,
                &[1; 32],
                Usage {
                    input: 40,
                    ..Default::default()
                },
                Outcome::Completed,
            )
            .await
            .unwrap();
        assert_eq!(settled.settled, 40);
        f.charging
            .refund(
                &started.id,
                "pi-old-refund",
                5,
                "synthetic original provider refund",
            )
            .await
            .unwrap();
        f.charging
            .refund(
                &started.id,
                "pi-old-refund",
                5,
                "synthetic original provider refund",
            )
            .await
            .unwrap();
        f.charging.release(&pending.id, &[2; 32]).await.unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            965
        );
        assert_eq!(f.ledger.balance(f.account).await.unwrap().reserved_units, 0);
        assert!(
            f.charging
                .resolve(&started.id)
                .await
                .unwrap()
                .unwrap()
                .oauth_source
                .is_some()
        );
        assert!(matches!(
            f.issuer.usage(&token.access_token).await,
            Err(ReadError::Identity(IdentityError::Unauthorized))
        ));
    })
    .await;
}
#[sqlx::test(migrations = false)]
async fn global_self_budget_still_constrains_oauth_and_native_history(pool: PgPool) {
    with_fixture(pool, true, |f| async move {
        let browser = lmm_core::identity::BrowserAuth::new(
            lmm_core::identity::IdentityStore::from_pool(f.role.pool.clone()),
            &[43; 32],
        )
        .unwrap();
        let session = browser
            .login("pi-alice", "test passphrase", &Default::default())
            .await
            .unwrap();
        f.charging
            .set_budget(
                session.data["access_token"].as_str().unwrap(),
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
        let token = f.token(200).await;
        let first = f
            .reserve(&token.access_token, &f.spec("pi-self-first", 50), &[1; 32])
            .await
            .unwrap();
        assert!(matches!(
            f.reserve(&token.access_token, &f.spec("pi-self-over", 11), &[2; 32])
                .await,
            Err(Error::BudgetExceeded)
        ));
        f.charging.release(&first.id, &[1; 32]).await.unwrap();
        assert_eq!(
            f.ledger.balance(f.account).await.unwrap().available_units,
            1000
        );
    })
    .await;
}
#[sqlx::test(migrations = false)]
async fn actual_core_tcp_pi_chat_responses_json_stream_and_native_control(pool: PgPool) {
    with_fixture(pool,true,|f|async move{
        let token=f.token(1000).await;
        for (index,(path,stream)) in [("/v1/chat/completions",false),("/v1/responses",false),("/v1/chat/completions",true),("/v1/responses",true)].into_iter().enumerate(){
            let body=if path.ends_with("responses"){json!({"model":"provider-model","input":"hello","max_output_tokens":20,"stream":stream})}else{json!({"model":"provider-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20,"stream":stream})};
            let response=f.client.post(format!("{}{path}",f.base)).bearer_auth(&token.access_token).header("x-lmm-group","YWxsb3dlZA").header("idempotency-key",format!("pi-http-{index}")).json(&body).send().await.unwrap();assert_eq!(response.status(),reqwest::StatusCode::OK);let body=response.text().await.unwrap();assert!(body.contains(if stream{"local stream answer"}else{"local synthetic answer"}));
        }
        assert_eq!(f.provider.hits.load(Ordering::SeqCst),4);
        let rows:Vec<(Option<i64>,Option<serde_json::Value>,i64,i64)>=sqlx::query_as("SELECT key_id,oauth_source,settled,payer_account_id FROM core_charging.requests ORDER BY id").fetch_all(&f.owner).await.unwrap();assert_eq!(rows.len(),4);for(key,source,settled,payer)in rows{assert!(key.is_none());assert!(source.is_some());assert_eq!(settled,49);assert_eq!(payer,f.account);}
        assert_eq!(f.ledger.balance(f.account).await.unwrap().available_units,804);
        let response=f.client.post(format!("{}/v1/chat/completions",f.base)).bearer_auth(&token.access_token).header("x-lmm-group","cHJpdmF0ZQ").json(&json!({"model":"provider-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20})).send().await.unwrap();assert_eq!(response.status(),reqwest::StatusCode::FORBIDDEN);assert_eq!(f.provider.hits.load(Ordering::SeqCst),4);
        let response=f.client.post(format!("{}/v1/chat/completions",f.base)).bearer_auth(&f.native_key).json(&json!({"model":"provider-model","messages":[{"role":"user","content":"hello"}],"max_tokens":20})).send().await.unwrap();assert_eq!(response.status(),reqwest::StatusCode::OK);let _=response.bytes().await.unwrap();assert_eq!(f.provider.hits.load(Ordering::SeqCst),5);assert_eq!(f.ledger.balance(f.account).await.unwrap().available_units,804);
    }).await;
}

#[sqlx::test(migrations = false)]
async fn malformed_double_or_foreign_oauth_source_cannot_enter_financial_records(pool: PgPool) {
    with_fixture(pool,true,|f|async move{
        let token=f.token(200).await;let charge=f.reserve(&token.access_token,&f.spec("pi-source-original",50),&[1;32]).await.unwrap();
        let proof=serde_json::to_value(charge.oauth_source.as_ref().unwrap()).unwrap();
        let other=f.token(200).await;let other_charge=f.reserve(&other.access_token,&f.spec("pi-source-other",50),&[2;32]).await.unwrap();let other_proof=serde_json::to_value(other_charge.oauth_source.as_ref().unwrap()).unwrap();
        let mut wrong_cap=proof.clone();wrong_cap["budget_credits"]=json!(199);
        let mut wrong_access=proof.clone();wrong_access["access_id"]=other_proof["access_id"].clone();
        let mut wrong_issuer=proof.clone();wrong_issuer["issuer"]=json!("https://other.example.test");wrong_issuer["resource"]=json!("https://other.example.test/api/oauth2");
        let mut null_issuer=proof.clone();null_issuer["issuer"]=serde_json::Value::Null;
        let mut no_scope=proof.clone();no_scope["scopes"]=json!(["balance:read","group:YWxsb3dlZA"]);
        let native_id:i64=sqlx::query_scalar("SELECT id FROM core_identity.credentials WHERE kind='api_key' ORDER BY id LIMIT 1").fetch_one(&f.owner).await.unwrap();
        const COPY_SOURCE_ROW:&str="INSERT INTO core_charging.requests(id,actor_user_id,key_id,oauth_source,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,state,reserved) SELECT $2,actor_user_id,$3,$4,owner_account_id,payer_account_id,fingerprint,worker_digest,spec_digest,price,authorized_at,lease_until,hard_deadline,'reserved',1 FROM core_charging.requests WHERE id=$1";
        let mut control=f.owner.begin().await.unwrap();sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE").execute(&mut *control).await.unwrap();
        let valid=sqlx::query(COPY_SOURCE_ROW).bind(&charge.id).bind("pi-valid-control").bind(None::<i64>).bind(Some(proof.clone())).execute(&mut *control).await.unwrap_or_else(|_|panic!("valid source control failed"));assert_eq!(valid.rows_affected(),1);control.rollback().await.unwrap();
        for (index,(key,source)) in [(None,None),(Some(native_id),Some(proof.clone())),(None,Some(json!({}))),(None,Some(wrong_cap)),(None,Some(wrong_access)),(None,Some(wrong_issuer)),(None,Some(null_issuer)),(None,Some(no_scope))].into_iter().enumerate(){
            let mut tx=f.owner.begin().await.unwrap();sqlx::query("SET TRANSACTION ISOLATION LEVEL SERIALIZABLE").execute(&mut *tx).await.unwrap();
            let result=sqlx::query(COPY_SOURCE_ROW)
                .bind(&charge.id).bind(format!("pi-invalid-source-{index}")).bind(key).bind(source).execute(&mut *tx).await;
            let error=result.expect_err("invalid financial source was accepted");let code=error.as_database_error().and_then(|e|e.code());assert!(matches!(code.as_deref(),Some("23503"|"23514")),"negative oracle failed for unrelated SQL error");tx.rollback().await.unwrap();
        }
        f.charging.release(&charge.id,&[1;32]).await.unwrap();f.charging.release(&other_charge.id,&[2;32]).await.unwrap();assert_eq!(f.ledger.balance(f.account).await.unwrap().available_units,1000);
        let count:i64=sqlx::query_scalar("SELECT count(*) FROM core_charging.requests WHERE id LIKE 'pi-invalid-source-%'").fetch_one(&f.owner).await.unwrap();assert_eq!(count,0);assert_eq!(f.provider.hits.load(Ordering::SeqCst),0);
    }).await;
}
