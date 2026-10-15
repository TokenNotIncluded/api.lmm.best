//! Stable model-request core. No extension runtime or Go dependency belongs here.
//!
//! PostgreSQL identity is usable in isolation. Public paid model endpoints stay
//! disabled until durable billing and routing adapters are integrated.
pub mod abuse;
pub mod accounts;
pub mod billing;
#[cfg(unix)]
pub mod commerce;
pub mod config;
pub mod console_compat;
pub mod core_schema;
pub mod events;
pub mod financial_profile;
pub mod funding;
pub mod http;
pub mod identity;
pub mod identity_http;
#[cfg(unix)]
pub mod internal_rpc;
pub mod legacy_auth;
pub mod lifecycle;
pub mod model_http;
pub mod oauth2_balance;
pub mod oauth2_http;
#[cfg(unix)]
pub mod payments;
pub mod relay;
pub mod remote_control;
pub mod routing;
pub mod runtime_database;
pub mod upstream_cost;
pub mod usage;
