package helper

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSystemOneTieredReserveDoesNotIncludeCompletionTokens(t *testing.T) {
	saved := map[string]string{}
	require.NoError(t, config.GlobalConfig.SaveToDB(func(key, value string) error {
		saved[key] = value
		return nil
	}))
	t.Cleanup(func() { require.NoError(t, config.GlobalConfig.LoadFromDB(saved)) })
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{
		"billing_setting.billing_mode":    `{"jev-tier-test":"tiered_expr"}`,
		"billing_setting.billing_expr":    `{"jev-tier-test":"tier(\"input\", p * 3 + c * 15)"}`,
		"group_ratio_setting.group_ratio": `{"default":1}`,
	}))
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/typesafe/v1/systemone", nil)
	c.Request.Header.Set("Content-Type", "application/json")
	c.Set("group", "default")
	info := &relaycommon.RelayInfo{
		OriginModelName: "jev-tier-test", UserGroup: "default", UsingGroup: "default", RelayFormat: types.RelayFormatSystemOne,
		BillingRequestInput: &billingexpr.RequestInput{Body: []byte(`{}`), Headers: map[string]string{"Content-Type": "application/json"}},
	}
	price, err := ModelPriceHelper(c, info, dto.SystemOneMaxInputTokens, (&dto.SystemOneRequest{}).GetTokenCountMeta())
	require.NoError(t, err)
	require.Equal(t, 98304, price.QuotaToPreConsume)
	require.Equal(t, dto.SystemOneMaxInputTokens, info.TieredBillingSnapshot.EstimatedPromptTokens)
	require.Zero(t, info.TieredBillingSnapshot.EstimatedCompletionTokens)
}
