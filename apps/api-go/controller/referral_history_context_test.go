package controller

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

type referralHistoryContextKey struct{}

func performReferralHistoryContextRequest(t *testing.T, ctx context.Context, identity interface{}) (int, map[string]interface{}) {
	t.Helper()
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	if identity != nil {
		c.Set("id", identity)
	}
	c.Request = httptest.NewRequest(http.MethodGet, "/api/user/self/aff/rewards?user_id=9999", nil).WithContext(ctx)
	GetReferralRewards(c)
	var response map[string]interface{}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	return recorder.Code, response
}

func TestReferralHistoryPropagatesContextToBothDatabaseReads(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralLedgerEntry{}))
	user := model.User{Username: "history-context", AffCode: "history-context"}
	require.NoError(t, db.Create(&user).Error)
	var markers []interface{}
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:history-context", func(query *gorm.DB) {
		markers = append(markers, query.Statement.Context.Value(referralHistoryContextKey{}))
	}))
	ctx := context.WithValue(context.Background(), referralHistoryContextKey{}, "incoming-request")
	code, response := performReferralHistoryContextRequest(t, ctx, user.Id)
	require.Equal(t, http.StatusOK, code)
	require.Equal(t, true, response["success"])
	require.Equal(t, []interface{}{"incoming-request", "incoming-request"}, markers)
}

func TestReferralHistoryCancelledRequestDoesNotReturnHistory(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralLedgerEntry{}))
	user := model.User{Username: "history-cancel", AffCode: "history-cancel", AffQuota: 1234}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.ReferralLedgerEntry{UserId: user.Id, RewardId: 1, EventKey: "cancel-private", Kind: "reward", Quota: 1234}).Error)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, response := performReferralHistoryContextRequest(t, ctx, user.Id)
	require.Equal(t, false, response["success"])
	require.NotContains(t, response, "data")
}

func TestReferralHistoryCancellationBetweenReadsDoesNotReturnPartialData(t *testing.T) {
	db := setupManageUserTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.ReferralLedgerEntry{}))
	user := model.User{Username: "history-between", AffCode: "history-between"}
	require.NoError(t, db.Create(&user).Error)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	queries := 0
	require.NoError(t, db.Callback().Query().After("gorm:query").Register("test:history-cancel-between", func(_ *gorm.DB) {
		queries++
		if queries == 1 {
			cancel()
		}
	}))
	_, response := performReferralHistoryContextRequest(t, ctx, user.Id)
	require.Equal(t, false, response["success"])
	require.NotContains(t, response, "data")
}

func TestReferralHistoryRejectsMissingIdentityBeforeReadingDatabase(t *testing.T) {
	db := setupManageUserTestDB(t)
	queries := 0
	require.NoError(t, db.Callback().Query().Before("gorm:query").Register("test:history-no-identity", func(_ *gorm.DB) {
		queries++
	}))
	for _, identity := range []interface{}{nil, 0, -1, "1"} {
		code, response := performReferralHistoryContextRequest(t, context.Background(), identity)
		require.Equal(t, http.StatusUnauthorized, code)
		require.Equal(t, false, response["success"])
		require.NotContains(t, response, "data")
	}
	require.Zero(t, queries)
}
