package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func TestListWaffoPancakeCatalogUsesRootProductQuery(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	var storeQuerySeen, productQuerySeen bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		require.NoError(t, json.NewDecoder(r.Body).Decode(&request))
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(request.Query, "stores {"):
			storeQuerySeen = true
			require.NotContains(t, request.Query, "onetimeProducts")
			_, err = w.Write([]byte(`{"data":{"stores":[{"id":"STO_AbCdEfGhIjKlMnOpQrStUv","name":"main","status":"active","prodEnabled":true}]}}`))
			require.NoError(t, err)
		case strings.Contains(request.Query, "onetimeProducts(storeId: $storeId, filter: { status: { eq: \"active\" } })"):
			productQuerySeen = true
			require.Contains(t, request.Query, "subscriptionProducts(storeId: $storeId")
			require.Contains(t, request.Query, "billingPeriod")
			require.NotContains(t, request.Query, "storeId: { eq:")
			require.Equal(t, "STO_AbCdEfGhIjKlMnOpQrStUv", request.Variables["storeId"])
			_, err = w.Write([]byte(`{"data":{"onetimeProducts":[{"id":"PROD_AbCdEfGhIjKlMnOpQrStUv","name":"wallet","status":"active"},{"id":"PROD_Inactive0000000000000000","name":"old","status":"inactive"}],"subscriptionProducts":[{"id":"PROD_Subscription000000000001","name":"monthly","status":"active","billingPeriod":"monthly"},{"id":"PROD_SubscriptionInactive000002","name":"legacy","status":"inactive","billingPeriod":"monthly"}]}}`))
			require.NoError(t, err)
		default:
			http.Error(w, "unexpected GraphQL query", http.StatusBadRequest)
		}
	}))
	defer server.Close()

	client, err := pancake.New(pancake.Config{
		MerchantID: "MER_AbCdEfGhIjKlMnOpQrStUv",
		PrivateKey: string(privateKeyPEM),
		BaseURL:    server.URL,
		HTTPClient: server.Client(),
	})
	require.NoError(t, err)

	catalog, err := listWaffoPancakeCatalogWithClient(context.Background(), client)
	require.NoError(t, err)
	require.True(t, storeQuerySeen)
	require.True(t, productQuerySeen)
	require.Len(t, catalog.Stores, 1)
	require.Len(t, catalog.Stores[0].OnetimeProducts, 1)
	require.Equal(t, "PROD_AbCdEfGhIjKlMnOpQrStUv", catalog.Stores[0].OnetimeProducts[0].ID)
	require.Len(t, catalog.Stores[0].SubscriptionProducts, 1)
	require.Equal(t, "PROD_Subscription000000000001", catalog.Stores[0].SubscriptionProducts[0].ID)
	require.Equal(t, "monthly", catalog.Stores[0].SubscriptionProducts[0].BillingPeriod)
	require.True(t, WaffoPancakeCatalogHasActiveSubscriptionProduct(
		catalog,
		"STO_AbCdEfGhIjKlMnOpQrStUv",
		"PROD_Subscription000000000001",
	))
	require.False(t, WaffoPancakeCatalogHasActiveSubscriptionProduct(
		catalog,
		"STO_AbCdEfGhIjKlMnOpQrStUv",
		"PROD_AbCdEfGhIjKlMnOpQrStUv", // one-time product
	))
	require.True(t, WaffoPancakeCatalogHasActiveOneTimeProduct(
		catalog,
		"STO_AbCdEfGhIjKlMnOpQrStUv",
		"PROD_AbCdEfGhIjKlMnOpQrStUv",
	))
	require.False(t, WaffoPancakeCatalogHasActiveOneTimeProduct(
		catalog,
		"STO_AbCdEfGhIjKlMnOpQrStUv",
		"PROD_Subscription000000000001",
	))
}
