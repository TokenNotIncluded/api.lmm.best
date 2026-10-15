//! Actual TLS fixture; no model/provider account or production credential.
use super::*;
use std::{
    fs,
    io::{BufRead, BufReader},
    process::{Child, Command, Stdio},
    time::{Duration, SystemTime, UNIX_EPOCH},
};

struct Server(Child);
impl Drop for Server {
    fn drop(&mut self) {
        let _ = self.0.kill();
        let _ = self.0.wait();
    }
}

const PYTHON_SERVER: &str = r#"
import http.server,ssl,json,sys,os
from pathlib import Path
root=Path(sys.argv[1]); records=[]
class Handler(http.server.BaseHTTPRequestHandler):
    protocol_version='HTTP/1.1'
    def log_message(self,*args): pass
    def do_POST(self):
        self.rfile.read(int(self.headers.get('Content-Length','0')))
        records.append({'host':self.headers.get('Host'),'path':self.path,'authorization':self.headers.get('Authorization')})
        fd=os.open(root/'captured.json',os.O_WRONLY|os.O_CREAT|os.O_TRUNC,0o600)
        with os.fdopen(fd,'w') as out: json.dump(records,out)
        if self.path=='/redirect':
            self.send_response(307); self.send_header('Location','https://other.invalid/never')
            self.send_header('Content-Length','0'); self.end_headers(); return
        body=b'{"ok":true}'
        self.send_response(200); self.send_header('Content-Type','application/json')
        self.send_header('Content-Length',str(len(body))); self.end_headers(); self.wfile.write(body)
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER)
context.load_cert_chain(root/'leaf.pem',root/'leaf.key')
server.socket=context.wrap_socket(server.socket,server_side=True)
print(server.server_port,flush=True); server.serve_forever()
"#;

fn openssl(root: &std::path::Path, args: &[&str]) {
    let status = Command::new("openssl")
        .args(args)
        .current_dir(root)
        .stdout(Stdio::null())
        .stderr(Stdio::null())
        .status()
        .expect("openssl is required for real TLS fixture");
    assert!(status.success(), "test TLS certificate generation failed");
}

#[cfg(unix)]
#[tokio::test]
async fn real_tls_checks_chain_hostname_origin_pinning_and_no_redirect() {
    use std::os::unix::fs::PermissionsExt;
    let root = std::env::temp_dir().join(format!(
        "lmm-tls-{}-{}",
        std::process::id(),
        SystemTime::now()
            .duration_since(UNIX_EPOCH)
            .unwrap()
            .as_nanos()
    ));
    fs::create_dir(&root).unwrap();
    fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
    openssl(
        &root,
        &[
            "req",
            "-x509",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-keyout",
            "ca.key",
            "-out",
            "ca.pem",
            "-subj",
            "/CN=LMM isolated test CA",
            "-days",
            "1",
            "-sha256",
        ],
    );
    openssl(
        &root,
        &[
            "req",
            "-new",
            "-newkey",
            "rsa:2048",
            "-nodes",
            "-keyout",
            "leaf.key",
            "-out",
            "leaf.csr",
            "-subj",
            "/CN=fixture.example",
        ],
    );
    fs::write(root.join("extensions.txt"),"basicConstraints=CA:FALSE\nkeyUsage=digitalSignature,keyEncipherment\nextendedKeyUsage=serverAuth\nsubjectAltName=DNS:fixture.example,IP:127.0.0.1\n").unwrap();
    openssl(
        &root,
        &[
            "x509",
            "-req",
            "-in",
            "leaf.csr",
            "-CA",
            "ca.pem",
            "-CAkey",
            "ca.key",
            "-CAcreateserial",
            "-out",
            "leaf.pem",
            "-days",
            "1",
            "-sha256",
            "-extfile",
            "extensions.txt",
        ],
    );
    let mut server = Server(
        Command::new("python3")
            .arg("-u")
            .arg("-c")
            .arg(PYTHON_SERVER)
            .arg(&root)
            .stdout(Stdio::piped())
            .stderr(Stdio::null())
            .spawn()
            .unwrap(),
    );
    let stdout = server.0.stdout.take().unwrap();
    let (sender, receiver) = std::sync::mpsc::channel();
    let reader = std::thread::spawn(move || {
        let mut line = String::new();
        let result = BufReader::new(stdout).read_line(&mut line).map(|_| line);
        let _ = sender.send(result);
    });
    let port: u16 = receiver
        .recv_timeout(Duration::from_secs(5))
        .expect("TLS fixture startup deadline")
        .unwrap()
        .trim()
        .parse()
        .unwrap();
    reader.join().unwrap();
    let ca = Certificate::from_pem(&fs::read(root.join("ca.pem")).unwrap()).unwrap();
    let url = Url::parse(&format!("https://127.0.0.1:{port}/v1/responses")).unwrap();
    let network = NetworkTarget {
        local_http: true,
        additional_root: Some(ca.clone()),
        allowed_origins: [canonical_origin(&url)].into(),
    };
    let limits = Limits::default();
    let prepared = network
        .prepare(&url, &limits, Arc::new(Semaphore::new(1)))
        .await
        .unwrap();
    let response = prepared
        .client
        .post(url.clone())
        .header("authorization", "Bearer isolated-tls-test")
        .body("{}")
        .send()
        .await
        .unwrap();
    assert!(prepared.matches_peer(response.remote_addr()));
    assert_eq!(
        response.json::<serde_json::Value>().await.unwrap()["ok"],
        true
    );
    // Without the explicitly configured root, the actual TLS handshake fails.
    let untrusted = NetworkTarget {
        additional_root: None,
        ..network.clone()
    }
    .prepare(&url, &limits, Arc::new(Semaphore::new(1)))
    .await
    .unwrap();
    assert!(
        untrusted
            .client
            .post(url.clone())
            .body("{}")
            .send()
            .await
            .is_err()
    );
    assert!(
        NetworkTarget {
            local_http: false,
            ..network.clone()
        }
        .prepare(&url, &limits, Arc::new(Semaphore::new(1)))
        .await
        .is_err()
    );
    // Connector primitive test: pin a fixture hostname to the owned loopback
    // endpoint while retaining its real SNI/Host certificate identity. This
    // deliberately does not claim DNS->loopback is a permitted runtime target.
    let address = SocketAddr::new("127.0.0.1".parse().unwrap(), port);
    let client = client_builder(&limits)
        .dns_resolver(Arc::new(RejectDns))
        .resolve_to_addrs("fixture.example", &[address])
        .resolve_to_addrs("wrong.example", &[address])
        .add_root_certificate(ca)
        .build()
        .unwrap();
    let response = client
        .post(format!("https://fixture.example:{port}/pinned"))
        .header("authorization", "Bearer isolated-tls-test")
        .body("{}")
        .send()
        .await
        .unwrap();
    assert_eq!(
        response.json::<serde_json::Value>().await.unwrap()["ok"],
        true
    );
    assert!(
        client
            .post(format!("https://wrong.example:{port}/wrong"))
            .body("{}")
            .send()
            .await
            .is_err()
    );
    let error = client
        .post(format!("https://unknown.example:{port}/fallback"))
        .body("{}")
        .send()
        .await
        .unwrap_err();
    let mut cause: &dyn std::error::Error = &error;
    let mut denied = false;
    loop {
        denied |= cause.to_string().contains("unconfigured DNS lookup");
        let Some(next) = cause.source() else {
            break;
        };
        cause = next;
    }
    assert!(
        denied,
        "connector must never fall back to another DNS lookup"
    );
    let redirect = client
        .post(format!("https://fixture.example:{port}/redirect"))
        .body("{}")
        .send()
        .await
        .unwrap();
    assert_eq!(redirect.status().as_u16(), 307);
    let captured: serde_json::Value =
        serde_json::from_slice(&fs::read(root.join("captured.json")).unwrap()).unwrap();
    let records = captured.as_array().unwrap();
    assert_eq!(records.len(), 3);
    assert_eq!(records[0]["host"], format!("127.0.0.1:{port}"));
    assert_eq!(records[1]["host"], format!("fixture.example:{port}"));
    assert_eq!(records[1]["authorization"], "Bearer isolated-tls-test");
    assert_eq!(records[2]["path"], "/redirect");
    // Keep private fixture evidence; the controlled server stops via Drop.
}
