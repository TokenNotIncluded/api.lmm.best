package model

import (
	"encoding/json"
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

func writeHeroSMSTestOffer(writer http.ResponseWriter, request *http.Request, price string, count int) bool {
	if request.URL.Path != "/api/v1/activations/offers/sms" {
		return false
	}
	_, _ = fmt.Fprintf(writer, `{"data":{"tg":{"6":{"counts":{"total":%d,"defaultPrice":%d},"prices":{"default":%s},"map":{"%s":%d}}}}}`, count, count, price, price, count)
	return true
}

func TestHeroSMSSMSReconciliationCandidateMatchesRequestBoundary(t *testing.T) {
	started := time.Now().Unix()
	order := HeroSMSSMSOrder{
		CountryID:                6,
		Service:                  "tg",
		Operator:                 "operator-a",
		ProviderPriceCNY:         "0.5",
		ProviderRequestStartedAt: started,
	}
	candidate := herosms.SMSActiveActivation{
		ID:                 "candidate",
		Service:            "tg",
		PhoneNumber:        "79000000000",
		ActivationCost:     decimal.RequireFromString("0.4"),
		CurrencyCode:       840,
		CountryCode:        6,
		ActivationOperator: "operator-a",
		ActivationTime:     time.Unix(started, 0).UTC().Format(time.RFC3339),
	}
	require.True(t, heroSMSSMSReconciliationCandidate(&order, &candidate))

	wrongPrice := candidate
	wrongPrice.ActivationCost = decimal.RequireFromString("0.6")
	require.False(t, heroSMSSMSReconciliationCandidate(&order, &wrongPrice))
	wrongCurrency := candidate
	wrongCurrency.CurrencyCode = 643
	require.False(t, heroSMSSMSReconciliationCandidate(&order, &wrongCurrency))
	wrongOperator := candidate
	wrongOperator.ActivationOperator = "operator-b"
	require.False(t, heroSMSSMSReconciliationCandidate(&order, &wrongOperator))
	stale := candidate
	stale.ActivationTime = time.Unix(started, 0).Add(-time.Minute).UTC().Format(time.RFC3339)
	require.False(t, heroSMSSMSReconciliationCandidate(&order, &stale))
}

func TestHeroSMSSMSBidPriceBounds(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1e1000000", "1000001", "0.0000001", "1.2.3"} {
		_, valid := parseHeroSMSSMSBidPrice(value)
		require.False(t, valid, value)
	}
	for _, value := range []string{".5", "1", "1.000001", "1000000"} {
		price, valid := parseHeroSMSSMSBidPrice(value)
		require.True(t, valid, value)
		require.True(t, price.GreaterThan(decimal.Zero))
	}
}

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
		if writeHeroSMSTestOffer(writer, request, "1", 4) {
			return
		}
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
			_, _ = writer.Write([]byte(`{"activationId":909,"phoneNumber":"79001234567","activationCost":1,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false,"activationTime":"2026-08-23T07:00:00+00:00","activationEndTime":"2099-08-23T07:20:00+00:00","activationOperator":"any"}`))
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

	countries, err := GetHeroSMSSMSCountries(t.Context(), "")
	require.NoError(t, err)
	require.Equal(t, []HeroSMSSMSCountryView{{
		ID: 6, Name: "俄罗斯", EnglishName: "Russia", ChineseName: "俄罗斯", Popularity: 0,
	}}, countries)
	services, err := GetHeroSMSSMSServices(t.Context())
	require.NoError(t, err)
	require.Equal(t, []HeroSMSSMSServiceView{{Code: "tg", Name: "Telegram", Popularity: 0}}, services)
	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "any")
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

	current, err := ListCurrentHeroSMSSMSOrders(t.Context(), user.Id)
	require.NoError(t, err)
	require.Len(t, current, 1)
	require.Equal(t, "123456", current[0].Code)

	summaries, err := ListHeroSMSSMSOrderSummaries(user.Id, 1, 20)
	require.NoError(t, err)
	require.Len(t, summaries.Items, 1)
	require.Equal(t, "79001234567", summaries.Items[0].PhoneNumber)
	require.Empty(t, summaries.Items[0].Code)
	require.Empty(t, summaries.Items[0].Message)
	require.Nil(t, summaries.Items[0].ProviderID)
}

func TestHeroSMSSMSHistoryCleanupKeepsAuditRowsAndRejectsActiveOrders(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 806, 2_000_000)
	now := int64(1_777_000_000)
	orders := []HeroSMSSMSOrder{
		{ID: "hssms_history_completed", UserID: user.Id, IdempotencyKeyHash: "history-completed", Status: HeroSMSSMSOrderStatusCompleted, CreatedAt: now},
		{ID: "hssms_history_failed", UserID: user.Id, IdempotencyKeyHash: "history-failed", Status: HeroSMSSMSOrderStatusFailed, CreatedAt: now - 1},
		{ID: "hssms_history_active", UserID: user.Id, IdempotencyKeyHash: "history-active", Status: HeroSMSSMSOrderStatusActive, CreatedAt: now - 2},
	}
	require.NoError(t, db.Create(&orders).Error)
	otherUser := createHeroSMSTestUser(t, db, 809, 2_000_000)
	otherErr := HideHeroSMSSMSOrderFromHistory(otherUser.Id, orders[0].ID)
	var notFound *HeroSMSError
	require.ErrorAs(t, otherErr, &notFound)
	require.Equal(t, http.StatusNotFound, notFound.Status)

	err := HideHeroSMSSMSOrderFromHistory(user.Id, orders[2].ID)
	var apiErr *HeroSMSError
	require.ErrorAs(t, err, &apiErr)
	require.Equal(t, http.StatusConflict, apiErr.Status)
	require.Equal(t, "ORDER_ACTIVE", apiErr.Code)

	require.NoError(t, HideHeroSMSSMSOrderFromHistory(user.Id, orders[1].ID))
	require.NoError(t, HideHeroSMSSMSOrderFromHistory(user.Id, orders[1].ID))
	page, err := ListHeroSMSSMSOrderSummaries(user.Id, 1, 20)
	require.NoError(t, err)
	require.Equal(t, int64(1), page.Total)
	require.Equal(t, orders[0].ID, page.Items[0].ID)

	hidden, err := ClearHeroSMSSMSOrderHistory(user.Id)
	require.NoError(t, err)
	require.Equal(t, int64(1), hidden)
	page, err = ListHeroSMSSMSOrderSummaries(user.Id, 1, 20)
	require.NoError(t, err)
	require.Zero(t, page.Total)

	var persisted []HeroSMSSMSOrder
	require.NoError(t, db.Order("id").Find(&persisted).Error)
	require.Len(t, persisted, 3)
	for _, order := range persisted {
		if order.Status == HeroSMSSMSOrderStatusActive {
			require.Zero(t, order.HistoryHiddenAt)
		} else {
			require.Positive(t, order.HistoryHiddenAt)
		}
	}
}

func TestHeroSMSSMSExpiredActivationWaitsForUpstreamCancellationBeforeRefund(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 810, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))
	phone, err := encryptHeroSMSPayload("56972498825")
	require.NoError(t, err)
	providerID := "expired-provider-id"
	charge := 12_345
	order := HeroSMSSMSOrder{
		UserID:             user.Id,
		IdempotencyKeyHash: "expired-activation",
		CountryID:          33,
		Service:            "wa",
		Status:             HeroSMSSMSOrderStatusActive,
		ProviderID:         &providerID,
		PhoneCiphertext:    phone,
		ReservedQuota:      charge,
		ChargeQuota:        charge,
		ProviderExpiresAt:  time.Now().Add(-time.Minute).Unix(),
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota-charge).Error)

	var setStatusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getStatus":
			_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
		case "setStatus":
			setStatusCalls.Add(1)
			_, _ = writer.Write([]byte("ACCESS_CANCEL"))
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

	view, err := RefreshHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, view.Status)
	require.Equal(t, "ACTIVATION_EXPIRED", view.LastErrorCode)
	require.Equal(t, charge, view.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
	require.Zero(t, setStatusCalls.Load(), "an already-cancelled upstream activation must not be cancelled again")

}

func TestHeroSMSSMSLegacyActivationUsesExplicitProviderCancellation(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 812, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))
	phone, err := encryptHeroSMSPayload("56972498825")
	require.NoError(t, err)
	providerID := "legacy-provider-id"
	charge := 12_345
	order := HeroSMSSMSOrder{
		UserID:             user.Id,
		IdempotencyKeyHash: "legacy-activation",
		CountryID:          33,
		Service:            "wa",
		Status:             HeroSMSSMSOrderStatusActive,
		ProviderID:         &providerID,
		PhoneCiphertext:    phone,
		ReservedQuota:      charge,
		ChargeQuota:        charge,
	}
	require.NoError(t, db.Create(&order).Error)
	require.NoError(t, db.Model(&User{}).Where("id = ?", user.Id).UpdateColumn("quota", user.Quota-charge).Error)

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		case "getStatus":
			_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
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

	view, err := RefreshHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, view.Status)
	require.Equal(t, "PROVIDER_CANCELLED", view.LastErrorCode)
	require.Equal(t, charge, view.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSPriceTiersOperatorsAndBidRefund(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 807, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "2",
	}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/activations/offers/sms" {
			_, _ = writer.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":5,"defaultPrice":3},"prices":{"default":1},"map":{"1":3,"0.5":2}}}}}`))
			return
		}
		switch request.URL.Query().Get("action") {
		case "getOperators":
			_, _ = writer.Write([]byte(`{"status":"success","countryOperators":{"6":["vodafone","optus"]}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			require.Equal(t, "840", request.URL.Query().Get("currency"))
			require.Equal(t, "vodafone", request.URL.Query().Get("operator"))
			require.Equal(t, "0.75", request.URL.Query().Get("maxPrice"))
			require.Empty(t, request.URL.Query().Get("fixedPrice"))
			_, _ = writer.Write([]byte(`{"activationId":915,"phoneNumber":"79005556677","activationCost":0.5,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false,"activationOperator":"vodafone"}`))
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

	operators, err := ListHeroSMSSMSOperators(t.Context(), 6)
	require.NoError(t, err)
	require.Equal(t, []string{"optus", "vodafone"}, operators)

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "VODAFONE")
	require.NoError(t, err)
	require.False(t, offer.Bid)
	require.Equal(t, "vodafone", offer.Operator)
	require.Equal(t, "1", offer.CustomerPriceUSD)
	require.Equal(t, 2, offer.Inventory)
	require.Len(t, offer.Tiers, 2)
	require.Equal(t, "1", offer.Tiers[0].CustomerPriceUSD)
	require.Equal(t, 2, offer.Tiers[0].Inventory)
	require.Equal(t, "2", offer.Tiers[1].CustomerPriceUSD)
	require.Equal(t, 3, offer.Tiers[1].Inventory)
	require.NotEqual(t, offer.Tiers[0].ID, offer.Tiers[1].ID)

	_, err = GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "not-an-operator")
	require.Error(t, err)

	bid, err := GetHeroSMSSMSBidOffer(t.Context(), user.Id, 6, "tg", "VODAFONE", "1.5")
	require.NoError(t, err)
	require.True(t, bid.Bid)
	require.Equal(t, "1.5", bid.CustomerPriceUSD)
	require.Equal(t, 2, bid.Inventory)

	_, _, _, err = CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id+1,
		HeroSMSSMSPurchaseRequest{OfferID: bid.ID},
		"sms-bid-cross-user",
	)
	require.Error(t, err)

	order, quota, status, err := CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: bid.ID},
		"sms-bid-1",
	)
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, status)
	require.Equal(t, HeroSMSSMSOrderStatusActive, order.Status)
	require.Equal(t, "1", order.CustomerPriceUSD)
	require.Positive(t, order.RefundedQuota)
	require.Equal(t, user.Quota-order.ChargeQuota, quota)
}

func TestHeroSMSSMSRejectsUnsafeProviderSettlement(t *testing.T) {
	tests := []struct {
		name           string
		userID         int
		activationCost string
		currencyCode   int
		cancelSucceeds bool
	}{
		{
			name:           "currency mismatch waits for upstream refund confirmation",
			userID:         812,
			activationCost: "0.5",
			currencyCode:   643,
			cancelSucceeds: true,
		},
		{
			name:           "price above cap waits for upstream refund confirmation",
			userID:         813,
			activationCost: "0.6",
			currencyCode:   840,
			cancelSucceeds: true,
		},
		{
			name:           "failed cancellation keeps quota reserved",
			userID:         814,
			activationCost: "0.5",
			currencyCode:   643,
			cancelSucceeds: false,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			db := setupHeroSMSTestDB(t)
			user := createHeroSMSTestUser(t, db, test.userID, 1_000_000)
			require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
				Enabled:         ptrBool(true),
				SMSEnabled:      ptrBool(true),
				APIKey:          "test-secret-key-12345",
				PriceMultiplier: "1",
			}))

			var cancelCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				if writeHeroSMSTestOffer(writer, request, "0.5", 1) {
					return
				}
				switch request.URL.Query().Get("action") {
				case "getActiveActivations":
					_, _ = writer.Write([]byte(`{"data":[]}`))
				case "getNumberV2":
					_, _ = fmt.Fprintf(writer, `{"activationId":920,"phoneNumber":"79000000920","activationCost":%s,"currencyCode":%d,"countryCode":6,"canGetAnotherSms":false}`, test.activationCost, test.currencyCode)
				case "getStatus":
					if test.cancelSucceeds && cancelCalls.Load() > 0 {
						_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
					} else {
						_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
					}
				case "getStatusV2":
					_, _ = writer.Write([]byte(`{"sms":null}`))
				case "setStatus":
					cancelCalls.Add(1)
					if test.cancelSucceeds {
						_, _ = writer.Write([]byte(`ACCESS_CANCEL`))
						return
					}
					http.Error(writer, "cancel unavailable", http.StatusBadGateway)
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

			offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
			require.NoError(t, err)
			_, _, _, err = CreateHeroSMSSMSOrder(
				t.Context(),
				user.Id,
				HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
				"sms-unsafe-settlement-"+test.name,
			)
			require.Error(t, err)
			require.EqualValues(t, 1, cancelCalls.Load())

			var order HeroSMSSMSOrder
			require.NoError(t, db.Where("user_id = ?", user.Id).First(&order).Error)
			require.Equal(t, HeroSMSSMSOrderStatusCancelPending, order.Status)
			require.Equal(t, HeroSMSSMSOrderStatusFailed, order.CancelFinalStatus)
			require.Equal(t, user.Quota-order.ChargeQuota, getUserQuotaValue(user.Id))
			require.Zero(t, order.RefundedQuota)
			if test.cancelSucceeds {
				require.Positive(t, order.ProviderCancelAcceptedAt)
				processed, reconcileErr := RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
				require.NoError(t, reconcileErr)
				require.Equal(t, 1, processed)
				require.NoError(t, db.Where("id = ?", order.ID).First(&order).Error)
				require.Equal(t, HeroSMSSMSOrderStatusFailed, order.Status)
				require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
				require.Equal(t, order.ChargeQuota, order.RefundedQuota)
			} else {
				require.Zero(t, order.ProviderCancelAcceptedAt)
			}
		})
	}
}

func TestHeroSMSSMSOperatorRemovalStopsBeforeReservation(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 810, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	var operatorCalls atomic.Int32
	var purchaseCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "0.5", 2) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getOperators":
			if operatorCalls.Add(1) == 1 {
				_, _ = writer.Write([]byte(`{"status":"success","countryOperators":{"6":["vodafone"]}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"status":"success","countryOperators":{"6":["optus"]}}`))
		case "getNumberV2":
			purchaseCalls.Add(1)
			http.Error(writer, "purchase should not run", http.StatusInternalServerError)
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "vodafone")
	require.NoError(t, err)
	_, _, _, err = CreateHeroSMSSMSOrder(
		t.Context(),
		user.Id,
		HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
		"sms-operator-removed",
	)
	require.Error(t, err)
	require.Zero(t, purchaseCalls.Load())
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSConcurrentIdempotentRetryPurchasesOnce(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 804, 2_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	activeStarted := make(chan struct{})
	releaseActive := make(chan struct{})
	secondIdempotencyMiss := make(chan struct{})
	var activeOnce sync.Once
	var purchaseCalls atomic.Int32
	var idempotencyMisses atomic.Int32
	heroSMSSMSIdempotencyMissHook = func() {
		if idempotencyMisses.Add(1) == 2 {
			close(secondIdempotencyMiss)
		}
	}
	defer func() { heroSMSSMSIdempotencyMissHook = nil }()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "1", 2) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":2}}}`))
		case "getActiveActivations":
			activeOnce.Do(func() {
				close(activeStarted)
				<-releaseActive
			})
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			purchaseCalls.Add(1)
			_, _ = writer.Write([]byte(`{"activationId":912,"phoneNumber":"79001112233","activationCost":1,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`))
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	type result struct {
		order *HeroSMSSMSOrderView
		err   error
	}
	results := make(chan result, 2)
	purchase := func() {
		order, _, _, purchaseErr := CreateHeroSMSSMSOrder(
			t.Context(),
			804,
			HeroSMSSMSPurchaseRequest{OfferID: offer.ID},
			"sms-concurrent-idempotency",
		)
		results <- result{order: order, err: purchaseErr}
	}
	go purchase()
	<-activeStarted
	go purchase()
	<-secondIdempotencyMiss
	close(releaseActive)

	first := <-results
	second := <-results
	require.NoError(t, first.err)
	require.NoError(t, second.err)
	require.NotNil(t, first.order)
	require.NotNil(t, second.order)
	require.Equal(t, first.order.ID, second.order.ID)
	require.EqualValues(t, 1, purchaseCalls.Load())

	current, err := ListCurrentHeroSMSSMSOrders(t.Context(), 804)
	require.NoError(t, err)
	require.Len(t, current, 1)
	require.True(t, current[0].CanCancel)
	require.Nil(t, current[0].ProviderID)
}

func TestHeroSMSSMSCatalogRanksBySuccessfulPurchases(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	createHeroSMSTestUser(t, db, 803, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Query().Get("action") {
		case "getCountries":
			_, _ = writer.Write([]byte(`{
				"1":{"rus":"Альфа","eng":"Alpha","chn":"阿尔法","visible":1},
				"2":{"rus":"Зулу","eng":"Zulu","chn":"祖鲁","visible":1},
				"3":{"rus":"Янки","eng":"Yankee","chn":"扬基","visible":1}
			}`))
		case "getServicesList":
			_, _ = writer.Write([]byte(`{"aa":"Alpha Service","mm":"Middle Service","zz":"Zulu Service"}`))
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

	orders := []HeroSMSSMSOrder{
		{ID: "popular-1", UserID: 803, IdempotencyKeyHash: "popular-key-1", RequestPayloadHash: "request-1", CountryID: 2, Service: "zz", Status: HeroSMSSMSOrderStatusActive, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1"},
		{ID: "popular-2", UserID: 803, IdempotencyKeyHash: "popular-key-2", RequestPayloadHash: "request-2", CountryID: 2, Service: "zz", Status: HeroSMSSMSOrderStatusCompleted, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1"},
		{ID: "popular-3", UserID: 803, IdempotencyKeyHash: "popular-key-3", RequestPayloadHash: "request-3", CountryID: 1, Service: "aa", Status: HeroSMSSMSOrderStatusActive, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1"},
		{ID: "ignored-failure", UserID: 803, IdempotencyKeyHash: "popular-key-4", RequestPayloadHash: "request-4", CountryID: 1, Service: "aa", Status: HeroSMSSMSOrderStatusFailed, PriceMultiplier: "1", ProviderPriceCNY: "1", CustomerPriceUSD: "1"},
	}
	require.NoError(t, db.Create(&orders).Error)

	countries, err := GetHeroSMSSMSCountries(t.Context(), "")
	require.NoError(t, err)
	require.Len(t, countries, 3)
	require.Equal(t, 2, countries[0].ID)
	require.EqualValues(t, 2, countries[0].Popularity)
	require.Equal(t, "Zulu", countries[0].EnglishName)
	require.Equal(t, "祖鲁", countries[0].ChineseName)
	require.Equal(t, 1, countries[1].ID)
	require.EqualValues(t, 1, countries[1].Popularity)
	require.Equal(t, 3, countries[2].ID)
	require.Zero(t, countries[2].Popularity)

	filteredCountries, err := GetHeroSMSSMSCountries(t.Context(), "aa")
	require.NoError(t, err)
	require.Len(t, filteredCountries, 3)
	require.Equal(t, 1, filteredCountries[0].ID)
	require.EqualValues(t, 1, filteredCountries[0].Popularity)
	require.Zero(t, filteredCountries[1].Popularity)

	services, err := GetHeroSMSSMSServices(t.Context())
	require.NoError(t, err)
	require.Len(t, services, 3)
	require.Equal(t, "zz", services[0].Code)
	require.EqualValues(t, 2, services[0].Popularity)
	require.Equal(t, "aa", services[1].Code)
	require.EqualValues(t, 1, services[1].Popularity)
	require.Equal(t, "mm", services[2].Code)
	require.Zero(t, services[2].Popularity)
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

	var cancelAccepted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "1", 1) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":1}}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			_, _ = writer.Write([]byte(`{"activationId":910,"phoneNumber":"79007654321","activationCost":0.5,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`))
		case "getStatus":
			if cancelAccepted.Load() {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
			} else {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
			}
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		case "setStatus":
			cancelAccepted.Store(true)
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, "sms-cancel-1")
	require.NoError(t, err)
	pending, quota, err := CancelHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelPending, pending.Status)
	require.Equal(t, 250_000, pending.ChargeQuota)
	require.Equal(t, 250_000, pending.RefundedQuota)
	require.Less(t, quota, user.Quota)

	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	cancelled, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, cancelled.Status)
	require.Equal(t, 500_000, cancelled.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}

func TestHeroSMSSMSCancellationCannotRefundCompletedRace(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 805, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	refreshStatusStarted := make(chan struct{})
	releaseRefreshStatus := make(chan struct{})
	var statusCalls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "1", 1) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":1}}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			_, _ = writer.Write([]byte(`{"activationId":913,"phoneNumber":"79002223344","activationCost":0.5,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`))
		case "getStatus":
			_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
		case "getStatusV2":
			if statusCalls.Add(1) == 1 {
				close(refreshStatusStarted)
				<-releaseRefreshStatus
				_, _ = writer.Write([]byte(`{"sms":{"code":"654321","text":"Code: 654321"}}`))
				return
			}
			_, _ = writer.Write([]byte(`{"sms":null}`))
		case "setStatus":
			_, _ = writer.Write([]byte("ACCESS_CANCEL"))
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, "sms-cancel-race")
	require.NoError(t, err)

	type refreshResult struct {
		order *HeroSMSSMSOrderView
		err   error
	}
	refreshResults := make(chan refreshResult, 1)
	go func() {
		refreshed, refreshErr := RefreshHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
		refreshResults <- refreshResult{order: refreshed, err: refreshErr}
	}()
	<-refreshStatusStarted
	type cancelResult struct {
		order *HeroSMSSMSOrderView
		quota int
		err   error
	}
	cancelResults := make(chan cancelResult, 1)
	go func() {
		cancelled, quota, cancelErr := CancelHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
		cancelResults <- cancelResult{order: cancelled, quota: quota, err: cancelErr}
	}()
	close(releaseRefreshStatus)
	refreshed := <-refreshResults
	cancelled := <-cancelResults
	require.NoError(t, refreshed.err)
	require.NoError(t, cancelled.err)

	require.Equal(t, HeroSMSSMSOrderStatusCompleted, cancelled.order.Status)
	require.Equal(t, HeroSMSSMSOrderStatusCompleted, refreshed.order.Status)
	require.Equal(t, "654321", refreshed.order.Code)
	require.Equal(t, 250_000, cancelled.order.RefundedQuota)
	require.Equal(t, user.Quota-cancelled.order.ChargeQuota, cancelled.quota)
}

func TestHeroSMSSMSCancellationPendingRecoversOnlyAfterUpstreamConfirmation(t *testing.T) {
	db := setupHeroSMSTestDB(t)
	user := createHeroSMSTestUser(t, db, 806, 1_000_000)
	require.NoError(t, UpdateHeroSMSSettings(HeroSMSSettingsUpdate{
		Enabled:         ptrBool(true),
		SMSEnabled:      ptrBool(true),
		APIKey:          "test-secret-key-12345",
		PriceMultiplier: "1",
	}))

	var cancelCalls atomic.Int32
	var cancelAccepted atomic.Bool
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if writeHeroSMSTestOffer(writer, request, "1", 1) {
			return
		}
		switch request.URL.Query().Get("action") {
		case "getPrices":
			_, _ = writer.Write([]byte(`{"6":{"tg":{"cost":1,"count":1}}}`))
		case "getActiveActivations":
			_, _ = writer.Write([]byte(`{"data":[]}`))
		case "getNumberV2":
			_, _ = writer.Write([]byte(`{"activationId":914,"phoneNumber":"79003334455","activationCost":0.5,"currencyCode":840,"countryCode":6,"canGetAnotherSms":false}`))
		case "getStatus":
			if cancelAccepted.Load() {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateCancel))
			} else {
				_, _ = writer.Write([]byte(herosms.SMSActivationStateWaiting))
			}
		case "getStatusV2":
			_, _ = writer.Write([]byte(`{"sms":null}`))
		case "setStatus":
			if cancelCalls.Add(1) == 1 {
				http.Error(writer, "temporary provider failure", http.StatusBadGateway)
				return
			}
			cancelAccepted.Store(true)
			_, _ = writer.Write([]byte("ACCESS_CANCEL"))
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

	offer, err := GetHeroSMSSMSOffer(t.Context(), user.Id, 6, "tg", "")
	require.NoError(t, err)
	order, _, _, err := CreateHeroSMSSMSOrder(t.Context(), user.Id, HeroSMSSMSPurchaseRequest{OfferID: offer.ID}, "sms-cancel-recovery")
	require.NoError(t, err)

	_, _, err = CancelHeroSMSSMSOrder(t.Context(), user.Id, order.ID)
	require.Error(t, err)
	pending, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelPending, pending.Status)

	processed, err := RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	stillPending, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelPending, stillPending.Status)
	require.Equal(t, 250_000, stillPending.RefundedQuota)

	processed, err = RunHeroSMSSMSReconciliationOnce(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, 1, processed)
	cancelled, err := GetHeroSMSSMSOrder(order.ID, user.Id)
	require.NoError(t, err)
	require.Equal(t, HeroSMSSMSOrderStatusCancelled, cancelled.Status)
	require.Positive(t, cancelled.RefundedQuota)
	require.Equal(t, user.Quota, getUserQuotaValue(user.Id))
}
