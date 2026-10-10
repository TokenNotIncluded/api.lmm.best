package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStoreStockQuotaCyclePreservesInventoryAndOrders(t *testing.T) {
	f := newStoreFixedTestFixture(t, "balance", "platform:waffo_pancake")
	var product MerchantStoreProduct
	require.NoError(t, DB.Where("seller_id = ? AND template = ?", f.seller.Id, "card-key").First(&product).Error)
	f.product = &product
	_, err := AddMerchantStoreStock(f.seller.Id, product.ID, []string{
		"QUOTA-CYCLE-THIRD", "QUOTA-CYCLE-FOURTH", "QUOTA-CYCLE-FIFTH",
		"QUOTA-CYCLE-SIXTH", "QUOTA-CYCLE-SEVENTH", "QUOTA-CYCLE-EIGHTH",
		"QUOTA-CYCLE-NINTH", "QUOTA-CYCLE-TENTH", "QUOTA-CYCLE-ELEVENTH",
	})
	require.NoError(t, err)
	paid, _, err := CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-paid", "balance"))
	require.NoError(t, err)
	require.Equal(t, "paid", paid.Status)
	pending, _, err := CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-reserved", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.Equal(t, "pending", pending.Status)

	stocks := func() []MerchantStoreStock {
		t.Helper()
		var rows []MerchantStoreStock
		require.NoError(t, DB.Where("product_id = ?", product.ID).Order("id").Find(&rows).Error)
		return rows
	}
	orders := func() []MerchantStoreOrder {
		t.Helper()
		var rows []MerchantStoreOrder
		require.NoError(t, DB.Where("product_id = ?", product.ID).Order("id").Find(&rows).Error)
		return rows
	}
	originalStocks, originalOrders := stocks(), orders()
	require.Len(t, originalStocks, 11)
	require.Len(t, originalOrders, 2)
	var available, reserved, sold int
	for _, row := range originalStocks {
		require.NotEmpty(t, row.Ciphertext)
		switch row.State {
		case "available":
			available++
		case "reserved":
			reserved++
		case "delivered":
			sold++
		}
	}
	require.Equal(t, 9, available)
	require.Equal(t, 1, reserved)
	require.Equal(t, 1, sold)
	// Detect writes as well as final row differences, including ciphertext and
	// reservation ownership. All data belongs to the isolated test database.
	storeFixedNoStockWrites(t, DB)
	five, zero := int64(5), int64(0)
	for _, step := range []struct {
		name        string
		remaining   *int64
		purchasable int64
	}{{"unlimited", nil, 9}, {"five", &five, 4}, {"zero", &zero, 0}, {"restored", nil, 9}} {
		t.Run(step.name, func(t *testing.T) {
			require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, product.ID, step.remaining))
			public, err := GetPublicMerchantStoreProduct(product.ID)
			require.NoError(t, err)
			require.False(t, public.UnlimitedSupply)
			require.EqualValues(t, 9, public.InventoryAvailable)
			require.EqualValues(t, 1, public.PaidQuantity)
			require.EqualValues(t, 1, public.ReservedQuantity)
			require.Equal(t, step.purchasable, public.SaleAvailable)
			require.Equal(t, step.remaining != nil && *step.remaining == 0, public.TradingPaused)
			if step.remaining == nil {
				require.Nil(t, public.SaleLimit)
			} else {
				require.NotNil(t, public.SaleLimit)
				require.Equal(t, int64(1)+*step.remaining, *public.SaleLimit)
			}
			if step.purchasable == 0 {
				_, _, err = CreateMerchantStoreOrder(storeFixedCheckout(t, f, "quota-cycle-blocked", "balance"))
				require.ErrorIs(t, err, ErrMerchantStoreStock)
				require.Contains(t, public.DisplayTags, "trading_paused")
			} else {
				require.Contains(t, public.DisplayTags, "in_stock")
			}
			require.Equal(t, originalStocks, stocks(), "quota edits preserve every stock row and its content/state")
			require.Equal(t, originalOrders, orders(), "paid and reserved obligations survive every quota edit")
		})
	}
}
