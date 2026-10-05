package model

import (
	"bytes"
	"encoding/json"
	"io"
)

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
	switch tool.BillingMode {
	case "", "fixed":
		if tool.InputTokenPriceQuota != 0 || tool.MaxInputTokens != 0 {
			return ErrToolMarketInput
		}
		tool.BillingMode = ""
	case "input_tokens":
		if tool.MaxInputTokens <= 0 {
			return ErrToolMarketInput
		}
		price, err := toolMarketTokenQuota(tool.InputTokenPriceQuota, tool.MaxInputTokens)
		if err != nil {
			return err
		}
		tool.PriceQuota = price
	default:
		return ErrToolMarketInput
	}
	return nil
}

func marketUsageObject(raw json.RawMessage) (map[string]json.RawMessage, error) {
	var value map[string]json.RawMessage
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if decoder.Decode(&value) != nil || value == nil {
		return nil, ErrToolMarketInput
	}
	var trailing any
	if decoder.Decode(&trailing) != io.EOF {
		return nil, ErrToolMarketInput
	}
	return value, nil
}

// Only final server-owned MCP results supply usage; request arguments and
// client _meta never do. Explicit null means the tool made no model request
// (for example Jev extraction with no regex candidates). Missing usage fails.
// If both structured and textual receipts exist they must agree.
func ToolMarketInputTokenUsage(data json.RawMessage) (int, error) {
	envelope, err := marketUsageObject(data)
	if err != nil {
		return 0, err
	}
	found, tokens := false, 0
	accept := func(raw json.RawMessage) error {
		receipt, err := marketUsageObject(raw)
		if err != nil {
			return err
		}
		usageRaw, ok := receipt["usage"]
		if !ok {
			return ErrToolMarketInput
		}
		count := 0
		if !bytes.Equal(bytes.TrimSpace(usageRaw), []byte("null")) {
			usage, err := marketUsageObject(usageRaw)
			if err != nil {
				return err
			}
			value, ok := usage["input_tokens"]
			if !ok || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &count) != nil || count < 0 || count > 1000000 {
				return ErrToolMarketInput
			}
		}
		if found && count != tokens {
			return ErrToolMarketInput
		}
		found, tokens = true, count
		return nil
	}
	if raw, ok := envelope["structuredContent"]; ok && !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		if err := accept(raw); err != nil {
			return 0, err
		}
	}
	var content []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if raw, ok := envelope["content"]; ok {
		if json.Unmarshal(raw, &content) != nil {
			return 0, ErrToolMarketInput
		}
		for _, item := range content {
			if item.Type == "text" {
				if err := accept(json.RawMessage(item.Text)); err != nil {
					return 0, err
				}
			}
		}
	}
	if !found {
		return 0, ErrToolMarketInput
	}
	return tokens, nil
}
