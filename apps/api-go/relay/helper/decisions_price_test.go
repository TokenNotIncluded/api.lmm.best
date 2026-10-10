package helper

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/constant"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/dto"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/stretchr/testify/require"
)

// Official rates are test data; production only reads administrator configuration.
const decisionsOfficialTestExpr = `(len > 272000 ? tier("long", p * 0.2) : tier("standard", p * 0.1)) * ((header("x-lmm-billing-upstream-host") == "eu.api.openai.com" || header("x-lmm-billing-upstream-host") == "us.api.openai.com") ? 1.1 : 1)`

func TestDecisionsConfiguredPricingBoundariesAndTrustedRegion(t *testing.T) {
	configureNativeDimensionPrice(t, "gpt-6-luna@decisions", decisionsOfficialTestExpr)
	for _, tc := range []struct {
		base        string
		input, want int
	}{
		{"https://api.openai.com", 272000, 13600},
		{"https://api.openai.com", 272001, 27200},
		{"https://eu.api.openai.com/v1", 272000, 14960},
		{"https://us.api.openai.com", 272001, 29920},
		{"https://eu.api.openai.com.attacker.example", 272000, 13600},
		{"", 1, 1},
		{"", 0, 0},
	} {
		c, info := nativeDimensionPriceContext("/v1/decisions", "gpt-6-luna")
		info.RelayFormat = types.RelayFormatOpenAIDecisions
		info.RequestHeaders["X-Lmm-Billing-Upstream-Host"] = "eu.api.openai.com"
		info.RequestHeaders[" x-lmm-billing-upstream-host "] = "us.api.openai.com"
		common.SetContextKey(c, constant.ContextKeyChannelBaseUrl, tc.base)
		price, err := ModelPriceHelper(c, info, tc.input, &types.TokenCountMeta{MaxTokens: 1000000})
		require.NoError(t, err)
		require.True(t, info.ForcePreConsume)
		require.Equal(t, "gpt-6-luna", info.OriginModelName)
		require.Equal(t, "gpt-6-luna@decisions", info.TieredBillingSnapshot.ModelName)
		require.Zero(t, info.TieredBillingSnapshot.EstimatedCompletionTokens)
		require.Zero(t, price.CompletionRatio)
		usage := &dto.Usage{PromptTokens: tc.input, TotalTokens: tc.input}
		ok, quota, result, err := service.TryTieredSettleWithError(info, service.BuildTieredTokenParams(usage, false, billingexpr.UsedVars(decisionsOfficialTestExpr)))
		require.NoError(t, err)
		require.True(t, ok)
		require.NotNil(t, result)
		require.Equal(t, tc.want, quota, "base=%s input=%d", tc.base, tc.input)
	}
}

func TestDecisionsPriceIsConfigurableAndFrozenPerRequest(t *testing.T) {
	configureNativeDimensionPrice(t, "alias@decisions", `tier("configured", p * 0.31)`)
	c, info := nativeDimensionPriceContext("/v1/decisions", "alias")
	info.RelayFormat = types.RelayFormatOpenAIDecisions
	_, err := ModelPriceDecisions(c, info, 10000)
	require.NoError(t, err)
	updated, err := json.Marshal(map[string]string{"alias@decisions": `tier("updated", p * 0.62)`})
	require.NoError(t, err)
	require.NoError(t, config.GlobalConfig.LoadFromDB(map[string]string{"billing_setting.billing_expr": string(updated)}))
	_, oldQuota, _, err := service.TryTieredSettleWithError(info, billingexpr.TokenParams{P: 10000, Len: 10000})
	require.NoError(t, err)
	require.Equal(t, 1550, oldQuota)
	_, err = ModelPriceDecisions(c, info, 10000)
	require.NoError(t, err)
	_, newQuota, _, err := service.TryTieredSettleWithError(info, billingexpr.TokenParams{P: 10000, Len: 10000})
	require.NoError(t, err)
	require.Equal(t, 3100, newQuota)
}

func TestDecisionsRetryRepricesSelectedHostBeforeReservation(t *testing.T) {
	configureNativeDimensionPrice(t, "gpt-6-luna@decisions", decisionsOfficialTestExpr)
	c, info := nativeDimensionPriceContext("/v1/decisions", "gpt-6-luna")
	info.RelayFormat = types.RelayFormatOpenAIDecisions
	price, err := ModelPriceDecisions(c, info, 272000)
	require.NoError(t, err)
	require.Equal(t, 13600, price.QuotaToPreConsume)
	info.ChannelMeta = &relaycommon.ChannelMeta{ChannelBaseUrl: "https://EU.api.openai.com"}
	require.NoError(t, RefreshDecisionsChannelPrice(info))
	require.Equal(t, 14960, info.PriceData.QuotaToPreConsume)
	require.Equal(t, "eu.api.openai.com", info.BillingRequestInput.Headers[DecisionsBillingHostHeader])
	require.Equal(t, decisionsOfficialTestExpr, info.TieredBillingSnapshot.ExprString)
	info.UsingGroup = "changed"
	require.Error(t, ValidateDecisionsPriceGroup(c, info))
}

func TestDecisionsMissingPriceNeverFallsBackToChatOrSelfUse(t *testing.T) {
	configureNativeDimensionPrice(t, "gpt-6-luna", `tier("chat", p * 9 + c * 18)`)
	c, info := nativeDimensionPriceContext("/v1/decisions", "missing-decisions-price")
	info.RelayFormat = types.RelayFormatOpenAIDecisions
	info.UserSetting.AcceptUnsetRatioModel = true
	_, err := ModelPriceDecisions(c, info, 100)
	require.ErrorContains(t, err, "missing-decisions-price@decisions")
	require.Nil(t, info.TieredBillingSnapshot)
}
