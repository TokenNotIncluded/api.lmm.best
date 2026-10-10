//! Transactional charging for a fresh PostgreSQL core. No HTTP/Go write API.
//! The host must supply task 02's transactional ledger adapter before forwarding.
mod authority;
mod engine;
mod limits;
mod types;

pub use engine::Charging;
pub use types::*;

pub const SCHEMA: &str = include_str!("../../../schema/charging.sql");

#[cfg(test)]
mod tests;
