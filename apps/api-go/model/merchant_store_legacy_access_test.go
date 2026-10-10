package model

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreLegacyTestModeDoesNotPersistAccessProjection(t *testing.T) {
	for _, floor := range []string{"1", "2", "3", "4"} {
		t.Run("floor-"+floor, func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			var original MerchantStoreProduct
			require.NoError(t, DB.First(&original, "id = ?", f.product.ID).Error)
			require.Empty(t, original.Visibility, "inactive access columns must remain their legacy representation")
			storeWriterGateForTest(t, floor)
			enabled := true
			in := storeModeInput(f.product, &enabled)
			in.Title = "Private legacy floor " + floor
			var err error
			f.product, err = SaveMerchantStoreProduct(f.seller.Id, "", in)
			require.NoError(t, err)
			_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"LEGACY-PRIVATE-CARD"})
			require.NoError(t, err)
			var stored MerchantStoreProduct
			require.NoError(t, DB.First(&stored, "id = ?", f.product.ID).Error)
			require.True(t, stored.TestMode)
			require.Empty(t, stored.Visibility)
			// Match MySQL's default changed-rows result for an identical draft
			// update. GORM must not then reinsert/upsert omitted access columns.
			const callback = "store-test-zero-changed-product"
			require.NoError(t, DB.Callback().Update().After("gorm:update").Register(callback, func(tx *gorm.DB) {
				if tx.Statement.Schema != nil && tx.Statement.Schema.Name == "MerchantStoreProduct" && tx.Error == nil {
					tx.RowsAffected = 0
				}
			}))
			_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, storeModeInput(f.product, nil))
			require.NoError(t, DB.Callback().Update().Remove(callback))
			require.NoError(t, err)
			stored = MerchantStoreProduct{}
			require.NoError(t, DB.First(&stored, "id = ?", f.product.ID).Error)
			require.True(t, stored.TestMode)
			require.Empty(t, stored.Visibility, "a zero-change update must preserve the legacy schema representation")
			for _, actor := range []int{f.buyer.Id, f.root.Id} {
				_, err = GetMerchantStoreProduct(actor, f.product.ID)
				require.ErrorIs(t, err, ErrMerchantStoreDenied)
				_, _, err = CreateMerchantStoreOrder(f.checkout("other-"+strconv.Itoa(actor), "balance"))
				require.ErrorIs(t, err, ErrMerchantStoreDenied)
			}
			checkout := storeSellerCheckout(t, f, "owner-private", "balance")
			order, created, err := CreateMerchantStoreOrder(checkout)
			require.NoError(t, err)
			require.True(t, created)
			require.Equal(t, "paid", order.Status)
			replay, created, err := CreateMerchantStoreOrder(checkout)
			require.NoError(t, err)
			require.False(t, created)
			require.Equal(t, order.ID, replay.ID)
			storeBalance(t, f.seller.Id, 9995000)
		})
	}
}
