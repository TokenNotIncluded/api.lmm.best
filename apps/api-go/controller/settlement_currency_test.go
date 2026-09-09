package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/i18n"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestSelfSettlementCurrencyPreferenceIsOwnerScopedAndPersistent(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, i18n.Init())
	user := model.User{Username: "fiat-preference-owner", AffCode: "fiat-owner", Status: common.UserStatusEnabled, Quota: 777, Setting: `{"language":"zh","record_ip_log":true,"future_setting":{"keep":1}}`}
	other := model.User{Username: "fiat-preference-other", AffCode: "fiat-other", Status: common.UserStatusEnabled, Setting: `{"language":"zh"}`}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&other).Error)
	engine := gin.New()
	engine.PUT("/api/user/self", func(c *gin.Context) { c.Set("id", user.Id); c.Next() }, UpdateSelf)
	update := func(body string) bool {
		t.Helper()
		request := httptest.NewRequest(http.MethodPut, "/api/user/self", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		response := httptest.NewRecorder()
		engine.ServeHTTP(response, request)
		var result struct {
			Success bool `json:"success"`
		}
		require.NoError(t, json.Unmarshal(response.Body.Bytes(), &result), response.Body.String())
		return result.Success
	}
	load := func() model.User {
		t.Helper()
		var current model.User
		require.NoError(t, db.First(&current, user.Id).Error)
		return current
	}
	require.True(t, update(`{"settlement_currency":" usd ","user_id":`+common.GetJsonString(other.Id)+`}`))
	current := load()
	require.Equal(t, "USD", userSettlementCurrency(&current, "zh"))
	require.True(t, current.GetSetting().RecordIpLog)
	require.Contains(t, current.Setting, `"future_setting":{"keep":1}`)
	require.Equal(t, 777, current.Quota)
	require.True(t, update(`{"language":"zh-TW"}`))
	current = load()
	require.Equal(t, "USD", userSettlementCurrency(&current, "zh"), "language changes do not override explicit currency")
	require.True(t, update(`{"language":"en","settlement_currency":"CNY"}`))
	current = load()
	require.Equal(t, "en", current.GetSetting().Language)
	require.Equal(t, "CNY", userSettlementCurrency(&current, "en"))
	before := current.Setting
	for _, body := range []string{`{"settlement_currency":"EUR"}`, `{"settlement_currency":123}`, `{"settlement_currency":null}`, `{"language":[]}`, `{"language":"zh","settlement_currency":"GBP"}`} {
		require.False(t, update(body), body)
		require.Equal(t, before, load().Setting, "invalid updates are atomic")
	}
	require.True(t, update(`{"settlement_currency":""}`))
	current = load()
	require.Equal(t, "USD", userSettlementCurrency(&current, "zh"))
	require.NoError(t, db.First(&other, other.Id).Error)
	require.Equal(t, `{"language":"zh"}`, other.Setting)
}

func TestConcurrentLocaleUpdatesPreserveExplicitCurrency(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	// SQLite has no SELECT FOR UPDATE; serialize its connection while testing
	// interleaved patch semantics. PostgreSQL lock behavior needs the PG gate.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	user := model.User{Username: "concurrent-fiat-preferences", Status: common.UserStatusEnabled, Setting: `{"record_ip_log":true}`}
	require.NoError(t, db.Create(&user).Error)
	language, currency := "en", "CNY"
	var wg sync.WaitGroup
	errors := make(chan error, 2)
	wg.Go(func() { errors <- model.UpdateUserLocalePreferences(user.Id, &language, nil) })
	wg.Go(func() { errors <- model.UpdateUserLocalePreferences(user.Id, nil, &currency) })
	wg.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	require.NoError(t, db.First(&user, user.Id).Error)
	require.Equal(t, "CNY", userSettlementCurrency(&user, "en"))
	require.Equal(t, "en", user.GetSetting().Language)
	require.True(t, user.GetSetting().RecordIpLog)
}
