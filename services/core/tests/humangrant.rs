use axum::{
    Router,
    body::{Body, to_bytes},
    http::{Request, StatusCode},
};
use base64::{
    Engine as _,
    engine::general_purpose::{STANDARD, URL_SAFE_NO_PAD},
};
use lmm_core::{
    identity::IdentityStore,
    legacy_auth::{self, LegacyAuthConfig},
};
use ring::{
    rand::SystemRandom,
    signature::{Ed25519KeyPair, KeyPair},
};
use serde_json::{Value, json};
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{
    sync::atomic::{AtomicU64, Ordering},
    time::{SystemTime, UNIX_EPOCH},
};
use tower::ServiceExt;

const ORIGIN: &str = "http://api.lmm.local";
fn clock() -> i64 {
    SystemTime::now().duration_since(UNIX_EPOCH).unwrap().as_secs() as i64
}

struct Signer {
    key: Ed25519KeyPair,
    next: AtomicU64,
}
impl Signer {
    fn new() -> Self {
        let rng = SystemRandom::new();
        let pkcs8 = Ed25519KeyPair::generate_pkcs8(&rng).unwrap();
        Self { key: Ed25519KeyPair::from_pkcs8(pkcs8.as_ref()).unwrap(), next: AtomicU64::new(1) }
    }
    fn actor(&self) -> String {
        format!("ed25519:{}", URL_SAFE_NO_PAD.encode(Sha256::digest(self.key.public_key().as_ref())))
    }
    fn headers(&self, method: &str, path: &str, body: &[u8], auth: &str) -> Vec<(String, String)> {
        let mut nonce = [0_u8; 24];
        nonce[..8].copy_from_slice(&self.next.fetch_add(1,Ordering::SeqCst).to_be_bytes());
        let created = clock();
        let content_type = "application/json";
        let digest = format!("sha-256=:{}:", STANDARD.encode(Sha256::digest(body)));
        let public_key = URL_SAFE_NO_PAD.encode(self.key.public_key().as_ref());
        let authorization = format!("HumanGrant {auth}");
        let input = format!(
            "hg=(\"@method\" \"@target-uri\" \"content-digest\" \"content-type\" \"x-hg-key\" \"authorization\");created={created};expires={};nonce=\"{}\";keyid=\"{}\";alg=\"ed25519\";tag=\"humangrant-0.1\"",
            created+60,URL_SAFE_NO_PAD.encode(nonce),self.actor()
        );
        let signed = format!(
            "\"@method\": {method}\n\"@target-uri\": {ORIGIN}{path}\n\"content-digest\": {digest}\n\"content-type\": {content_type}\n\"x-hg-key\": {public_key}\n\"authorization\": {authorization}\n\"@signature-params\": {}",
            input.strip_prefix("hg=").unwrap()
        );
        let signature = STANDARD.encode(self.key.sign(signed.as_bytes()).as_ref());
        vec![
            ("content-type".into(),content_type.into()),
            ("content-digest".into(),digest),
            ("x-hg-key".into(),public_key),
            ("authorization".into(),authorization),
            ("signature-input".into(),input),
            ("signature".into(),format!("hg=:{signature}:")),
        ]
    }
}
async fn call(
    app: &Router,
    method: &str,
    path: &str,
    body: &[u8],
    headers: &[(String, String)],
) -> (StatusCode, Value) {
    let mut builder = Request::builder().uri(path).method(method);
    for (name,value) in headers { builder=builder.header(name.as_str(),value.as_str()); }
    let response = app.clone().oneshot(builder.body(Body::from(body.to_vec())).unwrap()).await.unwrap();
    let status=response.status();
    let bytes=to_bytes(response.into_body(),65536).await.unwrap();
    let value=serde_json::from_slice::<Value>(&bytes).unwrap_or(Value::Null);
    (status,value)
}
async fn signed(
    app: &Router,
    signer: &Signer,
    method: &str,
    path: &str,
    body: &[u8],
    token: &str,
) -> (StatusCode,Value) {
    let headers=signer.headers(method,path,body,token);
    call(app,method,path,body,&headers).await
}
async fn browser(
    app: &Router,
    method: &str,
    path: &str,
    body: Value,
    bearer: Option<&str>,
    csrf: Option<&str>,
    origin: bool,
) -> (StatusCode,Value) {
    let mut h=vec![("content-type".into(),"application/json".into())];
    if origin {h.push(("origin".into(),ORIGIN.into()));}
    if let Some(bearer)=bearer {h.push(("authorization".into(),format!("Bearer {bearer}")));}
    if let Some(csrf)=csrf {h.push(("x-humangrant-csrf".into(),csrf.into()));}
    call(app,method,path,body.to_string().as_bytes(),&h).await
}

#[sqlx::test(migrations=false)]
async fn signed_human_consent_claim_replay_expiry_disable_and_revoke(pool: PgPool) {
    let store = IdentityStore::from_pool(pool.clone()).with_registration_enabled(true);
    store.init_database().await.unwrap();
    store.check_schema().await.unwrap();
    let config=LegacyAuthConfig::new(ORIGIN,&[43;32]).unwrap().with_insecure_local_http().unwrap();
    let app=legacy_auth::router(store,config);
    let (status,_) = browser(
        &app,"POST","/api/user/register",
        json!({"username":"alice","password":"abc12345","accepted_legal":true}),
        None,None,true
    ).await;
    assert_eq!(status,StatusCode::OK);
    let (status,login) = browser(
        &app,"POST","/api/user/login",
        json!({"username":"alice","password":"abc12345"}),
        None,None,true
    ).await;
    assert_eq!(status,StatusCode::OK, "{login:?}");
    let bearer=login["data"]["access_token"].as_str().unwrap();
    let uid=login["data"]["user"]["id"].as_i64().unwrap();
    let signer=Signer::new();
    let request=json!({
        "client_id":"test_agent",
        "target_service":"lmm",
        "purpose":"Read profile to display an account greeting",
        "requested_permissions":[{"action":"lmm.profile.read","resource":"self"}],
        "expected_human_id":uid.to_string(),
        "session_ttl_seconds":300
    }).to_string();
    let signed_headers=signer.headers("POST","/v1/requests",request.as_bytes(),"anonymous");
    let (status,created)=call(&app,"POST","/v1/requests",request.as_bytes(),&signed_headers).await;
    assert_eq!(status,StatusCode::CREATED,"{created:?}");
    let id=created["request_id"].as_str().unwrap();
    assert_eq!(created["status"],"waiting");
    assert_eq!(created["requested_permissions"][0]["action"],"lmm.profile.read");
    let (status,duplicate)=call(&app,"POST","/v1/requests",request.as_bytes(),&signed_headers).await;
    assert_eq!(status,StatusCode::UNAUTHORIZED,"{duplicate:?}");
    assert_eq!(duplicate["error"],"signature_replay");

    let (status,_) = signed(&app,&signer,"GET",&format!("/v1/requests/{id}"),b"","anonymous").await;
    assert_eq!(status,StatusCode::OK);

    let link=created["human_authorization_url"].as_str().unwrap().rsplit('/').next().unwrap();
    let base=format!("/human/{link}");
    let (status,details)=call(&app,"GET",&format!("{base}/details"),b"",&[]).await;
    assert_eq!(status,StatusCode::OK);
    assert_eq!(details["client_id"],"test_agent");
    let (status,_) = browser(&app,"POST",&format!("{base}/begin"),json!({}),Some(bearer),None,false).await;
    assert_eq!(status,StatusCode::FORBIDDEN,"a browser mutation without Origin must be denied");
    let (status,begun) = browser(&app,"POST",&format!("{base}/begin"),json!({}),Some(bearer),None,true).await;
    assert_eq!(status,StatusCode::OK,"{begun:?}");
    let csrf=begun["csrf_token"].as_str().unwrap();
    let permission=json!([{"action":"lmm.profile.read","resource":"self"}]);
    let approval=json!({"permissions":permission,"expires_at":clock()+240});
    let (status,_) = browser(
        &app,"POST",&format!("{base}/approve"),
        approval.clone(),Some(bearer),Some("bad"),true
    ).await;
    assert_eq!(status,StatusCode::FORBIDDEN);
    let (status,approved) = browser(&app,"POST",&format!("{base}/approve"),approval.clone(),Some(bearer),Some(csrf),true).await;
    assert_eq!(status,StatusCode::OK,"{approved:?}");
    let grant=approved["grant_id"].as_str().unwrap();

    let (status,reuse) = browser(&app,"POST",&format!("{base}/approve"),approval,Some(bearer),Some(csrf),true).await;
    assert_eq!(status,StatusCode::FORBIDDEN,"{reuse:?}");
    let claim_path=format!("/v1/requests/{id}/claim");
    let (status,claimed)=signed(&app,&signer,"POST",&claim_path,b"{}","anonymous").await;
    assert_eq!(status,StatusCode::OK,"{claimed:?}");
    let token=claimed["access_token"].as_str().unwrap();
    assert_eq!(claimed["grant_id"],grant);
    let (status,_) = signed(&app,&signer,"POST",&claim_path,b"{}","anonymous").await;
    assert_eq!(status,StatusCode::CONFLICT);
    let (status,session)=signed(&app,&signer,"GET","/v1/session",b"",token).await;
    assert_eq!(status,StatusCode::OK,"{session:?}");
    assert_eq!(session["actor"],signer.actor());
    let (status,profile)=signed(&app,&signer,"GET","/v1/humangrant/profile",b"",token).await;
    assert_eq!(status,StatusCode::OK,"{profile:?}");
    assert_eq!(profile["username"],"alice");

    let (status,grants)=browser(&app,"GET","/api/humangrant/grants",json!({}),Some(bearer),None,false).await;
    assert_eq!(status,StatusCode::OK,"{grants:?}");
    let csrf=grants["grants"][0]["csrf_token"].as_str().unwrap();
    let (status,revoked)=browser(&app,"POST",&format!("/api/humangrant/grants/{grant}/revoke"),json!({}),Some(bearer),Some(csrf),true).await;
    assert_eq!(status,StatusCode::OK,"{revoked:?}");
    let (status,_) = signed(&app,&signer,"GET","/v1/humangrant/profile",b"",token).await;
    assert_eq!(status,StatusCode::FORBIDDEN);

    sqlx::query("UPDATE core_identity.hg_config SET enabled=FALSE").execute(&pool).await.unwrap();
    let (status,_) = signed(&app,&signer,"GET","/v1/session",b"",token).await;
    assert_eq!(status,StatusCode::FORBIDDEN);
    let (status,_) = signed(&app,&signer,"POST","/v1/requests",request.as_bytes(),"anonymous").await;
    assert_eq!(status,StatusCode::FORBIDDEN);
}
