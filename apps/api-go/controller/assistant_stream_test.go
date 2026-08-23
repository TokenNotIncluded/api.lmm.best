package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/internal/agent"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantStreamSessionEmitsIncrementalSafeEvents(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	session := newAssistantStreamSession(context.Writer)

	require.NoError(t, session.start())
	require.NoError(t, session.appendContent("实时输出 "))
	require.NoError(t, session.appendContent("内容"))
	require.NoError(t, session.finish([]byte(`{"choices":[{"message":{"content":"实时输出 内容"}}]}`)))

	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Header().Get("Content-Type"), "text/event-stream")
	assert.Contains(t, recorder.Body.String(), "event: ready")
	assert.Contains(t, recorder.Body.String(), "event: delta")
	assert.Contains(t, recorder.Body.String(), "event: done")
	assert.Contains(t, recorder.Body.String(), "实时输出 内容")
}

func TestAssistantStreamingRelayWriterParsesSplitSSEChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	session := newAssistantStreamSession(context.Writer)
	require.NoError(t, session.start())

	writer := newAssistantStreamingRelayWriter(context.Writer, session)
	first := `data: {"choices":[{"delta":{"content":"hello "}}]}`
	second := "\n\ndata: {\"choices\":[{\"delta\":{\"content\":\"world\"}}]}\n\ndata: [DONE]\n\n"
	_, err := writer.Write([]byte(first[:len(first)-3]))
	require.NoError(t, err)
	_, err = writer.Write([]byte(first[len(first)-3:] + second))
	require.NoError(t, err)

	body, err := writer.responseBody()
	require.NoError(t, err)
	var parsed struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	require.NoError(t, json.Unmarshal(body, &parsed))
	assert.Equal(t, "hello world", parsed.Choices[0].Message.Content)
	assert.NotContains(t, recorder.Body.String(), `"content":"hello world"`)
}

func TestAssistantStreamingRelayWriterResetsFailedChannelBeforeRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	session := newAssistantStreamSession(context.Writer)
	require.NoError(t, session.start())

	writer := newAssistantStreamingRelayWriter(context.Writer, session)
	context.Writer = writer
	writer.Header().Set("X-Upstream-Attempt", "failed")
	writer.WriteHeader(http.StatusServiceUnavailable)
	_, err := writer.Write([]byte(`{"error":{"message":"stale channel failure"}}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusServiceUnavailable, writer.Status())
	require.True(t, writer.Written())

	require.NoError(t, resetRelayResponseForRetry(context))
	assert.Equal(t, http.StatusOK, writer.Status())
	assert.Equal(t, -1, writer.Size())
	assert.False(t, writer.Written())
	assert.Empty(t, writer.Header().Get("X-Upstream-Attempt"))

	writer.WriteHeader(http.StatusOK)
	_, err = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"recovered answer\"}}]}\n\ndata: [DONE]\n\n"))
	require.NoError(t, err)
	body, err := writer.responseBody()
	require.NoError(t, err)
	response, err := agent.Parse(body)
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	assert.Equal(t, "recovered answer", agent.Text(response.Choices[0].Message.Content))
	assert.NotContains(t, string(body), "stale channel failure")
}

func TestAssistantStreamingRelayWriterRetainsToolCallsWithoutLeakingAgentPlanning(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	session := newAssistantStreamSession(context.Writer)
	require.NoError(t, session.start())

	writer := newAssistantStreamingRelayWriter(context.Writer, session)
	_, err := writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"I will check that. \",\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"get_\",\"arguments\":\"{\\\"group\\\":\\\"def\"}}]}}]}\n\n"))
	require.NoError(t, err)
	_, err = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"name\":\"available_models\",\"arguments\":\"ault\\\"}\"}}]}}]}\n\ndata: [DONE]\n\n"))
	require.NoError(t, err)

	body, err := writer.responseBody()
	require.NoError(t, err)
	response, err := agent.Parse(body)
	require.NoError(t, err)
	require.Len(t, response.Choices, 1)
	require.Len(t, response.Choices[0].Message.ToolCalls, 1)
	call := response.Choices[0].Message.ToolCalls[0]
	assert.Equal(t, "call_1", call.ID)
	assert.Equal(t, "get_available_models", call.Function.Name)
	assert.Equal(t, `{"group":"default"}`, call.Function.Arguments)
	assert.NotContains(t, recorder.Body.String(), "I will check that.")
}

func TestAssistantStreamConfigurationControlsRelayAndResponseParameters(t *testing.T) {
	gin.SetMode(gin.TestMode)
	t.Run("streams the first final turn", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
		session := newAssistantStreamSession(context.Writer)
		require.NoError(t, session.start())
		context.Set(assistantStreamSessionKey, session)

		originalRelay := relayAssistantStreamTurn
		relayAssistantStreamTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int, stream *assistantStreamSession) (int, []byte, error) {
			assert.True(t, request.Stream)
			assert.Equal(t, 0.7, request.Temperature)
			assert.Equal(t, 1200, request.MaxTokens)
			require.NoError(t, stream.appendContent("streamed answer"))
			return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"streamed answer"}}]}`), nil
		}
		t.Cleanup(func() { relayAssistantStreamTurn = originalRelay })

		runAssistantAgent(context, setting.AssistantSettings{
			Model: "streaming-test-model", StreamEnabled: true, Temperature: 0.7, MaxTokens: 1200,
			TimeoutSeconds: 45,
		}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})

		assert.Contains(t, recorder.Body.String(), "event: delta")
		assert.Contains(t, recorder.Body.String(), "streamed answer")
		assert.Contains(t, recorder.Body.String(), "event: done")
	})

	t.Run("keeps the browser protocol stable when streaming is disabled", func(t *testing.T) {
		recorder := httptest.NewRecorder()
		context, _ := gin.CreateTestContext(recorder)
		context.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
		session := newAssistantStreamSession(context.Writer)
		require.NoError(t, session.start())
		context.Set(assistantStreamSessionKey, session)

		originalRelay := relayAssistantAgentTurn
		relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
			assert.False(t, request.Stream)
			assert.Equal(t, 0.1, request.Temperature)
			assert.Equal(t, 256, request.MaxTokens)
			return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"buffered answer"}}]}`), nil
		}
		t.Cleanup(func() { relayAssistantAgentTurn = originalRelay })

		runAssistantAgent(context, setting.AssistantSettings{
			Model: "buffered-test-model", StreamEnabled: false, Temperature: 0.1, MaxTokens: 256,
			TimeoutSeconds: 45,
		}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})

		assert.Contains(t, recorder.Body.String(), "event: delta")
		assert.Contains(t, recorder.Body.String(), "buffered answer")
		assert.Contains(t, recorder.Body.String(), "event: done")
	})
}

func TestAssistantStreamContentDoesNotExposeEmailAcrossChunks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	context, _ := gin.CreateTestContext(recorder)
	session := newAssistantStreamSession(context.Writer)
	require.NoError(t, session.start())
	require.NoError(t, session.appendContent("联系 alice@"))
	require.NoError(t, session.appendContent("example.test 获取帮助"))
	require.NoError(t, session.finish([]byte(`{"choices":[{"message":{"content":"联系 alice@example.test 获取帮助"}}]}`)))

	body := recorder.Body.String()
	assert.NotContains(t, body, "alice@example.test")
	assert.Contains(t, body, "[REDACTED_EMAIL]")
	assert.NotContains(t, strings.ToLower(body), "password")
}
