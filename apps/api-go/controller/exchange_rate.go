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
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/LIghtJUNction/api.lmm.best/setting/operation_setting"
	"github.com/gin-gonic/gin"
)

const (
	exchangeRateResponseMaxBytes int64 = 64 << 10
	exchangeRateRequestTimeout         = 5 * time.Second
)

var exchangeRateHTTPClient = &http.Client{
	Timeout: exchangeRateRequestTimeout,
	CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

type exchangeRateProviderResponse struct {
	Rate     float64
	Provider string
}

type frankfurterExchangeRateResponse struct {
	Rates map[string]float64 `json:"rates"`
}

type openExchangeRateResponse struct {
	Result string             `json:"result"`
	Rates  map[string]float64 `json:"rates"`
}

// GetUsdExchangeRate returns the latest fiat rate for one USD in the requested
// ISO-4217 currency. It intentionally lives behind the root-authenticated
// option router: the browser must never call a third-party rate service
// directly, and the server controls the timeout and response size.
func GetUsdExchangeRate(c *gin.Context) {
	currency := strings.ToUpper(strings.TrimSpace(c.Query("currency")))
	if err := operation_setting.ValidateCustomCurrencyCode(currency); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"success": false,
			"message": "currency must be a three-letter ISO code",
		})
		return
	}

	rate, err := fetchUsdExchangeRate(c.Request.Context(), currency)
	if err != nil {
		c.JSON(http.StatusBadGateway, gin.H{
			"success": false,
			"message": "failed to fetch the latest USD exchange rate",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "",
		"data": gin.H{
			"base_currency":  "USD",
			"quote_currency": currency,
			"rate":           rate.Rate,
			"fetched_at":     time.Now().UTC().Format(time.RFC3339),
			"provider":       rate.Provider,
		},
	})
}

func fetchUsdExchangeRate(ctx context.Context, currency string) (exchangeRateProviderResponse, error) {
	currency = strings.ToUpper(strings.TrimSpace(currency))
	if err := operation_setting.ValidateCustomCurrencyCode(currency); err != nil {
		return exchangeRateProviderResponse{}, err
	}
	if currency == "USD" {
		return exchangeRateProviderResponse{Rate: 1, Provider: "base"}, nil
	}

	providers := []struct {
		name string
		url  string
		load func([]byte) (float64, error)
	}{
		{
			name: "frankfurter.app",
			url:  "https://api.frankfurter.app/latest?from=USD&to=" + currency,
			load: func(body []byte) (float64, error) {
				var response frankfurterExchangeRateResponse
				if err := json.Unmarshal(body, &response); err != nil {
					return 0, err
				}
				return response.Rates[currency], nil
			},
		},
		{
			name: "open.er-api.com",
			url:  "https://open.er-api.com/v6/latest/USD",
			load: func(body []byte) (float64, error) {
				var response openExchangeRateResponse
				if err := json.Unmarshal(body, &response); err != nil {
					return 0, err
				}
				return response.Rates[currency], nil
			},
		},
	}

	var lastErr error
	for _, provider := range providers {
		rate, err := requestExchangeRate(ctx, provider.url, provider.load)
		if err != nil {
			lastErr = fmt.Errorf("%s: %w", provider.name, err)
			continue
		}
		return exchangeRateProviderResponse{Rate: rate, Provider: provider.name}, nil
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no exchange-rate provider returned a rate")
	}
	return exchangeRateProviderResponse{}, lastErr
}

func requestExchangeRate(
	ctx context.Context,
	url string,
	decode func([]byte) (float64, error),
) (float64, error) {
	requestContext, cancel := context.WithTimeout(ctx, exchangeRateRequestTimeout)
	defer cancel()
	request, err := http.NewRequestWithContext(requestContext, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")

	client := *exchangeRateHTTPClient
	client.Timeout = exchangeRateRequestTimeout
	client.CheckRedirect = func(_ *http.Request, _ []*http.Request) error {
		return http.ErrUseLastResponse
	}
	response, err := client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()

	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return 0, fmt.Errorf("provider returned HTTP %d", response.StatusCode)
	}
	if response.ContentLength > exchangeRateResponseMaxBytes {
		return 0, fmt.Errorf("provider response exceeds %d bytes", exchangeRateResponseMaxBytes)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, exchangeRateResponseMaxBytes+1))
	if err != nil {
		return 0, err
	}
	if int64(len(body)) > exchangeRateResponseMaxBytes {
		return 0, fmt.Errorf("provider response exceeds %d bytes", exchangeRateResponseMaxBytes)
	}

	rate, err := decode(body)
	if err != nil {
		return 0, err
	}
	if rate <= 0 || math.IsNaN(rate) || math.IsInf(rate, 0) || rate > 1e9 {
		return 0, fmt.Errorf("provider returned an invalid rate")
	}
	return rate, nil
}
