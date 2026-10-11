package toolmarket

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"
	"time"

	lab "github.com/TokenNotIncluded/api.lmm.best/extensions/internal/testkit/mocklab"
)

// Exercises the real MCP wire client. Does not invent Rust Authority or Funds,
// bypass market registration, or label provider results as settled executions.
func TestMocklabMCPAdapter(t *testing.T) {
	path := os.Getenv("LMM_MOCKLAB_MANIFEST")
	if path == "" {
		t.Skip("NOT RUN: standalone mocklab manifest is not configured")
	}
	c, err := lab.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	client, err := c.HTTP("mcp", "tools.mock.invalid")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	session := &mcpSession{net: &network{client: client}, endpoint: "https://tools.mock.invalid/mcp",
		headers: http.Header{"Accept": {"application/json, text/event-stream"}, "Content-Type": {"application/json"}, "Mcp-Protocol-Version": {ProtocolVersion}}}
	defer session.close(ctx)
	raw, err := session.rpc(ctx, "initialize", map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{},
		"clientInfo": map[string]string{"name": "lmm-mocklab-test", "version": "1"}})
	if err != nil {
		t.Fatal(err)
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(raw, &init) != nil || init.ProtocolVersion != ProtocolVersion || session.headers.Get("Mcp-Session-Id") == "" {
		t.Fatal("MCP version/session mismatch")
	}
	if _, err = session.rpc(ctx, "notifications/initialized", map[string]any{}); err != nil {
		t.Fatal(err)
	}
	tools, err := session.tools(ctx)
	if err != nil || len(tools) != 5 {
		t.Fatal("real schema discovery failed", err)
	}
	found := false
	for _, tool := range tools {
		if tool.Name == "mock_add" {
			found = true
		}
	}
	if !found {
		t.Fatal("mock_add not discovered")
	}
	raw, err = session.rpc(ctx, "tools/call", map[string]any{"name": "mock_add", "arguments": map[string]int{"a": 2, "b": 3}})
	if err != nil {
		t.Fatal(err)
	}
	var result struct {
		Content []struct {
			Text string `json:"text"`
		} `json:"content"`
		IsError bool `json:"isError"`
	}
	if json.Unmarshal(raw, &result) != nil || result.IsError || len(result.Content) != 1 || result.Content[0].Text != `{"value":5}` {
		t.Fatal("deterministic MCP result differs")
	}
	raw, err = session.rpc(ctx, "tools/call", map[string]any{"name": "mock_fail", "arguments": map[string]any{}})
	if err != nil || json.Unmarshal(raw, &result) != nil || !result.IsError {
		t.Fatal("tool isError contract", err)
	}
	session.close(ctx)
	var stats struct {
		Providers map[string]struct {
			Sessions int `json:"sessions"`
			Calls    int `json:"tool_calls"`
		} `json:"providers"`
	}
	if err = c.Control(ctx, map[string]any{"op": "stats"}, &stats); err != nil {
		t.Fatal(err)
	}
	if stats.Providers["mcp"].Sessions != 0 || stats.Providers["mcp"].Calls < 2 {
		t.Fatal("MCP session not released")
	}
	// No price or wallet assertion: those require the actual market and core.
}
