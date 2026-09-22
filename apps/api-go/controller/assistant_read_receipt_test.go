package controller

import (
	"encoding/json"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// Check the actual request history, not text that merely mentions a tool name.
func requireAssistantPairedReadReceipt(t *testing.T, request assistantOpenAIRequest, name string, wantOK bool) map[string]any {
	t.Helper()
	calls := map[string]int{}
	for index, message := range request.Messages {
		for _, call := range message.ToolCalls {
			if call.Function.Name == name {
				require.Equal(t, "assistant", message.Role)
				require.NotEmpty(t, call.ID)
				require.JSONEq(t, "{}", call.Function.Arguments)
				_, duplicate := calls[call.ID]
				require.False(t, duplicate, "duplicate read call ID")
				calls[call.ID] = index
			}
		}
	}
	require.Len(t, calls, 1, "required read must execute exactly once before the model")
	var receipt map[string]any
	matches := 0
	for index, message := range request.Messages {
		if callIndex, found := calls[message.ToolCallID]; found && message.Role == "tool" {
			require.Greater(t, index, callIndex)
			require.NoError(t, json.Unmarshal([]byte(message.Content), &receipt))
			matches++
		}
	}
	require.Equal(t, 1, matches, "required read must have one paired receipt")
	require.Equal(t, wantOK, receipt["ok"])
	return receipt
}

func TestAssistantAgentExplainsFailedRequiredReadWithoutInventedResults(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	c.Set(assistantUserContextKey, assistantUserContext{
		Intent: model.AssistantIntentRecommendation, AccessLevel: "L0",
		RecommendationAction: assistantRecommendationActionRevise,
	})
	turns := 0
	original := relayAssistantAgentTurn
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		receipt := requireAssistantPairedReadReceipt(t, request, "get_l1_recommendation", false)
		require.Contains(t, receipt["error"], "signed-in account is unavailable")
		require.Equal(t, "none", request.ToolChoice)
		return http.StatusOK, assistantLoopCallBody(t, nil, "The account is unavailable; no recommendation was read or changed."), nil
	}
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 6}, []assistantOpenAIMessage{{Role: "user", Content: "Please revise my recommendation"}})
	require.Equal(t, 1, turns)
	require.Equal(t, http.StatusOK, recorder.Code)
	require.Contains(t, recorder.Body.String(), "no recommendation was read or changed")
	_, action := c.Get(assistantClientActionKey)
	require.False(t, action)
}
