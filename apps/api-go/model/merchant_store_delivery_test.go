package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm/schema"
)

func storeDeliveryFixture(t *testing.T, template string) storeFixture {
	t.Helper()
	f := newStoreFixture(t, "balance")
	stock, err := ListMerchantStoreStock(f.seller.Id, f.product.ID, 0, 100)
	require.NoError(t, err)
	for _, item := range stock {
		require.NoError(t, RemoveMerchantStoreStock(f.seller.Id, f.product.ID, item.ID))
	}
	f.product, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Structured delivery", PriceQuota: 500000, Template: template, PaymentMethods: []string{"balance"}, PickupLoginRequired: true, PickupCodeRequired: true})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "reviewed"))
	return f
}

func storeDeliveryEnvelope(template string, fields map[string]any) string {
	encoded, _ := json.Marshal(map[string]any{"lmm_store_delivery": 1, "template": template, "fields": fields})
	return string(encoded)
}

func TestMerchantStoreDeliveryStructuredItemsAreEncryptedAndPrivatelyDelivered(t *testing.T) {
	for template, fields := range map[string]map[string]any{
		"redemption-code": {"code": "PRIVATE-REDEMPTION", "redeem_url": "https://example.test/redeem", "instructions": "First line\nSecond line"},
		"license-key":     {"license_key": "PRIVATE-LICENSE", "product": "Desktop application", "instructions": "Activate in settings"},
		"download-link":   {"url": "https://example.test/download?private=token", "access_code": "PRIVATE-DOWNLOAD", "instructions": "Download once"},
		"account-details": {"username": "PRIVATE-USERNAME", "password": "PRIVATE-PASSWORD", "url": "https://example.test/signin"},
	} {
		t.Run(template, func(t *testing.T) {
			f := storeDeliveryFixture(t, template)
			item := storeDeliveryEnvelope(template, fields)
			count, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{item})
			require.NoError(t, err)
			require.Equal(t, 1, count)
			var stock MerchantStoreStock
			require.NoError(t, DB.Where("product_id = ?", f.product.ID).First(&stock).Error)
			require.NotContains(t, stock.Ciphertext, "PRIVATE-")
			require.NotContains(t, stock.Ciphertext, "lmm_store_delivery")
			public, err := GetPublicMerchantStoreProduct(f.product.ID)
			require.NoError(t, err)
			encoded, err := json.Marshal(public)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "PRIVATE-")
			o, _, err := CreateMerchantStoreOrder(f.checkout("structured-order", "balance"))
			require.NoError(t, err)
			require.Equal(t, template, o.DeliveryTemplate)
			token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
			require.NoError(t, err)
			metadata, err := InspectMerchantStoreClaim(token)
			require.NoError(t, err)
			require.Equal(t, template, metadata.DeliveryTemplate)
			encoded, err = json.Marshal(metadata)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "PRIVATE-")
			require.NotContains(t, string(encoded), "fields")
			claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
			require.NoError(t, err)
			require.Equal(t, template, claim.DeliveryTemplate)
			require.Equal(t, []string{item}, claim.Items, "structured text remains byte-for-byte intact after encryption")
		})
	}
}

func TestMerchantStoreDeliveryRecognizedImportRejectsInvalidFieldsAtomically(t *testing.T) {
	f := storeDeliveryFixture(t, "redemption-code")
	valid := storeDeliveryEnvelope("redemption-code", map[string]any{"code": "SECRET", "instructions": ""})
	for name, fields := range map[string]map[string]any{
		"missing-required":    {},
		"blank-required":      {"code": " \n "},
		"numeric-secret":      {"code": 1234},
		"null-secret":         {"code": nil},
		"null-optional":       {"code": "SECRET", "instructions": nil},
		"unknown-field":       {"code": "SECRET", "extra": "ignored?"},
		"code-too-many-bytes": {"code": strings.Repeat("中", 1366)},
		"long-instructions":   {"code": "SECRET", "instructions": strings.Repeat("x", 16385)},
		"script-url":          {"code": "SECRET", "redeem_url": "javascript:alert(1)"},
		"data-url":            {"code": "SECRET", "redeem_url": "data:text/plain,secret"},
		"relative-url":        {"code": "SECRET", "redeem_url": "/redeem"},
		"userinfo-url":        {"code": "SECRET", "redeem_url": "https://name:password@example.test/redeem"},
		"control-url":         {"code": "SECRET", "redeem_url": "https://example.test/redeem\n"},
		"long-url":            {"code": "SECRET", "redeem_url": "https://example.test/" + strings.Repeat("x", 2048)},
	} {
		t.Run(name, func(t *testing.T) {
			count, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{valid, storeDeliveryEnvelope("redemption-code", fields)})
			require.ErrorIs(t, err, ErrMerchantStoreInput)
			require.Zero(t, count)
			var stored int64
			require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", f.product.ID).Count(&stored).Error)
			require.Zero(t, stored, "a partly valid batch must not commit partial inventory")
		})
	}
	for _, item := range []string{
		`{"lmm_store_delivery":1,"template":"redemption-code","fields":null}`,
		`{"lmm_store_delivery":1,"template":"redemption-code","fields":[]}`,
		`{"lmm_store_delivery":1,"template":"redemption-code","fields":{"code":"SECRET"},"extra":"x"}`,
		`{"lmm_store_delivery":1.0,"template":"redemption-code","fields":{"code":"SECRET","redeem_url":"javascript:alert(1)"}}`,
		`{"lmm_store_delivery":1e0,"template":"redemption-code","fields":{"code":"SECRET","instructions":null}}`,
	} {
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{item})
		require.ErrorIs(t, err, ErrMerchantStoreInput)
	}
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{storeDeliveryEnvelope("redemption-code", map[string]any{"code": strings.Repeat("x", 4096), "redeem_url": "", "instructions": strings.Repeat("x", 16384)})})
	require.NoError(t, err, "documented byte limits are inclusive")
}

func TestMerchantStoreDeliveryPlainMultilineAndUnknownMarkersRemainLiteral(t *testing.T) {
	f := storeDeliveryFixture(t, "custom-text")
	items := []string{
		"First line\nSecond line\nThird line",
		`{"ordinary":"JSON text"}`,
		`{"lmm_store_delivery":2,"template":"custom-text","fields":{"content":"v2"}}`,
		`{"lmm_store_delivery":1,"template":"unknown-kind","fields":{"arbitrary":"original"}}`,
		storeDeliveryEnvelope("redemption-code", map[string]any{"code": "MISMATCHED-KEEP-LITERAL"}),
	}
	count, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, items)
	require.NoError(t, err)
	require.Equal(t, len(items), count, "multiline text is one unit, not several lines of units")
	in := f.checkout("literal-items", "balance")
	in.Quantity = len(items)
	o, _, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, "custom-text", claim.DeliveryTemplate)
	require.Equal(t, items, claim.Items)
}

func TestMerchantStoreDeliveryDownloadAndAccountTemplatesRequireTheirOwnFields(t *testing.T) {
	for _, test := range []struct {
		Template string
		Valid    map[string]any
		Invalid  []map[string]any
	}{
		{"download-link", map[string]any{"url": "http://example.test/file", "access_code": ""}, []map[string]any{{"url": ""}, {"url": "//example.test/file"}, {"url": "https://example.test/file", "access_code": 123}}},
		{"account-details", map[string]any{"username": "user", "password": "secret", "url": ""}, []map[string]any{{"username": "user"}, {"username": "", "password": "secret"}, {"username": "user", "password": "secret", "url": "https://name:secret@example.test"}}},
		{"license-key", map[string]any{"license_key": "LIC-KEY", "product": ""}, []map[string]any{{"code": "wrong-field"}, {"license_key": "LIC", "product": nil}, {"license_key": "LIC", "product": strings.Repeat("x", 1001)}}},
	} {
		t.Run(test.Template, func(t *testing.T) {
			f := storeDeliveryFixture(t, test.Template)
			for _, fields := range test.Invalid {
				_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{storeDeliveryEnvelope(test.Template, fields)})
				require.ErrorIs(t, err, ErrMerchantStoreInput)
			}
			_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{storeDeliveryEnvelope(test.Template, test.Valid)})
			require.NoError(t, err)
		})
	}
}

func TestMerchantStoreDeliveryFreezesTemplateAndLeavesHistoricalOrdersLiteral(t *testing.T) {
	f := storeDeliveryFixture(t, "account-details")
	item := storeDeliveryEnvelope("account-details", map[string]any{"username": "buyer-private", "password": "PASSWORD"})
	_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{item})
	require.NoError(t, err)
	o, _, err := CreateMerchantStoreOrder(f.checkout("frozen-template", "balance"))
	require.NoError(t, err)
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Now a text product", PriceQuota: 500000, Template: "text", PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, err)
	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.Equal(t, "account-details", metadata.DeliveryTemplate)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, "account-details", claim.DeliveryTemplate)
	require.Equal(t, []string{item}, claim.Items)
	// Simulate a genuine pre-upgrade order's empty migration default. Its raw
	// text must never be inferred from today's product or the embedded marker.
	require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("delivery_template", "").Error)
	metadata, err = InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.Empty(t, metadata.DeliveryTemplate)
	claim, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Empty(t, claim.DeliveryTemplate)
	require.Equal(t, []string{item}, claim.Items)
}

func TestMerchantStoreDeliverySnapshotSchemaIsBoundedWithEmptyHistoryDefault(t *testing.T) {
	parsed, err := schema.Parse(&MerchantStoreOrder{}, &sync.Map{}, schema.NamingStrategy{})
	require.NoError(t, err)
	field := parsed.LookUpField("DeliveryTemplate")
	require.Equal(t, "delivery_template", field.DBName)
	require.Equal(t, "varchar(32)", field.TagSettings["TYPE"])
	require.True(t, field.NotNull)
	require.True(t, field.HasDefaultValue)
	require.Empty(t, field.DefaultValue)
}
