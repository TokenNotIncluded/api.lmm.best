package dto

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestWalletDisplayCurrencyFollowsLocaleAndExplicitChoiceWins(t *testing.T) {
	for _, language := range []string{"zh", "zh-CN", "zh-TW", "zh_HK", "zhcn", "zhtw", "zh-CN, en;q=0.8"} {
		require.Equal(t, "CNY", (UserSetting{Language: language}).EffectiveWalletDisplayCurrency("en"), language)
	}
	for _, language := range []string{"en", "en-US", "ja", ""} {
		require.Equal(t, "USD", (UserSetting{Language: language}).EffectiveWalletDisplayCurrency("en"), language)
	}
	require.Equal(t, "CNY", (UserSetting{}).EffectiveWalletDisplayCurrency("zh-TW"))
	for _, choice := range []string{"CREDIT", "CNY", "USD"} {
		for _, language := range []string{"en", "zh"} {
			prefs := UserSetting{Language: language, WalletDisplayCurrency: choice, SettlementCurrency: "CNY"}
			require.Equal(t, choice, prefs.EffectiveWalletDisplayCurrency(""))
			require.Equal(t, "CNY", prefs.EffectiveSettlementCurrency(""), "display never selects settlement currency")
		}
	}
	for _, invalid := range []string{"TOKENS", "EUR", "1", "CUSTOM"} {
		_, err := NormalizeWalletDisplayCurrencyPreference(invalid)
		require.Error(t, err)
	}
	_, err := NormalizeSettlementCurrencyPreference("CREDIT")
	require.Error(t, err)
	normalized, err := NormalizeWalletDisplayCurrencyPreference(" credit ")
	require.NoError(t, err)
	require.Equal(t, "CREDIT", normalized)
}
