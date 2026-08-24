package herosms

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestSMSClientCatalogQuoteAndPurchase(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/stubs/handler_api.php", r.URL.Path)
		require.Equal(t, "test-key", r.URL.Query().Get("api_key"))
		switch r.URL.Query().Get("action") {
		case "getCountries":
			_, _ = w.Write([]byte(`{"6":{"rus":"Россия","eng":"Russia","chn":"俄罗斯","visible":1}}`))
		case "getServicesList":
			_, _ = w.Write([]byte(`{"tg":"Telegram","go":"Google"}`))
		case "getPrices":
			require.Equal(t, "6", r.URL.Query().Get("country"))
			require.Equal(t, "tg", r.URL.Query().Get("service"))
			_, _ = w.Write([]byte(`{"6":{"tg":{"cost":1.25,"count":12}}}`))
		case "getNumberV2":
			require.Equal(t, "1.25", r.URL.Query().Get("maxPrice"))
			require.Equal(t, "true", r.URL.Query().Get("fixedPrice"))
			_, _ = w.Write([]byte(`{"activationId":12345,"phoneNumber":"79001234567","activationCost":1.25,"currencyCode":643,"countryCode":6,"canGetAnotherSms":true,"activationTime":"2026-02-18T16:11:33+00:00","activationEndTime":"2026-02-18T18:11:23+00:00","activationOperator":"any"}`))
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

	offer, err := client.GetSMSOffer(context.Background(), 6, "tg")
	require.NoError(t, err)
	require.Equal(t, "1.25", offer.PriceValue)
	require.Equal(t, 12, offer.Count)

	activation, err := client.PurchaseSMSActivation(context.Background(), SMSPurchaseRequest{
		CountryID: 6,
		Service:   "tg",
		Operator:  "any",
		MaxPrice:  decimal.RequireFromString("1.25"),
	})
	require.NoError(t, err)
	require.Equal(t, "12345", activation.ID)
	require.Equal(t, "79001234567", activation.PhoneNumber)
	require.Equal(t, "1.25", activation.CostValue)
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
	require.NoError(t, client.SetSMSActivationStatus(context.Background(), "12345", 8))
}

func TestSMSClientMapsLegacyErrors(t *testing.T) {
	response := "NO_NUMBERS"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(response))
	}))
	defer server.Close()

	client := NewClient(server.URL+"/api/v1", "test-key")
	_, err := client.GetSMSOffer(context.Background(), 6, "tg")
	require.True(t, errors.Is(err, ErrNoSMSNumbersAvailable))

	response = "NO_BALANCE"
	_, err = client.PurchaseSMSActivation(context.Background(), SMSPurchaseRequest{
		CountryID: 6,
		Service:   "tg",
		MaxPrice:  decimal.NewFromInt(1),
	})
	require.True(t, errors.Is(err, ErrProviderBalanceInsufficient))
}
