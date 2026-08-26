package herosms

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSMSPriceValueBounds(t *testing.T) {
	for _, value := range []string{"", "0", "-1", "1e1000000", "1000001", "1.2.3"} {
		_, valid := parseSMSPriceValue(value)
		require.False(t, valid, value)
	}
	for _, value := range []string{".5", "1", "0.0001", "1000000"} {
		price, valid := parseSMSPriceValue(value)
		require.True(t, valid, value)
		require.True(t, price.GreaterThan(decimal.Zero))
	}
}

func TestSMSClientCatalogQuoteAndPurchase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v1/activations/offers/sms" {
			require.Empty(t, r.URL.Query().Get("api_key"))
			require.Equal(t, "ApiKey test-key", r.Header.Get("Authorization"))
			require.Equal(t, "6", r.URL.Query().Get("countries"))
			require.Equal(t, "tg", r.URL.Query().Get("services"))
			_, _ = w.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":12,"defaultPrice":8},"prices":{"default":1.25},"map":{"1.25":8,"0.9":4,"0.900":2}}}}}`))
			return
		}

		require.Equal(t, "/stubs/handler_api.php", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("api_key"))
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"6":{"rus":"Россия","eng":"Russia","chn":"俄罗斯","visible":1}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"tg":"Telegram","go":"Google"}`))
		case "getOperators":
			require.Equal(t, "6", r.URL.Query().Get("country"))
			_, _ = w.Write([]byte(`{"status":"success","countryOperators":{"6":["tele2","mts","MTS","any","  "]}}`))
		case "getNumberV2":
			require.Equal(t, "840", r.URL.Query().Get("currency"))
			require.Empty(t, r.URL.Query().Get("fixedPrice"))
			switch r.URL.Query().Get("maxPrice") {
			case "1.25":
			case "1.5":
			default:
				t.Fatalf("unexpected maxPrice %q", r.URL.Query().Get("maxPrice"))
			}
			_, _ = w.Write([]byte(`{"activationId":12345,"phoneNumber":"79001234567","activationCost":1.25,"currency":840,"countryCode":6,"canGetAnotherSms":true,"activationTime":"2026-02-18T16:11:33+00:00","activationEndTime":"2026-02-18T18:11:23+00:00","activationOperator":"any"}`))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	countries, err := client.ListSMSCountries(context.Background())
	require.NoError(t, err)
	require.Equal(t, []SMSCountry{{
		ID:          6,
		Name:        "俄罗斯",
		EnglishName: "Russia",
		ChineseName: "俄罗斯",
		Visible:     true,
	}}, countries)

	services, err := client.ListSMSServices(context.Background())
	require.NoError(t, err)
	require.ElementsMatch(t, []SMSService{{Code: "tg", Name: "Telegram"}, {Code: "go", Name: "Google"}}, services)

	operators, err := client.ListSMSOperators(context.Background(), 6)
	require.NoError(t, err)
	require.Equal(t, []string{"mts", "tele2"}, operators)

	offer, err := client.GetSMSOffer(context.Background(), 6, "tg")
	require.NoError(t, err)
	require.Equal(t, "0.9", offer.PriceValue)
	require.Equal(t, 4, offer.Count)
	require.Equal(t, []SMSPriceTier{
		{Count: 4, Price: decimal.RequireFromString("0.9"), PriceValue: "0.9"},
		{Count: 8, Price: decimal.RequireFromString("1.25"), PriceValue: "1.25"},
	}, offer.Tiers)

	activation, err := client.PurchaseSMSActivation(context.Background(), SMSPurchaseRequest{
		CountryID:    6,
		Service:      "tg",
		Operator:     "any",
		MaxPrice:     decimal.RequireFromString("1.25"),
		CurrencyCode: 840,
	})
	require.NoError(t, err)
	require.Equal(t, "12345", activation.ID)
	require.Equal(t, "79001234567", activation.PhoneNumber)
	require.Equal(t, "1.25", activation.CostValue)
	require.Equal(t, 840, activation.CurrencyCode)
	require.Equal(t, "2026-02-18T18:11:23+00:00", activation.ActivationEndTime)
	require.Equal(t, "any", activation.ActivationOperator)

	_, err = client.PurchaseSMSActivation(context.Background(), SMSPurchaseRequest{
		CountryID:    6,
		Service:      "tg",
		MaxPrice:     decimal.RequireFromString("1.5"),
		CurrencyCode: 840,
	})
	require.NoError(t, err)
}

func TestSMSClientServicesAcceptsHeroSMSEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "getServicesList", r.URL.Query().Get("action"))
		_, _ = w.Write([]byte(`{"status":"success","services":[{"code":" tg ","name":" Telegram "},{"code":"","name":"ignored"}]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	services, err := client.ListSMSServices(context.Background())
	require.NoError(t, err)
	require.Equal(t, []SMSService{{Code: "tg", Name: "Telegram"}}, services)
}

func TestSMSClientServicesRejectsFailedHeroSMSEnvelope(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"error","services":[]}`))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	_, err := client.ListSMSServices(context.Background())
	require.ErrorIs(t, err, ErrBadResponse)
}

func TestSMSClientStatusAndStatusUpdate(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Query().Get("action") {
		case "getStatusV2":
			require.Equal(t, "12345", r.URL.Query().Get("id"))
			_, _ = w.Write([]byte(`{"sms":{"code":"8675309","text":"Your code is 8675309"}}`))
		case "getStatus":
			require.Equal(t, "12345", r.URL.Query().Get("id"))
			_, _ = w.Write([]byte(SMSActivationStateCancel))
		case "setStatus":
			require.Equal(t, "8", r.URL.Query().Get("status"))
			_, _ = w.Write([]byte("ACCESS_CANCEL"))
		default:
			http.Error(w, "unexpected action", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	status, err := client.GetSMSActivationStatus(context.Background(), "12345")
	require.NoError(t, err)
	require.Equal(t, "8675309", status.Code)
	require.Equal(t, "Your code is 8675309", status.Text)
	state, err := client.GetSMSActivationState(context.Background(), "12345")
	require.NoError(t, err)
	require.Equal(t, SMSActivationStateCancel, state)
	require.NoError(t, client.SetSMSActivationStatus(context.Background(), "12345", 8))
}

func TestSMSClientNormalizesCompletedActivationState(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, "getStatus", request.URL.Query().Get("action"))
		_, _ = w.Write([]byte("STATUS_OK:8675309"))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	state, err := client.GetSMSActivationState(t.Context(), "12345")
	require.NoError(t, err)
	require.Equal(t, SMSActivationStateOK, state)
}

func TestSMSClientSubmitsOfficialComplaintReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		require.Equal(t, http.MethodPost, request.Method)
		require.Equal(t, "/api/v1/complaints/activations/12345", request.URL.Path)
		require.Equal(t, "ApiKey test-key", request.Header.Get("Authorization"))
		require.Equal(t, "test-key", request.Header.Get("ApiKey"))
		var payload map[string]string
		require.NoError(t, json.NewDecoder(request.Body).Decode(&payload))
		require.Equal(t, map[string]string{"type": "SMS_NOT_RECEIVED"}, payload)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	require.NoError(t, client.SubmitSMSActivationComplaint(t.Context(), "12345", "SMS_NOT_RECEIVED"))
	require.ErrorIs(t, client.SubmitSMSActivationComplaint(t.Context(), "12345", "free text"), ErrInvalidRequest)
}

func TestSMSClientFallsBackWhenTierMapIsEmpty(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/api/v1/activations/offers/sms":
			_, _ = w.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":3,"defaultPrice":3},"prices":{"default":0.4,"min":0.4,"retail":0.7},"map":{}}}}}`))
		case "/stubs/handler_api.php":
			_, _ = w.Write([]byte(`OPERATORS_NOT_FOUND`))
		default:
			http.NotFound(w, request)
		}
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")

	offer, err := client.GetSMSOffer(t.Context(), 6, "tg")
	require.NoError(t, err)
	require.Equal(t, "0.4", offer.PriceValue)
	require.Equal(t, 3, offer.Count)
	require.Len(t, offer.Tiers, 1)

	operators, err := client.ListSMSOperators(t.Context(), 6)
	require.NoError(t, err)
	require.Empty(t, operators)
}

func TestSMSClientRejectsNonMonotonicPriceAvailability(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"tg":{"6":{"counts":{"total":5,"defaultPrice":2},"prices":{"default":1},"map":{"0.5":4,"1":2}}}}}`))
	}))
	defer server.Close()

	client := NewClient(server.URL, "test-key")
	_, err := client.GetSMSOffer(t.Context(), 6, "tg")
	require.ErrorIs(t, err, ErrBadResponse)
}

func TestSMSClientRequiresStatusSpecificAccessResponse(t *testing.T) {
	response := "ACCESS_ACTIVATION"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()
	client := NewClient(server.URL, "test-key")

	err := client.SetSMSActivationStatus(t.Context(), "123", 8)
	require.ErrorIs(t, err, ErrBadResponse)

	response = "ACCESS_CANCEL"
	require.NoError(t, client.SetSMSActivationStatus(t.Context(), "123", 8))
}

func TestSMSClientMapsLegacyErrors(t *testing.T) {
	response := "NO_NUMBERS"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/activations/offers/sms" {
			_, _ = w.Write([]byte(`{"data":{}}`))
			return
		}
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	_, err := client.GetSMSOffer(context.Background(), 6, "tg")
	require.True(t, errors.Is(err, ErrNoSMSNumbersAvailable))

	response = "NO_BALANCE"
	_, err = client.PurchaseSMSActivation(context.Background(), SMSPurchaseRequest{
		CountryID:    6,
		Service:      "tg",
		MaxPrice:     decimal.NewFromInt(1),
		CurrencyCode: 840,
	})
	require.True(t, errors.Is(err, ErrProviderBalanceInsufficient))
}
