package model

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/common"
	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSSMSOrderLookupReconciliationRejectsMissingAndOtherOrders(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	caller := createHeroSMSTestUser(t, db, 9023, common.GetTrustQuota())
	owner := createHeroSMSTestUser(t, db, 9024, common.GetTrustQuota())
	order := HeroSMSSMSOrder{
		ID: "sms-reconcile-other-user-order", UserID: owner.Id,
		IdempotencyKeyHash: "sms-reconcile-idempotency", RequestPayloadHash: "sms-reconcile-payload",
		Status: HeroSMSSMSOrderStatusCancelPending, Service: "tg", CustomerPriceUSD: "1", ChargeQuota: 1,
	}
	require.NoError(t, db.Create(&order).Error)
	var beforeUsers []User
	var beforeOrders []HeroSMSSMSOrder
	require.NoError(t, db.Order("id").Find(&beforeUsers).Error)
	require.NoError(t, db.Order("id").Find(&beforeOrders).Error)
	var providerCalls atomic.Int32
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		providerCalls.Add(1)
		w.WriteHeader(http.StatusBadRequest)
	}))
	t.Cleanup(provider.Close)
	client := herosms.NewClient(provider.URL, "fixture")
	for _, reconcile := range []struct {
		name string
		run  func(string) error
	}{
		{name: "cancellation", run: func(id string) error { return reconcileHeroSMSSMSCancellation(t.Context(), client, id, caller.Id) }},
		{name: "complaint", run: func(id string) error { return reconcileHeroSMSSMSComplaint(t.Context(), client, id, caller.Id) }},
	} {
		for _, orderID := range []string{"sms-reconcile-missing-order", order.ID} {
			t.Run(reconcile.name+"/"+orderID, func(t *testing.T) {
				err := reconcile.run(orderID)
				var afterUsers []User
				var afterOrders []HeroSMSSMSOrder
				require.NoError(t, db.Order("id").Find(&afterUsers).Error)
				require.NoError(t, db.Order("id").Find(&afterOrders).Error)
				require.Equal(t, beforeUsers, afterUsers)
				require.Equal(t, beforeOrders, afterOrders)
				var ledgerCount int64
				require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).Count(&ledgerCount).Error)
				require.Zero(t, ledgerCount)
				require.Zero(t, providerCalls.Load())
				var apiErr *HeroSMSError
				require.ErrorAs(t, err, &apiErr)
				require.Equal(t, http.StatusNotFound, apiErr.Status)
				require.Equal(t, "ORDER_NOT_FOUND", apiErr.Code)
				require.Equal(t, "HeroSMS SMS order not found", apiErr.Message)
			})
		}
	}
}
