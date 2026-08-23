package controller

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"unicode"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/internal/agent"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
)

const (
	assistantStreamSessionKey     = "assistant_stream_session"
	assistantFinalResponseBodyKey = "assistant_final_response_body"
	assistantStreamSafetyHoldback = 32
)

// assistantStreamSession is the browser-facing SSE boundary. The upstream
// relay may expose provider-specific fields, but this session only emits
// bounded natural-language deltas and one normalized final response.
type assistantStreamSession struct {
	writer      gin.ResponseWriter
	mu          sync.Mutex
	started     bool
	finished    bool
	rawContent  strings.Builder
	emittedSafe string
}

func newAssistantStreamSession(writer gin.ResponseWriter) *assistantStreamSession {
	return &assistantStreamSession{writer: writer}
}

func (s *assistantStreamSession) start() error {
	if s == nil || s.writer == nil {
		return fmt.Errorf("assistant stream writer is unavailable")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.started {
		return nil
	}

	s.writer.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
	s.writer.Header().Set("Cache-Control", "no-cache, no-transform")
	s.writer.Header().Set("Connection", "keep-alive")
	s.writer.Header().Set("X-Accel-Buffering", "no")
	s.writer.WriteHeader(http.StatusOK)
	s.started = true
	return s.writeJSONEventLocked("ready", map[string]string{"type": "ready"})
}

func (s *assistantStreamSession) appendContent(delta string) error {
	if s == nil || delta == "" {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.finished {
		return nil
	}
	s.rawContent.WriteString(delta)
	return s.emitStableContentLocked(false)
}

// resetContent removes any tentative prose if an upstream stream turns into a
// tool call. Models normally emit no prose before a tool call, but a provider
// is allowed to do so. Clearing it keeps intermediate agent planning out of
// the browser-facing answer.
func (s *assistantStreamSession) resetContent() error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.finished {
		return nil
	}
	s.rawContent.Reset()
	if s.emittedSafe == "" {
		return nil
	}
	if err := s.writeJSONEventLocked("replace", map[string]string{"content": ""}); err != nil {
		return err
	}
	s.emittedSafe = ""
	return nil
}

func (s *assistantStreamSession) finish(body []byte) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.finished {
		return nil
	}
	if err := s.emitStableContentLocked(true); err != nil {
		return err
	}
	safeBody := sanitizeAssistantStreamResponseBody(body, s.emittedSafe)
	if err := s.writeRawEventLocked("done", safeBody); err != nil {
		return err
	}
	s.finished = true
	return nil
}

func (s *assistantStreamSession) fail(status int, code, message string) error {
	if s == nil {
		return nil
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.started || s.finished {
		return nil
	}
	if status < http.StatusBadRequest {
		status = http.StatusBadGateway
	}
	err := s.writeJSONEventLocked("error", map[string]any{
		"success":   false,
		"code":      code,
		"message":   message,
		"status":    status,
		"retryable": status == http.StatusRequestTimeout || status == http.StatusTooEarly || status == http.StatusTooManyRequests || status >= http.StatusInternalServerError,
	})
	s.finished = true
	return err
}

func (s *assistantStreamSession) startedAndFinished() (bool, bool) {
	if s == nil {
		return false, true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.started, s.finished
}

func (s *assistantStreamSession) safeContent() string {
	if s == nil {
		return ""
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return redactAssistantStreamContent(s.rawContent.String())
}

func (s *assistantStreamSession) writeJSONEventLocked(event string, payload any) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return s.writeRawEventLocked(event, data)
}

func (s *assistantStreamSession) writeRawEventLocked(event string, data []byte) error {
	if event == "" {
		return fmt.Errorf("assistant stream event name is empty")
	}
	if len(data) == 0 {
		data = []byte("{}")
	}
	if _, err := fmt.Fprintf(s.writer, "event: %s\ndata: %s\n\n", event, data); err != nil {
		return err
	}
	s.writer.Flush()
	return nil
}

func (s *assistantStreamSession) emitStableContentLocked(final bool) error {
	safe := redactAssistantStreamContent(s.rawContent.String())
	stable := safe
	if !final {
		runes := []rune(stable)
		if len(runes) > assistantStreamSafetyHoldback {
			stable = string(runes[:len(runes)-assistantStreamSafetyHoldback])
		} else {
			stable = ""
		}
	}

	if strings.HasPrefix(stable, s.emittedSafe) {
		delta := strings.TrimPrefix(stable, s.emittedSafe)
		if delta != "" {
			if err := s.writeJSONEventLocked("delta", map[string]string{"content": delta}); err != nil {
				return err
			}
			s.emittedSafe = stable
		}
		return nil
	}

	// A redaction can change the already-computed prefix only when a sensitive
	// value was split across provider chunks. Replace the browser buffer rather
	// than leaking the old prefix or duplicating the new one.
	if err := s.writeJSONEventLocked("replace", map[string]string{"content": stable}); err != nil {
		return err
	}
	s.emittedSafe = stable
	return nil
}

func redactAssistantStreamContent(value string) string {
	if value == "" {
		return ""
	}
	leading := value[:len(value)-len(strings.TrimLeftFunc(value, unicode.IsSpace))]
	trimmed := strings.TrimSpace(value)
	trimmedRight := strings.TrimRightFunc(value, unicode.IsSpace)
	trailing := value[len(trimmedRight):]
	if trimmed == "" {
		return value
	}
	return leading + model.RedactAssistantHistoryContent(trimmed) + trailing
}

func sanitizeAssistantStreamResponseBody(body []byte, safeContent string) []byte {
	if len(body) == 0 {
		return body
	}
	var payload map[string]any
	if json.Unmarshal(body, &payload) != nil {
		return body
	}
	choices, ok := payload["choices"].([]any)
	if !ok || len(choices) == 0 {
		return body
	}
	choice, ok := choices[0].(map[string]any)
	if !ok {
		return body
	}
	message, ok := choice["message"].(map[string]any)
	if !ok {
		return body
	}
	message["content"] = safeContent
	encoded, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return encoded
}

func assistantStreamSessionFrom(c *gin.Context) *assistantStreamSession {
	if c == nil {
		return nil
	}
	value, exists := c.Get(assistantStreamSessionKey)
	if !exists {
		return nil
	}
	session, _ := value.(*assistantStreamSession)
	return session
}

func assistantWantsStream(c *gin.Context) bool {
	if c == nil {
		return false
	}
	return strings.Contains(strings.ToLower(c.GetHeader("Accept")), "text/event-stream")
}

// assistantSSEDecoder accepts both the relay's data-only events and standard
// SSE events. Writes from an HTTP response are not guaranteed to align with
// event boundaries, so parsing is deliberately byte-based and incremental.
type assistantSSEDecoder struct {
	buffer bytes.Buffer
	data   bytes.Buffer
}

func (d *assistantSSEDecoder) feed(input []byte, dispatch func(string)) {
	if d == nil || len(input) == 0 {
		return
	}
	d.buffer.Write(input)
	for {
		line, ok := readAssistantSSELine(&d.buffer)
		if !ok {
			return
		}
		if len(line) == 0 {
			if d.data.Len() > 0 {
				dispatch(strings.TrimSuffix(d.data.String(), "\n"))
				d.data.Reset()
			}
			continue
		}
		if bytes.HasPrefix(line, []byte("data:")) {
			value := line[len("data:"):]
			if len(value) > 0 && value[0] == ' ' {
				value = value[1:]
			}
			d.data.Write(value)
			d.data.WriteByte('\n')
		}
	}
}

func (d *assistantSSEDecoder) flush(dispatch func(string)) {
	if d == nil || d.data.Len() == 0 {
		return
	}
	dispatch(strings.TrimSuffix(d.data.String(), "\n"))
	d.data.Reset()
}

func readAssistantSSELine(buffer *bytes.Buffer) ([]byte, bool) {
	value := buffer.Bytes()
	index := bytes.IndexByte(value, '\n')
	if index < 0 {
		return nil, false
	}
	line := append([]byte(nil), value[:index]...)
	buffer.Next(index + 1)
	return bytes.TrimSuffix(line, []byte{'\r'}), true
}

type assistantChatStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content   json.RawMessage `json:"content"`
			ToolCalls []struct {
				Index    int    `json:"index"`
				ID       string `json:"id"`
				Type     string `json:"type"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"delta"`
	} `json:"choices"`
}

type assistantStreamingRelayWriter struct {
	gin.ResponseWriter
	header       http.Header
	body         *common.LimitBuffer
	status       int
	wroteHeader  bool
	writeErr     error
	decoder      assistantSSEDecoder
	session      *assistantStreamSession
	content      strings.Builder
	toolCalls    map[int]agent.Call
	toolCallSeen bool
}

func mergeAssistantStreamFragment(current, next string) string {
	if next == "" || strings.HasPrefix(current, next) {
		return current
	}
	if current == "" || strings.HasPrefix(next, current) {
		return next
	}
	return current + next
}

func newAssistantStreamingRelayWriter(writer gin.ResponseWriter, session *assistantStreamSession) *assistantStreamingRelayWriter {
	return &assistantStreamingRelayWriter{
		ResponseWriter: writer,
		header:         make(http.Header),
		body:           common.NewLimitBuffer(assistantUpstreamResponseMaxBytes),
		session:        session,
		toolCalls:      make(map[int]agent.Call),
	}
}

func (r *assistantStreamingRelayWriter) Header() http.Header {
	return r.header
}

func (r *assistantStreamingRelayWriter) WriteHeader(statusCode int) {
	if statusCode <= 0 {
		// gin.Context.Render uses -1 to write content without changing status.
		return
	}
	if r.wroteHeader {
		return
	}
	r.status = statusCode
	r.wroteHeader = true
}

func (r *assistantStreamingRelayWriter) WriteHeaderNow() {
	if !r.wroteHeader {
		r.WriteHeader(http.StatusOK)
	}
}

func (r *assistantStreamingRelayWriter) Write(data []byte) (int, error) {
	r.WriteHeaderNow()
	if r.writeErr != nil {
		return len(data), nil
	}
	if _, err := r.body.Write(data); err != nil {
		r.writeErr = err
		return len(data), nil
	}
	r.decoder.feed(data, r.handleData)
	return len(data), nil
}

func (r *assistantStreamingRelayWriter) WriteString(data string) (int, error) {
	return r.Write([]byte(data))
}

func (r *assistantStreamingRelayWriter) Flush() {
	r.WriteHeaderNow()
}

func (r *assistantStreamingRelayWriter) Status() int {
	if !r.wroteHeader {
		return http.StatusOK
	}
	return r.status
}

func (r *assistantStreamingRelayWriter) Size() int {
	if !r.wroteHeader {
		return -1
	}
	return r.body.Len()
}

func (r *assistantStreamingRelayWriter) Written() bool {
	return r.wroteHeader
}

func (r *assistantStreamingRelayWriter) ResetForRelayRetry() error {
	sessionErr := r.session.resetContent()
	clear(r.header)
	r.body = common.NewLimitBuffer(assistantUpstreamResponseMaxBytes)
	r.status = 0
	r.wroteHeader = false
	r.writeErr = nil
	r.decoder = assistantSSEDecoder{}
	r.content.Reset()
	clear(r.toolCalls)
	r.toolCallSeen = false
	return sessionErr
}

func (r *assistantStreamingRelayWriter) handleData(data string) {
	data = strings.TrimSpace(data)
	if data == "" || data == "[DONE]" {
		return
	}
	var chunk assistantChatStreamChunk
	if json.Unmarshal([]byte(data), &chunk) != nil {
		return
	}
	for _, choice := range chunk.Choices {
		if len(choice.Delta.ToolCalls) > 0 {
			if !r.toolCallSeen {
				r.toolCallSeen = true
				if err := r.session.resetContent(); err != nil && r.writeErr == nil {
					r.writeErr = err
				}
			}
			for _, streamedCall := range choice.Delta.ToolCalls {
				call := r.toolCalls[streamedCall.Index]
				if streamedCall.ID != "" {
					call.ID = streamedCall.ID
				}
				if streamedCall.Type != "" {
					call.Type = streamedCall.Type
				}
				if streamedCall.Function.Name != "" {
					call.Function.Name = mergeAssistantStreamFragment(call.Function.Name, streamedCall.Function.Name)
				}
				call.Function.Arguments = mergeAssistantStreamFragment(call.Function.Arguments, streamedCall.Function.Arguments)
				r.toolCalls[streamedCall.Index] = call
			}
		}
		content := agent.Text(choice.Delta.Content)
		if content == "" || r.toolCallSeen {
			continue
		}
		r.content.WriteString(content)
		if err := r.session.appendContent(content); err != nil && r.writeErr == nil {
			r.writeErr = err
		}
	}
}

func (r *assistantStreamingRelayWriter) responseBody() ([]byte, error) {
	r.decoder.flush(r.handleData)
	if !r.toolCallSeen && r.content.Len() == 0 && r.body.Len() > 0 {
		if response, err := agent.Parse(r.body.Bytes()); err == nil && len(response.Choices) > 0 {
			content := agent.Text(response.Choices[0].Message.Content)
			if content != "" {
				r.content.WriteString(content)
				if err := r.session.appendContent(content); err != nil && r.writeErr == nil {
					r.writeErr = err
				}
			}
		}
	}
	message := map[string]any{
		"role":    "assistant",
		"content": r.content.String(),
	}
	if len(r.toolCalls) > 0 {
		indexes := make([]int, 0, len(r.toolCalls))
		for index := range r.toolCalls {
			indexes = append(indexes, index)
		}
		sort.Ints(indexes)
		toolCalls := make([]agent.Call, 0, len(indexes))
		for _, index := range indexes {
			toolCalls = append(toolCalls, r.toolCalls[index])
		}
		message["tool_calls"] = toolCalls
	}
	return json.Marshal(map[string]any{
		"choices": []any{map[string]any{
			"message": message,
		}},
	})
}
