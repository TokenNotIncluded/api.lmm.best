package controller

import (
	"encoding/json"
	"fmt"
	"math"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestAssistantAdminPricingAuditFlagsMissingConfigurationInsteadOfTrustingFallback(t *testing.T) {
	row := assistantAdminPricingAuditRow(model.Pricing{ModelName: "unpriced-model", ModelRatio: 37.5, CompletionRatio: 1, EnableGroup: []string{"missing-group"}}, nil, nil, nil)
	assert.Contains(t, row["issues"], "missing_configured_token_ratio")
	assert.Equal(t, float64(75), row["base_input_usd_per_million"])
	groups := row["groups"].([]map[string]any)
	assert.Equal(t, "missing_group_ratio_uses_default_one", groups[0]["issue"])
	_, err := json.Marshal(row)
	require.NoError(t, err)
}

func TestAssistantAdminPricingAuditDistinguishesFreeInvalidAndConflictingPrices(t *testing.T) {
	row := assistantAdminPricingAuditRow(model.Pricing{ModelName: "free-model", QuotaType: 1, ModelPrice: 0, EnableGroup: []string{"default"}}, map[string]float64{"free-model": 1}, map[string]float64{"free-model": 0}, map[string]float64{"default": 1})
	assert.Contains(t, row["issues"], "zero_price_review_intent")
	assert.Contains(t, row["issues"], "both_fixed_and_token_rates_configured")
	assert.NotContains(t, row["issues"], "missing_configured_fixed_price")
	invalid := math.Inf(1)
	row = assistantAdminPricingAuditRow(model.Pricing{ModelName: "bad-model", ModelRatio: math.NaN(), CacheRatio: &invalid}, nil, nil, nil)
	assert.Contains(t, row["issues"], "invalid_token_ratio")
	assert.Contains(t, row["issues"], "invalid_cache_ratio")
	_, err := json.Marshal(row)
	require.NoError(t, err, "nonfinite source values must never poison the tool result")
	row = assistantAdminPricingAuditRow(model.Pricing{ModelName: "overflow-model", ModelRatio: math.MaxFloat64, CompletionRatio: 2}, nil, nil, nil)
	assert.Contains(t, row["issues"], "invalid_token_ratio")
	_, err = json.Marshal(row)
	require.NoError(t, err, "finite input ratios may still overflow their calculated display rates")
}

func TestAssistantAdminPricingAuditPaginatesAndReportsUnknownIDs(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.Channel{}))
	user := model.User{Username: "pricing-audit-admin", Password: "password", Role: common.RoleAdminUser, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	previousPricing := getPricingCache
	getPricingCache = func() []model.Pricing {
		rows := make([]model.Pricing, 105)
		for index := range rows {
			rows[index] = model.Pricing{ModelName: fmt.Sprintf("model-%03d", 104-index), ModelRatio: 1, CompletionRatio: 1, EnableGroup: []string{"default"}}
		}
		return rows
	}
	t.Cleanup(func() { getPricingCache = previousPricing })
	result := executeAssistantAdminPricingAuditTool(user.Id, nil)
	require.Equal(t, true, result["ok"])
	assert.Equal(t, 105, result["total_models"])
	assert.Equal(t, 100, result["next_offset"])
	rows := result["models"].([]map[string]any)
	require.Len(t, rows, 100)
	assert.Equal(t, "model-000", rows[0]["model_id"])
	result = executeAssistantAdminPricingAuditTool(user.Id, map[string]any{"offset": float64(100)})
	assert.Len(t, result["models"], 5)
	assert.NotContains(t, result, "next_offset")
	result = executeAssistantAdminPricingAuditTool(user.Id, map[string]any{"model_ids": []any{"missing-model"}})
	assert.Equal(t, []string{"missing-model"}, result["missing_requested_models"])
	assert.Empty(t, result["models"])
	assert.Equal(t, false, executeAssistantAdminPricingAuditTool(user.Id, map[string]any{"offset": 0.5})["ok"])
	assert.Equal(t, false, executeAssistantAdminPricingAuditTool(user.Id, map[string]any{"limit": float64(101)})["ok"])
	require.NoError(t, db.Model(&user).Update("role", common.RoleCommonUser).Error)
	assert.Equal(t, false, executeAssistantAdminPricingAuditTool(user.Id, nil)["ok"])
}
