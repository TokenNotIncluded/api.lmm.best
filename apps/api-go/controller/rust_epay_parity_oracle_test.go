package controller

// This oracle executes the CURRENT Go handlers on the same fixtures consumed
// by Rust's PostgreSQL test. It is intentionally separate from the frozen
// legacy behavior oracle and never reads deployed database credentials.

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type rustEpayFixture struct {
	Name    string              `json:"name"`
	Amount  string              `json:"amount"`
	Method  string              `json:"method"`
	Body    string              `json:"body"`
	Options map[string]string   `json:"options"`
	Methods []map[string]string `json:"methods"`
}

func TestRustEpayCurrentGoOracle(t *testing.T) {
	outputPath := os.Getenv("LMM_EPAY_GO_ORACLE_OUTPUT")
	if outputPath == "" {
		t.Skip("set LMM_EPAY_GO_ORACLE_OUTPUT to export current-Go ePay reference evidence")
	}
	fixturePath := os.Getenv("LMM_EPAY_PARITY_FIXTURES")
	if fixturePath == "" {
		fixturePath = "../../api-rust/tests/fixtures/epay-current-go-input.json"
	}
	raw, err := os.ReadFile(fixturePath)
	require.NoError(t, err)
	var fixtures []rustEpayFixture
	require.NoError(t, json.Unmarshal(raw, &fixtures))
	gin.SetMode(gin.TestMode)
	results := make(map[string]any)
	for _, fixture := range fixtures {
		t.Run(fixture.Name, func(t *testing.T) {
			preserveChannelPricing(t)
			setupTopupInfoUser(t, 7, "default")
			previousLogDB := model.LOG_DB
			previousQuota := common.QuotaPerUnit
			previousInviter := common.QuotaForInviter
			previousAddress, previousID, previousKey := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey
			previousCallback, previousServer := operation_setting.CustomCallbackAddress, system_setting.ServerAddress
			previousMinimum := operation_setting.MinTopUp
			previousCompliance, previousTerms := operation_setting.GetPaymentSetting().ComplianceConfirmed, operation_setting.GetPaymentSetting().ComplianceTermsVersion
			t.Cleanup(func() {
				model.LOG_DB = previousLogDB
				common.QuotaPerUnit = previousQuota
				common.QuotaForInviter = previousInviter
				operation_setting.PayAddress = previousAddress
				operation_setting.EpayId = previousID
				operation_setting.EpayKey = previousKey
				operation_setting.CustomCallbackAddress = previousCallback
				system_setting.ServerAddress = previousServer
				operation_setting.MinTopUp = previousMinimum
				operation_setting.GetPaymentSetting().ComplianceConfirmed = previousCompliance
				operation_setting.GetPaymentSetting().ComplianceTermsVersion = previousTerms
			})
			model.LOG_DB = model.DB
			require.NoError(t, model.DB.AutoMigrate(&model.Log{}, &model.DiscountCode{}, &model.DiscountCodeReservation{}, &model.ReferralReward{}, &model.ReferralLedgerEntry{}))
			require.NoError(t, model.DB.Model(&model.User{}).Where("id=7").Updates(map[string]any{"email": "payer@example.com", "created_at": 1}).Error)
			common.QuotaPerUnit = 500000
			common.QuotaForInviter = 0
			operation_setting.MinTopUp = 1
			operation_setting.USDExchangeRate = 7.3
			operation_setting.TopUpPlatformUnitsPerCNY = 2
			operation_setting.PayAddress = "https://pay.example/gateway"
			operation_setting.EpayId = "fixture-merchant"
			operation_setting.EpayKey = "fixture-key"
			operation_setting.CustomCallbackAddress = ""
			system_setting.ServerAddress = "https://console.example"
			operation_setting.PayMethods = fixture.Methods
			operation_setting.GetGeneralSetting().QuotaDisplayType = operation_setting.QuotaDisplayTypeUSD
			operation_setting.GetPaymentSetting().AmountDiscount = map[int]float64{}
			operation_setting.GetPaymentSetting().ComplianceConfirmed = true
			operation_setting.GetPaymentSetting().ComplianceTermsVersion = "v1"
			require.NoError(t, common.UpdateTopupGroupRatioByJSONString(`{"default":1}`))
			for key, value := range fixture.Options {
				switch key {
				case "general_setting.quota_display_type":
					operation_setting.GetGeneralSetting().QuotaDisplayType = value
				case "payment_setting.amount_discount":
					require.NoError(t, json.Unmarshal([]byte(value), &operation_setting.GetPaymentSetting().AmountDiscount))
				default:
					t.Fatalf("unsupported reference fixture option: %s", key)
				}
			}
			writer := httptest.NewRecorder()
			ctx, _ := gin.CreateTestContext(writer)
			body := fixture.Body
			if body == "" {
				body = fmt.Sprintf(`{"amount":%s,"payment_method":%q}`, fixture.Amount, fixture.Method)
			}
			ctx.Request = httptest.NewRequest("POST", "/api/user/pay", strings.NewReader(body))
			ctx.Request.Header.Set("Content-Type", "application/json")
			ctx.Set("id", 7)
			RequestEpay(ctx)
			require.Equal(t, 200, writer.Code)
			var response map[string]any
			require.NoError(t, json.Unmarshal(writer.Body.Bytes(), &response))
			if response["message"] != "success" {
				var count int64
				require.NoError(t, model.DB.Model(&model.TopUp{}).Count(&count).Error)
				results[fixture.Name] = map[string]any{"response": response, "order_count": count}
				return
			}
			data := response["data"].(map[string]any)
			trade := data["out_trade_no"].(string)
			order := model.GetTopUpByTradeNo(trade)
			require.NotNil(t, order)
			checkoutFields := map[string]string{}
			for key, value := range data {
				checkoutFields[key] = value.(string)
			}
			verify, err := GetEpayClient().Verify(checkoutFields)
			require.NoError(t, err)
			notify := func(money string, tampered bool) string {
				fields := epay.GenerateParams(map[string]string{"out_trade_no": trade, "trade_no": "paid-" + trade, "type": fixture.Method, "money": money, "trade_status": epay.StatusTradeSuccess}, "fixture-key")
				if tampered {
					fields["sign"] = "invalid"
				}
				query := url.Values{}
				for key, value := range fields {
					query.Set(key, value)
				}
				writer := httptest.NewRecorder()
				ctx, _ := gin.CreateTestContext(writer)
				ctx.Request = httptest.NewRequest("GET", "/api/user/epay/notify?"+query.Encode(), nil)
				EpayNotify(ctx)
				return writer.Body.String()
			}
			money := data["money"].(string)
			acks := []string{notify(money, true), notify("0.01", false), notify(money, false), notify(money, false)}
			var quota int64
			require.NoError(t, model.DB.Model(&model.User{}).Select("quota").Where("id=7").Scan(&quota).Error)
			var logCount int64
			require.NoError(t, model.DB.Model(&model.Log{}).Where("type=?", model.LogTypeTopup).Count(&logCount).Error)
			results[fixture.Name] = map[string]any{"money": money, "name": data["name"], "url": response["url"], "currency": order.SettlementCurrency, "amount": order.Amount, "platform_amount_micros": order.PlatformAmountMicros, "credited_quota": order.CreditedQuota, "signature_valid": verify.VerifyStatus, "acks": acks, "wallet_quota": quota, "topup_logs": logCount}
		})
	}
	// One deterministic signed notification proves both implementations consume
	// the same original fields, rather than only signing their own output.
	fields := epay.GenerateParams(map[string]string{"money": "2.62", "out_trade_no": "USR7NOfixed", "trade_no": "provider-fixed", "trade_status": epay.StatusTradeSuccess, "type": "alipay"}, "fixture-key")
	results["notification_signature"] = fields["sign"]
	results["fixture_count"] = strconv.Itoa(len(fixtures))
	encoded, err := json.MarshalIndent(results, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(outputPath, append(encoded, '\n'), 0600))
}
