package model

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestAIDirectoryAdPostgresActualUSDConcurrentChargeAndHistoricalRefund(t *testing.T) {
	usePostgresDatabaseType(t)
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &AIDirectoryAd{}, &Log{})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	t.Cleanup(cancel)
	db = db.WithContext(ctx)
	previousDB, previousLog, previousRedis := DB, LOG_DB, common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	t.Cleanup(func() { DB, LOG_DB, common.RedisEnabled = previousDB, previousLog, previousRedis })
	directoryAdCurrencyFixture(t, "3500000")
	owner := User{Username: "pg-ad-owner", AffCode: "pg-ad-owner", Quota: 10_000_000}
	require.NoError(t, db.Create(&owner).Error)
	input := directoryAdInput("directory-pg-concurrent-0001", 100)
	input.ExpectedQuota = 3_500_000
	type result struct {
		ad      AIDirectoryAd
		changed bool
		err     error
	}
	results := make(chan result, 16)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ad, changed, err := CreateAIDirectoryAd(owner.Id, input, 1000)
			results <- result{ad, changed, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	created, id := 0, 0
	for outcome := range results {
		require.NoError(t, outcome.err)
		require.Equal(t, 3_500_000, outcome.ad.ChargedQuota)
		if outcome.changed {
			created++
		}
		if id != 0 {
			require.Equal(t, id, outcome.ad.ID)
		}
		id = outcome.ad.ID
	}
	require.Equal(t, 1, created)
	var wallet User
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	require.Equal(t, 6_500_000, wallet.Quota)

	// This represents an already-paid legacy row. Never recalculate its charge.
	legacyInput := directoryAdInput("directory-pg-legacy-paid-0001", 2000)
	legacyInput.ExpectedQuota = 500_000
	legacy := AIDirectoryAd{OwnerUserID: owner.Id, Name: legacyInput.Name, URL: legacyInput.URL,
		Summary: legacyInput.Summary, Description: legacyInput.Description, BidCents: legacyInput.BidCents,
		ChargedQuota: 500_000, RequestID: legacyInput.RequestID, Status: AIDirectoryAdStatusActive,
		PaidAt: 999, ExpiresAt: 999 + 30*86400}
	require.NoError(t, db.Create(&legacy).Error)
	ads, _, err := ListActiveAIDirectoryAds(1000, 0, 20)
	require.NoError(t, err)
	require.Equal(t, []int{id, legacy.ID}, []int{ads[0].ID, ads[1].ID})
	common.ClearCreditsPerUSD()
	replayed, changed, err := CreateAIDirectoryAd(owner.Id, legacyInput, 1001)
	require.NoError(t, err)
	require.False(t, changed)
	require.Equal(t, 500_000, replayed.ChargedQuota)
	results = make(chan result, 16)
	start = make(chan struct{})
	for i := 0; i < cap(results); i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			ad, refunded, err := HideAIDirectoryAd(legacy.ID, 1002)
			results <- result{ad, refunded, err}
		}()
	}
	close(start)
	workers.Wait()
	close(results)
	refunded := 0
	for outcome := range results {
		require.NoError(t, outcome.err)
		require.Equal(t, 500_000, outcome.ad.ChargedQuota)
		if outcome.changed {
			refunded++
		}
	}
	require.Equal(t, 1, refunded)
	require.NoError(t, db.First(&wallet, owner.Id).Error)
	require.Equal(t, 7_000_000, wallet.Quota)
}
