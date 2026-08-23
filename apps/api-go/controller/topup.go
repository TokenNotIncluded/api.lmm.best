package controller

import (
	"encoding/json"
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

func topUpOrderAmounts(requestedAmount int64) (storedAmount int64, creditedQuota int64) {
	if requestedAmount <= 0 {
		return 0, 0
	}
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return 0, 0
		}
		var ok bool
		storedAmount, ok = decimalInt64Truncated(
			decimal.NewFromInt(requestedAmount).Div(decimal.NewFromFloat(common.QuotaPerUnit)),
		)
		if !ok {
			return 0, 0
		}
		return storedAmount, requestedAmount
	}
	return requestedAmount, model.StandardTopUpCreditedQuota(requestedAmount)
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

	payMethods := append([]map[string]string(nil), operation_setting.PayMethods...)
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
	return payMethods
}

func sanitizedPaymentMethods(methods []map[string]string) []map[string]string {
	allowed := map[string]struct{}{
		"name": {}, "type": {}, "icon": {}, "color": {}, "min_topup": {},
		"max_topup": {}, "description": {}, "settlement_unit": {}, "unit_price": {}, "topup_ratio": {},
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

// getPayMethodUnitPrice returns the settlement price configured for a payment
// method. The optional unit_price is deliberately strict: an invalid configured
// value must not silently fall back to the global Price, otherwise a typo could
// create underpriced orders.
func getPayMethodUnitPrice(paymentMethod string) (decimal.Decimal, error) {
	payMethod, err := getPayMethod(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	unitPrice, hasUnitPrice := payMethod["unit_price"]
	settlementUnit, hasSettlementUnit := payMethod["settlement_unit"]
	if hasUnitPrice != hasSettlementUnit {
		return decimal.Zero, fmt.Errorf("payment method %q must configure settlement_unit and unit_price together", paymentMethod)
	}
	if !hasUnitPrice {
		return decimal.NewFromFloat(operation_setting.Price), nil
	}
	if !settlementUnitPattern.MatchString(settlementUnit) {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid settlement_unit", paymentMethod)
	}
	if !positiveDecimalPattern.MatchString(unitPrice) {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid unit_price", paymentMethod)
	}
	price, err := decimal.NewFromString(unitPrice)
	if err != nil || !price.IsPositive() {
		return decimal.Zero, fmt.Errorf("payment method %q has invalid unit_price", paymentMethod)
	}
	return price, nil
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
// one amount and create an order at another amount. Calculation order is:
// platform amount × payment-method unit_price (or global Price) × group ratio
// × payment-method topup_ratio × amount discount. settlement_unit is
// presentation metadata only.
func quoteTopUp(amount int64, group, paymentMethod string) (decimal.Decimal, error) {
	dPrice, err := getPayMethodUnitPrice(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	dTopupRatio, err := getPayMethodTopupRatio(paymentMethod)
	if err != nil {
		return decimal.Zero, err
	}
	return quoteTopUpWithPricing(amount, group, dPrice, dTopupRatio), nil
}

// getPayMoney keeps legacy FAST callers on the global price while sharing the
// same calculation order as the ePay method-aware quote.
func getPayMoney(amount int64, group string) float64 {
	return quoteTopUpWithPricing(amount, group, decimal.NewFromFloat(operation_setting.Price), decimal.NewFromInt(1)).InexactFloat64()
}

func quoteTopUpWithPricing(amount int64, group string, dPrice, dPaymentRatio decimal.Decimal) decimal.Decimal {
	dAmount := decimal.NewFromInt(amount)
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
	if ds, ok := operation_setting.GetPaymentSetting().AmountDiscount[int(amount)]; ok {
		if ds > 0 {
			discount = ds
		}
	}
	dDiscount := decimal.NewFromFloat(discount)

	return dAmount.Mul(dPrice).Mul(dTopupGroupRatio).Mul(dPaymentRatio).Mul(dDiscount).Round(2)
}

func getMinTopup() int64 {
	minTopup := operation_setting.MinTopUp
	if operation_setting.GetQuotaDisplayType() == operation_setting.QuotaDisplayTypeTokens {
		if !validQuotaPerUnit() {
			return int64(common.MaxQuota)
		}
		dMinTopup := decimal.NewFromInt(int64(minTopup))
		dQuotaPerUnit := decimal.NewFromFloat(common.QuotaPerUnit)
		converted, ok := decimalInt64Truncated(dMinTopup.Mul(dQuotaPerUnit))
		if !ok || converted < 0 || converted > int64(common.MaxQuota) {
			return int64(common.MaxQuota)
		}
		minTopup = int(converted)
	}
	return int64(minTopup)
}

func RequestEpay(c *gin.Context) {
	var req EpayRequest
	if err := parsePayRequest(c, &req.Amount, &req.PaymentMethod, &req.DiscountCode); err != nil {
		logger.LogWarn(c.Request.Context(), fmt.Sprintf("Epay 参数解包失败 error=%q", err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("参数错误: %s", err.Error())})
		return
	}
	int64Amount := int64(req.Amount)
	if int64Amount < getMinTopup() {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": fmt.Sprintf("充值数量不能小于 %d", getMinTopup())})
		return
	}

	if !requirePaymentMethodAvailable(c, req.PaymentMethod) {
		return
	}
	if !requirePaymentMethodTopUpWithinLimit(c, req.PaymentMethod, int64Amount) {
		return
	}

	id := c.GetInt("id")
	group, err := getTopupUserGroup(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	payMoney, discountCode, err := quoteTopUpWithDiscount(int64Amount, group, req.PaymentMethod, req.DiscountCode, id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "支付方式配置无效"})
		return
	}
	if payMoney.LessThan(decimal.NewFromFloat(0.01)) {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "充值金额过低"})
		return
	}
	amount, creditedQuota := topUpOrderAmounts(int64Amount)
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
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
	uri, params, err := client.Purchase(&epay.PurchaseArgs{
		Type:           req.PaymentMethod,
		ServiceTradeNo: tradeNo,
		Name:           fmt.Sprintf("TUC%d", int64Amount),
		Money:          payMoney.StringFixed(2),
		Device:         epay.PC,
		NotifyUrl:      notifyUrl,
		ReturnUrl:      returnUrl,
	})
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 拉起支付失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, int64Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "拉起支付失败"})
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
		CreditedQuota:        creditedQuota,
		ExpectedAmountMicros: expectedAmountMicros,
		Money:                monetaryMicrosToFloat(expectedAmountMicros),
		TradeNo:              tradeNo,
		PaymentMethod:        req.PaymentMethod,
		PaymentProvider:      model.PaymentProviderEpay,
		DiscountCodeId:       discountCodeID(discountCode),
		DiscountPercent:      discountPercent(discountCode),
		CreateTime:           time.Now().Unix(),
		Status:               common.TopUpStatusPending,
	}
	err = topUp.Insert()
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 创建充值订单失败 user_id=%d trade_no=%s payment_method=%s amount=%d error=%q", id, tradeNo, req.PaymentMethod, int64Amount, err.Error()))
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "创建订单失败"})
		return
	}
	logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 充值订单创建成功 user_id=%d trade_no=%s payment_method=%s amount=%d money=%s", id, tradeNo, req.PaymentMethod, int64Amount, payMoney.StringFixed(2)))
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
	if err == nil && verifyInfo.VerifyStatus {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签成功 trade_no=%s callback_type=%s trade_status=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, c.ClientIP()))
	} else {
		_, err := c.Writer.Write([]byte("fail"))
		if err != nil {
			logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 client_ip=%s error=%q", c.ClientIP(), err.Error()))
		}
		if err != nil {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 client_ip=%s verify_error=%q", c.ClientIP(), err.Error()))
		} else {
			logger.LogWarn(c.Request.Context(), fmt.Sprintf("易支付 webhook 验签失败 client_ip=%s verify_status=false", c.ClientIP()))
		}
		return
	}

	if verifyInfo.TradeStatus == epay.StatusTradeSuccess {
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
		}
	} else {
		logger.LogInfo(c.Request.Context(), fmt.Sprintf("易支付 webhook 忽略事件 trade_no=%s callback_type=%s trade_status=%s client_ip=%s", verifyInfo.ServiceTradeNo, verifyInfo.Type, verifyInfo.TradeStatus, c.ClientIP()))
	}
	_, err = c.Writer.Write([]byte("success"))
	if err != nil {
		logger.LogError(c.Request.Context(), fmt.Sprintf("易支付 webhook 响应写入失败 trade_no=%s client_ip=%s error=%q", verifyInfo.ServiceTradeNo, c.ClientIP(), err.Error()))
	}
}

// validateEpayCallback makes the signed callback match the order created by
// RequestEpay. A successful order is acknowledged but never credited again.
func validateEpayCallback(topUp *model.TopUp, verifyInfo *epay.VerifyRes) (bool, error) {
	if topUp.PaymentMethod != verifyInfo.Type {
		return false, fmt.Errorf("payment method mismatch")
	}
	callbackMoney, err := decimal.NewFromString(verifyInfo.Money)
	if err != nil || !callbackMoney.IsPositive() {
		return false, fmt.Errorf("invalid callback money")
	}
	orderMoney := decimal.NewFromFloat(topUp.Money)
	if !callbackMoney.Round(2).Equal(orderMoney.Round(2)) {
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
	group, err := getTopupUserGroup(id)
	if err != nil {
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": "获取用户分组失败"})
		return
	}
	_, creditedQuota := topUpOrderAmounts(req.Amount)
	if !requireTopUpCreditCapacity(c, id, creditedQuota) {
		return
	}
	var payMoney decimal.Decimal
	if req.PaymentMethod == "" {
		// Older clients did not send payment_method to the quote endpoint.
		// Keep their global Price behavior while new clients use the selected
		// payment method's server-authoritative settlement price.
		payMoney, _, err = quoteLegacyTopUpWithDiscount(req.Amount, group, req.DiscountCode, id)
	} else {
		payMoney, _, err = quoteTopUpWithDiscount(req.Amount, group, req.PaymentMethod, req.DiscountCode, id)
	}
	if err != nil {
		message := "优惠码无效"
		if req.PaymentMethod != "" {
			message = "支付方式配置无效"
		}
		c.JSON(http.StatusOK, gin.H{"message": "error", "data": message})
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
