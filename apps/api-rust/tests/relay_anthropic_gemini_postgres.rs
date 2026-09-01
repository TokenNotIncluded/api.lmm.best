//! Construction coverage for the PostgreSQL-backed Anthropic/Gemini adapter.
//!
//! The HTTP compatibility surface is covered by the neighbouring relay tests;
//! this test keeps the production adapter's listener dependency boundary in the
//! migration inventory as well.

use lmm_api_rs::routes::relay_anthropic_gemini_postgres::PgAnthropicGeminiRelayBackend;
use sqlx::postgres::PgPoolOptions;
use std::time::Duration;

#[tokio::test]
async fn postgres_relay_backend_accepts_lazy_listener_dependencies() {
    let pg = PgPoolOptions::new()
        .connect_lazy("postgres://unused:unused@127.0.0.1/unused")
        .expect("valid lazy PostgreSQL URL");
    let client = reqwest::Client::builder()
        .build()
        .expect("bounded relay client");

    let _backend = PgAnthropicGeminiRelayBackend::new(pg, client, Duration::from_secs(1));
}
