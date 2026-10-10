/* Copyright (C) 2026 LIghtJUNction. AGPL-3.0-or-later. */
package model

import (
	"context"
	"encoding/json"
	"fmt"
	"math/big"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func adminStatisticsFixture(t *testing.T) *gorm.DB {
	t.Helper()
	db := marketTestDB(t)
	require.NoError(t, db.AutoMigrate(&TopUp{}, &SubscriptionOrder{}, &FinanceLedgerEntry{}))
	return db
}

func TestAdminSiteStatisticsSeparatesDiscountedPaymentsRefundsAndOtherSources(t *testing.T) {
	db := adminStatisticsFixture(t)
	user := marketTestUser(t, db, "statistics-private-user", 4_750_000, common.RoleCommonUser)
	require.NoError(t, db.Model(&user).Updates(map[string]any{"used_quota": 250_000, "email": "private-statistics@example.test"}).Error)
	orders := []TopUp{
		{TradeNo: "discounted-usd", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, CreditedQuota: 5_000_000, SettledAmountMicros: 1_000_000, RefundedAmountMicros: 250_001},
		{TradeNo: "full-refund-usd", PaymentProvider: PaymentProviderCreem, PaymentMethod: PaymentMethodCreem, SettlementCurrency: " usd ", Status: common.TopUpStatusSuccess, SettledAmountMicros: 3_000_000, RefundedAmountMicros: 3_000_000},
		{TradeNo: "cash-cny", PaymentProvider: PaymentProviderEpay, PaymentMethod: "alipay", SettlementCurrency: "CNY", Status: common.TopUpStatusSuccess, SettledAmountMicros: 7_333_333},
		{TradeNo: "missing-settlement", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 999_000_000, Money: 999},
		{TradeNo: "missing-currency", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_000_000},
		{TradeNo: "unknown-provider", PaymentProvider: "unverified", PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_000_000},
		{TradeNo: "bad-refund", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 1_000_000, RefundedAmountMicros: 1_000_001},
		{TradeNo: "gift", PaymentProvider: "admin", PaymentMethod: "gift", SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_000_000},
		{TradeNo: "internal-wallet", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodBalance, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_000_000},
		{TradeNo: "virtual-credit", PaymentProvider: PaymentProviderEpay, PaymentMethod: "ldc", SettlementCurrency: "LDC", Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 5_000_000, CreditedQuota: 2_000, SettledAmountMicros: 5_000_000, RefundedAmountMicros: 1_000_001},
		{TradeNo: "legacy-virtual-credit", PaymentProvider: PaymentProviderEpay, PaymentMethod: "epay", SettlementCurrency: "LDC", Status: common.TopUpStatusSuccess, Money: 999, SettledAmountMicros: 999_000_000},
		{TradeNo: "pending", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusPending, SettledAmountMicros: 999_000_000},
		{TradeNo: "subscription-mirror", PaymentProvider: PaymentProviderWaffoPancake, PaymentMethod: PaymentMethodWaffoPancake, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_000_000},
	}
	for i := range orders {
		orders[i].UserId = user.Id
	}
	require.NoError(t, db.Create(&orders).Error)
	require.NoError(t, db.Create(&SubscriptionOrder{TradeNo: "subscription-mirror", UserId: user.Id, Status: common.TopUpStatusPending}).Error)
	// Other revenue/refund rows are not independent recharge receipts.
	require.NoError(t, db.Create(&FinanceLedgerEntry{EntryType: FinanceEntryRevenue, Direction: FinanceDirectionCredit, SourceType: FinanceSourceManual, AmountMicros: 999_000_000, Currency: "USD", IdempotencyKey: "manual-statistics-income"}).Error)
	view, err := GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	require.Equal(t, "4750000", view.TotalBalanceCredits)
	require.Equal(t, "250000", view.TotalUsedCredits)
	require.EqualValues(t, common.FixedCreditsPerUSD, view.CreditsPerUSD)
	require.EqualValues(t, 4, view.Recharge.ConfirmedOrders)
	require.EqualValues(t, 4, view.Recharge.UnconfirmedOrders)
	require.EqualValues(t, 1, view.Recharge.InvalidOrders)
	require.Equal(t, []AdminRechargeCurrency{
		{Currency: "CNY", GrossAmountMicros: "7333333", RefundedAmountMicros: "0", NetAmountMicros: "7333333", Orders: 1},
		{Currency: "USD", GrossAmountMicros: "4000000", RefundedAmountMicros: "3250001", NetAmountMicros: "749999", Orders: 2},
	}, view.Recharge.Currencies)
	require.Equal(t, []AdminRechargeCurrency{{Currency: "LDC", GrossAmountMicros: "5000000", RefundedAmountMicros: "1000001", NetAmountMicros: "3999999", Orders: 1}}, view.Recharge.VirtualUnits)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	for _, private := range []string{user.Username, "private-statistics@example.test", "user_id", "trade_no", "discounted-usd"} {
		require.NotContains(t, string(encoded), private)
	}
	var unchanged TopUp
	require.NoError(t, db.First(&unchanged, orders[0].Id).Error)
	require.EqualValues(t, 5_000_000, unchanged.CreditedQuota)
	require.EqualValues(t, 250_001, unchanged.RefundedAmountMicros)
}

func TestAdminSiteStatisticsReusesCanonicalHistoricalUsageProjection(t *testing.T) {
	db := adminStatisticsFixture(t)
	user := marketTestUser(t, db, "statistics-historical", 198_041_882, common.RoleCommonUser)
	require.NoError(t, db.Model(&user).Update("used_quota", 2_491_782_362).Error)
	fresh := marketTestUser(t, db, "statistics-new", 500_000, common.RoleCommonUser)
	require.NoError(t, db.Model(&fresh).Update("used_quota", 500_001).Error)
	plan := usageTestPlan()
	plan["user_ids"] = []int{user.Id}
	plan["user_sources"] = []map[string]any{{"id": user.Id, "used_quota": 2_491_672_787}}
	plan["token_sources"] = []any{}
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY,plan TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?,?)", "usage-test", string(encoded)).Error)
	view, err := GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	// The frozen 6.710363 baseline normalizes to 371317138, then the
	// 109575 post-migration delta and fresh account's 500001 are added.
	require.Equal(t, "371926714", view.TotalUsedCredits)
	require.Equal(t, "198541882", view.TotalBalanceCredits)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, 2_491_782_362, stored.UsedQuota, "read-only statistics must not rewrite raw history")
	require.NoError(t, db.Model(&user).Update("used_quota", 2_491_672_786).Error)
	view, err = GetAdminSiteStatistics(context.Background())
	require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
	require.Nil(t, view, "unproven counter reductions must not produce inflated or zero totals")
	require.NoError(t, db.Exec("UPDATE wallet_credit_rebases SET plan = 'broken'").Error)
	view, err = GetAdminSiteStatistics(context.Background())
	require.ErrorIs(t, err, ErrUsageProjectionUnavailable)
	require.Nil(t, view)
}

func TestAdminSiteStatisticsKeepsExactTotalsBeyondInt64AndRetainedAccounts(t *testing.T) {
	db := adminStatisticsFixture(t)
	const count = 1030
	users, orders := make([]User, count), make([]TopUp, count)
	for i := range count {
		users[i] = User{Username: fmt.Sprintf("statistics-large-%d", i), AffCode: fmt.Sprintf("statistics-large-%d", i), Quota: common.MaxWalletQuota, UsedQuota: common.MaxWalletQuota, Status: common.UserStatusEnabled, Role: common.RoleCommonUser}
		orders[i] = TopUp{TradeNo: fmt.Sprintf("statistics-large-%d", i), PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: int64(common.MaxWalletQuota), RefundedAmountMicros: 1}
	}
	require.NoError(t, db.CreateInBatches(&users, 50).Error)
	require.NoError(t, db.CreateInBatches(&orders, 50).Error)
	require.NoError(t, db.Delete(&users[0]).Error)
	require.NoError(t, db.Model(&users[1]).Update("status", common.UserStatusDisabled).Error)
	view, err := GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	expected := new(big.Int).Mul(big.NewInt(int64(common.MaxWalletQuota)), big.NewInt(count))
	require.False(t, expected.IsInt64(), "fixture must exceed both browser and signed SQL SUM limits")
	require.Equal(t, expected.String(), view.TotalBalanceCredits)
	require.Equal(t, expected.String(), view.TotalUsedCredits)
	require.Len(t, view.Recharge.Currencies, 1)
	require.Equal(t, expected.String(), view.Recharge.Currencies[0].GrossAmountMicros)
	require.Equal(t, "1030", view.Recharge.Currencies[0].RefundedAmountMicros)
	require.Equal(t, new(big.Int).Sub(expected, big.NewInt(count)).String(), view.Recharge.Currencies[0].NetAmountMicros)
}

func TestAdminSiteStatisticsAcceptsLegacyNullProviderOnlyWithRealSettlement(t *testing.T) {
	db := adminStatisticsFixture(t)
	order := TopUp{TradeNo: "legacy-null-source", PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 999_999}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Model(&order).Update("payment_provider", nil).Error)
	view, err := GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	require.EqualValues(t, 1, view.Recharge.ConfirmedOrders)
	require.Equal(t, "999999", view.Recharge.Currencies[0].GrossAmountMicros)
	require.NoError(t, db.Model(&order).Updates(map[string]any{"settled_amount_micros": 0, "expected_amount_micros": 999_999, "money": 0.999999}).Error)
	view, err = GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	require.Empty(t, view.Recharge.Currencies)
	require.EqualValues(t, 1, view.Recharge.UnconfirmedOrders)
}

func TestAdminSiteStatisticsPostgresReadSnapshot(t *testing.T) {
	db := openIsolatedPostgresCacheTestDB(t, &User{}, &TopUp{}, &SubscriptionOrder{})
	usePostgresDatabaseType(t)
	previousDB := DB
	DB = db
	t.Cleanup(func() { DB = previousDB })
	user := User{Username: "postgres-statistics", AffCode: "postgres-statistics", Quota: 500_000, UsedQuota: 2_491_782_362}
	require.NoError(t, db.Create(&user).Error)
	plan := usageTestPlan()
	plan["user_ids"] = []int{user.Id}
	plan["user_sources"] = []map[string]any{{"id": user.Id, "used_quota": 2_491_672_787}}
	plan["token_sources"] = []any{}
	encoded, err := json.Marshal(plan)
	require.NoError(t, err)
	require.NoError(t, db.Exec("CREATE TABLE wallet_credit_rebases (migration_id TEXT PRIMARY KEY,plan TEXT NOT NULL)").Error)
	require.NoError(t, db.Exec("INSERT INTO wallet_credit_rebases VALUES (?,?)", "usage-test", string(encoded)).Error)
	orders := []TopUp{
		{TradeNo: "postgres-real-cash", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, CreditedQuota: 5_000_000, SettledAmountMicros: 999_999, RefundedAmountMicros: 333_333},
		{TradeNo: "postgres-subscription", PaymentProvider: PaymentProviderStripe, PaymentMethod: PaymentMethodStripe, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, SettledAmountMicros: 777_777},
	}
	require.NoError(t, db.Create(&orders).Error)
	require.NoError(t, db.Create(&SubscriptionOrder{TradeNo: "postgres-subscription", UserId: user.Id}).Error)
	view, err := GetAdminSiteStatistics(context.Background())
	require.NoError(t, err)
	require.Equal(t, "500000", view.TotalBalanceCredits)
	require.Equal(t, "371426713", view.TotalUsedCredits)
	require.Equal(t, []AdminRechargeCurrency{{Currency: "USD", GrossAmountMicros: "999999", RefundedAmountMicros: "333333", NetAmountMicros: "666666", Orders: 1}}, view.Recharge.Currencies)
	var stored User
	require.NoError(t, db.First(&stored, user.Id).Error)
	require.Equal(t, user.Quota, stored.Quota)
	require.Equal(t, user.UsedQuota, stored.UsedQuota)
	var order TopUp
	require.NoError(t, db.First(&order, orders[0].Id).Error)
	require.EqualValues(t, 5_000_000, order.CreditedQuota)
	require.EqualValues(t, 333_333, order.RefundedAmountMicros)
}
