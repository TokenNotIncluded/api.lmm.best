package model

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSSMSComplaintRefundsOnlyAfterUpstreamCancellation(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 807, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	providerID := "920"
	order := HeroSMSSMSOrder{
		ID:                 "sms-complaint-order",
		UserID:             user.Id,
		IdempotencyKeyHash: "sms-complaint-idempotency",
		RequestPayloadHash: "sms-complaint-request",
		CountryID:          6,
		Service:            "tg",
		Status:             HeroSMSSMSOrderStatusActive,
		PriceMultiplier:    "1",
		ProviderPriceCNY:   "0.2",
		CustomerPriceUSD:   "0.2",
		ReservedQuota:      100_000,
		ChargeQuota:        100_000,
		ProviderID:         &providerID,
		CreatedAt:          time.Now().Add(-3 * time.Minute).Unix(),
		UpdatedAt:          time.Now().Unix(),
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota-order.ChargeQuota).Error)

	var complaintCalls atomic.Int32
	var providerRefunded atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/api/v1/complaints/activations/920" {
			complaintCalls.Add(1)
			var payload map[string]string
			require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
			require.Equal(t, map[string]string{"type": HeroSMSSMSComplaintNotReceived}, payload)
			writer.WriteHeader(http.StatusNoContent)
			return
		}
		switch request.URL.Query().Get("action") {
		case "getStatus":
			if providerRefunded.Load() {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
			} else {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
			}
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") },
		server.URL+"/api/v1",
	)
	defer restore()

	before, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.True(t, before.CanComplain)

	submitted, err := SubmitHeroSMSSMSComplaint(t.Context(), user.Id, order.ID, HeroSMSSMSComplaintNotReceived)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSComplaintStatusSubmitted, submitted.ComplaintStatus)
	require.EqualValues(t, 1, complaintCalls.Load())
	require.Equal(t, user.Quota-order.ChargeQuota, getUserQuotaValue(user.Id))

	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	pending, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusActive, pending.Status)
	require.Zero(t, pending.RefundedQuota)
	require.Equal(t, user.Quota-order.ChargeQuota, getUserQuotaValue(user.Id))

	providerRefunded.Store(true)
	processed, err = RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	refunded, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, refunded.Status)
	require.Equal(t, HeroSMSSMSComplaintStatusClosedRefund, refunded.ComplaintStatus)
	require.Equal(t, order.ChargeQuota, refunded.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSComplaintAmbiguityDoesNotRetryOrRefund(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 809, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))
	providerID := "921"
	order := HeroSMSSMSOrder{
		ID: "sms-complaint-unknown", UserID: user.Id,
		IdempotencyKeyHash: "sms-complaint-unknown-idempotency", RequestPayloadHash: "sms-complaint-unknown-request",
		CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusActive,
		PriceMultiplier: "1", ProviderPriceCNY: "0.2", CustomerPriceUSD: "0.2",
		ReservedQuota: 100_000, ChargeQuota: 100_000, ProviderID: &providerID,
		CreatedAt: time.Now().Add(-3 * time.Minute).Unix(), UpdatedAt: time.Now().Unix(),
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota-order.ChargeQuota).Error)

	var complaintCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/api/v1/complaints/activations/921" {
			complaintCalls.Add(1)
			http.Error(writer, "upstream unavailable", http.StatusBadGateway)
			return
		}
		switch request.URL.Query().Get("action") {
		case "getStatus":
			_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") },
		server.URL+"/api/v1",
	)
	defer restore()

	_, err := SubmitHeroSMSSMSComplaint(t.Context(), user.Id, order.ID, HeroSMSSMSComplaintNotReceived)
	require.Error(t, err)
	unknown, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSComplaintStatusSubmitUnknown, unknown.ComplaintStatus)
	require.Zero(t, unknown.RefundedQuota)

	replayed, err := SubmitHeroSMSSMSComplaint(t.Context(), user.Id, order.ID, HeroSMSSMSComplaintNotReceived)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSComplaintStatusSubmitUnknown, replayed.ComplaintStatus)
	require.EqualValues(t, 1, complaintCalls.Load())
	require.Equal(t, user.Quota-order.ChargeQuota, getUserQuotaValue(user.Id))

	require.NoError(t, db.Model(&HeroSMSSMSOrder{}).Where("id = ?", order.ID).UpdateColumn("complaint_next_retry_at", time.Now().Add(-time.Second).Unix()).Error)
	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	retried, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSComplaintStatusSubmitUnknown, retried.ComplaintStatus)
	require.EqualValues(t, 2, complaintCalls.Load())
	require.Zero(t, retried.RefundedQuota)
	require.Equal(t, user.Quota-order.ChargeQuota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSCancellationIsNotStarvedByOldComplaints(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 810, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled: ptrBool(true), SMSEnabled: ptrBool(true), APIKey: "test-secret-key-12345", PriceMultiplier: "1",
	}))
	now := time.Now().Unix()
	orders := make([]HeroSMSSMSOrder, 0, 22)
	for index := 0; index < 21; index++ {
		providerID := fmt.Sprintf("old-complaint-%02d", index)
		orders = append(orders, HeroSMSSMSOrder{
			ID: fmt.Sprintf("old-complaint-order-%02d", index), UserID: user.Id,
			IdempotencyKeyHash: fmt.Sprintf("old-complaint-key-%02d", index), RequestPayloadHash: "request",
			CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusActive,
			PriceMultiplier: "1", ProviderPriceCNY: "0.1", CustomerPriceUSD: "0.1",
			ProviderID: &providerID, ComplaintType: HeroSMSSMSComplaintNotReceived,
			ComplaintStatus: HeroSMSSMSComplaintStatusSubmitted, ComplaintSubmittedAt: now - 3600,
			CreatedAt: now - 3600, UpdatedAt: now - 3600 + int64(index),
		})
	}
	cancelProviderID := "priority-cancel"
	orders = append(orders, HeroSMSSMSOrder{
		ID: "priority-cancel-order", UserID: user.Id,
		IdempotencyKeyHash: "priority-cancel-key", RequestPayloadHash: "request",
		CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusCancelPending,
		PriceMultiplier: "1", ProviderPriceCNY: "0.2", CustomerPriceUSD: "0.2",
		ReservedQuota: 100_000, ChargeQuota: 100_000, ProviderID: &cancelProviderID,
		ProviderCancelAcceptedAt: now - 1, CancelFinalStatus: HeroSMSSMSOrderStatusCancelled,
		CancelErrorCode: "USER_CANCELLED", CreatedAt: now, UpdatedAt: now,
	})
	require.NoError(t, db.Create(&orders).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota-100_000).Error)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getStatus":
			if request.URL.Query().Get("id") == cancelProviderID {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
			} else {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
			}
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") }, server.URL+"/api/v1",
	)
	defer restore()

	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	require.Equal(t, 20, processed)
	cancelled, err := GetHeroSMSSMSOrder("priority-cancel-order", user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, cancelled.Status)
	require.Equal(t, 100_000, cancelled.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSComplaintErrorsStillRotatePollingQueue(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 811, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled: ptrBool(true), SMSEnabled: ptrBool(true), APIKey: "test-secret-key-12345", PriceMultiplier: "1",
	}))
	now := time.Now().Unix()
	orders := make([]HeroSMSSMSOrder, 0, 21)
	for index := 0; index < 20; index++ {
		providerID := fmt.Sprintf("broken-complaint-%02d", index)
		orders = append(orders, HeroSMSSMSOrder{
			ID: fmt.Sprintf("broken-complaint-order-%02d", index), UserID: user.Id,
			IdempotencyKeyHash: fmt.Sprintf("broken-complaint-key-%02d", index), RequestPayloadHash: "request",
			CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusActive,
			PriceMultiplier: "1", ProviderPriceCNY: "0.1", CustomerPriceUSD: "0.1", ProviderID: &providerID,
			ComplaintType: HeroSMSSMSComplaintNotReceived, ComplaintStatus: HeroSMSSMSComplaintStatusSubmitted,
			ComplaintSubmittedAt: now - 3600, CreatedAt: now - 3600, UpdatedAt: now - 3600 + int64(index),
		})
	}
	targetProviderID := "fairness-target"
	orders = append(orders, HeroSMSSMSOrder{
		ID: "fairness-target-order", UserID: user.Id,
		IdempotencyKeyHash: "fairness-target-key", RequestPayloadHash: "request",
		CountryID: 6, Service: "tg", Status: HeroSMSSMSOrderStatusActive,
		PriceMultiplier: "1", ProviderPriceCNY: "0.1", CustomerPriceUSD: "0.1", ProviderID: &targetProviderID,
		ComplaintType: HeroSMSSMSComplaintNotReceived, ComplaintStatus: HeroSMSSMSComplaintStatusSubmitted,
		ComplaintSubmittedAt: now - 1800, CreatedAt: now - 1800, UpdatedAt: now - 1800,
	})
	require.NoError(t, db.Create(&orders).Error)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getStatus":
			if request.URL.Query().Get("id") == targetProviderID {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
			} else {
				_, _ = writer.Write([]byte("NO_ACTIVATION"))
			}
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		default:
			http.Error(writer, "unexpected request", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") }, server.URL+"/api/v1",
	)
	defer restore()

	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	require.Zero(t, processed)
	processed, err = RunHeroSMSSMSReconciliationOnce(t.Context(), 20)
	require.NoError(t, err)
	require.Positive(t, processed)
	var target HeroSMSSMSOrder
	require.NoError(t, db.Where("id = ?", "fairness-target-order").First(&target).Error)
	require.Positive(t, target.ComplaintLastCheckedAt)
}
