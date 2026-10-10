package model

import (
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func storeSelfPurchaseFixture(t *testing.T, methods ...string) storeFixture {
	t.Helper()
	f := newStoreFixture(t, methods...)
	f.buyer = f.seller
	require.NoError(t, AcceptMerchantStoreDisclaimer(f.seller.Id, MerchantStoreDisclaimerVersion))
	return f
}

func storeSelfPurchaseInventory(t *testing.T, product, state string) int64 {
	t.Helper()
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", product, state).Count(&count).Error)
	return count
}

func TestMerchantStoreSelfPurchaseBalanceDebitsFullPriceAndNetsOnlyNormalFee(t *testing.T) {
	f := storeSelfPurchaseFixture(t, "balance")
	in := f.checkout("normal-self", "balance")
	o, created, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, f.seller.Id, o.BuyerID)
	require.Equal(t, f.seller.Id, o.SellerID)
	require.Equal(t, "paid", o.Status)
	require.Equal(t, 500000, o.PriceQuota)
	require.Equal(t, 5000, o.FeeQuota)
	storeBalance(t, f.seller.Id, 9995000)
	storeBalance(t, f.root.Id, 5000)
	require.EqualValues(t, 1, storeSelfPurchaseInventory(t, f.product.ID, "delivered"))
	require.EqualValues(t, 1, storeSelfPurchaseInventory(t, f.product.ID, "available"))
	token, err := GetMerchantStoreOrderPickupToken(f.seller.Id, o.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.seller.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	var transfers []MerchantStoreTransfer
	require.NoError(t, DB.Where("order_id = ?", o.ID).Order("kind ASC").Find(&transfers).Error)
	require.Len(t, transfers, 2)
	var net int
	for _, transfer := range transfers {
		if transfer.FromUserID == f.seller.Id {
			net -= transfer.Quota
		}
		if transfer.ToUserID == f.seller.Id {
			net += transfer.Quota
		}
	}
	require.Equal(t, -5000, net, "a same-wallet sale nets zero, while the normal merchant fee remains auditable")
	replay, made, err := CreateMerchantStoreOrder(in)
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, o.ID, replay.ID)
	storeBalance(t, f.seller.Id, 9995000)
	storeBalance(t, f.root.Id, 5000)
	require.EqualValues(t, 1, storeSelfPurchaseInventory(t, f.product.ID, "delivered"))
}

func TestMerchantStoreSelfPurchaseRootBuyerSellerRecipientShareWalletWithNoFee(t *testing.T) {
	f := storeSelfPurchaseFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", common.RoleRootUser).Error)
	require.NoError(t, SetMerchantStoreConfig(f.root.Id, MerchantStoreConfig{FeeBPS: 100, RecipientID: f.seller.Id, PromotionQuota: 500000}))
	o, created, err := CreateMerchantStoreOrder(f.checkout("root-self", "balance"))
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, o.OfficialAtPurchase)
	require.Equal(t, f.seller.Id, o.RecipientID)
	require.Zero(t, o.FeeQuota)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	require.EqualValues(t, 1, storeSelfPurchaseInventory(t, f.product.ID, "delivered"))
	var transfers []MerchantStoreTransfer
	require.NoError(t, DB.Where("order_id = ?", o.ID).Find(&transfers).Error)
	require.Len(t, transfers, 1)
	require.Equal(t, "sale", transfers[0].Kind)
	require.Equal(t, f.seller.Id, transfers[0].FromUserID)
	require.Equal(t, f.seller.Id, transfers[0].ToUserID)
}

func TestMerchantStoreSelfPurchaseInsufficientFullPriceRollsBackEverything(t *testing.T) {
	f := storeSelfPurchaseFixture(t, "balance")
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 499999).Error)
	o, created, err := CreateMerchantStoreOrder(f.checkout("cannot-afford-full-price", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreBalance)
	require.False(t, created)
	require.NotEqual(t, "paid", o.Status)
	storeBalance(t, f.seller.Id, 499999)
	storeBalance(t, f.root.Id, 0)
	require.EqualValues(t, 2, storeSelfPurchaseInventory(t, f.product.ID, "available"))
	require.Zero(t, storeSelfPurchaseInventory(t, f.product.ID, "reserved"))
	for _, table := range []any{&MerchantStoreOrder{}, &MerchantStoreTransfer{}, &MerchantStoreEmailDelivery{}} {
		var count int64
		require.NoError(t, DB.Model(table).Count(&count).Error)
		require.Zero(t, count)
	}
}

func TestMerchantStoreSelfPurchasePreservesExistingCheckoutGuards(t *testing.T) {
	for _, scenario := range []string{"disabled-user", "paused", "out-of-stock", "unselected-method", "gateway-disabled", "category-disabled", "disclaimer"} {
		t.Run(scenario, func(t *testing.T) {
			f := storeSelfPurchaseFixture(t, "balance")
			input := f.checkout("blocked-self", "balance")
			var expected error
			switch scenario {
			case "disabled-user":
				require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("status", common.UserStatusDisabled).Error)
				expected = ErrMerchantStoreDenied
			case "paused":
				require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true))
				expected = ErrMerchantStoreUnavailable
			case "out-of-stock":
				input.Quantity = 3
				expected = ErrMerchantStoreStock
			case "unselected-method":
				input.PaymentMethod = "platform:waffo_pancake"
				expected = ErrMerchantStoreDenied
			case "gateway-disabled":
				_, err := SaveMerchantStoreGateway(f.seller.Id, "balance", false, "")
				require.NoError(t, err)
				expected = ErrMerchantStoreUnavailable
			case "category-disabled":
				require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}))
				expected = ErrMerchantStorePaymentCategoryDisabled
			case "disclaimer":
				require.NoError(t, DB.Where("user_id = ?", f.seller.Id).Delete(&MerchantStoreDisclaimerAcceptance{}).Error)
				expected = ErrMerchantStoreDisclaimer
			}
			_, created, err := CreateMerchantStoreOrder(input)
			require.ErrorIs(t, err, expected)
			require.False(t, created)
			storeBalance(t, f.seller.Id, 10000000)
			storeBalance(t, f.root.Id, 0)
			require.EqualValues(t, 2, storeSelfPurchaseInventory(t, f.product.ID, "available"))
		})
	}
}

func TestMerchantStoreSelfPurchaseExternalPaymentAndPlatformCreditSettleOnce(t *testing.T) {
	for _, method := range []string{"external:epay", "platform:waffo_pancake"} {
		t.Run(method, func(t *testing.T) {
			f := storeSelfPurchaseFixture(t, method)
			input := f.checkout("self-gateway", method)
			o, created, err := CreateMerchantStoreOrder(input)
			require.NoError(t, err)
			require.True(t, created)
			require.Equal(t, f.seller.Id, o.BuyerID)
			require.True(t, o.FeeHeld)
			storeBalance(t, f.seller.Id, 9995000)
			storeBalance(t, f.root.Id, 0)
			require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
			require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-self-receipt"))
			require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-self-receipt"))
			replay, made, err := CreateMerchantStoreOrder(input)
			require.NoError(t, err)
			require.False(t, made)
			require.Equal(t, o.ID, replay.ID)
			expected := 9995000
			if method == "platform:waffo_pancake" {
				expected += 500000
			}
			storeBalance(t, f.seller.Id, expected)
			storeBalance(t, f.root.Id, 5000)
			require.EqualValues(t, 1, storeSelfPurchaseInventory(t, f.product.ID, "delivered"))
			require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "another-receipt"), ErrMerchantStoreConflict)
			var receipts int64
			require.NoError(t, DB.Model(&MerchantStorePaymentReceipt{}).Where("order_id = ?", o.ID).Count(&receipts).Error)
			require.EqualValues(t, 1, receipts)
			var fee MerchantStoreTransfer
			require.NoError(t, DB.Where("order_id = ? AND kind = ?", o.ID, "fee").First(&fee).Error)
			require.Zero(t, fee.FromUserID, "a held fee moves from escrow, not a second seller debit")
			require.Equal(t, f.root.Id, fee.ToUserID)
			require.Equal(t, 5000, fee.Quota)
		})
	}
}
