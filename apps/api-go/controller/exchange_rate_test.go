/*
Copyright (C) 2023-2026 QuantumNous

This program is free software: you can redistribute it and/or modify
it under the terms of the GNU Affero General Public License as published by
the Free Software Foundation, either version 3 of the License, or
(at your option) any later version.

This program is distributed in the hope that it will be useful,
but WITHOUT ANY WARRANTY; without even the implied warranty of
MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
GNU Affero General Public License for more details.

You should have received a copy of the GNU Affero General Public License
along with this program. If not, see <https://www.gnu.org/licenses/>.

For commercial licensing, please contact support@quantumnous.com
*/
package controller

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type exchangeRateRoundTripper func(*http.Request) (*http.Response, error)

func (roundTripper exchangeRateRoundTripper) RoundTrip(
	request *http.Request,
) (*http.Response, error) {
	return roundTripper(request)
}

func TestGetUsdExchangeRateRejectsInvalidCurrency(t *testing.T) {
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/?currency=CN", nil)

	GetUsdExchangeRate(c)

	require.Equal(t, http.StatusBadRequest, w.Code)
	require.JSONEq(t, `{"success":false,"message":"currency must be a three-letter ISO code"}`, w.Body.String())
}

func TestFetchUsdExchangeRateUsesProviderCurrency(t *testing.T) {
	previousClient := exchangeRateHTTPClient
	exchangeRateHTTPClient = &http.Client{
		Transport: exchangeRateRoundTripper(func(request *http.Request) (*http.Response, error) {
			require.Equal(t, "CNY", request.URL.Query().Get("to"))
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader(`{"rates":{"CNY":6.8}}`)),
				Header:     make(http.Header),
			}, nil
		}),
	}
	t.Cleanup(func() { exchangeRateHTTPClient = previousClient })

	rate, err := fetchUsdExchangeRate(context.Background(), "CNY")
	require.NoError(t, err)
	require.Equal(t, "frankfurter.app", rate.Provider)
	require.InDelta(t, 6.8, rate.Rate, 0.000001)
}

func TestFetchUsdExchangeRateReturnsOneForUsd(t *testing.T) {
	rate, err := fetchUsdExchangeRate(context.Background(), "USD")
	require.NoError(t, err)
	require.Equal(t, exchangeRateProviderResponse{Rate: 1, Provider: "base"}, rate)
}
