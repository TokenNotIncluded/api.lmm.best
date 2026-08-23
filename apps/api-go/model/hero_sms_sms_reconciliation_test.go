package model

import (
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSSMSPurchaseReconcilesTimeoutWithoutDoubleCharge(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 803, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	var activeCalls atomic.Int32
	var purchaseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":1}}}`))
		case "getActiveActivations":
			if activeCalls.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data":[{"activationId":911,"serviceCode":"tg","phoneNumber":"79000000911","activationCost":0.5,"currency":643,"activationStatus":1,"smsCode":null,"smsText":null,"activationTime":"2026-08-23 10:00:00","countryCode":6}]}`))
		case "getNumberV2":
			purchaseCalls.Add(1)
			http.Error(writer, "temporary gateway timeout", http.StatusGatewayTimeout)
		default:
			http.Error(writer, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client {
			return herosms.NewClient(server.URL+"/api/v1", "secret")
		},
		server.URL+"/api/v1",
	)
	defer restore()

	offer, err := GetHeroSMSSMSOffer(t.Context(), 6, "tg", "")
	require.NoError(t, err)
	order, quota, status, err := CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
		"sms-timeout-reconcile",
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSSMSOrderStatusActive, order.Status)
	require.Equal(t, "911", *order.ProviderID)
	require.Equal(t, "79000000911", order.PhoneNumber)
	require.Equal(t, user.Quota-order.ChargeQuota, quota)
	require.EqualValues(t, 1, purchaseCalls.Load())

	var reserves int64
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).
		Where("order_id = ? AND entry_type = ?", order.ID, HeroSMSSMSLedgerReserve).
		Count(&reserves).Error)
	require.EqualValues(t, 1, reserves)
}
