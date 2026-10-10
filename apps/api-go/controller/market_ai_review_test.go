package controller

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func marketAISettingsRequest(t *testing.T, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest("PUT", "/api/security/market-ai-review/settings", strings.NewReader(body))
	UpdateMarketAIReviewSettings(c)
	return w
}

func TestMarketAISettingsOnlyWritesModesAndRequiresOfficialRoute(t *testing.T) {
	db := setupSecurityModerationDB(t)
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = make(map[string]string)
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	policy := `{"legacy":{"mode":"strict","amount_currency":"USD","category_fines_usd":{"hate":12}}}`
	require.NoError(t, db.Create(&model.Option{Key: setting.ModerationGroupPoliciesOptionKey, Value: policy}).Error)
	for _, body := range []string{`null`, `{}`, `{"tool_mode":"off"}`, `{"tool_mode":"AUTO","store_mode":"off"}`, `{"tool_mode":"off","store_mode":"off","review_model":"gpt-5"}`, `{"tool_mode":"off","store_mode":"off"} {}`} {
		w := marketAISettingsRequest(t, body)
		require.Equal(t, 400, w.Code, body)
	}
	w := marketAISettingsRequest(t, `{"tool_mode":"assist","store_mode":"auto"}`)
	require.Contains(t, w.Body.String(), `"success":false`, "enabling without a real official route must fail")
	s, err := model.ReadMarketAIReviewSettings(context.Background())
	require.NoError(t, err)
	require.Equal(t, setting.MarketAIReviewOff, s.ToolMode)
	require.Equal(t, setting.MarketAIReviewOff, s.StoreMode)
	channel := model.Channel{Type: constant.ChannelTypeOpenAI, Status: common.ChannelStatusEnabled, Models: setting.DefaultModerationModel, Key: "offline-fixture-key-never-sent"}
	require.NoError(t, db.Create(&channel).Error)
	require.NoError(t, db.Create(&model.Ability{Group: setting.DefaultModerationGroup, Model: setting.DefaultModerationModel, ChannelId: channel.Id, Enabled: true}).Error)
	w = marketAISettingsRequest(t, `{"tool_mode":"assist","store_mode":"auto"}`)
	require.Equal(t, 200, w.Code, w.Body.String())
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			ToolMode        string   `json:"tool_mode"`
			StoreMode       string   `json:"store_mode"`
			SupportedInputs []string `json:"supported_inputs"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success, w.Body.String())
	require.Equal(t, "assist", response.Data.ToolMode)
	require.Equal(t, "auto", response.Data.StoreMode)
	require.Equal(t, []string{"text"}, response.Data.SupportedInputs)
	var saved model.Option
	require.NoError(t, db.Where("key = ?", setting.ModerationGroupPoliciesOptionKey).First(&saved).Error)
	require.Equal(t, policy, saved.Value, "market mode updates cannot rewrite chat fines")
	require.NotContains(t, w.Body.String(), channel.Key)
	require.NotContains(t, w.Body.String(), "category_fines")
	// Disabling remains possible after the provider route disappears.
	require.NoError(t, db.Delete(&channel).Error)
	w = marketAISettingsRequest(t, `{"tool_mode":"off","store_mode":"off"}`)
	require.Contains(t, w.Body.String(), `"success":true`)
}
