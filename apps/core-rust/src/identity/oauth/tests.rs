use super::*;
use jsonwebtoken::{EncodingKey, Header, encode};

fn fixture() -> Value {
    serde_json::from_str(include_str!("../testdata/google-signing-key.json")).unwrap()
}
fn keys() -> KeySet {
    let f = fixture();
    serde_json::from_value(json!({"keys":[{"kid":"test-only","kty":"RSA","alg":"RS256","use":"sig","n":f["n"],"e":f["e"]}]})).unwrap()
}
fn token(claims: Value) -> String {
    let private = URL_SAFE_NO_PAD
        .decode(fixture()["private_key_pkcs1_der"].as_str().unwrap())
        .unwrap();
    let mut header = Header::new(Algorithm::RS256);
    header.kid = Some("test-only".into());
    encode(&header, &claims, &EncodingKey::from_rsa_der(&private)).unwrap()
}
fn claims() -> Value {
    let now = now().unwrap();
    json!({"iss":ISSUER,"sub":"google-user-1","aud":"client-1","nonce":"n".repeat(64),"iat":now,"exp":now+300})
}
#[test]
fn signed_tokens_require_issuer_audience_nonce_expiry_and_signature() {
    let expected = digest(&"n".repeat(64)).unwrap();
    assert_eq!(
        verify_token(&token(claims()), &keys(), &expected, "client-1")
            .unwrap()
            .sub,
        "google-user-1"
    );
    for (field, bad) in [
        ("iss", json!("https://attacker.invalid")),
        ("aud", json!("other-client")),
        ("nonce", json!("x".repeat(64))),
        ("azp", json!("other-client")),
        ("sub", json!("")),
        ("exp", json!(1)),
        ("iat", json!(now().unwrap() + 3600)),
    ] {
        let mut c = claims();
        c[field] = bad;
        assert!(verify_token(&token(c), &keys(), &expected, "client-1").is_err());
    }
    let valid = token(claims());
    let mut parts: Vec<_> = valid.split('.').map(str::to_owned).collect();
    let mut altered = claims();
    altered["sub"] = json!("attacker");
    parts[1] = URL_SAFE_NO_PAD.encode(serde_json::to_vec(&altered).unwrap());
    assert!(verify_token(&parts.join("."), &keys(), &expected, "client-1").is_err());
    let mut duplicate = keys();
    duplicate.keys.extend(keys().keys);
    assert!(verify_token(&valid, &duplicate, &expected, "client-1").is_err());
}
#[test]
fn callback_configuration_cannot_redirect_to_an_unrelated_path_or_http() {
    for uri in [
        "http://example.com/core/v1/auth/oauth/google/callback",
        "https://example.com/other",
        "https://user@example.com/core/v1/auth/oauth/google/callback",
        "https://example.com/core/v1/auth/oauth/google/callback?next=evil",
    ] {
        assert!(GoogleOAuth::new("id".into(), "secret".into(), uri.into()).is_err());
    }
}
#[sqlx::test(migrations = false)]
async fn flow_binding_expiry_and_atomic_consumption(pool: PgPool) {
    let config = GoogleOAuth::new(
        "id".into(),
        "secret".into(),
        format!("https://example.com{CALLBACK_PATH}"),
    )
    .unwrap();
    let store = IdentityStore::from_pool(pool.clone()).with_google_oauth(config);
    store.init_database().await.unwrap();
    let binding = new_secret("").unwrap();
    let url = Url::parse(&store.begin_google_login(&binding).await.unwrap()).unwrap();
    let params: std::collections::HashMap<_, _> = url.query_pairs().into_owned().collect();
    assert_eq!(params["code_challenge_method"], "S256");
    assert!(!url.as_str().contains(&binding));
    assert!(matches!(
        store
            .take_google_flow(&params["state"], &"x".repeat(64))
            .await,
        Err(IdentityError::Unauthorized)
    ));
    let (one, two) = tokio::join!(
        store.take_google_flow(&params["state"], &binding),
        store.take_google_flow(&params["state"], &binding)
    );
    assert_eq!(usize::from(one.is_ok()) + usize::from(two.is_ok()), 1);
    let row = one.or(two).unwrap();
    let verifier: String = row.try_get("pkce_verifier").unwrap();
    assert_eq!(
        params["code_challenge"],
        URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()))
    );
    let url = Url::parse(&store.begin_google_login(&binding).await.unwrap()).unwrap();
    let state = url
        .query_pairs()
        .find(|(k, _)| k == "state")
        .unwrap()
        .1
        .into_owned();
    sqlx::query(
        "UPDATE core_identity.oauth_flows SET expires_at=clock_timestamp()-interval '1 second'",
    )
    .execute(&pool)
    .await
    .unwrap();
    assert!(store.take_google_flow(&state, &binding).await.is_err());
}

use axum::{
    Form, Json, Router,
    extract::State,
    http::StatusCode,
    routing::{get, post},
};
use std::{
    collections::HashMap,
    sync::{
        Arc,
        atomic::{AtomicUsize, Ordering},
    },
};
use tokio::sync::Mutex;

type Codes = HashMap<String, (Value, String)>;
#[derive(Clone, Default)]
struct MockProvider {
    codes: Arc<Mutex<Codes>>,
    exchanges: Arc<AtomicUsize>,
}
async fn exchange(
    State(mock): State<MockProvider>,
    Form(params): Form<HashMap<String, String>>,
) -> (StatusCode, Json<Value>) {
    mock.exchanges.fetch_add(1, Ordering::SeqCst);
    let Some((claims, verifier)) = mock
        .codes
        .lock()
        .await
        .remove(params.get("code").map(String::as_str).unwrap_or_default())
    else {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error":"invalid_grant"})),
        );
    };
    let valid = [
        ("client_id", "client-1"),
        ("client_secret", "test-only-client-secret"),
        ("grant_type", "authorization_code"),
        (
            "redirect_uri",
            "https://example.com/core/v1/auth/oauth/google/callback",
        ),
        ("code_verifier", verifier.as_str()),
    ]
    .into_iter()
    .all(|(key, value)| params.get(key).map(String::as_str) == Some(value));
    if !valid {
        return (
            StatusCode::BAD_REQUEST,
            Json(json!({"error":"invalid_request"})),
        );
    }
    (StatusCode::OK, Json(json!({"id_token":token(claims)})))
}
async fn public_keys() -> Json<Value> {
    let f = fixture();
    Json(
        json!({"keys":[{"kid":"test-only","kty":"RSA","alg":"RS256","use":"sig","n":f["n"],"e":f["e"]}]}),
    )
}
async fn prepare_code(
    store: &IdentityStore,
    mock: &MockProvider,
    url: &str,
    subject: &str,
) -> (String, String) {
    let url = Url::parse(url).unwrap();
    let params: HashMap<_, _> = url.query_pairs().into_owned().collect();
    let state = params["state"].clone();
    let verifier: String = sqlx::query_scalar(
        "SELECT pkce_verifier FROM core_identity.oauth_flows WHERE state_digest=$1",
    )
    .bind(digest(&state).unwrap())
    .fetch_one(&store.pool)
    .await
    .unwrap();
    assert_eq!(
        params["code_challenge"],
        URL_SAFE_NO_PAD.encode(Sha256::digest(verifier.as_bytes()))
    );
    let mut c = claims();
    c["nonce"] = json!(params["nonce"]);
    c["sub"] = json!(subject);
    // Neither an email nor privilege-shaped provider fields can link or elevate.
    c["email"] = json!("alice@example.invalid");
    c["platform_role"] = json!("superadmin");
    c["user_id"] = json!(1);
    let code = new_secret("code_").unwrap();
    mock.codes.lock().await.insert(code.clone(), (c, verifier));
    (state, code)
}
#[sqlx::test(migrations = false)]
async fn signed_provider_round_trip_creates_native_sessions_without_go_or_email_linking(
    pool: PgPool,
) {
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use tower::ServiceExt;
    let mock = MockProvider::default();
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let app = Router::new()
        .route("/token", post(exchange))
        .route("/keys", get(public_keys))
        .with_state(mock.clone());
    let server = tokio::spawn(async move { axum::serve(listener, app).await.unwrap() });
    let mut config = GoogleOAuth::new(
        "client-1".into(),
        "test-only-client-secret".into(),
        format!("https://example.com{CALLBACK_PATH}"),
    )
    .unwrap();
    // Only this private unit-test module can replace fixed HTTPS endpoints.
    config.client = Client::builder()
        .redirect(Policy::none())
        .timeout(Duration::from_secs(3))
        .build()
        .unwrap();
    config.token_endpoint = Url::parse(&format!("http://{address}/token")).unwrap();
    config.keys_endpoint = Url::parse(&format!("http://{address}/keys")).unwrap();
    let store = IdentityStore::from_pool(pool.clone())
        .with_google_oauth(config)
        .with_registration_enabled(true);
    store.init_database().await.unwrap();
    let native = store
        .register(&LoginRequest {
            login_name: "alice".into(),
            password: "test-only long passphrase".into(),
        })
        .await
        .unwrap();
    let native_id = store.authorize(&native.secret).await.unwrap().user_id;
    let http = crate::identity_http::router(store.clone());
    let started = http
        .clone()
        .oneshot(
            Request::builder()
                .uri("/core/v1/auth/oauth/google")
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(started.status(), StatusCode::SEE_OTHER);
    let cookie = started.headers()["set-cookie"].to_str().unwrap();
    assert!(cookie.contains("Secure; HttpOnly; SameSite=Lax"));
    let cookie = cookie.split(';').next().unwrap().to_owned();
    let (state, code) = prepare_code(
        &store,
        &mock,
        started.headers()["location"].to_str().unwrap(),
        "google-user-1",
    )
    .await;
    let path = format!("{CALLBACK_PATH}?state={state}&code={code}");
    let missing = http
        .clone()
        .oneshot(Request::builder().uri(&path).body(Body::empty()).unwrap())
        .await
        .unwrap();
    assert_eq!(missing.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 0);
    let response = http
        .clone()
        .oneshot(
            Request::builder()
                .uri(&path)
                .header("cookie", &cookie)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()["cache-control"], "no-store");
    assert_eq!(response.headers()["referrer-policy"], "no-referrer");
    assert!(
        response.headers()["set-cookie"]
            .to_str()
            .unwrap()
            .contains("Max-Age=0")
    );
    let body: Value =
        serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap()).unwrap();
    let actor = store
        .authorize(body["secret"].as_str().unwrap())
        .await
        .unwrap();
    assert_ne!(actor.user_id, native_id);
    assert_eq!(actor.platform_level, 0);
    assert_eq!(actor.owner.kind, AccountKind::Personal);
    let replay = http
        .clone()
        .oneshot(
            Request::builder()
                .uri(&path)
                .header("cookie", cookie)
                .body(Body::empty())
                .unwrap(),
        )
        .await
        .unwrap();
    assert_eq!(replay.status(), StatusCode::UNAUTHORIZED);
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 1);
    // Existing linked subjects can sign in with registration disabled. New
    // subjects cannot; failed registration leaves no orphan account/session.
    let closed = store.clone().with_registration_enabled(false);
    for (subject, allowed) in [("google-user-1", true), ("new-google-user", false)] {
        let binding = new_secret("").unwrap();
        let url = closed.begin_google_login(&binding).await.unwrap();
        let (state, code) = prepare_code(&closed, &mock, &url, subject).await;
        let result = closed.finish_google_login(&state, &binding, &code).await;
        if allowed {
            assert_eq!(
                closed
                    .authorize(&result.unwrap().secret)
                    .await
                    .unwrap()
                    .user_id,
                actor.user_id
            );
        } else {
            assert!(matches!(result, Err(IdentityError::Forbidden)));
        }
    }
    let count: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.users")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(count, 2);
    let linked: i64 = sqlx::query_scalar("SELECT count(*) FROM core_identity.oauth_identities")
        .fetch_one(&pool)
        .await
        .unwrap();
    assert_eq!(linked, 1);
    sqlx::query("UPDATE core_identity.users SET active=FALSE WHERE id=$1")
        .bind(actor.user_id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(
        store
            .authorize(body["secret"].as_str().unwrap())
            .await
            .is_err()
    );
    sqlx::query("UPDATE core_identity.users SET active=TRUE WHERE id=$1")
        .bind(actor.user_id)
        .execute(&pool)
        .await
        .unwrap();
    assert!(
        store
            .authorize(body["secret"].as_str().unwrap())
            .await
            .is_err()
    );
    server.abort();
}
