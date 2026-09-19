//! Policy decisions shared by future adapters; discovery never authorizes a write.

use serde::Serialize;
use thiserror::Error;

/// Authoritative selection read from the chosen software instance.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Selection {
    /// Qualified adapter confirms no provider has been configured.
    Unconfigured,
    /// LMM is currently selected.
    Lmm,
    /// User selected another provider, including after earlier LMM setup.
    Other,
    /// Selection could not be established.
    Unknown,
}

/// Explicit intent, separate from broad unattended confirmation.
#[derive(Debug, Clone, Copy, PartialEq, Eq)]
pub enum Intent {
    /// Add only; retain selection, even in a new installation.
    AddOnly,
    /// User explicitly requested a switch to LMM.
    Activate,
    /// Choose a conservative default from authoritative current state.
    Default,
    /// Sync, update and repair must always preserve user selection.
    Maintain,
}

/// The only selection change an adapter may propose.
#[derive(Debug, Clone, Copy, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Activation {
    /// Leave current provider and model selection unchanged.
    Preserve,
    /// Proposed change; still requires approval of the plan.
    ProposeLmm,
}

/// A condition that prevents a safe plan.
#[derive(Debug, Error, PartialEq, Eq)]
pub enum PlanError {
    /// Unknown state must not be silently replaced with defaults.
    #[error("无法确认当前服务商；需要先读取选定实例的实际配置")]
    UnknownSelection,
    /// A revoked application must not be silently granted permission again.
    #[error("应用授权已撤销；需要明确确认重新授权")]
    RevokedGrant,
    /// Cost checks must use a server-enforced reservation.
    #[error("付费验证需要明确同意、已知报价和服务端可执行的消费上限")]
    PaidVerificationNotAuthorized,
}

/// Plan activation without turning setup repetition into a provider switch.
pub fn activation(selection: Selection, intent: Intent) -> Result<Activation, PlanError> {
    if matches!(intent, Intent::AddOnly | Intent::Maintain) {
        return Ok(Activation::Preserve);
    }
    if selection == Selection::Unknown {
        return Err(PlanError::UnknownSelection);
    }
    if intent == Intent::Activate || selection == Selection::Unconfigured {
        Ok(Activation::ProposeLmm)
    } else {
        Ok(Activation::Preserve)
    }
}

/// Require explicit reauthorization after revocation even under `--all` or `--yes`.
pub fn check_revocation(revoked: bool, explicitly_reauthorize: bool) -> Result<(), PlanError> {
    if revoked && !explicitly_reauthorize {
        Err(PlanError::RevokedGrant)
    } else {
        Ok(())
    }
}

/// Paid verification is possible only with an enforceable, positive budget.
/// Amounts use fixed integer micro-USD rather than floating-point balances.
pub fn check_paid_verification(
    consented: bool,
    quoted_micro_usd: Option<u64>,
    reserved_micro_usd: Option<u64>,
) -> Result<(), PlanError> {
    match (consented, quoted_micro_usd, reserved_micro_usd) {
        (true, Some(quote), Some(limit)) if limit > 0 && quote <= limit => Ok(()),
        _ => Err(PlanError::PaidVerificationNotAuthorized),
    }
}

/// A read-only setup preview; no unavailable adapter is represented as executable.
#[derive(Debug, Serialize)]
pub struct Preview {
    /// The selected software identity.
    pub software: &'static str,
    /// Current setup phase.
    pub outcome: &'static str,
    /// Nothing is applied when required capabilities are missing.
    pub changes: Vec<&'static str>,
    /// Account identity remains unknown without verified CLI login.
    pub account: &'static str,
    /// Requested selection intent; it is not an achieved state.
    pub activation: &'static str,
    /// Whether a model is changed by this plan.
    pub model_change: bool,
    /// This milestone never invokes a paid endpoint.
    pub paid_test: bool,
    /// No discovered service is restarted.
    pub restart: bool,
    /// Specific missing prerequisites.
    pub blockers: Vec<&'static str>,
}

impl Preview {
    /// Produce an honest blocked plan for integrations still under development.
    pub fn blocked(software: &'static str, intent: Intent) -> Self {
        Self {
            software,
            outcome: "blocked",
            changes: vec![],
            account: "not_checked",
            activation: if intent == Intent::Activate {
                "requested_not_applied"
            } else {
                "preserve"
            },
            model_change: false,
            paid_test: false,
            restart: false,
            blockers: vec![
                "此版本尚无经过验证的目标软件写入适配器",
                "CLI 登录只允许读取目录和余额；目标应用独立授权尚未接入",
                "未验证软件版本、配置管理关系及实际调用路径",
            ],
        }
    }
}
