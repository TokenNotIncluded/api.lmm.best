package controller

import (
	"fmt"
	"math"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
)

// topUpCreditDiscounts is the one adapter for persisted amount-discount keys.
// Both public catalog metadata and canonical quotes use this validated map of
// actual ledger credits. The persisted configuration is not rewritten.
func topUpCreditDiscounts(config operation_setting.PaymentAmountDiscount, tokens bool) (map[string]float64, error) {
	discounts := make(map[string]float64, len(config))
	for amount, discount := range config {
		value, err := operation_setting.ParsePaymentDiscountAmount(amount)
		if err != nil {
			return nil, err
		}
		credits, err := operation_setting.PaymentConfigAmountCredit(value, tokens)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(discount) || math.IsInf(discount, 0) || discount <= 0 {
			return nil, fmt.Errorf("invalid top-up discount for catalog amount %s", amount)
		}
		key := strconv.FormatInt(credits, 10)
		if previous, exists := discounts[key]; exists && previous != discount {
			return nil, fmt.Errorf("conflicting top-up discounts for credit amount %s", key)
		}
		discounts[key] = discount
	}
	return discounts, nil
}
