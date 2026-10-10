package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func assistantCompletionTestContext(t *testing.T, stream bool) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	c.Set(assistantUserContextKey, assistantUserContext{
		AccessLevel: "L1", DeveloperAccessGranted: true, LatestUserRequest: "生成一段动画代码",
	})
	if stream {
		session := newAssistantStreamSession(c.Writer)
		require.NoError(t, session.start())
		c.Set(assistantStreamSessionKey, session)
	}
	return c, recorder
}

func assistantCompletionFinalBody(t *testing.T, recorder *httptest.ResponseRecorder, stream bool) []byte {
	t.Helper()
	if !stream {
		return recorder.Body.Bytes()
	}
	for _, event := range strings.Split(recorder.Body.String(), "\n\n") {
		if strings.HasPrefix(event, "event: done\ndata: ") {
			return []byte(strings.TrimPrefix(event, "event: done\ndata: "))
		}
	}
	t.Fatalf("no done event: %s", recorder.Body.String())
	return nil
}

func TestAssistantOutputLengthHasVisibleStableIncompleteOutcome(t *testing.T) {
	for _, stream := range []bool{false, true} {
		for _, content := range []string{"```html\n<svg><g transform=\"translate(10,20)\"><line stro", ""} {
			name := "json"
			if stream {
				name = "stream"
			}
			if content == "" {
				name += "-empty"
			}
			t.Run(name, func(t *testing.T) {
				c, recorder := assistantCompletionTestContext(t, stream)
				cacheKey := t.Name()
				c.Set("assistant_cache_key", cacheKey)
				upstream, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
					"message": map[string]any{"content": content}, "finish_reason": "length",
				}}})
				require.NoError(t, err)
				calls := 0
				originalJSON, originalStream := relayAssistantAgentTurn, relayAssistantStreamTurn
				t.Cleanup(func() { relayAssistantAgentTurn, relayAssistantStreamTurn = originalJSON, originalStream })
				relayAssistantAgentTurn = func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error) {
					calls++
					return http.StatusOK, upstream, nil
				}
				relayAssistantStreamTurn = func(_ *gin.Context, _ assistantOpenAIRequest, _ string, _ int, session *assistantStreamSession) (int, []byte, error) {
					calls++
					require.NoError(t, session.appendContent(content))
					return http.StatusOK, upstream, nil
				}
				runAssistantAgent(c, setting.AssistantSettings{
					Model: "completion-test", MaxTokens: 100, TimeoutSeconds: 45,
					StreamEnabled: stream, CacheEnabled: true, CacheTTLMinutes: 1,
				}, []assistantOpenAIMessage{{Role: "user", Content: "生成一段动画代码"}})
				require.Equal(t, 1, calls, "length must not trigger another model/tool run")
				body := assistantCompletionFinalBody(t, recorder, stream)
				parsed, err := parseAssistantResponse(body)
				require.NoError(t, err)
				require.Len(t, parsed.Choices, 1)
				answer := assistantResponseContent(parsed.Choices[0].Message.Content)
				assert.Equal(t, assistantIncompleteOutputContent(c, content), answer)
				assert.Equal(t, 1, strings.Count(answer, assistantOutputIncompleteZH))
				assert.Equal(t, "length", parsed.Choices[0].FinishReason)
				var metadata struct {
					Completion struct {
						Status    string `json:"status"`
						Code      string `json:"code"`
						Retryable bool   `json:"retryable"`
					} `json:"lmm_assistant_completion"`
				}
				require.NoError(t, json.Unmarshal(body, &metadata))
				assert.Equal(t, "incomplete", metadata.Completion.Status)
				assert.Equal(t, assistantOutputIncompleteCode, metadata.Completion.Code)
				assert.False(t, metadata.Completion.Retryable)
				_, found := getAssistantCachedResponse(cacheKey)
				assert.False(t, found)
				if stream {
					assert.Equal(t, answer, assistantStreamSessionFrom(c).safeContent())
					if content != "" {
						assert.Contains(t, recorder.Body.String(), "event: replace")
					}
				}
			})
		}
	}
}

func TestAssistantIncompleteOutputLanguageAndByteLimit(t *testing.T) {
	for _, content := range []string{"Partial English answer", strings.Repeat("<", 50000)} {
		body := []byte(`{"choices":[{"message":{"content":"` + content + `"},"finish_reason":"length"}]}`)
		normalized, err := normalizeAssistantClientResponse(nil, body)
		require.NoError(t, err)
		assert.LessOrEqual(t, len(normalized), assistantUpstreamResponseMaxBytes)
		parsed, err := parseAssistantResponse(normalized)
		require.NoError(t, err)
		answer := assistantResponseContent(parsed.Choices[0].Message.Content)
		assert.True(t, strings.HasPrefix(answer, assistantOutputIncompleteEN))
		if len(content) > 1000 {
			assert.Equal(t, assistantOutputIncompleteEN, answer)
		} else {
			assert.Contains(t, answer, content)
		}
		normalizedAgain, err := normalizeAssistantClientResponse(nil, normalized)
		require.NoError(t, err)
		assert.JSONEq(t, string(normalized), string(normalizedAgain))
	}
}

func TestAssistantLengthEmptyResponseDoesNotRetry(t *testing.T) {
	c, _ := assistantCompletionTestContext(t, false)
	calls := 0
	status, body, err := relayAssistantTurnWithRetryUsing(c, assistantOpenAIRequest{}, "limit", 0,
		func(*gin.Context, assistantOpenAIRequest, string, int) (int, []byte, error) {
			calls++
			return http.StatusOK, []byte(`{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`), nil
		})
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Contains(t, string(body), `"length"`)
	assert.Equal(t, 1, calls)
}

func TestAssistantStreamPreservesLengthAndClearsItBeforeRetry(t *testing.T) {
	for _, upstream := range []string{
		"data: {\"choices\":[{\"delta\":{\"content\":\"partial\"}}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"length\"}]}\n\ndata: [DONE]\n\n",
		`{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`,
		`{"choices":[{"message":{"content":""},"finish_reason":"length"}]}`,
	} {
		c, _ := assistantCompletionTestContext(t, true)
		writer := newAssistantStreamingRelayWriter(c.Writer, assistantStreamSessionFrom(c))
		_, err := writer.Write([]byte(upstream))
		require.NoError(t, err)
		body, err := writer.responseBody()
		require.NoError(t, err)
		parsed, err := parseAssistantResponse(body)
		require.NoError(t, err)
		assert.True(t, assistantOutputLengthLimited(parsed))
		require.NoError(t, writer.ResetForRelayRetry())
		_, err = writer.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"complete\"},\"finish_reason\":\"stop\"}]}\n\n"))
		require.NoError(t, err)
		body, err = writer.responseBody()
		require.NoError(t, err)
		parsed, err = parseAssistantResponse(body)
		require.NoError(t, err)
		assert.Equal(t, "stop", parsed.Choices[0].FinishReason)
		assert.Equal(t, "complete", assistantResponseContent(parsed.Choices[0].Message.Content))
	}
}

func TestAssistantLengthAfterMemoryWriteDoesNotReplayTools(t *testing.T) {
	for _, test := range []struct{ truncatedTool, stream bool }{{}, {stream: true}, {truncatedTool: true}, {truncatedTool: true, stream: true}} {
		truncatedTool := test.truncatedTool
		name := "final-answer"
		if truncatedTool {
			name = "tool-arguments"
		}
		if test.stream {
			name += "-stream"
		}
		t.Run(name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.User{}, &model.AssistantMemory{}))
			user := model.User{Username: "completion-user", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}
			require.NoError(t, db.Create(&user).Error)
			writes := 0
			countWrites := func(tx *gorm.DB) {
				if tx.Statement.Table == "assistant_memories" {
					writes++
				}
			}
			require.NoError(t, db.Callback().Create().After("gorm:create").Register("completion_write_count", countWrites))
			require.NoError(t, db.Callback().Update().After("gorm:update").Register("completion_write_count", countWrites))
			c, recorder := assistantCompletionTestContext(t, test.stream)
			c.Set("id", user.Id)
			c.Set(assistantActorUserIDKey, user.Id)
			calls := 0
			originalJSON, originalStream := relayAssistantAgentTurn, relayAssistantStreamTurn
			t.Cleanup(func() { relayAssistantAgentTurn, relayAssistantStreamTurn = originalJSON, originalStream })
			response := func(_ *gin.Context, request assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
				calls++
				if calls == 1 {
					reason := "tool_calls"
					if truncatedTool {
						reason = "length"
					}
					body, err := json.Marshal(map[string]any{"choices": []any{map[string]any{
						"finish_reason": reason, "message": map[string]any{"tool_calls": []any{map[string]any{
							"id": "memory-write", "type": "function", "function": map[string]any{
								"name": "remember_memory", "arguments": `{"title":"Project preferences","content":"Use short numbered instructions"}`,
							},
						}}},
					}}})
					require.NoError(t, err)
					return http.StatusOK, body, nil
				}
				last := request.Messages[len(request.Messages)-1]
				assert.Equal(t, "tool", last.Role)
				assert.Contains(t, last.Content, `"ok":true`)
				return http.StatusOK, []byte(`{"choices":[{"message":{"content":"Saved. Partial explanation"},"finish_reason":"length"}]}`), nil
			}
			relayAssistantAgentTurn = response
			relayAssistantStreamTurn = func(c *gin.Context, request assistantOpenAIRequest, root string, step int, session *assistantStreamSession) (int, []byte, error) {
				status, body, err := response(c, request, root, step)
				parsed, parseErr := parseAssistantResponse(body)
				require.NoError(t, parseErr)
				if len(parsed.Choices[0].Message.ToolCalls) == 0 {
					require.NoError(t, session.appendContent(assistantResponseContent(parsed.Choices[0].Message.Content)))
				}
				return status, body, err
			}
			runAssistantAgent(c, setting.AssistantSettings{Model: "completion-test", MaxTokens: 100, MaxSteps: 4, AgentLoopEnabled: true, StreamEnabled: test.stream, TimeoutSeconds: 45}, []assistantOpenAIMessage{{Role: "user", Content: "Use short instructions"}})
			if truncatedTool {
				assert.Equal(t, 1, calls)
				assert.Equal(t, 0, writes)
				assert.Contains(t, recorder.Body.String(), assistantOutputIncompleteCode)
				assert.Contains(t, recorder.Body.String(), `"retryable":false`)
			} else {
				assert.Equal(t, 2, calls)
				assert.Equal(t, 1, writes)
				assert.Contains(t, recorder.Body.String(), `"lmm_assistant_tools"`)
				assert.Contains(t, recorder.Body.String(), `"status":"incomplete"`)
			}
		})
	}
}

func TestAssistantCompletionCacheRejectsIncompleteAndKeepsStop(t *testing.T) {
	settings := setting.AssistantSettings{CacheEnabled: true, CacheTTLMinutes: 1}
	partial := []byte(`{"choices":[{"message":{"content":"partial"},"finish_reason":"length"}]}`)
	normalized, err := normalizeAssistantClientResponse(nil, partial)
	require.NoError(t, err)
	for _, body := range [][]byte{partial, normalized} {
		key := t.Name() + string(body)
		storeAssistantCachedResponse(settings, key, http.StatusOK, body)
		_, found := getAssistantCachedResponse(key)
		assert.False(t, found)
		require.NoError(t, getAssistantResponseCache().SetWithTTL(key, assistantCachedResponse{Status: http.StatusOK, Body: body}, time.Minute))
		_, found = getAssistantCachedResponse(key)
		assert.False(t, found, "incomplete cached data must not be served")
	}
	key := t.Name() + "-stop"
	storeAssistantCachedResponse(settings, key, http.StatusOK, []byte(`{"choices":[{"message":{"content":"complete"},"finish_reason":"stop"}]}`))
	cached, found := getAssistantCachedResponse(key)
	require.True(t, found)
	assert.NotContains(t, string(cached.Body), "incomplete")
	parsed, err := parseAssistantResponse(cached.Body)
	require.NoError(t, err)
	assert.Equal(t, "complete", assistantResponseContent(parsed.Choices[0].Message.Content))
	assert.Equal(t, "stop", parsed.Choices[0].FinishReason)
}
