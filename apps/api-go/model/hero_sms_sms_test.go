package model

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/LIghtJUNction/api.lmm.best/service/herosms"
	"github.com/stretchr/testify/require"
)

func TestHeroSMSSMSPurchaseRefreshAndPricing(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 801, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		EmailEnabled:    ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "2",
	}))

	var statusReady atomic.Bool
	var purchaseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		require.Equal(t, "/stubs/handler_api.php", request.URL.Path)
		switch request.URL.Query().Get("action") {
		case "getCountries":
			_, _ = writer.Write([]byte(`{"6":{"rus":"Россия","eng":"Russia","chn":"俄罗斯","visible":1}}`))
		case "getServicesList":
			_, _ = writer.Write([]byte(`{"tg":"Telegram"}`))
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":4}}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			purchaseCalls.Add(1)
			_, _ = writer.Write([]byte(`{"activationId":909,"phoneNumber":"79001234567","activationCost":1,"currencyCode":643,"countryCode":6,"canGetAnotherSms":false,"activationTime":"2026-08-23T07:00:00+00:00","activationEndTime":"2026-08-23T07:20:00+00:00","activationOperator":"any"}`))
		case "getStatusV2":
			if statusReady.Load() {
				_, _ = writer.Write([]byte(`{"sms":{"code":"123456","text":"Code: 123456"}}`))
			} else {
				_, _ = writer.Write([]byte(`{"sms":null}`))
			}
		case "setStatus":
			_, _ = writer.Write([]byte("ACCESS_ACTIVATION"))
		default:
			http.Error(writer, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") },
		server.URL+"/api/v1",
	)
	defer restore()

	countries, err := GetHeroSMSSMSCountries(t.Context())
	require.NoError(t, err)
	require.Equal(t, []HeroSMSSMSCountryView{{ID: 6, Name: "俄罗斯"}}, countries)
	services, err := GetHeroSMSSMSServices(t.Context())
	require.NoError(t, err)
	require.Equal(t, []HeroSMSSMSServiceView{{Code: "tg", Name: "Telegram"}}, services)
	offer, err := GetHeroSMSSMSOffer(t.Context(), 6, "tg", "any")
	require.NoError(t, err)
	require.Equal(t, "2", offer.CustomerPriceUSD)
	require.Positive(t, offer.ChargeQuota)
	publicPayload, err := json.Marshal(offer)
	require.NoError(t, err)
	for _, internalField := range []string{"provider_price_cny", "price_multiplier"} {
		require.NotContains(t, string(publicPayload), `"`+internalField+`"`)
	}

	order, quota, status, err := CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
		"sms-purchase-1",
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSSMSOrderStatusActive, order.Status)
	require.Equal(t, "79001234567", order.PhoneNumber)
	require.Equal(t, user.Quota-order.ChargeQuota, quota)
	publicPayload, err = json.Marshal(order)
	require.NoError(t, err)
	require.NotContains(t, string(publicPayload), `"provider_price_cny"`)

	replayed, _, _, err := CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
		"sms-purchase-1",
	)
	require.NoError(t, err)
	require.Equal(t, order.ID, replayed.ID)
	require.EqualValues(t, 1, purchaseCalls.Load())

	statusReady.Store(true)
	completed, err := RefreshHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCompleted, completed.Status)
	require.Equal(t, "123456", completed.Code)
	require.Equal(t, "Code: 123456", completed.Message)
}

func TestHeroSMSSMSCancellationRefundsReservedQuota(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 802, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":1}}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			_, _ = writer.Write([]byte(`{"activationId":910,"phoneNumber":"79007654321","activationCost":0.5,"currencyCode":643,"countryCode":6,"canGetAnotherSms":false}`))
		case "setStatus":
			_, _ = writer.Write([]byte("ACCESS_CANCEL"))
		default:
			_ = json.NewEncoder(writer).Encode(map[string]any{"sms": nil})
		}
	}))
	defer server.Close()
	restore := SetHeroSMSClientFactoryForTest(
		func(_ string, _ string) herosms.Client { return herosms.NewClient(server.URL+"/api/v1", "secret") },
		server.URL+"/api/v1",
	)
	defer restore()

	offer, err := GetHeroSMSSMSOffer(t.Context(), 6, "tg", "")
	require.NoError(t, err)
	order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, "sms-cancel-1")
	require.NoError(t, err)
	cancelled, quota, err := CancelHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, cancelled.Status)
	require.Equal(t, 250_000, cancelled.ChargeQuota)
	require.Equal(t, 500_000, cancelled.RefundedQuota)
	require.Equal(t, user.Quota, quota)
}
