//! Strict complete-response evidence for the local synthetic Chat fixture.
//! This is a bounded test oracle, not a general provider or EventSource decoder.
use serde_json::Value;

fn envelope(value: &Value, id: &str, object: &str) -> bool {
    value.is_object()
        && value.get("error").is_none()
        && value["id"].as_str() == Some(id)
        && value["object"].as_str() == Some(object)
        && value["model"].as_str() == Some("local-model")
        && value["created"].as_u64().is_some()
}
fn usage(value: &Value) -> bool {
    value["prompt_tokens"].as_u64() == Some(10)
        && value["completion_tokens"].as_u64() == Some(20)
        && value["total_tokens"].as_u64() == Some(30)
        && value["prompt_tokens_details"]["cached_tokens"].as_u64() == Some(2)
}
fn choice(value: &Value) -> Option<&Value> {
    let choices = value["choices"].as_array()?;
    let [choice] = choices.as_slice() else {
        return None;
    };
    (choice.is_object() && choice["index"].as_u64() == Some(0) && choice.get("error").is_none())
        .then_some(choice)
}
#[derive(Default)]
struct Stream {
    started: bool,
    finished: bool,
    usage: bool,
    done: bool,
    answer: String,
    created: Option<u64>,
}
impl Stream {
    fn frame(&mut self, data: &str, id: &str) -> bool {
        if self.done {
            return false;
        }
        if data == "[DONE]" {
            self.done =
                self.started && self.finished && self.usage && self.answer == "local stream answer";
            return self.done;
        }
        let Ok(value) = serde_json::from_str::<Value>(data) else {
            return false;
        };
        if !envelope(&value, id, "chat.completion.chunk") {
            return false;
        }
        let created = value["created"].as_u64();
        if self.created.is_some() && self.created != created {
            return false;
        }
        self.created = created;
        if value.get("usage").is_some() {
            if !self.finished
                || self.usage
                || value["choices"].as_array().is_none_or(|x| !x.is_empty())
                || !usage(&value["usage"])
            {
                return false;
            }
            self.usage = true;
            return true;
        }
        if self.finished || self.usage {
            return false;
        }
        let Some(choice) = choice(&value) else {
            return false;
        };
        let Some(delta) = choice["delta"].as_object() else {
            return false;
        };
        if choice["finish_reason"].as_str() == Some("stop") {
            if !self.started || !delta.is_empty() {
                return false;
            }
            self.finished = true;
            return true;
        }
        if choice.get("finish_reason") != Some(&Value::Null) {
            return false;
        }
        if !self.started {
            if delta.len() != 2
                || choice["delta"]["role"] != "assistant"
                || choice["delta"]["content"].as_str() != Some("")
            {
                return false;
            }
            self.started = true;
            return true;
        }
        let Some(content) = choice["delta"]["content"].as_str() else {
            return false;
        };
        if delta.len() != 1 {
            return false;
        }
        self.answer.push_str(content);
        "local stream answer".starts_with(&self.answer)
    }
}
fn streaming(text: &str, id: &str) -> bool {
    // SSE line/data framing follows WHATWG: CR/LF/CRLF, one initial BOM,
    // one optional space after colon, and joined data lines per dispatched event.
    // Only data and comments occur in this fixture; other fields fail closed.
    let normalized = text
        .strip_prefix('\u{feff}')
        .unwrap_or(text)
        .replace("\r\n", "\n")
        .replace('\r', "\n");
    let mut state = Stream::default();
    let mut data = String::new();
    for line in normalized.split_terminator('\n') {
        if line.is_empty() {
            if !data.is_empty() {
                data.pop(); // The framing newline is not part of the event data.
                if !state.frame(&data, id) {
                    return false;
                }
                data.clear();
            }
        } else if !line.starts_with(':') {
            let Some(value) = line.strip_prefix("data:") else {
                return false;
            };
            if state.done {
                return false;
            }
            data.push_str(value.strip_prefix(' ').unwrap_or(value));
            data.push('\n');
        }
    }
    state.done && data.is_empty()
}

// @mark model.response-validation role=oracle
pub(super) fn valid_complete_body(bytes: &[u8], stream: bool, request_id: &str) -> bool {
    if bytes.len() > 16_384
        || request_id.len() != 64
        || !request_id.bytes().all(|x| x.is_ascii_hexdigit())
    {
        return false;
    }
    let Ok(text) = std::str::from_utf8(bytes) else {
        return false;
    };
    let id = format!("chatcmpl_{request_id}");
    if stream {
        return streaming(text, &id);
    }
    let Ok(value) = serde_json::from_str::<Value>(text) else {
        return false;
    };
    if !envelope(&value, &id, "chat.completion") || !usage(&value["usage"]) {
        return false;
    }
    let Some(choice) = choice(&value) else {
        return false;
    };
    let Some(message) = choice["message"].as_object() else {
        return false;
    };
    message.len() == 2
        && choice["message"]["role"] == "assistant"
        && choice["message"]["content"] == "local synthetic answer"
        && choice["finish_reason"] == "stop"
}
