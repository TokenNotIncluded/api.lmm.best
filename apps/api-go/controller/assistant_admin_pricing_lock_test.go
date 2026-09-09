package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/billing_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAssistantPricingLockTest(t *testing.T) (*gin.Context, model.User, model.UserSession) {
	t.Helper()
	setupPricingLockControllerTest(t)
	c, user, session := assistantAutomationTestContext(t, model.DB, common.RoleRootUser)
	previousPrices := ratio_setting.ModelPrice2JSONString()
	billing := config.GlobalConfig.Get("billing_setting").(*billing_setting.BillingSetting)
	previousModes := billing_setting.GetBillingModeCopy()
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(previousPrices))
		billing.BillingMode = previousModes
	})
	require.NoError(t, model.UpdateOptionsBulk(map[string]string{
		"ModelPrice":                   `{"locked-model":2,"open-model":3}`,
		"billing_setting.billing_mode": `{"locked-model":"ratio","open-model":"ratio"}`,
	}))
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{"locked-model":true}`))
	return c, user, session
}

func TestAssistantLockedPricingIgnoresInvalidInputAfterAuthorization(t *testing.T) {
	for _, scenario := range []string{"active", "revoked", "demoted"} {
		t.Run(scenario, func(t *testing.T) {
			c, user, session := setupAssistantPricingLockTest(t)
			switch scenario {
			case "revoked":
				require.NoError(t, model.DB.Model(&session).Update("status", model.UserSessionStatusRevoked).Error)
			case "demoted":
				require.NoError(t, model.DB.Model(&user).Update("role", common.RoleCommonUser).Error)
			}
			result := executeAssistantAdminPricingChangeTool(c, user.Id, map[string]any{
				"model_id": "locked-model", "mode": "invalid", "value": "invalid", "audio_ratio": -1.0,
			})
			if scenario == "active" {
				assert.Equal(t, true, result["ok"], "%v", result)
				assert.Equal(t, "ignored", result["status"])
				assert.Equal(t, false, result["applied"])
				assert.NotEmpty(t, result["warnings"])
			} else {
				assert.Equal(t, false, result["ok"], "%v", result)
				assert.NotContains(t, result, "warnings")
			}
			assert.Equal(t, 2.0, ratio_setting.GetModelPriceCopy()["locked-model"])
			var logs int64
			require.NoError(t, model.DB.Model(&model.Log{}).Count(&logs).Error)
			assert.Zero(t, logs, "ignored or unauthorized prices must not be audited as applied")
		})
	}
}

func TestAssistantConfigLockFiltersInvalidEntriesAndClearsPreviousWarnings(t *testing.T) {
	c, user, _ := setupAssistantPricingLockTest(t)
	result := executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{
		"billing_setting.billing_mode": `{"locked-model":"invalid","open-model":"tiered_expr"}`,
	}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.NotEmpty(t, result["warnings"])
	assert.Equal(t, "ratio", billing_setting.GetBillingMode("locked-model"))
	assert.Equal(t, "tiered_expr", billing_setting.GetBillingMode("open-model"))

	result = executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{
		"billing_setting.billing_mode": `{"locked-model":"ratio","open-model":"ratio"}`,
	}})
	require.Equal(t, true, result["ok"], "%v", result)
	assert.Empty(t, result["warnings"], "later tools must not inherit an earlier pricing warning")
	assert.Equal(t, "ratio", billing_setting.GetBillingMode("open-model"))
}

func TestAssistantConfigPreviewCannotRestoreIgnoredPriceAfterUnlock(t *testing.T) {
	c, user, session := setupAssistantPricingLockTest(t)
	require.NoError(t, model.DB.AutoMigrate(&model.AuthFlow{}))
	c.Set(assistantAdminAutomationContextKey, false)
	result := executeAssistantAdminConfigChangeTool(c, user.Id, map[string]any{"changes": map[string]any{
		"billing_setting.billing_mode": `{"locked-model":"tiered_expr","open-model":"tiered_expr"}`,
	}})
	require.Equal(t, true, result["ok"], "%v", result)
	require.NotEmpty(t, result["warnings"])
	action, exists := c.Get(assistantClientActionKey)
	require.True(t, exists)
	token := action.(map[string]any)["confirmation_token"].(string)
	flow, err := model.GetAuthFlow(token, model.AuthFlowMatch{Purpose: model.AuthFlowPurposeAssistantAdmin, UserId: user.Id, SessionId: session.SID})
	require.NoError(t, err)
	var payload assistantAdminChangePayload
	require.NoError(t, json.Unmarshal([]byte(flow.Payload), &payload))
	require.JSONEq(t, `{"locked-model":"ratio","open-model":"tiered_expr"}`, payload.ConfigChanges["billing_setting.billing_mode"])
	require.NotEmpty(t, payload.PricingWarnings)
	previews := result["changes"].([]assistantAdminConfigPreview)
	require.Len(t, previews, 1)
	require.JSONEq(t, payload.ConfigChanges["billing_setting.billing_mode"], previews[0].NewValue)

	// The old token must apply exactly what was displayed even after an unlock.
	require.NoError(t, model.UpdateOption("ModelPriceLock", `{}`))
	recorder := httptest.NewRecorder()
	applyContext, _ := gin.CreateTestContext(recorder)
	applyContext.Request = httptest.NewRequest(http.MethodPost, "/api/assistant/admin/apply", strings.NewReader(`{"confirmed":true,"confirmation_token":"`+token+`"}`))
	applyContext.Request.Header.Set("Content-Type", "application/json")
	applyContext.Set("id", user.Id)
	applyContext.Set("session_id", session.SID)
	ApplyAssistantAdminChange(applyContext)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Applied  bool     `json:"applied"`
			Warnings []string `json:"warnings"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success, recorder.Body.String())
	require.True(t, response.Data.Applied)
	require.NotEmpty(t, response.Data.Warnings)
	assert.Equal(t, "ratio", billing_setting.GetBillingMode("locked-model"))
	assert.Equal(t, "tiered_expr", billing_setting.GetBillingMode("open-model"))
}

func TestAssistantPricingLockOverridesStalePreviewWithoutError(t *testing.T) {
	c, _, _ := setupAssistantPricingLockTest(t)
	// Lock protection runs before stale-value checks for an in-flight preview.
	err := applyAssistantAdminChange(c, assistantAdminChangePayload{
		Kind:    assistantAdminPricingChangeKind,
		Pricing: &assistantAdminPricingChange{ModelID: "locked-model", Mode: "fixed_request", Value: 99},
	})
	require.NoError(t, err)
	assert.True(t, c.GetBool(assistantAdminPricingIgnoredContextKey))
	assert.NotEmpty(t, assistantAdminPricingWarnings(c))
	assert.Equal(t, 2.0, ratio_setting.GetModelPriceCopy()["locked-model"])
}
