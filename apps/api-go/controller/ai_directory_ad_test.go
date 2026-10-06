package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
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

func TestHideAIDirectoryAdReturnsActualRebasedRefund(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.AIDirectoryAd{}))
	owner := model.User{Username: "ad-rebased-response", AffCode: "ad-rebased-response", Quota: 123}
	require.NoError(t, db.Create(&owner).Error)
	now := time.Now().Unix()
	ad := model.AIDirectoryAd{OwnerUserID: owner.Id, ChargedQuota: 6_710_363,
		RequestID: "ad-rebased-response-0001", Status: model.AIDirectoryAdStatusActive,
		PaidAt: now - 2, ExpiresAt: now + 1000}
	require.NoError(t, db.Create(&ad).Error)
	plan, err := json.Marshal(map[string]any{
		"user_ids": []int{owner.Id}, "include_other_rights": true, "snapshot_at": now - 1,
		"divisor": "6.710363", "rounding": "half-away-from-zero",
		"other_credit_bases": []map[string]any{{
			"kind": "ai_directory_ad_refund", "source_id": strconv.Itoa(ad.ID), "user_id": owner.Id,
			"original_quota": ad.ChargedQuota, "rebased_quota": 1_000_000,
			"source": map[string]any{"id": ad.ID, "owner_user_id": owner.Id, "status": ad.Status,
				"charged_quota": ad.ChargedQuota, "paid_at": ad.PaidAt, "expires_at": ad.ExpiresAt},
		}},
	})
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?, ?)", "ad-response-test", string(plan)).Error)
	for i := 0; i < 2; i++ {
		c, response := publicCreditTestContext(t, http.MethodPost, "/api/ai-directory/ads/1/hide", "", owner.Id)
		c.Params = gin.Params{{Key: "id", Value: strconv.Itoa(ad.ID)}}
		HideAIDirectoryAd(c)
		data := publicCreditResponseData(t, response)
		require.Equal(t, float64(1_000_000), data["refunded_quota"])
		require.Equal(t, i == 0, data["refunded"])
		receipt := data["ad"].(map[string]any)
		require.Equal(t, float64(6_710_363), receipt["charged_quota"])
		require.Equal(t, float64(1_000_000), receipt["refunded_quota"])
	}
	var wallet model.User
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	require.Equal(t, 1_000_123, wallet.Quota)
}
