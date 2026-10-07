package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/model"
)

const (
	MerchantStoreExternalEpay    = "external:epay"
	MerchantStoreExternalPancake = "external:waffo_pancake"
	MerchantStorePlatformPancake = "platform:waffo_pancake"
	MerchantStorePlatformLinuxDO = "platform:linuxdo"
	MerchantStoreBalance         = "balance"
)

var (
	ErrMerchantStorePaymentConfiguration = errors.New("merchant store payment method is unavailable or misconfigured")
	ErrMerchantStorePaymentVerification  = errors.New("merchant store payment verification failed")
	ErrMerchantStorePaymentIgnored       = errors.New("merchant store payment event does not apply")
	ErrMerchantStorePaymentAccess        = errors.New("merchant store order is unavailable")
	ErrMerchantStorePaymentNetwork       = errors.New("merchant store payment gateway must use a public HTTPS destination")
)

// Credentials are write-only. A blank credential keeps the existing value;
// disabling a method does not erase credentials needed by pending orders.
type MerchantStoreGatewayConfigInput struct {
	Provider    string `json:"provider"`
	Enabled     bool   `json:"enabled"`
	GatewayURL  string `json:"gateway_url,omitempty"`
	PartnerID   string `json:"partner_id,omitempty"`
	Key         string `json:"key,omitempty"`
	PaymentType string `json:"payment_type,omitempty"`
	Currency    string `json:"currency,omitempty"`
	MerchantID  string `json:"merchant_id,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
	StoreID     string `json:"store_id,omitempty"`
	ProductID   string `json:"product_id,omitempty"`
	Environment string `json:"environment,omitempty"`
}

// Private gateway configuration is encrypted with an owner/provider-specific
// purpose. It is never embedded in any API response or public product DTO.
type merchantStoreGatewayConfig struct {
	GatewayURL  string `json:"gateway_url,omitempty"`
	PartnerID   string `json:"partner_id,omitempty"`
	Key         string `json:"key,omitempty"`
	PaymentType string `json:"payment_type,omitempty"`
	Currency    string `json:"currency,omitempty"`
	MerchantID  string `json:"merchant_id,omitempty"`
	PrivateKey  string `json:"private_key,omitempty"`
	StoreID     string `json:"store_id,omitempty"`
	ProductID   string `json:"product_id,omitempty"`
	Environment string `json:"environment,omitempty"`
	// Old reader integrity only. New LDC invoices use complete PlatformPricing.
	UnitsPerUSD string `json:"units_per_usd,omitempty"`
}

type MerchantStoreGatewayView struct {
	Provider         string            `json:"provider"`
	Enabled          bool              `json:"enabled"`
	Category         string            `json:"category"`
	CategoryEnabled  bool              `json:"category_enabled"`
	EffectiveEnabled bool              `json:"effective_enabled"`
	Configured       bool              `json:"configured"`
	GatewayURL       string            `json:"gateway_url,omitempty"`
	PartnerID        string            `json:"partner_id,omitempty"`
	PaymentType      string            `json:"payment_type,omitempty"`
	Currency         string            `json:"currency,omitempty"`
	MerchantID       string            `json:"merchant_id,omitempty"`
	StoreID          string            `json:"store_id,omitempty"`
	ProductID        string            `json:"product_id,omitempty"`
	Environment      string            `json:"environment,omitempty"`
	HasKey           bool              `json:"has_key"`
	HasPrivateKey    bool              `json:"has_private_key"`
	CallbackURLs     map[string]string `json:"callback_urls,omitempty"`
	UnavailableCode  string            `json:"unavailable_code,omitempty"`
}

var merchantStoreProviderIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
var merchantStorePancakeShortIDBody = regexp.MustCompile(`^[A-Za-z0-9]{22}$`)

func merchantStorePancakeShortID(value, prefix string) bool {
	return strings.HasPrefix(value, prefix+"_") && merchantStorePancakeShortIDBody.MatchString(strings.TrimPrefix(value, prefix+"_"))
}

func encryptMerchantStorePaymentValue(purpose string, value any) (string, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", ErrMerchantStorePaymentConfiguration
	}
	ciphertext, err := common.EncryptPersistentString(purpose, "MERCHANT_STORE_ENCRYPTION_KEY", "CRYPTO_SECRET", string(encoded))
	if err != nil {
		return "", ErrMerchantStorePaymentConfiguration
	}
	return ciphertext, nil
}

func decryptMerchantStorePaymentValue(purpose, ciphertext string, destination any) error {
	plaintext, err := common.DecryptPersistentString(purpose, "MERCHANT_STORE_ENCRYPTION_KEY", "CRYPTO_SECRET", ciphertext)
	if err != nil || plaintext == "" || json.Unmarshal([]byte(plaintext), destination) != nil {
		return ErrMerchantStorePaymentConfiguration
	}
	return nil
}

func merchantStorePaymentProviderSupported(provider string) bool {
	_, known := model.MerchantStorePaymentCategory(provider)
	return known
}

func merchantStorePublicHTTPSURL(raw string, allowQuery bool) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 2048 || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || (!allowQuery && (u.RawQuery != "" || u.ForceQuery)) || u.Fragment != "" || (u.Port() != "" && u.Port() != "443") {
		return nil, ErrMerchantStorePaymentNetwork
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "localhost" || strings.HasSuffix(host, ".localhost") || strings.HasSuffix(host, ".local") || strings.Contains(host, "%") {
		return nil, ErrMerchantStorePaymentNetwork
	}
	if ip, err := netip.ParseAddr(host); err == nil && !marketPublicIP(ip) {
		return nil, ErrMerchantStorePaymentNetwork
	}
	return u, nil
}

func merchantStorePublicGatewayView(sellerID int, provider string, enabled bool, config merchantStoreGatewayConfig) MerchantStoreGatewayView {
	view := MerchantStoreGatewayView{
		Provider: provider, Enabled: enabled, GatewayURL: config.GatewayURL, PartnerID: config.PartnerID,
		PaymentType: config.PaymentType, Currency: config.Currency, MerchantID: config.MerchantID,
		StoreID: config.StoreID, ProductID: config.ProductID, Environment: config.Environment,
		HasKey: config.Key != "", HasPrivateKey: config.PrivateKey != "",
	}
	view.Configured = validateMerchantStoreGatewayConfig(provider, config) == nil
	if !view.Configured {
		view.UnavailableCode = "payment_configuration_required"
	}
	if origin, err := merchantStoreCallbackOrigin(); err == nil {
		switch provider {
		case MerchantStoreExternalEpay, MerchantStorePlatformLinuxDO:
			view.CallbackURLs = map[string]string{"notify": origin + "/api/store/payments/epay/{order_id}/notify"}
		case MerchantStoreExternalPancake, MerchantStorePlatformPancake:
			scope, id := "external", sellerID
			if provider == MerchantStorePlatformPancake {
				scope, id = "platform", 0
			}
			view.CallbackURLs = map[string]string{}
			for _, env := range []string{"prod", "test"} {
				view.CallbackURLs[env] = fmt.Sprintf("%s/api/store/payments/pancake/%s/%d/%s/webhook", origin, scope, id, env)
			}
		}
	}
	return view
}

func validateMerchantStoreGatewayConfig(provider string, config merchantStoreGatewayConfig) error {
	switch provider {
	case MerchantStoreExternalEpay:
		gatewayURL, err := merchantStorePublicHTTPSURL(config.GatewayURL, false)
		if err != nil {
			return err
		}
		if strings.EqualFold(strings.TrimSuffix(gatewayURL.Hostname(), "."), "credit.linux.do") {
			// Linux DO's ePay protocol charges LDC, never CNY. It is supported
			// only through the dedicated explicitly priced LDC platform method.
			return ErrMerchantStorePaymentConfiguration
		}
		if !merchantStoreProviderIDPattern.MatchString(config.PartnerID) || len(config.Key) < 8 || len(config.Key) > 512 || !merchantStoreProviderIDPattern.MatchString(config.PaymentType) || config.Currency != "CNY" {
			return ErrMerchantStorePaymentConfiguration
		}
	case MerchantStoreExternalPancake, MerchantStorePlatformPancake:
		if !merchantStorePancakeShortID(config.MerchantID, "MER") || len(config.PrivateKey) < 128 || len(config.PrivateKey) > 16<<10 || !merchantStorePancakeShortID(config.StoreID, "STO") || !merchantStorePancakeShortID(config.ProductID, "PROD") || (config.Environment != "prod" && config.Environment != "test") || (config.Currency != "USD" && config.Currency != "CNY") {
			return ErrMerchantStorePaymentConfiguration
		}
	case MerchantStorePlatformLinuxDO:
		if config.GatewayURL != "https://credit.linux.do/epay" || config.Currency != "LDC" || config.PaymentType != "epay" || !merchantStoreProviderIDPattern.MatchString(config.PartnerID) || len(config.Key) < 8 || len(config.Key) > 512 {
			return ErrMerchantStorePaymentConfiguration
		}
		if _, err := merchantStoreParsePositiveRate(config.UnitsPerUSD); err != nil {
			return err
		}
	case MerchantStoreBalance:
		return nil
	default:
		return ErrMerchantStorePaymentConfiguration
	}
	return nil
}

func loadMerchantStoreGatewayConfig(sellerID int, provider string) (merchantStoreGatewayConfig, error) {
	var config merchantStoreGatewayConfig
	plaintext, err := model.GetMerchantStoreGatewaySecret(sellerID, provider)
	if err != nil {
		return config, err
	}
	if plaintext == "" {
		return config, nil
	}
	if json.Unmarshal([]byte(plaintext), &config) != nil {
		return config, ErrMerchantStorePaymentConfiguration
	}
	return config, nil
}

// SaveMerchantStorePaymentGateway never writes shared platform gateway options.
// The model additionally checks the owner's credit balance while holding the
// wallet lock before permitting external payment methods to be enabled.
func SaveMerchantStorePaymentGateway(sellerID int, input MerchantStoreGatewayConfigInput) (*MerchantStoreGatewayView, error) {
	provider := strings.TrimSpace(input.Provider)
	if sellerID <= 0 || !merchantStorePaymentProviderSupported(provider) {
		return nil, ErrMerchantStorePaymentConfiguration
	}
	config, err := loadMerchantStoreGatewayConfig(sellerID, provider)
	if err != nil {
		return nil, err
	}
	if strings.HasPrefix(provider, "external:") {
		for _, field := range []struct {
			dst   *string
			value string
		}{
			{&config.GatewayURL, input.GatewayURL}, {&config.PartnerID, input.PartnerID}, {&config.Key, input.Key},
			{&config.PaymentType, input.PaymentType}, {&config.Currency, input.Currency}, {&config.MerchantID, input.MerchantID},
			{&config.PrivateKey, input.PrivateKey}, {&config.StoreID, input.StoreID}, {&config.ProductID, input.ProductID}, {&config.Environment, input.Environment},
		} {
			if value := strings.TrimSpace(field.value); value != "" {
				*field.dst = value
			}
		}
		config.Currency = strings.ToUpper(config.Currency)
		if config.Currency == "" {
			config.Currency = "USD"
			if provider == MerchantStoreExternalEpay {
				config.Currency = "CNY"
			}
		}
		if config.Environment == "" {
			config.Environment = "prod"
		}
		if config.PaymentType == "" {
			config.PaymentType = "alipay"
		}
		if input.Enabled {
			if err := validateMerchantStoreGatewayConfig(provider, config); err != nil {
				return nil, err
			}
			if provider == MerchantStoreExternalPancake {
				if _, err := newMerchantStorePancakeClient(config, "configuration"); err != nil {
					return nil, ErrMerchantStorePaymentConfiguration
				}
			}
		}
	} else if input.Enabled {
		config, err = merchantStorePlatformGatewayConfig(provider)
		if err != nil {
			return nil, err
		}
	}
	encodedConfig := ""
	if strings.HasPrefix(provider, "external:") {
		encoded, marshalErr := json.Marshal(config)
		err = marshalErr
		if err != nil {
			return nil, ErrMerchantStorePaymentConfiguration
		}
		encodedConfig = string(encoded)
	}
	// The model encrypts this private JSON with an owner/provider-specific
	// persistent purpose before saving. No caller can persist a plaintext key.
	if _, err := model.SaveMerchantStoreGateway(sellerID, provider, input.Enabled, encodedConfig); err != nil {
		return nil, err
	}
	view := merchantStorePublicGatewayView(sellerID, provider, input.Enabled, config)
	if err := merchantStoreGatewayPolicy(sellerID, &view); err != nil {
		return nil, err
	}
	return &view, nil
}

func merchantStoreGatewayPolicy(sellerID int, view *MerchantStoreGatewayView) error {
	categories, err := model.GetMerchantStorePaymentCategories(sellerID)
	if err != nil {
		return err
	}
	u, err := model.GetUserById(sellerID, false)
	if err != nil {
		return err
	}
	merchantStoreApplyGatewayPolicy(categories, u.Quota, view)
	return nil
}

func merchantStoreApplyGatewayPolicy(categories model.MerchantStorePaymentCategories, quota int, view *MerchantStoreGatewayView) {
	view.Category, _ = model.MerchantStorePaymentCategory(view.Provider)
	view.CategoryEnabled = categories.Enabled(view.Provider)
	view.EffectiveEnabled = view.Enabled && view.CategoryEnabled && view.Configured && (view.Category != model.MerchantStoreCategoryExternal || quota > model.MerchantStoreExternalMinimumQuota)
}

// All payment methods are returned even for a new seller, with enabled=false.
// These views are owner-only; a public product exposes just enabled methods.
func ListMerchantStorePaymentGateways(sellerID int) ([]MerchantStoreGatewayView, error) {
	rows, err := model.ListMerchantStoreGateways(sellerID)
	if err != nil {
		return nil, err
	}
	categories, err := model.GetMerchantStorePaymentCategories(sellerID)
	if err != nil {
		return nil, err
	}
	user, err := model.GetUserById(sellerID, false)
	if err != nil {
		return nil, err
	}
	enabled := map[string]bool{}
	for _, row := range rows {
		enabled[row.Provider] = row.Enabled
	}
	views := make([]MerchantStoreGatewayView, 0, 5)
	for _, provider := range model.MerchantStorePaymentProviders() {
		var config merchantStoreGatewayConfig
		if strings.HasPrefix(provider, "external:") {
			config, err = loadMerchantStoreGatewayConfig(sellerID, provider)
		} else {
			config, err = merchantStorePlatformGatewayConfig(provider)
		}
		view := merchantStorePublicGatewayView(sellerID, provider, enabled[provider], config)
		if err != nil {
			view.Configured = false
			view.UnavailableCode = "payment_configuration_required"
		}
		merchantStoreApplyGatewayPolicy(categories, user.Quota, &view)
		views = append(views, view)
	}
	return views, nil
}

type MerchantStorePlatformMethodView struct {
	Provider        string `json:"provider"`
	PaymentType     string `json:"payment_type,omitempty"`
	Name            string `json:"name,omitempty"`
	Supported       bool   `json:"supported"`
	Configured      bool   `json:"configured"`
	UnavailableCode string `json:"unavailable_code,omitempty"`
}

// Public capability metadata contains neither gateway credentials nor owners.
// Seller enablement remains separate and defaults to false for every method.
func AvailableMerchantStorePlatformMethods(catalog []MerchantStorePlatformMethodView) []MerchantStorePlatformMethodView {
	views := make([]MerchantStorePlatformMethodView, 0, len(catalog))
	for _, view := range catalog {
		if view.Supported && view.Configured {
			views = append(views, view)
		}
	}
	return views
}

// Only display fields cross this public boundary. Unknown platform methods
// are capability declarations, never newly accepted store order providers.
func MerchantStorePlatformPaymentCatalog(methods []map[string]string) []MerchantStorePlatformMethodView {
	views := []MerchantStorePlatformMethodView{{Provider: MerchantStoreBalance, PaymentType: "balance", Name: "Platform balance", Supported: true, Configured: true}}
	seen := map[string]bool{"balance": true}
	for _, method := range methods {
		paymentType := strings.TrimSpace(method["type"])
		if paymentType == "" || len(paymentType) > 128 || seen[paymentType] {
			continue
		}
		seen[paymentType] = true
		name := strings.TrimSpace(method["name"])
		if name == "" || len(name) > 200 {
			name = paymentType
		}
		view := MerchantStorePlatformMethodView{PaymentType: paymentType, Name: name, UnavailableCode: "merchant_settlement_unsupported"}
		switch paymentType {
		case "waffo_pancake":
			view.Provider = MerchantStorePlatformPancake
		case "epay":
			unit := strings.ToUpper(strings.TrimSpace(method["settlement_currency"]))
			if unit == "" {
				unit = strings.ToUpper(strings.TrimSpace(method["settlement_unit"]))
			}
			if unit == "LDC" {
				view.Provider = MerchantStorePlatformLinuxDO
			}
		}
		if view.Provider != "" {
			view.Supported = true
			_, err := merchantStorePlatformGatewayConfig(view.Provider)
			view.Configured = err == nil
			view.UnavailableCode = ""
			if err != nil {
				view.UnavailableCode = "payment_configuration_required"
			}
		}
		views = append(views, view)
	}
	return views
}

// Public products expose a usable provider identifier, never credentials.
// Model policy already intersected category, channel and product selection.
func FilterMerchantStorePublicPaymentMethods(product *model.MerchantStoreProduct) {
	methods := make([]string, 0, len(product.PaymentMethods))
	for _, provider := range product.PaymentMethods {
		if !merchantStorePaymentProviderSupported(provider) {
			continue
		}
		var config merchantStoreGatewayConfig
		var err error
		if strings.HasPrefix(provider, "external:") {
			config, err = loadMerchantStoreGatewayConfig(product.SellerID, provider)
		} else {
			config, err = merchantStorePlatformGatewayConfig(provider)
		}
		if err == nil && validateMerchantStoreGatewayConfig(provider, config) == nil {
			methods = append(methods, provider)
		}
	}
	product.PaymentMethods = methods
	product.TradingPaused = product.TradingPaused || len(methods) == 0
}
