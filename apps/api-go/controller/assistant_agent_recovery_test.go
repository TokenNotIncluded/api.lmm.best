package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantAgentTitleFailurePreservesAnswerAndRequiredReads(t *testing.T) {
	for _, test := range []struct {
		name       string
		firstBody  string
		status     int
		needsRead  bool
		ignoreRead bool
		wantTurns  int
	}{
		{name: "unsupported title", status: http.StatusBadRequest, firstBody: `{"error":{"message":"provider does not support forced tool_choice"}}`, wantTurns: 2},
		{name: "ignored title reuses answer", status: http.StatusOK, firstBody: `{"choices":[{"message":{"content":"useful answer"}}]}`, wantTurns: 1},
		{name: "ignored title still reads facts", status: http.StatusOK, firstBody: `{"choices":[{"message":{"content":"unverified claim"}}]}`, needsRead: true, wantTurns: 3},
		{name: "malformed title still reads facts", status: http.StatusOK, firstBody: `{"choices":[{"message":{"tool_calls":[{"id":"title","type":"function","function":{"name":"set_conversation_title","arguments":"{}"}}]}}]}`, needsRead: true, wantTurns: 3},
		{name: "required read cannot be skipped", status: http.StatusOK, firstBody: `{"choices":[{"message":{"content":"unverified claim"}}]}`, needsRead: true, ignoreRead: true, wantTurns: 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
			message := "hello"
			if test.needsRead {
				message = "What is the Base URL?"
			}
			c.Set(assistantUserContextKey, assistantUserContext{
				AccessLevel: "L0", ConversationTitleNeeded: true, LatestUserRequest: message,
			})
			turns := 0
			originalRelay := relayAssistantAgentTurn
			relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
				turns++
				if turns == 1 {
					assert.Equal(t, "set_conversation_title", assistantNamedToolChoiceName(request.ToolChoice))
					return test.status, []byte(test.firstBody), nil
				}
				if test.needsRead && turns == 2 {
					assert.Equal(t, "get_service_facts", assistantNamedToolChoiceName(request.ToolChoice))
					if test.ignoreRead {
						return http.StatusOK, []byte(`{"choices":[{"message":{"content":"unverified claim"}}]}`), nil
					}
					return http.StatusOK, []byte(`{"choices":[{"message":{"tool_calls":[{"id":"facts","type":"function","function":{"name":"get_service_facts","arguments":"{}"}}]}}]}`), nil
				}
				assert.Empty(t, request.Tools)
				if test.needsRead {
					result := request.Messages[len(request.Messages)-1]
					assert.Equal(t, "tool", result.Role)
					assert.Contains(t, result.Content, `"openai_base_url"`)
				}
				return http.StatusOK, []byte(`{"choices":[{"message":{"content":"useful answer"}}]}`), nil
			}
			t.Cleanup(func() { relayAssistantAgentTurn = originalRelay })

			runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1, AgentLoopEnabled: false}, []assistantOpenAIMessage{{Role: "user", Content: message}})

			assert.Equal(t, test.wantTurns, turns)
			assert.False(t, assistantUserContextFromGin(c).ConversationTitleNeeded)
			assert.Empty(t, c.GetString(assistantConversationTitleDraftKey))
			assert.NotContains(t, recorder.Body.String(), "unverified claim")
			if test.ignoreRead {
				assert.Equal(t, http.StatusBadGateway, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "ASSISTANT_REQUIRED_TOOL_MISSING")
			} else {
				assert.Equal(t, http.StatusOK, recorder.Code)
				assert.Contains(t, recorder.Body.String(), "useful answer")
			}
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

func TestAssistantAgentSkippedTitleReservesTaskAndClearsTentativeStream(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
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
		switch turns {
		case 1:
			assert.Equal(t, "set_conversation_title", assistantNamedToolChoiceName(request.ToolChoice))
			require.NoError(t, stream.appendContent("An unverified answer must be cleared before completing the calculation."))
			return http.StatusOK, []byte(`{"choices":[{"message":{"content":"unverified answer"}}]}`), nil
		case 2:
			assert.Empty(t, stream.safeContent())
			assert.Equal(t, "calculate_math", assistantNamedToolChoiceName(request.ToolChoice))
			return http.StatusOK, []byte(`{"choices":[{"message":{"tool_calls":[{"id":"math","type":"function","function":{"name":"calculate_math","arguments":"{\"expression\":\"1+1\"}"}}]}}]}`), nil
		default:
			assert.Empty(t, request.Tools)
			assert.Contains(t, request.Messages[len(request.Messages)-1].Content, `"result":2`)
			require.NoError(t, stream.appendContent("2"))
			return http.StatusOK, []byte(`{"choices":[{"message":{"content":"2"}}]}`), nil
		}
	}
	t.Cleanup(func() { relayAssistantStreamTurn = originalRelay })

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: false, MaxSteps: 1, StreamEnabled: true}, []assistantOpenAIMessage{{Role: "user", Content: "Calculate 1 + 1"}})

	assert.Equal(t, 3, turns)
	assert.Equal(t, "2", session.safeContent())
	assert.Contains(t, recorder.Body.String(), "event: replace")
	assert.Contains(t, recorder.Body.String(), "event: done")
	assert.NotContains(t, recorder.Body.String(), "event: error")
}
