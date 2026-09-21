package controller

import (
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/stretchr/testify/require"
)

func TestSubscriptionFinanceContractGroupsFiatAndExcludesPlatformOrUnknownAmounts(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.SubscriptionOrder{},
		&model.SubscriptionPaymentEvent{},
		&model.Log{},
		&model.FinanceLedgerEntry{},
		&model.FinancePaymentMethod{},
	))
	now := time.Now().Unix()
	user := model.User{Username: "subscription-finance-contract", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "subscription-finance-contract"}
	require.NoError(t, db.Create(&user).Error)

	require.NoError(t, db.Create(&[]model.TopUp{
		{UserId: user.Id, TradeNo: "contract-topup-usd", Money: 200, SettledAmountMicros: 2_000_000, SettlementCurrency: "usd", Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 50},
		{UserId: user.Id, TradeNo: "contract-topup-cny", Money: 200, SettledAmountMicros: 14_000_000, SettlementCurrency: "CNY", Status: common.TopUpStatusSuccess, PaymentMethod: "epay", PaymentProvider: model.PaymentProviderEpay, CompleteTime: now - 49},
	}).Error)
	require.NoError(t, db.Create(&[]model.SubscriptionOrder{
		{UserId: user.Id, PlanId: 1, Money: 300, TradeNo: "contract-subscription-usd", PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 3_000_000, SettlementCurrency: "USD", CreateTime: now - 48, CompleteTime: now - 47},
		{UserId: user.Id, PlanId: 1, Money: 300, TradeNo: "contract-subscription-cny", PaymentMethod: "epay", PaymentProvider: model.PaymentProviderEpay, Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 21_000_000, SettlementCurrency: "cny", CreateTime: now - 46, CompleteTime: now - 45},
		{UserId: user.Id, PlanId: 1, Money: 99, TradeNo: "contract-subscription-legacy", PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 9_000_000, SettlementCurrency: "", CreateTime: now - 44, CompleteTime: now - 43},
		{UserId: user.Id, PlanId: 1, Money: 10, TradeNo: "contract-subscription-wallet", PaymentMethod: model.PaymentMethodBalance, PaymentProvider: model.PaymentProviderBalance, Status: common.TopUpStatusSuccess, CreateTime: now - 42, CompleteTime: now - 41},
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, model.FinanceCurrencyUSD, view.Currency)
	require.Equal(t, int64(5_000_000), view.RevenueMicros, "the USD overview must not add CNY, platform balance, or unknown-currency amounts")
	require.Equal(t, []financeCurrencyMetric{
		{Currency: "CNY", AmountMicros: 35_000_000, Orders: 2},
		{Currency: "USD", AmountMicros: 5_000_000, Orders: 2},
	}, view.SettlementRevenueByCurrency)
	require.Equal(t, int64(2), view.UnclassifiedSettlementOrders, "wallet platform amounts and legacy rows without ISO currency must be marked, not guessed as USD")
}

func TestSubscriptionFinanceContractUsesPaymentEventsWithoutDoubleCountingOrder(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&model.User{},
		&model.TopUp{},
		&model.SubscriptionOrder{},
		&model.SubscriptionPaymentEvent{},
		&model.Log{},
		&model.FinanceLedgerEntry{},
		&model.FinancePaymentMethod{},
	))
	now := time.Now().Unix()
	user := model.User{Username: "subscription-finance-events", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "subscription-finance-events"}
	require.NoError(t, db.Create(&user).Error)
	order := model.SubscriptionOrder{
		UserId: user.Id, PlanId: 1, Money: 400, TradeNo: "contract-recurring-usd",
		PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe,
		Status: common.TopUpStatusSuccess, ExpectedAmountMicros: 4_000_000, SettlementCurrency: "USD",
		CreateTime: now - 80, CompleteTime: now - 70,
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Create(&[]model.SubscriptionPaymentEvent{
		{SubscriptionOrderId: order.Id, PaymentProvider: model.PaymentProviderStripe, ProviderEventId: "contract-event-initial", ProviderTransactionId: "contract-transaction-initial", SettlementCurrency: "USD", SettlementAmountMicros: 4_000_000, PeriodStart: now - 70, PeriodEnd: now + 1000, CreatedTime: now - 60},
		{SubscriptionOrderId: order.Id, PaymentProvider: model.PaymentProviderStripe, ProviderEventId: "contract-event-renewal", ProviderTransactionId: "contract-transaction-renewal", SettlementCurrency: "usd", SettlementAmountMicros: 4_000_000, PeriodStart: now - 10, PeriodEnd: now + 2000, CreatedTime: now - 5},
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(8_000_000), view.RevenueMicros)
	require.Equal(t, []financeCurrencyMetric{{Currency: "USD", AmountMicros: 8_000_000, Orders: 2}}, view.SettlementRevenueByCurrency)
	require.Len(t, view.RevenueByMethod, 1)
	require.Equal(t, int64(2), view.RevenueByMethod[0].Orders, "payment events replace the order amount as settlement evidence")
}
