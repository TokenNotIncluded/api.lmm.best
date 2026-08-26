package model

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSSMSConflictingReconcilersCannotReactivateRefundedOrder(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 815, 1_000_000)
	chargeQuota, err := heroSMSChargeQuota(decimal.RequireFromString("0.5"))
	require.NoError(t, err)
	snapshot, err := encryptHeroSMSSMSSnapshot([]herosms.SMSActiveActivation{})
	require.NoError(t, err)
	order := HeroSMSSMSOrder{
		UserID:                     user.Id,
		IdempotencyKeyHash:         fmt.Sprintf("%064d", 815),
		RequestPayloadHash:         fmt.Sprintf("%064d", 816),
		CountryID:                  6,
		Service:                    "tg",
		Status:                     HeroSMSSMSOrderStatusPurchaseUnknown,
		PriceMultiplier:            "1",
		ProviderPriceCNY:           "0.5",
		CustomerPriceUSD:           "0.5",
		ReservedQuota:              chargeQuota,
		ChargeQuota:                chargeQuota,
		ProviderSnapshotCiphertext: snapshot,
		ProviderRequestStartedAt:   time.Now().Add(-heroSMSSMSUnknownWindow - time.Second).Unix(),
	}
	_, err = reserveHeroSMSSMSQuota(&order)
	require.NoError(t, err)

	candidateStarted := make(chan struct{})
	releaseCandidate := make(chan struct{})
	var activeCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("action") != "getActiveActivations" {
			http.Error(writer, "unexpected action", http.StatusBadRequest)
			return
		}
		if activeCalls.Add(1) == 1 {
			close(candidateStarted)
			<-releaseCandidate
			_, _ = writer.Write([]byte(`{"data":[{"activationId":930,"serviceCode":"tg","phoneNumber":"79000000930","activationCost":0.5,"currency":840,"activationStatus":1,"activationTime":"2026-08-23 10:00:00","countryCode":6}]}`))
			return
		}
		_, _ = writer.Write([]byte(`{"data":[]}`))
	}))
	defer server.Close()
	client := herosms.NewClient(server.URL+"/api/v1", "secret")

	var wait sync.WaitGroup
	wait.Add(1)
	var candidateErr error
	go func() {
		defer wait.Done()
		_, candidateErr = reconcileHeroSMSSMSOrder(t.Context(), client, order.ID)
	}()
	<-candidateStarted
	emptyDone := make(chan error, 1)
	go func() {
		_, emptyErr := reconcileHeroSMSSMSOrder(t.Context(), client, order.ID)
		emptyDone <- emptyErr
	}()
	select {
	case <-emptyDone:
		t.Fatal("second reconciler bypassed the provider lease")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseCandidate)
	wait.Wait()
	require.NoError(t, candidateErr)
	require.NoError(t, <-emptyDone)

	var stored HeroSMSSMSOrder
	require.NoError(t, db.Where("id = ?", order.ID).First(&stored).Error)
	require.Equal(t, HeroSMSSMSOrderStatusActive, stored.Status)
	require.Zero(t, stored.RefundedQuota)
	require.Equal(t, user.Quota-stored.ChargeQuota, getUserQuotaValue(user.Id))
	require.EqualValues(t, 1, activeCalls.Load())
}

func TestHeroSMSSMSPurchaseReconcilesMalformedSuccessWithoutDoubleCharge(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 811, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	var activeCalls atomic.Int32
	var purchaseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "0.5", 1) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getActiveActivations":
			if activeCalls.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data":[{"activationId":916,"serviceCode":"tg","phoneNumber":"79000000916","activationCost":0.5,"currency":840,"activationStatus":1,"smsCode":null,"smsText":null,"activationTime":"2026-08-23 10:00:00","countryCode":6}]}`))
		case "getNumberV2":
			purchaseCalls.Add(1)
			_, _ = writer.Write([]byte(`{"activationId":916`))
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	order, quota, status, err := CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
		"sms-malformed-success-reconcile",
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSSMSOrderStatusActive, order.Status)
	require.Equal(t, "916", *order.ProviderID)
	require.Equal(t, user.Quota-order.ChargeQuota, quota)
	require.EqualValues(t, 1, purchaseCalls.Load())

	var reserves int64
	require.NoError(t, db.Model(&HeroSMSSMSQuotaLedger{}).
		Where("order_id = ? AND entry_type = ?", order.ID, HeroSMSSMSLedgerReserve).
		Count(&reserves).Error)
	require.EqualValues(t, 1, reserves)
}

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
		if writeHeroSMSTestOffer(writer, request, "0.5", 1) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":0.5,"count":1}}}`))
		case "getActiveActivations":
			if activeCalls.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"data":[]}`))
				return
			}
			_, _ = writer.Write([]byte(`{"data":[{"activationId":911,"serviceCode":"tg","phoneNumber":"79000000911","activationCost":0.5,"currency":840,"activationStatus":1,"smsCode":null,"smsText":null,"activationTime":"2026-08-23 10:00:00","countryCode":6}]}`))
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
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
