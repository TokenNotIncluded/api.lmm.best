"""The repository's pinned MCP 2025-11-25 JSON HTTP profile."""
from __future__ import annotations

from .config import Invalid, integer, object_fields, wire
from .protocol import Reply, json_reply, ProviderError

VERSION = "2025-11-25"
SCHEMAS = {
    "mock_add": {"type": "object", "properties": {"a": {"type": "integer", "minimum": -1000, "maximum": 1000},
                  "b": {"type": "integer", "minimum": -1000, "maximum": 1000}}, "required": ["a", "b"], "additionalProperties": False},
    "mock_upper": {"type": "object", "properties": {"text": {"type": "string", "maxLength": 256}},
                   "required": ["text"], "additionalProperties": False},
    "mock_lookup": {"type": "object", "properties": {"key": {"type": "string", "enum": ["one", "two"]}},
                    "required": ["key"], "additionalProperties": False},
    "mock_delay": {"type": "object", "properties": {"delay_ms": {"type": "integer", "minimum": 0, "maximum": 86400000}},
                   "required": ["delay_ms"], "additionalProperties": False},
    "mock_fail": {"type": "object", "properties": {}, "additionalProperties": False},
}


class MCP:
    def __init__(self, uid):
        self.uid = uid
        self.sessions: dict[str, bool] = {}
        self.calls = 0

    def handle(self, method: str, path: str, req: dict, headers: dict, kind: str) -> Reply:
        if path != "/mcp":
            raise ProviderError(404, "mcp_endpoint_not_found")
        sid = headers.get("mcp-session-id", "")
        if method == "DELETE":
            if sid not in self.sessions:
                raise ProviderError(404, "session_not_found")
            del self.sessions[sid]
            return Reply(204)
        if method != "POST":
            raise ProviderError(405, "mcp_method_not_supported")
        object_fields(req, {"jsonrpc", "id", "method", "params"}, {"jsonrpc", "method"})
        if req["jsonrpc"] != "2.0" or not isinstance(req.get("params", {}), dict):
            raise Invalid("invalid JSON-RPC envelope")
        name, params = req["method"], req.get("params", {})
        if "id" in req and (type(req["id"]) not in {int, str} or isinstance(req["id"], str) and len(req["id"]) > 128):
            raise Invalid("invalid JSON-RPC id")
        if name == "initialize":
            if ("id" not in req or params.get("protocolVersion") != VERSION
                    or headers.get("mcp-protocol-version", VERSION) != VERSION):
                raise Invalid("only pinned MCP 2025-11-25 is implemented")
            sid = self.uid("session")
            self.sessions[sid] = False
            reply = json_reply({"jsonrpc": "2.0", "id": req["id"], "result": {
                "protocolVersion": VERSION, "capabilities": {"tools": {}},
                "serverInfo": {"name": "MOCK ONLY - lmm-mocklab", "version": "1"}}})
            reply.headers["Mcp-Session-Id"] = sid
            return reply
        if sid not in self.sessions:
            raise ProviderError(404, "session_not_found")
        if headers.get("mcp-protocol-version") != VERSION:
            raise Invalid("missing negotiated MCP version")
        if name == "notifications/initialized" and "id" not in req:
            self.sessions[sid] = True
            return Reply(202)
        if not self.sessions[sid] or "id" not in req:
            raise Invalid("MCP handshake is not complete")
        if name == "tools/list":
            object_fields(params, {"cursor"})
            if params.get("cursor"):
                raise Invalid("this small fixture has one page")
            result = {"tools": [{"name": n, "description": "MOCK ONLY: deterministic local test tool",
                                  "inputSchema": schema} for n, schema in SCHEMAS.items()]}
        elif name == "tools/call":
            object_fields(params, {"name", "arguments"}, {"name", "arguments"})
            tool = params["name"]
            if tool not in SCHEMAS:
                return json_reply({"jsonrpc": "2.0", "id": req["id"], "error": {"code": -32602, "message": "Unknown mock tool"}})
            args = object_fields(params["arguments"], set(SCHEMAS[tool]["properties"]), set(SCHEMAS[tool].get("required", [])))
            delay = 0
            if tool == "mock_add":
                value = integer(args["a"], -1000, 1000) + integer(args["b"], -1000, 1000)
            elif tool == "mock_upper":
                if not isinstance(args["text"], str) or len(args["text"]) > 256:
                    raise Invalid("invalid mock text input")
                value = args["text"].upper()
            elif tool == "mock_lookup":
                if args["key"] not in {"one", "two"}:
                    raise Invalid("unknown mock lookup key")
                value = {"one": 1, "two": 2}[args["key"]]
            elif tool == "mock_delay":
                delay = integer(args["delay_ms"], 0, 86400000)
                value = "delay complete"
            else:
                value = "controlled tool failure"
            self.calls += 1
            failed = tool == "mock_fail" or kind == "tool_error"
            result = {"content": [{"type": "text", "text": wire({"value": value}).decode()}], "isError": failed}
            reply = json_reply({"jsonrpc": "2.0", "id": req["id"], "result": result})
            reply.first_byte_ms = delay
            # A disconnected provider call is not safe to replay. The market decides settlement.
            if kind == "disconnect":
                reply.chunks = [b'data: {"jsonrpc":"2.0",']
                reply.headers["Content-Type"] = "text/event-stream"
                reply.interrupt_after = 1
            return reply
        else:
            return json_reply({"jsonrpc": "2.0", "id": req["id"], "error": {"code": -32601, "message": "Method not implemented"}})
        return json_reply({"jsonrpc": "2.0", "id": req["id"], "result": result})
