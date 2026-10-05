package controller

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func preserveAccountBalanceBasis(t *testing.T) {
	t.Helper()
	previous, previousErr := common.CreditsPerUSD()
	previousLegacy, _ := common.LegacyPricingQuotaPerUnit()
	previousUnit, previousFX := common.QuotaPerUnit, operation_setting.USDExchangeRate
	t.Cleanup(func() {
		common.QuotaPerUnit, operation_setting.USDExchangeRate = previousUnit, previousFX
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previous, previousLegacy))
		}
	})
}

// The Rust durable HTTP test consumes this same fixture. Keep expectations
// independent of either adapter; timestamps are checked before normalization.
func TestAccountBalanceSharedParityVectors(t *testing.T) {
	raw, err := os.ReadFile("../../api-rust/tests/fixtures/account-balance-parity.json")
	require.NoError(t, err)
	var cases []struct {
		Name           string  `json:"name"`
		Quota          int     `json:"quota"`
		Unit           float64 `json:"unit"`
		Anchor         *string `json:"credits_per_usd"`
		Legacy         *string `json:"legacy_unit"`
		FX             string  `json:"fx"`
		Grant          bool    `json:"grant"`
		Status         int     `json:"status"`
		ExpectedStatus int     `json:"expected_status"`
		Remaining      float64 `json:"remaining"`
		Error          string  `json:"error"`
	}
	require.NoError(t, json.Unmarshal(raw, &cases))
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	preserveAccountBalanceBasis(t)
	user := model.User{Username: "shared-balance-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "shared-balance-key", ExpiredTime: -1, UnlimitedQuota: true, UsedQuota: 98765, AccessedTime: 123}
	require.NoError(t, db.Create(&token).Error)
	router := gin.New()
	router.GET("/v1/balance", middleware.DisableCache(), middleware.QuotaQueryAuth(), GetAccountBalance)
	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			common.QuotaPerUnit = tc.Unit
			common.ClearCreditsPerUSD()
			if tc.Anchor != nil && tc.Legacy != nil {
				anchor, anchorErr := decimal.NewFromString(*tc.Anchor)
				legacy, legacyErr := decimal.NewFromString(*tc.Legacy)
				if anchorErr == nil && legacyErr == nil {
					// Invalid persisted bases are rejected by initialization. The
					// endpoint must refuse the resulting unavailable basis.
					_ = common.SetCreditCurrencyBasis(anchor, legacy)
				}
			}
			fx, fxErr := decimal.NewFromString(tc.FX)
			require.NoError(t, fxErr)
			operation_setting.USDExchangeRate = fx.InexactFloat64()
			require.NoError(t, db.Model(&user).Update("quota", tc.Quota).Error)
			require.NoError(t, db.Model(&token).Updates(map[string]any{"status": tc.Status, "account_balance_read": tc.Grant}).Error)
			var beforeUser model.User
			var beforeToken model.Token
			require.NoError(t, db.First(&beforeUser, user.Id).Error)
			require.NoError(t, db.First(&beforeToken, token.Id).Error)
			r := httptest.NewRequest("GET", "/v1/balance", nil)
			r.Header.Set("Authorization", "Bearer sk-"+token.Key)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, tc.ExpectedStatus, w.Code, w.Body.String())
			require.Contains(t, w.Header().Get("Cache-Control"), "no-store")
			var body map[string]any
			require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
			if tc.ExpectedStatus == 200 {
				updated, ok := body["updated_at"].(float64)
				require.True(t, ok)
				require.InDelta(t, time.Now().Unix(), updated, 10)
				delete(body, "updated_at")
				require.Equal(t, map[string]any{"valid": true, "scope": "account", "currency": "USD", "remaining": tc.Remaining, "consistency": "persisted_snapshot"}, body)
			} else {
				require.Equal(t, map[string]any{"valid": false, "error": tc.Error}, body)
			}
			var storedUser model.User
			require.NoError(t, db.First(&storedUser, user.Id).Error)
			require.Equal(t, tc.Quota, storedUser.Quota, "balance reads must not alter wallet credits")
			require.Equal(t, beforeUser, storedUser, "balance reads must not write user state")
			var stored model.Token
			require.NoError(t, db.First(&stored, token.Id).Error)
			require.Equal(t, int64(123), stored.AccessedTime)
			require.Equal(t, 98765, stored.UsedQuota)
			require.Equal(t, beforeToken, stored, "balance reads must not write token state")
		})
	}
}

func TestAccountBalanceRejectsNullCredentialLifecycle(t *testing.T) {
	db := setupManageUserTestDB(t)
	// Deliberately model a legacy nullable row. The current Go migration makes
	// oauth_managed NOT NULL; reads must still fail closed on older schemas.
	require.NoError(t, db.Exec(`CREATE TABLE tokens (
		id INTEGER PRIMARY KEY, user_id INTEGER, key TEXT, status INTEGER,
		expired_time INTEGER, oauth_managed BOOLEAN, account_balance_read BOOLEAN,
		deleted_at DATETIME
	)`).Error)
	user := model.User{Username: "nullable-balance-owner", Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{Id: 1, UserId: user.Id, Key: "nullable-balance-key"}
	require.NoError(t, db.Exec("INSERT INTO tokens VALUES (?, ?, ?, ?, -1, false, true, NULL)", token.Id, token.UserId, token.Key, common.TokenStatusEnabled).Error)
	router := gin.New()
	router.GET("/v1/balance", middleware.DisableCache(), middleware.QuotaQueryAuth(), GetAccountBalance)
	for _, column := range []string{"status", "expired_time", "oauth_managed"} {
		t.Run(column, func(t *testing.T) {
			require.NoError(t, db.Model(&token).Update(column, nil).Error)
			r := httptest.NewRequest("GET", "/v1/balance", nil)
			r.Header.Set("Authorization", "Bearer sk-"+token.Key)
			w := httptest.NewRecorder()
			router.ServeHTTP(w, r)
			require.Equal(t, 401, w.Code, w.Body.String())
			require.JSONEq(t, `{"valid":false,"error":"invalid_api_key"}`, w.Body.String())
			require.NoError(t, db.Model(&token).Updates(map[string]any{"status": common.TokenStatusEnabled, "expired_time": -1, "oauth_managed": false}).Error)
		})
	}
}
