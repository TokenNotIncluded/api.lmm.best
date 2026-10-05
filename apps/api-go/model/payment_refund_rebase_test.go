package model

import (
	"fmt"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestPaymentRefundRebase(t *testing.T) {
	for _, snapshot := range []bool{true, false} {
		t.Run(fmt.Sprint(snapshot), func(t *testing.T) {
			db := setupConsoleActivationTestDB(t)
			require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}, &User{}, &TopUp{}, &FinanceLedgerEntry{}, &WalletTopUpCreditRebase{}))
			user := User{Username: "refund-rebase", Password: "password", Quota: 10, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			order := TopUp{UserId: user.Id, TradeNo: "rebased-partial", PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, Status: common.TopUpStatusSuccess, CreditedQuota: 100, SettledAmountMicros: 100, RefundedAmountMicros: 20, RefundedQuota: 20}
			require.NoError(t, db.Create(&order).Error)
			base := WalletTopUpCreditRebase{TopUpID: order.Id, UserID: user.Id, MigrationID: "test", OriginalCreditedQuota: 100, OriginalRefundedQuota: 20, OriginalRefundedAmountMicros: 20, OriginalPaidAmountMicros: 100, RefundableQuota: 12}
			if snapshot {
				require.NoError(t, db.Create(&base).Error)
			}
			refund := func(amount int64, event string) (PaymentRefundResult, error) {
				return ApplyPaymentRefund(order.TradeNo, false, amount, FinanceCurrencyUSD, event, PaymentMethodWaffoPancake, PaymentProviderWaffoPancake, "test", user.Id)
			}
			for i := 0; i < 5; i++ {
				result, err := refund(1, fmt.Sprint(i))
				require.NoError(t, err)
				expected := int64(1)
				if snapshot {
					expected = 0
					if i == 3 {
						expected = 1
					}
				}
				require.Equal(t, expected, result.QuotaDebited)
			}
			replay, err := refund(1, "3")
			require.NoError(t, err)
			require.False(t, replay.Created)
			require.Zero(t, replay.QuotaDebited)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.EqualValues(t, 25, order.RefundedQuota)
			require.EqualValues(t, 25, order.RefundedAmountMicros)
			_, err = refund(75, "full")
			require.ErrorIs(t, err, ErrRefundWalletQuotaInsufficient)
			if snapshot {
				require.NoError(t, db.First(&base, "top_up_id = ?", order.Id).Error)
				require.EqualValues(t, 1, base.RebasedDebitedQuota)
			}
			required := 11
			if !snapshot {
				required = 75
			}
			require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).Update("quota", required).Error)
			result, err := refund(75, "full")
			require.NoError(t, err)
			require.EqualValues(t, required, result.QuotaDebited)
			require.NoError(t, db.First(&order, order.Id).Error)
			require.EqualValues(t, 100, order.RefundedQuota)
			require.Zero(t, getUserQuotaForRefundTest(t, db, user.Id))
		})
	}
}

func TestPaymentRefundRebaseRejectsCorruptBaseline(t *testing.T) {
	db := setupConsoleActivationTestDB(t)
	require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}, &User{}, &TopUp{}, &FinanceLedgerEntry{}, &WalletTopUpCreditRebase{}))
	user := User{Username: "corrupt-rebase", Password: "password", Quota: 100, Status: common.UserStatusEnabled}
	require.NoError(t, db.Create(&user).Error)
	order := TopUp{UserId: user.Id, TradeNo: "corrupt", PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, Status: common.TopUpStatusSuccess, CreditedQuota: 100, SettledAmountMicros: 100}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Create(&WalletTopUpCreditRebase{TopUpID: order.Id, UserID: user.Id, MigrationID: "test", OriginalCreditedQuota: 100, OriginalPaidAmountMicros: 100, RefundableQuota: 15, RebasedDebitedQuota: 1}).Error)
	_, err := ApplyPaymentRefund(order.TradeNo, false, 10, FinanceCurrencyUSD, "corrupt", PaymentMethodWaffoPancake, PaymentProviderWaffoPancake, "test", user.Id)
	require.ErrorIs(t, err, ErrRefundAmountInvalid)
	require.Equal(t, 100, getUserQuotaForRefundTest(t, db, user.Id))
	var count int64
	require.NoError(t, db.Model(&FinanceLedgerEntry{}).Count(&count).Error)
	require.Zero(t, count)
}

func TestRebasedRefundTargetExactHalfCreditBoundary(t *testing.T) {
	// A 16-digit decimal division can round this fraction to exactly 0.5.
	require.EqualValues(t, 0, rebasedRefundTarget(1, 9_000_000_000_000_000, 4_499_999_999_999_999))
	require.EqualValues(t, 1, rebasedRefundTarget(1, 9_000_000_000_000_000, 4_500_000_000_000_000))
	require.EqualValues(t, 9_000_000_000_000_000, rebasedRefundTarget(9_000_000_000_000_000, 9_000_000_000_000_000, 9_000_000_000_000_000))
}

func TestPaymentRefundRebaseMissingAuditFailsClosed(t *testing.T) {
	for _, test := range []struct {
		name       string
		childTable bool
		plan       string
		wantError  bool
	}{
		{"missing_table", false, `{"user_ids":[1],"refund_bases":[{"top_up_id":1}]}`, true},
		{"missing_row", true, `{"user_ids":[1],"refund_bases":[{"top_up_id":1}]}`, true},
		{"new_payment", true, `{"user_ids":[1],"refund_bases":[{"top_up_id":2}]}`, false},
		{"invalid_plan", false, `broken`, true},
		{"missing_order_inventory", false, `{"user_ids":[1]}`, true},
		{"historical_noncash", true, `{"user_ids":[1],"refund_bases":[],"noncash_topups":[{"id":1}]}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			db := setupConsoleActivationTestDB(t)
			require.NoError(t, db.AutoMigrate(&SubscriptionOrder{}, &User{}, &TopUp{}, &FinanceLedgerEntry{}))
			if test.childTable {
				require.NoError(t, db.AutoMigrate(&WalletTopUpCreditRebase{}))
			}
			require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY, plan TEXT NOT NULL)").Error)
			require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases (migration_id, plan) VALUES (?, ?)", "test", test.plan).Error)
			user := User{Id: 1, Username: "missing-basis", Password: "password", Quota: 100, Status: common.UserStatusEnabled}
			require.NoError(t, db.Create(&user).Error)
			order := TopUp{Id: 1, UserId: user.Id, TradeNo: "missing", PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, Status: common.TopUpStatusSuccess, CreditedQuota: 100, SettledAmountMicros: 100}
			require.NoError(t, db.Create(&order).Error)
			result, err := ApplyPaymentRefund(order.TradeNo, false, 10, FinanceCurrencyUSD, "missing", PaymentMethodWaffoPancake, PaymentProviderWaffoPancake, "test", user.Id)
			if test.wantError {
				require.ErrorIs(t, err, ErrRefundAmountInvalid)
				require.Equal(t, 100, getUserQuotaForRefundTest(t, db, user.Id))
				require.NoError(t, db.First(&order, order.Id).Error)
				require.Zero(t, order.RefundedQuota)
				var count int64
				require.NoError(t, db.Model(&FinanceLedgerEntry{}).Count(&count).Error)
				require.Zero(t, count)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 10, result.QuotaDebited)
			}
		})
	}
}

func TestExportWalletTopUpCreditRebaseFactsKeepsSourceAuthority(t *testing.T) {
	facts := ExportWalletTopUpCreditRebaseFacts(&TopUp{PaymentProvider: PaymentProviderEpay, PaymentMethod: PaymentProviderEpay, CreditedQuota: 5_000_000, Money: 10})
	require.True(t, facts.IsLegacyLinuxDOCreditTopUp)
	require.Zero(t, facts.EffectiveCreditedQuota)
	cash := ExportWalletTopUpCreditRebaseFacts(&TopUp{PaymentProvider: PaymentProviderEpay, PaymentMethod: PaymentProviderEpay, CreditedQuota: 5_000_000, ExpectedAmountMicros: 10_000_000, SettlementCurrency: "CNY"})
	require.False(t, cash.IsLegacyLinuxDOCreditTopUp)
	require.EqualValues(t, 5_000_000, cash.EffectiveCreditedQuota)
}
