package model

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

func refundDispatchFixture(t *testing.T) (storeFixture, *MerchantStoreOrder, *MerchantStoreRefund) {
	f, o, _ := refundPaidOrder(t, "external:waffo_pancake", 1)
	require.NoError(t, RecordMerchantStoreRefundPaymentBasis(o.ID, VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: o.ProviderTradeID, PaymentReference: "PAY_AbCdEfGhIjKlMnOpQrStUv", AmountMinor: 3, Currency: "USD", EvidenceHash: strings.Repeat("a", 64)}))
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("provider"))
	require.NoError(t, e)
	r = refundApprove(t, f.seller.Id, o, r)
	return f, o, r
}

func TestMerchantStoreRefundDispatchOneShotDurableCrashAndLease(t *testing.T) {
	_, o, r := refundDispatchFixture(t)
	d, e := PrepareMerchantStoreRefundDispatch(r.ID, 100)
	require.NoError(t, e)
	require.NotNil(t, d)
	second, e := PrepareMerchantStoreRefundDispatch(r.ID, 100)
	require.NoError(t, e)
	require.Nil(t, second)
	var successful int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			yes, err := ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, 101)
			if err != nil {
				t.Errorf("claim: %v", err)
			}
			if yes {
				mu.Lock()
				successful++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	require.Equal(t, 1, successful)
	var persisted MerchantStoreRefundProviderAttempt
	require.NoError(t, DB.First(&persisted, "refund_id = ?", r.ID).Error)
	require.Equal(t, 1, persisted.SubmitCount)
	require.Equal(t, "unknown", persisted.State)
	// Crash immediately after CAS: no response and no observation save. A later
	// backend may take the expired query lease, but never a second submit.
	recovered, e := PrepareMerchantStoreRefundDispatch(r.ID, 192)
	require.NoError(t, e)
	require.NotNil(t, recovered)
	yes, e := ClaimMerchantStoreRefundSubmit(r.ID, recovered.Attempt.LeaseToken, recovered.Attempt.RequestHash, 193)
	require.NoError(t, e)
	require.False(t, yes)
	var order MerchantStoreOrder
	require.NoError(t, DB.First(&order, "id = ?", o.ID).Error)
	require.Equal(t, "refund_pending", order.Status)
	require.NoError(t, RecordMerchantStoreRefundObservation(r.ID, recovered.Attempt.LeaseToken, recovered.Attempt.RequestHash, "pending", "ticket_requested", "TKT_fixture", "", "", 194))
	require.ErrorIs(t, RecordMerchantStoreRefundObservation(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, "unknown", "old_worker", "", "", "", 195), ErrMerchantStoreConflict)
	encoded, e := json.Marshal(persisted)
	require.NoError(t, e)
	require.JSONEq(t, `{}`, string(encoded))
}

func TestMerchantStoreRefundDispatchGuardsImmutableNativeAndCapability(t *testing.T) {
	f, o, r := refundDispatchFixture(t)
	require.NoError(t, AuthorizeMerchantStoreRefundReconciliation(f.seller.Id, o.ID, r.ID))
	require.NoError(t, AuthorizeMerchantStoreRefundReconciliation(f.root.Id, o.ID, r.ID))
	require.ErrorIs(t, AuthorizeMerchantStoreRefundReconciliation(f.buyer.Id, o.ID, r.ID), ErrMerchantStoreDenied)
	d, e := PrepareMerchantStoreRefundDispatch(r.ID, 100)
	require.NoError(t, e)
	require.NotNil(t, d)
	_, e = ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, strings.Repeat("f", 64), 101)
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
	require.NoError(t, DB.Model(&Option{}).Where("key = ?", MerchantStoreWriterCapabilityOption).Update("value", "3").Error)
	_, e = ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, 101)
	require.ErrorIs(t, e, ErrMerchantStoreWriterFrozen)
	ids, e := DueMerchantStoreRefundDispatches(context.Background(), 200, 5)
	require.NoError(t, e)
	require.Empty(t, ids)
	refundActivate(t)
	require.NoError(t, DB.Model(r).Update("amount_minor", 2).Error)
	_, e = ClaimMerchantStoreRefundSubmit(r.ID, d.Attempt.LeaseToken, d.Attempt.RequestHash, 101)
	require.ErrorIs(t, e, ErrMerchantStoreConflict)
}

func TestMerchantStoreRefundDispatchQueueExcludesMissingOriginalBasis(t *testing.T) {
	f, o, _ := refundPaidOrder(t, "external:waffo_pancake", 1)
	r, e := RequestMerchantStoreRefund(f.buyer.Id, o.ID, refundFull("no-basis"))
	require.NoError(t, e)
	refundApprove(t, f.seller.Id, o, r)
	_, e = PrepareMerchantStoreRefundDispatch(r.ID, 100)
	require.ErrorIs(t, e, ErrMerchantStoreRefundUnsupported)
	ids, e := DueMerchantStoreRefundDispatches(context.Background(), 200, 5)
	require.NoError(t, e)
	require.Empty(t, ids)
	require.NoError(t, RecordMerchantStoreRefundPaymentBasis(o.ID, VerifiedMerchantStoreRefundPaymentBasis{ReceiptReference: o.ProviderTradeID, PaymentReference: "PAY_AbCdEfGhIjKlMnOpQrStUv", AmountMinor: 3, Currency: "USD", EvidenceHash: strings.Repeat("a", 64)}))
	ids, e = DueMerchantStoreRefundDispatches(context.Background(), 200, 5)
	require.NoError(t, e)
	require.Equal(t, []string{r.ID}, ids)
}
