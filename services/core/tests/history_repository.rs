//! Real PostgreSQL and stored credentials. The only synthetic boundary is the
//! offline A03 source-view provider, never an always-allow authorization mock.
use lmm_core::{accounts::{Account,AccountKind},core_schema,funding::FundingPolicy,
    identity::{IdentityStore,IssuedCredential},history_read::{http::{self,HistoryApi},
    repository::{Audience,Filter,HistoryRead,ReadError}},finance_export::archive};
use axum::{body::{Body,to_bytes},http::{Request,StatusCode}};
use serde_json::{Value,json};
use sqlx::PgPool;
use std::io::{Cursor,Read};
use tower::ServiceExt;
#[path="common/runtime.rs"]
mod runtime;

struct Fixture {
    owner:PgPool, runtime:runtime::RuntimeRole, session:IssuedCredential,
    other:IssuedCredential, admin:IssuedCredential, root:IssuedCredential,
    key:IssuedCredential, second_key:IssuedCredential, account:i64,
}
impl Fixture {
    async fn new(pool:PgPool)->Self {
        core_schema::init(&pool).await.unwrap();
        let store=IdentityStore::from_pool(pool.clone());
        let session=store.bootstrap_user(7,1).await.unwrap();
        let other=store.bootstrap_user(8,1).await.unwrap();
        let admin=store.bootstrap_user(9,5).await.unwrap();
        let root=store.bootstrap_user(10,6).await.unwrap();
        let account=store.authorize(&session.secret).await.unwrap().owner_account_id;
        let key=store.issue_key(&session.secret,Account {id:7,kind:AccountKind::Personal},FundingPolicy::new_personal_key(),3600).await.unwrap();
        let second_key=store.issue_key(&session.secret,Account {id:7,kind:AccountKind::Personal},FundingPolicy::new_personal_key(),3600).await.unwrap();
        sqlx::raw_sql(include_str!("fixtures/history_source_v1.sql")).execute(&pool).await.unwrap();
        let runtime=runtime::RuntimeRole::create(&pool).await;
        sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA core_history TO {0}; GRANT SELECT ON ALL TABLES IN SCHEMA core_history TO {0}",runtime.name))
            .execute(&pool).await.unwrap();
        let this=Self {owner:pool,runtime,session,other,admin,root,key,second_key,account};
        this.insert(1,7,this.key.id,Some(500000),r#"{"pricing_credits_per_usd":500000,"api_key":"SYNTHETIC_SECRET"}"#).await;
        this.insert(2,7,this.second_key.id,Some(1000000),r#"{"pricing_credits_per_usd":1000000}"#).await;
        this.insert(3,8,this.other.id,Some(99000000),"{}").await;
        this
    }
    async fn insert(&self,id:i64,user:i64,key:i64,quota:Option<i64>,other:&str) {
        let account:i64=sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=$1").bind(user).fetch_one(&self.owner).await.unwrap();
        sqlx::query("INSERT INTO a04_fixture.logs(id,event_key,user_id,owner_account_id,credential_id,quota,other,channel_id) VALUES($1,$2,$3,$4,$5,$6,$7,99)")
            .bind(id).bind(format!("event-{id}")).bind(user).bind(account).bind(key).bind(quota).bind(other).execute(&self.owner).await.unwrap();
    }
    async fn request(&self,path:&str,secret:&str)->(StatusCode,Value) {
        let response=http::router(HistoryApi::new(self.runtime.pool.clone())).oneshot(Request::builder().uri(path).header("authorization",format!("Bearer {secret}")).body(Body::empty()).unwrap()).await.unwrap();
        let status=response.status();
        assert_eq!(response.headers()["cache-control"],"no-store");
        assert_eq!(response.headers()["x-content-type-options"],"nosniff");
        let bytes=to_bytes(response.into_body(),8*1024*1024).await.unwrap();
        (status,serde_json::from_slice(&bytes).unwrap())
    }
    async fn cleanup(self) {self.runtime.cleanup().await;}
}
fn range()->Filter {Filter {start_timestamp:1_699_999_900,end_timestamp:1_700_000_100,..Filter::default()}}

#[sqlx::test(migrations=false)]
async fn real_sessions_and_keys_never_broaden_history_scope(pool:PgPool) {
    let f=Fixture::new(pool).await;
    for path in ["/api/log/self?user_id=8","/api/log/","/api/log/stat","/api/data/users?start_timestamp=1&end_timestamp=2","/api/finance/export"] {
        assert_eq!(f.request(path,&f.session.secret).await.0,StatusCode::FORBIDDEN,"{path}");
    }
    for path in ["/api/log/self","/api/log/stat","/api/finance/export"] {
        assert_eq!(f.request(path,&f.key.secret).await.0,StatusCode::FORBIDDEN,"{path}");
    }
    let (status,body)=f.request("/api/log/token",&f.key.secret).await;
    assert_eq!(status,StatusCode::OK);assert_eq!(body["data"]["total"],1);
    assert_eq!(body["data"]["items"][0]["id"],1);
    assert!(body["data"]["items"][0].get("channel_id").is_none());
    let (_,body)=f.request("/api/log/self",&f.session.secret).await;
    assert_eq!(body["data"]["total"],2);
    let text=body.to_string();
    assert!(!text.contains("SYNTHETIC_SECRET") && !text.contains("credential_id") && !text.contains("event_key"));
    let (_,body)=f.request("/api/log/?user_id=8",&f.admin.secret).await;
    assert_eq!(body["data"]["total"],1);assert_eq!(body["data"]["items"][0]["user_id"],8);
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn count_page_money_and_authority_share_a_single_read_snapshot(pool:PgPool) {
    let f=Fixture::new(pool).await;
    let before:i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.audit").fetch_one(&f.owner).await.unwrap();
    let mut read=HistoryRead::begin(&f.runtime.pool,&f.session.secret,Audience::SelfUser,Filter::default()).await.unwrap();
    assert_eq!(read.logs().await.unwrap().total,2);
    f.insert(4,7,f.key.id,Some(500000),r#"{"pricing_credits_per_usd":500000}"#).await;
    sqlx::query("UPDATE core_identity.credentials SET revoked_at=clock_timestamp() WHERE id=$1").bind(f.session.id).execute(&f.owner).await.unwrap();
    let page=read.logs().await.unwrap();let stat=read.statistics().await.unwrap();
    assert_eq!(page.total,2);assert_eq!(page.items.len(),2);assert_eq!(stat.total,2);
    assert_eq!(stat.quota,Some(1500000));assert_eq!(stat.projection.money.quota_usd.as_deref(),Some("2"));
    read.finish().await.unwrap();
    assert_eq!(f.request("/api/log/self",&f.session.secret).await.0,StatusCode::UNAUTHORIZED);
    let after:i64=sqlx::query_scalar("SELECT count(*) FROM core_identity.audit").fetch_one(&f.owner).await.unwrap();
    assert_eq!(before,after);
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn missing_incomplete_writable_or_duplicate_source_never_looks_empty(pool:PgPool) {
    let f=Fixture::new(pool).await;
    sqlx::query("UPDATE a04_fixture.contract SET complete=false WHERE dataset='logs'").execute(&f.owner).await.unwrap();
    assert_eq!(f.request("/api/log/self",&f.session.secret).await.0,StatusCode::SERVICE_UNAVAILABLE);
    sqlx::query("UPDATE a04_fixture.contract SET complete=true WHERE dataset='logs'").execute(&f.owner).await.unwrap();
    sqlx::query("UPDATE a04_fixture.logs SET event_key='event-1' WHERE id=2").execute(&f.owner).await.unwrap();
    assert_eq!(f.request("/api/log/self",&f.session.secret).await.0,StatusCode::SERVICE_UNAVAILABLE);
    sqlx::query("UPDATE a04_fixture.logs SET event_key='event-2' WHERE id=2").execute(&f.owner).await.unwrap();
    sqlx::raw_sql(&format!("GRANT UPDATE ON core_history.log_records_v1 TO {}",f.runtime.name)).execute(&f.owner).await.unwrap();
    assert_eq!(f.request("/api/log/self",&f.session.secret).await.0,StatusCode::SERVICE_UNAVAILABLE);
    sqlx::raw_sql("DROP VIEW core_history.source_contract_v1").execute(&f.owner).await.unwrap();
    assert_eq!(f.request("/api/log/self",&f.session.secret).await.0,StatusCode::SERVICE_UNAVAILABLE);
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn metadata_nulls_and_refunds_are_not_silently_repriced(pool:PgPool) {
    let f=Fixture::new(pool).await;
    f.insert(4,7,f.key.id,None,"{}").await;
    f.insert(5,7,f.key.id,Some(-7),"{}").await;
    sqlx::query("UPDATE a04_fixture.logs SET log_type=6 WHERE id=5").execute(&f.owner).await.unwrap();
    let (_,body)=f.request("/api/log/self/stat",&f.session.secret).await;
    assert_eq!(body["data"]["total"],3);
    assert_eq!(body["data"]["quota_money_available"],false);
    assert!(body["data"].get("quota_usd").is_none());
    assert!(body["data"]["quota"].is_null());
    assert_eq!(body["data"]["unpriced_quota_records"],1);
    let (_,page)=f.request("/api/log/self?type=6",&f.session.secret).await;
    assert_eq!(page["data"]["items"][0]["quota"],-7);
    assert_eq!(page["data"]["items"][0]["quota_money_basis"],"not_consumption");
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn team_filter_requires_membership_and_does_not_broaden_key_owner(pool:PgPool) {
    let f=Fixture::new(pool).await;
    let store=IdentityStore::from_pool(f.owner.clone());
    let team=store.create_team(&f.other.secret).await.unwrap();
    let path=format!("/api/log/self?team_id={team}");
    assert_eq!(f.request(&path,&f.session.secret).await.0,StatusCode::FORBIDDEN);
    assert_eq!(f.request(&format!("/api/log/token?team_id={team}"),&f.key.secret).await.0,StatusCode::FORBIDDEN);
    // The owner may select the team, but cannot read unrelated personal rows.
    let (_,body)=f.request(&path,&f.other.secret).await;
    assert_eq!(body["data"]["total"],0);
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn bucket_and_flow_preserve_raw_quota_and_redact_admin_dimensions(pool:PgPool) {
    let f=Fixture::new(pool).await;
    sqlx::query("INSERT INTO a04_fixture.quota_data VALUES(1,7,$1,'synthetic-user',1700000000,'model',1,'key','default',99,'channel','node',500000,7,1)")
        .bind(f.account).execute(&f.owner).await.unwrap();
    for path in ["/api/data/self","/api/data/flow/self"] {
        let (_,body)=f.request(&format!("{path}?start_timestamp=1699999999&end_timestamp=1700000001"),&f.session.secret).await;
        let row=&body["data"][0];assert_eq!(row["quota"],500000);assert_eq!(row["quota_money_available"],false);
        assert!(row.get("channel_id").is_none() && row.get("node_name").is_none());
    }
    let (_,body)=f.request("/api/data/flow?start_timestamp=1699999999&end_timestamp=1700000001",&f.admin.secret).await;
    assert_eq!(body["data"][0]["channel_id"],99);assert!(body["data"][0].get("node_name").is_none());
    let (_,body)=f.request("/api/data/flow?start_timestamp=1699999999&end_timestamp=1700000001",&f.root.secret).await;
    assert_eq!(body["data"][0]["node_name"],"node");
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn query_parameters_are_values_not_sql_or_authority(pool:PgPool) {
    let f=Fixture::new(pool).await;
    let (status,body)=f.request("/api/log/self?model_name=%27%20OR%201%3D1--",&f.session.secret).await;
    assert_eq!(status,StatusCode::OK);assert_eq!(body["data"]["total"],0);
    for path in ["/api/log/self?start_timestamp=-1","/api/log/self?start_timestamp=2&end_timestamp=1","/api/log/self?type=99","/api/log/self?role=admin"] {
        assert_eq!(f.request(path,&f.session.secret).await.0,StatusCode::BAD_REQUEST,"{path}");
    }
    assert_eq!(f.request("/api/log/self?p=9223372036854775807",&f.session.secret).await.0,StatusCode::PAYLOAD_TOO_LARGE);
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn complete_zip_is_readable_redacted_and_uses_a_single_snapshot(pool:PgPool) {
    let f=Fixture::new(pool).await;
    sqlx::query("INSERT INTO a04_fixture.finance_records(dataset,id,record) VALUES ('topups',1,$1),('channels-pricing',1,$2)")
        .bind(json!({"topup_id":1,"user_id":7,"settled_amount_micros":1000000,"api_key":"SYNTHETIC_SECRET"}))
        .bind(json!({"channel_id":99,"base_url":"https://user:secret@example.test/path?token=secret#secret","channel_key":"SYNTHETIC_SECRET"}))
        .execute(&f.owner).await.unwrap();
    let mut read=HistoryRead::begin(&f.runtime.pool,&f.admin.secret,Audience::Administrator,range()).await.unwrap();
    sqlx::query("UPDATE a04_fixture.finance_records SET record=record||'{\"settled_amount_micros\":2000000}'::jsonb WHERE dataset='topups'").execute(&f.owner).await.unwrap();
    let bytes=archive::build(&mut read,1024*1024).await.unwrap();
    read.finish().await.unwrap();
    let mut zip=zip::ZipArchive::new(Cursor::new(bytes)).unwrap();
    assert_eq!(zip.len(),15);
    for index in 0..zip.len() {
        let mut file=zip.by_index(index).unwrap();let mut text=String::new();file.read_to_string(&mut text).unwrap();
        assert!(!text.contains("SYNTHETIC_SECRET"));
        let value:Value=serde_json::from_str(&text).unwrap();
        if file.name()=="topups.json" {assert_eq!(value[0]["settled_amount_micros"],1000000);}
        if file.name()=="channels-pricing.json" {assert_eq!(value[0]["base_url"],"https://example.test");}
    }
    f.cleanup().await;
}

#[sqlx::test(migrations=false)]
async fn archive_refuses_missing_documents_limits_and_runtime_writes(pool:PgPool) {
    let f=Fixture::new(pool).await;
    let mut read=HistoryRead::begin(&f.runtime.pool,&f.admin.secret,Audience::Administrator,range()).await.unwrap();
    assert!(matches!(archive::build(&mut read,1024).await,Err(ReadError::Limit)));
    read.finish().await.unwrap();
    sqlx::query("DELETE FROM a04_fixture.contract WHERE dataset='topups'").execute(&f.owner).await.unwrap();
    let mut read=HistoryRead::begin(&f.runtime.pool,&f.admin.secret,Audience::Administrator,range()).await.unwrap();
    assert_eq!(archive::build(&mut read,1024*1024).await.unwrap_err(),ReadError::Unavailable);
    let error=sqlx::query("UPDATE core_identity.users SET active=false WHERE id=7").execute(read.connection()).await.unwrap_err();
    assert_eq!(error.as_database_error().unwrap().code().as_deref(),Some("25006"));
    read.finish().await.unwrap();
    assert!(IdentityStore::from_pool(f.owner.clone()).authorize(&f.session.secret).await.is_ok());
    f.cleanup().await;
}

#[test]
fn finance_field_gate_never_serializes_nested_opaque_payloads() {
    let topup=archive::DATASETS.iter().find(|d| d.name=="topups").unwrap();
    assert_eq!(archive::project_record(topup,&json!({"topup_id":1,"provider_payload":{"secret":"x"}})).unwrap(),json!({"topup_id":1}));
    assert_eq!(archive::project_record(topup,&json!({"money":{"secret":"x"}})).unwrap_err(),ReadError::Unavailable);
}
