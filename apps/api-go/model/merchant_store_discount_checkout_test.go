package model

import (
	"errors"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func storeCheckoutPromotion(t *testing.T, f storeFixture, bps int, uses *int64) *MerchantStoreDiscountCode {
	t.Helper()
	storePurchaseGateFour(t)
	code, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: &bps, MaxUses: uses})
	require.NoError(t, err)
	return code
}

func TestMerchantStoreDiscountCheckoutCapThreeIsFrozen(t *testing.T) {
	f := newStoreFixture(t, "balance")
	storeWriterGateForTest(t, "3")
	bps := 10000
	_, err := SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: &bps})
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	_, created, err := CreateMerchantStoreOrder(f.checkout("plain", "balance"))
	require.NoError(t, err)
	require.True(t, created)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("max_quantity_per_order", 1).Error)
	_, err = QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, "", "", 1)
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen, "configured limits acquire their gate before the minimum-price config lock")
}

func TestMerchantStoreDiscountFreeDeliveryHasNoGatewayOrWalletMovement(t *testing.T) {
	f := newStoreFixture(t, "balance")
	code := storeCheckoutPromotion(t, f, 10000, nil)
	require.NoError(t, SetMerchantStorePaymentCategories(f.seller.Id, MerchantStorePaymentCategories{}))
	require.NoError(t, DB.Where("seller_id = ?", f.seller.Id).Delete(&MerchantStoreGateway{}).Error)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, public.TradingPaused, "existing paid-method aggregates intentionally have no capacity")
	require.Zero(t, public.SaleAvailable)
	quote, err := QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, code.Code, "", 1)
	require.NoError(t, err)
	require.True(t, quote.CheckoutAllowed)
	require.True(t, quote.Free)
	require.Equal(t, 2, quote.MaxQuantity)
	input := f.checkout("free", "free")
	input.PromotionCode = code.Code
	order, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, "paid", order.Status)
	require.Positive(t, order.PaidAt)
	require.Equal(t, "CREDIT", order.Currency)
	require.Equal(t, 500000, order.OriginalPriceQuota)
	require.Equal(t, 500000, order.DiscountQuota)
	require.Zero(t, order.PriceQuota)
	require.Zero(t, order.FeeQuota)
	require.False(t, order.FeeHeld)
	require.False(t, order.PaymentIssued)
	require.Empty(t, order.GatewaySnapshot)
	require.Empty(t, order.ProviderSessionID)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	var transfers, delivered int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", order.ID).Count(&transfers).Error)
	require.Zero(t, transfers)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("order_id = ? AND state = ?", order.ID, "delivered").Count(&delivered).Error)
	require.EqualValues(t, 1, delivered)
}

func TestMerchantStoreDiscountPartialQuoteUsesNetFeeAndQuantityCapacity(t *testing.T) {
	f := newStoreFixture(t, "balance")
	code := storeCheckoutPromotion(t, f, 5000, nil)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 3000).Error)
	public, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.NoError(t, err)
	require.True(t, public.TradingPaused)
	require.Zero(t, public.SaleAvailable, "the original unit fee exceeds seller funds")
	quote, err := QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, code.Code, "", 1)
	require.NoError(t, err)
	require.True(t, quote.CheckoutAllowed)
	require.False(t, quote.Free)
	require.Equal(t, 250000, quote.PriceQuota)
	require.Equal(t, 1, quote.MaxQuantity, "two discounted units still exceed seller fee funds")
	_, err = QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, code.Code, "", 2)
	require.ErrorIs(t, err, ErrMerchantStoreBalance)
	input := f.checkout("partial-fee", "balance")
	input.PromotionCode = code.Code
	order, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	require.Equal(t, 250000, order.PriceQuota)
	require.Equal(t, 2500, order.FeeQuota)
	storeBalance(t, f.buyer.Id, 9750000)
	storeBalance(t, f.seller.Id, 250500)
	storeBalance(t, f.root.Id, 2500)
}

func TestMerchantStoreDiscountDeletedProductAllowsOnlyOwnerCleanup(t *testing.T) {
	f := newStoreFixture(t, "balance")
	uses := int64(1)
	used := storeCheckoutPromotion(t, f, 10000, &uses)
	active := storeCheckoutPromotion(t, f, 10000, nil)
	input := f.checkout("retained-promotion-history", "free")
	input.PromotionCode = used.Code
	order, _, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
	var beforeOrder, afterOrder map[string]any
	var beforeStock, afterStock []map[string]any
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", order.ID).Take(&beforeOrder).Error)
	require.NoError(t, DB.Table("merchant_store_stocks").Where("product_id = ?", f.product.ID).Order("id").Find(&beforeStock).Error)
	for _, actor := range []int{f.seller.Id, f.root.Id} {
		rows, total, err := ListMerchantStoreDiscountCodes(actor, f.product.ID, 0, 100)
		require.NoError(t, err)
		require.Len(t, rows, 2)
		require.EqualValues(t, 2, total)
	}
	_, _, err = ListMerchantStoreDiscountCodes(f.buyer.Id, f.product.ID, 0, 100)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = BatchMerchantStoreDiscountCodes(f.buyer.Id, f.product.ID, []string{active.ID}, "delete")
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = CleanupMerchantStoreDiscountCodes(f.buyer.Id, f.product.ID, 100)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	for _, action := range []string{"pause", "resume", "revoke"} {
		_, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{active.ID}, action)
		require.Error(t, err)
	}
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, "", MerchantStoreDiscountCodeInput{DiscountBPS: storeDiscountInt(10000)})
	require.Error(t, err)
	_, err = ResolveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, active.Code)
	require.Error(t, err)
	_, err = QuoteMerchantStoreDiscountCode(f.seller.Id, f.product.ID, active.Code, "", 1)
	require.Error(t, err)
	deleted, err := CleanupMerchantStoreDiscountCodes(f.root.Id, f.product.ID, 100)
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	deleted, err = BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{active.ID}, "delete")
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	rows, total, err := ListMerchantStoreDiscountCodes(f.root.Id, f.product.ID, 0, 100)
	require.NoError(t, err)
	require.Empty(t, rows)
	require.Zero(t, total)
	require.NoError(t, DB.Table("merchant_store_orders").Where("id = ?", order.ID).Take(&afterOrder).Error)
	require.NoError(t, DB.Table("merchant_store_stocks").Where("product_id = ?", f.product.ID).Order("id").Find(&afterStock).Error)
	require.Equal(t, beforeOrder, afterOrder)
	require.Equal(t, beforeStock, afterStock)
}

func TestMerchantStoreDiscountCheckoutRequiresExplicitFreeAndKeepsGuards(t *testing.T) {
	for _, test := range []string{"no-code", "partial-code", "paid-method", "disclaimer", "pickup-code", "quantity-stock", "sale-limit", "paused-product", "other-buyer-test-mode"} {
		t.Run(test, func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			bps := 10000
			if test == "partial-code" {
				bps = 9999
			}
			code := storeCheckoutPromotion(t, f, bps, nil)
			input := f.checkout(test, "free")
			input.PromotionCode = code.Code
			expected := ErrMerchantStoreInput
			switch test {
			case "no-code":
				input.PromotionCode = ""
			case "paid-method":
				input.PaymentMethod = "balance"
			case "disclaimer":
				require.NoError(t, DB.Where("user_id = ?", f.buyer.Id).Delete(&MerchantStoreDisclaimerAcceptance{}).Error)
				expected = ErrMerchantStoreDisclaimer
			case "pickup-code":
				input.PickupCode = ""
			case "quantity-stock":
				input.Quantity = 3
				expected = ErrMerchantStoreStock
			case "sale-limit":
				limit := int64(0)
				require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &limit))
				expected = ErrMerchantStoreStock
			case "paused-product":
				require.NoError(t, SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true))
				expected = ErrMerchantStoreUnavailable
			case "other-buyer-test-mode":
				require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("test_mode", true).Error)
				expected = ErrMerchantStoreDenied
			}
			_, created, err := CreateMerchantStoreOrder(input)
			require.ErrorIs(t, err, expected)
			require.False(t, created)
			var orders, stock int64
			require.NoError(t, DB.Model(&MerchantStoreOrder{}).Count(&orders).Error)
			require.Zero(t, orders)
			require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "available").Count(&stock).Error)
			require.EqualValues(t, 2, stock)
		})
	}
}

func TestMerchantStoreDiscountUseLimitReplayAndDeleteRetainDeliveredStock(t *testing.T) {
	f := newStoreFixture(t, "balance")
	maxUses := int64(1)
	code := storeCheckoutPromotion(t, f, 10000, &maxUses)
	input := f.checkout("gift", "free")
	input.PromotionCode = code.Code
	order, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.True(t, created)
	replay, created, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, order.ID, replay.ID)
	next := input
	next.RequestKey = "gift-next"
	_, _, err = CreateMerchantStoreOrder(next)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountLimit)
	token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, order.ID)
	require.NoError(t, err)
	claim, err := ClaimMerchantStoreOrder(token, input.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	deleted, err := BatchMerchantStoreDiscountCodes(f.seller.Id, f.product.ID, []string{code.ID}, "delete")
	require.NoError(t, err)
	require.EqualValues(t, 1, deleted)
	replay, created, err = CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, created)
	require.Equal(t, code.Code, replay.PromotionCode)
	require.Equal(t, 10000, replay.DiscountBPS)
	require.Zero(t, replay.PriceQuota)
	_, _, err = CreateMerchantStoreOrder(next)
	require.ErrorIs(t, err, ErrMerchantStoreDiscountUnavailable)
	otherCode := storeCheckoutPromotion(t, f, 10000, nil)
	next.PromotionCode = otherCode.Code
	second, _, err := CreateMerchantStoreOrder(next)
	require.NoError(t, err)
	secondToken, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, second.ID)
	require.NoError(t, err)
	secondClaim, err := ClaimMerchantStoreOrder(secondToken, input.PickupCode, f.buyer.Id)
	require.NoError(t, err)
	require.Equal(t, []string{"CARD-SECRET-SECOND"}, secondClaim.Items)
	// Changing the offer cannot reuse an already-paid idempotency key.
	input.PromotionCode = otherCode.Code
	_, _, err = CreateMerchantStoreOrder(input)
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
}

func TestMerchantStoreDiscountPaidSnapshotRoundsUpOnceAndFreezesNetPrincipal(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("price_quota", 500001).Error)
	require.NoError(t, DB.Model(&MerchantStoreVariant{}).Where("product_id = ?", f.product.ID).Update("price_quota", 500001).Error)
	code := storeCheckoutPromotion(t, f, 3333, nil)
	input := f.checkout("discount", "balance")
	input.PromotionCode = code.Code
	order, _, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.Equal(t, 500001, order.UnitPriceQuota)
	require.Equal(t, 500001, order.OriginalPriceQuota)
	require.Equal(t, 333351, order.PriceQuota)
	require.Equal(t, 166650, order.DiscountQuota)
	storeBalance(t, f.buyer.Id, 10000000-333351)
	newBPS := 10000
	_, err = SaveMerchantStoreDiscountCode(f.seller.Id, f.product.ID, code.ID, MerchantStoreDiscountCodeInput{DiscountBPS: &newBPS, Status: "paused"})
	require.NoError(t, err)
	replay, made, err := CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	require.False(t, made)
	require.Equal(t, 333351, replay.PriceQuota)
	require.Equal(t, 3333, replay.DiscountBPS)
}

func TestMerchantStoreDiscountFreeLastUseSerializesWithInventory(t *testing.T) {
	f := newStoreFixture(t, "balance")
	maxUses := int64(1)
	code := storeCheckoutPromotion(t, f, 10000, &maxUses)
	results := make(chan error, 2)
	var workers sync.WaitGroup
	for _, key := range []string{"concurrent-a", "concurrent-b"} {
		workers.Add(1)
		go func(key string) {
			defer workers.Done()
			input := f.checkout(key, "free")
			input.PromotionCode = code.Code
			_, _, err := CreateMerchantStoreOrder(input)
			results <- err
		}(key)
	}
	workers.Wait()
	close(results)
	paid, refused := 0, 0
	for err := range results {
		if err == nil {
			paid++
		} else if errors.Is(err, ErrMerchantStoreDiscountLimit) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	require.Equal(t, 1, paid)
	require.Equal(t, 1, refused)
	var delivered, available int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "delivered").Count(&delivered).Error)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("state = ?", "available").Count(&available).Error)
	require.EqualValues(t, 1, delivered)
	require.EqualValues(t, 1, available)
}

func TestMerchantStoreDiscountFreeQuoteRequiresBuyerQuantityEligibility(t *testing.T) {
	f := newStoreFixture(t, "balance")
	code := storeCheckoutPromotion(t, f, 10000, nil)
	limit := int64(1)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("max_quantity_per_buyer", limit).Error)
	quote, err := QuoteMerchantStoreDiscountCode(0, f.product.ID, code.Code, "", 1)
	require.NoError(t, err)
	require.True(t, quote.Free)
	require.False(t, quote.CheckoutAllowed, "anonymous price previews cannot authorize checkout")
	quote, err = QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, code.Code, "", 1)
	require.NoError(t, err)
	require.True(t, quote.CheckoutAllowed)
	input := f.checkout("limited-free", "free")
	input.PromotionCode = code.Code
	_, _, err = CreateMerchantStoreOrder(input)
	require.NoError(t, err)
	_, err = QuoteMerchantStoreDiscountCode(f.buyer.Id, f.product.ID, code.Code, "", 1)
	require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
	input.RequestKey = "limited-free-next"
	_, _, err = CreateMerchantStoreOrder(input)
	require.ErrorIs(t, err, ErrMerchantStorePurchaseLimit)
}
