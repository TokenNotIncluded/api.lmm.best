"""Small protocol-specific fixtures, not universal model emulation."""
from __future__ import annotations

from .config import Instance, Invalid, loads
from .protocol import Reply, json_reply, sse



def fault(protocol: str, kind: str) -> Reply:
    status, code = {"rate_limit": (429, "rate_limit_exceeded"),
                    "upstream_error": (503, "upstream_unavailable"),
                    "context_exceeded": (400, "context_length_exceeded")}[kind]
    if protocol == "anthropic-messages":
        body = {"type": "error", "error": {"type": "rate_limit_error" if status == 429 else
                "overloaded_error" if status == 503 else "invalid_request_error", "message": "Mock scenario"}}
    elif protocol == "gemini-generate-content":
        body = {"error": {"code": status, "status": {429: "RESOURCE_EXHAUSTED", 503: "UNAVAILABLE", 400: "INVALID_ARGUMENT"}[status],
                          "message": "Mock scenario"}}
    else:
        body = {"error": {"type": "invalid_request_error" if status == 400 else "server_error",
                          "code": code, "message": "Mock scenario"}}
    r = json_reply(body, status)
    if status == 429:
        r.headers["Retry-After"] = "1"
    return r


def response(instance: Instance, path: str, request: dict, ident: str, now: int) -> Reply:
    p = instance.protocol
    expected = {"openai-chat": "/v1/chat/completions", "openai-responses": "/v1/responses",
                "anthropic-messages": "/v1/messages"}
    stream = request.get("stream", False)
    if type(stream) is not bool:
        raise Invalid("stream must be boolean")
    if p == "gemini-generate-content":
        normal = f"/v1beta/models/{instance.model}:generateContent"
        streamed = f"/v1beta/models/{instance.model}:streamGenerateContent"
        if path not in (normal, streamed):
            raise Invalid("unsupported Gemini method or model")
        stream = path == streamed
        if not isinstance(request.get("contents"), list):
            raise Invalid("contents required")
    else:
        if path != expected[p] or request.get("model") != instance.model:
            raise Invalid("unsupported endpoint or model")
        required = "input" if p == "openai-responses" else "messages"
        if required not in request:
            raise Invalid("missing model input")
    text = "" if instance.scenario.kind == "empty" else instance.text
    parts = [text[n:n + instance.chunk_chars] for n in range(0, len(text), instance.chunk_chars)]
    tool = instance.output_kind == "tool_call"
    tool_args = '{"a":2,"b":3}'
    usage = {"input": instance.input_tokens, "output": instance.output_tokens, "cached": instance.cached_tokens}
    missing = instance.scenario.kind == "missing_usage"
    payload, chunks = {"openai-chat": chat, "openai-responses": responses,
                       "anthropic-messages": anthropic, "gemini-generate-content": gemini}[p](
                           request, ident, now, instance.model, text, parts, tool, tool_args, usage, missing)
    reply = (Reply(headers={"Content-Type": "text/event-stream", "Cache-Control": "no-cache"}, chunks=chunks)
             if stream else json_reply(payload))
    reply.first_byte_ms = instance.scenario.first_byte_ms
    reply.chunk_delay_ms = instance.scenario.chunk_delay_ms
    if instance.scenario.kind == "disconnect":
        if not stream:
            raise Invalid("disconnect fixture requires a stream")
        reply.interrupt_after = instance.scenario.interrupt_after
    return reply


def chat(req, ident, now, model, text, parts, tool, args, u, missing):
    base = {"id": "chatcmpl_" + ident, "created": now, "model": model}
    usage = {"prompt_tokens": u["input"], "completion_tokens": u["output"],
             "total_tokens": u["input"] + u["output"], "prompt_tokens_details": {"cached_tokens": u["cached"]}}
    finish = "tool_calls" if tool else "stop"
    msg = {"role": "assistant", "content": None if tool else text}
    call = {"id": "call_" + ident, "type": "function", "function": {"name": "mock_add", "arguments": args}}
    if tool:
        msg["tool_calls"] = [call]
    out = {**base, "object": "chat.completion", "choices": [{"index": 0, "message": msg, "finish_reason": finish}]}
    if not missing:
        out["usage"] = usage
    def chunk(delta, finish_reason=None):
        return sse({**base, "object": "chat.completion.chunk", "choices": [
            {"index": 0, "delta": delta, "finish_reason": finish_reason}], "usage": None})
    frames = [chunk({"role": "assistant", "content": ""})]
    if tool:
        frames.append(chunk({"tool_calls": [{"index": 0, **call, "function": {"name": "mock_add", "arguments": ""}}]}))
        frames += [chunk({"tool_calls": [{"index": 0, "function": {"arguments": part}}]}) for part in (args[:7], args[7:])]
    else:
        frames += [chunk({"content": part}) for part in parts]
    frames.append(chunk({}, finish))
    options = req.get("stream_options", {})
    if not isinstance(options, dict):
        raise Invalid("invalid stream_options")
    if options.get("include_usage", False) and not missing:
        frames.append(sse({**base, "object": "chat.completion.chunk", "choices": [], "usage": usage}))
    frames.append(b"data: [DONE]\n\n")
    return out, frames


def responses(req, ident, now, model, text, parts, tool, args, u, missing):
    rid, mid = "resp_" + ident, "msg_" + ident
    content = {"type": "output_text", "text": text, "annotations": [], "logprobs": []}
    item = ({"type": "function_call", "id": "fc_" + ident, "call_id": "call_" + ident,
             "name": "mock_add", "arguments": args, "status": "completed"} if tool else
            {"type": "message", "id": mid, "role": "assistant", "status": "completed", "content": [content]})
    out = {"id": rid, "object": "response", "created_at": now, "status": "completed", "error": None,
           "incomplete_details": None, "model": model, "output": [item], "parallel_tool_calls": False,
           "tool_choice": "auto", "tools": [], "store": False}
    if not missing:
        out["usage"] = {"input_tokens": u["input"], "output_tokens": u["output"],
                        "total_tokens": u["input"] + u["output"], "input_tokens_details": {"cached_tokens": u["cached"]},
                        "output_tokens_details": {"reasoning_tokens": 0}}
    frames = []
    def event(kind, **data):
        frames.append(sse({"type": kind, "sequence_number": len(frames), **data}, kind))
    started = {**out, "status": "in_progress", "output": [], "usage": None}
    event("response.created", response=started)
    event("response.in_progress", response=started)
    initial = {**item, "status": "in_progress"}
    if tool:
        initial["arguments"] = ""
    else:
        initial["content"] = []
    event("response.output_item.added", output_index=0, item=initial)
    if tool:
        for part in (args[:7], args[7:]):
            event("response.function_call_arguments.delta", item_id=item["id"], output_index=0, delta=part)
        event("response.function_call_arguments.done", item_id=item["id"], output_index=0, arguments=args)
    else:
        event("response.content_part.added", item_id=mid, output_index=0, content_index=0, part={**content, "text": ""})
        for part in parts:
            event("response.output_text.delta", item_id=mid, output_index=0, content_index=0, delta=part, logprobs=[])
        event("response.output_text.done", item_id=mid, output_index=0, content_index=0, text=text, logprobs=[])
        event("response.content_part.done", item_id=mid, output_index=0, content_index=0, part=content)
    event("response.output_item.done", output_index=0, item=item)
    event("response.completed", response=out)
    return out, frames


def anthropic(req, ident, now, model, text, parts, tool, args, u, missing):
    content = ({"type": "tool_use", "id": "toolu_" + ident, "name": "mock_add", "input": loads(args)}
               if tool else {"type": "text", "text": text})
    # The fixture's input count includes cache hits. Anthropic separates them.
    usage = {"input_tokens": u["input"] - u["cached"], "output_tokens": u["output"],
             "cache_creation_input_tokens": 0, "cache_read_input_tokens": u["cached"]}
    stop = "tool_use" if tool else "end_turn"
    out = {"id": "msg_" + ident, "type": "message", "role": "assistant", "model": model,
           "content": [content], "stop_reason": stop, "stop_sequence": None}
    if not missing:
        out["usage"] = usage
    initial = {**out, "content": [], "stop_reason": None}
    if not missing:
        initial["usage"] = {**usage, "output_tokens": 0}
    def event(kind, **data):
        return sse({"type": kind, **data}, kind)
    block = {**content, "input": {}} if tool else {"type": "text", "text": ""}
    frames = [event("message_start", message=initial), event("content_block_start", index=0, content_block=block)]
    for part in ((args[:7], args[7:]) if tool else parts):
        delta = {"type": "input_json_delta", "partial_json": part} if tool else {"type": "text_delta", "text": part}
        frames.append(event("content_block_delta", index=0, delta=delta))
    frames.append(event("content_block_stop", index=0))
    final = {"delta": {"stop_reason": stop, "stop_sequence": None}}
    if not missing:
        final["usage"] = {"output_tokens": u["output"]}  # Cumulative, not a per-chunk increment.
    frames += [event("message_delta", **final), event("message_stop")]
    return out, frames


def gemini(req, ident, now, model, text, parts, tool, args, u, missing):
    def candidate(part, final=False):
        result = {"index": 0, "content": {"role": "model", "parts": [part]}}
        if final:
            result["finishReason"] = "STOP"
        return result
    payload_part = {"functionCall": {"name": "mock_add", "args": loads(args)}} if tool else {"text": text}
    usage = {"promptTokenCount": u["input"], "candidatesTokenCount": u["output"],
             "totalTokenCount": u["input"] + u["output"], "cachedContentTokenCount": u["cached"]}
    out = {"candidates": [candidate(payload_part, True)], "modelVersion": model, "responseId": ident}
    if not missing:
        out["usageMetadata"] = usage
    pieces = [payload_part] if tool else [{"text": part} for part in parts] or [{"text": ""}]
    frames = []
    for index, part in enumerate(pieces):
        final = index == len(pieces) - 1
        body = {"candidates": [candidate(part, final)], "modelVersion": model, "responseId": ident}
        if final and not missing:
            body["usageMetadata"] = usage
        frames.append(sse(body))
    return out, frames
