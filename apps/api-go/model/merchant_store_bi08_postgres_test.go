package model

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

// These tests use main's existing loopback-only PostgreSQL harness and real
// model transactions. No payment adapter, email worker, or HTTP server starts.
// Run with -race -count=1 and an explicitly disposable local database.
func TestMerchantStoreBI08Postgres(t *testing.T) {
	t.Run("multiple-buyers-last-unit-and-seller-isolation", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		f.product = bi08ProtectedProduct(t, f, "balance")
		other := f
		other.seller = marketTestUser(t, db, "bi08-other-seller", 10000000, common.RoleCommonUser)
		require.NoError(t, SetMerchantStorePaymentCategories(other.seller.Id, MerchantStorePaymentCategories{PlatformEnabled: true, ExternalEnabled: true}))
		_, err := SaveMerchantStoreGateway(other.seller.Id, "balance", true, "")
		require.NoError(t, err)
		other.product = bi08ProtectedProduct(t, other, "balance")
		_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"BI08-FIRST-STORE"})
		require.NoError(t, err)
		_, err = AddMerchantStoreStock(other.seller.Id, other.product.ID, []string{"BI08-OTHER-STORE"})
		require.NoError(t, err)
		buyers := []User{f.buyer, bi08Buyer(t, db, "bi08-buyer-2"), bi08Buyer(t, db, "bi08-buyer-3")}
		orders := make([]*MerchantStoreOrder, len(buyers))
		created := make([]bool, len(buyers))
		inputs := make([]MerchantStoreCheckoutInput, len(buyers))
		ops := make([]func() error, len(buyers))
		for i, buyer := range buyers {
			inputs[i] = bi08Checkout(f, fmt.Sprintf("last-unit-%d", i), "balance")
			inputs[i].BuyerID = buyer.Id
			ops[i] = func() error {
				var err error
				orders[i], created[i], err = CreateMerchantStoreOrder(inputs[i])
				return err
			}
		}
		bi08State(t, db, "before competing purchases")
		winner := -1
		for i, err := range merchantStorePGContend(t, db, observer, name, ops) {
			if err == nil {
				require.Equal(t, -1, winner, "only one buyer can own the last unit")
				winner = i
				require.True(t, created[i])
			} else {
				require.ErrorIs(t, err, ErrMerchantStoreStock)
				require.False(t, created[i])
			}
		}
		require.NotEqual(t, -1, winner)
		o := orders[winner]
		require.Equal(t, "paid", o.Status)
		require.Equal(t, f.seller.Id, o.SellerID)
		require.Equal(t, f.product.ID, o.ProductID)
		require.Equal(t, MerchantStoreDefaultVariantID(f.product.ID), o.VariantID)
		require.Equal(t, 500000, o.UnitPriceQuota)
		for i, buyer := range buyers {
			balance := 10000000
			if i == winner {
				balance -= 500000
			}
			storeBalance(t, buyer.Id, balance)
		}
		storeBalance(t, f.seller.Id, 10495000)
		storeBalance(t, other.seller.Id, 10000000)
		storeBalance(t, f.root.Id, 5000)
		var stock []MerchantStoreStock
		require.NoError(t, db.Where("product_id = ?", f.product.ID).Find(&stock).Error)
		require.Len(t, stock, 1)
		require.Equal(t, "delivered", stock[0].State)
		require.Equal(t, o.ID, stock[0].OrderID)
		bi08Count(t, db, &MerchantStoreOrder{}, "product_id = ?", f.product.ID, 1)
		bi08Count(t, db, &MerchantStoreTransfer{}, "order_id = ?", o.ID, 2)
		bi08Count(t, db, &MerchantStoreStock{}, "product_id = ? AND state = 'available'", other.product.ID, 1)
		before := bi08State(t, db, "after one purchase")
		// The caller lost the successful checkout response and retries it.
		replay, made, err := CreateMerchantStoreOrder(inputs[winner])
		require.NoError(t, err)
		require.False(t, made)
		require.Equal(t, o.ID, replay.ID)
		token, err := GetMerchantStoreOrderPickupToken(buyers[winner].Id, o.ID)
		require.NoError(t, err)
		_, err = GetMerchantStoreOrder(other.seller.Id, o.ID)
		require.Error(t, err)
		_, err = GetMerchantStoreOrder(buyers[(winner+1)%len(buyers)].Id, o.ID)
		require.Error(t, err)
		_, err = GetMerchantStoreOrderPickupToken(f.seller.Id, o.ID)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		_, err = AddMerchantStoreStock(other.seller.Id, f.product.ID, []string{"unauthorized"})
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		zero := int64(0)
		require.ErrorIs(t, SetMerchantStoreProductSaleLimit(other.seller.Id, f.product.ID, &zero), ErrMerchantStoreDenied)
		_, err = SaveMerchantStoreProduct(other.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "unauthorized", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", buyers[(winner+1)%len(buyers)].Id)
		require.ErrorIs(t, err, ErrMerchantStorePickupAccount)
		_, err = ClaimMerchantStoreOrder(token, "wrong-code", buyers[winner].Id)
		require.ErrorIs(t, err, ErrMerchantStorePickupCode)
		require.Equal(t, before, bi08State(t, db, "after replay and denied cross-account operations"))
		claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", buyers[winner].Id)
		require.NoError(t, err)
		require.Equal(t, []string{"BI08-FIRST-STORE"}, claim.Items)
		claimed := bi08State(t, db, "after first authorized claim")
		again, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", buyers[winner].Id)
		require.NoError(t, err)
		require.Equal(t, claim, again)
		require.Equal(t, claimed, bi08State(t, db, "after repeated claim"))
	})

	t.Run("settlement-rollback-lost-receipt-and-delivery-recovery", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		f.product = bi08ProtectedProduct(t, f, "platform:waffo_pancake")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"BI08-RECOVERABLE"})
		require.NoError(t, err)
		o, made, err := CreateMerchantStoreOrder(bi08Checkout(f, "recover", "platform:waffo_pancake"))
		require.NoError(t, err)
		require.True(t, made)
		require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
		require.NoError(t, BindMerchantStorePaymentContext(o.ID, "v1:bi08-test-only-context"))
		before := bi08State(t, db, "before verified settlement")
		injected := errors.New("BI08 injected final payment-event write failure")
		func() {
			const callback = "bi08:reject-final-payment-event"
			require.NoError(t, db.Callback().Create().Before("gorm:create").Register(callback, func(tx *gorm.DB) {
				event, ok := tx.Statement.Dest.(*MerchantStoreEvent)
				if ok && event.ObjectID == o.ID && event.Action == "payment_verified" {
					tx.AddError(injected)
				}
			}))
			defer func() { require.NoError(t, db.Callback().Create().Remove(callback)) }()
			err := CompleteMerchantStorePayment(o.ID, "bi08-provider-receipt")
			require.ErrorIs(t, err, injected)
		}()
		require.Equal(t, before, bi08State(t, db, "after injected settlement failure"), "receipt, wallets, stock, order, and event writes roll back together")
		require.NoError(t, CompleteMerchantStorePayment(o.ID, "bi08-provider-receipt"))
		paid := bi08State(t, db, "after successful settlement retry")
		// Simulate the adapter losing the successful settlement acknowledgement.
		require.NoError(t, CompleteMerchantStorePayment(o.ID, "bi08-provider-receipt"))
		require.Equal(t, paid, bi08State(t, db, "after lost-ack replay"))
		require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "bi08-different-receipt"), ErrMerchantStoreConflict)
		storeBalance(t, f.buyer.Id, 10000000)
		storeBalance(t, f.seller.Id, 10495000)
		storeBalance(t, f.root.Id, 5000)
		bi08Count(t, db, &MerchantStorePaymentReceipt{}, "order_id = ?", o.ID, 1)
		bi08Count(t, db, &MerchantStoreTransfer{}, "order_id = ?", o.ID, 3)
		token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
		require.NoError(t, err)
		var stock MerchantStoreStock
		require.NoError(t, db.First(&stock, "order_id = ?", o.ID).Error)
		// Fault injection is restricted to the test-owned stock row. No key or
		// service setting is changed, and no replacement delivery is created.
		originalCiphertext := stock.Ciphertext
		require.NoError(t, db.Model(&stock).Update("ciphertext", "bi08-invalid-ciphertext").Error)
		broken := bi08State(t, db, "after test-only ciphertext fault injection")
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.Error(t, err)
		require.Equal(t, broken, bi08State(t, db, "after paid delivery decryption failure"))
		require.NoError(t, db.Model(&stock).Update("ciphertext", originalCiphertext).Error)
		require.Equal(t, paid, bi08State(t, db, "after repair before claiming"))
		claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		require.Equal(t, []string{"BI08-RECOVERABLE"}, claim.Items)
		require.Equal(t, []string{stock.ID}, claim.ItemStockIDs)
		claimed := bi08State(t, db, "after repaired delivery")
		again, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		require.Equal(t, claim, again)
		require.Equal(t, claimed, bi08State(t, db, "after delivery-response replay"))
	})

	t.Run("unpaid-cancelled-expired-and-sales-quota-boundaries", func(t *testing.T) {
		db, _, _, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
		f.product = bi08ProtectedProduct(t, f, "platform:waffo_pancake")
		otherBuyer := bi08Buyer(t, db, "bi08-quota-buyer")
		_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"BI08-ONE", "BI08-TWO"})
		require.NoError(t, err)
		limit := int64(1)
		require.NoError(t, SetMerchantStoreProductSaleLimit(f.seller.Id, f.product.ID, &limit))
		o, _, err := CreateMerchantStoreOrder(bi08Checkout(f, "unpaid", "platform:waffo_pancake"))
		require.NoError(t, err)
		// Test knowledge of the token is not payment authorization.
		token, err := storeDecrypt("pickup", o.ID, o.PickupTokenCiphertext)
		require.NoError(t, err)
		_, err = GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		public, err := GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.EqualValues(t, 1, public.AvailableStock)
		require.Zero(t, public.SaleAvailable, "available inventory is not available sales quota")
		other := bi08Checkout(f, "other-buyer", "platform:waffo_pancake")
		other.BuyerID = otherBuyer.Id
		before := bi08State(t, db, "one unpaid reservation fills sales quota")
		_, _, err = CreateMerchantStoreOrder(other)
		require.ErrorIs(t, err, ErrMerchantStoreStock)
		require.Equal(t, before, bi08State(t, db, "quota rejection has no side effects"))
		require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
		require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		require.ErrorIs(t, CompleteMerchantStorePayment(o.ID, "bi08-cancelled-receipt"), ErrMerchantStoreConflict)
		bi08Count(t, db, &MerchantStoreStock{}, "product_id = ? AND state = 'available'", f.product.ID, 2)
		expired, _, err := CreateMerchantStoreOrder(bi08Checkout(f, "expires-unpaid", "platform:waffo_pancake"))
		require.NoError(t, err)
		expiredToken, err := storeDecrypt("pickup", expired.ID, expired.PickupTokenCiphertext)
		require.NoError(t, err)
		require.NoError(t, db.Model(expired).Update("expires_at", common.GetTimestamp()-1).Error)
		count, err := ExpireMerchantStoreOrders(30)
		require.NoError(t, err)
		require.Equal(t, 1, count)
		_, err = ClaimMerchantStoreOrder(expiredToken, "safe-pickup-code", f.buyer.Id)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		bi08Count(t, db, &MerchantStoreStock{}, "product_id = ? AND state = 'available'", f.product.ID, 2)
		storeBalance(t, f.seller.Id, 10000000)
		paidOrder, _, err := CreateMerchantStoreOrder(bi08Checkout(f, "paid", "platform:waffo_pancake"))
		require.NoError(t, err)
		require.NoError(t, BindMerchantStorePaymentQuote(paidOrder.ID, 100, "USD", "1"))
		require.NoError(t, CompleteMerchantStorePayment(paidOrder.ID, "bi08-paid-quota"))
		// ExpiresAt is checkout expiry, not an invented one-use/expiry policy for
		// an already paid entitlement. Preserve subsequent legitimate reads.
		require.NoError(t, db.Model(paidOrder).Update("expires_at", common.GetTimestamp()-1).Error)
		paidToken, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, paidOrder.ID)
		require.NoError(t, err)
		_, err = ClaimMerchantStoreOrder(paidToken, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		_, err = AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"BI08-RESTOCK"})
		require.NoError(t, err)
		public, err = GetPublicMerchantStoreProduct(f.product.ID)
		require.NoError(t, err)
		require.EqualValues(t, 2, public.AvailableStock)
		require.EqualValues(t, 1, public.PaidQuantity)
		require.Zero(t, public.SaleAvailable, "restocking does not reset lifetime sales quota")
		_, _, err = CreateMerchantStoreOrder(other)
		require.ErrorIs(t, err, ErrMerchantStoreStock)
		bi08State(t, db, "cancelled, expired, and paid orders with restocked inventory")
	})

	t.Run("fixed-content-repeat-read-partial-and-full-refund", func(t *testing.T) {
		db, name, observer, _ := merchantStorePGDB(t)
		f := merchantStorePGFixture(t, db, "balance")
		storeActivateFixedTest(t, db)
		f = storeCreateFixedTestProduct(t, f, "balance")
		storeFixedNoStockWrites(t, db)
		in := storeFixedCheckout(t, f, "fixed-bi08", "balance")
		in.PickupEmail, in.Quantity = "", 2
		bi08State(t, db, "before fixed-content purchase")
		o, _, err := CreateMerchantStoreOrder(in)
		require.NoError(t, err)
		token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
		require.NoError(t, err)
		claims := make([]*MerchantStoreClaim, 2)
		ops := make([]func() error, len(claims))
		for i := range ops {
			ops[i] = func() error {
				var err error
				claims[i], err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
				return err
			}
		}
		for _, err := range merchantStorePGContend(t, db, observer, name, ops) {
			require.NoError(t, err)
		}
		require.Equal(t, claims[0], claims[1])
		require.Equal(t, storeFixedTestContent, claims[0].FixedContent)
		require.Equal(t, 2, claims[0].Quantity)
		bi08Count(t, db, &MerchantStoreOrderFixedDelivery{}, "order_id = ?", o.ID, 1)
		claimed := bi08State(t, db, "after concurrent reads of one fixed delivery")
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		require.Equal(t, claimed, bi08State(t, db, "after fixed-content response replay"))
		partial, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "bi08-partial", Reason: "test quantity return", Mode: "quantity", Quantity: 1})
		require.NoError(t, err)
		refundApprove(t, f.seller.Id, o, partial)
		remaining, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.NoError(t, err)
		require.Equal(t, 1, remaining.Quantity)
		require.Equal(t, storeFixedTestContent, remaining.FixedContent)
		bi08State(t, db, "after partial refund; remaining entitlement is readable")
		full, err := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("bi08-remainder"))
		require.NoError(t, err)
		refundApprove(t, f.seller.Id, o, full)
		before := bi08State(t, db, "after full refund")
		_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		_, err = GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
		require.ErrorIs(t, err, ErrMerchantStoreDenied)
		require.Equal(t, before, bi08State(t, db, "refunded content denied without a new delivery"))
		storeBalance(t, f.buyer.Id, 10000000)
		storeBalance(t, f.seller.Id, 9990000)
		storeBalance(t, f.root.Id, 10000)
		storeFixedStockless(t, f.product.ID)
	})
}

func bi08ProtectedProduct(t *testing.T, f storeFixture, methods ...string) *MerchantStoreProduct {
	t.Helper()
	p, err := SaveMerchantStoreProduct(f.seller.Id, "", MerchantStoreProductInput{Title: "BI08 protected delivery", PriceQuota: 500000, PaymentMethods: methods, PickupLoginRequired: true, PickupCodeRequired: true})
	require.NoError(t, err)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, "isolated BI08 test"))
	return p
}

func bi08Buyer(t *testing.T, db *gorm.DB, name string) User {
	t.Helper()
	buyer := marketTestUser(t, db, name, 10000000, common.RoleCommonUser)
	require.NoError(t, AcceptMerchantStoreDisclaimer(buyer.Id, MerchantStoreDisclaimerVersion))
	return buyer
}

func bi08Checkout(f storeFixture, key, method string) MerchantStoreCheckoutInput {
	in := f.checkout(key, method)
	in.PickupEmail = "" // No delivery email is enqueued or sent by these cases.
	return in
}

func bi08Count(t *testing.T, db *gorm.DB, model any, where string, value any, expected int64) {
	t.Helper()
	var count int64
	require.NoError(t, db.Model(model).Where(where, value).Count(&count).Error)
	require.Equal(t, expected, count)
}

// Log only synthetic, test-owned accounting and fulfillment facts. Never log a
// DSN, token, pickup code, gateway snapshot, email address, or delivery content.
func bi08State(t *testing.T, db *gorm.DB, label string) string {
	t.Helper()
	state := map[string]any{}
	for _, table := range []struct{ name, columns, order string }{
		{"users", "id, quota", "id"},
		{"merchant_store_products", "id, seller_id, title, price_quota, status, sale_limit, template, pickup_login_required, pickup_code_required", "id"},
		{"merchant_store_variants", "id, product_id, name, price_quota, template, enabled", "id"},
		{"merchant_store_orders", "id, buyer_id, seller_id, product_id, variant_id, variant_name, delivery_template, quantity, unit_price_quota, price_quota, fee_quota, fee_held, amount_minor, currency, status, paid_at, expires_at, claimed_at", "id"},
		{"merchant_store_stocks", "id, product_id, variant_id, state, order_id, md5(ciphertext) AS content_digest", "id"},
		{"merchant_store_transfers", "id, order_id, from_user_id, to_user_id, kind, quota", "id"},
		{"merchant_store_payment_receipts", "id, order_id", "id"},
		{"merchant_store_events", "id, actor_id, object_id, action", "id"},
		{"merchant_store_refunds", "id, order_id, status, quantity, principal_quota", "id"},
		{"merchant_store_order_fixed_deliveries", "order_id, md5(ciphertext) AS content_digest", "order_id"},
	} {
		var rows []map[string]any
		require.NoError(t, db.Table(table.name).Select(table.columns).Order(table.order).Find(&rows).Error)
		state[table.name] = rows
	}
	var emails int64
	require.NoError(t, db.Model(&MerchantStoreEmailDelivery{}).Count(&emails).Error)
	require.Zero(t, emails, "BI08 cases do not enqueue notification work")
	state["email_delivery_count"] = emails
	encoded, err := json.Marshal(state)
	require.NoError(t, err)
	t.Logf("BI08_DB_STATE %s %s", label, encoded)
	return string(encoded)
}
