//! Stable model-request core. No extension runtime or Go dependency belongs here.
//!
//! The initial domain rules are not a database, authorization service or ledger.
//! The HTTP composition deliberately rejects business traffic until these exist.
pub mod accounts;
pub mod billing;
pub mod funding;
pub mod http;
