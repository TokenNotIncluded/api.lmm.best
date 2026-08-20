/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantRelayRetryPolicyClassifiesTransientHTTPAndNetworkErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)

	transientStatuses := []int{
		http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusNetworkAuthenticationRequired,
	}
	for _, status := range transientStatuses {
		t.Run("http-"+strconv.Itoa(status), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			apiErr := types.NewOpenAIError(
				errors.New("transient assistant upstream failure"),
				types.ErrorCodeBadResponseStatusCode,
				status,
			)

			assert.True(t, service.ShouldRetryRelayError(c, apiErr, 1))
		})
	}

	networkErr := types.NewErrorWithStatusCode(
		errors.New("dial tcp: connection reset by peer"),
		types.ErrorCodeDoRequestFailed,
		0,
	)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	assert.True(t, service.ShouldRetryRelayError(c, networkErr, 1))

	for _, status := range []int{
		http.StatusBadRequest,
		http.StatusRequestTimeout,
		http.StatusGatewayTimeout,
		524,
	} {
		t.Run("hard-stop-http-"+strconv.Itoa(status), func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			apiErr := types.NewOpenAIError(
				errors.New("assistant upstream failure must not be retried"),
				types.ErrorCodeBadResponseStatusCode,
				status,
			)

			assert.False(t, service.ShouldRetryRelayError(c, apiErr, 1))
		})
	}
}

func TestAssistantRelayRetryPolicyStopsWhenAttemptBudgetIsExhausted(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	apiErr := types.NewOpenAIError(
		errors.New("assistant upstream unavailable"),
		types.ErrorCodeBadResponseStatusCode,
		http.StatusServiceUnavailable,
	)

	assert.True(t, service.ShouldRetryRelayError(c, apiErr, 1))
	assert.False(t, service.ShouldRetryRelayError(c, apiErr, 0))
}

func TestAssistantRelayRetryAdaptsResponsesToolChoiceShape(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	request := assistantOpenAIRequest{ToolChoice: assistantNamedToolChoice("get_service_facts")}
	choices := make([]any, 0, 2)
	calls := 0

	status, _, err := relayAssistantTurnWithRetryUsing(c, request, "assistant-tool-choice-fallback", 0,
		func(_ *gin.Context, req assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
			calls++
			choices = append(choices, req.ToolChoice)
			if calls == 1 {
				return http.StatusBadGateway, []byte(`{"error":{"message":"[ObjectParam] [tool_choice.name] [missing_required_parameter]"}}`), nil
			}
			return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
		})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, 2, calls)
	require.Len(t, choices, 2)
	assert.Equal(t, "get_service_facts", assistantNamedToolChoiceName(choices[0]))
	assert.Equal(t, "get_service_facts", assistantNamedToolChoiceName(choices[1]))
	second, ok := choices[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "function", second["type"])
	assert.Equal(t, "get_service_facts", second["name"])
	assert.NotContains(t, second, "function")
}

func TestAssistantRelayRetryOmitsStringToolChoiceWhenGatewayRequiresName(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/chat", nil)
	choices := make([]any, 0, 2)
	calls := 0

	status, _, err := relayAssistantTurnWithRetryUsing(c, assistantOpenAIRequest{ToolChoice: "auto"}, "assistant-tool-choice-omit", 0,
		func(_ *gin.Context, req assistantOpenAIRequest, _ string, _ int) (int, []byte, error) {
			calls++
			choices = append(choices, req.ToolChoice)
			if calls == 1 {
				return http.StatusBadRequest, []byte(`{"error":{"message":"Missing required parameter: 'tool_choice.name'"}}`), nil
			}
			return http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"ok"}}]}`), nil
		})

	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, status)
	assert.Equal(t, 2, calls)
	assert.Equal(t, "auto", choices[0])
	assert.Nil(t, choices[1])
}

func TestAssistantNamedToolChoiceDoesNotSerializeEmptyName(t *testing.T) {
	assert.Equal(t, "auto", assistantNamedToolChoice("  "))
}

func TestAssistantNamedToolChoiceUnsupportedRecognizesProviderCapabilityErrors(t *testing.T) {
	assert.True(t, assistantNamedToolChoiceUnsupported([]byte(`{"error":{"message":"当前模型或上游不支持指定工具的强制选择方式，请改用 tool_choice=auto"}}`)))
	assert.True(t, assistantNamedToolChoiceUnsupported([]byte(`{"error":{"message":"provider does not support forced tool_choice"}}`)))
	assert.False(t, assistantNamedToolChoiceUnsupported([]byte(`{"error":{"message":"upstream overloaded"}}`)))
	assert.True(t, assistantServerReadFallbackAllowed("get_l1_recommendation"))
	assert.True(t, assistantServerReadFallbackAllowed("get_available_models"))
	assert.False(t, assistantServerReadFallbackAllowed("prepare_l1_recommendation"))
	assert.False(t, assistantServerReadFallbackAllowed("request_create_key"))
}

func TestAssistantRetryRejectsInvalidInputBeforePersistentWrites(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserOAuthBinding{},
		&model.AssistantUserProfile{},
		&model.TopUp{},
		&model.AssistantLead{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.DeveloperAccessRequest{},
	))
	withAssistantSettings(t, true, "assistant-retry-validation-model")

	user := model.User{
		Username:           "assistant-retry-validation-user",
		Password:           "password",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		Group:              "default",
		ConsoleActivatedAt: 1,
	}
	require.NoError(t, db.Create(&user).Error)

	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", user.Id)
		PrepareAssistantRequest(c)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/assistant/chat",
		strings.NewReader(`{"message":"."}`),
	)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-LMM-Assistant-Attempt", "1")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadRequest, response.Code)
	assert.Contains(t, response.Body.String(), "ASSISTANT_SINGLE_PUNCTUATION")

	var conversationCount int64
	var intentCount int64
	var firstQuestionCount int64
	var developerAccessCount int64
	require.NoError(t, db.Model(&model.AssistantConversation{}).Count(&conversationCount).Error)
	require.NoError(t, db.Model(&model.AssistantLead{}).Count(&intentCount).Error)
	require.NoError(t, db.Model(&model.AssistantFirstQuestionStat{}).Count(&firstQuestionCount).Error)
	require.NoError(t, db.Model(&model.DeveloperAccessRequest{}).Count(&developerAccessCount).Error)
	assert.Zero(t, conversationCount)
	assert.Zero(t, intentCount)
	assert.Zero(t, firstQuestionCount)
	assert.Zero(t, developerAccessCount)
}

func TestAssistantRetryDoesNotQueueL1BeforeConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserOAuthBinding{},
		&model.AssistantUserProfile{},
		&model.TopUp{},
		&model.AssistantLead{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.DeveloperAccessRequest{},
	))
	withAssistantSettings(t, true, "assistant-retry-idempotency-model")
	originalSettings := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(false)
	t.Cleanup(func() { setting.SetAssistantCacheEnabled(originalSettings.CacheEnabled) })

	user := model.User{
		Username: "assistant-retry-l0-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", user.Id)
		PrepareAssistantRequest(c)
	}, func(c *gin.Context) {
		c.Status(http.StatusNoContent)
	})

	message := "Please request L1 developer access for my integration"
	for attempt := 1; attempt <= 2; attempt++ {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/assistant/chat",
			strings.NewReader(`{"message":"`+message+`"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-LMM-Assistant-Attempt", strconv.Itoa(attempt))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, http.StatusNoContent, response.Code)
	}

	var requests []model.DeveloperAccessRequest
	require.NoError(t, db.Where("user_id = ?", user.Id).Find(&requests).Error)
	assert.Empty(t, requests)
}

func TestAssistantChatDoesNotQueueL1BeforeConfirmationOnDownstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserOAuthBinding{},
		&model.AssistantUserProfile{},
		&model.TopUp{},
		&model.AssistantLead{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.DeveloperAccessRequest{},
	))
	withAssistantSettings(t, true, "assistant-l1-downstream-failure-model")
	originalSettings := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(false)
	t.Cleanup(func() { setting.SetAssistantCacheEnabled(originalSettings.CacheEnabled) })

	user := model.User{
		Username: "assistant-l1-downstream-failure-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", user.Id)
		PrepareAssistantRequest(c)
	}, func(c *gin.Context) {
		c.AbortWithStatus(http.StatusBadGateway)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/assistant/chat",
		strings.NewReader(`{"message":"Please request L1 developer access for my integration"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusBadGateway, response.Code)
	queued, err := model.GetDeveloperAccessRequest(user.Id)
	require.NoError(t, err)
	assert.Nil(t, queued)
}

func TestAssistantL1ConversationDoesNotTouchQueueBeforeConfirmation(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.UserOAuthBinding{},
		&model.AssistantUserProfile{},
		&model.TopUp{},
		&model.AssistantLead{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.DeveloperAccessRequest{},
	))
	withAssistantSettings(t, true, "assistant-l1-queue-failure-model")
	originalSettings := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(false)
	t.Cleanup(func() { setting.SetAssistantCacheEnabled(originalSettings.CacheEnabled) })

	user := model.User{
		Username: "assistant-l1-queue-failure-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Migrator().DropTable(&model.DeveloperAccessRequest{}))

	downstreamCalls := 0
	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", user.Id)
		PrepareAssistantRequest(c)
	}, func(c *gin.Context) {
		downstreamCalls++
		c.Status(http.StatusNoContent)
	})

	request := httptest.NewRequest(
		http.MethodPost,
		"/api/assistant/chat",
		strings.NewReader(`{"message":"Please request L1 developer access for my integration"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	engine.ServeHTTP(response, request)

	assert.Equal(t, http.StatusNoContent, response.Code)
	assert.Equal(t, 1, downstreamCalls)
}

func TestAssistantL1RecommendationPreparationDoesNotTouchQueue(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.DeveloperAccessRequest{},
		&model.AuthFlow{},
	))
	withAssistantSettings(t, true, "assistant-l1-recommendation-queue-failure-model")

	levelZero := model.TrustLevelMinUser
	user := model.User{
		Username:           "assistant-l1-recommendation-queue-failure-user",
		Password:           "password",
		Role:               common.RoleCommonUser,
		Status:             common.UserStatusEnabled,
		Group:              "default",
		TrustLevelOverride: &levelZero,
	}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Migrator().DropTable(&model.DeveloperAccessRequest{}))

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set("id", user.Id)
	c.Set(assistantActorUserIDKey, user.Id)
	c.Set("session_id", "assistant-l1-queue-failure-session")

	result := executeAssistantL1RecommendationTool(c, user.Id, map[string]any{
		"user_statement": "I need L1 access for my integration.",
		"recommendation": "The user described a concrete integration workflow and can be reviewed for L1 access.",
	})

	assert.Equal(t, true, result["ok"])
	assert.Equal(t, "confirmation_required", result["status"])
	assert.Equal(t, "l1_recommendation", result["action"])
	assert.Empty(t, recorder.Body.String())

	var flowCount int64
	require.NoError(t, db.Model(&model.AuthFlow{}).Count(&flowCount).Error)
	assert.EqualValues(t, 1, flowCount)
}

func TestAssistantRetryDoesNotDuplicateFirstTurnConversationOnReplay(t *testing.T) {
	gin.SetMode(gin.TestMode)
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.AssistantLead{},
		&model.AssistantProfileBucket{},
		&model.AssistantFirstQuestionStat{},
		&model.User{},
		&model.UserOAuthBinding{},
		&model.AssistantUserProfile{},
		&model.TopUp{},
		&model.DeveloperAccessRequest{},
	))
	withAssistantSettings(t, true, "assistant-retry-replay-model")
	originalSettings := setting.GetAssistantSettings()
	setting.SetAssistantCacheEnabled(true)
	require.NoError(t, setting.UpdateAssistantCacheTTLMinutes("10"))
	t.Cleanup(func() {
		setting.SetAssistantCacheEnabled(originalSettings.CacheEnabled)
		_ = setting.UpdateAssistantCacheTTLMinutes(strconv.Itoa(originalSettings.CacheTTLMinutes))
	})

	user := model.User{
		Username: "assistant-retry-replay-user",
		Password: "password",
		Role:     common.RoleCommonUser,
		Status:   common.UserStatusEnabled,
		Group:    "default",
	}
	require.NoError(t, db.Create(&user).Error)

	message := "replay this assistant answer without creating another conversation"
	settings := setting.GetAssistantSettings()
	userContext := assistantUserContextForRequest(user.Id, message)
	cacheKey := assistantCacheKey(
		settings,
		[]assistantOpenAIMessage{{Role: "user", Content: message}},
		userContext,
	)
	require.NotEmpty(t, cacheKey)
	storeAssistantCachedResponse(
		settings,
		cacheKey,
		http.StatusOK,
		[]byte(`{"choices":[{"message":{"role":"assistant","content":"cached answer"}}]}`),
	)

	engine := gin.New()
	engine.POST("/api/assistant/chat", func(c *gin.Context) {
		c.Set("id", user.Id)
		PrepareAssistantRequest(c)
	})

	// A lost successful response can make the browser replay the same first
	// turn after attempt 1 has already populated the response cache. The
	// replay must attach to the original conversation rather than append a
	// second user/assistant pair.
	for attempt := 1; attempt <= 2; attempt++ {
		request := httptest.NewRequest(
			http.MethodPost,
			"/api/assistant/chat",
			strings.NewReader(`{"message":"`+message+`"}`),
		)
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set("X-LMM-Assistant-Attempt", strconv.Itoa(attempt))
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		assert.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "HIT", response.Header().Get("X-LMM-Assistant-Cache"))
	}

	var conversationCount int64
	var historyMessageCount int64
	var firstQuestion model.AssistantFirstQuestionStat
	require.NoError(t, db.Model(&model.AssistantConversation{}).Count(&conversationCount).Error)
	require.NoError(t, db.Model(&model.AssistantHistoryMessage{}).Count(&historyMessageCount).Error)
	require.NoError(t, db.First(&firstQuestion).Error)
	assert.EqualValues(t, 1, conversationCount)
	assert.EqualValues(t, 2, historyMessageCount)
	assert.EqualValues(t, 1, firstQuestion.Count)
}
