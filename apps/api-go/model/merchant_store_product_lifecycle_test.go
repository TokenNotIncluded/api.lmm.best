package model

import (
	"context"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/setting"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func TestMerchantStoreUnlistWithdrawsApprovalAndRequiresFreshReview(t *testing.T) {
	f := newStoreFixture(t, "balance")
	require.ErrorIs(t, UnlistMerchantStoreProduct(f.buyer.Id, f.product.ID), ErrMerchantStoreDenied)
	require.ErrorIs(t, UnlistMerchantStoreProduct(f.root.Id, f.product.ID), ErrMerchantStoreDenied)
	require.NoError(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID))
	_, err := GetPublicMerchantStoreProduct(f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	p, err := GetMerchantStoreProduct(f.seller.Id, f.product.ID)
	require.NoError(t, err)
	require.Equal(t, "unlisted", p.Status)
	require.Empty(t, p.AIReviewToken)
	rows, err := ListMerchantStoreProducts(f.seller.Id, false, 0, 30)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.ErrorIs(t, SetMerchantStoreProductPaused(f.seller.Id, p.ID, false), ErrMerchantStoreConflict)
	require.ErrorIs(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID), ErrMerchantStoreConflict)
	_, _, err = CreateMerchantStoreOrder(f.checkout("after-unlist", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	p, err = SaveMerchantStoreProduct(f.seller.Id, p.ID, MerchantStoreProductInput{Title: "Edited listing", PriceQuota: 500000, PaymentMethods: []string{"balance"}})
	require.NoError(t, err)
	require.Equal(t, "draft", p.Status)
	require.Zero(t, p.ReviewedAt)
	require.NoError(t, SubmitMerchantStoreProduct(f.seller.Id, p.ID))
	require.NoError(t, ReviewMerchantStoreProduct(f.root.Id, p.ID, true, "Fresh approval"))
	_, err = GetPublicMerchantStoreProduct(p.ID)
	require.NoError(t, err)
}

func TestMerchantStoreDeletedProductCannotBeReadEditedOrReactivated(t *testing.T) {
	f := newStoreFixture(t, "balance")
	stock, err := ListMerchantStoreStock(f.seller.Id, f.product.ID, 0, 30)
	require.NoError(t, err)
	require.ErrorIs(t, DeleteMerchantStoreProduct(f.buyer.Id, f.product.ID), ErrMerchantStoreDenied)
	require.ErrorIs(t, DeleteMerchantStoreProduct(f.root.Id, f.product.ID), ErrMerchantStoreDenied)
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
	for _, actor := range []int{f.seller.Id, f.root.Id} {
		_, err = GetMerchantStoreProduct(actor, f.product.ID)
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
		_, err = ListMarketAIReviews(context.Background(), actor, ModerationSourceMarketProduct, f.product.ID, "")
		require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	}
	_, err = GetPublicMerchantStoreProduct(f.product.ID)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	_, err = ListMerchantStoreStock(f.seller.Id, f.product.ID, 0, 30)
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	public, err := ListPublicMerchantStoreProducts("", 0, 30)
	require.NoError(t, err)
	require.Empty(t, public)
	for _, review := range []bool{false, true} {
		actor := f.seller.Id
		if review {
			actor = f.root.Id
		}
		rows, listErr := ListMerchantStoreProducts(actor, review, 0, 30)
		require.NoError(t, listErr)
		require.Empty(t, rows)
	}
	_, err = SaveMerchantStoreProduct(f.seller.Id, f.product.ID, MerchantStoreProductInput{Title: "Resurrection", PriceQuota: 1})
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
	for _, op := range []func() error{
		func() error { return SubmitMerchantStoreProduct(f.seller.Id, f.product.ID) },
		func() error { return ReviewMerchantStoreProduct(f.root.Id, f.product.ID, true, "") },
		func() error { return SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, true) },
		func() error { return SetMerchantStoreProductPaused(f.seller.Id, f.product.ID, false) },
		func() error { return UnlistMerchantStoreProduct(f.seller.Id, f.product.ID) },
		func() error { return RemoveMerchantStoreStock(f.seller.Id, f.product.ID, stock[0].ID) },
		func() error { _, e := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"NEW-CARD"}); return e },
		func() error {
			_, e := PurchaseMerchantStorePromotion(f.seller.Id, f.product.ID, 1, "after-delete")
			return e
		},
	} {
		require.ErrorIs(t, op(), gorm.ErrRecordNotFound)
	}
	_, _, err = CreateMerchantStoreOrder(f.checkout("after-delete", "balance"))
	require.ErrorIs(t, err, ErrMerchantStoreUnavailable)
	var retained MerchantStoreProduct
	require.NoError(t, DB.First(&retained, "id = ?", f.product.ID).Error)
	require.Equal(t, f.seller.Id, retained.SellerID)
	require.Equal(t, "deleted", retained.Status)
	var retainedStock int64
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("product_id = ?", f.product.ID).Count(&retainedStock).Error)
	require.Len(t, stock, int(retainedStock))
	var deletes int64
	require.NoError(t, DB.Model(&MerchantStoreEvent{}).Where("object_id = ? AND action = ?", f.product.ID, "deleted").Count(&deletes).Error)
	require.EqualValues(t, 1, deletes)
}

func TestMerchantStoreProductDeletionPreservesPaidAndPendingDelivery(t *testing.T) {
	for _, method := range []string{"balance", "platform:waffo_pancake", "external:epay"} {
		t.Run(method, func(t *testing.T) {
			f := newStoreFixture(t, method)
			in := f.checkout("before-delete", method)
			o, _, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			if method != "balance" {
				require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 100, "USD", "1"))
				require.NoError(t, BindMerchantStorePaymentContext(o.ID, "frozen-gateway-context"))
			}
			var before MerchantStoreStock
			require.NoError(t, DB.First(&before, "order_id = ?", o.ID).Error)
			require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
			replay, made, err := CreateMerchantStoreOrder(in)
			require.NoError(t, err)
			require.False(t, made)
			require.Equal(t, o.ID, replay.ID)
			if method != "balance" {
				// Issued pending orders retain their reservation after expiry and
				// can still reconcile a verified callback after product deletion.
				require.NoError(t, DB.Model(&MerchantStoreOrder{}).Where("id = ?", o.ID).Update("expires_at", common.GetTimestamp()-1).Error)
				n, e := ExpireMerchantStoreOrders(30)
				require.NoError(t, e)
				require.Equal(t, 1, n)
				require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-after-delete"))
				require.NoError(t, CompleteMerchantStorePayment(o.ID, "verified-after-delete"))
			}
			paid, err := GetMerchantStoreOrder(f.buyer.Id, o.ID)
			require.NoError(t, err)
			require.Equal(t, "paid", paid.Status)
			require.Equal(t, f.seller.Id, paid.SellerID)
			token, err := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
			require.NoError(t, err)
			claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
			require.NoError(t, err)
			require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
			var after MerchantStoreStock
			require.NoError(t, DB.First(&after, "id = ?", before.ID).Error)
			require.Equal(t, before.Ciphertext, after.Ciphertext)
			require.Equal(t, "delivered", after.State)
			var transfers, emails int64
			require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&transfers).Error)
			expectedTransfers := int64(2)
			if method == "platform:waffo_pancake" {
				expectedTransfers = 3 // Reserved fee, sale, and verified fee.
			}
			if method == "external:epay" {
				expectedTransfers = 2 // Reserved fee and verified fee.
			}
			require.Equal(t, expectedTransfers, transfers)
			require.NoError(t, DB.Model(&MerchantStoreEmailDelivery{}).Where("order_id = ?", o.ID).Count(&emails).Error)
			require.EqualValues(t, 1, emails)
			var p MerchantStoreProduct
			require.NoError(t, DB.First(&p, "id = ?", f.product.ID).Error)
			require.Equal(t, "deleted", p.Status)
		})
	}
}

func TestMerchantStoreDeletedPendingOrderCanCancelAndRefundOnce(t *testing.T) {
	f := newStoreFixture(t, "platform:waffo_pancake")
	o, _, err := CreateMerchantStoreOrder(f.checkout("unissued-before-delete", "platform:waffo_pancake"))
	require.NoError(t, err)
	require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	require.NoError(t, CancelMerchantStoreOrder(f.buyer.Id, o.ID))
	storeBalance(t, f.seller.Id, 10000000)
}

func TestMerchantStoreProductDeletionCancelsRunningAndSealsAppliedAIReview(t *testing.T) {
	for _, complete := range []bool{false, true} {
		t.Run(map[bool]string{false: "running", true: "applied"}[complete], func(t *testing.T) {
			db, seller, root, p := marketAIProduct(t, setting.MarketAIReviewAuto)
			j := marketAIClaim(t)
			if complete {
				require.NoError(t, marketAIComplete(t, j, false, false))
			}
			// A historical outstanding token must also lose its payload and lease.
			old := *j
			old.ID, old.EventKey, old.RequestID = 0, "historical-review", "historical-token"
			old.Status, old.Payload = ModerationJobPending, "historical-provider-payload"
			require.NoError(t, db.Create(&old).Error)
			require.NoError(t, DeleteMerchantStoreProduct(seller.Id, p.ID))
			require.Error(t, MarketAIReviewPreflight(context.Background(), j))
			if complete {
				require.NoError(t, marketAIComplete(t, j, false, false))
			} else {
				require.ErrorIs(t, marketAIComplete(t, j, false, false), ErrModerationLeaseLost)
			}
			require.ErrorIs(t, ReviewMerchantStoreProduct(root.Id, p.ID, true, "stale override"), gorm.ErrRecordNotFound)
			require.NoError(t, db.First(p, "id = ?", p.ID).Error)
			require.Equal(t, "deleted", p.Status)
			require.Empty(t, p.AIReviewToken)
			for _, id := range []int64{j.ID, old.ID} {
				var sealed ModerationJob
				require.NoError(t, db.First(&sealed, id).Error)
				require.Equal(t, "stale", sealed.MarketOutcome)
				require.Empty(t, sealed.Payload)
				require.Empty(t, sealed.LeaseOwner)
				require.Zero(t, sealed.LeaseUntil)
			}
		})
	}
}

func TestMerchantStoreProductLifecycleUsesOwnershipForEverySellerRole(t *testing.T) {
	for _, role := range []int{common.RoleCommonUser, common.RoleAdminUser, common.RoleRootUser} {
		t.Run(map[int]string{common.RoleCommonUser: "user", common.RoleAdminUser: "admin", common.RoleRootUser: "root"}[role], func(t *testing.T) {
			f := newStoreFixture(t, "balance")
			require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("role", role).Error)
			require.NoError(t, UnlistMerchantStoreProduct(f.seller.Id, f.product.ID))
			require.NoError(t, DeleteMerchantStoreProduct(f.seller.Id, f.product.ID))
		})
	}
}

func TestMerchantStorePostgresDeletionSerializesWithCheckout(t *testing.T) {
	for _, checkoutFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "deletion-wins", true: "checkout-wins"}[checkoutFirst], func(t *testing.T) {
			db, name, observer, _ := merchantStorePGDB(t)
			f := merchantStorePGFixture(t, db, "platform:waffo_pancake")
			_, err := AddMerchantStoreStock(f.seller.Id, f.product.ID, []string{"reserved-before-delete"})
			require.NoError(t, err)
			var order *MerchantStoreOrder
			checkout := func() error {
				in := f.checkout("lifecycle-lock", "platform:waffo_pancake")
				in.PickupEmail = ""
				var e error
				order, _, e = CreateMerchantStoreOrder(in)
				return e
			}
			deletion := func() error { return DeleteMerchantStoreProduct(f.seller.Id, f.product.ID) }
			ops := []func() error{deletion, checkout}
			if checkoutFirst {
				ops = []func() error{checkout, deletion}
			}
			errs := merchantStorePGContend(t, db, observer, name, ops)
			require.NoError(t, errs[0])
			if checkoutFirst {
				require.NoError(t, errs[1])
				require.NoError(t, BindMerchantStorePaymentQuote(order.ID, 100, "USD", "1"))
				require.NoError(t, CompleteMerchantStorePayment(order.ID, "pg-after-delete"))
			} else {
				require.ErrorIs(t, errs[1], ErrMerchantStoreUnavailable)
				var orders int64
				require.NoError(t, db.Model(&MerchantStoreOrder{}).Count(&orders).Error)
				require.Zero(t, orders)
			}
			var p MerchantStoreProduct
			require.NoError(t, db.First(&p, "id = ?", f.product.ID).Error)
			require.Equal(t, "deleted", p.Status)
		})
	}
}
