//! Stable model-request core. No extension runtime or Go dependency belongs here.
//!
//! PostgreSQL identity is usable in isolation. The ledger and model forwarding
//! are not ready; identity authorization alone must never permit a paid request.
pub mod accounts;
pub mod billing;
pub mod config;
pub mod events;
pub mod funding;
pub mod http;
pub mod identity;
pub mod identity_http;
#[cfg(unix)]
pub mod internal_rpc;
