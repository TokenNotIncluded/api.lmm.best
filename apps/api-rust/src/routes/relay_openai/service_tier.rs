//! Fail closed in the preview backend until it owns the accelerated tariff.
//! The production Go backend implements sync, reservation and settlement.

use super::OpenAiRelayFailure;
use axum::{body::Bytes, http::{HeaderMap, StatusCode}};

fn unavailable() -> OpenAiRelayFailure {
    OpenAiRelayFailure::new(StatusCode::BAD_REQUEST, "service_tier_pricing_unavailable", "Accelerated service-tier pricing requires the updated Go backend")
}
pub(super) fn guard_body(url: &reqwest::Url, headers: &HeaderMap, body: &Bytes) -> Result<Bytes, OpenAiRelayFailure> {
    if headers.contains_key("openai-service-tier") { return Err(unavailable()); }
    let mut value: serde_json::Value = serde_json::from_slice(body).map_err(|_| unavailable())?;
    let fields = value.as_object_mut().ok_or_else(unavailable)?;
    let tier = match fields.get("service_tier") {
        None | Some(serde_json::Value::Null) => "",
        Some(serde_json::Value::String(value)) => value.as_str(),
        _ => return Err(unavailable()),
    };
    if matches!(tier, "fast" | "priority" | "ultrafast") { return Err(unavailable()); }
    if url.host_str() != Some("api.openai.com") { return Ok(body.clone()); }
    match tier {
        "" | "auto" | "default" => {
            fields.insert("service_tier".to_owned(), serde_json::Value::String("default".to_owned()));
        }
        "flex" => return Ok(body.clone()),
        _ => return Err(unavailable()),
    }
    serde_json::to_vec(&value).map(Bytes::from).map_err(|_| unavailable())
}
#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn premium_body_and_headers_fail_closed() -> Result<(), Box<dyn std::error::Error>> {
        let url = reqwest::Url::parse("https://api.openai.com/v1/responses")?;
        for tier in ["fast", "priority", "ultrafast"] {
            let body = Bytes::from(serde_json::to_vec(&serde_json::json!({"model":"test", "service_tier":tier}))?);
            let error = guard_body(&url, &HeaderMap::new(), &body).err().ok_or("premium request was accepted")?;
            assert_eq!(error.code, "service_tier_pricing_unavailable");
        }
        let mut headers = HeaderMap::new();
        headers.insert("openai-service-tier", "ultrafast".parse()?);
        assert!(guard_body(&url, &headers, &Bytes::from_static(b"{}")).is_err());
        Ok(())
    }
    #[test]
    fn ordinary_official_calls_never_inherit_a_paid_project_default() -> Result<(), Box<dyn std::error::Error>> {
        let url = reqwest::Url::parse("https://api.openai.com/v1/responses")?;
        for body in [r#"{}"#, r#"{"service_tier":"auto"}"#] {
            let result = guard_body(&url, &HeaderMap::new(), &Bytes::copy_from_slice(body.as_bytes())).map_err(|failure| failure.message)?;
            let value: serde_json::Value = serde_json::from_slice(&result)?;
            assert_eq!(value["service_tier"], "default");
        }
        Ok(())
    }
}
