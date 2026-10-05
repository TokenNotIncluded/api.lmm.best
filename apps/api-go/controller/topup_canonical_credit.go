package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/shopspring/decimal"
)

const canonicalTopUpCreditKey = "canonical_topup_credit"

// RequireCanonicalTopUpCredit belongs only to the distinct currency routes.
// An older server has no such route, so raw Credits can never be interpreted
// by an older handler as legacy batches. Preserve the original JSON spelling
// before any float64 binding, including fractions beside the safe-integer cap.
func RequireCanonicalTopUpCredit(c *gin.Context) {
	reject := func() {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"success": false, "message": "error", "data": "amount 必须为正整数 Credit，amount_unit 必须为 CREDIT",
		})
	}
	if c.ContentType() != "application/json" {
		reject()
		return
	}
	var body map[string]json.RawMessage
	if err := c.ShouldBindBodyWith(&body, binding.JSON); err != nil {
		reject()
		return
	}
	var unit string
	if err := json.Unmarshal(body["amount_unit"], &unit); err != nil || unit != "CREDIT" {
		reject()
		return
	}
	raw := bytes.TrimSpace(body["amount"])
	// Canonical clients serialize a raw integer as ordinary decimal digits.
	// Bound the spelling before arithmetic: a short enormous exponent must
	// not cause a decimal library to allocate 10^exponent for comparison.
	if len(raw) == 0 || len(raw) > 16 {
		reject()
		return
	}
	for _, digit := range raw {
		if digit < '0' || digit > '9' {
			reject()
			return
		}
	}
	credits, err := strconv.ParseInt(string(raw), 10, 64)
	if err != nil || credits <= 0 || credits > common.MaxWalletQuota {
		reject()
		return
	}
	if _, err := common.LegacyPricingUnitsPerUSD(); err != nil {
		c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "error", "data": "充值额度配置无效"})
		return
	}
	resolved, err := resolveTopUpDecimalAmount(decimal.NewFromInt(credits), "CREDIT")
	if err != nil {
		reject()
		return
	}
	c.Set(canonicalTopUpCreditKey, resolved)
	c.Next()
}

func canonicalTopUpCredit(c *gin.Context) (resolvedTopUpAmount, bool) {
	value, exists := c.Get(canonicalTopUpCreditKey)
	amount, ok := value.(resolvedTopUpAmount)
	return amount, exists && ok
}

func topUpConfigAmountFromResolved(amount resolvedTopUpAmount) decimal.Decimal {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return decimal.NewFromInt(amount.CreditedQuota)
	}
	return amount.LegacyBatch
}

func quoteTopUpResolvedWithSettlementPricing(amount resolvedTopUpAmount, group string, pricing payMethodSettlementPricing, ratio decimal.Decimal) (decimal.Decimal, error) {
	if amount.CreditedQuota <= 0 || !validQuotaPerUnit() {
		return decimal.Zero, errors.New("invalid credit amount")
	}
	credits := decimal.NewFromInt(amount.CreditedQuota)
	var settlement decimal.Decimal
	if pricing.usesFixedCreditDenomination {
		anchor, err := common.CreditsPerUSD()
		if err != nil || !pricing.settlementUnitsPerUSD.IsPositive() {
			return decimal.Zero, errors.New("invalid credit settlement pricing")
		}
		settlement = credits.Mul(pricing.settlementUnitsPerUSD).Div(anchor)
	} else if pricing.usesSettlementUnitsPerPlatformUnit {
		settlement = credits.Mul(pricing.settlementUnitsPerPlatformUnit).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	} else {
		if !pricing.platformUnitsPerUSD.IsPositive() || !pricing.settlementUnitsPerUSD.IsPositive() {
			return decimal.Zero, errors.New("invalid credit settlement pricing")
		}
		settlement = credits.Mul(pricing.settlementUnitsPerUSD).
			Div(decimal.NewFromFloat(common.QuotaPerUnit).Mul(pricing.platformUnitsPerUSD))
	}
	return applyTopUpSettlementRatios(settlement, topUpConfigAmountFromResolved(amount), group, ratio), nil
}

func quoteTopUpRequestWithDiscount(c *gin.Context, amount resolvedTopUpAmount, group, paymentMethod, code string, userID int) (decimal.Decimal, *model.DiscountCode, error) {
	if _, canonical := canonicalTopUpCredit(c); !canonical {
		return quoteTopUpLegacyDecimalWithDiscount(amount.LegacyBatch, group, paymentMethod, code, userID)
	}
	pricing, err := getPayMethodSettlementPricing(paymentMethod)
	if err != nil {
		return decimal.Zero, nil, err
	}
	ratio, err := getPayMethodTopupRatio(paymentMethod)
	if err != nil {
		return decimal.Zero, nil, err
	}
	base, err := quoteTopUpResolvedWithSettlementPricing(amount, group, pricing, ratio)
	if err != nil {
		return decimal.Zero, nil, err
	}
	return applyDiscountCodeQuoteDecimal(base, topUpConfigAmountFromResolved(amount), code, userID)
}

func standardTopUpRequestBase(c *gin.Context, amount resolvedTopUpAmount, group, currency string) (decimal.Decimal, error) {
	pricing, err := standardSettlementPricing(currency)
	if err != nil {
		return decimal.Zero, err
	}
	if _, canonical := canonicalTopUpCredit(c); canonical {
		return quoteTopUpResolvedWithSettlementPricing(amount, group, pricing, decimal.NewFromInt(1))
	}
	return quoteTopUpLegacyDecimalWithSettlementPricing(amount.LegacyBatch, group, pricing, decimal.NewFromInt(1))
}

func applyDiscountCodeQuoteRequest(c *gin.Context, base decimal.Decimal, amount resolvedTopUpAmount, code string, userID int) (decimal.Decimal, *model.DiscountCode, error) {
	if _, canonical := canonicalTopUpCredit(c); canonical {
		return applyDiscountCodeQuoteDecimal(base, topUpConfigAmountFromResolved(amount), code, userID)
	}
	return applyDiscountCodeQuoteLegacyDecimal(base, amount.LegacyBatch, code, userID)
}

func quoteStandardTopUpRequestWithDiscount(c *gin.Context, amount resolvedTopUpAmount, group, currency, code string, userID int) (decimal.Decimal, *model.DiscountCode, error) {
	base, err := standardTopUpRequestBase(c, amount, group, currency)
	if err != nil {
		return decimal.Zero, nil, err
	}
	return applyDiscountCodeQuoteRequest(c, base, amount, code, userID)
}

func withTopUpPublicCreditMetadata(info any) any {
	var data gin.H
	var methods []map[string]string
	switch value := info.(type) {
	case gin.H:
		data = value
		methods, _ = value["pay_methods"].([]map[string]string)
	case neutralTopUpInfo:
		methods = value.PayMethods
		encoded, err := json.Marshal(value)
		if err != nil {
			return info
		}
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.UseNumber()
		if err := decoder.Decode(&data); err != nil {
			return info
		}
	default:
		return info
	}
	metadata, err := topUpCreditMetadata(methods)
	if err != nil {
		// Old clients still receive their compatibility fields. New clients
		// require versioned raw metadata and must not infer Credits from them.
		data["credit_metadata_available"] = false
		return data
	}
	for key, value := range metadata {
		data[key] = value
	}
	data["pay_methods"] = methods
	data["credit_metadata_available"] = true
	return data
}
