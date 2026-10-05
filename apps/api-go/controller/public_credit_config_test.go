package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestPublicCreditOptionsVersionedWriteAndFreshCrossNodeRead(t *testing.T) {
	previousOptions := common.OptionMap
	t.Cleanup(func() { common.OptionMap = previousOptions })
	db, user, _ := setupWalletMCPTest(t)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(3359744), decimal.NewFromInt(500000)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	persistCreditDenominationFixture(t, db)
	router := gin.New()
	router.GET("/unit", GetPublicCreditUnitOptions)
	router.PUT("/unit", func(c *gin.Context) { c.Set("id", user.Id); c.Next() }, PutPublicCreditUnitOptions)
	request := func(method, body string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest(method, "/unit", strings.NewReader(body)))
		return w
	}
	valid := `{"credit_unit_schema_version":2,"public_credits_per_usd_exact":"200000","expected_public_credits_per_usd_exact":"100000","expected_ledger_quota_per_usd_exact":"3359744"}`
	w := request(http.MethodPut, valid)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())
	var response struct {
		Success bool                      `json:"success"`
		Data    common.CreditDenomination `json:"data"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "200000", response.Data.PublicCreditsPerUSDExact)
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(100000)))
	w = request(http.MethodPut, valid)
	require.Equal(t, http.StatusConflict, w.Code, "CAS checks durable P, not this node's stale cache")
	w = request(http.MethodGet, "")
	require.Equal(t, http.StatusOK, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &response))
	require.Equal(t, "200000", response.Data.PublicCreditsPerUSDExact)
	require.Equal(t, "3359744", response.Data.LedgerQuotaPerUSDExact)
	for _, body := range []string{
		strings.Replace(valid, `"credit_unit_schema_version":2`, `"credit_unit_schema_version":1`, 1),
		strings.Replace(valid, `"200000"`, `"0"`, 1),
		strings.Replace(valid, `"200000"`, `200000`, 1),
		strings.TrimSuffix(valid, "}") + `,"quota":1}`,
		strings.TrimSuffix(valid, "}") + `,"public_credits_per_usd_exact":"500000"}`,
		valid + ` {}`,
	} {
		w := request(http.MethodPut, body)
		require.NotEqual(t, http.StatusOK, w.Code, body)
	}
	var current model.User
	require.NoError(t, db.First(&current, user.Id).Error)
	require.Equal(t, user.Quota, current.Quota, "denomination settings do not debit the wallet")
	require.NoError(t, db.Model(&model.Option{}).Where("key = ?", model.PublicCreditsPerUSDOptionKey).Update("value", "invalid").Error)
	w = request(http.MethodGet, "")
	require.Equal(t, http.StatusServiceUnavailable, w.Code)
	require.NotContains(t, w.Body.String(), "public_credits_per_usd")
}
