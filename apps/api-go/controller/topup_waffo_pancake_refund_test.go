package controller

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHandleWaffoPancakeRefundEventRecordsUserVisibleRefundLog(t *testing.T) {
	tests := []struct {
		name      string
		eventType string
		amount    string
		wantText  string
	}{
		{name: "succeeded", eventType: "refund.succeeded", amount: "2.50", wantText: "refund.succeeded"},
		{name: "failed", eventType: "refund.failed", amount: "", wantText: "refund.failed"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupTokenControllerTestDB(t)
			require.NoError(t, db.AutoMigrate(&model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}, &model.WaffoPancakeWebhookReceipt{}))
			user := model.User{
				Username: "pancake-refund-" + tt.name,
				Password: "password",
				Status:   common.UserStatusEnabled,
			}
			require.NoError(t, db.Create(&user).Error)
			require.NoError(t, db.Create(&model.TopUp{
				UserId:          user.Id,
				TradeNo:         "WAFFO_PANCAKE-" + tt.name,
				PaymentMethod:   model.PaymentMethodWaffoPancake,
				PaymentProvider: model.PaymentProviderWaffoPancake,
				Status:          common.TopUpStatusSuccess,
				Money:           2.50,
			}).Error)

			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			event := &service.WaffoPancakeWebhookEvent{
				ID:        "evt-refund-" + tt.name,
				EventType: tt.eventType,
				Data: service.WaffoPancakeWebhookData{
					OrderMerchantExternalID:        "WAFFO_PANCAKE-" + tt.name,
					RefundTicketMerchantExternalID: "refund-" + tt.name,
					Amount:                         tt.amount,
					Currency:                       "USD",
					RefundReason:                   "provider declined",
				},
			}

			require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))
			var logs []model.Log
			require.NoError(t, db.Where("user_id = ?", user.Id).Find(&logs).Error)
			require.Len(t, logs, 1)
			require.Equal(t, model.LogTypeRefund, logs[0].Type)
			require.Contains(t, logs[0].Content, tt.wantText)
		})
	}
}

func TestHandleWaffoPancakeRefundReversesWalletQuotaExactlyOnce(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-refund-wallet",
		Password: "password",
		Status:   common.UserStatusEnabled,
		Quota:    100_000,
	}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "WAFFO_PANCAKE-wallet-refund"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:              user.Id,
		TradeNo:             tradeNo,
		Amount:              100,
		CreditedQuota:       100_000,
		SettledAmountMicros: 10_000_000,
		PaymentMethod:       model.PaymentMethodWaffoPancake,
		PaymentProvider:     model.PaymentProviderWaffoPancake,
		Status:              common.TopUpStatusSuccess,
		Money:               10,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	event := &service.WaffoPancakeWebhookEvent{
		ID:        "evt-refund-wallet",
		EventType: "refund.succeeded",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-wallet",
			Amount:                         "2.50",
			Currency:                       "USD",
		},
	}
	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))
	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))

	var refreshed model.User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	assert.Equal(t, 75_000, refreshed.Quota)
	var topUp model.TopUp
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&topUp).Error)
	assert.Equal(t, int64(2_500_000), topUp.RefundedAmountMicros)
	assert.Equal(t, int64(25_000), topUp.RefundedQuota)
	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ?", model.FinanceSourceRefund).Find(&entries).Error)
	require.Len(t, entries, 1)
}

func TestHandleWaffoPancakeSubscriptionRefundIsLedgerIdempotent(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	setting.WaffoPancakeStoreID = "store-current"
	t.Cleanup(func() { setting.WaffoPancakeStoreID = originalStoreID })

	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-subscription-refund",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{
		// This order represents a legacy checkout: the plan exists, but the
		// provider refund has no StoreID or order metadata.
		Id:                    987653,
		Title:                 "Legacy refund plan",
		PriceAmount:           2.50,
		Currency:              "USD",
		WaffoPancakeProductId: "product-legacy-refund",
		Enabled:               true,
	}
	require.NoError(t, db.Create(&plan).Error)
	tradeNo := "WAFFO_PANCAKE_SUB-1-refund"
	require.NoError(t, db.Create(&model.SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusSuccess,
		Money:           plan.PriceAmount,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	event := &service.WaffoPancakeWebhookEvent{
		ID:        "evt-subscription-refund",
		EventType: "refund.succeeded",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-subscription-1",
			Amount:                         "2.50",
			Currency:                       "USD",
		},
	}

	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))
	// Providers retry webhook deliveries. The second delivery must not create
	// a second financial row or duplicate the user-visible audit log.
	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))

	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ? AND source_id = ?", model.FinanceSourceRefund, event.ID).Find(&entries).Error)
	require.Len(t, entries, 1)
	require.Equal(t, model.PaymentProviderWaffoPancake, entries[0].PaymentProvider)
	require.Equal(t, int64(2_500_000), entries[0].AmountMicros)

	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Find(&logs).Error)
	require.Len(t, logs, 1)
}

func TestHandleWaffoPancakeSubscriptionRefundUsesCheckoutSnapshotNotLivePlan(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	setting.WaffoPancakeStoreID = "store-rotated"
	t.Cleanup(func() { setting.WaffoPancakeStoreID = originalStoreID })

	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-subscription-refund-snapshot",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{
		Id:                    987656,
		Title:                 "CNY list price plan",
		PriceAmount:           6.8,
		Currency:              "CNY",
		WaffoPancakeProductId: "product-rotated",
		Enabled:               true,
	}
	require.NoError(t, db.Create(&plan).Error)
	tradeNo := "WAFFO_PANCAKE_SUB-refund-snapshot"
	require.NoError(t, db.Create(&model.SubscriptionOrder{
		UserId:               user.Id,
		PlanId:               plan.Id,
		TradeNo:              tradeNo,
		PaymentMethod:        model.PaymentMethodWaffoPancake,
		PaymentProvider:      model.PaymentProviderWaffoPancake,
		Status:               common.TopUpStatusSuccess,
		Money:                plan.PriceAmount,
		ExpectedAmountMicros: 1_000_000,
		SettlementCurrency:   "USD",
		ProviderProductId:    "product-checkout",
		ProviderStoreId:      "store-checkout",
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	event := &service.WaffoPancakeWebhookEvent{
		ID:        "evt-subscription-refund-snapshot",
		EventType: "refund.succeeded",
		StoreID:   "store-checkout",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-subscription-snapshot",
			Amount:                         "1.00",
			Currency:                       "USD",
			OrderMetadata: map[string]string{
				service.WaffoPancakeOrderMetadataProductID: "product-checkout",
				service.WaffoPancakeOrderMetadataPlanID:    strconv.Itoa(plan.Id),
			},
		},
	}

	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))

	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ? AND source_id = ?", model.FinanceSourceRefund, event.ID).Find(&entries).Error)
	require.Len(t, entries, 1)
	require.Equal(t, int64(1_000_000), entries[0].AmountMicros)
	require.Equal(t, "USD", entries[0].Currency)

	var stored model.SubscriptionOrder
	require.NoError(t, db.Where("trade_no = ?", tradeNo).First(&stored).Error)
	require.Equal(t, int64(1_000_000), stored.RefundedAmountMicros)
}

func TestHandleWaffoPancakeSubscriptionRefundRejectsMismatchedMetadata(t *testing.T) {
	originalStoreID := setting.WaffoPancakeStoreID
	setting.WaffoPancakeStoreID = "store-subscription-refund"
	t.Cleanup(func() { setting.WaffoPancakeStoreID = originalStoreID })

	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.SubscriptionPlan{}, &model.SubscriptionOrder{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-subscription-refund-binding",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	plan := model.SubscriptionPlan{
		// Keep this ID outside the range used by other temporary test databases;
		// subscription plans are cached by ID across tests.
		Id:                    987654,
		Title:                 "Refund binding plan",
		PriceAmount:           4,
		Currency:              "USD",
		WaffoPancakeProductId: "product-subscription-refund",
		Enabled:               true,
	}
	require.NoError(t, db.Create(&plan).Error)
	tradeNo := "WAFFO_PANCAKE_SUB-refund-binding"
	require.NoError(t, db.Create(&model.SubscriptionOrder{
		UserId:          user.Id,
		PlanId:          plan.Id,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusSuccess,
		Money:           plan.PriceAmount,
	}).Error)

	tests := []struct {
		name   string
		mutate func(*service.WaffoPancakeWebhookEvent)
		want   string
	}{
		{name: "store", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.StoreID = "store-other" }, want: "store mismatch"},
		{name: "currency", mutate: func(event *service.WaffoPancakeWebhookEvent) { event.Data.Currency = "EUR" }, want: "currency mismatch"},
		{name: "product", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataProductID] = "product-other"
		}, want: "product metadata mismatch"},
		{name: "plan", mutate: func(event *service.WaffoPancakeWebhookEvent) {
			event.Data.OrderMetadata[service.WaffoPancakeOrderMetadataPlanID] = "987655"
		}, want: "plan metadata mismatch"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
			ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
			event := &service.WaffoPancakeWebhookEvent{
				ID:        "evt-subscription-refund-binding-" + tt.name,
				EventType: "refund.succeeded",
				StoreID:   "store-subscription-refund",
				Data: service.WaffoPancakeWebhookData{
					OrderMerchantExternalID:        tradeNo,
					RefundTicketMerchantExternalID: "refund-subscription-binding-" + tt.name,
					Amount:                         "1.00",
					Currency:                       "USD",
					OrderMetadata: map[string]string{
						service.WaffoPancakeOrderMetadataProductID: plan.WaffoPancakeProductId,
						service.WaffoPancakeOrderMetadataPlanID:    strconv.Itoa(plan.Id),
					},
				},
			}
			tt.mutate(event)

			err := handleWaffoPancakeRefundEvent(ctx, event)
			require.ErrorContains(t, err, tt.want)
			var entries []model.FinanceLedgerEntry
			require.NoError(t, db.Where("source_type = ?", model.FinanceSourceRefund).Find(&entries).Error)
			require.Empty(t, entries)
		})
	}
}

func TestHandleWaffoPancakeRefundRejectsDifferentStore(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-refund-store-binding",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "WAFFO_PANCAKE-store-binding"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          user.Id,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		ProviderStoreId: "store-expected",
		Status:          common.TopUpStatusSuccess,
		Money:           2.50,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	event := &service.WaffoPancakeWebhookEvent{
		ID:        "evt-refund-wrong-store",
		EventType: "refund.succeeded",
		StoreID:   "store-other",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-store-binding",
			Amount:                         "2.50",
			Currency:                       "USD",
		},
	}

	err := handleWaffoPancakeRefundEvent(ctx, event)
	require.Error(t, err)
	require.ErrorContains(t, err, "store mismatch")

	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ?", model.FinanceSourceRefund).Find(&entries).Error)
	require.Empty(t, entries)
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Find(&logs).Error)
	require.Empty(t, logs)
}

func TestHandleWaffoPancakeRefundRejectsContradictoryStatus(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{
		Username: "pancake-refund-status-binding",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "WAFFO_PANCAKE-status-binding"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          user.Id,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusSuccess,
		Money:           2.50,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	err := handleWaffoPancakeRefundEvent(ctx, &service.WaffoPancakeWebhookEvent{
		ID:        "evt-refund-status-binding",
		EventType: "refund.succeeded",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-status-binding",
			Amount:                         "2.50",
			Currency:                       "USD",
			RefundStatus:                   "failed",
		},
	})
	require.ErrorContains(t, err, "refundStatus mismatch")

	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Where("source_type = ?", model.FinanceSourceRefund).Find(&entries).Error)
	require.Empty(t, entries)
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Find(&logs).Error)
	require.Empty(t, logs)
}

func TestHandleWaffoPancakeRefundFailedIsEventIdempotent(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.WaffoPancakeWebhookReceipt{}))
	user := model.User{
		Username: "pancake-refund-failed-idempotency",
		Password: "password",
		Status:   common.UserStatusEnabled,
	}
	require.NoError(t, db.Create(&user).Error)
	tradeNo := "WAFFO_PANCAKE-refund-failed-idempotency"
	require.NoError(t, db.Create(&model.TopUp{
		UserId:          user.Id,
		TradeNo:         tradeNo,
		PaymentMethod:   model.PaymentMethodWaffoPancake,
		PaymentProvider: model.PaymentProviderWaffoPancake,
		Status:          common.TopUpStatusSuccess,
		Money:           2.50,
	}).Error)

	ctx, _ := gin.CreateTestContext(httptest.NewRecorder())
	ctx.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	event := &service.WaffoPancakeWebhookEvent{
		ID:        "evt-refund-failed-idempotency",
		EventType: "refund.failed",
		Data: service.WaffoPancakeWebhookData{
			OrderMerchantExternalID:        tradeNo,
			RefundTicketMerchantExternalID: "refund-failed-idempotency",
			Currency:                       "USD",
			RefundStatus:                   "failed",
			RefundReason:                   "provider declined",
		},
	}

	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))
	require.NoError(t, handleWaffoPancakeRefundEvent(ctx, event))

	var receipts []model.WaffoPancakeWebhookReceipt
	require.NoError(t, db.Where("provider = ? AND event_id = ?", model.PaymentProviderWaffoPancake, event.ID).Find(&receipts).Error)
	require.Len(t, receipts, 1)
	var logs []model.Log
	require.NoError(t, db.Where("user_id = ? AND type = ?", user.Id, model.LogTypeRefund).Find(&logs).Error)
	require.Len(t, logs, 1)
}
