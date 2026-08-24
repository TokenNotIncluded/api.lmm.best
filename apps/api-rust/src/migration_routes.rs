//! Flat module root for route slices undergoing legacy-oracle validation.
//!
//! Exporting these modules lets integration tests exercise the same compiled
//! code that will eventually be composed into the listener.  The typed
//! [`MigrationCandidateStates`] adapter can compose every complete, non-stub
//! candidate into a test-only root router without granting production
//! ownership.

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
pub mod billing_payments;
pub mod billing_subscriptions;
pub mod channel_advanced;
pub mod channel_core;
pub mod channel_ops;
pub mod control_admin;
pub mod control_public;
pub mod deployment;
pub mod developer_access;
pub mod discount_code;
pub mod dynamic_pricing;
pub mod finance;
pub mod finance_export;
pub mod gifts;
pub mod hero_sms;
pub mod identity_2fa;
pub mod identity_admin;
pub mod identity_federation;
pub mod identity_profile;
pub mod identity_security;
pub mod kling_task_reads;
pub mod mcp;
pub mod media_midjourney;
pub mod media_tasks;
pub mod missing_billing_dashboard;
pub mod missing_billing_webhooks;
pub mod missing_control_public;
pub mod missing_control_ratio_sync;
pub mod missing_control_tasks;
pub mod missing_identity_catalog;
pub mod missing_identity_checkin_aff;
pub mod missing_identity_epay;
pub mod missing_identity_stripe_creem;
pub mod missing_identity_topup;
pub mod missing_identity_waffo;
pub mod missing_relay_misc_new;
pub mod missing_relay_models_billing;
pub mod missing_relay_video;
pub mod observability;
pub mod open_source_bounties;
pub mod public_relay;
pub mod relay_anthropic_gemini;
pub mod relay_anthropic_gemini_postgres;
pub mod relay_media;
pub mod relay_misc;
pub mod relay_misc_active;
pub mod relay_misc_frozen;
pub mod relay_misc_postgres;
pub mod relay_openai;
pub mod release_notes;
pub mod responses_websocket;
pub mod security_admin;
pub mod security_overview;
pub mod sse;
pub mod system_config;
pub mod unified_todo;
pub mod user_assistant_admin;
pub mod user_rankings;
pub mod verify_email;
