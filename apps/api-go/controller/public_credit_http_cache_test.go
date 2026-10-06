package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestPublicCreditHTTPMetadataRefreshesWithoutCacheableResponses(t *testing.T) {
	installStatusCurrencyFixture(t)
	installControllerCreditAnchor(t, 500000)
	preserveCacheRuntimeHooks(t)
	cacheReadinessError = func() error { return nil }
	getPricingCache = func() []model.Pricing { return []model.Pricing{} }
	persistCreditDenominationFixture(t, model.DB)
	router := gin.New()
	router.GET("/api/status", GetStatus)
	router.GET("/api/pricing", GetPricing)
	request := func(path string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
		require.Equal(t, "no-store", w.Header().Get("Cache-Control"), path)
		return w
	}
	for _, public := range []string{"500000"} {
		require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", public).Error)
		common.ClearPublicCreditsPerUSD() // A cleared compatibility cache still reads the durable fixed contract.
		for _, path := range []string{"/api/status", "/api/pricing"} {
			w := request(path)
			require.Equal(t, http.StatusOK, w.Code, w.Body.String())
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			if path == "/api/status" {
				body = body["data"].(map[string]any)
			}
			require.Equal(t, public, body["public_credits_per_usd_exact"])
			require.Equal(t, "500000", body["ledger_quota_per_usd_exact"])
		}
	}
	require.NoError(t, model.DB.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "200000").Error)
	for _, path := range []string{"/api/status", "/api/pricing"} {
		w := request(path)
		require.Equal(t, http.StatusServiceUnavailable, w.Code, w.Body.String())
		require.NotContains(t, w.Body.String(), "public_credits_per_usd")
	}
}
