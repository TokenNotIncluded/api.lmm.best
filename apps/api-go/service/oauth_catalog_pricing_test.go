package service

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/stretchr/testify/require"
)

func TestOAuthPricingConvertsPlatformUnitsToUSDOnce(t *testing.T) {
	oldQuota, oldFX, oldPurchase := common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	oldRatios, oldCaches, oldCreates := ratio_setting.ModelRatio2JSONString(), ratio_setting.CacheRatio2JSONString(), ratio_setting.CreateCacheRatio2JSONString()
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = oldQuota, oldFX, oldPurchase
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(oldRatios))
		require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(oldCaches))
		require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(oldCreates))
	})
	common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 500000, 7.2, 1.25
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"oauth-pricing-test":1}`))
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{"oauth-pricing-test":0.1}`))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{"oauth-pricing-test":0.2}`))

	p := oauthPricing("oauth-pricing-test", floatPtr(2), floatPtr(0.5), 1)
	require.Equal(t, "USD", p.Currency)
	require.Equal(t, "configured_base_rates", p.PriceBasis)
	require.InDelta(t, 2.0/9.0, *p.Input, 1e-12)
	require.InDelta(t, 2.0/9.0, *p.Output, 1e-12)
	require.InDelta(t, 0.2/9.0, *p.CacheRead, 1e-12)
	require.InDelta(t, 0.4/9.0, *p.CacheWrite, 1e-12)
	require.NotNil(t, p.NativeCost)
	require.NoError(t, ratio_setting.UpdateCacheRatioByJSONString(`{}`))
	require.NoError(t, ratio_setting.UpdateCreateCacheRatioByJSONString(`{}`))
	p = oauthPricing("oauth-pricing-test", floatPtr(2), floatPtr(0.5), 1)
	require.InDelta(t, 2.0/9.0, *p.CacheRead, 1e-12)
	require.InDelta(t, 2.5/9.0, *p.CacheWrite, 1e-12)
	require.NotNil(t, p.NativeCost)
}

func TestOAuthPricingUnknownAndMissingCacheStayNull(t *testing.T) {
	p := oauthPricing("definitely-unknown-oauth-model", nil, floatPtr(1), 1)
	require.Nil(t, p.Input)
	require.Nil(t, p.NativeCost)
}

func TestOAuthPricingRejectsNonFinitePlatformAmount(t *testing.T) {
	old := ratio_setting.ModelRatio2JSONString()
	t.Cleanup(func() { require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(old)) })
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"oauth-overflow-test":1}`))
	p := oauthPricing("oauth-overflow-test", floatPtr(1e308), floatPtr(1e308), 1)
	require.Nil(t, p.Input)
}

func floatPtr(value float64) *float64 { return &value }
