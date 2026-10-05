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
const canonicalTopUpCreditUnitsKey = "canonical_topup_credit_units"

// RequireCanonicalTopUpCredit retains version-1 raw ledger semantics only.
// New denominations have distinct routes so an older node returns 404 instead
// of interpreting a version-2 CREDIT amount as version-1 raw ledger quota.
func RequireCanonicalTopUpCredit(c *gin.Context) {
	requireVersionedTopUpCredit(c, 1)
}

// RequirePublicTopUpCredit belongs exclusively to /topup/currency/v2 routes.
// Preserve the original spelling before float64 binding at the safe-integer cap.
func RequirePublicTopUpCredit(c *gin.Context) {
	requireVersionedTopUpCredit(c, 2)
}

func requireVersionedTopUpCredit(c *gin.Context, requiredVersion int) {
	reject := func() {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
			"success": false, "message": "error", "data": "充值数量、单位或 credit_metadata_version 无效",
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
	if err := json.Unmarshal(body["amount_unit"], &unit); err != nil {
		reject()
		return
	}
	version := 1
	if raw, present := body["credit_metadata_version"]; present {
		switch string(bytes.TrimSpace(raw)) {
		case "1":
		case "2":
			version = 2
		default:
			reject()
			return
		}
	}
	if version != requiredVersion {
		reject()
		return
	}
	if unit != "CREDIT" && !(version == 2 && unit == "LEDGER_QUOTA") {
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
	var units common.CreditDenomination
	if version == 2 {
		units, err = common.CreditDenominationMetadata()
		if err != nil {
			c.AbortWithStatusJSON(http.StatusServiceUnavailable, gin.H{"success": false, "message": "error", "data": "充值额度配置无效"})
			return
		}
	}
	ledgerQuota := credits
	if version == 2 && unit == "CREDIT" {
		var expected string
		if err := json.Unmarshal(body["expected_public_credits_per_usd_exact"], &expected); err != nil || len(expected) == 0 || len(expected) > 16 || expected[0] == '0' {
			reject()
			return
		}
		for _, digit := range expected {
			if digit < '0' || digit > '9' {
				reject()
				return
			}
		}
		if expected != units.PublicCreditsPerUSDExact {
			c.AbortWithStatusJSON(http.StatusConflict, gin.H{"success": false, "message": "error", "data": "公开 Credit 单位已更新，请刷新充值报价"})
			return
		}
		ledgerQuota, err = units.ResolvePublicCredits(decimal.NewFromInt(credits))
		if err != nil {
			reject()
			return
		}
	}
	// The shared resolver retains legacy/raw semantics. Convert only at this
	// versioned boundary, before quote, capacity checks and order snapshots.
	resolved, err := resolveTopUpDecimalAmount(decimal.NewFromInt(ledgerQuota), "CREDIT")
	if err != nil {
		reject()
		return
	}
	c.Set(canonicalTopUpCreditKey, resolved)
	if version == 2 {
		c.Set(canonicalTopUpCreditUnitsKey, units)
	}
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
	_, paid, err := quoteTopUpResolvedSettlementAmounts(amount, group, pricing, ratio)
	return paid, err
}

func quoteTopUpResolvedSettlementAmounts(amount resolvedTopUpAmount, group string, pricing payMethodSettlementPricing, ratio decimal.Decimal) (decimal.Decimal, decimal.Decimal, error) {
	if amount.CreditedQuota <= 0 || !validQuotaPerUnit() {
		return decimal.Zero, decimal.Zero, errors.New("invalid credit amount")
	}
	credits := decimal.NewFromInt(amount.CreditedQuota)
	var settlement decimal.Decimal
	if pricing.usesFixedCreditDenomination {
		anchor, err := common.CreditsPerUSD()
		if err != nil || !pricing.settlementUnitsPerUSD.IsPositive() {
			return decimal.Zero, decimal.Zero, errors.New("invalid credit settlement pricing")
		}
		settlement = credits.Mul(pricing.settlementUnitsPerUSD).Div(anchor)
	} else if pricing.usesSettlementUnitsPerPlatformUnit {
		settlement = credits.Mul(pricing.settlementUnitsPerPlatformUnit).Div(decimal.NewFromFloat(common.QuotaPerUnit))
	} else {
		if !pricing.platformUnitsPerUSD.IsPositive() || !pricing.settlementUnitsPerUSD.IsPositive() {
			return decimal.Zero, decimal.Zero, errors.New("invalid credit settlement pricing")
		}
		settlement = credits.Mul(pricing.settlementUnitsPerUSD).
			Div(decimal.NewFromFloat(common.QuotaPerUnit).Mul(pricing.platformUnitsPerUSD))
	}
	original, paid := applyTopUpSettlementRatiosWithOriginal(settlement, topUpConfigAmountFromResolved(amount), group, ratio)
	return original, paid, nil
}

func quoteTopUpRequestWithDiscount(c *gin.Context, amount resolvedTopUpAmount, group, paymentMethod, code string, userID int) (decimal.Decimal, *model.DiscountCode, error) {
	_, canonical := canonicalTopUpCredit(c)
	pricing, err := getPayMethodSettlementPricing(paymentMethod)
	if err != nil {
		return decimal.Zero, nil, err
	}
	ratio, err := getPayMethodTopupRatio(paymentMethod)
	if err != nil {
		return decimal.Zero, nil, err
	}
	var original, base decimal.Decimal
	if canonical {
		original, base, err = quoteTopUpResolvedSettlementAmounts(amount, group, pricing, ratio)
	} else {
		original, base, err = quoteTopUpLegacySettlementAmounts(amount.LegacyBatch, topUpConfigAmountFromLegacy(amount.LegacyBatch), group, pricing, ratio)
	}
	if err != nil {
		return decimal.Zero, nil, err
	}
	recordTopUpQuoteBasis(c, original, base, pricing.settlementCurrency)
	return applyDiscountCodeQuoteRequest(c, base, amount, code, userID)
}

func standardTopUpRequestBase(c *gin.Context, amount resolvedTopUpAmount, group, currency string) (decimal.Decimal, error) {
	pricing, err := standardSettlementPricing(currency)
	if err != nil {
		return decimal.Zero, err
	}
	var original, paid decimal.Decimal
	if _, canonical := canonicalTopUpCredit(c); canonical {
		original, paid, err = quoteTopUpResolvedSettlementAmounts(amount, group, pricing, decimal.NewFromInt(1))
	} else {
		original, paid, err = quoteTopUpLegacySettlementAmounts(amount.LegacyBatch, topUpConfigAmountFromLegacy(amount.LegacyBatch), group, pricing, decimal.NewFromInt(1))
	}
	if err == nil {
		recordTopUpQuoteBasis(c, original, paid, pricing.settlementCurrency)
	}
	return paid, err
}

func applyDiscountCodeQuoteRequest(c *gin.Context, base decimal.Decimal, amount resolvedTopUpAmount, code string, userID int) (decimal.Decimal, *model.DiscountCode, error) {
	var paid decimal.Decimal
	var discount *model.DiscountCode
	var err error
	if _, canonical := canonicalTopUpCredit(c); canonical {
		paid, discount, err = applyDiscountCodeQuoteDecimal(base, topUpConfigAmountFromResolved(amount), code, userID)
	} else {
		paid, discount, err = applyDiscountCodeQuoteLegacyDecimal(base, amount.LegacyBatch, code, userID)
	}
	if err == nil {
		updateTopUpQuotePaid(c, paid)
	}
	return paid, discount, err
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
