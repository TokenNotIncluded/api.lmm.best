package model

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStorePickupDetailsUseFrozenOrderIdentityAndRetainedProductContent(t *testing.T) {
	f := newStoreFixture(t, "balance")
	input := f.checkout("pickup-details", "balance")
	input.Quantity = 2
	order, _, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)

	f.product.Title = "Renamed after purchase"
	f.product.Description = "Current merchant instructions\nsecond line <img src=x>"
	f.product.Links = []MerchantStoreLink{
		{Title: "Instructions", URL: "https://merchant.example/instructions?first=1&second=2", Description: "Merchant supplied link"},
		{Title: "Unsafe old value", URL: "javascript:alert(1)"},
		{Title: "Credential URL", URL: "https://user:password@merchant.example/private"},
		{Title: "Control characters", URL: "https://merchant.example/\x00"},
	}
	// Retired products remain stored; existing purchases retain delivery access.
	f.product.Status = "deleted"
	require.NoError(t, DB.Save(f.product).Error)

	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	publicMetadata, err := json.Marshal(metadata)
	require.NoError(t, err)
	require.NotContains(t, string(publicMetadata), "product_description")
	require.NotContains(t, string(publicMetadata), "merchant.example")

	for _, attempt := range []struct {
		code  string
		buyer int
	}{{"wrong-code", f.buyer.Id}, {input.PickupCode, f.seller.Id}} {
		claim, err := ClaimMerchantStoreOrder(token, attempt.code, attempt.buyer)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		require.Empty(t, claim.ProductDescription)
		require.Empty(t, claim.ProductLinks)
		require.Empty(t, claim.Items)
	}

	claim, err := ClaimMerchantStoreOrder(token, input.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, order.ProductTitle, claim.ProductTitle)
	require.NotEqual(t, f.product.Title, claim.ProductTitle)
	require.Equal(t, f.product.ID, claim.ProductID)
	require.Equal(t, order.TradeNo, claim.TradeNo)
	require.Equal(t, 2, claim.Quantity)
	require.Equal(t, f.product.Description, claim.ProductDescription)
	require.Equal(t, f.product.Links[:1], claim.ProductLinks)
	require.Equal(t, []string{"CARD-SECRET-FIRST", "CARD-SECRET-SECOND"}, claim.Items)
	again, err := ClaimMerchantStoreOrder(token, input.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, claim, again)

	details, err := GetMerchantStoreOrderPickupDetails(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, claim.ProductTitle, details.ProductTitle)
	require.Equal(t, claim.ProductDescription, details.ProductDescription)
	require.Equal(t, claim.ProductLinks, details.ProductLinks)
	serialized, err := json.Marshal(details)
	require.NoError(t, err)
	require.NotContains(t, string(serialized), "CARD-SECRET")
	require.NotContains(t, string(serialized), "store-buyer@example.test")
	require.NotContains(t, string(serialized), "pickup_token")
}

func TestMerchantStorePickupDetailsRejectOtherBuyersAndUnpaidOrders(t *testing.T) {
	t.Run("unpaid", func(t *testing.T) {
		f := newStoreFixture(t, "external:epay")
		order, _, err := CreateMerchantStoreOrder(f.checkout("unpaid-details", "external:epay"))
		require.NoError(t, err)
		for _, buyer := range []int{0, f.buyer.Id, f.seller.Id} {
			details, err := GetMerchantStoreOrderPickupDetails(buyer, order.ID)
			require.ErrorIs(t, err, ErrMerchantStoreDenied)
			require.Nil(t, details)
		}
	})
	t.Run("other-buyer", func(t *testing.T) {
		f := newStoreFixture(t, "balance")
		order, _, err := CreateMerchantStoreOrder(f.checkout("paid-details", "balance"))
		require.NoError(t, err)
		details, err := GetMerchantStoreOrderPickupDetails(f.seller.Id, order.ID)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		require.Nil(t, details)
	})
}

func TestMerchantStorePickupDetailsFreezeArbitraryMerchantVariantName(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	variant := storeTestVariant(t, f, "商家自定义：标准版 / 93天 · 特别包", 875000, "ONLY-THIS-VARIANT")
	storeTestVariant(t, f, "另一规格（独立卡池）", 1250000, "PRIVATE-OTHER-POOL")
	storePublishVariants(t, f)
	input := storeVariantCheckout(f, variant, "frozen-freeform-details", "balance")
	order, _, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.NoError(t, DB.Model(variant).Update("name", "Changed after purchase").Error)
	details, err := GetMerchantStoreOrderPickupDetails(f.buyer.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, variant.ID, details.VariantID)
	require.Equal(t, "商家自定义：标准版 / 93天 · 特别包", details.VariantName)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, input.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, details.VariantID, claim.VariantID)
	require.Equal(t, details.VariantName, claim.VariantName)
	require.Equal(t, []string{"ONLY-THIS-VARIANT"}, claim.Items)
	encoded, err := json.Marshal(details)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "ONLY-THIS-VARIANT")
	require.NotContains(t, string(encoded), "PRIVATE-OTHER-POOL")
}
