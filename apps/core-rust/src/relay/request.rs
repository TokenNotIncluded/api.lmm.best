//! Bounded, text/function-call request conversion. Unsupported inputs fail closed.
use std::collections::{BTreeMap, BTreeSet};
use serde_json::{Map, Value, json};
use super::{ClientProtocol, Limits, RelayError, Route, UpstreamProtocol};

#[derive(Clone, Debug)]
pub enum InputBlock {
    Text(String), Refusal(String),
    Thinking { text: String, signature: Option<(UpstreamProtocol, String)> },
    ToolCall { id: String, name: String, arguments: String, signature: Option<(UpstreamProtocol, String)> },
    ToolResult { id: String, content: String },
}
#[derive(Clone, Copy, Debug, Eq, PartialEq)]
pub enum Role { System, Developer, User, Assistant, Tool }
impl Role {
    fn parse(v: &Value) -> Result<Self, RelayError> {
        match v.as_str() { Some("system") => Ok(Self::System), Some("developer") => Ok(Self::Developer),
            Some("user") => Ok(Self::User), Some("assistant") => Ok(Self::Assistant), Some("tool") => Ok(Self::Tool),
            _ => Err(RelayError::Invalid("unsupported message role")) }
    }
    fn name(self) -> &'static str {
        match self { Self::System => "system", Self::Developer => "developer", Self::User => "user", Self::Assistant => "assistant", Self::Tool => "tool" }
    }
}
#[derive(Clone, Debug)]
pub struct Message { pub role: Role, pub content: Vec<InputBlock> }
#[derive(Clone, Debug)]
pub struct FunctionTool { pub name: String, pub description: Option<String>, pub parameters: Value, pub strict: Option<bool> }
#[derive(Clone, Debug)]
pub enum ToolChoice { Auto, None, Required, Named(String) }
#[derive(Clone, Debug)]
pub struct Request {
    pub client: ClientProtocol, pub model: String, pub stream: bool,
    pub max_output_tokens: u32, pub messages: Vec<Message>, pub tools: Vec<FunctionTool>,
    pub temperature: Option<f64>, pub top_p: Option<f64>, pub stop: Vec<String>,
    pub tool_choice: ToolChoice, pub parallel_tool_calls: Option<bool>, pub reasoning_effort: Option<String>,
    pub(crate) include_usage: bool,
}

fn object(v: &Value) -> Result<&Map<String, Value>, RelayError> {
    v.as_object().ok_or(RelayError::Invalid("expected an object"))
}
fn keys(v: &Value, allowed: &[&str]) -> Result<(), RelayError> {
    if object(v)?.keys().any(|k| !allowed.contains(&k.as_str())) {
        return Err(RelayError::Unsupported("request contains an unsupported field"));
    }
    Ok(())
}
fn string(v: &Value) -> Result<String, RelayError> {
    v.as_str().map(str::to_owned).ok_or(RelayError::Invalid("expected a string"))
}
fn name(v: &Value) -> Result<String, RelayError> {
    let s = string(v)?;
    if s.is_empty() || s.len() > 128 || !s.bytes().all(|b| b.is_ascii_alphanumeric() || b"-_".contains(&b)) {
        return Err(RelayError::Invalid("invalid function name or call ID"));
    }
    Ok(s)
}
fn boolean(v: Option<&Value>) -> Result<Option<bool>, RelayError> {
    v.filter(|v| !v.is_null()).map(|v| v.as_bool().ok_or(RelayError::Invalid("expected a boolean"))).transpose()
}
fn number(v: Option<&Value>, max: f64) -> Result<Option<f64>, RelayError> {
    v.filter(|v| !v.is_null()).map(|v| {
        v.as_f64().filter(|n| n.is_finite() && *n >= 0.0 && *n <= max)
            .ok_or(RelayError::Invalid("invalid sampling value"))
    }).transpose()
}
fn signature(v: Option<&Value>) -> Result<Option<(UpstreamProtocol, String)>, RelayError> {
    let Some(v) = v.filter(|v| !v.is_null()) else { return Ok(None); };
    keys(v, &["protocol", "signature"])?;
    let protocol = match v["protocol"].as_str() {
        Some("anthropic") => UpstreamProtocol::Anthropic, Some("gemini") => UpstreamProtocol::Gemini,
        Some("chat") => UpstreamProtocol::Chat, Some("responses") => UpstreamProtocol::Responses,
        _ => return Err(RelayError::Invalid("invalid signature provider")),
    };
    Ok(Some((protocol, string(&v["signature"])?)))
}
fn arguments(v: &Value, limit: usize) -> Result<String, RelayError> {
    let text = string(v)?;
    if text.len() > limit { return Err(RelayError::Limit("tool arguments")); }
    let parsed: Value = serde_json::from_str(&text).map_err(|_| RelayError::Invalid("tool arguments must be JSON"))?;
    if !parsed.is_object() { return Err(RelayError::Invalid("tool arguments must be an object")); }
    Ok(text)
}
fn text_blocks(v: &Value, assistant: bool) -> Result<Vec<InputBlock>, RelayError> {
    if v.is_null() { return Ok(Vec::new()); }
    if let Some(s) = v.as_str() { return Ok(vec![InputBlock::Text(s.into())]); }
    let parts = v.as_array().ok_or(RelayError::Invalid("invalid message content"))?;
    let mut out = Vec::new();
    for p in parts {
        match p["type"].as_str() {
            Some("text" | "input_text" | "output_text") => {
                keys(p, &["type", "text", "annotations"])?;
                if p.get("annotations").is_some_and(|a| a.as_array().is_none_or(|a| !a.is_empty())) {
                    return Err(RelayError::Unsupported("annotated input content"));
                }
                out.push(InputBlock::Text(string(&p["text"])?));
            }
            Some("refusal") if assistant => { keys(p, &["type", "refusal"])?; out.push(InputBlock::Refusal(string(&p["refusal"])?)); }
            _ => return Err(RelayError::Unsupported("only text and function tools are supported")),
        }
    }
    Ok(out)
}

impl Request {
    pub fn parse(client: ClientProtocol, body: &[u8], limits: &Limits) -> Result<Self, RelayError> {
        limits.validate()?;
        if body.len() > limits.request_bytes { return Err(RelayError::Limit("request body")); }
        let v: Value = serde_json::from_slice(body).map_err(|_| RelayError::Invalid("invalid JSON body"))?;
        match client {
            ClientProtocol::Chat => keys(&v, &["model","messages","stream","stream_options","max_tokens","max_completion_tokens",
                "temperature","top_p","stop","tools","tool_choice","parallel_tool_calls","n","reasoning_effort","store"] )?,
            ClientProtocol::Responses => keys(&v, &["model","input","instructions","stream","max_output_tokens",
                "temperature","top_p","tools","tool_choice","parallel_tool_calls","reasoning","store"] )?,
        }
        let model = string(&v["model"])?;
        if model.is_empty() || model.len() > 256 { return Err(RelayError::Invalid("invalid model")); }
        if boolean(v.get("store"))? == Some(true) { return Err(RelayError::Unsupported("stored responses are not implemented")); }
        if v.get("n").is_some_and(|n| n.as_u64() != Some(1)) { return Err(RelayError::Unsupported("only one candidate is supported")); }
        let stream = boolean(v.get("stream"))?.unwrap_or(false);
        let include_usage = if let Some(opts) = v.get("stream_options").filter(|v| !v.is_null()) {
            keys(opts, &["include_usage"])?;
            boolean(opts.get("include_usage"))?.unwrap_or(false)
        } else { false };
        let token_values: Vec<&Value> = ["max_tokens", "max_completion_tokens", "max_output_tokens"].iter()
            .filter_map(|k| v.get(k)).filter(|v| !v.is_null()).collect();
        if token_values.len() > 1 { return Err(RelayError::Invalid("conflicting output limits")); }
        let max_output_tokens = if let Some(n) = token_values.first() {
            n.as_u64().and_then(|n| u32::try_from(n).ok()).filter(|n| *n > 0 && *n <= limits.max_output_tokens)
                .ok_or(RelayError::Invalid("invalid output token limit"))?
        } else { 4096.min(limits.max_output_tokens) };
        let mut stop = Vec::new();
        if let Some(s) = v.get("stop").filter(|s| !s.is_null()) {
            if let Some(s) = s.as_str() { stop.push(s.to_owned()); }
            else { for s in s.as_array().ok_or(RelayError::Invalid("invalid stop sequences"))? { stop.push(string(s)?); } }
        }
        if stop.len() > 4 || stop.iter().any(|s| s.is_empty() || s.len() > 1024) { return Err(RelayError::Invalid("invalid stop sequences")); }
        let mut tools = Vec::new();
        let mut tool_names = BTreeSet::new();
        if let Some(raw) = v.get("tools").filter(|v| !v.is_null()) {
            for t in raw.as_array().ok_or(RelayError::Invalid("invalid tools"))? {
                if t["type"] != "function" { return Err(RelayError::Unsupported("only function tools are supported")); }
                let f = match client {
                    ClientProtocol::Chat => { keys(t, &["type", "function"])?; &t["function"] },
                    ClientProtocol::Responses => t,
                };
                keys(f, &["type", "name", "description", "parameters", "strict"])?;
                let name = name(&f["name"])?;
                if !tool_names.insert(name.clone()) { return Err(RelayError::Invalid("duplicate function name")); }
                let parameters = f.get("parameters").cloned().unwrap_or_else(|| json!({"type":"object","properties":{}}));
                if !parameters.is_object() { return Err(RelayError::Invalid("function parameters must be an object")); }
                tools.push(FunctionTool { name, parameters, description: f.get("description").map(string).transpose()?, strict: boolean(f.get("strict"))? });
            }
        }
        if tools.len() > limits.max_blocks { return Err(RelayError::Limit("too many tools")); }
        let tool_choice = match v.get("tool_choice").filter(|v| !v.is_null()) {
            None => ToolChoice::Auto,
            Some(Value::String(s)) => match s.as_str() { "auto" => ToolChoice::Auto, "none" => ToolChoice::None,
                "required" => ToolChoice::Required, _ => return Err(RelayError::Unsupported("unsupported tool choice")) },
            Some(t) => {
                if t["type"] != "function" { return Err(RelayError::Unsupported("unsupported tool choice")); }
                let n = match client { ClientProtocol::Chat => { keys(t, &["type","function"])?; keys(&t["function"], &["name"])?; &t["function"]["name"] },
                    ClientProtocol::Responses => { keys(t, &["type","name"])?; &t["name"] } };
                let n = name(n)?;
                if !tool_names.contains(&n) { return Err(RelayError::Invalid("tool choice names an unknown function")); }
                ToolChoice::Named(n)
            }
        };
        if tools.is_empty() && matches!(tool_choice, ToolChoice::Required | ToolChoice::Named(_)) { return Err(RelayError::Invalid("tool choice requires tools")); }
        let reasoning_effort = match client {
            ClientProtocol::Chat => v.get("reasoning_effort").filter(|v| !v.is_null()).map(string).transpose()?,
            ClientProtocol::Responses => if let Some(r) = v.get("reasoning").filter(|v| !v.is_null()) {
                keys(r, &["effort"])?; r.get("effort").map(string).transpose()?
            } else { None },
        };
        if reasoning_effort.as_ref().is_some_and(|s| !["low","medium","high"].contains(&s.as_str())) {
            return Err(RelayError::Unsupported("unsupported reasoning effort"));
        }
        let mut messages = Vec::new();
        if let Some(s) = v.get("instructions").filter(|v| !v.is_null()) { messages.push(Message { role: Role::System, content: vec![InputBlock::Text(string(s)?)] }); }
        match client {
            ClientProtocol::Chat => {
                for m in v["messages"].as_array().ok_or(RelayError::Invalid("messages must be an array"))? {
                    keys(m, &["role","content","tool_calls","tool_call_id","reasoning_content","provider_metadata","refusal"])?;
                    let role = Role::parse(&m["role"])?;
                    let mut content = Vec::new();
                    if role == Role::Tool {
                        content.push(InputBlock::ToolResult { id: name(&m["tool_call_id"] )?, content: string(&m["content"])? });
                    } else {
                        if let Some(t) = m.get("reasoning_content").filter(|v| !v.is_null()) {
                            if role != Role::Assistant { return Err(RelayError::Invalid("reasoning requires assistant role")); }
                            content.push(InputBlock::Thinking { text: string(t)?, signature: signature(m.get("provider_metadata"))? });
                        } else if m.get("provider_metadata").is_some() { return Err(RelayError::Invalid("signature without reasoning content")); }
                        content.extend(text_blocks(&m["content"], role == Role::Assistant)?);
                        if let Some(t) = m.get("refusal").filter(|v| !v.is_null()) {
                            if role != Role::Assistant { return Err(RelayError::Invalid("refusal requires assistant role")); }
                            content.push(InputBlock::Refusal(string(t)?));
                        }
                    }
                    if let Some(calls) = m.get("tool_calls").filter(|v| !v.is_null()) {
                        if role != Role::Assistant { return Err(RelayError::Invalid("tool calls require assistant role")); }
                        for c in calls.as_array().ok_or(RelayError::Invalid("invalid tool calls"))? {
                            keys(c, &["id","type","function","provider_metadata"])?;
                            keys(&c["function"], &["name","arguments"])?;
                            if c["type"] != "function" { return Err(RelayError::Unsupported("unsupported tool call")); }
                            content.push(InputBlock::ToolCall { id: name(&c["id"] )?, name: name(&c["function"]["name"] )?,
                                arguments: arguments(&c["function"]["arguments"], limits.tool_argument_bytes)?, signature: signature(c.get("provider_metadata"))? });
                        }
                    }
                    messages.push(Message { role, content });
                }
            }
            ClientProtocol::Responses => {
                let input = &v["input"];
                if let Some(s) = input.as_str() { messages.push(Message { role: Role::User, content: vec![InputBlock::Text(s.into())] }); }
                else {
                    for i in input.as_array().ok_or(RelayError::Invalid("input must be a string or array"))? {
                        match i.get("type").and_then(Value::as_str).unwrap_or("message") {
                            "message" => { keys(i, &["type","role","content"])?;
                                let role = Role::parse(&i["role"])?;
                                if role == Role::Tool { return Err(RelayError::Invalid("use function_call_output")); }
                                messages.push(Message { role, content: text_blocks(&i["content"], role == Role::Assistant)? });
                            }
                            "function_call" => { keys(i, &["type","call_id","name","arguments","provider_metadata"])?;
                                messages.push(Message { role: Role::Assistant, content: vec![InputBlock::ToolCall {
                                    id: name(&i["call_id"] )?, name: name(&i["name"] )?, arguments: arguments(&i["arguments"], limits.tool_argument_bytes)?,
                                    signature: signature(i.get("provider_metadata"))? }] });
                            }
                            "function_call_output" => { keys(i, &["type","call_id","output"])?;
                                messages.push(Message { role: Role::Tool, content: vec![InputBlock::ToolResult { id: name(&i["call_id"] )?, content: string(&i["output"])? }] });
                            }
                            _ => return Err(RelayError::Unsupported("stateful, encrypted or non-text Responses input")),
                        }
                    }
                }
            }
        }
        if messages.is_empty() || messages.len() > 1024 { return Err(RelayError::Limit("message count")); }
        let mut calls = BTreeSet::new();
        let mut results = BTreeSet::new();
        let mut count = 0usize;
        for m in &messages {
            for b in &m.content {
                count += 1;
                match b {
                    InputBlock::ToolCall { id, .. } if !calls.insert(id) => return Err(RelayError::Invalid("duplicate call ID")),
                    InputBlock::ToolResult { id, .. } if !calls.contains(id) || !results.insert(id) => return Err(RelayError::Invalid("orphan or duplicate tool result")),
                    _ => {},
                }
            }
        }
        if count > 4096 { return Err(RelayError::Limit("input block count")); }
        Ok(Self { client, model, stream, max_output_tokens, messages, tools, stop, tool_choice, include_usage,
            temperature: number(v.get("temperature"), 2.0)?, top_p: number(v.get("top_p"), 1.0)?,
            parallel_tool_calls: boolean(v.get("parallel_tool_calls"))?, reasoning_effort })
    }

    pub(crate) fn upstream(&self, route: &Route) -> Result<Value, RelayError> {
        for m in &self.messages { for b in &m.content {
            let sig = match b { InputBlock::Thinking { signature, .. } | InputBlock::ToolCall { signature, .. } => signature, _ => continue };
            if sig.as_ref().is_some_and(|(p, _)| *p != route.protocol) {
                return Err(RelayError::Unsupported("provider signatures cannot be replayed to a different protocol"));
            }
        } }
        if matches!(route.protocol, UpstreamProtocol::Anthropic | UpstreamProtocol::Gemini)
            && (self.reasoning_effort.is_some() || self.tools.iter().any(|t| t.strict == Some(true))) {
            return Err(RelayError::Unsupported("reasoning effort or strict tools require an OpenAI protocol route"));
        }
        match route.protocol { UpstreamProtocol::Chat => self.chat(route), UpstreamProtocol::Responses => self.responses(route),
            UpstreamProtocol::Anthropic => self.anthropic(route), UpstreamProtocol::Gemini => self.gemini() }
    }
    fn openai_tools(&self, chat: bool) -> Vec<Value> {
        self.tools.iter().map(|t| {
            let mut v = json!({"name":t.name,"parameters":t.parameters});
            if let Some(d) = &t.description { v["description"] = json!(d); }
            if let Some(s) = t.strict { v["strict"] = json!(s); }
            if chat { json!({"type":"function","function":v}) } else { v["type"] = json!("function"); v }
        }).collect()
    }
    fn openai_choice(&self, chat: bool) -> Value {
        match &self.tool_choice { ToolChoice::Auto => json!("auto"), ToolChoice::None => json!("none"), ToolChoice::Required => json!("required"),
            ToolChoice::Named(n) => if chat { json!({"type":"function","function":{"name":n}}) } else { json!({"type":"function","name":n}) } }
    }
    fn sampling(&self, v: &mut Value) {
        if let Some(n) = self.temperature { v["temperature"] = json!(n); }
        if let Some(n) = self.top_p { v["top_p"] = json!(n); }
    }
    fn chat(&self, route: &Route) -> Result<Value, RelayError> {
        let mut messages = Vec::new();
        for m in &self.messages {
            let mut v = json!({"role":m.role.name(),"content":Value::Null});
            let mut text = String::new(); let mut thoughts = String::new(); let mut refusals = String::new(); let mut calls = Vec::new();
            for b in &m.content { match b {
                InputBlock::Text(s) => text.push_str(s), InputBlock::Refusal(s) => refusals.push_str(s),
                InputBlock::Thinking { text, signature } => { thoughts.push_str(text); if signature.is_some() { return Err(RelayError::Unsupported("opaque Chat reasoning signatures")); } },
                InputBlock::ToolCall { id, name, arguments, signature } => {
                    if signature.is_some() { return Err(RelayError::Unsupported("opaque Chat tool signatures")); }
                    calls.push(json!({"id":id,"type":"function","function":{"name":name,"arguments":arguments}}));
                }
                InputBlock::ToolResult { id, content } => { v["tool_call_id"] = json!(id); text.push_str(content); }
            } }
            if !text.is_empty() || m.role != Role::Assistant { v["content"] = json!(text); }
            if !thoughts.is_empty() { v["reasoning_content"] = json!(thoughts); }
            if !refusals.is_empty() { v["refusal"] = json!(refusals); }
            if !calls.is_empty() { v["tool_calls"] = json!(calls); }
            messages.push(v);
        }
        let mut v = json!({"model":route.model,"messages":messages,"stream":self.stream,"max_completion_tokens":self.max_output_tokens,"store":false});
        if self.stream { v["stream_options"] = json!({"include_usage":true}); }
        if !self.tools.is_empty() { v["tools"] = json!(self.openai_tools(true)); v["tool_choice"] = self.openai_choice(true); }
        if let Some(p) = self.parallel_tool_calls { v["parallel_tool_calls"] = json!(p); }
        if let Some(e) = &self.reasoning_effort { v["reasoning_effort"] = json!(e); }
        if !self.stop.is_empty() { v["stop"] = json!(self.stop); }
        self.sampling(&mut v); Ok(v)
    }
    fn responses(&self, route: &Route) -> Result<Value, RelayError> {
        if !self.stop.is_empty() { return Err(RelayError::Unsupported("Responses stop sequences")); }
        let mut input = Vec::new();
        for m in &self.messages { for b in &m.content { match b {
            InputBlock::Text(s) => input.push(json!({"role":m.role.name(),"content":[{"type":if m.role == Role::Assistant {"output_text"} else {"input_text"},"text":s}]})),
            InputBlock::Refusal(s) => input.push(json!({"role":"assistant","content":[{"type":"refusal","refusal":s}]})),
            InputBlock::Thinking { .. } => return Err(RelayError::Unsupported("Responses reasoning history requires native item identity")),
            InputBlock::ToolCall { id, name, arguments, signature } => {
                if signature.is_some() { return Err(RelayError::Unsupported("opaque Responses tool signatures")); }
                input.push(json!({"type":"function_call","call_id":id,"name":name,"arguments":arguments}));
            }
            InputBlock::ToolResult { id, content } => input.push(json!({"type":"function_call_output","call_id":id,"output":content})),
        } } }
        let mut v = json!({"model":route.model,"input":input,"stream":self.stream,"max_output_tokens":self.max_output_tokens,"store":false});
        if !self.tools.is_empty() { v["tools"] = json!(self.openai_tools(false)); v["tool_choice"] = self.openai_choice(false); }
        if let Some(p) = self.parallel_tool_calls { v["parallel_tool_calls"] = json!(p); }
        if let Some(e) = &self.reasoning_effort { v["reasoning"] = json!({"effort":e}); }
        self.sampling(&mut v); Ok(v)
    }
    fn split_system(&self) -> Result<(Vec<Value>, Vec<&Message>), RelayError> {
        let mut system = Vec::new(); let mut messages = Vec::new();
        for m in &self.messages {
            if matches!(m.role, Role::System | Role::Developer) {
                if !messages.is_empty() { return Err(RelayError::Unsupported("late system instructions cannot be reordered")); }
                for b in &m.content { match b { InputBlock::Text(s) => system.push(json!({"type":"text","text":s})),
                    _ => return Err(RelayError::Unsupported("non-text system instruction")) } }
            } else { messages.push(m); }
        }
        Ok((system, messages))
    }
    fn anthropic(&self, route: &Route) -> Result<Value, RelayError> {
        if self.temperature.is_some_and(|t| t > 1.0) { return Err(RelayError::Unsupported("Anthropic temperature exceeds one")); }
        let (system, source) = self.split_system()?;
        let mut messages = Vec::new();
        for m in source {
            let mut content = Vec::new();
            for b in &m.content { content.push(match b {
                InputBlock::Text(s) | InputBlock::Refusal(s) => json!({"type":"text","text":s}),
                InputBlock::Thinking { text, signature: Some((UpstreamProtocol::Anthropic, s)) } => json!({"type":"thinking","thinking":text,"signature":s}),
                InputBlock::Thinking { .. } => return Err(RelayError::Unsupported("Anthropic thinking history needs its original signature")),
                InputBlock::ToolCall { id, name, arguments, .. } => json!({"type":"tool_use","id":id,"name":name,"input":serde_json::from_str::<Value>(arguments).map_err(|_| RelayError::Invalid("invalid tool JSON"))?}),
                InputBlock::ToolResult { id, content } => json!({"type":"tool_result","tool_use_id":id,"content":content}),
            }); }
            messages.push(json!({"role":if m.role == Role::Assistant {"assistant"} else {"user"},"content":content}));
        }
        let mut v = json!({"model":route.model,"system":system,"messages":messages,"stream":self.stream,"max_tokens":self.max_output_tokens});
        if !self.tools.is_empty() {
            v["tools"] = json!(self.tools.iter().map(|t| { let mut v = json!({"name":t.name,"input_schema":t.parameters});
                if let Some(d) = &t.description { v["description"] = json!(d); } v }).collect::<Vec<_>>());
            v["tool_choice"] = match &self.tool_choice { ToolChoice::Auto => json!({"type":"auto"}), ToolChoice::None => json!({"type":"none"}),
                ToolChoice::Required => json!({"type":"any"}), ToolChoice::Named(n) => json!({"type":"tool","name":n}) };
            if let Some(p) = self.parallel_tool_calls { v["tool_choice"]["disable_parallel_tool_use"] = json!(!p); }
        }
        if !self.stop.is_empty() { v["stop_sequences"] = json!(self.stop); }
        self.sampling(&mut v); Ok(v)
    }
    fn gemini(&self) -> Result<Value, RelayError> {
        if self.parallel_tool_calls == Some(false) { return Err(RelayError::Unsupported("Gemini cannot enforce serial tool calls")); }
        let (system, source) = self.split_system()?;
        let mut calls = BTreeMap::new(); let mut contents = Vec::new();
        for m in source {
            let mut parts = Vec::new();
            for b in &m.content { parts.push(match b {
                InputBlock::Text(s) | InputBlock::Refusal(s) => json!({"text":s}),
                InputBlock::Thinking { text, signature: Some((UpstreamProtocol::Gemini, s)) } => json!({"text":text,"thought":true,"thoughtSignature":s}),
                InputBlock::Thinking { .. } => return Err(RelayError::Unsupported("Gemini thinking history needs its original signature")),
                InputBlock::ToolCall { id, name, arguments, signature } => {
                    calls.insert(id, name);
                    let mut p = json!({"functionCall":{"id":id,"name":name,"args":serde_json::from_str::<Value>(arguments).map_err(|_| RelayError::Invalid("invalid tool JSON"))?}});
                    if let Some((_, s)) = signature { p["thoughtSignature"] = json!(s); } p
                }
                InputBlock::ToolResult { id, content } => {
                    let name = calls.get(id).ok_or(RelayError::Invalid("Gemini tool result requires its original call"))?;
                    let result = serde_json::from_str::<Value>(content).ok().filter(Value::is_object).unwrap_or_else(|| json!({"output":content}));
                    json!({"functionResponse":{"id":id,"name":name,"response":result}})
                }
            }); }
            contents.push(json!({"role":if m.role == Role::Assistant {"model"} else {"user"},"parts":parts}));
        }
        let mut generation = json!({"maxOutputTokens":self.max_output_tokens,"candidateCount":1});
        if let Some(t) = self.temperature { generation["temperature"] = json!(t); }
        if let Some(t) = self.top_p { generation["topP"] = json!(t); }
        if !self.stop.is_empty() { generation["stopSequences"] = json!(self.stop); }
        let mut v = json!({"contents":contents,"generationConfig":generation});
        if !system.is_empty() { v["systemInstruction"] = json!({"parts":system.iter().map(|v| json!({"text":v["text"]})).collect::<Vec<_>>()}); }
        if !self.tools.is_empty() {
            v["tools"] = json!([{"functionDeclarations":self.tools.iter().map(|t| { let mut v = json!({"name":t.name,"parametersJsonSchema":t.parameters});
                if let Some(d) = &t.description { v["description"] = json!(d); } v }).collect::<Vec<_>>()}]);
            v["toolConfig"] = match &self.tool_choice { ToolChoice::Auto => json!({"functionCallingConfig":{"mode":"AUTO"}}),
                ToolChoice::None => json!({"functionCallingConfig":{"mode":"NONE"}}), ToolChoice::Required => json!({"functionCallingConfig":{"mode":"ANY"}}),
                ToolChoice::Named(n) => json!({"functionCallingConfig":{"mode":"ANY","allowedFunctionNames":[n]}}) };
        }
        Ok(v)
    }
}
