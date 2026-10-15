//! Owned disposable PG, real native browser consent, restricted runtime, real
//! TCP Core routes and a bounded synthetic provider. Not external/TLS acceptance.
use super::{model_fixture, runtime};
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use lmm_core::{
    billing::{
        Credits,
        charging::{Charging, Price, Request as ChargeRequest},
        ledger::{Action, Ledger, Request as LedgerRequest},
        transactional_ledger::TransactionalLedger,
    },
    identity::{
        BrowserAuth, BrowserRegistration, BrowserSessionMetadata, IdentityError, IdentityStore,
        grants::{AuthorizationRequest, LmmIssuer, LmmProfile, TokenForm, TokenWire},
    },
    legacy_auth::{self, LegacyAuthConfig},
    model_http::{ChargePolicy, ModelGateway, UpstreamCredential},
    oauth2_http,
    relay::Limits,
    routing::*,
    runtime_database::{self, RequiredCapabilities},
};
use model_fixture::Provider;
use sha2::{Digest, Sha256};
use sqlx::PgPool;
use std::{collections::BTreeMap, future::Future, sync::Arc, time::Duration};
use tokio::{sync::oneshot, task::JoinHandle};
const ISSUER: &str = "https://pi-issuer.example.test";
const VERIFIER: &str = "abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ-._~";
const REDIRECT: &str = "http://127.0.0.1:38423/oauth/lmm/callback";
fn routes(
    endpoint: &str,
    cache_write: u64,
    request_fee: u64,
    policy: PayloadPolicy,
) -> Arc<lmm_core::routing::Router> {
    let prices = PriceSnapshot::compile(PriceConfig {
        version: 1,
        rules: vec![PriceRule {
            billing_mode: lmm_core::routing::BillingMode::Tokens,
            id: 1,
            model: "provider-model".into(),
            group: "allowed".into(),
            speed: Speed::Standard,
            rates: TokenRates {
                input: 2_000_000,
                output: 4_000_000,
                cache_read: 500_000,
                cache_write,
                request: request_fee,
            },
            group_multiplier_ppm: MULTIPLIER_ONE,
            speed_multiplier_ppm: MULTIPLIER_ONE,
        }],
    })
    .unwrap();
    Arc::new(
        lmm_core::routing::Router::new(
            RoutingConfig {
                version: 1,
                groups: vec![ModelGroup {
                    name: "allowed".into(),
                    enabled: true,
                }],
                upstreams: vec![UpstreamConfig {
                    id: 1,
                    endpoint: if endpoint.starts_with("https:") {
                        endpoint.to_owned().try_into().unwrap()
                    } else {
                        Endpoint::local_loopback(endpoint.to_owned()).unwrap()
                    },
                    protocol: Protocol::OpenAiChat,
                    credential: CredentialRef { id: 7, revision: 1 },
                    enabled: true,
                }],
                routes: vec![ModelRoute {
                    id: 1,
                    model: "provider-model".into(),
                    group: "allowed".into(),
                    enabled: true,
                    speeds: vec![Speed::Standard],
                    targets: vec![RouteTarget {
                        id: 1,
                        upstream_id: 1,
                        upstream_model: "provider-model".into(),
                        enabled: true,
                        priority: 0,
                        weight: 1,
                        speeds: vec![Speed::Standard],
                        payload: policy,
                        timeout_ms: 5_000,
                    }],
                    failover: FailoverPolicy {
                        max_attempts: 1,
                        retry_on: vec![],
                        allow_replay_after_send: false,
                    },
                }],
                aliases: vec![],
            },
            Arc::new(prices),
        )
        .unwrap(),
    )
}

fn gateway_with_routes(
    pool: &PgPool,
    router: Arc<lmm_core::routing::Router>,
    key: i64,
) -> ModelGateway {
    ModelGateway::new(
        pool.clone(),
        router,
        vec![UpstreamCredential {
            reference: CredentialRef { id: 7, revision: 1 },
            secret: "synthetic-upstream-key".into(),
        }],
        BTreeMap::from([(key, "allowed".into())]),
        Limits {
            total_timeout: Duration::from_secs(3),
            hook_timeout: Duration::from_secs(2),
            ..Limits::default()
        },
        ChargePolicy {
            input_token_ceiling: 100,
            lease_seconds: 30,
        },
    )
    .unwrap()
}

pub(super) struct Fixture {
    pub owner: PgPool,
    pub role: runtime::RuntimeRole,
    pub issuer: LmmIssuer,
    pub ledger: Ledger,
    pub charging: Charging,
    pub provider: Provider,
    pub user: i64,
    pub account: i64,
    pub refresh: String,
    pub native_key: String,
    pub base: String,
    pub client: reqwest::Client,
    stop: Option<oneshot::Sender<()>>,
    server: JoinHandle<()>,
}
impl Fixture {
    async fn new(owner: &PgPool, developer: bool) -> Self {
        let (native, ledger, _native_session, native_key, native_id, _native_account) =
            model_fixture::fixture(owner, 1000).await;
        let provider = Provider::start(true).await;
        let router = routes(&provider.endpoint, 0, 0, PayloadPolicy::Adapt);
        let store = native
            .with_registration_enabled(true)
            .with_dashboard_routing(router.clone());
        let browser = BrowserAuth::new(store.clone(), &[43; 32]).unwrap();
        browser
            .register(&BrowserRegistration {
                username: "pi-alice",
                password: "test passphrase",
                email: "",
            })
            .await
            .unwrap();
        let user: i64 = sqlx::query_scalar(
            "SELECT user_id FROM core_identity.user_profiles WHERE username='pi-alice'",
        )
        .fetch_one(owner)
        .await
        .unwrap();
        let account: i64 =
            sqlx::query_scalar("SELECT personal_account_id FROM core_identity.users WHERE id=$1")
                .bind(user)
                .fetch_one(owner)
                .await
                .unwrap();
        if developer {
            sqlx::query("INSERT INTO core_identity.developer_access(user_id,granted,source_reference) VALUES($1,true,'synthetic-pi-owner')").bind(user).execute(owner).await.unwrap();
        }
        sqlx::query("INSERT INTO core_identity.user_routing_groups(user_id,group_name,source_reference) VALUES($1,'allowed','synthetic-pi-owner')").bind(user).execute(owner).await.unwrap();
        // Wallet provisioning and funding are separate real ledger operations.
        ledger.open_wallet(account).await.unwrap();
        let funding = ledger
            .execute(&LedgerRequest {
                scope: "pi-oauth-test".into(),
                operation_key: "synthetic-pi-credit".into(),
                actor_user_id: Some(user),
                reason: "Synthetic disposable test funds".into(),
                action: Action::Credit {
                    account_id: account,
                    amount_units: Credits::try_from(1000).unwrap(),
                    payment_reference: "synthetic:pi-oauth".into(),
                },
            })
            .await
            .unwrap();
        assert!(matches!(
            funding,
            lmm_core::billing::ledger::Outcome::Posted { .. }
        ));
        assert_eq!(ledger.balance(account).await.unwrap().available_units, 1000);
        let session = browser
            .login(
                "pi-alice",
                "test passphrase",
                &BrowserSessionMetadata::default(),
            )
            .await
            .unwrap();
        let role = runtime::RuntimeRole::create(owner).await;
        for scope in ["core_charging", "core_model", "web_identity"] {
            role.optional(scope).await;
        }
        sqlx::raw_sql(&format!("GRANT USAGE ON SCHEMA core_oauth TO {0}; GRANT SELECT ON core_oauth.contract TO {0}; GRANT SELECT,INSERT ON core_oauth.families,core_oauth.authorization_requests,core_oauth.authorization_codes,core_oauth.access_tokens,core_oauth.refresh_tokens TO {0}; GRANT UPDATE(scopes,revoked_at) ON core_oauth.families TO {0}; GRANT UPDATE(consumed_at) ON core_oauth.authorization_requests,core_oauth.authorization_codes,core_oauth.refresh_tokens TO {0}; GRANT EXECUTE ON FUNCTION core_oauth.readonly_scope_set(text[]),core_oauth.client_scope_set(text,text[],boolean) TO {0};",role.name)).execute(owner).await.unwrap();
        runtime_database::check(
            &role.pool,
            RequiredCapabilities {
                models: true,
                oauth2: true,
                web_identity: true,
                ..Default::default()
            },
        )
        .await
        .unwrap();
        let gateway =
            gateway_with_routes(&role.pool, router.clone(), native_id).with_local_streaming();
        let store = IdentityStore::from_pool(role.pool.clone()).with_dashboard_routing(router);
        let profile = LmmProfile::new(ISSUER, vec!["allowed".into()])
            .unwrap()
            .with_pi_budget_limit(1000)
            .unwrap();
        let issuer = LmmIssuer::open(store.clone(), profile, 900, 86400)
            .await
            .unwrap()
            .with_catalog_surface(gateway.oauth_catalog_surface());
        let gateway = gateway.with_oauth2_issuer(issuer.clone());
        let config = LegacyAuthConfig::new(ISSUER, &[43; 32])
            .unwrap()
            .with_oauth2_issuer(issuer.clone())
            .unwrap();
        let app = gateway
            .router()
            .merge(oauth2_http::router(issuer.clone()))
            .merge(legacy_auth::router(store, config));
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let base = format!("http://{}", listener.local_addr().unwrap());
        let (stop, stopped) = oneshot::channel();
        let server = tokio::spawn(async move {
            axum::serve(
                listener,
                app.into_make_service_with_connect_info::<std::net::SocketAddr>(),
            )
            .with_graceful_shutdown(async move {
                let _ = stopped.await;
            })
            .await
            .unwrap();
        });
        let charging = Charging::new(role.pool.clone(), Arc::new(TransactionalLedger));
        Self {
            owner: owner.clone(),
            role,
            issuer,
            ledger,
            charging,
            provider,
            user,
            account,
            refresh: session.refresh_token,
            native_key,
            base,
            client: reqwest::Client::builder()
                .redirect(reqwest::redirect::Policy::none())
                .timeout(Duration::from_secs(5))
                .build()
                .unwrap(),
            stop: Some(stop),
            server,
        }
    }
    pub fn request(&self) -> AuthorizationRequest {
        AuthorizationRequest{response_type:"code".into(),client_id:"lmm-pi".into(),redirect_uri:REDIRECT.into(),scope:"catalog:read balance:read usage:read models:invoke mcp:bounties mcp:drawing remote:control".into(),resource:format!("{ISSUER}/api/oauth2"),code_challenge:URL_SAFE_NO_PAD.encode(Sha256::digest(VERIFIER)),code_challenge_method:"S256".into(),state:"synthetic-pi-state".into()}
    }
    pub async fn token(&self, cap: i64) -> TokenWire {
        let request = self.request();
        let mut url = reqwest::Url::parse(&format!("{}/api/oauth2/authorize", self.base)).unwrap();
        url.query_pairs_mut().extend_pairs([
            ("response_type", request.response_type.as_str()),
            ("client_id", request.client_id.as_str()),
            ("redirect_uri", request.redirect_uri.as_str()),
            ("scope", request.scope.as_str()),
            ("resource", request.resource.as_str()),
            ("code_challenge", request.code_challenge.as_str()),
            (
                "code_challenge_method",
                request.code_challenge_method.as_str(),
            ),
            ("state", request.state.as_str()),
        ]);
        let response = self.client.get(url).send().await.unwrap();
        assert_eq!(response.status(), reqwest::StatusCode::OK);
        let binding = flow_cookie(&response);
        let csrf = flow_csrf(&response.text().await.unwrap());
        let browser = format!("new_api_refresh={}", self.refresh);
        let response = self
            .client
            .post(format!("{}/api/user/auth/oauth2/continue", self.base))
            .header("origin", ISSUER)
            .header("cookie", format!("{binding}; {browser}"))
            .header("content-type", "application/x-www-form-urlencoded")
            .body(form(&[("csrf", &csrf)]))
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), reqwest::StatusCode::OK);
        let binding = flow_cookie(&response);
        let html = response.text().await.unwrap();
        assert!(html.contains("name=budget_credits"));
        let csrf = flow_csrf(&html);
        let response = self
            .client
            .post(format!("{}/api/user/auth/oauth2/consent", self.base))
            .header("origin", ISSUER)
            .header("cookie", format!("{binding}; {browser}"))
            .header("content-type", "application/x-www-form-urlencoded")
            .body(form(&[
                ("csrf", &csrf),
                ("decision", "allow"),
                ("budget_credits", &cap.to_string()),
            ]))
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), reqwest::StatusCode::SEE_OTHER);
        let url = reqwest::Url::parse(response.headers()["location"].to_str().unwrap()).unwrap();
        assert_eq!(url.origin().ascii_serialization(), "http://127.0.0.1:38423");
        let code = url
            .query_pairs()
            .find(|(k, _)| k == "code")
            .unwrap()
            .1
            .into_owned();
        let response = self
            .client
            .post(format!("{}/api/oauth2/token", self.base))
            .header("content-type", "application/x-www-form-urlencoded")
            .body(form(&[
                ("grant_type", "authorization_code"),
                ("client_id", "lmm-pi"),
                ("resource", &format!("{ISSUER}/api/oauth2")),
                ("code", &code),
                ("redirect_uri", REDIRECT),
                ("code_verifier", VERIFIER),
            ]))
            .send()
            .await
            .unwrap();
        assert_eq!(response.status(), reqwest::StatusCode::OK);
        let value: serde_json::Value = response.json().await.unwrap();
        assert_eq!(value["token_type"], "Bearer");
        assert!((1..=900).contains(&value["expires_in"].as_i64().unwrap()));
        TokenWire {
            access_token: value["access_token"].as_str().unwrap().into(),
            refresh_token: value["refresh_token"].as_str().unwrap().into(),
            token_type: "Bearer",
            expires_in: value["expires_in"].as_i64().unwrap(),
            scope: value["scope"].as_str().unwrap().into(),
        }
    }
    pub async fn refresh(
        &self,
        token: &str,
        scope: Option<String>,
    ) -> Result<TokenWire, IdentityError> {
        let form = TokenForm {
            grant_type: "refresh_token".into(),
            client_id: "lmm-pi".into(),
            resource: format!("{ISSUER}/api/oauth2"),
            code: None,
            redirect_uri: None,
            code_verifier: None,
            refresh_token: Some(token.into()),
            scope,
        };
        self.issuer
            .token(form.validate(self.issuer.profile())?)
            .await
    }
    pub fn spec(&self, id: &str, amount: i64) -> ChargeRequest {
        ChargeRequest {
            id: id.into(),
            fingerprint: Sha256::digest(id.as_bytes()).into(),
            price: Price {
                upstream_cost_group_ppm: None,
                version: "1:1:1".into(),
                model: "provider-model".into(),
                group: "allowed".into(),
                input_per_million: 1_000_000,
                cached_per_million: 250_000,
                output_per_million: 2_000_000,
            },
            reserve: amount,
            lease_seconds: 30,
            maximum_seconds: 30,
        }
    }
    pub async fn reserve(
        &self,
        token: &str,
        spec: &ChargeRequest,
        worker: &[u8; 32],
    ) -> lmm_core::billing::charging::Result<lmm_core::billing::charging::Charge> {
        for attempt in 0..5 {
            match self
                .charging
                .reserve_oauth_with_context(&self.issuer, token, spec, worker, None)
                .await
            {
                Err(lmm_core::billing::charging::Error::Retry) if attempt < 4 => {
                    tokio::time::sleep(Duration::from_millis(10)).await
                }
                result => return result,
            }
        }
        unreachable!()
    }
    async fn close(mut self) {
        if let Some(stop) = self.stop.take() {
            let _ = stop.send(());
        }
        tokio::time::timeout(Duration::from_secs(5), self.server)
            .await
            .unwrap()
            .unwrap();
        drop(self.issuer);
        drop(self.charging);
        drop(self.client);
        drop(self.provider);
        self.role.cleanup().await;
    }
}
pub(super) async fn with_fixture<F, Fut>(pool: PgPool, developer: bool, case: F)
where
    F: FnOnce(Arc<Fixture>) -> Fut + Send + 'static,
    Fut: Future<Output = ()> + Send + 'static,
{
    let fixture = Arc::new(Fixture::new(&pool, developer).await);
    let copy = fixture.clone();
    let result = tokio::spawn(async move { case(copy).await }).await;
    let fixture = match Arc::try_unwrap(fixture) {
        Ok(f) => f,
        Err(_) => panic!("Pi fixture retained unexpected reference"),
    };
    fixture.close().await;
    if let Err(error) = result {
        if error.is_panic() {
            std::panic::resume_unwind(error.into_panic());
        }
        panic!("Pi test task cancelled");
    }
}

fn form(pairs: &[(&str, &str)]) -> String {
    let mut serializer = reqwest::Url::parse("http://127.0.0.1").unwrap();
    serializer
        .query_pairs_mut()
        .extend_pairs(pairs.iter().copied());
    serializer.query().unwrap().into()
}
fn flow_csrf(html: &str) -> String {
    html.split_once("name=csrf value=\"")
        .unwrap()
        .1
        .split_once('"')
        .unwrap()
        .0
        .into()
}
fn flow_cookie(response: &reqwest::Response) -> String {
    let cookies: Vec<_> = response
        .headers()
        .get_all("set-cookie")
        .iter()
        .map(|v| v.to_str().unwrap())
        .filter(|v| v.contains("Max-Age=300"))
        .collect();
    assert_eq!(cookies.len(), 1);
    cookies[0].split(';').next().unwrap().into()
}
