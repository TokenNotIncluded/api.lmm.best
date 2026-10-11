//! The service token authenticates the configured extension principal; explicit
//! server permissions and current user/evidence checks grant each operation.
use super::{Error, PaymentsStore};
use crate::internal_rpc::pb;
use sha2::{Digest, Sha256};
use std::{future::Future, sync::Arc, time::Duration};
use subtle::ConstantTimeEq;
use tokio::sync::Semaphore;
use tonic::{Request, Response, Status, metadata::MetadataMap};

#[derive(Clone, Copy, Default)]
pub struct Permissions {
    pub prepare: bool,
    pub credit_evidence: bool,
    pub refund_user: bool,
    pub refund_evidence: bool,
    pub read_user: bool,
    pub reconcile: bool,
}
#[derive(Clone)]
pub struct PaymentsRpc {
    store: PaymentsStore,
    token_digest: [u8; 32],
    permissions: Permissions,
    slots: Arc<Semaphore>,
}
fn single<'a>(metadata: &'a MetadataMap, name: &'static str) -> Result<&'a str, Status> {
    let mut values = metadata.get_all(name).iter();
    let value = values
        .next()
        .ok_or_else(|| Status::unauthenticated("missing credential or protocol"))?;
    if values.next().is_some() {
        return Err(Status::unauthenticated("duplicate credential or protocol"));
    }
    value
        .to_str()
        .map_err(|_| Status::unauthenticated("invalid credential or protocol"))
}
fn status(error: Error) -> Status {
    match error {
        Error::Invalid => Status::invalid_argument("invalid payment request"),
        Error::Denied => Status::permission_denied("payment authority or evidence denied"),
        Error::Conflict => Status::aborted("payment state or request changed; reconcile same key"),
        Error::NotFound => Status::not_found("payment record not found"),
        Error::Unavailable | Error::Storage => {
            Status::unavailable("payment dependency unavailable; reconcile same key")
        }
    }
}
impl PaymentsRpc {
    pub fn new(
        store: PaymentsStore,
        token: &[u8],
        permissions: Permissions,
    ) -> Result<Self, Error> {
        if !(32..=256).contains(&token.len()) || !token.iter().all(u8::is_ascii_graphic) {
            return Err(Error::Invalid);
        }
        Ok(Self {
            store,
            token_digest: Sha256::digest(token).into(),
            permissions,
            slots: Arc::new(Semaphore::new(8)),
        })
    }
    fn check(&self, metadata: &MetadataMap, allowed: bool) -> Result<(), Status> {
        let token = single(metadata, "authorization")?
            .strip_prefix("Bearer ")
            .ok_or_else(|| Status::unauthenticated("invalid service credential"))?;
        if !(32..=256).contains(&token.len()) || !token.bytes().all(|b| b.is_ascii_graphic()) {
            return Err(Status::unauthenticated("invalid service credential"));
        }
        let digest: [u8; 32] = Sha256::digest(token.as_bytes()).into();
        if !bool::from(digest.ct_eq(&self.token_digest)) {
            return Err(Status::unauthenticated("invalid service credential"));
        }
        if single(metadata, "x-lmm-protocol")? != "1" {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        if !allowed {
            return Err(Status::permission_denied(
                "service payment permission denied",
            ));
        }
        Ok(())
    }
    async fn bounded<T>(
        &self,
        call: impl Future<Output = super::Result<T>>,
    ) -> Result<Response<T>, Status> {
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("payment service busy"))?;
        tokio::time::timeout(Duration::from_secs(2), call)
            .await
            .map_err(|_| {
                Status::deadline_exceeded("payment deadline exceeded; reconcile same key")
            })?
            .map(Response::new)
            .map_err(status)
    }
}
#[tonic::async_trait]
impl pb::core_payments_server::CorePayments for PaymentsRpc {
    async fn prepare_topup(
        &self,
        request: Request<pb::PrepareTopupRequest>,
    ) -> Result<Response<pb::PaymentIntent>, Status> {
        self.check(request.metadata(), self.permissions.prepare)?;
        let user = single(request.metadata(), "x-lmm-user-credential")?.to_owned();
        self.bounded(self.store.prepare(&user, request.into_inner()))
            .await
    }
    async fn credit_topup(
        &self,
        request: Request<pb::CreditTopupRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        self.check(request.metadata(), self.permissions.credit_evidence)?;
        self.bounded(self.store.credit(request.into_inner())).await
    }
    async fn hold_refund(
        &self,
        request: Request<pb::HoldRefundRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        self.check(request.metadata(), self.permissions.refund_user)?;
        let user = single(request.metadata(), "x-lmm-user-credential")?.to_owned();
        self.bounded(self.store.hold(&user, request.into_inner()))
            .await
    }
    async fn commit_refund(
        &self,
        request: Request<pb::ResolveRefundRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        self.check(request.metadata(), self.permissions.refund_evidence)?;
        self.bounded(self.store.resolve(request.into_inner(), true))
            .await
    }
    async fn release_refund(
        &self,
        request: Request<pb::ResolveRefundRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        self.check(request.metadata(), self.permissions.refund_evidence)?;
        self.bounded(self.store.resolve(request.into_inner(), false))
            .await
    }
    async fn apply_external_refund(
        &self,
        request: Request<pb::ExternalRefundRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        self.check(request.metadata(), self.permissions.refund_evidence)?;
        self.bounded(self.store.external_refund(request.into_inner()))
            .await
    }
    async fn get_payment_receipt(
        &self,
        request: Request<pb::GetPaymentReceiptRequest>,
    ) -> Result<Response<pb::PaymentReceipt>, Status> {
        let user = if request.metadata().contains_key("x-lmm-user-credential") {
            self.check(request.metadata(), self.permissions.read_user)?;
            Some(single(request.metadata(), "x-lmm-user-credential")?.to_owned())
        } else {
            self.check(request.metadata(), self.permissions.reconcile)?;
            None
        };
        self.bounded(self.store.receipt(user.as_deref(), request.into_inner()))
            .await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    use pb::core_payments_server::CorePayments;
    fn service(permission: bool) -> PaymentsRpc {
        let pool = sqlx::postgres::PgPoolOptions::new()
            .connect_lazy("postgresql://synthetic:synthetic@127.0.0.1:1/synthetic")
            .unwrap();
        PaymentsRpc::new(
            PaymentsStore::new(pool, vec![]).unwrap(),
            b"0123456789abcdef0123456789abcdef",
            Permissions {
                prepare: permission,
                ..Permissions::default()
            },
        )
        .unwrap()
    }
    fn request() -> Request<pb::PrepareTopupRequest> {
        let mut request = Request::new(pb::PrepareTopupRequest::default());
        request.metadata_mut().insert(
            "authorization",
            "Bearer 0123456789abcdef0123456789abcdef".parse().unwrap(),
        );
        request
            .metadata_mut()
            .insert("x-lmm-protocol", "1".parse().unwrap());
        request
    }
    #[tokio::test]
    async fn service_permission_does_not_replace_user_authority() {
        assert_eq!(
            service(false)
                .prepare_topup(request())
                .await
                .unwrap_err()
                .code(),
            tonic::Code::PermissionDenied
        );
        assert_eq!(
            service(true)
                .prepare_topup(request())
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
        let mut duplicate = request();
        duplicate.metadata_mut().append(
            "authorization",
            "Bearer 0123456789abcdef0123456789abcdef".parse().unwrap(),
        );
        assert_eq!(
            service(true)
                .prepare_topup(duplicate)
                .await
                .unwrap_err()
                .code(),
            tonic::Code::Unauthenticated
        );
    }
}
