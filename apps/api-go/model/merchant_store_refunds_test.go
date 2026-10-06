package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func refundActivate(t *testing.T) {
	t.Helper()
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "4").Error)
}
func refundPaidOrder(t *testing.T, method string, quantity int) (storeFixture, *MerchantStoreOrder, string) {
	t.Helper()
	f := newStoreFixture(t, method)
	in := f.checkout("refund-order", method)
	in.Quantity = quantity
	o, _, e := CreateMerchantStoreOrder(in)
	require.NoError(t, e)
	if method != "balance" {
		require.NoError(t, BindMerchantStorePaymentQuote(o.ID, 3, "USD", "1"))
		require.NoError(t, CompleteMerchantStorePayment(o.ID, "original-provider-receipt"))
		require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	}
	token, e := GetMerchantStoreOrderPickupToken(f.buyer.Id, o.ID)
	require.NoError(t, e)
	refundActivate(t)
	return f, o, token
}
func refundFull(key string) MerchantStoreRefundInput {
	return MerchantStoreRefundInput{RequestKey: key, Reason: "Product issue", Mode: "full"}
}
func refundApprove(t *testing.T, actor int, o *MerchantStoreOrder, r *MerchantStoreRefund) *MerchantStoreRefund {
	t.Helper()
	done, e := DecideMerchantStoreRefund(actor, o.ID, r.ID, MerchantStoreRefundDecision{Decision: "approve"})
	require.NoError(t, e)
	return done
}
func refundStockCount(t *testing.T, o *MerchantStoreOrder, state string, want int64) {
	t.Helper()
	var n int64
	require.NoError(t, storeOrderStock(DB, o).Model(&MerchantStoreStock{}).Where("state = ?", state).Count(&n).Error)
	require.Equal(t, want, n)
}

func TestMerchantStoreRefundBalancePartialQuantityAmountFullAudit(t *testing.T) {
	f, o, token := refundPaidOrder(t, "balance", 2)
	require.Equal(t, 1000000, o.PriceQuota)
	require.Equal(t, 10000, o.FeeQuota)
	// Future discounted orders may retain an original SKU unit snapshot. Refund
	// the frozen paid net principal, never reconstruct it from the unit price.
	require.NoError(t, DB.Model(o).Update("unit_price_quota", 1000000).Error)
	view, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Len(t, view.EligibleItems, 2)
	quantity := MerchantStoreRefundInput{RequestKey: "quantity", Reason: "One card broken", Mode: "quantity", Quantity: 1, StockIDs: []string{view.EligibleItems[1].StockID}}
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, quantity)
	require.NoError(t, e)
	require.Equal(t, 500000, r.PrincipalQuota)
	replay, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, quantity)
	require.NoError(t, e)
	require.Equal(t, r.ID, replay.ID)
	changed := quantity
	changed.Reason = "Changed body"
	_, e = RequestMerchantStoreRefund(f.buyer.Id, o.ID, changed)
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	done := refundApprove(t, f.seller.Id, o, r)
	require.Equal(t, "completed", done.Status)
	refundStockCount(t, o, "refunded", 1)
	refundStockCount(t, o, "available", 0)
	claim, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, 1, claim.Quantity)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	adjust := MerchantStoreRefundInput{RequestKey: "adjust", Reason: "Difference", Mode: "amount", AmountQuota: 12345}
	a, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, adjust)
	require.NoError(t, e)
	refundApprove(t, f.root.Id, o, a)
	refundStockCount(t, o, "refunded", 1)
	r, e = RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("remainder"))
	require.NoError(t, e)
	require.Equal(t, 487655, r.PrincipalQuota)
	refundApprove(t, f.seller.Id, o, r)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	require.NotZero(t, o.PaidAt)
	require.Equal(t, 2, o.Quantity)
	require.Equal(t, 1000000, o.PriceQuota)
	require.Equal(t, 10000, o.FeeQuota)
	refundStockCount(t, o, "refunded", 2)
	refundStockCount(t, o, "available", 0)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.root.Id, 10000)
	_, e = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	usage, e := storeSalesUsage(DB, o.ProductID)
	require.NoError(t, e)
	require.EqualValues(t, 2, usage.Paid)
	var original, refunds int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind IN ?", o.ID, []string{"sale", "fee"}).Count(&original).Error)
	require.EqualValues(t, 2, original)
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", o.ID, "refund").Count(&refunds).Error)
	require.EqualValues(t, 3, refunds)
	refundApprove(t, f.seller.Id, o, r)
	storeBalance(t, f.buyer.Id, 10000000)
}
func TestMerchantStoreRefundReservationReleaseConcurrentAndFinalAmountRetires(t *testing.T) {
	f, o, _ := refundPaidOrder(t, "balance", 2)
	first := MerchantStoreRefundInput{RequestKey: "first", Reason: "Adjustment", Mode: "amount", AmountQuota: 600000}
	second := MerchantStoreRefundInput{RequestKey: "second", Reason: "Adjustment", Mode: "amount", AmountQuota: 600000}
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	for _, in := range []MerchantStoreRefundInput{first, second} {
		wg.Add(1)
		go func(in MerchantStoreRefundInput) {
			defer wg.Done()
			_, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, in)
			errs <- e
		}(in)
	}
	wg.Wait()
	close(errs)
	successes, conflicts := 0, 0
	for e := range errs {
		if e == nil {
			successes++
		} else {
			require.ErrorIs(t, e, ErrMerchantStoreConflict)
			conflicts++
		}
	}
	require.Equal(t, 1, successes)
	require.Equal(t, 1, conflicts)
	view, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Equal(t, 400000, view.RemainingQuota)
	cancelled, e := CancelMerchantStoreRefund(f.buyer.Id, o.ID, view.Refunds[0].ID)
	require.NoError(t, e)
	require.Equal(t, "cancelled", cancelled.Status)
	rejected, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("reject"))
	require.NoError(t, e)
	_, e = DecideMerchantStoreRefund(f.seller.Id, o.ID, rejected.ID, MerchantStoreRefundDecision{Decision: "reject", Reason: "Investigate"})
	require.NoError(t, e)
	a, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "a", Reason: "A", Mode: "amount", AmountQuota: 500000})
	require.NoError(t, e)
	b, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "b", Reason: "B", Mode: "amount", AmountQuota: 500000})
	require.NoError(t, e)
	refundApprove(t, f.seller.Id, o, b)
	refundStockCount(t, o, "refunded", 0)
	done := refundApprove(t, f.seller.Id, o, a)
	require.Equal(t, 2, done.Quantity)
	require.Len(t, done.StockIDs, 2)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	refundStockCount(t, o, "refunded", 2)
}
func TestMerchantStoreRefundBalanceInsufficientRecipientBoundsAndSelfAudit(t *testing.T) {
	t.Run("atomicFailure", func(t *testing.T) {
		f, o, _ := refundPaidOrder(t, "balance", 1)
		r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("full"))
		require.NoError(t, e)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
		_, e = DecideMerchantStoreRefund(f.seller.Id, o.ID, r.ID, MerchantStoreRefundDecision{Decision: "approve"})
		require.ErrorIs(t, e, ErrMerchantStoreBalance)
		refundStockCount(t, o, "delivered", 1)
		require.NoError(t, DB.First(r, "id = ?", r.ID).Error)
		require.Equal(t, "requested", r.Status)
		require.Zero(t, r.DecidedAt)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 1000000).Error)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", f.buyer.Id).Update("quota", common.MaxWalletQuota).Error)
		_, e = DecideMerchantStoreRefund(f.seller.Id, o.ID, r.ID, MerchantStoreRefundDecision{Decision: "approve"})
		require.ErrorIs(t, e, ErrWalletQuotaOutOfRange)
		storeBalance(t, f.seller.Id, 1000000)
		refundStockCount(t, o, "delivered", 1)
	})
	t.Run("selfNetZero", func(t *testing.T) {
		f := newStoreFixture(t, "balance")
		require.NoError(t, AcceptMerchantStoreDisclaimer(f.seller.Id, MerchantStoreDisclaimerVersion))
		in := f.checkout("self", "balance")
		in.BuyerID = f.seller.Id
		in.PickupEmail = ""
		require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", f.product.ID).Update("email_pickup_link", false).Error)
		o, _, e := CreateMerchantStoreOrder(in)
		require.NoError(t, e)
		refundActivate(t)
		require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
		r, e := ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("self-refund"))
		require.NoError(t, e)
		require.Equal(t, "completed", r.Status)
		storeBalance(t, f.seller.Id, 0)
		storeBalance(t, f.root.Id, 5000)
		var transfer MerchantStoreTransfer
		require.NoError(t, DB.First(&transfer, "id = ?", r.ID+":refund").Error)
		require.Equal(t, f.seller.Id, transfer.FromUserID)
		require.Equal(t, f.seller.Id, transfer.ToUserID)
		require.Equal(t, 500000, transfer.Quota)
	})
}
func TestMerchantStoreRefundOwnershipPurePickupAndDeletedProduct(t *testing.T) {
	f, o, token := refundPaidOrder(t, "balance", 2)
	other := marketTestUser(t, DB, "other", 1000000, common.RoleAdminUser)
	_, e := GetMerchantStoreRefunds(other.Id, o.ID)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	_, e = RequestMerchantStoreRefund(other.Id, o.ID, refundFull("attack"))
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	_, e = ProactivelyRefundMerchantStoreOrder(other.Id, o.ID, refundFull("attack"))
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	proof := MerchantStoreRefundPickupProof{OrderID: o.ID, Token: token, Code: "safe-pickup-code"}
	_, e = GetMerchantStoreRefundsWithPickupProof(0, proof)
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	require.NoError(t, DB.Model(o).Update("pickup_login_required", false).Error)
	view, e := GetMerchantStoreRefundsWithPickupProof(0, proof)
	require.NoError(t, e)
	require.Len(t, view.EligibleItems, 2)
	encoded, e := json.Marshal(view)
	require.NoError(t, e)
	require.NotContains(t, string(encoded), "CARD-SECRET")
	require.NotContains(t, string(encoded), token)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Zero(t, o.ClaimedAt)
	require.NoError(t, DB.Model(&MerchantStoreProduct{}).Where("id = ?", o.ProductID).Update("status", "deleted").Error)
	bad := proof
	bad.Code = "wrong"
	_, e = RequestMerchantStoreRefundWithPickupProof(0, bad, refundFull("guest"))
	require.ErrorIs(t, e, ErrMerchantStoreDenied)
	r, e := RequestMerchantStoreRefundWithPickupProof(0, proof, refundFull("guest"))
	require.NoError(t, e)
	require.Equal(t, o.BuyerID, r.RequestedBy)
	refundApprove(t, f.root.Id, o, r)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Zero(t, o.ClaimedAt)
	require.Equal(t, "refunded", o.Status)
}
func TestMerchantStoreRefundExactStockPoolAndWriterFloor(t *testing.T) {
	f, o, _ := refundPaidOrder(t, "balance", 1)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "3").Error)
	_, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("frozen"))
	require.ErrorIs(t, e, ErrMerchantStoreWriterFrozen)
	refundActivate(t)
	var wrong MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ? AND state = ?", o.ProductID, "available").First(&wrong).Error)
	_, e = RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "wrong", Reason: "Wrong pool", Mode: "quantity", Quantity: 1, StockIDs: []string{wrong.ID}})
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	view, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("id = ?", view.EligibleItems[0].StockID).Update("product_id", "wrong-product").Error)
	_, e = RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("pool-corrupt"))
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
}
func refundNativeBasis(t *testing.T, o *MerchantStoreOrder, amount int64) {
	t.Helper()
	require.NoError(t, RecordMerchantStoreRefundPaymentBasis(o.ID, VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: o.ProviderTradeID, PaymentReference: "original-PAY", AmountMinor: amount, Currency: o.Currency, EvidenceHash: strings.Repeat("a", 64)}))
}
func refundNativeComplete(t *testing.T, r *MerchantStoreRefund) {
	t.Helper()
	require.NoError(t, CompleteMerchantStoreVerifiedRefund(r.ID, MerchantStoreVerifiedRefundEvidence{PaymentReference: "original-PAY", RefundReference: "refund-" + r.ID, AmountMinor: r.AmountMinor, Currency: r.Currency, EvidenceHash: strings.Repeat("b", 64)}))
}
func TestMerchantStoreRefundExternalAwaitingNoFakeCreditsAndTerminalCallbacks(t *testing.T) {
	f, o, token := refundPaidOrder(t, "external:epay", 2)
	v, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.False(t, v.NativeBasisVerified)
	require.Nil(t, v.AmountMinor)
	require.False(t, v.SupportsAmount)
	_, e = RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "unsupported", Reason: "Amount", Mode: "amount", AmountMinor: 1})
	require.ErrorIs(t, e, ErrMerchantStoreRefundUnsupported)
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("external"))
	require.NoError(t, e)
	require.Zero(t, r.AmountMinor)
	r = refundApprove(t, f.seller.Id, o, r)
	require.Equal(t, "awaiting_provider", r.Status)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refund_pending", o.Status)
	storeBalance(t, f.buyer.Id, 10000000)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, o.ProviderTradeID))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, o.ProviderTradeID, "settlement_conflict"))
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refund_pending", o.Status)
	_, e = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	refundNativeBasis(t, o, 4)
	require.NoError(t, DB.First(r, "id = ?", r.ID).Error)
	require.EqualValues(t, 4, r.AmountMinor)
	refundNativeComplete(t, r)
	refundNativeComplete(t, r)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	require.NoError(t, CompleteMerchantStorePayment(o.ID, o.ProviderTradeID))
	require.NoError(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, o.ProviderTradeID, "settlement_conflict"))
	require.ErrorIs(t, RecordMerchantStoreVerifiedPaymentIssue(o.ID, "other-receipt", "settlement_conflict"), ErrMerchantStoreConflict)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.root.Id, 10000)
	refundStockCount(t, o, "available", 0)
}
func TestMerchantStoreRefundNativeCumulativeFloorAndRemainingClaim(t *testing.T) {
	f, o, token := refundPaidOrder(t, "platform:waffo_pancake", 2)
	refundNativeBasis(t, o, 3)
	inputs := []MerchantStoreRefundInput{{RequestKey: "one", Reason: "First adjustment", Mode: "amount", AmountMinor: 1}, {RequestKey: "two", Reason: "Second adjustment", Mode: "amount", AmountMinor: 1}, {RequestKey: "three", Reason: "Third adjustment", Mode: "amount", AmountMinor: 1}}
	records := []*MerchantStoreRefund{}
	for _, in := range inputs {
		r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, in)
		require.NoError(t, e)
		records = append(records, refundApprove(t, f.seller.Id, o, r))
	}
	require.Equal(t, 333333, records[0].PrincipalQuota)
	require.Equal(t, 333333, records[1].PrincipalQuota)
	require.Equal(t, 333334, records[2].PrincipalQuota)
	// Complete in a different order: cumulative native truth still allocates the
	// exact original quota, with the final minor receiving the remainder.
	refundNativeComplete(t, records[2])
	refundStockCount(t, o, "refunded", 0)
	claim, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Len(t, claim.Items, 2)
	refundNativeComplete(t, records[0])
	refundNativeComplete(t, records[1])
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Equal(t, "refunded", o.Status)
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.buyer.Id, 10000000)
	refundStockCount(t, o, "refunded", 2)
}
func TestMerchantStoreRefundNativeSuccessProofSurvivesLocalWalletFailure(t *testing.T) {
	f, o, _ := refundPaidOrder(t, "platform:waffo_pancake", 1)
	refundNativeBasis(t, o, 3)
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("native"))
	require.NoError(t, e)
	r = refundApprove(t, f.seller.Id, o, r)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
	proof := MerchantStoreVerifiedRefundEvidence{PaymentReference: "original-PAY", RefundReference: "actual-provider-refund", AmountMinor: 3, Currency: "USD", EvidenceHash: strings.Repeat("b", 64)}
	require.ErrorIs(t, CompleteMerchantStoreVerifiedRefund(r.ID, proof), ErrMerchantStoreBalance)
	require.NoError(t, DB.First(r, "id = ?", r.ID).Error)
	require.Equal(t, "reconciliation_required", r.Status)
	require.NotNil(t, r.ProviderRefundReference)
	require.Equal(t, proof.RefundReference, *r.ProviderRefundReference)
	refundStockCount(t, o, "delivered", 1)
	storeBalance(t, f.buyer.Id, 10000000)
	bad := proof
	bad.RefundReference = "changed"
	require.ErrorIs(t, CompleteMerchantStoreVerifiedRefund(r.ID, bad), ErrMerchantStoreConflict)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 500000).Error)
	require.NoError(t, CompleteMerchantStoreVerifiedRefund(r.ID, proof))
	storeBalance(t, f.seller.Id, 0)
	refundStockCount(t, o, "refunded", 1)
}

func TestMerchantStoreRefundNativeQuantityHeldThenVerifiedFailureReleases(t *testing.T) {
	f, o, token := refundPaidOrder(t, "platform:waffo_pancake", 2)
	refundNativeBasis(t, o, 3)
	v, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Equal(t, 2, v.MaxQuantity)
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "quantity-native", Reason: "One card", Mode: "quantity", Quantity: 1, StockIDs: []string{v.EligibleItems[1].StockID}})
	require.NoError(t, e)
	r = refundApprove(t, f.seller.Id, o, r)
	claim, e := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Equal(t, []string{"CARD-SECRET-FIRST"}, claim.Items)
	failure := MerchantStoreVerifiedRefundFailure{PaymentReference: "original-PAY", RefundReference: "failed-refund-reference", RequestedAmountMinor: r.AmountMinor, Currency: r.Currency, EvidenceHash: strings.Repeat("c", 64)}
	require.NoError(t, RejectMerchantStoreVerifiedRefund(r.ID, failure))
	require.NoError(t, RejectMerchantStoreVerifiedRefund(r.ID, failure))
	claim, e = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.NoError(t, e)
	require.Len(t, claim.Items, 2)
	refundStockCount(t, o, "refunded", 0)
	v, e = GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Equal(t, o.PriceQuota, v.RemainingQuota)
	require.Equal(t, "rejected", v.Refunds[0].Status)
	proof := MerchantStoreVerifiedRefundEvidence{PaymentReference: "original-PAY", RefundReference: failure.RefundReference, AmountMinor: r.AmountMinor, Currency: r.Currency, EvidenceHash: failure.EvidenceHash}
	require.ErrorIs(t, CompleteMerchantStoreVerifiedRefund(r.ID, proof), ErrMerchantStoreConflict)
}
func TestMerchantStoreRefundTinyNativeAmountAndCancellationFloor(t *testing.T) {
	t.Run("zeroCreditsIsRealNativeRefund", func(t *testing.T) {
		f, o, _ := refundPaidOrder(t, "platform:waffo_pancake", 1)
		refundNativeBasis(t, o, 1000001)
		r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "tiny", Reason: "Difference", Mode: "amount", AmountMinor: 1})
		require.NoError(t, e)
		require.Zero(t, r.PrincipalQuota)
		r = refundApprove(t, f.seller.Id, o, r)
		refundNativeComplete(t, r)
		v, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
		require.NoError(t, e)
		require.EqualValues(t, 1, *v.RefundedAmountMinor)
		require.Zero(t, v.RefundedQuota)
		require.Equal(t, 500000, v.RemainingQuota)
		r, e = ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("rest"))
		require.NoError(t, e)
		require.EqualValues(t, 1000000, r.AmountMinor)
		refundNativeComplete(t, r)
		storeBalance(t, f.seller.Id, 9995000)
	})
	t.Run("cancelAndCompleteReordered", func(t *testing.T) {
		f, o, _ := refundPaidOrder(t, "platform:waffo_pancake", 2)
		refundNativeBasis(t, o, 3)
		records := []*MerchantStoreRefund{}
		for _, key := range []string{"a", "b", "c"} {
			r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: key, Reason: "Difference", Mode: "amount", AmountMinor: 1})
			require.NoError(t, e)
			records = append(records, r)
		}
		_, e := CancelMerchantStoreRefund(f.buyer.Id, o.ID, records[0].ID)
		require.NoError(t, e)
		r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, MerchantStoreRefundInput{RequestKey: "d", Reason: "Difference", Mode: "amount", AmountMinor: 1})
		require.NoError(t, e)
		records = append(records, r)
		for _, r := range []*MerchantStoreRefund{records[2], records[3], records[1]} {
			done := refundApprove(t, f.seller.Id, o, r)
			refundNativeComplete(t, done)
		}
		v, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
		require.NoError(t, e)
		require.Equal(t, 1000000, v.RefundedQuota)
		require.EqualValues(t, 3, *v.RefundedAmountMinor)
		storeBalance(t, f.seller.Id, 9990000)
	})
}
func TestMerchantStoreRefundNeverBorrowsAnotherVariantStock(t *testing.T) {
	f := newVariantsFixture(t, "balance")
	v := storeTestVariant(t, f, "Custom grade", 750001, "SPEC-ONLY-CARD")
	storePublishVariants(t, f)
	o, _, e := CreateMerchantStoreOrder(storeVariantCheckout(f, v, "refund-spec", "balance"))
	require.NoError(t, e)
	refundActivate(t)
	view, e := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, e)
	require.Equal(t, "Custom grade", view.VariantName)
	require.Len(t, view.EligibleItems, 1)
	wrong := MerchantStoreDefaultVariantID(o.ProductID)
	require.NoError(t, DB.Model(&MerchantStoreStock{}).Where("id = ?", view.EligibleItems[0].StockID).Update("variant_id", wrong).Error)
	_, e = ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("pool"))
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	storeBalance(t, f.buyer.Id, 9249999)
	refundStockCount(t, o, "refunded", 0)
}

func TestMerchantStoreRefundFreePrincipalNeverCreditsOrReopensInventory(t *testing.T) {
	f := newStoreFixture(t, "balance")
	o := &MerchantStoreOrder{ID: storeHash("free-fixture"), TradeNo: "MS_FREE_REFUND_FIXTURE", BuyerID: f.buyer.Id, SellerID: f.seller.Id, ProductID: f.product.ID, VariantID: MerchantStoreDefaultVariantID(f.product.ID), Quantity: 1, UnitPriceQuota: 500000, PriceQuota: 0, FeeQuota: 0, Status: "paid", PaidAt: common.GetTimestamp(), PaymentMethod: "free", Currency: "CREDIT"}
	require.NoError(t, DB.Create(o).Error)
	var stock MerchantStoreStock
	require.NoError(t, DB.Where("product_id = ? AND state = ?", o.ProductID, "available").First(&stock).Error)
	require.NoError(t, DB.Model(&stock).Updates(map[string]any{"state": "delivered", "order_id": o.ID}).Error)
	refundActivate(t)
	_, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("free"))
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	_, e = ProactivelyRefundMerchantStoreOrder(f.root.Id, o.ID, MerchantStoreRefundInput{RequestKey: "fake-positive", Reason: "No paid money", Mode: "amount", AmountQuota: 1})
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 10000000)
	storeBalance(t, f.root.Id, 0)
	refundStockCount(t, o, "delivered", 1)
	refundStockCount(t, o, "refunded", 0)
	refundStockCount(t, o, "available", 0)
	var n int64
	require.NoError(t, DB.Model(&MerchantStoreRefund{}).Where("order_id = ?", o.ID).Count(&n).Error)
	require.Zero(t, n)
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ?", o.ID).Count(&n).Error)
	require.Zero(t, n)
}
