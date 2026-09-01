package service

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestPreviewWaffoPancakeTaxRulesUsesSessionAndReturnsAuthoritativeFields(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v1/actions/checkout-session/preview-tax", r.URL.Path)
		require.Equal(t, "Bearer session-token", r.Header.Get("Authorization"))
		require.Equal(t, "prod", r.Header.Get("X-Context-Environment"))
		var request struct {
			CheckoutSessionID string `json:"checkoutSessionId"`
			BillingDetail     struct {
				Country    string `json:"country"`
				IsBusiness bool   `json:"isBusiness"`
			} `json:"billingDetail"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		require.Equal(t, "session-41", request.CheckoutSessionID)
		require.Equal(t, "US", request.BillingDetail.Country)
		require.True(t, request.BillingDetail.IsBusiness)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"rules":{"requiredFields":["state","taxId"]}}}`))
	}))
	defer server.Close()

	originalBaseURL := waffoPancakeTaxPreviewBaseURL
	originalClient := waffoPancakeTaxPreviewClient
	waffoPancakeTaxPreviewBaseURL = server.URL
	waffoPancakeTaxPreviewClient = server.Client()
	t.Cleanup(func() {
		waffoPancakeTaxPreviewBaseURL = originalBaseURL
		waffoPancakeTaxPreviewClient = originalClient
	})

	rules, err := PreviewWaffoPancakeTaxRules(context.Background(), &WaffoPancakeCheckoutSession{
		SessionID: "session-41",
		Token:     "session-token",
	}, WaffoPancakeBillingDetail{Country: "US", IsBusiness: true})
	require.NoError(t, err)
	require.Equal(t, []string{"state", "taxId"}, rules)
}

func TestBuildWaffoPancakeCheckoutParamsIncludesSavedBillingDetail(t *testing.T) {
	params, err := buildWaffoPancakeSDKCheckoutParams(&WaffoPancakeCreateSessionParams{
		ProductID: "product", BuyerIdentity: "buyer", BillingDetail: &WaffoPancakeBillingDetail{
			Country: "US", IsBusiness: true, Postcode: "10001", State: "NY",
			BusinessName: "Example Company", TaxID: "TAX-41",
		},
	})
	require.NoError(t, err)
	require.NotNil(t, params.BillingDetail)
	require.Equal(t, "US", params.BillingDetail.Country)
	require.True(t, params.BillingDetail.IsBusiness)
	require.Equal(t, "10001", *params.BillingDetail.Postcode)
	require.Equal(t, "NY", *params.BillingDetail.State)
	require.Equal(t, "Example Company", *params.BillingDetail.BusinessName)
	require.Equal(t, "TAX-41", *params.BillingDetail.TaxID)
}

func TestPreviewWaffoPancakeTaxRulesFailsClosedWithoutRules(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"rules":{}}}`))
	}))
	defer server.Close()

	originalBaseURL := waffoPancakeTaxPreviewBaseURL
	originalClient := waffoPancakeTaxPreviewClient
	waffoPancakeTaxPreviewBaseURL = server.URL
	waffoPancakeTaxPreviewClient = server.Client()
	t.Cleanup(func() {
		waffoPancakeTaxPreviewBaseURL = originalBaseURL
		waffoPancakeTaxPreviewClient = originalClient
	})

	rules, err := PreviewWaffoPancakeTaxRules(context.Background(), &WaffoPancakeCheckoutSession{
		SessionID: "session-41", Token: "session-token",
	}, WaffoPancakeBillingDetail{Country: "US"})
	require.ErrorIs(t, err, ErrWaffoPancakeTaxPreviewUnavailable)
	require.Nil(t, rules)
}
