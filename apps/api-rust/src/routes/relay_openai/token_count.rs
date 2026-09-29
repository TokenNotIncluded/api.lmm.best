//! Current Go token counting, including its intentionally older model lookup.
//! Do not use tiktoken-rs's evolving model map: Go v0.6.2 falls back to
//! cl100k for several newer model names, including gpt-5 and o4.

use std::collections::BTreeMap;

use serde_json::{Value, value::RawValue};

use super::OpenAiRelayEndpoint;

mod go_unicode;

pub(super) fn count_text(text: &str, model: &str) -> i64 {
    if text.is_empty() {
        return 0;
    }
    if !is_openai(model) {
        return estimate_text(text, model);
    }
    // Detection is case-insensitive in common.IsOpenAITextModel; the codec
    // lookup itself is case-sensitive in tokenizer.ForModel.
    let o200k = matches!(model, "o1-preview" | "o1-mini" | "o3-mini" | "gpt-4o")
        || ["o1-", "chatgpt-4o-", "gpt-4o-"]
            .iter()
            .any(|prefix| model.starts_with(prefix));
    let encoder = if o200k {
        tiktoken_rs::o200k_base_singleton()
    } else {
        tiktoken_rs::cl100k_base_singleton()
    };
    i64::try_from(encoder.count_ordinary(text)).unwrap_or(i64::MAX)
}

fn is_openai(model: &str) -> bool {
    let model = model.to_lowercase();
    ["gpt-", "o1", "o3", "o4", "chatgpt"]
        .iter()
        .any(|marker| model.contains(marker))
}

fn in_table(ch: char, ranges: &[(u32, u32, u32)]) -> bool {
    let ch = u32::from(ch);
    let index = ranges.partition_point(|(_, high, _)| *high < ch);
    ranges.get(index).is_some_and(|(low, high, stride)| {
        ch >= *low && ch <= *high && (ch - low).is_multiple_of(*stride)
    })
}

pub(super) fn estimate_text(text: &str, model: &str) -> i64 {
    // Order: word, number, CJK, symbol, math, URL, @, emoji, newline, space.
    let model = model.to_lowercase();
    let m: [f64; 10] = if model.contains("gemini") {
        [1.15, 2.8, 0.68, 0.38, 1.05, 1.2, 2.5, 1.08, 1.15, 0.2]
    } else if model.contains("claude") {
        [1.13, 1.63, 1.21, 0.4, 4.52, 1.26, 2.82, 2.6, 0.89, 0.39]
    } else {
        [1.02, 1.55, 0.85, 0.4, 2.68, 1.0, 2.0, 2.12, 0.5, 0.42]
    };
    let mut count = 0.0_f64;
    let mut previous = 0;
    let mut run: usize = 0;
    for ch in text.chars() {
        let number = in_table(ch, go_unicode::NUMBERS);
        let cost = if in_table(ch, go_unicode::SPACE) {
            if ch == '\n' || ch == '\t' { m[8] } else { m[9] }
        } else if in_table(ch, go_unicode::HAN)
            || ('\u{3040}'..='\u{30ff}').contains(&ch)
            || ('\u{ac00}'..='\u{d7a3}').contains(&ch)
        {
            m[2]
        } else if ('\u{1f300}'..='\u{1f9ff}').contains(&ch)
            || ('\u{2600}'..='\u{26ff}').contains(&ch)
            || ('\u{2700}'..='\u{27bf}').contains(&ch)
            || ('\u{1fa00}'..='\u{1faff}').contains(&ch)
        {
            m[7]
        } else if number || in_table(ch, go_unicode::LETTERS) {
            let kind = if number { 2 } else { 1 };
            if previous != kind {
                count += if number { m[1] } else { m[0] };
                run = usize::from(number);
                previous = kind;
            } else if number {
                run += 1;
                if (run - 1).is_multiple_of(3) {
                    count += 1.0;
                }
            }
            continue;
        } else if "∑∫∂√∞≤≥≠≈±×÷∈∉∋∌⊂⊃⊆⊇∪∩∧∨¬∀∃∄∅∆∇∝∟∠∡∢°′″‴⁺⁻⁼⁽⁾ⁿ₀₁₂₃₄₅₆₇₈₉₊₋₌₍₎²³¹⁴⁵⁶⁷⁸⁹⁰"
            .contains(ch)
            || ('\u{2200}'..='\u{22ff}').contains(&ch)
            || ('\u{2a00}'..='\u{2aff}').contains(&ch)
            || ('\u{1d400}'..='\u{1d7ff}').contains(&ch)
        {
            m[4]
        } else if ch == '@' {
            m[6]
        } else if "/:?&=;#%".contains(ch) {
            m[5]
        } else {
            m[3]
        };
        previous = 0;
        run = 0;
        count += cost;
    }
    count.ceil() as i64
}

#[derive(Debug, Default)]
pub(super) struct Estimate {
    pub tokens: i64,
    pub max_tokens: i64,
    #[cfg(test)]
    combined: String,
}

#[derive(Default)]
struct Metadata {
    texts: Vec<String>,
    overhead: i64,
    media: Vec<(String, String)>,
}

pub(super) fn request(
    endpoint: OpenAiRelayEndpoint,
    model: &str,
    raw: &[u8],
) -> Result<Estimate, String> {
    let fields: BTreeMap<String, &RawValue> =
        serde_json::from_slice(raw).map_err(|error| error.to_string())?;
    let mut meta = Metadata::default();
    let mut max_tokens = 0;
    let value: Value = serde_json::from_slice(raw).map_err(|error| error.to_string())?;
    match endpoint {
        OpenAiRelayEndpoint::Responses | OpenAiRelayEndpoint::ResponsesCompact => {
            let compact = endpoint == OpenAiRelayEndpoint::ResponsesCompact;
            if compact {
                raw_field(&fields, "instructions", &mut meta.texts);
            }
            if let Some(input) = fields.get("input") {
                collect_responses(input, &mut meta)?;
            }
            if !compact {
                for name in ["instructions", "metadata", "text", "tool_choice", "prompt"] {
                    raw_field(&fields, name, &mut meta.texts);
                }
                max_tokens = value
                    .get("max_output_tokens")
                    .and_then(Value::as_i64)
                    .unwrap_or(0)
                    .max(0);
            }
            raw_field(&fields, "tools", &mut meta.texts);
        }
        OpenAiRelayEndpoint::Completions | OpenAiRelayEndpoint::ChatCompletions => {
            collect_chat(&value, &mut meta);
            max_tokens = ["max_tokens", "max_completion_tokens"]
                .iter()
                .filter_map(|name| value.get(name).and_then(Value::as_i64))
                .max()
                .unwrap_or(0)
                .max(0);
        }
    }
    let combined = meta.texts.join("\n");
    let mut tokens = count_text(&combined, model).saturating_add(meta.overhead);
    let streaming = value
        .get("stream")
        .and_then(Value::as_bool)
        .unwrap_or(false);
    for (kind, detail) in meta.media {
        let count = match kind.as_str() {
            "audio" => 256,
            "video" => 8192,
            "image" if !is_openai(model) => 520,
            "image" => {
                let model = model.to_lowercase();
                let base = if model.starts_with("gpt-4o-mini") {
                    2833
                } else if model.starts_with("gpt-5-chat-latest")
                    || model.starts_with("gpt-5")
                        && !model.contains("mini")
                        && !model.contains("nano")
                {
                    70
                } else if model.starts_with("o1") || model.starts_with("o3") {
                    75
                } else if model.contains("computer-use-preview") {
                    65
                } else {
                    85
                };
                if detail == "low" {
                    base
                } else if !streaming {
                    3 * base
                } else {
                    return Err(
                        "streaming image token counting requires image dimensions".to_owned()
                    );
                }
            }
            _ => 4096,
        };
        tokens = tokens.saturating_add(count);
    }
    Ok(Estimate {
        tokens,
        max_tokens,
        #[cfg(test)]
        combined,
    })
}

fn raw_field(fields: &BTreeMap<String, &RawValue>, name: &str, texts: &mut Vec<String>) {
    if let Some(raw) = fields.get(name) {
        texts.push(normalize_unicode(raw.get()));
    }
}

fn collect_responses(raw: &RawValue, meta: &mut Metadata) -> Result<(), String> {
    let value: Value = serde_json::from_str(raw.get()).map_err(|error| error.to_string())?;
    if let Some(text) = value.as_str() {
        meta.texts.push(text.to_owned());
        return Ok(());
    }
    if value.is_null() {
        meta.texts.push(String::new());
        return Ok(());
    }
    if value.is_array() {
        let values: Vec<&RawValue> =
            serde_json::from_str(raw.get()).map_err(|error| error.to_string())?;
        for raw in values {
            collect_responses(raw, meta)?;
        }
        return Ok(());
    }
    let kind = value
        .get("type")
        .and_then(Value::as_str)
        .unwrap_or_default();
    match kind {
        "reasoning" => {
            for key in ["summary", "content"] {
                if let Some(parts) = value.get(key).and_then(Value::as_array) {
                    for part in parts {
                        meta.texts.push(
                            part.get("text")
                                .and_then(Value::as_str)
                                .unwrap_or_default()
                                .to_owned(),
                        );
                    }
                }
            }
            return Ok(());
        }
        "input_text" | "output_text" => {
            meta.texts.push(
                value
                    .get("text")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
            );
            return Ok(());
        }
        "input_image" if media_url(value.get("image_url")).is_some() => {
            meta.media.push((
                "image".to_owned(),
                value
                    .get("detail")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
            ));
            return Ok(());
        }
        "input_file"
            if media_url(value.get("file_url")).is_some()
                || value
                    .get("file_data")
                    .and_then(Value::as_str)
                    .is_some_and(|data| !data.is_empty()) =>
        {
            meta.media.push(("file".to_owned(), String::new()));
            return Ok(());
        }
        "" | "message" => {
            if let Some(role) = value
                .get("role")
                .and_then(Value::as_str)
                .filter(|role| !role.is_empty())
            {
                let fields: BTreeMap<String, &RawValue> =
                    serde_json::from_str(raw.get()).map_err(|error| error.to_string())?;
                if let Some(content) = fields.get("content") {
                    meta.texts.push(role.to_owned());
                    collect_responses(content, meta)?;
                    return Ok(());
                }
            }
        }
        _ => {}
    }
    meta.texts.push(normalize_unicode(raw.get()));
    Ok(())
}

fn media_url(value: Option<&Value>) -> Option<&str> {
    value
        .and_then(|value| {
            value
                .as_str()
                .or_else(|| value.get("url").and_then(Value::as_str))
        })
        .filter(|url| !url.is_empty())
}

fn collect_chat(value: &Value, meta: &mut Metadata) {
    for name in ["prompt", "input"] {
        if let Some(value) = value.get(name).filter(|value| !value.is_null()) {
            if let Some(text) = value.as_str() {
                meta.texts.push(text.to_owned());
            } else if let Some(items) = value.as_array() {
                meta.texts
                    .extend(items.iter().filter_map(Value::as_str).map(str::to_owned));
            } else if name == "prompt" {
                meta.texts.push(go_display(value));
            }
        }
    }
    let mut tools = Vec::new();
    if let Some(messages) = value.get("messages").and_then(Value::as_array) {
        for message in messages {
            meta.overhead += 3;
            meta.texts.push(
                message
                    .get("role")
                    .and_then(Value::as_str)
                    .unwrap_or_default()
                    .to_owned(),
            );
            if let Some(dynamic) = message.get("tools").and_then(Value::as_array) {
                tools.extend(dynamic.iter());
            }
            if let Some(content) = message.get("content").filter(|value| !value.is_null()) {
                if let Some(name) = message.get("name").and_then(Value::as_str) {
                    meta.overhead += 3;
                    meta.texts.push(name.to_owned());
                }
                if let Some(text) = content.as_str() {
                    meta.texts.push(text.to_owned());
                } else if let Some(parts) = content.as_array() {
                    for part in parts {
                        match part.get("type").and_then(Value::as_str) {
                            Some("text") => {
                                if let Some(text) = part.get("text").and_then(Value::as_str) {
                                    meta.texts.push(text.to_owned());
                                }
                            }
                            Some("image_url") if media_url(part.get("image_url")).is_some() => {
                                meta.media.push((
                                    "image".to_owned(),
                                    part.pointer("/image_url/detail")
                                        .and_then(Value::as_str)
                                        .unwrap_or("high")
                                        .to_owned(),
                                ))
                            }
                            Some("input_audio")
                                if part
                                    .pointer("/input_audio/data")
                                    .and_then(Value::as_str)
                                    .is_some() =>
                            {
                                meta.media.push(("audio".to_owned(), String::new()))
                            }
                            Some("video_url") => {
                                meta.media.push(("video".to_owned(), String::new()))
                            }
                            Some("file") => meta.media.push(("file".to_owned(), String::new())),
                            _ => {}
                        }
                    }
                }
            }
        }
    }
    if let Some(top_tools) = value.get("tools").and_then(Value::as_array) {
        tools.extend(top_tools);
    }
    for tool in tools {
        meta.overhead += 8;
        let function = &tool["function"];
        meta.texts.push(
            function
                .get("name")
                .and_then(Value::as_str)
                .unwrap_or_default()
                .to_owned(),
        );
        if let Some(description) = function
            .get("description")
            .and_then(Value::as_str)
            .filter(|value| !value.is_empty())
        {
            meta.texts.push(description.to_owned());
        }
        if let Some(parameters) = function.get("parameters").filter(|value| !value.is_null()) {
            meta.texts.push(go_display(parameters));
        }
    }
    meta.overhead += 3;
}

fn go_display(value: &Value) -> String {
    match value {
        Value::String(value) => value.clone(),
        Value::Null => "<nil>".to_owned(),
        Value::Array(values) => format!(
            "[{}]",
            values.iter().map(go_display).collect::<Vec<_>>().join(" ")
        ),
        Value::Object(values) => format!(
            "map[{}]",
            values
                .iter()
                .map(|(key, value)| format!("{key}:{}", go_display(value)))
                .collect::<Vec<_>>()
                .join(" ")
        ),
        _ => value.to_string(),
    }
}

fn normalize_unicode(raw: &str) -> String {
    let bytes = raw.as_bytes();
    let mut out = Vec::with_capacity(bytes.len());
    let mut in_string = false;
    let mut i = 0;
    while i < bytes.len() {
        let byte = bytes[i];
        if byte == b'"' {
            in_string = !in_string;
            out.push(byte);
            i += 1;
            continue;
        }
        if !in_string || byte != b'\\' || i + 1 >= bytes.len() {
            out.push(byte);
            i += 1;
            continue;
        }
        if bytes[i + 1] != b'u' || i + 6 > bytes.len() {
            out.extend_from_slice(&bytes[i..i + 2]);
            i += 2;
            continue;
        }
        let hex = |start: usize| {
            std::str::from_utf8(&bytes[start..start + 4])
                .ok()
                .and_then(|raw| u16::from_str_radix(raw, 16).ok())
        };
        let Some(first) = hex(i + 2) else {
            out.extend_from_slice(&bytes[i..i + 2]);
            i += 2;
            continue;
        };
        let (code, consumed) = if (0xd800..=0xdbff).contains(&first) {
            if i + 12 <= bytes.len() && bytes[i + 6..i + 8] == *b"\\u" {
                if let Some(second) = hex(i + 8).filter(|value| (0xdc00..=0xdfff).contains(value)) {
                    (
                        0x10000 + ((u32::from(first) - 0xd800) << 10) + u32::from(second) - 0xdc00,
                        12,
                    )
                } else {
                    out.extend_from_slice(&bytes[i..i + 6]);
                    i += 6;
                    continue;
                }
            } else {
                out.extend_from_slice(&bytes[i..i + 6]);
                i += 6;
                continue;
            }
        } else if (0xdc00..=0xdfff).contains(&first) {
            out.extend_from_slice(&bytes[i..i + 6]);
            i += 6;
            continue;
        } else {
            (u32::from(first), 6)
        };
        if let Some(ch) = char::from_u32(code) {
            let mut buffer = [0; 4];
            out.extend_from_slice(ch.encode_utf8(&mut buffer).as_bytes());
        }
        i += consumed;
    }
    String::from_utf8(out).unwrap_or_else(|_| raw.to_owned())
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn current_go_text_and_request_count_vectors() {
        let vectors: Vec<Value> = serde_json::from_str(include_str!(
            "../../../tests/behavior-oracle/fixtures/relay-token-counts.json"
        ))
        .unwrap();
        assert_eq!(vectors.len(), 208);
        for vector in vectors {
            let model = vector["model"].as_str().unwrap();
            let expected = vector["tokens"].as_i64().unwrap();
            if let Some(raw) = vector.get("request").and_then(Value::as_str) {
                let endpoint = match vector["endpoint"].as_str().unwrap() {
                    "responses" => OpenAiRelayEndpoint::Responses,
                    "compact" => OpenAiRelayEndpoint::ResponsesCompact,
                    "completions" => OpenAiRelayEndpoint::Completions,
                    _ => OpenAiRelayEndpoint::ChatCompletions,
                };
                let actual = request(endpoint, model, raw.as_bytes()).unwrap();
                assert_eq!(
                    actual.combined,
                    vector["combined"].as_str().unwrap_or_default(),
                    "metadata: {model} {raw}"
                );
                assert_eq!(actual.tokens, expected, "request: {model} {raw}");
                assert_eq!(
                    actual.max_tokens,
                    vector["max_tokens"].as_i64().unwrap_or(0)
                );
            } else {
                let text = vector["text"].as_str().unwrap_or_default();
                assert_eq!(count_text(text, model), expected, "text: {model} {text}");
            }
        }
    }

    #[test]
    fn unicode_normalization_preserves_literal_escapes_and_whitespace() {
        for (raw, expected) in [
            (r#"{ "x":"\u4e2d" }"#, "{ \"x\":\"中\" }"),
            (r#"{"x":"\\u4e2d"}"#, r#"{"x":"\\u4e2d"}"#),
            (r#""\uD83D\uDE00""#, "\"😀\""),
            (r#""\uD83D!""#, r#""\uD83D!""#),
        ] {
            assert_eq!(normalize_unicode(raw), expected);
        }
    }
}
