package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestProfileShareModelUsageAggregatesBeforeLimit(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&QuotaData{}))
	for _, row := range []QuotaData{
		{UserID: 1, ModelName: " alpha ", CreatedAt: 100, TokenUsed: 40, Count: 4, Quota: 40},
		{UserID: 1, ModelName: "alpha", CreatedAt: 101, TokenUsed: 30, Count: 3, Quota: 30},
		{UserID: 1, ModelName: "beta", CreatedAt: 101, TokenUsed: 20, Count: 2, Quota: 20},
		{UserID: 1, ModelName: "gamma", CreatedAt: 101, TokenUsed: 10, Count: 1, Quota: 10},
		{UserID: 1, ModelName: " ", CreatedAt: 101, TokenUsed: 5, Count: 1, Quota: 5},
		{UserID: 1, ModelName: "", CreatedAt: 101, TokenUsed: 5, Count: 1, Quota: 5},
		{UserID: 2, ModelName: "not-yours", CreatedAt: 101, TokenUsed: 1000, Count: 1, Quota: 1000},
		{UserID: 1, ModelName: "not-in-window", CreatedAt: 200, TokenUsed: 1000, Count: 1, Quota: 1000},
	} {
		require.NoError(t, db.Create(&row).Error)
	}
	usage, err := GetProfileShareModelUsage(1, 100, 101, 3)
	require.NoError(t, err)
	require.EqualValues(t, 110, usage.Tokens)
	require.EqualValues(t, 110, usage.Quota)
	require.EqualValues(t, 12, usage.Requests)
	require.EqualValues(t, 4, usage.ModelCount)
	require.Len(t, usage.Models, 3)
	require.Equal(t, "alpha", usage.Models[0].ModelName)
	require.EqualValues(t, 70, usage.Models[0].Tokens)
	require.Equal(t, "unknown", usage.Models[2].ModelName)
	empty, err := GetProfileShareModelUsage(99, 100, 101, 3)
	require.NoError(t, err)
	require.Zero(t, empty.Tokens)
	require.Empty(t, empty.Models)
	for _, args := range [][4]int64{{0, 100, 101, 3}, {1, -1, 101, 3}, {1, 101, 100, 3}, {1, 0, 366 * 86400, 3}, {1, 100, 101, 13}} {
		_, err := GetProfileShareModelUsage(int(args[0]), args[1], args[2], int(args[3]))
		require.Error(t, err)
	}
}
