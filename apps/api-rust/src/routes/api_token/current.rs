//! Current token-management policy shared by list, detail and mutations.

use super::{ApiToken, TokenError};

pub(super) const VISIBLE: &str =
    "COALESCE((to_jsonb(tokens)->>'oauth_managed')::boolean,FALSE)=FALSE";
pub(super) const COUNTED: &str = "COALESCE((to_jsonb(tokens)->>'oauth_managed')::boolean,FALSE)=FALSE AND COALESCE(to_jsonb(tokens)->>'creation_source','')<>'assistant_runtime'";
pub(super) const REVEALABLE: &str = "COALESCE((to_jsonb(tokens)->>'oauth_managed')::boolean,FALSE)=FALSE AND COALESCE((to_jsonb(tokens)->>'one_time_reveal')::boolean,FALSE)=FALSE";

pub(super) fn mutable(token: &ApiToken) -> Result<(), TokenError> {
    if token.creation_source == "assistant_runtime" {
        Err(TokenError::invalid(
            "the assistant runtime key is managed by the system",
        ))
    } else {
        Ok(())
    }
}

pub(super) fn creation_filter(mode: &str) -> Result<&'static str, TokenError> {
    match mode {
        "" => Ok("TRUE"),
        "manual" => Ok(
            "COALESCE(to_jsonb(tokens)->>'creation_source','') IN ('','manual') AND name NOT LIKE '%的初始令牌'",
        ),
        "automatic" => Ok(
            "COALESCE(to_jsonb(tokens)->>'creation_source','') NOT IN ('','manual') OR name LIKE '%的初始令牌'",
        ),
        _ => Err(TokenError::invalid("无效的令牌创建方式")),
    }
}
