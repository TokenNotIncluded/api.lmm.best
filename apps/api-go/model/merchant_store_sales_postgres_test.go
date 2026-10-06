package model

import (
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

// These cases share the explicit loopback-only disposable PostgreSQL harness.
// A SQLite fixture or a skipped DSN is not evidence of concurrent row locking.
func TestMerchantStorePostgresSalesLimit(t *testing.T) {
	t.Run("physical-stock-exceeds-cumulative-limit", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		var column struct {
			DataType      string
			IsNullable    string
			ColumnDefault *string
		}
		require.NoError(t, db.Raw("SELECT data_type, is_nullable, column_default FROM information_schema.columns WHERE table_schema = ? AND table_name = ? AND column_name = ?", name, "merchant_store_products", "sale_limit").Scan(&column).Error)
		require.Equal(t, "bigint", column.DataType)
		require.Equal(t, "YES", column.IsNullable)
		require.Nil(t, column.ColumnDefault)
		f := merchantStorePGFixture(t, db, "balance")
		items := make([]string, 100)
		for i := range items {
			items[i] = fmt.Sprintf("inventory-%d", i)
		}
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, items)
		require.NoError(t, err)
		require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(10)))
		ops := make([]func() error, 8)
		for i := range ops {
			i := i
			ops[i] = func() error {
				in := f.checkout(fmt.Sprintf("cap-%d", i), "balance")
				in.PickupCode, in.Quantity = "", 2
				_, _, err := CreateMerchantStoreOrder(in)
				return err
			}
		}
		success := 0
		for _, err := range merchantStorePGContend(t, db, observer, name, ops) {
			if err == nil {
				success++
			} else {
				require.ErrorIs(t, err, ErrMerchantStoreStock)
			}
		}
		require.Equal(t, 5, success)
		p := storeSalesProduct(t, f)
		require.EqualValues(t, 10, p.PaidQuantity)
		require.Zero(t, p.ReservedQuantity)
		require.EqualValues(t, 90, p.AvailableStock)
		require.Zero(t, p.SaleAvailable)
		storeBalance(t, f.buyer.Id, 5000000)
		storeBalance(t, f.seller.Id, 14950000)
		storeBalance(t, f.root.Id, 50000)
	})
	t.Run("late-payment-proof-prevents-fresh-checkout", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"one", "two"})
		require.NoError(t, err)
		require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, storeLimit(1)))
		in := f.checkout("closed-before-proof", "platform:waffo_pancake")
		in.PickupCode = ""
		o, _, err := CreateMerchantStoreOrder(in)
		require.NoError(t, err)
		require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
		require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
		ops := []func() error{func() error {
			return RecordMerchantStoreVerifiedPaymentIssue(o.ID, "late-proof", "settlement_unavailable")
		}}
		for i := 0; i < 7; i++ {
			buyer := marketTestUser(t, db, fmt.Sprintf("other-buyer-%d", i), 10000000, common.RoleCommonUser)
			require.NoError(t, AcceptMerchantStoreDisclaimer(buyer.Id, MerchantStoreDisclaimerVersion))
			input := f.checkout(fmt.Sprintf("after-proof-%d", i), "platform:waffo_pancake")
			input.BuyerID, input.PickupCode = buyer.Id, ""
			ops = append(ops, func() error {
				_, _, err := CreateMerchantStoreOrder(input)
				return err
			})
		}
		results := merchantStorePGContend(t, db, observer, name, ops)
		require.NoError(t, results[0])
		for _, err := range results[1:] {
			require.ErrorIs(t, err, ErrMerchantStoreStock)
		}
		p := storeSalesProduct(t, f)
		require.EqualValues(t, 1, p.PaidQuantity)
		require.Zero(t, p.ReservedQuantity)
		require.EqualValues(t, 2, p.AvailableStock)
		require.Zero(t, p.SaleAvailable)
		stored, err := GetMerchantStorePaymentOrder(o.ID)
		require.NoError(t, err)
		require.Positive(t, stored.VerifiedPaymentIssueAt)
		require.Equal(t, "late-proof", stored.ProviderTradeID)
		storeBalance(t, f.seller.Id, 10000000)
		storeBalance(t, f.root.Id, 0)
	})
}
