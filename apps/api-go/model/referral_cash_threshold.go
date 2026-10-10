package model

import (
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strings"

	"github.com/shopspring/decimal"
)

var referralCashAmountPattern = regexp.MustCompile(`^[0-9]{1,10}(\.[0-9]{1,6})?$`)
var referralCashCurrencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Floors are explicit amounts in each settlement currency, not an exchange-rate
// estimate. Missing currencies fail closed for rewards, never for paid credit.
func parseReferralMinTopUpAmounts(raw string) (map[string]string, error) {
	invalid := errors.New("ReferralMinTopUpAmounts must contain 1-32 currency codes and positive cash amounts with at most 6 decimal places")
	if len(raw) > 4096 {
		return nil, invalid
	}
	decoder := json.NewDecoder(strings.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, invalid
	}
	amounts := make(map[string]string)
	for decoder.More() {
		token, err := decoder.Token()
		currency, ok := token.(string)
		if err != nil || !ok || !referralCashCurrencyPattern.MatchString(currency) {
			return nil, invalid
		}
		if _, exists := amounts[currency]; exists {
			return nil, invalid
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return nil, invalid
		}
		text := strings.TrimSpace(string(value))
		if strings.HasPrefix(text, `"`) {
			if err := json.Unmarshal(value, &text); err != nil {
				return nil, invalid
			}
		}
		if !referralCashAmountPattern.MatchString(text) {
			return nil, invalid
		}
		amount, err := decimal.NewFromString(text)
		if err != nil || !amount.IsPositive() {
			return nil, invalid
		}
		amounts[currency] = amount.String()
		if len(amounts) > 32 {
			return nil, invalid
		}
	}
	if token, err = decoder.Token(); err != nil || token != json.Delim('}') || len(amounts) == 0 {
		return nil, invalid
	}
	if err = decoder.Decode(new(any)); err != io.EOF {
		return nil, invalid
	}
	return amounts, nil
}

func referralCashMinimumMet(policy ReferralPolicy, topUp *TopUp) bool {
	if topUp == nil || topUp.SettledAmountMicros <= 0 {
		return false
	}
	raw, exists := policy.MinTopUpAmounts[strings.ToUpper(strings.TrimSpace(topUp.SettlementCurrency))]
	if !exists || !referralCashAmountPattern.MatchString(raw) {
		return false
	}
	floor, err := decimal.NewFromString(raw)
	if err != nil || !floor.IsPositive() {
		return false
	}
	// Compare integers exactly: gifts, quota units and coupon face values do
	// not increase the amount verified by the payment provider.
	return decimal.NewFromInt(topUp.SettledAmountMicros).GreaterThanOrEqual(floor.Mul(decimal.NewFromInt(1_000_000)))
}
