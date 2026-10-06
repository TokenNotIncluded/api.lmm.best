package model

import (
	"encoding/json"
	"net/url"
	"strings"
	"unicode"
)

type merchantStoreDeliveryField struct {
	Required bool
	MaxBytes int
	URL      bool
}

func storeDeliveryTemplateSupported(template string) bool {
	switch template {
	case "card-key", "text", "custom-text", "redemption-code", "license-key", "download-link", "account-details":
		return true
	default:
		return false
	}
}

func storeStructuredDeliveryFields(template string) map[string]merchantStoreDeliveryField {
	instructions := merchantStoreDeliveryField{MaxBytes: 16384}
	secret := merchantStoreDeliveryField{Required: true, MaxBytes: 4096}
	optionalURL := merchantStoreDeliveryField{MaxBytes: 2048, URL: true}
	switch template {
	case "redemption-code":
		return map[string]merchantStoreDeliveryField{"code": secret, "redeem_url": optionalURL, "instructions": instructions}
	case "license-key":
		return map[string]merchantStoreDeliveryField{"license_key": secret, "product": {MaxBytes: 1000}, "instructions": instructions}
	case "download-link":
		return map[string]merchantStoreDeliveryField{"url": {Required: true, MaxBytes: 2048, URL: true}, "access_code": {MaxBytes: 4096}, "instructions": instructions}
	case "account-details":
		return map[string]merchantStoreDeliveryField{"username": {Required: true, MaxBytes: 1000}, "password": secret, "url": optionalURL, "instructions": instructions}
	default:
		return nil
	}
}

// Delivery links are displayed to the buyer, never fetched or executed by the
// server. Keep schemes, credentials and control characters out of URL fields.
func storeDeliveryURL(value string) bool {
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, char := range value {
		if unicode.IsControl(char) {
			return false
		}
	}
	u, err := url.Parse(value)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" && u.User == nil && u.Opaque == ""
}

// Each input element remains exactly one inventory unit, including multiline
// text. Unmarked/unknown JSON and another template's JSON remain literal text.
// Only an explicitly recognized matching v1 envelope is a structured import.
func validateMerchantStoreDeliveryItem(template, item string) error {
	var envelope map[string]json.RawMessage
	if json.Unmarshal([]byte(item), &envelope) != nil {
		return nil
	}
	var marker any
	if json.Unmarshal(envelope["lmm_store_delivery"], &marker) != nil {
		return nil
	}
	// Match JSON.parse's numeric version semantics; quoted versions are plain
	// text, but numeric 1.0 and 1e0 still require the full v1 field validation.
	version, numeric := marker.(float64)
	if !numeric || version != 1 {
		return nil
	}
	var kind string
	if json.Unmarshal(envelope["template"], &kind) != nil || kind != template {
		return nil
	}
	fields := storeStructuredDeliveryFields(kind)
	if fields == nil {
		return nil
	}
	if len(envelope) != 3 {
		return ErrMerchantStoreInput
	}
	var values map[string]any
	if json.Unmarshal(envelope["fields"], &values) != nil || values == nil {
		return ErrMerchantStoreInput
	}
	for key, raw := range values {
		field, exists := fields[key]
		value, stringValue := raw.(string)
		if !exists || !stringValue || len(value) > field.MaxBytes {
			return ErrMerchantStoreInput
		}
		if value != "" && field.URL && !storeDeliveryURL(value) {
			return ErrMerchantStoreInput
		}
	}
	for key, field := range fields {
		if field.Required {
			value, ok := values[key].(string)
			if !ok || strings.TrimSpace(value) == "" {
				return ErrMerchantStoreInput
			}
		}
	}
	return nil
}
