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
	c, user := setupAssistantPriceLockTest(t)
	expected := assistantAdminCurrentPricingState("locked-model")
	payload := assistantAdminChangePayload{Kind: assistantAdminPricingChangeKind, Pricing: &assistantAdminPricingChange{
		ModelID: "locked-model", Mode: "fixed_request", Value: 9, Expected: &expected,
	}}
	token, err := createAssistantAdminFlow(c, user.Id, payload)
	require.NoError(t, err)
	result, handled := maybeApplyAssistantAdminAutomatically(c, user.Id, payload)
	require.False(t, handled)
	require.Nil(t, result)
	assert.Equal(t, expected, assistantAdminCurrentPricingState("locked-model"))
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.Empty(t, logs)

	_, err = model.UpdateModelPriceLock("locked-model", true)
	require.NoError(t, err)
	result = applyAssistantPriceLockTestConfirmation(t, user.Id, token)
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, false, result["applied"])
	assert.Equal(t, "ignored_locked", result["status"])
	assert.NotEmpty(t, result["warnings"])
	assert.Equal(t, expected, assistantAdminCurrentPricingState("locked-model"))
	pricing := result["pricing"].(map[string]any)
	assert.Equal(t, "ratio", pricing["mode"])
	assert.Equal(t, 2.0, pricing["value"])
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0].Other, "ignored_locked")
}

func applyAssistantPriceLockTestConfirmation(t *testing.T, userID int, token string) map[string]any {
	t.Helper()
	recorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(recorder)
	applyContext.Set("id", userID)
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
	_, err := model.GetAuthFlow(token, model.AuthFlowMatch{
		Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: userID, SessionId: "automation-session",
	})
	require.ErrorIs(t, err, model.ErrAuthFlowConsumed)
	return response.Data
}

func TestAssistantAdminLockedConfigSkipsInvalidExpressionAndReportsSavedKeys(t *testing.T) {
	c, user := setupAssistantPriceLockTest(t)
	expressions := `{"z-model":"tier(\"base\", p)","locked-model":"tier(\"base\", p)"}`
	require.NoError(t, model.UpdateOption("billing_setting.billing_expr", expressions))
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-model":true,"z-model":true}`))
	previousName := common.SystemName
	result := executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{
		"billing_setting.billing_expr": `{"locked-model":"invalid(","z-model":"invalid("}`,
		"SystemName":                   "changed-unlocked-setting",
	}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, "confirmation_required", result["status"])
	assert.Equal(t, previousName, common.SystemName)
	assert.NotEmpty(t, result["warnings"])
	action, ok := c.Get(assistantClientActionKey)
	require.True(t, ok)
	actionMap, ok := action.(map[string]any)
	require.True(t, ok)
	require.Equal(t, true, actionMap["requires_confirmation"])
	token, ok := actionMap["confirmation_token"].(string)
	require.True(t, ok)
	require.NotEmpty(t, token)
	var logs []model.Log
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.Empty(t, logs)

	result = applyAssistantPriceLockTestConfirmation(t, user.Id, token)
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Equal(t, "applied_with_warnings", result["status"])
	assert.ElementsMatch(t, []string{"SystemName"}, result["updated_keys"])
	assert.Equal(t, "changed-unlocked-setting", common.SystemName)
	assert.NotEmpty(t, result["warnings"])
	current := assistantAdminCurrentOptions([]string{"billing_setting.billing_expr"})
	assert.JSONEq(t, expressions, current["billing_setting.billing_expr"])
	require.NoError(t, model.LOG_DB.Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Contains(t, logs[0].Other, "applied_with_warnings")
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
