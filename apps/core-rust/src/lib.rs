//! Stable model-request core. No extension runtime or Go dependency belongs here.
//!
//! PostgreSQL identity is usable in isolation. Public paid model endpoints stay
//! disabled until durable billing and routing adapters are integrated.
pub mod accounts;
pub mod billing;
pub mod config;
pub mod funding;
pub mod http;
pub mod identity;
pub mod identity_http;
#[cfg(unix)]
pub mod internal_rpc;
pub mod relay;
