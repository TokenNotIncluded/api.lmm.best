package model

import (
	"fmt"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting/ratio_setting"
	"github.com/shopspring/decimal"
)

const PricingSchemaUSD = 2
const PricingCurrencyUSD = "USD"
const PricingStorageLegacy = "legacy_pricing_unit"

// ModelRatio is calibrated credits per token. Completion, cache, modality and
// group ratios are multipliers and must not be rewritten during normalization.
func ModelRatioUSDPerMillion(ratio float64) (float64, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return 0, err
	}
	if ratio < 0 || math.IsNaN(ratio) || math.IsInf(ratio, 0) {
		return 0, fmt.Errorf("invalid model ratio")
	}
	return pricingFiniteFloat(decimal.NewFromFloat(ratio).Mul(decimal.NewFromInt(1_000_000)).DivRound(anchor, 64))
}

func LegacyPricingAmountUSD(amount float64) (float64, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return 0, err
	}
	if _, err = common.LegacyPricingUnitsPerUSD(); err != nil {
		return 0, err
	}
	if amount < 0 || math.IsNaN(amount) || math.IsInf(amount, 0) {
		return 0, fmt.Errorf("invalid legacy price")
	}
	return pricingFiniteFloat(decimal.NewFromFloat(amount).Mul(decimal.NewFromFloat(common.QuotaPerUnit)).DivRound(anchor, 64))
}

func pricingFiniteFloat(value decimal.Decimal) (float64, error) {
	result := value.InexactFloat64()
	if math.IsNaN(result) || math.IsInf(result, 0) || (result == 0 && !value.IsZero()) {
		return 0, fmt.Errorf("price is outside the representable range")
	}
	return result, nil
}

// USDExpression scales the entire monetary result. Tier conditions, request
// rules and the immutable stored expression are never rewritten.
func USDExpression(expr string) (string, error) {
	if strings.TrimSpace(expr) == "" {
		return expr, nil
	}
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return "", err
	}
	if _, err = common.LegacyPricingUnitsPerUSD(); err != nil {
		return "", err
	}
	scale := anchor.DivRound(decimal.NewFromFloat(common.QuotaPerUnit), 64)
	return "(" + expr + ") / (" + scale.String() + ")", nil
}

// NormalizePricingUSD returns a detached public DTO, leaving the immutable
// legacy cache and every request's billing snapshot untouched.
func NormalizePricingUSD(pricing []Pricing) ([]Pricing, error) {
	if _, err := common.CreditsPerUSD(); err != nil {
		return nil, err
	}
	result := clonePricing(pricing)
	for i := range result {
		p := &result[i]
		p.PricingSchemaVersion, p.PricingCurrency = PricingSchemaUSD, PricingCurrencyUSD
		var err error
		p.ModelPrice, err = LegacyPricingAmountUSD(p.ModelPrice)
		if err != nil {
			return nil, err
		}
		p.BillingExpr, err = USDExpression(p.BillingExpr)
		if err != nil {
			return nil, err
		}
		if p.QuotaType != 0 || p.BillingMode == "tiered_expr" {
			continue
		}
		input, err := ModelRatioUSDPerMillion(p.ModelRatio)
		if err != nil {
			return nil, err
		}
		output := input * p.CompletionRatio
		if math.IsNaN(output) || math.IsInf(output, 0) || output < 0 {
			return nil, fmt.Errorf("invalid completion price")
		}
		p.InputPrice, p.OutputPrice = &input, &output
		cache, _ := ratio_setting.GetCacheRatio(p.ModelName)
		create, _ := ratio_setting.GetCreateCacheRatio(p.ModelName)
		read, write := input*cache, input*create
		p.CacheReadPrice, p.CacheWritePrice = &read, &write
		for _, field := range []struct {
			ratio *float64
			price **float64
		}{{p.ImageRatio, &p.ImagePrice}, {p.AudioRatio, &p.AudioInputPrice}, {p.AudioCompletionRatio, &p.AudioOutputPrice}} {
			if field.ratio != nil {
				v := input * *field.ratio
				*field.price = &v
			}
		}
	}
	return result, nil
}
