#![allow(clippy::unwrap_used)]

use super::*;
use base64::{Engine, engine::general_purpose::URL_SAFE_NO_PAD};
use std::{
    cell::{Cell, RefCell},
    io::{Read, Write},
    net::TcpListener,
    thread,
};

struct MemoryStore {
    raw: RefCell<Option<String>>,
    writes: Cell<usize>,
    fail_write: Option<usize>,
}

impl MemoryStore {
    fn new(issuer: &str, fail_write: Option<usize>) -> Self {
        let credential = Credential {
            issuer: issuer.into(),
            access: token("lmm_at_", 1),
            refresh: token("lmm_rt_", 2),
            scope: "catalog:read balance:read group:ZGVmYXVsdA".into(),
            expires_at: 1,
        };
        let session = StoredSession {
            credential,
            refresh_blocked: false,
        };
        Self {
            raw: RefCell::new(Some(serde_json::to_string(&session).unwrap())),
            writes: Cell::new(0),
            fail_write,
        }
    }
}

impl SecretStore for MemoryStore {
    fn load(&self) -> Result<Option<StoredSession>, AuthError> {
        Ok(self
            .raw
            .borrow()
            .as_ref()
            .map(|raw| serde_json::from_str(raw).unwrap()))
    }
    fn save(&self, session: &StoredSession) -> Result<(), AuthError> {
        self.writes.set(self.writes.get() + 1);
        if self.fail_write == Some(self.writes.get()) {
            return Err(AuthError::CredentialStore);
        }
        *self.raw.borrow_mut() = Some(serde_json::to_string(session).unwrap());
        Ok(())
    }
    fn delete(&self) -> Result<(), AuthError> {
        *self.raw.borrow_mut() = None;
        Ok(())
    }
}

fn token(prefix: &str, value: u8) -> String {
    format!("{prefix}{}", URL_SAFE_NO_PAD.encode([value; 32]))
}

fn token_body() -> String {
    serde_json::json!({"access_token":token("lmm_at_",3),"refresh_token":token("lmm_rt_",4),"expires_in":900,"token_type":"Bearer","scope":"catalog:read balance:read group:ZGVmYXVsdA"}).to_string()
}

/// Test-only HTTP transport. Production only constructs HTTPS-only clients.
fn transport(issuer: &str) -> Transport {
    Transport {
        issuer: issuer.into(),
        resource: format!("{issuer}/api/oauth2"),
        client: Client::builder()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .timeout(Duration::from_secs(2))
            .build()
            .unwrap(),
    }
}

fn server(responses: Vec<(u16, String)>) -> (Transport, thread::JoinHandle<Vec<String>>) {
    let listener = TcpListener::bind("127.0.0.1:0").unwrap();
    let issuer = format!("http://{}", listener.local_addr().unwrap());
    let server_issuer = issuer.clone();
    let task = thread::spawn(move || {
        let mut requests = vec![];
        listener.set_nonblocking(true).unwrap();
        for (status, body) in responses {
            let body = body.replace("__ISSUER__", &server_issuer);
            let started = std::time::Instant::now();
            let mut stream = loop {
                match listener.accept() {
                    Ok((stream, _)) => break stream,
                    Err(error) if error.kind() == std::io::ErrorKind::WouldBlock => {
                        assert!(
                            started.elapsed() < Duration::from_secs(5),
                            "expected request did not arrive"
                        );
                        thread::sleep(Duration::from_millis(10));
                    }
                    Err(error) => panic!("test listener: {error}"),
                }
            };
            stream
                .set_read_timeout(Some(Duration::from_secs(2)))
                .unwrap();
            let mut data = Vec::new();
            let mut byte = [0];
            while !data.ends_with(b"\r\n\r\n") {
                stream.read_exact(&mut byte).unwrap();
                data.push(byte[0]);
                assert!(data.len() <= 16 * 1024);
            }
            let header = String::from_utf8(data.clone()).unwrap();
            let len = header
                .lines()
                .find_map(|line| {
                    line.to_lowercase()
                        .strip_prefix("content-length:")
                        .map(|value| value.trim().parse::<usize>().unwrap())
                })
                .unwrap_or(0);
            let mut payload = vec![0; len];
            stream.read_exact(&mut payload).unwrap();
            data.extend(payload);
            requests.push(String::from_utf8(data).unwrap());
            let location = if status == 302 {
                format!("Location: {server_issuer}/redirected\r\n")
            } else {
                String::new()
            };
            write!(stream, "HTTP/1.1 {status} Test\r\n{location}Content-Type: application/json\r\nContent-Length: {}\r\nConnection: close\r\n\r\n{body}", body.len()).unwrap();
        }
        requests
    });
    (transport(&issuer), task)
}

#[test]
fn refresh_rotation_is_saved_and_uses_readonly_client_contract() {
    let (transport, task) = server(vec![(200, token_body())]);
    let store = MemoryStore::new(&transport.issuer, None);
    let ready = refresh(&transport, &store, store.load().unwrap().unwrap()).unwrap();
    assert!(!ready.refresh_blocked);
    assert_eq!(ready.credential.refresh, token("lmm_rt_", 4));
    assert_eq!(store.writes.get(), 2);
    let request = &task.join().unwrap()[0];
    let body = request.split_once("\r\n\r\n").unwrap().1;
    let form: std::collections::HashMap<_, _> =
        url::form_urlencoded::parse(body.as_bytes()).collect();
    assert_eq!(form["client_id"], "lmm");
    assert_eq!(form["resource"], transport.resource);
    assert!(!form.contains_key("scope"));
}

#[test]
fn ambiguous_refresh_failure_persists_block_and_never_replays() {
    let (transport, task) = server(vec![(500, "private-server-body".into())]);
    let store = MemoryStore::new(&transport.issuer, None);
    let error = refresh(&transport, &store, store.load().unwrap().unwrap())
        .err()
        .unwrap();
    assert_eq!(error, AuthError::Network);
    assert!(!error.to_string().contains("private-server-body"));
    let stored = store.load().unwrap().unwrap();
    assert!(stored.refresh_blocked);
    assert!(matches!(
        refresh(&transport, &store, stored),
        Err(AuthError::RefreshBlocked)
    ));
    assert_eq!(task.join().unwrap().len(), 1);
}

#[test]
fn failed_preflight_save_prevents_refresh_network_request() {
    let transport = transport("http://127.0.0.1:1");
    let store = MemoryStore::new(&transport.issuer, Some(1));
    assert!(matches!(
        refresh(&transport, &store, store.load().unwrap().unwrap()),
        Err(AuthError::CredentialStore)
    ));
    assert!(!store.load().unwrap().unwrap().refresh_blocked);
}

#[test]
fn failed_rotated_token_storage_revokes_replacement_and_leaves_replay_blocked() {
    let (transport, task) = server(vec![(200, token_body()), (200, String::new())]);
    let store = MemoryStore::new(&transport.issuer, Some(2));
    assert!(matches!(
        refresh(&transport, &store, store.load().unwrap().unwrap()),
        Err(AuthError::CredentialStore)
    ));
    assert!(store.load().unwrap().unwrap().refresh_blocked);
    let requests = task.join().unwrap();
    assert!(requests[1].starts_with("POST /api/oauth2/revoke "));
    assert!(requests[1].contains(&token("lmm_rt_", 4)));
}

#[test]
fn logout_retains_credential_until_remote_revocation_is_confirmed() {
    let (transport, task) = server(vec![(503, "unavailable".into()), (200, String::new())]);
    let store = MemoryStore::new(&transport.issuer, None);
    assert_eq!(logout_from(&transport, &store), Err(AuthError::Network));
    assert!(store.load().unwrap().is_some());
    assert_eq!(
        logout_from(&transport, &store),
        Ok("logged_out_cli_grant_revoked")
    );
    assert!(store.load().unwrap().is_none());
    assert_eq!(task.join().unwrap().len(), 2);
}

#[test]
fn transport_does_not_follow_redirects_or_emit_response_secrets() {
    let (transport, task) = server(vec![(302, "DO_NOT_PRINT".into())]);
    let response = transport.client.get(&transport.issuer).send().unwrap();
    assert_eq!(response.status(), reqwest::StatusCode::FOUND);
    assert_eq!(task.join().unwrap().len(), 1);
}

#[test]
fn oversized_response_is_rejected_before_parsing() {
    let (transport, task) = server(vec![(200, "x".repeat(2048))]);
    assert!(matches!(
        transport.request(transport.client.get(&transport.issuer), 1024),
        Err(AuthError::InvalidResponse)
    ));
    assert_eq!(task.join().unwrap().len(), 1);
}

#[test]
fn login_completes_discovery_callback_pkce_exchange_and_secret_storage() {
    use sha2::{Digest, Sha256};
    let metadata = serde_json::json!({
        "issuer":"__ISSUER__", "authorization_endpoint":"__ISSUER__/api/oauth2/authorize",
        "token_endpoint":"__ISSUER__/api/oauth2/token", "revocation_endpoint":"__ISSUER__/api/oauth2/revoke",
        "authorization_response_iss_parameter_supported":true,"code_challenge_methods_supported":["S256"],
        "response_types_supported":["code"], "token_endpoint_auth_methods_supported":["none"]
    });
    let resource = serde_json::json!({"resource":"__ISSUER__/api/oauth2","authorization_servers":["__ISSUER__"]});
    let (transport, task) = server(vec![
        (200, metadata.to_string()),
        (200, resource.to_string()),
        (200, token_body()),
    ]);
    let store = MemoryStore::new(&transport.issuer, None);
    store.delete().unwrap();
    let mut browser = None;
    let mut challenge = String::new();
    let status = login_with(&transport, &store, Duration::from_secs(5), |url| {
        let url = url::Url::parse(url).unwrap();
        let query: std::collections::HashMap<String, String> = url
            .query_pairs()
            .map(|(key, value)| (key.into_owned(), value.into_owned()))
            .collect();
        assert_eq!(query["client_id"], "lmm");
        assert_eq!(query["scope"], SCOPE);
        challenge = query["code_challenge"].clone();
        let mut redirect = url::Url::parse(&query["redirect_uri"]).unwrap();
        redirect.query_pairs_mut().extend_pairs([
            ("state", query["state"].as_str()),
            ("iss", transport.issuer.as_str()),
            ("code", "lmm_oa_testcode"),
        ]);
        browser = Some(thread::spawn(move || {
            let host = format!("127.0.0.1:{}", redirect.port().unwrap());
            let mut stream = std::net::TcpStream::connect(&host).unwrap();
            stream
                .set_read_timeout(Some(Duration::from_secs(3)))
                .unwrap();
            write!(
                stream,
                "GET {}?{} HTTP/1.1\r\nHost: {host}\r\n\r\n",
                redirect.path(),
                redirect.query().unwrap()
            )
            .unwrap();
            let mut response = String::new();
            stream.read_to_string(&mut response).unwrap();
            assert!(response.starts_with("HTTP/1.1 200"));
        }));
        Ok(())
    })
    .unwrap();
    browser.unwrap().join().unwrap();
    assert_eq!(status.outcome, "logged_in");
    assert!(!status.applications_changed);
    assert!(store.load().unwrap().is_some());
    let requests = task.join().unwrap();
    let form: std::collections::HashMap<_, _> =
        url::form_urlencoded::parse(requests[2].split_once("\r\n\r\n").unwrap().1.as_bytes())
            .collect();
    assert_eq!(
        URL_SAFE_NO_PAD.encode(Sha256::digest(form["code_verifier"].as_bytes())),
        challenge
    );
    assert_eq!(form["code"], "lmm_oa_testcode");
    assert!(!serde_json::to_string(&status).unwrap().contains("lmm_at_"));
}

#[test]
fn existing_login_is_not_silently_replaced_or_reauthorized() {
    let transport = transport("http://127.0.0.1:1");
    let store = MemoryStore::new(&transport.issuer, None);
    assert!(matches!(
        login_with(&transport, &store, Duration::from_secs(1), |_| panic!(
            "must not open browser"
        )),
        Err(AuthError::AlreadyLoggedIn)
    ));
    assert_eq!(store.writes.get(), 0);
}
