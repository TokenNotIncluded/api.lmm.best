package model

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// The shared harness accepts only an explicit loopback DSN and creates a unique
// disposable schema. Skipped cases are not PostgreSQL locking evidence.
func TestMerchantStorePostgresRemainingQuota(t *testing.T) {
	for _, paymentFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "quota-locks-before-settlement", true: "settlement-locks-before-quota"}[paymentFirst], func(t *testing.T) {
			db, name, observer, _ := merchantStorePGDB(t)
			f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
			_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"FIRST-POOL-ONE", "FIRST-POOL-TWO", "FIRST-POOL-THREE", "FIRST-POOL-FOUR", "FIRST-POOL-FIVE", "FIRST-POOL-SIX", "FIRST-POOL-SEVEN", "FIRST-POOL-EIGHT", "FIRST-POOL-NINE", "FIRST-POOL-TEN"})
			require.NoError(t, err)
			require.NoError(t, SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(10)))
			input := f.checkout("existing-two", "platform:waffo_pancake")
			input.PickupCode, input.PickupEmail, input.Quantity = "", "", 2
			order, _, err := CreateMerchantStoreOrder(input)
			require.NoError(t, err)
			require.NoError(t, BindMerchantStorePaymentQuote(order.ID, 200, "USD", "1"))
			payment := func() error { return CompleteMerchantStorePayment(order.ID, "pg-quota-settlement") }
			quota := func() error { return SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(5)) }
			operations := []func() error{quota, payment}
			if paymentFirst {
				operations = []func() error{payment, quota}
			}
			for _, err := range merchantStorePGContend(t, db, observer, name, operations) {
				require.NoError(t, err)
			}
			product := storeSalesProduct(t, f)
			require.EqualValues(t, 2, product.PaidQuantity)
			require.Zero(t, product.ReservedQuantity)
			require.EqualValues(t, 8, product.InventoryTotal)
			if paymentFirst {
				require.EqualValues(t, 7, *product.SaleLimit)
				require.EqualValues(t, 5, product.SaleAvailable)
			} else {
				require.EqualValues(t, 5, *product.SaleLimit)
				require.EqualValues(t, 3, product.SaleAvailable)
			}
			stored, err := GetMerchantStoreOrder(f.buyer.Id, order.ID)
			require.NoError(t, err)
			require.Equal(t, "paid", stored.Status)
			storeBalance(t, f.buyer.Id, f.buyer.Quota)
			storeBalance(t, f.seller.Id, f.seller.Quota+order.PriceQuota-order.FeeQuota)
			storeBalance(t, f.root.Id, order.FeeQuota)
		})
	}
	for _, checkoutFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "quota-zero-before-checkout", true: "checkout-before-quota-zero"}[checkoutFirst], func(t *testing.T) {
			db, name, observer, _ := merchantStorePGDB(t)
			f := merchantStorePGFixture(t, db, "balance")
			_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"ONLY-AVAILABLE-CARD"})
			require.NoError(t, err)
			var order *MerchantStoreOrder
			checkout := func() error {
				input := f.checkout("zero-quota-race", "balance")
				input.PickupCode, input.PickupEmail = "", ""
				var err error
				order, _, err = CreateMerchantStoreOrder(input)
				return err
			}
			quota := func() error { return SetMerchantStoreProductRemainingQuota(f.seller.Id, f.product.ID, storeLimit(0)) }
			operations := []func() error{quota, checkout}
			if checkoutFirst {
				operations = []func() error{checkout, quota}
			}
			errors := merchantStorePGContend(t, db, observer, name, operations)
			require.NoError(t, errors[0])
			product := storeSalesProduct(t, f)
			require.Zero(t, product.SaleAvailable)
			if checkoutFirst {
				require.NoError(t, errors[1])
				require.EqualValues(t, 1, product.PaidQuantity)
				require.EqualValues(t, 1, *product.SaleLimit)
				storeBalance(t, f.buyer.Id, f.buyer.Quota-order.PriceQuota)
				storeBalance(t, f.seller.Id, f.seller.Quota+order.PriceQuota-order.FeeQuota)
				storeBalance(t, f.root.Id, order.FeeQuota)
			} else {
				require.ErrorIs(t, errors[1], ErrMerchantStoreStock)
				require.Zero(t, product.PaidQuantity)
				require.EqualValues(t, 1, product.InventoryTotal)
				storeBalance(t, f.buyer.Id, f.buyer.Quota)
				storeBalance(t, f.seller.Id, f.seller.Quota)
				storeBalance(t, f.root.Id, f.root.Quota)
			}
		})
	}
}
