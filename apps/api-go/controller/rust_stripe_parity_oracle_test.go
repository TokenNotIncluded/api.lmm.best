package controller

// Exercise current Go HTTP/controller and ledger code against a local Stripe
// HTTP fixture. No real API key, provider host or database is used.
import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
	"github.com/stripe/stripe-go/v81"
	"github.com/stripe/stripe-go/v81/webhook"
)

func preserveRustStripeOraclePricing(t *testing.T) {
	t.Helper()
	previousQuota := common.QuotaPerUnit
	previousCredits, previousErr := common.CreditsPerUSD()
	previousLegacy, previousLegacyErr := common.LegacyPricingQuotaPerUnit()
	previousPublic, previousPublicErr := common.PublicCreditsPerUSD()
	previousAPISecret, previousWebhookSecret := setting.StripeApiSecret, setting.StripeWebhookSecret
	previousPriceID, previousMinimum := setting.StripePriceId, setting.StripeMinTopUp
	// Restore the complete immutable basis after preserveChannelPricing's
	// cleanup, including a legacy calibration different from the live setting.
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret = previousAPISecret, previousWebhookSecret
		setting.StripePriceId, setting.StripeMinTopUp = previousPriceID, previousMinimum
		common.QuotaPerUnit = previousQuota
		if previousPublicErr != nil || (previousErr == nil && previousPublic.Equal(previousCredits)) {
			common.ClearPublicCreditsPerUSD()
		} else {
			require.NoError(t, common.SetPublicCreditsPerUSD(previousPublic))
		}
		if previousErr != nil {
			common.ClearCreditsPerUSD()
		} else {
			require.NoError(t, previousLegacyErr)
			require.NoError(t, common.SetCreditCurrencyBasis(previousCredits, previousLegacy))
		}
	})
	preserveChannelPricing(t)
	common.QuotaPerUnit = 500000
	operation_setting.USDExchangeRate = 7.3
	operation_setting.TopUpPlatformUnitsPerCNY = 2
	// FX and recharge bonuses never redefine the fixed credit/USD contract.
	require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(500000), decimal.NewFromInt(500000)))
	require.NoError(t, common.SetPublicCreditsPerUSD(decimal.NewFromInt(500000)))
}

func TestRustStripeOraclePricingRestoresGatewayAvailability(t *testing.T) {
	originalSecret, originalWebhook := setting.StripeApiSecret, setting.StripeWebhookSecret
	originalPrice, originalMinimum := setting.StripePriceId, setting.StripeMinTopUp
	t.Cleanup(func() {
		setting.StripeApiSecret, setting.StripeWebhookSecret = originalSecret, originalWebhook
		setting.StripePriceId, setting.StripeMinTopUp = originalPrice, originalMinimum
	})
	setting.StripeApiSecret, setting.StripeWebhookSecret, setting.StripePriceId = "", "", ""
	setting.StripeMinTopUp = 7
	t.Run("oracle fixture", func(t *testing.T) {
		preserveRustStripeOraclePricing(t)
		setting.StripeApiSecret, setting.StripeWebhookSecret = "sk_fixture", "whsec_fixture"
		setting.StripePriceId, setting.StripeMinTopUp = "price_fixture", 1
	})
	require.Empty(t, setting.StripeApiSecret)
	require.Empty(t, setting.StripeWebhookSecret)
	require.Empty(t, setting.StripePriceId)
	require.Equal(t, 7, setting.StripeMinTopUp)
}

func TestRustStripeOraclePricingRestoresCurrencyBasis(t *testing.T) {
	preserveChannelPricing(t)
	previousQuota := common.QuotaPerUnit
	t.Cleanup(func() { common.QuotaPerUnit = previousQuota })
	common.QuotaPerUnit = 123456
	for _, initialized := range []bool{true, false} {
		if initialized {
			require.NoError(t, common.SetCreditCurrencyBasis(decimal.NewFromInt(1111111), decimal.NewFromInt(654321)))
		} else {
			common.ClearCreditsPerUSD()
		}
		t.Run(fmt.Sprintf("initialized=%t", initialized), func(t *testing.T) {
			preserveRustStripeOraclePricing(t)
			require.Equal(t, float64(500000), common.QuotaPerUnit)
			// Later FX/bonus changes cannot alter this site's USD quote or ledger.
			operation_setting.USDExchangeRate = 8.9
			operation_setting.TopUpPlatformUnitsPerCNY = 99
			require.Equal(t, "14.6", getStripePayMoneyForLegacyAmount(decimal.RequireFromString("14.6"), "default").String())
			_, _, quota, err := topUpOrderAmountsLegacyDecimal(decimal.RequireFromString("14.6"))
			require.NoError(t, err)
			require.EqualValues(t, 7300000, quota)
		})
		require.Equal(t, float64(123456), common.QuotaPerUnit)
		credits, err := common.CreditsPerUSD()
		if initialized {
			require.NoError(t, err)
			require.Equal(t, "1111111", credits.String())
			legacy, err := common.LegacyPricingQuotaPerUnit()
			require.NoError(t, err)
			require.Equal(t, "654321", legacy.String())
		} else {
			require.ErrorIs(t, err, common.ErrCreditUnitsUnavailable)
		}
	}
}

func TestRustStripeCurrentGoOracle(t *testing.T) {
	output := os.Getenv("LMM_STRIPE_GO_ORACLE_OUTPUT")
	if output == "" {
		t.Skip("set LMM_STRIPE_GO_ORACLE_OUTPUT for current Go Stripe reference")
	}
	preserveRustStripeOraclePricing(t)
	preservePaymentGatewaySettings(t)
	confirmPaymentComplianceForTest(t)
	setupTopupInfoUser(t, 7, "default")
	persistCreditDenominationFixture(t, model.DB)
	var optionRows []model.Option
	require.NoError(t, model.DB.Where("key IN ?", []string{model.CreditsPerUSDOptionKey, model.LegacyPricingQuotaPerUnitOptionKey, "QuotaPerUnit", model.PublicCreditsPerUSDOptionKey}).Find(&optionRows).Error)
	require.Len(t, optionRows, 4)
	currencyOptions := make(map[string]string, 4)
	for _, row := range optionRows {
		currencyOptions[row.Key] = row.Value
	}
	previousLog, previousInviter := model.LOG_DB, common.QuotaForInviter
	previousServer := system_setting.ServerAddress
	previousPromotion := setting.StripePromotionCodesEnabled
	previousBackend := stripe.GetBackend(stripe.APIBackend)
	t.Cleanup(func() {
		model.LOG_DB = previousLog
		common.QuotaForInviter = previousInviter
		system_setting.ServerAddress = previousServer
		setting.StripePromotionCodesEnabled = previousPromotion
		stripe.SetBackend(stripe.APIBackend, previousBackend)
	})
	model.LOG_DB = model.DB
	// The Stripe controller uses GetUserGroup's dialect-aware column name.
	// Initialize the shared log/database dialect exactly as startup does,
	// while forcing the already-open disposable SQLite DB (no external DSN).
	t.Setenv("LOG_SQL_DSN", "")
	require.NoError(t, model.InitLogDB(new(model.StartupMigrationSession)))
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}, &model.DiscountCode{}, &model.DiscountCodeReservation{}, &model.ReferralReward{}, &model.ReferralLedgerEntry{}, &model.SubscriptionOrder{}, &model.FinanceLedgerEntry{}))
	require.NoError(t, model.DB.Create(&model.User{Id: 1, Username: "inviter", Password: "password", AffCode: "stripe-fixture-inviter", Email: "inviter@example.com", Role: common.RoleCommonUser, Status: common.UserStatusEnabled, Group: "default"}).Error)
	require.NoError(t, model.DB.Model(&model.User{}).Where("id=7").Updates(map[string]any{"email": "payer@example.com", "inviter_id": 1, "created_at": 1}).Error)
	common.QuotaForInviter = 75
	operation_setting.PayMethods = []map[string]string{{"type": "alipay"}}
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
	operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{}
	require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
	setting.StripeApiSecret = "sk_fixture"
	setting.StripeWebhookSecret = "whsec_fixture"
	setting.StripePriceId = "price_fixture"
	setting.StripeMinTopUp = 1
	setting.StripePromotionCodesEnabled = true
	system_setting.ServerAddress = "https://console.example"
	var mutex sync.Mutex
	var checkoutFields map[string]string
	var persistedBeforeCheckout bool
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/v1/prices/price_fixture" {
			_, _ = w.Write([]byte(`{"id":"price_fixture","currency":"usd","product":"prod_fixture"}`))
			return
		}
		if r.URL.Path != "/v1/checkout/sessions" {
			w.WriteHeader(404)
			return
		}
		require.NoError(t, r.ParseForm())
		fields := map[string]string{}
		for key := range r.PostForm {
			fields[key] = r.PostForm.Get(key)
		}
		var count int64
		require.NoError(t, model.DB.Model(&model.TopUp{}).Where("trade_no=? AND status=? AND expected_amount_micros>0 AND credited_quota>0", fields["client_reference_id"], "pending").Count(&count).Error)
		mutex.Lock()
		checkoutFields = fields
		persistedBeforeCheckout = count == 1
		mutex.Unlock()
		minor, _ := strconv.ParseInt(fields["line_items[0][price_data][unit_amount]"], 10, 64)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "cs_fixture", "url": "https://checkout.stripe.test/cs_fixture", "amount_subtotal": minor})
	}))
	t.Cleanup(provider.Close)
	stripe.SetBackend(stripe.APIBackend, stripe.GetBackendWithConfig(stripe.APIBackend, &stripe.BackendConfig{URL: stripe.String(provider.URL), HTTPClient: provider.Client()}))
	// 14.600001 legacy batches produce 7,300,000.5 credits. The shared
	// Stripe quote must floor at the ledger boundary, never grant the half.
	quoteWriter := httptest.NewRecorder()
	quoteContext, _ := gin.CreateTestContext(quoteWriter)
	quoteContext.Set("id", 7)
	quoteContext.Request = httptest.NewRequest("POST", "/api/user/stripe/amount", strings.NewReader(`{"amount":14.600001}`))
	quoteContext.Request.Header.Set("Content-Type", "application/json")
	RequestStripeAmount(quoteContext)
	require.JSONEq(t, `{
		"message":"success","data":"14.60","amount_unit":"LEGACY","currency_unit":"credit",
		"credit_amount":7300000,"credited_quota":7300000,"legacy_batch_units":"14.600001","settlement_currency":"USD",
		"credit_amount_unit":"LEDGER_QUOTA","credit_unit_schema_version":2,"quota_unit":"LEDGER_QUOTA",
		"public_credit_unit":"CREDIT","legacy_credit_unit":"LEDGER_QUOTA","ledger_quota_per_usd":500000,
		"ledger_quota_per_usd_exact":"500000","public_credits_per_usd":500000,"public_credits_per_usd_exact":"500000",
		"public_credit_amount":"7300000","public_credit_amount_unit":"CREDIT","public_credit_metadata_version":2
	}`, quoteWriter.Body.String())
	var quoteResponse map[string]any
	require.NoError(t, json.Unmarshal(quoteWriter.Body.Bytes(), &quoteResponse))
	requestBody := `{"amount":14.6,"payment_method":"stripe"}`
	writer := httptest.NewRecorder()
	ctx, _ := gin.CreateTestContext(writer)
	ctx.Set("id", 7)
	ctx.Request = httptest.NewRequest("POST", "/api/user/stripe/pay", strings.NewReader(requestBody))
	ctx.Request.Header.Set("Content-Type", "application/json")
	RequestStripePay(ctx)
	var response map[string]any
	require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &response))
	require.Equal(t, "success", response["message"], response)
	responseData := response["data"].(map[string]any)
	require.Equal(t, "LEGACY", responseData["amount_unit"])
	require.Equal(t, "credit", responseData["currency_unit"])
	require.Equal(t, "14.6", responseData["legacy_batch_units"])
	require.Equal(t, "USD", responseData["settlement_currency"])
	require.Equal(t, float64(7300000), responseData["credited_quota"])
	require.Equal(t, float64(7300000), responseData["credit_amount"])
	var order model.TopUp
	require.NoError(t, model.DB.First(&order).Error)
	require.EqualValues(t, 14600000, order.PlatformAmountMicros)
	require.EqualValues(t, 7300000, order.CreditedQuota)
	require.EqualValues(t, 14600000, order.ExpectedAmountMicros)
	mutex.Lock()
	fields := checkoutFields
	persisted := persistedBeforeCheckout
	mutex.Unlock()
	fields["client_reference_id"] = "<order>"
	require.Equal(t, "1460", fields["line_items[0][price_data][unit_amount]"])
	paid := map[string]any{"id": "evt_paid", "type": "checkout.session.completed", "data": map[string]any{"object": map[string]any{"id": "cs_fixture", "client_reference_id": order.TradeNo, "status": "complete", "payment_status": "paid", "currency": "usd", "amount_subtotal": 1460, "amount_total": 1168, "payment_intent": "pi_fixture", "customer": "cus_fixture", "customer_details": map[string]string{"email": "payer-contact@example.net"}}}}
	deliver := func(event any, secret string) int {
		raw, err := json.Marshal(event)
		require.NoError(t, err)
		signed := webhook.GenerateTestSignedPayload(&webhook.UnsignedPayload{Payload: raw, Secret: secret, Timestamp: time.Now()})
		writer := httptest.NewRecorder()
		ctx, _ := gin.CreateTestContext(writer)
		ctx.Request = httptest.NewRequest("POST", "/api/stripe/webhook", strings.NewReader(string(raw)))
		ctx.Request.Header.Set("Stripe-Signature", signed.Header)
		StripeWebhook(ctx)
		return writer.Code
	}
	statuses := []int{deliver(paid, "wrong-secret"), deliver(paid, "whsec_fixture"), deliver(paid, "whsec_fixture")}
	require.Equal(t, []int{400, 200, 200}, statuses)
	var payer model.User
	require.NoError(t, model.DB.First(&payer, 7).Error)
	walletAfterPayment := payer.Quota
	require.EqualValues(t, 7300000, walletAfterPayment)
	var topupLogs int64
	require.NoError(t, model.DB.Model(&model.Log{}).Where("type=1").Count(&topupLogs).Error)
	refund := func(id string, amount int) map[string]any {
		return map[string]any{"id": "evt_" + id, "type": "refund.updated", "data": map[string]any{"object": map[string]any{"id": id, "payment_intent": "pi_fixture", "status": "succeeded", "currency": "usd", "amount": amount}}}
	}
	refundStatuses := []int{deliver(refund("re_partial", 365), "whsec_fixture"), deliver(refund("re_partial", 365), "whsec_fixture")}
	require.NoError(t, model.DB.First(&payer, 7).Error)
	partialQuota := payer.Quota
	require.EqualValues(t, 5018750, partialQuota)
	refundStatuses = append(refundStatuses, deliver(refund("re_final", 803), "whsec_fixture"))
	require.NoError(t, model.DB.First(&payer, 7).Error)
	require.NoError(t, model.DB.First(&order, order.Id).Error)
	require.EqualValues(t, 0, payer.Quota)
	require.EqualValues(t, 11680000, order.SettledAmountMicros)
	require.EqualValues(t, 11680000, order.RefundedAmountMicros)
	require.EqualValues(t, 7300000, order.RefundedQuota)
	var inviter model.User
	require.NoError(t, model.DB.First(&inviter, 1).Error)
	var ledgers, refundLogs int64
	require.NoError(t, model.DB.Model(&model.FinanceLedgerEntry{}).Count(&ledgers).Error)
	require.NoError(t, model.DB.Model(&model.Log{}).Where("type=6").Count(&refundLogs).Error)
	goldenPayload := `{"id":"evt_fixed","type":"checkout.session.completed","data":{"object":{"id":"cs_fixed"}}}`
	goldenTimestamp := time.Unix(1700000000, 0)
	result := map[string]any{"quote_response": quoteResponse, "currency_options": currencyOptions, "request": json.RawMessage(requestBody), "response": response, "checkout_fields": fields, "persisted_before_checkout": persisted, "amount": order.Amount, "platform_amount_micros": order.PlatformAmountMicros, "credited_quota": order.CreditedQuota, "expected_amount_micros": order.ExpectedAmountMicros, "settled_amount_micros": order.SettledAmountMicros, "currency": order.SettlementCurrency, "callback_statuses": statuses, "wallet_after_payment": walletAfterPayment, "topup_logs": topupLogs, "refund_statuses": refundStatuses, "partial_wallet": partialQuota, "final_wallet": payer.Quota, "refunded_amount_micros": order.RefundedAmountMicros, "refunded_quota": order.RefundedQuota, "inviter_aff_quota": inviter.AffQuota, "refund_ledger_count": ledgers, "refund_logs": refundLogs, "payer_email": payer.Email, "stripe_customer": payer.StripeCustomer, "signature_payload": goldenPayload, "signature_header": fmt.Sprintf("t=%d,v1=%s", goldenTimestamp.Unix(), hex.EncodeToString(webhook.ComputeSignature(goldenTimestamp, []byte(goldenPayload), "whsec_fixture")))}
	encoded, err := json.MarshalIndent(result, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(output, append(encoded, '\n'), 0600))
}
