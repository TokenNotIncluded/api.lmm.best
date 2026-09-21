package service

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

func TestWaffoPancakeBillingPeriodForDuration(t *testing.T) {
	tests := []struct {
		unit      string
		value     int
		want      pancake.BillingPeriod
		wantError bool
	}{
		{unit: "day", value: 7, want: pancake.BillingPeriodWeekly},
		{unit: "month", value: 1, want: pancake.BillingPeriodMonthly},
		{unit: "month", value: 3, want: pancake.BillingPeriodQuarterly},
		{unit: "month", value: 12, want: pancake.BillingPeriodYearly},
		{unit: "year", value: 1, want: pancake.BillingPeriodYearly},
		{unit: "day", value: 30, wantError: true},
		{unit: "month", value: 6, wantError: true},
		{unit: "custom", value: 1, wantError: true},
	}

	for _, tt := range tests {
		period, err := WaffoPancakeBillingPeriodForDuration(tt.unit, tt.value)
		if tt.wantError {
			require.Error(t, err)
			continue
		}
		require.NoError(t, err)
		require.Equal(t, tt.want, period)
	}
}

func TestEnsureWaffoPancakeProductPublishedHandlesKeyEnvironment(t *testing.T) {
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKCS8PrivateKey(privateKey)
	require.NoError(t, err)
	privateKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})

	const productID = "PROD_AbCdEfGhIjKlMnOpQrStUv"
	tests := []struct {
		name               string
		publicationStates  []bool
		publishError       bool
		publishStatus      string
		wantErrorContains  string
		wantPublishCalls   int
		wantGraphQLQueries int
	}{
		{
			name:               "production key product is already live",
			publicationStates:  []bool{true},
			wantPublishCalls:   0,
			wantGraphQLQueries: 1,
		},
		{
			name:               "test key product is published",
			publicationStates:  []bool{false},
			wantPublishCalls:   1,
			wantGraphQLQueries: 1,
		},
		{
			name:               "lost publish response is reconciled",
			publicationStates:  []bool{false, true},
			publishError:       true,
			wantPublishCalls:   1,
			wantGraphQLQueries: 2,
		},
		{
			name:               "unpublished product preserves provider error",
			publicationStates:  []bool{false, false},
			publishError:       true,
			wantErrorContains:  "No test version found",
			wantPublishCalls:   1,
			wantGraphQLQueries: 2,
		},
		{
			name:               "inactive publish response is rejected",
			publicationStates:  []bool{false},
			publishStatus:      "inactive",
			wantErrorContains:  "not active",
			wantPublishCalls:   1,
			wantGraphQLQueries: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var graphQLQueries, publishCalls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				switch r.URL.Path {
				case "/v1/graphql":
					var request struct {
						Query     string         `json:"query"`
						Variables map[string]any `json:"variables"`
					}
					if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
						http.Error(w, "invalid request", http.StatusBadRequest)
						return
					}
					if !strings.Contains(request.Query, "query ($id: String!)") || request.Variables["id"] != productID {
						http.Error(w, "unexpected publication query", http.StatusBadRequest)
						return
					}
					stateIndex := int(graphQLQueries.Add(1) - 1)
					if stateIndex >= len(tt.publicationStates) {
						stateIndex = len(tt.publicationStates) - 1
					}
					if _, err := fmt.Fprintf(w, `{"data":{"onetimeProduct":{"id":%q,"status":"active","hasProdVersion":%t}}}`, productID, tt.publicationStates[stateIndex]); err != nil {
						t.Errorf("write GraphQL response: %v", err)
					}
				case "/v1/actions/onetime-product/publish-product":
					publishCalls.Add(1)
					if tt.publishError {
						w.WriteHeader(http.StatusBadRequest)
						if _, err := w.Write([]byte(`{"errors":[{"message":"No test version found","layer":"application"}]}`)); err != nil {
							t.Errorf("write publish error: %v", err)
						}
						return
					}
					publishStatus := tt.publishStatus
					if publishStatus == "" {
						publishStatus = "active"
					}
					if _, err := fmt.Fprintf(w, `{"data":{"product":{"id":%q,"status":%q}}}`, productID, publishStatus); err != nil {
						t.Errorf("write publish response: %v", err)
					}
				default:
					http.Error(w, "unexpected path", http.StatusNotFound)
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

			err = ensureWaffoPancakeProductPublished(t.Context(), client, productID)
			if tt.wantErrorContains != "" {
				require.ErrorContains(t, err, tt.wantErrorContains)
			} else {
				require.NoError(t, err)
			}
			require.Equal(t, int32(tt.wantPublishCalls), publishCalls.Load())
			require.Equal(t, int32(tt.wantGraphQLQueries), graphQLQueries.Load())
		})
	}
}
