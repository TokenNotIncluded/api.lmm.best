//! Bounded, read-only protobuf control plane over a private Unix socket.
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
    io::{self, Read},
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
    let mut bytes = Vec::new();
    File::open(path)?.take(4097).read_to_end(&mut bytes)?;
    if bytes.len() > 4096 {
        return Err(invalid("RPC credential file is too large"));
    }
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
                features: if self.store.is_some() {
                    vec!["identity.read".into(), "teams.read".into()]
                } else {
                    vec![]
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
}
impl RpcServer {
    pub fn bind(path: &Path, token: &[u8], store: Option<IdentityStore>) -> io::Result<Self> {
        let control = Control::new(token, store)?;
        if !path.is_absolute() || path.as_os_str().len() > 100 {
            return Err(invalid("RPC socket must be a short absolute path"));
        }
        let parent = path
            .parent()
            .ok_or_else(|| invalid("RPC socket has no parent"))?;
        let metadata = fs::symlink_metadata(parent)?;
        if !metadata.is_dir() || metadata.permissions().mode() & 0o077 != 0 {
            return Err(invalid(
                "RPC socket directory must be a real private directory (0700)",
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
        fs::set_permissions(path, fs::Permissions::from_mode(0o600))?;
        let guard = SocketGuard {
            path: path.into(),
            inode: fs::symlink_metadata(path)?.ino(),
            _lock: lock,
        };
        Ok(Self {
            listener,
            guard,
            control,
        })
    }
    pub async fn serve(
        self,
        mut shutdown: watch::Receiver<bool>,
    ) -> Result<(), tonic::transport::Error> {
        let Self {
            listener,
            guard: _guard,
            control,
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
            .max_header_list_size(8192)
            .initial_stream_window_size(64 * 1024)
            .initial_connection_window_size(256 * 1024)
            .add_service(
                CoreControlServer::new(control)
                    .max_decoding_message_size(MAX_MESSAGE)
                    .max_encoding_message_size(MAX_MESSAGE),
            )
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
    fn request<T>(body: T) -> Request<T> {
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
}
