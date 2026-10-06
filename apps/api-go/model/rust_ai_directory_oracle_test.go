package model

// Export current Go behavior for Rust's independently implemented catalogue.
// No production configuration or database is read by this oracle.
import (
	"encoding/json"
	"os"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestRustAIDirectoryCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("set LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT to export current Go evidence")
	}
	directoryAdCurrencyFixture(t, "3500000")
	quotes := []map[string]any{}
	for _, unit := range []string{"1000.1", "500000", "0.00000000000000001", "0.00000000000000005", "0.0000000000000001", "1e-30", "0", "-1", "NaN", "+Inf", "9007199254740991", "9007199254740992", "3500000"} {
		common.ClearCreditsPerUSD()
		if k, err := decimal.NewFromString(unit); err == nil {
			_ = common.SetCreditsPerUSD(k)
		}
		for _, bid := range []int64{99, 100, 125, 1000000, 1000001} {
			quota, err := AIDirectoryAdChargeQuota(bid)
			item := map[string]any{"unit": unit, "credits_per_usd": unit, "legacy_quota_per_unit": "500000", "bid_cents": bid, "quota": quota, "error": ""}
			if err != nil {
				item["error"] = err.Error()
			}
			quotes = append(quotes, item)
		}
	}
	amounts := []map[string]any{}
	for _, vector := range []struct {
		k string
		q int64
	}{
		{"3500000", 500000}, {"3500000", 3500000}, {"3500000", 0},
		{"3500000", -500000}, {"1000.1", 1234}, {"1e-30", 1},
		{"9007199254740991", 1}, {"1.234567890123456789012345678901", int64(common.MaxWalletQuota)},
		{"", 500000},
	} {
		common.ClearCreditsPerUSD()
		if k, err := decimal.NewFromString(vector.k); err == nil {
			_ = common.SetCreditsPerUSD(k)
		}
		var amount any
		if usd, err := common.CreditsToUSD(vector.q); err == nil {
			amount = usd.String()
		}
		amounts = append(amounts, map[string]any{"credits_per_usd": vector.k, "charged_quota": vector.q, "charged_amount_usd": amount})
	}
	urls := []map[string]any{}
	for _, raw := range []string{"https://example.com", " https://Example.COM ", "HTTPS://Example.com", "https://example.com/中文?a=中文#中文", "https://例子.com/路径", "https://example.com/a[b]", "https://example.com/a\\b", "https://example.com:99999", "https://example.com/%zz", "https://127.1", "https://127.0.0.1", "https://10.1.2.3", "https://0.0.0.0", "https://169.254.1.2", "https://localhost", "https://host.local", "https://[::ffff:8.8.8.8]/", "https://[::ffff:127.0.0.1]/", "https://user:pass@example.com", "https://@example.com", "http://example.com", "https://example.com?#"} {
		normalized, err := normalizeAIDirectoryAdURL(raw)
		item := map[string]any{"input": raw, "normalized": normalized, "error": ""}
		if err != nil {
			item["error"] = err.Error()
		}
		urls = append(urls, item)
	}
	encoded, err := json.MarshalIndent(map[string]any{"pricing_schema_version": 2, "pricing_currency": "USD", "quotes": quotes, "amounts": amounts, "urls": urls}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
