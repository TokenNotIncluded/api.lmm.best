package service

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Calcium-Ion/go-epay/epay"
	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/pkg/paymentpricing"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/shopspring/decimal"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

const merchantStoreCreditsPerUSD int64 = 500000

var (
	merchantStoreTradeNoPattern = regexp.MustCompile(`^MS[A-Za-z0-9]{30}$`)
	merchantStoreMoneyPattern   = regexp.MustCompile(`^[0-9]{1,16}(?:\.[0-9]{1,2})?$`)
	merchantStoreRatePattern    = regexp.MustCompile(`^[0-9]{1,12}(?:\.[0-9]{1,12})?$`)
	merchantStoreSignature      = regexp.MustCompile(`^[a-f0-9]{32}$`)
)

type MerchantStorePaymentSession struct {
	OrderID     string            `json:"order_id"`
	PaymentURL  string            `json:"payment_url,omitempty"`
	Method      string            `json:"method"`
	Parameters  map[string]string `json:"parameters,omitempty"`
	AmountMinor int64             `json:"amount_minor"`
	Amount      string            `json:"amount"`
	Currency    string            `json:"currency"`
	Status      string            `json:"status"`
}

type merchantStorePaymentContext struct {
	Provider        string                            `json:"provider"`
	Config          merchantStoreGatewayConfig        `json:"config"`
	AmountMinor     int64                             `json:"amount_minor"`
	Currency        string                            `json:"currency"`
	FrozenRate      string                            `json:"frozen_rate"`
	Origin          string                            `json:"origin"`
	PublicOrigin    string                            `json:"public_origin"`
	ExpiresIn       int                               `json:"expires_in"`
	PricingSource   string                            `json:"pricing_source,omitempty"`
	PlatformPricing *paymentpricing.SettlementPricing `json:"platform_pricing,omitempty"`
}

func merchantStorePaymentContextPurpose(orderID string) string {
	return "merchant_store.order.payment." + orderID
}

func merchantStorePaymentScopeHash(paymentContext merchantStorePaymentContext) (string, error) {
	provider, endpoint, account, environment := "", "", "", ""
	switch paymentContext.Provider {
	case MerchantStoreExternalEpay, MerchantStorePlatformLinuxDO:
		provider = "epay"
		if paymentContext.Provider == MerchantStorePlatformLinuxDO {
			provider = "linuxdo"
		}
		u, err := merchantStorePublicHTTPSURL(paymentContext.Config.GatewayURL, false)
		if err != nil {
			return "", err
		}
		u.Host = strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
		// Explicit :443 and a trailing slash describe the same provider account.
		if strings.Contains(u.Host, ":") {
			u.Host = "[" + u.Host + "]"
		}
		u.Path, u.RawPath = strings.TrimRight(u.Path, "/"), strings.TrimRight(u.RawPath, "/")
		endpoint, account = u.String(), paymentContext.Config.PartnerID
	case MerchantStoreExternalPancake, MerchantStorePlatformPancake:
		provider, endpoint = "waffo_pancake", pancake.DefaultBaseURL
		account, environment = paymentContext.Config.MerchantID, paymentContext.Config.Environment
	default:
		return "", ErrMerchantStorePaymentConfiguration
	}
	// Keys can rotate while order receipts remain unique for this account.
	// Product, FX, currency and current merchant balance are not its identity.
	canonical, err := json.Marshal([]string{provider, endpoint, account, environment})
	if err != nil {
		return "", ErrMerchantStorePaymentConfiguration
	}
	hash := sha256.Sum256(canonical)
	return hex.EncodeToString(hash[:]), nil
}

func merchantStoreParsePositiveRate(value string) (decimal.Decimal, error) {
	if !merchantStoreRatePattern.MatchString(value) {
		return decimal.Zero, ErrMerchantStorePaymentConfiguration
	}
	rate, err := decimal.NewFromString(value)
	if err != nil || !rate.IsPositive() {
		return decimal.Zero, ErrMerchantStorePaymentConfiguration
	}
	return rate, nil
}

// Quotes start with integer credits, then convert real money independently.
// The recharge bonus/display legacy scale never enters this calculation.
func merchantStoreQuotePayment(quota int64, currency, rate string) (int64, error) {
	anchor, err := common.CreditsPerUSD()
	if err != nil || !anchor.Equal(decimal.NewFromInt(merchantStoreCreditsPerUSD)) || quota <= 0 {
		return 0, ErrMerchantStorePaymentConfiguration
	}
	ratio, err := merchantStoreParsePositiveRate(rate)
	if err != nil {
		return 0, err
	}
	amount := decimal.NewFromInt(quota).Div(decimal.NewFromInt(merchantStoreCreditsPerUSD))
	switch currency {
	case "USD":
		if !ratio.Equal(decimal.NewFromInt(1)) {
			return 0, ErrMerchantStorePaymentConfiguration
		}
	case "CNY", "LDC":
		amount = amount.Mul(ratio)
	default:
		return 0, ErrMerchantStorePaymentConfiguration
	}
	// Providers cannot collect fractions of their smallest currency unit.
	// Round up the payable money, without changing the frozen credit price.
	minor := amount.Mul(decimal.NewFromInt(100)).Ceil()
	if !minor.IsPositive() || minor.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, ErrMerchantStorePaymentConfiguration
	}
	return minor.IntPart(), nil
}

func merchantStoreMoneyToMinor(value string) (int64, error) {
	if !merchantStoreMoneyPattern.MatchString(value) {
		return 0, ErrMerchantStorePaymentVerification
	}
	amount, err := decimal.NewFromString(value)
	minor := amount.Mul(decimal.NewFromInt(100))
	if err != nil || !minor.IsPositive() || minor.GreaterThan(decimal.NewFromInt(math.MaxInt64)) {
		return 0, ErrMerchantStorePaymentVerification
	}
	return minor.IntPart(), nil
}

func merchantStoreMinorMoney(amount int64) string {
	return decimal.NewFromInt(amount).Div(decimal.NewFromInt(100)).StringFixed(2)
}

func merchantStoreCallbackOrigin() (string, error) {
	value := strings.TrimRight(strings.TrimSpace(GetCallbackAddress()), "/")
	u, err := merchantStorePublicHTTPSURL(value, false)
	if err != nil || (u.Path != "" && u.Path != "/") {
		return "", ErrMerchantStorePaymentConfiguration
	}
	return value, nil
}

func merchantStorePublicOrigin() (string, error) {
	value := strings.TrimRight(strings.TrimSpace(system_setting.ServerAddress), "/")
	u, err := merchantStorePublicHTTPSURL(value, false)
	if err != nil || (u.Path != "" && u.Path != "/") {
		return "", ErrMerchantStorePaymentConfiguration
	}
	return value, nil
}

func merchantStoreOrderPaymentReturnURL(order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext) string {
	query := url.Values{"pay": {"return"}}
	if order.PaymentMethod != MerchantStorePlatformLinuxDO {
		query.Set("order", order.ID)
	}
	// The order history route exists in the public web app. Query parameters
	// identify a row only; the server must still verify actual payment state.
	return paymentContext.PublicOrigin + "/store/orders?" + query.Encode()
}

func merchantStorePlatformGatewayConfig(provider string) (merchantStoreGatewayConfig, error) {
	switch provider {
	case MerchantStoreBalance:
		return merchantStoreGatewayConfig{}, nil
	case MerchantStorePlatformPancake:
		if !operation_setting.IsPaymentComplianceConfirmed() || !merchantStorePlatformMethodEnabled("waffo_pancake") {
			return merchantStoreGatewayConfig{}, ErrMerchantStorePaymentConfiguration
		}
		merchantID, key := WaffoPancakeCredentials()
		config := merchantStoreGatewayConfig{
			MerchantID: merchantID, PrivateKey: key, StoreID: strings.TrimSpace(setting.WaffoPancakeStoreID),
			ProductID: strings.TrimSpace(setting.WaffoPancakeProductID), Currency: "USD", Environment: "prod",
		}
		return config, validateMerchantStoreGatewayConfig(provider, config)
	case MerchantStorePlatformLinuxDO:
		// Existing ePay may point at a fiat gateway. The LDC method is available
		// only on Linux DO's exact endpoint and an explicitly priced LDC method.
		if strings.TrimRight(strings.TrimSpace(operation_setting.PayAddress), "/") != "https://credit.linux.do/epay" {
			return merchantStoreGatewayConfig{}, ErrMerchantStorePaymentConfiguration
		}
		pricing, err := merchantStoreLinuxDOPricing()
		if err != nil {
			return merchantStoreGatewayConfig{}, ErrMerchantStorePaymentConfiguration
		}
		// Compatibility integrity field: keep an original declared platform
		// rate, never a rounded derived USD rate. New amounts use full pricing.
		legacyRate := pricing.SettlementUnitsPerUSD.String()
		if pricing.UsesSettlementUnitsPerPlatformUnit {
			legacyRate = pricing.SettlementUnitsPerPlatformUnit.String()
		}
		base := merchantStoreGatewayConfig{
			GatewayURL: "https://credit.linux.do/epay", PartnerID: strings.TrimSpace(operation_setting.EpayId),
			Key: strings.TrimSpace(operation_setting.EpayKey), PaymentType: "epay", Currency: "LDC",
			UnitsPerUSD: legacyRate,
		}
		if base.UnitsPerUSD != "" {
			return base, validateMerchantStoreGatewayConfig(provider, base)
		}
	}
	return merchantStoreGatewayConfig{}, ErrMerchantStorePaymentConfiguration
}

func merchantStorePlatformMethodEnabled(paymentType string) bool {
	for _, method := range operation_setting.PayMethods {
		if strings.TrimSpace(method["type"]) != paymentType {
			continue
		}
		if value := strings.TrimSpace(method["enabled"]); value != "" {
			enabled, err := strconv.ParseBool(value)
			if err != nil || !enabled {
				return false
			}
		}
	}
	return true
}

func merchantStoreLinuxDOPricing() (paymentpricing.SettlementPricing, error) {
	if !operation_setting.IsPaymentComplianceConfirmed() || !merchantStorePlatformMethodEnabled("epay") {
		return paymentpricing.SettlementPricing{}, ErrMerchantStorePaymentConfiguration
	}
	platformBasis, err := common.LegacyPricingUnitsPerUSD()
	if err != nil {
		return paymentpricing.SettlementPricing{}, ErrMerchantStorePaymentConfiguration
	}
	for _, method := range operation_setting.PayMethods {
		if strings.TrimSpace(method["type"]) != "epay" {
			continue
		}
		pricing, err := paymentpricing.ParseSettlementPricing("epay", method, platformBasis, decimal.Zero)
		if err != nil || pricing.SettlementCurrency != "LDC" {
			return paymentpricing.SettlementPricing{}, ErrMerchantStorePaymentConfiguration
		}
		return pricing, nil
	}
	return paymentpricing.SettlementPricing{}, ErrMerchantStorePaymentConfiguration
}

type merchantStoreProviderTransport struct {
	base    http.RoundTripper
	orderID string
}

func (transport merchantStoreProviderTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	if request == nil || request.URL == nil {
		return nil, ErrMerchantStorePaymentNetwork
	}
	u, err := merchantStorePublicHTTPSURL(request.URL.String(), false)
	if err != nil || u.Host != "api.waffo.ai" || request.Method != http.MethodPost {
		return nil, ErrMerchantStorePaymentNetwork
	}
	if u.Path != "/v1/actions/checkout/create-session" && u.Path != "/v1/actions/auth/issue-session-token" && u.Path != "/v1/graphql" {
		return nil, ErrMerchantStorePaymentNetwork
	}
	clone := request.Clone(request.Context())
	clone.Header = request.Header.Clone()
	// The SDK's checkout idempotency key rotates every minute. A store order
	// instead uses its durable identity, including after an ambiguous timeout.
	if u.Path == "/v1/graphql" {
		// Payment lookup must be fresh, never a cached response to an earlier read.
		clone.Header.Del("X-Idempotency-Key")
	} else {
		idempotency := sha256.Sum256([]byte("merchant-store:" + transport.orderID + ":" + u.Path))
		clone.Header.Set("X-Idempotency-Key", hex.EncodeToString(idempotency[:]))
	}
	clone.Header.Del("Cookie")
	clone.Header.Del("Authorization")
	clone.Header.Del("Proxy-Authorization")
	response, err := transport.base.RoundTrip(clone)
	if err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	if err := common.LimitResponseBody(response, 256<<10); err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	return response, nil
}

func newMerchantStorePaymentHTTPClient(orderID string) *http.Client {
	// Reuse the shared resolve-at-dial-time protection with a mandatory public
	// policy. Neither global insecure TLS/private-IP flags nor environment
	// proxies can relax this merchant-controlled payment boundary.
	protection := &common.SSRFProtection{AllowPrivateIp: false, DomainFilterMode: false, IpFilterMode: false, AllowedPorts: []int{443}, ApplyIPFilterForDomain: true}
	dialer := protectedFetchDialer{
		resolver:      net.DefaultResolver,
		dialContext:   (&net.Dialer{Timeout: 5 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		getProtection: func() (*common.SSRFProtection, bool, error) { return protection, true, nil },
	}
	transport := &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext, ForceAttemptHTTP2: true,
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 15 * time.Second,
		MaxResponseHeaderBytes: 32 << 10, MaxIdleConns: 16, MaxConnsPerHost: 8, IdleConnTimeout: 30 * time.Second,
	}
	return &http.Client{
		Transport: merchantStoreProviderTransport{base: transport, orderID: orderID}, Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return ErrMerchantStorePaymentNetwork },
	}
}

func newMerchantStorePancakeClient(config merchantStoreGatewayConfig, orderID string) (*pancake.Client, error) {
	return pancake.New(pancake.Config{
		MerchantID: config.MerchantID, PrivateKey: config.PrivateKey, Environment: pancake.Environment(config.Environment),
		BaseURL: pancake.DefaultBaseURL, HTTPClient: newMerchantStorePaymentHTTPClient(orderID),
	})
}

func loadMerchantStorePaymentContext(order *model.MerchantStoreOrder) (merchantStorePaymentContext, error) {
	var context merchantStorePaymentContext
	if order == nil || order.GatewaySnapshot == "" {
		return context, ErrMerchantStorePaymentConfiguration
	}
	if err := decryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), order.GatewaySnapshot, &context); err != nil {
		return context, err
	}
	if context.Provider != order.PaymentMethod || context.AmountMinor <= 0 || context.AmountMinor != order.AmountMinor || context.Currency != order.Currency || context.FrozenRate != order.FrozenUSDFX || validateMerchantStoreGatewayConfig(context.Provider, context.Config) != nil {
		return context, ErrMerchantStorePaymentVerification
	}
	if _, err := merchantStorePublicHTTPSURL(context.Origin, false); err != nil {
		return context, ErrMerchantStorePaymentVerification
	}
	if _, err := merchantStorePublicHTTPSURL(context.PublicOrigin, false); err != nil {
		return context, ErrMerchantStorePaymentVerification
	}
	if order.PaymentScopeHash != "" {
		scope, err := merchantStorePaymentScopeHash(context)
		if err != nil || scope != order.PaymentScopeHash {
			return context, ErrMerchantStorePaymentVerification
		}
	}
	return context, nil
}

func prepareMerchantStorePaymentContext(order *model.MerchantStoreOrder, requestedCurrency string) error {
	if order.GatewaySnapshot != "" {
		context, err := loadMerchantStorePaymentContext(order)
		if err != nil || (requestedCurrency != "" && requestedCurrency != context.Currency) {
			return ErrMerchantStorePaymentConfiguration
		}
		return nil
	}
	config, err := merchantStorePlatformGatewayConfig(order.PaymentMethod)
	if strings.HasPrefix(order.PaymentMethod, "external:") {
		config, err = loadMerchantStoreGatewayConfig(order.SellerID, order.PaymentMethod)
	}
	if err != nil || validateMerchantStoreGatewayConfig(order.PaymentMethod, config) != nil {
		return ErrMerchantStorePaymentConfiguration
	}
	currency := config.Currency
	if (order.PaymentMethod == MerchantStoreExternalPancake || order.PaymentMethod == MerchantStorePlatformPancake) && requestedCurrency != "" {
		currency = requestedCurrency
	}
	if requestedCurrency != "" && requestedCurrency != currency {
		return ErrMerchantStorePaymentConfiguration
	}
	rate := "1"
	var platformPricing *paymentpricing.SettlementPricing
	switch currency {
	case "CNY":
		rates, err := paymentpricing.CurrentRates()
		if err != nil {
			return ErrMerchantStorePaymentConfiguration
		}
		rate = rates.CNYPerUSD.String()
	case "LDC":
		pricing, err := merchantStoreLinuxDOPricing()
		if err != nil {
			return err
		}
		platformPricing = &pricing
		rate = pricing.SettlementUnitsPerUSD.String()
		if pricing.UsesSettlementUnitsPerPlatformUnit {
			rate = pricing.SettlementUnitsPerPlatformUnit.String()
		}
		config.UnitsPerUSD = rate // Legacy integrity only, not the amount basis.
	case "USD":
	default:
		return ErrMerchantStorePaymentConfiguration
	}
	minor, err := merchantStoreQuotePayment(int64(order.PriceQuota), currency, rate)
	if platformPricing != nil {
		anchor, anchorErr := common.CreditsPerUSD()
		quotaBasis, basisErr := common.LegacyPricingQuotaPerUnit()
		if anchorErr != nil || basisErr != nil || !anchor.Equal(decimal.NewFromInt(merchantStoreCreditsPerUSD)) || !quotaBasis.Equal(anchor) {
			return ErrMerchantStorePaymentConfiguration
		}
		amount, quoteErr := paymentpricing.CeilSettlementMinorForCredits(int64(order.PriceQuota), *platformPricing, anchor, quotaBasis, 100)
		if quoteErr != nil || amount <= 0 {
			return ErrMerchantStorePaymentConfiguration
		}
		minor, err = amount, nil
	}
	if err != nil {
		return err
	}
	origin, err := merchantStoreCallbackOrigin()
	if err != nil {
		return err
	}
	publicOrigin, err := merchantStorePublicOrigin()
	if err != nil {
		return err
	}
	expiresIn := order.ExpiresAt - time.Now().Unix()
	if expiresIn <= 0 || expiresIn > WaffoPancakeCheckoutExpirySeconds {
		return ErrMerchantStorePaymentAccess
	}
	context := merchantStorePaymentContext{Provider: order.PaymentMethod, Config: config, AmountMinor: minor, Currency: currency, FrozenRate: rate, Origin: origin, PublicOrigin: publicOrigin, ExpiresIn: int(expiresIn)}
	if platformPricing != nil {
		context.PricingSource = "platform_pay_methods"
		context.PlatformPricing = platformPricing
	}
	ciphertext, err := encryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), context)
	if err != nil {
		return err
	}
	if err := model.BindMerchantStorePaymentQuote(order.ID, minor, currency, rate); err != nil {
		return err
	}
	scope, err := merchantStorePaymentScopeHash(context)
	if err != nil {
		return err
	}
	return model.BindMerchantStorePaymentContext(order.ID, ciphertext, scope)
}

// CreateMerchantStorePaymentSession receives only a requested supported
// currency. Price, owner, fees and converted amount always come from the order.
func CreateMerchantStorePaymentSession(ctx context.Context, buyerID int, orderID, requestedCurrency string) (*MerchantStorePaymentSession, error) {
	order, err := model.GetMerchantStorePaymentOrder(orderID)
	if err != nil || order == nil || order.BuyerID != buyerID || buyerID <= 0 || !merchantStoreTradeNoPattern.MatchString(order.TradeNo) {
		return nil, ErrMerchantStorePaymentAccess
	}
	if order.PaymentMethod == MerchantStoreBalance {
		return &MerchantStorePaymentSession{OrderID: order.ID, Method: "balance", Currency: "CREDIT", Status: order.Status}, nil
	}
	if order.Status != "pending" {
		return nil, ErrMerchantStorePaymentAccess
	}
	if order.ExpiresAt <= time.Now().Unix() {
		return nil, ErrMerchantStorePaymentAccess
	}
	requestedCurrency = strings.ToUpper(strings.TrimSpace(requestedCurrency))
	if err := prepareMerchantStorePaymentContext(order, requestedCurrency); err != nil {
		return nil, err
	}
	order, err = model.GetMerchantStorePaymentOrder(orderID)
	if err != nil {
		return nil, err
	}
	paymentContext, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return nil, err
	}
	result := &MerchantStorePaymentSession{OrderID: order.ID, AmountMinor: order.AmountMinor, Amount: merchantStoreMinorMoney(order.AmountMinor), Currency: order.Currency, Status: order.Status}
	if order.CheckoutURL != "" {
		result.Method, result.PaymentURL = http.MethodGet, order.CheckoutURL
		return result, nil
	}
	switch order.PaymentMethod {
	case MerchantStoreExternalEpay, MerchantStorePlatformLinuxDO:
		return createMerchantStoreEpaySession(order, paymentContext, result)
	case MerchantStoreExternalPancake, MerchantStorePlatformPancake:
		return createMerchantStorePancakeSession(ctx, order, paymentContext, result)
	default:
		return nil, ErrMerchantStorePaymentConfiguration
	}
}

func createMerchantStoreEpaySession(order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, result *MerchantStorePaymentSession) (*MerchantStorePaymentSession, error) {
	config := paymentContext.Config
	client, err := epay.NewClient(&epay.Config{PartnerID: config.PartnerID, Key: config.Key}, config.GatewayURL)
	if err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	callbackReference := order.ID
	if order.PaymentMethod == MerchantStorePlatformLinuxDO {
		callbackReference = order.TradeNo
	}
	notifyURL, _ := url.Parse(paymentContext.Origin + "/api/store/payments/epay/" + callbackReference + "/notify")
	returnURL, _ := url.Parse(merchantStoreOrderPaymentReturnURL(order, paymentContext))
	endpoint, parameters, err := client.Purchase(&epay.PurchaseArgs{
		Type: config.PaymentType, ServiceTradeNo: order.TradeNo, Name: "Store order " + order.TradeNo,
		Money: result.Amount, Device: epay.PC, NotifyUrl: notifyURL, ReturnUrl: returnURL,
	})
	if err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	if order.PaymentMethod == MerchantStorePlatformLinuxDO {
		// LDC's documented protocol requires a browser form POST to /pay/submit.php.
		endpoint = "https://credit.linux.do/epay/pay/submit.php"
		if len(notifyURL.String()) > 100 || len(returnURL.String()) > 100 {
			return nil, ErrMerchantStorePaymentConfiguration
		}
		result.Method, result.PaymentURL, result.Parameters = http.MethodPost, endpoint, parameters
		return result, nil
	}
	u, err := merchantStorePublicHTTPSURL(endpoint, false)
	if err != nil {
		return nil, err
	}
	query := url.Values{}
	for key, value := range parameters {
		query.Set(key, value)
	}
	u.RawQuery = query.Encode()
	if err := model.BindMerchantStoreCheckoutSession(order.ID, u.String(), order.TradeNo); err != nil {
		return nil, err
	}
	result.Method, result.PaymentURL = http.MethodGet, u.String()
	return result, nil
}

func createMerchantStorePancakeSession(ctx context.Context, order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, result *MerchantStorePaymentSession) (*MerchantStorePaymentSession, error) {
	client, err := newMerchantStorePancakeClient(paymentContext.Config, order.ID)
	if err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	return createMerchantStorePancakeSessionWithClient(ctx, order, paymentContext, result, client)
}

func createMerchantStorePancakeSessionWithClient(ctx context.Context, order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, result *MerchantStorePaymentSession, client *pancake.Client) (*MerchantStorePaymentSession, error) {
	if client == nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	expiresIn := paymentContext.ExpiresIn
	if expiresIn <= 0 || expiresIn > WaffoPancakeCheckoutExpirySeconds {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	returnURL := merchantStoreOrderPaymentReturnURL(order, paymentContext)
	params := pancake.AuthenticatedCheckoutParams{
		CreateCheckoutSessionParams: pancake.CreateCheckoutSessionParams{
			ProductID: paymentContext.Config.ProductID, Currency: order.Currency,
			PriceSnapshot:    &pancake.PriceSnapshot{Amount: result.Amount, TaxCategory: pancake.TaxCategoryDigitalGoods},
			ExpiresInSeconds: &expiresIn, SuccessURL: &returnURL, OrderMerchantExternalID: &order.TradeNo,
			Metadata: map[string]string{"lmm_store_order_id": order.ID, "lmm_store_product_id": order.ProductID, "lmm_pancake_product_id": paymentContext.Config.ProductID, "lmm_store_seller_id": strconv.Itoa(order.SellerID)},
		},
		BuyerIdentity: WaffoPancakeBuyerIdentityFromUserID(order.BuyerID),
	}
	// BuyerEmail is deliberately omitted: a merchant does not receive the
	// platform account email merely because the buyer purchases a product.
	session, err := client.Checkout.Authenticated.Create(ctx, params)
	if err != nil || session == nil || strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.Token) == "" {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	u, err := url.Parse(session.CheckoutURL)
	if err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	fragment := u.Fragment
	u.Fragment = ""
	if _, err := merchantStorePublicHTTPSURL(u.String(), true); err != nil {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	u.Fragment = fragment
	providerExpires, err := time.Parse(time.RFC3339, session.ExpiresAt)
	if err != nil || providerExpires.Unix() <= time.Now().Unix() {
		return nil, ErrMerchantStorePaymentVerification
	}
	if err := model.BindMerchantStoreCheckoutSession(order.ID, u.String(), session.SessionID, providerExpires.Unix()); err != nil {
		return nil, err
	}
	result.Method, result.PaymentURL = http.MethodGet, u.String()
	return result, nil
}

func merchantStoreEpayCallbackFields(values url.Values) (map[string]string, error) {
	if len(values) == 0 || len(values) > 32 {
		return nil, ErrMerchantStorePaymentVerification
	}
	fields := map[string]string{}
	for key, values := range values {
		if len(values) != 1 || len(key) > 64 || !merchantStoreProviderIDPattern.MatchString(key) || len(values[0]) > 4096 || strings.ContainsAny(values[0], "\r\n\x00") {
			return nil, ErrMerchantStorePaymentVerification
		}
		fields[key] = values[0]
	}
	return fields, nil
}

func validateMerchantStoreEpayCallback(order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, values url.Values) (string, error) {
	fields, err := merchantStoreEpayCallbackFields(values)
	if err != nil {
		return "", err
	}
	sign := fields["sign"]
	if !merchantStoreSignature.MatchString(sign) || (fields["sign_type"] != "" && fields["sign_type"] != "MD5") {
		return "", ErrMerchantStorePaymentVerification
	}
	expected := epay.GenerateParams(fields, paymentContext.Config.Key)["sign"]
	if subtle.ConstantTimeCompare([]byte(sign), []byte(expected)) != 1 {
		return "", ErrMerchantStorePaymentVerification
	}
	if fields["trade_status"] != epay.StatusTradeSuccess {
		return "", ErrMerchantStorePaymentIgnored
	}
	minor, err := merchantStoreMoneyToMinor(fields["money"])
	if err != nil || minor != order.AmountMinor || fields["out_trade_no"] != order.TradeNo || fields["pid"] != paymentContext.Config.PartnerID || fields["type"] != paymentContext.Config.PaymentType || !merchantStoreProviderIDPattern.MatchString(fields["trade_no"]) {
		return "", ErrMerchantStorePaymentVerification
	}
	// ePay's protocol carries no fiat unit. It is fixed by this encrypted
	// CNY/LDC gateway snapshot, never guessed from a display preference.
	if currency := fields["currency"]; currency != "" && currency != order.Currency {
		return "", ErrMerchantStorePaymentVerification
	}
	return fields["trade_no"], nil
}

// HandleMerchantStoreEpayCallback is only for the dedicated signed notify URL.
// A browser return URL never calls this settlement path.
func HandleMerchantStoreEpayCallback(_ context.Context, orderID string, parameters url.Values) error {
	order, err := model.GetMerchantStorePaymentOrder(orderID)
	if merchantStoreTradeNoPattern.MatchString(orderID) {
		order, err = model.GetMerchantStorePaymentOrderByTradeNo(orderID)
	}
	if err != nil || order == nil || !merchantStoreTradeNoPattern.MatchString(order.TradeNo) || (order.PaymentMethod != MerchantStoreExternalEpay && order.PaymentMethod != MerchantStorePlatformLinuxDO) {
		return ErrMerchantStorePaymentVerification
	}
	paymentContext, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return err
	}
	tradeID, err := validateMerchantStoreEpayCallback(order, paymentContext, parameters)
	if err != nil {
		return err
	}
	return completeMerchantStoreVerifiedPayment(order.ID, tradeID)
}

// Only verified provider adapters may enter this helper. Preserving paid
// evidence is independent of settlement, and must never move stock or money.
func completeMerchantStoreVerifiedPayment(orderID, receipt string) error {
	err := model.CompleteMerchantStorePayment(orderID, receipt)
	if err == nil {
		return nil
	}
	code := ""
	switch {
	case errors.Is(err, model.ErrMerchantStoreDenied), errors.Is(err, model.ErrMerchantStoreBalance):
		code = "settlement_unavailable"
	case errors.Is(err, model.ErrWalletQuotaOutOfRange):
		code = "wallet_bounds"
	case errors.Is(err, model.ErrMerchantStoreStock):
		code = "stock_unavailable"
	case errors.Is(err, model.ErrMerchantStoreConflict):
		code = "settlement_conflict"
	}
	if code != "" {
		if recordErr := model.RecordMerchantStoreVerifiedPaymentIssue(orderID, receipt, code); recordErr != nil {
			// No SQL/upstream error, receipt, email or secret enters this log.
			common.SysError("merchant store verified payment recovery record failed")
		}
	}
	// A known paid receipt is not a successful settlement. Return a failure so
	// the provider retries while the order stays recoverable for reconciliation.
	return err
}

func validateMerchantStorePancakeCallback(order *model.MerchantStoreOrder, paymentContext merchantStorePaymentContext, event *WaffoPancakeWebhookEvent) (string, error) {
	if event == nil || event.EventType != "order.completed" {
		return "", ErrMerchantStorePaymentIgnored
	}
	if err := ValidateWaffoPancakeWebhookEvent(event); err != nil {
		return "", ErrMerchantStorePaymentVerification
	}
	if event.Mode != paymentContext.Config.Environment || event.StoreID != paymentContext.Config.StoreID || event.Data.Currency != order.Currency || event.Data.OrderMerchantExternalID != order.TradeNo || event.Data.MerchantProvidedBuyerIdentity != WaffoPancakeBuyerIdentityFromUserID(order.BuyerID) || event.Data.OrderMetadata["lmm_store_order_id"] != order.ID || event.Data.OrderMetadata["lmm_store_product_id"] != order.ProductID || event.Data.OrderMetadata["lmm_pancake_product_id"] != paymentContext.Config.ProductID || event.Data.OrderMetadata["lmm_store_seller_id"] != strconv.Itoa(order.SellerID) {
		return "", ErrMerchantStorePaymentVerification
	}
	minor, err := merchantStoreMoneyToMinor(event.Data.Amount)
	// Waffo's amount is the actual charge, including any collected tax.
	// Compare the signed pre-tax listing price with our frozen quote.
	if err == nil && event.Data.TaxAmount != "" {
		if !merchantStoreMoneyPattern.MatchString(event.Data.TaxAmount) {
			return "", ErrMerchantStorePaymentVerification
		}
		tax, taxErr := decimal.NewFromString(event.Data.TaxAmount)
		taxMinor := tax.Mul(decimal.NewFromInt(100))
		if taxErr != nil || taxMinor.IsNegative() || taxMinor.GreaterThan(decimal.NewFromInt(minor)) {
			return "", ErrMerchantStorePaymentVerification
		}
		minor -= taxMinor.IntPart()
	}
	if err != nil || minor != order.AmountMinor || !merchantStoreProviderIDPattern.MatchString(event.Data.OrderID) {
		return "", ErrMerchantStorePaymentVerification
	}
	return event.Data.OrderID, nil
}

// Waffo verifies with the route's environment key before any order lookup.
// The order's frozen merchant/store/product/identity and amount are then checked.
func HandleMerchantStorePancakeWebhook(_ context.Context, scope string, sellerID int, environment string, payload []byte, signature string) error {
	if len(payload) == 0 || len(payload) > 256<<10 || (scope != "platform" && scope != "external") || (environment != "prod" && environment != "test") || (scope == "platform" && sellerID != 0) || (scope == "external" && sellerID <= 0) {
		return ErrMerchantStorePaymentVerification
	}
	event, err := VerifyConfiguredWaffoPancakeWebhook(string(payload), signature, environment)
	if err != nil {
		return ErrMerchantStorePaymentVerification
	}
	if event.EventType != "order.completed" || !merchantStoreTradeNoPattern.MatchString(event.Data.OrderMerchantExternalID) {
		return ErrMerchantStorePaymentIgnored
	}
	order, err := model.GetMerchantStorePaymentOrderByTradeNo(event.Data.OrderMerchantExternalID)
	if err != nil || order == nil {
		return ErrMerchantStorePaymentIgnored
	}
	if (scope == "platform" && order.PaymentMethod != MerchantStorePlatformPancake) || (scope == "external" && (order.PaymentMethod != MerchantStoreExternalPancake || order.SellerID != sellerID)) {
		return ErrMerchantStorePaymentVerification
	}
	paymentContext, err := loadMerchantStorePaymentContext(order)
	if err != nil {
		return err
	}
	tradeID, err := validateMerchantStorePancakeCallback(order, paymentContext, event)
	if err != nil {
		return err
	}
	return completeMerchantStoreVerifiedPayment(order.ID, tradeID)
}
