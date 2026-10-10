//! Ollama transport admission for the OpenAI-compatible executor.
//!
//! This executor does not convert requests to Ollama's native chat/generate
//! protocol. Require the stored chat opt-in before forwarding an OpenAI body;
//! unsupported native requests must fail before quota reservation or I/O.

use axum::http::StatusCode;
use serde_json::{Map, Value};

use super::{OpenAiRelayEndpoint, OpenAiRelayFailure};

const CHANNEL_TYPE_OLLAMA: i64 = 4;

pub(super) fn validate_transport(
    channel_type: i64,
    endpoint: OpenAiRelayEndpoint,
    raw_settings: &str,
) -> Result<(), OpenAiRelayFailure> {
    if channel_type != CHANNEL_TYPE_OLLAMA
        || matches!(
            endpoint,
            OpenAiRelayEndpoint::Responses | OpenAiRelayEndpoint::ResponsesCompact
        )
    {
        return Ok(());
    }

    let invalid_settings = || {
        OpenAiRelayFailure::new(
            StatusCode::INTERNAL_SERVER_ERROR,
            "invalid_channel_settings",
            "Ollama channel settings are invalid",
        )
    };
    let settings = if raw_settings.trim().is_empty() {
        Map::new()
    } else {
        serde_json::from_str::<Map<String, Value>>(raw_settings).map_err(|_| invalid_settings())?
    };
    let enabled = match settings.get("ollama_openai_chat") {
        None => false,
        Some(Value::Bool(enabled)) => *enabled,
        Some(_) => return Err(invalid_settings()),
    };
    if endpoint == OpenAiRelayEndpoint::ChatCompletions && enabled {
        return Ok(());
    }

    Err(OpenAiRelayFailure::new(
        StatusCode::NOT_IMPLEMENTED,
        "ollama_native_transport_unsupported",
        "Rust relay does not support Ollama native chat or generate transport; use the Go relay or enable ollama_openai_chat for Chat Completions",
    ))
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn ollama_chat_requires_explicit_stored_opt_in() {
        for settings in ["", "{}", r#"{"ollama_openai_chat":false}"#] {
            let failure = validate_transport(4, OpenAiRelayEndpoint::ChatCompletions, settings)
                .expect_err("native Ollama chat must not silently use the OpenAI adapter");
            assert_eq!(failure.status, StatusCode::NOT_IMPLEMENTED);
            assert_eq!(failure.code, "ollama_native_transport_unsupported");
        }
        assert!(
            validate_transport(
                4,
                OpenAiRelayEndpoint::ChatCompletions,
                r#"{"ollama_openai_chat":true,"other_setting":"preserved"}"#,
            )
            .is_ok()
        );
    }

    #[test]
    fn ollama_chat_does_not_accept_invalid_flag_values() {
        for settings in [
            r#"{"ollama_openai_chat":"true"}"#,
            r#"{"ollama_openai_chat":1}"#,
            r#"{"ollama_openai_chat":null}"#,
            "[]",
            "null",
            "{",
        ] {
            let failure = validate_transport(4, OpenAiRelayEndpoint::ChatCompletions, settings)
                .expect_err("invalid channel settings must fail before upstream I/O");
            assert_eq!(failure.code, "invalid_channel_settings");
        }
    }

    #[test]
    fn ollama_completions_never_uses_the_chat_opt_in() {
        let failure = validate_transport(
            4,
            OpenAiRelayEndpoint::Completions,
            r#"{"ollama_openai_chat":true}"#,
        )
        .expect_err("Ollama native generate is not implemented in the Rust executor");
        assert_eq!(failure.code, "ollama_native_transport_unsupported");
    }

    #[test]
    fn existing_responses_and_other_channels_ignore_the_chat_opt_in() {
        for endpoint in [
            OpenAiRelayEndpoint::Responses,
            OpenAiRelayEndpoint::ResponsesCompact,
        ] {
            for settings in ["", r#"{"ollama_openai_chat":false}"#, "{"] {
                assert!(validate_transport(4, endpoint, settings).is_ok());
            }
        }
        assert!(validate_transport(1, OpenAiRelayEndpoint::ChatCompletions, "{").is_ok());
    }
}
