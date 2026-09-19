package relay

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/constant"
	relaycommon "github.com/LIghtJUNction/api.lmm.best/relay/common"
	hosttypes "github.com/LIghtJUNction/api.lmm.best/types"

	"github.com/stretchr/testify/require"
)

func TestTaskSubmitQuotaIgnoresRetiredProfitMultiplier(t *testing.T) {
	previous := constant.TaskPricePatches
	constant.TaskPricePatches = []string{"patched-model"}
	t.Cleanup(func() { constant.TaskPricePatches = previous })

	info := &relaycommon.RelayInfo{PriceData: hosttypes.PriceData{Quota: 100}}
	info.PriceData.AddOtherRatio("duration", 3)
	info.PriceData.AddOtherRatio("dynamic_pricing", 2)

	quota, clamp := taskSubmitQuotaWithRatios(info, "patched-model")
	require.Nil(t, clamp)
	require.Equal(t, 100, quota, "price patches use their fixed base price")

	quota, clamp = taskSubmitQuotaWithRatios(info, "ordinary-model")
	require.Nil(t, clamp)
	require.Equal(t, 300, quota)
}

func TestMidjourneyPerCallQuotaIgnoresRetiredProfitMultiplier(t *testing.T) {
	priceData := hosttypes.PriceData{Quota: 100}
	priceData.AddOtherRatio("dynamic_pricing", 2.5)

	require.NoError(t, applyMidjourneyPriceRatios(&priceData))
	require.Equal(t, 100, priceData.Quota)
}
