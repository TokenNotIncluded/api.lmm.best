package helper

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"
)

func FlushWriter(c *gin.Context) (err error) {
	defer func() {
		if r := recover(); r != nil {
			err = errors.New("stream flush failed")
		}
		if err != nil {
			markHTTPStreamDownstreamFailure(c)
		}
	}()

	if c == nil || c.Writer == nil {
		return nil
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	markHTTPStreamCommitted(c)
	c.Writer.WriteHeaderNow()
	writer := http.ResponseWriter(c.Writer)
	// Gin exposes Unwrap but its Flush discards an underlying FlushError.
	// Commit through Gin first, then let ResponseController observe the error.
	if unwrapped, ok := writer.(interface{ Unwrap() http.ResponseWriter }); ok {
		writer = unwrapped.Unwrap()
	}
	return http.NewResponseController(writer).Flush()
}

func hasHTTPStreamWriter(c *gin.Context) bool {
	if c == nil || c.Writer == nil {
		return false
	}
	if _, resettable := c.Writer.(interface{ ResetForRelayRetry() error }); resettable {
		return false
	}
	return c.GetBool("event_stream_headers_set")
}

func markHTTPStreamCommitted(c *gin.Context) {
	if hasHTTPStreamWriter(c) {
		common.SetContextKey(c, constant.ContextKeyHTTPStreamCommitted, true)
	}
}

func markHTTPStreamDownstreamFailure(c *gin.Context) {
	if hasHTTPStreamWriter(c) {
		common.SetContextKey(c, constant.ContextKeyHTTPStreamDownstreamFailure, true)
	}
}

// MarkHTTPStreamDownstreamFailure is also used by legacy handlers that write
// directly through Gin. It stops retries/provider penalties without changing
// those handlers' usage-collection or settlement rules.
func MarkHTTPStreamDownstreamFailure(c *gin.Context) { markHTTPStreamDownstreamFailure(c) }

func HTTPStreamDownstreamFailed(c *gin.Context) bool {
	return c != nil && common.GetContextKeyBool(c, constant.ContextKeyHTTPStreamDownstreamFailure)
}

// CommitEventStreamHeaders ends the HTTP attempt's pre-output retry window.
// Call it only after validating the upstream stream, and after retiring an
// enabled first-visible-output boundary. Internal resettable writers retain
// their existing retry behavior.
func CommitEventStreamHeaders(c *gin.Context) error {
	SetEventStreamHeaders(c)
	if _, resettable := c.Writer.(interface{ ResetForRelayRetry() error }); resettable {
		// Internal assistant writers must also retain their pre-body status
		// behavior; excluding retry flags alone would still lock them to 200.
		return nil
	}
	ExtendWriteDeadline(c)
	return FlushWriter(c)
}

func requestContextDone(c *gin.Context) bool {
	return c != nil && c.Request != nil && c.Request.Context().Err() != nil
}

func SetEventStreamHeaders(c *gin.Context) {
	// 检查是否已经设置过头部
	if c.GetBool("event_stream_headers_set") {
		return
	}

	// 设置标志，表示头部已经设置过
	c.Set("event_stream_headers_set", true)

	c.Writer.Header().Set("Content-Type", "text/event-stream")
	c.Writer.Header().Set("Cache-Control", "no-cache, no-transform")
	c.Writer.Header().Set("Connection", "keep-alive")
	c.Writer.Header().Set("Transfer-Encoding", "chunked")
	c.Writer.Header().Set("X-Accel-Buffering", "no")
}

func ClaudeData(c *gin.Context, resp dto.ClaudeResponse) error {
	if requestContextDone(c) {
		return nil
	}

	jsonData, err := common.Marshal(resp)
	if err != nil {
		common.SysError("error marshalling stream response: " + err.Error())
	} else {
		if err := renderSSEEvent(c, fmt.Sprintf("event: %s\n", resp.Type)); err != nil {
			return err
		}
		if err := renderSSEDataLines(c, string(jsonData)); err != nil {
			return err
		}
	}
	return FlushWriter(c)
}

func ClaudeChunkData(c *gin.Context, resp dto.ClaudeResponse, data string) {
	if requestContextDone(c) {
		return
	}

	if err := renderSSEEvent(c, fmt.Sprintf("event: %s\n", resp.Type)); err != nil {
		return
	}
	if err := renderSSEDataLines(c, data); err != nil {
		return
	}
	_ = FlushWriter(c)
}

func ResponseChunkData(c *gin.Context, resp dto.ResponsesStreamResponse, data string) error {
	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	if err := renderSSEEvent(c, fmt.Sprintf("event: %s\n", resp.Type)); err != nil {
		return err
	}
	if err := renderSSEDataLines(c, data); err != nil {
		return err
	}
	return FlushWriter(c)
}

// renderSSEDataLines encodes embedded newlines as multiple data fields in the
// same SSE event. CustomEvent appends the event delimiter once, so the joined
// fields are rendered as one event rather than one event per source line.
func renderSSEEvent(c *gin.Context, data string) error {
	if HTTPStreamDownstreamFailed(c) {
		return errors.New("downstream stream write failed")
	}
	markHTTPStreamCommitted(c)
	err := (common.CustomEvent{Data: data}).Render(c.Writer)
	if err != nil {
		markHTTPStreamDownstreamFailure(c)
	}
	return err
}

func renderSSEDataLines(c *gin.Context, data string) error {
	normalized := strings.ReplaceAll(data, "\r\n", "\n")
	normalized = strings.ReplaceAll(normalized, "\r", "\n")
	lines := strings.Split(normalized, "\n")
	var encoded strings.Builder
	for i, line := range lines {
		if i > 0 {
			encoded.WriteByte('\n')
		}
		encoded.WriteString("data: ")
		encoded.WriteString(line)
	}
	return renderSSEEvent(c, encoded.String())
}

func StringData(c *gin.Context, str string) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	if err := renderSSEDataLines(c, str); err != nil {
		return err
	}
	return FlushWriter(c)
}

func PingData(c *gin.Context) error {
	if c == nil || c.Writer == nil {
		return errors.New("context or writer is nil")
	}
	if HTTPStreamDownstreamFailed(c) {
		return errors.New("downstream stream write failed")
	}

	if requestContextDone(c) {
		return fmt.Errorf("request context done: %w", c.Request.Context().Err())
	}

	markHTTPStreamCommitted(c)
	if _, err := c.Writer.Write([]byte(": PING\n\n")); err != nil {
		markHTTPStreamDownstreamFailure(c)
		return fmt.Errorf("write ping data failed: %w", err)
	}
	return FlushWriter(c)
}

func ObjectData(c *gin.Context, object interface{}) error {
	if object == nil {
		return errors.New("object is nil")
	}
	jsonData, err := common.Marshal(object)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	return StringData(c, string(jsonData))
}

func Done(c *gin.Context) {
	_ = StringData(c, "[DONE]")
}

func WssString(c *gin.Context, ws *websocket.Conn, str string) error {
	if ws == nil {
		logger.LogError(c, "websocket connection is nil")
		return errors.New("websocket connection is nil")
	}
	//common.LogInfo(c, fmt.Sprintf("sending message: %s", str))
	return ws.WriteMessage(1, []byte(str))
}

func WssObject(c *gin.Context, ws *websocket.Conn, object interface{}) error {
	jsonData, err := common.Marshal(object)
	if err != nil {
		return fmt.Errorf("error marshalling object: %w", err)
	}
	if ws == nil {
		logger.LogError(c, "websocket connection is nil")
		return errors.New("websocket connection is nil")
	}
	//common.LogInfo(c, fmt.Sprintf("sending message: %s", jsonData))
	return ws.WriteMessage(1, jsonData)
}

func WssError(c *gin.Context, ws *websocket.Conn, openaiError types.OpenAIError) {
	if ws == nil {
		return
	}
	errorObj := &dto.RealtimeEvent{
		Type:    "error",
		EventId: GetLocalRealtimeID(c),
		Error:   &openaiError,
	}
	_ = WssObject(c, ws, errorObj)
}

func GetResponseID(c *gin.Context) string {
	logID := c.GetString(common.RequestIdKey)
	return fmt.Sprintf("chatcmpl-%s", logID)
}

func GetLocalRealtimeID(c *gin.Context) string {
	logID := c.GetString(common.RequestIdKey)
	return fmt.Sprintf("evt_%s", logID)
}

func GenerateStartEmptyResponse(id string, createAt int64, model string, systemFingerprint *string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: systemFingerprint,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				Delta: dto.ChatCompletionsStreamResponseChoiceDelta{
					Role:    "assistant",
					Content: common.GetPointer(""),
				},
			},
		},
	}
}

func GenerateStopResponse(id string, createAt int64, model string, finishReason string) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: nil,
		Choices: []dto.ChatCompletionsStreamResponseChoice{
			{
				FinishReason: &finishReason,
			},
		},
	}
}

func GenerateFinalUsageResponse(id string, createAt int64, model string, usage dto.Usage) *dto.ChatCompletionsStreamResponse {
	return &dto.ChatCompletionsStreamResponse{
		Id:                id,
		Object:            "chat.completion.chunk",
		Created:           createAt,
		Model:             model,
		SystemFingerprint: nil,
		Choices:           make([]dto.ChatCompletionsStreamResponseChoice, 0),
		Usage:             &usage,
	}
}
