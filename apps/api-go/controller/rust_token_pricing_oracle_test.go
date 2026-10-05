package controller

import (
	"encoding/json"
	"os"
	"sort"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRustTokenPricingCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_TOKEN_PRICING_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("explicit current Go token pricing export was not selected")
	}
	previous := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { previous[key] = value; return nil }))
	oldUnit := common.QuotaPerUnit
	oldFX, oldBase := operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	oldAnchor, anchorErr := common.CreditsPerUSD()
	oldLegacy, legacyErr := common.LegacyPricingQuotaPerUnit()
	oldCache, oldCreateCache := ratio_setting.CacheRatio2JSONString(), ratio_setting.CreateCacheRatio2JSONString()
	oldRatio, oldPrice, oldCompletion := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString(), ratio_setting.CompletionRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit = oldUnit
		operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldFX, oldBase
		if anchorErr != nil || legacyErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldAnchor, oldLegacy))
		}
		require.NoError(t, config.GlobalConfig.LoadFromDB(previous))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatio))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrice))
		require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(oldCompletion))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(oldCache))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(oldCreateCache))
	})
	options := map[string]string{
		"QuotaPerUnit":                          "500000",
		"CreditsPerUSD":                         "3500000",
		"LegacyPricingQuotaPerUnit":             "500000",
		"USDExchangeRate":                       "7",
		"TopUpPlatformUnitsPerCNY":              "1",
		"ModelRatio":                            `{"gpt-4o":1.25,"secret":2,"zero":0,"claude-3-5-sonnet":2,"vendor/claude-3-5-sonnet":2,"gpt-4-gizmo-*":1}`,
		"CacheRatio":                            `{"gpt-4o":0.5,"claude-3-5-sonnet":0.1}`,
		"CreateCacheRatio":                      `{"claude-3-5-sonnet":1.25}`,
		"ModelPrice":                            `{"image":0.04}`,
		"CompletionRatio":                       `{"gpt-4o":3,"claude-3-5-sonnet":7,"vendor/claude-3-5-sonnet":7}`,
		"group_ratio_setting.group_ratio":       `{"default":1,"vip":2,"private":3}`,
		"group_ratio_setting.group_group_ratio": `{"vip":{"default":0.5}}`,
		"billing_setting.billing_mode":          `{"tier":"tiered_expr","missing-expr":"tiered_expr"}`,
		"billing_setting.billing_expr":          `{"tier":"p * 2 + c * 4"}`,
		"UserUsableGroups":                      `{"auto":"Auto","default":"Default","vip":"VIP","private":"Private"}`,
	}
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 7, 1
	// One migrated site: initial Q=500000, CNY/USD=7, base recharge ratio=1.
	// K remains 3500000 even when the live FX or a later recharge bonus changes.
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(options["ModelRatio"]))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(options["ModelPrice"]))
	require.NoError(t, ratio_setting.UpdateCompletionRatioByJSONString(options["CompletionRatio"]))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(options["CacheRatio"]))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(options["CreateCacheRatio"]))
	require.NoError(t, config.GlobalConfig.LoadFromDB(options))
	catalog := []model.Pricing{}
	for _, name := range []string{"gpt-4o", "image", "tier", "missing-expr", "zero", "claude-3-5-sonnet", "vendor/claude-3-5-sonnet", "gpt-4-gizmo-demo", "unpriced"} {
		catalog = append(catalog, model.Pricing{ModelName: name, EnableGroup: []string{"default", "private"}})
	}
	catalog = append(catalog, model.Pricing{ModelName: "secret", EnableGroup: []string{"private"}})
	scenarios := []struct {
		Name, UserGroup, Requested string
		Groups                     []string
		Limited                    bool
		Limits                     map[string]bool
		Discount                   float64
	}{
		{Name: "default-discount", UserGroup: "default", Groups: []string{"default"}, Discount: 0.97},
		{Name: "group-override", UserGroup: "vip", Groups: []string{"default"}, Discount: 0.94},
		{Name: "two-groups", UserGroup: "default", Groups: []string{"default", "private"}, Discount: 1},
		{Name: "model-limit", UserGroup: "default", Groups: []string{"default"}, Limited: true, Limits: map[string]bool{"gpt-4o": true}, Discount: 1},
		{Name: "wildcard-limit", UserGroup: "default", Groups: []string{"default"}, Limited: true, Limits: map[string]bool{"gpt-4-gizmo-*": true}, Discount: 1},
		{Name: "expression", UserGroup: "vip", Groups: []string{"default"}, Requested: "tier", Discount: 0.97},
	}
	cases := []map[string]any{}
	for _, scenario := range scenarios {
		entries := tokenPricingEntries(catalog, modelListGroups{userGroup: scenario.UserGroup, ownerGroups: scenario.Groups}, scenario.Limited, scenario.Limits, scenario.Discount, scenario.Requested)
		require.NotEmpty(t, entries, "oracle must execute priced scope %s", scenario.Name)
		for _, entry := range entries {
			require.Equal(t, 2, entry.PricingSchemaVersion)
			require.Equal(t, "USD", entry.Currency)
		}
		// The process-wide Go catalogue is map-derived: compare stable model/group identities.
		sort.Slice(entries, func(i, j int) bool {
			if entries[i].Model == entries[j].Model {
				return entries[i].Group < entries[j].Group
			}
			return entries[i].Model < entries[j].Model
		})
		cases = append(cases, map[string]any{"name": scenario.Name, "user_group": scenario.UserGroup, "groups": scenario.Groups, "requested": scenario.Requested, "limited": scenario.Limited, "limits": scenario.Limits, "discount": scenario.Discount, "entries": entries})
	}
	floatVectors := []map[string]string{}
	for _, raw := range []string{"0", "-0", "1", "1.25", "0.000001", "0.0000001", "1000000000000000100", "1e20", "1e21", "1e-20", "-1e-20", "9007199254740992", "1.7976931348623157e308", "5e-324"} {
		value, err := strconv.ParseFloat(raw, 64)
		require.NoError(t, err)
		encoded, err := json.Marshal(value)
		require.NoError(t, err)
		floatVectors = append(floatVectors, map[string]string{"input": raw, "json": string(encoded)})
	}
	encoded, err := json.MarshalIndent(map[string]any{"options": options, "catalog": catalog, "cases": cases, "float_json": floatVectors}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
