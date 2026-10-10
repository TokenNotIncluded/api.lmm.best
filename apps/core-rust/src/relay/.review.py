# One-shot reviewed source edits. CI removes this file before committing its result.
from pathlib import Path

ROOT = Path('apps/core-rust/src/relay')

def edit(name, old, new):
    path = ROOT / name
    source = path.read_text()
    assert source.count(old) == 1, (name, old[:80], source.count(old))
    path.write_text(source.replace(old, new, 1))

edit('mod.rs', 'mod types;\n', 'mod types;\n#[cfg(test)]\nmod tests;\n')
edit('mod.rs',
    '                let _ = timeout(Duration::from_millis(100), self.sender.send(packet)).await;',
    '''                let _ = timeout(Duration::from_millis(100), async {
                    match packet {
                        Ok(bytes) => {
                            for chunk in bytes.chunks(self.inner.limits.chunk_bytes) {
                                self.sender.send(Ok(Bytes::copy_from_slice(chunk))).await.map_err(|_| ())?;
                            }
                        }
                        Err(error) => self.sender.send(Err(error)).await.map_err(|_| ())?,
                    }
                    Ok::<(), ()>(())
                }).await;''')
edit('request.rs',
    '                    let role = Role::parse(&m["role"])?;\n                    let mut content = Vec::new();',
    '''                    let role = Role::parse(&m["role"])?;
                    if role == Role::Tool {
                        keys(m, &["role", "content", "tool_call_id"])?;
                    } else if m.get("tool_call_id").is_some() {
                        return Err(RelayError::Invalid("tool_call_id requires tool role"));
                    }
                    let mut content = Vec::new();''')
edit('request.rs',
    '                        match i.get("type").and_then(Value::as_str).unwrap_or("message") {',
    '''                        let kind = match i.get("type") {
                            None => "message",
                            Some(kind) => kind.as_str().ok_or(RelayError::Invalid("invalid Responses input type"))?,
                        };
                        match kind {''')
edit('request.rs',
    '                let sig = match b {',
    '''                if route.protocol == UpstreamProtocol::Anthropic
                    && matches!(b, InputBlock::ToolCall { signature: Some(_), .. }) {
                    return Err(RelayError::Unsupported("Anthropic tool history cannot carry standalone signatures"));
                }
                let sig = match b {''')
edit('encode.rs',
    '''                    } else {
                        json!({"reasoning_details":[{"index":index,"type":"signature_delta","protocol":provider,"signature":value}]})
                    };''',
    '''                    } else if matches!(b.kind, BlockKind::Thinking) {
                        json!({"reasoning_details":[{"index":index,"type":"signature_delta","protocol":provider,"signature":value}]})
                    } else {
                        json!({"content_details":[{"index":index,"type":"signature_delta","protocol":provider,"signature":value}]})
                    };''')
edit('encode.rs',
    '''        let mut details = Vec::new();
        for (i, b) in self.blocks.iter().enumerate() {''',
    '''        let mut details = Vec::new();
        let mut content_details = Vec::new();
        for (i, b) in self.blocks.iter().enumerate() {
            if matches!(b.kind, BlockKind::Text | BlockKind::Refusal)
                && let Some((provider, signature)) = &b.signature {
                content_details.push(json!({"index":i,"protocol":provider,"signature":signature}));
            }''')
edit('encode.rs',
    '        if details.len() == 1 {',
    '''        if !content_details.is_empty() {
            message["content_details"] = json!(content_details);
        }
        if details.len() == 1 {''')
edit('decode.rs',
    'impl Decoder {',
    '''fn plain_response_part(part: &Value) -> Result<(), RelayError> {
    if part.get("annotations").is_some_and(|a| !a.is_null() && a.as_array().is_none_or(|a| !a.is_empty())) {
        return Err(RelayError::Protocol("annotated Responses output is not supported"));
    }
    Ok(())
}
fn plain_response_item(item: &Value) -> Result<(), RelayError> {
    if item.get("encrypted_content").is_some_and(|v| !v.is_null()) {
        return Err(RelayError::Protocol("encrypted Responses output is not supported"));
    }
    if let Some(parts) = item.get("content").filter(|v| !v.is_null()) {
        for part in parts.as_array().ok_or(RelayError::Protocol("invalid Responses content"))? {
            plain_response_part(part)?;
        }
    }
    Ok(())
}
fn plain_response(response: &Value) -> Result<(), RelayError> {
    if let Some(items) = response.get("output").filter(|v| !v.is_null()) {
        for item in items.as_array().ok_or(RelayError::Protocol("invalid Responses output"))? {
            plain_response_item(item)?;
        }
    }
    Ok(())
}

impl Decoder {''')
edit('decode.rs',
    '''            UpstreamProtocol::Responses => {
                self.start(v["id"].as_str(), &mut out)?;''',
    '''            UpstreamProtocol::Responses => {
                plain_response(v)?;
                self.start(v["id"].as_str(), &mut out)?;''')
edit('decode.rs',
    '''    fn responses_event(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        match v["type"].as_str() {''',
    '''    fn responses_event(&mut self, v: &Value, out: &mut Vec<Event>) -> Result<(), RelayError> {
        if let Some(item) = v.get("item") { plain_response_item(item)?; }
        if let Some(part) = v.get("part") { plain_response_part(part)?; }
        if let Some(response) = v.get("response") { plain_response(response)?; }
        match v["type"].as_str() {''')

p = ROOT / 'types.rs'
s = p.read_text()
a = s.index('    pub(crate) fn merge(')
b = s.index('    pub(crate) fn wire(',a)
method = s[a:b]
assert 'let mut combined' not in method
method = method.replace('self.', 'combined.')
method = method.replace('        field(&mut combined.input_tokens, next.input_tokens)?;',
    '        let mut combined = *self;\n        field(&mut combined.input_tokens, next.input_tokens)?;',1)
last = method.rfind('        Ok(())\n')
assert last >= 0
method = method[:last] + '        *self = combined;\n' + method[last:]
p.write_text(s[:a] + method + s[b:])

p = ROOT / 'tests.rs'
s = p.read_text()
assert 'invalid_usage_does_not_partially_replace_the_last_valid_snapshot' not in s
s += '''
#[test]
fn invalid_usage_does_not_partially_replace_the_last_valid_snapshot() {
    let before = Usage { input_tokens:Some(7),output_tokens:Some(3),..Usage::default() };
    let mut current = before;
    assert!(current.merge(Usage { input_tokens:Some(8),output_tokens:Some(2),..Usage::default() }).is_err());
    assert_eq!(current,before);
    let before = Usage { input_tokens:Some(u64::MAX),..Usage::default() };
    let mut current = before;
    assert!(current.merge(Usage { output_tokens:Some(1),..Usage::default() }).is_err());
    assert_eq!(current,before);
}
'''
p.write_text(s)
p = Path('apps/core-rust/tests/relay_http.rs')
s = p.read_text()
assert 'error_packets_obey_the_same_chunk_limit_as_model_output' not in s
s += '''
#[tokio::test]
async fn error_packets_obey_the_same_chunk_limit_as_model_output() {
    let upstream = Upstream::start(Script::sse(first_text("partial"))).await;
    let limits = Limits { queue_chunks:2,chunk_bytes:17,..Limits::default() };
    let money = Arc::new(Money::default());
    let relay = relay(&upstream,UpstreamProtocol::Chat,limits.clone(),money);
    let Session { head,mut body,completion,.. } = relay.start(request(ClientProtocol::Chat,true,&limits),context()).unwrap();
    timeout(WAIT,head).await.unwrap().unwrap().unwrap();
    let mut bytes = Vec::new();
    while let Some(chunk) = timeout(WAIT,body.next_chunk()).await.unwrap() {
        let chunk = chunk.unwrap(); assert!(chunk.len() <= 17); bytes.extend(chunk);
    }
    let done = timeout(WAIT,completion).await.unwrap().unwrap();
    assert_eq!(done.error,Some(RelayError::Truncated));
    let body = String::from_utf8(bytes).unwrap();
    assert!(body.contains("invalid_upstream_response")); assert!(!body.contains("[DONE]"));
}
'''
p.write_text(s)
p = ROOT / 'README.md'
s = p.read_text()
s = s.replace('Counters cannot decrease or overflow.', 'Counters cannot decrease or overflow; invalid counters leave the last accepted snapshot unchanged.')
s = s.replace('Opaque signatures remain provider-tagged metadata, not fabricated OpenAI', 'Plain-text signatures use the Chat `content_details` extension or Responses item metadata. These details are preserved on output; replay of signed plain-text history is not yet supported.\nOpaque signatures remain provider-tagged metadata, not fabricated OpenAI')
s = s.replace('unknown request fields, unsupported provider content blocks and', 'unknown request fields, annotated Responses output, unsupported provider content blocks and')
s = s.replace('300 s total; 10 s per billing/routing hook.', '300 s forwarding deadline; 10 s per billing/routing hook. Bounded money cleanup can continue after the forwarding deadline.')
p.write_text(s)
