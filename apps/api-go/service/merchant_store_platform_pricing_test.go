package service

import (
	"encoding/json"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/model"
	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/LIghtJUNction/api.lmm.best/setting/system_setting"
	"github.com/stretchr/testify/require"
)

func merchantStoreTestLinuxDOSettings(t *testing.T, method map[string]string) {
	t.Helper()
	merchantStoreTestCreditBasis(t)
	oldAddress, oldID, oldKey, oldMethods := operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods
	oldOrigin, oldCallback := system_setting.ServerAddress, operation_setting.CustomCallbackAddress
	payment := operation_setting.GetPaymentSetting()
	oldCompliance, oldTerms := payment.ComplianceConfirmed, payment.ComplianceTermsVersion
	t.Cleanup(func() {
		operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey, operation_setting.PayMethods = oldAddress, oldID, oldKey, oldMethods
		system_setting.ServerAddress, operation_setting.CustomCallbackAddress = oldOrigin, oldCallback
		payment.ComplianceConfirmed, payment.ComplianceTermsVersion = oldCompliance, oldTerms
	})
	operation_setting.PayAddress, operation_setting.EpayId, operation_setting.EpayKey = "https://credit.linux.do/epay", "123", "platform-private-key"
	operation_setting.PayMethods = nil
	if method != nil {
		operation_setting.PayMethods = []map[string]string{method}
	}
	system_setting.ServerAddress, operation_setting.CustomCallbackAddress = "https://api.example.com", ""
	payment.ComplianceConfirmed, payment.ComplianceTermsVersion = true, operation_setting.CurrentComplianceTermsVersion
}

// This is the Go85 reader's pre-existing DTO. Optional new pricing evidence
// is ignored; the reader authenticates the original frozen amount and account.
type merchantStoreLegacy85Context struct {
	Provider     string                     `json:"provider"`
	Config       merchantStoreGatewayConfig `json:"config"`
	AmountMinor  int64                      `json:"amount_minor"`
	Currency     string                     `json:"currency"`
	FrozenRate   string                     `json:"frozen_rate"`
	Origin       string                     `json:"origin"`
	PublicOrigin string                     `json:"public_origin"`
	ExpiresIn    int                        `json:"expires_in"`
}

func TestMerchantStorePlatformPricingExactMinorAndGo85FrozenReaderCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name       string
		method     map[string]string
		minor      int64
		legacyRate string
	}{
		{"direct", map[string]string{"type": "epay", "settlement_unit": "LDC", "settlement_units_per_platform_unit": "20"}, 2000, "20"},
		{"paired", map[string]string{"type": "epay", "settlement_unit": "LDC", "platform_units_per_usd": "6.8", "settlement_units_per_usd": "68"}, 1000, "68"},
		{"cyclic_pair", map[string]string{"type": "epay", "settlement_unit": "LDC", "platform_units_per_usd": "6.8", "settlement_units_per_usd": "1"}, 15, "1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := merchantStoreServiceDB(t, MerchantStorePlatformLinuxDO)
			merchantStoreTestLinuxDOSettings(t, tc.method)
			order, _, err := model.CreateMerchantStoreOrder(model.MerchantStoreCheckoutInput{BuyerID: f.buyer.Id, ProductID: f.product.ID, Quantity: 1, RequestKey: "priced-order", PaymentMethod: MerchantStorePlatformLinuxDO})
			require.NoError(t, err)
			require.NoError(t, prepareMerchantStorePaymentContext(order, "LDC"))
			order, err = model.GetMerchantStorePaymentOrder(order.ID)
			require.NoError(t, err)
			require.Equal(t, tc.minor, order.AmountMinor)
			require.Equal(t, tc.legacyRate, order.FrozenUSDFX, "original declaration is integrity evidence, never derived money")
			modern, err := loadMerchantStorePaymentContext(order)
			require.NoError(t, err)
			require.Equal(t, "platform_pay_methods", modern.PricingSource)
			require.NotNil(t, modern.PlatformPricing)
			var old merchantStoreLegacy85Context
			require.NoError(t, decryptMerchantStorePaymentValue(merchantStorePaymentContextPurpose(order.ID), order.GatewaySnapshot, &old))
			require.Equal(t, order.PaymentMethod, old.Provider)
			require.Equal(t, order.AmountMinor, old.AmountMinor)
			require.Equal(t, order.Currency, old.Currency)
			require.Equal(t, order.FrozenUSDFX, old.FrozenRate)
			require.NoError(t, validateMerchantStoreGatewayConfig(old.Provider, old.Config))
			_, err = merchantStorePublicHTTPSURL(old.Origin, false)
			require.NoError(t, err)
			_, err = merchantStorePublicHTTPSURL(old.PublicOrigin, false)
			require.NoError(t, err)
			oldScope, err := merchantStorePaymentScopeHash(merchantStorePaymentContext{Provider: old.Provider, Config: old.Config})
			require.NoError(t, err)
			require.Equal(t, order.PaymentScopeHash, oldScope)
			// New platform config and category policy cannot reprice an invoice.
			operation_setting.PayMethods = []map[string]string{{"type": "epay", "enabled": "false", "settlement_unit": "CNY"}}
			operation_setting.EpayKey = "a-rotated-key-never-used-for-this-invoice"
			require.NoError(t, model.SetMerchantStorePaymentCategories(f.seller.Id, model.MerchantStorePaymentCategories{}))
			require.NoError(t, prepareMerchantStorePaymentContext(order, "LDC"))
			unchanged, err := loadMerchantStorePaymentContext(order)
			require.NoError(t, err)
			require.Equal(t, modern, unchanged)
			tradeID, err := validateMerchantStoreEpayCallback(order, unchanged, merchantStoreTestEpaySigned(order, unchanged, nil))
			require.NoError(t, err)
			require.Equal(t, "PROVIDER_123", tradeID)
		})
	}
}

func TestMerchantStoreCatalogDeclaresUnsupportedTypesAndNeverPublishesCredentials(t *testing.T) {
	merchantStoreTestLinuxDOSettings(t, map[string]string{"type": "epay", "settlement_unit": "LDC", "unit_price": "20"})
	methods := []map[string]string{{"type": "epay", "name": "Linux DO", "settlement_unit": "LDC", "key": "PRIVATE-DO-NOT-RETURN"}, {"type": "stripe", "name": "Stripe", "private_key": "NEVER-RETURN"}, {"type": "future_gateway", "name": "Future"}}
	catalog := MerchantStorePlatformPaymentCatalog(methods)
	publicJSON, err := json.Marshal(catalog)
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), "PRIVATE-DO-NOT-RETURN")
	require.NotContains(t, string(publicJSON), "NEVER-RETURN")
	require.Len(t, catalog, 4)
	require.True(t, catalog[1].Supported)
	require.True(t, catalog[1].Configured)
	require.Equal(t, MerchantStorePlatformLinuxDO, catalog[1].Provider)
	for _, row := range catalog[2:] {
		require.False(t, row.Supported)
		require.False(t, row.Configured)
		require.Empty(t, row.Provider)
		require.Equal(t, "merchant_settlement_unsupported", row.UnavailableCode)
	}
	selectable := AvailableMerchantStorePlatformMethods(catalog)
	require.Len(t, selectable, 2)
	operation_setting.PayMethods[0]["enabled"] = "false"
	selectable = AvailableMerchantStorePlatformMethods(MerchantStorePlatformPaymentCatalog(methods))
	require.Len(t, selectable, 1)
	require.Equal(t, MerchantStoreBalance, selectable[0].Provider)
}
