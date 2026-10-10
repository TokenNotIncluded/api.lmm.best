package model

import (
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func seedPublicCreditConfig(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Create(&Option{Key: CreditsPerUSDOptionKey, Value: "500000"}).Error)
	require.NoError(t, InitializeCreditUnits(t.Context()))
}

func TestPublicCreditSnapshotReadsDurableDenominationAndPreservesUSD(t *testing.T) {
	setupCreditUnitsDB(t)
	seedPublicCreditConfig(t)
	before, err := GetUSDPriceConfig()
	require.NoError(t, err)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", PublicCreditsPerUSDOptionKey).Update("value", "500000").Error)
	require.Error(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(999999)), "old display-only scales are rejected")
	units, err := CreditDenominationSnapshot()
	require.NoError(t, err)
	require.Equal(t, "500000", units.PublicCreditsPerUSDExact)
	require.Equal(t, "500000", units.LedgerQuotaPerUSDExact)
	amount, err := units.ProjectLedgerQuota(500000)
	require.NoError(t, err)
	require.Equal(t, "500000", amount.String())
	usd, err := common.CreditsToUSD(500000)
	require.NoError(t, err)
	require.Equal(t, "1", usd.String())
	after, err := GetUSDPriceConfig()
	require.NoError(t, err)
	require.Equal(t, before.Revision, after.Revision)
	require.Equal(t, before.Values, after.Values)
	require.Equal(t, before.ModelRatioUSDPerMillion, after.ModelRatioUSDPerMillion)
	require.Equal(t, before.CreditsPerUSD, after.CreditsPerUSD)
	require.Equal(t, "LEDGER_QUOTA_PER_TOKEN", after.ModelRatioUnit)
	require.Equal(t, "500000", after.PublicCreditsPerUSDExact)
	for _, invalid := range []string{"100000", "200000", "3359744", "0", "-1", "100000.1", "9007199254740992", "oops"} {
		require.NoError(t, DB.Model(&Option{}).Where("key = ?", PublicCreditsPerUSDOptionKey).Update("value", invalid).Error)
		_, err = CreditDenominationSnapshot()
		require.Error(t, err, "invalid durable P must not fall back to the cache")
	}
}

func TestPublicCreditUnitConfigUsesDurableCompareAndSwap(t *testing.T) {
	setupCreditUnitsDB(t)
	seedPublicCreditConfig(t)
	request := PublicCreditUnitUpdate{CreditUnitSchemaVersion: 2, PublicCreditsPerUSDExact: "500000", ExpectedPublicCreditsPerUSDExact: "500000", ExpectedLedgerQuotaPerUSDExact: "500000"}
	result, err := UpdatePublicCreditUnitConfig(request)
	require.NoError(t, err)
	require.Equal(t, "500000", result.PublicCreditsPerUSDExact)
	request.ExpectedPublicCreditsPerUSDExact = "100000"
	_, err = UpdatePublicCreditUnitConfig(request)
	require.ErrorIs(t, err, ErrPublicCreditRevisionConflict)
	stored, err := GetPublicCreditUnitConfig()
	require.NoError(t, err)
	require.Equal(t, "500000", stored.PublicCreditsPerUSDExact)
	for _, mutate := range []func(*PublicCreditUnitUpdate){
		func(r *PublicCreditUnitUpdate) { r.CreditUnitSchemaVersion = 1 },
		func(r *PublicCreditUnitUpdate) { r.PublicCreditsPerUSDExact = "0" },
		func(r *PublicCreditUnitUpdate) { r.PublicCreditsPerUSDExact = "1.5" },
		func(r *PublicCreditUnitUpdate) { r.ExpectedPublicCreditsPerUSDExact = "" },
		func(r *PublicCreditUnitUpdate) { r.ExpectedLedgerQuotaPerUSDExact = "100000" },
	} {
		bad := request
		bad.ExpectedPublicCreditsPerUSDExact = "500000"
		mutate(&bad)
		_, err := UpdatePublicCreditUnitConfig(bad)
		require.Error(t, err)
	}
	stored, err = GetPublicCreditUnitConfig()
	require.NoError(t, err)
	require.Equal(t, "500000", stored.PublicCreditsPerUSDExact)
}

func TestPublicCreditConfigPostgresConcurrentCASAndReadOnlySnapshot(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &Option{})
	previousDB, previousQ := DB, common.QuotaPerUnit
	previousLedger, previousErr := common.LedgerQuotaPerUSD()
	previousLegacy, _ := common.LegacyPricingQuotaPerUnit()
	previousPublic, _ := common.PublicCreditsPerUSD()
	DB, common.QuotaPerUnit = db, 500000
	usePostgresDatabaseType(t)
	t.Cleanup(func() {
		DB, common.QuotaPerUnit = previousDB, previousQ
		common.ClearCreditsPerUSD()
		if previousErr == nil {
			require.NoError(t, common.SetCreditCurrencyBasis(previousLedger, previousLegacy))
			if !previousPublic.Equal(previousLedger) {
				require.NoError(t, common.SetPublicCreditsPerUSD(previousPublic))
			}
		}
	})
	common.ClearCreditsPerUSD()
	require.NoError(t, DB.Create(&[]Option{{Key: "QuotaPerUnit", Value: "500000"}, {Key: CreditsPerUSDOptionKey, Value: "500000"}, {Key: LegacyPricingQuotaPerUnitOptionKey, Value: "500000"}, {Key: PublicCreditsPerUSDOptionKey, Value: "500000"}}).Error)
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	start := make(chan struct{})
	results := make(chan error, 8)
	var wait sync.WaitGroup
	for i := 0; i < 8; i++ {
		wait.Add(1)
		go func(i int) {
			defer wait.Done()
			<-start
			_, err := UpdatePublicCreditUnitConfig(PublicCreditUnitUpdate{CreditUnitSchemaVersion: 2, PublicCreditsPerUSDExact: decimal.NewFromInt(100000 + int64(i)).String(), ExpectedPublicCreditsPerUSDExact: "100000", ExpectedLedgerQuotaPerUSDExact: "500000"})
			results <- err
		}(i)
	}
	close(start)
	wait.Wait()
	close(results)
	for err := range results {
		require.Error(t, err, "every attempt to change the fixed credit denomination is rejected")
	}
	readOnly := db.Begin()
	require.NoError(t, readOnly.Exec("SET TRANSACTION READ ONLY").Error)
	_, err := CreditDenominationSnapshotForDB(readOnly)
	require.NoError(t, err, "snapshot only reads the durable configuration")
	require.NoError(t, readOnly.Rollback().Error)
}
