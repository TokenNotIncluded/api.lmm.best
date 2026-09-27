package model

// Export current Go behavior for Rust's independently implemented catalogue.
// No production configuration or database is read by this oracle.
import (
	"encoding/json"
	"os"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestRustAIDirectoryCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("set LMM_AI_DIRECTORY_GO_ORACLE_OUTPUT to export current Go evidence")
	}
	previous := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previous })
	quotes := []map[string]any{}
	for _, unit := range []string{"1000.1", "500000", "0.00000000000000001", "0.00000000000000005", "0.0000000000000001", "1e-30", "0", "-1", "NaN", "+Inf", "9007199254740991", "9007199254740992"} {
		common.QuotaPerUnit, _ = strconv.ParseFloat(unit, 64)
		for _, bid := range []int64{99, 100, 125, 1000000, 1000001} {
			quota, err := AIDirectoryAdChargeQuota(bid)
			item := map[string]any{"unit": unit, "bid_cents": bid, "quota": quota, "error": ""}
			if err != nil {
				item["error"] = err.Error()
			}
			quotes = append(quotes, item)
		}
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
	encoded, err := json.MarshalIndent(map[string]any{"quotes": quotes, "urls": urls}, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
