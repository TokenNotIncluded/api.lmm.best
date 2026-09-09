package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssistantPriceLockTest(t *testing.T) (*gin.Context, model.User) {
	t.Helper()
	db := setupTokenControllerTestDB(t)
	c, user, _ := assistantAutomationTestContext(t, db, common.RoleRootUser)
	require.NoError(t, db.AutoMigrate(&model.AuthFlow{}))
	previousRatio, previousPrice := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	previousConfig := config.GlobalConfig.ExportAllConfigs()
	previousName, previousRefresh := common.SystemName, refreshPricingCache
	common.OptionMapRWMutex.Lock()
	previousOptions := common.OptionMap
	common.OptionMap = map[string]string{}
	common.OptionMapRWMutex.Unlock()
	refreshPricingCache = func() error { return nil }
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(previousRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrice))
		require.NoError(t, config.GlobalConfig.LoadFromDB(previousConfig))
		common.SystemName, refreshPricingCache = previousName, previousRefresh
		common.OptionMapRWMutex.Lock()
		common.OptionMap = previousOptions
		common.OptionMapRWMutex.Unlock()
	})
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"ModelRatio": `{"locked-model":2}`, "ModelPrice": "{}", "ModelPriceLock": "{}",
		"billing_setting.billing_mode": "{}", "billing_setting.billing_expr": "{}",
	}))
	return c, user
}

func TestAssistantAdminPriceLockedAfterPreviewReturnsWarning(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		t.Run(map[bool]string{true: "automatic", false: "confirmation"}[automatic], func(t *testing.T) {
			c, user := setupAssistantPriceLockTest(t)
			expected := assistantAdminCurrentPricingState("locked-model")
			payload := assistantAdminChangePayload{Kind: assistantAdminPricingChangeKind, Pricing: &assistantAdminPricingChange{
				ModelID: "locked-model", Mode: "fixed_request", Value: 9, Expected: &expected,
			}}
			token, err := createAssistantAdminFlow(c, user.Id, payload)
			require.NoError(t, err)
			_, err = model.UpdateModelPriceLock("locked-model", true)
			require.NoError(t, err)
			var result map[string]any
			if automatic {
				var handled bool
				result, handled = maybeApplyAssistantAdminAutomatically(c, user.Id, payload)
				require.True(t, handled)
			} else {
				recorder := httptest.NewRecorder()
				applyContext, _ := gin.CreateTestContext(recorder)
				applyContext.Set("id", user.Id)
				applyContext.Set("session_id", "automation-session")
				applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
				ApplyAssistantAdminChange(applyContext)
				require.Equal(t, http.StatusOK, recorder.Code)
				var response struct {
					Success bool           `json:"success"`
					Data    map[string]any `json:"data"`
				}
				require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
				require.True(t, response.Success, "%s", recorder.Body.String())
				result = response.Data
			}
			require.Equal(t, true, result["ok"], "%v", result)
			assert.Equal(t, false, result["applied"])
			assert.Equal(t, "ignored_locked", result["status"])
			assert.NotEmpty(t, result["warnings"])
			assert.Equal(t, expected, assistantAdminCurrentPricingState("locked-model"))
			pricing := result["pricing"].(map[string]any)
			assert.Equal(t, "ratio", pricing["mode"])
			assert.Equal(t, 2.0, pricing["value"])
			var logs []model.Log
			require.NoError(t, model.LOG_DB.Find(&logs).Error)
			require.Len(t, logs, 1)
			assert.Contains(t, logs[0].Other, "ignored_locked")
		})
	}
}

func TestAssistantAdminLockedConfigSkipsInvalidExpressionAndReportsSavedKeys(t *testing.T) {
	c, user := setupAssistantPriceLockTest(t)
	expressions := `{"z-model":"tier(\"base\", p)","locked-model":"tier(\"base\", p)"}`
	require.NoError(t, model.UpdateOption("billing_setting.billing_expr", expressions))
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-model":true,"z-model":true}`))
	result := executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{
		"billing_setting.billing_expr": `{"locked-model":"invalid(","z-model":"invalid("}`,
		"SystemName":                   "changed-unlocked-setting",
	}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, "applied_with_warnings", result["status"])
	assert.Equal(t, []string{"SystemName"}, result["updated_keys"])
	assert.Equal(t, "changed-unlocked-setting", common.SystemName)
	assert.NotEmpty(t, result["warnings"])
	current := assistantAdminCurrentOptions([]string{"billing_setting.billing_expr"})
	assert.JSONEq(t, expressions, current["billing_setting.billing_expr"])
}

func TestAssistantAdminLockedPricingToolIgnoresInvalidProposal(t *testing.T) {
	c, user := setupAssistantPriceLockTest(t)
	_, err := model.UpdateModelPriceLock("locked-model", true)
	require.NoError(t, err)
	result := executeAssistantAdminPricingChangeTool(c, user.Id, map[string]any{
		"model_id": "locked-model", "mode": "unsupported", "value": -1,
	})
	assert.Equal(t, true, result["ok"])
	assert.Equal(t, "ignored_locked", result["status"])
	assert.Equal(t, false, result["applied"])
	assert.NotEmpty(t, result["warnings"])
	assert.Contains(t, result["next_step"], "explicit administrator request")
}
