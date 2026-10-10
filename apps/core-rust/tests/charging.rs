// Compile the isolated task 03 module without editing task 02's billing.rs.
// Final integration adds `pub mod charging;` to src/billing.rs and can replace
// this path harness with normal lmm_core::billing::charging integration imports.
#[path = "../src/billing/charging/mod.rs"]
pub mod charging;
