//! OpenAI client encoders. Successful terminal events are withheld until settlement.
use super::{
    BlockKind, ClientProtocol, Event, FinishReason, Limits, RelayError, Request, UpstreamProtocol,
    Usage,
};
use serde_json::{Value, json};

struct Block {
    kind: BlockKind,
    text: String,
    signature: Option<(UpstreamProtocol, String)>,
    ended: bool,
    tool_index: usize,
}
pub(crate) struct Encoder {
    client: ClientProtocol,
    stream: bool,
    include_usage: bool,
    id: String,
    model: String,
    created: u64,
    sequence: u64,
    started: bool,
    done: bool,
    blocks: Vec<Block>,
    usage: Usage,
    finish: Option<FinishReason>,
    bytes: usize,
    limits: Limits,
}
impl Encoder {
    pub fn new(request: &Request, request_id: &str, limits: &Limits) -> Self {
        let prefix = match request.client {
            ClientProtocol::Chat => "chatcmpl",
            ClientProtocol::Responses => "resp",
        };
        Self {
            client: request.client,
            stream: request.stream,
            include_usage: request.include_usage,
            id: format!("{prefix}_{request_id}"),
            model: request.model.clone(),
            created: std::time::SystemTime::now()
                .duration_since(std::time::UNIX_EPOCH)
                .unwrap_or_default()
                .as_secs(),
            sequence: 0,
            started: false,
            done: false,
            blocks: Vec::new(),
            usage: Usage::default(),
            finish: None,
            bytes: 0,
            limits: limits.clone(),
        }
    }
    pub fn usage(&self) -> Usage {
        self.usage
    }
    pub fn finish_reason(&self) -> Option<FinishReason> {
        self.finish
    }
    fn account(&mut self, bytes: usize) -> Result<(), RelayError> {
        self.bytes = self
            .bytes
            .checked_add(bytes)
            .ok_or(RelayError::Limit("aggregate output"))?;
        if self.bytes > self.limits.output_bytes {
            return Err(RelayError::Limit("aggregate output"));
        }
        Ok(())
    }
    fn serialize(&self, value: &Value) -> Result<Vec<u8>, RelayError> {
        let data = serde_json::to_vec(value)
            .map_err(|_| RelayError::Protocol("response encoding failed"))?;
        // JSON may escape each input byte into six bytes. Structure overhead is
        // bounded separately by max_blocks. This is not a second unbounded cache.
        let max = self
            .limits
            .output_bytes
            .saturating_mul(8)
            .saturating_add(self.limits.max_blocks.saturating_mul(4096));
        if data.len() > max {
            return Err(RelayError::Limit("encoded response"));
        }
        Ok(data)
    }
    fn packet(&self, event: Option<&str>, value: &Value) -> Result<Vec<u8>, RelayError> {
        let mut out = Vec::new();
        if let Some(name) = event {
            out.extend_from_slice(b"event: ");
            out.extend_from_slice(name.as_bytes());
            out.push(b'\n');
        }
        out.extend_from_slice(b"data: ");
        out.extend(self.serialize(value)?);
        out.extend_from_slice(b"\n\n");
        Ok(out)
    }
    fn response_event(&mut self, kind: &str, mut v: Value) -> Result<Vec<u8>, RelayError> {
        v["type"] = json!(kind);
        v["sequence_number"] = json!(self.sequence);
        self.sequence = self
            .sequence
            .checked_add(1)
            .ok_or(RelayError::Limit("event sequence"))?;
        self.packet(Some(kind), &v)
    }
    fn chat_chunk(
        &self,
        delta: Value,
        finish: Option<FinishReason>,
    ) -> Result<Vec<u8>, RelayError> {
        self.packet(None,&json!({"id":self.id,"object":"chat.completion.chunk","created":self.created,"model":self.model,
            "choices":[{"index":0,"delta":delta,"finish_reason":finish.map(FinishReason::chat)}]}))
    }
    fn block_id(&self, index: usize) -> String {
        let prefix = match self.blocks[index].kind {
            BlockKind::Tool { .. } => "fc",
            BlockKind::Thinking => "rs",
            _ => "msg",
        };
        format!("{prefix}_{}_{}", self.id, index)
    }
    fn part(&self, index: usize, empty: bool) -> Value {
        let b = &self.blocks[index];
        let text = if empty { "" } else { &b.text };
        match b.kind {
            BlockKind::Text => json!({"type":"output_text","text":text,"annotations":[]}),
            BlockKind::Refusal => json!({"type":"refusal","refusal":text}),
            BlockKind::Thinking => json!({"type":"summary_text","text":text}),
            BlockKind::Tool { .. } => Value::Null,
        }
    }
    fn item(&self, index: usize, empty: bool) -> Value {
        let b = &self.blocks[index];
        let text = if empty { "" } else { &b.text };
        let status = if empty {
            "in_progress"
        } else if matches!(
            self.finish,
            Some(FinishReason::Length | FinishReason::ContentFilter)
        ) {
            "incomplete"
        } else {
            "completed"
        };
        let mut item = match &b.kind {
            BlockKind::Tool { id, name } => {
                json!({"id":self.block_id(index),"type":"function_call","call_id":id,"name":name,"arguments":text,"status":status})
            }
            BlockKind::Thinking => {
                json!({"id":self.block_id(index),"type":"reasoning","summary":if empty {vec![]} else {vec![self.part(index,false)]}})
            }
            _ => {
                json!({"id":self.block_id(index),"type":"message","role":"assistant","status":status,
                "content":if empty {vec![]} else {vec![self.part(index,false)]}})
            }
        };
        if !empty {
            if let Some((p, s)) = &b.signature {
                item["provider_metadata"] = json!({"protocol":p,"signature":s});
            }
        }
        item
    }
    fn response(&self, initial: bool) -> Value {
        let incomplete = !initial
            && matches!(
                self.finish,
                Some(FinishReason::Length | FinishReason::ContentFilter)
            );
        let status = if initial {
            "in_progress"
        } else if incomplete {
            "incomplete"
        } else {
            "completed"
        };
        json!({"id":self.id,"object":"response","created_at":self.created,"status":status,"model":self.model,
            "error":Value::Null,"incomplete_details":if incomplete {json!({"reason":if self.finish == Some(FinishReason::Length) {"max_output_tokens"} else {"content_filter"}})} else {Value::Null},
            "output":if initial {vec![]} else {(0..self.blocks.len()).map(|i|self.item(i,false)).collect::<Vec<_>>()},
            "usage":if initial {Value::Null} else {self.usage.wire(ClientProtocol::Responses)},"store":false})
    }
    pub fn consume(&mut self, event: Event) -> Result<Vec<Vec<u8>>, RelayError> {
        let mut out = Vec::new();
        match event {
            Event::Start(source_id) => {
                if self.started {
                    return Err(RelayError::Protocol("duplicate response start"));
                }
                self.account(source_id.len())?;
                self.started = true;
                if self.stream {
                    match self.client {
                        ClientProtocol::Chat => out
                            .push(self.chat_chunk(json!({"role":"assistant","content":""}), None)?),
                        ClientProtocol::Responses => {
                            out.push(self.response_event(
                                "response.created",
                                json!({"response":self.response(true)}),
                            )?);
                            out.push(self.response_event(
                                "response.in_progress",
                                json!({"response":self.response(true)}),
                            )?);
                        }
                    }
                }
            }
            Event::Block { index, kind } => {
                if !self.started || index != self.blocks.len() || index >= self.limits.max_blocks {
                    return Err(RelayError::Protocol("invalid output block index"));
                }
                let tool_index = self
                    .blocks
                    .iter()
                    .filter(|b| matches!(b.kind, BlockKind::Tool { .. }))
                    .count();
                if let BlockKind::Tool { id, name } = &kind {
                    self.account(id.len() + name.len())?;
                }
                self.blocks.push(Block {
                    kind,
                    text: String::new(),
                    signature: None,
                    ended: false,
                    tool_index,
                });
                if self.stream {
                    match self.client {
                        ClientProtocol::Chat => {
                            if let BlockKind::Tool { id, name } = &self.blocks[index].kind {
                                out.push(self.chat_chunk(json!({"tool_calls":[{"index":tool_index,"id":id,"type":"function","function":{"name":name,"arguments":""}}]}),None)?);
                            }
                        }
                        ClientProtocol::Responses => {
                            out.push(self.response_event(
                                "response.output_item.added",
                                json!({"output_index":index,"item":self.item(index,true)}),
                            )?);
                            match self.blocks[index].kind {
                            BlockKind::Text | BlockKind::Refusal => out.push(self.response_event("response.content_part.added",
                                json!({"output_index":index,"item_id":self.block_id(index),"content_index":0,"part":self.part(index,true)}))?),
                            BlockKind::Thinking => out.push(self.response_event("response.reasoning_summary_part.added",
                                json!({"output_index":index,"item_id":self.block_id(index),"summary_index":0,"part":self.part(index,true)}))?),
                            BlockKind::Tool { .. } => {},
                        }
                        }
                    }
                }
            }
            Event::Delta { index, text } => {
                self.account(text.len())?;
                let b = self
                    .blocks
                    .get_mut(index)
                    .ok_or(RelayError::Protocol("unknown output block"))?;
                if b.ended {
                    return Err(RelayError::Protocol("output after block end"));
                }
                if matches!(b.kind, BlockKind::Tool { .. })
                    && b.text.len().saturating_add(text.len()) > self.limits.tool_argument_bytes
                {
                    return Err(RelayError::Limit("tool arguments"));
                }
                b.text.push_str(&text);
                if self.stream {
                    match self.client {
                        ClientProtocol::Chat => {
                            let delta = match b.kind {
                                BlockKind::Text => json!({"content":text}),
                                BlockKind::Refusal => json!({"refusal":text}),
                                BlockKind::Thinking => json!({"reasoning_content":text}),
                                BlockKind::Tool { .. } => {
                                    json!({"tool_calls":[{"index":b.tool_index,"function":{"arguments":text}}]})
                                }
                            };
                            out.push(self.chat_chunk(delta, None)?);
                        }
                        ClientProtocol::Responses => {
                            let mut v = json!({"output_index":index,"item_id":self.block_id(index),"delta":text});
                            let kind = match self.blocks[index].kind {
                                BlockKind::Text => {
                                    v["content_index"] = json!(0);
                                    "response.output_text.delta"
                                }
                                BlockKind::Refusal => {
                                    v["content_index"] = json!(0);
                                    "response.refusal.delta"
                                }
                                BlockKind::Thinking => {
                                    v["summary_index"] = json!(0);
                                    "response.reasoning_summary_text.delta"
                                }
                                BlockKind::Tool { .. } => "response.function_call_arguments.delta",
                            };
                            out.push(self.response_event(kind, v)?);
                        }
                    }
                }
            }
            Event::Signature {
                index,
                provider,
                value,
            } => {
                self.account(value.len())?;
                let b = self
                    .blocks
                    .get_mut(index)
                    .ok_or(RelayError::Protocol("unknown signature block"))?;
                if b.ended {
                    return Err(RelayError::Protocol("signature after output end"));
                }
                match &mut b.signature {
                    Some((p, s)) if *p == provider && provider == UpstreamProtocol::Anthropic => {
                        s.push_str(&value)
                    }
                    Some((p, s)) if *p == provider && *s == value => {}
                    None => b.signature = Some((provider, value.clone())),
                    _ => return Err(RelayError::Protocol("conflicting provider signature")),
                }
                if self.stream && self.client == ClientProtocol::Chat {
                    let delta = if matches!(b.kind, BlockKind::Tool { .. }) {
                        json!({"tool_calls":[{"index":b.tool_index,"provider_metadata":{"protocol":provider,"signature":value}}]})
                    } else {
                        json!({"reasoning_details":[{"index":index,"type":"signature_delta","protocol":provider,"signature":value}]})
                    };
                    out.push(self.chat_chunk(delta, None)?);
                }
            }
            Event::EndBlock(i) => {
                let b = self
                    .blocks
                    .get_mut(i)
                    .ok_or(RelayError::Protocol("unknown output end"))?;
                b.ended = true;
            }
            Event::Usage(u) => self.usage.merge(u)?,
            Event::Finish(f) => {
                if self.finish.replace(f).is_some() {
                    return Err(RelayError::Protocol("duplicate finish reason"));
                }
            }
            Event::Done => {
                if self.finish.is_none() || self.done {
                    return Err(RelayError::Truncated);
                }
                self.done = true;
            }
        }
        Ok(out)
    }
    fn validate_complete(&self) -> Result<(), RelayError> {
        if !self.done
            || !self.started
            || self.finish.is_none()
            || self.blocks.iter().any(|b| !b.ended)
        {
            return Err(RelayError::Truncated);
        }
        if self.finish == Some(FinishReason::ToolCalls)
            && !self
                .blocks
                .iter()
                .any(|b| matches!(b.kind, BlockKind::Tool { .. }))
        {
            return Err(RelayError::Protocol("tool finish without tools"));
        }
        if matches!(
            self.finish,
            Some(FinishReason::Stop | FinishReason::ToolCalls)
        ) {
            for b in &self.blocks {
                if matches!(b.kind, BlockKind::Tool { .. }) {
                    let v: Value = serde_json::from_str(&b.text)
                        .map_err(|_| RelayError::Protocol("incomplete tool arguments"))?;
                    if !v.is_object() {
                        return Err(RelayError::Protocol("tool arguments are not an object"));
                    }
                }
            }
        }
        Ok(())
    }
    fn chat_response(&self) -> Value {
        let mut content = String::new();
        let mut reasoning = String::new();
        let mut refusal = String::new();
        let mut calls = Vec::new();
        let mut details = Vec::new();
        for (i, b) in self.blocks.iter().enumerate() {
            match &b.kind {
                BlockKind::Text => content.push_str(&b.text),
                BlockKind::Refusal => refusal.push_str(&b.text),
                BlockKind::Thinking => {
                    reasoning.push_str(&b.text);
                    if let Some((p, s)) = &b.signature {
                        details.push(json!({"index":i,"text":b.text,"protocol":p,"signature":s}));
                    }
                }
                BlockKind::Tool { id, name } => {
                    let mut c = json!({"id":id,"type":"function","function":{"name":name,"arguments":b.text}});
                    if let Some((p, s)) = &b.signature {
                        c["provider_metadata"] = json!({"protocol":p,"signature":s});
                    }
                    calls.push(c);
                }
            }
        }
        let mut message = json!({"role":"assistant","content":if content.is_empty() {Value::Null} else {json!(content)}});
        if !reasoning.is_empty() {
            message["reasoning_content"] = json!(reasoning);
        }
        if !refusal.is_empty() {
            message["refusal"] = json!(refusal);
        }
        if !calls.is_empty() {
            message["tool_calls"] = json!(calls);
        }
        if details.len() == 1 {
            message["provider_metadata"] =
                json!({"protocol":details[0]["protocol"],"signature":details[0]["signature"]});
        } else if !details.is_empty() {
            message["reasoning_details"] = json!(details);
        }
        json!({"id":self.id,"object":"chat.completion","created":self.created,"model":self.model,
            "choices":[{"index":0,"message":message,"finish_reason":self.finish.map(FinishReason::chat)}],"usage":self.usage.wire(ClientProtocol::Chat)})
    }
    /// Build (but do not send) a terminal response. Validation must happen before
    /// the billing hook. A length-limited tool may contain partial JSON.
    pub fn terminal(&mut self) -> Result<Vec<Vec<u8>>, RelayError> {
        self.validate_complete()?;
        if !self.stream {
            return Ok(vec![self.serialize(&match self.client {
                ClientProtocol::Chat => self.chat_response(),
                ClientProtocol::Responses => self.response(false),
            })?]);
        }
        let mut out = Vec::new();
        match self.client {
            ClientProtocol::Chat => {
                out.push(self.chat_chunk(json!({}), self.finish)?);
                if self.include_usage {
                    out.push(self.packet(None,&json!({"id":self.id,"object":"chat.completion.chunk","created":self.created,
                    "model":self.model,"choices":[],"usage":self.usage.wire(ClientProtocol::Chat)}))?);
                }
                out.push(b"data: [DONE]\n\n".to_vec());
            }
            ClientProtocol::Responses => {
                for i in 0..self.blocks.len() {
                    let mut v = json!({"output_index":i,"item_id":self.block_id(i)});
                    match &self.blocks[i].kind {
                        BlockKind::Text | BlockKind::Refusal => {
                            let refusal = matches!(self.blocks[i].kind, BlockKind::Refusal);
                            v["content_index"] = json!(0);
                            v[if refusal { "refusal" } else { "text" }] =
                                json!(self.blocks[i].text);
                            out.push(self.response_event(
                                if refusal {
                                    "response.refusal.done"
                                } else {
                                    "response.output_text.done"
                                },
                                v.clone(),
                            )?);
                            v.as_object_mut()
                                .ok_or(RelayError::Protocol("response encoding failed"))?
                                .remove(if refusal { "refusal" } else { "text" });
                            v["part"] = self.part(i, false);
                            out.push(self.response_event("response.content_part.done", v)?);
                        }
                        BlockKind::Thinking => {
                            v["summary_index"] = json!(0);
                            v["text"] = json!(self.blocks[i].text);
                            out.push(self.response_event(
                                "response.reasoning_summary_text.done",
                                v.clone(),
                            )?);
                            v.as_object_mut()
                                .ok_or(RelayError::Protocol("response encoding failed"))?
                                .remove("text");
                            v["part"] = self.part(i, false);
                            out.push(
                                self.response_event("response.reasoning_summary_part.done", v)?,
                            );
                        }
                        BlockKind::Tool { .. } => {
                            v["arguments"] = json!(self.blocks[i].text);
                            out.push(
                                self.response_event("response.function_call_arguments.done", v)?,
                            );
                        }
                    }
                    out.push(self.response_event(
                        "response.output_item.done",
                        json!({"output_index":i,"item":self.item(i,false)}),
                    )?);
                }
                let kind = if matches!(
                    self.finish,
                    Some(FinishReason::Length | FinishReason::ContentFilter)
                ) {
                    "response.incomplete"
                } else {
                    "response.completed"
                };
                out.push(self.response_event(kind, json!({"response":self.response(false)}))?);
            }
        }
        Ok(out)
    }
    pub fn error_packet(&mut self, error: &RelayError) -> Result<Vec<u8>, RelayError> {
        match self.client {
            ClientProtocol::Chat => self.packet(None, &error.json()),
            ClientProtocol::Responses => self.response_event(
                "error",
                json!({"code":error.code(),"message":error.to_string(),"param":Value::Null}),
            ),
        }
    }
}
