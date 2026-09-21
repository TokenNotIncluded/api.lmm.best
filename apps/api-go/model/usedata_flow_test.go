package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func seedFlowQuotaData(t *testing.T, quotaData QuotaData) {
	t.Helper()
	require.NoError(t, DB.Create(&quotaData).Error)
}

func seedFlowLookupData(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Channel{Id: 1, Name: "east"}).Error)
	require.NoError(t, DB.Create(&Channel{Id: 2, Name: "west"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 11, UserId: 1, Key: "sk-primary", Name: "primary"}).Error)
	require.NoError(t, DB.Create(&Token{Id: 22, UserId: 2, Key: "sk-backup", Name: "backup"}).Error)
	require.NoError(t, DB.Delete(&Token{Id: 11}).Error)
}

func TestGetFlowQuotaDataUsesQuotaDataRoleSpecificDimensions(t *testing.T) {
	truncateTables(t)
	seedFlowLookupData(t)

	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1000,
		Count:     2,
		Quota:     100,
		TokenUsed: 40,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 1,
		CreatedAt: 1100,
		Count:     1,
		Quota:     50,
		TokenUsed: 20,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		NodeName:  "node-a",
		TokenID:   11,
		UseGroup:  "vip",
		ModelName: "gpt-a",
		ChannelID: 2,
		CreatedAt: 1200,
		Count:     1,
		Quota:     25,
		TokenUsed: 10,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    2,
		Username:  "bob",
		NodeName:  "node-b",
		TokenID:   22,
		UseGroup:  "default",
		ModelName: "gpt-b",
		ChannelID: 1,
		CreatedAt: 1300,
		Count:     3,
		Quota:     70,
		TokenUsed: 30,
	})
	seedFlowQuotaData(t, QuotaData{
		UserID:    1,
		Username:  "alice",
		ModelName: "legacy",
		CreatedAt: 1400,
		Count:     99,
		Quota:     999,
		TokenUsed: 999,
	})

	rootRows, err := GetFlowQuotaData(900, 2000, "", 0, common.RoleRootUser)
	require.NoError(t, err)
	require.Len(t, rootRows, 3)
	// Token 11 was soft-deleted, so its name is intentionally left empty for the
	// frontend to render a localized "deleted (id)" label instead.
	require.Equal(t, FlowQuotaData{
		UserID:      1,
		Username:    "alice",
		NodeName:    "node-a",
		TokenID:     11,
		TokenName:   "",
		UseGroup:    "vip",
		ChannelID:   1,
		ChannelName: "east",
		ModelName:   "gpt-a",
		TokenUsed:   60,
		Count:       3,
		Quota:       150,
	}, *rootRows[0])
	// A token that still exists resolves to its current name.
	require.Equal(t, 22, rootRows[1].TokenID)
	require.Equal(t, "backup", rootRows[1].TokenName)

	adminRows, err := GetFlowQuotaData(900, 2000, "alice", 0, common.RoleAdminUser)
	require.NoError(t, err)
	require.Len(t, adminRows, 2)
	require.Equal(t, 0, adminRows[0].TokenID)
	require.Empty(t, adminRows[0].TokenName)
	require.Empty(t, adminRows[0].NodeName)
	require.Equal(t, "alice", adminRows[0].Username)
	require.Equal(t, "vip", adminRows[0].UseGroup)
	require.Equal(t, "east", adminRows[0].ChannelName)
	require.Equal(t, 150, adminRows[0].Quota)

	selfRows, err := GetFlowQuotaData(900, 2000, "", 1, common.RoleCommonUser)
	require.NoError(t, err)
	require.Len(t, selfRows, 1)
	require.Empty(t, selfRows[0].Username)
	require.Equal(t, 0, selfRows[0].ChannelID)
	require.Empty(t, selfRows[0].ChannelName)
	require.Empty(t, selfRows[0].TokenName)
	require.Equal(t, "vip", selfRows[0].UseGroup)
	require.Equal(t, 175, selfRows[0].Quota)
}

func TestLogQuotaDataSplitsRowsByUseGroupTokenChannelAndNode(t *testing.T) {
	truncateTables(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	CacheQuotaDataLock.Unlock()

	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3661,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     100,
		TokenUsed: 40,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "vip",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     50,
		TokenUsed: 20,
	})
	LogQuotaData(QuotaDataLogParams{
		UserID:    1,
		Username:  "alice",
		ModelName: "gpt-a",
		CreatedAt: 3700,
		UseGroup:  "default",
		TokenID:   11,
		ChannelID: 1,
		NodeName:  "node-a",
		Quota:     25,
		TokenUsed: 10,
	})

	SaveQuotaDataCache()

	var rows []QuotaData
	require.NoError(t, DB.Order("quota DESC").Find(&rows).Error)
	require.Len(t, rows, 2)
	require.Equal(t, int64(3600), rows[0].CreatedAt)
	require.Equal(t, "vip", rows[0].UseGroup)
	require.Equal(t, 11, rows[0].TokenID)
	require.Equal(t, 1, rows[0].ChannelID)
	require.Equal(t, "node-a", rows[0].NodeName)
	require.Equal(t, 2, rows[0].Count)
	require.Equal(t, 150, rows[0].Quota)
	require.Equal(t, 60, rows[0].TokenUsed)
	require.Equal(t, "default", rows[1].UseGroup)
	require.Equal(t, 25, rows[1].Quota)
}

func TestQuotaDataMergeIsBoundedAndStillAggregatesKnownKeys(t *testing.T) {
	cache := make(map[string]*QuotaData)
	first := &QuotaData{UserID: 1, Username: "alice", ModelName: "model-a", Count: 1, Quota: 10, TokenUsed: 2}
	second := &QuotaData{UserID: 2, Username: "bob", ModelName: "model-b", Count: 1}
	third := &QuotaData{UserID: 3, Username: "carol", ModelName: "model-c", Count: 1}

	require.True(t, mergeQuotaData(cache, first, 2))
	require.True(t, mergeQuotaData(cache, second, 2))
	require.True(t, mergeQuotaData(cache, &QuotaData{UserID: 1, Username: "alice", ModelName: "model-a", Count: 2, Quota: 5, TokenUsed: 3}, 2))
	require.False(t, mergeQuotaData(cache, third, 2))
	require.Len(t, cache, 2)
	require.Equal(t, 3, cache[quotaDataKey(first)].Count)
	require.Equal(t, 15, cache[quotaDataKey(first)].Quota)
	require.Equal(t, 5, cache[quotaDataKey(first)].TokenUsed)
}

func TestPersistQuotaDataRejectsWalletBoundaryOverflowAtomically(t *testing.T) {
	truncateTables(t)
	existing := QuotaData{
		UserID: 1, Username: "wallet-boundary", ModelName: "model-a", CreatedAt: 3600,
		UseGroup: "default", TokenID: 1, ChannelID: 1, NodeName: "node-a",
		Count: 2, Quota: common.MaxWalletQuota, TokenUsed: 3,
	}
	require.NoError(t, DB.Create(&existing).Error)

	err := persistQuotaData(&QuotaData{
		UserID: 1, Username: "wallet-boundary", ModelName: "model-a", CreatedAt: 3600,
		UseGroup: "default", TokenID: 1, ChannelID: 1, NodeName: "node-a",
		Count: 1, Quota: 1, TokenUsed: 1,
	})
	require.ErrorIs(t, err, ErrWalletQuotaOutOfRange)

	var stored QuotaData
	require.NoError(t, DB.First(&stored, existing.Id).Error)
	require.Equal(t, common.MaxWalletQuota, stored.Quota)
	require.Equal(t, 2, stored.Count)
	require.Equal(t, 3, stored.TokenUsed)
}

func TestQuotaDataConcurrentFlushPreservesCounts(t *testing.T) {
	truncateTables(t)
	CacheQuotaDataLock.Lock()
	CacheQuotaData = make(map[string]*QuotaData)
	quotaDataDropped = 0
	CacheQuotaDataLock.Unlock()

	var workers sync.WaitGroup
	for worker := 0; worker < 8; worker++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for index := 0; index < 100; index++ {
				LogQuotaData(QuotaDataLogParams{
					UserID: 1, Username: "alice", ModelName: "model-a", CreatedAt: 3601,
					UseGroup: "default", TokenID: 1, ChannelID: 1, NodeName: "node-a", Quota: 1, TokenUsed: 1,
				})
			}
		}()
	}
	for range 4 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			SaveQuotaDataCache()
		}()
	}
	workers.Wait()
	SaveQuotaDataCache()

	var count int64
	require.NoError(t, DB.Model(&QuotaData{}).Select("COALESCE(SUM(count), 0)").Scan(&count).Error)
	require.Equal(t, int64(800), count)
}
