package service

import (
	"bytes"
	"io"
	"mime"
	"net/http"
	"strings"
	"sync"
)

// The MCP SDK decodes open schema/result objects through float64. Keep only the
// latest response for each of its three relevant methods, in memory, and restore
// numeric fields after the SDK has validated the protocol response. This never
// sends another request or substitutes for SDK protocol dispatch.
type marketWireCapture struct {
	mu        sync.Mutex
	responses map[string]*marketWireResponse
}

type marketWireResponse struct {
	id          string
	contentType string
	data        []byte
	tooLarge    bool
}

type marketWireBody struct {
	io.ReadCloser
	owner    *marketWireCapture
	method   string
	response *marketWireResponse
}

func (c *marketWireCapture) wrap(req *http.Request, resp *http.Response) {
	if req.Method != http.MethodPost || req.GetBody == nil {
		return
	}
	body, err := req.GetBody()
	if err != nil {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(body, (256<<10)+1))
	_ = body.Close()
	if err != nil || len(raw) > 256<<10 {
		return
	}
	var envelope map[string]any
	if marketDecodeExactJSON(raw, &envelope) != nil {
		return
	}
	method, _ := envelope["method"].(string)
	if method != "initialize" && method != "tools/list" && method != "tools/call" {
		return
	}
	id, exists := envelope["id"]
	if !exists || id == nil {
		return
	}
	contentType, _, contentTypeErr := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	if contentTypeErr != nil {
		contentType = ""
	}
	response := &marketWireResponse{id: marketCanonical(id), contentType: contentType}
	c.mu.Lock()
	if c.responses == nil {
		c.responses = make(map[string]*marketWireResponse, 3)
	}
	c.responses[method] = response
	c.mu.Unlock()
	resp.Body = &marketWireBody{ReadCloser: resp.Body, owner: c, method: method, response: response}
}

func (b *marketWireBody) Read(data []byte) (int, error) {
	n, err := b.ReadCloser.Read(data)
	if n > 0 {
		b.owner.mu.Lock()
		// Retired page/response readers cannot overwrite a newer RPC response.
		if b.owner.responses[b.method] == b.response {
			if len(b.response.data)+n > 2<<20 {
				b.response.tooLarge = true
				b.owner.mu.Unlock()
				return 0, ErrMarketRemoteResult
			}
			b.response.data = append(b.response.data, data[:n]...)
		}
		b.owner.mu.Unlock()
	}
	return n, err
}

func (c *marketWireCapture) result(method string) (map[string]any, error) {
	c.mu.Lock()
	response := c.responses[method]
	if response == nil || response.tooLarge {
		c.mu.Unlock()
		return nil, ErrMarketRemoteResult
	}
	id, contentType := response.id, response.contentType
	raw := append([]byte(nil), response.data...)
	c.mu.Unlock()
	if contentType == "application/json" {
		return marketWireResult(raw, id)
	}
	if contentType != "text/event-stream" {
		return nil, ErrMarketRemoteResult
	}
	// Only complete message events for this RPC ID qualify. Comments, progress
	// notifications, unrelated IDs and other SSE event types are ignored.
	var eventType string
	var eventData []byte
	lines := bytes.Split(raw, []byte{'\n'})
	// The trailing split segment is not a complete line. A single final
	// newline terminates data, but only a second newline dispatches an event.
	for _, line := range lines[:len(lines)-1] {
		line = bytes.TrimSuffix(line, []byte{'\r'})
		if len(line) == 0 {
			if len(eventData) > 0 && (eventType == "" || eventType == "message") {
				if result, err := marketWireResult(eventData, id); err == nil {
					return result, nil
				}
			}
			eventType, eventData = "", nil
			continue
		}
		if bytes.HasPrefix(line, []byte("event:")) {
			eventType = strings.TrimPrefix(string(line[6:]), " ")
		} else if bytes.HasPrefix(line, []byte("data:")) {
			if len(eventData) > 0 {
				eventData = append(eventData, '\n')
			}
			eventData = append(eventData, bytes.TrimPrefix(line[5:], []byte{' '})...)
		}
	}
	return nil, ErrMarketRemoteResult
}

func marketWireResult(raw []byte, expectedID string) (map[string]any, error) {
	var envelope map[string]any
	if marketDecodeExactJSON(raw, &envelope) != nil || envelope["jsonrpc"] != "2.0" || marketCanonical(envelope["id"]) != expectedID {
		return nil, ErrMarketRemoteResult
	}
	result, ok := envelope["result"].(map[string]any)
	if !ok || !marketExactJSONBounded(result, 0) {
		return nil, ErrMarketRemoteResult
	}
	return result, nil
}
