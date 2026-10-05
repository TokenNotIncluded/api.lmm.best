package logger

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestQuotaLogsAlwaysUseUSD(t *testing.T) {
	oldAnchor, anchorErr := common.CreditsPerUSD()
	oldLegacy, legacyErr := common.LegacyPricingQuotaPerUnit()
	settings := operation_setting.GetGeneralSetting()
	previous := *settings
	oldFX := operation_setting.USDExchangeRate
	t.Cleanup(func() {
		*settings = previous
		operation_setting.USDExchangeRate = oldFX
		if anchorErr != nil || legacyErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(oldAnchor, oldLegacy))
		}
	})
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3500000), decimal.NewFromInt(500000)))
	operation_setting.USDExchangeRate = 7
	for _, display := range []string{"USD", "CNY", "TOKENS", "CUSTOM"} {
		settings.QuotaDisplayType = display
		require.Equal(t, "1.000000 USD", FormatQuota(3500000))
		require.Equal(t, "1.000000 USD", LogQuota(3500000))
	}
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3359744), decimal.NewFromInt(500000)))
	operation_setting.USDExchangeRate = 9
	settings.QuotaDisplayType = "CNY"
	require.Equal(t, "1.000000 USD", LogQuota(3359744))
	require.Equal(t, "-1.000000 USD", LogQuota(-3359744))
	require.Equal(t, "0.000000 USD", LogQuota(0))
	require.False(t, strings.HasPrefix(FormatQuota(1), "0.000000 "))
	require.Contains(t, FormatQuota(-1), "-0.")
	common.ClearCreditsPerUSD()
	for _, display := range []string{"USD", "CNY", "TOKENS", "CUSTOM"} {
		settings.QuotaDisplayType = display
		require.Equal(t, "USD unavailable", FormatQuota(3500000))
		require.Equal(t, "USD unavailable", LogQuota(0))
	}
}
