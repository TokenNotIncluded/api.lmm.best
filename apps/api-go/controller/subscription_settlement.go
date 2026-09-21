package controller

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// subscriptionSettlementSnapshot converts a real ISO-fiat plan price to a
// provider settlement currency. It deliberately never applies the wallet
// recharge purchase ratio B.
func subscriptionSettlementSnapshot(plan *model.SubscriptionPlan, settlementCurrency string) (int64, string, error) {
	if plan == nil || plan.PriceAmount <= 0 {
		return 0, "", fmt.Errorf("subscription plan price must be positive")
	}
	planCurrency := strings.ToUpper(strings.TrimSpace(plan.Currency))
	if planCurrency == "" {
		planCurrency = paymentpricing.CurrencyCNY
	}
	currency := strings.ToUpper(strings.TrimSpace(settlementCurrency))
	if currency == "" {
		return 0, "", fmt.Errorf("subscription settlement currency is required")
	}
	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return 0, "", err
	}
	converted, err := rates.ConvertFiat(decimal.NewFromFloat(plan.PriceAmount), planCurrency, currency)
	if err != nil {
		return 0, "", err
	}
	// USD/CNY providers settle in two decimal places. Persist the rounded
	// boundary amount so checkout creation and webhook validation use the exact
	// same value (for example 1 CNY / 6.8 rounds to 0.15 USD).
	converted = converted.Round(2)
	amountMicros, err := decimalToMonetaryMicros(converted)
	if err != nil || amountMicros <= 0 {
		return 0, "", fmt.Errorf("invalid subscription settlement amount")
	}
	return amountMicros, currency, nil
}

const waffoPancakeUnsupportedSettlementCurrency = "unsupported_settlement_currency"

func waffoPancakeCheckoutCurrency(c *gin.Context, user *model.User, languageHint string) string {
	if strings.TrimSpace(languageHint) == "" {
		languageHint = c.GetHeader("Accept-Language")
	}
	return userSettlementCurrency(user, languageHint)
}

// Quote amounts are bounded plain decimal fiat, never exponents or client FX.
var waffoPancakeExpectedAmountPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]{1,2})?$`)

func waffoPancakeExpectedQuoteMatches(expectedCurrency, expectedAmount json.RawMessage, currency string, amount decimal.Decimal) bool {
	if expectedCurrency == nil && expectedAmount == nil {
		return true // Legacy callers omitted both guards, not explicit nulls.
	}
	if len(expectedCurrency) > 32 || len(expectedAmount) > 128 {
		return false
	}
	var quotedCurrency, quotedAmount string
	if json.Unmarshal(expectedCurrency, &quotedCurrency) != nil || quotedCurrency != currency ||
		json.Unmarshal(expectedAmount, &quotedAmount) != nil {
		return false
	}
	if len(quotedAmount) > 24 || !waffoPancakeExpectedAmountPattern.MatchString(quotedAmount) {
		return false
	}
	parsed, err := decimal.NewFromString(quotedAmount)
	return err == nil && parsed.IsPositive() && parsed.Equal(amount)
}

func waffoPancakeCheckoutError(c *gin.Context, code, message string) {
	c.JSON(http.StatusOK, gin.H{"success": false, "message": message, "data": message, "code": code})
}

func requireWaffoPancakeExpectedQuote(c *gin.Context, expectedCurrency, expectedAmount json.RawMessage, currency string, amount decimal.Decimal) bool {
	if waffoPancakeExpectedQuoteMatches(expectedCurrency, expectedAmount, currency, amount) {
		return true
	}
	waffoPancakeCheckoutError(c, "SETTLEMENT_QUOTE_CHANGED", "结算报价已更新，请刷新后重试")
	return false
}

func subscriptionWaffoPancakeSettlementQuote(plan *model.SubscriptionPlan, currency string) *WaffoPancakeSettlementQuote {
	quote := &WaffoPancakeSettlementQuote{Currency: currency}
	micros, _, err := subscriptionSettlementSnapshot(plan, currency)
	if err != nil {
		quote.Reason = "invalid_settlement_quote"
		return quote
	}
	quote.Amount = decimal.NewFromInt(micros).Shift(-6).StringFixed(2)
	if !service.WaffoPancakeSupportsSettlementCurrency(currency, model.NormalizeWaffoPancakeProductType(plan.WaffoPancakeProductType)) {
		quote.Reason = waffoPancakeUnsupportedSettlementCurrency
		return quote
	}
	quote.Available = true
	return quote
}
