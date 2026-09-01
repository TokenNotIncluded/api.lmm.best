package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	pancake "github.com/waffo-com/waffo-pancake-sdk-go"
)

var ErrWaffoPancakeTaxPreviewUnavailable = errors.New("Waffo Pancake tax preview unavailable")

var (
	waffoPancakeTaxPreviewBaseURL = pancake.DefaultBaseURL
	waffoPancakeTaxPreviewClient  = &http.Client{Timeout: 10 * time.Second}
)

type waffoPancakeTaxPreviewRequest struct {
	CheckoutSessionID string                    `json:"checkoutSessionId"`
	BillingDetail     WaffoPancakeBillingDetail `json:"billingDetail"`
}

type waffoPancakeTaxPreviewEnvelope struct {
	Data *struct {
		Rules *struct {
			RequiredFields []string `json:"requiredFields"`
		} `json:"rules"`
	} `json:"data"`
}

// PreviewWaffoPancakeTaxRules queries the provider-created checkout session.
// The session already carries the actual price snapshot and tax category. The
// response is deliberately reduced to field names; provider errors and the
// request's invoice identity are never returned or logged.
func PreviewWaffoPancakeTaxRules(
	ctx context.Context,
	session *WaffoPancakeCheckoutSession,
	billing WaffoPancakeBillingDetail,
) ([]string, error) {
	if session == nil || strings.TrimSpace(session.SessionID) == "" || strings.TrimSpace(session.Token) == "" {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	body, err := json.Marshal(waffoPancakeTaxPreviewRequest{
		CheckoutSessionID: session.SessionID,
		BillingDetail:     billing,
	})
	if err != nil {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	request, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		strings.TrimRight(waffoPancakeTaxPreviewBaseURL, "/")+"/api/v1/actions/checkout-session/preview-tax",
		bytes.NewReader(body),
	)
	if err != nil {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	request.Header.Set("Authorization", "Bearer "+session.Token)
	request.Header.Set("Content-Type", "application/json")
	// Existing application configuration is production-only; the provider
	// requires this explicit header for customer-session requests.
	request.Header.Set("X-Context-Environment", "prod")

	response, err := waffoPancakeTaxPreviewClient.Do(request)
	if err != nil {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	var envelope waffoPancakeTaxPreviewEnvelope
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64*1024))
	if err := decoder.Decode(&envelope); err != nil || envelope.Data == nil || envelope.Data.Rules == nil || envelope.Data.Rules.RequiredFields == nil {
		return nil, ErrWaffoPancakeTaxPreviewUnavailable
	}
	return append([]string(nil), envelope.Data.Rules.RequiredFields...), nil
}
