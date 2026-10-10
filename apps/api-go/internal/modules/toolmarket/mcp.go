package toolmarket

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"net/http"
	"strings"
)

type rpcEnvelope struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   json.RawMessage `json:"error,omitempty"`
}
type mcpSession struct {
	net      *network
	endpoint string
	headers  http.Header
	next     int
}

func (m *Module) connect(ctx context.Context, s Server, secret Secret) (*mcpSession, error) {
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
	c := &mcpSession{net: m.net, endpoint: s.Endpoint, headers: h}
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
	return c, nil
}
func (c *mcpSession) close(ctx context.Context) {
	if c.headers.Get("Mcp-Session-Id") == "" {
		return
	}
	r, e := c.net.request(ctx, "DELETE", c.endpoint, nil, c.headers)
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
	// Bound both each event and the entire stream, including endless comments.
	scan := bufio.NewScanner(io.LimitReader(r.Body, maxBody+1))
	scan.Buffer(make([]byte, 4096), maxBody)
	var data []string
	total := 0
	for scan.Scan() {
		line := scan.Text()
		total += len(line) + 1
		if total > maxBody {
			return nil, ErrUpstream
		}
		if line == "" {
			if len(data) > 0 {
				b := []byte(strings.Join(data, "\n"))
				data = nil
				if uniqueJSON(b) != nil {
					return nil, ErrUpstream
				}
				var env rpcEnvelope
				if json.Unmarshal(b, &env) != nil {
					return nil, ErrUpstream
				}
				if len(env.ID) > 0 {
					return rpcResult(b, id)
				}
			}
			continue
		}
		if strings.HasPrefix(line, "data:") {
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	return nil, ErrUpstream // Never retry an uncertain tools/call automatically.
}
func rpcResult(b []byte, id json.RawMessage) (json.RawMessage, error) {
	if uniqueJSON(b) != nil {
		return nil, ErrUpstream
	}
	var r rpcEnvelope
	if json.Unmarshal(b, &r) != nil || r.JSONRPC != "2.0" || !bytes.Equal(r.ID, id) || r.Method != "" || len(r.Error) > 0 || len(r.Result) == 0 || bytes.Equal(r.Result, []byte("null")) {
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
		if json.Unmarshal(b, &list) != nil || len(list.Tools) == 0 && list.NextCursor != "" {
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
