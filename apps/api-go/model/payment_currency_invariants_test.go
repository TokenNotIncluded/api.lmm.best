package model

import (
	"encoding/json"
	"fmt"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func fixedPaymentCurrencyFixture(t *testing.T, postgres bool) *gorm.DB {
	t.Helper()
	models := []any{&User{}, &TopUp{}, &SubscriptionPlan{}, &SubscriptionOrder{}, &UserSubscription{}, &FinanceLedgerEntry{}, &SubscriptionPaymentEvent{}, &Log{}, &DiscountCode{}, &DiscountCodeReservation{}}
	var db *gorm.DB
	if postgres {
		db = openIsolatedPostgresCacheTestDB(t, models...)
		usePostgresDatabaseType(t)
	} else {
		db = setupExternalTopUpSettlementDB(t, 1)
		require.NoError(t, db.AutoMigrate(models...))
	}
	previousDB, previousLog, previousRedis := DB, LOG_DB, common.RedisEnabled
	DB, LOG_DB, common.RedisEnabled = db, db, false
	previousAnchor, anchorErr := common.CreditsPerUSD()
	previousQ, previousFX, previousBonus := common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY
	previousDisplay := operation_setting.GetGeneralSetting().QuotaDisplayType
	require.NoError(t, common.SetCreditsPerUSD(decimal.NewFromInt(3500000)))
	common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 500000, 7, 1
	t.Cleanup(func() {
		DB, LOG_DB, common.RedisEnabled = previousDB, previousLog, previousRedis
		common.QuotaPerUnit, operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = previousQ, previousFX, previousBonus
		operation_setting.GetGeneralSetting().QuotaDisplayType = previousDisplay
		if anchorErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previousAnchor))
		}
	})
	return db
}

func TestPaymentCreditInvariantsSQLite(t *testing.T)   { runPaymentCreditInvariants(t, false) }
func TestPaymentCreditInvariantsPostgres(t *testing.T) { runPaymentCreditInvariants(t, true) }

func runPaymentCreditInvariants(t *testing.T, postgres bool) {
	db := fixedPaymentCurrencyFixture(t, postgres)
	providers := []struct {
		provider, method, currency string
		cash                       int64
	}{
		{PaymentProviderStripe, PaymentMethodStripe, "USD", 900000},
		{PaymentProviderCreem, PaymentMethodCreem, "USD", 900000},
		{PaymentProviderWaffo, PaymentMethodWaffo, "USD", 900000},
		{PaymentProviderWaffoPancake, PaymentMethodWaffoPancake, "CNY", 6300000},
		{PaymentProviderEpay, "alipay", "CNY", 6300000},
	}
	for _, p := range providers {
		t.Run(p.provider, func(t *testing.T) {
			user := User{Username: "fixed-credit-" + p.provider, AffCode: "fixed-aff-" + p.provider, Quota: 400000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			order := TopUp{UserId: user.Id, TradeNo: "fixed-order-" + p.provider, Amount: 7, PlatformAmountMicros: 7000000, CreditedQuota: 3500000,
				ExpectedAmountMicros: p.cash, SettlementCurrency: p.currency, Money: float64(p.cash) / 1000000,
				PaymentProvider: p.provider, PaymentMethod: p.method, Status: common.TopUpStatusPending}
			require.NoError(t, db.Create(&order).Error)
			// Historical pending snapshots and the existing balance survive every
			// display/rate/old recharge-setting change before the webhook arrives.
			operation_setting.USDExchangeRate = 8
			operation_setting.TopUpPlatformUnitsPerCNY = 99
			operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
			var pending TopUp
			require.NoError(t, db.First(&pending, order.Id).Error)
			require.EqualValues(t, 3500000, pending.CreditedQuota)
			require.EqualValues(t, p.cash, pending.ExpectedAmountMicros)
			settlement := ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: p.provider, PaymentMethod: p.method, SettlementCurrency: p.currency, SettledAmountMicros: p.cash, ProviderEventId: "event-" + p.provider, ProviderTransactionId: "txn-" + p.provider}
			wrong := settlement
			wrong.SettledAmountMicros++
			_, err := completeExternalTopUpOnDB(db, wrong)
			require.Error(t, err)
			_, err = completeExternalTopUpOnDB(db, settlement)
			require.NoError(t, err)
			_, err = completeExternalTopUpOnDB(db, settlement)
			require.NoError(t, err)
			require.Equal(t, 3900000, getUserQuotaForRefundTest(t, db, user.Id))
			// A failed reversal writes neither refund counters nor a finance receipt;
			// retrying that event later uses exactly the original credit/cash ratio.
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 100).Error)
			refund := func(event string, cash int64) (PaymentRefundResult, error) {
				return ApplyPaymentRefund(order.TradeNo, false, cash, p.currency, event, p.method, p.provider, "fixture", user.Id)
			}
			_, err = refund("refund-half-"+p.provider, p.cash/2)
			require.ErrorIs(t, err, ErrRefundWalletQuotaInsufficient)
			require.NoError(t, db.First(&pending, order.Id).Error)
			require.Zero(t, pending.RefundedQuota)
			require.Zero(t, pending.RefundedAmountMicros)
			var count int64
			require.NoError(t, db.Model(&FinanceLedgerEntry{}).Where("source_type = ?", FinanceSourceRefund).Count(&count).Error)
			// Earlier providers already have refund receipts; this provider's event
			// must remain absent until its successful retry.
			require.NoError(t, db.Model(&FinanceLedgerEntry{}).Where("idempotency_key = ?", p.provider+":refund:refund-half-"+p.provider).Count(&count).Error)
			require.Zero(t, count)
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", 3900000).Error)
			result, err := refund("refund-half-"+p.provider, p.cash/2)
			require.NoError(t, err)
			require.True(t, result.Created)
			require.EqualValues(t, 1750000, result.QuotaDebited)
			result, err = refund("refund-half-"+p.provider, p.cash/2)
			require.NoError(t, err)
			require.False(t, result.Created)
			require.Equal(t, 2150000, getUserQuotaForRefundTest(t, db, user.Id))
			result, err = refund("refund-rest-"+p.provider, p.cash/2)
			require.NoError(t, err)
			require.EqualValues(t, 1750000, result.QuotaDebited)
			require.Equal(t, 400000, getUserQuotaForRefundTest(t, db, user.Id))
			require.NoError(t, db.First(&pending, order.Id).Error)
			require.EqualValues(t, 3500000, pending.CreditedQuota)
			require.EqualValues(t, 3500000, pending.RefundedQuota)
			require.EqualValues(t, p.cash, pending.RefundedAmountMicros)
		})
	}
}

func TestSubscriptionWalletFixedUSDAndDynamicCNYSQLite(t *testing.T) {
	runSubscriptionWalletCreditInvariants(t, false)
}
func TestSubscriptionWalletFixedUSDAndDynamicCNYPostgres(t *testing.T) {
	runSubscriptionWalletCreditInvariants(t, true)
}

func runSubscriptionWalletCreditInvariants(t *testing.T, postgres bool) {
	db := fixedPaymentCurrencyFixture(t, postgres)
	for i, tc := range []struct {
		currency            string
		price, fx, oldBonus float64
		debit               int64
	}{
		{"USD", 1, 7, 1, 3500000}, {"USD", 1, 8, 99, 3500000}, {"CNY", 7, 7, 1, 3500000}, {"CNY", 7, 8, 99, 3062500},
		{"CNY", 1, 7, 99, 500000}, {"USD", 0.000001, 7, 99, 4},
	} {
		t.Run(fmt.Sprintf("%s-%d", tc.currency, i), func(t *testing.T) {
			operation_setting.USDExchangeRate = tc.fx
			operation_setting.TopUpPlatformUnitsPerCNY = tc.oldBonus
			plan := SubscriptionPlan{Title: fmt.Sprintf("currency-wallet-%d", i), PriceAmount: tc.price, Currency: tc.currency, PriceCurrencyVersion: 1, DurationUnit: SubscriptionDurationDay, DurationValue: 1, Enabled: true, TotalAmount: 100}
			require.NoError(t, db.Create(&plan).Error)
			user := User{Username: fmt.Sprintf("currency-wallet-owner-%d", i), AffCode: fmt.Sprintf("currency-wallet-aff-%d", i), Quota: 5000000, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			required, err := SubscriptionBalanceQuota(&plan)
			require.NoError(t, err)
			require.Equal(t, tc.debit, required)
			require.NoError(t, PurchaseSubscriptionWithBalance(user.Id, plan.Id))
			require.EqualValues(t, 5000000-tc.debit, getUserQuotaForRefundTest(t, db, user.Id))
			var order SubscriptionOrder
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&order).Error)
			require.Equal(t, tc.currency, order.PlanCurrency)
			require.Equal(t, tc.debit, order.ChargedQuota)
			require.Positive(t, order.UserSubscriptionId)
			var snapshot SubscriptionPlan
			require.NoError(t, json.Unmarshal([]byte(order.PlanSnapshot), &snapshot))
			require.Equal(t, tc.currency, snapshot.Currency)
			require.Equal(t, tc.price, snapshot.PriceAmount)
			operation_setting.USDExchangeRate = 100
			operation_setting.TopUpPlatformUnitsPerCNY = 100
			require.NoError(t, db.Model(&SubscriptionPlan{}).Where("id = ?", plan.Id).Update("price_amount", 500).Error)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.Equal(t, tc.debit, order.ChargedQuota)
			require.Equal(t, tc.price, order.Money)
		})
	}
}

func TestPaymentSnapshotReplayAcrossWorkersPostgres(t *testing.T) {
	db := fixedPaymentCurrencyFixture(t, true)
	user := User{Username: "credit-replay", AffCode: "credit-replay-aff", Quota: 10, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	order := TopUp{UserId: user.Id, TradeNo: "credit-replay-order", Amount: 7, CreditedQuota: 3500000, ExpectedAmountMicros: 1000000, SettlementCurrency: "USD", Money: 1, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusPending}
	require.NoError(t, db.Create(&order).Error)
	settlement := ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", SettledAmountMicros: 1000000, ProviderEventId: "credit-replay-event", ProviderTransactionId: "credit-replay-txn"}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); _, err := completeExternalTopUpOnDB(db, settlement); errs <- err }()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 3500010, getUserQuotaForRefundTest(t, db, user.Id))
}

func installFixedPaymentAnchor(t *testing.T, value string) {
	t.Helper()
	previous, err := common.CreditsPerUSD()
	require.NoError(t, common.SetCreditsPerUSD(decimal.RequireFromString(value)))
	t.Cleanup(func() {
		if err != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, common.SetCreditsPerUSD(previous))
		}
	})
}

func TestPaymentCouponSnapshotSQLite(t *testing.T)   { runPaymentCouponSnapshot(t, false) }
func TestPaymentCouponSnapshotPostgres(t *testing.T) { runPaymentCouponSnapshot(t, true) }
func runPaymentCouponSnapshot(t *testing.T, postgres bool) {
	db := fixedPaymentCurrencyFixture(t, postgres)
	user := User{Username: "coupon-unit-owner", AffCode: "coupon-unit-aff", Quota: 5, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	code := DiscountCode{Code: "UNIT10", DiscountPercent: 10, MinAmount: 3500000, Status: DiscountCodeStatusEnabled, MaxUses: 1}
	require.NoError(t, db.Create(&code).Error)
	operation_setting.GetGeneralSetting().QuotaDisplayType = "TOKENS"
	order := TopUp{UserId: user.Id, TradeNo: "coupon-unit-order", Amount: 7, PlatformAmountMicros: 7000000, CreditedQuota: 3500000, ExpectedAmountMicros: 900000, SettlementCurrency: "USD", Money: 0.9, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, DiscountCodeId: code.Id, DiscountPercent: 10, DiscountQualifyingAmount: "3500000", DiscountQualifyingUnit: "CREDIT", Status: common.TopUpStatusPending}
	require.NoError(t, order.Insert())
	var reservation DiscountCodeReservation
	require.NoError(t, db.Where("top_up_trade_no = ?", order.TradeNo).First(&reservation).Error)
	require.Equal(t, DiscountCodeReservationStatusReserved, reservation.Status)
	operation_setting.GetGeneralSetting().QuotaDisplayType = "USD"
	operation_setting.USDExchangeRate = 99
	operation_setting.TopUpPlatformUnitsPerCNY = 99
	require.NoError(t, db.Model(&DiscountCode{}).Where("id = ?", code.Id).Update("min_amount", 7000000).Error)
	settlement := ExternalTopUpSettlement{TradeNo: order.TradeNo, PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", SettledAmountMicros: 900000, ProviderEventId: "coupon-unit-event", ProviderTransactionId: "coupon-unit-txn"}
	_, err := completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	_, err = completeExternalTopUpOnDB(db, settlement)
	require.NoError(t, err)
	require.Equal(t, 3500005, getUserQuotaForRefundTest(t, db, user.Id))
	require.NoError(t, db.First(&code, code.Id).Error)
	require.EqualValues(t, 1, code.UsedCount)
	require.NoError(t, db.First(&reservation, reservation.Id).Error)
	require.Equal(t, DiscountCodeReservationStatusConsumed, reservation.Status)
	var refreshed TopUp
	require.NoError(t, db.First(&refreshed, order.Id).Error)
	require.Equal(t, "3500000", refreshed.DiscountQualifyingAmount)
	require.Equal(t, "CREDIT", refreshed.DiscountQualifyingUnit)
	require.EqualValues(t, 3500000, refreshed.CreditedQuota)
	require.EqualValues(t, 900000, refreshed.ExpectedAmountMicros)
}
