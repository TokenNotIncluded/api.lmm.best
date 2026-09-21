package model

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/glebarez/sqlite"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupTopUpAccessTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	previousDB := DB
	dsn := fmt.Sprintf("file:%s?mode=memory&cache=shared", strings.ReplaceAll(t.Name(), "/", "_"))
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	require.NoError(t, err)
	DB = db
	require.NoError(t, db.AutoMigrate(&TopUp{}))

	t.Cleanup(func() {
		DB = previousDB
		sqlDB, err := db.DB()
		if err == nil {
			_ = sqlDB.Close()
		}
	})
	return db
}

func TestHasSuccessfulPaidTopUpRequiresCompletedExternalPayment(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	userID := 42

	fixtures := []TopUp{
		{UserId: userID, TradeNo: "pending", Amount: 100, Money: 10, Status: common.TopUpStatusPending, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "failed", Amount: 100, Money: 10, Status: common.TopUpStatusFailed, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "zero-money", Amount: 100, Money: 0, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "admin-grant", Amount: 0, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "balance", Amount: 100, Money: 10, Status: common.TopUpStatusSuccess, PaymentMethod: PaymentMethodBalance, PaymentProvider: PaymentProviderBalance},
		{UserId: userID + 1, TradeNo: "other-user", Amount: 100, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
	}
	require.NoError(t, db.Create(&fixtures).Error)

	granted, err := HasSuccessfulPaidTopUp(userID)
	require.NoError(t, err)
	assert.False(t, granted)

	require.NoError(t, db.Create(&TopUp{
		UserId:        userID,
		TradeNo:       "legacy-success",
		Amount:        1,
		Money:         10,
		Status:        common.TopUpStatusSuccess,
		PaymentMethod: "alipay",
	}).Error)

	granted, err = HasSuccessfulPaidTopUp(userID)
	require.NoError(t, err)
	assert.True(t, granted)

	require.NoError(t, db.Create(&TopUp{
		UserId:        userID + 2,
		TradeNo:       "legacy-unknown-source",
		Amount:        1,
		Money:         10,
		Status:        common.TopUpStatusSuccess,
		PaymentMethod: "manual",
	}).Error)
	granted, err = HasSuccessfulPaidTopUp(userID + 2)
	require.NoError(t, err)
	assert.False(t, granted)
}

func TestHasSuccessfulPaidTopUpRejectsUnknownNullProviderWithCreditedQuota(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	topUp := TopUp{
		UserId:          48,
		TradeNo:         "unknown-null-provider",
		Amount:          10,
		CreditedQuota:   10,
		Money:           10,
		Status:          common.TopUpStatusSuccess,
		PaymentMethod:   "unknown",
		PaymentProvider: "",
	}
	require.NoError(t, db.Create(&topUp).Error)
	require.NoError(t, db.Exec("UPDATE top_ups SET payment_provider = NULL WHERE id = ?", topUp.Id).Error)
	var providerIsNull int
	require.NoError(t, db.Raw("SELECT payment_provider IS NULL FROM top_ups WHERE id = ?", topUp.Id).Scan(&providerIsNull).Error)
	assert.Equal(t, 1, providerIsNull)

	granted, err := HasSuccessfulPaidTopUp(48)
	require.NoError(t, err)
	assert.False(t, granted)
	aggregate, err := getFreshPaidTopUpAggregate(48)
	require.NoError(t, err)
	assert.False(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
	assert.Zero(t, aggregate.PaidAmountMicros)
}

func TestSuccessfulExternalTopUpPredicateIsSharedWithTrustAggregation(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	userID := 43
	fixtures := []TopUp{
		{UserId: userID, TradeNo: "redemption-like", Amount: 10, Money: 0, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "quota-grant", Amount: 0, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
		{UserId: userID, TradeNo: "admin-canonical-grant", Amount: 10, CreditedQuota: int64(common.QuotaPerUnit), Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: "admin"},
		{UserId: userID, TradeNo: "balance-method", Amount: 10, Money: 10, Status: common.TopUpStatusSuccess, PaymentMethod: PaymentMethodBalance},
		{UserId: userID, TradeNo: "balance-provider", Amount: 10, Money: 10, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderBalance},
	}
	require.NoError(t, db.Create(&fixtures).Error)

	activated, err := HasSuccessfulPaidTopUp(userID)
	require.NoError(t, err)
	assert.False(t, activated)
	aggregate, err := getPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.Zero(t, aggregate.PaidAmount)
	assert.False(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	require.NoError(t, db.Create(&TopUp{UserId: userID, TradeNo: "successful-external", Amount: 1, CreditedQuota: int64(common.QuotaPerUnit), Money: 0.01, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe}).Error)
	activated, err = HasSuccessfulPaidTopUp(userID)
	require.NoError(t, err)
	assert.True(t, activated)
	aggregate, err = getPaidTopUpAggregate(userID)
	require.NoError(t, err)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
	assert.InDelta(t, 1, aggregate.PaidAmount, 0.0001)
}

func TestFreshPaidTopUpAggregateNormalizesProviderWriterSemantics(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	fixtures := []struct {
		name     string
		topUp    TopUp
		expected float64
	}{
		{
			name:     "stripe platform amount",
			topUp:    TopUp{UserId: 101, TradeNo: "stripe-canonical", Amount: 100, CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderStripe},
			expected: 100,
		},
		{
			name:     "epay platform amount",
			topUp:    TopUp{UserId: 102, TradeNo: "epay-canonical", Amount: 100, CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100, Status: common.TopUpStatusSuccess, PaymentMethod: "alipay", PaymentProvider: PaymentProviderEpay},
			expected: 100,
		},
		{
			name:     "waffo platform amount",
			topUp:    TopUp{UserId: 103, TradeNo: "waffo-canonical", Amount: 100, CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderWaffo},
			expected: 100,
		},
		{
			name:     "waffo pancake platform amount",
			topUp:    TopUp{UserId: 104, TradeNo: "waffo-pancake-canonical", Amount: 100, CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderWaffoPancake},
			expected: 100,
		},
		{
			name:     "creem quota amount",
			topUp:    TopUp{UserId: 105, TradeNo: "creem-canonical", Amount: int64(common.QuotaPerUnit) * 100, CreditedQuota: int64(common.QuotaPerUnit) * 100, Money: 100, Status: common.TopUpStatusSuccess, PaymentProvider: PaymentProviderCreem},
			expected: 100,
		},
	}

	for _, fixture := range fixtures {
		t.Run(fixture.name, func(t *testing.T) {
			require.NoError(t, db.Create(&fixture.topUp).Error)
			aggregate, err := getFreshPaidTopUpAggregate(fixture.topUp.UserId)
			require.NoError(t, err)
			assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
			assert.InDelta(t, fixture.expected, aggregate.PaidAmount, 0.0001)
		})
	}
}

func TestFreshPaidTopUpAggregateUsesLegacyWriterFallbackAndCreateTimeAnchor(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	createdAt := time.Now().Add(-time.Hour).Unix()
	topUp := TopUp{
		UserId:          111,
		TradeNo:         "legacy-stripe-created-anchor",
		Amount:          100,
		Money:           0.01,
		Status:          common.TopUpStatusSuccess,
		PaymentMethod:   PaymentMethodStripe,
		PaymentProvider: PaymentProviderStripe,
		CreateTime:      createdAt,
		CompleteTime:    0,
	}
	require.NoError(t, db.Create(&topUp).Error)

	aggregate, err := getFreshPaidTopUpAggregate(topUp.UserId)
	require.NoError(t, err)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
	assert.Equal(t, createdAt, aggregate.LastPaidCompleteAt)
	assert.InDelta(t, 100, aggregate.PaidAmount, 0.0001)
}

func TestFreshPaidTopUpAggregateUsesCreditedQuotaInsteadOfSettledMoney(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	previousQuotaPerUnit := common.QuotaPerUnit
	common.QuotaPerUnit = 999_999
	t.Cleanup(func() { common.QuotaPerUnit = previousQuotaPerUnit })
	topUp := TopUp{
		UserId:              119,
		TradeNo:             "settled-money-source",
		Amount:              1,
		CreditedQuota:       123_456_789,
		Money:               999,
		SettledAmountMicros: 12_340_000,
		Status:              common.TopUpStatusSuccess,
		PaymentProvider:     PaymentProviderStripe,
	}
	require.NoError(t, db.Create(&topUp).Error)

	aggregate, err := getFreshPaidTopUpAggregate(topUp.UserId)
	require.NoError(t, err)
	assert.True(t, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))
	assert.InDelta(t, 123.456912, aggregate.PaidAmount, 0.000001)
}

func TestLinuxDOCreditDoesNotCountAsPaidTopUp(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	const ldcOnlyUserID = 122
	const mixedUserID = 123
	fixtures := []TopUp{
		{
			UserId:          ldcOnlyUserID,
			TradeNo:         "linuxdo-credit-only",
			Amount:          500,
			CreditedQuota:   int64(common.QuotaPerUnit) * 500,
			Money:           500,
			Status:          common.TopUpStatusSuccess,
			PaymentProvider: PaymentProviderEpay,
			PaymentMethod:   "epay",
		},
		{
			UserId:          mixedUserID,
			TradeNo:         "linuxdo-credit-mixed",
			Amount:          500,
			CreditedQuota:   int64(common.QuotaPerUnit) * 500,
			Money:           500,
			Status:          common.TopUpStatusSuccess,
			PaymentProvider: PaymentProviderEpay,
			PaymentMethod:   "epay",
		},
		{
			UserId:          mixedUserID,
			TradeNo:         "real-money-mixed",
			Amount:          10,
			CreditedQuota:   int64(common.QuotaPerUnit) * 10,
			Money:           10,
			Status:          common.TopUpStatusSuccess,
			PaymentProvider: PaymentProviderEpay,
			PaymentMethod:   "alipay",
		},
	}
	require.NoError(t, db.Create(&fixtures).Error)

	granted, err := HasSuccessfulPaidTopUp(ldcOnlyUserID)
	require.NoError(t, err)
	assert.False(t, granted)
	ldcAggregate, err := getFreshPaidTopUpAggregate(ldcOnlyUserID)
	require.NoError(t, err)
	assert.Zero(t, ldcAggregate.PaidAmount)
	assert.False(t, ldcAggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()))

	granted, err = HasSuccessfulPaidTopUp(mixedUserID)
	require.NoError(t, err)
	assert.True(t, granted)
	mixedAggregate, err := getFreshPaidTopUpAggregate(mixedUserID)
	require.NoError(t, err)
	assert.InDelta(t, 10, mixedAggregate.PaidAmount, 0.0001)
}

func TestDeveloperAccessStateSeparatesPaidFactFromEffectiveGrant(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	paidUserID := 120
	require.NoError(t, db.Create(&TopUp{
		UserId:              paidUserID,
		TradeNo:             "paid-access-state",
		Amount:              1,
		CreditedQuota:       1,
		SettledAmountMicros: 10_000,
		Status:              common.TopUpStatusSuccess,
		PaymentProvider:     PaymentProviderStripe,
	}).Error)

	levelZero := 0
	state, err := GetDeveloperAccessStateForUserBase(&UserBase{Id: paidUserID, Role: common.RoleCommonUser, TrustLevelOverride: &levelZero})
	require.NoError(t, err)
	assert.False(t, state.Granted)
	assert.False(t, state.PaidActivationComplete)

	levelOne := 1
	state, err = GetDeveloperAccessStateForUserBase(&UserBase{Id: paidUserID + 1, Role: common.RoleCommonUser, TrustLevelOverride: &levelOne})
	require.NoError(t, err)
	assert.True(t, state.Granted)
	assert.False(t, state.PaidActivationComplete)

	invalidLevel := 99
	state, err = GetDeveloperAccessStateForUserBase(&UserBase{Id: paidUserID, Role: common.RoleCommonUser, TrustLevelOverride: &invalidLevel})
	require.NoError(t, err)
	assert.False(t, state.Granted)
	assert.False(t, state.PaidActivationComplete)

	state, err = GetDeveloperAccessStateForUserBase(&UserBase{Role: common.RoleAdminUser})
	require.NoError(t, err)
	assert.True(t, state.Granted)

	previousDB := DB
	DB = nil
	state, err = GetDeveloperAccessStateForUserBase(&UserBase{Id: paidUserID + 2, Role: common.RoleCommonUser, TrustLevelOverride: &levelOne})
	DB = previousDB
	require.NoError(t, err)
	assert.True(t, state.Granted)
}

func TestSuccessfulExternalPaymentUnlocksLevelOneFromCreditedQuota(t *testing.T) {
	db := setupTopUpAccessTestDB(t)
	topUp := TopUp{
		UserId:          121,
		TradeNo:         "small-real-payment",
		Amount:          1,
		CreditedQuota:   int64(common.QuotaPerUnit),
		Money:           0.01,
		Status:          common.TopUpStatusSuccess,
		PaymentProvider: PaymentProviderStripe,
	}
	require.NoError(t, db.Create(&topUp).Error)

	aggregate, err := getFreshPaidTopUpAggregate(topUp.UserId)
	require.NoError(t, err)
	info := EvaluateTrustLevelWithActivation(common.RoleCommonUser, nil, aggregate.PaidAmount, aggregate.paidActivationComplete(CurrentDeveloperAccessPolicy()), time.Now().Unix(), time.Now().Unix())
	assert.Equal(t, TrustLevelMinUser+1, info.Level)
}
