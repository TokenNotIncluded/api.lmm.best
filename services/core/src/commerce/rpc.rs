//! Separately granted store principal. The identity transport token is not a
//! commerce grant. User credentials are mandatory even for receipt replay/read.
use super::{CommerceStore, Error, Receipt};
use crate::internal_rpc::pb;
use sha2::{Digest, Sha256};
use std::{sync::Arc, time::Duration};
use subtle::ConstantTimeEq;
use tokio::sync::Semaphore;
use tonic::{Request, Response, Status, metadata::MetadataMap};

#[derive(Clone, Copy, Default)]
pub struct Permissions {
    pub pay: bool,
    pub settle: bool,
    pub refund: bool,
    pub read: bool,
    pub complete_delivery: bool,
    pub reconcile: bool,
}
#[derive(Clone)]
pub struct CommerceRpc {
    store: CommerceStore,
    token_digest: [u8; 32],
    permissions: Permissions,
    slots: Arc<Semaphore>,
}
fn single<'a>(m: &'a MetadataMap, key: &'static str) -> Result<&'a str, Status> {
    let mut values = m.get_all(key).iter();
    let value = values
        .next()
        .ok_or_else(|| Status::unauthenticated("missing commerce credential or protocol"))?;
    if values.next().is_some() {
        return Err(Status::unauthenticated(
            "duplicate commerce credential or protocol",
        ));
    }
    value
        .to_str()
        .map_err(|_| Status::unauthenticated("invalid commerce credential or protocol"))
}
fn status(e: Error) -> Status {
    match e {
        Error::Invalid => Status::invalid_argument("invalid frozen commerce command"),
        Error::Unauthorized => Status::unauthenticated("invalid current user credential"),
        Error::Forbidden => Status::permission_denied("commerce user or funding permission denied"),
        Error::Conflict => Status::already_exists("commerce key or frozen order changed"),
        Error::Missing => Status::not_found("commerce payment not found"),
        Error::Pending => Status::failed_precondition(
            "commerce funds unavailable; original operation remains pending",
        ),
        Error::Retry | Error::Storage => {
            Status::unavailable("commerce outcome may be unknown; reconcile the same key")
        }
    }
}
fn receipt(v: Receipt) -> pb::CommerceReceipt {
    pb::CommerceReceipt {
        key: v.key,
        command_hash: v.command_hash,
        reference: v.reference,
        outcome: v.outcome,
        rejection_code: v.rejection_code,
    }
}
impl CommerceRpc {
    pub fn new(
        store: CommerceStore,
        token: &[u8],
        permissions: Permissions,
    ) -> super::Result<Self> {
        if !(32..=256).contains(&token.len()) || !token.iter().all(u8::is_ascii_graphic) {
            return Err(Error::Invalid);
        }
        Ok(Self {
            store,
            token_digest: Sha256::digest(token).into(),
            permissions,
            slots: Arc::new(Semaphore::new(4)),
        })
    }
    pub fn features(&self) -> Vec<String> {
        [
            (self.permissions.pay, "commerce.pay.v1"),
            (self.permissions.settle, "commerce.settle.v1"),
            (self.permissions.refund, "commerce.refund.v1"),
            (self.permissions.read, "commerce.receipt.read.v1"),
            (
                self.permissions.complete_delivery,
                "commerce.delivery.complete.v1",
            ),
            (self.permissions.reconcile, "commerce.receipt.reconcile.v1"),
        ]
        .into_iter()
        .filter(|(enabled, _)| *enabled)
        .map(|(_, name)| name.to_owned())
        .collect()
    }
    fn check(&self, m: &MetadataMap, allowed: bool) -> Result<(), Status> {
        let value = single(m, "authorization")?
            .strip_prefix("Bearer ")
            .ok_or_else(|| Status::unauthenticated("invalid commerce service credential"))?;
        let digest: [u8; 32] = Sha256::digest(value.as_bytes()).into();
        if !(32..=256).contains(&value.len()) || !bool::from(digest.ct_eq(&self.token_digest)) {
            return Err(Status::unauthenticated(
                "invalid commerce service credential",
            ));
        }
        if single(m, "x-lmm-protocol")? != "1" {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        if !allowed {
            return Err(Status::permission_denied(
                "commerce service permission denied",
            ));
        }
        Ok(())
    }
}
#[tonic::async_trait]
impl pb::core_commerce_server::CoreCommerce for CommerceRpc {
    async fn execute_commerce(
        &self,
        request: Request<pb::ExecuteCommerceRequest>,
    ) -> Result<Response<pb::CommerceReceipt>, Status> {
        if request.get_ref().protocol_major != 1 {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        if request.get_ref().command_json.len() > 8192 {
            return Err(Status::invalid_argument("commerce command too large"));
        }
        self.check(request.metadata(), true)?;
        let user = single(request.metadata(), "x-lmm-user-credential")?.to_owned();
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("commerce busy"))?;
        let [command]: [super::MoneyCommand; 1] =
            serde_json::from_slice(&request.get_ref().command_json)
                .map_err(|_| Status::invalid_argument("invalid commerce command"))?;
        let permission = match command.kind.as_str() {
            "pay" => self.permissions.pay,
            "settle" => self.permissions.settle,
            "refund" => self.permissions.refund,
            _ => false,
        };
        if !permission {
            return Err(Status::permission_denied(
                "commerce service permission denied",
            ));
        }
        tokio::time::timeout(
            Duration::from_secs(2),
            self.store.execute(&user, &request.get_ref().command_json),
        )
        .await
        .map_err(|_| {
            Status::deadline_exceeded("commerce outcome may be unknown; reconcile same key")
        })?
        .map(|v| Response::new(receipt(v)))
        .map_err(status)
    }
    async fn query_commerce(
        &self,
        request: Request<pb::QueryCommerceRequest>,
    ) -> Result<Response<pb::QueryCommerceResponse>, Status> {
        if request.get_ref().protocol_major != 1 {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        self.check(request.metadata(), self.permissions.read)?;
        let user = single(request.metadata(), "x-lmm-user-credential")?.to_owned();
        let q = request.into_inner();
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("commerce busy"))?;
        tokio::time::timeout(
            Duration::from_secs(2),
            self.store
                .query(&user, &q.shop_id, &q.order_id, &q.operation_key),
        )
        .await
        .map_err(|_| Status::deadline_exceeded("commerce receipt remains unknown"))?
        .map(|v| {
            Response::new(pb::QueryCommerceResponse {
                receipt: v.map(receipt),
            })
        })
        .map_err(status)
    }
    async fn complete_delivery(
        &self,
        request: Request<pb::CompleteDeliveryRequest>,
    ) -> Result<Response<pb::CommerceReceipt>, Status> {
        if request.get_ref().protocol_major != 1 {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        if request.get_ref().command_json.len() > 8192 {
            return Err(Status::invalid_argument("commerce command too large"));
        }
        self.check(request.metadata(), self.permissions.complete_delivery)?;
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("commerce busy"))?;
        let q = request.into_inner();
        tokio::time::timeout(
            Duration::from_secs(2),
            self.store.complete_delivery(
                &q.command_json,
                &q.payment_command_hash,
                &q.delivery_reference,
            ),
        )
        .await
        .map_err(|_| {
            Status::deadline_exceeded("capture outcome may be unknown; replay same delivery")
        })?
        .map(|v| Response::new(receipt(v)))
        .map_err(status)
    }
    async fn reconcile_commerce(
        &self,
        request: Request<pb::ExecuteCommerceRequest>,
    ) -> Result<Response<pb::QueryCommerceResponse>, Status> {
        if request.get_ref().protocol_major != 1 {
            return Err(Status::failed_precondition("unsupported protocol major"));
        }
        if request.get_ref().command_json.len() > 8192 {
            return Err(Status::invalid_argument("commerce command too large"));
        }
        self.check(request.metadata(), self.permissions.reconcile)?;
        let _slot = self
            .slots
            .clone()
            .try_acquire_owned()
            .map_err(|_| Status::resource_exhausted("commerce busy"))?;
        tokio::time::timeout(
            Duration::from_secs(2),
            self.store.reconcile(&request.get_ref().command_json),
        )
        .await
        .map_err(|_| Status::deadline_exceeded("commerce receipt remains unknown"))?
        .map(|v| {
            Response::new(pb::QueryCommerceResponse {
                receipt: v.map(receipt),
            })
        })
        .map_err(status)
    }
}
