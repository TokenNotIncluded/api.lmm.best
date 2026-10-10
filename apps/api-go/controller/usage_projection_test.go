package controller

import (
	"encoding/json"
	"errors"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"net/http"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

type fixtureUsageProjector struct {
	normalized int
	err        error
	calls      [][3]int
}

func (f *fixtureUsageProjector) User(id, raw int) (model.UsageProjection, error) {
	f.calls = append(f.calls, [3]int{id, 0, raw})
	return model.UsageProjection{NormalizedUsedQuota: f.normalized}, f.err
}
func (f *fixtureUsageProjector) Token(id, userID, raw int) (model.UsageProjection, error) {
	f.calls = append(f.calls, [3]int{id, userID, raw})
	return model.UsageProjection{NormalizedUsedQuota: f.normalized}, f.err
}

func TestUsageProjectionUserDTOSeparatesCurrentFromHistorical(t *testing.T) {
	installPublicCreditBoundaryFixture(t, "500000")
	basis, err := captureCreditBoundaryBasis()
	require.NoError(t, err)
	for _, tc := range []struct {
		name      string
		projector usageProjector
		want      *int
	}{
		{name: "projected", projector: &fixtureUsageProjector{normalized: 123}, want: usageInt(123)},
		{name: "real-zero", projector: &fixtureUsageProjector{normalized: 0}, want: usageInt(0)},
		{name: "broken-audit", projector: &fixtureUsageProjector{err: errors.New("missing retained audit")}},
		{name: "loader-unavailable"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			user := &model.User{Id: 17, Quota: 500000, UsedQuota: 6800}
			response, err := buildPublicUserCreditResponseWithBasis(user, basis, tc.projector)
			require.NoError(t, err)
			encoded, err := json.Marshal(response)
			require.NoError(t, err)
			var data map[string]any
			require.NoError(t, json.Unmarshal(encoded, &data))
			require.EqualValues(t, 6800, data["used_quota"])
			require.EqualValues(t, 500000, data["quota"])
			require.Equal(t, "500000", data["public_credit_balance"])
			require.Equal(t, tc.want != nil, data["usage_projection_available"])
			if tc.want == nil {
				require.NotContains(t, data, "normalized_used_quota")
				require.NotContains(t, data, "public_credit_used")
			} else {
				require.EqualValues(t, *tc.want, data["normalized_used_quota"])
				require.Equal(t, jsonNumberString(*tc.want), data["public_credit_used"])
			}
			require.Equal(t, 6800, user.UsedQuota)
			require.Equal(t, 500000, user.Quota)
		})
	}
}
func usageInt(value int) *int           { return &value }
func jsonNumberString(value int) string { raw, _ := json.Marshal(value); return string(raw) }

func TestUsageProjectionTokenDTOUsesScopedProjectionAndPreservesRaw(t *testing.T) {
	token := &model.Token{Id: 27, UserId: 17, Key: "fixture-secret-key", UsedQuota: 6800, RemainQuota: 500000}
	for _, unavailable := range []bool{false, true} {
		projector := &fixtureUsageProjector{normalized: 1000}
		if unavailable {
			projector.err = errors.New("missing token audit")
		}
		response := buildMaskedTokenResponseWithProjector(token, projector)
		raw, err := json.Marshal(response)
		require.NoError(t, err)
		var data map[string]any
		require.NoError(t, json.Unmarshal(raw, &data))
		require.Equal(t, [][3]int{{27, 17, 6800}}, projector.calls)
		require.Equal(t, !unavailable, data["usage_projection_available"])
		if unavailable {
			require.NotContains(t, data, "normalized_used_quota")
		} else {
			require.EqualValues(t, 1000, data["normalized_used_quota"])
		}
		require.EqualValues(t, 6800, data["used_quota"])
		require.EqualValues(t, 500000, data["remain_quota"])
		require.NotEqual(t, token.Key, data["key"])
		require.Equal(t, 6800, token.UsedQuota)
	}
}

func seedControllerUsageAudit(t *testing.T, db *gorm.DB, user *model.User, token *model.Token) {
	t.Helper()
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (plan TEXT NOT NULL)").Error)
	plan := map[string]any{"version": 1, "kind": "offline_credit_balance_rebase_preview", "migration_id": "controller-usage-fixture", "user_ids": []int{user.Id}, "has_complete_history": true, "usd_credit_conversion": 500000, "divisor": "6.7", "rounding": "half-away-from-zero", "snapshot_at": int64(1700000000), "user_sources": []map[string]any{{"id": user.Id, "used_quota": 6700}}, "token_sources": []map[string]any{{"id": token.Id, "user_id": user.Id, "used_quota": 3350}}, "other_credit_bases": []any{}}
	raw, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases(plan) VALUES (?)", string(raw)).Error)
}

func TestUsageProjectionEndpointsUseHistoricalBasisAndKeepNewUsage(t *testing.T) {
	db := setupUserOnboardingTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.Token{}))
	installPublicCreditBoundaryFixture(t, "500000", db)
	user := model.User{Username: "usage-endpoints", Role: common.RoleRootUser, Status: common.UserStatusEnabled, Quota: 500000, UsedQuota: 6800}
	require.NoError(t, db.Create(&user).Error)
	token := model.Token{UserId: user.Id, Key: "usage-endpoints-fixture", Status: common.TokenStatusEnabled, ExpiredTime: -1, RemainQuota: 500000, UsedQuota: 3450}
	require.NoError(t, db.Create(&token).Error)
	seedControllerUsageAudit(t, db, &user, &token)
	c, w := publicCreditTestContext(t, http.MethodGet, "/api/user/self", "", user.Id)
	GetSelf(c)
	data := publicCreditResponseData(t, w)
	require.EqualValues(t, 6800, data["used_quota"])
	require.EqualValues(t, 1100, data["normalized_used_quota"])
	require.Equal(t, "1100", data["public_credit_used"])
	require.Equal(t, true, data["usage_projection_available"])
	rows, err := buildPublicUserCreditResponses([]*model.User{&user})
	require.NoError(t, err)
	require.Equal(t, 1100, *rows[0].NormalizedUsedQuota)
	tokens := buildMaskedTokenResponses([]*model.Token{&token})
	require.Equal(t, 600, *tokens[0].NormalizedUsedQuota)
	require.True(t, tokens[0].UsageProjectionAvailable)
	oldStat, oldLogs := common.DisplayTokenStatEnabled, common.LogConsumeEnabled
	common.DisplayTokenStatEnabled, common.LogConsumeEnabled = true, false
	t.Cleanup(func() { common.DisplayTokenStatEnabled, common.LogConsumeEnabled = oldStat, oldLogs })
	var sdk map[string]any
	require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, GetUsage).Body.Bytes(), &sdk))
	require.Equal(t, 0.12, sdk["total_usage"])
	require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, GetSubscription).Body.Bytes(), &sdk))
	require.Equal(t, 1.0012, sdk["hard_limit_usd"])
	c, w = publicCreditTestContext(t, http.MethodGet, "/v1/usage", "", user.Id)
	c.Set("quota_query_token", &token)
	GetQuotaQuery(c)
	var quota map[string]any
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &quota))
	require.Equal(t, 1.0, quota["remaining"])
	require.Equal(t, 0.0012, quota["used_total"])
	require.Equal(t, 1.0012, quota["total_quota"])
	// Malformed immutable history must disable only its display, not the wallet.
	require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = ?", "{").Error)
	c, w = publicCreditTestContext(t, http.MethodGet, "/api/user/self", "", user.Id)
	GetSelf(c)
	data = publicCreditResponseData(t, w)
	require.EqualValues(t, 500000, data["quota"])
	require.EqualValues(t, 6800, data["used_quota"])
	require.Equal(t, "500000", data["public_credit_balance"])
	require.Equal(t, false, data["usage_projection_available"])
	require.NotContains(t, data, "public_credit_used")
	require.NotContains(t, data, "normalized_used_quota")
	for _, handler := range []gin.HandlerFunc{GetUsage, GetSubscription} {
		var unavailable map[string]any
		require.NoError(t, json.Unmarshal(billingReadRequest(t, &token, handler).Body.Bytes(), &unavailable))
		require.Equal(t, "billing_unavailable", unavailable["error"].(map[string]any)["type"])
	}
	c, w = publicCreditTestContext(t, http.MethodGet, "/v1/usage", "", user.Id)
	c.Set("quota_query_token", &token)
	GetQuotaQuery(c)
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &quota))
	require.Equal(t, true, quota["valid"])
	require.Equal(t, false, quota["usage_projection_available"])
	require.Equal(t, 1.0, quota["remaining"])
	require.Nil(t, quota["used_total"])
	require.Nil(t, quota["total_quota"])
	require.EqualValues(t, 3450, quota["used_quota"])
	var storedUser model.User
	var storedToken model.Token
	require.NoError(t, db.First(&storedUser, user.Id).Error)
	require.NoError(t, db.First(&storedToken, token.Id).Error)
	require.Equal(t, 6800, storedUser.UsedQuota)
	require.Equal(t, 500000, storedUser.Quota)
	require.Equal(t, 3450, storedToken.UsedQuota)
	require.Equal(t, 500000, storedToken.RemainQuota)
}
