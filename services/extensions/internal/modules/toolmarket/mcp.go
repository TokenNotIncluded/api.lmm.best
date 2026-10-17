package toolmarket

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime"
	"net/http"
	"strings"
	"time"
)

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}
type mcpSession struct {
	net          *network
	endpoint     string
	headers      http.Header
	next         int
	checkpoint   func(json.RawMessage, string, time.Duration) error
	beforeResume func() error
	preserve     bool
}

func (m *Module) connectionHeaders(s Server, secret Secret) (http.Header, error) {
	h := http.Header{"Accept": []string{"application/json, text/event-stream"}, "Content-Type": []string{"application/json"}, "Mcp-Protocol-Version": []string{ProtocolVersion}}
	if s.Auth != "none" {
		if secret.Access == "" || secret.Expires > 0 && secret.Expires <= m.now().Unix() {
			return nil, ErrAuth
		}
		if s.Auth == "oauth" || s.Header == "Authorization" {
			h.Set("Authorization", "Bearer "+secret.Access)
		} else {
			h.Set(s.Header, secret.Access)
		}
	}
	return h, nil
}

func (m *Module) connect(ctx context.Context, s Server, secret Secret) (*mcpSession, error) {
	h, e := m.connectionHeaders(s, secret)
	if e != nil {
		return nil, e
	}
	c := &mcpSession{net: m.net, endpoint: s.Endpoint, headers: h}
	connected := false
	defer func() {
		if !connected {
			c.close(ctx)
		}
	}()
	b, e := c.rpc(ctx, "initialize", map[string]any{"protocolVersion": ProtocolVersion, "capabilities": map[string]any{}, "clientInfo": map[string]string{"name": "lmm-toolmarket", "version": "1"}})
	if e != nil {
		return nil, e
	}
	var init struct {
		ProtocolVersion string `json:"protocolVersion"`
		Capabilities    struct {
			Tools *json.RawMessage `json:"tools"`
		} `json:"capabilities"`
	}
	if json.Unmarshal(b, &init) != nil || init.ProtocolVersion != ProtocolVersion || init.Capabilities.Tools == nil {
		return nil, ErrUpstream
	}
	if _, e = c.rpc(ctx, "notifications/initialized", map[string]any{}); e != nil {
		return nil, e
	}
	connected = true
	return c, nil
}
func (c *mcpSession) close(ctx context.Context) {
	if c.headers.Get("Mcp-Session-Id") == "" {
		return
	}
	// Cleanup has its own small deadline, including after request cancellation.
	// It never retries a model/tool operation or resets its durable state.
	cleanup, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
	defer cancel()
	headers := c.headers.Clone()
	c.headers.Del("Mcp-Session-Id")
	r, e := c.net.request(cleanup, "DELETE", c.endpoint, nil, headers)
	if e == nil {
		r.Body.Close()
	}
}
func (c *mcpSession) rpc(ctx context.Context, method string, params any) (json.RawMessage, error) {
	notification := strings.HasPrefix(method, "notifications/")
	message := map[string]any{"jsonrpc": "2.0", "method": method, "params": params}
	c.next++
	id := json.RawMessage(fmt.Sprint(c.next))
	if !notification {
		message["id"] = c.next
	}
	body, e := json.Marshal(message)
	if e != nil {
		return nil, ErrInvalid
	}
	r, e := c.net.request(ctx, "POST", c.endpoint, body, c.headers)
	if e != nil {
		return nil, e
	}
	defer r.Body.Close()
	if r.StatusCode == 401 || r.StatusCode == 403 {
		return nil, ErrAuth
	}
	if r.StatusCode == 429 {
		return nil, ErrLimit
	}
	if r.StatusCode < 200 || r.StatusCode >= 300 {
		return nil, ErrUpstream
	}
	if method == "initialize" {
		if len(r.Header.Values("Mcp-Session-Id")) > 1 {
			return nil, ErrUpstream
		}
		sid := r.Header.Get("Mcp-Session-Id")
		if len(sid) > 512 {
			return nil, ErrUpstream
		}
		for _, ch := range sid {
			if ch < 33 || ch > 126 {
				return nil, ErrUpstream
			}
		}
		if sid != "" {
			c.headers.Set("Mcp-Session-Id", sid)
		}
	}
	if notification {
		if r.StatusCode != 202 {
			return nil, ErrUpstream
		}
		return nil, nil
	}
	kind, _, e := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if e != nil {
		return nil, ErrUpstream
	}
	if kind == "application/json" {
		b, e := readBody(r)
		if e != nil {
			return nil, e
		}
		return rpcResult(b, id)
	}
	if kind != "text/event-stream" {
		return nil, ErrUpstream
	}
	stream := newMCPStream(id, nil)
	if c.checkpoint != nil {
		stream.checkpoint = func(cursor string, retry time.Duration) error {
			// A failed store acknowledgement may already have committed a cursor.
			// Do not delete the session while that outcome remains uncertain.
			c.preserve = true
			return c.checkpoint(id, cursor, retry)
		}
	}
	result, err := stream.read(r)
	if !errors.Is(err, errStreamEnded) {
		return result, err
	}
	// Release the old TCP response before trying a read-only reconnection.
	r.Body.Close()
	return c.resumeStream(ctx, stream)
}
func rpcResult(b []byte, id json.RawMessage) (json.RawMessage, error) {
	if uniqueJSON(b) != nil {
		return nil, ErrUpstream
	}
	var r rpcEnvelope
	if json.Unmarshal(b, &r) != nil {
		return nil, ErrUpstream
	}
	return checkedRPCResult(r, id)
}

// checkedRPCResult accepts only an envelope already checked by uniqueJSON and
// json.Unmarshal. The SSE path can reuse that envelope instead of parsing twice.
func checkedRPCResult(r rpcEnvelope, id json.RawMessage) (json.RawMessage, error) {
	if r.JSONRPC != "2.0" || !bytes.Equal(r.ID, id) || r.Method != "" || len(r.Error) > 0 || len(r.Result) == 0 || bytes.Equal(r.Result, []byte("null")) {
		return nil, ErrUpstream
	}
	return r.Result, nil
}
func (c *mcpSession) tools(ctx context.Context) ([]Tool, error) {
	result := []Tool{}
	cursor := ""
	seen := map[string]bool{}
	names := map[string]bool{}
	for page := 0; page < 20; page++ {
		p := map[string]string{}
		if cursor != "" {
			p["cursor"] = cursor
		}
		b, e := c.rpc(ctx, "tools/list", p)
		if e != nil {
			return nil, e
		}
		var list struct {
			Tools      []Tool `json:"tools"`
			NextCursor string `json:"nextCursor"`
		}
		if json.Unmarshal(b, &list) != nil || list.Tools == nil || len(list.Tools) == 0 && list.NextCursor != "" {
			return nil, ErrUpstream
		}
		for _, t := range list.Tools {
			if !validTool.MatchString(t.Name) || names[t.Name] || len(t.Description) > 2000 {
				return nil, ErrUpstream
			}
			s, e := readSchema(t.InputSchema, 0)
			if e != nil || s.Type != "object" {
				return nil, invalid("unsupported tool schema")
			}
			if len(t.OutputSchema) > 0 && string(t.OutputSchema) != "null" {
				s, e := readSchema(t.OutputSchema, 0)
				if e != nil || s.Type != "object" {
					return nil, invalid("unsupported output schema")
				}
			}
			names[t.Name] = true
			result = append(result, t)
			if len(result) > 200 {
				return nil, ErrLimit
			}
		}
		cursor = list.NextCursor
		if cursor == "" {
			return result, nil
		}
		if len(cursor) > 1024 || seen[cursor] {
			return nil, ErrUpstream
		}
		seen[cursor] = true
	}
	return nil, ErrLimit
}
