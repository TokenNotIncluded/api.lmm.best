use super::*;
use jsonwebtoken::{EncodingKey, Header, encode};

#[sqlx::test(migrations = false)]
async fn google_login_cannot_issue_sessions_while_local_second_factor_is_required(pool: PgPool) {
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
    // Only this private test module can use the loopback provider.
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
            login_name: "mfa-account".into(),
            password: "test-only long passphrase".into(),
        })
        .await
        .unwrap();
    let user_id = store.authorize(&native.secret).await.unwrap().user_id;
    // Synthetic prelinked identity. This does not test an account-link API.
    sqlx::query(
        "INSERT INTO core_identity.oauth_identities(issuer,subject,user_id) VALUES ($1,$2,$3)",
    )
    .bind(ISSUER)
    .bind("mfa-subject")
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    let binding_a = new_secret("").unwrap();
    let binding_b = new_secret("").unwrap();
    let url_a = store.begin_google_login(&binding_a).await.unwrap();
    let url_b = store.begin_google_login(&binding_b).await.unwrap();
    let (state_a, code_a) = prepare_code(&store, &mock, &url_a, "mfa-subject").await;
    let (state_b, code_b) = prepare_code(&store, &mock, &url_b, "mfa-subject").await;
    // The policy changes after the flows begin but before the callbacks.
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=$1",
    )
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    assert!(store.authorize(&native.secret).await.is_err());
    let counts_sql = "SELECT (SELECT count(*) FROM core_identity.credentials), (SELECT count(*) FROM core_identity.audit WHERE action='session.google_login')";
    let before: (i64, i64) = sqlx::query_as(counts_sql).fetch_one(&pool).await.unwrap();
    let (first, second) = tokio::join!(
        store.finish_google_login(&state_a, &binding_a, &code_a),
        store.finish_google_login(&state_b, &binding_b, &code_b),
    );
    assert!(matches!(first, Err(IdentityError::Unauthorized)));
    assert!(matches!(second, Err(IdentityError::Unauthorized)));
    let after: (i64, i64) = sqlx::query_as(counts_sql).fetch_one(&pool).await.unwrap();
    assert_eq!(before, after);
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 2);
    // A rejected callback still consumes its state. Replaying it does not
    // make another provider exchange or recover an issued credential.
    assert!(matches!(
        store
            .finish_google_login(&state_a, &binding_a, &code_a)
            .await,
        Err(IdentityError::Unauthorized)
    ));
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 2);
    sqlx::query(
        "UPDATE core_identity.user_profiles SET second_factor_required=FALSE WHERE user_id=$1",
    )
    .bind(user_id)
    .execute(&pool)
    .await
    .unwrap();
    assert!(store.authorize(&native.secret).await.is_err());
    let binding = new_secret("").unwrap();
    let url = store.begin_google_login(&binding).await.unwrap();
    let (state, code) = prepare_code(&store, &mock, &url, "mfa-subject").await;
    let issued = store
        .finish_google_login(&state, &binding, &code)
        .await
        .unwrap();
    assert_eq!(
        store.authorize(&issued.secret).await.unwrap().user_id,
        user_id
    );
    server.abort();
}

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
fn signed_token_without_optional_nbf_remains_valid() {
    let expected = digest(&"n".repeat(64)).unwrap();
    let claims = claims();
    assert!(claims.get("nbf").is_none());
    assert_eq!(
        verify_token(&token(claims), &keys(), &expected, "client-1")
            .unwrap()
            .sub,
        "google-user-1"
    );
}

#[test]
fn signed_tokens_reject_malformed_optional_nbf() {
    let expected = digest(&"n".repeat(64)).unwrap();
    let future = now().unwrap() + 3600;
    for invalid in [
        json!(future.to_string()),
        Value::Null,
        json!([future]),
        json!({"timestamp": future}),
    ] {
        let mut claims = claims();
        claims["nbf"] = invalid;
        // Every negative is signed with the same genuine test RSA key as the
        // positive control; refusal must come from claim validation.
        assert!(matches!(
            verify_token(&token(claims), &keys(), &expected, "client-1"),
            Err(IdentityError::Unauthorized)
        ));
    }
}

#[test]
fn signed_tokens_reject_malformed_required_exp() {
    let expected = digest(&"n".repeat(64)).unwrap();
    let future = now().unwrap() + 3600;
    for invalid in [
        json!(future.to_string()),
        Value::Null,
        json!([future]),
        json!({"timestamp": future}),
        json!(true),
        json!(-1),
    ] {
        let mut claims = claims();
        claims["exp"] = invalid;
        assert!(matches!(
            verify_token(&token(claims), &keys(), &expected, "client-1"),
            Err(IdentityError::Unauthorized)
        ));
    }
    let mut absent = claims();
    absent.as_object_mut().unwrap().remove("exp");
    assert!(matches!(
        verify_token(&token(absent), &keys(), &expected, "client-1"),
        Err(IdentityError::Unauthorized)
    ));
}

#[test]
fn signed_tokens_reject_numeric_future_nbf_and_accept_past_nbf() {
    let expected = digest(&"n".repeat(64)).unwrap();
    let current = now().unwrap();
    let mut future = claims();
    future["exp"] = json!(current + 7200);
    future["nbf"] = json!(current + 3600);
    assert!(matches!(
        verify_token(&token(future), &keys(), &expected, "client-1"),
        Err(IdentityError::Unauthorized)
    ));
    let mut past = claims();
    past["nbf"] = json!(current.saturating_sub(60));
    assert_eq!(
        verify_token(&token(past), &keys(), &expected, "client-1")
            .unwrap()
            .sub,
        "google-user-1"
    );
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
    redirect_uri: Option<Arc<str>>,
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
            mock.redirect_uri
                .as_deref()
                .unwrap_or("https://example.com/core/v1/auth/oauth/google/callback"),
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
    let id_token = if claims.get("__test_algorithm") == Some(&json!("HS256")) {
        let mut header = Header::new(Algorithm::HS256);
        header.kid = Some("test-only".into());
        encode(
            &header,
            &claims,
            &EncodingKey::from_secret(b"synthetic-wrong-algorithm-key"),
        )
        .unwrap()
    } else {
        let mut signed = token(claims.clone());
        if claims.get("__test_bad_signature") == Some(&json!(true)) {
            let offset = signed.rfind('.').unwrap() + 1;
            let replacement = if signed.as_bytes()[offset] == b'A' {
                "B"
            } else {
                "A"
            };
            signed.replace_range(offset..offset + 1, replacement);
        }
        signed
    };
    (StatusCode::OK, Json(json!({"id_token":id_token})))
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

// These are real browser routes, real PostgreSQL transactions and a local
// provider that signs ID tokens and validates the one-time code / PKCE verifier.
// Endpoint replacement remains private to this unit-test module.
async fn web_call(
    app: &Router,
    method: &str,
    path: &str,
    cookie: Option<&str>,
    body: Option<Value>,
) -> (StatusCode, axum::http::HeaderMap, Value) {
    use axum::{
        body::{Body, to_bytes},
        http::Request,
    };
    use tower::ServiceExt;
    let mut request = Request::builder().method(method).uri(path);
    if method == "POST" {
        request = request.header("origin", "https://example.com");
    }
    if let Some(cookie) = cookie {
        request = request.header("cookie", cookie);
    }
    let body = if let Some(body) = body {
        request = request.header("content-type", "application/json");
        Body::from(body.to_string())
    } else {
        Body::empty()
    };
    let response = app
        .clone()
        .oneshot(request.body(body).unwrap())
        .await
        .unwrap();
    let status = response.status();
    let headers = response.headers().clone();
    assert_eq!(headers["cache-control"], "no-store");
    assert_eq!(headers["referrer-policy"], "no-referrer");
    let json = serde_json::from_slice(&to_bytes(response.into_body(), 65536).await.unwrap())
        .unwrap_or(Value::Null);
    (status, headers, json)
}
async fn web_flow(
    app: &Router,
    store: &IdentityStore,
    mock: &MockProvider,
    subject: &str,
) -> (String, String, String) {
    let (status, headers, body) = web_call(
        app,
        "POST",
        "/api/oauth/google",
        None,
        Some(json!({"accepted_legal":true})),
    )
    .await;
    assert_eq!(status, StatusCode::OK);
    let raw = headers["set-cookie"].to_str().unwrap();
    assert!(raw.starts_with("__Host-lmm_web_google_flow="));
    assert!(raw.contains("Secure; HttpOnly; SameSite=Lax"));
    let cookie = raw.split(';').next().unwrap().to_owned();
    let url = body["data"]["authorization_url"].as_str().unwrap();
    assert!(!url.contains(cookie.split_once('=').unwrap().1));
    let (state, code) = prepare_code(store, mock, url, subject).await;
    (cookie, state, code)
}
fn web_app(store: IdentityStore) -> Router {
    crate::legacy_auth::router(
        store,
        crate::legacy_auth::LegacyAuthConfig::new("https://example.com", &[43; 32]).unwrap(),
    )
}
async fn identity_counts(pool: &PgPool) -> (i64, i64, i64, i64, i64) {
    sqlx::query_as("SELECT (SELECT count(*) FROM core_identity.users),(SELECT count(*) FROM core_identity.accounts),(SELECT count(*) FROM core_identity.credentials),(SELECT count(*) FROM core_identity.web_sessions),(SELECT count(*) FROM core_identity.audit)").fetch_one(pool).await.unwrap()
}
#[sqlx::test(migrations = false)]
async fn google_browser_signed_routes_cookie_refresh_replay_policy_and_registration(pool: PgPool) {
    let mock = MockProvider {
        redirect_uri: Some(Arc::from("https://example.com/api/oauth/google/callback")),
        ..Default::default()
    };
    let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
    let address = listener.local_addr().unwrap();
    let provider = Router::new()
        .route("/token", post(exchange))
        .route("/keys", get(public_keys))
        .with_state(mock.clone());
    let server = tokio::spawn(async move { axum::serve(listener, provider).await.unwrap() });
    let mut config = GoogleOAuth::new(
        "client-1".into(),
        "test-only-client-secret".into(),
        mock.redirect_uri.as_deref().unwrap().into(),
    )
    .unwrap();
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
    // Explicit installer-owned fixture for the key-management/user projection dependency.
    sqlx::raw_sql(crate::abuse::SCHEMA)
        .execute(&pool)
        .await
        .unwrap();
    let app = web_app(store.clone());
    let (cookie, state, code) = web_flow(&app, &store, &mock, "browser-subject").await;
    let path =
        format!("/api/oauth/google/callback?state={state}&code={code}&next=https://evil.invalid");
    let wrong = format!("__Host-lmm_web_google_flow={}", "f".repeat(64));
    for cookie in [None, Some(wrong.as_str())] {
        assert_eq!(
            web_call(&app, "GET", &path, cookie, None).await.0,
            StatusCode::UNAUTHORIZED
        );
    }
    let duplicate = format!("{cookie}; {cookie}");
    assert_eq!(
        web_call(&app, "GET", &path, Some(&duplicate), None).await.0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 0);
    let (first, second) = tokio::join!(
        web_call(&app, "GET", &path, Some(&cookie), None),
        web_call(&app, "GET", &path, Some(&cookie), None)
    );
    let (success, replay) = if first.0 == StatusCode::SEE_OTHER {
        (first, second)
    } else {
        (second, first)
    };
    assert_eq!(success.0, StatusCode::SEE_OTHER);
    assert_eq!(replay.0, StatusCode::UNAUTHORIZED);
    assert_eq!(success.1["location"], "/console");
    assert!(success.2.is_null()); // no native credential or access token in callback
    let cookies: Vec<_> = success
        .1
        .get_all("set-cookie")
        .iter()
        .map(|v| v.to_str().unwrap())
        .collect();
    let refresh_raw = cookies
        .iter()
        .find(|c| c.starts_with("new_api_refresh="))
        .unwrap();
    assert!(refresh_raw.contains("HttpOnly; Secure; SameSite=Strict"));
    assert!(refresh_raw.contains("Path=/api/user/auth"));
    assert!(
        cookies
            .iter()
            .any(|c| c.starts_with("__Host-lmm_web_google_flow=;") && c.contains("Max-Age=0"))
    );
    let refresh = refresh_raw.split(';').next().unwrap();
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), 1);
    let (status, headers, bundle) =
        web_call(&app, "POST", "/api/user/auth/refresh", Some(refresh), None).await;
    assert_eq!(status, StatusCode::OK);
    assert_eq!(bundle["data"]["session"]["login_method"], "google");
    assert_eq!(bundle["data"]["user"]["role"], 1);
    assert_eq!(bundle["data"]["user"]["email"], "");
    let access = bundle["data"]["access_token"].as_str().unwrap();
    let user = store.authorize(access).await.unwrap().user_id;
    assert!(matches!(
        store.refresh_session(access).await,
        Err(IdentityError::Forbidden)
    ));
    let browser = crate::identity::BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
    assert_eq!(browser.current_user(access).await.unwrap()["id"], user);
    let issued_refresh = headers["set-cookie"]
        .to_str()
        .unwrap()
        .split(';')
        .next()
        .unwrap()
        .to_owned();
    assert_eq!(
        web_call(
            &app,
            "POST",
            "/api/user/auth/logout",
            Some(&issued_refresh),
            None
        )
        .await
        .0,
        StatusCode::OK
    );
    assert!(store.authorize(access).await.is_err());
    assert_eq!(
        web_call(
            &app,
            "POST",
            "/api/user/auth/refresh",
            Some(&issued_refresh),
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );

    // Domain-separated Web bindings cannot redeem flows started by native auth.
    let raw_binding = new_secret("").unwrap();
    let native_url = store.begin_google_login(&raw_binding).await.unwrap();
    let (native_state, native_code) =
        prepare_code(&store, &mock, &native_url, "browser-subject").await;
    let before_exchange = mock.exchanges.load(Ordering::SeqCst);
    assert!(
        browser
            .finish_google_login(
                &native_state,
                &raw_binding,
                &native_code,
                &crate::identity::BrowserSessionMetadata::default()
            )
            .await
            .is_err()
    );
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), before_exchange);

    // Signature, issuer, audience, algorithm, expiry and nonce refusal are
    // exercised through the callback, with no orphan users or issued sessions.
    for (field, bad) in [
        ("iss", json!("https://evil.invalid")),
        ("aud", json!("other-client")),
        ("nonce", json!("x".repeat(64))),
        ("exp", json!(1)),
        ("__test_bad_signature", json!(true)),
        ("__test_algorithm", json!("HS256")),
    ] {
        let (cookie, state, code) = web_flow(&app, &store, &mock, "untrusted-subject").await;
        mock.codes.lock().await.get_mut(&code).unwrap().0[field] = bad;
        let before = identity_counts(&pool).await;
        let path = format!("/api/oauth/google/callback?state={state}&code={code}");
        let response = web_call(&app, "GET", &path, Some(&cookie), None).await;
        assert_eq!(response.0, StatusCode::UNAUTHORIZED);
        assert_eq!(before, identity_counts(&pool).await);
        assert!(
            !response
                .1
                .get_all("set-cookie")
                .iter()
                .any(|v| v.to_str().unwrap().starts_with("new_api_refresh="))
        );
        let exchanges = mock.exchanges.load(Ordering::SeqCst);
        assert_eq!(
            web_call(&app, "GET", &path, Some(&cookie), None).await.0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(mock.exchanges.load(Ordering::SeqCst), exchanges);
    }
    let (cookie, state, _code) = web_flow(&app, &store, &mock, "provider-refused-subject").await;
    let before = identity_counts(&pool).await;
    let refused_path =
        format!("/api/oauth/google/callback?state={state}&code=provider-secret-must-not-leak");
    let refused = web_call(&app, "GET", &refused_path, Some(&cookie), None).await;
    assert_eq!(refused.0, StatusCode::UNAUTHORIZED);
    assert!(
        !refused
            .2
            .to_string()
            .contains("provider-secret-must-not-leak")
    );
    assert_eq!(before, identity_counts(&pool).await);
    let exchanged = mock.exchanges.load(Ordering::SeqCst);
    assert_eq!(
        web_call(&app, "GET", &refused_path, Some(&cookie), None)
            .await
            .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), exchanged);
    let (cookie, state, code) = web_flow(&app, &store, &mock, "expired-subject").await;
    sqlx::query("UPDATE core_identity.oauth_flows SET expires_at=clock_timestamp()-interval '1 second' WHERE state_digest=$1").bind(digest(&state).unwrap()).execute(&pool).await.unwrap();
    let exchanges = mock.exchanges.load(Ordering::SeqCst);
    assert_eq!(
        web_call(
            &app,
            "GET",
            &format!("/api/oauth/google/callback?state={state}&code={code}"),
            Some(&cookie),
            None
        )
        .await
        .0,
        StatusCode::UNAUTHORIZED
    );
    assert_eq!(mock.exchanges.load(Ordering::SeqCst), exchanges);

    // Flip policy after start, as in #877. Existing credentials become invalid
    // by generation, and callbacks must not create a new family through MFA.
    for policy in ["mfa", "disabled-user", "disabled-account"] {
        let (cookie, state, code) = web_flow(&app, &store, &mock, "browser-subject").await;
        let (sql, undo) = match policy {
            "mfa" => (
                "UPDATE core_identity.user_profiles SET second_factor_required=TRUE WHERE user_id=$1",
                "UPDATE core_identity.user_profiles SET second_factor_required=FALSE WHERE user_id=$1",
            ),
            "disabled-user" => (
                "UPDATE core_identity.users SET active=FALSE WHERE id=$1",
                "UPDATE core_identity.users SET active=TRUE WHERE id=$1",
            ),
            _ => (
                "UPDATE core_identity.accounts SET active=FALSE WHERE id=(SELECT personal_account_id FROM core_identity.users WHERE id=$1)",
                "UPDATE core_identity.accounts SET active=TRUE WHERE id=(SELECT personal_account_id FROM core_identity.users WHERE id=$1)",
            ),
        };
        sqlx::query(sql).bind(user).execute(&pool).await.unwrap();
        let before = identity_counts(&pool).await;
        assert_eq!(
            web_call(
                &app,
                "GET",
                &format!("/api/oauth/google/callback?state={state}&code={code}"),
                Some(&cookie),
                None
            )
            .await
            .0,
            StatusCode::UNAUTHORIZED
        );
        assert_eq!(before, identity_counts(&pool).await);
        sqlx::query(undo).bind(user).execute(&pool).await.unwrap();
    }
    let closed = store.clone().with_registration_enabled(false);
    let closed_app = web_app(closed.clone());
    for (subject, expected) in [
        ("browser-subject", StatusCode::SEE_OTHER),
        ("closed-new-subject", StatusCode::FORBIDDEN),
    ] {
        let (cookie, state, code) = web_flow(&closed_app, &closed, &mock, subject).await;
        let before = identity_counts(&pool).await;
        assert_eq!(
            web_call(
                &closed_app,
                "GET",
                &format!("/api/oauth/google/callback?state={state}&code={code}"),
                Some(&cookie),
                None
            )
            .await
            .0,
            expected
        );
        if expected == StatusCode::FORBIDDEN {
            assert_eq!(before, identity_counts(&pool).await);
        }
    }
    // Different one-time states for the same new subject may both log in,
    // but subject/account creation is serialized and must produce one identity.
    let (cookie_a, state_a, code_a) = web_flow(&app, &store, &mock, "concurrent-new-subject").await;
    let (cookie_b, state_b, code_b) = web_flow(&app, &store, &mock, "concurrent-new-subject").await;
    let path_a = format!("/api/oauth/google/callback?state={state_a}&code={code_a}");
    let path_b = format!("/api/oauth/google/callback?state={state_b}&code={code_b}");
    let (a, b) = tokio::join!(
        web_call(&app, "GET", &path_a, Some(&cookie_a), None),
        web_call(&app, "GET", &path_b, Some(&cookie_b), None)
    );
    assert_eq!(a.0, StatusCode::SEE_OTHER);
    assert_eq!(b.0, StatusCode::SEE_OTHER);
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_identity.users")
            .fetch_one(&pool)
            .await
            .unwrap(),
        2
    );
    assert_eq!(
        sqlx::query_scalar::<_, i64>("SELECT count(*) FROM core_identity.password_logins")
            .fetch_one(&pool)
            .await
            .unwrap(),
        0
    );
    server.abort();
}
