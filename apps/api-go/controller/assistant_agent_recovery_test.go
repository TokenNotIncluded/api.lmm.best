package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantAgentNewConversationStartsWithTask(t *testing.T) {
	for _, test := range []struct {
		name      string
		message   string
		needsRead bool
	}{
		{name: "plain answer", message: "hello"},
		{name: "live facts before first model request", message: "What is the Base URL?", needsRead: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			c, recorder := assistantLoopTestContext(t)
			c.Set(assistantUserContextKey, assistantUserContext{
				AccessLevel: "L0", ConversationTitleNeeded: true, LatestUserRequest: test.message,
			})
			c.Set("assistant_history_latest_message", test.message)
			c.Set(assistantPromptKey, "stale prepared title-generation prompt")
			turns := 0
			originalRelay := relayAssistantAgentTurn
			relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
				turns++
				assert.NotContains(t, request.Messages[0].Content, "stale prepared title-generation prompt")
				assert.Empty(t, request.Tools)
				assert.Empty(t, assistantNamedToolChoiceName(request.ToolChoice))
				if test.needsRead {
					require.Len(t, request.Messages, 4)
					callMessage := request.Messages[2]
					require.Len(t, callMessage.ToolCalls, 1)
					call := callMessage.ToolCalls[0]
					assert.Equal(t, "get_service_facts", call.Function.Name)
					assert.JSONEq(t, "{}", call.Function.Arguments)
					result := request.Messages[3]
					assert.Equal(t, "tool", result.Role)
					assert.Equal(t, call.ID, result.ToolCallID)
					assert.Contains(t, result.Content, `"ok":true`)
					assert.Contains(t, result.Content, `"openai_base_url"`)
				} else {
					assert.Len(t, request.Messages, 2)
				}
				return http.StatusOK, assistantLoopCallBody(t, nil, "useful answer"), nil
			}
			t.Cleanup(func() { relayAssistantAgentTurn = originalRelay })

			runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1, AgentLoopEnabled: false}, []assistantOpenAIMessage{{Role: "user", Content: test.message}})

			assert.Equal(t, 1, turns, "optional titles and known reads must not add model requests")
			assert.False(t, assistantUserContextFromGin(c).ConversationTitleNeeded)
			if rawTraces, exists := c.Get(assistantClientToolsKey); exists {
				for _, trace := range rawTraces.([]assistantToolTrace) {
					assert.NotEqual(t, "set_conversation_title", trace.Name)
				}
			}
			assert.Equal(t, http.StatusOK, recorder.Code)
			assert.Contains(t, recorder.Body.String(), "useful answer")
		})
	}
}

func TestAssistantAgentToolResultsMatchRepairedCallIDs(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0"})

	turns := 0
	originalRelay := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns == 1 {
			calls := []assistantOpenAIToolCall{}
			for _, id := range []string{"", " padded-call "} {
				calls = append(calls, assistantOpenAIToolCall{
					ID: id, Type: "function", Function: assistantOpenAIToolCallFunction{Name: "calculate_math", Arguments: `{"expression":"1+1"}`},
				})
			}
			body, err := json.Marshal(assistantOpenAIResponse{Choices: []assistantOpenAIResponseChoice{{Message: assistantOpenAIResponseMessage{ToolCalls: calls}}}})
			require.NoError(t, err)
			return http.StatusOK, body, nil
		}
		require.Len(t, request.Messages, 5)
		calls := request.Messages[2].ToolCalls
		require.Len(t, calls, 2)
		assert.NotEmpty(t, calls[0].ID)
		assert.Equal(t, "padded-call", calls[1].ID)
		assert.NotEqual(t, calls[0].ID, calls[1].ID)
		for index, call := range calls {
			result := request.Messages[3+index]
			assert.Equal(t, call.ID, result.ToolCallID)
			assert.Contains(t, result.Content, `"ok":true`)
		}
		return http.StatusOK, []byte(`{"choices":[{"message":{"content":"2"}}]}`), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = originalRelay })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 2}, []assistantOpenAIMessage{{Role: "user", Content: "calculate"}})
	assert.Equal(t, 2, turns)
	assert.Equal(t, http.StatusOK, recorder.Code)
}

func TestAssistantAgentTitleFreeMathStillRequiresArgumentsAndAnswer(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	c.Set(assistantUserContextKey, assistantUserContext{
		AccessLevel: "L0", ConversationTitleNeeded: true, Intent: model.AssistantIntentMath,
		LatestUserRequest: "Calculate 1 + 1",
	})
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	c.Set(assistantStreamSessionKey, session)

	turns := 0
	originalRelay := relayAssistantStreamTurn
	relayAssistantStreamTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int, stream *assistantStreamSession) (int, []byte, error) {
		turns++
		if turns == 1 {
			assert.Equal(t, "calculate_math", assistantNamedToolChoiceName(request.ToolChoice))
			return http.StatusOK, assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("math", 1)}, ""), nil
		}
		assert.Empty(t, request.Tools)
		assert.Contains(t, request.Messages[len(request.Messages)-1].Content, `"result":2`)
		require.NoError(t, stream.appendContent("2"))
		return http.StatusOK, assistantLoopCallBody(t, nil, "2"), nil
	}
	t.Cleanup(func() { relayAssistantStreamTurn = originalRelay })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: false, MaxSteps: 1, StreamEnabled: true}, []assistantOpenAIMessage{{Role: "user", Content: "Calculate 1 + 1"}})

	assert.Equal(t, 2, turns)
	assert.Equal(t, "2", session.safeContent())
	assert.Contains(t, recorder.Body.String(), "event: done")
	assert.NotContains(t, recorder.Body.String(), "event: error")
}

func TestAssistantAgentCannotSkipRequiredParameterizedTool(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	c.Set(assistantUserContextKey, assistantUserContext{
		AccessLevel: "L0", ConversationTitleNeeded: true, Intent: model.AssistantIntentMath,
		LatestUserRequest: "Calculate 1 + 1",
	})
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		assert.Equal(t, "calculate_math", assistantNamedToolChoiceName(request.ToolChoice))
		return http.StatusOK, assistantLoopCallBody(t, nil, "unverified claim"), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1}, []assistantOpenAIMessage{{Role: "user", Content: "Calculate 1 + 1"}})
	assert.Equal(t, http.StatusBadGateway, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "ASSISTANT_REQUIRED_TOOL_MISSING")
	assert.NotContains(t, recorder.Body.String(), "unverified claim")
}

func TestAssistantAgentFirstAnswerDeltaPrecedesUpstreamCompletion(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	c.Set(assistantUserContextKey, assistantUserContext{AccessLevel: "L0", ConversationTitleNeeded: true})
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	c.Set(assistantStreamSessionKey, session)
	answer := strings.Repeat("This is safe answer text. ", 4)
	turns := 0
	original := relayAssistantStreamTurn
	relayAssistantStreamTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int, stream *assistantStreamSession) (int, []byte, error) {
		turns++
		assert.True(t, request.Stream)
		assert.Empty(t, request.Tools)
		assert.NotContains(t, recorder.Body.String(), "event: delta")
		require.NoError(t, stream.appendContent(answer))
		// Check while upstream is still executing, not only after run returns.
		assert.Contains(t, recorder.Body.String(), "event: delta")
		assert.NotContains(t, recorder.Body.String(), "event: done")
		assert.True(t, recorder.Flushed)
		return http.StatusOK, assistantLoopCallBody(t, nil, answer), nil
	}
	t.Cleanup(func() { relayAssistantStreamTurn = original })
	runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1, StreamEnabled: true}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})
	assert.Equal(t, 1, turns)
	assert.Contains(t, recorder.Body.String(), "event: done")
	assert.NotContains(t, recorder.Body.String(), "event: error")
}

func TestAssistantPlannedReadRequiresAllowedExposedUncalledTool(t *testing.T) {
	userContext := assistantUserContext{AccessLevel: "L1", DeveloperAccessGranted: true}
	tools := assistantToolDefinitionsForContext(userContext)
	for _, name := range []string{"get_service_facts", "get_account_access", "get_available_models", "get_l1_recommendation"} {
		t.Run(name, func(t *testing.T) {
			request := assistantOpenAIRequest{Tools: tools, ToolChoice: assistantNamedToolChoice(name)}
			call, ok := assistantPlannedReadCall(request, userContext, nil, 0)
			require.True(t, ok)
			assert.Equal(t, name, call.Function.Name)
			assert.JSONEq(t, "{}", call.Function.Arguments)
			assert.NotEmpty(t, call.ID)
			_, ok = assistantPlannedReadCall(request, userContext, map[string]bool{name: true}, 1)
			assert.False(t, ok, "do not pre-execute the same read twice")
			request.Tools = nil
			_, ok = assistantPlannedReadCall(request, userContext, nil, 0)
			assert.False(t, ok, "named choice alone does not expose a tool")
		})
	}
	for _, name := range []string{"calculate_math", "get_model_pricing", "request_create_key", "prepare_admin_config_change", "grant_l1_access", "set_conversation_title"} {
		_, ok := assistantPlannedReadCall(assistantOpenAIRequest{
			Tools: tools, ToolChoice: assistantNamedToolChoice(name),
		}, userContext, nil, 0)
		assert.False(t, ok, name)
	}
	_, ok := assistantPlannedReadCall(assistantOpenAIRequest{Tools: tools, ToolChoice: "auto"}, userContext, nil, 0)
	assert.False(t, ok, "never prefetch tools for an unrelated ordinary question")
}
