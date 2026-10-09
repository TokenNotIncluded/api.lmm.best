package model

// Rates are integer wallet quota per million input tokens. PriceQuota remains
// the reviewed maximum reservation, so existing grants and budgets stay caps.
func toolMarketTokenQuota(rate, tokens int) (int, error) {
	if !marketQuotaValid(rate) || rate <= 0 || tokens < 0 || tokens > 1000000 {
		return 0, ErrToolMarketInput
	}
	price := (rate / 1000000) * tokens
	remainder := (rate % 1000000) * tokens
	price += remainder / 1000000
	if remainder%1000000 != 0 {
		price++
	}
	if !marketQuotaValid(price) {
		return 0, ErrToolMarketInput
	}
	return price, nil
}

func normalizeToolMarketPricing(tool *ToolMarketToolInput) error {
	if tool.ProviderPricing != nil && (tool.BillingMode != "" || len(tool.BillingRules) != 0 || tool.InputTokenPriceQuota != 0 || tool.MaxInputTokens != 0 || tool.PriceQuota <= 0) {
		return ErrToolMarketInput
	}
	switch tool.BillingMode {
	case "", "fixed":
		if len(tool.BillingRules) != 0 {
			return ErrToolMarketInput
		}
		if tool.InputTokenPriceQuota != 0 || tool.MaxInputTokens != 0 {
			return ErrToolMarketInput
		}
		tool.BillingMode = ""
	case "input_tokens":
		if len(tool.BillingRules) != 0 {
			return ErrToolMarketInput
		}
		if tool.MaxInputTokens <= 0 {
			return ErrToolMarketInput
		}
		price, err := toolMarketTokenQuota(tool.InputTokenPriceQuota, tool.MaxInputTokens)
		if err != nil {
			return err
		}
		tool.PriceQuota = price
	case "metered":
		if tool.InputTokenPriceQuota != 0 || tool.MaxInputTokens != 0 {
			return ErrToolMarketInput
		}
		price, err := toolMarketRulesQuota(tool.BillingRules, nil)
		if err != nil {
			return err
		}
		tool.PriceQuota = price
	default:
		return ErrToolMarketInput
	}
	return nil
}
