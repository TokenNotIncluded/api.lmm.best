package setting

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCommerceImportSettingsValidateIndependentTrustTargets(t *testing.T) {
	for _, value := range []string{"true", "false"} {
		require.NoError(t, ValidateCommerceImportOption(MerchantStoreCommerceImportEnabledOption, value))
	}
	for _, value := range []string{"1", "TRUE", "yes", ""} {
		require.Error(t, ValidateCommerceImportOption(MerchantStoreCommerceImportEnabledOption, value))
	}
	for _, value := range []string{`[]`, `["https://one.example.com","https://two.example.com:8443"]`} {
		require.NoError(t, ValidateCommerceImportOption(MerchantStoreCommerceImportTrustedOriginsOption, value))
	}
	for _, value := range []string{`null`, `{}`, `["http://one.example.com"]`, `["https://one.example.com/"]`, `["https://localhost"]`, `["https://127.0.0.1"]`, `["https://one.example.com?q=x"]`, `["https://one.example.com","https://one.example.com"]`} {
		require.Error(t, ValidateCommerceImportOption(MerchantStoreCommerceImportTrustedOriginsOption, value), value)
	}
}
