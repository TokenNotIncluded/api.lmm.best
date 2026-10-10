package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func TestMerchantStoreRemainingQuotaKeepsThousandItemPoolAndExistingObligations(t *testing.T) {
	f := newVariantsFixture(t, "platform:waffo_pancake")
	stock, err := ListMerchantStoreStock(f.seller.Id, f.product.ID, 0, 30)
	require.NoError(t, err)
	for _, item := range stock {
		require.NoError(t, RemoveMerchantStoreStock(f.seller.Id, f.product.ID, item.ID))
	}
	items := make([]string, 1000)
	for index := range items {
		items[index] = fmt.Sprintf("ONLY-CUSTOM-POOL-%04d", index)
	}
	variant := storeTestVariant(t, f, "自定义规格：93天 / 商家卡包", 875321, items...)
	storePublishVariants(t, f)
	p := storeSalesProduct(t, f)
	require.EqualValues(t, 1000, p.InventoryTotal)
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(10)))
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 1000, p.InventoryTotal)
	require.EqualValues(t, 10, p.SaleAvailable)
	tooMany := storeVariantCheckout(f, variant, "cannot-buy-eleven", "platform:waffo_pancake")
	tooMany.Quantity = 11
	_, _, err = CreateMerchantStoreOrder(tooMany)
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	require.EqualValues(t, 1000, storeSalesProduct(t, f).InventoryTotal)
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(25)))

	settle := func(order *MerchantStoreOrder, receipt string) {
		minor := (order.PriceQuota*100 + int(common.FixedCreditsPerUSD) - 1) / int(common.FixedCreditsPerUSD)
		require.NoError(t, BindMerchantStorePaymentQuote(order.ID, int64(minor), "USD", "1"))
		require.NoError(t, CompleteMerchantStorePayment(order.ID, receipt))
	}
	old := storeVariantCheckout(f, variant, "historical-twenty", "platform:waffo_pancake")
	old.Quantity = 20
	historical, _, err := CreateMerchantStoreOrder(old)
	require.NoError(t, err)
	settle(historical, "paid-history-twenty")
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(10)))
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 30, *p.SaleLimit, "the UI supplies remaining ten, not lifetime thirty")
	require.EqualValues(t, 20, p.PaidQuantity)
	require.EqualValues(t, 980, p.InventoryTotal)
	require.EqualValues(t, 10, p.SaleAvailable)

	reserve := storeVariantCheckout(f, variant, "reserve-three", "platform:waffo_pancake")
	reserve.Quantity = 3
	first, _, err := CreateMerchantStoreOrder(reserve)
	require.NoError(t, err)
	other := marketTestUser(t, DB, "quota-other-buyer", 10000000, common.RoleCommonUser)
	require.NoError(t, AcceptMerchantStoreDisclaimer(other.Id, MerchantStoreDisclaimerVersion))
	secondInput := storeVariantCheckout(f, variant, "reserve-two", "platform:waffo_pancake")
	secondInput.BuyerID, secondInput.PickupEmail, secondInput.Quantity = other.Id, "other-quota@example.test", 2
	second, _, err := CreateMerchantStoreOrder(secondInput)
	require.NoError(t, err)
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 5, p.ReservedQuantity)
	require.EqualValues(t, 5, p.SaleAvailable)
	require.EqualValues(t, 980, p.InventoryTotal, "reserving a card does not remove it from undelivered inventory")

	_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, variant.ID, []string{"RESTOCK-DOES-NOT-EXPAND-QUOTA"})
	require.NoError(t, err)
	require.EqualValues(t, 5, storeSalesProduct(t, f).SaleAvailable)
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(2)))
	require.Zero(t, storeSalesProduct(t, f).SaleAvailable, "lowering below active reservations blocks only new orders")
	replay, created, err := CreateMerchantStoreOrder(reserve)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, first.ID, replay.ID)

	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(10)))
	require.NoError(t, CancelMerchantStoreOrder(other.Id, second.ID))
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 3, p.ReservedQuantity)
	require.EqualValues(t, 7, p.SaleAvailable)
	settle(first, "paid-original-three")
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 23, p.PaidQuantity)
	require.Zero(t, p.ReservedQuantity)
	require.EqualValues(t, 7, p.SaleAvailable)
	require.EqualValues(t, 978, p.InventoryTotal)

	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(10)))
	p = storeSalesProduct(t, f)
	require.EqualValues(t, 33, *p.SaleLimit)
	require.EqualValues(t, 10, p.SaleAvailable)
	require.ErrorIs(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(int64(common.MaxWalletQuota))), ErrMerchantStoreInput)
	require.EqualValues(t, 33, *storeSalesProduct(t, f).SaleLimit, "overflow cannot overwrite the current limit")
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(0)))
	_, _, err = CreateMerchantStoreOrder(storeVariantCheckout(f, variant, "new-blocked", "platform:waffo_pancake"))
	require.ErrorIs(t, err, ErrMerchantStoreStock)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, first.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, err)
	require.Len(t, claim.Items, 3)
	require.Equal(t, variant.Name, claim.VariantName)
	for _, item := range claim.Items {
		require.Contains(t, item, "ONLY-CUSTOM-POOL-")
	}
	require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, nil))
	require.EqualValues(t, 978, storeSalesProduct(t, f).SaleAvailable)
}
