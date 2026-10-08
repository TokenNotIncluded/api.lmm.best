package service

import (
	"encoding/json"
	"errors"
	"net/url"
	"os"
	"strings"

	"github.com/LIghtJUNction/api.lmm.best/internal/commerceimport"
	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
)

const CommerceImportCallbackPath = "/api/user/auth/store-commerce-import/callback"
const CommerceImportCompletePath = "/api/user/auth/store-commerce-import/complete"

var ErrCommerceImportConfiguration = errors.New("commerce import requires reviewed trusted origins")
var ErrCommerceImportReauthorize = errors.New("commerce connection requires a new authorization")
var ErrCommerceImportManualRecovery = errors.New("commerce issuance needs manual recovery")

type CommerceImportConfiguration struct {
	Enabled             bool     `json:"enabled"`
	RedirectURI         string   `json:"redirect_uri"`
	TrustedOrigins      []string `json:"trusted_origins"`
	EnvironmentOverride bool     `json:"environment_override"`
}

// The operator trust list is separate from merchant input. An arbitrary URL
// supplied by a buyer, listing or merchant never becomes a credential target.
func GetCommerceImportConfiguration() CommerceImportConfiguration {
	result := CommerceImportConfiguration{TrustedOrigins: []string{}}
	origin := strings.TrimSpace(system_setting.ServerAddress)
	if commerceimport.ValidateOrigin(origin) != nil {
		return result
	}
	result.RedirectURI = origin + CommerceImportCallbackPath
	var options []model.Option
	if model.DB == nil || model.DB.Where("key IN ?", []string{setting.MerchantStoreCommerceImportEnabledOption, setting.MerchantStoreCommerceImportTrustedOriginsOption}).Find(&options).Error != nil {
		return result
	}
	configured := []string{}
	activated := false
	for _, option := range options {
		if setting.ValidateCommerceImportOption(option.Key, option.Value) != nil {
			return result
		}
		if option.Key == setting.MerchantStoreCommerceImportEnabledOption {
			activated = option.Value == "true"
		}
		if option.Key == setting.MerchantStoreCommerceImportTrustedOriginsOption {
			_ = json.Unmarshal([]byte(option.Value), &configured)
		}
	}
	if raw, override := os.LookupEnv("MERCHANT_STORE_COMMERCE_IMPORT_ORIGINS"); override {
		configured = strings.Split(raw, ",")
		activated = strings.TrimSpace(raw) != ""
		result.EnvironmentOverride = true
	}
	seen := map[string]bool{}
	if len(configured) > 50 {
		return result
	}
	for _, raw := range configured {
		candidate := strings.TrimSpace(raw)
		if candidate == "" {
			continue
		}
		if commerceimport.ValidateOrigin(candidate) != nil {
			return CommerceImportConfiguration{TrustedOrigins: []string{}}
		}
		if !seen[candidate] {
			result.TrustedOrigins = append(result.TrustedOrigins, candidate)
			seen[candidate] = true
		}
	}
	result.Enabled = activated && len(result.TrustedOrigins) > 0 && model.CommerceImportSupported()
	return result
}

func commerceImportTrusted(origin string) (CommerceImportConfiguration, error) {
	config := GetCommerceImportConfiguration()
	if !config.Enabled {
		return config, ErrCommerceImportConfiguration
	}
	return config, commerceImportTrustedOrigin(config, origin)
}

// Revocation remains permitted when new imports are disabled, but credentials
// are never sent to an issuer removed from the current operator trust list.
func commerceImportTrustedOrigin(config CommerceImportConfiguration, origin string) error {
	for _, allowed := range config.TrustedOrigins {
		if origin == allowed {
			return nil
		}
	}
	return ErrCommerceImportConfiguration
}

// The configured origin supplies the scheme. Forwarded host/proto never
// determine the callback target; complete callback validation keeps the full
// registered path and query contract in the protocol client.
func CommerceImportRequestURL(host, escapedPath, rawQuery string) (string, error) {
	config := GetCommerceImportConfiguration()
	registered, err := url.Parse(config.RedirectURI)
	if err != nil || registered.Host == "" || escapedPath != CommerceImportCallbackPath {
		return "", model.ErrMerchantStoreDenied
	}
	actual := registered.Scheme + "://" + host + escapedPath
	if rawQuery != "" {
		actual += "?" + rawQuery
	}
	parsed, err := url.Parse(actual)
	if err != nil || parsed.User != nil || !strings.EqualFold(parsed.Hostname(), registered.Hostname()) {
		return "", model.ErrMerchantStoreDenied
	}
	port := func(u *url.URL) string {
		if u.Port() == "" {
			return "443"
		}
		return u.Port()
	}
	if port(parsed) != port(registered) {
		return "", model.ErrMerchantStoreDenied
	}
	return actual, nil
}
