package controller

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/middleware"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestQuotaQueryScopeAndReadOnly(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	oldUnit, oldLogs := common.QuotaPerUnit, common.LogConsumeEnabled
	common.QuotaPerUnit, common.LogConsumeEnabled = 100, true
	t.Cleanup(func() { common.QuotaPerUnit, common.LogConsumeEnabled = oldUnit, oldLogs })
	user := model.User{Username: "quota-reader", Status: common.UserStatusEnabled, Quota: 999999}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "quota-query-test", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 8850, UsedQuota: 12080}
	require.NoError(t, db.Create(&token).Error)
	now := time.Now().UTC()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Unix()
	require.NoError(t, db.Create(&[]model.Log{
		{UserId: user.Id, TokenId: token.Id, Type: model.LogTypeConsume, Quota: 230, CreatedAt: now.Unix()},
		{UserId: user.Id, TokenId: token.Id + 1, Type: model.LogTypeConsume, Quota: 999, CreatedAt: now.Unix()},
		{UserId: user.Id + 1, TokenId: token.Id, Type: model.LogTypeConsume, Quota: 999, CreatedAt: now.Unix()},
		{UserId: user.Id, TokenId: token.Id, Type: model.LogTypeConsume, Quota: 999, CreatedAt: start - 1},
	}).Error)
	router := gin.New()
	router.GET("/v1/usage", middleware.QuotaQueryAuth(), GetQuotaQuery)
	query := func(auth string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/v1/usage", nil)
		r.Header.Set("Authorization", auth)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	w := query("Bearer sk-" + token.Key)
	require.Equal(t, 200, w.Code, w.Body.String())
	require.NotContains(t, w.Body.String(), token.Key)
	var body map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, "USD", body["currency"])
	require.Equal(t, "token", body["scope"])
	require.Equal(t, 88.5, body["remaining"])
	require.Equal(t, 120.8, body["used_total"])
	require.Equal(t, 209.3, body["total_quota"])
	require.Equal(t, 2.3, body["used_today"])
	var after model.Token
	require.NoError(t, db.First(&after, token.Id).Error)
	require.Equal(t, token, after)
	for _, auth := range []string{"", token.Key, "Basic " + token.Key, "Bearer bad", "Bearer sk-" + token.Key + "-1"} {
		require.Equal(t, 401, query(auth).Code)
	}
	require.NoError(t, db.Model(&token).Updates(map[string]any{"unlimited_quota": true, "status": common.TokenStatusExhausted}).Error)
	common.LogConsumeEnabled = false
	w = query("Bearer " + token.Key)
	require.Equal(t, 200, w.Code)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &body))
	require.Equal(t, true, body["unlimited"])
	require.Nil(t, body["remaining"])
	require.Nil(t, body["total_quota"])
	require.Nil(t, body["used_today"])
	require.NoError(t, db.Model(&token).Update("status", common.TokenStatusDisabled).Error)
	require.Equal(t, 401, query("Bearer "+token.Key).Code)
	require.NoError(t, db.Model(&token).Updates(map[string]any{"status": common.TokenStatusEnabled, "expired_time": now.Unix() - 1}).Error)
	require.Equal(t, 401, query("Bearer "+token.Key).Code)
}
