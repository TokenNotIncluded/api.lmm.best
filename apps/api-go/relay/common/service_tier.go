package common

import "github.com/tidwall/gjson"

// ObserveServiceTier reads provider response metadata, never request parameters.
func (info *RelayInfo) ObserveServiceTier(data []byte) {
	if info == nil || info.ServiceTierQuote == nil {
		return
	}
	value := gjson.GetBytes(data, "service_tier")
	if !value.Exists() {
		value = gjson.GetBytes(data, "response.service_tier")
	}
	if value.Type == gjson.String {
		info.ServiceTierQuote.Observe(value.String())
	}
}
