//! Provider decoders share a bounded block model. Usage counters are cumulative.
use super::sse::Frame;
use super::{BlockKind, Event, FinishReason, Limits, RelayError, UpstreamProtocol, Usage};
use serde_json::Value;
use std::collections::BTreeMap;

type Key = (u64, u64);
struct Slot {
    index: usize,
    kind: BlockKind,
    ended: bool,
    has_text: bool,
    default_object: bool,
}
pub(crate) struct Decoder {
    protocol: UpstreamProtocol,
    started: bool,
    source_id: Option<String>,
    done: bool,
    finish: Option<FinishReason>,
    blocks: BTreeMap<Key, Slot>,
    max_blocks: usize,
    gemini_calls: u64,
}
fn text(v: &Value) -> Result<&str, RelayError> {
    v.as_str()
        .ok_or(RelayError::Protocol("expected upstream string"))
}
fn index(v: &Value) -> Result<u64, RelayError> {
    v.as_u64()
        .ok_or(RelayError::Protocol("invalid upstream index"))
}
fn counter(v: &Value, key: &str) -> Result<Option<u64>, RelayError> {
    v.get(key)
        .filter(|v| !v.is_null())
        .map(|v| {
            v.as_u64()
                .ok_or(RelayError::Protocol("invalid usage counter"))
        })
        .transpose()
}
fn add(a: Option<u64>, b: Option<u64>, c: Option<u64>) -> Result<Option<u64>, RelayError> {
    a.map(|a| {
        a.checked_add(b.unwrap_or(0))
            .and_then(|n| n.checked_add(c.unwrap_or(0)))
            .ok_or(RelayError::Protocol("usage overflow"))
    })
    .transpose()
}
fn usage(protocol: UpstreamProtocol, v: &Value) -> Result<Usage, RelayError> {
    if v.is_null() {
        return Ok(Usage::default());
    }
    if !v.is_object() {
        return Err(RelayError::Protocol("invalid usage object"));
    }
    let mut u = Usage::default();
    match protocol {
        UpstreamProtocol::Chat => {
            u.input_tokens = counter(v, "prompt_tokens")?;
            u.output_tokens = counter(v, "completion_tokens")?;
            u.cached_input_tokens = counter(&v["prompt_tokens_details"], "cached_tokens")?;
            u.reasoning_output_tokens =
                counter(&v["completion_tokens_details"], "reasoning_tokens")?;
        }
        UpstreamProtocol::Responses => {
            u.input_tokens = counter(v, "input_tokens")?;
            u.output_tokens = counter(v, "output_tokens")?;
            u.cached_input_tokens = counter(&v["input_tokens_details"], "cached_tokens")?;
            u.reasoning_output_tokens = counter(&v["output_tokens_details"], "reasoning_tokens")?;
        }
        UpstreamProtocol::Anthropic => {
            u.cached_input_tokens = counter(v, "cache_read_input_tokens")?;
            u.cache_creation_input_tokens = counter(v, "cache_creation_input_tokens")?;
            u.input_tokens = add(
                counter(v, "input_tokens")?,
                u.cached_input_tokens,
                u.cache_creation_input_tokens,
            )?;
            u.output_tokens = counter(v, "output_tokens")?;
        }
        UpstreamProtocol::Gemini => {
            u.input_tokens = counter(v, "promptTokenCount")?;
            u.cached_input_tokens = counter(v, "cachedContentTokenCount")?;
            u.reasoning_output_tokens = counter(v, "thoughtsTokenCount")?;
            u.output_tokens = add(
                counter(v, "candidatesTokenCount")?,
                u.reasoning_output_tokens,
                None,
            )?;
        }
    }
    Ok(u)
}
fn finish_reason(
    protocol: UpstreamProtocol,
    v: &str,
    tools: bool,
) -> Result<FinishReason, RelayError> {
    match (protocol, v) {
        (UpstreamProtocol::Chat, "stop")
        | (UpstreamProtocol::Anthropic, "end_turn" | "stop_sequence") => Ok(FinishReason::Stop),
        (UpstreamProtocol::Chat, "tool_calls" | "function_call")
        | (UpstreamProtocol::Anthropic, "tool_use") => Ok(FinishReason::ToolCalls),
        (UpstreamProtocol::Chat, "length")
        | (UpstreamProtocol::Anthropic, "max_tokens")
        | (UpstreamProtocol::Gemini, "MAX_TOKENS") => Ok(FinishReason::Length),
        (UpstreamProtocol::Chat, "content_filter")
        | (UpstreamProtocol::Anthropic, "refusal")
        | (
            UpstreamProtocol::Gemini,
            "SAFETY" | "RECITATION" | "BLOCKLIST" | "PROHIBITED_CONTENT" | "SPII" | "IMAGE_SAFETY",
        ) => Ok(FinishReason::ContentFilter),
        (UpstreamProtocol::Gemini, "STOP") => Ok(if tools {
            FinishReason::ToolCalls
        } else {
            FinishReason::Stop
        }),
        _ => Err(RelayError::Protocol("unsupported upstream finish reason")),
    }
}
fn plain_response_part(part: &Value) -> Result<(), RelayError> {
    if part
        .get("annotations")
        .is_some_and(|a| !a.is_null() && a.as_array().is_none_or(|a| !a.is_empty()))
    {
        return Err(RelayError::Protocol(
            "annotated Responses output is not supported",
        ));
    }
    Ok(())
}
fn plain_response_item(item: &Value) -> Result<(), RelayError> {
    if item.get("encrypted_content").is_some_and(|v| !v.is_null()) {
        return Err(RelayError::Protocol(
            "encrypted Responses output is not supported",
        ));
    }
    if let Some(parts) = item.get("content").filter(|v| !v.is_null()) {
        for part in parts
            .as_array()
            .ok_or(RelayError::Protocol("invalid Responses content"))?
        {
            plain_response_part(part)?;
        }
    }
    Ok(())
}
fn plain_response(response: &Value) -> Result<(), RelayError> {
    if let Some(items) = response.get("output").filter(|v| !v.is_null()) {
        for item in items
            .as_array()
            .ok_or(RelayError::Protocol("invalid Responses output"))?
        {
            plain_response_item(item)?;
        }
    }
    Ok(())
}

impl Decoder {
    pub fn new(protocol: UpstreamProtocol, limits: &Limits) -> Self {
        Self {
            protocol,
            started: false,
            source_id: None,
            done: false,
            finish: None,
            blocks: BTreeMap::new(),
            max_blocks: limits.max_blocks,
            gemini_calls: 0,
        }
    }
    pub fn is_done(&self) -> bool {
        self.done
    }
    fn start(&mut self, id: Option<&str>, out: &mut Vec<Event>) -> Result<(), RelayError> {
        if let Some(id) = id {
            if id.is_empty() || id.len() > 256 || id.chars().any(char::is_control) {
                return Err(RelayError::Protocol("invalid upstream ID"));
            }
            if self.source_id.as_deref().is_some_and(|old| old != id) {
                return Err(RelayError::Protocol("upstream response ID changed"));
            }
            self.source_id = Some(id.to_owned());
        }
        if !self.started {
            self.started = true;
            out.push(Event::Start(self.source_id.clone().unwrap_or_default()));
        }
        Ok(())
    }
    fn tools(&self) -> bool {
        self.blocks
            .values()
            .any(|s| matches!(s.kind, BlockKind::Tool { .. }))
    }
    fn block(
        &mut self,
        key: Key,
        kind: BlockKind,
        out: &mut Vec<Event>,
    ) -> Result<usize, RelayError> {
        if self.finish.is_some() {
            return Err(RelayError::Protocol("content after finish"));
        }
        if let Some(s) = self.blocks.get(&key) {
            let compatible = match (&s.kind, &kind) {
                (BlockKind::Text, BlockKind::Text)
                | (BlockKind::Thinking, BlockKind::Thinking)
                | (BlockKind::Refusal, BlockKind::Refusal) => true,
                (BlockKind::Tool { id: a, name: b }, BlockKind::Tool { id: c, name: d }) => {
                    a == c && b == d
                }
                _ => false,
            };
            if s.ended || !compatible {
                return Err(RelayError::Protocol("invalid block transition"));
            }
            return Ok(s.index);
        }
        if self.blocks.len() >= self.max_blocks {
            return Err(RelayError::Limit("output block count"));
        }
        if let BlockKind::Tool { id, name } = &kind {
            if id.is_empty() || name.is_empty() || id.len() > 256 || name.len() > 128 {
                return Err(RelayError::Protocol("invalid tool identity"));
            }
            if self
                .blocks
                .values()
                .any(|s| matches!(&s.kind, BlockKind::Tool { id: old, .. } if old == id))
            {
                return Err(RelayError::Protocol("duplicate upstream tool ID"));
            }
        }
        let index = self.blocks.len();
        out.push(Event::Block {
            index,
            kind: kind.clone(),
        });
        self.blocks.insert(
            key,
            Slot {
                index,
                kind,
                ended: false,
                has_text: false,
                default_object: false,
            },
        );
        Ok(index)
    }
    fn delta(&mut self, key: Key, value: &str, out: &mut Vec<Event>) -> Result<(), RelayError> {
        let slot = self
            .blocks
            .get_mut(&key)
            .ok_or(RelayError::Protocol("delta without a block"))?;
        if slot.ended || self.finish.is_some() {
            return Err(RelayError::Protocol("delta after block end"));
        }
        if !value.is_empty() {
            slot.has_text = true;
            out.push(Event::Delta {
                index: slot.index,
                text: value.to_owned(),
            });
        }
        Ok(())
    }
    fn simple(
        &mut self,
        key: Key,
        kind: BlockKind,
        value: &str,
        out: &mut Vec<Event>,
    ) -> Result<(), RelayError> {
        self.block(key, kind, out)?;
        self.delta(key, value, out)
    }
    fn signature(&self, key: Key, value: &str, out: &mut Vec<Event>) -> Result<(), RelayError> {
        let s = self
            .blocks
            .get(&key)
            .ok_or(RelayError::Protocol("signature without a block"))?;
        if s.ended {
            return Err(RelayError::Protocol("signature after block end"));
        }
        out.push(Event::Signature {
            index: s.index,
            provider: self.protocol,
            value: value.to_owned(),
        });
        Ok(())
    }
    fn close(&mut self, key: Key, out: &mut Vec<Event>) -> Result<(), RelayError> {
        let s = self
            .blocks
            .get_mut(&key)
            .ok_or(RelayError::Protocol("unknown block end"))?;
        if !s.ended {
            if s.default_object && !s.has_text {
                out.push(Event::Delta {
                    index: s.index,
                    text: "{}".into(),
                });
            }
            s.ended = true;
            out.push(Event::EndBlock(s.index));
        }
        Ok(())
    }
    fn close_all(&mut self, out: &mut Vec<Event>) -> Result<(), RelayError> {
        let keys: Vec<_> = self.blocks.keys().copied().collect();
        for key in keys {
            self.close(key, out)?;
        }
        Ok(())
    }
    fn finish(&mut self, reason: FinishReason, out: &mut Vec<Event>) -> Result<(), RelayError> {
        if self.finish.is_some() {
            return Err(RelayError::Protocol("duplicate finish"));
        }
        self.close_all(out)?;
        self.finish = Some(reason);
        out.push(Event::Finish(reason));
        Ok(())
    }
    fn done(&mut self, out: &mut Vec<Event>) -> Result<(), RelayError> {
        if self.finish.is_none() || self.done {
            return Err(RelayError::Truncated);
        }
        self.done = true;
        out.push(Event::Done);
        Ok(())
    }
    pub fn frame(&mut self, frame: Frame) -> Result<Vec<Event>, RelayError> {
        if self.done {
            return Err(RelayError::Protocol("event after terminal event"));
        }
        let mut out = Vec::new();
        if frame.data.is_empty() {
            return Ok(out);
        }
        if frame.data == "[DONE]" {
            if self.protocol != UpstreamProtocol::Chat {
                return Err(RelayError::Protocol("unexpected DONE marker"));
            }
            self.done(&mut out)?;
            return Ok(out);
        }
        let v: Value = serde_json::from_str(&frame.data)
            .map_err(|_| RelayError::Protocol("invalid SSE JSON"))?;
        if !v.is_object() {
            return Err(RelayError::Protocol("upstream event is not an object"));
        }
        if v.get("error").is_some_and(|e| !e.is_null()) {
            return Err(RelayError::Protocol("upstream reported an error"));
        }
        if let (Some(wire), Some(kind)) = (frame.event.as_deref(), v["type"].as_str())
            && wire != "message"
            && wire != kind
        {
            return Err(RelayError::Protocol("SSE event type mismatch"));
        }
        match self.protocol {
            UpstreamProtocol::Chat => self.chat(&v, false, &mut out)?,
            UpstreamProtocol::Responses => self.responses_event(&v, &mut out)?,
            UpstreamProtocol::Anthropic => self.anthropic_event(&v, &mut out)?,
            UpstreamProtocol::Gemini => self.gemini(&v, &mut out)?,
        }
        Ok(out)
    }
    pub fn eof(&mut self) -> Result<Vec<Event>, RelayError> {
        let mut out = Vec::new();
        // Gemini ends with HTTP EOF, but still must have a semantic finish reason.
        if !self.done && self.protocol == UpstreamProtocol::Gemini {
            self.done(&mut out)?;
        }
        if !self.done {
            return Err(RelayError::Truncated);
        }
        Ok(out)
    }
    pub fn json(&mut self, v: &Value) -> Result<Vec<Event>, RelayError> {
        if !v.is_object() || v.get("error").is_some_and(|e| !e.is_null()) {
            return Err(RelayError::Protocol("invalid upstream JSON response"));
        }
        let mut out = Vec::new();
        match self.protocol {
            UpstreamProtocol::Chat => self.chat(v, true, &mut out)?,
            UpstreamProtocol::Responses => {
                plain_response(v)?;
                self.start(v["id"].as_str(), &mut out)?;
                for (i, item) in v["output"]
                    .as_array()
                    .ok_or(RelayError::Protocol("missing Responses output"))?
                    .iter()
                    .enumerate()
                {
                    let i = i as u64;
                    match item["type"].as_str() {
                        Some("function_call") => {
                            self.block(
                                (i, u64::MAX),
                                BlockKind::Tool {
                                    id: text(&item["call_id"])?.into(),
                                    name: text(&item["name"])?.into(),
                                },
                                &mut out,
                            )?;
                            self.delta((i, u64::MAX), text(&item["arguments"])?, &mut out)?;
                        }
                        Some("message") => {
                            for (j, p) in item["content"]
                                .as_array()
                                .ok_or(RelayError::Protocol("invalid Responses content"))?
                                .iter()
                                .enumerate()
                            {
                                let (kind, s) = match p["type"].as_str() {
                                    Some("output_text") => (BlockKind::Text, text(&p["text"])?),
                                    Some("refusal") => (BlockKind::Refusal, text(&p["refusal"])?),
                                    _ => {
                                        return Err(RelayError::Protocol(
                                            "unsupported output part",
                                        ));
                                    }
                                };
                                self.simple((i, j as u64), kind, s, &mut out)?;
                            }
                        }
                        Some("reasoning") => {
                            for (j, p) in item["summary"]
                                .as_array()
                                .ok_or(RelayError::Protocol("invalid reasoning summary"))?
                                .iter()
                                .enumerate()
                            {
                                self.simple(
                                    (i, j as u64),
                                    BlockKind::Thinking,
                                    text(&p["text"])?,
                                    &mut out,
                                )?;
                            }
                            if item.get("encrypted_content").is_some_and(|e| !e.is_null()) {
                                return Err(RelayError::Protocol(
                                    "encrypted Responses output is not supported",
                                ));
                            }
                        }
                        _ => return Err(RelayError::Protocol("unsupported Responses output item")),
                    }
                }
                self.response_finish(v, &mut out)?;
            }
            UpstreamProtocol::Anthropic => {
                self.start(v["id"].as_str(), &mut out)?;
                for (i, b) in v["content"]
                    .as_array()
                    .ok_or(RelayError::Protocol("invalid Anthropic content"))?
                    .iter()
                    .enumerate()
                {
                    self.anthropic_block((0, i as u64), b, false, &mut out)?;
                }
                out.push(Event::Usage(usage(self.protocol, &v["usage"])?));
                self.finish(
                    finish_reason(self.protocol, text(&v["stop_reason"])?, self.tools())?,
                    &mut out,
                )?;
            }
            UpstreamProtocol::Gemini => self.gemini(v, &mut out)?,
        }
        self.done(&mut out)?;
        Ok(out)
    }
    fn chat(&mut self, v: &Value, full: bool, out: &mut Vec<Event>) -> Result<(), RelayError> {
        self.start(v["id"].as_str(), out)?;
        let choices = v["choices"]
            .as_array()
            .ok_or(RelayError::Protocol("missing choices"))?;
        if choices.len() > 1 || (full && choices.len() != 1) {
            return Err(RelayError::Protocol(
                "only one upstream candidate is supported",
            ));
        }
        for c in choices {
            if c.get("index").is_some_and(|i| i.as_u64() != Some(0)) {
                return Err(RelayError::Protocol("invalid candidate index"));
            }
            let d = if full { &c["message"] } else { &c["delta"] };
            if !d.is_object() {
                return Err(RelayError::Protocol("missing message delta"));
            }
            for (field, key, kind) in [
                ("reasoning_content", (1, 0), BlockKind::Thinking),
                ("content", (0, 0), BlockKind::Text),
                ("refusal", (2, 0), BlockKind::Refusal),
            ] {
                if let Some(s) = d.get(field).filter(|s| !s.is_null()) {
                    self.simple(key, kind, text(s)?, out)?;
                }
            }
            if let Some(calls) = d.get("tool_calls").filter(|v| !v.is_null()) {
                for (ordinal, c) in calls
                    .as_array()
                    .ok_or(RelayError::Protocol("invalid tool calls"))?
                    .iter()
                    .enumerate()
                {
                    let key = (
                        3,
                        if full {
                            ordinal as u64
                        } else {
                            index(&c["index"])?
                        },
                    );
                    if !self.blocks.contains_key(&key) {
                        self.block(
                            key,
                            BlockKind::Tool {
                                id: text(&c["id"])?.into(),
                                name: text(&c["function"]["name"])?.into(),
                            },
                            out,
                        )?;
                    } else if let Some(Slot {
                        kind: BlockKind::Tool { id, name },
                        ..
                    }) = self.blocks.get(&key)
                        && (c
                            .get("id")
                            .and_then(Value::as_str)
                            .is_some_and(|s| !s.is_empty() && s != id)
                            || c["function"]
                                .get("name")
                                .and_then(Value::as_str)
                                .is_some_and(|s| !s.is_empty() && s != name))
                    {
                        return Err(RelayError::Protocol("tool identity changed"));
                    }
                    if let Some(a) = c["function"].get("arguments").filter(|a| !a.is_null()) {
                        self.delta(key, text(a)?, out)?;
                    }
                }
            }
            if d.get("function_call").is_some() {
                return Err(RelayError::Protocol(
                    "legacy function_call output is unsupported",
                ));
            }
            if let Some(reason) = c.get("finish_reason").filter(|r| !r.is_null()) {
                self.finish(
                    finish_reason(self.protocol, text(reason)?, self.tools())?,
                    out,
                )?;
            }
        }
        if let Some(u) = v.get("usage").filter(|u| !u.is_null()) {
            out.push(Event::Usage(usage(self.protocol, u)?));
        }
        Ok(())
    }
    fn anthropic_block(
        &mut self,
        key: Key,
        b: &Value,
        stream: bool,
        out: &mut Vec<Event>,
    ) -> Result<(), RelayError> {
        match b["type"].as_str() {
            Some("text") => self.simple(key, BlockKind::Text, text(&b["text"])?, out)?,
            Some("thinking") => {
                self.simple(key, BlockKind::Thinking, text(&b["thinking"])?, out)?;
                if let Some(s) = b.get("signature") {
                    self.signature(key, text(s)?, out)?;
                }
            }
            Some("tool_use") => {
                self.block(
                    key,
                    BlockKind::Tool {
                        id: text(&b["id"])?.into(),
                        name: text(&b["name"])?.into(),
                    },
                    out,
                )?;
                let input = b["input"]
                    .as_object()
                    .ok_or(RelayError::Protocol("invalid tool input"))?;
                if stream && input.is_empty() {
                    if let Some(s) = self.blocks.get_mut(&key) {
                        s.default_object = true;
                    }
                } else {
                    self.delta(key, &b["input"].to_string(), out)?;
                }
            }
            _ => return Err(RelayError::Protocol("unsupported Anthropic content block")),
        }
        Ok(())
    }
    fn anthropic_event(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        match v["type"].as_str() {
            Some("ping") => {}
            Some("message_start") => {
                if self.started {
                    return Err(RelayError::Protocol("duplicate message start"));
                }
                self.start(v["message"]["id"].as_str(), out)?;
                out.push(Event::Usage(usage(self.protocol, &v["message"]["usage"])?));
            }
            Some("content_block_start") => {
                if !self.started {
                    return Err(RelayError::Protocol("content before message start"));
                }
                let key = (0, index(&v["index"])?);
                if self.blocks.contains_key(&key) {
                    return Err(RelayError::Protocol("duplicate content block"));
                }
                self.anthropic_block(key, &v["content_block"], true, out)?;
            }
            Some("content_block_delta") => {
                let key = (0, index(&v["index"])?);
                let d = &v["delta"];
                let kind = &self
                    .blocks
                    .get(&key)
                    .ok_or(RelayError::Protocol("unknown delta block"))?
                    .kind;
                match (d["type"].as_str(), kind) {
                    (Some("text_delta"), BlockKind::Text) => {
                        self.delta(key, text(&d["text"])?, out)?
                    }
                    (Some("thinking_delta"), BlockKind::Thinking) => {
                        self.delta(key, text(&d["thinking"])?, out)?
                    }
                    (Some("input_json_delta"), BlockKind::Tool { .. }) => {
                        self.delta(key, text(&d["partial_json"])?, out)?
                    }
                    (Some("signature_delta"), BlockKind::Thinking) => {
                        self.signature(key, text(&d["signature"])?, out)?
                    }
                    _ => return Err(RelayError::Protocol("unsupported Anthropic delta")),
                }
            }
            Some("content_block_stop") => self.close((0, index(&v["index"])?), out)?,
            Some("message_delta") => {
                if let Some(u) = v.get("usage") {
                    out.push(Event::Usage(usage(self.protocol, u)?));
                }
                if let Some(r) = v["delta"].get("stop_reason").filter(|r| !r.is_null()) {
                    self.finish(finish_reason(self.protocol, text(r)?, self.tools())?, out)?;
                }
            }
            Some("message_stop") => self.done(out)?,
            Some("error") => return Err(RelayError::Protocol("upstream stream failed")),
            _ => return Err(RelayError::Protocol("unsupported Anthropic event")),
        }
        Ok(())
    }
    fn responses_event(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        if let Some(item) = v.get("item") {
            plain_response_item(item)?;
        }
        if let Some(part) = v.get("part") {
            plain_response_part(part)?;
        }
        if let Some(response) = v.get("response") {
            plain_response(response)?;
        }
        match v["type"].as_str() {
            Some("response.created" | "response.in_progress") => {
                self.start(v["response"]["id"].as_str(), out)?
            }
            Some("response.output_item.added") => {
                if !self.started {
                    return Err(RelayError::Protocol("output before response start"));
                }
                let i = index(&v["output_index"])?;
                let item = &v["item"];
                match item["type"].as_str() {
                    Some("function_call") => {
                        let key = (i, u64::MAX);
                        self.block(
                            key,
                            BlockKind::Tool {
                                id: text(&item["call_id"])?.into(),
                                name: text(&item["name"])?.into(),
                            },
                            out,
                        )?;
                        if let Some(a) = item.get("arguments") {
                            self.delta(key, text(a)?, out)?;
                        }
                    }
                    Some("message" | "reasoning") => {}
                    _ => return Err(RelayError::Protocol("unsupported Responses item")),
                }
            }
            Some("response.content_part.added") => {
                let key = (index(&v["output_index"])?, index(&v["content_index"])?);
                let p = &v["part"];
                let (kind, s) = match p["type"].as_str() {
                    Some("output_text") => (BlockKind::Text, text(&p["text"])?),
                    Some("refusal") => (BlockKind::Refusal, text(&p["refusal"])?),
                    _ => return Err(RelayError::Protocol("unsupported response content")),
                };
                self.simple(key, kind, s, out)?;
            }
            Some("response.reasoning_summary_part.added") => {
                let key = (index(&v["output_index"])?, index(&v["summary_index"])?);
                self.simple(key, BlockKind::Thinking, text(&v["part"]["text"])?, out)?;
            }
            Some("response.output_text.delta" | "response.refusal.delta") => {
                self.delta(
                    (index(&v["output_index"])?, index(&v["content_index"])?),
                    text(&v["delta"])?,
                    out,
                )?;
            }
            Some("response.reasoning_summary_text.delta") => self.delta(
                (index(&v["output_index"])?, index(&v["summary_index"])?),
                text(&v["delta"])?,
                out,
            )?,
            Some("response.function_call_arguments.delta") => self.delta(
                (index(&v["output_index"])?, u64::MAX),
                text(&v["delta"])?,
                out,
            )?,
            Some("response.function_call_arguments.done") => {
                let key = (index(&v["output_index"])?, u64::MAX);
                if self.blocks.get(&key).is_some_and(|s| !s.has_text) {
                    self.delta(key, text(&v["arguments"])?, out)?;
                }
            }
            Some("response.content_part.done") => self.close(
                (index(&v["output_index"])?, index(&v["content_index"])?),
                out,
            )?,
            Some("response.reasoning_summary_part.done") => self.close(
                (index(&v["output_index"])?, index(&v["summary_index"])?),
                out,
            )?,
            Some("response.output_item.done") => {
                let i = index(&v["output_index"])?;
                let keys: Vec<_> = self
                    .blocks
                    .keys()
                    .filter(|(a, _)| *a == i)
                    .copied()
                    .collect();
                for k in keys {
                    self.close(k, out)?;
                }
            }
            Some(
                "response.output_text.done"
                | "response.refusal.done"
                | "response.reasoning_summary_text.done",
            ) => {}
            Some("response.completed" | "response.incomplete") => {
                self.response_finish(&v["response"], out)?;
                self.done(out)?;
            }
            Some("response.failed" | "error") => {
                return Err(RelayError::Protocol("Responses stream failed"));
            }
            _ => return Err(RelayError::Protocol("unsupported Responses event")),
        }
        Ok(())
    }
    fn response_finish(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        out.push(Event::Usage(usage(self.protocol, &v["usage"])?));
        let reason = match v["status"].as_str() {
            Some("completed") => {
                if self.tools() {
                    FinishReason::ToolCalls
                } else {
                    FinishReason::Stop
                }
            }
            Some("incomplete") => match v["incomplete_details"]["reason"].as_str() {
                Some("max_output_tokens") => FinishReason::Length,
                Some("content_filter") => FinishReason::ContentFilter,
                _ => return Err(RelayError::Protocol("unknown incomplete response")),
            },
            _ => return Err(RelayError::Protocol("upstream response is not complete")),
        };
        self.finish(reason, out)
    }
    fn gemini(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        self.start(v["responseId"].as_str(), out)?;
        if let Some(u) = v.get("usageMetadata") {
            out.push(Event::Usage(usage(self.protocol, u)?));
        }
        if v["promptFeedback"].get("blockReason").is_some() {
            self.finish(FinishReason::ContentFilter, out)?;
            return Ok(());
        }
        let Some(candidates) = v.get("candidates") else {
            return Ok(());
        };
        let candidates = candidates
            .as_array()
            .ok_or(RelayError::Protocol("invalid Gemini candidates"))?;
        if candidates.len() > 1 {
            return Err(RelayError::Protocol("multiple Gemini candidates"));
        }
        for c in candidates {
            if c.get("index").is_some_and(|i| i.as_u64() != Some(0)) {
                return Err(RelayError::Protocol("invalid Gemini candidate index"));
            }
            if let Some(parts) = c["content"].get("parts") {
                for p in parts
                    .as_array()
                    .ok_or(RelayError::Protocol("invalid Gemini parts"))?
                {
                    let key = if let Some(s) = p.get("text") {
                        let thought = p.get("thought").and_then(Value::as_bool).unwrap_or(false);
                        let key = (u64::from(thought), 0);
                        self.simple(
                            key,
                            if thought {
                                BlockKind::Thinking
                            } else {
                                BlockKind::Text
                            },
                            text(s)?,
                            out,
                        )?;
                        key
                    } else if let Some(f) = p.get("functionCall") {
                        let key = (2, self.gemini_calls);
                        self.gemini_calls += 1;
                        let id = f["id"]
                            .as_str()
                            .map(str::to_owned)
                            .unwrap_or_else(|| format!("call_gemini_{}", key.1));
                        self.block(
                            key,
                            BlockKind::Tool {
                                id,
                                name: text(&f["name"])?.into(),
                            },
                            out,
                        )?;
                        if !f["args"].is_object() {
                            return Err(RelayError::Protocol("invalid Gemini function arguments"));
                        }
                        self.delta(key, &f["args"].to_string(), out)?;
                        key
                    } else {
                        return Err(RelayError::Protocol("unsupported Gemini part"));
                    };
                    if let Some(s) = p.get("thoughtSignature") {
                        self.signature(key, text(s)?, out)?;
                    }
                }
            }
            if let Some(r) = c.get("finishReason") {
                self.finish(finish_reason(self.protocol, text(r)?, self.tools())?, out)?;
            }
        }
        Ok(())
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    #[test]
    fn usage_is_not_zero_when_absent_and_cache_is_normalized() {
        assert_eq!(
            usage(UpstreamProtocol::Chat, &Value::Null).unwrap(),
            Usage::default()
        );
        let u = usage(UpstreamProtocol::Anthropic,&serde_json::json!({"input_tokens":10,"cache_read_input_tokens":20,"cache_creation_input_tokens":30,"output_tokens":4})).unwrap();
        assert_eq!(u.input_tokens, Some(60));
        assert_eq!(u.output_tokens, Some(4));
        let g = usage(UpstreamProtocol::Gemini,&serde_json::json!({"promptTokenCount":5,"candidatesTokenCount":7,"thoughtsTokenCount":3})).unwrap();
        assert_eq!(g.output_tokens, Some(10));
        assert_eq!(g.reasoning_output_tokens, Some(3));
    }
    #[test]
    fn usage_overflow_and_negative_counters_are_errors() {
        assert!(
            usage(
                UpstreamProtocol::Chat,
                &serde_json::json!({"prompt_tokens":-1})
            )
            .is_err()
        );
        assert!(
            usage(
                UpstreamProtocol::Anthropic,
                &serde_json::json!({"input_tokens":u64::MAX,"cache_read_input_tokens":1})
            )
            .is_err()
        );
    }
}
