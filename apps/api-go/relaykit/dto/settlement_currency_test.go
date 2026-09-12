package dto

import "testing"

func TestEffectiveSettlementCurrency(t *testing.T) {
	cases := []struct {
		name, saved, language, hint, want string
	}{
		{"Chinese", "", "zh", "en", "CNY"},
		{"TraditionalChinese", "", "zh-TW", "en", "CNY"},
		{"SimplifiedChineseCamel", "", "zhCN", "en", "CNY"},
		{"TraditionalChineseCamel", "", "zhTW", "en", "CNY"},
		{"ChineseUnderscore", "", "zh_CN", "en", "CNY"},
		{"TraditionalChineseUnderscore", "", "zh_Hant", "en", "CNY"},
		{"English", "", "en", "zh", "USD"},
		{"ChineseHint", "", "", "zh-CN,zh;q=0.9,en;q=0.8", "CNY"},
		{"Empty", "", "", "", "USD"},
		{"OtherLanguage", "", "fr", "", "USD"},
		{"ManualUSD", "USD", "zh", "zh", "USD"},
		{"ManualCNY", "CNY", "en", "en", "CNY"},
		{"Normalized", " cny ", "en", "", "CNY"},
		{"LegacyInvalid", "EUR", "zh", "", "CNY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			setting := UserSetting{SettlementCurrency: tc.saved, Language: tc.language}
			if got := setting.EffectiveSettlementCurrency(tc.hint); got != tc.want {
				t.Fatalf("currency = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestSettlementCurrencyPreferenceRejectsUnsupportedFiat(t *testing.T) {
	for _, value := range []string{"EUR", "CNY/USD", "auto", "USDT", "人民币"} {
		if _, err := NormalizeSettlementCurrencyPreference(value); err == nil {
			t.Fatalf("unsupported preference %q accepted", value)
		}
	}
	for _, value := range []string{"", " USD ", "cny"} {
		if _, err := NormalizeSettlementCurrencyPreference(value); err != nil {
			t.Fatalf("valid preference %q rejected: %v", value, err)
		}
	}
}
