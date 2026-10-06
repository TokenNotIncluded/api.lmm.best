package controller

import (
	"bytes"
	"encoding/json"
	"errors"
	"math"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
	"github.com/gin-gonic/gin/binding"
	"github.com/shopspring/decimal"
)

const topUpLegacyUnit = "LEGACY"

// New clients name their amount unit. Only requests without a unit retain the
// historical display-setting convention. Everything after this boundary uses
// legacy batch units as a compatibility projection; CreditedQuota is authority.
type resolvedTopUpAmount struct {
	LegacyBatch   decimal.Decimal
	CreditedQuota int64
}

func parseTopUpAmountWithUnit(value float64, rawUnit string) (decimal.Decimal, error) {
	resolved, err := resolveTopUpAmount(value, rawUnit)
	return resolved.LegacyBatch, err
}

func resolveTopUpAmount(value float64, rawUnit string) (resolvedTopUpAmount, error) {
	if !validFinitePositivePaymentRate(value) {
		return resolvedTopUpAmount{LegacyBatch: decimal.Zero}, errors.New("充值数量无效")
	}
	return resolveTopUpDecimalAmount(decimal.NewFromFloat(value), rawUnit)
}

func resolveTopUpDecimalAmount(amount decimal.Decimal, rawUnit string) (resolvedTopUpAmount, error) {
	invalid := resolvedTopUpAmount{LegacyBatch: decimal.Zero}
	if !amount.IsPositive() {
		return invalid, errors.New("充值数量无效")
	}
	if !amount.Equal(amount.Truncate(6)) {
		return invalid, errors.New("充值数量最多支持 6 位小数")
	}
	if !validQuotaPerUnit() {
		return invalid, errors.New("充值额度配置无效")
	}

	quotaPerBatch := decimal.NewFromFloat(common.QuotaPerUnit)
	unit := topUpRequestUnit(rawUnit)
	var credits decimal.Decimal
	var err error
	legacy := amount
	switch unit {
	case topUpLegacyUnit:
		credits = amount.Mul(quotaPerBatch).Floor()
	case "CREDIT":
		if !amount.IsInteger() {
			return invalid, errors.New("CREDIT 必须为整数")
		}
		credits = amount
	case "USD", "CNY":
		fx := operation_setting.USDExchangeRate
		if !validFinitePositivePaymentRate(fx) {
			return invalid, errors.New("充值汇率配置无效")
		}
		credits, err = common.FiatToCreditsDecimal(amount, unit, decimal.NewFromFloat(fx))
		if err != nil {
			return invalid, errors.New("充值汇率配置无效")
		}
		credits = credits.Floor()
	default:
		return invalid, errors.New("amount_unit 必须为 USD、CNY、CREDIT 或 LEGACY")
	}
	integer, err := validateCreditedQuota(credits)
	if err != nil {
		return invalid, err
	}
	if unit != topUpLegacyUnit {
		legacy = decimal.NewFromInt(int64(integer)).Div(quotaPerBatch)
	}
	return resolvedTopUpAmount{LegacyBatch: legacy, CreditedQuota: int64(integer)}, nil
}

func topUpOrderAmountsResolved(amount resolvedTopUpAmount) (int64, int64, int64, error) {
	if amount.CreditedQuota <= 0 || common.ValidateWalletQuota(int(amount.CreditedQuota)) != nil {
		return 0, 0, 0, errors.New("充值额度无效")
	}
	stored, ok := decimalInt64Truncated(amount.LegacyBatch)
	if !ok {
		return 0, 0, 0, errors.New("充值数量超出系统可表示范围")
	}
	// Six-place batch micros are only a backward-compatible projection. They
	// must never round, reject, or replace an already valid integer credit grant.
	projected := amount.LegacyBatch.Shift(6).Truncate(0)
	micros, ok := decimalInt64Truncated(projected)
	if !ok || micros < 0 {
		return 0, 0, 0, errors.New("充值数量超出系统可表示范围")
	}
	return stored, micros, amount.CreditedQuota, nil
}

func topUpRequestUnit(rawUnit string) string {
	unit := strings.ToUpper(strings.TrimSpace(rawUnit))
	if unit == "" {
		if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
			return "CREDIT"
		}
		return topUpLegacyUnit
	}
	return unit
}

func topUpCreditFields(rawUnit string, legacyBatch decimal.Decimal, credits int64, currency string, snapshots ...common.CreditDenomination) gin.H {
	fields := gin.H{
		"currency_unit": "credit", "amount_unit": topUpRequestUnit(rawUnit),
		"legacy_batch_units":  legacyBatch.String(),
		"settlement_currency": strings.ToUpper(strings.TrimSpace(currency)),
	}
	return withTopUpCreditAmountFields(fields, credits, snapshots...)
}

func withTopUpCreditAmountFields(fields gin.H, credits int64, snapshots ...common.CreditDenomination) gin.H {
	// Compatibility aliases retain their immutable ledger meaning, including
	// fixed-product checkout responses which have no legacy-batch input.
	fields["credited_quota"] = credits
	fields["credit_amount"] = credits
	fields["credit_amount_unit"] = "LEDGER_QUOTA"
	var units common.CreditDenomination
	var err error
	if len(snapshots) > 0 {
		units = snapshots[0]
	} else {
		units, err = model.CreditDenominationSnapshot()
	}
	if err != nil {
		return fields
	}
	if publicCredits, err := units.ProjectLedgerQuota(credits); err == nil {
		for key, value := range creditUnitMetadataFieldsFor(units) {
			fields[key] = value
		}
		fields["public_credit_amount"] = publicCredits.String()
		fields["public_credit_amount_unit"] = "CREDIT"
		fields["public_credit_metadata_version"] = 2
	}
	return fields
}

func withTopUpCreditFields(response gin.H, rawUnit string, legacyBatch decimal.Decimal, credits int64, currency string, snapshots ...common.CreditDenomination) gin.H {
	for key, value := range topUpCreditFields(rawUnit, legacyBatch, credits, currency, snapshots...) {
		response[key] = value
	}
	return response
}

func withTopUpRequestCreditFields(c *gin.Context, response gin.H, rawUnit string, legacyBatch decimal.Decimal, credits int64, currency string) gin.H {
	addTopUpSettlementQuote(c, response, currency)
	if value, exists := c.Get(canonicalTopUpCreditUnitsKey); exists {
		if units, ok := value.(common.CreditDenomination); ok {
			return withTopUpCreditFields(response, rawUnit, legacyBatch, credits, currency, units)
		}
	}
	return withTopUpCreditFields(response, rawUnit, legacyBatch, credits, currency)
}

func requirePaymentMethodLegacyAmountWithinLimit(c *gin.Context, paymentType string, amount decimal.Decimal) bool {
	usd, err := common.LegacyAmountToUSD(amount)
	if err != nil {
		common.ApiErrorMsg(c, "充值汇率配置无效")
		return false
	}
	return requirePaymentMethodUSDWithinLimit(c, paymentType, usd)
}

func validFinitePositivePaymentRate(rate float64) bool {
	return rate > 0 && !math.IsNaN(rate) && !math.IsInf(rate, 0)
}

// Legacy preset/coupon configuration used raw credit keys in global TOKENS
// mode. This bridge preserves that configuration without letting a display
// preference redefine new explicitly named request amounts or the anchor.
func topUpConfigAmountFromLegacy(amount decimal.Decimal) decimal.Decimal {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return amount.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	}
	return amount
}

func legacyTopUpPresetOptions() []float64 {
	options := operation_setting.GetPaymentSetting().AmountOptions
	result := make([]float64, 0, len(options))
	if !validQuotaPerUnit() {
		return result
	}
	for _, amount := range options {
		value := decimal.NewFromInt(int64(amount))
		if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
			value = value.Div(decimal.NewFromFloat(common.QuotaPerUnit))
		}
		result = append(result, value.InexactFloat64())
	}
	return result
}

func legacyTopUpDiscountOptions() map[string]float64 {
	result := make(map[string]float64)
	if !validQuotaPerUnit() {
		return result
	}
	for amount, discount := range operation_setting.GetPaymentSetting().AmountDiscount {
		value := decimal.NewFromInt(int64(amount))
		if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
			value = value.Div(decimal.NewFromFloat(common.QuotaPerUnit))
		}
		result[value.String()] = discount
	}
	return result
}

func topUpDiscountQualifyingAmount(amount decimal.Decimal, credits int64) string {
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return decimal.NewFromInt(credits).String()
	}
	return amount.Truncate(6).String()
}

// Cache JSON only in these monetary endpoints so CREDIT integrality is checked
// from the original decimal spelling, before float64 could hide a fraction near
// the JSON-safe wallet boundary. The body is never logged or persisted here.
func bindTopUpRequest(c *gin.Context, target any) error {
	return c.ShouldBindBodyWith(target, binding.JSON)
}

func resolveTopUpRequestAmount(c *gin.Context, value float64, unit string) (resolvedTopUpAmount, error) {
	if amount, canonical := canonicalTopUpCredit(c); canonical {
		return amount, nil
	}
	raw := ""
	if body, exists := c.Get(gin.BodyBytesKey); exists {
		if rawBody, ok := body.([]byte); ok {
			var request struct {
				Amount  json.Number     `json:"amount"`
				Version json.RawMessage `json:"credit_metadata_version"`
			}
			if err := json.Unmarshal(rawBody, &request); err == nil {
				if len(request.Version) > 0 && string(bytes.TrimSpace(request.Version)) != "1" {
					return resolvedTopUpAmount{LegacyBatch: decimal.Zero}, errors.New("此充值入口仅支持 credit_metadata_version 1")
				}
				raw = request.Amount.String()
			}
		}
	} else if c.Request != nil {
		raw = c.PostForm("amount")
		if version, present := c.Request.PostForm["credit_metadata_version"]; present && (len(version) != 1 || version[0] != "1") {
			return resolvedTopUpAmount{LegacyBatch: decimal.Zero}, errors.New("此充值入口仅支持 credit_metadata_version 1")
		}
		if version, present := c.Request.URL.Query()["credit_metadata_version"]; present && (len(version) != 1 || version[0] != "1") {
			return resolvedTopUpAmount{LegacyBatch: decimal.Zero}, errors.New("此充值入口仅支持 credit_metadata_version 1")
		}
		if raw == "" {
			raw = c.Query("amount")
		}
	}
	if raw != "" {
		amount, err := decimal.NewFromString(raw)
		if err != nil {
			return resolvedTopUpAmount{LegacyBatch: decimal.Zero}, errors.New("充值数量无效")
		}
		return resolveTopUpDecimalAmount(amount, unit)
	}
	return resolveTopUpAmount(value, unit)
}
