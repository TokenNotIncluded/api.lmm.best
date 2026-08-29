/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as
published by the Free Software Foundation, either version 3 of the
License, or (at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

const (
	paymentMethodMinTopUpKey = "min_topup"
	paymentMethodMaxTopUpKey = "max_topup"
)

// configuredPaymentMethodMinTopUp returns the strictest configured minimum
// when duplicate entries share a payment type. The amount is the credited USD
// value, matching max_topup and the payment quote shown to users.
func configuredPaymentMethodMinTopUp(paymentType string) (decimal.Decimal, bool, error) {
	paymentType = strings.TrimSpace(paymentType)
	var minimum decimal.Decimal
	configured := false

	for _, method := range operation_setting.PayMethods {
		if strings.TrimSpace(method["type"]) != paymentType {
			continue
		}

		rawMinimum, exists := method[paymentMethodMinTopUpKey]
		rawMinimum = strings.TrimSpace(rawMinimum)
		if !exists || rawMinimum == "" {
			continue
		}
		if !nonNegativeDecimalPattern.MatchString(rawMinimum) {
			return decimal.Zero, true, fmt.Errorf("payment method %q has invalid %s", paymentType, paymentMethodMinTopUpKey)
		}
		parsed, err := decimal.NewFromString(rawMinimum)
		if err != nil || parsed.IsNegative() {
			return decimal.Zero, true, fmt.Errorf("payment method %q has invalid %s", paymentType, paymentMethodMinTopUpKey)
		}
		if !configured || parsed.GreaterThan(minimum) {
			minimum = parsed
			configured = true
		}
	}

	return minimum, configured, nil
}

// configuredPaymentMethodMaxTopUp returns the most restrictive configured
// limit when duplicate catalog entries share a payment type. Checkout only
// carries the type, so choosing the smallest limit prevents a duplicate entry
// from weakening the server-side policy.
func configuredPaymentMethodMaxTopUp(paymentType string) (decimal.Decimal, bool, error) {
	paymentType = strings.TrimSpace(paymentType)
	var limit decimal.Decimal
	configured := false

	for _, method := range operation_setting.PayMethods {
		if strings.TrimSpace(method["type"]) != paymentType {
			continue
		}

		rawLimit, exists := method[paymentMethodMaxTopUpKey]
		rawLimit = strings.TrimSpace(rawLimit)
		if !exists || rawLimit == "" {
			continue
		}
		if !positiveDecimalPattern.MatchString(rawLimit) {
			return decimal.Zero, true, fmt.Errorf("payment method %q has invalid %s", paymentType, paymentMethodMaxTopUpKey)
		}
		parsed, err := decimal.NewFromString(rawLimit)
		if err != nil || !parsed.IsPositive() {
			return decimal.Zero, true, fmt.Errorf("payment method %q has invalid %s", paymentType, paymentMethodMaxTopUpKey)
		}
		if !configured || parsed.LessThan(limit) {
			limit = parsed
			configured = true
		}
	}

	return limit, configured, nil
}

func requestedTopUpUSD(amount int64) (decimal.Decimal, error) {
	return requestedTopUpUSDDecimal(decimal.NewFromInt(amount))
}

func requestedTopUpUSDDecimal(amount decimal.Decimal) (decimal.Decimal, error) {
	platformAmount := amount
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return decimal.Zero, fmt.Errorf("quota per unit must be positive")
		}
		platformAmount = platformAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return decimal.Zero, err
	}
	return rates.FiatForPlatformUnits(platformAmount, paymentpricing.CurrencyUSD)
}

func creditedQuotaUSD(quota int64) (decimal.Decimal, error) {
	if !validQuotaPerUnit() {
		return decimal.Zero, fmt.Errorf("quota per unit must be positive")
	}
	platformAmount := decimal.NewFromInt(quota).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return decimal.Zero, err
	}
	return rates.FiatForPlatformUnits(platformAmount, paymentpricing.CurrencyUSD)
}

func requirePaymentMethodTopUpWithinLimit(c *gin.Context, paymentType string, amount int64) bool {
	return requirePaymentMethodTopUpDecimalWithinLimit(c, paymentType, decimal.NewFromInt(amount))
}

func requirePaymentMethodTopUpDecimalWithinLimit(c *gin.Context, paymentType string, amount decimal.Decimal) bool {
	amountUSD, err := requestedTopUpUSDDecimal(amount)
	if err != nil {
		common.ApiErrorMsg(c, "充值汇率配置无效")
		return false
	}
	return requirePaymentMethodUSDWithinLimit(c, paymentType, amountUSD)
}

func requirePaymentMethodCreditedQuotaWithinLimit(c *gin.Context, paymentType string, quota int64) bool {
	amountUSD, err := creditedQuotaUSD(quota)
	if err != nil {
		common.ApiErrorMsg(c, "充值汇率配置无效")
		return false
	}
	return requirePaymentMethodUSDWithinLimit(c, paymentType, amountUSD)
}

// requireTopUpCreditCapacity rejects a checkout before contacting a payment
// provider when the user's wallet cannot represent the resulting balance.
// Settlement repeats this check atomically, because the balance can change
// while a checkout is open.
func requireTopUpCreditCapacity(c *gin.Context, userID int, creditedQuota int64) bool {
	err := model.ValidateTopUpQuotaCapacity(userID, creditedQuota)
	if err == nil {
		return true
	}
	message := "充值额度无效"
	if errors.Is(err, model.ErrTopUpQuotaLimitExceeded) {
		message = "充值后余额将超过账户额度上限"
	} else if errors.Is(err, model.ErrInvalidTopUpQuota) {
		message = "充值额度超出系统可表示范围"
	}
	c.JSON(http.StatusOK, gin.H{"message": "error", "data": message})
	return false
}

// requireTopUpAmountCapacity applies the same wallet ceiling check to quote
// endpoints as to checkout endpoints. Keeping the conversion here ensures a
// preview uses exactly the credited quota that the eventual order stores.
func requireTopUpAmountCapacity(c *gin.Context, userID int, amount int64) bool {
	_, creditedQuota := topUpOrderAmounts(amount)
	return requireTopUpCreditCapacity(c, userID, creditedQuota)
}

func requirePaymentMethodUSDWithinLimit(c *gin.Context, paymentType string, amount decimal.Decimal) bool {
	minimum, minimumConfigured, err := configuredPaymentMethodMinTopUp(paymentType)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return false
	}
	if minimumConfigured && amount.LessThan(minimum) {
		c.JSON(http.StatusOK, gin.H{
			"message": "error",
			"data":    fmt.Sprintf("该支付方式单笔最少充值 %s 美元到账余额", minimum.String()),
		})
		return false
	}

	limit, configured, err := configuredPaymentMethodMaxTopUp(paymentType)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return false
	}
	if !configured || !amount.GreaterThan(limit) {
		return true
	}
	c.JSON(http.StatusOK, gin.H{
		"message": "error",
		"data":    fmt.Sprintf("该支付方式单笔最多充值 %s 美元到账余额", limit.String()),
	})
	return false
}
