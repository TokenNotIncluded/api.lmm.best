package model

import (
	"fmt"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/pkg/billingexpr"
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
	price, err := pricingFiniteFloat(decimal.NewFromFloat(ratio).Mul(decimal.NewFromInt(1_000_000)).DivRound(anchor, 64))
	if price == 0 && ratio > 0 {
		return 0, fmt.Errorf("price is outside the representable range")
	}
	return price, err
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
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return 0, err
	}
	price, err := pricingFiniteFloat(decimal.NewFromFloat(amount).Mul(baseline).DivRound(anchor, 64))
	if price == 0 && amount > 0 {
		return 0, fmt.Errorf("price is outside the representable range")
	}
	return price, err
}

func pricingFiniteFloat(value decimal.Decimal) (float64, error) {
	result := value.InexactFloat64()
	if math.IsNaN(result) || math.IsInf(result, 0) || (result == 0 && !value.IsZero()) {
		return 0, fmt.Errorf("price is outside the representable range")
	}
	return result, nil
}

// USDPriceProduct preserves explicitly free rates, while rejecting invalid
// factors and positive products that underflow to a fake free USD price.
func USDPriceProduct(input, completion float64) (float64, error) {
	output := input * completion
	if input < 0 || completion < 0 || math.IsNaN(input) || math.IsNaN(completion) || math.IsInf(input, 0) || math.IsInf(completion, 0) || math.IsNaN(output) || math.IsInf(output, 0) || (input > 0 && completion > 0 && output == 0) {
		return 0, fmt.Errorf("price product is outside the representable range")
	}
	return output, nil
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
	baseline, err := common.LegacyPricingQuotaPerUnit()
	if err != nil {
		return "", err
	}
	scale := anchor.DivRound(baseline, 64)
	if !scale.IsPositive() {
		return "", fmt.Errorf("legacy price scale is outside the representable range")
	}
	return wrapPricingExpression(expr, "/", scale.String()), nil
}

func wrapPricingExpression(expr, operator, scale string) string {
	_, body := billingexpr.ParseExprVersion(expr)
	prefix := expr[:len(expr)-len(body)]
	return prefix + "(" + body + ") " + operator + " (" + scale + ")"
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
		output, err := USDPriceProduct(input, p.CompletionRatio)
		if err != nil {
			return nil, err
		}
		p.InputPrice, p.OutputPrice = &input, &output
		cache, _ := ratio_setting.GetCacheRatio(p.ModelName)
		create, _ := ratio_setting.GetCreateCacheRatio(p.ModelName)
		read, err := USDPriceProduct(input, cache)
		if err != nil {
			return nil, err
		}
		write, err := USDPriceProduct(input, create)
		if err != nil {
			return nil, err
		}
		p.CacheReadPrice, p.CacheWritePrice = &read, &write
		for _, field := range []struct {
			ratio *float64
			price **float64
		}{{p.ImageRatio, &p.ImagePrice}, {p.AudioRatio, &p.AudioInputPrice}} {
			if field.ratio != nil {
				v, err := USDPriceProduct(input, *field.ratio)
				if err != nil {
					return nil, err
				}
				*field.price = &v
			}
		}
		if p.AudioRatio != nil || p.AudioCompletionRatio != nil {
			// Audio completion is relative to audio input, not text input.
			// Settlement defaults either absent audio multiplier to one.
			audioInput, audioCompletion := input, 1.0
			if p.AudioRatio != nil {
				audioInput = *p.AudioInputPrice
			}
			if p.AudioCompletionRatio != nil {
				audioCompletion = *p.AudioCompletionRatio
			}
			v, err := USDPriceProduct(audioInput, audioCompletion)
			if err != nil {
				return nil, err
			}
			p.AudioOutputPrice = &v
		}
	}
	return result, nil
}
