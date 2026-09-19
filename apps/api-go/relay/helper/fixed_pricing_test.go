package helper

import (
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

func TestFixedPricesAndTokenTiersDoNotRequireChannelCost(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error { saved[key] = value; return nil }))
	prices, ratios := ratio_setting.ModelPrice2JSONString(), ratio_setting.ModelRatio2JSONString()
	quotaUnit, preconsume := common.QuotaPerUnit, common.PreConsumedQuota
	t.Cleanup(func() {
		require.NoError(t, config.GlobalConfig.LoadFromDB(saved))
		require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(prices))
		require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(ratios))
		common.QuotaPerUnit, common.PreConsumedQuota = quotaUnit, preconsume
	})
	common.QuotaPerUnit, common.PreConsumedQuota = 500000, 100
	require.NoError(t, ratio_setting.UpdateModelPriceByJSONString(`{"fixed-without-profit":0.04}`))
	require.NoError(t, ratio_setting.UpdateModelRatioByJSONString(`{"ratio-without-profit":10}`))
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"group_ratio_setting.group_ratio": `{"fixed-test":2}`,
		"billing_setting.billing_mode":    `{"tiered-without-profit":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"tiered-without-profit":"tier(\"base\", p * 2)"}`,
	}))
	for _, tc := range []struct {
		model string
		want  int
	}{
		{"fixed-without-profit", 40000}, {"ratio-without-profit", 20000}, {"tiered-without-profit", 2000},
	} {
		t.Run(tc.model, func(t *testing.T) {
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Set("group", "fixed-test")
			info := &relaycommon.RelayInfo{OriginModelName: tc.model, UserGroup: "fixed-test", UsingGroup: "fixed-test", BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`)}}
			price, err := ModelPriceHelper(c, info, 1000, &types.TokenCountMeta{})
			require.NoError(t, err)
			require.Equal(t, tc.want, price.QuotaToPreConsume)
			require.Equal(t, 2.0, price.GroupRatioInfo.GroupRatio)
			require.NotContains(t, price.OtherRatios(), "dynamic_pricing")
			if tc.model == "tiered-without-profit" {
				require.NotNil(t, info.TieredBillingSnapshot)
				require.Equal(t, tc.want, info.TieredBillingSnapshot.EstimatedQuotaAfterGroup)
			}
		})
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Set("group", "fixed-test")
	price, err := ModelPriceHelperPerCall(c, &relaycommon.RelayInfo{OriginModelName: "fixed-without-profit", UserGroup: "fixed-test", UsingGroup: "fixed-test"})
	require.NoError(t, err)
	require.Equal(t, 40000, price.Quota)
	require.Equal(t, 1.0, price.OtherRatioMultiplier())
}
