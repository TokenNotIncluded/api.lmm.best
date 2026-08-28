/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.
*/

package controller

import (
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/shopspring/decimal"
)

// applyDiscountCodeQuote layers a discount code on top of the existing
// amount/group/payment pricing. The server re-runs this exact calculation at
// checkout, so the browser can only preview a price and never set the amount.
func applyDiscountCodeQuote(base decimal.Decimal, amount int64, rawCode string, userIDs ...int) (decimal.Decimal, *model.DiscountCode, error) {
	if strings.TrimSpace(rawCode) == "" {
		return base, nil, nil
	}
	userID := 0
	if len(userIDs) > 0 {
		userID = userIDs[0]
	}
	code, err := model.ValidateDiscountCodeForUser(rawCode, amount, common.GetTimestamp(), userID)
	if err != nil {
		return decimal.Zero, nil, err
	}
	multiplier := decimal.NewFromInt(int64(100 - code.DiscountPercent)).Div(decimal.NewFromInt(100))
	return base.Mul(multiplier).Round(2), code, nil
}

func discountCodeID(code *model.DiscountCode) int {
	if code == nil {
		return 0
	}
	return code.Id
}

func discountPercent(code *model.DiscountCode) int {
	if code == nil {
		return 0
	}
	return code.DiscountPercent
}

func quoteTopUpWithDiscount(amount int64, group, paymentMethod, rawCode string, userIDs ...int) (decimal.Decimal, *model.DiscountCode, error) {
	base, err := quoteTopUp(amount, group, paymentMethod)
	if err != nil {
		return decimal.Zero, nil, err
	}
	return applyDiscountCodeQuote(base, amount, rawCode, userIDs...)
}
