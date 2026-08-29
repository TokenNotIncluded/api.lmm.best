package controller

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/logger"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/service"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/gin-gonic/gin"
	"github.com/samber/lo"
	"github.com/shopspring/decimal"
)

const monetaryMicrosPerUnit int64 = 1_000_000

var zeroDecimalSettlementCurrencies = map[string]struct{}{
	"BIF": {}, "CLP": {}, "DJF": {}, "GNF": {}, "JPY": {}, "KMF": {},
	"KRW": {}, "MGA": {}, "PYG": {}, "RWF": {}, "UGX": {}, "VND": {},
	"VUV": {}, "XAF": {}, "XOF": {}, "XPF": {},
}

func decimalToMonetaryMicros(value decimal.Decimal) (int64, error) {
	if !value.IsPositive() {
		return 0, fmt.Errorf("settlement amount must be positive")
	}
	scaled := value.Mul(decimal.NewFromInt(monetaryMicrosPerUnit))
	if !scaled.Equal(scaled.Truncate(0)) {
		return 0, fmt.Errorf("settlement amount has more than six decimal places")
	}
	maxInt64 := decimal.NewFromInt(int64(^uint64(0) >> 1))
	if scaled.GreaterThan(maxInt64) {
		return 0, fmt.Errorf("settlement amount is out of range")
	}
	return scaled.IntPart(), nil
}

func monetaryStringToMicros(value string) (int64, error) {
	parsed, err := decimal.NewFromString(strings.TrimSpace(value))
	if err != nil {
		return 0, err
	}
	return decimalToMonetaryMicros(parsed)
}

func monetaryFloatToMicros(value float64) (int64, error) {
	return decimalToMonetaryMicros(decimal.NewFromFloat(value).Round(6))
}

func minorCurrencyUnitsToMicros(amount int64, currency string) (int64, error) {
	if amount <= 0 {
		return 0, fmt.Errorf("settlement amount must be positive")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return 0, fmt.Errorf("settlement currency is required")
	}
	if _, zeroDecimal := zeroDecimalSettlementCurrencies[currency]; zeroDecimal {
		return decimalToMonetaryMicros(decimal.NewFromInt(amount))
	}
	return decimalToMonetaryMicros(decimal.NewFromInt(amount).Div(decimal.NewFromInt(100)))
}

func monetaryMicrosToMinorCurrencyUnits(micros int64, currency string) (int64, error) {
	if micros <= 0 {
		return 0, fmt.Errorf("settlement amount must be positive")
	}
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if currency == "" {
		return 0, fmt.Errorf("settlement currency is required")
	}
	divisor := int64(10_000)
	if _, zeroDecimal := zeroDecimalSettlementCurrencies[currency]; zeroDecimal {
		divisor = monetaryMicrosPerUnit
	}
	if micros%divisor != 0 {
		return 0, fmt.Errorf("settlement amount cannot be represented in %s minor units", currency)
	}
	return micros / divisor, nil
}

func parseRequestedTopUpAmount(value float64) (decimal.Decimal, error) {
	if math.IsNaN(value) || math.IsInf(value, 0) || value <= 0 {
		return decimal.Zero, errors.New("充值数量无效")
	}
	amount := decimal.NewFromFloat(value)
	if _, err := decimalToMonetaryMicros(amount); err != nil {
		return decimal.Zero, errors.New("充值数量最多支持 6 位小数")
	}
	return amount, nil
}

// topUpOrderAmountsDecimal snapshots fractional platform units without float-derived authority.
func topUpOrderAmountsDecimal(requestedAmount decimal.Decimal) (storedAmount int64, platformAmountMicros int64, creditedQuota int64, err error) {
	if !requestedAmount.IsPositive() {
		return 0, 0, 0, errors.New("充值数量无效")
	}
	platformAmount := requestedAmount
	credited := requestedAmount.Mul(decimal.NewFromFloat(common.QuotaPerUnit))
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return 0, 0, 0, errors.New("充值额度配置无效")
		}
		platformAmount = requestedAmount.Div(decimal.NewFromFloat(common.QuotaPerUnit))
		credited = requestedAmount
	}
	storedAmount, ok := decimalInt64Truncated(platformAmount)
	if !ok {
		return 0, 0, 0, errors.New("充值数量超出系统可表示范围")
	}
	platformAmountMicros, err = decimalToMonetaryMicros(platformAmount)
	if err != nil {
		return 0, 0, 0, errors.New("平台充值数量最多支持 6 位小数")
	}
	creditedQuotaInt, err := validateCreditedQuota(credited)
	if err != nil {
		return 0, 0, 0, err
	}
	return storedAmount, platformAmountMicros, int64(creditedQuotaInt), nil
}

func topUpOrderAmounts(requestedAmount int64) (storedAmount int64, creditedQuota int64) {
	storedAmount, _, creditedQuota, err := topUpOrderAmountsDecimal(decimal.NewFromInt(requestedAmount))
	if err != nil {
		return 0, 0
	}
	return storedAmount, creditedQuota
}

func validQuotaPerUnit() bool {
	return common.QuotaPerUnit > 0 && !math.IsNaN(common.QuotaPerUnit) && !math.IsInf(common.QuotaPerUnit, 0)
}

// decimalInt64Truncated keeps the existing truncation semantics while
// checking the arbitrary-precision integer before converting it to int64.
func decimalInt64Truncated(value decimal.Decimal) (int64, bool) {
	integer := value.BigInt()
	if !integer.IsInt64() {
		return 0, false
	}
	return integer.Int64(), true
}

func monetaryMicrosToFloat(micros int64) float64 {
	return decimal.NewFromInt(micros).
		Div(decimal.NewFromInt(monetaryMicrosPerUnit)).
		InexactFloat64()
}

func GetTopUpInfo(c *gin.Context) {
	user, err := model.GetUserById(c.GetInt("id"), false)
	if err != nil {
		common.ApiErrorMsg(c, "获取用户分组失败")
		return
	}
	access, err := model.GetDeveloperAccessStateForUser(user)
	if err != nil {
		common.ApiErrorMsg(c, "获取开发者访问状态失败")
		return
	}
	complianceConfirmed := operation_setting.IsPaymentComplianceConfirmed()
	gatewayAvailability := paymentGatewayAvailabilityForUser(user, complianceConfirmed, time.Now())
	subscriptionAvailability := subscriptionPaymentAvailabilityForUser(user, complianceConfirmed, time.Now())
	if model.IsPaymentRestricted(user) && !gatewayAvailability.hasPayment() && !subscriptionAvailability.hasPayment() {
		common.ApiSuccess(c, neutralTopUpInfo{
			DeveloperAccessGranted:         access.Granted,
			ActivationRequired:             !access.Granted,
			PaymentAvailable:               false,
			PayMethods:                     []map[string]string{},
			AmountOptions:                  []int{},
			Discount:                       map[int]float64{},
			EnableRedemption:               complianceConfirmed,
			PaymentComplianceConfirmed:     complianceConfirmed,
			PaymentComplianceTermsVersion:  operation_setting.CurrentComplianceTermsVersion,
			EnableStripeSubscription:       false,
			EnableCreemSubscription:        false,
			EnableWaffoPancakeSubscription: false,
		})
		return
	}
	if !access.Granted {
		paymentAvailable, minPayment := neutralTopUpAvailability(gatewayAvailability)
		common.ApiSuccess(c, neutralTopUpInfo{
			DeveloperAccessGranted:         false,
			ActivationRequired:             true,
			PaymentAvailable:               paymentAvailable,
			MinPayment:                     minPayment,
			EnableOnlineTopUp:              gatewayAvailability.Online,
			EnableStripeTopUp:              gatewayAvailability.Stripe,
			EnableCreemTopUp:               gatewayAvailability.Creem,
			EnableWaffoTopUp:               gatewayAvailability.Waffo,
			WaffoCurrency:                  waffoSettlementCurrency(),
			WaffoUnitPrice:                 standardUSDPerPlatformUnit(),
			EnableWaffoPancakeTopUp:        gatewayAvailability.WaffoPancake,
			EnableStripeSubscription:       subscriptionAvailability.Stripe,
			EnableCreemSubscription:        subscriptionAvailability.Creem,
			EnableWaffoPancakeSubscription: subscriptionAvailability.WaffoPancake,
			PayMethods:                     sanitizedPaymentMethods(gatewayAvailability.PayMethods),
			CreemProducts:                  setting.CreemProducts,
			WaffoPayMethods: func() interface{} {
				if gatewayAvailability.Waffo {
					return setting.GetWaffoPayMethods()
				}
				return nil
			}(),
			MinTopUp:                      operation_setting.MinTopUp,
			StripeMinTopUp:                setting.StripeMinTopUp,
			WaffoMinTopUp:                 setting.WaffoMinTopUp,
			WaffoPancakeMinTopUp:          setting.WaffoPancakeMinTopUp,
			AmountOptions:                 operation_setting.GetPaymentSetting().AmountOptions,
			Discount:                      operation_setting.GetPaymentSetting().AmountDiscount,
			TopUpLink:                     common.TopUpLink,
			PaymentComplianceConfirmed:    complianceConfirmed,
			PaymentComplianceTermsVersion: operation_setting.CurrentComplianceTermsVersion,
		})
		return
	}

	topupGroupRatio := common.GetTopupGroupRatio(user.Group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	// 获取支付方式
	payMethods := gatewayAvailability.PayMethods

	data := gin.H{
		"developer_access_granted":          true,
		"enable_online_topup":               gatewayAvailability.Online,
		"enable_stripe_topup":               gatewayAvailability.Stripe,
		"enable_creem_topup":                gatewayAvailability.Creem,
		"enable_waffo_topup":                gatewayAvailability.Waffo,
		"waffo_currency":                    waffoSettlementCurrency(),
		"waffo_unit_price":                  standardUSDPerPlatformUnit(),
		"enable_waffo_pancake_topup":        gatewayAvailability.WaffoPancake,
		"enable_stripe_subscription":        subscriptionAvailability.Stripe,
		"enable_creem_subscription":         subscriptionAvailability.Creem,
		"enable_waffo_pancake_subscription": subscriptionAvailability.WaffoPancake,
		"enable_redemption":                 complianceConfirmed,
		"payment_compliance_confirmed":      complianceConfirmed,
		"payment_compliance_terms_version":  operation_setting.CurrentComplianceTermsVersion,
		"waffo_pay_methods": func() interface{} {
			if gatewayAvailability.Waffo {
				return setting.GetWaffoPayMethods()
			}
			return nil
		}(),
		"creem_products":          setting.CreemProducts,
		"pay_methods":             sanitizedPaymentMethods(payMethods),
		"topup_group_ratio":       topupGroupRatio,
		"min_topup":               operation_setting.MinTopUp,
		"stripe_min_topup":        setting.StripeMinTopUp,
		"waffo_min_topup":         setting.WaffoMinTopUp,
		"waffo_pancake_min_topup": setting.WaffoPancakeMinTopUp,
		"amount_options":          operation_setting.GetPaymentSetting().AmountOptions,
		"discount":                operation_setting.GetPaymentSetting().AmountDiscount,
		"topup_link":              common.TopUpLink,
	}
	common.ApiSuccess(c, data)
}

type neutralTopUpInfo struct {
	DeveloperAccessGranted         bool                `json:"developer_access_granted"`
	ActivationRequired             bool                `json:"activation_required"`
	PaymentAvailable               bool                `json:"payment_available"`
	MinPayment                     float64             `json:"min_payment"`
	EnableOnlineTopUp              bool                `json:"enable_online_topup"`
	EnableStripeTopUp              bool                `json:"enable_stripe_topup"`
	EnableCreemTopUp               bool                `json:"enable_creem_topup"`
	EnableWaffoTopUp               bool                `json:"enable_waffo_topup"`
	WaffoCurrency                  string              `json:"waffo_currency,omitempty"`
	WaffoUnitPrice                 float64             `json:"waffo_unit_price,omitempty"`
	EnableWaffoPancakeTopUp        bool                `json:"enable_waffo_pancake_topup"`
	EnableStripeSubscription       bool                `json:"enable_stripe_subscription"`
	EnableCreemSubscription        bool                `json:"enable_creem_subscription"`
	EnableWaffoPancakeSubscription bool                `json:"enable_waffo_pancake_subscription"`
	EnableRedemption               bool                `json:"enable_redemption"`
	PayMethods                     []map[string]string `json:"pay_methods"`
	CreemProducts                  string              `json:"creem_products"`
	WaffoPayMethods                interface{}         `json:"waffo_pay_methods"`
	MinTopUp                       int                 `json:"min_topup"`
	StripeMinTopUp                 int                 `json:"stripe_min_topup"`
	WaffoMinTopUp                  int                 `json:"waffo_min_topup"`
	WaffoPancakeMinTopUp           int                 `json:"waffo_pancake_min_topup"`
	TopUpLink                      string              `json:"topup_link"`
	AmountOptions                  []int               `json:"amount_options"`
	Discount                       map[int]float64     `json:"discount"`
	PaymentComplianceConfirmed     bool                `json:"payment_compliance_confirmed"`
	PaymentComplianceTermsVersion  string              `json:"payment_compliance_terms_version"`
}

type subscriptionPaymentAvailability struct {
	Stripe       bool
	Creem        bool
	WaffoPancake bool
}

func (availability subscriptionPaymentAvailability) hasPayment() bool {
	return availability.Stripe || availability.Creem || availability.WaffoPancake
}

// subscriptionPaymentAvailabilityForUser is deliberately separate from
// paymentGatewayAvailabilityForUser: wallet top-up providers may require a
// global product, while subscription plans carry their own provider product
// IDs. The same audience and registration-delay rules still apply.
func subscriptionPaymentAvailabilityForUser(user *model.User, complianceConfirmed bool, now time.Time) subscriptionPaymentAvailability {
	if !complianceConfirmed || user == nil {
		return subscriptionPaymentAvailability{}
	}
	return subscriptionPaymentAvailability{
		Stripe:       isStripeSubscriptionPaymentEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodStripe, now),
		Creem:        isCreemSubscriptionPaymentEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodCreem, now),
		WaffoPancake: isWaffoPancakeSubscriptionPaymentEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodWaffoPancake, now),
	}
}

// availablePaymentMethods returns the operator-configured catalog plus any
// dedicated gateways that are enabled at runtime. The catalog contains only
// display/quote metadata; gateway credentials remain server-side.
func availablePaymentMethods(complianceConfirmed bool) []map[string]string {
	if !complianceConfirmed {
		return []map[string]string{}
	}

	payMethods := make([]map[string]string, 0, len(operation_setting.PayMethods)+3)
	for _, method := range operation_setting.PayMethods {
		// Clone the operator catalog before adding server-owned metadata.
		payMethods = append(payMethods, clonePaymentMethod(method))
	}
	appendIfMissing := func(method map[string]string) {
		for _, existing := range payMethods {
			if existing["type"] == method["type"] {
				return
			}
		}
		payMethods = append(payMethods, method)
	}

	if isStripeTopUpEnabled() {
		appendIfMissing(map[string]string{
			"name":      "Stripe",
			"type":      "stripe",
			"color":     "#635BFF",
			"min_topup": strconv.Itoa(setting.StripeMinTopUp),
		})
	}
	if isWaffoPancakeTopUpEnabled() {
		appendIfMissing(map[string]string{
			"name":      "Waffo Pancake",
			"type":      model.PaymentMethodWaffoPancake,
			"color":     "#F97316",
			"min_topup": strconv.Itoa(setting.WaffoPancakeMinTopUp),
		})
	}
	if isWaffoTopUpEnabled() {
		appendIfMissing(map[string]string{
			"name":      "Waffo (Global Payment)",
			"type":      model.PaymentMethodWaffo,
			"color":     "#3B82F6",
			"min_topup": strconv.Itoa(setting.WaffoMinTopUp),
		})
	}

	// Publish the same currency contract used by server checkout. Dedicated
	// gateways cannot inherit stale UnitPrice/direct-rate fields from old
	// settings, otherwise the UI preview and provider charge diverge.
	for _, method := range payMethods {
		switch method["type"] {
		case "stripe", model.PaymentMethodWaffoPancake, model.PaymentMethodWaffo:
			setPaymentMethodStandardPricing(method, "USD")
		case "alipay", "wxpay":
			setPaymentMethodStandardPricing(method, "CNY")
		default:
			if legacyPrice := strings.TrimSpace(method["unit_price"]); legacyPrice != "" && strings.TrimSpace(method["settlement_units_per_platform_unit"]) == "" {
				method["settlement_units_per_platform_unit"] = legacyPrice
			}
			if strings.TrimSpace(method["settlement_units_per_usd"]) != "" && strings.TrimSpace(method["platform_units_per_usd"]) == "" {
				if platformRate, err := configuredPlatformUnitsPerUSD(); err == nil {
					method["platform_units_per_usd"] = platformRate.String()
				}
			}
		}
	}
	return payMethods
}

func clonePaymentMethod(method map[string]string) map[string]string {
	clone := make(map[string]string, len(method)+2)
	for key, value := range method {
		clone[key] = value
	}
	return clone
}

func waffoSettlementCurrency() string {
	return "USD"
}

func standardUSDPerPlatformUnit() float64 {
	platformUnitsPerUSD, err := configuredPlatformUnitsPerUSD()
	if err != nil || !platformUnitsPerUSD.IsPositive() {
		return 0
	}
	return decimal.NewFromInt(1).Div(platformUnitsPerUSD).InexactFloat64()
}

func setPaymentMethodStandardPricing(method map[string]string, currency string) {
	for _, legacyKey := range []string{
		"unit_price",
		"settlement_units_per_platform_unit",
		"platform_units_per_usd",
		"settlement_units_per_usd",
	} {
		delete(method, legacyKey)
	}
	pricing, err := standardSettlementPricing(currency)
	if err != nil {
		return
	}
	method["settlement_currency"] = strings.ToUpper(currency)
	method["settlement_unit"] = strings.ToUpper(currency)
	method["platform_units_per_usd"] = pricing.platformUnitsPerUSD.String()
	method["settlement_units_per_usd"] = pricing.settlementUnitsPerUSD.String()
}

func isLegacyLinuxDOCreditMethod(method map[string]string) bool {
	if !strings.EqualFold(strings.TrimSpace(method["type"]), "epay") {
		return false
	}
	name := strings.ToLower(strings.TrimSpace(method["name"]))
	return strings.Contains(name, "ldc") || strings.Contains(name, "linuxdo") || strings.Contains(name, "linux do")
}

func sanitizedPaymentMethods(methods []map[string]string) []map[string]string {
	allowed := map[string]struct{}{
		"name": {}, "type": {}, "icon": {}, "color": {}, "min_topup": {},
		"max_topup": {}, "description": {}, "settlement_currency": {}, "settlement_unit": {}, "unit_price": {}, "topup_ratio": {},
		"platform_units_per_usd": {}, "settlement_units_per_usd": {}, "settlement_units_per_platform_unit": {},
	}
	result := make([]map[string]string, 0, len(methods))
	for _, method := range methods {
		public := make(map[string]string)
		for key, value := range method {
			if _, ok := allowed[key]; ok {
				public[key] = value
			}
		}
		if public["name"] == "" || public["type"] == "" {
			continue
		}
		hasExplicitPricing := strings.TrimSpace(public["platform_units_per_usd"]) != "" ||
			strings.TrimSpace(public["settlement_units_per_usd"]) != "" ||
			strings.TrimSpace(public["settlement_units_per_platform_unit"]) != "" ||
			strings.TrimSpace(public["unit_price"]) != ""
		currency := strings.ToUpper(strings.TrimSpace(public["settlement_currency"]))
		if currency == "" {
			currency = strings.ToUpper(strings.TrimSpace(public["settlement_unit"]))
		}
		builtInEpay := public["type"] == "alipay" || public["type"] == "wxpay"
		if builtInEpay {
			currency = paymentpricing.CurrencyCNY
			hasExplicitPricing = false
			delete(public, "settlement_unit")
			delete(public, "unit_price")
			delete(public, "settlement_units_per_platform_unit")
		}
		// PayMethods use Epay and therefore default to real CNY. The historical
		// type=epay/name=LDC entry is a non-fiat LinuxDO credit rail and must never
		// receive cash settlement metadata.
		if currency == "" && !hasExplicitPricing && !isLegacyLinuxDOCreditMethod(public) {
			currency = paymentpricing.CurrencyCNY
		}
		if !hasExplicitPricing && (currency == paymentpricing.CurrencyCNY || currency == paymentpricing.CurrencyUSD) {
			if pricing, err := standardSettlementPricing(currency); err == nil {
				public["settlement_currency"] = currency
				public["platform_units_per_usd"] = pricing.platformUnitsPerUSD.String()
				public["settlement_units_per_usd"] = pricing.settlementUnitsPerUSD.String()
			}
		}
		result = append(result, public)
	}
	return result
}

type paymentGatewayAvailability struct {
	Online       bool
	Stripe       bool
	Creem        bool
	Waffo        bool
	WaffoPancake bool
	PayMethods   []map[string]string
}

func (availability paymentGatewayAvailability) hasPayment() bool {
	return availability.Online || availability.Stripe || availability.Creem || availability.Waffo || availability.WaffoPancake
}

func paymentGatewayAvailabilityForUser(user *model.User, complianceConfirmed bool, now time.Time) paymentGatewayAvailability {
	methods := availablePaymentMethods(complianceConfirmed)
	unlockedMethods := make([]map[string]string, 0, len(methods))
	for _, method := range methods {
		if isPaymentMethodAvailableForUser(user, method["type"], now) {
			unlockedMethods = append(unlockedMethods, method)
		}
	}

	availability := paymentGatewayAvailability{
		Stripe:       isStripeTopUpEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodStripe, now),
		Creem:        isCreemTopUpEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodCreem, now),
		Waffo:        isWaffoTopUpEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodWaffo, now),
		WaffoPancake: isWaffoPancakeTopUpEnabled() && isPaymentMethodAvailableForUser(user, model.PaymentMethodWaffoPancake, now),
		PayMethods:   unlockedMethods,
	}
	for _, method := range unlockedMethods {
		paymentType := method["type"]
		switch paymentType {
		case model.PaymentMethodStripe, model.PaymentMethodCreem, model.PaymentMethodWaffo, model.PaymentMethodWaffoPancake:
			continue
		}
		if isEpayTopUpEnabled() {
			availability.Online = true
			break
		}
	}
	return availability
}

func neutralTopUpAvailability(availability paymentGatewayAvailability) (bool, float64) {
	minimums := make([]float64, 0, 5)
	addMinimum := func(enabled bool, value float64) {
		if enabled && value > 0 {
			minimums = append(minimums, value)
		}
	}

	onlineEnabled := availability.Online
	stripeEnabled := availability.Stripe
	creemEnabled := availability.Creem
	waffoEnabled := availability.Waffo
	pancakeEnabled := availability.WaffoPancake
	addMinimum(onlineEnabled, float64(operation_setting.MinTopUp))
	addMinimum(stripeEnabled, float64(setting.StripeMinTopUp))
	addMinimum(waffoEnabled, float64(setting.WaffoMinTopUp))
	addMinimum(pancakeEnabled, float64(setting.WaffoPancakeMinTopUp))
	if creemEnabled {
		var products []CreemProduct
		if err := json.Unmarshal([]byte(setting.CreemProducts), &products); err == nil {
			for _, product := range products {
				addMinimum(true, product.Price)
			}
		}
	}

	if len(minimums) == 0 {
		return onlineEnabled || stripeEnabled || creemEnabled || waffoEnabled || pancakeEnabled, 0
	}
	minimum := minimums[0]
	for _, candidate := range minimums[1:] {
		if candidate < minimum {
			minimum = candidate
		}
	}
	return true, minimum
}

func getTopupUserGroup(id int) (string, error) {
	user, err := model.GetUserById(id, true)
	if err != nil {
		return "", err
	}
	return user.Group, nil
}

type EpayRequest struct {
	Amount        float64 `json:"amount"`
	PaymentMethod string  `json:"payment_method"`
	DiscountCode  string  `json:"discount_code,omitempty"`
}

type AmountRequest struct {
	Amount        int64  `json:"amount"`
	PaymentMethod string `json:"payment_method"`
	DiscountCode  string `json:"discount_code,omitempty"`
}

func GetEpayClient() *epay.Client {
	if operation_setting.PayAddress == "" || operation_setting.EpayId == "" || operation_setting.EpayKey == "" {
		return nil
	}
	withUrl, err := epay.NewClient(&epay.Config{
		PartnerID: operation_setting.EpayId,
		Key:       operation_setting.EpayKey,
	}, operation_setting.PayAddress)
	if err != nil {
		return nil
	}
	return withUrl
}

var positiveDecimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var nonNegativeDecimalPattern = regexp.MustCompile(`^[0-9]+(?:\.[0-9]+)?$`)
var settlementUnitPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,16}$`)

func integerPlatformAmount(value float64) (int64, error) {
	decimalAmount := decimal.NewFromFloat(value)
	if !decimalAmount.Equal(decimalAmount.Truncate(0)) {
		return 0, fmt.Errorf("platform amount must be an integer")
	}
	converted, ok := decimalInt64Truncated(decimalAmount)
	if !ok {
		return 0, fmt.Errorf("platform amount is out of range")
	}
	return converted, nil
}

func parsePayRequest(c *gin.Context, amount *float64, paymentMethod, discountCode *string) error {
	if value, exists := c.Get("parsed_amount"); exists {
		if parsed, ok := value.(float64); ok && parsed > 0 {
			*amount = parsed
		}
	}
	if value, exists := c.Get("parsed_payment_method"); exists {
		if parsed, ok := value.(string); ok && parsed != "" {
			*paymentMethod = parsed
		}
	}
	if value, exists := c.Get("parsed_discount_code"); exists {
		if parsed, ok := value.(string); ok {
			*discountCode = parsed
		}
	}
	if *amount > 0 && *paymentMethod != "" {
		return nil
	}

	var request struct {
		Amount        float64 `json:"amount" form:"amount"`
		PaymentMethod string  `json:"payment_method" form:"payment_method"`
		DiscountCode  string  `json:"discount_code" form:"discount_code"`
	}
	_ = c.ShouldBind(&request)
	if *amount <= 0 && request.Amount > 0 {
		*amount = request.Amount
	}
	if *paymentMethod == "" && request.PaymentMethod != "" {
		*paymentMethod = request.PaymentMethod
	}
	if *discountCode == "" && request.DiscountCode != "" {
		*discountCode = request.DiscountCode
	}
	if *amount <= 0 {
		if raw := c.PostForm("amount"); raw != "" {
			*amount, _ = strconv.ParseFloat(raw, 64)
		} else if raw := c.Query("amount"); raw != "" {
			*amount, _ = strconv.ParseFloat(raw, 64)
		}
	}
	if *paymentMethod == "" {
		if value := c.PostForm("payment_method"); value != "" {
			*paymentMethod = value
		} else if value := c.Query("payment_method"); value != "" {
			*paymentMethod = value
		}
	}
	if *amount <= 0 {
		return fmt.Errorf("amount is required and must be > 0")
	}
	return nil
}

func getPayMethod(paymentMethod string) (map[string]string, error) {
	enabled, err := configuredPaymentMethodEnabled(paymentMethod)
	if err != nil {
		return nil, err
	}
	if !enabled {
		return nil, fmt.Errorf("payment method %q is disabled", paymentMethod)
	}
	for _, payMethod := range operation_setting.PayMethods {
		if payMethod["type"] == paymentMethod {
			return payMethod, nil
		}
	}
	return nil, fmt.Errorf("payment method %q does not exist", paymentMethod)
}

// getPayMethodSettlementUnit returns the real fiat currency charged by the
// selected gateway. Epay's alipay/wxpay contracts are CNY-only: accepting a
// configurable USD label here would turn a correctly converted USD number
// back into CNY at checkout (for example 1 USD becoming 1 CNY).
func getPayMethodSettlementUnit(paymentMethod string) (string, error) {
	payMethod, err := getPayMethod(paymentMethod)
	if err != nil {
		return "", err
	}
	settlementUnit := strings.ToUpper(strings.TrimSpace(payMethod["settlement_currency"]))
	if settlementUnit == "" {
		settlementUnit = strings.ToUpper(strings.TrimSpace(payMethod["settlement_unit"]))
	}
	if isLegacyLinuxDOCreditMethod(payMethod) && settlementUnit == "" {
		return "", fmt.Errorf("payment method %q is non-fiat LinuxDO credit without an explicit unit", paymentMethod)
	}
	if paymentMethod == "alipay" || paymentMethod == "wxpay" {
		// Epay's protocol amount is CNY regardless of any stale operator label.
		// Forcing the physical contract here both migrates old USD-tagged rows
		// and prevents an already-converted USD number from being sent as CNY.
		return "CNY", nil
	}
	if settlementUnit == "" {
		// PayMethods are sent through the Epay protocol, whose physical amount
		// defaults to CNY. Virtual/non-CNY methods must opt in explicitly.
		return "CNY", nil
	}
	if !settlementUnitPattern.MatchString(settlementUnit) {
		return "", fmt.Errorf("payment method %q has invalid settlement_unit", paymentMethod)
	}
	return settlementUnit, nil
}

type payMethodSettlementPricing struct {
	platformUnitsPerUSD                decimal.Decimal
	settlementUnitsPerUSD              decimal.Decimal
	settlementUnitsPerPlatformUnit     decimal.Decimal
	usesSettlementUnitsPerPlatformUnit bool
}

func parsePositivePaymentRate(paymentMethod, field, raw string) (decimal.Decimal, error) {
	if !positiveDecimalPattern.MatchString(raw) {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid %s", paymentMethod, field)
	}
	rate, err := decimal.NewFromString(raw)
	if err != nil || !rate.IsPositive() {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid %s", paymentMethod, field)
	}
	return rate, nil
}

// configuredPlatformUnitsPerUSD derives the purchase rate from two independent
// global facts:
//
//   - USDExchangeRate: real CNY per fiat USD
//   - TopUpPlatformUnitsPerCNY: platform units bought by one CNY
//
// Display mode is deliberately irrelevant. Platform units are accounting
// credits, not fiat USD, even when the UI uses a dollar-like symbol.
func configuredPlatformUnitsPerUSD() (decimal.Decimal, error) {
	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return decimal.Zero, err
	}
	return rates.PlatformUnitsPerUSD()
}

func standardSettlementPricing(settlementCurrency string) (payMethodSettlementPricing, error) {
	rates, err := paymentpricing.CurrentRates()
	if err != nil {
		return payMethodSettlementPricing{}, err
	}
	platformUnitsPerUSD, err := rates.PlatformUnitsPerUSD()
	if err != nil {
		return payMethodSettlementPricing{}, err
	}
	var settlementUnitsPerUSD decimal.Decimal
	switch strings.ToUpper(strings.TrimSpace(settlementCurrency)) {
	case paymentpricing.CurrencyUSD:
		settlementUnitsPerUSD = decimal.NewFromInt(1)
	case paymentpricing.CurrencyCNY:
		settlementUnitsPerUSD = rates.CNYPerUSD
	default:
		return payMethodSettlementPricing{}, fmt.Errorf("unsupported standard settlement currency %q", settlementCurrency)
	}
	return payMethodSettlementPricing{
		platformUnitsPerUSD:   platformUnitsPerUSD,
		settlementUnitsPerUSD: settlementUnitsPerUSD,
	}, nil
}

// getPayMethodSettlementPricing accepts explicit pricing for genuinely custom
// gateways. Built-in Epay methods always use the global CNY contract so stale
// unit_price fields cannot bypass the recharge ratio or apply USD conversion to
// a CNY checkout.
func getPayMethodSettlementPricing(paymentMethod string) (payMethodSettlementPricing, error) {
	payMethod, err := getPayMethod(paymentMethod)
	if err != nil {
		return payMethodSettlementPricing{}, err
	}
	settlementUnit, err := getPayMethodSettlementUnit(paymentMethod)
	if err != nil {
		return payMethodSettlementPricing{}, err
	}
	if paymentMethod == "alipay" || paymentMethod == "wxpay" {
		return standardSettlementPricing("CNY")
	}

	platformRaw, hasPlatformRate := payMethod["platform_units_per_usd"]
	settlementRaw, hasSettlementRate := payMethod["settlement_units_per_usd"]
	directRaw, hasDirectRate := payMethod["settlement_units_per_platform_unit"]
	legacyRaw, hasLegacyRate := payMethod["unit_price"]
	if !hasPlatformRate && !hasSettlementRate && !hasDirectRate && !hasLegacyRate {
		return standardSettlementPricing(settlementUnit)
	}
	if hasPlatformRate && !hasSettlementRate {
		return payMethodSettlementPricing{}, fmt.Errorf("payment method %q configures platform_units_per_usd without settlement_units_per_usd", paymentMethod)
	}
	if hasSettlementRate && (hasDirectRate || hasLegacyRate) {
		return payMethodSettlementPricing{}, fmt.Errorf("payment method %q mixes FX and per-platform-unit pricing", paymentMethod)
	}

	if hasSettlementRate {
		var platformRate decimal.Decimal
		if hasPlatformRate {
			platformRate, err = parsePositivePaymentRate(paymentMethod, "platform_units_per_usd", platformRaw)
		} else {
			platformRate, err = configuredPlatformUnitsPerUSD()
			if err != nil {
				return payMethodSettlementPricing{}, fmt.Errorf("payment method %q requires a configured platform USD rate: %w", paymentMethod, err)
			}
		}
		settlementRate, err := parsePositivePaymentRate(paymentMethod, "settlement_units_per_usd", settlementRaw)
		if err != nil {
			return payMethodSettlementPricing{}, err
		}
		return payMethodSettlementPricing{
			platformUnitsPerUSD:   platformRate,
			settlementUnitsPerUSD: settlementRate,
		}, nil
	}

	if !hasDirectRate && !hasLegacyRate {
		return payMethodSettlementPricing{}, fmt.Errorf("payment method %q has no explicit settlement pricing", paymentMethod)
	}
	if !hasDirectRate {
		directRaw = legacyRaw
	}
	directRate, err := parsePositivePaymentRate(paymentMethod, "settlement_units_per_platform_unit", directRaw)
	if err != nil {
		return payMethodSettlementPricing{}, err
	}
	if hasDirectRate && hasLegacyRate {
		legacyRate, err := parsePositivePaymentRate(paymentMethod, "unit_price", legacyRaw)
		if err != nil {
			return payMethodSettlementPricing{}, err
		}
		if !directRate.Equal(legacyRate) {
			return payMethodSettlementPricing{}, fmt.Errorf("payment method %q has conflicting per-platform-unit rates", paymentMethod)
		}
	}
	return payMethodSettlementPricing{
		settlementUnitsPerPlatformUnit:     directRate,
		usesSettlementUnitsPerPlatformUnit: true,
	}, nil
}

func settlementAmountForPlatformAmount(platformAmount decimal.Decimal, pricing payMethodSettlementPricing) (decimal.Decimal, error) {
	if !platformAmount.IsPositive() {
		return decimal.Zero, fmt.Errorf("platform amount must be positive")
	}
	if pricing.usesSettlementUnitsPerPlatformUnit {
		if !pricing.settlementUnitsPerPlatformUnit.IsPositive() {
			return decimal.Zero, fmt.Errorf("settlement units per platform unit must be positive")
		}
		return platformAmount.Mul(pricing.settlementUnitsPerPlatformUnit), nil
	}
	if !pricing.platformUnitsPerUSD.IsPositive() || !pricing.settlementUnitsPerUSD.IsPositive() {
		return decimal.Zero, fmt.Errorf("USD settlement rates must be positive")
	}
	return platformAmount.
		Div(pricing.platformUnitsPerUSD).
		Mul(pricing.settlementUnitsPerUSD), nil
}

// getPayMethodTopupRatio returns the optional payment-method multiplier. It is
// combined with the user's group multiplier, while legacy methods default to 1.
func getPayMethodTopupRatio(paymentMethod string) (decimal.Decimal, error) {
	payMethod, err := getPayMethod(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	topupRatio, configured := payMethod["topup_ratio"]
	if !configured {
		return decimal.NewFromInt(1), nil
	}
	if !positiveDecimalPattern.MatchString(topupRatio) {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid topup_ratio", paymentMethod)
	}
	ratio, err := decimal.NewFromString(topupRatio)
	if err != nil || !ratio.IsPositive() {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid topup_ratio", paymentMethod)
	}
	return ratio, nil
}

// quoteTopUp is the single server-authoritative online-top-up quote. It is
// intentionally shared by quote and checkout endpoints so a client cannot see
// one amount and create an order at another amount. FX pricing follows:
//
// settlement = platform amount / platform units per USD * settlement units per USD
//
// The group ratio, optional payment-method ratio, and amount discount are then
// applied to the settlement amount. Legacy unit_price is direct pricing and is
// accepted only as settlement units per platform unit.
func quoteTopUp(amount int64, group, paymentMethod string) (decimal.Decimal, error) {
	return quoteTopUpDecimal(decimal.NewFromInt(amount), group, paymentMethod)
}

func quoteTopUpDecimal(amount decimal.Decimal, group, paymentMethod string) (decimal.Decimal, error) {
	pricing, err := getPayMethodSettlementPricing(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	dTopupRatio, err := getPayMethodTopupRatio(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	return quoteTopUpDecimalWithSettlementPricing(amount, group, pricing, dTopupRatio)
}

func quoteTopUpWithSettlementPricing(amount int64, group string, pricing payMethodSettlementPricing, dPaymentRatio decimal.Decimal) (decimal.Decimal, error) {
	return quoteTopUpDecimalWithSettlementPricing(decimal.NewFromInt(amount), group, pricing, dPaymentRatio)
}

func quoteTopUpDecimalWithSettlementPricing(requestedAmount decimal.Decimal, group string, pricing payMethodSettlementPricing, dPaymentRatio decimal.Decimal) (decimal.Decimal, error) {
	dAmount := requestedAmount
	// 充值金额以“展示类型”为准：
	// - USD/CNY: 前端传 amount 为金额单位；TOKENS: 前端传 tokens，需要换成 USD 金额
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		dAmount = dAmount.Div(dQuotaPerUnit)
	}

	topupGroupRatio := common.GetTopupGroupRatio(group)
	if topupGroupRatio == 0 {
		topupGroupRatio = 1
	}

	dTopupGroupRatio := decimal.NewFromFloat(topupGroupRatio)
	// apply optional preset discount by the original request amount (if configured), default 1.0
	discount := 1.0
	if requestedAmount.Equal(requestedAmount.Truncate(0)) && requestedAmount.IsInteger() {
		if key, ok := decimalInt64Truncated(requestedAmount); ok {
			if ds, exists := operation_setting.GetPaymentSetting().AmountDiscount[int(key)]; exists && ds > 0 {
				discount = ds
			}
		}
	}
	dDiscount := decimal.NewFromFloat(discount)

	settlementAmount, err := settlementAmountForPlatformAmount(dAmount, pricing)
	if err != nil {
		return decimal.Zero, err
	}
	return settlementAmount.
		Mul(dTopupGroupRatio).
		Mul(dPaymentRatio).
		Mul(dDiscount).
		Round(2), nil
}

func getMinTopup() int64 {
	minTopup := operation_setting.MinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return int64(common.MaxWalletQuota)
		}
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		converted, ok := decimalInt64Truncated(dMinTopup.Mul(dQuotaPerUnit))
		if !ok || converted < 0 || converted > int64(common.MaxWalletQuota) {
			return int64(common.MaxWalletQuota)
		}
		minTopup = int(converted)
	}
	return int64(minTopup)
}

func getTopUpQuota(amount int64) (int, error) {
	if !validQuotaPerUnit() {
		return 0, errors.New("QuotaPerUnit 必须为有限正数")
	}
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	quota := decimal.NewFromInt(amount)
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		quota = quota.Div(quotaPerUnit).Truncate(0).Mul(quotaPerUnit)
	} else {
		quota = quota.Mul(quotaPerUnit)
	}
	return common.WalletQuotaFromDecimalStrict(quota)
}

func nonNegativeDecimalInt64OrMax(value decimal.Decimal) int64 {
	converted, ok := decimalInt64Truncated(value)
	if ok {
		if converted < 0 {
			return 0
		}
		return converted
	}
	if value.IsPositive() {
		return math.MaxInt64
	}
	return 0
}

func getMaxTopUpAmount() int64 {
	if !validQuotaPerUnit() {
		return 0
	}
	quotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
	maxStoredAmount := decimal.NewFromInt(common.MaxWalletQuota).
		Div(quotaPerUnit).
		Floor()
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		return nonNegativeDecimalInt64OrMax(
			maxStoredAmount.Add(decimal.NewFromInt(1)).
				Mul(quotaPerUnit).
				Ceil().
				Sub(decimal.NewFromInt(1)),
		)
	}
	return nonNegativeDecimalInt64OrMax(maxStoredAmount)
}

func validateCreditedQuota(quota decimal.Decimal) (int, error) {
	value, err := common.WalletQuotaFromDecimalStrict(quota)
	if err != nil {
		return 0, errors.New("充值额度超出系统可表示范围")
	}
	if value <= 0 {
		return 0, errors.New("充值额度必须大于 0")
	}
	return value, nil
}

func validateTopUpQuota(amount int64) (int, error) {
	quota, err := getTopUpQuota(amount)
	if err == nil && quota > 0 {
		return quota, nil
	}
	maxAmount := getMaxTopUpAmount()
	if maxAmount > 0 && amount > maxAmount {
		return 0, fmt.Errorf("单笔充值数量不能大于 %d", maxAmount)
	}
	return 0, errors.New("充值数量无效")
}

func rejectInvalidCreditedQuota(c *gin.Context, userId int, quota decimal.Decimal) bool {
	creditedQuota, err := validateCreditedQuota(quota)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userId, int64(creditedQuota))
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return true
	}
	return false
}

func rejectInvalidTopUpQuota(c *gin.Context, userId int, amount int64) bool {
	creditedQuota, err := validateTopUpQuota(amount)
	if err == nil {
		err = model.ValidateTopUpQuotaCapacity(userId, int64(creditedQuota))
	}
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return true
	}
	return false
}

func RequestEpay(c *gin.Context) {
	var req EpayRequest
	if err := parsePayRequest(c, &req.Amount, &req.PaymentMethod, &req.DiscountCode); err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Epay 参数解包失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("参数错误: %s", err.Error())})
		return
	}
	requestedAmount, err := parseRequestedTopUpAmount(req.Amount)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return
	}
	if requestedAmount.LessThan(decimal.NewFromInt(getMinTopup())) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}
	if !requirePaymentMethodAvailable(c, req.PaymentMethod) {
		return
	}
	if !requirePaymentMethodTopUpDecimalWithinLimit(c, req.PaymentMethod, requestedAmount) {
		return
	}
	settlementCurrency, err := getPayMethodSettlementUnit(req.PaymentMethod)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return
	}

	id := c.GetInt("id")
	amount, platformAmountMicros, creditedQuota, err := topUpOrderAmountsDecimal(requestedAmount)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": err.Error()})
		return
	}
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	group, err := getTopupUserGroup(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney, discountCode, err := quoteTopUpDecimalWithDiscount(requestedAmount, group, req.PaymentMethod, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return
	}
	if payMoney.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	callBackAddress := service.GetCallbackAddress()
	returnUrl, _ := url.Parse(paymentReturnPath("/usage-logs"))
	notifyUrl, _ := url.Parse(callBackAddress + "/api/user/epay/notify")
	tradeNo := fmt.Sprintf("%s%d", common.GetRandomString(6), time.Now().Unix())
	tradeNo = fmt.Sprintf("USR%dNO%s", id, tradeNo)
	client := GetEpayClient()
	if client == nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "当前管理员未配置支付信息"})
		return
	}
	expectedAmountMicros, err := monetaryStringToMicros(payMoney.StringFixed(2))
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 结算金额无效 user_id=%d trade_no=%s error=%q", id, tradeNo, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付金额无效"})
		return
	}
	topUp := &model.TopUp{
		UserId:               id,
		Amount:               amount,
		PlatformAmountMicros: platformAmountMicros,
		CreditedQuota:        creditedQuota,
		ExpectedAmountMicros: expectedAmountMicros,
		Money:                monetaryMicrosToFloat(expectedAmountMicros),
		TradeNo:              tradeNo,
		PaymentMethod:        req.PaymentMethod,
		PaymentProvider:      model.PaymentProviderEpay,
		SettlementCurrency:   settlementCurrency,
		DiscountCodeId:       discountCodeID(discountCode),
		DiscountPercent:      discountPercent(discountCode),
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
	}
	if err := topUp.Insert(); err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 创建充值订单失败 user_id=%d trade_no=%s payment_method=%s amount=%s error=%q", id, tradeNo, req.PaymentMethod, requestedAmount.String(), err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}

	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("TUC%s", requestedAmount.String()),
		Money:          payMoney.StringFixed(2),
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 拉起支付失败 user_id=%d trade_no=%s payment_method=%s amount=%s error=%q", id, tradeNo, req.PaymentMethod, requestedAmount.String(), err.Error()))
		// The gateway may have accepted the order before the transport failed.
		// Preserve pending so a signed callback can still settle it.
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 充值订单创建成功 user_id=%d trade_no=%s payment_method=%s amount=%s money=%s", id, tradeNo, req.PaymentMethod, requestedAmount.String(), payMoney.StringFixed(2)))
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": params, "url": uri})
}

// tradeNo lock
var orderLocks sync.Map
var createLock sync.Mutex

// refCountedMutex 带引用计数的互斥锁，确保最后一个使用者才从 map 中删除
type refCountedMutex struct {
	mu       sync.Mutex
	refCount int
}

// LockOrder 尝试对给定订单号加锁
func LockOrder(tradeNo string) {
	createLock.Lock()
	var rcm *refCountedMutex
	if v, ok := orderLocks.Load(tradeNo); ok {
		rcm = v.(*refCountedMutex)
	} else {
		rcm = &refCountedMutex{}
		orderLocks.Store(tradeNo, rcm)
	}
	rcm.refCount++
	createLock.Unlock()
	rcm.mu.Lock()
}

// UnlockOrder 释放给定订单号的锁
func UnlockOrder(tradeNo string) {
	v, ok := orderLocks.Load(tradeNo)
	if !ok {
		return
	}
	rcm := v.(*refCountedMutex)
	rcm.mu.Unlock()

	createLock.Lock()
	rcm.refCount--
	if rcm.refCount == 0 {
		orderLocks.Delete(tradeNo)
	}
	createLock.Unlock()
}

func EpayNotify(c *gin.Context) {
	if !isEpayWebhookEnabled() {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 被拒绝 reason=webhook_disabled client_ip=%s", c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}

	var params map[string]string

	if c.Request.Method == "POST" {
		// POST 请求：从 POST body 解析参数
		if err := c.Request.ParseForm(); err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook POST 表单解析失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		params = lo.Reduce(lo.Keys(c.Request.PostForm), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.PostForm.Get(t)
			return r
		}, map[string]string{})
	} else {
		// GET 请求：从 URL Query 解析参数
		params = lo.Reduce(lo.Keys(c.Request.URL.Query()), func(r map[string]string, t string, i int) map[string]string {
			r[t] = c.Request.URL.Query().Get(t)
			return r
		}, map[string]string{})
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 收到请求 client_ip=%s method=%s", c.ClientIP(), c.Request.Method))

	if len(params) == 0 {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 参数为空 client_ip=%s", c.ClientIP()))
		_, _ = c.Writer.Write([]byte("fail"))
		return
	}
	client := GetEpayClient()
	if client == nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 client 未初始化 client_ip=%s", c.ClientIP()))
		_, err := c.Writer.Write([]byte("fail"))
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		}
		return
	}
	verifyInfo, err := client.Verify(params)
	if err != nil || !verifyInfo.VerifyStatus {
		if _, writeErr := c.Writer.Write([]byte("fail")); writeErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 client_ip=%s error=%q", c.ClientIP(), writeErr.Error()))
		}
		if err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 client_ip=%s verify_error=%q", c.ClientIP(), err.Error()))
		} else {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 client_ip=%s verify_status=false", c.ClientIP()))
		}
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签成功 trade_no=%s callback_type=%s trade_status=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, c.ClientIP()))

	if verifyInfo.TradeStatus == epay.StatusTradeSuccess {
		// 进程内锁只是优化；重复/并发回调的正确性由 RechargeEpay 的
		// 数据库行锁 + 事务内状态校验保证（多实例部署下同样安全）。
		LockOrder(verifyInfo.ServiceTradeNo)
		defer UnlockOrder(verifyInfo.ServiceTradeNo)
		topUp := model.GetTopUpByTradeNo(verifyInfo.ServiceTradeNo)
		if topUp == nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 回调订单不存在 trade_no=%s callback_type=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, c.ClientIP()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		if topUp.PaymentProvider != model.PaymentProviderEpay {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 订单支付网关不匹配 trade_no=%s order_provider=%s callback_type=%s client_ip=%s", verifyInfo.ServiceTradeNo, topUp.PaymentProvider, verifyInfo.Type, c.ClientIP()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		shouldCredit, callbackErr := validateEpayCallback(topUp, verifyInfo)
		if callbackErr != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 回调与订单不匹配 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), callbackErr.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		settledAmountMicros, callbackErr := monetaryStringToMicros(verifyInfo.Money)
		if callbackErr != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 回调金额无效 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), callbackErr.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		completed, callbackErr := model.CompleteExternalTopUp(model.ExternalTopUpSettlement{
			TradeNo:               verifyInfo.ServiceTradeNo,
			PaymentProvider:       model.PaymentProviderEpay,
			PaymentMethod:         verifyInfo.Type,
			SettlementCurrency:    topUp.SettlementCurrency,
			SettledAmountMicros:   settledAmountMicros,
			ProviderTransactionId: verifyInfo.TradeNo,
		})
		if callbackErr != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 充值结算失败 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), callbackErr.Error()))
			_, _ = c.Writer.Write([]byte("fail"))
			return
		}
		if shouldCredit {
			logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 充值成功 trade_no=%s user_id=%d client_ip=%s quota_to_add=%d money=%.2f", completed.TradeNo, completed.UserId, c.ClientIP(), completed.CreditedQuota, completed.Money))
			model.RecordTopupLog(completed.UserId, fmt.Sprintf("使用在线充值成功，充值金额: %v，支付金额：%f", logger.LogQuota(int(completed.CreditedQuota)), completed.Money), c.ClientIP(), completed.PaymentMethod, "epay")
		} else {
			logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 重复回调幂等忽略 trade_no=%s callback_type=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, c.ClientIP()))
		}
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 忽略事件 trade_no=%s callback_type=%s trade_status=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, c.ClientIP()))
	}
	if _, writeErr := c.Writer.Write([]byte("success")); writeErr != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), writeErr.Error()))
	}
}

// validateEpayCallback makes the signed callback match the order created by
// RequestEpay. A successful order is acknowledged but never credited again.
func validateEpayCallback(topUp *model.TopUp, verifyInfo *epay.VerifyRes) (bool, error) {
	if topUp == nil || verifyInfo == nil || topUp.PaymentProvider != model.PaymentProviderEpay {
		return false, fmt.Errorf("invalid Epay settlement evidence")
	}
	if topUp.PaymentMethod != verifyInfo.Type {
		return false, fmt.Errorf("payment method mismatch")
	}
	if topUp.ExpectedAmountMicros <= 0 || topUp.CreditedQuota <= 0 || !strings.EqualFold(strings.TrimSpace(topUp.SettlementCurrency), "CNY") {
		return false, fmt.Errorf("Epay order has no immutable CNY settlement snapshot")
	}
	callbackMoneyMicros, err := monetaryStringToMicros(verifyInfo.Money)
	if err != nil || callbackMoneyMicros <= 0 {
		return false, fmt.Errorf("invalid callback money")
	}
	if callbackMoneyMicros != topUp.ExpectedAmountMicros {
		return false, fmt.Errorf("payment money mismatch")
	}
	return topUp.Status == common.TopUpStatusPending, nil
}

func RequestAmount(c *gin.Context) {
	var req AmountRequest
	err := c.ShouldBindJSON(&req)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "参数错误"})
		return
	}

	if req.Amount < getMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}
	id := c.GetInt("id")
	if req.PaymentMethod != "" && !requirePaymentMethodAvailable(c, req.PaymentMethod) {
		return
	}
	if req.PaymentMethod != "" && !requirePaymentMethodTopUpWithinLimit(c, req.PaymentMethod, req.Amount) {
		return
	}
	if rejectInvalidTopUpQuota(c, id, req.Amount) {
		return
	}
	group, err := getTopupUserGroup(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	_, creditedQuota := topUpOrderAmounts(req.Amount)
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	if req.PaymentMethod == "" {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "payment_method is required"})
		return
	}
	payMoney, _, err := quoteTopUpWithDiscount(req.Amount, group, req.PaymentMethod, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return
	}
	if payMoney.LessThanOrEqual(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "success", "data": payMoney.StringFixed(2)})
}

func GetUserTopUps(c *gin.Context) {
	userId := c.GetInt("id")
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchUserTopUps(userId, keyword, pageInfo)
	} else {
		topups, total, err = model.GetUserTopUps(userId, pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	records := make([]topUpSelfRecord, 0, len(topups))
	for _, topUp := range topups {
		if topUp == nil {
			continue
		}
		records = append(records, newTopUpSelfRecord(topUp))
	}
	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(records)
	common.ApiSuccess(c, pageInfo)
}

type topUpSelfRecord struct {
	Id            int     `json:"id"`
	UserId        int     `json:"user_id"`
	Amount        int64   `json:"amount"`
	Money         float64 `json:"money"`
	Currency      string  `json:"currency,omitempty"`
	TradeNo       string  `json:"trade_no"`
	PaymentMethod string  `json:"payment_method"`
	CreateTime    int64   `json:"create_time"`
	CompleteTime  int64   `json:"complete_time"`
	Status        string  `json:"status"`
}

func newTopUpSelfRecord(topUp *model.TopUp) topUpSelfRecord {
	return topUpSelfRecord{
		Id:            topUp.Id,
		UserId:        topUp.UserId,
		Amount:        topUp.Amount,
		Money:         topUp.Money,
		Currency:      topUp.SettlementCurrency,
		TradeNo:       topUp.TradeNo,
		PaymentMethod: topUp.PaymentMethod,
		CreateTime:    topUp.CreateTime,
		CompleteTime:  topUp.CompleteTime,
		Status:        topUp.Status,
	}
}

// GetAllTopUps 管理员获取全平台充值记录
func GetAllTopUps(c *gin.Context) {
	pageInfo := common.GetPageQuery(c)
	keyword := c.Query("keyword")

	var (
		topups []*model.TopUp
		total  int64
		err    error
	)
	if keyword != "" {
		topups, total, err = model.SearchAllTopUps(keyword, pageInfo)
	} else {
		topups, total, err = model.GetAllTopUps(pageInfo)
	}
	if err != nil {
		common.ApiError(c, err)
		return
	}

	pageInfo.SetTotal(int(total))
	pageInfo.SetItems(topups)
	common.ApiSuccess(c, pageInfo)
}

type AdminCompleteTopupRequest struct {
	TradeNo string `json:"trade_no"`
}

// AdminCompleteTopUp 管理员补单接口
func AdminCompleteTopUp(c *gin.Context) {
	var req AdminCompleteTopupRequest
	if err := c.ShouldBindJSON(&req); err != nil || req.TradeNo == "" {
		common.ApiErrorMsg(c, "参数错误")
		return
	}

	// 订单级互斥，防止并发补单
	LockOrder(req.TradeNo)
	defer UnlockOrder(req.TradeNo)

	if err := model.ManualCompleteTopUp(req.TradeNo, c.ClientIP()); err != nil {
		common.ApiError(c, err)
		return
	}
	common.ApiSuccess(c, nil)
}
