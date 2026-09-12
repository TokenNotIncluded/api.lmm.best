package controller

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestTokenPricingUsesLiveRatesAndPermissionBoundary(t *testing.T) {
	oldUnit := common.QuotaPerUnit
	oldModels, oldPrices := ratio_setting.ModelRatio2JSONString(), ratio_setting.ModelPrice2JSONString()
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"gpt-4o":1.25}`))
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"dall-e-3":0.04}`))
	t.Cleanup(func() {
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldModels))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(oldPrices))
	})
	common.QuotaPerUnit = 500000
	t.Cleanup(func() { common.QuotaPerUnit = oldUnit })
	catalog := []model.Pricing{
		{ModelName: "gpt-4o", ModelRatio: 999, EnableGroup: []string{"default", "private"}},
		{ModelName: "dall-e-3", EnableGroup: []string{"default"}},
		{ModelName: "secret-model", EnableGroup: []string{"private"}},
	}
	groups := modelListGroups{userGroup: "default", ownerGroups: []string{"default"}}
	rows := tokenPricingEntries(catalog, groups, false, nil, 0.97, "")
	require.Len(t, rows, 2)
	modelRatio, _, _ := ratio_setting.GetModelRatio("gpt-4o")
	require.InDelta(t, modelRatio*2*0.97, *rows[0].InputPrice, 1e-10)
	require.InDelta(t, *rows[0].InputPrice*ratio_setting.GetCompletionRatio("gpt-4o"), *rows[0].OutputPrice, 1e-10)
	require.Equal(t, "million_tokens", rows[0].Unit)
	require.Equal(t, "request", rows[1].Unit)
	price, _ := ratio_setting.GetModelPrice("dall-e-3", false)
	require.InDelta(t, price*0.97, *rows[1].RequestPrice, 1e-10)
	require.Empty(t, tokenPricingEntries(catalog, groups, true, nil, 1, ""))
	rows = tokenPricingEntries(catalog, groups, true, map[string]bool{"gpt-4o": true}, 1, "gpt-4o")
	require.Len(t, rows, 1)
	require.Equal(t, "default", rows[0].Group)
	require.Empty(t, tokenPricingEntries(catalog, groups, false, nil, 1, "secret-model"))
	require.Empty(t, tokenPricingEntries(catalog, groups, false, nil, 1, "unknown-model"))
	groups.ownerGroups = []string{"default", "private", "default"}
	rows = tokenPricingEntries(catalog[:1], groups, false, nil, 1, "")
	require.Len(t, rows, 2)
}

func TestTokenPricingReportsExpressionInsteadOfFixedQuote(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":          `{"tiered-query":"tiered_expr"}`,
		"billing_setting.billing_expr":          `{"tiered-query":"p * 2 + c * 4"}`,
		"group_ratio_setting.group_ratio":       `{"default":3}`,
		"group_ratio_setting.group_group_ratio": `{"vip":{"default":0.5}}`,
	}))
	rows := tokenPricingEntries([]model.Pricing{{ModelName: "tiered-query", EnableGroup: []string{"default"}}}, modelListGroups{userGroup: "vip", ownerGroups: []string{"default"}}, false, nil, 0.97, "tiered-query")
	require.Len(t, rows, 1)
	require.Equal(t, "expression", rows[0].Unit)
	require.Equal(t, "p * 2 + c * 4", rows[0].Expression)
	require.InDelta(t, 0.485, rows[0].GroupRatio, 1e-10)
	require.Nil(t, rows[0].InputPrice)
	require.Nil(t, rows[0].OutputPrice)
	require.Nil(t, rows[0].RequestPrice)
}
