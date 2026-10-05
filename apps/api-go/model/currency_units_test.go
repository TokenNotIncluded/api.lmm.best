package model

import (
	"context"
	"database/sql"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm/clause"
)

func setupCreditUnitsDB(t *testing.T) {
	t.Helper()
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&Option{}, &TopUp{}))
	previous, err := common.CreditsPerUSD()
	previousLegacy, _ := common.LegacyPricingQuotaPerUnit()
	previousQ := common.QuotaPerUnit
	previousOptionMap := common.OptionMap
	common.OptionMap = map[string]string{}
	t.Cleanup(func() {
		common.QuotaPerUnit = previousQ
		common.OptionMap = previousOptionMap
		if err != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previous, previousLegacy))
		}
	})
	common.ClearCreditsPerUSD()
	for key, value := range map[string]string{"QuotaPerUnit": "500000", "USDExchangeRate": "7", "TopUpPlatformUnitsPerCNY": "1.25"} {
		require.NoError(t, db.Create(&Option{Key: key, Value: value}).Error)
	}
}

func TestCreditUnitsInitializationFixedAndPreservesHistory(t *testing.T) {
	setupCreditUnitsDB(t)
	user := User{Username: "currency-owner", Quota: 123456, Password: "password"}
	require.NoError(t, DB.Create(&user).Error)
	order := TopUp{UserId: user.Id, TradeNo: "currency-paid-order", CreditedQuota: 123456, SettlementCurrency: "CNY", ExpectedAmountMicros: 1000000, Status: common.TopUpStatusSuccess}
	require.NoError(t, DB.Create(&order).Error)
	pending := TopUp{UserId: user.Id, TradeNo: "currency-pending-order", CreditedQuota: 7654321, SettlementCurrency: "USD", ExpectedAmountMicros: 2345678, Status: common.TopUpStatusPending}
	require.NoError(t, DB.Create(&pending).Error)
	require.NoError(t, InitializeCreditUnits(context.Background()))
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "4375000", anchor.String())
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "USDExchangeRate").Update("value", "8").Error)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "TopUpPlatformUnitsPerCNY").Update("value", "2").Error)
	require.NoError(t, InitializeCreditUnits(context.Background()))
	fixed, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.True(t, fixed.Equal(anchor))
	var currentUser User
	var currentOrder TopUp
	require.NoError(t, DB.First(&currentUser, user.Id).Error)
	require.NoError(t, DB.First(&currentOrder, order.Id).Error)
	require.Equal(t, user.Quota, currentUser.Quota)
	require.Equal(t, order.CreditedQuota, currentOrder.CreditedQuota)
	require.Equal(t, order.ExpectedAmountMicros, currentOrder.ExpectedAmountMicros)
	require.Equal(t, order.SettlementCurrency, currentOrder.SettlementCurrency)
	var currentPending TopUp
	require.NoError(t, DB.First(&currentPending, pending.Id).Error)
	require.Equal(t, pending, currentPending, "pending cash and credit snapshots remain byte-for-byte equivalent")
	require.Error(t, ValidateOptionValue(CreditsPerUSDOptionKey, "1"))
	require.Error(t, UpdateOptionsBulk(map[string]string{CreditsPerUSDOptionKey: "1", "USDExchangeRate": "9"}))
	var fx Option
	require.NoError(t, DB.First(&fx, "key = ?", "USDExchangeRate").Error)
	require.Equal(t, "8", fx.Value, "immutable-anchor rejection rolls back all supplied options")
	require.Error(t, UpdateOption("QuotaPerUnit", "1000000"))
	require.Error(t, UpdateOptionsBulk(map[string]string{"QuotaPerUnit": "1000000", "USDExchangeRate": "9"}))
	require.NoError(t, UpdateOption("QuotaPerUnit", "500000.0"), "numeric-equivalent legacy calibration remains idempotent")
	var calibration Option
	require.NoError(t, DB.Where("key = ?", LegacyPricingQuotaPerUnitOptionKey).First(&calibration).Error)
	require.Equal(t, "500000", calibration.Value)
	require.Error(t, UpdateOption(LegacyPricingQuotaPerUnitOptionKey, "500000"), "baseline cannot be edited even through ordinary options")
}

func TestCreditUnitsLegacyCalibrationFreezeAndDriftDetection(t *testing.T) {
	setupCreditUnitsDB(t)
	require.NoError(t, UpdateOption("QuotaPerUnit", "750000"), "before initialization the existing calibration remains configurable")
	require.NoError(t, InitializeCreditUnits(t.Context()))
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "6562500", anchor.String())
	legacy, err := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, err)
	require.Equal(t, "750000", legacy.String())
	require.Error(t, UpdateOption("QuotaPerUnit", "500000"))
	// Simulate an older binary bypassing the new option guard. New startup
	// verification must detect the authoritative calibration drift.
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", "QuotaPerUnit").Update("value", "1000000").Error)
	require.Error(t, updateOptionMap("QuotaPerUnit", "1000000"))
	require.Equal(t, float64(750000), common.QuotaPerUnit)
	snapshot, refreshErr := RefreshOptionsSnapshot(t.Context())
	require.NoError(t, refreshErr)
	require.Equal(t, float64(750000), common.QuotaPerUnit, "old-binary DB changes cannot alter the running node's actual debit scale on refresh")
	require.Equal(t, "750000", snapshot["QuotaPerUnit"])
	canonical, bridgeErr := common.LegacyAmountToUSD(decimal.NewFromInt(7))
	require.NoError(t, bridgeErr)
	require.Equal(t, "0.8", canonical.String())
	require.Error(t, VerifyCreditUnits(t.Context()))
	_, err = common.CreditsPerUSD()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
}

func TestCreditUnitsConcurrentInitialization(t *testing.T) {
	setupCreditUnitsDB(t)
	var wait sync.WaitGroup
	errors := make(chan error, 12)
	for i := 0; i < 12; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); errors <- InitializeCreditUnits(context.Background()) }()
	}
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var options []Option
	require.NoError(t, DB.Where("key = ?", CreditsPerUSDOptionKey).Find(&options).Error)
	require.Len(t, options, 1)
	require.Equal(t, "4375000", options[0].Value)
}

func TestCreditUnitsDefaultsAndCanceledInitialization(t *testing.T) {
	setupCreditUnitsDB(t)
	require.Error(t, VerifyCreditUnits(context.Background()))
	var before int64
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", CreditsPerUSDOptionKey).Count(&before).Error)
	require.Zero(t, before, "verification never creates the immutable option")
	require.NoError(t, DB.Where("key IN ?", []string{"QuotaPerUnit", "USDExchangeRate", "TopUpPlatformUnitsPerCNY"}).Delete(&Option{}).Error)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	require.Error(t, InitializeCreditUnits(ctx))
	_, err := common.CreditsPerUSD()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
	require.NoError(t, InitializeCreditUnits(context.Background()))
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "3650000", anchor.String(), "missing legacy settings use the documented old defaults, never 1:1")
	require.NoError(t, VerifyCreditUnits(context.Background()))
	require.NoError(t, DB.Create(&Option{Key: "USDExchangeRate", Value: "NaN"}).Error)
	require.Error(t, InitializeCreditUnits(context.Background()), "a cached default cannot conceal invalid persisted fiat FX")
	_, err = common.CreditsPerUSD()
	require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
}

func TestCreditUnitsInvalidPersistenceFailsClosed(t *testing.T) {
	for _, bad := range []string{"0", "-1", "NaN", "Inf", "1e100", "9007199254740992"} {
		t.Run(bad, func(t *testing.T) {
			setupCreditUnitsDB(t)
			require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(100)))
			require.NoError(t, DB.Clauses(clause.OnConflict{DoNothing: true}).Create(&Option{Key: CreditsPerUSDOptionKey, Value: bad}).Error)
			require.Error(t, InitializeCreditUnits(context.Background()))
			_, err := common.CreditsPerUSD()
			require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
		})
	}
	t.Run("invalid-initial-FX", func(t *testing.T) {
		setupCreditUnitsDB(t)
		require.NoError(t, DB.Model(&Option{}).Where("key = ?", "USDExchangeRate").Update("value", "0").Error)
		require.Error(t, InitializeCreditUnits(context.Background()))
		var count int64
		require.NoError(t, DB.Model(&Option{}).Where("key = ?", CreditsPerUSDOptionKey).Count(&count).Error)
		require.Zero(t, count)
	})
}

func TestCreditUnitsPostgresConcurrentInitialization(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &Option{})
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	usePostgresDatabaseType(t)
	previous, previousErr := common.CreditsPerUSD()
	previousLegacy, _ := common.LegacyPricingQuotaPerUnit()
	previousQ := common.QuotaPerUnit
	t.Cleanup(func() {
		common.QuotaPerUnit = previousQ
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previous, previousLegacy))
		}
	})
	common.ClearCreditsPerUSD()
	for key, value := range map[string]string{"QuotaPerUnit": "500000", "USDExchangeRate": "7", "TopUpPlatformUnitsPerCNY": "1.25"} {
		require.NoError(t, db.Create(&Option{Key: key, Value: value}).Error)
	}
	require.ErrorContains(t, VerifyCreditUnits(t.Context()), "migrate --apply")
	var wait sync.WaitGroup
	errors := make(chan error, 16)
	start := make(chan struct{})
	for i := 0; i < 16; i++ {
		wait.Add(1)
		go func() { defer wait.Done(); <-start; errors <- InitializeCreditUnits(t.Context()) }()
	}
	close(start)
	wait.Wait()
	close(errors)
	for err := range errors {
		require.NoError(t, err)
	}
	var rows []Option
	require.NoError(t, db.Where("key = ?", CreditsPerUSDOptionKey).Find(&rows).Error)
	require.Len(t, rows, 1)
	require.Equal(t, "4375000", rows[0].Value)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", "USDExchangeRate").Update("value", "8").Error)
	require.NoError(t, db.Model(&Option{}).Where("key = ?", "TopUpPlatformUnitsPerCNY").Update("value", "2").Error)
	require.NoError(t, VerifyCreditUnits(t.Context()))
	readOnly := db.Begin(&sql.TxOptions{ReadOnly: true})
	require.NoError(t, readOnly.Error)
	DB = readOnly
	verifyErr := VerifyCreditUnits(t.Context())
	DB = db
	require.NoError(t, verifyErr, "verification also works in a PostgreSQL read-only transaction")
	require.NoError(t, readOnly.Rollback().Error)
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	require.Equal(t, "4375000", anchor.String())
}

func TestCreditUnitsPostgresCalibrationInitializationFence(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &Option{})
	previousDB, previousMap, previousQ := DB, common.OptionMap, common.QuotaPerUnit
	previous, previousErr := common.CreditsPerUSD()
	previousLegacy, _ := common.LegacyPricingQuotaPerUnit()
	DB, common.OptionMap = db, map[string]string{}
	usePostgresDatabaseType(t)
	t.Cleanup(func() {
		DB, common.OptionMap, common.QuotaPerUnit = previousDB, previousMap, previousQ
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditCurrencyBasis(previous, previousLegacy))
		}
	})
	for key, value := range map[string]string{"QuotaPerUnit": "500000", "USDExchangeRate": "7", "TopUpPlatformUnitsPerCNY": "1.25"} {
		require.NoError(t, db.Create(&Option{Key: key, Value: value}).Error)
	}
	start := make(chan struct{})
	var wait sync.WaitGroup
	var initErr, updateErr error
	wait.Add(2)
	go func() { defer wait.Done(); <-start; initErr = InitializeCreditUnits(t.Context()) }()
	go func() { defer wait.Done(); <-start; updateErr = UpdateOption("QuotaPerUnit", "1000000") }()
	close(start)
	wait.Wait()
	require.NoError(t, initErr)
	require.NoError(t, VerifyCreditUnits(t.Context()))
	anchor, err := common.CreditsPerUSD()
	require.NoError(t, err)
	baseline, err := common.LegacyPricingQuotaPerUnit()
	require.NoError(t, err)
	if updateErr == nil {
		require.Equal(t, "1000000", baseline.String())
		require.Equal(t, "8750000", anchor.String())
	} else {
		require.ErrorContains(t, updateErr, "immutable")
		require.Equal(t, "500000", baseline.String())
		require.Equal(t, "4375000", anchor.String())
	}
	require.Error(t, UpdateOptionsBulk(map[string]string{"QuotaPerUnit": "2000000", "USDExchangeRate": "99"}))
	var rate Option
	require.NoError(t, db.Where("key = ?", "USDExchangeRate").First(&rate).Error)
	require.Equal(t, "7", rate.Value)
}
