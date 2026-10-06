package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStorePaymentCategoriesDefaultCompatibilityAndReservedIsolation(t *testing.T) {
	f := newStoreFixture(t, "balance", "external:epay")
	// Simulate an installed Go85 merchant with only real gateway rows.
	require.NoError(t, DB.Where("seller_id = ? AND provider IN ?", f.seller.Id, []string{storeCategoryPlatformProvider, storeCategoryExternalProvider}).Delete(&MerchantStoreGateway{}).Error)
	categories, err := GetMerchantStorePaymentCategories(f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, MerchantStorePaymentCategories{true, true}, categories)
	fresh := marketTestUser(t, DB, "fresh-l0-seller", 0, common.RoleCommonUser)
	require.NoError(t, DB.Create(&MerchantStoreGateway{ID: "unknown-legacy", SellerID: fresh.Id, Provider: "unknown:old", Enabled: true}).Error)
	categories, err = GetMerchantStorePaymentCategories(fresh.Id)
	require.NoError(t, err)
	require.Equal(t, MerchantStorePaymentCategories{}, categories)
	_, err = SaveMerchantStoreGateway(fresh.Id, "balance", true, "")
	require.NoError(t, err)
	categories, err = GetMerchantStorePaymentCategories(fresh.Id)
	require.NoError(t, err)
	require.False(t, categories.PlatformEnabled, "first channel enable cannot implicitly opt a new merchant into a category")
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{ExternalEnabled: true}))
	// Explicit off survives subsequent channel changes; credentials stay intact.
	_, err = SaveMerchantStoreGateway(f.seller.Id, "balance", true, "")
	require.NoError(t, err)
	categories, err = GetMerchantStorePaymentCategories(f.seller.Id)
	require.NoError(t, err)
	require.False(t, categories.PlatformEnabled)
	rows, err := ListMerchantStoreGateways(f.seller.Id)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.NotEqual(t, storeCategoryPlatformProvider, row.Provider)
		require.NotEqual(t, storeCategoryExternalProvider, row.Provider)
	}
	_, err = GetMerchantStoreGatewaySecret(f.seller.Id, storeCategoryPlatformProvider)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	_, err = SaveMerchantStoreGateway(f.seller.Id, storeCategoryPlatformProvider, true, `{}`)
	require.ErrorIs(t, err, ErrMerchantStoreInput)
	secret, err := GetMerchantStoreGatewaySecret(f.seller.Id, "external:epay")
	require.NoError(t, err)
	require.Contains(t, secret, "fixture")
}

func TestMerchantStorePaymentSelectionIsMerchantSubsetAndPrivateDraftSurvivesClosing(t *testing.T) {
	f := newStoreFixture(t, "balance")
	_, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Invalid selection", PriceQuota: 1, PaymentMethods: []string{"platform:linuxdo"}})
	require.ErrorIs(t, err, ErrMerchantStorePaymentSelection)
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}))
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.Empty(t, public.PaymentMethods)
	require.True(t, public.TradingPaused)
	private, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.Equal(t, []string{"balance"}, private.PaymentMethods)
	_, _, err = CreateMerchantStoreOrder(f.checkout("closed", "balance"))
	require.ErrorIs(t, err, ErrMerchantStorePaymentCategoryDisabled)
	_, err = SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "Closed subset", PriceQuota: 1, PaymentMethods: []string{"balance"}})
	require.ErrorIs(t, err, ErrMerchantStorePaymentSelection)
	_, err = SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "No methods yet", PriceQuota: 1})
	require.NoError(t, err)
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{PlatformEnabled: true}))
	_, _, err = CreateMerchantStoreOrder(f.checkout("reopened", "balance"))
	require.NoError(t, err)
}

func TestMerchantStoreCategoryClosingStopsFirstIssuanceButPreservesFrozenObligations(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	first, _, err := CreateMerchantStoreOrder(f.checkout("issued", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(first.ID, 100, "USD", "1"))
	require.NoError(t, BindMerchantStorePaymentContext(first.ID, "frozen-existing-invoice"))
	secondInput := f.checkout("not-issued", "platform:waffo_pancake")
	// Pending limit intentionally permits only one order per product/buyer.
	other := marketTestUser(t, DB, "second-buyer", 1000000, common.RoleCommonUser)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", other.Id).Update("email", "second@example.test").Error)
	require.NoError(t, MarkMerchantStoreEmailVerified(other.Id, "second@example.test"))
	require.NoError(t, AcceptMerchantStoreDisclaimer(other.Id, MerchantStoreDisclaimerVersion))
	secondInput.BuyerID = other.Id
	second, _, err := CreateMerchantStoreOrder(secondInput)
	require.NoError(t, err)
	require.NoError(t, BindMerchantStorePaymentQuote(second.ID, 100, "USD", "1"))
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}))
	require.ErrorIs(t, BindMerchantStorePaymentContext(second.ID, "not-yet-issued-invoice"), ErrMerchantStorePaymentCategoryDisabled)
	require.NoError(t, BindMerchantStorePaymentContext(first.ID, "frozen-existing-invoice"))
	replayed, created, err := CreateMerchantStoreOrder(f.checkout("issued", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, replayed.ID)
	require.NoError(t, CancelMerchantStoreOrder(other.Id, second.ID))
	require.NoError(t, CompleteMerchantStorePayment(first.ID, "verified-provider-payment"))
	require.NoError(t, CompleteMerchantStorePayment(first.ID, "verified-provider-payment"))
	storeBalance(t, f.seller.Id, 10495000)
	storeBalance(t, f.root.Id, 5000)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", first.ID).Count(&count).Error)
	require.EqualValues(t, 3, count) // fee escrow, fee recipient, merchant credit
}
