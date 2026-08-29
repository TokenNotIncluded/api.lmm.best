package controller

import (
	"fmt"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
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
