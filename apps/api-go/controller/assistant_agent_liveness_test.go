// Copyright (C) 2026 LIghtJUNction
package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantLivenessRetriesMalformedSuccessWithoutRepeatingTools(t *testing.T) {
	for _, malformed := range []string{`{`, `{}`, `{"choices":[]}`, `{"choices":[{"message":{"content":"  "}}]}`} {
		t.Run(malformed, func(t *testing.T) {
			c, _ := assistantLoopTestContext(t)
			turns := 0
			status, body, err := relayAssistantTurnWithRetryUsing(c, assistantOpenAIRequest{}, "test", 0,
				func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error) {
					turns++
					if turns == 1 {
						return 200, []byte(malformed), nil
					}
					return 200, assistantLoopCallBody(t, nil, "Recovered"), nil
				})
			require.NoError(t, err)
			assert.Equal(t, 200, status)
			assert.Equal(t, 2, turns)
			assert.Contains(t, string(body), "Recovered")
		})
	}
}

func TestAssistantLivenessLastFallbackPreservesFailure(t *testing.T) {
	c, _ := assistantLoopTestContext(t)
	turns := 0
	failure := `{"error":{"message":"Missing required parameter: tool_choice.name"}}`
	status, body, err := relayAssistantTurnWithRetryUsing(c, assistantOpenAIRequest{ToolChoice: "auto"}, "test", 0,
		func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error) {
			turns++
			if turns < assistantUpstreamMaxAttempts {
				return 503, []byte(`{}`), nil
			}
			return 400, []byte(failure), nil
		})
	require.NoError(t, err)
	assert.Equal(t, 400, status)
	assert.Equal(t, failure, string(body))
	assert.Equal(t, assistantUpstreamMaxAttempts, turns)
}

func TestAssistantLivenessRetainsNestedArgumentDeltas(t *testing.T) {
	c, _ := assistantLoopTestContext(t)
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	writer := newAssistantStreamingRelayWriter(c.Writer, session)
	for _, fragment := range []string{`{`, `"query":`, `{`, `"p":"1"`, `}`, `}`} {
		payload, err := json.Marshal(map[string]any{"choices": []any{map[string]any{"delta": map[string]any{
			"tool_calls": []any{map[string]any{"index": 0, "id": "call", "function": map[string]any{
				"name": "execute_admin_operation", "arguments": fragment,
			}}},
		}}}})
		require.NoError(t, err)
		_, err = writer.Write([]byte("data: " + string(payload) + "\n\n"))
		require.NoError(t, err)
	}
	body, err := writer.responseBody()
	require.NoError(t, err)
	response, err := parseAssistantResponse(body)
	require.NoError(t, err)
	require.Len(t, response.Choices[0].Message.ToolCalls, 1)
	assert.Equal(t, `{"query":{"p":"1"}}`, response.Choices[0].Message.ToolCalls[0].Function.Arguments)
}

func TestAssistantLivenessBufferedProviderKeepsToolsAndHTTPError(t *testing.T) {
	for _, status := range []int{200, 400} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			c, recorder := assistantLoopTestContext(t)
			session := newAssistantStreamSession(c.Writer)
			require.NoError(t, session.start())
			writer := newAssistantStreamingRelayWriter(c.Writer, session)
			body := assistantLoopCallBody(t, []assistantOpenAIToolCall{assistantLoopMathCall("call", 2)}, "private planning")
			if status == 400 {
				body = []byte(`{"error":{"message":"tool_choice.name required"}}`)
			}
			writer.WriteHeader(status)
			_, err := writer.Write(body)
			require.NoError(t, err)
			result, err := writer.responseBody()
			require.NoError(t, err)
			assert.JSONEq(t, string(body), string(result))
			assert.NotContains(t, recorder.Body.String(), "private planning")
		})
	}
}

func TestAssistantLivenessVaryingFailuresStillEndWithFinalAnswer(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	turns := 0
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns < 3 {
			call := assistantOpenAIToolCall{Function: assistantOpenAIToolCallFunction{Name: fmt.Sprintf("nonexistent_%d", turns), Arguments: `{}`}}
			return 200, assistantLoopCallBody(t, []assistantOpenAIToolCall{call}, ""), nil
		}
		require.Empty(t, request.Tools)
		assert.Contains(t, request.Messages[0].Content, "Do not invent user IDs")
		return 200, assistantLoopCallBody(t, nil, "未取得可验证的用户数据，不能提供用户 ID。"), nil
	}
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 32},
		[]assistantOpenAIMessage{{Role: "user", Content: "分析批量注册用户，告诉我用户ID"}})
	assert.Equal(t, 3, turns)
	assert.Equal(t, 200, recorder.Code)
	assert.Contains(t, recorder.Body.String(), "未取得可验证的用户数据")
	assert.False(t, assistantToolMadeProgress("list_admin_operations", map[string]any{"ok": true, "total": 0}))
	assert.True(t, assistantToolMadeProgress("list_admin_operations", map[string]any{"ok": true, "total": 1}))
}

func TestAssistantLivenessPanicReturnsTerminalError(t *testing.T) {
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	relayAssistantAgentTurn = func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error) {
		panic("provider-internal-secret")
	}
	c, recorder := assistantLoopTestContext(t)
	runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})
	assert.Equal(t, 500, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"retryable":false`)
	assert.Contains(t, recorder.Body.String(), "ASSISTANT_RUN_FAILED")
	assert.NotContains(t, recorder.Body.String(), "provider-internal-secret")
}

func TestAssistantLivenessWatchHasExactlyOneTerminalEvent(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	stop := session.watch(ctx, cancel, 5*time.Millisecond, nil)
	require.NoError(t, session.progress("tool", 2))
	session.markWorkStarted()
	require.Eventually(t, func() bool { _, finished := session.startedAndFinished(); return finished }, time.Second, time.Millisecond)
	stop()
	require.NoError(t, session.fail(503, "ASSISTANT_UPSTREAM_FAILED", "late error"))
	assert.Equal(t, 1, strings.Count(recorder.Body.String(), "event: error"))
	assert.Contains(t, recorder.Body.String(), "event: heartbeat")
	assert.Contains(t, recorder.Body.String(), `"phase":"tool"`)
	assert.Contains(t, recorder.Body.String(), `"retryable":false`)
}

func TestAssistantLivenessTimeoutReportsStructuralDiagnostics(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	session := newAssistantStreamSession(c.Writer)
	require.NoError(t, session.start())
	c.Set(assistantStreamSessionKey, session)
	c.Set("assistant_work_started", true)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 60*time.Millisecond)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	relayAssistantAgentTurn = func(c *gin.Context, _ assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		<-c.Request.Context().Done()
		return 0, nil, c.Request.Context().Err()
	}

	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 12, TimeoutSeconds: 60},
		[]assistantOpenAIMessage{{Role: "user", Content: "hello"}})

	body := recorder.Body.String()
	assert.Contains(t, body, "ASSISTANT_REQUEST_TIMEOUT")
	// The deadline fired inside the first model turn of a twelve-step budget,
	// and a tool had already been reported as started.
	assert.Contains(t, body, `"steps":1`)
	assert.Contains(t, body, `"max_steps":12`)
	assert.Contains(t, body, `"work_started":true`)
	var failure struct {
		ElapsedMS int64 `json:"elapsed_ms"`
		TimeoutMS int64 `json:"timeout_ms"`
	}
	for _, line := range strings.Split(body, "\n") {
		if data, ok := strings.CutPrefix(line, "data: "); ok && strings.Contains(data, "ASSISTANT_REQUEST_TIMEOUT") {
			require.NoError(t, json.Unmarshal([]byte(data), &failure))
		}
	}
	// Measured at stop time, so it must be far below the configured budget.
	assert.NotZero(t, failure.ElapsedMS)
	assert.Less(t, failure.ElapsedMS, int64(50_000))
	assert.Equal(t, int64(60_000), failure.TimeoutMS)
}

func TestAssistantLivenessCancelsStalledHTTPProvider(t *testing.T) {
	finished := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		<-r.Context().Done()
	}))
	defer server.Close()
	c, recorder := assistantLoopTestContext(t)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 50*time.Millisecond)
	defer cancel()
	c.Request = c.Request.WithContext(ctx)
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	relayAssistantAgentTurn = func(c *gin.Context, _ assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, server.URL, nil)
		if err != nil {
			return 500, nil, err
		}
		response, err := server.Client().Do(req)
		if err != nil {
			return 502, nil, err
		}
		defer response.Body.Close()
		body, err := io.ReadAll(response.Body)
		return response.StatusCode, body, err
	}
	runAssistantAgent(c, setting.AssistantSettings{MaxSteps: 1}, []assistantOpenAIMessage{{Role: "user", Content: "hello"}})
	assert.Equal(t, 408, recorder.Code)
	assert.Contains(t, recorder.Body.String(), `"retryable":false`)
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("provider request was not cancelled")
	}
}

func TestAssistantLivenessDoesNotExposeIPAccessMutations(t *testing.T) {
	for _, key := range []string{"IPAccessRoutingRules", "GlobalIPWhitelist", "PersonalIPWhitelist", "RegionAccessPolicy"} {
		_, allowed := assistantAdminConfigLabel(key)
		assert.False(t, allowed, key)
	}
	_, allowed := assistantAdminConfigLabel("SystemName")
	assert.True(t, allowed)
}

func TestAssistantLivenessTotalToolBudgetReservesAnswer(t *testing.T) {
	c, recorder := assistantLoopTestContext(t)
	original := relayAssistantAgentTurn
	t.Cleanup(func() { relayAssistantAgentTurn = original })
	turns := 0
	relayAssistantAgentTurn = func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
		turns++
		if turns <= assistantAgentToolCallBudget/assistantToolCallsPerTurn {
			calls := make([]assistantOpenAIToolCall, assistantToolCallsPerTurn)
			for i := range calls {
				calls[i] = assistantLoopMathCall("call", turns*assistantToolCallsPerTurn+i)
			}
			return 200, assistantLoopCallBody(t, calls, ""), nil
		}
		assert.Empty(t, request.Tools)
		return 200, assistantLoopCallBody(t, nil, "Only the completed calculations were verified."), nil
	}
	runAssistantAgent(c, setting.AssistantSettings{AgentLoopEnabled: true, MaxSteps: 32}, []assistantOpenAIMessage{{Role: "user", Content: "Calculate"}})
	assert.Equal(t, assistantAgentToolCallBudget/assistantToolCallsPerTurn+1, turns)
	assert.Equal(t, 200, recorder.Code)
}
