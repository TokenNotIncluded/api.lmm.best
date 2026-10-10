package model

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/stretchr/testify/require"
)

func refundSyncFixture(t *testing.T) (storeFixture, *MerchantStoreOrder, string) {
	t.Helper()
	f, o, token := refundPaidOrder(t, "platform:waffo_pancake", 2)
	refundNativeBasis(t, o, 3)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "7").Error)
	require.NoError(t, ActivateMerchantStoreRefundSync(DB, 7))
	return f, o, token
}
func refundSyncProof(reference string, minor int64) MerchantStoreVerifiedRefundEvidence {
	return MerchantStoreVerifiedRefundEvidence{PaymentReference: "original-PAY", RefundReference: reference, AmountMinor: minor, Currency: "USD", EvidenceHash: storeHash(reference)}
}

func TestMerchantStoreRefundSyncPartialDuplicatesAndFinalRetirement(t *testing.T) {
	f, o, token := refundSyncFixture(t)
	for i, reference := range []string{"native-third", "native-first", "native-second"} {
		proof := refundSyncProof(reference, 1)
		id, err := RecordMerchantStoreExternalRefund(o.ID, proof)
		require.NoError(t, err)
		require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
		replay, err := RecordMerchantStoreExternalRefund(o.ID, proof)
		require.NoError(t, err)
		require.Equal(t, id, replay)
		require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
		view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
		require.NoError(t, err)
		require.EqualValues(t, i+1, *view.RefundedAmountMinor)
		require.Equal(t, 1000000*(i+1)/3, view.RefundedQuota)
		if i < 2 {
			claim, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
			require.NoError(t, err)
			require.Len(t, claim.Items, 2, "amount-only partial refunds do not invent retired card quantities")
		}
	}
	storeBalance(t, f.buyer.Id, 10000000)
	storeBalance(t, f.seller.Id, 9990000)
	storeBalance(t, f.root.Id, 10000)
	refundStockCount(t, o, "refunded", 2)
	refundStockCount(t, o, "available", 0)
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", o.ID, "refund_external").Count(&count).Error)
	require.EqualValues(t, 3, count)
	metadata, err := InspectMerchantStoreClaim(token)
	require.NoError(t, err)
	require.Equal(t, "refunded", metadata.Status, "keep refund metadata reachable without exposing retired content")
	_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	encoded, err := json.Marshal(view)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "original-PAY")
	require.NotContains(t, string(encoded), "native-first")
	require.NotContains(t, string(encoded), "provider_evidence_hash")
}

func TestMerchantStoreRefundSyncKeepsUnknownDispatchAndFencesNewMoney(t *testing.T) {
	f, o, token := refundSyncFixture(t)
	r, err := ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("already-submitted"))
	require.NoError(t, err)
	d, err := PrepareMerchantStoreRefundDispatch(r.ID, common.GetTimestamp())
	require.NoError(t, err)
	require.NotNil(t, d)
	sent, err := ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, common.GetTimestamp())
	require.NoError(t, err)
	require.True(t, sent)
	id, err := RecordMerchantStoreExternalRefund(o.ID, refundSyncProof("independent-refund", 1))
	require.NoError(t, err)
	require.ErrorIs(t, ReconcileMerchantStoreExternalRefund(id), ErrMerchantStoreRefundReconciliation)
	view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.True(t, view.ProviderReconciliationPending)
	require.True(t, view.SupportsProviderSync)
	_, err = RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("do-not-send"))
	require.ErrorIs(t, err, ErrMerchantStoreRefundReconciliation)
	_, err = ClaimMerchantStoreOrder(token, "safe-pickup-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreRefundReconciliation)
	require.NoError(t, DB.First(r, "id = ?", r.ID).Error)
	require.Equal(t, "awaiting_provider", r.Status, "an ambiguous submitted refund is never cancelled from a different receipt")
	imported, err := PrepareMerchantStoreRefundDispatch(id, common.GetTimestamp())
	require.NoError(t, err)
	require.NotNil(t, imported)
	require.Equal(t, "succeeded", imported.Attempt.State)
	require.Equal(t, 1, imported.Attempt.SubmitCount, "imported receipts can never POST a new refund")
	// Only an authenticated terminal failure can release the submitted request.
	require.NoError(t, RejectMerchantStoreVerifiedRefund(r.ID, MerchantStoreVerifiedRefundFailure{PaymentReference: "original-PAY", RefundReference: "provider-failed-operation", RequestedAmountMinor: 3, Currency: "USD", EvidenceHash: strings.Repeat("c", 64)}))
	require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
	view, err = GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.False(t, view.ProviderReconciliationPending)
	require.EqualValues(t, 1, *view.RefundedAmountMinor)
}

func TestMerchantStoreRefundSyncSupersedesOnlyUnsentConflicts(t *testing.T) {
	f, o, _ := refundSyncFixture(t)
	r, err := ProactivelyRefundMerchantStoreOrder(f.seller.Id, o.ID, refundFull("not-sent"))
	require.NoError(t, err)
	lease, err := PrepareMerchantStoreRefundDispatch(r.ID, common.GetTimestamp())
	require.NoError(t, err)
	require.NotNil(t, lease)
	id, err := RecordMerchantStoreExternalRefund(o.ID, refundSyncProof("dashboard-partial", 1))
	require.NoError(t, err)
	require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
	claimed, err := ClaimMerchantStoreRefundSubmit(r.ID, lease.Attempt.LeaseToken, lease.Attempt.RequestHash, common.GetTimestamp())
	require.ErrorIs(t, err, ErrMerchantStoreConflict)
	require.False(t, claimed, "a worker holding an old lease cannot send after provider import")
	require.NoError(t, DB.First(r, "id = ?", r.ID).Error)
	require.Equal(t, "rejected", r.Status)
	require.Contains(t, r.DecisionReason, "before this request was sent")
	view, err := GetMerchantStoreRefunds(f.buyer.Id, o.ID)
	require.NoError(t, err)
	require.EqualValues(t, 2, *view.RemainingAmountMinor)
}

func TestMerchantStoreRefundSyncWalletFailureRetainsReceipt(t *testing.T) {
	f, o, _ := refundSyncFixture(t)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 0).Error)
	id, err := RecordMerchantStoreExternalRefund(o.ID, refundSyncProof("already-paid-back", 1))
	require.NoError(t, err)
	require.ErrorIs(t, ReconcileMerchantStoreExternalRefund(id), ErrMerchantStoreBalance)
	var r MerchantStoreRefund
	require.NoError(t, DB.First(&r, "id = ?", id).Error)
	require.Equal(t, "reconciliation_required", r.Status)
	require.Equal(t, "already-paid-back", *r.ProviderRefundReference)
	require.NoError(t, DB.Model(&User{}).Where("id = ?", f.seller.Id).Update("quota", 1000000).Error)
	require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
	require.NoError(t, ReconcileMerchantStoreExternalRefund(id))
	storeBalance(t, f.seller.Id, 666667)
	storeBalance(t, f.buyer.Id, 10000000)
}

func TestMerchantStoreRefundSyncConcurrentReceiptsAndEvidenceMismatch(t *testing.T) {
	_, o, _ := refundSyncFixture(t)
	proof := refundSyncProof("one-execution-many-notifications", 1)
	var wg sync.WaitGroup
	errs := make(chan error, 12)
	for range 12 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			id, err := RecordMerchantStoreExternalRefund(o.ID, proof)
			if err == nil {
				err = ReconcileMerchantStoreExternalRefund(id)
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	var count int64
	require.NoError(t, DB.Model(&MerchantStoreTransfer{}).Where("order_id = ? AND kind = ?", o.ID, "refund_external").Count(&count).Error)
	require.EqualValues(t, 1, count)
	for _, change := range []func(*MerchantStoreVerifiedRefundEvidence){
		func(p *MerchantStoreVerifiedRefundEvidence) { p.AmountMinor = 2 },
		func(p *MerchantStoreVerifiedRefundEvidence) { p.Currency = "CNY" },
		func(p *MerchantStoreVerifiedRefundEvidence) { p.PaymentReference = "other-payment" },
		func(p *MerchantStoreVerifiedRefundEvidence) { p.EvidenceHash = strings.Repeat("d", 64) },
	} {
		bad := proof
		change(&bad)
		_, err := RecordMerchantStoreExternalRefund(o.ID, bad)
		require.ErrorIs(t, err, ErrMerchantStoreConflict)
	}
}

func TestMerchantStoreRefundSyncActivationRequiresReviewedFloorAndSchema(t *testing.T) {
	_, o, _ := refundPaidOrder(t, "platform:waffo_pancake", 2)
	refundNativeBasis(t, o, 3)
	_, err := RecordMerchantStoreExternalRefund(o.ID, refundSyncProof("no-silent-activation", 1))
	require.ErrorIs(t, err, ErrMerchantStoreWriterFrozen)
	require.ErrorIs(t, ActivateMerchantStoreRefundSync(DB, 4), ErrMerchantStoreWriterFrozen)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "7").Error)
	require.NoError(t, ActivateMerchantStoreRefundSync(DB, 7))
	require.NoError(t, ActivateMerchantStoreRefundSync(DB, 8))
	require.NoError(t, DB.Migrator().DropTable(&MerchantStoreRefundProviderAttempt{}))
	require.ErrorIs(t, ActivateMerchantStoreRefundSync(DB, 8), ErrMerchantStoreWriterFrozen, "retry validates schema and does not recreate missing tables")
}

func TestMerchantStorePickupDenialsExplainAccountAndCodeWithoutWeakeningAccess(t *testing.T) {
	f, o, token := refundPaidOrder(t, "balance", 1)
	_, err := ClaimMerchantStoreOrder(token, "safe-pickup-code", f.seller.Id)
	require.ErrorIs(t, err, ErrMerchantStorePickupAccount)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ClaimMerchantStoreOrder(token, "not-the-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStorePickupCode)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	_, err = ClaimMerchantStoreOrder(strings.Repeat("x", 43), "not-the-code", f.buyer.Id)
	require.ErrorIs(t, err, ErrMerchantStoreDenied)
	require.NotErrorIs(t, err, ErrMerchantStorePickupCode)
	require.NoError(t, DB.First(o, "id = ?", o.ID).Error)
	require.Zero(t, o.ClaimedAt)
}
