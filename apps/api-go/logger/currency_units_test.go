package logger

import (
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestQuotaLogsUseRealFiatAndExactCredits(t *testing.T) {
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
	for _, tc := range []struct{ display, expected string }{
		{"USD", "1.000000 USD"}, {"CNY", "7.000000 CNY"}, {"TOKENS", "3500000 Credits"},
	} {
		settings.QuotaDisplayType = tc.display
		require.Equal(t, tc.expected, FormatQuota(3500000))
		require.Equal(t, tc.expected, LogQuota(3500000))
	}
	settings.QuotaDisplayType = "USD"
	require.False(t, strings.HasPrefix(FormatQuota(1), "0.000000 "))
	require.Contains(t, FormatQuota(-1), "-0.")
	common.ClearCreditsPerUSD()
	for _, display := range []string{"USD", "CNY", "TOKENS", "CUSTOM"} {
		settings.QuotaDisplayType = display
		require.Equal(t, "3500000 Credits", FormatQuota(3500000))
		require.Equal(t, "0 Credits", LogQuota(0))
	}
}
