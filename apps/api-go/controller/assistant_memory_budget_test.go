package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantRelayRecorderHasHardByteBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	recorder := newAssistantRelayRecorder(context.Writer)
	written, err := recorder.WriteString(strings.Repeat("x", assistantUpstreamResponseMaxBytes+1))
	require.NoError(t, err, "the recorder must contain overflow without destabilizing the relay writer")
	assert.Equal(t, assistantUpstreamResponseMaxBytes+1, written)
	assert.ErrorIs(t, recorder.writeErr, common.ErrLimitExceeded)
	assert.LessOrEqual(t, recorder.body.Len(), assistantUpstreamResponseMaxBytes)
}

func TestAssistantContextBudgetIncludesToolArguments(t *testing.T) {
	messages := []assistantOpenAIMessage{{
		Role: "assistant",
		ToolCalls: []assistantOpenAIToolCall{{
			ID: "call", Type: "function",
			Function: assistantOpenAIToolCallFunction{Name: "tool", Arguments: strings.Repeat("x", 128)},
		}},
	}}
	assert.GreaterOrEqual(t, assistantContextBytes(messages), 128)
}

func TestAssistantRelayRequestHasSerializedByteBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	originalBody := context.Request.Body
	request := assistantOpenAIRequest{
		Model:    "test",
		Messages: []assistantOpenAIMessage{{Role: "user", Content: strings.Repeat("x", assistantUpstreamRequestMaxBytes)}},
	}

	err := setAssistantRelayRequest(context, request)
	assert.True(t, errors.Is(err, common.ErrLimitExceeded))
	assert.Equal(t, originalBody, context.Request.Body)
}

func TestAssistantRelayRequestSetsUpstreamResponseBudget(t *testing.T) {
	gin.SetMode(gin.TestMode)
	context, _ := gin.CreateTestContext(httptest.NewRecorder())
	context.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	err := setAssistantRelayRequest(context, assistantOpenAIRequest{
		Model:    "test",
		Messages: []assistantOpenAIMessage{{Role: "user", Content: "hello"}},
	})
	require.NoError(t, err)
	assert.Equal(t, assistantUpstreamResponseMaxBytes, common.GetContextKeyInt(context, constant.ContextKeyResponseByteLimit))
	assert.Equal(t, "test", common.GetContextKeyString(context, constant.ContextKeyOriginalModel))
	assert.Equal(t, "test", relaycommon.GenRelayInfoOpenAI(context, nil).OriginModelName,
		"billing must see the configured model even without distributor middleware")
}

func TestAssistantClientResponseKeepsNormalizedBodyWithinBudget(t *testing.T) {
	// '<' is valid unescaped JSON input, but encoding/json expands it to
	// "\\u003c" during normalization. The input fits the upstream budget while
	// the normalized output would exceed it without the final boundary check.
	body := []byte(`{"choices":[{"message":{"content":"` +
		strings.Repeat("<", assistantUpstreamResponseMaxBytes/4) +
		`"}}]}`)
	assert.Less(t, len(body), assistantUpstreamResponseMaxBytes)

	normalized, err := normalizeAssistantClientResponse(nil, body)
	assert.Nil(t, normalized)
	assert.ErrorIs(t, err, common.ErrLimitExceeded)
}
