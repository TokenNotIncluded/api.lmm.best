package model

import (
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestStandardTopUpCreditedQuotaRejectsInt64OverflowWithoutWrapping(t *testing.T) {
	previousQuotaPerUnit := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })

	common.QuotaPerUnit = 500_000
	assert.EqualValues(t, 12_500_000, StandardTopUpCreditedQuota(25))

	common.QuotaPerUnit = 1e19
	quota, err := standardTopUpCreditedQuotaChecked(1)
	assert.ErrorIs(t, err, ErrInvalidTopUpQuota)
	assert.Zero(t, quota)
	assert.Zero(t, StandardTopUpCreditedQuota(1))

	common.QuotaPerUnit = math.Inf(1)
	assert.Zero(t, StandardTopUpCreditedQuota(1))
}

func setupExternalTopUpSettlementDB(t *testing.T, maxOpenConnections int) *gorm.DB {
	t.Helper()
	previousDB := DB
	previousDatabaseType := common.MainDatabaseType()
	databasePath := filepath.Join(t.TempDir(), "settlement.db")
	db, err := gorm.Open(sqlite.Open(fmt.Sprintf("%s?_busy_timeout=5000&_journal_mode=WAL", databasePath)), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(maxOpenConnections)
	require.NoError(t, db.AutoMigrate(&User{}, &TopUp{}))
	DB = db
	common.SetMainDatabaseType(common.DatabaseTypeSQLite)
	t.Cleanup(func() {
		DB = previousDB
		common.SetMainDatabaseType(previousDatabaseType)
		_ = sqlDB.Close()
	})
	return db
}

func createSettlementFixture(t *testing.T, db *gorm.DB, tradeNo string) (User, TopUp, ExternalTopUpSettlement) {
	t.Helper()
	user := User{
		Username: fmt.Sprintf("settlement-%s", tradeNo),
		Password: "password",
		AffCode:  "aff-" + tradeNo,
		Status:   common.UserStatusEnabled,
		Quota:    100,
	}
	require.NoError(t, db.Create(&user).Error)
	topUp := TopUp{
		UserId:               user.Id,
		Amount:               2,
		CreditedQuota:        1_234,
		ExpectedAmountMicros: 12_340_000,
		SettlementCurrency:   "USD",
		Money:                12.34,
		TradeNo:              tradeNo,
		PaymentMethod:        PaymentMethodStripe,
		PaymentProvider:      PaymentProviderStripe,
		ProviderProductId:    "prod_" + tradeNo,
		ProviderStoreId:      "store_" + tradeNo,
		Status:               common.TopUpStatusPending,
	}
	require.NoError(t, db.Create(&topUp).Error)
	settlement := ExternalTopUpSettlement{
		TradeNo:               tradeNo,
		PaymentProvider:       PaymentProviderStripe,
		PaymentMethod:         PaymentMethodStripe,
		SettlementCurrency:    "usd",
		SettledAmountMicros:   12_340_000,
		ProviderProductId:     topUp.ProviderProductId,
		ProviderStoreId:       topUp.ProviderStoreId,
		ProviderEventId:       "evt_" + tradeNo,
		ProviderTransactionId: "pi_" + tradeNo,
		StripeCustomer:        "cus_" + tradeNo,
	}
	return user, topUp, settlement
}

func TestCompleteExternalTopUpAtomicallyPersistsEvidenceAndCreditsQuota(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, _, settlement := createSettlementFixture(t, db, "atomic-success")

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NotNil(t, completed)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.EqualValues(t, 1_234, completed.CreditedQuota)
	assert.EqualValues(t, 12_340_000, completed.ExpectedAmountMicros)
	assert.EqualValues(t, 12_340_000, completed.SettledAmountMicros)
	assert.Equal(t, "USD", completed.SettlementCurrency)
	assert.Equal(t, settlement.ProviderProductId, completed.ProviderProductId)
	assert.Equal(t, settlement.ProviderStoreId, completed.ProviderStoreId)
	assert.Equal(t, settlement.ProviderEventId, evidenceValue(completed.ProviderEventId))
	assert.Equal(t, settlement.ProviderTransactionId, evidenceValue(completed.ProviderTransactionId))
	assert.Positive(t, completed.CompleteTime)

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
	assert.Equal(t, settlement.StripeCustomer, reloadedUser.StripeCustomer)
}

func TestCompleteExternalTopUpRejectsWalletQuotaOverflow(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "wallet-overflow")
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).
		Update("quota", common.MaxWalletQuota-500).Error)

	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)

	var reloadedTopUp TopUp
	require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, common.MaxWalletQuota-500, reloadedUser.Quota)
}

func TestCompleteExternalTopUpWalletGuardCoversEveryProvider(t *testing.T) {
	providers := []struct {
		name     string
		provider string
		method   string
		currency string
	}{
		{name: "epay", provider: PaymentProviderEpay, method: "alipay", currency: "CNY"},
		{name: "stripe", provider: PaymentProviderStripe, method: PaymentMethodStripe, currency: "USD"},
		{name: "creem", provider: PaymentProviderCreem, method: PaymentMethodCreem, currency: "USD"},
		{name: "waffo", provider: PaymentProviderWaffo, method: PaymentMethodWaffo, currency: "USD"},
		{name: "waffo pancake", provider: PaymentProviderWaffoPancake, method: PaymentMethodWaffoPancake, currency: "USD"},
	}
	for _, tc := range providers {
		t.Run(tc.name, func(t *testing.T) {
			db := setupExternalTopUpSettlementDB(t, 1)
			user, topUp, settlement := createSettlementFixture(t, db, "provider-boundary-"+tc.name)
			require.NoError(t, db.Model(&topUp).Updates(map[string]interface{}{
				"payment_provider":    tc.provider,
				"payment_method":      tc.method,
				"settlement_currency": tc.currency,
			}).Error)
			settlement.PaymentProvider = tc.provider
			settlement.PaymentMethod = tc.method
			settlement.SettlementCurrency = tc.currency
			if tc.provider != PaymentProviderStripe {
				settlement.ProviderQuotedAmountMicros = 0
			}
			overflowingCurrent := common.MaxWalletQuota - int(topUp.CreditedQuota) + 1
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", overflowingCurrent).Error)

			_, err := CompleteExternalTopUp(settlement)
			require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)

			var reloadedTopUp TopUp
			require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
			assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
			assert.Zero(t, reloadedTopUp.CompleteTime)
			var reloadedUser User
			require.NoError(t, db.First(&reloadedUser, user.Id).Error)
			assert.Equal(t, overflowingCurrent, reloadedUser.Quota)
		})
	}
}

func TestManualCompleteTopUpRollsBackStatusAtWalletBoundary(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, _ := createSettlementFixture(t, db, "manual-wallet-boundary")
	overflowingCurrent := common.MaxWalletQuota - int(topUp.CreditedQuota) + 1
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", overflowingCurrent).Error)

	err := ManualCompleteTopUp(topUp.TradeNo, "127.0.0.1")
	require.ErrorIs(t, err, ErrTopUpQuotaLimitExceeded)

	var reloadedTopUp TopUp
	require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
	assert.Zero(t, reloadedTopUp.CompleteTime)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, overflowingCurrent, reloadedUser.Quota)
}

func TestCompleteExternalTopUpRollsBackOrderWhenUserCreditFails(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "credit-rollback")
	injectedError := errors.New("injected user update failure")
	callbackName := "test:fail_external_topup_user_update"
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(callbackName, func(tx *gorm.DB) {
		if tx.Statement.Table == "users" {
			tx.AddError(injectedError)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Update().Remove(callbackName) })

	completed, err := CompleteExternalTopUp(settlement)
	assert.Nil(t, completed)
	require.ErrorIs(t, err, injectedError)

	var reloadedTopUp TopUp
	require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
	assert.Zero(t, reloadedTopUp.SettledAmountMicros)
	assert.Zero(t, reloadedTopUp.CompleteTime)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestCompleteExternalTopUpSequentialReplayIsIdempotent(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, _, settlement := createSettlementFixture(t, db, "sequential-replay")

	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
}

func TestCompleteExternalTopUpLegacySuccessfulReplayIsMigrationSafe(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "legacy-success-replay")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"status":              common.TopUpStatusSuccess,
		"complete_time":       int64(123),
		"provider_product_id": "",
		"provider_store_id":   "",
	}).Error)

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.EqualValues(t, settlement.SettledAmountMicros, completed.SettledAmountMicros)
	assert.Equal(t, "USD", completed.SettlementCurrency)
	assert.Equal(t, settlement.ProviderStoreId, completed.ProviderStoreId)
	assert.Equal(t, settlement.ProviderEventId, evidenceValue(completed.ProviderEventId))
	assert.Equal(t, settlement.ProviderTransactionId, evidenceValue(completed.ProviderTransactionId))
	assert.EqualValues(t, 123, completed.CompleteTime)

	var reloadedTopUp TopUp
	require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
	assert.True(t, settlementEvidenceMatches(&reloadedTopUp, normalizeSettlement(settlement)))
	assert.EqualValues(t, 123, reloadedTopUp.CompleteTime)

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)

	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)

	conflicting := settlement
	conflicting.ProviderProductId = "prod_legacy_conflict"
	_, err = CompleteExternalTopUp(conflicting)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestCompleteExternalTopUpLegacyCreemBindsAndRejectsConflictingProductEvidence(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "legacy-creem-product")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":    PaymentProviderCreem,
		"payment_method":      PaymentMethodCreem,
		"status":              common.TopUpStatusSuccess,
		"complete_time":       int64(456),
		"provider_product_id": "",
		"provider_store_id":   "",
	}).Error)
	settlement.PaymentProvider = PaymentProviderCreem
	settlement.PaymentMethod = PaymentMethodCreem
	settlement.ProviderProductId = "prod_signed_creem"
	settlement.ProviderStoreId = "store_signed_creem"

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	assert.Equal(t, settlement.ProviderProductId, completed.ProviderProductId)
	assert.Equal(t, settlement.ProviderStoreId, completed.ProviderStoreId)
	assert.EqualValues(t, 456, completed.CompleteTime)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)

	conflicting := settlement
	conflicting.ProviderProductId = "prod_conflicting_creem"
	_, err = CompleteExternalTopUp(conflicting)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100, reloadedUser.Quota)
}

func TestCompleteExternalTopUpAcceptsSignedStripePromotionSubtotal(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, _, settlement := createSettlementFixture(t, db, "stripe-promotion")
	settlement.ProviderQuotedAmountMicros = settlement.SettledAmountMicros
	settlement.SettledAmountMicros = 10_000_000

	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)

	_, _, excessive := createSettlementFixture(t, db, "stripe-promotion-excess")
	excessive.ProviderQuotedAmountMicros = excessive.SettledAmountMicros
	excessive.SettledAmountMicros++
	_, err = CompleteExternalTopUp(excessive)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestCompleteExternalTopUpRejectsConflictingReplayEvidence(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, _, settlement := createSettlementFixture(t, db, "conflicting-replay")
	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)

	conflictingEvent := settlement
	conflictingEvent.ProviderEventId = "evt_other"
	_, err = CompleteExternalTopUp(conflictingEvent)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	conflictingTransaction := settlement
	conflictingTransaction.ProviderTransactionId = "pi_other"
	_, err = CompleteExternalTopUp(conflictingTransaction)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
}

func TestCompleteExternalTopUpConcurrentReplayCreditsExactlyOnce(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 4)
	user, _, settlement := createSettlementFixture(t, db, "concurrent-replay")

	const replays = 8
	start := make(chan struct{})
	errorsByReplay := make(chan error, replays)
	var waitGroup sync.WaitGroup
	for replay := 0; replay < replays; replay++ {
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			<-start
			_, err := CompleteExternalTopUp(settlement)
			errorsByReplay <- err
		}()
	}
	close(start)
	waitGroup.Wait()
	close(errorsByReplay)
	for err := range errorsByReplay {
		require.NoError(t, err)
	}

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
	var successfulOrders int64
	require.NoError(t, db.Model(&TopUp{}).
		Where("trade_no = ? AND status = ?", settlement.TradeNo, common.TopUpStatusSuccess).
		Count(&successfulOrders).Error)
	assert.EqualValues(t, 1, successfulOrders)
}

func TestCompleteExternalTopUpIndependentHandlesCreditExactlyOnce(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 4)
	user, _, settlement := createSettlementFixture(t, db, "independent-handles")
	var database struct {
		File string
	}
	require.NoError(t, db.Raw("PRAGMA database_list").Scan(&database).Error)
	require.NotEmpty(t, database.File)
	openHandle := func() *gorm.DB {
		handle, err := gorm.Open(sqlite.Open(fmt.Sprintf("%s?_busy_timeout=5000&_journal_mode=WAL", database.File)), &gorm.Config{})
		require.NoError(t, err)
		sqlDB, err := handle.DB()
		require.NoError(t, err)
		sqlDB.SetMaxOpenConns(1)
		t.Cleanup(func() { _ = sqlDB.Close() })
		return handle
	}

	// These are separate gorm/sql.DB handles and bypass the process-local
	// CompleteExternalTopUp shard lock by calling the database CAS seam.
	handleOne := openHandle()
	handleTwo := openHandle()
	start := make(chan struct{})
	errorsByHandle := make(chan error, 2)
	var waitGroup sync.WaitGroup
	for _, handle := range []*gorm.DB{handleOne, handleTwo} {
		waitGroup.Add(1)
		go func(handle *gorm.DB) {
			defer waitGroup.Done()
			<-start
			_, err := completeExternalTopUpOnDB(handle, normalizeSettlement(settlement))
			errorsByHandle <- err
		}(handle)
	}
	close(start)
	waitGroup.Wait()
	close(errorsByHandle)
	for err := range errorsByHandle {
		require.NoError(t, err)
	}

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
}

func TestCompleteExternalTopUpCreditsEpayNonCNYSnapshot(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "epay-ldc")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":       PaymentProviderEpay,
		"payment_method":         PaymentProviderEpay,
		"settlement_currency":    "LDC",
		"provider_product_id":    "",
		"provider_store_id":      "",
		"expected_amount_micros": 5_000_000,
		"credited_quota":         2_000,
	}).Error)
	settlement.PaymentProvider = PaymentProviderEpay
	settlement.PaymentMethod = PaymentProviderEpay
	settlement.SettlementCurrency = "LDC"
	settlement.SettledAmountMicros = 5_000_000
	settlement.ProviderQuotedAmountMicros = 0
	settlement.ProviderProductId = ""
	settlement.ProviderStoreId = ""

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	require.NotNil(t, completed)
	assert.Equal(t, common.TopUpStatusSuccess, completed.Status)
	assert.Equal(t, "LDC", completed.SettlementCurrency)
	assert.EqualValues(t, 5_000_000, completed.SettledAmountMicros)
	assert.EqualValues(t, 2_000, completed.CreditedQuota)

	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+2_000, reloadedUser.Quota)
}

func TestCompleteExternalTopUpRejectsEpayWithoutImmutableSnapshot(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, settlement := createSettlementFixture(t, db, "epay-legacy")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":       PaymentProviderEpay,
		"payment_method":         "alipay",
		"settlement_currency":    "",
		"expected_amount_micros": 0,
		"credited_quota":         0,
		"provider_product_id":    "",
		"provider_store_id":      "",
	}).Error)
	settlement.PaymentProvider = PaymentProviderEpay
	settlement.PaymentMethod = "alipay"
	settlement.SettlementCurrency = "CNY"
	settlement.ProviderQuotedAmountMicros = 0
	settlement.ProviderProductId = ""
	settlement.ProviderStoreId = ""

	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestCompleteExternalTopUpRejectsExpectedMoneyOrCurrencyMismatch(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, settlement := createSettlementFixture(t, db, "money-mismatch")

	wrongAmount := settlement
	wrongAmount.SettledAmountMicros++
	_, err := CompleteExternalTopUp(wrongAmount)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	wrongCurrency := settlement
	wrongCurrency.SettlementCurrency = "EUR"
	_, err = CompleteExternalTopUp(wrongCurrency)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	var reloadedTopUp TopUp
	require.NoError(t, db.First(&reloadedTopUp, topUp.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, reloadedTopUp.Status)
}

func TestCompleteExternalTopUpCreemBlankCurrencyDoesNotDisableComparison(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, settlement := createSettlementFixture(t, db, "creem-blank-currency")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":    PaymentProviderCreem,
		"payment_method":      PaymentMethodCreem,
		"provider_product_id": "prod_creem",
		"settlement_currency": "",
	}).Error)
	settlement.PaymentProvider = PaymentProviderCreem
	settlement.PaymentMethod = PaymentMethodCreem
	settlement.ProviderProductId = "prod_creem"
	settlement.SettlementCurrency = "USD"

	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestCompleteExternalTopUpWaffoPancakeBindsStoreEvidence(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, settlement := createSettlementFixture(t, db, "pancake-store")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":    PaymentProviderWaffoPancake,
		"payment_method":      PaymentMethodWaffoPancake,
		"provider_product_id": "prod_pancake",
		"provider_store_id":   "store_expected",
	}).Error)
	settlement.PaymentProvider = PaymentProviderWaffoPancake
	settlement.PaymentMethod = PaymentMethodWaffoPancake
	settlement.ProviderStoreId = "store_other"

	_, err := CompleteExternalTopUp(settlement)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)
}

func TestCompleteExternalTopUpWaffoPancakeAcceptsBoundStoreWhenWebhookLacksProductID(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, topUp, settlement := createSettlementFixture(t, db, "pancake-product-limitation")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":    PaymentProviderWaffoPancake,
		"payment_method":      PaymentMethodWaffoPancake,
		"provider_product_id": "prod_pancake",
		"provider_store_id":   "store_expected",
	}).Error)
	settlement.PaymentProvider = PaymentProviderWaffoPancake
	settlement.PaymentMethod = PaymentMethodWaffoPancake
	settlement.ProviderProductId = ""
	settlement.ProviderStoreId = "store_expected"

	_, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
}

func TestCompleteExternalTopUpWaffoPancakePendingBindsEmptyStoreAndReplaysWithoutProductID(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	user, topUp, settlement := createSettlementFixture(t, db, "pancake-empty-store")
	require.NoError(t, db.Model(&topUp).Updates(map[string]any{
		"payment_provider":    PaymentProviderWaffoPancake,
		"payment_method":      PaymentMethodWaffoPancake,
		"provider_product_id": "prod_configured_only",
		"provider_store_id":   "",
	}).Error)
	settlement.PaymentProvider = PaymentProviderWaffoPancake
	settlement.PaymentMethod = PaymentMethodWaffoPancake
	settlement.ProviderProductId = ""
	settlement.ProviderStoreId = "store_signed_pancake"

	completed, err := CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	assert.Equal(t, "prod_configured_only", completed.ProviderProductId)
	assert.Equal(t, settlement.ProviderStoreId, completed.ProviderStoreId)
	_, err = CompleteExternalTopUp(settlement)
	require.NoError(t, err)
	var reloadedUser User
	require.NoError(t, db.First(&reloadedUser, user.Id).Error)
	assert.Equal(t, 100+1_234, reloadedUser.Quota)
}

func TestCompleteExternalTopUpRejectsProductMismatchAndEvidenceReuse(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	_, firstTopUp, firstSettlement := createSettlementFixture(t, db, "bound-evidence-one")
	firstTopUp.ProviderProductId = "product_one"
	require.NoError(t, db.Model(&firstTopUp).Update("provider_product_id", firstTopUp.ProviderProductId).Error)
	firstSettlement.ProviderProductId = firstTopUp.ProviderProductId
	_, err := CompleteExternalTopUp(firstSettlement)
	require.NoError(t, err)

	_, secondTopUp, secondSettlement := createSettlementFixture(t, db, "bound-evidence-two")
	secondTopUp.ProviderProductId = "product_two"
	require.NoError(t, db.Model(&secondTopUp).Update("provider_product_id", secondTopUp.ProviderProductId).Error)
	secondSettlement.ProviderProductId = "wrong_product"
	_, err = CompleteExternalTopUp(secondSettlement)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	secondSettlement.ProviderProductId = secondTopUp.ProviderProductId
	secondSettlement.ProviderEventId = firstSettlement.ProviderEventId
	_, err = CompleteExternalTopUp(secondSettlement)
	require.ErrorIs(t, err, ErrPaymentEvidenceConflict)

	var reloaded TopUp
	require.NoError(t, db.First(&reloaded, secondTopUp.Id).Error)
	assert.Equal(t, common.TopUpStatusPending, reloaded.Status)
}

func TestPaidTopUpsAlwaysCreditAfterDiscountCapacityChanges(t *testing.T) {
	db := setupExternalTopUpSettlementDB(t, 1)
	previousLogDB := LOG_DB
	previousRedisEnabled := common.RedisEnabled
	LOG_DB = db
	common.RedisEnabled = false
	t.Cleanup(func() {
		LOG_DB = previousLogDB
		common.RedisEnabled = previousRedisEnabled
	})
	require.NoError(t, db.AutoMigrate(&DiscountCode{}, &DiscountCodeReservation{}, &Log{}))

	code := DiscountCode{
		Code:            "MANUAL-ONCE",
		Status:          DiscountCodeStatusEnabled,
		DiscountPercent: 10,
		MaxUses:         1,
	}
	require.NoError(t, db.Create(&code).Error)
	firstUser, firstTopUp, _ := createSettlementFixture(t, db, "manual-discount-one")
	secondUser, secondTopUp, _ := createSettlementFixture(t, db, "manual-discount-two")
	require.NoError(t, db.Model(&TopUp{}).Where("id IN ?", []int{firstTopUp.Id, secondTopUp.Id}).Update("discount_code_id", code.Id).Error)

	require.NoError(t, ManualCompleteTopUp(firstTopUp.TradeNo, "127.0.0.1"))
	var consumed DiscountCode
	require.NoError(t, db.First(&consumed, code.Id).Error)
	assert.EqualValues(t, 1, consumed.UsedCount)

	// This models a legacy/raced order that already reached the provider before
	// the final coupon slot was consumed. Payment settlement must still credit.
	require.NoError(t, ManualCompleteTopUp(secondTopUp.TradeNo, "127.0.0.1"))
	require.NoError(t, db.First(&consumed, code.Id).Error)
	assert.EqualValues(t, 2, consumed.UsedCount)
	var settled TopUp
	require.NoError(t, db.First(&settled, secondTopUp.Id).Error)
	assert.Equal(t, common.TopUpStatusSuccess, settled.Status)
	var reloadedFirstUser, reloadedSecondUser User
	require.NoError(t, db.First(&reloadedFirstUser, firstUser.Id).Error)
	require.NoError(t, db.First(&reloadedSecondUser, secondUser.Id).Error)
	assert.Equal(t, 100+1_234, reloadedFirstUser.Quota)
	assert.Equal(t, 100+1_234, reloadedSecondUser.Quota)
}
