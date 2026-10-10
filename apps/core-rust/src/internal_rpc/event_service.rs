use super::*;
use crate::events::{self, EventError, EventStore};
use pb::core_events_server::CoreEvents;

fn event_status(error: EventError) -> Status {
    match error {
        EventError::Invalid => Status::invalid_argument("invalid event request"),
        EventError::Forbidden => Status::permission_denied("subscription is not permitted"),
        EventError::Missing => Status::not_found("event or subscription not found"),
        EventError::Conflict => Status::already_exists("idempotency content differs"),
        EventError::LeaseLost => Status::aborted("event lease was replaced or released"),
        EventError::Full => Status::resource_exhausted("event backlog limit reached"),
        EventError::Busy => Status::aborted("event transaction is busy; retry safely"),
        EventError::Storage => Status::unavailable("event storage unavailable"),
    }
}
fn version(value: Option<&pb::EventClientVersion>) -> Result<(), Status> {
    let value = value.ok_or_else(|| Status::failed_precondition("event version is required"))?;
    if value.protocol_major != 1 || value.protocol_minor < 1 {
        return Err(Status::failed_precondition(
            "unsupported event protocol version",
        ));
    }
    if value.required_features.len() > 8 || value.required_features.iter().any(|f| f.len() > 64) {
        return Err(Status::invalid_argument("too many required event features"));
    }
    if value
        .required_features
        .iter()
        .any(|f| !events::FEATURES.contains(&f.as_str()))
    {
        return Err(Status::failed_precondition(
            "required event feature is unavailable",
        ));
    }
    Ok(())
}
impl Control {
    fn events(&self) -> Result<&EventStore, Status> {
        self.events
            .as_ref()
            .ok_or_else(|| Status::unavailable("event store is not configured"))
    }
}
#[tonic::async_trait]
impl CoreEvents for Control {
    async fn negotiate(
        &self,
        request: Request<pb::NegotiateEventsRequest>,
    ) -> Result<Response<pb::NegotiateEventsResponse>, Status> {
        self.check(request.metadata())?;
        version(request.get_ref().version.as_ref())?;
        self.bounded(async {
            let store = self.events()?;
            let types = store
                .event_types(&self.service_id, &request.get_ref().consumer_id)
                .await
                .map_err(event_status)?;
            Ok(pb::NegotiateEventsResponse {
                protocol_major: 1,
                protocol_minor: 1,
                features: events::FEATURES.iter().map(|s| (*s).into()).collect(),
                max_batch: events::MAX_BATCH,
                max_payload_bytes: events::MAX_PAYLOAD as u32,
                lease_seconds: store.lease_seconds(),
                event_types: types,
            })
        })
        .await
    }
    async fn pull(
        &self,
        request: Request<pb::PullEventsRequest>,
    ) -> Result<Response<pb::PullEventsResponse>, Status> {
        self.check(request.metadata())?;
        let body = request.get_ref();
        version(body.version.as_ref())?;
        self.bounded(async {
            let deliveries = self
                .events()?
                .pull(&self.service_id, &body.consumer_id, body.max_events)
                .await
                .map_err(event_status)?
                .into_iter()
                .map(|d| pb::EventDelivery {
                    event: Some(pb::EventEnvelope {
                        event_id: d.event.id,
                        event_key: d.event.key,
                        event_type: d.event.event_type,
                        schema_version: d.event.schema_version,
                        resource_id: d.event.resource_id,
                        resource_version: d.event.resource_version,
                        payload: d.event.payload,
                        content_sha256: d.event.fingerprint,
                    }),
                    lease_token: d.lease_token,
                    attempt: d.attempt,
                    lease_until_unix_ms: d.lease_until_unix_ms,
                })
                .collect();
            Ok(pb::PullEventsResponse { deliveries })
        })
        .await
    }
    async fn acknowledge(
        &self,
        request: Request<pb::AcknowledgeEventRequest>,
    ) -> Result<Response<pb::AcknowledgeEventResponse>, Status> {
        self.check(request.metadata())?;
        let body = request.get_ref();
        self.bounded(async {
            let already_acknowledged = self
                .events()?
                .acknowledge(
                    &self.service_id,
                    &body.consumer_id,
                    body.event_id,
                    &body.lease_token,
                )
                .await
                .map_err(event_status)?;
            Ok(pb::AcknowledgeEventResponse {
                already_acknowledged,
            })
        })
        .await
    }
    async fn retry(
        &self,
        request: Request<pb::RetryEventRequest>,
    ) -> Result<Response<pb::RetryEventResponse>, Status> {
        self.check(request.metadata())?;
        let body = request.get_ref();
        self.bounded(async {
            let already_acknowledged = self
                .events()?
                .retry(
                    &self.service_id,
                    &body.consumer_id,
                    body.event_id,
                    &body.lease_token,
                    body.reason,
                )
                .await
                .map_err(event_status)?;
            Ok(pb::RetryEventResponse {
                already_acknowledged,
            })
        })
        .await
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn v() -> pb::EventClientVersion {
        pb::EventClientVersion {
            protocol_major: 1,
            protocol_minor: 1,
            required_features: vec![],
        }
    }
    #[test]
    fn incompatible_major_and_required_features_fail_closed() {
        assert!(version(Some(&v())).is_ok());
        assert!(version(None).is_err());
        let mut x = v();
        x.protocol_major = 2;
        assert!(version(Some(&x)).is_err());
        x = v();
        x.protocol_minor = 0;
        assert!(version(Some(&x)).is_err());
        x = v();
        x.protocol_minor = 99;
        assert!(version(Some(&x)).is_ok());
        x.required_features.push("events.magic.v99".into());
        assert!(version(Some(&x)).is_err());
    }
    #[tokio::test]
    async fn event_rpcs_authenticate_before_storage_or_version_checks() {
        let control = Control::new(b"0123456789abcdef0123456789abcdef", None).unwrap();
        let mut r = Request::new(pb::PullEventsRequest {
            consumer_id: "x".into(),
            version: Some(v()),
            max_events: 1,
        });
        r.metadata_mut().insert(
            "x-lmm-user-credential",
            "0123456789abcdef0123456789abcdef".parse().unwrap(),
        );
        assert_eq!(
            control.pull(r).await.unwrap_err().code(),
            tonic::Code::Unauthenticated
        );
        let r = super::super::tests::request(pb::AcknowledgeEventRequest {
            consumer_id: "x".into(),
            event_id: 1,
            lease_token: vec![0; 32],
        });
        assert_eq!(
            control.acknowledge(r).await.unwrap_err().code(),
            tonic::Code::Unavailable
        );
    }
}
