package model

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMerchantStorePurchaseLimitsPostgresConcurrentVariantsCannotBypassBuyerCap(t *testing.T) {
	db, name, observer, _ := merchantStorePGDB(t)
	f := merchantStorePGFixture(t, db, "balance")
	storePurchaseGateFour(t)
	variant, err := SaveMerchantStoreVariant(f.seller.Id, f.product.ID, "", MerchantStoreVariantInput{Name: "Another specification", PriceQuota: 500000, Template: "card-key", Enabled: true})
	require.NoError(t, err)
	_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one", "two", "three", "four"})
	require.NoError(t, err)
	_, err = AddMerchantStoreVariantStock(f.seller.Id, f.product.ID, variant.ID, []string{"five", "six", "seven", "eight"})
	require.NoError(t, err)
	require.NoError(t, db.Model(f.product).Updates(map[string]any{"status": "published", "max_quantity_per_buyer": 2, "max_quantity_per_order": 1}).Error)
	ops := make([]func() error, 8)
	for i := range ops {
		i := i
		ops[i] = func() error {
			in := f.checkout(fmt.Sprintf("limit-concurrent-%d", i), "balance")
			in.PickupCode, in.PickupEmail = "", ""
			in.VariantID = MerchantStoreDefaultVariantID(f.product.ID)
			if i%2 == 1 {
				in.VariantID = variant.ID
			}
			_, _, e := CreateMerchantStoreOrder(in)
			return e
		}
	}
	success := 0
	for _, e := range merchantStorePGContend(t, db, observer, name, ops) {
		if e == nil {
			success++
		} else {
			require.ErrorIs(t, e, ErrMerchantStorePurchaseLimit)
		}
	}
	require.Equal(t, 2, success)
	used, err := storeBuyerPurchaseUsage(db, f.product.ID, f.buyer.Id)
	require.NoError(t, err)
	require.EqualValues(t, 2, used)
	storeBalance(t, f.buyer.Id, 9000000)
	var stock int64
	require.NoError(t, db.Model(&MerchantStoreStock{}).Where("product_id = ? AND state = ?", f.product.ID, "delivered").Count(&stock).Error)
	require.EqualValues(t, 2, stock)
}
