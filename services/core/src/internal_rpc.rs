//! Bounded protobuf control and durable event plane over a private Unix socket.
//! Never calls Go; public model/identity routes do not depend on this listener.
use crate::{
    accounts,
    identity::{CredentialKind, IdentityError, IdentityStore},
};
use fs2::FileExt;
use sha2::{Digest, Sha256};
use sqlx::postgres::PgPoolOptions;
use std::{
    fs::{self, File, OpenOptions},
    future::Future,
    io,
    os::unix::fs::{FileTypeExt, MetadataExt, OpenOptionsExt, PermissionsExt},
    path::{Path, PathBuf},
    pin::Pin,
    sync::Arc,
    task::{Context, Poll},
    time::Duration,
};
use subtle::ConstantTimeEq;
use tokio::{
    io::{AsyncRead, AsyncWrite, ReadBuf},
    net::{UnixListener, UnixStream},
    sync::{OwnedSemaphorePermit, Semaphore, watch},
};
use tokio_stream::{StreamExt, wrappers::UnixListenerStream};
use tonic::transport::server::{Connected, UdsConnectInfo};
use tonic::{Request, Response, Status, metadata::MetadataMap, transport::Server};

mod event_service;

pub mod pb {
    tonic::include_proto!("lmm.core.v1");
}
use pb::core_control_server::{CoreControl, CoreControlServer};

pub const MAX_MESSAGE: usize = 64 * 1024;
pub const MAX_CALLS: usize = 8;
const MAX_CONNECTIONS: usize = 8;
const DEADLINE: Duration = Duration::from_secs(2);

fn invalid(message: &'static str) -> io::Error {
    io::Error::new(io::ErrorKind::InvalidInput, message)
}
fn valid_service_token(value: &[u8]) -> bool {
    (32..=256).contains(&value.len()) && value.iter().all(u8::is_ascii_graphic)
}
pub fn read_service_token(path: &Path) -> io::Result<Vec<u8>> {
    let bytes = crate::config::private_file(path, 4096)?;
    let value = std::str::from_utf8(&bytes)
        .map_err(|_| invalid("RPC credential is not UTF-8"))?
        .trim()
        .as_bytes();
    if !valid_service_token(value) {
        return Err(invalid(
            "RPC credential must contain 32 to 256 visible ASCII bytes",
        ));
    }
    Ok(value.to_vec())
}
/// Separate pool: extension queries cannot consume the public core pool.
/// PostgreSQL also rejects writes on these connections.
pub async fn readonly_store(url: &str) -> Result<IdentityStore, IdentityError> {
    let pool = PgPoolOptions::new()
        .max_connections(2)
        .min_connections(0)
        .idle_timeout(Some(Duration::from_secs(30)))
        .acquire_timeout(Duration::from_millis(400))
        .after_connect(|c, _| {
            Box::pin(async move {
                sqlx::query("SET statement_timeout='1500ms'")
                    .execute(&mut *c)
                    .await?;
                sqlx::query("SET idle_in_transaction_session_timeout='2s'")
                    .execute(&mut *c)
                    .await?;
                sqlx::query("SET default_transaction_read_only=on")
                    .execute(&mut *c)
                    .await?;
                Ok(())
            })
        })
        .connect(url)
        .await?;
    let store = IdentityStore::from_pool(pool);
    store.check_schema().await?;
    Ok(store)
}
fn single<'a>(metadata: &'a MetadataMap, key: &'static str) -> Result<&'a str, Status> {
    let mut values = metadata.get_all(key).iter();
    let value = values
        .next()
        .ok_or_else(|| Status::unauthenticated("missing credential or protocol metadata"))?;
    if values.next().is_some() {
        return Err(Status::unauthenticated(
            "duplicate credential or protocol metadata",
        ));
    }
    value
        .to_str()
        .map_err(|_| Status::unauthenticated("invalid credential or protocol metadata"))
}
fn identity_status(error: IdentityError) -> Status {
    match error {
        IdentityError::Unauthorized => Status::unauthenticated("invalid user credential"),
        IdentityError::Forbidden => Status::permission_denied("operation not permitted"),
        IdentityError::Invalid => Status::invalid_argument("invalid request"),
        IdentityError::Conflict => Status::aborted("state changed"),
        IdentityError::Storage => Status::unavailable("identity storage unavailable"),
    }
}
fn account(value: accounts::Account) -> pb::Account {
    pb::Account {
        id: value.id,
        kind: match value.kind {
            accounts::AccountKind::Personal => pb::AccountKind::Personal as i32,
            accounts::AccountKind::Team => pb::AccountKind::Team as i32,
        },
    }
}
#[derive(Clone)]
struct Control {
    token_digest: [u8; 32],
    store: Option<IdentityStore>,
    // Unconfigured until reviewed OAuth component/runtime/purpose wiring exists.
    grants: Option<crate::identity::grants::GrantStore>,
    events: Option<crate::events::EventStore>,
    service_id: String,
    commerce_features: Vec<String>,
    slots: Arc<Semaphore>,
}
impl Control {
    fn new(token: &[u8], store: Option<IdentityStore>) -> io::Result<Self> {
        if !valid_service_token(token) {
            return Err(invalid("invalid RPC service credential"));
        }
        Ok(Self {
            token_digest: Sha256::digest(token).into(),
            store,
            grants: None,
            events: None,
            service_id: "extensions".into(),
            commerce_features: Vec::new(),
            slots: Arc::new(Semaphore::new(MAX_CALLS)),
        })
    }
    fn check(&self, metadata: &MetadataMap) -> Result<(), Status> {
        let value = single(metadata, "authorization")?
            .strip_prefix("Bearer ")
            .ok_or_else(|| Status::unauthenticated("invalid service credential"))?;
        if !valid_service_token(value.as_bytes()) {
            return Err(Status::unauthenticated("invalid service credential"));
        }
        let digest: [u8; 32] = Sha256::digest(value.as_bytes()).into();
        if !bool::from(self.token_digest.ct_eq(&digest)) {
            return Err(Status::unauthenticated("invalid service credential"));
        }
        if single(metadata, "x-lmm-protocol")? != "1" {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        Ok(())
    }
    fn user(metadata: &MetadataMap) -> Result<&str, Status> {
        let value = single(metadata, "x-lmm-user-credential")?;
        if !(32..=256).contains(&value.len())
            || !value
                .bytes()
                .all(|b| b.is_ascii_alphanumeric() || b == b'_' || b == b'-')
        {
            return Err(Status::unauthenticated("invalid user credential"));
        }
        Ok(value)
    }
    fn store(&self) -> Result<&IdentityStore, Status> {
        self.store
            .as_ref()
            .ok_or_else(|| Status::unavailable("identity is not configured"))
    }
    async fn bounded<T>(
        &self,
        call: impl Future<Output = Result<T, Status>>,
    ) -> Result<Response<T>, Status> {
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("core RPC is busy"))?;
        tokio::time::timeout(DEADLINE, call)
            .await
            .map_err(|_| Status::deadline_exceeded("core RPC deadline exceeded"))?
            .map(Response::new)
    }
}
#[tonic::async_trait]
impl CoreControl for Control {
    async fn capabilities(
        &self,
        request: Request<pb::CapabilitiesRequest>,
    ) -> Result<Response<pb::CapabilitiesResponse>, Status> {
        self.check(request.metadata())?;
        self.bounded(async {
            Ok(pb::CapabilitiesResponse {
                protocol_major: 1,
                identity_available: self.store.is_some(),
                features: {
                    let mut features = Vec::new();
                    if self.store.is_some() {
                        features.extend(["identity.read".into(), "teams.read".into()]);
                    }
                    if self.events.is_some() {
                        features.extend(crate::events::FEATURES.iter().map(|v| (*v).into()));
                    }
                    // Discovery reports mounted commerce methods. Their
                    // independent credential still gates every money RPC.
                    features.extend(self.commerce_features.iter().cloned());
                    features
                },
            })
        })
        .await
    }
    async fn authorize(
        &self,
        request: Request<pb::AuthorizeRequest>,
    ) -> Result<Response<pb::AuthorizeResponse>, Status> {
        self.check(request.metadata())?;
        let credential = Self::user(request.metadata())?;
        self.bounded(async {
            let actor = self
                .store()?
                .authorize(credential)
                .await
                .map_err(identity_status)?;
            Ok(pb::AuthorizeResponse {
                credential_id: actor.credential_id,
                credential_kind: match actor.credential_kind {
                    CredentialKind::Session => pb::CredentialKind::Session as i32,
                    CredentialKind::ApiKey => pb::CredentialKind::ApiKey as i32,
                },
                user_id: actor.user_id,
                platform_level: i32::from(actor.platform_level),
                owner: Some(account(actor.owner)),
                funding_accounts: actor.funding_accounts.into_iter().map(account).collect(),
            })
        })
        .await
    }
    async fn authorize_remote(
        &self,
        request: Request<pb::AuthorizeRemoteRequest>,
    ) -> Result<Response<pb::AuthorizeRemoteResponse>, Status> {
        use crate::identity::grants::RemoteSource;
        self.check(request.metadata())?;
        let credential = Self::user(request.metadata())?;
        self.bounded(async {
            let authority = if credential.starts_with("lmm_at_") {
                self.grants
                    .as_ref()
                    .ok_or_else(|| Status::unavailable("OAuth remote authority is not configured"))?
                    .authorize_remote(credential)
                    .await
                    .map_err(identity_status)?
            } else {
                self.store()?
                    .authorize_remote_native(credential)
                    .await
                    .map_err(identity_status)?
            };
            let source = match authority.source {
                RemoteSource::NativePersonal { credential_id } => {
                    pb::authorize_remote_response::Source::NativePersonal(
                        pb::NativePersonalRemoteSource {
                            credential_id,
                            native_kind: pb::CredentialKind::Session as i32,
                        },
                    )
                }
                RemoteSource::OAuthGrant {
                    grant_id,
                    access_id,
                } => {
                    pb::authorize_remote_response::Source::OauthGrant(pb::OAuthGrantRemoteSource {
                        grant_id,
                        access_id,
                    })
                }
            };
            Ok(pb::AuthorizeRemoteResponse {
                source: Some(source),
                user_id: authority.user_id,
                owner_account_id: authority.owner_account_id,
                auth_version: authority.auth_version,
                expires_at: authority.expires_at,
                remote_permission: true,
            })
        })
        .await
    }
    async fn list_teams(
        &self,
        request: Request<pb::ListTeamsRequest>,
    ) -> Result<Response<pb::ListTeamsResponse>, Status> {
        self.check(request.metadata())?;
        let credential = Self::user(request.metadata())?;
        let after = request.get_ref().after_id;
        if after < 0 {
            return Err(Status::invalid_argument("after_id must be nonnegative"));
        }
        self.bounded(async {
            let items = self
                .store()?
                .list_teams(credential, after)
                .await
                .map_err(identity_status)?;
            let next_after_id = if items.len() == 100 {
                items.last().map_or(0, |t| t.id)
            } else {
                0
            };
            let teams = items
                .into_iter()
                .map(|t| pb::Team {
                    id: t.id,
                    membership_version: t.membership_version,
                    can_spend: t.can_spend,
                    role: match t.role {
                        accounts::TeamRole::Owner => pb::TeamRole::Owner as i32,
                        accounts::TeamRole::Admin => pb::TeamRole::Admin as i32,
                        accounts::TeamRole::Member => pb::TeamRole::Member as i32,
                    },
                })
                .collect();
            Ok(pb::ListTeamsResponse {
                teams,
                next_after_id,
            })
        })
        .await
    }
}

struct SocketGuard {
    path: PathBuf,
    inode: u64,
    _lock: File,
}
impl Drop for SocketGuard {
    fn drop(&mut self) {
        if fs::symlink_metadata(&self.path)
            .is_ok_and(|m| m.file_type().is_socket() && m.ino() == self.inode)
        {
            let _ = fs::remove_file(&self.path);
        }
    }
}
pub struct RpcServer {
    listener: UnixListener,
    guard: SocketGuard,
    control: Control,
    payments: Option<crate::payments::rpc::PaymentsRpc>,
    commerce: Option<crate::commerce::rpc::CommerceRpc>,
}
impl RpcServer {
    pub fn bind(path: &Path, token: &[u8], store: Option<IdentityStore>) -> io::Result<Self> {
        Self::bind_with_group(path, token, store, None)
    }
    /// A shared group is an explicit transport opt-in. Every RPC still requires
    /// its own service credential; filesystem access never supplies authority.
    pub fn bind_with_group(
        path: &Path,
        token: &[u8],
        store: Option<IdentityStore>,
        group: Option<u32>,
    ) -> io::Result<Self> {
        let control = Control::new(token, store)?;
        if !path.is_absolute() || path.as_os_str().len() > 100 {
            return Err(invalid("RPC socket must be a short absolute path"));
        }
        let parent = path
            .parent()
            .ok_or_else(|| invalid("RPC socket has no parent"))?;
        let metadata = fs::symlink_metadata(parent)?;
        let mode = metadata.permissions().mode() & 0o7777;
        let valid_directory = match group {
            None => mode == 0o700,
            Some(gid) => gid != 0 && mode == 0o750 && metadata.gid() == gid,
        };
        if !metadata.is_dir() || !valid_directory {
            return Err(invalid(
                "RPC socket directory must match its explicit private or shared-group policy",
            ));
        }
        let lock_path = path.with_extension("lock");
        if let Ok(m) = fs::symlink_metadata(&lock_path)
            && !m.file_type().is_file()
        {
            return Err(invalid("RPC lock must be a regular file"));
        }
        let lock = OpenOptions::new()
            .create(true)
            .truncate(false)
            .read(true)
            .write(true)
            .mode(0o600)
            .open(lock_path)?;
        lock.try_lock_exclusive()?;
        // A process-wide lock prevents removing another live core's socket.
        match fs::symlink_metadata(path) {
            Ok(m) if m.file_type().is_socket() => fs::remove_file(path)?,
            Ok(_) => return Err(invalid("refusing to replace a non-socket file")),
            Err(e) if e.kind() == io::ErrorKind::NotFound => {}
            Err(e) => return Err(e),
        }
        let listener = UnixListener::bind(path)?;
        let socket_metadata = fs::symlink_metadata(path)?;
        if socket_metadata.uid() != metadata.uid()
            || group.is_some_and(|gid| socket_metadata.gid() != gid)
        {
            let _ = fs::remove_file(path);
            return Err(invalid(
                "RPC socket owner or group does not match its directory",
            ));
        }
        fs::set_permissions(
            path,
            fs::Permissions::from_mode(if group.is_some() { 0o660 } else { 0o600 }),
        )?;
        let guard = SocketGuard {
            path: path.into(),
            inode: fs::symlink_metadata(path)?.ino(),
            _lock: lock,
        };
        Ok(Self {
            listener,
            guard,
            control,
            payments: None,
            commerce: None,
        })
    }
    /// Explicit integration hook. Provisioned subscriptions bind to this
    /// server-configured principal, never a principal supplied by the caller.
    pub fn with_events(
        mut self,
        store: crate::events::EventStore,
        service_id: &str,
    ) -> io::Result<Self> {
        if !crate::events::valid_name(service_id, 64) {
            return Err(invalid("invalid event service identity"));
        }
        self.control.events = Some(store);
        self.control.service_id = service_id.into();
        Ok(self)
    }
    /// Optional test-provider integration. The host must construct the store,
    /// verification keys and permissions; a service token never grants funds
    /// authority by itself. Default servers do not mount or advertise payments.
    pub fn with_payments(mut self, payments: crate::payments::rpc::PaymentsRpc) -> Self {
        self.payments = Some(payments);
        self
    }
    pub fn with_commerce(mut self, commerce: crate::commerce::rpc::CommerceRpc) -> Self {
        self.control.commerce_features = commerce.features();
        self.commerce = Some(commerce);
        self
    }
    pub async fn serve(
        self,
        mut shutdown: watch::Receiver<bool>,
    ) -> Result<(), tonic::transport::Error> {
        let Self {
            listener,
            guard: _guard,
            control,
            payments,
            commerce,
        } = self;
        let connections = Arc::new(Semaphore::new(MAX_CONNECTIONS));
        let incoming = UnixListenerStream::new(listener).filter_map(move |item| match item {
            Ok(stream) => connections.clone().try_acquire_owned().ok().map(|permit| {
                Ok(Connection {
                    stream,
                    _permit: permit,
                })
            }),
            Err(error) => Some(Err(error)),
        });
        Server::builder()
            .timeout(DEADLINE)
            .max_concurrent_streams(8)
            .concurrency_limit_per_connection(8)
            .http2_max_header_list_size(8192)
            .load_shed(true)
            .initial_stream_window_size(64 * 1024)
            .initial_connection_window_size(256 * 1024)
            .add_service(
                CoreControlServer::new(control.clone())
                    .max_decoding_message_size(MAX_MESSAGE)
                    .max_encoding_message_size(MAX_MESSAGE),
            )
            .add_service(
                pb::core_events_server::CoreEventsServer::new(control)
                    .max_decoding_message_size(MAX_MESSAGE)
                    .max_encoding_message_size(MAX_MESSAGE),
            )
            .add_optional_service(payments.map(|payments| {
                pb::core_payments_server::CorePaymentsServer::new(payments)
                    .max_decoding_message_size(MAX_MESSAGE)
                    .max_encoding_message_size(MAX_MESSAGE)
            }))
            .add_optional_service(commerce.map(|commerce| {
                pb::core_commerce_server::CoreCommerceServer::new(commerce)
                    .max_decoding_message_size(MAX_MESSAGE)
                    .max_encoding_message_size(MAX_MESSAGE)
            }))
            .serve_with_incoming_shutdown(incoming, async move {
                if !*shutdown.borrow() {
                    let _ = shutdown.changed().await;
                }
            })
            .await
    }
}
struct Connection {
    stream: UnixStream,
    _permit: OwnedSemaphorePermit,
}
impl Connected for Connection {
    type ConnectInfo = UdsConnectInfo;
    fn connect_info(&self) -> Self::ConnectInfo {
        self.stream.connect_info()
    }
}
impl AsyncRead for Connection {
    fn poll_read(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        Pin::new(&mut self.stream).poll_read(cx, buf)
    }
}
impl AsyncWrite for Connection {
    fn poll_write(
        mut self: Pin<&mut Self>,
        cx: &mut Context<'_>,
        buf: &[u8],
    ) -> Poll<io::Result<usize>> {
        Pin::new(&mut self.stream).poll_write(cx, buf)
    }
    fn poll_flush(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.stream).poll_flush(cx)
    }
    fn poll_shutdown(mut self: Pin<&mut Self>, cx: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.stream).poll_shutdown(cx)
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use prost::Message;
    const TOKEN: &[u8] = b"0123456789abcdef0123456789abcdef";
    pub(super) fn request<T>(body: T) -> Request<T> {
        let mut r = Request::new(body);
        r.metadata_mut().insert(
            "authorization",
            "Bearer 0123456789abcdef0123456789abcdef".parse().unwrap(),
        );
        r.metadata_mut()
            .insert("x-lmm-protocol", "1".parse().unwrap());
        r
    }
    #[tokio::test]
    async fn service_auth_and_protocol_are_separate_from_user_authority() {
        let control = Control::new(TOKEN, None).unwrap();
        assert_eq!(
            control
                .capabilities(Request::new(pb::CapabilitiesRequest {}))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        let caps = control
            .capabilities(request(pb::CapabilitiesRequest {}))
            .await
            .unwrap()
            .into_inner();
        assert_eq!(caps.protocol_major, 1);
        assert!(!caps.identity_available);
        assert!(caps.features.is_empty());
        assert_eq!(
            control
                .authorize(request(pb::AuthorizeRequest {}))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        let mut r = request(pb::CapabilitiesRequest {});
        r.metadata_mut()
            .insert("x-lmm-protocol", "2".parse().unwrap());
        assert_eq!(
            control.capabilities(r).await.unwrap_err().code(),
            tonic::Code::FailedPrecondition
        );
        let mut r = request(pb::CapabilitiesRequest {});
        r.metadata_mut().append(
            "authorization",
            "Bearer 0123456789abcdef0123456789abcdef".parse().unwrap(),
        );
        assert_eq!(
            control.capabilities(r).await.unwrap_err().code(),
            tonic::Code::Unauthenticated
        );
    }

    #[tokio::test]
    async fn commerce_discovery_does_not_share_identity_transport_authority() {
        let pool = PgPoolOptions::new()
            .connect_lazy("postgresql://synthetic:synthetic@127.0.0.1:1/synthetic")
            .unwrap();
        let commerce = crate::commerce::rpc::CommerceRpc::new(
            crate::commerce::CommerceStore::from_pool(pool),
            b"independent-commerce-credential-0123456789",
            crate::commerce::rpc::Permissions {
                read: true,
                complete_delivery: true,
                ..crate::commerce::rpc::Permissions::default()
            },
        )
        .unwrap();
        let mut control = Control::new(TOKEN, None).unwrap();
        control.commerce_features = commerce.features();
        let response = control
            .capabilities(request(pb::CapabilitiesRequest {}))
            .await
            .unwrap()
            .into_inner();
        assert_eq!(
            response.features,
            vec!["commerce.receipt.read.v1", "commerce.delivery.complete.v1"]
        );
        use pb::core_commerce_server::CoreCommerce;
        let mut query = request(pb::QueryCommerceRequest {
            protocol_major: 1,
            ..Default::default()
        });
        query.metadata_mut().insert(
            "x-lmm-user-credential",
            "synthetic-session-01234567890123456789".parse().unwrap(),
        );
        assert_eq!(
            commerce.query_commerce(query).await.unwrap_err().code(),
            tonic::Code::Unauthenticated
        );
    }
    #[tokio::test]
    async fn calls_have_global_admission_and_server_deadlines() {
        let control = Control::new(TOKEN, None).unwrap();
        let held = control
            .slots
            .clone()
            .acquire_many_owned(MAX_CALLS as u32)
            .await
            .unwrap();
        assert_eq!(
            control
                .capabilities(request(pb::CapabilitiesRequest {}))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::ResourceExhausted
        );
        drop(held);
        let result = control.bounded::<()>(std::future::pending()).await;
        assert_eq!(result.unwrap_err().code(), tonic::Code::DeadlineExceeded);
        assert_eq!(control.slots.available_permits(), MAX_CALLS);
    }
    #[test]
    fn protobuf_preserves_large_team_ids_and_accepts_unknown_fields() {
        let data = [
            0x08, 0x02, 0x10, 0x81, 0x80, 0x80, 0x80, 0x80, 0x80, 0x80, 0x10,
        ];
        let account = pb::Account::decode(data.as_slice()).unwrap();
        assert_eq!(account.id, 9_007_199_254_740_993);
        assert_eq!(account.kind, pb::AccountKind::Team as i32);
        assert_eq!(account.encode_to_vec(), data);
        let mut future = data.to_vec();
        future.extend([0x98, 0x06, 0x01]);
        assert_eq!(pb::Account::decode(future.as_slice()).unwrap(), account);
        assert!(pb::Account::decode(&[0x10, 0x80][..]).is_err());
    }
    #[tokio::test]
    async fn socket_ownership_survives_restart_and_rejects_regular_files() {
        let dir = std::env::temp_dir().join(format!(
            "lmm-rpc-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        fs::create_dir(&dir).unwrap();
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o700)).unwrap();
        let path = dir.join("core.sock");
        let first = RpcServer::bind(&path, TOKEN, None).unwrap();
        assert_eq!(
            fs::metadata(&path).unwrap().permissions().mode() & 0o777,
            0o600
        );
        assert!(RpcServer::bind(&path, TOKEN, None).is_err());
        assert!(path.exists());
        drop(first);
        assert!(!path.exists());
        let stale = UnixListener::bind(&path).unwrap();
        drop(stale);
        drop(RpcServer::bind(&path, TOKEN, None).unwrap());
        fs::write(&path, "keep").unwrap();
        assert!(RpcServer::bind(&path, TOKEN, None).is_err());
        assert_eq!(fs::read_to_string(&path).unwrap(), "keep");
        fs::remove_dir_all(dir).unwrap();
    }

    #[tokio::test]
    async fn shared_socket_requires_explicit_matching_group_and_exact_directory_modes() {
        let dir = std::env::temp_dir().join(format!(
            "lmm-rpc-group-{}-{}",
            std::process::id(),
            rand::random::<u64>()
        ));
        fs::create_dir(&dir).unwrap();
        let gid = fs::metadata(&dir).unwrap().gid();
        let path = dir.join("core.sock");
        fs::set_permissions(&dir, fs::Permissions::from_mode(0o750)).unwrap();
        assert!(RpcServer::bind(&path, TOKEN, None).is_err());
        assert!(RpcServer::bind_with_group(&path, TOKEN, None, Some(gid.wrapping_add(1))).is_err());
        assert!(RpcServer::bind_with_group(&path, TOKEN, None, Some(0)).is_err());
        if gid != 0 {
            let service = RpcServer::bind_with_group(&path, TOKEN, None, Some(gid)).unwrap();
            let metadata = fs::metadata(&path).unwrap();
            assert_eq!(metadata.gid(), gid);
            assert_eq!(metadata.permissions().mode() & 0o777, 0o660);
            assert_eq!(
                fs::metadata(path.with_extension("lock"))
                    .unwrap()
                    .permissions()
                    .mode()
                    & 0o777,
                0o600
            );
            drop(service);
        }
        for mode in [0o700, 0o770, 0o755, 0o2750] {
            fs::set_permissions(&dir, fs::Permissions::from_mode(mode)).unwrap();
            assert!(RpcServer::bind_with_group(&path, TOKEN, None, Some(gid)).is_err());
        }
        fs::remove_dir_all(dir).unwrap();
    }
    #[sqlx::test(migrations = false)]
    async fn oauth_remote_real_grpc_typed_sources_and_denials(pool: sqlx::PgPool) {
        use crate::identity::grants::tests::{fresh, tokens};
        use pb::authorize_remote_response::Source;
        let (identity, grants, native) = fresh(&pool).await;
        let bundle = tokens(&grants, &native.secret).await;
        use sqlx::ConnectOptions;
        // Exercise the same read-only connection setup used by actual RPC startup.
        let readonly = readonly_store(pool.connect_options().to_url_lossy().as_str())
            .await
            .unwrap();
        let mut control = Control::new(TOKEN, Some(readonly)).unwrap();
        assert!(control.grants.is_none());
        control.grants = Some(grants.clone()); // Fixture-only wiring; startup stays None.
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let addr = listener.local_addr().unwrap();
        let (stop, stopped) = tokio::sync::oneshot::channel::<()>();
        let server = tokio::spawn(async move {
            Server::builder()
                .add_service(CoreControlServer::new(control))
                .serve_with_incoming_shutdown(
                    tokio_stream::wrappers::TcpListenerStream::new(listener),
                    async {
                        let _ = stopped.await;
                    },
                )
                .await
                .unwrap();
        });
        let channel = tonic::transport::Endpoint::from_shared(format!("http://{addr}"))
            .unwrap()
            .connect()
            .await
            .unwrap();
        let mut client = tonic::client::Grpc::new(channel);
        async fn remote(
            client: &mut tonic::client::Grpc<tonic::transport::Channel>,
            request: Request<pb::AuthorizeRemoteRequest>,
        ) -> Result<Response<pb::AuthorizeRemoteResponse>, Status> {
            client
                .ready()
                .await
                .map_err(|_| Status::unavailable("transport"))?;
            client
                .unary(
                    request,
                    "/lmm.core.v1.CoreControl/AuthorizeRemote".parse().unwrap(),
                    tonic_prost::ProstCodec::default(),
                )
                .await
        }
        let remote_request = |secret: &str| {
            let mut r = request(pb::AuthorizeRemoteRequest {});
            r.metadata_mut()
                .insert("x-lmm-user-credential", secret.parse().unwrap());
            r
        };
        let a = remote(&mut client, remote_request(&native.secret))
            .await
            .unwrap()
            .into_inner();
        assert!(
            matches!(a.source, Some(Source::NativePersonal(ref s)) if s.native_kind == pb::CredentialKind::Session as i32)
        );
        let b = remote(&mut client, remote_request(&bundle.access_token))
            .await
            .unwrap()
            .into_inner();
        assert!(matches!(b.source, Some(Source::OauthGrant(_))));
        assert_eq!(
            (a.user_id, a.owner_account_id, a.auth_version),
            (b.user_id, b.owner_account_id, b.auth_version)
        );
        assert!(b.remote_permission && b.expires_at > 0);
        let mut general = request(pb::AuthorizeRequest {});
        general.metadata_mut().insert(
            "x-lmm-user-credential",
            bundle.access_token.parse().unwrap(),
        );
        client.ready().await.unwrap();
        assert_eq!(
            client
                .unary::<pb::AuthorizeRequest, pb::AuthorizeResponse, _>(
                    general,
                    "/lmm.core.v1.CoreControl/Authorize".parse().unwrap(),
                    tonic_prost::ProstCodec::default()
                )
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        for key in ["authorization", "x-lmm-protocol", "x-lmm-user-credential"] {
            let mut missing = remote_request(&native.secret);
            missing.metadata_mut().remove(key);
            assert_eq!(
                remote(&mut client, missing).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
            let mut duplicate = remote_request(&native.secret);
            let value = duplicate.metadata().get(key).unwrap().clone();
            duplicate.metadata_mut().append(key, value);
            assert_eq!(
                remote(&mut client, duplicate).await.unwrap_err().code(),
                tonic::Code::Unauthenticated
            );
        }
        let key = identity
            .issue_key(
                &native.secret,
                accounts::Account {
                    kind: accounts::AccountKind::Personal,
                    id: 1,
                },
                crate::funding::FundingPolicy::new_personal_key(),
                3600,
            )
            .await
            .unwrap();
        assert_eq!(
            remote(&mut client, remote_request(&key.secret))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        for bad in [
            "lmm_host_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        ] {
            assert_eq!(
                remote(&mut client, remote_request(bad))
                    .await
                    .unwrap_err()
                    .code(),
                tonic::Code::Unauthenticated
            );
        }
        grants
            .revoke_access(&bundle.access_token, "lmm-pi")
            .await
            .unwrap();
        assert_eq!(
            remote(&mut client, remote_request(&bundle.access_token))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        sqlx::query("UPDATE core_identity.users SET auth_version=auth_version+1 WHERE id=1")
            .execute(&pool)
            .await
            .unwrap();
        assert_eq!(
            remote(&mut client, remote_request(&native.secret))
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        stop.send(()).unwrap();
        server.await.unwrap();
    }
}
