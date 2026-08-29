package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestFinanceOverviewAggregatesPaymentMethodsUsersAndTokenCost(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	users := []model.User{{Username: "finance-a", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-a"}, {Username: "finance-b", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-b"}}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Create(&[]model.TopUp{
		{UserId: users[0].Id, TradeNo: "finance-topup-a", Amount: 100, Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: "stripe", PaymentProvider: "stripe", CompleteTime: now - 10},
		{UserId: users[1].Id, TradeNo: "finance-topup-b", Amount: 250, Money: 25, SettledAmountMicros: 25_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: "creem", PaymentProvider: "creem", CompleteTime: now - 20},
	}).Error)
	require.NoError(t, db.Create(&model.Log{UserId: users[0].Id, CreatedAt: now - 5, Type: model.LogTypeConsume, PromptTokens: 1000, CompletionTokens: 500, ModelName: "priced", Other: `{"model_price":0.02}`}).Error)
	_, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{EntryType: model.FinanceEntryExpense, Category: "hosting", AmountMicros: 3_000_000, Currency: "USD", Direction: model.FinanceDirectionDebit, SourceType: model.FinanceSourceManual, Note: "monthly host", OccurredAt: now - 30, CreatedBy: users[0].Id, IdempotencyKey: "hosting-1"})
	require.NoError(t, err)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(35_000_000), view.RevenueMicros)
	require.Equal(t, int64(3_000_000+30), view.ExpenseMicros)
	require.Equal(t, int64(31_999_970), view.ProfitMicros)
	require.Equal(t, int64(1500), view.Tokens.TotalTokens)
	require.Equal(t, int64(30), view.Tokens.EstimatedCostMicros)
	require.Len(t, view.RevenueByMethod, 2)
	require.Len(t, view.Users, 2)
	byUsername := make(map[string]financeUserMetric, len(view.Users))
	for _, metric := range view.Users {
		byUsername[metric.Username] = metric
	}
	require.Equal(t, users[0].Id, byUsername["finance-a"].UserID)
	require.Equal(t, users[1].Id, byUsername["finance-b"].UserID)

	stripeView, err := buildFinanceOverview(now-100, now+1, 0, "stripe")
	require.NoError(t, err)
	require.Equal(t, int64(10_000_000), stripeView.RevenueMicros)
}

func TestFinanceOverviewCountsChargedFixedPriceUsageWithoutTokens(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	user := model.User{Username: "finance-fixed-price", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-fixed-price"}
	require.NoError(t, db.Create(&user).Error)
	// Per-call requests such as image/MJ tasks have no token usage, but their
	// consume log records both the charged quota and the fixed model price.
	require.NoError(t, db.Create(&model.Log{
		UserId: user.Id, CreatedAt: now - 5, Type: model.LogTypeConsume,
		Quota: 50_000, ModelName: "mj_imagine", Other: `{"model_price":0.1}`,
	}).Error)
	require.NoError(t, db.Create(&model.Log{
		UserId: user.Id, CreatedAt: now - 4, Type: model.LogTypeConsume,
		Quota: 0, ModelName: "mj_imagine", Other: `{"model_price":0.1}`,
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(2), view.Tokens.Requests)
	require.Equal(t, int64(0), view.Tokens.TotalTokens)
	require.Equal(t, int64(100_000), view.Tokens.EstimatedCostMicros)
	require.Equal(t, int64(100_000), view.ExpenseMicros)
	require.Equal(t, int64(100_000), view.Users[0].TokenCostMicros)
}

func TestFinanceOverviewDoesNotAttributeUsageCostToPaymentMethod(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	users := []model.User{
		{Username: "finance-method-a", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-method-a"},
		{Username: "finance-method-b", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-method-b"},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Create(&[]model.TopUp{
		{UserId: users[0].Id, TradeNo: "finance-method-stripe", Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: "stripe", PaymentProvider: "stripe", CompleteTime: now - 10},
		{UserId: users[1].Id, TradeNo: "finance-method-creem", Money: 20, SettledAmountMicros: 20_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: "creem", PaymentProvider: "creem", CompleteTime: now - 9},
	}).Error)
	require.NoError(t, db.Create(&[]model.Log{
		{UserId: users[0].Id, CreatedAt: now - 8, Type: model.LogTypeConsume, PromptTokens: 100, CompletionTokens: 100, Other: `{"model_price":0.10}`},
		{UserId: users[1].Id, CreatedAt: now - 7, Type: model.LogTypeConsume, PromptTokens: 200, CompletionTokens: 200, Other: `{"model_price":0.10}`},
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "stripe")
	require.NoError(t, err)
	require.Equal(t, "unavailable_for_payment_method", view.CostAttribution)
	require.Equal(t, int64(0), view.Tokens.TotalTokens)
	require.Equal(t, int64(0), view.ExpenseMicros)
	require.Equal(t, int64(10_000_000), view.RevenueMicros)
	require.Equal(t, int64(10_000_000), view.ProfitMicros, "the numeric profit is only the attributable subset; the response flag prevents treating it as platform profit")
	require.Len(t, view.Users, 1)
	require.Equal(t, users[0].Id, view.Users[0].UserID)
}

func TestFinanceOverviewDoesNotDoubleCountSubscriptionTopUpMirror(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	user := model.User{Username: "finance-subscription", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-subscription"}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "finance-subscription-mirror"
	// CompleteSubscriptionOrder keeps a successful TopUp mirror for wallet and
	// order compatibility. Finance must count the SubscriptionOrder only.
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: tradeNo, Money: 10, Status: common.TopUpStatusSuccess,
		PaymentMethod: model.PaymentMethodStripe, CompleteTime: now - 10,
	}).Error)
	require.NoError(t, db.Create(&model.SubscriptionOrder{
		UserId: user.Id, PlanId: 1, Money: 10, TradeNo: tradeNo,
		PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe,
		ExpectedAmountMicros: 10_000_000, SettlementCurrency: "USD",
		Status: common.TopUpStatusSuccess, CreateTime: now - 20, CompleteTime: now - 10,
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(10_000_000), view.RevenueMicros)
	require.Len(t, view.RevenueByMethod, 1)
	require.Equal(t, int64(1), view.RevenueByMethod[0].Orders)
	require.Len(t, view.Users, 1)
	require.Equal(t, int64(10_000_000), view.Users[0].RevenueMicros)
}

func TestFinanceOverviewExcludesInternalCreditsAndSubtractsRefunds(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	user := model.User{Username: "finance-refund", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-refund"}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&[]model.TopUp{
		{UserId: user.Id, TradeNo: "finance-paid", Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 20},
		{UserId: user.Id, TradeNo: "finance-linuxdo", Money: 100, Status: common.TopUpStatusSuccess, PaymentMethod: "epay", PaymentProvider: model.PaymentProviderEpay, CompleteTime: now - 19},
	}).Error)
	_, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{
		EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 2_000_000,
		Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe, UserId: &user.Id, SourceType: model.FinanceSourceRefund,
		SourceId: "refund-finance", OccurredAt: now - 10, CreatedBy: user.Id, IdempotencyKey: "finance-refund-1",
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&[]model.Log{
		{UserId: user.Id, CreatedAt: now - 5, Type: model.LogTypeConsume, PromptTokens: 100, CompletionTokens: 100, Other: `{"model_price":0.01,"billing_source":"wallet"}`},
		{UserId: user.Id, CreatedAt: now - 4, Type: model.LogTypeConsume, PromptTokens: 900, CompletionTokens: 900, Other: `{"model_price":0.01,"billing_source":"linuxdo"}`},
	}).Error)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(10_000_000), view.RevenueMicros)
	require.Equal(t, int64(2_000_000), view.RefundMicros)
	require.Equal(t, int64(8_000_000), view.NetRevenueMicros)
	require.Equal(t, int64(2), view.ExpenseMicros)
	require.Equal(t, int64(7_999_998), view.ProfitMicros)
	require.Equal(t, int64(200), view.Tokens.TotalTokens)
	for _, method := range view.RevenueByMethod {
		require.NotEqual(t, "epay", method.Method)
	}
}

func TestFinanceOverviewSortsRefundUsersAndExcludesNonUSDCurrency(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	users := []model.User{
		{Username: "finance-small-revenue", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-small-revenue"},
		{Username: "finance-refund-only", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-refund-only"},
	}
	require.NoError(t, db.Create(&users).Error)
	require.NoError(t, db.Create(&[]model.TopUp{
		{UserId: users[0].Id, TradeNo: "finance-usd-topup", Money: 1, SettledAmountMicros: 1_000_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 20},
		{UserId: users[0].Id, TradeNo: "finance-eur-topup", Money: 100, SettledAmountMicros: 100_000_000, SettlementCurrency: "EUR", Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 19},
	}).Error)
	_, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{
		EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 2_000_000,
		Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe, UserId: &users[1].Id, SourceType: model.FinanceSourceRefund,
		SourceId: "finance-refund-only", OccurredAt: now - 10, CreatedBy: users[1].Id, IdempotencyKey: "finance-refund-only-1",
	})
	require.NoError(t, err)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(1_000_000), view.RevenueMicros)
	require.Equal(t, int64(2_000_000), view.RefundMicros)
	require.Len(t, view.Users, 2)
	require.Equal(t, users[1].Id, view.Users[0].UserID, "refund activity must participate in bounded user ranking")
	require.Equal(t, int64(2_000_000), view.Users[0].RefundMicros)

	accumulator := newFinanceAccumulator(now-100, now+1, nil)
	for userID := 1; userID <= financeDashboardMaxEntries+1; userID++ {
		accumulator.addRevenue("stripe", "stripe", 1, now-1, userID)
	}
	bounded := accumulator.finish()
	require.True(t, bounded.UserMetricsTruncated)
}

func TestFinancePaymentMethodsDiscoverAllFinancialSources(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.SubscriptionOrder{
		UserId: 1, PlanId: 1, Money: 2, TradeNo: "finance-method-subscription",
		PaymentMethod: "", PaymentProvider: "subscription-provider",
		Status: common.TopUpStatusSuccess, CreateTime: now - 2, CompleteTime: now - 1,
	}).Error)
	_, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{
		EntryType: model.FinanceEntryExpense, Category: "refund", AmountMicros: 1_000_000,
		Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit,
		PaymentMethod: "manual-wire", SourceType: model.FinanceSourceRefund,
		OccurredAt: now - 1, CreatedBy: 1, IdempotencyKey: "finance-method-ledger",
	})
	require.NoError(t, err)

	methods, byMethod, err := loadFinancePaymentMethods()
	require.NoError(t, err)
	require.Contains(t, byMethod, "subscription-provider")
	require.Contains(t, byMethod, "manual-wire")
	methodNames := make([]string, 0, len(methods))
	for _, method := range methods {
		methodNames = append(methodNames, method.Method)
	}
	require.Contains(t, methodNames, "subscription-provider")
	require.Contains(t, methodNames, "manual-wire")
}

func TestFinancePaymentMethodsRejectUnboundedDiscovery(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))

	entries := make([]model.FinanceLedgerEntry, financeDashboardMaxPaymentMethods+1)
	for index := range entries {
		entries[index] = model.FinanceLedgerEntry{
			EntryType:      model.FinanceEntryExpense,
			Category:       "external",
			AmountMicros:   1,
			Currency:       model.FinanceCurrencyUSD,
			Direction:      model.FinanceDirectionDebit,
			PaymentMethod:  fmt.Sprintf("abnormal-method-%03d", index),
			SourceType:     model.FinanceSourceManual,
			OccurredAt:     time.Now().Unix(),
			CreatedBy:      1,
			IdempotencyKey: fmt.Sprintf("finance-method-limit-%03d", index),
		}
	}
	require.NoError(t, db.Create(&entries).Error)

	_, _, err := loadFinancePaymentMethods()
	require.ErrorIs(t, err, errFinancePaymentMethodsLimit)
}

func TestFinanceAccumulatorBoundsUserMetricsWithoutDroppingTotals(t *testing.T) {
	accumulator := newFinanceAccumulator(0, 24*60*60, nil)
	for userID := 1; userID <= financeDashboardMaxUserMetrics+1; userID++ {
		accumulator.addRevenue("stripe", "stripe", 1, 0, userID)
	}

	require.Len(t, accumulator.users, financeDashboardMaxUserMetrics)
	require.Equal(t, int64(financeDashboardMaxUserMetrics+1), accumulator.overview.RevenueMicros)

	view := accumulator.finish()
	require.Equal(t, int64(financeDashboardMaxUserMetrics+1), view.RevenueMicros)
	require.False(t, view.UserMetricsComplete)
	require.Equal(t, financeDashboardMaxUserMetrics, view.UserMetricsLimit)
	require.Len(t, view.Users, financeDashboardMaxEntries)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	var metadata struct {
		UserMetricsComplete bool `json:"user_metrics_complete"`
		UserMetricsLimit    int  `json:"user_metrics_limit"`
	}
	require.NoError(t, json.Unmarshal(encoded, &metadata))
	require.False(t, metadata.UserMetricsComplete)
	require.Equal(t, financeDashboardMaxUserMetrics, metadata.UserMetricsLimit)
}

func TestFinanceAccumulatorBoundsMethodUserPairs(t *testing.T) {
	accumulator := newFinanceAccumulator(0, 24*60*60, nil)
	for userID := 1; userID <= financeDashboardMaxMethodUserPairs+1; userID++ {
		accumulator.addMethodUser("stripe\x00stripe", userID)
	}

	require.Equal(t, financeDashboardMaxMethodUserPairs, accumulator.methodUserPairs)
	require.Len(t, accumulator.methodUsers["stripe\x00stripe"], financeDashboardMaxMethodUserPairs)

	view := accumulator.finish()
	require.False(t, view.MethodUserMetricsComplete)
	require.Equal(t, financeDashboardMaxMethodUserPairs, view.MethodUserMetricsLimit)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	var metadata struct {
		MethodUserMetricsComplete bool `json:"method_user_metrics_complete"`
		MethodUserMetricsLimit    int  `json:"method_user_metrics_limit"`
	}
	require.NoError(t, json.Unmarshal(encoded, &metadata))
	require.False(t, metadata.MethodUserMetricsComplete)
	require.Equal(t, financeDashboardMaxMethodUserPairs, metadata.MethodUserMetricsLimit)
}

func TestFinanceOverviewStreamsSourcesAcrossBatchBoundary(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	user := model.User{Username: "finance-batch", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-batch"}
	require.NoError(t, db.Create(&user).Error)

	rows := make([]model.TopUp, financeDashboardBatchSize+1)
	for index := range rows {
		rows[index] = model.TopUp{
			UserId:              user.Id,
			TradeNo:             "finance-batch-" + strconv.Itoa(index),
			Money:               1,
			SettledAmountMicros: 1_000_000,
			SettlementCurrency:  "USD",
			Status:              common.TopUpStatusSuccess,
			PaymentMethod:       "stripe",
			PaymentProvider:     "stripe",
			CompleteTime:        now - int64(index),
		}
	}
	require.NoError(t, db.Create(&rows).Error)

	view, err := buildFinanceOverview(now-int64(len(rows))-1, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(financeDashboardBatchSize+1)*1_000_000, view.RevenueMicros)
	require.Len(t, view.Users, 1)
}

func TestFinanceOverviewStreamsClickHouseOrderedLogsAcrossSameSecond(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	user := model.User{Username: "finance-log-batch", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-log-batch"}
	require.NoError(t, db.Create(&user).Error)

	// ClickHouse stores Log.id as zero and orders by (created_at, request_id).
	// Keep every row in the same second so a numeric-id cursor would stop after
	// the first batch and silently undercount the dashboard.
	count := financeDashboardBatchSize + 1
	logs := make([]model.Log, count)
	for index := range logs {
		logs[index] = model.Log{
			UserId: user.Id, CreatedAt: now, Type: model.LogTypeConsume,
			PromptTokens: 1, CompletionTokens: 1,
			RequestId: fmt.Sprintf("finance-log-%04d", index),
		}
	}
	require.NoError(t, db.Create(&logs).Error)

	view, err := buildFinanceOverview(now-1, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(count), view.Tokens.Requests)
	require.Equal(t, int64(count*2), view.Tokens.TotalTokens)
	require.Len(t, view.Users, 1)
	require.Equal(t, int64(count), view.Users[0].Requests)
}

func TestFinanceLedgerIsAppendOnlyAndReversalIsExactlyOnce(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.FinanceLedgerEntry{}))
	now := time.Now().Unix()
	entry, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{EntryType: model.FinanceEntryExpense, Category: "vendor", AmountMicros: 1_250_000, Currency: "usd", Direction: model.FinanceDirectionDebit, SourceType: model.FinanceSourceManual, OccurredAt: now, CreatedBy: 1, IdempotencyKey: "vendor-1"})
	require.NoError(t, err)
	require.Equal(t, entry.Id, mustFinanceEntry(t, "vendor-1").Id)
	replay, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{EntryType: model.FinanceEntryExpense, Category: "different", AmountMicros: 9, Currency: "USD", Direction: model.FinanceDirectionDebit, SourceType: model.FinanceSourceManual, OccurredAt: now, CreatedBy: 1, IdempotencyKey: "vendor-1"})
	require.NoError(t, err)
	require.Equal(t, entry.Id, replay.Id)
	reversal, err := model.ReverseFinanceLedgerEntry(entry.Id, 1, now+1)
	require.NoError(t, err)
	require.Equal(t, model.FinanceEntryExpense, reversal.EntryType)
	require.Equal(t, int8(model.FinanceDirectionCredit), reversal.Direction)
	_, err = model.ReverseFinanceLedgerEntry(entry.Id, 1, now+2)
	require.ErrorIs(t, err, model.ErrFinanceAlreadyReversed)
}

func TestFinanceOverviewSubtractsCreditDirectionExpenseReversal(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	user := model.User{Username: "finance-reversal", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-reversal"}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	entry, err := model.AppendFinanceLedgerEntry(&model.FinanceLedgerEntry{
		EntryType: model.FinanceEntryExpense, Category: "hosting", AmountMicros: 2_500_000,
		Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit,
		SourceType: model.FinanceSourceManual, UserId: &user.Id, OccurredAt: now - 20,
		CreatedBy: user.Id, IdempotencyKey: "finance-reversal-expense",
	})
	require.NoError(t, err)
	_, err = model.ReverseFinanceLedgerEntry(entry.Id, user.Id, now-10)
	require.NoError(t, err)

	view, err := buildFinanceOverview(now-100, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(0), view.ExpenseMicros)
	require.Equal(t, int64(0), view.ProfitMicros)
	require.Len(t, view.Users, 1)
	require.Equal(t, int64(0), view.Users[0].ExpenseMicros)
}

func TestFinanceOverviewIncludesLegacyLowercaseUSDLedgerRows(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	now := time.Now().Unix()
	// Direct imports and old versions could persist currency without the
	// append-path normalizer. A lowercase USD row is still a USD financial
	// event and must remain visible in the administrator dashboard.
	require.NoError(t, db.Create(&model.FinanceLedgerEntry{
		EntryType:      model.FinanceEntryExpense,
		Category:       "legacy-hosting",
		AmountMicros:   1_250_000,
		Currency:       "usd",
		Direction:      model.FinanceDirectionDebit,
		SourceType:     model.FinanceSourceManual,
		OccurredAt:     now - 1,
		CreatedAt:      now - 1,
		CreatedBy:      1,
		IdempotencyKey: "finance-legacy-lowercase-usd",
	}).Error)

	view, err := buildFinanceOverview(now-10, now+1, 0, "")
	require.NoError(t, err)
	require.Equal(t, int64(1_250_000), view.ExpenseMicros)
	require.Equal(t, int64(-1_250_000), view.ProfitMicros)
}

func TestFinanceHandlersRequireAdminRouteContractAndReturnUserDetail(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	user := model.User{Username: "finance-detail", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-detail"}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.TopUp{UserId: user.Id, TradeNo: "finance-detail-topup", Money: 3.5, Amount: 35, SettledAmountMicros: 3_500_000, SettlementCurrency: "USD", Status: common.TopUpStatusSuccess, PaymentMethod: "stripe", PaymentProvider: "stripe", CompleteTime: now - 1}).Error)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/finance/users/"+stringInt(user.Id), nil)
	c.Params = gin.Params{{Key: "user_id", Value: stringInt(user.Id)}}
	GetFinanceUser(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool            `json:"success"`
		Data    financeOverview `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, int64(3_500_000), response.Data.RevenueMicros)
}

func TestFinanceUsersExposeBoundedMetricsMetadata(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.FinancePaymentMethod{}))
	user := model.User{Username: "finance-users-metadata", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-users-metadata"}
	require.NoError(t, db.Create(&user).Error)
	now := time.Now().Unix()
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "finance-users-metadata-topup", Money: 1,
		SettledAmountMicros: 1_000_000, SettlementCurrency: "USD",
		Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe,
		PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 1,
	}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, "/api/finance/users", nil)
	GetFinanceUsers(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Users                []financeUserMetric `json:"users"`
			UserMetricsComplete  bool                `json:"user_metrics_complete"`
			UserMetricsTruncated bool                `json:"user_metrics_truncated"`
			UserMetricsLimit     int                 `json:"user_metrics_limit"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Len(t, response.Data.Users, 1)
	require.True(t, response.Data.UserMetricsComplete)
	require.False(t, response.Data.UserMetricsTruncated)
	require.Equal(t, financeDashboardMaxUserMetrics, response.Data.UserMetricsLimit)
}

func TestFinanceOverviewRejectsInvalidUserFilter(t *testing.T) {
	for _, rawUserID := range []string{"abc", "0", "-1"} {
		t.Run(rawUserID, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodGet, "/api/finance/overview?user_id="+rawUserID, nil)

			GetFinanceOverview(c)

			require.Equal(t, http.StatusBadRequest, recorder.Code)
			require.Contains(t, recorder.Body.String(), "invalid user_id")
		})
	}
}

func TestParseFinanceEntryCursorRequiresStablePair(t *testing.T) {
	newContext := func(path string) *gin.Context {
		recorder := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(recorder)
		c.Request = httptest.NewRequest(http.MethodGet, path, nil)
		return c
	}
	c := newContext("/api/finance/entries?before_id=7")
	_, _, err := parseFinanceEntryCursor(c)
	require.Error(t, err)

	c = newContext("/api/finance/entries?before_occurred_at=10&before_id=7")
	occurredAt, entryID, err := parseFinanceEntryCursor(c)
	require.NoError(t, err)
	require.Equal(t, int64(10), occurredAt)
	require.Equal(t, int64(7), entryID)
}

func TestFinanceEntriesUseStableCursorPages(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.FinanceLedgerEntry{}))
	now := time.Now().Unix()
	for index, occurredAt := range []int64{now - 30, now - 20, now - 10} {
		require.NoError(t, db.Create(&model.FinanceLedgerEntry{
			EntryType: model.FinanceEntryExpense, Category: "test", AmountMicros: int64(index + 1),
			Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit,
			SourceType: model.FinanceSourceManual, OccurredAt: occurredAt, CreatedAt: occurredAt,
			CreatedBy: 1, IdempotencyKey: "finance-page-" + strconv.Itoa(index),
		}).Error)
	}

	firstRecorder := httptest.NewRecorder()
	first, _ := gin.CreateTestContext(firstRecorder)
	first.Request = httptest.NewRequest(http.MethodGet, "/api/finance/entries?limit=2", nil)
	ListFinanceEntries(first)
	require.Equal(t, http.StatusOK, firstRecorder.Code)
	var firstResponse struct {
		Data struct {
			Entries              []model.FinanceLedgerEntry `json:"entries"`
			HasMore              bool                       `json:"has_more"`
			NextBeforeOccurredAt int64                      `json:"next_before_occurred_at"`
			NextBeforeID         int64                      `json:"next_before_id"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(firstRecorder.Body.Bytes(), &firstResponse))
	require.Len(t, firstResponse.Data.Entries, 2)
	require.True(t, firstResponse.Data.HasMore)
	require.Equal(t, now-20, firstResponse.Data.NextBeforeOccurredAt)
	require.Equal(t, firstResponse.Data.Entries[1].Id, firstResponse.Data.NextBeforeID)

	secondRecorder := httptest.NewRecorder()
	second, _ := gin.CreateTestContext(secondRecorder)
	second.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/finance/entries?limit=2&before_occurred_at=%d&before_id=%d", firstResponse.Data.NextBeforeOccurredAt, firstResponse.Data.NextBeforeID), nil)
	ListFinanceEntries(second)
	require.Equal(t, http.StatusOK, secondRecorder.Code)
	var secondResponse struct {
		Data struct {
			Entries []model.FinanceLedgerEntry `json:"entries"`
			HasMore bool                       `json:"has_more"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(secondRecorder.Body.Bytes(), &secondResponse))
	require.Len(t, secondResponse.Data.Entries, 1)
	require.False(t, secondResponse.Data.HasMore)
	require.Equal(t, now-30, secondResponse.Data.Entries[0].OccurredAt)
}

func TestFinanceEntriesFilterLedgerScopeByMethodRangeAndUser(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.FinanceLedgerEntry{}))
	users := []model.User{
		{Username: "finance-entry-a", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-entry-a"},
		{Username: "finance-entry-b", Password: "password", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default", AffCode: "finance-entry-b"},
	}
	require.NoError(t, db.Create(&users).Error)
	now := time.Now().Unix()
	for _, entry := range []model.FinanceLedgerEntry{
		{EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 100, Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, UserId: &users[0].Id, SourceType: model.FinanceSourceRefund, SourceId: "stripe-refund-a", OccurredAt: now - 20, CreatedAt: now - 20, CreatedBy: 1, IdempotencyKey: "finance-entry-stripe-a"},
		{EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 200, Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, UserId: &users[1].Id, SourceType: model.FinanceSourceRefund, SourceId: "stripe-refund-b", OccurredAt: now - 30, CreatedAt: now - 30, CreatedBy: 1, IdempotencyKey: "finance-entry-stripe-b"},
		{EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 300, Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodCreem, PaymentProvider: model.PaymentProviderCreem, UserId: &users[0].Id, SourceType: model.FinanceSourceRefund, SourceId: "creem-refund-a", OccurredAt: now - 40, CreatedAt: now - 40, CreatedBy: 1, IdempotencyKey: "finance-entry-creem-a"},
		{EntryType: model.FinanceEntryRevenue, Category: model.FinanceSourceRefund, AmountMicros: 400, Currency: model.FinanceCurrencyUSD, Direction: model.FinanceDirectionDebit, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, UserId: &users[0].Id, SourceType: model.FinanceSourceRefund, SourceId: "stripe-refund-old", OccurredAt: now - 200, CreatedAt: now - 200, CreatedBy: 1, IdempotencyKey: "finance-entry-stripe-old"},
	} {
		require.NoError(t, db.Create(&entry).Error)
	}
	// A settled TopUp is intentionally not returned by the append-only ledger
	// endpoint. This prevents the detail UI from implying it contains every
	// payment receipt before providers write revenue ledger rows consistently.
	require.NoError(t, db.Create(&model.TopUp{UserId: users[0].Id, TradeNo: "finance-entry-topup", Money: 9, Status: common.TopUpStatusSuccess, PaymentMethod: model.PaymentMethodStripe, PaymentProvider: model.PaymentProviderStripe, CompleteTime: now - 20}).Error)

	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/finance/entries?start_timestamp=%d&end_timestamp=%d&payment_method=stripe&user_id=%d", now-100, now, users[0].Id), nil)
	ListFinanceEntries(c)
	require.Equal(t, http.StatusOK, recorder.Code)
	var response struct {
		Success bool `json:"success"`
		Data    struct {
			Scope   string                     `json:"scope"`
			Range   financeRange               `json:"range"`
			Entries []model.FinanceLedgerEntry `json:"entries"`
		} `json:"data"`
	}
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	require.True(t, response.Success)
	require.Equal(t, "append_only_ledger", response.Data.Scope)
	require.Equal(t, now-100, response.Data.Range.Start)
	require.Equal(t, now, response.Data.Range.End)
	require.Len(t, response.Data.Entries, 1)
	require.Equal(t, "stripe-refund-a", response.Data.Entries[0].SourceId)
}

func mustFinanceEntry(t *testing.T, key string) *model.FinanceLedgerEntry {
	t.Helper()
	var entry model.FinanceLedgerEntry
	require.NoError(t, model.DB.Where("idempotency_key = ?", key).First(&entry).Error)
	return &entry
}
