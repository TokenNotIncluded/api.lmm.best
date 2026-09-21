//! Normal-listener mounts for the frozen legacy unavailable relay routes.
//!
//! These routes retain the Go response only after the shared relay auth and
//! distribution boundary has run.  They do not select an upstream provider.

use axum::{
    Router,
    routing::{get, post},
};

use super::relay_misc::{RelayMiscHttpState, legacy_not_implemented};

pub fn router(state: RelayMiscHttpState) -> Router {
    Router::new()
        .route("/v1/images/variations", post(legacy_not_implemented))
        .route(
            "/v1/files",
            get(legacy_not_implemented).post(legacy_not_implemented),
        )
        .route(
            "/v1/files/{id}",
            get(legacy_not_implemented).delete(legacy_not_implemented),
        )
        .route("/v1/files/{id}/content", get(legacy_not_implemented))
        .route(
            "/v1/fine-tunes",
            get(legacy_not_implemented).post(legacy_not_implemented),
        )
        .route("/v1/fine-tunes/{id}", get(legacy_not_implemented))
        .route("/v1/fine-tunes/{id}/cancel", post(legacy_not_implemented))
        .route("/v1/fine-tunes/{id}/events", get(legacy_not_implemented))
        .with_state(state)
}
