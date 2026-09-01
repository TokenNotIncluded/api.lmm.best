//! Normal-listener mounts for the PostgreSQL-backed miscellaneous relay slice.
//!
//! Keeping the four live protocols in their own route factory makes the
//! production ledger explicit and prevents the frozen compatibility endpoints
//! from being mistaken for provider-capable routes.

use axum::{Router, routing::post};

use super::relay_misc::{RelayMiscHttpState, alpha_search, embeddings, moderations, rerank};

pub fn router(state: RelayMiscHttpState) -> Router {
    Router::new()
        .route("/v1/alpha/search", post(alpha_search))
        .route("/v1/embeddings", post(embeddings))
        .route("/v1/rerank", post(rerank))
        .route("/v1/moderations", post(moderations))
        .with_state(state)
}
