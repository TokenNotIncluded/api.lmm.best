//! Explicit project identities and independently stated integration capabilities.

use serde::Serialize;

/// A capability with no verified adapter must never be promoted by discovery.
#[derive(Clone, Copy, Debug, Serialize, PartialEq, Eq)]
#[serde(rename_all = "snake_case")]
pub enum Capability {
    /// This release can inspect local evidence without modifying the target.
    InspectOnly,
    /// No implementation has been qualified for this action.
    Unavailable,
}

/// An individual software distribution; desktop and CLI identities stay separate.
#[derive(Debug, Serialize)]
pub struct Software {
    /// Stable CLI selector.
    pub id: &'static str,
    /// Display name.
    pub name: &'static str,
    /// Canonical project identity, not a download mirror.
    pub source: &'static str,
    /// Intended use.
    pub purpose: &'static str,
    /// Installation capability of this LMM release.
    pub installation: Capability,
    /// Integration capability of this LMM release.
    pub integration: Capability,
    /// Maintenance capability of this LMM release.
    pub maintenance: Capability,
    /// Verified version/OS/scenario tuples; an empty list means unverified.
    pub verified_combinations: &'static [&'static str],
    /// Binary names searched only in the current process's PATH.
    pub executables: &'static [&'static str],
}

const fn software(
    id: &'static str,
    name: &'static str,
    source: &'static str,
    purpose: &'static str,
    executables: &'static [&'static str],
) -> Software {
    Software {
        id,
        name,
        source,
        purpose,
        installation: Capability::Unavailable,
        integration: Capability::Unavailable,
        maintenance: Capability::InspectOnly,
        verified_combinations: &[],
        executables,
    }
}

/// Initial discovery catalog. No third-party setup is qualified yet.
pub static SOFTWARE: &[Software] = &[
    software(
        "astrbot",
        "AstrBot",
        "https://github.com/AstrBotDevs/AstrBot",
        "消息平台 Agent 与机器人运行环境",
        &["astrbot"],
    ),
    software(
        "cc-switch",
        "CC Switch",
        "https://github.com/farion1231/cc-switch",
        "服务商配置管理与本地路由",
        &["cc-switch"],
    ),
    software(
        "claude-code",
        "Claude Code CLI",
        "https://github.com/anthropics/claude-code",
        "终端编程助手",
        &["claude"],
    ),
    software(
        "codex",
        "OpenAI Codex CLI",
        "https://github.com/openai/codex",
        "终端编程助手",
        &["codex"],
    ),
    software(
        "gemini-cli",
        "Gemini CLI",
        "https://github.com/google-gemini/gemini-cli",
        "终端编程助手",
        &["gemini"],
    ),
    software(
        "pi",
        "Pi coding agent",
        "https://github.com/badlogic/pi-mono",
        "终端 Agent，LMM Provider 已有独立项目",
        &["pi"],
    ),
];

/// Look up a single unambiguous project.
pub fn find(id: &str) -> Option<&'static Software> {
    SOFTWARE.iter().find(|software| software.id == id)
}

/// Search project identity and purpose without network access.
pub fn search(query: &str) -> Vec<&'static Software> {
    let query = query.to_lowercase();
    SOFTWARE
        .iter()
        .filter(|item| {
            [item.id, item.name, item.purpose]
                .iter()
                .any(|text| text.to_lowercase().contains(&query))
        })
        .collect()
}
