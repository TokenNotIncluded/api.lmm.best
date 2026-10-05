package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestAIDirectoryAdQuoteActualUSDAndMissingBasis(t *testing.T) {
	oldQ := common.QuotaPerUnit
	oldK, oldErr := common.CreditsPerUSD()
	oldLegacy, _ := common.LegacyPricingQuotaPerUnit()
	common.QuotaPerUnit = 500_000
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(3_500_000)))
	t.Cleanup(func() {
		common.QuotaPerUnit = oldQ
		common.ClearCreditsPerUSD()
		if oldErr == nil {
			require.NoError(t, common.SetCreditCurrencyBasis(oldK, oldLegacy))
		}
	})
	router := gin.New()
	router.GET("/quote", QuoteAIDirectoryAd)
	quote := func() (int, map[string]any) {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/quote?bid_cents=100", nil))
		var body map[string]any
		require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
		return w.Code, body
	}
	status, body := quote()
	require.Equal(t, http.StatusOK, status)
	data := body["data"].(map[string]any)
	require.Equal(t, "USD", data["currency"])
	require.Equal(t, float64(2), data["pricing_schema_version"])
	require.Equal(t, float64(3_500_000), data["quota"])
	common.ClearCreditsPerUSD()
	status, body = quote()
	require.Equal(t, http.StatusServiceUnavailable, status)
	require.Equal(t, false, body["success"])
	require.Equal(t, "AI_DIRECTORY_AD_CURRENCY_UNAVAILABLE", body["code"])
}
