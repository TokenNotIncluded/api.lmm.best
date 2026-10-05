package controller

import (
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/shopspring/decimal"
)

// topUpCreditMetadata publishes authoritative integer-credit catalog values.
// Legacy fields remain compatibility projections, never inputs to the new limits.
// The caller supplies sanitized public maps; they are updated only after every
// catalog value and payment policy has passed validation.
func topUpCreditMetadata(payMethods []map[string]string) (gin.H, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil {
		return nil, err
	}
	if _, err := common.LegacyPricingUnitsPerUSD(); err != nil {
		return nil, err
	}
	quotaPerBatch := decimal.NewFromFloat(common.QuotaPerUnit)
	tokens := operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens
	catalogCredit := func(amount int) (int64, error) {
		value := decimal.NewFromInt(int64(amount))
		if !tokens {
			value = value.Mul(quotaPerBatch)
		}
		credits, err := topUpMetadataCreditInteger(value.Floor())
		if err != nil || credits <= 0 {
			return 0, fmt.Errorf("invalid top-up credit catalog amount %d", amount)
		}
		return credits, nil
	}

	paymentSetting := operation_setting.GetPaymentSetting()
	options := make([]int64, 0, len(paymentSetting.AmountOptions))
	for _, amount := range paymentSetting.AmountOptions {
		credits, err := catalogCredit(amount)
		if err != nil {
			return nil, err
		}
		options = append(options, credits)
	}
	discounts := make(map[string]float64, len(paymentSetting.AmountDiscount))
	for amount, discount := range paymentSetting.AmountDiscount {
		credits, err := catalogCredit(amount)
		if err != nil {
			return nil, err
		}
		if math.IsNaN(discount) || math.IsInf(discount, 0) || discount <= 0 {
			return nil, fmt.Errorf("invalid top-up discount for catalog amount %d", amount)
		}
		key := strconv.FormatInt(credits, 10)
		if previous, exists := discounts[key]; exists && previous != discount {
			return nil, fmt.Errorf("conflicting top-up discounts for credit amount %s", key)
		}
		discounts[key] = discount
	}

	metadata := gin.H{
		"credit_metadata_version": 1,
		"credit_amount_options":   options,
		"credit_discount":         discounts,
	}
	providerMinima := make(map[string]int64, 4)
	for _, minimum := range []struct {
		key    string
		legacy int
	}{
		{"credit_min_topup", operation_setting.MinTopUp},
		{"stripe_credit_min_topup", setting.StripeMinTopUp},
		{"waffo_credit_min_topup", setting.WaffoMinTopUp},
		{"pancake_credit_min_topup", setting.WaffoPancakeMinTopUp},
	} {
		if minimum.legacy < 0 {
			return nil, fmt.Errorf("invalid %s: legacy minimum must not be negative", minimum.key)
		}
		credits, err := topUpMetadataCreditInteger(decimal.NewFromInt(int64(minimum.legacy)).Mul(quotaPerBatch).Ceil())
		if err != nil {
			return nil, fmt.Errorf("invalid %s: %w", minimum.key, err)
		}
		metadata[minimum.key] = credits
		providerMinima[minimum.key] = credits
	}

	type methodCreditPolicy struct {
		minimum    int64
		maximum    int64
		hasMaximum bool
	}
	// Dedicated checkout views may synthesize their payment method instead of
	// retaining the public catalog row. Publish their entire effective policy,
	// including configured limits, even when no row is present in payMethods.
	providerPolicies := make(map[string]methodCreditPolicy, 3)
	providerKeys := []struct {
		paymentType string
		minimumKey  string
		maximumKey  string
	}{
		{model.PaymentMethodStripe, "stripe_credit_min_topup", "stripe_credit_max_topup"},
		{model.PaymentMethodWaffo, "waffo_credit_min_topup", "waffo_credit_max_topup"},
		{model.PaymentMethodWaffoPancake, "pancake_credit_min_topup", "pancake_credit_max_topup"},
	}
	for _, provider := range providerKeys {
		policy := methodCreditPolicy{minimum: providerMinima[provider.minimumKey]}
		minimum, configured, err := configuredPaymentMethodMinTopUp(provider.paymentType)
		if err != nil {
			return nil, err
		}
		if configured {
			credits, err := topUpMetadataCreditInteger(minimum.Mul(anchor).Ceil())
			if err != nil {
				return nil, fmt.Errorf("payment method %q has invalid credit minimum: %w", provider.paymentType, err)
			}
			if credits > policy.minimum {
				policy.minimum = credits
			}
		}
		maximum, configured, err := configuredPaymentMethodMaxTopUp(provider.paymentType)
		if err != nil {
			return nil, err
		}
		if configured {
			policy.maximum, err = topUpMetadataCreditInteger(maximum.Mul(anchor).Floor())
			if err != nil {
				return nil, fmt.Errorf("payment method %q has invalid credit maximum: %w", provider.paymentType, err)
			}
			policy.hasMaximum = true
		}
		if provider.paymentType == model.PaymentMethodStripe {
			// Both Stripe quote and checkout retain the 10000 legacy-batch cap.
			// Clamp it to the wallet domain before publishing its integer alias.
			cap := decimal.NewFromInt(10000).Mul(quotaPerBatch).Floor()
			if cap.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
				cap = decimal.NewFromInt(common.MaxWalletQuota)
			}
			maximum, err := topUpMetadataCreditInteger(cap)
			if err != nil {
				return nil, err
			}
			if !policy.hasMaximum || maximum < policy.maximum {
				policy.maximum = maximum
			}
			policy.hasMaximum = true
		}
		if policy.hasMaximum && policy.minimum > policy.maximum {
			return nil, fmt.Errorf("payment method %q has credit minimum above maximum", provider.paymentType)
		}
		providerPolicies[provider.paymentType] = policy
	}
	policies := make([]methodCreditPolicy, len(payMethods))
	for i, method := range payMethods {
		paymentType := strings.TrimSpace(method["type"])
		if method == nil || paymentType == "" {
			return nil, fmt.Errorf("invalid public payment method")
		}
		minimumUSD, configured, err := configuredPaymentMethodMinTopUp(paymentType)
		if err != nil {
			return nil, err
		}
		if unit := strings.ToUpper(strings.TrimSpace(method["min_topup_unit"])); !configured && unit != "" && unit != topUpLegacyUnit {
			return nil, fmt.Errorf("payment method %q has invalid legacy minimum unit", paymentType)
		}
		minimumCredits := decimal.Zero
		if configured {
			minimumCredits = minimumUSD.Mul(anchor).Ceil()
		} else if raw := strings.TrimSpace(method["legacy_min_topup"]); raw != "" {
			minimum, err := decimal.NewFromString(raw)
			if err != nil || minimum.IsNegative() {
				return nil, fmt.Errorf("payment method %q has invalid legacy minimum", paymentType)
			}
			minimumCredits = minimum.Mul(quotaPerBatch).Ceil()
		} else if strings.TrimSpace(method["min_topup"]) != "" {
			return nil, fmt.Errorf("payment method %q is missing legacy minimum metadata", paymentType)
		}
		policies[i].minimum, err = topUpMetadataCreditInteger(minimumCredits)
		if err != nil {
			return nil, fmt.Errorf("payment method %q has invalid credit minimum: %w", paymentType, err)
		}
		providerMinimumKey := "credit_min_topup"
		switch paymentType {
		case model.PaymentMethodStripe:
			providerMinimumKey = "stripe_credit_min_topup"
		case model.PaymentMethodWaffo:
			providerMinimumKey = "waffo_credit_min_topup"
		case model.PaymentMethodWaffoPancake:
			providerMinimumKey = "pancake_credit_min_topup"
		case model.PaymentMethodCreem:
			// Fixed products do not inherit the ordinary Epay recharge minimum.
			providerMinimumKey = ""
		}
		if providerMinimum := providerMinima[providerMinimumKey]; providerMinimum > policies[i].minimum {
			policies[i].minimum = providerMinimum
		}
		maximumUSD, configured, err := configuredPaymentMethodMaxTopUp(paymentType)
		if err != nil {
			return nil, err
		}
		if configured {
			maximum, err := topUpMetadataCreditInteger(maximumUSD.Mul(anchor).Floor())
			if err != nil {
				return nil, fmt.Errorf("payment method %q has invalid credit maximum: %w", paymentType, err)
			}
			policies[i].maximum, policies[i].hasMaximum = maximum, true
		}
		if providerPolicy, dedicated := providerPolicies[paymentType]; dedicated {
			if providerPolicy.minimum > policies[i].minimum {
				policies[i].minimum = providerPolicy.minimum
			}
			if providerPolicy.hasMaximum && (!policies[i].hasMaximum || providerPolicy.maximum < policies[i].maximum) {
				policies[i].maximum, policies[i].hasMaximum = providerPolicy.maximum, true
			}
			// Preserve any stricter validated automatic legacy minimum too.
			if policies[i].minimum > providerPolicy.minimum {
				providerPolicy.minimum = policies[i].minimum
				providerPolicies[paymentType] = providerPolicy
			}
		}
		if policies[i].hasMaximum && policies[i].minimum > policies[i].maximum {
			return nil, fmt.Errorf("payment method %q has credit minimum above maximum", paymentType)
		}
	}
	for _, provider := range providerKeys {
		policy := providerPolicies[provider.paymentType]
		metadata[provider.minimumKey] = policy.minimum
		metadata[provider.maximumKey] = nil // Explicitly unlimited; absence is incomplete metadata.
		if policy.hasMaximum {
			metadata[provider.maximumKey] = policy.maximum
		}
	}
	for i, method := range payMethods {
		method["min_topup_credit"] = strconv.FormatInt(policies[i].minimum, 10)
		delete(method, "max_topup_credit")
		if policies[i].hasMaximum {
			method["max_topup_credit"] = strconv.FormatInt(policies[i].maximum, 10)
		}
	}
	return metadata, nil
}

func topUpMetadataCreditInteger(value decimal.Decimal) (int64, error) {
	if value.IsNegative() || !value.IsInteger() || value.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, fmt.Errorf("credit amount is outside the non-negative integer wallet domain")
	}
	return value.IntPart(), nil
}
