package controller

import (
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const topUpQuoteBasisKey = "topup_quote_basis"

type topUpQuoteBasis struct {
	original decimal.Decimal
	paid     decimal.Decimal
	currency string
}

func recordTopUpQuoteBasis(c *gin.Context, original, paid decimal.Decimal, currency string) {
	c.Set(topUpQuoteBasisKey, topUpQuoteBasis{original: original, paid: paid, currency: strings.ToUpper(strings.TrimSpace(currency))})
}

func updateTopUpQuotePaid(c *gin.Context, paid decimal.Decimal) {
	if value, exists := c.Get(topUpQuoteBasisKey); exists {
		if quote, ok := value.(topUpQuoteBasis); ok {
			quote.paid = paid
			c.Set(topUpQuoteBasisKey, quote)
		}
	}
}

// The declared basis is the same account and payment pricing before amount
// presets/coupons. Group pricing and payment fees stay in both amounts. Only a
// successful monetary preview can publish this optional marketing breakdown;
// a display conversion, checkout URL or an unverified amount is not a quote.
func addTopUpSettlementQuote(c *gin.Context, response gin.H, currency string) {
	value, exists := c.Get(topUpQuoteBasisKey)
	quote, ok := value.(topUpQuoteBasis)
	if !exists || !ok || !quote.paid.IsPositive() {
		return
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if quote.currency != currency || (currency != "USD" && currency != "CNY") {
		return
	}
	actual, ok := response["data"].(string)
	if !ok || actual != quote.paid.StringFixed(2) {
		return
	}
	original, savings, discounted := settlementQuoteSavings(quote.original, quote.paid)
	if !discounted || !original.IsPositive() {
		return
	}
	percent := savings.Mul(decimal.NewFromInt(100)).Div(original)
	percentText := percent.StringFixed(2)
	if percent.IsPositive() && percent.Round(2).IsZero() {
		percentText = percent.String()
	}
	response["settlement_quote"] = gin.H{
		"schema_version":   1,
		"currency":         currency,
		"original_amount":  original.StringFixed(2),
		"paid_amount":      quote.paid.StringFixed(2),
		"savings_amount":   savings.StringFixed(2),
		"discount_percent": percentText,
		"basis":            "amount_preset_and_code",
	}
}
