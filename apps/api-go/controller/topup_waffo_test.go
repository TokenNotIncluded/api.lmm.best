package controller

import (
	"bytes"
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	waffo "github.com/waffo-com/waffo-go/v2"
	"github.com/waffo-com/waffo-go/v2/config"
	"github.com/waffo-com/waffo-go/v2/core"
	waffonet "github.com/waffo-com/waffo-go/v2/net"
	"github.com/waffo-com/waffo-go/v2/types/order"
	"github.com/waffo-com/waffo-go/v2/utils"
)

type waffoWireTestTransport func(context.Context, *waffonet.HttpRequest) (*waffonet.HttpResponse, error)

func (f waffoWireTestTransport) Send(ctx context.Context, req *waffonet.HttpRequest) (*waffonet.HttpResponse, error) {
	return f(ctx, req)
}

func requireWaffoRSASignature(t *testing.T, body []byte, signature, publicKey string) {
	t.Helper()
	keyDER, err := base64.StdEncoding.DecodeString(publicKey)
	require.NoError(t, err)
	key, err := x509.ParsePKIXPublicKey(keyDER)
	require.NoError(t, err)
	rsaKey, ok := key.(*rsa.PublicKey)
	require.True(t, ok)
	signatureBytes, err := base64.StdEncoding.DecodeString(signature)
	require.NoError(t, err)
	digest := sha256.Sum256(body)
	require.NoError(t, rsa.VerifyPKCS1v15(rsaKey, crypto.SHA256, digest[:], signatureBytes))
}

func TestWaffoSDKV2LegacyCheckoutWireContract(t *testing.T) {
	merchantKeys, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	providerKeys, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	previousSystemName := common.SystemName
	common.SystemName = "Legacy Checkout Test"
	t.Cleanup(func() { common.SystemName = previousSystemName })

	goods := buildWaffoTopUpGoodsInfo(decimal.RequireFromString("12.5"))
	params := &order.CreateOrderParams{
		PaymentRequestID:   "WAFFO-v2-wire",
		MerchantOrderID:    "WAFFO-v2-wire",
		OrderAmount:        formatWaffoAmount(12.34, getWaffoCurrency()),
		OrderCurrency:      getWaffoCurrency(),
		OrderDescription:   goods.GoodsName,
		OrderRequestedAt:   "2026-09-04T00:00:00.000Z",
		NotifyURL:          "https://merchant.example/api/waffo/webhook",
		SuccessRedirectURL: "https://merchant.example/wallet",
		FailedRedirectURL:  "https://merchant.example/wallet",
		UserInfo: &order.UserInfo{
			UserID: "41", UserEmail: getWaffoUserEmail(&model.User{Id: 41}), UserTerminal: "WEB",
		},
		PaymentInfo: &order.PaymentInfo{
			ProductName: "ONE_TIME_PAYMENT", PayMethodType: "CARD", PayMethodName: "CARD",
		},
		GoodsInfo: goods,
	}
	profile := enabledCompanyBillingProfile()
	profile.State = "NY"
	require.NoError(t, validateLegacyWaffoCompanyBilling(profile))
	applyCompanyBillingToLegacyWaffoOrder(params, profile)

	calls := 0
	transport := waffoWireTestTransport(func(_ context.Context, req *waffonet.HttpRequest) (*waffonet.HttpResponse, error) {
		calls++
		require.Equal(t, http.MethodPost, req.Method)
		require.Equal(t, config.Sandbox.BaseURL()+"/order/create", req.URL)
		require.Equal(t, "waffo-go/2.1.0", req.Headers[core.HeaderSDKVersion])
		require.Equal(t, "waffo-wire-test-key", req.Headers[core.HeaderAPIKey])
		require.Equal(t, "1.0.0", req.Headers[core.HeaderAPIVersion])
		require.Equal(t, "application/json", req.Headers[core.HeaderContentType])
		requireWaffoRSASignature(t, req.Body, req.Headers[core.HeaderSignature], merchantKeys.PublicKey)
		// Exact shape guards string amount/currency, nested company address,
		// and omission of unused token, subscription, and x402 fields.
		require.JSONEq(t, `{
			"paymentRequestId":"WAFFO-v2-wire","merchantOrderId":"WAFFO-v2-wire",
			"orderAmount":"12.34","orderCurrency":"USD",
			"orderDescription":"Recharge 12.5 platform units","orderRequestedAt":"2026-09-04T00:00:00.000Z",
			"notifyUrl":"https://merchant.example/api/waffo/webhook",
			"successRedirectUrl":"https://merchant.example/wallet","failedRedirectUrl":"https://merchant.example/wallet",
			"merchantInfo":{"merchantId":"merchant-wire-test"},
			"userInfo":{"userId":"41","userEmail":"41@examples.com","userTerminal":"WEB"},
			"paymentInfo":{"productName":"ONE_TIME_PAYMENT","payMethodType":"CARD","payMethodName":"CARD"},
			"goodsInfo":{"goodsName":"Recharge 12.5 platform units","appName":"Legacy Checkout Test"},
			"addressInfo":{"billingAddress":{"country":"US","state":"NY","postalCode":"10001"}}
		}`, string(req.Body))
		body := []byte(`{"code":"0","data":{"paymentRequestId":"WAFFO-v2-wire","merchantOrderId":"WAFFO-v2-wire","orderAction":"{\"actionType\":\"REDIRECT\",\"webUrl\":\"https://checkout.example/pay\"}"}}`)
		signature, err := utils.Sign(string(body), providerKeys.PrivateKey)
		if err != nil {
			return nil, err
		}
		return waffonet.NewHttpResponse(http.StatusOK, map[string]string{core.HeaderSignature: signature}, body), nil
	})
	cfg, err := config.NewConfigBuilder().
		APIKey("waffo-wire-test-key").
		PrivateKey(merchantKeys.PrivateKey).
		WaffoPublicKey(providerKeys.PublicKey).
		Environment(config.Sandbox).
		MerchantID("merchant-wire-test").
		CustomTransport(transport).
		Build()
	require.NoError(t, err)
	// CustomTransport handles every request in memory; no provider is contacted.
	resp, err := waffo.New(cfg).Order().Create(context.Background(), params, nil)
	require.NoError(t, err)
	require.Equal(t, 1, calls)
	require.True(t, resp.IsSuccess())
	require.NotNil(t, resp.GetData())
	require.Equal(t, params.MerchantOrderID, resp.GetData().MerchantOrderID)
	require.Equal(t, "https://checkout.example/pay", resp.GetData().FetchRedirectURL())
}

func TestWaffoWebhookReceiptLogOmitsSensitiveValues(t *testing.T) {
	logLine := waffoWebhookReceiptLog("/api/waffo/webhook", "198.51.100.7", len(`{"secret":"payload"}`))

	require.Contains(t, logLine, "body_bytes=")
	require.NotContains(t, logLine, "secret")
	require.NotContains(t, logLine, "payload")
	require.NotContains(t, logLine, "signature")
}

func newWaffoRefundTestContext(t *testing.T) (*gin.Context, *httptest.ResponseRecorder, *core.WebhookHandler) {
	t.Helper()
	keys, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	ctx.Request = httptest.NewRequest(http.MethodPost, "/api/waffo/webhook", nil)
	return ctx, response, core.NewWebhookHandler(&config.WaffoConfig{PrivateKey: keys.PrivateKey})
}

func TestHandleWaffoRefundAppliesPartialFullAndReplayExactlyOnce(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{Username: "waffo-refund-owner", Password: "password", Status: common.UserStatusEnabled, Quota: 100_000}
	require.NoError(t, db.Create(&user).Error)
	topUp := model.TopUp{
		UserId: user.Id, TradeNo: "WAFFO-refund-owner", Amount: 100, CreditedQuota: 100_000,
		Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD",
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, db.Create(&topUp).Error)

	apply := func(refundID, amount, status string) {
		ctx, response, handler := newWaffoRefundTestContext(t)
		handleWaffoRefund(ctx, handler, &core.RefundNotificationResult{
			OrigPaymentRequestID:   topUp.TradeNo,
			AcquiringRefundOrderID: refundID,
			RefundAmount:           amount,
			RefundStatus:           status,
		})
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `{"message":"success"}`, response.Body.String())
	}

	apply("waffo-refund-partial", "2.50", core.RefundStatusPartiallyRefunded)
	apply("waffo-refund-partial", "2.50", core.RefundStatusPartiallyRefunded)
	apply("waffo-refund-full", "7.50", core.RefundStatusFullyRefunded)

	var refreshedUser model.User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	assert.Zero(t, refreshedUser.Quota)
	var refreshedTopUp model.TopUp
	require.NoError(t, db.First(&refreshedTopUp, topUp.Id).Error)
	assert.Equal(t, int64(10_000_000), refreshedTopUp.RefundedAmountMicros)
	assert.Equal(t, int64(100_000), refreshedTopUp.RefundedQuota)
	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Order("id").Find(&entries).Error)
	require.Len(t, entries, 2)
	assert.Equal(t, model.PaymentProviderWaffo, entries[0].PaymentProvider)
	assert.Equal(t, model.PaymentMethodWaffo, entries[0].PaymentMethod)
}

func TestHandleWaffoRefundRejectsMismatchedOrderOrAmount(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	user := model.User{Username: "waffo-refund-mismatch", Password: "password", Status: common.UserStatusEnabled, Quota: 100_000}
	require.NoError(t, db.Create(&user).Error)
	topUp := model.TopUp{
		UserId: user.Id, TradeNo: "WAFFO-refund-mismatch", Amount: 100, CreditedQuota: 100_000,
		Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD",
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusSuccess,
	}
	require.NoError(t, db.Create(&topUp).Error)

	for _, result := range []*core.RefundNotificationResult{
		{OrigPaymentRequestID: "unknown-order", AcquiringRefundOrderID: "waffo-refund-unknown", RefundAmount: "1.00", RefundStatus: core.RefundStatusPartiallyRefunded},
		{OrigPaymentRequestID: topUp.TradeNo, AcquiringRefundOrderID: "waffo-refund-too-large", RefundAmount: "10.01", RefundStatus: core.RefundStatusFullyRefunded},
	} {
		ctx, response, handler := newWaffoRefundTestContext(t)
		handleWaffoRefund(ctx, handler, result)
		require.Equal(t, http.StatusOK, response.Code)
		require.JSONEq(t, `{"message":"failed"}`, response.Body.String())
	}
	// A refund must use the currency persisted at original settlement. Do not
	// guess from mutable Waffo configuration or accept the callback display
	// currency when a legacy order is incomplete.
	require.NoError(t, db.Model(&model.TopUp{}).Where("id = ?", topUp.Id).Update("settlement_currency", "").Error)
	ctx, response, handler := newWaffoRefundTestContext(t)
	handleWaffoRefund(ctx, handler, &core.RefundNotificationResult{
		OrigPaymentRequestID:   topUp.TradeNo,
		AcquiringRefundOrderID: "waffo-refund-no-currency",
		RefundAmount:           "1.00",
		RefundStatus:           core.RefundStatusPartiallyRefunded,
	})
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"message":"failed"}`, response.Body.String())

	var refreshedUser model.User
	require.NoError(t, db.First(&refreshedUser, user.Id).Error)
	assert.Equal(t, 100_000, refreshedUser.Quota)
	var entries []model.FinanceLedgerEntry
	require.NoError(t, db.Find(&entries).Error)
	assert.Empty(t, entries)
}

func TestWaffoWebhookVerifiedRefundReversesLocalTopUp(t *testing.T) {
	db := setupTokenControllerTestDB(t)
	require.NoError(t, db.AutoMigrate(&model.User{}, &model.TopUp{}, &model.Log{}, &model.FinanceLedgerEntry{}))
	confirmPaymentComplianceForTest(t)
	merchantKeys, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	providerKeys, err := utils.GenerateKeyPair()
	require.NoError(t, err)
	original := struct {
		enabled, sandbox               bool
		apiKey, privateKey, publicCert string
	}{
		setting.WaffoEnabled, setting.WaffoSandbox,
		setting.WaffoSandboxApiKey, setting.WaffoSandboxPrivateKey, setting.WaffoSandboxPublicCert,
	}
	t.Cleanup(func() {
		setting.WaffoEnabled = original.enabled
		setting.WaffoSandbox = original.sandbox
		setting.WaffoSandboxApiKey = original.apiKey
		setting.WaffoSandboxPrivateKey = original.privateKey
		setting.WaffoSandboxPublicCert = original.publicCert
	})
	setting.WaffoEnabled = true
	setting.WaffoSandbox = true
	setting.WaffoSandboxApiKey = "waffo-refund-test-api-key"
	setting.WaffoSandboxPrivateKey = merchantKeys.PrivateKey
	setting.WaffoSandboxPublicCert = providerKeys.PublicKey

	user := model.User{Username: "waffo-refund-webhook", Password: "password", Status: common.UserStatusEnabled, Quota: 100_000}
	require.NoError(t, db.Create(&user).Error)
	require.NoError(t, db.Create(&model.TopUp{
		UserId: user.Id, TradeNo: "WAFFO-refund-webhook", Amount: 100, CreditedQuota: 100_000,
		Money: 10, SettledAmountMicros: 10_000_000, SettlementCurrency: "USD",
		PaymentMethod: model.PaymentMethodWaffo, PaymentProvider: model.PaymentProviderWaffo,
		Status: common.TopUpStatusSuccess,
	}).Error)

	payload := []byte(`{"eventType":"REFUND_NOTIFICATION","result":{"origPaymentRequestId":"WAFFO-refund-webhook","acquiringRefundOrderId":"waffo-refund-webhook-partial","refundAmount":"2.50","refundStatus":"ORDER_PARTIALLY_REFUNDED"}}`)
	signature, err := utils.Sign(string(payload), providerKeys.PrivateKey)
	require.NoError(t, err)
	response := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(response)
	request := httptest.NewRequest(http.MethodPost, "/api/waffo/webhook", bytes.NewReader(payload))
	request.Header.Set("X-SIGNATURE", signature)
	ctx.Request = request

	WaffoWebhook(ctx)
	require.Equal(t, http.StatusOK, response.Code)
	require.JSONEq(t, `{"message":"success"}`, response.Body.String())
	var refreshed model.User
	require.NoError(t, db.First(&refreshed, user.Id).Error)
	assert.Equal(t, 75_000, refreshed.Quota)
}
