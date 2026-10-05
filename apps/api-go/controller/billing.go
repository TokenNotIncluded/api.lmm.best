package controller

import (
	"math"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/relaykit/types"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

func GetSubscription(c *gin.Context) {
	var remainQuota int
	var usedQuota int
	var err error
	var token *model.Token
	var expiredTime int64
	if common.DisplayTokenStatEnabled {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err != nil {
			writeBillingOpenAIError(c, err, "upstream_error")
			return
		}
		expiredTime = token.ExpiredTime
		remainQuota = token.RemainQuota
		usedQuota = token.UsedQuota
	} else {
		userId := c.GetInt("id")
		remainQuota, err = model.GetUserQuota(userId, false)
		if err != nil {
			writeBillingOpenAIError(c, err, "upstream_error")
			return
		}
		usedQuota, err = model.GetUserUsedQuota(userId)
		if err != nil {
			writeBillingOpenAIError(c, err, "upstream_error")
			return
		}
	}
	if expiredTime <= 0 {
		expiredTime = 0
	}
	anchor, err := billingCreditAnchor()
	if err != nil {
		writeBillingOpenAIError(c, err, "billing_unavailable")
		return
	}
	// OpenAI's USD fields always represent real USD, regardless of UI display.
	quota := decimal.NewFromInt(int64(remainQuota)).Add(decimal.NewFromInt(int64(usedQuota)))
	amount := float64(100000000)
	if token == nil || !token.UnlimitedQuota {
		amount, err = billingUSDFromCredits(quota, anchor)
		if err != nil {
			writeBillingOpenAIError(c, err, "billing_unavailable")
			return
		}
	}
	subscription := OpenAISubscriptionResponse{
		Object:             "billing_subscription",
		HasPaymentMethod:   true,
		SoftLimitUSD:       amount,
		HardLimitUSD:       amount,
		SystemHardLimitUSD: amount,
		AccessUntil:        expiredTime,
	}
	c.JSON(200, subscription)
	return
}

func GetUsage(c *gin.Context) {
	var quota int
	var err error
	var token *model.Token
	if common.DisplayTokenStatEnabled {
		tokenId := c.GetInt("token_id")
		token, err = model.GetTokenById(tokenId)
		if err != nil {
			writeBillingOpenAIError(c, err, "new_api_error")
			return
		}
		quota = token.UsedQuota
	} else {
		userId := c.GetInt("id")
		quota, err = model.GetUserUsedQuota(userId)
		if err != nil {
			writeBillingOpenAIError(c, err, "new_api_error")
			return
		}
	}
	anchor, err := billingCreditAnchor()
	if err != nil {
		writeBillingOpenAIError(c, err, "billing_unavailable")
		return
	}
	amount, err := billingUSDFromCredits(decimal.NewFromInt(int64(quota)), anchor)
	if err != nil {
		writeBillingOpenAIError(c, err, "billing_unavailable")
		return
	}
	amount *= 100
	if math.IsNaN(amount) || math.IsInf(amount, 0) {
		writeBillingOpenAIError(c, common.ErrCreditUnitsUnavailable, "billing_unavailable")
		return
	}
	usage := OpenAIUsageResponse{
		Object:     "list",
		TotalUsage: amount,
	}
	c.JSON(200, usage)
	return
}

// SDK dollar fields require a valid immutable basis, regardless of display.
func billingCreditAnchor() (decimal.Decimal, error) {
	if _, err := common.LegacyPricingUnitsPerUSD(); err != nil {
		return decimal.Zero, common.ErrCreditUnitsUnavailable
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return decimal.Zero, common.ErrCreditUnitsUnavailable
	}
	return anchor, nil
}

func billingUSDFromCredits(quota, anchor decimal.Decimal) (float64, error) {
	amount, _ := quota.Div(anchor).Float64()
	if math.IsNaN(amount) || math.IsInf(amount, 0) || (!quota.IsZero() && amount == 0) {
		return 0, common.ErrCreditUnitsUnavailable
	}
	return amount, nil
}

func writeBillingOpenAIError(c *gin.Context, err error, errorType string) {
	c.JSON(200, gin.H{
		"error": types.OpenAIError{
			Message: err.Error(),
			Type:    errorType,
		},
	})
}
