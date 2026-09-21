package model

import (
	"fmt"
	"strings"
	"testing"

	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestEstimateRecentModelQuotaUsesAvailableSamplesCapsAndExcludesEstimates(t *testing.T) {
	previous := LOG_DB
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))), &gorm.Config{})
	require.NoError(t, err)
	LOG_DB = db
	t.Cleanup(func() { LOG_DB = previous })
	require.NoError(t, db.AutoMigrate(&Log{}))
	for index, quota := range []int{100, 200, 300, 400} {
		require.NoError(t, db.Create(&Log{CreatedAt: int64(index + 1), Type: LogTypeConsume, ModelName: "m", PromptTokens: 1, Quota: quota}).Error)
	}
	quota, samples, err := EstimateRecentModelQuota("m", 250)
	require.NoError(t, err)
	require.Equal(t, 250, quota)
	require.Equal(t, 4, samples)
	require.NoError(t, db.Create(&Log{CreatedAt: 5, Type: LogTypeConsume, ModelName: "m", PromptTokens: 1, Quota: 500}).Error)
	require.NoError(t, db.Create(&Log{CreatedAt: 6, Type: LogTypeConsume, ModelName: "m", PromptTokens: 1, Quota: 9999, Other: `{"usage_estimated":true}`}).Error)
	quota, samples, err = EstimateRecentModelQuota("m", 250)
	require.NoError(t, err)
	require.Equal(t, 5, samples)
	require.Equal(t, 250, quota)
}
