package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPromptPresetControllerTestDB(t *testing.T) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.PromptPresetRow{},
		&model.PromptPresetStat{},
		&model.PromptConversionRef{},
		&model.PromptConversationRef{},
	))
}

func TestPromptPresetPublicReadAndClick(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPromptPresetControllerTestDB(t)
	engine := gin.New()
	engine.GET("/api/assistant/pre-conversation-presets", GetPromptPresets)
	engine.POST("/api/assistant/pre-conversation-presets/:id/click", CountPromptPresetClick)

	readResponse := httptest.NewRecorder()
	engine.ServeHTTP(readResponse, httptest.NewRequest(http.MethodGet, "/api/assistant/pre-conversation-presets", nil))
	require.Equal(t, http.StatusOK, readResponse.Code)
	assert.Equal(t, "public, max-age=300", readResponse.Header().Get("Cache-Control"))
	var envelope struct {
		Success bool                  `json:"success"`
		Data    model.PromptPresetSet `json:"data"`
	}
	require.NoError(t, json.Unmarshal(readResponse.Body.Bytes(), &envelope))
	assert.True(t, envelope.Success)
	require.NotEmpty(t, envelope.Data.Presets)
	assert.LessOrEqual(t, len(envelope.Data.Presets), 4)

	clickResponse := httptest.NewRecorder()
	engine.ServeHTTP(clickResponse, httptest.NewRequest(http.MethodPost, "/api/assistant/pre-conversation-presets/"+envelope.Data.Presets[0].Id+"/click", nil))
	assert.Equal(t, http.StatusOK, clickResponse.Code)

	unknownResponse := httptest.NewRecorder()
	engine.ServeHTTP(unknownResponse, httptest.NewRequest(http.MethodPost, "/api/assistant/pre-conversation-presets/stale/click", nil))
	assert.Equal(t, http.StatusNotFound, unknownResponse.Code)
}

func TestPromptPresetPublicReadLocalizesWithoutChangingAttribution(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPromptPresetControllerTestDB(t)
	engine := gin.New()
	engine.GET("/api/assistant/pre-conversation-presets", GetPromptPresets)
	seed, err := model.GetPromptPresets()
	require.NoError(t, err)
	for _, language := range []string{"en", "zh", "zh-TW", "fr", "ja", "ru", "vi", "zhCN", "zhTW", "unsupported"} {
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/assistant/pre-conversation-presets?language="+url.QueryEscape(language), nil))
		require.Equal(t, http.StatusOK, response.Code)
		assert.Equal(t, "public, max-age=300", response.Header().Get("Cache-Control"))
		var envelope struct {
			Success bool                  `json:"success"`
			Data    model.PromptPresetSet `json:"data"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &envelope))
		assert.True(t, envelope.Success)
		assert.Equal(t, model.LocalizePromptPresets(seed, language), envelope.Data)
		for _, preset := range envelope.Data.Presets {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			capturePromptPresetRef(c, preset.Id, preset.Prompt)
			ref, ok := promptPresetRef(c)
			require.True(t, ok, language+"/"+preset.Id)
			assert.Equal(t, seed.Generation, ref.Generation)
			assert.Equal(t, seed.Version, ref.Version)
			assert.Equal(t, preset.Id, ref.PresetId)

			invalid, _ := gin.CreateTestContext(httptest.NewRecorder())
			capturePromptPresetRef(invalid, preset.Id, preset.Prompt+" modified")
			_, ok = promptPresetRef(invalid)
			assert.False(t, ok)
			assert.False(t, invalid.GetBool(promptPresetCountKey))
		}
	}
}

func TestAssistantPresetConversationCountsOnlySuccessfulRecordedTurn(t *testing.T) {
	gin.SetMode(gin.TestMode)
	setupPromptPresetControllerTestDB(t)
	owner := model.User{Username: "preset-conversation-owner", Password: "password", AffCode: "preset-conversation-owner"}
	require.NoError(t, model.DB.Create(&owner).Error)
	set, err := model.GetPromptPresets()
	require.NoError(t, err)
	set = model.LocalizePromptPresets(set, "fr")
	require.NotEmpty(t, set.Presets)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Set(assistantActorUserIDKey, owner.Id)
	c.Set("assistant_history_latest_message", set.Presets[0].Prompt)
	capturePromptPresetRef(c, set.Presets[0].Id, set.Presets[0].Prompt)

	recordAssistantHistoryResponse(c, http.StatusBadGateway, []byte(`{"error":"upstream failed"}`))
	var before int64
	require.NoError(t, model.DB.Model(&model.PromptPresetStat{}).Count(&before).Error)
	assert.Zero(t, before)

	recordAssistantHistoryResponse(c, http.StatusOK, []byte(`{"choices":[{"message":{"role":"assistant","content":"可以，我们从你的实际需求开始。"}}]}`))
	var stat model.PromptPresetStat
	require.NoError(t, model.DB.Where("preset_id = ?", set.Presets[0].Id).First(&stat).Error)
	assert.EqualValues(t, 1, stat.ConversationCount)
}
