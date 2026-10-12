package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/config"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

const (
	cnyFixedAnchorFX       = "6.714466"
	cnyFixedAnchorOptions  = `[1,2,3.5,5,10,20,50,100]`
	cnyFixedAnchorDiscount = `{"1":1,"2":0.99,"3.5":0.98,"5":0.97,"10":0.96,"20":0.94,"50":0.92,"100":0.9}`
	cnyFixedAnchorUserID   = 808
	cnyFixedAnchorInitial  = 123
)

type cnyFixedAnchorNoHTTP struct{ attempts atomic.Int64 }

func (guard *cnyFixedAnchorNoHTTP) RoundTrip(*http.Request) (*http.Response, error) {
	guard.attempts.Add(1)
	return nil, errors.New("fixed-anchor recharge fixture forbids outbound HTTP")
}

// This fixture uses in-memory SQLite, httptest handlers, a synthetic Epay key,
// and local SDK form construction. It never loads a database DSN. The Pancake
// quote handler is covered, but its provider checkout/webhook and real merchant
// behavior are not. Global-setting fixtures must not run with t.Parallel.
func setupCNYFixedAnchorRecharge(t *testing.T, display string, discounts bool) {
	t.Helper()
	publicTopUpCreditTestConfig(t, "500000")
	setupSettlementQuoteUser(t, cnyFixedAnchorUserID)
	preservePaymentGatewaySettings(t)
	sqlDB, err := model.DB.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	oldPrice, oldTransport := operation_setting.Price, http.DefaultTransport
	guard := &cnyFixedAnchorNoHTTP{}
	http.DefaultTransport = guard
	operation_setting.Price = 1 // Retained legacy setting, not the CNY/USD FX.
	t.Cleanup(func() {
		operation_setting.Price, http.DefaultTransport = oldPrice, oldTransport
		require.Zero(t, guard.attempts.Load(), "quote, checkout, and callback must stay local")
	})

	operation_setting.GetGeneralSetting().QuotaDisplayType = display
	operation_setting.USDExchangeRate = 6.714466
	operation_setting.PayAddress = "http://127.0.0.1:1"
	operation_setting.EpayId, operation_setting.EpayKey = "fixed-anchor-fixture", "fixed-anchor-local-key" // gitleaks:allow
	operation_setting.PayMethods = []map[string]string{
		{"type": "alipay"}, {"type": model.PaymentMethodWaffoPancake},
		{"type": "epay", "settlement_unit": "LDC", "unit_price": "10"},
	}
	if discounts {
		require.NoError(t, config.UpdateConfigFromMap(operation_setting.GetPaymentSetting(), map[string]string{
			"amount_options": cnyFixedAnchorOptions, "amount_discount": cnyFixedAnchorDiscount,
		}))
	}
	require.NoError(t, model.DB.AutoMigrate(&model.Log{}))
	require.NoError(t, model.DB.Model(&model.User{}).Where("id = ?", cnyFixedAnchorUserID).Updates(map[string]any{
		"quota": cnyFixedAnchorInitial, "setting": `{"settlement_currency":"CNY","wallet_display_currency":"CNY"}`,
	}).Error)
	for _, getter := range []func() (decimal.Decimal, error){common.CreditsPerUSD, common.LegacyPricingQuotaPerUnit, common.PublicCreditsPerUSD} {
		value, err := getter()
		require.NoError(t, err)
		require.Equal(t, "500000", value.String())
	}
	require.Equal(t, float64(500000), common.QuotaPerUnit)
}

func cnyFixedAnchorBody(quota int64, method string) string {
	return fmt.Sprintf(`{"amount":%d,"amount_unit":"LEDGER_QUOTA","credit_metadata_version":2,"payment_method":%q}`, quota, method)
}

func cnyFixedAnchorCallback(t *testing.T, order model.TopUp, money string, tamper bool) *httptest.ResponseRecorder {
	t.Helper()
	params := epay.GenerateParams(map[string]string{
		"pid": operation_setting.EpayId, "type": order.PaymentMethod,
		"out_trade_no": order.TradeNo, "trade_no": "synthetic-fixed-anchor-transaction",
		"money": money, "trade_status": epay.StatusTradeSuccess,
	}, operation_setting.EpayKey)
	if tamper {
		params["money"] = "0.01" // Change the body after the signature was produced.
	}
	form := url.Values{}
	for key, value := range params {
		form.Set(key, value)
	}
	router := gin.New()
	router.POST("/api/user/epay/notify", EpayNotify)
	request := httptest.NewRequest(http.MethodPost, "/api/user/epay/notify", strings.NewReader(form.Encode()))
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestFixedAnchorCNYRechargeQuoteCheckoutSignedCallbackAndReplay(t *testing.T) {
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY} {
		for _, tc := range []struct {
			name, inputCNY, configAmount, faceCNY, cash, ldc string
			quota                                            int64
			discounts                                        bool
		}{
			{"neutral 100 CNY", "100", "14.893216", "99.999992462656", "100.00", "148.93", 7446608, false},
			{"100 CNY with retained catalog", "100", "14.893216", "99.999992462656", "100.00", "148.93", 7446608, true},
			{"retained 100 USD preset", "671.4466", "100", "671.4466", "604.30", "900.00", 50000000, true},
		} {
			t.Run(display+"/"+tc.name, func(t *testing.T) {
				setupCNYFixedAnchorRecharge(t, display, tc.discounts)
				resolved, err := resolveTopUpDecimalAmount(decimal.RequireFromString(tc.inputCNY), "CNY")
				require.NoError(t, err)
				require.Equal(t, tc.quota, resolved.CreditedQuota)
				require.Equal(t, tc.configAmount, topUpConfigAmountFromResolved(resolved).String())
				face, err := common.CreditsToFiat(tc.quota, "CNY", decimal.RequireFromString(cnyFixedAnchorFX))
				require.NoError(t, err)
				require.Equal(t, tc.faceCNY, face.String())
				if tc.inputCNY == "100" {
					require.Equal(t, "100.00", face.StringFixed(2))
					require.Less(t, decimal.RequireFromString("100").Sub(face).InexactFloat64(), 6.714466/500000)
					require.NotContains(t, operation_setting.GetPaymentSetting().AmountDiscount, tc.configAmount,
						"100 CNY is not the retained 100 USD preset")
				}

				body := cnyFixedAnchorBody(tc.quota, "alipay")
				type quoteResponse struct {
					Message, Data      string
					Credits            int64  `json:"credited_quota"`
					SettlementCurrency string `json:"settlement_currency"`
				}
				var quoted quoteResponse
				quote := publicCreditRequest(t, RequestAmount, cnyFixedAnchorUserID, body)
				require.Equal(t, http.StatusOK, quote.Code)
				require.NoError(t, json.Unmarshal(quote.Body.Bytes(), &quoted))
				require.Equal(t, "success", quoted.Message, quote.Body.String())
				require.Equal(t, tc.cash, quoted.Data)
				require.Equal(t, tc.quota, quoted.Credits)
				require.Equal(t, "CNY", quoted.SettlementCurrency)

				// The production fiat rail shares the same fixed-anchor quote. No
				// Pancake checkout or external merchant request is made here.
				pancake := publicCreditRequest(t, RequestWaffoPancakeAmount, cnyFixedAnchorUserID, cnyFixedAnchorBody(tc.quota, model.PaymentMethodWaffoPancake))
				require.Equal(t, http.StatusOK, pancake.Code)
				quoted = quoteResponse{}
				require.NoError(t, json.Unmarshal(pancake.Body.Bytes(), &quoted))
				require.Equal(t, "success", quoted.Message, pancake.Body.String())
				require.Equal(t, tc.cash, quoted.Data)
				require.Equal(t, tc.quota, quoted.Credits)
				require.Equal(t, "CNY", quoted.SettlementCurrency)

				// The other observed rail quotes virtual LDC units. Its unit_price
				// must not be interpreted as CNY or change the requested grant.
				ldc := publicCreditRequest(t, RequestAmount, cnyFixedAnchorUserID, cnyFixedAnchorBody(tc.quota, "epay"))
				require.Equal(t, http.StatusOK, ldc.Code)
				quoted = quoteResponse{}
				require.NoError(t, json.Unmarshal(ldc.Body.Bytes(), &quoted))
				require.Equal(t, "success", quoted.Message, ldc.Body.String())
				require.Equal(t, tc.ldc, quoted.Data)
				require.Equal(t, tc.quota, quoted.Credits)
				require.Equal(t, "LDC", quoted.SettlementCurrency)

				var opened struct {
					Message string
					Data    map[string]string
					Credits int64 `json:"credited_quota"`
				}
				checkout := publicCreditRequest(t, RequestEpay, cnyFixedAnchorUserID, body)
				require.Equal(t, http.StatusOK, checkout.Code)
				require.NoError(t, json.Unmarshal(checkout.Body.Bytes(), &opened))
				require.Equal(t, "success", opened.Message, checkout.Body.String())
				require.Equal(t, tc.cash, opened.Data["money"])
				require.Equal(t, tc.quota, opened.Credits)
				var order model.TopUp
				require.NoError(t, model.DB.Where("trade_no = ?", opened.Data["out_trade_no"]).First(&order).Error)
				require.Equal(t, tc.quota, order.CreditedQuota)
				require.Equal(t, "CNY", order.SettlementCurrency)
				micros, err := monetaryStringToMicros(tc.cash)
				require.NoError(t, err)
				require.Equal(t, micros, order.ExpectedAmountMicros)

				assertQuota := func(expected int64) {
					t.Helper()
					quota, err := model.GetUserQuota(cnyFixedAnchorUserID, true)
					require.NoError(t, err)
					require.EqualValues(t, expected, quota)
				}
				tampered := cnyFixedAnchorCallback(t, order, tc.cash, true)
				require.Equal(t, "fail", tampered.Body.String())
				mismatch := cnyFixedAnchorCallback(t, order, "0.01", false)
				require.Equal(t, "fail", mismatch.Body.String(), "a valid signature cannot change the frozen payment amount")
				assertQuota(cnyFixedAnchorInitial)

				// The grant and collected amount are frozen, even if later quotes
				// would have different FX, purchase bonuses, or pricing multipliers.
				operation_setting.USDExchangeRate, operation_setting.TopUpPlatformUnitsPerCNY = 8.2, 99
				operation_setting.GetPaymentSetting().AmountDiscount = operation_setting.PaymentAmountDiscount{tc.configAmount: 0.01}
				operation_setting.PayMethods[0]["topup_ratio"] = "1.3"
				require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":0.8}`))
				for replay := 0; replay < 2; replay++ {
					response := cnyFixedAnchorCallback(t, order, tc.cash, false)
					require.Equal(t, "success", response.Body.String())
					assertQuota(cnyFixedAnchorInitial + tc.quota)
				}
				require.NoError(t, model.DB.First(&order, order.Id).Error)
				require.Equal(t, common.TopUpStatusSuccess, order.Status)
				require.Equal(t, tc.quota, order.CreditedQuota)
				require.Equal(t, micros, order.ExpectedAmountMicros)
				require.Equal(t, micros, order.SettledAmountMicros)
			})
		}
	}
}

func TestFixedAnchorCNYRetainedCatalogUnits(t *testing.T) {
	publicTopUpCreditTestConfig(t, "500000")
	require.NoError(t, config.UpdateConfigFromMap(operation_setting.GetPaymentSetting(), map[string]string{
		"amount_options": cnyFixedAnchorOptions, "amount_discount": cnyFixedAnchorDiscount,
	}))
	for _, display := range []string{operation_setting.QuotaDisplayTypeUSD, operation_setting.QuotaDisplayTypeCNY} {
		operation_setting.GetGeneralSetting().QuotaDisplayType = display
		metadata, err := topUpCreditMetadata(nil)
		require.NoError(t, err)
		require.Equal(t, []int64{500000, 1000000, 1750000, 2500000, 5000000, 10000000, 25000000, 50000000}, metadata["credit_amount_options"])
		discounts := metadata["credit_discount"].(map[string]float64)
		require.Equal(t, 0.9, discounts["50000000"])
		require.NotContains(t, discounts, "7446608")
	}
	operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeTokens
	_, err := topUpCreditMetadata(nil)
	require.Error(t, err, "retained fractional batch 3.5 is not an integer raw-credit TOKENS preset")
}
