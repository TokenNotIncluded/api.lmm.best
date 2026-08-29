package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/stretchr/testify/require"
)

func TestPaymentRateOptionsRejectInvalidValuesBeforeMutatingRuntimeOrOptionMap(t *testing.T) {
	originalFX := operation_setting.USDExchangeRate
	originalPurchaseRatio := operation_setting.TopUpPlatformUnitsPerCNY
	common.OptionMapRWMutex.Lock()
	if common.OptionMap == nil {
		common.OptionMap = make(map[string]string)
	}
	originalFXOption, hadFXOption := common.OptionMap["USDExchangeRate"]
	originalRatioOption, hadRatioOption := common.OptionMap["TopUpPlatformUnitsPerCNY"]
	common.OptionMap["USDExchangeRate"] = "7.2"
	common.OptionMap["TopUpPlatformUnitsPerCNY"] = "1"
	common.OptionMapRWMutex.Unlock()
	t.Cleanup(func() {
		operation_setting.USDExchangeRate = originalFX
		operation_setting.TopUpPlatformUnitsPerCNY = originalPurchaseRatio
		common.OptionMapRWMutex.Lock()
		if hadFXOption {
			common.OptionMap["USDExchangeRate"] = originalFXOption
		} else {
			delete(common.OptionMap, "USDExchangeRate")
		}
		if hadRatioOption {
			common.OptionMap["TopUpPlatformUnitsPerCNY"] = originalRatioOption
		} else {
			delete(common.OptionMap, "TopUpPlatformUnitsPerCNY")
		}
		common.OptionMapRWMutex.Unlock()
	})

	operation_setting.USDExchangeRate = 7.2
	operation_setting.TopUpPlatformUnitsPerCNY = 1
	for _, key := range []string{"USDExchangeRate", "TopUpPlatformUnitsPerCNY"} {
		for _, invalid := range []string{"", "0", "-1", "NaN", "+Inf"} {
			err := updateOptionMap(key, invalid)
			require.Error(t, err, key+"="+invalid)
		}
	}
	require.Equal(t, 7.2, operation_setting.USDExchangeRate)
	require.Equal(t, float64(1), operation_setting.TopUpPlatformUnitsPerCNY)
	common.OptionMapRWMutex.RLock()
	require.Equal(t, "7.2", common.OptionMap["USDExchangeRate"])
	require.Equal(t, "1", common.OptionMap["TopUpPlatformUnitsPerCNY"])
	common.OptionMapRWMutex.RUnlock()
}
