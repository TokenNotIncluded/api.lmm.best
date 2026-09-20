package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAssistantChatBodyMaximumEnvelope(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))
	withAssistantSettings(t, true, "server-owned-model")
	messages := make([]map[string]any, 12)
	for i := range messages {
		count, role := 1, "assistant"
		if i < 2 {
			count = 4000
		} else if i == 2 {
			count = 3991
		}
		if i == 0 || i == 11 {
			role = "user"
		}
		messages[i] = map[string]any{"role": role, "content": strings.Repeat("🙂", count)}
	}
	arguments := `{"q":"` + strings.Repeat("x", 16*1024-8) + `"}`
	require.Len(t, arguments, 16*1024)
	messages[1]["tool_calls"] = []any{map[string]any{"id": "call", "type": "function", "function": map[string]any{"name": "fixture", "arguments": arguments}}}
	payload, err := json.Marshal(map[string]any{"messages": messages})
	require.NoError(t, err)
	require.Less(t, len(payload), 64<<10)
	router := gin.New()
	router.POST("/api/assistant/chat", middleware.RequestBodyLimit(64<<10), PrepareAssistantRequest, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	response := httptest.NewRecorder()
	request := httptest.NewRequest("POST", "/api/assistant/chat", strings.NewReader(string(payload)))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(response, request)
	require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
}

func TestAssistantChatBodyTransportBoundary(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AssistantLead{}, &model.AssistantProfileBucket{}, &model.AssistantFirstQuestionStat{}))
	withAssistantSettings(t, true, "server-owned-model")
	const limit = 64 << 10
	for _, declared := range []bool{false, true} {
		for _, size := range []int{limit - 1, limit, limit + 1} {
			t.Run(fmt.Sprintf("declared=%t/bytes=%d", declared, size), func(t *testing.T) {
				router := gin.New()
				router.POST("/api/assistant/chat", middleware.RequestBodyLimit(limit), PrepareAssistantRequest, func(c *gin.Context) { c.Status(http.StatusNoContent) })
				payload := `{"message":"hello"}`
				payload += strings.Repeat(" ", size-len(payload))
				request := httptest.NewRequest("POST", "/api/assistant/chat", strings.NewReader(payload))
				request.Header.Set("Content-Type", "application/json")
				if !declared {
					request.ContentLength = -1
					request.TransferEncoding = []string{"chunked"}
				}
				response := httptest.NewRecorder()
				router.ServeHTTP(response, request)
				if size <= limit {
					require.Equal(t, http.StatusNoContent, response.Code, response.Body.String())
				} else {
					require.Equal(t, http.StatusRequestEntityTooLarge, response.Code)
					if declared {
						require.Empty(t, response.Body.String())
					} else {
						require.JSONEq(t, `{"success":false,"code":"ASSISTANT_REQUEST_TOO_LARGE","message":"request body too large","retryable":false}`, response.Body.String())
					}
				}
			})
		}
	}
}
