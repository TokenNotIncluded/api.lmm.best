package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestFinanceUserExportUsesMigrationProjectionWithoutChangingWalletOrHistory(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	user := model.User{Username: "export-money", Password: "fixture-secret-password", Quota: 500000, UsedQuota: 6800}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "fixture-secret-token", UsedQuota: 3450}
	require.NoError(t, db.Create(&token).Error)
	seedControllerUsageAudit(t, db, &user, &token)
	var output bytes.Buffer
	require.NoError(t, streamFinanceUsersJSON(&output, nil))
	var rows []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &rows))
	require.Len(t, rows, 1)
	require.EqualValues(t, 6800, rows[0]["used_quota"])
	require.EqualValues(t, 500000, rows[0]["quota"])
	require.EqualValues(t, 1100, rows[0]["normalized_used_quota"])
	require.Equal(t, true, rows[0]["usage_projection_available"])
	require.NotContains(t, output.String(), "secret")
	var stored model.User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 6800, stored.UsedQuota)
	require.Equal(t, 500000, stored.Quota)

	// Retained facts that no longer prove a projection must not be replaced by
	// today's denomination, nor break export of the original wallet snapshot.
	require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", "malformed").Error)
	output.Reset()
	require.NoError(t, streamFinanceUsersJSON(&output, nil))
	rows = nil
	require.NoError(t, json.Unmarshal(output.Bytes(), &rows))
	require.Equal(t, false, rows[0]["usage_projection_available"])
	require.NotContains(t, rows[0], "normalized_used_quota")
	require.EqualValues(t, 6800, rows[0]["used_quota"])
	require.EqualValues(t, 500000, rows[0]["quota"])
}

func TestFinanceUsageStreamExportsCapturedMoneyWithoutOpaqueMetadata(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	rows := []model.Log{
		{UserId: 7, Type: model.LogTypeConsume, Quota: 3359744, Other: `{"pricing_credits_per_usd":3359744,"provider_payload":"fixture-secret-provider-payload","admin_info":{"ip":"fixture-secret-ip"}}`},
		{UserId: 7, Type: model.LogTypeConsume, Quota: 500000, Other: `{}`},
		{UserId: 7, Type: model.LogTypeTopup, Quota: 100, Other: `{"pricing_credits_per_usd":500000}`},
	}
	require.NoError(t, db.Create(&rows).Error)
	require.NoError(t, db.Exec("INSERT INTO logs(user_id, type, quota, other) VALUES (7, ?, NULL, ?)", model.LogTypeConsume, `{"pricing_credits_per_usd":500000}`).Error)
	var output bytes.Buffer
	query := db.Model(&model.Log{}).Select("id", "user_id", "type", "quota", "other", "quota IS NOT NULL AS quota_stored_valid").Order("id ASC")
	require.NoError(t, streamFinanceQueryJSON[financeUsageExport](&output, query, projectFinanceUsage))
	var exported []map[string]any
	require.NoError(t, json.Unmarshal(output.Bytes(), &exported))
	require.Len(t, exported, 4)
	require.Equal(t, "1", exported[0]["quota_usd"])
	require.EqualValues(t, 3359744, exported[0]["quota"])
	require.Equal(t, true, exported[0]["quota_money_available"])
	require.Equal(t, false, exported[1]["quota_money_available"])
	require.NotContains(t, exported[1], "quota_usd")
	require.Equal(t, "not_consumption", exported[2]["quota_money_basis"])
	require.Equal(t, false, exported[3]["quota_money_available"])
	require.Equal(t, "invalid_stored_quota", exported[3]["quota_money_basis"])
	require.NotContains(t, exported[3], "quota_usd")
	require.NotContains(t, output.String(), "fixture-secret")
	require.NotContains(t, output.String(), "provider_payload")
	require.NotContains(t, output.String(), `"other"`)
	var stored model.Log
	require.NoError(t, db.First(&stored, rows[0].Id).Error)
	require.Contains(t, stored.Other, "fixture-secret-provider-payload")
	require.Equal(t, 3359744, stored.Quota)
}

func TestLogSelfStatReturnsMoneyAvailabilityWithStableOwnerScope(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	previous := model.LOG_DB
	model.LOG_DB = db
	t.Cleanup(func() { model.LOG_DB = previous })
	require.NoError(t, db.AutoMigrate(&model.Log{}))
	require.NoError(t, db.Create(&[]model.Log{
		{UserId: 7, Username: "old-name", Type: model.LogTypeConsume, Quota: 3359744, Other: `{"pricing_credits_per_usd":3359744}`},
		{UserId: 8, Username: "same-name", Type: model.LogTypeConsume, Quota: 100, Other: `{}`},
	}).Error)
	c, response := publicCreditTestContext(t, http.MethodGet, "/api/log/self/stat", "", 7)
	GetLogsSelfStat(c)
	data := publicCreditResponseData(t, response)
	require.EqualValues(t, 3359744, data["quota"])
	require.Equal(t, true, data["quota_money_available"])
	require.Equal(t, "1", data["quota_usd"])
	require.EqualValues(t, 0, data["unpriced_quota_records"])
	require.Equal(t, false, data["quota_projection_truncated"])
}
