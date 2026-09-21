package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func assistantLoopTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0"})
	return c, recorder
}

func assistantLoopCallBody(t *testing.T, calls []assistantOpenAIToolCall, content string) []byte {
	t.Helper()
	rawContent, err := json.Marshal(content)
	require.NoError(t, err)
	body, err := json.Marshal(assistantOpenAIResponse{Choices: []assistantOpenAIResponseChoice{{Message: assistantOpenAIResponseMessage{ToolCalls: calls, Content: rawContent}}}})
	require.NoError(t, err)
	return body
}

func assistantLoopMathCall(id string, number int) assistantOpenAIToolCall {
	return assistantOpenAIToolCall{ID: id, Type: "function", Function: assistantOpenAIToolCallFunction{Name: "calculate_math", Arguments: fmt.Sprintf(`{"expression":"%d+1"}`, number)}}
}

func TestAssistantAgentReportsEveryOverflowCallAndContinuesNextRound(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		switch turns {
		case 1:
			calls := make([]assistantOpenAIToolCall, 6)
			for index := range calls {
				calls[index] = assistantLoopMathCall("duplicate", index)
			}
			return http.StatusOK, assistantLoopCallBody(t, calls, ""), nil
		case 2:
			message := request.Messages[len(request.Messages)-7]
			require.Len(t, message.ToolCalls, 6)
			ids := map[string]bool{}
			for index, call := range message.ToolCalls {
				assert.False(t, ids[call.ID])
				ids[call.ID] = true
				result := request.Messages[len(request.Messages)-6+index]
				assert.Equal(t, call.ID, result.ToolCallID)
				if index < assistantToolCallsPerTurn {
					assert.Contains(t, result.Content, `"ok":true`)
				} else {
					assert.Contains(t, result.Content, `"status":"tool_batch_limit"`)
				}
			}
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("duplicate", 4), assistantLoopMathCall("", 5)}, ""), nil
		default:
			for _, result := range request.Messages[len(request.Messages)-2:] {
				assert.Contains(t, result.Content, `"ok":true`)
			}
			return http.StatusOK, assistantLoopCallBody(t, nil, "All six calculations completed."), nil
		}
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 4}, []assistantOpenAIMessage{{Role: "user", Content: "Compute these values"}})
	assert.Equal(t, 3, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "All six calculations completed.")
}

func TestAssistantAgentStopsRepeatedBatchesWithOneFinalAnswerTurn(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns < 4 {
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("same", 1)}, ""), nil
		}
		assert.Empty(t, request.Tools)
		assert.Contains(t, request.Messages[len(request.Messages)-1].Content, `"status":"tool_repetition_limit"`)
		return http.StatusOK, assistantLoopCallBody(t, nil, "The verified result is 2."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 10}, []assistantOpenAIMessage{{Role: "user", Content: "Compute"}})
	assert.Equal(t, 4, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestAssistantAgentCompactsGrowingContextBetweenToolRounds(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	latest := strings.Repeat("current requirement ", 5000)
	old := strings.Repeat("old conversation ", 10000)
	newContent := strings.Repeat("latest model reasoning ", 6500)
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns == 1 {
			assert.Contains(t, request.Messages, assistantOpenAIMessage{Role: "assistant", Content: old})
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("fresh", 1)}, newContent), nil
		}
		assert.LessOrEqual(t, assistantContextBytes(request.Messages), assistantAgentContextTargetBytes)
		assert.Contains(t, request.Messages, assistantOpenAIMessage{Role: "user", Content: latest})
		assert.NotContains(t, request.Messages, assistantOpenAIMessage{Role: "assistant", Content: old})
		lastCall := request.Messages[len(request.Messages)-2]
		assert.Equal(t, newContent, lastCall.Content)
		require.Len(t, lastCall.ToolCalls, 1)
		assert.Equal(t, "fresh", lastCall.ToolCalls[0].ID)
		assert.Equal(t, "fresh", request.Messages[len(request.Messages)-1].ToolCallID)
		assert.Contains(t, request.Messages[len(request.Messages)-1].Content, `"result":2`)
		return http.StatusOK, assistantLoopCallBody(t, nil, "Verified."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 3}, []assistantOpenAIMessage{{Role: "assistant", Content: old}, {Role: "user", Content: latest}})
	assert.Equal(t, 2, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestAssistantAgentRecoversFromSmallerProviderContextWindow(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	old := strings.Repeat("older conversation ", 5000)
	turns := 0
	initialBytes := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, step int) (int, []byte, error) {
		turns++
		assert.Zero(t, step, "context recovery does not consume a successful task step")
		if turns == 1 {
			initialBytes = assistantContextBytes(request.Messages)
			return http.StatusBadRequest, []byte(`{"error":{"code":"context_length_exceeded"}}`), nil
		}
		assert.Less(t, assistantContextBytes(request.Messages), initialBytes*2/3)
		assert.Equal(t, assistantOpenAIMessage{Role: "user", Content: "current task"}, request.Messages[len(request.Messages)-1])
		return http.StatusOK, assistantLoopCallBody(t, nil, "Recovered."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1}, []assistantOpenAIMessage{{Role: "assistant", Content: old}, {Role: "user", Content: "current task"}})
	assert.Equal(t, 2, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestAssistantAgentCancellationPreventsReturnedToolExecution(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0", ConversationTitleNeeded: true})
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, _ assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		cancel()
		return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{{ID: "title", Function: assistantOpenAIToolCallFunction{Name: "set_conversation_title", Arguments: `{"title":"Cancelled title"}`}}}, ""), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 3}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})
	assert.Empty(t, c.GetString(assistantConversationTitleDraftKey))
	assert.Equal(t, http.StatusRequestTimeout, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ASSISTANT_REQUEST_CANCELLED")
}

func TestAssistantAgentRetriesStreamingFailuresWithoutDuplicateOutput(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	c.Set(assistantStreamSessionKey, session)
	turns := 0
	original := relayAssistantStreamTurn
	relayAssistantStreamTurn = func(_ *gin.Context, _ assistantOpenAIRequest, _ string, _ int, stream *assistantStreamSession) (int, []byte, error) {
		turns++
		if turns == 1 {
			require.NoError(t, stream.appendContent(strings.Repeat("tentative ", 12)))
			return http.StatusServiceUnavailable, []byte(`{"error":"unavailable"}`), nil
		}
		assert.Empty(t, stream.safeContent())
		require.NoError(t, stream.appendContent("Recovered answer."))
		return http.StatusOK, assistantLoopCallBody(t, nil, "Recovered answer."), nil
	}
	t.Cleanup(func() { relayAssistantStreamTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 2, StreamEnabled: true}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})
	assert.Equal(t, 2, turns)
	assert.Equal(t, "Recovered answer.", session.safeContent())
	assert.Contains(t, recorder.Body.String(), "event: replace")
	assert.Contains(t, recorder.Body.String(), "event: done")
}

func TestAssistantAgentOversizedResultPreservesSuccessfulMutationReceipt(t *testing.T) {
	encoded := assistantAgentToolResultJSON(map[string]any{"ok": true, "applied": true, "status": "applied", "data": strings.Repeat("x", assistantToolResultMaxBytes+1)})
	var result map[string]any
	require.NoError(t, json.Unmarshal(encoded, &result))
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, true, result["applied"])
	assert.Equal(t, "applied", result["status"])
	assert.Equal(t, true, result["context_compacted"])
}

func TestAssistantAgentRetriedAdminRequestRefusesWritesAndKeepsReads(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	c, _, _ := assistantAutomationTestContext(t, db, common.RoleRootUser)
	c.Request.Header.Set(assistantAttemptHeader, "2")
	result := executeAssistantTool(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{
		Name: "prepare_admin_config_change", Arguments: `{"changes":{"SystemName":"must not replay"}}`,
	}})
	assert.Equal(t, "retry_requires_verification", result["status"])
	assert.Equal(t, true, result["do_not_retry"])
	var count int64
	require.NoError(t, db.Model(&model.Option{}).Count(&count).Error)
	assert.Zero(t, count)
	assert.False(t, assistantAdminRetryMutationBlocked(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "get_admin_server_config"}}))
	assert.False(t, assistantAdminRetryMutationBlocked(c, assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: "audit_admin_model_pricing"}}))
}

func TestAssistantAgentEnforcesHardStepLimit(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if len(request.Tools) > 0 {
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("call", turns)}, ""), nil
		}
		return http.StatusOK, assistantLoopCallBody(t, nil, "Remaining work requires another request."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 999}, []assistantOpenAIMessage{{Role: "user", Content: "Compute"}})
	assert.Equal(t, assistantAgentMaxSteps, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}
