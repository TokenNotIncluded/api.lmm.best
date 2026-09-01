//! HTTP route modules grouped by product domain.
//!
//! Each module owns the handlers and typed dependencies for one API surface.
//! Listener assembly lives in `main.rs` and `test_instance.rs`.

#![allow(dead_code, private_interfaces)]
#![allow(
    clippy::result_large_err,
    clippy::result_unit_err,
    clippy::too_many_arguments
)]

pub mod access_ip;
pub mod account_action;
pub mod admin_catalog;
pub mod api_token;
pub mod assistant;
pub mod assistant_extended;
pub mod billing_dashboard;
pub mod billing_payments;
pub mod billing_subscriptions;
pub mod channel_advanced;
pub mod channel_core;
pub mod channel_ops;
pub mod checkin_affiliate;
pub mod control_admin;
pub mod control_public;
pub mod control_tasks;
pub mod deployment;
pub mod developer_access;
pub mod discount_code;
pub mod dynamic_pricing;
pub mod epay;
pub mod finance;
pub mod finance_export;
pub mod gifts;
pub mod hero_sms;
pub mod identity_2fa;
pub mod identity_admin;
pub mod identity_catalog;
pub mod identity_federation;
pub mod identity_profile;
pub mod identity_security;
pub mod kling_task_reads;
pub(crate) mod legacy_http;
pub mod mcp;
pub mod media_midjourney;
pub mod media_tasks;
pub mod model_lookup;
pub mod observability;
pub mod open_source_bounties;
pub mod public_catalog;
pub mod public_relay;
pub mod ratio_sync;
pub mod relay_anthropic_gemini;
pub mod relay_anthropic_gemini_postgres;
pub mod relay_compat;
pub mod relay_media;
pub mod relay_misc;
pub mod relay_misc_active;
pub mod relay_misc_frozen;
pub mod relay_misc_postgres;
pub mod relay_openai;
pub mod relay_video;
pub mod release_notes;
pub mod responses_websocket;
pub mod security_admin;
pub mod security_overview;
pub mod sse;
pub mod stripe_creem;
pub mod system_config;
#[cfg(test)]
pub(crate) mod test_support;
pub mod topup;
pub mod unified_todo;
pub mod user_assistant_admin;
pub mod user_rankings;
pub mod verify_email;
pub mod waffo;
pub mod waffo_webhooks;
