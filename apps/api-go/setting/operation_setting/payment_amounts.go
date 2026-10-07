package operation_setting

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"regexp"
	"strconv"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/shopspring/decimal"
)

// JSON numbers retain their decimal spelling until the integer-credit catalog
// is validated. float64 must not hide a fractional credit or a wallet overflow.
type PaymentAmountOptions []json.Number
type PaymentAmountDiscount map[string]float64

var paymentAmountNumber = regexp.MustCompile(`^-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE]([+-]?[0-9]+))?$`)
var paymentAmountKey = regexp.MustCompile(`^[+-]?[0-9]+(?:\.[0-9]+)?(?:[eE]([+-]?[0-9]+))?$`)

func ParsePaymentAmount(value string) (decimal.Decimal, error) {
	return parsePaymentAmount(value, paymentAmountNumber)
}

func ParsePaymentDiscountAmount(value string) (decimal.Decimal, error) {
	// Old map[int] JSON accepted +1 and leading-zero keys; retain that numeric
	// meaning, while checking all canonical aliases before saving a new map.
	return parsePaymentAmount(value, paymentAmountKey)
}

func parsePaymentAmount(value string, grammar *regexp.Regexp) (decimal.Decimal, error) {
	if len(value) == 0 || len(value) > 64 {
		return decimal.Zero, errors.New("payment amount must be a bounded decimal number")
	}
	parts := grammar.FindStringSubmatch(value)
	if parts == nil {
		return decimal.Zero, errors.New("payment amount must be a decimal number")
	}
	if parts[1] != "" {
		exponent, err := strconv.Atoi(parts[1])
		if err != nil || exponent < -18 || exponent > 18 {
			return decimal.Zero, errors.New("payment amount exponent must be between -18 and 18")
		}
	}
	amount, err := decimal.NewFromString(value)
	if err != nil || !amount.IsPositive() || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return decimal.Zero, errors.New("payment amount must be positive and within the wallet domain")
	}
	return amount, nil
}

// The retained TOKENS configuration names raw ledger credits. Other existing
// configurations name legacy USD batches, independently of a user's CNY or
// other display preference. Their pricing scale is still validated by the
// immutable currency basis in the quote/metadata path; this never changes it.
func PaymentConfigAmountCredit(amount decimal.Decimal, tokens bool) (int64, error) {
	if !tokens {
		if common.QuotaPerUnit <= 0 || math.IsNaN(common.QuotaPerUnit) || math.IsInf(common.QuotaPerUnit, 0) {
			return 0, errors.New("payment amount pricing scale is invalid")
		}
		amount = amount.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	if !amount.IsPositive() || !amount.IsInteger() || amount.GreaterThan(decimal.NewFromInt(common.MaxWalletQuota)) {
		return 0, errors.New("payment amount must represent a positive integer credit balance within the wallet domain")
	}
	return amount.IntPart(), nil
}

func (amounts *PaymentAmountOptions) UnmarshalJSON(data []byte) error {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '[' {
		return errors.New("amount_options must be a JSON array")
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(trimmed, &entries); err != nil || len(entries) > 100 {
		return errors.New("amount_options must be a JSON array with at most 100 entries")
	}
	parsed := make(PaymentAmountOptions, 0, len(entries))
	for _, entry := range entries {
		value, err := ParsePaymentAmount(string(bytes.TrimSpace(entry)))
		if err != nil {
			return err
		}
		parsed = append(parsed, json.Number(value.String()))
	}
	// Never partially mutate the active catalog when loading old persisted JSON.
	*amounts = parsed
	return nil
}

func (discounts *PaymentAmountDiscount) UnmarshalJSON(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("amount_discount must be a JSON object")
	}
	parsed := make(PaymentAmountDiscount)
	entries := 0
	for decoder.More() {
		entries++
		if entries > 100 {
			return errors.New("amount_discount must have at most 100 entries")
		}
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		text, ok := key.(string)
		if !ok {
			return errors.New("amount_discount key must be a decimal amount")
		}
		amount, err := ParsePaymentDiscountAmount(text)
		if err != nil {
			return err
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return err
		}
		var multiplier float64
		if len(raw) == 0 || raw[0] == '"' || string(raw) == "null" || json.Unmarshal(raw, &multiplier) != nil || multiplier <= 0 || multiplier > 1 || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
			return errors.New("top-up discounts must map positive amounts to finite values between 0 and 1")
		}
		canonical := amount.String()
		if previous, exists := parsed[canonical]; exists && previous != multiplier {
			return fmt.Errorf("conflicting top-up discounts for amount %s", canonical)
		}
		parsed[canonical] = multiplier
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("invalid amount_discount object")
	}
	if _, err := decoder.Token(); err != io.EOF {
		return errors.New("unexpected amount_discount suffix")
	}
	*discounts = parsed
	return nil
}

func ValidatePaymentCatalogJSON(key, value string, tokens bool) error {
	switch key {
	case "payment_setting.amount_options":
		var amounts PaymentAmountOptions
		if err := json.Unmarshal([]byte(value), &amounts); err != nil {
			return err
		}
		seen := make(map[string]bool, len(amounts))
		for _, number := range amounts {
			amount, err := ParsePaymentAmount(number.String())
			if err != nil {
				return err
			}
			if _, err := PaymentConfigAmountCredit(amount, tokens); err != nil {
				return err
			}
			if seen[amount.String()] {
				return errors.New("top-up amounts cannot contain duplicates")
			}
			seen[amount.String()] = true
		}
	case "payment_setting.amount_discount":
		var discounts PaymentAmountDiscount
		if err := json.Unmarshal([]byte(value), &discounts); err != nil {
			return err
		}
		for key := range discounts {
			amount, err := ParsePaymentDiscountAmount(key)
			if err != nil {
				return err
			}
			if _, err := PaymentConfigAmountCredit(amount, tokens); err != nil {
				return err
			}
		}
	default:
		return errors.New("unknown payment catalog option: " + strings.TrimSpace(key))
	}
	return nil
}
